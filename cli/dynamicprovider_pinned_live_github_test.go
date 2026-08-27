// UBI-182's own real, final proof for GitHub, mirroring
// dynamicprovider_pinned_live_test.go's own Kubernetes proof exactly: a
// single [providers.github] pin resolves BOTH real members of the group
// (github resource mode, github_ds data-source mode) together, against
// the real, live github.com/Ubiquex/ubx-schema-github release, with
// zero schema_url network at resolution time. Checks actual type names,
// not just counts, from the start -- this repo never carried the
// count-only gap Kubernetes' own earlier verification had (see that
// file's own doc comment for the real bug that gap let through). Gated
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

// githubSchemaCacheDir is provider.AcquireSchema's own real, documented
// cache location for this exact pinned group.
func githubSchemaCacheDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".ubx", "schemas", "ubiquex", "github", "1.0.0")
}

// pinnedGithubParams is the real, ONLY [providers.github] entry a real
// stack needs -- source = "ubiquex/github" resolves to
// github.com/Ubiquex/ubx-schema-github, version 1.0.0 is the real, live
// GitHub Release cut by that repo's own publish.yml. No separate
// github_ds entry exists or is needed -- the launched process resolves
// and merges both real members from this one pin.
var pinnedGithubParams = map[string]any{
	"source":  "ubiquex/github",
	"version": "1.0.0",
}

// wantGithubResourceTypes/wantGithubDataSourceTypes are real,
// hand-picked, stable type names -- core GitHub REST concepts unlikely
// to disappear between this checkpoint and whenever this test next
// runs.
var wantGithubResourceTypes = []string{
	"github_full_repository",
	"github_issue",
	"github_issue_comment",
}

var wantGithubDataSourceTypes = []string{
	"github_repo",
	"github_issue",
	"github_user",
}

// TestConformance_DynamicProvider_Github_Pinned_PopulatesCache is phase
// 1 of the real, two-process proof: deletes any existing cache entry so
// this run proves a REAL first-time fetch from the real, live GitHub
// Release, then resolves the ONE pin and confirms it returns BOTH real
// schema shapes together (resources AND data sources) from a single
// call, checking actual type names, not just counts.
func TestConformance_DynamicProvider_Github_Pinned_PopulatesCache(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := githubSchemaCacheDir(t)
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatalf("clear existing cache at %s: %v", cacheDir, err)
	}

	schemas, err := loadDynamicProviderSchema(context.Background(), "github", pinnedGithubParams)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, github): %v", err)
	}
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution returned zero data source types -- the merge should have included github_ds too, from the SAME single pin")
	}
	t.Logf("real, pinned resolution from ONE entry returned %d resource types AND %d data source types together", len(schemas.Resources), len(schemas.DataSources))
	requireDynamicProviderTypeNames(t, "resource", schemas.Resources, wantGithubResourceTypes, "github_ds_")
	requireDynamicProviderTypeNames(t, "data source", schemas.DataSources, wantGithubDataSourceTypes, "github_ds_")

	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected a real, verified group manifest cached at %s after a real fetch+extract from the live release: %v", manifestPath, err)
	}
	t.Logf("real cache populated at %s (extracted from the real, live snapshot.tar.gz release asset)", manifestPath)
}

// TestConformance_DynamicProvider_Github_Pinned_ZeroNetworkOnCacheHit is
// phase 2: run as a SEPARATE `go test` process (see the Kubernetes
// sibling test's own doc comment for why this can't be one test
// function). This process sets an unreachable proxy for ALL of its own
// HTTP traffic before ubx ever runs -- if AcquireSchema attempted so
// much as one real HTTP request, it would fail immediately rather than
// silently succeeding.
func TestConformance_DynamicProvider_Github_Pinned_ZeroNetworkOnCacheHit(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := githubSchemaCacheDir(t)
	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected phase 1 (TestConformance_DynamicProvider_Github_Pinned_PopulatesCache) to have already cached a real group manifest at %s: %v", manifestPath, err)
	}

	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	schemas, err := loadDynamicProviderSchema(context.Background(), "github", pinnedGithubParams)
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
	requireDynamicProviderTypeNames(t, "resource", schemas.Resources, wantGithubResourceTypes, "github_ds_")
	requireDynamicProviderTypeNames(t, "data source", schemas.DataSources, wantGithubDataSourceTypes, "github_ds_")
}
