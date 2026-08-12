package resolver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/ledgerstore"
)

// UBI-57 Part 2: real semantics question answered first, with evidence,
// before any of this was written -- VerifyPins' own real code (read
// directly, core/resolver/resolver.go) only ever walks p's OWN
// resolution.inputs, never opening a neighbor's proposal to check ITS
// pins; no design doc anywhere in this codebase claims staleness should
// cascade (docs/resolver-adversarial.md's own honest framing: a
// multi-hop pin chain is named as NOT YET COVERED, "a candidate for a
// later extension," never a settled "it cascades" claim). Code and
// design AGREE on today's actual, per-pair-only behavior. Founder
// confirmed directly, in-conversation, given that finding: build
// WalkPinChain purely for VISIBILITY (ubx why/ubx addresses render the
// full chain), never change VerifyPins' own blocking semantics. These
// tests prove BOTH halves of that decision at once: the chain is walked
// correctly (multiple hops, per-hop staleness), AND an indirect
// ancestor's own staleness never blocks the proposal that only directly
// pins its immediate neighbor.
//
// seedLedgerWithPin (below) hand-constructs an adoption-shaped proposal
// carrying a REAL cross_stack_pin resolution input alongside its own
// live_state one -- deliberately, not a real Resolve() call against
// networking: core.Ledger.FoldState only ever seeds a KindChange
// create's own state from a real APPLY record (core/ubi29_test.go's own
// TestUBI29_FoldState_ShippedCreate_SeedsFromApplyRecord), never from a
// merely-accepted-but-unshipped one -- confirmed directly, not assumed,
// after a real accept-only fixture attempt genuinely failed with
// "not recorded in neighbor ledger" (this file's own real debugging).
// An adoption (seedLedger's OWN existing convention in this test
// package) folds immediately, matching what a real "networking already
// has this resource, AND it was itself pinned against security when it
// was authored" fixture needs to look like for WalkPinChain's own
// purposes -- the same "hand-built proposal, real ledger content" shape
// seedLedger already establishes, extended with one more real
// resolution input.

func seedLedgerWithPin(t *testing.T, l *core.Ledger, addr core.Address, state, pinResource, pinLedgerDir, pinHead string) {
	t.Helper()
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"state": json.RawMessage(state),
	})
	if err != nil {
		t.Fatalf("seed ledger with pin: %v", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("seed ledger with pin: head: %v", err)
	}
	hash, err := core.ObservedHash(json.RawMessage(state))
	if err != nil {
		t.Fatalf("seed ledger with pin: observed hash: %v", err)
	}
	lookup := core.DeriveLookupFromResult(json.RawMessage(state), nil)
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          core.KindAdoption,
		Intent:        core.Intent{Summary: "seed " + addr.String() + " with a real cross-stack pin"},
		Delta:         core.Delta{Creates: []json.RawMessage{node}},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: lookup},
				{Kind: "cross_stack_pin", Resource: pinResource, LedgerDir: pinLedgerDir, PinnedHead: pinHead},
			},
		},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		Status:    core.StatusDraft,
	}
	if _, err := core.Accept(l, p); err != nil {
		t.Fatalf("seed ledger with pin: accept: %v", err)
	}
}

// threeStackFixture builds the real chain payments -> networking ->
// security, shared by every test below: security holds one real
// resource; networking holds one real resource whose own proposal
// records a real cross_stack_pin to security; payments resolves a real
// $cross reference to networking, producing pA with its own real
// cross_stack_pin to networking. Returns pA and the security ledger
// handle and the networking ledger handle (so a test can advance either
// to prove which one does, and doesn't, block payments).
func threeStackFixture(t *testing.T) (pA *core.Proposal, security, networking *core.Ledger) {
	t.Helper()
	shared := memblob.OpenBucket(nil)
	t.Cleanup(func() { shared.Close() })
	registerFakeRemoteOpener(t, shared)

	securityStore := ledgerstore.New(blob.PrefixedBucket(shared, "security/"))
	security = core.OpenStore(securityStore)
	seedLedger(t, security, core.Address{Stack: "security", Type: "aws_iam_role", Name: "shared"}, `{"id":"role-abc"}`)
	securityHead, err := security.Head()
	if err != nil {
		t.Fatalf("security head: %v", err)
	}

	networkingStore := ledgerstore.New(blob.PrefixedBucket(shared, "networking/"))
	networking = core.OpenStoreForStack(networkingStore, testBase, nil)
	securityAddr := testBase + "/security/"
	seedLedgerWithPin(t, networking,
		core.Address{Stack: "networking", Type: "aws_db_instance", Name: "db"}, `{"id":"db-123"}`,
		"networking.aws_db_instance.db", securityAddr, securityHead)

	paymentsStore := ledgerstore.New(blob.PrefixedBucket(shared, "payments/"))
	payments := core.OpenStoreForStack(paymentsStore, testBase, nil)
	schema := newFakeSchema()
	paymentsIntent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"stack":"networking","to":"networking.aws_db_instance.db.id"}}}`),
	)
	pA, err = Resolve(payments, singleProvider(schema), paymentsIntent, nil)
	if err != nil {
		t.Fatalf("resolve payments: %v", err)
	}
	return pA, security, networking
}

// TestWalkPinChain_TwoHops_BothFresh proves a real, three-stack chain
// (payments -> networking -> security) walks correctly when nothing has
// moved: two hops, neither stale.
func TestWalkPinChain_TwoHops_BothFresh(t *testing.T) {
	pA, _, _ := threeStackFixture(t)

	hops, err := WalkPinChain(context.Background(), pA)
	if err != nil {
		t.Fatalf("WalkPinChain: %v", err)
	}
	if len(hops) != 2 {
		t.Fatalf("got %d hops, want 2 (payments->networking, networking->security): %+v", len(hops), hops)
	}
	for _, h := range hops {
		if h.Stale {
			t.Errorf("hop %+v is stale, want fresh -- nothing has moved", h)
		}
	}
	if hops[0].Resource == "" || hops[1].Resource == "" {
		t.Errorf("expected both hops to name a real Resource, got %+v", hops)
	}
}

// TestWalkPinChain_IndirectAncestorStale_NeverBlocksVerifyPins is the
// KEY test: security (C) advances after networking (B) was seeded
// against it, but payments (A) only ever pinned networking directly --
// the second hop (networking->security) must show Stale: true, the
// first hop (payments->networking) must NOT, and VerifyPins(pA) -- the
// real, unmodified BLOCKING check -- must still return nil. This is the
// actual "does C changing make A stale too" question, proven directly
// rather than asserted.
func TestWalkPinChain_IndirectAncestorStale_NeverBlocksVerifyPins(t *testing.T) {
	pA, security, _ := threeStackFixture(t)

	// C genuinely advances -- someone accepted a real change against
	// security AFTER networking was seeded against it. Networking (B)
	// itself is completely untouched.
	seedLedger(t, security, core.Address{Stack: "security", Type: "aws_iam_role", Name: "second"}, `{"id":"role-def"}`)

	hops, err := WalkPinChain(context.Background(), pA)
	if err != nil {
		t.Fatalf("WalkPinChain: %v", err)
	}
	if len(hops) != 2 {
		t.Fatalf("got %d hops, want 2: %+v", len(hops), hops)
	}
	if hops[0].Stale {
		t.Errorf("hop 0 (payments->networking) is stale, want fresh -- networking itself never moved: %+v", hops[0])
	}
	if !hops[1].Stale {
		t.Errorf("hop 1 (networking->security) is NOT stale, want stale -- security genuinely advanced: %+v", hops[1])
	}

	// The actual semantics finding, proven directly: an indirect
	// ancestor's own staleness never blocks the proposal that only
	// directly pinned its immediate neighbor.
	if err := VerifyPins(pA); err != nil {
		t.Fatalf("VerifyPins(pA) = %v, want nil -- payments only directly pinned networking, which itself never moved; security's own staleness must never cascade into blocking payments", err)
	}
}

// TestWalkPinChain_DirectNeighborStale_StillCaughtByVerifyPins confirms
// the OTHER half is unaffected too: when the DIRECT neighbor itself
// moves (not an indirect ancestor), VerifyPins still catches it exactly
// as it always did -- WalkPinChain's own existence changes nothing about
// that.
func TestWalkPinChain_DirectNeighborStale_StillCaughtByVerifyPins(t *testing.T) {
	pA, _, networking := threeStackFixture(t)

	// networking (the DIRECT neighbor) genuinely advances -- the
	// direct-neighbor staleness case docs/resolver-adversarial.md's own
	// row 5 already covers, reconfirmed here alongside the new
	// multi-hop tests for a complete before/after picture in one file.
	seedLedger(t, networking, core.Address{Stack: "networking", Type: "aws_db_instance", Name: "second"}, `{"id":"db-456"}`)

	if err := VerifyPins(pA); err == nil {
		t.Fatal("VerifyPins(pA) = nil, want ErrCrossStackPinStale -- networking (the DIRECT neighbor) genuinely advanced")
	}
}

// TestWalkPinChain_NoCrossStackPins_EmptyChain confirms the common,
// ordinary case (no cross-stack pin at all) costs nothing and returns an
// empty, non-error chain.
func TestWalkPinChain_NoCrossStackPins_EmptyChain(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments", ri("aws_db_instance", "db", OpCreate, `{"vpc_id":"vpc-123"}`))
	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	hops, err := WalkPinChain(context.Background(), p)
	if err != nil {
		t.Fatalf("WalkPinChain: %v", err)
	}
	if len(hops) != 0 {
		t.Fatalf("got %v, want an empty chain -- no cross-stack pin exists at all", hops)
	}
}

// TestWalkPinChain_SingleHop_MatchesDirectPin confirms the one-hop case
// (no further nesting) is exactly VerifyPins' own existing single-pin
// view, just returned as a walkable chain of length 1.
func TestWalkPinChain_SingleHop_MatchesDirectPin(t *testing.T) {
	shared := memblob.OpenBucket(nil)
	t.Cleanup(func() { shared.Close() })
	registerFakeRemoteOpener(t, shared)

	currentStore := ledgerstore.New(blob.PrefixedBucket(shared, "payments/"))
	l := core.OpenStoreForStack(currentStore, testBase, nil)

	neighborStore := ledgerstore.New(blob.PrefixedBucket(shared, "networking/"))
	neighbor := core.OpenStore(neighborStore)
	seedLedger(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "main"}, `{"id":"vpc-123"}`)

	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"stack":"networking","to":"networking.aws_vpc.main.id"}}}`),
	)
	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	hops, err := WalkPinChain(context.Background(), p)
	if err != nil {
		t.Fatalf("WalkPinChain: %v", err)
	}
	if len(hops) != 1 {
		t.Fatalf("got %d hops, want exactly 1 (networking has no cross-stack pin of its own)", len(hops))
	}
	if hops[0].Stale {
		t.Errorf("hop = %+v, want fresh", hops[0])
	}
}
