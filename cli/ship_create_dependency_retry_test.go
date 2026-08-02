package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShip_CreateFailsNotFoundReferencingShippedDependency_RealFakeProvider
// proves UBI-92's own create-side eventual-consistency retry
// (retryCreateOnDependencyNotVisible, core/executor/ship.go) end to end,
// through the real tfplugin wire protocol (provider/internal/fakeprovider's
// new "fail-create-not-found" mode), not just core/executor's own
// in-process fakeApplier (core/executor/create_dependency_retry_test.go
// already covers that). "role" ships first; "attach" $ref-references
// role's own name, so resolve gives it a real depends_on edge on role --
// then attach's own create is scripted to fail with a NoSuchEntity-shaped
// diagnostic 3 times before succeeding, the exact live shape the founder
// hit (playground-14: AttachRolePolicy -> 404 NoSuchEntity immediately
// after that same role shipped successfully in the same batch).
func TestShip_CreateFailsNotFoundReferencingShippedDependency_RealFakeProvider(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "playground",
		"intent":         map[string]interface{}{"summary": "UBI-92: role + attach create chain"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "role",
				"op":   "create",
				"config": map[string]interface{}{
					"name": "ci-runner-v14",
				},
			},
			{
				"type": "fake_widget",
				"name": "attach",
				"op":   "create",
				"config": map[string]interface{}{
					// A distinct "name" of its own (fakeprovider's own
					// FAKEPROVIDER_FAIL_CREATE_TARGET keys off "name" --
					// see decodeWidgetState's own doc comment on why "id"
					// can't disambiguate, every fake_widget instance
					// computes the identical literal "computed-id"); the
					// $ref lives in "tags" instead, still giving attach a
					// real depends_on edge onto role (core/resolver/refs.go
					// scans $ref anywhere in config, not just top-level
					// attributes).
					"name": "attach-ci-runner-v14",
					"tags": map[string]interface{}{
						"role": map[string]interface{}{
							"$ref": map[string]interface{}{"to": "playground.fake_widget.role.name"},
						},
					},
				},
			},
		},
	})

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath)
	if err != nil {
		t.Fatalf("ubx resolve: %v\noutput: %s", err, resolveOut)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	// attach's own create fails not-found-shaped 3 times, then succeeds --
	// role must already have shipped for attach's own retry to be entered
	// at all (hasShippedDependency), which shipChange's own topo order
	// guarantees within this single `ubx ship` invocation.
	shipEnv := []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"FAKEPROVIDER_APPLY_MODE=fail-create-not-found",
		"FAKEPROVIDER_FAIL_CREATE_TARGET=attach-ci-runner-v14",
		"FAKEPROVIDER_FAIL_CREATE_COUNT=3",
	}
	shipOut, err := runUbx(t, shipEnv, "ship", changeID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped (the retry should have absorbed the simulated eventual-consistency lag), got: %s", shipOut)
	}

	whyOut, err := runUbx(t, env, "why", changeID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", changeID, err, whyOut)
	}
	if strings.Count(whyOut, "reconcile: inconclusive") < 3 {
		t.Fatalf("expected at least 3 inconclusive reconcile attempts logged (one per simulated not-found failure), got: %s", whyOut)
	}
	if !strings.Contains(whyOut, "reconcile: applied") || !strings.Contains(whyOut, "retry succeeded") {
		t.Fatalf("expected a final 'reconcile: applied ... retry succeeded' entry, got: %s", whyOut)
	}
}

// TestShip_CreateFailsNotFoundReferencingShippedDependency_BudgetExhausted_RealFakeProvider
// proves the OTHER half of UBI-92's own narrow scoping through the real
// wire protocol: a same-batch dependency that never becomes visible within
// the retry budget must still fail terminally, not hang or silently
// succeed. Since UBI-93, this no longer grinds out the full ~64s budget:
// noProgressBailoutThreshold (core/executor/ship.go) gives up early, after
// ~19s, once elapsed retry time clearly exceeds realistic propagation lag
// with zero change in outcome -- a live repro (playground-15) found the
// full budget grinding to completion against a dependency that had
// genuinely never been created under the identity referenced, no amount
// of waiting would have helped. Still gated behind UBX_TEST_SLOW=1
// (~19s is still slow relative to this package's normal suite), same
// reasoning as cli/ship_lying_destroy_test.go's own gate. core/executor's
// own hermetic suite (create_dependency_retry_test.go, shrunk schedule
// AND shrunk threshold) is the fast, always-run regression guard for the
// same logic; this test's own job is proving the identical behavior
// survives the real wire protocol.
func TestShip_CreateFailsNotFoundReferencingShippedDependency_BudgetExhausted_RealFakeProvider(t *testing.T) {
	if os.Getenv("UBX_TEST_SLOW") == "" {
		t.Skip("set UBX_TEST_SLOW=1 to run this real-wire-protocol test -- it takes about ~19s (core/executor's own noProgressBailoutThreshold, UBI-93, now bails out well before eventualConsistencyBackoffSchedule's own full ~64s budget)")
	}

	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "playground",
		"intent":         map[string]interface{}{"summary": "UBI-92: role + attach, dependency never becomes visible"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "role",
				"op":   "create",
				"config": map[string]interface{}{
					"name": "ci-runner-v99",
				},
			},
			{
				"type": "fake_widget",
				"name": "attach",
				"op":   "create",
				"config": map[string]interface{}{
					// See the sibling (non-budget-exhausted) test's own
					// comment: a distinct "name" of its own, the $ref
					// lives in "tags" so the depends_on edge onto role
					// still forms without colliding fakeprovider's own
					// name-keyed fault-injection target with role's.
					"name": "attach-ci-runner-v99",
					"tags": map[string]interface{}{
						"role": map[string]interface{}{
							"$ref": map[string]interface{}{"to": "playground.fake_widget.role.name"},
						},
					},
				},
			},
		},
	})

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath)
	if err != nil {
		t.Fatalf("ubx resolve: %v\noutput: %s", err, resolveOut)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	// A budget far larger than eventualConsistencyBackoffSchedule's own 10
	// steps -- attach's create never becomes visible within the retry
	// window, so it must still fail terminally.
	shipEnv := []string{
		"FAKEPROVIDER_MODE=ok-v6",
		"FAKEPROVIDER_APPLY_MODE=fail-create-not-found",
		"FAKEPROVIDER_FAIL_CREATE_TARGET=attach-ci-runner-v99",
		"FAKEPROVIDER_FAIL_CREATE_COUNT=1000",
	}
	shipOut, err := runUbx(t, shipEnv, "ship", changeID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if exitCode(err) != 1 {
		t.Fatalf("ubx ship against a dependency that never becomes visible: want exit 1 (an outcome other than fully applied), got %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "1 shipped") || !strings.Contains(shipOut, "1 failed") {
		t.Fatalf("expected the summary to show 1 shipped (role), 1 failed (attach), got: %s", shipOut)
	}

	whyOut, err := runUbx(t, env, "why", changeID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", changeID, err, whyOut)
	}
	if !strings.Contains(whyOut, "error (terminal)") {
		t.Fatalf("expected attach's own exhausted retry to surface as a terminal error, got: %s", whyOut)
	}
	if !strings.Contains(whyOut, "cannot be found") {
		t.Fatalf("expected the original not-found diagnostic to still be the surfaced error, got: %s", whyOut)
	}
	if !strings.Contains(whyOut, "does not appear to exist") {
		t.Fatalf("expected UBI-93's own honest early-bailout wording, got: %s", whyOut)
	}
}
