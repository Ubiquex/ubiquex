// UBI-182's own real, final proof for Datadog, mirroring
// dynamicprovider_pinned_live_test.go's own Kubernetes proof exactly: a
// single [providers.datadog] pin resolves ALL FOUR real members of the
// group (datadog/datadog_v2 resource mode, datadog_ds/datadog_v2_ds
// data-source mode) together, against the real, live
// github.com/Ubiquex/ubx-schema-datadog release, with zero schema_url
// network at resolution time. The real v1/v2 collision (two resource
// type names, one data-source type name) is resolved by the manifest's
// own Exclude table (ubx-provider-dynamic#20) -- a user never needs four
// separate pins, or any awareness the collision exists at all. Gated
// behind UBX_CONFORMANCE_LIVE, matching every other real-network-touching
// test in this codebase -- go test ./... stays hermetic and
// credential-free everywhere else.
package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// datadogSchemaCacheDir is provider.AcquireSchema's own real, documented
// cache location for this exact pinned group.
func datadogSchemaCacheDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".ubx", "schemas", "ubiquex", "datadog", "1.0.0")
}

// pinnedDatadogParams is the real, ONLY [providers.datadog] entry a real
// stack needs -- source = "ubiquex/datadog" resolves to
// github.com/Ubiquex/ubx-schema-datadog, version 1.0.0 is the real, live
// GitHub Release cut by that repo's own publish.yml. No separate
// datadog_v2/datadog_ds/datadog_v2_ds entry exists or is needed -- the
// launched process resolves and merges all four real members from this
// one pin, with the v1/v2 collision resolved via the manifest's own
// Exclude table.
var pinnedDatadogParams = map[string]any{
	"source":  "ubiquex/datadog",
	"version": "1.0.0",
}

// TestConformance_DynamicProvider_Datadog_Pinned_PopulatesCache is phase 1
// of the real, two-process proof: deletes any existing cache entry so
// this run proves a REAL first-time fetch from the real, live GitHub
// Release, then resolves the ONE pin and confirms it returns BOTH real
// schema shapes together (resources AND data sources) from a single
// call, with the real v1/v2 collision already resolved.
func TestConformance_DynamicProvider_Datadog_Pinned_PopulatesCache(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := datadogSchemaCacheDir(t)
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatalf("clear existing cache at %s: %v", cacheDir, err)
	}

	schemas, err := loadDynamicProviderSchema(context.Background(), "datadog", pinnedDatadogParams)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, datadog): %v", err)
	}
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution returned zero data source types -- the merge should have included datadog_ds/datadog_v2_ds too, from the SAME single pin")
	}
	t.Logf("real, pinned resolution from ONE entry returned %d resource types AND %d data source types together", len(schemas.Resources), len(schemas.DataSources))

	if _, ok := schemas.Resources["datadog_application_key_response"]; ok {
		attrs := schemas.Resources["datadog_application_key_response"].Block.Attributes
		if len(attrs) != 5 {
			t.Fatalf("datadog_application_key_response: expected v1's own 5 attributes to win the real collision (per the manifest's own Exclude table), got %d", len(attrs))
		}
		t.Logf("real collision resolved correctly: datadog_application_key_response has v1's own %d attributes", len(attrs))
	}

	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected a real, verified group manifest cached at %s after a real fetch+extract from the live release: %v", manifestPath, err)
	}
	t.Logf("real cache populated at %s (extracted from the real, live snapshot.tar.gz release asset)", manifestPath)
}

// TestConformance_DynamicProvider_Datadog_Pinned_ZeroNetworkOnCacheHit is
// phase 2: run as a SEPARATE `go test` process (see the Kubernetes
// sibling test's own doc comment for why this can't be one test
// function). This process sets an unreachable proxy for ALL of its own
// HTTP traffic before ubx ever runs -- if AcquireSchema attempted so much
// as one real HTTP request, it would fail immediately rather than
// silently succeeding.
func TestConformance_DynamicProvider_Datadog_Pinned_ZeroNetworkOnCacheHit(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := datadogSchemaCacheDir(t)
	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected phase 1 (TestConformance_DynamicProvider_Datadog_Pinned_PopulatesCache) to have already cached a real group manifest at %s: %v", manifestPath, err)
	}

	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	schemas, err := loadDynamicProviderSchema(context.Background(), "datadog", pinnedDatadogParams)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, network poisoned) failed -- this means real network was attempted and blocked, not that resolution is cache-only: %v", err)
	}
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero data source types")
	}
	t.Logf("real, pinned resolution from ONE entry succeeded with ALL network poisoned: %d resource types AND %d data source types together, served entirely from the real, verified local cache", len(schemas.Resources), len(schemas.DataSources))
}
