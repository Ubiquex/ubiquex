package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient is the real construction path every test in this package
// uses, with the error New now returns handled once rather than at every
// call site.
func newTestClient(t *testing.T, token string, opts ...Option) *Client {
	t.Helper()
	c, err := New(token, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestWithEnterpriseBaseURL_AppliesGHESAPIPath is UBI-171's own
// load-bearing case: a real GitHub Enterprise Server instance URL, with
// no path spelled out, must produce requests under that instance's own
// /api/v3/ root -- not at the instance root, which is what the previous
// raw WithBaseURL wiring produced and which 404s against every real GHES
// deployment.
//
// Adversarial in the way that matters: the fake server refuses anything
// outside /api/v3/, so a client that regressed to the instance root
// fails loudly instead of quietly passing on a lenient mux.
func TestWithEnterpriseBaseURL_AppliesGHESAPIPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if !strings.HasPrefix(r.URL.Path, "/api/v3/") {
			http.Error(w, "not a GHES API path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 7, "body": "ubx-proposal: abc"})
	}))
	defer srv.Close()

	client := newTestClient(t, "tok", WithEnterpriseBaseURL(srv.URL))

	_, _, err := client.api.PullRequests.Get(context.Background(), "acme", "infra", 7)
	if err != nil {
		t.Fatalf("PullRequests.Get against a GHES-style base URL: %v", err)
	}
	if want := "/api/v3/repos/acme/infra/pulls/7"; gotPath != want {
		t.Errorf("request path = %q, want %q", gotPath, want)
	}
}

// TestWithEnterpriseBaseURL_RespectsExplicitAPIPath covers the operator
// who already knows the convention and types it: appending a second
// /api/v3 would break just as badly as omitting the first.
func TestWithEnterpriseBaseURL_RespectsExplicitAPIPath(t *testing.T) {
	client := newTestClient(t, "tok", WithEnterpriseBaseURL("https://github.example.com/api/v3/"))
	if got := client.api.BaseURL.String(); got != "https://github.example.com/api/v3/" {
		t.Errorf("BaseURL = %q, want the operator's own spelled-out path unchanged", got)
	}
}

// TestWithEnterpriseBaseURL_TrailingSlashOptional: go-github's own
// NewRequest errors when BaseURL has no trailing slash, so an operator
// who omits it must still get a working client rather than a failure on
// the first real call.
func TestWithEnterpriseBaseURL_TrailingSlashOptional(t *testing.T) {
	client := newTestClient(t, "tok", WithEnterpriseBaseURL("https://github.example.com"))
	if got := client.api.BaseURL.String(); got != "https://github.example.com/api/v3/" {
		t.Errorf("BaseURL = %q, want the GHES API root with a trailing slash", got)
	}
}

// TestWithEnterpriseBaseURL_Malformed proves the configuration is
// validated at construction time (gitlab.New's own shape) rather than
// surfacing later as a mysterious request error.
func TestWithEnterpriseBaseURL_Malformed(t *testing.T) {
	if _, err := New("tok", WithEnterpriseBaseURL("://not a url")); err == nil {
		t.Fatal("expected an error for a malformed enterprise base URL")
	}
}

// TestWithBaseURL_UsedVerbatim keeps the test/env seam honest: the exact
// form must not acquire an /api/v3/ suffix, or every httptest-backed test
// in this package would break.
func TestWithBaseURL_UsedVerbatim(t *testing.T) {
	client := newTestClient(t, "", WithBaseURL("https://example.test/"))
	if got := client.api.BaseURL.String(); got != "https://example.test/" {
		t.Errorf("BaseURL = %q, want it used verbatim", got)
	}
}
