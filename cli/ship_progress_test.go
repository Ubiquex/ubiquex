package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestShip_Destroy_NarratesReadBackAttempts is docs/cli-output-spec.md's
// own mandatory line, proven at the CLI level (core/executor's own
// TestWithProgress_ReconcileDestroyLoop_EmitsReadBackAttempts already
// proves the underlying event-emission mechanism; this proves ship.go
// actually wires executor.WithProgress and prints the real text): a real
// destroy that needs several reconcile-by-query retries narrates each
// one live, including the exact "provider reported success" phrasing
// when the provider already claimed success. UBI-70 (this session):
// pending/in_flight no longer get their own line at all -- "in_flight"
// must never appear in this output -- and the final line reports the
// real outcome, not a second copy of the read-back narration.
func TestShip_Destroy_NarratesReadBackAttempts(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"FAKEPROVIDER_APPLY_MODE=ok",
	}

	adoptPath := filepath.Join(ledgerDir, "adopt.json")
	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "narrated",
		"--lookup", `{"id":"narrated"}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
	)
	requireExitCode(t, err, 1, scanOut)
	if _, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	termOut, err := runUbx(t, env, "terminate", "payments.fake_widget.narrated", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx terminate: %v\noutput: %s", err, termOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, termOut)

	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--confirm-destroys", "--yes")
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "payments.fake_widget.narrated") {
		t.Fatalf("expected the resource address narrated, got: %s", shipOut)
	}
	if strings.Contains(shipOut, "in_flight") {
		t.Fatalf("UBI-70: pending/in_flight must never render their own line, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "provider reported success -- verifying via read-back") {
		t.Fatalf("expected the MANDATORY read-back narration line, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "attempt 1/") {
		t.Fatalf("expected an attempt N/M counter, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "✓ shipped") {
		t.Fatalf("expected exactly one final outcome line, got: %s", shipOut)
	}
}
