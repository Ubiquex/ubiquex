package resolver

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// seedLedgerWithSources is seedLedgerWithLookup (destroys_test.go) with
// provenance on the create node, the shape blueprint.ExpandCalls has
// produced since 2026-08-05.
func seedLedgerWithSources(t *testing.T, l *core.Ledger, addr core.Address, state, lookup string, sources []core.IntentSource) {
	t.Helper()
	node := map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"state": json.RawMessage(state),
	}
	if len(sources) > 0 {
		node["sources"] = sources
	}
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("seed ledger: head: %v", err)
	}
	hash, err := core.ObservedHash(json.RawMessage(state))
	if err != nil {
		t.Fatalf("seed ledger: observed hash: %v", err)
	}
	if _, err := core.Accept(l, &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          core.KindAdoption,
		Intent:        core.Intent{Summary: "seed " + addr.String()},
		Delta:         core.Delta{Creates: []json.RawMessage{raw}},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(lookup)},
			},
		},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		Status:    core.StatusDraft,
	}); err != nil {
		t.Fatalf("seed ledger: accept: %v", err)
	}
}

// TestResolve_Destroy_RecoversProvenanceFromTheChain is UBI-284 end to end
// through the real resolver.
//
// A destroy is the one delta shape built with no intent in hand: `ubx
// resolve` is given an address string and nothing else, so unlike a create
// or a modify there is nothing to copy provenance from. It has to come back
// out of the ledger.
func TestResolve_Destroy_RecoversProvenanceFromTheChain(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "old"}
	seedLedgerWithSources(t, l, addr,
		`{"id":"vpc-999","cidr_block":"10.0.0.0/16"}`, `{"id":"vpc-999"}`,
		[]core.IntentSource{{Kind: "blueprint", Ref: "network:sha256:abc123"}})

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.old"}

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Destroys) != 1 {
		t.Fatalf("destroys = %d, want 1", len(p.Delta.Destroys))
	}
	got := p.Delta.Destroys[0].Sources
	if len(got) != 1 || got[0].Kind != "blueprint" || got[0].Ref != "network:sha256:abc123" {
		t.Fatalf("destroy provenance = %+v, want the blueprint reference recovered from the chain", got)
	}
	if err := core.Validate(p); err != nil {
		t.Fatalf("a destroy carrying provenance must still validate: %v", err)
	}
}

// TestResolve_Destroy_HandWrittenCarriesNoProvenance: nil is the ordinary
// answer for a resource no declaration claims, and must stay nil rather
// than becoming an empty array in the hashed content.
func TestResolve_Destroy_HandWrittenCarriesNoProvenance(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	addr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "old"}
	seedLedgerWithLookup(t, l, addr, `{"id":"vpc-999","cidr_block":"10.0.0.0/16"}`, `{"id":"vpc-999"}`)

	intent := intentFile("payments")
	intent.Destroys = []string{"payments.aws_vpc.old"}

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := p.Delta.Destroys[0].Sources; len(got) != 0 {
		t.Fatalf("destroy provenance = %+v, want none", got)
	}
	// The property that keeps a pre-UBI-284 ledger verifying: absent, not
	// present-and-empty, in the bytes the hash is taken over.
	stored, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back core.Proposal
	if err := json.Unmarshal(stored, &back); err != nil {
		t.Fatal(err)
	}
	beforeHash, err := core.Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	afterHash, err := core.Hash(&back)
	if err != nil {
		t.Fatal(err)
	}
	if beforeHash != afterHash {
		t.Fatalf("a destroy with no provenance did not round trip: %s vs %s", beforeHash, afterHash)
	}
}
