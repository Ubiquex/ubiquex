package pyeval

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// requireWasmtime skips a test when the real wasmtime binary isn't on
// PATH -- same reasoning as sdkeval's own requireDeno and goeval's own
// requireSandbox: a genuinely new hard dependency this arc introduces;
// skip loudly rather than break `go test ./...` for a contributor who
// hasn't installed it yet, run for real whenever it's present (as it is
// in this session's own environment).
func requireWasmtime(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not found in PATH -- skipping pyeval's own real-subprocess tests")
	}
}

func evalCtx(t *testing.T) context.Context {
	t.Helper()
	// The first real run of a session may need to download+cache the
	// CPython-WASI build (~42MB) -- a longer timeout than goeval's own
	// purely-local compile+run budget.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestEvaluate_HappyPath_RealWasiSubprocess(t *testing.T) {
	requireWasmtime(t)
	canon, err := Evaluate(evalCtx(t), "testdata/happy/main.py")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	var doc struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Stack         string `json:"stack"`
		Intent        struct {
			Summary string `json:"summary"`
			Sources []struct {
				Kind string `json:"kind"`
				Ref  string `json:"ref"`
			} `json:"sources"`
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
	if len(doc.Intent.Sources) != 1 || doc.Intent.Sources[0].Kind != "document" || doc.Intent.Sources[0].Ref != "main.py" {
		t.Fatalf("expected a stamped document source naming main.py, got: %+v", doc.Intent.Sources)
	}
}

func TestEvaluate_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	requireWasmtime(t)
	first, err := Evaluate(evalCtx(t), "testdata/happy/main.py")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	second, err := Evaluate(evalCtx(t), "testdata/happy/main.py")
	if err != nil {
		t.Fatalf("Evaluate (second call): %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("Evaluate produced different canonical output across two separate calls:\n%s\nvs\n%s", first, second)
	}
}

// --- Adversarial: nondeterminism ---

func TestEvaluate_Nondeterminism_TimeNow_CaughtByDoubleRun(t *testing.T) {
	requireWasmtime(t)
	// PYTHONHASHSEED is pinned unconditionally (the common Python
	// nondeterminism trap, docs/sdk.md), so this proves the OTHER real
	// source -- time.time_ns() -- is still caught by core.DoubleRun as
	// the backstop, the same two-layer shape Go's own time.Now() finding
	// established.
	_, err := Evaluate(evalCtx(t), "testdata/nondeterministic_time/main.py")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program leaking time.time_ns() into config, want ErrDoubleRunMismatch")
	}
	if !errors.Is(err, core.ErrDoubleRunMismatch) {
		t.Fatalf("expected core.ErrDoubleRunMismatch, got: %v", err)
	}
}

// --- Adversarial: sandbox escape -- fs/net reach, structurally absent under WASI ---

func TestEvaluate_SandboxEscape_FilesystemReadBlocked(t *testing.T) {
	requireWasmtime(t)
	_, err := Evaluate(evalCtx(t), "testdata/sandbox_fs/main.py")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program reading /etc/hosts, want a failure")
	}
	if !strings.Contains(err.Error(), "No such file or directory") && !strings.Contains(err.Error(), "FileNotFoundError") {
		t.Fatalf("expected a deny-by-nonexistence failure, got: %v", err)
	}
}

func TestEvaluate_SandboxEscape_NetworkReachBlocked(t *testing.T) {
	requireWasmtime(t)
	_, err := Evaluate(evalCtx(t), "testdata/sandbox_net/main.py")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program attempting a network dial, want a failure")
	}
	if !strings.Contains(err.Error(), "getaddrinfo") && !strings.Contains(err.Error(), "Not supported") {
		t.Fatalf("expected a WASI network-capability-absent failure, got: %v", err)
	}
}

func TestEvaluate_EnvIsAbsent_NotMerelyDenied(t *testing.T) {
	requireWasmtime(t)
	// wasmtime forwards zero host env vars into the guest by default
	// (docs/sdk.md) -- this program succeeds (not a permission error)
	// with an empty HOME, proving env visibility was never granted at
	// all, not merely denied-with-an-error.
	canon, err := Evaluate(evalCtx(t), "testdata/sandbox_env/main.py")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !strings.Contains(string(canon), `home=None`) {
		t.Fatalf("expected an absent HOME value (home=None), got: %s", canon)
	}
}

// --- Adversarial: program raises mid-evaluation ---

func TestEvaluate_ExceptionMidEvaluation_NoPartialOutput(t *testing.T) {
	requireWasmtime(t)
	canon, err := Evaluate(evalCtx(t), "testdata/throws/main.py")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program that raises mid-evaluation, want an error")
	}
	if canon != nil {
		t.Fatalf("Evaluate returned non-nil output alongside an error -- want no partial intent/v1 ever: %s", canon)
	}
	if !strings.Contains(err.Error(), "deliberate mid-evaluation exception") {
		t.Fatalf("expected the program's own real exception message to be surfaced, got: %v", err)
	}
}

func TestEvaluate_MissingEntryFile_ClearError(t *testing.T) {
	_, err := Evaluate(evalCtx(t), "testdata/does_not_exist.py")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a nonexistent entry file")
	}
}
