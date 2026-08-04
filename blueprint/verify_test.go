package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerify_Success(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	packed, err := Package(dir, filepath.Join(t.TempDir(), "out.tar.gz"))
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
	if _, err := Package(dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
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
	if _, err := Package(dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
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
	if _, err := Package(dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
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
	if _, err := Package(dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
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
