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

	"github.com/ubiquex/ubiquex/core/resolver"
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

	// ContentHash is the verified content hash this dependency resolved
	// to, "sha256:<hex>" -- the same value Ref embeds, kept separately
	// so the lock file records an identity rather than parsing one back
	// out of a composite string.
	ContentHash string
	// Ref is UBI-126's own real, complete "<name>:<content_hash>" ref for
	// this dependency -- already known for free at this point (Verify,
	// above, already computed it to establish trust before ever mounting
	// this dependency), never a second hashing pass. Used to complete any
	// incomplete "blueprint" source a direct sdk.push_blueprint_source
	// call inside the evaluated program may have left behind (see
	// StampDirectCallProvenancePy, below) -- Python's own external
	// completion half of the mechanism sdk/go/runtime's own doc comment
	// describes, needing no separate discovery step the way Go's
	// `go list -m all`/TS's `deno info` do, since this dependency
	// resolution step already produced it as a byproduct.
	Ref string
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
// notes are receipt lines that belong to the resolution as a whole
// rather than to any one dependency (a supersession, a pruned lock
// entry). Returned separately rather than as a mount carrying only a
// Receipt: a mount is also a PYTHONPATH root and a provenance ref, and a
// half-populated one would add an empty root and an empty-named ref
// downstream.
func ResolvePyDependencies(ctx context.Context, entryFile string) (mounts []PyDepMount, notes []string, err error) {
	fromRequirements, err := ParsePyDependencies(filepath.Dir(entryFile))
	if err != nil {
		return nil, nil, err
	}

	// .ubx/config's own [blueprints] table joins requirements.txt here,
	// and wins on a name collision. See mergeDeclaredBlueprints for why
	// both exist and why the table is the path forward.
	policy := lockPolicyFrom(ctx)
	deps, superseded := mergeDeclaredBlueprints(policy.Declared, fromRequirements)

	lock, err := loadLockForPolicy(policy)
	if err != nil {
		return nil, nil, err
	}

	mounts = make([]PyDepMount, 0, len(deps))
	for _, dep := range deps {
		locked, hasLock := lock.Entry(policy.Stack, dep.Name)

		// The locked hash is used as the cache key, so a locked stack
		// resolves content-addressed rather than by declaration string.
		// Only when the source still matches: a changed declaration must
		// reach CheckLocked's own "the declaration changed" message
		// rather than being silently served the old content from cache.
		expect := ""
		if hasLock && locked.Source == dep.URL && policy.Mode != LockUpdate {
			expect = locked.ContentHash
		}

		m, err := resolveOnePyDependency(ctx, dep, expect)
		if err != nil {
			return nil, nil, fmt.Errorf("blueprint dependency %q (%s @ %s): %w", dep.Name, dep.Name, dep.URL, err)
		}

		if err := applyLockPolicy(policy, lock, dep, m, hasLock, locked); err != nil {
			return nil, nil, err
		}
		mounts = append(mounts, m)
	}

	notes, err = finishLockPolicy(policy, lock, deps, superseded)
	if err != nil {
		return nil, nil, err
	}
	return mounts, notes, nil
}

// loadLockForPolicy reads the lock file when the policy involves one.
// LockOff gets an empty lock rather than a nil one, so every lookup
// below is a plain miss and needs no special case.
func loadLockForPolicy(p LockPolicy) (*StackLock, error) {
	if p.Mode == LockOff {
		return &StackLock{Stacks: map[string]map[string]LockEntry{}}, nil
	}
	return LoadStackLock(p.LedgerDir)
}

// applyLockPolicy is the per-dependency half: verify against the lock,
// or record, according to mode.
func applyLockPolicy(p LockPolicy, lock *StackLock, dep PyDependency, m PyDepMount, hasLock bool, locked LockEntry) error {
	hash := m.ContentHash
	switch p.Mode {
	case LockOff:
		return nil

	case LockUpdate:
		lock.Set(p.Stack, dep.Name, LockEntry{Source: dep.URL, ContentHash: hash})
		return nil

	case LockVerify, LockWrite:
		// No lock file at all means the stack has not adopted the pin;
		// see StackLock.Existed. LockWrite still records, which is how
		// the file comes to exist.
		if !lock.Existed() && p.Mode == LockVerify {
			return nil
		}
		if hasLock {
			return CheckLocked(p.Stack, dep.Name, locked, dep.URL, hash)
		}
		// Declared and not locked. LockWrite fills it in, which is how a
		// lock file comes to exist at all. LockVerify refuses, because
		// accepting an unlocked dependency silently would make the
		// verify mode mean nothing on exactly the stack that has never
		// been planned.
		if p.Mode == LockWrite {
			lock.Set(p.Stack, dep.Name, LockEntry{Source: dep.URL, ContentHash: hash})
			return nil
		}
		return fmt.Errorf("%w: %q in stack %q resolved to %s, and %s has no entry for it -- run `ubx plan` to record it",
			ErrLockMissingEntry, dep.Name, p.Stack, hash, StackLockFileName)
	}
	return nil
}

// finishLockPolicy is the whole-invocation half: prune entries for
// blueprints no longer declared, write the file when the mode writes,
// and add the receipt lines that make both visible.
func finishLockPolicy(p LockPolicy, lock *StackLock, deps []PyDependency, superseded []string) ([]string, error) {
	var notes []string
	for _, name := range superseded {
		notes = append(notes, fmt.Sprintf("note: %q is declared in both .ubx/config's [blueprints] table and requirements.txt -- the table wins", name))
	}
	if p.Mode != LockWrite && p.Mode != LockUpdate {
		return notes, nil
	}

	declared := make(map[string]string, len(deps))
	for _, d := range deps {
		declared[d.Name] = d.URL
	}
	for _, name := range lock.Prune(p.Stack, declared) {
		notes = append(notes, fmt.Sprintf("dropped %s from %s: stack %q no longer declares it", name, StackLockFileName, p.Stack))
	}
	return notes, lock.Save(p.LedgerDir)
}

// resolveOnePyDependency pulls+verifies (or reuses a cache hit for) one
// declared dependency, mirroring provider.Acquire's own cache discipline
// (provider/cache.go): once verified, always verified -- a cache hit is
// never re-pulled or re-verified from the network. Unlike a provider
// binary, a blueprint has no registry-signed version to trust before
// ever pulling, so the cache is keyed by the declared spec itself
// (name+URL, hashed) rather than a pre-known content hash; Verify (run on
// every hit, cached or fresh) is what actually establishes trust.
func resolveOnePyDependency(ctx context.Context, dep PyDependency, lockedHash string) (PyDepMount, error) {
	if dep.isLocalSource() {
		return resolveLocalPyDependency(ctx, dep)
	}

	// A locked hash makes the cache content-addressed; without one the
	// spec-keyed cache is unchanged from before this existed, so an
	// unlocked stack behaves exactly as it did.
	cacheDir, err := pyDepCacheDir(dep)
	if lockedHash != "" {
		cacheDir, err = blueprintCacheDirByHash(lockedHash)
	}
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

	pyDir, err := pyMountDir(dir)
	if err != nil {
		return PyDepMount{}, err
	}

	cacheNote := ""
	if fromCache {
		cacheNote = " (cached)"
	}
	receipt := fmt.Sprintf("pulled %s @ %s%s, verified: content hash %s matches (%d file(s))",
		dep.Name, dep.URL, cacheNote, manifest.ContentHash, len(manifest.Files))

	return PyDepMount{Dep: dep, HostDir: pyDir, Receipt: receipt, ContentHash: manifest.ContentHash, Ref: dep.Name + ":" + manifest.ContentHash}, nil
}

// pyMountDir picks the directory that goes on the guest's PYTHONPATH.
//
// Two blueprint models, two shapes on disk. An Ubxfile blueprint is
// BUILT, and its build writes a Python package under py/. A blueprint
// written as code is not built and has no py/ at all: its source IS the
// package, so the directory itself is what goes on the path, and the
// module a caller imports is the entry file's own basename. Schema's
// own Entrypoint.PyModule records exactly that contract already.
//
// Requiring py/ for both refused every Python code blueprint, and sent
// the reader to `ubx blueprint build`, which correctly refuses a code
// blueprint in turn ("a blueprint written as code is not built, it IS
// the package"). The two messages pointed at each other, leaving a
// colocated copy as the only thing that worked and that records no
// provenance at all (UBI-265).
func pyMountDir(dir string) (string, error) {
	pyDir := filepath.Join(dir, "py")
	if info, err := os.Stat(pyDir); err == nil && info.IsDir() {
		return pyDir, nil
	}

	schema, ok, err := ReadSchema(dir)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("has no built py/ package and no %s -- an Ubxfile blueprint must be built with `ubx blueprint build` (lang: py or all) before it can be used as a Python dependency", SchemaFileName)
	}

	// A blueprint written as code is single-language by construction
	// (Entrypoint.Language's own doc comment). Naming the language it IS
	// written in matters more than naming the one it is not: the usual
	// cause is a requirements.txt entry pointing at the wrong blueprint,
	// and that is only visible from the answer.
	if lang := schema.Entrypoint.Language; lang != "py" {
		if lang == "" {
			return "", fmt.Errorf("has no built py/ package, and its %s names no language, so there is nothing to import from Python", SchemaFileName)
		}
		return "", fmt.Errorf("is written in %s, not py, so it cannot be used as a Python dependency -- a blueprint written as code is single-language", lang)
	}

	// The module a Python caller imports has to actually be there.
	// Without this the failure surfaces inside the sandbox as a bare
	// ModuleNotFoundError naming a module the caller never chose.
	mod := schema.Entrypoint.PyModule
	if mod == "" {
		return "", fmt.Errorf("is written in py but its %s names no module to import (entrypoint.py_module is empty) -- re-run `ubx blueprint package` on it", SchemaFileName)
	}
	if _, err := os.Stat(filepath.Join(dir, mod+".py")); err != nil {
		if _, perr := os.Stat(filepath.Join(dir, mod, "__init__.py")); perr != nil {
			return "", fmt.Errorf("declares python module %q in its %s, but neither %s.py nor %s/__init__.py is present in the package", mod, SchemaFileName, mod, mod)
		}
	}

	return dir, nil
}

// pyDepCacheDir returns the local cache directory for dep, keyed by its
// own declared spec (name+URL, hashed) -- ~/.ubx/blueprints/by-spec/<hex>,
// the same "~/.ubx/<kind>/..." cache-root convention provider/cache.go's
// own defaultCacheRoot (~/.ubx/providers) already established.
// blueprintCacheDirByHash is the content-addressed cache directory for a
// blueprint whose content hash is already known from the lock file.
//
// pyDepCacheDir's own doc comment below explains why the spec-keyed
// cache exists: "a blueprint has no registry-signed version to trust
// before ever pulling, so the cache is keyed by the declared spec
// itself". The lock file IS that pre-known hash, so wherever one exists
// the reason no longer holds.
//
// It matters for correctness, not just tidiness. A spec-keyed hit is
// keyed on the declaration STRING, and re-verifies the cached directory
// against its own manifest, which is self-consistency and never a
// re-check against the registry. So a mutable tag repointed upstream
// left every warm machine on the old content indefinitely while a cold
// machine silently got the new content, and both verified. Keyed by
// hash, a cache hit means "this is the content the lock names", which is
// the question actually being asked.
//
// Two stacks pinning the same content through different tags also share
// one entry, which the spec-keyed layout could not do.
func blueprintCacheDirByHash(contentHash string) (string, error) {
	root, err := defaultBlueprintCacheRoot()
	if err != nil {
		return "", err
	}
	// The hash is "sha256:<hex>"; ":" is legal in a path segment on the
	// platforms ubx targets, but avoiding it costs nothing and keeps the
	// directory copy-pasteable on any of them.
	return filepath.Join(root, "by-hash", strings.ReplaceAll(contentHash, ":", "-")), nil
}

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
// the CLI layer to print, and (UBI-126) a name -> "name:content_hash" ref
// map -- every declared dependency's own real content hash, already
// computed as a byproduct of resolving it above, for
// StampDirectCallProvenancePy (below) to complete any incomplete
// "blueprint" source the evaluated program's own
// sdk.push_blueprint_source call may have left behind. Empty (never nil)
// when entryFile has no requirements.txt at all -- the common case pays
// nothing extra to build or consult this map.
func EvaluatePythonWithDeps(ctx context.Context, entryFile string) (canon []byte, receipts []string, refs map[string]string, err error) {
	mounts, notes, err := ResolvePyDependencies(ctx, entryFile)
	if err != nil {
		return nil, nil, nil, err
	}
	receipts = make([]string, 0, len(mounts)+len(notes))
	refs = map[string]string{}

	// UBI-266: every blueprint whose code this program can reach, from
	// both directions. A declared dependency is already resolved above,
	// name and hash included. A blueprint sitting inside the program's
	// own tree has no declaration anywhere, so it is found by walking.
	var roots []pyeval.BlueprintRoot
	for _, m := range mounts {
		receipts = append(receipts, m.Receipt)
		refs[m.Dep.Name] = m.Ref
		roots = append(roots, pyeval.BlueprintRoot{HostDir: m.HostDir, Name: m.Dep.Name})
	}
	// Whole-resolution notes print alongside the per-dependency
	// receipts, after them, so the list reads as "here is what was
	// pulled, and here is what else changed".
	receipts = append(receipts, notes...)
	local, err := DiscoverPyBlueprintRoots(entryFile)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, r := range local {
		if _, declared := refs[r.Name]; declared {
			// A declared dependency wins over a copy of the same
			// blueprint sitting in the tree: the declared one is what was
			// pulled and verified, and it is the one actually on
			// PYTHONPATH ahead of the program's own directory.
			continue
		}
		refs[r.Name] = r.Ref
		roots = append(roots, pyeval.BlueprintRoot{HostDir: r.Dir, Name: r.Name})
	}

	canon, err = pyeval.EvaluateWithBlueprintRoots(ctx, entryFile, roots, pyEvalDeps(mounts)...)
	if err != nil {
		return nil, nil, nil, err
	}
	return canon, receipts, refs, nil
}

// StampDirectCallProvenancePy is StampDirectCallProvenance's own Python
// sibling (sdkprovenance.go's own doc comment has the shared design
// account) -- but needs no discovery step of its own at all, unlike Go's
// `go list -m all`/TS's `deno info`: refs (EvaluatePythonWithDeps, above)
// already names every requirements.txt-declared blueprint dependency's
// real content hash, computed as a byproduct of resolving it BEFORE the
// script ever ran. A Python program that instead colocates a blueprint's
// built py/ package as a bare subdirectory of its own, with no
// requirements.txt entry naming it at all -- Python's own calling
// convention before UBI-130 existed, still mechanically possible since
// pyeval mounts the entry file's own whole directory tree -- has no
// entry in refs and gets the same clear, named refusal Go's own
// "standalone published module, not supported yet" boundary does, rather
// than a silent, permanently-incomplete ref: a real, honest limitation,
// not a gap this fix pretends to close.
func StampDirectCallProvenancePy(intent *resolver.IntentFile, refs map[string]string) error {
	if len(pendingBlueprintNames(intent)) == 0 {
		return nil
	}
	hint := "no requirements.txt entry (the \"<name> @ <url>\" syntax, UBI-130) declares a blueprint dependency with that name -- this works for a blueprint declared that way (local, git, or OCI source, all uniformly); a blueprint whose py/ package is imported via a bare colocated copy with no requirements.txt entry isn't supported yet"
	return applyBlueprintRefs(intent, refs, hint)
}
