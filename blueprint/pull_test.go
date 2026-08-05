package blueprint

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeMinimalTarGz writes a real, valid gzipped tar at path containing
// exactly files (name -> content) -- for a hermetic test that needs a
// real tarball shape without going through Package (e.g. proving Pull
// refuses a tarball that's real but not a blueprint).
func writeMinimalTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(content)), Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
}

// tamperTarGzEntry rewrites tarPath in place, replacing entryName's own
// content with newContent (every other entry copied through unchanged)
// -- simulates a tarball corrupted/tampered with AFTER a real Package
// call, the exact scenario content-hash verification (not tarball
// structure) is what catches.
func tamperTarGzEntry(t *testing.T, tarPath, entryName, newContent string) {
	t.Helper()
	orig, err := os.Open(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	gr, err := gzip.NewReader(orig)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gr)

	tmpPath := tarPath + ".tmp"
	out, err := os.Create(tmpPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(out)
	tw := tar.NewWriter(gw)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == entryName {
			raw = []byte(newContent)
			hdr.Size = int64(len(raw))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gr.Close(); err != nil {
		t.Fatal(err)
	}
	if err := orig.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpPath, tarPath); err != nil {
		t.Fatal(err)
	}
}

func TestPull_Local_CopiesDirectoryExcludingDotfiles(t *testing.T) {
	src := writeSampleBuiltBlueprint(t)
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "pulled")
	got, err := Pull(context.Background(), src, dest, "", "")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got != dest {
		t.Errorf("Pull returned %q, want %q", got, dest)
	}

	if _, err := os.Stat(filepath.Join(dest, UbxfileName)); err != nil {
		t.Errorf("pulled dest missing %s: %v", UbxfileName, err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git leaked into the pulled copy (err = %v)", err)
	}
}

func TestPull_Local_DestNotEmpty_Errors(t *testing.T) {
	src := writeSampleBuiltBlueprint(t)
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "already-here.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Pull(context.Background(), src, dest, "", ""); err == nil {
		t.Fatal("Pull: want error for a non-empty dest, got nil")
	}
}

// initTestGitRepo creates a real, throwaway git repository under a fresh
// temp dir -- author identity is set via per-command `-c` flags scoped to
// this repo only, never touching the real machine's/user's own git
// config (CLAUDE.md's own git-identity rule is about THIS project's own
// commits; a disposable repo a test creates and destroys is a different
// thing entirely).
func initTestGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--quiet")
	return dir
}

func gitCommitAll(t *testing.T, repoDir, message string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("add", "-A")
	run("-c", "user.name=ubx-test", "-c", "user.email=ubx-test@example.com", "commit", "--quiet", "-m", message)
}

func TestPull_Git_ClonesChecksOutRefAndPath(t *testing.T) {
	repoDir := initTestGitRepo(t)

	// A first commit with no blueprint at all -- proves ref actually
	// matters, not just "whatever HEAD happens to be."
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repoDir, "initial commit, no blueprint yet")

	// A second commit adds the blueprint under a subdirectory, tagged v1.
	bpDir := filepath.Join(repoDir, "ci-platform")
	if err := os.MkdirAll(bpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bpDir, UbxfileName), []byte("lang: go\n\nresources: |\n  An ECR repository.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bpDir, "go.mod"), []byte("module ciplatform\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repoDir, "add ci-platform blueprint")
	tag := exec.Command("git", "tag", "v1")
	tag.Dir = repoDir
	if out, err := tag.CombinedOutput(); err != nil {
		t.Fatalf("git tag v1: %v: %s", err, out)
	}

	// file:// forces Pull down the git-clone path even though the
	// "repository" is itself a local directory -- os.Stat(source) would
	// otherwise treat it as an ordinary local blueprint dir and just copy
	// it verbatim, never exercising gitClone/gitCheckout at all.
	source := "file://" + repoDir
	dest := filepath.Join(t.TempDir(), "pulled")

	got, err := Pull(context.Background(), source, dest, "v1", "ci-platform")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got != dest {
		t.Errorf("Pull returned %q, want %q", got, dest)
	}
	if _, err := os.Stat(filepath.Join(dest, UbxfileName)); err != nil {
		t.Errorf("pulled dest missing %s: %v", UbxfileName, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "go.mod")); err != nil {
		t.Errorf("pulled dest missing go.mod: %v", err)
	}
	// The root-level README.md from the first commit must NOT appear --
	// proves --path genuinely scoped the pull to the subdirectory.
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README.md (outside --path) leaked into the pulled copy (err = %v)", err)
	}
}

func TestPull_Git_WrongPath_Errors(t *testing.T) {
	repoDir := initTestGitRepo(t)
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repoDir, "initial commit")

	source := "file://" + repoDir
	dest := filepath.Join(t.TempDir(), "pulled")

	if _, err := Pull(context.Background(), source, dest, "", "does-not-exist"); err == nil {
		t.Fatal("Pull: want error for a --path that doesn't exist in the repo, got nil")
	}
}

func TestPull_UnreachableSource_Errors(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "pulled")
	if _, err := Pull(context.Background(), "file:///no/such/repo/anywhere", dest, "", ""); err == nil {
		t.Fatal("Pull: want error for an unreachable git source, got nil")
	}
}

// TestPull_BareTarballFile_ExtractsWithoutNetwork is UBI-74 Slice 8's own
// required hermetic proof (item 1): a bare tarball FILE (not a
// directory, not a URL) is detected purely by os.Stat's own IsDir bit --
// the exact same source-detection logic Slice 3's local-directory branch
// already uses, extended without introducing any ambiguity between "a
// local blueprint directory" and "a local tarball file" (a file can
// never satisfy info.IsDir(), so the existing dispatch already
// disambiguates the two source types for free). No network call is
// possible on this path at all -- extractTarGz is pure local file I/O,
// confirmed by this test's own use of an http_proxy environment variable
// pointing nowhere reachable, so any accidental network attempt would
// hang/fail loudly rather than silently succeed.
func TestPull_BareTarballFile_ExtractsWithoutNetwork(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")

	dir := writeSampleBuiltBlueprint(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform-v1.tar.gz")
	original, err := Package(dir, tarPath)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "pulled")
	got, err := Pull(context.Background(), tarPath, dest, "", "")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got != dest {
		t.Errorf("Pull returned %q, want %q", got, dest)
	}

	if _, err := os.Stat(filepath.Join(dest, UbxfileName)); err != nil {
		t.Errorf("pulled dest missing %s: %v", UbxfileName, err)
	}

	verified, err := Verify(dest)
	if err != nil {
		t.Fatalf("Verify(pulled): %v", err)
	}
	if verified.ContentHash != original.ContentHash {
		t.Errorf("pulled ContentHash = %q, want %q", verified.ContentHash, original.ContentHash)
	}
}

// TestPull_BareTarballFile_RefPathFlagsRefused mirrors the oci:// source
// type's own --ref/--path refusal (pull.go) -- neither flag means
// anything for a bare tarball file, so silently ignoring them would be
// misleading.
func TestPull_BareTarballFile_RefPathFlagsRefused(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform-v1.tar.gz")
	if _, err := Package(dir, tarPath); err != nil {
		t.Fatalf("Package: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "pulled")
	if _, err := Pull(context.Background(), tarPath, dest, "some-ref", ""); err == nil {
		t.Fatal("Pull: want error for --ref against a bare tarball file, got nil")
	}
}

// TestPull_BareTarballFile_MissingUbxfile_Errors confirms a real gzipped
// tarball that just isn't a blueprint (no Ubxfile inside) is refused
// with a clear, named reason rather than silently accepted.
func TestPull_BareTarballFile_MissingUbxfile_Errors(t *testing.T) {
	tarPath := filepath.Join(t.TempDir(), "not-a-blueprint.tar.gz")
	writeMinimalTarGz(t, tarPath, map[string]string{"hello.txt": "hello"})

	dest := filepath.Join(t.TempDir(), "pulled")
	if _, err := Pull(context.Background(), tarPath, dest, "", ""); err == nil {
		t.Fatal("Pull: want error for a tarball with no Ubxfile, got nil")
	}
}

// TestPull_BareTarballFile_TamperedContent_VerifyFails is Slice 8's own
// required proof for item 2: this delivery mode has no git history or
// registry-native integrity to lean on (the original design record's own
// framing) -- content-hash verification, unchanged since Slice 3, is
// what actually protects it. A tarball whose own tar-level bytes were
// altered AFTER packaging (simulating tampering in transit -- an email
// attachment swapped, a support-ticket upload corrupted) extracts
// without complaint (Pull itself never trusts the declared hash, exactly
// like every other source type), but Verify -- recomputing the hash from
// what's actually on disk -- catches the mismatch.
func TestPull_BareTarballFile_TamperedContent_VerifyFails(t *testing.T) {
	dir := writeSampleBuiltBlueprint(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform-v1.tar.gz")
	if _, err := Package(dir, tarPath); err != nil {
		t.Fatalf("Package: %v", err)
	}

	tamperTarGzEntry(t, tarPath, "ciplatform.go", "package ciplatform\n\n// tampered in transit\n")

	dest := filepath.Join(t.TempDir(), "pulled")
	if _, err := Pull(context.Background(), tarPath, dest, "", ""); err != nil {
		t.Fatalf("Pull (of a tampered but structurally valid tarball): %v", err)
	}

	if _, err := Verify(dest); err == nil {
		t.Fatal("Verify: want an error for tampered content pulled from a bare tarball, got nil")
	}
}
