// Package pyeval is docs/sdk.md's own "The Python evaluator: decided
// empirically" (UBI-36 session 1) made real: the Go-side wrapper that
// runs a Python SDK program hermetically via WASI (wasmtime + a real,
// pinned CPython-WASI build) -- chosen empirically over the expected
// front-runner (subprocess + sandbox-exec/bubblewrap, the Go arc's own
// mechanism retargeted at CPython) because it proved structurally
// stronger: network and subprocess-spawning are absent as WASI
// capabilities, not merely policy-denied, and the mechanism is
// genuinely identical across macOS and Linux (verified this session),
// unlike Go's own two-platform-specific-mechanisms answer.
//
// Unlike sdk/go's own evaluator (goeval: build once, run twice -- a
// compiled binary's whole "cheat"), Python is interpreted, so there is
// no build/run split to exploit -- runOnce spawns a fresh wasmtime
// subprocess re-interpreting the program's own source every time,
// structurally closer to TS's own tseval shape than to goeval's.
package pyeval

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/ubiquex/ubiquex/core"
)

// ExtraDep is one additional host directory pyeval's own WASI sandbox
// preopens (at a fresh, pyeval-assigned top-level guest path, "/ubxdep<i>")
// and adds to the guest's own PYTHONPATH, ahead of the entry script's own
// directory being mounted. UBI-130's own blueprint-dependency mechanism
// (blueprint.ResolvePyDependencies) is the one real caller: it pulls +
// verifies a requirements.txt-declared blueprint into ubx's own local
// cache and passes its built py/ directory here so the calling script's
// own plain `from <pkg> import ...` resolves -- pyeval itself has zero
// knowledge of blueprints, pulling, caching, or verification (blueprint
// already depends on pyeval, so the reverse dependency would be a cycle);
// it only ever mounts whatever real host directory it's handed.
type ExtraDep struct {
	HostDir string
}

// Evaluate spawns the evaluator harness against entryFile TWICE, via
// core.DoubleRun, as two entirely separate `wasmtime` subprocesses --
// PYTHONHASHSEED is pinned unconditionally (docs/sdk.md's own
// PYTHONHASHSEED finding) so the common nondeterminism source never
// even reaches this backstop, but DoubleRun still catches anything else
// (mirrors tseval.Evaluate/goeval.Evaluate's own structure exactly).
// deps is optional and empty for every caller except UBI-130's own
// blueprint-dependency resolution.
func Evaluate(ctx context.Context, entryFile string, deps ...ExtraDep) ([]byte, error) {
	// A project's own virtualenv, if it has one, is mounted like any
	// other dependency directory so that `pip install`-ed packages are
	// importable (UBI-260, pyeval/venv.go). Appended AFTER the caller's
	// own deps so a blueprint dependency resolved by ubx still takes
	// precedence on PYTHONPATH over a same-named package that happens to
	// be installed in the venv.
	if abs, err := filepath.Abs(entryFile); err == nil {
		if sp := venvSitePackages(filepath.Dir(abs)); sp != "" {
			deps = append(deps, ExtraDep{HostDir: sp})
		}
	}

	rawCanon, err := core.DoubleRun(func() ([]byte, error) {
		raw, err := runOnce(ctx, entryFile, deps)
		if err != nil {
			return nil, err
		}
		return core.CanonicalJSONBytes(raw)
	})
	if err != nil {
		return nil, fmt.Errorf("pyeval: %w", err)
	}

	stamped, err := stampDocumentSource(rawCanon, entryFile)
	if err != nil {
		return nil, err
	}
	final, err := core.CanonicalJSONBytes(stamped)
	if err != nil {
		return nil, fmt.Errorf("pyeval: %w", err)
	}

	if err := validateIntentShape(final); err != nil {
		return nil, err
	}
	return final, nil
}
