package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBlueprintBindingBypassStack writes a plain SDK program that
// imports a real, locally-built blueprint package's own bindings
// DIRECTLY (platform.Primary/platform.PrimaryConfig) and calls
// sdk.Resource() on them by hand -- never calling the blueprint's own
// wrapper function (platform.Platform(...)) at all. This is UBI-225's
// own real reachable-by-accident case: a stack author reusing one
// resource type from a blueprint's own generated package, or who never
// learns there's a separate "correct" entry point, reaches for exactly
// this call shape -- the SAME sdk.Resource(SomeBinding, name,
// SomeConfig{...}) pattern every ordinary provider SDK tutorial teaches.
func writeBlueprintBindingBypassStack(t *testing.T, dir, pkgDir string) string {
	t.Helper()
	stackDir := filepath.Join(dir, "bypass-stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sdkGoRoot := blueprintCallSdkGoRoot(t)
	goMod := "module blueprint-bypass-fixture\n\ngo 1.23\n\n" +
		"require github.com/ubiquex/ubx-sdk-go v0.0.0\n" +
		"require platform v0.0.0\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n" +
		"replace platform => " + pkgDir + "\n"
	if err := os.WriteFile(filepath.Join(stackDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := filepath.Join(stackDir, "create_platform.go")
	src := "package main\n\n" +
		"import (\n" +
		"\tplatform \"platform\"\n" +
		"\tsdk \"github.com/ubiquex/ubx-sdk-go/runtime\"\n" +
		")\n\n" +
		"func main() {\n" +
		"\tsdk.Main(sdk.Stack(\"platform\", func() {\n" +
		"\t\tsdk.Intent(sdk.IntentInfo{Summary: \"platform, bypassing the blueprint's own wrapper function\"})\n" +
		"\t\tsdk.Resource(platform.Primary, \"bypass\", platform.PrimaryConfig{Name: \"bypass-widget\"})\n" +
		"\t}))\n" +
		"}\n"
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

// TestBlueprintBinding_DirectImportBypass_NowHasProvenance is UBI-225's
// own required regression proof, closing the known gap UBI-225 found
// still real and reachable: before this fix, a resource built by
// importing a blueprint's own generated binding/config directly and
// calling sdk.Resource() by hand -- never through the blueprint's own
// wrapper function -- shipped with "sources": null, completely
// indistinguishable from an ordinary hand-written resource in the
// resolved proposal, `ubx why`, and `ubx render` alike, even though the
// binding itself came from a blueprint. ResourceBinding now carries
// BlueprintName (stamped by blueprint/gogen.go's own renderGoBindings),
// and sdk.Resource checks it as a fallback exactly when no
// PushBlueprintSource scope is open -- this test proves the fix reaches
// all the way through resolve, ubx why, and ubx render, the same three
// real surfaces UBI-225's own report checked by hand.
func TestBlueprintBinding_DirectImportBypass_NowHasProvenance(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	pkgDir := writeBlueprintPackage(t, dir, "platform")
	entry := writeBlueprintBindingBypassStack(t, dir, pkgDir)

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve",
		"--from-code", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
		"--timeout", "60s",
	)
	if err != nil {
		t.Fatalf("ubx resolve --from-code (blueprint binding bypass): %v\noutput: %s", err, resolveOut)
	}

	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Delta struct {
			Creates []struct {
				Type    string `json:"type"`
				Name    string `json:"name"`
				Sources []struct {
					Kind string `json:"kind"`
					Ref  string `json:"ref"`
				} `json:"sources"`
			} `json:"creates"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse resolved proposal: %v\nraw: %s", err, raw)
	}
	if len(doc.Delta.Creates) != 1 {
		t.Fatalf("expected 1 create (bypass), got %d: %s", len(doc.Delta.Creates), raw)
	}
	c := doc.Delta.Creates[0]
	if len(c.Sources) != 1 {
		t.Fatalf("%s.%s: expected exactly 1 source now that the binding carries its own BlueprintName, got %d: %s", c.Type, c.Name, len(c.Sources), raw)
	}
	if c.Sources[0].Kind != "blueprint" {
		t.Fatalf("%s.%s: source kind = %q, want \"blueprint\": %s", c.Type, c.Name, c.Sources[0].Kind, raw)
	}
	name, hash, ok := strings.Cut(c.Sources[0].Ref, ":")
	if !ok || name != "platform" || !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("%s.%s: ref = %q, want \"platform:sha256:<hex>\": %s", c.Type, c.Name, c.Sources[0].Ref, raw)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)
	if _, err := runUbx(t, env, "ship", changeID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx ship: %v", err)
	}

	whyOut, err := runUbx(t, nil, "why", "platform.fake_widget.bypass", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "source: blueprint platform:sha256:") {
		t.Fatalf("ubx why on a bypass-constructed blueprint resource is missing provenance -- the UBI-225 gap, unfixed:\n%s", whyOut)
	}

	renderOut, err := runUbx(t, nil, "render", "--stack", "platform", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render: %v\noutput: %s", err, renderOut)
	}
	if !strings.Contains(renderOut, "style.stroke-dash: 3") {
		t.Fatalf("ubx render on a bypass-constructed blueprint resource is missing the dashed-border grouping -- the UBI-225 gap, unfixed:\n%s", renderOut)
	}
	if !strings.Contains(renderOut, `"bypass"`) {
		t.Fatalf("ubx render missing the bypass resource:\n%s", renderOut)
	}
}
