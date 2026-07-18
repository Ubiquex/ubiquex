package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/core/executor"
	"github.com/ubiquex/ubiquex-cli/provider"
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
	)

	cmd := &cobra.Command{
		Use:   "ship <proposal-id>",
		Short: "Execute an accepted drift_revert or change proposal against live cloud -- the only command that applies",
		Long: `Executes an already-accepted drift_revert or change proposal: for a drift_revert, restores the
resource's live state to match the ledger's recorded truth; for a change (UBI-27, "ubx resolve"'s own
output), creates and modifies resources for real, in real dependency order, feeding each resource's
real applied output into any sibling still carrying a $computed marker pointing at it. This is the one
ubx command that changes real infrastructure -- accept/why/status/scan/revert-plan/resolve only ever
read or record.

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
		// already fully applied -- a genuine no-op), 1 partially applied or
		// failed (an actionable finding -- retry, or investigate why),
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
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
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

	return cmd
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
