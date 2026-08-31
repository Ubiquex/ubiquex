package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	ghapi "github.com/google/go-github/v78/github"
)

// ErrNoPullRequestForCommit means the GitHub API found no pull request
// associated with a merge SHA at all — without a PR there is no body to
// read a trailer from and no reviews to derive approvers from.
var ErrNoPullRequestForCommit = errors.New("no pull request found for commit")

// Client wraps the real GitHub API (go-github) — the only place in this
// package (and, alongside cloudtrail/, one of only two places in this
// codebase) that talks to an external HTTP API directly.
type Client struct {
	api *ghapi.Client
}

// Option configures New/NewWithHTTPClient.
//
// Applied to a small options struct rather than straight to the
// go-github client, because the real GitHub Enterprise Server form
// (WithEnterpriseBaseURL) both rewrites the client and can fail on a
// malformed URL -- go-github's own WithEnterpriseURLs returns a new
// client and an error, which a mutate-in-place option type cannot
// express.
type Option func(*clientOptions)

type clientOptions struct {
	baseURL           string
	enterpriseBaseURL string
}

// WithBaseURL points the client at an exact API endpoint, used
// verbatim.
//
// This is the "I already know the full API root" form: tests use it to
// talk to an httptest.Server, and so does the UBX_GITHUB_API_BASE_URL
// seam. For a real GitHub Enterprise Server deployment use
// WithEnterpriseBaseURL instead, which applies GHES' own real
// /api/v3/ path convention rather than requiring the operator to know
// and type it.
func WithBaseURL(rawURL string) Option {
	return func(o *clientOptions) { o.baseURL = rawURL }
}

// WithEnterpriseBaseURL points the client at a real GitHub Enterprise
// Server instance, given that instance's own URL (for example
// "https://github.example.com").
//
// Delegates to go-github's own WithEnterpriseURLs rather than setting
// the URL raw, which is the whole point (UBI-171): GHES serves its REST
// API under /api/v3/ and its uploads under /api/uploads/, neither of
// which api.github.com uses. WithEnterpriseURLs appends both when they
// are missing and leaves them alone when the operator already spelled
// them out, so both forms work. Setting the URL raw, as this package
// did before, produced a client that requested /repos/... at the
// instance root and 404'd on every real call.
//
// The same value is passed for the upload URL, which is correct for
// GHES: both live on the instance's own host, unlike github.com where
// uploads live on a separate uploads.github.com.
func WithEnterpriseBaseURL(rawURL string) Option {
	return func(o *clientOptions) { o.enterpriseBaseURL = rawURL }
}

// New returns a Client authenticated with token (a GitHub personal access
// token or Actions-provided token — read from GITHUB_TOKEN by convention,
// same as GitHub's own tooling and gh CLI; ubx doesn't reinvent credential
// discovery here any more than cloudtrail.New does for AWS). An empty
// token is valid — unauthenticated requests work against public repos, at
// a much lower rate limit.
//
// Returns an error only when a configured enterprise base URL cannot be
// applied, the same "validates its configuration at construction time"
// shape gitlab.New already has, so a malformed GHES URL fails at
// startup rather than as a mysterious first-request error.
func New(token string, opts ...Option) (*Client, error) {
	api := ghapi.NewClient(nil)
	if token != "" {
		api = api.WithAuthToken(token)
	}
	return applyOptions(api, opts)
}

// NewWithHTTPClient returns a Client using hc directly as its transport,
// rather than a static token — package server's own use case (UBI-28):
// hc wraps a github.com/bradleyfalzon/ghinstallation/v2 Transport, which
// signs a fresh App JWT and exchanges it for a short-lived installation
// token automatically, refreshing before each expires, for the life of
// a long-running daemon process. A bare token (New, above) has no
// refresh story of its own, which is exactly wrong for that case.
func NewWithHTTPClient(hc *http.Client, opts ...Option) (*Client, error) {
	return applyOptions(ghapi.NewClient(hc), opts)
}

func applyOptions(api *ghapi.Client, opts []Option) (*Client, error) {
	var o clientOptions
	for _, opt := range opts {
		opt(&o)
	}

	if o.enterpriseBaseURL != "" {
		enterprise, err := api.WithEnterpriseURLs(o.enterpriseBaseURL, o.enterpriseBaseURL)
		if err != nil {
			return nil, fmt.Errorf("github: enterprise base URL %q: %w", o.enterpriseBaseURL, err)
		}
		api = enterprise
	}
	if o.baseURL != "" {
		u, err := url.Parse(o.baseURL)
		if err != nil {
			return nil, fmt.Errorf("github: base URL %q: %w", o.baseURL, err)
		}
		api.BaseURL = u
	}
	return &Client{api: api}, nil
}

// API exposes the underlying go-github client directly, for callers that
// need a real GitHub API surface this package hasn't wrapped yet (package
// server's own collaborator-permission, team-membership, PR-file, and
// comment-listing/editing calls, UBI-28) rather than growing this
// package into a second, parallel go-github wrapper for every endpoint
// any one caller happens to need once.
func (c *Client) API() *ghapi.Client {
	return c.api
}

// pullRequestForCommit finds the pull request mergeSHA belongs to. GitHub
// can associate more than one PR with a commit in unusual histories
// (cherry-picks, etc.); the first (most API-relevant) result is used —
// this is a skeleton-scope simplification, not a claim that it's always
// unambiguous.
func (c *Client) pullRequestForCommit(ctx context.Context, owner, repo, sha string) (*ghapi.PullRequest, error) {
	prs, _, err := c.api.PullRequests.ListPullRequestsWithCommit(ctx, owner, repo, sha, nil)
	if err != nil {
		return nil, fmt.Errorf("list pull requests for commit %s: %w", sha, err)
	}
	if len(prs) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoPullRequestForCommit, sha)
	}
	return prs[0], nil
}

// ApprovingReviewers returns every reviewer whose most recent review on
// prNumber is APPROVED — a later CHANGES_REQUESTED (or a re-review after
// approving) supersedes an earlier APPROVED from the same person, so a
// withdrawn/superseded approval never counts (docs/architecture.md —
// Decision loop: "ubx doesn't count a withdrawn approval"). May return an
// empty, non-nil slice; that's a valid outcome, not an error. Exported
// (unlike pullRequestForCommit) because `ubx why --verify-acceptance`
// re-checks just this half of DeriveAcceptance's work, already knowing the
// PR number from the ledger — it has no need to re-resolve a commit to a
// PR the way a fresh accept does.
func (c *Client) ApprovingReviewers(ctx context.Context, owner, repo string, prNumber int64) ([]string, error) {
	reviews, _, err := c.api.PullRequests.ListReviews(ctx, owner, repo, int(prNumber), nil)
	if err != nil {
		return nil, fmt.Errorf("list reviews for PR #%d: %w", prNumber, err)
	}

	type latest struct {
		state       string
		submittedAt int64 // unix seconds; used only to compare, never displayed
	}
	byUser := map[string]latest{}
	for _, r := range reviews {
		login := r.GetUser().GetLogin()
		if login == "" {
			continue
		}
		submitted := r.GetSubmittedAt().Unix()
		if prev, ok := byUser[login]; !ok || submitted >= prev.submittedAt {
			byUser[login] = latest{state: r.GetState(), submittedAt: submitted}
		}
	}

	approvers := []string{}
	for login, l := range byUser {
		if l.state == "APPROVED" {
			approvers = append(approvers, login)
		}
	}
	return approvers, nil
}
