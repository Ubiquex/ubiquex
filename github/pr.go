package github

import (
	"context"
	"fmt"

	ghapi "github.com/google/go-github/v78/github"
)

// PullRequest is what OpenDraftPR returns.
type PullRequest struct {
	Number  int64
	HTMLURL string
}

// OpenDraftPR creates a branch off the repo's default branch, commits one
// new file to it (filePath/fileContent — the draft proposal, in UBI-11
// stage 3's actual use), and opens a pull request from that branch back to
// the default. This automates the exact by-hand flow stage 1 already
// defined — author resolves a proposal, commits it to a branch, puts
// "ubx-proposal: <hash>" in the PR body — so once a human merges the
// result, `ubx accept --from-merge` derives acceptance from it exactly
// the same way it would for a manually-opened PR (docs/architecture.md —
// Decision loop's GitHub App section: "reuses stage 1's binding directly").
//
// Needs "contents: write" (to create the branch and commit) and
// "pull_requests: write" — CreateIssue's read-only-on-content posture
// doesn't apply here, which is exactly why issue mode exists as the more
// conservative default (see docs/architecture.md's security-story framing).
func OpenDraftPR(ctx context.Context, api *Client, owner, repo, branch, filePath string, fileContent []byte, commitMessage, prTitle, prBody string) (*PullRequest, error) {
	repoInfo, _, err := api.api.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("get repository: %w", err)
	}
	defaultBranch := repoInfo.GetDefaultBranch()

	baseRef, _, err := api.api.Git.GetRef(ctx, owner, repo, "refs/heads/"+defaultBranch)
	if err != nil {
		return nil, fmt.Errorf("get ref for default branch %q: %w", defaultBranch, err)
	}

	if _, _, err := api.api.Git.CreateRef(ctx, owner, repo, ghapi.CreateRef{
		Ref: "refs/heads/" + branch,
		SHA: baseRef.GetObject().GetSHA(),
	}); err != nil {
		return nil, fmt.Errorf("create branch %q: %w", branch, err)
	}

	if _, _, err := api.api.Repositories.CreateFile(ctx, owner, repo, filePath, &ghapi.RepositoryContentFileOptions{
		Message: ghapi.Ptr(commitMessage),
		Content: fileContent,
		Branch:  ghapi.Ptr(branch),
	}); err != nil {
		return nil, fmt.Errorf("commit %s to %s: %w", filePath, branch, err)
	}

	pr, _, err := api.api.PullRequests.Create(ctx, owner, repo, &ghapi.NewPullRequest{
		Title: ghapi.Ptr(prTitle),
		Head:  ghapi.Ptr(branch),
		Base:  ghapi.Ptr(defaultBranch),
		Body:  ghapi.Ptr(prBody),
	})
	if err != nil {
		return nil, fmt.Errorf("open pull request: %w", err)
	}
	return &PullRequest{Number: int64(pr.GetNumber()), HTMLURL: pr.GetHTMLURL()}, nil
}
