package blueprint

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func requirePython3Toolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found in PATH -- skipping the real Python import/run half of this test")
	}
}

// sdkPyModuleRoot resolves this repo's own sdk/py directory -- the
// identical path sdk/codegen/templates/py's own (unexported, different-
// package) function of the same name resolves, independently re-derived
// here since that helper isn't reachable across the package boundary.
func sdkPyModuleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not determine this test file's own path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "sdk", "py"))
	if err != nil {
		t.Fatalf("resolve sdk/py path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "ubx_sdk", "__init__.py")); err != nil {
		t.Fatalf("expected %s to contain ubx_sdk/__init__.py (sdk/py's own real runtime): %v", root, err)
	}
	return root
}

func TestGeneratePython_CiPlatform(t *testing.T) {
	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GeneratePython("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	for _, want := range []string{"py/bindings.py", "py/ciplatform.py"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("missing generated file %q; got %v", want, keysOf(files))
		}
	}

	fn := files["py/ciplatform.py"]
	// Native default argument, not a functional-options pattern --
	// docs/blueprint.md's own "Multi-language codegen" divergence from Go.
	if !strings.Contains(fn, "def ci_platform(repo_name: str, queue_name: str, retention_days: int = 1) -> None:") {
		t.Fatalf("ciplatform.py missing expected signature (native default argument):\n%s", fn)
	}
	if !strings.Contains(fn, "name=repo_name,") {
		t.Fatalf("ciplatform.py should substitute the repo_name param directly:\n%s", fn)
	}
	if !strings.Contains(fn, "ci_runner = sdk.resource(CiRunner,") {
		t.Fatalf("ciplatform.py should assign a local var to a referenced resource:\n%s", fn)
	}
	if !strings.Contains(fn, "ci_runner.id") {
		t.Fatalf("ciplatform.py should translate the $ref into an attribute-access chain:\n%s", fn)
	}
	if strings.Index(fn, "CiRunnerConfig(") > strings.Index(fn, "CiRunnerAccessConfig(") {
		t.Fatalf("ci-runner must be created before ci-runner-access:\n%s", fn)
	}
	if strings.Contains(fn, "ci_runner_access =") {
		t.Fatalf("ci-runner-access is never referenced, should not get an assigned local var:\n%s", fn)
	}

	bindings := files["py/bindings.py"]
	if !strings.Contains(bindings, `"aws_ecr_repository"`) {
		t.Fatalf("bindings.py missing ecr binding:\n%s", bindings)
	}
	if !strings.Contains(bindings, "@dataclasses.dataclass") {
		t.Fatalf("bindings.py missing a dataclass Config declaration:\n%s", bindings)
	}
}

func TestGeneratePython_DuplicateIdentifier(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [
			{"type": "aws_iam_role", "name": "ci-runner", "op": "create", "config": {}},
			{"type": "aws_iam_policy", "name": "ci_runner", "op": "create", "config": {}}
		]
	}`)
	if _, err := GeneratePython("s", &Ubxfile{Lang: "py"}, intent); err == nil {
		t.Fatal("expected a collision error for ci-runner/ci_runner, got nil")
	}
}

func TestGeneratePython_ModifyOpRejected(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [{"type": "a", "name": "one", "op": "modify", "config": {}}]
	}`)
	if _, err := GeneratePython("s", &Ubxfile{Lang: "py"}, intent); err == nil {
		t.Fatal("expected an error for op modify, got nil")
	}
}

// TestGeneratePython_ReservedWordFieldEscaped confirms a real, live-
// relevant Python reserved-word collision (a config field literally
// named "lambda" -- both a Python keyword AND a real AWS service, per
// sdk/codegen/templates/py's own established precedent for exactly this
// case) is escaped with a trailing underscore, not left to produce a
// SyntaxError.
func TestGeneratePython_ReservedWordFieldEscaped(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [{"type": "a", "name": "one", "op": "create", "config": {"lambda": "x"}}]
	}`)
	files, err := GeneratePython("s", &Ubxfile{Lang: "py"}, intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	if !strings.Contains(files["py/bindings.py"], "lambda_:") {
		t.Fatalf("bindings.py should escape the \"lambda\" field to \"lambda_\":\n%s", files["py/bindings.py"])
	}
}

// TestGeneratePython_CompilesClean is this package's own real, direct
// proof that the generated CI-platform module is REAL, IMPORTABLE
// Python: written to disk and imported for real in a Python subprocess,
// with PYTHONPATH set to this repo's own real sdk/py -- mirrors
// sdk/codegen/templates/py/collision_test.go's own
// importGeneratedRepoWithTimeout exactly.
func TestGeneratePython_CompilesClean(t *testing.T) {
	requirePython3Toolchain(t)

	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GeneratePython("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}

	dir := t.TempDir()
	pyDir := filepath.Join(dir, "py")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		rel := strings.TrimPrefix(name, "py/")
		if err := os.WriteFile(filepath.Join(pyDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	script := "import bindings\nimport ciplatform\nprint('OK')\n"
	scriptPath := filepath.Join(pyDir, "_verify_imports.py")
	if err := os.WriteFile(scriptPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", scriptPath)
	cmd.Env = []string{"PYTHONPATH=" + sdkPyModuleRoot(t) + string(os.PathListSeparator) + pyDir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real Python import of the generated blueprint module failed:\n%s", out)
	}
}

// TestGeneratePython_DefaultParamsPattern is this package's own real,
// direct proof -- running the generated function, not just importing it --
// that a params: default value is genuinely load-bearing at call time.
// Mirrors TestGenerateGo_DefaultParamOptionsPattern/
// TestGenerateTS_DefaultParamsPattern, proving Python's own native
// default-argument design works exactly as well.
func TestGeneratePython_DefaultParamsPattern(t *testing.T) {
	requirePython3Toolchain(t)

	intent := mustIntentFile(t, ciPlatformIntentWithDefaultUsed)
	files, err := GeneratePython("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}

	for _, tc := range []struct {
		name     string
		callArgs string
		want     float64
	}{
		{"default", `"payments-ci-artifacts", "payments-notifications"`, 1},
		{"overridden", `"payments-ci-artifacts", "payments-notifications", 30`, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runPyBlueprintCall(t, files, "ci_platform", tc.callArgs)

			var doc struct {
				Resources []struct {
					Type   string         `json:"type"`
					Config map[string]any `json:"config"`
				} `json:"resources"`
			}
			if err := json.Unmarshal(out, &doc); err != nil {
				t.Fatalf("parse emitted intent/v1: %v\noutput: %s", err, out)
			}
			if len(doc.Resources) != 1 || doc.Resources[0].Type != "aws_sqs_queue" {
				t.Fatalf("expected exactly one aws_sqs_queue resource, got: %s", out)
			}
			got, ok := doc.Resources[0].Config["message_retention_seconds"].(float64)
			if !ok {
				t.Fatalf("message_retention_seconds missing or not a number: %s", out)
			}
			if got != tc.want {
				t.Fatalf("message_retention_seconds = %v, want %v: %s", got, tc.want, out)
			}
		})
	}
}

// TestGeneratePython_EmbeddedRefUsesRuntimeStackName mirrors
// TestGenerateGo_EmbeddedRefUsesRuntimeStackName/
// TestGenerateTS_EmbeddedRefUsesRuntimeStackName: running the generated
// function from a stack named DIFFERENTLY than the blueprint's own
// build-time name proves the embedded $ref's own address reflects the
// REAL CALLING stack at run time, via Computed's own .address property.
func TestGeneratePython_EmbeddedRefUsesRuntimeStackName(t *testing.T) {
	requirePython3Toolchain(t)

	intent := mustIntentFile(t, ciPlatformIntentWithEmbeddedRef)
	ubxfile := &Ubxfile{
		Dir:  "testdata",
		Lang: "py",
		Params: []Param{
			{Name: "repo_name", Type: ParamString, Required: true},
		},
	}
	files, err := GeneratePython("ci-platform", ubxfile, intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}

	fn := files["py/ciplatform.py"]
	if strings.Contains(fn, `ci-platform.aws_ecr_repository.container-repo.arn`) {
		t.Fatalf("ciplatform.py bakes in the build-time stack name as a literal address, should use container_repo's own runtime .address instead:\n%s", fn)
	}
	if !strings.Contains(fn, `container_repo.arn.address`) {
		t.Fatalf("ciplatform.py missing the runtime address expression for the embedded $ref:\n%s", fn)
	}

	out := runPyBlueprintCallNamedStack(t, files, "ci_platform", `"payments-ci-artifacts"`, "payments")

	var doc struct {
		Resources []struct {
			Type   string         `json:"type"`
			Config map[string]any `json:"config"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse emitted intent/v1: %v\noutput: %s", err, out)
	}
	var policy string
	for _, r := range doc.Resources {
		if r.Type == "aws_iam_role_policy" {
			policy, _ = r.Config["policy"].(string)
		}
	}
	if policy == "" {
		t.Fatalf("emitted document missing the aws_iam_role_policy resource's own policy field: %s", out)
	}
	if !strings.Contains(policy, `"payments.aws_ecr_repository.container-repo.arn"`) {
		t.Fatalf("embedded $ref should address the REAL calling stack (\"payments\"), not the build-time blueprint name (\"ci-platform\"): %s", policy)
	}
}

// TestGeneratePython_PlaceholderArithmetic is UBI-123's own required
// cross-language regression test for Python, mirroring
// TestGenerateGo_PlaceholderArithmetic/TestGenerateTS_PlaceholderArithmetic
// exactly: runs the generated function for real and confirms the actual
// COMPUTED number reaches the wire, not a bare pass-through.
func TestGeneratePython_PlaceholderArithmetic(t *testing.T) {
	requirePython3Toolchain(t)

	intent := mustIntentFile(t, ciPlatformIntentWithUnitConversion)
	files, err := GeneratePython("ci-platform", ciPlatformUbxfileWithUnitConversion(), intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}

	fn := files["py/ciplatform.py"]
	if !strings.Contains(fn, "(retention_days * 86400)") {
		t.Fatalf("ciplatform.py missing the real days->seconds arithmetic ((retention_days * 86400)), got a bare pass-through instead:\n%s", fn)
	}
	if !strings.Contains(fn, "(max_receive_count * 2)") {
		t.Fatalf("ciplatform.py missing the required param's own arithmetic ((max_receive_count * 2)):\n%s", fn)
	}

	for _, tc := range []struct {
		name              string
		callArgs          string
		wantRetentionSec  float64
		wantVisibilitySec float64
	}{
		{"default", `"payments-notifications", 5`, 86400, 10},
		{"overridden", `"payments-notifications", 5, 30`, 2592000, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runPyBlueprintCall(t, files, "ci_platform", tc.callArgs)

			var doc struct {
				Resources []struct {
					Type   string         `json:"type"`
					Config map[string]any `json:"config"`
				} `json:"resources"`
			}
			if err := json.Unmarshal(out, &doc); err != nil {
				t.Fatalf("parse emitted intent/v1: %v\noutput: %s", err, out)
			}
			if len(doc.Resources) != 1 {
				t.Fatalf("expected exactly one resource, got: %s", out)
			}
			gotRetention, ok := doc.Resources[0].Config["message_retention_seconds"].(float64)
			if !ok {
				t.Fatalf("message_retention_seconds missing or not a number: %s", out)
			}
			if gotRetention != tc.wantRetentionSec {
				t.Fatalf("message_retention_seconds = %v, want %v (retention_days * 86400 not genuinely computed): %s", gotRetention, tc.wantRetentionSec, out)
			}
			gotVisibility, ok := doc.Resources[0].Config["visibility_timeout_seconds"].(float64)
			if !ok {
				t.Fatalf("visibility_timeout_seconds missing or not a number: %s", out)
			}
			if gotVisibility != tc.wantVisibilitySec {
				t.Fatalf("visibility_timeout_seconds = %v, want %v (max_receive_count * 2 not genuinely computed): %s", gotVisibility, tc.wantVisibilitySec, out)
			}
		})
	}
}

// runPyBlueprintCall is runPyBlueprintCallNamedStack's own convenience
// form, using "ci-platform" as the calling stack's own name.
func runPyBlueprintCall(t *testing.T, files map[string]string, funcName, callArgs string) []byte {
	t.Helper()
	return runPyBlueprintCallNamedStack(t, files, funcName, callArgs, "ci-platform")
}

// runPyBlueprintCallNamedStack writes files (a GeneratePython output) to
// a throwaway directory alongside a tiny driver script that imports the
// generated function and calls it inside a real ubx_sdk.stack()/
// intent()/evaluate() cycle, runs it via a real python3 subprocess (with
// PYTHONPATH covering both sdk/py and the throwaway py/ package
// directory), and returns the emitted intent/v1 document's own raw JSON
// bytes -- the Python equivalent of gogen_test.go's own `go run`/
// tsgen_test.go's own `deno run` calling-stack pattern.
func runPyBlueprintCallNamedStack(t *testing.T, files map[string]string, funcName, callArgs, stackName string) []byte {
	t.Helper()
	dir := t.TempDir()
	pyDir := filepath.Join(dir, "py")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		rel := strings.TrimPrefix(name, "py/")
		if err := os.WriteFile(filepath.Join(pyDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	driver := "import json\n" +
		"import ubx_sdk as sdk\n" +
		"from " + pkgModuleStem(t, files) + " import " + funcName + "\n\n" +
		"def describe():\n" +
		"    sdk.intent(\"x\")\n" +
		"    " + funcName + "(" + callArgs + ")\n\n" +
		"doc = sdk.stack(" + jsonStringLiteral(stackName) + ", describe).evaluate()\n" +
		"print(json.dumps(doc))\n"
	driverPath := filepath.Join(pyDir, "_driver.py")
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", driverPath)
	cmd.Env = []string{"PYTHONPATH=" + sdkPyModuleRoot(t) + string(os.PathListSeparator) + pyDir}
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("python3 run of the calling driver failed: %v\nstdout: %s\nstderr: %s", err, out, stderr)
	}
	return out
}

// pkgModuleStem finds the single "py/<stem>.py" file's own stem among
// files (excluding bindings.py) -- avoids the caller having to pass the
// blueprint's own package name a second time redundantly.
func pkgModuleStem(t *testing.T, files map[string]string) string {
	t.Helper()
	for name := range files {
		if name == "py/bindings.py" {
			continue
		}
		if strings.HasPrefix(name, "py/") && strings.HasSuffix(name, ".py") {
			return strings.TrimSuffix(strings.TrimPrefix(name, "py/"), ".py")
		}
	}
	t.Fatal("no py/<pkg>.py file found among GeneratePython's own output")
	return ""
}
