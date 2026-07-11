package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeGitHubAPI serves exactly the two endpoints DeriveAcceptance needs,
// fixture-driven — no real network call, but real HTTP request/response
// handling through go-github's own client, same as the real API would see.
type fakeGitHubAPI struct {
	pulls   []map[string]any // response body for ListPullRequestsWithCommit
	reviews []map[string]any // response body for ListReviews
}

func (f *fakeGitHubAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/infra/commits/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(f.pulls); err != nil {
			t.Fatal(err)
		}
	})
	mux.HandleFunc("/repos/acme/infra/pulls/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(f.reviews); err != nil {
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

	api := &fakeGitHubAPI{
		pulls: []map[string]any{
			{"number": 42, "body": "## Summary\n\nubx-proposal: " + trailerHash},
		},
		reviews: []map[string]any{
			{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
			{"user": map[string]any{"login": "alice"}, "state": "APPROVED", "submitted_at": "2026-07-11T09:00:00Z"},
		},
	}
	srv := api.server(t)
	client := New("", WithBaseURL(srv.URL+"/"))

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "acme", "infra", sha, "proposals/pending.json")
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
	if derived.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", derived.PRNumber)
	}
	if len(derived.Approvers) != 2 {
		t.Fatalf("Approvers = %v, want 2 entries", derived.Approvers)
	}
}

func TestDeriveAcceptance_LatestReviewWinsOverEarlierApproval(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)

	api := &fakeGitHubAPI{
		pulls: []map[string]any{
			{"number": 7, "body": "ubx-proposal: " + trailerHash},
		},
		reviews: []map[string]any{
			// alice approved first, then later requested changes -- the
			// withdrawn approval must not count.
			{"user": map[string]any{"login": "alice"}, "state": "APPROVED", "submitted_at": "2026-07-11T09:00:00Z"},
			{"user": map[string]any{"login": "alice"}, "state": "CHANGES_REQUESTED", "submitted_at": "2026-07-11T10:00:00Z"},
			{"user": map[string]any{"login": "bob"}, "state": "APPROVED", "submitted_at": "2026-07-11T09:30:00Z"},
		},
	}
	srv := api.server(t)
	client := New("", WithBaseURL(srv.URL+"/"))

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "acme", "infra", sha, "proposal.json")
	if err != nil {
		t.Fatalf("DeriveAcceptance: %v", err)
	}
	if len(derived.Approvers) != 1 || derived.Approvers[0] != "bob" {
		t.Fatalf("Approvers = %v, want exactly [bob] (alice's approval was withdrawn)", derived.Approvers)
	}
}

func TestDeriveAcceptance_EmptyApproversIsFine(t *testing.T) {
	const trailerHash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)

	api := &fakeGitHubAPI{
		pulls:   []map[string]any{{"number": 7, "body": "ubx-proposal: " + trailerHash}},
		reviews: []map[string]any{}, // merged with zero reviews at all
	}
	srv := api.server(t)
	client := New("", WithBaseURL(srv.URL+"/"))

	derived, err := DeriveAcceptance(context.Background(), client, repoDir, "acme", "infra", sha, "proposal.json")
	if err != nil {
		t.Fatalf("DeriveAcceptance (unreviewed merge must not be blocked): %v", err)
	}
	if len(derived.Approvers) != 0 {
		t.Fatalf("Approvers = %v, want empty", derived.Approvers)
	}
}

func TestDeriveAcceptance_CommitNotInHistory(t *testing.T) {
	repoDir, _ := testRepo(t, "proposal.json", `{"schema_version":1}`)
	client := New("") // never reached -- git check fails first

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "acme", "infra",
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "proposal.json")
	if !errors.Is(err, ErrCommitNotFound) {
		t.Fatalf("got %v, want ErrCommitNotFound", err)
	}
}

func TestDeriveAcceptance_ProposalFileAbsentFromMerge(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	client := New("") // never reached -- git check fails first

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "acme", "infra", sha, "wrong/path.json")
	if !errors.Is(err, ErrFileNotFoundAtCommit) {
		t.Fatalf("got %v, want ErrFileNotFoundAtCommit", err)
	}
}

func TestDeriveAcceptance_NoPullRequestForCommit(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	api := &fakeGitHubAPI{pulls: []map[string]any{}}
	srv := api.server(t)
	client := New("", WithBaseURL(srv.URL+"/"))

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "acme", "infra", sha, "proposal.json")
	if !errors.Is(err, ErrNoPullRequestForCommit) {
		t.Fatalf("got %v, want ErrNoPullRequestForCommit", err)
	}
}

func TestDeriveAcceptance_NoTrailerInPRBody(t *testing.T) {
	repoDir, sha := testRepo(t, "proposal.json", `{"schema_version":1}`)
	api := &fakeGitHubAPI{
		pulls: []map[string]any{{"number": 9, "body": "just a normal PR, nothing special"}},
	}
	srv := api.server(t)
	client := New("", WithBaseURL(srv.URL+"/"))

	_, err := DeriveAcceptance(context.Background(), client, repoDir, "acme", "infra", sha, "proposal.json")
	if !errors.Is(err, ErrNoProposalTrailer) {
		t.Fatalf("got %v, want ErrNoProposalTrailer", err)
	}
}
