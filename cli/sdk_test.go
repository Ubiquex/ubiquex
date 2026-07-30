package cli

import (
	"fmt"
	"os"
	"os/exec"
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

func TestSDKGen_GeneratesGoBindingsFromRealSchema_ViaMirror(t *testing.T) {
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
	}, "sdk", "gen", "--out", outDir, "--lang", "go")
	if err != nil {
		t.Fatalf("ubx sdk gen --lang go: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "generated 1 resource type(s) for fake/widget@0.1.0") {
		t.Fatalf("unexpected stdout: %s", out)
	}

	genPath := filepath.Join(outDir, "fake-widget.go")
	content, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("reading generated file: %v", err)
	}
	generated := string(content)

	mustContainSDK(t, generated, "package generated")
	mustContainSDK(t, generated, `Source: "fake/widget", Version: "0.1.0"`)
	mustContainSDK(t, generated, "type FakeWidgetConfig struct {")
	mustContainSDK(t, generated, "Name any")
	mustContainSDK(t, generated, "Tags any")
	mustContainSDK(t, generated, `var FakeWidget = sdk.ResourceBinding{`)
	mustContainSDK(t, generated, `WireType: "fake_widget",`)
	mustContainSDK(t, generated, `"Name": sdk.FieldSpec{WireName: "name"},`)
	mustContainSDK(t, generated, `"Tags": sdk.FieldSpec{WireName: "tags"},`)

	// id is computed-only -- must never appear in the Config struct.
	configBlock := generated[strings.Index(generated, "type FakeWidgetConfig struct {"):]
	configBlock = configBlock[:strings.Index(configBlock, "}")]
	if strings.Contains(configBlock, "Id ") {
		t.Fatalf("FakeWidgetConfig should not include the computed-only id field:\n%s", configBlock)
	}

	// Not just string matching -- the generated file must actually
	// compile against the real sdk/go runtime module, exactly as a real
	// SDK program's own go.mod would resolve it (a local replace,
	// mirroring how sdk/conformance/programs/go does it for real).
	assertGoFileCompiles(t, genPath, "generated")
}

// assertGoFileCompiles copies genPath into a fresh, throwaway Go module
// that requires+replaces github.com/ubiquex/ubx-sdk-go with this repo's
// own real sdk/go directory, and runs `go build` against it -- proof the
// generated source is not just textually plausible but real, compilable
// Go that a program can actually import and use.
func assertGoFileCompiles(t *testing.T, genPath, pkgName string) {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", "sdk", "go"))
	if err != nil {
		t.Fatalf("resolve sdk/go path: %v", err)
	}

	moduleDir := t.TempDir()
	pkgDir := filepath.Join(moduleDir, pkgName)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "generated.go"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	goMod := "module ubx-sdkgen-compile-check\n\ngo 1.23\n\n" +
		"require github.com/ubiquex/ubx-sdk-go v0.0.0\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + repoRoot + "\n"
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated Go file does not compile against sdk/go/runtime: %v\n%s", err, out)
	}
}

func mustContainSDK(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("output missing %s:\n%s", fmt.Sprintf("%q", needle), haystack)
	}
}
