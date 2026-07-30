package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// destroyProposalJSON is a hand-written, minimally valid kind:"change"
// proposal carrying exactly one delta.destroys entry -- the same
// hand-written-JSON convention TestAcceptThenWhy already uses for a create,
// with the destroy_target resolution input core.Validate now requires
// (docs/schema.md's "Amendment: destroys").
const destroyProposalJSON = `{
	"schema_version": 2,
	"stack": "payments",
	"parent": "",
	"kind": "change",
	"intent": {"summary": "decommission the old widget"},
	"delta": {
		"destroys": [
			{
				"address": {"stack": "payments", "type": "aws_db_instance", "name": "old-db"},
				"state": {"id": "old-db"}
			}
		]
	},
	"resolution": {
		"resolved_at": "2026-07-18T00:00:00Z",
		"inputs": [
			{
				"kind": "destroy_target",
				"resource": "payments.aws_db_instance.old-db",
				"observed_hash": "sha256:deadbeef",
				"lookup": {"id": "old-db"}
			}
		]
	},
	"cost_delta": {"monthly_usd": 0},
	"blast_radius": {"creates": 0, "modifies": 0, "destroys": 1},
	"status": "draft"
}`

func TestAccept_DestroysRequireConfirmFlag_Refused(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "destroy.json")
	writeFile(t, proposalPath, destroyProposalJSON)

	_, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err == nil {
		t.Fatal("expected a non-nil error (exit code 1) when destroys aren't confirmed")
	}
	if exitCode(err) != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode(err))
	}
	if !strings.Contains(err.Error(), "--confirm-destroys") {
		t.Fatalf("expected the error to name --confirm-destroys, got: %v", err)
	}

	// Nothing was written to the ledger.
	ledger := core.Open(ledgerDir)
	head, err := ledger.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head != "" {
		t.Fatalf("ledger head = %q, want empty -- the refused proposal must never be appended", head)
	}
}

func TestAccept_DestroysWithConfirmFlag_Accepted(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "destroy.json")
	writeFile(t, proposalPath, destroyProposalJSON)

	out, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir, "--confirm-destroys")
	if err != nil {
		t.Fatalf("ubx accept --confirm-destroys: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "accepted") {
		t.Fatalf("expected an \"accepted\" confirmation, got: %s", out)
	}

	ledger := core.Open(ledgerDir)
	head, err := ledger.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if head == "" {
		t.Fatal("ledger head is empty -- the confirmed destroy proposal should have been appended")
	}
}

func TestAccept_NoDestroys_NeverNeedsConfirmFlag(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "create.json")
	writeFile(t, proposalPath, `{
		"schema_version": 1,
		"stack": "payments",
		"parent": "",
		"kind": "change",
		"intent": {"summary": "a plain create, no destroys"},
		"delta": {
			"creates": [
				{"stack": "payments", "type": "aws_db_instance", "name": "payments-db"}
			]
		},
		"resolution": {"resolved_at": "2026-07-18T00:00:00Z"},
		"cost_delta": {"monthly_usd": 0},
		"blast_radius": {"creates": 1, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)

	out, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (no destroys, no flag): %v\noutput: %s", err, out)
	}
}
