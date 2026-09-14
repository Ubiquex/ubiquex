package blueprint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireNPMLive gates the tests that genuinely reach the npm registry.
//
// `go test ./...` stays hermetic. These are gated rather than faked
// because the property under test is that a REAL deno subprocess refuses
// REAL bytes that do not match a lock, and a fake registry would only
// assert that ubx builds the command it builds.
func requireNPMLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_NPM_LIVE") != "1" {
		t.Skip("skipping: set UBX_NPM_LIVE=1 to fetch real packages from the real npm registry")
	}
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH")
	}
}

// writeNPMBlueprintSource writes an npm-authored blueprint: a
// package.json with a bare semver range, no deno.json, no lock. This is
// what `npm install` leaves behind, and the case the whole design turns
// on.
func writeNPMBlueprintSource(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, packageJSONFileName),
		[]byte(`{"name":"bp","version":"1.0.0","dependencies":{"left-pad":"1.3.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blueprint.ts"),
		[]byte("import leftPad from \"left-pad\";\nexport const f = () => leftPad(\"w\", 3, \"-\");\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGenerateTSLock_ProducesALockTheAuthorNeverWrote is the claim the
// design rests on.
//
// The argument for fetching a blueprint's npm dependencies is that its
// lock travels under the content hash. That argument originally rested on
// a claim that the lock travels for free, which was WRONG: `npm install`
// writes package-lock.json and no deno.lock. Generating it at package
// time makes the claim true rather than assuming it, so the claim is
// asserted rather than assumed here too.
func TestGenerateTSLock_ProducesALockTheAuthorNeverWrote(t *testing.T) {
	requireNPMLive(t)
	dir := t.TempDir()
	t.Setenv("DENO_DIR", t.TempDir())
	writeNPMBlueprintSource(t, dir)

	generated, err := GenerateTSLock(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !generated {
		t.Fatal("a blueprint with npm dependencies must get a generated lock")
	}
	data, err := os.ReadFile(filepath.Join(dir, denoLockFileName))
	if err != nil {
		t.Fatalf("no lock was written: %v", err)
	}
	// The integrity hash is the whole point. A lock recording only version
	// resolutions would pin names rather than bytes.
	if !strings.Contains(string(data), "integrity") {
		t.Fatalf("the generated lock must carry integrity hashes:\n%s", data)
	}

	// And nothing was left in the author's tree beyond the lock itself.
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); !os.IsNotExist(err) {
		t.Error("packaging must not leave a node_modules tree in the author's source directory")
	}
}

// TestGenerateTSLock_NoNPMDepsMakesNoNetworkCall pins the scope of the
// network dependency packaging just acquired.
//
// `ubx blueprint package` reaching the network is a real change in what
// the command is, so it is worth a test that it happens only for the
// blueprints that need it. Every blueprint ubx itself generates imports
// "@ubx/sdk" and nothing else, and must package exactly as it did before.
//
// DENO_DIR and PATH are not even set up here: if this made a subprocess
// call at all it would be visible as a failure rather than as a silent
// slowdown.
func TestGenerateTSLock_NoNPMDepsMakesNoNetworkCall(t *testing.T) {
	t.Run("no declaration at all", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "blueprint.ts"), []byte("export const x = 1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		generated, err := GenerateTSLock(context.Background(), dir)
		if err != nil || generated {
			t.Fatalf("a blueprint with no dependencies must be untouched: %v %v", generated, err)
		}
	})

	t.Run("a vendored file: dependency", func(t *testing.T) {
		dir := t.TempDir()
		writeNPMPackage(t, filepath.Join(dir, "deps", "helper"), "helper", "entry.js")
		writePackageJSON(t, dir, `{"dependencies":{"helper":"file:./deps/helper"}}`)
		if err := os.WriteFile(filepath.Join(dir, "blueprint.ts"), []byte("export const x = 1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		generated, err := GenerateTSLock(context.Background(), dir)
		if err != nil || generated {
			t.Fatalf("a vendored dependency needs no lock and no network: %v %v", generated, err)
		}
	})

	t.Run("a Go blueprint", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "bp.go"), []byte("package bp\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		generated, err := GenerateTSLock(context.Background(), dir)
		if err != nil || generated {
			t.Fatalf("another language's blueprint must not be touched: %v %v", generated, err)
		}
	})
}

// TestMaterializeBlueprintDeps_EnforcesTheLock is what the pass exists
// for, and it is worth being precise about why.
//
// It is not only about making the dependency available. What it buys on
// top of that is that the bytes are checked against the blueprint's OWN
// lock before anything runs. Without that, evaluation would take
// whatever the registry served and the blueprint's lock would be
// decorative.
//
// So the property to pin is the refusal, not the success.
func TestMaterializeBlueprintDeps_EnforcesTheLock(t *testing.T) {
	requireNPMLive(t)
	dir := t.TempDir()
	t.Setenv("DENO_DIR", t.TempDir())
	writeNPMBlueprintSource(t, dir)

	if _, err := GenerateTSLock(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, denoLockFileName)
	good, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	// An honest lock fetches cleanly, against a cache that has never seen
	// the package, and produces a mirror with a node_modules in it.
	t.Setenv("DENO_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	evalDir, err := materializeBlueprintDeps(context.Background(), dir, "sha256:"+strings.Repeat("a", 64), "bp")
	if err != nil {
		t.Fatalf("an honest lock must fetch: %v", err)
	}
	if evalDir == dir {
		t.Fatal("a blueprint with npm dependencies must be evaluated from a mirror, not from the store")
	}
	if info, err := os.Stat(filepath.Join(evalDir, nodeModulesDirName)); err != nil || !info.IsDir() {
		t.Fatalf("the mirror must carry a node_modules: %v", err)
	}
	// The store is untouched. That property is load-bearing for the
	// content-addressed layout, so it is asserted rather than assumed.
	if _, err := os.Stat(filepath.Join(dir, nodeModulesDirName)); !os.IsNotExist(err) {
		t.Error("the content store must not gain a node_modules")
	}
	// And the blueprint's own files are reachable through the mirror.
	if _, err := os.Stat(filepath.Join(evalDir, "blueprint.ts")); err != nil {
		t.Errorf("the mirror must expose the blueprint's own source: %v", err)
	}

	// A tampered one does not. This is the whole mechanism: the lock is
	// only worth carrying if something enforces it.
	tampered := replaceIntegrity(string(good))
	if tampered == string(good) {
		t.Fatal("test bug: no integrity hash found to tamper with")
	}
	if err := os.WriteFile(lockPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DENO_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	_, err = materializeBlueprintDeps(context.Background(), dir, "sha256:"+strings.Repeat("b", 64), "bp")
	if err == nil {
		t.Fatal("bytes that do not match the blueprint's lock must be refused")
	}
	msg := err.Error()
	// deno's own message names both hashes, which is the first thing
	// anyone compares, so it is passed through rather than summarized.
	if !strings.Contains(msg, "checksum") {
		t.Errorf("the refusal should carry deno's own checksum message: %s", msg)
	}
	// And it must be attributed to the blueprint rather than to the stack
	// that merely declared it.
	if !strings.Contains(msg, `"bp"`) {
		t.Errorf("the refusal should name the blueprint: %s", msg)
	}
}

// replaceIntegrity swaps the first sha512 integrity value for a
// well-formed but wrong one, so the failure is a checksum mismatch rather
// than a parse error.
func replaceIntegrity(lock string) string {
	const marker = `"integrity": "sha512-`
	i := strings.Index(lock, marker)
	if i < 0 {
		return lock
	}
	start := i + len(marker)
	end := strings.Index(lock[start:], `"`)
	if end < 0 {
		return lock
	}
	return lock[:start] + strings.Repeat("A", end) + lock[start+end:]
}

// TestMirrorIntact is the reuse gate, tested directly because the
// end-to-end path does not currently exercise it.
//
// A mirror is a directory of symlinks into wherever the blueprint was
// staged. For a blueprint declared from a LOCAL path that is a temp
// directory, and today those are never cleaned up, so the links happen
// to stay valid and a presence-only check happens to pass. That is luck,
// not design: the OS clears that tree, and cleaning it up is an obvious
// future fix which would silently turn every reused mirror into a
// directory of dangling links pointing at nothing.
//
// The gate asks whether the mirror still resolves, not whether it
// exists.
func TestMirrorIntact(t *testing.T) {
	store := t.TempDir()
	for _, f := range []string{"blueprint.ts", "package.json"} {
		if err := os.WriteFile(filepath.Join(store, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mirror := t.TempDir()
	if err := mirrorInto(mirror, store); err != nil {
		t.Fatal(err)
	}

	// No node_modules yet: not a usable mirror, whatever else is true.
	if mirrorIntact(mirror, store) {
		t.Error("a mirror without node_modules is not ready to evaluate from")
	}
	if err := os.Mkdir(filepath.Join(mirror, nodeModulesDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if !mirrorIntact(mirror, store) {
		t.Fatal("a complete mirror must be reusable")
	}

	// The staging directory goes away. Every symlink still EXISTS; none
	// of them leads anywhere.
	if err := os.RemoveAll(store); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(mirror, "blueprint.ts")); err != nil {
		t.Fatalf("test bug: the symlink should still be present: %v", err)
	}
	if mirrorIntact(mirror, "") {
		t.Error("a mirror of a source that is gone must not be reused")
	}
}

// TestMirrorInto_LeavesTheSourceAlone: the content store is immutable by
// design and a great deal rests on that, which is the whole reason the
// dependencies are materialised beside it rather than in it.
func TestMirrorInto_LeavesTheSourceAlone(t *testing.T) {
	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "blueprint.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	mirror := t.TempDir()
	if err := mirrorInto(mirror, store); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(mirror, nodeModulesDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("the store gained entries: %d -> %d", len(before), len(after))
	}
	// Symlinks, not copies: the store is the one answer to "what is this
	// content", and a second full copy would make two.
	info, err := os.Lstat(filepath.Join(mirror, "blueprint.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("mirror entries should be symlinks rather than copies")
	}
}
