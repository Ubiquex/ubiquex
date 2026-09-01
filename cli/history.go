package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
)

// newHistoryCmd is UBI-227's read-only half: a stack's own real ledger
// history, newest head first (matching "ubx why <address>"'s own chain-
// view ordering, and git log's own convention) -- each real accepted
// head, its kind, a short summary of what actually changed, when it was
// resolved, and who accepted it. Never a "finding" the way "ubx status
// --drift"/"ubx verify" produce one: 0 on any successful listing,
// including an empty one, 2 on a genuine error, matching every other
// read-only projection command's own UBI-20 exit-code contract.
//
// Deliberately narrower than "ubx why": this is an overview across the
// whole chain, not the full per-attribute diff/acceptance/apply-history
// detail "ubx why <id>" already renders for one proposal at a time --
// naming a real head from this list is exactly what "ubx restore <head>"
// and "ubx why <head>" both need next.
func newHistoryCmd() *cobra.Command {
	var (
		ledgerDir  string
		stack      string
		jsonOut    bool
		fullHashes bool
	)

	cmd := &cobra.Command{
		Use:           "history",
		Short:         "List a stack's own real ledger history, newest head first",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("history: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("history: %w", err)}
			}
			defer closeLedger()

			chain, err := ledger.Chain()
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("history: %w", err)}
			}

			out := cmd.OutOrStdout()
			if jsonOut {
				return writeJSON(out, historyToJSON(chain))
			}
			st := newStylerFull(cmd, fullHashes)
			renderHistoryHuman(out, st, chain)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack's history to list -- required only when .ubx/config's [ledger] store is a remote store; unused for the default git store")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	cmd.Flags().BoolVar(&fullHashes, "full-hashes", false, "render every hash in full, never truncated")
	return cmd
}

// renderHistoryHuman prints chain newest-first, one line per proposal,
// using the exact same "+N create(s) ~N change(s) -N terminate(s)"
// vocabulary "ubx scan"'s own renderScanCard already established
// (cli/scan.go) rather than inventing a second phrasing for the same
// three counts.
func renderHistoryHuman(out io.Writer, st *styler, chain []*core.Proposal) {
	if len(chain) == 0 {
		fmt.Fprintln(out, "(empty -- no proposals recorded yet)")
		return
	}
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		who := "(not yet accepted)"
		if p.Acceptance != nil {
			who = fmt.Sprintf("%v via %s", p.Acceptance.Approvers, p.Acceptance.Method)
		}
		summary := fmt.Sprintf("+%d create(s) ~%d change(s) -%d terminate(s)", p.BlastRadius.Creates, p.BlastRadius.Modifies, p.BlastRadius.Destroys)
		fmt.Fprintf(out, "%s  %-14s %s\n", st.Hash(p.ID), p.Kind, summary)
		fmt.Fprintf(out, "    %s\n", p.Intent.Summary)
		fmt.Fprintf(out, "    resolved %s, accepted by %s\n", p.Resolution.ResolvedAt, who)
	}
}

type historyEntryJSON struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Summary     string   `json:"summary"`
	ResolvedAt  string   `json:"resolved_at"`
	Creates     int64    `json:"creates"`
	Changes     int64    `json:"changes"`
	Terminates  int64    `json:"terminates"`
	Accepted    bool     `json:"accepted"`
	AcceptedAt  string   `json:"accepted_at,omitempty"`
	AcceptedBy  []string `json:"accepted_by,omitempty"`
	AcceptedVia string   `json:"accepted_via,omitempty"`
}

type historyJSON struct {
	Format  int                `json:"format"`
	Entries []historyEntryJSON `json:"entries"`
}

// historyToJSON mirrors renderHistoryHuman's own newest-first ordering
// and field selection, structured rather than pre-formatted -- the same
// "human and machine output describe the identical real data, never two
// independently-assembled views" discipline every other --json command
// in this package already holds to.
func historyToJSON(chain []*core.Proposal) historyJSON {
	payload := historyJSON{Format: jsonFormatVersion, Entries: make([]historyEntryJSON, 0, len(chain))}
	for i := len(chain) - 1; i >= 0; i-- {
		p := chain[i]
		entry := historyEntryJSON{
			ID:         p.ID,
			Kind:       string(p.Kind),
			Summary:    p.Intent.Summary,
			ResolvedAt: p.Resolution.ResolvedAt,
			Creates:    p.BlastRadius.Creates,
			Changes:    p.BlastRadius.Modifies,
			Terminates: p.BlastRadius.Destroys,
		}
		if p.Acceptance != nil {
			entry.Accepted = true
			entry.AcceptedAt = p.Acceptance.AcceptedAt
			entry.AcceptedBy = p.Acceptance.Approvers
			entry.AcceptedVia = p.Acceptance.Method
		}
		payload.Entries = append(payload.Entries, entry)
	}
	return payload
}
