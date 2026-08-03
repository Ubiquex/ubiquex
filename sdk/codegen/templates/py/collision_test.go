package pytmpl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// TestCheckNoDuplicateDeclarations_DetectsRealCollision proves the checker
// catches a genuine Python module-namespace collision WITHIN one file --
// a `class Foo` followed by a `Foo = sdk.ResourceBinding(...)`
// module-level assignment, which Python itself never errors on (the
// second silently overwrites the first -- see this package's own
// resourceRenderer doc comment for the full, live-verified account,
// UBI-96).
func TestCheckNoDuplicateDeclarations_DetectsRealCollision(t *testing.T) {
	src := `@dataclasses.dataclass
class Foo:
    bar: Any = None

Foo = sdk.ResourceBinding(
    wire_type="aws_foo",
    fields={},
)
`
	err := CheckNoDuplicateDeclarations(src)
	if err == nil {
		t.Fatal("expected an error for `class Foo` + `Foo = sdk.ResourceBinding(...)` sharing one module name, got nil")
	}
	mustContain(t, err.Error(), "Foo")
}

func TestCheckNoDuplicateDeclarations_CleanSource_NoError(t *testing.T) {
	src := `@dataclasses.dataclass
class Foo:
    bar: Any = None

Baz = sdk.ResourceBinding(
    wire_type="aws_baz",
    fields={},
)
`
	if err := CheckNoDuplicateDeclarations(src); err != nil {
		t.Fatalf("expected no error for a collision-free module, got: %v", err)
	}
}

// TestCheckRepoNoDuplicateDeclarations_SameNameDifferentFiles_NeverCollides
// is this restructure's own central Python-specific proof, confirmed not
// assumed: the SAME identifier name in TWO DIFFERENT resource types' own
// modules is never a collision -- every Python module has its own
// independent namespace (unlike the old flat single-module shape UBI-96
// found broken).
func TestCheckRepoNoDuplicateDeclarations_SameNameDifferentFiles_NeverCollides(t *testing.T) {
	repo := map[string]string{
		"pyproject.toml":    `[project]`,
		"ecr/repository.py": "@dataclasses.dataclass\nclass Thing:\n    bar: Any = None\n",
		"iam/role.py":       "@dataclasses.dataclass\nclass Thing:\n    bar: Any = None\n",
	}
	if err := CheckRepoNoDuplicateDeclarations(repo); err != nil {
		t.Fatalf("identically-named classes in different modules should never collide: %v", err)
	}
}

func TestCheckRepoNoDuplicateDeclarations_WithinOneFile_StillCollides(t *testing.T) {
	repo := map[string]string{
		"pyproject.toml": `[project]`,
		"iam/role.py":    "@dataclasses.dataclass\nclass Foo:\n    bar: Any = None\n\nFoo = sdk.ResourceBinding(\n    wire_type=\"aws_foo\",\n    fields={},\n)\n",
	}
	err := CheckRepoNoDuplicateDeclarations(repo)
	if err == nil {
		t.Fatal("expected an error for a real within-file collision, got nil")
	}
	mustContain(t, err.Error(), "iam/role.py")
}

// TestGeneratedRepo_CrossResourceNestedBlockVsSiblingResource_NoCollision
// reproduces UBI-96's own real, live-verified shape directly, now scoped
// to ONE FILE (UBI-98's own restructure) -- see
// sdk/codegen/templates/go's own sibling test for the full account
// (STATE.md).
func TestGeneratedRepo_CrossResourceNestedBlockVsSiblingResource_NoCollision(t *testing.T) {
	loggingBlock := ir.Field{
		WireName: "logging",
		Type: ir.TypeRef{Kind: ir.KindObject, Object: []ir.Field{
			{WireName: "enabled", Optional: true, Type: ir.TypeRef{Kind: ir.KindScalar, Scalar: ir.ScalarBool}},
		}},
	}
	types := []*ir.ResourceType{
		rt("aws_svc_thing", loggingBlock),
		rt("aws_svc_thing_logging",
			scalarField("id", ir.ScalarString, false, false, true, false),
			scalarField("target", ir.ScalarString, false, true, false, false),
		),
	}
	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", types)
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	thingSrc, ok := files["aws/svc/thing.py"]
	if !ok {
		t.Fatalf("expected aws/svc/thing.py, got paths: %v", keys(files))
	}
	loggingSrc, ok := files["aws/svc/thing_logging.py"]
	if !ok {
		t.Fatalf("expected aws/svc/thing_logging.py, got paths: %v", keys(files))
	}

	mustContain(t, thingSrc, "class Thing_Logging:\n    enabled: Any = None")
	mustContain(t, loggingSrc, "class ThingLoggingConfig:")
	mustContain(t, loggingSrc, "ThingLogging = sdk.ResourceBinding(")
	mustNotContain(t, thingSrc, "class ThingLogging:\n")

	if err := CheckRepoNoDuplicateDeclarations(files); err != nil {
		t.Fatalf("GeneratedRepo output has a real declaration collision: %v", err)
	}

	requirePython3(t)
	importGeneratedRepo(t, files)
}

// requirePython3 skips a test when python3 isn't on PATH -- matches this
// project's own requireDeno/requireGoToolchain pattern.
func requirePython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found in PATH -- skipping the real Python import half of this test")
	}
}

// importGeneratedRepo writes files (a GeneratedRepo's own output) to a
// throwaway directory, then runs a real Python subprocess that imports
// EVERY generated ".py" module in the tree (never just the entry point --
// there is no single entry point, each resource type's own module is a
// real, independently-importable leaf, the same "every unit gets
// verified" spirit as `go build ./...` building every package) with
// PYTHONPATH set to both the throwaway dir (the generated repo root) and
// this repo's own real sdk/py (the ubx_sdk runtime every generated
// module imports) -- proof the generated source is not just textually
// plausible but real, importable Python, exactly like
// cli/sdk_test.go's own assertPyFileImports, generalized to a whole
// tree instead of one file.
func importGeneratedRepo(t *testing.T, files map[string]string) {
	t.Helper()
	importGeneratedRepoWithTimeout(t, files, 120*time.Second)
}

// importGeneratedRepoWithTimeout is importGeneratedRepo's own
// parameterized form -- fullprovider_live_test.go's own
// importGeneratedRepoAtFullScale needs a longer budget than this
// package's small hermetic fixtures do.
func importGeneratedRepoWithTimeout(t *testing.T, files map[string]string, timeout time.Duration) {
	t.Helper()

	sdkPyDir := sdkPyModuleRoot(t)
	dir := t.TempDir()
	var modules []string
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if filepath.Ext(full) == ".py" {
			rel := path[:len(path)-len(".py")]
			modules = append(modules, moduleDotted(rel))
		}
	}

	var script strings.Builder
	for _, mod := range modules {
		fmt.Fprintf(&script, "import %s\n", mod)
	}
	fmt.Fprintf(&script, "print('OK', %d, 'modules imported')\n", len(modules))

	scriptPath := filepath.Join(dir, "_verify_imports.py")
	if err := os.WriteFile(scriptPath, []byte(script.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", scriptPath)
	cmd.Env = []string{"PYTHONPATH=" + sdkPyDir + string(os.PathListSeparator) + dir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real Python import of the generated repo failed:\n%s", out)
	}
}

// moduleDotted turns a "/"-separated relative path with no extension
// (e.g. "ecr/repository") into Python's own dotted import form
// ("ecr.repository").
func moduleDotted(relPath string) string {
	return strings.ReplaceAll(relPath, "/", ".")
}

// sdkPyModuleRoot resolves this repo's own sdk/py directory (the
// hand-authored ubx_sdk runtime every generated Python module imports) --
// shared by every test in this file that needs a real import.
func sdkPyModuleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not determine this test file's own path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "py"))
	if err != nil {
		t.Fatalf("resolve sdk/py path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "ubx_sdk", "__init__.py")); err != nil {
		t.Fatalf("expected %s to contain ubx_sdk/__init__.py (sdk/py's own real runtime): %v", root, err)
	}
	return root
}
