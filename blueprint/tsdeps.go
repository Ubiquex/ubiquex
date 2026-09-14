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
// SCOPE OF THIS FILE: relative targets only, resolving to files inside
// the blueprint. A target naming a registry (npm:, jsr:, http:) is
// refused rather than resolved, because honouring one would mean `ubx
// plan` reaching a registry during evaluation, and that is a policy
// question GOPROXY=off already answers "no" to for Go. Answering it
// differently per language is a real decision and not one to make as a
// side effect of a bug fix.

const denoLockFileName = "deno.lock"

// tsBlueprintImports reads dir's own deno.json and returns its imports
// with every target made absolute against dir.
//
// A blueprint with no deno.json has no imports of its own to resolve,
// which is the ordinary case for a built blueprint: its generated code
// imports "@ubx/sdk" and nothing else.
func tsBlueprintImports(dir, name string) (map[string]string, error) {
	// Both declaration formats, one boundary. A blueprint authored with
	// npm tooling has a package.json and no deno.json, and reading only
	// the latter made the refusal inconsistent rather than narrow.
	fromDeno, err := tsDenoConfigImports(dir, name)
	if err != nil {
		return nil, err
	}
	fromNPM, err := tsPackageJSONImports(dir, name)
	if err != nil {
		return nil, err
	}
	if len(fromDeno) == 0 && len(fromNPM) == 0 {
		return nil, nil
	}
	merged := make(map[string]string, len(fromDeno)+len(fromNPM))
	for k, v := range fromNPM {
		merged[k] = v
	}
	// deno.json wins a collision: it is the format Deno itself prefers,
	// and a blueprint carrying both has usually adopted it over the
	// package.json that came first.
	for k, v := range fromDeno {
		merged[k] = v
	}
	return merged, nil
}

// tsDenoConfigImports reads dir's own deno.json imports.
func tsDenoConfigImports(dir, name string) (map[string]string, error) {
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
	if len(doc.Imports) == 0 {
		return nil, nil
	}

	out := make(map[string]string, len(doc.Imports))
	var remote []string
	for specifier, target := range doc.Imports {
		if isRemoteSpecifier(target) {
			remote = append(remote, specifier+" -> "+target)
			continue
		}
		abs := target
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(dir, abs)
		}
		out[specifier] = "file://" + filepath.ToSlash(filepath.Clean(abs))
	}

	if len(remote) > 0 {
		sort.Strings(remote)
		return nil, fmt.Errorf("%s", remoteSpecifierRefusal(name, configPath, dir, remote))
	}
	return out, nil
}

// isRemoteSpecifier reports whether a target would be resolved by
// reaching a registry or the network rather than the filesystem.
func isRemoteSpecifier(target string) bool {
	for _, prefix := range []string{"npm:", "jsr:", "http://", "https://", "node:"} {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	return false
}

// remoteSpecifierRefusal explains what is not supported yet and why,
// rather than letting Deno fail later with a message about node_modules
// that names neither the blueprint nor the reason.
//
// It reports whether the blueprint ships a deno.lock, because that is
// the thing that would make honouring these specifiers safe rather than
// merely possible: a lock travels in the content store and is covered by
// the content hash, so it pins the resolved graph and not just the
// names. That is the property UBI-265 concluded only vendoring could
// provide, and it is available here for free.
func remoteSpecifierRefusal(name, configPath, dir string, remote []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "blueprint %q declares %d import(s) that resolve from a registry, which ubx does not fetch during evaluation yet:\n", name, len(remote))
	for _, r := range remote {
		fmt.Fprintf(&b, "    %s\n", r)
	}
	fmt.Fprintf(&b, "  declared in %s\n", filepath.Base(configPath))
	b.WriteString("  Reaching a registry while evaluating is a deliberate policy question, not an oversight: the Go evaluator already answers it \"no\" with GOPROXY=off, and answering it differently per language is a real decision (UBI-274).\n")

	if _, err := os.Stat(filepath.Join(dir, denoLockFileName)); err == nil {
		b.WriteString("  This blueprint does ship a " + denoLockFileName + ", so its dependency graph is pinned and travels under the content hash. That is what would make fetching safe here, and is the strongest argument for allowing it.")
	} else {
		b.WriteString("  This blueprint ships no " + denoLockFileName + ", so even if ubx did fetch, the content hash would cover the names and not the bytes that ran.")
	}
	return b.String()
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
// The boundary is relative-file versus registry, not which file
// declared it. A `file:` dependency names a path exactly as a deno.json
// relative target does, so it belongs on the resolvable side.
//
// It is not the same KIND of path, though, and that is the wrinkle:
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
func tsPackageJSONImports(dir, name string) (map[string]string, error) {
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

	out := map[string]string{}
	var remote, escaped, unresolvable []string
	for specifier, spec := range doc.Dependencies {
		local, ok := strings.CutPrefix(spec, "file:")
		if !ok {
			remote = append(remote, specifier+" -> "+spec)
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
		out[specifier] = "file://" + filepath.ToSlash(entry)
	}

	if len(remote) > 0 {
		sort.Strings(remote)
		return nil, fmt.Errorf("%s", remoteSpecifierRefusal(name, path, dir, remote))
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
