package tseval

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// declarations.go reads a TypeScript file's own exported declarations
// without running it, for `ubx blueprint package`'s schema extraction
// (blueprint/extractts.go, docs/blueprint.md's own "Blueprint schema").
//
// It lives here rather than in blueprint/ because it is Deno knowledge,
// not blueprint knowledge: which binary, which flags, and above all the
// import map, which this package already had to get right once
// (importmap.go, UBI-260) and which extraction needs for exactly the
// same reason evaluation does.
//
// `deno doc --json` is the mechanism. It is Deno's own documentation
// generator, it emits a structured view of every exported symbol with
// its full type, and it does not execute the module. That last property
// is the whole reason to prefer it to any amount of hand-written
// parsing: TypeScript's type syntax is large, and a hand-rolled reader
// of it would be wrong in ways nobody notices until a blueprint's schema
// quietly disagrees with its own function.
//
// It is also strictly better than matching a type by its source
// spelling, which is what the Go extractor has to do: every type
// reference comes back tagged with where it resolved FROM, so a
// CrossMarker imported from @ubx/sdk is distinguishable from a local
// type that merely shares the name.

// Declarations returns `deno doc --json` output for files.
//
// Takes every file at once rather than one per call because deno doc
// reports a type reference's own resolution across the whole set, so a
// Config interface declared beside the entrypoint rather than in it is
// still resolvable.
//
// The import map is the project's own, merged with @ubx/sdk pointed at
// the embedded runtime, exactly as evaluation builds it. Without it a
// blueprint's own `import { Computed } from "@ubx/sdk"` is unresolvable
// and deno doc refuses to emit anything at all.
func Declarations(ctx context.Context, files ...string) ([]byte, error) {
	if len(files) == 0 {
		return nil, fmt.Errorf("no files to read declarations from")
	}
	absFiles := make([]string, 0, len(files))
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			return nil, fmt.Errorf("source file: %w", err)
		}
		if info, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("source file: %w", err)
		} else if info.IsDir() {
			return nil, fmt.Errorf("source file: %s is a directory", abs)
		}
		absFiles = append(absFiles, abs)
	}

	denoPath, err := exec.LookPath("deno")
	if err != nil {
		return nil, fmt.Errorf("deno not found in PATH -- reading a TypeScript blueprint's own signature requires Deno (https://deno.com), the same binary the TypeScript evaluator already requires: %w", err)
	}

	assetsDir, err := extractAssets()
	if err != nil {
		return nil, err
	}

	mapPath, cleanupMap, err := writeMergedImportMap(filepath.Dir(absFiles[0]), filepath.Join(assetsDir, "runtime", "src", "index.ts"))
	if err != nil {
		return nil, err
	}
	defer cleanupMap()

	// No --allow-* flags, matching evaluatorFlags' own posture. deno doc
	// needs none: it reads and type-resolves, it does not run.
	args := append([]string{"doc", "--json", "--import-map=" + mapPath}, absFiles...)
	cmd := exec.CommandContext(ctx, denoPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			// deno doc resolves the WHOLE module graph and emits nothing
			// at all if any import in it is unresolvable, even an import
			// whose type never appears in the signature being read. That
			// is the one real asymmetry against the Go extractor, which
			// reads an undownloaded tree happily, so the failure says so
			// rather than leaving an author to guess.
			return nil, fmt.Errorf("read declarations of %s: %w\n%s\n\nEvery import must resolve before a TypeScript blueprint's signature can be read, including ones its signature never mentions. Install the blueprint's own dependencies (npm install, or deno add) and try again", strings.Join(files, ", "), err, msg)
		}
		return nil, fmt.Errorf("read declarations of %s: %w", strings.Join(files, ", "), err)
	}
	return stdout.Bytes(), nil
}
