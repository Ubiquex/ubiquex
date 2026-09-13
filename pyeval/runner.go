package pyeval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
// runOnce returns the program's stdout AND its stderr. stderr is
// returned on success too, not only on failure: a program can write a
// diagnosis and still exit 0, and that diagnosis used to be discarded
// exactly when it was the only thing available (core/evaloutput.go).
func runOnce(ctx context.Context, entryFile string, deps []ExtraDep, roots []BlueprintRoot) (stdoutBytes, stderrBytes []byte, err error) {
	absEntry, err := filepath.Abs(entryFile)
	if err != nil {
		return nil, nil, fmt.Errorf("entry file: %w", err)
	}
	info, err := os.Stat(absEntry)
	if err != nil {
		return nil, nil, fmt.Errorf("entry file: %w", err)
	}
	if info.IsDir() {
		return nil, nil, fmt.Errorf("entry file: %s is a directory", absEntry)
	}

	wasmtimePath, err := exec.LookPath("wasmtime")
	if err != nil {
		return nil, nil, fmt.Errorf("wasmtime not found in PATH -- the Python SDK evaluator requires wasmtime (https://wasmtime.dev), chosen empirically over subprocess+sandbox-exec/bwrap (docs/sdk.md's own \"The Python evaluator: decided empirically\" section): %w", err)
	}

	wasiDir, err := acquirePythonWasi(ctx)
	if err != nil {
		return nil, nil, err
	}

	assetsDir, err := extractAssets()
	if err != nil {
		return nil, nil, err
	}

	entryDir := filepath.Dir(absEntry)
	entryBase := filepath.Base(absEntry)

	// UBI-130: each ExtraDep gets its own fresh top-level guest preopen
	// (same "one top-level preopen per real directory tree, always" rule
	// the ubx_sdk mount below already established empirically) and an
	// entry on PYTHONPATH ahead of the entry script's own directory, so a
	// pulled blueprint's own `from <pkg> import ...` resolves before the
	// script's own code ever runs.
	pythonPath := runtimeGuestPath
	var depDirArgs []string
	for i, d := range deps {
		guest := fmt.Sprintf("/ubxdep%d", i)
		depDirArgs = append(depDirArgs, "--dir", d.HostDir+"::"+guest)
		pythonPath += ":" + guest
	}

	// UBI-266: hand the runtime the blueprint roots this program can
	// reach, as GUEST paths, since that is what a frame's co_filename
	// will be. Their own scratch directory with its own top-level
	// preopen, following the same "one top-level preopen per real
	// directory tree" rule every other mount here already follows.
	//
	// A mounted module rather than an environment variable: the Go and
	// TypeScript evaluators scrub the environment deliberately and "no
	// environment leakage" is this project's own determinism rule, so
	// all three languages stay on the same footing. It also cannot be
	// written into assetsDir, which is extracted once per process and
	// shared by every evaluation in it.
	rootsDir, cleanupRoots, err := writeBlueprintRootsModule(roots, entryDir, deps)
	if err != nil {
		return nil, nil, err
	}
	defer cleanupRoots()
	if rootsDir != "" {
		pythonPath += ":" + blueprintRootsGuestPath
	}

	args := []string{
		"run",
		"--env", "PYTHONHASHSEED=0",
		// UBI-130, a real, live-found finding: CPython's own import
		// machinery writes __pycache__/*.pyc bytecode-cache files into
		// whatever directory a module was imported FROM -- and wasmtime's
		// own "--dir host::guest" preopen is read-write by default, so
		// without this, importing a blueprint dependency mutates its own
		// host directory as a side effect of merely being USED. Harmless
		// for a one-shot scratch copy (invoke.go's own throwaway callers),
		// but fatal for a directory meant to be VERIFIED again later --
		// caught empirically via UBI-130's own required live GHCR
		// verification: a cached blueprint dependency's own re-Verify
		// failed content-hash comparison after its first real use, because
		// the very act of importing it had already changed two files
		// inside it. PYTHONDONTWRITEBYTECODE=1 stops CPython from ever
		// writing bytecode caches at all, for every mounted directory, not
		// just UBI-130's own dependency mounts -- a pure performance
		// optimization CPython otherwise does silently, so this has no
		// other observable effect.
		"--env", "PYTHONDONTWRITEBYTECODE=1",
		// Makes `import ubx_sdk` (and any UBI-130 blueprint dependency)
		// resolve inside the guest -- this is a GUEST env var (via
		// --env), not a host one; wasmtime forwards zero host env vars
		// otherwise (confirmed empirically), so the host process's own
		// PYTHONPATH, if any, never reaches the sandbox regardless.
		"--env", "PYTHONPATH=" + pythonPath,
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
	}
	args = append(args, depDirArgs...)
	if rootsDir != "" {
		args = append(args, "--dir", rootsDir+"::"+blueprintRootsGuestPath)
	}
	args = append(args,
		"--dir", entryDir+"::/prog",
		filepath.Join(wasiDir, "python.wasm"),
		"/prog/"+entryBase,
	)

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
			return nil, nil, fmt.Errorf("evaluate %s: %w\n%s", entryFile, err, msg)
		}
		return nil, nil, fmt.Errorf("evaluate %s: %w", entryFile, err)
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

// extractAssets writes the embedded ubx_sdk runtime source
// (sdk/py/embed.go) to a fresh temp directory, once per process (the
// content is static for the lifetime of one `ubx` invocation).
var extractAssets = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "ubx-pyeval-")
	if err != nil {
		return "", fmt.Errorf("pyeval: create assets dir: %w", err)
	}

	data, err := fs.ReadFile(assets, "ubx_sdk/__init__.py")
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

// blueprintRootsGuestPath is where the generated roots module is
// mounted. A top-level path of its own, distinct from /ubxdepN so a
// program can never have a dependency shadow it.
const blueprintRootsGuestPath = "/ubxroots"

// BlueprintRoot names one blueprint whose code this program can reach,
// by the HOST directory it occupies. Translating that to the guest path
// the program will actually see is this package's own job, since this
// package is what assigns every guest path in the first place.
type BlueprintRoot struct {
	HostDir string
	Name    string
}

// writeBlueprintRootsModule writes the _ubx_blueprint_roots module the
// runtime imports, returning its host directory and a cleanup. Returns
// an empty directory, and mounts nothing, when there are no roots to
// report: an ordinary stack importing no blueprint pays nothing.
func writeBlueprintRootsModule(roots []BlueprintRoot, entryDir string, deps []ExtraDep) (string, func(), error) {
	type entry struct {
		Match string `json:"match"`
		Name  string `json:"name"`
	}
	var entries []entry
	for _, r := range roots {
		guest, ok := guestPathFor(r.HostDir, entryDir, deps)
		if !ok {
			// A blueprint the program can reach on the host but that is
			// not mounted into the sandbox cannot produce a frame, so
			// there is nothing to match against. Skipping it is not a
			// silent loss: the stamping pass still refuses a bare name it
			// cannot resolve.
			continue
		}
		entries = append(entries, entry{Match: guest, Name: r.Name})
	}
	if len(entries) == 0 {
		return "", func() {}, nil
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Match != entries[j].Match {
			return entries[i].Match < entries[j].Match
		}
		return entries[i].Name < entries[j].Name
	})

	// Canonical JSON inside a Python literal, which JSON already is.
	// Sorted so two evaluations of the same program write byte-identical
	// bytes, matching DoubleRun's own expectation that nothing about a
	// run differs between its two passes.
	payload, err := json.Marshal(entries)
	if err != nil {
		return "", func() {}, fmt.Errorf("pyeval: encode blueprint roots: %w", err)
	}

	dir, err := os.MkdirTemp("", "ubx-pyroots-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("pyeval: create blueprint roots dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(dir) }
	content := "# Written by ubx for one evaluation (UBI-266). Not API.\nROOTS = " + string(payload) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "_ubx_blueprint_roots.py"), []byte(content), 0o644); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("pyeval: write blueprint roots module: %w", err)
	}
	return dir, cleanup, nil
}

// guestPathFor translates a host directory into the path the guest will
// see it at, which is the only form a frame's co_filename can take.
//
// Two shapes reach here, and both are this package's own doing: a
// declared dependency, mounted at the /ubxdepN it was assigned in
// order, and a blueprint sitting inside the program's own directory,
// which arrives under /prog with the rest of it.
func guestPathFor(hostDir, entryDir string, deps []ExtraDep) (string, bool) {
	for i, d := range deps {
		if d.HostDir == hostDir {
			return fmt.Sprintf("/ubxdep%d", i), true
		}
	}
	rel, err := filepath.Rel(entryDir, hostDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if rel == "." {
		return "/prog", true
	}
	return "/prog/" + filepath.ToSlash(rel), true
}
