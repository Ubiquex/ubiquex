package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeProvider is an in-memory test double for core.StateReader — no
// subprocess, no gRPC, no dependency on package provider at all, just
// enough to drive core.RunScan/VerifyFreshness through their adversarial
// paths deterministically.
type fakeProvider struct {
	schemaErr    error
	configureErr error
	readErr      error
	state        json.RawMessage // returned by ReadResource; nil/"null" simulates "unreadable"
}

func (f *fakeProvider) Schema(context.Context) (any, map[string]any, error) {
	if f.schemaErr != nil {
		return nil, nil, f.schemaErr
	}
	return struct{}{}, map[string]any{"aws_s3_bucket": struct{}{}}, nil
}

func (f *fakeProvider) Configure(context.Context, any, json.RawMessage) error {
	return f.configureErr
}

func (f *fakeProvider) ReadResource(context.Context, any, string, json.RawMessage) (json.RawMessage, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.state, nil
}

func testAddr() Address {
	return Address{Stack: "payments", Type: "aws_s3_bucket", Name: "ubx-states"}
}

func TestRunScan_New(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	if res.Outcome != ScanNew {
		t.Fatalf("Outcome = %v, want ScanNew", res.Outcome)
	}
	if res.ObservedHash == "" {
		t.Fatal("ObservedHash is empty")
	}
}

func TestRunScan_UnchangedAfterAdoption(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}
	addr := testAddr()

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	proposal, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	if _, err := Accept(l, proposal); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// Scan again -- nothing changed.
	res2, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (2nd): %v", err)
	}
	if res2.Outcome != ScanUnchanged {
		t.Fatalf("Outcome = %v, want ScanUnchanged", res2.Outcome)
	}
}

func TestRunScan_DriftDetectedAfterAdoption(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	proposal, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	if _, err := Accept(l, proposal); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// Reality changes: someone edits the tag outside ubx.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"}}`)

	res2, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (2nd): %v", err)
	}
	if res2.Outcome != ScanDrifted {
		t.Fatalf("Outcome = %v, want ScanDrifted", res2.Outcome)
	}

	driftProposal, err := GenerateProposal(l, "payments", res2)
	if err != nil {
		t.Fatalf("GenerateProposal (drift): %v", err)
	}
	if driftProposal.Kind != KindDriftAdopt {
		t.Fatalf("Kind = %q, want %q", driftProposal.Kind, KindDriftAdopt)
	}
	if len(driftProposal.Delta.Modifies) != 1 {
		t.Fatalf("got %d modifies entries, want 1", len(driftProposal.Delta.Modifies))
	}
	mod := driftProposal.Delta.Modifies[0]
	if string(mod.Before["tags.env"]) != `"prod"` {
		t.Fatalf("before[tags.env] = %s, want %q", mod.Before["tags.env"], "prod")
	}
	if string(mod.After["tags.env"]) != `"staging"` {
		t.Fatalf("after[tags.env] = %s, want %q", mod.After["tags.env"], "staging")
	}
	// "id" didn't change -- must not appear in the diff.
	if _, ok := mod.Before["id"]; ok {
		t.Fatalf("unchanged attribute %q leaked into the diff", "id")
	}

	if _, err := Accept(l, driftProposal); err != nil {
		t.Fatalf("Accept (drift): %v", err)
	}
}

func TestRunScan_ResourceUnreadable(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: nil}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrResourceUnreadable) {
		t.Fatalf("got %v, want ErrResourceUnreadable", err)
	}
}

func TestRunScan_ResourceUnreadable_JSONNull(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: json.RawMessage(`null`)}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrResourceUnreadable) {
		t.Fatalf("got %v, want ErrResourceUnreadable", err)
	}
}

// TestRunScan_ResourceUnreadable_TeachesKnownType is UBI-20 workstream 3:
// aws_s3_bucket (testAddr's own type) is one of core/lookuphints' known
// types -- the error should name "id" as the fix and link to the docs,
// not just report the bare sentinel.
func TestRunScan_ResourceUnreadable_TeachesKnownType(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: nil}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`), ProviderSource: "hashicorp/aws"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), `"id"`) {
		t.Errorf("expected the error to teach \"id\" as the fix for aws_s3_bucket, got: %v", err)
	}
	// docs.ubiquex.io, not the retired ubiquex-docs repo. Asserting on the
	// live host rather than only on a path fragment, since a path that
	// still resolves on a dead site is exactly what this used to pass on.
	if !strings.Contains(err.Error(), "https://docs.ubiquex.io/cli-reference/scan") {
		t.Errorf("expected a link to the live lookup docs page, got: %v", err)
	}
}

// TestLookupHintText_HonestFallbackForUnknownType confirms a type
// core/lookuphints has no entry for gets an honest "check the schema"
// fallback, never a fabricated guess dressed up as a known fact.
// lookupHintText, not the full RunScan pipeline, since fakeProvider's
// Schema only ever knows about "aws_s3_bucket" (there is no way to reach
// ErrResourceUnreadable through RunScan for a type the provider's own
// schema doesn't recognize -- that's ErrUnknownResourceType instead,
// tested elsewhere).
func TestLookupHintText_HonestFallbackForUnknownType(t *testing.T) {
	// A provider that publishes no identity map at all, which is every
	// Terraform-registry provider: the fallback chain must still work.
	got := lookupHintText(&fakeProvider{}, "hashicorp/aws", "aws_totally_unknown_type")
	if !strings.Contains(got, "check aws_totally_unknown_type's provider schema") {
		t.Errorf("expected the honest fallback wording, got: %v", got)
	}
	if strings.Contains(got, `"id"`) {
		t.Errorf("must not fabricate a specific field guess for an unknown type, got: %v", got)
	}
}

// TestRunScan_ResourceUnreadable_EmptyProviderSourceFallsBack is UBI-21's
// own adversarial case for the (source, type) keying refactor: a scan
// launched via a raw --provider path (no known registry source, so
// ScanRequest.ProviderSource is "") for a type core/lookuphints DOES know
// about under "hashicorp/aws" must still fall back to the honest generic
// message, never guess the source.
func TestRunScan_ResourceUnreadable_EmptyProviderSourceFallsBack(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: nil}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), `must include "id"`) {
		t.Errorf("expected the honest fallback (no known ProviderSource), got a specific hint: %v", err)
	}
	if !strings.Contains(err.Error(), "check aws_s3_bucket's provider schema") {
		t.Errorf("expected the honest fallback wording, got: %v", err)
	}
}

func TestRunScan_ProviderErrorOnSchema(t *testing.T) {
	l := Open(t.TempDir())
	wantErr := errors.New("schema fetch boom")
	fp := &fakeProvider{schemaErr: wantErr}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want wrapped %v", err, wantErr)
	}
}

func TestRunScan_ProviderErrorOnConfigure(t *testing.T) {
	l := Open(t.TempDir())
	wantErr := errors.New("configure boom")
	fp := &fakeProvider{configureErr: wantErr}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want wrapped %v", err, wantErr)
	}
}

func TestRunScan_ProviderErrorOnReadResource(t *testing.T) {
	l := Open(t.TempDir())
	wantErr := errors.New("read boom")
	fp := &fakeProvider{readErr: wantErr}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want wrapped %v", err, wantErr)
	}
}

func TestRunScan_UnknownResourceType(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: json.RawMessage(`{}`)}
	addr := Address{Stack: "payments", Type: "aws_totally_made_up", Name: "x"}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrUnknownResourceType) {
		t.Fatalf("got %v, want ErrUnknownResourceType", err)
	}
}

func TestGenerateProposal_UnchangedRefused(t *testing.T) {
	l := Open(t.TempDir())
	res := &ScanResult{Address: testAddr(), Outcome: ScanUnchanged}
	if _, err := GenerateProposal(l, "payments", res); err == nil {
		t.Fatal("expected an error for an unchanged scan result, got nil")
	}
}

func TestVerifyFreshness_PassesWhenUnchanged(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	proposal, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}

	if err := VerifyFreshness(context.Background(), fp, l, addr, "", nil, proposal); err != nil {
		t.Fatalf("VerifyFreshness: %v", err)
	}
}

// TestVerifyFreshness_BlocksStaleAcceptance is the "drift-on-drift
// staleness" adversarial case: reality changes again between when scan
// generated the proposal and when accept would run.
func TestVerifyFreshness_BlocksStaleAcceptance(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	proposal, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}

	// Reality changes again, after the proposal was generated but before
	// it's accepted.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"}}`)

	err = VerifyFreshness(context.Background(), fp, l, addr, "", nil, proposal)
	if !errors.Is(err, ErrStaleObservation) {
		t.Fatalf("got %v, want ErrStaleObservation", err)
	}
}

// TestVerifyFreshness_FalseStaleOnNormalizationNoise is UBI-89's own
// regression test for the P1's first confirmed root cause: VerifyFreshness
// compares recorded/fresh via a RAW hash-equality check, with ZERO
// normalization awareness -- unlike RunScan's own drift verdict (this
// same file, TestRunScan_*), which already downgrades a candidate
// mismatch fully explained by null<->zero-value/materialization noise
// (FilterNormalizationNoise, UBI-63) before ever calling it real drift.
// This gap isn't a UBI-88 regression (UBI-88 never touched ObservedHash/
// VerifyFreshness/resolution.inputs.ObservedHash computation at all,
// confirmed by reading every call site) -- it's a PRE-EXISTING hole
// UBI-63's own fix never reached, because VerifyFreshness's own
// "recorded" value is a bare hash string (ResolutionInput.ObservedHash),
// never the raw state DiffAttributes would need; this fix re-derives the
// raw recorded state from the ledger's own FoldState (the exact same
// value that produced `recorded`'s hash at resolve time for a
// core/resolver-produced modify) as a fallback, mirroring RunScan's own
// established downgrade pattern exactly.
//
// A real SDKv2-vintage provider (an SQS queue's own force_detach_policies-
// shaped attribute, matching the founder's own real repro against AWS)
// doesn't round-trip a "no value" attribute byte-for-byte -- recorded as
// an explicit null (the ledger's own FoldState, ultimately traced back to
// whatever the resource's own last successful apply returned), a LATER
// fresh read of the exact SAME, genuinely-untouched resource reads back
// its own zero value instead. Nothing about the real resource changed.
func TestVerifyFreshness_FalseStaleOnNormalizationNoise(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()

	recordedState := json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"},"force_detach_policies":null}`)
	adoptForTest(t, l, addr, recordedState)
	recordedHash, err := ObservedHash(recordedState)
	if err != nil {
		t.Fatal(err)
	}

	proposal := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Kind:          KindChange,
		Resolution: Resolution{Inputs: []ResolutionInput{
			{Kind: "live_state", Resource: addr.String(), ObservedHash: recordedHash, Lookup: json.RawMessage(`{"id":"ubx-states"}`)},
		}},
	}

	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"},"force_detach_policies":false}`)}

	if err := VerifyFreshness(context.Background(), fp, l, addr, "", nil, proposal); err != nil {
		t.Fatalf("VerifyFreshness incorrectly refused as stale on pure null<->zero-value normalization noise (no real change to the resource): %v", err)
	}
}

// TestVerifyFreshness_RealChangeStillBlocksAlongsideNoise proves the fix
// only ever DOWNGRADES a false positive -- a genuine change hiding
// alongside pure normalization noise on another attribute must still
// refuse as stale, the same "only ever downgrades, never upgrades"
// discipline FilterNormalizationNoise's own doc comment already commits
// to for every other caller.
func TestVerifyFreshness_RealChangeStillBlocksAlongsideNoise(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()

	recordedState := json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"},"force_detach_policies":null}`)
	adoptForTest(t, l, addr, recordedState)
	recordedHash, err := ObservedHash(recordedState)
	if err != nil {
		t.Fatal(err)
	}

	proposal := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Kind:          KindChange,
		Resolution: Resolution{Inputs: []ResolutionInput{
			{Kind: "live_state", Resource: addr.String(), ObservedHash: recordedHash, Lookup: json.RawMessage(`{"id":"ubx-states"}`)},
		}},
	}

	// force_detach_policies: null -> false is pure noise, BUT tags.env
	// also genuinely changed out of band -- a real divergence must still
	// surface.
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"},"force_detach_policies":false}`)}

	err = VerifyFreshness(context.Background(), fp, l, addr, "", nil, proposal)
	if !errors.Is(err, ErrStaleObservation) {
		t.Fatalf("got %v, want ErrStaleObservation -- a real change must still block even alongside pure normalization noise on another attribute", err)
	}
}

// UBI-178: VerifyDataSourceFreshness's own real tests. No real caller
// produces a "data_source"-kind resolution.inputs entry yet -- the
// declarative pipeline (ubx.Data, IntentDocument's own new field,
// Collector.addDataSource) doesn't exist across any of the three
// runtimes as of this session -- so these hand-construct the Proposal
// directly, proving the verification LOGIC itself is correct in
// isolation, the same adversarial-first discipline this file's own
// VerifyFreshness tests already use. fakeProvider's own Schema() only
// ever registers "aws_s3_bucket" -- reused as-is here rather than a
// literal "data.aws_s3_bucket" string, since this test proves the
// iteration/hash-compare logic, not the real address-naming convention
// (ir.go/codegen's own job, not yet built).

func TestVerifyDataSourceFreshness_PassesWhenUnchanged(t *testing.T) {
	addr := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "existing_bucket"}
	state := json.RawMessage(`{"id":"prod-logs","region":"us-east-1"}`)
	hash, err := ObservedHash(state)
	if err != nil {
		t.Fatal(err)
	}
	fp := &fakeProvider{state: state}
	proposal := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Kind:          KindChange,
		Resolution: Resolution{Inputs: []ResolutionInput{
			{Kind: "data_source", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"prod-logs"}`)},
		}},
	}

	if err := VerifyDataSourceFreshness(context.Background(), fp, "", nil, proposal); err != nil {
		t.Fatalf("VerifyDataSourceFreshness: %v", err)
	}
}

// TestVerifyDataSourceFreshness_BlocksWhenLookedUpValueChanged is the
// real case this whole pass exists for: UBI-178's own worked example --
// a data source's own looked-up value moved after resolve time, and
// nothing else in this proposal (no resource in delta.Modifies) would
// otherwise ever notice, because VerifyFreshness's own per-modification
// loop never reaches a "data_source" entry.
func TestVerifyDataSourceFreshness_BlocksWhenLookedUpValueChanged(t *testing.T) {
	addr := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "existing_bucket"}
	recordedState := json.RawMessage(`{"id":"prod-logs","region":"us-east-1"}`)
	recordedHash, err := ObservedHash(recordedState)
	if err != nil {
		t.Fatal(err)
	}
	proposal := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Kind:          KindChange,
		Resolution: Resolution{Inputs: []ResolutionInput{
			{Kind: "data_source", Resource: addr.String(), ObservedHash: recordedHash, Lookup: json.RawMessage(`{"id":"prod-logs"}`)},
		}},
	}

	// The real bucket this data source looked up got moved to a
	// different region by someone else, after this proposal resolved.
	fp := &fakeProvider{state: json.RawMessage(`{"id":"prod-logs","region":"eu-west-1"}`)}

	err = VerifyDataSourceFreshness(context.Background(), fp, "", nil, proposal)
	if !errors.Is(err, ErrStaleObservation) {
		t.Fatalf("got %v, want ErrStaleObservation", err)
	}
}

// TestVerifyDataSourceFreshness_IgnoresNonDataSourceKinds proves the
// unconditional iteration doesn't re-verify -- or, worse, misinterpret
// -- a "live_state"/"cross_stack_pin"/other Kind entry as if it were a
// data source. A live_state entry's own Resource is a real resource
// address VerifyFreshness already owns checking; this pass must leave
// it alone even when it's sitting right next to a data_source entry in
// the same slice.
func TestVerifyDataSourceFreshness_IgnoresNonDataSourceKinds(t *testing.T) {
	liveAddr := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "managed"}
	proposal := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Kind:          KindChange,
		Resolution: Resolution{Inputs: []ResolutionInput{
			// A deliberately WRONG hash/lookup for the live_state entry --
			// if VerifyDataSourceFreshness touched it at all, this would
			// fail. It must not even look.
			{Kind: "live_state", Resource: liveAddr.String(), ObservedHash: "deliberately-wrong", Lookup: json.RawMessage(`{"id":"whatever"}`)},
		}},
	}
	fp := &fakeProvider{readErr: errors.New("ReadResource must never be called for a non-data_source entry")}

	if err := VerifyDataSourceFreshness(context.Background(), fp, "", nil, proposal); err != nil {
		t.Fatalf("VerifyDataSourceFreshness: %v (should have skipped the only entry, it isn't Kind data_source)", err)
	}
}

// TestVerifyDataSourceFreshness_MissingLookupErrors proves a
// data_source entry recorded without its own lookup key (should be
// structurally impossible once the real resolve-time producer exists,
// but this pass must never silently treat "no lookup" as "trivially
// fresh") fails loud, mirroring VerifyFreshness's own identical guard.
func TestVerifyDataSourceFreshness_MissingLookupErrors(t *testing.T) {
	addr := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "existing_bucket"}
	proposal := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Kind:          KindChange,
		Resolution: Resolution{Inputs: []ResolutionInput{
			{Kind: "data_source", Resource: addr.String(), ObservedHash: "somehash"},
		}},
	}
	fp := &fakeProvider{state: json.RawMessage(`{"id":"prod-logs"}`)}

	err := VerifyDataSourceFreshness(context.Background(), fp, "", nil, proposal)
	if err == nil {
		t.Fatal("expected an error for a data_source entry with no recorded lookup key, got nil")
	}
}

// TestVerifyDataSourceFreshness_InvalidAddressErrors proves a
// malformed Resource field (not real "<stack>.<type>.<name>") fails
// loud rather than panicking or silently skipping.
func TestVerifyDataSourceFreshness_InvalidAddressErrors(t *testing.T) {
	proposal := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Kind:          KindChange,
		Resolution: Resolution{Inputs: []ResolutionInput{
			{Kind: "data_source", Resource: "not-a-real-address", ObservedHash: "somehash", Lookup: json.RawMessage(`{"id":"x"}`)},
		}},
	}
	fp := &fakeProvider{state: json.RawMessage(`{"id":"x"}`)}

	err := VerifyDataSourceFreshness(context.Background(), fp, "", nil, proposal)
	if err == nil {
		t.Fatal("expected an error for a malformed Resource address, got nil")
	}
}

// identityPublishingProvider is a fakeProvider that also implements
// core.ResourceIdentityPublisher, standing in for a dynamic provider whose
// schema snapshot ships an identity.json.
type identityPublishingProvider struct {
	fakeProvider
	identity map[string][]string
}

func (p *identityPublishingProvider) IdentityAttributes(resourceType string) ([]string, bool) {
	attrs, ok := p.identity[resourceType]
	if !ok || len(attrs) == 0 {
		return nil, false
	}
	return attrs, true
}

// TestLookupHintText_NamesThePublishedIdentity is UBI-270's core case.
//
// The real one: adopting an orphaned SQS queue needed --lookup, the
// obvious guess {"id": ...} is wrong for that type, and ubx answered
// "check aws_sqs_queue's provider schema for its required lookup fields"
// while holding a map that said queue_url. The map was loaded in the same
// process, by the same adapter, and used on the apply path to derive the
// lookup key to record after a create. Nothing read it here.
func TestLookupHintText_NamesThePublishedIdentity(t *testing.T) {
	prov := &identityPublishingProvider{identity: map[string][]string{
		"aws_sqs_queue": {"queue_url"},
	}}

	got := lookupHintText(prov, "ubiquex/aws", "aws_sqs_queue")

	if !strings.Contains(got, "queue_url") {
		t.Fatalf("the hint does not name the attribute the provider published: %v", got)
	}
	// Naming the attribute is not enough on its own: the reader is stuck on
	// what to type, so the hint has to carry a runnable shape.
	if !strings.Contains(got, `--lookup '{"queue_url":"<queue_url>"}'`) {
		t.Fatalf("the hint does not give a runnable --lookup: %v", got)
	}
	if strings.Contains(got, "check aws_sqs_queue's provider schema") {
		t.Fatalf("fell through to the generic fallback while the provider could answer: %v", got)
	}
}

// TestLookupHintText_MultipleIdentityAttributes covers a type identified
// by more than one attribute, since the prose and the JSON both have to
// hold up.
func TestLookupHintText_MultipleIdentityAttributes(t *testing.T) {
	prov := &identityPublishingProvider{identity: map[string][]string{
		"fake_thing": {"cluster", "name"},
	}}
	got := lookupHintText(prov, "ubiquex/fake", "fake_thing")
	if !strings.Contains(got, `"cluster" and "name"`) {
		t.Fatalf("prose does not read naturally for two attributes: %v", got)
	}
	if !strings.Contains(got, `--lookup '{"cluster":"<cluster>","name":"<name>"}'`) {
		t.Fatalf("the runnable shape is wrong for two attributes: %v", got)
	}
}

// TestLookupHintText_PublishedIdentityBeatsLookuphints pins the precedence
// deliberately rather than leaving it to call order.
//
// aws_s3_bucket is one of core/lookuphints' three hand-curated entries. A
// provider that publishes a real identity map knows more: the curated
// table names the misleading attribute a user might have reached for,
// while the map names the attributes that actually identify the resource,
// and it covers every type rather than three.
func TestLookupHintText_PublishedIdentityBeatsLookuphints(t *testing.T) {
	prov := &identityPublishingProvider{identity: map[string][]string{
		"aws_s3_bucket": {"bucket"},
	}}
	got := lookupHintText(prov, "hashicorp/aws", "aws_s3_bucket")
	if !strings.Contains(got, `--lookup '{"bucket":"<bucket>"}'`) {
		t.Fatalf("expected the published identity to win: %v", got)
	}
	if strings.Contains(got, `must include "id"`) {
		t.Fatalf("lookuphints won over a provider that could answer directly: %v", got)
	}
}

// TestLookupHintText_CannotSayFallsThrough covers the absent cases, which
// have to stay real. A snapshot is allowed to publish no identity map at
// all, and a map is allowed to omit a type. Neither means "this type has
// no identity", so neither may produce an assertion about one.
func TestLookupHintText_CannotSayFallsThrough(t *testing.T) {
	t.Run("type missing from a published map", func(t *testing.T) {
		prov := &identityPublishingProvider{identity: map[string][]string{
			"aws_sqs_queue": {"queue_url"},
		}}
		got := lookupHintText(prov, "hashicorp/aws", "aws_s3_bucket")
		// Falls through to lookuphints, which does know this one.
		if !strings.Contains(got, `must include "id"`) {
			t.Fatalf("expected the lookuphints answer for a type the map omits: %v", got)
		}
	})

	t.Run("provider publishes nothing", func(t *testing.T) {
		got := lookupHintText(&fakeProvider{}, "hashicorp/aws", "aws_s3_bucket")
		if !strings.Contains(got, `must include "id"`) {
			t.Fatalf("expected the lookuphints answer when nothing is published: %v", got)
		}
	})

	t.Run("empty attribute list is not an answer", func(t *testing.T) {
		prov := &identityPublishingProvider{identity: map[string][]string{
			"aws_totally_unknown_type": {},
		}}
		got := lookupHintText(prov, "hashicorp/aws", "aws_totally_unknown_type")
		if !strings.Contains(got, "check aws_totally_unknown_type's provider schema") {
			t.Fatalf("an empty list must mean cannot-say, not an assertion: %v", got)
		}
	})
}

// TestRunScan_UnreadableResource_TeachesTheRealLookup runs the whole scan
// path rather than the helper, so the hint is proven to actually reach the
// error a user sees.
func TestRunScan_UnreadableResource_TeachesTheRealLookup(t *testing.T) {
	l := Open(t.TempDir())
	prov := &identityPublishingProvider{identity: map[string][]string{
		"aws_s3_bucket": {"bucket"},
	}}

	_, err := RunScan(context.Background(), prov, l, ScanRequest{
		Address:      testAddr(),
		CurrentState: json.RawMessage(`{"id":"wrong-shape"}`),
	})
	if err == nil {
		t.Fatal("expected an error for an unreadable resource")
	}
	if !strings.Contains(err.Error(), `--lookup '{"bucket":"<bucket>"}'`) {
		t.Fatalf("the runnable lookup did not reach the user-facing error: %v", err)
	}
}

// ownershipFake answers core.AttrOwnership for a fixed set of
// provider-owned attribute names.
type ownershipFake struct{ owned map[string]bool }

func (f ownershipFake) IsAttrProviderOwned(attrName string) bool { return f.owned[attrName] }

// TestFilterNormalizationNoise_OptionalComputedStillReportsDrift is
// UBI-268's reason for splitting one predicate into two.
//
// A null-to-value transition is uninteresting only when nobody could have
// set the attribute: the provider owns it outright, so the value appearing
// is it materializing its own. That is Computed AND NOT Optional.
//
// For an Optional+Computed attribute a user COULD have set it, so a value
// appearing where the ledger recorded none is exactly the drift they would
// want reported. The two were one predicate until now, which was harmless
// only because the CloudFormation source emitted no Optional+Computed
// attributes at all. Fixing that puts roughly 8,000 AWS attributes in the
// second category at once, and without this split every one of them would
// have silently stopped reporting this kind of drift.
func TestFilterNormalizationNoise_OptionalComputedStillReportsDrift(t *testing.T) {
	before := map[string]json.RawMessage{"visibility_timeout": json.RawMessage(`null`)}
	after := map[string]json.RawMessage{"visibility_timeout": json.RawMessage(`300`)}

	t.Run("provider-owned outright is materialization", func(t *testing.T) {
		// Computed and not Optional: the region-after-create shape UBI-63
		// built this filter for. Nobody could have set it, so nothing to
		// report.
		fb, fa := FilterNormalizationNoise(before, after,
			ownershipFake{owned: map[string]bool{"visibility_timeout": true}})
		if len(fb) != 0 || len(fa) != 0 {
			t.Fatalf("expected the transition to be filtered as materialization, got before=%v after=%v", fb, fa)
		}
	})

	t.Run("optional and computed is real drift", func(t *testing.T) {
		// Optional+Computed answers false here. Someone could have set
		// this out of band, and this is the only signal that they did.
		fb, fa := FilterNormalizationNoise(before, after,
			ownershipFake{owned: map[string]bool{}})
		if len(fb) == 0 || len(fa) == 0 {
			t.Fatal("an attribute a user could have set must still report drift when a value appears where the ledger recorded none")
		}
		if string(fa["visibility_timeout"]) != "300" {
			t.Fatalf("the reported drift lost its value: %v", fa)
		}
	})
}
