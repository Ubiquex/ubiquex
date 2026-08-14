package azuredevops

import (
	"context"
	"fmt"

	"github.com/ubiquex/ubiquex/github"
)

// DerivedAcceptance is everything core.AcceptFromMerge needs, gathered
// from git history and the Azure DevOps API -- the Azure DevOps analog
// of github.DerivedAcceptance / gitlab.DerivedAcceptance (deliberately
// the same shape: core.MergeAcceptance is already platform-agnostic, see
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
// that exact commit, finds the pull request whose completion produced
// mergeSHA, extracts its "ubx-proposal: <hash>" trailer from the PR's
// description (Azure DevOps' own name for what GitHub calls a pull
// request's body and GitLab calls a merge request's description), and
// reads the current set of reviewers whose vote is 10 (approved).
// Structurally identical to github.DeriveAcceptance/gitlab.DeriveAcceptance
// -- see either's own doc comment for what it does and does not check:
// the trailer-hash comparison against the proposal file's own recomputed
// hash is core.AcceptFromMerge's job, not this function's, on any
// platform. project and repositoryID are Azure DevOps' own two separate
// identifiers (a project name/ID and a repository name/ID) -- unlike
// GitHub's single owner/repo pair or GitLab's single namespace/project
// path, Azure DevOps genuinely needs three real identifiers to address a
// repository (organization, already fixed into the Client itself;
// project; repository), not two.
func DeriveAcceptance(ctx context.Context, api *Client, repoDir, project, repositoryID, mergeSHA, proposalPath string) (*DerivedAcceptance, error) {
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

	pr, err := api.pullRequestForCommit(ctx, project, repositoryID, mergeSHA)
	if err != nil {
		return nil, fmt.Errorf("derive acceptance: %w", err)
	}
	if pr.PullRequestId == nil {
		return nil, fmt.Errorf("derive acceptance: pull request for %s has no pullRequestId", mergeSHA)
	}

	var description string
	if pr.Description != nil {
		description = *pr.Description
	}
	claimedHash, ok := github.ParseProposalTrailer(description)
	if !ok {
		return nil, fmt.Errorf("derive acceptance: %w: PR #%d", github.ErrNoProposalTrailer, *pr.PullRequestId)
	}

	return &DerivedAcceptance{
		ProposalFileContent: content,
		ClaimedHash:         claimedHash,
		MergeSHA:            mergeSHA,
		PullRequestID:       int64(*pr.PullRequestId),
		ProposalFile:        proposalPath,
		Approvers:           ApprovingReviewers(pr),
	}, nil
}
