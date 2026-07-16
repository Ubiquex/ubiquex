package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core"
)

func adoptViaCLI(t *testing.T, ledgerDir, stack, resourceType, name, lookup string, env []string) {
	t.Helper()
	adoptPath := filepath.Join(t.TempDir(), name+".json")
	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", stack,
		"--type", resourceType,
		"--name", name,
		"--lookup", lookup,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
		"--no-attribution",
	)
	requireExitCode(t, err, 1, scanOut)
	if _, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt %s): %v", name, err)
	}
}

func TestStatus_LedgerOnly_EmptyLedger(t *testing.T) {
	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "status", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx status: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "0 resource(s) (ledger-only") {
		t.Fatalf("expected a 0-resource ledger-only summary, got: %s", out)
	}
}

func TestStatus_LedgerOnly_ListsAdoptedResources(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-status-1", `{"name":"widget-status-1","tags":{"env":"prod"}}`, env)
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-status-2", `{"name":"widget-status-2","tags":{"env":"prod"}}`, env)

	out, err := runUbx(t, nil, "status", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx status: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "payments.fake_widget.widget-status-1: adoption") {
		t.Fatalf("expected widget-status-1 listed, got: %s", out)
	}
	if !strings.Contains(out, "payments.fake_widget.widget-status-2: adoption") {
		t.Fatalf("expected widget-status-2 listed, got: %s", out)
	}
	if !strings.Contains(out, "2 resource(s) (ledger-only") {
		t.Fatalf("expected a 2-resource ledger-only summary, got: %s", out)
	}
	// No provider was launched -- no --provider flag was even given.
}

func TestStatus_MultiStack_FilterByStack(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-multi-a", `{"name":"widget-multi-a","tags":{"env":"prod"}}`, env)
	adoptViaCLI(t, ledgerDir, "network", "fake_widget", "widget-multi-b", `{"name":"widget-multi-b","tags":{"env":"prod"}}`, env)

	allOut, err := runUbx(t, nil, "status", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx status: %v", err)
	}
	if !strings.Contains(allOut, "payments.fake_widget.widget-multi-a") || !strings.Contains(allOut, "network.fake_widget.widget-multi-b") {
		t.Fatalf("expected both stacks' resources listed by default, got: %s", allOut)
	}
	if !strings.Contains(allOut, "2 resource(s)") {
		t.Fatalf("expected a 2-resource summary, got: %s", allOut)
	}

	filteredOut, err := runUbx(t, nil, "status", "--ledger-dir", ledgerDir, "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx status --stack payments: %v", err)
	}
	if !strings.Contains(filteredOut, "payments.fake_widget.widget-multi-a") {
		t.Fatalf("expected the payments resource listed, got: %s", filteredOut)
	}
	if strings.Contains(filteredOut, "network.fake_widget.widget-multi-b") {
		t.Fatalf("--stack payments must not list the network resource, got: %s", filteredOut)
	}
	if !strings.Contains(filteredOut, "1 resource(s)") {
		t.Fatalf("expected a 1-resource summary, got: %s", filteredOut)
	}
}

func TestStatus_Drift_AllClean_ExitZero(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-clean", `{"name":"widget-clean","tags":{"env":"prod"}}`, env)

	out, err := runUbx(t, env, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx status --drift: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "clean: payments.fake_widget.widget-clean") {
		t.Fatalf("expected a clean classification, got: %s", out)
	}
	if !strings.Contains(out, "1 resource(s), 0 drifted, 0 unreadable") {
		t.Fatalf("expected an all-clean summary, got: %s", out)
	}
}

func TestStatus_Drift_SomeDrifted_ExitOne(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-drifted", `{"name":"widget-drifted","tags":{"env":"prod"}}`, adoptEnv)

	// Simulate an out-of-band mutation: the live read now carries an extra
	// tag regardless of the recorded lookup (same mechanism
	// TestAccept_ReverifyBlocksStaleAcceptance uses).
	driftEnv := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=mutated=yes"}
	out, err := runUbx(t, driftEnv, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary)
	if err == nil {
		t.Fatal("expected a non-nil error (exit code 1) when a resource has drifted")
	}
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected an *ExitCodeError, got: %T (%v)", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("Code = %d, want 1", exitErr.Code)
	}
	if !strings.Contains(out, "drifted: payments.fake_widget.widget-drifted") {
		t.Fatalf("expected a drifted classification, got: %s", out)
	}
	if !strings.Contains(out, "1 resource(s), 1 drifted, 0 unreadable") {
		t.Fatalf("expected a 1-drifted summary, got: %s", out)
	}
}

func TestStatus_Drift_MissingLookup_Unreadable_ExitTwo(t *testing.T) {
	ledgerDir := t.TempDir()
	ledger := core.Open(ledgerDir)

	// A proposal recorded before the resolution.inputs lookup amendment
	// existed -- no Lookup field at all.
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		ID:            "0000000000000000000000000000000000000000000000000000000000000000",
		Stack:         "payments",
		Kind:          core.KindAdoption,
		Intent:        core.Intent{Summary: "x"},
		Resolution: core.Resolution{
			ResolvedAt: "2026-07-16T00:00:00Z",
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: "payments.fake_widget.widget-no-lookup", ObservedHash: "deadbeef"},
			},
		},
		Acceptance: &core.Acceptance{Method: "local", Approvers: []string{"roozbeh"}, AcceptedAt: "2026-07-16T00:00:00Z"},
		Status:     core.StatusAccepted,
	}
	if err := ledger.Append(p); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary)
	if err == nil {
		t.Fatal("expected a non-nil error (exit code 2) for an unreadable resource")
	}
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected an *ExitCodeError, got: %T (%v)", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("Code = %d, want 2", exitErr.Code)
	}
	if !strings.Contains(out, "unreadable: payments.fake_widget.widget-no-lookup") || !strings.Contains(out, "no lookup key recorded") {
		t.Fatalf("expected an unreadable/no-lookup classification, got: %s", out)
	}
	if !strings.Contains(out, "1 resource(s), 0 drifted, 1 unreadable") {
		t.Fatalf("expected a 1-unreadable summary, got: %s", out)
	}
}

func TestStatus_Drift_ProviderFailureMidFleet_ContinuesWalk(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-ok", `{"name":"widget-ok","tags":{"env":"prod"}}`, env)

	// A second resource of a type the fake provider doesn't serve at all
	// -- core.ErrUnknownResourceType, a real per-resource provider failure,
	// not a hand-crafted one.
	ledger := core.Open(ledgerDir)
	unknownAddr := core.Address{Stack: "payments", Type: "aws_totally_made_up", Name: "widget-unknown-type"}
	unknownProposal := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		ID:            "1111111111111111111111111111111111111111111111111111111111111111",
		Stack:         "payments",
		Parent:        mustHead(t, ledger),
		Kind:          core.KindAdoption,
		Intent:        core.Intent{Summary: "x"},
		Resolution: core.Resolution{
			ResolvedAt: "2026-07-16T00:00:00Z",
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: unknownAddr.String(), ObservedHash: "deadbeef", Lookup: []byte(`{"id":"x"}`)},
			},
		},
		Acceptance: &core.Acceptance{Method: "local", Approvers: []string{"roozbeh"}, AcceptedAt: "2026-07-16T00:00:00Z"},
		Status:     core.StatusAccepted,
	}
	if err := ledger.Append(unknownProposal); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, env, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary)
	if err == nil {
		t.Fatal("expected a non-nil error (exit code 2) -- one resource is unreadable")
	}
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got: %v", err)
	}
	// The walk must have continued past the failing resource -- both
	// appear in the report, not just the one before the failure.
	if !strings.Contains(out, "clean: payments.fake_widget.widget-ok") {
		t.Fatalf("expected the good resource still reported clean, got: %s", out)
	}
	if !strings.Contains(out, "unreadable: payments.aws_totally_made_up.widget-unknown-type") {
		t.Fatalf("expected the bad resource reported unreadable, got: %s", out)
	}
	if !strings.Contains(out, "unknown resource type") {
		t.Fatalf("expected the underlying error surfaced, got: %s", out)
	}
	if !strings.Contains(out, "2 resource(s), 0 drifted, 1 unreadable") {
		t.Fatalf("expected a mixed clean/unreadable summary, got: %s", out)
	}
}

// TestStatus_MixedDriftedAndUnreadable_SummaryCorrectness covers a single
// walk with more than one non-clean classification at once (clean is
// already covered in isolation by TestStatus_Drift_AllClean_ExitZero; every
// fake_widget resource in one launched provider process shares
// FAKEPROVIDER_EXTRA_TAG's effect, so a clean+drifted split within the same
// invocation isn't reachable with this fixture -- drifted+unreadable
// together already exercises the summary math and exit-code precedence
// that matters).
func TestStatus_MixedDriftedAndUnreadable_SummaryCorrectness(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-mix-drifted", `{"name":"widget-mix-drifted","tags":{"env":"prod"}}`, adoptEnv)

	// A second resource with no recorded lookup at all -- unreadable,
	// independent of the fake provider's own behavior.
	ledger := core.Open(ledgerDir)
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		ID:            "2222222222222222222222222222222222222222222222222222222222222222",
		Stack:         "payments",
		Parent:        mustHead(t, ledger),
		Kind:          core.KindAdoption,
		Intent:        core.Intent{Summary: "x"},
		Resolution: core.Resolution{
			ResolvedAt: "2026-07-16T00:00:00Z",
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: "payments.fake_widget.widget-mix-nolookup", ObservedHash: "deadbeef"},
			},
		},
		Acceptance: &core.Acceptance{Method: "local", Approvers: []string{"roozbeh"}, AcceptedAt: "2026-07-16T00:00:00Z"},
		Status:     core.StatusAccepted,
	}
	if err := ledger.Append(p); err != nil {
		t.Fatal(err)
	}

	driftEnv := []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=mutated=yes"}
	out, err := runUbx(t, driftEnv, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary)
	if err == nil {
		t.Fatal("expected a non-nil error (exit code 2, unreadable wins over drifted)")
	}
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2 (unreadable outranks drifted), got: %v", err)
	}
	if !strings.Contains(out, "drifted: payments.fake_widget.widget-mix-drifted") {
		t.Fatalf("expected widget-mix-drifted reported drifted, got: %s", out)
	}
	if !strings.Contains(out, "unreadable: payments.fake_widget.widget-mix-nolookup") {
		t.Fatalf("expected widget-mix-nolookup reported unreadable, got: %s", out)
	}
	if !strings.Contains(out, "2 resource(s), 1 drifted, 1 unreadable") {
		t.Fatalf("expected a correct 1-drifted/1-unreadable summary, got: %s", out)
	}
}

func TestStatus_ProviderLaunchFailure_ExitTwo(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-launch-fail", `{"name":"widget-launch-fail","tags":{"env":"prod"}}`, []string{"FAKEPROVIDER_MODE=ok-v6"})

	// FAKEPROVIDER_MODE explicitly cleared this time (t.Setenv from the
	// adopt call above otherwise persists for the rest of this test) --
	// the provider process itself fails to complete its handshake, a
	// whole-command failure, not a per-resource one.
	_, err := runUbx(t, []string{"FAKEPROVIDER_MODE="}, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary)
	if err == nil {
		t.Fatal("expected an error when the provider itself fails to launch")
	}
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected an *ExitCodeError, got: %T (%v)", err, err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("Code = %d, want 2", exitErr.Code)
	}
	if exitErr.Err == nil {
		t.Fatal("expected a wrapped error describing the launch failure")
	}
}

func mustHead(t *testing.T, l *core.Ledger) string {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	return head
}
