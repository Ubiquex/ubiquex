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

	"github.com/ubiquex/ubiquex/core/resolver"
)

func mustIntentFile(t *testing.T, doc string) *resolver.IntentFile {
	t.Helper()
	var f resolver.IntentFile
	if err := json.Unmarshal([]byte(doc), &f); err != nil {
		t.Fatalf("unmarshal intent file: %v", err)
	}
	return &f
}

func ciPlatformUbxfile() *Ubxfile {
	return &Ubxfile{
		Dir:  "testdata",
		Lang: "go",
		Params: []Param{
			{Name: "repo_name", Type: ParamString, Required: true},
			{Name: "queue_name", Type: ParamString, Required: true},
			{Name: "retention_days", Type: ParamNumber, Default: 1},
		},
	}
}

const ciPlatformIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "ci-platform",
  "intent": {"summary": "CI platform"},
  "resources": [
    {
      "type": "aws_ecr_repository",
      "name": "ci-artifacts",
      "op": "create",
      "config": {"name": "{repo_name}", "image_tag_mutability": "IMMUTABLE"}
    },
    {
      "type": "aws_sqs_queue",
      "name": "ci-notifications",
      "op": "create",
      "config": {"name": "{queue_name}", "message_retention_seconds": 86400}
    },
    {
      "type": "aws_iam_role",
      "name": "ci-runner",
      "op": "create",
      "config": {"name": "ci-runner", "assume_role_policy": "{\"Version\":\"2012-10-17\"}"}
    },
    {
      "type": "aws_iam_role_policy",
      "name": "ci-runner-access",
      "op": "create",
      "config": {
        "name": "ci-runner-access",
        "role": {"$ref": {"to": "ci-platform.aws_iam_role.ci-runner.id"}},
        "targets": {
          "repo_arn": {"$ref": {"to": "ci-platform.aws_ecr_repository.ci-artifacts.arn"}},
          "queue_arn": {"$ref": {"to": "ci-platform.aws_sqs_queue.ci-notifications.arn"}}
        }
      }
    }
  ]
}`

func TestGenerateGo_CiPlatform(t *testing.T) {
	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GenerateGo("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	for _, want := range []string{"go.mod", "bindings.go", "ciplatform.go"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("missing generated file %q; got %v", want, keysOf(files))
		}
	}

	fn := files["ciplatform.go"]
	if !strings.Contains(fn, "func CiPlatform(repoName string, queueName string, opts ...Option) {") {
		t.Fatalf("ciplatform.go missing expected signature (required params direct, default param via functional options):\n%s", fn)
	}
	if !strings.Contains(fn, "Name: repoName,") {
		t.Fatalf("ciplatform.go should substitute the repoName param directly:\n%s", fn)
	}
	// retention_days (default 1) must be functional-options plumbing, not a
	// positional argument -- docs/blueprint.md's "Resolved defaults" design.
	if !strings.Contains(fn, "type Option func(*options)") {
		t.Fatalf("ciplatform.go missing the Option type:\n%s", fn)
	}
	if !strings.Contains(fn, "type options struct {\n\tretentionDays int\n}") {
		t.Fatalf("ciplatform.go missing the options struct with retentionDays:\n%s", fn)
	}
	if !strings.Contains(fn, "func WithRetentionDays(v int) Option {") {
		t.Fatalf("ciplatform.go missing WithRetentionDays:\n%s", fn)
	}
	if !strings.Contains(fn, "cfg := options{\n\t\tretentionDays: 1,\n\t}") {
		t.Fatalf("ciplatform.go should seed cfg with the declared default 1:\n%s", fn)
	}
	if !strings.Contains(fn, "for _, opt := range opts {\n\t\topt(&cfg)\n\t}") {
		t.Fatalf("ciplatform.go missing the options-applying loop:\n%s", fn)
	}
	// ci-notifications' message_retention_seconds is a literal in this
	// fixture's intent (not wired to retention_days at all) -- nothing here
	// asserts cfg.retentionDays is actually consumed; that's covered by a
	// separate fixture below (TestGenerateGo_DefaultParamOptionsPattern).
	// ci-runner is referenced by ci-runner-access, so it must get a local var.
	if !strings.Contains(fn, "ciRunner := sdk.Resource(CiRunner,") {
		t.Fatalf("ciplatform.go should assign a local var to a referenced resource:\n%s", fn)
	}
	if !strings.Contains(fn, "ciRunner.Field(\"id\")") {
		t.Fatalf("ciplatform.go should translate the $ref into a .Field() chain:\n%s", fn)
	}
	// The topological order must place ci-runner before ci-runner-access.
	if strings.Index(fn, "CiRunnerConfig{") > strings.Index(fn, "CiRunnerAccessConfig{") {
		t.Fatalf("ci-runner must be created before ci-runner-access:\n%s", fn)
	}
	// ci-runner-access has no dependent -- must NOT get an unused local var.
	if strings.Contains(fn, "ciRunnerAccess :=") {
		t.Fatalf("ci-runner-access is never referenced, should not get an assigned local var:\n%s", fn)
	}

	bindings := files["bindings.go"]
	if !strings.Contains(bindings, `WireType: "aws_ecr_repository"`) {
		t.Fatalf("bindings.go missing ecr binding:\n%s", bindings)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestGenerateGo_DuplicateIdentifier(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [
			{"type": "aws_iam_role", "name": "ci-runner", "op": "create", "config": {}},
			{"type": "aws_iam_policy", "name": "ci_runner", "op": "create", "config": {}}
		]
	}`)
	if _, err := GenerateGo("s", &Ubxfile{Lang: "go"}, intent); err == nil {
		t.Fatal("expected a collision error for ci-runner/ci_runner, got nil")
	}
}

func TestGenerateGo_DependencyCycle(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [
			{"type": "a", "name": "one", "op": "create", "config": {"x": {"$ref": {"to": "s.b.two.id"}}}},
			{"type": "b", "name": "two", "op": "create", "config": {"x": {"$ref": {"to": "s.a.one.id"}}}}
		]
	}`)
	if _, err := GenerateGo("s", &Ubxfile{Lang: "go"}, intent); err == nil {
		t.Fatal("expected a dependency cycle error, got nil")
	}
}

func TestGenerateGo_UndeclaredParamPlaceholder(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [{"type": "a", "name": "one", "op": "create", "config": {"name": "{not_declared}"}}]
	}`)
	if _, err := GenerateGo("s", &Ubxfile{Lang: "go"}, intent); err == nil {
		t.Fatal("expected an error for a placeholder naming an undeclared param, got nil")
	}
}

// TestGenerateGo_DefaultParamOptionIdentCollision proves
// checkOptionIdentCollisions is real: a resource named "option" would
// otherwise collide with the generated `type Option func(*options)` --
// hard build error, never a silent identifier clash the Go compiler would
// only report cryptically later.
func TestGenerateGo_DefaultParamOptionIdentCollision(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [{"type": "a", "name": "option", "op": "create", "config": {}}]
	}`)
	ubxfile := &Ubxfile{Lang: "go", Params: []Param{{Name: "retention_days", Type: ParamNumber, Default: 1}}}
	_, err := GenerateGo("s", ubxfile, intent)
	if err == nil {
		t.Fatal("expected a collision error between resource \"option\" and the generated Option type, got nil")
	}
	if !strings.Contains(err.Error(), "Option") {
		t.Fatalf("expected the collision error to name Option, got: %v", err)
	}
}

func TestGenerateGo_ModifyOpRejected(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [{"type": "a", "name": "one", "op": "modify", "config": {}}]
	}`)
	if _, err := GenerateGo("s", &Ubxfile{Lang: "go"}, intent); err == nil {
		t.Fatal("expected an error for op modify, got nil")
	}
}

// TestGenerateGo_CompilesClean is this package's own real, direct proof --
// not a string check -- that the generated CI-platform package is REAL,
// COMPILABLE Go: written to disk and `go build ./...`'d for real, exactly
// mirroring sdk/codegen/templates/go/collision_test.go's own
// buildGeneratedRepo (a local `replace` onto this repo's own sdk/go
// module root + GOPROXY=off, so this stays hermetic -- no network, no
// dependency on github.com/ubiquex/ubx-sdk-go actually being reachable).
func TestGenerateGo_CompilesClean(t *testing.T) {
	requireGoToolchain(t)

	intent := mustIntentFile(t, ciPlatformIntent)
	files, err := GenerateGo("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}

	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	sdkGoRoot := sdkGoModuleRoot(t)
	goMod := files["go.mod"] + "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build of the generated blueprint package failed:\n%s", out)
	}
}

// ciPlatformIntentWithDefaultUsed is ciPlatformIntent's own two-resource
// slice, but wires retention_days (the params: default entry) into a real
// Config field -- message_retention_seconds -- as a bare "{retention_days}"
// token, the exact shape a real intent-provider draft would produce for a
// numeric param interpolated directly (docs/blueprint.md's "Value
// translation" section). ciPlatformIntent itself never actually USES
// retention_days anywhere, so it can't prove cfg.retentionDays's own value
// actually reaches the wire -- this fixture exists specifically to prove
// that, by running the generated function for real (below), not just
// compiling it.
const ciPlatformIntentWithDefaultUsed = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "ci-platform",
  "intent": {"summary": "CI platform"},
  "resources": [
    {
      "type": "aws_sqs_queue",
      "name": "ci-notifications",
      "op": "create",
      "config": {"name": "{queue_name}", "message_retention_seconds": "{retention_days}"}
    }
  ]
}`

// TestGenerateGo_DefaultParamOptionsPattern is this package's own real,
// direct proof -- running the generated function, not just compiling it --
// that a params: default value is genuinely load-bearing at call time:
// omitting it uses the Ubxfile's own declared default (1), and
// With<Param>(...) genuinely overrides it. Resolves Slice 1's own named
// open point (docs/blueprint.md: "params: default values are parsed but
// not yet load-bearing at codegen time").
func TestGenerateGo_DefaultParamOptionsPattern(t *testing.T) {
	requireGoToolchain(t)
	sdkGoRoot := sdkGoModuleRoot(t)

	intent := mustIntentFile(t, ciPlatformIntentWithDefaultUsed)
	files, err := GenerateGo("ci-platform", ciPlatformUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}

	for _, tc := range []struct {
		name     string
		callArgs string
		want     float64
	}{
		{"default", `"payments-ci-artifacts", "payments-notifications"`, 1},
		{"overridden", `"payments-ci-artifacts", "payments-notifications", ciplatform.WithRetentionDays(30)`, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pkgDir := filepath.Join(dir, "ciplatform")
			if err := os.MkdirAll(pkgDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, content := range files {
				if name == "go.mod" {
					content += "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
				}
				if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			stackDir := filepath.Join(dir, "stack")
			if err := os.MkdirAll(stackDir, 0o755); err != nil {
				t.Fatal(err)
			}
			stackGoMod := "module stack\n\ngo 1.23\n\n" +
				"require github.com/ubiquex/ubx-sdk-go v0.0.0\nrequire ciplatform v0.0.0\n\n" +
				"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n" +
				"replace ciplatform => " + pkgDir + "\n"
			if err := os.WriteFile(filepath.Join(stackDir, "go.mod"), []byte(stackGoMod), 0o644); err != nil {
				t.Fatal(err)
			}
			mainSrc := "package main\n\n" +
				"import (\n\tciplatform \"ciplatform\"\n\tsdk \"github.com/ubiquex/ubx-sdk-go/runtime\"\n)\n\n" +
				"func main() {\n" +
				"\tsdk.Main(sdk.Stack(\"ci-platform\", func() {\n" +
				"\t\tsdk.Intent(sdk.IntentInfo{Summary: \"x\"})\n" +
				"\t\tciplatform.CiPlatform(" + tc.callArgs + ")\n" +
				"\t}))\n" +
				"}\n"
			if err := os.WriteFile(filepath.Join(stackDir, "main.go"), []byte(mainSrc), 0o644); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", ".")
			cmd.Dir = stackDir
			cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
			out, err := cmd.Output()
			if err != nil {
				var stderr []byte
				if ee, ok := err.(*exec.ExitError); ok {
					stderr = ee.Stderr
				}
				t.Fatalf("go run the calling stack failed: %v\nstdout: %s\nstderr: %s", err, out, stderr)
			}

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
				t.Fatalf("message_retention_seconds = %v, want %v (retention_days default/override not load-bearing): %s", got, tc.want, out)
			}
		})
	}
}

// ciPlatformIntentWithEmbeddedRef reproduces UBI-74 Slice 2's own real,
// live-found bug: a JSON-embedded $ref (an IAM policy's "Resource" field
// naming a sibling resource's ARN, docs/blueprint.md's own adversarial
// case) whose address is prefixed with the BUILD-time blueprint stack
// name ("ci-platform", GenerateGo's own first argument below) -- exactly
// the shape a real intent-provider draft produces, confirmed live against
// the real Claude API (STATE.md, UBI-74 Slice 2).
const ciPlatformIntentWithEmbeddedRef = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "ci-platform",
  "intent": {"summary": "CI platform"},
  "resources": [
    {
      "type": "aws_ecr_repository",
      "name": "container-repo",
      "op": "create",
      "config": {"name": "{repo_name}"}
    },
    {
      "type": "aws_iam_role_policy",
      "name": "ci-runner-access",
      "op": "create",
      "config": {
        "name": "ci-runner-access",
        "policy": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"ecr:GetAuthorizationToken\",\"Resource\":{\"$ref\":{\"to\":\"ci-platform.aws_ecr_repository.container-repo.arn\"}}}]}"
      }
    }
  ]
}`

// TestGenerateGo_EmbeddedRefUsesRuntimeStackName is this package's own
// real, direct proof -- running the generated function from a stack named
// DIFFERENTLY than the blueprint's own build-time stack name, not just
// compiling it -- that a JSON-embedded $ref's own address reflects the
// REAL CALLING stack at run time, not the literal build-time text. Before
// this fix, GenerateGo rendered the embedded $ref's address VERBATIM as
// an opaque Go string literal (docs/blueprint.md's own Slice 1
// "adversarial cases" framing, which turned out to be wrong once actually
// called from a real, differently-named stack -- UBI-74 Slice 2's own
// live-AWS verification hit this directly, not hypothetically): the
// resolver then rejects the address outright, since "ci-platform.
// aws_ecr_repository.container-repo.arn" names no resource in a stack
// actually named "payments". See renderEmbeddedRefString's own doc
// comment for the full account.
func TestGenerateGo_EmbeddedRefUsesRuntimeStackName(t *testing.T) {
	requireGoToolchain(t)
	sdkGoRoot := sdkGoModuleRoot(t)

	intent := mustIntentFile(t, ciPlatformIntentWithEmbeddedRef)
	ubxfile := &Ubxfile{
		Dir:  "testdata",
		Lang: "go",
		Params: []Param{
			{Name: "repo_name", Type: ParamString, Required: true},
		},
	}
	files, err := GenerateGo("ci-platform", ubxfile, intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}

	fn := files["ciplatform.go"]
	// The build-time stack name must never appear as a baked address
	// literal -- it must be reconstructed from the referenced resource's
	// own runtime Computed.Address() instead.
	if strings.Contains(fn, `ci-platform.aws_ecr_repository.container-repo.arn`) {
		t.Fatalf("ciplatform.go bakes in the build-time stack name as a literal address, should use containerRepo's own runtime Address() instead:\n%s", fn)
	}
	if !strings.Contains(fn, `containerRepo.Field("arn").Address()`) {
		t.Fatalf("ciplatform.go missing the runtime address expression for the embedded $ref:\n%s", fn)
	}

	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "ciplatform")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if name == "go.mod" {
			content += "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
		}
		if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stackGoMod := "module stack\n\ngo 1.23\n\n" +
		"require github.com/ubiquex/ubx-sdk-go v0.0.0\nrequire ciplatform v0.0.0\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n" +
		"replace ciplatform => " + pkgDir + "\n"
	if err := os.WriteFile(filepath.Join(stackDir, "go.mod"), []byte(stackGoMod), 0o644); err != nil {
		t.Fatal(err)
	}
	// Deliberately named "payments" -- NOT "ci-platform" -- the ordinary,
	// ubiquitous case: a real stack almost never shares its own name with
	// whatever directory a blueprint happened to be built in.
	mainSrc := "package main\n\n" +
		"import (\n\tciplatform \"ciplatform\"\n\tsdk \"github.com/ubiquex/ubx-sdk-go/runtime\"\n)\n\n" +
		"func main() {\n" +
		"\tsdk.Main(sdk.Stack(\"payments\", func() {\n" +
		"\t\tsdk.Intent(sdk.IntentInfo{Summary: \"x\"})\n" +
		"\t\tciplatform.CiPlatform(\"payments-ci-artifacts\")\n" +
		"\t}))\n" +
		"}\n"
	if err := os.WriteFile(filepath.Join(stackDir, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = stackDir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("go run the calling stack failed: %v\nstdout: %s\nstderr: %s", err, out, stderr)
	}

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
	if strings.Contains(policy, "ci-platform.aws_ecr_repository") {
		t.Fatalf("embedded $ref still carries the build-time stack name literally: %s", policy)
	}
}

// ciPlatformIntentWithUnitConversion reproduces UBI-123's own real,
// live-found bug: an SQS queue whose message_retention_seconds needs a
// UNIT CONVERSION from the blueprint's own natural param unit (days) to
// the real AWS wire unit (seconds) -- exactly the shape that shipped
// broken against real AWS (`InvalidAttributeValue: Invalid value for the
// parameter MessageRetentionPeriod`, retention_days=30 sent as
// message_retention_seconds=30 instead of 2592000). Also exercises a
// REQUIRED param's own arithmetic (max_receive_count -- an int doubled),
// since UBI-123's own root-cause framing ("any blueprint parameter that
// needs unit conversion or derived computation may be affected") isn't
// specific to default params.
const ciPlatformIntentWithUnitConversion = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "ci-platform",
  "intent": {"summary": "CI platform"},
  "resources": [
    {
      "type": "aws_sqs_queue",
      "name": "ci-notifications",
      "op": "create",
      "config": {
        "name": "{queue_name}",
        "message_retention_seconds": "{retention_days * 86400}",
        "visibility_timeout_seconds": "{max_receive_count * 2}"
      }
    }
  ]
}`

func ciPlatformUbxfileWithUnitConversion() *Ubxfile {
	return &Ubxfile{
		Dir:  "testdata",
		Lang: "go",
		Params: []Param{
			{Name: "queue_name", Type: ParamString, Required: true},
			{Name: "max_receive_count", Type: ParamNumber, Required: true},
			{Name: "retention_days", Type: ParamNumber, Default: 1},
		},
	}
}

// TestGenerateGo_PlaceholderArithmetic is UBI-123's own required hermetic
// regression test: an Ubxfile with a parameter that needs arithmetic in
// the generated resource config, confirming the generated Go performs the
// REAL computation, not a bare pass-through. Runs the generated function
// for real (go run, not go build) across three cases -- a required
// param's own arithmetic, a default param's own declared default used
// arithmetically, and that same default overridden -- decoding the
// emitted intent/v1 document's own JSON to check the actual computed
// number reaches the wire, exactly the way UBI-74 Slice 2's own
// TestGenerateGo_DefaultParamOptionsPattern already proved plain defaults
// load-bearing.
func TestGenerateGo_PlaceholderArithmetic(t *testing.T) {
	requireGoToolchain(t)
	sdkGoRoot := sdkGoModuleRoot(t)

	intent := mustIntentFile(t, ciPlatformIntentWithUnitConversion)
	files, err := GenerateGo("ci-platform", ciPlatformUbxfileWithUnitConversion(), intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}

	fn := files["ciplatform.go"]
	if !strings.Contains(fn, "cfg.retentionDays * 86400") {
		t.Fatalf("ciplatform.go missing the real days->seconds arithmetic (cfg.retentionDays * 86400), got a bare pass-through instead:\n%s", fn)
	}
	if !strings.Contains(fn, "maxReceiveCount * 2") {
		t.Fatalf("ciplatform.go missing the required param's own arithmetic (maxReceiveCount * 2):\n%s", fn)
	}

	for _, tc := range []struct {
		name              string
		callArgs          string
		wantRetentionSec  float64
		wantVisibilitySec float64
	}{
		{"default", `"payments-notifications", 5`, 86400, 10},
		{"overridden", `"payments-notifications", 5, ciplatform.WithRetentionDays(30)`, 2592000, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pkgDir := filepath.Join(dir, "ciplatform")
			if err := os.MkdirAll(pkgDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, content := range files {
				if name == "go.mod" {
					content += "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
				}
				if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			stackDir := filepath.Join(dir, "stack")
			if err := os.MkdirAll(stackDir, 0o755); err != nil {
				t.Fatal(err)
			}
			stackGoMod := "module stack\n\ngo 1.23\n\n" +
				"require github.com/ubiquex/ubx-sdk-go v0.0.0\nrequire ciplatform v0.0.0\n\n" +
				"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n" +
				"replace ciplatform => " + pkgDir + "\n"
			if err := os.WriteFile(filepath.Join(stackDir, "go.mod"), []byte(stackGoMod), 0o644); err != nil {
				t.Fatal(err)
			}
			mainSrc := "package main\n\n" +
				"import (\n\tciplatform \"ciplatform\"\n\tsdk \"github.com/ubiquex/ubx-sdk-go/runtime\"\n)\n\n" +
				"func main() {\n" +
				"\tsdk.Main(sdk.Stack(\"ci-platform\", func() {\n" +
				"\t\tsdk.Intent(sdk.IntentInfo{Summary: \"x\"})\n" +
				"\t\tciplatform.CiPlatform(" + tc.callArgs + ")\n" +
				"\t}))\n" +
				"}\n"
			if err := os.WriteFile(filepath.Join(stackDir, "main.go"), []byte(mainSrc), 0o644); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "run", ".")
			cmd.Dir = stackDir
			cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
			out, err := cmd.Output()
			if err != nil {
				var stderr []byte
				if ee, ok := err.(*exec.ExitError); ok {
					stderr = ee.Stderr
				}
				t.Fatalf("go run the calling stack failed: %v\nstdout: %s\nstderr: %s", err, out, stderr)
			}

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

// TestGenerateGo_PlaceholderArithmeticRejectsNonNumberParam confirms
// arithmetic on a non-number-typed param is a hard build error, never a
// silently-broken Go expression (e.g. a string "*" 3, which wouldn't
// compile, or worse, would compile into something nonsensical for a
// looser field type) -- matching this codebase's own "hard error, never
// silent" posture for every other adversarial case in this package.
func TestGenerateGo_PlaceholderArithmeticRejectsNonNumberParam(t *testing.T) {
	intent := mustIntentFile(t, `{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "s",
		"intent": {"summary": "x"},
		"resources": [{"type": "a", "name": "one", "op": "create", "config": {"name": "{repo_name * 2}"}}]
	}`)
	ubxfile := &Ubxfile{Lang: "go", Params: []Param{{Name: "repo_name", Type: ParamString, Required: true}}}
	_, err := GenerateGo("s", ubxfile, intent)
	if err == nil {
		t.Fatal("expected a hard error for arithmetic on a string param, got nil")
	}
	if !strings.Contains(err.Error(), "repo_name") || !strings.Contains(err.Error(), "number") {
		t.Fatalf("expected the error to name repo_name and the number-only restriction, got: %v", err)
	}
}

func requireGoToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not found in PATH -- skipping the real `go build` half of this test")
	}
}

// sdkGoModuleRoot resolves this repo's own sdk/go module root -- the
// identical path sdk/codegen/templates/go/collision_test.go's own
// function of the same name resolves, independently re-derived here
// since that helper is unexported in a different package.
func sdkGoModuleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not determine this test file's own path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "sdk", "go"))
	if err != nil {
		t.Fatalf("resolve sdk/go path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected %s to be sdk/go's own module root (containing go.mod): %v", root, err)
	}
	return root
}
