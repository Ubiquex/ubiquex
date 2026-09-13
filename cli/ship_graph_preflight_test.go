package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// A proposal whose delta cannot form a dependency graph cannot ship.
// executor.Ship discovered that only AFTER the signing moment, so the
// ledger kept an accepted proposal that could never be shipped and
// could never be retracted, an append-only store having no way to take
// one back (UBI-267).
//
// The same ordering was already fixed once for the provider route, and
// cli/ship.go's own comment there records why the window matters. This
// is the other thing ship needs before it can do anything.
func TestShip_DanglingDependency_RefusesBeforeAccepting(t *testing.T) {
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	ledgerDir := t.TempDir()

	// A plan whose one create depends on an address the proposal does
	// not carry. Written straight into the plan store, because the
	// resolver will not produce one and the point is what ship does when
	// it meets one.
	create, err := json.Marshal(map[string]interface{}{
		"stack":      "payments",
		"type":       "fake_widget",
		"name":       "queue",
		"provider":   map[string]interface{}{"source": "fake/widget", "version": "0.1.0"},
		"config":     map[string]interface{}{"name": "queue"},
		"depends_on": []string{"payments.fake_widget.dlq"},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal := map[string]interface{}{
		"schema_version": 2,
		"stack":          "payments",
		"parent":         "",
		"kind":           "change",
		"intent":         map[string]interface{}{"summary": "depends on something not here"},
		"delta":          map[string]interface{}{"creates": []json.RawMessage{create}},
		"resolution":     map[string]interface{}{"resolved_at": "2026-09-13T00:00:00Z"},
		"blast_radius":   map[string]interface{}{"creates": 1, "modifies": 0, "destroys": 0},
		"status":         "draft",
	}
	raw, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	var p core.Proposal
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = hash
	stored, err := json.MarshalIndent(&p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	planDir := filepath.Join(ledgerDir, ".ubx", "plans")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(planDir, hash+".json"), stored, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, env, "ship", hash,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes")
	if err == nil {
		t.Fatalf("a proposal with a dangling dependency shipped:\n%s", out)
	}
	combined := err.Error() + out

	// It refuses, and it says which resource and which dependency.
	if !strings.Contains(combined, "does not create, change or terminate") {
		t.Fatalf("refused for the wrong reason: %v\n%s", err, out)
	}
	for _, want := range []string{"payments.fake_widget.queue", "payments.fake_widget.dlq"} {
		if !strings.Contains(combined, want) {
			t.Errorf("the refusal does not name %s: %v\n%s", want, err, out)
		}
	}

	// And nothing was accepted. This is the half that matters: the
	// ledger must hold no record of a proposal that can never ship.
	if strings.Contains(out, "accepted") {
		t.Errorf("the proposal was accepted before the refusal:\n%s", out)
	}
	if entries, _ := os.ReadDir(filepath.Join(ledgerDir, "proposals")); len(entries) != 0 {
		t.Errorf("the ledger holds %d proposal(s) after a refused ship", len(entries))
	}
}
