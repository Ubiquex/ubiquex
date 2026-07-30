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

// evaluatorFlags is the exact locked-down `deno run` flag set docs/sdk.md's
// own "hermetic evaluator" section commits to -- decided empirically
// (UBI-33/34 session 1), re-verified rigorously this session:
//
//   - --no-remote: the one flag that actually closes the dynamic
//     `import("https://...")` gap found empirically in session 1 --
//     --deny-net alone does NOT close it (Deno treats network access and
//     module resolution as two separate permission surfaces).
//   - --deny-net/--deny-env/--deny-run/--deny-ffi/--deny-sys: redundant
//     with Deno's own default-deny posture (zero --allow-* flags already
//     blocks all of these) -- kept explicit anyway as defense-in-depth
//     against a future Deno release quietly changing a default, the same
//     "never assume a security-relevant default stays the default"
//     instinct this project already applies elsewhere (ubx accept's
//     locking, UBI-20).
//   - --deny-read/--deny-write: confirmed this session, rigorously, NOT
//     to need any --allow-read carve-out at all (session 1's own design
//     speculated one would be needed for "the program's own directory
//     tree" -- wrong, corrected here). The distinguishing factor isn't
//     directory location, it's whether an import specifier is a STATIC,
//     LITERAL string (part of Deno's own pre-execution module-graph
//     analysis, ungated by --deny-read regardless of how far outside the
//     harness's own directory it points -- confirmed for a same-dir
//     relative path, a "../sibling" relative path, an absolute path, and
//     an import-map-resolved bare specifier, all under full --deny-read)
//     versus a RUNTIME-COMPUTED one (e.g. built from Deno.args via string
//     concatenation or a constructed URL -- ungated-by-default-deny false,
//     confirmed to throw NotCapable even for a path that would otherwise
//     load fine as a literal). This is exactly why runner.go generates a
//     fresh runner script per evaluation with the entry file's own path
//     baked in as a literal import specifier, rather than a fixed script
//     that dynamically imports a path read from argv.
var evaluatorFlags = []string{
	"run",
	"--no-remote",
	"--deny-net",
	"--deny-read",
	"--deny-write",
	"--deny-env",
	"--deny-run",
	"--deny-ffi",
	"--deny-sys",
}

// runnerTemplate is the freshly-generated, per-evaluation runner script:
// install the nondeterminism guards (guards.ts) -- eagerly, via a static
// import, safe because stack() (sdk/ts/runtime) defers running a
// program's own describe function to an explicit evaluate() call this
// script only reaches afterward -- then statically, literally import the
// program's own entry file and run it, printing the resulting document
// as the ONLY thing on stdout. A thrown exception (docs/sdk.md's own
// adversarial row 4: "no partial intent/v1 is ever emitted") is caught
// here, reported to stderr with the program's own real stack trace/
// message verbatim, and turned into a nonzero exit -- stdout stays
// completely empty in that case, so the Go side never has to guess
// whether partial output on stdout means anything.
const runnerTemplate = `import { installNondeterminismGuards } from %q;
installNondeterminismGuards();
import def from %q;

try {
  const result = def.evaluate();
  console.log(JSON.stringify(result));
} catch (e) {
  const message = e instanceof Error ? (e.stack ?? e.message) : String(e);
  console.error(message);
  Deno.exit(1);
}
`

// runOnce spawns exactly one deno subprocess evaluating entryFile under
// evaluatorFlags, returning its raw (uncanonicalized) stdout on success.
func runOnce(ctx context.Context, entryFile string) ([]byte, error) {
	absEntry, err := filepath.Abs(entryFile)
	if err != nil {
		return nil, fmt.Errorf("entry file: %w", err)
	}
	if info, err := os.Stat(absEntry); err != nil {
		return nil, fmt.Errorf("entry file: %w", err)
	} else if info.IsDir() {
		return nil, fmt.Errorf("entry file: %s is a directory", absEntry)
	}

	denoPath, err := exec.LookPath("deno")
	if err != nil {
		return nil, fmt.Errorf("deno not found in PATH -- the SDK evaluator requires Deno (https://deno.com), chosen empirically over Node/isolated-vm (docs/sdk.md's own \"hermetic evaluator\" section): %w", err)
	}

	assetsDir, err := extractAssets()
	if err != nil {
		return nil, err
	}

	runnerPath, err := writeRunnerScript(assetsDir, absEntry)
	if err != nil {
		return nil, err
	}
	defer os.Remove(runnerPath)

	args := make([]string, 0, len(evaluatorFlags)+1)
	args = append(args, evaluatorFlags...)
	args = append(args, runnerPath)

	cmd := exec.CommandContext(ctx, denoPath, args...)
	// assetsDir also holds deno.json (the @ubx/sdk import map) -- running
	// from there is what makes Deno's own config-file discovery (walking
	// up from the entry SCRIPT passed on the command line, i.e. runnerPath)
	// find it without needing an explicit --config flag.
	cmd.Dir = assetsDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("evaluate %s: %w\n%s", entryFile, err, msg)
		}
		return nil, fmt.Errorf("evaluate %s: %w", entryFile, err)
	}
	return stdout.Bytes(), nil
}

// writeRunnerScript writes a fresh runner .ts file into assetsDir (a
// unique name per call, so concurrent Evaluate calls -- or DoubleRun's
// own two sequential calls -- never race on the same path) and returns
// its path.
func writeRunnerScript(assetsDir, absEntryFile string) (string, error) {
	f, err := os.CreateTemp(assetsDir, "runner-*.ts")
	if err != nil {
		return "", fmt.Errorf("write runner script: %w", err)
	}
	defer f.Close()

	guardsPath := filepath.Join(assetsDir, "evaluator", "guards.ts")
	content := fmt.Sprintf(runnerTemplate, guardsPath, absEntryFile)
	if _, err := f.WriteString(content); err != nil {
		return "", fmt.Errorf("write runner script: %w", err)
	}
	return f.Name(), nil
}
