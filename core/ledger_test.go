package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLedger_AcceptAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	p := sampleProposal()
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if accepted.ID == "" {
		t.Fatal("accepted proposal has no ID")
	}
	if accepted.Status != StatusAccepted {
		t.Fatalf("status = %q, want %q", accepted.Status, StatusAccepted)
	}

	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != accepted.ID {
		t.Fatalf("head = %q, want %q", head, accepted.ID)
	}

	got, err := l.Read(accepted.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Intent.Summary != p.Intent.Summary {
		t.Fatalf("read back summary = %q, want %q", got.Intent.Summary, p.Intent.Summary)
	}
	if got.Acceptance == nil || got.Acceptance.Method != "local" {
		t.Fatalf("read back acceptance missing/wrong: %+v", got.Acceptance)
	}
}

func TestLedger_HeadEmptyForFreshLedger(t *testing.T) {
	l := Open(t.TempDir())
	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != "" {
		t.Fatalf("head = %q, want empty", head)
	}
}

func TestLedger_GenesisRequiresEmptyParent(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	p.Parent = "0000000000000000000000000000000000000000000000000000000000000000" // 68 chars: not a real parent, but a fresh ledger's head is "" either way

	if _, err := Accept(l, p); !errors.Is(err, ErrParentMismatch) {
		t.Fatalf("got %v, want ErrParentMismatch", err)
	}
}

func TestLedger_ParentMismatchRejected(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	first := sampleProposal()
	if _, err := Accept(l, first); err != nil {
		t.Fatalf("Accept(first): %v", err)
	}

	second := sampleProposal()
	second.Intent.Summary = "a second, unrelated change"
	second.Parent = "" // stale: should be first's ID by now
	if _, err := Accept(l, second); !errors.Is(err, ErrParentMismatch) {
		t.Fatalf("got %v, want ErrParentMismatch", err)
	}
}

func TestLedger_ChainedAcceptSucceeds(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	first := sampleProposal()
	acceptedFirst, err := Accept(l, first)
	if err != nil {
		t.Fatalf("Accept(first): %v", err)
	}

	second := sampleProposal()
	second.Intent.Summary = "a second, unrelated change"
	second.Parent = acceptedFirst.ID
	acceptedSecond, err := Accept(l, second)
	if err != nil {
		t.Fatalf("Accept(second): %v", err)
	}

	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != acceptedSecond.ID {
		t.Fatalf("head = %q, want %q", head, acceptedSecond.ID)
	}
}

func TestLedger_DuplicateProposalRejected(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	p := sampleProposal()
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// Re-append the exact same (already-accepted) record directly,
	// bypassing Accept — simulates a re-run/retry writing the same ID.
	if err := l.Append(accepted); !errors.Is(err, ErrDuplicateProposal) {
		t.Fatalf("got %v, want ErrDuplicateProposal", err)
	}
}

func TestLedger_ReadMissingProposal(t *testing.T) {
	l := Open(t.TempDir())
	_, err := l.Read("deadbeef")
	if !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("got %v, want ErrProposalNotFound", err)
	}
}

func TestLedger_ReadTruncatedProposalFile(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	if err := os.MkdirAll(filepath.Join(dir, "ledger", "proposals"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A truncated JSON file: looks like it started fine, cuts off mid-object.
	truncated := []byte(`{"schema_version":1,"stack":"payments","kind":"change","intent":{"sum`)
	if err := os.WriteFile(filepath.Join(dir, "ledger", "proposals", "badid.prop.json"), truncated, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := l.Read("badid")
	if !errors.Is(err, ErrCorruptLedgerEntry) {
		t.Fatalf("got %v, want ErrCorruptLedgerEntry", err)
	}
}

func TestLedger_ReadCorruptedProposalFile(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	if err := os.MkdirAll(filepath.Join(dir, "ledger", "proposals"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Not JSON at all -- e.g. a partially-overwritten block on disk.
	garbage := []byte("\x00\x01\xffnot even close to json")
	if err := os.WriteFile(filepath.Join(dir, "ledger", "proposals", "badid2.prop.json"), garbage, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := l.Read("badid2")
	if !errors.Is(err, ErrCorruptLedgerEntry) {
		t.Fatalf("got %v, want ErrCorruptLedgerEntry", err)
	}
}

func TestLedger_CorruptedHeadFile(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ubx", "ledger.lock"), []byte("not-a-hash\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := l.Head()
	if !errors.Is(err, ErrCorruptLedgerHead) {
		t.Fatalf("got %v, want ErrCorruptLedgerHead", err)
	}
}

func TestAccept_RejectsAlreadyAccepted(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	p.ID = "already-has-an-id"

	if _, err := Accept(l, p); !errors.Is(err, ErrAlreadyAccepted) {
		t.Fatalf("got %v, want ErrAlreadyAccepted", err)
	}
}
