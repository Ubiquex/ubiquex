package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/intentprovider"
)

// proposalIDPattern matches a full 64-hex-char content hash — the only
// legal shape for a proposal ID (docs/schema.md: "id is a content hash...
// no sequential numbering"). Anything else passed to `ubx why` is tried as
// a resource address instead.
var proposalIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func newWhyCmd() *cobra.Command {
	var (
		ledgerDir        string
		stack            string
		verifyAcceptance bool
		repoDir          string
		githubRepo       string
		jsonOut          bool
		dialogue         bool
	)

	cmd := &cobra.Command{
		Use:   "why <proposal-id> | <stack>.<type>.<name>",
		Short: "Explain an accepted proposal, or a resource's full proposal history",
		Args:  cobra.ExactArgs(1),
		// Exit code is a CI contract (UBI-20, docs/exit-codes.mdx): 0
		// (nothing to re-verify, or it checks out), 1 (--verify-acceptance
		// found the claimed acceptance doesn't check out -- an actionable
		// finding), 2 (error). SilenceUsage/Errors: same reasoning as
		// status.go.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("why: %w", err)}
			}
			applyGithubRepoDefault(cmd, &githubRepo, cfg)
			applyStackDefault(cmd, &stack, cfg)

			out := cmd.OutOrStdout()

			if proposalIDPattern.MatchString(args[0]) {
				// A bare proposal ID carries no stack of its own -- unlike
				// the address branch below, which derives one directly
				// from the argument -- so --stack (or its config default)
				// is what a remote store needs to know which chain to
				// open at all (docs/architecture.md -- Addressing).
				ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("why: %w", err)}
				}
				defer closeLedger()

				p, err := ledger.Read(args[0])
				if err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
				// UBI-26/27: a drift_revert or change proposal's own "full
				// story" includes what ubx ship actually did, not just the
				// decision -- fetched for every shippable kind regardless
				// of whether it was ever shipped (an empty/nil slice
				// renders nothing, see renderApplies).
				var attempts []*core.ApplyRecord
				if p.Kind == core.KindDriftRevert || p.Kind == core.KindChange {
					attempts, err = ledger.ApplyAttempts(p.ID)
					if err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("why: %w", err)}
					}
				}
				var dlg *intentprovider.Dialogue
				if dialogue {
					dlg = loadDialogueSource(ledgerDir, p)
				}

				if !jsonOut {
					renderProposal(out, p)
					renderApplies(out, attempts)
					if dialogue {
						renderDialogue(out, p, dlg)
					}
				}
				if !verifyAcceptance {
					if jsonOut {
						if err := writeJSON(out, whyJSON{Format: jsonFormatVersion, Proposal: p, Applies: attempts, Dialogue: dlg}); err != nil {
							return &ExitCodeError{Code: 2, Err: err}
						}
					}
					return nil
				}
				verifyResult, verifyErr := runVerifyAcceptance(cmd.Context(), out, p, repoDir, githubRepo, jsonOut)
				if jsonOut {
					payload := whyJSON{Format: jsonFormatVersion, Proposal: p, Applies: attempts, VerifyAcceptance: verifyResult, Dialogue: dlg}
					if err := writeJSON(out, payload); err != nil {
						return &ExitCodeError{Code: 2, Err: err}
					}
				}
				return verifyErr
			}

			addr, ok := core.ParseAddress(args[0])
			if !ok {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("%q is not a valid proposal ID (64-char hex) or resource address (<stack>.<type>.<name>)", args[0])}
			}
			// The address itself already names its own stack -- used
			// directly, regardless of --stack/config's own default, since
			// it's unambiguous by construction.
			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, addr.Stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("why: %w", err)}
			}
			defer closeLedger()

			proposals, err := ledger.ProposalsForAddress(addr)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}
			if len(proposals) == 0 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("no proposals found for %s", addr)}
			}

			// Newest first, matching the human view's own order.
			chain := make([]*core.Proposal, len(proposals))
			for i, p := range proposals {
				chain[len(proposals)-1-i] = p
			}

			if jsonOut {
				payload := whyJSON{Format: jsonFormatVersion, Chain: chain}
				if err := writeJSON(out, payload); err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
				return nil
			}

			fmt.Fprintf(out, "%s: %d proposal(s), newest first\n", addr, len(proposals))
			for _, p := range chain {
				if err := renderProposalCompact(out, ledger, p); err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack's ledger to open, for a bare proposal-id argument -- required only when .ubx/config's [ledger] store is a remote store (a resource-address argument already names its own stack); unused for the default git store")
	cmd.Flags().BoolVar(&verifyAcceptance, "verify-acceptance", false, "re-derive a pr_merge proposal's acceptance against git history + the GitHub API and report whether it still checks out (UBI-11)")
	cmd.Flags().StringVar(&repoDir, "repo-dir", ".", "local git working tree to verify --verify-acceptance's merge commit against")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "owner/name of the GitHub repository, for --verify-acceptance's reviewer re-check (git-history re-check runs without it)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text (UBI-20); the chain view emits a JSON array under \"chain\"")
	cmd.Flags().BoolVar(&dialogue, "dialogue", false, "if this proposal's intent.sources names a captured dialogue (UBI-46, ubx chat), read and render the actual conversation behind it -- change proposal -> the draft it came from -> the real conversation")
	return cmd
}

// whyJSON is `ubx why --json`'s payload (UBI-20 workstream 2,
// docs/exit-codes.mdx). Exactly one of Proposal (single-id form) or Chain
// (resource-address form) is set, never both -- the two `ubx why` input
// forms are genuinely different views, not one view with an optional
// extra field.
type whyJSON struct {
	Format           int                      `json:"format"`
	Proposal         *core.Proposal           `json:"proposal,omitempty"`
	Chain            []*core.Proposal         `json:"chain,omitempty"`
	Applies          []*core.ApplyRecord      `json:"applies,omitempty"`
	VerifyAcceptance *verifyAcceptanceJSON    `json:"verify_acceptance,omitempty"`
	Dialogue         *intentprovider.Dialogue `json:"dialogue,omitempty"`
}

// renderProposal is the full single-proposal view, unchanged from before
// this session except that intent.sources now goes through
// renderIntentSource (see its doc comment) instead of a bare kind/ref/hash
// line — dialogue/manual_edit/issue sources render byte-identically to
// before; only cloudtrail/cloudtrail_unattributed sources look different.
func renderProposal(out io.Writer, p *core.Proposal) {
	fmt.Fprintf(out, "proposal %s (%s)\n", p.ID, p.Kind)
	fmt.Fprintf(out, "stack:  %s\n", p.Stack)
	fmt.Fprintf(out, "status: %s\n", p.Status)
	fmt.Fprintf(out, "intent: %s\n", p.Intent.Summary)
	for _, s := range p.Intent.Sources {
		renderIntentSource(out, s, "  ")
	}
	if p.Acceptance != nil {
		fmt.Fprintf(out, "accepted by %v via %s at %s\n", p.Acceptance.Approvers, p.Acceptance.Method, p.Acceptance.AcceptedAt)
	}
	fmt.Fprintf(out, "blast radius: +%d ~%d -%d\n", p.BlastRadius.Creates, p.BlastRadius.Modifies, p.BlastRadius.Destroys)
	renderModifies(out, p.Delta.Modifies, "")
	renderDestroys(out, p.Delta.Destroys, "", true)
}

// renderApplies is UBI-26's own addition to `ubx why`'s single-proposal
// view: a drift_revert's full story includes what `ubx ship` actually did,
// not just the decision to revert. A nil/empty attempts (never shipped, or
// not a drift_revert at all) renders nothing -- silence, not a "no applies"
// line, matching this command's existing terseness. Every attempt is
// shown, sealed or not, oldest first (docs/schema.md: an unsealed attempt
// is a real, honest artifact of an interrupted run, not something to hide).
func renderApplies(out io.Writer, attempts []*core.ApplyRecord) {
	if len(attempts) == 0 {
		return
	}
	fmt.Fprintln(out, "apply history:")
	for _, a := range attempts {
		status := "unsealed (interrupted or still in progress)"
		if a.Sealed() {
			status = fmt.Sprintf("outcome=%s", a.Summary.Outcome)
		}
		fmt.Fprintf(out, "  attempt %d: %s\n", a.Attempt, status)
		for _, ra := range a.Resources {
			fmt.Fprintf(out, "    %s:\n", ra.Address)
			// UBI-30: a destroy's own terminal "applied" transition means
			// either "destroyed" or "already_absent" -- neither of which
			// reads as a create/modify's own plain "applied" at all, so
			// this is called out explicitly on that exact line rather than
			// leaving a reader to notice and interpret the reconcile:
			// lines below on their own.
			outcome := destroyOutcome(ra.Reconciliation)
			for i, t := range ra.Transitions {
				fmt.Fprintf(out, "      %s at %s", t.State, t.At)
				if t.Detail != "" {
					fmt.Fprintf(out, " -- %s", t.Detail)
				}
				if t.State == core.ResourceApplied && i == len(ra.Transitions)-1 && outcome != "" {
					fmt.Fprintf(out, " (%s)", outcome)
				}
				fmt.Fprintln(out)
			}
			for _, r := range ra.Reconciliation {
				fmt.Fprintf(out, "      reconcile: %s at %s", r.Outcome, r.At)
				if r.Detail != "" {
					fmt.Fprintf(out, " -- %s", r.Detail)
				}
				fmt.Fprintln(out)
			}
			for _, e := range ra.Errors {
				fmt.Fprintf(out, "      error (%s): %s\n", e.Classification, e.Message)
			}
		}
	}
}

// renderModifies prints each Delta.Modifies entry's changed attributes,
// current -> new (drift_adopt's before/after convention, or
// drift_revert's own reversed one -- Kind already prints verbatim above,
// so this needs no kind-specific branching). A $redacted value (UBI-23)
// renders "(redacted)" via rawOrAbsent, the same rule revert-plan's own
// printPlan uses -- so a proposal involving a sensitive attribute change
// is visibly a change without ever surfacing the salted hash next to a
// human-readable attribute name.
func renderModifies(out io.Writer, modifies []core.Modification, indent string) {
	for _, m := range modifies {
		for _, path := range sortedAttributePaths(m.Before, m.After) {
			fmt.Fprintf(out, "%schange: %s: %s: %s -> %s\n", indent, m.Target, path, rawOrAbsent(m.Before[path]), rawOrAbsent(m.After[path]))
		}
	}
}

// renderDestroys prints each Delta.Destroys entry (UBI-30, docs/resolver.md's
// "Amendment: destroys") -- the resolve-time content of what's being
// removed, the same "make the content visible, not just the decision"
// role renderModifies already plays for delta.modifies. Before this,
// nothing rendered delta.destroys at all: a destroy proposal's own
// address, and the full state it carries inline specifically so a human
// signing it away can see what's being lost (docs/resolver.md's own
// reasoning for why it's the whole state, not just changed attributes),
// were both invisible in `ubx why`'s output. showState controls whether
// each attribute is printed too (the full single-proposal view) or just
// the address (the terser per-entry chain view, matching
// renderProposalCompact's own existing terseness elsewhere).
func renderDestroys(out io.Writer, destroys []core.DestroyEntry, indent string, showState bool) {
	for _, d := range destroys {
		fmt.Fprintf(out, "%sdestroy: %s\n", indent, d.Address)
		if !showState {
			continue
		}
		var state map[string]json.RawMessage
		if err := json.Unmarshal(d.State, &state); err != nil {
			continue
		}
		keys := make([]string, 0, len(state))
		for k := range state {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(out, "%s  %s: %s\n", indent, k, rawOrAbsent(state[k]))
		}
	}
}

// destroyOutcome returns reconciliation's own last entry's Outcome if it's
// one of the two destroy-terminal values (UBI-30, docs/executor.md's
// "shipping destroys" amendment: destroyed | already_absent) -- "" for a
// create/modify (whose own Reconciliation outcomes, if ever recorded at
// all, are always applied/failed/inconclusive) or for a destroy resource
// with no reconciliation recorded (shouldn't happen once a destroy
// reaches ResourceApplied, core/executor's own shipDestroyNode always
// records at least one entry first, but never misrepresented as one of
// the two if it somehow did).
func destroyOutcome(reconciliation []core.ReconciliationAttempt) string {
	if len(reconciliation) == 0 {
		return ""
	}
	switch last := reconciliation[len(reconciliation)-1].Outcome; last {
	case "destroyed", "already_absent":
		return last
	default:
		return ""
	}
}

// renderProposalCompact is why's per-entry rendering for a resource
// address's full proposal chain — terser than renderProposal (a short id,
// one summary line) since a chain view shows several entries at once, but
// still renders attribution in full (see renderIntentSource): "who/when
// changed this" is exactly what a chain view over a resource is for.
//
// Apply history is also rendered for drift_revert/change entries (UBI-29)
// -- the same condition the single-proposal-ID view already gates on --
// so a resource whose genesis is a shipped create (rather than an
// adoption) shows the full resolve -> accept -> ship story in its own
// chain view, not just the accepted decision.
func renderProposalCompact(out io.Writer, ledger *core.Ledger, p *core.Proposal) error {
	fmt.Fprintf(out, "- %s %s (%s): %s\n", p.Kind, shortID(p.ID), p.Resolution.ResolvedAt, p.Intent.Summary)
	for _, s := range p.Intent.Sources {
		renderIntentSource(out, s, "    ")
	}
	renderModifies(out, p.Delta.Modifies, "    ")
	renderDestroys(out, p.Delta.Destroys, "    ", false)
	if p.Kind == core.KindDriftRevert || p.Kind == core.KindChange {
		attempts, err := ledger.ApplyAttempts(p.ID)
		if err != nil {
			return err
		}
		renderApplies(out, attempts)
	}
	return nil
}

// shortID is a presentation-layer truncation only (docs/schema.md's hash.go
// comment: "a short display form is a presentation concern, not part of
// the canonical identity itself") — never used to look anything back up.
func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}

// renderIntentSource prints one intent.sources entry. cloudtrail sources
// render the human story inline — actor, what they did, when, from where —
// with the event id/content_hash demoted to an indented detail line; that
// story, not the event id, is what "who changed this" is actually asking
// for. cloudtrail_unattributed sources render their reason in words rather
// than a bare enum value. Every other kind (dialogue/manual_edit/issue) is
// unchanged from before this session.
func renderIntentSource(out io.Writer, s core.IntentSource, indent string) {
	switch s.Kind {
	case "cloudtrail":
		fmt.Fprintf(out, "%ssource: cloudtrail -- %s %s at %s", indent, s.ActorARN, s.EventName, s.EventTime)
		if s.SourceIP != "" {
			fmt.Fprintf(out, " from %s", s.SourceIP)
		}
		fmt.Fprintln(out)
		fmt.Fprintf(out, "%s  event %s (content_hash=%s)\n", indent, s.EventID, s.ContentHash)
	case "cloudtrail_unattributed":
		fmt.Fprintf(out, "%ssource: cloudtrail_unattributed -- %s\n", indent, unattributedReason(s.Reason))
	default:
		fmt.Fprintf(out, "%ssource: %s %s (content_hash=%s)\n", indent, s.Kind, s.Ref, s.ContentHash)
	}
}

// loadDialogueSource is `ubx why --dialogue`'s own "change proposal -> the
// draft it came from -> the actual conversation" walk (UBI-46,
// docs/intent-provider.md's own "Amendment: the chat medium"): finds p's
// own dialogue-kind intent.sources entry, if any, and reads the real
// captured file it names. A proposal with no dialogue source (every
// proposal that didn't come from `ubx chat`) returns nil -- an ordinary,
// expected case, not an error; renderDialogue says so plainly rather
// than printing nothing with no explanation.
func loadDialogueSource(ledgerDir string, p *core.Proposal) *intentprovider.Dialogue {
	for _, s := range p.Intent.Sources {
		if s.Kind != intentprovider.SourceKindDialogue {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ledgerDir, s.Ref))
		if err != nil {
			return nil
		}
		var dlg intentprovider.Dialogue
		if err := json.Unmarshal(data, &dlg); err != nil {
			return nil
		}
		return &dlg
	}
	return nil
}

// renderDialogue prints the real conversation behind p, if --dialogue
// found one -- the actual turns, not just the source line's own
// kind/ref/hash (renderIntentSource's own job, unchanged, still printed
// first as part of renderProposal).
func renderDialogue(out io.Writer, p *core.Proposal, dlg *intentprovider.Dialogue) {
	if dlg == nil {
		fmt.Fprintln(out, "\n--dialogue: this proposal has no captured dialogue source (it wasn't drafted via `ubx chat`, or the file couldn't be read)")
		return
	}
	fmt.Fprintf(out, "\nconversation (%s / %s, started %s):\n", dlg.Adapter, dlg.Model, dlg.StartedAt)
	for i, t := range dlg.Turns {
		fmt.Fprintf(out, "  [Turn %d, %s]: %s\n", i+1, t.At, t.Text)
	}
}

// unattributedReason renders IntentSource.Reason (docs/schema.md's
// CloudTrail attribution amendment) in words rather than a bare enum value.
// Falls back to the raw reason string for anything unrecognized (a future
// reason added to the schema shouldn't render as nothing).
func unattributedReason(reason string) string {
	switch reason {
	case core.ReasonNoMatchingEvent:
		return "no matching CloudTrail event found in the correlation window"
	case core.ReasonDeliveryWindow:
		return "too recent for CloudTrail to have delivered a matching event yet"
	case core.ReasonNotLogged:
		return "CloudTrail visibility unavailable (denied access or not logged)"
	default:
		return reason
	}
}
