package core

import (
	"context"
	"encoding/json"
	"testing"
)

// driftToStaging adopts testAddr() in "prod", then drifts it to "staging",
// returning the ledger and the ScanResult for the drifted scan -- the
// common setup every drift_revert test in this file starts from.
func driftToStaging(t *testing.T) (*Ledger, *fakeProvider, *ScanResult) {
	t.Helper()
	l := Open(t.TempDir())
	addr := testAddr()
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (adopt): %v", err)
	}
	adopt, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal (adopt): %v", err)
	}
	if _, err := Accept(l, adopt); err != nil {
		t.Fatalf("Accept (adopt): %v", err)
	}

	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"}}`)
	res2, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (drift): %v", err)
	}
	if res2.Outcome != ScanDrifted {
		t.Fatalf("Outcome = %v, want ScanDrifted", res2.Outcome)
	}
	return l, fp, res2
}

func TestGenerateRevertProposal_Basic(t *testing.T) {
	l, _, res := driftToStaging(t)

	revert, err := GenerateRevertProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateRevertProposal: %v", err)
	}
	if revert.Kind != KindDriftRevert {
		t.Fatalf("Kind = %q, want %q", revert.Kind, KindDriftRevert)
	}
	if len(revert.Delta.Modifies) != 1 {
		t.Fatalf("got %d modifies entries, want 1", len(revert.Delta.Modifies))
	}
	mod := revert.Delta.Modifies[0]

	// Reversed relative to drift_adopt: before is the observed (drifted)
	// value, after is the ledger-recorded (restore-to) value.
	if string(mod.Before["tags.env"]) != `"staging"` {
		t.Fatalf("before[tags.env] = %s, want %q (observed/drifted)", mod.Before["tags.env"], "staging")
	}
	if string(mod.After["tags.env"]) != `"prod"` {
		t.Fatalf("after[tags.env] = %s, want %q (ledger-recorded)", mod.After["tags.env"], "prod")
	}

	// blast_radius is real, unlike drift_adopt's all-zero.
	if revert.BlastRadius.Modifies != 1 || revert.BlastRadius.Creates != 0 || revert.BlastRadius.Destroys != 0 {
		t.Fatalf("BlastRadius = %+v, want {Creates:0 Modifies:1 Destroys:0}", revert.BlastRadius)
	}

	// resolution.inputs carries the OBSERVED (drifted) hash, same as
	// drift_adopt would from the same scan -- not the restore target's
	// hash -- so accept --reverify-* keeps meaning "did reality move again".
	if len(revert.Resolution.Inputs) != 1 {
		t.Fatalf("got %d resolution.inputs entries, want 1", len(revert.Resolution.Inputs))
	}
	if revert.Resolution.Inputs[0].ObservedHash != res.ObservedHash {
		t.Fatalf("resolution.inputs[0].observed_hash = %q, want %q (the observed/drifted hash)",
			revert.Resolution.Inputs[0].ObservedHash, res.ObservedHash)
	}

	if _, err := Accept(l, revert); err != nil {
		t.Fatalf("Accept (revert): %v", err)
	}
}

func TestGenerateRevertProposal_RequiresDrifted(t *testing.T) {
	l := Open(t.TempDir())

	for _, outcome := range []ScanOutcome{ScanNew, ScanUnchanged} {
		res := &ScanResult{Address: testAddr(), Outcome: outcome}
		if _, err := GenerateRevertProposal(l, "payments", res); err == nil {
			t.Fatalf("outcome %v: expected an error, got nil", outcome)
		}
	}
}

func TestGenerateRevertProposal_BothFromSameScanShareParent(t *testing.T) {
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
		t.Fatalf("adopt.Parent = %q, revert.Parent = %q -- both are alternative resolutions to the same drift, must share a parent",
			adopt.Parent, revert.Parent)
	}

	// Accepting one advances the ledger head; the other, now stale
	// (parent no longer matches head), must be rejected -- ordinary
	// parent-mismatch staleness, no new mechanism.
	if _, err := Accept(l, revert); err != nil {
		t.Fatalf("Accept (revert): %v", err)
	}
	if _, err := Accept(l, adopt); err == nil {
		t.Fatal("expected the second (now-stale) alternative to be rejected, got nil error")
	}
}

// TestRunScan_AfterRevertAccepted_ManualCorrection_ScanClean proves the
// RunScan baseline correction (FoldState, not LastObservedHash) end to end:
// accepting a drift_revert doesn't itself touch cloud, so a scan run
// immediately after still (correctly) sees the drifted value. Once the
// correction is applied outside of ubx -- exactly like the live-verify
// sequence's manual `aws` CLI step -- a further scan must report clean,
// because the ledger's reconstructed truth (FoldState) already matches.
func TestRunScan_AfterRevertAccepted_ManualCorrection_ScanClean(t *testing.T) {
	l, fp, res := driftToStaging(t)
	addr := testAddr()

	revert, err := GenerateRevertProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateRevertProposal: %v", err)
	}
	if _, err := Accept(l, revert); err != nil {
		t.Fatalf("Accept (revert): %v", err)
	}

	// Immediately after accepting: cloud is untouched, still "staging" --
	// ubx never applies a revert itself (see docs/architecture.md). Honest
	// behavior here is to STILL report drift: the ledger's truth (FoldState)
	// is "prod" again (the revert restored it), but reality hasn't caught
	// up yet, so they genuinely differ -- this is correctly recorded as an
	// outstanding drift, not silently treated as resolved just because a
	// decision was made.
	resAfterAccept, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (immediately after accept): %v", err)
	}
	if resAfterAccept.Outcome != ScanDrifted {
		t.Fatalf("Outcome = %v, want ScanDrifted (ledger truth is 'prod' again, cloud hasn't caught up yet)", resAfterAccept.Outcome)
	}

	// Now simulate the manual correction (the live-verify scenario's `aws`
	// CLI step): cloud goes back to "prod", matching the ledger's own
	// FoldState-reconstructed truth.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)

	resAfterCorrection, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (after manual correction): %v", err)
	}
	if resAfterCorrection.Outcome != ScanUnchanged {
		t.Fatalf("Outcome = %v, want ScanUnchanged -- the ledger's FoldState truth (prod) now matches reality; "+
			"a LastObservedHash-based baseline would wrongly report drift here (it'd still be pinned to the observed 'staging' value)",
			resAfterCorrection.Outcome)
	}
}

// TestGenerateRevertProposal_LedgerRecordIsAdoptionOfAlreadyDriftedState is
// the adversarial case: a resource is adopted in whatever state it already
// happened to be in (ubx has no notion of some earlier "true original" --
// it only ever knows what the ledger itself recorded). A subsequent revert
// must restore to that recorded state, not some imagined original that was
// never part of the ledger at all.
func TestGenerateRevertProposal_LedgerRecordIsAdoptionOfAlreadyDriftedState(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	// Adopted in "staging" -- this IS the ledger's recorded truth, even
	// though (unknowably, to ubx) the resource may have started life as
	// "prod" before anyone ever ran ubx against it.
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (adopt): %v", err)
	}
	adopt, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal (adopt): %v", err)
	}
	if _, err := Accept(l, adopt); err != nil {
		t.Fatalf("Accept (adopt): %v", err)
	}

	// Drift again, to "canary".
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"canary"}}`)
	res2, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (drift): %v", err)
	}
	if res2.Outcome != ScanDrifted {
		t.Fatalf("Outcome = %v, want ScanDrifted", res2.Outcome)
	}

	revert, err := GenerateRevertProposal(l, "payments", res2)
	if err != nil {
		t.Fatalf("GenerateRevertProposal: %v", err)
	}
	mod := revert.Delta.Modifies[0]
	if string(mod.Before["tags.env"]) != `"canary"` {
		t.Fatalf("before[tags.env] = %s, want %q", mod.Before["tags.env"], "canary")
	}
	// The restore target MUST be "staging" (the ledger's own recorded
	// truth from adoption) -- never "prod" (an imagined original ubx never
	// actually recorded anywhere).
	if string(mod.After["tags.env"]) != `"staging"` {
		t.Fatalf("after[tags.env] = %s, want %q (the ledger's recorded truth, not an imagined original)", mod.After["tags.env"], "staging")
	}
}

// TestVerifyFreshness_BlocksStaleAcceptance_ForDriftRevert is the "revert,
// then reality moves again before accept" adversarial case: staleness
// applies doubly, the same VerifyFreshness mechanism blocking acceptance
// unmodified.
func TestVerifyFreshness_BlocksStaleAcceptance_ForDriftRevert(t *testing.T) {
	l, fp, res := driftToStaging(t)
	addr := testAddr()

	revert, err := GenerateRevertProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateRevertProposal: %v", err)
	}

	// Reality moves again, a second time, before the revert is accepted.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"canary"}}`)

	err = VerifyFreshness(context.Background(), fp, l, addr, "", json.RawMessage(`{}`), revert)
	if err == nil {
		t.Fatal("expected VerifyFreshness to block acceptance -- reality moved again since the revert was drafted")
	}
}

func TestValidateDriftRevert_RejectsAllZeroBlastRadius(t *testing.T) {
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Kind:          KindDriftRevert,
		Intent:        Intent{Summary: "revert"},
		Delta: Delta{Modifies: []Modification{{
			Target: testAddr(),
			Before: map[string]json.RawMessage{"tags.env": json.RawMessage(`"staging"`)},
			After:  map[string]json.RawMessage{"tags.env": json.RawMessage(`"prod"`)},
		}}},
		Resolution: Resolution{
			ResolvedAt: "2026-07-16T00:00:00Z",
			Inputs: []ResolutionInput{{
				Kind: "live_state", Resource: testAddr().String(), ObservedHash: "deadbeef",
			}},
		},
		BlastRadius: BlastRadius{}, // all-zero -- wrong for drift_revert
		Status:      StatusDraft,
	}
	if err := Validate(p); err == nil {
		t.Fatal("expected an error: drift_revert must not have all-zero blast_radius")
	}
}

func TestValidateDriftRevert_RejectsCreatesOrDestroys(t *testing.T) {
	base := func() *Proposal {
		return &Proposal{
			SchemaVersion: SchemaVersion,
			Stack:         "payments",
			Kind:          KindDriftRevert,
			Intent:        Intent{Summary: "revert"},
			Delta: Delta{Modifies: []Modification{{
				Target: testAddr(),
				Before: map[string]json.RawMessage{"tags.env": json.RawMessage(`"staging"`)},
				After:  map[string]json.RawMessage{"tags.env": json.RawMessage(`"prod"`)},
			}}},
			Resolution: Resolution{
				ResolvedAt: "2026-07-16T00:00:00Z",
				Inputs: []ResolutionInput{{
					Kind: "live_state", Resource: testAddr().String(), ObservedHash: "deadbeef",
				}},
			},
			BlastRadius: BlastRadius{Modifies: 1},
			Status:      StatusDraft,
		}
	}

	withCreates := base()
	withCreates.Delta.Creates = []json.RawMessage{json.RawMessage(`{}`)}
	if err := Validate(withCreates); err == nil {
		t.Fatal("expected an error: drift_revert must not have delta.creates")
	}

	withDestroys := base()
	withDestroys.Delta.Destroys = []DestroyEntry{{Address: testAddr()}}
	if err := Validate(withDestroys); err == nil {
		t.Fatal("expected an error: drift_revert must not have delta.destroys")
	}

	withBlastDestroys := base()
	withBlastDestroys.BlastRadius.Destroys = 1
	if err := Validate(withBlastDestroys); err == nil {
		t.Fatal("expected an error: drift_revert must have zero blast_radius.destroys")
	}
}

func TestValidateDriftRevert_RequiresAtLeastOneModify(t *testing.T) {
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Kind:          KindDriftRevert,
		Intent:        Intent{Summary: "revert"},
		Delta:         Delta{},
		Resolution:    Resolution{ResolvedAt: "2026-07-16T00:00:00Z"},
		BlastRadius:   BlastRadius{},
		Status:        StatusDraft,
	}
	if err := Validate(p); err == nil {
		t.Fatal("expected an error: drift_revert with zero delta.modifies entries")
	}
}

func TestValidateDriftRevert_AcceptsCorrectShape(t *testing.T) {
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Kind:          KindDriftRevert,
		Intent:        Intent{Summary: "revert"},
		Delta: Delta{Modifies: []Modification{{
			Target: testAddr(),
			Before: map[string]json.RawMessage{"tags.env": json.RawMessage(`"staging"`)},
			After:  map[string]json.RawMessage{"tags.env": json.RawMessage(`"prod"`)},
		}}},
		Resolution: Resolution{
			ResolvedAt: "2026-07-16T00:00:00Z",
			Inputs: []ResolutionInput{{
				Kind: "live_state", Resource: testAddr().String(), ObservedHash: "deadbeef",
			}},
		},
		BlastRadius: BlastRadius{Modifies: 1},
		Status:      StatusDraft,
	}
	if err := Validate(p); err != nil {
		t.Fatalf("Validate: unexpected error for a correctly-shaped drift_revert: %v", err)
	}
}
