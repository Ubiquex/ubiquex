package github

import (
	"context"
	"fmt"

	ghapi "github.com/google/go-github/v78/github"
)

// Issue is what CreateIssue returns — just enough for a caller to report
// back to the operator (a URL to look at, a number to reference later).
type Issue struct {
	Number  int64
	HTMLURL string
}

// CreateIssue opens a new issue — the read-only-permissions-friendly half
// of UBI-11 stage 3's GitHub App skeleton (docs/architecture.md — Decision
// loop): this needs only "issues: write" on the repo, never
// "contents: write" at all (contrast OpenDraftPR, which does need that).
func CreateIssue(ctx context.Context, api *Client, owner, repo, title, body string) (*Issue, error) {
	issue, _, err := api.api.Issues.Create(ctx, owner, repo, &ghapi.IssueRequest{
		Title: ghapi.Ptr(title),
		Body:  ghapi.Ptr(body),
	})
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}
	return &Issue{Number: int64(issue.GetNumber()), HTMLURL: issue.GetHTMLURL()}, nil
}
