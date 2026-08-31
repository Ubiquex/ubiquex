package server

import (
	"context"
	"fmt"
	"strings"

	glab "github.com/ubiquex/ubiquex/vcs/gitlab"
	glapi "gitlab.com/gitlab-org/api/client-go/v2"
)

// postOrEditCommentGitLab is comment.go's own postOrEditComment,
// GitLab's real counterpart: find this kind's own last note the bot
// itself posted on the MR, and edit it in place; if none exists yet,
// create one. Same real marker-based, edit-last mechanism, against
// GitLab's own real Notes API (GitLab's own comment concept -- a
// "note" -- structurally the same shape as a GitHub issue comment for
// this purpose: a plain body, an author, list/create/update
// endpoints).
func postOrEditCommentGitLab(ctx context.Context, api *glab.Client, project string, mrIID int64, botUsername, kind, body string) error {
	marker := commentMarker(kind)
	fullBody := marker + "\n" + body

	existing, err := findLastBotNote(ctx, api, project, mrIID, botUsername, marker)
	if err != nil {
		return err
	}

	if existing != nil {
		_, _, err := api.API().Notes.UpdateMergeRequestNote(project, mrIID, existing.ID,
			&glapi.UpdateMergeRequestNoteOptions{Body: &fullBody}, glapi.WithContext(ctx))
		if err != nil {
			return fmt.Errorf("edit note %d on %s!%d: %w", existing.ID, project, mrIID, err)
		}
		return nil
	}

	_, _, err = api.API().Notes.CreateMergeRequestNote(project, mrIID,
		&glapi.CreateMergeRequestNoteOptions{Body: &fullBody}, glapi.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("create note on %s!%d: %w", project, mrIID, err)
	}
	return nil
}

// findLastBotNote pages through mrIID's own real notes (oldest first,
// go-gitlab's own default order) and returns the last one authored by
// botUsername whose body carries marker -- "last" meaning most
// recently posted, the same real semantics findLastBotComment (GitHub)
// already implements.
func findLastBotNote(ctx context.Context, api *glab.Client, project string, mrIID int64, botUsername, marker string) (*glapi.Note, error) {
	var last *glapi.Note
	opt := &glapi.ListMergeRequestNotesOptions{ListOptions: glapi.ListOptions{PerPage: 100}}
	for {
		notes, resp, err := api.API().Notes.ListMergeRequestNotes(project, mrIID, opt, glapi.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("list notes on %s!%d: %w", project, mrIID, err)
		}
		for _, n := range notes {
			if !strings.EqualFold(n.Author.Username, botUsername) {
				continue
			}
			if !strings.HasPrefix(n.Body, marker) {
				continue
			}
			last = n
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return last, nil
}
