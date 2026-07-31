package provider

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestEncodeUnknownAwareDynamicValue_SingleNestedBlock_MaxItemsOne is
// UBI-63 bug 2's own regression test: a real SDKv2-style provider's
// List/Set-nested "single instance" block (confirmed live against
// hashicorp/aws's own aws_ecr_repository.image_scanning_configuration,
// which reports max_items=1 despite being wire-nested as a list) must
// accept a bare object for that block's own config value -- the shape
// every authoring medium (md/diagram/SDK) naturally produces for
// something that's conceptually singular -- not just a one-element
// array. Before this fix, `ubx ship` failed a real create with "array
// required, got map[string]interface{}" the moment a bare object
// reached this exact block.
func TestEncodeUnknownAwareDynamicValue_SingleNestedBlock_MaxItemsOne(t *testing.T) {
	block := Block{
		NestedBlocks: []NestedBlock{
			{TypeName: "image_scanning_configuration", Nesting: NestingList, MaxItems: 1, Block: Block{
				Attributes: []Attribute{{Name: "scan_on_push", Type: json.RawMessage(`"bool"`), Required: true}},
			}},
		},
	}
	// A bare object, not `[{"scan_on_push": true}]` -- the exact shape
	// the md medium produced live.
	in := json.RawMessage(`{"image_scanning_configuration": {"scan_on_push": true}}`)

	out, err := encodeUnknownAwareDynamicValue(block, in)
	if err != nil {
		t.Fatalf("encodeUnknownAwareDynamicValue: %v", err)
	}

	decoded, err := decodeDynamicValue(block, out, nil)
	if err != nil {
		t.Fatalf("decodeDynamicValue (round trip): %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(decoded, &m); err != nil {
		t.Fatalf("decode round-tripped JSON: %v", err)
	}
	items, ok := m["image_scanning_configuration"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("expected a single-element list, got: %v", m["image_scanning_configuration"])
	}
	item, ok := items[0].(map[string]interface{})
	if !ok || item["scan_on_push"] != true {
		t.Fatalf("expected scan_on_push=true in the wrapped element, got: %v", items[0])
	}
}

// TestEncodeUnknownAwareDynamicValue_SingleNestedBlock_StillAcceptsArray
// proves the fix is purely additive -- the already-correct one-element
// array shape (what SDK/diagram codegen presumably already produces)
// keeps working identically.
func TestEncodeUnknownAwareDynamicValue_SingleNestedBlock_StillAcceptsArray(t *testing.T) {
	block := Block{
		NestedBlocks: []NestedBlock{
			{TypeName: "image_scanning_configuration", Nesting: NestingList, MaxItems: 1, Block: Block{
				Attributes: []Attribute{{Name: "scan_on_push", Type: json.RawMessage(`"bool"`), Required: true}},
			}},
		},
	}
	in := json.RawMessage(`{"image_scanning_configuration": [{"scan_on_push": true}]}`)
	if _, err := encodeUnknownAwareDynamicValue(block, in); err != nil {
		t.Fatalf("encodeUnknownAwareDynamicValue: %v", err)
	}
}

// TestEncodeUnknownAwareDynamicValue_MultiItemBlock_BareObjectAccepted
// is UBI-63 bug 2's own revised regression test (session 2, revised):
// the original fix gated bare-object acceptance on the schema declaring
// `MaxItems == 1`, reasoning that was the signal a List/Set block was
// conventionally single-instance. Found live, the SAME session: this
// resource's own real sibling block, encryption_configuration, is ALSO
// always authored as a single bare block in practice (AWS ECR only ever
// has one encryption configuration per repository) but reports
// MaxItems=0 ("not declared") in the real live schema -- a real `ubx
// ship` failed this exact block with "array required, got
// map[string]interface{}" even after the MaxItems==1 fix landed. A bare
// object is never ambiguous regardless of MaxItems -- it always means
// exactly one instance, schema-legal for every List/Set block -- so
// acceptance is no longer gated on MaxItems at all (this test uses
// MaxItems=5, this resource's own real image_tag_mutability_exclusion_filter
// value, to prove even a genuine multi-item-capable block still accepts
// a bare single object as shorthand for one instance).
func TestEncodeUnknownAwareDynamicValue_MultiItemBlock_BareObjectAccepted(t *testing.T) {
	block := Block{
		NestedBlocks: []NestedBlock{
			{TypeName: "filter", Nesting: NestingList, MaxItems: 5, Block: Block{
				Attributes: []Attribute{{Name: "filter", Type: json.RawMessage(`"string"`), Required: true}},
			}},
		},
	}
	in := json.RawMessage(`{"filter": {"filter": "tag1"}}`)
	out, err := encodeUnknownAwareDynamicValue(block, in)
	if err != nil {
		t.Fatalf("encodeUnknownAwareDynamicValue: %v", err)
	}
	decoded, err := decodeDynamicValue(block, out, nil)
	if err != nil {
		t.Fatalf("decodeDynamicValue (round trip): %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(decoded, &m); err != nil {
		t.Fatalf("decode round-tripped JSON: %v", err)
	}
	items, ok := m["filter"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("expected a single-element list, got: %v", m["filter"])
	}
}

// TestEncodeUnknownAwareDynamicValue_SingleNestedBlock_MaxItemsZero is
// the exact live repro this revision fixes: a List/Set-nested block
// whose schema declares MaxItems=0 (unset/"no explicit limit" --
// confirmed live against hashicorp/aws's own real
// aws_ecr_repository.encryption_configuration) still accepts a bare
// object, exactly like a MaxItems=1 block already does.
func TestEncodeUnknownAwareDynamicValue_SingleNestedBlock_MaxItemsZero(t *testing.T) {
	block := Block{
		NestedBlocks: []NestedBlock{
			{TypeName: "encryption_configuration", Nesting: NestingList, MaxItems: 0, Block: Block{
				Attributes: []Attribute{{Name: "encryption_type", Type: json.RawMessage(`"string"`), Optional: true}},
			}},
		},
	}
	in := json.RawMessage(`{"encryption_configuration": {"encryption_type": "AES256"}}`)
	out, err := encodeUnknownAwareDynamicValue(block, in)
	if err != nil {
		t.Fatalf("encodeUnknownAwareDynamicValue: %v", err)
	}
	decoded, err := decodeDynamicValue(block, out, nil)
	if err != nil {
		t.Fatalf("decodeDynamicValue (round trip): %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(decoded, &m); err != nil {
		t.Fatalf("decode round-tripped JSON: %v", err)
	}
	items, ok := m["encryption_configuration"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("expected a single-element list, got: %v", m["encryption_configuration"])
	}
	item, ok := items[0].(map[string]interface{})
	if !ok || item["encryption_type"] != "AES256" {
		t.Fatalf("expected encryption_type=AES256 in the wrapped element, got: %v", items[0])
	}
}

// TestEncodeUnknownAwareDynamicValue_UnrecognizedConfigKey_Refused is
// UBI-63 session 2's own regression test for a real live bug: a config
// key that matches no real schema Attribute or NestedBlock name (a
// model wrote "repository_name" for aws_ecr_repository, whose real
// schema only has "name") used to be silently ignored -- now a clear,
// immediate encode-time error naming the exact bad key, instead of
// letting the resource's own real "name" attribute go missing without a
// trace and reach a real provider as an empty, rejected value.
func TestEncodeUnknownAwareDynamicValue_UnrecognizedConfigKey_Refused(t *testing.T) {
	block := Block{
		Attributes: []Attribute{{Name: "name", Type: json.RawMessage(`"string"`), Required: true}},
	}
	in := json.RawMessage(`{"repository_name": "ci-artifacts"}`)
	_, err := encodeUnknownAwareDynamicValue(block, in)
	if !errors.Is(err, ErrUnrecognizedConfigKey) {
		t.Fatalf("err = %v, want ErrUnrecognizedConfigKey", err)
	}
}

// TestEncodeUnknownAwareDynamicValue_RequiredAttributeMissing_Refused
// is this same live bug's other half: the real "name" attribute,
// genuinely absent from config (not merely misnamed), is now a clear
// encode-time error naming it -- never silently sent as an explicit
// null for a Required attribute a real provider needs a real value for.
func TestEncodeUnknownAwareDynamicValue_RequiredAttributeMissing_Refused(t *testing.T) {
	block := Block{
		Attributes: []Attribute{
			{Name: "name", Type: json.RawMessage(`"string"`), Required: true},
			{Name: "force_delete", Type: json.RawMessage(`"bool"`), Optional: true},
		},
	}
	in := json.RawMessage(`{"force_delete": true}`)
	_, err := encodeUnknownAwareDynamicValue(block, in)
	if !errors.Is(err, ErrRequiredAttributeMissing) {
		t.Fatalf("err = %v, want ErrRequiredAttributeMissing", err)
	}
}

// TestEncodeUnknownAwareDynamicValue_OptionalAttributeMissing_StillFine
// proves the fix is precisely scoped -- an Optional (non-Required,
// non-Computed) attribute genuinely absent from config is still
// perfectly legal, unaffected by either new check.
func TestEncodeUnknownAwareDynamicValue_OptionalAttributeMissing_StillFine(t *testing.T) {
	block := Block{
		Attributes: []Attribute{
			{Name: "name", Type: json.RawMessage(`"string"`), Required: true},
			{Name: "force_delete", Type: json.RawMessage(`"bool"`), Optional: true},
		},
	}
	in := json.RawMessage(`{"name": "ci-artifacts"}`)
	if _, err := encodeUnknownAwareDynamicValue(block, in); err != nil {
		t.Fatalf("encodeUnknownAwareDynamicValue: %v", err)
	}
}

// TestEncodeUnknownAwareDynamicValue_RequiredAttributeSatisfiedByComputed_StillFine
// proves a Required attribute satisfied by an unresolved $computed
// marker (a real, legitimate in-flight state mid-ship, docs/executor.md)
// is a PRESENT key, not a missing one -- the new required-attribute
// check must never fire for this case.
func TestEncodeUnknownAwareDynamicValue_RequiredAttributeSatisfiedByComputed_StillFine(t *testing.T) {
	block := Block{
		Attributes: []Attribute{{Name: "role", Type: json.RawMessage(`"string"`), Required: true}},
	}
	in := json.RawMessage(`{"role": {"$computed": {"from": "payments.aws_iam_role.ci-runner.name"}}}`)
	if _, err := encodeUnknownAwareDynamicValue(block, in); err != nil {
		t.Fatalf("encodeUnknownAwareDynamicValue: %v", err)
	}
}
