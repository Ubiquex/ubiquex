package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runUbxTTY is runUbx's own interactive-confirmation sibling: forces
// isTerminal true (the same package-var-swap seam withTerminal uses) and
// feeds stdin from a real string, rather than the empty reader runUbx
// always sets -- for exercising ubx ship's own "type yes" prompt
// hermetically, with no real pty required.
func runUbxTTY(t *testing.T, stdin string, env []string, args ...string) (string, error) {
	t.Helper()
	withTerminal(t, true)
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		t.Setenv(parts[0], parts[1])
	}
	err := root.Execute()
	return out.String(), err
}

// TestShip_TTY_TypedYes_Accepts is UBI-62's own core acceptance test: on
// a real (forced) terminal, the receipt renders again and a typed "yes"
// is what actually signs the plan -- no --yes flag involved at all.
func TestShip_TTY_TypedYes_Accepts(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	shipOut, err := runUbxTTY(t, "yes\n", env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx ship (typed yes): %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "Ship this to payments? Only 'yes' accepted:") {
		t.Fatalf("expected the exact confirmation prompt, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut)
	}
}

// TestShip_TTY_TypedSomethingElse_DeclinesCleanly proves declining is a
// deliberate abort (exit 0), not a finding or an error, and that nothing
// is accepted or applied.
func TestShip_TTY_TypedSomethingElse_DeclinesCleanly(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)
	ledgerHeadBefore := readLedgerHead(t, ledgerDir)

	shipOut, err := runUbxTTY(t, "nope\n", env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("expected a clean exit 0 on decline, got: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "ship aborted -- nothing accepted or shipped") {
		t.Fatalf("expected the abort message, got: %s", shipOut)
	}
	if got := readLedgerHead(t, ledgerDir); got != ledgerHeadBefore {
		t.Fatalf("ledger head moved after a declined confirmation -- must never accept, got %q want %q", got, ledgerHeadBefore)
	}
	// The plan file must survive a decline too -- only a successful
	// accept prunes it.
	if _, err := os.Stat(planFilePath(ledgerDir, hash)); err != nil {
		t.Fatalf("expected the declined plan file to still exist: %v", err)
	}
}

// TestShip_NonTTY_NoYes_RefusesWithTeachingError is UBI-62's own "never
// silently proceed" requirement: no prompt is ever attempted on a
// non-interactive stdin without --yes.
func TestShip_NonTTY_NoYes_RefusesWithTeachingError(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	_, err = runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), "not an interactive terminal") {
		t.Fatalf("expected a teaching error naming --yes, got: %v", err)
	}
}

// TestShip_Bare_ResolvesLatestPlanForStack is UBI-62's own "bare ubx
// ship = latest plan for current stack" acceptance test.
func TestShip_Bare_ResolvesLatestPlanForStack(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
		},
	})
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, ledgerDir, planOut)

	shipOut, err := runUbx(t, env, "ship", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes")
	if err != nil {
		t.Fatalf("ubx ship (bare): %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "latest plan for stack payments: "+shortRef(hash)) {
		t.Fatalf("expected the resolved 'latest plan' to be shown explicitly, got: %s", shipOut)
	}
	if !strings.Contains(shipOut, "outcome: shipped") {
		t.Fatalf("expected outcome: shipped, got: %s", shipOut)
	}

	// Shipped plan must be pruned -- "latest" never re-offers a
	// consumed plan.
	if _, err := os.Stat(planFilePath(ledgerDir, hash)); !os.IsNotExist(err) {
		t.Fatalf("expected the shipped plan file to be pruned, stat err = %v", err)
	}
}

// TestShip_Bare_MultipleStacks_NonTTY_RefusesWithList proves multiple
// distinct stacks' worth of unshipped plans are never guessed between.
func TestShip_Bare_MultipleStacks_NonTTY_RefusesWithList(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	for _, stack := range []string{"payments", "networking"} {
		intentPath := filepath.Join(ledgerDir, stack+"-intent.json")
		writeIntentFile(t, intentPath, map[string]interface{}{
			"schema_version": 1,
			"kind":           "ubx:intent/v1",
			"stack":          stack,
			"intent":         map[string]interface{}{"summary": "widget via ubx plan"},
			"resources": []map[string]interface{}{
				{"type": "fake_widget", "name": "widget1", "op": "create", "config": map[string]interface{}{"name": "widget1"}},
			},
		})
		if out, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir); err != nil {
			t.Fatalf("ubx plan (%s): %v\noutput: %s", stack, err, out)
		}
	}

	out, err := runUbx(t, env, "ship", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, out)
	if !strings.Contains(out, "multiple stacks have unshipped plans") {
		t.Fatalf("expected the multi-stack candidate list, got: %s", out)
	}
	if !strings.Contains(out, "payments") || !strings.Contains(out, "networking") {
		t.Fatalf("expected both stacks named in the list, got: %s", out)
	}
	if !strings.Contains(err.Error(), "more than one stack") {
		t.Fatalf("expected the refusal to explain why, got: %v", err)
	}
}

// TestShip_Bare_NoPlansAtAll_ErrorsCleanly proves an empty plan store
// gets a clear, specific error rather than a confusing one.
func TestShip_Bare_NoPlansAtAll_ErrorsCleanly(t *testing.T) {
	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "ship", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "no plans in .ubx/plans/") {
		t.Fatalf("expected a clear empty-plan-store error, got: %v", err)
	}
}
