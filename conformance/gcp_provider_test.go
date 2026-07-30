package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/provider"
)

// gcpProviderVersion is pinned, not "latest" (see docs/architecture.md —
// provider acquisition, UBI-8: explicit version pins only). Bump
// deliberately if a newer hashicorp/google release needs conformance
// coverage.
const gcpProviderVersion = "7.40.0"

// TestGCPProvider_AcquireAndProtocolVersion is UBI-21 Stage 1's
// hermetic-except-for-network provider-layer verification
// (docs/architecture.md — GCP support): acquires the real
// hashicorp/google binary (registry.opentofu.org, checksum-verified --
// the same path hashicorp/aws already uses), launches it, and pulls its
// schema. No GCP account or credentials are touched at all -- Configure/
// ReadResource are never called, only GetProviderSchema, the same
// "free, no credentials" standard conformance.Registry's own
// IdentityFields already hold to.
//
// Gated behind RequireLive/UBX_CONFORMANCE_LIVE=1 like every other
// conformance test in this package, even though this one specifically
// doesn't need a GCP account -- it does need real network access to
// registry.opentofu.org, which every other UBX_CONFORMANCE_LIVE=1 test
// also implicitly requires, and go test ./... stays fully offline-safe
// without it (see CLAUDE.md: "Live tests are gated behind env vars").
//
// Empirical finding, asserted here rather than just noted in docs:
// hashicorp/google negotiates tfplugin v5, the same version
// hashicorp/aws was found to speak in Slice 1 -- dual v5/v6 support earns
// its keep a second time.
func TestGCPProvider_AcquireAndProtocolVersion(t *testing.T) {
	RequireLive(t)

	src, err := provider.ParseSource("hashicorp/google")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	result, err := provider.Acquire(ctx, src, gcpProviderVersion)
	if err != nil {
		t.Fatalf("acquire hashicorp/google: %v", err)
	}

	client, err := provider.Launch(ctx, result.Path)
	if err != nil {
		t.Fatalf("launch hashicorp/google: %v", err)
	}
	defer client.Close()

	if got := client.Provider.ProtocolVersion(); got != 5 {
		t.Errorf("hashicorp/google negotiated protocol v%d, want v5 (matching hashicorp/aws -- if this changed, dual v5/v6 support just earned its keep a third time, update docs/architecture.md rather than just this assertion)", got)
	}

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		t.Fatalf("fetch schema: %v", err)
	}
	if len(schemas.Resources) < 500 {
		t.Errorf("hashicorp/google reports only %d resource types, want at least 500 (sanity check against a truncated/wrong schema response)", len(schemas.Resources))
	}

	// Spot-check a handful of the ~40 types docs/plan.md's GCP resource
	// type list names, one per category -- not exhaustive (the full list
	// was verified once, empirically, while building this list; see
	// STATE.md), but enough to catch a provider version bump that quietly
	// renames or drops one of these before conformance.Registry's own
	// entries silently go stale.
	for _, typ := range []string{
		"google_compute_instance",
		"google_compute_network",
		"google_service_account",
		"google_storage_bucket",
		"google_sql_database_instance",
		"google_dns_managed_zone",
		"google_pubsub_topic",
	} {
		if _, ok := schemas.Resources[typ]; !ok {
			t.Errorf("expected %s in hashicorp/google's schema, not found", typ)
		}
	}
}
