package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestShip_ModifyRefusedAsStale_LedgerNeverLies is UBI-89 P1's own
// end-to-end regression test, against a real fakeprovider subprocess
// (never hand-seeded): the founder's own exact live repro shape --
// `ubx plan` drafts a modify correctly, `ubx ship` refuses it as stale
// (reality moved between plan and ship -- here, deterministically forced
// via FAKEPROVIDER_EXTRA_TAG injecting an extra tag ONLY at ship time,
// modeling "something genuinely differs on the live read"), yet the
// proposal's own acceptance still durably lands in the ledger (`ubx ship`
// accepts an inline plan BEFORE the freshness check that guards actual
// execution, core/executor's per-resource loop -- by design, not a bug in
// itself: see this session's own STATE.md diagnosis). Before this
// session's fix, `core.Ledger.FoldState` folded the modify's own `after`
// into "current truth" regardless of whether it ever actually shipped --
// the literal mechanism behind the founder's own live finding (ledger
// claims 259200s retention, real AWS still 86400s). This test proves the
// full real CLI pipeline (plan -> ship -> why/status) now tells the
// truth: the ship attempt fails, and every reader of ledger truth still
// correctly reports the PRE-modify value, never the attempted one.
func TestShip_ModifyRefusedAsStale_LedgerNeverLies(t *testing.T) {
	ledgerDir := t.TempDir()

	seedPath := filepath.Join(ledgerDir, "seed.json")
	writeIntentFile(t, seedPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "playground",
		"intent":         map[string]interface{}{"summary": "seed one widget, UBI-89 staleness regression"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "ci-notifications", "op": "create", "config": map[string]interface{}{"name": "ci-notifications"}},
		},
	})
	// FAKEPROVIDER_EXTRA_TAG is deliberately unset for every step up to and
	// including the seed ship and the modify's own `ubx plan` -- only the
	// FINAL ship (below) gets it, modeling "reality moved between plan and
	// ship," not "the fixture is misconfigured throughout."
	seedEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}
	resolvedPath := filepath.Join(ledgerDir, "seed-resolved.json")
	if _, err := runUbx(t, seedEnv, "resolve", seedPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath); err != nil {
		t.Fatalf("seed resolve: %v", err)
	}
	acceptOut, err := runUbx(t, seedEnv, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("seed accept: %v", err)
	}
	seedID := mustExtractID(t, acceptOut)
	if _, err := runUbx(t, seedEnv, "ship", seedID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes"); err != nil {
		t.Fatalf("seed ship: %v", err)
	}

	modifyPath := filepath.Join(ledgerDir, "modify.json")
	writeIntentFile(t, modifyPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "playground",
		"intent":         map[string]interface{}{"summary": "rename ci-notifications"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "ci-notifications", "op": "modify", "config": map[string]interface{}{"name": "ci-notifications-v2"}},
		},
	})
	planOut, err := runUbx(t, seedEnv, "plan", modifyPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan (modify): %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	// Reality moves between plan and ship: the live read at ship time
	// returns something the plan-time snapshot never saw.
	shipEnv := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=drift=injected-between-plan-and-ship"}
	shipOut, shipErr := runUbx(t, shipEnv, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes")
	if shipErr == nil {
		t.Fatalf("expected the ship attempt to fail (stale observation), got success: %s", shipOut)
	}
	if !strings.Contains(shipOut, "failed") {
		t.Fatalf("expected the ship output to report a failure, got: %s", shipOut)
	}

	// The ticket's own exact repro: the acceptance stuck despite the
	// failed apply -- "accepted ... via local plan" already printed above
	// the failure summary, and a re-run of `ubx ship <same hash>` would
	// now find nothing in the plan store (it was consumed on accept) --
	// confirmed indirectly below via `ubx why`, which only ever shows an
	// ACCEPTED proposal's own content.
	whyOut, err := runUbx(t, nil, "why", "playground.fake_widget.ci-notifications", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "2 proposal(s)") {
		t.Fatalf("expected the modify's own acceptance to still be recorded (2 proposals: create + the failed-to-ship modify), got: %s", whyOut)
	}

	// The decisive assertion: ledger truth (core.Ledger.FoldState, what
	// `ubx status --drift`/a future `ubx plan`/`resolve` against this
	// stack would all read) must still show the resource's ORIGINAL name
	// -- the failed modify's own attempted rename never actually reached
	// the real (fake) resource, so it must never be reported as if it
	// had. Observed indirectly through a SECOND `ubx plan` targeting the
	// same attribute: core/resolver's own OpModify diff computes its
	// "before" value from FoldState directly, so its own before/after
	// line reveals exactly what the ledger currently believes.
	secondModifyPath := filepath.Join(ledgerDir, "modify2.json")
	writeIntentFile(t, secondModifyPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "playground",
		"intent":         map[string]interface{}{"summary": "rename ci-notifications again"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "ci-notifications", "op": "modify", "config": map[string]interface{}{"name": "ci-notifications-v3"}},
		},
	})
	secondPlanOut, err := runUbx(t, seedEnv, "plan", secondModifyPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan (second modify): %v\noutput: %s", err, secondPlanOut)
	}
	if !strings.Contains(secondPlanOut, `name: "ci-notifications" -> "ci-notifications-v3"`) {
		t.Fatalf("ledger truth lied about the failed modify's own effect: expected the diff to start from the ORIGINAL name \"ci-notifications\" (the first modify never actually shipped), got:\n%s", secondPlanOut)
	}
	if strings.Contains(secondPlanOut, "ci-notifications-v2") {
		t.Fatalf("ledger truth still shows the never-shipped modify's own attempted value \"ci-notifications-v2\" -- the exact UBI-89 bug, got:\n%s", secondPlanOut)
	}
}
