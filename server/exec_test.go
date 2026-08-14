package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeUbxScript writes a tiny, real shell script standing in for the
// real ubx binary -- exec.go's own subprocess plumbing (exit code,
// stdout, stderr capture, working directory, extra env vars) is what
// these tests verify, not ubx's own command logic, which core/cli's own
// test suites already cover directly.
func fakeUbxScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fakeUbxScript uses a real POSIX shell script -- skip on windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-ubx")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunUbx_CapturesStdoutAndSuccessExitCode(t *testing.T) {
	script := fakeUbxScript(t, `echo "real plan output"`)

	result, err := runUbx(context.Background(), script, t.TempDir(), nil, "plan")
	if err != nil {
		t.Fatalf("runUbx: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Stdout != "real plan output\n" {
		t.Errorf("Stdout = %q, want the real captured output", result.Stdout)
	}
}

func TestRunUbx_CapturesFailureExitCodeAndStderr_NotAGoError(t *testing.T) {
	script := fakeUbxScript(t, `echo "a real refusal reason" >&2; exit 2`)

	result, err := runUbx(context.Background(), script, t.TempDir(), nil, "ship", "--yes")
	if err != nil {
		t.Fatalf("runUbx must not itself error on the subprocess's own ordinary failure: %v", err)
	}
	if result.ExitCode != 2 {
		t.Errorf("ExitCode = %d, want 2", result.ExitCode)
	}
	if result.Stderr != "a real refusal reason\n" {
		t.Errorf("Stderr = %q, want the real captured stderr", result.Stderr)
	}
}

func TestRunUbx_PassesExtraEnvWithoutDroppingRealEnvironment(t *testing.T) {
	script := fakeUbxScript(t, `echo "GITHUB_TOKEN=$GITHUB_TOKEN"; echo "HOME_SET=$([ -n "$HOME" ] && echo yes || echo no)"`)

	result, err := runUbx(context.Background(), script, t.TempDir(), []string{"GITHUB_TOKEN=real-token-value"}, "status")
	if err != nil {
		t.Fatalf("runUbx: %v", err)
	}
	if !strings.Contains(result.Stdout, "GITHUB_TOKEN=real-token-value") {
		t.Errorf("Stdout = %q, want the extra env var passed through", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "HOME_SET=yes") {
		t.Errorf("Stdout = %q, want the real process environment (HOME) preserved, not replaced", result.Stdout)
	}
}

func TestRunUbx_RunsInGivenWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fakeUbxScript(t, `cat marker.txt`)

	result, err := runUbx(context.Background(), script, dir, nil, "plan")
	if err != nil {
		t.Fatalf("runUbx: %v", err)
	}
	if result.Stdout != "real" {
		t.Errorf("Stdout = %q, want the marker file's own real content, proving the subprocess ran in dir", result.Stdout)
	}
}
