package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// blueprintCallSdkGoRoot resolves this repo's own sdk/go module root, the
// identical path blueprint/gogen_test.go's own (unexported, different-
// package) sdkGoModuleRoot resolves -- independently re-derived here since
// that helper isn't reachable across the package boundary, matching its
// own doc comment's stated precedent for doing exactly this.
func blueprintCallSdkGoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller: could not determine this test file's own path")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "sdk", "go"))
	if err != nil {
		t.Fatalf("resolve sdk/go path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("expected %s to be sdk/go's own module root (containing go.mod): %v", root, err)
	}
	return root
}

// blueprintCallIntent is a small, deterministic two-resource draft (no AI
// involved -- this test is about the CALLING convention, not codegen or
// drafting, both already covered by blueprint/gogen_test.go and
// cli/blueprint_test.go respectively): "primary" and "mirror" fake_widgets,
// mirror's own name/tags cross-referencing primary via real $ref markers --
// the identical shape TestResolveAcceptShip_CreateChain_RealFakeProvider
// (ship_change_test.go) already proves ships correctly hand-authored, now
// produced by GenerateGo instead. One required param (primary_name) and one
// params: default (tag_value, actually consumed in a resource's own Config,
// not just plumbed) -- proving Slice 2's own functional-options design is
// genuinely exercised through this path too, not just blueprint package's
// own unit tests.
const blueprintCallIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "platform",
  "intent": {"summary": "blueprint call fixture"},
  "resources": [
    {
      "type": "fake_widget",
      "name": "primary",
      "op": "create",
      "config": {"name": "{primary_name}", "tags": {"env": "{tag_value}"}}
    },
    {
      "type": "fake_widget",
      "name": "mirror",
      "op": "create",
      "config": {
        "name": {"$ref": {"to": "platform.fake_widget.primary.name"}},
        "tags": {"peer_id": {"$ref": {"to": "platform.fake_widget.primary.id"}}}
      }
    }
  ]
}`

func blueprintCallUbxfile() *blueprint.Ubxfile {
	return &blueprint.Ubxfile{
		Dir:  "testdata",
		Lang: "go",
		Params: []blueprint.Param{
			{Name: "primary_name", Type: blueprint.ParamString, Required: true},
			{Name: "tag_value", Type: blueprint.ParamString, Default: "prod"},
		},
	}
}

// writeBlueprintPackage runs the real, unmodified blueprint.GenerateGo
// (Slice 1) against blueprintCallIntent and writes its output to
// dir/name/go (Slice 4's own real sibling-per-language layout -- restored
// here, UBI-126: StampDirectCallProvenance needs a real Ubxfile at the Go
// module's own PARENT directory to find this fixture at all, so a flat
// layout with no Ubxfile can no longer stand in for a real built
// blueprint the way it could before this fix), with a local replace onto
// sdk/go so the package builds hermetically -- exactly blueprint/
// gogen_test.go's own TestGenerateGo_CompilesClean pattern, reused here
// because Task 3's own subject is what happens AFTER build, not build
// itself. A placeholder Ubxfile at dir/name (never parsed by anything in
// this call chain -- GenerateGo already ran against a hand-built
// *blueprint.Ubxfile, not this file) is written purely so
// StampDirectCallProvenance's own "is this on-disk directory a real
// blueprint" check (an Ubxfile's mere presence, matching every other
// blueprint-root detection in this codebase) finds it.
func writeBlueprintPackage(t *testing.T, dir, name string) string {
	t.Helper()
	var intent resolver.IntentFile
	if err := json.Unmarshal([]byte(blueprintCallIntent), &intent); err != nil {
		t.Fatalf("parse blueprintCallIntent: %v", err)
	}
	files, err := blueprint.GenerateGo(name, blueprintCallUbxfile(), &intent)
	if err != nil {
		t.Fatalf("blueprint.GenerateGo: %v", err)
	}

	root := filepath.Join(dir, name)
	pkgDir := filepath.Join(root, "go")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, blueprint.UbxfileName), []byte("lang: go\n\nparams:\n  primary_name: string, required\n\nresources: |\n  placeholder -- never re-drafted by a blueprint CALL, only by\n  `ubx blueprint build`, which this test never runs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sdkGoRoot := blueprintCallSdkGoRoot(t)
	for fname, content := range files {
		rel := strings.TrimPrefix(fname, "go/")
		if rel == "go.mod" {
			content += "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
		}
		if err := os.WriteFile(filepath.Join(pkgDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return pkgDir
}

// writeBlueprintCallingStack writes a plain SDK program -- sdk.Stack/
// sdk.Main, no blueprint machinery of its own -- that imports pkgDir (the
// locally-built blueprint package) via a real Go local-path replace
// directive and calls its function with real parameter values. This is
// UBI-74 Slice 2's own core claim made concrete: "a real stack imports
// Slice 1's built package directly... calls its blueprint function with
// real parameter values" (STATE.md handoff) -- not a fixture standing in
// for one.
func writeBlueprintCallingStack(t *testing.T, dir, pkgDir, callArgs string) string {
	t.Helper()
	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sdkGoRoot := blueprintCallSdkGoRoot(t)
	goMod := "module blueprint-call-fixture\n\ngo 1.23\n\n" +
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
		"\t\tsdk.Intent(sdk.IntentInfo{Summary: \"platform, via a called blueprint\"})\n" +
		"\t\tplatform.Platform(" + callArgs + ")\n" +
		"\t}))\n" +
		"}\n"
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

// TestBlueprintCall_ResolveAcceptShip_RealFakeProvider is UBI-74 Slice 2's
// own required hermetic proof: a real stack, importing a real locally-built
// blueprint package (blueprint.GenerateGo's own output, not a hand-written
// approximation of it), resolves via the SAME "ubx resolve --from-code" CLI
// path every other Go SDK program already uses -- completely unchanged --
// then accepts and ships against fakeprovider end to end, zero errors. The
// blueprint's two resources (primary + mirror, mirror's name/tags wired via
// real $ref markers to primary) must appear in the resolved proposal
// exactly as if hand-authored, and both must genuinely ship (mirror's own
// real ref substitution -- name and peer_id -- only succeeds if primary's
// real post-apply state was actually threaded through, the same executor
// path ship_change_test.go's own hand-authored equivalent already proves).
func TestBlueprintCall_ResolveAcceptShip_RealFakeProvider(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	pkgDir := writeBlueprintPackage(t, dir, "platform")
	entry := writeBlueprintCallingStack(t, dir, pkgDir, `"widget1"`)

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve",
		"--from-code", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
		"--timeout", "60s",
	)
	if err != nil {
		t.Fatalf("ubx resolve --from-code (blueprint call): %v\noutput: %s", err, resolveOut)
	}
	if !strings.Contains(resolveOut, "2 create(s), 0 change(s)") {
		t.Fatalf("expected a 2-create summary (primary+mirror), got: %s", resolveOut)
	}

	resolved, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	resolvedStr := string(resolved)
	// The blueprint's own required param (primary_name="widget1") and its
	// params: default (tag_value, never overridden -- must be "prod") must
	// both have reached the resolved proposal exactly as a hand-authored
	// stack's would -- proving the functional-options default is real, not
	// just compile-clean, through this real CLI path too.
	if !strings.Contains(resolvedStr, `"widget1"`) {
		t.Fatalf("resolved proposal missing the real primary_name param value: %s", resolvedStr)
	}
	if !strings.Contains(resolvedStr, `"prod"`) {
		t.Fatalf("resolved proposal missing the unoverridden tag_value default (\"prod\"): %s", resolvedStr)
	}
	if !strings.Contains(resolvedStr, `"fake_widget"`) || !strings.Contains(resolvedStr, `"mirror"`) {
		t.Fatalf("resolved proposal missing the blueprint's own fake_widget/mirror resource: %s", resolvedStr)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (blueprint call resolved): %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	shipOut, err := runUbx(t, env, "ship", changeID,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx ship (blueprint call): %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "platform.fake_widget.primary: shipped") {
		t.Fatalf("expected primary to show shipped, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "platform.fake_widget.mirror : shipped") {
		t.Fatalf("expected mirror to show shipped (real ref substitution against primary's own real post-apply state), got: %s", shipOut)
	}
}

// TestBlueprintCall_DirectSDKImport_HasBlueprintProvenance is UBI-126's
// own required success-bar proof: the exact same direct-SDK-import
// calling path TestBlueprintCall_ResolveAcceptShip_RealFakeProvider
// already proves ships correctly (Slice 2's own original mechanism) now
// ALSO carries the same {"kind":"blueprint","ref":"<name>:sha256:..."}
// per-resource provenance a diagram/md call already gets from
// blueprint.ExpandCalls (Slice 6) -- confirmed directly against the real
// resolved JSON, not inferred from `ubx why`/`ubx render`'s own
// downstream rendering (those are already proven to render whatever
// `sources` they're given correctly; this test's own job is confirming
// `sources` genuinely gets THERE for a direct-SDK call, which it never
// did before this fix).
func TestBlueprintCall_DirectSDKImport_HasBlueprintProvenance(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	pkgDir := writeBlueprintPackage(t, dir, "platform")
	entry := writeBlueprintCallingStack(t, dir, pkgDir, `"widget1"`)

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve",
		"--from-code", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
		"--timeout", "60s",
	)
	if err != nil {
		t.Fatalf("ubx resolve --from-code (blueprint call): %v\noutput: %s", err, resolveOut)
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
		if !ok || name != "platform" || !strings.HasPrefix(hash, "sha256:") {
			t.Fatalf("%s.%s: ref = %q, want \"platform:sha256:<hex>\": %s", c.Type, c.Name, c.Sources[0].Ref, raw)
		}
		refs = append(refs, c.Sources[0].Ref)
	}
	if refs[0] != refs[1] {
		t.Fatalf("primary and mirror got DIFFERENT blueprint refs from the SAME call: %q vs %q", refs[0], refs[1])
	}
}

// TestBlueprintCall_DirectSDKImport_WhyAndRenderShowProvenance is
// UBI-126's own end-to-end success-bar proof, one level past resolve:
// not just that the resolved JSON carries "sources", but that `ubx why`
// and `ubx render` -- run for real, against a real shipped ledger --
// now show full blueprint provenance for a direct-SDK-import call,
// matching what `cli/blueprint_provenance_test.go`/`render_blueprint_
// test.go` already proved for a diagram call (UBI-74 Slice 6). Same
// primary+mirror fixture, same real fakeprovider ship
// TestBlueprintCall_ResolveAcceptShip_RealFakeProvider already proves,
// carried one step further into the two commands a real user would
// actually run to inspect provenance.
func TestBlueprintCall_DirectSDKImport_WhyAndRenderShowProvenance(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	pkgDir := writeBlueprintPackage(t, dir, "platform")
	entry := writeBlueprintCallingStack(t, dir, pkgDir, `"widget1"`)

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	if _, err := runUbx(t, env, "resolve", "--from-code", entry, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath, "--timeout", "60s"); err != nil {
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
	if !strings.Contains(whyOut, "source: blueprint platform:sha256:") {
		t.Fatalf("ubx why on a direct-SDK-imported blueprint's own resource is missing provenance -- the UBI-126 gap, unfixed:\n%s", whyOut)
	}

	renderOut, err := runUbx(t, nil, "render", "--stack", "platform", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render: %v\noutput: %s", err, renderOut)
	}
	if !strings.Contains(renderOut, "style.stroke-dash: 3") {
		t.Fatalf("ubx render on a direct-SDK-imported blueprint's own resources is missing the dashed-border grouping -- the UBI-126 gap, unfixed:\n%s", renderOut)
	}
	if !strings.Contains(renderOut, `"primary"`) || !strings.Contains(renderOut, `"mirror"`) {
		t.Fatalf("ubx render missing one of the blueprint's own resources:\n%s", renderOut)
	}
}

// TestBlueprintCall_DefaultOverride_ResolveUnchanged proves the calling
// stack's own With<Param>(...) override (not just the bare default) also
// reaches "ubx resolve --from-code" correctly -- resolve-only, cheaper than
// a second full ship, since accept/ship's own handling of the resolved
// value doesn't depend on where that value came from.
func TestBlueprintCall_DefaultOverride_ResolveUnchanged(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	pkgDir := writeBlueprintPackage(t, dir, "platform")
	entry := writeBlueprintCallingStack(t, dir, pkgDir, `"widget1", platform.WithTagValue("staging")`)

	resolveOut, err := runUbx(t, env, "resolve",
		"--from-code", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--timeout", "60s",
	)
	if err != nil {
		t.Fatalf("ubx resolve --from-code (blueprint call, overridden default): %v\noutput: %s", err, resolveOut)
	}
	if !strings.Contains(resolveOut, "2 create(s), 0 change(s)") {
		t.Fatalf("expected a 2-create summary, got: %s", resolveOut)
	}
	if !strings.Contains(resolveOut, `"staging"`) {
		t.Fatalf("resolved proposal missing the overridden tag_value (\"staging\"): %s", resolveOut)
	}
	if strings.Contains(resolveOut, `"prod"`) {
		t.Fatalf("resolved proposal should not contain the un-overridden default \"prod\" once WithTagValue(\"staging\") is passed: %s", resolveOut)
	}
}
