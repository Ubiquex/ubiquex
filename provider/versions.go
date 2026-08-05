package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/mod/semver"
)

// versionsAPIBase is the real, confirmed, unauthenticated Terraform
// Registry "list versions" endpoint (UBI-99's own proven call shape,
// reused here verbatim rather than re-derived) --
// https://registry.terraform.io/v1/providers/hashicorp/aws/versions
// returns real version metadata for a provider, no auth needed. This is
// deliberately registry.terraform.io, NOT registryAPIBase
// (registry.opentofu.org, above) -- that substitution exists for
// BINARY-download ToS reasons (this file's own sibling, registry.go, has
// the full account) and has no bearing here: this endpoint only ever
// lists version numbers, never downloads a package, and UBI-99 already
// confirmed live that hitting the real Terraform Registry directly for
// exactly this purpose works with no auth and no rate-limit friction.
const versionsAPIBase = "https://registry.terraform.io/v1/providers"

// versionsResponse is the subset of the real "list versions" response
// this package reads -- confirmed against the real, live endpoint by
// UBI-99's own session (docs/sdk.md's "Amendment ... UBI-99"), reused
// here rather than re-verified from scratch a second time.
type versionsResponse struct {
	Versions []struct {
		Version string `json:"version"`
	} `json:"versions"`
}

// versionsConfig mirrors acquireConfig's own "With* options exist for
// tests, real callers don't need them" posture (acquire.go) -- a real
// httptest server for hermetic tests, a real client otherwise.
type versionsConfig struct {
	httpClient *http.Client
	apiBase    string
}

// VersionsOption configures LatestVersion.
type VersionsOption func(*versionsConfig)

// WithVersionsHTTPClient overrides the http.Client LatestVersion uses
// (default: http.DefaultClient).
func WithVersionsHTTPClient(c *http.Client) VersionsOption {
	return func(cfg *versionsConfig) { cfg.httpClient = c }
}

// WithVersionsAPIBase overrides the registry API base URL (default:
// versionsAPIBase, the real registry.terraform.io endpoint) -- tests
// point this at a local httptest server.
func WithVersionsAPIBase(base string) VersionsOption {
	return func(cfg *versionsConfig) { cfg.apiBase = base }
}

// LatestVersion queries the real Terraform Registry's own "list
// versions" endpoint for src and returns the highest real semantic
// version it reports -- UBI-99's own proven mechanism
// (`GET /v1/providers/:namespace/:type/versions`), as a reusable Go
// primitive instead of hand-rolled curl+jq+sort -V duplicated in every
// consuming workflow's own YAML (this ticket's own UBI-124 scope: a
// first-class primitive, not GitHub-Actions-only boilerplate).
//
// Comparison uses golang.org/x/mod/semver (a real "v"-prefixed semver
// comparator, already resolved in this module's own dependency graph),
// with pre-release versions excluded from consideration entirely unless
// EVERY reported version is a pre-release -- a real, deliberate
// improvement over UBI-99's own accepted `sort -V` limitation (a bare
// numeric-precedence comparator alone is NOT enough: real semver
// precedence ranks 6.60.0-beta1 ABOVE 6.57.1, since a pre-release only
// ranks below its OWN associated stable release, not below every
// lower-numbered stable one -- confirmed by a real, failing test before
// this filter was added, not assumed correct from the library's own
// name). This is the one place this function's own behavior is more
// than a mechanical port of UBI-99's shell version.
// src.Hostname is ignored -- the real Terraform Registry's own "list
// versions" endpoint has no multi-registry concept, matching UBI-99's
// own hardcoded hashicorp/aws call shape (a self-hosted registry mirror
// is out of scope here, same as it was there).
func LatestVersion(ctx context.Context, src Source, opts ...VersionsOption) (string, error) {
	cfg := versionsConfig{httpClient: http.DefaultClient, apiBase: versionsAPIBase}
	for _, opt := range opts {
		opt(&cfg)
	}

	url := fmt.Sprintf("%s/%s/%s/versions", cfg.apiBase, src.Namespace, src.Type)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("provider: build versions request for %s: %w", src, err)
	}
	resp, err := cfg.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("provider: query registry for %s: %w", src, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("provider: registry returned %s for %s: %s", resp.Status, src, body)
	}

	var parsed versionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("provider: decode versions response for %s: %w", src, err)
	}
	if len(parsed.Versions) == 0 {
		return "", fmt.Errorf("provider: registry reported zero versions for %s -- failing loudly rather than silently reporting no update available", src)
	}

	stable := make([]string, 0, len(parsed.Versions))
	for _, v := range parsed.Versions {
		if semver.Prerelease("v"+v.Version) == "" {
			stable = append(stable, v.Version)
		}
	}
	candidates := stable
	if len(candidates) == 0 {
		// Every reported version is a pre-release -- fall back to
		// comparing all of them rather than reporting no versions at
		// all (a real, if unlikely, state for a brand-new provider).
		for _, v := range parsed.Versions {
			candidates = append(candidates, v.Version)
		}
	}

	latest := candidates[0]
	for _, v := range candidates[1:] {
		if semver.Compare("v"+v, "v"+latest) > 0 {
			latest = v
		}
	}
	return latest, nil
}
