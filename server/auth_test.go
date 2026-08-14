package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	ghub "github.com/ubiquex/ubiquex/github"
)

// fakeGitHubAuthServer serves exactly the real endpoints auth.go's own
// functions call -- collaborator permission, changed-PR-files, CODEOWNERS
// contents, team membership -- fixture-driven, no real network call, but
// a real HTTP request/response round trip through net/http, the same
// real-transport-fake-fixture discipline every other platform package in
// this codebase already uses.
type fakeGitHubAuthServer struct {
	permission   string // "admin" | "write" | "read" | "none"
	changedFiles []string
	codeowners   string          // empty means "no CODEOWNERS file at all"
	teamMembers  map[string]bool // "org/slug/login" -> is an active member
}

func (f *fakeGitHubAuthServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/repos/acme/infra/collaborators/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"permission": f.permission})
	})

	mux.HandleFunc("/repos/acme/infra/pulls/42/files", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		files := make([]map[string]any, 0, len(f.changedFiles))
		for _, name := range f.changedFiles {
			files = append(files, map[string]any{"filename": name})
		}
		_ = json.NewEncoder(w).Encode(files)
	})

	mux.HandleFunc("/repos/acme/infra/contents/CODEOWNERS", func(w http.ResponseWriter, r *http.Request) {
		if f.codeowners == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":     "file",
			"encoding": "base64",
			"content":  base64.StdEncoding.EncodeToString([]byte(f.codeowners)),
		})
	})
	mux.HandleFunc("/repos/acme/infra/contents/.github/CODEOWNERS", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/repos/acme/infra/contents/docs/CODEOWNERS", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	mux.HandleFunc("/orgs/acme/teams/", func(w http.ResponseWriter, r *http.Request) {
		// Path shape: /orgs/acme/teams/{slug}/memberships/{login}
		w.Header().Set("Content-Type", "application/json")
		if f.teamMembers[r.URL.Path] {
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "active"})
			return
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(t *testing.T, srv *httptest.Server) *ghub.Client {
	t.Helper()
	return ghub.New("test-token", ghub.WithBaseURL(srv.URL+"/"))
}

func TestIsAuthorizedToReplan_HumanCreatorMatch(t *testing.T) {
	ok, err := isAuthorizedToReplan(context.Background(), nil, "acme", "infra", "ubx[bot]", "roozbeh", "roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToReplan: %v", err)
	}
	if !ok {
		t.Error("expected true -- the commenter is the PR's own real creator")
	}
}

func TestIsAuthorizedToReplan_HumanCreatorMismatchRejected(t *testing.T) {
	ok, err := isAuthorizedToReplan(context.Background(), nil, "acme", "infra", "ubx[bot]", "roozbeh", "someoneelse")
	if err != nil {
		t.Fatalf("isAuthorizedToReplan: %v", err)
	}
	if ok {
		t.Error("expected false -- someoneelse is neither the PR's own creator nor the bot-PR fallback applies")
	}
}

func TestIsAuthorizedToReplan_BotPRWithWriteAccess(t *testing.T) {
	f := &fakeGitHubAuthServer{permission: "write"}
	api := newTestClient(t, f.server(t))

	ok, err := isAuthorizedToReplan(context.Background(), api, "acme", "infra", "ubx[bot]", "ubx[bot]", "roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToReplan: %v", err)
	}
	if !ok {
		t.Error("expected true -- a bot-opened PR falls back to real write-access, and roozbeh has write")
	}
}

func TestIsAuthorizedToReplan_BotPRWithoutWriteAccessRejected(t *testing.T) {
	f := &fakeGitHubAuthServer{permission: "read"}
	api := newTestClient(t, f.server(t))

	ok, err := isAuthorizedToReplan(context.Background(), api, "acme", "infra", "ubx[bot]", "ubx[bot]", "randomreader")
	if err != nil {
		t.Fatalf("isAuthorizedToReplan: %v", err)
	}
	if ok {
		t.Error("expected false -- read-only access must not authorize a re-plan on a bot-opened PR")
	}
}

func TestIsAuthorizedToShip_DirectUsernameOwnerMatch(t *testing.T) {
	f := &fakeGitHubAuthServer{
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @roozbeh\n",
	}
	api := newTestClient(t, f.server(t))

	ok, err := isAuthorizedToShip(context.Background(), api, "acme", "infra", 42, "roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToShip: %v", err)
	}
	if !ok {
		t.Error("expected true -- roozbeh directly owns the one changed file per CODEOWNERS")
	}
}

func TestIsAuthorizedToShip_DirectUsernameOwnerMismatchRejected(t *testing.T) {
	f := &fakeGitHubAuthServer{
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @roozbeh\n",
	}
	api := newTestClient(t, f.server(t))

	ok, err := isAuthorizedToShip(context.Background(), api, "acme", "infra", 42, "alice")
	if err != nil {
		t.Fatalf("isAuthorizedToShip: %v", err)
	}
	if ok {
		t.Error("expected false -- alice owns nothing this PR changes")
	}
}

func TestIsAuthorizedToShip_TeamOwnerMatchViaMembership(t *testing.T) {
	f := &fakeGitHubAuthServer{
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @acme/payments-team\n",
		teamMembers:  map[string]bool{"/orgs/acme/teams/payments-team/memberships/alice": true},
	}
	api := newTestClient(t, f.server(t))

	ok, err := isAuthorizedToShip(context.Background(), api, "acme", "infra", 42, "alice")
	if err != nil {
		t.Fatalf("isAuthorizedToShip: %v", err)
	}
	if !ok {
		t.Error("expected true -- alice is a real, active member of the owning team")
	}
}

func TestIsAuthorizedToShip_TeamOwnerNonMemberRejected(t *testing.T) {
	f := &fakeGitHubAuthServer{
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @acme/payments-team\n",
		teamMembers:  map[string]bool{}, // bob is not a member of anything
	}
	api := newTestClient(t, f.server(t))

	ok, err := isAuthorizedToShip(context.Background(), api, "acme", "infra", 42, "bob")
	if err != nil {
		t.Fatalf("isAuthorizedToShip: %v", err)
	}
	if ok {
		t.Error("expected false -- bob is not a member of the owning team")
	}
}

// TestIsAuthorizedToShip_EmailOwnerNeverMatches covers the real,
// documented CODEOWNERS edge case: an email-address owner entry parses
// fine but can never authorize a GitHub-login-based commenter.
func TestIsAuthorizedToShip_EmailOwnerNeverMatches(t *testing.T) {
	f := &fakeGitHubAuthServer{
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ owner@example.com\n",
	}
	api := newTestClient(t, f.server(t))

	ok, err := isAuthorizedToShip(context.Background(), api, "acme", "infra", 42, "owner")
	if err != nil {
		t.Fatalf("isAuthorizedToShip: %v", err)
	}
	if ok {
		t.Error("expected false -- an email CODEOWNERS entry can never match a GitHub login")
	}
}

func TestIsAuthorizedToShip_UnownedFileRejected(t *testing.T) {
	f := &fakeGitHubAuthServer{
		changedFiles: []string{"stacks/other/main.tf"},
		codeowners:   "/stacks/payments/ @roozbeh\n",
	}
	api := newTestClient(t, f.server(t))

	ok, err := isAuthorizedToShip(context.Background(), api, "acme", "infra", 42, "roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToShip: %v", err)
	}
	if ok {
		t.Error("expected false -- the PR's own changed file matches no CODEOWNERS rule at all")
	}
}

// TestIsAuthorizedToShip_NoCodeownersFileRejectsEveryone is the real,
// deliberate "nobody is authorized, not everybody" default -- a missing
// CODEOWNERS file must never silently authorize a manual ship.
func TestIsAuthorizedToShip_NoCodeownersFileRejectsEveryone(t *testing.T) {
	f := &fakeGitHubAuthServer{changedFiles: []string{"stacks/payments/main.tf"}}
	api := newTestClient(t, f.server(t))

	ok, err := isAuthorizedToShip(context.Background(), api, "acme", "infra", 42, "roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToShip: %v", err)
	}
	if ok {
		t.Error("expected false -- no CODEOWNERS file at all authorizes nobody")
	}
}

func TestTeamSlug(t *testing.T) {
	cases := map[string]string{
		"@acme/payments-team": "payments-team",
		"acme/payments-team":  "payments-team",
		"@roozbeh":            "",
		"malformed":           "",
	}
	for input, want := range cases {
		if got := teamSlug(input); got != want {
			t.Errorf("teamSlug(%q) = %q, want %q", input, got, want)
		}
	}
}
