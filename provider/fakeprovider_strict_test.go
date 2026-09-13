package provider

import (
	"context"
	"testing"
	"time"
)

// fakeprovider_strict_test.go locks the strict-v6 fixture's own
// narrowness (UBI-252).
//
// Four times, a hermetic fixture accepted something the real API
// rejects, and each blind spot was shaped exactly like the fixture's
// generosity. The permissive ok-v6 fixture was written to make the code
// under test succeed, which is the natural thing to do for a happy
// path, and the result accepts a superset of what a real API accepts.
// Every place the superset is strictly larger is invisible precisely
// because the tests pass.
//
// strict-v6 models the real narrowness instead, and these tests exist
// so nobody quietly widens it back. A fixture that grows a convenient
// "id" is no longer strict, and the failure that would reintroduce is
// silent by construction, so it is asserted rather than trusted.
//
// This is deliberately NOT a claim that fixtures match reality in
// general. Knowing which dimension a fake is generous in comes only
// from contact with the real API. What this buys is that each such
// discovery, once made, stays made.

func strictSchemas(t *testing.T) *Schemas {
	t.Helper()
	client, err := launchFake(t, "strict-v6")
	if err != nil {
		t.Fatalf("Launch strict-v6: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		t.Fatalf("Schema: %v", err)
	}
	return schemas
}

// A real ubx dynamic provider declares an EMPTY provider block and its
// ConfigureProvider is a no-op. ok-v6 declares an optional "region",
// which let `ubx init --region` write a provider_configs entry no
// dynamic provider can accept: it planned clean and failed at ship.
func TestStrictV6_ProviderBlockIsEmpty(t *testing.T) {
	schemas := strictSchemas(t)
	if schemas.Provider == nil {
		t.Fatal("a real provider serves a provider block with no attributes, which is not the same as serving no block at all")
	}
	if n := len(schemas.Provider.Block.Attributes); n != 0 {
		var names []string
		for _, a := range schemas.Provider.Block.Attributes {
			names = append(names, a.Name)
		}
		t.Errorf("strict-v6's provider block declares %d attribute(s) %v -- it models a provider that accepts no configuration at all, and widening it re-opens the gap that shipped a provider_configs entry nothing could accept", n, names)
	}
	if n := len(schemas.Provider.Block.NestedBlocks); n != 0 {
		t.Errorf("strict-v6's provider block declares %d nested block(s), same reason", n)
	}
}

// Measured against the real AWS snapshot ubx ships: 0 of 1687
// dynamic-provider resource types declare an "id". ok-v6's fake_widget
// does, and that hid the lookup-key derivation entirely: 10% of AWS
// types recorded no lookup key and 53% recorded one that could not
// re-find the resource, leaving them undeletable and outside drift
// detection.
func TestStrictV6_NoResourceDeclaresAnID(t *testing.T) {
	schemas := strictSchemas(t)
	if len(schemas.Resources) == 0 {
		t.Fatal("strict-v6 serves no resources at all, so it asserts nothing")
	}
	for name, s := range schemas.Resources {
		for _, a := range s.Block.Attributes {
			if a.Name == "id" {
				t.Errorf("%s declares an \"id\": 0 of 1687 real AWS types do, and a fixture that has one can never reach the derivation path every real resource takes", name)
			}
		}
	}
}

// 14% of real resource types declare no required attribute at all, and
// that is the case where lookup derivation has nothing whatsoever to
// work with. A fixture where every resource has both an id and a
// required name cannot reach it.
func TestStrictV6_OneResourceHasNoRequiredAttributeAtAll(t *testing.T) {
	schemas := strictSchemas(t)

	found := false
	for name, s := range schemas.Resources {
		required := 0
		for _, a := range s.Block.Attributes {
			if a.Required {
				required++
			}
		}
		if required == 0 {
			found = true
			t.Logf("%s has no required attribute, which is the 14%% case", name)
		}
	}
	if !found {
		t.Error("no strict-v6 resource has zero required attributes -- the hardest lookup-derivation case is then unreachable, which is exactly how it stayed unnoticed before")
	}
}

// The strict fixture has to remain a plausible provider, not merely a
// narrow one: something a real client can still talk to.
func TestStrictV6_IsStillAWorkingProvider(t *testing.T) {
	schemas := strictSchemas(t)
	if len(schemas.Resources) < 2 {
		t.Errorf("want at least two resource types (one with a required attribute, one without), got %d", len(schemas.Resources))
	}
	for name, s := range schemas.Resources {
		if len(s.Block.Attributes) == 0 {
			t.Errorf("%s declares no attributes at all, which is narrower than any real resource and tests nothing useful", name)
		}
	}
}
