package cli

import (
	"os"
	"strings"
	"testing"
)

// TestAccept_ShortHashFromPlanStore_Accepts is UBI-49 finding #6's own
// acceptance test for accept's plan-store fallback: a plan `ubx plan`
// saved resolves and accepts by hash directly, no file path involved at
// all -- previously only a file path worked, and a hash produced a raw
// os.ReadFile error.
func TestAccept_ShortHashFromPlanStore_Accepts(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := ledgerDir + "/intent.json"
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "widget1",
				"op":   "create",
				"config": map[string]interface{}{
					"name": "widget1",
				},
			},
		},
	})

	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	fullHash := mustExtractPlanHash(t, planOut)
	hash := shortRef(fullHash)

	acceptOut, err := runUbx(t, env, "accept", hash, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept %s (plan-store short hash): %v\noutput: %s", hash, err, acceptOut)
	}
	if !strings.Contains(acceptOut, "accepted") {
		t.Fatalf("expected an acceptance report, got: %s", acceptOut)
	}

	// UBI-62: a plan consumed via accept is pruned too, same as ship's own
	// inline-accept path -- "latest" must never re-offer it.
	if _, err := os.Stat(planFilePath(ledgerDir, fullHash)); !os.IsNotExist(err) {
		t.Fatalf("expected the accepted plan file to be pruned, stat err = %v", err)
	}
}

// TestAccept_GarbageArg_TeachingErrorNamesBothInterpretations is UBI-49
// finding #6's own error-message requirement: given something that's
// neither a real file nor a real plan-store hash, accept's error must
// name both interpretations (docs/cli-output-spec.md principle 6).
func TestAccept_GarbageArg_TeachingErrorNamesBothInterpretations(t *testing.T) {
	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "accept", "not-a-real-file-or-hash", "--ledger-dir", ledgerDir)
	if err == nil {
		t.Fatal("expected an error for a garbage argument")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error = %v, want it to name the file-path interpretation", err)
	}
	if !strings.Contains(err.Error(), "no matching plan") {
		t.Errorf("error = %v, want it to name the plan-store interpretation", err)
	}
	if !strings.Contains(err.Error(), "ubx plan") || !strings.Contains(err.Error(), "ubx scan --propose") {
		t.Errorf("error = %v, want it to name where a valid hash comes from", err)
	}
}
