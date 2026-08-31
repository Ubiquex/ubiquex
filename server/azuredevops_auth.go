package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/hmarr/codeowners"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	adevops "github.com/ubiquex/ubiquex/vcs/azuredevops"
)

// isAuthorizedToReplanAzureDevOps mirrors isAuthorizedToReplan/
// isAuthorizedToReplanGitLab's own identical two-tier real rule,
// against Azure DevOps' own real identity shape: the commenter must be
// the PR's own real creator (compared by Descriptor -- Azure DevOps'
// own real, stable identity key, confirmed directly against the SDK's
// own IdentityRef doc comment: "will uniquely identify the same graph
// subject across both Accounts and Organizations" -- DisplayName is
// explicitly documented non-unique, never used for this comparison),
// or, for a drift-watch-opened PR (creator's own DisplayName equals
// the configured bot display name -- Azure DevOps has no fixed bot-
// naming convention to compare against instead), the rule falls back
// to the commenter's own real, live membership in the project's
// well-known "Contributors" security group.
func isAuthorizedToReplanAzureDevOps(ctx context.Context, api *adevops.Client, project, botDisplayName string, creator, commenter *git.IdentityRefWithVote) (bool, error) {
	creatorDisplayName, creatorDescriptor := identityRefWithVoteFields(creator)
	_, commenterDescriptor := identityRefWithVoteFields(commenter)

	if commenterDescriptor != "" && commenterDescriptor == creatorDescriptor {
		return true, nil
	}
	if !strings.EqualFold(creatorDisplayName, botDisplayName) {
		return false, nil
	}
	return hasContributorAccessAzureDevOps(ctx, api, project, commenterDescriptor)
}

// identityRefWithVoteFields safely extracts DisplayName/Descriptor from
// a possibly-nil *git.IdentityRefWithVote with possibly-nil string
// pointer fields -- both real webhook payload and fresh-GetPullRequest
// paths hand this same real, optional-pointer-heavy SDK type around.
func identityRefWithVoteFields(id *git.IdentityRefWithVote) (displayName, descriptor string) {
	if id == nil {
		return "", ""
	}
	if id.DisplayName != nil {
		displayName = *id.DisplayName
	}
	if id.Descriptor != nil {
		descriptor = *id.Descriptor
	}
	return displayName, descriptor
}

// hasContributorAccessAzureDevOps reports whether subjectDescriptor is
// a real, current member of project's own well-known, default
// "Contributors" security group -- real display-name format
// "[ProjectName]\Contributors", confirmed directly against Microsoft's
// own current docs/API example payloads for how a real Azure DevOps
// group identity's own DisplayName is formatted
// ("[Mobile]\Mobile Team"). A deliberate, documented, practical
// simplification: Azure DevOps' own real permission model is ACL/
// security-namespace-based, confirmed genuinely deep/complex (no
// single-call "does user X have write access" endpoint the way
// GitHub's collaborator-permission or GitLab's project-member
// access-level endpoints are) -- Contributors is the real, well-known,
// default group every real Azure DevOps project ships with real,
// current push/contribute rights, the same practical-threshold
// reasoning GitLab's own Developer-or-above check applies where a
// fully granular ACL evaluation isn't practical for a single check.
func hasContributorAccessAzureDevOps(ctx context.Context, api *adevops.Client, project, subjectDescriptor string) (bool, error) {
	if subjectDescriptor == "" {
		return false, nil
	}
	groupDescriptor, ok, err := api.QuerySubjectDescriptor(ctx, "["+project+"]\\Contributors", "Group")
	if err != nil {
		return false, fmt.Errorf("resolve %s Contributors group: %w", project, err)
	}
	if !ok {
		return false, nil
	}
	return api.CheckMembership(ctx, subjectDescriptor, groupDescriptor)
}

// isAuthorizedToShipAzureDevOps mirrors isAuthorizedToShip/
// isAuthorizedToShipGitLab's own identical CODEOWNERS-ownership rule,
// against ubx's own CODEOWNERS-file convention overlaid on Azure
// DevOps (see azuredevops.Client.GetRawFile's own doc comment: Azure
// DevOps has no native CODEOWNERS support at all, confirmed directly
// against Microsoft's own current docs -- this is ubx's own real
// convention, not something Azure DevOps itself enforces).
// commenterDisplayName/commenterDescriptor come directly from the
// real webhook payload's own Comment.Author -- no extra resolve call
// needed for the commenter's own identity, only for a matched
// CODEOWNERS group owner's identity (ownsAsAzureDevOps).
func isAuthorizedToShipAzureDevOps(ctx context.Context, api *adevops.Client, project, repositoryID string, prID int, commenterDisplayName, commenterDescriptor string) (bool, error) {
	body, err := fetchCodeownersFileAzureDevOps(ctx, api, project, repositoryID)
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

	changedFiles, err := api.ListPullRequestChangedFiles(ctx, project, repositoryID, prID)
	if err != nil {
		return false, err
	}
	if len(changedFiles) == 0 {
		return false, nil
	}

	checkedGroups := make(map[string]bool)
	for _, path := range changedFiles {
		rule, err := ruleset.Match(path)
		if err != nil {
			return false, fmt.Errorf("match CODEOWNERS rule for %s: %w", path, err)
		}
		if rule == nil {
			continue
		}
		ok, err := ownsAsAzureDevOps(ctx, api, commenterDisplayName, commenterDescriptor, rule.Owners, checkedGroups)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// ownsAsAzureDevOps mirrors ownsAsGitLab's own real structure: a
// CODEOWNERS "username" owner entry is matched as plain text against
// commenterDisplayName -- Azure DevOps has no fixed "username" concept
// the way GitHub/GitLab do (confirmed directly against Microsoft's own
// current identity model docs: a real user is addressed by display
// name, email/unique name, or descriptor, never a single canonical
// "login" string), so DisplayName is the one real, human-writable
// identifier a CODEOWNERS file's own author could plausibly use, the
// same real reasoning this project's own hmarr/codeowners reuse
// already accepts as "the identifier this platform's own commenter is
// naturally compared against," not a a new, invented convention. A
// "team" owner entry (hmarr/codeowners' own real classification for
// any "@name" with a "/") resolves as one real Azure DevOps group
// display name via Graph, exactly the same "whole string, never split"
// principle ownsAsGitLab's own doc comment establishes for GitLab's
// own nested group paths -- Azure DevOps group display names can
// themselves carry a real "\" separator ("[Project]\Group Name"), a
// different real delimiter, never conflated with GitHub/GitLab's own
// "/"-separated conventions.
func ownsAsAzureDevOps(ctx context.Context, api *adevops.Client, commenterDisplayName, commenterDescriptor string, owners []codeowners.Owner, checkedGroups map[string]bool) (bool, error) {
	for _, owner := range owners {
		switch owner.Type {
		case "username":
			if strings.EqualFold(strings.TrimPrefix(owner.Value, "@"), commenterDisplayName) {
				return true, nil
			}
		case "team":
			groupName := strings.TrimPrefix(owner.Value, "@")
			if groupName == "" || commenterDescriptor == "" {
				continue
			}
			if member, ok := checkedGroups[groupName]; ok {
				if member {
					return true, nil
				}
				continue
			}
			groupDescriptor, ok, err := api.QuerySubjectDescriptor(ctx, groupName, "Group")
			if err != nil {
				return false, err
			}
			member := false
			if ok {
				member, err = api.CheckMembership(ctx, commenterDescriptor, groupDescriptor)
				if err != nil {
					return false, err
				}
			}
			checkedGroups[groupName] = member
			if member {
				return true, nil
			}
		case "email":
			// Never matches an Azure DevOps display name -- same real
			// reasoning ownsAs/ownsAsGitLab's own doc comments give.
		}
	}
	return false, nil
}

// fetchCodeownersFileAzureDevOps reads repositoryID's real CODEOWNERS
// file from its default branch, checking the same three real, ordered
// locations fetchCodeownersFile/fetchCodeownersFileGitLab already
// check for GitHub/GitLab -- ubx's own, consistent, cross-platform
// convention (see isAuthorizedToShipAzureDevOps's own doc comment:
// Azure DevOps has no native CODEOWNERS support to defer to instead).
// Returns nil, nil (not an error) when none of the three exist.
func fetchCodeownersFileAzureDevOps(ctx context.Context, api *adevops.Client, project, repositoryID string) ([]byte, error) {
	for _, path := range []string{"CODEOWNERS", ".azuredevops/CODEOWNERS", "docs/CODEOWNERS"} {
		body, err := api.GetRawFile(ctx, project, repositoryID, path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if body != nil {
			return body, nil
		}
	}
	return nil, nil
}
