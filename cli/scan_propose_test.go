package cli

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mustExtractShipHash pulls the FIRST hash named on scan --propose's own
// "next: ubx ship <hash>" line -- the same handoff a human would copy,
// used here to reach into the plan store the card's own hash names.
var shipHashPattern = regexp.MustCompile(`next: ubx ship ([0-9a-f]+)`)

func mustExtractShipHash(t *testing.T, out string) string {
	t.Helper()
	m := shipHashPattern.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no \"next: ubx ship <hash>\" line found in: %s", out)
	}
	return m[1]
}

// adoptThenDrift runs scan (adopt) + accept, then scan again with a
// different lookup to produce a drifted outcome -- the common setup every
// --propose test in this file starts from. Returns the ledger dir and the
// stdout of the second (drifted) scan, run with the given extra --propose
// (and any other) args.
func adoptThenDrift(t *testing.T, extraArgs ...string) (ledgerDir, scanOut string) {
	t.Helper()
	ledgerDir = t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	_, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-propose",
		"--lookup", `{"name":"widget-propose","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	requireExitCode(t, err, 1, "")
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	args := append([]string{
		"scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-propose",
		"--lookup", `{"name":"widget-propose","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
	}, extraArgs...)
	scanOut, err = runUbx(t, env, args...)
	requireExitCode(t, err, 1, scanOut)
	return ledgerDir, scanOut
}

// cardHeaderPattern matches renderScanCard's own per-proposal header line
// (UBI-49 residual round 1's "scan cards regain content" polish
// replaced the old "+N create(s) ~N modify(ies) -N destroy(s)" summary
// with kind-specific descriptive text -- "record-only · ..."/"restores
// the ledger's own recorded state" -- so counting cards now matches the
// one thing every card header still has in common: two leading spaces,
// a left-padded kind name, then its own hash).
var cardHeaderPattern = regexp.MustCompile(`(?m)^  (adoption|drift_adopt|drift_revert)\s+[0-9a-f]+\s+`)

// countProposalLines counts scan --propose's own card lines (UBI-49
// finding #6) -- one per generated proposal -- standing in for the old
// JSON dump's "generated a %q proposal" count.
func countProposalLines(out string) int {
	return len(cardHeaderPattern.FindAllString(out, -1))
}

func TestScan_ProposeDefault_GeneratesDriftAdoptOnly(t *testing.T) {
	ledgerDir, out := adoptThenDrift(t)
	if countProposalLines(out) != 1 {
		t.Fatalf("expected exactly one generated proposal by default, got: %s", out)
	}
	if !strings.Contains(out, "drift_adopt") {
		t.Fatalf("expected a drift_adopt proposal, got: %s", out)
	}
	if strings.Contains(out, "drift_revert") {
		t.Fatalf("default --propose must not generate a drift_revert, got: %s", out)
	}

	// The card's own hash must be a real, ship-able plan-store entry
	// (finding #6's whole point) -- not just a printed label.
	hash := mustExtractShipHash(t, out)
	if _, _, err := resolvePlanHash(ledgerDir, hash); err != nil {
		t.Fatalf("expected the card's own hash %s to resolve in the plan store: %v", hash, err)
	}
}

func TestScan_ProposeRevert_GeneratesDriftRevertOnly(t *testing.T) {
	ledgerDir, out := adoptThenDrift(t, "--propose", "revert")
	if countProposalLines(out) != 1 {
		t.Fatalf("expected exactly one generated proposal, got: %s", out)
	}
	if !strings.Contains(out, "drift_revert") {
		t.Fatalf("expected a drift_revert proposal, got: %s", out)
	}
	if strings.Contains(out, "drift_adopt") {
		t.Fatalf("--propose revert must not generate a drift_adopt, got: %s", out)
	}

	hash := mustExtractShipHash(t, out)
	_, p, err := resolvePlanHash(ledgerDir, hash)
	if err != nil {
		t.Fatalf("expected the card's own hash %s to resolve in the plan store: %v", hash, err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	saved := string(data)
	// before=observed(drifted)/after=ledger(restored), the reverse of
	// drift_adopt's own convention.
	if !strings.Contains(saved, "staging") {
		t.Fatalf("expected before to carry the observed/drifted value, got: %s", saved)
	}
	if !strings.Contains(saved, "prod") {
		t.Fatalf("expected after to carry the ledger-recorded value, got: %s", saved)
	}
	// Real blast radius -- unlike drift_adopt's all-zero.
	if p.BlastRadius.Modifies != 1 {
		t.Fatalf("expected a real (non-zero) blast_radius.modifies, got: %+v", p.BlastRadius)
	}
}

func TestScan_ProposeBoth_GeneratesBothProposals(t *testing.T) {
	_, out := adoptThenDrift(t, "--propose", "both")
	if countProposalLines(out) != 2 {
		t.Fatalf("expected exactly two generated proposals, got: %s", out)
	}
	if !strings.Contains(out, "drift_adopt") {
		t.Fatalf("expected a drift_adopt proposal, got: %s", out)
	}
	if !strings.Contains(out, "drift_revert") {
		t.Fatalf("expected a drift_revert proposal, got: %s", out)
	}
}

func TestScan_ProposeInvalidValue_Errors(t *testing.T) {
	_, err := adoptThenDriftExpectErr(t, "--propose", "carrier-pigeon")
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), `must be "adopt", "revert", or "both"`) {
		t.Fatalf("expected a clear invalid-value error, got: %v", err)
	}
}

func TestScan_ProposeBoth_WithOutFlag_Errors(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-both-out",
		"--lookup", `{"name":"widget-both-out","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	_, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-both-out",
		"--lookup", `{"name":"widget-both-out","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
		"--propose", "both",
		"--out", filepath.Join(ledgerDir, "drift.json"),
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "--out only supports a single generated proposal") {
		t.Fatalf("expected a clear --out+both error, got: %v", err)
	}
}

func TestScan_ProposeRevert_WithSurfaceAs_Errors(t *testing.T) {
	_, err := adoptThenDriftExpectErr(t, "--propose", "revert", "--surface-as", "issue", "--github-repo", "acme/infra")
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "--surface-as requires --propose adopt") {
		t.Fatalf("expected a clear --surface-as+revert error, got: %v", err)
	}
}

func TestScan_ProposeHasNoEffectOnNewResource(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-new-propose",
		"--lookup", `{"name":"widget-new-propose","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--propose", "revert",
	)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "adoption") {
		t.Fatalf("a never-seen resource must still generate an adoption proposal regardless of --propose, got: %s", out)
	}
	if countProposalLines(out) != 1 {
		t.Fatalf("expected exactly one generated proposal, got: %s", out)
	}
}

// adoptThenDriftExpectErr is like adoptThenDrift but for cases where the
// drifted scan itself is expected to fail (invalid flag combinations) --
// returns the error instead of failing the test on one.
func adoptThenDriftExpectErr(t *testing.T, extraArgs ...string) (scanOut string, err error) {
	t.Helper()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-propose-err",
		"--lookup", `{"name":"widget-propose-err","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	args := append([]string{
		"scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-propose-err",
		"--lookup", `{"name":"widget-propose-err","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
	}, extraArgs...)
	return runUbx(t, env, args...)
}
