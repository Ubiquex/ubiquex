package cli

import (
	"strings"
	"testing"
)

// TestStatus_TTY_Drift_ClosingSummary_StyledByOutcome is UBI-76's own
// regression guard: `ubx status --drift`'s closing summary line needs a
// blank line before it, bold weight throughout, and -- the affirmative-
// success convention already established for `ubx init` (UBI-59) -- the
// WHOLE line green when the run is fully clean; yellow/red per-count
// otherwise (matching UBI-75's own per-count color scheme for ship's
// closing summary). Verified against the real ANSI codes (not just
// plain-text substrings), TTY-forced, the same technique
// TestStatus_TTY_DriftLines_AddressBrightMetadataDim already established.
func TestStatus_TTY_Drift_ClosingSummary_StyledByOutcome(t *testing.T) {
	adoptEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}

	cleanDir := t.TempDir()
	adoptViaCLI(t, cleanDir, "payments", "fake_widget", "widget-summary-clean", `{"name":"widget-summary-clean","tags":{"env":"prod"}}`, adoptEnv)
	cleanOut, err := runUbxTTY(t, "", []string{"FAKEPROVIDER_MODE=ok-v6", "NO_COLOR="}, "status", "--ledger-dir", cleanDir, "--drift", "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx status --drift (clean): %v\noutput: %s", err, cleanOut)
	}
	wantCleanSummary := ansiBold + ansiGreen + "1 resource(s), 0 drifted, 0 unreadable" + ansiReset
	if !strings.Contains(cleanOut, "\n\n"+wantCleanSummary) {
		t.Fatalf("expected a blank line then the bold-green closing summary %q, got: %q", wantCleanSummary, cleanOut)
	}

	driftDir := t.TempDir()
	adoptViaCLI(t, driftDir, "payments", "fake_widget", "widget-summary-drifted", `{"name":"widget-summary-drifted","tags":{"env":"prod"}}`, adoptEnv)
	driftOut, err := runUbxTTY(t, "", []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=mutated=yes", "NO_COLOR="}, "status", "--ledger-dir", driftDir, "--drift", "--provider", fakeProviderBinary)
	if err == nil {
		t.Fatal("expected exit code 1 for a drifted resource")
	}
	// Bold throughout (reasserted after every embedded reset, forceBold's
	// own technique), drifted count yellow, unreadable count red.
	if !strings.Contains(driftOut, "\n\n"+ansiBold+"1 resource(s), "+ansiYellow+"1 drifted"+ansiReset+ansiBold+", "+ansiRed+"0 unreadable"+ansiReset+ansiBold+ansiReset) {
		t.Fatalf("expected a blank line then the bold+yellow/red closing summary, got: %q", driftOut)
	}
}

// TestScanFleet_TTY_ClosingSummary_StyledByOutcome is status --drift's own
// sibling for `ubx scan --stack`'s closing summary (UBI-76: "same
// spacing/weight/color principle likely applies to scan's closing summary
// line too").
func TestScanFleet_TTY_ClosingSummary_StyledByOutcome(t *testing.T) {
	adoptEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}

	cleanDir := t.TempDir()
	adoptViaCLI(t, cleanDir, "payments", "fake_widget", "widget-fleet-clean", `{"name":"widget-fleet-clean","tags":{"env":"prod"}}`, adoptEnv)
	cleanOut, err := runUbxTTY(t, "", []string{"FAKEPROVIDER_MODE=ok-v6", "NO_COLOR="}, "scan", "--ledger-dir", cleanDir, "--stack", "payments", "--provider", fakeProviderBinary, "--no-attribution")
	if err != nil {
		t.Fatalf("ubx scan --stack (clean): %v\noutput: %s", err, cleanOut)
	}
	wantCleanSummary := ansiBold + ansiGreen + "no drift: all 1 resource(s) in stack payments match the ledger" + ansiReset
	if !strings.HasPrefix(cleanOut, "\n"+wantCleanSummary) {
		t.Fatalf("expected a leading blank line then the bold-green closing summary %q, got: %q", wantCleanSummary, cleanOut)
	}

	driftDir := t.TempDir()
	adoptViaCLI(t, driftDir, "payments", "fake_widget", "widget-fleet-drifted", `{"name":"widget-fleet-drifted","tags":{"env":"prod"}}`, adoptEnv)
	driftOut, err := runUbxTTY(t, "", []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=mutated=yes", "NO_COLOR="}, "scan", "--ledger-dir", driftDir, "--stack", "payments", "--provider", fakeProviderBinary, "--no-attribution")
	if err == nil {
		t.Fatal("expected a non-zero exit for a drifted fleet scan")
	}
	if !strings.Contains(driftOut, "\n\n"+ansiBold+"1 resource(s) scanned, "+ansiYellow+"1 drifted"+ansiReset+ansiBold) {
		t.Fatalf("expected a blank line then the bold+yellow closing summary, got: %q", driftOut)
	}
}
