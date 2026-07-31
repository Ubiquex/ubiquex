package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPlan_ReceiptV2_Format is docs/cli-output-spec.md §v2's own receipt
// format, end to end (UBI-63): no AI summary sentence under the header,
// a JSON-valued (map) attribute renders as a formatted multi-line block
// rather than an escaped single-line string, a JSON-embedded string
// attribute renders the same way, a resolved $computed marker renders
// as the friendly "$ref:<address>" notation, one blank line separates
// resource blocks, and the footer carries no plan-path line.
func TestPlan_ReceiptV2_Format(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "platform",
		"intent":         map[string]interface{}{"summary": "a widget referencing another widget's computed id"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "widget1",
				"op":   "create",
				"config": map[string]interface{}{
					"name": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow"}]}`,
					"tags": map[string]string{"env": "prod"},
				},
			},
			{
				"type": "fake_widget",
				"name": "widget2",
				"op":   "create",
				"config": map[string]interface{}{
					"name": map[string]interface{}{
						"$ref": map[string]interface{}{"to": "platform.fake_widget.widget1.id"},
					},
				},
			},
		},
	})

	out, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, out)
	}

	// No AI summary sentence under the header.
	if strings.Contains(out, "a widget referencing another widget's computed id") {
		t.Fatalf("expected the AI summary sentence to be removed from the receipt, got:\n%s", out)
	}

	// JSON-embedded string attribute renders as a formatted block, not
	// an escaped single-line string.
	if strings.Contains(out, `\"Version\"`) {
		t.Fatalf("expected the JSON-embedded string to render formatted, not escaped, got:\n%s", out)
	}
	if !strings.Contains(out, "\"Version\": \"2012-10-17\"") {
		t.Fatalf("expected the JSON-embedded string's own decoded content formatted, got:\n%s", out)
	}

	// A resolved $computed marker renders as the friendly $ref: notation.
	if !strings.Contains(out, "name: $ref:platform.fake_widget.widget1.id") {
		t.Fatalf("expected the $computed marker to render as $ref:<address>, got:\n%s", out)
	}
	if strings.Contains(out, "$computed") {
		t.Fatalf("expected the raw $computed envelope to never appear in the receipt, got:\n%s", out)
	}

	// One blank line between resource blocks: "widget1 create" block ends
	// with its own tags block, then exactly one blank line, then the
	// "widget2 create" header.
	idx1 := strings.Index(out, "+ fake_widget.widget1 create")
	idx2 := strings.Index(out, "+ fake_widget.widget2 create")
	if idx1 < 0 || idx2 < 0 || idx1 > idx2 {
		t.Fatalf("expected both resource headers in order, got:\n%s", out)
	}
	between := out[idx1:idx2]
	if strings.Count(between, "\n\n") != 1 {
		t.Fatalf("expected exactly one blank line between resource blocks, got:\n%s", between)
	}

	// Footer: no plan-path line, hash + next hint present.
	hash := mustExtractPlanHash(t, ledgerDir, out)
	if strings.Contains(out, "plan: "+filepath.Join(ledgerDir, ".ubx", "plans")) {
		t.Fatalf("expected no plan-path line in the footer, got:\n%s", out)
	}
	if !strings.Contains(out, "ubx-proposal: "+displayHash(hash, false)) {
		t.Fatalf("expected the ubx-proposal footer line, got:\n%s", out)
	}
	if !strings.Contains(out, "next: ubx ship "+shortRef(hash)) {
		t.Fatalf("expected the next: ubx ship footer line, got:\n%s", out)
	}
}
