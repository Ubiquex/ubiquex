package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// promoteFakeWidgetDraft is a fakeIntentAdapter draft producing one
// fake_widget create -- fakeprovider's own known-good schema (plan_test.go's
// own convention), reused here so promote's own re-derivation exercises a
// real resolver.Resolve/fakeprovider round trip, not a hand-rolled shortcut.
const promoteFakeWidgetDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "widget, modeled on staging"},
  "resources": [
    {"type": "fake_widget", "name": "widget1", "op": "create", "config": "{\"name\":\"widget1\",\"tags\":{\"env\":\"prod\"}}"}
  ],
  "destroys": []
}`

// appendAcceptedProposal directly appends an already-"accepted" proposal to
// ledgerDir's own ledger -- the same shortcut cli/status_test.go's own
// TestStatus_Drift_MissingLookup_Unreadable_ExitTwo uses to build a ledger
// fixture without running the full accept ceremony, since promote's own
// adversarial tests care about intent.sources shape, not how a proposal
// came to be accepted.
func appendAcceptedProposal(t *testing.T, ledgerDir, id, stack string, sources []core.IntentSource) *core.Proposal {
	t.Helper()
	ledger := core.Open(ledgerDir)
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		ID:            id,
		Stack:         stack,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "test fixture", Sources: sources},
		Acceptance:    &core.Acceptance{Method: "local", Approvers: []string{"test"}, AcceptedAt: "2026-07-30T00:00:00Z"},
		Status:        core.StatusAccepted,
	}
	if err := ledger.Append(p); err != nil {
		t.Fatalf("append fixture proposal: %v", err)
	}
	return p
}

func TestPromote_HappyPath_ReResolvesAgainstTarget(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	docPath := filepath.Join(sourceDir, "payments.md")
	writeFile(t, docPath, "# Payments\n\nA widget, like staging.")

	sourceID := strings.Repeat("a", 64)
	appendAcceptedProposal(t, sourceDir, sourceID, "payments", []core.IntentSource{
		{Kind: "document", Ref: docPath, ContentHash: "sha256:deadbeef"},
		{Kind: "intent_provider", Ref: "fake:fake-model-v1", ContentHash: "sha256:beadfeed"},
	})

	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: promoteFakeWidgetDraft})

	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "promote", sourceID,
		"--ledger-dir", sourceDir, "--to", targetDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx promote: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "promoted "+displayHash(sourceID, false)+" -> payments") {
		t.Errorf("expected a promoted-header line, got:\n%s", out)
	}
	if !strings.Contains(out, "delta: +1 create(s)") {
		t.Errorf("expected the target's own fresh resolve delta, got:\n%s", out)
	}

	hash := mustExtractPlanHash(t, out)
	planFile := filepath.Join(targetDir, ".ubx", "plans", hash+".json")
	if _, err := os.Stat(planFile); err != nil {
		t.Fatalf("expected a saved plan file at %s: %v", planFile, err)
	}

	np, err := readPlanFile(targetDir, hash)
	if err != nil {
		t.Fatalf("read saved plan: %v", err)
	}
	if !hasPromotionSourceRef(np, sourceID) {
		t.Fatalf("expected a promotion source referencing %s, got sources: %+v", sourceID, np.Intent.Sources)
	}
	var promo *core.IntentSource
	for i := range np.Intent.Sources {
		if np.Intent.Sources[i].Kind == "promotion" {
			promo = &np.Intent.Sources[i]
		}
	}
	if promo == nil || promo.Base != sourceDir {
		t.Errorf("promotion source's base = %+v, want %q (the source's own --ledger-dir, git-local has no other stack-base concept)", promo, sourceDir)
	}
	// The fresh re-resolution's own document/intent_provider sources must
	// also be present -- promotion is additive, never a replacement.
	var hasDoc, hasProvider bool
	for _, s := range np.Intent.Sources {
		hasDoc = hasDoc || s.Kind == "document"
		hasProvider = hasProvider || s.Kind == "intent_provider"
	}
	if !hasDoc || !hasProvider {
		t.Errorf("expected fresh document/intent_provider sources from re-resolution, got: %+v", np.Intent.Sources)
	}
}

func TestPromote_SourceProposalNotFound(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	_, err := runUbx(t, nil, "promote", strings.Repeat("f", 64), "--ledger-dir", sourceDir, "--to", targetDir)
	if err == nil {
		t.Fatal("expected an error for a nonexistent source proposal id")
	}
	if !strings.Contains(err.Error(), "proposal not found") {
		t.Errorf("error = %v, want it to name \"proposal not found\"", err)
	}
}

func TestPromote_SourceIsUnacceptedPlanDraft_Refused(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	intentPath := filepath.Join(sourceDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})

	planOut, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", sourceDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, planOut)

	_, err = runUbx(t, nil, "promote", hash, "--ledger-dir", sourceDir, "--to", targetDir)
	if err == nil {
		t.Fatal("expected promote to refuse an unaccepted plan-store draft")
	}
	if !strings.Contains(err.Error(), "unaccepted \"ubx plan\" draft") {
		t.Errorf("error = %v, want it to name the unaccepted-draft reason", err)
	}
}

func TestPromote_NoReResolvableSource_Refused(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	id := strings.Repeat("b", 64)
	appendAcceptedProposal(t, sourceDir, id, "payments", nil)

	_, err := runUbx(t, nil, "promote", id, "--ledger-dir", sourceDir, "--to", targetDir)
	if err == nil {
		t.Fatal("expected promote to refuse a proposal with no document/dialogue source")
	}
	if !strings.Contains(err.Error(), "no re-resolvable authoring source") {
		t.Errorf("error = %v, want it to name the missing-source reason", err)
	}
}

func TestPromote_DialogueSource_RefusedByName(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	id := strings.Repeat("c", 64)
	appendAcceptedProposal(t, sourceDir, id, "payments", []core.IntentSource{
		{Kind: "dialogue", Ref: "dialogues/deadbeef.dlg.json", ContentHash: "sha256:deadbeef"},
	})

	_, err := runUbx(t, nil, "promote", id, "--ledger-dir", sourceDir, "--to", targetDir)
	if err == nil {
		t.Fatal("expected promote to refuse a dialogue-sourced proposal")
	}
	if !strings.Contains(err.Error(), "ubx chat") || !strings.Contains(err.Error(), "SOURCE ledger directory") {
		t.Errorf("error = %v, want it to name the dialogue-ref-portability reason", err)
	}
}

func TestPromote_SDKAuthoredSource_RefusedByName(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()

	id := strings.Repeat("d", 64)
	appendAcceptedProposal(t, sourceDir, id, "payments", []core.IntentSource{
		{Kind: "document", Ref: "main.ts", ContentHash: "sha256:deadbeef"},
	})

	_, err := runUbx(t, nil, "promote", id, "--ledger-dir", sourceDir, "--to", targetDir)
	if err == nil {
		t.Fatal("expected promote to refuse an SDK-authored (basename-only ref) document source")
	}
	if !strings.Contains(err.Error(), "SDK program") || !strings.Contains(err.Error(), "basename") {
		t.Errorf("error = %v, want it to name the basename-only-ref reason", err)
	}
}

func TestPromote_TargetDirWithNoConfig_Allowed(t *testing.T) {
	// "target dir has no config" is deliberately NOT an error -- every
	// other command already treats a missing .ubx/config as "defaults
	// apply" (LoadConfig's own documented behavior), and promote follows
	// the identical convention for --to. This is exercised implicitly by
	// every other test here (none of them create a .ubx/config in
	// targetDir), asserted explicitly here as its own adversarial case.
	sourceDir := t.TempDir()
	targetDir := t.TempDir() // no .ubx/config ever written here

	docPath := filepath.Join(sourceDir, "payments.md")
	writeFile(t, docPath, "# Payments\n\nA widget.")
	sourceID := strings.Repeat("e", 64)
	appendAcceptedProposal(t, sourceDir, sourceID, "payments", []core.IntentSource{
		{Kind: "document", Ref: docPath, ContentHash: "sha256:deadbeef"},
	})
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: promoteFakeWidgetDraft})

	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "promote", sourceID,
		"--ledger-dir", sourceDir, "--to", targetDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx promote against a target dir with no .ubx/config: %v\noutput: %s", err, out)
	}
}

func TestPromote_ChainOfPromotionEvidence_WalksOneHopAtATime(t *testing.T) {
	stagingDir := t.TempDir()
	qaDir := t.TempDir()
	prodDir := t.TempDir()

	docPath := filepath.Join(stagingDir, "payments.md")
	writeFile(t, docPath, "# Payments\n\nA widget.")
	stagingID := strings.Repeat("1", 64)
	appendAcceptedProposal(t, stagingDir, stagingID, "payments", []core.IntentSource{
		{Kind: "document", Ref: docPath, ContentHash: "sha256:deadbeef"},
	})

	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: promoteFakeWidgetDraft})
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// staging -> qa, then accept it for real in qa so it can itself be promoted.
	qaPromoteOut, err := runUbx(t, env, "promote", stagingID, "--ledger-dir", stagingDir, "--to", qaDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("promote staging->qa: %v\noutput: %s", err, qaPromoteOut)
	}
	qaHash := mustExtractPlanHash(t, qaPromoteOut)
	shipOut, err := runUbx(t, env, "ship", qaHash, "--ledger-dir", qaDir, "--provider", fakeProviderBinary, "--yes")
	if err != nil {
		t.Fatalf("ubx ship (accept the qa promotion): %v\noutput: %s", err, shipOut)
	}

	// qa's own accepted proposal must itself carry a promotion source
	// pointing back at staging.
	qaLedger := core.Open(qaDir)
	qaProposal, err := qaLedger.Read(qaHash)
	if err != nil {
		t.Fatalf("read qa's own accepted proposal: %v", err)
	}
	if !hasPromotionSourceRef(qaProposal, stagingID) {
		t.Fatalf("expected qa's own proposal to carry a promotion source referencing staging id %s, got sources: %+v", stagingID, qaProposal.Intent.Sources)
	}

	// qa -> prod: promotes qa's own now-accepted id.
	prodPromoteOut, err := runUbx(t, env, "promote", qaHash, "--ledger-dir", qaDir, "--to", prodDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("promote qa->prod: %v\noutput: %s", err, prodPromoteOut)
	}
	prodHash := mustExtractPlanHash(t, prodPromoteOut)
	prodPlan, err := readPlanFile(prodDir, prodHash)
	if err != nil {
		t.Fatalf("read prod's own saved plan: %v", err)
	}

	// prod's own new proposal names qa (its immediate source), NOT
	// staging directly -- the chain is walked one hop at a time, not
	// flattened. qa's own ledger entry (still readable, asserted above)
	// is what recovers the earlier staging<-qa hop.
	if !hasPromotionSourceRef(prodPlan, qaHash) {
		t.Fatalf("expected prod's own proposal to carry a promotion source referencing qa's own id %s (its immediate source), got sources: %+v", qaHash, prodPlan.Intent.Sources)
	}
	if hasPromotionSourceRef(prodPlan, stagingID) {
		t.Fatalf("prod's own proposal should NOT directly reference staging (%s) -- the chain is per-hop, reconstructed by following qa's own promotion source, not flattened", stagingID)
	}
}

// hasPromotionSourceRef reports whether p has a "promotion"-kind
// intent.sources entry whose Ref matches want.
func hasPromotionSourceRef(p *core.Proposal, want string) bool {
	for _, s := range p.Intent.Sources {
		if s.Kind == "promotion" && s.Ref == want {
			return true
		}
	}
	return false
}

func TestWhy_RendersPromotionSource(t *testing.T) {
	ledgerDir := t.TempDir()
	id := strings.Repeat("9", 64)
	appendAcceptedProposal(t, ledgerDir, id, "payments", []core.IntentSource{
		{Kind: "promotion", Ref: strings.Repeat("8", 64), Base: "staging"},
	})

	out, err := runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, out)
	}
	want := "source: promoted from staging/" + displayHash(strings.Repeat("8", 64), false)
	if !strings.Contains(out, want) {
		t.Errorf("expected why to render %q, got:\n%s", want, out)
	}
}
