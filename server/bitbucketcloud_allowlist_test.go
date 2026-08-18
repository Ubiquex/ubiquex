package server

import (
	"context"
	"strings"
	"testing"
)

// bitbucketCloudPayload builds a real, minimal Bitbucket Cloud webhook
// payload for repository fullName. Deliberately uses full_name, which
// is the real "workspace/repo_slug" pair every API path needs, rather
// than the separate display name field.
func bitbucketCloudPayload(fullName string, prID int64, comment string) []byte {
	body := `{"actor":{"account_id":"acct-human","nickname":"jdoe"},` +
		`"repository":{"full_name":"` + fullName + `"},` +
		`"pullrequest":{"id":` + itoa(prID) + `}`
	if comment != "" {
		body += `,"comment":{"content":{"raw":"` + comment + `"}}`
	}
	return []byte(body + `}`)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestBitbucketCloud_UnlistedRepoRefusedAndLogged proves UBI-166's own
// real allowlist enforcement extends unchanged to the fifth platform,
// on every real event type it handles. s.bitbucketcloud is deliberately
// left nil: the allowlist check must happen before any client use at
// all, so a refusal must not depend on Bitbucket Cloud support even
// being configured.
func TestBitbucketCloud_UnlistedRepoRefusedAndLogged(t *testing.T) {
	cases := []struct {
		name     string
		eventKey string
		comment  string
	}{
		{"pull request created", "pullrequest:created", ""},
		{"push to the source branch", "pullrequest:push", ""},
		{"approval", "pullrequest:approved", ""},
		{"merge", "pullrequest:fulfilled", ""},
		{"ubx ship comment", "pullrequest:comment_created", "ubx ship"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := captureSlog(t)
			s := &Server{cfg: &Config{ShipOnMerge: true}}

			s.handleBitbucketCloudEvent(context.Background(), c.eventKey, bitbucketCloudPayload("acme/infra", 42, c.comment))

			out := buf.String()
			if !strings.Contains(out, "allowlist") || !strings.Contains(out, "acme/infra") {
				t.Errorf("log output = %q, want a real, loud refusal naming the repo and the allowlist", out)
			}
			if !strings.Contains(out, "UBI-166") {
				t.Errorf("log output = %q, want the refusal attributed to UBI-166", out)
			}
		})
	}
}

// TestBitbucketCloud_UnrecognizedEventKeyIgnored is the real
// fail-closed property behind dispatching on the X-Event-Key header:
// the header name is the one detail in this integration that comes from
// Atlassian's webhook documentation rather than from the
// machine-readable API definition, so an absent or unrecognized key
// must do nothing at all rather than fall through to some default
// action.
func TestBitbucketCloud_UnrecognizedEventKeyIgnored(t *testing.T) {
	for _, key := range []string{"", "pullrequest:updated", "repo:push", "pullrequest:rejected", "nonsense"} {
		buf := captureSlog(t)
		s := &Server{cfg: &Config{ShipOnMerge: true}}

		s.handleBitbucketCloudEvent(context.Background(), key, bitbucketCloudPayload("acme/infra", 42, ""))

		if out := buf.String(); out != "" {
			t.Errorf("event key %q produced output %q, want a silent no-op", key, out)
		}
	}
}

// TestBitbucketCloud_MalformedPayloadRefused covers a payload that
// parses but carries no real repository identity: refused with a real
// error rather than acted on against an empty workspace.
func TestBitbucketCloud_MalformedPayloadRefused(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{Repos: []RepoConfig{{Platform: "bitbucketcloud", Project: "acme", Repository: "infra"}}}}

	s.handleBitbucketCloudEvent(context.Background(), "pullrequest:created", []byte(`{"repository":{},"pullrequest":{"id":1}}`))

	if out := buf.String(); !strings.Contains(out, "full_name") {
		t.Errorf("log output = %q, want a real refusal naming the missing repository identity", out)
	}
}

// TestMatchingRepoConfigsBitbucketCloud proves the allowlist match is
// on the real workspace and repository slug pair, and that a Bitbucket
// SERVER entry with the same two segments never satisfies a Bitbucket
// Cloud event. The two platforms are genuinely separate (UBI-170), and
// an operator who allowlisted a repo on one must not silently have
// allowlisted a same-named repo on the other.
func TestMatchingRepoConfigsBitbucketCloud(t *testing.T) {
	s := &Server{cfg: &Config{Repos: []RepoConfig{
		{Platform: "bitbucketcloud", Project: "acme", Repository: "infra"},
		{Platform: "bitbucketserver", Project: "acme", Repository: "payments"},
	}}}

	if got := s.matchingRepoConfigsBitbucketCloud("acme", "infra"); len(got) != 1 {
		t.Errorf("expected the real Bitbucket Cloud entry to match, got %v", got)
	}
	if got := s.matchingRepoConfigsBitbucketCloud("acme", "payments"); len(got) != 0 {
		t.Errorf("a Bitbucket Server entry must never authorize a Bitbucket Cloud event, got %v", got)
	}
	if got := s.matchingRepoConfigsBitbucketCloud("acme", "unlisted"); len(got) != 0 {
		t.Errorf("expected no match for an unlisted repo, got %v", got)
	}
}
