package blueprint

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writeSampleBuiltBlueprint writes a minimal, but structurally realistic,
// "already built" blueprint directory (an Ubxfile plus the kind of files
// `ubx blueprint build` itself would have written -- go.mod/bindings.go/
// <pkg>.go) -- Package/Verify/Pull's own tests don't need the Go source
// to actually compile, only that packaging/hashing/pulling treat it like
// any other real file.
func writeSampleBuiltBlueprint(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		UbxfileName:     "lang: go\n\nparams:\n  repo_name: string, required\n\nresources: |\n  An ECR repository called \"{repo_name}\".\n",
		"go.mod":        "module ciplatform\n\ngo 1.23\n",
		"bindings.go":   "package ciplatform\n\n// generated\n",
		"ciplatform.go": "package ciplatform\n\nfunc CIPlatform(repoName string) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func readTarGz(t *testing.T, path string) map[string][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()

	entries := map[string][]byte{}
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read %s: %v", hdr.Name, err)
		}
		entries[hdr.Name] = raw
		if !hdr.ModTime.IsZero() && hdr.ModTime.Unix() != 0 {
			t.Errorf("entry %s: ModTime = %v, want zeroed (epoch) for deterministic output", hdr.Name, hdr.ModTime)
		}
	}
	return entries
}

func TestPackage_ProducesManifestAndTarball(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	out := filepath.Join(t.TempDir(), "ci-platform.tar.gz")

	manifest, err := Package(dir, out)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}

	if manifest.Name != filepath.Base(dir) {
		t.Errorf("manifest.Name = %q, want %q", manifest.Name, filepath.Base(dir))
	}
	if !strings.HasPrefix(manifest.ContentHash, "sha256:") {
		t.Errorf("ContentHash = %q, want sha256: prefix", manifest.ContentHash)
	}
	// 4 source files, the manifest itself is excluded from its own Files map.
	if len(manifest.Files) != 4 {
		t.Fatalf("len(Files) = %d, want 4: %v", len(manifest.Files), manifest.Files)
	}

	// blueprint.lock.json must exist on disk now, alongside the source dir.
	lockRaw, err := os.ReadFile(filepath.Join(dir, ManifestFileName))
	if err != nil {
		t.Fatalf("read %s: %v", ManifestFileName, err)
	}
	if !strings.Contains(string(lockRaw), manifest.ContentHash) {
		t.Errorf("%s on disk doesn't contain the reported content hash", ManifestFileName)
	}

	// The tarball must contain exactly the manifest's own file set plus
	// the manifest itself, with byte-identical content.
	entries := readTarGz(t, out)
	wantNames := make([]string, 0, len(manifest.Files)+1)
	for name := range manifest.Files {
		wantNames = append(wantNames, name)
	}
	wantNames = append(wantNames, ManifestFileName)
	sort.Strings(wantNames)

	gotNames := make([]string, 0, len(entries))
	for name := range entries {
		gotNames = append(gotNames, name)
	}
	sort.Strings(gotNames)

	if strings.Join(wantNames, ",") != strings.Join(gotNames, ",") {
		t.Fatalf("tarball entries = %v, want %v", gotNames, wantNames)
	}

	original, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(entries["go.mod"], original) {
		t.Errorf("tarball's go.mod content doesn't match the source file's own bytes")
	}
}

func TestPackage_MissingUbxfile_Errors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(dir, filepath.Join(t.TempDir(), "out.tar.gz")); err == nil {
		t.Fatal("Package: want error for a directory with no Ubxfile, got nil")
	}
}

func TestPackage_DeterministicAcrossRuns(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	out1 := filepath.Join(t.TempDir(), "a.tar.gz")
	out2 := filepath.Join(t.TempDir(), "b.tar.gz")

	m1, err := Package(dir, out1)
	if err != nil {
		t.Fatalf("Package (1st): %v", err)
	}
	m2, err := Package(dir, out2)
	if err != nil {
		t.Fatalf("Package (2nd): %v", err)
	}
	if m1.ContentHash != m2.ContentHash {
		t.Fatalf("content hash changed across identical re-packaging: %s vs %s", m1.ContentHash, m2.ContentHash)
	}

	raw1, err := os.ReadFile(out1)
	if err != nil {
		t.Fatal(err)
	}
	raw2, err := os.ReadFile(out2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw1, raw2) {
		t.Error("tarball bytes differ across identical re-packaging -- want byte-identical output")
	}
}
