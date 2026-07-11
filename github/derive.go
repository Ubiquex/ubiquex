package github

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoProposalTrailer means the pull request associated with a merge
// commit has no "ubx-proposal: <hash>" line in its body at all — there is
// nothing to derive acceptance from (docs/schema.md's pr_merge acceptance
// amendment: the trailer is the convention this whole tier depends on).
var ErrNoProposalTrailer = errors.New("pull request body has no ubx-proposal trailer")

// DerivedAcceptance is everything core.AcceptFromMerge needs, gathered
// from git history and the GitHub API — see docs/architecture.md's
// Decision loop section for the full flow this implements.
type DerivedAcceptance struct {
	ProposalFileContent []byte
	ClaimedHash         string
	MergeSHA            string
	PRNumber            int64
	ProposalFile        string // the repo-relative path passed in, echoed back for convenience
	Approvers           []string
}

// DeriveAcceptance runs the whole PR-merge derivation: verifies mergeSHA
// exists in the git history at repoDir, reads proposalPath's content at
// that exact commit, finds the pull request mergeSHA belongs to, extracts
// its "ubx-proposal: <hash>" trailer, and computes the current set of
// approving reviewers. It does not compare the trailer's hash against the
// proposal file's own recomputed hash — that check belongs to
// core.AcceptFromMerge, which is what actually enforces "derived, never
// asserted" (see ErrTrailerHashMismatch); this function's job is only to
// gather the inputs.
func DeriveAcceptance(ctx context.Context, api *Client, repoDir, owner, repo, mergeSHA, proposalPath string) (*DerivedAcceptance, error) {
	exists, err := CommitExists(ctx, repoDir, mergeSHA)
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("derive acceptance: %w: %s", ErrCommitNotFound, mergeSHA)
	}

	content, err := FileAtCommit(ctx, repoDir, mergeSHA, proposalPath)
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}

	pr, err := api.pullRequestForCommit(ctx, owner, repo, mergeSHA)
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}

	claimedHash, ok := ParseProposalTrailer(pr.GetBody())
	if !ok {
		return nil, fmt.Errorf("derive acceptance: %w: PR #%d", ErrNoProposalTrailer, pr.GetNumber())
	}

	approvers, err := api.ApprovingReviewers(ctx, owner, repo, int64(pr.GetNumber()))
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}

	return &DerivedAcceptance{
		ProposalFileContent: content,
		ClaimedHash:         claimedHash,
		MergeSHA:            mergeSHA,
		PRNumber:            int64(pr.GetNumber()),
		ProposalFile:        proposalPath,
		Approvers:           approvers,
	}, nil
}
