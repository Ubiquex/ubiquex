package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"net/http"
	"strings"

	bbcloud "github.com/ubiquex/ubiquex/bitbucketcloud"
)

// newBitbucketCloudClient builds the one, real, static Bitbucket Cloud
// API client this server uses for every Bitbucket-Cloud-watched repo --
// one real, static access token, the same "one instance, one
// credential" simplification GitLab's/Azure DevOps'/Bitbucket Server's
// own single clients already make.
//
// Unlike Bitbucket Server (self-hosted, no default host at all),
// Bitbucket Cloud is SaaS with one real, fixed host, so baseURL here is
// a genuine test-only override of a real default, matching GitHub's own
// shape rather than Bitbucket Server's.
func newBitbucketCloudClient(token, baseURL string) *bbcloud.Client {
	var opts []bbcloud.Option
	if baseURL != "" {
		opts = append(opts, bbcloud.WithBaseURL(baseURL))
	}
	return bbcloud.New(token, opts...)
}

// bitbucketCloudSignatureAlgorithms is the real set of HMAC algorithms
// this server will verify a Bitbucket Cloud webhook signature with.
//
// Deliberately a lookup keyed on the algorithm name carried in the
// header itself, rather than a hardcoded single algorithm. Bitbucket
// Cloud's own current documentation states the digest is HMAC-SHA256
// delivered as "sha256=<hex>", AND states the algorithm might change in
// the future. Parsing the name the delivery actually declares means a
// future Atlassian change surfaces as a real, explicit "unsupported
// algorithm" refusal rather than as every delivery silently failing to
// verify against an assumption baked into this code.
//
// Adding an algorithm here is the entire change needed to support one.
// An algorithm NOT in this map is refused outright: falling back to a
// default would let a delivery choose the weaker of two schemes, which
// is the classic signature-verification downgrade.
var bitbucketCloudSignatureAlgorithms = map[string]func() hash.Hash{
	"sha256": sha256.New,
}

// validBitbucketCloudSignature verifies r's own real X-Hub-Signature
// header against a fresh HMAC digest of body, keyed on secret.
//
// Real, confirmed mechanism (Bitbucket Cloud's own official API
// definition, api.bitbucket.org/swagger.json): a webhook subscription
// carries an optional write-only `secret`; when set, "the secret is
// used as a key to generate the HMAC hex digest sent in the
// X-Hub-Signature header at delivery time", and Bitbucket only
// generates that header when a secret is set at all.
//
// A real, easy-to-misread collision worth stating plainly: Bitbucket
// SERVER uses the identical header name for its own separate scheme.
// Sharing a vendor and a header name does not make the two mechanisms
// one mechanism, and this function was written against Bitbucket
// Cloud's own documentation independently rather than by reusing
// validBitbucketServerSignature.
//
// Fails closed on every ambiguous input, the same discipline every
// other platform's own signature check in this package already applies:
// an unset secret (a webhook can legitimately exist with no secret, in
// which case no header is sent at all and no delivery can be trusted),
// a missing or malformed header, an unparseable hex digest, or an
// algorithm this server does not implement.
func validBitbucketCloudSignature(secret string, body []byte, r *http.Request) bool {
	if secret == "" {
		return false
	}
	header := r.Header.Get("X-Hub-Signature")
	algName, digest, ok := strings.Cut(header, "=")
	if !ok {
		return false
	}
	algName = strings.ToLower(strings.TrimSpace(algName))
	newHash, supported := bitbucketCloudSignatureAlgorithms[algName]
	if !supported {
		return false
	}
	got, err := hex.DecodeString(strings.TrimSpace(digest))
	if err != nil {
		return false
	}

	mac := hmac.New(newHash, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
