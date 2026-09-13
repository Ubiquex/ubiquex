package pyeval

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// wasi_checksum_test.go covers UBI-255's content-verification half.
//
// Acquisition used to verify SHAPE and not content: verifyPythonWasiDir
// checked that python.wasm and lib/ exist, so a truncated, corrupted or
// substituted archive containing those two entries passed. pyeval was
// the one ubx-side acquisition path with no content check at all.
//
// It became more load-bearing when CI started caching the extracted
// tree, since an unverified extract would otherwise be reused on every
// run under a key that keeps matching until the pin changes.

// The substituted-archive case: a well-formed zip of the right shape
// that is simply not the archive this ubx pins.
func TestDownload_RefusesAnArchiveThatIsNotThePinnedOne(t *testing.T) {
	fastRetries(t)
	body := zipWithInterpreter(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "python-wasi")
	err := downloadAndExtractWithRetry(context.Background(), srv.URL, dest, sha256Hex([]byte("a different archive entirely")))
	if err == nil {
		t.Fatal("want a refusal: the archive is well-formed and passes every shape check, which is exactly why a shape check was not enough")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("the refusal has to name what went wrong, got: %v", err)
	}
	// And nothing was left behind for a later run to trust.
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("a refused archive was extracted anyway, so the next run would find a cached tree that was never verified")
	}
}

// A digest mismatch is not transient. The same bytes mismatch every
// time, and the two things it can mean, a wrong pin or an archive that
// is not what it claims, are both answered by stopping.
func TestDownload_DoesNotRetryAChecksumMismatch(t *testing.T) {
	fastRetries(t)
	body := zipWithInterpreter(t)

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Write(body)
	}))
	defer srv.Close()

	_ = downloadAndExtractWithRetry(context.Background(), srv.URL, filepath.Join(t.TempDir(), "d"), sha256Hex([]byte("nope")))
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1: retrying a checksum mismatch only delays the same answer", got)
	}
}

// A truncated download is the case a shape check is least able to
// catch, since a truncated zip can still contain the two entries the
// shape check looks for.
func TestDownload_RefusesATruncatedArchive(t *testing.T) {
	fastRetries(t)
	full := zipWithInterpreter(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(full[:len(full)/2])
	}))
	defer srv.Close()

	err := downloadAndExtractWithRetry(context.Background(), srv.URL, filepath.Join(t.TempDir(), "d"), sha256Hex(full))
	if err == nil {
		t.Fatal("want a refusal for a half-delivered archive")
	}
}

// A cache hit is only trusted when the tree records the digest of the
// archive that produced it. Without this, an extract produced before
// checksums existed, or populated by hand, is reused forever.
func TestCachedDigest_OnlyTrustsATreeProducedByThePinnedArchive(t *testing.T) {
	dir := t.TempDir()

	if cachedDigestMatches(dir) {
		t.Error("an unmarked tree must not be trusted: it predates this check or was populated by hand")
	}

	if err := os.WriteFile(filepath.Join(dir, digestMarkerName), []byte("some other digest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if cachedDigestMatches(dir) {
		t.Error("a tree left over from a different pin must not be trusted")
	}

	if err := recordCachedDigest(dir); err != nil {
		t.Fatal(err)
	}
	if !cachedDigestMatches(dir) {
		t.Error("a tree this ubx just extracted and verified has to be trusted, or every run re-downloads")
	}
}

// The marker is compared after trimming, so a trailing newline written
// by one version and not another does not invalidate a good cache.
func TestCachedDigest_TolerantOfSurroundingWhitespace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, digestMarkerName), []byte("  "+pythonWasiSHA256+"\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !cachedDigestMatches(dir) {
		t.Error("whitespace around a correct digest should not force a re-download")
	}
}

// The pinned digest has to be a real sha256, since a malformed constant
// would refuse every archive and the failure would look like an
// upstream problem.
func TestPinnedDigest_IsWellFormed(t *testing.T) {
	if len(pythonWasiSHA256) != 64 {
		t.Fatalf("pinned digest is %d characters, want 64", len(pythonWasiSHA256))
	}
	if strings.ToLower(pythonWasiSHA256) != pythonWasiSHA256 {
		t.Error("pinned digest should be lowercase hex, which is what hex.EncodeToString produces")
	}
	if _, err := hexDecodeCheck(pythonWasiSHA256); err != nil {
		t.Errorf("pinned digest is not hex: %v", err)
	}
}

func hexDecodeCheck(s string) ([]byte, error) {
	var buf bytes.Buffer
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return nil, errNotHex
		}
		buf.WriteRune(r)
	}
	return buf.Bytes(), nil
}

var errNotHex = &notHexError{}

type notHexError struct{}

func (e *notHexError) Error() string { return "not hexadecimal" }
