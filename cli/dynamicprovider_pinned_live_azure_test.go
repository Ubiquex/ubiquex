// UBI-193's own real, final proof for Azure, the sixth and last real
// provider, mirroring dynamicprovider_pinned_live_google_test.go's own
// proof exactly: a single [providers.azure] pin resolves ALL 604 real
// members of the group together, against the real, live
// github.com/Ubiquex/ubx-schema-azure release, with zero schema_url
// network at resolution time. Azure was the one real provider blocked
// on UBI-193 Part 1 (external $ref bundling) -- this is the first live
// proof that internal/openapi.Bundle's own real fix actually resolves
// for real, pinned use, not just at generation time. Specifically
// checks the network/virtualnetwork family, the real, live-confirmed
// worst case for both external refs (330 distinct external files
// referenced from virtualNetwork.json alone) and cyclic references
// (PublicIPAddress reaches itself through its own real
// linkedPublicIPAddress property) -- if bundling had left anything
// unresolved or produced a genuinely broken cycle, this is the family
// most likely to show it. Gated behind UBX_CONFORMANCE_LIVE, matching
// every other real-network-touching test in this codebase -- go test
// ./... stays hermetic and credential-free everywhere else.
package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// azureSchemaCacheDir is provider.AcquireSchema's own real, documented
// cache location for this exact pinned group.
func azureSchemaCacheDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".ubx", "schemas", "ubiquex", "azure", "1.0.0")
}

// pinnedAzureParams is the real, ONLY [providers.azure] entry a real
// stack needs -- source = "ubiquex/azure" resolves to
// github.com/Ubiquex/ubx-schema-azure, version 1.0.0 is the real, live
// GitHub Release cut by that repo's own publish.yml. No separate entry
// per resource provider exists or is needed -- the launched process
// resolves and merges all 604 real members from this one pin.
var pinnedAzureParams = map[string]any{
	"source":  "ubiquex/azure",
	"version": "1.0.0",
}

// wantAzureResourceTypes are real, hand-picked, stable resource type
// names -- core Azure concepts unlikely to disappear, including two
// from the real, previously-blocked network/virtualnetwork family
// specifically (confirmed present against the real, locally generated
// snapshot before writing this test, not guessed).
var wantAzureResourceTypes = []string{
	"azure_network_virtualnetwork_virtual_network",
	"azure_network_virtualnetwork_virtual_network_peering",
	"azure_sql_servers_server",
}

// wantAzureDataSourceTypes are real, hand-picked, stable data source
// type names, including one from the network/virtualnetwork family.
var wantAzureDataSourceTypes = []string{
	"azure_network_virtualnetwork_virtual_network_list_result",
	"azure_sql_servers_server_list_result",
}

// wantAzureResourceCount/wantAzureDataSourceCount are the real, exact
// counts confirmed at generation time (--dump-group-summary against
// the real, locally generated 604-member snapshot). Checking the EXACT
// count, not just nonzero, is what actually proves the real, full
// group -- including every previously-blocked, now-bundled member --
// resolves intact, not partially.
const (
	wantAzureResourceCount   = 1090
	wantAzureDataSourceCount = 2177
)

// TestConformance_DynamicProvider_Azure_Pinned_PopulatesCache is phase
// 1 of the real, two-process proof: deletes any existing cache entry so
// this run proves a REAL first-time fetch from the real, live GitHub
// Release, then resolves the ONE pin and confirms it returns real
// schema shapes together (resources AND data sources) from a single
// call, checking actual type names AND exact counts, not just nonzero.
// Real acquisition wall time is logged.
func TestConformance_DynamicProvider_Azure_Pinned_PopulatesCache(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := azureSchemaCacheDir(t)
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatalf("clear existing cache at %s: %v", cacheDir, err)
	}

	start := time.Now()
	schemas, err := loadDynamicProviderSchema(context.Background(), "azure", pinnedAzureParams)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, azure): %v", err)
	}
	t.Logf("real, first-time acquisition (download + extract + merge 604 real members, external refs already bundled) took %s", elapsed)
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution returned zero data source types")
	}
	t.Logf("real, pinned resolution from ONE entry returned %d resource types AND %d data source types together", len(schemas.Resources), len(schemas.DataSources))

	if len(schemas.Resources) != wantAzureResourceCount {
		t.Errorf("resource type count = %d, want exactly %d", len(schemas.Resources), wantAzureResourceCount)
	}
	if len(schemas.DataSources) != wantAzureDataSourceCount {
		t.Errorf("data source type count = %d, want exactly %d", len(schemas.DataSources), wantAzureDataSourceCount)
	}

	requireDynamicProviderTypeNames(t, "resource", schemas.Resources, wantAzureResourceTypes, "")
	requireDynamicProviderTypeNames(t, "data source", schemas.DataSources, wantAzureDataSourceTypes, "")

	manifestPath := filepath.Join(cacheDir, "manifest.json")
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("expected a real, verified group manifest cached at %s after a real fetch+extract from the live release: %v", manifestPath, err)
	}
	t.Logf("real cache populated at %s (extracted from the real, live snapshot.tar.gz release asset)", manifestPath)

	var totalSize int64
	var fileCount int
	err = filepath.Walk(cacheDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			totalSize += fi.Size()
			fileCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk real cache dir %s: %v", cacheDir, err)
	}
	t.Logf("real, extracted cache: %d files, %d bytes total (~%.1fMB) -- manifest itself: %d bytes", fileCount, totalSize, float64(totalSize)/1e6, info.Size())
}

// TestConformance_DynamicProvider_Azure_Pinned_ZeroNetworkOnCacheHit is
// phase 2: run as a SEPARATE `go test` process (see the Kubernetes
// sibling test's own doc comment for why this can't be one test
// function). This process sets an unreachable proxy for ALL of its own
// HTTP traffic before ubx ever runs -- if AcquireSchema attempted so
// much as one real HTTP request, it would fail immediately rather than
// silently succeeding. Real cache-hit resolution time (604 real
// members, zero network) is logged too. Re-checks the same exact
// counts and the same network/virtualnetwork type names as phase 1.
func TestConformance_DynamicProvider_Azure_Pinned_ZeroNetworkOnCacheHit(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := azureSchemaCacheDir(t)
	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected phase 1 (TestConformance_DynamicProvider_Azure_Pinned_PopulatesCache) to have already cached a real group manifest at %s: %v", manifestPath, err)
	}

	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	start := time.Now()
	schemas, err := loadDynamicProviderSchema(context.Background(), "azure", pinnedAzureParams)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, network poisoned) failed -- this means real network was attempted and blocked, not that resolution is cache-only: %v", err)
	}
	t.Logf("real cache-hit resolution (604 real members, zero network) took %s", elapsed)
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero data source types")
	}
	if len(schemas.Resources) != wantAzureResourceCount {
		t.Errorf("resource type count = %d, want exactly %d", len(schemas.Resources), wantAzureResourceCount)
	}
	if len(schemas.DataSources) != wantAzureDataSourceCount {
		t.Errorf("data source type count = %d, want exactly %d", len(schemas.DataSources), wantAzureDataSourceCount)
	}
	t.Logf("real, pinned resolution from ONE entry succeeded with ALL network poisoned: %d resource types AND %d data source types together, served entirely from the real, verified local cache", len(schemas.Resources), len(schemas.DataSources))
	requireDynamicProviderTypeNames(t, "resource", schemas.Resources, wantAzureResourceTypes, "")
	requireDynamicProviderTypeNames(t, "data source", schemas.DataSources, wantAzureDataSourceTypes, "")
}
