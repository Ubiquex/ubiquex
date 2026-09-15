package blueprint

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// godeps.go is the Go half of a blueprint's own third-party
// dependencies: refusing one that is not pinned, and fetching one that
// is before evaluation reaches it.
//
// The failure it closes is the same as TypeScript's and arrived by a
// different route. A declared Go blueprint importing anything outside
// the standard library ran on a machine that already happened to have
// that module cached, and nowhere else:
//
//	github.com/google/uuid@v1.6.0: module lookup disabled by GOPROXY=off
//
// GOPROXY=off is deliberate (goeval/build.go): the evaluator does not
// fetch code while evaluating, which is the same posture --no-remote
// gives the TypeScript side. So the bytes have to be present before
// evaluation starts, which is what the prefetch below does.
//
// # Why this does not generate the pin, where TypeScript does
//
// `ubx blueprint package` writes a deno.lock for a TypeScript blueprint
// that needs one, because `deno install` writes a lock BESIDE the source
// and leaves the source alone.
//
// Go's equivalent does not. `go mod tidy` rewrites the author's go.mod
// and changes what their module requires: observed bumping a fixture
// from `go 1.23` to `go 1.26.3` unasked. Packaging is not the place to
// change what someone's module requires, and the author should see that
// diff rather than find it applied.
//
// So an unpinned Go blueprint is refused, naming the command and the
// file it produces. The wall is hit once per blueprint and the author
// keeps control of their own module.
//
// # Why there is no mirror, where TypeScript has one
//
// TypeScript needed a directory of symlinks beside the content store,
// because node_modules resolution walks UP from the importing file and a
// blueprint in the content store has nothing above it. Go resolves from
// a module cache instead, so where the blueprint sits does not matter
// and a populated cache is enough.
//
// The cache is the user's own, not one ubx owns, and that was measured
// rather than preferred. Go has exactly ONE module cache per build.
// Pointing GOMODCACHE at an ubx-owned cache holding only the blueprint's
// dependencies breaks the consumer's own:
//
//	main.go:6:2: github.com/google/go-cmp@v0.7.0: module lookup disabled
//	by GOPROXY=off
//
// That is the same trade rejected on the TypeScript side, where
// --node-modules-dir=none would have bought the blueprint's dependencies
// by taking away the consumer's. Prefetching into the default cache
// leaves the consumer untouched and is what any ordinary Go workflow
// already does.

// goSumFileName is Go's own name for the pin.
const goSumFileName = "go.sum"

// goModFileName is Go's own module manifest.
const goModFileName = "go.mod"

// goBlueprintDeps reports the module paths a blueprint requires beyond
// the standard library, in declaration order.
//
// A `replace` to a local path is skipped: those resolve from the
// filesystem rather than the module cache, so they are neither fetched
// nor pinned by go.sum, and refusing over one would refuse a blueprint
// that needs no network at all.
func goBlueprintDeps(dir string) ([]string, error) {
	path := filepath.Join(dir, goModFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", goModFileName, err)
	}
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", goModFileName, err)
	}

	local := map[string]bool{}
	for _, r := range f.Replace {
		if r.New.Version == "" && isLocalReplaceTarget(r.New.Path) {
			local[r.Old.Path] = true
		}
	}

	var out []string
	for _, r := range f.Require {
		if local[r.Mod.Path] {
			continue
		}
		out = append(out, r.Mod.Path)
	}
	sort.Strings(out)
	return out, nil
}

// isLocalReplaceTarget mirrors Go's own rule for a filesystem replace
// target: it starts with "./" or "../", or is absolute.
func isLocalReplaceTarget(p string) bool {
	return strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") || filepath.IsAbs(p)
}

// goBlueprintSumPath returns dir's own go.sum, or "".
func goBlueprintSumPath(dir string) string {
	p := filepath.Join(dir, goSumFileName)
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return ""
}

// checkGoBlueprintPinned refuses a blueprint whose dependencies nothing
// pins.
//
// The boundary is the same one the TypeScript side settled on, for the
// same reason: a dependency graph with a pin that travels under the
// content hash may be fetched, and one without has nothing saying what
// would arrive.
func checkGoBlueprintPinned(dir, name string) error {
	deps, err := goBlueprintDeps(dir)
	if err != nil {
		return fmt.Errorf("blueprint %q: %w", name, err)
	}
	if len(deps) == 0 || goBlueprintSumPath(dir) != "" {
		return nil
	}
	return fmt.Errorf("%s", unpinnedGoRefusal(name, deps))
}

// unpinnedGoRefusal names the command and the file, not the concept.
//
// The author has a working module; what they do not have is a reason to
// think ubx cares about go.sum. Telling them to run one command they
// already know is better than explaining a boundary.
func unpinnedGoRefusal(name string, deps []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "blueprint %q requires %d module(s) and ships no %s, so nothing pins what would be fetched:\n", name, len(deps), goSumFileName)
	for _, d := range deps {
		fmt.Fprintf(&b, "    %s\n", d)
	}
	fmt.Fprintf(&b, "  ubx fetches a blueprint's modules only when the blueprint pins them. A %s travels in the content\n", goSumFileName)
	b.WriteString("  store and is covered by the content hash, so the dependency graph is pinned by the same mechanism that\n")
	b.WriteString("  pins the blueprint itself. Without one, the hash would cover the module paths and not the bytes that ran.\n")
	fmt.Fprintf(&b, "  Fix it in the blueprint: run `go mod tidy`, commit the %s it writes, and re-package.\n", goSumFileName)
	b.WriteString("  ubx does not run `go mod tidy` for you: it rewrites go.mod and can change what your module requires,\n")
	b.WriteString("  which is a diff you should see rather than have applied by a packaging step.")
	return b.String()
}

// prefetchGoBlueprintDeps downloads a blueprint's modules before
// evaluation, with integrity enforced by the go.sum it ships.
//
// Run in a scratch directory holding copies of go.mod and go.sum rather
// than in the content store itself, because `go mod download` can write
// to go.sum and the store is immutable by design. Nothing else has to
// persist: the modules land in the shared cache, which is where the
// build will look for them.
//
// The default module cache, deliberately. See this file's own header for
// why an ubx-owned one is the wrong answer.
func prefetchGoBlueprintDeps(ctx context.Context, dir, name string) error {
	deps, err := goBlueprintDeps(dir)
	if err != nil {
		return fmt.Errorf("blueprint %q: %w", name, err)
	}
	if len(deps) == 0 {
		return nil
	}
	if goBlueprintSumPath(dir) == "" {
		// checkGoBlueprintPinned refuses before this is reached, so
		// arriving here means the two disagree.
		return fmt.Errorf("blueprint %q: no %s to fetch against", name, goSumFileName)
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("go not found in PATH, which fetching a blueprint's modules requires: %w", err)
	}

	scratch, err := os.MkdirTemp("", "ubx-bpdep-gomod-*")
	if err != nil {
		return fmt.Errorf("blueprint %q: %w", name, err)
	}
	defer os.RemoveAll(scratch)

	for _, f := range []string{goModFileName, goSumFileName} {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return fmt.Errorf("blueprint %q: read %s: %w", name, f, err)
		}
		if err := os.WriteFile(filepath.Join(scratch, f), data, 0o644); err != nil {
			return fmt.Errorf("blueprint %q: %w", name, err)
		}
	}

	cmd := exec.CommandContext(ctx, goBin, "mod", "download")
	cmd.Dir = scratch
	// GOFLAGS=-mod=mod for the reason goeval/build.go already records: a
	// go.mod the toolchain considers untidy otherwise fails rather than
	// being read as-is. GOWORK=off so a workspace above the temp
	// directory, or one inherited from the environment, cannot redirect
	// what this resolves.
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s", goPrefetchFailure(name, err, strings.TrimSpace(out.String())))
	}
	return nil
}

// goPrefetchFailure attributes a failure to the blueprint rather than to
// the stack that merely declared it.
//
// Go's own message for a checksum mismatch is explicit about what it
// means, so it is passed through rather than summarized: "This download
// does NOT match an earlier download recorded in go.sum. The bits may
// have been replaced on the origin server, or an attacker may have
// intercepted the download attempt."
func goPrefetchFailure(name string, err error, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "blueprint %q: fetching its modules failed: %v\n", name, err)
	if output != "" {
		for _, line := range strings.Split(output, "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	fmt.Fprintf(&b, "  ubx fetches a blueprint's modules with integrity enforced by the %s it ships, before evaluating\n", goSumFileName)
	b.WriteString("  anything. A failure here is the blueprint's own: its go.sum and its go.mod disagree, or the module\n")
	b.WriteString("  source is serving something other than what the go.sum records.")
	return b.String()
}
