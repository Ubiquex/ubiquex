package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenDraftPR(t *testing.T) {
	var (
		gotRefBody     map[string]any
		gotFileContent string
		gotFileBranch  string
		gotPRHead      string
		gotPRBase      string
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/infra", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
	})
	mux.HandleFunc("/repos/acme/infra/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ref":    "refs/heads/main",
			"object": map[string]any{"sha": "abc123", "type": "commit"},
		})
	})
	mux.HandleFunc("/repos/acme/infra/git/refs", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotRefBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ref":    gotRefBody["ref"],
			"object": map[string]any{"sha": "abc123", "type": "commit"},
		})
	})
	mux.HandleFunc("/repos/acme/infra/contents/ubx-drift/proposal.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", r.Method)
		}
		var req struct {
			Content []byte `json:"content"`
			Branch  string `json:"branch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		gotFileContent, gotFileBranch = string(req.Content), req.Branch
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": map[string]any{"sha": "def456"},
		})
	})
	mux.HandleFunc("/repos/acme/infra/pulls", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Head string `json:"head"`
			Base string `json:"base"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		gotPRHead, gotPRBase = req.Head, req.Base
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number":   9,
			"html_url": "https://github.com/acme/infra/pull/9",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newTestClient(t, "", WithBaseURL(srv.URL+"/"))
	pr, err := OpenDraftPR(context.Background(), client, "acme", "infra",
		"ubx-drift/prod-aws_instance-web", "ubx-drift/proposal.json",
		[]byte(`{"schema_version":1}`), "record drift", "drift: aws_instance.web", "ubx-proposal: abc\n\nreceipt")
	if err != nil {
		t.Fatalf("OpenDraftPR: %v", err)
	}
	if pr.Number != 9 {
		t.Errorf("Number = %d, want 9", pr.Number)
	}
	if pr.HTMLURL != "https://github.com/acme/infra/pull/9" {
		t.Errorf("HTMLURL = %q", pr.HTMLURL)
	}
	if gotRefBody["ref"] != "refs/heads/ubx-drift/prod-aws_instance-web" {
		t.Errorf("created ref = %v, want refs/heads/ubx-drift/prod-aws_instance-web", gotRefBody["ref"])
	}
	if gotRefBody["sha"] != "abc123" {
		t.Errorf("created ref sha = %v, want abc123 (the default branch's HEAD)", gotRefBody["sha"])
	}
	if gotFileContent != `{"schema_version":1}` {
		t.Errorf("committed content = %q", gotFileContent)
	}
	if gotFileBranch != "ubx-drift/prod-aws_instance-web" {
		t.Errorf("committed to branch %q, want the new branch", gotFileBranch)
	}
	if gotPRHead != "ubx-drift/prod-aws_instance-web" || gotPRBase != "main" {
		t.Errorf("PR head/base = %q/%q, want the new branch / main", gotPRHead, gotPRBase)
	}
}
