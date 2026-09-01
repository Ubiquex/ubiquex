package core

import (
	"encoding/json"
	"testing"
)

// TestChainFrom_MatchesChainAtCurrentHead proves ChainFrom(Head()) and
// Chain() are the same computation by construction (UBI-227): Chain() is
// now nothing but "resolve Head(), then call ChainFrom" (core/state.go),
// so this pins that relationship as a real, checked contract rather than
// an implementation detail nobody verifies.
func TestChainFrom_MatchesChainAtCurrentHead(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	shipChangeCreateForTest(t, l, addr, json.RawMessage(`{"id":"a1","name":"ubx-states"}`), true, true)

	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	fromChain, err := l.Chain()
	if err != nil {
		t.Fatalf("Chain: %v", err)
	}
	fromChainFrom, err := l.ChainFrom(head)
	if err != nil {
		t.Fatalf("ChainFrom(Head()): %v", err)
	}
	if len(fromChain) != len(fromChainFrom) {
		t.Fatalf("Chain() has %d proposals, ChainFrom(Head()) has %d", len(fromChain), len(fromChainFrom))
	}
	for i := range fromChain {
		if fromChain[i].ID != fromChainFrom[i].ID {
			t.Fatalf("proposal %d differs: Chain()=%s ChainFrom(Head())=%s", i, fromChain[i].ID, fromChainFrom[i].ID)
		}
	}
}

// TestChainFrom_StopsAtTheGivenHead proves ChainFrom walks only up to and
// including headID, never past it -- a restore targeting an earlier head
// must never see proposals that came after it.
func TestChainFrom_StopsAtTheGivenHead(t *testing.T) {
	l := Open(t.TempDir())
	addrA := testAddr()
	addrB := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "ubx-logs"}

	p1 := shipChangeCreateForTest(t, l, addrA, json.RawMessage(`{"id":"a1","name":"ubx-states"}`), true, true)
	shipChangeCreateForTest(t, l, addrB, json.RawMessage(`{"id":"b1","name":"ubx-logs"}`), true, true)

	chain, err := l.ChainFrom(p1.ID)
	if err != nil {
		t.Fatalf("ChainFrom(p1): %v", err)
	}
	if len(chain) != 1 {
		t.Fatalf("ChainFrom(p1) returned %d proposals, want exactly 1 (up to and including p1, never the later create)", len(chain))
	}
	if chain[0].ID != p1.ID {
		t.Fatalf("ChainFrom(p1)[0].ID = %s, want %s", chain[0].ID, p1.ID)
	}
}

// TestFoldStateAt_ReconstructsAnEarlierAttributeValue proves FoldStateAt
// reads a resource's own config as it existed at an earlier head, not the
// value a later modify changed it to -- the exact capability restore's
// own target-shape reconstruction depends on.
func TestFoldStateAt_ReconstructsAnEarlierAttributeValue(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	shipChangeCreateForTest(t, l, addr, json.RawMessage(`{"id":"a1","name":"ubx-states","retention_days":1}`), true, true)
	afterCreateHead, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}

	shipChangeModifyForTest(t, l, addr,
		map[string]json.RawMessage{"retention_days": json.RawMessage(`1`)},
		map[string]json.RawMessage{"retention_days": json.RawMessage(`30`)},
		true, true)

	// Current truth: 30.
	current, found, err := l.FoldState(addr)
	if err != nil {
		t.Fatalf("FoldState: %v", err)
	}
	if !found {
		t.Fatal("FoldState: not found")
	}
	var currentDecoded map[string]interface{}
	if err := json.Unmarshal(current, &currentDecoded); err != nil {
		t.Fatal(err)
	}
	if currentDecoded["retention_days"] != float64(30) {
		t.Fatalf("current retention_days = %v, want 30", currentDecoded["retention_days"])
	}

	// As of the head right after creation, before the modify: 1.
	historical, found, err := l.FoldStateAt(afterCreateHead, addr)
	if err != nil {
		t.Fatalf("FoldStateAt: %v", err)
	}
	if !found {
		t.Fatal("FoldStateAt: not found")
	}
	var historicalDecoded map[string]interface{}
	if err := json.Unmarshal(historical, &historicalDecoded); err != nil {
		t.Fatal(err)
	}
	if historicalDecoded["retention_days"] != float64(1) {
		t.Fatalf("historical retention_days = %v, want 1", historicalDecoded["retention_days"])
	}
}

// TestAddressesAt_ReflectsTheStackShapeAtAnEarlierHead is the whole-stack
// version of the test above: an address created after the target head is
// absent from AddressesAt's own result, and one destroyed after the
// target head is still present -- exactly the two facts restore's own
// create/destroy computation reads off this function.
func TestAddressesAt_ReflectsTheStackShapeAtAnEarlierHead(t *testing.T) {
	l := Open(t.TempDir())
	addrA := testAddr()
	addrB := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "ubx-logs"}

	shipChangeCreateForTest(t, l, addrA, json.RawMessage(`{"id":"a1","name":"ubx-states"}`), true, true)
	targetHead, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	// After the target head: B is created, then A is destroyed.
	shipChangeCreateForTest(t, l, addrB, json.RawMessage(`{"id":"b1","name":"ubx-logs"}`), true, true)
	shipChangeDestroyForTest(t, l, addrA, json.RawMessage(`{"id":"a1","name":"ubx-states"}`), "applied", true, true)

	entries, err := l.AddressesAt(targetHead, "payments", false)
	if err != nil {
		t.Fatalf("AddressesAt: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("AddressesAt(targetHead) returned %d addresses, want exactly 1 (A only): %+v", len(entries), entries)
	}
	if entries[0].Address != addrA {
		t.Fatalf("AddressesAt(targetHead)[0].Address = %s, want %s", entries[0].Address, addrA)
	}

	// Current shape: A is gone (destroyed), B exists -- the opposite of
	// the target head's own shape, proving AddressesAt and Addresses
	// genuinely disagree when the stack has actually changed.
	current, err := l.Addresses("payments", false)
	if err != nil {
		t.Fatalf("Addresses: %v", err)
	}
	if len(current) != 1 {
		t.Fatalf("Addresses() (current) returned %d addresses, want exactly 1 (B only): %+v", len(current), current)
	}
	if current[0].Address != addrB {
		t.Fatalf("Addresses()[0].Address = %s, want %s", current[0].Address, addrB)
	}
}
