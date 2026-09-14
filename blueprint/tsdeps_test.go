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
