package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	bitbucketv1 "github.com/gfleury/go-bitbucket-v1"
	bbserver "github.com/ubiquex/ubiquex/vcs/bitbucketserver"
)

// fakeBitbucketServerAuthServer serves exactly the real endpoints
// bitbucketserver_auth.go's own functions call: repository permission
// listing (write-access check), raw file content (CODEOWNERS), and PR
// changes (changed-file listing) -- fixture-driven, real HTTP round
// trips through net/http, the same real pattern
// fakeAzureDevOpsAuthServer's own doc comment documents.
type fakeBitbucketServerAuthServer struct {
	projectKey     string
	repositorySlug string
	prID           int
	// writeUsers is the set of usernames HasWriteAccess should report
	// as holding REPO_WRITE or above.
	writeUsers   map[string]bool
	codeowners   string
	changedFiles []string
}

func (f *fakeBitbucketServerAuthServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	base := "/rest/api/1.0/projects/" + f.projectKey + "/repos/" + f.repositorySlug

	mux.HandleFunc(base+"/permissions/users", func(w http.ResponseWriter, r *http.Request) {
		filter := r.URL.Query().Get("filter")
		w.Header().Set("Content-Type", "application/json")
		if !f.writeUsers[filter] {
			w.Write([]byte(`{"values":[]}`))
			return
		}
		body, _ := json.Marshal(map[string]any{
			"values": []map[string]any{
				{"user": map[string]any{"name": filter}, "permission": "REPO_WRITE"},
			},
		})
		w.Write(body)
	})

	mux.HandleFunc(base+"/raw/", func(w http.ResponseWriter, r *http.Request) {
		if f.codeowners == "" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(f.codeowners))
	})

	mux.HandleFunc(base+"/pull-requests/"+strconv.Itoa(f.prID)+"/changes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		values := make([]map[string]any, 0, len(f.changedFiles))
		for _, path := range f.changedFiles {
			values = append(values, map[string]any{"path": map[string]any{"toString": path}})
		}
		body, _ := json.Marshal(map[string]any{"values": values})
		w.Write(body)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestBitbucketServerClient(t *testing.T, srv *httptest.Server) *bbserver.Client {
	t.Helper()
	return bbserver.New(srv.URL, "test-token")
}

func bbUser(name string) bitbucketv1.UserWithLinks {
	return bitbucketv1.UserWithLinks{Name: name, DisplayName: name}
}

func TestIsAuthorizedToReplanBitbucketServer_HumanCreatorMatch(t *testing.T) {
	creator := bbUser("roozbeh")
	commenter := bbUser("roozbeh")
	ok, err := isAuthorizedToReplanBitbucketServer(context.Background(), nil, "INFRA", "infra", "ubx-bot", creator, commenter)
	if err != nil {
		t.Fatalf("isAuthorizedToReplanBitbucketServer: %v", err)
	}
	if !ok {
		t.Error("expected true -- the commenter is the PR's own real creator (matched by Name)")
	}
}

func TestIsAuthorizedToReplanBitbucketServer_HumanCreatorMismatchRejected(t *testing.T) {
	creator := bbUser("roozbeh")
	commenter := bbUser("someone-else")
	ok, err := isAuthorizedToReplanBitbucketServer(context.Background(), nil, "INFRA", "infra", "ubx-bot", creator, commenter)
	if err != nil {
		t.Fatalf("isAuthorizedToReplanBitbucketServer: %v", err)
	}
	if ok {
		t.Error("expected false -- someone-else is neither the PR's own creator nor does the bot-PR fallback apply")
	}
}

func TestIsAuthorizedToReplanBitbucketServer_BotPRWithWriteAccess(t *testing.T) {
	f := &fakeBitbucketServerAuthServer{
		projectKey:     "INFRA",
		repositorySlug: "infra",
		writeUsers:     map[string]bool{"roozbeh": true},
	}
	api := newTestBitbucketServerClient(t, f.server(t))

	creator := bbUser("ubx-bot")
	commenter := bbUser("roozbeh")
	ok, err := isAuthorizedToReplanBitbucketServer(context.Background(), api, f.projectKey, f.repositorySlug, "ubx-bot", creator, commenter)
	if err != nil {
		t.Fatalf("isAuthorizedToReplanBitbucketServer: %v", err)
	}
	if !ok {
		t.Error("expected true -- a bot-opened PR falls back to real REPO_WRITE-or-above access, and roozbeh has it")
	}
}

func TestIsAuthorizedToReplanBitbucketServer_BotPRWithoutWriteAccessRejected(t *testing.T) {
	f := &fakeBitbucketServerAuthServer{
		projectKey:     "INFRA",
		repositorySlug: "infra",
		writeUsers:     map[string]bool{},
	}
	api := newTestBitbucketServerClient(t, f.server(t))

	creator := bbUser("ubx-bot")
	commenter := bbUser("random-reader")
	ok, err := isAuthorizedToReplanBitbucketServer(context.Background(), api, f.projectKey, f.repositorySlug, "ubx-bot", creator, commenter)
	if err != nil {
		t.Fatalf("isAuthorizedToReplanBitbucketServer: %v", err)
	}
	if ok {
		t.Error("expected false -- a non-writer must not authorize a re-plan on a bot-opened PR")
	}
}

func TestIsAuthorizedToShipBitbucketServer_DirectUsernameOwnerMatch(t *testing.T) {
	f := &fakeBitbucketServerAuthServer{
		projectKey:     "INFRA",
		repositorySlug: "infra",
		prID:           42,
		changedFiles:   []string{"stacks/payments/main.tf"},
		codeowners:     "/stacks/payments/ @roozbeh\n",
	}
	api := newTestBitbucketServerClient(t, f.server(t))

	ok, err := isAuthorizedToShipBitbucketServer(context.Background(), api, f.projectKey, f.repositorySlug, f.prID, bbUser("roozbeh"))
	if err != nil {
		t.Fatalf("isAuthorizedToShipBitbucketServer: %v", err)
	}
	if !ok {
		t.Error("expected true -- roozbeh directly owns the one changed file per CODEOWNERS")
	}
}

func TestIsAuthorizedToShipBitbucketServer_DirectUsernameOwnerMismatchRejected(t *testing.T) {
	f := &fakeBitbucketServerAuthServer{
		projectKey:     "INFRA",
		repositorySlug: "infra",
		prID:           42,
		changedFiles:   []string{"stacks/payments/main.tf"},
		codeowners:     "/stacks/payments/ @roozbeh\n",
	}
	api := newTestBitbucketServerClient(t, f.server(t))

	ok, err := isAuthorizedToShipBitbucketServer(context.Background(), api, f.projectKey, f.repositorySlug, f.prID, bbUser("alice"))
	if err != nil {
		t.Fatalf("isAuthorizedToShipBitbucketServer: %v", err)
	}
	if ok {
		t.Error("expected false -- alice owns nothing this PR changes")
	}
}

// TestIsAuthorizedToShipBitbucketServer_GroupOwnerNeverMatches proves the
// real, deliberate scope gap documented on ownsAsBitbucketServer: a
// CODEOWNERS "team" entry never authorizes anyone against Bitbucket
// Server, since this package has no group-membership-by-name lookup
// for it (unlike GitHub/GitLab/Azure DevOps, each of which do).
func TestIsAuthorizedToShipBitbucketServer_GroupOwnerNeverMatches(t *testing.T) {
	f := &fakeBitbucketServerAuthServer{
		projectKey:     "INFRA",
		repositorySlug: "infra",
		prID:           42,
		changedFiles:   []string{"stacks/payments/main.tf"},
		codeowners:     "/stacks/payments/ @backend-team\n",
	}
	api := newTestBitbucketServerClient(t, f.server(t))

	ok, err := isAuthorizedToShipBitbucketServer(context.Background(), api, f.projectKey, f.repositorySlug, f.prID, bbUser("alice"))
	if err != nil {
		t.Fatalf("isAuthorizedToShipBitbucketServer: %v", err)
	}
	if ok {
		t.Error("expected false -- a CODEOWNERS team entry is a real, documented, deliberate non-match against Bitbucket Server")
	}
}

func TestIsAuthorizedToShipBitbucketServer_NoCodeownersFileRejectsEveryone(t *testing.T) {
	f := &fakeBitbucketServerAuthServer{
		projectKey:     "INFRA",
		repositorySlug: "infra",
		prID:           42,
		changedFiles:   []string{"stacks/payments/main.tf"},
	}
	api := newTestBitbucketServerClient(t, f.server(t))

	ok, err := isAuthorizedToShipBitbucketServer(context.Background(), api, f.projectKey, f.repositorySlug, f.prID, bbUser("roozbeh"))
	if err != nil {
		t.Fatalf("isAuthorizedToShipBitbucketServer: %v", err)
	}
	if ok {
		t.Error("expected false -- no CODEOWNERS file at all authorizes nobody")
	}
}
