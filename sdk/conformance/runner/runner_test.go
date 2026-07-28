// Package runner is docs/sdk.md's own "sdk/conformance/runner/": evaluate
// each language's program, canonicalize, byte-compare to golden/ (slice
// 6, built alongside slice 7's live finale, UBI-33/34 session 4). Only a
// TS runner exists so far -- UBI-35/36 (Go/Python) each add their own
// evaluator and their own case in this same suite, against the identical
// golden files, once built.
package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/sdkeval"
)

// requireDeno skips when deno isn't on PATH -- same reasoning as
// sdkeval's own requireDeno (a genuinely new hard dependency; skip
// loudly rather than break `go test ./...` for a contributor who
// hasn't installed it).
func requireDeno(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH -- skipping sdk/conformance's own real-evaluator tests")
	}
}

// TestPaymentsGoldenCase_TS is the suite's own first case (docs/sdk.md's
// slice 6): programs/ts/payments.ts, evaluated for real through the real
// Deno harness -- no real provider binary needed at evaluate time
// (programs/ts/generated/hashicorp-aws.ts is already-generated, static,
// committed content; codegen ran once, offline-after-generation is the
// whole point, docs/sdk.md's own "ubx sdk gen" section) -- byte-compared
// against golden/payments.json after canonicalization on both sides
// (the committed fixture is pretty-printed for reviewability, never
// assumed to already be in canonical form itself).
//
// This is the SAME real, concrete values a real, live `ubx propose
// --from-doc payments.md` run produced (UBI-33/34 session 4's own live
// finale, docs/sdk.md's own "Live finale" section has the full real
// transcript) -- this test's own passing is the ongoing, automated half
// of that one-time convergence proof: if a future change to the runtime,
// codegen, or this program ever drifts from the golden shape, this test
// catches it immediately, not just at the one session that happened to
// check by hand.
func TestPaymentsGoldenCase_TS(t *testing.T) {
	requireDeno(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	programPath := filepath.Join("..", "programs", "ts", "payments.ts")
	got, err := sdkeval.Evaluate(ctx, programPath)
	if err != nil {
		t.Fatalf("sdkeval.Evaluate(%s): %v", programPath, err)
	}

	goldenRaw, err := os.ReadFile(filepath.Join("..", "golden", "payments.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	want, err := core.CanonicalJSONBytes(goldenRaw)
	if err != nil {
		t.Fatalf("canonicalize golden fixture: %v", err)
	}

	if string(got) != string(want) {
		t.Fatalf("evaluated output does not match the golden fixture, byte for byte after canonicalization:\ngot:  %s\nwant: %s", got, want)
	}
}
