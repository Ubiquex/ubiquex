package blueprint

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// tslock.go is the two ends of the npm boundary tsdeps.go describes:
// generating a blueprint's deno.lock when it is PACKAGED, and fetching
// its dependencies against that lock before it is EVALUATED.
//
// # Why generation happens at package time
//
// The argument for fetching a blueprint's npm dependencies at all is
// that the lock travels under the content hash, so the dependency graph
// is pinned by the same mechanism that pins the blueprint. That argument
// rested on a claim that a deno.lock travels for free. It does not.
// `npm install` writes package-lock.json and node_modules, and no
// deno.lock; a deno.lock appears only when someone runs a Deno command.
// So requiring one would have refused every npm-authored blueprint until
// its author ran a command they have no reason to know about.
//
// Generating it here makes the claim true rather than assuming it.
// Packaging is the one moment ubx holds the whole blueprint, and it
// already derives the schema at exactly this point for exactly this
// reason. The lock is re-derived on every package rather than reused,
// for the reason package.go already states about the schema: one that
// could be stale defeats the reason it is derived at all.
//
// And deno does not read npm's package-lock.json, which was the obvious
// alternative. Verified by replacing an integrity hash in one with a
// run of A's: `deno install` installed the package without complaint and
// wrote its own deno.lock beside it.
//
// # What this costs
//
// `ubx blueprint package` REACHES THE NETWORK for a blueprint with npm
// dependencies, and requires deno on PATH. That is a real change in what
// the command is: packaging needed neither before. It is scoped as
// narrowly as it can be: a blueprint with no npm dependencies, which is
// every blueprint ubx itself generates, packages exactly as it did.
//
// # What the fetch pass is for, and what it is not for
//
// It is not what makes a blueprint's npm dependencies AVAILABLE. deno
// fetches them itself during evaluation regardless, because --no-remote
// does not block npm. Verified by removing this pass and evaluating
// against a cold cache: the blueprint ran fine.
//
// What it buys is that those bytes are checked against the blueprint's
// OWN lock before anything runs. Without it, evaluation would take
// whatever the registry served and record it in a throwaway lock, and the
// blueprint's lock would be decorative. So the test that matters is the
// refusal of tampered bytes, not the success of honest ones
// (tslock_test.go).
//
// # Why it has to be a separate invocation
//
// deno takes ONE --lock per invocation, so the blueprint's lock cannot
// simply be handed to the consumer's evaluation. Both ways of trying it
// fail, differently:
//
//   - With --frozen, deno refuses. --frozen validates the lock's own
//     workspace section against the workspace actually being evaluated,
//     and the blueprint's lock describes the blueprint's workspace.
//   - Without --frozen, it runs, and deno REWRITES the lock file. That
//     file lives in the content store, so writing to it would mutate
//     content-addressed storage and break its own hash.
//
// So fetch-and-verify is a separate invocation with its own lock, in a
// scratch directory ubx owns, and evaluation is a second invocation that
// finds the packages already in deno's cache. This is the same split ubx
// already uses one level up, where pulling and verifying a blueprint is
// a distinct step from using it.
//
// # The limit, stated plainly
//
// The BLUEPRINT's half becomes pinned. Evaluation still reaches the
// network for the CONSUMER's own dependencies, which are the consumer's
// business and are governed by the consumer's own lock exactly as before.
// deno resolves a workspace's npm dependencies eagerly regardless of what
// the entry file imports, so this cannot be narrowed by touching fewer
// things. The boundary is not that evaluation becomes network-free.

// denoBinary finds deno, with an error naming why it is needed here
// rather than the bare exec.LookPath one.
func denoBinary(why string) (string, error) {
	path, err := exec.LookPath("deno")
	if err != nil {
		return "", fmt.Errorf("deno not found in PATH, which %s requires (https://deno.com): %w", why, err)
	}
	return path, nil
}

// tsDeclaresNPMDeps reports whether dir declares any dependency that
// resolves through the npm registry.
//
// Classification only, no policy: this runs at package time, where the
// question is "would evaluating this ever need a fetch", not "is this
// blueprint allowed to". A declaration this cannot parse is not an error
// here, for the same reason tsDenoConfigImports tolerates one: packaging
// a blueprint should not fail over a file whose contents only matter to
// a later step, which reports it far better.
func tsDeclaresNPMDeps(dir string) bool {
	fromDeno, err := tsDenoConfigImports(dir)
	if err != nil {
		return false
	}
	fromNPM, err := tsPackageJSONImports(dir, filepath.Base(dir))
	if err != nil {
		return false
	}
	for _, d := range append(fromDeno, fromNPM...) {
		if d.Kind == tsImportNPM {
			return true
		}
	}
	return false
}

// GenerateTSLock writes dir's own deno.lock when it declares npm
// dependencies, so the lock is in place before the content hash is
// computed over the directory.
//
// Reports whether it generated one, so `ubx blueprint package` can say
// so in its receipt: an author whose packaging step just made a network
// call should be told, not left to infer it from a new file.
//
// A blueprint with no npm dependencies is untouched and no network call
// is made.
func GenerateTSLock(ctx context.Context, dir string) (bool, error) {
	if lang, err := DetectLanguage(dir); err != nil || lang != "ts" {
		return false, nil
	}
	if !tsDeclaresNPMDeps(dir) {
		return false, nil
	}

	deno, err := denoBinary("packaging a TypeScript blueprint with npm dependencies")
	if err != nil {
		return false, err
	}

	// --node-modules-dir=none keeps this to the lock and deno's own
	// cache. Packaging should not leave a node_modules tree in the
	// author's source directory as a side effect, and packaging excludes
	// it from the archive anyway, so creating one would be pure cost.
	//
	// Nothing downstream needs it. Schema derivation resolves the
	// blueprint's npm specifiers through an import map instead
	// (extractts.go), and so does evaluation. A package.json project
	// resolves BARE specifiers through node_modules, which is why both
	// paths hand deno an explicit npm: entry rather than relying on
	// discovery.
	cmd := exec.CommandContext(ctx, deno, "install", "--node-modules-dir=none")
	cmd.Dir = dir
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("generate %s for blueprint %q: %w\n%s\n  `ubx blueprint package` resolves a TypeScript blueprint's npm dependencies so the resulting %s travels with it and pins what a consumer will fetch. This step needs network access",
			denoLockFileName, filepath.Base(dir), err, strings.TrimSpace(out.String()), denoLockFileName)
	}
	if tsBlueprintLockPath(dir) == "" {
		return false, fmt.Errorf("generate %s for blueprint %q: deno install reported success but wrote no %s\n%s",
			denoLockFileName, filepath.Base(dir), denoLockFileName, strings.TrimSpace(out.String()))
	}
	return true, nil
}

// prefetchTSBlueprintDeps is the fetch-and-verify pass: it downloads a
// blueprint's npm dependencies into deno's cache, with integrity
// enforced by the blueprint's OWN lock, before anything evaluates.
//
// The scratch workspace is rebuilt from the blueprint's own declaration
// files rather than pointing deno at the content store directly, for two
// reasons that both matter. The content store is content-addressed, so
// deno rewriting a lock inside it would break the hash that identifies
// it. And --frozen only passes in a workspace the lock actually
// describes, which is the blueprint's own and not the consumer's.
func prefetchTSBlueprintDeps(ctx context.Context, dir, name string) error {
	lock := tsBlueprintLockPath(dir)
	if lock == "" {
		// tsBlueprintImports refuses before this is reached, so arriving
		// here means the two disagree.
		return fmt.Errorf("blueprint %q: no %s to fetch against", name, denoLockFileName)
	}

	deno, err := denoBinary("fetching a blueprint's npm dependencies")
	if err != nil {
		return err
	}

	scratch, err := os.MkdirTemp("", "ubx-blueprint-deps-*")
	if err != nil {
		return fmt.Errorf("blueprint %q: %w", name, err)
	}
	defer os.RemoveAll(scratch)

	// Only the declaration travels into the scratch workspace. The
	// blueprint's SOURCE is deliberately absent: this pass resolves
	// dependencies, and copying the code in would make deno type-check
	// and graph it for no benefit.
	copied := false
	for _, f := range []string{"deno.json", "deno.jsonc", packageJSONFileName, denoLockFileName} {
		src := filepath.Join(dir, f)
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(scratch, f), data, 0o644); err != nil {
			return fmt.Errorf("blueprint %q: %w", name, err)
		}
		copied = true
	}
	if !copied {
		return fmt.Errorf("blueprint %q: no declaration files to fetch against", name)
	}

	cmd := exec.CommandContext(ctx, deno, "install", "--frozen", "--node-modules-dir=none")
	cmd.Dir = scratch
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s", prefetchFailure(name, err, strings.TrimSpace(out.String())))
	}
	return nil
}

// prefetchFailure explains a failure that belongs to the blueprint
// rather than to the stack evaluating it.
//
// The common shape is a lock that no longer matches the blueprint's own
// manifest, which happens when an author edits package.json and
// re-packages with an older ubx, or hand-edits one of the two. deno's
// own message for that is precise and names the missing entries, so it
// is passed through rather than summarized.
func prefetchFailure(name string, err error, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "blueprint %q: fetching its npm dependencies failed: %v\n", name, err)
	if output != "" {
		for _, line := range strings.Split(output, "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	fmt.Fprintf(&b, "  ubx fetches a blueprint's npm dependencies with integrity enforced by the %s it ships, before\n", denoLockFileName)
	b.WriteString("  evaluating anything. A failure here is the blueprint's own: its lock and its manifest disagree, or the\n")
	b.WriteString("  registry no longer serves what the lock pins. Re-packaging the blueprint with `ubx blueprint package`\n")
	b.WriteString("  regenerates the lock from its manifest.")
	return b.String()
}

// NeedsNetworkToPackage reports whether packaging dir will reach the
// network, so `ubx blueprint package` can say so rather than leaving an
// author to infer it from a new file and a pause.
//
// A predicate rather than a return value from Package: the answer is
// worth having BEFORE the call, and threading a fourth return through
// every call site would be churn for a receipt line.
func NeedsNetworkToPackage(dir string) bool {
	if lang, err := DetectLanguage(dir); err != nil || lang != "ts" {
		return false
	}
	return tsDeclaresNPMDeps(dir)
}
