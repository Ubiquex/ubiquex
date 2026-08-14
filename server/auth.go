package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/hmarr/codeowners"
	ghub "github.com/ubiquex/ubiquex/github"
)

// isAuthorizedToReplan implements UBI-28's own re-plan authorization
// rule (step 3 of the core flow) plus its own flagged, resolved open
// question: who counts as "the PR creator" when the PR itself was opened
// by this server's own bot identity, not a human.
//
// The real rule has two tiers, deliberately not one uniform check:
//
//  1. Normal case, a human-opened PR: the commenter must be the PR's own
//     real creator, exactly, compared via the platform's own API rather
//     than assumed from webhook payload trust alone (prCreatorLogin here
//     is always read from the PR object itself, never the comment's own
//     claim about who opened it).
//  2. Bot-PR case, prCreatorLogin == botLogin (a drift-watch-opened PR,
//     see drift.go -- botLogin is Config.GitHubBotLogin, the operator's
//     own configured App bot identity, never hardcoded, since a GitHub
//     App's real bot login is "<app-slug>[bot]" and the slug is whatever
//     name the operator gave their own App at creation time): there is
//     no real human "creator" to compare against, so the rule falls back
//     to a real, live, per-request authorization signal instead -- does
//     the commenter currently hold write access (or higher) on the
//     repository, checked via GitHub's own real collaborator-permission
//     API, not assumed from group membership or cached anywhere. This is
//     deliberately narrower than "any repo collaborator can re-plan any
//     PR" would be (which stays the rule ONLY for bot-opened PRs, never
//     loosened for a human-opened one) and deliberately broader than
//     "only the bot itself" would be (which would make re-plan
//     unreachable in practice on every drift PR, defeating the point of
//     exposing the comment command at all).
func isAuthorizedToReplan(ctx context.Context, api *ghub.Client, owner, repo, botLogin, prCreatorLogin, commenterLogin string) (bool, error) {
	if strings.EqualFold(commenterLogin, prCreatorLogin) {
		return true, nil
	}
	if !strings.EqualFold(prCreatorLogin, botLogin) {
		return false, nil
	}
	return hasWriteAccess(ctx, api, owner, repo, commenterLogin)
}

// hasWriteAccess checks commenterLogin's real, current, live permission
// level on owner/repo via GitHub's own repository-permissions API.
// RepositoryPermissionLevel.Permission's own real, current possible
// values are exactly "admin", "write", "read", "none" (confirmed via
// `go doc` against the actually-downloaded go-github module, not
// assumed from GitHub's separate five-tier role-naming UI, which this
// specific field doesn't use) -- "admin" and "write" count as real write
// access, "read" and "none" do not. Never cached: permissions can change
// between two comments on the same PR, and a stale "yes" here is a real
// security defect in the more dangerous direction.
func hasWriteAccess(ctx context.Context, api *ghub.Client, owner, repo, login string) (bool, error) {
	level, _, err := api.API().Repositories.GetPermissionLevel(ctx, owner, repo, login)
	if err != nil {
		return false, fmt.Errorf("get permission level for %s on %s/%s: %w", login, owner, repo, err)
	}
	switch level.GetPermission() {
	case "admin", "write":
		return true, nil
	default:
		return false, nil
	}
}

// isAuthorizedToShip implements UBI-28's own manual-ship authorization
// rule (step 4 of the core flow): commenterLogin must own, per the
// repository's real CODEOWNERS file, at least one of the PR's own real
// changed files -- real gitignore-style glob matching via
// github.com/hmarr/codeowners (a real, existing library, not
// hand-rolled), never assumed from repo-wide write access alone. A wrong
// match here is a real security defect either direction (UBI-28's own
// framing), so this reads the CODEOWNERS file and the PR's own changed
// files fresh on every call, never cached.
//
// An owner entry naming a team (GitHub's own "@org/team" CODEOWNERS
// syntax) is resolved via a real, live team-membership check
// (TeamsService.GetTeamMembershipBySlug); an owner entry naming a user
// is compared directly, case-insensitively (GitHub logins are
// case-insensitive); an email-address owner entry can never match a
// commenter's own login and is skipped -- CODEOWNERS permits email
// owners for platforms with no concept of a login, which GitHub itself
// isn't, so a real CODEOWNERS file naming one here still parses and
// matches every other real entry correctly, it just can never itself
// authorize a comment-triggered ship.
func isAuthorizedToShip(ctx context.Context, api *ghub.Client, owner, repo string, prNumber int, commenterLogin string) (bool, error) {
	body, err := fetchCodeownersFile(ctx, api, owner, repo)
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

	files, _, err := api.API().PullRequests.ListFiles(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return false, fmt.Errorf("list changed files for PR #%d: %w", prNumber, err)
	}
	if len(files) == 0 {
		return false, nil
	}

	checkedTeams := make(map[string]bool) // memoize within one call -- the same team often owns many of a PR's files
	for _, f := range files {
		rule, err := ruleset.Match(f.GetFilename())
		if err != nil {
			return false, fmt.Errorf("match CODEOWNERS rule for %s: %w", f.GetFilename(), err)
		}
		if rule == nil {
			continue // unowned file -- not this file's own authorization source
		}
		ok, err := ownsAs(ctx, api, owner, commenterLogin, rule.Owners, checkedTeams)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// ownsAs reports whether commenterLogin matches any of owners, directly
// as a named user or indirectly as a member of a named team.
func ownsAs(ctx context.Context, api *ghub.Client, org, commenterLogin string, owners []codeowners.Owner, checkedTeams map[string]bool) (bool, error) {
	for _, owner := range owners {
		switch owner.Type {
		case "username":
			if strings.EqualFold(strings.TrimPrefix(owner.Value, "@"), commenterLogin) {
				return true, nil
			}
		case "team":
			slug := teamSlug(owner.Value)
			if slug == "" {
				continue
			}
			if member, ok := checkedTeams[slug]; ok {
				if member {
					return true, nil
				}
				continue
			}
			membership, _, err := api.API().Teams.GetTeamMembershipBySlug(ctx, org, slug, commenterLogin)
			member := err == nil && membership.GetState() == "active"
			checkedTeams[slug] = member
			if member {
				return true, nil
			}
		case "email":
			// Never matches a GitHub login -- see the function's own doc comment.
		}
	}
	return false, nil
}

// teamSlug extracts a team's own slug from CODEOWNERS' "@org/team-slug"
// owner syntax. Returns "" for anything else (a malformed entry the
// codeowners package's own parser already accepted structurally, but
// which can't resolve to a real team).
func teamSlug(value string) string {
	v := strings.TrimPrefix(value, "@")
	_, slug, found := strings.Cut(v, "/")
	if !found {
		return ""
	}
	return slug
}

// fetchCodeownersFile reads owner/repo's real CODEOWNERS file from its
// default branch, checking every real location GitHub itself recognizes
// (root, .github/, docs/), in that real precedence order. Returns nil,
// nil (not an error) when none of the three exist -- a repo with no
// CODEOWNERS file at all is a real, valid state, not a failure.
func fetchCodeownersFile(ctx context.Context, api *ghub.Client, owner, repo string) ([]byte, error) {
	for _, path := range []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		content, _, resp, err := api.API().Repositories.GetContents(ctx, owner, repo, path, nil)
		if err != nil {
			if resp != nil && resp.StatusCode == 404 {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		body, err := content.GetContent()
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		return []byte(body), nil
	}
	return nil, nil
}
