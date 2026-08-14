package server

import (
	"context"
	"fmt"
	"strings"

	bitbucketv1 "github.com/gfleury/go-bitbucket-v1"
	"github.com/hmarr/codeowners"
	bbserver "github.com/ubiquex/ubiquex/bitbucketserver"
)

// isAuthorizedToReplanBitbucketServer mirrors isAuthorizedToReplan/
// isAuthorizedToReplanGitLab/isAuthorizedToReplanAzureDevOps' own
// identical two-tier real rule, against Bitbucket Server's own real
// identity shape: the commenter must be the PR's own real creator
// (compared by Name -- Bitbucket Server's own real, stable, unique
// username, confirmed directly against the webhook payload's own
// "actor"/"author" shape and the go-bitbucket-v1 SDK's own
// UserWithLinks struct; unlike Azure DevOps, Bitbucket Server DOES have
// a real, fixed, human-readable username, so no separate opaque-
// identity-key workaround is needed here), or, for a drift-watch-opened
// PR (creator's own Name equals the configured bot name), the rule
// falls back to the commenter's own real, current REPO_WRITE-or-above
// permission on this repository (bbserver.Client.HasWriteAccess -- a
// genuine, single-call check, unlike Azure DevOps' own group-membership
// approximation, see HasWriteAccess's own doc comment).
func isAuthorizedToReplanBitbucketServer(ctx context.Context, api *bbserver.Client, projectKey, repositorySlug, botName string, creator, commenter bitbucketv1.UserWithLinks) (bool, error) {
	if commenter.Name != "" && commenter.Name == creator.Name {
		return true, nil
	}
	if !strings.EqualFold(creator.Name, botName) {
		return false, nil
	}
	return api.HasWriteAccess(ctx, projectKey, repositorySlug, commenter.Name)
}

// isAuthorizedToShipBitbucketServer mirrors isAuthorizedToShip/
// isAuthorizedToShipGitLab/isAuthorizedToShipAzureDevOps' own identical
// CODEOWNERS-ownership rule, against ubx's own CODEOWNERS-file
// convention overlaid on Bitbucket Server (Bitbucket Server has no
// native CODEOWNERS support at all, the same real gap every other
// platform this project covers has -- this is ubx's own convention,
// not something Bitbucket Server itself enforces).
func isAuthorizedToShipBitbucketServer(ctx context.Context, api *bbserver.Client, projectKey, repositorySlug string, pullRequestID int, commenter bitbucketv1.UserWithLinks) (bool, error) {
	body, err := fetchCodeownersFileBitbucketServer(ctx, api, projectKey, repositorySlug)
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

	changedFiles, err := api.ListPullRequestChangedFiles(ctx, projectKey, repositorySlug, pullRequestID)
	if err != nil {
		return false, err
	}
	if len(changedFiles) == 0 {
		return false, nil
	}

	for _, path := range changedFiles {
		rule, err := ruleset.Match(path)
		if err != nil {
			return false, fmt.Errorf("match CODEOWNERS rule for %s: %w", path, err)
		}
		if rule == nil {
			continue
		}
		if ownsAsBitbucketServer(commenter, rule.Owners) {
			return true, nil
		}
	}
	return false, nil
}

// ownsAsBitbucketServer mirrors ownsAs/ownsAsGitLab's own real
// structure: a CODEOWNERS "username" owner entry is matched against
// commenter.Name -- Bitbucket Server's own real, stable, fixed username
// (unlike Azure DevOps' own DisplayName-only fallback, see
// ownsAsAzureDevOps' own doc comment, Bitbucket Server has a real login
// string GitHub/GitLab CODEOWNERS authors would already expect). A
// "team" owner entry (hmarr/codeowners' own real classification for any
// "@name" with a "/") never matches here at all: Bitbucket Server has
// no native group-membership-by-name lookup this package's own scope
// ever needed to build for the two-tier re-plan rule (HasWriteAccess
// answers "does this user have write access," not "is this user a
// member of group X"), so a CODEOWNERS group entry against Bitbucket
// Server is a real, deliberate, documented gap -- never silently
// treated as a match, unlike a missing rule at all (which already
// returns false, not true).
func ownsAsBitbucketServer(commenter bitbucketv1.UserWithLinks, owners []codeowners.Owner) bool {
	for _, owner := range owners {
		if owner.Type != "username" {
			continue
		}
		if strings.EqualFold(strings.TrimPrefix(owner.Value, "@"), commenter.Name) {
			return true
		}
	}
	return false
}

// fetchCodeownersFileBitbucketServer reads repositorySlug's real
// CODEOWNERS file from its default branch, checking the same three
// real, ordered locations fetchCodeownersFile/fetchCodeownersFileGitLab/
// fetchCodeownersFileAzureDevOps already check -- ubx's own, consistent,
// cross-platform convention. Returns nil, nil (not an error) when none
// of the three exist.
func fetchCodeownersFileBitbucketServer(ctx context.Context, api *bbserver.Client, projectKey, repositorySlug string) ([]byte, error) {
	for _, path := range []string{"CODEOWNERS", ".bitbucket/CODEOWNERS", "docs/CODEOWNERS"} {
		body, err := api.GetRawFile(ctx, projectKey, repositorySlug, path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if body != nil {
			return body, nil
		}
	}
	return nil, nil
}
