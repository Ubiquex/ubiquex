package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/provider"
)

// azurermProviderVersion is pinned, not "latest" (see docs/architecture.md
// — provider acquisition, UBI-8: explicit version pins only). Bump
// deliberately if a newer hashicorp/azurerm release needs conformance
// coverage.
const azurermProviderVersion = "5.0.0"

// azapiProviderVersion is assessed separately (UBI-37 Stage 1's own
// "assess azapi" line) -- a distinct registry namespace (azure/azapi, not
// hashicorp/), and a structurally different provider: azapi models EVERY
// Azure resource via a handful of generic types (azapi_resource,
// azapi_resource_action, ...) parameterized by an ARM resource type
// string and API version, rather than one native Go type per Azure
// resource the way azurerm does -- confirmed by inspecting its schema
// below (TestAzapiProvider_AcquireAndAssess), not assumed from its
// reputation. This makes azapi a poor fit for conformance.Registry's own
// (Source, Type)-keyed, one-entry-per-resource-type model: there is
// nothing analogous to azurerm's ~40 distinct resource types to seed --
// azapi's "coverage" is total by construction (every ARM resource type,
// via the same few generic types), so it earns its own short assessment
// test rather than 40 near-identical registry entries.
const azapiProviderVersion = "2.11.0"

// TestAzureProvider_AcquireAndProtocolVersion is UBI-37 Stage 1's
// hermetic-except-for-network provider-layer verification
// (docs/architecture.md — Azure support), mirroring
// TestGCPProvider_AcquireAndProtocolVersion exactly: acquires the real
// hashicorp/azurerm binary (registry.opentofu.org, checksum-verified),
// launches it, and pulls its schema. No Azure subscription or credentials
// are touched at all -- Configure/ReadResource are never called, only
// GetProviderSchema, the same "free, no credentials" standard every other
// platform's own conformance.Registry entries hold to.
//
// Gated behind RequireLive/UBX_CONFORMANCE_LIVE=1 like every other
// conformance test in this package, purely for the real network access to
// registry.opentofu.org this needs (see TestGCPProvider_
// AcquireAndProtocolVersion's own identical reasoning) -- go test ./...
// stays fully offline-safe without it.
//
// Empirical finding, asserted here rather than just noted in docs:
// hashicorp/azurerm negotiates tfplugin v5, the same version
// hashicorp/aws and hashicorp/google were both found to speak -- dual
// v5/v6 support earns its keep a fourth time.
func TestAzureProvider_AcquireAndProtocolVersion(t *testing.T) {
	RequireLive(t)

	src, err := provider.ParseSource("hashicorp/azurerm")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	result, err := provider.Acquire(ctx, src, azurermProviderVersion)
	if err != nil {
		t.Fatalf("acquire hashicorp/azurerm: %v", err)
	}

	client, err := provider.Launch(ctx, result.Path)
	if err != nil {
		t.Fatalf("launch hashicorp/azurerm: %v", err)
	}
	defer client.Close()

	if got := client.Provider.ProtocolVersion(); got != 5 {
		t.Errorf("hashicorp/azurerm negotiated protocol v%d, want v5 (matching hashicorp/aws and hashicorp/google -- if this changed, update docs/architecture.md rather than just this assertion)", got)
	}

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		t.Fatalf("fetch schema: %v", err)
	}
	if len(schemas.Resources) < 800 {
		t.Errorf("hashicorp/azurerm reports only %d resource types, want at least 800 (sanity check against a truncated/wrong schema response -- azurerm is the largest provider by resource-type count of any platform this project supports)", len(schemas.Resources))
	}

	// Spot-check the ~40 types docs/plan.md's Azure resource type list
	// names, one per category -- not exhaustive (the full list was
	// verified once, empirically, while building conformance/registry.go's
	// own Azure entries; see STATE.md), but enough to catch a provider
	// version bump that quietly renames or drops one of these before
	// conformance.Registry's own entries silently go stale.
	for _, typ := range []string{
		"azurerm_resource_group",
		"azurerm_linux_virtual_machine",
		"azurerm_virtual_network",
		"azurerm_storage_account",
		"azurerm_key_vault",
		"azurerm_kubernetes_cluster",
		"azurerm_mssql_database",
		"azurerm_dns_zone",
		"azurerm_monitor_metric_alert",
	} {
		if _, ok := schemas.Resources[typ]; !ok {
			t.Errorf("expected %s in hashicorp/azurerm's schema, not found", typ)
		}
	}
}

// TestAzapiProvider_AcquireAndAssess is UBI-37 Stage 1's "assess azapi"
// line: acquires the real azure/azapi binary and inspects its schema
// shape directly, rather than assuming from its documentation/reputation
// how it models Azure resources.
//
// Empirical finding: azapi's ResourceSchemas map is small (a handful of
// entries: azapi_resource, azapi_resource_action, azapi_update_resource,
// azapi_data_plane_resource, and their data-source counterparts) compared
// to azurerm's 800+ -- confirming the doc-comment claim above that azapi
// is a generic, ARM-type-parameterized provider rather than one Go type
// per Azure resource. azapi_resource's own schema models "type" (the ARM
// resource type + API version string, e.g.
// "Microsoft.Storage/storageAccounts@2023-01-01") and "body" (a
// dynamically-typed JSON blob of the resource's own ARM properties) as
// its primary attributes -- there is no fixed, enumerable set of
// per-resource-type attributes conformance.Registry's own IdentityFields/
// LookupHint model assumes; that model only fits azurerm.
func TestAzapiProvider_AcquireAndAssess(t *testing.T) {
	RequireLive(t)

	src, err := provider.ParseSource("azure/azapi")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	result, err := provider.Acquire(ctx, src, azapiProviderVersion)
	if err != nil {
		t.Fatalf("acquire azure/azapi: %v", err)
	}

	client, err := provider.Launch(ctx, result.Path)
	if err != nil {
		t.Fatalf("launch azure/azapi: %v", err)
	}
	defer client.Close()

	// Empirical finding, asserted here rather than assumed to match
	// azurerm: azure/azapi negotiates tfplugin v6, NOT v5 -- the first
	// provider this project has onboarded that doesn't speak v5. Every
	// other source (hashicorp/aws, hashicorp/google, hashicorp/azurerm
	// above) negotiates v5; azapi's own SDKv2-vs-plugin-framework
	// implementation choice is independent of azurerm's, and "assume
	// nothing" (this ticket's own Stage 1 instruction) paid off here --
	// a same-publisher-family assumption would have gotten this wrong.
	// Unsurprising in hindsight (the fakeprovider fixture already speaks
	// both v5 and v6, built for exactly this kind of divergence), but
	// confirmed, not assumed.
	if got := client.Provider.ProtocolVersion(); got != 6 {
		t.Errorf("azure/azapi negotiated protocol v%d, want v6 (this differs from every other provider source this project uses, including hashicorp/azurerm -- if this changed, update this comment along with the assertion)", got)
	}

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		t.Fatalf("fetch schema: %v", err)
	}
	if len(schemas.Resources) > 20 {
		t.Errorf("azure/azapi reports %d resource types, want a small handful (<20) -- confirms/refutes the doc comment's claim that azapi is a generic, ARM-type-parameterized provider rather than one type per resource; update this test's own comment if the provider's shape has genuinely changed", len(schemas.Resources))
	}
	if _, ok := schemas.Resources["azapi_resource"]; !ok {
		t.Errorf("expected azapi_resource (the provider's own generic ARM-resource type) in azure/azapi's schema, not found")
	}
}
