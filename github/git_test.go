package github

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// testRepo creates a real, throwaway git repository with one commit adding
// path with the given content, returning the repo dir and the commit SHA.
// Real `git` commands throughout — no mocking, since these are read-only
// plumbing operations against a repo this test itself fully owns.
func testRepo(t *testing.T, path, content string) (repoDir, sha string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")

	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", path)
	run("commit", "-q", "-m", "init")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v\n%s", err, out)
	}
	return dir, string(bytesTrimNewline(out))
}

func bytesTrimNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func TestCommitExists_Real(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)

	exists, err := CommitExists(context.Background(), repoDir, sha)
	if err != nil {
		t.Fatalf("CommitExists: %v", err)
	}
	if !exists {
		t.Fatal("expected the real commit to exist")
	}
}

func TestCommitExists_NotInHistory(t *testing.T) {
	repoDir, _ := testRepo(t, "proposal.json", `{"schema_version":1}`)

	exists, err := CommitExists(context.Background(), repoDir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if err != nil {
		t.Fatalf("CommitExists: %v", err)
	}
	if exists {
		t.Fatal("expected a made-up SHA to not exist")
	}
}

func TestFileAtCommit_Present(t *testing.T) {
	repoDir, sha := testRepo(t, "proposals/pending.json", `{"schema_version":1,"stack":"payments"}`)

	content, err := FileAtCommit(context.Background(), repoDir, sha, "proposals/pending.json")
	if err != nil {
		t.Fatalf("FileAtCommit: %v", err)
	}
	if string(content) != `{"schema_version":1,"stack":"payments"}` {
		t.Fatalf("content = %q", content)
	}
}

func TestFileAtCommit_MissingPath(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)

	_, err := FileAtCommit(context.Background(), repoDir, sha, "does/not/exist.json")
	if !errors.Is(err, ErrFileNotFoundAtCommit) {
		t.Fatalf("got %v, want ErrFileNotFoundAtCommit", err)
	}
}
