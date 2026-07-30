package pyeval

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	pyassets "github.com/ubiquex/ubiquex/sdk/py"
)

// runtimeGuestPath is the fixed guest path ubx_sdk's own runtime source
// is preopened at -- sys.path is extended to include its parent
// directory via PYTHONPATH so `import ubx_sdk` resolves.
const runtimeGuestPath = "/ubxsdk"

// runOnce spawns exactly one wasmtime subprocess evaluating entryFile
// under WASI, returning its raw (uncanonicalized) stdout on success.
//
// Deliberately passes NO PYTHONHOME at all -- found empirically, not
// assumed (docs/sdk.md's own "The Python evaluator: decided
// empirically"): this specific CPython-WASI build resolves its own
// stdlib from a baked-in default that matches the conventional /lib
// guest path automatically; explicitly setting PYTHONHOME via --env
// (even to the identical "/lib" value) BREAKS stdlib resolution
// entirely ("Failed to import encodings module"), the opposite of what
// every other language's evaluator in this arc needed. Only
// PYTHONHASHSEED is passed explicitly, for determinism -- wasmtime
// forwards zero host env vars otherwise (confirmed empirically), so
// there is nothing else to scrub.
func runOnce(ctx context.Context, entryFile string) ([]byte, error) {
	absEntry, err := filepath.Abs(entryFile)
	if err != nil {
		return nil, fmt.Errorf("entry file: %w", err)
	}
	info, err := os.Stat(absEntry)
	if err != nil {
		return nil, fmt.Errorf("entry file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("entry file: %s is a directory", absEntry)
	}

	wasmtimePath, err := exec.LookPath("wasmtime")
	if err != nil {
		return nil, fmt.Errorf("wasmtime not found in PATH -- the Python SDK evaluator requires wasmtime (https://wasmtime.dev), chosen empirically over subprocess+sandbox-exec/bwrap (docs/sdk.md's own \"The Python evaluator: decided empirically\" section): %w", err)
	}

	wasiDir, err := acquirePythonWasi(ctx)
	if err != nil {
		return nil, err
	}

	assetsDir, err := extractAssets()
	if err != nil {
		return nil, err
	}

	entryDir := filepath.Dir(absEntry)
	entryBase := filepath.Base(absEntry)

	args := []string{
		"run",
		"--env", "PYTHONHASHSEED=0",
		// Makes `import ubx_sdk` resolve inside the guest -- this is a
		// GUEST env var (via --env), not a host one; wasmtime forwards
		// zero host env vars otherwise (confirmed empirically), so the
		// host process's own PYTHONPATH, if any, never reaches the
		// sandbox regardless.
		"--env", "PYTHONPATH=" + runtimeGuestPath,
		"--dir", filepath.Join(wasiDir, "lib") + "::/lib",
		// Mounts assetsDir itself (the parent of ubx_sdk/) at a single,
		// TOP-LEVEL guest path -- found empirically, not assumed: a
		// nested guest path two segments deep with no separate preopen
		// for the parent segment (e.g. "...::/ubxsdk/ubx_sdk") is not
		// reliably listable by the guest even though direct file opens
		// sometimes silently succeed via a DIFFERENT lookup path -- an
		// import that appears to work can actually be silently resolving
		// against something else entirely (this exact bug: an earlier
		// version of this mount looked like it worked because the test
		// script's own directory happened to also contain a copy of
		// ubx_sdk, masking that the intended mount was never really
		// reachable at all). One top-level preopen per real directory
		// tree, always.
		"--dir", assetsDir + "::" + runtimeGuestPath,
		"--dir", entryDir + "::/prog",
		filepath.Join(wasiDir, "python.wasm"),
		"/prog/" + entryBase,
	}

	cmd := exec.CommandContext(ctx, wasmtimePath, args...)
	// The wasmtime HOST process itself gets an explicitly empty
	// environment too -- defense in depth, even though wasmtime already
	// forwards nothing to the guest by default.
	cmd.Env = []string{}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("evaluate %s: %w\n%s", entryFile, err, msg)
		}
		return nil, fmt.Errorf("evaluate %s: %w", entryFile, err)
	}
	return stdout.Bytes(), nil
}

// extractAssets writes the embedded ubx_sdk runtime source
// (sdk/py/embed.go) to a fresh temp directory, once per process (the
// content is static for the lifetime of one `ubx` invocation).
var extractAssets = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "ubx-pyeval-")
	if err != nil {
		return "", fmt.Errorf("pyeval: create assets dir: %w", err)
	}

	data, err := pyassets.Assets.ReadFile("ubx_sdk/__init__.py")
	if err != nil {
		return "", fmt.Errorf("pyeval: read embedded ubx_sdk/__init__.py: %w", err)
	}
	destDir := filepath.Join(dir, "ubx_sdk")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("pyeval: %w", err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "__init__.py"), data, 0o644); err != nil {
		return "", fmt.Errorf("pyeval: %w", err)
	}

	return dir, nil
})
