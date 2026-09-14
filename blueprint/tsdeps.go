package blueprint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// tsdeps.go reads a TypeScript blueprint's OWN dependency declaration.
//
// A blueprint's deno.json travels: it is an ordinary file in the
// blueprint directory, so it is in the content store and covered by the
// content hash exactly as the source is. Nothing read it. ubx merged the
// CONSUMER's deno.json into the import map and never the blueprint's, so
// a blueprint importing anything by a bare specifier failed at
// evaluation:
//
//	error: Import "helper" not a dependency and not in import map from
//	".../blueprints/sha256/.../blueprint.ts"
//
// The error names the mechanism exactly. The import map IS the
// resolution path, and the entry was simply absent.
//
// UBI-265 recorded that TypeScript did not have this problem, on the
// reasoning that "the import map travels and JSR resolves from it". Half
// right, and the correct half is what makes this fixable: the
// declaration really does travel. What did not happen was ubx reading
// it.
//
// # The boundary
//
// Three outcomes, decided by what a target resolves THROUGH rather than
// by which file declared it:
//
//   - A LOCAL target (a relative path, or an npm "file:" dependency) is
//     resolved here, against the blueprint's own directory. No network,
//     no policy question.
//
//   - An NPM target is fetched, but only from a blueprint that ships a
//     deno.lock. The lock pins the bytes and travels under the content
//     hash, so the graph is pinned by the same mechanism that pins
//     everything else. `ubx blueprint package` generates one (tslock.go),
//     so this is the ordinary case rather than something an author opts
//     into; the refusal below exists for blueprints packaged before that
//     did.
//
//   - A JSR target is refused outright, lock or no lock, and this is the
//     limit worth knowing about. The evaluator runs deno with
//     --no-remote, which runner.go documents as the one flag that
//     actually closes the dynamic import("https://...") gap. It does not
//     block npm, but it does block jsr, because resolving a JSR package
//     fetches https://jsr.io/<pkg>/meta.json and that is a remote
//     specifier. Verified against a fully warm cache, so it is a
//     resolution-time rule and not a caching artifact:
//
//     error: JSR package manifest for '@std/encoding' failed to load. A
//     remote specifier was requested: "https://jsr.io/@std/encoding/
//     meta.json", but --no-remote is specified.
//
//     Lifting it means narrowing --no-remote, which is a security flag
//     with a deliberate rationale and not something to change as part of
//     a dependency-resolution fix (UBI-274).
//
// The lock requirement is on the PROPERTY that matters rather than on
// where a dependency came from: a registry specifier with a lock is
// pinned, the same specifier without one is not, and specifier kind
// cannot tell those apart.

const denoLockFileName = "deno.lock"

// tsImportKind is what a declared target resolves through, which is what
// the policy above is written in terms of.
type tsImportKind int

const (
	// tsImportLocal resolves to a file inside the blueprint.
	tsImportLocal tsImportKind = iota
	// tsImportNPM resolves through the npm registry.
	tsImportNPM
	// tsImportJSR resolves through jsr.io.
	tsImportJSR
	// tsImportWeb is a direct http(s) or VCS module URL.
	tsImportWeb
)

// tsDeclaredImport is one entry from a blueprint's own declaration,
// classified but not yet judged.
type tsDeclaredImport struct {
	Specifier string
	// Target is an absolute file:// URL for a local import and the
	// registry specifier verbatim for every other kind.
	Target string
	Kind   tsImportKind
	// Declared names the file it came from, for the refusal message.
	Declared string
}

// tsResolvedImports is what one blueprint contributes to the import map,
// plus whether honouring it needs a fetch first.
type tsResolvedImports struct {
	// Imports is specifier -> target, ready for the blueprint's own
	// scope in the merged import map.
	Imports map[string]string
	// NeedsPrefetch is true when at least one target resolves through a
	// registry, so the caller must run the fetch-and-verify pass before
	// evaluating (tslock.go).
	NeedsPrefetch bool
}

// tsBlueprintImports reads dir's own declaration and applies the
// boundary this file documents.
//
// A blueprint with no declaration has no imports of its own to resolve,
// which is the ordinary case for a built blueprint: its generated code
// imports "@ubx/sdk" and nothing else.
func tsBlueprintImports(dir, name string) (tsResolvedImports, error) {
	declared, err := tsDeclaredImports(dir, name)
	if err != nil {
		return tsResolvedImports{}, err
	}
	if len(declared) == 0 {
		return tsResolvedImports{}, nil
	}

	hasLock := tsBlueprintLockPath(dir) != ""

	out := tsResolvedImports{Imports: map[string]string{}}
	var jsr, web, unpinned []string
	for _, d := range declared {
		switch d.Kind {
		case tsImportLocal:
			out.Imports[d.Specifier] = d.Target
		case tsImportNPM:
			if !hasLock {
				unpinned = append(unpinned, d.Specifier+" -> "+d.Target+" (in "+d.Declared+")")
				continue
			}
			out.Imports[d.Specifier] = d.Target
			// A package's SUBPATHS need an entry of their own. An import
			// map matches a bare specifier exactly, so mapping only
			// "@ubx/sdk-aws" leaves "@ubx/sdk-aws/aws/sqs/queue"
			// unmatched: deno falls back to package.json resolution and
			// demands a node_modules tree that is not there.
			//
			//	Could not resolve "@ubx/sdk-aws/aws/sqs/queue", but found
			//	it in a package.json. Deno expects the node_modules/
			//	directory to be up to date.
			//
			// Missed because the first fixture imported left-pad, which
			// has no subpaths, so the whole mechanism was exercised
			// against the one shape that cannot show this.
			if prefix := npmPrefixTarget(d.Target); prefix != "" {
				out.Imports[d.Specifier+"/"] = prefix
			}
			out.NeedsPrefetch = true
		case tsImportJSR:
			jsr = append(jsr, d.Specifier+" -> "+d.Target+" (in "+d.Declared+")")
		case tsImportWeb:
			web = append(web, d.Specifier+" -> "+d.Target+" (in "+d.Declared+")")
		}
	}

	// JSR first: it is refused for a reason no author action can fix, so
	// reporting it under a "commit a lock" message would send them after
	// the wrong thing.
	if len(jsr) > 0 {
		sort.Strings(jsr)
		return tsResolvedImports{}, fmt.Errorf("%s", jsrRefusal(name, jsr))
	}
	if len(web) > 0 {
		sort.Strings(web)
		return tsResolvedImports{}, fmt.Errorf("blueprint %q declares %d import(s) from a direct http(s) or VCS URL, which the evaluator does not load:\n    %s\n  The evaluator runs with --no-remote, which exists to close the dynamic import(\"https://...\") gap (tseval/runner.go). A blueprint's dependencies have to come from a registry ubx can pin, or live inside the blueprint",
			name, len(web), strings.Join(web, "\n    "))
	}
	if len(unpinned) > 0 {
		sort.Strings(unpinned)
		return tsResolvedImports{}, fmt.Errorf("%s", unpinnedNPMRefusal(name, unpinned))
	}
	if len(out.Imports) == 0 {
		return tsResolvedImports{}, nil
	}
	return out, nil
}

// jsrRefusal explains a limit the author cannot fix, so it says so
// rather than suggesting an action.
func jsrRefusal(name string, jsr []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "blueprint %q declares %d import(s) that resolve through jsr.io, which ubx cannot evaluate:\n", name, len(jsr))
	for _, j := range jsr {
		fmt.Fprintf(&b, "    %s\n", j)
	}
	b.WriteString("  This is a limit of the evaluator rather than of the blueprint. Evaluation runs deno with --no-remote,\n")
	b.WriteString("  and resolving a JSR package fetches https://jsr.io/<pkg>/meta.json, which that flag blocks even when the\n")
	b.WriteString("  package is already cached. --no-remote is what closes the dynamic import(\"https://...\") gap, so it is not\n")
	b.WriteString("  something to narrow casually.\n")
	b.WriteString("  npm: dependencies DO work, from a blueprint that ships a " + denoLockFileName + ". Publishing the same code to\n")
	b.WriteString("  npm, or vendoring it into the blueprint, are the two paths that work today.")
	return b.String()
}

// unpinnedNPMRefusal is the path for blueprints packaged before ubx
// generated a lock for them.
//
// It names the command rather than the concept. An author who reached
// for npm has no reason to know what a deno.lock is, and the honest
// remedy is one command plus one committed file.
func unpinnedNPMRefusal(name string, unpinned []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "blueprint %q declares %d npm dependency(ies) and ships no %s, so nothing pins what would be fetched:\n", name, len(unpinned), denoLockFileName)
	for _, u := range unpinned {
		fmt.Fprintf(&b, "    %s\n", u)
	}
	fmt.Fprintf(&b, "  ubx fetches a blueprint's npm dependencies only when the blueprint pins them. A %s travels in the\n", denoLockFileName)
	b.WriteString("  content store and is covered by the content hash, so the dependency graph is pinned by the same mechanism\n")
	b.WriteString("  that pins the blueprint itself. Without one, the hash would cover the names and not the bytes that ran.\n")
	b.WriteString("  `ubx blueprint package` generates this lock, so re-packaging the blueprint is the fix. To do it by hand:\n")
	fmt.Fprintf(&b, "  run `deno install` in the blueprint and commit the %s it writes.\n", denoLockFileName)
	b.WriteString("  npm's own package-lock.json does not serve here: deno does not read it.")
	return b.String()
}

// tsBlueprintLockPath returns dir's own deno.lock, or "".
func tsBlueprintLockPath(dir string) string {
	p := filepath.Join(dir, denoLockFileName)
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return p
	}
	return ""
}

// tsDeclaredImports merges both declaration formats into one classified
// list.
//
// Both formats, one boundary. A blueprint authored with npm tooling has
// a package.json and no deno.json, and reading only the latter made the
// refusal inconsistent rather than narrow.
func tsDeclaredImports(dir, name string) ([]tsDeclaredImport, error) {
	fromDeno, err := tsDenoConfigImports(dir)
	if err != nil {
		return nil, err
	}
	fromNPM, err := tsPackageJSONImports(dir, name)
	if err != nil {
		return nil, err
	}

	// deno.json wins a collision: it is the format Deno itself prefers,
	// and a blueprint carrying both has usually adopted it over the
	// package.json that came first.
	byName := map[string]tsDeclaredImport{}
	for _, d := range fromNPM {
		byName[d.Specifier] = d
	}
	for _, d := range fromDeno {
		byName[d.Specifier] = d
	}
	if len(byName) == 0 {
		return nil, nil
	}
	out := make([]tsDeclaredImport, 0, len(byName))
	for _, d := range byName {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Specifier < out[j].Specifier })
	return out, nil
}

// tsDenoConfigImports reads dir's own deno.json imports.
func tsDenoConfigImports(dir string) ([]tsDeclaredImport, error) {
	configPath := ""
	for _, candidate := range []string{"deno.json", "deno.jsonc"} {
		p := filepath.Join(dir, candidate)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			configPath = p
			break
		}
	}
	if configPath == "" {
		return nil, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(configPath), err)
	}
	var doc struct {
		Imports map[string]string `json:"imports"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		// A deno.jsonc legitimately carries comments, which encoding/json
		// rejects. Unreadable is not fatal: the blueprint may declare
		// nothing that needs resolving, and refusing to evaluate over a
		// file this cannot parse would be worse than the state before
		// this existed. A blueprint that DOES need an entry from it
		// fails at evaluation with Deno's own precise message.
		return nil, nil
	}

	declaredIn := filepath.Base(configPath)
	out := make([]tsDeclaredImport, 0, len(doc.Imports))
	for specifier, target := range doc.Imports {
		d := tsDeclaredImport{Specifier: specifier, Target: target, Declared: declaredIn}
		switch {
		case strings.HasPrefix(target, "npm:"):
			d.Kind = tsImportNPM
		case strings.HasPrefix(target, "jsr:"):
			d.Kind = tsImportJSR
		case strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"):
			d.Kind = tsImportWeb
		case strings.HasPrefix(target, "node:"):
			// A Node builtin. Not a fetch at all, so it passes through
			// untouched rather than being treated as remote.
			d.Kind = tsImportLocal
		default:
			abs := target
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(dir, abs)
			}
			abs = filepath.ToSlash(filepath.Clean(abs))
			// A trailing slash makes an import-map entry a PREFIX mapping,
			// which is how a blueprint maps a whole directory ("lib/":
			// "./lib/"). filepath.Clean eats it, turning a prefix mapping
			// into an exact one that matches only the bare specifier. Same
			// class of bug as the npm subpath one above, and the consumer's
			// own map already handles it (tseval/importmap.go).
			if strings.HasSuffix(target, "/") && !strings.HasSuffix(abs, "/") {
				abs += "/"
			}
			d.Kind = tsImportLocal
			d.Target = "file://" + abs
		}
		out = append(out, d)
	}
	return out, nil
}

// packageJSONFileName is npm's own manifest, which a TypeScript
// blueprint authored with npm tooling has instead of a deno.json.
const packageJSONFileName = "package.json"

// tsPackageJSONImports reads dir's own package.json dependencies.
//
// This exists because the first version of this file read deno.json and
// nothing else, which made the boundary inconsistent rather than narrow:
// a deno.json registry specifier was refused loudly and a package.json
// one fell through to Deno, failing later with a message about
// node_modules that names neither the blueprint nor the reason. That is
// the worse half, since a blueprint authored with npm tooling has a
// package.json and no deno.json, which is the common case.
//
// A "file:" dependency names a path exactly as a deno.json relative
// target does, so it belongs on the resolvable side. It is not the same
// KIND of path, though, and that is the wrinkle:
//
//   - A deno.json target names a FILE ("./helper.ts").
//   - An npm file: target names a PACKAGE DIRECTORY ("file:../vendored"),
//     whose entry point comes from that package's own main or exports.
//     An import map cannot express "the package at this directory":
//     mapping a specifier to a directory URL fails with
//     ERR_UNSUPPORTED_DIR_IMPORT. So honouring one means resolving its
//     entry point here.
//   - And an npm file: target usually points OUTSIDE the declaring
//     package, which is the whole reason to use one. Packaging walks the
//     blueprint directory only, so that target never travelled and the
//     pulled blueprint carries a dangling reference.
//
// So a file: dependency is honoured only where it can actually work:
// inside the blueprint, with an entry point simple enough to resolve
// without reimplementing npm. Everything else is refused, each with the
// reason that applies to it.
func tsPackageJSONImports(dir, name string) ([]tsDeclaredImport, error) {
	path := filepath.Join(dir, packageJSONFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", packageJSONFileName, err)
	}
	var doc struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s in blueprint %q: %w", packageJSONFileName, name, err)
	}
	if len(doc.Dependencies) == 0 {
		return nil, nil
	}

	out := make([]tsDeclaredImport, 0, len(doc.Dependencies))
	var escaped, unresolvable []string
	for specifier, spec := range doc.Dependencies {
		d := tsDeclaredImport{Specifier: specifier, Declared: packageJSONFileName}

		local, isFile := strings.CutPrefix(spec, "file:")
		if !isFile {
			d.Kind, d.Target = npmSpecifierFor(specifier, spec)
			out = append(out, d)
			continue
		}

		target := local
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		target = filepath.Clean(target)

		if !withinDir(dir, target) {
			escaped = append(escaped, specifier+" -> "+spec)
			continue
		}
		entry, err := npmPackageEntry(target)
		if err != nil {
			unresolvable = append(unresolvable, specifier+" -> "+spec+" ("+err.Error()+")")
			continue
		}
		d.Kind = tsImportLocal
		d.Target = "file://" + filepath.ToSlash(entry)
		out = append(out, d)
	}

	if len(escaped) > 0 {
		sort.Strings(escaped)
		return nil, fmt.Errorf("blueprint %q declares %d file: dependency(ies) pointing outside the blueprint:\n    %s\n  declared in %s\n  Packaging walks the blueprint directory only, so those targets never travelled: the pulled blueprint carries a reference to something that is not there. A blueprint's dependencies have to live inside it to survive being published",
			name, len(escaped), strings.Join(escaped, "\n    "), packageJSONFileName)
	}
	if len(unresolvable) > 0 {
		sort.Strings(unresolvable)
		return nil, fmt.Errorf("blueprint %q declares %d file: dependency(ies) whose entry point ubx cannot resolve:\n    %s\n  declared in %s\n  An import map entry has to name a FILE, and npm resolves a package directory through its own rules. ubx handles a plain \"main\" or an index file and deliberately does not reimplement conditional or subpath \"exports\", which would be a second, subtly different npm resolver",
			name, len(unresolvable), strings.Join(unresolvable, "\n    "), packageJSONFileName)
	}
	return out, nil
}

// npmSpecifierFor turns one package.json dependency into the specifier
// an import map entry needs.
//
// A bare semver range becomes "npm:<name>@<range>", which is exactly
// what Deno synthesizes for a workspace package.json, and the range is
// kept rather than resolved because the lock is what pins it. npm's own
// alias form ("foo": "npm:bar@1.0.0") already IS such a specifier and
// passes through. Anything naming a URL or a VCS is neither, and is
// classified as web so the refusal explains itself.
func npmSpecifierFor(name, spec string) (tsImportKind, string) {
	switch {
	case strings.HasPrefix(spec, "npm:"):
		return tsImportNPM, spec
	case strings.HasPrefix(spec, "jsr:"):
		return tsImportJSR, spec
	case strings.HasPrefix(spec, "http://"), strings.HasPrefix(spec, "https://"),
		strings.HasPrefix(spec, "git+"), strings.HasPrefix(spec, "github:"),
		strings.HasPrefix(spec, "git:"):
		return tsImportWeb, spec
	default:
		return tsImportNPM, "npm:" + name + "@" + spec
	}
}

// npmPrefixTarget turns an npm specifier into the prefix form an import
// map needs for that package's subpaths.
//
//	npm:date-fns@3.6.0  ->  npm:/date-fns@3.6.0/
//
// The leading slash after the scheme is required and is easy to miss:
// "npm:date-fns@3.6.0/" is not a valid prefix target, and deno rejects
// the map rather than silently ignoring the entry.
//
// Returns "" for anything that is not an npm specifier, so a caller can
// skip the entry rather than emit a broken one.
func npmPrefixTarget(target string) string {
	rest, ok := strings.CutPrefix(target, "npm:")
	if !ok || rest == "" {
		return ""
	}
	// Already a prefix form, which nothing emits today but a hand-written
	// deno.json legitimately could.
	rest = strings.TrimPrefix(rest, "/")
	return "npm:/" + strings.TrimSuffix(rest, "/") + "/"
}

// withinDir reports whether target is inside root.
func withinDir(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// npmPackageEntry resolves a package directory's entry file, for the
// subset of npm's rules that has one obvious answer.
//
// "exports" is refused rather than guessed. It supports conditional
// resolution and subpath patterns, and a partial implementation would
// resolve some packages to the wrong file rather than failing, which is
// worse than not resolving them at all.
func npmPackageEntry(pkgDir string) (string, error) {
	info, err := os.Stat(pkgDir)
	if err != nil {
		return "", fmt.Errorf("no such directory")
	}
	if !info.IsDir() {
		// npm also accepts a .tgz, which is an archive rather than a
		// tree and would have to be extracted somewhere first.
		return "", fmt.Errorf("names a file rather than a package directory")
	}

	data, err := os.ReadFile(filepath.Join(pkgDir, packageJSONFileName))
	if err != nil {
		return "", fmt.Errorf("no package.json in it")
	}
	var doc struct {
		Main    string          `json:"main"`
		Exports json.RawMessage `json:"exports"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("its package.json does not parse")
	}
	if len(doc.Exports) > 0 {
		return "", fmt.Errorf("uses an \"exports\" map")
	}

	candidates := []string{doc.Main}
	candidates = append(candidates, "index.js", "index.mjs", "index.ts")
	for _, c := range candidates {
		if c == "" {
			continue
		}
		entry := filepath.Join(pkgDir, c)
		if st, err := os.Stat(entry); err == nil && !st.IsDir() {
			return entry, nil
		}
	}
	return "", fmt.Errorf("no \"main\" and no index file")
}
