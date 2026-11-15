package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/provider"
)

func newAcceptCmd() *cobra.Command {
	var (
		ledgerDir               string
		reverifyWith            string
		reverifySource          string
		reverifyProviderVersion string
		resourceType            string
		resourceName            string
		providerConfig          string
		timeout                 time.Duration
		confirmDestroys         bool
	)

	cmd := &cobra.Command{
		Use:   "accept [proposal.json]",
		Short: "Accept a proposal from a file at the local tier, and append it to the ledger",
		Args:  cobra.MaximumNArgs(1),
		// Exit code is a CI contract (UBI-20, docs/exit-codes.mdx): 0
		// accepted, 1 an actionable finding (stale reverify, a
		// parent-mismatched or trailer-hash-mismatched proposal -- the
		// world moved, or the claimed acceptance doesn't check out), 2
		// error. SilenceUsage/Errors: same reasoning as status.go.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: %w", err)}
			}
			if err := applyProviderConfigDefault(cmd, &providerConfig, cfg); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: %w", err)}
			}
			// Config's [provider] only fills a gap in an ALREADY-opted-into
			// reverify (--reverify-source given without --reverify-provider-version)
			// -- it never turns reverification on by itself. accept's reverify
			// is opt-in per invocation, unlike scan/status where a provider is
			// always needed; config supplying [provider] shouldn't silently
			// make every `ubx accept` start reverifying.
			if reverifySource != "" && !cmd.Flags().Changed("reverify-provider-version") && cfg.Provider.Version != "" {
				reverifyProviderVersion = cfg.Provider.Version
			}

			if len(args) != 1 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept requires a proposal.json argument")}
			}

			p, planHash, err := readProposalArg(ledgerDir, args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}

			// docs/schema.md's "Amendment: destroys" -- this project's first
			// hardcoded acceptance-time friction invariant, checked as early
			// as possible (before any reverify round trip or pin
			// re-verification work): a human must explicitly acknowledge a
			// destructive proposal by name, every time, not just once
			// implicitly by running `ubx accept` at all.
			if err := checkDestroysConfirmed(p, confirmDestroys); err != nil {
				return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
			}

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, p.Stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: %w", err)}
			}
			defer closeLedger()

			if reverifyWith != "" || reverifySource != "" {
				if resourceType == "" || resourceName == "" {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: reverification requires --resource-type and --resource-name")}
				}
				addr := core.Address{Stack: p.Stack, Type: resourceType, Name: resourceName}

				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				defer cancel()

				path, _, err := resolveProviderBinary(ctx, reverifyWith, reverifySource, reverifyProviderVersion)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: reverify: %w", err)}
				}

				client, err := provider.Launch(ctx, path)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: reverify: %w", err)}
				}
				defer client.Close()

				salt, err := ledger.Salt()
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: %w", err)}
				}
				if err := core.VerifyFreshness(ctx, newStateReader(client.Provider, salt, reverifySource), ledger, addr, reverifySource,
					json.RawMessage(providerConfig), p); err != nil {
					return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
				}
			}

			// Cross-stack pins (UBI-27, docs/schema.md's "cross_stack_pin"
			// resolution inputs) are re-verified unconditionally, not gated
			// behind an opt-in flag the way live-state reverify is: unlike a
			// real provider round trip, checking whether a neighbor ledger's
			// head has moved is a free, local filesystem read, so there's no
			// cost/latency reason to make an operator ask for it explicitly.
			if err := resolver.VerifyPins(p); err != nil {
				return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
			}

			accepted, err := core.Accept(ledger, p)
			if err != nil {
				return &ExitCodeError{Code: acceptErrorCode(err), Err: err}
			}
			// UBI-62: a plan consumed via acceptance (whether or not it's
			// ever shipped afterward) is pruned from the plan store, same
			// as ship.go's own inline-accept path -- "latest" must never
			// re-offer it either way it was consumed. Tidiness, not
			// correctness: a failed removal only warns.
			if planHash != "" {
				if err := os.Remove(planFilePath(ledgerDir, planHash)); err != nil && !os.IsNotExist(err) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: accept: could not prune consumed plan file: %v\n", err)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "accepted %s (stack %s)\n", accepted.ID, accepted.Stack)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&reverifyWith, "reverify-with", "", "path to a provider binary: re-read the resource live and refuse to accept if it no longer matches the proposal's recorded observation (stale). Mutually exclusive with --reverify-source")
	cmd.Flags().StringVar(&reverifySource, "reverify-source", "", "provider source address to acquire for reverification, e.g. hashicorp/aws (mutually exclusive with --reverify-with; requires --reverify-provider-version)")
	cmd.Flags().StringVar(&reverifyProviderVersion, "reverify-provider-version", "", "explicit provider version to acquire for reverification (required with --reverify-source)")
	cmd.Flags().StringVar(&resourceType, "resource-type", "", "resource type to re-read (required with --reverify-with/--reverify-source)")
	cmd.Flags().StringVar(&resourceName, "resource-name", "", "resource name to re-read (required with --reverify-with/--reverify-source)")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "{}", "JSON object configuring the provider (used with --reverify-with/--reverify-source)")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "timeout for the reverify provider round trip")
	cmd.Flags().BoolVar(&confirmDestroys, "confirm-destroys", false, "required to accept any proposal with blast_radius.destroys > 0 -- this project's first hardcoded acceptance-time friction invariant (docs/schema.md)")
	return cmd
}

// ErrDestroysNotConfirmed means a proposal with blast_radius.destroys > 0
// was accepted without --confirm-destroys -- docs/schema.md's "Amendment:
// destroys," this project's first hardcoded acceptance-time friction
// invariant, distinct in kind from every other check accept makes (which
// are all either structural validation or freshness/staleness detection
// the system verifies for itself, not something asking a human to
// affirmatively acknowledge). The proposal is already fully valid,
// orphan-checked, and fresh by the time this gate is reached -- its only
// job is making sure a human actually meant to accept something with
// teeth, not catching a mistake in the proposal itself.
var ErrDestroysNotConfirmed = errors.New("this proposal has blast_radius.destroys > 0 -- pass --confirm-destroys to accept it")

// checkDestroysConfirmed enforces ErrDestroysNotConfirmed -- checked as
// early as possible, before any reverify round trip or cross-stack pin re-verification
// work, since there is no point spending that effort on a proposal this
// gate will refuse regardless of what it finds.
func checkDestroysConfirmed(p *core.Proposal, confirmed bool) error {
	if p.BlastRadius.Destroys > 0 && !confirmed {
		return ErrDestroysNotConfirmed
	}
	return nil
}

// readProposalArg is UBI-49 finding #6's own fix for accept's local-file
// path: ref is tried as a file path first (unchanged for hand-authored
// drafts -- the only thing this command ever supported before this
// session), and only on a not-found does it fall back to the plan store
// (resolvePlanHash, the same short-hash-aware lookup ship.go's own
// acceptPlanInline fallback already uses) -- the hash `ubx plan`/`ubx
// scan --propose`/`ubx terminate` printed as their own "next: ubx ship
// <hash>" handoff is just as acceptable to `ubx accept` directly. A plan
// file found this way gets the identical hash-matches-filename integrity
// check acceptPlanInline already performs. If neither interpretation
// finds anything, the error names both, per docs/cli-output-spec.md
// principle 6 (teaching errors enumerate all modes).
// readProposalArg returns the resolved proposal plus, only when it came
// from the plan store rather than a hand-authored file, that plan's own
// full hash (planHash == "" for a file path) -- so the caller can prune
// it from .ubx/plans/ after a successful accept (UBI-62: "shipped plans
// pruned from latest" applies equally here, since accepting via a hash
// consumes the draft exactly as shipping one does).
func readProposalArg(ledgerDir, ref string) (p *core.Proposal, planHash string, err error) {
	data, fileErr := os.ReadFile(ref)
	if fileErr == nil {
		var fileP core.Proposal
		if err := json.Unmarshal(data, &fileP); err != nil {
			return nil, "", fmt.Errorf("parse proposal: %w", err)
		}
		return &fileP, "", nil
	}
	if !os.IsNotExist(fileErr) {
		return nil, "", fileErr
	}

	fullHash, planP, planErr := resolvePlanHash(ledgerDir, ref)
	if planErr != nil {
		return nil, "", fmt.Errorf("no such file %q, and no matching plan in %s: %w -- "+
			"pass a path to a hand-authored proposal file, or a hash printed by `ubx plan`/`ubx scan --propose`/`ubx terminate`",
			ref, filepath.Join(ledgerDir, ".ubx", "plans"), planErr)
	}
	computedHash, herr := core.Hash(planP)
	if herr != nil {
		return nil, "", herr
	}
	if computedHash != fullHash {
		return nil, "", fmt.Errorf("plan file at %s hashes to %s, not %s -- stale or corrupted plan file", planFilePath(ledgerDir, fullHash), computedHash, fullHash)
	}
	return planP, fullHash, nil
}

// acceptErrorCode classifies core.Accept's failures for the UBI-20
// exit-code contract: ErrStaleObservation (VerifyFreshness),
// ErrParentMismatch (Accept/Append), and resolver.ErrCrossStackPinStale
// (VerifyPins) all mean "the world moved since this proposal was
// resolved" -- an actionable finding (exit 1), matching the "stale
// block"/"proposal rejected" examples in docs/architecture.md's Hardening
// pass section. ErrDestroysNotConfirmed (UBI-30) joins the same tier for a
// different reason -- not staleness, but a human needing to explicitly
// acknowledge a destructive proposal before it's accepted -- still exactly
// as actionable (add --confirm-destroys and re-run) as any of the above.
// Anything else (a malformed proposal, a double-accept attempt, ledger
// I/O) is a genuine error (exit 2).
func acceptErrorCode(err error) int {
	if errors.Is(err, core.ErrStaleObservation) || errors.Is(err, core.ErrParentMismatch) ||
		errors.Is(err, resolver.ErrCrossStackPinStale) || errors.Is(err, ErrDestroysNotConfirmed) {
		return 1
	}
	return 2
}
