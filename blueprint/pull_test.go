package blueprint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
