// Package runner is docs/sdk.md's own "sdk/conformance/runner/": evaluate
// each language's program, canonicalize, byte-compare to golden/ (slice
// 6, built alongside slice 7's live finale, UBI-33/34 session 4). TS, Go,
// and Python runners all exist now (UBI-35 added Go's own case, UBI-36
// added Python's -- each with its own golden file, see
// TestPaymentsGoldenCase_Go's own doc comment for why a separate golden
// file per language, not one shared file) -- the full three-language
// contract UBI-33 named is proven here, in one test file.
package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/goeval"
	"github.com/ubiquex/ubiquex-cli/pyeval"
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

// requireHermeticSandbox skips when this platform has no verified
// hermetic mechanism for goeval (sandbox-exec on macOS, bubblewrap on
// Linux) -- same reasoning as requireDeno, one language over.
func requireHermeticSandbox(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec not found in PATH -- skipping sdk/conformance's own real Go-evaluator tests")
		}
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bubblewrap (bwrap) not found in PATH -- skipping sdk/conformance's own real Go-evaluator tests")
		}
	default:
		t.Skip("no verified hermetic sandbox mechanism on this platform -- skipping sdk/conformance's own real Go-evaluator tests")
	}
}

// TestPaymentsGoldenCase_Go is UBI-35's own sibling to
// TestPaymentsGoldenCase_TS: programs/go/payments.go, independently
// authored (not a mechanical transliteration of payments.ts -- see that
// file's own doc comment), evaluated for real through goeval (compile +
// run under this platform's own OS-level sandbox, core.DoubleRun across
// two real runs of the same built binary) -- byte-compared against
// golden/payments_go.json after canonicalization on both sides.
//
// A SEPARATE golden file from payments.json, not the same one, for a
// real, structural reason: intent.sources' own document-provenance entry
// names the entry file itself and its own real content hash -- payments.go
// and payments.ts are two different files with two different real
// hashes, so no single golden document could ever be byte-identical to
// both at once. What payments_go.json proves is the same thing the
// original TS+md convergence proof established (docs/sdk.md's own "Live
// finale"): the SAME resolved resources/stack/intent.summary, reached by
// a third independent producer -- checked here at the resources/stack/
// summary level (Go-specific provenance is expected and real, not a
// mismatch to paper over).
func TestPaymentsGoldenCase_Go(t *testing.T) {
	requireHermeticSandbox(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	programPath := filepath.Join("..", "programs", "go", "payments.go")
	got, err := goeval.Evaluate(ctx, programPath)
	if err != nil {
		t.Fatalf("goeval.Evaluate(%s): %v", programPath, err)
	}

	goldenRaw, err := os.ReadFile(filepath.Join("..", "golden", "payments_go.json"))
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

// requireWasmtime skips when wasmtime isn't on PATH -- same reasoning as
// requireDeno/requireHermeticSandbox, one language over.
func requireWasmtime(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not found in PATH -- skipping sdk/conformance's own real Python-evaluator tests")
	}
}

// TestPaymentsGoldenCase_Py is UBI-36's own sibling to
// TestPaymentsGoldenCase_TS/_Go: programs/py/payments.py, independently
// authored (not a transliteration -- see that file's own doc comment),
// evaluated for real through pyeval (WASI via wasmtime running a real,
// pinned CPython-WASI build, core.DoubleRun across two real subprocess
// runs) -- byte-compared against golden/payments_py.json after
// canonicalization on both sides.
//
// A fourth independent producer (after the hand-written intent file,
// the md-medium/LLM transcript, and TS/Go) reaching the SAME resolved
// resources/stack/intent.summary -- checked here at that level, exactly
// like TestPaymentsGoldenCase_Go's own comparison (Python-specific
// provenance -- payments.py's own real content hash -- is expected and
// real, not a mismatch to paper over).
func TestPaymentsGoldenCase_Py(t *testing.T) {
	requireWasmtime(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	programPath := filepath.Join("..", "programs", "py", "payments.py")
	got, err := pyeval.Evaluate(ctx, programPath)
	if err != nil {
		t.Fatalf("pyeval.Evaluate(%s): %v", programPath, err)
	}

	goldenRaw, err := os.ReadFile(filepath.Join("..", "golden", "payments_py.json"))
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
