package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// fakeBitbucketServerServer serves the one real Bitbucket Server API
// endpoint `ubx accept --from-merge --bitbucket-server-project` needs
// (reviewer approvals are embedded directly in its own response, the
// same real economy Azure DevOps' own equivalent has), fixture-driven
// -- no real network call. The Bitbucket Server analog of
// fakeGitHubServer / fakeGitLabServer / fakeAzureDevOpsServer (UBI-160
// Phase 3). Unlike the other three platforms, --bitbucket-server-url is
// itself the client's entire base URL, with no default host to replace
// -- the real request path here is "/rest/api/1.0/projects/{projectKey}/
// repos/{repositorySlug}/commits/{commitId}/pull-requests".
func fakeBitbucketServerServer(t *testing.T, prDescription string, reviewers []map[string]any) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/1.0/projects/PAYMENTS/repos/payments-infra/commits/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{
			"values": []map[string]any{
				{"id": 42, "description": prDescription, "reviewers": reviewers},
			},
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAcceptFromMerge_BitbucketServer_EndToEnd(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposals/pending.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeBitbucketServerServer(t, "## Summary\n\nubx-proposal: "+hash, []map[string]any{
		{"user": map[string]any{"name": "roozbeh"}, "approved": true},
	})

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposals/pending.json",
		"--bitbucket-server-url", apiBase,
		"--bitbucket-server-project", "PAYMENTS/payments-infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx accept --from-merge --bitbucket-server-project: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, hash) {
		t.Fatalf("expected the accepted id in output, got: %s", out)
	}
	if !strings.Contains(out, "PR #42") || !strings.Contains(out, "1 approver") {
		t.Fatalf("expected PR number and approver count in output, got: %s", out)
	}

	ledger := core.Open(ledgerDir)
	accepted, err := ledger.Read(hash)
	if err != nil {
		t.Fatalf("ledger.Read: %v", err)
	}
	if accepted.Acceptance.Method != "pr_merge" {
		t.Errorf("Method = %q, want pr_merge", accepted.Acceptance.Method)
	}
	if accepted.Acceptance.Platform != "bitbucket-server" {
		t.Errorf("Platform = %q, want bitbucket-server", accepted.Acceptance.Platform)
	}
	if accepted.Acceptance.MergeSHA != sha {
		t.Errorf("MergeSHA = %q, want %q", accepted.Acceptance.MergeSHA, sha)
	}
	if accepted.Acceptance.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", accepted.Acceptance.PRNumber)
	}
	if accepted.Acceptance.ProposalFile != "proposals/pending.json" {
		t.Errorf("ProposalFile = %q, want proposals/pending.json", accepted.Acceptance.ProposalFile)
	}
	if len(accepted.Acceptance.Approvers) != 1 || accepted.Acceptance.Approvers[0] != "roozbeh" {
		t.Errorf("Approvers = %v, want [roozbeh]", accepted.Acceptance.Approvers)
	}
}

// TestAcceptFromMerge_BitbucketServer_OnlyApprovedReviewersCount is the
// real, Bitbucket-Server-specific edge case: a reviewer merely added to
// the PR (approved: false) must not count, only a real, direct approved
// boolean does.
func TestAcceptFromMerge_BitbucketServer_OnlyApprovedReviewersCount(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeBitbucketServerServer(t, "ubx-proposal: "+hash, []map[string]any{
		{"user": map[string]any{"name": "roozbeh"}, "approved": true},
		{"user": map[string]any{"name": "alice"}, "approved": false},
	})

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--bitbucket-server-url", apiBase,
		"--bitbucket-server-project", "PAYMENTS/payments-infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx accept --from-merge --bitbucket-server-project: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "1 approver") {
		t.Fatalf("expected exactly 1 approver (unapproved reviewer must not count), got: %s", out)
	}
}

// TestAcceptFromMerge_BitbucketServer_EmptyApproversNotBlocked mirrors
// the other three platforms' own unreviewed-merge cases.
func TestAcceptFromMerge_BitbucketServer_EmptyApproversNotBlocked(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeBitbucketServerServer(t, "ubx-proposal: "+hash, []map[string]any{})

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--bitbucket-server-url", apiBase,
		"--bitbucket-server-project", "PAYMENTS/payments-infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("expected a zero-approval merge to still be accepted, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 approver") {
		t.Fatalf("expected 0 approvers reported, got: %s", out)
	}
}

func TestAcceptFromMerge_BitbucketServer_HashMismatchRejected(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	const wrongHash = "0000000000000000000000000000000000000000000000000000000000000000"
	apiBase := fakeBitbucketServerServer(t, "ubx-proposal: "+wrongHash, []map[string]any{})

	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--bitbucket-server-url", apiBase,
		"--bitbucket-server-project", "PAYMENTS/payments-infra",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "does not hash to the trailer") {
		t.Fatalf("expected a trailer-hash-mismatch error, got: %v", err)
	}
}

func TestAcceptFromMerge_BitbucketServer_NoPullRequestForCommit(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/1.0/projects/PAYMENTS/repos/payments-infra/commits/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"values": []map[string]any{}}) // no PR associated with this commit
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--bitbucket-server-url", srv.URL,
		"--bitbucket-server-project", "PAYMENTS/payments-infra",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "no pull request found for commit") {
		t.Fatalf("expected a no-pull-request error, got: %v", err)
	}
}

func TestAcceptFromMerge_BitbucketServer_MissingURL(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	ledgerDir := t.TempDir()

	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--bitbucket-server-project", "PAYMENTS/payments-infra",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "requires --bitbucket-server-url") {
		t.Fatalf("expected a missing-url error, got: %v", err)
	}
}

func TestAcceptFromMerge_BitbucketServerProjectMalformed(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	ledgerDir := t.TempDir()

	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--bitbucket-server-url", "https://bitbucket.example.com",
		"--bitbucket-server-project", "PAYMENTS", // missing the repository slug segment
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "PROJECTKEY/repository-slug") {
		t.Fatalf("expected a malformed --bitbucket-server-project error, got: %v", err)
	}
}

// TestAcceptFromMerge_FourRepoFlagsRejected extends UBI-160 Phase 2's
// own three-flag dispatch-validation case to four.
func TestAcceptFromMerge_FourRepoFlagsRejected(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	ledgerDir := t.TempDir()

	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--gitlab-project", "acme/infra",
		"--azure-devops-project", "acme-org/widgets/infra",
		"--bitbucket-server-project", "PAYMENTS/payments-infra",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "exactly one of --github-repo/--gitlab-project/--azure-devops-project/--bitbucket-server-project") {
		t.Fatalf("expected an exactly-one-of error, got: %v", err)
	}
}
