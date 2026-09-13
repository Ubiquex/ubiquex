package blueprint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A blueprint's content hash is its identity, so a dependency directory
// inside it made that identity change on every reinstall. A real
// TypeScript blueprint of three files packaged 13,922 of them.

// writeBlueprintWithDeps writes a minimal Go code blueprint plus one
// directory of each shape that must never be packaged.
func writeBlueprintWithDeps(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dep-bp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("go.mod", "module github.com/ubx-blueprints/dep-bp\n\ngo 1.23\n")
	write("bp.go", "package depbp\n\ntype Config struct{ Name string }\ntype Outputs struct{}\n\nfunc DepBp(cfg Config) Outputs { return Outputs{} }\n")

	// One file in each excluded shape. venv is undotted deliberately:
	// see below.
	write("node_modules/left-pad/index.js", "module.exports = 1;")
	write("vendor/github.com/x/y/y.go", "package y\n")
	write("venv/lib/site.py", "x = 1\n")
	write(".venv/lib/site.py", "x = 1\n")
	write("__pycache__/bp.cpython-311.pyc", "bytecode")
	write(".git/config", "[core]\n")
	return dir
}

func TestHashFiles_ExcludesDependencyDirectories(t *testing.T) {
	dir := writeBlueprintWithDeps(t)

	files, excluded, err := hashFiles(dir)
	if err != nil {
		t.Fatal(err)
	}

	for rel := range files {
		for _, bad := range []string{"node_modules", "vendor", "venv", ".venv", "__pycache__", ".git"} {
			if strings.HasPrefix(rel, bad+"/") {
				t.Errorf("%s was packaged", rel)
			}
		}
	}
	if len(files) != 2 {
		t.Errorf("packaged %d files, want just go.mod and bp.go: %v", len(files), files)
	}

	// Only the named dependency directories are reported. A dot-prefixed
	// entry is skipped for its own separate reason and is not a
	// dependency, so naming it would be misleading.
	want := "node_modules, vendor, venv, __pycache__"
	got := strings.Join(excluded, ", ")
	if sortedCSV(got) != sortedCSV(want) {
		t.Errorf("excluded = %q, want %q", got, want)
	}
}

// The undotted spelling is the load-bearing case. `.venv` was excluded
// long before this, but only because it begins with a dot: nothing knew
// it was a virtualenv. `venv` was packaged, and pyeval's own
// venvSitePackages treats BOTH spellings as a real virtualenv, so a
// Python author following Python's own documentation (`python -m venv
// venv`) hit exactly the TypeScript problem. Python was never right
// here, only lucky about which spelling it used.
func TestHashFiles_UndottedVenvIsExcludedToo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "py-bp")
	if err := os.MkdirAll(filepath.Join(dir, "venv", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bp.py"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "venv", "lib", "site.py"), []byte("y = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, excluded, err := hashFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Errorf("packaged %d files, want only bp.py: %v", len(files), files)
	}
	if len(excluded) != 1 || excluded[0] != "venv" {
		t.Errorf("excluded = %v, want [venv]", excluded)
	}
}

// The property that matters: reinstalling dependencies must not change
// a blueprint's identity.
func TestBuildManifest_HashSurvivesAReinstall(t *testing.T) {
	dir := writeBlueprintWithDeps(t)

	before, err := buildManifest(dir, "dep-bp")
	if err != nil {
		t.Fatal(err)
	}

	// A reinstall: every existing dependency file rewritten, plus a new
	// transitive package that was not there before.
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "left-pad", "index.js"), []byte("module.exports = 2;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "brand-new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "brand-new", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	after, err := buildManifest(dir, "dep-bp")
	if err != nil {
		t.Fatal(err)
	}
	if before.ContentHash != after.ContentHash {
		t.Errorf("reinstalling dependencies changed the blueprint's identity:\n  before: %s\n  after:  %s",
			before.ContentHash, after.ContentHash)
	}
}

// Authored content that merely lives beside a dependency directory is
// still packaged. The exclusion is by directory name at any depth, not
// a prefix match on a path.
func TestHashFiles_OnlySkipsTheDirectoriesThemselves(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bp")
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"bp.go", "vendored.go", "internal/node_modules_helper.go"} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte("package bp\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files, excluded, err := hashFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Errorf("packaged %d files, want all 3: %v", len(files), files)
	}
	if len(excluded) != 0 {
		t.Errorf("excluded = %v, want none", excluded)
	}
}

func TestPackageReportingExclusions_NamesWhatItLeftOut(t *testing.T) {
	dir := writeBlueprintWithDeps(t)

	_, excluded, err := PackageReportingExclusions(context.Background(), dir, filepath.Join(t.TempDir(), "out.tar.gz"))
	if err != nil {
		t.Fatalf("PackageReportingExclusions: %v", err)
	}
	if len(excluded) == 0 {
		t.Fatal("package reported no exclusions for a tree full of them")
	}
	for _, want := range []string{"node_modules", "vendor", "venv", "__pycache__"} {
		found := false
		for _, got := range excluded {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s was excluded but not reported: %v", want, excluded)
		}
	}
}

func sortedCSV(s string) string {
	parts := strings.Split(s, ", ")
	for i := 0; i < len(parts); i++ {
		for j := i + 1; j < len(parts); j++ {
			if parts[j] < parts[i] {
				parts[i], parts[j] = parts[j], parts[i]
			}
		}
	}
	return strings.Join(parts, ", ")
}
