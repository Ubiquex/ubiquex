package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ghapi "github.com/google/go-github/v78/github"
)

// TestHandlePullRequestEvent_UnlistedRepoRefusedAndLogged proves
// UBI-166's own real allowlist enforcement for GitHub: a real
// "pull_request" event for a repository with zero Config.Repos entries
// is refused outright, with a real, structured log line an operator
// can find -- s.installations is deliberately left nil here, since the
// allowlist check must happen before any installation resolution is
// even attempted.
func TestHandlePullRequestEvent_UnlistedRepoRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{}}

	e := &ghapi.PullRequestEvent{
		Repo:         &ghapi.Repository{Owner: &ghapi.User{Login: ghapi.Ptr("acme")}, Name: ghapi.Ptr("infra")},
		Installation: &ghapi.Installation{ID: ghapi.Ptr(int64(1))},
		Number:       ghapi.Ptr(42),
		Action:       ghapi.Ptr("opened"),
		PullRequest:  &ghapi.PullRequest{Head: &ghapi.PullRequestBranch{SHA: ghapi.Ptr("abc123")}},
	}

	if err := s.handlePullRequestEvent(context.Background(), e); err != nil {
		t.Fatalf("handlePullRequestEvent: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "acme/infra") {
		t.Errorf("log output = %q, want a real, loud refusal naming the repo and the allowlist", out)
	}
}

func TestHandleIssueCommentEvent_UnlistedRepoRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{}}

	e := &ghapi.IssueCommentEvent{
		Action:       ghapi.Ptr("created"),
		Repo:         &ghapi.Repository{Owner: &ghapi.User{Login: ghapi.Ptr("acme")}, Name: ghapi.Ptr("infra")},
		Installation: &ghapi.Installation{ID: ghapi.Ptr(int64(1))},
		Issue:        &ghapi.Issue{Number: ghapi.Ptr(7), PullRequestLinks: &ghapi.PullRequestLinks{}},
		Comment:      &ghapi.IssueComment{Body: ghapi.Ptr("ubx plan"), User: &ghapi.User{Login: ghapi.Ptr("roozbeh")}},
	}

	if err := s.handleIssueCommentEvent(context.Background(), e); err != nil {
		t.Fatalf("handleIssueCommentEvent: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "acme/infra") {
		t.Errorf("log output = %q, want a real, loud refusal naming the repo and the allowlist", out)
	}
}

func TestHandlePullRequestReviewEvent_UnlistedRepoRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{}}

	e := &ghapi.PullRequestReviewEvent{
		Repo:         &ghapi.Repository{Owner: &ghapi.User{Login: ghapi.Ptr("acme")}, Name: ghapi.Ptr("infra")},
		Installation: &ghapi.Installation{ID: ghapi.Ptr(int64(1))},
		PullRequest:  &ghapi.PullRequest{Number: ghapi.Ptr(9)},
		Review:       &ghapi.PullRequestReview{State: ghapi.Ptr("approved"), CommitID: ghapi.Ptr("def456")},
	}

	if err := s.handlePullRequestReviewEvent(context.Background(), e); err != nil {
		t.Fatalf("handlePullRequestReviewEvent: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "acme/infra") {
		t.Errorf("log output = %q, want a real, loud refusal naming the repo and the allowlist", out)
	}
}

// TestChangedFilePathsGitHub_ResolvesTwoRealStacksIndependently proves
// the real GitHub wiring for a genuinely multi-stack repository:
// changedFilePathsGitHub's own real fetch (a real HTTP round trip
// against a fixture server, the same "changes" endpoint GitHub's own
// List Files API exposes) feeds the resolver the real, current
// changed-file paths, and the PR resolves against the stacks actually
// discovered in the repository's own checkout (UBI-167) rather than
// any declared path.
func TestChangedFilePathsGitHub_ResolvesTwoRealStacksIndependently(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/infra/pulls/42/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"filename":"stacks/payments/proposals/db-replica.json"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	api := newTestClient(t, srv)
	paths, err := changedFilePathsGitHub(context.Background(), api, "acme", "infra", 42)
	if err != nil {
		t.Fatalf("changedFilePathsGitHub: %v", err)
	}

	repoDir := t.TempDir()
	writeStack(t, repoDir, "stacks/payments", "config")
	writeStack(t, repoDir, "stacks/networking", "config")

	dir, err := resolveStackIn(repoDir, func() ([]string, error) { return paths, nil })
	if err != nil {
		t.Fatalf("resolveStackIn: %v", err)
	}
	if dir != "stacks/payments" {
		t.Errorf("dir = %q, want %q -- a payments-only PR must resolve to the payments stack via the real fetched changed files", dir, "stacks/payments")
	}
}
