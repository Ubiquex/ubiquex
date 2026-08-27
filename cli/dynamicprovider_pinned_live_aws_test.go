// UBI-193's own real, final proof for AWS, mirroring
// dynamicprovider_pinned_live_google_test.go's own proof exactly: a
// single [providers.aws] pin resolves ALL 430 real members of the
// group together, against the real, live
// github.com/Ubiquex/ubx-schema-aws release, with zero schema_url
// network at resolution time. AWS is the only real mixed-source group
// among this org's six real providers (1 CloudFormation resource
// member, 429 Smithy data-source members) and the first real test of
// internal/mixedserver's own dispatch layer against real data -- so
// this test checks, beyond the usual real type-name checks, that
// resources and data sources each carry their own real, exact,
// per-source count (1,715 CloudFormation resources, 4,884 Smithy data
// sources), proving both real sources served together rather than one
// silently winning or the merge silently dropping one side. Gated
// behind UBX_CONFORMANCE_LIVE, matching every other real-network-
// touching test in this codebase -- go test ./... stays hermetic and
// credential-free everywhere else.
package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// awsSchemaCacheDir is provider.AcquireSchema's own real, documented
// cache location for this exact pinned group.
func awsSchemaCacheDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".ubx", "schemas", "ubiquex", "aws", "1.0.0")
}

// pinnedAWSParams is the real, ONLY [providers.aws] entry a real stack
// needs -- source = "ubiquex/aws" resolves to
// github.com/Ubiquex/ubx-schema-aws, version 1.0.0 is the real, live
// GitHub Release cut by that repo's own publish.yml. No separate entry
// for CloudFormation vs Smithy exists or is needed -- the launched
// process resolves and merges all 430 real members from this one pin,
// routed by internal/mixedserver to whichever real sub-server actually
// owns each type.
var pinnedAWSParams = map[string]any{
	"source":  "ubiquex/aws",
	"version": "1.0.0",
}

// wantAWSResourceTypes are real, hand-picked, stable CloudFormation-
// sourced resource type names -- core AWS concepts unlikely to
// disappear between this checkpoint and whenever this test next runs.
var wantAWSResourceTypes = []string{
	"aws_s3_bucket",
	"aws_iam_role",
	"aws_lambda_function",
}

// wantAWSDataSourceTypes are real, hand-picked, stable Smithy-sourced
// data source type names -- confirmed present against the real,
// locally generated snapshot before writing this test, not guessed.
var wantAWSDataSourceTypes = []string{
	"aws_s3_bucket_location",
	"aws_sqs_queue_url",
	"aws_iam_roles",
}

// wantAWSResourceCount/wantAWSDataSourceCount are the real, exact
// counts confirmed at generation time (--dump-group-summary against
// the real, locally generated 430-member snapshot) -- CloudFormation's
// own real registry contributes every resource type, Smithy's own real
// service models contribute every data source type. Checking the EXACT
// count, not just nonzero, is what actually proves both real sources
// served together: a routing bug that silently dropped one source's
// own contribution, or double-counted a collision, would still pass a
// nonzero check but fail this one.
const (
	wantAWSResourceCount   = 1715
	wantAWSDataSourceCount = 4884
)

// TestConformance_DynamicProvider_AWS_Pinned_PopulatesCache is phase 1
// of the real, two-process proof: deletes any existing cache entry so
// this run proves a REAL first-time fetch from the real, live GitHub
// Release, then resolves the ONE pin and confirms it returns real
// schema shapes together (CloudFormation-sourced resources AND
// Smithy-sourced data sources) from a single call, checking actual
// type names AND exact per-source counts, not just nonzero. Real
// acquisition wall time is logged -- this is this arc's largest real
// archive extraction (430 members, ~203MB raw content).
func TestConformance_DynamicProvider_AWS_Pinned_PopulatesCache(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := awsSchemaCacheDir(t)
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatalf("clear existing cache at %s: %v", cacheDir, err)
	}

	start := time.Now()
	schemas, err := loadDynamicProviderSchema(context.Background(), "aws", pinnedAWSParams)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, aws): %v", err)
	}
	t.Logf("real, first-time acquisition (download + extract + merge 430 real members, CloudFormation + Smithy) took %s", elapsed)
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution returned zero data source types")
	}
	t.Logf("real, pinned resolution from ONE entry returned %d resource types AND %d data source types together", len(schemas.Resources), len(schemas.DataSources))

	if len(schemas.Resources) != wantAWSResourceCount {
		t.Errorf("resource type count = %d, want exactly %d (CloudFormation's own real count) -- a mismatch here means the mixed-source dispatch layer is not serving CloudFormation's own real contribution intact", len(schemas.Resources), wantAWSResourceCount)
	}
	if len(schemas.DataSources) != wantAWSDataSourceCount {
		t.Errorf("data source type count = %d, want exactly %d (Smithy's own real count) -- a mismatch here means the mixed-source dispatch layer is not serving Smithy's own real contribution intact", len(schemas.DataSources), wantAWSDataSourceCount)
	}

	requireDynamicProviderTypeNames(t, "resource", schemas.Resources, wantAWSResourceTypes, "")
	requireDynamicProviderTypeNames(t, "data source", schemas.DataSources, wantAWSDataSourceTypes, "")

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

// TestConformance_DynamicProvider_AWS_Pinned_ZeroNetworkOnCacheHit is
// phase 2: run as a SEPARATE `go test` process (see the Kubernetes
// sibling test's own doc comment for why this can't be one test
// function). This process sets an unreachable proxy for ALL of its own
// HTTP traffic before ubx ever runs -- if AcquireSchema attempted so
// much as one real HTTP request, it would fail immediately rather than
// silently succeeding. Real cache-hit resolution time (430 real
// members, zero network) is logged too, for comparison against phase
// 1's own real first-fetch time. Re-checks the same exact per-source
// counts as phase 1 -- proving the cache-hit path routes both real
// sources correctly too, not just the fresh-fetch path.
func TestConformance_DynamicProvider_AWS_Pinned_ZeroNetworkOnCacheHit(t *testing.T) {
	requireDynamicProviderPinLive(t)

	cacheDir := awsSchemaCacheDir(t)
	manifestPath := filepath.Join(cacheDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("expected phase 1 (TestConformance_DynamicProvider_AWS_Pinned_PopulatesCache) to have already cached a real group manifest at %s: %v", manifestPath, err)
	}

	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")

	start := time.Now()
	schemas, err := loadDynamicProviderSchema(context.Background(), "aws", pinnedAWSParams)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("loadDynamicProviderSchema (pinned, network poisoned) failed -- this means real network was attempted and blocked, not that resolution is cache-only: %v", err)
	}
	t.Logf("real cache-hit resolution (430 real members, zero network) took %s", elapsed)
	if len(schemas.Resources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero resource types")
	}
	if len(schemas.DataSources) == 0 {
		t.Fatal("pinned resolution with network poisoned returned zero data source types")
	}
	if len(schemas.Resources) != wantAWSResourceCount {
		t.Errorf("resource type count = %d, want exactly %d", len(schemas.Resources), wantAWSResourceCount)
	}
	if len(schemas.DataSources) != wantAWSDataSourceCount {
		t.Errorf("data source type count = %d, want exactly %d", len(schemas.DataSources), wantAWSDataSourceCount)
	}
	t.Logf("real, pinned resolution from ONE entry succeeded with ALL network poisoned: %d resource types AND %d data source types together, served entirely from the real, verified local cache", len(schemas.Resources), len(schemas.DataSources))
	requireDynamicProviderTypeNames(t, "resource", schemas.Resources, wantAWSResourceTypes, "")
	requireDynamicProviderTypeNames(t, "data source", schemas.DataSources, wantAWSDataSourceTypes, "")
}
