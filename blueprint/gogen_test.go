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
	if !strings.Contains(fn, "func CiPlatform(repoName string, queueName string, retentionDays int) {") {
		t.Fatalf("ciplatform.go missing expected signature:\n%s", fn)
	}
	if !strings.Contains(fn, "Name: repoName,") {
		t.Fatalf("ciplatform.go should substitute the repoName param directly:\n%s", fn)
	}
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
