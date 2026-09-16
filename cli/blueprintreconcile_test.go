package cli

import (
	"bytes"
	"strings"
	"testing"
)

// renderReconcile runs the renderer with colour off, as a pipe would.
func renderReconcile(t *testing.T, r blueprintReconcile) string {
	t.Helper()
	var buf bytes.Buffer
	writeBlueprintReconcile(&buf, &styler{}, r)
	return buf.String()
}

// TestWriteBlueprintReconcile_AlwaysSaysWhatItChecked is the property that
// matters most here, so it is asserted on every shape rather than once.
//
// Arguments are not in the ledger in either language: expanding a call
// records its result and not its arguments. So someone can match every
// line this prints and still get a different plan, which makes an
// unqualified report actively misleading rather than merely incomplete.
func TestWriteBlueprintReconcile_AlwaysSaysWhatItChecked(t *testing.T) {
	shapes := map[string]blueprintReconcile{
		"everything matches": {UsedAll: 2},
		"a version differs": {UsedAll: 1, Differs: []blueprintDiff{
			{Name: "ci-platform", Declared: "oci://x/ci:v2", Used: "oci://x/ci:v1"},
		}},
		"an entry is missing": {UsedAll: 1, Missing: []blueprintUse{
			{Name: "network", Declarations: []string{"oci://x/net:v1"}},
		}},
		"an entry is extra": {UsedAll: 0, Extra: []string{"legacy-vpc"}},
		"a name has two sources": {UsedAll: 1, Conflict: []blueprintUse{
			{Name: "ci-platform", Declarations: []string{"oci://x/ci:v1", "oci://x/ci:v2"}},
		}},
		"nothing was recorded": {UsedAll: 1, Unknown: []blueprintUse{
			{Name: "ci-platform", Undeclared: 3},
		}},
	}
	for name, r := range shapes {
		t.Run(name, func(t *testing.T) {
			out := renderReconcile(t, r)
			if out == "" {
				t.Fatal("a restore that used blueprints must say something about them")
			}
			for _, want := range []string{"checked:", "not checked:", "arguments"} {
				if !strings.Contains(out, want) {
					t.Errorf("the scope of the check is not stated, so this reads as complete:\n%s", out)
					break
				}
			}
			if !strings.Contains(out, "does not guarantee") {
				t.Errorf("output does not say matching it is insufficient:\n%s", out)
			}
		})
	}
}

// TestWriteBlueprintReconcile_ThinAnswerReadsAsThin: a head resolved
// before declarations existed can only be partly checked, and silence
// about that would read as a clean bill.
func TestWriteBlueprintReconcile_ThinAnswerReadsAsThin(t *testing.T) {
	out := renderReconcile(t, blueprintReconcile{
		UsedAll: 1,
		Unknown: []blueprintUse{{Name: "ci-platform", Undeclared: 3}},
	})
	for _, want := range []string{
		"before ubx recorded declarations", // what happened
		"cannot be turned back into a source", // why it cannot be fixed by looking harder
		"unchecked rather than confirmed correct", // what the reader should conclude
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not say %q:\n%s", want, out)
		}
	}
	// And it must not claim a match.
	if strings.Contains(out, "match your table") {
		t.Errorf("an unchecked entry was reported as matching:\n%s", out)
	}
}

// TestWriteBlueprintReconcile_SaysItEditsNothing: the missing-entry case
// is the one the config cascade cannot resolve, and a reader should know
// that is why nothing was written for them rather than assuming an
// oversight.
func TestWriteBlueprintReconcile_SaysItEditsNothing(t *testing.T) {
	out := renderReconcile(t, blueprintReconcile{
		UsedAll: 1,
		Missing: []blueprintUse{{Name: "network", Declarations: []string{"oci://x/net:v1"}}},
	})
	if !strings.Contains(out, "nothing is edited for you") {
		t.Errorf("output does not say it edits nothing:\n%s", out)
	}
	if !strings.Contains(out, "which file should own") {
		t.Errorf("output does not say why the dropped case cannot be edited:\n%s", out)
	}
}

// TestWriteBlueprintReconcile_CleanRunStillStatesScope: when everything
// matches, the argument gap is the ONLY remaining reason a plan could
// diverge, so it is the one case where saying so matters most.
func TestWriteBlueprintReconcile_CleanRunStillStatesScope(t *testing.T) {
	out := renderReconcile(t, blueprintReconcile{UsedAll: 3})
	if !strings.Contains(out, "the 3 this head used match your table") {
		t.Errorf("clean run does not report what it compared:\n%s", out)
	}
	if !strings.Contains(out, "not checked:") {
		t.Errorf("a clean run is exactly where the unchecked half must still be named:\n%s", out)
	}
}

// TestWriteBlueprintReconcile_SilentWhenNoBlueprints: a stack that uses
// none should get no section at all, rather than a paragraph explaining
// that nothing was checked about nothing.
func TestWriteBlueprintReconcile_SilentWhenNoBlueprints(t *testing.T) {
	if out := renderReconcile(t, blueprintReconcile{}); out != "" {
		t.Errorf("a stack with no blueprints got a blueprints section:\n%s", out)
	}
}
