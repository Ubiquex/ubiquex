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

// blueprintCallUbxfileTS mirrors blueprintCallUbxfile (blueprint_call_test.go)
// exactly, Lang: "ts" instead of "go" -- the same blueprintCallIntent fixture
// (language-independent: plain resource declarations) is reused unchanged.
func blueprintCallUbxfileTS() *blueprint.Ubxfile {
	return &blueprint.Ubxfile{
		Dir:  "testdata",
		Lang: "ts",
		Params: []blueprint.Param{
			{Name: "primary_name", Type: blueprint.ParamString, Required: true},
			{Name: "tag_value", Type: blueprint.ParamString, Default: "prod"},
		},
	}
}

// writeBlueprintPackageTS is writeBlueprintPackage's own TS sibling: runs
// the real, unmodified blueprint.GenerateTS against blueprintCallIntent and
// writes its output to dir/name/ts/ (Slice 4's own real sibling-per-language
// layout -- UBI-126's own StampDirectCallProvenanceTS needs a real Ubxfile
// at the ts/ directory's own PARENT to find this fixture at all, mirroring
// writeBlueprintPackage's own identical requirement for Go). Unlike Go, no
// module manifest / local replace directive is needed at all: TS resolves
// "@ubx/sdk" entirely through tseval's own embedded deno.json, regardless
// of which directory the blueprint's own generated code lives in -- a real,
// structural simplification over Go's own test, not an oversight.
func writeBlueprintPackageTS(t *testing.T, dir, name string) string {
	t.Helper()
	var intent resolver.IntentFile
	if err := json.Unmarshal([]byte(blueprintCallIntent), &intent); err != nil {
		t.Fatalf("parse blueprintCallIntent: %v", err)
	}
	files, err := blueprint.GenerateTS(name, blueprintCallUbxfileTS(), &intent)
	if err != nil {
		t.Fatalf("blueprint.GenerateTS: %v", err)
	}

	root := filepath.Join(dir, name)
	tsDir := filepath.Join(root, "ts")
	if err := os.MkdirAll(tsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A placeholder Ubxfile (never parsed by anything in this call chain --
	// GenerateTS already ran against a hand-built *blueprint.Ubxfile, not
	// this file) written purely so StampDirectCallProvenanceTS's own "is
	// this on-disk directory a real blueprint" check (an Ubxfile's mere
	// presence) finds it -- mirrors writeBlueprintPackage's own identical
	// placeholder exactly.
	if err := os.WriteFile(filepath.Join(root, blueprint.UbxfileName), []byte("lang: ts\n\nparams:\n  primary_name: string, required\n\nresources: |\n  placeholder -- never re-drafted by a blueprint CALL, only by\n  `ubx blueprint build`, which this test never runs.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for fname, content := range files {
		rel := strings.TrimPrefix(fname, "ts/")
		if err := os.WriteFile(filepath.Join(tsDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tsDir
}

// writeBlueprintCallingStackTS is writeBlueprintCallingStack's own TS
// sibling: a plain SDK program -- stack()/no blueprint machinery of its
// own -- that imports tsDir's own built platform.ts function via its real
// absolute path (Deno resolves a literal, static import specifier -- a
// same-dir, "../sibling," or absolute path, all confirmed under full
// --deny-read by tseval/runner.go's own doc comment -- with no import map
// or module-manifest needed at all, unlike Go's go.mod-based resolution)
// and calls it with real parameter values -- the TS mirror of UBI-74
// Slice 2's own core claim, for the calling convention UBI-126 is
// specifically about.
func writeBlueprintCallingStackTS(t *testing.T, dir, tsDir, callArgs string) string {
	t.Helper()
	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	platformFile, err := filepath.Abs(filepath.Join(tsDir, "platform.ts"))
	if err != nil {
		t.Fatal(err)
	}

	src := fmt.Sprintf(`import { intent, stack } from "@ubx/sdk";
import { platform } from %s;

export default stack("platform", () => {
  intent({ summary: "platform, via a called blueprint" });
  platform(%s);
});
`, jsonStringLiteralForTest(platformFile), callArgs)

	entry := filepath.Join(stackDir, "create_platform.ts")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

// jsonStringLiteralForTest quotes s as a JSON/TS string literal --
// filepath.Abs output on a real machine can contain characters (spaces,
// unicode) `%q`-equivalent quoting must escape correctly; reusing
// encoding/json's own real escaping rather than hand-rolling it here.
func jsonStringLiteralForTest(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err) // a Go string is always JSON-marshalable
	}
	return string(b)
}

// TestBlueprintCall_DirectSDKImport_HasBlueprintProvenance_TS mirrors
// TestBlueprintCall_DirectSDKImport_HasBlueprintProvenance (Go) exactly --
// UBI-126's own required success-bar proof, ported: the exact same
// direct-SDK-import calling path Slice 2 already proved ships correctly
// now ALSO carries the same {"kind":"blueprint","ref":"<name>:sha256:..."}
// per-resource provenance a diagram/md call already gets, for TypeScript.
func TestBlueprintCall_DirectSDKImport_HasBlueprintProvenance_TS(t *testing.T) {
	requireDeno(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	tsDir := writeBlueprintPackageTS(t, dir, "platform")
	entry := writeBlueprintCallingStackTS(t, dir, tsDir, `"widget1"`)

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve",
		"--from-code", entry,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
		"--timeout", "60s",
	)
	if err != nil {
		t.Fatalf("ubx resolve --from-code (TS blueprint call): %v\noutput: %s", err, resolveOut)
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

// TestBlueprintCall_DirectSDKImport_WhyAndRenderShowProvenance_TS mirrors
// TestBlueprintCall_DirectSDKImport_WhyAndRenderShowProvenance (Go)
// exactly, one level past resolve: `ubx why`/`ubx render`, run for real
// against a real shipped ledger, show full blueprint provenance for a
// TypeScript direct-SDK-import call.
func TestBlueprintCall_DirectSDKImport_WhyAndRenderShowProvenance_TS(t *testing.T) {
	requireDeno(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	tsDir := writeBlueprintPackageTS(t, dir, "platform")
	entry := writeBlueprintCallingStackTS(t, dir, tsDir, `"widget1"`)

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
		t.Fatalf("ubx why on a TS direct-SDK-imported blueprint's own resource is missing provenance:\n%s", whyOut)
	}

	renderOut, err := runUbx(t, nil, "render", "--stack", "platform", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render: %v\noutput: %s", err, renderOut)
	}
	if !strings.Contains(renderOut, "style.stroke-dash: 3") {
		t.Fatalf("ubx render on a TS direct-SDK-imported blueprint's own resources is missing the dashed-border grouping:\n%s", renderOut)
	}
	if !strings.Contains(renderOut, `"primary"`) || !strings.Contains(renderOut, `"mirror"`) {
		t.Fatalf("ubx render missing one of the blueprint's own resources:\n%s", renderOut)
	}
}
