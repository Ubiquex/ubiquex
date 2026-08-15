package gitlab

import (
	"context"
	"fmt"

	glapi "gitlab.com/gitlab-org/api/client-go/v2"
)

// Issue is what CreateIssue returns, the GitLab counterpart of
// github.Issue: enough for a caller to report back to the operator.
//
// Number is GitLab's own IID (the per-project number a human sees and
// types, "#42"), never the instance-wide `id` -- the same identifier
// this package's own DeriveAcceptance already records for merge
// requests, kept consistent so a receipt and a ledger entry refer to
// numbers of the same kind.
type Issue struct {
	Number  int64
	HTMLURL string
}

// MergeRequest is what OpenDraftMR returns, the GitLab counterpart of
// github.PullRequest. Same IID reasoning as Issue above.
type MergeRequest struct {
	Number  int64
	HTMLURL string
}

// CreateIssue opens a new project issue (UBI-165).
//
// GitLab genuinely has a first-class issue tracker on every project,
// which makes this the closest of the four non-GitHub platforms to
// GitHub's own shape -- confirmed against go-gitlab's own
// IssuesService.CreateIssue, which posts to `projects/:id/issues`.
// GitLab calls the body "description" where GitHub calls it "body";
// the receipt content itself is identical.
func CreateIssue(ctx context.Context, api *Client, project, title, body string) (*Issue, error) {
	issue, _, err := api.API().Issues.CreateIssue(project, &glapi.CreateIssueOptions{
		Title:       glapi.Ptr(title),
		Description: glapi.Ptr(body),
	}, glapi.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("create issue: %w", err)
	}
	return &Issue{Number: issue.IID, HTMLURL: issue.WebURL}, nil
}

// OpenDraftMR is github.OpenDraftPR's GitLab counterpart: branch off the
// project's own default branch, commit one new file to it, and open a
// merge request back to the default.
//
// Four real calls, the same four GitHub needs, but composed from
// GitLab's own API rather than assumed symmetric: GitLab has no
// separate "get ref" step (CreateBranch takes the ref name directly,
// not a resolved SHA), and its file-creation endpoint takes the content
// as a string with an explicit encoding rather than as bytes.
//
// A GitLab merge request has no "draft" boolean at all. Draft status is
// a real title convention instead ("Draft:" prefix), which is what this
// applies -- the closest true equivalent of GitHub's own draft flag, and
// deliberately not silently dropped.
func OpenDraftMR(ctx context.Context, api *Client, project, branch, filePath string, fileContent []byte, commitMessage, mrTitle, mrDescription string) (*MergeRequest, error) {
	proj, _, err := api.API().Projects.GetProject(project, nil, glapi.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	defaultBranch := proj.DefaultBranch
	if defaultBranch == "" {
		return nil, fmt.Errorf("project %s reports no default branch", project)
	}

	if _, _, err := api.API().Branches.CreateBranch(project, &glapi.CreateBranchOptions{
		Branch: glapi.Ptr(branch),
		Ref:    glapi.Ptr(defaultBranch),
	}, glapi.WithContext(ctx)); err != nil {
		return nil, fmt.Errorf("create branch %q: %w", branch, err)
	}

	if _, _, err := api.API().RepositoryFiles.CreateFile(project, filePath, &glapi.CreateFileOptions{
		Branch:        glapi.Ptr(branch),
		Content:       glapi.Ptr(string(fileContent)),
		CommitMessage: glapi.Ptr(commitMessage),
		Encoding:      glapi.Ptr("text"),
	}, glapi.WithContext(ctx)); err != nil {
		return nil, fmt.Errorf("commit %s to %s: %w", filePath, branch, err)
	}

	mr, _, err := api.API().MergeRequests.CreateMergeRequest(project, &glapi.CreateMergeRequestOptions{
		Title:        glapi.Ptr(draftTitle(mrTitle)),
		Description:  glapi.Ptr(mrDescription),
		SourceBranch: glapi.Ptr(branch),
		TargetBranch: glapi.Ptr(defaultBranch),
	}, glapi.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("open merge request: %w", err)
	}
	return &MergeRequest{Number: mr.IID, HTMLURL: mr.WebURL}, nil
}

// draftTitle applies GitLab's own real draft convention, and only when
// it is not already applied -- "Draft: Draft: ..." would be a real,
// visible defect in the merge request's own title.
func draftTitle(title string) string {
	if len(title) >= 6 && (title[:6] == "Draft:" || title[:6] == "draft:") {
		return title
	}
	return "Draft: " + title
}
