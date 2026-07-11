package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core"
)

// fakeGitHubServer serves exactly the two GitHub API endpoints `ubx accept
// --from-merge` needs, fixture-driven -- no real network call.
func fakeGitHubServer(t *testing.T, prBody string, reviews []map[string]any) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/infra/commits/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{"number": 42, "body": prBody}})
	})
	mux.HandleFunc("/repos/acme/infra/pulls/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reviews)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/"
}

// gitRepoWithProposal creates a real, throwaway git repo with one commit
// adding path with content, returning the repo dir and commit SHA.
func gitRepoWithProposal(t *testing.T, path, content string) (repoDir, sha string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")

	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", path)
	run("commit", "-q", "-m", "init")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return dir, strings.TrimSpace(string(out))
}

func TestAcceptFromMerge_EndToEnd(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposals/pending.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeGitHubServer(t, "## Summary\n\nubx-proposal: "+hash, []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
	})
	t.Setenv("UBX_GITHUB_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposals/pending.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx accept --from-merge: %v\noutput: %s", err, out)
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

func TestAcceptFromMerge_UnreviewedMergeRecordedNotBlocked(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeGitHubServer(t, "ubx-proposal: "+hash, []map[string]any{}) // zero reviews
	t.Setenv("UBX_GITHUB_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("expected an unreviewed merge to still be accepted, got: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 approver") {
		t.Fatalf("expected 0 approvers reported, got: %s", out)
	}
}

func TestAcceptFromMerge_HashMismatchRejected(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)

	const wrongHash = "0000000000000000000000000000000000000000000000000000000000000000"
	apiBase := fakeGitHubServer(t, "ubx-proposal: "+wrongHash, []map[string]any{})
	t.Setenv("UBX_GITHUB_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	if err == nil {
		t.Fatal("expected a hash-mismatch rejection")
	}
	if !strings.Contains(err.Error(), "does not hash to the trailer") {
		t.Fatalf("expected a trailer-hash-mismatch error, got: %v", err)
	}
}

func TestAcceptFromMerge_CommitNotInHistory(t *testing.T) {
	repoDir, _ := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	ledgerDir := t.TempDir()

	_, err := runUbx(t, nil, "accept",
		"--from-merge", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	if err == nil {
		t.Fatal("expected an error for a merge SHA not in history")
	}
	if !strings.Contains(err.Error(), "not found in git history") {
		t.Fatalf("expected a not-in-history error, got: %v", err)
	}
}

func TestAcceptFromMerge_ProposalFileAbsentFromMerge(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	ledgerDir := t.TempDir()

	_, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "wrong/path.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	if err == nil {
		t.Fatal("expected an error for a proposal file absent from the merge")
	}
	if !strings.Contains(err.Error(), "not found at commit") {
		t.Fatalf("expected a file-not-found error, got: %v", err)
	}
}
