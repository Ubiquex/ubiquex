package cli

import (
	"strings"
	"testing"
)

// TestConfigCmd_DerivedLedgerAddress is UBI-32 Arc B's own addressing
// rule (docs/architecture.md -- <base store>/<stack>/, derived, never
// declared) surfaced in the provenance view: `ubx config` prints the
// full address a remote [ledger] store resolves to for a given stack,
// not just the raw configured store string.
func TestConfigCmd_DerivedLedgerAddress(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config", `
stack = "payments"

[ledger]
store = "s3://acme-ledger/acme/prod/"
`)
	withConfigSearchDir(t, dir)

	out, err := runUbx(t, nil, "config")
	if err != nil {
		t.Fatalf("ubx config: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, `ledger address (stack "payments"): s3://acme-ledger/acme/prod/payments/`) {
		t.Fatalf("expected the derived ledger address for stack payments, got:\n%s", out)
	}
}

func TestConfigCmd_DerivedLedgerAddress_ExplicitStackFlag(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config", `
stack = "payments"

[ledger]
store = "s3://acme-ledger/acme/prod/"
`)
	withConfigSearchDir(t, dir)

	out, err := runUbx(t, nil, "config", "--stack", "network")
	if err != nil {
		t.Fatalf("ubx config: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, `ledger address (stack "network"): s3://acme-ledger/acme/prod/network/`) {
		t.Fatalf("expected --stack to override config's own default stack for the derived address, got:\n%s", out)
	}
}

func TestConfigCmd_DerivedLedgerAddress_NoStackKnown(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config", `
[ledger]
store = "s3://acme-ledger/acme/prod/"
`)
	withConfigSearchDir(t, dir)

	out, err := runUbx(t, nil, "config")
	if err != nil {
		t.Fatalf("ubx config: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "no stack is known to derive it against") {
		t.Fatalf("expected a clear explanation that no stack is known, got:\n%s", out)
	}
}

func TestConfigCmd_NoDerivedAddressForGitStore(t *testing.T) {
	dir := t.TempDir()
	writeCascadeFile(t, dir, "config", `stack = "payments"`)
	withConfigSearchDir(t, dir)

	out, err := runUbx(t, nil, "config")
	if err != nil {
		t.Fatalf("ubx config: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "ledger address") {
		t.Fatalf("expected no derived-address line for the default git store, got:\n%s", out)
	}
}
