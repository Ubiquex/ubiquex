// UBI-182's own real, final proof: a single [providers.kubernetes] pin
// resolves BOTH real members of the group (kubernetes resource mode,
// kubernetes_ds data-source mode) together, against the real, live
// github.com/Ubiquex/ubx-schema-kubernetes release, with zero
// schema_url network at resolution time -- the whole reason this
// mechanism exists (see provider/acquireschema.go's own doc comment).
// The resource/data-source split is a real, internal discovery-time
// detail (ubx-provider-dynamic's own internal/snapshot.MergeOpenAPIGroup
// merges every real member of the group into one served schema before
// this test ever sees it) -- a user never needs a second
// [providers.kubernetes_ds] entry. Gated behind UBX_CONFORMANCE_LIVE,
// matching every other real-network-touching test in this codebase
// (conformance.RequireLive) -- go test ./... stays hermetic and
// credential-free everywhere else.
//
// Checks actual type names, not just counts -- the real, live gap this
// test's own earlier version had: v2.0.0's published release served
// 116 data sources, the CORRECT count, every one of them wrong-prefixed
// (kubernetes_ds_ instead of kubernetes_). A count-only check passed
// against that release for real, live, undetected -- requireDynamicProviderTypeNames
// (below) checks a real, hand-picked set of stable type names are
// actually present under their correct names, AND that no returned name
// anywhere carries the wrong prefix, so this exact bug shape cannot
// slip through silently again.
package cli

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/provider"
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
// documented convention, not a private implementation detail.
func kubernetesSchemaCacheDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".ubx", "schemas", "ubiquex", "kubernetes", "3.0.1")
}

// pinnedKubernetesParams is the real, ONLY [providers.kubernetes] entry
// a real stack needs -- source = "ubiquex/kubernetes" resolves to
// github.com/Ubiquex/ubx-schema-kubernetes (provider/schemasource.go's
// own schemaRepoPrefix). version 3.0.1 is UBI-194's own real
// MinBinaryVersion-only regeneration (3.0.0 -> 3.0.1, no member content
// changed -- see internal/snapshot.AssembleGroup's own forced
// Patch-bump-on-MinBinaryVersion-transition doc comment,
// ubx-provider-dynamic), the first real Kubernetes release to carry a
// real min_binary_version so the bootstrap fallback (schema_format 3 ->
// "1.0.0") no longer fires for it. Before that: version 3.0.0 was UBI-182's
// own real resource/data-source collapse landing here twice over: the
// driving config that generates this group collapses from two
// [dynamic_providers.*] tables to one (ubx-schema-kubernetes#5's own
// doc comment has the full account), AND it's the first real release to
// actually carry the wire_name fix that corrected kubernetes_ds's own
// type-name prefixes -- v2.0.0's real, published release asset kept
// serving the wrong ones the whole time (#4 fixed the repo's own
// committed content but was never followed by a real republish, caught
// while regenerating for this collapse, not before). No separate
// "kubernetes_ds" entry exists or is needed -- the launched process
// resolves and merges BOTH real members from this one pin.
var pinnedKubernetesParams = map[string]any{
	"source":  "ubiquex/kubernetes",
	"version": "3.0.1",
}

// wantKubernetesResourceTypes/wantKubernetesDataSourceTypes are real,
// hand-picked, stable type names -- core Kubernetes concepts that have
// existed for years and are in no real danger of disappearing between
// this checkpoint and whenever this test next runs. The real, live bug
// this check exists to catch (v2.0.0's own published release silently
// serving kubernetes_ds_-prefixed data source names instead of the
// intended, shared kubernetes_ prefix) would NOT have been caught by
// counting alone -- the wrong-prefixed release still returned the
// correct COUNT, 116, every time. requireDynamicProviderTypeNames below
// checks both that these specific real names are present AND that no
// returned name anywhere carries the wrong prefix -- the actual,
// structural shape of the bug, not just its cardinality.
var wantKubernetesResourceTypes = []string{
	"kubernetes_core_pod",
	"kubernetes_core_service",
	"kubernetes_core_namespace",
	"kubernetes_core_config_map",
	"kubernetes_apps_deployment",
}

var wantKubernetesDataSourceTypes = []string{
	"kubernetes_core_pod",
	"kubernetes_core_pod_list",
	"kubernetes_core_namespace",
	"kubernetes_core_namespace_list",
}

// requireDynamicProviderTypeNames is the real, shared check both
// Kubernetes' and Datadog's own pinned live tests use: every name in
// want must actually be a key of got, and (when forbiddenPrefix is
// non-empty) no key of got may carry that prefix at all, except any
// real, already-known, legitimate name listed in allowedExceptions
// (Datadog's own real self-disambiguated datadog_v2_event_response,
// for instance -- a real name that legitimately carries what would
// otherwise be treated as a wrong-identity-leak prefix). kind is used
// only for the failure message.
func requireDynamicProviderTypeNames(t *testing.T, kind string, got map[string]*provider.Schema, want []string, forbiddenPrefix string, allowedExceptions ...string) {
	t.Helper()
	var missing []string
	for _, name := range want {
		if _, ok := got[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("expected %s type(s) not found in the real, pinned resolution: %v", kind, missing)
	}
	if forbiddenPrefix == "" {
		return
	}
	allowed := make(map[string]bool, len(allowedExceptions))
	for _, n := range allowedExceptions {
		allowed[n] = true
	}
	var wrong []string
	for name := range got {
		if strings.HasPrefix(name, forbiddenPrefix) && !allowed[name] {
			wrong = append(wrong, name)
		}
	}
	if len(wrong) > 0 {
		sort.Strings(wrong)
		t.Errorf("%d %s type name(s) carry the wrong %q prefix -- exactly the real, live class of bug this check exists to catch (v2.0.0's own release served this shape silently): %v", len(wrong), kind, forbiddenPrefix, wrong)
	}
}

// TestConformance_DynamicProvider_Kubernetes_Pinned_PopulatesCache is
// phase 1 of the real, two-process proof (see phase 2's own doc comment
// for why this can't be one test function): deletes any existing cache
// entry so this run proves a REAL first-time fetch from the real, live
// GitHub Release -- downloading and extracting the real snapshot.tar.gz
// that release actually carries, not a locally-built stand-in -- then
// resolves the ONE pin and confirms it returns BOTH real schema shapes
// together (resources AND data sources) from a single call.
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
		t.Fatal("pinned resolution returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution returned zero data source types -- the merge should have included kubernetes_ds too, from the SAME single pin")
	}
	t.Logf("real, pinned resolution from ONE entry returned %d resource types AND %d data source types together", len(schemas.Resources), len(schemas.DataSources))
	requireDynamicProviderTypeNames(t, "resource", schemas.Resources, wantKubernetesResourceTypes, "kubernetes_ds_")
	requireDynamicProviderTypeNames(t, "data source", schemas.DataSources, wantKubernetesDataSourceTypes, "kubernetes_ds_")

	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected a real, verified group manifest cached at %s after a real fetch+extract from the live release: %v", manifestPath, err)
	}
	t.Logf("real cache populated at %s (extracted from the real, live snapshot.tar.gz release asset)", manifestPath)
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
// silently succeeding. The one pin succeeding here, with both real
// schema shapes intact, is real, observed proof of zero network at
// schema resolution, not an inference from the config's own shape.
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
		t.Fatalf("loadDynamicProviderSchema (pinned, network poisoned) failed -- this means real network was attempted and blocked, not that resolution is cache-only: %v", err)
	}
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero data source types")
	}
	t.Logf("real, pinned resolution from ONE entry succeeded with ALL network poisoned: %d resource types AND %d data source types together, served entirely from the real, verified local cache", len(schemas.Resources), len(schemas.DataSources))
	requireDynamicProviderTypeNames(t, "resource", schemas.Resources, wantKubernetesResourceTypes, "kubernetes_ds_")
	requireDynamicProviderTypeNames(t, "data source", schemas.DataSources, wantKubernetesDataSourceTypes, "kubernetes_ds_")
}
