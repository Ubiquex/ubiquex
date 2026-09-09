package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// renderAmbiguity is `ubx propose --from-doc`'s own human-facing render
// of a draft's ambiguity content (docs/intent-provider.md's own
// "ambiguity as visible content" design center) -- printed before the
// raw JSON draft so a reviewer sees exactly what the intent provider
// interpreted, filled in, or was unsure about, without first having to
// parse the draft file by hand. The JSON draft itself already carries
// this content in full (nothing here is the only copy) -- this is
// purely a readability aid, matching docs/intent-provider.md's own
// "today's plain-JSON draft is already reviewable; a nicer human-facing
// rendering is [a] polish [step], not a schema change."
//
// docs/cli-output-spec.md principle 4 ("the AI's judgment gets visual
// rank") reshaped this session: questions (blocking review items) render
// first, in their own red-accented block; assumptions and defaults merge
// into ONE purple "AI defaults — you are signing these:" block, since
// both are the same thing from a reviewer's chair -- an interpretive
// choice the human is being asked to sign off on, not two separate kinds
// of content.
func renderAmbiguity(w io.Writer, st *styler, draft *resolver.IntentFile) {
	// `ubx propose --from-doc`/`--from-diagram`'s own pre-resolve draft
	// review is out of UBI-72's scope (that ticket is `ubx plan`'s own
	// receipt specifically -- its collapsed one-liner names "the saved
	// plan file," a concept this draft-only step doesn't have yet) --
	// always the full block here, unaffected by [intent] show_defaults.
	renderAmbiguityStyled(w, st, draft.Intent.Assumptions, draft.Intent.Defaults, draft.Intent.Questions, true)
}

// renderAmbiguityStyled is renderAmbiguity's own styled/reusable core --
// renderPlanReceipt calls this directly (it already has a *styler and
// the three slices in hand from a resolved proposal's own Intent, not a
// draft IntentFile) rather than going through renderAmbiguity's
// IntentFile-shaped wrapper. showDefaults is UBI-72's own [intent]
// show_defaults/--show-defaults/--hide-defaults resolution (config.go's
// resolveShowDefaults) -- false collapses the AI-defaults block to a
// one-line count; Questions are never affected by it (see renderQuestions'
// own unconditional call below) -- a blocking ambiguity is never
// informational the way an assumption/default is, so it never collapses,
// regardless of this setting.
func renderAmbiguityStyled(w io.Writer, st *styler, assumptions, defaults []core.AmbiguityNote, questions []core.Question, showDefaults bool) {
	if len(assumptions) == 0 && len(defaults) == 0 && len(questions) == 0 {
		fmt.Fprintln(w, "no assumptions, defaults, or open questions -- the document was unambiguous.")
		return
	}
	renderQuestions(w, st, questions)
	if showDefaults {
		renderAIDefaults(w, st, assumptions, defaults)
		return
	}
	renderAIDefaultsCollapsed(w, st, assumptions, defaults)
}

// renderAIDefaults is docs/cli-output-spec.md principle 4's own titled
// block, literally: "AI defaults — you are signing these:" followed by
// one "◦"-bulleted line per assumption/default -- the two are rendered
// as one merged list (order: assumptions then defaults, both already
// human-readable AmbiguityNote text), not two separately-labeled
// sections the way this codebase's own earlier "Assumptions (N):"/
// "Defaults (N):" headers used to.
func renderAIDefaults(w io.Writer, st *styler, assumptions, defaults []core.AmbiguityNote) {
	all := mergeAmbiguityNotes(assumptions, defaults)
	if len(all) == 0 {
		return
	}
	// docs/cli-output-spec.md §v2: header bold (forceBold keeps the
	// existing purple "AI judgment" color, style.go's own doc comment on
	// why a naive nested Bold(Purple(...)) call wouldn't); one empty line
	// after the header and between every entry -- never a trailing blank
	// after the LAST entry, since every caller's own footer already
	// starts with its own leading blank line (double-blank otherwise).
	fmt.Fprintln(w, st.forceBold(st.Purple("AI defaults — you are signing these:")))
	fmt.Fprintln(w)
	for i, n := range all {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "%s %s\n", st.Purple("◦"), n.Text)
		for _, a := range n.Affects {
			fmt.Fprintf(w, "    affects: %s\n", a)
		}
	}
}

// renderAIDefaultsCollapsed is UBI-72's own [intent] show_defaults=false
// rendering: the same content renderAIDefaults would render, replaced
// with a one-line count and a pointer to how to see it in full -- never
// an omission (the full content is always in the saved plan file and the
// signed proposal itself, this is a display toggle only, see
// IntentConfig.ShowDefaults's own doc comment). Never called when the
// merged list is empty, matching renderAIDefaults' own early return.
func renderAIDefaultsCollapsed(w io.Writer, st *styler, assumptions, defaults []core.AmbiguityNote) {
	n := len(assumptions) + len(defaults)
	if n == 0 {
		return
	}
	fmt.Fprintln(w, st.Purple(fmt.Sprintf("%d AI default(s) in effect -- ubx plan --show-defaults to review, or see the saved plan file", n)))
}

// mergeAmbiguityNotes is renderAIDefaults/renderAIDefaultsCollapsed's own
// shared "assumptions then defaults, one merged list" convention,
// factored out so the collapsed count can never drift out of sync with
// what the full block would actually enumerate.
func mergeAmbiguityNotes(assumptions, defaults []core.AmbiguityNote) []core.AmbiguityNote {
	all := make([]core.AmbiguityNote, 0, len(assumptions)+len(defaults))
	all = append(all, assumptions...)
	all = append(all, defaults...)
	return all
}

// renderQuestions renders blocking-review questions in their own
// red-accented block, ABOVE the AI-defaults block (docs/cli-output-spec.md:
// "Questions (blocking) render above defaults in a red-accented block").
func renderQuestions(w io.Writer, st *styler, questions []core.Question) {
	if len(questions) == 0 {
		return
	}
	fmt.Fprintln(w, st.Red(fmt.Sprintf("Questions (%d):", len(questions))))
	for _, q := range questions {
		tag := ""
		if q.Blocking {
			tag = " " + st.Red("[blocking -- review before accepting]")
		}
		fmt.Fprintf(w, "  -%s %s\n", tag, q.Text)
		for _, a := range q.Affects {
			fmt.Fprintf(w, "      affects: %s\n", a)
		}
	}
}

// authoredSummary is the receipt's own summary line, or "" when there
// should not be one (UBI-251).
//
// A prose summary was removed in v2 as noise, the reasoning recorded at
// docs/cli-output-spec.md's own plan-receipt section: once every resource
// block renders in full, a sentence paraphrasing the resource list says
// nothing new. That judgement was correct about the summary it was made
// against and wrong as a general rule, which is the whole of why this
// exists again.
//
// The real authored summary in the corpus (sdk/conformance/golden/
// payments.json) reads "Provision a small Postgres RDS instance in the
// payments stack, modeled on the staging database but downsized for low
// initial traffic." The clause after the comma is in no resource block
// and cannot be: no amount of rendering attributes tells a reader the
// shape was derived from staging and deliberately reduced. That is
// interpretation, not paraphrase, and it is what this renders.
//
// SOURCE-GATED, and that is the load-bearing part. Three paths write a
// mechanical template into the same field: scan's "adopt existing
// <address> into the ledger (discovered by scan)" and "record drift on
// <address> observed outside the ledger", and restore's "restore <stack>
// to ledger head <hash>". Rendering those would print exactly the
// paraphrase-of-one-resource that v2 removed, so the rule is a positive
// allow-list of authored and AI-derived source kinds rather than a
// blocklist of the paths that exist today.
//
// Proposal.Kind is NOT the discriminator, though it looks like one.
// `ubx restore` builds a KindChange proposal with a mechanical summary
// (cli/restore.go), so gating on kind would print it. The source kind is
// what actually separates them.
//
// Promotion keeps working because cli/promote.go APPENDS its own
// {Kind: "promotion"} source to the ones already there rather than
// replacing them, so a promoted proposal still carries the document or
// dialogue it was authored from and still shows its summary.
func authoredSummary(in core.Intent) string {
	if !hasAuthoredSource(in) {
		return ""
	}
	return firstParagraph(in.Summary)
}

// hasAuthoredSource reports whether any of the intent's sources is one a
// person or the model actually wrote.
//
// The machine-derived kinds are the rest of what the codebase writes:
// live_state (scan), restore, promotion, cloudtrail, gcp_audit,
// cloudtrail_unattributed, audit_unattributed. Scan, adopt and revert
// populate no sources at all, so they fall out here too.
//
// IntentSource.Kind's own doc comment also lists "manual_edit" and
// "issue". Neither is written anywhere in the tree today, so neither is
// listed here: an allow-list should name what exists, and a kind that
// starts being written can be added alongside the code that writes it.
func hasAuthoredSource(in core.Intent) bool {
	for _, s := range in.Sources {
		switch s.Kind {
		case core.SourceKindDocument, core.SourceKindDialogue, core.SourceKindIntentProvider:
			return true
		}
	}
	return false
}

// firstParagraph returns everything up to the first blank line.
//
// The marketing design shows two paragraphs, and the second one reads
// "The replica is the largest share of the cost increase." That is a cost
// claim, and ubx has no pricing source (UBI-251's own cost arc), so a
// model writing it would be asserting something the binary cannot
// compute. Rendering only the first paragraph is what keeps the receipt
// to what is actually known, and it costs nothing today since every
// summary in the corpus is a single paragraph already.
func firstParagraph(s string) string {
	s = strings.TrimSpace(s)
	for _, sep := range []string{"\n\n", "\r\n\r\n"} {
		if i := strings.Index(s, sep); i >= 0 {
			s = s[:i]
		}
	}
	return strings.TrimSpace(s)
}

// isPricedCostDelta reports whether a cost delta carries a real figure,
// as opposed to the placeholder zero every writer sets today (UBI-251).
//
// MonthlyUSD is json.RawMessage holding a union per docs/schema.md's
// ratified number rule: a bare JSON integer, or a JSON string for
// anything fractional, never a float literal. So "is this zero" has more
// than one spelling to check, and the string form has quotes.
//
// Absent and zero are treated identically on purpose. Both mean the same
// thing right now, which is that nothing priced this change, and a
// receipt should not distinguish two flavours of "unknown" to a reader.
// A genuine, computed zero is indistinguishable from the placeholder
// today; when a pricing source exists it can say so explicitly rather
// than by writing the same byte the placeholder writes.
func isPricedCostDelta(c core.CostDelta) bool {
	raw := strings.TrimSpace(string(c.MonthlyUSD))
	switch raw {
	case "", "0", `"0"`, "null", `""`:
		return false
	}
	return true
}

// renderPinnedHeads shows which neighbour ledger head each cross-stack
// reference resolved against (UBI-251).
//
// This is here because the marketing design wanted the sentence "pinned
// to the network stack at head 4b1e77" in the summary paragraph, and the
// summary is the wrong place for it. Intent.Summary is written by the
// author's own program, before resolution computes any head, and nothing
// rewrites it afterwards, so an authored summary can never carry a head
// hash. The fact itself is real and already on the proposal: the
// resolver records {Kind: "cross_stack_pin", LedgerDir, PinnedHead} into
// Resolution.Inputs, which renderPlanReceipt already holds.
//
// So the receipt states it directly rather than hoping prose does. A
// proposal recording which neighbour head it resolved against is the
// ledger argument in one line, and it was previously visible only in
// `ubx why`'s pin chain, after the fact, or as JSON from
// `ubx addresses`. It belongs on the receipt a reader is signing.
//
// Deduplicated by (ledger, head): several references into one neighbour
// stack all pin the same head, and repeating the line once per reference
// would say the same thing five times.
func renderPinnedHeads(out io.Writer, st *styler, inputs []core.ResolutionInput) {
	seen := map[string]bool{}
	for _, in := range inputs {
		if in.Kind != "cross_stack_pin" || in.PinnedHead == "" {
			continue
		}
		// LedgerDir names the neighbour; fall back to the resource
		// address when a caller recorded a pin without one, so a pin is
		// never silently invisible.
		where := in.LedgerDir
		if where == "" {
			where = in.Resource
		}
		key := where + "\x00" + in.PinnedHead
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintln(out, st.Bold(fmt.Sprintf("pinned: %s @ %s", where, st.Hash(in.PinnedHead))))
	}
}

// renderOrphanCheck surfaces a destroy's own cross_stack_orphan_check
// evidence, at the two moments a human is deciding whether to destroy
// something: the plan receipt, and the ship confirmation just above the
// prompt.
//
// This evidence has existed since UBI-30 and was visible nowhere. The
// resolver records "not_performed" precisely so the gap is never
// silently indistinguishable from a real check (docs/resolver.md), and
// the whole point of recording it is that a human reviewing and signing
// a destroy sees it. Grepped before writing this: "not_performed"
// appeared only in the resolver that writes it and in doc comments.
// renderPinnedHeads above rendered cross_stack_pin entries and skipped
// every other kind, `ubx terminate`'s receipt said nothing, and neither
// did `ubx ship --confirm-terminate`'s confirmation. It reached the
// proposal JSON and `ubx why --json` and stopped there, which inverted
// the audience: an assistant reading raw JSON could see it and the
// person signing could not. A gap recorded for a human's benefit and
// shown only in JSON reads as diligence to whoever wrote it while
// protecting nobody.
//
// checked_clear is rendered too, not just the warning. Showing only the
// gap would leave a clean check and a version of ubx that does not
// perform one looking identical from the output, which is the same
// indistinguishability this is here to remove.
func renderOrphanCheck(out io.Writer, st *styler, inputs []core.ResolutionInput) {
	var unchecked []string
	var cleared []string
	var checkedDirs []string
	seenDir := map[string]bool{}
	for _, in := range inputs {
		if in.Kind != "cross_stack_orphan_check" {
			continue
		}
		switch in.Status {
		case "not_performed":
			unchecked = append(unchecked, in.Resource)
		case "checked_clear":
			cleared = append(cleared, in.Resource)
			for _, dir := range in.CheckedLedgerDirs {
				if !seenDir[dir] {
					seenDir[dir] = true
					checkedDirs = append(checkedDirs, dir)
				}
			}
		}
	}

	if len(cleared) > 0 {
		fmt.Fprintf(out, "%s no stack in %s references %s\n",
			st.Green("cross-stack check:"), strings.Join(checkedDirs, ", "), joinAddresses(cleared))
	}
	if len(unchecked) > 0 {
		fmt.Fprintf(out, "%s no dependent stacks were checked, so another stack may still reference %s -- set known_dependents in .ubx/config, or pass --known-dependent <ledger_dir>, to check before destroying\n",
			st.Yellow("warning:"), joinAddresses(unchecked))
	}
}

// joinAddresses renders a destroy list for one line of prose: every
// address when there are few, a count plus the first two when there are
// many, so a bulk terminate does not push the remedy off the screen.
func joinAddresses(addrs []string) string {
	if len(addrs) <= 3 {
		return strings.Join(addrs, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(addrs[:2], ", "), len(addrs)-2)
}
