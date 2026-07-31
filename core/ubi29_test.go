package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// shipChangeCreateForTest hand-builds and accepts a kind:"change" proposal
// with one create for addr, then hand-builds its own apply record showing
// that resource applied with result -- the core-package equivalent of
// core/executor.Ship's own shipCreate (core can't import core/executor;
// the reverse import already exists), used to exercise the UBI-29
// discovery fold (Fleet/FoldState/ProposalsForAddress/LastObservedHash)
// against a real accepted proposal + real apply record, never a
// hand-waved shortcut. seal controls whether SealApply is ever called --
// false simulates a real `kill -9` before the attempt was sealed, proving
// discovery gates on the resource's own transition, not the record's
// sealed status. recordLookup controls whether ra.Lookup is set at ship
// time, simulating an apply record that predates the UBI-29 amendment
// when false (row 2's own adversarial case).
func shipChangeCreateForTest(t *testing.T, l *Ledger, addr Address, result json.RawMessage, seal, recordLookup bool) *Proposal {
	t.Helper()
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"config": map[string]interface{}{"name": addr.Name},
	})
	if err != nil {
		t.Fatal(err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindChange,
		Intent:        Intent{Summary: "test create " + addr.String()},
		Delta:         Delta{Creates: []json.RawMessage{node}},
		Resolution:    Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339)},
		CostDelta:     CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius:   BlastRadius{Creates: 1},
		Status:        StatusDraft,
	}
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("accept change: %v", err)
	}

	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ra := &ResourceApply{
		Address: addr,
		Transitions: []Transition{
			{State: ResourcePending, At: now},
			{State: ResourceInFlight, At: now},
			{State: ResourceApplied, At: now},
		},
		ProviderResult: result,
	}
	if recordLookup {
		ra.Lookup = DeriveLookupFromResult(result, nil)
	}
	rec.Resources = append(rec.Resources, ra)
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	if seal {
		if _, err := l.SealApply(rec, ApplySummary{
			Outcome: "applied", StartedAt: now, FinishedAt: now, ResourcesApplied: 1,
		}); err != nil {
			t.Fatalf("seal apply: %v", err)
		}
	}
	return accepted
}

// shipChangeInFlightForTest hand-builds a change proposal whose create
// never got past in_flight -- a real `kill -9` mid-create, left unsealed
// (docs/resolver-adversarial.md-style row 3): discovery must never
// surface this resource as watchable.
func shipChangeInFlightForTest(t *testing.T, l *Ledger, addr Address) *Proposal {
	t.Helper()
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"config": map[string]interface{}{"name": addr.Name},
	})
	if err != nil {
		t.Fatal(err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindChange,
		Intent:        Intent{Summary: "test in-flight create " + addr.String()},
		Delta:         Delta{Creates: []json.RawMessage{node}},
		Resolution:    Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339)},
		CostDelta:     CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius:   BlastRadius{Creates: 1},
		Status:        StatusDraft,
	}
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("accept change: %v", err)
	}

	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ra := &ResourceApply{
		Address: addr,
		Transitions: []Transition{
			{State: ResourcePending, At: now},
			{State: ResourceInFlight, At: now},
		},
		// Never reaches ResourceApplied: the process died mid-call.
	}
	rec.Resources = append(rec.Resources, ra)
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	// Left unsealed -- exactly what a real kill -9 leaves behind.
	return accepted
}

func TestUBI29_Fleet_ShippedCreate_Discoverable(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_sqs_queue", Name: "chain-a"}
	result := json.RawMessage(`{"id":"https://sqs.example/chain-a","url":"https://sqs.example/chain-a","name":"chain-a"}`)
	p := shipChangeCreateForTest(t, l, addr, result, true, true)

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 -- a shipped create must be Fleet-discoverable", len(entries))
	}
	e := entries[0]
	if e.Address != addr {
		t.Fatalf("Address = %+v, want %+v", e.Address, addr)
	}
	if e.Kind != KindChange {
		t.Fatalf("Kind = %q, want %q", e.Kind, KindChange)
	}
	if e.ProposalID != p.ID {
		t.Fatalf("ProposalID = %q, want %q", e.ProposalID, p.ID)
	}
	var lookup struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(e.Lookup, &lookup); err != nil {
		t.Fatalf("decode lookup: %v (raw: %s)", err, e.Lookup)
	}
	if lookup.ID != "https://sqs.example/chain-a" {
		t.Fatalf("lookup.id = %q, want the applied id", lookup.ID)
	}
}

func TestUBI29_FoldState_ShippedCreate_SeedsFromApplyRecord(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_sqs_queue", Name: "chain-a"}
	result := json.RawMessage(`{"id":"q-1","url":"https://sqs.example/q-1","name":"chain-a"}`)
	shipChangeCreateForTest(t, l, addr, result, true, true)

	state, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("FoldState: %v", err)
	}
	if !found {
		t.Fatal("FoldState found=false, want true -- a shipped create's real applied state must seed FoldState")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(state, &decoded); err != nil {
		t.Fatalf("decode folded state: %v", err)
	}
	if decoded["url"] != "https://sqs.example/q-1" {
		t.Fatalf("folded state url = %v, want the real applied url (not $computed markers or the pre-ship config)", decoded["url"])
	}
}

func TestUBI29_ProposalsForAddress_ShippedCreate_IncludesCreateProposal(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_sqs_queue", Name: "chain-a"}
	result := json.RawMessage(`{"id":"q-1","url":"https://sqs.example/q-1","name":"chain-a"}`)
	p := shipChangeCreateForTest(t, l, addr, result, true, true)

	proposals, err := l.ProposalsForAddress(addr)
	if err != nil {
		t.Fatalf("ProposalsForAddress: %v", err)
	}
	if len(proposals) != 1 || proposals[0].ID != p.ID {
		t.Fatalf("proposals = %+v, want exactly [%s] -- ubx why <address> must show the create as genesis", proposals, p.ID)
	}
}

func TestUBI29_LastObservedHashAndTime_ShippedCreate(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_sqs_queue", Name: "chain-a"}
	result := json.RawMessage(`{"id":"q-1","url":"https://sqs.example/q-1","name":"chain-a"}`)
	shipChangeCreateForTest(t, l, addr, result, true, true)

	hash, found, err := l.LastObservedHash(addr)
	if err != nil {
		t.Fatalf("LastObservedHash: %v", err)
	}
	if !found || hash == "" {
		t.Fatalf("LastObservedHash found=%v hash=%q, want a real hash derived from the apply record", found, hash)
	}
	wantHash, err := ObservedHash(result)
	if err != nil {
		t.Fatal(err)
	}
	if hash != wantHash {
		t.Fatalf("hash = %q, want %q (hash of the real applied result)", hash, wantHash)
	}

	when, found, err := l.LastObservationTime(addr)
	if err != nil {
		t.Fatalf("LastObservationTime: %v", err)
	}
	if !found || when.IsZero() {
		t.Fatal("LastObservationTime found=false or zero, want the apply record's own applied-transition time")
	}
}

// TestUBI29_CreatedThenDrifted_FullLifecycle is the first named adversarial
// row: a resource whose only genesis is a shipped create, later drifted
// out-of-band, must be discoverable, scannable, and correctable exactly
// like an adopted resource.
func TestUBI29_CreatedThenDrifted_FullLifecycle(t *testing.T) {
	l := Open(t.TempDir())
	// aws_s3_bucket, not aws_sqs_queue: fakeProvider (scan_test.go) only
	// ever advertises this one type in its own hardcoded Schema() --
	// RunScan needs the type recognized, unrelated to what this test is
	// actually about.
	addr := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "chain-a"}
	result := json.RawMessage(`{"id":"q-1","url":"https://sqs.example/q-1","tags":{"env":"prod"}}`)
	shipChangeCreateForTest(t, l, addr, result, true, true)

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d fleet entries, want 1", len(entries))
	}
	lookup := entries[0].Lookup

	fp := &fakeProvider{state: result}
	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: lookup})
	if err != nil {
		t.Fatalf("RunScan (unchanged): %v", err)
	}
	if res.Outcome != ScanUnchanged {
		t.Fatalf("outcome = %v, want ScanUnchanged (nothing has drifted yet)", res.Outcome)
	}

	// Out-of-band mutation.
	drifted := json.RawMessage(`{"id":"q-1","url":"https://sqs.example/q-1","tags":{"env":"staging"}}`)
	fp.state = drifted
	res, err = RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: lookup})
	if err != nil {
		t.Fatalf("RunScan (drifted): %v", err)
	}
	if res.Outcome != ScanDrifted {
		t.Fatalf("outcome = %v, want ScanDrifted -- a shipped-create resource must participate in drift detection like any other", res.Outcome)
	}

	driftProposal, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	if driftProposal.Kind != KindDriftAdopt {
		t.Fatalf("Kind = %q, want %q", driftProposal.Kind, KindDriftAdopt)
	}
	if _, err := Accept(l, driftProposal); err != nil {
		t.Fatalf("accept drift_adopt: %v", err)
	}

	// Fleet now reflects the drift_adopt as the latest touching proposal.
	entries, err = l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet (after drift): %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != KindDriftAdopt {
		t.Fatalf("entries = %+v, want exactly one KindDriftAdopt entry", entries)
	}

	state, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("FoldState (after drift): %v", err)
	}
	if !found {
		t.Fatal("FoldState found=false after drift_adopt, want true")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(state, &decoded); err != nil {
		t.Fatal(err)
	}
	tags, _ := decoded["tags"].(map[string]interface{})
	if tags["env"] != "staging" {
		t.Fatalf("folded tags.env = %v, want %q (the newly recorded drift)", tags["env"], "staging")
	}
}

// TestUBI29_OldApplyRecord_NoLookupField_DerivesGracefully is the second
// named adversarial row: an apply record that predates the UBI-29
// amendment (no Lookup ever recorded on its ResourceApply entry) must
// still be discoverable, deriving {"id": ...} from provider_result at
// read time rather than reporting an error.
func TestUBI29_OldApplyRecord_NoLookupField_DerivesGracefully(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_sqs_queue", Name: "chain-a"}
	result := json.RawMessage(`{"id":"q-old","url":"https://sqs.example/q-old"}`)
	shipChangeCreateForTest(t, l, addr, result, true, false) // recordLookup=false: simulates a pre-UBI-29 record

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	var lookup struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(entries[0].Lookup, &lookup); err != nil {
		t.Fatalf("decode derived lookup: %v (raw: %s)", err, entries[0].Lookup)
	}
	if lookup.ID != "q-old" {
		t.Fatalf("derived lookup.id = %q, want %q (gracefully derived from provider_result, not a hard failure)", lookup.ID, "q-old")
	}

	state, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("FoldState: %v", err)
	}
	if !found {
		t.Fatal("FoldState found=false for an old-shaped apply record, want true (graceful derivation, not a hard requirement)")
	}
	_ = state
}

// TestUBI29_KillMidCreate_UnsealedRecord_NeverDiscoverable is the third
// named adversarial row: a resource that never reached `applied` (a real
// `kill -9` mid-create) must not be surfaced as watchable by Fleet,
// FoldState, or ProposalsForAddress, sealed attempt or not.
func TestUBI29_KillMidCreate_UnsealedRecord_NeverDiscoverable(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_sqs_queue", Name: "half-created"}
	shipChangeInFlightForTest(t, l, addr)

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d fleet entries, want 0 -- a half-created resource must never be discoverable", len(entries))
	}

	_, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("FoldState: %v", err)
	}
	if found {
		t.Fatal("FoldState found=true for a half-created resource, want false")
	}

	proposals, err := l.ProposalsForAddress(addr)
	if err != nil {
		t.Fatalf("ProposalsForAddress: %v", err)
	}
	if len(proposals) != 0 {
		t.Fatalf("got %d proposals for a half-created resource, want 0", len(proposals))
	}

	_, found, err = l.LastObservedHash(addr)
	if err != nil {
		t.Fatalf("LastObservedHash: %v", err)
	}
	if found {
		t.Fatal("LastObservedHash found=true for a half-created resource, want false")
	}
}

// TestUBI29_MixedAttempt_OnlyAppliedResourceDiscoverable proves the design
// doc's own key claim directly: within ONE unsealed multi-resource apply
// attempt, a resource that reached applied is discoverable while a
// sibling that didn't is not -- gating is per-resource, never per-attempt.
func TestUBI29_MixedAttempt_OnlyAppliedResourceDiscoverable(t *testing.T) {
	l := Open(t.TempDir())
	primary := Address{Stack: "payments", Type: "aws_sqs_queue", Name: "primary"}
	mirror := Address{Stack: "payments", Type: "aws_sqs_queue", Name: "mirror"}

	primaryNode, _ := json.Marshal(map[string]interface{}{
		"stack": primary.Stack, "type": primary.Type, "name": primary.Name,
		"config": map[string]interface{}{"name": primary.Name},
	})
	mirrorNode, _ := json.Marshal(map[string]interface{}{
		"stack": mirror.Stack, "type": mirror.Type, "name": mirror.Name,
		"config":     map[string]interface{}{"name": mirror.Name},
		"depends_on": []string{primary.String()},
	})
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Parent:        head,
		Kind:          KindChange,
		Intent:        Intent{Summary: "mixed attempt test"},
		Delta:         Delta{Creates: []json.RawMessage{primaryNode, mirrorNode}},
		Resolution:    Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339)},
		CostDelta:     CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius:   BlastRadius{Creates: 2},
		Status:        StatusDraft,
	}
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	primaryResult := json.RawMessage(`{"id":"primary-real-id"}`)
	rec.Resources = append(rec.Resources, &ResourceApply{
		Address:        primary,
		Transitions:    []Transition{{State: ResourcePending, At: now}, {State: ResourceInFlight, At: now}, {State: ResourceApplied, At: now}},
		ProviderResult: primaryResult,
		Lookup:         DeriveLookupFromResult(primaryResult, nil),
	})
	// mirror never got its own turn -- the process died right after primary.
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	// Left unsealed.

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d fleet entries, want exactly 1 (primary only)", len(entries))
	}
	if entries[0].Address != primary {
		t.Fatalf("the one discoverable entry = %+v, want %+v", entries[0].Address, primary)
	}

	_, found, err := l.FoldState(mirror)
	if err != nil {
		t.Fatalf("FoldState (mirror): %v", err)
	}
	if found {
		t.Fatal("FoldState found=true for mirror, which never applied -- want false")
	}
	_, found, err = l.FoldState(primary)
	if err != nil {
		t.Fatalf("FoldState (primary): %v", err)
	}
	if !found {
		t.Fatal("FoldState found=false for primary, which DID apply -- want true even though the enclosing attempt is unsealed")
	}
}
