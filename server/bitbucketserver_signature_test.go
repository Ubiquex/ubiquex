package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func bitbucketServerRequest(t *testing.T, body []byte, headerValue string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/webhook/bitbucketserver", strings.NewReader(string(body)))
	if headerValue != "" {
		r.Header.Set("X-Hub-Signature", headerValue)
	}
	return r
}

func realBitbucketServerSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidBitbucketServerSignature_Accepted(t *testing.T) {
	body := []byte(`{"eventKey":"pr:opened"}`)
	r := bitbucketServerRequest(t, body, realBitbucketServerSignature("s3cr3t", body))
	if !validBitbucketServerSignature("s3cr3t", body, r) {
		t.Error("expected a real, matching HMAC-SHA256 signature to be accepted")
	}
}

func TestValidBitbucketServerSignature_WrongSecretRejected(t *testing.T) {
	body := []byte(`{"eventKey":"pr:opened"}`)
	r := bitbucketServerRequest(t, body, realBitbucketServerSignature("wrong-secret", body))
	if validBitbucketServerSignature("s3cr3t", body, r) {
		t.Error("expected a signature computed with the wrong secret to be rejected")
	}
}

func TestValidBitbucketServerSignature_TamperedBodyRejected(t *testing.T) {
	original := []byte(`{"eventKey":"pr:opened"}`)
	sig := realBitbucketServerSignature("s3cr3t", original)
	tampered := []byte(`{"eventKey":"pr:merged"}`)
	r := bitbucketServerRequest(t, tampered, sig)
	if validBitbucketServerSignature("s3cr3t", tampered, r) {
		t.Error("expected a signature computed over a different body to be rejected")
	}
}

func TestValidBitbucketServerSignature_MissingHeaderRejected(t *testing.T) {
	body := []byte(`{"eventKey":"pr:opened"}`)
	r := bitbucketServerRequest(t, body, "")
	if validBitbucketServerSignature("s3cr3t", body, r) {
		t.Error("expected a missing X-Hub-Signature header to be rejected")
	}
}

func TestValidBitbucketServerSignature_MissingPrefixRejected(t *testing.T) {
	// A real header value that carries the correct hex digest but without
	// the required "sha256=" prefix -- must never be silently accepted
	// as if it were a bare-hex format.
	body := []byte(`{"eventKey":"pr:opened"}`)
	mac := hmac.New(sha256.New, []byte("s3cr3t"))
	mac.Write(body)
	r := bitbucketServerRequest(t, body, hex.EncodeToString(mac.Sum(nil)))
	if validBitbucketServerSignature("s3cr3t", body, r) {
		t.Error("expected a header value missing the required \"sha256=\" prefix to be rejected")
	}
}

func TestValidBitbucketServerSignature_MalformedHexRejected(t *testing.T) {
	body := []byte(`{"eventKey":"pr:opened"}`)
	r := bitbucketServerRequest(t, body, "sha256=not-real-hex")
	if validBitbucketServerSignature("s3cr3t", body, r) {
		t.Error("expected a non-hex signature value to be rejected, not panic or silently pass")
	}
}

func TestValidBitbucketServerSignature_UnconfiguredSecretAlwaysRejected(t *testing.T) {
	// A real, deliberate refusal: an empty configured secret must never
	// accidentally accept anything -- the same "no confused fail-open"
	// discipline every other platform's own signature check in this
	// package already has.
	body := []byte(`{"eventKey":"pr:opened"}`)
	r := bitbucketServerRequest(t, body, realBitbucketServerSignature("", body))
	if validBitbucketServerSignature("", body, r) {
		t.Error("expected an unconfigured (empty) secret to always be rejected")
	}
}
