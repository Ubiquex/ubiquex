package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	glab "github.com/ubiquex/ubiquex/gitlab"
)

// newGitLabClient builds the one, real, static GitLab API client this
// server uses for every GitLab-watched repo -- unlike GitHub's own
// per-installation client cache (githubapp.go), GitLab has no unified
// "App" concept and no per-installation token to cache in the first
// place; confirmed directly against GitLab's own current docs, not
// assumed symmetric with GitHub. A single real Group Access Token
// authenticates every call this package makes.
func newGitLabClient(token, baseURL string) (*glab.Client, error) {
	var opts []glab.Option
	if baseURL != "" {
		opts = append(opts, glab.WithBaseURL(baseURL))
	}
	c, err := glab.New(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("gitlab client: %w", err)
	}
	return c, nil
}

// validGitLabSignature verifies r's own real, current GitLab webhook
// signing-token mechanism -- confirmed directly against GitLab's own
// current docs, not the older, GitLab-labeled "not recommended"
// plain-text X-Gitlab-Token header, which this function doesn't accept
// at all. Real construction: three headers (Webhook-Id, Webhook-
// Timestamp, Webhook-Signature), a message of
// "{webhook-id}.{webhook-timestamp}.{raw body}", an HMAC-SHA256 digest
// keyed on the signing token (stripped of its real "whsec_" prefix,
// then base64-decoded into raw key bytes), base64-encoded and
// formatted "v1,{signature}". The header may carry more than one
// space-separated signature (GitLab's own real key-rotation support);
// a match against any one of them is accepted.
func validGitLabSignature(secret string, r *http.Request, body []byte) bool {
	rawKey, err := decodeGitLabSigningToken(secret)
	if err != nil {
		return false
	}

	messageID := r.Header.Get("Webhook-Id")
	timestamp := r.Header.Get("Webhook-Timestamp")
	received := r.Header.Get("Webhook-Signature")
	if messageID == "" || timestamp == "" || received == "" {
		return false
	}

	message := messageID + "." + timestamp + "." + string(body)
	mac := hmac.New(sha256.New, rawKey)
	mac.Write([]byte(message))
	expected := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))

	for _, sig := range strings.Split(received, " ") {
		if subtle.ConstantTimeCompare([]byte(expected), []byte(sig)) == 1 {
			return true
		}
	}
	return false
}

// decodeGitLabSigningToken strips signingToken's own real "whsec_"
// prefix and base64-decodes the remainder into the raw HMAC key bytes
// -- exactly GitLab's own documented decode steps, not guessed.
func decodeGitLabSigningToken(signingToken string) ([]byte, error) {
	trimmed := strings.TrimPrefix(signingToken, "whsec_")
	key, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("decode gitlab signing token: %w", err)
	}
	return key, nil
}
