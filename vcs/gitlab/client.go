// Package gitlab is UBI-160 Phase 1's GitLab counterpart to package github
// (UBI-11 stage 1): git history verification (reusing
// github.CommitExists/FileAtCommit/ParseProposalTrailer/ErrCommitNotFound/
// ErrFileNotFoundAtCommit/ErrNoProposalTrailer directly -- those are
// genuinely host-agnostic local-git-plumbing and regex-parsing, not
// GitHub-specific, despite living in that package today; a real, minor
// UBI-52 package-naming quirk worth a follow-up cleanup ticket, not fixed
// here to keep this session's blast radius to what UBI-160 Phase 1 asked
// for) plus a GitLab API client (gitlab.com/gitlab-org/api/client-go/v2)
// for the merge request a merge commit belongs to and its approvals.
//
// core stays entirely dependency-free of this package too, same as
// package github (see core/accept.go's AcceptFromMerge doc comment) --
// core.MergeAcceptance was already platform-agnostic before this package
// existed, so no core change was needed to support a second VCS host.
package gitlab

import (
	"context"
	"errors"
	"fmt"

	glapi "gitlab.com/gitlab-org/api/client-go/v2"
)

// ErrNoMergeRequestForCommit means the GitLab API found no merge request
// associated with a merge SHA at all -- the GitLab analog of
// github.ErrNoPullRequestForCommit: without an MR there is no description
// to read a trailer from and no approvals to derive approvers from.
var ErrNoMergeRequestForCommit = errors.New("no merge request found for commit")

// Client wraps the real GitLab API -- the GitLab analog of github.Client.
type Client struct {
	api *glapi.Client
}

// Option configures New -- a type alias for glapi.ClientOptionFunc rather
// than a wrapping func type, since go-gitlab's own options are applied
// during construction (passed straight through to glapi.NewClient), unlike
// go-github's, which New (in package github) applies to an already-built
// client afterward.
type Option = glapi.ClientOptionFunc

// WithBaseURL points the client at a different API base -- tests use this
// to talk to an httptest.Server instead of the real gitlab.com, the same
// convention as github.WithBaseURL.
func WithBaseURL(rawURL string) Option {
	return glapi.WithBaseURL(rawURL)
}

// New returns a Client authenticated with token (a GitLab personal access
// token, project/group access token, or CI job token -- read from
// GITLAB_TOKEN by convention, mirroring github.New's GITHUB_TOKEN). An
// empty token is valid -- unauthenticated requests work against public
// projects, at a much lower rate limit. Unlike github.New, this can fail:
// the underlying client validates its configuration (including a custom
// base URL, if given) at construction time rather than on first use.
func New(token string, opts ...Option) (*Client, error) {
	api, err := glapi.NewClient(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("gitlab: new client: %w", err)
	}
	return &Client{api: api}, nil
}

// API exposes the underlying go-gitlab client directly, for callers
// that need a real GitLab API surface this package hasn't wrapped yet
// (package server's own Notes/MergeRequests/project-approval-settings/
// project-and-group-membership calls, UBI-28 Phase 2) rather than
// growing this package into a second, parallel go-gitlab wrapper for
// every endpoint any one caller happens to need once -- the same real
// reasoning package github's own API() accessor gives.
func (c *Client) API() *glapi.Client {
	return c.api
}

// mergeRequestForCommit finds the merge request mergeSHA belongs to.
// GitLab, like GitHub, can associate more than one MR with a commit in an
// unusual history; the first (most API-relevant) result is used -- the
// same skeleton-scope simplification as github.Client.pullRequestForCommit.
// project is "namespace/project" (or a numeric GitLab project ID) -- the
// GitLab API accepts either as its own :id path parameter and URL-encodes
// a path form itself, so no manual encoding is done here.
func (c *Client) mergeRequestForCommit(ctx context.Context, project, sha string) (*glapi.BasicMergeRequest, error) {
	mrs, _, err := c.api.Commits.ListMergeRequestsByCommit(project, sha, glapi.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("list merge requests for commit %s: %w", sha, err)
	}
	if len(mrs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoMergeRequestForCommit, sha)
	}
	return mrs[0], nil
}

// ApprovingReviewers returns every user GitLab currently records as having
// approved the merge request identified by mrIID (project-scoped, GitLab's
// own term for what this codebase elsewhere calls a PR/MR number) -- the
// GitLab analog of github.Client.ApprovingReviewers, but genuinely simpler
// to derive, not just differently named: GitLab's own approvals API
// already returns the authoritative, current approved-by set directly
// (approved_by), not a raw history of individual review events that needs
// folding into "most recent review per person wins" the way GitHub's
// ListReviews does. GitLab has no CHANGES_REQUESTED-supersedes-APPROVED
// concept at all -- instead, by default, GitLab clears every existing
// approval outright whenever a new commit lands on the MR
// (reset_approvals_on_push, a real, project-level setting, true by
// default), so approved_by, queried against an already-merged MR, already
// reflects exactly who approved the version that was actually merged --
// confirmed directly against GitLab's own current API documentation, not
// assumed symmetric with GitHub's model. May return an empty, non-nil
// slice; that's a valid outcome, not an error, same as
// github.Client.ApprovingReviewers.
func (c *Client) ApprovingReviewers(ctx context.Context, project string, mrIID int64) ([]string, error) {
	approvals, _, err := c.api.MergeRequestApprovals.GetConfiguration(project, mrIID, glapi.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("get approvals for MR !%d: %w", mrIID, err)
	}

	approvers := []string{}
	for _, a := range approvals.ApprovedBy {
		if a.User == nil || a.User.Username == "" {
			continue
		}
		approvers = append(approvers, a.User.Username)
	}
	return approvers, nil
}
