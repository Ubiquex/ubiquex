package sdkeval

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	tsassets "github.com/ubiquex/ubiquex-cli/sdk/ts"
)

// embeddedFiles are exactly the files sdk/ts/embed.go's own //go:embed
// directive carries -- kept as one list here too so a mismatch between
// the two (a file added to one but not the other) fails loudly at
// extraction time rather than silently shipping an incomplete harness.
var embeddedFiles = []string{
	"evaluator/guards.ts",
	"runtime/src/index.ts",
}

// extractAssets writes the embedded guards.ts + @ubx/sdk runtime source
// (sdk/ts/embed.go) to a fresh temp directory, once per process (the
// content is static for the lifetime of one `ubx` invocation -- no
// reason to re-extract per Evaluate call), and writes a deno.json there
// mapping the bare "@ubx/sdk" specifier a program's own code imports to
// the extracted runtime file. This is what makes evaluation work
// completely offline, even though @ubx/sdk isn't published to npm yet
// (UBI-34's own "Out of scope") -- the runtime always ships inside the
// `ubx` binary itself; a real npm install is an independent convenience
// for a program author's own editor/IDE type-checking, never required
// for evaluation to work.
var extractAssets = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "ubx-sdkeval-")
	if err != nil {
		return "", fmt.Errorf("sdkeval: create assets dir: %w", err)
	}

	for _, name := range embeddedFiles {
		data, err := tsassets.Assets.ReadFile(name)
		if err != nil {
			return "", fmt.Errorf("sdkeval: read embedded %s: %w", name, err)
		}
		dest := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", fmt.Errorf("sdkeval: %w", err)
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return "", fmt.Errorf("sdkeval: %w", err)
		}
	}

	runtimePath := filepath.Join(dir, "runtime", "src", "index.ts")
	importMap := fmt.Sprintf(`{"imports":{"@ubx/sdk":%q}}`, runtimePath)
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte(importMap), 0o644); err != nil {
		return "", fmt.Errorf("sdkeval: write import map: %w", err)
	}

	return dir, nil
})
