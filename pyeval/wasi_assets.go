package pyeval

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	if verifyPythonWasiDir(dir) == nil {
		return dir, nil
	}

	if err := downloadAndExtractWithRetry(ctx, pythonWasiURL, dir); err != nil {
		return "", fmt.Errorf("acquire CPython-WASI build: %w", err)
	}
	if err := verifyPythonWasiDir(dir); err != nil {
		return "", fmt.Errorf("acquired CPython-WASI build looks wrong: %w", err)
	}
	return dir, nil
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
func downloadAndExtractWithRetry(ctx context.Context, url, dest string) error {
	var err error
	for attempt := 0; ; attempt++ {
		err = downloadAndExtract(ctx, url, dest)
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
	var status *downloadStatusError
	if errors.As(err, &status) {
		return status.code >= 500 || status.code == http.StatusTooManyRequests
	}
	// Not a status at all: a DNS failure, a reset connection, a timeout.
	// Those are the transient ones by definition.
	return true
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
func downloadAndExtract(ctx context.Context, url, dest string) error {
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

	if _, err := io.Copy(zipFile, resp.Body); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
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
