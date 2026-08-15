package azuredevops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// DefaultWorkItemType is what CreateWorkItem uses when the caller names
// no type.
//
// "Issue" is a real work item type in Azure DevOps' own Agile and Basic
// process templates, which is why it is the default and the closest
// analogue of a GitHub issue. It does NOT exist in the Scrum or CMMI
// templates, whose equivalents are "Product Backlog Item" and
// "Requirement". A project's process template is chosen at creation
// time and cannot be inferred from the repository, so a Scrum or CMMI
// project must name its own type explicitly rather than have ubx guess
// one that would fail at the API.
const DefaultWorkItemType = "Issue"

// WorkItem is what CreateWorkItem returns -- Azure DevOps' own
// counterpart of github.Issue.
//
// Deliberately named for what Azure DevOps actually creates rather than
// for "issue". Azure DevOps has no issue tracker as such: work items
// live in Azure Boards, a genuinely separate service from Repos that a
// project can have disabled entirely, and their type comes from the
// project's own process template. Calling this an Issue would paper
// over both facts (UBI-52's own package/naming rule applied to a type).
type WorkItem struct {
	ID      int64
	HTMLURL string
}

// PullRequest is what OpenDraftPR returns.
type PullRequest struct {
	Number  int64
	HTMLURL string
}

// jsonPatchOp is one operation in the JSON Patch document Azure DevOps'
// own work item API requires. Confirmed directly against Microsoft's own
// published REST spec (specification/wit/7.1, operation
// "Work Items_Create"): the route is
// POST {project}/_apis/wit/workitems/${type} with a literal "$" before
// the type name, and the content type is application/json-patch+json,
// not application/json. Neither detail is guessable from the other
// platforms.
type jsonPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// CreateWorkItem opens a new work item carrying the drift receipt
// (UBI-165), the Azure DevOps counterpart of github.CreateIssue.
//
// workItemType empty means DefaultWorkItemType. A type the project's own
// process does not define is rejected by Azure DevOps itself, which is
// the right place for that to fail: ubx cannot know a project's process
// template, and guessing would be worse than a clear API error.
//
// The receipt goes in System.Description, which Azure DevOps renders as
// HTML rather than as Markdown. It is sent unchanged anyway: the receipt
// is deliberately identical across all five platforms, and a work item
// showing literal Markdown is far better than a receipt that silently
// differs from the one every other platform records.
func CreateWorkItem(ctx context.Context, api *Client, project, workItemType, title, body string) (*WorkItem, error) {
	if workItemType == "" {
		workItemType = DefaultWorkItemType
	}
	doc := []jsonPatchOp{
		{Op: "add", Path: "/fields/System.Title", Value: title},
		{Op: "add", Path: "/fields/System.Description", Value: body},
	}
	payload, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encode work item patch document: %w", err)
	}

	u := fmt.Sprintf("%s/%s/_apis/wit/workitems/$%s?api-version=7.1",
		api.baseURL, project, workItemType)
	resp, err := api.doWithContentType(ctx, http.MethodPost, u, payload, "application/json-patch+json")
	if err != nil {
		return nil, fmt.Errorf("create work item: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		ID    int64 `json:"id"`
		Links struct {
			HTML struct {
				Href string `json:"href"`
			} `json:"html"`
		} `json:"_links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode work item response: %w", err)
	}
	return &WorkItem{ID: out.ID, HTMLURL: out.Links.HTML.Href}, nil
}

// OpenDraftPR is github.OpenDraftPR's Azure DevOps counterpart.
//
// Five real calls, and the shape is genuinely its own rather than
// GitHub's with names changed. Every path and body below is confirmed
// against Microsoft's own published REST spec and its own request
// examples (specification/git/7.1):
//
//  1. GET the repository, for its own real defaultBranch (a full ref
//     name, "refs/heads/main", not a bare branch name).
//  2. GET that ref, for the object ID to branch from.
//  3. POST refs, creating the new branch. oldObjectId is the all-zero
//     SHA, Azure DevOps' own way of saying "this ref must not exist
//     yet".
//  4. POST pushes, committing the file. Branch creation and commit are
//     separate here, unlike Bitbucket Server and Bitbucket Cloud, which
//     each do both in one call.
//  5. POST pullrequests.
//
// Azure DevOps has a real isDraft flag on a pull request, so unlike
// GitLab (title convention only) the draft state is set properly.
func OpenDraftPR(ctx context.Context, api *Client, project, repositoryID, branch, filePath string, fileContent []byte, commitMessage, prTitle, prDescription string) (*PullRequest, error) {
	base := fmt.Sprintf("%s/%s/_apis/git/repositories/%s", api.baseURL, project, repositoryID)

	defaultRef, err := repositoryDefaultRef(ctx, api, base)
	if err != nil {
		return nil, err
	}
	baseObjectID, err := refObjectID(ctx, api, base, defaultRef)
	if err != nil {
		return nil, err
	}

	newRef := "refs/heads/" + branch
	createRef, err := json.Marshal([]map[string]string{{
		"name":        newRef,
		"oldObjectId": zeroObjectID,
		"newObjectId": baseObjectID,
	}})
	if err != nil {
		return nil, err
	}
	resp, err := api.do(ctx, http.MethodPost, base+"/refs?api-version=7.1", createRef)
	if err != nil {
		return nil, fmt.Errorf("create branch %q: %w", branch, err)
	}
	resp.Body.Close()

	push, err := json.Marshal(map[string]any{
		"refUpdates": []map[string]string{{"name": newRef, "oldObjectId": baseObjectID}},
		"commits": []map[string]any{{
			"comment": commitMessage,
			"changes": []map[string]any{{
				"changeType": "add",
				"item":       map[string]string{"path": "/" + strings.TrimPrefix(filePath, "/")},
				"newContent": map[string]string{"content": string(fileContent), "contentType": "rawtext"},
			}},
		}},
	})
	if err != nil {
		return nil, err
	}
	resp, err = api.do(ctx, http.MethodPost, base+"/pushes?api-version=7.1", push)
	if err != nil {
		return nil, fmt.Errorf("commit %s to %s: %w", filePath, branch, err)
	}
	resp.Body.Close()

	prBody, err := json.Marshal(map[string]any{
		"sourceRefName": newRef,
		"targetRefName": defaultRef,
		"title":         prTitle,
		"description":   prDescription,
		"isDraft":       true,
	})
	if err != nil {
		return nil, err
	}
	resp, err = api.do(ctx, http.MethodPost, base+"/pullrequests?api-version=7.1", prBody)
	if err != nil {
		return nil, fmt.Errorf("open pull request: %w", err)
	}
	defer resp.Body.Close()

	var out struct {
		PullRequestID int64 `json:"pullRequestId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode pull request response: %w", err)
	}
	// Azure DevOps' own pull request response carries no browser URL at
	// all (only _links.web on some expansions, absent by default), so
	// the real, stable web URL is composed from the identifiers already
	// in hand rather than reported as empty.
	return &PullRequest{
		Number:  out.PullRequestID,
		HTMLURL: fmt.Sprintf("%s/%s/_git/%s/pullrequest/%d", api.baseURL, project, repositoryID, out.PullRequestID),
	}, nil
}

// zeroObjectID is Azure DevOps' own "this ref does not exist yet"
// sentinel in a ref update, confirmed against Microsoft's own published
// refs request example.
const zeroObjectID = "0000000000000000000000000000000000000000"

func repositoryDefaultRef(ctx context.Context, api *Client, base string) (string, error) {
	resp, err := api.do(ctx, http.MethodGet, base+"?api-version=7.1", nil)
	if err != nil {
		return "", fmt.Errorf("get repository: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		DefaultBranch string `json:"defaultBranch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode repository response: %w", err)
	}
	if out.DefaultBranch == "" {
		return "", fmt.Errorf("repository reports no default branch")
	}
	return out.DefaultBranch, nil
}

func refObjectID(ctx context.Context, api *Client, base, ref string) (string, error) {
	filter := strings.TrimPrefix(ref, "refs/")
	resp, err := api.do(ctx, http.MethodGet, base+"/refs?filter="+filter+"&api-version=7.1", nil)
	if err != nil {
		return "", fmt.Errorf("get ref %q: %w", ref, err)
	}
	defer resp.Body.Close()
	var out struct {
		Value []struct {
			Name     string `json:"name"`
			ObjectID string `json:"objectId"`
		} `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode refs response: %w", err)
	}
	for _, r := range out.Value {
		if r.Name == ref {
			return r.ObjectID, nil
		}
	}
	return "", fmt.Errorf("default branch ref %q not found in the repository's own refs", ref)
}
