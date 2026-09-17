package blueprint

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// seedBlueprintResource records one live resource produced by a blueprint
// declared at the given source, so the drop warning has a real ledger to
// read rather than a hand-built answer.
func seedBlueprintResource(t *testing.T, l *core.Ledger, stack, name, ref, declaration string) {
	t.Helper()
	addr := core.Address{Stack: stack, Type: "aws_sqs_queue", Name: name}
	state := json.RawMessage(`{"id":"q-1"}`)
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name, "state": state,
		"sources": []core.IntentSource{{
			Kind: "blueprint", Ref: ref,
			Declaration: declaration, DeclaredSource: declaration,
		}},
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
		SchemaVersion: core.SchemaVersion, Stack: stack, Parent: head,
		Kind: core.KindAdoption, Intent: core.Intent{Summary: "seed " + name},
		Delta: core.Delta{Creates: []json.RawMessage{node}},
		Resolution: core.Resolution{ResolvedAt: now, Inputs: []core.ResolutionInput{
			{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"q-1"}`)},
		}},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)}, Status: core.StatusDraft,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestDropNotes_NamesWhatTheDropLeftBehind is the whole point.
//
// Removing a blueprint call removed its lock entry and said so, then the
// plan said there were no changes. Those two lines together read as
// confirmation that nothing is outstanding, while the resources that
// blueprint produced are still running and now declared by nothing.
func TestDropNotes_NamesWhatTheDropLeftBehind(t *testing.T) {
	dir := t.TempDir()
	l := core.Open(dir)
	const decl = "oci://ghcr.io/ubx-blueprints/rev-bp2:v1.0.0"
	seedBlueprintResource(t, l, "rev2", "events", "bp:sha256:aaa", decl)
	seedBlueprintResource(t, l, "rev2", "alerts", "bp:sha256:aaa", decl)

	notes := dropNotes(dir, "rev2", []string{"rev-bp2"})
	if len(notes) != 1 {
		t.Fatalf("notes = %v", notes)
	}
	n := notes[0]
	for _, want := range []string{
		"dropped rev-bp2", // the drop itself, as before
		"2 resources it produced are still in the ledger", // and what it left
		"rev2.aws_sqs_queue.alerts",                       // named, not counted
		"rev2.aws_sqs_queue.events",
		"Nothing will change or remove them", // so the empty plan is not reassurance
		"ubx terminate",                      // the ways out
		"declaring the blueprint again",
		"leaving them keeps them running and unmanaged", // and that inaction is a choice
	} {
		if !strings.Contains(n, want) {
			t.Errorf("the drop note does not say %q:\n%s", want, n)
		}
	}
	// Sorted, since the ledger walk has no order a reader should see.
	if strings.Index(n, "alerts") > strings.Index(n, "events") {
		t.Errorf("addresses are not sorted:\n%s", n)
	}
}

// TestDropNotes_PairsByDeclarationNotByPackagedName: the lock is keyed by
// the name the CALL used and a provenance ref carries the blueprint's own
// packaged name. Matching on the packaged name would find nothing for
// exactly the blueprints whose repository is spelled differently.
func TestDropNotes_PairsByDeclarationNotByPackagedName(t *testing.T) {
	dir := t.TempDir()
	l := core.Open(dir)
	// Packaged as "bp", called as "rev-bp2".
	seedBlueprintResource(t, l, "rev2", "events", "bp:sha256:aaa", "oci://ghcr.io/ubx-blueprints/rev-bp2:v1.0.0")

	n := dropNotes(dir, "rev2", []string{"rev-bp2"})[0]
	if !strings.Contains(n, "rev2.aws_sqs_queue.events") {
		t.Errorf("a blueprint whose packaged name differs from its called name found nothing:\n%s", n)
	}
}

// TestDropNotes_QuietWhenNothingWasLeft: a blueprint dropped before it
// ever produced anything gets the plain line it always had.
func TestDropNotes_QuietWhenNothingWasLeft(t *testing.T) {
	dir := t.TempDir()
	core.Open(dir)
	n := dropNotes(dir, "rev2", []string{"rev-bp2"})[0]
	if strings.Contains(n, "still in the ledger") {
		t.Errorf("a drop that orphaned nothing gained a warning:\n%s", n)
	}
	if !strings.Contains(n, "dropped rev-bp2") {
		t.Errorf("the drop itself is no longer reported:\n%s", n)
	}
}

// TestDropNotes_OtherBlueprintsAreNotSwept: only resources from the
// dropped blueprint are named. A stack usually declares several, and
// naming the wrong ones would send someone to terminate something still
// declared.
func TestDropNotes_OtherBlueprintsAreNotSwept(t *testing.T) {
	dir := t.TempDir()
	l := core.Open(dir)
	seedBlueprintResource(t, l, "rev2", "events", "bp:sha256:aaa", "oci://ghcr.io/ubx-blueprints/rev-bp2:v1.0.0")
	seedBlueprintResource(t, l, "rev2", "kept", "other:sha256:bbb", "oci://ghcr.io/ubx-blueprints/other-bp:v1.0.0")

	n := dropNotes(dir, "rev2", []string{"rev-bp2"})[0]
	if strings.Contains(n, "kept") {
		t.Errorf("a resource from a blueprint that is still declared was named:\n%s", n)
	}
	if !strings.Contains(n, "events") {
		t.Errorf("the dropped blueprint's own resource was not named:\n%s", n)
	}
}

// TestDropNotes_NoLedgerIsNotAFailure: a warning that could fail a plan
// would be a worse trade than one that occasionally cannot be given.
func TestDropNotes_NoLedgerIsNotAFailure(t *testing.T) {
	n := dropNotes(t.TempDir()+"/does-not-exist", "rev2", []string{"rev-bp2"})
	if len(n) != 1 || !strings.Contains(n[0], "dropped rev-bp2") {
		t.Errorf("a missing ledger changed the drop line itself: %v", n)
	}
}

// TestFinishLockPolicy_DropNoteReachesTheReceipt goes through the
// function production calls, not the helper the wording lives in.
//
// Every other test here calls dropNotes directly, so unwiring it from
// finishLockPolicy leaves them all green while a real drop says only that
// the entry went, which is the behaviour this whole change exists to end.
// That is UBI-252's entry-point pattern, and it was verified by reverting
// exactly that line rather than assumed.
func TestFinishLockPolicy_DropNoteReachesTheReceipt(t *testing.T) {
	dir := t.TempDir()
	l := core.Open(dir)
	const decl = "oci://ghcr.io/ubx-blueprints/rev-bp2:v1.0.0"
	seedBlueprintResource(t, l, "rev2", "events", "bp:sha256:aaa", decl)

	// A lock that holds the blueprint, and a resolution that declares
	// nothing: the shape of removing the call from the stack.
	lock := &StackLock{SchemaVersion: StackLockSchemaVersion, Stacks: map[string]map[string]LockEntry{
		"rev2": {"rev-bp2": {Source: decl, ContentHash: "sha256:aaa"}},
	}}
	notes, err := finishLockPolicy(LockPolicy{LedgerDir: dir, Stack: "rev2", Mode: LockWrite}, lock, nil, nil)
	if err != nil {
		t.Fatalf("finishLockPolicy: %v", err)
	}

	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "dropped rev-bp2") {
		t.Fatalf("the drop itself is not reported:\n%s", joined)
	}
	if !strings.Contains(joined, "still in the ledger and now declared by nothing") {
		t.Fatalf("a real drop does not say what it left behind, so an empty plan still reads as reassurance:\n%s", joined)
	}
	if !strings.Contains(joined, "rev2.aws_sqs_queue.events") {
		t.Errorf("the orphaned resource is not named:\n%s", joined)
	}
}
