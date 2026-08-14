package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	ghapi "github.com/google/go-github/v78/github"
	ghub "github.com/ubiquex/ubiquex/github"
)

// fakeCommentServer serves exactly the two real endpoints
// postOrEditComment needs, backed by a real, mutable in-memory comment
// list -- a genuine create-then-edit round trip through net/http, not a
// stubbed single response.
type fakeCommentServer struct {
	comments []*ghapi.IssueComment
	nextID   int64
}

func (f *fakeCommentServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/repos/acme/infra/issues/42/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(f.comments)
		case http.MethodPost:
			var body ghapi.IssueComment
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.nextID++
			id := f.nextID
			body.ID = &id
			login := "ubx[bot]"
			body.User = &ghapi.User{Login: &login}
			f.comments = append(f.comments, &body)
			_ = json.NewEncoder(w).Encode(body)
		}
	})

	mux.HandleFunc("/repos/acme/infra/issues/comments/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		idStr := strings.TrimPrefix(r.URL.Path, "/repos/acme/infra/issues/comments/")
		var body ghapi.IssueComment
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, c := range f.comments {
			if idStrOf(c.GetID()) == idStr {
				c.Body = body.Body
				_ = json.NewEncoder(w).Encode(c)
				return
			}
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func idStrOf(id int64) string {
	return strconv.FormatInt(id, 10)
}

func TestPostOrEditComment_CreatesWhenNoneExists(t *testing.T) {
	f := &fakeCommentServer{}
	api := ghub.New("test-token", ghub.WithBaseURL(f.server(t).URL+"/"))

	if err := postOrEditComment(context.Background(), api, "acme", "infra", 42, "ubx[bot]", "plan", "delta: 1 creates"); err != nil {
		t.Fatalf("postOrEditComment: %v", err)
	}
	if len(f.comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(f.comments))
	}
	if !strings.Contains(f.comments[0].GetBody(), "delta: 1 creates") {
		t.Errorf("comment body = %q, want it to contain the real plan output", f.comments[0].GetBody())
	}
	if !strings.HasPrefix(f.comments[0].GetBody(), "<!-- ubx:plan -->") {
		t.Errorf("comment body = %q, want it to start with the real plan marker", f.comments[0].GetBody())
	}
}

// TestPostOrEditComment_EditsExistingInPlace is the real, load-bearing
// case: a second plan must edit the first comment, not pile up a new
// one -- UBI-161's own --edit-last mechanism, reused here.
func TestPostOrEditComment_EditsExistingInPlace(t *testing.T) {
	f := &fakeCommentServer{}
	api := ghub.New("test-token", ghub.WithBaseURL(f.server(t).URL+"/"))
	ctx := context.Background()

	if err := postOrEditComment(ctx, api, "acme", "infra", 42, "ubx[bot]", "plan", "delta: 1 creates"); err != nil {
		t.Fatalf("first postOrEditComment: %v", err)
	}
	if err := postOrEditComment(ctx, api, "acme", "infra", 42, "ubx[bot]", "plan", "delta: 2 creates"); err != nil {
		t.Fatalf("second postOrEditComment: %v", err)
	}

	if len(f.comments) != 1 {
		t.Fatalf("comments = %d, want exactly 1 -- the second plan must edit the first, not add a second", len(f.comments))
	}
	if !strings.Contains(f.comments[0].GetBody(), "delta: 2 creates") {
		t.Errorf("comment body = %q, want the real, current (second) plan output", f.comments[0].GetBody())
	}
}

// TestPostOrEditComment_IgnoresOtherKinds proves the marker actually
// distinguishes kinds: an existing "ship" comment must never be
// overwritten by a "plan" post.
func TestPostOrEditComment_IgnoresOtherKinds(t *testing.T) {
	f := &fakeCommentServer{}
	api := ghub.New("test-token", ghub.WithBaseURL(f.server(t).URL+"/"))
	ctx := context.Background()

	if err := postOrEditComment(ctx, api, "acme", "infra", 42, "ubx[bot]", "ship", "shipped abc123"); err != nil {
		t.Fatalf("postOrEditComment (ship): %v", err)
	}
	if err := postOrEditComment(ctx, api, "acme", "infra", 42, "ubx[bot]", "plan", "delta: 1 creates"); err != nil {
		t.Fatalf("postOrEditComment (plan): %v", err)
	}

	if len(f.comments) != 2 {
		t.Fatalf("comments = %d, want 2 -- a plan comment must never overwrite a ship comment", len(f.comments))
	}
}

// TestPostOrEditComment_IgnoresNonBotComments proves a human's own
// comment on the PR, even one that happens to start with the same
// marker text, is never treated as this bot's own "last" comment.
func TestPostOrEditComment_IgnoresNonBotComments(t *testing.T) {
	f := &fakeCommentServer{
		comments: []*ghapi.IssueComment{
			{ID: ghapi.Ptr(int64(1)), Body: ghapi.Ptr("<!-- ubx:plan -->\nold human copy-paste"), User: &ghapi.User{Login: ghapi.Ptr("someone-else")}},
		},
	}
	f.nextID = 1
	api := ghub.New("test-token", ghub.WithBaseURL(f.server(t).URL+"/"))

	if err := postOrEditComment(context.Background(), api, "acme", "infra", 42, "ubx[bot]", "plan", "delta: 1 creates"); err != nil {
		t.Fatalf("postOrEditComment: %v", err)
	}

	if len(f.comments) != 2 {
		t.Fatalf("comments = %d, want 2 -- a human's own comment must never be edited by the bot", len(f.comments))
	}
	if f.comments[0].GetBody() != "<!-- ubx:plan -->\nold human copy-paste" {
		t.Error("the human's own comment must be left completely untouched")
	}
}
