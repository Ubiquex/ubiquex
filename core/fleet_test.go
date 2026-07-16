package core

import (
	"context"
	"encoding/json"
	"testing"
)

func TestFleet_EmptyLedger(t *testing.T) {
	l := Open(t.TempDir())
	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0 for an empty ledger", len(entries))
	}
}

func TestFleet_LatestProposalWinsPerAddress(t *testing.T) {
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
	e := entries[0]
	if e.Address != testAddr() {
		t.Fatalf("Address = %+v, want %+v", e.Address, testAddr())
	}
	// The latest proposal for this address is the drift_adopt just
	// accepted, not the earlier adoption.
	if e.Kind != KindDriftAdopt {
		t.Fatalf("Kind = %q, want %q (the latest proposal, not the original adoption)", e.Kind, KindDriftAdopt)
	}
	if e.ProposalID != adopt.ID {
		t.Fatalf("ProposalID = %q, want %q", e.ProposalID, adopt.ID)
	}
	if e.AcceptedAt == "" {
		t.Fatal("AcceptedAt is empty")
	}
	if string(e.Lookup) == "" {
		t.Fatal("Lookup is empty, want the recorded lookup key")
	}
}

func TestFleet_MultiStack(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	// Two different stacks, sharing the same ledger directory --
	// docs/architecture.md's confirmed (not assumed) finding that this
	// chains correctly since GenerateProposal always reads the live head.
	fpPayments := &fakeProvider{state: json.RawMessage(`{"id":"a","tags":{"env":"prod"}}`)}
	addrPayments := Address{Stack: "payments", Type: "aws_s3_bucket", Name: "a"}
	resPayments, err := RunScan(context.Background(), fpPayments, l, ScanRequest{Address: addrPayments, CurrentState: json.RawMessage(`{"id":"a"}`)})
	if err != nil {
		t.Fatalf("RunScan (payments): %v", err)
	}
	proposalPayments, err := GenerateProposal(l, "payments", resPayments)
	if err != nil {
		t.Fatalf("GenerateProposal (payments): %v", err)
	}
	if _, err := Accept(l, proposalPayments); err != nil {
		t.Fatalf("Accept (payments): %v", err)
	}

	fpNetwork := &fakeProvider{state: json.RawMessage(`{"id":"b","tags":{"env":"prod"}}`)}
	addrNetwork := Address{Stack: "network", Type: "aws_s3_bucket", Name: "b"}
	resNetwork, err := RunScan(context.Background(), fpNetwork, l, ScanRequest{Address: addrNetwork, CurrentState: json.RawMessage(`{"id":"b"}`)})
	if err != nil {
		t.Fatalf("RunScan (network): %v", err)
	}
	proposalNetwork, err := GenerateProposal(l, "network", resNetwork)
	if err != nil {
		t.Fatalf("GenerateProposal (network): %v", err)
	}
	if _, err := Accept(l, proposalNetwork); err != nil {
		t.Fatalf("Accept (network): %v", err)
	}

	all, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet(\"\"): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d entries across both stacks, want 2", len(all))
	}

	onlyPayments, err := l.Fleet("payments")
	if err != nil {
		t.Fatalf("Fleet(\"payments\"): %v", err)
	}
	if len(onlyPayments) != 1 || onlyPayments[0].Address != addrPayments {
		t.Fatalf("Fleet(\"payments\") = %+v, want exactly [%+v]", onlyPayments, addrPayments)
	}

	onlyNetwork, err := l.Fleet("network")
	if err != nil {
		t.Fatalf("Fleet(\"network\"): %v", err)
	}
	if len(onlyNetwork) != 1 || onlyNetwork[0].Address != addrNetwork {
		t.Fatalf("Fleet(\"network\") = %+v, want exactly [%+v]", onlyNetwork, addrNetwork)
	}
}

func TestFleet_SortedByAddress(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	for _, name := range []string{"zeta", "alpha", "mid"} {
		fp := &fakeProvider{state: json.RawMessage(`{"id":"x","tags":{"env":"prod"}}`)}
		addr := Address{Stack: "payments", Type: "aws_s3_bucket", Name: name}
		res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"x"}`)})
		if err != nil {
			t.Fatalf("RunScan (%s): %v", name, err)
		}
		p, err := GenerateProposal(l, "payments", res)
		if err != nil {
			t.Fatalf("GenerateProposal (%s): %v", name, err)
		}
		if _, err := Accept(l, p); err != nil {
			t.Fatalf("Accept (%s): %v", name, err)
		}
	}

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	want := []string{"payments.aws_s3_bucket.alpha", "payments.aws_s3_bucket.mid", "payments.aws_s3_bucket.zeta"}
	for i, w := range want {
		if entries[i].Address.String() != w {
			t.Fatalf("entries[%d] = %q, want %q (sorted order)", i, entries[i].Address.String(), w)
		}
	}
}

func TestFleet_SkipsUnparseableAddressString(t *testing.T) {
	l := Open(t.TempDir())

	// Hand-crafted, never through Accept: a resolution.inputs entry whose
	// Resource doesn't parse as a canonical <stack>.<type>.<name> address
	// -- must be skipped, not crash the whole walk.
	p := &Proposal{
		SchemaVersion: SchemaVersion,
		ID:            "0000000000000000000000000000000000000000000000000000000000000000",
		Stack:         "payments",
		Kind:          KindChange,
		Intent:        Intent{Summary: "x"},
		Resolution: Resolution{
			ResolvedAt: "2026-07-16T00:00:00Z",
			Inputs: []ResolutionInput{
				{Kind: "live_state", Resource: "not-a-valid-address", ObservedHash: "deadbeef"},
			},
		},
		Status: StatusAccepted,
	}
	if err := l.Append(p); err != nil {
		t.Fatal(err)
	}

	entries, err := l.Fleet("")
	if err != nil {
		t.Fatalf("Fleet: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0 (the malformed address must be skipped, not surfaced)", len(entries))
	}
}
