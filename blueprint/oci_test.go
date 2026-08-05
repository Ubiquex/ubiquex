package blueprint

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/oci"
)

func TestStripOCIScheme(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "oci://ghcr.io/ubiquex/ci-platform:v1", want: "ghcr.io/ubiquex/ci-platform:v1"},
		{in: "ghcr.io/ubiquex/ci-platform:v1", wantErr: true},
		{in: "oci://", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		got, err := stripOCIScheme(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("stripOCIScheme(%q): want error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("stripOCIScheme(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("stripOCIScheme(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestExtractTarGz_RoundTripsWithWriteTarGz is Slice 7's own required
// hermetic proof that extraction genuinely reverses Package's own
// writeTarGz -- a real built blueprint directory, packaged, extracted
// into a fresh directory, byte-for-byte identical to the original.
func TestExtractTarGz_RoundTripsWithWriteTarGz(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	tarPath := filepath.Join(t.TempDir(), "out.tar.gz")
	if _, err := Package(dir, tarPath); err != nil {
		t.Fatalf("Package: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "extracted")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(tarPath, dest); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		want, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dest, e.Name()))
		if err != nil {
			t.Fatalf("extracted copy missing %s: %v", e.Name(), err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s: extracted content doesn't match the original", e.Name())
		}
	}
}

// TestExtractTarGz_RejectsPathTraversal confirms a hand-crafted malicious
// tarball naming an entry outside destDir is refused, not silently
// written outside the intended directory.
func TestExtractTarGz_RejectsPathTraversal(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "evil.tar.gz")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	content := []byte("pwned")
	if err := tw.WriteHeader(&tar.Header{Name: "../../etc/passwd", Size: int64(len(content)), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := extractTarGz(tarPath, dest); err == nil {
		t.Fatal("extractTarGz: want error for a path-traversal entry, got nil")
	}
	escaped := filepath.Join(filepath.Clean(dest), "../../etc/passwd")
	if _, err := os.Stat(escaped); err == nil {
		t.Fatalf("extractTarGz: path-traversal entry was actually written to %s, outside destDir", escaped)
	}
}

func TestManifestFromTarball_MissingLockfile_Errors(t *testing.T) {
	// A real gzipped tarball, but with no blueprint.lock.json inside --
	// e.g. a directory that was never `ubx blueprint package`d.
	tarPath := filepath.Join(t.TempDir(), "not-a-blueprint.tar.gz")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	content := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{Name: "hello.txt", Size: int64(len(content)), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := manifestFromTarball(tarPath); err == nil {
		t.Fatal("manifestFromTarball: want error for a tarball with no blueprint.lock.json, got nil")
	} else if !strings.Contains(err.Error(), "package") {
		t.Errorf("manifestFromTarball error = %v, want it to point at `ubx blueprint package`", err)
	}
}

func TestSoleFile_NoFiles_Errors(t *testing.T) {
	if _, err := soleFile(t.TempDir()); err == nil {
		t.Fatal("soleFile: want error for an empty directory, got nil")
	}
}

func TestSoleFile_MultipleFiles_Errors(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := soleFile(dir); err == nil {
		t.Fatal("soleFile: want error for a directory with more than one file, got nil")
	}
}

// TestPushPullTarget_LocalOCIStoreRoundTrip is UBI-74 Slice 7's own core
// hermetic proof: pushToTarget/pullFromTarget's real ORAS mechanics
// (fs.Add/oras.PackManifest/fs.Tag/oras.Copy), exercised against a real
// on-disk OCI image layout (oras.land/oras-go/v2/content/oci.Store) --
// oras-go's own real local target implementation, never a hand-rolled
// fake standing in for a registry -- proving the whole push/pull round
// trip (including the annotations and content-hash preservation) without
// any network dependency. The real remote.Repository-backed network path
// (Push/pullOCI) is covered by this slice's own required LIVE
// verification against real ghcr.io, not repeated hermetically here.
func TestPushPullTarget_LocalOCIStoreRoundTrip(t *testing.T) {
	ctx := context.Background()

	dir := writeSampleBuiltBlueprint(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform-v1.tar.gz")
	pushManifest, err := Package(dir, tarPath)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}

	registryDir := t.TempDir()
	target, err := oci.New(registryDir)
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}

	tag := "v1"
	if err := pushToTarget(ctx, tarPath, pushManifest, target, tag); err != nil {
		t.Fatalf("pushToTarget: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "pulled")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pullFromTarget(ctx, target, tag, dest); err != nil {
		t.Fatalf("pullFromTarget: %v", err)
	}

	pulledManifest, err := Verify(dest)
	if err != nil {
		t.Fatalf("Verify(pulled): %v", err)
	}
	if pulledManifest.ContentHash != pushManifest.ContentHash {
		t.Errorf("pulled ContentHash = %q, want %q (the pushed manifest's own)", pulledManifest.ContentHash, pushManifest.ContentHash)
	}
	if pulledManifest.Name != pushManifest.Name {
		t.Errorf("pulled Name = %q, want %q", pulledManifest.Name, pushManifest.Name)
	}

	// Every real source file must have round-tripped byte-for-byte, not
	// just the manifest's own declared hashes (which Verify already
	// confirms indirectly -- this checks actual file content directly).
	original, err := os.ReadFile(filepath.Join(dir, "ciplatform.go"))
	if err != nil {
		t.Fatal(err)
	}
	pulled, err := os.ReadFile(filepath.Join(dest, "ciplatform.go"))
	if err != nil {
		t.Fatalf("pulled copy missing ciplatform.go: %v", err)
	}
	if !bytes.Equal(original, pulled) {
		t.Error("pulled ciplatform.go doesn't match the original source file's own bytes")
	}
}

// TestPushToTarget_ManifestCarriesContentHashAnnotation confirms the OCI
// manifest itself records the SAME content_hash blueprint.lock.json
// carries inside the tarball, as a real, independently-inspectable OCI
// annotation -- so a `docker manifest inspect`/`oras manifest fetch`
// against a real registry can cross-check it without pulling+extracting
// first (never a second, competing hash scheme -- see oci.go's own doc
// comment on annoContentHash).
func TestPushToTarget_ManifestCarriesContentHashAnnotation(t *testing.T) {
	ctx := context.Background()

	dir := writeSampleBuiltBlueprint(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform-v1.tar.gz")
	pushManifest, err := Package(dir, tarPath)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}

	registryDir := t.TempDir()
	target, err := oci.New(registryDir)
	if err != nil {
		t.Fatalf("oci.New: %v", err)
	}

	tag := "v1"
	if err := pushToTarget(ctx, tarPath, pushManifest, target, tag); err != nil {
		t.Fatalf("pushToTarget: %v", err)
	}

	desc, err := target.Resolve(ctx, tag)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	rc, err := target.Fetch(ctx, desc)
	if err != nil {
		t.Fatalf("Fetch manifest: %v", err)
	}
	defer rc.Close()
	raw := make([]byte, desc.Size)
	if _, err := rc.Read(raw); err != nil && err.Error() != "EOF" {
		t.Fatalf("read manifest: %v", err)
	}
	manifestJSON := string(raw)
	if !strings.Contains(manifestJSON, pushManifest.ContentHash) {
		t.Errorf("OCI manifest doesn't carry the content_hash annotation (%s): %s", pushManifest.ContentHash, manifestJSON)
	}
	if !strings.Contains(manifestJSON, pushManifest.Name) {
		t.Errorf("OCI manifest doesn't carry the blueprint name annotation (%s): %s", pushManifest.Name, manifestJSON)
	}
}
