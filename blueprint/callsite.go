// callsite.go is UBI-266's own mechanism: attributing a resource to the
// blueprint that created it by WHERE THE CALL CAME FROM, rather than by
// an in-band marker the blueprint has to push for itself.
//
// The mechanism it replaces works only for a built blueprint. An
// Ubxfile blueprint's generated wrapper calls PushBlueprintSource(name)
// around its own body (gogen.go/tsgen.go/pygen.go), so every
// sdk.Resource() call inside it carries a bare blueprint name that
// StampDirectCallProvenance* later completes into a real content hash.
// A blueprint written as code has no generated wrapper: it is a
// hand-written function, the as-code model's whole point is that
// nothing is declared twice, and so nothing pushes. Every resource it
// created reached the ledger with no source at all, in all three
// languages.
//
// Asking authors to write the push themselves was rejected: a marker
// that can be forgotten produces a ledger that is silently incomplete,
// which is worse than one that is visibly empty. Generating a wrapper
// at package time was rejected too, since it puts generated code back
// into the model whose selling point is that there is none.
//
// So the runtime is told, before it runs, which code belongs to which
// blueprint, and it attributes each resource to the innermost frame
// that lies inside one of them. ubx already discovers those roots and
// already hashes them; this hands the same answer to the runtime rather
// than only using it afterwards.
//
// The channel is per-language on purpose. Both the Go and TypeScript
// evaluators scrub the environment deliberately (goeval sets an empty
// cmd.Env, tseval passes --deny-env), and "no environment leakage" is
// this project's own determinism rule, so an env var would buy
// convenience by spending a hermeticity property. What each language
// gets instead is the channel it already has: Go a linker flag, TS the
// runner script the evaluator already generates per evaluation, Python
// a scratch module on the guest's own path.
package blueprint

import (
	"context"
	"encoding/base64"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/goeval"
	"github.com/ubiquex/ubiquex/tseval"
)

// BlueprintRoot is one blueprint a program can reach, as both halves of
// what the two sides need: Match for the runtime to recognize a call
// site, and Ref for the host to stamp the real content hash afterwards.
type BlueprintRoot struct {
	// Match is what a runtime compares a call site against. Its meaning
	// is fixed per language and is the only part of this contract a
	// runtime implementation has to get right:
	//
	//   Go     a package import-path prefix, compared against a frame's
	//          own fully-qualified function name
	//   TS     a file:// URL prefix, compared against a stack frame's
	//          own source URL
	//   Python a guest absolute path prefix, compared against a frame's
	//          own co_filename
	//
	// One kind per language, never mixed, so a runtime never has to
	// branch on what an entry means.
	Match string `json:"match"`

	// Name is the blueprint's own declared name, the bare form a
	// resource's source carries until applyBlueprintRefs completes it.
	Name string `json:"name"`

	// Dir and Ref are host-side only and never cross to the runtime:
	// Dir is the blueprint root on disk, Ref the completed
	// "<name>:sha256:<hex>" the stamping pass needs.
	Dir string `json:"-"`
	Ref string `json:"-"`
}

// BlueprintRootManifest is what a runtime is handed: Match and Name
// only, sorted, canonical.
func BlueprintRootManifest(roots []BlueprintRoot) ([]byte, error) {
	// Sorted and canonical because this string reaches a Go binary as a
	// linker flag, which makes it part of that binary's own bytes. An
	// unstable ordering would make two builds of the same program differ,
	// and determinism is not something to spend on a map iteration.
	trimmed := make([]BlueprintRoot, 0, len(roots))
	for _, r := range roots {
		trimmed = append(trimmed, BlueprintRoot{Match: r.Match, Name: r.Name})
	}
	sort.Slice(trimmed, func(i, j int) bool {
		if trimmed[i].Match != trimmed[j].Match {
			return trimmed[i].Match < trimmed[j].Match
		}
		return trimmed[i].Name < trimmed[j].Name
	})

	raw, err := core.CanonicalJSON(trimmed)
	if err != nil {
		return nil, fmt.Errorf("encode blueprint root manifest: %w", err)
	}
	return raw, nil
}

// EncodeBlueprintRootManifest is BlueprintRootManifest wrapped in
// base64, the form the Go evaluator passes through `go build -ldflags
// -X`.
//
// Base64 rather than the raw JSON because `go build` splits an -ldflags
// value on spaces itself, and quoting rules from there differ by
// platform. The encoded form has no spaces, no quotes and no
// backslashes, so it survives every layer unchanged. A trailing "="
// from padding is harmless: -X splits its argument on the FIRST "=".
func EncodeBlueprintRootManifest(roots []BlueprintRoot) (string, error) {
	raw, err := BlueprintRootManifest(roots)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// blueprintRefs turns discovered roots into the name -> "<name>:<hash>"
// map applyBlueprintRefs completes a document with.
func blueprintRefs(roots []BlueprintRoot) map[string]string {
	refs := make(map[string]string, len(roots))
	for _, r := range roots {
		refs[r.Name] = r.Ref
	}
	return refs
}

// EvaluateGoWithBlueprints is the Go half of UBI-266's calling
// sequence: discover which blueprints this program can reach, hand
// their roots to the runtime, evaluate, and complete every bare
// blueprint name the run produced.
//
// Discovery moved BEFORE evaluation, where it used to run only after.
// It has to: the runtime cannot attribute a call to a blueprint it was
// never told about. The same discovery result serves both ends, so the
// module graph is still walked exactly once per evaluation.
func EvaluateGoWithBlueprints(ctx context.Context, entryFile string) ([]byte, []string, map[string]string, error) {
	// Declared blueprints first: they are fetched, verified and mounted
	// before the program runs, exactly as Python's have been. Their roots
	// join the discovered ones, so a resource created inside a declared
	// blueprint is attributed the same way one inside an imported
	// directory already was.
	declared, notes, err := ResolveGoDependencies(ctx, entryFile)
	if err != nil {
		return nil, nil, nil, err
	}
	var useDirs []string
	declaredRoots := make([]BlueprintRoot, 0, len(declared))
	receipts := make([]string, 0, len(declared)+len(notes))
	for _, d := range declared {
		useDirs = append(useDirs, d.Dir)
		receipts = append(receipts, d.Receipt)
		declaredRoots = append(declaredRoots, BlueprintRoot{
			Match: d.ModulePath,
			Name:  d.Dep.Name,
			Dir:   d.Dir,
			Ref:   d.Ref,
		})
	}
	receipts = append(receipts, notes...)

	roots, err := DiscoverGoBlueprintRoots(ctx, entryFile)
	if err != nil {
		// Discovery failing is not a reason to refuse to evaluate. A
		// program with no blueprint in sight still has a `go list` that
		// can fail for its own unrelated reasons, and the stamping pass
		// below is what refuses when a name genuinely cannot be resolved.
		canon, evalErr := goeval.EvaluateWithBlueprints(ctx, entryFile, mustEncodeRoots(declaredRoots), useDirs)
		return canon, receipts, blueprintRefs(declaredRoots), evalErr
	}
	roots = append(roots, declaredRoots...)

	encoded, err := EncodeBlueprintRootManifest(roots)
	if err != nil {
		return nil, nil, nil, err
	}

	canon, err := goeval.EvaluateWithBlueprints(ctx, entryFile, encoded, useDirs)
	if err != nil {
		return nil, nil, nil, err
	}
	return canon, receipts, blueprintRefs(roots), nil
}

// mustEncodeRoots encodes a manifest for the degraded path, where
// discovery already failed and an encoding error would replace one
// unhelpful outcome with another. An empty manifest disables attribution,
// which is what that path produced before declared blueprints existed.
func mustEncodeRoots(roots []BlueprintRoot) string {
	if len(roots) == 0 {
		return ""
	}
	encoded, err := EncodeBlueprintRootManifest(roots)
	if err != nil {
		return ""
	}
	return encoded
}

// EvaluateTSWithBlueprints is EvaluateGoWithBlueprints' TypeScript
// sibling, the same sequence against the same contract: discover, hand
// the roots to the runtime, evaluate, complete the bare names.
func EvaluateTSWithBlueprints(ctx context.Context, entryFile string) ([]byte, []string, map[string]string, error) {
	declared, notes, err := ResolveTSDependencies(ctx, entryFile)
	if err != nil {
		return nil, nil, nil, err
	}
	imports := make([]tseval.BlueprintImport, 0, len(declared))
	declaredRoots := make([]BlueprintRoot, 0, len(declared))
	receipts := make([]string, 0, len(declared)+len(notes))
	for _, d := range declared {
		imports = append(imports, tseval.BlueprintImport{
			Specifier: d.Specifier,
			EntryFile: d.EntryFile,
			Dir:       d.Dir,
			Imports:   d.Imports,
		})
		receipts = append(receipts, d.Receipt)
		// Match is the blueprint ROOT's URL, not the entry file's: a
		// blueprint of several modules has frames from all of them, and
		// the root is the prefix they share. Same value
		// DiscoverTSBlueprintRoots builds for an imported directory.
		declaredRoots = append(declaredRoots, BlueprintRoot{
			Match: (&url.URL{Scheme: "file", Path: d.Dir}).String(),
			Name:  d.Dep.Name,
			Dir:   d.Dir,
			Ref:   d.Ref,
		})
	}
	receipts = append(receipts, notes...)

	roots, err := DiscoverTSBlueprintRoots(ctx, entryFile)
	if err != nil {
		manifest, _ := BlueprintRootManifest(declaredRoots)
		canon, evalErr := tseval.EvaluateWithBlueprints(ctx, entryFile, string(manifest), imports)
		return canon, receipts, blueprintRefs(declaredRoots), evalErr
	}
	roots = append(roots, declaredRoots...)

	manifest, err := BlueprintRootManifest(roots)
	if err != nil {
		return nil, nil, nil, err
	}

	canon, err := tseval.EvaluateWithBlueprints(ctx, entryFile, string(manifest), imports)
	if err != nil {
		return nil, nil, nil, err
	}
	return canon, receipts, blueprintRefs(roots), nil
}

// DiscoverPyBlueprintRoots finds blueprints sitting inside the entry
// program's OWN directory tree.
//
// Python is the one language with no module graph to walk. Go has `go
// list -m all` and TypeScript has `deno info`; a Python program reaches
// a colocated blueprint by putting a directory on sys.path, which
// leaves no declaration anywhere for a host to read. A declared
// dependency is covered by ResolvePyDependencies, which already knows
// exactly what it pulled; this covers the other shape, which is the one
// that used to record nothing at all.
//
// The walk is bounded to the entry file's own directory, and stops
// descending into a blueprint once it finds one: a blueprint's own
// internals are part of that blueprint, not further blueprints. It also
// never enters a virtualenv or a VCS directory, which are large,
// uninteresting, and exactly where a recursive walk goes to die.
func DiscoverPyBlueprintRoots(entryFile string) ([]BlueprintRoot, error) {
	absEntry, err := filepath.Abs(entryFile)
	if err != nil {
		return nil, err
	}
	entryDir := filepath.Dir(absEntry)

	var roots []BlueprintRoot
	seen := map[string]bool{}
	err = filepath.WalkDir(entryDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subdirectory is not a reason to refuse to
			// evaluate. Attribution only ever ADDS provenance, and the
			// stamping pass is what refuses a name it cannot resolve.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case ".git", ".venv", "venv", "node_modules", "__pycache__":
			return fs.SkipDir
		}
		if path == entryDir || !IsBlueprintDir(path) {
			return nil
		}
		name := blueprintNameAt(path)
		if seen[name] {
			return fs.SkipDir
		}
		seen[name] = true
		manifest, err := buildManifest(path, name)
		if err != nil {
			return fmt.Errorf("hash blueprint %q at %s: %w", name, path, err)
		}
		roots = append(roots, BlueprintRoot{
			// Match is filled in by pyeval, which is what assigns every
			// guest path and so is the only thing that can say what a
			// frame's co_filename will actually be.
			Name: name,
			Dir:  path,
			Ref:  name + ":" + manifest.ContentHash,
		})
		return fs.SkipDir
	})
	if err != nil {
		return nil, err
	}
	return roots, nil
}

// StampDirectCallProvenanceRefs completes every pending blueprint source
// from an already-computed ref map, for any language.
//
// The Go and TypeScript paths used to re-run discovery here, which was
// correct while discovery was the only way a blueprint could be reached.
// It is not any more: a declared blueprint is resolved before the
// program runs and never appears in the program's own module graph, so a
// second discovery pass cannot see it and refuses a name the evaluation
// already resolved.
//
// EvaluateGoWithBlueprints and EvaluateTSWithBlueprints already return
// the merged map, declared and discovered together, so stamping from it
// is both correct and one pass rather than two.
func StampDirectCallProvenanceRefs(intent *resolver.IntentFile, refs map[string]string, lang string) error {
	if len(pendingBlueprintNames(intent)) == 0 {
		return nil
	}
	hint := "no blueprint of that name was declared in .ubx/config's [blueprints] table, and none was found in this program's own imports"
	switch lang {
	case "go":
		hint += " (an imported Go module whose directory, or its parent, is a blueprint root)"
	case "ts":
		hint += " (an imported file inside a blueprint directory)"
	}
	return applyBlueprintRefs(intent, refs, hint)
}
