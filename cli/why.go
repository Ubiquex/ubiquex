package cli

import (
	"fmt"
	"io"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
)

// proposalIDPattern matches a full 64-hex-char content hash — the only
// legal shape for a proposal ID (docs/schema.md: "id is a content hash...
// no sequential numbering"). Anything else passed to `ubx why` is tried as
// a resource address instead.
var proposalIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func newWhyCmd() *cobra.Command {
	var (
		ledgerDir        string
		verifyAcceptance bool
		repoDir          string
		githubRepo       string
		jsonOut          bool
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

			ledger := core.Open(ledgerDir)
			out := cmd.OutOrStdout()

			if proposalIDPattern.MatchString(args[0]) {
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
				if !jsonOut {
					renderProposal(out, p)
					renderApplies(out, attempts)
				}
				if !verifyAcceptance {
					if jsonOut {
						if err := writeJSON(out, whyJSON{Format: jsonFormatVersion, Proposal: p, Applies: attempts}); err != nil {
							return &ExitCodeError{Code: 2, Err: err}
						}
					}
					return nil
				}
				verifyResult, verifyErr := runVerifyAcceptance(cmd.Context(), out, p, repoDir, githubRepo, jsonOut)
				if jsonOut {
					payload := whyJSON{Format: jsonFormatVersion, Proposal: p, Applies: attempts, VerifyAcceptance: verifyResult}
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
				renderProposalCompact(out, p)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().BoolVar(&verifyAcceptance, "verify-acceptance", false, "re-derive a pr_merge proposal's acceptance against git history + the GitHub API and report whether it still checks out (UBI-11)")
	cmd.Flags().StringVar(&repoDir, "repo-dir", ".", "local git working tree to verify --verify-acceptance's merge commit against")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "owner/name of the GitHub repository, for --verify-acceptance's reviewer re-check (git-history re-check runs without it)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text (UBI-20); the chain view emits a JSON array under \"chain\"")
	return cmd
}

// whyJSON is `ubx why --json`'s payload (UBI-20 workstream 2,
// docs/exit-codes.mdx). Exactly one of Proposal (single-id form) or Chain
// (resource-address form) is set, never both -- the two `ubx why` input
// forms are genuinely different views, not one view with an optional
// extra field.
type whyJSON struct {
	Format           int                   `json:"format"`
	Proposal         *core.Proposal        `json:"proposal,omitempty"`
	Chain            []*core.Proposal      `json:"chain,omitempty"`
	Applies          []*core.ApplyRecord   `json:"applies,omitempty"`
	VerifyAcceptance *verifyAcceptanceJSON `json:"verify_acceptance,omitempty"`
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
			for _, t := range ra.Transitions {
				fmt.Fprintf(out, "      %s at %s", t.State, t.At)
				if t.Detail != "" {
					fmt.Fprintf(out, " -- %s", t.Detail)
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

// renderProposalCompact is why's per-entry rendering for a resource
// address's full proposal chain — terser than renderProposal (a short id,
// one summary line) since a chain view shows several entries at once, but
// still renders attribution in full (see renderIntentSource): "who/when
// changed this" is exactly what a chain view over a resource is for.
func renderProposalCompact(out io.Writer, p *core.Proposal) {
	fmt.Fprintf(out, "- %s %s (%s): %s\n", p.Kind, shortID(p.ID), p.Resolution.ResolvedAt, p.Intent.Summary)
	for _, s := range p.Intent.Sources {
		renderIntentSource(out, s, "    ")
	}
	renderModifies(out, p.Delta.Modifies, "    ")
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
