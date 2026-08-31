package bitbucketserver

import (
	"context"
	"fmt"

	"github.com/ubiquex/ubiquex/vcs/github"
)

// DerivedAcceptance is everything core.AcceptFromMerge needs, gathered
// from git history and the Bitbucket Server API -- the Bitbucket Server
// analog of github.DerivedAcceptance / gitlab.DerivedAcceptance /
// azuredevops.DerivedAcceptance (deliberately the same shape:
// core.MergeAcceptance is already platform-agnostic, see
// core/accept.go).
type DerivedAcceptance struct {
	ProposalFileContent []byte
	ClaimedHash         string
	MergeSHA            string
	PullRequestID       int64
	ProposalFile        string // the repo-relative path passed in, echoed back for convenience
	Approvers           []string
}

// DeriveAcceptance runs the whole PR-merge derivation: verifies mergeSHA
// exists in the git history at repoDir, reads proposalPath's content at
// that exact commit, finds the pull request mergeSHA belongs to,
// extracts its "ubx-proposal: <hash>" trailer from the PR's description
// (Bitbucket Server's own name for what GitHub calls a pull request's
// body), and reads the current set of approving reviewers. Structurally
// identical to github.DeriveAcceptance/gitlab.DeriveAcceptance/
// azuredevops.DeriveAcceptance -- see any of their own doc comments for
// what it does and does not check: the trailer-hash comparison against
// the proposal file's own recomputed hash is core.AcceptFromMerge's
// job, not this function's, on any platform.
func DeriveAcceptance(ctx context.Context, api *Client, repoDir, projectKey, repositorySlug, mergeSHA, proposalPath string) (*DerivedAcceptance, error) {
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

	pr, err := api.pullRequestForCommit(ctx, projectKey, repositorySlug, mergeSHA)
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}

	claimedHash, ok := github.ParseProposalTrailer(pr.Description)
	if !ok {
		return nil, fmt.Errorf("derive acceptance: %w: PR #%d", github.ErrNoProposalTrailer, pr.ID)
	}

	return &DerivedAcceptance{
		ProposalFileContent: content,
		ClaimedHash:         claimedHash,
		MergeSHA:            mergeSHA,
		PullRequestID:       int64(pr.ID),
		ProposalFile:        proposalPath,
		Approvers:           ApprovingReviewers(pr),
	}, nil
}
