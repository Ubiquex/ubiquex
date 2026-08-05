package blueprint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSamplePyDepBlueprint writes a real, on-disk, buildable blueprint
// directory named name (its own basename, matching this codebase's
// established "directory basename IS the blueprint name" convention,
// Package.go's own `name := filepath.Base(absDir)`) with a real py/
// package -- a bindings.py + a "<pkg>.py" defining one function that
// calls sdk.resource, the exact shape GeneratePython itself produces
// (pygen.go), just hand-written rather than generated, since these tests
// are about pull-before-import sequencing, not codegen -- and a real
// blueprint.lock.json (via Package, the same call `ubx blueprint package`
// itself makes).
func writeSamplePyDepBlueprint(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "py"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, UbxfileName), []byte("lang: py\n\nresources: |\n  A trivial fake widget.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bindings := `import dataclasses
from typing import Any

import ubx_sdk as sdk

FakeWidget = sdk.ResourceBinding(
    wire_type="fake_widget",
    fields={"name": sdk.FieldSpec(wire_name="name")},
)


@dataclasses.dataclass
class FakeWidgetConfig:
    name: Any = None
`
	if err := os.WriteFile(filepath.Join(dir, "py", "bindings.py"), []byte(bindings), 0o644); err != nil {
		t.Fatal(err)
	}
	fn := `import bindings
import ubx_sdk as sdk


def add_widget(name):
    sdk.resource(bindings.FakeWidget, "primary", bindings.FakeWidgetConfig(name=name))
`
	if err := os.WriteFile(filepath.Join(dir, "py", "widgetlib.py"), []byte(fn), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(dir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}
	return dir
}

func writeRequirementsTxt(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, PyRequirementsFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------
// Parsing (ParsePyDependencies / parsePyRequirementURL)
// ---------------------------------------------------------------------

func TestParsePyDependencies_OCI(t *testing.T) {
	dir := t.TempDir()
	writeRequirementsTxt(t, dir, "ci-platform @ oci://ghcr.io/ubiquex/ci-platform:v3\n")

	deps, err := ParsePyDependencies(dir)
	if err != nil {
		t.Fatalf("ParsePyDependencies: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("got %d deps, want 1: %+v", len(deps), deps)
	}
	d := deps[0]
	if d.Name != "ci-platform" || d.URL != "oci://ghcr.io/ubiquex/ci-platform:v3" || d.Source != "oci://ghcr.io/ubiquex/ci-platform:v3" || d.Ref != "" || d.Path != "" {
		t.Fatalf("unexpected parse: %+v", d)
	}
}

func TestParsePyDependencies_GitPlusRefAndSubdirectory(t *testing.T) {
	dir := t.TempDir()
	writeRequirementsTxt(t, dir, "widget-lib @ git+https://github.com/ubiquex/blueprints.git@v2#subdirectory=widget-lib\n")

	deps, err := ParsePyDependencies(dir)
	if err != nil {
		t.Fatalf("ParsePyDependencies: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("got %d deps, want 1", len(deps))
	}
	d := deps[0]
	if d.Source != "https://github.com/ubiquex/blueprints.git" {
		t.Errorf("Source = %q, want the git+ prefix stripped and @ref/#fragment removed", d.Source)
	}
	if d.Ref != "v2" {
		t.Errorf("Ref = %q, want %q", d.Ref, "v2")
	}
	if d.Path != "widget-lib" {
		t.Errorf("Path = %q, want %q", d.Path, "widget-lib")
	}
}

func TestParsePyDependencies_GitPlusSSHUserInfo_NotMistakenForRef(t *testing.T) {
	dir := t.TempDir()
	writeRequirementsTxt(t, dir, "widget-lib @ git+ssh://git@github.com/ubiquex/blueprints.git\n")

	deps, err := ParsePyDependencies(dir)
	if err != nil {
		t.Fatalf("ParsePyDependencies: %v", err)
	}
	d := deps[0]
	if d.Source != "ssh://git@github.com/ubiquex/blueprints.git" {
		t.Errorf("Source = %q, want the ssh user-info @ left untouched (not mistaken for a ref)", d.Source)
	}
	if d.Ref != "" {
		t.Errorf("Ref = %q, want empty (no @ref was actually declared)", d.Ref)
	}
}

func TestParsePyDependencies_FileScheme(t *testing.T) {
	dir := t.TempDir()
	writeRequirementsTxt(t, dir, "widget-lib @ file:///abs/path/to/widget-lib\n")

	deps, err := ParsePyDependencies(dir)
	if err != nil {
		t.Fatalf("ParsePyDependencies: %v", err)
	}
	if deps[0].Source != "/abs/path/to/widget-lib" {
		t.Errorf("Source = %q, want the file:// scheme stripped", deps[0].Source)
	}
}

func TestParsePyDependencies_BareLocalPath(t *testing.T) {
	dir := t.TempDir()
	writeRequirementsTxt(t, dir, "widget-lib @ ../shared/widget-lib\n")

	deps, err := ParsePyDependencies(dir)
	if err != nil {
		t.Fatalf("ParsePyDependencies: %v", err)
	}
	if deps[0].Source != "../shared/widget-lib" {
		t.Errorf("Source = %q, want the bare path verbatim", deps[0].Source)
	}
}

func TestParsePyDependencies_SkipsOrdinaryRequirementsAndComments(t *testing.T) {
	dir := t.TempDir()
	writeRequirementsTxt(t, dir, "# a comment\n\nrequests==2.31.0\nci-platform @ oci://ghcr.io/ubiquex/ci-platform:v3\n")

	deps, err := ParsePyDependencies(dir)
	if err != nil {
		t.Fatalf("ParsePyDependencies: %v", err)
	}
	if len(deps) != 1 || deps[0].Name != "ci-platform" {
		t.Fatalf("want exactly the one \"@ url\" entry, got %+v", deps)
	}
}

func TestParsePyDependencies_MissingFile_ReturnsNilNil(t *testing.T) {
	deps, err := ParsePyDependencies(t.TempDir())
	if err != nil || deps != nil {
		t.Fatalf("ParsePyDependencies with no requirements.txt = (%v, %v), want (nil, nil)", deps, err)
	}
}

func TestParsePyDependencies_MalformedLine_Errors(t *testing.T) {
	dir := t.TempDir()
	writeRequirementsTxt(t, dir, "ci-platform @ \n")
	if _, err := ParsePyDependencies(dir); err == nil {
		t.Fatal("want an error for a malformed \"name @\" line with no URL")
	}
}

func TestParsePyDependencies_UnrecognizedScheme_Errors(t *testing.T) {
	dir := t.TempDir()
	writeRequirementsTxt(t, dir, "ci-platform @ ftp://example.com/ci-platform\n")
	if _, err := ParsePyDependencies(dir); err == nil {
		t.Fatal("want an error for an unrecognized scheme")
	}
}

// ---------------------------------------------------------------------
// Resolution: pull + verify (+ cache) sequencing
// ---------------------------------------------------------------------

func TestResolvePyDependencies_LocalPath_MountsAndVerifies(t *testing.T) {
	bpDir := writeSamplePyDepBlueprint(t, "widget-lib")

	progDir := t.TempDir()
	writeRequirementsTxt(t, progDir, "widget-lib @ "+bpDir+"\n")

	mounts, err := ResolvePyDependencies(context.Background(), filepath.Join(progDir, "main.py"))
	if err != nil {
		t.Fatalf("ResolvePyDependencies: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("got %d mounts, want 1", len(mounts))
	}
	m := mounts[0]
	if _, err := os.Stat(filepath.Join(m.HostDir, "widgetlib.py")); err != nil {
		t.Errorf("HostDir %s missing widgetlib.py: %v", m.HostDir, err)
	}
	if !strings.HasPrefix(m.Receipt, "pulled widget-lib @ "+bpDir) {
		t.Errorf("Receipt = %q, doesn't name the pulled dependency", m.Receipt)
	}
	if !strings.Contains(m.Receipt, "verified: content hash sha256:") {
		t.Errorf("Receipt = %q, missing a verified content hash -- UBI-130's own required receipt-line content", m.Receipt)
	}
}

func TestResolvePyDependencies_NameMismatch_Errors(t *testing.T) {
	bpDir := writeSamplePyDepBlueprint(t, "widget-lib")

	progDir := t.TempDir()
	// Declares a DIFFERENT name than the pulled blueprint's own declared
	// (directory-basename-derived) name -- a real integrity check, not a
	// silent accept of whatever got pulled.
	writeRequirementsTxt(t, progDir, "totally-different-name @ "+bpDir+"\n")

	if _, err := ResolvePyDependencies(context.Background(), filepath.Join(progDir, "main.py")); err == nil {
		t.Fatal("want an error when requirements.txt's declared name doesn't match the pulled blueprint's own name")
	}
}

func TestResolvePyDependencies_MissingPyPackage_Errors(t *testing.T) {
	bpDir := writeSampleBuiltBlueprint(t) // go-only fixture (package_test.go), no py/
	if _, err := Package(bpDir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}

	progDir := t.TempDir()
	writeRequirementsTxt(t, progDir, filepath.Base(bpDir)+" @ "+bpDir+"\n")

	_, err := ResolvePyDependencies(context.Background(), filepath.Join(progDir, "main.py"))
	if err == nil || !strings.Contains(err.Error(), "no built py/ package") {
		t.Fatalf("ResolvePyDependencies: got %v, want a \"no built py/ package\" error", err)
	}
}

func TestResolvePyDependencies_Git_CachesAndSurvivesSourceRemoval(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := initTestGitRepo(t)
	bpDir := filepath.Join(repoDir, "blueprints", "widget-lib")
	if err := os.MkdirAll(filepath.Join(bpDir, "py"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bpDir, UbxfileName), []byte("lang: py\n\nresources: |\n  A trivial fake widget.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bpDir, "py", "widgetlib.py"), []byte("def add_widget(name):\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// blueprint.lock.json, computed the same way `ubx blueprint package`
	// itself would (buildManifest+writeManifest, package.go's own Package
	// does exactly this against "widget-lib", the directory's own
	// basename).
	if _, err := Package(bpDir, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("Package: %v", err)
	}
	gitCommitAll(t, repoDir, "add widget-lib blueprint")
	tagCmd := exec.Command("git", "tag", "v1")
	tagCmd.Dir = repoDir
	if out, err := tagCmd.CombinedOutput(); err != nil {
		t.Fatalf("git tag v1: %v: %s", err, out)
	}

	// Isolate the blueprint cache under a fresh $HOME so this test never
	// touches (or is polluted by) the real ~/.ubx/blueprints.
	t.Setenv("HOME", t.TempDir())

	progDir := t.TempDir()
	writeRequirementsTxt(t, progDir, "widget-lib @ git+file://"+repoDir+"@v1#subdirectory=blueprints/widget-lib\n")
	entryFile := filepath.Join(progDir, "main.py")

	first, err := ResolvePyDependencies(context.Background(), entryFile)
	if err != nil {
		t.Fatalf("first ResolvePyDependencies: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("got %d mounts, want 1", len(first))
	}
	if strings.Contains(first[0].Receipt, "(cached)") {
		t.Errorf("first resolve's own Receipt = %q, should be a fresh pull, not a cache hit", first[0].Receipt)
	}

	// Remove the real source entirely -- a second resolve that still
	// succeeds proves the cache hit genuinely never touches git/network
	// again, not just that the receipt SAYS "(cached)".
	if err := os.RemoveAll(repoDir); err != nil {
		t.Fatal(err)
	}

	second, err := ResolvePyDependencies(context.Background(), entryFile)
	if err != nil {
		t.Fatalf("second ResolvePyDependencies (source removed): %v", err)
	}
	if !strings.Contains(second[0].Receipt, "(cached)") {
		t.Errorf("second resolve's own Receipt = %q, want a \"(cached)\" hit", second[0].Receipt)
	}
	if second[0].HostDir != first[0].HostDir {
		t.Errorf("HostDir changed across a cache hit: first %q, second %q", first[0].HostDir, second[0].HostDir)
	}
	if first[0].Dep.Name != second[0].Dep.Name {
		t.Errorf("Dep.Name changed across a cache hit")
	}
}

// ---------------------------------------------------------------------
// End to end: pull-before-import sequencing, proven against a real
// wasmtime subprocess -- not just that ResolvePyDependencies computes the
// right HostDir, but that a plain `from <pkg> import <fn>` in a
// completely separate, hand-written entry script actually resolves and
// runs, exactly UBI-130's own required success bar (short of the real
// GHCR artifact, which only a live network run can prove).
// ---------------------------------------------------------------------

func requireWasmtimeForBlueprintTests(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not found in PATH -- skipping UBI-130's own real-subprocess pull-before-import test")
	}
}

func TestEvaluatePythonWithDeps_RealPullBeforeImport(t *testing.T) {
	requireWasmtimeForBlueprintTests(t)

	bpDir := writeSamplePyDepBlueprint(t, "widget-lib")

	progDir := t.TempDir()
	writeRequirementsTxt(t, progDir, "widget-lib @ "+bpDir+"\n")
	driver := `import ubx_sdk as sdk
from widgetlib import add_widget


def describe():
    sdk.intent("UBI-130: pulled via requirements.txt, imported before this script ran")
    add_widget("from-pulled-dependency")


if __name__ == "__main__":
    sdk.run("payments", describe)
`
	entryFile := filepath.Join(progDir, "main.py")
	if err := os.WriteFile(entryFile, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second) // matching pyeval's own evalCtx budget
	defer cancel()

	canon, receipts, err := EvaluatePythonWithDeps(ctx, entryFile)
	if err != nil {
		t.Fatalf("EvaluatePythonWithDeps: %v", err)
	}
	if len(receipts) != 1 || !strings.Contains(receipts[0], "verified") {
		t.Fatalf("receipts = %v, want exactly one \"verified\" receipt line", receipts)
	}
	if !strings.Contains(string(canon), "fake_widget") || !strings.Contains(string(canon), "from-pulled-dependency") {
		t.Fatalf("evaluated output missing the resource the PULLED dependency's own add_widget() call should have produced:\n%s", canon)
	}
}
