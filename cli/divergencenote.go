package cli

import (
	"fmt"
	"io"
	"sort"

	"github.com/ubiquex/ubiquex/core"
)

// divergencenote.go tells a plan reader that some of what they are about
// to change was put where it is on purpose.
//
// A stack's declared source says what a resource should be. A restore and
// a drift_revert deliberately move a resource away from that, and
// afterwards the declaration and the ledger disagree by design. The next
// plan reads the declaration, sees live state differs, and proposes
// changing it back, correctly and with no idea the difference was chosen.
//
// So a restore someone shipped on purpose gets undone by the next routine
// plan. What makes that dangerous is not that the plan is wrong, it is
// that the plan looks completely ordinary. The person running it was
// expecting nothing to happen, which is exactly the frame of mind in which
// a diff gets skimmed and accepted.
//
// # What this says, and what it refuses to say
//
// It does not say "this would undo your restore". That cannot be said
// safely: a plan touching one attribute of a diverged resource is not
// necessarily reversing the divergence, and several changes later the
// claim is a guess.
//
// It says the thing that is always true, and leaves the conclusion where
// it belongs: the most recent change to this resource was a restore (or a
// revert), here is which one and when. A reader who meant to move forward
// reads it and continues. A reader who did not now knows before accepting
// rather than afterwards.
//
// An overclaiming marker on a legitimate move-forward is how people learn
// to skip a warning, and this one has to keep working after being read
// many times.

// divergedResource is one address a plan touches whose current state was
// set deliberately.
type divergedResource struct {
	Address core.Address
	core.Divergence
}

// planTouches is every address a proposal would change. Destroys count:
// destroying something a restore put back is the case where a silent
// undo costs the most.
func planTouches(p *core.Proposal) []core.Address {
	seen := map[core.Address]bool{}
	var out []core.Address
	add := func(a core.Address) {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for i := range p.Delta.Modifies {
		add(p.Delta.Modifies[i].Target)
	}
	for i := range p.Delta.Destroys {
		add(p.Delta.Destroys[i].Address)
	}
	// Creates are deliberately excluded: an address a plan CREATES holds
	// nothing yet, so there is no current state for anything to have set
	// deliberately. Including them would mean reporting a divergence
	// about a resource that does not exist.
	return out
}

// divergencesInPlan finds which of a plan's targets are currently held by
// a deliberate divergence.
func divergencesInPlan(l *core.Ledger, p *core.Proposal) ([]divergedResource, error) {
	var found []divergedResource
	for _, addr := range planTouches(p) {
		d, ok, err := l.LastDivergence(addr)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		found = append(found, divergedResource{Address: addr, Divergence: d})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Address.String() < found[j].Address.String() })
	return found, nil
}

// describeDivergence renders one divergence as a phrase, without claiming
// what changing it would mean.
func describeDivergence(st *styler, d core.Divergence) string {
	switch d.Kind {
	case core.DivergenceRestore:
		return fmt.Sprintf("restore to head %s", st.Hash(d.TargetHead))
	case core.DivergenceDriftRevert:
		return "drift revert"
	default:
		return string(d.Kind)
	}
}

// writeDivergenceNote renders the section, or nothing at all when no
// target was deliberately placed.
func writeDivergenceNote(w io.Writer, st *styler, found []divergedResource) {
	if len(found) == 0 {
		return
	}
	// Agreement matters here because the singular case is the common one
	// and "1 resource were last changed" reads as a bug in the tool,
	// which is not what a reader should be thinking about at this point.
	noun, verb, pronoun := "resource", "was", "it"
	if len(found) > 1 {
		noun, verb, pronoun = "resources", "were", "them"
	}
	fmt.Fprintf(w, "  %d %s here %s last changed deliberately, away from what your declaration says:\n",
		len(found), noun, verb)

	width := 0
	for _, f := range found {
		if n := cellWidth(f.Address.String()); n > width {
			width = n
		}
	}
	for _, f := range found {
		line := fmt.Sprintf("    %s  %s", padStyled(st.Yellow(f.Address.String()), width), describeDivergence(st, f.Divergence))
		if f.ResolvedAt != "" {
			line += fmt.Sprintf(" (%s)", f.ResolvedAt)
		}
		fmt.Fprintln(w, line)
	}
	// The conclusion is the reader's. This says what happened, never what
	// it means to change it again, because moving forward after a restore
	// is an ordinary thing to do and a marker that treated it as a
	// mistake would be wrong more often than right.
	subject := "they are"
	if len(found) == 1 {
		subject = "it is"
	}
	fmt.Fprintf(w, "\n  This plan changes %s again. If that is what you want, nothing here is wrong.\n"+
		"  If it is not, the declaration %s compared against is what to change.\n\n", pronoun, subject)
}
