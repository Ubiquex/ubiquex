package bitbucketcloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// TestListPullRequestChangedFiles_RenameReportsBothSides covers a real
// Bitbucket Cloud shape difference: a diffstat entry carries separate
// old and new file objects, so a rename names two paths. Both are
// returned, because a CODEOWNERS rule or a stack subtree can
// legitimately own either side, and dropping the old path would
// silently under-authorize a real rename.
func TestListPullRequestChangedFiles_RenameReportsBothSides(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/pullrequests/7/diffstat", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"values":[
			{"status":"renamed","old":{"path":"stacks/old/main.tf"},"new":{"path":"stacks/new/main.tf"}},
			{"status":"added","old":null,"new":{"path":"proposals/db.json"}},
			{"status":"removed","old":{"path":"gone.tf"},"new":null}
		]}`)
	})
	api := newTestClient(t, mux)

	got, err := api.ListPullRequestChangedFiles(context.Background(), "acme", "infra", 7)
	if err != nil {
		t.Fatalf("ListPullRequestChangedFiles: %v", err)
	}
	want := map[string]bool{
		"stacks/new/main.tf": true,
		"stacks/old/main.tf": true,
		"proposals/db.json":  true,
		"gone.tf":            true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %d paths covering both sides of the rename", got, len(want))
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected path %q", p)
		}
	}
}

// TestListPullRequestChangedFiles_FollowsRealPagination proves the
// real "next" absolute-URL pagination Bitbucket Cloud uses is followed
// to completion. A single-page implementation would silently see only
// part of a large pull request's changed files, which would
// under-authorize a ship.
func TestListPullRequestChangedFiles_FollowsRealPagination(t *testing.T) {
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/pullrequests/7/diffstat", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"values":[{"status":"added","new":{"path":"second-page.tf"}}]}`)
			return
		}
		fmt.Fprintf(w, `{"next":%q,"values":[{"status":"added","new":{"path":"first-page.tf"}}]}`,
			base+"/repositories/acme/infra/pullrequests/7/diffstat?page=2")
	})
	srvClient := newTestClient(t, mux)
	base = srvClient.baseURL

	got, err := srvClient.ListPullRequestChangedFiles(context.Background(), "acme", "infra", 7)
	if err != nil {
		t.Fatalf("ListPullRequestChangedFiles: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %v, want both pages", got)
	}
}

// TestListPullRequestComments_SkipsDeleted covers Bitbucket Cloud's own
// real behavior of keeping deleted comments in the listing with a
// deleted flag: treating one as this bot's own last comment would edit
// a tombstone instead of posting a fresh comment.
func TestListPullRequestComments_SkipsDeleted(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/pullrequests/7/comments", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"values":[
			{"id":1,"deleted":true,"content":{"raw":""},"user":{"account_id":"bot"}},
			{"id":2,"deleted":false,"content":{"raw":"alive"},"user":{"account_id":"bot"}}
		]}`)
	})
	api := newTestClient(t, mux)

	got, err := api.ListPullRequestComments(context.Background(), "acme", "infra", 7)
	if err != nil {
		t.Fatalf("ListPullRequestComments: %v", err)
	}
	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("got %+v, want only the live comment", got)
	}
}

func TestGetRawFile_MissingFileIsNotAnError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/src/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	api := newTestClient(t, mux)

	body, err := api.GetRawFile(context.Background(), "acme", "infra", "abc123", "CODEOWNERS")
	if err != nil {
		t.Fatalf("GetRawFile: %v", err)
	}
	if body != nil {
		t.Errorf("body = %q, want nil for a missing file", body)
	}
}

func TestHasWriteAccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/workspaces/acme/permissions/repositories/infra", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"values":[
			{"permission":"read","user":{"account_id":"acct-reader"}},
			{"permission":"write","user":{"account_id":"acct-writer"}},
			{"permission":"admin","user":{"account_id":"acct-admin"}}
		]}`)
	})
	api := newTestClient(t, mux)

	cases := []struct {
		accountID string
		want      bool
	}{
		{"acct-writer", true},
		{"acct-admin", true},
		{"acct-reader", false},
		{"acct-stranger", false},
		{"", false},
	}
	for _, c := range cases {
		got, err := api.HasWriteAccess(context.Background(), "acme", "infra", c.accountID)
		if err != nil {
			t.Fatalf("HasWriteAccess(%q): %v", c.accountID, err)
		}
		if got != c.want {
			t.Errorf("HasWriteAccess(%q) = %v, want %v", c.accountID, got, c.want)
		}
	}
}

// TestResolveAccountID is UBI-170's own core identity decision under
// test: a CODEOWNERS file names people in a human-readable form, and
// Bitbucket Cloud has no stable human-readable identifier, so each
// entry is resolved to a real account_id and both failure modes are
// refusals rather than guesses.
func TestResolveAccountID(t *testing.T) {
	members := []Account{
		{AccountID: "acct-jdoe", Nickname: "jdoe", DisplayName: "Jane Doe"},
		{AccountID: "acct-rroe", Nickname: "rroe", DisplayName: "Robin Roe"},
		{AccountID: "acct-twin-a", Nickname: "twin-a", DisplayName: "Sam Twin"},
		{AccountID: "acct-twin-b", Nickname: "twin-b", DisplayName: "Sam Twin"},
		{AccountID: "acct-nonick", DisplayName: "No Nickname"},
	}

	t.Run("nickname resolves", func(t *testing.T) {
		got, err := ResolveAccountID(members, "jdoe")
		if err != nil || got != "acct-jdoe" {
			t.Fatalf("got %q, %v; want acct-jdoe", got, err)
		}
	})
	t.Run("leading @ is accepted", func(t *testing.T) {
		got, err := ResolveAccountID(members, "@rroe")
		if err != nil || got != "acct-rroe" {
			t.Fatalf("got %q, %v; want acct-rroe", got, err)
		}
	})
	t.Run("match is case-insensitive", func(t *testing.T) {
		got, err := ResolveAccountID(members, "JDoe")
		if err != nil || got != "acct-jdoe" {
			t.Fatalf("got %q, %v; want acct-jdoe", got, err)
		}
	})
	t.Run("display name resolves only when no nickname matched", func(t *testing.T) {
		got, err := ResolveAccountID(members, "No Nickname")
		if err != nil || got != "acct-nonick" {
			t.Fatalf("got %q, %v; want acct-nonick", got, err)
		}
	})
	t.Run("ambiguous display name is refused, never guessed", func(t *testing.T) {
		_, err := ResolveAccountID(members, "Sam Twin")
		if !errors.Is(err, ErrAccountAmbiguous) {
			t.Fatalf("err = %v, want ErrAccountAmbiguous", err)
		}
	})
	t.Run("unknown entry is refused", func(t *testing.T) {
		_, err := ResolveAccountID(members, "nobody")
		if !errors.Is(err, ErrAccountNotResolved) {
			t.Fatalf("err = %v, want ErrAccountNotResolved", err)
		}
	})
	t.Run("empty entry is refused", func(t *testing.T) {
		if _, err := ResolveAccountID(members, "@"); !errors.Is(err, ErrAccountNotResolved) {
			t.Fatalf("err = %v, want ErrAccountNotResolved", err)
		}
	})
}

// TestResetApprovalsOnChange is the real TOCTOU primitive under test.
// Bitbucket Cloud follows the GitLab/Azure DevOps branch-level pattern,
// NOT Bitbucket Server's per-approval commit reference, so this checks
// that both real reset kinds count, that the branch scope is honored,
// and that a branching_model restriction is deliberately NOT treated as
// applying (ubx cannot know which concrete branches it covers, and
// guessing permissively would weaken the very gate this enforces).
func TestResetApprovalsOnChange(t *testing.T) {
	cases := []struct {
		name   string
		values string
		branch string
		want   bool
	}{
		{
			name:   "reset_pullrequest_approvals_on_change on an exact branch",
			values: `{"kind":"reset_pullrequest_approvals_on_change","branch_match_kind":"glob","pattern":"main"}`,
			branch: "main",
			want:   true,
		},
		{
			name:   "smart_reset_pullrequest_approvals also counts",
			values: `{"kind":"smart_reset_pullrequest_approvals","branch_match_kind":"glob","pattern":"main"}`,
			branch: "main",
			want:   true,
		},
		{
			name:   "a glob wildcard spans slashes, as Bitbucket Cloud's own patterns do",
			values: `{"kind":"reset_pullrequest_approvals_on_change","branch_match_kind":"glob","pattern":"release/*"}`,
			branch: "release/1.2/hotfix",
			want:   true,
		},
		{
			name:   "a restriction scoped to another branch does not apply",
			values: `{"kind":"reset_pullrequest_approvals_on_change","branch_match_kind":"glob","pattern":"develop"}`,
			branch: "main",
			want:   false,
		},
		{
			name:   "an unrelated restriction kind does not apply",
			values: `{"kind":"require_approvals_to_merge","branch_match_kind":"glob","pattern":"main"}`,
			branch: "main",
			want:   false,
		},
		{
			name:   "a branching_model restriction is deliberately not treated as applying",
			values: `{"kind":"reset_pullrequest_approvals_on_change","branch_match_kind":"branching_model","branch_type":"production","pattern":""}`,
			branch: "main",
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/repositories/acme/infra/branch-restrictions", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"values":[%s]}`, c.values)
			})
			api := newTestClient(t, mux)

			got, err := api.ResetApprovalsOnChange(context.Background(), "acme", "infra", c.branch)
			if err != nil {
				t.Fatalf("ResetApprovalsOnChange: %v", err)
			}
			if got != c.want {
				t.Errorf("ResetApprovalsOnChange(%q) = %v, want %v", c.branch, got, c.want)
			}
		})
	}
}

func TestResetApprovalsOnChange_NoRestrictionsAtAll(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/branch-restrictions", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"values":[]}`)
	})
	api := newTestClient(t, mux)

	got, err := api.ResetApprovalsOnChange(context.Background(), "acme", "infra", "main")
	if err != nil {
		t.Fatalf("ResetApprovalsOnChange: %v", err)
	}
	if got {
		t.Error("expected no reset restriction to report false, so acceptance is refused rather than derived")
	}
}
