package core

import (
	"encoding/json"
	"testing"
)

// TestDeriveLookupFromResult_IDOnly_Unaffected proves the fix is purely
// additive -- a resource type with no requiredAttrNames given still gets
// the original id-only lookup, byte-equivalent to before this amendment.
func TestDeriveLookupFromResult_IDOnly_Unaffected(t *testing.T) {
	result := json.RawMessage(`{"id":"widget-1","name":"widget-1","tags":{}}`)
	got := DeriveLookupFromResult(result, nil)
	var m map[string]interface{}
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(m) != 1 || m["id"] != "widget-1" {
		t.Fatalf("lookup = %v, want exactly {\"id\":\"widget-1\"}", m)
	}
}

// TestDeriveLookupFromResult_RequiredAttrsIncluded is UBI-63 session 2's
// own regression test for a real live bug: aws_iam_role_policy_attachment's
// own real AWS ReadResource needs "role"/"policy_arn" present in its own
// current_state to construct a valid re-read at all -- "id" alone,
// despite existing in this type's own real schema, doesn't round-trip.
// requiredAttrNames now includes their real, non-empty values in the
// derived lookup, alongside "id".
func TestDeriveLookupFromResult_RequiredAttrsIncluded(t *testing.T) {
	result := json.RawMessage(`{"id":"ci-runner/ci-runner-access","role":"ci-runner","policy_arn":"arn:aws:iam::123456789012:policy/ci-runner-access"}`)
	got := DeriveLookupFromResult(result, []string{"role", "policy_arn"})
	var m map[string]interface{}
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["id"] != "ci-runner/ci-runner-access" {
		t.Errorf("id = %v, want ci-runner/ci-runner-access", m["id"])
	}
	if m["role"] != "ci-runner" {
		t.Errorf("role = %v, want ci-runner", m["role"])
	}
	if m["policy_arn"] != "arn:aws:iam::123456789012:policy/ci-runner-access" {
		t.Errorf("policy_arn = %v, want the real policy ARN", m["policy_arn"])
	}
}

// TestDeriveLookupFromResult_RequiredAttrEmpty_Omitted proves an empty
// (never a legitimately absent, since it's Required) value is left out
// rather than baked in as a useless empty string -- an honest "couldn't
// derive this one," matching the function's own id-handling precedent.
func TestDeriveLookupFromResult_RequiredAttrEmpty_Omitted(t *testing.T) {
	result := json.RawMessage(`{"id":"x","role":"","policy_arn":"arn:aws:iam::123456789012:policy/p"}`)
	got := DeriveLookupFromResult(result, []string{"role", "policy_arn"})
	var m map[string]interface{}
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := m["role"]; ok {
		t.Errorf("expected empty \"role\" to be omitted, got: %v", m)
	}
	if m["policy_arn"] != "arn:aws:iam::123456789012:policy/p" {
		t.Errorf("policy_arn = %v, want the real value", m["policy_arn"])
	}
}

// TestDeriveLookupFromResult_NoID_RequiredAttrsStillCaptured proves the
// lookup isn't gated on "id" being present at all -- some resource
// types identify themselves purely by their own required fields.
func TestDeriveLookupFromResult_NoID_RequiredAttrsStillCaptured(t *testing.T) {
	result := json.RawMessage(`{"role":"ci-runner","policy_arn":"arn:aws:iam::123456789012:policy/p"}`)
	got := DeriveLookupFromResult(result, []string{"role", "policy_arn"})
	var m map[string]interface{}
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := m["id"]; ok {
		t.Errorf("expected no \"id\" key at all, got: %v", m)
	}
	if m["role"] != "ci-runner" || m["policy_arn"] != "arn:aws:iam::123456789012:policy/p" {
		t.Errorf("expected both required attrs captured, got: %v", m)
	}
}
