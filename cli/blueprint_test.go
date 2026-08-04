package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const blueprintTestDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "ci-platform",
  "intent": {"summary": "CI platform blueprint"},
  "resources": [
    {"type": "aws_ecr_repository", "name": "ci-artifacts", "op": "create", "config": "{\"name\": \"{repo_name}\"}"}
  ]
}`

func TestBlueprintBuild_WritesCompilableGoPackage(t *testing.T) {
	fake := &fakeIntentAdapter{draft: blueprintTestDraft}
	withBuildIntentAdapter(t, fake)

	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\nparams:\n  repo_name: string, required\nresources: |\n  An ECR repository called \"{repo_name}\".\n")

	out, err := runUbx(t, nil, "blueprint", "build", dir)
	if err != nil {
		t.Fatalf("blueprint build: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "built 1 resource(s)") {
		t.Fatalf("output = %q, want a built-resource-count line", out)
	}

	for _, want := range []string{"go.mod", "bindings.go", "ciplatform.go"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Fatalf("expected %s to be written: %v", want, err)
		}
	}

	fn, err := os.ReadFile(filepath.Join(dir, "ciplatform.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fn), "func CiPlatform(repoName string) {") {
		t.Fatalf("ciplatform.go missing expected signature:\n%s", fn)
	}

	// The adapter should have received the parameter-preservation
	// preamble, not the bare resources: prose.
	if !strings.Contains(fake.lastReq.Stack, "ci-platform") {
		t.Fatalf("draft stack = %q, want the blueprint's own directory-derived name", fake.lastReq.Stack)
	}
	if !strings.Contains(string(fake.lastReq.Content), "repo_name") || !strings.Contains(string(fake.lastReq.Content), "PARAMETERIZED BLUEPRINT") {
		t.Fatalf("adapter content missing the parameter-preservation preamble:\n%s", fake.lastReq.Content)
	}
}

func TestBlueprintBuild_MissingUbxfile(t *testing.T) {
	dir := t.TempDir()
	if _, err := runUbx(t, nil, "blueprint", "build", dir); err == nil {
		t.Fatal("expected an error when no Ubxfile exists, got nil")
	}
}

func TestBlueprintBuild_DefaultsToCurrentDirectory(t *testing.T) {
	fake := &fakeIntentAdapter{draft: blueprintTestDraft}
	withBuildIntentAdapter(t, fake)

	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\nparams:\n  repo_name: string, required\nresources: hello\n")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if _, err := runUbx(t, nil, "blueprint", "build"); err != nil {
		t.Fatalf("blueprint build (no dir arg): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ciplatform.go")); err != nil {
		t.Fatalf("expected ciplatform.go to be written into the current directory: %v", err)
	}
}
