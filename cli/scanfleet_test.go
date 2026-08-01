package cli

import (
	"strings"
	"testing"
)

// TestScanFleet_WalksMultipleAddresses_EachGetsItsOwnCard is UBI-49
// residual round 1's own headline acceptance test: bare `ubx scan
// --propose <mode>` with a stack resolved but no --type/--name walks
// every address the ledger already tracks (core.Ledger.Fleet), exactly
// like `ubx status` already does -- each drifted address gets its own
// card and its own saved, ship-able plan (fakeprovider's own
// FAKEPROVIDER_EXTRA_TAG knob is a whole-process fixture setting, not
// per-resource, so both addresses drift together here;
// TestScanFleet_AllClean_ExitZero is this test's own "nothing drifted"
// sibling, proving the opposite case doesn't just always report drift).
func TestScanFleet_WalksMultipleAddresses_EachGetsItsOwnCard(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-a", `{"name":"widget-a","tags":{"env":"prod"}}`, adoptEnv)
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-b", `{"name":"widget-b","tags":{"env":"prod"}}`, adoptEnv)

	driftEnv := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=drift=byhand"}
	out, err := runUbx(t, driftEnv, "scan", "--stack", "payments", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--no-attribution")
	requireExitCode(t, err, 1, out)

	if !strings.Contains(out, "payments.fake_widget.widget-a") || !strings.Contains(out, "payments.fake_widget.widget-b") {
		t.Fatalf("expected a card for each drifted address, got: %s", out)
	}
	// UBI-71 part 2: scan's own card renders a compact one-line diff
	// summary now, not the full before/after attribute diff restated
	// verbatim (that's status --drift's own job) -- one summary line per
	// drifted address here.
	if strings.Count(out, "attribute(s) changed: tags.drift") != 2 {
		t.Fatalf("expected the compact diff summary on each of the two cards, got: %s", out)
	}
	if !strings.Contains(out, "2 resource(s) scanned, 2 drifted, 2 proposal(s) saved") {
		t.Fatalf("expected the aggregate summary line, got: %s", out)
	}

	hash := mustExtractShipHash(t, out)
	if _, _, err := resolvePlanHash(ledgerDir, hash); err != nil {
		t.Fatalf("expected the card's own hash %s to resolve in the plan store: %v", hash, err)
	}
}

// TestScanFleet_AllClean_ExitZero proves a fleet with no drift at all
// reports cleanly (exit 0), not as if nothing happened -- the same
// "make the content visible" posture as a single-resource clean scan.
func TestScanFleet_AllClean_ExitZero(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-clean", `{"name":"widget-clean","tags":{"env":"prod"}}`, env)

	out, err := runUbx(t, env, "scan", "--stack", "payments", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("expected exit 0 for an all-clean fleet, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "no drift: all 1 resource(s) in stack payments match the ledger") {
		t.Fatalf("expected the all-clean summary line, got: %s", out)
	}
}

// TestScanFleet_EmptyLedger_ReportsCleanly is
// TestScan_StackKnown_NeitherTypeNorNameGiven_WalksFleet_EmptyLedger's
// own more thorough sibling, run from this file since it's the
// fleet-walk's own dedicated home.
func TestScanFleet_EmptyLedger_ReportsCleanly(t *testing.T) {
	out, err := runUbx(t, nil, "scan", "--stack", "payments", "--ledger-dir", t.TempDir())
	if err != nil {
		t.Fatalf("expected exit 0 for an empty fleet, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 resource(s) tracked for stack payments") {
		t.Fatalf("expected the empty-fleet report, got: %s", out)
	}
}

// TestScanFleet_JSON_Rejected / Out / SurfaceAs prove the fleet walk
// refuses single-resource-only flags rather than silently misbehaving --
// a fleet's own output is N independent cards, never one JSON document
// or a single --out file.
func TestScanFleet_JSON_Rejected(t *testing.T) {
	_, err := runUbx(t, nil, "scan", "--stack", "payments", "--json", "--ledger-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected --json to be rejected for a fleet-scoped scan")
	}
	if !strings.Contains(err.Error(), "--json is only supported for a single-resource scan") {
		t.Fatalf("expected a teaching error naming the single-resource requirement, got: %v", err)
	}
}

func TestScanFleet_Out_Rejected(t *testing.T) {
	_, err := runUbx(t, nil, "scan", "--stack", "payments", "--out", "x.json", "--ledger-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected --out to be rejected for a fleet-scoped scan")
	}
	if !strings.Contains(err.Error(), "--out/--out-dir are only supported for a single-resource scan") {
		t.Fatalf("expected a teaching error naming the single-resource requirement, got: %v", err)
	}
}

func TestScanFleet_SurfaceAs_Rejected(t *testing.T) {
	_, err := runUbx(t, nil, "scan", "--stack", "payments", "--surface-as", "issue", "--github-repo", "acme/infra", "--ledger-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected --surface-as to be rejected for a fleet-scoped scan")
	}
	if !strings.Contains(err.Error(), "--surface-as is only supported for a single-resource scan") {
		t.Fatalf("expected a teaching error naming the single-resource requirement, got: %v", err)
	}
}

// TestScanFleet_ShipShortHash_EndToEnd proves the fleet walk's own saved
// proposal is exactly as ship-able as a single-resource scan's -- the
// whole point of routing through the same plan store.
func TestScanFleet_ShipShortHash_EndToEnd(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-drifted", `{"name":"widget-drifted","tags":{"env":"prod"}}`, adoptEnv)

	driftEnv := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=drift=byhand"}
	scanOut, err := runUbx(t, driftEnv, "scan", "--stack", "payments", "--propose", "revert", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--no-attribution")
	requireExitCode(t, err, 1, scanOut)
	hash := mustExtractShipHash(t, scanOut)

	shipOut, err := runUbx(t, driftEnv, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes")
	if err != nil {
		t.Fatalf("ubx ship %s: %v\noutput: %s", hash, err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: applied") {
		t.Fatalf("expected outcome: applied, got: %s", shipOut)
	}
}

// TestScanFleet_MultiProviderConfig_NoProviderFlagsNeeded proves the
// fleet walk resolves each tracked address's own provider through the
// modern [providers] map (declaredProvidersForInference/InferProvider,
// the same mechanism status.go's own multi-provider fleet walk already
// uses) rather than demanding --provider/--source on top of an already-
// configured stack.
func TestScanFleet_MultiProviderConfig_NoProviderFlagsNeeded(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()
	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[providers]
"fake/widget" = "0.1.0"
`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6", "UBX_PROVIDER_MIRROR=" + mirrorDir}
	adoptViaCLI(t, dir, "payments", "fake_widget", "widget-drifted", `{"name":"widget-drifted","tags":{"env":"prod"}}`, env)

	driftEnv := append(append([]string{}, env...), "FAKEPROVIDER_EXTRA_TAG=drift=byhand")
	out, err := runUbx(t, driftEnv, "scan", "--stack", "payments", "--ledger-dir", dir, "--no-attribution")
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "1 resource(s) scanned, 1 drifted, 1 proposal(s) saved") {
		t.Fatalf("expected the aggregate summary line, got: %s", out)
	}
}
