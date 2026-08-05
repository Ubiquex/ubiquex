package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBlueprintPush_MissingTo_Errors confirms `ubx blueprint push` refuses
// cleanly when --to is omitted, before attempting any network operation
// at all (no real tarball is even built here -- the --to check runs
// first).
func TestBlueprintPush_MissingTo_Errors(t *testing.T) {
	out, err := runUbx(t, nil, "blueprint", "push", "whatever.tar.gz")
	if err == nil {
		t.Fatalf("blueprint push: want an error for a missing --to, got nil (output: %s)", out)
	}
}

// TestBlueprintPush_InvalidOCIRef_Errors confirms a --to value missing
// the required "oci://" scheme is refused before any network call --
// the tarball itself is real (a real `ubx blueprint package` output), so
// this specifically isolates the --to validation, not a tarball-shape
// failure.
func TestBlueprintPush_InvalidOCIRef_Errors(t *testing.T) {
	dir := writeBuiltBlueprintDir(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform.tar.gz")
	if _, err := runUbx(t, nil, "blueprint", "package", dir, "-o", tarPath); err != nil {
		t.Fatalf("blueprint package: %v", err)
	}

	out, err := runUbx(t, nil, "blueprint", "push", tarPath, "--to", "ghcr.io/ubiquex/ci-platform:v1")
	if err == nil {
		t.Fatalf("blueprint push: want an error for a --to missing the oci:// scheme, got nil (output: %s)", out)
	}
}

// TestBlueprintPush_UnpackagedTarball_Errors confirms pushing a tarball
// with no blueprint.lock.json inside (i.e. not a real `ubx blueprint
// package` output) is refused before any network call -- manifestFromTarball
// (oci.go) runs before Push ever builds a remote.Repository.
func TestBlueprintPush_UnpackagedTarball_Errors(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "not-packaged.tar.gz")
	// A real gzip stream, but not a blueprint tarball -- an empty gzip
	// member is enough to prove manifestFromTarball's own "no
	// blueprint.lock.json inside" refusal, without needing a real tar
	// entry at all.
	writeEmptyGzip(t, tarPath)

	out, err := runUbx(t, nil, "blueprint", "push", tarPath, "--to", "oci://ghcr.io/ubiquex/ci-platform:v1")
	if err == nil {
		t.Fatalf("blueprint push: want an error for an unpackaged tarball, got nil (output: %s)", out)
	}
}

func writeEmptyGzip(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Minimal valid empty gzip stream (RFC 1952 header + empty deflate
	// block + trailer) -- written by hand rather than via compress/gzip
	// so this test file doesn't need its own tar-writing machinery just
	// to prove a validation error path.
	if _, err := f.Write([]byte{0x1f, 0x8b, 0x08, 0x00, 0, 0, 0, 0, 0, 0xff, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}); err != nil {
		t.Fatal(err)
	}
}

// TestBlueprintPull_OCI_RefFlagRefused confirms --ref is refused for an
// oci:// source before any network call -- pull.go's own dispatch checks
// this before ever reaching pullOCI.
func TestBlueprintPull_OCI_RefFlagRefused(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "dest")
	out, err := runUbx(t, nil, "blueprint", "pull", "oci://ghcr.io/ubiquex/ci-platform:v1", dest, "--ref", "main")
	if err == nil {
		t.Fatalf("blueprint pull: want an error for --ref against an oci:// source, got nil (output: %s)", out)
	}
}

// TestBlueprintPull_OCI_PathFlagRefused is the --path sibling of the
// above.
func TestBlueprintPull_OCI_PathFlagRefused(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "dest")
	out, err := runUbx(t, nil, "blueprint", "pull", "oci://ghcr.io/ubiquex/ci-platform:v1", dest, "--path", "ci-platform")
	if err == nil {
		t.Fatalf("blueprint pull: want an error for --path against an oci:// source, got nil (output: %s)", out)
	}
}

// TestBlueprintPull_OCI_MissingTag_Errors confirms an oci:// reference
// with no tag is refused before any network call -- newAuthenticatedRepository
// (oci.go) succeeds offline (it only reads local Docker credential
// config), so the missing-tag check that follows it is what this test
// isolates.
func TestBlueprintPull_OCI_MissingTag_Errors(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "dest")
	out, err := runUbx(t, nil, "blueprint", "pull", "oci://ghcr.io/ubiquex/ci-platform", dest)
	if err == nil {
		t.Fatalf("blueprint pull: want an error for an oci:// reference with no tag, got nil (output: %s)", out)
	}
}
