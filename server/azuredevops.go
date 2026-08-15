package server

import (
	"crypto/subtle"
	"net/http"

	adevops "github.com/ubiquex/ubiquex/azuredevops"
)

// newAzureDevOpsClient builds the one, real, static Azure DevOps API
// client this server uses for every Azure-DevOps-watched repo --
// unlike GitHub's own per-installation client cache, and like GitLab's
// single Group Access Token client, Azure DevOps has no per-
// installation concept either: one real Personal Access Token,
// authenticating against one real, fixed organization.
func newAzureDevOpsClient(organization, token, baseURL string) *adevops.Client {
	var opts []adevops.Option
	if baseURL != "" {
		// Both bases get the same value, which is the real, correct
		// production shape for an on-prem Azure DevOps Server (UBI-171):
		// the separate vssps.dev.azure.com Graph host exists only in the
		// SaaS, and on-prem serves Graph from the same collection base
		// as the git and policy APIs. A test pointing both at one
		// httptest.Server is the same wiring for a different reason.
		opts = append(opts, adevops.WithBaseURL(baseURL), adevops.WithGraphBaseURL(baseURL))
	}
	return adevops.New(organization, token, opts...)
}

// validAzureDevOpsSignature checks r's own real, configured shared-
// secret header against secret -- Azure DevOps' own Service Hooks have
// no cryptographic webhook signature scheme at all, confirmed directly
// against Microsoft's own current docs (unlike GitHub's
// X-Hub-Signature-256 or GitLab's Webhook-Signature); this implements
// Microsoft's own documented security recommendation instead: "Use
// shared secrets for authentication. Pass a secret in the custom HTTP
// headers when creating subscriptions and verify this secret on every
// incoming request." headerName is itself operator-configured (Azure
// DevOps has no fixed convention for it), defaulting to
// "X-Ubx-Webhook-Secret" (Config.AzureDevOpsWebhookSecretHeader).
// Constant-time comparison, same discipline as every other platform's
// own signature check in this package, even though this is a direct
// secret comparison rather than an HMAC digest.
func validAzureDevOpsSignature(headerName, secret string, r *http.Request) bool {
	if secret == "" {
		return false
	}
	got := r.Header.Get(headerName)
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(secret)) == 1
}
