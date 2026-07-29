package conformance

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// TestProbeDestroyHonesty_HonestDestroy_NoFinding proves the common,
// honest case costs nothing extra and produces no false finding: a real
// destroy through core/executor.Ship's own real path, against the real
// fakeprovider binary (ordinary mode, no lying), resolves genuinely
// destroyed and ProbeDestroyHonesty reports nothing.
func TestProbeDestroyHonesty_HonestDestroy_NoFinding(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "conformance", Type: "fake_widget", Name: "honest-destroy"}

	finding, err := ProbeDestroyHonesty(context.Background(), l, DestroyProbeConfig{
		ProviderPath: fakeProviderBinary,
		Source:       "fake/widget",
		Version:      "0.1.0",
		Stack:        "conformance",
		Address:      addr,
		Lookup:       json.RawMessage(`{"id":"honest-destroy"}`),
		ProviderEnv:  []string{"FAKEPROVIDER_MODE=ok-v6"},
	})
	if err != nil {
		t.Fatalf("ProbeDestroyHonesty: %v", err)
	}
	if finding != nil {
		t.Fatalf("got %+v, want nil -- an honest destroy must never produce a FindingDestroyLie", finding)
	}

	// Confirm it's ACTUALLY destroyed, not just "no finding because the
	// probe silently failed to check" -- the same discipline
	// docs/destroys-adversarial.md's own row 14 requires.
	state, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("FoldState: %v", err)
	}
	if found {
		t.Fatalf("FoldState still finds %s present (state=%s) -- the destroy did not actually tombstone the address", addr, state)
	}
}

// TestProbeDestroyHonesty_LyingDestroy_ConfirmedFinding proves the
// UBI-44 class is genuinely caught end to end, through the real tfplugin
// wire protocol (provider/internal/fakeprovider's own
// FAKEPROVIDER_APPLY_MODE=lying-destroy), via core/executor.Ship's real
// path -- never a shortcut. Deliberately gated behind UBX_TEST_SLOW=1,
// skipped by default, the identical reasoning
// cli/ship_lying_destroy_test.go's own sibling test already documents:
// the destroy-reconcile retry budget this path exercises
// (core/executor's own destroyReconcileBackoffSchedule, unexported and
// not overridable from this package) is real production sizing, ~64
// seconds. TestProbeDestroyHonesty_HonestDestroy_NoFinding, above, is
// the fast, always-run sibling proving the common case costs nothing.
func TestProbeDestroyHonesty_LyingDestroy_ConfirmedFinding(t *testing.T) {
	if os.Getenv("UBX_TEST_SLOW") == "" {
		t.Skip("set UBX_TEST_SLOW=1 to run this real-wire-protocol test -- it takes about a minute (core/executor's own destroyReconcileBackoffSchedule is real production sizing, ~64s, and isn't overridable from this package)")
	}

	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "conformance", Type: "fake_widget", Name: "lying-destroy"}

	env := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_APPLY_MODE=lying-destroy"}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	finding, err := ProbeDestroyHonesty(ctx, l, DestroyProbeConfig{
		ProviderPath: fakeProviderBinary,
		Source:       "fake/widget",
		Version:      "0.1.0",
		Stack:        "conformance",
		Address:      addr,
		Lookup:       json.RawMessage(`{"id":"lying-destroy"}`),
		ProviderEnv:  env,
		Timeout:      90 * time.Second,
	})
	if err != nil {
		t.Fatalf("ProbeDestroyHonesty: %v", err)
	}
	if finding == nil {
		t.Fatal("got nil, want a FindingDestroyLie -- the lying provider's own clean-success response must be caught, not silently trusted")
	}
	if finding.Class != FindingDestroyLie || finding.Tier != TierLive || finding.Confidence != Confirmed {
		t.Fatalf("unexpected finding shape: %+v", finding)
	}
	if finding.Verb != "destroy" {
		t.Fatalf("Verb = %q, want \"destroy\"", finding.Verb)
	}
	if finding.Type != "fake_widget" {
		t.Fatalf("Type = %q, want \"fake_widget\"", finding.Type)
	}

	// The address must NOT be tombstoned -- the whole point of catching
	// the lie is that ubx's own ledger never records a destroy that
	// didn't really happen.
	_, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("FoldState: %v", err)
	}
	if !found {
		t.Fatal("FoldState no longer finds the address -- a caught lie must never still tombstone the ledger")
	}
}

func TestClassifyDestroyOutcome_NilRecord(t *testing.T) {
	if got := classifyDestroyOutcome("acme/widget", "1.0.0", "acme_widget", nil); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestClassifyDestroyOutcome_NoResources(t *testing.T) {
	rec := &core.ApplyRecord{}
	if got := classifyDestroyOutcome("acme/widget", "1.0.0", "acme_widget", rec); got != nil {
		t.Fatalf("got %+v, want nil", got)
	}
}

func TestClassifyDestroyOutcome_OrdinaryFailed_NoFinding(t *testing.T) {
	// An ordinary, honestly-reported failure (never claimed success) --
	// must never be misclassified as a lie.
	rec := &core.ApplyRecord{Resources: []*core.ResourceApply{
		{Reconciliation: []core.ReconciliationAttempt{{Outcome: "failed"}}},
	}}
	if got := classifyDestroyOutcome("acme/widget", "1.0.0", "acme_widget", rec); got != nil {
		t.Fatalf("got %+v, want nil -- an ordinary ambiguous failure is not a lie", got)
	}
}

func TestClassifyDestroyOutcome_Destroyed_NoFinding(t *testing.T) {
	rec := &core.ApplyRecord{Resources: []*core.ResourceApply{
		{Reconciliation: []core.ReconciliationAttempt{{Outcome: "present_matches"}, {Outcome: "destroyed"}}},
	}}
	if got := classifyDestroyOutcome("acme/widget", "1.0.0", "acme_widget", rec); got != nil {
		t.Fatalf("got %+v, want nil -- a genuinely honest destroy is not a lie", got)
	}
}

func TestClassifyDestroyOutcome_LiedOutcome_ConfirmedFinding(t *testing.T) {
	rec := &core.ApplyRecord{Resources: []*core.ResourceApply{
		{Reconciliation: []core.ReconciliationAttempt{{Outcome: "provider_reported_success_but_present"}}},
	}}
	got := classifyDestroyOutcome("acme/widget", "1.0.0", "acme_widget", rec)
	if got == nil {
		t.Fatal("got nil, want a FindingDestroyLie")
	}
	if got.Class != FindingDestroyLie || got.Confidence != Confirmed || got.Tier != TierLive {
		t.Fatalf("unexpected finding shape: %+v", got)
	}
}
