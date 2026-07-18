package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fakeProviderBinary is built once by TestMain and used by every scan/accept
// test below, standing in for a real Terraform provider binary (see
// provider/internal/fakeprovider).
var fakeProviderBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ubx-cli-test")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	fakeProviderBinary = filepath.Join(dir, "fakeprovider")
	build := exec.Command("go", "build", "-o", fakeProviderBinary, "../provider/internal/fakeprovider")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	build.Env = append(os.Environ(), "FAKEPROVIDER_MODE=") // unused at build time, just documenting the knob
	if err := build.Run(); err != nil {
		panic("building fakeprovider fixture: " + err.Error())
	}

	// Every test in this package that runs a command indirectly calls
	// LoadConfig, which walks upward from configSearchStartDir() looking
	// for .ubx/config. Left at its real os.Getwd default, that walk would
	// depend on whatever happens to exist above wherever `go test` is
	// invoked from (a developer's home directory, say) -- exactly the kind
	// of ambient host-machine state `go test ./...` staying hermetic is
	// supposed to rule out. Pin it to this run's own empty scratch
	// directory by default; config_test.go's own tests override this
	// per-test to check real discovery behavior.
	emptyConfigDir := filepath.Join(dir, "no-config-here")
	if err := os.MkdirAll(emptyConfigDir, 0o755); err != nil {
		panic(err)
	}
	configSearchStartDir = func() (string, error) { return emptyConfigDir, nil }

	// Same reasoning, for the cascade's own $HOME ceiling check (UBI-32
	// Arc B addendum): left at the real os.UserHomeDir, every test's own
	// upward walk would compare against whatever the real host machine's
	// $HOME actually is -- harmless today only because no test's temp
	// directory happens to collide with it, not because it's actually
	// pinned. A deliberately nonexistent path removes that coincidence;
	// configcascade_test.go's own ceiling tests override this per-test.
	noHomeDir := filepath.Join(dir, "no-home-here")
	userHomeDir = func() (string, error) { return noHomeDir, nil }

	os.Exit(m.Run())
}

func runUbx(t *testing.T, env []string, args ...string) (stdout string, err error) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(args)

	if len(env) > 0 {
		for _, kv := range env {
			parts := strings.SplitN(kv, "=", 2)
			t.Setenv(parts[0], parts[1])
		}
	}
	err = root.Execute()
	return out.String(), err
}

// exitCode extracts the process exit code err represents under the UBI-20
// contract: 0 for a nil error, an ExitCodeError's own Code, or -1 for a
// plain (non-ExitCodeError) error -- which never happens for an audited
// verb, so callers can tell "an actionable finding" (1) apart from "an
// unaudited plain error" (-1) instead of conflating both with "any error."
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *ExitCodeError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return -1
}

// requireExitCode fails the test unless err's exit code is want (UBI-20's
// CI contract, docs/exit-codes.mdx) -- e.g. a scan that generates a
// proposal now returns ExitCodeError{Code: 1}, not nil, so test helpers
// that treat "a proposal was generated" as the expected outcome must
// assert exit code 1 explicitly rather than "err == nil".
func requireExitCode(t *testing.T, err error, want int, out string) {
	t.Helper()
	if got := exitCode(err); got != want {
		t.Fatalf("expected exit code %d, got %d (err: %v)\noutput: %s", want, got, err, out)
	}
}

// TestScanAcceptWhy exercises Slice 3's whole loop through the CLI: scan
// (new resource -> adoption proposal) -> accept -> why, then scan again
// with a different observed lookup (standing in for "reality changed") ->
// drift_adopt proposal -> accept -> why.
func TestScanAcceptWhy(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget1",
		"--lookup", `{"name":"widget1","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	requireExitCode(t, err, 1, scanOut)
	if !strings.Contains(scanOut, "new:") {
		t.Fatalf("expected a 'new' classification, got: %s", scanOut)
	}

	acceptOut, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (adopt): %v\noutput: %s", err, acceptOut)
	}
	adoptID := mustExtractID(t, acceptOut)

	whyOut, err := runUbx(t, env, "why", adoptID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why (adopt): %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "adoption") {
		t.Fatalf("why output missing adoption kind: %s", whyOut)
	}

	// Scan again with a different lookup -- the fixture just echoes its
	// input, so a different lookup stands in for "reality changed since
	// the last scan" (the same effect a real mutated cloud resource would
	// have: a different observed_hash).
	scanOut2, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget1",
		"--lookup", `{"name":"widget1","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "drift.json"),
		"--no-attribution", // hermetic: never touch real AWS CloudTrail from this suite (see attribution_test.go for the gated live/fake-lookup coverage)
	)
	requireExitCode(t, err, 1, scanOut2)
	if !strings.Contains(scanOut2, "drifted:") {
		t.Fatalf("expected a 'drifted' classification, got: %s", scanOut2)
	}

	acceptOut2, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "drift.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (drift): %v\noutput: %s", err, acceptOut2)
	}
	driftID := mustExtractID(t, acceptOut2)

	whyOut2, err := runUbx(t, env, "why", driftID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why (drift): %v\noutput: %s", err, whyOut2)
	}
	if !strings.Contains(whyOut2, "drift_adopt") {
		t.Fatalf("why output missing drift_adopt kind: %s", whyOut2)
	}

	// Scanning again with the same (post-drift) lookup should now report
	// no drift.
	scanOut3, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget1",
		"--lookup", `{"name":"widget1","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
	)
	if err != nil {
		t.Fatalf("ubx scan (unchanged): %v\noutput: %s", err, scanOut3)
	}
	if !strings.Contains(scanOut3, "no drift") {
		t.Fatalf("expected 'no drift', got: %s", scanOut3)
	}
}

// TestAccept_ReverifyBlocksStaleAcceptance is the CLI-level version of the
// "drift-on-drift staleness" adversarial requirement: a proposal generated
// by scan must be refused at accept time if reality changed again in the
// meantime. --reverify-with no longer takes --lookup (UBI-7 follow-up: the
// lookup key scan used is now persisted in the proposal's
// resolution.inputs and read back from there), so staleness has to come
// from the provider actually returning something different on the second
// call — FAKEPROVIDER_EXTRA_TAG simulates that real out-of-band change
// (see provider/internal/fakeprovider) rather than the test varying
// --lookup itself, which wouldn't reflect how a real caller behaves.
func TestAccept_ReverifyBlocksStaleAcceptance(t *testing.T) {
	ledgerDir := t.TempDir()

	scanOut, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget2",
		"--lookup", `{"name":"widget2","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	requireExitCode(t, err, 1, scanOut)

	// Reality changes again before accept runs: someone tags the resource
	// out-of-band, same lookup key or not.
	_, err = runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6", "FAKEPROVIDER_EXTRA_TAG=mutated=yes"},
		"accept", filepath.Join(ledgerDir, "adopt.json"),
		"--ledger-dir", ledgerDir,
		"--reverify-with", fakeProviderBinary,
		"--resource-type", "fake_widget",
		"--resource-name", "widget2",
	)
	requireExitCode(t, err, 1, "")
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected a staleness error, got: %v", err)
	}

	// And confirm it truly never reached the ledger.
	if _, statErr := os.Stat(filepath.Join(ledgerDir, ".ubx", "ledger.lock")); statErr == nil {
		t.Fatal("ledger.lock should not exist -- the stale proposal must not have been accepted")
	}
}

// TestAccept_ReverifyPassesWhenFresh confirms --reverify-with doesn't block
// a legitimate accept when nothing changed since scan, using only the
// lookup key persisted in the proposal (no --lookup flag on accept).
func TestAccept_ReverifyPassesWhenFresh(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	_, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget3",
		"--lookup", `{"name":"widget3"}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	requireExitCode(t, err, 1, "")

	acceptOut, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"),
		"--ledger-dir", ledgerDir,
		"--reverify-with", fakeProviderBinary,
		"--resource-type", "fake_widget",
		"--resource-name", "widget3",
	)
	if err != nil {
		t.Fatalf("ubx accept (fresh, should pass): %v\noutput: %s", err, acceptOut)
	}
}

// TestGenerateProposal_PersistsLookup confirms the lookup key scan used to
// find the resource round-trips into the generated proposal's
// resolution.inputs (the UBI-7 follow-up amendment to docs/schema.md).
func TestGenerateProposal_PersistsLookup(t *testing.T) {
	ledgerDir := t.TempDir()

	_, err := runUbx(t, []string{"FAKEPROVIDER_MODE=ok-v6"}, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget4",
		"--lookup", `{"name":"widget4"}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	)
	requireExitCode(t, err, 1, "")

	raw, err := os.ReadFile(filepath.Join(ledgerDir, "adopt.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"lookup"`) || !strings.Contains(string(raw), `"widget4"`) {
		t.Fatalf("generated proposal missing a persisted lookup key: %s", raw)
	}
}

func mustExtractID(t *testing.T, acceptOutput string) string {
	t.Helper()
	m := regexp.MustCompile(`accepted ([0-9a-f]{64})`).FindStringSubmatch(acceptOutput)
	if m == nil {
		t.Fatalf("could not find accepted proposal ID in: %s", acceptOutput)
	}
	return m[1]
}
