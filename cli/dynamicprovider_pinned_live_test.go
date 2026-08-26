// UBI-182's own real, final proof: both real members of the
// [providers.kubernetes]/[providers.kubernetes_ds] group resolve against
// the real, live github.com/Ubiquex/ubx-schema-kubernetes v2.0.0 release
// (the group-container format, real 2-member group: kubernetes resource
// mode, kubernetes_ds data-source mode) with zero schema_url network at
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
// documented cache location for this exact pinned group
// (~/.ubx/schemas/<namespace>/<type>/<version>/, see that function's own
// doc comment) -- hardcoded here rather than calling the unexported
// defaultSchemaCacheRoot in package provider, since this is a stable,
// documented convention, not a private implementation detail. Both real
// members (kubernetes, kubernetes_ds) share this SAME cache directory --
// the whole real point of the group container: one real download, one
// real cache entry, regardless of how many members reference it.
func kubernetesSchemaCacheDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".ubx", "schemas", "ubiquex", "kubernetes", "2.0.0")
}

// pinnedKubernetesParams/pinnedKubernetesDataSourceParams are the real
// [providers.kubernetes]/[providers.kubernetes_ds] entries UBI-182
// actually published -- source = "ubiquex/kubernetes" resolves to
// github.com/Ubiquex/ubx-schema-kubernetes (provider/schemasource.go's
// own schemaRepoPrefix), version 2.0.0 is the real, live GitHub Release
// cut by that repo's own publish.yml (the group-container format --
// v1.0.0's own flat, single-member shape is superseded, not compatible).
// Both members point at the SAME repo+version -- only the launched
// process's own UBX_DYNAMIC_PROVIDER_NAME differs, which member it
// resolves out of the shared group.
var pinnedKubernetesParams = map[string]any{
	"source":  "ubiquex/kubernetes",
	"version": "2.0.0",
}

var pinnedKubernetesDataSourceParams = map[string]any{
	"source":  "ubiquex/kubernetes",
	"version": "2.0.0",
}

// TestConformance_DynamicProvider_Kubernetes_Pinned_PopulatesCache is
// phase 1 of the real, two-process proof (see phase 2's own doc comment
// for why this can't be one test function): deletes any existing cache
// entry so this run proves a REAL first-time fetch from the real, live
// GitHub Release -- downloading and extracting the real snapshot.tar.gz
// that release actually carries, not a locally-built stand-in -- then
// resolves BOTH real members and confirms each returns its own real,
// distinct schema shape (kubernetes: resources; kubernetes_ds: data
// sources) from ONE real cache entry.
func TestConformance_DynamicProvider_Kubernetes_Pinned_PopulatesCache(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := kubernetesSchemaCacheDir(t)
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatalf("clear existing cache at %s: %v", cacheDir, err)
	}

	schemas, err := loadDynamicProviderSchema(context.Background(), "kubernetes", pinnedKubernetesParams)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, kubernetes): %v", err)
	}
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution (kubernetes) returned zero resource types")
	}
	t.Logf("real, pinned resolution (kubernetes) returned %d resource types", len(schemas.Resources))

	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected a real, verified group manifest cached at %s after a real fetch+extract from the live release: %v", manifestPath, err)
	}
	t.Logf("real cache populated at %s (extracted from the real, live snapshot.tar.gz release asset)", manifestPath)

	dsSchemas, err := loadDynamicProviderSchema(context.Background(), "kubernetes_ds", pinnedKubernetesDataSourceParams)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, kubernetes_ds): %v", err)
	}
	if len(dsSchemas.DataSources) == 0 {
		t.Fatal("pinned resolution (kubernetes_ds) returned zero data source types")
	}
	if len(dsSchemas.Resources) != 0 {
		t.Errorf("pinned resolution (kubernetes_ds) returned %d RESOURCES, want zero -- data-source mode must never serve resource-shaped output", len(dsSchemas.Resources))
	}
	t.Logf("real, pinned resolution (kubernetes_ds) returned %d data source types, from the SAME cache entry (kubernetes_ds.json member file)", len(dsSchemas.DataSources))
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
// silently succeeding. Both real members succeeding here is real,
// observed proof of zero network at schema resolution, not an inference
// from the config's own shape.
func TestConformance_DynamicProvider_Kubernetes_Pinned_ZeroNetworkOnCacheHit(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := kubernetesSchemaCacheDir(t)
	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected phase 1 (TestConformance_DynamicProvider_Kubernetes_Pinned_PopulatesCache) to have already cached a real group manifest at %s: %v", manifestPath, err)
	}

	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	schemas, err := loadDynamicProviderSchema(context.Background(), "kubernetes", pinnedKubernetesParams)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, kubernetes, network poisoned) failed -- this means real network was attempted and blocked, not that resolution is cache-only: %v", err)
	}
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution (kubernetes) with network poisoned returned zero resource types")
	}
	t.Logf("real, pinned resolution (kubernetes) succeeded with ALL network poisoned: %d resource types, served entirely from the real, verified local cache", len(schemas.Resources))

	dsSchemas, err := loadDynamicProviderSchema(context.Background(), "kubernetes_ds", pinnedKubernetesDataSourceParams)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, kubernetes_ds, network poisoned) failed: %v", err)
	}
	if len(dsSchemas.DataSources) == 0 {
		t.Fatal("pinned resolution (kubernetes_ds) with network poisoned returned zero data source types")
	}
	t.Logf("real, pinned resolution (kubernetes_ds) succeeded with ALL network poisoned: %d data source types, served entirely from the real, verified local cache", len(dsSchemas.DataSources))
}
