package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	glab "github.com/ubiquex/ubiquex/vcs/gitlab"
	glapi "gitlab.com/gitlab-org/api/client-go/v2"
)

// fakeGitLabAuthServer is fakeGitHubAuthServer's own GitLab counterpart
// -- serves exactly the real endpoints gitlab_auth.go's own functions
// call: user lookup by username, inherited project/group membership,
// CODEOWNERS contents, MR changed files.
type fakeGitLabAuthServer struct {
	project      string
	mrIID        int64
	users        map[string]int64          // username -> user ID
	projectPerm  map[int64]int             // user ID -> real GitLab AccessLevelValue on f.project
	groupMembers map[string]map[int64]bool // group path -> set of member user IDs
	changedFiles []string
	codeowners   string // empty means no CODEOWNERS file at all
}

func (f *fakeGitLabAuthServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v4/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		username := r.URL.Query().Get("username")
		id, ok := f.users[username]
		if !ok {
			w.Write([]byte("[]"))
			return
		}
		w.Write([]byte(`[{"id":` + strconv.FormatInt(id, 10) + `,"username":"` + username + `"}]`))
	})

	mux.HandleFunc("/api/v4/projects/"+f.project+"/members/all/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, "/api/v4/projects/"+f.project+"/members/all/")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		level, ok := f.projectPerm[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":` + idStr + `,"access_level":` + strconv.Itoa(level) + `}`))
	})

	mux.HandleFunc("/api/v4/groups/", func(w http.ResponseWriter, r *http.Request) {
		// Path shape: /api/v4/groups/{group}/members/all/{userID}
		rest := strings.TrimPrefix(r.URL.Path, "/api/v4/groups/")
		parts := strings.Split(rest, "/members/all/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		group, idStr := parts[0], parts[1]
		id, _ := strconv.ParseInt(idStr, 10, 64)
		if f.groupMembers[group][id] {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":` + idStr + `}`))
			return
		}
		http.NotFound(w, r)
	})

	mux.HandleFunc("/api/v4/projects/"+f.project+"/merge_requests/"+strconv.FormatInt(f.mrIID, 10)+"/diffs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[`))
		for i, name := range f.changedFiles {
			if i > 0 {
				w.Write([]byte(`,`))
			}
			w.Write([]byte(`{"new_path":"` + name + `"}`))
		}
		w.Write([]byte(`]`))
	})

	mux.HandleFunc("/api/v4/projects/"+f.project+"/repository/files/CODEOWNERS/raw", func(w http.ResponseWriter, r *http.Request) {
		if f.codeowners == "" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(f.codeowners))
	})
	mux.HandleFunc("/api/v4/projects/"+f.project+"/repository/files/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // .gitlab/CODEOWNERS, docs/CODEOWNERS -- neither exists in this fixture
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestGitLabClient(t *testing.T, srv *httptest.Server) *glab.Client {
	t.Helper()
	api, err := glab.New("test-token", glab.WithBaseURL(srv.URL+"/"))
	if err != nil {
		t.Fatalf("glab.New: %v", err)
	}
	return api
}

func TestIsAuthorizedToReplanGitLab_HumanCreatorMatch(t *testing.T) {
	ok, err := isAuthorizedToReplanGitLab(context.Background(), nil, "acme-infra", "ubx-bot", "roozbeh", "roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToReplanGitLab: %v", err)
	}
	if !ok {
		t.Error("expected true -- the commenter is the MR's own real creator")
	}
}

func TestIsAuthorizedToReplanGitLab_HumanCreatorMismatchRejected(t *testing.T) {
	ok, err := isAuthorizedToReplanGitLab(context.Background(), nil, "acme-infra", "ubx-bot", "roozbeh", "someoneelse")
	if err != nil {
		t.Fatalf("isAuthorizedToReplanGitLab: %v", err)
	}
	if ok {
		t.Error("expected false -- someoneelse is neither the MR's own creator nor the bot-MR fallback applies")
	}
}

func TestIsAuthorizedToReplanGitLab_BotMRWithDeveloperAccess(t *testing.T) {
	f := &fakeGitLabAuthServer{
		project:     "acme-infra",
		users:       map[string]int64{"roozbeh": 1},
		projectPerm: map[int64]int{1: int(glapi.DeveloperPermissions)},
	}
	api := newTestGitLabClient(t, f.server(t))

	ok, err := isAuthorizedToReplanGitLab(context.Background(), api, f.project, "ubx-bot", "ubx-bot", "roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToReplanGitLab: %v", err)
	}
	if !ok {
		t.Error("expected true -- a bot-opened MR falls back to real write access, and roozbeh has Developer")
	}
}

func TestIsAuthorizedToReplanGitLab_BotMRWithReporterAccessRejected(t *testing.T) {
	f := &fakeGitLabAuthServer{
		project:     "acme-infra",
		users:       map[string]int64{"reader": 2},
		projectPerm: map[int64]int{2: int(glapi.ReporterPermissions)},
	}
	api := newTestGitLabClient(t, f.server(t))

	ok, err := isAuthorizedToReplanGitLab(context.Background(), api, f.project, "ubx-bot", "ubx-bot", "reader")
	if err != nil {
		t.Fatalf("isAuthorizedToReplanGitLab: %v", err)
	}
	if ok {
		t.Error("expected false -- Reporter (below Developer) must not authorize a re-plan on a bot-opened MR")
	}
}

func TestIsAuthorizedToReplanGitLab_BotMRUnknownUserRejected(t *testing.T) {
	f := &fakeGitLabAuthServer{project: "acme-infra"} // no users registered at all
	api := newTestGitLabClient(t, f.server(t))

	ok, err := isAuthorizedToReplanGitLab(context.Background(), api, f.project, "ubx-bot", "ubx-bot", "ghost")
	if err != nil {
		t.Fatalf("isAuthorizedToReplanGitLab: %v", err)
	}
	if ok {
		t.Error("expected false -- a commenter GitLab itself has no user record for must never be authorized")
	}
}

func TestIsAuthorizedToShipGitLab_DirectUsernameOwnerMatch(t *testing.T) {
	f := &fakeGitLabAuthServer{
		project:      "acme-infra",
		mrIID:        42,
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @roozbeh\n",
	}
	api := newTestGitLabClient(t, f.server(t))

	ok, err := isAuthorizedToShipGitLab(context.Background(), api, f.project, f.mrIID, "roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToShipGitLab: %v", err)
	}
	if !ok {
		t.Error("expected true -- roozbeh directly owns the one changed file per CODEOWNERS")
	}
}

func TestIsAuthorizedToShipGitLab_DirectUsernameOwnerMismatchRejected(t *testing.T) {
	f := &fakeGitLabAuthServer{
		project:      "acme-infra",
		mrIID:        42,
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @roozbeh\n",
	}
	api := newTestGitLabClient(t, f.server(t))

	ok, err := isAuthorizedToShipGitLab(context.Background(), api, f.project, f.mrIID, "alice")
	if err != nil {
		t.Fatalf("isAuthorizedToShipGitLab: %v", err)
	}
	if ok {
		t.Error("expected false -- alice owns nothing this MR changes")
	}
}

// TestIsAuthorizedToShipGitLab_GroupOwnerMatchViaMembership proves the
// real, deliberate divergence from GitHub's own ownsAs (auth.go): a
// CODEOWNERS group owner resolves as one whole, real GitLab group path
// ("acme/backend-team"), never split at the first slash the way
// GitHub's org+team-slug pair is. The owner entry deliberately carries
// a real "/" (github.com/hmarr/codeowners classifies a slash-free
// "@name" as a username owner, not a team owner, at the parse layer --
// a flat name would never even reach ownsAsGitLab's own "team" case).
func TestIsAuthorizedToShipGitLab_GroupOwnerMatchViaMembership(t *testing.T) {
	f := &fakeGitLabAuthServer{
		project:      "acme-infra",
		mrIID:        42,
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @acme/backend-team\n",
		users:        map[string]int64{"alice": 3},
		groupMembers: map[string]map[int64]bool{"acme/backend-team": {3: true}},
	}
	api := newTestGitLabClient(t, f.server(t))

	ok, err := isAuthorizedToShipGitLab(context.Background(), api, f.project, f.mrIID, "alice")
	if err != nil {
		t.Fatalf("isAuthorizedToShipGitLab: %v", err)
	}
	if !ok {
		t.Error("expected true -- alice is a real, active member of the owning group")
	}
}

func TestIsAuthorizedToShipGitLab_GroupOwnerNonMemberRejected(t *testing.T) {
	f := &fakeGitLabAuthServer{
		project:      "acme-infra",
		mrIID:        42,
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @acme/backend-team\n",
		users:        map[string]int64{"bob": 4},
		groupMembers: map[string]map[int64]bool{},
	}
	api := newTestGitLabClient(t, f.server(t))

	ok, err := isAuthorizedToShipGitLab(context.Background(), api, f.project, f.mrIID, "bob")
	if err != nil {
		t.Fatalf("isAuthorizedToShipGitLab: %v", err)
	}
	if ok {
		t.Error("expected false -- bob is not a member of the owning group")
	}
}

func TestIsAuthorizedToShipGitLab_NoCodeownersFileRejectsEveryone(t *testing.T) {
	f := &fakeGitLabAuthServer{
		project:      "acme-infra",
		mrIID:        42,
		changedFiles: []string{"stacks/payments/main.tf"},
	}
	api := newTestGitLabClient(t, f.server(t))

	ok, err := isAuthorizedToShipGitLab(context.Background(), api, f.project, f.mrIID, "roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToShipGitLab: %v", err)
	}
	if ok {
		t.Error("expected false -- no CODEOWNERS file at all authorizes nobody")
	}
}
