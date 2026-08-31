package server

import (
	"context"
	"strings"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	adevops "github.com/ubiquex/ubiquex/vcs/azuredevops"
)

// postOrEditCommentAzureDevOps mirrors postOrEditComment/
// postOrEditCommentGitLab's own real edit-in-place mechanism, against
// Azure DevOps' own genuinely different comment concept: there is no
// bare "create a comment" endpoint at all -- every real comment is the
// first Comment inside a real GitPullRequestCommentThread (see
// azuredevops.Client.CreatePullRequestThread's own doc comment), so
// "edit this kind's own last comment in place" here means "edit the
// first comment of this kind's own last marked thread," and "post a
// new one" means "open a whole new thread."
func postOrEditCommentAzureDevOps(ctx context.Context, api *adevops.Client, project, repositoryID string, prID int, botDisplayName, kind, body string) error {
	marker := commentMarker(kind)
	fullBody := marker + "\n" + body

	existingThread, existingComment := findLastBotThreadAzureDevOps(ctx, api, project, repositoryID, prID, botDisplayName, marker)
	if existingThread != nil && existingComment != nil {
		return api.UpdateThreadComment(ctx, project, repositoryID, prID, *existingThread.Id, *existingComment.Id, fullBody)
	}
	return api.CreatePullRequestThread(ctx, project, repositoryID, prID, fullBody)
}

// findLastBotThreadAzureDevOps walks prID's own real threads (oldest
// first, the real order ListPullRequestThreads returns) and returns
// the last thread/first-comment pair whose author's own DisplayName
// matches botDisplayName and whose own content starts with marker --
// the same "last" semantics findLastBotComment/findLastBotNote already
// establish for GitHub/GitLab. A real fetch error is swallowed to nil,
// nil here (matching this function's own "best-effort lookup, fall
// back to creating a new thread" contract) rather than propagated,
// since a transient list failure should degrade to "post a new
// comment" (a real, visible, if slightly redundant outcome) rather
// than silently failing the whole plan/ship flow that called it.
func findLastBotThreadAzureDevOps(ctx context.Context, api *adevops.Client, project, repositoryID string, prID int, botDisplayName, marker string) (*git.GitPullRequestCommentThread, *git.Comment) {
	threads, err := api.ListPullRequestThreads(ctx, project, repositoryID, prID)
	if err != nil {
		return nil, nil
	}

	var lastThread *git.GitPullRequestCommentThread
	var lastComment *git.Comment
	for _, thread := range threads {
		if thread == nil || thread.Comments == nil || len(*thread.Comments) == 0 {
			continue
		}
		first := (*thread.Comments)[0]
		if first.IsDeleted != nil && *first.IsDeleted {
			continue
		}
		if first.Author == nil || first.Author.DisplayName == nil || !strings.EqualFold(*first.Author.DisplayName, botDisplayName) {
			continue
		}
		if first.Content == nil || !strings.HasPrefix(*first.Content, marker) {
			continue
		}
		lastThread = thread
		lastComment = &first
	}
	return lastThread, lastComment
}
