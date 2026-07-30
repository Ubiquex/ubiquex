package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestAcceptThenWhy exercises the whole Slice 2 loop through the CLI
// surface: a hand-written proposal JSON file, `ubx accept` (local signing +
// ledger append), then `ubx why` reading it back.
func TestAcceptThenWhy(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "proposal.json")

	writeFile(t, proposalPath, `{
		"schema_version": 1,
		"stack": "payments",
		"parent": "",
		"kind": "change",
		"intent": {
			"summary": "postgres for payments, modeled on staging",
			"sources": [
				{"kind": "dialogue", "ref": "d-99f2", "content_hash": "sha256:abc123"}
			]
		},
		"delta": {
			"creates": [
				{"stack": "payments", "type": "aws_db_instance", "name": "payments-db"}
			]
		},
		"resolution": {"resolved_at": "2026-07-10T00:00:00Z"},
		"cost_delta": {"monthly_usd": 59},
		"blast_radius": {"creates": 1, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)

	acceptOut := &bytes.Buffer{}
	acceptCmd := NewRootCmd()
	acceptCmd.SetOut(acceptOut)
	acceptCmd.SetErr(acceptOut)
	acceptCmd.SetArgs([]string{"accept", proposalPath, "--ledger-dir", ledgerDir})
	if err := acceptCmd.Execute(); err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut.String())
	}

	matches := regexp.MustCompile(`accepted ([0-9a-f]{64})`).FindStringSubmatch(acceptOut.String())
	if matches == nil {
		t.Fatalf("could not find accepted proposal ID in output: %s", acceptOut.String())
	}
	id := matches[1]

	whyOut := &bytes.Buffer{}
	whyCmd := NewRootCmd()
	whyCmd.SetOut(whyOut)
	whyCmd.SetErr(whyOut)
	whyCmd.SetArgs([]string{"why", id, "--ledger-dir", ledgerDir})
	if err := whyCmd.Execute(); err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, whyOut.String())
	}

	got := whyOut.String()
	if !strings.Contains(got, "postgres for payments") {
		t.Fatalf("why output missing intent summary: %s", got)
	}
	if !strings.Contains(got, "d-99f2") {
		t.Fatalf("why output missing intent source: %s", got)
	}
	if !strings.Contains(got, "local") {
		t.Fatalf("why output missing acceptance method: %s", got)
	}

	// Also verify directly against core.Ledger that the on-disk record is
	// exactly what accept reported.
	ledger := core.Open(ledgerDir)
	p, err := ledger.Read(id)
	if err != nil {
		t.Fatalf("core.Ledger.Read: %v", err)
	}
	if p.Status != core.StatusAccepted {
		t.Fatalf("status = %q, want %q", p.Status, core.StatusAccepted)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
