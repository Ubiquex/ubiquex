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
// # What the pass is for
//
// Two things, and they are worth separating.
//
// It makes the dependencies RESOLVABLE, which the first version did not.
// That version fetched into deno's global cache and stopped, which works
// only while the consumer's own project has no package.json. One
// anywhere deno discovers from the module graph root puts the whole
// graph into node_modules resolution, and there the global cache is not
// consulted at all:
//
//	Could not find "@ubx/sdk-aws" in a node_modules folder.
//
// A consumer with a package.json is the documented setup, so that fired
// for the ordinary case and not for the fixture, which had none.
//
// And it makes them VERIFIED, which is the part no other step provides.
// The bytes are checked against the blueprint's own lock before anything
// runs. Without that, evaluation would take whatever the registry served
// and the blueprint's lock would be decorative. So the test that matters
// is the refusal of tampered bytes, not the success of honest ones
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
//   - Without --frozen, it runs, and deno REWRITES the lock file, which
//     for a mirror is a symlink straight into the content store.
//
// So this is a separate invocation with its own lock, against the
// blueprint's own workspace, and evaluation is a second one. The same
// split ubx already uses one level up, where pulling and verifying a
// blueprint is a distinct step from using it.
//
// # The limit, stated plainly
//
// The BLUEPRINT's half becomes pinned, and once its mirror exists,
// available offline. Evaluation still reaches the network for the
// CONSUMER's own dependencies, which are the consumer's business and are
// governed by the consumer's own lock exactly as before. The boundary is
// not that evaluation becomes network-free.
//
// The rejected alternative was --node-modules-dir=none on the evaluator,
// which would also have fixed the resolution failure. It was measured
// rather than argued: it makes a consumer holding a populated
// node_modules re-download those packages, and makes planning fail
// outright when they are offline. Materialising the blueprint's own
// dependencies costs a directory of symlinks and takes neither.

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

// depsDirName is the sibling tree holding materialised dependencies,
// laid out by content hash exactly as the store is so the two line up by
// eye: <root>/sha256/<hex> is the blueprint, <root>/deps/sha256/<hex> is
// what it needs to run.
const depsDirName = "deps"

// BlueprintDepsDir is where this content's materialised dependencies
// live.
func BlueprintDepsDir(contentHash string) (string, error) {
	root, err := defaultBlueprintCacheRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, depsDirName, contentStoreDirName, hashHex(contentHash)), nil
}

// materializeBlueprintDeps prepares a blueprint to be evaluated and
// returns the directory to evaluate it FROM.
//
// # Why a mirror instead of the store itself
//
// deno resolves a bare npm specifier through Node resolution, walking up
// from the importing file, so a blueprint's dependencies have to be in a
// node_modules ADJACENT to it. Putting one inside the content store
// would work: node_modules is excluded from the manifest, so the content
// hash is unaffected and `ubx blueprint verify` still passes.
//
// It is still the wrong place. The store is immutable by design and a
// great deal rests on that, and two concurrent plans would both run
// `deno install` inside the same store directory with nothing arbitrating
// it. A directory of symlinks costs almost nothing and keeps the
// property.
//
// So the mirror is: one symlink per store entry, plus a real
// node_modules, in a sibling tree. Verified that deno does NOT resolve
// the symlinks and walk up from the store instead, which would have
// defeated the whole arrangement.
//
// # Why this is not just the old prefetch
//
// The previous version fetched into deno's global cache and stopped
// there. That works only when the CONSUMER's project has no package.json,
// because a package.json anywhere deno discovers from the module graph
// root puts the whole graph into node_modules resolution, and in that
// mode the global cache is not consulted at all:
//
//	Could not find "@ubx/sdk-aws" in a node_modules folder.
//
// The consumer having a package.json is the documented setup, so this
// fired for the ordinary case and not for the test fixture, which had
// none. Materialising node_modules covers both modes: the import map's
// own npm: entries still serve a consumer with no package.json, and the
// two coexist.
//
// Returns the store directory unchanged when the blueprint has no
// registry dependencies, which is every blueprint ubx itself generates.
func materializeBlueprintDeps(ctx context.Context, storeDir, contentHash, name string) (string, error) {
	lock := tsBlueprintLockPath(storeDir)
	if lock == "" {
		// tsBlueprintImports refuses before this is reached, so arriving
		// here means the two disagree.
		return "", fmt.Errorf("blueprint %q: no %s to fetch against", name, denoLockFileName)
	}

	evalDir, err := BlueprintDepsDir(contentHash)
	if err != nil {
		return "", fmt.Errorf("blueprint %q: %w", name, err)
	}
	// Keyed by content hash, so an existing mirror is by definition a
	// mirror of exactly this content. Reusing it keeps repeat plans
	// offline as well as fast.
	//
	// Intact, though, not merely present. A blueprint declared from a
	// LOCAL path is staged into a temp directory that is removed when the
	// command exits, so its mirror's symlinks dangle from the next run
	// onward while the content hash stays identical. Checking only for
	// node_modules would hand back a directory whose every source file
	// had vanished. Found by running it twice.
	if mirrorIntact(evalDir, storeDir) {
		return evalDir, nil
	}
	// Stale rather than absent. Removing it is safe: everything here is
	// derived, and the store it mirrors is untouched.
	if err := os.RemoveAll(evalDir); err != nil {
		return "", fmt.Errorf("blueprint %q: %w", name, err)
	}

	deno, err := denoBinary("preparing a blueprint's npm dependencies")
	if err != nil {
		return "", err
	}

	root, err := defaultBlueprintCacheRoot()
	if err != nil {
		return "", fmt.Errorf("blueprint %q: %w", name, err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("blueprint %q: %w", name, err)
	}
	// Built in staging and renamed into place, so a killed process leaves
	// nothing half-prepared and two concurrent plans cannot interleave
	// inside one directory. Same shape fetchIntoContentStore uses, and
	// same reason.
	staging, err := os.MkdirTemp(root, ".deps-*")
	if err != nil {
		return "", fmt.Errorf("blueprint %q: %w", name, err)
	}
	defer os.RemoveAll(staging)

	if err := mirrorInto(staging, storeDir); err != nil {
		return "", fmt.Errorf("blueprint %q: %w", name, err)
	}

	// --frozen, and deliberately NOT --node-modules-dir=none: the
	// node_modules tree is the whole point here. --frozen is what keeps
	// this a verification rather than a fetch, enforcing the blueprint's
	// own lock, and it also leaves the lock unwritten, which matters
	// because the lock in this mirror is a symlink INTO the store.
	cmd := exec.CommandContext(ctx, deno, "install", "--frozen")
	cmd.Dir = staging
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s", prefetchFailure(name, err, strings.TrimSpace(out.String())))
	}

	if err := os.MkdirAll(filepath.Dir(evalDir), 0o755); err != nil {
		return "", fmt.Errorf("blueprint %q: %w", name, err)
	}
	if err := os.Rename(staging, evalDir); err != nil {
		// Already present is the ordinary race: another plan prepared the
		// same content hash first, and by construction its mirror is of
		// the same bytes. Reuse it.
		if _, statErr := os.Stat(evalDir); statErr != nil {
			return "", fmt.Errorf("blueprint %q: %w", name, err)
		}
	}
	return evalDir, nil
}

// nodeModulesDirName is npm's own name for it, and the marker that a
// mirror is complete.
const nodeModulesDirName = "node_modules"

// mirrorIntact reports whether evalDir is a usable mirror of storeDir:
// it has the materialised dependencies, and every file it claims to
// expose actually resolves.
//
// os.Stat rather than os.Lstat, deliberately: the question is whether
// the symlink still leads somewhere, not whether the symlink exists.
func mirrorIntact(evalDir, storeDir string) bool {
	if info, err := os.Stat(filepath.Join(evalDir, nodeModulesDirName)); err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == nodeModulesDirName {
			continue
		}
		if _, err := os.Stat(filepath.Join(evalDir, e.Name())); err != nil {
			return false
		}
	}
	return true
}

// mirrorInto symlinks every entry of src into dst.
//
// Symlinks rather than copies: a blueprint is small, but the store is
// content-addressed and copying its bytes into a second tree would make
// two answers to "what is this content" where the design has exactly
// one. Absolute targets, so the mirror survives being renamed from
// staging into place.
//
// node_modules is skipped if the store somehow has one, since this is
// the tree that owns that directory.
func mirrorInto(dst, src string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == nodeModulesDirName {
			continue
		}
		if err := os.Symlink(filepath.Join(absSrc, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
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
