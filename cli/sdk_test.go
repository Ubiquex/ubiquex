package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// writeMirrorProvider places fakeProviderBinary (built once by TestMain,
// see scan_test.go) at the exact path provider.Acquire's
// UBX_PROVIDER_MIRROR resolution order expects
// (<mirrorDir>/<namespace>/<type>/<version>/<goos>_<goarch>/<file>) --
// the same mechanism UBI-8 built specifically so a local binary can be
// used "directly, unverified" with no network access at all
// (docs/architecture.md), reused here rather than inventing a second
// hermetic-provider seam just for "ubx sdk gen"'s own tests.
func writeMirrorProvider(t *testing.T, mirrorDir, namespace, typ, version string) {
	t.Helper()
	dir := filepath.Join(mirrorDir, namespace, typ, version, runtime.GOOS+"_"+runtime.GOARCH)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fakeProviderBinary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fakeprovider"), data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestSDKGen_NoProvidersDeclared_Errors(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, "") // no [providers] table at all

	out, err := runUbx(t, nil, "sdk", "gen", "--out", filepath.Join(dir, "out"))
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "[providers]") {
		t.Fatalf("expected the error to name the missing [providers] table, got: %v", err)
	}
}

func TestSDKGen_GeneratesBindingsFromRealSchema_ViaMirror(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()
	outDir := filepath.Join(dir, "generated")

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[providers]
"fake/widget" = "0.1.0"
`)

	out, err := runUbx(t, []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}, "sdk", "gen", "--out", outDir)
	if err != nil {
		t.Fatalf("ubx sdk gen: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "generated 1 resource type(s) for fake/widget@0.1.0") {
		t.Fatalf("unexpected stdout: %s", out)
	}

	genPath := filepath.Join(outDir, "fake-widget.ts")
	content, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	generated := string(content)

	// fake_widget's own real schema (provider/internal/fakeprovider,
	// shared across both protocol versions): id (computed string), name
	// (required string), tags (optional map<string>).
	mustContainSDK(t, generated, `export const __ubxSourceProvenance = { source: "fake/widget", version: "0.1.0" } as const;`)
	mustContainSDK(t, generated, "export interface FakeWidgetConfig {")
	mustContainSDK(t, generated, "name: string | Computed<string>;") // required, no `?`
	mustContainSDK(t, generated, "tags?: Record<string, string> | Computed<Record<string, string>>;")
	mustContainSDK(t, generated, "export interface FakeWidgetAttrs {")
	mustContainSDK(t, generated, "id: string;")
	mustContainSDK(t, generated, `export const FakeWidget: ResourceBinding<FakeWidgetConfig, FakeWidgetAttrs> = {`)
	mustContainSDK(t, generated, `wireType: "fake_widget",`)
	mustContainSDK(t, generated, `name: "name",`)
	mustContainSDK(t, generated, `tags: "tags",`)

	// id is computed-only -- must never appear in the Config interface's
	// own field list, only in Attrs (checked above) -- the exact real bug
	// TestGeneratedFile_NestedObjectBlock's own sibling in
	// sdk/codegen/templates/ts caught earlier this session, re-checked
	// here against the real CLI path end to end.
	configBlock := generated[strings.Index(generated, "export interface FakeWidgetConfig {"):strings.Index(generated, "export interface FakeWidgetAttrs {")]
	if strings.Contains(configBlock, "id:") || strings.Contains(configBlock, "id?:") {
		t.Fatalf("FakeWidgetConfig should not include the computed-only id field:\n%s", configBlock)
	}

	// Re-running is idempotent and deterministic -- the same real schema,
	// acquired fresh again via the mirror, produces byte-identical output
	// (docs/sdk.md's own "always regenerates... never a stale cache"
	// posture, checked here for the CLI path specifically, not just
	// sdk/codegen/templates/ts's own Go-level determinism test).
	out2, err := runUbx(t, []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}, "sdk", "gen", "--out", outDir)
	if err != nil {
		t.Fatalf("ubx sdk gen (rerun): %v\noutput: %s", err, out2)
	}
	rerunContent, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("reading regenerated file: %v", err)
	}
	if string(rerunContent) != generated {
		t.Fatalf("ubx sdk gen produced different output on a rerun against the identical pinned schema")
	}
}

func mustContainSDK(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("output missing %s:\n%s", fmt.Sprintf("%q", needle), haystack)
	}
}
