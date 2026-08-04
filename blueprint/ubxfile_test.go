package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUbxfile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, UbxfileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestParseUbxfile_Full(t *testing.T) {
	dir := writeUbxfile(t, `lang: go

params:
  repo_name: string, required
  queue_name: string, required
  retention_days: number, default 1

resources: |
  An ECR repository called "{repo_name}".
`)

	uf, err := ParseUbxfile(dir)
	if err != nil {
		t.Fatalf("ParseUbxfile: %v", err)
	}
	if uf.Lang != "go" {
		t.Fatalf("Lang = %q, want go", uf.Lang)
	}
	if len(uf.Params) != 3 {
		t.Fatalf("len(Params) = %d, want 3", len(uf.Params))
	}
	// Declaration order must be preserved, not map order.
	wantNames := []string{"repo_name", "queue_name", "retention_days"}
	for i, want := range wantNames {
		if uf.Params[i].Name != want {
			t.Fatalf("Params[%d].Name = %q, want %q", i, uf.Params[i].Name, want)
		}
	}
	if !uf.Params[0].Required || uf.Params[0].Type != ParamString {
		t.Fatalf("Params[0] = %+v, want required string", uf.Params[0])
	}
	if uf.Params[2].Required || uf.Params[2].Type != ParamNumber || uf.Params[2].Default != 1 {
		t.Fatalf("Params[2] = %+v, want number default 1", uf.Params[2])
	}
	if uf.ResourcesSource != "inline" {
		t.Fatalf("ResourcesSource = %q, want inline", uf.ResourcesSource)
	}
	if !strings.Contains(uf.Resources, "{repo_name}") {
		t.Fatalf("Resources = %q, want to contain {repo_name}", uf.Resources)
	}
}

func TestParseUbxfile_ResourcesFromFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "platform.md"), []byte("# platform\n\nAn SQS queue.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, UbxfileName), []byte("lang: go\nresources: ./platform.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	uf, err := ParseUbxfile(dir)
	if err != nil {
		t.Fatalf("ParseUbxfile: %v", err)
	}
	if uf.ResourcesSource == "inline" {
		t.Fatalf("ResourcesSource = inline, want the resolved platform.md path")
	}
	if !strings.Contains(uf.Resources, "SQS queue") {
		t.Fatalf("Resources = %q, want the included file's own content", uf.Resources)
	}
}

func TestParseUbxfile_MissingMdFallsBackToInline(t *testing.T) {
	dir := writeUbxfile(t, "lang: go\nresources: ./does-not-exist.md\n")

	uf, err := ParseUbxfile(dir)
	if err != nil {
		t.Fatalf("ParseUbxfile: %v", err)
	}
	if uf.ResourcesSource != "inline" {
		t.Fatalf("ResourcesSource = %q, want inline (no such file on disk)", uf.ResourcesSource)
	}
	if uf.Resources != "./does-not-exist.md" {
		t.Fatalf("Resources = %q, want the literal value", uf.Resources)
	}
}

func TestParseUbxfile_UnknownKeyRejected(t *testing.T) {
	dir := writeUbxfile(t, "lang: go\nresources: hello\nuses:\n  standard-vpc:v2:\n    cidr_block: \"10.0.0.0/16\"\n")

	_, err := ParseUbxfile(dir)
	if err == nil {
		t.Fatal("expected an error for the unrecognized uses: key, got nil")
	}
	if !strings.Contains(err.Error(), "uses") {
		t.Fatalf("error = %v, want it to name the unrecognized key", err)
	}
}

func TestParseUbxfile_MissingLang(t *testing.T) {
	dir := writeUbxfile(t, "resources: hello\n")
	if _, err := ParseUbxfile(dir); err == nil {
		t.Fatal("expected an error for missing lang:, got nil")
	}
}

func TestParseUbxfile_UnsupportedLang(t *testing.T) {
	dir := writeUbxfile(t, "lang: ts\nresources: hello\n")
	_, err := ParseUbxfile(dir)
	if err == nil {
		t.Fatal("expected an error for lang: ts (Slice 1 only supports go), got nil")
	}
	if !strings.Contains(err.Error(), "ts") {
		t.Fatalf("error = %v, want it to name the unsupported lang", err)
	}
}

func TestParseUbxfile_MissingResources(t *testing.T) {
	dir := writeUbxfile(t, "lang: go\n")
	if _, err := ParseUbxfile(dir); err == nil {
		t.Fatal("expected an error for missing resources:, got nil")
	}
}

func TestParseUbxfile_ParamMissingRequiredOrDefault(t *testing.T) {
	dir := writeUbxfile(t, "lang: go\nparams:\n  repo_name: string\nresources: hello\n")
	_, err := ParseUbxfile(dir)
	if err == nil {
		t.Fatal("expected an error for a param spec with neither required nor default, got nil")
	}
}

func TestParseUbxfile_ParamUnrecognizedType(t *testing.T) {
	dir := writeUbxfile(t, "lang: go\nparams:\n  tags: list, required\nresources: hello\n")
	_, err := ParseUbxfile(dir)
	if err == nil {
		t.Fatal("expected an error for an unrecognized param type, got nil")
	}
}

func TestParseUbxfile_DuplicateParam(t *testing.T) {
	dir := writeUbxfile(t, "lang: go\nparams:\n  repo_name: string, required\n  repo_name: string, required\nresources: hello\n")
	_, err := ParseUbxfile(dir)
	if err == nil {
		t.Fatal("expected an error for a duplicate param name, got nil")
	}
}

func TestParseUbxfile_BoolAndStringDefaults(t *testing.T) {
	dir := writeUbxfile(t, "lang: go\nparams:\n  enabled: bool, default true\n  team: string, default \"payments\"\nresources: hello\n")
	uf, err := ParseUbxfile(dir)
	if err != nil {
		t.Fatalf("ParseUbxfile: %v", err)
	}
	if uf.Params[0].Default != true {
		t.Fatalf("Params[0].Default = %v, want true", uf.Params[0].Default)
	}
	if uf.Params[1].Default != "payments" {
		t.Fatalf("Params[1].Default = %v, want payments", uf.Params[1].Default)
	}
}

func TestParseUbxfile_NoParams(t *testing.T) {
	dir := writeUbxfile(t, "lang: go\nresources: hello\n")
	uf, err := ParseUbxfile(dir)
	if err != nil {
		t.Fatalf("ParseUbxfile: %v", err)
	}
	if len(uf.Params) != 0 {
		t.Fatalf("len(Params) = %d, want 0", len(uf.Params))
	}
}

func TestParseUbxfile_MissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := ParseUbxfile(dir); err == nil {
		t.Fatal("expected an error when no Ubxfile exists, got nil")
	}
}
