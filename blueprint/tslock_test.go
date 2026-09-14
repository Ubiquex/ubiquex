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

// TestPrefetchTSBlueprintDeps_EnforcesTheLock is what the fetch pass
// exists for, and it is worth being precise about why.
//
// The pass is NOT what makes the dependency available. Verified by
// removing it and evaluating against a cold cache: deno fetches npm
// packages itself during evaluation and the blueprint runs fine, because
// --no-remote does not block npm. What the pass buys is that those bytes
// are checked against the blueprint's OWN lock before anything runs.
// Without it, evaluation would fetch whatever the registry served and
// record it in a throwaway lock, so the blueprint's lock would be
// decorative.
//
// So the property to pin is the refusal, not the success.
func TestPrefetchTSBlueprintDeps_EnforcesTheLock(t *testing.T) {
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
	// the package.
	t.Setenv("DENO_DIR", t.TempDir())
	if err := prefetchTSBlueprintDeps(context.Background(), dir, "bp"); err != nil {
		t.Fatalf("an honest lock must fetch: %v", err)
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
	err = prefetchTSBlueprintDeps(context.Background(), dir, "bp")
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
