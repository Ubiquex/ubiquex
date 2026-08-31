package bitbucketserver

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// UBI-165: Bitbucket Server is the one platform of the five with no
// issue tracker at all, and the one whose file write is
// multipart/form-data rather than JSON. Both are pinned here against a
// real httptest server.

type bbsSurfaceFixture struct {
	mu       sync.Mutex
	paths    []string
	bodies   map[string]string
	ctypes   map[string]string
	branches map[string]string // form fields captured from the multipart write
}

func newBBSSurfaceFixture(t *testing.T) (*bbsSurfaceFixture, *Client) {
	t.Helper()
	f := &bbsSurfaceFixture{bodies: map[string]string{}, ctypes: map[string]string{}, branches: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		ct := r.Header.Get("Content-Type")

		if strings.HasPrefix(ct, "multipart/form-data") {
			_, params, err := mime.ParseMediaType(ct)
			if err == nil {
				mr := multipart.NewReader(r.Body, params["boundary"])
				for {
					part, perr := mr.NextPart()
					if perr != nil {
						break
					}
					b, _ := io.ReadAll(part)
					f.mu.Lock()
					f.branches[part.FormName()] = string(b)
					f.mu.Unlock()
				}
			}
		} else {
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.bodies[key] = string(body)
			f.mu.Unlock()
		}
		f.mu.Lock()
		f.paths = append(f.paths, key)
		f.ctypes[key] = ct
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/branches/default"):
			_, _ = w.Write([]byte(`{"id":"refs/heads/trunk","displayId":"trunk"}`))
		case strings.HasSuffix(r.URL.Path, "/pull-requests"):
			_, _ = w.Write([]byte(`{"id":9,"links":{"self":[{"href":"https://bitbucket.example.com/projects/INFRA/repos/infra/pull-requests/9"}]}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return f, New(srv.URL, "test-token")
}

// TestErrNoIssueTracker_IsRealAndExplains is the whole point for this
// platform. Bitbucket Server ships no issue tracker; Atlassian's model
// is Jira. Surfacing an issue here has to refuse, loudly and with a
// pointer to what to do instead, rather than silently opening a pull
// request that commits a file to somebody's repository.
func TestErrNoIssueTracker_IsRealAndExplains(t *testing.T) {
	if ErrNoIssueTracker == nil {
		t.Fatal("expected a real sentinel error")
	}
	msg := ErrNoIssueTracker.Error()
	for _, want := range []string{"no issue tracker", "Jira", "--surface-as pr"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

// TestOpenDraftPR_CreatesBranchAndCommitInOneCall pins Bitbucket
// Server's own real capability: the file write takes both `branch` (the
// branch to commit on) and `sourceBranch` (where to start it from), so
// there is no separate branch-creation call at all -- unlike GitHub and
// Azure DevOps, which each need one.
func TestOpenDraftPR_CreatesBranchAndCommitInOneCall(t *testing.T) {
	f, api := newBBSSurfaceFixture(t)

	pr, err := OpenDraftPR(context.Background(), api, "INFRA", "infra", "ubx-drift/x", "ubx-drift/x.json",
		[]byte(`{"schema_version":1}`), "record drift", "drift: x", "receipt")
	if err != nil {
		t.Fatalf("OpenDraftPR: %v", err)
	}
	if pr.Number != 9 {
		t.Errorf("Number = %d, want 9", pr.Number)
	}
	if pr.HTMLURL == "" {
		t.Error("HTMLURL empty, want the real links.self href")
	}

	f.mu.Lock()
	paths := append([]string(nil), f.paths...)
	branch, sourceBranch, content := f.branches["branch"], f.branches["sourceBranch"], f.branches["content"]
	f.mu.Unlock()

	for _, p := range paths {
		if strings.Contains(p, "/branches") && !strings.Contains(p, "/branches/default") {
			t.Errorf("made a separate branch-creation call (%s): Bitbucket Server creates the branch as part of the file write", p)
		}
	}
	if branch != "ubx-drift/x" {
		t.Errorf("branch form field = %q", branch)
	}
	if sourceBranch != "refs/heads/trunk" {
		t.Errorf("sourceBranch = %q, want the repository's own declared default, not a hardcoded main", sourceBranch)
	}
	if content != `{"schema_version":1}` {
		t.Errorf("content = %q", content)
	}

	// The write must be multipart, which is what this endpoint takes and
	// is unlike every other call this package makes.
	var wroteMultipart bool
	f.mu.Lock()
	for k, ct := range f.ctypes {
		if strings.Contains(k, "/browse/") && strings.HasPrefix(ct, "multipart/form-data") {
			wroteMultipart = true
		}
	}
	f.mu.Unlock()
	if !wroteMultipart {
		t.Error("the file write was not multipart/form-data")
	}
}

// TestOpenDraftPR_TargetsTheRepositorysOwnDefaultBranch keeps the pull
// request honest about where it merges to, and marks draft in the title
// since Bitbucket Server has no draft flag.
func TestOpenDraftPR_TargetsTheRepositorysOwnDefaultBranch(t *testing.T) {
	f, api := newBBSSurfaceFixture(t)

	if _, err := OpenDraftPR(context.Background(), api, "INFRA", "infra", "ubx-drift/x", "p.json",
		[]byte(`{}`), "msg", "drift: x", "receipt"); err != nil {
		t.Fatalf("OpenDraftPR: %v", err)
	}

	f.mu.Lock()
	var raw string
	for k, v := range f.bodies {
		if strings.HasSuffix(k, "/pull-requests") {
			raw = v
		}
	}
	f.mu.Unlock()
	if raw == "" {
		t.Fatal("no pull request body recorded")
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	toRef := body["toRef"].(map[string]any)
	if toRef["id"] != "refs/heads/trunk" {
		t.Errorf("toRef.id = %v, want the repository's own declared default", toRef["id"])
	}
	fromRef := body["fromRef"].(map[string]any)
	if fromRef["id"] != "refs/heads/ubx-drift/x" {
		t.Errorf("fromRef.id = %v", fromRef["id"])
	}
	if title, _ := body["title"].(string); !strings.HasPrefix(title, "Draft: ") {
		t.Errorf("title = %q: Bitbucket Server has no draft flag, so draft must be carried in the title", title)
	}
}
