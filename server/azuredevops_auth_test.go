package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	adevops "github.com/ubiquex/ubiquex/vcs/azuredevops"
)

// fakeAzureDevOpsAuthServer serves exactly the real endpoints
// azuredevops_auth.go's own functions call: Graph subject search,
// Graph membership check, repository item content (CODEOWNERS), and
// PR iterations/changes (changed-file listing) -- fixture-driven, real
// HTTP round trips through net/http, collapsing Azure DevOps' own two
// real hosts (dev.azure.com, vssps.dev.azure.com) onto one httptest
// server via WithBaseURL+WithGraphBaseURL together, the same real
// pattern azuredevops.Client's own doc comment documents for tests.
type fakeAzureDevOpsAuthServer struct {
	project      string
	repositoryID string
	prID         int
	// subjects["User"]["roozbeh"] = "descriptor-roozbeh" -- QuerySubjectDescriptor's own real (query, kind) -> descriptor lookup.
	subjects map[string]map[string]string
	// memberships["descriptor-alice"]["descriptor-group"] = true
	memberships  map[string]map[string]bool
	codeowners   string
	changedFiles []string
}

func (f *fakeAzureDevOpsAuthServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/_apis/graph/subjectquery", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query       string   `json:"query"`
			SubjectKind []string `json:"subjectKind"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if len(body.SubjectKind) == 0 {
			w.Write([]byte(`[]`))
			return
		}
		descriptor, ok := f.subjects[body.SubjectKind[0]][body.Query]
		if !ok {
			w.Write([]byte(`[]`))
			return
		}
		w.Write([]byte(`[{"descriptor":"` + descriptor + `"}]`))
	})

	mux.HandleFunc("/_apis/graph/memberships/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/_apis/graph/memberships/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		subject, container := parts[0], parts[1]
		if f.memberships[subject][container] {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	})

	base := "/" + f.project + "/_apis/git/repositories/" + f.repositoryID

	mux.HandleFunc(base+"/items", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		if path != "CODEOWNERS" || f.codeowners == "" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(f.codeowners))
	})

	prBase := base + "/pullRequests/" + strconv.Itoa(f.prID)
	mux.HandleFunc(prBase+"/iterations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value":[{"id":1}]}`))
	})
	mux.HandleFunc(prBase+"/iterations/1/changes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		entries := make([]map[string]any, 0, len(f.changedFiles))
		for _, path := range f.changedFiles {
			entries = append(entries, map[string]any{"item": map[string]any{"path": path}})
		}
		body, _ := json.Marshal(map[string]any{"changeEntries": entries})
		w.Write(body)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestAzureDevOpsClient(t *testing.T, srv *httptest.Server) *adevops.Client {
	t.Helper()
	return adevops.New("acme-org", "test-token", adevops.WithBaseURL(srv.URL), adevops.WithGraphBaseURL(srv.URL))
}

func identityRef(displayName, descriptor string) *git.IdentityRefWithVote {
	return &git.IdentityRefWithVote{DisplayName: &displayName, Descriptor: &descriptor}
}

func TestIsAuthorizedToReplanAzureDevOps_HumanCreatorMatch(t *testing.T) {
	creator := identityRef("Roozbeh Shafiee", "descriptor-roozbeh")
	commenter := identityRef("Roozbeh Shafiee", "descriptor-roozbeh")
	ok, err := isAuthorizedToReplanAzureDevOps(context.Background(), nil, "acme-infra", "ubx-bot", creator, commenter)
	if err != nil {
		t.Fatalf("isAuthorizedToReplanAzureDevOps: %v", err)
	}
	if !ok {
		t.Error("expected true -- the commenter is the PR's own real creator (matched by Descriptor)")
	}
}

func TestIsAuthorizedToReplanAzureDevOps_HumanCreatorMismatchRejected(t *testing.T) {
	creator := identityRef("Roozbeh Shafiee", "descriptor-roozbeh")
	commenter := identityRef("Someone Else", "descriptor-someone-else")
	ok, err := isAuthorizedToReplanAzureDevOps(context.Background(), nil, "acme-infra", "ubx-bot", creator, commenter)
	if err != nil {
		t.Fatalf("isAuthorizedToReplanAzureDevOps: %v", err)
	}
	if ok {
		t.Error("expected false -- someone-else is neither the PR's own creator nor the bot-PR fallback applies")
	}
}

func TestIsAuthorizedToReplanAzureDevOps_BotPRWithContributorAccess(t *testing.T) {
	f := &fakeAzureDevOpsAuthServer{
		project: "acme-infra",
		subjects: map[string]map[string]string{
			"Group": {"[acme-infra]\\Contributors": "descriptor-contributors"},
		},
		memberships: map[string]map[string]bool{
			"descriptor-roozbeh": {"descriptor-contributors": true},
		},
	}
	api := newTestAzureDevOpsClient(t, f.server(t))

	creator := identityRef("ubx-bot", "descriptor-bot")
	commenter := identityRef("Roozbeh Shafiee", "descriptor-roozbeh")
	ok, err := isAuthorizedToReplanAzureDevOps(context.Background(), api, f.project, "ubx-bot", creator, commenter)
	if err != nil {
		t.Fatalf("isAuthorizedToReplanAzureDevOps: %v", err)
	}
	if !ok {
		t.Error("expected true -- a bot-opened PR falls back to real Contributors-group membership, and roozbeh is a member")
	}
}

func TestIsAuthorizedToReplanAzureDevOps_BotPRWithoutContributorAccessRejected(t *testing.T) {
	f := &fakeAzureDevOpsAuthServer{
		project: "acme-infra",
		subjects: map[string]map[string]string{
			"Group": {"[acme-infra]\\Contributors": "descriptor-contributors"},
		},
		memberships: map[string]map[string]bool{},
	}
	api := newTestAzureDevOpsClient(t, f.server(t))

	creator := identityRef("ubx-bot", "descriptor-bot")
	commenter := identityRef("Random Reader", "descriptor-reader")
	ok, err := isAuthorizedToReplanAzureDevOps(context.Background(), api, f.project, "ubx-bot", creator, commenter)
	if err != nil {
		t.Fatalf("isAuthorizedToReplanAzureDevOps: %v", err)
	}
	if ok {
		t.Error("expected false -- non-member must not authorize a re-plan on a bot-opened PR")
	}
}

// TestIsAuthorizedToShipAzureDevOps_DirectDisplayNameOwnerMatch proves
// the real match against ownsAsAzureDevOps' own chosen identifier
// (DisplayName, since Azure DevOps has no fixed "username" concept --
// see that function's own doc comment). The CODEOWNERS entry is
// deliberately a single, space-free token ("roozbeh"), matching real
// CODEOWNERS syntax (whitespace-delimited fields per line) rather than
// a real Azure DevOps display name that might contain a space -- an
// operator writing CODEOWNERS entries for Azure DevOps needs a
// space-free identifier either way, the same real constraint this
// test's own fixture reflects.
func TestIsAuthorizedToShipAzureDevOps_DirectDisplayNameOwnerMatch(t *testing.T) {
	f := &fakeAzureDevOpsAuthServer{
		project:      "acme-infra",
		repositoryID: "repo-1",
		prID:         42,
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @roozbeh\n",
	}
	api := newTestAzureDevOpsClient(t, f.server(t))

	ok, err := isAuthorizedToShipAzureDevOps(context.Background(), api, f.project, f.repositoryID, f.prID, "roozbeh", "descriptor-roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToShipAzureDevOps: %v", err)
	}
	if !ok {
		t.Error("expected true -- roozbeh directly owns the one changed file per CODEOWNERS")
	}
}

func TestIsAuthorizedToShipAzureDevOps_DirectDisplayNameOwnerMismatchRejected(t *testing.T) {
	f := &fakeAzureDevOpsAuthServer{
		project:      "acme-infra",
		repositoryID: "repo-1",
		prID:         42,
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @roozbeh\n",
	}
	api := newTestAzureDevOpsClient(t, f.server(t))

	ok, err := isAuthorizedToShipAzureDevOps(context.Background(), api, f.project, f.repositoryID, f.prID, "alice", "descriptor-alice")
	if err != nil {
		t.Fatalf("isAuthorizedToShipAzureDevOps: %v", err)
	}
	if ok {
		t.Error("expected false -- alice owns nothing this PR changes")
	}
}

func TestIsAuthorizedToShipAzureDevOps_GroupOwnerMatchViaMembership(t *testing.T) {
	f := &fakeAzureDevOpsAuthServer{
		project:      "acme-infra",
		repositoryID: "repo-1",
		prID:         42,
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @acme/backend-team\n",
		subjects: map[string]map[string]string{
			"Group": {"acme/backend-team": "descriptor-backend-team"},
		},
		memberships: map[string]map[string]bool{
			"descriptor-alice": {"descriptor-backend-team": true},
		},
	}
	api := newTestAzureDevOpsClient(t, f.server(t))

	ok, err := isAuthorizedToShipAzureDevOps(context.Background(), api, f.project, f.repositoryID, f.prID, "alice", "descriptor-alice")
	if err != nil {
		t.Fatalf("isAuthorizedToShipAzureDevOps: %v", err)
	}
	if !ok {
		t.Error("expected true -- alice is a real, current member of the owning group")
	}
}

func TestIsAuthorizedToShipAzureDevOps_GroupOwnerNonMemberRejected(t *testing.T) {
	f := &fakeAzureDevOpsAuthServer{
		project:      "acme-infra",
		repositoryID: "repo-1",
		prID:         42,
		changedFiles: []string{"stacks/payments/main.tf"},
		codeowners:   "/stacks/payments/ @acme/backend-team\n",
		subjects: map[string]map[string]string{
			"Group": {"acme/backend-team": "descriptor-backend-team"},
		},
		memberships: map[string]map[string]bool{},
	}
	api := newTestAzureDevOpsClient(t, f.server(t))

	ok, err := isAuthorizedToShipAzureDevOps(context.Background(), api, f.project, f.repositoryID, f.prID, "bob", "descriptor-bob")
	if err != nil {
		t.Fatalf("isAuthorizedToShipAzureDevOps: %v", err)
	}
	if ok {
		t.Error("expected false -- bob is not a member of the owning group")
	}
}

func TestIsAuthorizedToShipAzureDevOps_NoCodeownersFileRejectsEveryone(t *testing.T) {
	f := &fakeAzureDevOpsAuthServer{
		project:      "acme-infra",
		repositoryID: "repo-1",
		prID:         42,
		changedFiles: []string{"stacks/payments/main.tf"},
	}
	api := newTestAzureDevOpsClient(t, f.server(t))

	ok, err := isAuthorizedToShipAzureDevOps(context.Background(), api, f.project, f.repositoryID, f.prID, "roozbeh", "descriptor-roozbeh")
	if err != nil {
		t.Fatalf("isAuthorizedToShipAzureDevOps: %v", err)
	}
	if ok {
		t.Error("expected false -- no CODEOWNERS file at all authorizes nobody")
	}
}
