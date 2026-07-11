package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core"
)

const writebackDriftProposal = `{
	"schema_version": 1,
	"stack": "s",
	"parent": "",
	"kind": "drift_adopt",
	"intent": {"summary": "record drift on s.aws_instance.web observed outside the ledger"},
	"delta": {
		"modifies": [
			{
				"target": {"stack": "s", "type": "aws_instance", "name": "web"},
				"before": {"instance_type": "t3.medium"},
				"after": {"instance_type": "t3.large"}
			}
		]
	},
	"resolution": {
		"resolved_at": "2026-07-11T00:00:00Z",
		"inputs": [
			{"kind": "live_state", "resource": "s.aws_instance.web", "observed_hash": "sha256:abc"}
		]
	},
	"cost_delta": {"monthly_usd": 0},
	"blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
	"status": "draft"
}`

const writebackTF = `resource "aws_instance" "web" {
  instance_type = "t3.medium"
  ami           = var.ami_id
}
`

func acceptWritebackProposal(t *testing.T, ledgerDir string) string {
	t.Helper()
	proposalPath := filepath.Join(t.TempDir(), "drift.json")
	writeFile(t, proposalPath, writebackDriftProposal)
	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	return mustExtractID(t, acceptOut)
}

func TestWriteback_DryRun_PrintsDiffLeavesFileUntouched(t *testing.T) {
	ledgerDir := t.TempDir()
	id := acceptWritebackProposal(t, ledgerDir)

	tfDir := t.TempDir()
	tfPath := filepath.Join(tfDir, "compute.tf")
	writeFile(t, tfPath, writebackTF)

	out, err := runUbx(t, nil, "writeback", id, "--ledger-dir", ledgerDir, "--tf-dir", tfDir)
	if err != nil {
		t.Fatalf("ubx writeback: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "applied: instance_type") {
		t.Errorf("expected instance_type reported applied, got: %s", out)
	}
	if !strings.Contains(out, "-  instance_type = \"t3.medium\"") || !strings.Contains(out, "+  instance_type = \"t3.large\"") {
		t.Errorf("expected a unified diff showing the change, got: %s", out)
	}

	onDisk, err := os.ReadFile(tfPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != writebackTF {
		t.Fatalf("dry run must not modify the file on disk; got:\n%s", onDisk)
	}
}

func TestWriteback_Write_ActuallyModifiesFile(t *testing.T) {
	ledgerDir := t.TempDir()
	id := acceptWritebackProposal(t, ledgerDir)

	tfDir := t.TempDir()
	tfPath := filepath.Join(tfDir, "compute.tf")
	writeFile(t, tfPath, writebackTF)

	out, err := runUbx(t, nil, "writeback", id, "--ledger-dir", ledgerDir, "--tf-dir", tfDir, "--write")
	if err != nil {
		t.Fatalf("ubx writeback --write: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "wrote "+tfPath) {
		t.Errorf("expected a 'wrote' confirmation, got: %s", out)
	}

	onDisk, err := os.ReadFile(tfPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onDisk), `instance_type = "t3.large"`) {
		t.Fatalf("expected the file actually updated, got:\n%s", onDisk)
	}
	if !strings.Contains(string(onDisk), "ami           = var.ami_id") {
		t.Fatalf("expected the untouched attribute to survive, got:\n%s", onDisk)
	}
}

func TestWriteback_DeclinedAttributeReportedNotFatal(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "drift.json")
	writeFile(t, proposalPath, `{
		"schema_version": 1, "stack": "s", "parent": "", "kind": "drift_adopt",
		"intent": {"summary": "x"},
		"delta": {"modifies": [{
			"target": {"stack": "s", "type": "aws_instance", "name": "web"},
			"before": {"ami": "ami-old"},
			"after": {"ami": "ami-new"}
		}]},
		"resolution": {"resolved_at": "2026-07-11T00:00:00Z", "inputs": [
			{"kind": "live_state", "resource": "s.aws_instance.web", "observed_hash": "sha256:abc"}
		]},
		"cost_delta": {}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)
	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v", err)
	}
	id := mustExtractID(t, acceptOut)

	tfDir := t.TempDir()
	writeFile(t, filepath.Join(tfDir, "compute.tf"), writebackTF) // ami = var.ami_id, an expression

	out, err := runUbx(t, nil, "writeback", id, "--ledger-dir", ledgerDir, "--tf-dir", tfDir)
	if err != nil {
		t.Fatalf("a declined attribute must not fail the command: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "declined: ami") {
		t.Fatalf("expected ami reported declined, got: %s", out)
	}
	if !strings.Contains(out, "var.ami_id") {
		t.Fatalf("expected the offending expression named in the report, got: %s", out)
	}
}

func TestWriteback_ResourceBlockAbsent(t *testing.T) {
	ledgerDir := t.TempDir()
	id := acceptWritebackProposal(t, ledgerDir)

	tfDir := t.TempDir()
	writeFile(t, filepath.Join(tfDir, "network.tf"), "resource \"aws_vpc\" \"main\" {\n  cidr_block = \"10.0.0.0/16\"\n}\n")

	out, err := runUbx(t, nil, "writeback", id, "--ledger-dir", ledgerDir, "--tf-dir", tfDir)
	if err == nil {
		t.Fatal("expected an error when no matching resource block exists")
	}
	if !strings.Contains(err.Error(), "could not locate a resource block") {
		t.Fatalf("expected a resource-block-not-found error, got: %v", err)
	}
	if !strings.Contains(out, "no matching resource block found") {
		t.Fatalf("expected the underlying tfwrite error detail printed, got: %s", out)
	}
}

func TestWriteback_RejectsNonDriftAdoptKind(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "proposal.json")
	writeFile(t, proposalPath, draftProposalJSON) // kind "change"
	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v", err)
	}
	id := mustExtractID(t, acceptOut)

	_, err = runUbx(t, nil, "writeback", id, "--ledger-dir", ledgerDir, "--tf-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected writeback to refuse a non-drift_adopt proposal")
	}
	if !strings.Contains(err.Error(), "not drift_adopt") {
		t.Fatalf("expected a kind-mismatch error, got: %v", err)
	}
}

func TestWriteback_RejectsUnacceptedProposal(t *testing.T) {
	ledgerDir := t.TempDir()
	ledger := core.Open(ledgerDir)

	// A drift_adopt proposal written directly to the ledger, never
	// through Accept -- still in draft status.
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		ID:            "0000000000000000000000000000000000000000000000000000000000000000",
		Stack:         "s",
		Kind:          core.KindDriftAdopt,
		Intent:        core.Intent{Summary: "x"},
		Resolution:    core.Resolution{ResolvedAt: "2026-07-11T00:00:00Z"},
		Status:        core.StatusDraft,
	}
	if err := ledger.Append(p); err != nil {
		t.Fatal(err)
	}

	_, err := runUbx(t, nil, "writeback", p.ID, "--ledger-dir", ledgerDir, "--tf-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected writeback to refuse an unaccepted proposal")
	}
	if !strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("expected a not-accepted error, got: %v", err)
	}
}
