package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

func TestProposeFromDiagram_NoProvidersDeclared_Errors(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, "") // no [providers] table at all

	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, "payments: {\n  vpc: main-vpc\n}\n")

	out, err := runUbx(t, nil, "propose", "--from-diagram", diagramPath, "--stack", "payments")
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "[providers]") {
		t.Fatalf("expected the error to name the missing [providers] table, got: %v", err)
	}
}

func TestProposeFromDiagram_NoStack_Errors(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, "")

	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, "payments: {\n  vpc: main-vpc\n}\n")

	out, err := runUbx(t, nil, "propose", "--from-diagram", diagramPath)
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "--stack is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProposeFromDiagram_WithArg_MutuallyExclusive(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, "")

	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, "payments: {\n  vpc: main-vpc\n}\n")
	proposalPath := filepath.Join(dir, "proposal.json")
	writeFile(t, proposalPath, "{}")

	out, err := runUbx(t, nil, "propose", proposalPath, "--from-diagram", diagramPath, "--stack", "payments")
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestProposeFromDiagram_EndToEnd_ViaMirror is the live-shape finale for
// slice 3 (docs/diagram-medium.md): a real .d2 topology, real provider
// schema fetched via the UBX_PROVIDER_MIRROR seam (same mechanism
// cli/sdk_test.go uses), parsed end to end through the actual CLI
// command. Checks all three things slice 3 was scoped to wire correctly:
// sources provenance pinning (a single "document" entry naming the
// diagram file), ambiguity rendering (the edge into the reference node
// produces a visible defaults[] note -- the $cross structural
// limitation, not a real $cross marker), and the draft's own resources.
func TestProposeFromDiagram_EndToEnd_ViaMirror(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()
	outPath := filepath.Join(dir, "draft.json")

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[providers]
"fake/widget" = "0.1.0"
`)

	// A neighboring stack's own ledger, referenced from the diagram as a
	// cross-stack node -- proves the "../<stack>" convention resolves.
	neighborDir := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-shared")
	if err := os.MkdirAll(neighborDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(neighborDir) })

	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, `
classes: {
  fake_widget: {}
  external: {}
}
shared_ref: "@shared.fake_widget.other-widget" {
  class: external
}
payments: {
  primary: main-widget {
    class: fake_widget
  }
}
payments.primary -> shared_ref
`)

	out, err := runUbx(t, []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}, "propose", "--from-diagram", diagramPath, "--stack", "payments",
		"--neighbor-ledger", "shared="+neighborDir,
		"--out", outPath)
	if err != nil {
		t.Fatalf("ubx propose --from-diagram: %v\noutput: %s", err, out)
	}

	// Ambiguity rendering: the edge into the reference node can't carry a
	// real $cross marker, so it must surface as a visible default note.
	if !strings.Contains(out, "AI defaults") {
		t.Fatalf("expected the $cross structural limitation rendered as a default note, got:\n%s", out)
	}
	if !strings.Contains(out, "wrote draft: "+outPath) {
		t.Fatalf("unexpected stdout: %s", out)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading draft: %v", err)
	}
	var draft resolver.IntentFile
	if err := json.Unmarshal(data, &draft); err != nil {
		t.Fatalf("unmarshal draft: %v", err)
	}

	if draft.Stack != "payments" {
		t.Fatalf("stack = %q, want payments", draft.Stack)
	}
	if len(draft.Resources) != 1 || draft.Resources[0].Name != "main-widget" || draft.Resources[0].Type != "fake_widget" {
		t.Fatalf("resources = %+v", draft.Resources)
	}
	if draft.Intent.Summary == "" {
		t.Fatalf("expected an auto-generated summary, got empty")
	}
	if len(draft.Intent.Defaults) != 1 {
		t.Fatalf("defaults = %+v, want 1 (the $cross structural limitation)", draft.Intent.Defaults)
	}

	// Sources provenance: exactly one "document" entry naming the diagram
	// file itself -- the single-entry precedent established across every
	// non-LLM medium (md's own --from-doc uses two: document + intent
	// provider; diagram has no adapter round trip, so just the one).
	if len(draft.Intent.Sources) != 1 {
		t.Fatalf("sources = %+v, want exactly 1", draft.Intent.Sources)
	}
	src := draft.Intent.Sources[0]
	if src.Kind != "document" || src.Ref != diagramPath || src.ContentHash == "" {
		t.Fatalf("source = %+v", src)
	}
}

func TestProposeFromDiagram_NoAmbiguity_RendersUnambiguousMessage(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[providers]
"fake/widget" = "0.1.0"
`)

	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, `
classes: {
  fake_widget: {}
}
payments: {
  primary: main-widget {
    class: fake_widget
  }
}
`)

	out, err := runUbx(t, []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}, "propose", "--from-diagram", diagramPath, "--stack", "payments", "--summary", "custom summary")
	if err != nil {
		t.Fatalf("ubx propose --from-diagram: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "no assumptions, defaults, or open questions -- the document was unambiguous.") {
		t.Fatalf("unexpected stdout: %s", out)
	}

	idx := strings.Index(out, "{")
	var draft resolver.IntentFile
	if err := json.Unmarshal([]byte(out[idx:]), &draft); err != nil {
		t.Fatalf("unmarshal draft from stdout: %v", err)
	}
	if draft.Intent.Summary != "custom summary" {
		t.Fatalf("summary = %q, want the --summary override", draft.Intent.Summary)
	}
}
