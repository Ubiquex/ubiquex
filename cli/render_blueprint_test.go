package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// renderBlueprintGoOnlyUbxfile is blueprintCallUbxfile's own param shape
// (primary_name required, tag_value default "prod"), written as a real
// on-disk Ubxfile with lang: go ONLY -- deliberately narrower than
// blueprintCrossMediumUbxfileContent's own "lang: all" (which pulls in a
// ts/ package too, forcing ExpandCalls' callLanguagePreference to prefer
// deno). A single language is enough to prove Slice 6's own provenance-
// stamping-inside-ExpandCalls behavior; which language actually executed
// the call is already covered independently by blueprint/invoke_test.go's
// own TestExpandCalls_ProvenanceStamped(_Python).
const renderBlueprintGoOnlyUbxfile = `lang: go

params:
  primary_name: string, required
  tag_value: string, default "prod"

resources: |
  placeholder -- never re-drafted by a blueprint CALL, only by
  ` + "`ubx blueprint build`" + `, which this test never runs.
`

// writeRenderBlueprintPackage builds a real, on-disk, callable blueprint
// package for `ubx render`'s own Slice 6 proof: blueprintCallIntent's
// primary+mirror fake_widget fixture (the SAME shape Slice 2's direct-
// import test already ships against fakeprovider), generated via the
// REAL blueprint.GenerateGo, written into the multi-language sibling-dir
// layout (go/...) ExpandCalls' own invokeCall expects -- go-only, no ts/
// or py/ sibling, so ExpandCalls' callLanguagePreference falls through to
// go without needing deno or python3 installed.
func writeRenderBlueprintPackage(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, blueprint.UbxfileName), []byte(renderBlueprintGoOnlyUbxfile), 0o644); err != nil {
		t.Fatal(err)
	}

	var intent resolver.IntentFile
	if err := json.Unmarshal([]byte(blueprintCallIntent), &intent); err != nil {
		t.Fatalf("parse blueprintCallIntent: %v", err)
	}
	ubxfile := blueprintCallUbxfile()

	sdkGoRoot := blueprintCallSdkGoRoot(t)
	goFiles, err := blueprint.GenerateGo("platform", ubxfile, &intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	for name, content := range goFiles {
		if name == "go/go.mod" {
			content += "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
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

// TestRender_BlueprintCall_GroupsInDashedContainer_RealFakeProvider is
// UBI-74 Slice 6's own required hermetic proof for `ubx render`'s new
// grouping behavior (item 3): a real blueprint call (Slice 5's own
// BlueprintCalls mechanism, real ExpandCalls invocation, no synthetic
// shortcuts) against a real, locally built blueprint package resolves,
// accepts, and ships two real resources (primary + mirror) through the
// real fakeprovider pipeline -- then `ubx render --stack platform` must
// visually group BOTH inside one dashed-border container, labeled with
// the blueprint's own ref, with their real depends_on edge (mirror's own
// $ref to primary) drawn between the nested (container-qualified) node
// paths, never bare top-level ones.
//
// UBI-224 removed diagram/md, the two mediums that used to reach
// intent.blueprint_calls -- a hand-written intent/v1 file's
// blueprint_calls entry, always a supported input independent of any
// authoring medium, is what still reaches it.
func TestRender_BlueprintCall_GroupsInDashedContainer_RealFakeProvider(t *testing.T) {
	requireHermeticSandbox(t)
	pkgDir := writeRenderBlueprintPackage(t)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	args, err := json.Marshal(map[string]string{"primary_name": "widget1"})
	if err != nil {
		t.Fatal(err)
	}
	intentJSON := `{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": "platform",
	  "intent": {"summary": "platform, via a blueprint call"},
	  "resources": [],
	  "destroys": [],
	  "blueprint_calls": [{"name": "platform call", "blueprint": ` + jsonQuote(pkgDir) + `, "ref": "", "path": "", "args": ` + string(args) + `}]
	}`
	intentPath := filepath.Join(t.TempDir(), "intent.json")
	if err := os.WriteFile(intentPath, []byte(intentJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
	)
	if err != nil {
		t.Fatalf("ubx resolve (diagram blueprint call): %v\noutput: %s", err, resolveOut)
	}
	resolved, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resolved), `"kind": "blueprint"`) {
		t.Fatalf("resolved proposal missing the per-resource blueprint provenance (Slice 6): %s", resolved)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	shipOut, err := runUbx(t, env, "ship", changeID,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut)
	}

	renderOut, err := runUbx(t, nil, "render", "--stack", "platform", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render: %v\noutput: %s", err, renderOut)
	}

	if !strings.Contains(renderOut, "style.stroke-dash: 3") || !strings.Contains(renderOut, "style.fill: transparent") {
		t.Fatalf("expected a dashed-border blueprint container, got:\n%s", renderOut)
	}
	if !strings.Contains(renderOut, "platform:sha256:") {
		t.Fatalf("expected the container labeled with the blueprint's own ref, got:\n%s", renderOut)
	}
	if !strings.Contains(renderOut, `"primary"`) || !strings.Contains(renderOut, `"mirror"`) {
		t.Fatalf("expected both blueprint-created resources by name, got:\n%s", renderOut)
	}
	// Both resources came from the SAME blueprint call, so both nest under
	// the SAME single container key -- exactly one container, never two.
	if strings.Count(renderOut, "style.stroke-dash: 3") != 1 {
		t.Fatalf("expected exactly one blueprint container (both resources share one call), got:\n%s", renderOut)
	}
	// The depends_on edge (mirror -> primary, via mirror's own $ref) must
	// be drawn between the container-qualified node paths, not bare "r0
	// -> r1" -- proof fullKeyOf (not the bare keyOf) is what edges use.
	if !strings.Contains(renderOut, "bp0.") || !strings.Contains(renderOut, "->") {
		t.Fatalf("expected a depends_on edge between container-qualified (bp0.rN) node paths, got:\n%s", renderOut)
	}
}
