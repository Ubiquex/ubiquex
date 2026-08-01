package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyChain_EmptyLedger(t *testing.T) {
	l := Open(t.TempDir())
	result, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.ProposalsChecked != 0 || !result.ChainIntact || len(result.Findings) != 0 {
		t.Fatalf("result = %+v, want empty and intact", result)
	}
}

func TestVerifyChain_ValidChain_NoFindings(t *testing.T) {
	l := Open(t.TempDir())

	first, err := Accept(l, sampleProposal())
	if err != nil {
		t.Fatalf("accept first: %v", err)
	}
	second := sampleProposal()
	second.Intent.Summary = "a second, unrelated change"
	second.Parent = first.ID
	acceptedSecond, err := Accept(l, second)
	if err != nil {
		t.Fatalf("accept second: %v", err)
	}
	third := sampleProposal()
	third.Intent.Summary = "a third, unrelated change"
	third.Parent = acceptedSecond.ID
	if _, err := Accept(l, third); err != nil {
		t.Fatalf("accept third: %v", err)
	}

	result, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.ProposalsChecked != 3 || !result.ChainIntact || len(result.Findings) != 0 {
		t.Fatalf("result = %+v, want 3 proposals, intact, no findings", result)
	}
	if len(result.Proposals) != 3 {
		t.Fatalf("Proposals = %d, want 3", len(result.Proposals))
	}
}

// row: tampered proposal byte -- hash breaks + every descendant flagged.
func TestVerifyChain_TamperedProposal_HashBreaksAndDescendantsTainted(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	first, err := Accept(l, sampleProposal())
	if err != nil {
		t.Fatalf("accept first: %v", err)
	}
	second := sampleProposal()
	second.Intent.Summary = "second"
	second.Parent = first.ID
	acceptedSecond, err := Accept(l, second)
	if err != nil {
		t.Fatalf("accept second: %v", err)
	}
	third := sampleProposal()
	third.Intent.Summary = "third"
	third.Parent = acceptedSecond.ID
	acceptedThird, err := Accept(l, third)
	if err != nil {
		t.Fatalf("accept third: %v", err)
	}

	// Tamper the FIRST proposal's own stored bytes -- change a content
	// field in place, leaving the filename (and therefore its own "id" as
	// far as the store's own lookup path is concerned) untouched. This is
	// exactly what "one byte on disk changed after the fact" means: no
	// API was used, just a direct edit of the committed file.
	path := filepath.Join(dir, "ledger", "proposals", first.ID+".prop.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read first's own file: %v", err)
	}
	var onDisk map[string]interface{}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	onDisk["intent"].(map[string]interface{})["summary"] = "TAMPERED -- this was never accepted"
	tampered, err := json.MarshalIndent(onDisk, "", "  ")
	if err != nil {
		t.Fatalf("marshal tampered: %v", err)
	}
	if err := os.WriteFile(path, tampered, 0o644); err != nil {
		t.Fatalf("write tampered: %v", err)
	}

	result, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.ChainIntact {
		t.Fatal("ChainIntact = true, want false after tampering")
	}
	if result.ProposalsChecked != 3 {
		t.Fatalf("ProposalsChecked = %d, want 3 (a tampered proposal is still readable, still checked)", result.ProposalsChecked)
	}

	byID := map[string][]string{}
	for _, f := range result.Findings {
		byID[f.ProposalID] = append(byID[f.ProposalID], f.Kind)
	}
	if !containsKind(byID[first.ID], FindingHashMismatch) {
		t.Fatalf("findings for first = %v, want %s", byID[first.ID], FindingHashMismatch)
	}
	if !containsKind(byID[acceptedSecond.ID], FindingTaintedDescendant) {
		t.Fatalf("findings for second = %v, want %s", byID[acceptedSecond.ID], FindingTaintedDescendant)
	}
	if !containsKind(byID[acceptedThird.ID], FindingTaintedDescendant) {
		t.Fatalf("findings for third = %v, want %s", byID[acceptedThird.ID], FindingTaintedDescendant)
	}
}

func containsKind(kinds []string, want string) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

// row: missing parent -- a dangling Parent reference the store has no
// object for at all.
func TestVerifyChain_MissingParent(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	danglingParent := strings.Repeat("1", 64)

	orphan := sampleProposal()
	orphan.Parent = danglingParent
	hash, err := Hash(orphan)
	if err != nil {
		t.Fatalf("hash orphan: %v", err)
	}
	orphan.ID = hash
	orphan.Status = StatusAccepted
	orphan.Acceptance = &Acceptance{Method: "local", AcceptedAt: "2026-07-28T00:00:00Z"}
	// re-hash isn't needed: id/acceptance/status are excluded from the
	// hashed content (core/canonical.go), so setting them after computing
	// hash is exactly how Accept itself does it.

	raw, err := json.MarshalIndent(orphan, "", "  ")
	if err != nil {
		t.Fatalf("marshal orphan: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "ledger", "proposals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ledger", "proposals", orphan.ID+".prop.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ubx", "ledger.lock"), []byte(orphan.ID+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.ChainIntact {
		t.Fatal("ChainIntact = true, want false")
	}
	if result.ProposalsChecked != 1 {
		t.Fatalf("ProposalsChecked = %d, want 1 (orphan itself reads fine; its own parent doesn't exist)", result.ProposalsChecked)
	}
	if len(result.Findings) != 1 || result.Findings[0].Kind != FindingMissingParent || result.Findings[0].ProposalID != danglingParent {
		t.Fatalf("findings = %+v, want exactly one missing_parent finding naming %s", result.Findings, danglingParent)
	}
}

// row (UBI-64): the head ITSELF is dangling -- zero levels removed, unlike
// TestVerifyChain_MissingParent above (an orphan whose own Parent is
// missing, one level removed from head). This is exactly the founder-test
// finding's shape: ledger/ deleted by hand, .ubx/ left in place, head still
// names a proposal that's now gone. Confirms ubx verify's own finding gets
// the richer UBI-64 teaching detail, not just the raw "proposal not found"
// wrap every other missing-parent case gets.
func TestVerifyChain_HeadItselfMissing_TeachingDetail(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	missing := strings.Repeat("2", 64)
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ubx", "ledger.lock"), []byte(missing+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.ChainIntact {
		t.Fatal("ChainIntact = true, want false")
	}
	if result.ProposalsChecked != 0 {
		t.Fatalf("ProposalsChecked = %d, want 0 -- nothing readable at all", result.ProposalsChecked)
	}
	if len(result.Findings) != 1 || result.Findings[0].Kind != FindingMissingParent || result.Findings[0].ProposalID != missing {
		t.Fatalf("findings = %+v, want exactly one missing_parent finding naming %s", result.Findings, missing)
	}
	detail := result.Findings[0].Detail
	for _, want := range []string{".ubx/ledger.lock", "ubx init --reset-ledger", "deleted or moved"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail %q missing teaching text %q", detail, want)
		}
	}
	if strings.Contains(detail, "ubx verify") {
		t.Fatalf("detail is embedded inside ubx verify's own output -- shouldn't tell the user to run ubx verify: %q", detail)
	}
}

// row: truncated apply record.
func TestVerifyChain_TruncatedApplyRecord(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	first, err := Accept(l, sampleProposal())
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := l.BeginApply(first.ID); err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	path := filepath.Join(dir, "ledger", "applies", first.ID+".attempt-1.apply.json")
	if err := os.WriteFile(path, []byte("{ not valid json"), 0o644); err != nil {
		t.Fatalf("corrupt apply file: %v", err)
	}

	result, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.ChainIntact {
		t.Fatal("ChainIntact = true, want false")
	}
	found := false
	for _, f := range result.Findings {
		if f.ProposalID == first.ID && f.Kind == FindingCorruptApply {
			found = true
		}
	}
	if !found {
		t.Fatalf("findings = %+v, want a corrupt_apply finding for %s", result.Findings, first.ID)
	}
}

// A legitimate crash-and-retry sequence (an unsealed attempt followed by a
// sealed one) must NOT be flagged -- BeginApply's own "parent is the most
// recently SEALED attempt" rule, mirrored exactly.
func TestVerifyChain_ApplyChain_CrashedAttemptNotFlagged(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	first, err := Accept(l, sampleProposal())
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if _, err := l.BeginApply(first.ID); err != nil {
		t.Fatalf("begin apply 1 (never sealed -- simulates a crash): %v", err)
	}
	rec2, err := l.BeginApply(first.ID)
	if err != nil {
		t.Fatalf("begin apply 2: %v", err)
	}
	if rec2.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", rec2.Attempt)
	}
	if rec2.Parent != "" {
		t.Fatalf("rec2.Parent = %q, want empty (attempt 1 never sealed, so there's no prior sealed id yet)", rec2.Parent)
	}
	if _, err := l.SealApply(rec2, ApplySummary{Outcome: "applied", StartedAt: "t0", FinishedAt: "t1"}); err != nil {
		t.Fatalf("seal 2: %v", err)
	}

	result, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !result.ChainIntact {
		t.Fatalf("ChainIntact = false, want true (a crashed-then-retried apply is legitimate): findings=%+v", result.Findings)
	}
}

// row: $redacted well-formedness.
func TestVerifyChain_MalformedRedacted_Flagged(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	p.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"payments-db","config":{"password":{"$redacted":"not-an-object"}}}`),
	}
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	result, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if result.ChainIntact {
		t.Fatal("ChainIntact = true, want false")
	}
	found := false
	for _, f := range result.Findings {
		if f.ProposalID == accepted.ID && f.Kind == FindingRedactedMalformed {
			found = true
		}
	}
	if !found {
		t.Fatalf("findings = %+v, want a redacted_malformed finding", result.Findings)
	}
}

func TestVerifyChain_WellFormedRedacted_NotFlagged(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	p.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"payments-db","config":{"password":{"$redacted":{"sha256":"` +
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `"}}}}`),
	}
	if _, err := Accept(l, p); err != nil {
		t.Fatalf("accept: %v", err)
	}

	result, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !result.ChainIntact {
		t.Fatalf("ChainIntact = false, want true: findings=%+v", result.Findings)
	}
}

// row: mixed schema versions -- a schema_version:1 proposal (create-only,
// exactly the shape Validate has always allowed pre-v2, per docs/schema.md)
// chained with a schema_version:2 one must verify identically either way;
// nothing in VerifyChain assumes a fixed version.
func TestVerifyChain_MixedSchemaVersions(t *testing.T) {
	l := Open(t.TempDir())

	v1 := sampleProposal()
	v1.SchemaVersion = 1
	acceptedV1, err := Accept(l, v1)
	if err != nil {
		t.Fatalf("accept v1: %v", err)
	}

	v2 := sampleProposal()
	v2.SchemaVersion = SchemaVersion
	v2.Intent.Summary = "a v2 proposal chained after a v1 one"
	v2.Parent = acceptedV1.ID
	if _, err := Accept(l, v2); err != nil {
		t.Fatalf("accept v2: %v", err)
	}

	result, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !result.ChainIntact || result.ProposalsChecked != 2 || len(result.Findings) != 0 {
		t.Fatalf("result = %+v, want 2 proposals, intact, no findings", result)
	}
}
