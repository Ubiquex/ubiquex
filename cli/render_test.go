package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shipTwoWidgetChain runs a real resolve -> accept -> ship pipeline
// against the fakeprovider binary (the identical fixture shape
// cli/ship_change_test.go's own TestResolveAcceptShip_CreateChain_
// RealFakeProvider already established) so ubx render has a real,
// shipped, live fleet to walk: "primary" plus "mirror", which
// $ref-references primary's own name -- a real depends_on edge shows up
// on mirror's own resolved create node as a direct result, the exact
// shape render's own emitter reads back via each live resource's
// creating proposal.
func shipTwoWidgetChain(t *testing.T, ledgerDir string) {
	t.Helper()
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
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut)
	}
}

func TestRender_TwoResourcesWithDependsOn_RealFakeProvider(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	out, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, `"mirror"`) || !strings.Contains(out, `"primary"`) {
		t.Fatalf("expected both resources by name, got: %s", out)
	}
	if !strings.Contains(out, "class: fake_widget") {
		t.Fatalf("expected class: fake_widget, got: %s", out)
	}
	if !strings.Contains(out, "->") {
		t.Fatalf("expected a depends_on edge from mirror's own $ref, got: %s", out)
	}
	if !strings.Contains(out, "name: primary-widget") {
		t.Fatalf("expected primary's own real applied attributes in a tooltip, got: %s", out)
	}
}

func TestRender_Out_WritesFile(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	outPath := filepath.Join(ledgerDir, "rendered.d2")
	out, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir, "--out", outPath)
	if err != nil {
		t.Fatalf("ubx render: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "wrote "+outPath) {
		t.Fatalf("unexpected stdout: %s", out)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected %s to exist: %v", outPath, err)
	}
}

func TestRender_Check_MatchingFile_Succeeds(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	outPath := filepath.Join(ledgerDir, "rendered.d2")
	if out, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir, "--out", outPath); err != nil {
		t.Fatalf("ubx render (write): %v\noutput: %s", err, out)
	}

	out, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir, "--out", outPath, "--check")
	if err != nil {
		t.Fatalf("ubx render --check (should match): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "matches the current resolved state") {
		t.Fatalf("unexpected stdout: %s", out)
	}
}

func TestRender_Check_StaleFile_Fails(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	outPath := filepath.Join(ledgerDir, "rendered.d2")
	writeFile(t, outPath, "r0: \"stale\" {\n  class: fake_widget\n}\n")

	out, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir, "--out", outPath, "--check")
	requireExitCode(t, err, 1, out)
	if !strings.Contains(err.Error(), "is stale") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "-stale") && !strings.Contains(out, "+r0") {
		t.Fatalf("expected a unified diff in stdout, got: %s", out)
	}
}

func TestRender_Check_MissingFile_Fails(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	outPath := filepath.Join(ledgerDir, "does-not-exist.d2")
	out, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir, "--out", outPath, "--check")
	requireExitCode(t, err, 1, out)
	if !strings.Contains(err.Error(), "does not exist yet") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRender_Check_WithoutOut_Errors(t *testing.T) {
	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir, "--check")
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "--check requires --out") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRender_NoStack_Errors(t *testing.T) {
	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "render", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "--stack is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRender_EmptyStack_SucceedsWithEmptyOutput(t *testing.T) {
	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render (no live resources): %v\noutput: %s", err, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty output for a stack with no live resources, got: %q", out)
	}
}

// TestRender_CrossStackReference_RealFakeProvider proves UBI-47 session
// 4's own real ResolutionInput.From fix end to end, through the full CLI
// pipeline -- not just emitD2's own unit-level fixture. Two real,
// separate ledgers: "networking" ships one widget; "payments" ships a
// second widget whose own config $cross-references the first. ubx render
// against the payments ledger must draw a reference node annotated with
// the real pinned head, edged from the correct referencing resource --
// only possible because resolveCross now stamps From with the local
// resource's own address.
func TestRender_CrossStackReference_RealFakeProvider(t *testing.T) {
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	networkingDir := t.TempDir()
	writeIntentFile(t, filepath.Join(networkingDir, "intent.json"), map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "networking",
		"intent":         map[string]interface{}{"summary": "shared widget"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "main",
				"op":   "create",
				"config": map[string]interface{}{
					"name": "shared-widget",
				},
			},
		},
	})
	networkingResolved := filepath.Join(networkingDir, "resolved.json")
	if out, err := runUbx(t, env, "resolve", filepath.Join(networkingDir, "intent.json"),
		"--provider", fakeProviderBinary, "--ledger-dir", networkingDir, "--out", networkingResolved); err != nil {
		t.Fatalf("ubx resolve (networking): %v\noutput: %s", err, out)
	}
	acceptOut, err := runUbx(t, env, "accept", networkingResolved, "--ledger-dir", networkingDir)
	if err != nil {
		t.Fatalf("ubx accept (networking): %v\noutput: %s", err, acceptOut)
	}
	networkingChangeID := mustExtractID(t, acceptOut)
	if out, err := runUbx(t, env, "ship", networkingChangeID, "--provider", fakeProviderBinary, "--ledger-dir", networkingDir); err != nil {
		t.Fatalf("ubx ship (networking): %v\noutput: %s", err, out)
	}

	paymentsDir := t.TempDir()
	writeIntentFile(t, filepath.Join(paymentsDir, "intent.json"), map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "references the shared widget"},
		"resources": []map[string]interface{}{
			{
				"type": "fake_widget",
				"name": "consumer",
				"op":   "create",
				"config": map[string]interface{}{
					"name": map[string]interface{}{
						"$cross": map[string]interface{}{
							"ledger_dir": networkingDir,
							"to":         "networking.fake_widget.main.name",
						},
					},
				},
			},
		},
	})
	paymentsResolved := filepath.Join(paymentsDir, "resolved.json")
	if out, err := runUbx(t, env, "resolve", filepath.Join(paymentsDir, "intent.json"),
		"--provider", fakeProviderBinary, "--ledger-dir", paymentsDir, "--out", paymentsResolved); err != nil {
		t.Fatalf("ubx resolve (payments): %v\noutput: %s", err, out)
	}
	acceptOut, err = runUbx(t, env, "accept", paymentsResolved, "--ledger-dir", paymentsDir)
	if err != nil {
		t.Fatalf("ubx accept (payments): %v\noutput: %s", err, acceptOut)
	}
	paymentsChangeID := mustExtractID(t, acceptOut)
	if out, err := runUbx(t, env, "ship", paymentsChangeID, "--provider", fakeProviderBinary, "--ledger-dir", paymentsDir); err != nil {
		t.Fatalf("ubx ship (payments): %v\noutput: %s", err, out)
	}

	out, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", paymentsDir)
	if err != nil {
		t.Fatalf("ubx render: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, `"consumer"`) {
		t.Fatalf("expected the referencing resource by name, got: %s", out)
	}
	if !strings.Contains(out, `"@networking.fake_widget.main"`) {
		t.Fatalf("expected a reference node labeled with the pinned neighbor address, got: %s", out)
	}
	if !strings.Contains(out, "class: external") {
		t.Fatalf("expected the reference node's own class: external, got: %s", out)
	}
	if !strings.Contains(out, "pinned_head:") {
		t.Fatalf("expected a pinned_head annotation, got: %s", out)
	}
	if !strings.Contains(out, "->") {
		t.Fatalf("expected an edge from consumer to the reference node, got: %s", out)
	}
}

func TestRender_Determinism_RepeatedRunsByteIdentical(t *testing.T) {
	ledgerDir := t.TempDir()
	shipTwoWidgetChain(t, ledgerDir)

	first, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render: %v\noutput: %s", err, first)
	}
	second, err := runUbx(t, nil, "render", "--stack", "payments", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render (rerun): %v\noutput: %s", err, second)
	}
	if first != second {
		t.Fatalf("ubx render produced different output on a rerun against an unchanged ledger:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
