// Package bitbucketserver is UBI-160 Phase 3's Bitbucket Server/Data
// Center counterpart to package github/gitlab/azuredevops: git history
// verification (reusing github.CommitExists/FileAtCommit/
// ParseProposalTrailer/ErrCommitNotFound/ErrFileNotFoundAtCommit/
// ErrNoProposalTrailer directly, same host-agnostic reuse as package
// gitlab/package azuredevops) plus a real, direct Bitbucket Server REST
// API client for the pull request a merge commit belongs to and its
// reviewer approvals.
//
// Bamboo has no single native git host the way GitHub Actions/GitLab
// CI/Azure Pipelines each do -- confirmed directly against Atlassian's
// own current documentation, not assumed: a Bamboo plan's linked
// repository can be Bitbucket Server/Data Center, Bitbucket Cloud, or
// GitHub. A GitHub-paired Bamboo plan is already covered by the
// existing --github-repo flag -- no new work needed for that case.
// Bitbucket Server/Data Center is the real, deliberately scoped target
// here: it's Bamboo's own deepest, most natively integrated pairing
// (bidirectional commit/build/deployment linking via Application Links,
// confirmed against Atlassian's own docs), and every other real
// mechanism this project's own bamboo.mdx guide already documents
// (plan branches, native build-status posting, the PR-comment REST call
// UBI-31 already verified) already assumes it. Bitbucket Cloud is a
// real, genuinely different API (different base URL, different token
// model) -- a real, separate scope decision, not built out here.
//
// Unlike GitHub/GitLab/Azure DevOps, Bitbucket Server has no official
// Atlassian-maintained Go SDK at all (confirmed by search -- only
// community-maintained ones exist, none exposing the specific
// commit-to-pull-request lookup this package needs). This package calls
// the real, confirmed Bitbucket Server REST API directly with net/http,
// reusing github.com/gfleury/go-bitbucket-v1's own real struct types
// (PullRequest, UserWithMetadata -- swagger-generated from Atlassian's
// own real API spec) for accurate JSON shapes, the same considered
// choice package azuredevops already made for a different real reason.
//
// A real, structural difference from every other platform this project
// covers, worth stating plainly: Bitbucket Server is self-hosted, with
// no single fixed default host the way api.github.com/gitlab.com/
// dev.azure.com each are. New returns no error and takes no options --
// baseURL is not an optional override of some real default; it's the
// one genuinely required piece of identity this platform has that none
// of the others do.
//
// core stays entirely dependency-free of this package too, same as
// package github/gitlab/azuredevops (see core/accept.go's
// AcceptFromMerge doc comment) -- core.MergeAcceptance was already
// platform-agnostic.
package bitbucketserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	bitbucketv1 "github.com/gfleury/go-bitbucket-v1"
)

// ErrNoPullRequestForCommit means the Bitbucket Server API found no pull
// request associated with a merge SHA at all -- the Bitbucket Server
// analog of github.ErrNoPullRequestForCommit / gitlab.
// ErrNoMergeRequestForCommit / azuredevops.ErrNoPullRequestForCommit.
var ErrNoPullRequestForCommit = errors.New("no pull request found for commit")

// Client wraps the real Bitbucket Server REST API -- the Bitbucket
// Server analog of github.Client / gitlab.Client / azuredevops.Client.
type Client struct {
	httpClient *http.Client
	baseURL    string // the real, self-hosted Bitbucket Server base URL, e.g. https://bitbucket.example.com -- no default exists
	token      string
}

// New returns a Client authenticated with a Bitbucket Server personal,
// project, or repository access token (read from
// BITBUCKET_SERVER_TOKEN by convention, mirroring the other three
// packages' own token env vars), sent as Authorization: Bearer, the
// same real convention this project's own bamboo.mdx guide already
// uses for the PR-comment call (UBI-31, confirmed against Atlassian's
// own HTTP access token docs). An empty token is valid --
// unauthenticated requests work against a public/anonymous-read
// project, same as the other three packages.
func New(baseURL, token string) *Client {
	return &Client{
		httpClient: http.DefaultClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
	}
}

// pullRequestForCommit finds the pull request mergeSHA belongs to, via
// the real, confirmed "get pull requests for a commit" endpoint (`GET
// /rest/api/1.0/projects/{projectKey}/repos/{repositorySlug}/commits/
// {commitId}/pull-requests`, a paged API -- confirmed both against
// Atlassian's own documentation and, independently, against the
// go-bitbucket-v1 SDK's own real "values" envelope-decoding convention
// for this exact family of paged response). Bitbucket Server, like the
// other three platforms, can in principle associate more than one PR
// with a commit in an unusual history; the first result is used, the
// same skeleton-scope simplification as the other three packages'
// clients.
func (c *Client) pullRequestForCommit(ctx context.Context, projectKey, repositorySlug, mergeSHA string) (*bitbucketv1.PullRequest, error) {
	url := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/commits/%s/pull-requests",
		c.baseURL, projectKey, repositorySlug, mergeSHA)
	resp, err := c.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("pull requests for commit %s: %w", mergeSHA, err)
	}
	defer resp.Body.Close()

	var page struct {
		Values []bitbucketv1.PullRequest `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("pull requests for commit %s: decode response: %w", mergeSHA, err)
	}
	if len(page.Values) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoPullRequestForCommit, mergeSHA)
	}
	return &page.Values[0], nil
}

// ApprovingReviewers returns every reviewer whose real, current
// `approved` field on the pull request's own `reviewers` array is
// true -- the Bitbucket Server analog of the other three packages' own
// ApprovingReviewers, but simpler to derive than any of them: Bitbucket
// Server's own API already returns a direct boolean per reviewer,
// confirmed against the real, swagger-generated UserWithMetadata type
// (`Approved bool`, alongside a separate `Status` string this package
// doesn't need to interpret itself) -- no most-recent-review-wins fold
// (GitHub), no reset-on-push default (GitLab), no five-value vote scale
// (Azure DevOps) to account for. May return an empty, non-nil slice;
// that's a valid outcome, not an error, same as the other three
// packages' clients.
func ApprovingReviewers(pr *bitbucketv1.PullRequest) []string {
	approvers := []string{}
	for _, r := range pr.Reviewers {
		if !r.Approved {
			continue
		}
		if r.User.Name == "" {
			continue
		}
		approvers = append(approvers, r.User.Name)
	}
	return approvers
}

func (c *Client) do(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s %s: status %d: %s", method, url, resp.StatusCode, string(b))
	}
	return resp, nil
}
