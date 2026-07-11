package core

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDiffAttributes_NestedDotPath(t *testing.T) {
	before := json.RawMessage(`{"tags":{"env":"prod","owner":"payments"},"count":3}`)
	after := json.RawMessage(`{"tags":{"env":"staging","owner":"payments"},"count":3}`)

	b, a, err := diffAttributes(before, after)
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

	b, a, err := diffAttributes(before, after)
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

	b, a, err := diffAttributes(before, after)
	if err != nil {
		t.Fatalf("diffAttributes: %v", err)
	}
	if len(b) != 1 || len(a) != 1 {
		t.Fatalf("expected exactly one changed key each, got before=%v after=%v", b, a)
	}
}

func TestDiffAttributes_NoChangesProducesEmptyDiff(t *testing.T) {
	same := json.RawMessage(`{"a":1,"nested":{"b":2}}`)
	b, a, err := diffAttributes(same, same)
	if err != nil {
		t.Fatalf("diffAttributes: %v", err)
	}
	if len(b) != 0 || len(a) != 0 {
		t.Fatalf("expected empty diffs, got before=%v after=%v", b, a)
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
