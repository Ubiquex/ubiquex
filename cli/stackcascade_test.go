package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPlan_FromDoc_StackFromConfig_NoFlag is UBI-49 finding #2's own
// repro, fixed: .ubx/config's own `stack` key must be consulted by
// `ubx plan --from-doc` exactly like every other post-cascade verb,
// never requiring the flag just because this input mode wires its own
// flag handling (plan.go's own applyStackDefault call, added this
// session).
func TestPlan_FromDoc_StackFromConfig_NoFlag(t *testing.T) {
	fake := &fakeIntentAdapter{draft: `{
		"schema_version": 1,
		"kind": "ubx:intent/v1",
		"stack": "playground",
		"intent": {"summary": "queue via ubx plan --from-doc"},
		"resources": [
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": "{\"name\":\"widget1\"}"}
		],
		"destroys": []
	}`}
	withBuildIntentAdapter(t, fake)

	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)
	writeConfig(t, ledgerDir, `stack = "playground"`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	docPath := filepath.Join(ledgerDir, "queue.md")
	writeFile(t, docPath, "# Queue\n\nA single widget.")

	out, err := runUbx(t, env, "plan",
		"--from-doc", docPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx plan --from-doc with config-supplied stack, no --stack flag: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "blast radius: +1 ~0 -0") {
		t.Fatalf("expected the resolved delta's own blast radius, got: %s", out)
	}

	wantProgress := "drafting via claude:claude-opus-4-8… ✓ · resolving…"
	progressIdx := strings.Index(out, wantProgress)
	receiptIdx := strings.Index(out, "Plan  playground")
	if progressIdx < 0 {
		t.Fatalf("expected docs/cli-output-spec.md §v2's own pre-receipt progress line %q, got: %s", wantProgress, out)
	}
	if receiptIdx < 0 || progressIdx > receiptIdx {
		t.Fatalf("expected the progress line to render BEFORE the receipt, got: %s", out)
	}
}

// TestPlan_FromDoc_NoStackAnywhere_TeachingError proves the upgraded
// error text (finding #2's own secondary ask): names both the flag and
// the config key, and points at the nearest config file actually
// consulted when one exists (even if it doesn't declare `stack`).
func TestPlan_FromDoc_NoStackAnywhere_TeachingError(t *testing.T) {
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: proposeFromDocGoodDraft})

	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)
	writeConfig(t, ledgerDir, `github_repo = "example/repo"`) // present, but no stack key

	docPath := filepath.Join(ledgerDir, "queue.md")
	writeFile(t, docPath, "# Queue\n\nA single widget.")

	out, err := runUbx(t, nil, "plan", "--from-doc", docPath, "--ledger-dir", ledgerDir)
	if err == nil {
		t.Fatalf("expected an error with no stack anywhere, got output: %s", out)
	}
	if !strings.Contains(err.Error(), "--stack is required") {
		t.Errorf("error = %v, want it to name --stack", err)
	}
	if !strings.Contains(err.Error(), `stack = "..."`) {
		t.Errorf("error = %v, want it to name the config key", err)
	}
	if !strings.Contains(err.Error(), "nearest config checked:") || !strings.Contains(err.Error(), filepath.Join(ledgerDir, ".ubx", "config")) {
		t.Errorf("error = %v, want it to name the consulted config file", err)
	}
}

// TestPlan_FromDiagram_StackFromConfig_NoFlag mirrors the --from-doc case
// above for --from-diagram's own identical bug/fix. UBI-90: fake_widget's
// own schema-Required "name" (topology-only, so the diagram can never
// supply it) now makes plan's own resolve step refuse -- still exactly
// what this test needs to prove, since the refusal error names the
// address "playground.fake_widget.main-widget": the ONLY way that address
// carries "playground" is if the stack really did cascade from config with
// no --stack flag given. A resolve failure for the wrong reason (e.g. "no
// stack" or a ledger opened under the wrong directory) would name a
// different stack, or no address at all -- this still fails the assertion
// below exactly as before.
func TestPlan_FromDiagram_StackFromConfig_NoFlag(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "playground"

[providers]
"fake/widget" = "0.1.0"
`)

	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, `
classes: {
  fake_widget: {}
}
playground: {
  primary: main-widget {
    class: fake_widget
  }
}
`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6", "UBX_PROVIDER_MIRROR=" + mirrorDir}
	out, err := runUbx(t, env, "plan", "--from-diagram", diagramPath, "--ledger-dir", dir)
	if err == nil {
		t.Fatalf("ubx plan --from-diagram with config-supplied stack, no --stack flag: expected a required-attribute refusal, got success: %s", out)
	}
	if !strings.Contains(err.Error(), "playground.fake_widget.main-widget") {
		t.Fatalf("expected the refusal to name playground.fake_widget.main-widget (proving the stack cascaded from config), got: %v\noutput: %s", err, out)
	}
}

// TestPropose_FromDoc_StackFromConfig_NoFlag proves propose.go's own
// from-doc RunE got the identical fix plan.go did -- both call the same
// draftFromDoc, and both had the same pre-fix bug.
func TestPropose_FromDoc_StackFromConfig_NoFlag(t *testing.T) {
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: proposeFromDocGoodDraft})

	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)
	writeConfig(t, ledgerDir, `stack = "payments"`) // matches proposeFromDocGoodDraft's own declared stack

	docPath := filepath.Join(ledgerDir, "queue.md")
	writeFile(t, docPath, "# Queue\n\nA single widget.")

	out, err := runUbx(t, nil, "propose", "--from-doc", docPath)
	if err != nil {
		t.Fatalf("ubx propose --from-doc with config-supplied stack, no --stack flag: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, `"stack"`) {
		t.Fatalf("expected the drafted intent file in the output, got: %s", out)
	}
}

// TestChat_StackFromConfig_NoFlag proves chat.go's reordered check
// (config loaded and applyStackDefault'd before the stack==="" check,
// rather than after) actually lets config supply the value.
func TestChat_StackFromConfig_NoFlag(t *testing.T) {
	withBuildIntentAdapter(t, &chatFakeAdapter{responses: []string{proposeFromDocGoodDraft}})

	ledgerDir := t.TempDir()
	withConfigSearchDir(t, ledgerDir)
	writeConfig(t, ledgerDir, `stack = "playground"`)

	out, err := runUbxWithStdin(t, "We need a Postgres database for payments.\n/quit\n", "chat", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx chat with config-supplied stack, no --stack flag: %v\noutput: %s", err, out)
	}
}
