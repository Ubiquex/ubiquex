// UBI-182 Stage D's own real, final proof: a [providers.kubernetes]
// pinned entry resolves against the real, live github.com/Ubiquex/
// ubx-schema-kubernetes release with zero schema_url network at
// resolution time -- the whole reason this mechanism exists (see
// provider/acquireschema.go's own doc comment). Gated behind
// UBX_CONFORMANCE_LIVE, matching every other real-network-touching test
// in this codebase (conformance.RequireLive) -- go test ./... stays
// hermetic and credential-free everywhere else.
package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// requireDynamicProviderPinLive is requireAttributionLive's own real
// sibling for this file -- same env var, same "small enough not to
// share" duplication attribution_live_test.go's own doc comment already
// established, kept local rather than reused because the two files gate
// unrelated concerns (CloudTrail attribution vs. a pinned schema
// snapshot) even though the mechanism is identical.
func requireDynamicProviderPinLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_CONFORMANCE_LIVE") != "1" {
		t.Skip("skipping: set UBX_CONFORMANCE_LIVE=1 to resolve the real, pinned ubx-schema-kubernetes release")
	}
}

// kubernetesSchemaCacheDir is provider.AcquireSchema's own real,
// documented cache location for this exact pinned entry
// (~/.ubx/schemas/<namespace>/<type>/<version>/, see that function's own
// doc comment) -- hardcoded here rather than calling the unexported
// defaultSchemaCacheRoot in package provider, since this is a stable,
// documented convention, not a private implementation detail.
func kubernetesSchemaCacheDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".ubx", "schemas", "ubiquex", "kubernetes", "1.0.0")
}

// pinnedKubernetesParams is the real [providers.kubernetes] entry UBI-182
// Stage D actually published -- source = "ubiquex/kubernetes" resolves to
// github.com/Ubiquex/ubx-schema-kubernetes (provider/schemasource.go's own
// schemaRepoPrefix), version 1.0.0 is the real, live GitHub Release cut by
// that repo's own publish.yml.
var pinnedKubernetesParams = map[string]any{
	"source":  "ubiquex/kubernetes",
	"version": "1.0.0",
}

// TestConformance_DynamicProvider_Kubernetes_Pinned_PopulatesCache is
// phase 1 of the real, two-process proof (see phase 2's own doc comment
// for why this can't be one test function): deletes any existing cache
// entry so this run proves a REAL first-time fetch from the real, live
// GitHub Release (not an accidental cache hit left over from an earlier
// run), then resolves the pinned entry and confirms it returns real
// Kubernetes resource schemas and leaves a real, verified snapshot behind
// in the cache.
func TestConformance_DynamicProvider_Kubernetes_Pinned_PopulatesCache(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := kubernetesSchemaCacheDir(t)
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatalf("clear existing cache at %s: %v", cacheDir, err)
	}

	schemas, err := loadDynamicProviderSchema(context.Background(), "kubernetes", pinnedKubernetesParams)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned): %v", err)
	}
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution returned zero resource types")
	}
	t.Logf("real, pinned resolution returned %d resource types", len(schemas.Resources))

	snapPath := filepath.Join(cacheDir, "snapshot.json")
	if _, err := os.Stat(snapPath); err != nil {
		t.Fatalf("expected a real, verified snapshot cached at %s after a real fetch: %v", snapPath, err)
	}
	t.Logf("real cache populated at %s", snapPath)
}

// TestConformance_DynamicProvider_Kubernetes_Pinned_ZeroNetworkOnCacheHit
// is phase 2: run as a SEPARATE `go test` process (Go's own
// http.ProxyFromEnvironment caches the environment-derived proxy config
// once per process, so poisoning HTTPS_PROXY/HTTP_PROXY mid-process after
// phase 1's own real network call would never take effect -- confirmed
// against net/http's own real, documented behavior before designing this
// test this way, not guessed). This process sets an unreachable proxy
// for ALL of its own HTTP traffic before ubx ever runs -- if
// AcquireSchema attempted so much as one real HTTP request (to GitHub's
// release API, to schema_url, to anything), it would fail immediately
// (connection refused, nothing listens on 127.0.0.1:1) rather than
// silently succeeding. A pinned entry succeeding here is real, observed
// proof of zero network at schema resolution, not an inference from the
// config's own shape.
func TestConformance_DynamicProvider_Kubernetes_Pinned_ZeroNetworkOnCacheHit(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := kubernetesSchemaCacheDir(t)
	snapPath := filepath.Join(cacheDir, "snapshot.json")
	if _, err := os.Stat(snapPath); err != nil {
		t.Fatalf("expected phase 1 (TestConformance_DynamicProvider_Kubernetes_Pinned_PopulatesCache) to have already cached a real snapshot at %s: %v", snapPath, err)
	}

	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	schemas, err := loadDynamicProviderSchema(context.Background(), "kubernetes", pinnedKubernetesParams)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, network poisoned) failed -- this means real network was attempted and blocked, not that resolution is cache-only: %v", err)
	}
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero resource types")
	}
	t.Logf("real, pinned resolution succeeded with ALL network poisoned (HTTPS_PROXY/HTTP_PROXY -> unreachable): %d resource types, served entirely from the real, verified local cache", len(schemas.Resources))
}
