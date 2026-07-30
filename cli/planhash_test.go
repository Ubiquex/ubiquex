package cli

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestShip_ShortHash_ResolvesUniquePrefix is UBI-49 finding #6's own
// acceptance test for the plan store's short-hash support
// (docs/cli-output-spec.md principle 3): whatever 12-char prefix `ubx
// plan`/`ubx scan --propose`/`ubx terminate` would print as their own
// "next: ubx ship <shorthash>" handoff must actually resolve.
func TestShip_ShortHash_ResolvesUniquePrefix(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "widget1",
				"op":   "create",
				"config": map[string]interface{}{
					"name": "widget1",
				},
			},
		},
	})

	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	fullHash := mustExtractPlanHash(t, planOut)
	short := shortHash(fullHash)
	if len(short) != 12 || !strings.HasPrefix(fullHash, short) {
		t.Fatalf("shortHash(%q) = %q, want a 12-char prefix", fullHash, short)
	}

	shipOut, err := runUbx(t, env, "ship", short, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx ship %s (short hash): %v\noutput: %s", short, err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: applied") {
		t.Fatalf("expected outcome: applied, got: %s", shipOut)
	}
}

// TestResolvePlanHash_AmbiguousPrefix_Errors is resolvePlanHash's own unit
// test for the multiple-match case -- exercised directly (not through a
// real hash collision, which isn't practical to engineer) by writing two
// plan files under fabricated ids that share a common prefix.
func TestResolvePlanHash_AmbiguousPrefix_Errors(t *testing.T) {
	ledgerDir := t.TempDir()
	p := &core.Proposal{SchemaVersion: core.SchemaVersion, Stack: "payments"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writePlanFile(ledgerDir, "abc123one", data); err != nil {
		t.Fatal(err)
	}
	if _, err := writePlanFile(ledgerDir, "abc123two", data); err != nil {
		t.Fatal(err)
	}

	_, _, err = resolvePlanHash(ledgerDir, "abc123")
	if err == nil {
		t.Fatal("expected an ambiguous-prefix error")
	}
	if !errors.Is(err, ErrPlanAmbiguous) {
		t.Fatalf("err = %v, want ErrPlanAmbiguous", err)
	}
	if !strings.Contains(err.Error(), "abc123one") || !strings.Contains(err.Error(), "abc123two") {
		t.Fatalf("err = %v, want it to name both candidates", err)
	}
}

// TestResolvePlanHash_NotFound_ErrorsCleanly proves the zero-match case
// (no plan store directory at all, and a prefix that matches nothing once
// one exists) reports ErrPlanNotFound rather than a raw stat/read error.
func TestResolvePlanHash_NotFound_ErrorsCleanly(t *testing.T) {
	ledgerDir := t.TempDir()
	if _, _, err := resolvePlanHash(ledgerDir, "nosuchhash"); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("err = %v, want ErrPlanNotFound (no .ubx/plans/ at all)", err)
	}

	p := &core.Proposal{SchemaVersion: core.SchemaVersion, Stack: "payments"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writePlanFile(ledgerDir, "abcdef123456", data); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolvePlanHash(ledgerDir, "zzz"); !errors.Is(err, ErrPlanNotFound) {
		t.Fatalf("err = %v, want ErrPlanNotFound (prefix matches nothing)", err)
	}
}
