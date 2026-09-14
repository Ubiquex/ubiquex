package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTSBlueprintConfig(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTSBlueprintImports_RelativeTargets is the fix: a blueprint's own
// deno.json travels in the content store and is now read.
//
// Targets come back absolute, because a scope entry is applied to
// modules anywhere and a relative path would resolve against whoever is
// importing rather than against the blueprint.
func TestTSBlueprintImports_RelativeTargets(t *testing.T) {
	dir := t.TempDir()
	writeTSBlueprintConfig(t, dir, `{"imports":{"helper":"./helper.ts","deep":"./lib/deep.ts"}}`)

	got, err := tsBlueprintImports(dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"helper": "file://" + filepath.ToSlash(filepath.Join(dir, "helper.ts")),
		"deep":   "file://" + filepath.ToSlash(filepath.Join(dir, "lib", "deep.ts")),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("imports[%q] = %q, want %q", k, got[k], v)
		}
	}
}

// TestTSBlueprintImports_NoConfig: a built blueprint imports "@ubx/sdk"
// and nothing else, so it ships no deno.json. That is the ordinary case
// and must stay free.
func TestTSBlueprintImports_NoConfig(t *testing.T) {
	got, err := tsBlueprintImports(t.TempDir(), "bp")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected no imports for a blueprint with no deno.json, got %v", got)
	}
}

// TestTSBlueprintImports_RemoteIsRefused pins the boundary this change
// deliberately stops at.
//
// Honouring a registry specifier would mean `ubx plan` fetching during
// evaluation, which GOPROXY=off already answers "no" to for Go.
// Refusing loudly, naming the blueprint and the decision, beats letting
// Deno fail later with a message about node_modules that names neither.
func TestTSBlueprintImports_RemoteIsRefused(t *testing.T) {
	for _, target := range []string{
		"jsr:@ubx/sdk-aws@1.2.0",
		"npm:left-pad@1.3.0",
		"https://deno.land/x/thing/mod.ts",
	} {
		t.Run(target, func(t *testing.T) {
			dir := t.TempDir()
			writeTSBlueprintConfig(t, dir, `{"imports":{"dep":"`+target+`"}}`)

			_, err := tsBlueprintImports(dir, "widget-bp")
			if err == nil {
				t.Fatalf("expected a refusal for %q", target)
			}
			msg := err.Error()
			for _, want := range []string{"widget-bp", target, "deno.json", "UBI-274"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal does not name %q: %s", want, msg)
				}
			}
		})
	}
}

// TestTSBlueprintImports_RefusalReportsTheLock: whether the blueprint
// pins its dependencies changes what the right answer to the open
// question is, so the refusal says which case this is rather than
// leaving whoever reads it to go and look.
func TestTSBlueprintImports_RefusalReportsTheLock(t *testing.T) {
	withLock := t.TempDir()
	writeTSBlueprintConfig(t, withLock, `{"imports":{"dep":"jsr:@scope/dep@1.0.0"}}`)
	if err := os.WriteFile(filepath.Join(withLock, denoLockFileName), []byte(`{"version":"4"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := tsBlueprintImports(withLock, "bp")
	if err == nil || !strings.Contains(err.Error(), "does ship a deno.lock") {
		t.Fatalf("a blueprint that pins its graph should be reported as such: %v", err)
	}

	without := t.TempDir()
	writeTSBlueprintConfig(t, without, `{"imports":{"dep":"jsr:@scope/dep@1.0.0"}}`)
	_, err = tsBlueprintImports(without, "bp")
	if err == nil || !strings.Contains(err.Error(), "ships no deno.lock") {
		t.Fatalf("a blueprint that does not pin should be reported as such: %v", err)
	}
}

// TestTSBlueprintImports_RelativeSurvivesAlongsideRemote: the refusal is
// all-or-nothing on purpose. Silently dropping the registry entries and
// resolving the rest would produce a blueprint that half works, failing
// later at the first import of the dropped one.
func TestTSBlueprintImports_RelativeSurvivesAlongsideRemote(t *testing.T) {
	dir := t.TempDir()
	writeTSBlueprintConfig(t, dir, `{"imports":{"local":"./a.ts","remote":"npm:x@1"}}`)
	got, err := tsBlueprintImports(dir, "bp")
	if err == nil {
		t.Fatal("a config mixing local and registry targets must be refused, not partly honoured")
	}
	if got != nil {
		t.Fatalf("no imports should be returned alongside a refusal, got %v", got)
	}
}

func writePackageJSON(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, packageJSONFileName), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeNPMPackage writes a package directory with a main entry.
func writeNPMPackage(t *testing.T, dir, name, main string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageJSON(t, dir, `{"name":"`+name+`","version":"1.0.0","main":"`+main+`"}`)
	if err := os.WriteFile(filepath.Join(dir, main), []byte("export const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTSBlueprintImports_PackageJSONRegistryIsRefused is the half the
// first version missed.
//
// A blueprint authored with npm tooling has a package.json and no
// deno.json, which is the common case, and it fell through to Deno's own
// resolution and failed later with a message about node_modules naming
// neither the blueprint nor the reason. Refusing one format loudly and
// the other silently is an inconsistent boundary, not a narrow one.
func TestTSBlueprintImports_PackageJSONRegistryIsRefused(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{"dependencies":{"left-pad":"1.3.0"}}`)

	_, err := tsBlueprintImports(dir, "ts-bp")
	if err == nil {
		t.Fatal("a registry dependency in package.json must be refused")
	}
	for _, want := range []string{"ts-bp", "left-pad", packageJSONFileName, "UBI-274"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q: %s", want, err)
		}
	}
}

// TestTSBlueprintImports_FileDependencyInsideResolves: the boundary is
// relative-file versus registry, not which format declared it. A file:
// dependency names a path exactly as a deno.json relative target does.
func TestTSBlueprintImports_FileDependencyInsideResolves(t *testing.T) {
	dir := t.TempDir()
	writeNPMPackage(t, filepath.Join(dir, "deps", "helper"), "helper", "entry.js")
	writePackageJSON(t, dir, `{"dependencies":{"helper":"file:./deps/helper"}}`)

	got, err := tsBlueprintImports(dir, "ts-bp")
	if err != nil {
		t.Fatalf("a file: dependency inside the blueprint must resolve: %v", err)
	}
	want := "file://" + filepath.ToSlash(filepath.Join(dir, "deps", "helper", "entry.js"))
	if got["helper"] != want {
		t.Fatalf("imports[helper] = %q, want the package's own entry FILE %q", got["helper"], want)
	}
}

// TestTSBlueprintImports_FileDependencyOutsideIsRefused is the wrinkle
// that makes file: unlike a deno.json target.
//
// An npm file: usually points outside the declaring package, which is
// the whole reason to use one. Packaging walks the blueprint directory
// only, so that target never travelled and the pulled blueprint carries
// a reference to something that is not there. Verified: packaging a
// blueprint with `file:../sibling` produces an archive holding the
// package.json and not the sibling.
func TestTSBlueprintImports_FileDependencyOutsideIsRefused(t *testing.T) {
	parent := t.TempDir()
	writeNPMPackage(t, filepath.Join(parent, "sibling"), "sibling", "index.js")
	dir := filepath.Join(parent, "bp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageJSON(t, dir, `{"dependencies":{"sibling":"file:../sibling"}}`)

	_, err := tsBlueprintImports(dir, "ts-bp")
	if err == nil {
		t.Fatal("a file: dependency pointing outside the blueprint must be refused: it did not travel")
	}
	if !strings.Contains(err.Error(), "never travelled") {
		t.Errorf("refusal should say the target did not travel, got: %s", err)
	}
	// And it must NOT be reported as a registry dependency, which would
	// send the reader to the wrong question entirely.
	if strings.Contains(err.Error(), "resolve from a registry") {
		t.Errorf("an outside file: is not a registry dependency: %s", err)
	}
}

// TestTSBlueprintImports_ExportsMapIsRefused: npm's "exports" supports
// conditional resolution and subpath patterns. A partial implementation
// would resolve some packages to the WRONG file rather than failing,
// which is worse than not resolving them.
func TestTSBlueprintImports_ExportsMapIsRefused(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "deps", "fancy")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageJSON(t, pkg, `{"name":"fancy","exports":{".":{"import":"./esm.js","require":"./cjs.js"}}}`)
	writePackageJSON(t, dir, `{"dependencies":{"fancy":"file:./deps/fancy"}}`)

	_, err := tsBlueprintImports(dir, "ts-bp")
	if err == nil {
		t.Fatal("an exports map must be refused rather than guessed")
	}
	if !strings.Contains(err.Error(), "exports") {
		t.Errorf("refusal should name the exports map: %s", err)
	}
}

// TestTSBlueprintImports_BothFormatsMerge: a blueprint may carry both,
// and deno.json wins a collision because it is the format Deno itself
// prefers and a blueprint carrying both has usually adopted it.
func TestTSBlueprintImports_BothFormatsMerge(t *testing.T) {
	dir := t.TempDir()
	writeNPMPackage(t, filepath.Join(dir, "deps", "shared"), "shared", "npm.js")
	writePackageJSON(t, dir, `{"dependencies":{"shared":"file:./deps/shared","onlynpm":"file:./deps/shared"}}`)
	if err := os.WriteFile(filepath.Join(dir, "viadeno.ts"), []byte("export const y = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTSBlueprintConfig(t, dir, `{"imports":{"shared":"./viadeno.ts","onlydeno":"./viadeno.ts"}}`)

	got, err := tsBlueprintImports(dir, "ts-bp")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got["shared"], "viadeno.ts") {
		t.Errorf("deno.json should win a collision, got %q", got["shared"])
	}
	if got["onlynpm"] == "" || got["onlydeno"] == "" {
		t.Errorf("entries unique to each format must both survive: %v", got)
	}
}

// TestTSBlueprintImports_NoPackageJSON keeps the common case free.
func TestTSBlueprintImports_NoPackageJSON(t *testing.T) {
	got, err := tsBlueprintImports(t.TempDir(), "ts-bp")
	if err != nil || got != nil {
		t.Fatalf("a blueprint with neither config should resolve nothing: %v, %v", got, err)
	}
}
