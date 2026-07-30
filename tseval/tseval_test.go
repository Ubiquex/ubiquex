package tseval

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// requireDeno skips a test when the real deno binary isn't on PATH --
// slice 4's whole job is proving the REAL sandbox holds under a REAL
// deno subprocess (docs/sdk.md's own "verify before implementing"
// discipline, applied to the evaluator itself, not just its design), so
// these tests deliberately do not mock deno the way conformance/'s own
// FakeOnly types mock a provider. Unlike git (already an assumed
// prerequisite for this being a git repo at all), Deno is a genuinely
// new hard dependency this arc introduces -- skipping loudly rather
// than hard-failing keeps `go test ./...` passing for a contributor who
// hasn't installed it yet, while still running for real whenever it's
// present (as it is in this session's own environment).
func requireDeno(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH -- skipping tseval's real-subprocess tests (see this function's own doc comment)")
	}
}

func evalCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestEvaluate_HappyPath_RealDenoSubprocess(t *testing.T) {
	requireDeno(t)
	canon, err := Evaluate(evalCtx(t), "testdata/happy.ts")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	var doc struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Stack         string `json:"stack"`
		Intent        struct {
			Summary string `json:"summary"`
		} `json:"intent"`
		Resources []struct {
			Type   string          `json:"type"`
			Name   string          `json:"name"`
			Op     string          `json:"op"`
			Config json.RawMessage `json:"config"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(canon, &doc); err != nil {
		t.Fatalf("unmarshal canonical output: %v\noutput: %s", err, canon)
	}
	if doc.SchemaVersion != 1 || doc.Kind != "ubx:intent/v1" || doc.Stack != "payments" {
		t.Fatalf("unexpected document shape: %+v", doc)
	}
	if len(doc.Resources) != 1 || doc.Resources[0].Type != "fake_widget" || doc.Resources[0].Op != "create" {
		t.Fatalf("unexpected resources: %+v", doc.Resources)
	}
	if !strings.Contains(string(doc.Resources[0].Config), `"name":"primary-widget"`) {
		t.Fatalf("expected wire-name-mapped config, got: %s", doc.Resources[0].Config)
	}
}

func TestEvaluate_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	requireDeno(t)
	first, err := Evaluate(evalCtx(t), "testdata/happy.ts")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	second, err := Evaluate(evalCtx(t), "testdata/happy.ts")
	if err != nil {
		t.Fatalf("Evaluate (second call): %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("Evaluate produced different canonical output across two separate calls:\n%s\nvs\n%s", first, second)
	}
}

// --- Adversarial row 1: nondeterminism ---

func TestEvaluate_Row1_DateNow_CaughtByEagerGuard(t *testing.T) {
	requireDeno(t)
	_, err := Evaluate(evalCtx(t), "testdata/nondeterministic_date.ts")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program calling Date.now(), want a NondeterministicAPIError-shaped failure")
	}
	if !strings.Contains(err.Error(), "NondeterministicAPIError") {
		t.Fatalf("expected the eager guard's own NondeterministicAPIError to be named in the error, got: %v", err)
	}
	// The eager guard means this never even reaches a second subprocess
	// run -- the very first run already fails, so this must NOT be
	// reported as a DoubleRun mismatch (a different, misleading failure
	// mode for what's actually an immediate, single-run guard failure).
	if errors.Is(err, core.ErrDoubleRunMismatch) {
		t.Fatalf("expected a direct guard failure, not ErrDoubleRunMismatch: %v", err)
	}
}

func TestEvaluate_Row1_DenoPid_CaughtByDoubleRunBackstop(t *testing.T) {
	requireDeno(t)
	// Deno.pid is real, legitimate, ordinary process introspection --
	// not something the eager guard blocks (it isn't Date/Math.random) --
	// but it genuinely differs between DoubleRun's own two separate
	// subprocess runs. This is the concrete test for "the override can't
	// foresee everything, core.DoubleRun is the backstop"
	// (docs/sdk.md's own "clock/random gap" section).
	_, err := Evaluate(evalCtx(t), "testdata/nondeterministic_pid.ts")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program leaking Deno.pid across two double-run subprocesses, want ErrDoubleRunMismatch")
	}
	if !errors.Is(err, core.ErrDoubleRunMismatch) {
		t.Fatalf("expected core.ErrDoubleRunMismatch, got: %v", err)
	}
}

// --- Adversarial row 2: sandbox escape -- fs/env/net reach ---

func TestEvaluate_Row2_FilesystemReadBlocked(t *testing.T) {
	requireDeno(t)
	_, err := Evaluate(evalCtx(t), "testdata/sandbox_fs.ts")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program reading /etc/hosts, want a permission failure")
	}
	if !strings.Contains(err.Error(), "NotCapable") && !strings.Contains(err.Error(), "read access") {
		t.Fatalf("expected a filesystem permission failure, got: %v", err)
	}
}

func TestEvaluate_Row2_EnvReadBlocked(t *testing.T) {
	requireDeno(t)
	_, err := Evaluate(evalCtx(t), "testdata/sandbox_env.ts")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program reading an env var, want a permission failure")
	}
	if !strings.Contains(err.Error(), "NotCapable") && !strings.Contains(err.Error(), "env access") {
		t.Fatalf("expected an env permission failure, got: %v", err)
	}
}

func TestEvaluate_Row2_NetworkReachBlocked(t *testing.T) {
	requireDeno(t)
	_, err := Evaluate(evalCtx(t), "testdata/sandbox_net.ts")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program attempting a network fetch, want a permission failure")
	}
	if !strings.Contains(err.Error(), "NotCapable") && !strings.Contains(err.Error(), "net access") {
		t.Fatalf("expected a net permission failure, got: %v", err)
	}
}

// --- Adversarial row 2b: dynamic remote import escape ---

func TestEvaluate_Row2b_DynamicRemoteImportBlocked(t *testing.T) {
	requireDeno(t)
	_, err := Evaluate(evalCtx(t), "testdata/sandbox_remote_import.ts")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program dynamically importing a remote URL, want a --no-remote failure")
	}
	if !strings.Contains(err.Error(), "remote") {
		t.Fatalf("expected a --no-remote failure naming a remote specifier, got: %v", err)
	}
}

// --- Adversarial row 4: program throws mid-evaluation ---

func TestEvaluate_Row4_ThrowMidEvaluation_NoPartialOutput(t *testing.T) {
	requireDeno(t)
	canon, err := Evaluate(evalCtx(t), "testdata/throws.ts")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program that throws mid-evaluation, want an error")
	}
	if canon != nil {
		t.Fatalf("Evaluate returned non-nil output alongside an error -- want no partial intent/v1 ever: %s", canon)
	}
	if !strings.Contains(err.Error(), "deliberate mid-evaluation throw") {
		t.Fatalf("expected the program's own real error message to be surfaced verbatim, got: %v", err)
	}
}

// --- Adversarial row 5: output exceeding the intent/v1 schema (Go-level
// unit tests, not real subprocesses -- see validate_test.go's own doc
// comment for why: @ubx/sdk's own runtime, built in slice 3, already
// preemptively blocks every malformed shape at its own API boundary
// (empty name/summary, always op:"create", config always an object),
// so there is no realistic way for a normal SDK program to reach this
// Go-side check with bad output at all -- it is real, load-bearing
// defense-in-depth (a runtime bug, a version-mismatched @ubx/sdk), not
// something a live fixture program can exercise honestly.
