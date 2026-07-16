package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestExitCode_VersionNeverNonZero: version has no "finding" concept at
// all -- it always succeeds (UBI-20 exit-code contract: 0 success, no verb
// gives it a 1).
func TestExitCode_VersionNeverNonZero(t *testing.T) {
	out, err := runUbx(t, nil, "version")
	requireExitCode(t, err, 0, out)
}

// TestExitCode_ProposeInvalidInput_ExitsTwo: propose has no "finding"
// concept either -- a malformed input is a plain error, exit 2.
func TestExitCode_ProposeInvalidInput_ExitsTwo(t *testing.T) {
	proposalPath := filepath.Join(t.TempDir(), "not-json.json")
	writeFile(t, proposalPath, "not valid json")

	_, err := runUbx(t, nil, "propose", proposalPath)
	requireExitCode(t, err, 2, "")
}

// TestExitCode_Init_ConflictingFlags_ExitsTwo: same story for init.
func TestExitCode_Init_ConflictingFlags_ExitsTwo(t *testing.T) {
	dir := t.TempDir()
	_, err := runUbx(t, nil, "init", "--dir", dir, "--provider", "/bin/true", "--source", "hashicorp/aws")
	requireExitCode(t, err, 2, "")
}

// TestExitCode_Accept_ParentMismatch_ExitsOne: two draft proposals both
// claim Parent="" (the empty/genesis ledger head). Accepting the first
// moves the head; accepting the second against a now-stale claimed parent
// must report ErrParentMismatch -- the world (ledger) moved since this
// proposal was resolved, the same "stale" family as a reverify block, an
// actionable finding rather than a plain error (UBI-20 exit-code
// contract).
func TestExitCode_Accept_ParentMismatch_ExitsOne(t *testing.T) {
	ledgerDir := t.TempDir()

	firstPath := filepath.Join(t.TempDir(), "first.json")
	writeFile(t, firstPath, draftProposalJSON)
	if _, err := runUbx(t, nil, "accept", firstPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (first): %v", err)
	}

	secondPath := filepath.Join(t.TempDir(), "second.json")
	writeFile(t, secondPath, `{
		"schema_version": 1, "stack": "payments", "parent": "", "kind": "change",
		"intent": {"summary": "a different draft, same stale (genesis) parent"},
		"delta": {"creates": [{"stack": "payments", "type": "aws_db_instance", "name": "payments-db-2"}]},
		"resolution": {"resolved_at": "2026-07-11T00:00:00Z"},
		"cost_delta": {"monthly_usd": 1}, "blast_radius": {"creates": 1, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)
	_, err := runUbx(t, nil, "accept", secondPath, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "parent does not match ledger head") {
		t.Fatalf("expected a parent-mismatch error, got: %v", err)
	}
}

// TestExitCode_Accept_AlreadyAccepted_ExitsTwo: a proposal file that
// already carries an id/acceptance (as if re-submitted after already going
// through this once) is a caller mistake, not an actionable infra finding
// -- exit 2.
func TestExitCode_Accept_AlreadyAccepted_ExitsTwo(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "already-accepted.json")
	writeFile(t, proposalPath, `{
		"schema_version": 1, "id": "`+strings.Repeat("a", 64)+`", "stack": "payments", "parent": "", "kind": "change",
		"intent": {"summary": "x"}, "delta": {},
		"resolution": {"resolved_at": "2026-07-11T00:00:00Z"},
		"cost_delta": {}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"status": "accepted",
		"acceptance": {"method": "local", "approvers": ["someone"], "accepted_at": "2026-07-11T00:00:00Z"}
	}`)

	_, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "already accepted") {
		t.Fatalf("expected an already-accepted error, got: %v", err)
	}
}

// TestExitCode_Accept_DuplicateProposal_ExitsTwo: accepting the exact same
// draft file twice hashes to the same content both times -- the ledger
// refuses the second write as a duplicate (immutable entries), a caller
// mistake (retry without checking ledger state first), not a finding.
func TestExitCode_Accept_DuplicateProposal_ExitsTwo(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "dup.json")
	writeFile(t, proposalPath, draftProposalJSON)

	if _, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (first): %v", err)
	}

	// Re-read the same never-mutated draft file and accept it again --
	// accept never writes ID/Acceptance back to disk, so this reproduces
	// the exact same content and hash as the first, legitimate accept.
	_, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "already exists in ledger") {
		t.Fatalf("expected a duplicate-proposal error, got: %v", err)
	}
}
