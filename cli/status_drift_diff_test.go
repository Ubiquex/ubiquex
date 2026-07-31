package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
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
	// UBI-49 residual round 1: flag-free scan means the next: handoff no
	// longer needs to spell out --stack/--type/--name -- `ubx scan
	// --propose both` alone now resolves the stack from the cascade and
	// walks the whole fleet (this address included).
	if !strings.Contains(out, "next: ubx scan --propose both") {
		t.Fatalf("expected the simplified next: handoff, got: %s", out)
	}
	// This resource was only ever adopted once, with no drift accepted
	// yet -- no prior adopt exists to surface recorded attribution from,
	// so the "who:" line must be omitted entirely, never a placeholder
	// (UBI-49 residual round 1 finding #3's own reconciliation).
	if strings.Contains(out, "who:") {
		t.Fatalf("expected no who: line (no prior recorded attribution exists), got: %s", out)
	}
}

// TestStatus_Drift_SurfacesRecordedAttribution_FromPriorAdopt is
// TestStatus_Drift_RendersAttributeLevelDiff's own sibling for the OTHER
// half of finding #3's reconciliation: once a PRIOR drift has already
// been adopted (and so carries whatever CloudTrail attribution that
// scan --propose cycle recorded), a SECOND live drift on the same
// address surfaces that history as "who:", explicitly labeled as
// recorded rather than computed fresh for the drift being shown now.
func TestStatus_Drift_SurfacesRecordedAttribution_FromPriorAdopt(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-attr", `{"name":"widget-attr","tags":{"env":"prod"}}`, adoptEnv)

	// First drift, accepted as a drift_adopt -- attribution attempted
	// (and, with no real CloudTrail available in this hermetic test,
	// recorded as unattributed with a reason -- see
	// TestScan_AttributionDegradesGracefully_NoCredentials for that half;
	// this test instead hand-seeds a real attributed source directly so
	// lastRecordedAttribution has something concrete to find).
	firstDraftPath := ledgerDir + "/first-drift.json"
	firstDriftEnv := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=env=staging"}
	scanOut, err := runUbx(t, firstDriftEnv, "scan",
		"--provider", fakeProviderBinary, "--stack", "payments", "--type", "fake_widget", "--name", "widget-attr",
		"--lookup", `{"name":"widget-attr","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir, "--no-attribution",
		"--out", firstDraftPath,
	)
	requireExitCode(t, err, 1, scanOut)

	raw, err := os.ReadFile(firstDraftPath)
	if err != nil {
		t.Fatal(err)
	}
	var firstDraft core.Proposal
	if err := json.Unmarshal(raw, &firstDraft); err != nil {
		t.Fatal(err)
	}
	// Hand-seeds a real attributed source directly (rather than relying
	// on real CloudTrail credentials, unavailable in this hermetic test --
	// see TestScan_AttributionDegradesGracefully_NoCredentials for the
	// "no credentials" half already covered elsewhere) so
	// lastRecordedAttribution has something concrete to find.
	firstDraft.Intent.Sources = append(firstDraft.Intent.Sources, core.IntentSource{
		Kind: "cloudtrail", ActorARN: "arn:aws:iam::123456789012:user/roozbeh", EventName: "TagResource", EventTime: "2026-07-31T00:00:00Z",
	})
	edited, err := json.Marshal(firstDraft)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(firstDraftPath, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runUbx(t, adoptEnv, "accept", firstDraftPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (first drift_adopt): %v", err)
	}

	// Second, live drift -- status --drift must surface the FIRST drift's
	// own recorded attribution, explicitly as history, not compute
	// anything fresh.
	secondDriftEnv := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=drift=byhand"}
	out, err := runUbx(t, secondDriftEnv, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "who: arn:aws:iam::123456789012:user/roozbeh · TagResource · 2026-07-31T00:00:00Z (recorded at drift_adopt)") {
		t.Fatalf("expected the prior adopt's own recorded attribution, explicitly labeled as recorded history, got: %s", out)
	}
}
