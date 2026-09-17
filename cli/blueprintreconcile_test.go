package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core"
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
	if !strings.Contains(out, "the 3 this head used match what this stack declares") {
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

// TestReconcile_PairsOnContentHashAcrossDifferentNames is the regression
// test for a double report found by walking a real HCL stack.
//
// One blueprint appeared as two problems: missing under the ledger's
// name, unused under the lock's. Same source, same bytes, two names.
//
// A ledger ref carries the blueprint's own PACKAGED name, from its
// blueprint.lock.json. A lock entry is keyed by the name the CALL used,
// derived from the source's last path segment. For an OCI repository
// called rev-bp holding a blueprint packaged as bp, those differ, and
// the report had no way to know they were one thing.
func TestReconcile_PairsOnContentHashAcrossDifferentNames(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_sqs_queue", Name: "q"}
	const (
		hash   = "sha256:aaa111bbb222"
		source = "oci://ghcr.io/ubx-blueprints/rev-bp:v1.0.0"
	)
	// The ledger records the PACKAGED name.
	seedAdoptionWithSource(t, l, addr, core.IntentSource{
		Kind: "blueprint", Ref: "bp:" + hash,
		Declaration: source, DeclaredSource: source,
	})
	// The lock records the CALLED name, for the same bytes.
	declared := map[string]declaredSource{
		"rev-bp": {Source: source, ContentHash: hash, FromLock: true},
	}

	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	r, err := reconcileBlueprints(l, head, "payments", declared)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(r.Missing) != 0 {
		t.Errorf("a declared blueprint was reported missing under its packaged name: %+v", r.Missing)
	}
	if len(r.Extra) != 0 {
		t.Errorf("the same blueprint was reported unused under its called name: %v", r.Extra)
	}
	if len(r.Differs) != 0 {
		t.Errorf("the sources are identical, so nothing differs: %+v", r.Differs)
	}
	if !r.Empty() {
		t.Errorf("one blueprint, declared and used, should produce no findings at all: %+v", r)
	}
}

// TestReconcile_VersionChangeIsOneLineNotTwo is the case this report
// exists for, and the case the first pairing got wrong.
//
// A blueprint whose version moved has DIFFERENT bytes on the two sides by
// construction, so pairing on the content hash finds nothing precisely
// when there is something to say. Both sides then fell back to names that
// disagree, and one blueprint was reported twice: missing under the
// ledger's packaged name, unused under the lock's called name.
//
// An earlier version of this test asserted that double report as the
// honest answer. It was not honest, it was the bug, written down as
// intent. The correct answer is one line naming a version that moved.
func TestReconcile_VersionChangeIsOneLineNotTwo(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_sqs_queue", Name: "q"}
	seedAdoptionWithSource(t, l, addr, core.IntentSource{
		Kind: "blueprint", Ref: "bp:sha256:oldbytes",
		Declaration:    "oci://ghcr.io/ubx-blueprints/rev-bp:v1.0.0",
		DeclaredSource: "oci://ghcr.io/ubx-blueprints/rev-bp:v1.0.0",
	})
	declared := map[string]declaredSource{
		"rev-bp": {Source: "oci://ghcr.io/ubx-blueprints/rev-bp:v2.0.0", ContentHash: "sha256:newbytes", FromLock: true},
	}

	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	r, err := reconcileBlueprints(l, head, "payments", declared)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(r.Missing) != 0 || len(r.Extra) != 0 {
		t.Fatalf("one blueprint whose version moved was reported twice: missing=%+v extra=%v", r.Missing, r.Extra)
	}
	if len(r.Differs) != 1 {
		t.Fatalf("a moved version must be reported as a difference: %+v", r.Differs)
	}
	d := r.Differs[0]
	if d.Declared != "oci://ghcr.io/ubx-blueprints/rev-bp:v2.0.0" || d.Used != "oci://ghcr.io/ubx-blueprints/rev-bp:v1.0.0" {
		t.Errorf("the difference does not name both versions: %+v", d)
	}
}

// TestReconcile_ADifferentBlueprintIsStillTwoLines: pairing must not
// become a way of agreeing with everything. Two unrelated blueprints are
// genuinely a head using one thing and a stack declaring another.
func TestReconcile_ADifferentBlueprintIsStillTwoLines(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_sqs_queue", Name: "q"}
	seedAdoptionWithSource(t, l, addr, core.IntentSource{
		Kind: "blueprint", Ref: "bp:sha256:oldbytes",
		Declaration:    "oci://ghcr.io/ubx-blueprints/rev-bp:v1.0.0",
		DeclaredSource: "oci://ghcr.io/ubx-blueprints/rev-bp:v1.0.0",
	})
	// A different repository entirely, not a different version of one.
	declared := map[string]declaredSource{
		"other-bp": {Source: "oci://ghcr.io/ubx-blueprints/other-bp:v1.0.0", ContentHash: "sha256:otherbytes", FromLock: true},
	}
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	r, err := reconcileBlueprints(l, head, "payments", declared)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(r.Missing) != 1 || len(r.Extra) != 1 {
		t.Fatalf("two unrelated blueprints must stay two findings: missing=%+v extra=%v differs=%+v",
			r.Missing, r.Extra, r.Differs)
	}
}

// TestReconcile_TableDeclarationsStillPairByName: a table-declared
// blueprint carries no hash and needs none, because resolveOne REQUIRES
// the pulled blueprint's packaged name to match the declared one. Those
// two names cannot differ, so the name is an exact key there.
func TestReconcile_TableDeclarationsStillPairByName(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_sqs_queue", Name: "q"}
	seedAdoptionWithSource(t, l, addr, core.IntentSource{
		Kind: "blueprint", Ref: "ci-platform:sha256:abc",
		Declaration: "oci://ghcr.io/x/ci:v2", DeclaredSource: "oci://ghcr.io/x/ci:v2",
	})
	declared := map[string]declaredSource{
		"ci-platform": {Source: "oci://ghcr.io/x/ci:v2"}, // no hash: from the config table
	}

	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	r, err := reconcileBlueprints(l, head, "payments", declared)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !r.Empty() {
		t.Errorf("a table-declared blueprint in use should produce no findings: %+v", r)
	}
}

// seedAdoptionWithSource records one resource carrying the given
// provenance, so a reconciliation has a real folded head to read rather
// than a hand-built result value.
func seedAdoptionWithSource(t *testing.T, l *core.Ledger, addr core.Address, src core.IntentSource) {
	t.Helper()
	state := json.RawMessage(`{"id":"q-1"}`)
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"state": state, "sources": []core.IntentSource{src},
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := core.ObservedHash(state)
	if err != nil {
		t.Fatal(err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := core.Accept(l, &core.Proposal{
		SchemaVersion: core.SchemaVersion, Stack: addr.Stack, Parent: head,
		Kind: core.KindAdoption, Intent: core.Intent{Summary: "seed " + addr.Name},
		Delta: core.Delta{Creates: []json.RawMessage{node}},
		Resolution: core.Resolution{ResolvedAt: now, Inputs: []core.ResolutionInput{
			{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"q-1"}`)},
		}},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)}, Status: core.StatusDraft,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestWriteBlueprintReconcile_DroppedExplanationOnlyWhenDropped: the
// explanation names a situation, and printing it beside a report that
// does not contain that situation sends a reader looking for a dropped
// blueprint there is not one of.
func TestWriteBlueprintReconcile_DroppedExplanationOnlyWhenDropped(t *testing.T) {
	versionOnly := renderReconcile(t, blueprintReconcile{UsedAll: 1, Differs: []blueprintDiff{
		{Name: "rev-bp", Declared: "oci://x/rev:v2", Used: "oci://x/rev:v1", FromLock: true},
	}})
	if strings.Contains(versionOnly, "dropped blueprint") {
		t.Errorf("a version mismatch explains a dropped blueprint that is not in this report:\n%s", versionOnly)
	}
	// It must still say nothing was edited, since that is true of every
	// report and is what stops a reader assuming a fix was applied.
	if !strings.Contains(versionOnly, "nothing is edited for you") {
		t.Errorf("a version mismatch no longer says it edited nothing:\n%s", versionOnly)
	}

	dropped := renderReconcile(t, blueprintReconcile{UsedAll: 1, Missing: []blueprintUse{
		{Name: "network", Declarations: []string{"oci://x/net:v1"}},
	}})
	for _, want := range []string{"unmanaged", "which file should own"} {
		if !strings.Contains(dropped, want) {
			t.Errorf("the case the explanation is for does not carry it, missing %q:\n%s", want, dropped)
		}
	}
}

// TestWriteBlueprintReconcile_NamesBothDeclarationSitesInTheHeader: the
// header said "your table", which is the same error the missing-entry
// line made before it learned to read the lock. A reader whose stack
// declares inline has no table, and a heading naming one sends them to a
// file that does not mention this blueprint.
func TestWriteBlueprintReconcile_NamesBothDeclarationSitesInTheHeader(t *testing.T) {
	for name, r := range map[string]blueprintReconcile{
		"a difference": {UsedAll: 1, Differs: []blueprintDiff{{Name: "rev-bp", Declared: "a", Used: "b", FromLock: true}}},
		"a clean run":  {UsedAll: 2},
	} {
		t.Run(name, func(t *testing.T) {
			out := renderReconcile(t, r)
			if strings.Contains(out, "your table") {
				t.Errorf("the header names only the config table:\n%s", out)
			}
			if !strings.Contains(out, "this stack declares") {
				t.Errorf("the header does not name what it compared:\n%s", out)
			}
		})
	}
}

// TestWriteBlueprintReconcile_DoesNotClaimArgumentsAreUnrecorded: the
// scope statement explained that expanding a call records its result and
// not its arguments. That described a system that has since changed, and
// an HCL call's arguments are in the ledger now.
//
// The claim worth making is about this report, which does not compare
// them. Why they might be unavailable has a different answer per calling
// path, and stating one answer for both is how the sentence went wrong.
func TestWriteBlueprintReconcile_DoesNotClaimArgumentsAreUnrecorded(t *testing.T) {
	out := renderReconcile(t, blueprintReconcile{UsedAll: 1})
	if !strings.Contains(out, "not checked: the arguments") {
		t.Fatalf("the scope statement no longer names arguments:\n%s", out)
	}
	for _, stale := range []string{"records\n  its result and not its arguments", "not its arguments"} {
		if strings.Contains(out, stale) {
			t.Errorf("the report still explains arguments as unrecorded, which is false for an HCL call:\n%s", out)
		}
	}
}

// TestWriteBlueprintReconcile_UnmanagedIsNotAMismatch: the two findings
// need different advice, so they are not listed together under one
// heading.
//
// A declaration that moved is a mismatch: change your declaration and the
// next plan agrees. A blueprint nothing declares is not a mismatch at
// all. Its resources still exist, nothing will remove them, and the
// question is whether they should be there. A reader who reads the second
// as the first edits a version and changes nothing.
func TestWriteBlueprintReconcile_UnmanagedIsNotAMismatch(t *testing.T) {
	out := renderReconcile(t, blueprintReconcile{
		UsedAll: 2,
		Differs: []blueprintDiff{{Name: "ci-platform", Declared: "oci://x/ci:v2", Used: "oci://x/ci:v1"}},
		Missing: []blueprintUse{{Name: "network", Declarations: []string{"oci://x/net:v1"}}},
	})

	for _, want := range []string{
		"their resources are unmanaged",                       // what the state is
		"Deleting a declaration does not delete what it made", // why it is not fixed by editing
		"ubx terminate", // the only thing that removes them
		"Doing nothing leaves them in place and unmanaged", // and that inaction is a choice
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the unmanaged case does not say %q:\n%s", want, out)
		}
	}

	// The two findings must not read as one list, since the advice differs.
	unmanagedHeading := strings.Index(out, "their resources are unmanaged")
	mismatch := strings.Index(out, "ci-platform")
	if unmanagedHeading < 0 || mismatch < 0 || mismatch > unmanagedHeading {
		t.Errorf("the version mismatch is not separated from the unmanaged block:\n%s", out)
	}
}

// TestWriteBlueprintReconcile_NoUnmanagedBlockWithoutOne: a report with
// only a version difference must not print advice about resources nobody
// stopped declaring.
func TestWriteBlueprintReconcile_NoUnmanagedBlockWithoutOne(t *testing.T) {
	out := renderReconcile(t, blueprintReconcile{UsedAll: 1, Differs: []blueprintDiff{
		{Name: "ci-platform", Declared: "oci://x/ci:v2", Used: "oci://x/ci:v1"},
	}})
	for _, absent := range []string{"unmanaged", "ubx terminate", "does not delete"} {
		if strings.Contains(out, absent) {
			t.Errorf("a version difference mentions %q:\n%s", absent, out)
		}
	}
}
