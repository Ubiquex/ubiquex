package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// writeDanglingHead directly writes a well-formed-but-nonexistent proposal
// id into ledgerDir/.ubx/ledger.lock -- UBI-64's own founder-test finding
// reproduced at the file level: ledger/ deleted by hand, .ubx/ left in
// place. Distinct from a garbage/corrupt head (already covered by
// TestLedger_CorruptedHeadFile in core) -- this hash is perfectly valid,
// just absent from the ledger's own proposal store.
func writeDanglingHead(t *testing.T, ledgerDir, missing string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(ledgerDir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ledgerDir, ".ubx", "ledger.lock"), []byte(missing+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// row (UBI-64): head -> missing proposal, git store. ubx verify already
// detects this as a missing_parent finding (core/verify_test.go proves
// that directly); this test proves the teaching error ALSO reaches
// fold-touching commands other than verify (status, here), and that the
// sanctioned `ubx init --reset-ledger` fresh-start actually clears it.
func TestInit_ResetLedger_HeadPointsToMissingProposal_GitStore(t *testing.T) {
	dir := t.TempDir()
	missing := strings.Repeat("4", 64)
	writeDanglingHead(t, dir, missing)

	// Before reset: a fold-touching command other than verify (status
	// walks Chain() via FoldState) hits the raw chain crash, now with the
	// UBI-64 teaching text instead of a bare "proposal not found."
	_, statusErr := runUbx(t, nil, "status", "--ledger-dir", dir, "--stack", "payments")
	if statusErr == nil {
		t.Fatal("expected ubx status to fail against a dangling head")
	}
	for _, want := range []string{".ubx/ledger.lock", "ubx init --reset-ledger", "ubx verify", "deleted or moved"} {
		if !strings.Contains(statusErr.Error(), want) {
			t.Fatalf("status error %q missing teaching text %q", statusErr.Error(), want)
		}
	}

	// ubx verify's own independent chain walk reports the same condition
	// as a structured finding, not a crash -- exit 1, not 2.
	verifyOut, verifyErr := runUbx(t, nil, "verify", "--ledger-dir", dir)
	requireExitCode(t, verifyErr, 1, "")
	if !strings.Contains(verifyOut, "missing_parent") || !strings.Contains(verifyOut, ".ubx/ledger.lock") {
		t.Fatalf("expected a missing_parent finding naming .ubx/ledger.lock, got: %s", verifyOut)
	}

	// The broken head needs no --force: it's genuinely broken, not a
	// healthy ledger being reset by mistake.
	resetOut, err := runUbx(t, nil, "init", "--dir", dir, "--reset-ledger")
	if err != nil {
		t.Fatalf("ubx init --reset-ledger: %v\noutput: %s", err, resetOut)
	}
	if !strings.Contains(resetOut, "ledger head cleared") {
		t.Fatalf("expected a reset confirmation line, got: %s", resetOut)
	}
	if _, err := os.Stat(filepath.Join(dir, ".ubx", "ledger.lock")); !os.IsNotExist(err) {
		t.Fatalf(".ubx/ledger.lock should be gone after reset, stat err = %v", err)
	}

	// Post-reset, the ledger is a genesis ledger again -- every command
	// that used to crash now sees an ordinary empty chain.
	statusOut, err := runUbx(t, nil, "status", "--ledger-dir", dir, "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx status after reset: %v\noutput: %s", err, statusOut)
	}
	verifyOut2, err := runUbx(t, nil, "verify", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx verify after reset: %v\noutput: %s", err, verifyOut2)
	}
	if !strings.Contains(verifyOut2, "chain: intact") {
		t.Fatalf("expected an intact chain after reset, got: %s", verifyOut2)
	}
}

// A healthy ledger (head resolves to a real, readable proposal) refuses
// --reset-ledger without --force -- resetting it would silently orphan
// real history, exactly the kind of accidental data loss this project
// refuses to risk by default.
func TestInit_ResetLedger_RefusesHealthyLedgerWithoutForce(t *testing.T) {
	dir := t.TempDir()
	acceptLocalProposal(t, dir, "healthy", "")

	out, err := runUbx(t, nil, "init", "--dir", dir, "--reset-ledger")
	if err == nil {
		t.Fatalf("expected refusal resetting a healthy ledger without --force, got success: %s", out)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected the refusal to mention --force, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".ubx", "ledger.lock")); statErr != nil {
		t.Fatalf("head should be untouched by a refused reset: %v", statErr)
	}

	// --force overrides the refusal.
	forceOut, err := runUbx(t, nil, "init", "--dir", dir, "--reset-ledger", "--force")
	if err != nil {
		t.Fatalf("ubx init --reset-ledger --force: %v\noutput: %s", err, forceOut)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".ubx", "ledger.lock")); !os.IsNotExist(statErr) {
		t.Fatalf("head should be cleared once --force overrides the refusal, stat err = %v", statErr)
	}
}

// row (UBI-64): stale plans referencing a wiped ledger. A plan file
// (.ubx/plans/<hash>.json) is a draft pinned to whatever head was current
// when `ubx plan` wrote it -- reset clears these alongside the head since
// keeping them around would only ever fail a later `ubx ship <hash>` with
// a parent mismatch. Confirms both halves: the stale file is actually
// removed, and resolving its hash afterward fails cleanly (no matching
// plan), never a crash.
func TestInit_ResetLedger_StalePlansCleared(t *testing.T) {
	dir := t.TempDir()
	first := acceptLocalProposal(t, dir, "first", "")

	planHash := strings.Repeat("9", 64)
	planJSON := `{
		"schema_version": 1, "stack": "payments", "parent": "` + first + `", "kind": "change",
		"intent": {"summary": "stale plan"}, "delta": {},
		"resolution": {"resolved_at": "2026-07-11T00:00:00Z"},
		"cost_delta": {}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`
	if _, err := writePlanFile(dir, planHash, []byte(planJSON)); err != nil {
		t.Fatalf("seed stale plan file: %v", err)
	}
	planPath := filepath.Join(dir, ".ubx", "plans", planHash+".json")
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("plan file should exist before reset: %v", err)
	}

	out, err := runUbx(t, nil, "init", "--dir", dir, "--reset-ledger", "--force")
	if err != nil {
		t.Fatalf("ubx init --reset-ledger --force: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "1 stale plan draft(s) removed") {
		t.Fatalf("expected the reset summary to report 1 removed plan draft, got: %s", out)
	}
	if _, err := os.Stat(planPath); !os.IsNotExist(err) {
		t.Fatalf("stale plan file should be removed after reset, stat err = %v", err)
	}

	_, shipErr := runUbx(t, nil, "ship", planHash, "--ledger-dir", dir)
	if shipErr == nil {
		t.Fatal("expected ubx ship on a cleared stale plan hash to fail, got success")
	}
	if !strings.Contains(shipErr.Error(), "no matching plan") {
		t.Fatalf("expected a clean \"no matching plan\" failure, got: %v", shipErr)
	}
}

// row (UBI-64): salt-only survival. Resetting a broken ledger clears the
// head and stale plans but must never touch .ubx/salt -- losing it
// degrades $redacted comparisons across whatever ledger/proposals content
// remains on disk for no benefit a reset actually needs (core/salt.go).
func TestInit_ResetLedger_SaltSurvives(t *testing.T) {
	dir := t.TempDir()
	ledger := core.Open(dir)
	saltBefore, err := ledger.Salt()
	if err != nil {
		t.Fatalf("generate salt: %v", err)
	}
	saltPath := filepath.Join(dir, ".ubx", "salt")
	if _, err := os.Stat(saltPath); err != nil {
		t.Fatalf("salt file should exist after Salt(): %v", err)
	}

	writeDanglingHead(t, dir, strings.Repeat("7", 64))

	out, err := runUbx(t, nil, "init", "--dir", dir, "--reset-ledger")
	if err != nil {
		t.Fatalf("ubx init --reset-ledger: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "salt preserved") {
		t.Fatalf("expected the reset summary to mention the salt being preserved, got: %s", out)
	}

	saltAfter, err := os.ReadFile(saltPath)
	if err != nil {
		t.Fatalf("salt file should survive reset: %v", err)
	}
	if !bytes.Equal(saltBefore, saltAfter) {
		t.Fatalf("salt content changed across reset: before=%x after=%x", saltBefore, saltAfter)
	}
}

// row (UBI-64), remote store half: the same "head resolves, proposal
// missing" condition, but for a LedgerStore-backed stack rather than the
// git-directory default -- ubx verify's own finding must carry the
// remote-store phrasing ("head object"), never the git-only ".ubx/
// ledger.lock" text, which would be actively misleading here (there is no
// such local file for a remote store at all).
func TestVerify_RemoteStore_HeadResolvesButProposalMissing(t *testing.T) {
	storeName, bucket := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	missing := strings.Repeat("b", 64)
	if err := bucket.WriteAll(context.Background(), "heads/genesis", []byte(missing), nil); err != nil {
		t.Fatalf("seed dangling remote head edge: %v", err)
	}

	out, err := runUbx(t, nil, "verify", "--stack", "payments")
	requireExitCode(t, err, 1, "")
	if !strings.Contains(out, "missing_parent") {
		t.Fatalf("expected a missing_parent finding, got: %s", out)
	}
	if strings.Contains(out, "ledger.lock") {
		t.Fatalf("a remote-store finding should never mention the git-local ledger.lock file: %s", out)
	}
	if !strings.Contains(out, "head object") {
		t.Fatalf("expected remote-store phrasing (\"head object\"), got: %s", out)
	}
}
