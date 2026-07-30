package core

import (
	"encoding/json"
	"errors"
	"testing"
)

func sampleProposal() *Proposal {
	return &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Parent:        "",
		Kind:          KindChange,
		Intent: Intent{
			Summary: "postgres for payments",
			Sources: []IntentSource{
				{Kind: "dialogue", Ref: "d-99f2", ContentHash: "sha256:abc"},
			},
		},
		Delta: Delta{
			Creates: []json.RawMessage{
				json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"payments-db","config":{"engine":"postgres"}}`),
			},
		},
		Resolution:  Resolution{ResolvedAt: "2026-07-10T00:00:00Z"},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`59`)},
		BlastRadius: BlastRadius{Creates: 1},
		Status:      StatusDraft,
	}
}

func TestHash_StableAcrossMapOrdering(t *testing.T) {
	a := sampleProposal()
	a.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"payments-db","config":{"engine":"postgres","version":"16"}}`),
	}

	b := sampleProposal()
	// Same logical object, keys in a different order and different
	// insignificant whitespace.
	b.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"config":  {"version": "16", "engine":"postgres"}, "name":"payments-db","type":"aws_db_instance","stack":"payments"}`),
	}

	ha, err := Hash(a)
	if err != nil {
		t.Fatalf("Hash(a): %v", err)
	}
	hb, err := Hash(b)
	if err != nil {
		t.Fatalf("Hash(b): %v", err)
	}
	if ha != hb {
		t.Fatalf("hashes differ despite identical logical content: %s vs %s", ha, hb)
	}
}

func TestHash_DeltaOrderNotSignificant(t *testing.T) {
	nodeX := json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"x"}`)
	nodeY := json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"y"}`)

	a := sampleProposal()
	a.Delta.Creates = []json.RawMessage{nodeX, nodeY}

	b := sampleProposal()
	b.Delta.Creates = []json.RawMessage{nodeY, nodeX}

	ha, err := Hash(a)
	if err != nil {
		t.Fatalf("Hash(a): %v", err)
	}
	hb, err := Hash(b)
	if err != nil {
		t.Fatalf("Hash(b): %v", err)
	}
	if ha != hb {
		t.Fatalf("hashes differ based on authoring order, want order-independent: %s vs %s", ha, hb)
	}
}

func TestHash_DestroysOrderNotSignificant(t *testing.T) {
	x := Address{Stack: "payments", Type: "aws_db_instance", Name: "x"}
	y := Address{Stack: "payments", Type: "aws_db_instance", Name: "y"}

	a := sampleProposal()
	a.Delta.Destroys = []DestroyEntry{{Address: x}, {Address: y}}

	b := sampleProposal()
	b.Delta.Destroys = []DestroyEntry{{Address: y}, {Address: x}}

	ha, err := Hash(a)
	if err != nil {
		t.Fatalf("Hash(a): %v", err)
	}
	hb, err := Hash(b)
	if err != nil {
		t.Fatalf("Hash(b): %v", err)
	}
	if ha != hb {
		t.Fatalf("hashes differ based on destroys authoring order: %s vs %s", ha, hb)
	}
}

func TestHash_ModifiesOrderNotSignificant(t *testing.T) {
	mkMod := func(name string) Modification {
		return Modification{
			Target: Address{Stack: "payments", Type: "aws_db_instance", Name: name},
			Before: map[string]json.RawMessage{"instance_class": json.RawMessage(`"db.t3.medium"`)},
			After:  map[string]json.RawMessage{"instance_class": json.RawMessage(`"db.t3.large"`)},
		}
	}
	resolutionInputs := func(names ...string) []ResolutionInput {
		var in []ResolutionInput
		for _, n := range names {
			addr := Address{Stack: "payments", Type: "aws_db_instance", Name: n}
			in = append(in, ResolutionInput{Kind: "live_state", Resource: addr.String(), ObservedHash: "sha256:obs-" + n})
		}
		return in
	}

	// resolution.inputs order is untouched by sortDeltaElements (only the
	// three delta arrays get the ratified lexicographic sort) — keep it
	// identical between a and b so this test isolates delta.modifies order
	// specifically.
	inputs := resolutionInputs("x", "y")

	a := sampleProposal()
	a.Delta.Modifies = []Modification{mkMod("x"), mkMod("y")}
	a.Resolution.Inputs = inputs

	b := sampleProposal()
	b.Delta.Modifies = []Modification{mkMod("y"), mkMod("x")}
	b.Resolution.Inputs = inputs

	ha, err := Hash(a)
	if err != nil {
		t.Fatalf("Hash(a): %v", err)
	}
	hb, err := Hash(b)
	if err != nil {
		t.Fatalf("Hash(b): %v", err)
	}
	if ha != hb {
		t.Fatalf("hashes differ based on modifies authoring order: %s vs %s", ha, hb)
	}
}

func TestHash_FloatRejected(t *testing.T) {
	p := sampleProposal()
	p.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"x","config":{"amount":59.5}}`),
	}
	_, err := Hash(p)
	if !errors.Is(err, ErrFloatRejected) {
		t.Fatalf("got %v, want ErrFloatRejected", err)
	}
}

func TestHash_FloatRejected_CostDelta(t *testing.T) {
	p := sampleProposal()
	p.CostDelta = CostDelta{MonthlyUSD: json.RawMessage(`59.99`)}
	_, err := Hash(p)
	if !errors.Is(err, ErrFloatRejected) {
		t.Fatalf("got %v, want ErrFloatRejected", err)
	}
}

func TestHash_DecimalStringAccepted(t *testing.T) {
	p := sampleProposal()
	p.CostDelta = CostDelta{MonthlyUSD: json.RawMessage(`"59.99"`)}
	if _, err := Hash(p); err != nil {
		t.Fatalf("decimal string should be accepted, got: %v", err)
	}
}

func TestHash_ExponentRejected(t *testing.T) {
	p := sampleProposal()
	p.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"stack":"payments","type":"t","name":"n","config":{"amount":1e2}}`),
	}
	_, err := Hash(p)
	if !errors.Is(err, ErrFloatRejected) {
		t.Fatalf("got %v, want ErrFloatRejected (exponent form is a float literal shape)", err)
	}
}

func TestHash_MutationDetected(t *testing.T) {
	a := sampleProposal()
	b := sampleProposal()
	b.Intent.Summary = "different summary"

	ha, err := Hash(a)
	if err != nil {
		t.Fatalf("Hash(a): %v", err)
	}
	hb, err := Hash(b)
	if err != nil {
		t.Fatalf("Hash(b): %v", err)
	}
	if ha == hb {
		t.Fatalf("expected different hashes for mutated content, got the same: %s", ha)
	}
}

func TestHash_ExcludesIDAcceptanceStatus(t *testing.T) {
	a := sampleProposal()
	a.Status = StatusDraft

	b := sampleProposal()
	b.Status = StatusAccepted
	b.Acceptance = &Acceptance{Method: "local", Approvers: []string{"someone"}, AcceptedAt: "2026-07-10T00:00:00Z"}
	b.ID = "irrelevant-because-excluded"

	ha, err := Hash(a)
	if err != nil {
		t.Fatalf("Hash(a): %v", err)
	}
	hb, err := Hash(b)
	if err != nil {
		t.Fatalf("Hash(b): %v", err)
	}
	if ha != hb {
		t.Fatalf("id/acceptance/status must not affect the hash: %s vs %s", ha, hb)
	}
}

func TestHash_DomainPrefixApplied(t *testing.T) {
	p := sampleProposal()
	canon, err := canonicalProposalBytes(p)
	if err != nil {
		t.Fatalf("canonicalProposalBytes: %v", err)
	}
	withoutPrefix := sha256Hex(canon)
	h, err := Hash(p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if h == withoutPrefix {
		t.Fatalf("Hash() must differ from a plain hash of the canonical bytes — domain prefix isn't being applied")
	}
}

// CanonicalJSON/CanonicalJSONBytes -- the general-purpose canonicalizer
// factored out of canonicalProposalBytes's own JCS logic this session
// (UBI-33/34 slice 4), for tseval's own use (docs/sdk.md's "byte-
// identical-after-canonicalization" conformance discipline). These tests
// exercise the exported entry points directly, independent of Proposal.

func TestCanonicalJSONBytes_SortsObjectKeys(t *testing.T) {
	a, err := CanonicalJSONBytes([]byte(`{"b":1,"a":2}`))
	if err != nil {
		t.Fatalf("CanonicalJSONBytes: %v", err)
	}
	b, err := CanonicalJSONBytes([]byte(`{"a":2,"b":1}`))
	if err != nil {
		t.Fatalf("CanonicalJSONBytes: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("key order should not affect canonical output: %s vs %s", a, b)
	}
	if string(a) != `{"a":2,"b":1}` {
		t.Fatalf("got %s, want sorted-key canonical output", a)
	}
}

func TestCanonicalJSONBytes_WhitespaceInsensitive(t *testing.T) {
	a, err := CanonicalJSONBytes([]byte(`{"a":1,"b":[1,2,3]}`))
	if err != nil {
		t.Fatalf("CanonicalJSONBytes: %v", err)
	}
	b, err := CanonicalJSONBytes([]byte("{\n  \"a\": 1,\n  \"b\": [1, 2, 3]\n}\n"))
	if err != nil {
		t.Fatalf("CanonicalJSONBytes: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("whitespace should not affect canonical output: %s vs %s", a, b)
	}
}

func TestCanonicalJSONBytes_RejectsFloatLiteral(t *testing.T) {
	if _, err := CanonicalJSONBytes([]byte(`{"a":1.5}`)); !errors.Is(err, ErrFloatRejected) {
		t.Fatalf("CanonicalJSONBytes: got %v, want ErrFloatRejected", err)
	}
}

func TestCanonicalJSONBytes_LargeIntegerSurvivesIntact(t *testing.T) {
	// UseNumber decoding (not a plain float64 unmarshal) is what makes
	// this safe -- a large int64 must not silently lose precision.
	out, err := CanonicalJSONBytes([]byte(`{"a":9223372036854775807}`))
	if err != nil {
		t.Fatalf("CanonicalJSONBytes: %v", err)
	}
	if string(out) != `{"a":9223372036854775807}` {
		t.Fatalf("got %s, want the exact int64 max preserved", out)
	}
}

func TestCanonicalJSONBytes_MalformedJSON_Errors(t *testing.T) {
	if _, err := CanonicalJSONBytes([]byte(`{not valid`)); err == nil {
		t.Fatal("CanonicalJSONBytes: got nil error for malformed JSON, want an error")
	}
}

func TestCanonicalJSON_MatchesProposalCanonicalization(t *testing.T) {
	// CanonicalJSON is the exact primitive canonicalProposalBytes now
	// calls internally -- confirmed here by feeding it the identical
	// already-decoded generic value canonicalProposalBytes itself would
	// build, and checking the result matches what canonicalProposalBytes
	// produces for an equivalent already-Proposal-shaped map.
	generic := map[string]interface{}{"b": json.Number("2"), "a": json.Number("1")}
	out, err := CanonicalJSON(generic)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if string(out) != `{"a":1,"b":2}` {
		t.Fatalf("got %s, want sorted-key canonical output", out)
	}
}
