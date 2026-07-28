package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// addCommitToExistingRepo adds a second real commit to an already-existing
// git repo dir (built by gitRepoWithProposal), writing content to path,
// and returns the new commit's own SHA.
func addCommitToExistingRepo(t *testing.T, repoDir, path, content string) string {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repoDir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	full := filepath.Join(repoDir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", path)
	run("commit", "-q", "-m", "a second, different change")

	out, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

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

func TestVerify_PrMerge_RepoDirAndGithubRepoGiven_Derived(t *testing.T) {
	ledgerDir, id, repoDir := acceptFromMergeForVerifyTest(t, []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
	})
	_ = id

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir, "--repo-dir", repoDir, "--github-repo", "acme/infra")
	if err != nil {
		t.Fatalf("ubx verify: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "1 re-derived") {
		t.Fatalf("expected 1 re-derived acceptance, got: %s", out)
	}
	if !strings.Contains(out, "0 inconclusive") {
		t.Fatalf("expected 0 inconclusive, got: %s", out)
	}
}

func TestVerify_PrMerge_NoRepoDir_Inconclusive(t *testing.T) {
	ledgerDir, _, _ := acceptFromMergeForVerifyTest(t, []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
	})

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx verify: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 re-derived") || !strings.Contains(out, "1 inconclusive") {
		t.Fatalf("expected 0 re-derived, 1 inconclusive (no --repo-dir given), got: %s", out)
	}
}

func TestVerify_PrMerge_RepoDirOnly_ReviewerCheckInconclusive(t *testing.T) {
	ledgerDir, _, repoDir := acceptFromMergeForVerifyTest(t, []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
	})

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir, "--repo-dir", repoDir)
	if err != nil {
		t.Fatalf("ubx verify: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 re-derived") || !strings.Contains(out, "1 inconclusive") {
		t.Fatalf("expected git-only check to count as inconclusive (no --github-repo), got: %s", out)
	}
}

// row: forged acceptance (hash mismatch) -- the ledger's own acceptance
// record points its merge_sha at a real commit whose own proposal file
// content doesn't match what was actually accepted (simulating a tampered
// acceptance record, not a rewritten git history -- git commits are
// content-addressed and immutable, so "the file at that exact commit now
// hashes differently" can only mean the RECORD points somewhere it
// shouldn't).
func TestVerify_PrMerge_ForgedAcceptance_ExitsOne(t *testing.T) {
	ledgerDir, id, repoDir := acceptFromMergeForVerifyTest(t, []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
	})

	// A second, real commit in the same repo with different proposal
	// content -- a real, existing commit, just not the one that was
	// actually accepted.
	forgedSHA := addCommitToExistingRepo(t, repoDir, "proposals/pending.json", `{
		"schema_version": 1, "stack": "payments", "parent": "", "kind": "change",
		"intent": {"summary": "a completely different change"}, "delta": {},
		"resolution": {"resolved_at": "2026-07-11T00:00:00Z"},
		"cost_delta": {}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)

	propPath := filepath.Join(ledgerDir, "ledger", "proposals", id+".prop.json")
	raw, err := os.ReadFile(propPath)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk map[string]interface{}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	onDisk["acceptance"].(map[string]interface{})["merge_sha"] = forgedSHA
	forged, err := json.MarshalIndent(onDisk, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(propPath, forged, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir, "--repo-dir", repoDir, "--github-repo", "acme/infra")
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "FAILED") {
		t.Fatalf("expected a FAILED acceptance line, got: %s", out)
	}
}
