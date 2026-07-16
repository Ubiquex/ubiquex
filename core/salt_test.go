package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLedger_Salt_GeneratesAndPersists(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)

	salt1, err := l.Salt()
	if err != nil {
		t.Fatalf("Salt: %v", err)
	}
	if len(salt1) != 32 {
		t.Fatalf("expected a 32-byte salt, got %d bytes", len(salt1))
	}

	info, err := os.Stat(filepath.Join(dir, ".ubx", "salt"))
	if err != nil {
		t.Fatalf("expected .ubx/salt to exist: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected .ubx/salt to be 0600, got %o", perm)
	}

	// A fresh Ledger handle over the same directory must read back the
	// exact same salt -- not regenerate one -- since salt stability across
	// process invocations is what keeps an unchanged secret's redacted
	// hash equal across separate `ubx` runs.
	salt2, err := Open(dir).Salt()
	if err != nil {
		t.Fatalf("Salt (second read): %v", err)
	}
	if string(salt1) != string(salt2) {
		t.Fatal("expected Salt to return the same bytes on a subsequent call/process")
	}
}

func TestLedger_Salt_CreatesGitignore(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir).Salt(); err != nil {
		t.Fatalf("Salt: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("expected a .gitignore to be created: %v", err)
	}
	if !strings.Contains(string(b), ".ubx/salt") {
		t.Fatalf("expected .gitignore to list .ubx/salt, got: %q", string(b))
	}
}

func TestLedger_Salt_AppendsToExistingGitignore(t *testing.T) {
	dir := t.TempDir()
	existing := "*.tfstate\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}
	if _, err := Open(dir).Salt(); err != nil {
		t.Fatalf("Salt: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "*.tfstate") {
		t.Fatalf("expected existing .gitignore content to survive, got: %q", got)
	}
	if !strings.Contains(got, ".ubx/salt") {
		t.Fatalf("expected .ubx/salt to be appended, got: %q", got)
	}
}

func TestLedger_Salt_DoesNotDuplicateGitignoreEntry(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".ubx/salt\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}
	if _, err := Open(dir).Salt(); err != nil {
		t.Fatalf("Salt: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if n := strings.Count(string(b), ".ubx/salt"); n != 1 {
		t.Fatalf("expected exactly one .ubx/salt entry, got %d in: %q", n, string(b))
	}
}

// TestLedger_Salt_RegeneratedAfterLoss is the adversarial "salt
// missing/regenerated" case (UBI-23): deleting .ubx/salt and calling Salt
// again must succeed (never error) and must produce a genuinely different
// salt -- crypto/rand, not some deterministic fallback -- confirming the
// documented recovery tradeoff (redacted-value equality breaks across the
// loss, it doesn't break the mechanism itself).
func TestLedger_Salt_RegeneratedAfterLoss(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	salt1, err := l.Salt()
	if err != nil {
		t.Fatalf("Salt: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, ".ubx", "salt")); err != nil {
		t.Fatalf("remove salt: %v", err)
	}
	salt2, err := l.Salt()
	if err != nil {
		t.Fatalf("Salt (after loss): %v", err)
	}
	if string(salt1) == string(salt2) {
		t.Fatal("expected a regenerated salt to differ from the lost one")
	}
}
