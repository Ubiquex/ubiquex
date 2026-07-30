package conformance

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/provider"
)

// TestProbeSchema_RealProviders runs the full hermetic tier (ProbeSchema)
// against every provider this project already has a pinned, cached-or-
// acquirable real schema for — aws/gcp/azurerm/kubernetes/helm — proving
// the probe generator is genuinely provider-agnostic (docs/
// conformance-harness.md's own central claim) against real schemas, not
// just the small hand-built fixtures probe_test.go uses.
//
// Gated behind RequireLive/UBX_CONFORMANCE_LIVE=1 for the identical
// reason gcp_provider_test.go/azure_provider_test.go already are: this
// needs real network access to registry.opentofu.org to acquire a
// not-yet-locally-cached provider version, not a cloud account or
// credentials — Configure/ReadResource are never called, only
// GetProviderSchema, so nothing here is live-tier work (see
// docs/conformance-harness.md's own "Hermetic tier" section: the ONLY
// live-tier exception the hermetic tier ever needs is this same network
// fetch, and only once per provider version, ever, after which
// provider.Acquire's own local cache makes every later run fully
// offline).
func TestProbeSchema_RealProviders(t *testing.T) {
	RequireLive(t)

	cases := []struct {
		source  string
		version string
		path    func(t *testing.T) string
	}{
		{"hashicorp/aws", awsProviderVersion, realAWSProviderPath},
		{"hashicorp/google", gcpProviderVersion, realGCPProviderPath},
		{"hashicorp/azurerm", azurermProviderVersion, realAzureProviderPath},
		{"hashicorp/kubernetes", k8sProviderVersion, realK8sProviderPath},
		{"hashicorp/helm", helmProviderVersion, realHelmProviderPath},
	}

	for _, c := range cases {
		t.Run(c.source, func(t *testing.T) {
			path := c.path(t)
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			client, err := provider.Launch(ctx, path)
			if err != nil {
				t.Fatalf("launch %s: %v", c.source, err)
			}
			defer client.Close()

			schemas, err := client.Provider.Schema(ctx)
			if err != nil {
				t.Fatalf("fetch schema for %s: %v", c.source, err)
			}
			if len(schemas.Resources) == 0 {
				t.Fatalf("%s reported zero resource types -- sanity check against a truncated/wrong schema response", c.source)
			}

			findings := ProbeSchema(c.source, c.version, schemas)

			// Determinism, asserted directly against a REAL schema, not
			// just the hand-built fixtures probe_test.go covers --
			// running the same real schema through ProbeSchema twice
			// must produce byte-identical output (CLAUDE.md's own
			// standing determinism rule, and exactly the property
			// "rerun on version bump" needs to diff against later).
			again := ProbeSchema(c.source, c.version, schemas)
			if len(findings) != len(again) {
				t.Fatalf("%s: non-deterministic finding count across two runs of the same schema: %d vs %d", c.source, len(findings), len(again))
			}
			for i := range findings {
				if findings[i] != again[i] {
					t.Fatalf("%s: non-deterministic finding at index %d: %+v vs %+v", c.source, i, findings[i], again[i])
				}
			}

			reportFindingSummary(t, c.source, c.version, len(schemas.Resources), findings)
		})
	}
}

// reportFindingSummary logs a real, honest per-class/per-confidence
// tally — never asserting a specific total count (a provider version
// bump changing counts slightly is expected and healthy, not a
// regression); see TestProbeSchema_RealProviders_KnownFindings, below,
// for the real spot-checks against hand-verified ground truth.
func reportFindingSummary(t *testing.T, source, version string, typeCount int, findings []Finding) {
	t.Helper()
	byClass := map[FindingClass]int{}
	byConfidence := map[Confidence]int{}
	for _, f := range findings {
		byClass[f.Class]++
		byConfidence[f.Confidence]++
	}

	var classes []string
	for c := range byClass {
		classes = append(classes, string(c))
	}
	sort.Strings(classes)
	var summary string
	for _, c := range classes {
		summary += fmt.Sprintf(" %s=%d", c, byClass[FindingClass(c)])
	}
	t.Logf("%s@%s: %d resource types, %d findings (%d confirmed, %d candidate):%s",
		source, version, typeCount, len(findings), byConfidence[Confirmed], byConfidence[Candidate], summary)
}

// TestProbeSchema_RealProviders_KnownFindings spot-checks the hermetic
// tier against real, hand-verified ground truth this project already
// has on file for specific types — proving the mechanism reproduces
// known-real findings, not just that it runs without error against a
// real schema.
func TestProbeSchema_RealProviders_KnownFindings(t *testing.T) {
	RequireLive(t)

	t.Run("hashicorp/helm: helm_release.metadata.notes flagged as a sensitive-echo candidate", func(t *testing.T) {
		path := realHelmProviderPath(t)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		client, err := provider.Launch(ctx, path)
		if err != nil {
			t.Fatalf("launch: %v", err)
		}
		defer client.Close()
		schemas, err := client.Provider.Schema(ctx)
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		rs, ok := schemas.Resources["helm_release"]
		if !ok {
			t.Fatal("helm_release not found in real hashicorp/helm schema")
		}
		findings := ProbeType("hashicorp/helm", helmProviderVersion, "helm_release", rs.Block)
		found := false
		for _, f := range findings {
			if f.Class == FindingSensitiveUnderflag {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected at least one sensitive-underflag candidate for helm_release (its own real metadata.notes/manifest fields, UBI-22/24's own hand-verified finding), got: %+v", findings)
		}
	})

	t.Run("hashicorp/azurerm: azurerm_resource_group has a recognized identity candidate", func(t *testing.T) {
		path := realAzureProviderPath(t)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		client, err := provider.Launch(ctx, path)
		if err != nil {
			t.Fatalf("launch: %v", err)
		}
		defer client.Close()
		schemas, err := client.Provider.Schema(ctx)
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		rs, ok := schemas.Resources["azurerm_resource_group"]
		if !ok {
			t.Fatal("azurerm_resource_group not found in real hashicorp/azurerm schema")
		}
		findings := ProbeType("hashicorp/azurerm", azurermProviderVersion, "azurerm_resource_group", rs.Block)
		for _, f := range findings {
			if f.Class == FindingIncompleteRead {
				t.Fatalf("expected NO incomplete-read finding for azurerm_resource_group -- it has both id and name (UBI-37's own hand-verified finding), got: %+v", f)
			}
		}
	})
}
