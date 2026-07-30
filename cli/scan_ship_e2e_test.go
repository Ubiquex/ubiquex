package cli

import (
	"strings"
	"testing"
)

// TestScanPropose_ShipShortHash_ZeroManualFileHandling is UBI-49 finding
// #6's own end-to-end acceptance test: "scan --propose -> ship
// <short-hash> end-to-end with zero manual file handling." Before this
// session, scan --propose printed the proposal's own JSON to the
// terminal and saved nothing -- resolving drift meant hand-copying that
// JSON into a file for `ubx accept`. Now the generated proposal is saved
// to the plan store automatically and the card's own hash ships directly.
func TestScanPropose_ShipShortHash_ZeroManualFileHandling(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// A never-seen resource always generates an adoption proposal --
	// no --out, no hand-authored file, nothing but the CLI's own stdout.
	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-e2e",
		"--lookup", `{"name":"widget-e2e","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 1, scanOut)
	if !strings.Contains(scanOut, "saved to plan store") {
		t.Fatalf("expected scan --propose to report saving to the plan store, got: %s", scanOut)
	}
	hash := mustExtractShipHash(t, scanOut)

	// Adoption proposals are accept-only (nothing to actually apply), so
	// this is ubx accept's own plan-store fallback (finding #6's other
	// half) -- not ubx ship, which only handles drift_revert/change.
	acceptOut, err := runUbx(t, env, "accept", hash, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept %s (plan-store hash, no file path involved): %v\noutput: %s", hash, err, acceptOut)
	}
}

// TestScanPropose_Revert_ShipShortHash_AppliesAgainstLedger proves the
// same zero-manual-file-handling flow end to end through `ubx ship`
// itself, for a drift_revert proposal (a kind ship actually applies).
func TestScanPropose_Revert_ShipShortHash_AppliesAgainstLedger(t *testing.T) {
	ledgerDir, out := adoptThenDrift(t, "--propose", "revert")
	hash := mustExtractShipHash(t, out)

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx ship %s (plan-store short hash from scan --propose): %v\noutput: %s", hash, err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: applied") {
		t.Fatalf("expected outcome: applied, got: %s", shipOut)
	}
}
