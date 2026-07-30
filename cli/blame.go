package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
)

// newBlameCmd is UBI-39, the second of the read-only projection quartet:
// per-attribute provenance -- git blame for infrastructure. Structurally
// impossible for a state-file tool (state has no history) or a log
// aggregator (no signed decisions to point at); this is the ledger's own
// history, walked per attribute instead of merged into one opaque final
// value the way core.Ledger.FoldState's own output already is.
//
// Mechanically core.Blame -- see that function's own doc comment for the
// full fold mechanics. This command's own job is address resolution
// (mirroring `ubx why <address>`'s own pattern: the address itself names
// its own stack, used directly, never --stack/config's own default),
// rendering, and the UBI-20 exit-code contract: 0 found (whether or not
// destroyed -- a destroyed address is a normal, complete answer, not a
// finding), 2 never recorded at all (nothing to blame) or a hard error.
// Blame never itself signals exit 1 -- there is no "wrong" answer a
// re-derived attribute history could surface the way `ubx verify` finds
// broken hashes; it either has an answer or it doesn't.
func newBlameCmd() *cobra.Command {
	var (
		ledgerDir  string
		jsonOut    bool
		all        bool
		fullHashes bool
	)

	cmd := &cobra.Command{
		Use:           "blame <address>",
		Short:         "Per-attribute provenance -- which proposal last set each attribute, and who signed it",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, ok := core.ParseAddress(args[0])
			if !ok {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("%q is not a valid resource address (<stack>.<type>.<name>)", args[0])}
			}

			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blame: %w", err)}
			}

			// The address itself already names its own stack -- used
			// directly, the same posture `ubx why <address>` already
			// holds, since it's unambiguous by construction.
			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, addr.Stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blame: %w", err)}
			}
			defer closeLedger()

			result, err := core.Blame(ledger, addr)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blame: %w", err)}
			}
			if !result.Found {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blame: %s has no recorded history -- never adopted, and never created via a shipped change proposal", addr)}
			}

			out := cmd.OutOrStdout()
			if jsonOut {
				if err := writeJSON(out, blameToJSON(result)); err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
				return nil
			}
			renderBlameHuman(out, newStylerFull(cmd, fullHashes), result, all)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text")
	cmd.Flags().BoolVar(&all, "all", false, "show every attribute, including zero/null/empty provider defaults hidden by default within a grouped block")
	cmd.Flags().BoolVar(&fullHashes, "full-hashes", false, "render every hash in full instead of the default 12-char short form")
	return cmd
}

// blameGroupKey is docs/cli-output-spec.md's own blame grouping key
// (founder test finding, UBI-61): entries that share identical
// provenance -- same proposal, same acceptance -- are the ordinary case
// (one proposal usually sets many attributes at once) and read far
// better as one header covering all of them than as N repeated blocks
// each re-stating the identical "set by/accepted" story. SetAt is
// included even though it's already implied by ProposalID (one proposal
// has exactly one SetAt) -- matching the founder's own literal framing
// ("same proposal/time/acceptance") rather than assuming the implication
// holds forever.
type blameGroupKey struct {
	ProposalID       string
	SetAt            string
	AcceptanceMethod string
}

// blameGroup is every entry sharing one blameGroupKey, in the same
// relative order core.Blame produced them.
type blameGroup struct {
	blameGroupKey
	Kind             core.ProposalKind
	Approvers        []string
	AttributedActors []string
	Entries          []core.BlameEntry
}

// groupBlameEntries preserves first-seen order (both across groups and
// within a group) rather than sorting -- core.Blame's own entries are
// already produced in a meaningful order (docs/schema.md), and this
// grouping pass is purely a presentation collapse over it, never a
// reordering.
func groupBlameEntries(entries []core.BlameEntry) []blameGroup {
	var order []blameGroupKey
	byKey := map[blameGroupKey]*blameGroup{}
	for _, e := range entries {
		key := blameGroupKey{ProposalID: e.ProposalID, SetAt: e.SetAt, AcceptanceMethod: e.AcceptanceMethod}
		g, ok := byKey[key]
		if !ok {
			g = &blameGroup{blameGroupKey: key, Kind: e.Kind, Approvers: e.Approvers, AttributedActors: e.AttributedActors}
			byKey[key] = g
			order = append(order, key)
		}
		g.Entries = append(g.Entries, e)
	}
	groups := make([]blameGroup, 0, len(order))
	for _, key := range order {
		groups = append(groups, *byKey[key])
	}
	return groups
}

// isBlameDefaultValue reports whether raw looks like a provider default
// left untouched (docs/cli-output-spec.md: "empty/null/zero-default
// attributes collapsed by default behind --all -- 24 lines of ""/null/
// false drowns the 3 lines that matter") -- null, empty string, zero
// number, false, or an empty object/array. A value that fails to parse
// as JSON at all is never hidden (never silently drop something this
// can't confidently classify as a default).
func isBlameDefaultValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	switch trimmed {
	case "", "null", `""`, "0", "false", "{}", "[]":
		return true
	default:
		return false
	}
}

func renderBlameHuman(out io.Writer, st *styler, result *core.BlameResult, showAll bool) {
	fmt.Fprintf(out, "Blame  %s\n\n", result.Address)
	if result.Destroyed {
		fmt.Fprintf(out, "%s by %s at %s -- blaming the final pre-destroy state\n\n",
			st.Red("DESTROYED"), st.Hash(result.DestroyedBy.ProposalID), result.DestroyedBy.At)
	}

	for _, g := range groupBlameEntries(result.Entries) {
		if g.ProposalID == "" {
			for _, e := range g.Entries {
				value := string(e.Value)
				if e.Redacted {
					value = "(redacted)"
				}
				fmt.Fprintf(out, "%s: %s\n", e.Path, value)
				fmt.Fprintln(out, "  set by: unknown")
			}
			continue
		}

		var shown, hidden []core.BlameEntry
		for _, e := range g.Entries {
			if !showAll && !e.Redacted && isBlameDefaultValue(e.Value) {
				hidden = append(hidden, e)
			} else {
				shown = append(shown, e)
			}
		}

		fmt.Fprintf(out, "%s %d attribute(s) · set by %s (%s) · %s",
			st.Dim("▸"), len(g.Entries), st.Hash(g.ProposalID), g.Kind, g.SetAt)
		switch g.AcceptanceMethod {
		case "":
			// no acceptance recorded -- shouldn't happen for anything
			// reachable via ProposalsForAddress, but never crash over it.
		case "pr_merge":
			fmt.Fprintf(out, " · pr_merge (%s)", strings.Join(g.Approvers, ", "))
		default:
			fmt.Fprintf(out, " · %s", g.AcceptanceMethod)
		}
		fmt.Fprintln(out)
		if len(g.AttributedActors) > 0 {
			fmt.Fprintf(out, "    %s %s\n", st.Purple("attributed to:"), strings.Join(g.AttributedActors, ", "))
		}
		for _, e := range shown {
			value := string(e.Value)
			if e.Redacted {
				value = "(redacted)"
			}
			fmt.Fprintf(out, "    %s: %s\n", e.Path, value)
		}
		if len(hidden) > 0 {
			fmt.Fprintf(out, "    %s %d provider default(s) hidden -- --all to show\n", st.Dim("·"), len(hidden))
		}
		fmt.Fprintln(out)
	}
}

// blameJSON is `ubx blame --json`'s own top-level document (UBI-20
// workstream 2, UBI-39).
type blameJSON struct {
	Format      int              `json:"format"`
	Address     addressJSON      `json:"address"`
	Destroyed   bool             `json:"destroyed"`
	DestroyedBy *destroyedByJSON `json:"destroyed_by,omitempty"`
	Entries     []blameEntryJSON `json:"entries"`
}

type destroyedByJSON struct {
	ProposalID string `json:"proposal_id"`
	At         string `json:"at"`
}

type blameEntryJSON struct {
	Path             string          `json:"path"`
	Value            json.RawMessage `json:"value"`
	Redacted         bool            `json:"redacted"`
	ProposalID       string          `json:"proposal_id,omitempty"`
	Kind             string          `json:"kind,omitempty"`
	SetAt            string          `json:"set_at,omitempty"`
	AcceptanceMethod string          `json:"acceptance_method,omitempty"`
	Approvers        []string        `json:"approvers,omitempty"`
	AttributedActors []string        `json:"attributed_actors,omitempty"`
}

func blameToJSON(result *core.BlameResult) blameJSON {
	payload := blameJSON{
		Format:    jsonFormatVersion,
		Address:   addressToJSON(result.Address),
		Destroyed: result.Destroyed,
		Entries:   make([]blameEntryJSON, 0, len(result.Entries)),
	}
	if result.DestroyedBy != nil {
		payload.DestroyedBy = &destroyedByJSON{ProposalID: result.DestroyedBy.ProposalID, At: result.DestroyedBy.At}
	}
	for _, e := range result.Entries {
		entry := blameEntryJSON{
			Path:             e.Path,
			Value:            e.Value,
			Redacted:         e.Redacted,
			ProposalID:       e.ProposalID,
			Kind:             string(e.Kind),
			SetAt:            e.SetAt,
			AcceptanceMethod: e.AcceptanceMethod,
			Approvers:        e.Approvers,
			AttributedActors: e.AttributedActors,
		}
		payload.Entries = append(payload.Entries, entry)
	}
	return payload
}
