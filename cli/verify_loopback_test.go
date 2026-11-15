package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// loopbackFixtureProposal is a minimal proposal that validates and hashes.
func loopbackFixtureProposal() *core.Proposal {
	return &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "payments",
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "postgres for payments"},
		Delta: core.Delta{Creates: []json.RawMessage{
			json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"payments-db","config":{"engine":"postgres"}}`),
		}},
		Resolution:  core.Resolution{ResolvedAt: "2026-11-15T00:00:00Z"},
		BlastRadius: core.BlastRadius{Creates: 1},
		Status:      core.StatusDraft,
	}
}

// acceptViaLoopback records a real loopback acceptance the way the
// approval page will: stage, then accept. Returns the proposal ID and the
// rendered hash.
func acceptViaLoopback(t *testing.T, ledgerDir string) (id, renderedHash string) {
	t.Helper()
	l := core.Open(ledgerDir)
	p := loopbackFixtureProposal()
	h, _, err := l.StageRender(p)
	if err != nil {
		t.Fatalf("StageRender: %v", err)
	}
	accepted, err := core.AcceptFromLoopback(l, p, h)
	if err != nil {
		t.Fatalf("AcceptFromLoopback: %v", err)
	}
	return accepted.ID, h
}

// appendLoopbackRecord appends a loopback acceptance naming whatever
// rendered bytes the test supplies, bypassing AcceptFromLoopback's own
// checks, to stand in for a record written by a different renderer or
// altered afterwards. Returns the proposal ID.
func appendLoopbackRecord(t *testing.T, ledgerDir string, rendered []byte) string {
	t.Helper()
	sum := sha256.Sum256(rendered)
	h := hex.EncodeToString(sum[:])
	dir := filepath.Join(ledgerDir, "ledger", "rendered")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, h+".render"), rendered, 0o644); err != nil {
		t.Fatal(err)
	}
	p := loopbackFixtureProposal()
	id, err := core.Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = id
	p.Status = core.StatusAccepted
	p.Acceptance = &core.Acceptance{
		Method:       core.MethodLoopback,
		Approvers:    []string{core.LoopbackApprover},
		AcceptedAt:   "2026-11-15T00:00:00Z",
		RenderedHash: h,
	}
	if err := core.Open(ledgerDir).Append(p); err != nil {
		t.Fatalf("Append: %v", err)
	}
	return id
}

type loopbackVerifyJSON struct {
	Acceptance struct {
		Derived      int `json:"derived"`
		Inconclusive int `json:"inconclusive"`
		Checks       []struct {
			ProposalID   string `json:"proposal_id"`
			Method       string `json:"method"`
			OK           bool   `json:"ok"`
			Inconclusive bool   `json:"inconclusive"`
			Outcome      string `json:"outcome"`
			Note         string `json:"note"`
		} `json:"checks"`
	} `json:"acceptance"`
}

func verifyLoopbackJSON(t *testing.T, ledgerDir string, wantExit int) loopbackVerifyJSON {
	t.Helper()
	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir, "--json")
	if wantExit == 0 {
		if err != nil {
			t.Fatalf("ubx verify --json: %v\noutput: %s", err, out)
		}
	} else {
		requireExitCode(t, err, wantExit, out)
	}
	var payload loopbackVerifyJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, out)
	}
	if len(payload.Acceptance.Checks) != 1 || payload.Acceptance.Checks[0].Method != "loopback" {
		t.Fatalf("want exactly one loopback check, got %+v", payload.Acceptance.Checks)
	}
	return payload
}

func TestVerify_Loopback_Verified(t *testing.T) {
	ledgerDir := t.TempDir()
	id, _ := acceptViaLoopback(t, ledgerDir)

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx verify: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "acceptance: 1 re-derived, 0 convenience-tier (local), 0 inconclusive") {
		t.Fatalf("loopback acceptance not counted as re-derived:\n%s", out)
	}
	if !strings.Contains(out, id+" (loopback): VERIFIED") || !strings.Contains(out, "not identity") {
		t.Fatalf("passing loopback check not printed with what it proves:\n%s", out)
	}
	if strings.Contains(out, "unrecognized") {
		t.Fatalf("loopback still reported as unrecognized:\n%s", out)
	}

	c := verifyLoopbackJSON(t, ledgerDir, 0).Acceptance.Checks[0]
	if !c.OK || c.Outcome != core.LoopbackVerified {
		t.Fatalf("JSON check = %+v, want ok with outcome verified", c)
	}
}

func TestVerify_Loopback_RenderedMissing_Fails(t *testing.T) {
	ledgerDir := t.TempDir()
	_, h := acceptViaLoopback(t, ledgerDir)
	if err := os.Remove(filepath.Join(ledgerDir, "ledger", "rendered", h+".render")); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "(loopback): FAILED") || !strings.Contains(out, "no render under rendered_hash") {
		t.Fatalf("missing render not reported as a failure:\n%s", out)
	}
	c := verifyLoopbackJSON(t, ledgerDir, 1).Acceptance.Checks[0]
	if c.OK || c.Inconclusive || c.Outcome != core.LoopbackRenderedMissing {
		t.Fatalf("JSON check = %+v, want failed with outcome rendered_missing", c)
	}
}

func TestVerify_Loopback_TamperedRender_Fails(t *testing.T) {
	ledgerDir := t.TempDir()
	_, h := acceptViaLoopback(t, ledgerDir)
	path := filepath.Join(ledgerDir, "ledger", "rendered", h+".render")
	if err := os.WriteFile(path, []byte("ubx-render proposal/v1\n\nnothing to see\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := verifyLoopbackJSON(t, ledgerDir, 1).Acceptance.Checks[0]
	if c.OK || c.Inconclusive || c.Outcome != core.LoopbackRenderedHashMismatch {
		t.Fatalf("JSON check = %+v, want failed with outcome rendered_hash_mismatch", c)
	}
}

func TestVerify_Loopback_RerenderDiffers_Fails(t *testing.T) {
	ledgerDir := t.TempDir()
	appendLoopbackRecord(t, ledgerDir, []byte(core.RendererVersion+"\n\nsomething this proposal does not render to\n"))
	c := verifyLoopbackJSON(t, ledgerDir, 1).Acceptance.Checks[0]
	if c.OK || c.Inconclusive || c.Outcome != core.LoopbackRerenderDiffers {
		t.Fatalf("JSON check = %+v, want failed with outcome rerender_differs", c)
	}
}

// A render produced by another renderer version is intact evidence that
// this binary cannot reproduce: inconclusive, never failed, never passed.
func TestVerify_Loopback_RendererChanged_Inconclusive(t *testing.T) {
	ledgerDir := t.TempDir()
	appendLoopbackRecord(t, ledgerDir, []byte("ubx-render proposal/v0\n\nan older layout\n"))

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("an inconclusive check must not fail verify: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(loopback): INCONCLUSIVE") {
		t.Fatalf("renderer change not reported as inconclusive:\n%s", out)
	}
	p := verifyLoopbackJSON(t, ledgerDir, 0)
	c := p.Acceptance.Checks[0]
	if c.OK || !c.Inconclusive || c.Outcome != core.LoopbackRendererChanged {
		t.Fatalf("JSON check = %+v, want inconclusive with outcome renderer_version_changed", c)
	}
	if p.Acceptance.Inconclusive != 1 || p.Acceptance.Derived != 0 {
		t.Fatalf("counts = derived %d, inconclusive %d; want 0 and 1", p.Acceptance.Derived, p.Acceptance.Inconclusive)
	}
}

func TestVerify_Loopback_NoRenderedHash_Fails(t *testing.T) {
	ledgerDir := t.TempDir()
	p := loopbackFixtureProposal()
	id, err := core.Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = id
	p.Status = core.StatusAccepted
	p.Acceptance = &core.Acceptance{Method: core.MethodLoopback, Approvers: []string{core.LoopbackApprover}}
	if err := core.Open(ledgerDir).Append(p); err != nil {
		t.Fatal(err)
	}
	c := verifyLoopbackJSON(t, ledgerDir, 1).Acceptance.Checks[0]
	if c.OK || c.Inconclusive || c.Outcome != core.LoopbackNoRenderedHash {
		t.Fatalf("JSON check = %+v, want failed with outcome no_rendered_hash", c)
	}
}
