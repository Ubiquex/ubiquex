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
	pending := pendingBlueprintNames(intent)
	if len(pending) == 0 {
		return nil
	}

	found, err := discoverImportedBlueprints(ctx, entryFile)
	if err != nil {
		return fmt.Errorf("blueprint: resolve direct-call provenance: %w", err)
	}

	for i := range intent.Resources {
		ri := &intent.Resources[i]
		for j := range ri.Sources {
			s := &ri.Sources[j]
			if s.Kind != "blueprint" || strings.Contains(s.Ref, ":") {
				continue
			}
			ref, ok := found[s.Ref]
			if !ok {
				return fmt.Errorf("blueprint: resolve direct-call provenance: %s.%s.%s names blueprint %q, but no imported Go module in %s resolves to a real blueprint directory (an Ubxfile-bearing parent of a go.mod'd package) with that name -- this works for a blueprint imported via a local directory or a local `replace` directive (the pattern every direct-SDK-import example in this project uses today); a blueprint distributed as a standalone published Go module with no adjacent Ubxfile/blueprint.lock.json isn't supported yet",
					intent.Stack, ri.Type, ri.Name, s.Ref, entryFile)
			}
			s.Ref = ref
		}
	}
	return nil
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
// module directory's PARENT contains an Ubxfile (Slice 4's own
// established go/ts/py sibling-directory convention -- a blueprint's Go
// module root is always exactly one level below the blueprint's own
// root). Returns a map of blueprint name (Ubxfile-bearing directory's
// own basename -- the SAME derivation buildManifest/Package/Verify
// already use) -> full "name:content_hash" ref.
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

		root := filepath.Dir(moduleDir)
		if _, err := os.Stat(filepath.Join(root, UbxfileName)); err != nil {
			continue // an ordinary Go dependency, not a blueprint
		}
		name := filepath.Base(root)
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
