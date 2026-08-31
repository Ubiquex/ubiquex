package bitbucketcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Issue is what CreateIssue returns.
type Issue struct {
	ID      int64
	HTMLURL string
}

// CreatedPullRequest is what OpenDraftPR returns.
//
// Named apart from this package's own PullRequest, which is the far
// richer type derive.go reads back from the API (participants, source
// and destination refs, description). Reusing that name for a two-field
// creation result would make two genuinely different things look alike.
type CreatedPullRequest struct {
	Number  int64
	HTMLURL string
}

// CreateIssue opens a new repository issue carrying the drift receipt
// (UBI-165).
//
// Bitbucket Cloud genuinely has an issue tracker, unlike Bitbucket
// Server, which has none at all. Two real differences from GitHub's own,
// both confirmed against Atlassian's own current API definition:
//
//   - The tracker is OPT-IN PER REPOSITORY. Atlassian's own wording on
//     the surrounding issue-tracker endpoints is that they are "only
//     available on repositories that have the issue tracker enabled".
//     A repository with it switched off rejects this call, which is the
//     right place for that to surface: ubx cannot enable somebody's
//     tracker for them, and a silent fallback would hide a real
//     misconfiguration.
//   - The body is a nested object, `content.raw`, not a flat string --
//     the same shape this package's own pull request comments already
//     use, and deliberately not assumed from GitHub's flat "body".
func CreateIssue(ctx context.Context, api *Client, workspace, repoSlug, title, body string) (*Issue, error) {
	payload, err := json.Marshal(map[string]any{
		"title":   title,
		"content": map[string]string{"raw": body},
	})
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/repositories/%s/%s/issues", api.baseURL, workspace, repoSlug)
	resp, err := api.do(ctx, http.MethodPost, u, payload)
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		ID    int64 `json:"id"`
		Links struct {
			HTML struct {
				Href string `json:"href"`
			} `json:"html"`
		} `json:"links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode issue response: %w", err)
	}
	return &Issue{ID: out.ID, HTMLURL: out.Links.HTML.Href}, nil
}

// OpenDraftPR is github.OpenDraftPR's Bitbucket Cloud counterpart.
//
// Two real calls, the fewest of any of the five platforms, and the
// reason is a genuine capability of Bitbucket Cloud's own API rather
// than a shortcut. Atlassian's own documentation for POST .../src
// states it directly: "When a new branch name is specified (that does
// not already exist in the repo), and no parent SHA1s are provided,
// then the new commit will inherit from the current main branch's
// tip/HEAD commit, but not advance the main branch. The new commit will
// be the new branch." So branch creation, the commit, and the implicit
// branch-from-default all happen in one request, and no default-branch
// lookup is needed at all.
//
// The request is application/x-www-form-urlencoded, with the file's own
// repository path used as the FIELD NAME and its content as the value.
// That is Atlassian's own real convention, confirmed against their own
// documented curl example, and it is unlike every other platform here.
// The leading slash on the path is load-bearing: Atlassian documents
// that a file whose path would otherwise collide with a metadata field
// name (`message`, `branch`, `author`, `parents`, `files`) is
// disambiguated by an explicit leading slash, so one is always sent
// rather than only when a collision happens to be noticed.
//
// Bitbucket Cloud has no draft pull request concept, so draft status is
// carried in the title, as it is for GitLab and Bitbucket Server.
func OpenDraftPR(ctx context.Context, api *Client, workspace, repoSlug, branch, filePath string, fileContent []byte, commitMessage, prTitle, prDescription string) (*CreatedPullRequest, error) {
	form := url.Values{}
	form.Set("/"+strings.TrimPrefix(filePath, "/"), string(fileContent))
	form.Set("message", commitMessage)
	form.Set("branch", branch)

	srcURL := fmt.Sprintf("%s/repositories/%s/%s/src", api.baseURL, workspace, repoSlug)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srcURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if api.token != "" {
		req.Header.Set("Authorization", "Bearer "+api.token)
	}
	resp, err := api.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("commit %s to %s: %w", filePath, branch, err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("commit %s to %s: status %d", filePath, branch, resp.StatusCode)
	}

	prPayload, err := json.Marshal(map[string]any{
		"title":       draftTitle(prTitle),
		"description": prDescription,
		"source":      map[string]any{"branch": map[string]string{"name": branch}},
	})
	if err != nil {
		return nil, err
	}
	// The destination is deliberately omitted: Atlassian documents that
	// "If the pull request's destination is not specified, it will
	// default to the repository.mainbranch", which is exactly what is
	// wanted and saves a lookup that could disagree with the repository's
	// own current setting.
	u := fmt.Sprintf("%s/repositories/%s/%s/pullrequests", api.baseURL, workspace, repoSlug)
	prResp, err := api.do(ctx, http.MethodPost, u, prPayload)
	if err != nil {
		return nil, fmt.Errorf("open pull request: %w", err)
	}
	defer prResp.Body.Close()

	var out struct {
		ID    int64 `json:"id"`
		Links struct {
			HTML struct {
				Href string `json:"href"`
			} `json:"html"`
		} `json:"links"`
	}
	if err := json.NewDecoder(prResp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode pull request response: %w", err)
	}
	return &CreatedPullRequest{Number: out.ID, HTMLURL: out.Links.HTML.Href}, nil
}

// draftTitle marks the pull request as a draft in its own title,
// Bitbucket Cloud having no real draft flag of its own.
func draftTitle(title string) string {
	if len(title) >= 6 && (title[:6] == "Draft:" || title[:6] == "draft:") {
		return title
	}
	return "Draft: " + title
}
