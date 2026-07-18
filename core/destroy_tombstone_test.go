package core

import (
	"encoding/json"
	"testing"
	"time"
)

// adoptForTest hand-builds and accepts a plain KindAdoption proposal
// seeding addr with state -- the common starting point every test below
// needs before it can be destroyed at all (docs/resolver.md's own
// resolve-time presence validation: a destroy target must already exist).
func adoptForTest(t *testing.T, l *Ledger, addr Address, state json.RawMessage) *Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"state": json.RawMessage(state),
	})
	if err != nil {
		t.Fatal(err)
	}
	hash, err := ObservedHash(state)
	if err != nil {
		t.Fatal(err)
	}
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindAdoption,
		Intent:        Intent{Summary: "seed " + addr.String()},
		Delta:         Delta{Creates: []json.RawMessage{node}},
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"` + addr.String() + `"}`)},
			},
		},
		CostDelta: CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		Status:    StatusDraft,
	}
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("adopt %s: %v", addr, err)
	}
	return accepted
}

// shipChangeDestroyForTest hand-builds and accepts a kind:"change"
// proposal with one Delta.Destroys entry for addr, then hand-builds its
// own apply record -- the core-package equivalent of core/executor.Ship's
// own shipDestroyNode (core can't import core/executor). seal controls
// whether SealApply is ever called (false simulates a real kill -9 before
// the attempt was sealed, mirroring shipChangeCreateForTest's own
// convention for the analogous create-side test); reachApplied controls
// whether the resource's own last transition is ResourceApplied at all
// (false leaves it at ResourceInFlight, simulating a kill -9 that never
// got a chance to reconcile -- docs/destroys-adversarial.md's own "kill -9
// mid-destroy, before the call" row, one level down at the fold layer);
// outcome is the terminal Reconciliation entry's own Outcome
// ("destroyed" | "already_absent"), only meaningful when reachApplied.
func shipChangeDestroyForTest(t *testing.T, l *Ledger, addr Address, state json.RawMessage, outcome string, seal, reachApplied bool) *Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	hash, err := ObservedHash(state)
	if err != nil {
		t.Fatal(err)
	}
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindChange,
		Intent:        Intent{Summary: "test destroy " + addr.String()},
		Delta:         Delta{Destroys: []DestroyEntry{{Address: addr, State: state}}},
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{Kind: "destroy_target", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"` + addr.String() + `"}`)},
			},
		},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{Destroys: 1},
		Status:      StatusDraft,
	}
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("accept destroy: %v", err)
	}

	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ra := &ResourceApply{
		Address:     addr,
		Transitions: []Transition{{State: ResourcePending, At: now}, {State: ResourceInFlight, At: now}},
		Reconciliation: []ReconciliationAttempt{
			{At: now, Outcome: "present_matches"},
		},
	}
	if reachApplied {
		ra.Reconciliation = append(ra.Reconciliation, ReconciliationAttempt{At: now, Outcome: outcome})
		ra.Transitions = append(ra.Transitions, Transition{State: ResourceApplied, At: now})
	}
	rec.Resources = append(rec.Resources, ra)
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	if seal {
		summary := ApplySummary{StartedAt: now, FinishedAt: now}
		if reachApplied {
			summary.Outcome, summary.ResourcesApplied = "applied", 1
		} else {
			summary.Outcome, summary.ResourcesStillUnknown = "partially_applied", 1
		}
		if _, err := l.SealApply(rec, summary); err != nil {
			t.Fatalf("seal apply: %v", err)
		}
	}
	return accepted
}

func TestFoldState_ShippedDestroy_TombstonesAddress(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "old-db"}
	state := json.RawMessage(`{"id":"old-db","instance_class":"db.t3.medium"}`)
	adoptForTest(t, l, addr, state)

	if _, found, err := l.FoldState(addr); err != nil || !found {
		t.Fatalf("before destroy: found=%v err=%v, want found=true", found, err)
	}

	shipChangeDestroyForTest(t, l, addr, state, "destroyed", true, true)

	_, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("fold state: %v", err)
	}
	if found {
		t.Fatal("FoldState still reports found=true after a shipped, sealed destroy -- want the address tombstoned back to absent")
	}
}

func TestFoldState_ShippedDestroy_AlreadyAbsentOutcome_AlsoTombstones(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "old-db"}
	state := json.RawMessage(`{"id":"old-db"}`)
	adoptForTest(t, l, addr, state)

	shipChangeDestroyForTest(t, l, addr, state, "already_absent", true, true)

	if _, found, err := l.FoldState(addr); err != nil || found {
		t.Fatalf("found=%v err=%v, want found=false (already_absent tombstones exactly like destroyed)", found, err)
	}
}

func TestFoldState_UnshippedDestroy_DoesNotTombstone(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "old-db"}
	state := json.RawMessage(`{"id":"old-db"}`)
	adoptForTest(t, l, addr, state)

	// Accepted, but never even a BeginApply -- no apply record at all.
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	hash, _ := ObservedHash(state)
	p := &Proposal{
		SchemaVersion: SchemaVersion, Stack: addr.Stack, Parent: head, Kind: KindChange,
		Intent: Intent{Summary: "unshipped destroy"},
		Delta:  Delta{Destroys: []DestroyEntry{{Address: addr, State: state}}},
		Resolution: Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339), Inputs: []ResolutionInput{
			{Kind: "destroy_target", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"old-db"}`)},
		}},
		CostDelta: CostDelta{MonthlyUSD: json.RawMessage(`0`)}, BlastRadius: BlastRadius{Destroys: 1}, Status: StatusDraft,
	}
	if _, err := Accept(l, p); err != nil {
		t.Fatalf("accept: %v", err)
	}

	if _, found, err := l.FoldState(addr); err != nil || !found {
		t.Fatalf("found=%v err=%v, want found=true -- an accepted-but-unshipped destroy removes nothing yet", found, err)
	}
}

func TestFoldState_DestroyKilledMidAttempt_NeverTombstones(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "old-db"}
	state := json.RawMessage(`{"id":"old-db"}`)
	adoptForTest(t, l, addr, state)

	// A real kill -9 mid-destroy: in_flight was durably written, but the
	// resource never actually reached ResourceApplied, and the attempt
	// itself was never sealed.
	shipChangeDestroyForTest(t, l, addr, state, "", false, false)

	if _, found, err := l.FoldState(addr); err != nil || !found {
		t.Fatalf("found=%v err=%v, want found=true -- a killed, unresolved destroy attempt must never tombstone", found, err)
	}
}

func TestFoldState_RecreateAfterDestroy_ReSeedsFresh(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "old-db"}
	oldState := json.RawMessage(`{"id":"old-db","instance_class":"db.t3.medium"}`)
	adoptForTest(t, l, addr, oldState)
	shipChangeDestroyForTest(t, l, addr, oldState, "destroyed", true, true)

	if _, found, _ := l.FoldState(addr); found {
		t.Fatal("expected the address tombstoned before recreating it")
	}

	// A later change proposal creates a NEW resource under the identical
	// address -- a real, legitimate "tear down, rebuild under the same
	// name" lifecycle. op:"create"'s own resolve-time validation (FoldState
	// absent required) is exactly what this tombstone-fold makes possible.
	newResult := json.RawMessage(`{"id":"old-db","instance_class":"db.t3.large"}`)
	accepted := shipChangeCreateForTest(t, l, addr, newResult, true, true)
	_ = accepted

	newState, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("fold state: %v", err)
	}
	if !found {
		t.Fatal("expected found=true after a real recreate under the same address")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(newState, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["instance_class"] != "db.t3.large" {
		t.Fatalf("state = %v, want the NEW create's own state, not anything from the destroyed lifecycle", decoded)
	}
}

func TestFleet_ExcludesTombstonedAddress(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "old-db"}
	state := json.RawMessage(`{"id":"old-db"}`)
	adoptForTest(t, l, addr, state)

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("fleet: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("fleet before destroy = %d entries, want 1", len(entries))
	}

	shipChangeDestroyForTest(t, l, addr, state, "destroyed", true, true)

	entries, err = l.Fleet("")
	if err != nil {
		t.Fatalf("fleet: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("fleet after destroy = %+v, want empty -- a tombstoned address must not be actively watched", entries)
	}
}

func TestFleet_RecreatedAddress_ReappearsAfterDestroy(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "old-db"}
	state := json.RawMessage(`{"id":"old-db"}`)
	adoptForTest(t, l, addr, state)
	shipChangeDestroyForTest(t, l, addr, state, "destroyed", true, true)

	if entries, _ := l.Fleet(""); len(entries) != 0 {
		t.Fatalf("expected fleet empty after destroy, got %+v", entries)
	}

	shipChangeCreateForTest(t, l, addr, json.RawMessage(`{"id":"old-db"}`), true, true)

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("fleet: %v", err)
	}
	if len(entries) != 1 || entries[0].Address != addr {
		t.Fatalf("fleet after recreate = %+v, want exactly [%s]", entries, addr)
	}
}
