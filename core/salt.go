package core

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (l *Ledger) saltPath() string { return filepath.Join(l.dir, ".ubx", "salt") }

// Salt returns this ledger directory's redaction salt (UBI-23,
// docs/architecture.md -- Secrets), generating and persisting one at
// .ubx/salt on first use. The salt is never part of any hashed/ledgered
// content and must never be committed -- see docs/schema.md's $redacted
// amendment for the honest recovery implication of losing it (it degrades
// redacted-value equality comparison across history -- an unchanged
// secret reports as drifted exactly once -- it never causes real material
// to leak and never causes a genuine change to go undetected).
func (l *Ledger) Salt() ([]byte, error) {
	path := l.saltPath()
	b, err := os.ReadFile(path)
	if err == nil {
		return b, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read ledger salt: %w", err)
	}

	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate ledger salt: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("generate ledger salt: %w", err)
	}
	if err := os.WriteFile(path, salt, 0o600); err != nil {
		return nil, fmt.Errorf("generate ledger salt: %w", err)
	}
	if err := ensureGitignored(l.dir, ".ubx/salt"); err != nil {
		return nil, fmt.Errorf("generate ledger salt: %w", err)
	}
	return salt, nil
}

// ensureGitignored makes sure entry is listed in dir's .gitignore,
// creating a minimal one if none exists yet -- a safety net against an
// operator's own "git add -A" habit, not the only line of defense (the
// salt file is already 0600 and never itself becomes ledgered content
// regardless of whether it's gitignored).
func ensureGitignored(dir, entry string) error {
	path := filepath.Join(dir, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		return os.WriteFile(path, []byte(entry+"\n"), 0o644)
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	content := string(existing)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += entry + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}
