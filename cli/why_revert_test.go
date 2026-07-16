package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestWhy_ChainWithMixedKinds_DistinguishableAtAGlance extends
// TestWhy_ResourceAddress_ChainOrdering with a third entry -- a
// drift_revert, this time -- confirming a chain mixing all three kinds
// (adoption, drift_adopt, drift_revert) renders each recognizably without
// any cli/why.go rendering changes (docs/architecture.md's "ubx why and
// revert chains" claim): Kind prints verbatim in the compact chain view,
// and a drift_revert's real (non-zero) blast radius already reads
// differently from drift_adopt/adoption's always-zero one in the
// single-proposal view.
func TestWhy_ChainWithMixedKinds_DistinguishableAtAGlance(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	addr := "payments.fake_widget.widget-why-mixed"

	// adoption
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-why-mixed",
		"--lookup", `{"name":"widget-why-mixed","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	acceptOut, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}
	adoptID := mustExtractID(t, acceptOut)

	// drift_adopt
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-why-mixed",
		"--lookup", `{"name":"widget-why-mixed","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "drift.json"),
		"--no-attribution",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (drift): %v", err)
	}
	acceptOut2, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "drift.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (drift): %v", err)
	}
	driftAdoptID := mustExtractID(t, acceptOut2)

	// drift again, this time generating (and accepting) a drift_revert.
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-why-mixed",
		"--lookup", `{"name":"widget-why-mixed","tags":{"env":"canary"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "revert.json"),
		"--no-attribution",
		"--propose", "revert",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (revert): %v", err)
	}
	acceptOut3, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "revert.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (revert): %v", err)
	}
	revertID := mustExtractID(t, acceptOut3)

	whyOut, err := runUbx(t, env, "why", addr, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", addr, err, whyOut)
	}
	if !strings.Contains(whyOut, "3 proposal(s)") {
		t.Fatalf("expected a 3-proposal summary, got: %s", whyOut)
	}
	if !strings.Contains(whyOut, "adoption") || !strings.Contains(whyOut, "drift_adopt") || !strings.Contains(whyOut, "drift_revert") {
		t.Fatalf("expected all three kinds to appear, got: %s", whyOut)
	}

	// Newest first: revert, then drift_adopt, then adoption.
	revertIdx := strings.Index(whyOut, shortID(revertID))
	driftAdoptIdx := strings.Index(whyOut, shortID(driftAdoptID))
	adoptIdx := strings.Index(whyOut, shortID(adoptID))
	if revertIdx == -1 || driftAdoptIdx == -1 || adoptIdx == -1 {
		t.Fatalf("expected all three proposal ids to appear (short form), got: %s", whyOut)
	}
	if !(revertIdx < driftAdoptIdx && driftAdoptIdx < adoptIdx) {
		t.Fatalf("expected newest-first ordering (revert, drift_adopt, adoption), got: %s", whyOut)
	}

	// The single-proposal view's blast radius is the other distinguishing
	// signal: real for drift_revert, always-zero for drift_adopt/adoption.
	revertSingle, err := runUbx(t, env, "why", revertID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v", revertID, err)
	}
	if !strings.Contains(revertSingle, "blast radius: +0 ~1 -0") {
		t.Fatalf("expected a real (non-zero) blast radius for the drift_revert, got: %s", revertSingle)
	}

	driftAdoptSingle, err := runUbx(t, env, "why", driftAdoptID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v", driftAdoptID, err)
	}
	if !strings.Contains(driftAdoptSingle, "blast radius: +0 ~0 -0") {
		t.Fatalf("expected an all-zero blast radius for the drift_adopt, got: %s", driftAdoptSingle)
	}
}
