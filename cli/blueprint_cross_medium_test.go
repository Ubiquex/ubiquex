package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/diagram"
	"github.com/ubiquex/ubiquex/intentprovider"
)

// blueprintCrossMediumUbxfileContent is a real, on-disk Ubxfile matching
// blueprintCallUbxfile's own declared params (blueprint_call_test.go)
// exactly -- ExpandCalls re-parses the Ubxfile from disk at call time,
// it has no access to the in-memory *blueprint.Ubxfile GenerateGo/
// GenerateTS were built with.
const blueprintCrossMediumUbxfileContent = `lang: all

params:
  primary_name: string, required
  tag_value: string, default "prod"

resources: |
  placeholder -- never re-drafted by a blueprint CALL, only by
  ` + "`ubx blueprint build`" + `, which this test never runs.
`

// writeBlueprintCrossMediumPackage builds blueprintCallIntent (the SAME
// primary+mirror fixture UBI-74 Slice 2's own
// TestBlueprintCall_ResolveAcceptShip_RealFakeProvider already proves
// against real fakeprovider) via BOTH GenerateGo (for the SDK leg,
// reusing writeBlueprintCallingStack unchanged) and GenerateTS (for the
// diagram/md legs -- ExpandCalls prefers ts/ when present), writing a
// real Ubxfile alongside both, into one real on-disk blueprint package.
func writeBlueprintCrossMediumPackage(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, blueprint.UbxfileName), []byte(blueprintCrossMediumUbxfileContent), 0o644); err != nil {
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

// resolveCreates runs `ubx resolve <file> --provider fakeProviderBinary`
// (the real CLI command, real ExpandCalls splice included) against a
// fresh ledger and returns the resolved proposal's own Delta.Creates,
// normalized (sorted by type/name) for a stable, order-independent
// comparison.
func resolveCreates(t *testing.T, intentPath string) []json.RawMessage {
	t.Helper()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	resolvedPath := filepath.Join(t.TempDir(), "resolved.json")
	out, err := runUbx(t, env, "resolve", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath)
	if err != nil {
		t.Fatalf("ubx resolve %s: %v\noutput: %s", intentPath, err, out)
	}

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
	creates := doc.Delta.Creates
	sort.Slice(creates, func(i, j int) bool { return string(creates[i]) < string(creates[j]) })
	return creates
}

// normalizeCreateForComparison strips whatever a real resolved IR
// resource node carries that's expected to legitimately differ ACROSS
// the three call mechanisms for reasons that have nothing to do with
// blueprint-call correctness (Provider hints, if any) -- keeping only
// stack/type/name/config, the exact shape the ticket's own "identical
// delta shape" bar cares about.
func normalizeCreateForComparison(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var node struct {
		Stack  string          `json:"stack"`
		Type   string          `json:"type"`
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatalf("unmarshal create node: %v\nraw: %s", err, raw)
	}
	var cfg any
	if err := json.Unmarshal(node.Config, &cfg); err != nil {
		t.Fatalf("unmarshal create node config: %v\nraw: %s", err, raw)
	}
	normalized, err := json.Marshal(struct {
		Stack  string `json:"stack"`
		Type   string `json:"type"`
		Name   string `json:"name"`
		Config any    `json:"config"`
	}{node.Stack, node.Type, node.Name, cfg})
	if err != nil {
		t.Fatal(err)
	}
	return string(normalized)
}

func assertIdenticalCreates(t *testing.T, label string, want, got []json.RawMessage) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: got %d create(s), want %d", label, len(got), len(want))
	}
	wantNorm := make([]string, len(want))
	for i, r := range want {
		wantNorm[i] = normalizeCreateForComparison(t, r)
	}
	gotNorm := make([]string, len(got))
	for i, r := range got {
		gotNorm[i] = normalizeCreateForComparison(t, r)
	}
	sort.Strings(wantNorm)
	sort.Strings(gotNorm)
	for i := range wantNorm {
		if wantNorm[i] != gotNorm[i] {
			t.Fatalf("%s: create[%d] differs:\n  SDK:  %s\n  this: %s", label, i, wantNorm[i], gotNorm[i])
		}
	}
}

// TestBlueprintCrossMedium_SDKGoDiagramTSAndMDTS_IdenticalDelta is UBI-74
// Slice 5's own required success-bar proof: the SAME blueprint
// (blueprintCallIntent, the identical fixture Slice 2's own SDK-calling
// test already proves against real fakeprovider) called via THREE
// different mechanisms -- a hand-written Go SDK program (Slice 2's own
// proven path, unchanged), a real .d2 diagram's own ubx_blueprint node
// (diagram.Parse, real structural parsing), and a real md draft's own
// blueprint_calls recognition (intentprovider.DraftWithRetry, real
// validation) -- all resolve to the IDENTICAL delta shape (docs/
// blueprint.md's own "Cross-medium calling" section has the full
// account): same resources, same $ref-derived config values, same
// correct retention-analog values, proving "three different SOURCES,
// one resolved-delta mechanism" concretely, not just architecturally.
func TestBlueprintCrossMedium_SDKGoDiagramTSAndMDTS_IdenticalDelta(t *testing.T) {
	requireHermeticSandbox(t)
	requireDeno(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	pkgDir := writeBlueprintCrossMediumPackage(t)

	// --- SDK leg (Slice 2's own proven mechanism, unchanged) ---
	sdkDir := t.TempDir()
	sdkPkgDir := writeBlueprintPackage(t, sdkDir, "platform")
	sdkEntry := writeBlueprintCallingStack(t, sdkDir, sdkPkgDir, `"widget1"`)
	sdkLedgerDir := t.TempDir()
	sdkResolvedPath := filepath.Join(t.TempDir(), "sdk-resolved.json")
	sdkOut, err := runUbx(t, env, "resolve", "--from-code", sdkEntry, "--provider", fakeProviderBinary, "--ledger-dir", sdkLedgerDir, "--timeout", "60s", "--out", sdkResolvedPath)
	if err != nil {
		t.Fatalf("ubx resolve --from-code (SDK leg): %v\noutput: %s", err, sdkOut)
	}
	sdkRaw, err := os.ReadFile(sdkResolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	var sdkDoc struct {
		Delta struct {
			Creates []json.RawMessage `json:"creates"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(sdkRaw, &sdkDoc); err != nil {
		t.Fatalf("parse SDK resolved proposal: %v\nfile: %s", err, sdkRaw)
	}
	sdkCreates := sdkDoc.Delta.Creates
	sort.Slice(sdkCreates, func(i, j int) bool { return string(sdkCreates[i]) < string(sdkCreates[j]) })

	// --- Diagram leg: a real .d2 topology, real diagram.Parse, zero AI ---
	d2Src := `
platform: "platform call" {
  class: ubx_blueprint
  blueprint: ` + jsonQuote(pkgDir) + `
  primary_name: "widget1"
}
`
	diagramIntent, err := diagram.Parse("cross-medium.d2", strings.NewReader(d2Src), "platform", nil, diagram.Options{})
	if err != nil {
		t.Fatalf("diagram.Parse: %v", err)
	}
	if len(diagramIntent.BlueprintCalls) != 1 {
		t.Fatalf("expected exactly 1 blueprint call from the diagram, got %d", len(diagramIntent.BlueprintCalls))
	}
	diagramIntent.Intent.Summary = "platform, via a diagram blueprint call"
	diagramPath := filepath.Join(t.TempDir(), "diagram-draft.json")
	writeResolverIntentFile(t, diagramPath, diagramIntent)
	diagramCreates := resolveCreates(t, diagramPath)

	// --- md leg: a real fake-adapter draft, real intentprovider validation ---
	mdArgs, err := json.Marshal(map[string]string{"primary_name": "widget1"})
	if err != nil {
		t.Fatal(err)
	}
	mdDraftJSON := `{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": "platform",
	  "intent": {"summary": "platform, via an md blueprint call", "assumptions": [], "defaults": [], "questions": []},
	  "resources": [],
	  "destroys": [],
	  "blueprint_calls": [{"name": "platform call", "blueprint": ` + jsonQuote(pkgDir) + `, "ref": "", "path": "", "args": ` + jsonQuote(string(mdArgs)) + `}]
	}`
	fake := &fakeIntentAdapter{draft: mdDraftJSON}
	mdIntent, _, err := intentprovider.DraftWithRetry(context.Background(), fake, "platform", []byte("Use blueprint platform with: primary_name = widget1"), nil)
	if err != nil {
		t.Fatalf("intentprovider.DraftWithRetry: %v", err)
	}
	if len(mdIntent.BlueprintCalls) != 1 {
		t.Fatalf("expected exactly 1 blueprint call from the md draft, got %d", len(mdIntent.BlueprintCalls))
	}
	mdPath := filepath.Join(t.TempDir(), "md-draft.json")
	writeResolverIntentFile(t, mdPath, mdIntent)
	mdCreates := resolveCreates(t, mdPath)

	assertIdenticalCreates(t, "diagram vs SDK", sdkCreates, diagramCreates)
	assertIdenticalCreates(t, "md vs SDK", sdkCreates, mdCreates)
}

// blueprintSourcesForComparison extracts each create's own "sources"
// entries (stack/type/name -> the raw sources array, re-marshaled for a
// stable, order-independent string comparison) -- normalizeCreateForComparison's
// own sibling, deliberately separate: that function EXCLUDES sources
// entirely (by design, since it predates UBI-126 and the SDK/diagram/md
// delta-shape comparison never needed to know about provenance), this
// one is ONLY about sources.
func blueprintSourcesForComparison(t *testing.T, creates []json.RawMessage) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, raw := range creates {
		var node struct {
			Stack   string          `json:"stack"`
			Type    string          `json:"type"`
			Name    string          `json:"name"`
			Sources json.RawMessage `json:"sources"`
		}
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatalf("unmarshal create node: %v\nraw: %s", err, raw)
		}
		var sources any
		if len(node.Sources) > 0 {
			if err := json.Unmarshal(node.Sources, &sources); err != nil {
				t.Fatalf("unmarshal sources: %v\nraw: %s", err, raw)
			}
		}
		normalized, err := json.Marshal(sources)
		if err != nil {
			t.Fatal(err)
		}
		out[node.Stack+"."+node.Type+"."+node.Name] = string(normalized)
	}
	return out
}

// TestBlueprintCrossMedium_ProvenanceIdentical is UBI-126's own required
// hermetic proof, extending TestBlueprintCrossMedium_
// SDKGoDiagramTSAndMDTS_IdenticalDelta's own "three mediums, one
// resolved delta" bar to provenance too: the SAME blueprint (one shared
// on-disk pkgDir, unlike the sibling test above -- which deliberately
// builds a SEPARATE copy for its own SDK leg, since a direct comparison
// there only ever needed matching DELTA shape, never a matching content
// hash) called via all three mediums must produce BYTE-IDENTICAL
// "sources" -- the exact same {"kind":"blueprint","ref":"platform:
// sha256:..."} entry, not merely the same SHAPE. This is the real
// regression proof for UBI-126's own core claim: before this fix, the
// SDK leg's own resolved creates carried no "sources" key at all, while
// diagram/md's did -- after it, all three are indistinguishable.
func TestBlueprintCrossMedium_ProvenanceIdentical(t *testing.T) {
	requireHermeticSandbox(t)
	requireDeno(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	pkgDir := writeBlueprintCrossMediumPackage(t)

	// --- SDK leg: a direct import of the SAME shared pkgDir (not a
	// separately-built copy) -- the only way a byte-identical content
	// hash across all three legs is even possible. ---
	sdkDir := t.TempDir()
	sdkEntry := writeBlueprintCallingStack(t, sdkDir, filepath.Join(pkgDir, "go"), `"widget1"`)
	sdkLedgerDir := t.TempDir()
	sdkResolvedPath := filepath.Join(t.TempDir(), "sdk-resolved.json")
	sdkOut, err := runUbx(t, env, "resolve", "--from-code", sdkEntry, "--provider", fakeProviderBinary, "--ledger-dir", sdkLedgerDir, "--timeout", "60s", "--out", sdkResolvedPath)
	if err != nil {
		t.Fatalf("ubx resolve --from-code (SDK leg): %v\noutput: %s", err, sdkOut)
	}
	sdkRaw, err := os.ReadFile(sdkResolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	var sdkDoc struct {
		Delta struct {
			Creates []json.RawMessage `json:"creates"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(sdkRaw, &sdkDoc); err != nil {
		t.Fatalf("parse SDK resolved proposal: %v\nfile: %s", err, sdkRaw)
	}
	sdkSources := blueprintSourcesForComparison(t, sdkDoc.Delta.Creates)

	// --- Diagram leg ---
	d2Src := `
platform: "platform call" {
  class: ubx_blueprint
  blueprint: ` + jsonQuote(pkgDir) + `
  primary_name: "widget1"
}
`
	diagramIntent, err := diagram.Parse("cross-medium-provenance.d2", strings.NewReader(d2Src), "platform", nil, diagram.Options{})
	if err != nil {
		t.Fatalf("diagram.Parse: %v", err)
	}
	diagramIntent.Intent.Summary = "platform, via a diagram blueprint call"
	diagramPath := filepath.Join(t.TempDir(), "diagram-draft.json")
	writeResolverIntentFile(t, diagramPath, diagramIntent)
	diagramSources := blueprintSourcesForComparison(t, resolveCreates(t, diagramPath))

	// --- md leg ---
	mdArgs, err := json.Marshal(map[string]string{"primary_name": "widget1"})
	if err != nil {
		t.Fatal(err)
	}
	mdDraftJSON := `{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": "platform",
	  "intent": {"summary": "platform, via an md blueprint call", "assumptions": [], "defaults": [], "questions": []},
	  "resources": [],
	  "destroys": [],
	  "blueprint_calls": [{"name": "platform call", "blueprint": ` + jsonQuote(pkgDir) + `, "ref": "", "path": "", "args": ` + jsonQuote(string(mdArgs)) + `}]
	}`
	fake := &fakeIntentAdapter{draft: mdDraftJSON}
	mdIntent, _, err := intentprovider.DraftWithRetry(context.Background(), fake, "platform", []byte("Use blueprint platform with: primary_name = widget1"), nil)
	if err != nil {
		t.Fatalf("intentprovider.DraftWithRetry: %v", err)
	}
	mdPath := filepath.Join(t.TempDir(), "md-draft.json")
	writeResolverIntentFile(t, mdPath, mdIntent)
	mdSources := blueprintSourcesForComparison(t, resolveCreates(t, mdPath))

	if len(sdkSources) == 0 {
		t.Fatal("SDK leg produced zero resources with sources -- fixture problem, not a real comparison")
	}
	for addr, want := range sdkSources {
		if !strings.Contains(want, `"kind":"blueprint"`) {
			t.Fatalf("%s: SDK leg's own source is missing blueprint provenance entirely: %s", addr, want)
		}
		if got := diagramSources[addr]; got != want {
			t.Fatalf("%s: diagram sources differ from SDK:\n  SDK:     %s\n  diagram: %s", addr, want, got)
		}
		if got := mdSources[addr]; got != want {
			t.Fatalf("%s: md sources differ from SDK:\n  SDK: %s\n  md:  %s", addr, want, got)
		}
	}
}

// jsonQuote quotes s as a JSON string literal suitable for splicing
// directly into a D2/JSON source snippet built via string concatenation
// in this file (a bare JSON-quoted string is also valid D2 quoted-string
// syntax).
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func writeResolverIntentFile(t *testing.T, path string, intent any) {
	t.Helper()
	b, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
