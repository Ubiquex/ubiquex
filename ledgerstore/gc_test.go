package ledgerstore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// chainedProposal appends p (via the real l.Append path, so it's
// genuinely chained, not just written) and returns it with a real ID/
// Status/Acceptance set, matching sampleProposal's own real shape.
// label is folded into Intent.Summary so two calls sharing the same
// parent still hash to distinct, real, independent proposal IDs
// (core.Hash is content-addressed -- sampleProposal's own fixed content
// plus an identical Parent would otherwise collide).
func chainedProposal(t *testing.T, l *core.Ledger, parent, label string) *core.Proposal {
	t.Helper()
	p := sampleProposal()
	p.Parent = parent
	p.Intent.Summary = label
	hash, err := core.Hash(p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	p.ID = hash
	p.Status = core.StatusAccepted
	p.Acceptance = &core.Acceptance{Method: "local", Approvers: []string{"tester"}, AcceptedAt: "2026-07-19T00:00:00Z"}
	if err := l.Append(p); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return p
}

// orphanedProposal writes p's own object directly via WriteProposalIfAbsent,
// never advancing the head -- the exact real shape a crash between the
// two leaves behind (matching store_test.go's own
// TestStore_InterruptedAppendResumes setup, but deliberately never
// resumed here). label, see chainedProposal's own doc comment.
func orphanedProposal(t *testing.T, store *Store, parent, label string) *core.Proposal {
	t.Helper()
	ctx := context.Background()
	p := sampleProposal()
	p.Parent = parent
	p.Intent.Summary = label
	hash, err := core.Hash(p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	p.ID = hash
	p.Status = core.StatusAccepted
	p.Acceptance = &core.Acceptance{Method: "local", Approvers: []string{"tester"}, AcceptedAt: "2026-07-19T00:00:00Z"}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := store.WriteProposalIfAbsent(ctx, p.ID, b); err != nil {
		t.Fatalf("WriteProposalIfAbsent: %v", err)
	}
	return p
}

func TestOrphanedProposals_EmptyStore_NoOrphans(t *testing.T) {
	store := newTestStore(t)
	orphans, err := store.OrphanedProposals(context.Background())
	if err != nil {
		t.Fatalf("OrphanedProposals: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("got %v, want no orphans in a fresh, empty store", orphans)
	}
}

func TestOrphanedProposals_ChainOnly_NoOrphans(t *testing.T) {
	store := newTestStore(t)
	l := core.OpenStore(store)
	p1 := chainedProposal(t, l, "", "p1")
	p2 := chainedProposal(t, l, p1.ID, "p2")
	_ = chainedProposal(t, l, p2.ID, "p3")

	orphans, err := store.OrphanedProposals(context.Background())
	if err != nil {
		t.Fatalf("OrphanedProposals: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("got %v, want zero orphans -- every proposal here is genuinely chained (a real 3-hop chain, including the head itself)", orphans)
	}
}

// TestOrphanedProposals_DetectsInterruptedAppend is the ticket's own
// real crash scenario: one legitimately chained proposal, one written
// but never chained (the exact object a real crash between
// WriteProposalIfAbsent and AdvanceHead leaves behind) -- confirms the
// orphan is found, and the chained one is never mistaken for one.
func TestOrphanedProposals_DetectsInterruptedAppend(t *testing.T) {
	store := newTestStore(t)
	l := core.OpenStore(store)
	chained := chainedProposal(t, l, "", "chained")
	orphan := orphanedProposal(t, store, chained.ID, "orphan")

	orphans, err := store.OrphanedProposals(context.Background())
	if err != nil {
		t.Fatalf("OrphanedProposals: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != orphan.ID {
		t.Fatalf("got %v, want exactly [%s]", orphans, orphan.ID)
	}
}

// TestOrphanedProposals_TransitiveSafety is the ticket's own explicit
// "absolute safety property" -- a real, longer chain (5 hops) plus one
// orphan branching off an EARLY link (never the head), proving the
// reachability walk covers the WHOLE chain back to genesis, not just a
// recent tail -- nothing transitively reachable is ever misclassified.
func TestOrphanedProposals_TransitiveSafety(t *testing.T) {
	store := newTestStore(t)
	l := core.OpenStore(store)

	p1 := chainedProposal(t, l, "", "p1")
	p2 := chainedProposal(t, l, p1.ID, "p2")
	p3 := chainedProposal(t, l, p2.ID, "p3")
	p4 := chainedProposal(t, l, p3.ID, "p4")
	p5 := chainedProposal(t, l, p4.ID, "p5")

	// The orphan's own Parent points at p2 (an early, non-head link) --
	// it was never itself appended, so it doesn't actually affect p3/p4/
	// p5's own real chain at all; it's a genuinely separate, unreferenced
	// object that merely happens to name a real proposal as its Parent
	// field, the same shape a real interrupted concurrent append (racing
	// against whichever proposal actually won that slot) would leave.
	orphan := orphanedProposal(t, store, p2.ID, "orphan")

	orphans, err := store.OrphanedProposals(context.Background())
	if err != nil {
		t.Fatalf("OrphanedProposals: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != orphan.ID {
		t.Fatalf("got %v, want exactly [%s]", orphans, orphan.ID)
	}

	// Every genuinely chained id, INCLUDING early links and the head
	// itself, must never appear -- the actual safety property, checked
	// directly rather than just inferred from the count above.
	reachable := map[string]bool{p1.ID: true, p2.ID: true, p3.ID: true, p4.ID: true, p5.ID: true}
	for _, id := range orphans {
		if reachable[id] {
			t.Fatalf("OrphanedProposals flagged %s, which is genuinely chain-reachable -- this is exactly the mistake this function must never make", id)
		}
	}
}

func TestOrphanedProposals_MultipleOrphans(t *testing.T) {
	store := newTestStore(t)
	l := core.OpenStore(store)
	chained := chainedProposal(t, l, "", "chained")
	o1 := orphanedProposal(t, store, chained.ID, "o1")
	o2 := orphanedProposal(t, store, chained.ID, "o2")
	o3 := orphanedProposal(t, store, o1.ID, "o3")

	orphans, err := store.OrphanedProposals(context.Background())
	if err != nil {
		t.Fatalf("OrphanedProposals: %v", err)
	}
	want := map[string]bool{o1.ID: true, o2.ID: true, o3.ID: true}
	if len(orphans) != len(want) {
		t.Fatalf("got %v, want exactly %d orphans", orphans, len(want))
	}
	for _, id := range orphans {
		if !want[id] {
			t.Fatalf("unexpected orphan %s", id)
		}
	}
}

func TestDeleteProposal_RemovesOrphan_LeavesChainIntact(t *testing.T) {
	store := newTestStore(t)
	l := core.OpenStore(store)
	ctx := context.Background()
	chained := chainedProposal(t, l, "", "chained")
	orphan := orphanedProposal(t, store, chained.ID, "orphan")

	if err := store.DeleteProposal(ctx, orphan.ID); err != nil {
		t.Fatalf("DeleteProposal: %v", err)
	}

	orphans, err := store.OrphanedProposals(ctx)
	if err != nil {
		t.Fatalf("OrphanedProposals: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("got %v, want zero orphans after deleting the only one", orphans)
	}

	// The real, legitimately chained proposal must be completely
	// unaffected -- still readable, still resolves as head.
	if _, err := store.ReadProposal(ctx, chained.ID); err != nil {
		t.Fatalf("ReadProposal(chained): %v, want it still readable", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != chained.ID {
		t.Fatalf("head = %q, want %q -- gc must never disturb the real chain", head, chained.ID)
	}

	// The deleted orphan is genuinely gone, not just excluded from the
	// next OrphanedProposals call. Store.ReadProposal (this is the
	// ledgerstore.Store method, not core.Ledger's) reports the store's
	// own sentinel, core.ErrStoreObjectNotFound.
	if _, err := store.ReadProposal(ctx, orphan.ID); err != core.ErrStoreObjectNotFound {
		t.Fatalf("ReadProposal(orphan) after delete = %v, want core.ErrStoreObjectNotFound", err)
	}
}

func TestDeleteProposal_AlreadyGone_NoError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	if err := store.DeleteProposal(ctx, "0000000000000000000000000000000000000000000000000000000000000000"); err != nil {
		t.Fatalf("DeleteProposal(never-existed id): %v, want nil (already gone is not an error)", err)
	}
}
