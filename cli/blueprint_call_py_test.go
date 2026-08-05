package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// blueprintCallUbxfilePy mirrors blueprintCallUbxfile (blueprint_call_test.go)
// exactly, Lang: "py" instead of "go" -- the same blueprintCallIntent
// fixture (language-independent: plain resource declarations) is reused
// unchanged.
func blueprintCallUbxfilePy() *blueprint.Ubxfile {
	return &blueprint.Ubxfile{
		Dir:  "testdata",
		Lang: "py",
		Params: []blueprint.Param{
			{Name: "primary_name", Type: blueprint.ParamString, Required: true},
			{Name: "tag_value", Type: blueprint.ParamString, Default: "prod"},
		},
	}
}

// writeBlueprintPackagePy is writeBlueprintPackage's own Python sibling,
// with one real, structural difference from Go/TS named explicitly (not
// glossed over): pyeval's own WASI sandbox mounts ONLY the entry script's
// own directory tree by default, so a bare on-disk py/ package elsewhere
// on disk (the Go/TS pattern, referenced by a real relative/absolute
// import path) is NOT reachable at all -- the ONLY mechanism that makes a
// Python blueprint dependency importable AND gives UBI-126 a real content
// hash to complete its provenance with is UBI-130's own requirements.txt
// "<name> @ <url>" declaration (blueprint.ResolvePyDependencies), which
// this fixture uses instead of a bare colocated copy. blueprint.Package
// (below) writes a real blueprint.lock.json, exactly what a real
// `ubx blueprint package` run would produce, so Verify (run when
// requirements.txt's own local-file dependency is resolved) has something
// real to check.
func writeBlueprintPackagePy(t *testing.T, dir, name string) string {
	t.Helper()
	var intent resolver.IntentFile
	if err := json.Unmarshal([]byte(blueprintCallIntent), &intent); err != nil {
		t.Fatalf("parse blueprintCallIntent: %v", err)
	}
	files, err := blueprint.GeneratePython(name, blueprintCallUbxfilePy(), &intent)
	if err != nil {
		t.Fatalf("blueprint.GeneratePython: %v", err)
	}

	root := filepath.Join(dir, name)
	pyDir := filepath.Join(root, "py")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, blueprint.UbxfileName), []byte("lang: py\n\nparams:\n  primary_name: string, required\n\nresources: |\n  placeholder -- never re-drafted by a blueprint CALL, only by\n  `ubx blueprint build`, which this test never runs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for fname, content := range files {
		rel := strings.TrimPrefix(fname, "py/")
		if err := os.WriteFile(filepath.Join(pyDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := blueprint.Package(root, filepath.Join(t.TempDir(), "out.tar.gz")); err != nil {
		t.Fatalf("blueprint.Package: %v", err)
	}
	return root
}

// writeBlueprintCallingStackPy is writeBlueprintCallingStack's own Python
// sibling: a plain SDK program -- sdk.run/no blueprint machinery of its
// own -- declaring bpDir as a real requirements.txt "<name> @ <url>"
// dependency (a bare local path, UBI-130's own "file://"-equivalent
// scheme) and calling its function with real parameter values, matching
// UBI-74 Slice 2's own core claim for the one calling convention UBI-126
// is specifically about. Named "widgetplatform" rather than "platform"
// deliberately: Python's own real stdlib has a module literally named
// `platform`, and this fixture's own generated module/function names
// derive directly from the blueprint's own directory name (packageIdent/
// pythonIdentifier) -- avoiding the collision outright is simpler than
// relying on WASI's own PYTHONPATH ordering to shadow it correctly.
func writeBlueprintCallingStackPy(t *testing.T, dir, bpDir, callArgs string) string {
	t.Helper()
	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stackDir, "requirements.txt"), []byte(fmt.Sprintf("widgetplatform @ %s\n", bpDir)), 0o644); err != nil {
		t.Fatal(err)
	}

	src := fmt.Sprintf(`import ubx_sdk as sdk
from widgetplatform import widgetplatform


def describe():
    sdk.intent("platform, via a called blueprint")
    widgetplatform(%s)


if __name__ == "__main__":
    sdk.run("platform", describe)
`, callArgs)
	entry := filepath.Join(stackDir, "create_platform.py")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

// TestBlueprintCall_DirectSDKImport_HasBlueprintProvenance_Py mirrors
// TestBlueprintCall_DirectSDKImport_HasBlueprintProvenance (Go) exactly --
// UBI-126's own required success-bar proof, ported: the exact same
// direct-SDK-import calling path Slice 2 already proved ships correctly
// now ALSO carries the same {"kind":"blueprint","ref":"<name>:sha256:..."}
// per-resource provenance a diagram/md call already gets, for Python --
// via UBI-130's real requirements.txt dependency resolution, the only
// mechanism that gives Python's own WASI sandbox both an importable
// dependency AND a real content hash to complete provenance with (see
// writeBlueprintPackagePy's own doc comment).
func TestBlueprintCall_DirectSDKImport_HasBlueprintProvenance_Py(t *testing.T) {
	requireWasmtime(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	bpDir := writeBlueprintPackagePy(t, dir, "widgetplatform")
	entry := writeBlueprintCallingStackPy(t, dir, bpDir, `"widget1"`)

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve",
		"--from-code", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
		"--timeout", "120s",
	)
	if err != nil {
		t.Fatalf("ubx resolve --from-code (Python blueprint call): %v\noutput: %s", err, resolveOut)
	}
	if !strings.Contains(resolveOut, "pulled widgetplatform @") || !strings.Contains(resolveOut, "verified") {
		t.Fatalf("expected a real UBI-130 pull+verify receipt line, got: %s", resolveOut)
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
	if len(doc.Delta.Creates) != 2 {
		t.Fatalf("expected 2 creates (primary+mirror), got %d: %s", len(doc.Delta.Creates), raw)
	}

	var refs []string
	for _, c := range doc.Delta.Creates {
		if len(c.Sources) != 1 {
			t.Fatalf("%s.%s: expected exactly 1 source, got %d: %s", c.Type, c.Name, len(c.Sources), raw)
		}
		if c.Sources[0].Kind != "blueprint" {
			t.Fatalf("%s.%s: source kind = %q, want \"blueprint\": %s", c.Type, c.Name, c.Sources[0].Kind, raw)
		}
		name, hash, ok := strings.Cut(c.Sources[0].Ref, ":")
		if !ok || name != "widgetplatform" || !strings.HasPrefix(hash, "sha256:") {
			t.Fatalf("%s.%s: ref = %q, want \"widgetplatform:sha256:<hex>\": %s", c.Type, c.Name, c.Sources[0].Ref, raw)
		}
		refs = append(refs, c.Sources[0].Ref)
	}
	if refs[0] != refs[1] {
		t.Fatalf("primary and mirror got DIFFERENT blueprint refs from the SAME call: %q vs %q", refs[0], refs[1])
	}
}

// TestBlueprintCall_DirectSDKImport_WhyAndRenderShowProvenance_Py mirrors
// TestBlueprintCall_DirectSDKImport_WhyAndRenderShowProvenance (Go)
// exactly, one level past resolve: `ubx why`/`ubx render`, run for real
// against a real shipped ledger, show full blueprint provenance for a
// Python direct-SDK-import call.
func TestBlueprintCall_DirectSDKImport_WhyAndRenderShowProvenance_Py(t *testing.T) {
	requireWasmtime(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	bpDir := writeBlueprintPackagePy(t, dir, "widgetplatform")
	entry := writeBlueprintCallingStackPy(t, dir, bpDir, `"widget1"`)

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	if _, err := runUbx(t, env, "resolve", "--from-code", entry, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath, "--timeout", "120s"); err != nil {
		t.Fatalf("ubx resolve --from-code: %v", err)
	}
	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)
	if _, err := runUbx(t, env, "ship", changeID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx ship: %v", err)
	}

	whyOut, err := runUbx(t, nil, "why", "platform.fake_widget.primary", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "source: blueprint widgetplatform:sha256:") {
		t.Fatalf("ubx why on a Python direct-SDK-imported blueprint's own resource is missing provenance:\n%s", whyOut)
	}

	renderOut, err := runUbx(t, nil, "render", "--stack", "platform", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render: %v\noutput: %s", err, renderOut)
	}
	if !strings.Contains(renderOut, "style.stroke-dash: 3") {
		t.Fatalf("ubx render on a Python direct-SDK-imported blueprint's own resources is missing the dashed-border grouping:\n%s", renderOut)
	}
	if !strings.Contains(renderOut, `"primary"`) || !strings.Contains(renderOut, `"mirror"`) {
		t.Fatalf("ubx render missing one of the blueprint's own resources:\n%s", renderOut)
	}
}
