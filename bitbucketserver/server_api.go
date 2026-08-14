// server_api.go adds the real Bitbucket Server REST API calls UBI-28
// Phase 4's server/bitbucketserver*.go needs beyond what client.go (UBI-
// 160 Phase 3, `ubx accept --from-merge` only) already has: fetching a
// pull request directly by ID, listing its changed files, posting/
// editing a comment, reading a raw file at a ref (CODEOWNERS), and
// checking a user's own real repository permission level. Continues
// client.go's own established real net/http-direct style (reusing only
// go-bitbucket-v1's struct types for accurate JSON shapes) for
// consistency within this one package, even though the real,
// community-maintained SDK does implement every one of these endpoints
// via its own generated DefaultApiService transport -- switching transport
// styles endpoint-by-endpoint within a single package would be a real,
// avoidable inconsistency client.go's own doc comment doesn't have to
// account for.
package bitbucketserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	bitbucketv1 "github.com/gfleury/go-bitbucket-v1"
)

// GetPullRequest fetches pullRequestID directly -- the real `GET
// /rest/api/1.0/projects/{projectKey}/repos/{repositorySlug}/pull-requests/{id}`
// endpoint, confirmed against Atlassian's own current REST API
// reference and the real, locally-verified go-bitbucket-v1 SDK source
// (DefaultApiService.GetPullRequest's own real path construction).
func (c *Client) GetPullRequest(ctx context.Context, projectKey, repositorySlug string, pullRequestID int) (*bitbucketv1.PullRequest, error) {
	u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/pull-requests/%d", c.baseURL, projectKey, repositorySlug, pullRequestID)
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("get pull request %d: %w", pullRequestID, err)
	}
	defer resp.Body.Close()

	var pr bitbucketv1.PullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("get pull request %d: decode response: %w", pullRequestID, err)
	}
	return &pr, nil
}

// changePath mirrors com.atlassian.bitbucket.content.Path's own real
// JSON shape -- confirmed against Bitbucket Server's own current
// Javadoc reference for the type every real "changes"/"browse" REST
// response reuses server-wide (components/parent/name/extension/
// toString) -- go-bitbucket-v1 has no typed struct for this endpoint's
// own real "values" envelope at all, so this package decodes its own
// minimal local shape, using only the one real field
// (ListPullRequestChangedFiles) needs.
type changePath struct {
	ToString string `json:"toString"`
}

// ListPullRequestChangedFiles returns every real, current changed-file
// path for pullRequestID -- the real `GET .../pull-requests/{id}/changes`
// endpoint, a single real call (unlike Azure DevOps' own two-call
// Iterations-then-Changes dance, see azuredevops/server_api.go's own
// ListPullRequestChangedFiles doc comment): Bitbucket Server's own
// pull-request-changes API is always relative to the PR's own current
// state, no separate "which iteration is latest" lookup needed.
func (c *Client) ListPullRequestChangedFiles(ctx context.Context, projectKey, repositorySlug string, pullRequestID int) ([]string, error) {
	u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/pull-requests/%d/changes?limit=1000",
		c.baseURL, projectKey, repositorySlug, pullRequestID)
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("list changed files for pull request %d: %w", pullRequestID, err)
	}
	defer resp.Body.Close()

	var page struct {
		Values []struct {
			Path changePath `json:"path"`
		} `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("list changed files for pull request %d: decode response: %w", pullRequestID, err)
	}

	paths := make([]string, 0, len(page.Values))
	for _, v := range page.Values {
		if v.Path.ToString != "" {
			paths = append(paths, v.Path.ToString)
		}
	}
	return paths, nil
}

// prComment is the real, minimal shape of a decoded pull-request
// comment response -- id/version (needed to edit it back in via
// UpdateComment, Bitbucket Server's own real optimistic-locking
// requirement, confirmed against Atlassian's own docs: every update
// call must echo the comment's own current version), text, and the
// real author sub-object findLastBotCommentBitbucketServer needs.
type prComment struct {
	ID      int                       `json:"id"`
	Version int                       `json:"version"`
	Text    string                    `json:"text"`
	Author  bitbucketv1.UserWithLinks `json:"author"`
}

// ListPullRequestComments returns every real, current top-level comment
// on pullRequestID (Bitbucket Server's own real activity-comment model
// nests replies under a "parent" field this package never needs to
// walk -- only top-level comments matter for ubx's own edit-in-place
// mechanism, the same "top-level only" scope postOrEditComment/
// postOrEditCommentGitLab already have).
func (c *Client) ListPullRequestComments(ctx context.Context, projectKey, repositorySlug string, pullRequestID int) ([]prComment, error) {
	u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/pull-requests/%d/comments?limit=1000",
		c.baseURL, projectKey, repositorySlug, pullRequestID)
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("list comments for pull request %d: %w", pullRequestID, err)
	}
	defer resp.Body.Close()

	var page struct {
		Values []prComment `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, fmt.Errorf("list comments for pull request %d: decode response: %w", pullRequestID, err)
	}
	return page.Values, nil
}

// CreatePullRequestComment posts a real, new, top-level comment --
// `POST .../pull-requests/{id}/comments`, body `{"text": "..."}`
// (go-bitbucket-v1's own real Comment request type, confirmed via its
// struct doc: `{Text string; Parent *Parent; Anchor *Anchor}` --
// omitting Parent is what makes this a top-level comment, not a reply).
func (c *Client) CreatePullRequestComment(ctx context.Context, projectKey, repositorySlug string, pullRequestID int, text string) error {
	body, err := json.Marshal(bitbucketv1.Comment{Text: text})
	if err != nil {
		return fmt.Errorf("create comment on pull request %d: %w", pullRequestID, err)
	}
	u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/pull-requests/%d/comments", c.baseURL, projectKey, repositorySlug, pullRequestID)
	resp, err := c.do(ctx, http.MethodPost, u, body)
	if err != nil {
		return fmt.Errorf("create comment on pull request %d: %w", pullRequestID, err)
	}
	resp.Body.Close()
	return nil
}

// UpdateComment edits commentID's own text in place -- `PUT
// .../pull-requests/{id}/comments/{commentId}`, real body `{"text":
// "...", "version": <n>}`. version is the comment's own real, current
// optimistic-lock version (from ListPullRequestComments), required on
// every update -- confirmed against Atlassian's own current REST API
// reference: a stale/omitted version is rejected outright, the same
// real discipline this project's own ledger-hash comparisons already
// apply elsewhere, just enforced server-side here instead.
func (c *Client) UpdateComment(ctx context.Context, projectKey, repositorySlug string, pullRequestID, commentID, version int, text string) error {
	body, err := json.Marshal(struct {
		Text    string `json:"text"`
		Version int    `json:"version"`
	}{Text: text, Version: version})
	if err != nil {
		return fmt.Errorf("update comment %d on pull request %d: %w", commentID, pullRequestID, err)
	}
	u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/pull-requests/%d/comments/%d",
		c.baseURL, projectKey, repositorySlug, pullRequestID, commentID)
	resp, err := c.do(ctx, http.MethodPut, u, body)
	if err != nil {
		return fmt.Errorf("update comment %d on pull request %d: %w", commentID, pullRequestID, err)
	}
	resp.Body.Close()
	return nil
}

// GetRawFile reads path's own real, current content at repositorySlug's
// default branch -- the real `GET .../raw/{path}` endpoint (confirmed
// against Atlassian's own current REST API reference; go-bitbucket-v1's
// own GetRawContent implements the identical path shape). Returns nil,
// nil (not an error) on a real 404 -- "this file doesn't exist," the
// same "absence is a valid outcome" contract
// azuredevops.Client.GetRawFile/gitlab's own raw-file fetch already
// establish, never surfaced as an error to a caller that's specifically
// probing several candidate CODEOWNERS locations in order.
func (c *Client) GetRawFile(ctx context.Context, projectKey, repositorySlug, path string) ([]byte, error) {
	u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/raw/%s", c.baseURL, projectKey, repositorySlug, url.PathEscape(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("read %s: status %d", path, resp.StatusCode)
	}
	body := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return body, nil
}

// permissionLevel is real, confirmed as the string values Bitbucket
// Server's own permissions API returns (bitbucketv1.PermissionRepository's
// own real constants: "REPO_READ", "REPO_WRITE", "REPO_ADMIN").
var writeOrAbovePermissions = map[string]bool{
	string(bitbucketv1.PermissionRepositoryWrite): true,
	string(bitbucketv1.PermissionRepositoryAdmin): true,
}

// HasWriteAccess reports whether username currently holds REPO_WRITE or
// REPO_ADMIN on repositorySlug -- the real `GET
// .../permissions/users?filter=<username>` endpoint (confirmed against
// the real, locally-verified go-bitbucket-v1 SDK source,
// GetUsersWithAnyPermission_24's own real path/query construction), a
// genuine, single-call "does user X have write access" check --
// structurally simpler than Azure DevOps' own ACL/security-namespace
// model, which has no such single-call endpoint at all (see
// hasContributorAccessAzureDevOps's own doc comment) and needed a
// group-membership approximation instead; Bitbucket Server's own
// project-and-repository permission model gives ubx a real, direct
// answer here, not an approximation.
func (c *Client) HasWriteAccess(ctx context.Context, projectKey, repositorySlug, username string) (bool, error) {
	if username == "" {
		return false, nil
	}
	u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/permissions/users?filter=%s",
		c.baseURL, projectKey, repositorySlug, url.QueryEscape(username))
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, fmt.Errorf("check write access for %s: %w", username, err)
	}
	defer resp.Body.Close()

	var page struct {
		Values []struct {
			User       bitbucketv1.UserWithLinks `json:"user"`
			Permission string                    `json:"permission"`
		} `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return false, fmt.Errorf("check write access for %s: decode response: %w", username, err)
	}
	for _, v := range page.Values {
		if v.User.Name == username && writeOrAbovePermissions[v.Permission] {
			return true, nil
		}
	}
	return false, nil
}
