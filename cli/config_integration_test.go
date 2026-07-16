package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// setUpConfig writes a .ubx/config with the given content into a fresh
// directory and points configSearchStartDir at it for the calling test.
func setUpConfig(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	writeConfig(t, dir, content)
	withConfigSearchDir(t, dir)
}

func TestScan_HonorsConfigStack(t *testing.T) {
	setUpConfig(t, `stack = "payments"`)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--type", "fake_widget",
		"--name", "config-stack-widget",
		"--lookup", `{"name":"config-stack-widget"}`,
		"--ledger-dir", t.TempDir(),
		"--no-attribution",
	)
	if err != nil {
		t.Fatalf("ubx scan (no --stack, config has one): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "payments.fake_widget.config-stack-widget") {
		t.Fatalf("expected the config-supplied stack applied, got: %s", out)
	}
}

func TestScan_CLIStackOverridesConfig(t *testing.T) {
	setUpConfig(t, `stack = "config-stack"`)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan",
		"--stack", "cli-stack",
		"--provider", fakeProviderBinary,
		"--type", "fake_widget",
		"--name", "override-widget",
		"--lookup", `{"name":"override-widget"}`,
		"--ledger-dir", t.TempDir(),
		"--no-attribution",
	)
	if err != nil {
		t.Fatalf("ubx scan: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "cli-stack.fake_widget.override-widget") {
		t.Fatalf("expected the explicit --stack to win over config, got: %s", out)
	}
	if strings.Contains(out, "config-stack.") {
		t.Fatalf("config's stack must not have been used, got: %s", out)
	}
}

func TestScan_MissingStackErrorsWhenNeitherCLINorConfigSupplyOne(t *testing.T) {
	setUpConfig(t, `github_repo = "acme/infra"`) // present, but no stack key
	_, err := runUbx(t, nil, "scan",
		"--provider", fakeProviderBinary,
		"--type", "fake_widget",
		"--name", "no-stack-widget",
		"--ledger-dir", t.TempDir(),
	)
	if err == nil {
		t.Fatal("expected an error: no --stack given and config has none either")
	}
	if !strings.Contains(err.Error(), "scan requires --stack") {
		t.Fatalf("expected the usual required-flags error, got: %v", err)
	}
}

func TestScan_HonorsConfigProviderPath(t *testing.T) {
	setUpConfig(t, `
[provider]
path = "`+fakeProviderBinary+`"
`)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan",
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "config-provider-widget",
		"--lookup", `{"name":"config-provider-widget"}`,
		"--ledger-dir", t.TempDir(),
		"--no-attribution",
	)
	if err != nil {
		t.Fatalf("ubx scan (no --provider, config supplies path): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "payments.fake_widget.config-provider-widget") {
		t.Fatalf("expected the scan to succeed using config's provider path, got: %s", out)
	}
}

func TestStatus_DoesNotApplyConfigStackDefault(t *testing.T) {
	setUpConfig(t, `stack = "payments"`)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// Adopt a resource in a DIFFERENT stack than config's default.
	adoptPath := filepath.Join(t.TempDir(), "adopt.json")
	if _, err := runUbx(t, env, "scan",
		"--stack", "network",
		"--provider", fakeProviderBinary,
		"--type", "fake_widget",
		"--name", "vpc",
		"--lookup", `{"name":"vpc"}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
	); err != nil {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept: %v", err)
	}

	out, err := runUbx(t, nil, "status", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx status: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "network.fake_widget.vpc") {
		t.Fatalf("expected the network-stack resource still reported despite config's stack=payments default "+
			"(status deliberately ignores it -- see docs/architecture.md), got: %s", out)
	}
}

func TestWriteback_HonorsConfigTFDir(t *testing.T) {
	tfDir := t.TempDir()
	writeFile(t, filepath.Join(tfDir, "compute.tf"), writebackTF)
	setUpConfig(t, `tf_dir = "`+tfDir+`"`)

	ledgerDir := t.TempDir()
	id := acceptWritebackProposal(t, ledgerDir)

	out, err := runUbx(t, nil, "writeback", id, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx writeback (no --tf-dir, config supplies one): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "applied: instance_type") {
		t.Fatalf("expected writeback to have found and used config's tf_dir, got: %s", out)
	}
}

func TestRevertPlan_HonorsConfigTFDir(t *testing.T) {
	tfDir := t.TempDir()
	writeFile(t, filepath.Join(tfDir, "compute.tf"), revertPlanTF)
	setUpConfig(t, `tf_dir = "`+tfDir+`"`)

	ledgerDir := t.TempDir()
	id := acceptRevertPlanProposal(t, ledgerDir)

	out, err := runUbx(t, nil, "revert-plan", id, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx revert-plan (no --tf-dir, config supplies one): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "--- .tf diff ---") {
		t.Fatalf("expected revert-plan to have found and used config's tf_dir, got: %s", out)
	}
}

func TestRevertPlan_CLITFDirOverridesConfig(t *testing.T) {
	configTFDir := t.TempDir() // deliberately left empty -- must not be used
	setUpConfig(t, `tf_dir = "`+configTFDir+`"`)

	cliTFDir := t.TempDir()
	writeFile(t, filepath.Join(cliTFDir, "compute.tf"), revertPlanTF)

	ledgerDir := t.TempDir()
	id := acceptRevertPlanProposal(t, ledgerDir)

	out, err := runUbx(t, nil, "revert-plan", id, "--ledger-dir", ledgerDir, "--tf-dir", cliTFDir)
	if err != nil {
		t.Fatalf("ubx revert-plan: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "--- .tf diff ---") {
		t.Fatalf("expected the explicit --tf-dir to be used over config's, got: %s", out)
	}
}

func TestLoadConfig_UnknownKeyWarningReachesStderr(t *testing.T) {
	setUpConfig(t, `
stack = "payments"
oops_a_typo = true
`)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--type", "fake_widget",
		"--name", "warn-widget",
		"--lookup", `{"name":"warn-widget"}`,
		"--ledger-dir", t.TempDir(),
		"--no-attribution",
	)
	if err != nil {
		t.Fatalf("ubx scan: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "oops_a_typo") {
		t.Fatalf("expected the unknown-key warning surfaced (runUbx merges stderr into the same buffer), got: %s", out)
	}
}
