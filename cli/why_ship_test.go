package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestWhy_ShippedDriftRevert_ShowsApplyHistory is UBI-26 session 4's own
// regression guard for a real gap found live-testing against real cloud
// infrastructure: ubx why's single-proposal view didn't render anything
// about what ubx ship actually did -- only the decision to revert, never
// the evidence. Confirms the fix: a shipped drift_revert's "why" output
// includes its apply history (attempt number, outcome, per-resource
// transitions); a never-shipped drift_revert shows none at all.
func TestWhy_ShippedDriftRevert_ShowsApplyHistory(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-why-ship",
		"--lookup", `{"name":"widget-why-ship","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-why-ship",
		"--lookup", `{"name":"widget-why-ship","tags":{"env":"canary"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "revert.json"),
		"--no-attribution",
		"--propose", "revert",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (revert): %v", err)
	}
	acceptOut, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "revert.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (revert): %v", err)
	}
	revertID := mustExtractID(t, acceptOut)

	// Before shipping: no ship history at all.
	beforeShip, err := runUbx(t, env, "why", revertID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why (before ship): %v", err)
	}
	if strings.Contains(beforeShip, "ship history") {
		t.Fatalf("expected no ship history before shipping, got: %s", beforeShip)
	}

	if _, err := runUbx(t, env, "ship", revertID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx ship: %v", err)
	}

	afterShip, err := runUbx(t, env, "why", revertID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why (after ship): %v", err)
	}
	for _, want := range []string{"ship history:", "attempt 1: outcome=shipped", "payments.fake_widget.widget-why-ship", "shipped at"} {
		if !strings.Contains(afterShip, want) {
			t.Fatalf("expected why output to contain %q, got: %s", want, afterShip)
		}
	}
}

// TestWhy_JSON_IncludesApplies confirms --json's "applies" field mirrors
// the human view -- present (non-empty) for a shipped drift_revert.
func TestWhy_JSON_IncludesApplies(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-why-ship-json",
		"--lookup", `{"name":"widget-why-ship-json","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-why-ship-json",
		"--lookup", `{"name":"widget-why-ship-json","tags":{"env":"canary"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "revert.json"),
		"--no-attribution",
		"--propose", "revert",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (revert): %v", err)
	}
	acceptOut, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "revert.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (revert): %v", err)
	}
	revertID := mustExtractID(t, acceptOut)

	if _, err := runUbx(t, env, "ship", revertID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx ship: %v", err)
	}

	jsonOut, err := runUbx(t, env, "why", revertID, "--ledger-dir", ledgerDir, "--json")
	if err != nil {
		t.Fatalf("ubx why --json: %v", err)
	}
	if !strings.Contains(jsonOut, `"applies"`) || !strings.Contains(jsonOut, `"outcome": "applied"`) {
		t.Fatalf("expected --json output to include a non-empty \"applies\" array, got: %s", jsonOut)
	}
}
