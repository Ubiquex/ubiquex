package bitbucketserver

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

	"github.com/ubiquex/ubiquex/vcs/github"
)

// testRepo creates a real, throwaway git repository with one commit
// adding path with the given content, returning the repo dir and the
// commit SHA. Real `git` commands throughout, same as github's own
// testRepo helper (github/git_test.go) and the other two platform
// packages' own (gitlab/derive_test.go, azuredevops/derive_test.go) --
// duplicated here rather than shared, since it's small, test-only, and
// package-private on every side.
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

// fakeBitbucketServerAPI serves exactly the one real endpoint
// DeriveAcceptance needs (reviewer approvals are embedded directly in
// its own response, the same real economy azure DevOps' own equivalent
// query has, unlike GitHub/GitLab's two-call shape), fixture-driven --
// no real network call, but a real HTTP request/response round trip
// through net/http, same real-transport-fake-fixture approach as the
// other three packages' own fake servers.
type fakeBitbucketServerAPI struct {
	pullRequests []map[string]any // one entry per PR associated with the queried commit
}

func (f *fakeBitbucketServerAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/1.0/projects/PAYMENTS/repos/payments-infra/commits/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{"values": f.pullRequests}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Fatal(err)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDeriveAcceptance_HappyPath(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposals/pending.json", `{"schema_version":1,"stack":"payments"}`)

	api := &fakeBitbucketServerAPI{
		pullRequests: []map[string]any{
			{
				"id":          42,
				"description": "## Summary\n\nubx-proposal: " + trailerHash,
				"reviewers": []map[string]any{
					{"user": map[string]any{"name": "roozbeh"}, "approved": true},
					{"user": map[string]any{"name": "alice"}, "approved": true},
				},
			},
		},
	}
	srv := api.server(t)
	client := New(srv.URL, "")

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "PAYMENTS", "payments-infra", sha, "proposals/pending.json")
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
// reviewers at all -- an empty, non-nil Approvers slice is the correct,
// non-error outcome, not blocked, same principle as the other three
// platforms' own empty-approvers cases.
func TestDeriveAcceptance_EmptyApproversIsFine(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)

	api := &fakeBitbucketServerAPI{
		pullRequests: []map[string]any{
			{"id": 7, "description": "ubx-proposal: " + trailerHash, "reviewers": []map[string]any{}},
		},
	}
	srv := api.server(t)
	client := New(srv.URL, "")

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "PAYMENTS", "payments-infra", sha, "proposal.json")
	if err != nil {
		t.Fatalf("DeriveAcceptance (unreviewed merge must not be blocked): %v", err)
	}
	if len(derived.Approvers) != 0 {
		t.Fatalf("Approvers = %v, want empty", derived.Approvers)
	}
}

// TestDeriveAcceptance_OnlyApprovedReviewersCount covers a mix of
// approved and not-yet-approved reviewers -- Bitbucket Server's own
// real approval model is a direct boolean per reviewer (confirmed
// against the real, swagger-generated UserWithMetadata type), simpler
// than any of the other three platforms', but still real enough to get
// wrong: a reviewer merely added to the PR without approving must not
// count.
func TestDeriveAcceptance_OnlyApprovedReviewersCount(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)

	api := &fakeBitbucketServerAPI{
		pullRequests: []map[string]any{
			{
				"id":          7,
				"description": "ubx-proposal: " + trailerHash,
				"reviewers": []map[string]any{
					{"user": map[string]any{"name": "roozbeh"}, "approved": true},
					{"user": map[string]any{"name": "alice"}, "approved": false, "status": "UNAPPROVED"},
					{"user": map[string]any{"name": "bob"}, "approved": false, "status": "NEEDS_WORK"},
				},
			},
		},
	}
	srv := api.server(t)
	client := New(srv.URL, "")

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "PAYMENTS", "payments-infra", sha, "proposal.json")
	if err != nil {
		t.Fatalf("DeriveAcceptance: %v", err)
	}
	if len(derived.Approvers) != 1 || derived.Approvers[0] != "roozbeh" {
		t.Fatalf("Approvers = %v, want exactly [roozbeh]", derived.Approvers)
	}
}

func TestDeriveAcceptance_CommitNotInHistory(t *testing.T) {
	repoDir, _ := testRepo(t, "proposal.json", `{"schema_version":1}`)
	client := New("https://bitbucket.example.invalid", "") // never reached -- git check fails first

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "PAYMENTS", "payments-infra",
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "proposal.json")
	if !errors.Is(err, github.ErrCommitNotFound) {
		t.Fatalf("got %v, want github.ErrCommitNotFound", err)
	}
}

func TestDeriveAcceptance_ProposalFileAbsentFromMerge(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	client := New("https://bitbucket.example.invalid", "") // never reached -- git check fails first

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "PAYMENTS", "payments-infra", sha, "wrong/path.json")
	if !errors.Is(err, github.ErrFileNotFoundAtCommit) {
		t.Fatalf("got %v, want github.ErrFileNotFoundAtCommit", err)
	}
}

func TestDeriveAcceptance_NoPullRequestForCommit(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	api := &fakeBitbucketServerAPI{pullRequests: []map[string]any{}}
	srv := api.server(t)
	client := New(srv.URL, "")

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "PAYMENTS", "payments-infra", sha, "proposal.json")
	if !errors.Is(err, ErrNoPullRequestForCommit) {
		t.Fatalf("got %v, want ErrNoPullRequestForCommit", err)
	}
}

func TestDeriveAcceptance_NoTrailerInPRDescription(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	api := &fakeBitbucketServerAPI{
		pullRequests: []map[string]any{
			{"id": 9, "description": "just a normal PR, nothing special", "reviewers": []map[string]any{}},
		},
	}
	srv := api.server(t)
	client := New(srv.URL, "")

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "PAYMENTS", "payments-infra", sha, "proposal.json")
	if !errors.Is(err, github.ErrNoProposalTrailer) {
		t.Fatalf("got %v, want github.ErrNoProposalTrailer", err)
	}
}
