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

	runnerPath, err := writeRunnerScript(filepath.Dir(absEntry), assetsDir, absEntry)
	if err != nil {
		return nil, err
	}
	defer os.Remove(runnerPath)

	args := make([]string, 0, len(evaluatorFlags)+3)
	args = append(args, evaluatorFlags...)
	// The import map has to be named explicitly now. It used to be found
	// by Deno's own config discovery, which walks up from the entry
	// SCRIPT -- that worked only while the runner lived beside it in
	// assetsDir, and the runner has moved (see writeRunnerScript).
	args = append(args, "--import-map="+filepath.Join(assetsDir, "deno.json"))
	args = append(args, runnerPath)

	cmd := exec.CommandContext(ctx, denoPath, args...)
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

// writeRunnerScript writes a fresh runner .ts file into dir (a unique
// name per call, so concurrent Evaluate calls -- or DoubleRun's own two
// sequential calls -- never race on the same path) and returns its path.
//
// dir is the ENTRY FILE'S OWN DIRECTORY, not assetsDir, and that is the
// whole of UBI-252's fix.
//
// Deno resolves a bare npm specifier from the package.json/node_modules
// it finds by walking up from the ROOT OF THE MODULE GRAPH, which is
// this runner script. Not from the working directory, and not from the
// file doing the importing. So while the runner lived in assetsDir, a
// program written the way docs.ubiquex.io/tutorial/sdk/install
// documents it --
//
//	npm install @ubx/sdk-aws
//	import { Queue } from "@ubx/sdk-aws/aws/sqs/queue";
//
// -- could not be evaluated at all: "Import ... not a dependency and
// not in import map". The documented TypeScript path type-checked
// under tsc and then failed at `ubx plan`. Only a relative import into
// the user's own tree worked, which is not what any documentation
// describes.
//
// Isolated empirically rather than reasoned about, because the obvious
// fix is the wrong one. cmd.Dir has no bearing on this:
//
//	runner in the project,  cwd outside  -> resolves
//	runner outside,         cwd inside   -> fails
//	runner outside,         cwd outside  -> fails (the old shape)
//
// Permissions are untouched. The full locked-down flag set still
// applies, --deny-read and --no-remote included, verified by running
// the real documented program under exactly those flags. That matches
// what evaluatorFlags' own comment already establishes: a static,
// literal specifier is part of Deno's pre-execution module-graph
// analysis and ungated by --deny-read, and an import-map-resolved bare
// specifier was already known to be. Node resolution turns out to sit
// on the same side of that line.
//
// --no-remote keeps its meaning, and that is a constraint on this fix
// rather than a coincidence: resolution comes from an already-installed
// node_modules on disk. Mapping npm: specifiers instead would push
// resolution into Deno's own registry cache and weaken the one flag
// that closes the dynamic-import gap.
//
// The cost is a temp file in the author's directory for the duration of
// one evaluation. It is uniquely named, removed by the caller's defer
// on every path, and never written anywhere but beside a file the
// author already owns.
func writeRunnerScript(dir, assetsDir, absEntryFile string) (string, error) {
	f, err := os.CreateTemp(dir, ".ubx-runner-*.ts")
	if err != nil {
		// Worth explaining rather than surfacing a bare EACCES: this is
		// the one case where the fix above is visible to an author, and
		// "permission denied" on a file they never asked for is
		// otherwise baffling. No silent fallback to a temp directory --
		// that would trade this clear failure for an unresolvable bare
		// import later, which is much harder to diagnose.
		return "", fmt.Errorf("write runner script in %s: %w\n"+
			"the evaluator writes one short-lived runner file beside your entry file, "+
			"then removes it -- Deno resolves an npm package from the node_modules it "+
			"finds by walking up from that file, so it has to live in your project for "+
			"a bare import like \"@ubx/sdk-aws/aws/sqs/queue\" to resolve at all", dir, err)
	}
	defer f.Close()

	guardsPath := filepath.Join(assetsDir, "evaluator", "guards.ts")
	content := fmt.Sprintf(runnerTemplate, guardsPath, absEntryFile)
	if _, err := f.WriteString(content); err != nil {
		return "", fmt.Errorf("write runner script: %w", err)
	}
	return f.Name(), nil
}
