// tsprovenance.go ports UBI-126's own direct-SDK-import provenance fix
// (sdkprovenance.go's own doc comment has the full design account) to
// TypeScript -- the SAME two-half shape (an implicit in-process scope
// stamping an incomplete ref, an external step completing it to a real
// "<name>:<content_hash>" afterward), but a genuinely different
// discovery mechanism: Go walks its own module graph via `go list -m
// all`; Deno has no analogous module/dependency-graph CLI built into the
// runtime it evaluates under, but it does ship one as a first-class,
// hermetic, zero-execution introspection command of its own: `deno info
// --json`, which resolves every static import in entryFile's own module
// graph (recursively, including transitive imports) to a real local
// file path without ever running the program. This is the TS-specific
// "analogous, language-appropriate discovery mechanism" docs/blueprint.md's
// own UBI-126 section named as required follow-up work.
package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// StampDirectCallProvenanceTS is StampDirectCallProvenance's own TS
// sibling -- same fast-path (a no-op, never spawning `deno`, for an
// ordinary TS SDK program with no incomplete blueprint sources) and the
// same completion contract (applyBlueprintRefs), differing only in HOW
// found is discovered.
func StampDirectCallProvenanceTS(ctx context.Context, entryFile string, intent *resolver.IntentFile) error {
	if len(pendingBlueprintNames(intent)) == 0 {
		return nil
	}

	found, err := discoverImportedBlueprintsTS(ctx, entryFile)
	if err != nil {
		return fmt.Errorf("blueprint: resolve direct-call provenance: %w", err)
	}

	hint := fmt.Sprintf("no import in %s's own module graph (deno info) sits inside a blueprint this can hash. A blueprint is found by walking that graph and checking whether each local file's own directory, or its parent, is a blueprint root, so a blueprint reached only through a remote or bare specifier (jsr:, npm:, an import-map entry with no local file behind it) has nothing on disk to hash", entryFile)
	return applyBlueprintRefs(intent, found, hint)
}

// denoInfoModule is the one shape denoInfoOutput.Modules actually needs
// -- deno info --json's own real schema has more fields (kind, size,
// mediaType, dependencies, ...), all irrelevant here; encoding/json
// silently ignores whatever this struct doesn't name.
type denoInfoModule struct {
	Local string `json:"local"`
}

type denoInfoOutput struct {
	Modules []denoInfoModule `json:"modules"`
}

// discoverImportedBlueprintsTS walks entryFile's own real Deno module
// graph (`deno info --json --no-remote entryFile`, run directly against
// entryFile's own real directory -- a read-only, zero-execution
// introspection command, never actually running the program) and
// returns every resolved LOCAL module that turns out to be a real,
// locally-reachable blueprint: one whose own file lives inside a `ts/`
// directory whose own PARENT contains an Ubxfile (Slice 4's own
// established go/ts/py sibling-directory convention -- mirrors
// discoverImportedBlueprints' own "module directory's PARENT" check
// exactly, one directory level deeper here since a TS blueprint's own
// generated files sit directly inside ts/, with no further module-root
// nesting the way Go's go.mod introduces one). `--no-remote` keeps this
// fully offline and deterministic: `deno info` never fails just because
// some OTHER import (a real user program's own "@ubx/sdk" specifier,
// resolved however that program's own deno.json says, or a genuinely
// remote jsr:/npm: import) can't be resolved here -- it reports that
// dependency as an unresolved node and keeps walking every other one,
// confirmed empirically before relying on it. Returns a map of blueprint
// name (Ubxfile-bearing directory's own basename -- the SAME derivation
// buildManifest/Package/Verify already use) -> full "name:content_hash"
// ref.
func discoverImportedBlueprintsTS(ctx context.Context, entryFile string) (map[string]string, error) {
	absEntry, err := filepath.Abs(entryFile)
	if err != nil {
		return nil, err
	}

	denoPath, err := exec.LookPath("deno")
	if err != nil {
		return nil, fmt.Errorf("deno not found in PATH -- resolving a TypeScript SDK program's own blueprint provenance requires Deno (https://deno.com), the same evaluator this program already ran under: %w", err)
	}

	cmd := exec.CommandContext(ctx, denoPath, "info", "--json", "--no-remote", absEntry)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		return nil, fmt.Errorf("deno info --json %s: %w: %s", absEntry, err, stderr)
	}

	var info denoInfoOutput
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("parse deno info --json output: %w", err)
	}

	found := map[string]string{}
	for _, m := range info.Modules {
		if m.Local == "" {
			continue // an unresolved or remote (non-local) dependency -- never a blueprint we can hash
		}
		// Was: require the file's directory to be named exactly "ts",
		// then look at its parent. That is the BUILT model's own shape
		// and only that one, so a blueprint written as code, whose entry
		// sits at the blueprint root itself, was never discovered.
		// blueprintRootContaining (sdkprovenance.go) covers both, and the
		// marker test it ends in is what rules out an ordinary local
		// import either way.
		root, ok := blueprintRootContaining(filepath.Dir(m.Local))
		if !ok {
			continue // an ordinary local import, not a blueprint
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
