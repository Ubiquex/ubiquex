package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateIssue(t *testing.T) {
	var gotTitle, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/infra/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		var req struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		gotTitle, gotBody = req.Title, req.Body
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":   7,
			"html_url": "https://github.com/acme/infra/issues/7",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := New("", WithBaseURL(srv.URL+"/"))
	issue, err := CreateIssue(context.Background(), client, "acme", "infra", "drift detected", "receipt body")
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if issue.Number != 7 {
		t.Errorf("Number = %d, want 7", issue.Number)
	}
	if issue.HTMLURL != "https://github.com/acme/infra/issues/7" {
		t.Errorf("HTMLURL = %q", issue.HTMLURL)
	}
	if gotTitle != "drift detected" || gotBody != "receipt body" {
		t.Errorf("request title/body = %q/%q, want %q/%q", gotTitle, gotBody, "drift detected", "receipt body")
	}
}
