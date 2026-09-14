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
