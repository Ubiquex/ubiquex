package executor

import (
	"context"
	"sync"
	"testing"
)

// TestWithProgress_NoSink_NeverPanics proves emitProgress is a true no-op
// for the overwhelmingly common case (a ctx nobody ever called
// WithProgress on) -- every existing test in this package exercises this
// path already; this just asserts it directly rather than only by
// implication.
func TestWithProgress_NoSink_NeverPanics(t *testing.T) {
	emitProgress(context.Background(), ProgressEvent{Kind: "transition"})
}

// TestWithProgress_ReconcileDestroyLoop_EmitsReadBackAttempts is the
// foundation-level proof for docs/cli-output-spec.md's own "the read-back
// verification line is mandatory": a real destroy that needs several
// reconcile-by-query retries (the exact scenario
// TestShipDestroy_DelayedAbsence_ResolvesDestroyed already covers,
// mirrored here with a progress sink attached) emits one
// "reconcile_attempt" event per retry, in order, each carrying a real
// Attempt/Total pair a CLI could render as "attempt N/M".
func TestWithProgress_ReconcileDestroyLoop_EmitsReadBackAttempts(t *testing.T) {
	shrinkDestroyReconcileBackoff(t)
	l, fake, addr, p := singleResourceDestroy(t)
	fake.scriptDelayedAbsence(addr.String(), 3)

	var mu sync.Mutex
	var events []ProgressEvent
	ctx := WithProgress(context.Background(), func(ev ProgressEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, ev)
	})

	sealed, err := Ship(ctx, l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" {
		t.Fatalf("outcome = %s, want applied", sealed.Summary.Outcome)
	}

	var reconcileAttempts []ProgressEvent
	for _, ev := range events {
		if ev.Kind == "reconcile_attempt" {
			reconcileAttempts = append(reconcileAttempts, ev)
		}
	}
	// present_matches precheck (not a reconcile-loop attempt) + 3
	// delayed-present retries + 1 final destroyed read -- the reconcile
	// LOOP itself (reconcileDestroyLoop) runs 4 times before resolving.
	if len(reconcileAttempts) != 4 {
		t.Fatalf("got %d reconcile_attempt events, want 4: %+v", len(reconcileAttempts), reconcileAttempts)
	}
	for i, ev := range reconcileAttempts {
		if ev.Address != addr.String() {
			t.Errorf("event %d address = %q, want %q", i, ev.Address, addr.String())
		}
		if ev.Attempt != i+1 {
			t.Errorf("event %d Attempt = %d, want %d", i, ev.Attempt, i+1)
		}
		if ev.Total == 0 {
			t.Errorf("event %d Total = 0, want a real retry budget", i)
		}
		if ev.Detail == "" {
			t.Errorf("event %d Detail is empty, want a narration string", i)
		}
	}

	var transitions []string
	for _, ev := range events {
		if ev.Kind == "transition" {
			transitions = append(transitions, ev.State)
		}
	}
	if len(transitions) == 0 {
		t.Fatal("expected at least one transition event (e.g. pending/in_flight/applied), got none")
	}
}
