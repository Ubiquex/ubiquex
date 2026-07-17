package provider

import (
	"encoding/json"
	"testing"
)

// withOverrides swaps SensitiveOverrides for the duration of the calling
// test -- the same "swap in a test double for a package var" convention
// this project already uses elsewhere (core/lock.go's lockWaitTimeout,
// cli/config.go's configSearchStartDir).
func withOverrides(t *testing.T, overrides []SensitiveOverride) {
	t.Helper()
	orig := SensitiveOverrides
	SensitiveOverrides = overrides
	t.Cleanup(func() { SensitiveOverrides = orig })
}

func TestOverridePathsFor(t *testing.T) {
	withOverrides(t, []SensitiveOverride{
		{Source: "hashicorp/helm", Type: "helm_release", Path: "manifest"},
		{Source: "hashicorp/helm", Type: "helm_release", Path: "metadata.values"},
		{Source: "hashicorp/aws", Type: "aws_instance", Path: "user_data"},
	})
	got := OverridePathsFor("hashicorp/helm", "helm_release")
	want := map[string]bool{"manifest": true, "metadata.values": true}
	if len(got) != len(want) {
		t.Fatalf("OverridePathsFor = %v, want %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected path %q", p)
		}
	}
	if paths := OverridePathsFor("hashicorp/helm", "other_type"); paths != nil {
		t.Errorf("expected no paths for an unregistered type, got %v", paths)
	}
}

// TestSensitiveOverrides_HelmReleaseSeeded is a permanent regression
// guard on the real seed data (UBI-24, UBI-22's own finding) -- not a
// withOverrides test, deliberately checks the actual package var.
func TestSensitiveOverrides_HelmReleaseSeeded(t *testing.T) {
	want := map[string]bool{"manifest": true, "metadata.values": true, "metadata.notes": true}
	got := OverridePathsFor("hashicorp/helm", "helm_release")
	if len(got) != len(want) {
		t.Fatalf("OverridePathsFor(hashicorp/helm, helm_release) = %v, want exactly %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("unexpected seeded override path %q", p)
		}
	}
}

// TestRedact_OverrideOnlyAttributeRedacted is UBI-24's core case: an
// attribute the schema never flags Sensitive at all, redacted purely
// because it's in the override table.
func TestRedact_OverrideOnlyAttributeRedacted(t *testing.T) {
	withOverrides(t, []SensitiveOverride{{Source: "acme/widget", Type: "fake_thing", Path: "notes"}})
	block := Block{Attributes: []Attribute{{Name: "id"}, {Name: "notes"}}}
	observed := json.RawMessage(`{"id":"widget-1","notes":"contains a password: hunter2"}`)

	out, err := Redact("acme/widget", "fake_thing", block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["id"] != "widget-1" {
		t.Fatalf("expected id untouched, got: %v", decoded["id"])
	}
	redactedHash(t, mustMarshal(t, decoded["notes"]))
	if containsPlaintext(out, "hunter2") {
		t.Fatal("override-redacted attribute leaked its real value")
	}
}

// TestRedact_OverrideAndSchemaFlagUnion confirms redaction is the UNION
// of schema Sensitive flags and the override table, not one replacing
// the other -- a schema-Sensitive attribute AND an override-only
// attribute in the same resource must both end up redacted.
func TestRedact_OverrideAndSchemaFlagUnion(t *testing.T) {
	withOverrides(t, []SensitiveOverride{{Source: "acme/widget", Type: "fake_thing", Path: "notes"}})
	block := Block{Attributes: []Attribute{
		{Name: "id"},
		{Name: "password", Sensitive: true},
		{Name: "notes"},
	}}
	observed := json.RawMessage(`{"id":"widget-1","password":"hunter2","notes":"rendered hunter2 again"}`)

	out, err := Redact("acme/widget", "fake_thing", block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["id"] != "widget-1" {
		t.Fatalf("expected id untouched, got: %v", decoded["id"])
	}
	redactedHash(t, mustMarshal(t, decoded["password"]))
	redactedHash(t, mustMarshal(t, decoded["notes"]))
	if containsPlaintext(out, "hunter2") {
		t.Fatal("either the schema-flagged or the override-only attribute leaked")
	}
}

// TestRedact_OverrideNestedPathInListWrappedBlock is the real
// helm_release shape: metadata is a one-item JSON array (a compound
// list(object(...))-typed attribute, not a real NestedBlock -- see
// docs/architecture.md's "Sensitive overrides" section), and the
// override path "metadata.values" must reach metadata[0].values without
// disturbing metadata[0]'s sibling fields.
func TestRedact_OverrideNestedPathInListWrappedBlock(t *testing.T) {
	withOverrides(t, []SensitiveOverride{
		{Source: "hashicorp/helm", Type: "helm_release", Path: "metadata.values"},
	})
	block := Block{} // metadata's own compound Type isn't modeled via NestedBlocks at all -- the override walker doesn't need it to be
	observed := json.RawMessage(`{"metadata":[{"name":"myrelease","values":"{\"replicaCount\":1}","notes":"install notes"}]}`)

	out, err := Redact("hashicorp/helm", "helm_release", block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	metadata := decoded["metadata"].([]interface{})
	item := metadata[0].(map[string]interface{})
	if item["name"] != "myrelease" {
		t.Fatalf("expected name untouched, got: %v", item["name"])
	}
	if item["notes"] != "install notes" {
		t.Fatalf("expected notes untouched (not listed in this test's override set), got: %v", item["notes"])
	}
	redactedHash(t, mustMarshal(t, item["values"]))
	if containsPlaintext(out, "replicaCount") {
		t.Fatal("metadata.values leaked its real content")
	}
}

// TestRedact_OverrideAppliesToEveryListElement confirms the same "apply
// to every element, not just index 0" generalization Redact's own
// schema-driven List/Set handling already has (UBI-23) -- an override
// path landing on an array applies to every item, each redacting to its
// own distinct hash.
func TestRedact_OverrideAppliesToEveryListElement(t *testing.T) {
	withOverrides(t, []SensitiveOverride{{Source: "acme/x", Type: "y", Path: "items.secret"}})
	block := Block{}
	observed := json.RawMessage(`{"items":[{"secret":"one","id":"a"},{"secret":"two","id":"b"}]}`)

	out, err := Redact("acme/x", "y", block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items := decoded["items"].([]interface{})
	if items[0].(map[string]interface{})["id"] != "a" || items[1].(map[string]interface{})["id"] != "b" {
		t.Fatal("expected sibling id fields untouched")
	}
	h1 := redactedHash(t, mustMarshal(t, items[0].(map[string]interface{})["secret"]))
	h2 := redactedHash(t, mustMarshal(t, items[1].(map[string]interface{})["secret"]))
	if h1 == h2 {
		t.Fatal("expected different secret values across list elements to redact to different hashes")
	}
	if containsPlaintext(out, "one") || containsPlaintext(out, "two") {
		t.Fatal("a list element's secret leaked")
	}
}

// TestRedact_OverrideDoesNotApplyToDifferentSourceOrType confirms
// non-listed (source, type) pairs are left completely untouched -- an
// override is scoped, not a blanket rule.
func TestRedact_OverrideDoesNotApplyToDifferentSourceOrType(t *testing.T) {
	withOverrides(t, []SensitiveOverride{{Source: "hashicorp/helm", Type: "helm_release", Path: "manifest"}})
	block := Block{}
	observed := json.RawMessage(`{"manifest":"plain rendered text, not this resource's problem"}`)

	out, err := Redact("hashicorp/other", "other_type", block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["manifest"] != "plain rendered text, not this resource's problem" {
		t.Fatalf("expected manifest untouched for a non-matching (source, type), got: %v", decoded["manifest"])
	}
}

// TestRedact_OverrideSkipsAlreadyRedactedValue confirms overrides only
// ADD redaction -- an attribute that's both schema-Sensitive AND
// override-listed is redacted once by the schema-driven pass, and the
// override pass leaves the resulting marker alone rather than re-hashing
// it (which would still be secret-safe, but pointlessly non-idempotent).
func TestRedact_OverrideSkipsAlreadyRedactedValue(t *testing.T) {
	withOverrides(t, []SensitiveOverride{{Source: "acme/x", Type: "y", Path: "secret"}})
	block := Block{Attributes: []Attribute{{Name: "secret", Sensitive: true}}}
	observed := json.RawMessage(`{"secret":"hunter2"}`)

	// The hash a direct schema-only redaction would produce.
	wantOut, err := Redact("acme/x", "y-schema-only", Block{Attributes: []Attribute{{Name: "secret", Sensitive: true}}}, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact (schema-only baseline): %v", err)
	}
	var wantDecoded map[string]interface{}
	json.Unmarshal(wantOut, &wantDecoded)
	wantHash := redactedHash(t, mustMarshal(t, wantDecoded["secret"]))

	out, err := Redact("acme/x", "y", block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var decoded map[string]interface{}
	json.Unmarshal(out, &decoded)
	gotHash := redactedHash(t, mustMarshal(t, decoded["secret"]))
	if gotHash != wantHash {
		t.Fatalf("expected the override pass to leave the schema-redacted marker's hash unchanged, got %s want %s", gotHash, wantHash)
	}
}
