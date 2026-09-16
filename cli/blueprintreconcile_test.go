package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
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
		"before ubx recorded declarations",        // what happened
		"cannot be turned back into a source",     // why it cannot be fixed by looking harder
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

// TestStackDeclarations_ReadsBothDeclarationSites is the regression test
// for a false "missing" found by walking a real HCL stack.
//
// The config table is not the only declaration site. An HCL stack
// declares a blueprint inline on the block, with source/version/path and
// no table entry at all, and `ubx restore` never sees that file: it is
// handed a head, not a document. So reading only the table meant every
// blueprint in such a stack was reported as a missing entry, advising a
// reader to add something already present.
//
// That is the one line this report cannot be wrong about without being
// actively misleading, since "add an entry" is advice someone acts on.
func TestStackDeclarations_ReadsBothDeclarationSites(t *testing.T) {
	lock := &blueprint.StackLock{Stacks: map[string]map[string]blueprint.LockEntry{
		"payments": {
			"rev-bp":  {Source: "oci://ghcr.io/ubx-blueprints/rev-bp:v1.0.0", ContentHash: "sha256:aaa"},
			"network": {Source: "oci://ghcr.io/ubx-blueprints/network:v1", ContentHash: "sha256:bbb"},
		},
		"other-stack": {"unrelated": {Source: "oci://x/y:v1"}},
	}}

	got := stackDeclarations(map[string]string{"ci-platform": "oci://ghcr.io/x/ci:v2"}, lock, "payments")

	// From the table, as before.
	if d := got["ci-platform"]; d.Source != "oci://ghcr.io/x/ci:v2" || d.FromLock {
		t.Errorf("ci-platform = %+v, want the table's own entry", d)
	}
	// From the lock, which is how an inline HCL declaration is visible.
	if d := got["rev-bp"]; d.Source != "oci://ghcr.io/ubx-blueprints/rev-bp:v1.0.0" || !d.FromLock {
		t.Errorf("rev-bp = %+v, want the lock's entry marked as such", d)
	}
	// Another stack's lock entries are not this stack's declarations.
	if _, present := got["unrelated"]; present {
		t.Error("a different stack's lock entry leaked into this stack's declarations")
	}
}

// TestStackDeclarations_TableWinsForANameInBoth: the table is the file a
// reader edits, so it stays authoritative for what to compare against.
// The lock only fills in names the table does not mention.
func TestStackDeclarations_TableWinsForANameInBoth(t *testing.T) {
	lock := &blueprint.StackLock{Stacks: map[string]map[string]blueprint.LockEntry{
		"payments": {"ci-platform": {Source: "oci://ghcr.io/x/ci:v1"}},
	}}
	got := stackDeclarations(map[string]string{"ci-platform": "oci://ghcr.io/x/ci:v2"}, lock, "payments")
	if d := got["ci-platform"]; d.Source != "oci://ghcr.io/x/ci:v2" || d.FromLock {
		t.Errorf("ci-platform = %+v, want the table's v2 rather than the lock's v1", d)
	}
}

// TestStackDeclarations_NoLockIsNotAFailure: a stack with no lock at all
// is the ordinary case for a table-declared stack that has never called a
// remote blueprint.
func TestStackDeclarations_NoLockIsNotAFailure(t *testing.T) {
	got := stackDeclarations(map[string]string{"ci-platform": "oci://ghcr.io/x/ci:v2"}, nil, "payments")
	if len(got) != 1 || got["ci-platform"].Source != "oci://ghcr.io/x/ci:v2" {
		t.Errorf("declarations = %+v", got)
	}
}

// TestWriteBlueprintReconcile_NamesWhereTheDeclarationIs: a reader whose
// stack declares inline has no blueprints table, and telling them "your
// table says" sends them to a file that does not mention this blueprint.
func TestWriteBlueprintReconcile_NamesWhereTheDeclarationIs(t *testing.T) {
	fromTable := renderReconcile(t, blueprintReconcile{UsedAll: 1, Differs: []blueprintDiff{
		{Name: "ci-platform", Declared: "oci://x/ci:v2", Used: "oci://x/ci:v1"},
	}})
	if !strings.Contains(fromTable, "your table says") {
		t.Errorf("a table-declared difference does not name the table:\n%s", fromTable)
	}

	fromLock := renderReconcile(t, blueprintReconcile{UsedAll: 1, Differs: []blueprintDiff{
		{Name: "rev-bp", Declared: "oci://x/rev:v2", Used: "oci://x/rev:v1", FromLock: true},
	}})
	if strings.Contains(fromLock, "your table says") {
		t.Errorf("an inline declaration was attributed to a table the stack does not have:\n%s", fromLock)
	}
	if !strings.Contains(fromLock, "your stack declares") {
		t.Errorf("an inline difference does not say where the declaration is:\n%s", fromLock)
	}
}

// TestWriteBlueprintReconcile_SaysWhereItLooked: the report's own scope
// statement has to name both declaration sites now, or a reader with an
// inline stack still cannot tell whether their file was consulted.
func TestWriteBlueprintReconcile_SaysWhereItLooked(t *testing.T) {
	out := renderReconcile(t, blueprintReconcile{UsedAll: 1, Missing: []blueprintUse{
		{Name: "network", Declarations: []string{"oci://x/net:v1"}},
	}})
	for _, want := range []string{"looked in:", "blueprints table", "blueprints.lock", "inline on an HCL block"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not say it looked in %q:\n%s", want, out)
		}
	}
}
