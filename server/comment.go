package server

import (
	"context"
	"fmt"
	"strings"

	ghapi "github.com/google/go-github/v78/github"
	ghub "github.com/ubiquex/ubiquex/vcs/github"
)

// commentMarker is a hidden HTML-comment prefix distinguishing one kind
// of bot comment from another on the same PR (currently only "plan",
// but the same mechanism carries forward without change if a later
// phase adds another kind). Real, needed distinction: postOrEditComment
// must find *this kind's own* last comment specifically, not just "the
// bot's last comment on the PR" -- a PR could carry more than one real
// kind of ubx-posted comment at once.
func commentMarker(kind string) string {
	return fmt.Sprintf("<!-- ubx:%s -->", kind)
}

// postOrEditComment is the real counterpart to `gh pr comment --edit-last
// --create-if-none` (the exact mechanism UBI-161's own CI/CD guides
// already use, and this ticket's own instruction to reuse rather than
// reimplement): find this kind's own last comment the bot itself posted
// on the PR, and edit it in place; if none exists yet, create one. This
// keeps exactly one, current comment of each kind visible per PR,
// instead of a new one piling up on every re-plan.
//
// login is the bot's own authenticated identity (read once from the
// installation client, see server.go), used to filter ListComments down
// to comments this bot itself posted -- a human's or another bot's own
// comment, even one that happens to start with the same marker text, is
// never a match.
func postOrEditComment(ctx context.Context, api *ghub.Client, owner, repo string, prNumber int, botLogin, kind, body string) error {
	marker := commentMarker(kind)
	fullBody := marker + "\n" + body

	existing, err := findLastBotComment(ctx, api, owner, repo, prNumber, botLogin, marker)
	if err != nil {
		return err
	}

	if existing != nil {
		_, _, err := api.API().Issues.EditComment(ctx, owner, repo, existing.GetID(), &ghapi.IssueComment{Body: &fullBody})
		if err != nil {
			return fmt.Errorf("edit comment %d on %s/%s#%d: %w", existing.GetID(), owner, repo, prNumber, err)
		}
		return nil
	}

	_, _, err = api.API().Issues.CreateComment(ctx, owner, repo, prNumber, &ghapi.IssueComment{Body: &fullBody})
	if err != nil {
		return fmt.Errorf("create comment on %s/%s#%d: %w", owner, repo, prNumber, err)
	}
	return nil
}

// findLastBotComment pages through prNumber's own comments (oldest
// first, go-github's own default order) and returns the last one
// authored by botLogin whose body carries marker -- "last" meaning most
// recently posted, matching gh's own --edit-last semantics exactly.
func findLastBotComment(ctx context.Context, api *ghub.Client, owner, repo string, prNumber int, botLogin, marker string) (*ghapi.IssueComment, error) {
	var last *ghapi.IssueComment
	opts := &ghapi.IssueListCommentsOptions{ListOptions: ghapi.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := api.API().Issues.ListComments(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, fmt.Errorf("list comments on %s/%s#%d: %w", owner, repo, prNumber, err)
		}
		for _, c := range comments {
			if !strings.EqualFold(c.GetUser().GetLogin(), botLogin) {
				continue
			}
			if !strings.HasPrefix(c.GetBody(), marker) {
				continue
			}
			last = c
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return last, nil
}
