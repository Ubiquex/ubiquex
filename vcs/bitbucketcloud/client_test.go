package bitbucketcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestClient wires a Client at a real httptest.Server. Every test in
// this package drives a real HTTP round trip against a real fixture
// server: nothing is mocked at the transport level, matching the bar
// every other platform package in this project already holds.
func newTestClient(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New("test-token", WithBaseURL(srv.URL))
}

// TestDo_SendsBearerAuthorization proves the real, documented Bitbucket
// Cloud auth scheme is what actually goes on the wire. Atlassian's own
// current documentation states app passwords are deprecated and that an
// access token is sent as a Bearer token, so this asserts the header
// rather than trusting the constructor.
func TestDo_SendsBearerAuthorization(t *testing.T) {
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/pullrequests/7", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"id":7}`)
	})
	api := newTestClient(t, mux)

	if _, err := api.GetPullRequest(context.Background(), "acme", "infra", 7); err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-token")
	}
}

// TestPullRequestForCommit_RepoNotIndexedRefused covers a real,
// Bitbucket-Cloud-specific condition no other platform has: the
// commit-to-pull-request response carries its own repo_indexed flag,
// and an unindexed repository returns an empty values array that
// genuinely means "not known yet", not "no pull request". Treating it
// as no-PR would silently skip a real acceptance.
func TestPullRequestForCommit_RepoNotIndexedRefused(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/commit/abc123/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"repo_indexed":false,"values":[]}`)
	})
	api := newTestClient(t, mux)

	_, err := api.pullRequestForCommit(context.Background(), "acme", "infra", "abc123")
	if !errors.Is(err, ErrRepoNotIndexed) {
		t.Fatalf("err = %v, want ErrRepoNotIndexed", err)
	}
}

func TestPullRequestForCommit_NoPullRequestForIndexedRepo(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/commit/abc123/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"repo_indexed":true,"values":[]}`)
	})
	api := newTestClient(t, mux)

	_, err := api.pullRequestForCommit(context.Background(), "acme", "infra", "abc123")
	if !errors.Is(err, ErrNoPullRequestForCommit) {
		t.Fatalf("err = %v, want ErrNoPullRequestForCommit", err)
	}
}

// TestPullRequestForCommit_MinimalRepresentationNeedsSecondFetch pins
// the real two-call shape confirmed live: the commit lookup returns
// only id/title/links, with no description and no participants, so the
// full representation must be fetched separately. A fixture that
// returned everything in one call would let a regression to a
// single-call implementation pass.
func TestPullRequestForCommit_MinimalRepresentationNeedsSecondFetch(t *testing.T) {
	var commitCalls, prCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/commit/abc123/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		commitCalls++
		// Exactly the minimal shape the live API returns.
		fmt.Fprint(w, `{"repo_indexed":true,"values":[{"type":"pullrequest","id":900,"title":"release","draft":false,"queued":false}]}`)
	})
	mux.HandleFunc("/repositories/acme/infra/pullrequests/900", func(w http.ResponseWriter, r *http.Request) {
		prCalls++
		fmt.Fprint(w, `{"id":900,"description":"ubx-proposal: abc","participants":[{"user":{"account_id":"acct-1"},"approved":true,"state":"approved"}]}`)
	})
	api := newTestClient(t, mux)

	id, err := api.pullRequestForCommit(context.Background(), "acme", "infra", "abc123")
	if err != nil {
		t.Fatalf("pullRequestForCommit: %v", err)
	}
	if id != 900 {
		t.Fatalf("id = %d, want 900", id)
	}
	pr, err := api.GetPullRequest(context.Background(), "acme", "infra", id)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	if pr.Description == "" || len(pr.Participants) != 1 {
		t.Errorf("full representation missing description/participants: %+v", pr)
	}
	if commitCalls != 1 || prCalls != 1 {
		t.Errorf("calls = commit:%d pr:%d, want exactly one of each", commitCalls, prCalls)
	}
}

// TestApprovingReviewers_ReturnsAccountIDsNotLabels is the real
// identity property (UBI-170): Bitbucket Cloud accounts have no
// username, and nickname/display_name are user-changeable, so an
// approver recorded in an append-only ledger must be an account_id.
func TestApprovingReviewers_ReturnsAccountIDsNotLabels(t *testing.T) {
	var pr PullRequest
	body := `{"participants":[
		{"user":{"account_id":"acct-approver","nickname":"jdoe","display_name":"J Doe"},"approved":true,"state":"approved"},
		{"user":{"account_id":"acct-reviewer","nickname":"rroe","display_name":"R Roe"},"approved":false,"state":null},
		{"user":{"account_id":"acct-changes","nickname":"kkay"},"approved":false,"state":"changes_requested"}
	]}`
	if err := json.Unmarshal([]byte(body), &pr); err != nil {
		t.Fatal(err)
	}

	got := ApprovingReviewers(&pr)
	if len(got) != 1 || got[0] != "acct-approver" {
		t.Fatalf("ApprovingReviewers = %v, want exactly [acct-approver]", got)
	}
	for _, g := range got {
		if g == "jdoe" || g == "J Doe" {
			t.Errorf("approver recorded by a mutable label %q, must be an account_id", g)
		}
	}
}

// TestApprovingReviewers_UnidentifiableParticipantSkipped covers a
// participant Bitbucket Cloud returned with neither account_id nor
// uuid: unidentifiable, so it must never be recorded as an approver.
func TestApprovingReviewers_UnidentifiableParticipantSkipped(t *testing.T) {
	var pr PullRequest
	if err := json.Unmarshal([]byte(`{"participants":[{"user":{"display_name":"Ghost"},"approved":true}]}`), &pr); err != nil {
		t.Fatal(err)
	}
	if got := ApprovingReviewers(&pr); len(got) != 0 {
		t.Errorf("ApprovingReviewers = %v, want empty for an unidentifiable participant", got)
	}
}

// TestAccountIdentity_PrefersAccountIDFallsBackToUUID pins the one
// stable identifier's own resolution order.
func TestAccountIdentity_PrefersAccountIDFallsBackToUUID(t *testing.T) {
	cases := []struct {
		a    Account
		want string
	}{
		{Account{AccountID: "acct-1", UUID: "{u}"}, "acct-1"},
		{Account{UUID: "{u}"}, "{u}"},
		{Account{Nickname: "jdoe", DisplayName: "J Doe"}, ""},
	}
	for _, c := range cases {
		if got := c.a.Identity(); got != c.want {
			t.Errorf("Identity(%+v) = %q, want %q", c.a, got, c.want)
		}
	}
}
