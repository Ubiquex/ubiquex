package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveAcceptShip_CreateChain_RealFakeProvider is a fakeprovider-level
// dry run of the full UBI-27 pipeline against the actual built binary's own
// CLI wiring: resolve -> accept -> ship, a two-resource create chain
// (fake_widget "primary" plus "mirror", which cross-references primary's
// own name -- concrete -- and its schema-Computed id -- $computed,
// substituted mid-walk once primary applies). This is cheaper than the
// real AWS finale and catches CLI/wiring bugs (proposal JSON shapes,
// stateReaderAdapter/provider.Provider plumbing) before spending real
// cloud calls on the same mechanism.
func TestResolveAcceptShip_CreateChain_RealFakeProvider(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "primary + mirror create chain"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "primary",
				"op":   "create",
				"config": map[string]interface{}{
					"name": "primary-widget",
					"tags": map[string]string{"env": "prod"},
				},
			},
			{
				"type": "fake_widget",
				"name": "mirror",
				"op":   "create",
				"config": map[string]interface{}{
					"name": map[string]interface{}{
						"$ref": map[string]interface{}{"to": "payments.fake_widget.primary.name"},
					},
					"tags": map[string]interface{}{
						"peer_id": map[string]interface{}{
							"$ref": map[string]interface{}{"to": "payments.fake_widget.primary.id"},
						},
					},
				},
			},
		},
	})

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
	)
	if err != nil {
		t.Fatalf("ubx resolve: %v\noutput: %s", err, resolveOut)
	}
	if !strings.Contains(resolveOut, "2 create(s), 0 modify(ies)") {
		t.Fatalf("expected a 2-create summary, got: %s", resolveOut)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	shipOut, err := runUbx(t, env, "ship", changeID,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "outcome: applied") {
		t.Fatalf("expected outcome: applied, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "applied: payments.fake_widget.primary") {
		t.Fatalf("expected primary to show applied, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "applied: payments.fake_widget.mirror") {
		t.Fatalf("expected mirror to show applied, got: %s", shipOut)
	}

	whyOut, err := runUbx(t, env, "why", changeID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "apply history:") {
		t.Fatalf("expected why to render apply history for a change proposal, got: %s", whyOut)
	}
	if !strings.Contains(whyOut, "payments.fake_widget.mirror") {
		t.Fatalf("expected why's apply history to mention mirror, got: %s", whyOut)
	}
}
