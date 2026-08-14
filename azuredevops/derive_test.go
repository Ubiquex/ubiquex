package azuredevops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/github"
)

// testRepo creates a real, throwaway git repository with one commit
// adding path with the given content, returning the repo dir and the
// commit SHA. Real `git` commands throughout, same as github's own
// testRepo helper (github/git_test.go) and gitlab's own (gitlab/
// derive_test.go) -- duplicated here rather than shared, since it's
// small, test-only, and package-private on all three sides.
func testRepo(t *testing.T, path, content string) (repoDir, sha string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
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

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v\n%s", err, out)
	}
	return dir, strings.TrimSpace(string(out))
}

// fakeAzureDevOpsAPI serves exactly the one real endpoint DeriveAcceptance
// needs (the Pull Request Query API -- reviewers/votes are embedded
// directly in its own response, unlike GitHub/GitLab which need a second
// call), fixture-driven -- no real network call, but a real HTTP
// request/response round trip through net/http, same real-transport-
// fake-fixture approach as github's fakeGitHubAPI / gitlab's
// fakeGitLabAPI.
type fakeAzureDevOpsAPI struct {
	pullRequests []map[string]any // one entry per PR associated with the queried commit
}

func (f *fakeAzureDevOpsAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	// WithBaseURL replaces the client's entire base URL (organization
	// included), so a real request path here is "/{project}/_apis/git/
	// repositories/{repositoryId}/pullrequestquery" relative to the test
	// server -- no organization segment, matching client.go's own
	// c.baseURL+"/"+project+"/_apis/..." construction exactly.
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
				{sha: f.pullRequests},
			},
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Fatal(err)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newTestClient constructs a Client whose organization ("acme-org") is
// deliberately different from the project ("widgets") used in every
// DeriveAcceptance call below -- proves the two are never accidentally
// conflated, since WithBaseURL drops the organization from the request
// path entirely (see fakeAzureDevOpsAPI.server's own comment).
func newTestClient(baseURL string) *Client {
	return New("acme-org", "", WithBaseURL(baseURL))
}

func TestDeriveAcceptance_HappyPath(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposals/pending.json", `{"schema_version":1,"stack":"payments"}`)

	api := &fakeAzureDevOpsAPI{
		pullRequests: []map[string]any{
			{
				"pullRequestId": 42,
				"description":   "## Summary\n\nubx-proposal: " + trailerHash,
				"reviewers": []map[string]any{
					{"displayName": "roozbeh", "vote": 10},
					{"displayName": "alice", "vote": 10},
				},
			},
		},
	}
	srv := api.server(t)
	client := newTestClient(srv.URL)

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "widgets", "infra", sha, "proposals/pending.json")
	if err != nil {
		t.Fatalf("DeriveAcceptance: %v", err)
	}
	if string(derived.ProposalFileContent) != `{"schema_version":1,"stack":"payments"}` {
		t.Errorf("ProposalFileContent = %q", derived.ProposalFileContent)
	}
	if derived.ClaimedHash != trailerHash {
		t.Errorf("ClaimedHash = %q, want %q", derived.ClaimedHash, trailerHash)
	}
	if derived.MergeSHA != sha {
		t.Errorf("MergeSHA = %q, want %q", derived.MergeSHA, sha)
	}
	if derived.PullRequestID != 42 {
		t.Errorf("PullRequestID = %d, want 42", derived.PullRequestID)
	}
	if len(derived.Approvers) != 2 {
		t.Fatalf("Approvers = %v, want 2 entries", derived.Approvers)
	}
}

// TestDeriveAcceptance_EmptyApproversIsFine covers a merge with zero
// reviewers at all (a direct, unattended completion, or a project with
// no required-reviewer policy) -- an empty, non-nil Approvers slice is
// the correct, non-error outcome, not blocked, same principle as
// GitHub's/GitLab's own empty-approvers cases.
func TestDeriveAcceptance_EmptyApproversIsFine(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)

	api := &fakeAzureDevOpsAPI{
		pullRequests: []map[string]any{
			{"pullRequestId": 7, "description": "ubx-proposal: " + trailerHash, "reviewers": []map[string]any{}},
		},
	}
	srv := api.server(t)
	client := newTestClient(srv.URL)

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "widgets", "infra", sha, "proposal.json")
	if err != nil {
		t.Fatalf("DeriveAcceptance (unreviewed merge must not be blocked): %v", err)
	}
	if len(derived.Approvers) != 0 {
		t.Fatalf("Approvers = %v, want empty", derived.Approvers)
	}
}

// TestDeriveAcceptance_OnlyFullApprovalCounts is Azure DevOps' own real,
// distinct edge case, genuinely different from GitHub's/GitLab's binary
// approve models: a five-value vote scale (10 approved, 5 approved with
// suggestions, 0 no vote, -5 waiting for author, -10 rejected),
// confirmed directly against Microsoft's own REST API docs. Only vote
// 10 counts as an approver -- 5 ("approved with suggestions") is a real,
// qualified vote, not a full approval, and must not be rounded up to
// one; -5/-10/0 must not count either.
func TestDeriveAcceptance_OnlyFullApprovalCounts(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)

	api := &fakeAzureDevOpsAPI{
		pullRequests: []map[string]any{
			{
				"pullRequestId": 7,
				"description":   "ubx-proposal: " + trailerHash,
				"reviewers": []map[string]any{
					{"displayName": "roozbeh", "vote": 10}, // approved -- counts
					{"displayName": "alice", "vote": 5},    // approved with suggestions -- does not count
					{"displayName": "bob", "vote": 0},      // no vote -- does not count
					{"displayName": "carol", "vote": -5},   // waiting for author -- does not count
					{"displayName": "dave", "vote": -10},   // rejected -- does not count
				},
			},
		},
	}
	srv := api.server(t)
	client := newTestClient(srv.URL)

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "widgets", "infra", sha, "proposal.json")
	if err != nil {
		t.Fatalf("DeriveAcceptance: %v", err)
	}
	if len(derived.Approvers) != 1 || derived.Approvers[0] != "roozbeh" {
		t.Fatalf("Approvers = %v, want exactly [roozbeh] (only a full-approval vote counts)", derived.Approvers)
	}
}

func TestDeriveAcceptance_CommitNotInHistory(t *testing.T) {
	repoDir, _ := testRepo(t, "proposal.json", `{"schema_version":1}`)
	client := newTestClient("https://azuredevops.example.invalid") // never reached -- git check fails first

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "widgets", "infra",
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "proposal.json")
	if !errors.Is(err, github.ErrCommitNotFound) {
		t.Fatalf("got %v, want github.ErrCommitNotFound", err)
	}
}

func TestDeriveAcceptance_ProposalFileAbsentFromMerge(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	client := newTestClient("https://azuredevops.example.invalid") // never reached -- git check fails first

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "widgets", "infra", sha, "wrong/path.json")
	if !errors.Is(err, github.ErrFileNotFoundAtCommit) {
		t.Fatalf("got %v, want github.ErrFileNotFoundAtCommit", err)
	}
}

func TestDeriveAcceptance_NoPullRequestForCommit(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	api := &fakeAzureDevOpsAPI{pullRequests: []map[string]any{}}
	srv := api.server(t)
	client := newTestClient(srv.URL)

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "widgets", "infra", sha, "proposal.json")
	if !errors.Is(err, ErrNoPullRequestForCommit) {
		t.Fatalf("got %v, want ErrNoPullRequestForCommit", err)
	}
}

func TestDeriveAcceptance_NoTrailerInPRDescription(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	api := &fakeAzureDevOpsAPI{
		pullRequests: []map[string]any{
			{"pullRequestId": 9, "description": "just a normal PR, nothing special", "reviewers": []map[string]any{}},
		},
	}
	srv := api.server(t)
	client := newTestClient(srv.URL)

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "widgets", "infra", sha, "proposal.json")
	if !errors.Is(err, github.ErrNoProposalTrailer) {
		t.Fatalf("got %v, want github.ErrNoProposalTrailer", err)
	}
}
