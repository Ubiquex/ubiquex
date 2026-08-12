package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/ledgerstore"
)

// UBI-57 Part 1: `ubx store gc`'s own CLI-level tests, against a real,
// shared in-memory bucket (remoteStoreFixture, this package's own
// existing remote-store test fixture, extended to also cover
// openRemoteStoreGC -- see accept_frommerge_remote_test.go).

func storeGCFixtureAppend(t *testing.T, l *core.Ledger, parent, label string) *core.Proposal {
	t.Helper()
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "payments",
		Parent:        parent,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: label},
		Resolution:    core.Resolution{ResolvedAt: "2026-08-12T00:00:00Z"},
		Status:        core.StatusDraft,
	}
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	return accepted
}

func storeGCFixtureOrphan(t *testing.T, store *ledgerstore.Store, parent, label string) *core.Proposal {
	t.Helper()
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "payments",
		Parent:        parent,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: label},
		Resolution:    core.Resolution{ResolvedAt: "2026-08-12T00:00:00Z"},
		Status:        core.StatusAccepted,
		Acceptance:    &core.Acceptance{Method: "local", Approvers: []string{"tester"}, AcceptedAt: "2026-08-12T00:00:00Z"},
	}
	hash, err := core.Hash(p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	p.ID = hash
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := store.WriteProposalIfAbsent(context.Background(), p.ID, b); err != nil {
		t.Fatalf("WriteProposalIfAbsent: %v", err)
	}
	return p
}

func TestStoreGC_DryRunByDefault_ListsButNeverDeletes(t *testing.T) {
	storeName, bucket := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	store := ledgerstore.New(bucket)
	l := core.OpenStoreForStack(store, storeName, nil)
	chained := storeGCFixtureAppend(t, l, "", "chained")
	orphan := storeGCFixtureOrphan(t, store, chained.ID, "orphan")

	out, err := runUbx(t, nil, "store", "gc", "--stack", "payments")
	if err != nil {
		t.Fatalf("store gc: %v\n%s", err, out)
	}
	if !strings.Contains(out, orphan.ID) {
		t.Fatalf("output %q does not name the real orphan %s", out, orphan.ID)
	}
	if !strings.Contains(out, "dry run") {
		t.Fatalf("output %q does not say this was a dry run", out)
	}

	// Real, independent confirmation: the orphan object is genuinely
	// still present in the bucket -- a dry run must never delete anything.
	if _, err := store.ReadProposal(context.Background(), orphan.ID); err != nil {
		t.Fatalf("ReadProposal(orphan) after a dry run = %v, want it still present", err)
	}
}

func TestStoreGC_Yes_DeletesOrphanOnly(t *testing.T) {
	storeName, bucket := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	store := ledgerstore.New(bucket)
	l := core.OpenStoreForStack(store, storeName, nil)
	chained := storeGCFixtureAppend(t, l, "", "chained")
	orphan := storeGCFixtureOrphan(t, store, chained.ID, "orphan")

	out, err := runUbx(t, nil, "store", "gc", "--stack", "payments", "--yes")
	if err != nil {
		t.Fatalf("store gc --yes: %v\n%s", err, out)
	}
	if !strings.Contains(out, orphan.ID) || !strings.Contains(out, "deleted") {
		t.Fatalf("output %q does not report the real orphan %s as deleted", out, orphan.ID)
	}

	ctx := context.Background()
	if _, err := store.ReadProposal(ctx, orphan.ID); err != core.ErrStoreObjectNotFound {
		t.Fatalf("ReadProposal(orphan) after --yes = %v, want core.ErrStoreObjectNotFound", err)
	}
	// The real, legitimately chained proposal and the head must be
	// completely unaffected.
	if _, err := store.ReadProposal(ctx, chained.ID); err != nil {
		t.Fatalf("ReadProposal(chained) after gc --yes: %v, want it still present", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != chained.ID {
		t.Fatalf("head = %q after gc --yes, want %q -- gc must never disturb the real chain", head, chained.ID)
	}
}

func TestStoreGC_NoOrphans_CleanMessage(t *testing.T) {
	storeName, bucket := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	store := ledgerstore.New(bucket)
	l := core.OpenStoreForStack(store, storeName, nil)
	storeGCFixtureAppend(t, l, "", "chained")

	out, err := runUbx(t, nil, "store", "gc", "--stack", "payments")
	if err != nil {
		t.Fatalf("store gc: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no orphaned proposals") {
		t.Fatalf("output %q does not report a clean result for a store with zero orphans", out)
	}
}

func TestStoreGC_GitLocalStore_RefusesClearly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No [ledger] table at all -- the real, common default (git-local).
	if err := os.WriteFile(filepath.Join(dir, ".ubx", "config"), []byte("stack = \"payments\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := configSearchStartDir
	configSearchStartDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { configSearchStartDir = orig })

	out, err := runUbx(t, nil, "store", "gc")
	if err == nil {
		t.Fatalf("store gc against a git-local store: want a clear refusal, got success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "git-local") || !strings.Contains(err.Error(), "git gc") {
		t.Fatalf("error %q does not clearly explain the git-local refusal and point at git gc", err)
	}
}

func TestStoreGC_MissingStack_RefusesClearly(t *testing.T) {
	storeName, _ := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	out, err := runUbx(t, nil, "store", "gc")
	if err == nil {
		t.Fatalf("store gc with no --stack against a remote store: want a clear refusal, got success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--stack") {
		t.Fatalf("error %q does not name --stack as the fix", err)
	}
}
