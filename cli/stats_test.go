package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestStats_EmptyLedger(t *testing.T) {
	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "stats", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx stats: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 proposal(s) total") {
		t.Fatalf("unexpected output: %s", out)
	}
	if !strings.Contains(out, "no drift proposals in this window") {
		t.Fatalf("expected the no-drift-data message, got: %s", out)
	}
}

// row: drift proposal open (counted unresolved), plus a general sanity
// check of the by-kind/acceptance-method breakdown, built via the real
// CLI (ubx scan/accept) rather than direct core calls.
func TestStats_DriftOpen_RealCLI(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-stats",
		"--lookup", `{"name":"widget-stats","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-stats",
		"--lookup", `{"name":"widget-stats","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "drift.json"),
		"--no-attribution",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (drift): %v", err)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "drift.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (drift): %v", err)
	}

	out, err := runUbx(t, nil, "stats", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx stats: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "2 proposal(s) total") {
		t.Fatalf("expected 2 total (adoption + drift_adopt), got: %s", out)
	}
	if !strings.Contains(out, "adoption: 1") || !strings.Contains(out, "drift_adopt: 1") {
		t.Fatalf("expected by-kind breakdown, got: %s", out)
	}
	if !strings.Contains(out, "1 still open") {
		t.Fatalf("expected the drift to be counted as still open, got: %s", out)
	}
	if !strings.Contains(out, "local: 2 (100%)") {
		t.Fatalf("expected both proposals accepted locally, got: %s", out)
	}
}

func TestStats_JSON_Shape(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "proposal.json")
	writeFile(t, proposalPath, draftProposalJSON)
	if _, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept: %v", err)
	}

	out, err := runUbx(t, nil, "stats", "--ledger-dir", ledgerDir, "--json")
	if err != nil {
		t.Fatalf("ubx stats --json: %v\noutput: %s", err, out)
	}
	var payload struct {
		Format          int            `json:"format"`
		TotalProposals  int            `json:"total_proposals"`
		ByKind          map[string]int `json:"by_kind"`
		DriftResolution struct {
			SurfacedEvents int      `json:"surfaced_events"`
			ResolutionPct  *float64 `json:"resolution_pct"`
		} `json:"drift_resolution"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, out)
	}
	if payload.Format != 1 || payload.TotalProposals != 1 || payload.ByKind["change"] != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.DriftResolution.SurfacedEvents != 0 || payload.DriftResolution.ResolutionPct != nil {
		t.Fatalf("expected no drift data (a plain change proposal), got: %+v", payload.DriftResolution)
	}
}

func TestStats_SinceUntil_RFC3339(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "proposal.json")
	writeFile(t, proposalPath, draftProposalJSON)
	if _, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept: %v", err)
	}

	out, err := runUbx(t, nil, "stats", "--ledger-dir", ledgerDir, "--since", "2099-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("ubx stats: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 proposal(s) total") {
		t.Fatalf("expected 0 proposals for a future --since window, got: %s", out)
	}

	out2, err := runUbx(t, nil, "stats", "--ledger-dir", ledgerDir, "--since", "2000-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("ubx stats: %v\noutput: %s", err, out2)
	}
	if !strings.Contains(out2, "1 proposal(s) total") {
		t.Fatalf("expected 1 proposal for a past --since window, got: %s", out2)
	}
}

func TestStats_InvalidSince_ExitsTwo(t *testing.T) {
	out, err := runUbx(t, nil, "stats", "--ledger-dir", t.TempDir(), "--since", "not-a-timestamp")
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "RFC3339") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStats_AttributionCoverage_RealCLI(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptPath := filepath.Join(t.TempDir(), "adopt.json")
	writeFile(t, adoptPath, `{
		"schema_version": 1, "stack": "conformance", "parent": "", "kind": "adoption",
		"intent": {"summary": "adopt"},
		"delta": {"creates": [{"stack": "conformance", "type": "aws_s3_bucket", "name": "ubx-states", "state": {"tags.env": "prod"}}]},
		"resolution": {"resolved_at": "2026-07-10T21:00:00Z", "inputs": [
			{"kind": "live_state", "resource": "conformance.aws_s3_bucket.ubx-states", "observed_hash": "sha256:abc"}
		]},
		"cost_delta": {"monthly_usd": 0}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)
	acceptOut, err := runUbx(t, nil, "accept", adoptPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (adopt): %v\noutput: %s", err, acceptOut)
	}
	adoptID := mustExtractID(t, acceptOut)

	driftPath := filepath.Join(t.TempDir(), "drift.json")
	writeFile(t, driftPath, strings.Replace(handWrittenDriftProposal, `"parent": "",`, `"parent": "`+adoptID+`",`, 1))
	if _, err := runUbx(t, nil, "accept", driftPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (drift): %v", err)
	}

	out, err := runUbx(t, nil, "stats", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx stats: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Attribution coverage: 100% (1 of 1 drift_adopt proposal(s) carry a real actor)") {
		t.Fatalf("expected 100%% attribution coverage, got: %s", out)
	}
}
