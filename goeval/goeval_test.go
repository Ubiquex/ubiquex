package goeval

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

func isDarwin() bool { return runtime.GOOS == "darwin" }
func isLinux() bool  { return runtime.GOOS == "linux" }

// requireSandbox skips a test when this platform has no verified
// hermetic mechanism available (docs/sdk.md's own "The Go evaluator:
// decided empirically" -- macOS via sandbox-exec, Linux via bubblewrap;
// see sandbox_darwin.go/sandbox_linux.go/sandbox_other.go), mirroring
// tseval's own requireDeno: run for real whenever the real mechanism is
// present (as it is in this session's own macOS environment), skip
// loudly rather than hard-fail otherwise.
func requireSandbox(t *testing.T) {
	t.Helper()
	switch {
	case isDarwin():
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec not found in PATH -- skipping goeval's real-subprocess tests")
		}
	case isLinux():
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bubblewrap (bwrap) not found in PATH -- skipping goeval's real-subprocess tests")
		}
	default:
		t.Skip("no verified hermetic sandbox mechanism on this platform -- skipping goeval's real-subprocess tests")
	}
}

func evalCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestEvaluate_HappyPath_RealSandboxedSubprocess(t *testing.T) {
	requireSandbox(t)
	canon, err := Evaluate(evalCtx(t), "testdata/happy/main.go")
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
	if len(doc.Intent.Sources) != 1 || doc.Intent.Sources[0].Kind != "document" || doc.Intent.Sources[0].Ref != "main.go" {
		t.Fatalf("expected a stamped document source naming main.go, got: %+v", doc.Intent.Sources)
	}
}

func TestEvaluate_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	requireSandbox(t)
	first, err := Evaluate(evalCtx(t), "testdata/happy/main.go")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	second, err := Evaluate(evalCtx(t), "testdata/happy/main.go")
	if err != nil {
		t.Fatalf("Evaluate (second call): %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("Evaluate produced different canonical output across two separate calls:\n%s\nvs\n%s", first, second)
	}
}

// --- Adversarial: nondeterminism ---

func TestEvaluate_Nondeterminism_TimeNow_CaughtByDoubleRun(t *testing.T) {
	requireSandbox(t)
	// Compiled Go has no monkey-patchable Date.now()-equivalent to
	// eagerly guard (docs/sdk.md's own "The determinism story turns out
	// simpler than TypeScript's") -- core.DoubleRun running the SAME
	// already-built binary twice is the whole backstop, proven here
	// against a real nondeterministic call, not a hypothetical one.
	_, err := Evaluate(evalCtx(t), "testdata/nondeterministic_time/main.go")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program leaking time.Now() into config, want ErrDoubleRunMismatch")
	}
	if !errors.Is(err, core.ErrDoubleRunMismatch) {
		t.Fatalf("expected core.ErrDoubleRunMismatch, got: %v", err)
	}
}

// --- Adversarial: sandbox escape -- fs/net reach ---

func TestEvaluate_SandboxEscape_FilesystemReadBlocked(t *testing.T) {
	requireSandbox(t)
	_, err := Evaluate(evalCtx(t), "testdata/sandbox_fs/main.go")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program reading /etc/hosts, want a permission failure")
	}
	if !strings.Contains(err.Error(), "operation not permitted") && !strings.Contains(err.Error(), "permission denied") && !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("expected a filesystem permission failure, got: %v", err)
	}
}

func TestEvaluate_SandboxEscape_NetworkReachBlocked(t *testing.T) {
	requireSandbox(t)
	_, err := Evaluate(evalCtx(t), "testdata/sandbox_net/main.go")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program attempting a network dial, want a permission/unreachable failure")
	}
	if !strings.Contains(err.Error(), "operation not permitted") && !strings.Contains(err.Error(), "network is unreachable") {
		t.Fatalf("expected a network denial failure, got: %v", err)
	}
}

func TestEvaluate_EnvIsScrubbed_NotMerelySandboxDenied(t *testing.T) {
	requireSandbox(t)
	// A real, deliberate distinction (docs/sdk.md): the sandbox profile
	// is a filesystem/network policy, not an env filter -- env-scrubbing
	// is the evaluator's own separate cmd.Env mechanism. This program
	// succeeds (not a permission error) with an empty HOME, proving env
	// visibility was actually cleared, not merely denied-with-an-error.
	canon, err := Evaluate(evalCtx(t), "testdata/sandbox_env/main.go")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !strings.Contains(string(canon), `home=\"\"`) {
		t.Fatalf(`expected an empty scrubbed HOME value (home=\"\"), got: %s`, canon)
	}
}

// --- Adversarial: Go's own analog of the remote-import escape --
// GOPROXY=off must fail a build reaching beyond a local replace/cache,
// never silently fetch untrusted code mid-evaluation. ---

func TestEvaluate_RemoteDependencyDuringBuild_Blocked(t *testing.T) {
	_, err := Evaluate(evalCtx(t), "testdata/remote_dependency/main.go")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program whose go.mod requires an unfetchable remote dependency, want a build failure")
	}
	if !strings.Contains(err.Error(), "GOPROXY") && !strings.Contains(err.Error(), "disabled") && !strings.Contains(err.Error(), "module lookup disabled") {
		t.Fatalf("expected a GOPROXY=off build failure naming the disabled lookup, got: %v", err)
	}
}

// --- Adversarial: program panics mid-evaluation ---

func TestEvaluate_PanicMidEvaluation_NoPartialOutput(t *testing.T) {
	requireSandbox(t)
	canon, err := Evaluate(evalCtx(t), "testdata/throws/main.go")
	if err == nil {
		t.Fatal("Evaluate: got nil error for a program that panics mid-evaluation, want an error")
	}
	if canon != nil {
		t.Fatalf("Evaluate returned non-nil output alongside an error -- want no partial intent/v1 ever: %s", canon)
	}
	if !strings.Contains(err.Error(), "deliberate mid-evaluation panic") {
		t.Fatalf("expected the program's own real panic message to be surfaced, got: %v", err)
	}
}

func TestEvaluate_MissingGoMod_ClearError(t *testing.T) {
	_, err := Evaluate(evalCtx(t), "/tmp")
	if err == nil {
		t.Fatal("Evaluate: got nil error for an entry path with no go.mod above it")
	}
}
