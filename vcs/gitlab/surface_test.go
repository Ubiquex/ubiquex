package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// UBI-165: GitLab is the closest of the four non-GitHub platforms to
// GitHub's own shape, having a real first-class issue tracker on every
// project. These exercise the real requests against a real httptest
// server, never a transport-level mock.

type glSurfaceFixture struct {
	mu       sync.Mutex
	requests []string
	bodies   map[string]string
}

func newGLSurfaceFixture(t *testing.T) (*glSurfaceFixture, *Client) {
	t.Helper()
	f := &glSurfaceFixture{bodies: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.requests = append(f.requests, key)
		f.bodies[key] = string(body)
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues") && r.Method == http.MethodPost:
			// "id" is not optional in this fixture: go-gitlab's own
			// Issue.UnmarshalJSON calls reflect.TypeOf(raw["id"]).Kind()
			// unconditionally and panics on a nil interface when the
			// field is absent. Real GitLab always sends it, so including
			// it keeps the fixture honest rather than working around a
			// quirk with a fake shape.
			_, _ = w.Write([]byte(`{"id":101,"iid":7,"web_url":"https://gitlab.example.com/acme/infra/-/issues/7"}`))
		case strings.HasSuffix(r.URL.Path, "/repository/branches") && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"name":"ubx-drift/x"}`))
		case strings.Contains(r.URL.Path, "/repository/files/") && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"file_path":"ubx-drift/x.json","branch":"ubx-drift/x"}`))
		case strings.HasSuffix(r.URL.Path, "/merge_requests") && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"id":202,"iid":9,"web_url":"https://gitlab.example.com/acme/infra/-/merge_requests/9"}`))
		case r.Method == http.MethodGet:
			// GetProject, for the real default branch.
			_, _ = w.Write([]byte(`{"id":1,"path_with_namespace":"acme/infra","default_branch":"trunk"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := New("test-token", WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return f, c
}

func (f *glSurfaceFixture) body(t *testing.T, key string) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, ok := f.bodies[key]
	if !ok {
		t.Fatalf("no request recorded for %q; recorded: %v", key, f.requests)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode body for %q (%q): %v", key, raw, err)
	}
	return out
}

func TestCreateIssue_PostsTitleAndDescription(t *testing.T) {
	f, api := newGLSurfaceFixture(t)

	issue, err := CreateIssue(context.Background(), api, "acme/infra", "drift: payments.fake_widget.w", "receipt body")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issue.Number != 7 {
		t.Errorf("Number = %d, want GitLab's own per-project IID 7", issue.Number)
	}
	if issue.HTMLURL == "" {
		t.Error("HTMLURL is empty, want the real web_url GitLab returned")
	}

	body := f.body(t, "POST /api/v4/projects/acme/infra/issues")
	if body["title"] != "drift: payments.fake_widget.w" {
		t.Errorf("title = %v", body["title"])
	}
	// GitLab calls it description, not body -- the real difference from
	// GitHub, and the receipt must land in the right field.
	if body["description"] != "receipt body" {
		t.Errorf("description = %v, want the receipt", body["description"])
	}
	if _, wrong := body["body"]; wrong {
		t.Error("sent a \"body\" field: that is GitHub's name, not GitLab's")
	}
}

// TestOpenDraftMR_BranchesFromTheProjectsOwnDefault is the load-bearing
// one: the merge request must target whatever the project actually
// declares as its default branch, never a hardcoded "main". The fixture
// deliberately reports "trunk".
func TestOpenDraftMR_BranchesFromTheProjectsOwnDefault(t *testing.T) {
	f, api := newGLSurfaceFixture(t)

	mr, err := OpenDraftMR(context.Background(), api, "acme/infra", "ubx-drift/x", "ubx-drift/x.json",
		[]byte(`{"schema_version":1}`), "record drift", "drift: payments.fake_widget.w", "receipt")
	if err != nil {
		t.Fatalf("OpenDraftMR: %v", err)
	}
	if mr.Number != 9 {
		t.Errorf("Number = %d, want 9", mr.Number)
	}

	branch := f.body(t, "POST /api/v4/projects/acme/infra/repository/branches")
	if branch["ref"] != "trunk" {
		t.Errorf("branched from %v, want the project's own declared default \"trunk\"", branch["ref"])
	}
	mrBody := f.body(t, "POST /api/v4/projects/acme/infra/merge_requests")
	if mrBody["target_branch"] != "trunk" {
		t.Errorf("target_branch = %v, want \"trunk\"", mrBody["target_branch"])
	}
	if mrBody["source_branch"] != "ubx-drift/x" {
		t.Errorf("source_branch = %v", mrBody["source_branch"])
	}
}

// TestOpenDraftMR_MarksDraftInTheTitle covers GitLab's real difference
// from GitHub and Azure DevOps: there is no draft boolean at all, so the
// only way to mark a merge request as a draft is the title convention.
func TestOpenDraftMR_MarksDraftInTheTitle(t *testing.T) {
	f, api := newGLSurfaceFixture(t)

	if _, err := OpenDraftMR(context.Background(), api, "acme/infra", "b", "p.json",
		[]byte(`{}`), "msg", "drift: payments.fake_widget.w", "receipt"); err != nil {
		t.Fatalf("OpenDraftMR: %v", err)
	}
	title, _ := f.body(t, "POST /api/v4/projects/acme/infra/merge_requests")["title"].(string)
	if !strings.HasPrefix(title, "Draft: ") {
		t.Errorf("title = %q, want GitLab's own Draft: prefix", title)
	}
}

func TestDraftTitle_NeverDoubledUp(t *testing.T) {
	for _, in := range []string{"Draft: already", "draft: already"} {
		if got := draftTitle(in); got != in {
			t.Errorf("draftTitle(%q) = %q, want it left alone", in, got)
		}
	}
	if got := draftTitle("plain"); got != "Draft: plain" {
		t.Errorf("draftTitle(\"plain\") = %q", got)
	}
}
