package gitlab

import (
	"context"
	"fmt"

	"github.com/ubiquex/ubiquex/vcs/github"
)

// DerivedAcceptance is everything core.AcceptFromMerge needs, gathered
// from git history and the GitLab API -- the GitLab analog of
// github.DerivedAcceptance (deliberately the same shape:
// core.MergeAcceptance is already platform-agnostic, see
// core/accept.go).
type DerivedAcceptance struct {
	ProposalFileContent []byte
	ClaimedHash         string
	MergeSHA            string
	MRIID               int64
	ProposalFile        string // the repo-relative path passed in, echoed back for convenience
	Approvers           []string
}

// DeriveAcceptance runs the whole MR-merge derivation: verifies mergeSHA
// exists in the git history at repoDir, reads proposalPath's content at
// that exact commit, finds the merge request mergeSHA belongs to, extracts
// its "ubx-proposal: <hash>" trailer from the MR's description (GitLab's
// own name for what GitHub calls a pull request's body), and reads the
// current set of approving reviewers. Structurally identical to
// github.DeriveAcceptance -- see that function's own doc comment for what
// it does and does not check: the trailer-hash comparison against the
// proposal file's own recomputed hash is core.AcceptFromMerge's job, not
// this function's, on either platform.
func DeriveAcceptance(ctx context.Context, api *Client, repoDir, project, mergeSHA, proposalPath string) (*DerivedAcceptance, error) {
	exists, err := github.CommitExists(ctx, repoDir, mergeSHA)
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("derive acceptance: %w: %s", github.ErrCommitNotFound, mergeSHA)
	}

	content, err := github.FileAtCommit(ctx, repoDir, mergeSHA, proposalPath)
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}

	mr, err := api.mergeRequestForCommit(ctx, project, mergeSHA)
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}

	claimedHash, ok := github.ParseProposalTrailer(mr.Description)
	if !ok {
		return nil, fmt.Errorf("derive acceptance: %w: MR !%d", github.ErrNoProposalTrailer, mr.IID)
	}

	approvers, err := api.ApprovingReviewers(ctx, project, mr.IID)
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}

	return &DerivedAcceptance{
		ProposalFileContent: content,
		ClaimedHash:         claimedHash,
		MergeSHA:            mergeSHA,
		MRIID:               mr.IID,
		ProposalFile:        proposalPath,
		Approvers:           approvers,
	}, nil
}
