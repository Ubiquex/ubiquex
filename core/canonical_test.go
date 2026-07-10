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
		Resolution: Resolution{ResolvedAt: "2026-07-10T00:00:00Z"},
		CostDelta:  CostDelta{MonthlyUSD: json.RawMessage(`59`)},
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
	a.Delta.Destroys = []Address{x, y}

	b := sampleProposal()
	b.Delta.Destroys = []Address{y, x}

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
