package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestScan_JSON_New: --json's stable payload for a never-seen resource --
// the whole point of --json is that it's the ONLY thing on stdout, so
// unmarshaling the raw, untrimmed output must succeed (any stray human-text
// line before or after the JSON document would break this).
func TestScan_JSON_New(t *testing.T) {
	ledgerDir := t.TempDir()
	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-json-new",
		"--lookup", `{"name":"widget-json-new"}`,
		"--ledger-dir", ledgerDir,
		"--json",
	)
	requireExitCode(t, err, 1, out)

	var payload scanJSON
	if jsonErr := json.Unmarshal([]byte(out), &payload); jsonErr != nil {
		t.Fatalf("stdout must be exactly one JSON document, got parse error %v for: %s", jsonErr, out)
	}
	if payload.Format != jsonFormatVersion {
		t.Errorf("format = %d, want %d", payload.Format, jsonFormatVersion)
	}
	if payload.Outcome != "new" {
		t.Errorf("outcome = %q, want \"new\"", payload.Outcome)
	}
	if payload.Address != (addressJSON{Stack: "payments", Type: "fake_widget", Name: "widget-json-new"}) {
		t.Errorf("address = %+v, want the scanned address", payload.Address)
	}
	if len(payload.Proposals) != 1 || payload.Proposals[0].Kind != "adoption" {
		t.Errorf("expected exactly one adoption proposal, got: %+v", payload.Proposals)
	}
}

// TestScan_JSON_Unchanged: the "no drift" outcome, exit 0, empty proposals.
func TestScan_JSON_Unchanged(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptPath := filepath.Join(t.TempDir(), "adopt.json")
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-json-clean",
		"--lookup", `{"name":"widget-json-clean"}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept: %v", err)
	}

	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-json-clean",
		"--lookup", `{"name":"widget-json-clean"}`,
		"--ledger-dir", ledgerDir,
		"--json",
	)
	requireExitCode(t, err, 0, out)

	var payload scanJSON
	if jsonErr := json.Unmarshal([]byte(out), &payload); jsonErr != nil {
		t.Fatalf("stdout must be exactly one JSON document, got: %v for: %s", jsonErr, out)
	}
	if payload.Outcome != "unchanged" {
		t.Errorf("outcome = %q, want \"unchanged\"", payload.Outcome)
	}
	if len(payload.Proposals) != 0 {
		t.Errorf("expected no proposals for an unchanged scan, got: %+v", payload.Proposals)
	}
}

// TestScan_JSON_RejectsWithAllAndSurfaceAs: --json is deliberately scoped
// to single-resource scan without --surface-as (UBI-20 workstream 2's own
// documented limit, not an oversight) -- both combinations must fail
// loudly, not silently ignore --json.
func TestScan_JSON_RejectsWithAllAndSurfaceAs(t *testing.T) {
	statePath := writeState(t, `{"mode": "managed", "type": "fake_widget", "name": "api", "instances": [{"attributes": {"id": "api"}}]}`)
	_, err := runUbx(t, nil, "scan", "--all", "--tfstate", statePath, "--json", "--ledger-dir", t.TempDir())
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "--json") {
		t.Fatalf("expected a clear --json+--all error, got: %v", err)
	}

	_, err = runUbx(t, nil, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "widget-json-surface",
		"--lookup", `{"name":"widget-json-surface"}`,
		"--ledger-dir", t.TempDir(),
		"--json", "--surface-as", "issue", "--github-repo", "acme/infra",
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "--json") {
		t.Fatalf("expected a clear --json+--surface-as error, got: %v", err)
	}
}

// TestStatus_JSON_LedgerOnly: drift_checked must be false, and no
// resource carries a status field, when --drift wasn't given -- a
// consumer must not be able to mistake "we didn't check" for "0 drifted."
func TestStatus_JSON_LedgerOnly(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-json-status-1", `{"name":"widget-json-status-1"}`, env)
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-json-status-2", `{"name":"widget-json-status-2"}`, env)

	out, err := runUbx(t, nil, "status", "--ledger-dir", ledgerDir, "--json")
	requireExitCode(t, err, 0, out)

	var payload statusJSON
	if jsonErr := json.Unmarshal([]byte(out), &payload); jsonErr != nil {
		t.Fatalf("stdout must be exactly one JSON document, got: %v for: %s", jsonErr, out)
	}
	if payload.DriftChecked {
		t.Error("drift_checked = true, want false (no --drift given)")
	}
	if payload.Summary.Total != 2 {
		t.Errorf("summary.total = %d, want 2", payload.Summary.Total)
	}
	for _, r := range payload.Resources {
		if r.Status != "" {
			t.Errorf("resource %+v has a status set despite ledger-only mode", r)
		}
	}
}

// TestStatus_JSON_Drift: --drift --json populates per-resource status and
// a summary that matches the human view's own counts, and exits 1 when
// something drifted.
func TestStatus_JSON_Drift(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-json-drift", `{"name":"widget-json-drift","tags":{"env":"prod"}}`, env)

	// Drift it out from under the ledger via the fake provider's extra-tag
	// knob (same technique as TestAccept_ReverifyBlocksStaleAcceptance).
	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=mutated=yes"},
		"status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary, "--json")
	requireExitCode(t, err, 1, out)

	var payload statusJSON
	if jsonErr := json.Unmarshal([]byte(out), &payload); jsonErr != nil {
		t.Fatalf("stdout must be exactly one JSON document, got: %v for: %s", jsonErr, out)
	}
	if !payload.DriftChecked {
		t.Error("drift_checked = false, want true (--drift given)")
	}
	if payload.Summary.Drifted != 1 {
		t.Errorf("summary.drifted = %d, want 1", payload.Summary.Drifted)
	}
	if len(payload.Resources) != 1 || payload.Resources[0].Status != "drifted" {
		t.Fatalf("expected exactly one drifted resource, got: %+v", payload.Resources)
	}
}

// TestWhy_JSON_SingleProposal: the single-id form wraps the exact
// core.Proposal in a versioned envelope.
func TestWhy_JSON_SingleProposal(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "proposal.json")
	writeFile(t, proposalPath, draftProposalJSON)
	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v", err)
	}
	id := mustExtractID(t, acceptOut)

	out, err := runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir, "--json")
	requireExitCode(t, err, 0, out)

	var payload whyJSON
	if jsonErr := json.Unmarshal([]byte(out), &payload); jsonErr != nil {
		t.Fatalf("stdout must be exactly one JSON document, got: %v for: %s", jsonErr, out)
	}
	if payload.Proposal == nil || payload.Proposal.ID != id {
		t.Fatalf("expected proposal.id = %s, got: %+v", id, payload.Proposal)
	}
	if payload.Chain != nil {
		t.Errorf("expected no chain field for the single-id form, got: %+v", payload.Chain)
	}
}

// TestWhy_JSON_Chain: the resource-address form emits an array, newest
// first, matching the human view's own ordering.
func TestWhy_JSON_Chain(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	addr := "payments.fake_widget.widget-json-chain"

	adoptPath := filepath.Join(t.TempDir(), "adopt.json")
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary, "--stack", "payments", "--type", "fake_widget",
		"--name", "widget-json-chain", "--lookup", `{"name":"widget-json-chain","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir, "--out", adoptPath,
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	acceptOut, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}
	adoptID := mustExtractID(t, acceptOut)

	driftPath := filepath.Join(t.TempDir(), "drift.json")
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary, "--stack", "payments", "--type", "fake_widget",
		"--name", "widget-json-chain", "--lookup", `{"name":"widget-json-chain","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir, "--out", driftPath, "--no-attribution",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (drift): %v", err)
	}
	acceptOut2, err := runUbx(t, env, "accept", driftPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (drift): %v", err)
	}
	driftID := mustExtractID(t, acceptOut2)

	out, err := runUbx(t, env, "why", addr, "--ledger-dir", ledgerDir, "--json")
	requireExitCode(t, err, 0, out)

	var payload whyJSON
	if jsonErr := json.Unmarshal([]byte(out), &payload); jsonErr != nil {
		t.Fatalf("stdout must be exactly one JSON document, got: %v for: %s", jsonErr, out)
	}
	if payload.Proposal != nil {
		t.Errorf("expected no proposal field for the chain form, got: %+v", payload.Proposal)
	}
	if len(payload.Chain) != 2 {
		t.Fatalf("expected a 2-entry chain, got: %+v", payload.Chain)
	}
	if payload.Chain[0].ID != driftID || payload.Chain[1].ID != adoptID {
		t.Fatalf("expected newest-first ordering [drift, adopt], got [%s, %s]", payload.Chain[0].ID, payload.Chain[1].ID)
	}
}

// TestWhy_JSON_VerifyAcceptance_Mismatch: --json --verify-acceptance
// embeds the same reviewer-mismatch finding the human view reports as
// MISMATCH, and the command still exits 1 for it in JSON mode.
func TestWhy_JSON_VerifyAcceptance_Mismatch(t *testing.T) {
	ledgerDir, id, repoDir := acceptFromMergeForVerifyTest(t, []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "APPROVED", "submitted_at": "2026-07-11T10:00:00Z"},
	})

	apiBaseNow := fakeGitHubServer(t, "irrelevant -- verify never re-reads the PR body", []map[string]any{
		{"user": map[string]any{"login": "roozbeh"}, "state": "CHANGES_REQUESTED", "submitted_at": "2026-07-11T11:00:00Z"},
	})
	t.Setenv("UBX_GITHUB_API_BASE_URL", apiBaseNow)

	out, err := runUbx(t, nil, "why", id,
		"--ledger-dir", ledgerDir, "--verify-acceptance",
		"--repo-dir", repoDir, "--github-repo", "acme/infra", "--json",
	)
	requireExitCode(t, err, 1, out)

	var payload whyJSON
	if jsonErr := json.Unmarshal([]byte(out), &payload); jsonErr != nil {
		t.Fatalf("stdout must be exactly one JSON document, got: %v for: %s", jsonErr, out)
	}
	va := payload.VerifyAcceptance
	if va == nil || !va.Applicable {
		t.Fatalf("expected an applicable verify_acceptance result, got: %+v", va)
	}
	if !va.GitOK {
		t.Errorf("git_ok = false, want true (only reviewers changed)")
	}
	if va.ReviewersMatch {
		t.Error("reviewers_match = true, want false (a MISMATCH)")
	}
	if len(va.CurrentApprovers) != 0 {
		t.Errorf("expected zero current approvers (the approval was withdrawn), got: %v", va.CurrentApprovers)
	}
	if len(va.RecordedApprovers) != 1 || va.RecordedApprovers[0] != "roozbeh" {
		t.Errorf("expected recorded approvers = [roozbeh], got: %v", va.RecordedApprovers)
	}
}
