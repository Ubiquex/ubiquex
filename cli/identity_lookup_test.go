package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ubiquex/ubiquex/core"
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
