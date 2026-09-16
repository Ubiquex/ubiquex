package blueprint

import (
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// TestApplyBlueprintRefs_CarriesTheDeclaration is the regression test for
// a bug found by walking the flow by hand.
//
// A stack that declares a blueprint and calls it from an SDK program got
// its provenance completed here, by a pass that knew a name and a content
// hash and nothing about what asked for those bytes. So `ubx why`
// reported "no declaration recorded -- resolved before ubx recorded one"
// on a proposal resolved minutes earlier.
//
// The HCL path was correct throughout, which is what made this hard to
// see: one calling path stamps through blueprintSource and the other
// completes a bare ref here, and only the first was taught about
// declarations.
func TestApplyBlueprintRefs_CarriesTheDeclaration(t *testing.T) {
	intent := &resolver.IntentFile{
		Stack: "payments",
		Resources: []resolver.ResourceIntent{{
			Type: "aws_sqs_queue", Name: "orders",
			Sources: []core.IntentSource{{Kind: "blueprint", Ref: "ci-platform"}},
		}},
	}
	found := map[string]BlueprintProvenance{"ci-platform": {
		Ref: "ci-platform:sha256:abc123",
		Dep: Declaration{
			Name:   "ci-platform",
			URL:    "git+https://github.com/ubiquex/bps.git#ref=v2.1.0&path=ci",
			Source: "https://github.com/ubiquex/bps.git",
			Ref:    "v2.1.0",
			Path:   "ci",
		},
	}}

	if err := applyBlueprintRefs(intent, found, "hint"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := intent.Resources[0].Sources[0]
	if got.Ref != "ci-platform:sha256:abc123" {
		t.Errorf("Ref = %q", got.Ref)
	}
	if got.Declaration != "git+https://github.com/ubiquex/bps.git#ref=v2.1.0&path=ci" {
		t.Errorf("Declaration = %q, want the table's own entry verbatim", got.Declaration)
	}
	if got.DeclaredSource != "https://github.com/ubiquex/bps.git" || got.DeclaredRev != "v2.1.0" || got.DeclaredPath != "ci" {
		t.Errorf("derived triple = %q / %q / %q", got.DeclaredSource, got.DeclaredRev, got.DeclaredPath)
	}
}

// TestApplyBlueprintRefs_DiscoveredRootHasNoDeclaration: a blueprint
// found by walking the module graph was never named in a blueprints
// table, so it has no table entry to quote.
//
// This is the same distinction the HCL path draws between a declared
// blueprint and an inline call, and inventing a declaration here would
// make a future table reconciliation restore an entry that never existed.
func TestApplyBlueprintRefs_DiscoveredRootHasNoDeclaration(t *testing.T) {
	intent := &resolver.IntentFile{
		Stack: "payments",
		Resources: []resolver.ResourceIntent{{
			Type: "aws_sqs_queue", Name: "orders",
			Sources: []core.IntentSource{{Kind: "blueprint", Ref: "local-bp"}},
		}},
	}
	found := map[string]BlueprintProvenance{"local-bp": {Ref: "local-bp:sha256:def456"}}

	if err := applyBlueprintRefs(intent, found, "hint"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := intent.Resources[0].Sources[0]
	if got.Ref != "local-bp:sha256:def456" {
		t.Errorf("Ref = %q", got.Ref)
	}
	if got.Declaration != "" || got.DeclaredSource != "" {
		t.Errorf("a discovered root gained a declaration it never had: %q / %q", got.Declaration, got.DeclaredSource)
	}
}

// TestBlueprintRefs_CarriesDepFromDeclaredRoots covers the other half of
// the route: the declaration has to survive from the resolved dependency
// into the map the stamping pass reads.
func TestBlueprintRefs_CarriesDepFromDeclaredRoots(t *testing.T) {
	refs := blueprintRefs([]BlueprintRoot{
		{Name: "ci-platform", Ref: "ci-platform:sha256:abc", Dep: Declaration{Name: "ci-platform", URL: "oci://ghcr.io/x/ci:v1"}},
		{Name: "local-bp", Ref: "local-bp:sha256:def"},
	})
	if refs["ci-platform"].Dep.URL != "oci://ghcr.io/x/ci:v1" {
		t.Errorf("a declared root's declaration was dropped: %+v", refs["ci-platform"])
	}
	if refs["local-bp"].Dep.URL != "" {
		t.Errorf("a discovered root gained a declaration: %+v", refs["local-bp"])
	}
	if !strings.HasPrefix(refs["local-bp"].Ref, "local-bp:sha256:") {
		t.Errorf("the ref itself was lost: %+v", refs["local-bp"])
	}
}
