// sdkprovenance.go is UBI-126's own fix: retrofitting the SAME
// per-resource blueprint provenance blueprint.ExpandCalls already stamps
// for a diagram/md call (Slice 6) onto a direct SDK-import call (Slice
// 2's own original calling convention) -- the one calling path Slice 6
// never covered, named as the #1 gap in UBI-74's own closing
// retrospective (2026-08-05).
//
// The two calling paths are structurally different, not just
// historically unequal: ExpandCalls SYNTHESIZES its own throwaway
// calling program (invoke.go), so it already knows -- external to the
// evaluated program entirely -- exactly which blueprint produced every
// resource in the result, and stamps all of them. A direct SDK-import
// program is a real, hand-written stack that imports a blueprint
// package and calls its exported function directly (sdk/go/runtime's
// own PushBlueprintSource/PopBlueprintSource, called only by generated
// code, marks each sdk.Resource() call made from inside that function
// with the blueprint's own bare, unsanitized name) -- but that
// compiled, sandboxed binary has no way to compute its own blueprint's
// real content hash (it doesn't know its own on-disk directory, and
// baking a hash into source that then gets hashed itself is circular,
// the same reasoning blueprint.lock.json's own hash-exclusion already
// establishes). StampDirectCallProvenance is the external half: run
// AFTER the evaluated program returns, resolving each bare blueprint
// name to a real on-disk directory via the entry program's own Go
// module graph, computing its real content hash the exact same way
// ExpandCalls already does (buildManifest), and rewriting each
// resource's own incomplete "blueprint" source in place.
package blueprint

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// StampDirectCallProvenance resolves every INCOMPLETE blueprint source
// (a bare name, no ":" -- what sdk.Resource() itself can produce, never
// a real ref) intent's own resources carry into the SAME "<name>:
// <content_hash>" form ExpandCalls already produces, by walking
// entryFile's own Go module graph to find each named blueprint's real
// on-disk directory. A no-op, and never runs `go list` at all, if intent
// has no incomplete blueprint sources to begin with -- an ordinary Go
// SDK program (the overwhelming majority) pays nothing extra.
func StampDirectCallProvenance(ctx context.Context, entryFile string, intent *resolver.IntentFile) error {
	if len(pendingBlueprintNames(intent)) == 0 {
		return nil
	}

	found, err := discoverImportedBlueprints(ctx, entryFile)
	if err != nil {
		return fmt.Errorf("blueprint: resolve direct-call provenance: %w", err)
	}

	hint := fmt.Sprintf("no imported Go module in %s sits inside a blueprint this can hash. A blueprint is found by walking %s's own module graph (`go list -m all`) and checking whether each module's own directory, or its parent, is a blueprint root", entryFile, entryFile)
	return applyBlueprintRefs(intent, found, hint)
}

// applyBlueprintRefs is the language-independent second half every
// StampDirectCallProvenance* variant shares (Go, TS -- see
// tsprovenance.go; Python's own equivalent completes refs directly from
// its already-resolved requirements.txt dependencies, blueprint/pydeps.go,
// never needing this helper since it has no bare-name "discovery" step
// of its own to run): rewrites every resource's own incomplete
// "blueprint" source in place using found (name -> "name:content_hash"),
// or fails with a clear, named error -- never a silent no-op -- naming
// notFoundHint (the language-specific reason a name might not resolve)
// for whichever bare name found doesn't cover.
func applyBlueprintRefs(intent *resolver.IntentFile, found map[string]string, notFoundHint string) error {
	for i := range intent.Resources {
		ri := &intent.Resources[i]
		for j := range ri.Sources {
			s := &ri.Sources[j]
			if s.Kind != "blueprint" || strings.Contains(s.Ref, ":") {
				continue
			}
			ref, ok := found[s.Ref]
			if !ok {
				return fmt.Errorf("blueprint: resolve direct-call provenance: %s.%s.%s names blueprint %q, but %s.%s",
					intent.Stack, ri.Type, ri.Name, s.Ref, notFoundHint, discoveredSuffix(found))
			}
			s.Ref = ref
		}
	}
	return nil
}

// discoveredSuffix reports what discovery DID find, which is usually
// the whole diagnosis (UBI-257).
//
// The old message asserted the layout was wrong ("an Ubxfile-bearing
// parent of a go.mod'd package"), and the layout was usually correct:
// the real cause was a name mismatch, and the message sent whoever hit
// it to inspect a structure that had nothing wrong with it. Naming what
// was found instead lets a reader see a near-miss immediately, and an
// empty list says something quite different from a list of two
// blueprints with other names.
func discoveredSuffix(found map[string]string) string {
	if len(found) == 0 {
		return " No blueprint was found at all, so either none is imported or none of the imported ones is a blueprint this can reach on disk"
	}
	names := make([]string, 0, len(found))
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	return fmt.Sprintf(" The blueprints it did find are: %s. A blueprint calls itself whatever its own blueprint.lock.json records, which is not necessarily the directory it sits in", strings.Join(names, ", "))
}

// pendingBlueprintNames collects every distinct bare (incomplete) "kind":
// "blueprint" source name across intent's own resources -- the set
// discoverImportedBlueprints actually needs to resolve, and (if empty)
// the fast-path check that skips `go list` entirely.
func pendingBlueprintNames(intent *resolver.IntentFile) map[string]bool {
	names := map[string]bool{}
	for _, ri := range intent.Resources {
		for _, s := range ri.Sources {
			if s.Kind == "blueprint" && !strings.Contains(s.Ref, ":") {
				names[s.Ref] = true
			}
		}
	}
	return names
}

// discoverImportedBlueprints walks entryFile's own Go module graph
// (`go list -m all`, run directly against entryFile's own real
// directory and go.mod -- never goeval's own ephemeral build copy,
// already torn down by the time this runs, but module resolution is
// deterministic from the same go.mod/module cache/replace directives
// either way) and returns every DIRECT-OR-TRANSITIVE dependency that
// turns out to be a real, locally-reachable blueprint: one whose own
// module directory, or that directory's parent, is a blueprint root
// (blueprintRootContaining, below, has the account of why both). Returns
// a map of blueprint name (blueprintNameAt's own derivation, the SAME
// one buildManifest/Package/Verify already use) -> full
// "name:content_hash" ref.
func discoverImportedBlueprints(ctx context.Context, entryFile string) (map[string]string, error) {
	absEntry, err := filepath.Abs(entryFile)
	if err != nil {
		return nil, err
	}
	entryDir := filepath.Dir(absEntry)

	// GOFLAGS=-mod=mod matches goeval's own buildProgram exactly (build.go)
	// -- a synthesized or freshly-written go.mod (real params/replace
	// directives, but never yet `go mod tidy`d) is the normal, expected
	// state here, not an error; without this, `go list -m all` refuses
	// outright ("updates to go.mod needed; to update it: go mod tidy")
	// on exactly the same real fixture shape goeval already tolerates.
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Path}}|{{.Dir}}", "all")
	cmd.Dir = entryDir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(ee.Stderr))
		}
		return nil, fmt.Errorf("go list -m all in %s: %w: %s", entryDir, err, stderr)
	}

	found := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue // the main module itself, or one with no resolved Dir (not yet downloaded) -- never a blueprint we can hash
		}
		moduleDir := parts[1]

		root, ok := blueprintRootContaining(moduleDir)
		if !ok {
			continue // an ordinary Go dependency, not a blueprint
		}
		name := blueprintNameAt(root)
		if _, already := found[name]; already {
			continue // first match wins; a genuine ambiguity (two distinct blueprints sharing a bare name) is a real, separate problem this fix doesn't attempt to detect
		}
		manifest, err := buildManifest(root, name)
		if err != nil {
			return nil, fmt.Errorf("hash blueprint %q at %s: %w", name, root, err)
		}
		found[name] = name + ":" + manifest.ContentHash
	}
	return found, nil
}

// blueprintRootContaining maps a directory holding imported source to
// the blueprint root containing it, if any. Shared by Go's and
// TypeScript's own discovery, which had the identical bug.
//
// Where the imported source sits depends on which blueprint model
// produced it. An Ubxfile blueprint is BUILT, and its build writes each
// language's package as a sibling directory under the blueprint root,
// so the source is exactly one level below (root/go/, root/ts/). A
// blueprint written as code has no build step and no generated sibling:
// its own directory IS the package, so the source sits at the blueprint
// root itself.
//
// Checking only the parent was right for the built model and one level
// too high for the code model, so a code blueprint was never
// discovered. That did not fail quietly: one that pushed its own name
// got a hard refusal saying no imported module sits inside a blueprint
// this can hash, which is worse than the silence beside it, since it
// refuses the author who did the right thing.
//
// The directory itself is checked FIRST. A blueprint root nested one
// level below another blueprint root is not a shape this project
// produces, but if it ever appeared, the more specific answer is the
// right one.
//
// Source nested deeper than one level below a blueprint root is still
// not discovered. Left alone deliberately: walking up an unbounded
// number of levels would claim any stack that happens to sit inside a
// blueprint's own directory, and the two real models both put their
// source at zero or one.
func blueprintRootContaining(dir string) (string, bool) {
	if IsBlueprintDir(dir) {
		return dir, true
	}
	if parent := filepath.Dir(dir); IsBlueprintDir(parent) {
		return parent, true
	}
	return "", false
}

// blueprintNameAt returns the name a blueprint at root calls ITSELF,
// preferring what it recorded when it was packaged over the directory
// it happens to sit in now (UBI-257).
//
// The two can differ, and when they do, provenance discovery silently
// finds nothing. A resource's blueprint source carries the name baked
// into the blueprint's own generated code at BUILD time
// (sdk.PushBlueprintSource("<name>")), while discovery used to key its
// results on the directory basename on the CONSUMER's disk at resolve
// time. `ubx blueprint pull <source> <dest>` lets a consumer choose
// that directory freely, so pulling into any directory not named
// exactly after the blueprint made the two disagree and the hash never
// got attached.
//
// It also meant a blueprint's recorded identity depended on where
// someone put it: the same verified bytes produced "bp:sha256:..." or
// "bp-renamed:sha256:..." according to the directory alone.
//
// blueprint.lock.json records the build-time name and travels with the
// bytes, so it is the right source. Falling back to the basename keeps
// an unpackaged working directory (no lock file yet) working exactly as
// before.
func blueprintNameAt(root string) string {
	if m, err := readManifest(root); err == nil && m.Name != "" {
		return m.Name
	}
	return filepath.Base(root)
}
