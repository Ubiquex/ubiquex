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

// TestWriteMergedImportMap_RuntimeIsPinnedInEveryBlueprintScope is the
// one-runtime-instance invariant.
//
// This test used to assert the opposite, that a blueprint declaring no
// imports grows no scope at all, on the reasoning that an empty scope is
// clutter. That was right about the clutter and wrong about what a scope
// is for here. The runtime is not one of a blueprint's imports to be
// resolved, it is the single instance every module in the evaluation has
// to share, and pinning it per blueprint is what makes that true no
// matter what else is reachable from the blueprint's own directory.
//
// The collector resource() writes into is module state, so a second
// instance is a second collector with no active stack behind it. A
// blueprint that had its own copy failed with the runtime's own message:
//
//	Error: resource() called outside of an active stack() evaluation.
//
// So it is pinned for every blueprint with a directory, whether or not it
// declares anything today, because "declares nothing" is a fact about
// this version of a blueprint and the invariant is not.
func TestWriteMergedImportMap_RuntimeIsPinnedInEveryBlueprintScope(t *testing.T) {
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
	var doc struct {
		Imports map[string]string            `json:"imports"`
		Scopes  map[string]map[string]string `json:"scopes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	scope, ok := doc.Scopes["file:///bp/"]
	if !ok {
		t.Fatalf("a blueprint with a directory must get a scope: %s", data)
	}
	if scope[RuntimeSpecifier] != "/rt/index.ts" {
		t.Errorf("scope[%s] = %q, want the embedded runtime", RuntimeSpecifier, scope[RuntimeSpecifier])
	}
	// And the top level still carries it, for the consumer's own modules.
	if doc.Imports[RuntimeSpecifier] != "/rt/index.ts" {
		t.Errorf("imports[%s] = %q, want the embedded runtime", RuntimeSpecifier, doc.Imports[RuntimeSpecifier])
	}
}
