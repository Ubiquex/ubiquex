package bitbucketcloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// UBI-165: Bitbucket Cloud is the only platform of the five whose file
// write is form-encoded with the repository path used as the FIELD
// NAME, and the only one that creates a branch and a commit in a single
// call. Both are Atlassian's own documented behavior, and both are
// pinned here against a real httptest server.

type bbcSurfaceFixture struct {
	mu     sync.Mutex
	paths  []string
	ctypes map[string]string
	bodies map[string]string
}

func newBBCSurfaceFixture(t *testing.T) (*bbcSurfaceFixture, *Client) {
	t.Helper()
	f := &bbcSurfaceFixture{ctypes: map[string]string{}, bodies: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.paths = append(f.paths, key)
		f.ctypes[key] = r.Header.Get("Content-Type")
		f.bodies[key] = string(body)
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues"):
			_, _ = w.Write([]byte(`{"id":7,"links":{"html":{"href":"https://bitbucket.org/acme/infra/issues/7"}}}`))
		case strings.HasSuffix(r.URL.Path, "/src"):
			w.WriteHeader(http.StatusCreated)
		case strings.HasSuffix(r.URL.Path, "/pullrequests"):
			_, _ = w.Write([]byte(`{"id":9,"links":{"html":{"href":"https://bitbucket.org/acme/infra/pull-requests/9"}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return f, New("test-token", WithBaseURL(srv.URL))
}

func (f *bbcSurfaceFixture) get(t *testing.T, suffix string) (path, ctype, body string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.paths {
		if strings.HasSuffix(p, suffix) {
			return p, f.ctypes[p], f.bodies[p]
		}
	}
	t.Fatalf("no request ending in %q; recorded: %v", suffix, f.paths)
	return "", "", ""
}

// TestCreateIssue_UsesNestedContentRaw pins Bitbucket Cloud's own real
// body shape. GitHub takes a flat "body" string; Bitbucket Cloud takes
// a nested content.raw object, the same shape its pull request comments
// use. Assuming symmetry with GitHub here would silently post an issue
// with an empty description.
func TestCreateIssue_UsesNestedContentRaw(t *testing.T) {
	f, api := newBBCSurfaceFixture(t)

	issue, err := CreateIssue(context.Background(), api, "acme", "infra", "drift: x", "receipt body")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issue.ID != 7 {
		t.Errorf("ID = %d, want 7", issue.ID)
	}
	if issue.HTMLURL == "" {
		t.Error("HTMLURL empty, want the real links.html.href")
	}

	_, _, raw := f.get(t, "/issues")
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	if body["title"] != "drift: x" {
		t.Errorf("title = %v", body["title"])
	}
	content, ok := body["content"].(map[string]any)
	if !ok {
		t.Fatalf("content is not a nested object: %v", body["content"])
	}
	if content["raw"] != "receipt body" {
		t.Errorf("content.raw = %v, want the receipt", content["raw"])
	}
	if _, wrong := body["body"]; wrong {
		t.Error("sent a flat \"body\": that is GitHub's shape, not Bitbucket Cloud's")
	}
}

// TestOpenDraftPR_FormEncodesThePathAsTheFieldName is the load-bearing
// one for this platform. Atlassian's own documented convention is that
// the form FIELD NAME is the file's repository path, with an explicit
// leading slash to disambiguate from the metadata fields (message,
// branch, author, parents, files).
func TestOpenDraftPR_FormEncodesThePathAsTheFieldName(t *testing.T) {
	f, api := newBBCSurfaceFixture(t)

	pr, err := OpenDraftPR(context.Background(), api, "acme", "infra", "ubx-drift/x", "ubx-drift/x.json",
		[]byte(`{"schema_version":1}`), "record drift", "drift: x", "receipt")
	if err != nil {
		t.Fatalf("OpenDraftPR: %v", err)
	}
	if pr.Number != 9 {
		t.Errorf("Number = %d, want 9", pr.Number)
	}

	_, ctype, raw := f.get(t, "/src")
	if ctype != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want application/x-www-form-urlencoded", ctype)
	}
	form, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("body is not form-encoded: %v", err)
	}
	if got := form.Get("/ubx-drift/x.json"); got != `{"schema_version":1}` {
		t.Errorf("the file field %q = %q, want the proposal JSON -- the path IS the field name, with a leading slash", "/ubx-drift/x.json", got)
	}
	if form.Get("branch") != "ubx-drift/x" {
		t.Errorf("branch = %q", form.Get("branch"))
	}
	if form.Get("message") != "record drift" {
		t.Errorf("message = %q", form.Get("message"))
	}
}

// TestOpenDraftPR_TakesExactlyTwoCalls pins the real consequence of
// Bitbucket Cloud creating branch and commit together and defaulting a
// pull request's destination to the repository's own mainbranch: no
// default-branch lookup, no branch call. A regression that added either
// back would be a real, needless extra round trip.
func TestOpenDraftPR_TakesExactlyTwoCalls(t *testing.T) {
	f, api := newBBCSurfaceFixture(t)

	if _, err := OpenDraftPR(context.Background(), api, "acme", "infra", "b", "p.json",
		[]byte(`{}`), "msg", "drift: x", "receipt"); err != nil {
		t.Fatalf("OpenDraftPR: %v", err)
	}

	f.mu.Lock()
	paths := append([]string(nil), f.paths...)
	f.mu.Unlock()
	if len(paths) != 2 {
		t.Errorf("made %d calls (%v), want exactly 2: the src write and the pull request", len(paths), paths)
	}

	_, _, raw := f.get(t, "/pullrequests")
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	// destination deliberately omitted: Atlassian documents that it
	// defaults to the repository's own mainbranch.
	if _, present := body["destination"]; present {
		t.Error("sent a destination: Bitbucket Cloud defaults it to the repository's own mainbranch")
	}
	source := body["source"].(map[string]any)["branch"].(map[string]any)
	if source["name"] != "b" {
		t.Errorf("source.branch.name = %v", source["name"])
	}
	if title, _ := body["title"].(string); !strings.HasPrefix(title, "Draft: ") {
		t.Errorf("title = %q: Bitbucket Cloud has no draft flag, so draft must be carried in the title", title)
	}
}
