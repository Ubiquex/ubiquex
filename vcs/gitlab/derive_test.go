package gitlab

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

// testRepo creates a real, throwaway git repository with one commit adding
// path with the given content, returning the repo dir and the commit SHA.
// Real `git` commands throughout, same as github's own testRepo helper
// (github/git_test.go) -- duplicated here rather than shared, since it's
// small, test-only, and package-private on both sides.
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

// fakeGitLabAPI serves exactly the two endpoints DeriveAcceptance needs,
// fixture-driven -- no real network call, but real HTTP request/response
// handling through go-gitlab's own client, same real-transport-fake-
// fixture approach as github's own fakeGitHubAPI.
type fakeGitLabAPI struct {
	mergeRequests []map[string]any // response body for ListMergeRequestsByCommit
	approvedBy    []map[string]any // response body for GetConfiguration's approved_by
}

func (f *fakeGitLabAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v4/projects/acme%2Finfra/repository/commits/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(f.mergeRequests); err != nil {
			t.Fatal(err)
		}
	})
	mux.HandleFunc("/api/v4/projects/acme%2Finfra/merge_requests/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{"approved_by": f.approvedBy}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Fatal(err)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New("", WithBaseURL(baseURL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestDeriveAcceptance_HappyPath(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposals/pending.json", `{"schema_version":1,"stack":"payments"}`)

	api := &fakeGitLabAPI{
		mergeRequests: []map[string]any{
			{"iid": 42, "description": "## Summary\n\nubx-proposal: " + trailerHash},
		},
		approvedBy: []map[string]any{
			{"user": map[string]any{"username": "roozbeh"}},
			{"user": map[string]any{"username": "alice"}},
		},
	}
	srv := api.server(t)
	client := newTestClient(t, srv.URL)

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "acme/infra", sha, "proposals/pending.json")
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
	if derived.MRIID != 42 {
		t.Errorf("MRIID = %d, want 42", derived.MRIID)
	}
	if len(derived.Approvers) != 2 {
		t.Fatalf("Approvers = %v, want 2 entries", derived.Approvers)
	}
}

// TestDeriveAcceptance_EmptyApproversIsFine covers two real, distinct
// scenarios that both produce an empty approved_by from GitLab's own API:
// a merge with genuinely zero reviews, and -- the "force-pushed branch"
// edge case -- a merge where every prior approval was cleared by GitLab's
// own reset_approvals_on_push default (true unless a project turns it
// off) when a later commit landed on the MR's source branch before merge.
// DeriveAcceptance can't distinguish the two from the API response alone
// (nor does it need to -- GitLab's approved_by, queried after the MR is
// merged, already reflects exactly who approved the version that was
// actually merged, whichever real history produced that state); either
// way, an empty, non-nil Approvers slice is the correct, non-error
// outcome, not blocked -- enforcing "this needed N approvals" is
// GitLab's own job (merge request approval rules), never ubx's.
func TestDeriveAcceptance_EmptyApproversIsFine(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)

	api := &fakeGitLabAPI{
		mergeRequests: []map[string]any{{"iid": 7, "description": "ubx-proposal: " + trailerHash}},
		approvedBy:    []map[string]any{}, // merged with zero (or reset-to-zero) approvals
	}
	srv := api.server(t)
	client := newTestClient(t, srv.URL)

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "acme/infra", sha, "proposal.json")
	if err != nil {
		t.Fatalf("DeriveAcceptance (unapproved/reset-approvals merge must not be blocked): %v", err)
	}
	if len(derived.Approvers) != 0 {
		t.Fatalf("Approvers = %v, want empty", derived.Approvers)
	}
}

// TestDeriveAcceptance_ApprovedByIsTakenAsIs is the GitLab-specific
// counterpart to github's own TestDeriveAcceptance_LatestReviewWinsOverEarlierApproval:
// GitLab has no CHANGES_REQUESTED-supersedes-APPROVED concept to fold
// client-side at all (confirmed against GitLab's own current API docs,
// not assumed absent) -- approved_by is already the authoritative,
// deduplicated current state, so a user appearing once in approved_by
// simply is an approver, with no "most recent review wins" logic needed
// or present in ApprovingReviewers.
func TestDeriveAcceptance_ApprovedByIsTakenAsIs(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)

	api := &fakeGitLabAPI{
		mergeRequests: []map[string]any{{"iid": 7, "description": "ubx-proposal: " + trailerHash}},
		approvedBy: []map[string]any{
			{"user": map[string]any{"username": "bob"}},
		},
	}
	srv := api.server(t)
	client := newTestClient(t, srv.URL)

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "acme/infra", sha, "proposal.json")
	if err != nil {
		t.Fatalf("DeriveAcceptance: %v", err)
	}
	if len(derived.Approvers) != 1 || derived.Approvers[0] != "bob" {
		t.Fatalf("Approvers = %v, want exactly [bob]", derived.Approvers)
	}
}

func TestDeriveAcceptance_CommitNotInHistory(t *testing.T) {
	repoDir, _ := testRepo(t, "proposal.json", `{"schema_version":1}`)
	client := newTestClient(t, "https://gitlab.example.invalid") // never reached -- git check fails first

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "acme/infra",
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "proposal.json")
	if !errors.Is(err, github.ErrCommitNotFound) {
		t.Fatalf("got %v, want github.ErrCommitNotFound", err)
	}
}

func TestDeriveAcceptance_ProposalFileAbsentFromMerge(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	client := newTestClient(t, "https://gitlab.example.invalid") // never reached -- git check fails first

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "acme/infra", sha, "wrong/path.json")
	if !errors.Is(err, github.ErrFileNotFoundAtCommit) {
		t.Fatalf("got %v, want github.ErrFileNotFoundAtCommit", err)
	}
}

func TestDeriveAcceptance_NoMergeRequestForCommit(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	api := &fakeGitLabAPI{mergeRequests: []map[string]any{}}
	srv := api.server(t)
	client := newTestClient(t, srv.URL)

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "acme/infra", sha, "proposal.json")
	if !errors.Is(err, ErrNoMergeRequestForCommit) {
		t.Fatalf("got %v, want ErrNoMergeRequestForCommit", err)
	}
}

func TestDeriveAcceptance_NoTrailerInMRDescription(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	api := &fakeGitLabAPI{
		mergeRequests: []map[string]any{{"iid": 9, "description": "just a normal MR, nothing special"}},
	}
	srv := api.server(t)
	client := newTestClient(t, srv.URL)

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "acme/infra", sha, "proposal.json")
	if !errors.Is(err, github.ErrNoProposalTrailer) {
		t.Fatalf("got %v, want github.ErrNoProposalTrailer", err)
	}
}
