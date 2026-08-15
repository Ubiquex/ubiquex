package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// UBI-165: surfaceDrift used to be GitHub-only. These prove the real
// dispatch across all five platforms, each against a real httptest
// server standing in for that platform's own API, and each asserting
// the request actually landed on that platform's own real endpoint.
//
// Deliberately end-to-end through surfaceDrift rather than through each
// platform package directly: the per-package tests already cover request
// shapes, and what is untested without this is the routing itself, which
// is exactly where a five-way switch goes wrong.

func surfaceTestProposal() *core.Proposal {
	return &core.Proposal{
		SchemaVersion: 1,
		Kind:          core.KindDriftAdopt,
		Stack:         "payments",
		Intent:        core.Intent{Summary: "adopt observed drift"},
		BlastRadius:   core.BlastRadius{Modifies: 1},
	}
}

type surfaceRecorder struct {
	mu    sync.Mutex
	paths []string
}

func (r *surfaceRecorder) record(p string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, p)
}

func (r *surfaceRecorder) hit(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.paths {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

// newSurfaceServer answers every endpoint any of the five platforms
// touches, so one fixture can serve whichever one the dispatch picks.
func newSurfaceServer(t *testing.T, rec *surfaceRecorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		// GitHub
		case strings.HasSuffix(p, "/issues") && strings.Contains(p, "/repos/"):
			_, _ = w.Write([]byte(`{"number":7,"html_url":"https://github.example.com/acme/infra/issues/7"}`))
		// GitLab
		case strings.HasSuffix(p, "/issues"):
			_, _ = w.Write([]byte(`{"id":101,"iid":7,"web_url":"https://gitlab.example.com/acme/infra/-/issues/7"}`))
		// Azure DevOps work item
		case strings.Contains(p, "/wit/workitems/"):
			_, _ = w.Write([]byte(`{"id":42,"_links":{"html":{"href":"https://dev.azure.example.com/_workitems/edit/42"}}}`))
		// Bitbucket Cloud issue
		case strings.Contains(p, "/repositories/") && strings.HasSuffix(p, "/issues"):
			_, _ = w.Write([]byte(`{"id":7,"links":{"html":{"href":"https://bitbucket.org/acme/infra/issues/7"}}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSurfaceDrift_DispatchesToEveryPlatformsOwnIssueEndpoint(t *testing.T) {
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "w"}

	tests := []struct {
		name     string
		target   func(base string) surfaceTarget
		wantPath string
		wantOut  string
	}{
		{
			name: "github",
			target: func(base string) surfaceTarget {
				return surfaceTarget{githubRepo: "acme/infra", githubAPIBaseURL: base}
			},
			wantPath: "/api/v3/repos/acme/infra/issues",
			wantOut:  "opened issue",
		},
		{
			name: "gitlab",
			target: func(base string) surfaceTarget {
				return surfaceTarget{gitlabProject: "acme/infra", gitlabAPIBaseURL: base}
			},
			wantPath: "/api/v4/projects/acme/infra/issues",
			wantOut:  "opened issue",
		},
		{
			name: "azuredevops",
			target: func(base string) surfaceTarget {
				return surfaceTarget{azureDevOpsProject: "acme/payments/infra", azureDevOpsAPIBaseURL: base}
			},
			// The literal "$" before the type is Azure DevOps' own real
			// route convention, and "Issue" is the default work item type.
			wantPath: "/payments/_apis/wit/workitems/$Issue",
			wantOut:  "opened work item",
		},
		{
			name: "bitbucketcloud",
			target: func(base string) surfaceTarget {
				t.Setenv("UBX_BITBUCKET_CLOUD_API_BASE_URL", base)
				return surfaceTarget{bitbucketCloudRepo: "acme/infra"}
			},
			wantPath: "/repositories/acme/infra/issues",
			wantOut:  "opened issue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &surfaceRecorder{}
			srv := newSurfaceServer(t, rec)
			var out bytes.Buffer

			if err := surfaceDrift(context.Background(), &out, surfaceTestProposal(), addr, "issue", tt.target(srv.URL), ""); err != nil {
				t.Fatalf("surfaceDrift: %v", err)
			}
			if !rec.hit(tt.wantPath) {
				t.Errorf("no request to %q; recorded: %v", tt.wantPath, rec.paths)
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("output = %q, want it to report %q", out.String(), tt.wantOut)
			}
		})
	}
}

// TestSurfaceDrift_BitbucketServerIssueRefused is the one platform that
// genuinely cannot honor --surface-as issue, and the refusal has to
// happen before any request goes out: silently opening a pull request
// instead would commit a file to somebody's repository when they asked
// for an issue.
func TestSurfaceDrift_BitbucketServerIssueRefused(t *testing.T) {
	rec := &surfaceRecorder{}
	srv := newSurfaceServer(t, rec)
	var out bytes.Buffer

	err := surfaceDrift(context.Background(), &out, surfaceTestProposal(),
		core.Address{Stack: "payments", Type: "fake_widget", Name: "w"}, "issue",
		surfaceTarget{bitbucketServerURL: srv.URL, bitbucketServerProject: "INFRA/infra"}, "")
	if err == nil {
		t.Fatal("expected --surface-as issue to be refused for Bitbucket Server")
	}
	for _, want := range []string{"no issue tracker", "--surface-as pr"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	rec.mu.Lock()
	n := len(rec.paths)
	rec.mu.Unlock()
	if n != 0 {
		t.Errorf("made %d request(s) before refusing (%v); the refusal must be decided locally", n, rec.paths)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q, want nothing reported on a refusal", out.String())
	}
}

func TestSurfaceDrift_RejectsAmbiguousAndMissingTargets(t *testing.T) {
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "w"}
	var out bytes.Buffer

	t.Run("none", func(t *testing.T) {
		err := surfaceDrift(context.Background(), &out, surfaceTestProposal(), addr, "issue", surfaceTarget{}, "")
		if err == nil || !strings.Contains(err.Error(), "exactly one of --github-repo") {
			t.Fatalf("got %v, want a teaching error listing every platform flag", err)
		}
	})

	t.Run("two", func(t *testing.T) {
		err := surfaceDrift(context.Background(), &out, surfaceTestProposal(), addr, "issue",
			surfaceTarget{githubRepo: "acme/infra", bitbucketCloudRepo: "acme/infra"}, "")
		if err == nil || !strings.Contains(err.Error(), "exactly one platform") {
			t.Fatalf("got %v, want the one-platform rule", err)
		}
	})

	t.Run("bad surface-as", func(t *testing.T) {
		err := surfaceDrift(context.Background(), &out, surfaceTestProposal(), addr, "wiki",
			surfaceTarget{githubRepo: "acme/infra"}, "")
		if err == nil || !strings.Contains(err.Error(), `must be "issue" or "pr"`) {
			t.Fatalf("got %v", err)
		}
	})
}
