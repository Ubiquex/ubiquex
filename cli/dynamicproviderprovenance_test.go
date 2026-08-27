package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// realGitRepo builds a real, hermetic git repository in a temp directory
// (git init, real commit, real config -- no network, no dependency on
// this machine's own global git config) and returns its path. Mirrors
// this codebase's own "real git build, never a stubbed one" discipline
// (buildGeneratedRepo and friends).
func realGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out.String())
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.invalid/fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "initial")
	return dir
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func TestComputeDynamicProviderProvenance_ExplicitBinary_NoCheckoutInspected(t *testing.T) {
	prov, err := computeDynamicProviderProvenance("/some/prebuilt/binary", "/nonexistent/repo/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Source != "explicit-binary" {
		t.Fatalf("Source = %q, want explicit-binary", prov.Source)
	}
	if prov.Commit != "" || prov.RepoPath != "" || prov.Dirty || prov.Unpushed {
		t.Fatalf("explicit-binary provenance should carry no checkout-derived fields, got %+v", prov)
	}
	if prov.clean() {
		t.Fatal("explicit-binary must never be clean() -- provenance is genuinely unknown, not confirmed good")
	}
}

func TestComputeDynamicProviderProvenance_CleanAndPushed(t *testing.T) {
	dir := realGitRepo(t)
	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("remote", "add", "origin", remote)
	run("push", "-q", "-u", "origin", "HEAD:refs/heads/main")

	wantCommit := gitOutput(t, dir, "rev-parse", "HEAD")

	prov, err := computeDynamicProviderProvenance("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Source != "local-checkout" {
		t.Fatalf("Source = %q, want local-checkout", prov.Source)
	}
	if prov.Commit != wantCommit {
		t.Fatalf("Commit = %q, want %q", prov.Commit, wantCommit)
	}
	if prov.Dirty {
		t.Fatal("expected Dirty=false for a freshly committed, unmodified checkout")
	}
	if prov.Unpushed {
		t.Fatal("expected Unpushed=false -- HEAD was pushed to origin/main and tracks it")
	}
	if !prov.clean() {
		t.Fatalf("expected clean()=true, got %+v", prov)
	}
}

func TestComputeDynamicProviderProvenance_Dirty(t *testing.T) {
	dir := realGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.invalid/fixture\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prov, err := computeDynamicProviderProvenance("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !prov.Dirty {
		t.Fatal("expected Dirty=true for a real uncommitted modification")
	}
	if prov.clean() {
		t.Fatal("a dirty checkout must never report clean()=true")
	}
}

func TestComputeDynamicProviderProvenance_UntrackedFileCountsAsDirty(t *testing.T) {
	// Real, live-found shape this test guards against regressing: `git
	// status --porcelain` reports a real untracked file (an entirely new
	// package like ubx-provider-dynamic's own internal/dsfilter/ before
	// its first `git add`) exactly the same way it reports a modified
	// tracked one -- both are real, uncommitted content a later session
	// has no way to reconstruct from the commit alone.
	dir := realGitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "untracked.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prov, err := computeDynamicProviderProvenance("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !prov.Dirty {
		t.Fatal("expected Dirty=true for a real untracked file")
	}
}

func TestComputeDynamicProviderProvenance_NoUpstream_Unpushed(t *testing.T) {
	dir := realGitRepo(t)

	prov, err := computeDynamicProviderProvenance("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !prov.Unpushed {
		t.Fatal("expected Unpushed=true when no upstream is configured at all")
	}
	if prov.Dirty {
		t.Fatal("expected Dirty=false -- the working tree itself is clean, only the push state is the problem")
	}
	if prov.clean() {
		t.Fatal("unpushed must never report clean()=true")
	}
}

func TestComputeDynamicProviderProvenance_CommitsAheadOfUpstream_Unpushed(t *testing.T) {
	dir := realGitRepo(t)
	remote := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", remote).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("remote", "add", "origin", remote)
	run("push", "-q", "-u", "origin", "HEAD:refs/heads/main")

	// A real second commit, never pushed -- exactly the shape this
	// session found live: real WIP committed to a local branch, its own
	// remote never updated.
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "real wip, never pushed")

	prov, err := computeDynamicProviderProvenance("", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Dirty {
		t.Fatal("expected Dirty=false -- the second commit is real, committed content, not uncommitted")
	}
	if !prov.Unpushed {
		t.Fatal("expected Unpushed=true -- HEAD is one real commit ahead of origin/main")
	}
}

func TestCheckDynamicProviderProvenance_Clean_NoErrorJustLogs(t *testing.T) {
	var buf bytes.Buffer
	prov := dynamicProviderProvenance{Source: "local-checkout", Commit: "abc123", RepoPath: "/repo"}
	if err := checkDynamicProviderProvenance(&buf, prov, false); err != nil {
		t.Fatalf("unexpected error for clean provenance: %v", err)
	}
	if !strings.Contains(buf.String(), "abc123") {
		t.Fatalf("expected the real commit to be logged, got: %s", buf.String())
	}
	if strings.Contains(buf.String(), "WARNING") {
		t.Fatalf("a clean provenance record should never print a warning, got: %s", buf.String())
	}
}

func TestCheckDynamicProviderProvenance_Dirty_WarnsButProceeds(t *testing.T) {
	var buf bytes.Buffer
	prov := dynamicProviderProvenance{Source: "local-checkout", Commit: "abc123", RepoPath: "/repo", Dirty: true}
	if err := checkDynamicProviderProvenance(&buf, prov, false); err != nil {
		t.Fatalf("expected no error when requireClean=false (this is the whole point -- local iteration must not break), got: %v", err)
	}
	if !strings.Contains(buf.String(), "WARNING") {
		t.Fatalf("expected a loud warning for dirty provenance, got: %s", buf.String())
	}
}

func TestCheckDynamicProviderProvenance_Dirty_RequireClean_Refuses(t *testing.T) {
	prov := dynamicProviderProvenance{Source: "local-checkout", Commit: "abc123", RepoPath: "/repo", Dirty: true}
	err := checkDynamicProviderProvenance(&bytes.Buffer{}, prov, true)
	if err == nil {
		t.Fatal("expected an error when requireClean=true and the checkout is dirty")
	}
	if !strings.Contains(err.Error(), "require-clean-provenance") {
		t.Fatalf("expected the error to name the flag that would need dropping, got: %v", err)
	}
}

func TestCheckDynamicProviderProvenance_Unpushed_RequireClean_Refuses(t *testing.T) {
	prov := dynamicProviderProvenance{Source: "local-checkout", Commit: "abc123", RepoPath: "/repo", Unpushed: true}
	err := checkDynamicProviderProvenance(&bytes.Buffer{}, prov, true)
	if err == nil {
		t.Fatal("expected an error when requireClean=true and the checkout is unpushed")
	}
}

func TestCheckDynamicProviderProvenance_ExplicitBinary_RequireClean_Refuses(t *testing.T) {
	// An explicit --dynamic-provider-bin path is honestly-unknown
	// provenance, not confirmed-clean -- --require-clean-provenance must
	// refuse this too, not just the dirty/unpushed local-checkout cases.
	prov := dynamicProviderProvenance{Source: "explicit-binary"}
	err := checkDynamicProviderProvenance(&bytes.Buffer{}, prov, true)
	if err == nil {
		t.Fatal("expected an error for explicit-binary provenance when requireClean=true")
	}
}

func TestCheckDynamicProviderProvenance_ExplicitBinary_WarnsWithoutRequireClean(t *testing.T) {
	var buf bytes.Buffer
	prov := dynamicProviderProvenance{Source: "explicit-binary"}
	if err := checkDynamicProviderProvenance(&buf, prov, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "WARNING") {
		t.Fatalf("expected a loud warning for explicit-binary provenance, got: %s", buf.String())
	}
}
