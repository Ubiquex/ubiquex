package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/ledgerstore"
)

// UBI-32: `ubx accept --from-merge` against a remote-store stack -- git
// stays the signing surface (every check above the ledger-open line is
// unchanged git/GitHub verification), the configured LedgerStore becomes
// the system of record, mirrored via the exact same CAS-guarded
// WriteProposalIfAbsent+AdvanceHead path local `ubx accept` already used
// (Arc B session 1's own store-agnostic Ledger.Append) -- no new mirroring
// mechanism, just opening the right store.
//
// A real, shared memblob bucket is used here, held onto directly and
// injected via openRemoteLedgerStore (cli/ledgeropen.go's own test seam),
// not a mem://-scheme URL -- confirmed directly that gocloud.dev/blob's
// own memblob URL opener hands back a fresh, unshared bucket per call
// regardless of host name, so two separate ubx invocations (or a
// verification read after one) would otherwise silently see two different
// empty buckets. Holding the *blob.Bucket directly and injecting it via
// the seam sidesteps that -- genuinely the same underlying storage a real
// S3 bucket would be across multiple real `ubx` invocations. All tests
// here use exactly one stack ("payments"), so the shared bucket is used
// directly, un-prefixed -- confirmed directly (not assumed) that
// blob.PrefixedBucket has consuming semantics (it marks its own *Bucket
// argument closed as a side effect and returns a new wrapper), which would
// make a held bucket unusable after the very first prefixed access; never
// prefixing at all sidesteps that entirely and is exactly correct for a
// single-stack fixture.

// remoteStoreFixture wires cfg.Ledger.Store at storeName and installs
// openRemoteLedgerStore so every openLedgerForStack call for the
// remainder of the test (regardless of how many times it's called)
// resolves to the SAME shared, real in-memory bucket -- restoring the
// original seam on cleanup.
func remoteStoreFixture(t *testing.T) (storeName string, bucket *blob.Bucket) {
	t.Helper()
	bucket = memblob.OpenBucket(nil)
	t.Cleanup(func() { bucket.Close() })

	origSeam := openRemoteLedgerStore
	openRemoteLedgerStore = func(ctx context.Context, storeURI string) (*ledgerstore.Store, func() error, error) {
		return ledgerstore.New(bucket), func() error { return nil }, nil
	}
	t.Cleanup(func() { openRemoteLedgerStore = origSeam })

	return "mem://acme-ledger", bucket
}

// remoteConfigDir writes a .ubx/config (TOML) naming storeName as the
// [ledger] store, in its own directory, and points configSearchStartDir
// at it for the duration of the test -- independent of --ledger-dir/
// --repo-dir, matching how the config cascade genuinely works (a property
// of cwd, never of --ledger-dir).
func remoteConfigDir(t *testing.T, storeName string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "[ledger]\nstore = \"" + storeName + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, ".ubx", "config"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := configSearchStartDir
	configSearchStartDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { configSearchStartDir = orig })
}

// bucketIsEmpty reports whether bucket contains no real objects at all --
// the "never touched" assertion for a git/GitHub verification failure that
// should never have reached the ledger-open step, let alone written
// anything.
func bucketIsEmpty(t *testing.T, bucket *blob.Bucket) bool {
	t.Helper()
	iter := bucket.List(nil)
	_, err := iter.Next(context.Background())
	return err != nil // io.EOF on the very first Next means genuinely empty
}

// readFromBucket opens a Ledger directly against bucket (bypassing ubx's
// own CLI surface entirely) and reads id back -- proving the accepted
// record genuinely landed in the configured store, not just trusting
// ubx's own report.
func readFromBucket(t *testing.T, bucket *blob.Bucket, id string) *core.Proposal {
	t.Helper()
	ledger := core.OpenStoreForStack(ledgerstore.New(bucket), "mem://acme-ledger", nil)
	p, err := ledger.Read(id)
	if err != nil {
		t.Fatalf("read %s from bucket: %v", id, err)
	}
	return p
}

func headFromBucket(t *testing.T, bucket *blob.Bucket) string {
	t.Helper()
	ledger := core.OpenStoreForStack(ledgerstore.New(bucket), "mem://acme-ledger", nil)
	head, err := ledger.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	return head
}

func TestAcceptFromMerge_Remote_MirrorsIntoStore(t *testing.T) {
	storeName, bucket := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	repoDir, sha := gitRepoWithProposal(t, "proposals/pending.json", draftProposalJSON)
	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}
	apiBase := fakeGitHubServer(t, "ubx-proposal: "+hash, []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
	})
	t.Setenv("UBX_GITHUB_API_BASE_URL", apiBase)

	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposals/pending.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ".", // unused for a remote store -- openLedgerForStack ignores it once [ledger].store is non-git
	)
	if err != nil {
		t.Fatalf("ubx accept --from-merge: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, hash) {
		t.Fatalf("expected the accepted id in output, got: %s", out)
	}

	// The real proof: read the accepted proposal back directly from the
	// shared bucket, not through ubx at all.
	accepted := readFromBucket(t, bucket, hash)
	if accepted.Acceptance.Method != "pr_merge" {
		t.Errorf("Method = %q, want pr_merge", accepted.Acceptance.Method)
	}
	if accepted.Acceptance.MergeSHA != sha {
		t.Errorf("MergeSHA = %q, want %q", accepted.Acceptance.MergeSHA, sha)
	}
	if bucketIsEmpty(t, bucket) {
		t.Fatal("expected the store to contain real objects after a successful mirror")
	}
}

// TestAcceptFromMerge_Remote_NoProposalFile_StoreNeverTouched is the
// remote-store counterpart to TestAcceptFromMerge_ProposalFileAbsentFromMerge:
// git/GitHub verification fails before ubx ever knows which stack's store
// to open, let alone writes to it -- the configured store must end up
// completely untouched, not just unmentioned in the error.
func TestAcceptFromMerge_Remote_NoProposalFile_StoreNeverTouched(t *testing.T) {
	storeName, bucket := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "wrong/path.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ".",
	)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "not found at commit") {
		t.Fatalf("expected a file-not-found error, got: %v", err)
	}
	if !bucketIsEmpty(t, bucket) {
		t.Fatal("the configured store must never be touched -- git/GitHub verification never even reached the proposal's own stack")
	}
}

// TestAcceptFromMerge_Remote_TamperedTrailer_StoreNeverTouched is the
// remote-store counterpart to TestAcceptFromMerge_HashMismatchRejected:
// the trailer's claimed hash doesn't match what the merged proposal file
// actually hashes to (an attempted tamper, or an authoring bug) -- caught
// by core.AcceptFromMerge's own validateAndHash step, which runs BEFORE
// core/ledger.go's Append ever touches the store. Proves that ordering
// directly: the store ends up with zero objects, not a partially-written
// one.
func TestAcceptFromMerge_Remote_TamperedTrailer_StoreNeverTouched(t *testing.T) {
	storeName, bucket := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	const wrongHash = "0000000000000000000000000000000000000000000000000000000000000000"
	apiBase := fakeGitHubServer(t, "ubx-proposal: "+wrongHash, []map[string]any{})
	t.Setenv("UBX_GITHUB_API_BASE_URL", apiBase)

	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ".",
	)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "does not hash to the trailer") {
		t.Fatalf("expected a trailer-hash-mismatch error, got: %v", err)
	}
	if !bucketIsEmpty(t, bucket) {
		t.Fatal("a tampered/mismatched proposal must never reach the store -- validateAndHash refuses before Append is ever called")
	}
}

// TestAcceptFromMerge_Remote_ReAcceptIdempotency: the exact same merge
// commit accepted twice against a remote store -- the second call
// recomputes the identical proposal (same content, same hash) from the
// same git history, and must resolve exactly like local accept's own
// idempotency contract: the object already exists and the head has
// already advanced past it, a genuine duplicate, never silently re-mirrored
// or corrupted.
func TestAcceptFromMerge_Remote_ReAcceptIdempotency(t *testing.T) {
	storeName, _ := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}
	apiBase := fakeGitHubServer(t, "ubx-proposal: "+hash, []map[string]any{})
	t.Setenv("UBX_GITHUB_API_BASE_URL", apiBase)

	args := []string{"accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ".",
	}
	if _, err := runUbx(t, nil, args...); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	_, err = runUbx(t, nil, args...)
	if err == nil {
		t.Fatal("expected the second accept of the same merge commit to be refused as a duplicate")
	}
	if !strings.Contains(err.Error(), "already") && !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected a duplicate/already-accepted error, got: %v", err)
	}
}

// TestAcceptFromMerge_Remote_CASConflict_ParentMismatch proves the
// mirror step's own CAS guard fires against a remote store exactly like
// it already does for git-local: a merge-derived proposal whose recorded
// Parent (fixed at resolve time, before this test's own first accept ever
// ran) no longer matches the store's real current head is refused, never
// silently appended on top of the wrong parent.
func TestAcceptFromMerge_Remote_CASConflict_ParentMismatch(t *testing.T) {
	storeName, bucket := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	// First, a real proposal lands against the store's genesis head --
	// advancing it away from "" (the parent every proposal in this test
	// was authored against).
	firstRepoDir, firstSHA := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	var first core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &first); err != nil {
		t.Fatal(err)
	}
	firstHash, err := core.Hash(&first)
	if err != nil {
		t.Fatal(err)
	}
	firstAPIBase := fakeGitHubServer(t, "ubx-proposal: "+firstHash, []map[string]any{})
	t.Setenv("UBX_GITHUB_API_BASE_URL", firstAPIBase)
	if _, err := runUbx(t, nil, "accept",
		"--from-merge", firstSHA,
		"--repo-dir", firstRepoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ".",
	); err != nil {
		t.Fatalf("first (head-advancing) accept: %v", err)
	}

	// A second, DIFFERENT proposal (different intent, so a different
	// hash) was resolved against the SAME genesis parent "" -- stale now
	// that the real head has moved to firstHash. Simulates a second PR,
	// opened before the first merged, reviewed and merged after it.
	const secondProposalJSON = `{
		"schema_version": 1,
		"stack": "payments",
		"parent": "",
		"kind": "change",
		"intent": {"summary": "a second, independently-authored change"},
		"delta": {"creates": [{"stack": "payments", "type": "aws_db_instance", "name": "second-db"}]},
		"resolution": {"resolved_at": "2026-07-11T00:00:00Z"},
		"cost_delta": {"monthly_usd": 10},
		"blast_radius": {"creates": 1, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`
	secondRepoDir, secondSHA := gitRepoWithProposal(t, "proposal.json", secondProposalJSON)
	var second core.Proposal
	if err := json.Unmarshal([]byte(secondProposalJSON), &second); err != nil {
		t.Fatal(err)
	}
	secondHash, err := core.Hash(&second)
	if err != nil {
		t.Fatal(err)
	}
	secondAPIBase := fakeGitHubServer(t, "ubx-proposal: "+secondHash, []map[string]any{})
	t.Setenv("UBX_GITHUB_API_BASE_URL", secondAPIBase)

	_, err = runUbx(t, nil, "accept",
		"--from-merge", secondSHA,
		"--repo-dir", secondRepoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ".",
	)
	if err == nil {
		t.Fatal("expected a parent-mismatch refusal -- this proposal's own recorded parent is stale against the store's real current head")
	}
	if !strings.Contains(err.Error(), "parent") {
		t.Fatalf("expected a parent-mismatch error, got: %v", err)
	}

	// The store's own head must still be the first (winning) proposal --
	// never corrupted or advanced by the refused second one.
	if head := headFromBucket(t, bucket); head != firstHash {
		t.Fatalf("store head = %q, want the first (winning) proposal %q -- the refused second must never have advanced it", head, firstHash)
	}
}
