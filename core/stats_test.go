package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestStats_EmptyLedger(t *testing.T) {
	l := Open(t.TempDir())
	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalProposals != 0 || result.DriftEvents != 0 {
		t.Fatalf("result = %+v, want all zero", result)
	}
	if _, ok := result.DriftResolutionPct(); ok {
		t.Fatal("DriftResolutionPct ok = true, want false for zero drift events")
	}
	if _, ok := result.AttributionCoveragePct(); ok {
		t.Fatal("AttributionCoveragePct ok = true, want false for zero drift_adopt proposals")
	}
}

func TestStats_SingleProposal_TotalAndByKind(t *testing.T) {
	l := Open(t.TempDir())
	if _, err := Accept(l, sampleProposal()); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalProposals != 1 || result.ByKind[KindChange] != 1 {
		t.Fatalf("result = %+v, want 1 total, 1 change", result)
	}
	if result.AcceptanceMethod["local"] != 1 {
		t.Fatalf("AcceptanceMethod = %+v, want local:1", result.AcceptanceMethod)
	}
	if result.DriftEvents != 0 {
		t.Fatalf("DriftEvents = %d, want 0 (a plain change proposal is not drift)", result.DriftEvents)
	}
}

// row: drift proposal open (counted unresolved).
func TestStats_DriftOpen_CountedUnresolved(t *testing.T) {
	l, _, res := driftToStaging(t)
	drift, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	if _, err := Accept(l, drift); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.DriftEvents != 1 || result.DriftOpen != 1 || result.DriftResolvedReverted != 0 || result.DriftResolvedAdopted != 0 {
		t.Fatalf("result = %+v, want 1 open, 0 resolved", result)
	}
	pct, ok := result.DriftResolutionPct()
	if !ok || pct != 0 {
		t.Fatalf("DriftResolutionPct = %v, %v, want 0, true", pct, ok)
	}
}

// row: drift adopted (resolved-signed) -- superseded by a later re-drift,
// never explicitly reverted.
func TestStats_DriftAdopted_SupersededByLaterDrift_ResolvedSigned(t *testing.T) {
	l, fp, res := driftToStaging(t)
	addr := testAddr()
	first, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal (first drift): %v", err)
	}
	if _, err := Accept(l, first); err != nil {
		t.Fatalf("Accept (first drift): %v", err)
	}

	// Drift again.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"canary"}}`)
	res2, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (second drift): %v", err)
	}
	second, err := GenerateProposal(l, "payments", res2)
	if err != nil {
		t.Fatalf("GenerateProposal (second drift): %v", err)
	}
	if _, err := Accept(l, second); err != nil {
		t.Fatalf("Accept (second drift): %v", err)
	}

	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	// first drift_adopt: superseded by second. second drift_adopt: open
	// (nothing followed it).
	if result.DriftEvents != 2 || result.DriftResolvedAdopted != 1 || result.DriftOpen != 1 || result.DriftResolvedReverted != 0 {
		t.Fatalf("result = %+v, want 2 events, 1 superseded, 1 open", result)
	}
	pct, ok := result.DriftResolutionPct()
	if !ok || pct != 50 {
		t.Fatalf("DriftResolutionPct = %v, %v, want 50, true", pct, ok)
	}
}

// row: reverted (resolved-signed) -- a drift_adopt immediately followed
// by a drift_revert for the same address.
func TestStats_DriftAdoptThenRevert_ResolvedSigned(t *testing.T) {
	l, fp, res := driftToStaging(t)
	addr := testAddr()
	adopt, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	if _, err := Accept(l, adopt); err != nil {
		t.Fatalf("Accept (adopt): %v", err)
	}

	// A later, deliberate decision to revert this specific accepted
	// drift back toward the original -- a fresh scan against a state
	// that itself differs from what's now recorded, so GenerateRevertProposal
	// has something real to revert.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"canary"}}`)
	res2, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	revert, err := GenerateRevertProposal(l, "payments", res2)
	if err != nil {
		t.Fatalf("GenerateRevertProposal: %v", err)
	}
	if _, err := Accept(l, revert); err != nil {
		t.Fatalf("Accept (revert): %v", err)
	}

	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.DriftEvents != 1 || result.DriftResolvedReverted != 1 || result.DriftOpen != 0 || result.DriftResolvedAdopted != 0 {
		t.Fatalf("result = %+v, want exactly 1 event, resolved via revert (the revert must not ALSO count as its own second event)", result)
	}
}

// A standalone drift_revert -- generated as an alternative to adopting a
// freshly detected drift, never preceded by an accepted drift_adopt for
// the same instance (the real `ubx scan --propose both` shape) -- must
// still count as its own surfaced-and-resolved event, not be silently
// dropped.
func TestStats_StandaloneRevert_CountsAsOwnResolvedEvent(t *testing.T) {
	l, _, res := driftToStaging(t)
	revert, err := GenerateRevertProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateRevertProposal: %v", err)
	}
	if _, err := Accept(l, revert); err != nil {
		t.Fatalf("Accept (revert): %v", err)
	}

	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.DriftEvents != 1 || result.DriftResolvedReverted != 1 {
		t.Fatalf("result = %+v, want 1 standalone-reverted event", result)
	}
}

// row: stale-then-reresolved chains counted once -- `--propose both`
// shares a parent between a drift_adopt and drift_revert candidate;
// only one is ever accepted (the other never enters the ledger at all).
func TestStats_StaleSiblingProposal_NeverCounted(t *testing.T) {
	l, _, res := driftToStaging(t)
	adopt, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	revert, err := GenerateRevertProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateRevertProposal: %v", err)
	}
	if adopt.Parent != revert.Parent {
		t.Fatalf("adopt.Parent = %q, revert.Parent = %q, want equal (siblings from the same scan)", adopt.Parent, revert.Parent)
	}

	// Only the adopt is accepted -- the revert becomes stale the moment
	// that happens (its own Parent no longer matches head) and is never
	// appended.
	if _, err := Accept(l, adopt); err != nil {
		t.Fatalf("Accept (adopt): %v", err)
	}

	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalProposals != 2 { // the original adoption + the drift_adopt
		t.Fatalf("TotalProposals = %d, want 2 (adoption + the one accepted drift_adopt; the stale revert sibling must never appear)", result.TotalProposals)
	}
	if result.DriftEvents != 1 || result.DriftOpen != 1 {
		t.Fatalf("result = %+v, want exactly 1 open drift event, counted once", result)
	}
}

// Detail: destroyed addresses count in history, excluded from "open" --
// an open-ended drift_adopt whose address was later shipped-destroyed is
// neither genuinely open (nothing left to act on) nor resolved; it's
// moot, and must not inflate either bucket.
func TestStats_DestroyedAddress_ExcludedFromOpen(t *testing.T) {
	l, _, res := driftToStaging(t)
	addr := testAddr()
	drift, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	if _, err := Accept(l, drift); err != nil {
		t.Fatalf("Accept (drift): %v", err)
	}

	shipChangeDestroyForTest(t, l, addr, json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"}}`), "destroyed", true, true)

	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	// history: the drift_adopt is still tallied by kind...
	if result.ByKind[KindDriftAdopt] != 1 {
		t.Fatalf("ByKind[drift_adopt] = %d, want 1 (still counted in history)", result.ByKind[KindDriftAdopt])
	}
	// ...but excluded from open, and not counted as resolved either.
	if result.DriftOpen != 0 {
		t.Fatalf("DriftOpen = %d, want 0 (destroyed, not open)", result.DriftOpen)
	}
	if result.DriftResolvedAdopted != 0 || result.DriftResolvedReverted != 0 {
		t.Fatalf("result = %+v, want no resolved count either (moot, not resolved)", result)
	}
	if result.DriftEvents != 0 {
		t.Fatalf("DriftEvents = %d, want 0 (excluded from the surfaced-drift total entirely)", result.DriftEvents)
	}
	if _, ok := result.DriftResolutionPct(); ok {
		t.Fatal("DriftResolutionPct ok = true, want false (zero drift events left to compute a percentage from)")
	}
}

// row: unattributed drifts lower attribution coverage but not resolution
// rate.
func TestStats_UnattributedDrift_LowersCoverageNotResolution(t *testing.T) {
	l, _, res := driftToStaging(t)
	drift, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	drift.Intent.Sources = append(drift.Intent.Sources, IntentSource{Kind: "cloudtrail_unattributed", Reason: "delivery_window"})
	if _, err := Accept(l, drift); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	coverage, ok := result.AttributionCoveragePct()
	if !ok || coverage != 0 {
		t.Fatalf("AttributionCoveragePct = %v, %v, want 0, true", coverage, ok)
	}
	// Resolution rate is untouched by attribution -- this drift is
	// "open," same as it would be with no attribution data at all.
	resPct, ok := result.DriftResolutionPct()
	if !ok || resPct != 0 {
		t.Fatalf("DriftResolutionPct = %v, %v, want 0, true (unaffected by attribution)", resPct, ok)
	}
	if result.DriftOpen != 1 {
		t.Fatalf("DriftOpen = %d, want 1", result.DriftOpen)
	}
}

func TestStats_AttributedDrift_RaisesCoverage(t *testing.T) {
	l, _, res := driftToStaging(t)
	drift, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	drift.Intent.Sources = append(drift.Intent.Sources, IntentSource{
		Kind: "cloudtrail", ActorARN: "arn:aws:iam::123:user/alice", EventTime: "2026-07-20T00:00:00Z",
	})
	if _, err := Accept(l, drift); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	coverage, ok := result.AttributionCoveragePct()
	if !ok || coverage != 100 {
		t.Fatalf("AttributionCoveragePct = %v, %v, want 100, true", coverage, ok)
	}
}

func TestStats_TimeToDecision(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	p.Kind = KindDriftAdopt
	p.Delta = Delta{Modifies: []Modification{{
		Target: testAddr(),
		Before: map[string]json.RawMessage{"tags.env": json.RawMessage(`"prod"`)},
		After:  map[string]json.RawMessage{"tags.env": json.RawMessage(`"staging"`)},
	}}}
	p.Resolution = Resolution{
		ResolvedAt: "2026-07-20T10:00:00Z",
		Inputs:     []ResolutionInput{{Kind: "live_state", Resource: testAddr().String(), ObservedHash: "sha256:abc"}},
	}
	p.BlastRadius = BlastRadius{}
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	// Accept stamps AcceptedAt to time.Now() -- for a deterministic test,
	// hand-set it after the fact isn't possible via the public API, so
	// this test only asserts TimeToDecision is non-negative and exactly
	// one sample contributed, not an exact duration.
	if accepted.Acceptance.AcceptedAt == "" {
		t.Fatal("AcceptedAt is empty")
	}

	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TimeToDecisionSamples != 1 {
		t.Fatalf("TimeToDecisionSamples = %d, want 1", result.TimeToDecisionSamples)
	}
	if result.TimeToDecision < 0 {
		t.Fatalf("TimeToDecision = %v, want >= 0", result.TimeToDecision)
	}
}

func TestStats_SinceUntilWindow(t *testing.T) {
	l := Open(t.TempDir())
	if _, err := Accept(l, sampleProposal()); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	future := time.Now().UTC().Add(24 * time.Hour)
	result, err := Stats(l, StatsOptions{Since: &future})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalProposals != 0 {
		t.Fatalf("TotalProposals = %d, want 0 (accepted before the --since window)", result.TotalProposals)
	}

	past := time.Now().UTC().Add(-24 * time.Hour)
	result2, err := Stats(l, StatsOptions{Since: &past})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result2.TotalProposals != 1 {
		t.Fatalf("TotalProposals = %d, want 1 (accepted after --since)", result2.TotalProposals)
	}
}

func TestStats_StackFilter(t *testing.T) {
	l := Open(t.TempDir())
	payments := sampleProposal()
	if _, err := Accept(l, payments); err != nil {
		t.Fatalf("Accept (payments): %v", err)
	}
	networking := sampleProposal()
	networking.Stack = "networking"
	networking.Parent = payments.ID
	networking.Intent.Summary = "networking change"
	if _, err := Accept(l, networking); err != nil {
		t.Fatalf("Accept (networking): %v", err)
	}

	result, err := Stats(l, StatsOptions{Stack: "networking"})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalProposals != 1 {
		t.Fatalf("TotalProposals = %d, want 1 (networking only)", result.TotalProposals)
	}
}

// Mixed schema_version proposals in the same chain must fold correctly --
// Stats never branches on schema_version at all, confirmed by inspection,
// this test proves it doesn't choke on a mix either.
func TestStats_MixedSchemaVersions(t *testing.T) {
	l := Open(t.TempDir())
	v1 := sampleProposal()
	v1.SchemaVersion = 1
	accepted, err := Accept(l, v1)
	if err != nil {
		t.Fatalf("Accept (v1): %v", err)
	}
	v2 := sampleProposal()
	v2.SchemaVersion = SchemaVersion
	v2.Parent = accepted.ID
	v2.Intent.Summary = "v2 proposal"
	if _, err := Accept(l, v2); err != nil {
		t.Fatalf("Accept (v2): %v", err)
	}

	result, err := Stats(l, StatsOptions{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if result.TotalProposals != 2 {
		t.Fatalf("TotalProposals = %d, want 2", result.TotalProposals)
	}
}
