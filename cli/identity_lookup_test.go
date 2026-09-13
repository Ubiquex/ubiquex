package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/executor"
	"github.com/ubiquex/ubiquex/provider"
)

// A provider's declared identity beats ubx's own guess.
//
// tfplugin6 gives a SchemaAttribute Required/Optional/Computed/Sensitive
// and nothing meaning "this is how you find the resource again", so the
// lookup key was derived the only way the schema allowed: an attribute
// named "id", or failing that the Required ones. That is the Terraform
// shape. A CloudFormation resource's identifier is readOnly, therefore
// Computed, therefore deliberately excluded by that derivation, so 10% of
// AWS resource types recorded no lookup key at all and another 53%
// recorded one that could not re-find the resource. Those resources could
// not be destroyed through ubx and were never drift-checked, since
// status --drift counts a resource with no lookup as unreadable rather
// than checking it.
//
// The snapshot now publishes the answer, computed once at generation time
// from whichever source format the provider used. These tests cover the
// consuming half in ubx.

func TestDeriveLookup_DeclaredIdentityWins(t *testing.T) {
	// The real shape: no "id" anywhere, nothing Required, identifier
	// Computed. Exactly aws_sqs_queue.
	result := json.RawMessage(`{
		"queue_name": "ubx-demo-queue",
		"queue_url": "https://sqs.us-east-1.amazonaws.com/839333509514/ubx-demo-queue",
		"arn": "arn:aws:sqs:us-east-1:839333509514:ubx-demo-queue",
		"delay_seconds": 0
	}`)

	// What the old derivation manages with no id and no required attrs.
	if got := core.DeriveLookupFromResult(result, nil); got != nil {
		t.Fatalf("precondition failed: the id-only derivation should find nothing here, got %s", got)
	}

	// What the declared identity produces.
	got := core.DeriveLookupFromResult(result, []string{"queue_url"})
	if len(got) == 0 {
		t.Fatal("declared identity produced no lookup key")
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("lookup is not valid JSON: %v", err)
	}
	if m["queue_url"] != "https://sqs.us-east-1.amazonaws.com/839333509514/ubx-demo-queue" {
		t.Fatalf("lookup does not carry the identifier: %s", got)
	}
	if _, extra := m["queue_name"]; extra {
		t.Errorf("lookup carried a non-identity attribute, which makes it a state snapshot rather than a stable key: %s", got)
	}
}

// A compound identifier keeps both parts. CCAPI joins them in the
// resource's own declared order, so dropping either produces a key that
// addresses nothing.
func TestDeriveLookup_CompoundIdentity(t *testing.T) {
	result := json.RawMessage(`{"rest_api_id":"abc123","deployment_id":"dep456","description":"x"}`)
	got := core.DeriveLookupFromResult(result, []string{"rest_api_id", "deployment_id"})
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("lookup is not valid JSON: %v", err)
	}
	if m["rest_api_id"] != "abc123" || m["deployment_id"] != "dep456" {
		t.Fatalf("compound identity lost a part: %s", got)
	}
}

func TestReadSnapshotIdentity_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := map[string][]string{"aws_sqs_queue": {"queue_url"}}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := provider.ReadSnapshotIdentity(dir)
	if err != nil {
		t.Fatalf("ReadSnapshotIdentity: %v", err)
	}
	if len(got["aws_sqs_queue"]) != 1 || got["aws_sqs_queue"][0] != "queue_url" {
		t.Fatalf("identity did not round trip: %v", got)
	}
}

// Every snapshot published before identity.json existed has none, and
// must keep working: absent means "cannot say" and the caller falls back.
func TestReadSnapshotIdentity_AbsentIsNotAnError(t *testing.T) {
	got, err := provider.ReadSnapshotIdentity(t.TempDir())
	if err != nil {
		t.Fatalf("a snapshot without identity.json must not error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil identity, got %v", got)
	}
}

// A corrupt file is an error on purpose. Treating it as absent would be
// indistinguishable from "this provider has nothing to report" and would
// silently restore the derivation this replaces.
func TestReadSnapshotIdentity_CorruptIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "identity.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ReadSnapshotIdentity(dir); err == nil {
		t.Fatal("a corrupt identity.json must fail loudly rather than read as absent")
	}
}

// Declared identity that the observed result cannot satisfy falls through
// to the old derivation rather than emitting a partial key. A partial key
// is worse than the guess: it looks valid and addresses nothing.
func TestDeriveLookup_DeclaredIdentityAbsentFromResult_YieldsNothing(t *testing.T) {
	result := json.RawMessage(`{"queue_name":"ubx-demo-queue"}`)
	if got := core.DeriveLookupFromResult(result, []string{"queue_url"}); got != nil {
		t.Fatalf("expected no lookup when the identity attribute is absent, got %s", got)
	}
}

// UBI-270: the wiring is the fragile part, not the lookup itself.
//
// scan obtains an executor.Applier from the provider pool and asserts it
// to core.StateReader, on the documented fact that both views are the same
// stateReaderAdapter value. The identity map rides along on that value.
// Nothing in the type system connects "the pool built this with an
// identity map" to "the scan error can name the attributes", so these are
// compile-time proofs that the seam holds.
var (
	_ core.StateReader               = stateReaderAdapter{}
	_ executor.Applier               = stateReaderAdapter{}
	_ core.ResourceIdentityPublisher = stateReaderAdapter{}
)

// TestStateReaderAdapter_IdentityAttributes covers the three real answers,
// including the two that must stay "cannot say" rather than becoming an
// assertion that a type has no identity.
func TestStateReaderAdapter_IdentityAttributes(t *testing.T) {
	withMap := stateReaderAdapter{identity: map[string][]string{
		"aws_sqs_queue": {"queue_url"},
		"empty_entry":   {},
	}}

	if attrs, ok := withMap.IdentityAttributes("aws_sqs_queue"); !ok || len(attrs) != 1 || attrs[0] != "queue_url" {
		t.Fatalf("IdentityAttributes(aws_sqs_queue) = %v, %v; want [queue_url], true", attrs, ok)
	}
	if _, ok := withMap.IdentityAttributes("aws_s3_bucket"); ok {
		t.Fatal("a type absent from the map must report cannot-say, not an answer")
	}
	if _, ok := withMap.IdentityAttributes("empty_entry"); ok {
		t.Fatal("an empty attribute list must report cannot-say, not an answer")
	}

	// A Terraform-registry provider, and any snapshot cut before
	// identity.json existed: no map at all, and the absent case has to
	// survive that rather than panicking or claiming knowledge.
	noMap := stateReaderAdapter{}
	if _, ok := noMap.IdentityAttributes("aws_sqs_queue"); ok {
		t.Fatal("a provider with no identity map must report cannot-say")
	}
}

// TestNewApplierWithIdentity_CarriesTheMapToTheReadSide proves the value
// the pool hands to scan really does answer, which is the whole point of
// UBI-270: the map existed and was reachable only from the apply path.
func TestNewApplierWithIdentity_CarriesTheMapToTheReadSide(t *testing.T) {
	app := newApplierWithIdentity(nil, nil, "ubiquex/aws", map[string][]string{
		"aws_sqs_queue": {"queue_url"},
	})

	// Exactly the assertion cli/scan.go performs on the pool's return.
	sr, ok := app.(core.StateReader)
	if !ok {
		t.Fatal("the pool's Applier is not a StateReader, so scan cannot read at all")
	}
	pub, ok := sr.(core.ResourceIdentityPublisher)
	if !ok {
		t.Fatal("the reader scan uses cannot publish identity, so the map is loaded and unreachable again")
	}
	if attrs, known := pub.IdentityAttributes("aws_sqs_queue"); !known || attrs[0] != "queue_url" {
		t.Fatalf("identity did not survive the trip to the read side: %v, %v", attrs, known)
	}
}
