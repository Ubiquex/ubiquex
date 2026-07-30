package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// newTerminateCmd is UBI-49 finding #7's own verb (Linear comment on
// UBI-49, founder-decided name -- the wire/schema vocabulary underneath
// stays destroys[]/tombstone, "terminate" is the human-facing verb over
// it, docs/cli-output-spec.md's own "terminate" section).
//
// `ubx terminate <address>...` authors a destroy proposal directly from
// the ledger's own knowledge of each address -- no AI, no authoring
// document, the address IS the spec, exactly like `ubx plan`/`ubx
// resolve` given a hand-written destroys-only intent file, just without
// having to hand-write one. Every address is self-contained
// (<stack>.<type>.<name>, core.ParseAddress) -- the same convention
// `blame`/`why` already use, not a bare --stack-scoped short form, so
// multi-address termination is unambiguous by construction.
//
// Building IntentFile{Destroys: args} and calling the unmodified
// resolver.Resolve -- the same call plan.go makes for a hand-written
// intent file -- reuses 100% of the existing destroy validation
// (ErrDestroyTargetMissing, ErrDuplicateDestroy, ErrDestroyResourceConflict,
// ErrDestroyOrphaned, ErrDestroyTargetNoLookup, core/resolver/destroys.go)
// with zero new core code. Never touches the ledger itself -- exactly
// like `ubx plan`, this only authors and saves a draft to the plan store;
// `ubx ship`'s own standard double consent (--confirm-destroys, plus
// UBI-62's interactive yes once that lands) is unchanged and untouched by
// this command.
func newTerminateCmd() *cobra.Command {
	var (
		ledgerDir       string
		providerPath    string
		source          string
		providerVersion string
		out             string
		timeout         time.Duration
		knownDependents []string
	)

	cmd := &cobra.Command{
		Use:   "terminate <address>...",
		Short: "Author a destroy proposal directly from the ledger's own knowledge of one or more addresses -- no AI, no authoring document",
		Long: `Each <address> is self-contained (<stack>.<type>.<name>, the same convention
"ubx blame"/"ubx why" already use) and must all name the same stack. Builds a destroys-only
intent and resolves it through the identical, unmodified core/resolver.Resolve every other
entry point already uses -- same orphan-protection, same duplicate/missing-target checks, same
failure modes as a hand-written destroys[] intent file. Renders the full receipt (the
destroyed resource's own last-known state, per UBI-30's full-state-at-review requirement) and
saves the resolved-but-unaccepted proposal to .ubx/plans/<hash>.json, exactly like "ubx plan".

This command never touches a ledger -- nothing here is accepted or applied. Run "ubx ship
<hash>" to accept (local tier, --confirm-destroys required) and apply in one step.

"ubx destroy" is not an alias for this command -- it teaches toward "ubx terminate" instead,
by design (docs/cli-output-spec.md principle 6).`,
		Args: cobra.MinimumNArgs(1),
		// terminate has no "finding" concept, the same audit outcome as
		// plan/propose/resolve (UBI-20 exit-code contract): it either
		// resolves or it doesn't. 0 or 2 only -- exactly like "ubx plan",
		// since nothing here is accepted, applied, or itself a finding.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			addrs := make([]core.Address, 0, len(args))
			for _, s := range args {
				addr, ok := core.ParseAddress(s)
				if !ok {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("terminate: %q is not a valid resource address (<stack>.<type>.<name>)", s)}
				}
				addrs = append(addrs, addr)
			}
			stack := addrs[0].Stack
			for _, addr := range addrs[1:] {
				if addr.Stack != stack {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("terminate: %s and %s name different stacks -- a proposal is always single-stack, terminate them separately", addrs[0], addr)}
				}
			}

			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("terminate: %w", err)}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			providers, err := loadResolveProviders(ctx, cmd, cfg, &providerPath, &source, &providerVersion)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("terminate: %w", err)}
			}

			ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("terminate: %w", err)}
			}
			defer closeLedger()

			intent := resolver.IntentFile{
				SchemaVersion: core.SchemaVersion,
				Kind:          resolver.IntentFileKind,
				Stack:         stack,
				Intent:        core.Intent{Summary: terminateSummary(args)},
				Destroys:      args,
			}

			p, err := resolver.Resolve(ledger, providers, &intent, knownDependents)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("terminate: %w", err)}
			}

			hash, err := core.Hash(p)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("terminate: %w", err)}
			}

			data, err := json.MarshalIndent(p, "", "  ")
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("terminate: marshal proposal: %w", err)}
			}

			planPath, err := writePlanFile(ledgerDir, hash, data)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("terminate: %w", err)}
			}
			if out != "" {
				if err := os.WriteFile(out, data, 0o644); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("terminate: %w", err)}
				}
			}

			outWriter := cmd.OutOrStdout()
			renderPlanReceipt(outWriter, p)
			fmt.Fprintf(outWriter, "\nplan: %s\nubx-proposal: %s\nnext: %s\n", planPath, hash, nextShipHint([]string{hash}))
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/ -- also where the plan is saved, at .ubx/plans/<hash>.json")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source)")
	cmd.Flags().StringVar(&out, "out", "", "additionally write the full resolved proposal here (the plan is always saved under .ubx/plans/ regardless)")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "timeout for provider acquisition/schema fetch")
	cmd.Flags().StringArrayVar(&knownDependents, "known-dependent", nil,
		"ledger_dir of a neighbor stack to check for cross-stack orphan references before destroying (repeatable)")
	return cmd
}

// terminateSummary is terminate's own generated intent.Summary -- there's
// no authoring document to draw one from (the address is the whole
// spec), so this names exactly what's being destroyed rather than
// leaving Summary blank.
func terminateSummary(addrs []string) string {
	if len(addrs) == 1 {
		return fmt.Sprintf("terminate %s", addrs[0])
	}
	return fmt.Sprintf("terminate %d resources", len(addrs))
}

// newDestroyCmd is deliberately NOT an alias for "ubx terminate" (the
// founder's own "decide at build" note on UBI-49's finding #7): every
// other consent-relevant surface in this project is explicit rather than
// aliased/implicit (double-consent, typed confirmations, the plan/ship
// split itself), and docs/cli-output-spec.md principle 6 (teaching
// errors) is explicitly in this session's own scope -- a silent alias
// would hide the rename from a terraform-trained user typing muscle
// memory instead of teaching them the real verb. "ubx destroy" exists
// purely so that muscle memory finds *something*, and that something
// teaches.
func newDestroyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "destroy <address>...",
		Short:         "Not a real command -- teaches toward \"ubx terminate\"",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("destroy: there is no \"ubx destroy\" -- use \"ubx terminate\" (same destroys[] machinery underneath, ledger-derived, no authoring document needed)")}
		},
	}
	return cmd
}
