// pydeps.go is UBI-130's own mechanism: Python has no native "import from
// a URL" the way Go modules resolve straight from git or Deno's own
// import-map-based JSR resolution does, so a Python stack calling a
// blueprint published to a registry needs a real, CI-pipeline-friendly
// way to declare and resolve that dependency. The resolved design
// (Linear, filed at UBI-74's own close): reuse pip's own EXISTING
// "<name> @ <url>" requirements.txt specifier syntax (PEP 508 + pip's
// real VCS-URL support, https://pip.pypa.io/en/stable/topics/vcs-support/
// -- no new syntax invented), resolved at `ubx plan`/`ubx resolve
// --from-code` time -- pulled + verified into ubx's own local cache and
// made importable BEFORE the calling script ever runs, with a real,
// visible receipt line (this project's own "never a silent network call"
// discipline, matching provider.Acquire's own docs/architecture.md
// framing) -- never a separate manual sync step, so a CI pipeline that
// already runs `ubx plan` needs nothing extra.
//
// requirements.txt (not a pyproject.toml [tool.ubx.blueprints] table) is
// the primary, and for now the ONLY, supported format: it's already the
// ubiquitous, zero-config file every real Python CI pipeline reads with
// no extra tooling, and it's exactly the format UBI-130's own resolved
// design record commits to in its full worked example. A pyproject.toml-
// based path is a real, deferred alternative, not attempted here -- it
// can be added later without changing requirements.txt's own meaning at
// all.
//
// Deliberately reuses Pull/Verify (pull.go/verify.go, Slice 3/7/8's own
// git/OCI/local/tarball mechanism) for the actual fetch -- never a
// second, parallel pull mechanism -- and buildManifest (manifest.go) for
// the one content-hash scheme this whole codebase already uses
// everywhere else.
package blueprint

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ubiquex/ubiquex/pyeval"
)

// PyRequirementsFileName is the file ResolvePyDependencies reads, next to
// the entry .py file -- pip's own real requirements.txt convention,
// unchanged.
const PyRequirementsFileName = "requirements.txt"

// PyDependency is one requirements.txt "<name> @ <url>" line naming a
// blueprint package dependency. An ordinary requirements.txt line with no
// " @ " (a plain PyPI pin, e.g. the real published `ubx_sdk` runtime,
// UBI-107) is left alone entirely -- this file only ever interprets
// "@ url" entries, never takes over the whole file's meaning. `ubx_sdk`
// itself is never resolved through this mechanism at all inside
// `ubx plan`/`ubx resolve --from-code`'s own WASI sandbox -- pyeval
// embeds it directly (sdk/py/embed.go), independent of PyPI publish
// status; the real PyPI package matters only outside that sandbox (the
// standalone generated ubx-sdk-*-py bindings repos).
type PyDependency struct {
	// Name is the declared LHS distribution name -- checked against the
	// pulled blueprint's own declared blueprint.lock.json name (a real
	// integrity check: a requirements.txt entry that pulls the WRONG
	// blueprint under the expected name is a clear, named error, not a
	// silent mismatch), but otherwise purely a label -- unlike
	// invokeCall's own synthesized callers (invoke.go), nothing here
	// generates any calling code, so no package/function identifier is
	// ever derived from it. The calling .py script's own plain `import`
	// statement is expected to already know the blueprint's own real,
	// build-time-derived Python module name (packageIdent(blueprintName)),
	// exactly like any real pip package's distribution name and its
	// importable module name are already allowed to differ.
	Name string
	// URL is the RHS, verbatim, e.g. "oci://ghcr.io/ubiquex/ci-platform:v3"
	// -- kept for the receipt line and error messages.
	URL string
	// Source, Ref, Path are Pull's own three positional arguments,
	// already derived from URL.
	Source string
	Ref    string
	Path   string
}

// isLocalSource reports whether dep was declared with no recognized
// remote scheme -- a bare local path (or a "file://" URL), matching
// Pull's own local-directory/tarball-file dispatch. A local dependency is
// never cached: unlike a git ref or an OCI tag, a local path names
// something a blueprint author may be actively editing, and caching it
// under a spec-derived key would silently serve stale content after an
// edit.
func (d PyDependency) isLocalSource() bool {
	return !strings.Contains(d.Source, "://")
}

// ParsePyDependencies reads dir/requirements.txt, if present, and returns
// every "<name> @ <url>" entry it declares, in file order. Returns (nil,
// nil) if the file doesn't exist at all -- a Python SDK program with no
// blueprint dependencies needs no requirements.txt.
func ParsePyDependencies(dir string) ([]PyDependency, error) {
	path := filepath.Join(dir, PyRequirementsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("blueprint: read %s: %w", path, err)
	}

	var deps []PyDependency
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rawURL, ok := strings.Cut(line, "@")
		if !ok {
			continue // an ordinary pip requirement (no "@ url") -- not ours to interpret
		}
		name = strings.TrimSpace(name)
		url := strings.TrimSpace(rawURL)
		if name == "" || url == "" {
			return nil, fmt.Errorf("blueprint: %s:%d: malformed %q -- want \"<name> @ <url>\"", path, lineNo, line)
		}
		source, ref, refPath, err := parsePyRequirementURL(url)
		if err != nil {
			return nil, fmt.Errorf("blueprint: %s:%d: %w", path, lineNo, err)
		}
		deps = append(deps, PyDependency{Name: name, URL: url, Source: source, Ref: ref, Path: refPath})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("blueprint: read %s: %w", path, err)
	}
	return deps, nil
}

// parsePyRequirementURL parses one requirements.txt entry's RHS into
// Pull's own (source, ref, path) arguments -- the three schemes UBI-130's
// own resolved design names explicitly: "oci://" (passed straight
// through, Pull's own oci:// branch already expects exactly this form),
// "git+<transport>://..." (pip's own real VCS-URL syntax -- an optional
// "@<ref>" naming a branch/tag/commit and an optional
// "#subdirectory=<path>" fragment, both stripped before handing the bare
// transport URL to Pull's own git-clone branch), and a bare local path
// (no recognized scheme at all, or an explicit "file://" -- Pull's own
// os.Stat dispatch already handles a local directory or tarball file
// identically either way).
func parsePyRequirementURL(rawURL string) (source, ref, path string, err error) {
	switch {
	case strings.HasPrefix(rawURL, "oci://"):
		return rawURL, "", "", nil
	case strings.HasPrefix(rawURL, "file://"):
		return strings.TrimPrefix(rawURL, "file://"), "", "", nil
	case strings.HasPrefix(rawURL, "git+"):
		rest := strings.TrimPrefix(rawURL, "git+")
		if base, frag, ok := strings.Cut(rest, "#"); ok {
			rest = base
			v, ok := strings.CutPrefix(frag, "subdirectory=")
			if !ok {
				return "", "", "", fmt.Errorf("unsupported fragment %q in %q -- only #subdirectory=<path> is recognized", frag, rawURL)
			}
			path = v
		}
		// A ref never contains "/", so an "@" found only in the URL's
		// FINAL path segment is the "@<ref>" suffix -- an "@" earlier
		// (e.g. ssh's own "git@github.com" user-info) is left alone.
		if lastSlash := strings.LastIndex(rest, "/"); lastSlash >= 0 {
			if at := strings.Index(rest[lastSlash:], "@"); at >= 0 {
				refIdx := lastSlash + at
				ref = rest[refIdx+1:]
				rest = rest[:refIdx]
			}
		}
		if rest == "" {
			return "", "", "", fmt.Errorf("git+ URL %q has no repository URL after stripping the git+ prefix", rawURL)
		}
		return rest, ref, path, nil
	case strings.Contains(rawURL, "://"):
		return "", "", "", fmt.Errorf("unrecognized scheme in %q -- want oci://, git+<transport>://, file://, or a bare local path", rawURL)
	default:
		return rawURL, "", "", nil // bare local path
	}
}

// PyDepMount is one resolved blueprint dependency, ready to hand to
// pyeval as an extra WASI preopen.
type PyDepMount struct {
	Dep     PyDependency
	HostDir string // <cache-or-source-dir>/py -- the blueprint's own built Python package
	Receipt string
}

// ResolvePyDependencies reads entryFile's own sibling requirements.txt
// (if any) and, for every declared blueprint dependency, pulls (or
// reuses an already-verified local-cache hit) + verifies it, returning
// one PyDepMount per dependency, in requirements.txt's own declared
// order (deterministic -- never a map). Every dependency, cached or
// freshly pulled, gets a real, non-empty Receipt: per this project's own
// "never a silent network call" discipline, the caller (cli/resolve.go,
// cli/plan.go) prints every one of these to its own command output
// BEFORE resolving, so a reviewer can always see that pulling a
// blueprint dependency was part of planning.
func ResolvePyDependencies(ctx context.Context, entryFile string) ([]PyDepMount, error) {
	deps, err := ParsePyDependencies(filepath.Dir(entryFile))
	if err != nil {
		return nil, err
	}
	mounts := make([]PyDepMount, 0, len(deps))
	for _, dep := range deps {
		m, err := resolveOnePyDependency(ctx, dep)
		if err != nil {
			return nil, fmt.Errorf("blueprint dependency %q (%s @ %s): %w", dep.Name, dep.Name, dep.URL, err)
		}
		mounts = append(mounts, m)
	}
	return mounts, nil
}

// resolveOnePyDependency pulls+verifies (or reuses a cache hit for) one
// declared dependency, mirroring provider.Acquire's own cache discipline
// (provider/cache.go): once verified, always verified -- a cache hit is
// never re-pulled or re-verified from the network. Unlike a provider
// binary, a blueprint has no registry-signed version to trust before
// ever pulling, so the cache is keyed by the declared spec itself
// (name+URL, hashed) rather than a pre-known content hash; Verify (run on
// every hit, cached or fresh) is what actually establishes trust.
func resolveOnePyDependency(ctx context.Context, dep PyDependency) (PyDepMount, error) {
	if dep.isLocalSource() {
		return resolveLocalPyDependency(ctx, dep)
	}

	cacheDir, err := pyDepCacheDir(dep)
	if err != nil {
		return PyDepMount{}, err
	}

	fromCache := true
	manifest, verr := Verify(cacheDir)
	if verr != nil {
		fromCache = false
		if err := os.RemoveAll(cacheDir); err != nil {
			return PyDepMount{}, err
		}
		if err := os.MkdirAll(filepath.Dir(cacheDir), 0o755); err != nil {
			return PyDepMount{}, err
		}
		if _, err := Pull(ctx, dep.Source, cacheDir, dep.Ref, dep.Path); err != nil {
			return PyDepMount{}, fmt.Errorf("pull: %w", err)
		}
		manifest, err = Verify(cacheDir)
		if err != nil {
			return PyDepMount{}, fmt.Errorf("verify: %w", err)
		}
	}

	return finishPyDepMount(dep, cacheDir, manifest, fromCache)
}

// resolveLocalPyDependency pulls dep fresh into a throwaway scratch
// directory every time (never cached -- see PyDependency.isLocalSource's
// own doc comment) and verifies it in place.
func resolveLocalPyDependency(ctx context.Context, dep PyDependency) (PyDepMount, error) {
	scratch, err := os.MkdirTemp("", "ubx-pydep-local-*")
	if err != nil {
		return PyDepMount{}, err
	}
	dest := filepath.Join(scratch, "blueprint")
	if _, err := Pull(ctx, dep.Source, dest, dep.Ref, dep.Path); err != nil {
		return PyDepMount{}, fmt.Errorf("pull: %w", err)
	}
	manifest, err := Verify(dest)
	if err != nil {
		return PyDepMount{}, fmt.Errorf("verify: %w", err)
	}
	return finishPyDepMount(dep, dest, manifest, false)
}

func finishPyDepMount(dep PyDependency, dir string, manifest *Manifest, fromCache bool) (PyDepMount, error) {
	if manifest.Name != dep.Name {
		return PyDepMount{}, fmt.Errorf("declared name %q doesn't match the pulled blueprint's own declared name %q -- check the requirements.txt entry", dep.Name, manifest.Name)
	}

	pyDir := filepath.Join(dir, "py")
	if info, err := os.Stat(pyDir); err != nil || !info.IsDir() {
		return PyDepMount{}, fmt.Errorf("has no built py/ package -- the blueprint must be built with `ubx blueprint build` (lang: py or all) before it can be used as a Python dependency")
	}

	cacheNote := ""
	if fromCache {
		cacheNote = " (cached)"
	}
	receipt := fmt.Sprintf("pulled %s @ %s%s, verified: content hash %s matches (%d file(s))",
		dep.Name, dep.URL, cacheNote, manifest.ContentHash, len(manifest.Files))

	return PyDepMount{Dep: dep, HostDir: pyDir, Receipt: receipt}, nil
}

// pyDepCacheDir returns the local cache directory for dep, keyed by its
// own declared spec (name+URL, hashed) -- ~/.ubx/blueprints/by-spec/<hex>,
// the same "~/.ubx/<kind>/..." cache-root convention provider/cache.go's
// own defaultCacheRoot (~/.ubx/providers) already established.
func pyDepCacheDir(dep PyDependency) (string, error) {
	root, err := defaultBlueprintCacheRoot()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(dep.Name + "@" + dep.URL))
	return filepath.Join(root, "by-spec", hex.EncodeToString(sum[:])), nil
}

func defaultBlueprintCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ubx", "blueprints"), nil
}

// pyEvalDeps converts mounts into pyeval's own ExtraDep slice -- the one
// place blueprint's own PyDepMount is translated into the type pyeval
// itself actually understands.
func pyEvalDeps(mounts []PyDepMount) []pyeval.ExtraDep {
	deps := make([]pyeval.ExtraDep, len(mounts))
	for i, m := range mounts {
		deps[i] = pyeval.ExtraDep{HostDir: m.HostDir}
	}
	return deps
}

// EvaluatePythonWithDeps is `ubx resolve`/`ubx plan --from-code <file>.py`'s
// own single entry point: resolves entryFile's own requirements.txt
// blueprint dependencies (ResolvePyDependencies), makes them importable,
// then evaluates entryFile itself (pyeval.Evaluate) -- the one place
// pull-before-import sequencing is guaranteed: pyeval never runs the
// script until every dependency it declared is already pulled, verified,
// and mounted. Returns the evaluated, canonical intent/v1 document
// alongside every dependency's own receipt line, in declared order, for
// the CLI layer to print.
func EvaluatePythonWithDeps(ctx context.Context, entryFile string) (canon []byte, receipts []string, err error) {
	mounts, err := ResolvePyDependencies(ctx, entryFile)
	if err != nil {
		return nil, nil, err
	}
	canon, err = pyeval.Evaluate(ctx, entryFile, pyEvalDeps(mounts)...)
	if err != nil {
		return nil, nil, err
	}
	receipts = make([]string, len(mounts))
	for i, m := range mounts {
		receipts[i] = m.Receipt
	}
	return canon, receipts, nil
}
