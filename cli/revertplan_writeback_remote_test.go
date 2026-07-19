package cli

import (
	"strings"
	"testing"
)

// UBI-32: `ubx revert-plan`/`ubx writeback` now read `.ubx/config`'s own
// `[ledger]` table exactly like `ubx why`/`ubx ship` already did -- both
// take a bare proposal ID, which carries no stack of its own, so both
// gained the identical `--stack` flag those commands already have.

func TestRevertPlan_RemoteStore_RequiresStack(t *testing.T) {
	storeName, _ := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	out, err := runUbx(t, nil, "revert-plan", "3ff1769e2c4d2cadc8e492cce9ae5b465b9263b3f39a5aae946617ae8fc028c0", "--ledger-dir", ".")
	if err == nil {
		t.Fatalf("expected an error -- a bare proposal-id argument against a remote store needs --stack, got: %s", out)
	}
	if !strings.Contains(err.Error(), "--stack") {
		t.Fatalf("expected the error to name --stack, got: %v", err)
	}
}

func TestRevertPlan_RemoteStore_WithStack_ReadsFromStore(t *testing.T) {
	storeName, _ := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	id := acceptRevertPlanProposal(t, ".")

	out, err := runUbx(t, nil, "revert-plan", id, "--stack", "s", "--ledger-dir", ".")
	if err != nil {
		t.Fatalf("ubx revert-plan: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "--- plan ---") {
		t.Fatalf("expected a plan section read back from the remote store, got: %s", out)
	}
}

func TestWriteback_RemoteStore_RequiresStack(t *testing.T) {
	storeName, _ := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	out, err := runUbx(t, nil, "writeback", "3ff1769e2c4d2cadc8e492cce9ae5b465b9263b3f39a5aae946617ae8fc028c0", "--ledger-dir", ".")
	if err == nil {
		t.Fatalf("expected an error -- a bare proposal-id argument against a remote store needs --stack, got: %s", out)
	}
	if !strings.Contains(err.Error(), "--stack") {
		t.Fatalf("expected the error to name --stack, got: %v", err)
	}
}

func TestWriteback_RemoteStore_WithStack_ReadsFromStore(t *testing.T) {
	storeName, _ := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	id := acceptWritebackProposal(t, ".")
	tfDir := t.TempDir()
	writeFile(t, tfDir+"/compute.tf", writebackTF)

	out, err := runUbx(t, nil, "writeback", id, "--stack", "s", "--ledger-dir", ".", "--tf-dir", tfDir)
	requireExitCode(t, err, 0, out)
	if !strings.Contains(out, "s.aws_instance.web") {
		t.Fatalf("expected the write-back to have read the proposal back from the remote store, got: %s", out)
	}
}
