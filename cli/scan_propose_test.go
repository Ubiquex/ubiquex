package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

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

func TestScan_ProposeDefault_GeneratesDriftAdoptOnly(t *testing.T) {
	_, out := adoptThenDrift(t)
	if strings.Count(out, "generated a") != 1 {
		t.Fatalf("expected exactly one generated proposal by default, got: %s", out)
	}
	if !strings.Contains(out, `"drift_adopt"`) {
		t.Fatalf("expected a drift_adopt proposal, got: %s", out)
	}
	if strings.Contains(out, `"drift_revert"`) {
		t.Fatalf("default --propose must not generate a drift_revert, got: %s", out)
	}
}

func TestScan_ProposeRevert_GeneratesDriftRevertOnly(t *testing.T) {
	_, out := adoptThenDrift(t, "--propose", "revert")
	if strings.Count(out, "generated a") != 1 {
		t.Fatalf("expected exactly one generated proposal, got: %s", out)
	}
	if !strings.Contains(out, `"drift_revert"`) {
		t.Fatalf("expected a drift_revert proposal, got: %s", out)
	}
	if strings.Contains(out, `"drift_adopt"`) {
		t.Fatalf("--propose revert must not generate a drift_adopt, got: %s", out)
	}
	// before=observed(drifted)/after=ledger(restored), the reverse of
	// drift_adopt's own convention.
	if !strings.Contains(out, `"before": {`) || !strings.Contains(out, `"staging"`) {
		t.Fatalf("expected before to carry the observed/drifted value, got: %s", out)
	}
	if !strings.Contains(out, `"prod"`) {
		t.Fatalf("expected after to carry the ledger-recorded value, got: %s", out)
	}
	// Real blast radius -- unlike drift_adopt's all-zero.
	if !strings.Contains(out, `"modifies": 1`) {
		t.Fatalf("expected a real (non-zero) blast_radius.modifies, got: %s", out)
	}
}

func TestScan_ProposeBoth_GeneratesBothProposals(t *testing.T) {
	_, out := adoptThenDrift(t, "--propose", "both")
	if strings.Count(out, "generated a") != 2 {
		t.Fatalf("expected exactly two generated proposals, got: %s", out)
	}
	if !strings.Contains(out, `"drift_adopt"`) {
		t.Fatalf("expected a drift_adopt proposal, got: %s", out)
	}
	if !strings.Contains(out, `"drift_revert"`) {
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
	if !strings.Contains(out, `"adoption"`) {
		t.Fatalf("a never-seen resource must still generate an adoption proposal regardless of --propose, got: %s", out)
	}
	if strings.Count(out, "generated a") != 1 {
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
