package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/diagram"
)

// UBI-149: a real, end-to-end `ubx plan` regression for the
// writeTSCaller/writeGoCaller argument-ordering bug (blueprint/
// invoke.go), reusing blueprint_crossref_test.go's own real fixtures
// (crossRefStoreFixture/seedCrossRefNeighborNetworkingStack/
// crossRefResolvedTagsVpcID) as its base, per this ticket's own
// suggestion -- the exact real shape UBI-147's own session first found
// broken: a required param (vpc_id, cross_ref) declared AFTER a
// defaulted one (retention_days) in the Ubxfile's own params: block.

// argOrderBlueprintUbxfileContent mirrors crossRefBlueprintUbxfileContent
// exactly, with two more params inserted -- queue_name (required,
// first) and retention_days (defaulted, BETWEEN queue_name and vpc_id)
// -- so vpc_id, the required param, sits after a defaulted one, the
// exact interleaving that broke both the TS and the Go synthesized
// callers.
const argOrderBlueprintUbxfileContent = "lang: all\n\nparams:\n  queue_name: string, required\n  retention_days: number, default 14\n  vpc_id: cross_ref, required\n\nresources: |\n  placeholder -- never re-drafted by a blueprint CALL, only by\n  `ubx blueprint build`, which this test never runs.\n"

func argOrderBlueprintUbxfile() *blueprint.Ubxfile {
	return &blueprint.Ubxfile{
		Dir:  "testdata",
		Lang: "all",
		Params: []blueprint.Param{
			{Name: "queue_name", Type: blueprint.ParamString, Required: true},
			{Name: "retention_days", Type: blueprint.ParamNumber, Default: 14},
			{Name: "vpc_id", Type: blueprint.ParamCrossRef, Required: true},
		},
	}
}

// argOrderBlueprintIntent mirrors crossRefBlueprintIntent's own shape
// (one fake_widget resource, fakeprovider's own real schema only has
// id/name/tags), extended so every one of the three params lands
// somewhere real and checkable: name carries queue_name, tags carries
// BOTH retention_days and vpc_id -- a positional mixup between any two
// of the three shows up directly in the resolved output.
const argOrderBlueprintIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "platform",
  "intent": {"summary": "arg-order blueprint call fixture"},
  "resources": [
    {
      "type": "fake_widget",
      "name": "primary",
      "op": "create",
      "config": {"name": "{queue_name}", "tags": {"retention_days": "{retention_days}", "vpc_id": "{vpc_id}"}}
    }
  ]
}`

// writeArgOrderBlueprintPackage mirrors writeCrossRefBlueprintPackage
// exactly (same dual GenerateGo/GenerateTS build, same on-disk layout),
// built from argOrderBlueprintUbxfile/argOrderBlueprintIntent instead.
func writeArgOrderBlueprintPackage(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, blueprint.UbxfileName), []byte(argOrderBlueprintUbxfileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var intent resolver.IntentFile
	if err := json.Unmarshal([]byte(argOrderBlueprintIntent), &intent); err != nil {
		t.Fatalf("parse argOrderBlueprintIntent: %v", err)
	}
	ubxfile := argOrderBlueprintUbxfile()

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

	tsFiles, err := blueprint.GenerateTS("platform", ubxfile, &intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}
	for name, content := range tsFiles {
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

// argOrderResolvedNameAndTags extracts the resolved proposal's own
// single create's name + tags -- enough to prove every one of the three
// params landed in its own correct slot. tags is map[string]any, not
// map[string]string: retention_days is a real ParamNumber, so it
// resolves as a genuine JSON number, only vpc_id (a cross_ref's own
// resolved id) is a real string.
func argOrderResolvedNameAndTags(t *testing.T, resolvedPath string) (name string, tags map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Delta struct {
			Creates []json.RawMessage `json:"creates"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse resolved proposal: %v\nfile: %s", err, raw)
	}
	if len(doc.Delta.Creates) != 1 {
		t.Fatalf("expected exactly 1 create, got %d: %s", len(doc.Delta.Creates), raw)
	}
	var node struct {
		Config struct {
			Name string         `json:"name"`
			Tags map[string]any `json:"tags"`
		} `json:"config"`
	}
	if err := json.Unmarshal(doc.Delta.Creates[0], &node); err != nil {
		t.Fatalf("parse create node: %v\nraw: %s", err, doc.Delta.Creates[0])
	}
	return node.Config.Name, node.Config.Tags
}

// TestBlueprintCall_ArgOrder_Diagram_RequiredAfterDefaulted_CorrectSlots
// is UBI-149's own primary regression: a real diagram-medium call
// (writeTSCaller's own synthesized caller, the diagram/md default-
// preferred language) with vpc_id (required) declared after
// retention_days (defaulted), resolved through a real `ubx plan` run.
// Before the fix, retention_days's own value silently landed in
// vpc_id's own slot (a real, reproduced symptom: tags.vpc_id becoming
// "3" instead of the real neighbor id).
func TestBlueprintCall_ArgOrder_Diagram_RequiredAfterDefaulted_CorrectSlots(t *testing.T) {
	requireHermeticSandbox(t)
	requireDeno(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	storeName, shared := crossRefStoreFixture(t)
	remoteConfigDir(t, storeName)
	seedCrossRefNeighborNetworkingStack(t, shared)

	pkgDir := writeArgOrderBlueprintPackage(t)

	d2Src := `
platform: "platform call" {
  class: ubx_blueprint
  blueprint: ` + jsonQuote(pkgDir) + `
  queue_name: "pipeline-events"
  retention_days: "3"
  vpc_id: "@networking.aws_vpc.main.id"
}
`
	diagramIntent, err := diagram.Parse("argorder.d2", strings.NewReader(d2Src), "payments", nil, diagram.Options{})
	if err != nil {
		t.Fatalf("diagram.Parse: %v", err)
	}
	diagramIntent.Intent.Summary = "payments, via a diagram arg-order blueprint call"
	diagramPath := filepath.Join(t.TempDir(), "diagram-draft.json")
	writeResolverIntentFile(t, diagramPath, diagramIntent)

	ledgerDir := t.TempDir()
	resolvedPath := filepath.Join(t.TempDir(), "resolved.json")
	out, err := runUbx(t, env, "plan", diagramPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan (diagram leg): %v\noutput: %s", err, out)
	}

	name, tags := argOrderResolvedNameAndTags(t, resolvedPath)
	if name != "pipeline-events" {
		t.Fatalf("resolved name = %q, want \"pipeline-events\" -- queue_name landed in the wrong slot", name)
	}
	if tags["retention_days"] != float64(3) {
		t.Fatalf("resolved tags.retention_days = %v, want 3 -- retention_days landed in the wrong slot", tags["retention_days"])
	}
	if tags["vpc_id"] != "vpc-123" {
		t.Fatalf("resolved tags.vpc_id = %v, want the real neighbor value \"vpc-123\" (this is the exact bug UBI-149 fixes -- retention_days's own value used to land here instead)", tags["vpc_id"])
	}
}

// TestBlueprintCall_ArgOrder_MD_RequiredAfterDefaulted_CorrectSlots is
// the identical regression for the md medium.
func TestBlueprintCall_ArgOrder_MD_RequiredAfterDefaulted_CorrectSlots(t *testing.T) {
	requireHermeticSandbox(t)
	requireDeno(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	storeName, shared := crossRefStoreFixture(t)
	remoteConfigDir(t, storeName)
	seedCrossRefNeighborNetworkingStack(t, shared)

	pkgDir := writeArgOrderBlueprintPackage(t)

	mdArgs, err := json.Marshal(map[string]string{
		"queue_name":     "pipeline-events",
		"retention_days": "3",
		"vpc_id":         "@networking.aws_vpc.main.id",
	})
	if err != nil {
		t.Fatal(err)
	}
	mdDraftJSON := `{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": "payments",
	  "intent": {"summary": "payments, via an md arg-order blueprint call", "assumptions": [], "defaults": [], "questions": []},
	  "resources": [],
	  "destroys": [],
	  "blueprint_calls": [{"name": "platform call", "blueprint": ` + jsonQuote(pkgDir) + `, "ref": "", "path": "", "args": ` + jsonQuote(string(mdArgs)) + `}]
	}`
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: mdDraftJSON})

	docPath := filepath.Join(t.TempDir(), "platform.md")
	writeFile(t, docPath, "Use blueprint platform with:\n  queue_name = \"pipeline-events\"\n  retention_days = 3\n  vpc_id = @networking.aws_vpc.main.id")

	ledgerDir := t.TempDir()
	resolvedPath := filepath.Join(t.TempDir(), "resolved.json")
	out, err := runUbx(t, env, "plan", "--from-doc", docPath, "--stack", "payments", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan --from-doc (md leg): %v\noutput: %s", err, out)
	}

	name, tags := argOrderResolvedNameAndTags(t, resolvedPath)
	if name != "pipeline-events" {
		t.Fatalf("resolved name = %q, want \"pipeline-events\" -- queue_name landed in the wrong slot", name)
	}
	if tags["retention_days"] != float64(3) {
		t.Fatalf("resolved tags.retention_days = %v, want 3 -- retention_days landed in the wrong slot", tags["retention_days"])
	}
	if tags["vpc_id"] != "vpc-123" {
		t.Fatalf("resolved tags.vpc_id = %v, want the real neighbor value \"vpc-123\"", tags["vpc_id"])
	}
}
