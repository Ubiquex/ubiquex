package pyeval

import (
	"os"
	"path/filepath"
	"testing"
)

// A pip-installed distribution was invisible to an evaluated program no
// matter what the host's PYTHONPATH said, because the sandbox preopens
// only the embedded runtime and each ExtraDep. The docs' own Python
// hello world failed on it (UBI-260).

func mkVenv(t *testing.T, root, pyver string) string {
	t.Helper()
	sp := filepath.Join(root, "lib", "python"+pyver, "site-packages")
	if err := os.MkdirAll(sp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pyvenv.cfg"), []byte("home = /usr\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return sp
}

func TestVenvSitePackages_FoundBesideTheEntryScript(t *testing.T) {
	dir := t.TempDir()
	want := mkVenv(t, filepath.Join(dir, ".venv"), "3.12")
	if got := venvSitePackages(dir); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Walks up, so a stack in a subdirectory of the project still finds it.
func TestVenvSitePackages_FoundInAParentDirectory(t *testing.T) {
	dir := t.TempDir()
	want := mkVenv(t, filepath.Join(dir, ".venv"), "3.12")
	nested := filepath.Join(dir, "stacks", "payments")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := venvSitePackages(nested); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// pyvenv.cfg is the marker, not the directory name. A directory that
// merely happens to be called "venv" is not one, and mounting it would
// put an arbitrary host tree inside the sandbox.
func TestVenvSitePackages_RequiresPyvenvCfg(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".venv", "lib", "python3.12", "site-packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := venvSitePackages(dir); got != "" {
		t.Fatalf("a directory without pyvenv.cfg is not a venv, got %q", got)
	}
}

// No venv is the ordinary case and must stay silent, not an error.
func TestVenvSitePackages_AbsentIsEmpty(t *testing.T) {
	if got := venvSitePackages(t.TempDir()); got != "" {
		t.Fatalf("expected no venv, got %q", got)
	}
}

// The choice feeds an evaluation whose output is hashed, so a venv
// carrying more than one pythonX.Y directory must resolve the same way
// every run rather than by filesystem order.
func TestVenvSitePackages_DeterministicWithMultiplePythonDirs(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".venv")
	mkVenv(t, root, "3.12")
	mkVenv(t, root, "3.9")
	first := venvSitePackages(dir)
	for i := 0; i < 5; i++ {
		if got := venvSitePackages(dir); got != first {
			t.Fatalf("resolution is not deterministic: %q then %q", first, got)
		}
	}
	if filepath.Base(filepath.Dir(first)) != "python3.12" {
		t.Fatalf("expected the sorted-first python dir, got %q", first)
	}
}
