package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
)

// newRevertPlanCmd is UBI-16's "revert emits plan" (docs/plan.md M3-4,
// docs/architecture.md's Revert path): takes an already-accepted
// drift_revert proposal and prints the reconciliation artifact a human (or
// their own tooling) needs to actually fix cloud. It never writes a file,
// never touches cloud, and has no --write flag at all. Applying the
// correction is explicitly out of scope until the native executor earns
// that trust; this command only ever emits.
func newRevertPlanCmd() *cobra.Command {
	var (
		ledgerDir string
		stack     string
	)

	cmd := &cobra.Command{
		Use:   "revert-plan <proposal-id>",
		Short: "Emit the reconciliation plan for an accepted drift_revert proposal -- never makes any change",
		Long: `Emit the reconciliation plan for an accepted drift_revert proposal: a human-readable
list of what changed and what restoring it requires.

ubx revert-plan NEVER writes a file and NEVER touches cloud. Making the correction to the live
resource is the operator's own tooling to run (docs/plan.md's M3-4: "revert emits plan"; executor
trust comes later). The corrective .tf diff it once offered with --tf-dir went with writeback
(UBI-301): there are no source files for it to correct.`,
		Args: cobra.ExactArgs(1),
		// Exit code is a CI contract (UBI-20, docs/exit-codes.mdx): 0 (the
		// plan was emitted, or there was nothing to plan) or 2 (error).
		// Exit 1 meant "manual .tf steps are needed", which ended with
		// --tf-dir (UBI-301). SilenceUsage/Errors: same reasoning as
		// status.go.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("revert-plan: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			// A bare proposal ID carries no stack of its own (docs/architecture.md
			// -- Addressing) -- --stack (or its config default) is what a
			// remote store needs to know which chain to open at all, exactly
			// like `ubx why`/`ubx ship`.
			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("revert-plan: %w", err)}
			}
			defer closeLedger()

			ref, refErr := resolveProposalPrefix(ledgerDir, args[0])
			if refErr != nil {
				ref = args[0]
			}
			p, err := ledger.Read(ref)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}
			if p.Kind != core.KindDriftRevert {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("revert-plan: proposal %s is kind %q, not drift_revert -- revert-plan only works on a proposed revert", p.ID, p.Kind)}
			}
			if p.Status != core.StatusAccepted {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("revert-plan: proposal %s is not accepted (status %q)", p.ID, p.Status)}
			}
			if len(p.Delta.Modifies) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "revert-plan: proposal has no delta.modifies entries -- nothing to plan")
				return nil
			}

			out := cmd.OutOrStdout()
			printPlan(out, p.Delta.Modifies)

			fmt.Fprintln(out, "\nmake the correction above in cloud via your own tooling; ubx never makes it for you")
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack's ledger to open -- required only when .ubx/config's [ledger] store is a remote store; unused for the default git store")
	return cmd
}

// printPlan prints the human-readable reconciliation plan: one line per
// changed attribute, current (drifted) value -> restore-to (ledger-truth)
// value.
func printPlan(out io.Writer, modifies []core.Modification) {
	fmt.Fprintln(out, "--- plan ---")
	for _, m := range modifies {
		for _, path := range sortedAttributePaths(m.Before, m.After) {
			fmt.Fprintf(out, "%s: %s: %s -> %s\n", m.Target, path, rawOrAbsent(m.Before[path]), rawOrAbsent(m.After[path]))
		}
	}
}

// sortedAttributePaths returns the union of before/after's dot-paths,
// sorted -- Modification.Before/After are independently populated maps
// (core/state.go's diffObjects: a path may legitimately appear in only one
// of the two, e.g. an attribute that was added or removed entirely), so
// neither map alone is guaranteed to name every changed path, and Go map
// iteration order is never relied on for user-facing output here either.
func sortedAttributePaths(before, after map[string]json.RawMessage) []string {
	seen := make(map[string]struct{}, len(before)+len(after))
	for k := range before {
		seen[k] = struct{}{}
	}
	for k := range after {
		seen[k] = struct{}{}
	}
	paths := make([]string, 0, len(seen))
	for k := range seen {
		paths = append(paths, k)
	}
	sort.Strings(paths)
	return paths
}

// rawOrAbsent renders a Modification.Before/After entry for display,
// distinguishing "absent" (attribute didn't exist on this side) from an
// actual JSON value. A $redacted value (UBI-23, docs/architecture.md --
// Secrets) renders as "(redacted)" rather than its raw JSON -- the salted
// hash isn't secret material itself, but it has no business appearing
// inline next to a human-readable attribute name either. Always compacted
// (UBI-61): a proposal round-tripped through a saved plan file
// (.ubx/plans/<hash>.json, itself written pretty-printed for human
// readability on disk) would otherwise carry that same indentation as
// literal bytes into this one-line rendering, making `ubx ship`'s receipt
// for a plan-store hash look different from `ubx plan`'s own receipt for
// identical content -- one visual language, not two by accident.
func rawOrAbsent(raw json.RawMessage) string {
	if raw == nil {
		return "(absent)"
	}
	if core.IsRedactedValue(raw) {
		return "(redacted)"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}
