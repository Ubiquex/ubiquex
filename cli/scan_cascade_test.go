package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestScan_MissingFlagsError_MentionsDiscover is UBI-49 finding #3's own
// "the error's mode list is stale -- it omits --discover entirely" fix:
// the enumerated alternatives now name every real scan mode.
func TestScan_MissingFlagsError_MentionsDiscover(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, "")

	_, err := runUbx(t, nil, "scan", "--ledger-dir", dir)
	if err == nil {
		t.Fatal("expected an error with no --stack/--type/--name and no --all/--discover")
	}
	if !strings.Contains(err.Error(), "--discover") {
		t.Errorf("error = %v, want it to mention --discover as an alternative mode", err)
	}
	if !strings.Contains(err.Error(), "--all") {
		t.Errorf("error = %v, want it to still mention --all as an alternative mode", err)
	}
}

// TestScan_UsesLedgerRecordedLookup_WhenFlagNotGiven is UBI-49 finding
// #5's own acceptance test: an already-tracked address's recorded lookup
// (core.Ledger.LastLookup, the same source status.go's fleet walk already
// reads) is now consulted when --lookup isn't given, instead of always
// defaulting to "{}" -- fixing the asymmetry the finding describes
// (status --drift could see this resource's drift; per-resource scan
// went blind on the identical address). Mirrors TestScanAcceptWhy's own
// adopt-then-drift-then-accept setup, which already establishes that this
// fixture's ReadResource is a pure echo of whatever lookup it's given --
// scanning with the SAME lookup the ledger already recorded is the only
// way to observe "no drift" from it, exactly what --lookup omitted must
// now reproduce on its own.
func TestScan_UsesLedgerRecordedLookup_WhenFlagNotGiven(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget1",
		"--lookup", `{"name":"widget1","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	requireExitCode(t, err, 1, scanOut)

	acceptOut, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (adopt): %v\noutput: %s", err, acceptOut)
	}

	// Drift the fixture's own echoed state (a different lookup stands in
	// for "reality changed", per TestScanAcceptWhy's own established
	// convention), then accept the drift_adopt so the ledger's own
	// recorded lookup for this address becomes {"name":"widget1",
	// "tags":{"env":"staging"}}.
	scanOut2, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget1",
		"--lookup", `{"name":"widget1","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "drift.json"),
		"--no-attribution",
	)
	requireExitCode(t, err, 1, scanOut2)

	acceptOut2, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "drift.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (drift): %v\noutput: %s", err, acceptOut2)
	}

	// The fix under test: scan the SAME address with NO --lookup flag at
	// all. Before this session, --lookup defaulted to the literal "{}",
	// which this fixture would echo back with only "id" filled in -- a
	// different observed state than the ledger's own recorded
	// {"name":"widget1","tags":{"env":"staging"}}, so it would have
	// wrongly reported drift. With the fix, ledger.LastLookup supplies the
	// real recorded lookup automatically.
	scanOut3, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget1",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx scan with no --lookup flag on a tracked address: %v\noutput: %s", err, scanOut3)
	}
	if !strings.Contains(scanOut3, "no drift") {
		t.Fatalf("expected 'no drift' once the ledger's own recorded lookup is used automatically, got: %s", scanOut3)
	}
}

// TestScan_MultiProviderConfig_NoProviderFlagsNeeded is UBI-49 finding
// #4's own acceptance test: a stack whose only provider declaration is a
// real [providers] table (no [provider]/--provider/--source at all) can
// still run `ubx scan --type --name` -- previously "either --provider or
// --source (with --provider-version) is required" even though the exact
// same config already worked for `ubx scan --all`/`--discover`/`ubx ship`/
// `ubx status --drift`.
func TestScan_MultiProviderConfig_NoProviderFlagsNeeded(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "playground"

[providers]
"fake/widget" = "0.1.0"
`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6", "UBX_PROVIDER_MIRROR=" + mirrorDir}
	out, err := runUbx(t, env, "scan",
		"--type", "fake_widget",
		"--name", "widget1",
		"--lookup", `{"name":"widget1"}`,
		"--ledger-dir", dir,
	)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "New resource found") {
		t.Fatalf("expected a 'new' classification with zero --provider/--source flags, got: %s", out)
	}
}
