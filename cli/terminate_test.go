package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTerminate_HappyPath_ShipsAndTombstones is UBI-49 finding #7's own
// acceptance test: a tracked address is terminated with no authoring
// document at all -- the address is the whole spec -- saved to the plan
// store like `ubx plan`, then shipped through the standard double
// consent (--confirm-destroys at ship time, exactly like any other
// destroy-carrying plan) into a real tombstone. Mirrors
// TestWhy_RendersDestroyedResource's own adopt-then-destroy setup, but
// through `ubx terminate` instead of a hand-written destroys[] intent
// file + separate `ubx resolve`/`ubx accept`.
func TestTerminate_HappyPath_ShipsAndTombstones(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	addr := "payments.fake_widget.old-widget"

	adoptOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "old-widget",
		"--lookup", `{"id":"old-widget"}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	if exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v\noutput: %s", err, adoptOut)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	termOut, err := runUbx(t, env, "terminate", addr, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx terminate: %v\noutput: %s", err, termOut)
	}
	if !strings.Contains(termOut, "delta: +0 create(s), ~0 modify(ies), -1 destroy(s)") {
		t.Fatalf("expected a 1-destroy receipt, got: %s", termOut)
	}
	hash := mustExtractPlanHash(t, termOut)

	shipOut, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--confirm-destroys", "--yes")
	if err != nil {
		t.Fatalf("ubx ship --confirm-destroys: %v\noutput: %s", err, shipOut)
	}
	destroyID := mustExtractIDFromShip(t, shipOut)

	whyOut, err := runUbx(t, env, "why", destroyID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", destroyID, err, whyOut)
	}
	if !strings.Contains(whyOut, "destroy: "+addr) {
		t.Fatalf("expected a \"destroy: %s\" line, got: %s", addr, whyOut)
	}
	if !strings.Contains(whyOut, "(destroyed)") {
		t.Fatalf("expected the terminal transition annotated \"(destroyed)\", got: %s", whyOut)
	}
}

// TestTerminate_MissingTarget_Refused proves terminate surfaces
// core/resolver/destroys.go's own ErrDestroyTargetMissing for an address
// the ledger has never heard of -- no new validation, the same check a
// hand-written destroys[] intent file already gets via resolver.Resolve.
func TestTerminate_MissingTarget_Refused(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "terminate", "payments.fake_widget.never-existed", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "never recorded") {
		t.Fatalf("expected a missing-destroy-target error, got: %v", err)
	}
}

// TestTerminate_OrphanProtection_Refused proves terminate surfaces
// ErrDestroyOrphaned for a resource another resource still depends on --
// mirroring TestResolveAcceptShip_CreateChain_RealFakeProvider's own
// primary+mirror $ref setup (mirror's config genuinely references
// primary's own name/id), then terminating primary alone without also
// destroying or updating mirror.
func TestTerminate_OrphanProtection_Refused(t *testing.T) {
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
	planOut, err := runUbx(t, env, "plan", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx plan: %v\noutput: %s", err, planOut)
	}
	hash := mustExtractPlanHash(t, planOut)
	if _, err := runUbx(t, env, "ship", hash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--yes"); err != nil {
		t.Fatalf("ubx ship: %v", err)
	}

	out, err := runUbx(t, env, "terminate", "payments.fake_widget.primary", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "still referenced by") {
		t.Fatalf("expected an orphan-protection error, got: %v", err)
	}
}

// TestTerminate_AddressesFromDifferentStacks_Refused is terminate's own
// new validation (not a resolver.Resolve behavior) -- a proposal is
// always single-stack, so mixed-stack addresses are refused before ever
// touching a ledger.
func TestTerminate_AddressesFromDifferentStacks_Refused(t *testing.T) {
	out, err := runUbx(t, nil, "terminate", "payments.fake_widget.a", "staging.fake_widget.b", "--ledger-dir", t.TempDir())
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "different stacks") {
		t.Fatalf("expected a mixed-stack error, got: %v", err)
	}
}

// TestDestroy_TeachesTowardTerminate proves "ubx destroy" is never a
// silent alias -- the founder's own "decide at build" note on finding #7,
// resolved toward a teaching error (docs/cli-output-spec.md principle 6).
func TestDestroy_TeachesTowardTerminate(t *testing.T) {
	out, err := runUbx(t, nil, "destroy", "payments.fake_widget.old-widget")
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "ubx terminate") {
		t.Fatalf("expected the error to name ubx terminate, got: %v", err)
	}
}

// mustExtractIDFromShip pulls the accepted-inline proposal id from ship's
// own "accepted <id> (stack ...) via local plan" line.
func mustExtractIDFromShip(t *testing.T, shipOut string) string {
	t.Helper()
	const marker = "accepted "
	i := strings.Index(shipOut, marker)
	if i < 0 {
		t.Fatalf("no \"accepted <id>\" line found in: %s", shipOut)
	}
	rest := shipOut[i+len(marker):]
	end := strings.IndexAny(rest, " \n")
	if end < 0 {
		t.Fatalf("malformed \"accepted <id>\" line in: %s", shipOut)
	}
	return rest[:end]
}
