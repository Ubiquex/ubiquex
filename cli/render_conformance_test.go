package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPaymentsGoldenCase_Render is diagram.Emit's own conformance test
// (UBI-224: the PARSE-direction sibling this once split from,
// diagram/conformance/runner/runner_test.go, is gone along with the
// diagram authoring medium it tested; Emit itself is a read-only
// projection of ledger state, load-bearing for `ubx render`, and stays).
// Emit needs a real, shipped Fleet entry, which needs the real
// core/executor.Applier adapter this package already owns correctly
// (cli/stateadapter.go), reimplementing it a second time elsewhere
// would risk a real divergence for no benefit -- why this test lives
// here rather than in the diagram package itself.
//
// Ships a real, hand-built topology (main-vpc, payments-db depending on
// it, matching what the now-removed parse-direction fixture used to
// describe) through the hermetic fakeprovider binary (never a real
// cloud provider, the standing rule UBI-47 session 4's own real AWS
// incident established: fakeprovider + UBX_PROVIDER_MIRROR for anything
// that reaches `ubx ship`), renders it, and byte-compares against
// testdata/render_golden/payments-rendered.d2 -- a real, ongoing
// regression test for Emit's own determinism and correctness, the same
// discipline sdk/conformance/runner already established for the SDK arc.
func TestPaymentsGoldenCase_Render(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "conformance fixture #1"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "main-vpc",
				"op":   "create",
				"config": map[string]interface{}{
					"name": "main-vpc",
				},
			},
			{
				"type": "fake_widget",
				"name": "payments-db",
				"op":   "create",
				"config": map[string]interface{}{
					"name": "payments-db",
				},
				"depends_on": []string{"payments.fake_widget.main-vpc"},
			},
		},
	})

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	if out, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath); err != nil {
		t.Fatalf("ubx resolve: %v\noutput: %s", err, out)
	}
	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)
	if out, err := runUbx(t, env, "ship", changeID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, out)
	}

	got, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render: %v\noutput: %s", err, got)
	}

	goldenPath := filepath.Join("testdata", "render_golden", "payments-rendered.d2")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", goldenPath, err)
	}

	if got != string(want) {
		t.Fatalf("rendered output does not match the golden fixture, byte for byte:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
