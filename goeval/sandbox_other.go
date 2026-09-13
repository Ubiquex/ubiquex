//go:build !darwin && !linux

package goeval

import (
	"context"
	"fmt"
	"runtime"
)

// runSandboxed refuses to run on any platform where no hermetic
// mechanism has been empirically verified (docs/sdk.md's own "The Go
// evaluator: decided empirically" only tested macOS and Linux this
// session) -- a hard error, never a silent unsandboxed fallback.
func runSandboxed(ctx context.Context, binaryPath string) (stdoutBytes, stderrBytes []byte, err error) {
	return nil, nil, fmt.Errorf("goeval: no hermetic sandbox mechanism implemented for GOOS=%s -- refusing to evaluate unsandboxed", runtime.GOOS)
}
