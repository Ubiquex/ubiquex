package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
)

// TestBlueprintLock_EndToEnd drives the whole pin through the real CLI:
// declare in .ubx/config, plan writes the lock, resolve verifies it, and
// each way of disagreeing is refused with the message that fits it.
//
// The declaration here is the [blueprints] table rather than
// requirements.txt, because the table is the single declaration site
// that works for all three languages and the one the TypeScript and Go
// translators will read. requirements.txt stays supported and is covered
// by the existing tests in blueprint_call_py_test.go.
func TestBlueprintLock_EndToEnd(t *testing.T) {
	requireWasmtime(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	bpDir := writeBlueprintPackagePy(t, dir, "widgetplatform")
	entry := writeBlueprintCallingStackPy(t, dir, bpDir, `"widget1"`)
	// The table replaces requirements.txt for this stack, so the test
	// proves the table alone is sufficient.
	if err := os.Remove(filepath.Join(filepath.Dir(entry), "requirements.txt")); err != nil {
		t.Fatal(err)
	}
	writeStackConfigWithBlueprints(t, ledgerDir, "platform", map[string]string{"widgetplatform": bpDir})

	plan := func(extra ...string) (string, error) {
		args := append([]string{"plan", entry,
			"--provider", fakeProviderBinary,
			"--ledger-dir", ledgerDir,
			"--timeout", "120s",
		}, extra...)
		return runUbx(t, env, args...)
	}
	resolve := func() (string, error) {
		return runUbx(t, env, "resolve", "--from-code", entry,
			"--provider", fakeProviderBinary,
			"--ledger-dir", ledgerDir,
			"--timeout", "120s",
		)
	}

	lockPath := filepath.Join(ledgerDir, ".ubx", blueprint.StackLockFileName)

	// Before any plan, there is no lock and resolve must still work.
	// A stack that has not adopted the pin is not broken by it.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("expected no lock file before the first plan, stat err = %v", err)
	}
	if out, err := resolve(); err != nil {
		t.Fatalf("resolve on an unlocked stack must work: %v\n%s", err, out)
	}

	// plan writes it.
	if out, err := plan(); err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	locked := readLock(t, lockPath)
	entryFor, ok := locked.Entry("platform", "widgetplatform")
	if !ok {
		t.Fatalf("plan did not record the blueprint: %+v", locked)
	}
	if entryFor.Source != bpDir {
		t.Fatalf("lock recorded source %q, want the declaration %q", entryFor.Source, bpDir)
	}
	if !strings.HasPrefix(entryFor.ContentHash, "sha256:") {
		t.Fatalf("lock recorded no real content hash: %+v", entryFor)
	}

	// resolve now verifies against it and agrees.
	if out, err := resolve(); err != nil {
		t.Fatalf("resolve against a matching lock must pass: %v\n%s", err, out)
	}

	t.Run("content moved under an unchanged declaration is refused", func(t *testing.T) {
		restore := tamperLock(t, lockPath, func(l *blueprint.StackLock) {
			e, _ := l.Entry("platform", "widgetplatform")
			e.ContentHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
			l.Set("platform", "widgetplatform", e)
		})
		defer restore()

		out, err := resolve()
		if err == nil {
			t.Fatalf("expected a refusal when the locked hash disagrees:\n%s", out)
		}
		msg := out + err.Error()
		if !strings.Contains(msg, "the content behind it did") {
			t.Fatalf("wrong message for moved content:\n%s", msg)
		}
	})

	t.Run("a changed declaration is refused with its own message", func(t *testing.T) {
		restore := tamperLock(t, lockPath, func(l *blueprint.StackLock) {
			e, _ := l.Entry("platform", "widgetplatform")
			e.Source = "oci://example.invalid/something:v1"
			l.Set("platform", "widgetplatform", e)
		})
		defer restore()

		out, err := resolve()
		if err == nil {
			t.Fatalf("expected a refusal when the declaration disagrees:\n%s", out)
		}
		msg := out + err.Error()
		if !strings.Contains(msg, "the declaration changed") {
			t.Fatalf("wrong message for a changed declaration:\n%s", msg)
		}
		if !strings.Contains(msg, "--update-lock") {
			t.Fatalf("a changed declaration should name the update path:\n%s", msg)
		}
	})

	t.Run("update-lock is the way past a mismatch", func(t *testing.T) {
		restore := tamperLock(t, lockPath, func(l *blueprint.StackLock) {
			e, _ := l.Entry("platform", "widgetplatform")
			e.ContentHash = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
			l.Set("platform", "widgetplatform", e)
		})
		defer restore()

		if out, err := plan(); err == nil {
			t.Fatalf("plan must refuse a mismatch too, not silently rewrite it:\n%s", out)
		}
		if out, err := plan("--update-lock"); err != nil {
			t.Fatalf("--update-lock must accept and record: %v\n%s", err, out)
		}
		after := readLock(t, lockPath)
		e, _ := after.Entry("platform", "widgetplatform")
		if e.ContentHash == "sha256:1111111111111111111111111111111111111111111111111111111111111111" {
			t.Fatal("--update-lock did not replace the stale hash")
		}
	})

	t.Run("an undeclared blueprint is pruned", func(t *testing.T) {
		writeStackConfigWithBlueprints(t, ledgerDir, "platform", map[string]string{})
		defer writeStackConfigWithBlueprints(t, ledgerDir, "platform", map[string]string{"widgetplatform": bpDir})

		// The program still imports it, so this fails to evaluate. The
		// pruning is what is under test, and it happens either way.
		_, _ = plan()
		after := readLock(t, lockPath)
		if _, ok := after.Entry("platform", "widgetplatform"); ok {
			t.Fatal("an entry for a blueprint the stack no longer declares must be pruned")
		}
	})
}

func writeStackConfigWithBlueprints(t *testing.T, ledgerDir, stack string, bps map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(ledgerDir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "stack = %q\n\nblueprints = {\n", stack)
	for name, src := range bps {
		fmt.Fprintf(&b, "  %q = %q\n", name, src)
	}
	b.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(ledgerDir, ".ubx", "config.hcl"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	// The cascade walks up from the working directory, not from
	// --ledger-dir, so point it at the same place for this test.
	orig := configSearchStartDir
	configSearchStartDir = func() (string, error) { return ledgerDir, nil }
	t.Cleanup(func() { configSearchStartDir = orig })
}

func readLock(t *testing.T, path string) *blueprint.StackLock {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	var l blueprint.StackLock
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatalf("parse lock: %v", err)
	}
	return &l
}

// tamperLock edits the lock on disk and returns a function restoring the
// original bytes, so each mismatch case starts from a good lock.
func tamperLock(t *testing.T, path string, edit func(*blueprint.StackLock)) func() {
	t.Helper()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	l := readLock(t, path)
	edit(l)
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
