package core

import (
	"encoding/json"
	"testing"
)

// lookup_realshapes_test.go exercises DeriveLookupFromResult against the
// shapes real resources actually have, rather than the shape the
// permissive fixture has (UBI-252).
//
// fakeprovider's fake_widget declares both an "id" and a required
// "name". Measured against the real AWS snapshot ubx ships, 0 of 1687
// dynamic-provider resource types declare an "id" at all, and 14%
// declare no required attribute either. So every test that went through
// that fixture took the easiest possible path through this function,
// and the hard paths were unreachable. 10% of AWS types ended up with
// no lookup key and 53% with one that could not re-find the resource,
// which left them undeletable and outside drift detection.

// The ordinary real case: no "id", identity under a provider-specific
// name, reachable only through the required attributes.
func TestDeriveLookup_NoIDUsesRequiredAttributes(t *testing.T) {
	result := json.RawMessage(`{"arn":"arn:aws:sqs:eu-west-1:1:orders","name":"orders","delay_seconds":0}`)

	got := DeriveLookupFromResult(result, []string{"name"})
	if got == nil {
		t.Fatal("a resource with no id but a required attribute still has an identity, and dropping it is what leaves a resource undeletable")
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "orders" {
		t.Errorf("lookup = %s, want it keyed by the required attribute", got)
	}
	if _, present := m["id"]; present {
		t.Errorf("lookup invented an id the resource does not have: %s", got)
	}
}

// The 14% case: no "id" and no required attribute. There is genuinely
// nothing to key on, and the honest answer is nothing rather than
// something unusable.
//
// This is the case the permissive fixture could never reach, and the
// one worth pinning: a future change that returned a partial or
// invented key here would look like an improvement and would silently
// produce lookups that cannot re-find anything.
func TestDeriveLookup_NoIDAndNoRequiredAttributesYieldsNothing(t *testing.T) {
	result := json.RawMessage(`{"arn":"arn:aws:x:::thing","note":"anything"}`)

	if got := DeriveLookupFromResult(result, nil); got != nil {
		t.Errorf("lookup = %s, want nothing: with no id and no required attribute there is nothing to key on, and returning a partial key would be worse than returning none", got)
	}
}

// An empty or null-valued required attribute is not an identity either.
// A provider that returns "" for an unset field would otherwise produce
// a lookup that matches everything or nothing.
func TestDeriveLookup_EmptyValuesAreNotAnIdentity(t *testing.T) {
	result := json.RawMessage(`{"arn":"arn:aws:x:::thing","name":"","other":null}`)

	if got := DeriveLookupFromResult(result, []string{"name", "other"}); got != nil {
		t.Errorf("lookup = %s, want nothing: an empty string and a null are not identities", got)
	}
}

// The permissive shape still works, so this is a widening of coverage
// rather than a change of behaviour.
func TestDeriveLookup_TheFixtureShapeStillWorks(t *testing.T) {
	result := json.RawMessage(`{"id":"computed-id","name":"widget1","tags":{}}`)

	got := DeriveLookupFromResult(result, []string{"name"})
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["id"] != "computed-id" || m["name"] != "widget1" {
		t.Errorf("lookup = %s, want both the id and the required attribute", got)
	}
}
