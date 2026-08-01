package cli

import (
	"path/filepath"
	"testing"
)

// TestShip_ShortHash_AlreadyAcceptedProposal_ResolvesAgainstLedger is
// UBI-63 session 5's own hermetic repro of a real, live bug found during
// the terminate/destroy live verification: an already-accepted proposal
// (its plan file pruned from .ubx/plans/ at accept time, per `ubx
// accept`'s own doc comment) could not be shipped by its short hash --
// `ubx ship <short-hash>` refused with "no matching plan in the plan
// store," even though ship's own doc comment already promises a
// short-hash-or-full ref is "looked up two ways, in order: first as an
// already-accepted proposal id... if not found there, as a plan." Root
// cause: ledger.Read only ever did an exact-ID lookup, so a short hash
// that would have resolved fine against the plan store's own prefix
// matching (resolvePlanHash) never got the chance against the ledger,
// which has no such matching at all until resolveAcceptedProposal
// (cli/plan.go).
func TestShip_ShortHash_AlreadyAcceptedProposal_ResolvesAgainstLedger(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	addr := "payments.fake_widget.old-widget"

	adoptOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "old-widget",
		"--lookup", `{"id":"old-widget"}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	if exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v\noutput: %s", err, adoptOut)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	termOut, err := runUbx(t, env, "terminate", addr, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx terminate: %v\noutput: %s", err, termOut)
	}
	planHash := mustExtractPlanHash(t, ledgerDir, termOut)

	// Accept the destroy SEPARATELY (not via ship's own inline-accept
	// fallback) -- this is what prunes the plan file, exactly the
	// "already-accepted, no plan file left" state the real bug needs.
	acceptOut, err := runUbx(t, env, "accept", planHash, "--ledger-dir", ledgerDir, "--confirm-destroys")
	if err != nil {
		t.Fatalf("ubx accept (destroy): %v\noutput: %s", err, acceptOut)
	}
	fullID := mustExtractID(t, acceptOut)
	if _, statErr := readPlanFile(ledgerDir, planHash); statErr == nil {
		t.Fatalf("expected the plan file to be pruned after accept, but it still reads back")
	}

	// THE bug: ship this already-accepted proposal by a short hash, not
	// its full 64-char ID.
	shortHash := fullID[:12]
	shipOut, err := runUbx(t, env, "ship", shortHash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx ship %s: expected success, got err=%v\noutput: %s\n\n"+
			"this is the exact bug: an already-accepted proposal's plan file is pruned, "+
			"and ledger.Read never supported short-hash prefix matching", shortHash, err, shipOut)
	}
}
