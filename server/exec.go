package server

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// selfExePath finds ubx's own real, currently-running executable path --
// resolved once, at Server startup (see server.go), not re-derived on
// every invocation. Same binary as ubx itself, not a second codebase
// (UBI-28's own core safety property): every actual plan/accept/ship/
// scan operation this package performs shells back out to this exact
// path, reusing that command's real logic (confirm-destroys enforcement,
// freshness re-verification, ledger writes) exactly as a human or a CI
// job invoking it directly would, rather than re-implementing or risking
// drift from a second copy of any of it here.
func selfExePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve ubx's own executable path: %w", err)
	}
	return path, nil
}

// runResult is one subprocess invocation's own real, complete outcome --
// grouped together because callers need all three together (exit code
// to decide whether it succeeded, stdout to post as a comment or parse,
// stderr because a failure's real cause is usually there, not stdout).
type runResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// runUbx invokes selfExe with args, in dir, with the given extra
// environment variables appended to the process's own real environment
// (never replacing it -- PATH, HOME, and everything else a provider
// binary or git invocation underneath needs stays intact). Never
// returns a non-nil error for the subprocess's own real, ordinary
// failure (a resolve error, a stale proposal, a rejected destroy) --
// that's ExitCode/Stderr's job to carry, exactly like a human reading a
// failed command's own output; err is reserved for ubx server's own
// inability to even run the command at all (the binary vanished, the
// working directory doesn't exist).
func runUbx(ctx context.Context, selfExe, dir string, extraEnv []string, args ...string) (runResult, error) {
	cmd := exec.CommandContext(ctx, selfExe, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := runResult{Stdout: stdout.String(), Stderr: stderr.String()}

	if err == nil {
		result.ExitCode = 0
		return result, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, fmt.Errorf("run %s %v: %w", selfExe, args, err)
}
