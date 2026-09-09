package tseval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The evaluator's own import map used to REPLACE the project's, so a
// stack importing a published SDK failed with "not a dependency and not
// in import map" while the entry sat in its own deno.json (UBI-260).

func TestWriteMergedImportMap_KeepsTheProjectsOwnEntries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deno.json"),
		[]byte(`{"imports":{"@ubx/sdk-aws/":"npm:/@ubx/sdk-aws@3.0.1/"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	path, cleanup, err := writeMergedImportMap(dir, "/assets/runtime/src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	imports := readImports(t, path)
	if got := imports["@ubx/sdk-aws/"]; got != "npm:/@ubx/sdk-aws@3.0.1/" {
		t.Fatalf("the project's own entry was dropped, got %q", got)
	}
	if got := imports["@ubx/sdk"]; got != "/assets/runtime/src/index.ts" {
		t.Fatalf("the embedded runtime must still be mapped, got %q", got)
	}
}

// The embedded runtime wins. It ships inside the ubx binary so that
// evaluation works offline and so a program always evaluates against
// the runtime this binary shipped with, never a differently-versioned
// copy a project pinned.
func TestWriteMergedImportMap_RuntimeWinsOverAProjectRemap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deno.json"),
		[]byte(`{"imports":{"@ubx/sdk":"jsr:@ubx/sdk@0.0.1"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := writeMergedImportMap(dir, "/assets/runtime/src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if got := readImports(t, path)["@ubx/sdk"]; got != "/assets/runtime/src/index.ts" {
		t.Fatalf("a project remap of @ubx/sdk must not win, got %q", got)
	}
}

// The one thing a naive merge gets wrong: an import map's relative
// targets resolve against the MAP's own location, and the merged map
// lives in a temp directory, so copying them verbatim would silently
// repoint them at nothing.
func TestWriteMergedImportMap_RelativeTargetsBecomeAbsolute(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deno.json"),
		[]byte(`{"imports":{"vendored/":"./vendor/sdk/","one":"./vendor/one.ts"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := writeMergedImportMap(dir, "/assets/runtime/src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	imports := readImports(t, path)
	for spec, wantSuffix := range map[string]string{"vendored/": "/vendor/sdk/", "one": "/vendor/one.ts"} {
		got := imports[spec]
		if !strings.HasPrefix(got, "file://") {
			t.Errorf("%s: relative target must become an absolute file URL, got %q", spec, got)
		}
		if !strings.HasSuffix(got, wantSuffix) {
			t.Errorf("%s: got %q, want it to end in %q", spec, got, wantSuffix)
		}
	}
	// A trailing slash is meaningful in an import map: it makes the
	// entry a prefix mapping, and filepath.Join eats it.
	if !strings.HasSuffix(imports["vendored/"], "/") {
		t.Errorf("a prefix mapping lost its trailing slash: %q", imports["vendored/"])
	}
}

// A specifier that already carries a scheme is passed through untouched.
func TestWriteMergedImportMap_SchemedTargetsAreUntouched(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "deno.json"),
		[]byte(`{"imports":{"a":"npm:pkg@1","b":"jsr:@s/p@2","c":"https://example.com/m.ts"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path, cleanup, err := writeMergedImportMap(dir, "/rt.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	imports := readImports(t, path)
	for spec, want := range map[string]string{"a": "npm:pkg@1", "b": "jsr:@s/p@2", "c": "https://example.com/m.ts"} {
		if imports[spec] != want {
			t.Errorf("%s: got %q, want %q", spec, imports[spec], want)
		}
	}
}

// No project config at all is the ordinary case for a program that
// imports nothing but the runtime and relative paths, and it must keep
// working exactly as before.
func TestWriteMergedImportMap_NoProjectConfig(t *testing.T) {
	path, cleanup, err := writeMergedImportMap(t.TempDir(), "/rt.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	imports := readImports(t, path)
	if len(imports) != 1 || imports["@ubx/sdk"] != "/rt.ts" {
		t.Fatalf("expected the runtime-only map, got %v", imports)
	}
}

func readImports(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Imports map[string]string `json:"imports"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m.Imports
}
