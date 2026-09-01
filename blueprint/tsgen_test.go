package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func requireDenoToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH -- skipping the real `deno check`/`deno run` half of this test")
	}
}

// sdkTSRuntimeIndexPath resolves this repo's own sdk/ts/runtime/src/
// index.ts -- the identical path sdk/codegen/templates/ts's own
// (unexported, different-package) function of the same name resolves,
// independently re-derived here since that helper isn't reachable
// across the package boundary.
func sdkTSRuntimeIndexPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not determine this test file's own path")
	}
	path, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "sdk", "ts", "runtime", "src", "index.ts"))
	if err != nil {
		t.Fatalf("resolve sdk/ts/runtime/src/index.ts path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist (sdk/ts/runtime's own real index.ts): %v", path, err)
	}
	return path
}

func TestGenerateTS_CiPlatform(t *testing.T) {
	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GenerateTS("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}
	for _, want := range []string{"ts/bindings.ts", "ts/ciplatform.ts"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("missing generated file %q; got %v", want, keysOf(files))
		}
	}

	fn := files["ts/ciplatform.ts"]
	// Native default parameter, not a functional-options pattern --
	// docs/blueprint.md's own "Multi-language codegen" divergence from Go.
	if !strings.Contains(fn, "export function ciPlatform(repoName: string, queueName: string, retentionDays: number = 1): void {") {
		t.Fatalf("ciplatform.ts missing expected signature (native default parameter):\n%s", fn)
	}
	// UBI-126: every generated blueprint function wraps its own body in
	// pushBlueprintSource(name)/popBlueprintSource() (a try/finally, TS's
	// own equivalent of Go's defer) -- mirrors gogen's own
	// PushBlueprintSource/PopBlueprintSource wrapping, proven end-to-end
	// (not just as generated text) by TestGenerateTS_CompilesClean/
	// cli/blueprint_call_test.go's own TS sibling.
	if !strings.Contains(fn, `import { popBlueprintSource, pushBlueprintSource, resource`) {
		t.Fatalf("ciplatform.ts should import pushBlueprintSource/popBlueprintSource from @ubx/sdk:\n%s", fn)
	}
	if !strings.Contains(fn, `pushBlueprintSource("ci-platform");`) {
		t.Fatalf("ciplatform.ts should push its own bare declared name at the top of its body:\n%s", fn)
	}
	if !strings.Contains(fn, "  try {\n") || !strings.Contains(fn, "  } finally {\n    popBlueprintSource();\n  }\n") {
		t.Fatalf("ciplatform.ts should wrap its body in try/finally, popping in finally:\n%s", fn)
	}
	if !strings.Contains(fn, "name: repoName,") {
		t.Fatalf("ciplatform.ts should substitute the repoName param directly:\n%s", fn)
	}
	if !strings.Contains(fn, "const ciRunner = resource(CiRunner,") {
		t.Fatalf("ciplatform.ts should assign a local const to a referenced resource:\n%s", fn)
	}
	if !strings.Contains(fn, "ciRunner.id") {
		t.Fatalf("ciplatform.ts should translate the $ref into a property-access chain:\n%s", fn)
	}
	if strings.Index(fn, "CiRunner,") > strings.Index(fn, "CiRunnerAccess,") {
		t.Fatalf("ci-runner must be created before ci-runner-access:\n%s", fn)
	}
	if strings.Contains(fn, "const ciRunnerAccess") {
		t.Fatalf("ci-runner-access is never referenced, should not get an assigned local const:\n%s", fn)
	}

	bindings := files["ts/bindings.ts"]
	if !strings.Contains(bindings, `"aws_ecr_repository"`) {
		t.Fatalf("bindings.ts missing ecr binding:\n%s", bindings)
	}
	if !strings.Contains(bindings, "ResourceBinding<any, any>") {
		t.Fatalf("bindings.ts should type every binding ResourceBinding<any, any> (no schema to derive Config/Attrs interfaces from):\n%s", bindings)
	}
	// UBI-225: every generated binding carries the blueprint's own bare
	// name, so a resource() call built directly from it (bypassing the
	// wrapper function) still gets real provenance.
	if !strings.Contains(bindings, `blueprintName: "ci-platform"`) {
		t.Fatalf("bindings.ts missing blueprintName (UBI-225):\n%s", bindings)
	}
}

func TestGenerateTS_DuplicateIdentifier(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [
			{"type": "aws_iam_role", "name": "ci-runner", "op": "create", "config": {}},
			{"type": "aws_iam_policy", "name": "ci_runner", "op": "create", "config": {}}
		]
	}`)
	if _, err := GenerateTS("s", &Ubxfile{Lang: "ts"}, intent); err == nil {
		t.Fatal("expected a collision error for ci-runner/ci_runner, got nil")
	}
}

func TestGenerateTS_ReservedWordBlueprintName_Errors(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"}, "resources": []
	}`)
	if _, err := GenerateTS("class", &Ubxfile{Lang: "ts"}, intent); err == nil {
		t.Fatal("expected an error for a blueprint name deriving a TS reserved word, got nil")
	}
}

func TestGenerateTS_ModifyOpRejected(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [{"type": "a", "name": "one", "op": "modify", "config": {}}]
	}`)
	if _, err := GenerateTS("s", &Ubxfile{Lang: "ts"}, intent); err == nil {
		t.Fatal("expected an error for op modify, got nil")
	}
}

// TestGenerateTS_CompilesClean is this package's own real, direct proof
// that the generated CI-platform module is REAL, TYPE-CHECKED
// TypeScript: written to disk and `deno check --no-remote`'d for real,
// against a real @ubx/sdk import map pointing at this repo's own
// sdk/ts/runtime/src/index.ts -- mirrors
// sdk/codegen/templates/ts/fullprovider_live_test.go's own
// checkGeneratedRepoAtFullScale exactly.
func TestGenerateTS_CompilesClean(t *testing.T) {
	requireDenoToolchain(t)

	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GenerateTS("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}

	dir := t.TempDir()
	var tsFiles []string
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		tsFiles = append(tsFiles, full)
	}

	importMap := fmt.Sprintf(`{"imports":{"@ubx/sdk":%q}}`, sdkTSRuntimeIndexPath(t))
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte(importMap), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := append([]string{"check", "--no-remote"}, tsFiles...)
	cmd := exec.CommandContext(ctx, "deno", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deno check of the generated blueprint module failed:\n%s", out)
	}
}

// TestGenerateTS_DefaultParamsPattern is this package's own real, direct
// proof -- running the generated function, not just type-checking it --
// that a params: default value is genuinely load-bearing at call time:
// omitting it uses the Ubxfile's own declared default (1), and passing
// an explicit override genuinely overrides it. Mirrors
// TestGenerateGo_DefaultParamOptionsPattern, proving TS's own simpler
// native-default-parameter design works exactly as well.
func TestGenerateTS_DefaultParamsPattern(t *testing.T) {
	requireDenoToolchain(t)

	intent := mustIntentFile(t, ciPlatformIntentWithDefaultUsed)
	files, err := GenerateTS("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
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
			out := runTSBlueprintCall(t, files, "ciPlatform", tc.callArgs)

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

// TestGenerateTS_EmbeddedRefUsesRuntimeStackName mirrors
// TestGenerateGo_EmbeddedRefUsesRuntimeStackName: running the generated
// function from a stack named DIFFERENTLY than the blueprint's own
// build-time name proves the embedded $ref's own address reflects the
// REAL CALLING stack at run time, via @ubx/sdk's own addressOf() helper.
func TestGenerateTS_EmbeddedRefUsesRuntimeStackName(t *testing.T) {
	requireDenoToolchain(t)

	intent := mustIntentFile(t, ciPlatformIntentWithEmbeddedRef)
	ubxfile := &Ubxfile{
		Dir:  "testdata",
		Lang: "ts",
		Params: []Param{
			{Name: "repo_name", Type: ParamString, Required: true},
		},
	}
	files, err := GenerateTS("ci-platform", ubxfile, intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}

	fn := files["ts/ciplatform.ts"]
	if strings.Contains(fn, `ci-platform.aws_ecr_repository.container-repo.arn`) {
		t.Fatalf("ciplatform.ts bakes in the build-time stack name as a literal address, should use containerRepo's own runtime addressOf() instead:\n%s", fn)
	}
	if !strings.Contains(fn, `addressOf(containerRepo.arn)`) {
		t.Fatalf("ciplatform.ts missing the runtime address expression for the embedded $ref:\n%s", fn)
	}

	out := runTSBlueprintCallNamedStack(t, files, "ciPlatform", `"payments-ci-artifacts"`, "payments")

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

// TestGenerateTS_PlaceholderArithmetic is UBI-123's own required
// cross-language regression test for TypeScript, mirroring
// TestGenerateGo_PlaceholderArithmetic exactly: runs the generated
// function for real (deno run) and confirms the actual COMPUTED number
// reaches the wire, not a bare pass-through.
func TestGenerateTS_PlaceholderArithmetic(t *testing.T) {
	requireDenoToolchain(t)

	intent := mustIntentFile(t, ciPlatformIntentWithUnitConversion)
	files, err := GenerateTS("ci-platform", ciPlatformUbxfileWithUnitConversion(), intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}

	fn := files["ts/ciplatform.ts"]
	if !strings.Contains(fn, "(retentionDays * 86400)") {
		t.Fatalf("ciplatform.ts missing the real days->seconds arithmetic ((retentionDays * 86400)), got a bare pass-through instead:\n%s", fn)
	}
	if !strings.Contains(fn, "(maxReceiveCount * 2)") {
		t.Fatalf("ciplatform.ts missing the required param's own arithmetic ((maxReceiveCount * 2)):\n%s", fn)
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
			out := runTSBlueprintCall(t, files, "ciPlatform", tc.callArgs)

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

// runTSBlueprintCall is runTSBlueprintCallNamedStack's own convenience
// form, using "ci-platform" as the calling stack's own name (the
// ordinary case for tests that don't specifically care about a
// differently-named caller).
func runTSBlueprintCall(t *testing.T, files map[string]string, funcName, callArgs string) []byte {
	t.Helper()
	return runTSBlueprintCallNamedStack(t, files, funcName, callArgs, "ci-platform")
}

// runTSBlueprintCallNamedStack writes files (a GenerateTS output) to a
// throwaway directory alongside a tiny driver script that imports the
// generated function and calls it inside a real @ubx/sdk stack()/
// intent()/evaluate() cycle, runs it via a real `deno run --no-remote`
// subprocess, and returns the emitted intent/v1 document's own raw JSON
// bytes -- the TS equivalent of gogen_test.go's own `go run` calling-
// stack pattern (no separate SDK program file needed the way Go's own
// module system requires, since deno resolves relative imports directly
// with no manifest at all).
func runTSBlueprintCallNamedStack(t *testing.T, files map[string]string, funcName, callArgs, stackName string) []byte {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	driver := "import { intent, stack } from \"@ubx/sdk\";\n" +
		"import { " + funcName + " } from \"./ts/" + pkgFileStem(t, files) + ".ts\";\n\n" +
		"const s = stack(" + jsonStringLiteral(stackName) + ", () => {\n" +
		"  intent({ summary: \"x\" });\n" +
		"  " + funcName + "(" + callArgs + ");\n" +
		"});\n" +
		"console.log(JSON.stringify(s.evaluate()));\n"
	driverPath := filepath.Join(dir, "driver.ts")
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	importMap := fmt.Sprintf(`{"imports":{"@ubx/sdk":%q}}`, sdkTSRuntimeIndexPath(t))
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte(importMap), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "deno", "run", "--no-remote", "--allow-read", driverPath)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("deno run of the calling driver failed: %v\nstdout: %s\nstderr: %s", err, out, stderr)
	}
	return out
}

// pkgFileStem finds the single "ts/<stem>.ts" file's own stem (everything
// but "ts/" and ".ts") among files -- avoids the caller having to pass
// the blueprint's own package name a second time redundantly.
func pkgFileStem(t *testing.T, files map[string]string) string {
	t.Helper()
	for name := range files {
		if name == "ts/bindings.ts" {
			continue
		}
		if strings.HasPrefix(name, "ts/") && strings.HasSuffix(name, ".ts") {
			return strings.TrimSuffix(strings.TrimPrefix(name, "ts/"), ".ts")
		}
	}
	t.Fatal("no ts/<pkg>.ts file found among GenerateTS's own output")
	return ""
}
