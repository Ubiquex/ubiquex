package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	bbserver "github.com/ubiquex/ubiquex/bitbucketserver"
)

// newBitbucketServerClient builds the one, real, static Bitbucket
// Server API client this server uses for every Bitbucket-Server-watched
// repo -- one real, static HTTP access token, the same "one instance,
// one credential" simplification GitLab's/Azure DevOps' own single
// client already make. Unlike GitHub's per-installation client cache
// and unlike GitLab/Azure DevOps (each with one real, fixed default
// host), Bitbucket Server is self-hosted with no default host at all
// (bitbucketserver.Client's own doc comment) -- baseURL is always
// required here, never optional.
func newBitbucketServerClient(baseURL, token string) *bbserver.Client {
	return bbserver.New(baseURL, token)
}

// validBitbucketServerSignature verifies r's own real X-Hub-Signature
// header against a fresh HMAC-SHA256 digest of body, keyed on secret --
// confirmed directly against Atlassian's own current Bitbucket Data
// Center/Server docs, not assumed symmetric with GitHub's or GitLab's
// own signature schemes even though the underlying primitive (HMAC-
// SHA256) is the same one GitHub's current X-Hub-Signature-256 uses:
// Bitbucket Server's own header is literally named "X-Hub-Signature"
// (matching the NAME of GitHub's older, deprecated SHA1-only header),
// but its real, current value format is "sha256=<hex>", the same
// SHA-256 content GitHub's newer, differently-named header carries --
// a real, easy-to-misread naming collision this function's own tests
// deliberately guard against assuming either way. Webhook creation
// itself is real and optional in Bitbucket Server's own UI (a webhook
// can exist with no secret configured, in which case no signature
// header is sent at all) -- an empty secret here always fails closed,
// the same discipline every other platform's own signature check in
// this package already applies.
func validBitbucketServerSignature(secret string, body []byte, r *http.Request) bool {
	if secret == "" {
		return false
	}
	header := r.Header.Get("X-Hub-Signature")
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)

	return hmac.Equal(got, want)
}
