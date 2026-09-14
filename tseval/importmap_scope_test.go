package tseval

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestWriteMergedImportMap_BlueprintScope covers the shape scopes buy:
// a blueprint's own imports apply to the blueprint's code and nowhere
// else.
//
// A top level entry would be one namespace shared by everything
// evaluated, so a blueprint and the stack calling it would have to agree
// on every specifier they both use and whichever ubx merged last would
// silently win for both. Python cannot avoid that, since PYTHONPATH is
// one flat search order. Deno can, and taking it from the start beats
// retrofitting it once someone hits the collision.
func TestWriteMergedImportMap_BlueprintScope(t *testing.T) {
	entryDir := t.TempDir()
	bpDir := t.TempDir()

	path, cleanup, err := writeMergedImportMap(entryDir, "/rt/index.ts", []BlueprintImport{{
		Specifier: "widget-bp",
		EntryFile: bpDir + "/blueprint.ts",
		Dir:       bpDir,
		Imports:   map[string]string{"helper": "file://" + bpDir + "/helper.ts"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Imports map[string]string            `json:"imports"`
		Scopes  map[string]map[string]string `json:"scopes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}

	// The blueprint itself is reachable by its bare name from anywhere.
	if doc.Imports["widget-bp"] == "" {
		t.Fatalf("the blueprint's own specifier should be a top level import: %s", data)
	}
	// Its dependency is NOT, so the consumer cannot accidentally resolve
	// a specifier it never declared.
	if _, leaked := doc.Imports["helper"]; leaked {
		t.Fatalf("a blueprint's own dependency must not reach the top level map: %s", data)
	}
	if len(doc.Scopes) != 1 {
		t.Fatalf("expected exactly one scope, got %d: %s", len(doc.Scopes), data)
	}
	for prefix, scoped := range doc.Scopes {
		// A scope prefix is matched by string against a module URL, so a
		// missing trailing slash would also match a sibling directory
		// whose name merely starts the same way.
		if !strings.HasSuffix(prefix, "/") {
			t.Errorf("scope prefix %q must end in a slash", prefix)
		}
		if !strings.HasPrefix(prefix, "file://") {
			t.Errorf("scope prefix %q should be a file URL", prefix)
		}
		if scoped["helper"] == "" {
			t.Errorf("the blueprint's dependency is missing from its scope: %v", scoped)
		}
	}
	// The runtime still wins, for the reason it always has.
	if doc.Imports["@ubx/sdk"] != "/rt/index.ts" {
		t.Errorf("@ubx/sdk = %q, want the embedded runtime", doc.Imports["@ubx/sdk"])
	}
}

// TestWriteMergedImportMap_NoScopesWhenNothingDeclares keeps the common
// case clean: a built blueprint declares no imports of its own, and the
// map should not grow an empty scopes object for it.
func TestWriteMergedImportMap_NoScopesWhenNothingDeclares(t *testing.T) {
	path, cleanup, err := writeMergedImportMap(t.TempDir(), "/rt/index.ts", []BlueprintImport{{
		Specifier: "built-bp",
		EntryFile: "/bp/ts/built.ts",
		Dir:       "/bp",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "scopes") {
		t.Fatalf("no blueprint declared imports, so the map should carry no scopes: %s", data)
	}
}
