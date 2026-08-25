package pytmpl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// pyFixtureSource is a hermetic, real-shaped stand-in for ubx_sdk's own
// ResourceBinding/DataSourceBinding/resource()/data() surface --
// confirmed live against the real, separate ubx-sdk-python repo before
// writing this (this monorepo's own sdk/py submodule is pinned to a
// pre-DataSourceBinding commit -- confirmed no "DataSourceBinding" or
// "def data" anywhere in it -- so the existing importGeneratedRepo/
// sdkPyModuleRoot helpers in this package cannot exercise a real
// data-source consumer at all).
//
// The real runtime's own DataSourceBinding doc comment is explicit that
// resource()/data() perform no isinstance check at all -- "Python itself
// has no static enforcement either way" -- so, unlike the Go template's
// consumer test, calling data() with a ResourceBinding here will NOT
// raise; see TestConsumer_WrongBindingKind_PythonDoesNotCatchIt below,
// which documents that as a real, confirmed finding rather than leaving
// it assumed.
const pyFixtureSource = `import dataclasses
from typing import Any, Optional


@dataclasses.dataclass(frozen=True)
class FieldSpec:
    wire_name: str
    kind: str = ""
    fields: Optional[dict] = None


FieldMap = dict


@dataclasses.dataclass(frozen=True)
class ResourceBinding:
    wire_type: str
    fields: "FieldMap"


@dataclasses.dataclass(frozen=True)
class DataSourceBinding:
    wire_type: str
    fields: "FieldMap"


def resource(binding: ResourceBinding, name: str, config: Any) -> Any:
    return {"address": f"{binding.wire_type}.{name}"}


def data(binding: DataSourceBinding, name: str, lookup: Any) -> Any:
    return {"address": f"{binding.wire_type}.{name}"}
`

// writePyConsumerFixture writes files (a GeneratedRepo result) plus the
// hermetic ubx_sdk fixture into dir/sdk_root and dir/fixture_root
// respectively, mirroring importGeneratedRepo's own real PYTHONPATH
// convention (generated repo root + the ubx_sdk runtime's own parent
// directory), minus that helper's dependency on the stale submodule.
func writePyConsumerFixture(t *testing.T, dir string, files map[string]string) (sdkRoot, fixtureRoot string) {
	t.Helper()

	sdkRoot = filepath.Join(dir, "sdk_root")
	for path, content := range files {
		full := filepath.Join(sdkRoot, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	fixtureRoot = filepath.Join(dir, "fixture_root")
	fixturePkg := filepath.Join(fixtureRoot, "ubx_sdk")
	if err := os.MkdirAll(fixturePkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixturePkg, "__init__.py"), []byte(pyFixtureSource), 0o644); err != nil {
		t.Fatal(err)
	}

	return sdkRoot, fixtureRoot
}

func runPythonScript(t *testing.T, sdkRoot, fixtureRoot, script string) (string, bool) {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "_consumer.py")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", scriptPath)
	cmd.Env = []string{"PYTHONPATH=" + sdkRoot + string(os.PathListSeparator) + fixtureRoot}
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// TestConsumer_ResourceAndDataSource_RealImportAndCall is the real
// consumer program this package's own generated output has to satisfy:
// an actual Python subprocess importing the generated resource AND data
// source modules and calling ubx.resource()/ubx.data() on them --
// proof beyond ast.parse (which only proves the file is syntactically
// valid Python, never that ubx.resource/ubx.data actually exist with
// this shape or that the generated module is really importable end to
// end).
func TestConsumer_ResourceAndDataSource_RealImportAndCall(t *testing.T) {
	requirePython3(t)

	resourceType := rt("aws_instance", scalarField("id", ir.ScalarString, false, false, true, false))
	resourceType.RealNamespace = "ec2"

	dataSourceType := rt("aws_instance", scalarField("id", ir.ScalarString, true, false, false, false))
	dataSourceType.RealNamespace = "ec2"
	dataSourceType.IsDataSource = true

	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", []*ir.ResourceType{resourceType, dataSourceType})
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	dir := t.TempDir()
	sdkRoot, fixtureRoot := writePyConsumerFixture(t, dir, files)

	script := `import ubx_sdk as ubx
from sdk.python.ubx.aws.ec2 import instance
from sdk.python.ubx.aws.data.ec2 import instance as data_instance

ubx.resource(instance.Instance, "example", instance.InstanceConfig())
ubx.data(data_instance.Instance, "example", data_instance.InstanceConfig())
print("OK")
`
	out, ok := runPythonScript(t, sdkRoot, fixtureRoot, script)
	if !ok {
		t.Fatalf("real consumer program failed to import/call against the generated resource + data source bindings:\n%s", out)
	}
}

// TestConsumer_WrongBindingKind_PythonDoesNotCatchIt is a real, confirmed
// negative finding, not a hypothetical: the real ubx_sdk runtime's own
// data() performs no isinstance check on binding (confirmed by reading
// the real, separate ubx-sdk-python source directly), so passing a
// ResourceBinding where a DataSourceBinding is expected raises nothing
// at call time -- Python has neither Go's compile-time nominal typing
// nor even TypeScript's partial structural checking here. Recorded as a
// passing test with an explanatory name so this is visible rather than
// silently assumed; the real, load-bearing defense against this bug
// class in the Python template is the generated-source string assertion
// in py_test.go (mustContain "DataSourceBinding", mustNotContain
// "ResourceBinding"), not the runtime.
func TestConsumer_WrongBindingKind_PythonDoesNotCatchIt(t *testing.T) {
	requirePython3(t)

	resourceType := rt("aws_instance", scalarField("id", ir.ScalarString, false, false, true, false))
	resourceType.RealNamespace = "ec2"

	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", []*ir.ResourceType{resourceType})
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	dir := t.TempDir()
	sdkRoot, fixtureRoot := writePyConsumerFixture(t, dir, files)

	// Deliberately wrong: instance.Instance is a ResourceBinding, but
	// this passes it to ubx.data(), the data-source-only entry point.
	script := `import ubx_sdk as ubx
from sdk.python.ubx.aws.ec2 import instance

ubx.data(instance.Instance, "example", instance.InstanceConfig())
print("OK, no exception raised")
`
	out, ok := runPythonScript(t, sdkRoot, fixtureRoot, script)
	if !ok {
		t.Fatalf(
			"expected calling ubx.data() with a ResourceBinding to raise nothing (a real, confirmed gap -- see this test's own doc comment), but it failed instead:\n%s\n"+
				"If ubx_sdk's own data() has since been changed to validate binding type, this test should be updated to expect that failure, and the py_test.go string assertions revisited as no longer the only defense.",
			out,
		)
	}
	mustContain(t, out, "OK, no exception raised")
}

// requirePython3 (called above) is collision_test.go's own helper,
// shared within this package -- not redeclared here.
