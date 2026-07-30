package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// TestWhy_RendersDestroyedResource exercises UBI-30's rendering gap end to
// end, against the real fakeprovider subprocess (not just a hand-built
// proposal): adopt -> resolve a destroy -> accept --confirm-destroys ->
// ship -> ubx why, both the single-proposal-id view and the resource
// chain view, confirming delta.destroys and the destroyed/already_absent
// outcome both actually render -- before this session, neither did.
func TestWhy_RendersDestroyedResource(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	addr := "payments.fake_widget.old-widget"

	adoptOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "old-widget",
		"--lookup", `{"id":"old-widget"}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	if exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v\noutput: %s", err, adoptOut)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	intentPath := filepath.Join(ledgerDir, "destroy-intent.json")
	writeFile(t, intentPath, `{
		"schema_version": 1,
		"kind": "ubx:intent/v1",
		"stack": "payments",
		"intent": {"summary": "decommission the old widget"},
		"resources": [],
		"destroys": ["`+addr+`"]
	}`)
	resolvedPath := filepath.Join(ledgerDir, "destroy-resolved.json")
	if _, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath); err != nil {
		t.Fatalf("ubx resolve: %v", err)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir, "--confirm-destroys")
	if err != nil {
		t.Fatalf("ubx accept --confirm-destroys: %v\noutput: %s", err, acceptOut)
	}
	destroyID := mustExtractID(t, acceptOut)

	shipOut, err := runUbx(t, env, "ship", destroyID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}

	// Single-proposal-id view: delta.destroys itself must render (it
	// never did before this session), and the apply history's own
	// terminal transition must say which outcome closed the chain.
	whyOut, err := runUbx(t, env, "why", destroyID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", destroyID, err, whyOut)
	}
	if !strings.Contains(whyOut, "destroy: "+addr) {
		t.Fatalf("expected a \"destroy: %s\" line, got: %s", addr, whyOut)
	}
	if !strings.Contains(whyOut, "applied at") || !strings.Contains(whyOut, "(destroyed)") {
		t.Fatalf("expected the terminal transition annotated \"(destroyed)\", got: %s", whyOut)
	}

	// Resource-address chain view: same two things, terser rendering.
	chainOut, err := runUbx(t, env, "why", addr, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", addr, err, chainOut)
	}
	if !strings.Contains(chainOut, "destroy: "+addr) {
		t.Fatalf("expected a \"destroy: %s\" line in the chain view, got: %s", addr, chainOut)
	}
	if !strings.Contains(chainOut, "(destroyed)") {
		t.Fatalf("expected \"(destroyed)\" in the chain view's own apply history, got: %s", chainOut)
	}
	if !strings.Contains(chainOut, "2 proposal(s)") {
		t.Fatalf("expected the full biography (adoption + destroy, 2 proposals), got: %s", chainOut)
	}
}

// TestWhy_RendersAlreadyAbsentDestroy confirms the OTHER destroy outcome
// renders distinctly too -- a destroy target already gone before ubx ever
// attempted anything reads "(already_absent)", never conflated with a
// destroy ubx itself actually performed. Hand-built directly via core
// (rather than a real `ubx ship` run): each CLI invocation launches its
// own fresh fakeprovider subprocess, so there is no way to make a REAL
// ship's own precheck observe "already absent" for a resource that
// specific subprocess never itself destroyed first -- this is purely a
// rendering test, and core/executor's own hermetic
// TestShipDestroy_AlreadyAbsent_ResolvesWithoutInFlight (session 3)
// already proves the state machine produces this outcome correctly for
// real.
func TestWhy_RendersAlreadyAbsentDestroy(t *testing.T) {
	ledgerDir := t.TempDir()
	l := core.Open(ledgerDir)
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "ghost-db"}
	state := json.RawMessage(`{"id":"ghost-db"}`)

	adoptNode, _ := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name, "state": json.RawMessage(state),
	})
	hash, err := core.ObservedHash(state)
	if err != nil {
		t.Fatalf("observed hash: %v", err)
	}
	adopt := &core.Proposal{
		SchemaVersion: core.SchemaVersion, Stack: addr.Stack, Kind: core.KindAdoption,
		Intent: core.Intent{Summary: "seed " + addr.String()},
		Delta:  core.Delta{Creates: []json.RawMessage{adoptNode}},
		Resolution: core.Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339), Inputs: []core.ResolutionInput{
			{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"ghost-db"}`)},
		}},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)}, Status: core.StatusDraft,
	}
	if _, err := core.Accept(l, adopt); err != nil {
		t.Fatalf("accept adoption: %v", err)
	}

	head, err := l.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	destroyProp := &core.Proposal{
		SchemaVersion: core.SchemaVersion, Stack: addr.Stack, Parent: head, Kind: core.KindChange,
		Intent: core.Intent{Summary: "decommission the ghost db"},
		Delta:  core.Delta{Destroys: []core.DestroyEntry{{Address: addr, State: state}}},
		Resolution: core.Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339), Inputs: []core.ResolutionInput{
			{Kind: "destroy_target", Resource: addr.String(), ObservedHash: hash, Lookup: json.RawMessage(`{"id":"ghost-db"}`)},
		}},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)}, BlastRadius: core.BlastRadius{Destroys: 1}, Status: core.StatusDraft,
	}
	accepted, err := core.Accept(l, destroyProp)
	if err != nil {
		t.Fatalf("accept destroy: %v", err)
	}

	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ra := &core.ResourceApply{
		Address:        addr,
		Transitions:    []core.Transition{{State: core.ResourcePending, At: now}, {State: core.ResourceApplied, At: now}},
		Reconciliation: []core.ReconciliationAttempt{{At: now, Outcome: "already_absent"}},
	}
	rec.Resources = append(rec.Resources, ra)
	if _, err := l.SealApply(rec, core.ApplySummary{Outcome: "applied", StartedAt: now, FinishedAt: now, ResourcesApplied: 1}); err != nil {
		t.Fatalf("seal apply: %v", err)
	}

	whyOut, err := runUbx(t, nil, "why", accepted.ID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", accepted.ID, err, whyOut)
	}
	if !strings.Contains(whyOut, "(already_absent)") {
		t.Fatalf("expected \"(already_absent)\", got: %s", whyOut)
	}
	if strings.Contains(whyOut, "(destroyed)") {
		t.Fatalf("must not also say \"(destroyed)\" -- these are mutually exclusive outcomes, got: %s", whyOut)
	}
}
