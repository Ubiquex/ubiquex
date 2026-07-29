// Package sdkeval is docs/sdk.md's own "evaluator harness" (slice 4):
// the Go-side wrapper that spawns the real, locked-down Deno subprocess
// evaluating an SDK program (@ubx/sdk, sdk/ts/runtime), wires
// core.DoubleRun across two entirely separate subprocess runs, and
// validates the resulting document's own real structural shape.
//
// A new top-level package, not folded into cli/ -- docs/sdk.md's own
// "sdk/ts/evaluator/... the Go side lives in cli/ or a new sdkeval/
// package" named this as an open call; a standalone package matches
// this project's own established shape for a substantial, independently
// testable subsystem (intentprovider/, audit/cloudtrail/, audit/gcp/,
// ledgerstore/ are all top-level too) -- cli/ stays a thin wiring layer,
// not this session's job to build (ubx resolve --from-code is slice 5).
package sdkeval

import (
	"context"
	"fmt"

	"github.com/ubiquex/ubiquex-cli/core"
)

// Evaluate spawns the evaluator harness against entryFile TWICE, via
// core.DoubleRun (reused completely unchanged, exactly as docs/sdk.md's
// own "Double-run determinism at the evaluation boundary" section
// pins), as two entirely separate `deno` subprocesses -- a stronger
// guarantee than an in-process double-call, since it also catches
// process-level nondeterminism (e.g. a program that leaks Deno.pid into
// its own config) that an in-process call structurally can't.
//
// Once the evaluator's own raw output is confirmed stable across both
// runs, Evaluate stamps real provenance (stampDocumentSource, below) and
// re-canonicalizes -- stamping happens OUTSIDE the DoubleRun closure,
// deliberately: hashing a static file's own bytes is already fully
// deterministic Go-side work with no evaluator-subprocess nondeterminism
// of its own to catch, so folding it into the double-run comparison
// would only slow down the common case for no correctness gain (the
// same "everything that must vary identically lives inside the closure,
// everything else lives outside it" discipline docs/reliability-report.md's
// own resolvedAt bug already taught this project once).
//
// Returns canonical (RFC 8785/JCS-style, core.CanonicalJSONBytes)
// intent/v1 bytes, provenance-stamped, on success; core.ErrDoubleRunMismatch
// if the two evaluator runs disagree; a structural-validation error
// (validate.go) if the result doesn't match ubx:intent/v1's own real
// shape.
func Evaluate(ctx context.Context, entryFile string) ([]byte, error) {
	rawCanon, err := core.DoubleRun(func() ([]byte, error) {
		raw, err := runOnce(ctx, entryFile)
		if err != nil {
			return nil, err
		}
		return core.CanonicalJSONBytes(raw)
	})
	if err != nil {
		return nil, fmt.Errorf("sdkeval: %w", err)
	}

	stamped, err := stampDocumentSource(rawCanon, entryFile)
	if err != nil {
		return nil, err
	}
	final, err := core.CanonicalJSONBytes(stamped)
	if err != nil {
		return nil, fmt.Errorf("sdkeval: %w", err)
	}

	if err := validateIntentShape(final); err != nil {
		return nil, err
	}
	return final, nil
}
