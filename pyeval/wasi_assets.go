package pyeval

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pythonWasiVersion/pythonWasiSDK/pythonWasiURL pin the exact,
// empirically-verified CPython-WASI build this session's own probe used
// (docs/sdk.md's own "The Python evaluator: decided empirically") --
// a real, current, community-maintained release
// (github.com/brettcannon/cpython-wasi-build, a real CPython core
// developer's own channel), version-matched to this session's own
// installed CPython. Bumping this is a real, deliberate future decision
// (mirroring a provider version bump in .ubx/config), not something this
// package guesses at dynamically -- an evaluator must never silently run
// against a different interpreter build than the one its own hermeticity
// claims were verified against.
const (
	pythonWasiVersion = "3.14.6"
	pythonWasiSDK     = "24"
	pythonWasiURL     = "https://github.com/brettcannon/cpython-wasi-build/releases/download/v" + pythonWasiVersion + "/python-" + pythonWasiVersion + "-wasi_sdk-" + pythonWasiSDK + ".zip"

	// pythonWasiSHA256 is the archive's own real digest, pinned here
	// beside the version and URL it belongs to (UBI-255).
	//
	// Before this, acquisition verified SHAPE and not content:
	// verifyPythonWasiDir checked that python.wasm and lib/ exist, so a
	// truncated, corrupted or substituted archive containing those two
	// entries passed. That made pyeval the one ubx-side acquisition path
	// with no content check at all. The three ubx-published paths
	// (schema snapshots, the dynamic provider, descriptions) already
	// verify a real SHA256, so this copies a working pattern rather
	// than inventing one.
	//
	// It became more load-bearing, not less, when CI started caching the
	// extracted tree: a corrupt extract would otherwise persist across
	// every run under a key that keeps matching until the pin changes,
	// and nothing would ever notice.
	//
	// Taken from the release's own published digest and confirmed
	// against the bytes actually served. Bumping pythonWasiVersion means
	// bumping this too, and the acquisition fails loudly if they
	// disagree, which is the point: a version bump that forgets the
	// digest stops rather than silently trusting whatever arrives.
	//
	// What this does NOT establish is publisher authenticity. It catches
	// corruption, truncation and a substituted archive; it does not
	// prove who published the release, since anyone with push access to
	// that repository can publish a matching pair. That is the same
	// bounded position provider/acquireschema.go already states for
	// ubx's own artifacts, and the same trust boundary: whoever has push
	// access to the upstream repository.
	pythonWasiSHA256 = "73bf2e9774c4d8820d0877ec5db0b963df3a9611fc2a63838aeaee29dfd034e6"
)

// pythonWasiDirEnv, if set, points at a local directory already holding
// python.wasm + lib/ -- skips acquisition entirely, the same "hand-
// populate it yourself, point an env var at it" escape hatch
// UBX_PROVIDER_MIRROR already gives provider binaries.
const pythonWasiDirEnv = "UBX_PYTHON_WASI_DIR"

// acquirePythonWasi returns a local directory containing python.wasm and
// its own lib/ stdlib tree, downloading and caching it once (under
// ~/.ubx/python-wasi/<version>/, the same cache-root convention
// provider.Acquire already uses for provider binaries) if not already
// present. Not embedded into the ubx binary itself (~42MB, unlike the
// tiny ubx_sdk runtime source embed.go carries) -- growing every ubx
// install by 42MB regardless of whether its own user ever touches Python
// SDK programs would be a real, avoidable cost; acquire-once-cache-after
// is the same trade this project already made for provider binaries.
func acquirePythonWasi(ctx context.Context) (string, error) {
	if dir := os.Getenv(pythonWasiDirEnv); dir != "" {
		if err := verifyPythonWasiDir(dir); err != nil {
			return "", fmt.Errorf("%s=%s: %w", pythonWasiDirEnv, dir, err)
		}
		return dir, nil
	}

	cacheRoot, err := defaultCacheRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cacheRoot, pythonWasiVersion)
	if verifyPythonWasiDir(dir) == nil && cachedDigestMatches(dir) {
		return dir, nil
	}

	if err := downloadAndExtractWithRetry(ctx, pythonWasiURL, dir, pythonWasiSHA256); err != nil {
		return "", fmt.Errorf("acquire CPython-WASI build: %w", err)
	}
	if err := verifyPythonWasiDir(dir); err != nil {
		return "", fmt.Errorf("acquired CPython-WASI build looks wrong: %w", err)
	}
	if err := recordCachedDigest(dir); err != nil {
		return "", fmt.Errorf("acquire CPython-WASI build: %w", err)
	}
	return dir, nil
}

// digestMarkerName is written into an extracted tree to record WHICH
// archive produced it.
const digestMarkerName = ".ubx-archive-sha256"

// cachedDigestMatches reports whether this extracted tree was produced
// by the archive this ubx pins (UBI-255).
//
// A cache hit used to be trusted on shape alone, which was the weakest
// link once CI began caching the extracted tree: an extract produced
// from an unverified archive would be reused on every run under a key
// that keeps matching, and nothing would ever look again.
//
// Be precise about what this does and does not catch. It catches a tree
// extracted before checksums existed, one populated by hand, and one
// left over from a different pin. It does NOT detect a tree corrupted
// or edited AFTER extraction, because the marker records the archive's
// digest rather than the tree's. Hashing 42MB of stdlib on every
// evaluation to close that last case is not worth its cost, and the
// case it would catch is local tampering with a cache directory, which
// is not the threat this pin is for.
//
// A missing marker means "produced before this check existed", and is
// treated as a miss so the tree is re-acquired once and marked. That
// costs one download per machine, not one per run.
func cachedDigestMatches(dir string) bool {
	recorded, err := os.ReadFile(filepath.Join(dir, digestMarkerName))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(recorded)) == pythonWasiSHA256
}

// recordCachedDigest marks a freshly extracted tree with the digest of
// the archive it came from, which downloadAndExtract has already
// verified by that point.
func recordCachedDigest(dir string) error {
	return os.WriteFile(filepath.Join(dir, digestMarkerName), []byte(pythonWasiSHA256+"\n"), 0o644)
}

// PrefetchInterpreter downloads and caches the pinned CPython-WASI build
// without evaluating anything, and returns the directory holding it.
//
// It exists so CI can acquire this asset in a NAMED setup step, beside
// bubblewrap and wasmtime, rather than having the first Python test that
// happens to run pull 42MB from a third party mid-suite (UBI-255).
//
// On 2026-09-09 that release URL answered HTTP 500 and took main red as
// eight failures across two packages, none of which mentioned a
// download. ci.yml's own bubblewrap/wasmtime step already carries a
// comment about this exact shape, an install failure surfacing as
// "three unrelated-looking blueprint tests" three steps downstream, and
// the fix there was to verify at the step that installs. This asset is
// the third external dependency the suite needs and had none of that
// treatment.
//
// Exported rather than duplicated in a shell script so the version, the
// URL and the cache location stay in one place. A CI step that hardcoded
// the URL would be a second copy of a pin, which is the drift this
// project keeps paying for elsewhere.
func PrefetchInterpreter(ctx context.Context) (string, error) {
	return acquirePythonWasi(ctx)
}

// downloadRetrySchedule is the backoff between attempts at a transient
// failure. Short and bounded: this is a large asset from a third party,
// and the failure it exists for is a momentary 5xx, not an outage worth
// waiting out.
var downloadRetrySchedule = []time.Duration{time.Second, 3 * time.Second, 8 * time.Second}

// downloadAndExtractWithRetry retries a TRANSIENT download failure.
//
// A single HTTP 500 used to fail the whole acquisition, and therefore
// the whole test suite and any real `ubx plan` against a Python stack.
// Retrying is not only a CI concern: a developer's first Python
// evaluation hitting a momentary 5xx got the same hard failure.
//
// A 404 is deliberately NOT retried. That means the pinned version does
// not exist at that URL, which is a wrong pin rather than a blip, and
// retrying it three times only delays a clear answer.
func downloadAndExtractWithRetry(ctx context.Context, url, dest, wantSHA256 string) error {
	var err error
	for attempt := 0; ; attempt++ {
		err = downloadAndExtract(ctx, url, dest, wantSHA256)
		if err == nil {
			return nil
		}
		if attempt >= len(downloadRetrySchedule) || !isTransientDownloadFailure(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(downloadRetrySchedule[attempt]):
		}
	}
}

// isTransientDownloadFailure reports whether err is worth another
// attempt: a 5xx, a 429, or a transport-level failure. Anything else,
// including a 404, is answered the same way every time.
func isTransientDownloadFailure(err error) bool {
	if err == nil {
		return false
	}
	// A checksum mismatch is never transient: the same bytes mismatch
	// every time, and the two things it can mean, a wrong pin or an
	// archive that is not what it claims to be, are both answered by
	// stopping rather than trying again.
	var mismatch *checksumMismatchError
	if errors.As(err, &mismatch) {
		return false
	}
	var status *downloadStatusError
	if errors.As(err, &status) {
		return status.code >= 500 || status.code == http.StatusTooManyRequests
	}
	// Not a status at all: a DNS failure, a reset connection, a timeout.
	// Those are the transient ones by definition.
	return true
}

// checksumMismatchError is a distinct type so the retry decision can be
// made on it rather than on the text of a message.
//
// It exists because the first version of this was a plain fmt.Errorf
// and isTransientDownloadFailure treats any non-status error as
// transient, on the reasoning that a DNS failure or a reset connection
// is transient by definition. So a mismatch was retried three times
// while the comment beside it claimed it was not. A test asserting the
// attempt count is what caught that, not review.
type checksumMismatchError struct {
	url, got, want string
}

func (e *checksumMismatchError) Error() string {
	return fmt.Sprintf("download %s: checksum mismatch: got %s, want %s -- the archive is not the one this ubx pins, so it is refused rather than used", e.url, e.got, e.want)
}

// downloadStatusError carries the HTTP status so the retry decision can
// be made on it rather than on the text of a formatted message.
type downloadStatusError struct {
	code   int
	status string
	url    string
}

func (e *downloadStatusError) Error() string {
	return fmt.Sprintf("download %s: unexpected status %s", e.url, e.status)
}

func verifyPythonWasiDir(dir string) error {
	if _, err := os.Stat(filepath.Join(dir, "python.wasm")); err != nil {
		return fmt.Errorf("missing python.wasm: %w", err)
	}
	if info, err := os.Stat(filepath.Join(dir, "lib")); err != nil || !info.IsDir() {
		return fmt.Errorf("missing lib/ stdlib tree")
	}
	return nil
}

func defaultCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ubx", "python-wasi"), nil
}

// downloadAndExtract fetches url (a zip archive whose own top-level
// entries are python.wasm and lib/...) into a fresh temp location, then
// atomically renames it into place at dest -- so a failed or concurrent
// download never leaves a half-extracted, verifyPythonWasiDir-passing
// directory behind.
func downloadAndExtract(ctx context.Context, url, dest, wantSHA256 string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &downloadStatusError{code: resp.StatusCode, status: resp.Status, url: url}
	}

	zipFile, err := os.CreateTemp("", "ubx-python-wasi-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(zipFile.Name())
	defer zipFile.Close()

	// Hashed while streaming rather than by re-reading the file, so the
	// bytes that are checked are exactly the bytes that were written.
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(zipFile, digest), resp.Body); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != wantSHA256 {
		// Not retried: a digest mismatch is not a transient failure. The
		// same bytes will mismatch every time, and the two things it
		// means, a wrong pin or an archive that is not what it claims to
		// be, are both answered by stopping rather than by trying again.
		return &checksumMismatchError{url: url, got: got, want: wantSHA256}
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	stagingDir, err := os.MkdirTemp(filepath.Dir(dest), ".staging-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingDir)

	if err := unzip(zipFile.Name(), stagingDir); err != nil {
		return fmt.Errorf("extract %s: %w", url, err)
	}

	_ = os.RemoveAll(dest)
	if err := os.Rename(stagingDir, dest); err != nil {
		return fmt.Errorf("install to %s: %w", dest, err)
	}
	return nil
}

func unzip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
