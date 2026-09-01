package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conformanceWidgetEnv ships/scans a single-attribute (name) fake_widget
// via fakeprovider's own conformance mode (UBI-123's own real CREATE
// support for conformance mode, provider/internal/fakeprovider's own
// doc comment on fakeConformanceServerV6.ApplyResourceChange) -- unlike
// ok-v6 mode, whose only drift-simulation lever (FAKEPROVIDER_EXTRA_TAG)
// is tags-only, conformance mode's FAKEPROVIDER_MUTATE_ATTR/VALUE can
// drift ANY declared attribute, giving a real TOP-LEVEL SCALAR drift --
// exactly what the Go override-statement renderer needs to prove
// correctness against.
var conformanceWidgetEnv = []string{
	"FAKEPROVIDER_MODE=conformance-v6",
	"FAKEPROVIDER_RESOURCE_TYPE=fake_widget",
	"FAKEPROVIDER_ATTRS=id,name",
}

// driftEnvForName is conformanceWidgetEnv plus a real out-of-band
// mutation of the "name" attribute -- MUTATE_ATTR/VALUE apply
// unconditionally to whatever echoConformanceState is handed, so setting
// them ONLY for the drift-scan call (never the original create) is what
// makes this genuinely simulate drift rather than baking the mutated
// value in from the start.
func driftEnvForName(newValue string) []string {
	return append(append([]string{}, conformanceWidgetEnv...), "FAKEPROVIDER_MUTATE_ATTR=name", "FAKEPROVIDER_MUTATE_VALUE="+newValue)
}

// shipOneDriftableWidget adopts payments.fake_widget.primary with
// name="v1", via the SAME adoptViaCLI helper status_test.go's own real
// drift tests use (scan --propose + accept, --lookup carrying the FULL
// recorded config) -- deliberately NOT resolve/accept/ship: a create-
// shipped resource's own recorded lookup key is narrower than its full
// config (confirmed live, not assumed -- a first version of this helper
// used resolve/ship and found every later drift-scan spuriously
// "drifted" on every attribute the lookup didn't carry, even with no
// real mutation at all), whereas adopt's own --lookup IS exactly what a
// later scan echoes back, giving a genuinely clean, controllable
// baseline to drift away from.
func shipOneDriftableWidget(t *testing.T, ledgerDir string) {
	t.Helper()
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "primary", `{"name":"v1"}`, conformanceWidgetEnv)
}

const goSDKMarkerFile = `package main

import (
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

func main() {
	sdk.Main(sdk.Stack("payments", func() {}))
}
`

// TestRenderSyncOverrides_GoSyntax_CorrectStatement is UBI-86 Part 2's
// own required proof, Go half: a real drift (fakeprovider's own
// conformance-mode FAKEPROVIDER_MUTATE_ATTR) against a real shipped
// fake_widget, --sync-overrides against a directory whose one
// authoring-medium file is Go, generates a real sdk.Override(...) call
// carrying the drifted resource's own current value.
func TestRenderSyncOverrides_GoSyntax_CorrectStatement(t *testing.T) {
	ledgerDir := t.TempDir()
	shipOneDriftableWidget(t, ledgerDir)
	if err := os.WriteFile(filepath.Join(ledgerDir, "create_primary.go"), []byte(goSDKMarkerFile), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, driftEnvForName("v2"), "render", "--md", "--sync-overrides",
		"--stack", "payments", "--ledger-dir", ledgerDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx render --md --sync-overrides: %v\noutput: %s", err, out)
	}
	want := "sdk.Override(\"payments.fake_widget.primary\", map[string]any{\n\t\"name\": \"v2\",\n})\n"
	if out != want+"\n" {
		t.Fatalf("generated statement mismatch:\n got:  %q\nwant:  %q", out, want+"\n")
	}
}

func TestRenderFromDrift_SingleAddress_GeneratesOneStatement(t *testing.T) {
	ledgerDir := t.TempDir()
	shipOneDriftableWidget(t, ledgerDir)
	if err := os.WriteFile(filepath.Join(ledgerDir, "create_primary.go"), []byte(goSDKMarkerFile), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, driftEnvForName("v2"), "render", "--md", "--from-drift", "payments.fake_widget.primary",
		"--stack", "payments", "--ledger-dir", ledgerDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx render --md --from-drift: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, `sdk.Override("payments.fake_widget.primary"`) {
		t.Fatalf("expected the single override statement, got:\n%s", out)
	}
}

// TestRenderFromDrift_NotDrifted_ExitOne confirms a genuinely clean
// resource (a real re-scan against the exact same lookup adopt already
// recorded, no mutation) correctly reports "not drifted," not a false
// positive.
func TestRenderFromDrift_NotDrifted_ExitOne(t *testing.T) {
	ledgerDir := t.TempDir()
	shipOneDriftableWidget(t, ledgerDir)
	if err := os.WriteFile(filepath.Join(ledgerDir, "create_primary.go"), []byte(goSDKMarkerFile), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, conformanceWidgetEnv, "render", "--md", "--from-drift", "payments.fake_widget.primary",
		"--stack", "payments", "--ledger-dir", ledgerDir, "--provider", fakeProviderBinary)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(err.Error(), "is not currently drifted") {
		t.Fatalf("expected a not-currently-drifted refusal, got: %v", err)
	}
}

func TestRenderSyncOverrides_RequiresMd(t *testing.T) {
	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "render", "--sync-overrides", "--stack", "payments", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "require --md") {
		t.Fatalf("expected a --md-required refusal, got: %v", err)
	}
}

// TestRenderFromDrift_UsesConfigFileProviderDefault is a real, live-found
// fix: the first version of --from-drift/--sync-overrides required
// --provider on every invocation even when .ubx/config already declared
// one -- every OTHER provider-launching command (ubx status --drift, ubx
// resolve) applies that config-file default automatically
// (applyProviderDefaults). Caught during this ticket's own required live
// verification (a real `ubx render --md --sync-overrides` run against a
// real playground config failed with "either --provider or --source... is
// required" despite .ubx/config already naming one), fixed at the actual
// cause rather than worked around by always passing --provider in tests.
func TestRenderFromDrift_UsesConfigFileProviderDefault(t *testing.T) {
	ledgerDir := t.TempDir()
	shipOneDriftableWidget(t, ledgerDir)
	if err := os.WriteFile(filepath.Join(ledgerDir, "create_primary.go"), []byte(goSDKMarkerFile), 0o644); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, ledgerDir, "stack = \"payments\"\n\nprovider = {\n  path = "+quoteHCL(fakeProviderBinary)+"\n}\n")
	withConfigSearchDir(t, ledgerDir)

	out, err := runUbx(t, driftEnvForName("v2"), "render", "--md", "--from-drift", "payments.fake_widget.primary",
		"--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx render --md --from-drift (no --provider flag, config default only): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, `sdk.Override("payments.fake_widget.primary"`) {
		t.Fatalf("expected the override statement, got:\n%s", out)
	}
}

func quoteHCL(s string) string {
	return "\"" + strings.ReplaceAll(s, `"`, `\"`) + "\""
}

func TestRenderSyncOverrides_AmbiguousMedium_Refused(t *testing.T) {
	ledgerDir := t.TempDir()
	shipOneDriftableWidget(t, ledgerDir)
	// Two candidate SDK programs -- ambiguous, must refuse rather than
	// guess (autodetectMedium's own established "pick one" contract).
	if err := os.WriteFile(filepath.Join(ledgerDir, "create_primary.go"), []byte(goSDKMarkerFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ledgerDir, "create_secondary.go"), []byte(goSDKMarkerFile), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runUbx(t, driftEnvForName("v2"), "render", "--md", "--sync-overrides",
		"--stack", "payments", "--ledger-dir", ledgerDir, "--provider", fakeProviderBinary)
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "found 2") {
		t.Fatalf("expected an ambiguous-medium refusal naming 2 candidates, got: %v", err)
	}
}
