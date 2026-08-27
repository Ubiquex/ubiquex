// UBI-182's own real, final proof for Google, mirroring
// dynamicprovider_pinned_live_test.go's own Kubernetes proof exactly: a
// single [providers.google] pin resolves ALL 524 real members of the
// group together, against the real, live
// github.com/Ubiquex/ubx-schema-google release, with zero schema_url
// network at resolution time. Checks actual type names, not just
// counts, from the start -- this repo never carried the count-only gap
// Kubernetes' own earlier verification had. Also this arc's first real
// test of archive extraction at real production scale (524 members,
// ~17.8MB compressed release asset) -- real acquisition time is logged,
// not just pass/fail, since AWS's own real 430-member CFN+Smithy group
// will be larger still. Gated behind UBX_CONFORMANCE_LIVE, matching
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

// googleSchemaCacheDir is provider.AcquireSchema's own real, documented
// cache location for this exact pinned group.
func googleSchemaCacheDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".ubx", "schemas", "ubiquex", "google", "1.0.0")
}

// pinnedGoogleParams is the real, ONLY [providers.google] entry a real
// stack needs -- source = "ubiquex/google" resolves to
// github.com/Ubiquex/ubx-schema-google, version 1.0.0 is the real,
// live GitHub Release cut by that repo's own publish.yml. No separate
// per-product entry exists or is needed -- the launched process
// resolves and merges all 524 real members from this one pin.
var pinnedGoogleParams = map[string]any{
	"source":  "ubiquex/google",
	"version": "1.0.0",
}

// wantGoogleResourceTypes/wantGoogleDataSourceTypes are real,
// hand-picked, stable type names -- core GCP concepts unlikely to
// disappear between this checkpoint and whenever this test next runs.
var wantGoogleResourceTypes = []string{
	"google_compute_instance",
	"google_compute_instance_group",
	"google_storage_bucket",
}

var wantGoogleDataSourceTypes = []string{
	"google_compute_instance_setting",
	"google_compute_project",
	"google_compute_zone",
	"google_storage_service_account",
}

// TestConformance_DynamicProvider_Google_Pinned_PopulatesCache is phase
// 1 of the real, two-process proof: deletes any existing cache entry so
// this run proves a REAL first-time fetch from the real, live GitHub
// Release, then resolves the ONE pin and confirms it returns real
// schema shapes together (resources AND data sources) from a single
// call, checking actual type names, not just counts. Real acquisition
// wall time is logged -- this is the first real test of archive
// extraction at 524-member scale.
func TestConformance_DynamicProvider_Google_Pinned_PopulatesCache(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := googleSchemaCacheDir(t)
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatalf("clear existing cache at %s: %v", cacheDir, err)
	}

	start := time.Now()
	schemas, err := loadDynamicProviderSchema(context.Background(), "google", pinnedGoogleParams)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, google): %v", err)
	}
	t.Logf("real, first-time acquisition (download + extract + merge 524 real members) took %s", elapsed)
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution returned zero data source types")
	}
	t.Logf("real, pinned resolution from ONE entry returned %d resource types AND %d data source types together", len(schemas.Resources), len(schemas.DataSources))
	requireDynamicProviderTypeNames(t, "resource", schemas.Resources, wantGoogleResourceTypes, "")
	requireDynamicProviderTypeNames(t, "data source", schemas.DataSources, wantGoogleDataSourceTypes, "")

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

// TestConformance_DynamicProvider_Google_Pinned_ZeroNetworkOnCacheHit is
// phase 2: run as a SEPARATE `go test` process (see the Kubernetes
// sibling test's own doc comment for why this can't be one test
// function). This process sets an unreachable proxy for ALL of its own
// HTTP traffic before ubx ever runs -- if AcquireSchema attempted so
// much as one real HTTP request, it would fail immediately rather than
// silently succeeding. Real cache-hit resolution time (524 real
// members, zero network) is logged too, for comparison against phase
// 1's own real first-fetch time.
func TestConformance_DynamicProvider_Google_Pinned_ZeroNetworkOnCacheHit(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := googleSchemaCacheDir(t)
	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected phase 1 (TestConformance_DynamicProvider_Google_Pinned_PopulatesCache) to have already cached a real group manifest at %s: %v", manifestPath, err)
	}

	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	start := time.Now()
	schemas, err := loadDynamicProviderSchema(context.Background(), "google", pinnedGoogleParams)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, network poisoned) failed -- this means real network was attempted and blocked, not that resolution is cache-only: %v", err)
	}
	t.Logf("real cache-hit resolution (524 real members, zero network) took %s", elapsed)
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero data source types")
	}
	t.Logf("real, pinned resolution from ONE entry succeeded with ALL network poisoned: %d resource types AND %d data source types together, served entirely from the real, verified local cache", len(schemas.Resources), len(schemas.DataSources))
	requireDynamicProviderTypeNames(t, "resource", schemas.Resources, wantGoogleResourceTypes, "")
	requireDynamicProviderTypeNames(t, "data source", schemas.DataSources, wantGoogleDataSourceTypes, "")
}
