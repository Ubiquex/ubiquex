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
	renderAmbiguityStyled(w, st, draft.Intent.Assumptions, draft.Intent.Defaults, draft.Intent.Questions)
}

// renderAmbiguityStyled is renderAmbiguity's own styled/reusable core --
// renderPlanReceipt calls this directly (it already has a *styler and
// the three slices in hand from a resolved proposal's own Intent, not a
// draft IntentFile) rather than going through renderAmbiguity's
// IntentFile-shaped wrapper.
func renderAmbiguityStyled(w io.Writer, st *styler, assumptions, defaults []core.AmbiguityNote, questions []core.Question) {
	if len(assumptions) == 0 && len(defaults) == 0 && len(questions) == 0 {
		fmt.Fprintln(w, "no assumptions, defaults, or open questions -- the document was unambiguous.")
		return
	}
	renderQuestions(w, st, questions)
	renderAIDefaults(w, st, assumptions, defaults)
}

// renderAIDefaults is docs/cli-output-spec.md principle 4's own titled
// block, literally: "AI defaults — you are signing these:" followed by
// one "◦"-bulleted line per assumption/default -- the two are rendered
// as one merged list (order: assumptions then defaults, both already
// human-readable AmbiguityNote text), not two separately-labeled
// sections the way this codebase's own earlier "Assumptions (N):"/
// "Defaults (N):" headers used to.
func renderAIDefaults(w io.Writer, st *styler, assumptions, defaults []core.AmbiguityNote) {
	all := make([]core.AmbiguityNote, 0, len(assumptions)+len(defaults))
	all = append(all, assumptions...)
	all = append(all, defaults...)
	if len(all) == 0 {
		return
	}
	fmt.Fprintln(w, st.Purple("AI defaults — you are signing these:"))
	for _, n := range all {
		fmt.Fprintf(w, "%s %s\n", st.Purple("◦"), n.Text)
		for _, a := range n.Affects {
			fmt.Fprintf(w, "    affects: %s\n", a)
		}
	}
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
