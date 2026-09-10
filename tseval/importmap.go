package tseval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// importmap.go merges the evaluated project's own import map into the
// one this package generates, instead of replacing it.
//
// runner.go passes --import-map, and a Deno import map supplied that way
// REPLACES the project's own rather than layering on top of it. The
// generated map holds exactly one entry, "@ubx/sdk" pointing at the
// embedded runtime, so before this every other specifier a program
// declared was unresolvable: a stack importing the published
// @ubx/sdk-aws failed with "not a dependency and not in import map"
// while the entry sat in its own deno.json, and the only imports that
// ever worked were "@ubx/sdk" and relative paths. That made the whole
// published TypeScript SDK, 1,921 exports on jsr, unreachable from
// `ubx plan`, while `ubx init` printed "npm install @ubx/sdk-aws" as its
// own next step (UBI-260).
//
// "@ubx/sdk" still wins on conflict. The runtime is embedded in the ubx
// binary on purpose, so evaluation works offline and so the runtime a
// program evaluates against is always the one this binary shipped with,
// never a differently-versioned copy a project happens to have pinned.
// A project remapping it is therefore overridden rather than honored,
// deliberately.

// denoConfigNames is the discovery order Deno itself uses.
var denoConfigNames = []string{"deno.json", "deno.jsonc"}

// findDenoConfig walks up from dir looking for a Deno config, mirroring
// Deno's own upward config discovery. Returns "" when there is none,
// which is the ordinary case for a project that imports nothing beyond
// the runtime and relative paths.
func findDenoConfig(dir string) string {
	for {
		for _, name := range denoConfigNames {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// projectImports reads the "imports" object out of a Deno config.
//
// A config that cannot be parsed is not an error here. deno.jsonc
// legitimately carries comments, which encoding/json rejects, and a
// project whose config this cannot read is no worse off than before this
// existed: it falls back to the runtime-only map, which is exactly what
// every evaluation used until now. Deno itself will report a genuinely
// malformed config far better than this could.
func projectImports(configPath string) map[string]string {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}
	var cfg struct {
		Imports map[string]string `json:"imports"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return nil
	}
	return cfg.Imports
}

// absolutizeRelative rewrites a relative import-map target into an
// absolute file: URL rooted at the config it came from.
//
// This is the one thing a naive merge gets wrong. An import map's own
// relative targets resolve against the MAP's location, and the merged
// map is written to a temp directory, so copying "./vendor/sdk.ts"
// across verbatim would silently repoint it at a path that does not
// exist. A specifier carrying a scheme (jsr:, npm:, https:, file:) and a
// bare specifier that maps to another bare specifier both pass through
// untouched.
func absolutizeRelative(target, configDir string) string {
	if !strings.HasPrefix(target, "./") && !strings.HasPrefix(target, "../") && !strings.HasPrefix(target, "/") {
		return target
	}
	abs := target
	if !filepath.IsAbs(target) {
		abs = filepath.Join(configDir, filepath.FromSlash(target))
	}
	// A trailing slash is meaningful in an import map (it makes the entry
	// a prefix mapping), and filepath.Join eats it.
	if strings.HasSuffix(target, "/") && !strings.HasSuffix(abs, string(filepath.Separator)) {
		abs += string(filepath.Separator)
	}
	return "file://" + filepath.ToSlash(abs)
}

// writeMergedImportMap builds the import map for one evaluation: the
// project's own entries, relative ones made absolute, with the embedded
// runtime's "@ubx/sdk" layered over the top. Returns the path to write
// and a cleanup func.
func writeMergedImportMap(entryDir, runtimePath string) (string, func(), error) {
	imports := map[string]string{}
	if configPath := findDenoConfig(entryDir); configPath != "" {
		configDir := filepath.Dir(configPath)
		for specifier, target := range projectImports(configPath) {
			imports[specifier] = absolutizeRelative(target, configDir)
		}
	}
	imports["@ubx/sdk"] = runtimePath

	data, err := json.Marshal(struct {
		Imports map[string]string `json:"imports"`
	}{Imports: imports})
	if err != nil {
		return "", nil, fmt.Errorf("tseval: build import map: %w", err)
	}

	f, err := os.CreateTemp("", "ubx-tseval-importmap-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("tseval: build import map: %w", err)
	}
	path := f.Name()
	cleanup := func() { os.Remove(path) }
	if _, err := f.Write(data); err != nil {
		f.Close()
		cleanup()
		return "", nil, fmt.Errorf("tseval: build import map: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("tseval: build import map: %w", err)
	}
	return path, cleanup, nil
}
