package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPaymentsGoldenCase_Render is the diagram medium's own conformance
// suite (docs/diagram-medium.md's slice 5), RENDER direction --
// diagram/conformance/runner/runner_test.go's own PARSE-direction sibling,
// split into this package deliberately (see that file's own package doc
// for the full reasoning): Emit needs a real, shipped Fleet entry, which
// needs the real core/executor.Applier adapter this package already owns
// correctly (cli/stateadapter.go), reimplementing it a second time
// elsewhere would risk a real divergence for no benefit.
//
// Ships the identical topology diagram/conformance/golden/payments.d2
// and golden/payments-topology.json both describe -- main-vpc,
// payments-db depending on it -- through the hermetic fakeprovider
// binary (never a real cloud provider, this session's own explicit
// "hermetic only" instruction, and the standing rule UBI-47 session 4's
// own real AWS incident established: fakeprovider + UBX_PROVIDER_MIRROR
// for anything that reaches `ubx ship`), renders it, and byte-compares
// against golden/payments-rendered.d2 -- a real, ongoing regression test
// for Emit's own determinism and correctness, the same discipline
// sdk/conformance/runner already established for the SDK arc.
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

	goldenPath := filepath.Join("..", "diagram", "conformance", "golden", "payments-rendered.d2")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", goldenPath, err)
	}

	if got != string(want) {
		t.Fatalf("rendered output does not match the golden fixture, byte for byte:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
