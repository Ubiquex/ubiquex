package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerify_EmptyLedger(t *testing.T) {
	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx verify: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 proposal(s) checked") || !strings.Contains(out, "chain: intact") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// acceptLocalProposal accepts a minimal draft (local acceptance) and
// returns its own id and the ledger dir it landed in.
func acceptLocalProposal(t *testing.T, ledgerDir, summary, parent string) string {
	t.Helper()
	proposalPath := filepath.Join(t.TempDir(), "proposal.json")
	writeFile(t, proposalPath, `{
		"schema_version": 1, "stack": "payments", "parent": "`+parent+`", "kind": "change",
		"intent": {"summary": "`+summary+`"}, "delta": {},
		"resolution": {"resolved_at": "2026-07-11T00:00:00Z"},
		"cost_delta": {}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)
	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	return mustExtractID(t, acceptOut)
}

func TestVerify_ValidChain_ExitsZero(t *testing.T) {
	ledgerDir := t.TempDir()
	first := acceptLocalProposal(t, ledgerDir, "first", "")
	acceptLocalProposal(t, ledgerDir, "second", first)

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx verify: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "2 proposal(s) checked") || !strings.Contains(out, "chain: intact") {
		t.Fatalf("unexpected output: %s", out)
	}
	if !strings.Contains(out, "2 convenience-tier (local)") {
		t.Fatalf("expected both local acceptances reported convenience-tier, got: %s", out)
	}
}

// row: tampered proposal byte -- hash breaks, exit 1.
func TestVerify_TamperedProposal_ExitsOne(t *testing.T) {
	ledgerDir := t.TempDir()
	first := acceptLocalProposal(t, ledgerDir, "first", "")
	acceptLocalProposal(t, ledgerDir, "second", first)

	propPath := filepath.Join(ledgerDir, "ledger", "proposals", first+".prop.json")
	raw, err := os.ReadFile(propPath)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(raw), `"summary": "first"`, `"summary": "TAMPERED"`, 1)
	if tampered == string(raw) {
		t.Fatal("tamper replace found nothing to replace -- fixture drifted")
	}
	if err := os.WriteFile(propPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "hash_mismatch") {
		t.Fatalf("expected a hash_mismatch finding, got: %s", out)
	}
	if !strings.Contains(out, "tainted_descendant") {
		t.Fatalf("expected the second proposal flagged as a tainted descendant, got: %s", out)
	}
	if !strings.Contains(out, "chain: BROKEN") {
		t.Fatalf("expected chain: BROKEN, got: %s", out)
	}
}

func TestVerify_JSON_Shape(t *testing.T) {
	ledgerDir := t.TempDir()
	acceptLocalProposal(t, ledgerDir, "first", "")

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir, "--json")
	if err != nil {
		t.Fatalf("ubx verify --json: %v\noutput: %s", err, out)
	}
	var payload struct {
		Format           int  `json:"format"`
		ProposalsChecked int  `json:"proposals_checked"`
		ChainIntact      bool `json:"chain_intact"`
		Acceptance       struct {
			Derived      int `json:"derived"`
			Convenience  int `json:"convenience_tier"`
			Inconclusive int `json:"inconclusive"`
		} `json:"acceptance"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, out)
	}
	if payload.Format != 1 || payload.ProposalsChecked != 1 || !payload.ChainIntact {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Acceptance.Convenience != 1 {
		t.Fatalf("expected 1 convenience-tier acceptance, got: %+v", payload.Acceptance)
	}
}
