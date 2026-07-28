package core

import (
	"encoding/json"
	"testing"
)

func TestAddresses_EmptyLedger(t *testing.T) {
	l := Open(t.TempDir())
	entries, err := l.Addresses("", false)
	if err != nil {
		t.Fatalf("Addresses: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0 for an empty ledger", len(entries))
	}
}

func TestAddresses_ActiveOnly_MatchesFleet(t *testing.T) {
	l := Open(t.TempDir())
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "main"}
	result := json.RawMessage(`{"id":"db-main","arn":"arn:aws:rds:us-east-1:1:db:main"}`)
	shipChangeCreateWithProviderForTest(t, l, addr, result, &ProviderRef{Source: "hashicorp/aws", Version: "6.60.0"})

	entries, err := l.Addresses("", false)
	if err != nil {
		t.Fatalf("Addresses: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Address != addr {
		t.Fatalf("Address = %+v, want %+v", e.Address, addr)
	}
	if e.Tombstoned {
		t.Fatalf("Tombstoned = true, want false for an active resource")
	}
	if e.Provider == nil || e.Provider.Source != "hashicorp/aws" || e.Provider.Version != "6.60.0" {
		t.Fatalf("Provider = %+v, want hashicorp/aws@6.60.0", e.Provider)
	}
}

func TestAddresses_Destroyed_ExcludedByDefault(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	adoptForTest(t, l, addr, json.RawMessage(`{"id":"b-1"}`))
	destroy := shipChangeDestroyForTest(t, l, addr, json.RawMessage(`{"id":"b-1"}`), "destroyed", true, true)

	entries, err := l.Addresses("", false)
	if err != nil {
		t.Fatalf("Addresses: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0 -- a tombstoned address is excluded by default", len(entries))
	}

	all, err := l.Addresses("", true)
	if err != nil {
		t.Fatalf("Addresses(--all): %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d entries, want 1 with --all", len(all))
	}
	e := all[0]
	if !e.Tombstoned {
		t.Fatalf("Tombstoned = false, want true")
	}
	if e.TombstonedBy != destroy.ID {
		t.Fatalf("TombstonedBy = %q, want %q", e.TombstonedBy, destroy.ID)
	}
}

func TestAddresses_RecreateAfterDestroy_ActiveAgain(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	adoptForTest(t, l, addr, json.RawMessage(`{"id":"b-1"}`))
	shipChangeDestroyForTest(t, l, addr, json.RawMessage(`{"id":"b-1"}`), "destroyed", true, true)
	adoptForTest(t, l, addr, json.RawMessage(`{"id":"b-2"}`))

	entries, err := l.Addresses("", false)
	if err != nil {
		t.Fatalf("Addresses: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 -- a re-created address is active again", len(entries))
	}
	if entries[0].Tombstoned {
		t.Fatalf("Tombstoned = true, want false after re-create")
	}
}

func TestAddresses_StackFilter(t *testing.T) {
	l := Open(t.TempDir())
	adoptForTest(t, l, Address{Stack: "payments", Type: "aws_s3_bucket", Name: "a"}, json.RawMessage(`{"id":"1"}`))
	adoptForTest(t, l, Address{Stack: "networking", Type: "aws_s3_bucket", Name: "b"}, json.RawMessage(`{"id":"2"}`))

	entries, err := l.Addresses("networking", false)
	if err != nil {
		t.Fatalf("Addresses: %v", err)
	}
	if len(entries) != 1 || entries[0].Address.Stack != "networking" {
		t.Fatalf("Addresses(%q) = %+v, want exactly one networking address", "networking", entries)
	}
}

func TestAddresses_AdoptedResource_NilProvider(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	adoptForTest(t, l, addr, json.RawMessage(`{"id":"b-1"}`))

	entries, err := l.Addresses("", false)
	if err != nil {
		t.Fatalf("Addresses: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Provider != nil {
		t.Fatalf("Provider = %+v, want nil for a resource this ledger only ever adopted", entries[0].Provider)
	}
}
