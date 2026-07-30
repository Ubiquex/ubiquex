package pyeval

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

	if err := downloadAndExtract(ctx, pythonWasiURL, dir); err != nil {
		return "", fmt.Errorf("acquire CPython-WASI build: %w", err)
	}
	if err := verifyPythonWasiDir(dir); err != nil {
		return "", fmt.Errorf("acquired CPython-WASI build looks wrong: %w", err)
	}
	return dir, nil
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
		return fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
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
