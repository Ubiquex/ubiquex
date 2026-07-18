package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withUserHomeDir points userHomeDir at dir for the duration of the
// calling test, restoring the previous (hermetic, TestMain-set) value
// afterward -- same convention as withConfigSearchDir.
func withUserHomeDir(t *testing.T, dir string) {
	t.Helper()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { userHomeDir = orig })
}

func mkGitBoundary(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// --- row 12 ------------------------------------------------------------

func TestCascadeCeiling_Row12_RootTrueStopsCollectionMidTree(t *testing.T) {
	grandparent := t.TempDir()
	writeCascadeFile(t, grandparent, "config", `github_repo = "acme/infra"`)

	parent := filepath.Join(grandparent, "parent")
	writeCascadeFile(t, parent, "config", `
root = true
tf_dir = "./terraform"
`)

	child := filepath.Join(parent, "child")
	writeCascadeFile(t, child, "config", `stack = "payments"`)

	withConfigSearchDir(t, child)
	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.Stack != "payments" {
		t.Errorf("Stack = %q, want payments", rc.Config.Stack)
	}
	if rc.Config.TFDir != "./terraform" {
		t.Errorf("TFDir = %q, want ./terraform (the root=true file's own other keys still apply)", rc.Config.TFDir)
	}
	if rc.Config.GithubRepo != "" {
		t.Errorf("GithubRepo = %q, want empty -- the grandparent, beyond the root=true ceiling, must never be read", rc.Config.GithubRepo)
	}
	if rc.CeilingReason != ceilingRootMarker {
		t.Errorf("CeilingReason = %q, want %q", rc.CeilingReason, ceilingRootMarker)
	}
	parentConfigFile := filepath.Join(parent, ".ubx", "config")
	if rc.CeilingPath != parentConfigFile {
		t.Errorf("CeilingPath = %q, want %q", rc.CeilingPath, parentConfigFile)
	}
	for _, f := range rc.Files {
		if strings.Contains(f, grandparent) && !strings.Contains(f, parent) {
			t.Errorf("Files contains a grandparent path, must not: %v", rc.Files)
		}
	}
}

// --- row 13 --------------------------------------------------------------

func TestCascadeCeiling_Row13_RootNotABoolean_HardError(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config", `root = "true"`)
	withConfigSearchDir(t, dir)

	_, err := LoadConfigResolved(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error -- root must be a literal boolean")
	}
	if !strings.Contains(err.Error(), "root") || !strings.Contains(err.Error(), "boolean") {
		t.Fatalf("expected the error to explain root must be a boolean, got: %v", err)
	}
}

// --- row 14 ----------------------------------------------------------------

func TestCascadeCeiling_Row14_RepoBoundaryStopsCollection(t *testing.T) {
	outside := t.TempDir()
	writeCascadeFile(t, outside, "config", `github_repo = "should-never-be-read"`)

	repoRoot := filepath.Join(outside, "repo")
	mkGitBoundary(t, repoRoot)
	writeCascadeFile(t, repoRoot, "config", `stack = "payments"`)

	withConfigSearchDir(t, repoRoot)
	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.Stack != "payments" {
		t.Errorf("Stack = %q, want payments (the repo-root's own config is still included)", rc.Config.Stack)
	}
	if rc.Config.GithubRepo != "" {
		t.Errorf("GithubRepo = %q, want empty -- outside the repo boundary, must never be read", rc.Config.GithubRepo)
	}
	if rc.CeilingReason != ceilingRepoBoundary {
		t.Errorf("CeilingReason = %q, want %q", rc.CeilingReason, ceilingRepoBoundary)
	}
	if rc.CeilingPath != repoRoot {
		t.Errorf("CeilingPath = %q, want %q", rc.CeilingPath, repoRoot)
	}
}

// --- row 15 ------------------------------------------------------------------

func TestCascadeCeiling_Row15_HomeIsCeilingOutsideAnyRepo(t *testing.T) {
	beyondHome := t.TempDir()
	writeCascadeFile(t, beyondHome, "config", `github_repo = "should-never-be-read"`)

	home := filepath.Join(beyondHome, "home")
	writeCascadeFile(t, home, "config", `stack = "home-fallback"`)

	withUserHomeDir(t, home)
	withConfigSearchDir(t, home)
	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.Stack != "home-fallback" {
		t.Errorf("Stack = %q, want home-fallback ($HOME's own config is still included)", rc.Config.Stack)
	}
	if rc.Config.GithubRepo != "" {
		t.Errorf("GithubRepo = %q, want empty -- beyond $HOME, must never be read", rc.Config.GithubRepo)
	}
	if rc.CeilingReason != ceilingHome {
		t.Errorf("CeilingReason = %q, want %q", rc.CeilingReason, ceilingHome)
	}
	if rc.CeilingPath != home {
		t.Errorf("CeilingPath = %q, want %q", rc.CeilingPath, home)
	}
}

// --- row 16 ------------------------------------------------------------------

func TestCascadeCeiling_Row16_FilesystemRootFallback(t *testing.T) {
	dir := t.TempDir()
	// No .git anywhere in this tree, and userHomeDir points somewhere
	// entirely unrelated -- neither of the first two rules can ever fire,
	// so the walk must degrade all the way to the filesystem root without
	// error, exactly like the original nearest-file-wins walk did.
	withUserHomeDir(t, filepath.Join(t.TempDir(), "unrelated-home"))
	withConfigSearchDir(t, dir)

	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.CeilingReason != ceilingFilesystemRoot {
		t.Errorf("CeilingReason = %q, want %q", rc.CeilingReason, ceilingFilesystemRoot)
	}
	if rc.CeilingPath != "/" {
		t.Errorf("CeilingPath = %q, want \"/\"", rc.CeilingPath)
	}
}

// --- row 17 --------------------------------------------------------------

func TestCascadeCeiling_Row17_UserGlobalProjectKeyRefusedLoudly(t *testing.T) {
	home := t.TempDir()
	writeCascadeFile(t, home, "config", `stack = "should-never-apply"`)
	withUserHomeDir(t, home)
	withConfigSearchDir(t, t.TempDir())

	_, err := LoadConfigResolved(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error -- stack is a project-truth key, not allowed in user-global config")
	}
	if !strings.Contains(err.Error(), "stack") || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected the error to name stack and explain it's not allowed, got: %v", err)
	}
}

// --- row 18 --------------------------------------------------------------

func TestCascadeCeiling_Row18_UserGlobalUnknownKeyAlsoRefusedLoudly(t *testing.T) {
	home := t.TempDir()
	writeCascadeFile(t, home, "config", `totally_made_up_key = "x"`)
	withUserHomeDir(t, home)
	withConfigSearchDir(t, t.TempDir())

	_, err := LoadConfigResolved(&bytes.Buffer{})
	if err == nil {
		t.Fatal("expected an error -- an unrecognized key is refused, not silently warned")
	}
	if !strings.Contains(err.Error(), "totally_made_up_key") {
		t.Fatalf("expected the error to name the key, got: %v", err)
	}
}

// --- row 19 ----------------------------------------------------------------

func TestCascadeCeiling_Row19_UserGlobalInitFormatWorks(t *testing.T) {
	home := t.TempDir()
	writeCascadeFile(t, home, "config", `init_format = "yaml"`)
	withUserHomeDir(t, home)

	projectDir := t.TempDir()
	withConfigSearchDir(t, projectDir)

	out, err := runUbx(t, nil, "init", "--dir", projectDir)
	if err != nil {
		t.Fatalf("ubx init: %v\noutput: %s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(projectDir, ".ubx", "config.yaml")); statErr != nil {
		t.Fatalf("expected .ubx/config.yaml to be written (init_format from user-global config), got: %v", statErr)
	}
}

func TestCascadeCeiling_Row19_ExplicitFormatFlagStillWins(t *testing.T) {
	home := t.TempDir()
	writeCascadeFile(t, home, "config", `init_format = "yaml"`)
	withUserHomeDir(t, home)

	projectDir := t.TempDir()
	withConfigSearchDir(t, projectDir)

	out, err := runUbx(t, nil, "init", "--dir", projectDir, "--format", "toml")
	if err != nil {
		t.Fatalf("ubx init: %v\noutput: %s", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(projectDir, ".ubx", "config.toml")); statErr != nil {
		t.Fatalf("expected .ubx/config.toml (explicit --format beats user-global init_format), got: %v", statErr)
	}
}

// --- row 20 ------------------------------------------------------------------

func TestCascadeCeiling_Row20_ProvenanceRendersRootMarkerCeiling(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config", `
root = true
stack = "payments"
`)
	withConfigSearchDir(t, dir)

	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	report := renderProvenance(rc)
	if !strings.Contains(report, "root marker") {
		t.Fatalf("expected the provenance report to name the root-marker ceiling, got:\n%s", report)
	}
	if !strings.Contains(report, filepath.Join(dir, ".ubx", "config")) {
		t.Fatalf("expected the provenance report to name the root-marker file, got:\n%s", report)
	}
}

func TestCascadeCeiling_Row20_ProvenanceRendersRepoBoundaryCeiling(t *testing.T) {
	dir := t.TempDir()
	mkGitBoundary(t, dir)
	writeCascadeFile(t, dir, "config", `stack = "payments"`)
	withConfigSearchDir(t, dir)

	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	report := renderProvenance(rc)
	if !strings.Contains(report, "repo boundary") || !strings.Contains(report, dir) {
		t.Fatalf("expected the provenance report to name the repo-boundary ceiling and dir, got:\n%s", report)
	}
}

func TestCascadeCeiling_Row20_ProvenanceRendersHomeCeiling(t *testing.T) {
	home := t.TempDir()
	writeCascadeFile(t, home, "config", `stack = "payments"`)
	withUserHomeDir(t, home)
	withConfigSearchDir(t, home)

	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	report := renderProvenance(rc)
	if !strings.Contains(report, "$HOME") || !strings.Contains(report, home) {
		t.Fatalf("expected the provenance report to name the $HOME ceiling, got:\n%s", report)
	}
}

func TestCascadeCeiling_Row20_ProvenanceRendersFilesystemRootCeiling(t *testing.T) {
	dir := t.TempDir()
	withUserHomeDir(t, filepath.Join(t.TempDir(), "unrelated-home"))
	withConfigSearchDir(t, dir)

	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	report := renderProvenance(rc)
	if !strings.Contains(report, "filesystem root") {
		t.Fatalf("expected the provenance report to name the filesystem-root ceiling, got:\n%s", report)
	}
}

// --- git boundary detected via a .git FILE (worktree/submodule), not only a directory ---

func TestCascadeCeiling_GitBoundary_FileNotJustDirectory(t *testing.T) {
	outside := t.TempDir()
	writeCascadeFile(t, outside, "config", `github_repo = "should-never-be-read"`)

	worktree := filepath.Join(outside, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCascadeFile(t, worktree, "config", `stack = "payments"`)

	withConfigSearchDir(t, worktree)
	rc, err := LoadConfigResolved(&bytes.Buffer{})
	if err != nil {
		t.Fatalf("LoadConfigResolved: %v", err)
	}
	if rc.Config.GithubRepo != "" {
		t.Errorf("GithubRepo = %q, want empty -- a .git FILE (worktree pointer) must stop the walk exactly like a .git directory", rc.Config.GithubRepo)
	}
	if rc.CeilingReason != ceilingRepoBoundary {
		t.Errorf("CeilingReason = %q, want %q", rc.CeilingReason, ceilingRepoBoundary)
	}
}
