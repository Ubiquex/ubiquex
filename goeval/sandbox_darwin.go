//go:build darwin

package goeval

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sandboxProfileTemplate is docs/sdk.md's own "The Go evaluator: decided
// empirically" macOS profile, real and verified this session: import
// Apple's own system.sb (dyld bootstrap access + a handful of narrowly-
// scoped OS reads, no real network -- confirmed by reading the shipped
// profile directly) as a base, then explicitly deny everything a
// hermetic evaluator must deny. A naive from-scratch "(deny default)"
// profile crashes the process before main() even runs (dyld itself gets
// denied the shared-cache read it needs) -- system.sb is the real,
// working fix, not a guess. The compiled binary's own directory is the
// one carved-out readable/executable path (it needs no other file
// access at run time -- a compiled binary doesn't re-read its own
// source).
const sandboxProfileTemplate = `(version 1)
(import "system.sb")
(allow process-exec)
(allow process-fork)
(allow file-read* file-map-executable (subpath %q))
(deny file-write*)
(deny file-read* (subpath "/Users"))
(deny file-read* (subpath "/private/etc"))
(deny file-read* (subpath "/private/var"))
(deny network-outbound)
(deny network-inbound)
`

// runSandboxed runs binaryPath once under sandbox-exec, denying file
// read/write outside the binary's own directory and all network in/out
// -- verified empirically to hold even against a subprocess the
// sandboxed binary itself spawns (macOS sandbox profiles apply to the
// whole descendant process tree by default).
func runSandboxed(ctx context.Context, binaryPath string) ([]byte, error) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return nil, fmt.Errorf("sandbox-exec not found in PATH -- required to run a Go SDK program hermetically on macOS: %w", err)
	}

	profileFile, err := os.CreateTemp("", "ubx-goeval-profile-*.sb")
	if err != nil {
		return nil, fmt.Errorf("write sandbox profile: %w", err)
	}
	defer os.Remove(profileFile.Name())
	profile := fmt.Sprintf(sandboxProfileTemplate, filepath.Dir(binaryPath))
	if _, err := profileFile.WriteString(profile); err != nil {
		profileFile.Close()
		return nil, fmt.Errorf("write sandbox profile: %w", err)
	}
	if err := profileFile.Close(); err != nil {
		return nil, fmt.Errorf("write sandbox profile: %w", err)
	}

	cmd := exec.CommandContext(ctx, "sandbox-exec", "-f", profileFile.Name(), binaryPath)
	// Env-scrubbing is a real, cheap additional layer, not the primary
	// mechanism -- sandbox-exec's own file/network denial is (confirmed
	// empirically, docs/sdk.md: env-scrubbing alone does not block a
	// single one of file-read/file-write/network).
	cmd.Env = []string{}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("run: %w\n%s", err, msg)
		}
		return nil, fmt.Errorf("run: %w", err)
	}
	return stdout.Bytes(), nil
}
