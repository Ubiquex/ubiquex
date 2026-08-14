package core

import (
	"errors"
	"testing"
)

func TestAcceptFromReview_HappyPath(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()

	hash, err := Hash(p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	accepted, err := AcceptFromReview(l, p, hash, ReviewAcceptance{
		Platform:     "github",
		PRNumber:     42,
		ProposalFile: "proposals/pending.json",
		Approver:     "roozbeh",
		Comment:      "LGTM, matches what we discussed",
	})
	if err != nil {
		t.Fatalf("AcceptFromReview: %v", err)
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
	if accepted.Acceptance.Method != "pr_review" {
		t.Errorf("Method = %q, want pr_review", accepted.Acceptance.Method)
	}
	if accepted.Acceptance.Platform != "github" {
		t.Errorf("Platform = %q, want github", accepted.Acceptance.Platform)
	}
	if accepted.Acceptance.MergeSHA != "" {
		t.Errorf("MergeSHA = %q, want empty -- pr_review never claims a merge commit", accepted.Acceptance.MergeSHA)
	}
	if accepted.Acceptance.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", accepted.Acceptance.PRNumber)
	}
	if accepted.Acceptance.ProposalFile != "proposals/pending.json" {
		t.Errorf("ProposalFile = %q, want proposals/pending.json", accepted.Acceptance.ProposalFile)
	}
	if len(accepted.Acceptance.Approvers) != 1 || accepted.Acceptance.Approvers[0] != "roozbeh" {
		t.Errorf("Approvers = %v, want [roozbeh]", accepted.Acceptance.Approvers)
	}
	if accepted.Acceptance.ReviewComment != "LGTM, matches what we discussed" {
		t.Errorf("ReviewComment = %q, want the real review body", accepted.Acceptance.ReviewComment)
	}
	if accepted.Acceptance.AcceptedAt == "" {
		t.Error("AcceptedAt is empty")
	}

	// Actually landed in the ledger, not just returned.
	read, err := l.Read(accepted.ID)
	if err != nil {
		t.Fatalf("Read back: %v", err)
	}
	if read.Acceptance.Method != "pr_review" {
		t.Errorf("read back Method = %q, want pr_review", read.Acceptance.Method)
	}
}

// TestAcceptFromReview_EmptyCommentIsFine covers the real, common case: a
// reviewer clicks Approve with no review body at all -- GitHub allows an
// empty comment on an Approve review, and that must be recorded as it
// happened, not rejected for lacking one.
func TestAcceptFromReview_EmptyCommentIsFine(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()

	hash, err := Hash(p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	accepted, err := AcceptFromReview(l, p, hash, ReviewAcceptance{
		Platform:     "github",
		PRNumber:     7,
		ProposalFile: "proposal.json",
		Approver:     "roozbeh",
		Comment:      "",
	})
	if err != nil {
		t.Fatalf("AcceptFromReview (empty comment must not be rejected): %v", err)
	}
	if accepted.Acceptance.ReviewComment != "" {
		t.Errorf("ReviewComment = %q, want empty", accepted.Acceptance.ReviewComment)
	}
}

func TestAcceptFromReview_HashMismatchRejected(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()

	_, err := AcceptFromReview(l, p, "0000000000000000000000000000000000000000000000000000000000000000",
		ReviewAcceptance{Platform: "github", PRNumber: 42, Approver: "roozbeh"})
	if !errors.Is(err, ErrTrailerHashMismatch) {
		t.Fatalf("got %v, want ErrTrailerHashMismatch", err)
	}

	// Never reached the ledger.
	if head, _ := l.Head(); head != "" {
		t.Fatalf("expected no ledger head after a rejected accept, got %q", head)
	}
}

func TestAcceptFromReview_RejectsAlreadyAccepted(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	p.ID = "already-has-an-id"

	if _, err := AcceptFromReview(l, p, "irrelevant", ReviewAcceptance{Platform: "github", PRNumber: 42, Approver: "roozbeh"}); !errors.Is(err, ErrAlreadyAccepted) {
		t.Fatalf("got %v, want ErrAlreadyAccepted", err)
	}
}

func TestAcceptFromReview_InvalidProposalRejected(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	p.Kind = KindAdoption
	p.BlastRadius = BlastRadius{Creates: 1} // adoption requires all-zero blast_radius

	if _, err := AcceptFromReview(l, p, "irrelevant", ReviewAcceptance{Platform: "github", PRNumber: 42, Approver: "roozbeh"}); !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal", err)
	}
}
