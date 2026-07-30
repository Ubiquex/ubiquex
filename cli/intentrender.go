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
func renderAmbiguity(w io.Writer, draft *resolver.IntentFile) {
	if len(draft.Intent.Assumptions) == 0 && len(draft.Intent.Defaults) == 0 && len(draft.Intent.Questions) == 0 {
		fmt.Fprintln(w, "no assumptions, defaults, or open questions -- the document was unambiguous.")
		return
	}

	renderNotes(w, "Assumptions", draft.Intent.Assumptions)
	renderNotes(w, "Defaults", draft.Intent.Defaults)
	renderQuestions(w, draft.Intent.Questions)
}

func renderNotes(w io.Writer, label string, notes []core.AmbiguityNote) {
	if len(notes) == 0 {
		return
	}
	fmt.Fprintf(w, "%s (%d):\n", label, len(notes))
	for _, n := range notes {
		fmt.Fprintf(w, "  - %s\n", n.Text)
		for _, a := range n.Affects {
			fmt.Fprintf(w, "      affects: %s\n", a)
		}
	}
}

func renderQuestions(w io.Writer, questions []core.Question) {
	if len(questions) == 0 {
		return
	}
	fmt.Fprintf(w, "Questions (%d):\n", len(questions))
	for _, q := range questions {
		tag := ""
		if q.Blocking {
			tag = " [blocking -- review before accepting]"
		}
		fmt.Fprintf(w, "  -%s %s\n", tag, q.Text)
		for _, a := range q.Affects {
			fmt.Fprintf(w, "      affects: %s\n", a)
		}
	}
}
