package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/executor"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/provider"
)

// newShipCmd is UBI-26's executor CLI surface: takes an already-accepted
// drift_revert proposal and actually executes it against live cloud --
// the one command in this codebase, alongside a future writeback --write,
// that changes real infrastructure rather than only ever reading or
// recording. Every mechanism it drives (the failure-state machine,
// idempotent re-run, freshness re-verification, redaction) is built and
// tested in core/executor; this command is a thin CLI wrapper over
// executor.Ship, in the same spirit cli/status.go and cli/accept.go wrap
// core's own primitives.
func newShipCmd() *cobra.Command {
	var (
		ledgerDir       string
		stack           string
		providerPath    string
		source          string
		providerVersion string
		providerConfig  string
		timeout         time.Duration
		jsonOut         bool
		confirmDestroys bool
	)

	cmd := &cobra.Command{
		Use:   "ship <hash>",
		Short: "Accept (local tier, if needed) and execute a drift_revert or change proposal against live cloud -- the only command that applies",
		Long: `Executes a drift_revert or change proposal: for a drift_revert, restores the
resource's live state to match the ledger's recorded truth; for a change (UBI-27, "ubx resolve"'s own
output, or "ubx plan"'s fused equivalent, UBI-49), creates and modifies resources for real, in real
dependency order, feeding each resource's real applied output into any sibling still carrying a
$computed marker pointing at it. This is the one ubx command that changes real infrastructure --
accept/why/status/scan/revert-plan/resolve/plan only ever read or record.

<hash> is looked up two ways, in order: first as an already-accepted proposal id in this stack's
ledger (the four-verb ceremony's own path -- "ubx accept" ran separately, including PR-merge
acceptance); if not found there, as a plan "ubx plan" saved at .ubx/plans/<hash>.json, which this
command then accepts inline, local tier, before applying -- ALL of local accept's own invariants
still apply exactly as they do standalone: --confirm-destroys is still required for any plan with
blast_radius.destroys > 0, a stale cross-stack pin still refuses (resolver.VerifyPins), and the
ledger records acceptance.method: "local" exactly like "ubx accept" would. PR-merge acceptance
remains available as its own separate path ("ubx accept --from-merge") for teams who want it --
inline acceptance here is local tier only, never a substitute for it.

Safe to re-run: ubx ship is idempotent by contract (docs/executor.md). A resource already applied in a
prior attempt is skipped -- including, for a change proposal, recovering its real applied output from
the ledger so a still-pending dependent can proceed correctly even after a crash between the two; a
resource left in an unresolved state (a crash, a timeout) is reconciled against live reality before
anything new is attempted where a lookup key exists; a resource whose restore target is itself a
redacted ($redacted) value is declined every time -- ubx never constructs a live apply from a salted
hash, use "ubx revert-plan" for that resource's manual reconciliation steps instead.

Freshness is re-verified for every modified resource, immediately before its own attempt -- not just
once at the start -- so reality moving mid-run is refused, never bulldozed. Only drift_revert and
change proposals can be shipped; every other kind is record-only (nothing to ship).`,
		Args: cobra.ExactArgs(1),
		// Exit code is the CI contract (docs/exit-codes.mdx): 0 applied (or
		// already fully applied -- a genuine no-op), 1 partially applied,
		// failed, or an inline-accept refusal (an actionable finding --
		// confirm destroys, resolve staleness, retry, or investigate why),
		// 2 a genuine error (bad input, provider/ledger failure).
		// SilenceUsage/Errors: same reasoning as every other UBI-20-audited
		// command (status.go).
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
			}
			defer closeLedger()

			p, err := ledger.Read(args[0])
			if err != nil {
				// UBI-49: not yet an accepted proposal in this stack's ledger
				// -- fall back to a plan "ubx plan" saved locally, and accept
				// it inline (local tier) before proceeding, subject to every
				// invariant standalone "ubx accept" already enforces.
				accepted, acceptErr := acceptPlanInline(ledger, ledgerDir, args[0], confirmDestroys)
				if acceptErr != nil {
					return &ExitCodeError{Code: acceptErrorCode(acceptErr), Err: fmt.Errorf("ship: %w", acceptErr)}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "accepted %s (stack %s) via local plan\n", accepted.ID, accepted.Stack)
				p = accepted
			}
			// A friendly, early exit before ever launching a provider --
			// executor.Ship enforces both of these authoritatively too
			// (ErrUnsupportedKind/ErrNotAccepted), this just avoids the
			// acquire/launch round trip for an obviously-wrong proposal.
			if p.Kind != core.KindDriftRevert && p.Kind != core.KindChange {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: proposal %s is kind %q -- ship only executes drift_revert or change proposals", p.ID, p.Kind)}
			}
			if p.Acceptance == nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: proposal %s is not accepted", p.ID)}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			salt, err := ledger.Salt()
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
			}

			// docs/executor.md's own "Amendment (UBI-43): multi-provider
			// stacks" client pool -- a stack with a real [providers] table
			// in .ubx/config gets a genuine multi-entry pool (cli/providerpool.go),
			// lazily launching whichever providers this specific proposal's
			// own nodes actually need; a single-provider stack (no table)
			// keeps working exactly as it always has, one provider launched
			// up front, wrapped in the trivial SingleApplierPool. Never both
			// at once -- see docs/resolver.md's own staged
			// --source/--provider-version retirement plan for what happens
			// when a table AND the singular flags are both given.
			var pool executor.ApplierPool
			if len(cfg.Providers) > 0 {
				warnIfLegacyProviderFlagsGiven(cmd)
				pp, err := newProviderPool(salt, cfg.Providers, cfg.ProviderConfigs)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
				defer pp.Close()
				pool = pp
			} else {
				applyProviderDefaults(cmd, &providerPath, &source, &providerVersion, cfg)
				if err := applyProviderConfigDefault(cmd, &providerConfig, cfg); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
				path, _, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
				client, err := provider.Launch(ctx, path)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
				defer client.Close()
				applier := newApplier(client.Provider, salt, source)
				pool = executor.SingleApplierPool(applier, json.RawMessage(providerConfig))
			}

			out := cmd.OutOrStdout()

			sealed, err := executor.Ship(ctx, ledger, pool, source, p)
			if errors.Is(err, executor.ErrAlreadyApplied) {
				return reportAlreadyApplied(out, ledger, p, jsonOut)
			}
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
			}

			if jsonOut {
				if err := writeJSON(out, shipJSON{Format: jsonFormatVersion, ApplyRecord: sealed}); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
			} else {
				printShipReport(out, sealed)
			}

			switch sealed.Summary.Outcome {
			case "applied":
				return nil
			default: // partially_applied, failed
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("ship: %s (see above)", sealed.Summary.Outcome)}
			}
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack's ledger to open -- required only when .ubx/config's [ledger] store is a remote store (a bare proposal id carries no stack of its own to derive it from); unused for the default git store")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source)")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "{}", "JSON object configuring the provider, e.g. {\"region\":\"us-east-1\"}")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "overall timeout for the ship run")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text")
	cmd.Flags().BoolVar(&confirmDestroys, "confirm-destroys", false, "required for inline local-tier acceptance of any plan with blast_radius.destroys > 0 (docs/schema.md); unused when <hash> is already an accepted proposal, since that confirmation already happened at its own accept time")

	return cmd
}

// acceptPlanInline is UBI-49's own ship-time fallback (docs/architecture.md's
// "Two-step fusion" amendment): hash isn't yet an accepted proposal in this
// stack's ledger, so read the plan "ubx plan" saved locally and run it
// through the exact same local-tier acceptance checks "ubx accept" already
// enforces standalone -- in the same order accept.go's own RunE does them
// (destroys confirmed as early as possible, pins re-verified unconditionally,
// then core.Accept itself) -- before ship proceeds to apply it. Nothing here
// is a new invariant; this is the same core.Accept/checkDestroysConfirmed/
// resolver.VerifyPins every other acceptance path already goes through,
// called from a second entry point.
func acceptPlanInline(ledger *core.Ledger, ledgerDir, hash string, confirmDestroys bool) (*core.Proposal, error) {
	draft, err := readPlanFile(ledgerDir, hash)
	if err != nil {
		return nil, fmt.Errorf("no accepted proposal %s in this stack's ledger, and no plan file at %s: %w", hash, planFilePath(ledgerDir, hash), err)
	}

	// Integrity check specific to this fallback: a plan file is always
	// written at exactly its own content hash's path (plan.go's own
	// writePlanFile) -- if its content doesn't hash to the filename it was
	// found at, the file was hand-edited or corrupted since, and shipping
	// it under the hash the caller actually asked for would be shipping
	// something other than what that hash names.
	computedHash, err := core.Hash(draft)
	if err != nil {
		return nil, err
	}
	if computedHash != hash {
		return nil, fmt.Errorf("plan file at %s hashes to %s, not %s -- stale or corrupted plan file", planFilePath(ledgerDir, hash), computedHash, hash)
	}

	if err := checkDestroysConfirmed(draft, confirmDestroys); err != nil {
		return nil, err
	}
	if err := resolver.VerifyPins(draft); err != nil {
		return nil, err
	}
	return core.Accept(ledger, draft)
}

// reportAlreadyApplied handles executor.Ship's simplest idempotency case
// (docs/executor.md): every resource was already applied in a prior
// attempt, so this run touched nothing and wrote no new apply record.
// Reports the most recent sealed attempt as evidence, rather than an empty
// response -- a human asking "did this ship already?" gets the same
// receipt either way.
func reportAlreadyApplied(out io.Writer, ledger *core.Ledger, p *core.Proposal, jsonOut bool) error {
	attempts, err := ledger.ApplyAttempts(p.ID)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
	}
	var latest *core.ApplyRecord
	for _, a := range attempts {
		if a.Sealed() {
			latest = a
		}
	}
	if jsonOut {
		if err := writeJSON(out, shipJSON{Format: jsonFormatVersion, AlreadyApplied: true, ApplyRecord: latest}); err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
		}
		return nil
	}
	fmt.Fprintf(out, "%s: already fully applied -- nothing to do\n", p.ID)
	return nil
}

// printShipReport is ubx ship's human output: one line per resource's
// final state for this attempt, any recorded errors underneath it
// (including a redacted-after decline's own manual-steps-style message,
// docs/executor.md), and a trailing summary line.
func printShipReport(out io.Writer, rec *core.ApplyRecord) {
	for _, ra := range rec.Resources {
		st, _ := ra.LastState()
		fmt.Fprintf(out, "%s: %s\n", st, ra.Address)
		for _, e := range ra.Errors {
			fmt.Fprintf(out, "  %s: %s\n", e.Classification, e.Message)
		}
	}
	fmt.Fprintf(out, "%d resource(s), %d applied, %d failed, %d still unknown -- outcome: %s\n",
		len(rec.Resources), rec.Summary.ResourcesApplied, rec.Summary.ResourcesFailed,
		rec.Summary.ResourcesStillUnknown, rec.Summary.Outcome)
}

// shipJSON is `ubx ship --json`'s payload -- format:1, the same contract
// UBI-20's scan/status/why already established. ApplyRecord is the
// already-well-shaped core.ApplyRecord itself, wrapped rather than
// re-derived into a bespoke shape (the same convention whyJSON's Proposal
// field already uses).
type shipJSON struct {
	Format         int               `json:"format"`
	AlreadyApplied bool              `json:"already_applied,omitempty"`
	ApplyRecord    *core.ApplyRecord `json:"apply_record,omitempty"`
}
