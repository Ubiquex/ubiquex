package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
	ghub "github.com/ubiquex/ubiquex-cli/github"
)

// runVerifyAcceptance is `ubx why --verify-acceptance` (UBI-11 stage 1,
// docs/architecture.md — Decision loop: "acceptance claiming a merge must
// be checkable against git history + API anytime"). Runs two independent
// checks:
//
//  1. Git: p.Acceptance.MergeSHA still exists in repoDir's history, and
//     the proposal file at that exact commit still hashes to p.ID. Either
//     failing is a hard error — an acceptance record that can no longer be
//     verified against the history it claims is a serious finding, not a
//     soft one.
//  2. GitHub API (only if githubRepo is supplied): the PR's *current*
//     approving reviewers are re-fetched and compared to what's recorded.
//     A mismatch (e.g. a reviewer dismissed their approval after the fact)
//     is reported clearly but does not fail the command — the ledger
//     entry correctly recorded what was true at acceptance time; reality
//     having moved on since is exactly the kind of thing this check
//     exists to surface, not evidence the ledger itself is wrong.
//
// Non-pr_merge acceptances (or proposals with no acceptance at all) are
// reported as not applicable, not an error.
//
// Exit code is a CI contract (UBI-20, docs/exit-codes.mdx): a git-history
// failure (commit gone, file gone/rehashed at that commit) or a reviewer
// mismatch means the claimed acceptance doesn't check out -- an
// actionable finding, exit 1. A genuine tool/network failure (can't run
// git, can't reach the GitHub API, a malformed --github-repo) is exit 2.
// Nothing to check, or everything checks out, is exit 0.
func runVerifyAcceptance(cmd *cobra.Command, out io.Writer, p *core.Proposal, repoDir, githubRepo string) error {
	fmt.Fprintln(out, "--- verify-acceptance ---")

	if p.Acceptance == nil || p.Acceptance.Method != "pr_merge" {
		fmt.Fprintf(out, "acceptance method is %q -- nothing to re-verify (only pr_merge is derived from git/GitHub)\n", acceptanceMethod(p))
		return nil
	}
	a := p.Acceptance

	ctx := cmd.Context()

	exists, err := ghub.CommitExists(ctx, repoDir, a.MergeSHA)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: %w", err)}
	}
	if !exists {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("verify-acceptance: %w: %s no longer exists in %s's history", ghub.ErrCommitNotFound, a.MergeSHA, repoDir)}
	}
	fmt.Fprintf(out, "git: merge commit %s exists in %s\n", a.MergeSHA, repoDir)

	if a.ProposalFile != "" {
		content, err := ghub.FileAtCommit(ctx, repoDir, a.MergeSHA, a.ProposalFile)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: %w", err)}
		}
		var atCommit core.Proposal
		if err := json.Unmarshal(content, &atCommit); err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: parse %s at %s: %w", a.ProposalFile, a.MergeSHA, err)}
		}
		hash, err := core.Hash(&atCommit)
		if err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: %w", err)}
		}
		if hash != p.ID {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("verify-acceptance: %w: %s at %s now hashes to %s, not %s",
				core.ErrTrailerHashMismatch, a.ProposalFile, a.MergeSHA, hash, p.ID)}
		}
		fmt.Fprintf(out, "git: %s at that commit still hashes to %s\n", a.ProposalFile, p.ID)
	}

	if githubRepo == "" {
		fmt.Fprintln(out, "github API: skipped (no --github-repo given) -- reviewer re-check is inconclusive, not a pass")
		return nil
	}
	owner, repo, ok := strings.Cut(githubRepo, "/")
	if !ok || owner == "" || repo == "" {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: --github-repo must be \"owner/name\", got %q", githubRepo)}
	}

	var apiOpts []ghub.Option
	if base := os.Getenv("UBX_GITHUB_API_BASE_URL"); base != "" {
		apiOpts = append(apiOpts, ghub.WithBaseURL(base))
	}
	api := ghub.New(os.Getenv("GITHUB_TOKEN"), apiOpts...)

	current, err := api.ApprovingReviewers(ctx, owner, repo, a.PRNumber)
	if err != nil {
		fmt.Fprintf(out, "github API: could not re-fetch reviews for PR #%d: %v\n", a.PRNumber, err)
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: could not re-fetch reviews for PR #%d: %w", a.PRNumber, err)}
	}

	if sameSet(current, a.Approvers) {
		fmt.Fprintf(out, "github API: PR #%d's approvers are unchanged (%v)\n", a.PRNumber, current)
		return nil
	}
	fmt.Fprintf(out, "github API: MISMATCH -- PR #%d's approvers are now %v, recorded acceptance has %v\n",
		a.PRNumber, current, a.Approvers)
	return &ExitCodeError{Code: 1, Err: fmt.Errorf("verify-acceptance: PR #%d's approving reviewers changed since acceptance (see above)", a.PRNumber)}
}

func acceptanceMethod(p *core.Proposal) string {
	if p.Acceptance == nil {
		return "(none)"
	}
	return p.Acceptance.Method
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := append([]string{}, a...), append([]string{}, b...)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}
