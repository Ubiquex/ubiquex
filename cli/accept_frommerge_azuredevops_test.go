package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// fakeAzureDevOpsServer serves the one real Azure DevOps API endpoint
// `ubx accept --from-merge --azure-devops-project` needs (Pull Request
// Query -- reviewers/votes are embedded directly in its own response, no
// second call), fixture-driven -- no real network call. The Azure DevOps
// analog of fakeGitHubServer / fakeGitLabServer (UBI-160 Phase 2). Since
// UBX_AZURE_DEVOPS_API_BASE_URL replaces the client's entire base URL
// (organization included, see azuredevops/client.go), the real request
// path here is "/{project}/_apis/git/repositories/{repositoryId}/
// pullrequestquery" -- no organization segment.
func fakeAzureDevOpsServer(t *testing.T, prDescription string, reviewers []map[string]any) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/widgets/_apis/git/repositories/infra/pullrequestquery", func(w http.ResponseWriter, r *http.Request) {
		var queries []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&queries); err != nil {
			t.Fatal(err)
		}
		items, _ := queries[0]["items"].([]any)
		sha, _ := items[0].(string)

		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{
			"results": []map[string]any{
				{sha: []map[string]any{
					{"pullRequestId": 42, "description": prDescription, "reviewers": reviewers},
				}},
			},
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAcceptFromMerge_AzureDevOps_EndToEnd(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposals/pending.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeAzureDevOpsServer(t, "## Summary\n\nubx-proposal: "+hash, []map[string]any{
		{"displayName": "roozbeh", "vote": 10},
	})
	t.Setenv("UBX_AZURE_DEVOPS_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposals/pending.json",
		"--azure-devops-project", "acme-org/widgets/infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx accept --from-merge --azure-devops-project: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, hash) {
		t.Fatalf("expected the accepted id in output, got: %s", out)
	}
	if !strings.Contains(out, "PR 42") || !strings.Contains(out, "1 approver") {
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
	if accepted.Acceptance.Platform != "azure-devops" {
		t.Errorf("Platform = %q, want azure-devops", accepted.Acceptance.Platform)
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

// TestAcceptFromMerge_AzureDevOps_OnlyFullApprovalCounts is the real,
// Azure-DevOps-specific edge case: a vote of 5 ("approved with
// suggestions") is a real, qualified vote, not a full approval, and
// must not be rounded up to a counted approver.
func TestAcceptFromMerge_AzureDevOps_OnlyFullApprovalCounts(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeAzureDevOpsServer(t, "ubx-proposal: "+hash, []map[string]any{
		{"displayName": "roozbeh", "vote": 10},
		{"displayName": "alice", "vote": 5},
	})
	t.Setenv("UBX_AZURE_DEVOPS_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--azure-devops-project", "acme-org/widgets/infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx accept --from-merge --azure-devops-project: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "1 approver") {
		t.Fatalf("expected exactly 1 approver (vote 5 must not count), got: %s", out)
	}
}

// TestAcceptFromMerge_AzureDevOps_EmptyApproversNotBlocked mirrors the
// GitHub/GitLab unreviewed-merge cases: a merge with zero reviewers is
// recorded exactly as it happened, never rejected.
func TestAcceptFromMerge_AzureDevOps_EmptyApproversNotBlocked(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeAzureDevOpsServer(t, "ubx-proposal: "+hash, []map[string]any{})
	t.Setenv("UBX_AZURE_DEVOPS_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--azure-devops-project", "acme-org/widgets/infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("expected a zero-approval merge to still be accepted, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 approver") {
		t.Fatalf("expected 0 approvers reported, got: %s", out)
	}
}

func TestAcceptFromMerge_AzureDevOps_HashMismatchRejected(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	const wrongHash = "0000000000000000000000000000000000000000000000000000000000000000"
	apiBase := fakeAzureDevOpsServer(t, "ubx-proposal: "+wrongHash, []map[string]any{})
	t.Setenv("UBX_AZURE_DEVOPS_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--azure-devops-project", "acme-org/widgets/infra",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "does not hash to the trailer") {
		t.Fatalf("expected a trailer-hash-mismatch error, got: %v", err)
	}
}

func TestAcceptFromMerge_AzureDevOps_NoPullRequestForCommit(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	mux := http.NewServeMux()
	mux.HandleFunc("/widgets/_apis/git/repositories/infra/pullrequestquery", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{}}}) // no PR associated with this commit
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("UBX_AZURE_DEVOPS_API_BASE_URL", srv.URL)

	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--azure-devops-project", "acme-org/widgets/infra",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "no pull request found for commit") {
		t.Fatalf("expected a no-pull-request error, got: %v", err)
	}
}

// TestAcceptFromMerge_ThreeRepoFlagsRejected and
// TestAcceptFromMerge_AzureDevOpsProjectMalformed are UBI-160 Phase 2's
// own new dispatch-validation cases, extending Phase 1's two-flag checks
// to three.
func TestAcceptFromMerge_ThreeRepoFlagsRejected(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	ledgerDir := t.TempDir()

	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--gitlab-project", "acme/infra",
		"--azure-devops-project", "acme-org/widgets/infra",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "exactly one of --github-repo/--gitlab-project/--azure-devops-project") {
		t.Fatalf("expected an exactly-one-of error, got: %v", err)
	}
}

func TestAcceptFromMerge_AzureDevOpsProjectMalformed(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	ledgerDir := t.TempDir()

	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--azure-devops-project", "acme-org/widgets", // missing the repository segment
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "organization/project/repository") {
		t.Fatalf("expected a malformed --azure-devops-project error, got: %v", err)
	}
}
