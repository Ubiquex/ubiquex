package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core"
)

// acceptFromMergeForVerifyTest runs a full accept --from-merge (against a
// fake GitHub server serving reviewsAtAccept) and returns the accepted id
// plus the repo dir/merge SHA it happened against, so the verify-acceptance
// tests below start from a real pr_merge acceptance rather than a
// hand-crafted ledger entry.
func acceptFromMergeForVerifyTest(t *testing.T, reviewsAtAccept []map[string]any) (ledgerDir, id, repoDir string) {
	t.Helper()
	repoDir, sha := gitRepoWithProposal(t, "proposals/pending.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeGitHubServer(t, "ubx-proposal: "+hash, reviewsAtAccept)
	t.Setenv("UBX_GITHUB_API_BASE_URL", apiBase)

	ledgerDir = t.TempDir()
	if _, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposals/pending.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ledgerDir,
	); err != nil {
		t.Fatalf("accept --from-merge: %v", err)
	}
	return ledgerDir, hash, repoDir
}

func TestWhy_VerifyAcceptance_StillValid(t *testing.T) {
	ledgerDir, id, repoDir := acceptFromMergeForVerifyTest(t, []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
	})
	// Re-verification re-fetches reviews too -- point it at the same
	// (still-live) fake server state by re-setting the env var to what
	// t.Setenv left it as; nothing else to do, it's still in effect for
	// this test.

	out, err := runUbx(t, nil, "why", id,
		"--ledger-dir", ledgerDir,
		"--verify-acceptance",
		"--repo-dir", repoDir,
		"--github-repo", "acme/infra",
	)
	if err != nil {
		t.Fatalf("ubx why --verify-acceptance: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "merge commit") || !strings.Contains(out, "exists in") {
		t.Errorf("expected the git check to be reported, got: %s", out)
	}
	if !strings.Contains(out, "still hashes to") {
		t.Errorf("expected the file-hash re-check to be reported, got: %s", out)
	}
	if !strings.Contains(out, "approvers are unchanged") {
		t.Errorf("expected an unchanged-approvers report, got: %s", out)
	}
}

// TestWhy_VerifyAcceptance_ReviewerMismatchExitsOne: a reviewer withdrawing
// approval after acceptance doesn't mean the ledger entry was ever wrong --
// it was a correct record of what was true at acceptance time -- but it IS
// an actionable finding worth a human's attention (UBI-20 exit-code
// contract), so --verify-acceptance still reports it (never silently
// drops it) and now also exits 1 rather than 0.
func TestWhy_VerifyAcceptance_ReviewerMismatchExitsOne(t *testing.T) {
	ledgerDir, id, repoDir := acceptFromMergeForVerifyTest(t, []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
	})

	// By the time we re-verify, the review has been withdrawn -- point
	// the client at a *different* fake server reflecting that.
	apiBaseNow := fakeGitHubServer(t, "irrelevant for verify -- it never re-reads the PR body", []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "CHANGES_REQUESTED", "submitted_at": "2026-07-11T11:00:00Z"},
	})
	t.Setenv("UBX_GITHUB_API_BASE_URL", apiBaseNow)

	out, err := runUbx(t, nil, "why", id,
		"--ledger-dir", ledgerDir,
		"--verify-acceptance",
		"--repo-dir", repoDir,
		"--github-repo", "acme/infra",
	)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "MISMATCH") {
		t.Fatalf("expected a MISMATCH line, got: %s", out)
	}
}

func TestWhy_VerifyAcceptance_CommitRewrittenAway(t *testing.T) {
	ledgerDir, id, _ := acceptFromMergeForVerifyTest(t, []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
	})

	// Simulate history being rewritten out from under the acceptance
	// record: point --repo-dir at a fresh repo with no such commit at all.
	freshRepoDir := t.TempDir()
	runGit(t, freshRepoDir, "init", "-q")

	_, err := runUbx(t, nil, "why", id,
		"--ledger-dir", ledgerDir,
		"--verify-acceptance",
		"--repo-dir", freshRepoDir,
		"--github-repo", "acme/infra",
	)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "no longer exists in") {
		t.Fatalf("expected a commit-not-found error, got: %v", err)
	}
}

func TestWhy_VerifyAcceptance_NotApplicableForLocalAcceptance(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "proposal.json")
	writeFile(t, proposalPath, `{
		"schema_version": 1, "stack": "payments", "parent": "", "kind": "change",
		"intent": {"summary": "x"}, "delta": {},
		"resolution": {"resolved_at": "2026-07-11T00:00:00Z"},
		"cost_delta": {}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)
	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v", err)
	}
	id := mustExtractID(t, acceptOut)

	out, err := runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir, "--verify-acceptance")
	if err != nil {
		t.Fatalf("verify-acceptance on a local acceptance must not fail: %v", err)
	}
	if !strings.Contains(out, "nothing to re-verify") {
		t.Fatalf("expected a not-applicable message, got: %s", out)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
