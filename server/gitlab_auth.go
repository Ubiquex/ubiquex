package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/hmarr/codeowners"
	glab "github.com/ubiquex/ubiquex/gitlab"
	glapi "gitlab.com/gitlab-org/api/client-go/v2"
)

// isAuthorizedToReplanGitLab is auth.go's own isAuthorizedToReplan
// rule, GitLab's real counterpart -- the identical two-tier real rule:
// the commenter must be the MR's own real creator (the normal case),
// or, for a drift-watch-opened MR (authorLogin == botUsername, no real
// human creator to compare against), the rule falls back to the
// commenter's own real, live write access on the project, checked
// fresh via GitLab's own project-membership API, never cached, never
// loosened for a human-opened MR. Same reasoning as GitHub's own
// version, applied against GitLab's real, different API shape.
func isAuthorizedToReplanGitLab(ctx context.Context, api *glab.Client, project, botUsername, mrAuthorLogin, commenterLogin string) (bool, error) {
	if strings.EqualFold(commenterLogin, mrAuthorLogin) {
		return true, nil
	}
	if !strings.EqualFold(mrAuthorLogin, botUsername) {
		return false, nil
	}
	return hasWriteAccessGitLab(ctx, api, project, commenterLogin)
}

// hasWriteAccessGitLab checks commenterLogin's real, current, live
// access level on project via GitLab's own inherited-project-member
// API (real, since a real member could hold access via group
// inheritance, not just a direct project grant) -- Developer (30) and
// above count as real write access, the same real threshold GitLab's
// own documentation names as the level able to push to
// non-protected branches. Never cached: access can change between two
// comments on the same MR.
func hasWriteAccessGitLab(ctx context.Context, api *glab.Client, project, login string) (bool, error) {
	userID, ok, err := gitlabUserIDForUsername(ctx, api, login)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	member, resp, err := api.API().ProjectMembers.GetInheritedProjectMember(project, userID, glapi.WithContext(ctx))
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return false, nil // not a member at all -- a real, valid "no access" outcome, not an error
		}
		return false, fmt.Errorf("get inherited project member %s on %s: %w", login, project, err)
	}
	return member.AccessLevel >= glapi.DeveloperPermissions, nil
}

// isAuthorizedToShipGitLab is auth.go's own isAuthorizedToShip rule,
// GitLab's real counterpart: commenterLogin must own, per the
// project's real CODEOWNERS file, at least one of the MR's own real
// changed files -- the identical github.com/hmarr/codeowners library
// (CODEOWNERS syntax itself is real, gitignore-style-glob-plus-@owner,
// genuinely the same format across platforms, confirmed directly
// against GitLab's own current CODEOWNERS documentation rather than
// assumed identical to GitHub's), never assumed from project-wide
// write access alone.
func isAuthorizedToShipGitLab(ctx context.Context, api *glab.Client, project string, mrIID int64, commenterLogin string) (bool, error) {
	body, err := fetchCodeownersFileGitLab(ctx, api, project)
	if err != nil {
		return false, err
	}
	if body == nil {
		return false, nil // no CODEOWNERS file at all -- nobody is authorized, not everybody
	}
	ruleset, err := codeowners.ParseFile(strings.NewReader(string(body)))
	if err != nil {
		return false, fmt.Errorf("parse CODEOWNERS: %w", err)
	}

	diffs, _, err := api.API().MergeRequests.ListMergeRequestDiffs(project, mrIID, nil, glapi.WithContext(ctx))
	if err != nil {
		return false, fmt.Errorf("list changed files for MR !%d: %w", mrIID, err)
	}
	if len(diffs) == 0 {
		return false, nil
	}

	checkedGroups := make(map[string]bool) // memoize within one call -- the same group often owns many of an MR's files
	for _, d := range diffs {
		path := d.NewPath
		if path == "" {
			path = d.OldPath // a deleted file has no NewPath
		}
		rule, err := ruleset.Match(path)
		if err != nil {
			return false, fmt.Errorf("match CODEOWNERS rule for %s: %w", path, err)
		}
		if rule == nil {
			continue // unowned file -- not this file's own authorization source
		}
		ok, err := ownsAsGitLab(ctx, api, commenterLogin, rule.Owners, checkedGroups)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// ownsAsGitLab reports whether commenterLogin matches any of owners,
// directly as a named user or indirectly as a member of a named group.
// Real, structural difference from auth.go's own ownsAs (GitHub):
// GitLab's own group-membership API (GetInheritedGroupMember) takes
// one real group ID/path, never a separate org+slug pair the way
// GitHub's team API does, and a real GitLab group path can itself
// carry any number of nested subgroup segments -- so a CODEOWNERS
// "@acme/platform/backend-team" owner resolves as one whole group path
// here ("acme/platform/backend-team"), never split at the first slash.
func ownsAsGitLab(ctx context.Context, api *glab.Client, commenterLogin string, owners []codeowners.Owner, checkedGroups map[string]bool) (bool, error) {
	for _, owner := range owners {
		switch owner.Type {
		case "username":
			if strings.EqualFold(strings.TrimPrefix(owner.Value, "@"), commenterLogin) {
				return true, nil
			}
		case "team":
			groupPath := strings.TrimPrefix(owner.Value, "@")
			if groupPath == "" {
				continue
			}
			if member, ok := checkedGroups[groupPath]; ok {
				if member {
					return true, nil
				}
				continue
			}
			member, err := isGroupMemberGitLab(ctx, api, groupPath, commenterLogin)
			if err != nil {
				return false, err
			}
			checkedGroups[groupPath] = member
			if member {
				return true, nil
			}
		case "email":
			// Never matches a GitLab login -- same real reasoning as
			// auth.go's own ownsAs: CODEOWNERS permits email owners for
			// platforms with no login concept, which GitLab isn't.
		}
	}
	return false, nil
}

// isGroupMemberGitLab reports whether login is a real, current,
// active member of groupPath (including via subgroup inheritance).
func isGroupMemberGitLab(ctx context.Context, api *glab.Client, groupPath, login string) (bool, error) {
	userID, ok, err := gitlabUserIDForUsername(ctx, api, login)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	_, resp, err := api.API().GroupMembers.GetInheritedGroupMember(groupPath, userID, glapi.WithContext(ctx))
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return false, nil
		}
		return false, fmt.Errorf("get inherited group member %s on %s: %w", login, groupPath, err)
	}
	return true, nil
}

// gitlabUserIDForUsername resolves a real GitLab username to its own
// numeric user ID -- GitLab's own member-lookup and approval APIs
// address a user by ID, never by username directly. Confirmed directly
// against GitLab's own current Users API docs that the "username"
// filter on GET /users is available to any real, authenticated user,
// not admin-only, despite the Go client's own doc comment grouping it
// near admin-only fields.
func gitlabUserIDForUsername(ctx context.Context, api *glab.Client, username string) (int64, bool, error) {
	users, _, err := api.API().Users.ListUsers(&glapi.ListUsersOptions{Username: &username}, glapi.WithContext(ctx))
	if err != nil {
		return 0, false, fmt.Errorf("look up gitlab user %s: %w", username, err)
	}
	if len(users) == 0 {
		return 0, false, nil
	}
	return users[0].ID, true, nil
}

// fetchCodeownersFileGitLab reads project's real CODEOWNERS file from
// its default branch, checking every real location GitLab itself
// recognizes (root, .gitlab/, docs/), in that real precedence order --
// confirmed directly against GitLab's own current CODEOWNERS docs, not
// assumed identical to GitHub's own three locations (GitLab's own list
// is the same three, but confirmed rather than assumed). Returns nil,
// nil (not an error) when none of the three exist.
func fetchCodeownersFileGitLab(ctx context.Context, api *glab.Client, project string) ([]byte, error) {
	for _, path := range []string{"CODEOWNERS", ".gitlab/CODEOWNERS", "docs/CODEOWNERS"} {
		body, resp, err := api.API().RepositoryFiles.GetRawFile(project, path, &glapi.GetRawFileOptions{}, glapi.WithContext(ctx))
		if err != nil {
			if resp != nil && resp.StatusCode == 404 {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		return body, nil
	}
	return nil, nil
}
