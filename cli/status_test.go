package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
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

// TestStatus_Drift_AllClean_ExitZero is UBI-71's own finding: a clean
// resource's own detail line is noise on a mostly (or entirely) clean
// fleet -- hidden by default, collapsed to a trailing "N clean (--all to
// show)" count instead; --all reveals the exact pre-UBI-71 per-resource
// line.
func TestStatus_Drift_AllClean_ExitZero(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-clean", `{"name":"widget-clean","tags":{"env":"prod"}}`, env)

	out, err := runUbx(t, env, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx status --drift: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "clean: payments.fake_widget.widget-clean") {
		t.Fatalf("expected the clean resource's own line hidden by default, got: %s", out)
	}
	if !strings.Contains(out, "1 clean (--all to show)") {
		t.Fatalf("expected a clean-count hint naming --all, got: %s", out)
	}
	if !strings.Contains(out, "1 resource(s), 0 drifted, 0 unreadable") {
		t.Fatalf("expected an all-clean summary, got: %s", out)
	}

	allOut, err := runUbx(t, env, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary, "--all")
	if err != nil {
		t.Fatalf("ubx status --drift --all: %v\noutput: %s", err, allOut)
	}
	if !strings.Contains(allOut, "clean: payments.fake_widget.widget-clean") {
		t.Fatalf("expected --all to show the clean classification, got: %s", allOut)
	}
	if strings.Contains(allOut, "(--all to show)") {
		t.Fatalf("--all must not itself print the hint suggesting --all, got: %s", allOut)
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

	out, err := runUbx(t, env, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary, "--all")
	if err == nil {
		t.Fatal("expected a non-nil error (exit code 2) -- one resource is unreadable")
	}
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("expected exit code 2, got: %v", err)
	}
	// The walk must have continued past the failing resource -- both
	// appear in the report, not just the one before the failure. --all
	// (UBI-71) so the clean resource's own line still renders here.
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

// TestStatus_Drift_CleanHiddenByDefault_UnreadableStillShown is UBI-71's
// own default-mode companion to
// TestStatus_Drift_ProviderFailureMidFleet_ContinuesWalk (which passes
// --all): the exact same mixed clean+unreadable fleet, without --all --
// the clean resource's own line must be gone, replaced by the "N clean
// (--all to show)" hint, while the unreadable resource (never hidden,
// it's actionable) still renders in full.
func TestStatus_Drift_CleanHiddenByDefault_UnreadableStillShown(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-ok2", `{"name":"widget-ok2","tags":{"env":"prod"}}`, env)

	ledger := core.Open(ledgerDir)
	unknownAddr := core.Address{Stack: "payments", Type: "aws_totally_made_up", Name: "widget-unknown-type2"}
	unknownProposal := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		ID:            "4444444444444444444444444444444444444444444444444444444444444444",
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
	if strings.Contains(out, "clean: payments.fake_widget.widget-ok2") {
		t.Fatalf("expected the clean resource's own line hidden by default, got: %s", out)
	}
	if !strings.Contains(out, "1 clean (--all to show)") {
		t.Fatalf("expected a clean-count hint, got: %s", out)
	}
	if !strings.Contains(out, "unreadable: payments.aws_totally_made_up.widget-unknown-type2") {
		t.Fatalf("expected the bad resource still reported unreadable (never hidden), got: %s", out)
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

// TestStatus_TTY_LedgerOnlyLine_AddressBrightMetadataDim is UBI-68's own
// core acceptance test for the ledger-only (non-drift) resource line: on a
// real (forced) terminal, everything after the address's own colon --
// kind, hash, "(accepted <timestamp>)" -- renders as one dim block, while
// the address itself (printed outside forceDim) carries no ANSI code of
// its own at all, reading brightest by contrast.
func TestStatus_TTY_LedgerOnlyLine_AddressBrightMetadataDim(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-dim-1", `{"name":"widget-dim-1","tags":{"env":"prod"}}`, []string{"FAKEPROVIDER_MODE=ok-v6"})

	addr := "payments.fake_widget.widget-dim-1"
	out, err := runUbxTTY(t, "", []string{"NO_COLOR="}, "status", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx status: %v\noutput: %s", err, out)
	}

	line := lineContaining(out, addr+": ")
	if line == "" {
		t.Fatalf("expected a line for %s, got: %q", addr, out)
	}
	prefix := addr + ": "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("expected the address to render with no leading ANSI code (brightest), got line: %q", line)
	}
	rest := strings.TrimPrefix(line, prefix)
	if !strings.HasPrefix(rest, ansiDim) {
		t.Fatalf("expected the metadata after the colon to start dim, got: %q", line)
	}
	if !strings.HasSuffix(rest, ansiReset) {
		t.Fatalf("expected the dimmed metadata to end with a reset, got: %q", line)
	}
	if !strings.Contains(rest, "adoption") || !strings.Contains(rest, "(accepted ") {
		t.Fatalf("expected kind and accepted-timestamp text preserved inside the dimmed segment, got: %q", line)
	}
	// The hash keeps its own blue coloring (st.Hash), reasserted-dim
	// around it rather than losing its color -- confirmed by the presence
	// of both codes together, not just one or the other.
	if !strings.Contains(rest, ansiBlue) {
		t.Fatalf("expected the hash's own blue color preserved inside the dimmed segment, got: %q", line)
	}
}

// TestStatus_TTY_DriftLines_AddressBrightMetadataDim covers both the
// "clean:"/"drifted:" prefixed drift-path lines (classifyFleetEntry) --
// the colored status prefix stays exactly as before, only the metadata
// after the address's own colon gets dimmed.
func TestStatus_TTY_DriftLines_AddressBrightMetadataDim(t *testing.T) {
	ledgerDir := t.TempDir()
	adoptEnv := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "widget-dim-clean", `{"name":"widget-dim-clean","tags":{"env":"prod"}}`, adoptEnv)

	// UBI-71: a clean resource's own line is hidden by default -- --all
	// brings it back so this test can still inspect its dim styling.
	cleanOut, err := runUbxTTY(t, "", []string{"FAKEPROVIDER_MODE=ok-v6", "NO_COLOR="}, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary, "--all")
	if err != nil {
		t.Fatalf("ubx status --drift (clean): %v\noutput: %s", err, cleanOut)
	}
	addr := "payments.fake_widget.widget-dim-clean"
	cleanLine := lineContaining(cleanOut, addr+": ")
	if cleanLine == "" {
		t.Fatalf("expected a clean line for %s, got: %q", addr, cleanOut)
	}
	wantCleanPrefix := ansiGreen + "clean:" + ansiReset + " " + addr + ": "
	if !strings.HasPrefix(cleanLine, wantCleanPrefix) {
		t.Fatalf("expected %q as the undimmed prefix (green status word + bright address), got: %q", wantCleanPrefix, cleanLine)
	}
	cleanRest := strings.TrimPrefix(cleanLine, wantCleanPrefix)
	if !strings.HasPrefix(cleanRest, ansiDim) || !strings.HasSuffix(cleanRest, ansiReset) {
		t.Fatalf("expected the clean line's metadata dimmed end-to-end, got: %q", cleanLine)
	}

	ledgerDir2 := t.TempDir()
	adoptViaCLI(t, ledgerDir2, "payments", "fake_widget", "widget-dim-drifted", `{"name":"widget-dim-drifted","tags":{"env":"prod"}}`, adoptEnv)
	driftOut, err := runUbxTTY(t, "", []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=mutated=yes", "NO_COLOR="}, "status", "--ledger-dir", ledgerDir2, "--drift", "--provider", fakeProviderBinary)
	if err == nil {
		t.Fatal("expected exit code 1 for a drifted resource")
	}
	addr2 := "payments.fake_widget.widget-dim-drifted"
	driftLine := lineContaining(driftOut, addr2+": ")
	if driftLine == "" {
		t.Fatalf("expected a drifted line for %s, got: %q", addr2, driftOut)
	}
	wantDriftPrefix := ansiYellow + "drifted:" + ansiReset + " " + addr2 + ": "
	if !strings.HasPrefix(driftLine, wantDriftPrefix) {
		t.Fatalf("expected %q as the undimmed prefix (yellow status word + bright address), got: %q", wantDriftPrefix, driftLine)
	}
	driftRest := strings.TrimPrefix(driftLine, wantDriftPrefix)
	if !strings.HasPrefix(driftRest, ansiDim) || !strings.HasSuffix(driftRest, ansiReset) {
		t.Fatalf("expected the drifted line's own metadata dimmed end-to-end, got: %q", driftLine)
	}
}

// TestStatus_TTY_UnreadableNoLookupLine_AddressBrightMetadataDim covers
// the outer-walk unreadableNoLookup helper -- called before any provider
// is even reached, so it previously had no styler at all; UBI-68 threads
// one through so this line follows the identical hierarchy as every other
// resource line instead of staying an outlier.
func TestStatus_TTY_UnreadableNoLookupLine_AddressBrightMetadataDim(t *testing.T) {
	ledgerDir := t.TempDir()
	ledger := core.Open(ledgerDir)
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		ID:            "3333333333333333333333333333333333333333333333333333333333333333",
		Stack:         "payments",
		Kind:          core.KindAdoption,
		Intent:        core.Intent{Summary: "x"},
		Resolution: core.Resolution{
			ResolvedAt: "2026-07-16T00:00:00Z",
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: "payments.fake_widget.widget-dim-nolookup", ObservedHash: "deadbeef"},
			},
		},
		Acceptance: &core.Acceptance{Method: "local", Approvers: []string{"roozbeh"}, AcceptedAt: "2026-07-16T00:00:00Z"},
		Status:     core.StatusAccepted,
	}
	if err := ledger.Append(p); err != nil {
		t.Fatal(err)
	}

	out, err := runUbxTTY(t, "", []string{"FAKEPROVIDER_MODE=ok-v6", "NO_COLOR="}, "status", "--ledger-dir", ledgerDir, "--drift", "--provider", fakeProviderBinary)
	if err == nil {
		t.Fatal("expected exit code 2 for an unreadable resource")
	}
	addr := "payments.fake_widget.widget-dim-nolookup"
	line := lineContaining(out, addr+": ")
	if line == "" {
		t.Fatalf("expected an unreadable line for %s, got: %q", addr, out)
	}
	prefix := "unreadable: " + addr + ": "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("expected the address to render undecorated, got: %q", line)
	}
	rest := strings.TrimPrefix(line, prefix)
	if !strings.HasPrefix(rest, ansiDim) {
		t.Fatalf("expected the metadata after the colon to start dim, got: %q", line)
	}
	// The reason text after " -- " is deliberately outside forceDim's
	// span (only kind/hash/accepted-timestamp are named in the ticket) --
	// its own reset must appear before that text, not after.
	if idx := strings.Index(rest, " -- "); idx == -1 || !strings.Contains(rest[:idx], ansiReset) {
		t.Fatalf("expected the dim span to close before \" -- <reason>\", got: %q", line)
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
