// server_api.go is UBI-28 Phase 3's own real extension of package
// azuredevops: everything `ubx server`'s own webhook/auth/comment
// layers need beyond UBI-160 Phase 2's original merge-derived-
// acceptance scope (client.go). Same real discipline as that file's
// own doc comment: direct net/http REST calls against Microsoft's own
// current, stable, documented endpoints, using the official SDK's
// struct types only for accurate JSON shapes, never routing through
// the SDK's own Client.Send/resource-location discovery -- keeps this
// package callable against a real httptest server exactly like every
// other call here.
//
// Two real, distinct Azure DevOps API hosts are involved, confirmed
// directly against Microsoft's own current REST reference docs, not
// assumed: `dev.azure.com` for git/policy calls (Client.baseURL, same
// as client.go) and `vssps.dev.azure.com` for Graph calls (identity/
// group resolution and membership checks) -- a real, structural
// quirk neither GitHub nor GitLab's own single-host clients have.
// WithGraphBaseURL lets tests point both at the same httptest server;
// production defaults graphBaseURL from organization independently.
package azuredevops

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/graph"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/policy"
)

// minimumApproverCountPolicyTypeID is the real, confirmed, well-known
// Azure DevOps policy type GUID for the "Minimum number of reviewers"
// branch policy (its own real, current REST API display name is
// "Minimum approval count") -- confirmed directly against Microsoft's
// own official "Policy Configurations - List" REST reference example
// response, not a guessed or search-summarized value (an initial web
// search surfaced two candidate GUIDs differing in their last
// segment; only this one is confirmed by Microsoft's own real,
// official example JSON).
const minimumApproverCountPolicyTypeID = "fa4e907d-c16b-4a4c-9dfa-4906e5d171dd"

// GetPullRequest fetches project/repositoryID's own pull request prID
// fresh -- the Azure DevOps analog of ghapi's PullRequests.Get /
// glapi's MergeRequests.GetMergeRequest, used the same real way: never
// trust a webhook payload's own claims about current PR state (title,
// reviewers, target branch) when a fresh read is cheap and the
// payload could be stale by the time it's handled.
func (c *Client) GetPullRequest(ctx context.Context, project, repositoryID string, prID int) (*git.GitPullRequest, error) {
	u := fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullrequests/%d?api-version=7.1", c.baseURL, project, repositoryID, prID)
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("get pull request %d: %w", prID, err)
	}
	defer resp.Body.Close()
	var pr git.GitPullRequest
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, fmt.Errorf("get pull request %d: decode response: %w", prID, err)
	}
	return &pr, nil
}

// ListPullRequestChangedFiles returns every real, current repo-relative
// path prID's own latest iteration touches -- the Azure DevOps analog
// of GitHub's PullRequests.ListFiles / GitLab's
// ListMergeRequestDiffs, against a real, structurally different API
// shape: Azure DevOps has no single "changed files" endpoint on the PR
// itself; a real PR is a sequence of Iterations (one per push), and
// only the LATEST iteration's own real Changes reflect the PR's
// current, full diff against its target branch (confirmed directly
// against Microsoft's own current REST reference: earlier iterations'
// own changes are already superseded). GitPullRequestChange.Item is
// untyped JObject on the wire (no strong SDK type), decoded here just
// far enough to read its own real "path" field; SourceServerItem is
// used as a real fallback for a deleted item, whose own Item has no
// current path at all -- the same real "deleted file has no NewPath"
// case ownsAsGitLab's own diff-walking loop already handles for
// GitLab.
func (c *Client) ListPullRequestChangedFiles(ctx context.Context, project, repositoryID string, prID int) ([]string, error) {
	iterURL := fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullRequests/%d/iterations?api-version=7.1", c.baseURL, project, repositoryID, prID)
	resp, err := c.do(ctx, http.MethodGet, iterURL, nil)
	if err != nil {
		return nil, fmt.Errorf("list iterations for PR %d: %w", prID, err)
	}
	var iterResult struct {
		Value []git.GitPullRequestIteration `json:"value"`
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&iterResult)
	resp.Body.Close()
	if decodeErr != nil {
		return nil, fmt.Errorf("list iterations for PR %d: decode response: %w", prID, decodeErr)
	}
	if len(iterResult.Value) == 0 {
		return nil, nil
	}
	latest := iterResult.Value[0]
	for _, it := range iterResult.Value {
		if it.Id != nil && (latest.Id == nil || *it.Id > *latest.Id) {
			latest = it
		}
	}
	if latest.Id == nil {
		return nil, nil
	}

	changesURL := fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullRequests/%d/iterations/%d/changes?api-version=7.1",
		c.baseURL, project, repositoryID, prID, *latest.Id)
	resp, err = c.do(ctx, http.MethodGet, changesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("get changes for PR %d iteration %d: %w", prID, *latest.Id, err)
	}
	defer resp.Body.Close()
	var changes git.GitPullRequestIterationChanges
	if err := json.NewDecoder(resp.Body).Decode(&changes); err != nil {
		return nil, fmt.Errorf("get changes for PR %d iteration %d: decode response: %w", prID, *latest.Id, err)
	}
	if changes.ChangeEntries == nil {
		return nil, nil
	}

	paths := make([]string, 0, len(*changes.ChangeEntries))
	for _, entry := range *changes.ChangeEntries {
		var item struct {
			Path string `json:"path"`
		}
		if entry.Item != nil {
			itemJSON, err := json.Marshal(entry.Item)
			if err == nil {
				_ = json.Unmarshal(itemJSON, &item)
			}
		}
		path := item.Path
		if path == "" && entry.SourceServerItem != nil {
			path = *entry.SourceServerItem
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// ListPullRequestThreads returns every real comment thread on prID --
// Azure DevOps' own real comment concept: a PR comment is never a
// flat, top-level entry the way a GitHub issue comment or a GitLab
// note is; it's always the first Comment inside a
// GitPullRequestCommentThread, with any replies as further Comments in
// the same thread. postOrEditCommentAzureDevOps (server/
// azuredevops_comment.go) walks this list looking for its own last
// marked thread.
func (c *Client) ListPullRequestThreads(ctx context.Context, project, repositoryID string, prID int) ([]*git.GitPullRequestCommentThread, error) {
	u := fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullRequests/%d/threads?api-version=7.1", c.baseURL, project, repositoryID, prID)
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("list threads for PR %d: %w", prID, err)
	}
	defer resp.Body.Close()
	var result struct {
		Value []*git.GitPullRequestCommentThread `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("list threads for PR %d: decode response: %w", prID, err)
	}
	return result.Value, nil
}

// CreatePullRequestThread opens a real, new comment thread on prID
// with one real, initial Text comment, status Active -- the only real
// way to post what GitHub/GitLab would call a fresh top-level comment,
// since Azure DevOps has no bare "create a comment" endpoint at all.
func (c *Client) CreatePullRequestThread(ctx context.Context, project, repositoryID string, prID int, content string) error {
	commentType := git.CommentTypeValues.Text
	status := git.CommentThreadStatusValues.Active
	body, err := json.Marshal(map[string]any{
		"comments": []map[string]any{{"content": content, "commentType": commentType}},
		"status":   status,
	})
	if err != nil {
		return fmt.Errorf("marshal new thread: %w", err)
	}
	u := fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullRequests/%d/threads?api-version=7.1", c.baseURL, project, repositoryID, prID)
	resp, err := c.do(ctx, http.MethodPost, u, body)
	if err != nil {
		return fmt.Errorf("create thread on PR %d: %w", prID, err)
	}
	defer resp.Body.Close()
	return nil
}

// UpdateThreadComment edits commentID's own real content within
// threadID -- the edit-in-place half of the same real mechanism
// CreatePullRequestThread's own doc comment describes.
func (c *Client) UpdateThreadComment(ctx context.Context, project, repositoryID string, prID, threadID, commentID int, content string) error {
	body, err := json.Marshal(map[string]any{"content": content})
	if err != nil {
		return fmt.Errorf("marshal comment update: %w", err)
	}
	u := fmt.Sprintf("%s/%s/_apis/git/repositories/%s/pullRequests/%d/threads/%d/comments/%d?api-version=7.1",
		c.baseURL, project, repositoryID, prID, threadID, commentID)
	resp, err := c.do(ctx, http.MethodPatch, u, body)
	if err != nil {
		return fmt.Errorf("update comment %d in thread %d: %w", commentID, threadID, err)
	}
	defer resp.Body.Close()
	return nil
}

// GetRawFile reads path's own real, current content from repositoryID's
// default branch -- ubx's own real CODEOWNERS-file convention overlaid
// on top of Azure DevOps (which, confirmed directly against Microsoft's
// own current docs, has no native CODEOWNERS support at all, unlike
// GitHub and GitLab; "Automatically included reviewers" is Azure
// DevOps' own real, different, UI/API-configured equivalent, not a
// repo file), via the real, documented "Items - Get" API. Returns nil,
// nil (not an error) on a 404 -- the same "no CODEOWNERS file at all"
// real outcome fetchCodeownersFileGitLab's own doc comment describes.
func (c *Client) GetRawFile(ctx context.Context, project, repositoryID, path string) ([]byte, error) {
	u := fmt.Sprintf("%s/%s/_apis/git/repositories/%s/items?path=%s&api-version=7.1",
		c.baseURL, project, repositoryID, url.QueryEscape(path))
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		if strings.Contains(err.Error(), "status 404") {
			return nil, nil
		}
		return nil, fmt.Errorf("get item %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("get item %s: read response: %w", path, err)
	}
	return body, nil
}

// resetOnSourcePushSettings is the real, minimal subset of the
// "Minimum approval count" policy type's own real settings shape
// (confirmed against Microsoft's own official REST example response)
// this package actually needs -- PolicyConfiguration.Settings is
// untyped JObject on the wire, so this is decoded manually rather than
// relying on the SDK's own (nonexistent) strong type for it.
type resetOnSourcePushSettings struct {
	ResetOnSourcePush *bool `json:"resetOnSourcePush"`
	Scope             []struct {
		RefName      *string `json:"refName"`
		MatchKind    *string `json:"matchKind"`
		RepositoryId *string `json:"repositoryId"`
	} `json:"scope"`
}

// ResetOnSourcePush reports whether project has a real, enabled
// "Minimum approval count" branch policy, scoped to repositoryID and
// targetRefName, with resetOnSourcePush turned on -- the Azure DevOps
// real, structural counterpart of GitLab's ResetApprovalsOnPush
// project setting, and the same real safety property GitHub gets for
// free from `review.commit_id`: without this, Azure DevOps' own vote
// objects carry no per-vote commit/iteration reference at all
// (confirmed directly against the real, downloaded SDK's own
// IdentityRefWithVote struct -- no such field exists), so a currently-
// recorded "approved" vote can only be trusted as corresponding to the
// PR's own current head commit if this specific policy is both present
// and configured to clear votes on every new push. found is false when
// no enabled policy of this type matches repositoryID/targetRefName at
// all -- a real, distinct outcome from "found, but resetOnSourcePush is
// off," both of which the caller must refuse acceptance for, but worth
// distinguishing in the real, posted refusal message.
func (c *Client) ResetOnSourcePush(ctx context.Context, project, repositoryID, targetRefName string) (found, resetOnPush bool, err error) {
	u := fmt.Sprintf("%s/%s/_apis/policy/configurations?api-version=7.1", c.baseURL, project)
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, false, fmt.Errorf("list policy configurations: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		Value []policy.PolicyConfiguration `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, false, fmt.Errorf("list policy configurations: decode response: %w", err)
	}

	for _, cfg := range result.Value {
		if cfg.Type == nil || cfg.Type.Id == nil || cfg.Type.Id.String() != minimumApproverCountPolicyTypeID {
			continue
		}
		if cfg.IsEnabled == nil || !*cfg.IsEnabled {
			continue
		}
		settingsJSON, err := json.Marshal(cfg.Settings)
		if err != nil {
			continue
		}
		var settings resetOnSourcePushSettings
		if err := json.Unmarshal(settingsJSON, &settings); err != nil {
			continue
		}
		if !policyScopeMatches(settings.Scope, repositoryID, targetRefName) {
			continue
		}
		found = true
		if settings.ResetOnSourcePush != nil && *settings.ResetOnSourcePush {
			return true, true, nil
		}
	}
	return found, false, nil
}

// policyScopeMatches reports whether any real scope entry applies to
// repositoryID/targetRefName -- a real, project-wide policy config can
// carry more than one scope entry (one per branch/repo it applies to),
// each independently real/current: repositoryId null means "every repo
// in the project," matchKind "Exact" needs an exact refName match,
// "Prefix" needs targetRefName to start with refName -- confirmed
// directly against Microsoft's own official example response shape.
func policyScopeMatches(scope []struct {
	RefName      *string `json:"refName"`
	MatchKind    *string `json:"matchKind"`
	RepositoryId *string `json:"repositoryId"`
}, repositoryID, targetRefName string) bool {
	for _, s := range scope {
		if s.RepositoryId != nil && *s.RepositoryId != "" && !strings.EqualFold(*s.RepositoryId, repositoryID) {
			continue
		}
		if s.RefName == nil {
			continue
		}
		switch {
		case s.MatchKind != nil && *s.MatchKind == "Prefix":
			if strings.HasPrefix(targetRefName, *s.RefName) {
				return true
			}
		default: // "Exact", or unset -- treated as exact, the real documented default
			if targetRefName == *s.RefName {
				return true
			}
		}
	}
	return false
}

// QuerySubjectDescriptor resolves query (a real display name or unique
// name/email prefix) to the first matching real Azure DevOps Graph
// subject descriptor of the given kind ("User" or "Group") -- the real
// identifier every Graph membership call needs, since neither webhook
// payloads nor a CODEOWNERS file's own plain-text owner entries carry
// one directly. Real, documented prefix-match search (Microsoft's own
// "Subject Query" API); the first result is used, the same real
// skeleton-scope simplification client.go's own pullRequestForCommit
// already documents for an analogous "more than one match, in
// principle" case.
func (c *Client) QuerySubjectDescriptor(ctx context.Context, query, kind string) (descriptor string, ok bool, err error) {
	body, err := json.Marshal(map[string]any{"query": query, "subjectKind": []string{kind}})
	if err != nil {
		return "", false, fmt.Errorf("marshal subject query: %w", err)
	}
	u := fmt.Sprintf("%s/_apis/graph/subjectquery?api-version=7.1-preview.1", c.graphBaseURL)
	resp, err := c.do(ctx, http.MethodPost, u, body)
	if err != nil {
		return "", false, fmt.Errorf("query subjects %q: %w", query, err)
	}
	defer resp.Body.Close()
	var subjects []graph.GraphSubject
	if err := json.NewDecoder(resp.Body).Decode(&subjects); err != nil {
		return "", false, fmt.Errorf("query subjects %q: decode response: %w", query, err)
	}
	if len(subjects) == 0 || subjects[0].Descriptor == nil {
		return "", false, nil
	}
	return *subjects[0].Descriptor, true, nil
}

// CheckMembership reports whether subjectDescriptor is a real, current
// member of containerDescriptor (a group) -- the Azure DevOps analog of
// GitHub's GetTeamMembershipBySlug / GitLab's GetInheritedGroupMember,
// against Microsoft's own real, documented "Memberships - Check
// Membership Existence" API (200 = member, 404 = not, both real,
// valid outcomes -- never an error on 404 specifically).
func (c *Client) CheckMembership(ctx context.Context, subjectDescriptor, containerDescriptor string) (bool, error) {
	u := fmt.Sprintf("%s/_apis/graph/memberships/%s/%s?api-version=7.1-preview.1",
		c.graphBaseURL, url.PathEscape(subjectDescriptor), url.PathEscape(containerDescriptor))
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		if strings.Contains(err.Error(), "status 404") {
			return false, nil
		}
		return false, fmt.Errorf("check membership: %w", err)
	}
	defer resp.Body.Close()
	return true, nil
}
