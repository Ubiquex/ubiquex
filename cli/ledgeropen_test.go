package cli

import (
	"context"
	"strings"
	"testing"

	_ "gocloud.dev/blob/memblob"
)

func TestOpenLedgerForStack_GitDefault_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	ledger, closeFn, err := openLedgerForStack(context.Background(), dir, "", &Config{})
	if err != nil {
		t.Fatalf("openLedgerForStack: %v", err)
	}
	defer closeFn()
	if ledger.Dir() != dir {
		t.Fatalf("Dir() = %q, want %q -- empty store must mean the unchanged git-directory path", ledger.Dir(), dir)
	}
}

func TestOpenLedgerForStack_GitExplicit(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{Ledger: LedgerConfig{Store: "git"}}
	ledger, closeFn, err := openLedgerForStack(context.Background(), dir, "", cfg)
	if err != nil {
		t.Fatalf("openLedgerForStack: %v", err)
	}
	defer closeFn()
	if ledger.Dir() != dir {
		t.Fatalf("Dir() = %q, want %q", ledger.Dir(), dir)
	}
}

func TestOpenLedgerForStack_RemoteWithoutStack_Refused(t *testing.T) {
	cfg := &Config{Ledger: LedgerConfig{Store: "s3://acme-ledger"}}
	_, _, err := openLedgerForStack(context.Background(), ".", "", cfg)
	if err == nil {
		t.Fatal("expected an error -- a remote store requires --stack to open at all")
	}
	if !strings.Contains(err.Error(), "--stack") {
		t.Fatalf("expected the error to name --stack, got: %v", err)
	}
}

func TestOpenLedgerForStack_RemoteWithStack_OpensAtDerivedAddress(t *testing.T) {
	cfg := &Config{Ledger: LedgerConfig{Store: "mem://acme-ledger"}}
	ledger, closeFn, err := openLedgerForStack(context.Background(), ".", "payments", cfg)
	if err != nil {
		t.Fatalf("openLedgerForStack: %v", err)
	}
	defer closeFn()
	// A remote-store Ledger has no "Dir" of its own -- confirms this
	// really did go through the LedgerStore path, not silently fall back
	// to git-local.
	if ledger.Dir() != "" {
		t.Fatalf("Dir() = %q, want empty for a remote-store-backed Ledger", ledger.Dir())
	}
	head, err := ledger.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != "" {
		t.Fatalf("Head() = %q, want empty for a freshly-opened store", head)
	}
}
