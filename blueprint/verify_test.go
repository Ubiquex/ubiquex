package blueprint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerify_Success(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	packed, err := Package(context.Background(), dir, filepath.Join(t.TempDir(), "out.tar.gz"))
	if err != nil {
		t.Fatalf("Package: %v", err)
	}

	verified, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.ContentHash != packed.ContentHash {
		t.Errorf("Verify returned ContentHash %q, want %q", verified.ContentHash, packed.ContentHash)
	}
}

func TestVerify_TamperedFile_Fails(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	if _, err := Package(context.Background(), dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "bindings.go"), []byte("package ciplatform\n\n// tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Verify(dir)
	if err == nil {
		t.Fatal("Verify: want error after tampering with a packaged file, got nil")
	}
	if !strings.Contains(err.Error(), "bindings.go") {
		t.Errorf("Verify error doesn't name the tampered file: %v", err)
	}
	if !strings.Contains(err.Error(), "content changed") {
		t.Errorf("Verify error doesn't describe the tamper as a content change: %v", err)
	}
}

func TestVerify_MissingFile_Fails(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	if _, err := Package(context.Background(), dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}

	if err := os.Remove(filepath.Join(dir, "ciplatform.go")); err != nil {
		t.Fatal(err)
	}

	_, err := Verify(dir)
	if err == nil {
		t.Fatal("Verify: want error after removing a packaged file, got nil")
	}
	if !strings.Contains(err.Error(), "ciplatform.go") {
		t.Errorf("Verify error doesn't name the missing file: %v", err)
	}
	if !strings.Contains(err.Error(), "missing from this copy") {
		t.Errorf("Verify error doesn't describe the file as missing: %v", err)
	}
}

func TestVerify_ExtraFile_Fails(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	if _, err := Package(context.Background(), dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte("package ciplatform\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Verify(dir)
	if err == nil {
		t.Fatal("Verify: want error after adding an undeclared file, got nil")
	}
	if !strings.Contains(err.Error(), "extra.go") {
		t.Errorf("Verify error doesn't name the extra file: %v", err)
	}
}

func TestVerify_NoManifest_Errors(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	if _, err := Verify(dir); err == nil {
		t.Fatal("Verify: want error for a directory with no blueprint.lock.json, got nil")
	}
}

func TestVerify_InvariantUnderRename(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	if _, err := Package(context.Background(), dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}

	renamed := filepath.Join(t.TempDir(), "some-other-directory-name")
	if err := os.Rename(dir, renamed); err != nil {
		t.Fatal(err)
	}

	if _, err := Verify(renamed); err != nil {
		t.Fatalf("Verify after rename: %v (content hash should be invariant under which directory the blueprint lives in)", err)
	}
}

// TestVerify_NamesTheNpmInstallCause: installing a TypeScript
// blueprint's dependencies in its own directory is a reasonable thing to
// do, and `npm install` is the reflex. It writes a package-lock.json,
// which is an ordinary file and so becomes part of the content and
// changes the hash.
//
// Without naming the cause, the message reports a file the author did
// not knowingly create and leaves them to work out which command created
// it. Worth a test because the tolerance is asymmetric and invisible:
// node_modules is excluded and package-lock.json is not.
func TestVerify_NamesTheNpmInstallCause(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blueprint.ts"), []byte("export const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := buildManifest(dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err != nil {
		t.Fatalf("a freshly packaged blueprint must verify: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Verify(dir)
	if err == nil {
		t.Fatal("an added package-lock.json changes the content and must fail verification")
	}
	msg := err.Error()
	for _, want := range []string{"npm install", "deno install", "node_modules"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal should name %q so the author knows what did this: %s", want, msg)
		}
	}

	// node_modules is the asymmetry worth pinning: same directory, same
	// kind of command, opposite outcome.
	if err := os.Remove(filepath.Join(dir, "package-lock.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "left-pad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "left-pad", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(dir); err != nil {
		t.Fatalf("node_modules is excluded from the manifest and must not affect the hash: %v", err)
	}
}
