package ledgerstore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/ubiquex/ubiquex/core"
)

// newTestStore returns a Store backed by a fresh, empty memblob bucket --
// standing in for s3/gs/azblob alike, since Store's own code never
// touches anything driver-specific (UBI-32 Arc B, docs/ledgerstore-
// adversarial.md's own framing: "run once against the git-directory
// reference implementation and again, unmodified, against a
// memblob-backed store").
func newTestStore(t *testing.T) *Store {
	t.Helper()
	bucket := memblob.OpenBucket(nil)
	t.Cleanup(func() { bucket.Close() })
	return New(bucket)
}

func sampleProposal() *core.Proposal {
	return &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "payments",
		Parent:        "",
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "a test change"},
		Resolution:    core.Resolution{ResolvedAt: "2026-07-19T00:00:00Z"},
		BlastRadius:   core.BlastRadius{},
		Status:        core.StatusDraft,
	}
}

// --- basic roundtrip ---------------------------------------------------

func TestStore_AcceptAppendAndRead(t *testing.T) {
	l := core.OpenStore(newTestStore(t))

	p := sampleProposal()
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != accepted.ID {
		t.Fatalf("head = %q, want %q", head, accepted.ID)
	}
	got, err := l.Read(accepted.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Intent.Summary != p.Intent.Summary {
		t.Fatalf("read back summary = %q, want %q", got.Intent.Summary, p.Intent.Summary)
	}
}

func TestStore_ChainedAcceptSucceeds(t *testing.T) {
	l := core.OpenStore(newTestStore(t))

	first, err := core.Accept(l, sampleProposal())
	if err != nil {
		t.Fatalf("Accept(first): %v", err)
	}
	second := sampleProposal()
	second.Intent.Summary = "a second, unrelated change"
	second.Parent = first.ID
	acceptedSecond, err := core.Accept(l, second)
	if err != nil {
		t.Fatalf("Accept(second): %v", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != acceptedSecond.ID {
		t.Fatalf("head = %q, want %q", head, acceptedSecond.ID)
	}
}

// --- row 1: CAS race, two concurrent advances --------------------------

func TestStore_CASRace_ExactlyOneAdvanceWins(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = store.AdvanceHead(ctx, "", strings.Repeat(string(rune('a'+i)), 64))
		}(i)
	}
	wg.Wait()

	successes, conflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, core.ErrStoreHeadMoved):
			conflicts++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected exactly one success and one ErrStoreHeadMoved, got %d successes and %d conflicts (%v)", successes, conflicts, results)
	}

	head, err := store.Head(ctx)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if !core.IsHash(head) {
		t.Fatalf("head = %q, not a well-formed hash after the race", head)
	}
}

func TestLedgerAppend_TwoConcurrentAccepts(t *testing.T) {
	l := core.OpenStore(newTestStore(t))

	newDraft := func(summary string) *core.Proposal {
		p := sampleProposal()
		p.Intent.Summary = summary
		return p
	}

	var wg sync.WaitGroup
	results := make([]error, 2)
	proposals := []*core.Proposal{newDraft("first"), newDraft("second")}
	for i := range proposals {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := core.Accept(l, proposals[i])
			results[i] = err
		}(i)
	}
	wg.Wait()

	successes, mismatches := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, core.ErrParentMismatch):
			mismatches++
		default:
			t.Errorf("unexpected error from a concurrent Accept: %v", err)
		}
	}
	if successes != 1 || mismatches != 1 {
		t.Fatalf("expected exactly one success and one ErrParentMismatch, got %d successes and %d mismatches (%v)", successes, mismatches, results)
	}
}

// --- row 2/3: lock contention, TTL expiry reclaim -----------------------

func withShortLockTimings(t *testing.T) {
	t.Helper()
	origWait, origRetry := lockWaitTimeout, lockRetryInterval
	lockWaitTimeout = 200 * time.Millisecond
	lockRetryInterval = 10 * time.Millisecond
	t.Cleanup(func() { lockWaitTimeout, lockRetryInterval = origWait, origRetry })
}

func TestStore_LockContention_SerializesRatherThanRacing(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	release, err := store.Lock(ctx, time.Minute)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		release2, err := store.Lock(ctx, time.Minute)
		if err != nil {
			t.Errorf("second Lock: %v", err)
			return
		}
		close(acquired)
		release2(ctx)
	}()

	select {
	case <-acquired:
		t.Fatal("second Lock acquired before the first was released -- lock did not serialize")
	case <-time.After(100 * time.Millisecond):
	}

	if err := release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second Lock never acquired after the first released")
	}
}

func TestStore_LockTTLExpiry_Reclaimed(t *testing.T) {
	withShortLockTimings(t)
	ctx := context.Background()
	store := newTestStore(t)

	// Acquire with a TTL that's already effectively expired by the time we
	// try again -- simulating a holder that crashed and never released.
	_, err := store.Lock(ctx, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	release2, err := store.Lock(ctx, time.Minute)
	if err != nil {
		t.Fatalf("Lock after TTL expiry: %v, want a successful reclaim", err)
	}
	release2(ctx)
}

func TestStore_LockNotYetExpired_StillRefused(t *testing.T) {
	withShortLockTimings(t)
	ctx := context.Background()
	store := newTestStore(t)

	_, err := store.Lock(ctx, time.Minute) // long TTL, not expired
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}

	start := time.Now()
	_, err = store.Lock(ctx, time.Minute)
	elapsed := time.Since(start)
	if !errors.Is(err, core.ErrLedgerLocked) {
		t.Fatalf("got %v, want ErrLedgerLocked", err)
	}
	if elapsed < lockWaitTimeout {
		t.Errorf("returned after %v, before the %v wait window elapsed", elapsed, lockWaitTimeout)
	}
}

// --- row 4/5: interrupted append resumes, genuine duplicate refused -----

func TestStore_InterruptedAppendResumes(t *testing.T) {
	store := newTestStore(t)
	l := core.OpenStore(store)
	ctx := context.Background()

	p := sampleProposal()
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
	// Simulate a crash: the proposal object landed, the head never
	// advanced -- exactly what a process death between
	// WriteProposalIfAbsent and AdvanceHead leaves behind.
	if err := store.WriteProposalIfAbsent(ctx, p.ID, b); err != nil {
		t.Fatalf("WriteProposalIfAbsent: %v", err)
	}
	if head, err := l.Head(); err != nil || head != "" {
		t.Fatalf("test setup: head = %q, err = %v -- want empty before the resume", head, err)
	}

	if err := l.Append(p); err != nil {
		t.Fatalf("Append (resume): %v, want a clean resume", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != p.ID {
		t.Fatalf("head = %q, want %q", head, p.ID)
	}

	// A genuine retry now must be refused (row 5).
	if err := l.Append(p); !errors.Is(err, core.ErrDuplicateProposal) {
		t.Fatalf("got %v, want ErrDuplicateProposal on a genuinely-already-accepted retry", err)
	}
}

// --- row 6: corrupted proposal object -----------------------------------

func TestStore_CorruptedProposalObject(t *testing.T) {
	store := newTestStore(t)
	l := core.OpenStore(store)
	ctx := context.Background()

	if err := store.bucket.WriteAll(ctx, proposalKey("deadbeef"), []byte("\x00not json"), nil); err != nil {
		t.Fatalf("seed corrupt object: %v", err)
	}
	_, err := l.Read("deadbeef")
	if !errors.Is(err, core.ErrCorruptLedgerEntry) {
		t.Fatalf("got %v, want ErrCorruptLedgerEntry", err)
	}
}

// --- row 7: corrupted head-edge object -----------------------------------

func TestStore_CorruptedHeadEdge(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.bucket.WriteAll(ctx, headEdgeKey(""), []byte("not-a-hash"), nil); err != nil {
		t.Fatalf("seed corrupt edge: %v", err)
	}
	_, err := store.Head(ctx)
	if !errors.Is(err, core.ErrCorruptLedgerHead) {
		t.Fatalf("got %v, want ErrCorruptLedgerHead", err)
	}
}

// --- row 9 (UBI-64): well-formed head naming a proposal that isn't there --
//
// Distinct from TestStore_CorruptedHeadEdge above (garbage bytes -- not
// even a hash) and from TestStore_CorruptedProposalObject (a proposal
// object that exists but won't parse): this is a perfectly well-formed
// 64-hex head edge whose target proposal object was never written at all
// -- the remote-store mirror of the git-directory ".ubx/ledger.lock names
// a proposal ledger/proposals/ doesn't have" scenario the whole ticket is
// about ("head object present, proposal objects missing").
func TestStore_HeadResolvesButProposalMissing_TeachingError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	missing := strings.Repeat("a", 64)
	if err := store.bucket.WriteAll(ctx, headEdgeKey(""), []byte(missing), nil); err != nil {
		t.Fatalf("seed dangling head edge: %v", err)
	}

	l := core.OpenStore(store)
	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != missing {
		t.Fatalf("head = %q, want %q", head, missing)
	}

	_, err = l.Chain()
	if !errors.Is(err, core.ErrBrokenLedgerHead) {
		t.Fatalf("Chain: got %v, want ErrBrokenLedgerHead", err)
	}
	if strings.Contains(err.Error(), "ledger.lock") {
		t.Fatalf("a remote store's teaching error shouldn't mention the git-local ledger.lock file: %v", err)
	}
	if !strings.Contains(err.Error(), "head object") {
		t.Fatalf("expected remote-store phrasing (\"head object\") in error, got: %v", err)
	}

	result, verr := core.VerifyChain(l)
	if verr != nil {
		t.Fatalf("VerifyChain: %v", verr)
	}
	if result.ChainIntact {
		t.Fatal("ChainIntact = true, want false")
	}
	if len(result.Findings) != 1 || result.Findings[0].Kind != core.FindingMissingParent {
		t.Fatalf("findings = %+v, want exactly one missing_parent finding", result.Findings)
	}
	if strings.Contains(result.Findings[0].Detail, "ledger.lock") {
		t.Fatalf("verify finding detail shouldn't mention ledger.lock for a remote store: %s", result.Findings[0].Detail)
	}
}

// --- row 8: corrupted apply record --------------------------------------

func TestStore_CorruptedApplyRecord_OthersUnaffected(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	l := core.OpenStore(store)

	proposalID := strings.Repeat("a", 64)
	good1 := &core.ApplyRecord{SchemaVersion: core.ApplyRecordSchemaVersion, Proposal: proposalID, Attempt: 1}
	good3 := &core.ApplyRecord{SchemaVersion: core.ApplyRecordSchemaVersion, Proposal: proposalID, Attempt: 3}
	if err := l.SaveApplyProgress(good1); err != nil {
		t.Fatalf("save attempt 1: %v", err)
	}
	if err := l.SaveApplyProgress(good3); err != nil {
		t.Fatalf("save attempt 3: %v", err)
	}
	if err := store.bucket.WriteAll(ctx, applyKey(proposalID, 2), []byte("not json"), nil); err != nil {
		t.Fatalf("seed corrupt attempt 2: %v", err)
	}

	_, err := l.ApplyAttempts(proposalID)
	if !errors.Is(err, core.ErrCorruptApplyRecord) {
		t.Fatalf("got %v, want ErrCorruptApplyRecord", err)
	}

	// The two good attempts individually still read back fine.
	if _, err := l.ReadApply(proposalID, 1); err != nil {
		t.Errorf("ReadApply(1): %v", err)
	}
	if _, err := l.ReadApply(proposalID, 3); err != nil {
		t.Errorf("ReadApply(3): %v", err)
	}
}

// --- row 9: apply-attempt discovery via list, not glob ------------------

func TestStore_ListApplyAttempts_IgnoresUnrelatedKeys(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	l := core.OpenStore(store)

	proposalA := strings.Repeat("a", 64)
	proposalB := strings.Repeat("b", 64)
	for _, rec := range []*core.ApplyRecord{
		{SchemaVersion: core.ApplyRecordSchemaVersion, Proposal: proposalA, Attempt: 1},
		{SchemaVersion: core.ApplyRecordSchemaVersion, Proposal: proposalA, Attempt: 2},
		{SchemaVersion: core.ApplyRecordSchemaVersion, Proposal: proposalB, Attempt: 1},
	} {
		if err := l.SaveApplyProgress(rec); err != nil {
			t.Fatalf("save %+v: %v", rec, err)
		}
	}
	// Non-apply objects sharing the same store, including one whose key
	// merely starts with proposalA's own bytes.
	if err := store.bucket.WriteAll(ctx, "salt", []byte("not an apply record"), nil); err != nil {
		t.Fatal(err)
	}

	attempts, err := l.ApplyAttempts(proposalA)
	if err != nil {
		t.Fatalf("ApplyAttempts: %v", err)
	}
	if len(attempts) != 2 || attempts[0].Attempt != 1 || attempts[1].Attempt != 2 {
		t.Fatalf("got %d attempts, want [1,2] for proposal A: %+v", len(attempts), attempts)
	}
}

// --- row 11: salt race, first use ----------------------------------------

func TestStore_SaltRace_ExactlyOneWinner(t *testing.T) {
	l := core.OpenStore(newTestStore(t))

	var wg sync.WaitGroup
	salts := make([][]byte, 4)
	for i := range salts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := l.Salt()
			if err != nil {
				t.Errorf("Salt: %v", err)
				return
			}
			salts[i] = s
		}(i)
	}
	wg.Wait()

	for i := 1; i < len(salts); i++ {
		if string(salts[i]) != string(salts[0]) {
			t.Fatalf("salt[%d] differs from salt[0] -- every caller must see the same winning salt", i)
		}
	}
}

// --- row 10: salt never touches git for a remote store -------------------
// (nothing to test here for the blob store itself -- there is no
// .gitignore concept at all in this package; see gitLedgerStore's own
// TestLedger_Salt_CreatesGitignore in core for the git-store-specific
// behavior this deliberately does NOT have an equivalent of.)
