package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Ledger is a per-stack append-only proposal chain rooted at dir, matching
// docs/schema.md's Ledger layout:
//
//	<dir>/
//	  ledger/proposals/<id>.prop.json
//	  .ubx/ledger.lock
type Ledger struct {
	dir string
}

// Open returns a Ledger rooted at dir. dir need not exist yet; it (and the
// ledger/proposals and .ubx subdirectories) are created on first Append.
func Open(dir string) *Ledger {
	return &Ledger{dir: dir}
}

// Dir returns the root directory l was opened with.
func (l *Ledger) Dir() string { return l.dir }

func (l *Ledger) proposalsDir() string { return filepath.Join(l.dir, "ledger", "proposals") }
func (l *Ledger) lockPath() string     { return filepath.Join(l.dir, ".ubx", "ledger.lock") }
func (l *Ledger) proposalPath(id string) string {
	return filepath.Join(l.proposalsDir(), id+".prop.json")
}

var (
	// ErrProposalNotFound means no ledger entry exists for the given ID.
	ErrProposalNotFound = errors.New("proposal not found in ledger")

	// ErrCorruptLedgerEntry means a ledger file exists but its contents
	// aren't a well-formed proposal record (truncated write, disk
	// corruption, hand-editing, ...).
	ErrCorruptLedgerEntry = errors.New("corrupt ledger entry")

	// ErrCorruptLedgerHead means .ubx/ledger.lock exists but doesn't
	// contain a well-formed hash.
	ErrCorruptLedgerHead = errors.New("corrupt ledger head")

	// ErrParentMismatch means a proposal's Parent doesn't match the
	// ledger's current head — it was resolved against a stale or wrong
	// chain position.
	ErrParentMismatch = errors.New("proposal parent does not match ledger head")

	// ErrDuplicateProposal means a proposal with this ID already exists in
	// the ledger. Ledger entries are immutable once written.
	ErrDuplicateProposal = errors.New("proposal already exists in ledger")
)

var hexHash64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Head returns the current ledger head hash, or "" if the ledger has no
// accepted proposals yet (genesis state).
func (l *Ledger) Head() (string, error) {
	b, err := os.ReadFile(l.lockPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read ledger head: %w", err)
	}
	head := strings.TrimSpace(string(b))
	if head == "" {
		return "", nil
	}
	if !hexHash64.MatchString(head) {
		return "", fmt.Errorf("%w: %q is not a 64-character hex hash", ErrCorruptLedgerHead, head)
	}
	return head, nil
}

// Append writes p — which must already have ID/Status/Acceptance set, see
// Accept — to the ledger and advances the head to p.ID. It refuses to
// append if p.Parent doesn't match the current head, and refuses to
// overwrite an existing entry for the same ID (ledger entries are
// immutable once written).
func (l *Ledger) Append(p *Proposal) error {
	if p.ID == "" {
		return errors.New("append: proposal has no ID (compute and set it first, e.g. via Accept)")
	}

	// Locked for the whole check-then-write sequence below, not just the
	// final write -- two concurrent Accept calls that both read the same
	// head before either writes would otherwise both think they're
	// building on the current head and race to append (UBI-20 workstream
	// 4, docs/architecture.md — Ledger lock). scan/why/status never call
	// Append, so they never contend for this.
	release, err := l.acquireLedgerLock()
	if err != nil {
		return fmt.Errorf("append: %w", err)
	}
	defer release()

	// Checked before the parent match: if this exact ID is already in the
	// ledger, that's the more specific and useful signal to the caller —
	// the head has necessarily moved past a proposal's own parent by the
	// time it's already been appended once, which would otherwise report a
	// confusing "parent mismatch" for what is really a duplicate/retry.
	path := l.proposalPath(p.ID)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("append: %w: %s", ErrDuplicateProposal, p.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("append: %w", err)
	}

	head, err := l.Head()
	if err != nil {
		return fmt.Errorf("append: %w", err)
	}
	if p.Parent != head {
		return fmt.Errorf("append: %w: proposal parent %q, ledger head %q", ErrParentMismatch, p.Parent, head)
	}

	if err := os.MkdirAll(l.proposalsDir(), 0o755); err != nil {
		return fmt.Errorf("append: %w", err)
	}

	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("append: marshal proposal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("append: write proposal: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(l.lockPath()), 0o755); err != nil {
		return fmt.Errorf("append: %w", err)
	}
	if err := os.WriteFile(l.lockPath(), []byte(p.ID+"\n"), 0o644); err != nil {
		return fmt.Errorf("append: write ledger head: %w", err)
	}
	return nil
}

// Read loads one proposal record by ID. A missing file reports
// ErrProposalNotFound; a truncated or otherwise malformed file reports
// ErrCorruptLedgerEntry — never a panic or a silently-wrong partial value.
func (l *Ledger) Read(id string) (*Proposal, error) {
	b, err := os.ReadFile(l.proposalPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("proposal %s: %w", id, ErrProposalNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read proposal %s: %w", id, err)
	}
	var p Proposal
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("proposal %s: %w: %v", id, ErrCorruptLedgerEntry, err)
	}
	return &p, nil
}
