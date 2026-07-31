package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// shipChangeCreateWithProviderForTest mirrors shipChangeCreateForTest
// (ubi29_test.go) but sets a "provider" key on the create node
// (docs/schema.md's "Amendment: the provider field returns", UBI-43) --
// this session's own addition to Fleet, exercised against a real accepted
// + sealed apply record, never a hand-waved shortcut.
func shipChangeCreateWithProviderForTest(t *testing.T, l *Ledger, addr Address, result json.RawMessage, provider *ProviderRef) *Proposal {
	t.Helper()
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"provider": provider,
		"config":   map[string]interface{}{"name": addr.Name},
	})
	if err != nil {
		t.Fatal(err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindChange,
		Intent:        Intent{Summary: "test create " + addr.String()},
		Delta:         Delta{Creates: []json.RawMessage{node}},
		Resolution:    Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339)},
		CostDelta:     CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius:   BlastRadius{Creates: 1},
		Status:        StatusDraft,
	}
	accepted, err := Accept(l, p)
	if err != nil {
		t.Fatalf("accept change: %v", err)
	}

	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ra := &ResourceApply{
		Address: addr,
		Transitions: []Transition{
			{State: ResourcePending, At: now},
			{State: ResourceInFlight, At: now},
			{State: ResourceApplied, At: now},
		},
		ProviderResult: result,
		Lookup:         DeriveLookupFromResult(result, nil),
	}
	rec.Resources = append(rec.Resources, ra)
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	if _, err := l.SealApply(rec, ApplySummary{
		Outcome: "applied", StartedAt: now, FinishedAt: now, ResourcesApplied: 1,
	}); err != nil {
		t.Fatalf("seal apply: %v", err)
	}
	return accepted
}

func TestFleet_Provider_FromShippedCreate(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "main"}
	result := json.RawMessage(`{"id":"db-main","arn":"arn:aws:rds:us-east-1:1:db:main"}`)
	shipChangeCreateWithProviderForTest(t, l, addr, result, &ProviderRef{Source: "hashicorp/aws", Version: "6.60.0"})

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	prov := entries[0].Provider
	if prov == nil || prov.Source != "hashicorp/aws" || prov.Version != "6.60.0" {
		t.Fatalf("Provider = %+v, want hashicorp/aws@6.60.0", prov)
	}
}

func TestFleet_Provider_FromModify(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "main"}
	result := json.RawMessage(`{"id":"db-main","instance_class":"db.t3.small"}`)
	shipChangeCreateWithProviderForTest(t, l, addr, result, &ProviderRef{Source: "hashicorp/aws", Version: "6.60.0"})

	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	modify := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindChange,
		Intent:        Intent{Summary: "resize main"},
		Delta: Delta{Modifies: []Modification{
			{
				Target:   addr,
				Before:   map[string]json.RawMessage{"instance_class": json.RawMessage(`"db.t3.small"`)},
				After:    map[string]json.RawMessage{"instance_class": json.RawMessage(`"db.t3.large"`)},
				Provider: &ProviderRef{Source: "hashicorp/aws", Version: "6.61.0"},
			},
		}},
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: "deadbeef", Lookup: json.RawMessage(`{"id":"db-main"}`)},
			},
		},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{Modifies: 1},
		Status:      StatusDraft,
	}
	if _, err := Accept(l, modify); err != nil {
		t.Fatalf("accept modify: %v", err)
	}

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	// The modify's own recorded version (re-pinned since the original
	// create) wins -- the CURRENT touch's own provider, not stale history.
	prov := entries[0].Provider
	if prov == nil || prov.Source != "hashicorp/aws" || prov.Version != "6.61.0" {
		t.Fatalf("Provider = %+v, want hashicorp/aws@6.61.0 (the modify's own recorded version)", prov)
	}
}

func TestFleet_Provider_NilForAdoptedResource(t *testing.T) {
	l, _, res := driftToStaging(t)
	adopt, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	if _, err := Accept(l, adopt); err != nil {
		t.Fatalf("Accept (drift_adopt): %v", err)
	}

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Provider != nil {
		t.Fatalf("Provider = %+v, want nil -- an adopted/drift-recorded resource never has one", entries[0].Provider)
	}
}

// TestFleet_Provider_PersistsThroughLaterDrift proves the fallback
// docs/executor.md's own "Scan/status/fleet" section names: a
// drift_adopt (core/scan.go's own Modification entries never carry a
// provider at all) touching an address AFTER its own real create must
// never erase that create's own recorded provider -- the identical
// "most recent wins, falls back to the shipped create's own value"
// precedence Lookup already established, applied here to Provider too.
func TestFleet_Provider_PersistsThroughLaterDrift(t *testing.T) {
	l := Open(t.TempDir())
	// fakeProvider's own Schema() only ever advertises "aws_s3_bucket"
	// (scan_test.go) -- RunScan validates the type against it, so this
	// test uses that type; the point under test is provider persistence
	// across a later drift touch, not the resource's own type.
	addr := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "main"}
	result := json.RawMessage(`{"id":"db-main","tags":{"env":"prod"}}`)
	shipChangeCreateWithProviderForTest(t, l, addr, result, &ProviderRef{Source: "hashicorp/aws", Version: "6.60.0"})

	// A real out-of-band tag change, recorded via the ordinary
	// scan/drift_adopt path -- never resolver-produced, never carries a
	// provider field of its own.
	fp := &fakeProvider{state: json.RawMessage(`{"id":"db-main","tags":{"env":"prod","owner":"sre"}}`)}
	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"db-main"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	drift, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	if _, err := Accept(l, drift); err != nil {
		t.Fatalf("Accept (drift_adopt): %v", err)
	}

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Kind != KindDriftAdopt {
		t.Fatalf("Kind = %q, want %q (the latest touch)", e.Kind, KindDriftAdopt)
	}
	prov := e.Provider
	if prov == nil || prov.Source != "hashicorp/aws" || prov.Version != "6.60.0" {
		t.Fatalf("Provider = %+v, want hashicorp/aws@6.60.0 to survive the provider-less drift touch", prov)
	}
}

func TestFleet_Provider_FromAcceptedUnshippedDestroy(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "main"}
	result := json.RawMessage(`{"id":"db-main"}`)
	shipChangeCreateWithProviderForTest(t, l, addr, result, &ProviderRef{Source: "hashicorp/aws", Version: "6.60.0"})

	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	destroy := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          KindChange,
		Intent:        Intent{Summary: "decommission main"},
		Delta: Delta{Destroys: []DestroyEntry{
			{Address: addr, State: json.RawMessage(`{"id":"db-main"}`), Provider: &ProviderRef{Source: "hashicorp/aws", Version: "6.61.0"}},
		}},
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{Kind: "destroy_target", Resource: addr.String(), ObservedHash: "deadbeef", Lookup: json.RawMessage(`{"id":"db-main"}`)},
			},
		},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{Destroys: 1},
		Status:      StatusDraft,
	}
	if _, err := Accept(l, destroy); err != nil {
		t.Fatalf("accept destroy: %v", err)
	}

	// Not yet shipped -- FoldState's own tombstone-fold never fires, so
	// this address is still fully visible in Fleet, now showing the
	// pending destroy's own (freshly re-inferred) provider.
	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (an unshipped destroy never tombstones)", len(entries))
	}
	prov := entries[0].Provider
	if prov == nil || prov.Source != "hashicorp/aws" || prov.Version != "6.61.0" {
		t.Fatalf("Provider = %+v, want hashicorp/aws@6.61.0 (the pending destroy's own recorded provider)", prov)
	}
}
