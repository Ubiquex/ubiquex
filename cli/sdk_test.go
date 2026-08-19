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
	writeConfig(t, dir, "") // no [thirdparty_providers] table at all

	out, err := runUbx(t, nil, "sdk", "gen", "--out", filepath.Join(dir, "out"))
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "[thirdparty_providers]") {
		t.Fatalf("expected the error to name the missing [thirdparty_providers] table, got: %v", err)
	}
}

// TestSDKGen_MultipleLanguagesSameOut_DoNotCollide is a real, live-
// verified fix this session, not a hypothetical: --out defaults
// IDENTICALLY across --lang go/ts/py, and (unlike the old flat-file
// scheme, where each language's own file EXTENSION naturally
// disambiguated "hashicorp-aws.go" from "hashicorp-aws.ts" from
// "hashicorp_aws.py" sharing one directory) a repo-shaped TREE has no
// such built-in per-language distinction at the top level -- running
// `ubx sdk gen` for all three languages against the SAME --out (the
// obvious thing to do if you want all three) would otherwise interleave
// three different ecosystems' manifests and source trees into ONE
// directory, breaking the "ready to become its own real repo" promise.
// Fixed by making --lang its own path segment (<out>/<lang>/<source>/);
// this test proves all three coexist cleanly under one shared --out.
func TestSDKGen_MultipleLanguagesSameOut_DoNotCollide(t *testing.T) {
	requirePython3(t)
	requireDenoCLI(t)

	dir := t.TempDir()
	mirrorDir := t.TempDir()
	outDir := filepath.Join(dir, "generated")

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[thirdparty_providers]
"fake/widget" = "0.1.0"
`)

	env := []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}
	for _, lang := range []string{"go", "ts", "py"} {
		if out, err := runUbx(t, env, "sdk", "gen", "--out", outDir, "--lang", lang); err != nil {
			t.Fatalf("ubx sdk gen --lang %s: %v\noutput: %s", lang, err, out)
		}
	}

	// UBI-138: all three languages now land side by side under ONE
	// shared source directory (<outDir>/fake-widget/sdk/{go,typescript,python}/)
	// -- no per-language top-level segment any more, superseded by each
	// template's own self-namespacing output (cli/sdk.go's own repoDir
	// doc comment has the full account). Every language's own repo tree
	// still survives, side by side, none overwritten or merged into
	// another's -- that's the real property this test proves, the
	// mechanism just moved one level down.
	genDir := filepath.Join(outDir, "fake-widget")
	if _, err := os.Stat(filepath.Join(genDir, "sdk", "go", "go.mod")); err != nil {
		t.Errorf("go.mod missing after all three languages generated to the same --out: %v", err)
	}
	if _, err := os.Stat(filepath.Join(genDir, "sdk", "typescript", "package.json")); err != nil {
		t.Errorf("package.json missing after all three languages generated to the same --out: %v", err)
	}
	if _, err := os.Stat(filepath.Join(genDir, "sdk", "python", "pyproject.toml")); err != nil {
		t.Errorf("pyproject.toml missing after all three languages generated to the same --out: %v", err)
	}

	assertGoRepoCompiles(t, filepath.Join(genDir, "sdk", "go"))
	assertTSRepoChecks(t, filepath.Join(genDir, "sdk", "typescript"))
	assertPyRepoImports(t, filepath.Join(genDir, "sdk", "python"), "ubx.widget.widget.widget", "Widget", "WidgetConfig")
}

// TestSDKGen_GeneratesBindingsFromRealSchema_ViaMirror covers UBI-98's
// own repo-shaped --lang ts (the default) output end to end via the real
// CLI path -- see TestSDKGen_GeneratesGoBindingsFromRealSchema_ViaMirror's
// own doc comment for why "fake_widget" derives service AND local name
// both "widget".
func TestSDKGen_GeneratesBindingsFromRealSchema_ViaMirror(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()
	outDir := filepath.Join(dir, "generated")

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[thirdparty_providers]
"fake/widget" = "0.1.0"
`)

	// UBI-138: genDir is the printed top-level path (no language segment
	// any more -- superseded by GeneratedRepo's own "sdk/typescript/"
	// self-namespacing, cli/sdk.go's own GeneratedRepo doc comment has the
	// full account); repoDir is where package.json etc. actually land.
	genDir := filepath.Join(outDir, "fake-widget")
	repoDir := filepath.Join(genDir, "sdk", "typescript")
	out, err := runUbx(t, []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}, "sdk", "gen", "--out", outDir)
	if err != nil {
		t.Fatalf("ubx sdk gen: %v\noutput: %s", err, out)
	}
	wantStdout := "generated 1 resource type(s) for fake/widget@0.1.0 -> " + genDir
	if !strings.Contains(out, wantStdout) {
		t.Fatalf("unexpected stdout: %s\nwant to contain: %s", out, wantStdout)
	}

	pkgJSON, err := os.ReadFile(filepath.Join(repoDir, "package.json"))
	if err != nil {
		t.Fatalf("reading package.json: %v", err)
	}
	mustContainSDK(t, string(pkgJSON), `"name": "@ubx/sdk-widget"`)

	docContent, err := os.ReadFile(filepath.Join(repoDir, "widget", "widget", "doc.ts"))
	if err != nil {
		t.Fatalf("reading widget/widget/doc.ts: %v", err)
	}
	mustContainSDK(t, string(docContent), `__ubxSourceProvenance = { source: "fake/widget", version: "0.1.0" }`)

	genPath := filepath.Join(repoDir, "widget", "widget", "widget.ts")
	content, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("reading widget/widget/widget.ts: %v", err)
	}
	generated := string(content)

	// fake_widget's own real schema (provider/internal/fakeprovider,
	// shared across both protocol versions): id (computed string), name
	// (required string), tags (optional map<string>).
	mustContainSDK(t, generated, "export interface WidgetConfig {")
	mustContainSDK(t, generated, "name: string | Computed<string>;") // required, no `?`
	mustContainSDK(t, generated, "tags?: Record<string, string> | Computed<Record<string, string>>;")
	mustContainSDK(t, generated, "export interface WidgetAttrs {")
	mustContainSDK(t, generated, "id: string;")
	mustContainSDK(t, generated, `export const Widget: ResourceBinding<WidgetConfig, WidgetAttrs> = {`)
	// wireType carries the REAL, full wire type -- never shortened.
	mustContainSDK(t, generated, `wireType: "fake_widget",`)
	mustContainSDK(t, generated, `name: "name",`)
	mustContainSDK(t, generated, `tags: "tags",`)
	mustNotContainSDK(t, generated, "FakeWidget")

	// id is computed-only -- must never appear in the Config interface's
	// own field list, only in Attrs (checked above) -- the exact real bug
	// TestGeneratedFile_NestedObjectBlock's own sibling in
	// sdk/codegen/templates/ts caught earlier this session, re-checked
	// here against the real CLI path end to end.
	configBlock := generated[strings.Index(generated, "export interface WidgetConfig {"):strings.Index(generated, "export interface WidgetAttrs {")]
	if strings.Contains(configBlock, "id:") || strings.Contains(configBlock, "id?:") {
		t.Fatalf("WidgetConfig should not include the computed-only id field:\n%s", configBlock)
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

	// Not just string matching -- the generated repo tree must actually
	// type-check against the real @ubx/sdk runtime, exactly as a real
	// evaluation would resolve it.
	requireDenoCLI(t)
	assertTSRepoChecks(t, repoDir)
}

// TestSDKGen_GeneratesGoBindingsFromRealSchema_ViaMirror covers UBI-98's
// own repo-shaped --lang go output end to end via the real CLI path:
// "fake_widget" (tokens "fake"/"widget", no third token -- the same
// bare-two-token shape 11 real hashicorp/aws@6.54.0 types hit, e.g.
// "aws_vpc") derives service AND local name both "widget", so the
// expected tree is <outDir>/fake-widget/sdk/go/{go.mod,widget/widget/{doc.go,widget.go}}
// (UBI-106: every service package nests under the provider's own
// shortName directory, "widget/" here, never at the repo root; UBI-138:
// the whole tree additionally nests under "sdk/go/" now, the real Pulumi-
// precedent sibling-language-directory structure, GeneratedRepo's own
// doc comment has the full account) -- module
// github.com/ubiquex/ubx-sdk-widget/sdk/go, package widget, type Widget
// (never FakeWidget/generated.FakeWidget -- the founder's own locked
// naming scheme, checked here against the real CLI path, not just
// sdk/codegen/templates/go's own unit tests).
func TestSDKGen_GeneratesGoBindingsFromRealSchema_ViaMirror(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()
	outDir := filepath.Join(dir, "generated")

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[thirdparty_providers]
"fake/widget" = "0.1.0"
`)

	// UBI-138: genDir is the printed top-level path (no language segment
	// any more); repoDir (genDir/sdk/go) is where go.mod etc. actually land.
	genDir := filepath.Join(outDir, "fake-widget")
	repoDir := filepath.Join(genDir, "sdk", "go")
	out, err := runUbx(t, []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}, "sdk", "gen", "--out", outDir, "--lang", "go")
	if err != nil {
		t.Fatalf("ubx sdk gen --lang go: %v\noutput: %s", err, out)
	}
	wantStdout := "generated 1 resource type(s) for fake/widget@0.1.0 -> " + genDir
	if !strings.Contains(out, wantStdout) {
		t.Fatalf("unexpected stdout: %s\nwant to contain: %s", out, wantStdout)
	}

	goMod, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	mustContainSDK(t, string(goMod), "module github.com/ubiquex/ubx-sdk-widget/sdk/go")
	mustContainSDK(t, string(goMod), "require github.com/ubiquex/ubx-sdk-go v0.0.0")

	docContent, err := os.ReadFile(filepath.Join(repoDir, "widget", "widget", "doc.go"))
	if err != nil {
		t.Fatalf("reading widget/widget/doc.go: %v", err)
	}
	mustContainSDK(t, string(docContent), "package widget")
	mustContainSDK(t, string(docContent), `Source: "fake/widget", Version: "0.1.0"`)

	genPath := filepath.Join(repoDir, "widget", "widget", "widget.go")
	content, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("reading widget/widget/widget.go: %v", err)
	}
	generated := string(content)

	mustContainSDK(t, generated, "package widget")
	mustContainSDK(t, generated, "type WidgetConfig struct {")
	mustContainSDK(t, generated, "Name any")
	mustContainSDK(t, generated, "Tags any")
	mustContainSDK(t, generated, `var Widget = ubx.ResourceBinding{`)
	// WireType carries the REAL, full wire type -- never shortened.
	mustContainSDK(t, generated, `WireType: "fake_widget",`)
	mustContainSDK(t, generated, `"Name": ubx.FieldSpec{WireName: "name"},`)
	mustContainSDK(t, generated, `"Tags": ubx.FieldSpec{WireName: "tags"},`)
	mustNotContainSDK(t, generated, "FakeWidget")

	// id is computed-only -- must never appear in the Config struct.
	configBlock := generated[strings.Index(generated, "type WidgetConfig struct {"):]
	configBlock = configBlock[:strings.Index(configBlock, "}")]
	if strings.Contains(configBlock, "Id ") {
		t.Fatalf("WidgetConfig should not include the computed-only id field:\n%s", configBlock)
	}

	// Not just string matching -- the generated repo tree must actually
	// compile against the real sdk/go runtime module, exactly as a real
	// consumer's own go.mod would eventually resolve it (a local replace
	// appended here, mirroring how sdk/conformance/programs/go/go.mod
	// does it for real -- the generator's own emitted go.mod deliberately
	// has no such replace, since a real consumer would pin a real
	// published version instead).
	assertGoRepoCompiles(t, repoDir)
}

// assertGoRepoCompiles builds repoDir (a generateGoProviderRepo's own
// output: go.mod + one directory per derived service package) in place
// after appending a replace directive pointing github.com/ubiquex/ubx-sdk-go
// at this repo's own real sdk/go directory (the generator's own emitted
// go.mod deliberately has no such replace -- a real consumer would pin a
// real published version; only this test needs the local override,
// mirroring how sdk/conformance/programs/go/go.mod does it for real) --
// proof the generated repo tree is not just textually plausible but
// real, compilable Go across every package it wrote, not just one file.
func assertGoRepoCompiles(t *testing.T, repoDir string) {
	t.Helper()
	sdkGoRoot, err := filepath.Abs(filepath.Join("..", "sdk", "go"))
	if err != nil {
		t.Fatalf("resolve sdk/go path: %v", err)
	}

	goModPath := filepath.Join(repoDir, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("reading generated go.mod: %v", err)
	}
	updated := string(goMod) + "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
	if err := os.WriteFile(goModPath, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated repo tree does not compile against sdk/go/runtime: %v\n%s", err, out)
	}
}

// TestSDKGen_ExistingGoModDirective_PreservedNotOverwritten is UBI-153's
// own real regression test: a real, already-existing go.mod at the real
// target path, pre-set to a "go" directive HIGHER than the real go
// toolchain running this test (proves this isn't a coincidental match
// with the runtime.Version() fallback) -- confirms `ubx sdk gen` reads
// and preserves it verbatim rather than overwriting it with any
// hardcoded template value, the exact silent-downgrade UBI-151 found
// live against ubx-sdk-google's own real go.mod.
func TestSDKGen_ExistingGoModDirective_PreservedNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()
	outDir := filepath.Join(dir, "generated")

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[thirdparty_providers]
"fake/widget" = "0.1.0"
`)

	// A real, deliberately fictional, higher-than-anything-real version
	// -- proves the real value found on disk survives, not a
	// coincidental match with the real runtime.Version() fallback.
	const fixtureGoDirective = "1.99.9"
	repoDir := filepath.Join(outDir, "fake-widget", "sdk", "go")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureGoMod := "module github.com/ubiquex/ubx-sdk-widget/sdk/go\n\ngo " + fixtureGoDirective + "\n\nrequire github.com/ubiquex/ubx-sdk-go v0.0.0\n"
	if err := os.WriteFile(filepath.Join(repoDir, "go.mod"), []byte(fixtureGoMod), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}, "sdk", "gen", "--out", outDir, "--lang", "go")
	if err != nil {
		t.Fatalf("ubx sdk gen --lang go: %v\noutput: %s", err, out)
	}

	goMod, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	mustContainSDK(t, string(goMod), "go "+fixtureGoDirective+"\n")
	if strings.Contains(string(goMod), "go 1.23\n") {
		t.Fatalf("regen silently downgraded the real, existing go.mod directive to the old hardcoded 1.23:\n%s", goMod)
	}
}

// TestSDKGen_NoExistingGoMod_FallsBackToRuntimeGoVersion covers the
// other real half of UBI-153: a genuinely new repo, nothing to
// preserve. The real, sensible fallback is the real go toolchain that
// built the running ubx binary itself (runtime.Version()) -- never a
// second hardcoded constant, which would just recreate the same
// staleness bug one level up.
func TestSDKGen_NoExistingGoMod_FallsBackToRuntimeGoVersion(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()
	outDir := filepath.Join(dir, "generated")

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[thirdparty_providers]
"fake/widget" = "0.1.0"
`)

	out, err := runUbx(t, []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}, "sdk", "gen", "--out", outDir, "--lang", "go")
	if err != nil {
		t.Fatalf("ubx sdk gen --lang go: %v\noutput: %s", err, out)
	}

	repoDir := filepath.Join(outDir, "fake-widget", "sdk", "go")
	goMod, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	wantDirective := strings.TrimPrefix(runtime.Version(), "go")
	mustContainSDK(t, string(goMod), "go "+wantDirective+"\n")
}

// TestSDKGen_GeneratesPyBindingsFromRealSchema_ViaMirror covers UBI-98's
// own repo-shaped --lang py output end to end via the real CLI path --
// see TestSDKGen_GeneratesGoBindingsFromRealSchema_ViaMirror's own doc
// comment for why "fake_widget" derives service AND local name both
// "widget".
func TestSDKGen_GeneratesPyBindingsFromRealSchema_ViaMirror(t *testing.T) {
	requirePython3(t)
	dir := t.TempDir()
	mirrorDir := t.TempDir()
	outDir := filepath.Join(dir, "generated")

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[thirdparty_providers]
"fake/widget" = "0.1.0"
`)

	// UBI-138: genDir is the printed top-level path (no language segment
	// any more); repoDir (genDir/sdk/python) is where pyproject.toml etc.
	// actually land.
	genDir := filepath.Join(outDir, "fake-widget")
	repoDir := filepath.Join(genDir, "sdk", "python")
	out, err := runUbx(t, []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"UBX_PROVIDER_MIRROR=" + mirrorDir,
	}, "sdk", "gen", "--out", outDir, "--lang", "py")
	if err != nil {
		t.Fatalf("ubx sdk gen --lang py: %v\noutput: %s", err, out)
	}
	wantStdout := "generated 1 resource type(s) for fake/widget@0.1.0 -> " + genDir
	if !strings.Contains(out, wantStdout) {
		t.Fatalf("unexpected stdout: %s\nwant to contain: %s", out, wantStdout)
	}

	pyproject, err := os.ReadFile(filepath.Join(repoDir, "pyproject.toml"))
	if err != nil {
		t.Fatalf("reading pyproject.toml: %v", err)
	}
	mustContainSDK(t, string(pyproject), `name = "ubx-sdk-widget"`)
	mustContainSDK(t, string(pyproject), "namespaces = true")

	// "ubx" itself never gets an __init__.py -- a real PEP 420 implicit
	// namespace package.
	if _, err := os.Stat(filepath.Join(repoDir, "ubx", "__init__.py")); err == nil {
		t.Fatalf("ubx/__init__.py must not exist (would make \"ubx\" a regular package, not a namespace package)")
	}

	serviceInitContent, err := os.ReadFile(filepath.Join(repoDir, "ubx", "widget", "widget", "__init__.py"))
	if err != nil {
		t.Fatalf("reading ubx/widget/widget/__init__.py: %v", err)
	}
	mustContainSDK(t, string(serviceInitContent), `SOURCE_PROVENANCE = {"source": "fake/widget", "version": "0.1.0"}`)
	mustContainSDK(t, string(serviceInitContent), "from .widget import Widget, WidgetConfig")

	genPath := filepath.Join(repoDir, "ubx", "widget", "widget", "widget.py")
	content, err := os.ReadFile(genPath)
	if err != nil {
		t.Fatalf("reading ubx/widget/widget/widget.py: %v", err)
	}
	generated := string(content)

	mustContainSDK(t, generated, "class WidgetConfig:")
	mustContainSDK(t, generated, "name: Any = None")
	mustContainSDK(t, generated, "tags: Any = None")
	mustContainSDK(t, generated, "Widget = ubx.ResourceBinding(")
	// wire_type carries the REAL, full wire type -- never shortened.
	mustContainSDK(t, generated, `wire_type="fake_widget",`)
	mustContainSDK(t, generated, `"name": ubx.FieldSpec(wire_name="name"),`)
	mustContainSDK(t, generated, `"tags": ubx.FieldSpec(wire_name="tags"),`)
	mustNotContainSDK(t, generated, "FakeWidget")

	// id is computed-only -- must never appear in the Config dataclass.
	configBlock := generated[strings.Index(generated, "class WidgetConfig:"):]
	configBlock = configBlock[:strings.Index(configBlock, "\n\n")]
	if strings.Contains(configBlock, "id:") {
		t.Fatalf("WidgetConfig should not include the computed-only id field:\n%s", configBlock)
	}

	// Not just string matching -- the generated repo tree must actually
	// import and run against the real sdk/py/ubx_sdk runtime, exactly
	// as a real SDK program would. "ubx.widget.widget.widget" (not
	// "widget.widget"): UBI-106 nests the service package one level
	// deeper, under the provider's own shortName directory, which itself
	// nests under the shared "ubx" namespace-package root (PEP 420
	// implicit namespace package, no __init__.py at the "ubx" level
	// itself -- google.cloud.*/azure.mgmt.*-style precedent).
	assertPyRepoImports(t, repoDir, "ubx.widget.widget.widget", "Widget", "WidgetConfig")

	// The real, final import a consumer writes goes through the service
	// package's own re-export, never the file-stutter path.
	assertPyRepoImports(t, repoDir, "ubx.widget.widget", "Widget", "WidgetConfig")
}

// requirePython3 skips a test when python3 isn't on PATH -- this
// specific check runs the generated file natively (not under WASI --
// pyeval's own package proves the sandboxed path works; this test's job
// is proving the generated SOURCE is real, importable Python, a cheaper
// and faster check than spinning up wasmtime for it).
func requirePython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found in PATH -- skipping ubx sdk gen --lang py's own import-check test")
	}
}

// assertPyRepoImports imports dottedModule (e.g. "ubx.widget.widget", a
// module inside repoDir -- UBI-98's own repo-shaped tree, package
// directories with their own __init__.py, rooted under the shared "ubx"
// PEP 420 namespace package -- no __init__.py at the "ubx" level itself,
// resolved via repoDir on PYTHONPATH the same as any real namespace
// package) as a real Python module (with both sdk/py and repoDir itself
// on PYTHONPATH, the same shape a real end user's own program would
// need) and constructs its own generated Config dataclass via
// bindingName/configName -- proof the generated source is not just
// textually plausible but real, importable Python, through the real
// dotted-package import path a per-service directory restructure
// specifically requires (unlike the old flat single-file shape, which
// needed no package machinery at all).
func assertPyRepoImports(t *testing.T, repoDir, dottedModule, bindingName, configName string) {
	t.Helper()
	sdkPyDir, err := filepath.Abs(filepath.Join("..", "sdk", "py"))
	if err != nil {
		t.Fatalf("resolve sdk/py path: %v", err)
	}

	script := fmt.Sprintf(`
import importlib
mod = importlib.import_module(%q)
print(getattr(mod, %q))
print(getattr(mod, %q)(name="x"))
`, dottedModule, bindingName, configName)

	// Deliberately NOT -I (isolated mode) here -- isolated mode ignores
	// PYTHONPATH entirely, which this check relies on to resolve both
	// sdk/py/ubx_sdk and the generated repo tree itself.
	cmd := exec.Command("python3", "-c", script)
	cmd.Env = []string{"PYTHONPATH=" + sdkPyDir + string(os.PathListSeparator) + repoDir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated repo tree does not import against sdk/py/ubx_sdk: %v\n%s", err, out)
	}
}

// requireDenoCLI skips a test when `deno` isn't on PATH -- matches this
// project's own requirePython3/requireGoToolchain pattern.
func requireDenoCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH -- skipping ubx sdk gen (--lang ts)'s own type-check test")
	}
}

// assertTSRepoChecks writes a deno.json import map (pointing the bare
// "@ubx/sdk" specifier every generated file imports at this repo's own
// real sdk/ts/runtime/src/index.ts) into repoDir and runs
// `deno check --no-remote` against every ".ts" file in the tree --
// mirrors assertGoRepoCompiles/assertPyRepoImports' own role for TS:
// proof the generated repo tree is not just textually plausible but
// real, type-checkable TypeScript.
func assertTSRepoChecks(t *testing.T, repoDir string) {
	t.Helper()
	sdkTSRuntime, err := filepath.Abs(filepath.Join("..", "sdk", "ts", "runtime", "src", "index.ts"))
	if err != nil {
		t.Fatalf("resolve sdk/ts/runtime/src/index.ts path: %v", err)
	}

	importMap := fmt.Sprintf(`{"imports":{"@ubx/sdk":%q}}`, sdkTSRuntime)
	if err := os.WriteFile(filepath.Join(repoDir, "deno.json"), []byte(importMap), 0o644); err != nil {
		t.Fatal(err)
	}

	var tsFiles []string
	err = filepath.WalkDir(repoDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && filepath.Ext(path) == ".ts" {
			tsFiles = append(tsFiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", repoDir, err)
	}

	args := append([]string{"check", "--no-remote"}, tsFiles...)
	cmd := exec.Command("deno", args...)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated repo tree does not type-check against @ubx/sdk: %v\n%s", err, out)
	}
}

func mustContainSDK(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("output missing %s:\n%s", fmt.Sprintf("%q", needle), haystack)
	}
}

func mustNotContainSDK(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("output unexpectedly contains %s:\n%s", fmt.Sprintf("%q", needle), haystack)
	}
}
