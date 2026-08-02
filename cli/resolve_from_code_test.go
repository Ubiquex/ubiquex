package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// requireDeno skips a test when the real deno binary isn't on PATH --
// same reasoning as tseval's own requireDeno (a new, genuinely hard
// dependency this arc introduces; skip loudly rather than hard-fail
// `go test ./...` for a contributor who hasn't installed it yet).
func requireDeno(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not found in PATH -- skipping ubx resolve --from-code's own real-subprocess tests")
	}
}

// TestResolveFromCode_SimpleCreate mirrors TestResolveAccept_SimpleCreate
// (resolve_test.go) exactly, but authors the same logical intent in
// TypeScript instead of hand-written JSON -- slice 5's own CLI wiring,
// end to end, through the real evaluator, real resolve, real accept,
// real why.
func TestResolveFromCode_SimpleCreate(t *testing.T) {
	requireDeno(t)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	entryPath := filepath.Join("testdata", "sdk_resolve", "create_widget.ts")

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve",
		"--from-code", entryPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
	)
	if err != nil {
		t.Fatalf("ubx resolve --from-code: %v\noutput: %s", err, resolveOut)
	}
	if !strings.Contains(resolveOut, "1 create(s), 0 change(s)") {
		t.Fatalf("expected a 1-create summary, got: %s", resolveOut)
	}

	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	resolved := string(raw)
	if !strings.Contains(resolved, `"kind": "change"`) {
		t.Fatalf("resolved proposal missing kind:change: %s", resolved)
	}
	if !strings.Contains(resolved, `"fake_widget"`) || !strings.Contains(resolved, `"widget1"`) {
		t.Fatalf("resolved proposal missing the TS-authored fake_widget/widget1 resource: %s", resolved)
	}

	// The provenance stamp: a real "document" source naming the real
	// entry file and its own real content hash (docs/sdk.md's own
	// slice-5 decision -- a single "document" entry, the same kind the
	// md medium already uses, not a bespoke "sdk"/"sdk_evaluator" pair).
	if !strings.Contains(resolved, `"kind": "document"`) {
		t.Fatalf("resolved proposal missing a document intent.sources entry: %s", resolved)
	}
	if !strings.Contains(resolved, `"ref": "create_widget.ts"`) {
		t.Fatalf("resolved proposal's document source doesn't name the real entry file: %s", resolved)
	}
	entryBytes, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(entryBytes)
	wantHash := "sha256:" + hex.EncodeToString(sum[:])
	if !strings.Contains(resolved, wantHash) {
		t.Fatalf("resolved proposal's document source content_hash doesn't match the real entry file's own bytes (want %s): %s", wantHash, resolved)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (from-code resolved): %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	whyOut, err := runUbx(t, env, "why", changeID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why (from-code change): %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "change") {
		t.Fatalf("why output missing change kind: %s", whyOut)
	}
	if !strings.Contains(whyOut, "document create_widget.ts") {
		t.Fatalf("why output missing the document provenance source: %s", whyOut)
	}
}

// requireHermeticSandbox skips a test when this platform has no verified
// hermetic mechanism available for the Go evaluator (goeval) -- mirrors
// requireDeno's own reasoning, one level down (goeval's own package
// already skips identically; this mirrors it here since `ubx resolve
// --from-code` is a second, real subprocess call site for the same
// dependency).
func requireHermeticSandbox(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec not found in PATH -- skipping ubx resolve --from-code's own Go real-subprocess tests")
		}
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bubblewrap (bwrap) not found in PATH -- skipping ubx resolve --from-code's own Go real-subprocess tests")
		}
	default:
		t.Skip("no verified hermetic sandbox mechanism on this platform -- skipping ubx resolve --from-code's own Go real-subprocess tests")
	}
}

// TestResolveFromCode_Go_SimpleCreate mirrors TestResolveFromCode_SimpleCreate
// exactly, but authors the same logical intent in Go instead of
// TypeScript, through goeval instead of tseval (UBI-35's own dispatch,
// resolve.go's evaluateSDKProgram) -- proof the two languages really do
// converge on the exact same downstream pipeline, not just similar-
// looking output.
func TestResolveFromCode_Go_SimpleCreate(t *testing.T) {
	requireHermeticSandbox(t)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	entryPath := filepath.Join("testdata", "sdk_resolve_go", "create_widget.go")

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve",
		"--from-code", entryPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
		"--timeout", "60s",
	)
	if err != nil {
		t.Fatalf("ubx resolve --from-code (go): %v\noutput: %s", err, resolveOut)
	}
	if !strings.Contains(resolveOut, "1 create(s), 0 change(s)") {
		t.Fatalf("expected a 1-create summary, got: %s", resolveOut)
	}

	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	resolved := string(raw)
	if !strings.Contains(resolved, `"kind": "change"`) {
		t.Fatalf("resolved proposal missing kind:change: %s", resolved)
	}
	if !strings.Contains(resolved, `"fake_widget"`) || !strings.Contains(resolved, `"widget1"`) {
		t.Fatalf("resolved proposal missing the Go-authored fake_widget/widget1 resource: %s", resolved)
	}
	if !strings.Contains(resolved, `"kind": "document"`) || !strings.Contains(resolved, `"ref": "create_widget.go"`) {
		t.Fatalf("resolved proposal missing a document intent.sources entry naming create_widget.go: %s", resolved)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (from-code go resolved): %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	whyOut, err := runUbx(t, env, "why", changeID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why (from-code go change): %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "document create_widget.go") {
		t.Fatalf("why output missing the document provenance source: %s", whyOut)
	}
}

// requireWasmtime skips a test when the real wasmtime binary isn't on
// PATH -- same reasoning as requireDeno/requireHermeticSandbox: a new,
// genuinely hard dependency this arc introduces.
func requireWasmtime(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not found in PATH -- skipping ubx resolve --from-code's own Python real-subprocess tests")
	}
}

// TestResolveFromCode_Py_SimpleCreate mirrors
// TestResolveFromCode_SimpleCreate/TestResolveFromCode_Go_SimpleCreate
// exactly, but authors the same logical intent in Python instead,
// through pyeval instead of tseval/goeval -- proof all three languages
// really do converge on the exact same downstream pipeline.
func TestResolveFromCode_Py_SimpleCreate(t *testing.T) {
	requireWasmtime(t)
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	entryPath := filepath.Join("testdata", "sdk_resolve_py", "create_widget.py")

	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	resolveOut, err := runUbx(t, env, "resolve",
		"--from-code", entryPath,
		"--provider", fakeProviderBinary,
		"--ledger-dir", ledgerDir,
		"--out", resolvedPath,
		"--timeout", "120s",
	)
	if err != nil {
		t.Fatalf("ubx resolve --from-code (py): %v\noutput: %s", err, resolveOut)
	}
	if !strings.Contains(resolveOut, "1 create(s), 0 change(s)") {
		t.Fatalf("expected a 1-create summary, got: %s", resolveOut)
	}

	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	resolved := string(raw)
	if !strings.Contains(resolved, `"kind": "change"`) {
		t.Fatalf("resolved proposal missing kind:change: %s", resolved)
	}
	if !strings.Contains(resolved, `"fake_widget"`) || !strings.Contains(resolved, `"widget1"`) {
		t.Fatalf("resolved proposal missing the Python-authored fake_widget/widget1 resource: %s", resolved)
	}
	if !strings.Contains(resolved, `"kind": "document"`) || !strings.Contains(resolved, `"ref": "create_widget.py"`) {
		t.Fatalf("resolved proposal missing a document intent.sources entry naming create_widget.py: %s", resolved)
	}

	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (from-code py resolved): %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)

	whyOut, err := runUbx(t, env, "why", changeID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why (from-code py change): %v\noutput: %s", err, whyOut)
	}
	if !strings.Contains(whyOut, "document create_widget.py") {
		t.Fatalf("why output missing the document provenance source: %s", whyOut)
	}
}

func TestResolveFromCode_MutuallyExclusiveWithPositionalArg(t *testing.T) {
	// No requireDeno -- the mutual-exclusivity check happens before
	// tseval.Evaluate is ever called.
	ledgerDir := t.TempDir()
	entryPath := filepath.Join("testdata", "sdk_resolve", "create_widget.ts")
	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1, "kind": "ubx:intent/v1", "stack": "payments",
		"intent": map[string]interface{}{"summary": "x"}, "resources": []map[string]interface{}{},
	})

	_, err := runUbx(t, nil, "resolve", intentPath, "--from-code", entryPath, "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected a mutually-exclusive error, got: %v", err)
	}
}

func TestResolveFromCode_RequiresOneOrTheOther(t *testing.T) {
	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "resolve", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "requires either") {
		t.Fatalf("expected a \"requires either\" error, got: %v", err)
	}
}
