package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGenerateTS_Outputs_SignatureAndReturn is UBI-128's own TS half of
// TestGenerateGo_Outputs_SignatureAndReturn (gogen_test.go): the
// generated function's own return type gains one property per declared
// output, and the function body returns an object literal with the
// correct Computed<T> property-drilling expression for each -- TS's own
// native return construct (a plain object), no new language feature, and
// (unlike Go's named return VALUES) no new identifier declared into the
// function's own scope at all.
func TestGenerateTS_Outputs_SignatureAndReturn(t *testing.T) {
	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GenerateTS("ci-platform", ciPlatformUbxfileWithOutputs(), intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}
	fn := files["ts/ciplatform.ts"]

	if !strings.Contains(fn, "export function ciPlatform(repoName: string, queueName: string, retentionDays: number = 1): { repoArn: any; queueArn: any } {") {
		t.Fatalf("ciplatform.ts missing the outputs-bearing signature:\n%s", fn)
	}
	if !strings.Contains(fn, "return { repoArn: ciArtifacts.arn, queueArn: ciNotifications.arn };") {
		t.Fatalf("ciplatform.ts missing the outputs return statement:\n%s", fn)
	}
	if !strings.Contains(fn, `const ciArtifacts = resource(CiArtifacts, "ci-artifacts"`) {
		t.Fatalf("ciplatform.ts should assign a local const to ci-artifacts (now output-referenced):\n%s", fn)
	}
}

// TestGenerateTS_Outputs_NoOutputs_Unaffected confirms an Ubxfile with no
// outputs: block generates the exact same "): void" signature and no
// return statement as before this feature existed -- purely additive.
func TestGenerateTS_Outputs_NoOutputs_Unaffected(t *testing.T) {
	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GenerateTS("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}
	fn := files["ts/ciplatform.ts"]
	if !strings.Contains(fn, "export function ciPlatform(repoName: string, queueName: string, retentionDays: number = 1): void {") {
		t.Fatalf("expected the plain \"): void\" signature for a blueprint with no outputs:\n%s", fn)
	}
	if strings.Contains(fn, "return {") {
		t.Fatalf("expected no outputs return statement for a blueprint with no outputs:\n%s", fn)
	}
}

// TestGenerateTS_Outputs_DuplicateIdent_Refused confirms two outputs
// deriving the same camelCase identifier is a hard build error -- a
// silent last-write-wins object literal key would otherwise drop one
// output's own value with no signal at all.
func TestGenerateTS_Outputs_DuplicateIdent_Refused(t *testing.T) {
	intent := mustIntentFile(t, ciPlatformIntent)
	ubxfile := ciPlatformUbxfile()
	ubxfile.Outputs = []Output{
		{Name: "repo_arn", Target: "ci-artifacts.arn"},
		{Name: "repo-arn", Target: "ci-artifacts.name"},
	}
	_, err := GenerateTS("ci-platform", ubxfile, intent)
	if err == nil || !strings.Contains(err.Error(), "both derive the generated identifier") {
		t.Fatalf("GenerateTS err = %v, want a duplicate-output-identifier refusal", err)
	}
}

// TestGenerateTS_Outputs_CompilesClean is TestGenerateTS_CompilesClean's
// own outputs-bearing mirror: a real `deno check` against the generated
// module, confirming the outputs return TYPE annotation itself
// typechecks (not just the resource() calls the pre-existing test
// already covers).
func TestGenerateTS_Outputs_CompilesClean(t *testing.T) {
	requireDenoToolchain(t)

	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GenerateTS("ci-platform", ciPlatformUbxfileWithOutputs(), intent)
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
		t.Fatalf("deno check of the generated outputs-bearing blueprint module failed:\n%s", out)
	}
}

// TestGenerateTS_Outputs_RuntimeUsable is this package's own required
// real proof for TS (mirroring TestGenerateGo_Outputs_RuntimeUsable in
// gogen_test.go): a real `deno run` driver imports the generated
// ciPlatform function, calls it, and uses its own returned repoArn
// DIRECTLY as another resource()'s own config value -- confirming UBI-128's
// own success bar (section 3: "SDK callers use the return values
// directly... zero new runtime mechanism") for the TS medium specifically,
// not just Go.
func TestGenerateTS_Outputs_RuntimeUsable(t *testing.T) {
	requireDenoToolchain(t)

	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GenerateTS("ci-platform", ciPlatformUbxfileWithOutputs(), intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}

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

	driver := `import { intent, resource, stack } from "@ubx/sdk";
import { ciPlatform } from "./ts/ciplatform.ts";

const Downstream = {
  wireType: "aws_iam_role_policy",
  fields: { policyArn: "policy_arn" },
};

const s = stack("ci-platform", () => {
  intent({ summary: "x" });
  const { repoArn } = ciPlatform("payments-ci-artifacts", "payments-notifications");
  resource(Downstream as any, "downstream", { policyArn: repoArn });
});
console.log(JSON.stringify(s.evaluate()));
`
	if err := os.WriteFile(filepath.Join(dir, "driver.ts"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	importMap := fmt.Sprintf(`{"imports":{"@ubx/sdk":%q}}`, sdkTSRuntimeIndexPath(t))
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte(importMap), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "deno", "run", "--no-remote", "--allow-read", "driver.ts")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("deno run failed: %v\nstderr: %s", err, ee.Stderr)
		}
		t.Fatalf("deno run failed: %v", err)
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
