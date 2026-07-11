package core

import (
	"errors"
	"testing"
)

func TestAcceptFromMerge_HappyPath(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()

	hash, err := Hash(p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	accepted, err := AcceptFromMerge(l, p, hash, MergeAcceptance{
		MergeSHA:     "8c1d2e3f",
		PRNumber:     42,
		ProposalFile: "proposals/pending.json",
		Approvers:    []string{"roozbeh", "alice"},
	})
	if err != nil {
		t.Fatalf("AcceptFromMerge: %v", err)
	}
	if accepted.ID != hash {
		t.Errorf("ID = %q, want %q", accepted.ID, hash)
	}
	if accepted.Status != StatusAccepted {
		t.Errorf("Status = %q, want %q", accepted.Status, StatusAccepted)
	}
	if accepted.Acceptance == nil {
		t.Fatal("Acceptance is nil")
	}
	if accepted.Acceptance.Method != "pr_merge" {
		t.Errorf("Method = %q, want pr_merge", accepted.Acceptance.Method)
	}
	if accepted.Acceptance.MergeSHA != "8c1d2e3f" {
		t.Errorf("MergeSHA = %q, want 8c1d2e3f", accepted.Acceptance.MergeSHA)
	}
	if accepted.Acceptance.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", accepted.Acceptance.PRNumber)
	}
	if accepted.Acceptance.ProposalFile != "proposals/pending.json" {
		t.Errorf("ProposalFile = %q, want proposals/pending.json", accepted.Acceptance.ProposalFile)
	}
	if len(accepted.Acceptance.Approvers) != 2 {
		t.Errorf("Approvers = %v, want 2 entries", accepted.Acceptance.Approvers)
	}
	if accepted.Acceptance.AcceptedAt == "" {
		t.Error("AcceptedAt is empty")
	}

	// Actually landed in the ledger, not just returned.
	read, err := l.Read(accepted.ID)
	if err != nil {
		t.Fatalf("Read back: %v", err)
	}
	if read.Acceptance.Method != "pr_merge" {
		t.Errorf("read back Method = %q, want pr_merge", read.Acceptance.Method)
	}
}

func TestAcceptFromMerge_EmptyApproversIsRecordedNotRejected(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	hash, err := Hash(p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	accepted, err := AcceptFromMerge(l, p, hash, MergeAcceptance{MergeSHA: "8c1d2e3f", PRNumber: 42})
	if err != nil {
		t.Fatalf("AcceptFromMerge with zero approvers should succeed (enforcement is GitHub's job): %v", err)
	}
	if len(accepted.Acceptance.Approvers) != 0 {
		t.Errorf("Approvers = %v, want empty", accepted.Acceptance.Approvers)
	}
}

func TestAcceptFromMerge_HashMismatchRejected(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()

	_, err := AcceptFromMerge(l, p, "0000000000000000000000000000000000000000000000000000000000000000",
		MergeAcceptance{MergeSHA: "8c1d2e3f", PRNumber: 42, Approvers: []string{"roozbeh"}})
	if !errors.Is(err, ErrTrailerHashMismatch) {
		t.Fatalf("got %v, want ErrTrailerHashMismatch", err)
	}

	// Never reached the ledger.
	if head, _ := l.Head(); head != "" {
		t.Fatalf("expected no ledger head after a rejected accept, got %q", head)
	}
}

func TestAcceptFromMerge_RejectsAlreadyAccepted(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	p.ID = "already-has-an-id"

	if _, err := AcceptFromMerge(l, p, "irrelevant", MergeAcceptance{MergeSHA: "8c1d2e3f", PRNumber: 42}); !errors.Is(err, ErrAlreadyAccepted) {
		t.Fatalf("got %v, want ErrAlreadyAccepted", err)
	}
}

func TestAcceptFromMerge_InvalidProposalRejected(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	p.Kind = KindAdoption
	p.BlastRadius = BlastRadius{Creates: 1} // adoption requires all-zero blast_radius

	if _, err := AcceptFromMerge(l, p, "irrelevant", MergeAcceptance{MergeSHA: "8c1d2e3f", PRNumber: 42}); !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal", err)
	}
}
