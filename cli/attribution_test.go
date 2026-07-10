package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScan_AttributionDegradesGracefully_NoCredentials proves the CLI
// wiring for UBI-10 (cli/attribution.go's attributeDrift) never blocks
// `ubx scan` from completing, even when AWS CloudTrail is completely
// unreachable -- here, by making credential resolution fail before any
// network call is attempted at all (no static keys, no shared config/
// credentials files, no EC2 instance metadata, no SSO), the same
// "best-effort by construction" guarantee docs/schema.md's amendment
// requires. This stays hermetic (no real network I/O): with zero
// available credential sources, aws-sdk-go-v2's default credential chain
// fails synchronously when a request tries to sign, before dispatching
// anything.
func TestScan_AttributionDegradesGracefully_NoCredentials(t *testing.T) {
	// Blank every credential source the default chain would otherwise try.
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "ubx-test-nonexistent-profile")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "no-such-config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "no-such-credentials"))
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REGION", "us-east-1")

	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-attribution",
		"--lookup", `{"name":"widget-attribution","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	); err != nil {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	// Drift scan, deliberately WITHOUT --no-attribution -- this is the path
	// that tries (and, given the blanked credentials above, fails) real
	// CloudTrail attribution. It must still produce a normal drift_adopt
	// proposal, not an error.
	driftOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-attribution",
		"--lookup", `{"name":"widget-attribution","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "drift.json"),
	)
	if err != nil {
		t.Fatalf("ubx scan (drift) should succeed even with no CloudTrail access, got: %v\noutput: %s", err, driftOut)
	}
	if !strings.Contains(driftOut, "drifted:") {
		t.Fatalf("expected a 'drifted' classification, got: %s", driftOut)
	}

	b, err := os.ReadFile(filepath.Join(ledgerDir, "drift.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw := string(b)
	if !strings.Contains(raw, `"cloudtrail_unattributed"`) {
		t.Fatalf("expected a cloudtrail_unattributed source recording the failure, got: %s", raw)
	}
	if !strings.Contains(raw, `"reason": "not_logged"`) {
		t.Fatalf("expected reason not_logged (no credentials available), got: %s", raw)
	}
}
