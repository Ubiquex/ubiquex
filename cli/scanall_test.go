package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeState(t *testing.T, resourcesJSON string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "onboard.tfstate")
	writeFile(t, path, `{"version": 4, "resources": [`+resourcesJSON+`]}`)
	return path
}

func TestScanAll_Basic(t *testing.T) {
	statePath := writeState(t, `
		{"mode": "managed", "type": "fake_widget", "name": "api", "instances": [{"attributes": {"id": "api"}}]},
		{"mode": "managed", "type": "fake_widget", "name": "worker", "instances": [
			{"index_key": 0, "attributes": {"id": "worker-0"}},
			{"index_key": 1, "attributes": {"id": "worker-1"}}
		]},
		{"mode": "data", "type": "aws_ami", "name": "ignored", "instances": [{"attributes": {"id": "ami-1"}}]}
	`)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx scan --all: %v\noutput: %s", err, out)
	}

	stack := strings.TrimSuffix(filepath.Base(statePath), filepath.Ext(statePath))
	if !strings.Contains(out, stack+".fake_widget.api") {
		t.Fatalf("expected the api resource adopted (stack %q), got: %s", stack, out)
	}
	if !strings.Contains(out, stack+".fake_widget.worker[0]") || !strings.Contains(out, stack+".fake_widget.worker[1]") {
		t.Fatalf("expected both count instances adopted, got: %s", out)
	}
	if strings.Contains(out, "aws_ami") {
		t.Fatalf("data source must be ignored entirely, got: %s", out)
	}
	if !strings.Contains(out, "3 adopted, 0 skipped") {
		t.Fatalf("expected a 3-adopted summary, got: %s", out)
	}
}

// TestScanAll_AllGeneratedProposalsAcceptInSequence guards against a real
// bug caught by this session's own live-verification test: every proposal
// core.GenerateProposal produces gets its Parent from the ledger's actual
// on-disk head, which never moves during one --all walk (nothing gets
// accepted until later, separately) -- so, left uncorrected, all N
// generated proposals would carry the exact same stale parent, and only
// the first ever accepted would succeed. Every proposal from one --all
// batch must accept cleanly, in the order --all printed them.
func TestScanAll_AllGeneratedProposalsAcceptInSequence(t *testing.T) {
	statePath := writeState(t, `
		{"mode": "managed", "type": "fake_widget", "name": "a", "instances": [{"attributes": {"id": "a"}}]},
		{"mode": "managed", "type": "fake_widget", "name": "b", "instances": [{"attributes": {"id": "b"}}]},
		{"mode": "managed", "type": "fake_widget", "name": "c", "instances": [{"attributes": {"id": "c"}}]}
	`)
	ledgerDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "proposals")
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out-dir", outDir)
	if err != nil {
		t.Fatalf("ubx scan --all: %v\noutput: %s", err, out)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read out-dir: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d proposal files, want 3", len(entries))
	}
	for _, e := range entries {
		if _, err := runUbx(t, env, "accept", filepath.Join(outDir, e.Name()), "--ledger-dir", ledgerDir); err != nil {
			t.Fatalf("ubx accept %s: %v -- every proposal from one --all batch must accept in sequence", e.Name(), err)
		}
	}
}

func TestScanAll_OutDirWritesOneFilePerResource(t *testing.T) {
	statePath := writeState(t, `
		{"mode": "managed", "type": "fake_widget", "name": "api", "instances": [{"attributes": {"id": "api"}}]}
	`)
	ledgerDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "proposals")
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out-dir", outDir)
	if err != nil {
		t.Fatalf("ubx scan --all: %v\noutput: %s", err, out)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("read out-dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d files in --out-dir, want 1", len(entries))
	}
	b, err := os.ReadFile(filepath.Join(outDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"kind": "adoption"`) {
		t.Fatalf("expected an adoption proposal in the written file, got: %s", b)
	}
}

func TestScanAll_StackFlagOverridesFilenameDefault(t *testing.T) {
	statePath := writeState(t, `
		{"mode": "managed", "type": "fake_widget", "name": "api", "instances": [{"attributes": {"id": "api"}}]}
	`)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath, "--stack", "payments",
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx scan --all: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "payments.fake_widget.api") {
		t.Fatalf("expected --stack to override the filename-derived default, got: %s", out)
	}
}

func TestScanAll_AlreadyKnownToLedgerSkipped(t *testing.T) {
	statePath := writeState(t, `
		{"mode": "managed", "type": "fake_widget", "name": "api", "instances": [{"attributes": {"id": "api"}}]}
	`)
	ledgerDir := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "proposals")
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out-dir", outDir)
	if err != nil {
		t.Fatalf("ubx scan --all (1st): %v\noutput: %s", err, out)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one proposal written, got %v (err=%v)", entries, err)
	}
	if _, err := runUbx(t, env, "accept", filepath.Join(outDir, entries[0].Name()), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept: %v", err)
	}

	out2, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 1, out2)
	if !strings.Contains(out2, "already known to the ledger") {
		t.Fatalf("expected the already-accepted resource skipped, got: %s", out2)
	}
	if !strings.Contains(out2, "0 adopted, 1 skipped") {
		t.Fatalf("expected a 0-adopted/1-skipped summary, got: %s", out2)
	}
}

func TestScanAll_UnknownTypeSkipped_WalkContinues(t *testing.T) {
	statePath := writeState(t, `
		{"mode": "managed", "type": "aws_totally_made_up", "name": "bad", "instances": [{"attributes": {"id": "x"}}]},
		{"mode": "managed", "type": "fake_widget", "name": "good", "instances": [{"attributes": {"id": "good"}}]}
	`)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "unknown resource type") {
		t.Fatalf("expected the bad-type resource skipped with a reason, got: %s", out)
	}
	if !strings.Contains(out, ".fake_widget.good") {
		t.Fatalf("expected the good resource still adopted -- the walk must continue, got: %s", out)
	}
	if !strings.Contains(out, "1 adopted, 1 skipped") {
		t.Fatalf("expected a 1-adopted/1-skipped summary, got: %s", out)
	}
}

func TestScanAll_MissingIdentitySkipped(t *testing.T) {
	statePath := writeState(t, `
		{"mode": "managed", "type": "fake_widget", "name": "no-id", "instances": [{"attributes": {"name": "no-id"}}]}
	`)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "no id attribute") {
		t.Fatalf("expected a missing-identity skip reason, got: %s", out)
	}
	if !strings.Contains(out, "0 adopted, 1 skipped") {
		t.Fatalf("expected a 0-adopted/1-skipped summary, got: %s", out)
	}
}

func TestScanAll_ModulePathFoldedIntoNameAndSummary(t *testing.T) {
	statePath := writeState(t, `
		{"mode": "managed", "type": "fake_widget", "name": "web", "instances": [{"attributes": {"id": "root"}}]},
		{"mode": "managed", "type": "fake_widget", "name": "web", "module": "module.network", "instances": [{"attributes": {"id": "nested"}}]}
	`)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx scan --all: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, ".fake_widget.web\n") && !strings.Contains(out, ".fake_widget.web ") {
		t.Fatalf("expected the root resource's plain address, got: %s", out)
	}
	if !strings.Contains(out, ".fake_widget.network.web") {
		t.Fatalf("expected the module resource's disambiguated address, got: %s", out)
	}
	if !strings.Contains(out, "(from module network)") {
		t.Fatalf("expected the module hint in intent.summary, got: %s", out)
	}
	if !strings.Contains(out, "2 adopted, 0 skipped") {
		t.Fatalf("expected both resources adopted without collision, got: %s", out)
	}
}

func TestScanAll_DuplicateAddressSkipped(t *testing.T) {
	// Two separate resource blocks that, after module/index folding,
	// resolve to the exact same ubx address -- something a hand-edited or
	// otherwise malformed state file could produce, never a real
	// terraform-generated one.
	statePath := writeState(t, `
		{"mode": "managed", "type": "fake_widget", "name": "web", "instances": [{"attributes": {"id": "first"}}]},
		{"mode": "managed", "type": "fake_widget", "name": "web", "instances": [{"attributes": {"id": "second"}}]}
	`)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	out, err := runUbx(t, env, "scan", "--all", "--tfstate", statePath,
		"--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "duplicate address") {
		t.Fatalf("expected the second entry skipped as a duplicate address, got: %s", out)
	}
	if !strings.Contains(out, "1 adopted, 1 skipped") {
		t.Fatalf("expected exactly one adopted and one skipped, got: %s", out)
	}
}

func TestScanAll_RequiresTFStateFlag(t *testing.T) {
	_, err := runUbx(t, nil, "scan", "--all", "--ledger-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected an error when --all is given without --tfstate")
	}
	if !strings.Contains(err.Error(), "requires --tfstate") {
		t.Fatalf("expected a clear missing-tfstate error, got: %v", err)
	}
}

func TestScanAll_RejectsSingleResourceFlags(t *testing.T) {
	statePath := writeState(t, `{"mode": "managed", "type": "fake_widget", "name": "api", "instances": [{"attributes": {"id": "api"}}]}`)
	_, err := runUbx(t, nil, "scan", "--all", "--tfstate", statePath, "--type", "fake_widget", "--ledger-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected an error when --all is combined with --type")
	}
	if !strings.Contains(err.Error(), "don't apply to bulk onboarding") {
		t.Fatalf("expected a clear incompatible-flags error, got: %v", err)
	}
}

func TestScanAll_RejectsNonAdoptPropose(t *testing.T) {
	statePath := writeState(t, `{"mode": "managed", "type": "fake_widget", "name": "api", "instances": [{"attributes": {"id": "api"}}]}`)
	_, err := runUbx(t, nil, "scan", "--all", "--tfstate", statePath, "--propose", "revert", "--ledger-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected an error when --all is combined with --propose revert")
	}
	if !strings.Contains(err.Error(), `--propose must be "adopt"`) {
		t.Fatalf("expected a clear propose-must-be-adopt error, got: %v", err)
	}
}

func TestScanAll_SingleResourceModeStillRequiresStackTypeName(t *testing.T) {
	_, err := runUbx(t, nil, "scan", "--ledger-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected an error when neither --all nor --stack are given")
	}
	if !strings.Contains(err.Error(), "scan requires --stack") {
		t.Fatalf("expected the usual required-flags error, got: %v", err)
	}
}

// TestScan_StackKnown_NeitherTypeNorNameGiven_WalksFleet_EmptyLedger is
// TestScanAll_SingleResourceModeStillRequiresStackTypeName's own sibling
// for the OTHER half of the old combined error (UBI-49 residual round 1):
// once --stack IS known, omitting --type/--name is no longer an error at
// all -- it's the fleet-scoped walk's own trigger (cli/scanfleet_test.go
// has its own dedicated tests for a populated fleet; this just proves an
// empty one reports cleanly rather than erroring).
func TestScan_StackKnown_NeitherTypeNorNameGiven_WalksFleet_EmptyLedger(t *testing.T) {
	out, err := runUbx(t, nil, "scan", "--stack", "payments", "--ledger-dir", t.TempDir())
	if err != nil {
		t.Fatalf("expected a fleet-scoped walk (even of an empty ledger), not an error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 resource(s) tracked for stack payments") {
		t.Fatalf("expected an empty-fleet report, got: %s", out)
	}
}

// TestScan_OnlyTypeGiven_StillErrors proves giving exactly one of
// --type/--name (as opposed to neither, the fleet-walk trigger, or both,
// the single-resource path) stays a hard, unambiguous error.
func TestScan_OnlyTypeGiven_StillErrors(t *testing.T) {
	_, err := runUbx(t, nil, "scan", "--stack", "payments", "--type", "fake_widget", "--ledger-dir", t.TempDir())
	if err == nil {
		t.Fatal("expected an error when --type is given without --name")
	}
	if !strings.Contains(err.Error(), "pass both --type and --name") {
		t.Fatalf("expected the both-or-neither error, got: %v", err)
	}
}
