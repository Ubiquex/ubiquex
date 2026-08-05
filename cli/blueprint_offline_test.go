package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBlueprintPull_BareTarballFile_PackageVerifyRoundTrip is UBI-74
// Slice 8's own CLI-level proof: a real "ubx blueprint package" tarball,
// pulled directly by FILE PATH (no directory, no git, no oci://), then
// verified -- the standalone offline/email/support-ticket delivery mode.
func TestBlueprintPull_BareTarballFile_PackageVerifyRoundTrip(t *testing.T) {
	dir := writeBuiltBlueprintDir(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform.tar.gz")
	if _, err := runUbx(t, nil, "blueprint", "package", dir, "-o", tarPath); err != nil {
		t.Fatalf("blueprint package: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "pulled")
	pullOut, err := runUbx(t, nil, "blueprint", "pull", tarPath, dest)
	if err != nil {
		t.Fatalf("blueprint pull (bare tarball file): %v\noutput:\n%s", err, pullOut)
	}
	if _, err := os.Stat(filepath.Join(dest, "Ubxfile")); err != nil {
		t.Fatalf("expected pulled dest to contain Ubxfile: %v", err)
	}

	verifyOut, err := runUbx(t, nil, "blueprint", "verify", dest)
	if err != nil {
		t.Fatalf("blueprint verify: %v\noutput:\n%s", err, verifyOut)
	}
	if !strings.Contains(verifyOut, "matches") {
		t.Fatalf("blueprint verify output = %q, want a match confirmation", verifyOut)
	}
}

// TestBlueprintPull_BareTarballFile_RefFlagRefused mirrors the CLI-level
// --ref/--path refusal already proven for oci:// sources -- neither flag
// means anything for a bare tarball file.
func TestBlueprintPull_BareTarballFile_RefFlagRefused(t *testing.T) {
	dir := writeBuiltBlueprintDir(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform.tar.gz")
	if _, err := runUbx(t, nil, "blueprint", "package", dir, "-o", tarPath); err != nil {
		t.Fatalf("blueprint package: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "pulled")
	out, err := runUbx(t, nil, "blueprint", "pull", tarPath, dest, "--ref", "main")
	if err == nil {
		t.Fatalf("blueprint pull: want an error for --ref against a bare tarball file, got nil (output: %s)", out)
	}
}
