package core

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBlame_NeverRecorded_NotFound(t *testing.T) {
	l := Open(t.TempDir())
	result, err := Blame(l, testAddr())
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	if result.Found {
		t.Fatal("Found = true, want false for an address with no history at all")
	}
}

// A Modification touching addr with no genesis create ever recorded must
// never be silently applied -- mirrors FoldState's own `current == nil`
// guard exactly (a real regression this test would have caught: an
// earlier draft of Blame initialized current as an empty non-nil map,
// which would have defeated that guard entirely).
func TestBlame_ModifyWithNoGenesis_NeverApplied(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "orphan-modify"}
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindDriftAdopt,
		Intent:        Intent{Summary: "a modify with no prior create -- should never happen in a validated ledger"},
		Delta: Delta{Modifies: []Modification{
			{Target: addr, Before: map[string]json.RawMessage{"tags.env": json.RawMessage(`"prod"`)}, After: map[string]json.RawMessage{"tags.env": json.RawMessage(`"staging"`)}},
		}},
		Resolution: Resolution{
			ResolvedAt: "2026-07-20T00:00:00Z",
			Inputs:     []ResolutionInput{{Kind: "live_state", Resource: addr.String(), ObservedHash: "sha256:abc"}},
		},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{},
		Status:      StatusDraft,
	}
	if _, err := Accept(l, p); err != nil {
		t.Fatalf("accept: %v", err)
	}

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	if result.Found {
		t.Fatalf("Found = true, want false -- a modify with no genesis create must never be treated as a real answer: %+v", result)
	}
}

func TestBlame_AdoptionGenesis_EveryLeafAttributed(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	adopt, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	accepted, err := Accept(l, adopt)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	if !result.Found || result.Destroyed {
		t.Fatalf("result = %+v, want Found, not Destroyed", result)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("Entries = %+v, want 2 (id, tags.env)", result.Entries)
	}
	byPath := entriesByPath(result.Entries)
	for _, path := range []string{"id", "tags.env"} {
		e, ok := byPath[path]
		if !ok {
			t.Fatalf("no entry for %q: %+v", path, result.Entries)
		}
		if e.ProposalID != accepted.ID {
			t.Errorf("%s.ProposalID = %q, want %q (the genesis adoption)", path, e.ProposalID, accepted.ID)
		}
		if e.Kind != KindAdoption {
			t.Errorf("%s.Kind = %q, want %q", path, e.Kind, KindAdoption)
		}
		if e.AcceptanceMethod != "local" {
			t.Errorf("%s.AcceptanceMethod = %q, want local", path, e.AcceptanceMethod)
		}
	}
}

// row: attribute modified across multiple proposals -- latest wins,
// correctly, per attribute (never "the whole resource's own latest
// touching proposal" for every leaf).
func TestBlame_MultiTouch_LatestWinsPerAttribute(t *testing.T) {
	l, _, res := driftToStaging(t)
	addr := testAddr()

	drift, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal (drift_adopt): %v", err)
	}
	acceptedDrift, err := Accept(l, drift)
	if err != nil {
		t.Fatalf("Accept (drift_adopt): %v", err)
	}

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	byPath := entriesByPath(result.Entries)

	// "tags.env" was touched by the drift_adopt (prod -> staging) --
	// blame it there, not the original adoption.
	env, ok := byPath["tags.env"]
	if !ok {
		t.Fatalf("no entry for tags.env: %+v", result.Entries)
	}
	if env.ProposalID != acceptedDrift.ID {
		t.Errorf("tags.env.ProposalID = %q, want %q (the drift_adopt that changed it)", env.ProposalID, acceptedDrift.ID)
	}
	if env.Kind != KindDriftAdopt {
		t.Errorf("tags.env.Kind = %q, want %q", env.Kind, KindDriftAdopt)
	}
	var envVal string
	if err := json.Unmarshal(env.Value, &envVal); err != nil || envVal != "staging" {
		t.Errorf("tags.env value = %s, want \"staging\"", env.Value)
	}

	// "id" was never touched by the drift -- still blames the original
	// adoption.
	id, ok := byPath["id"]
	if !ok {
		t.Fatalf("no entry for id: %+v", result.Entries)
	}
	if id.Kind != KindAdoption {
		t.Errorf("id.Kind = %q, want %q (untouched since genesis)", id.Kind, KindAdoption)
	}
}

// row: drift_adopt-set attribute shows the attributed CloudTrail actor.
func TestBlame_CloudTrailActor(t *testing.T) {
	l, _, res := driftToStaging(t)
	addr := testAddr()

	drift, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	drift.Intent.Sources = append(drift.Intent.Sources, IntentSource{
		Kind:      "cloudtrail",
		ActorARN:  "arn:aws:iam::123456789012:user/alice",
		EventID:   "evt-1",
		EventName: "ModifyBucketTagging",
		EventTime: "2026-07-20T10:00:00Z",
	})
	accepted, err := Accept(l, drift)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	byPath := entriesByPath(result.Entries)
	env, ok := byPath["tags.env"]
	if !ok {
		t.Fatalf("no entry for tags.env: %+v", result.Entries)
	}
	if env.ProposalID != accepted.ID {
		t.Fatalf("tags.env.ProposalID = %q, want %q", env.ProposalID, accepted.ID)
	}
	if len(env.AttributedActors) != 1 || env.AttributedActors[0] != "arn:aws:iam::123456789012:user/alice" {
		t.Fatalf("AttributedActors = %v, want [arn:aws:iam::123456789012:user/alice]", env.AttributedActors)
	}
}

// row: redacted attribute provenance -- material never shown, provenance
// (who/when) still fully populated.
func TestBlame_RedactedAttributeProvenance(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "payments-db"}

	state, err := json.Marshal(map[string]interface{}{
		"id":       "db-1",
		"password": map[string]interface{}{RedactedMarkerKey: map[string]interface{}{"sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted := adoptForTest(t, l, addr, state)

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	byPath := entriesByPath(result.Entries)
	pw, ok := byPath["password"]
	if !ok {
		t.Fatalf("no entry for password: %+v", result.Entries)
	}
	if !pw.Redacted {
		t.Fatal("Redacted = false, want true")
	}
	if pw.ProposalID != accepted.ID {
		t.Fatalf("password.ProposalID = %q, want %q -- provenance must survive redaction", pw.ProposalID, accepted.ID)
	}
	if pw.SetAt == "" {
		t.Fatal("password.SetAt is empty -- provenance must survive redaction")
	}
}

// row: address with apply records (create-genesis, UBI-29 discovery
// path) -- genesis VALUES come from the real post-apply provider result,
// never from Delta.Creates' own config (which may carry stale $computed
// markers or simply predate the applied values), while genesis
// ATTRIBUTION still correctly names the change proposal that created it.
func TestBlame_ShippedCreateGenesis_UsesApplyRecordNotConfig(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "fake_widget", Name: "main"}
	result := json.RawMessage(`{"id":"computed-id","name":"main","tags":{"team":"payments"}}`)

	accepted := shipChangeCreateForTest(t, l, addr, result, true, true)

	blame, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	if !blame.Found {
		t.Fatal("Found = false, want true (shipped and sealed)")
	}
	byPath := entriesByPath(blame.Entries)
	for _, path := range []string{"id", "name", "tags.team"} {
		e, ok := byPath[path]
		if !ok {
			t.Fatalf("no entry for %q: %+v", path, blame.Entries)
		}
		if e.ProposalID != accepted.ID {
			t.Errorf("%s.ProposalID = %q, want %q", path, e.ProposalID, accepted.ID)
		}
		if e.Kind != KindChange {
			t.Errorf("%s.Kind = %q, want %q", path, e.Kind, KindChange)
		}
		if e.SetAt == "" {
			t.Errorf("%s.SetAt is empty, want the real apply timestamp", path)
		}
	}
	// "id" is computed -- shipChangeCreateForTest's own node config never
	// names it (only "name" is in config, per its own construction) --
	// confirming this value came from the apply record, not Delta.Creates.
	idEntry := byPath["id"]
	var idVal string
	if err := json.Unmarshal(idEntry.Value, &idVal); err != nil || idVal != "computed-id" {
		t.Fatalf("id value = %s, want \"computed-id\" (from the apply record, config never set it)", idEntry.Value)
	}
}

func TestBlame_UnshippedCreate_NotFound(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "fake_widget", Name: "main"}
	shipChangeInFlightForTest(t, l, addr)

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	if result.Found {
		t.Fatal("Found = true, want false -- a create that never sealed a real apply hasn't happened yet")
	}
}

// TestBlame_UnshippedModify_AttributesNothingNew is UBI-89 P1's own
// regression test for Blame's own independent instance of the exact same
// bug FoldState just got fixed for: Blame's own doc comment already
// claimed to mirror FoldState "mechanically," but its own modify loop had
// no shipped-gate at all before this session -- an accepted-but-never-
// attempted kind:change modify would attribute its own attempted value to
// "who set this," exactly the same trust-boundary lie, just surfaced via
// `ubx blame` instead of `ubx why`/`ubx status --drift`.
func TestBlame_UnshippedModify_AttributesNothingNew(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "fake_widget", Name: "main"}
	genesis := shipChangeCreateForTest(t, l, addr, json.RawMessage(`{"id":"computed-id","name":"main","tags":{"team":"payments"}}`), true, true)

	before := map[string]json.RawMessage{"tags.team": json.RawMessage(`"payments"`)}
	after := map[string]json.RawMessage{"tags.team": json.RawMessage(`"platform"`)}
	shipChangeModifyForTest(t, l, addr, before, after, false, false)

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	byPath := entriesByPath(result.Entries)
	e, ok := byPath["tags.team"]
	if !ok {
		t.Fatalf("no entry for tags.team: %+v", result.Entries)
	}
	if e.ProposalID != genesis.ID {
		t.Errorf("tags.team.ProposalID = %q, want %q (the genesis create -- the never-attempted modify must not be attributed)", e.ProposalID, genesis.ID)
	}
	var val string
	if err := json.Unmarshal(e.Value, &val); err != nil || val != "payments" {
		t.Errorf("tags.team value = %s, want \"payments\" (the modify never actually shipped)", e.Value)
	}
}

// TestBlame_FailedModify_AttributesNothingNew is the exact UBI-89 repro
// shape: a modify WAS attempted (VerifyFreshness's own ErrStaleObservation
// refusal, core/executor transitioning straight to ResourceFailed) but
// never reached ResourceApplied -- Blame must still attribute the genesis
// create, never the failed modify's own attempted value.
func TestBlame_FailedModify_AttributesNothingNew(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "fake_widget", Name: "main"}
	genesis := shipChangeCreateForTest(t, l, addr, json.RawMessage(`{"id":"computed-id","name":"main","tags":{"team":"payments"}}`), true, true)

	before := map[string]json.RawMessage{"tags.team": json.RawMessage(`"payments"`)}
	after := map[string]json.RawMessage{"tags.team": json.RawMessage(`"platform"`)}
	shipChangeModifyForTest(t, l, addr, before, after, true, false)

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	byPath := entriesByPath(result.Entries)
	e, ok := byPath["tags.team"]
	if !ok {
		t.Fatalf("no entry for tags.team: %+v", result.Entries)
	}
	if e.ProposalID != genesis.ID {
		t.Errorf("tags.team.ProposalID = %q, want %q (the genesis create -- the FAILED modify must not be attributed)", e.ProposalID, genesis.ID)
	}
	var val string
	if err := json.Unmarshal(e.Value, &val); err != nil || val != "payments" {
		t.Errorf("tags.team value = %s, want \"payments\" (the modify failed to ship)", e.Value)
	}
}

// TestBlame_ShippedModify_AttributesCorrectly is the positive baseline: a
// genuinely shipped modify must still be attributed correctly -- the
// shipped-gate this session adds must never suppress a REAL attribution.
func TestBlame_ShippedModify_AttributesCorrectly(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "fake_widget", Name: "main"}
	shipChangeCreateForTest(t, l, addr, json.RawMessage(`{"id":"computed-id","name":"main","tags":{"team":"payments"}}`), true, true)

	before := map[string]json.RawMessage{"tags.team": json.RawMessage(`"payments"`)}
	after := map[string]json.RawMessage{"tags.team": json.RawMessage(`"platform"`)}
	modified := shipChangeModifyForTest(t, l, addr, before, after, true, true)

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	byPath := entriesByPath(result.Entries)
	e, ok := byPath["tags.team"]
	if !ok {
		t.Fatalf("no entry for tags.team: %+v", result.Entries)
	}
	if e.ProposalID != modified.ID {
		t.Errorf("tags.team.ProposalID = %q, want %q (the shipped modify)", e.ProposalID, modified.ID)
	}
	var val string
	if err := json.Unmarshal(e.Value, &val); err != nil || val != "platform" {
		t.Errorf("tags.team value = %s, want \"platform\"", e.Value)
	}
}

func TestBlame_DestroyedAddress_BlamesFinalPreDestroyState(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "old-db"}
	state := json.RawMessage(`{"id":"db-1","engine":"postgres"}`)

	adoptForTest(t, l, addr, state)
	acceptedDestroy := shipChangeDestroyForTest(t, l, addr, state, "destroyed", true, true)

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	if !result.Destroyed {
		t.Fatal("Destroyed = false, want true")
	}
	if result.DestroyedBy == nil || result.DestroyedBy.ProposalID != acceptedDestroy.ID {
		t.Fatalf("DestroyedBy = %+v, want proposal %s", result.DestroyedBy, acceptedDestroy.ID)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("Entries = %+v, want 2 (the final pre-destroy state, not cleared)", result.Entries)
	}
}

func TestBlame_PrMergeApprovers(t *testing.T) {
	l, _, res := driftToStaging(t)
	addr := testAddr()

	drift, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	hash, err := Hash(drift)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	accepted, err := AcceptFromMerge(l, drift, hash, MergeAcceptance{
		MergeSHA:  "8c1d2e3f",
		PRNumber:  7,
		Approvers: []string{"roozbeh", "alice"},
	})
	if err != nil {
		t.Fatalf("AcceptFromMerge: %v", err)
	}

	result, err := Blame(l, addr)
	if err != nil {
		t.Fatalf("Blame: %v", err)
	}
	env := entriesByPath(result.Entries)["tags.env"]
	if env.ProposalID != accepted.ID {
		t.Fatalf("tags.env.ProposalID = %q, want %q", env.ProposalID, accepted.ID)
	}
	if env.AcceptanceMethod != "pr_merge" {
		t.Fatalf("AcceptanceMethod = %q, want pr_merge", env.AcceptanceMethod)
	}
	if len(env.Approvers) != 2 || env.Approvers[0] != "roozbeh" || env.Approvers[1] != "alice" {
		t.Fatalf("Approvers = %v, want [roozbeh alice]", env.Approvers)
	}
}

func entriesByPath(entries []BlameEntry) map[string]BlameEntry {
	m := make(map[string]BlameEntry, len(entries))
	for _, e := range entries {
		m[e.Path] = e
	}
	return m
}
