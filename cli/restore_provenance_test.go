package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// A blueprint-produced create, the shape blueprint.ExpandCalls stamps and
// UBI-282 completed with the declaration. Written into the intent file
// directly rather than by running a real blueprint: what this test is
// about is whether restore CARRIES provenance, not where it comes from.
const restoreProvInitialIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create a from a blueprint at v1"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a-v1"},
     "sources": [{"kind": "blueprint", "ref": "ci-platform:sha256:aaa111",
                  "declaration": "oci://ghcr.io/ubx-blueprints/ci-platform:v1",
                  "declared_source": "oci://ghcr.io/ubx-blueprints/ci-platform:v1"}]}
  ]
}`

const restoreProvDestroyIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "destroy a"},
  "resources": [],
  "destroys": ["payments.fake_widget.a"]
}`

// Recreated from the SAME blueprint at a later version. Destroy-then-
// recreate rather than a second in-place modify, for the pre-existing
// fakeprovider reason restore_test.go's own fixture comment sets out.
const restoreProvRecreateIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "recreate a from the blueprint at v2"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a-v2"},
     "sources": [{"kind": "blueprint", "ref": "ci-platform:sha256:bbb222",
                  "declaration": "oci://ghcr.io/ubx-blueprints/ci-platform:v2",
                  "declared_source": "oci://ghcr.io/ubx-blueprints/ci-platform:v2"}]}
  ]
}`

// planDocWithSources reads a restore plan keeping the per-resource
// provenance restorePlanDoc does not model.
type planDocWithSources struct {
	Delta struct {
		Creates  []json.RawMessage `json:"creates"`
		Modifies []struct {
			Target  map[string]string `json:"target"`
			Sources []struct {
				Kind        string `json:"kind"`
				Ref         string `json:"ref"`
				Declaration string `json:"declaration"`
			} `json:"sources"`
		} `json:"modifies"`
	} `json:"delta"`
}

// TestRestore_CarriesProvenanceForward is the regression test for a bug
// that destroyed information rather than merely getting an answer wrong.
//
// restore rebuilds a target head's config through FoldStateAt and emitted
// every resource with no sources at all. A restore's modifies go through
// the resolver, which sets Provider unconditionally, so FoldSources read
// them as a hand-written re-declaration and CLEARED the blueprint
// provenance of every resource a restore touched. Creates lost it the same
// way, by carrying no sources key.
//
// So the one command whose entire purpose is putting a stack back the way
// it was silently destroyed the record of what had declared it, and unlike
// the config it rebuilds, nothing afterwards could reconstruct that.
func TestRestore_CarriesProvenanceForward(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// The head to restore to: a exists, declared by the blueprint at v1.
	targetHead := resolveAcceptShip(t, dir, ledgerDir, env, restoreProvInitialIntent, "bp-v1")

	// Then it moves to v2, via destroy-and-recreate.
	resolveAcceptShip(t, dir, ledgerDir, env, restoreProvDestroyIntent, "destroy-a", "--confirm-destroys")
	resolveAcceptShip(t, dir, ledgerDir, env, restoreProvRecreateIntent, "recreate-a-v2")

	restoreOut, err := runUbx(t, env, "restore", targetHead, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx restore: %v\noutput: %s", err, restoreOut)
	}
	restoreID := extractProposalHash(t, restoreOut)
	raw, err := readPlanFileForTest(t, ledgerDir, restoreID)
	if err != nil {
		t.Fatalf("read restore plan: %v", err)
	}
	var doc planDocWithSources
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse restore plan: %v\nraw: %s", err, raw)
	}

	if len(doc.Delta.Modifies) != 1 {
		t.Fatalf("expected exactly 1 modify (a, back to v1's config), got %d:\n%s", len(doc.Delta.Modifies), raw)
	}
	mod := doc.Delta.Modifies[0]
	if mod.Target["name"] != "a" {
		t.Fatalf("modify targets %q, want a", mod.Target["name"])
	}
	if len(mod.Sources) == 0 {
		t.Fatalf("the restored modify carries no provenance, so accepting it would record that nothing declares this resource:\n%s", raw)
	}
	// v1, not v2: provenance is folded from the SAME head the config is,
	// so the two describe one moment rather than two.
	if mod.Sources[0].Ref != "ci-platform:sha256:aaa111" {
		t.Errorf("provenance ref = %q, want the target head's own v1", mod.Sources[0].Ref)
	}
	if mod.Sources[0].Declaration != "oci://ghcr.io/ubx-blueprints/ci-platform:v1" {
		t.Errorf("declaration = %q, want v1's, carried forward from the target head", mod.Sources[0].Declaration)
	}
}

// TestRestore_HandWrittenStaysHandWritten: the fix must not invent
// provenance where the target head had none. Nil is a real answer.
func TestRestore_HandWrittenStaysHandWritten(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	targetHead := resolveAcceptShip(t, dir, ledgerDir, env, restoreInitialIntent, "batch1")
	resolveAcceptShip(t, dir, ledgerDir, env, restoreDestroyAIntent, "batch2-destroy-a", "--confirm-destroys")
	resolveAcceptShip(t, dir, ledgerDir, env, restoreRecreateAIntent, "batch2-recreate-a")

	restoreOut, err := runUbx(t, env, "restore", targetHead, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx restore: %v\noutput: %s", err, restoreOut)
	}
	restoreID := extractProposalHash(t, restoreOut)
	raw, err := readPlanFileForTest(t, ledgerDir, restoreID)
	if err != nil {
		t.Fatalf("read restore plan: %v", err)
	}
	if strings.Contains(string(raw), `"sources"`) && strings.Contains(string(raw), `"blueprint"`) {
		t.Fatalf("a restore of hand-written resources invented blueprint provenance:\n%s", raw)
	}
}
