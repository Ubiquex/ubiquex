package server

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func bitbucketCloudRequest(t *testing.T, body []byte, headerValue string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/webhook/bitbucketcloud", strings.NewReader(string(body)))
	if headerValue != "" {
		r.Header.Set("X-Hub-Signature", headerValue)
	}
	return r
}

func realBitbucketCloudSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidBitbucketCloudSignature_Accepted(t *testing.T) {
	body := []byte(`{"pullrequest":{"id":42}}`)
	r := bitbucketCloudRequest(t, body, realBitbucketCloudSignature("s3cr3t", body))
	if !validBitbucketCloudSignature("s3cr3t", body, r) {
		t.Error("expected a real, matching HMAC-SHA256 signature to be accepted")
	}
}

func TestValidBitbucketCloudSignature_WrongSecretRejected(t *testing.T) {
	body := []byte(`{"pullrequest":{"id":42}}`)
	r := bitbucketCloudRequest(t, body, realBitbucketCloudSignature("wrong-secret", body))
	if validBitbucketCloudSignature("s3cr3t", body, r) {
		t.Error("expected a signature computed with the wrong secret to be rejected")
	}
}

func TestValidBitbucketCloudSignature_TamperedBodyRejected(t *testing.T) {
	original := []byte(`{"pullrequest":{"id":42}}`)
	sig := realBitbucketCloudSignature("s3cr3t", original)
	tampered := []byte(`{"pullrequest":{"id":99}}`)
	r := bitbucketCloudRequest(t, tampered, sig)
	if validBitbucketCloudSignature("s3cr3t", tampered, r) {
		t.Error("expected a signature computed over a different body to be rejected")
	}
}

func TestValidBitbucketCloudSignature_MissingHeaderRejected(t *testing.T) {
	body := []byte(`{"pullrequest":{"id":42}}`)
	r := bitbucketCloudRequest(t, body, "")
	if validBitbucketCloudSignature("s3cr3t", body, r) {
		t.Error("expected a missing X-Hub-Signature header to be rejected")
	}
}

// TestValidBitbucketCloudSignature_EmptySecretRejected covers the real,
// documented case where a Bitbucket Cloud webhook is created with no
// secret at all: Bitbucket then sends no signature header whatsoever,
// and every such delivery must fail closed rather than be treated as
// unsigned-but-fine.
func TestValidBitbucketCloudSignature_EmptySecretRejected(t *testing.T) {
	body := []byte(`{"pullrequest":{"id":42}}`)
	r := bitbucketCloudRequest(t, body, realBitbucketCloudSignature("", body))
	if validBitbucketCloudSignature("", body, r) {
		t.Error("expected an unconfigured secret to reject every delivery")
	}
}

// TestValidBitbucketCloudSignature_AlgorithmParsedFromHeader is the
// real point of parsing the algorithm name out of the header rather
// than hardcoding one: Atlassian's own documentation states the digest
// is currently HMAC-SHA256 and that this might change. A delivery that
// declares an algorithm this server does not implement must be refused
// explicitly, never verified against a different algorithm and never
// waved through.
//
// The sha1 case here is a real signature, correctly computed with SHA-1
// over the same body and secret. It is refused purely because sha1 is
// not in bitbucketCloudSignatureAlgorithms, which is exactly the
// downgrade protection this design exists for: an attacker who could
// choose the algorithm could otherwise pick the weakest one supported.
func TestValidBitbucketCloudSignature_AlgorithmParsedFromHeader(t *testing.T) {
	body := []byte(`{"pullrequest":{"id":42}}`)
	secret := "s3cr3t"

	sha1mac := hmac.New(sha1.New, []byte(secret))
	sha1mac.Write(body)
	realSHA1 := "sha1=" + hex.EncodeToString(sha1mac.Sum(nil))

	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"sha256 is implemented and accepted", realBitbucketCloudSignature(secret, body), true},
		{"a real sha1 signature is refused as an unimplemented algorithm", realSHA1, false},
		{"an unknown algorithm name is refused", "sha512=" + strings.Repeat("ab", 64), false},
		{"an empty algorithm name is refused", "=" + strings.Repeat("ab", 32), false},
		{"a bare digest with no algorithm prefix is refused", strings.Repeat("ab", 32), false},
		{"a non-hex digest is refused", "sha256=not-hex-at-all", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := bitbucketCloudRequest(t, body, c.header)
			if got := validBitbucketCloudSignature(secret, body, r); got != c.want {
				t.Errorf("validBitbucketCloudSignature(%q) = %v, want %v", c.header, got, c.want)
			}
		})
	}
}

// TestValidBitbucketCloudSignature_AlgorithmNameCaseInsensitive covers
// a real robustness property of parsing the name rather than comparing
// a fixed prefix: the algorithm name is matched case-insensitively, so
// a delivery declaring "SHA256=" verifies rather than being refused on
// a cosmetic difference.
func TestValidBitbucketCloudSignature_AlgorithmNameCaseInsensitive(t *testing.T) {
	body := []byte(`{"pullrequest":{"id":42}}`)
	sig := realBitbucketCloudSignature("s3cr3t", body)
	upper := strings.Replace(sig, "sha256=", "SHA256=", 1)
	r := bitbucketCloudRequest(t, body, upper)
	if !validBitbucketCloudSignature("s3cr3t", body, r) {
		t.Error("expected the algorithm name to be matched case-insensitively")
	}
}
