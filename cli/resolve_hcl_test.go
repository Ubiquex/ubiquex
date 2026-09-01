package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeUbxHCL writes a real .ubx.hcl file -- content is the caller's own
// exact HCL text, this helper just puts it on disk at the standard name
// resolve's own extension dispatch (strings.HasSuffix ".ubx.hcl") needs
// to see.
func writeUbxHCL(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "stack.ubx.hcl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestResolve_FromCode_UbxHCL_CallsRealLocalBlueprint is UBI-226's own
// required end-to-end proof, the thing hclstack's own package tests
// cannot cover alone (they only prove the parser's output shape, never
// that --from-code actually routes a .ubx.hcl file through it into the
// SAME shared blueprint_calls/ExpandCalls pipeline every other medium
// already uses): a real .ubx.hcl file calling a real, locally-built Go
// blueprint package (writeBlueprintPackage, blueprint_call_test.go's own
// fixture -- the identical primary+mirror fake_widget pair, mirror's
// name/tags cross-referencing primary via real $ref markers) resolves
// through the real cobra command tree into a draft proposal with both
// resources present, param values threaded through correctly.
func TestResolve_FromCode_UbxHCL_CallsRealLocalBlueprint(t *testing.T) {
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// writeBlueprintPackage writes dir/platform/go (the built package)
	// and dir/platform/Ubxfile (a placeholder, never parsed by this call
	// path) -- the call target is the ROOT (dir/platform), matching
	// pickCallLanguage's own "dest/go" lookup in blueprint/invoke.go.
	writeBlueprintPackage(t, dir, "platform")
	blueprintRoot := filepath.Join(dir, "platform")

	hclPath := writeUbxHCL(t, dir, `
stack = "demo"

blueprint "widget" "platform" {
  source       = "`+blueprintRoot+`"
  primary_name = "widget-from-hcl"
}
`)

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	out, err := runUbx(t, env, "resolve",
		"--from-code", hclPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
	)
	if err != nil {
		t.Fatalf("ubx resolve --from-code %s: %v\noutput: %s", hclPath, err, out)
	}
	if !strings.Contains(out, "demo: 2 create(s), 0 change(s)") {
		t.Fatalf("expected a 2-create summary for stack demo, got: %s", out)
	}

	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	var proposal map[string]any
	if err := json.Unmarshal(raw, &proposal); err != nil {
		t.Fatalf("unmarshal resolved proposal: %v", err)
	}
	delta, ok := proposal["delta"].(map[string]any)
	if !ok {
		t.Fatalf("resolved proposal missing delta: %s", raw)
	}
	creates, ok := delta["creates"].([]any)
	if !ok || len(creates) != 2 {
		t.Fatalf("expected exactly 2 creates, got: %s", raw)
	}

	var primaryName any
	for _, c := range creates {
		m := c.(map[string]any)
		if m["name"] == "primary" {
			cfg := m["config"].(map[string]any)
			primaryName = cfg["name"]
		}
	}
	if primaryName != "widget-from-hcl" {
		t.Fatalf("expected primary's own name to be \"widget-from-hcl\" (threaded through from the .ubx.hcl file's own primary_name arg), got %v in: %s", primaryName, raw)
	}
}

// TestResolve_FromCode_UbxHCL_ParseErrorSurfacesAsExitCode2 confirms a
// malformed .ubx.hcl file (missing the required top-level stack
// attribute here) surfaces as an ordinary resolve error, exit code 2,
// the same audit outcome as any other resolve failure -- not a panic,
// not a silently empty proposal.
func TestResolve_FromCode_UbxHCL_ParseErrorSurfacesAsExitCode2(t *testing.T) {
	dir := t.TempDir()
	hclPath := writeUbxHCL(t, dir, `
blueprint "widget" "platform" {
  source = "../platform"
}
`)
	_, err := runUbx(t, nil, "resolve", "--from-code", hclPath, "--ledger-dir", dir)
	if err == nil {
		t.Fatal("expected an error for a .ubx.hcl file missing its required stack attribute")
	}
	if !strings.Contains(err.Error(), "\"stack\" attribute is required") {
		t.Fatalf("expected the real hclstack parse error to surface, got: %v", err)
	}
}
