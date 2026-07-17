package core

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDiffAttributes_NestedDotPath(t *testing.T) {
	before := json.RawMessage(`{"tags":{"env":"prod","owner":"payments"},"count":3}`)
	after := json.RawMessage(`{"tags":{"env":"staging","owner":"payments"},"count":3}`)

	b, a, err := DiffAttributes(before, after)
	if err != nil {
		t.Fatalf("diffAttributes: %v", err)
	}
	if len(b) != 1 || string(b["tags.env"]) != `"prod"` {
		t.Fatalf("before diff = %v, want just tags.env=prod", b)
	}
	if len(a) != 1 || string(a["tags.env"]) != `"staging"` {
		t.Fatalf("after diff = %v, want just tags.env=staging", a)
	}
}

func TestDiffAttributes_AddedAndRemovedKeys(t *testing.T) {
	before := json.RawMessage(`{"a":1}`)
	after := json.RawMessage(`{"b":2}`)

	b, a, err := DiffAttributes(before, after)
	if err != nil {
		t.Fatalf("diffAttributes: %v", err)
	}
	if _, ok := b["a"]; !ok {
		t.Fatalf("expected removed key 'a' in before diff, got %v", b)
	}
	if _, ok := a["a"]; ok {
		t.Fatalf("removed key 'a' should not appear in after diff, got %v", a)
	}
	if _, ok := a["b"]; !ok {
		t.Fatalf("expected added key 'b' in after diff, got %v", a)
	}
	if _, ok := b["b"]; ok {
		t.Fatalf("added key 'b' should not appear in before diff, got %v", b)
	}
}

func TestDiffAttributes_ArraysAreAtomic(t *testing.T) {
	before := json.RawMessage(`{"list":[1,2,3]}`)
	after := json.RawMessage(`{"list":[1,2,4]}`)

	b, a, err := DiffAttributes(before, after)
	if err != nil {
		t.Fatalf("diffAttributes: %v", err)
	}
	if len(b) != 1 || len(a) != 1 {
		t.Fatalf("expected exactly one changed key each, got before=%v after=%v", b, a)
	}
}

func TestDiffAttributes_NoChangesProducesEmptyDiff(t *testing.T) {
	same := json.RawMessage(`{"a":1,"nested":{"b":2}}`)
	b, a, err := DiffAttributes(same, same)
	if err != nil {
		t.Fatalf("diffAttributes: %v", err)
	}
	if len(b) != 0 || len(a) != 0 {
		t.Fatalf("expected empty diffs, got before=%v after=%v", b, a)
	}
}

// TestDiffAttributes_RedactedValueUnchangedProducesNoDiff and
// TestDiffAttributes_RedactedValueChangedIsAtomic are UBI-23's core
// adversarial cases: a $redacted marker (docs/schema.md's value-encoding
// amendment) must never be recursed into by diffObjects -- an unchanged
// redacted value (same hash) must diff as nothing, and a changed one
// (different hash -- the real underlying secret changed) must diff as one
// whole-attribute change, never a spurious "attr.$redacted.sha256: ..."
// sub-path.
func TestDiffAttributes_RedactedValueUnchangedProducesNoDiff(t *testing.T) {
	same := json.RawMessage(`{"password":{"$redacted":{"sha256":"abc123"}}}`)
	b, a, err := DiffAttributes(same, same)
	if err != nil {
		t.Fatalf("diffAttributes: %v", err)
	}
	if len(b) != 0 || len(a) != 0 {
		t.Fatalf("expected an unchanged redacted value to produce no diff, got before=%v after=%v", b, a)
	}
}

func TestDiffAttributes_RedactedValueChangedIsAtomic(t *testing.T) {
	before := json.RawMessage(`{"password":{"$redacted":{"sha256":"hash-of-old-value"}}}`)
	after := json.RawMessage(`{"password":{"$redacted":{"sha256":"hash-of-new-value"}}}`)

	b, a, err := DiffAttributes(before, after)
	if err != nil {
		t.Fatalf("diffAttributes: %v", err)
	}
	if len(b) != 1 || len(a) != 1 {
		t.Fatalf("expected exactly one changed path each, got before=%v after=%v", b, a)
	}
	// The changed path must be "password" itself -- never a sub-path like
	// "password.$redacted.sha256" -- and its value must be the whole
	// $redacted object.
	if !IsRedactedValue(b["password"]) {
		t.Fatalf("expected before[\"password\"] to still be a $redacted marker, got %s", b["password"])
	}
	if !IsRedactedValue(a["password"]) {
		t.Fatalf("expected after[\"password\"] to still be a $redacted marker, got %s", a["password"])
	}
	if _, ok := b["password.$redacted.sha256"]; ok {
		t.Fatal("diff must not recurse into a $redacted marker's own fields")
	}
}

// TestFoldState_OverRedactedValues confirms the full adoption -> drift ->
// fold chain works correctly when the changing attribute is a redacted
// one (UBI-23): FoldState is pure dot-path JSON manipulation and needs no
// awareness of $redacted at all, but this proves that empirically rather
// than assuming it from FoldState's own code shape.
func TestFoldState_OverRedactedValues(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	ctx := context.Background()
	lookup := json.RawMessage(`{"id":"ubx-states"}`)

	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","password":{"$redacted":{"sha256":"hash-v1"}}}`)}

	res, err := RunScan(ctx, fp, l, ScanRequest{Address: addr, CurrentState: lookup})
	if err != nil {
		t.Fatalf("RunScan (adopt): %v", err)
	}
	if res.Outcome != ScanNew {
		t.Fatalf("expected ScanNew, got %v", res.Outcome)
	}
	p, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal (adopt): %v", err)
	}
	if _, err := Accept(l, p); err != nil {
		t.Fatalf("Accept (adopt): %v", err)
	}

	// Re-scanning against the exact same redacted value must report no
	// drift -- proves redaction doesn't spuriously fire drift on every
	// scan just because the attribute is sensitive.
	res, err = RunScan(ctx, fp, l, ScanRequest{Address: addr, CurrentState: lookup})
	if err != nil {
		t.Fatalf("RunScan (unchanged): %v", err)
	}
	if res.Outcome != ScanUnchanged {
		t.Fatalf("expected ScanUnchanged for an unchanged redacted value, got %v", res.Outcome)
	}

	// The real secret changes (a different redacted hash) -- drift must
	// fire.
	fp.state = json.RawMessage(`{"id":"ubx-states","password":{"$redacted":{"sha256":"hash-v2"}}}`)
	res, err = RunScan(ctx, fp, l, ScanRequest{Address: addr, CurrentState: lookup})
	if err != nil {
		t.Fatalf("RunScan (drift): %v", err)
	}
	if res.Outcome != ScanDrifted {
		t.Fatalf("expected ScanDrifted when the redacted hash changes, got %v", res.Outcome)
	}
	p, err = GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal (drift): %v", err)
	}
	mod := p.Delta.Modifies[0]
	if !IsRedactedValue(mod.Before["password"]) || !IsRedactedValue(mod.After["password"]) {
		t.Fatalf("expected both before/after to be $redacted markers, got before=%s after=%s", mod.Before["password"], mod.After["password"])
	}
	if _, err := Accept(l, p); err != nil {
		t.Fatalf("Accept (drift): %v", err)
	}

	folded, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("FoldState: %v", err)
	}
	if !found {
		t.Fatal("FoldState: not found")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(folded, &decoded); err != nil {
		t.Fatalf("decode folded state: %v", err)
	}
	pw, err := json.Marshal(decoded["password"])
	if err != nil {
		t.Fatalf("marshal folded password: %v", err)
	}
	if !IsRedactedValue(pw) {
		t.Fatalf("expected FoldState to carry forward a $redacted marker, got %s", pw)
	}

	// Re-scanning against the folded (latest) redacted value should now
	// report clean again.
	res, err = RunScan(ctx, fp, l, ScanRequest{Address: addr, CurrentState: lookup})
	if err != nil {
		t.Fatalf("RunScan (settled): %v", err)
	}
	if res.Outcome != ScanUnchanged {
		t.Fatalf("expected ScanUnchanged after the drift settles, got %v", res.Outcome)
	}
}

func TestFoldState_MultiLevelDrift(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"},"versioning":true}`)}
	ctx := context.Background()
	lookup := json.RawMessage(`{"id":"ubx-states"}`)

	// Adopt.
	res, err := RunScan(ctx, fp, l, ScanRequest{Address: addr, CurrentState: lookup})
	if err != nil {
		t.Fatalf("RunScan (adopt): %v", err)
	}
	p, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal (adopt): %v", err)
	}
	if _, err := Accept(l, p); err != nil {
		t.Fatalf("Accept (adopt): %v", err)
	}

	// First drift: env changes.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"},"versioning":true}`)
	res, err = RunScan(ctx, fp, l, ScanRequest{Address: addr, CurrentState: lookup})
	if err != nil {
		t.Fatalf("RunScan (drift 1): %v", err)
	}
	p, err = GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal (drift 1): %v", err)
	}
	if _, err := Accept(l, p); err != nil {
		t.Fatalf("Accept (drift 1): %v", err)
	}

	// Second drift: versioning changes too, env stays at its drifted value.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"},"versioning":false}`)
	res, err = RunScan(ctx, fp, l, ScanRequest{Address: addr, CurrentState: lookup})
	if err != nil {
		t.Fatalf("RunScan (drift 2): %v", err)
	}
	p, err = GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal (drift 2): %v", err)
	}
	// The second drift's diff should only mention versioning, not env
	// (which didn't change between drift 1 and drift 2).
	mod := p.Delta.Modifies[0]
	if _, ok := mod.Before["tags.env"]; ok {
		t.Fatalf("unchanged tags.env leaked into drift 2's diff: %v", mod.Before)
	}
	if string(mod.After["versioning"]) != "false" {
		t.Fatalf("after[versioning] = %s, want false", mod.After["versioning"])
	}
	if _, err := Accept(l, p); err != nil {
		t.Fatalf("Accept (drift 2): %v", err)
	}

	// FoldState should now reconstruct the fully current state: env staged,
	// versioning false.
	folded, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("FoldState: %v", err)
	}
	if !found {
		t.Fatal("FoldState: not found")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(folded, &decoded); err != nil {
		t.Fatalf("decode folded state: %v", err)
	}
	tags, _ := decoded["tags"].(map[string]interface{})
	if tags["env"] != "staging" {
		t.Fatalf("folded tags.env = %v, want staging", tags["env"])
	}
	if decoded["versioning"] != false {
		t.Fatalf("folded versioning = %v, want false", decoded["versioning"])
	}
}

func TestLedger_ProposalsForAddress_ChainOrder(t *testing.T) {
	l := Open(t.TempDir())
	ctx := context.Background()
	addr := testAddr()

	// Adopt.
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}
	res1, err := RunScan(ctx, fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (adopt): %v", err)
	}
	p1, err := GenerateProposal(l, "payments", res1)
	if err != nil {
		t.Fatalf("GenerateProposal (adopt): %v", err)
	}
	if _, err := Accept(l, p1); err != nil {
		t.Fatalf("Accept (adopt): %v", err)
	}

	// Drift.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"}}`)
	res2, err := RunScan(ctx, fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (drift): %v", err)
	}
	p2, err := GenerateProposal(l, "payments", res2)
	if err != nil {
		t.Fatalf("GenerateProposal (drift): %v", err)
	}
	accepted2, err := Accept(l, p2)
	if err != nil {
		t.Fatalf("Accept (drift): %v", err)
	}

	proposals, err := l.ProposalsForAddress(addr)
	if err != nil {
		t.Fatalf("ProposalsForAddress: %v", err)
	}
	if len(proposals) != 2 {
		t.Fatalf("len(proposals) = %d, want 2", len(proposals))
	}
	if proposals[0].Kind != KindAdoption {
		t.Errorf("proposals[0].Kind = %q, want %q (oldest first)", proposals[0].Kind, KindAdoption)
	}
	if proposals[1].Kind != KindDriftAdopt || proposals[1].ID != accepted2.ID {
		t.Errorf("proposals[1] = %+v, want the drift_adopt proposal (newest last, chain order)", proposals[1])
	}
}

func TestLedger_ProposalsForAddress_UnknownAddressIsEmptyNotError(t *testing.T) {
	l := Open(t.TempDir())
	proposals, err := l.ProposalsForAddress(Address{Stack: "payments", Type: "aws_s3_bucket", Name: "never-scanned"})
	if err != nil {
		t.Fatalf("ProposalsForAddress: %v", err)
	}
	if len(proposals) != 0 {
		t.Fatalf("len(proposals) = %d, want 0", len(proposals))
	}
}

func TestLedger_LastObservedHash_IsolatedPerAddress(t *testing.T) {
	l := Open(t.TempDir())
	ctx := context.Background()

	addrA := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "a"}
	addrB := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "b"}
	fp := &fakeProvider{state: json.RawMessage(`{"id":"a"}`)}

	resA, err := RunScan(ctx, fp, l, ScanRequest{Address: addrA, CurrentState: json.RawMessage(`{"id":"a"}`)})
	if err != nil {
		t.Fatalf("RunScan a: %v", err)
	}
	pA, err := GenerateProposal(l, "payments", resA)
	if err != nil {
		t.Fatalf("GenerateProposal a: %v", err)
	}
	if _, err := Accept(l, pA); err != nil {
		t.Fatalf("Accept a: %v", err)
	}

	// b has never been scanned -- must still read as "new", not
	// accidentally matched against a's observed_hash.
	_, found, err := l.LastObservedHash(addrB)
	if err != nil {
		t.Fatalf("LastObservedHash b: %v", err)
	}
	if found {
		t.Fatal("expected addrB to have no recorded observed_hash yet")
	}
}
