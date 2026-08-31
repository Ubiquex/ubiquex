// server_api.go adds the real Bitbucket Cloud REST API v2 calls
// UBI-170's server/bitbucketcloud*.go needs beyond what client.go
// (`ubx accept --from-merge` only) already has: listing a pull
// request's changed files, posting/editing a comment, reading a raw
// file at a ref (CODEOWNERS), checking a user's own real repository
// permission, resolving a human-readable identifier to a real
// account_id, and reading the branch restriction that decides whether
// an approval can be correlated with a commit at all.
//
// Every path and response shape below was confirmed against Bitbucket
// Cloud's own official API definition (api.bitbucket.org/swagger.json).
package bitbucketcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrAccountNotResolved means a human-readable CODEOWNERS entry matched
// no workspace member at all. Refused rather than skipped: an entry
// that resolves to nobody must never silently authorize nobody in a way
// that looks like a clean negative, and must never be string-matched
// against a mutable label as a fallback (UBI-170).
var ErrAccountNotResolved = errors.New("no workspace member matches this CODEOWNERS entry")

// ErrAccountAmbiguous means a human-readable CODEOWNERS entry matched
// more than one real workspace member. Bitbucket Cloud does not
// guarantee nickname or display_name uniqueness, which is exactly why
// this case is real and is refused outright rather than resolved to
// whichever member happened to sort first.
var ErrAccountAmbiguous = errors.New("more than one workspace member matches this CODEOWNERS entry")

// paged is Bitbucket Cloud's own real pagination envelope. Unlike
// Bitbucket Server's start/limit/isLastPage, Cloud returns an absolute
// "next" URL, so following it needs no cursor arithmetic.
type paged struct {
	Next string `json:"next"`
}

// ListPullRequestChangedFiles returns every real, current changed-file
// path for pullRequestID, via `GET .../pullrequests/{id}/diffstat`.
//
// A real Bitbucket Cloud shape difference: each diffstat entry carries
// separate old/new file objects rather than one path, so a rename
// reports both. Both sides are returned when they differ, since a
// CODEOWNERS rule or a stack subtree can legitimately match either the
// path a file moved from or the path it moved to, and missing the
// former would silently under-authorize a real rename.
func (c *Client) ListPullRequestChangedFiles(ctx context.Context, workspace, repoSlug string, pullRequestID int64) ([]string, error) {
	u := fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%d/diffstat?pagelen=100",
		c.baseURL, url.PathEscape(workspace), url.PathEscape(repoSlug), pullRequestID)

	seen := map[string]bool{}
	paths := []string{}
	for u != "" {
		resp, err := c.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("list changed files for pull request %d: %w", pullRequestID, err)
		}
		var page struct {
			paged
			Values []struct {
				Status string `json:"status"`
				Old    *struct {
					Path string `json:"path"`
				} `json:"old"`
				New *struct {
					Path string `json:"path"`
				} `json:"new"`
			} `json:"values"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("list changed files for pull request %d: decode response: %w", pullRequestID, err)
		}
		for _, v := range page.Values {
			for _, side := range []*struct {
				Path string `json:"path"`
			}{v.New, v.Old} {
				if side == nil || side.Path == "" || seen[side.Path] {
					continue
				}
				seen[side.Path] = true
				paths = append(paths, side.Path)
			}
		}
		u = page.Next
	}
	return paths, nil
}

// Comment is the real, minimal shape of a Bitbucket Cloud pull request
// comment. Unlike Bitbucket Server, there is no version field and no
// optimistic-locking requirement on update.
type Comment struct {
	ID      int64 `json:"id"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
	User    Account `json:"user"`
	Deleted bool    `json:"deleted"`
}

// ListPullRequestComments returns every real, current comment on
// pullRequestID, following Bitbucket Cloud's own real "next" pagination
// to completion. Deleted comments are skipped: Bitbucket Cloud keeps
// them in the list with a deleted flag and an emptied body, and treating
// one as this bot's own last comment would edit a tombstone.
func (c *Client) ListPullRequestComments(ctx context.Context, workspace, repoSlug string, pullRequestID int64) ([]Comment, error) {
	u := fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%d/comments?pagelen=100",
		c.baseURL, url.PathEscape(workspace), url.PathEscape(repoSlug), pullRequestID)

	var out []Comment
	for u != "" {
		resp, err := c.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("list comments for pull request %d: %w", pullRequestID, err)
		}
		var page struct {
			paged
			Values []Comment `json:"values"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("list comments for pull request %d: decode response: %w", pullRequestID, err)
		}
		for _, v := range page.Values {
			if v.Deleted {
				continue
			}
			out = append(out, v)
		}
		u = page.Next
	}
	return out, nil
}

// commentBody is Bitbucket Cloud's own real comment request shape:
// the text lives under content.raw, not a flat "text" field the way
// Bitbucket Server's own does.
type commentBody struct {
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
}

func newCommentBody(text string) ([]byte, error) {
	var b commentBody
	b.Content.Raw = text
	return json.Marshal(b)
}

// CreatePullRequestComment posts a real, new, top-level comment.
func (c *Client) CreatePullRequestComment(ctx context.Context, workspace, repoSlug string, pullRequestID int64, text string) error {
	body, err := newCommentBody(text)
	if err != nil {
		return fmt.Errorf("create comment on pull request %d: %w", pullRequestID, err)
	}
	u := fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%d/comments",
		c.baseURL, url.PathEscape(workspace), url.PathEscape(repoSlug), pullRequestID)
	resp, err := c.do(ctx, http.MethodPost, u, body)
	if err != nil {
		return fmt.Errorf("create comment on pull request %d: %w", pullRequestID, err)
	}
	resp.Body.Close()
	return nil
}

// UpdateComment edits an existing comment in place -- a real PUT, with
// no version echo required (unlike Bitbucket Server, which enforces
// optimistic locking on every comment update).
func (c *Client) UpdateComment(ctx context.Context, workspace, repoSlug string, pullRequestID, commentID int64, text string) error {
	body, err := newCommentBody(text)
	if err != nil {
		return fmt.Errorf("update comment %d: %w", commentID, err)
	}
	u := fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%d/comments/%d",
		c.baseURL, url.PathEscape(workspace), url.PathEscape(repoSlug), pullRequestID, commentID)
	resp, err := c.do(ctx, http.MethodPut, u, body)
	if err != nil {
		return fmt.Errorf("update comment %d: %w", commentID, err)
	}
	resp.Body.Close()
	return nil
}

// GetRawFile reads path at ref, via `GET .../src/{commit}/{path}`.
// Returns nil, nil (not an error) when the file does not exist at that
// ref, the same convention every other platform package's own raw-file
// reader uses, so a missing CODEOWNERS is a real, ordinary outcome.
func (c *Client) GetRawFile(ctx context.Context, workspace, repoSlug, ref, path string) ([]byte, error) {
	u := fmt.Sprintf("%s/repositories/%s/%s/src/%s/%s",
		c.baseURL, url.PathEscape(workspace), url.PathEscape(repoSlug), url.PathEscape(ref), path)
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		if strings.Contains(err.Error(), "status 404") {
			return nil, nil
		}
		return nil, fmt.Errorf("get raw file %s at %s: %w", path, ref, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("get raw file %s at %s: %w", path, ref, err)
	}
	return body, nil
}

// repoPermission is one entry of Bitbucket Cloud's own real
// per-repository permission listing.
type repoPermission struct {
	Permission string  `json:"permission"`
	User       Account `json:"user"`
}

// HasWriteAccess reports whether accountID has real, current write (or
// admin) permission on workspace/repoSlug, via `GET /workspaces/
// {workspace}/permissions/repositories/{repo_slug}`.
//
// Matched on account_id, never on a label: this answers an
// authorization question, and nickname/display_name are both
// user-changeable. Bitbucket Cloud's own permission values are read,
// write, and admin; only the latter two authorize.
func (c *Client) HasWriteAccess(ctx context.Context, workspace, repoSlug, accountID string) (bool, error) {
	if accountID == "" {
		return false, nil
	}
	u := fmt.Sprintf("%s/workspaces/%s/permissions/repositories/%s?pagelen=100",
		c.baseURL, url.PathEscape(workspace), url.PathEscape(repoSlug))

	for u != "" {
		resp, err := c.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			return false, fmt.Errorf("list repository permissions for %s/%s: %w", workspace, repoSlug, err)
		}
		var page struct {
			paged
			Values []repoPermission `json:"values"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return false, fmt.Errorf("list repository permissions for %s/%s: decode response: %w", workspace, repoSlug, err)
		}
		for _, v := range page.Values {
			if v.User.Identity() != accountID {
				continue
			}
			return v.Permission == "write" || v.Permission == "admin", nil
		}
		u = page.Next
	}
	return false, nil
}

// WorkspaceMembers returns every real, current member of workspace, as
// one snapshot. Exported so a caller resolving several CODEOWNERS
// entries at once pays for a single listing rather than one per entry.
func (c *Client) WorkspaceMembers(ctx context.Context, workspace string) ([]Account, error) {
	u := fmt.Sprintf("%s/workspaces/%s/members?pagelen=100", c.baseURL, url.PathEscape(workspace))
	var out []Account
	for u != "" {
		resp, err := c.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("list workspace members for %s: %w", workspace, err)
		}
		var page struct {
			paged
			Values []struct {
				User Account `json:"user"`
			} `json:"values"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("list workspace members for %s: decode response: %w", workspace, err)
		}
		for _, v := range page.Values {
			if v.User.Identity() != "" {
				out = append(out, v.User)
			}
		}
		u = page.Next
	}
	return out, nil
}

// ResolveAccountID resolves one human-readable CODEOWNERS entry to a
// real, current Bitbucket Cloud account_id, against an already-fetched
// members snapshot.
//
// This exists because Bitbucket Cloud accounts have no username at all
// (UBI-170). Every other platform this project covers has a stable
// login a CODEOWNERS author can write down and ubx can string-match;
// here the only stable identifier is an account_id no human would ever
// put in a CODEOWNERS file, so the human-readable form has to be
// resolved to it at check time, on every check, against live data.
//
// entry is matched case-insensitively against nickname first, then
// display_name -- nickname is the closer analog of a login, so a
// nickname match wins outright and display_name is only consulted when
// no nickname matched at all. Zero matches is ErrAccountNotResolved and
// more than one is ErrAccountAmbiguous: both are refusals, never a
// guess, since either direction is a real authorization defect.
func ResolveAccountID(members []Account, entry string) (string, error) {
	entry = strings.TrimPrefix(strings.TrimSpace(entry), "@")
	if entry == "" {
		return "", ErrAccountNotResolved
	}

	var byNickname, byDisplayName []string
	for _, m := range members {
		id := m.Identity()
		if id == "" {
			continue
		}
		if strings.EqualFold(m.Nickname, entry) {
			byNickname = append(byNickname, id)
			continue
		}
		if strings.EqualFold(m.DisplayName, entry) {
			byDisplayName = append(byDisplayName, id)
		}
	}

	matches := byNickname
	if len(matches) == 0 {
		matches = byDisplayName
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("%w: %q", ErrAccountNotResolved, entry)
	default:
		return "", fmt.Errorf("%w: %q matches %d members", ErrAccountAmbiguous, entry, len(matches))
	}
}

// ResetApprovalsOnChange reports whether workspace/repoSlug has a real,
// current branch restriction that clears existing approvals when new
// changes land, scoped to targetBranch.
//
// This is Bitbucket Cloud's own real TOCTOU primitive, and it is
// structurally the GitLab/Azure DevOps pattern, NOT Bitbucket Server's.
// Bitbucket Server delivers the exact commit each reviewer approved on
// the webhook payload itself (lastReviewedCommit), so no separate query
// is needed there at all. A Bitbucket Cloud participant object carries
// no commit reference whatsoever -- confirmed against both the official
// schema and the live API -- so the only real way to correlate an
// approval with a commit is to confirm the repository is configured to
// discard approvals whenever the source changes.
//
// Both real kinds count: "reset_pullrequest_approvals_on_change" clears
// every approval on any new change, and
// "smart_reset_pullrequest_approvals" clears them only when the change
// is substantive. Either one means a currently-recorded approval refers
// to the current head, which is the property the acceptance gate needs.
//
// Returns found=false when no such restriction applies to targetBranch,
// which callers must treat as "cannot safely derive acceptance" rather
// than as permission to proceed.
func (c *Client) ResetApprovalsOnChange(ctx context.Context, workspace, repoSlug, targetBranch string) (found bool, err error) {
	u := fmt.Sprintf("%s/repositories/%s/%s/branch-restrictions?pagelen=100",
		c.baseURL, url.PathEscape(workspace), url.PathEscape(repoSlug))

	for u != "" {
		resp, err := c.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			return false, fmt.Errorf("list branch restrictions for %s/%s: %w", workspace, repoSlug, err)
		}
		var page struct {
			paged
			Values []struct {
				Kind            string `json:"kind"`
				Pattern         string `json:"pattern"`
				BranchMatchKind string `json:"branch_match_kind"`
				BranchType      string `json:"branch_type"`
			} `json:"values"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return false, fmt.Errorf("list branch restrictions for %s/%s: decode response: %w", workspace, repoSlug, err)
		}
		for _, v := range page.Values {
			if v.Kind != "reset_pullrequest_approvals_on_change" && v.Kind != "smart_reset_pullrequest_approvals" {
				continue
			}
			if branchRestrictionApplies(v.BranchMatchKind, v.Pattern, targetBranch) {
				return true, nil
			}
		}
		u = page.Next
	}
	return false, nil
}

// branchRestrictionApplies reports whether a real branch restriction
// covers branch.
//
// Bitbucket Cloud's own branch_match_kind is either "glob" (pattern is
// a glob against the branch name) or "branching_model" (pattern is
// empty and the restriction applies to a named branch type instead).
// Only glob is interpreted here, and only with the one real wildcard
// form Bitbucket Cloud's own patterns use; a branching_model
// restriction is deliberately NOT treated as applying, because ubx
// cannot know which concrete branches a workspace's branching model
// maps to without a further live lookup, and guessing in the
// permissive direction would weaken exactly the gate this function
// exists to enforce.
func branchRestrictionApplies(matchKind, pattern, branch string) bool {
	if branch == "" {
		return false
	}
	if matchKind != "" && matchKind != "glob" {
		return false
	}
	return globMatches(pattern, branch)
}

// globMatches implements the real subset of glob syntax Bitbucket
// Cloud's own branch patterns use: literal text plus "*" wildcards.
// Written directly rather than via path.Match because a Bitbucket Cloud
// branch pattern's "*" legitimately spans "/" (a "release/*" pattern
// matches "release/1.2/hotfix"), which path.Match's own "*" does not.
func globMatches(pattern, s string) bool {
	if pattern == "" {
		return false
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	last := parts[len(parts)-1]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return strings.HasSuffix(s, last)
}
