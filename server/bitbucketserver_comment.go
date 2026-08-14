package server

import (
	"context"
	"strings"

	bbserver "github.com/ubiquex/ubiquex/bitbucketserver"
)

// postOrEditCommentBitbucketServer mirrors postOrEditComment/
// postOrEditCommentGitLab's own real edit-in-place mechanism, against
// Bitbucket Server's own real, flat, top-level-comment concept --
// structurally simpler than Azure DevOps' own thread-only model (see
// postOrEditCommentAzureDevOps' own doc comment): a real Bitbucket
// Server pull-request comment is created directly via `POST
// .../comments` and edited directly via `PUT .../comments/{id}`, no
// wrapping "thread" object to open first.
func postOrEditCommentBitbucketServer(ctx context.Context, api *bbserver.Client, projectKey, repositorySlug string, pullRequestID int, botName, kind, body string) error {
	marker := commentMarker(kind)
	fullBody := marker + "\n" + body

	existing := findLastBotCommentBitbucketServer(ctx, api, projectKey, repositorySlug, pullRequestID, botName, marker)
	if existing != nil {
		return api.UpdateComment(ctx, projectKey, repositorySlug, pullRequestID, existing.ID, existing.Version, fullBody)
	}
	return api.CreatePullRequestComment(ctx, projectKey, repositorySlug, pullRequestID, fullBody)
}

// findLastBotCommentBitbucketServer walks pullRequestID's own real,
// top-level comments (in the real order ListPullRequestComments
// returns) and returns the last one whose author's own Name matches
// botName and whose own text starts with marker -- the same "last"
// semantics findLastBotComment/findLastBotNote/
// findLastBotThreadAzureDevOps already establish. A real fetch error is
// swallowed to nil here (matching this function's own "best-effort
// lookup, fall back to creating a new comment" contract), the same
// real reasoning findLastBotThreadAzureDevOps' own doc comment gives.
func findLastBotCommentBitbucketServer(ctx context.Context, api *bbserver.Client, projectKey, repositorySlug string, pullRequestID int, botName, marker string) *bitbucketServerComment {
	comments, err := api.ListPullRequestComments(ctx, projectKey, repositorySlug, pullRequestID)
	if err != nil {
		return nil
	}

	var last *bitbucketServerComment
	for i := range comments {
		c := comments[i]
		if !strings.EqualFold(c.Author.Name, botName) {
			continue
		}
		if !strings.HasPrefix(c.Text, marker) {
			continue
		}
		last = &bitbucketServerComment{ID: c.ID, Version: c.Version}
	}
	return last
}

// bitbucketServerComment is the minimal id/version pair
// findLastBotCommentBitbucketServer hands back -- bbserver.prComment
// itself is unexported outside package bitbucketserver, so this local,
// server-package-owned shape carries just what postOrEditComment
// BitbucketServer needs across the package boundary.
type bitbucketServerComment struct {
	ID      int
	Version int
}
