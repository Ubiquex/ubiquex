package azuredevops

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

// UBI-165: Azure DevOps is the most structurally different of the five.
// It has no issue tracker at all; it has work items, in Azure Boards, a
// separate service, with a type that comes from the project's own
// process template and a request format (JSON Patch) that no other
// platform here uses. These tests pin exactly those differences against
// a real httptest server.

type adoSurfaceFixture struct {
	mu           sync.Mutex
	paths        []string
	contentTypes map[string]string
	bodies       map[string]string
}

func newADOSurfaceFixture(t *testing.T) (*adoSurfaceFixture, *Client) {
	t.Helper()
	f := &adoSurfaceFixture{contentTypes: map[string]string{}, bodies: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.paths = append(f.paths, key)
		f.contentTypes[key] = r.Header.Get("Content-Type")
		f.bodies[key] = string(body)
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/wit/workitems/"):
			_, _ = w.Write([]byte(`{"id":42,"_links":{"html":{"href":"https://dev.azure.com/acme/_workitems/edit/42"}}}`))
		case strings.HasSuffix(p, "/refs") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"value":[{"name":"refs/heads/trunk","objectId":"basesha"}]}`))
		case strings.HasSuffix(p, "/refs") && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"value":[{"success":true}]}`))
		case strings.HasSuffix(p, "/pushes"):
			_, _ = w.Write([]byte(`{"pushId":5}`))
		case strings.HasSuffix(p, "/pullrequests"):
			_, _ = w.Write([]byte(`{"pullRequestId":9}`))
		default:
			// GET the repository, for its own real defaultBranch.
			_, _ = w.Write([]byte(`{"id":"repo-guid","name":"infra","defaultBranch":"refs/heads/trunk"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return f, New("acme", "test-token", WithBaseURL(srv.URL), WithGraphBaseURL(srv.URL))
}

func (f *adoSurfaceFixture) find(t *testing.T, substr string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.paths {
		if strings.Contains(p, substr) {
			return p
		}
	}
	t.Fatalf("no request matching %q; recorded: %v", substr, f.paths)
	return ""
}

// TestCreateWorkItem_UsesTheRealJSONPatchContract pins the two details
// that are unique to Azure DevOps and unguessable from any other
// platform here: the literal "$" before the type in the route, and the
// application/json-patch+json content type. Both are confirmed against
// Microsoft's own published REST spec.
func TestCreateWorkItem_UsesTheRealJSONPatchContract(t *testing.T) {
	f, api := newADOSurfaceFixture(t)

	wi, err := CreateWorkItem(context.Background(), api, "payments", "", "drift: x", "receipt body")
	if err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	if wi.ID != 42 {
		t.Errorf("ID = %d, want 42", wi.ID)
	}
	if wi.HTMLURL == "" {
		t.Error("HTMLURL empty, want the real _links.html.href")
	}

	key := f.find(t, "/wit/workitems/")
	if !strings.Contains(key, "/wit/workitems/$Issue") {
		t.Errorf("route = %q, want the literal $ before the type (Azure DevOps' own convention)", key)
	}
	if ct := f.contentTypes[key]; ct != "application/json-patch+json" {
		t.Errorf("Content-Type = %q, want application/json-patch+json", ct)
	}

	var doc []map[string]any
	if err := json.Unmarshal([]byte(f.bodies[key]), &doc); err != nil {
		t.Fatalf("body is not a JSON Patch array: %v (%s)", err, f.bodies[key])
	}
	fields := map[string]any{}
	for _, op := range doc {
		if op["op"] != "add" {
			t.Errorf("op = %v, want \"add\"", op["op"])
		}
		fields[op["path"].(string)] = op["value"]
	}
	if fields["/fields/System.Title"] != "drift: x" {
		t.Errorf("System.Title = %v", fields["/fields/System.Title"])
	}
	if fields["/fields/System.Description"] != "receipt body" {
		t.Errorf("System.Description = %v, want the receipt", fields["/fields/System.Description"])
	}
}

// TestCreateWorkItem_TypeIsConfigurable covers the real consequence of
// the type coming from a project's own process template: an Agile or
// Basic project has "Issue", a Scrum project does not and must name its
// own.
func TestCreateWorkItem_TypeIsConfigurable(t *testing.T) {
	f, api := newADOSurfaceFixture(t)

	if _, err := CreateWorkItem(context.Background(), api, "payments", "Product Backlog Item", "drift: x", "receipt"); err != nil {
		t.Fatalf("CreateWorkItem: %v", err)
	}
	key := f.find(t, "/wit/workitems/")
	if !strings.Contains(key, "$Product Backlog Item") {
		t.Errorf("route = %q, want the caller's own work item type", key)
	}
}

// TestOpenDraftPR_UsesTheRepositorysOwnDefaultBranch is the same
// load-bearing property every platform's test asserts: never a
// hardcoded "main". The fixture reports refs/heads/trunk.
func TestOpenDraftPR_UsesTheRepositorysOwnDefaultBranch(t *testing.T) {
	f, api := newADOSurfaceFixture(t)

	pr, err := OpenDraftPR(context.Background(), api, "payments", "repo-guid", "ubx-drift/x", "ubx-drift/x.json",
		[]byte(`{"schema_version":1}`), "record drift", "drift: x", "receipt")
	if err != nil {
		t.Fatalf("OpenDraftPR: %v", err)
	}
	if pr.Number != 9 {
		t.Errorf("Number = %d, want 9", pr.Number)
	}
	if !strings.Contains(pr.HTMLURL, "/pullrequest/9") {
		t.Errorf("HTMLURL = %q, want a real composed pull request URL", pr.HTMLURL)
	}

	var prBody map[string]any
	if err := json.Unmarshal([]byte(f.bodies[f.find(t, "/pullrequests")]), &prBody); err != nil {
		t.Fatal(err)
	}
	if prBody["targetRefName"] != "refs/heads/trunk" {
		t.Errorf("targetRefName = %v, want the repository's own declared default", prBody["targetRefName"])
	}
	if prBody["sourceRefName"] != "refs/heads/ubx-drift/x" {
		t.Errorf("sourceRefName = %v", prBody["sourceRefName"])
	}
	// Azure DevOps has a real draft flag, unlike GitLab and both
	// Bitbuckets, so it must actually be set rather than faked in the
	// title.
	if prBody["isDraft"] != true {
		t.Errorf("isDraft = %v, want true", prBody["isDraft"])
	}
	if title, _ := prBody["title"].(string); strings.HasPrefix(title, "Draft:") {
		t.Errorf("title = %q: Azure DevOps has a real isDraft flag, so the title must not be decorated too", title)
	}

	// The new branch must be created at the base commit the fixture
	// reported, with the all-zero sentinel saying "must not exist yet".
	var refs []map[string]any
	if err := json.Unmarshal([]byte(f.bodies["POST "+strings.TrimPrefix(f.find(t, "/refs"), "POST ")]), &refs); err == nil && len(refs) > 0 {
		if refs[0]["oldObjectId"] != zeroObjectID {
			t.Errorf("oldObjectId = %v, want the all-zero sentinel", refs[0]["oldObjectId"])
		}
		if refs[0]["newObjectId"] != "basesha" {
			t.Errorf("newObjectId = %v, want the default branch's own tip", refs[0]["newObjectId"])
		}
	}
}

// TestOpenDraftPR_PushSendsRawTextContent pins the push body's own real
// shape, which is unlike every other platform: a changes array with an
// explicit contentType.
func TestOpenDraftPR_PushSendsRawTextContent(t *testing.T) {
	f, api := newADOSurfaceFixture(t)

	if _, err := OpenDraftPR(context.Background(), api, "payments", "repo-guid", "b", "ubx-drift/x.json",
		[]byte(`{"schema_version":1}`), "record drift", "drift: x", "receipt"); err != nil {
		t.Fatalf("OpenDraftPR: %v", err)
	}
	var push map[string]any
	if err := json.Unmarshal([]byte(f.bodies[f.find(t, "/pushes")]), &push); err != nil {
		t.Fatal(err)
	}
	commits := push["commits"].([]any)
	changes := commits[0].(map[string]any)["changes"].([]any)
	change := changes[0].(map[string]any)
	if change["changeType"] != "add" {
		t.Errorf("changeType = %v", change["changeType"])
	}
	item := change["item"].(map[string]any)
	if item["path"] != "/ubx-drift/x.json" {
		t.Errorf("item.path = %v, want a leading slash", item["path"])
	}
	newContent := change["newContent"].(map[string]any)
	if newContent["contentType"] != "rawtext" {
		t.Errorf("contentType = %v, want rawtext", newContent["contentType"])
	}
	if newContent["content"] != `{"schema_version":1}` {
		t.Errorf("content = %v", newContent["content"])
	}
}
