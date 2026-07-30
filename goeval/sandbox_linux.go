//go:build linux

package goeval

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// runSandboxed runs binaryPath once under bubblewrap (bwrap) --
// docs/sdk.md's own "The Go evaluator: decided empirically" real,
// verified Linux mechanism: unprivileged user+mount+net namespaces
// together, the same primitive Flatpak already relies on. Deliberately
// never falls back to unsandboxed execution when bwrap is missing or
// namespace creation is denied (a real, empirically-found caveat: this
// can happen when already running inside another, more restrictively-
// configured container -- see docs/sdk.md) -- fails loudly instead,
// matching this project's own standing refusal to silently degrade a
// safety guarantee.
//
// No --ro-bind for /lib, /lib64, or /usr/lib: buildProgram compiles
// with CGO_ENABLED=0, producing a fully static binary with zero dynamic-
// library reads at run time, so the ONLY path this sandbox needs to
// expose at all is the binary's own directory.
func runSandboxed(ctx context.Context, binaryPath string) ([]byte, error) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("bubblewrap (bwrap) not found in PATH -- required to run a Go SDK program hermetically on Linux; refusing to evaluate unsandboxed rather than silently degrading (docs/sdk.md's own \"The Go evaluator: decided empirically\"): %w", err)
	}

	binDir := filepath.Dir(binaryPath)
	args := []string{
		"--ro-bind", binDir, binDir,
		"--proc", "/proc",
		"--dev", "/dev",
		"--unshare-net",
		"--unshare-pid",
		"--die-with-parent",
		"--clearenv",
		binaryPath,
	}

	cmd := exec.CommandContext(ctx, bwrapPath, args...)
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
