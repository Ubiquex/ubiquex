package cli

import (
	"fmt"
	"io"

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
	fmt.Fprintln(w, st.Purple(fmt.Sprintf("%d AI default(s) applied -- ubx plan --show-defaults to review, or see the saved plan file", n)))
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
