package server

import (
	"context"
	"fmt"
	"strings"

	bbcloud "github.com/ubiquex/ubiquex/vcs/bitbucketcloud"
)

// postOrEditCommentBitbucketCloud is postOrEditComment's own real
// Bitbucket Cloud counterpart: find this kind's own last comment the
// bot itself posted on the pull request and edit it in place, creating
// one only if none exists yet, so exactly one current comment of each
// kind stays visible per pull request.
//
// Two real, Bitbucket-Cloud-specific differences from the Bitbucket
// Server counterpart, neither assumed from it:
//
//   - The bot is matched by account_id, never by a name. Bitbucket
//     Cloud accounts have no username at all, and a workspace access
//     token appears in API responses as an account whose display name
//     is the token's own name, which an operator can rename at any
//     time. Matching on the stable identifier is what keeps
//     edit-in-place working across a rename.
//   - A comment's text lives under content.raw, and editing needs no
//     version echo (Bitbucket Server enforces optimistic locking on
//     every comment update; Bitbucket Cloud does not).
func postOrEditCommentBitbucketCloud(ctx context.Context, api *bbcloud.Client, workspace, repoSlug string, pullRequestID int64, botAccountID, kind, body string) error {
	marker := commentMarker(kind)
	fullBody := marker + "\n" + body

	existing, err := findLastBotCommentBitbucketCloud(ctx, api, workspace, repoSlug, pullRequestID, botAccountID, marker)
	if err != nil {
		return err
	}
	if existing != nil {
		return api.UpdateComment(ctx, workspace, repoSlug, pullRequestID, existing.ID, fullBody)
	}
	return api.CreatePullRequestComment(ctx, workspace, repoSlug, pullRequestID, fullBody)
}

// findLastBotCommentBitbucketCloud returns the last comment authored by
// botAccountID whose body carries marker, or nil when there is none.
//
// botAccountID empty returns nil deliberately, never a match: an
// unidentified bot must post a new comment rather than risk editing
// somebody else's, and silently treating "no configured identity" as
// "matches everyone" would be exactly that risk.
func findLastBotCommentBitbucketCloud(ctx context.Context, api *bbcloud.Client, workspace, repoSlug string, pullRequestID int64, botAccountID, marker string) (*bbcloud.Comment, error) {
	if botAccountID == "" {
		return nil, nil
	}
	comments, err := api.ListPullRequestComments(ctx, workspace, repoSlug, pullRequestID)
	if err != nil {
		return nil, fmt.Errorf("list comments on %s/%s pull request %d: %w", workspace, repoSlug, pullRequestID, err)
	}
	var last *bbcloud.Comment
	for i := range comments {
		c := &comments[i]
		if c.User.Identity() != botAccountID {
			continue
		}
		if !strings.HasPrefix(c.Content.Raw, marker) {
			continue
		}
		last = c
	}
	return last, nil
}
