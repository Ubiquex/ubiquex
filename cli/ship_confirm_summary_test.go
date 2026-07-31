package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestShip_ConfirmSummary_DoesNotReRenderFullReceipt is UBI-63 bug 3's
// own regression test: the full receipt already rendered once, at `ubx
// plan` time -- ship's own confirmation must show just the one-line
// summary (stack, age, blast radius) plus the typed-yes prompt, never a
// second full copy of the resource/attribute breakdown a human already
// reviewed.
func TestShip_ConfirmSummary_DoesNotReRenderFullReceipt(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1", "tags": map[string]string{"env": "prod"}}},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	// The full receipt DOES render at plan time -- this is the review.
	if !strings.Contains(planOut, "fake_widget.widget1 create") {
		t.Fatalf("expected the full receipt to render at plan time, got: %s", planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	shipOut, err := runUbxTTY(t, "yes\n", env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if strings.Contains(shipOut, "fake_widget.widget1 create") || strings.Contains(shipOut, `"env":"prod"`) {
		t.Fatalf("expected ship's own confirmation to NOT re-render the full receipt (no resource/attribute breakdown), got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "Ship  payments ·") {
		t.Fatalf("expected the one-line summary's own header, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "+1 create(s)") || !strings.Contains(shipOut, "~0 modify(ies)") || !strings.Contains(shipOut, "-0 destroy(s)") {
		t.Fatalf("expected the one-line summary to still carry the blast radius, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "Ship this to payments? Only 'yes' accepted:") {
		t.Fatalf("expected the confirmation prompt, got: %s", shipOut)
	}
}
