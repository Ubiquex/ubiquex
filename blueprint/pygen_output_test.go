package blueprint

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGeneratePython_Outputs_SignatureAndReturn is UBI-128's own Python
// half of TestGenerateGo_Outputs_SignatureAndReturn/TestGenerateTS_Outputs_
// SignatureAndReturn: the generated function's own return type annotation
// gains a real type (tuple[Any, Any] for two-or-more outputs, plain Any
// for exactly one -- Python's own native multi-value-return construct, a
// bare tuple, no wrapper class), and the function body returns the
// correct attribute-access expression for each, in declared order.
func TestGeneratePython_Outputs_SignatureAndReturn(t *testing.T) {
	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GeneratePython("ci-platform", ciPlatformUbxfileWithOutputs(), intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	fn := files["py/ciplatform.py"]

	if !strings.Contains(fn, "def ci_platform(repo_name: str, queue_name: str, retention_days: int = 1) -> tuple[Any, Any]:") {
		t.Fatalf("ciplatform.py missing the outputs-bearing signature:\n%s", fn)
	}
	if !strings.Contains(fn, "return ci_artifacts.arn, ci_notifications.arn") {
		t.Fatalf("ciplatform.py missing the outputs return statement:\n%s", fn)
	}
	if !strings.Contains(fn, "ci_artifacts = sdk.resource(CiArtifacts,") {
		t.Fatalf("ciplatform.py should assign a local var to ci-artifacts (now output-referenced):\n%s", fn)
	}
	if !strings.Contains(fn, "from typing import Any") {
		t.Fatalf("ciplatform.py missing the \"from typing import Any\" import needed by its own return type annotation:\n%s", fn)
	}
}

// TestGeneratePython_Outputs_SingleOutput_BareType confirms exactly one
// declared output annotates the return type as bare "Any" (never a
// one-element tuple type) and returns a bare expression -- callers use
// "x = f(...)", not "x, = f(...)", matching Go's own single-named-return
// ergonomics.
func TestGeneratePython_Outputs_SingleOutput_BareType(t *testing.T) {
	intent := mustIntentFile(t, ciPlatformIntent)
	ubxfile := ciPlatformUbxfile()
	ubxfile.Outputs = []Output{{Name: "repo_arn", Target: "ci-artifacts.arn"}}
	files, err := GeneratePython("ci-platform", ubxfile, intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	fn := files["py/ciplatform.py"]
	if !strings.Contains(fn, ") -> Any:") {
		t.Fatalf("ciplatform.py missing the bare \"-> Any\" single-output return type:\n%s", fn)
	}
	if !strings.Contains(fn, "return ci_artifacts.arn\n") {
		t.Fatalf("ciplatform.py missing the bare single-output return statement:\n%s", fn)
	}
}

// TestGeneratePython_Outputs_NoOutputs_Unaffected confirms an Ubxfile
// with no outputs: block generates the exact same "-> None:" signature
// and no return statement as before this feature existed.
func TestGeneratePython_Outputs_NoOutputs_Unaffected(t *testing.T) {
	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GeneratePython("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	fn := files["py/ciplatform.py"]
	if !strings.Contains(fn, "def ci_platform(repo_name: str, queue_name: str, retention_days: int = 1) -> None:") {
		t.Fatalf("expected the plain \"-> None\" signature for a blueprint with no outputs:\n%s", fn)
	}
	if strings.Contains(fn, "from typing import Any") || strings.Contains(fn, "return ") {
		t.Fatalf("expected no typing import or return statement for a blueprint with no outputs:\n%s", fn)
	}
}

// TestGeneratePython_Outputs_CompilesClean is TestGeneratePython_
// CompilesClean's own outputs-bearing mirror: a real Python subprocess
// imports the generated module, confirming the outputs-bearing return
// type annotation itself is valid, importable Python (tuple[Any, Any]
// needs no "from __future__ import annotations" on the pinned CPython
// this project targets, confirmed live here rather than assumed).
func TestGeneratePython_Outputs_CompilesClean(t *testing.T) {
	requirePython3Toolchain(t)

	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GeneratePython("ci-platform", ciPlatformUbxfileWithOutputs(), intent)
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
		t.Fatalf("real Python import of the generated outputs-bearing blueprint module failed:\n%s", out)
	}
}

// TestGeneratePython_Outputs_RuntimeUsable is this package's own required
// real proof for Python (mirroring TestGenerateGo_Outputs_RuntimeUsable/
// TestGenerateTS_Outputs_RuntimeUsable): a real python3 driver imports the
// generated ci_platform function, calls it, unpacks its own returned
// tuple, and uses repo_arn DIRECTLY as another sdk.resource()'s own
// config value -- confirming UBI-128's own success bar for the Python
// medium specifically.
func TestGeneratePython_Outputs_RuntimeUsable(t *testing.T) {
	requirePython3Toolchain(t)

	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GeneratePython("ci-platform", ciPlatformUbxfileWithOutputs(), intent)
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

	driver := `import dataclasses
import json
from typing import Any

import ubx_sdk as sdk
from ciplatform import ci_platform


@dataclasses.dataclass
class DownstreamConfig:
    policy_arn: Any = None


Downstream = sdk.ResourceBinding(
    wire_type="aws_iam_role_policy",
    fields={"policy_arn": sdk.FieldSpec(wire_name="policy_arn")},
)


def describe():
    sdk.intent("x")
    repo_arn, queue_arn = ci_platform("payments-ci-artifacts", "payments-notifications")
    sdk.resource(Downstream, "downstream", DownstreamConfig(policy_arn=repo_arn))


doc = sdk.stack("ci-platform", describe).evaluate()
print(json.dumps(doc))
`
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
		t.Fatalf("python3 driver failed: %v\nstderr: %s", err, stderr)
	}

	var doc struct {
		Resources []struct {
			Type   string         `json:"type"`
			Name   string         `json:"name"`
			Config map[string]any `json:"config"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse emitted intent/v1: %v\noutput: %s", err, out)
	}
	var downstream map[string]any
	for _, r := range doc.Resources {
		if r.Name == "downstream" {
			downstream = r.Config
		}
	}
	if downstream == nil {
		t.Fatalf("no \"downstream\" resource in emitted document: %s", out)
	}
	refObj, ok := downstream["policy_arn"].(map[string]any)
	if !ok {
		t.Fatalf("policy_arn = %v, want a $ref object", downstream["policy_arn"])
	}
	inner, ok := refObj["$ref"].(map[string]any)
	if !ok {
		t.Fatalf("policy_arn = %v, want a real {\"$ref\":...} object", refObj)
	}
	if inner["to"] != "ci-platform.aws_ecr_repository.ci-artifacts.arn" {
		t.Fatalf("policy_arn.$ref.to = %v, want %q", inner["to"], "ci-platform.aws_ecr_repository.ci-artifacts.arn")
	}
}
