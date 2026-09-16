package core

import (
	"encoding/json"
	"testing"
	"time"
)

// shipRestoreModifyForTest is a restore as cli/restore.go actually
// produces one: an ordinary kind:change proposal whose intent sources
// carry {kind: "restore", ref: <head>}, shipped so it is what the address
// really holds.
func shipRestoreModifyForTest(t *testing.T, l *Ledger, addr Address, targetHead string, before, after map[string]json.RawMessage) *Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	hash, err := ObservedHash(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	accepted, err := Accept(l, &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindChange,
		Intent: Intent{
			Summary: "restore " + addr.Stack,
			Sources: []IntentSource{{Kind: "restore", Ref: targetHead}},
		},
		Delta: Delta{Modifies: []Modification{{
			Target:   addr,
			Before:   before,
			After:    after,
			Provider: &ProviderRef{Source: "hashicorp/aws", Version: "5.0.0"},
		}}},
		Resolution: Resolution{ResolvedAt: now, Inputs: []ResolutionInput{
			{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"q-1"}`)},
		}},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{Modifies: 1},
		Status:      StatusDraft,
	})
	if err != nil {
		t.Fatalf("accept restore: %v", err)
	}
	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	rec.Resources = append(rec.Resources, &ResourceApply{
		Address: addr,
		Transitions: []Transition{
			{State: ResourcePending, At: now},
			{State: ResourceInFlight, At: now},
			{State: ResourceApplied, At: now},
		},
	})
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	if _, err := l.SealApply(rec, ApplySummary{StartedAt: now, FinishedAt: now, Outcome: "applied", ResourcesApplied: 1}); err != nil {
		t.Fatalf("seal apply: %v", err)
	}
	return accepted
}

// TestDivergenceOf_ClassifiesBothKinds is the classification the whole
// mechanism rests on, tested against real proposals rather than
// hand-built result values.
//
// Restore is one instance of the concept and drift_revert is another. A
// version of this that only recognised restore would have to be built
// again the first time someone noticed the second has the same problem.
func TestDivergenceOf_ClassifiesBothKinds(t *testing.T) {
	restore := &Proposal{
		ID:   "r1",
		Kind: KindChange,
		Intent: Intent{Sources: []IntentSource{
			{Kind: "dialogue", Ref: "d-1"},
			{Kind: "restore", Ref: "head-abc"},
		}},
		Resolution: Resolution{ResolvedAt: "2026-09-15T22:43:40Z"},
	}
	d, ok := DivergenceOf(restore)
	if !ok {
		t.Fatal("a restore is a deliberate departure from the declared source")
	}
	if d.Kind != DivergenceRestore || d.TargetHead != "head-abc" {
		t.Errorf("restore classified as %+v", d)
	}
	if d.ResolvedAt != "2026-09-15T22:43:40Z" {
		t.Errorf("when it happened is part of the answer, got %q", d.ResolvedAt)
	}

	revert := &Proposal{ID: "r2", Kind: KindDriftRevert, Resolution: Resolution{ResolvedAt: "2026-09-14T10:02:00Z"}}
	d, ok = DivergenceOf(revert)
	if !ok {
		t.Fatal("a drift revert is a deliberate departure too, and dropping it would mean building this twice")
	}
	if d.Kind != DivergenceDriftRevert {
		t.Errorf("drift revert classified as %+v", d)
	}
	if d.TargetHead != "" {
		t.Errorf("a drift revert has no target head, got %q", d.TargetHead)
	}

	// An ordinary change must not be one, or the note fires on every plan
	// and stops meaning anything.
	ordinary := &Proposal{ID: "r3", Kind: KindChange, Intent: Intent{Sources: []IntentSource{{Kind: "dialogue", Ref: "d-2"}}}}
	if _, ok := DivergenceOf(ordinary); ok {
		t.Error("an ordinary change was classified as a deliberate divergence")
	}
	if _, ok := DivergenceOf(nil); ok {
		t.Error("nil is not a divergence")
	}
}

// TestLastDivergence_SupersededIsNotReported is why this is built on the
// fold rather than on a scan for the most recent restore.
//
// A restore that has since been superseded by an ordinary change is no
// longer what the resource holds. Warning about it would be telling
// someone that changing a value set last week was set deliberately, when
// what they are actually changing is yesterday's ordinary edit.
func TestLastDivergence_SupersededIsNotReported(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "s", Type: "aws_sqs_queue", Name: "q"}
	adoptForTest(t, l, addr, json.RawMessage(`{"id":"q-1","retention":86400}`))

	shipRestoreModifyForTest(t, l, addr, "head-abc",
		map[string]json.RawMessage{"retention": json.RawMessage(`86400`)},
		map[string]json.RawMessage{"retention": json.RawMessage(`259200`)})

	// Right after the restore, it is what the address holds.
	d, ok, err := l.LastDivergence(addr)
	if err != nil {
		t.Fatalf("last divergence: %v", err)
	}
	if !ok || d.Kind != DivergenceRestore {
		t.Fatalf("immediately after a restore, the state it set is what the address holds: ok=%v d=%+v", ok, d)
	}

	// An ordinary change lands on top of it.
	shipDeclaredModifyForTest(t, l, addr,
		map[string]json.RawMessage{"retention": json.RawMessage(`259200`)},
		map[string]json.RawMessage{"retention": json.RawMessage(`604800`)},
		nil)

	_, ok, err = l.LastDivergence(addr)
	if err != nil {
		t.Fatalf("last divergence: %v", err)
	}
	if ok {
		t.Error("a superseded restore was still reported, so a plan would claim an ordinary edit was set deliberately")
	}
}

// TestLastDivergence_UnshippedRestoreIsNotReported: an accepted but never
// shipped restore has not set anything, so nothing about the resource's
// current state is deliberate yet. The fold's own gating gives this for
// free, which is the other reason to build on it.
func TestLastDivergence_UnshippedRestoreIsNotReported(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "s", Type: "aws_sqs_queue", Name: "q"}
	adoptForTest(t, l, addr, json.RawMessage(`{"id":"q-1","retention":86400}`))

	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := ObservedHash(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := Accept(l, &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindChange,
		Intent:        Intent{Summary: "restore", Sources: []IntentSource{{Kind: "restore", Ref: "head-abc"}}},
		Delta: Delta{Modifies: []Modification{{
			Target:   addr,
			Before:   map[string]json.RawMessage{"retention": json.RawMessage(`86400`)},
			After:    map[string]json.RawMessage{"retention": json.RawMessage(`259200`)},
			Provider: &ProviderRef{Source: "hashicorp/aws", Version: "5.0.0"},
		}}},
		Resolution: Resolution{ResolvedAt: now, Inputs: []ResolutionInput{
			{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"q-1"}`)},
		}},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{Modifies: 1},
		Status:      StatusDraft,
	}); err != nil {
		t.Fatalf("accept: %v", err)
	}

	if _, ok, err := l.LastDivergence(addr); err != nil || ok {
		t.Errorf("an unshipped restore has set nothing, so nothing is deliberate yet: ok=%v err=%v", ok, err)
	}
}
