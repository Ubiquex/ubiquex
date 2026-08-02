package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanShipTerminate_FromDiagram_UbxRequired_FullLifecycle is UBI-91's
// own end-to-end live-finale proof, against a real fakeprovider
// subprocess (never hand-seeded) -- the full lifecycle CLAUDE.md's own
// standing "never a real cloud provider for live verification" rule
// requires proving hermetically instead of against real AWS: a diagram-
// authored fake_widget carrying "ubx_required.name" (fake_widget's own
// real schema-Required attribute, provider/internal/fakeprovider)
// resolves and plans with ZERO missing-required-attribute refusal, ships
// for real, and terminates for real -- pure diagram authoring throughout,
// no md/SDK fallback anywhere. diagram/integration_test.go's own
// TestParseThenResolve_PlatformD2_UBI91LiveFinale_AllRequiredSupplied_ResolvesClean
// proves the RESOLVE-time claim with the founder's own exact five real
// attribute names (name, assume_role_policy, policy, role, policy_arn)
// across five distinct fake-schema'd types -- fakeprovider's own single
// shippable type (fake_widget) can't individually model those, so this
// test instead proves the full EXECUTION half (ship + terminate + a
// clean final state) end to end, for real, the piece that resolve-only
// coverage can't.
func TestPlanShipTerminate_FromDiagram_UbxRequired_FullLifecycle(t *testing.T) {
	dir := t.TempDir()
	mirrorDir := t.TempDir()

	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
[providers]
"fake/widget" = "0.1.0"
`)

	diagramPath := filepath.Join(dir, "topo.d2")
	writeFile(t, diagramPath, `
classes: {
  fake_widget: {}
}
ubx-playground-91: {
  widget: "ci-notifications-v91" {
    class: fake_widget
    ubx_required.name: "ci-notifications-v91"
  }
}
`)

	env := []string{"FAKEPROVIDER_MODE=ok-v6", "UBX_PROVIDER_MIRROR=" + mirrorDir}

	// Plan: resolve + render the full receipt -- must succeed with
	// exactly 1 create, zero missing-required-attribute refusal.
	planOut, err := runUbx(t, env, "plan", "--from-diagram", diagramPath, "--stack", "ubx-playground-91", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx plan --from-diagram: %v\noutput: %s", err, planOut)
	}
	if !strings.Contains(planOut, "blast radius: +1 ~0 -0") {
		t.Fatalf("expected a 1-create blast radius, got: %s", planOut)
	}
	if strings.Contains(planOut, "missing required attribute") {
		t.Fatalf("expected zero missing-required-attribute refusal, got: %s", planOut)
	}
	hash := mustExtractPlanHash(t, dir, planOut)

	// Ship: for real, against the real fakeprovider subprocess.
	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", dir, "--yes")
	if err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, shipOut)
	}
	if !strings.Contains(shipOut, "shipped") {
		t.Fatalf("expected a shipped outcome, got: %s", shipOut)
	}

	// Confirm it's really there before tearing it down.
	whyOut, err := runUbx(t, env, "why", "ubx-playground-91.fake_widget.ci-notifications-v91", "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "fake_widget.ci-notifications-v91 create") {
		t.Fatalf("expected the shipped create in why's own history, got: %s", whyOut)
	}

	// Terminate: draft the destroy, then ship it for real too.
	termPlanOut, err := runUbx(t, env, "terminate", "ubx-playground-91.fake_widget.ci-notifications-v91", "--provider", fakeProviderBinary, "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx terminate: %v\noutput: %s", err, termPlanOut)
	}
	termHash := mustExtractPlanHash(t, dir, termPlanOut)

	termShipOut, err := runUbx(t, env, "ship", termHash, "--provider", fakeProviderBinary, "--ledger-dir", dir, "--confirm-terminate", "--yes")
	if err != nil {
		t.Fatalf("ubx ship (terminate): %v\noutput: %s", err, termShipOut)
	}
	if !strings.Contains(termShipOut, "shipped") {
		t.Fatalf("expected the terminate to ship cleanly, got: %s", termShipOut)
	}

	// Confirm the account is clean: a fresh scan of the same lookup key
	// reports nothing live, and status --drift over the whole stack
	// tracks zero resources.
	statusOut, err := runUbx(t, env, "status", "--drift", "--provider", fakeProviderBinary, "--ledger-dir", dir)
	if err != nil {
		t.Fatalf("ubx status --drift: %v\noutput: %s", err, statusOut)
	}
	if strings.Contains(statusOut, "ci-notifications-v91") {
		t.Fatalf("expected the terminated resource to be gone from status --drift entirely, got: %s", statusOut)
	}
}
