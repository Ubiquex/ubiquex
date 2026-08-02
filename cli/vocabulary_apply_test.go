package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestNoApplyVocabulary_AcrossRenderedOutput is UBI-79's own permanent
// regression guard: the ticket's own acceptance bar was "a test that runs
// several commands and asserts apply/applied never appears... catching
// future regressions automatically," not just fixing the specific strings
// this session found. Drives two independent real end-to-end flows against
// the real fakeprovider subprocess -- a create/modify/ship (adopt, drift,
// propose+accept a revert, ship it, status, why, a fleet-scoped scan) and,
// on a SEPARATE resource, a destroy flow (adopt, terminate, ship via the
// new --confirm-terminate alias, why over the fully-destroyed chain) -- and
// asserts none of the banned apply-family words ever appear in any of it.
// Two separate resources, not one chained through both flows: fakeprovider
// is a fresh, stateless subprocess per CLI invocation (its own "live"
// reads are synthesized from the lookup given, never a real store this
// process's own earlier "ship" call actually mutated), so shipping a
// revert against one resource and then reusing that same resource for a
// terminate would hit a real (and here, irrelevant) freshness-check
// failure -- a property of this hermetic setup, not a rendering bug, and
// not what this test exists to catch.
//
// --json output is deliberately never captured here: core.ApplyRecord's
// own wire/schema field values (e.g. `"outcome": "applied"`) are
// explicitly out of scope per UBI-79's own ticket ("wire/schema field
// names in proposal/apply JSON -- do not touch," "this is a DISPLAY-LAYER
// vocabulary sweep only") -- asserting against --json here would be
// testing the wrong layer entirely.
func TestNoApplyVocabulary_AcrossRenderedOutput(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_APPLY_MODE=ok"}

	var rendered strings.Builder
	run := func(args ...string) string {
		t.Helper()
		out, _ := runUbx(t, env, args...)
		rendered.WriteString(out)
		rendered.WriteString("\n")
		return out
	}

	// --- Resource 1: adopt -> drift -> propose+accept a revert -> ship it
	// (the create/modify progress-narration path, UBI-75).
	addr1 := "payments.fake_widget.vocab-check-revert"
	adoptPath := filepath.Join(ledgerDir, "adopt.json")
	adoptOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "vocab-check-revert",
		"--lookup", `{"name":"vocab-check-revert","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
	)
	rendered.WriteString(adoptOut)
	if exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt 1): %v\noutput: %s", err, adoptOut)
	}
	run("accept", adoptPath, "--ledger-dir", ledgerDir)

	revertPath := filepath.Join(ledgerDir, "revert.json")
	driftOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "vocab-check-revert",
		"--lookup", `{"name":"vocab-check-revert","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--out", revertPath,
		"--no-attribution", "--propose", "revert",
	)
	rendered.WriteString(driftOut)
	if exitCode(err) != 1 {
		t.Fatalf("ubx scan (drift, propose revert): %v\noutput: %s", err, driftOut)
	}
	revertOut, err := runUbx(t, env, "accept", revertPath, "--ledger-dir", ledgerDir)
	rendered.WriteString(revertOut)
	if err != nil {
		t.Fatalf("ubx accept (revert): %v\noutput: %s", err, revertOut)
	}
	revertID := mustExtractID(t, revertOut)
	run("ship", revertID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)

	// status, both ledger-only and --drift (UBI-76's closing summary).
	run("status", "--ledger-dir", ledgerDir)
	run("status", "--drift", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)

	// why, both the single-proposal-id view and the resource chain view.
	run("why", revertID, "--ledger-dir", ledgerDir)
	run("why", addr1, "--ledger-dir", ledgerDir)

	// Fleet-scoped scan (scanfleet.go's own closing summary, UBI-76).
	run("scan", "--provider", fakeProviderBinary, "--stack", "payments", "--ledger-dir", ledgerDir, "--no-attribution")

	// --- Resource 2: adopt -> terminate -> ship the destroy via the new
	// --confirm-terminate alias (UBI-77) -- the destroy progress-
	// narration and full-state-receipt path (UBI-75/UBI-78).
	addr2 := "payments.fake_widget.vocab-check-destroy"
	adopt2Path := filepath.Join(ledgerDir, "adopt2.json")
	adopt2Out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "vocab-check-destroy",
		"--lookup", `{"id":"vocab-check-destroy"}`,
		"--ledger-dir", ledgerDir,
		"--out", adopt2Path,
	)
	rendered.WriteString(adopt2Out)
	if exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt 2): %v\noutput: %s", err, adopt2Out)
	}
	run("accept", adopt2Path, "--ledger-dir", ledgerDir)

	termOut := run("terminate", addr2, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	destroyHash := mustExtractPlanHash(t, ledgerDir, termOut)
	shipDestroyOut, err := runUbx(t, env, "ship", destroyHash, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--confirm-terminate", "--yes")
	rendered.WriteString(shipDestroyOut)
	if err != nil {
		t.Fatalf("ubx ship (destroy via --confirm-terminate): %v\noutput: %s", err, shipDestroyOut)
	}

	// why one more time, now over the fully-destroyed chain.
	run("why", addr2, "--ledger-dir", ledgerDir)

	out := strings.ToLower(rendered.String())
	// "applied" is NOT a substring of "apply" (the past tense drops the
	// "y" for "-ied," not appended to it) -- each banned form is checked
	// explicitly rather than assuming one prefix check catches the whole
	// word family, which would silently miss "applied" specifically, the
	// single most common form this whole ticket exists to eliminate.
	for _, banned := range []string{"apply", "applied", "applies", "applying", "application"} {
		if strings.Contains(out, banned) {
			t.Fatalf("UBI-79: found banned word %q in rendered stdout:\n%s", banned, rendered.String())
		}
	}
}
