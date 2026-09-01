package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
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
		fullHashes       bool
	)

	cmd := &cobra.Command{
		Use:   "why <proposal-id-or-alias> | <stack>.<type>.<name>",
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
			st := newStylerFull(cmd, fullHashes)

			// UBI-228: args[0] may be a human-readable alias (ubx alias
			// set) rather than a hash or an address -- tried only when it
			// matches neither existing shape, so a raw hash or a real
			// <stack>.<type>.<name> address behaves exactly as before
			// this ticket. A failed alias lookup falls through silently;
			// the existing "not a valid proposal ID or resource address"
			// error below is still the single source of truth for
			// "neither shape matched," now also naming alias as a third
			// option.
			whyArg := args[0]
			if !proposalIDPattern.MatchString(whyArg) {
				if _, ok := core.ParseAddress(whyArg); !ok {
					if resolved, resolvedStack, aerr := resolveHeadOrAlias(ledgerDir, stack, whyArg); aerr == nil {
						whyArg = resolved
						if resolvedStack != "" {
							stack = resolvedStack
						}
					}
				}
			}

			if proposalIDPattern.MatchString(whyArg) {
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

				p, err := ledger.Read(whyArg)
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
				var dlg *resolver.Dialogue
				if dialogue {
					dlg = loadDialogueSource(ledgerDir, p)
				}

				if !jsonOut {
					renderProposal(out, st, p)
					renderPinChain(out, st, cmd.Context(), p)
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

			addr, ok := core.ParseAddress(whyArg)
			if !ok {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("%q is not a valid proposal ID (64-char hex), resource address (<stack>.<type>.<name>), or known alias", args[0])}
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
				if err := renderProposalCompact(out, st, ledger, p); err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack's ledger to open, for a bare proposal-id argument -- required only when .ubx/config's [ledger] store is a remote store (a resource-address argument already names its own stack); unused for the default git store")
	cmd.Flags().BoolVar(&verifyAcceptance, "verify-acceptance", false, "re-derive a pr_merge proposal's acceptance against git history + the GitHub API and report whether it still checks out")
	cmd.Flags().StringVar(&repoDir, "repo-dir", ".", "local git working tree to verify --verify-acceptance's merge commit against")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "owner/name of the GitHub repository, for --verify-acceptance's reviewer re-check (git-history re-check runs without it)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text; the chain view emits a JSON array under \"chain\"")
	cmd.Flags().BoolVar(&dialogue, "dialogue", false, "if this proposal's intent.sources names a captured dialogue (ubx chat), read and render the actual conversation behind it -- change proposal -> the draft it came from -> the real conversation")
	cmd.Flags().BoolVar(&fullHashes, "full-hashes", false, "render every hash in full instead of the default 12-char short form")
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
	Dialogue         *resolver.Dialogue    `json:"dialogue,omitempty"`
}

// renderProposal is the full single-proposal view, unchanged from before
// this session except that intent.sources now goes through
// renderIntentSource (see its doc comment) instead of a bare kind/ref/hash
// line — dialogue/manual_edit/issue sources render byte-identically to
// before; only cloudtrail/cloudtrail_unattributed sources look different.
func renderProposal(out io.Writer, st *styler, p *core.Proposal) {
	fmt.Fprintf(out, "proposal %s (%s)\n", st.Hash(p.ID), p.Kind)
	fmt.Fprintf(out, "stack:  %s\n", p.Stack)
	fmt.Fprintf(out, "status: %s\n", p.Status)
	fmt.Fprintf(out, "intent: %s\n", p.Intent.Summary)
	for _, s := range p.Intent.Sources {
		renderIntentSource(out, st, s, "  ")
	}
	if p.Acceptance != nil {
		fmt.Fprintf(out, "accepted by %v via %s at %s\n", p.Acceptance.Approvers, p.Acceptance.Method, p.Acceptance.AcceptedAt)
	}
	fmt.Fprintf(out, "blast radius: %s %s %s\n",
		st.Green(fmt.Sprintf("+%d", p.BlastRadius.Creates)),
		st.Yellow(fmt.Sprintf("~%d", p.BlastRadius.Modifies)),
		st.Red(fmt.Sprintf("-%d", p.BlastRadius.Destroys)))
	renderCreates(out, st, p.Delta.Creates, "")
	renderModifies(out, st, p.Delta.Modifies, "")
	renderDestroys(out, st, p.Delta.Destroys, "", true)
}

// renderPinChain is UBI-57 Part 2's own addition: renders the FULL,
// walked cross-stack pin chain p depends on (A pins B, which itself
// pins C, ...), via resolver.WalkPinChain -- purely informational, never
// a reason `ubx why` itself would fail or refuse anything. Silence for
// the common case (no cross-stack pin at all), matching this file's own
// existing terseness elsewhere (renderApplies).
//
// A real error walking the chain (a neighbor 2+ hops away being
// unreachable, say) is rendered as its own line rather than aborting
// the rest of `ubx why`'s otherwise-working output -- this is
// visibility, not the real blocking check (VerifyPins, called from `ubx
// accept`/`ubx ship`, an entirely separate code path this never touches
// or influences).
func renderPinChain(out io.Writer, st *styler, ctx context.Context, p *core.Proposal) {
	hops, err := resolver.WalkPinChain(ctx, p)
	if err != nil {
		fmt.Fprintf(out, "pin chain: %v\n", err)
		return
	}
	if len(hops) == 0 {
		return
	}
	fmt.Fprintf(out, "pin chain (%d hop%s):\n", len(hops), plural(len(hops)))
	for i, h := range hops {
		status := st.Green("fresh")
		if h.Stale {
			status = st.Red(fmt.Sprintf("STALE (pinned %s, now %s)", st.Hash(h.PinnedHead), st.Hash(h.CurrentHead)))
		}
		fmt.Fprintf(out, "  %d. %s -> %s: %s\n", i+1, h.Resource, h.LedgerDir, status)
	}
	for _, h := range hops {
		if h.Stale {
			fmt.Fprintf(out, "  (an indirect ancestor has moved -- informational only, this never blocks %s itself; only its own direct neighbor's staleness does)\n", st.Hash(p.ID))
			break
		}
	}
}

// plural is a tiny, local pluralization helper -- "1 hop" vs "2 hops",
// no dependency worth adding for a single call site.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
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
	fmt.Fprintln(out, "ship history:")
	for _, a := range attempts {
		status := "unsealed (interrupted or still in progress)"
		if a.Sealed() {
			status = fmt.Sprintf("outcome=%s", displayOutcome(a.Summary.Outcome))
		}
		fmt.Fprintf(out, "  attempt %d: %s\n", a.Attempt, status)
		for _, ra := range a.Resources {
			fmt.Fprintf(out, "    %s:\n", ra.Address)
			// UBI-30: a destroy's own terminal "applied"/"shipped" transition
			// means either "destroyed" or "already_absent" -- neither of
			// which reads as a create/modify's own plain "shipped" at all,
			// so this is called out explicitly on that exact line rather
			// than leaving a reader to notice and interpret the reconcile:
			// lines below on their own.
			outcome := destroyOutcome(ra.Reconciliation)
			for i, t := range ra.Transitions {
				fmt.Fprintf(out, "      %s at %s", displayResourceState(string(t.State)), t.At)
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

// renderCreates prints each Delta.Creates entry -- rendered for the
// first time ever this session (docs/cli-output-spec.md's plan mockup:
// creates get their own "+ type.name    create" block, same as modifies/
// destroys already had): before this, a create was only ever counted
// (delta/blast-radius numbers), never itemized, the one asymmetry left
// once modifies/destroys both got their own content-visible rendering
// (UBI-27/UBI-30). Decodes the same permissive {stack,type,name,config}
// shape core.createNodeAddress uses internally (unexported there, so
// re-decoded locally rather than reaching into core) -- a node this
// can't make sense of is skipped, never a panic or a blank line, mirroring
// every other renderer's own "make the content visible" but never fail
// posture here.
func renderCreates(out io.Writer, st *styler, creates []json.RawMessage, indent string) {
	for i, raw := range creates {
		var node struct {
			Type    string                     `json:"type"`
			Name    string                     `json:"name"`
			Config  map[string]json.RawMessage `json:"config"`
			Sources []core.IntentSource        `json:"sources"`
		}
		if err := json.Unmarshal(raw, &node); err != nil || node.Type == "" || node.Name == "" {
			continue
		}
		// docs/cli-output-spec.md §v2 (UBI-63): "+ <type>.<name> create"
		// renders green AND bold (GreenBold, not a plain Green "+" the way
		// this looked before); one empty line separates resource blocks.
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s%s\n", indent, st.GreenBold(fmt.Sprintf("+ %s.%s create", node.Type, node.Name)))
		// UBI-74 Slice 6: a resource's own per-resource sources (today,
		// only ever a "blueprint" kind -- blueprint.ExpandCalls is the
		// one producer that ever populates ResourceIntent.Sources)
		// render right under the create header, before its attributes --
		// "which blueprint made this" is scannable at a glance, the same
		// prominence the header line itself already gets.
		for _, s := range node.Sources {
			renderIntentSource(out, st, s, indent+"  ")
		}
		keys := make([]string, 0, len(node.Config))
		for k := range node.Config {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		attrIndent := indent + "    "
		for _, k := range keys {
			// docs/cli-output-spec.md §v2: JSON-valued attributes (IAM/
			// trust policies) render as formatted, readable JSON blocks,
			// never escaped single-line strings; a resolved $computed
			// marker renders as the friendly $ref:<address> notation
			// (formatConfigValueV2, configvaluev2.go).
			val := formatConfigValueV2(attrIndent, node.Config[k])
			if strings.Contains(val, "\n") {
				fmt.Fprintf(out, "%s%s:\n%s%s\n", attrIndent, k, attrIndent, val)
			} else {
				fmt.Fprintf(out, "%s%s: %s\n", attrIndent, k, val)
			}
		}
	}
}

// renderModifies prints each Delta.Modifies entry as ONE header line --
// "~ <address> change", yellow AND bold (YellowBold, the same general
// "+/~/- <address> <op>" header rule renderCreates' GreenBold and
// renderDestroys' RedBold already apply, UBI-88) -- with its changed
// attributes (current -> new; drift_adopt's before/after convention, or
// drift_revert's own reversed one -- Kind already prints verbatim above,
// so this needs no kind-specific branching) indented beneath it, one
// shared indent level subordinate to the header, matching renderCreates'
// own attrIndent. Before this, a modify rendered as a flat "~ change:
// <address>: <path>: <before> -> <after>" line per changed attribute,
// with no header/body structure at all -- the one asymmetry left once
// create/destroy both got their own header+body shape (UBI-27/30/78). A
// $redacted value (UBI-23) renders "(redacted)" via rawOrAbsent, the same
// rule revert-plan's own printPlan uses -- so a proposal involving a
// sensitive attribute change is visibly a change without ever surfacing
// the salted hash next to a human-readable attribute name. One empty line
// separates consecutive modify blocks, the same i>0 convention
// renderCreates/renderDestroys already use.
func renderModifies(out io.Writer, st *styler, modifies []core.Modification, indent string) {
	for i, m := range modifies {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s%s\n", indent, st.YellowBold(fmt.Sprintf("~ %s change", m.Target)))
		attrIndent := indent + "    "
		for _, path := range sortedAttributePaths(m.Before, m.After) {
			fmt.Fprintf(out, "%s%s: %s -> %s\n", attrIndent, path, rawOrAbsent(m.Before[path]), rawOrAbsent(m.After[path]))
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
// renderProposalCompact's own existing terseness elsewhere). The whole
// "- <address> destroy" header renders red AND bold (st.RedBold, UBI-61
// comment thread's "general +/-/~ header rule") -- docs/cli-output-
// spec.md: red = destroys, "red-led" full-state block -- matching
// renderCreates' own whole-line GreenBold treatment, not just the
// leading "-" glyph colored on its own as this used to render. UBI-88:
// address-then-op word order, matching the "<symbol> <address> <op>"
// shape create's "+ <type>.<name> create" and modify's "~ <address>
// change" both use -- this was the one op-renderer left as the
// "<symbol> <op>: <address>" pre-existing odd one out (the op word
// itself stays "destroy" here, unchanged; the vocabulary sweep's
// change(s)/terminate(s) rename is scoped to the delta line and the
// three other flagged surfaces, not this header).
func renderDestroys(out io.Writer, st *styler, destroys []core.DestroyEntry, indent string, showState bool) {
	// UBI-77: one blank line between consecutive destroy blocks -- the
	// identical i>0 convention renderCreates already uses (its own doc
	// comment: "one empty line separates resource blocks"). Before this,
	// nothing separated a multi-address `ubx terminate` receipt's own
	// destroy entries at all: the first block's own leading blank comes
	// from the section header above it, the last block's own trailing
	// blank comes from renderPlanReceipt's post-delta spacer below it, but
	// every block in between had neither -- confirmed empirically (a
	// throwaway 2-address repro) to run straight into the next block's
	// own "- <address> destroy" header with zero separation.
	for i, d := range destroys {
		if i > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "%s%s\n", indent, st.RedBold(fmt.Sprintf("- %s destroy", d.Address)))
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
		// UBI-88: 4-space extra indent, matching renderCreates/
		// renderModifies' own attrIndent -- every attribute/diff line under
		// a "+/~/- <address> <op>" header shares ONE indent level now,
		// visually subordinate to its header (this used to be only 2
		// spaces here, the one op-renderer left inconsistent with the
		// other two).
		attrIndent := indent + "    "
		for _, k := range keys {
			// UBI-78: the same formatted-JSON-block treatment renderCreates
			// already gives Delta.Creates' own config values -- before this,
			// a destroy's full-state block (plan/terminate/why's single-
			// proposal view all share this one renderer) was the one place
			// left rendering a JSON-valued attribute (an IAM/trust policy
			// document) as a raw escaped single-line string instead, in
			// violation of docs/cli-output-spec.md v2's own "JSON-valued
			// attributes render as FORMATTED, readable JSON blocks" rule.
			val := formatConfigValueV2(attrIndent, state[k])
			if strings.Contains(val, "\n") {
				fmt.Fprintf(out, "%s%s:\n%s%s\n", attrIndent, k, attrIndent, val)
			} else {
				fmt.Fprintf(out, "%s%s: %s\n", attrIndent, k, val)
			}
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
func renderProposalCompact(out io.Writer, st *styler, ledger *core.Ledger, p *core.Proposal) error {
	fmt.Fprintf(out, "- %s %s (%s): %s\n", p.Kind, st.Hash(p.ID), p.Resolution.ResolvedAt, p.Intent.Summary)
	for _, s := range p.Intent.Sources {
		renderIntentSource(out, st, s, "    ")
	}
	// UBI-74 Slice 6: the chain view (ubx why <address>) didn't render
	// Acceptance at all before this -- only the single-proposal view
	// (renderProposal, bare-proposal-id form) did. A blueprint-created
	// resource's own "full provenance chain" needs this: it's the real,
	// concrete half of the dual-signature story renderIntentSource's own
	// "blueprint" case names (the calling stack's own instantiation
	// signing) -- without it, `ubx why <address>` on a blueprint-created
	// resource would show WHICH blueprint but never WHO signed off on
	// actually calling it.
	if p.Acceptance != nil {
		fmt.Fprintf(out, "    accepted by %v via %s at %s\n", p.Acceptance.Approvers, p.Acceptance.Method, p.Acceptance.AcceptedAt)
	}
	renderCreates(out, st, p.Delta.Creates, "    ")
	renderModifies(out, st, p.Delta.Modifies, "    ")
	renderDestroys(out, st, p.Delta.Destroys, "    ", false)
	if p.Kind == core.KindDriftRevert || p.Kind == core.KindChange {
		attempts, err := ledger.ApplyAttempts(p.ID)
		if err != nil {
			return err
		}
		renderApplies(out, attempts)
	}
	return nil
}

// renderIntentSource prints one intent.sources entry. cloudtrail sources
// render the human story inline — actor, what they did, when, from where —
// with the event id/content_hash demoted to an indented detail line; that
// story, not the event id, is what "who changed this" is actually asking
// for. cloudtrail_unattributed sources render their reason in words rather
// than a bare enum value. "promotion" sources (UBI-55) render the exact
// human story docs/architecture.md's own "Environments & promotion"
// names literally ("promoted from staging/8f3c…") — see docs/schema.md's
// "Amendment: promotion evidence" for the field shape. "restore" sources
// (UBI-227) render just as directly ("restored ledger head 8f3c...") --
// the same provenance-not-equality posture "promotion" already
// established, applied to naming which earlier head a restore
// proposal targeted. Every other kind (dialogue/manual_edit/issue) is
// unchanged from before this session.
func renderIntentSource(out io.Writer, st *styler, s core.IntentSource, indent string) {
	label := st.Purple("source:")
	switch s.Kind {
	case "cloudtrail":
		fmt.Fprintf(out, "%s%s cloudtrail -- %s %s at %s", indent, label, s.ActorARN, s.EventName, s.EventTime)
		if s.SourceIP != "" {
			fmt.Fprintf(out, " from %s", s.SourceIP)
		}
		fmt.Fprintln(out)
		fmt.Fprintf(out, "%s  event %s (content_hash=%s)\n", indent, s.EventID, s.ContentHash)
	case "cloudtrail_unattributed":
		fmt.Fprintf(out, "%s%s cloudtrail_unattributed -- %s\n", indent, label, unattributedReason(s.Reason))
	case "promotion":
		fmt.Fprintf(out, "%s%s promoted from %s/%s\n", indent, label, s.Base, st.Hash(s.Ref))
	case "restore":
		// UBI-227: Ref is the target head this proposal restored -- a
		// provenance claim, never an equality claim, exactly like
		// "promotion" above (core/proposal.go's own doc comment). So
		// months later, "ubx why" names which head this change restored
		// directly, rather than someone having to infer it from a gap in
		// history.
		fmt.Fprintf(out, "%s%s restored ledger head %s\n", indent, label, st.Hash(s.Ref))
	case "blueprint":
		// UBI-74 Slice 6: Ref is always "<name>:<content_hash>"
		// (blueprint.ExpandCalls' own stamping) -- split so the hash
		// portion gets the SAME short/full-hash styling every other
		// hash in this command's output already gets (st.Hash,
		// --full-hashes-aware), rather than a bare, un-styled string.
		// The dual-signature story UBI-74's own design record names --
		// the blueprint AUTHOR's own signing, separate from the CALLING
		// stack's own instantiation signing -- is rendered honestly,
		// not assumed: this build has no separate accept/sign ceremony
		// for a blueprint's own definition at all (no `ubx blueprint
		// accept`, no ledger entry for a blueprint by itself), so only
		// the SECOND signature is real today -- the surrounding
		// proposal's own "accepted by ... via ... at ..." line (already
		// rendered elsewhere in this same `ubx why` output) IS that
		// real, calling-stack signing. Named here explicitly rather
		// than silently implying a blueprint-authorship signature
		// exists when it doesn't.
		name, hash, ok := strings.Cut(s.Ref, ":")
		hash = strings.TrimPrefix(hash, "sha256:") // truncate the hex itself, not the "sha256:" label eating into the budget
		if !ok {
			fmt.Fprintf(out, "%s%s blueprint %s\n", indent, label, s.Ref)
		} else {
			fmt.Fprintf(out, "%s%s blueprint %s:sha256:%s\n", indent, label, name, st.Hash(hash))
		}
		fmt.Fprintf(out, "%s  (this resource's own creation is signed by the CALLING stack's own acceptance below; "+
			"the blueprint's own authorship has no separate signing ceremony in this build yet)\n", indent)
	default:
		fmt.Fprintf(out, "%s%s %s %s (content_hash=%s)\n", indent, label, s.Kind, s.Ref, s.ContentHash)
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
func loadDialogueSource(ledgerDir string, p *core.Proposal) *resolver.Dialogue {
	for _, s := range p.Intent.Sources {
		if s.Kind != core.SourceKindDialogue {
			continue
		}
		data, err := os.ReadFile(filepath.Join(ledgerDir, s.Ref))
		if err != nil {
			return nil
		}
		var dlg resolver.Dialogue
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
func renderDialogue(out io.Writer, p *core.Proposal, dlg *resolver.Dialogue) {
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
