package blueprint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// deps.go is the language-neutral half of blueprint dependency
// resolution: read the declaration, resolve it through the lock, pull
// and verify into the content store. What each language then MOUNTS from
// the resulting directory is the only part that differs, and it is the
// last step rather than the whole pipeline.
//
// Python had all of this to itself. A TypeScript or Go stack had no
// declaration to read, so nothing was ever fetched for it, so an author
// pulled the blueprint by hand and carried a relative import into the
// pulled directory. Discovery then walked the module graph afterwards to
// find out what had been imported, which is why provenance worked while
// distribution did not.
//
// Generalising rather than adding a second mechanism is the point. The
// lock, the content store, the receipts and the precedence rule are one
// implementation with three thin adapters, not three implementations
// that have to agree.

// ResolvedDep is one declared blueprint, pulled and verified, before any
// language has decided what to import from it.
type ResolvedDep struct {
	Dep      Declaration
	Dir      string // the verified content-store directory
	Manifest *Manifest
	Receipt  string
	// Ref is the complete "<name>:<content_hash>" provenance ref.
	Ref         string
	ContentHash string
	FromCache   bool
}

// Declaration is one blueprint a stack declares: a name and a source in
// any of the four forms `ubx blueprint pull` accepts.
//
// An alias rather than a new type. The shape was introduced for
// requirements.txt and named for it, and it was already language-neutral
// in everything but its name: a name, a URL, and the three arguments
// Pull takes. Renaming the underlying type would churn every existing
// Python test for no behaviour change, so the neutral spelling is
// introduced here and used by the neutral code.
type Declaration = PyDependency

// resolveDeclaredBlueprints is the shared pipeline every language uses.
//
// entryDir is the directory holding the program, which is where a
// requirements.txt would be. The [blueprints] table arrives on the
// context, through the lock policy, because it belongs to the invocation
// rather than to the file.
func resolveDeclaredBlueprints(ctx context.Context, entryDir string) ([]ResolvedDep, []string, error) {
	fromRequirements, err := ParsePyDependencies(entryDir)
	if err != nil {
		return nil, nil, err
	}

	policy := lockPolicyFrom(ctx)
	deps, superseded := mergeDeclaredBlueprints(policy.Declared, fromRequirements)

	lock, err := loadLockForPolicy(policy)
	if err != nil {
		return nil, nil, err
	}

	resolved := make([]ResolvedDep, 0, len(deps))
	for _, dep := range deps {
		locked, hasLock := lock.Entry(policy.Stack, dep.Name)

		// The locked hash is the content-store key, so a locked stack
		// resolves by content rather than by declaration string. Only
		// while the source still matches: a changed declaration has to
		// reach CheckLocked's own message rather than being quietly
		// served the old content.
		expect := ""
		if hasLock && locked.Source == dep.URL && policy.Mode != LockUpdate {
			expect = locked.ContentHash
		}

		r, err := resolveOne(ctx, dep, expect)
		if err != nil {
			return nil, nil, fmt.Errorf("blueprint dependency %q (%s @ %s): %w", dep.Name, dep.Name, dep.URL, err)
		}
		if err := applyLockPolicyResolved(policy, lock, dep, r, hasLock, locked); err != nil {
			return nil, nil, err
		}
		resolved = append(resolved, r)
	}

	notes, err := finishLockPolicy(policy, lock, deps, superseded)
	if err != nil {
		return nil, nil, err
	}
	return resolved, notes, nil
}

// resolveOne pulls (or reuses) and verifies one declaration, requiring
// the pulled blueprint to be the one the declaration named.
func resolveOne(ctx context.Context, dep Declaration, expectHash string) (ResolvedDep, error) {
	return resolveOneNamed(ctx, dep, expectHash, true)
}

// resolveOneAdoptingName is resolveOne for a caller that DECLARED no
// name and derived one instead.
//
// An HCL blueprint call is the case. Its name comes from the source's
// own last path segment, so comparing that against the blueprint's own
// packaged name cannot catch a wrong blueprint under an expected name,
// which is the check's whole purpose: there is no independently stated
// expectation to violate. It can only fire when a repository or artifact
// path is spelled differently from the blueprint inside it, which is not
// an error and which the old HCL path never objected to.
//
// So the blueprint's own name is adopted rather than checked, which also
// makes it the identity used for the lock entry and the generated
// caller, both of which want the blueprint's real name rather than a
// path fragment.
func resolveOneAdoptingName(ctx context.Context, dep Declaration, expectHash string) (ResolvedDep, error) {
	return resolveOneNamed(ctx, dep, expectHash, false)
}

func resolveOneNamed(ctx context.Context, dep Declaration, expectHash string, requireName bool) (ResolvedDep, error) {
	var (
		dir       string
		manifest  *Manifest
		fromCache bool
		err       error
	)
	if dep.isLocalSource() {
		dir, manifest, err = pullLocalDependency(ctx, dep)
	} else {
		dir, manifest, fromCache, err = fetchIntoContentStore(ctx, dep, expectHash)
	}
	if err != nil {
		return ResolvedDep{}, err
	}
	if requireName && manifest.Name != dep.Name {
		return ResolvedDep{}, fmt.Errorf("declared name %q doesn't match the pulled blueprint's own declared name %q -- check the declaration", dep.Name, manifest.Name)
	}
	if !requireName && manifest.Name != "" {
		dep.Name = manifest.Name
	}

	cacheNote := ""
	if fromCache {
		cacheNote = " (cached)"
	}
	return ResolvedDep{
		Dep:         dep,
		Dir:         dir,
		Manifest:    manifest,
		FromCache:   fromCache,
		ContentHash: manifest.ContentHash,
		Ref:         dep.Name + ":" + manifest.ContentHash,
		Receipt: fmt.Sprintf("pulled %s @ %s%s, verified: content hash %s matches (%d file(s))",
			dep.Name, dep.URL, cacheNote, manifest.ContentHash, len(manifest.Files)),
	}, nil
}

// TSDepMount is one blueprint a TypeScript program can import by name.
type TSDepMount struct {
	ResolvedDep
	// Specifier is the bare name the program imports.
	Specifier string
	// EntryFile is the absolute path the import map points at.
	EntryFile string
	// Imports is the blueprint's OWN declared imports, absolutised, to
	// be applied in a scope covering only this blueprint's directory.
	Imports map[string]string

	// EvalDir is the directory the blueprint is evaluated FROM, which is
	// its content-store directory unless it has npm dependencies, in
	// which case it is the mirror holding their node_modules
	// (tslock.go).
	//
	// A separate field rather than reassigning ResolvedDep.Dir: that one
	// means "the verified content", and the lock, the receipts and the
	// provenance ref all rest on it meaning exactly that. This one means
	// "where the module graph is rooted", which is the same directory
	// only when nothing had to be materialised.
	EvalDir string
}

// ResolveTSDependencies resolves every declared blueprint and reports
// what each one contributes to the import map.
//
// A bare specifier rather than a relative path into a pulled directory,
// which is the whole point: tseval already generates an import map,
// merges the project's own into it, writes it to a temp file and passes
// it with --import-map. Adding entries there touches nothing the project
// or npm owns.
func ResolveTSDependencies(ctx context.Context, entryFile string) ([]TSDepMount, []string, error) {
	resolved, notes, err := resolveDeclaredBlueprints(ctx, filepath.Dir(entryFile))
	if err != nil {
		return nil, nil, err
	}
	mounts := make([]TSDepMount, 0, len(resolved))
	for _, r := range resolved {
		entry, err := tsMountFile(r.Dir)
		if err != nil {
			return nil, nil, fmt.Errorf("blueprint dependency %q: %w", r.Dep.Name, err)
		}
		own, err := tsBlueprintImports(r.Dir, r.Dep.Name)
		if err != nil {
			return nil, nil, err
		}
		// Fetch-and-verify before evaluation, never during it. deno takes
		// one --lock per invocation, so the blueprint's own lock can only
		// be enforced in an invocation of its own (tslock.go).
		evalDir := r.Dir
		if own.NeedsPrefetch {
			mirror, err := materializeBlueprintDeps(ctx, r.Dir, r.ContentHash, r.Dep.Name)
			if err != nil {
				return nil, nil, err
			}
			// Evaluate from the mirror, so the entry file has the
			// blueprint's node_modules beside it. Pointing at the store
			// would put the module graph one directory away from its own
			// dependencies, which is the bug this fixes.
			evalDir = mirror
			if rel, err := filepath.Rel(r.Dir, entry); err == nil {
				entry = filepath.Join(mirror, rel)
			}
		}
		mounts = append(mounts, TSDepMount{
			ResolvedDep: r,
			Specifier:   r.Dep.Name,
			EntryFile:   entry,
			Imports:     own.Imports,
			EvalDir:     evalDir,
		})
	}
	return mounts, notes, nil
}

// GoDepMount is one blueprint a Go program can import by module path.
type GoDepMount struct {
	ResolvedDep
	// ModulePath is what the program imports.
	ModulePath string
	// Dir is the module directory the workspace adds with `use`.
	Dir string
}

// ResolveGoDependencies resolves every declared blueprint and reports
// what each one contributes to the build workspace.
//
// A workspace `use` entry rather than a require/replace pair written
// into the author's go.mod. goeval already synthesizes a go.work in its
// build directory (to carry a program's own workspace through the module
// copy), so a blueprint is one more `use` in a file ubx already owns and
// the author's go.mod is never touched.
//
// `use` rather than `replace` deliberately: a replace only redirects a
// module something already requires, and a stack that declares a
// blueprint in config has no require for it. A workspace module is
// importable without one.
func ResolveGoDependencies(ctx context.Context, entryFile string) ([]GoDepMount, []string, error) {
	resolved, notes, err := resolveDeclaredBlueprints(ctx, filepath.Dir(entryFile))
	if err != nil {
		return nil, nil, err
	}
	mounts := make([]GoDepMount, 0, len(resolved))
	for _, r := range resolved {
		dir, modPath, err := goMountDir(r.Dir)
		if err != nil {
			return nil, nil, fmt.Errorf("blueprint dependency %q: %w", r.Dep.Name, err)
		}
		// Refuse an unpinned blueprint before fetching anything, and
		// fetch a pinned one before evaluation reaches it. The evaluator
		// builds with GOPROXY=off, so the modules have to be on disk
		// already (godeps.go).
		if err := checkGoBlueprintPinned(dir, r.Dep.Name); err != nil {
			return nil, nil, err
		}
		if err := prefetchGoBlueprintDeps(ctx, dir, r.Dep.Name); err != nil {
			return nil, nil, err
		}
		mounts = append(mounts, GoDepMount{ResolvedDep: r, ModulePath: modPath, Dir: dir})
	}
	return mounts, notes, nil
}

// tsMountFile picks the file a TypeScript caller's bare specifier
// resolves to.
//
// Two blueprint models, two shapes on disk, exactly as pyMountDir
// describes for Python. A built blueprint's build wrote ts/<pkg>.ts. A
// blueprint written as code is not built, and its schema names the entry
// file relative to the blueprint root.
func tsMountFile(dir string) (string, error) {
	if schema, ok, err := ReadSchema(dir); err != nil {
		return "", err
	} else if ok {
		if lang := schema.Entrypoint.Language; lang != "ts" {
			if lang == "" {
				return "", fmt.Errorf("has no built ts/ package and its %s names no language, so there is nothing to import from TypeScript", SchemaFileName)
			}
			return "", fmt.Errorf("is written in %s, not ts, so it cannot be used as a TypeScript dependency -- a blueprint written as code is single-language", lang)
		}
		if schema.Entrypoint.TSEntry == "" {
			return "", fmt.Errorf("is written in ts but its %s names no entry file (entrypoint.ts_entry is empty) -- re-run `ubx blueprint package` on it", SchemaFileName)
		}
		entry := filepath.Join(dir, schema.Entrypoint.TSEntry)
		if _, err := os.Stat(entry); err != nil {
			return "", fmt.Errorf("declares entry file %q in its %s, which is not present in the package", schema.Entrypoint.TSEntry, SchemaFileName)
		}
		return entry, nil
	}

	tsDir := filepath.Join(dir, "ts")
	if info, err := os.Stat(tsDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("has no built ts/ package and no %s -- an Ubxfile blueprint must be built with `ubx blueprint build` (lang: ts or all) before it can be used as a TypeScript dependency", SchemaFileName)
	}
	// A built blueprint's entry is ts/<pkg>.ts, named from the
	// blueprint's own name the same way its build named it.
	manifest, err := readManifest(dir)
	if err != nil {
		return "", err
	}
	pkg, err := packageIdent(manifest.Name)
	if err != nil {
		return "", err
	}
	entry := filepath.Join(tsDir, pkg+".ts")
	if _, err := os.Stat(entry); err != nil {
		return "", fmt.Errorf("has a built ts/ package with no %s.ts in it -- rebuild it with `ubx blueprint build`", pkg)
	}
	return entry, nil
}

// goMountDir picks the module directory a workspace `use` points at, and
// the module path the program imports.
func goMountDir(dir string) (moduleDir, modulePath string, err error) {
	if schema, ok, rerr := ReadSchema(dir); rerr != nil {
		return "", "", rerr
	} else if ok {
		if lang := schema.Entrypoint.Language; lang != "go" {
			if lang == "" {
				return "", "", fmt.Errorf("has no built go/ package and its %s names no language, so there is nothing to import from Go", SchemaFileName)
			}
			return "", "", fmt.Errorf("is written in %s, not go, so it cannot be used as a Go dependency -- a blueprint written as code is single-language", lang)
		}
		if schema.Entrypoint.GoModule == "" {
			return "", "", fmt.Errorf("is written in go but its %s names no module path (entrypoint.go_module is empty) -- re-run `ubx blueprint package` on it", SchemaFileName)
		}
		return dir, schema.Entrypoint.GoModule, nil
	}

	goDir := filepath.Join(dir, "go")
	if info, err := os.Stat(goDir); err != nil || !info.IsDir() {
		return "", "", fmt.Errorf("has no built go/ package and no %s -- an Ubxfile blueprint must be built with `ubx blueprint build` (lang: go or all) before it can be used as a Go dependency", SchemaFileName)
	}
	// The module path is whatever the built go.mod declares, read rather
	// than derived: the build wrote it, and re-deriving it here would be
	// a second rule that could disagree with the first.
	modPath, err := goModulePath(goDir)
	if err != nil {
		return "", "", err
	}
	return goDir, modPath, nil
}

// pullLocalDependency pulls a local-path dependency into a throwaway
// scratch directory, never the content store.
//
// A local path names something an author may be actively editing, so
// caching it under any key would serve stale content after an edit. That
// reasoning predates the content store and survives it unchanged: the
// content store answers "do I already have these bytes", and for a
// directory someone is editing the honest answer is always no.
func pullLocalDependency(ctx context.Context, dep Declaration) (string, *Manifest, error) {
	scratch, err := os.MkdirTemp("", "ubx-bpdep-local-*")
	if err != nil {
		return "", nil, err
	}
	dest := filepath.Join(scratch, "blueprint")
	if _, err := Pull(ctx, dep.Source, dest, dep.Ref, dep.Path); err != nil {
		return "", nil, fmt.Errorf("pull: %w", err)
	}
	manifest, err := Verify(dest)
	if err != nil {
		return "", nil, fmt.Errorf("verify: %w", err)
	}
	return dest, manifest, nil
}

// applyLockPolicyResolved is applyLockPolicy against a ResolvedDep.
//
// The two exist because the Python path still carries PyDepMount for its
// own callers; both hand the same content hash to the same rules.
func applyLockPolicyResolved(p LockPolicy, lock *StackLock, dep Declaration, r ResolvedDep, hasLock bool, locked LockEntry) error {
	return applyLockPolicy(p, lock, dep, PyDepMount{ContentHash: r.ContentHash}, hasLock, locked)
}
