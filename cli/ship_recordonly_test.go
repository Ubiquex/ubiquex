package cli

import (
	"regexp"
	"strings"
	"testing"
)

var acceptedIDPattern = regexp.MustCompile(`accepted ([0-9a-f]{64}) \(stack`)

// mustExtractAcceptedID pulls the full proposal id from `ubx accept`'s
// own "accepted <id> (stack ...)" line.
func mustExtractAcceptedID(t *testing.T, acceptOut string) string {
	t.Helper()
	m := acceptedIDPattern.FindStringSubmatch(acceptOut)
	if m == nil {
		t.Fatalf("no \"accepted <id> (stack ...)\" line found in: %s", acceptOut)
	}
	return m[1]
}

// TestShip_AdoptionProposal_InlineAccept_RecordOnlySucceeds is UBI-49
// residual round 2 finding #4's own acceptance test: `ubx ship
// <adopt-hash>` used to accept a record-only proposal (correct -- its
// own resolution IS the acceptance) and then error "ship only executes
// drift_revert or change proposals" -- success rendered as failure. It
// now recognizes the kind and reports success instead of ever reaching
// executor.Ship at all.
func TestShip_AdoptionProposal_InlineAccept_RecordOnlySucceeds(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary, "--stack", "payments", "--type", "fake_widget", "--name", "widget-new",
		"--lookup", `{"name":"widget-new"}`,
		"--ledger-dir", ledgerDir, "--no-attribution",
	)
	requireExitCode(t, err, 1, scanOut)
	if !strings.Contains(scanOut, "New resource found") {
		t.Fatalf("expected a new-resource classification, got: %s", scanOut)
	}
	hash := mustExtractShipHash(t, scanOut)

	shipOut, err := runUbx(t, env, "ship", hash, "--ledger-dir", ledgerDir, "--yes")
	if err != nil {
		t.Fatalf("ubx ship <adoption-hash>: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "record-only, nothing to execute") || !strings.Contains(shipOut, "resolved") {
		t.Fatalf("expected the record-only success message, got: %s", shipOut)
	}

	whyOut, err := runUbx(t, env, "why", "payments.fake_widget.widget-new", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "adoption") {
		t.Fatalf("expected the adoption to really be recorded (accepted, not just previewed), got: %s", whyOut)
	}
}

// TestShip_DriftAdoptProposal_AlreadyAccepted_RecordOnlySucceeds is the
// OTHER branch of the same fix: a drift_adopt already accepted through
// the standalone `ubx accept` (not ship's own inline fallback) must also
// report success when shipped, not the old error -- ship.go's kind check
// runs identically whether p came from ledger.Read directly or from a
// fresh inline accept.
func TestShip_DriftAdoptProposal_AlreadyAccepted_RecordOnlySucceeds(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-drift", `{"name":"widget-drift","tags":{"env":"prod"}}`, adoptEnv)

	driftEnv := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=drift=byhand"}
	scanOut, err := runUbx(t, driftEnv, "scan",
		"--provider", fakeProviderBinary, "--stack", "payments", "--type", "fake_widget", "--name", "widget-drift",
		"--lookup", `{"name":"widget-drift","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir, "--no-attribution",
		"--out", ledgerDir+"/drift.json",
	)
	requireExitCode(t, err, 1, scanOut)
	acceptOut, err := runUbx(t, adoptEnv, "accept", ledgerDir+"/drift.json", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (drift_adopt): %v", err)
	}
	// accept.go's own "accepted <id> (stack ...)" line still prints the
	// full id (out of scope for this session's short-hash sweep, which
	// only touched the plan/ship/terminate flow -- see STATE.md) -- used
	// here to ship the id DIRECTLY, exercising the ledger.Read success
	// branch rather than the plan-store fallback (the accepted proposal
	// is no longer in the plan store at all, `ubx accept` already pruned
	// it).
	id := mustExtractAcceptedID(t, acceptOut)

	shipOut, err := runUbx(t, adoptEnv, "ship", id, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx ship <already-accepted drift_adopt id>: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "record-only, nothing to execute") || !strings.Contains(shipOut, "resolved") {
		t.Fatalf("expected the record-only success message, got: %s", shipOut)
	}
}
