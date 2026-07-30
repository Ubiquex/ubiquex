package cli

import (
	"strings"
	"testing"
)

// TestStatus_Drift_RendersAttributeLevelDiff is UBI-61's own founder-test
// finding: "show WHAT drifted, not just that it drifted" -- status
// --drift used to print only a bare "drifted: <address>" label; it now
// renders the actual before/after attribute diff inline (core.
// DiffAttributes, already exported, fed from ScanResult's own new
// PreviousState field) plus a next: handoff naming the address's own
// --stack/--type/--name for `ubx scan --propose`.
func TestStatus_Drift_RendersAttributeLevelDiff(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-diff", `{"name":"widget-diff","tags":{"env":"prod"}}`, adoptEnv)

	driftEnv := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=drift=byhand"}
	out, err := runUbx(t, driftEnv, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary)
	requireExitCode(t, err, 1, out)

	if !strings.Contains(out, `tags.drift: (absent) -> "byhand"`) {
		t.Fatalf("expected the real attribute-level diff inline, got: %s", out)
	}
	if !strings.Contains(out, "next: ubx scan --propose both --stack payments --type fake_widget --name widget-diff") {
		t.Fatalf("expected a next: handoff naming the address's own flags, got: %s", out)
	}
}
