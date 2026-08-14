// Package azuredevops is UBI-160 Phase 2's Azure DevOps counterpart to
// package github (UBI-11 stage 1) and package gitlab (UBI-160 Phase 1):
// git history verification (reusing github.CommitExists/FileAtCommit/
// ParseProposalTrailer/ErrCommitNotFound/ErrFileNotFoundAtCommit/
// ErrNoProposalTrailer directly, same host-agnostic reuse as package
// gitlab) plus a real, direct Azure DevOps REST API client for the pull
// request a merge commit belongs to and its reviewer votes.
//
// A real, considered difference from package github/package gitlab,
// documented here rather than silently done: this package calls the
// real, confirmed Azure DevOps REST endpoints directly with net/http,
// using the official github.com/microsoft/azure-devops-go-api/v7
// package's own real struct types for accurate JSON shapes, rather than
// routing through that SDK's own Client.Send. The SDK's Send resolves
// every call through a locationId -> route-template lookup fetched live
// via an OPTIONS request to {baseUrl}/_apis (Connection.
// GetClientByResourceAreaId / Client.getResourceLocation) -- a real,
// additional discovery round trip neither GitHub's nor GitLab's own Go
// clients require, and not needed here either: the two REST endpoints
// this package calls are stable, versioned, directly documented paths
// (Microsoft Learn, api-version=7.1), not something that benefits from
// live discovery. Bypassing it keeps this package callable against a
// real httptest server exactly like package github/package gitlab
// (never mocked at the HTTP-transport level), without also having to
// fake Azure DevOps' own resource-location protocol just to reach an
// already-known, already-correct URL.
//
// core stays entirely dependency-free of this package too, same as
// package github/package gitlab (see core/accept.go's AcceptFromMerge
// doc comment) -- core.MergeAcceptance was already platform-agnostic.
package azuredevops

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
)

// ErrNoPullRequestForCommit means the Azure DevOps API found no pull
// request whose own completion produced this commit as its merge commit
// -- the Azure DevOps analog of github.ErrNoPullRequestForCommit /
// gitlab.ErrNoMergeRequestForCommit.
var ErrNoPullRequestForCommit = errors.New("no pull request found for commit")

// Client wraps the real Azure DevOps REST API -- the Azure DevOps analog
// of github.Client / gitlab.Client.
type Client struct {
	httpClient *http.Client
	baseURL    string // https://dev.azure.com/{organization}, or a test server URL
	authHeader string // "Basic " + base64(":"+PAT), empty for anonymous
}

// Option configures New.
type Option func(*Client)

// WithBaseURL points the client at a different API base -- tests use
// this to talk to an httptest.Server instead of the real dev.azure.com,
// the same convention as github.WithBaseURL / gitlab.WithBaseURL.
func WithBaseURL(rawURL string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(rawURL, "/")
	}
}

// New returns a Client authenticated with a personal access token (read
// from AZURE_DEVOPS_TOKEN by convention, mirroring github.New's
// GITHUB_TOKEN / gitlab.New's GITLAB_TOKEN). An empty token is valid --
// unauthenticated requests work against public projects, at a much
// lower rate limit, same as the other two platforms. organization is the
// real Azure DevOps organization name (the first path segment of
// https://dev.azure.com/{organization}).
func New(organization, token string, opts ...Option) *Client {
	c := &Client{
		httpClient: http.DefaultClient,
		baseURL:    "https://dev.azure.com/" + organization,
	}
	if token != "" {
		c.authHeader = "Basic " + basicAuth("", token)
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func basicAuth(username, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
}

// pullRequestForCommit finds the pull request whose own completion
// produced mergeSHA as its merge commit, via the real, confirmed
// "Pull Request Query" API (POST .../pullrequestquery?api-version=7.1,
// query type lastMergeCommit -- "search for pull requests that created
// the supplied merge commits," the precise match for what
// ubx accept --from-merge is given: a real merge commit that already
// landed, not just any commit a PR's source history happens to include,
// which is what query type "commit" would match instead). Azure DevOps,
// like GitHub and GitLab, can in principle associate more than one PR
// with a commit in an unusual history; the first result is used, the
// same skeleton-scope simplification as the other two platforms'
// clients.
func (c *Client) pullRequestForCommit(ctx context.Context, project, repositoryID, mergeSHA string) (*git.GitPullRequest, error) {
	queryType := git.GitPullRequestQueryTypeValues.LastMergeCommit
	query := git.GitPullRequestQuery{
		Queries: &[]git.GitPullRequestQueryInput{
			{Items: &[]string{mergeSHA}, Type: &queryType},
		},
	}
	body, err := json.Marshal(query.Queries)
	if err != nil {
		return nil, fmt.Errorf("marshal pull request query: %w", err)
	}

	url := fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullrequestquery?api-version=7.1",
		c.baseURL, project, repositoryID)
	resp, err := c.do(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("pull request query for commit %s: %w", mergeSHA, err)
	}
	defer resp.Body.Close()

	var result git.GitPullRequestQuery
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("pull request query for commit %s: decode response: %w", mergeSHA, err)
	}
	if result.Results == nil || len(*result.Results) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoPullRequestForCommit, mergeSHA)
	}
	prsByCommit := (*result.Results)[0]
	prs, ok := prsByCommit[mergeSHA]
	if !ok || len(prs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoPullRequestForCommit, mergeSHA)
	}
	return &prs[0], nil
}

// ApprovingReviewers returns every reviewer whose current vote on pr is
// 10 ("approved") -- the Azure DevOps analog of
// github.Client.ApprovingReviewers / gitlab.Client.ApprovingReviewers,
// but genuinely different in shape from both: Azure DevOps' own vote
// isn't a binary approve/reject the way GitHub's review state or
// GitLab's approved_by membership is -- it's a real five-value scale
// (10 approved, 5 approved with suggestions, 0 no vote, -5 waiting for
// author, -10 rejected), confirmed directly against Microsoft's own
// current REST API documentation, not assumed binary. Only 10 counts as
// an approver here -- 5 ("approved with suggestions") is a real,
// distinct, qualified vote, not a full approval, and rounding it up
// would be exactly the kind of "rounding up" this project's own
// acceptance model refuses elsewhere (see docs/schema.md's destroys
// amendment). Reviewers are already embedded directly on the pull
// request object this package's own pullRequestForCommit call already
// fetched -- unlike GitHub/GitLab, Azure DevOps needs no second API
// call for this. May return an empty, non-nil slice; that's a valid
// outcome, not an error, same as the other two platforms' clients.
func ApprovingReviewers(pr *git.GitPullRequest) []string {
	approvers := []string{}
	if pr.Reviewers == nil {
		return approvers
	}
	for _, r := range *pr.Reviewers {
		if r.Vote == nil || *r.Vote != 10 {
			continue
		}
		if r.DisplayName == nil || *r.DisplayName == "" {
			continue
		}
		approvers = append(approvers, *r.DisplayName)
	}
	return approvers
}

func (c *Client) do(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authHeader != "" {
		req.Header.Set("Authorization", c.authHeader)
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
