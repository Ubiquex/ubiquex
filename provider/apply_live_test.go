package provider

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// timeProviderVersion is pinned, not "latest" (docs/architecture.md --
// provider acquisition, UBI-8: explicit version pins only).
const timeProviderVersion = "0.9.2"

// requireLive skips t unless UBX_CONFORMANCE_LIVE=1 is set -- the same
// gate conformance.RequireLive uses (this package's own tests can't import
// conformance, which itself depends on provider -- that would be a cycle).
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_CONFORMANCE_LIVE") != "1" {
		t.Skip("skipping: set UBX_CONFORMANCE_LIVE=1 to run against a real acquired provider binary (network only, no cloud credentials)")
	}
}

// TestApplyResourceChange_RealProvider_TimeStatic is UBI-26's own empirical
// verification, run against a real provider binary rather than assumed
// safe from the fakeprovider fixture alone ("verify round-tripping against
// real schemas empirically, assume nothing"). hashicorp/time's time_static
// resource needs no cloud credentials and no network calls of its own at
// apply-time -- pure local computation -- so this is a genuine
// ApplyResourceChange round trip against a real SDKv2-vintage provider
// binary, safe in CI with only network access to acquire the binary
// itself (registry.opentofu.org), gated UBX_CONFORMANCE_LIVE=1 like every
// other network-touching test in this codebase.
//
// A real false start, worth recording rather than silently discarded: an
// earlier version of this test tried PriorState=null (a from-scratch
// create via ApplyResourceChange directly, no prior ReadResource) and
// found every computed attribute (day/hour/id/minute/month/second/unix/year)
// came back null, not computed -- an SDKv2-vintage provider's Apply only
// fills in a computed attribute it finds *unknown* in PlannedState (the
// marker a real PlanResourceChange call produces), and ubx's own
// encodeDynamicValue has no way to express "unknown", only "null" (an
// absent JSON key). This looked, briefly, like a real gap in "construct
// PlannedState without planning" (docs/executor.md). It isn't one:
// drift_revert never creates a resource from scratch -- it only ever
// modifies one whose PriorState came from a real, already-successful
// ReadResource call (core.ReadAndFingerprint, exactly what Ship's own loop
// does), which for any real, already-existing resource already carries
// every computed attribute's real, concrete value, never null. The test
// below models exactly that shape -- a realistic PriorState as a genuine
// ReadResource result would look, computed fields already concrete -- and
// confirms the real provider correctly carries every attribute *not* named
// in Modification.After forward unchanged, while applying the one that is.
func TestApplyResourceChange_RealProvider_TimeStatic(t *testing.T) {
	requireLive(t)

	src, err := ParseSource("hashicorp/time")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := Acquire(ctx, src, timeProviderVersion)
	if err != nil {
		t.Fatalf("acquire hashicorp/time: %v", err)
	}
	client, err := Launch(ctx, result.Path)
	if err != nil {
		t.Fatalf("launch hashicorp/time: %v", err)
	}
	defer client.Close()

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		t.Fatalf("fetch schema: %v", err)
	}
	resourceSchema, ok := schemas.Resources["time_static"]
	if !ok {
		t.Fatalf("time_static missing from hashicorp/time's schema: %+v", schemas.Resources)
	}
	if err := client.Provider.Configure(ctx, schemas.Provider, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("configure: %v", err)
	}

	// A realistic PriorState -- as a genuine ReadResource call against an
	// already-existing resource would return, every computed attribute
	// already concrete (hand-computed for 2024-01-01T00:00:00Z UTC, a
	// Monday) -- never synthesized from an ApplyResourceChange return value
	// (see the doc comment above for why that would misrepresent what
	// Ship's real loop actually does).
	prior := json.RawMessage(`{"id":"2024-01-01T00:00:00Z","rfc3339":"2024-01-01T00:00:00Z","day":1,"hour":0,"minute":0,"month":1,"second":0,"unix":1704067200,"year":2024,"triggers":{"key":"before"}}`)

	// The exact mechanism docs/executor.md's "Constructing PlannedState
	// without planning" describes: core.ApplyAfter substituting only the
	// drift_revert's changed dot-path onto the re-verified prior state.
	mod := core.Modification{After: map[string]json.RawMessage{"triggers.key": json.RawMessage(`"after"`)}}
	planned, err := core.ApplyAfter(prior, mod)
	if err != nil {
		t.Fatalf("ApplyAfter: %v", err)
	}

	updated, err := client.Provider.ApplyResourceChange(ctx, resourceSchema, "time_static", prior, planned, planned, nil)
	if err != nil {
		t.Fatalf("ApplyResourceChange: %v", err)
	}

	var decoded struct {
		ID       string            `json:"id"`
		RFC3339  string            `json:"rfc3339"`
		Day      json.Number       `json:"day"`
		Year     json.Number       `json:"year"`
		Unix     json.Number       `json:"unix"`
		Triggers map[string]string `json:"triggers"`
	}
	if err := json.Unmarshal(updated, &decoded); err != nil {
		t.Fatalf("decode ApplyResourceChange result %s: %v", updated, err)
	}

	// The one attribute Modification.After actually named: applied.
	if decoded.Triggers["key"] != "after" {
		t.Errorf("triggers[key] = %q, want %q", decoded.Triggers["key"], "after")
	}
	// Every already-concrete computed attribute NOT named in After: carried
	// forward unchanged, not nulled out or recomputed -- the real assertion
	// this test exists to make.
	if decoded.ID != "2024-01-01T00:00:00Z" {
		t.Errorf("id = %q, want the prior state's own value carried forward", decoded.ID)
	}
	if decoded.RFC3339 != "2024-01-01T00:00:00Z" {
		t.Errorf("rfc3339 = %q, want the prior state's own value carried forward", decoded.RFC3339)
	}
	if decoded.Day.String() != "1" {
		t.Errorf("day = %s, want 1 (carried forward, not nulled out)", decoded.Day.String())
	}
	if decoded.Year.String() != "2024" {
		t.Errorf("year = %s, want 2024 (carried forward, not nulled out)", decoded.Year.String())
	}
	if decoded.Unix.String() != "1704067200" {
		t.Errorf("unix = %s, want 1704067200 (carried forward, not nulled out)", decoded.Unix.String())
	}
}

// TestApplyResourceChange_RealProvider_TimeStatic_Create is UBI-27's own
// empirical settling of docs/resolver-adversarial.md's row 10: a genuine
// from-scratch create (PriorState = real JSON "null", not an all-null
// object -- the distinction ctyjson.Unmarshal already gets right given the
// literal token, confirmed here against a real provider for the first
// time) with PlannedState built via encodeUnknownAwareDynamicValue
// (ctyvalue.go, UBI-27): every schema-Computed attribute this resolved
// config never set at all (day/hour/id/minute/month/rfc3339/second/unix/
// year -- none given here, only "triggers") is encoded as a real
// cty.UnknownVal, not Null. If the fix is wrong, these come back null,
// exactly like the false start provider/apply_live_test.go's own doc
// comment already recorded once for PriorState; this test is the same
// question asked of PlannedState's Unknown-vs-Null distinction instead.
func TestApplyResourceChange_RealProvider_TimeStatic_Create(t *testing.T) {
	requireLive(t)

	src, err := ParseSource("hashicorp/time")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := Acquire(ctx, src, timeProviderVersion)
	if err != nil {
		t.Fatalf("acquire hashicorp/time: %v", err)
	}
	client, err := Launch(ctx, result.Path)
	if err != nil {
		t.Fatalf("launch hashicorp/time: %v", err)
	}
	defer client.Close()

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		t.Fatalf("fetch schema: %v", err)
	}
	resourceSchema, ok := schemas.Resources["time_static"]
	if !ok {
		t.Fatalf("time_static missing from hashicorp/time's schema: %+v", schemas.Resources)
	}
	if err := client.Provider.Configure(ctx, schemas.Provider, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("configure: %v", err)
	}

	// A genuine from-scratch create: no prior read, PriorState is the real
	// JSON null literal. PlannedState names only "triggers" -- every other
	// attribute (all schema-Computed) is absent entirely, relying on
	// encodeUnknownAwareDynamicValue to mark them Unknown rather than Null.
	prior := json.RawMessage(`null`)
	planned := json.RawMessage(`{"triggers":{"key":"first"}}`)

	updated, err := client.Provider.ApplyResourceChange(ctx, resourceSchema, "time_static", prior, planned, planned, nil)
	if err != nil {
		t.Fatalf("ApplyResourceChange: %v", err)
	}

	var decoded struct {
		ID       string            `json:"id"`
		RFC3339  string            `json:"rfc3339"`
		Day      json.Number       `json:"day"`
		Year     json.Number       `json:"year"`
		Unix     json.Number       `json:"unix"`
		Triggers map[string]string `json:"triggers"`
	}
	if err := json.Unmarshal(updated, &decoded); err != nil {
		t.Fatalf("decode ApplyResourceChange result %s: %v", updated, err)
	}

	if decoded.Triggers["key"] != "first" {
		t.Errorf("triggers[key] = %q, want %q (the one attribute actually set)", decoded.Triggers["key"], "first")
	}
	// The real assertion: every schema-Computed attribute the resolved
	// config never set came back genuinely computed, not null -- proof
	// the real provider accepted a directly-constructed Unknown (no
	// separate PlanResourceChange call) and resolved it correctly during
	// Apply, settling docs/resolver-adversarial.md row 10 empirically.
	if decoded.ID == "" {
		t.Error("id came back empty/null, want a real computed timestamp -- the Unknown-vs-Null fix did not take effect")
	}
	if decoded.RFC3339 == "" {
		t.Error("rfc3339 came back empty/null, want a real computed timestamp")
	}
	if decoded.Day.String() == "" || decoded.Day.String() == "0" {
		t.Errorf("day = %q, want a real nonzero computed value", decoded.Day.String())
	}
	if decoded.Year.String() == "" {
		t.Error("year came back empty, want a real computed value")
	}
	if decoded.Unix.String() == "" || decoded.Unix.String() == "0" {
		t.Errorf("unix = %q, want a real nonzero computed timestamp", decoded.Unix.String())
	}
}

// TestApplyResourceChange_RealProvider_TimeStatic_ComputedMarker is row
// 10's other half: not merely an attribute absent from the resolved
// config (the create test above), but a real, explicit
// {"$computed": {...}} marker sitting directly in PlannedState, exactly
// the shape core/resolver emits for a same-batch dependency's not-yet-
// applied output. Confirms the marker itself -- not just "absent and
// Computed" -- round-trips through cty-msgpack to a real provider and
// resolves correctly during Apply.
func TestApplyResourceChange_RealProvider_TimeStatic_ComputedMarker(t *testing.T) {
	requireLive(t)

	src, err := ParseSource("hashicorp/time")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := Acquire(ctx, src, timeProviderVersion)
	if err != nil {
		t.Fatalf("acquire hashicorp/time: %v", err)
	}
	client, err := Launch(ctx, result.Path)
	if err != nil {
		t.Fatalf("launch hashicorp/time: %v", err)
	}
	defer client.Close()

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		t.Fatalf("fetch schema: %v", err)
	}
	resourceSchema, ok := schemas.Resources["time_static"]
	if !ok {
		t.Fatalf("time_static missing from hashicorp/time's schema: %+v", schemas.Resources)
	}
	if err := client.Provider.Configure(ctx, schemas.Provider, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("configure: %v", err)
	}

	prior := json.RawMessage(`null`)
	// rfc3339 explicitly marked $computed, as if a sibling resource's
	// config had referenced it before this resource ever applied --
	// deliberately still present at ApplyResourceChange time, to prove the
	// marker-recognition path itself (not just the absent-attribute path
	// the Create test above exercises).
	planned := json.RawMessage(`{"triggers":{"key":"second"},"rfc3339":{"$computed":{"from":"payments.time_static.other.rfc3339"}}}`)

	updated, err := client.Provider.ApplyResourceChange(ctx, resourceSchema, "time_static", prior, planned, planned, nil)
	if err != nil {
		t.Fatalf("ApplyResourceChange: %v", err)
	}

	var decoded struct {
		RFC3339  string            `json:"rfc3339"`
		Triggers map[string]string `json:"triggers"`
	}
	if err := json.Unmarshal(updated, &decoded); err != nil {
		t.Fatalf("decode ApplyResourceChange result %s: %v", updated, err)
	}
	if decoded.Triggers["key"] != "second" {
		t.Errorf("triggers[key] = %q, want %q", decoded.Triggers["key"], "second")
	}
	if decoded.RFC3339 == "" {
		t.Error("rfc3339 came back empty/null -- the explicit $computed marker did not resolve to a real computed value")
	}
}
