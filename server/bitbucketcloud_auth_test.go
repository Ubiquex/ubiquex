package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	bbcloud "github.com/ubiquex/ubiquex/vcs/bitbucketcloud"
)

// bitbucketCloudFixture stands up a real httptest server carrying a
// CODEOWNERS file, a changed-file list, a workspace member roster, and
// a repository permission listing, and returns a client pointed at it.
// A real HTTP round trip on every call, never a transport-level mock.
func bitbucketCloudFixture(t *testing.T, codeowners string, changedFiles []string, members, permissions string) *bbcloud.Client {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/repositories/acme/infra/src/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/CODEOWNERS") && !strings.Contains(r.URL.Path, ".bitbucket") && !strings.Contains(r.URL.Path, "docs") {
			fmt.Fprint(w, codeowners)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/repositories/acme/infra/pullrequests/7/diffstat", func(w http.ResponseWriter, r *http.Request) {
		var vals []string
		for _, f := range changedFiles {
			vals = append(vals, fmt.Sprintf(`{"status":"modified","new":{"path":%q}}`, f))
		}
		fmt.Fprintf(w, `{"values":[%s]}`, strings.Join(vals, ","))
	})
	mux.HandleFunc("/workspaces/acme/members", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"values":[%s]}`, members)
	})
	mux.HandleFunc("/workspaces/acme/permissions/repositories/infra", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"values":[%s]}`, permissions)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return bbcloud.New("test-token", bbcloud.WithBaseURL(srv.URL))
}

const bbcMembers = `
	{"user":{"account_id":"acct-jdoe","nickname":"jdoe","display_name":"Jane Doe"}},
	{"user":{"account_id":"acct-rroe","nickname":"rroe","display_name":"Robin Roe"}},
	{"user":{"account_id":"acct-twin-a","nickname":"twin","display_name":"Sam Twin A"}},
	{"user":{"account_id":"acct-twin-b","nickname":"twin","display_name":"Sam Twin B"}}`

// TestIsAuthorizedToShipBitbucketCloud_ResolvesEntryToAccountID is
// UBI-170's own core authorization decision under test: the CODEOWNERS
// file names a human-readable "@jdoe", the commenter arrives from the
// webhook as an account_id, and the two are connected by resolving the
// entry against live workspace membership rather than by string
// comparison against a mutable label.
func TestIsAuthorizedToShipBitbucketCloud_ResolvesEntryToAccountID(t *testing.T) {
	api := bitbucketCloudFixture(t, "stacks/payments/ @jdoe\n", []string{"stacks/payments/main.tf"}, bbcMembers, "")

	ok, err := isAuthorizedToShipBitbucketCloud(context.Background(), api, "acme", "infra", 7, "abc123",
		bbcloud.Account{AccountID: "acct-jdoe", Nickname: "jdoe"})
	if err != nil {
		t.Fatalf("isAuthorizedToShipBitbucketCloud: %v", err)
	}
	if !ok {
		t.Error("expected the CODEOWNERS entry to resolve to the commenter's own real account_id and authorize")
	}
}

// TestIsAuthorizedToShipBitbucketCloud_RenamedNicknameDoesNotAuthorize
// is the real reason resolution happens at check time. The commenter
// carries the same account_id but a different nickname than CODEOWNERS
// names, and no member now answers to the CODEOWNERS entry, so the
// entry is unresolvable and nobody is authorized by it.
func TestIsAuthorizedToShipBitbucketCloud_RenamedNicknameDoesNotAuthorize(t *testing.T) {
	api := bitbucketCloudFixture(t, "stacks/payments/ @old-nickname\n", []string{"stacks/payments/main.tf"}, bbcMembers, "")

	ok, err := isAuthorizedToShipBitbucketCloud(context.Background(), api, "acme", "infra", 7, "abc123",
		bbcloud.Account{AccountID: "acct-jdoe", Nickname: "jdoe"})
	if ok {
		t.Error("a CODEOWNERS entry that resolves to nobody must never authorize")
	}
	if !errors.Is(err, ErrCodeownersUnresolvable) {
		t.Fatalf("err = %v, want ErrCodeownersUnresolvable so the operator is told their CODEOWNERS is broken", err)
	}
}

// TestIsAuthorizedToShipBitbucketCloud_AmbiguousEntryRefused covers the
// real ambiguity case Bitbucket Cloud genuinely permits: nickname
// uniqueness is NOT guaranteed, so two members can answer to the same
// "@twin" entry, which then cannot resolve to exactly one account and
// is refused rather than resolved to whichever sorted first.
func TestIsAuthorizedToShipBitbucketCloud_AmbiguousEntryRefused(t *testing.T) {
	api := bitbucketCloudFixture(t, "stacks/payments/ @twin\n", []string{"stacks/payments/main.tf"}, bbcMembers, "")

	ok, err := isAuthorizedToShipBitbucketCloud(context.Background(), api, "acme", "infra", 7, "abc123",
		bbcloud.Account{AccountID: "acct-twin-a"})
	if ok {
		t.Error("an ambiguous CODEOWNERS entry must never authorize")
	}
	if !errors.Is(err, ErrCodeownersUnresolvable) {
		t.Fatalf("err = %v, want ErrCodeownersUnresolvable", err)
	}
}

// TestIsAuthorizedToShipBitbucketCloud_ResolvedMatchWinsOverBrokenEntry
// proves one unresolvable entry never revokes a different owner's
// genuine authority on the same rule.
func TestIsAuthorizedToShipBitbucketCloud_ResolvedMatchWinsOverBrokenEntry(t *testing.T) {
	api := bitbucketCloudFixture(t, "stacks/payments/ @ghost @jdoe\n", []string{"stacks/payments/main.tf"}, bbcMembers, "")

	ok, err := isAuthorizedToShipBitbucketCloud(context.Background(), api, "acme", "infra", 7, "abc123",
		bbcloud.Account{AccountID: "acct-jdoe"})
	if err != nil {
		t.Fatalf("isAuthorizedToShipBitbucketCloud: %v", err)
	}
	if !ok {
		t.Error("a real, resolved owner must still authorize even when a sibling entry is broken")
	}
}

// TestIsAuthorizedToShipBitbucketCloud_TeamEntryNeverMatches covers the
// same real, deliberate, documented gap Bitbucket Server has: resolving
// group membership is a separate API this package never needed, so a
// group entry must never be treated as a match, which would authorize
// everyone.
func TestIsAuthorizedToShipBitbucketCloud_TeamEntryNeverMatches(t *testing.T) {
	api := bitbucketCloudFixture(t, "stacks/payments/ @acme/infra-team\n", []string{"stacks/payments/main.tf"}, bbcMembers, "")

	ok, _ := isAuthorizedToShipBitbucketCloud(context.Background(), api, "acme", "infra", 7, "abc123",
		bbcloud.Account{AccountID: "acct-jdoe"})
	if ok {
		t.Error("a CODEOWNERS group entry must never authorize a Bitbucket Cloud ship")
	}
}

func TestIsAuthorizedToShipBitbucketCloud_NoCodeownersAuthorizesNobody(t *testing.T) {
	api := bitbucketCloudFixture(t, "", []string{"stacks/payments/main.tf"}, bbcMembers, "")
	// The fixture answers 404 for every src path when codeowners is
	// empty only if we do not write it; write an explicit 404 case by
	// asking for a repo path that never matches a rule instead.
	ok, err := isAuthorizedToShipBitbucketCloud(context.Background(), api, "acme", "infra", 7, "abc123",
		bbcloud.Account{AccountID: "acct-jdoe"})
	if err != nil {
		t.Fatalf("isAuthorizedToShipBitbucketCloud: %v", err)
	}
	if ok {
		t.Error("an empty CODEOWNERS must authorize nobody, not everybody")
	}
}

func TestIsAuthorizedToShipBitbucketCloud_UnidentifiableCommenterRefused(t *testing.T) {
	api := bitbucketCloudFixture(t, "stacks/payments/ @jdoe\n", []string{"stacks/payments/main.tf"}, bbcMembers, "")

	ok, err := isAuthorizedToShipBitbucketCloud(context.Background(), api, "acme", "infra", 7, "abc123",
		bbcloud.Account{DisplayName: "Jane Doe"})
	if err != nil {
		t.Fatalf("isAuthorizedToShipBitbucketCloud: %v", err)
	}
	if ok {
		t.Error("a commenter with no stable identity must never be authorized by a label match")
	}
}

// TestIsAuthorizedToReplanBitbucketCloud covers the identical two-tier
// rule every other platform has, on account_ids: the pull request's own
// creator always qualifies, and for a drift-watch-opened pull request
// (creator is the configured bot) the fallback is a real, live
// write-or-above permission check.
func TestIsAuthorizedToReplanBitbucketCloud(t *testing.T) {
	const perms = `{"permission":"write","user":{"account_id":"acct-rroe"}},{"permission":"read","user":{"account_id":"acct-reader"}}`
	api := bitbucketCloudFixture(t, "", nil, bbcMembers, perms)

	cases := []struct {
		name      string
		creator   bbcloud.Account
		commenter bbcloud.Account
		want      bool
	}{
		{"the creator can re-plan their own pull request", bbcloud.Account{AccountID: "acct-jdoe"}, bbcloud.Account{AccountID: "acct-jdoe"}, true},
		{"a stranger cannot re-plan somebody else's", bbcloud.Account{AccountID: "acct-jdoe"}, bbcloud.Account{AccountID: "acct-rroe"}, false},
		{"a writer can re-plan a drift-watch-opened pull request", bbcloud.Account{AccountID: "acct-bot"}, bbcloud.Account{AccountID: "acct-rroe"}, true},
		{"a reader cannot re-plan a drift-watch-opened pull request", bbcloud.Account{AccountID: "acct-bot"}, bbcloud.Account{AccountID: "acct-reader"}, false},
		{"an unidentifiable commenter is refused", bbcloud.Account{AccountID: "acct-jdoe"}, bbcloud.Account{DisplayName: "Jane Doe"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := isAuthorizedToReplanBitbucketCloud(context.Background(), api, "acme", "infra", "acct-bot", c.creator, c.commenter)
			if err != nil {
				t.Fatalf("isAuthorizedToReplanBitbucketCloud: %v", err)
			}
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
