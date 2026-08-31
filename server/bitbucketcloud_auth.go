package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hmarr/codeowners"
	bbcloud "github.com/ubiquex/ubiquex/vcs/bitbucketcloud"
)

// ErrCodeownersUnresolvable means a real CODEOWNERS rule matched a
// changed file, but ubx could not resolve one or more of that rule's
// own owner entries to a real, current Bitbucket Cloud account.
//
// Surfaced rather than swallowed (UBI-170): on every other platform an
// unresolvable owner entry is indistinguishable from a non-matching
// one, because owners are plain logins matched as strings. Bitbucket
// Cloud has no login at all, so an entry that resolves to nobody, or to
// more than one person, is a real, actionable defect in the repository's
// own CODEOWNERS file that its operator needs told about, not a quiet
// "not authorized".
var ErrCodeownersUnresolvable = errors.New("this repository's own CODEOWNERS file has entries ubx could not resolve to exactly one real Bitbucket Cloud account")

// isAuthorizedToReplanBitbucketCloud mirrors isAuthorizedToReplan/
// isAuthorizedToReplanGitLab/isAuthorizedToReplanAzureDevOps/
// isAuthorizedToReplanBitbucketServer's own identical two-tier real
// rule, against Bitbucket Cloud's own real identity shape: the
// commenter must be the pull request's own real creator, or, for a
// drift-watch-opened pull request (creator is the configured bot
// identity), the rule falls back to the commenter's own real, current
// write-or-above permission on this repository.
//
// Every comparison here is on account_id, never on a label. Bitbucket
// Cloud accounts carry no username, and both nickname and display_name
// are user-changeable, so matching either would let a rename silently
// change who is authorized.
func isAuthorizedToReplanBitbucketCloud(ctx context.Context, api *bbcloud.Client, workspace, repoSlug, botAccountID string, creator, commenter bbcloud.Account) (bool, error) {
	commenterID := commenter.Identity()
	if commenterID == "" {
		return false, nil
	}
	if creatorID := creator.Identity(); creatorID != "" && creatorID == commenterID {
		return true, nil
	}
	if botAccountID == "" || creator.Identity() != botAccountID {
		return false, nil
	}
	return api.HasWriteAccess(ctx, workspace, repoSlug, commenterID)
}

// isAuthorizedToShipBitbucketCloud mirrors every other platform's own
// identical CODEOWNERS-ownership rule, against ubx's own CODEOWNERS-file
// convention overlaid on Bitbucket Cloud (Bitbucket Cloud has no native
// CODEOWNERS support at all, the same real gap every other platform
// this project covers has).
//
// The real, structural difference (UBI-170): a CODEOWNERS file names
// people in a human-readable form, and Bitbucket Cloud has no stable
// human-readable identifier to match that against. So every matched
// rule's owner entries are resolved to real, current account_ids at
// check time, against one live workspace-members snapshot, and compared
// to the commenter's own account_id. A resolved match authorizes. An
// entry that resolves to nobody, or ambiguously to more than one
// member, never authorizes and is reported via ErrCodeownersUnresolvable
// rather than silently counted as a non-match.
//
// A real, resolved match wins even when some other entry on the same
// rule was unresolvable: one broken entry must not revoke a different
// owner's genuine authority.
func isAuthorizedToShipBitbucketCloud(ctx context.Context, api *bbcloud.Client, workspace, repoSlug string, pullRequestID int64, ref string, commenter bbcloud.Account) (bool, error) {
	commenterID := commenter.Identity()
	if commenterID == "" {
		return false, nil
	}

	body, err := fetchCodeownersFileBitbucketCloud(ctx, api, workspace, repoSlug, ref)
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

	changedFiles, err := api.ListPullRequestChangedFiles(ctx, workspace, repoSlug, pullRequestID)
	if err != nil {
		return false, err
	}
	if len(changedFiles) == 0 {
		return false, nil
	}

	members, err := api.WorkspaceMembers(ctx, workspace)
	if err != nil {
		return false, err
	}

	resolved := map[string]string{}
	var unresolvable []string
	for _, path := range changedFiles {
		rule, err := ruleset.Match(path)
		if err != nil {
			return false, fmt.Errorf("match CODEOWNERS rule for %s: %w", path, err)
		}
		if rule == nil {
			continue
		}
		for _, owner := range rule.Owners {
			// A "team" owner entry (hmarr/codeowners' own real
			// classification for any "@name" carrying a "/") never
			// matches here at all, the same real, deliberate,
			// documented gap Bitbucket Server has: resolving Bitbucket
			// Cloud group membership is a separate API this package's
			// own scope never needed, and treating a group entry as a
			// match without checking it would authorize everyone.
			if owner.Type != "username" {
				continue
			}
			entry := strings.TrimPrefix(owner.Value, "@")
			id, ok := resolved[entry]
			if !ok {
				id, err = bbcloud.ResolveAccountID(members, entry)
				if err != nil {
					if errors.Is(err, bbcloud.ErrAccountNotResolved) || errors.Is(err, bbcloud.ErrAccountAmbiguous) {
						unresolvable = append(unresolvable, fmt.Sprintf("%q (%v)", entry, err))
						resolved[entry] = ""
						continue
					}
					return false, err
				}
				resolved[entry] = id
			}
			if id != "" && id == commenterID {
				return true, nil
			}
		}
	}

	if len(unresolvable) > 0 {
		return false, fmt.Errorf("%w: %s", ErrCodeownersUnresolvable, strings.Join(unresolvable, "; "))
	}
	return false, nil
}

// fetchCodeownersFileBitbucketCloud reads repoSlug's real CODEOWNERS
// file at ref, checking the same three real, ordered locations every
// other platform's own fetcher already checks -- ubx's own consistent,
// cross-platform convention. Returns nil, nil (not an error) when none
// of the three exist.
//
// Read at the pull request's own head ref rather than the default
// branch, deliberately and unlike the Bitbucket Server counterpart: it
// is the same commit the ship would actually operate on, so ownership
// is decided by the CODEOWNERS the reviewer can actually see on the
// pull request.
func fetchCodeownersFileBitbucketCloud(ctx context.Context, api *bbcloud.Client, workspace, repoSlug, ref string) ([]byte, error) {
	for _, path := range []string{"CODEOWNERS", ".bitbucket/CODEOWNERS", "docs/CODEOWNERS"} {
		body, err := api.GetRawFile(ctx, workspace, repoSlug, ref, path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if body != nil {
			return body, nil
		}
	}
	return nil, nil
}
