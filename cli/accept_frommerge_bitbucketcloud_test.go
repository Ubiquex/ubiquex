package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// fakeBitbucketCloudServer serves exactly the two Bitbucket Cloud API
// endpoints `ubx accept --from-merge --bitbucket-cloud-repo` needs.
//
// Two endpoints, not one, deliberately: Bitbucket Cloud's own
// commit-to-pull-request lookup returns a MINIMAL pull request
// representation carrying no description and no participants
// (confirmed live against the real API), so the full representation is
// a separate fetch. A fixture that answered everything in one call
// would let a regression to a single-call implementation pass.
func fakeBitbucketCloudServer(t *testing.T, description string, participants []map[string]any) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/commit/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"repo_indexed": true,
			"values":       []map[string]any{{"type": "pullrequest", "id": 900, "title": "db replica"}},
		})
	})
	mux.HandleFunc("/repositories/acme/infra/pullrequests/900", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           900,
			"description":  description,
			"participants": participants,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestAcceptFromMerge_BitbucketCloud_EndToEnd(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposals/pending.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeBitbucketCloudServer(t, "## Summary\n\nubx-proposal: "+hash, []map[string]any{
		{"user": map[string]any{"account_id": "acct-approver", "nickname": "roozbeh"}, "approved": true, "state": "approved"},
		{"user": map[string]any{"account_id": "acct-other", "nickname": "someone"}, "approved": false},
	})
	t.Setenv("UBX_BITBUCKET_CLOUD_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposals/pending.json",
		"--bitbucket-cloud-repo", "acme/infra",
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx accept --from-merge --bitbucket-cloud-repo: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "accepted") {
		t.Errorf("output = %q, want a real acceptance", out)
	}
}

// TestAcceptFromMerge_BitbucketCloud_ApproverRecordedAsAccountID is
// UBI-170's own identity property end to end: Bitbucket Cloud accounts
// have no username, and nickname is user-changeable, so the ledger must
// record the stable account_id rather than the label the fixture also
// supplies.
func TestAcceptFromMerge_BitbucketCloud_ApproverRecordedAsAccountID(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposals/pending.json", draftProposalJSON)

	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	hash, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}

	apiBase := fakeBitbucketCloudServer(t, "ubx-proposal: "+hash, []map[string]any{
		{"user": map[string]any{"account_id": "acct-approver", "nickname": "roozbeh"}, "approved": true, "state": "approved"},
	})
	t.Setenv("UBX_BITBUCKET_CLOUD_API_BASE_URL", apiBase)

	ledgerDir := t.TempDir()
	if _, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposals/pending.json",
		"--bitbucket-cloud-repo", "acme/infra",
		"--ledger-dir", ledgerDir,
	); err != nil {
		t.Fatalf("accept: %v", err)
	}

	ledger := core.Open(ledgerDir)
	accepted, err := ledger.Read(hash)
	if err != nil {
		t.Fatalf("ledger.Read: %v", err)
	}
	if accepted.Acceptance.Method != "pr_merge" {
		t.Errorf("Method = %q, want pr_merge", accepted.Acceptance.Method)
	}
	if accepted.Acceptance.PRNumber != 900 {
		t.Errorf("PRNumber = %d, want 900", accepted.Acceptance.PRNumber)
	}
	if len(accepted.Acceptance.Approvers) != 1 || accepted.Acceptance.Approvers[0] != "acct-approver" {
		t.Fatalf("Approvers = %v, want [acct-approver]: Bitbucket Cloud has no username, so the stable account_id is what a ledger can record", accepted.Acceptance.Approvers)
	}
	if strings.Contains(fmt.Sprint(accepted.Acceptance.Approvers), "roozbeh") {
		t.Error("must not record a mutable nickname as the approver")
	}
}

func TestAcceptFromMerge_BitbucketCloud_MalformedRepoRejected(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposals/pending.json", draftProposalJSON)

	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposals/pending.json",
		"--bitbucket-cloud-repo", "not-a-workspace-repo-pair",
		"--ledger-dir", t.TempDir(),
	)
	if err == nil {
		t.Fatalf("expected a real rejection, got output: %s", out)
	}
	if msg := err.Error() + out; !strings.Contains(msg, "workspace/repo-slug") {
		t.Errorf("error = %q, want it to name the real expected shape", msg)
	}
}

// TestAcceptFromMerge_BitbucketCloud_MutuallyExclusiveWithOtherPlatforms
// proves the new flag joins the existing exactly-one-platform rule
// rather than sitting outside it.
func TestAcceptFromMerge_BitbucketCloud_MutuallyExclusiveWithOtherPlatforms(t *testing.T) {
	repoDir, sha := gitRepoWithProposal(t, "proposals/pending.json", draftProposalJSON)

	out, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposals/pending.json",
		"--bitbucket-cloud-repo", "acme/infra",
		"--github-repo", "acme/infra",
		"--ledger-dir", t.TempDir(),
	)
	if err == nil {
		t.Fatalf("expected a real rejection for two platforms at once, got: %s", out)
	}
	if msg := err.Error() + out; !strings.Contains(msg, "not more than one") {
		t.Errorf("error = %q, want the exactly-one-platform refusal", msg)
	}
}
