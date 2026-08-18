package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	bbcloud "github.com/ubiquex/ubiquex/bitbucketcloud"
)

// bbcFlowFixture is a real httptest server covering every endpoint a
// Bitbucket Cloud event handler touches, plus a record of the comments
// the server actually posted. Real HTTP round trips throughout.
type bbcFlowFixture struct {
	mu                 sync.Mutex
	posted             []string
	branchRestrictions string
	codeowners         string
}

func newBBCFlowFixture(t *testing.T, branchRestrictions, codeowners string) (*bbcFlowFixture, *bbcloud.Client) {
	t.Helper()
	f := &bbcFlowFixture{branchRestrictions: branchRestrictions, codeowners: codeowners}
	mux := http.NewServeMux()

	mux.HandleFunc("/repositories/acme/infra/pullrequests/7", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":7,"description":"ubx-proposal: `+strings.Repeat("a", 64)+`",
			"author":{"account_id":"acct-author","nickname":"author"},
			"source":{"branch":{"name":"feature"},"commit":{"hash":"headsha"}},
			"destination":{"branch":{"name":"main"}},
			"participants":[{"user":{"account_id":"acct-approver"},"approved":true,"state":"approved"}]}`)
	})
	mux.HandleFunc("/repositories/acme/infra/branch-restrictions", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"values":[%s]}`, f.branchRestrictions)
	})
	mux.HandleFunc("/repositories/acme/infra/pullrequests/7/diffstat", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"values":[{"status":"added","new":{"path":"proposals/db.json"}}]}`)
	})
	mux.HandleFunc("/repositories/acme/infra/pullrequests/7/comments", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			buf := make([]byte, r.ContentLength)
			r.Body.Read(buf)
			f.mu.Lock()
			f.posted = append(f.posted, string(buf))
			f.mu.Unlock()
			fmt.Fprint(w, `{"id":1}`)
			return
		}
		fmt.Fprint(w, `{"values":[]}`)
	})
	mux.HandleFunc("/repositories/acme/infra/src/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/CODEOWNERS") && !strings.Contains(r.URL.Path, ".bitbucket") && !strings.Contains(r.URL.Path, "docs") {
			fmt.Fprint(w, f.codeowners)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/workspaces/acme/members", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"values":[{"user":{"account_id":"acct-owner","nickname":"owner"}}]}`)
	})
	mux.HandleFunc("/workspaces/acme/permissions/repositories/infra", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"values":[]}`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, bbcloud.New("test-token", bbcloud.WithBaseURL(srv.URL))
}

func (f *bbcFlowFixture) postedBodies() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.posted, "\n")
}

func newBBCServer(t *testing.T, api *bbcloud.Client) (*Server, string) {
	t.Helper()
	workDir := t.TempDir()
	return &Server{
		cfg: &Config{
			WorkDir:                    workDir,
			ShipOnMerge:                true,
			BitbucketCloudToken:        "test-token",
			BitbucketCloudBotAccountID: "acct-bot",
			Repos: []RepoConfig{
				{Platform: "bitbucketcloud", Project: "acme", Repository: "infra"},
			},
		},
		bitbucketcloud: api,
	}, workDir
}

func workDirIsEmpty(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries) == 0
}

// TestBitbucketCloudApproval_RefusedWithoutResetRestriction is the real
// TOCTOU gate under test, and it is deliberately the GitLab/Azure
// DevOps branch-restriction pattern rather than Bitbucket Server's.
//
// A Bitbucket Cloud participant object carries NO commit reference at
// all, so there is nothing to compare against the head the way
// Bitbucket Server's own lastReviewedCommit allows. With no branch
// restriction that resets approvals when changes land, a recorded
// approval cannot be correlated with any particular commit, so
// acceptance must be refused outright with a real, explanatory comment.
func TestBitbucketCloudApproval_RefusedWithoutResetRestriction(t *testing.T) {
	f, api := newBBCFlowFixture(t, "", "")
	s, workDir := newBBCServer(t, api)

	if err := s.handlePullRequestApprovedBitbucketCloud(context.Background(),
		mustBBCEvent(t, `{"actor":{"account_id":"acct-approver"},"repository":{"full_name":"acme/infra"},"pullrequest":{"id":7}}`)); err != nil {
		t.Fatalf("handlePullRequestApprovedBitbucketCloud: %v", err)
	}

	posted := f.postedBodies()
	if !strings.Contains(posted, "branch restriction") {
		t.Errorf("posted = %q, want a real refusal explaining the missing reset-approvals branch restriction", posted)
	}
	if !strings.Contains(posted, "carries no commit reference") {
		t.Errorf("posted = %q, want the refusal to state why correlation is impossible", posted)
	}
	if !workDirIsEmpty(t, workDir) {
		t.Error("expected nothing to be cloned when the TOCTOU gate refuses before any checkout")
	}
}

// TestBitbucketCloudApproval_ProceedsPastGateWithResetRestriction
// proves the gate opens for a repository that genuinely is configured
// to reset approvals on change, using both real restriction kinds.
func TestBitbucketCloudApproval_ProceedsPastGateWithResetRestriction(t *testing.T) {
	for _, kind := range []string{"reset_pullrequest_approvals_on_change", "smart_reset_pullrequest_approvals"} {
		t.Run(kind, func(t *testing.T) {
			restriction := fmt.Sprintf(`{"kind":%q,"branch_match_kind":"glob","pattern":"main"}`, kind)
			f, api := newBBCFlowFixture(t, restriction, "")
			s, _ := newBBCServer(t, api)

			// The clone itself cannot succeed in a hermetic test, so
			// this asserts only that the TOCTOU refusal is NOT what
			// happened: the handler got past the gate and on to the
			// real checkout.
			_ = s.handlePullRequestApprovedBitbucketCloud(context.Background(),
				mustBBCEvent(t, `{"actor":{"account_id":"acct-approver"},"repository":{"full_name":"acme/infra"},"pullrequest":{"id":7}}`))

			if posted := f.postedBodies(); strings.Contains(posted, "branch restriction") {
				t.Errorf("posted = %q, want the TOCTOU gate to pass for a %s restriction", posted, kind)
			}
		})
	}
}

// TestBitbucketCloudComment_UnauthorizedShipNeverClones is UBI-168's
// own finding, applied to this platform from the start rather than
// inherited as a defect.
//
// On the four older platforms the stack resolution (and therefore the
// clone/fetch) happens before the authorization check, so an
// unauthorized commenter on an allowlisted repository can still make
// the server do real work. Here the authorization check runs first, so
// an unauthorized `ubx ship` comment must produce a refusal comment and
// leave the work directory completely untouched.
func TestBitbucketCloudComment_UnauthorizedShipNeverClones(t *testing.T) {
	f, api := newBBCFlowFixture(t, "", "proposals/ @owner\n")
	s, workDir := newBBCServer(t, api)

	err := s.handlePullRequestCommentBitbucketCloud(context.Background(),
		mustBBCEvent(t, `{"actor":{"account_id":"acct-stranger","nickname":"stranger"},"repository":{"full_name":"acme/infra"},"pullrequest":{"id":7},"comment":{"content":{"raw":"ubx ship"}}}`))
	if err != nil {
		t.Fatalf("handlePullRequestCommentBitbucketCloud: %v", err)
	}

	if posted := f.postedBodies(); !strings.Contains(posted, "isn't a CODEOWNERS-listed owner") {
		t.Errorf("posted = %q, want a real not-authorized refusal", posted)
	}
	if !workDirIsEmpty(t, workDir) {
		t.Error("UBI-168: an unauthorized ship comment must never trigger a clone or fetch")
	}
}

// TestBitbucketCloudComment_UnauthorizedReplanNeverClones is the same
// ordering property for the re-plan verb.
func TestBitbucketCloudComment_UnauthorizedReplanNeverClones(t *testing.T) {
	f, api := newBBCFlowFixture(t, "", "")
	s, workDir := newBBCServer(t, api)

	err := s.handlePullRequestCommentBitbucketCloud(context.Background(),
		mustBBCEvent(t, `{"actor":{"account_id":"acct-stranger","nickname":"stranger"},"repository":{"full_name":"acme/infra"},"pullrequest":{"id":7},"comment":{"content":{"raw":"ubx plan"}}}`))
	if err != nil {
		t.Fatalf("handlePullRequestCommentBitbucketCloud: %v", err)
	}

	if posted := f.postedBodies(); !strings.Contains(posted, "isn't authorized to re-run") {
		t.Errorf("posted = %q, want a real not-authorized refusal", posted)
	}
	if !workDirIsEmpty(t, workDir) {
		t.Error("UBI-168: an unauthorized re-plan comment must never trigger a clone or fetch")
	}
}

// TestBitbucketCloudComment_NonCommandIgnored covers ordinary human
// conversation on a pull request: nothing runs, nothing is posted, and
// nothing is cloned.
func TestBitbucketCloudComment_NonCommandIgnored(t *testing.T) {
	f, api := newBBCFlowFixture(t, "", "")
	s, workDir := newBBCServer(t, api)

	for _, text := range []string{"looks good to me", "ubx", "ubxship", ""} {
		payload := fmt.Sprintf(`{"actor":{"account_id":"acct-human"},"repository":{"full_name":"acme/infra"},"pullrequest":{"id":7},"comment":{"content":{"raw":%q}}}`, text)
		if err := s.handlePullRequestCommentBitbucketCloud(context.Background(), mustBBCEvent(t, payload)); err != nil {
			t.Fatalf("comment %q: %v", text, err)
		}
	}
	if posted := f.postedBodies(); posted != "" {
		t.Errorf("posted = %q, want nothing for ordinary comments", posted)
	}
	if !workDirIsEmpty(t, workDir) {
		t.Error("an ordinary comment must never trigger a clone")
	}
}

// TestBitbucketCloudMerge_ShipOnMergeOffDoesNothing covers the real
// policy gate: with ship_on_merge off, a merge event must not act.
func TestBitbucketCloudMerge_ShipOnMergeOffDoesNothing(t *testing.T) {
	f, api := newBBCFlowFixture(t, "", "")
	s, workDir := newBBCServer(t, api)
	s.cfg.ShipOnMerge = false

	if err := s.handlePullRequestMergedBitbucketCloud(context.Background(),
		mustBBCEvent(t, `{"repository":{"full_name":"acme/infra"},"pullrequest":{"id":7}}`)); err != nil {
		t.Fatalf("handlePullRequestMergedBitbucketCloud: %v", err)
	}
	if posted := f.postedBodies(); posted != "" {
		t.Errorf("posted = %q, want nothing when ship_on_merge is off", posted)
	}
	if !workDirIsEmpty(t, workDir) {
		t.Error("ship_on_merge off must not trigger a clone")
	}
}

// mustBBCEvent decodes a real webhook payload into the shape the
// handlers take, failing the test on malformed fixture JSON.
func mustBBCEvent(t *testing.T, payload string) *bitbucketCloudEvent {
	t.Helper()
	var e bitbucketCloudEvent
	if err := json.Unmarshal([]byte(payload), &e); err != nil {
		t.Fatalf("fixture payload: %v", err)
	}
	return &e
}
