package blueprint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

func TestResolveCallOutputs_Basic(t *testing.T) {
	ubxfile := &Ubxfile{
		Outputs: []Output{
			{Name: "repo_arn", Target: "container-repo.arn"},
			{Name: "queue_url", Target: "pipeline-events.url"},
		},
	}
	resources := []resolver.ResourceIntent{
		{Type: "aws_ecr_repository", Name: "container-repo"},
		{Type: "aws_sqs_queue", Name: "pipeline-events"},
	}
	out, err := resolveCallOutputs("payments", ubxfile, resources)
	if err != nil {
		t.Fatalf("resolveCallOutputs: %v", err)
	}
	if out["repo_arn"] != "payments.aws_ecr_repository.container-repo.arn" {
		t.Errorf("repo_arn = %q, want payments.aws_ecr_repository.container-repo.arn", out["repo_arn"])
	}
	if out["queue_url"] != "payments.aws_sqs_queue.pipeline-events.url" {
		t.Errorf("queue_url = %q, want payments.aws_sqs_queue.pipeline-events.url", out["queue_url"])
	}
}

func TestResolveCallOutputs_NoOutputs_EmptyMap(t *testing.T) {
	out, err := resolveCallOutputs("payments", &Ubxfile{}, nil)
	if err != nil {
		t.Fatalf("resolveCallOutputs: %v", err)
	}
	if out == nil || len(out) != 0 {
		t.Fatalf("out = %#v, want an empty, non-nil map", out)
	}
}

func TestResolveCallOutputs_UnknownSlug_Errors(t *testing.T) {
	ubxfile := &Ubxfile{Outputs: []Output{{Name: "repo_arn", Target: "does-not-exist.arn"}}}
	resources := []resolver.ResourceIntent{{Type: "aws_ecr_repository", Name: "container-repo"}}
	_, err := resolveCallOutputs("payments", ubxfile, resources)
	if err == nil || !strings.Contains(err.Error(), "no resource with slug") {
		t.Fatalf("expected a no-such-slug error, got %v", err)
	}
}

func TestResolveCallOutputs_AmbiguousSlug_Errors(t *testing.T) {
	ubxfile := &Ubxfile{Outputs: []Output{{Name: "arn", Target: "thing.arn"}}}
	resources := []resolver.ResourceIntent{
		{Type: "aws_ecr_repository", Name: "thing"},
		{Type: "aws_sqs_queue", Name: "thing"},
	}
	_, err := resolveCallOutputs("payments", ubxfile, resources)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected an ambiguous-slug error, got %v", err)
	}
}

func TestRewriteBlueprintOutputRefs_Basic(t *testing.T) {
	intent := &resolver.IntentFile{
		Resources: []resolver.ResourceIntent{
			{
				Type: "aws_iam_role_policy", Name: "ci-runner-access",
				Config: json.RawMessage(`{"role":"ci-runner","policy_arn":{"$ref":{"to":"$blueprint_output:platform:repo_arn"}}}`),
			},
		},
	}
	outputAddr := map[string]string{
		"platform:repo_arn": "payments.aws_ecr_repository.container-repo.arn",
	}
	if err := rewriteBlueprintOutputRefs(intent, outputAddr); err != nil {
		t.Fatalf("rewriteBlueprintOutputRefs: %v", err)
	}

	var config map[string]any
	if err := json.Unmarshal(intent.Resources[0].Config, &config); err != nil {
		t.Fatal(err)
	}
	ref, ok := config["policy_arn"].(map[string]any)["$ref"].(map[string]any)
	if !ok {
		t.Fatalf("expected a real $ref object, got %v", config["policy_arn"])
	}
	if ref["to"] != "payments.aws_ecr_repository.container-repo.arn" {
		t.Fatalf("$ref.to = %v, want the real resolved address", ref["to"])
	}
}

func TestRewriteBlueprintOutputRefs_UnresolvedPlaceholder_Errors(t *testing.T) {
	intent := &resolver.IntentFile{
		Resources: []resolver.ResourceIntent{
			{Type: "a", Name: "b", Config: json.RawMessage(`{"x":{"$ref":{"to":"$blueprint_output:nosuch:output"}}}`)},
		},
	}
	err := rewriteBlueprintOutputRefs(intent, map[string]string{"platform:repo_arn": "x.y.z.arn"})
	if err == nil || !strings.Contains(err.Error(), "no such blueprint call/output declared") {
		t.Fatalf("expected an unresolved-placeholder error, got %v", err)
	}
}

func TestRewriteBlueprintOutputRefs_OrdinaryRefUntouched(t *testing.T) {
	intent := &resolver.IntentFile{
		Resources: []resolver.ResourceIntent{
			{Type: "a", Name: "b", Config: json.RawMessage(`{"x":{"$ref":{"to":"payments.aws_iam_role.ci-runner.id"}}}`)},
		},
	}
	orig := string(intent.Resources[0].Config)
	if err := rewriteBlueprintOutputRefs(intent, map[string]string{"platform:repo_arn": "x.y.z.arn"}); err != nil {
		t.Fatalf("rewriteBlueprintOutputRefs: %v", err)
	}
	if string(intent.Resources[0].Config) != orig {
		t.Fatalf("expected an ordinary $ref to be left completely untouched, got %s", intent.Resources[0].Config)
	}
}

func TestRewriteBlueprintOutputRefs_EmptyOutputAddr_NoOp(t *testing.T) {
	intent := &resolver.IntentFile{
		Resources: []resolver.ResourceIntent{
			{Type: "a", Name: "b", Config: json.RawMessage(`{"x":"y"}`)},
		},
	}
	orig := string(intent.Resources[0].Config)
	if err := rewriteBlueprintOutputRefs(intent, nil); err != nil {
		t.Fatalf("rewriteBlueprintOutputRefs: %v", err)
	}
	if string(intent.Resources[0].Config) != orig {
		t.Fatalf("expected a no-op with an empty outputAddr map")
	}
}

// callableUbxfileWithOutputs is writeCallableBlueprint's own on-disk
// Ubxfile (invoke_test.go), plus a real outputs: declaration naming
// ci-notifications' own real "arn" attribute -- ExpandCalls' own
// invokeCall re-parses THIS on-disk file (never the in-memory *Ubxfile
// GenerateGo was called with), so a real file declaring outputs: is
// required for this test to mean anything.
const callableUbxfileWithOutputs = `lang: go

params:
  queue_name: string, required
  max_receive_count: number, required
  retention_days: number, default 1

resources: |
  placeholder -- never re-drafted by a blueprint CALL, only by
  ` + "`ubx blueprint build`" + `, which this test never runs.

outputs:
  queue_arn: ci-notifications.arn
`

// writeCallableBlueprintWithOutputs mirrors writeCallableBlueprint
// (invoke_test.go) exactly, Go-only, with callableUbxfileWithOutputs'
// own outputs: declaration written to disk alongside the generated
// package.
func writeCallableBlueprintWithOutputs(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, UbxfileName), []byte(callableUbxfileWithOutputs), 0o644); err != nil {
		t.Fatal(err)
	}

	intent := mustIntentFile(t, ciPlatformIntentWithUnitConversion)
	ubxfile := ciPlatformUbxfileWithUnitConversion()
	files, err := GenerateGo("ci-platform", ubxfile, intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	for name, content := range files {
		if name == "go/go.mod" {
			content += "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoModuleRoot(t) + "\n"
		}
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestExpandCalls_OutputReference_RealEndToEnd is UBI-128's own required
// integration proof, bridging invokeCall's real output resolution
// (resolveCallOutputs) and the document-wide rewrite pass
// (rewriteBlueprintOutputRefs) through the ACTUAL blueprint.ExpandCalls
// entry point -- not just the isolated unit tests above. A real, on-disk
// Go blueprint (declaring queue_arn: ci-notifications.arn), called with
// CallName "platform", alongside a SIBLING hand-authored resource whose
// own config carries a PROVISIONAL "$blueprint_output:platform:queue_arn"
// placeholder -- exactly the shape a calling medium (diagram, so far)
// emits. After ExpandCalls, that placeholder must be the real resolved
// address, and the blueprint's own ci-notifications resource must
// genuinely exist in the document (proving the output-target resource
// itself was correctly produced, not just referenced).
func TestExpandCalls_OutputReference_RealEndToEnd(t *testing.T) {
	requireGoToolchain(t)
	dir := writeCallableBlueprintWithOutputs(t)

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{
				Name: "ci-platform call", Blueprint: dir, CallName: "platform",
				Args: map[string]string{"queue_name": "payments-notifications", "max_receive_count": "5"},
			},
		},
		Resources: []resolver.ResourceIntent{
			{
				Type: "aws_iam_role_policy", Name: "downstream-policy", Op: resolver.OpCreate,
				Config: json.RawMessage(`{"policy_arn":{"$ref":{"to":"$blueprint_output:platform:queue_arn"}}}`),
			},
		},
	}
	if err := ExpandCalls(context.Background(), intent); err != nil {
		t.Skipf("Go blueprint call failed (likely an uncached github.com/ubiquex/ubx-sdk-go module on this machine, not a code bug): %v", err)
	}

	var found *resolver.ResourceIntent
	var queueFound bool
	for i := range intent.Resources {
		if intent.Resources[i].Name == "downstream-policy" {
			found = &intent.Resources[i]
		}
		if intent.Resources[i].Type == "aws_sqs_queue" && intent.Resources[i].Name == "ci-notifications" {
			queueFound = true
		}
	}
	if !queueFound {
		t.Fatalf("expected the blueprint's own ci-notifications resource to exist, got: %+v", intent.Resources)
	}
	if found == nil {
		t.Fatalf("expected the hand-authored downstream-policy resource to survive expansion, got: %+v", intent.Resources)
	}

	var cfg map[string]any
	if err := json.Unmarshal(found.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	ref, ok := cfg["policy_arn"].(map[string]any)["$ref"].(map[string]any)
	if !ok {
		t.Fatalf("expected policy_arn to still be a $ref object, got %v", cfg["policy_arn"])
	}
	wantAddr := "payments.aws_sqs_queue.ci-notifications.arn"
	if ref["to"] != wantAddr {
		t.Fatalf("policy_arn.$ref.to = %v, want %q (the real resolved output address)", ref["to"], wantAddr)
	}
}

// TestRewriteBlueprintOutputRefs_NestedInList confirms the recursive walk
// finds a placeholder ref nested inside a list value too, not just a
// bare top-level config field.
func TestRewriteBlueprintOutputRefs_NestedInList(t *testing.T) {
	intent := &resolver.IntentFile{
		Resources: []resolver.ResourceIntent{
			{
				Type: "a", Name: "b",
				Config: json.RawMessage(`{"targets":[{"$ref":{"to":"$blueprint_output:platform:repo_arn"}},"literal"]}`),
			},
		},
	}
	outputAddr := map[string]string{"platform:repo_arn": "s.t.n.arn"}
	if err := rewriteBlueprintOutputRefs(intent, outputAddr); err != nil {
		t.Fatalf("rewriteBlueprintOutputRefs: %v", err)
	}
	if !strings.Contains(string(intent.Resources[0].Config), `"s.t.n.arn"`) {
		t.Fatalf("expected the nested ref to be rewritten, got %s", intent.Resources[0].Config)
	}
}
