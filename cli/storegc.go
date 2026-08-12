package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/ledgerstore"
)

// newStoreGCCmd is UBI-57 Part 1: a real, harmless-but-accumulating
// leftover named at UBI-32 Arc B's own closing (core/ledger.go's own
// Append: WriteProposalIfAbsent lands, then AdvanceHead -- a crash
// between the two leaves the just-written proposal object permanently
// unreferenced by any head/chain edge, unless the exact same proposal is
// later retried, core.Ledger.Append's own "interrupted append" resume).
// Only ever considers proposals/ -- see ledgerstore.Store.OrphanedProposals's
// own doc comment for exactly why heads/, head-hint, lock, salt, and
// applies/ are never candidates.
//
// Dry-run by default (the ticket's own explicit requirement): lists what
// WOULD be deleted, never deletes anything, regardless of whether stdin
// is a terminal. --yes is the one, explicit, required flag to actually
// delete -- matching this codebase's own "a flag itself is a real,
// deliberate confirmation" pattern (ship.go's own --confirm-destroys),
// not layered with an ADDITIONAL interactive "type yes" prompt on top,
// since orphaned objects are -- by construction, verified fresh
// immediately before each delete -- genuinely unreferenced, a materially
// different risk profile than shipping a real, live destroy.
//
// Safety, verified twice, not asserted once: the orphan set is computed
// once for the dry-run listing (what the operator actually sees), then
// recomputed FRESH immediately before deleting anything, and only ids
// present in BOTH the displayed list and that fresh recomputation are
// ever deleted -- never a newly-orphaned id the operator was never shown
// (a real TOCTOU concern for a store other callers may still be actively
// writing to), and never a formerly-orphaned id that a concurrent
// interrupted-append resume has since legitimately re-chained.
func newStoreGCCmd() *cobra.Command {
	var (
		stack string
		yes   bool
	)

	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Find and remove orphaned proposal objects in a remote LedgerStore (dry-run by default)",
		Long: `A crash between writing a proposal object and advancing the ledger's CAS head leaves that
object permanently unreferenced by any head/chain edge, unless a later retry of the exact same
proposal completes the interrupted append. This is real, harmless (an unreferenced object costs
storage, nothing else), and accumulating -- ubx store gc finds and, with --yes, removes exactly
those objects, never anything a chain still references, even transitively.

Only meaningful for a remote store (S3/GCS/Azure Blob) -- a git-local ledger's own equivalent
concern is git's own object database, already served by "git gc"/"git prune".

Dry-run by default: lists every orphan found, deletes nothing. Pass --yes to actually delete --
the orphan set is verified fresh immediately before each deletion, not trusted from the earlier
listing alone.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("store gc: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			if cfg.Ledger.Store == "" || cfg.Ledger.Store == "git" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("store gc: .ubx/config's [ledger] store is git-local -- git's own object database has no equivalent orphan problem this command exists for; use \"git gc\"/\"git prune\" on the ledger directory's own .git instead")}
			}
			if stack == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("store gc: --stack is required for a remote ledger store (.ubx/config's [ledger] store is %q) -- a remote store's own per-stack chain means opening one at all requires knowing which stack first", cfg.Ledger.Store)}
			}

			storeURI, err := ledgerstore.WithStack(cfg.Ledger.Store, stack)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("store gc: %w", err)}
			}
			store, closeFn, err := openRemoteStoreGC(cmd.Context(), storeURI)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("store gc: open %s: %w", storeURI, err)}
			}
			defer closeFn()

			out := cmd.OutOrStdout()
			ctx := cmd.Context()

			orphans, err := store.OrphanedProposals(ctx)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("store gc: %w", err)}
			}
			if len(orphans) == 0 {
				fmt.Fprintf(out, "no orphaned proposals in %s\n", storeURI)
				return nil
			}

			fmt.Fprintf(out, "%d orphaned proposal(s) in %s:\n", len(orphans), storeURI)
			for _, id := range orphans {
				fmt.Fprintf(out, "  %s\n", id)
			}

			if !yes {
				fmt.Fprintf(out, "\ndry run -- nothing deleted. Re-run with --yes to delete %d orphan(s).\n", len(orphans))
				return nil
			}

			// Re-verify immediately before deleting anything -- never trust
			// the listing above alone (see this command's own doc comment).
			fresh, err := store.OrphanedProposals(ctx)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("store gc: re-verify orphans before delete: %w", err)}
			}
			freshSet := make(map[string]bool, len(fresh))
			for _, id := range fresh {
				freshSet[id] = true
			}

			deleted := 0
			skipped := 0
			for _, id := range orphans {
				if !freshSet[id] {
					// No longer orphaned since the listing above (a
					// concurrent interrupted-append resume legitimately
					// re-chained it) -- never delete something a chain now
					// references, even if it looked orphaned moments ago.
					fmt.Fprintf(out, "  %s: skipped, no longer orphaned (re-chained since the listing above)\n", id)
					skipped++
					continue
				}
				if err := store.DeleteProposal(ctx, id); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("store gc: delete %s: %w", id, err)}
				}
				fmt.Fprintf(out, "  %s: deleted\n", id)
				deleted++
			}
			fmt.Fprintf(out, "\n%d deleted, %d skipped (re-chained since the listing above)\n", deleted, skipped)
			return nil
		},
	}

	cmd.Flags().StringVar(&stack, "stack", "", "which stack's remote ledger store to garbage-collect (required -- a remote store addresses one chain per stack)")
	cmd.Flags().BoolVar(&yes, "yes", false, "actually delete the orphaned proposals found (default: dry run, lists only, deletes nothing)")
	return cmd
}

// openRemoteStoreGC is a package-level seam (same convention as
// openRemoteLedgerStore, ledgeropen.go) so hermetic tests can point it at
// a shared, real bucket -- production always calls ledgerstore.Open
// unchanged. Returns the CONCRETE *ledgerstore.Store, deliberately not
// wrapped as core.LedgerStore/*core.Ledger the way openLedgerForStack
// returns: core.LedgerStore's own interface deliberately has no "list
// every proposal" method (core/ledgerstore.go's own doc comment --
// object stores are worst at making that cheap and strongly consistent,
// and core itself never needs it), so gc's own real work has to talk to
// ledgerstore.Store directly.
var openRemoteStoreGC = func(ctx context.Context, storeURI string) (*ledgerstore.Store, func() error, error) {
	return ledgerstore.Open(ctx, storeURI)
}
