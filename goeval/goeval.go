// Package goeval is docs/sdk.md's own "The Go evaluator: decided
// empirically" (UBI-35 session 1) made real: the Go-side wrapper that
// compiles a Go SDK program to a real binary and runs it, hermetically,
// as the sandboxed unit -- unlike tseval (TypeScript), which spawns a
// sandboxed INTERPRETER (Deno) that re-parses the program's own source
// on every run, a compiled Go program already IS the whole sandboxed
// unit once built: hermeticity comes from OS-level process restriction
// (sandbox-exec on macOS, bubblewrap on Linux -- see sandbox_darwin.go/
// sandbox_linux.go), not from a language-level permission system.
//
// A real, meaningful consequence of "compiled, not interpreted": building
// happens exactly ONCE, outside any sandbox (Go compilation does not
// execute the source it compiles -- CGO_ENABLED=0 and no `go generate`
// step means there is no arbitrary-code-execution risk during `go
// build`, only during running the result); core.DoubleRun then runs the
// SAME already-built binary twice, sandboxed both times, to catch
// runtime nondeterminism. TS's own evaluator, by contrast, has no
// separate build phase to hoist out of the double-run loop -- Deno both
// parses and executes on every invocation, so DoubleRun there wraps the
// whole "load + run" step, not just "run."
//
// A new top-level package, not folded into cli/, matching tseval's own
// precedent (docs/sdk.md, "the Go side lives in cli/ or a new tseval/
// package" -- decided there, applied identically here).
package goeval

import (
	"context"
	"fmt"

	"github.com/ubiquex/ubiquex/core"
)

// Evaluate builds entryFile's own Go module into a binary (once), then
// runs that binary TWICE via core.DoubleRun, each run sandboxed by the
// current platform's own mechanism (sandbox_darwin.go/sandbox_linux.go),
// requiring byte-identical stdout across both. Once confirmed stable,
// stamps real provenance (stampDocumentSource) and re-canonicalizes,
// exactly mirroring tseval.Evaluate's own structure and return
// contract: canonical (RFC 8785/JCS-style, core.CanonicalJSONBytes)
// intent/v1 bytes, provenance-stamped, on success; core.ErrDoubleRunMismatch
// if the two runs disagree; a structural-validation error if the result
// doesn't match ubx:intent/v1's own real shape.
func Evaluate(ctx context.Context, entryFile string) ([]byte, error) {
	binaryPath, cleanup, err := buildProgram(ctx, entryFile)
	if err != nil {
		return nil, fmt.Errorf("goeval: %w", err)
	}
	defer cleanup()

	rawCanon, err := core.DoubleRun(func() ([]byte, error) {
		raw, err := runSandboxed(ctx, binaryPath)
		if err != nil {
			return nil, err
		}
		return core.CanonicalJSONBytes(raw)
	})
	if err != nil {
		return nil, fmt.Errorf("goeval: %w", err)
	}

	stamped, err := stampDocumentSource(rawCanon, entryFile)
	if err != nil {
		return nil, err
	}
	final, err := core.CanonicalJSONBytes(stamped)
	if err != nil {
		return nil, fmt.Errorf("goeval: %w", err)
	}

	if err := validateIntentShape(final); err != nil {
		return nil, err
	}
	return final, nil
}
