package goeval

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// buildProgram compiles entryFile's own Go package into a standalone
// binary under a fresh temp directory, returning its path and a cleanup
// func that removes the temp directory. CGO_ENABLED=0 -- deliberately:
// a fully static binary needs no dynamic-library reads at run time
// (relevant to sandbox_linux.go, which binds nothing but the binary's
// own directory as a result) and eliminates cgo's own arbitrary-C-code
// surface from the build step entirely.
//
// entryFile must live inside a real Go module (a go.mod found by
// searching upward) that itself requires github.com/ubiquex/ubx-sdk-go
// -- the evaluator does not auto-inject that dependency (unlike
// sdkeval's own embedded-runtime-plus-import-map mechanism for
// @ubx/sdk); a real go.mod (with a `replace` during local development,
// same as sdk/conformance/programs/go's own real one) is the ordinary,
// idiomatic way any Go project declares a dependency, and requiring one
// here is not a burden, just how Go works.
func buildProgram(ctx context.Context, entryFile string) (binaryPath string, cleanup func(), err error) {
	absEntry, err := filepath.Abs(entryFile)
	if err != nil {
		return "", nil, fmt.Errorf("entry file: %w", err)
	}
	info, err := os.Stat(absEntry)
	if err != nil {
		return "", nil, fmt.Errorf("entry file: %w", err)
	}
	if info.IsDir() {
		return "", nil, fmt.Errorf("entry file: %s is a directory", absEntry)
	}

	moduleRoot, err := findModuleRoot(filepath.Dir(absEntry))
	if err != nil {
		return "", nil, err
	}

	goPath, err := exec.LookPath("go")
	if err != nil {
		return "", nil, fmt.Errorf("go toolchain not found in PATH -- the Go SDK evaluator requires `go build` to compile the program: %w", err)
	}

	buildDir, err := os.MkdirTemp("", "ubx-goeval-")
	if err != nil {
		return "", nil, fmt.Errorf("create build dir: %w", err)
	}
	cleanup = func() { os.RemoveAll(buildDir) }

	// Build from a throwaway copy of the module, never the program
	// author's own real directory -- GOFLAGS=-mod=mod (below) writes
	// back to go.mod when it reconciles a toolchain-version mismatch,
	// and this evaluator must never mutate a program's own checked-in
	// files as a side effect of evaluating it.
	moduleCopy := filepath.Join(buildDir, "module")
	if err := copyDir(moduleRoot, moduleCopy); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("copy module to build dir: %w", err)
	}
	// A `replace ... => ./relative/path` in the original go.mod resolved
	// relative to moduleRoot -- now that the module lives at moduleCopy
	// instead, rewrite any such local replace target to the ORIGINAL
	// absolute path so it still resolves correctly.
	if err := rewriteLocalReplaces(filepath.Join(moduleCopy, "go.mod"), moduleRoot); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("rewrite local replace directives: %w", err)
	}

	binPath := filepath.Join(buildDir, "program")

	pkgArg, err := packageArg(moduleRoot, filepath.Dir(absEntry))
	if err != nil {
		cleanup()
		return "", nil, err
	}

	cmd := exec.CommandContext(ctx, goPath, "build", "-o", binPath, pkgArg)
	cmd.Dir = moduleCopy
	// GOPROXY=off is this evaluator's own real analog of TS's --no-remote:
	// a go.mod requiring anything beyond what a local `replace` (or the
	// already-populated module cache) can satisfy fails the build loudly
	// rather than silently fetching untrusted code over the network
	// during evaluation. A `replace` to a local filesystem path (the
	// real, intended shape for a Go SDK program's own go.mod, matching
	// sdk/conformance/programs/go) is unaffected -- Go never consults
	// GOPROXY for a local-path replace.
	//
	// GOFLAGS=-mod=mod (found empirically, not assumed): a go.mod whose
	// own `go` directive doesn't exactly match the installed toolchain's
	// version -- an entirely ordinary, expected situation, not a real
	// dependency change -- otherwise fails under the default -mod=readonly
	// with "updates to go.mod needed," even though reconciling it is a
	// purely local, network-free bookkeeping step. Without this, a
	// hermetic evaluation could spuriously fail for a program author who
	// simply hasn't run `go mod tidy` on the exact machine `ubx` runs on.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOPROXY=off", "GOFLAGS=-mod=mod")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		cleanup()
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", nil, fmt.Errorf("build %s: %w\n%s", entryFile, runErr, msg)
		}
		return "", nil, fmt.Errorf("build %s: %w", entryFile, runErr)
	}

	return binPath, cleanup, nil
}

// findModuleRoot searches upward from dir for the nearest go.mod.
func findModuleRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s -- a Go SDK program must live in its own real Go module (requiring github.com/ubiquex/ubx-sdk-go)", dir)
		}
		dir = parent
	}
}

// copyDir recursively copies src's own file tree to dst (which must not
// already exist).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// localReplaceRe matches both single-line ("replace X => ./y") and
// replace-block ("X => ./y" inside a `replace ( ... )` block) forms --
// handles the common, realistic shapes a go.mod's own replace directives
// take, not a full go.mod grammar parser.
var localReplaceRe = regexp.MustCompile(`^(\s*(?:replace\s+)?\S+(?:\s+\S+)?\s*=>\s*)(\.\.?[/\\]\S*)(.*)$`)

// rewriteLocalReplaces rewrites every relative-filesystem-path replace
// target in goModPath to an absolute path resolved against
// originalRoot -- see buildProgram's own doc comment for why (the module
// has been copied to a new location; a relative replace path would
// otherwise resolve somewhere wrong, or nowhere at all).
func rewriteLocalReplaces(goModPath, originalRoot string) error {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		m := localReplaceRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		abs := filepath.Join(originalRoot, m[2])
		lines[i] = m[1] + abs + m[3]
	}
	return os.WriteFile(goModPath, []byte(strings.Join(lines, "\n")), 0o644)
}

// packageArg turns entryDir into the `go build`-relative package
// argument ("." or "./sub/dir") go build expects when run with
// cmd.Dir == moduleRoot.
func packageArg(moduleRoot, entryDir string) (string, error) {
	rel, err := filepath.Rel(moduleRoot, entryDir)
	if err != nil {
		return "", fmt.Errorf("compute package path: %w", err)
	}
	if rel == "." {
		return ".", nil
	}
	return "./" + filepath.ToSlash(rel), nil
}
