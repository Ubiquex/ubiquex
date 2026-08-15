package bitbucketcloud

import (
	"context"
	"fmt"

	"github.com/ubiquex/ubiquex/github"
)

// DerivedAcceptance is everything core.AcceptFromMerge needs, gathered
// from git history and the Bitbucket Cloud API -- the Bitbucket Cloud
// analog of github.DerivedAcceptance / gitlab.DerivedAcceptance /
// azuredevops.DerivedAcceptance / bitbucketserver.DerivedAcceptance
// (deliberately the same shape: core.MergeAcceptance is already
// platform-agnostic, see core/accept.go).
type DerivedAcceptance struct {
	ProposalFileContent []byte
	ClaimedHash         string
	MergeSHA            string
	PullRequestID       int64
	ProposalFile        string // the repo-relative path passed in, echoed back for convenience
	Approvers           []string
}

// DeriveAcceptance runs the whole pull-request-merge derivation:
// verifies mergeSHA exists in the git history at repoDir, reads
// proposalPath's content at that exact commit, finds the pull request
// mergeSHA belongs to, extracts its "ubx-proposal: <hash>" trailer from
// the pull request's description (Bitbucket Cloud's own name for what
// GitHub calls a body), and reads the current set of approving
// participants. Structurally identical to every other platform's own
// DeriveAcceptance -- see any of their doc comments for what it does and
// does not check: the trailer-hash comparison against the proposal
// file's own recomputed hash is core.AcceptFromMerge's job, not this
// function's, on any platform.
//
// One real, Bitbucket-Cloud-specific difference from all four of them:
// this needs TWO API calls, not one. Bitbucket Cloud's own
// commit-to-pull-request endpoint returns a minimal pull request
// representation with no description and no participants at all
// (confirmed live), so the full representation is fetched separately.
// Every other platform returns enough in one call.
func DeriveAcceptance(ctx context.Context, api *Client, repoDir, workspace, repoSlug, mergeSHA, proposalPath string) (*DerivedAcceptance, error) {
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

	prID, err := api.pullRequestForCommit(ctx, workspace, repoSlug, mergeSHA)
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}

	pr, err := api.GetPullRequest(ctx, workspace, repoSlug, prID)
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
		PullRequestID:       pr.ID,
		ProposalFile:        proposalPath,
		Approvers:           ApprovingReviewers(pr),
	}, nil
}
