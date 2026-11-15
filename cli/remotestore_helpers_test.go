package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/ledgerstore"
)

// Shared remote-store fixtures for tests that run a command against a
// stack whose ledger lives in a blob store. They lived with
// `ubx accept --from-merge`'s own tests until UBI-300 removed that path.
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
// openRemoteLedgerStore AND openRemoteStoreGC (cli/storegc.go's own,
// separate test seam -- UBI-57: gc's own real work needs the concrete
// *ledgerstore.Store core.LedgerStore's minimal interface deliberately
// never exposes, so it opens the store directly rather than through
// openLedgerForStack) so every call for the remainder of the test
// (regardless of how many times either is called) resolves to the SAME
// shared, real in-memory bucket -- restoring both original seams on
// cleanup.
func remoteStoreFixture(t *testing.T) (storeName string, bucket *blob.Bucket) {
	t.Helper()
	bucket = memblob.OpenBucket(nil)
	t.Cleanup(func() { bucket.Close() })

	opener := func(ctx context.Context, storeURI string) (*ledgerstore.Store, func() error, error) {
		return ledgerstore.New(bucket), func() error { return nil }, nil
	}

	origLedgerSeam := openRemoteLedgerStore
	openRemoteLedgerStore = opener
	t.Cleanup(func() { openRemoteLedgerStore = origLedgerSeam })

	origGCSeam := openRemoteStoreGC
	openRemoteStoreGC = opener
	t.Cleanup(func() { openRemoteStoreGC = origGCSeam })

	return "mem://acme-ledger", bucket
}

// remoteConfigDir writes a .ubx/config (TOML) naming storeName as the
// [ledger] store, in its own directory, points configSearchStartDir at
// it for the duration of the test, and returns it.
//
// For the CLI, this is independent of --ledger-dir/--repo-dir, matching
// how the config cascade genuinely works there: a property of cwd,
// never of --ledger-dir. The MCP tools derive it from their own
// ledger_dir instead (cli/mcp_config_test.go), so callers there pass
// the returned directory as ledger_dir -- a stack's root and the
// directory holding its config are the same directory in every real
// deployment, and the fixture now says so rather than leaving the two
// pointed at unrelated places.
func remoteConfigDir(t *testing.T, storeName string) string {
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
	return dir
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
