// Package bitbucketcloud is UBI-170's Bitbucket Cloud counterpart to
// package github/gitlab/azuredevops/bitbucketserver: git history
// verification (reusing github.CommitExists/FileAtCommit/
// ParseProposalTrailer/ErrCommitNotFound/ErrFileNotFoundAtCommit/
// ErrNoProposalTrailer directly, the same host-agnostic reuse every
// other platform package already makes) plus a real, direct Bitbucket
// Cloud REST API v2 client.
//
// Bitbucket Cloud is a genuinely different platform from Bitbucket
// Server, not a hosted variant of it, despite the shared vendor and the
// shared "Bitbucket" name. Every one of the following was confirmed
// directly against Bitbucket Cloud's own current, official API
// definition (https://api.bitbucket.org/swagger.json, which carries
// both the API schema and Atlassian's own narrative documentation
// inline) and, where observable, against the live API itself. None of
// it was assumed from Bitbucket Server's own already-verified behavior:
//
//   - Auth: Atlassian's own narrative states plainly that app passwords
//     are deprecated. This package sends an access token (repository,
//     project, or workspace scoped) as "Authorization: Bearer", the
//     documented scheme for access tokens and OAuth 2.0 alike.
//     Repository/project/workspace access tokens are bound to a
//     resource rather than a person, which is the right shape for an
//     unattended daemon; they appear AS a user in API responses.
//   - Identity: a Bitbucket Cloud account has NO username field at all.
//     The real fields are account_id, uuid, nickname, and display_name.
//     account_id is the stable one; nickname and display_name are
//     human-readable but user-changeable. This is why CODEOWNERS
//     matching in package server resolves human-readable entries to
//     real account_ids at check time rather than string-matching them
//     (UBI-170), a real, structural difference from every other
//     platform this project covers, all of which have a stable login.
//   - Approvals carry NO commit reference. A participant object is
//     exactly {user, role, approved, state, participated_on}. Bitbucket
//     Server's own per-approval lastReviewedCommit does not exist here,
//     so the TOCTOU gate follows GitLab's/Azure DevOps' own
//     branch-level "reset approvals on change" pattern instead (see
//     ResetApprovalsOnChange).
//   - A merge commit resolves to its pull request via a real, dedicated
//     endpoint, but that endpoint returns only a MINIMAL pull request
//     representation (id/title/links/draft/queued). Participants and
//     description need a second fetch. Confirmed live.
//
// Unlike Bitbucket Server, Bitbucket Cloud is SaaS with one real, fixed
// host (api.bitbucket.org), so baseURL here is a genuine default with a
// test-only override, matching github/gitlab rather than
// bitbucketserver.
//
// Bitbucket Cloud has no official Atlassian-maintained Go SDK, and
// go-bitbucket-v1's swagger-generated types are Bitbucket SERVER's own
// wire shapes, which do not match Cloud's v2 API at all. This package
// therefore declares its own minimal decode structs, the same choice
// package azuredevops made for its own separate reason.
//
// core stays entirely dependency-free of this package, same as every
// other platform package (see core/accept.go's AcceptFromMerge doc
// comment).
package bitbucketcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// defaultBaseURL is Bitbucket Cloud's own real, fixed API v2 root.
const defaultBaseURL = "https://api.bitbucket.org/2.0"

// ErrNoPullRequestForCommit means the Bitbucket Cloud API found no pull
// request associated with a merge SHA at all -- the Bitbucket Cloud
// analog of github.ErrNoPullRequestForCommit / gitlab.
// ErrNoMergeRequestForCommit / azuredevops.ErrNoPullRequestForCommit /
// bitbucketserver.ErrNoPullRequestForCommit.
var ErrNoPullRequestForCommit = errors.New("no pull request found for commit")

// ErrRepoNotIndexed means Bitbucket Cloud answered the commit-to-pull-
// request lookup but reported the repository as not yet indexed. A real,
// Bitbucket-Cloud-specific condition with no analog on any other
// platform this project covers: the response carries its own
// "repo_indexed" flag, and an unindexed repository returns an empty
// values array that is genuinely "we do not know yet", not "there is no
// pull request". Refused outright rather than reported as no-PR, since
// treating "unknown" as "none" would silently skip a real acceptance.
var ErrRepoNotIndexed = errors.New("bitbucket cloud has not finished indexing this repository, so a commit cannot be resolved to its pull request yet")

// Client wraps the real Bitbucket Cloud REST API v2 -- the Bitbucket
// Cloud analog of github.Client / gitlab.Client / azuredevops.Client /
// bitbucketserver.Client.
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

// Option configures New.
type Option func(*Client)

// WithBaseURL points the client at a different API base -- tests use
// this to talk to an httptest.Server instead of the real
// api.bitbucket.org, the same convention as github.WithBaseURL /
// gitlab.WithBaseURL / azuredevops.WithBaseURL.
func WithBaseURL(rawURL string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(rawURL, "/")
	}
}

// New returns a Client authenticated with a Bitbucket Cloud access
// token (repository, project, or workspace scoped -- read from
// BITBUCKET_CLOUD_TOKEN by convention, mirroring the other packages'
// own token env vars), sent as "Authorization: Bearer" per Atlassian's
// own current documentation. An empty token is valid: unauthenticated
// requests work against public repositories, at a much lower rate
// limit, same as every other platform package here.
func New(token string, opts ...Option) *Client {
	c := &Client{httpClient: http.DefaultClient, baseURL: defaultBaseURL, token: token}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Account is a real Bitbucket Cloud account as this package needs it.
//
// Deliberately NOT named "User": Bitbucket Cloud's own schema calls the
// type "account", and it genuinely is not a user in the sense every
// other platform here means, since a repository/workspace access token
// appears in exactly this shape too. There is no Name/Login/Username
// field to add, because Bitbucket Cloud does not have one.
type Account struct {
	AccountID   string `json:"account_id"`
	UUID        string `json:"uuid"`
	Nickname    string `json:"nickname"`
	DisplayName string `json:"display_name"`
}

// Identity returns the one real, stable identifier for a, preferring
// account_id and falling back to uuid. Empty when Bitbucket Cloud
// returned neither, which callers must treat as unidentifiable rather
// than as a match against anything.
func (a Account) Identity() string {
	if a.AccountID != "" {
		return a.AccountID
	}
	return a.UUID
}

// Label returns the most human-readable name a carries, for log lines
// and posted comments only. Never used for an authorization decision:
// nickname and display_name are both user-changeable.
func (a Account) Label() string {
	switch {
	case a.Nickname != "":
		return a.Nickname
	case a.DisplayName != "":
		return a.DisplayName
	default:
		return a.Identity()
	}
}

// Participant is a real Bitbucket Cloud pull request participant.
// Confirmed against both the official schema and the live API: these
// five fields are all there is. Note the absence of any commit
// reference, which is what forces the branch-restriction TOCTOU gate.
type Participant struct {
	User           Account `json:"user"`
	Role           string  `json:"role"`
	Approved       bool    `json:"approved"`
	State          string  `json:"state"`
	ParticipatedOn string  `json:"participated_on"`
}

// PullRequest is the real Bitbucket Cloud pull request shape this
// project needs. Fields the minimal (commit-to-pull-request) response
// omits are simply zero there, which is why callers that need
// Participants or Description always fetch the full representation.
type PullRequest struct {
	ID           int64         `json:"id"`
	Title        string        `json:"title"`
	Description  string        `json:"description"`
	State        string        `json:"state"`
	Author       Account       `json:"author"`
	Participants []Participant `json:"participants"`
	Source       struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
		Commit struct {
			Hash string `json:"hash"`
		} `json:"commit"`
	} `json:"source"`
	Destination struct {
		Branch struct {
			Name string `json:"name"`
		} `json:"branch"`
	} `json:"destination"`
	MergeCommit struct {
		Hash string `json:"hash"`
	} `json:"merge_commit"`
}

// HeadSHA returns the pull request's own current source commit.
func (pr *PullRequest) HeadSHA() string { return pr.Source.Commit.Hash }

// TargetBranch returns the pull request's own destination branch name.
func (pr *PullRequest) TargetBranch() string { return pr.Destination.Branch.Name }

// pullRequestForCommit finds the pull request mergeSHA belongs to, via
// Bitbucket Cloud's own real, dedicated endpoint (`GET /repositories/
// {workspace}/{repo_slug}/commit/{commit}/pullrequests`), confirmed
// live against a real public repository.
//
// Two real, Bitbucket-Cloud-specific behaviors this handles that no
// other platform has: the response envelope carries its own
// "repo_indexed" flag (see ErrRepoNotIndexed), and the pull requests it
// returns are a MINIMAL representation carrying only id/title/links, so
// this returns the ID and leaves the full fetch to the caller rather
// than pretending the minimal object is a whole pull request.
func (c *Client) pullRequestForCommit(ctx context.Context, workspace, repoSlug, mergeSHA string) (int64, error) {
	u := fmt.Sprintf("%s/repositories/%s/%s/commit/%s/pullrequests",
		c.baseURL, url.PathEscape(workspace), url.PathEscape(repoSlug), url.PathEscape(mergeSHA))
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("pull requests for commit %s: %w", mergeSHA, err)
	}
	defer resp.Body.Close()

	var page struct {
		RepoIndexed *bool `json:"repo_indexed"`
		Values      []struct {
			ID int64 `json:"id"`
		} `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return 0, fmt.Errorf("pull requests for commit %s: decode response: %w", mergeSHA, err)
	}
	if len(page.Values) == 0 {
		if page.RepoIndexed != nil && !*page.RepoIndexed {
			return 0, fmt.Errorf("%w: %s", ErrRepoNotIndexed, mergeSHA)
		}
		return 0, fmt.Errorf("%w: %s", ErrNoPullRequestForCommit, mergeSHA)
	}
	// Bitbucket Cloud, like every other platform here, can associate
	// more than one pull request with a commit in an unusual history;
	// the first result is used, the same skeleton-scope simplification
	// every other platform package's own client already makes.
	return page.Values[0].ID, nil
}

// GetPullRequest fetches pullRequestID's own full representation --
// required for anything needing Description or Participants, which the
// commit-to-pull-request lookup deliberately omits.
func (c *Client) GetPullRequest(ctx context.Context, workspace, repoSlug string, pullRequestID int64) (*PullRequest, error) {
	u := fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%d",
		c.baseURL, url.PathEscape(workspace), url.PathEscape(repoSlug), pullRequestID)
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("get pull request %d: %w", pullRequestID, err)
	}
	defer resp.Body.Close()

	var pr PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("get pull request %d: decode response: %w", pullRequestID, err)
	}
	return &pr, nil
}

// ApprovingReviewers returns the stable identity of every participant
// Bitbucket Cloud currently records as having approved pr.
//
// Returns account_ids, not names, deliberately: Bitbucket Cloud has no
// username, and nickname/display_name are both user-changeable, so an
// approver recorded in the ledger by a mutable label would not stay
// meaningful. May return an empty, non-nil slice; that is a valid
// outcome, not an error, same as every other platform package.
func ApprovingReviewers(pr *PullRequest) []string {
	approvers := []string{}
	for _, p := range pr.Participants {
		if !p.Approved {
			continue
		}
		id := p.User.Identity()
		if id == "" {
			continue
		}
		approvers = append(approvers, id)
	}
	return approvers
}

func (c *Client) do(ctx context.Context, method, rawURL string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
		return nil, fmt.Errorf("%s %s: status %d: %s", method, rawURL, resp.StatusCode, string(b))
	}
	return resp, nil
}
