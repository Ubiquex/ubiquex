package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
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
		ledgerDir string
		jsonOut   bool
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
			renderBlameHuman(out, result)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text (UBI-20)")
	return cmd
}

func renderBlameHuman(out io.Writer, result *core.BlameResult) {
	fmt.Fprintf(out, "%s\n", result.Address)
	if result.Destroyed {
		fmt.Fprintf(out, "DESTROYED by %s at %s -- blaming the final pre-destroy state\n", shortID(result.DestroyedBy.ProposalID), result.DestroyedBy.At)
	}
	fmt.Fprintln(out)

	for _, e := range result.Entries {
		value := string(e.Value)
		if e.Redacted {
			value = "(redacted)"
		}
		fmt.Fprintf(out, "%s: %s\n", e.Path, value)
		if e.ProposalID == "" {
			fmt.Fprintln(out, "  set by: unknown")
			continue
		}
		fmt.Fprintf(out, "  set by %s (%s), %s\n", shortID(e.ProposalID), e.Kind, e.SetAt)
		switch e.AcceptanceMethod {
		case "":
			// no acceptance recorded -- shouldn't happen for anything
			// reachable via ProposalsForAddress, but never crash over it.
		case "pr_merge":
			fmt.Fprintf(out, "  accepted: pr_merge, approvers: %s\n", strings.Join(e.Approvers, ", "))
		default:
			fmt.Fprintf(out, "  accepted: %s\n", e.AcceptanceMethod)
		}
		if len(e.AttributedActors) > 0 {
			fmt.Fprintf(out, "  attributed to: %s\n", strings.Join(e.AttributedActors, ", "))
		}
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
