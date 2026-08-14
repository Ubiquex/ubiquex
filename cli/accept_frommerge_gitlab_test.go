package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// fakeGitLabServer serves exactly the two GitLab API endpoints `ubx accept
// --from-merge --gitlab-project` needs, fixture-driven -- no real network
// call. The GitLab analog of fakeGitHubServer (UBI-160 Phase 1). Project
// paths are URL-path-escaped by the real client ("acme/infra" ->
// "acme%2Finfra"), matching gitlab/derive_test.go's own fake server.
func fakeGitLabServer(t *testing.T, mrDescription string, approvedBy []map[string]any) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/acme%2Finfra/repository/commits/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{"iid": 42, "description": mrDescription}})
	})
	mux.HandleFunc("/api/v4/projects/acme%2Finfra/merge_requests/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"approved_by": approvedBy})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAcceptFromMerge_Gitlab_EndToEnd(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposals/pending.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeGitLabServer(t, "## Summary\n\nubx-proposal: "+hash, []map[string]any{
		{"user": map[string]any{"username": "roozbeh"}},
	})
	t.Setenv("UBX_GITLAB_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposals/pending.json",
		"--gitlab-project", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx accept --from-merge --gitlab-project: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, hash) {
		t.Fatalf("expected the accepted id in output, got: %s", out)
	}
	if !strings.Contains(out, "MR !42") || !strings.Contains(out, "1 approver") {
		t.Fatalf("expected MR number and approver count in output, got: %s", out)
	}

	ledger := core.Open(ledgerDir)
	accepted, err := ledger.Read(hash)
	if err != nil {
		t.Fatalf("ledger.Read: %v", err)
	}
	if accepted.Acceptance.Method != "pr_merge" {
		t.Errorf("Method = %q, want pr_merge", accepted.Acceptance.Method)
	}
	if accepted.Acceptance.Platform != "gitlab" {
		t.Errorf("Platform = %q, want gitlab", accepted.Acceptance.Platform)
	}
	if accepted.Acceptance.MergeSHA != sha {
		t.Errorf("MergeSHA = %q, want %q", accepted.Acceptance.MergeSHA, sha)
	}
	if accepted.Acceptance.PRNumber != 42 {
		t.Errorf("PRNumber (MR IID) = %d, want 42", accepted.Acceptance.PRNumber)
	}
	if accepted.Acceptance.ProposalFile != "proposals/pending.json" {
		t.Errorf("ProposalFile = %q, want proposals/pending.json", accepted.Acceptance.ProposalFile)
	}
	if len(accepted.Acceptance.Approvers) != 1 || accepted.Acceptance.Approvers[0] != "roozbeh" {
		t.Errorf("Approvers = %v, want [roozbeh]", accepted.Acceptance.Approvers)
	}
}

// TestAcceptFromMerge_Gitlab_ResetApprovalsRecordedNotBlocked covers the
// real, GitLab-specific "force-pushed branch" edge case (this ticket's
// own named scenario): GitLab's default reset_approvals_on_push clears
// every existing approval when a new commit lands on an MR's source
// branch, so a merge whose approvals were reset (or one that genuinely
// had none) reports zero approvers from the API -- recorded as it
// happened, never blocked, same principle as the GitHub unreviewed-merge
// case above.
func TestAcceptFromMerge_Gitlab_ResetApprovalsRecordedNotBlocked(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeGitLabServer(t, "ubx-proposal: "+hash, []map[string]any{}) // zero approvals
	t.Setenv("UBX_GITLAB_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--gitlab-project", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("expected a zero-approval merge to still be accepted, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 approver") {
		t.Fatalf("expected 0 approvers reported, got: %s", out)
	}
}

func TestAcceptFromMerge_Gitlab_HashMismatchRejected(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	const wrongHash = "0000000000000000000000000000000000000000000000000000000000000000"
	apiBase := fakeGitLabServer(t, "ubx-proposal: "+wrongHash, []map[string]any{})
	t.Setenv("UBX_GITLAB_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--gitlab-project", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "does not hash to the trailer") {
		t.Fatalf("expected a trailer-hash-mismatch error, got: %v", err)
	}
}

func TestAcceptFromMerge_Gitlab_NoMergeRequestForCommit(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/acme%2Finfra/repository/commits/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{}) // no MR associated with this commit
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("UBX_GITLAB_API_BASE_URL", srv.URL)

	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--gitlab-project", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "no merge request found for commit") {
		t.Fatalf("expected a no-merge-request error, got: %v", err)
	}
}

// TestAcceptFromMerge_BothRepoFlagsRejected and
// TestAcceptFromMerge_NeitherRepoFlagRejected are UBI-160 Phase 1's own
// new dispatch-validation cases: --from-merge always derives against
// exactly one specific platform.
func TestAcceptFromMerge_BothRepoFlagsRejected(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	ledgerDir := t.TempDir()

	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--gitlab-project", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "exactly one of --github-repo/--gitlab-project") {
		t.Fatalf("expected an exactly-one-of error, got: %v", err)
	}
}

func TestAcceptFromMerge_NeitherRepoFlagRejected(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	ledgerDir := t.TempDir()

	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--ledger-dir", ledgerDir,
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "exactly one of --github-repo or --gitlab-project") {
		t.Fatalf("expected an exactly-one-of error, got: %v", err)
	}
}
