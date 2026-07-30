package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/writeback"
)

// newRevertPlanCmd is UBI-16's "revert emits plan" (docs/plan.md M3-4,
// docs/architecture.md's Revert path): takes an already-accepted
// drift_revert proposal and prints the reconciliation artifact a human (or
// their own tooling) needs to actually fix cloud. It never writes a file,
// never touches cloud, and has no --write flag at all -- unlike
// ubx writeback, which does apply to .tf given --write. Applying the
// correction is explicitly out of scope until the native executor earns
// that trust; this command only ever emits.
func newRevertPlanCmd() *cobra.Command {
	var (
		ledgerDir string
		stack     string
		tfDir     string
	)

	cmd := &cobra.Command{
		Use:   "revert-plan <proposal-id>",
		Short: "Emit the reconciliation plan for an accepted drift_revert proposal -- never applies anything",
		Long: `Emit the reconciliation plan for an accepted drift_revert proposal: a human-readable
list of what changed and what restoring it requires, plus (with --tf-dir) a corrective .tf diff for
whatever attributes are safely rewritable and a manual-steps section for whatever isn't.

ubx revert-plan NEVER writes a file and NEVER touches cloud. Applying the correction -- to .tf source,
or to the live resource -- is the operator's own tooling to run (docs/plan.md's M3-4: "revert emits
plan"; executor trust comes later).`,
		Args: cobra.ExactArgs(1),
		// Exit code is a CI contract (UBI-20, docs/exit-codes.mdx): 0
		// (nothing to plan, or every changed attribute got a .tf diff), 1
		// (manual steps are needed -- a declined attribute or a resource
		// block that couldn't be located -- an actionable finding), 2
		// (error). SilenceUsage/Errors: same reasoning as status.go.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("revert-plan: %w", err)}
			}
			applyTFDirDefault(cmd, &tfDir, cfg)
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

			p, err := ledger.Read(args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}
			if p.Kind != core.KindDriftRevert {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("revert-plan: proposal %s is kind %q, not drift_revert -- revert-plan only applies to a proposed revert", p.ID, p.Kind)}
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

			if tfDir == "" {
				fmt.Fprintln(out, "\nno --tf-dir given -- apply the correction above to cloud via your own tooling; ubx never applies it")
				return nil
			}
			manualSteps, err := printTFDiffAndManualSteps(out, tfDir, p.Delta.Modifies)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}
			if manualSteps > 0 {
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("revert-plan: %d attribute(s) need manual steps (see above)", manualSteps)}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack's ledger to open -- required only when .ubx/config's [ledger] store is a remote store; unused for the default git store")
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", "directory containing .tf files to compute a corrective diff against (optional) -- never written to, ubx never applies anything")
	return cmd
}

// printPlan prints the human-readable reconciliation plan: one line per
// changed attribute, current (drifted) value -> restore-to (ledger-truth)
// value. Always produced, regardless of --tf-dir.
func printPlan(out io.Writer, modifies []core.Modification) {
	fmt.Fprintln(out, "--- plan ---")
	for _, m := range modifies {
		for _, path := range sortedAttributePaths(m.Before, m.After) {
			fmt.Fprintf(out, "%s: %s: %s -> %s\n", m.Target, path, rawOrAbsent(m.Before[path]), rawOrAbsent(m.After[path]))
		}
	}
}

// printTFDiffAndManualSteps applies each modifies entry against tfDir via
// the same writeback machinery ubx writeback uses -- fed a Modification whose
// After is already the restore target, so no changes to writeback itself are
// needed; "reverse direction" describes the semantic meaning (the file
// moves back toward original truth), not a different code path. Every
// applied attribute prints as a diff; every declined attribute, and every
// modifies entry whose resource block isn't found in tfDir at all, is
// collected into a manual-steps section instead of failing the command --
// a revert can legitimately target a resource that was only ever adopted
// via `ubx scan`, never written to .tf. Returns the number of manual-steps
// entries (declined + not-found), so the caller can classify the UBI-20
// exit code -- any manual step needed is an actionable finding, not a
// clean pass.
func printTFDiffAndManualSteps(out io.Writer, tfDir string, modifies []core.Modification) (int, error) {
	var declined []writeback.DeclinedAttribute
	var notFound []string
	anyDiff := false

	for _, m := range modifies {
		path, newContent, report, err := writeback.FindAndApply(tfDir, m.Target, m)
		if err != nil {
			notFound = append(notFound, fmt.Sprintf("%s: %v", m.Target, err))
			continue
		}
		declined = append(declined, report.Declined...)
		if len(report.Applied) == 0 {
			continue
		}
		if !anyDiff {
			fmt.Fprintln(out, "\n--- .tf diff ---")
			anyDiff = true
		}
		diff, err := unifiedDiff(path, newContent)
		if err != nil {
			return 0, fmt.Errorf("revert-plan: diff %s: %w", path, err)
		}
		fmt.Fprintln(out, diff)
	}

	if len(declined) > 0 || len(notFound) > 0 {
		fmt.Fprintln(out, "\n--- manual steps ---")
		for _, nf := range notFound {
			fmt.Fprintf(out, "%s -- apply this correction manually\n", nf)
		}
		for _, d := range declined {
			fmt.Fprintf(out, "%s -- %s", d.Path, d.Reason)
			if d.Expression != "" {
				fmt.Fprintf(out, " (current value: %s)", d.Expression)
			}
			fmt.Fprintln(out, " -- apply this correction manually")
		}
	}
	return len(declined) + len(notFound), nil
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
// inline next to a human-readable attribute name either.
func rawOrAbsent(raw json.RawMessage) string {
	if raw == nil {
		return "(absent)"
	}
	if core.IsRedactedValue(raw) {
		return "(redacted)"
	}
	return string(raw)
}
