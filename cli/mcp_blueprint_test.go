package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMCP_DraftUbxfile_ValidResult proves the real, end-to-end assembly
// path: structured pieces in, a valid Ubxfile + resources.json out, self-
// validated internally (no separate validate_ubxfile call needed to
// learn the result is valid).
func TestMCP_DraftUbxfile_ValidResult(t *testing.T) {
	session := connectMCPTestClient(t)
	res := callTool(t, session, "draft_ubxfile", map[string]any{
		"lang":      "go",
		"stack":     "ci-platform",
		"summary":   "CI platform blueprint",
		"resources": `[{"type":"aws_ecr_repository","name":"ci-artifacts","op":"create","config":{"name":"{repo_name}"}}]`,
		"params": []map[string]any{
			{"name": "repo_name", "type": "string", "required": true},
		},
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", toolTextContent(t, res))
	}
	payload, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON object, got %T", res.StructuredContent)
	}
	if payload["valid"] != true {
		t.Fatalf("expected valid=true, got: %v", payload)
	}
	ubxfile, _ := payload["ubxfile"].(string)
	if ubxfile == "" || !contains(ubxfile, "lang: go") || !contains(ubxfile, "resources: resources.json") {
		t.Fatalf("expected a real assembled Ubxfile, got: %q", ubxfile)
	}
	resourcesJSON, _ := payload["resources_json"].(string)
	if resourcesJSON == "" || !contains(resourcesJSON, "aws_ecr_repository") {
		t.Fatalf("expected real resources.json content, got: %q", resourcesJSON)
	}
	if payload["resource_count"] != float64(1) {
		t.Fatalf("expected resource_count=1, got: %v", payload["resource_count"])
	}
}

// TestMCP_DraftUbxfile_InvalidResourceReturnsValidFalse proves an
// invalid draft is reported as data (valid=false), never a tool-call
// error -- an expected, ordinary outcome to reason about.
func TestMCP_DraftUbxfile_InvalidResourceReturnsValidFalse(t *testing.T) {
	session := connectMCPTestClient(t)
	res := callTool(t, session, "draft_ubxfile", map[string]any{
		"lang":    "go",
		"stack":   "ci-platform",
		"summary": "broken",
		// op "modify" is refused -- a blueprint template only ever
		// describes new resources (blueprint/decode.go's own
		// decodeBlueprint check).
		"resources": `[{"type":"aws_ecr_repository","name":"x","op":"modify","config":{}}]`,
	})
	if res.IsError {
		t.Fatalf("expected a normal (non-error) result with valid=false, got tool error: %s", toolTextContent(t, res))
	}
	payload, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON object, got %T", res.StructuredContent)
	}
	if payload["valid"] != false {
		t.Fatalf("expected valid=false, got: %v", payload)
	}
	if msg, _ := payload["validation_error"].(string); msg == "" {
		t.Fatalf("expected a real validation_error message, got: %v", payload)
	}
}

// TestMCP_ValidateUbxfile_InlineContent proves the round trip:
// draft_ubxfile's own output, fed straight into validate_ubxfile as
// inline content (never saved anywhere), validates cleanly through the
// SAME blueprint.Validate call.
func TestMCP_ValidateUbxfile_InlineContent(t *testing.T) {
	session := connectMCPTestClient(t)
	draftRes := callTool(t, session, "draft_ubxfile", map[string]any{
		"lang":      "go",
		"stack":     "ci-platform",
		"summary":   "CI platform blueprint",
		"resources": `[{"type":"aws_ecr_repository","name":"ci-artifacts","op":"create","config":{"name":"fixed"}}]`,
	})
	if draftRes.IsError {
		t.Fatalf("draft_ubxfile: %s", toolTextContent(t, draftRes))
	}
	draftPayload := draftRes.StructuredContent.(map[string]any)

	res := callTool(t, session, "validate_ubxfile", map[string]any{
		"ubxfile":   draftPayload["ubxfile"],
		"resources": draftPayload["resources_json"],
	})
	if res.IsError {
		t.Fatalf("validate_ubxfile: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	if payload["valid"] != true {
		t.Fatalf("expected valid=true, got: %v", payload)
	}
	if payload["stack"] != "ci-platform" {
		t.Fatalf("expected stack=ci-platform, got: %v", payload)
	}
}

// TestMCP_ValidateUbxfile_Dir proves the "I already have one checked in"
// flow, against a real on-disk directory (the same fixture
// cli/blueprint_test.go's own CLI tests use).
func TestMCP_ValidateUbxfile_Dir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBlueprintTestResources(t, dir)
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\nparams:\n  repo_name: string, required\nresources: resources.json\n")

	session := connectMCPTestClient(t)
	res := callTool(t, session, "validate_ubxfile", map[string]any{"dir": dir})
	if res.IsError {
		t.Fatalf("validate_ubxfile: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	if payload["valid"] != true {
		t.Fatalf("expected valid=true, got: %v", payload)
	}
	params, ok := payload["params"].([]any)
	if !ok || len(params) != 1 {
		t.Fatalf("expected 1 param, got: %v", payload["params"])
	}
}

// TestMCP_ValidateUbxfile_MissingBoth proves the tool refuses cleanly
// when neither dir nor inline content is given, rather than silently
// treating an empty input as valid.
func TestMCP_ValidateUbxfile_MissingBoth(t *testing.T) {
	session := connectMCPTestClient(t)
	res := callTool(t, session, "validate_ubxfile", map[string]any{})
	if !res.IsError {
		t.Fatalf("expected a tool error for missing dir/ubxfile, got: %v", res.StructuredContent)
	}
}

// TestMCP_BuildBlueprint_ReturnsFilesNotWritten is the core write-
// versus-return proof: building from inline content (nothing ever
// saved anywhere by the caller) returns real, compilable file content
// inline, and never writes anything to disk -- there is no opt-in to
// turn that off, unlike an earlier version of this tool.
func TestMCP_BuildBlueprint_ReturnsFilesNotWritten(t *testing.T) {
	session := connectMCPTestClient(t)
	res := callTool(t, session, "build_blueprint", map[string]any{
		"ubxfile":   "lang: go\nparams:\n  repo_name: string, required\nresources: resources.json\n",
		"resources": blueprintTestDraft,
	})
	if res.IsError {
		t.Fatalf("build_blueprint: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	files, ok := payload["files"].(map[string]any)
	if !ok || len(files) == 0 {
		t.Fatalf("expected real generated files inline, got: %v", payload)
	}
	found := false
	for name, content := range files {
		if contains(name, "go.mod") {
			found = true
			if s, _ := content.(string); s == "" {
				t.Fatalf("go.mod content is empty")
			}
		}
	}
	if !found {
		t.Fatalf("expected a go.mod among the returned files, got: %v", files)
	}
	if _, wrote := payload["written_to"]; wrote {
		t.Fatalf("expected no written_to field, this tool never writes to disk, got: %v", payload["written_to"])
	}
}

// TestMCP_ListBlueprints_FindsRealUbxfile proves list_blueprints walks a
// real directory tree and reports a real, parsed blueprint -- not a
// registry query, since none exists yet.
func TestMCP_ListBlueprints_FindsRealUbxfile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBlueprintTestResources(t, dir)
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\nparams:\n  repo_name: string, required\nresources: resources.json\n")

	session := connectMCPTestClient(t)
	res := callTool(t, session, "list_blueprints", map[string]any{"root_dir": root})
	if res.IsError {
		t.Fatalf("list_blueprints: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	blueprints, ok := payload["blueprints"].([]any)
	if !ok || len(blueprints) != 1 {
		t.Fatalf("expected exactly 1 blueprint found, got: %v", payload)
	}
	entry := blueprints[0].(map[string]any)
	if entry["valid"] != true || entry["name"] != "ci-platform" {
		t.Fatalf("expected a valid ci-platform entry, got: %v", entry)
	}
}

// TestMCP_ListBlueprints_EmptyDirReportsEmpty proves an ordinary
// directory with no blueprints is a clean, empty result, not an error.
func TestMCP_ListBlueprints_EmptyDirReportsEmpty(t *testing.T) {
	session := connectMCPTestClient(t)
	res := callTool(t, session, "list_blueprints", map[string]any{"root_dir": t.TempDir()})
	if res.IsError {
		t.Fatalf("list_blueprints: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	if blueprints, ok := payload["blueprints"].([]any); ok && len(blueprints) != 0 {
		t.Fatalf("expected zero blueprints, got: %v", blueprints)
	}
}

// TestMCP_DescribeBlueprint_LocalDir proves the read-only, scratch-
// directory pull-and-describe path against a real local source, the
// simplest of blueprint.Pull's four real forms.
func TestMCP_DescribeBlueprint_LocalDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBlueprintTestResources(t, dir)
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\nparams:\n  repo_name: string, required\nresources: resources.json\n")

	session := connectMCPTestClient(t)
	res := callTool(t, session, "describe_blueprint", map[string]any{"source": dir})
	if res.IsError {
		t.Fatalf("describe_blueprint: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	if payload["valid"] != true {
		t.Fatalf("expected valid=true, got: %v", payload)
	}
	if payload["stack"] != "ci-platform" {
		t.Fatalf("expected stack=ci-platform, got: %v", payload)
	}
	if payload["packaged"] != false {
		t.Fatalf("expected packaged=false for an unpackaged source dir, got: %v", payload["packaged"])
	}

	// Read-only: describing must never leave the source directory
	// modified (no blueprint.lock.json, no go/ts/py packages written
	// into the ORIGINAL dir -- only the scratch copy, already cleaned
	// up, could have been touched).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected the source dir to still hold exactly its original 2 files (Ubxfile, resources.json), got %d: %v", len(entries), entries)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
