package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShip_LyingDestroy_ResolvesFailed_NeverDestroyed proves UBI-44's own
// universal post-destroy read-back end to end, through the real tfplugin
// wire protocol (provider/internal/fakeprovider's new "lying-destroy" mode),
// not just core/executor's own in-process fakeApplier: a destroy whose
// ApplyResourceChange reports a clean success -- no error, no diagnostics,
// the byte-for-byte shape a real destroy produces -- while the resource
// stays reachable on the next ReadResource must never resolve "destroyed".
// This is the exact shape found live against a real google_pubsub_topic
// (see docs/reliability-report.md).
//
// Deliberately gated behind UBX_TEST_SLOW=1, skipped by default: the
// destroy-reconcile retry budget this path exercises
// (core/executor's own destroyReconcileBackoffSchedule, unexported and not
// overridable from this package) is real production sizing -- ~64 seconds,
// sized to outlast AWS's own documented SQS deletion-visibility lag
// (docs/executor.md's UBI-42 amendment) -- so this test genuinely takes
// about a minute to run. core/executor's own hermetic suite
// (TestShipDestroy_LyingApplySuccess_NeverResolvesDestroyed and siblings,
// core/executor/destroys_test.go) is the fast, always-run regression guard
// for the same logic, with a shrunk schedule; this test's own job is
// proving the identical behavior survives the real wire protocol, not
// re-proving the retry logic itself.
func TestShip_LyingDestroy_ResolvesFailed_NeverDestroyed(t *testing.T) {
	if os.Getenv("UBX_TEST_SLOW") == "" {
		t.Skip("set UBX_TEST_SLOW=1 to run this real-wire-protocol test -- it takes about a minute (core/executor's own destroyReconcileBackoffSchedule is real production sizing, ~64s, and isn't overridable from this package)")
	}

	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	addr := "payments.fake_widget.lying-widget"

	adoptOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "lying-widget",
		"--lookup", `{"id":"lying-widget"}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	if exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v\noutput: %s", err, adoptOut)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	intentPath := filepath.Join(ledgerDir, "destroy-intent.json")
	writeFile(t, intentPath, `{
		"schema_version": 1,
		"kind": "ubx:intent/v1",
		"stack": "payments",
		"intent": {"summary": "UBI-44: destroy a resource a lying provider will refuse to actually delete"},
		"resources": [],
		"destroys": ["`+addr+`"]
	}`)
	resolvedPath := filepath.Join(ledgerDir, "destroy-resolved.json")
	if _, err := runUbx(t, env, "resolve", intentPath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath); err != nil {
		t.Fatalf("ubx resolve: %v", err)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir, "--confirm-destroys")
	if err != nil {
		t.Fatalf("ubx accept --confirm-destroys: %v\noutput: %s", err, acceptOut)
	}
	destroyID := mustExtractID(t, acceptOut)

	// The lying provider: ship's own ApplyResourceChange call reports a
	// clean success, but FAKEPROVIDER_MODE's own destroyedIDs map is never
	// marked, so the resource stays reachable on the next ReadResource --
	// exactly the real google_pubsub_topic shape.
	shipEnv := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_APPLY_MODE=lying-destroy"}
	shipOut, err := runUbx(t, shipEnv, "ship", destroyID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if exitCode(err) != 1 {
		t.Fatalf("ubx ship against a lying provider: want exit 1 (an outcome other than fully applied), got %v\noutput: %s", err, shipOut)
	}
	if strings.Contains(shipOut, "destroyed") {
		t.Fatalf("ubx ship must never report this destroyed -- the provider's own claimed success was never real: %s", shipOut)
	}
	if !strings.Contains(shipOut, "0 shipped") || !strings.Contains(shipOut, "1 failed") {
		t.Fatalf("expected the summary to show 0 shipped, 1 failed, got: %s", shipOut)
	}

	whyOut, err := runUbx(t, env, "why", destroyID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", destroyID, err, whyOut)
	}
	if !strings.Contains(whyOut, "the provider reported a successful destroy") {
		t.Fatalf("expected ubx why to surface the honest provider_reported_success_but_present detail, got: %s", whyOut)
	}
}
