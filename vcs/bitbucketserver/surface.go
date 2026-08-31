package bitbucketserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// ErrNoIssueTracker means Bitbucket Server has no issue tracker to open
// an issue in, so `--surface-as issue` cannot be honored here.
//
// This is a real, structural property of the product, not a gap in ubx
// and not a permissions problem. Bitbucket Server/Data Center ships no
// issue-tracking feature at all; Atlassian's own model is that issue
// tracking lives in Jira, a separate product with its own separate API
// and its own separate credentials. Confirmed against the real,
// locally-downloaded Bitbucket Server REST SDK
// (github.com/gfleury/go-bitbucket-v1): 224 exported API methods, of
// which exactly zero touch any issue resource, while pull requests,
// branches and default-branch lookups are all present.
//
// Refused loudly rather than silently downgraded to a pull request: an
// operator who asked for an issue and silently got a pull request that
// commits a file to their repository would be getting something
// materially more privileged than they asked for.
var ErrNoIssueTracker = errors.New("bitbucket server has no issue tracker (Atlassian's own model is Jira); use --surface-as pr instead")

// PullRequest is what OpenDraftPR returns.
type PullRequest struct {
	Number  int64
	HTMLURL string
}

// OpenDraftPR is github.OpenDraftPR's Bitbucket Server counterpart.
//
// Three real calls, not GitHub's four, because Bitbucket Server's own
// file-write endpoint creates the branch and the commit together:
// PUT .../browse/{path} takes a `branch` to commit on and a
// `sourceBranch` naming the starting point for it when that branch does
// not exist yet. Confirmed against the real, downloaded SDK's own
// documented EditFile contract, which spells out exactly that:
// "The file can be updated or created on a new branch. In this case,
// the sourceBranch parameter should be provided to identify the
// starting point for the new branch and the branch parameter identifies
// the branch to create the new commit on."
//
// The request is multipart/form-data, not JSON -- the one endpoint in
// this package that is. sourceCommitId is deliberately omitted, which
// is the documented way to say "this is a new file" rather than an
// edit of a known revision.
//
// Bitbucket Server has no draft pull request concept at all (unlike
// GitHub's own draft flag and Azure DevOps' isDraft), so "draft" here
// is carried in the title the same way GitLab carries it, rather than
// being silently dropped.
func OpenDraftPR(ctx context.Context, api *Client, projectKey, repositorySlug, branch, filePath string, fileContent []byte, commitMessage, prTitle, prDescription string) (*PullRequest, error) {
	defaultBranch, err := api.defaultBranchID(ctx, projectKey, repositorySlug)
	if err != nil {
		return nil, err
	}

	if err := api.commitFileOnNewBranch(ctx, projectKey, repositorySlug, branch, defaultBranch, filePath, fileContent, commitMessage); err != nil {
		return nil, err
	}

	body, err := json.Marshal(map[string]any{
		"title":       draftTitle(prTitle),
		"description": prDescription,
		"fromRef": map[string]any{
			"id":         "refs/heads/" + branch,
			"repository": map[string]any{"slug": repositorySlug, "project": map[string]string{"key": projectKey}},
		},
		"toRef": map[string]any{
			"id":         defaultBranch,
			"repository": map[string]any{"slug": repositorySlug, "project": map[string]string{"key": projectKey}},
		},
	})
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/pull-requests", api.baseURL, projectKey, repositorySlug)
	resp, err := api.do(ctx, http.MethodPost, u, body)
	if err != nil {
		return nil, fmt.Errorf("open pull request: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		ID    int64 `json:"id"`
		Links struct {
			Self []struct {
				Href string `json:"href"`
			} `json:"self"`
		} `json:"links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode pull request response: %w", err)
	}
	pr := &PullRequest{Number: out.ID}
	if len(out.Links.Self) > 0 {
		pr.HTMLURL = out.Links.Self[0].Href
	}
	return pr, nil
}

// defaultBranchID returns the repository's own default branch as a full
// ref ID ("refs/heads/main"), which is the form both the file-write and
// the pull request endpoints want.
func (c *Client) defaultBranchID(ctx context.Context, projectKey, repositorySlug string) (string, error) {
	u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/branches/default", c.baseURL, projectKey, repositorySlug)
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("get default branch: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode default branch response: %w", err)
	}
	if out.ID == "" {
		return "", fmt.Errorf("repository %s/%s reports no default branch", projectKey, repositorySlug)
	}
	return out.ID, nil
}

// commitFileOnNewBranch writes filePath on a branch that does not exist
// yet, branching from sourceBranch, in one real call.
func (c *Client) commitFileOnNewBranch(ctx context.Context, projectKey, repositorySlug, branch, sourceBranch, filePath string, content []byte, message string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("content", filePath)
	if err != nil {
		return err
	}
	if _, err := part.Write(content); err != nil {
		return err
	}
	for k, v := range map[string]string{
		"message":      message,
		"branch":       branch,
		"sourceBranch": sourceBranch,
	} {
		if err := mw.WriteField(k, v); err != nil {
			return err
		}
	}
	if err := mw.Close(); err != nil {
		return err
	}

	u := fmt.Sprintf("%s/rest/api/1.0/projects/%s/repos/%s/browse/%s", c.baseURL, projectKey, repositorySlug, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("commit %s to %s: %w", filePath, branch, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("commit %s to %s: status %d: %s", filePath, branch, resp.StatusCode, string(b))
	}
	return nil
}

// draftTitle marks the pull request as a draft in its own title,
// Bitbucket Server having no real draft flag of its own.
func draftTitle(title string) string {
	if len(title) >= 6 && (title[:6] == "Draft:" || title[:6] == "draft:") {
		return title
	}
	return "Draft: " + title
}
