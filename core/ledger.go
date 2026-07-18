package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Ledger is a per-stack append-only proposal chain, backed by a
// LedgerStore (UBI-32 Arc B, docs/architecture.md — "LedgerStore
// interface"). Open constructs the git-directory reference
// implementation, matching docs/schema.md's Ledger layout, unchanged:
//
//	<dir>/
//	  ledger/proposals/<id>.prop.json
//	  .ubx/ledger.lock
//
// OpenStore wraps an arbitrary LedgerStore directly -- what a remote
// (s3://, gs://, azblob://) store uses.
type Ledger struct {
	dir   string // "" unless backed by the git-directory store; see Dir/Exists
	store LedgerStore
}

// Open returns a Ledger backed by the git-directory reference
// implementation, rooted at dir. dir need not exist yet; it (and the
// ledger/proposals and .ubx subdirectories) are created on first Append.
func Open(dir string) *Ledger {
	return &Ledger{dir: dir, store: newGitLedgerStore(dir)}
}

// OpenStore returns a Ledger backed by an arbitrary LedgerStore -- the
// entry point a remote store's own opener uses (see docs/architecture.md
// — Addressing).
func OpenStore(store LedgerStore) *Ledger {
	return &Ledger{store: store}
}

// Dir returns the root directory l was opened with, or "" for a Ledger
// opened via OpenStore against a non-git-directory store.
func (l *Ledger) Dir() string { return l.dir }

// Exists reports whether l has ever actually been used -- distinct from
// "this ledger has no record of address X" (FoldState's own found=false),
// which never errors and never tells you whether the ledger itself was
// ever initialized at all. core/resolver (UBI-27) needs this distinction
// for a cross-stack pin against an empty/missing neighbor ledger_dir: "no
// ledger here at all" and "a real ledger that's simply never recorded
// this address" are different, both-real scenarios, not the same "not
// found."
func (l *Ledger) Exists() bool {
	exists, err := l.store.Exists(context.Background())
	return err == nil && exists
}

var (
	// ErrProposalNotFound means no ledger entry exists for the given ID.
	ErrProposalNotFound = errors.New("proposal not found in ledger")

	// ErrCorruptLedgerEntry means a ledger file exists but its contents
	// aren't a well-formed proposal record (truncated write, disk
	// corruption, hand-editing, ...).
	ErrCorruptLedgerEntry = errors.New("corrupt ledger entry")

	// ErrCorruptLedgerHead means the ledger's own head pointer exists but
	// doesn't contain a well-formed hash.
	ErrCorruptLedgerHead = errors.New("corrupt ledger head")

	// ErrParentMismatch means a proposal's Parent doesn't match the
	// ledger's current head — it was resolved against a stale or wrong
	// chain position.
	ErrParentMismatch = errors.New("proposal parent does not match ledger head")

	// ErrDuplicateProposal means a proposal with this ID already exists in
	// the ledger. Ledger entries are immutable once written.
	ErrDuplicateProposal = errors.New("proposal already exists in ledger")
)

// ledgerLockTTL is the TTL a remote LedgerStore's own distributed lock
// uses to decide a lock is abandoned (docs/architecture.md — "Locking: a
// TTL, not a PID, is the staleness signal"); the git-directory store
// ignores it entirely, since it has a strictly better signal available
// (process liveness). A package var, not a constant, so a live
// conformance test can shrink it instead of waiting out a real,
// multi-minute-scale TTL.
var ledgerLockTTL = 2 * time.Minute

// Head returns the current ledger head hash, or "" if the ledger has no
// accepted proposals yet (genesis state).
func (l *Ledger) Head() (string, error) {
	return l.store.Head(context.Background())
}

// Append writes p — which must already have ID/Status/Acceptance set, see
// Accept — to the ledger and advances the head to p.ID. It refuses to
// append if p.Parent doesn't match the current head, and refuses to
// overwrite an existing entry for the same ID (ledger entries are
// immutable once written).
//
// A proposal object that already exists is only treated as a genuine,
// already-accepted duplicate if the head already reflects it (this ID is
// the current head, or the head has moved on past it via some other
// proposal). If the object exists but the head still names this
// proposal's own Parent as current, Append resumes rather than refuses --
// a real gap found and closed in UBI-32 Arc B: a crash between writing
// the proposal object and advancing the head, before this fix, left no
// path to recovery at all (a retry of the identical Append would have
// reported ErrDuplicateProposal for a proposal that was never actually
// accepted). See docs/architecture.md's own "A real gap found while
// building this" and docs/ledgerstore-adversarial.md's rows 4-5.
func (l *Ledger) Append(p *Proposal) error {
	if p.ID == "" {
		return errors.New("append: proposal has no ID (compute and set it first, e.g. via Accept)")
	}
	ctx := context.Background()

	// Locked for the whole check-then-write sequence below, not just the
	// final write -- two concurrent Accept calls that both read the same
	// head before either writes would otherwise both think they're
	// building on the current head and race to append (UBI-20 workstream
	// 4, docs/architecture.md — Ledger lock). scan/why/status never call
	// Append, so they never contend for this.
	release, err := l.store.Lock(ctx, ledgerLockTTL)
	if err != nil {
		return fmt.Errorf("append: %w", err)
	}
	defer release(ctx)

	existing, readErr := l.store.ReadProposal(ctx, p.ID)
	switch {
	case readErr == nil:
		// The object already exists -- resolved below, once we know
		// whether the head already reflects it.
	case errors.Is(readErr, ErrStoreObjectNotFound):
		existing = nil
	default:
		return fmt.Errorf("append: %w", readErr)
	}

	head, err := l.store.Head(ctx)
	if err != nil {
		return fmt.Errorf("append: %w", err)
	}

	if existing != nil {
		if head != p.Parent {
			// Either already fully accepted (head is this ID, or has
			// moved past it via some other proposal) -- a genuine
			// duplicate either way.
			return fmt.Errorf("append: %w: %s", ErrDuplicateProposal, p.ID)
		}
		// Interrupted append: the object landed, the head never advanced.
		// Confirm the existing bytes really do parse before completing
		// the resume -- a corrupt object here is a real corruption
		// finding, never silently papered over.
		var existingProp Proposal
		if err := json.Unmarshal(existing, &existingProp); err != nil {
			return fmt.Errorf("append: %w: %v", ErrCorruptLedgerEntry, err)
		}
		if err := l.store.AdvanceHead(ctx, head, p.ID); err != nil {
			return fmt.Errorf("append: %w", err)
		}
		return nil
	}

	if p.Parent != head {
		return fmt.Errorf("append: %w: proposal parent %q, ledger head %q", ErrParentMismatch, p.Parent, head)
	}

	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("append: marshal proposal: %w", err)
	}
	if err := l.store.WriteProposalIfAbsent(ctx, p.ID, b); err != nil {
		return fmt.Errorf("append: %w", err)
	}
	if err := l.store.AdvanceHead(ctx, head, p.ID); err != nil {
		return fmt.Errorf("append: %w", err)
	}
	return nil
}

// Read loads one proposal record by ID. A missing file reports
// ErrProposalNotFound; a truncated or otherwise malformed file reports
// ErrCorruptLedgerEntry — never a panic or a silently-wrong partial value.
func (l *Ledger) Read(id string) (*Proposal, error) {
	b, err := l.store.ReadProposal(context.Background(), id)
	if errors.Is(err, ErrStoreObjectNotFound) {
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
