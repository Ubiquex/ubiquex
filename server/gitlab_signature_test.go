package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// gitlabSignedRequest builds a real *http.Request carrying a real,
// correctly-computed GitLab webhook signature -- the same real
// construction validGitLabSignature's own doc comment documents:
// message = "{id}.{timestamp}.{raw body}", HMAC-SHA256 keyed on the
// signing token (stripped of "whsec_", base64-decoded), base64-encoded
// and prefixed "v1,".
func gitlabSignedRequest(t *testing.T, signingToken, id, timestamp, body string) *http.Request {
	t.Helper()
	rawKey, err := decodeGitLabSigningToken(signingToken)
	if err != nil {
		t.Fatalf("decodeGitLabSigningToken: %v", err)
	}
	mac := hmac.New(sha256.New, rawKey)
	mac.Write([]byte(id + "." + timestamp + "." + body))
	sig := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	r := httptest.NewRequest(http.MethodPost, "/webhook/gitlab", strings.NewReader(body))
	r.Header.Set("Webhook-Id", id)
	r.Header.Set("Webhook-Timestamp", timestamp)
	r.Header.Set("Webhook-Signature", sig)
	return r
}

const testGitLabSigningToken = "whsec_" // base64("") = "" -- an empty but structurally valid key

func TestValidGitLabSignature_Accepted(t *testing.T) {
	body := `{"object_kind":"merge_request"}`
	r := gitlabSignedRequest(t, testGitLabSigningToken, "msg-1", "1700000000", body)

	if !validGitLabSignature(testGitLabSigningToken, r, []byte(body)) {
		t.Error("expected a correctly-computed signature to be accepted")
	}
}

func TestValidGitLabSignature_WrongSecretRejected(t *testing.T) {
	body := `{"object_kind":"merge_request"}`
	r := gitlabSignedRequest(t, testGitLabSigningToken, "msg-1", "1700000000", body)

	if validGitLabSignature("whsec_d3JvbmctdG9rZW4=", r, []byte(body)) {
		t.Error("expected a signature computed with a different secret to be rejected")
	}
}

func TestValidGitLabSignature_TamperedBodyRejected(t *testing.T) {
	body := `{"object_kind":"merge_request"}`
	r := gitlabSignedRequest(t, testGitLabSigningToken, "msg-1", "1700000000", body)

	tampered := `{"object_kind":"merge_request","action":"open"}`
	if validGitLabSignature(testGitLabSigningToken, r, []byte(tampered)) {
		t.Error("expected a signature computed over a different body to be rejected -- the real replay/tamper defense this mechanism exists for")
	}
}

func TestValidGitLabSignature_MissingHeadersRejected(t *testing.T) {
	body := `{"object_kind":"merge_request"}`
	cases := []struct {
		name string
		drop string
	}{
		{"missing Webhook-Id", "Webhook-Id"},
		{"missing Webhook-Timestamp", "Webhook-Timestamp"},
		{"missing Webhook-Signature", "Webhook-Signature"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := gitlabSignedRequest(t, testGitLabSigningToken, "msg-1", "1700000000", body)
			r.Header.Del(c.drop)
			if validGitLabSignature(testGitLabSigningToken, r, []byte(body)) {
				t.Errorf("expected rejection with %s missing", c.drop)
			}
		})
	}
}

func TestValidGitLabSignature_MalformedSigningTokenRejected(t *testing.T) {
	body := `{"object_kind":"merge_request"}`
	r := gitlabSignedRequest(t, testGitLabSigningToken, "msg-1", "1700000000", body)

	// A signing token that isn't valid base64 after stripping "whsec_"
	// must be refused outright, not treated as an empty/zero key.
	if validGitLabSignature("whsec_not-valid-base64!!!", r, []byte(body)) {
		t.Error("expected rejection when the configured signing token itself doesn't decode")
	}
}

// TestValidGitLabSignature_KeyRotationAcceptsAnyListedSignature proves
// the real, documented key-rotation support: GitLab's own
// Webhook-Signature header may carry more than one space-separated
// signature (the webhook UI's own "Rotate signing token" flow keeps
// the old token's signature valid alongside the new one during
// rotation) -- a match against ANY one of them is accepted.
func TestValidGitLabSignature_KeyRotationAcceptsAnyListedSignature(t *testing.T) {
	body := `{"object_kind":"merge_request"}`
	oldToken := "whsec_b2xkLXRva2Vu"
	newToken := testGitLabSigningToken

	r := gitlabSignedRequest(t, oldToken, "msg-1", "1700000000", body)
	newSig := computeGitLabSignatureForTest(t, newToken, "msg-1", "1700000000", body)
	r.Header.Set("Webhook-Signature", r.Header.Get("Webhook-Signature")+" "+newSig)

	if !validGitLabSignature(newToken, r, []byte(body)) {
		t.Error("expected the new token's own signature to be accepted even though it's the second of two listed")
	}
	if !validGitLabSignature(oldToken, r, []byte(body)) {
		t.Error("expected the old token's own signature to still be accepted during rotation")
	}
}

func computeGitLabSignatureForTest(t *testing.T, signingToken, id, timestamp, body string) string {
	t.Helper()
	rawKey, err := decodeGitLabSigningToken(signingToken)
	if err != nil {
		t.Fatalf("decodeGitLabSigningToken: %v", err)
	}
	mac := hmac.New(sha256.New, rawKey)
	mac.Write([]byte(id + "." + timestamp + "." + body))
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
