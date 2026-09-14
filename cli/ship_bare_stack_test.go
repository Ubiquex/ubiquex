package cli

import (
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestOtherStacksNote covers bare `ubx ship` finding no plans for the
// stack it was told to look under, while plans for other stacks sit in
// the same store.
//
// The two stack names come from different places and nothing reconciles
// them. `ubx plan` files a plan under the stack THE DOCUMENT declares,
// which for a .ubx.hcl file is its own `stack = "..."` attribute. Bare
// ship looks it up by the stack .ubx/config declares. When they
// disagree, a perfectly shippable plan is invisible, and the message
// said only "no unshipped plans", which reads as "nothing was saved".
//
// Found with five shippable plans on disk and the error reporting none
// of them.
func TestOtherStacksNote(t *testing.T) {
	plans := func(stacks ...string) []planCandidate {
		out := make([]planCandidate, 0, len(stacks))
		for i, s := range stacks {
			out = append(out, planCandidate{
				Hash: strings.Repeat(string(rune('a'+i)), 8),
				P:    &core.Proposal{Stack: s},
			})
		}
		return out
	}

	t.Run("names the stack that actually has the plans", func(t *testing.T) {
		got := otherStacksNote(plans("demo"), "bp2")
		for _, want := range []string{`"demo"`, "1 plan(s)", `--stack "demo"`} {
			if !strings.Contains(got, want) {
				t.Fatalf("note is missing %q: %q", want, got)
			}
		}
	})

	t.Run("several stacks are listed, sorted", func(t *testing.T) {
		got := otherStacksNote(plans("zeta", "alpha", "alpha"), "bp2")
		if !strings.Contains(got, `"alpha", "zeta"`) {
			t.Fatalf("expected both stacks, deduplicated and sorted: %q", got)
		}
		// The suggestion has to name a real stack, not the missing one.
		if !strings.Contains(got, `--stack "alpha"`) {
			t.Fatalf("suggestion does not name a stack that has plans: %q", got)
		}
	})

	t.Run("nothing to add stays empty", func(t *testing.T) {
		// A genuinely empty store, and a store holding only the stack
		// that was asked for, both have nothing useful to say. The
		// second cannot happen through resolveBareShipTarget (it would
		// have matched) but the helper must not invent a note for it.
		if got := otherStacksNote(nil, "bp2"); got != "" {
			t.Fatalf("expected no note for an empty store: %q", got)
		}
		if got := otherStacksNote(plans("bp2"), "bp2"); got != "" {
			t.Fatalf("expected no note when only the wanted stack is present: %q", got)
		}
	})

	t.Run("a plan with no stack is not offered", func(t *testing.T) {
		// Suggesting `--stack ""` would be worse than saying nothing.
		if got := otherStacksNote(plans(""), "bp2"); got != "" {
			t.Fatalf("expected no note for a stackless plan: %q", got)
		}
	})
}
