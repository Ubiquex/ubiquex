package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestScan_Card_SurfacesUnattributedReason is UBI-49 residual #5's own
// correction: the wire layer already recorded WHY attribution came back
// empty (a cloudtrail_unattributed source, complete with its own
// Reason) -- the actual gap was display-only, scan's own card silently
// showed nothing either way. Reuses
// TestScan_AttributionDegradesGracefully_NoCredentials' exact
// credential-blanking setup (a real, hermetic way to produce a genuine
// "not_logged" reason with no live AWS account involved).
func TestScan_Card_SurfacesUnattributedReason(t *testing.T) {
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
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-attr-card", `{"name":"widget-attr-card","tags":{"env":"prod"}}`, env)

	// Deliberately WITHOUT --no-attribution -- this is the path that
	// tries (and, given the blanked credentials above, fails) real
	// CloudTrail attribution.
	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary, "--stack", "payments", "--type", "fake_widget", "--name", "widget-attr-card",
		"--lookup", `{"name":"widget-attr-card","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "attribution: none found yet (CloudTrail visibility unavailable (denied access or not logged)) -- re-scan to attribute") {
		t.Fatalf("expected the card to surface the recorded unattributed reason, got: %s", out)
	}
}

// TestBlame_SurfacesUnattributedReason is blame's own half of the same
// correction -- previously silently omitted the line entirely for a
// group whose own AttributedActors came back empty, indistinguishable
// from "attribution was never attempted at all."
func TestBlame_SurfacesUnattributedReason(t *testing.T) {
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
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-attr-blame", `{"name":"widget-attr-blame","tags":{"env":"prod"}}`, env)

	driftPath := filepath.Join(ledgerDir, "drift.json")
	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary, "--stack", "payments", "--type", "fake_widget", "--name", "widget-attr-blame",
		"--lookup", `{"name":"widget-attr-blame","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir, "--out", driftPath,
	)
	requireExitCode(t, err, 1, scanOut)
	if _, err := runUbx(t, env, "accept", driftPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (drift_adopt): %v", err)
	}

	out, err := runUbx(t, nil, "blame", "payments.fake_widget.widget-attr-blame", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx blame: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "unattributed (CloudTrail visibility unavailable (denied access or not logged))") {
		t.Fatalf("expected blame to surface the recorded unattributed reason, got: %s", out)
	}
}

// TestShip_WarnsBeforeAcceptingRecentUnattributedAdopt is UBI-49 residual
// #5's own one behavioral addition: a drift resolved (scanned) less than
// 10 minutes ago, with no recorded attribution, gets a warning before
// ship's own confirmation prompt -- accepting it now permanently records
// it unattributed, and it may simply be too soon for CloudTrail's own
// delivery window. Never blocking: the plan still ships with --yes.
func TestShip_WarnsBeforeAcceptingRecentUnattributedAdopt(t *testing.T) {
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
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-attr-warn", `{"name":"widget-attr-warn","tags":{"env":"prod"}}`, env)

	// No --out here -- scan --propose saves straight to the plan store,
	// exactly the path `ubx ship`'s own inline accept consumes.
	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary, "--stack", "payments", "--type", "fake_widget", "--name", "widget-attr-warn",
		"--lookup", `{"name":"widget-attr-warn","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 1, scanOut)
	hash := mustExtractShipHash(t, scanOut)

	shipOut, err := runUbx(t, env, "ship", hash, "--ledger-dir", ledgerDir, "--yes")
	if err != nil {
		t.Fatalf("ubx ship %s --yes: %v\noutput: %s", hash, err, shipOut)
	}
	if !strings.Contains(shipOut, "warning:") || !strings.Contains(shipOut, "has no recorded attribution") {
		t.Fatalf("expected the recent-unattributed-adopt warning, got: %s", shipOut)
	}
	// A drift_adopt is record-only (UBI-49 residual round 2 finding #4) --
	// the warning is advisory only, never blocking, so acceptance still
	// goes through to the same record-only success this session's other
	// fix reports (not "outcome: applied," which only ever prints for a
	// real executor.Ship run of a drift_revert/change proposal).
	if !strings.Contains(shipOut, "record-only, nothing to execute") {
		t.Fatalf("expected the warning to be advisory only -- still accepted, got: %s", shipOut)
	}
}
