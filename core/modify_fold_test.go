package core

import (
	"encoding/json"
	"testing"
	"time"
)

// shipChangeModifyForTest hand-builds and accepts a kind:"change" proposal
// with one Delta.Modifies entry for addr (before -> after), then optionally
// hand-builds its own apply record -- the core-package equivalent of
// core/executor.Ship's own shipModifyNode (core can't import core/
// executor), mirroring shipChangeDestroyForTest's own established
// convention (destroy_tombstone_test.go) for the modify side. attempted
// controls whether an apply record exists AT ALL (false models "accepted,
// ubx ship never even ran yet" -- no BeginApply call whatsoever);
// reachApplied controls whether the resource's own last transition is
// ResourceApplied (true) or ResourceFailed (false, only meaningful when
// attempted -- models a ship attempt that started, then failed, e.g.
// VerifyFreshness's own ErrStaleObservation refusal, UBI-89's own exact
// repro).
func shipChangeModifyForTest(t *testing.T, l *Ledger, addr Address, before, after map[string]json.RawMessage, attempted, reachApplied bool) *Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	// A real Modification's own ObservedHash comes from the pre-modify
	// current state (core/resolver's own OpModify case) -- not load-bearing
	// for FoldState itself (which never reads resolution.inputs), only
	// needed here so this hand-built proposal shapes like a real one.
	hash, err := ObservedHash(json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindChange,
		Intent:        Intent{Summary: "test modify " + addr.String()},
		Delta:         Delta{Modifies: []Modification{{Target: addr, Before: before, After: after}}},
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"` + addr.String() + `"}`)},
			},
		},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{Modifies: 1},
		Status:      StatusDraft,
	}
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("accept modify: %v", err)
	}
	if !attempted {
		return accepted
	}

	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ra := &ResourceApply{
		Address:     addr,
		Transitions: []Transition{{State: ResourcePending, At: now}, {State: ResourceInFlight, At: now}},
	}
	if reachApplied {
		ra.Transitions = append(ra.Transitions, Transition{State: ResourceApplied, At: now})
	} else {
		// UBI-89's own exact repro shape: VerifyFreshness refuses with
		// ErrStaleObservation, core/executor records it as ErrorTerminal
		// and transitions straight to ResourceFailed -- never reaching
		// ResourceApplied, never recording a ProviderResult (the real
		// ApplyResourceChange call is never even reached).
		ra.Transitions = append(ra.Transitions, Transition{State: ResourceFailed, At: now})
		ra.Errors = append(ra.Errors, ErrorRecord{Classification: ErrorTerminal, Message: "stale observation: reality changed since this proposal was generated"})
	}
	rec.Resources = append(rec.Resources, ra)
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	summary := ApplySummary{StartedAt: now, FinishedAt: now}
	if reachApplied {
		summary.Outcome, summary.ResourcesApplied = "applied", 1
	} else {
		summary.Outcome, summary.ResourcesFailed = "failed", 1
	}
	if _, err := l.SealApply(rec, summary); err != nil {
		t.Fatalf("seal apply: %v", err)
	}
	return accepted
}

// TestFoldState_UnshippedModify_DoesNotFoldIntoCurrent is UBI-89's own
// regression test for the P1's second, most severe confirmed root cause:
// FoldState's own modify-folding loop applied Delta.Modifies[].After
// UNCONDITIONALLY, with no equivalent of the shipped-gating create/destroy
// already had (shippedCreateFold/shippedDestroyFold, both check the
// resource's own real apply-record outcome before ever trusting a Delta as
// having actually happened) -- so an accepted-but-never-even-attempted
// modify was folded into "current truth" exactly as if it had shipped
// cleanly. This is the literal mechanism behind the founder's own live
// finding: the ledger claiming a change (259200s retention) that never
// touched real AWS (still 86400s).
func TestFoldState_UnshippedModify_DoesNotFoldIntoCurrent(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "playground", Type: "aws_sqs_queue", Name: "q"}
	adoptForTest(t, l, addr, json.RawMessage(`{"id":"q-1","message_retention_seconds":86400}`))

	before := map[string]json.RawMessage{"message_retention_seconds": json.RawMessage(`86400`)}
	after := map[string]json.RawMessage{"message_retention_seconds": json.RawMessage(`259200`)}
	shipChangeModifyForTest(t, l, addr, before, after, false, false)

	state, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("fold state: %v", err)
	}
	if !found {
		t.Fatalf("expected found=true (the resource still exists, only the modify never applied)")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(state, &m); err != nil {
		t.Fatalf("unmarshal folded state: %v", err)
	}
	if got := string(m["message_retention_seconds"]); got != "86400" {
		t.Fatalf("FoldState folded an accepted-but-never-attempted modify into current truth: message_retention_seconds = %s, want 86400 (the modify never actually shipped)", got)
	}
}

// TestFoldState_FailedModify_DoesNotFoldIntoCurrent is the exact UBI-89
// repro shape: a modify WAS attempted (BeginApply ran, ubx ship really
// executed) but the resource's own last transition is ResourceFailed, not
// ResourceApplied -- VerifyFreshness's own ErrStaleObservation refusal,
// confirmed as the founder's own live sequence. FoldState must still
// report the PRE-modify value; the ledger's own append-only acceptance
// record can't be un-recorded, but "current truth" (what every reader --
// `ubx why`, `ubx status --drift`, a future `ubx plan`/`resolve` -- sees)
// must never claim a change that never actually reached the real
// resource.
func TestFoldState_FailedModify_DoesNotFoldIntoCurrent(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "playground", Type: "aws_sqs_queue", Name: "q"}
	adoptForTest(t, l, addr, json.RawMessage(`{"id":"q-1","message_retention_seconds":86400}`))

	before := map[string]json.RawMessage{"message_retention_seconds": json.RawMessage(`86400`)}
	after := map[string]json.RawMessage{"message_retention_seconds": json.RawMessage(`259200`)}
	shipChangeModifyForTest(t, l, addr, before, after, true, false)

	state, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("fold state: %v", err)
	}
	if !found {
		t.Fatalf("expected found=true")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(state, &m); err != nil {
		t.Fatalf("unmarshal folded state: %v", err)
	}
	if got := string(m["message_retention_seconds"]); got != "86400" {
		t.Fatalf("FoldState folded a FAILED modify (VerifyFreshness's own ErrStaleObservation refusal) into current truth: message_retention_seconds = %s, want 86400 -- the ledger must never claim a change the real resource never received", got)
	}
}

// TestFoldState_AppliedModify_FoldsIntoCurrent is the positive baseline:
// a modify that genuinely reached ResourceApplied must still fold into
// current truth exactly as before -- the shipped-gate this session adds
// must never suppress a REAL, successfully-shipped change.
func TestFoldState_AppliedModify_FoldsIntoCurrent(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "playground", Type: "aws_sqs_queue", Name: "q"}
	adoptForTest(t, l, addr, json.RawMessage(`{"id":"q-1","message_retention_seconds":86400}`))

	before := map[string]json.RawMessage{"message_retention_seconds": json.RawMessage(`86400`)}
	after := map[string]json.RawMessage{"message_retention_seconds": json.RawMessage(`259200`)}
	shipChangeModifyForTest(t, l, addr, before, after, true, true)

	state, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("fold state: %v", err)
	}
	if !found {
		t.Fatalf("expected found=true")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(state, &m); err != nil {
		t.Fatalf("unmarshal folded state: %v", err)
	}
	if got := string(m["message_retention_seconds"]); got != "259200" {
		t.Fatalf("a genuinely shipped modify must still fold into current truth: message_retention_seconds = %s, want 259200", got)
	}
}
