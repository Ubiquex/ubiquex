package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bradleyfalzon/ghinstallation/v2"
	ghapi "github.com/google/go-github/v78/github"
	glapi "gitlab.com/gitlab-org/api/client-go/v2"

	adevops "github.com/ubiquex/ubiquex/azuredevops"
	bbserver "github.com/ubiquex/ubiquex/bitbucketserver"
	ghub "github.com/ubiquex/ubiquex/github"
	glab "github.com/ubiquex/ubiquex/gitlab"
)

// UBI-168: in every comment-triggered handler, the real, live
// authorization check must run BEFORE any clone or fetch. It used to run
// after on GitHub, GitLab, Azure DevOps and Bitbucket Server, which meant
// an unauthorized commenter on an allowlisted repository could still make
// this server do the real work of cloning that repository on every
// comment, before being told no. Bitbucket Cloud (UBI-170) was built the
// right way round from the start and is covered in
// bitbucketcloud_flow_test.go.
//
// Each test below asserts two things together, and both matter:
//
//   - the real refusal comment was posted, so the reordering did not
//     quietly skip the authorization decision itself, and
//   - the work directory is still completely empty.
//
// The second assertion is genuinely discriminating rather than
// decorative. ensureRepoCheckoutFromRemote calls os.MkdirAll on the
// checkout's parent BEFORE running git clone, so a handler that reaches
// the checkout leaves a real directory behind even though the clone
// itself cannot succeed against these fixtures. Reverting the reordering
// therefore fails these tests on the work-directory assertion, which is
// exactly the point: this is UBI-170's own verification method, not a
// logical assertion that the request was rejected.

// ---------------------------------------------------------------- GitHub

// ghCommentFixture serves every endpoint handleIssueCommentEvent touches
// up to and including the authorization decision, and records the
// comments the server actually posted. Real HTTP round trips throughout,
// never a transport-level mock.
type ghCommentFixture struct {
	mu         sync.Mutex
	posted     []string
	codeowners string
}

func newGHCommentFixture(t *testing.T, codeowners string) (*ghCommentFixture, *Server, string) {
	t.Helper()
	f := &ghCommentFixture{codeowners: codeowners}
	mux := http.NewServeMux()

	// The App JWT to installation token exchange. Present so the clone
	// path is genuinely reachable: if authorization ran too late, the
	// handler would get a real token and really attempt a checkout.
	mux.HandleFunc("/api/v3/app/installations/1/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"token":"ghs_test","expires_at":"2099-01-01T00:00:00Z"}`)
	})
	mux.HandleFunc("/api/v3/repos/acme/infra/pulls/7", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"number":7,"head":{"sha":"headsha"},"base":{"ref":"main"},"user":{"login":"author"}}`)
	})
	mux.HandleFunc("/api/v3/repos/acme/infra/pulls/7/files", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"filename":"proposals/db.json"}]`)
	})
	mux.HandleFunc("/api/v3/repos/acme/infra/contents/CODEOWNERS", func(w http.ResponseWriter, r *http.Request) {
		if f.codeowners == "" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "file", "encoding": "base64", "name": "CODEOWNERS", "path": "CODEOWNERS",
			"content": base64.StdEncoding.EncodeToString([]byte(f.codeowners)),
		})
	})
	mux.HandleFunc("/api/v3/repos/acme/infra/issues/7/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := readAllLimited(r)
			f.mu.Lock()
			f.posted = append(f.posted, body)
			f.mu.Unlock()
			fmt.Fprint(w, `{"id":1,"body":"","user":{"login":"ubx[bot]"}}`)
			return
		}
		fmt.Fprint(w, `[]`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	api, err := ghub.NewWithHTTPClient(&http.Client{Transport: ghTestTransport(t, srv.URL)},
		ghub.WithEnterpriseBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("ghub.NewWithHTTPClient: %v", err)
	}

	workDir := t.TempDir()
	s := &Server{
		cfg:      &Config{WorkDir: workDir, GitHubAPIBaseURL: srv.URL, Repos: []RepoConfig{{Platform: "github", Owner: "acme", Name: "infra"}}},
		botLogin: "ubx[bot]",
		installations: &installationClients{
			appID:   1,
			baseURL: srv.URL,
			entries: map[int64]installationEntry{
				1: {client: api, transport: ghTestTransport(t, srv.URL)},
			},
		},
	}
	return f, s, workDir
}

// ghTestTransport builds a real ghinstallation.Transport against a
// freshly generated key, pointed at the fixture. Real, not stubbed, so
// installationToken (which repo.go needs before it can clone) genuinely
// succeeds and the checkout is genuinely attempted whenever the handler
// reaches it.
func ghTestTransport(t *testing.T, baseURL string) *ghinstallation.Transport {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	itr, err := ghinstallation.New(http.DefaultTransport, 1, 1, pemKey)
	if err != nil {
		t.Fatalf("ghinstallation.New: %v", err)
	}
	itr.BaseURL = enterpriseAPIRoot(baseURL)
	return itr
}

func readAllLimited(r *http.Request) (string, error) {
	buf := make([]byte, r.ContentLength)
	_, err := r.Body.Read(buf)
	return string(buf), err
}

func (f *ghCommentFixture) postedBodies() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.posted, "\n")
}

func ghComment(body, commenter string) *ghapi.IssueCommentEvent {
	return &ghapi.IssueCommentEvent{
		Action:       ghapi.Ptr("created"),
		Repo:         &ghapi.Repository{Owner: &ghapi.User{Login: ghapi.Ptr("acme")}, Name: ghapi.Ptr("infra")},
		Installation: &ghapi.Installation{ID: ghapi.Ptr(int64(1))},
		Issue:        &ghapi.Issue{Number: ghapi.Ptr(7), PullRequestLinks: &ghapi.PullRequestLinks{}},
		Comment:      &ghapi.IssueComment{Body: ghapi.Ptr(body), User: &ghapi.User{Login: ghapi.Ptr(commenter)}},
	}
}

func TestGitHubComment_UnauthorizedReplanNeverClones(t *testing.T) {
	f, s, workDir := newGHCommentFixture(t, "")

	if err := s.handleIssueCommentEvent(context.Background(), ghComment("ubx plan", "stranger")); err != nil {
		t.Fatalf("handleIssueCommentEvent: %v", err)
	}

	if posted := f.postedBodies(); !strings.Contains(posted, "isn't authorized to re-run") {
		t.Errorf("posted = %q, want a real not-authorized refusal", posted)
	}
	assertWorkDirEmpty(t, workDir, "an unauthorized re-plan comment")
}

func TestGitHubComment_UnauthorizedShipNeverClones(t *testing.T) {
	f, s, workDir := newGHCommentFixture(t, "proposals/ @owner\n")

	if err := s.handleIssueCommentEvent(context.Background(), ghComment("ubx ship", "stranger")); err != nil {
		t.Fatalf("handleIssueCommentEvent: %v", err)
	}

	if posted := f.postedBodies(); !strings.Contains(posted, "isn't a CODEOWNERS-listed owner") {
		t.Errorf("posted = %q, want a real not-authorized refusal", posted)
	}
	assertWorkDirEmpty(t, workDir, "an unauthorized ship comment")
}

// TestGitHubComment_AuthorizedReplanDoesReachCheckout is the control.
// Without it, the two tests above would pass just as happily against a
// handler that never checks out at all, which would prove nothing about
// ordering. The PR's own creator is authorized, so this one must get
// past the decision and attempt the real checkout.
func TestGitHubComment_AuthorizedReplanDoesReachCheckout(t *testing.T) {
	_, s, workDir := newGHCommentFixture(t, "")

	// The clone cannot succeed against a fixture with no git remote, so
	// the error is expected and ignored; what matters is that the
	// handler got far enough to try.
	_ = s.handleIssueCommentEvent(context.Background(), ghComment("ubx plan", "author"))

	if workDirIsEmpty(t, workDir) {
		t.Error("an AUTHORIZED comment must reach the checkout -- if this passes, the unauthorized tests prove nothing")
	}
}

// ---------------------------------------------------------------- GitLab

type glCommentFixture struct {
	mu         sync.Mutex
	posted     []string
	codeowners string
}

func newGLCommentFixture(t *testing.T, codeowners string) (*glCommentFixture, *Server, string) {
	t.Helper()
	f := &glCommentFixture{codeowners: codeowners}
	// One catch-all handler rather than http.ServeMux routes: go-gitlab
	// URL-escapes the project path, so "acme/infra" arrives as a single
	// path segment ("acme%2Finfra") that ServeMux's own segment matching
	// cannot match against a multi-segment pattern. Matching on the
	// decoded r.URL.Path directly is both simpler and closer to what the
	// real API sees.
	const base = "/api/v4/projects/acme/infra"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := r.URL.Path; {
		case path == base+"/merge_requests/7":
			fmt.Fprint(w, `{"iid":7,"sha":"headsha","author":{"username":"author"},"target_branch":"main"}`)

		case path == base+"/merge_requests/7/changes":
			fmt.Fprint(w, `{"iid":7,"changes":[{"new_path":"proposals/db.json","old_path":"proposals/db.json"}]}`)

		case strings.HasSuffix(path, "/repository/files/CODEOWNERS"):
			if f.codeowners == "" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"file_name": "CODEOWNERS", "file_path": "CODEOWNERS", "encoding": "base64",
				"content": base64.StdEncoding.EncodeToString([]byte(f.codeowners)),
			})

		case path == base+"/merge_requests/7/notes":
			if r.Method == http.MethodPost {
				body, _ := readAllLimited(r)
				f.mu.Lock()
				f.posted = append(f.posted, body)
				f.mu.Unlock()
				fmt.Fprint(w, `{"id":1,"body":""}`)
				return
			}
			fmt.Fprint(w, `[]`)

		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	api, err := glab.New("test-token", glab.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("glab.New: %v", err)
	}

	workDir := t.TempDir()
	s := &Server{
		cfg: &Config{
			WorkDir: workDir, GitLabToken: "test-token", GitLabBotUsername: "ubx-bot",
			GitLabAPIBaseURL: srv.URL,
			Repos:            []RepoConfig{{Platform: "gitlab", Project: "acme/infra"}},
		},
		gitlab: api,
	}
	return f, s, workDir
}

func (f *glCommentFixture) postedBodies() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.posted, "\n")
}

func glComment(note, commenter string) *glapi.MergeCommentEvent {
	e := &glapi.MergeCommentEvent{}
	e.ObjectAttributes.Note = note
	e.ObjectAttributes.System = false
	e.Project.PathWithNamespace = "acme/infra"
	e.MergeRequest.IID = 7
	e.User = &glapi.EventUser{Username: commenter}
	return e
}

func TestGitLabComment_UnauthorizedReplanNeverClones(t *testing.T) {
	f, s, workDir := newGLCommentFixture(t, "")

	if err := s.handleMergeCommentEvent(context.Background(), glComment("ubx plan", "stranger")); err != nil {
		t.Fatalf("handleMergeCommentEvent: %v", err)
	}

	if posted := f.postedBodies(); !strings.Contains(posted, "isn't authorized to re-run") {
		t.Errorf("posted = %q, want a real not-authorized refusal", posted)
	}
	assertWorkDirEmpty(t, workDir, "an unauthorized re-plan note")
}

func TestGitLabComment_UnauthorizedShipNeverClones(t *testing.T) {
	f, s, workDir := newGLCommentFixture(t, "proposals/ @owner\n")

	if err := s.handleMergeCommentEvent(context.Background(), glComment("ubx ship", "stranger")); err != nil {
		t.Fatalf("handleMergeCommentEvent: %v", err)
	}

	if posted := f.postedBodies(); !strings.Contains(posted, "isn't a CODEOWNERS-listed owner") {
		t.Errorf("posted = %q, want a real not-authorized refusal", posted)
	}
	assertWorkDirEmpty(t, workDir, "an unauthorized ship note")
}

func TestGitLabComment_AuthorizedReplanDoesReachCheckout(t *testing.T) {
	_, s, workDir := newGLCommentFixture(t, "")

	_ = s.handleMergeCommentEvent(context.Background(), glComment("ubx plan", "author"))

	if workDirIsEmpty(t, workDir) {
		t.Error("an AUTHORIZED note must reach the checkout -- if this passes, the unauthorized tests prove nothing")
	}
}

// ----------------------------------------------------------- Azure DevOps

// Azure DevOps repository identity is a real GUID in the SDK's own
// GitPullRequest type, not a name, so the fixture uses one: a name here
// fails to decode before the handler is even reached.
const adoRepoGUID = "11111111-2222-3333-4444-555555555555"

type adoCommentFixture struct {
	mu         sync.Mutex
	posted     []string
	codeowners string
}

func newADOCommentFixture(t *testing.T, codeowners string) (*adoCommentFixture, *Server, string) {
	t.Helper()
	f := &adoCommentFixture{codeowners: codeowners}
	// A catch-all rather than ServeMux routes: Azure DevOps' own REST
	// paths mix casings for the same resource ("pullrequests" on the PR
	// itself, "pullRequests" on its sub-resources), which is real API
	// behavior worth matching loosely rather than pinning per route.
	prefix := "/acme/_apis/git/repositories/" + adoRepoGUID
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.ToLower(strings.TrimPrefix(r.URL.Path, prefix))
		switch {
		case path == "/pullrequests/7":
			fmt.Fprint(w, `{"pullRequestId":7,"lastMergeSourceCommit":{"commitId":"headsha"},
				"targetRefName":"refs/heads/main",
				"createdBy":{"displayName":"Author","descriptor":"aad.author"},
				"repository":{"id":"`+adoRepoGUID+`","name":"infra","project":{"name":"acme"}}}`)

		case path == "/pullrequests/7/iterations":
			fmt.Fprint(w, `{"value":[{"id":1}]}`)

		case path == "/pullrequests/7/iterations/1/changes":
			fmt.Fprint(w, `{"changeEntries":[{"item":{"path":"/proposals/db.json"}}]}`)

		case path == "/items":
			if f.codeowners == "" || !strings.Contains(r.URL.RawQuery, "CODEOWNERS") {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, f.codeowners)

		case path == "/pullrequests/7/threads":
			if r.Method == http.MethodPost {
				body, _ := readAllLimited(r)
				f.mu.Lock()
				f.posted = append(f.posted, body)
				f.mu.Unlock()
				fmt.Fprint(w, `{"id":1}`)
				return
			}
			fmt.Fprint(w, `{"value":[]}`)

		default:
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	workDir := t.TempDir()
	s := &Server{
		cfg: &Config{
			WorkDir: workDir, AzureDevOpsToken: "test-token", AzureDevOpsOrganization: "acme",
			AzureDevOpsBotDisplayName: "ubx-bot", AzureDevOpsAPIBaseURL: srv.URL,
			Repos: []RepoConfig{{Platform: "azuredevops", Project: "acme", Repository: adoRepoGUID}},
		},
		azuredevops: adevops.New("acme", "test-token",
			adevops.WithBaseURL(srv.URL), adevops.WithGraphBaseURL(srv.URL)),
	}
	return f, s, workDir
}

func (f *adoCommentFixture) postedBodies() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.posted, "\n")
}

func adoComment(t *testing.T, text, commenter string) json.RawMessage {
	t.Helper()
	return json.RawMessage(fmt.Sprintf(`{
		"comment":{"content":%q,"commentType":"text","author":{"displayName":%q,"descriptor":"aad.%s"}},
		"pullRequest":{"pullRequestId":7,"lastMergeSourceCommit":{"commitId":"headsha"},
			"repository":{"id":%q,"name":"infra","project":{"name":"acme"}}}}`,
		text, commenter, commenter, adoRepoGUID))
}

func TestAzureDevOpsComment_UnauthorizedReplanNeverClones(t *testing.T) {
	f, s, workDir := newADOCommentFixture(t, "")

	if err := s.handlePullRequestCommentAzureDevOps(context.Background(), adoComment(t, "ubx plan", "stranger")); err != nil {
		t.Fatalf("handlePullRequestCommentAzureDevOps: %v", err)
	}

	if posted := f.postedBodies(); !strings.Contains(posted, "isn't authorized to re-run") {
		t.Errorf("posted = %q, want a real not-authorized refusal", posted)
	}
	assertWorkDirEmpty(t, workDir, "an unauthorized re-plan comment")
}

func TestAzureDevOpsComment_UnauthorizedShipNeverClones(t *testing.T) {
	f, s, workDir := newADOCommentFixture(t, "proposals/ @owner\n")

	if err := s.handlePullRequestCommentAzureDevOps(context.Background(), adoComment(t, "ubx ship", "stranger")); err != nil {
		t.Fatalf("handlePullRequestCommentAzureDevOps: %v", err)
	}

	if posted := f.postedBodies(); !strings.Contains(posted, "isn't a CODEOWNERS-listed owner") {
		t.Errorf("posted = %q, want a real not-authorized refusal", posted)
	}
	assertWorkDirEmpty(t, workDir, "an unauthorized ship comment")
}

func TestAzureDevOpsComment_AuthorizedReplanDoesReachCheckout(t *testing.T) {
	_, s, workDir := newADOCommentFixture(t, "")

	_ = s.handlePullRequestCommentAzureDevOps(context.Background(), adoComment(t, "ubx plan", "author"))

	if workDirIsEmpty(t, workDir) {
		t.Error("an AUTHORIZED comment must reach the checkout -- if this passes, the unauthorized tests prove nothing")
	}
}

// -------------------------------------------------------- Bitbucket Server

type bbsCommentFixture struct {
	mu         sync.Mutex
	posted     []string
	codeowners string
}

func newBBSCommentFixture(t *testing.T, codeowners string) (*bbsCommentFixture, *Server, string) {
	t.Helper()
	f := &bbsCommentFixture{codeowners: codeowners}
	mux := http.NewServeMux()

	mux.HandleFunc("/rest/api/1.0/projects/INFRA/repos/infra/pull-requests/7", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":7,"fromRef":{"latestCommit":"headsha"},"toRef":{"id":"refs/heads/main",
			"repository":{"slug":"infra","project":{"key":"INFRA"}}},
			"author":{"user":{"name":"author","slug":"author"}}}`)
	})
	mux.HandleFunc("/rest/api/1.0/projects/INFRA/repos/infra/pull-requests/7/changes", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"values":[{"path":{"toString":"proposals/db.json"}}],"isLastPage":true}`)
	})
	mux.HandleFunc("/projects/INFRA/repos/infra/raw/CODEOWNERS", func(w http.ResponseWriter, r *http.Request) {
		if f.codeowners == "" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, f.codeowners)
	})
	mux.HandleFunc("/rest/api/1.0/projects/INFRA/repos/infra/pull-requests/7/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, _ := readAllLimited(r)
			f.mu.Lock()
			f.posted = append(f.posted, body)
			f.mu.Unlock()
			fmt.Fprint(w, `{"id":1,"version":0}`)
			return
		}
		fmt.Fprint(w, `{"values":[],"isLastPage":true}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	workDir := t.TempDir()
	s := &Server{
		cfg: &Config{
			WorkDir: workDir, BitbucketServerURL: srv.URL, BitbucketServerToken: "test-token",
			BitbucketServerBotName: "ubx-bot",
			Repos:                  []RepoConfig{{Platform: "bitbucketserver", Project: "INFRA", Repository: "infra"}},
		},
		bitbucketserver: bbserver.New(srv.URL, "test-token"),
	}
	return f, s, workDir
}

func (f *bbsCommentFixture) postedBodies() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.posted, "\n")
}

func bbsComment(text, commenter string) []byte {
	return []byte(fmt.Sprintf(`{
		"comment":{"text":%q,"author":{"name":%q,"slug":%q}},
		"pullRequest":{"id":7,"fromRef":{"latestCommit":"headsha"},
			"toRef":{"id":"refs/heads/main","repository":{"slug":"infra","project":{"key":"INFRA"}}}}}`,
		text, commenter, commenter))
}

func TestBitbucketServerComment_UnauthorizedReplanNeverClones(t *testing.T) {
	f, s, workDir := newBBSCommentFixture(t, "")

	if err := s.handlePullRequestCommentBitbucketServer(context.Background(), bbsComment("ubx plan", "stranger")); err != nil {
		t.Fatalf("handlePullRequestCommentBitbucketServer: %v", err)
	}

	if posted := f.postedBodies(); !strings.Contains(posted, "isn't authorized to re-run") {
		t.Errorf("posted = %q, want a real not-authorized refusal", posted)
	}
	assertWorkDirEmpty(t, workDir, "an unauthorized re-plan comment")
}

func TestBitbucketServerComment_UnauthorizedShipNeverClones(t *testing.T) {
	f, s, workDir := newBBSCommentFixture(t, "proposals/ @owner\n")

	if err := s.handlePullRequestCommentBitbucketServer(context.Background(), bbsComment("ubx ship", "stranger")); err != nil {
		t.Fatalf("handlePullRequestCommentBitbucketServer: %v", err)
	}

	if posted := f.postedBodies(); !strings.Contains(posted, "isn't a CODEOWNERS-listed owner") {
		t.Errorf("posted = %q, want a real not-authorized refusal", posted)
	}
	assertWorkDirEmpty(t, workDir, "an unauthorized ship comment")
}

func TestBitbucketServerComment_AuthorizedReplanDoesReachCheckout(t *testing.T) {
	_, s, workDir := newBBSCommentFixture(t, "")

	_ = s.handlePullRequestCommentBitbucketServer(context.Background(), bbsComment("ubx plan", "author"))

	if workDirIsEmpty(t, workDir) {
		t.Error("an AUTHORIZED comment must reach the checkout -- if this passes, the unauthorized tests prove nothing")
	}
}

// ----------------------------------------------------------------- shared

func assertWorkDirEmpty(t *testing.T, workDir, what string) {
	t.Helper()
	if workDirIsEmpty(t, workDir) {
		return
	}
	var found []string
	_ = filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && path != workDir {
			rel, _ := filepath.Rel(workDir, path)
			found = append(found, rel)
		}
		return nil
	})
	t.Errorf("UBI-168: %s must never trigger a clone or fetch, but the work directory contains %v", what, found)
}
