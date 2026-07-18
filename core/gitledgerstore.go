package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// gitLedgerStore is the reference LedgerStore implementation: today's
// exact in-repo directory layout and behavior (docs/schema.md's Ledger
// layout), moved here verbatim from what were core.Ledger's own direct
// os.* calls before UBI-32 Arc B -- zero behavior change, proved by the
// full pre-existing test suite passing unmodified against it. Every path
// function below is byte-for-byte the same layout as before extraction:
//
//	<dir>/
//	  ledger/proposals/<id>.prop.json
//	  ledger/applies/<id>.attempt-<n>.apply.json
//	  .ubx/ledger.lock   (head pointer)
//	  .ubx/lock          (concurrency lock)
//	  .ubx/salt          (redaction salt)
type gitLedgerStore struct {
	dir string
}

func newGitLedgerStore(dir string) *gitLedgerStore { return &gitLedgerStore{dir: dir} }

func (g *gitLedgerStore) proposalsDir() string { return filepath.Join(g.dir, "ledger", "proposals") }
func (g *gitLedgerStore) proposalPath(id string) string {
	return filepath.Join(g.proposalsDir(), id+".prop.json")
}
func (g *gitLedgerStore) headPath() string     { return filepath.Join(g.dir, ".ubx", "ledger.lock") }
func (g *gitLedgerStore) lockFilePath() string { return filepath.Join(g.dir, ".ubx", "lock") }
func (g *gitLedgerStore) saltPath() string     { return filepath.Join(g.dir, ".ubx", "salt") }
func (g *gitLedgerStore) appliesDir() string   { return filepath.Join(g.dir, "ledger", "applies") }
func (g *gitLedgerStore) applyPath(proposalID string, attempt int64) string {
	return filepath.Join(g.appliesDir(), fmt.Sprintf("%s.attempt-%d.apply.json", proposalID, attempt))
}

var applyFilenamePattern = regexp.MustCompile(`^([0-9a-f]{64})\.attempt-([0-9]+)\.apply\.json$`)

func (g *gitLedgerStore) Exists(ctx context.Context) (bool, error) {
	_, err := os.Stat(filepath.Join(g.dir, "ledger"))
	return err == nil, nil
}

func (g *gitLedgerStore) ReadProposal(ctx context.Context, id string) ([]byte, error) {
	b, err := os.ReadFile(g.proposalPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrStoreObjectNotFound
	}
	return b, err
}

func (g *gitLedgerStore) WriteProposalIfAbsent(ctx context.Context, id string, data []byte) error {
	path := g.proposalPath(id)
	if _, err := os.Stat(path); err == nil {
		return ErrStoreObjectExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(g.proposalsDir(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (g *gitLedgerStore) Head(ctx context.Context) (string, error) {
	b, err := os.ReadFile(g.headPath())
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
	if !IsHash(head) {
		return "", fmt.Errorf("%w: %q is not a 64-character hex hash", ErrCorruptLedgerHead, head)
	}
	return head, nil
}

// AdvanceHead re-verifies expectedPrev against the current head before
// writing -- defense in depth, matching the interface's own documented
// CAS contract, even though git-local's caller (core.Ledger.Append)
// already holds the lock for the whole check-then-write sequence and so
// never actually races here.
func (g *gitLedgerStore) AdvanceHead(ctx context.Context, expectedPrev, newHead string) error {
	current, err := g.Head(ctx)
	if err != nil {
		return err
	}
	if current != expectedPrev {
		return fmt.Errorf("%w: expected %q, found %q", ErrStoreHeadMoved, expectedPrev, current)
	}
	if err := os.MkdirAll(filepath.Dir(g.headPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(g.headPath(), []byte(newHead+"\n"), 0o644)
}

func (g *gitLedgerStore) ReadApply(ctx context.Context, proposalID string, attempt int64) ([]byte, error) {
	b, err := os.ReadFile(g.applyPath(proposalID, attempt))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrStoreObjectNotFound
	}
	return b, err
}

// WriteApply writes to a temp file in the same directory, fsyncs, then
// atomically renames -- so a crash mid-write never leaves a half-written,
// ambiguously-corrupt file in the record's real place (docs/executor.md's
// own THE invariant: a state transition must be durable before the risky
// operation it precedes is attempted).
func (g *gitLedgerStore) WriteApply(ctx context.Context, proposalID string, attempt int64, data []byte) error {
	if err := os.MkdirAll(g.appliesDir(), 0o755); err != nil {
		return fmt.Errorf("write apply record: %w", err)
	}
	tmp, err := os.CreateTemp(g.appliesDir(), ".apply-*.tmp")
	if err != nil {
		return fmt.Errorf("write apply record: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once successfully renamed below
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write apply record: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write apply record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write apply record: %w", err)
	}
	if err := os.Rename(tmpPath, g.applyPath(proposalID, attempt)); err != nil {
		return fmt.Errorf("write apply record: %w", err)
	}
	return nil
}

func (g *gitLedgerStore) ListApplyAttempts(ctx context.Context, proposalID string) ([]int64, error) {
	entries, err := os.ReadDir(g.appliesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var attempts []int64
	for _, e := range entries {
		m := applyFilenamePattern.FindStringSubmatch(e.Name())
		if m == nil || m[1] != proposalID {
			continue
		}
		n, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			continue
		}
		attempts = append(attempts, n)
	}
	return attempts, nil
}

// ErrLedgerLocked means another process currently holds the ledger lock
// and didn't release it within the wait window (UBI-20 workstream 4,
// docs/architecture.md — Ledger lock). This is real contention between two
// legitimate, cooperating processes -- not evidence anything is broken.
var ErrLedgerLocked = errors.New("ledger is locked by another process")

// ErrStaleLedgerLock means the lock file names a PID that is no longer
// running -- the holder was killed or crashed without releasing it. This
// is reported, never silently broken: an operator must remove the file
// themselves (recovery guidance is in the error text) after confirming
// it's genuinely safe to do so, since Append refuses to guess.
var ErrStaleLedgerLock = errors.New("stale ledger lock: holder is no longer running")

// lockWaitTimeout/lockRetryInterval are package vars, not constants, so
// tests can shrink them instead of a multi-second sleep for every
// contention test (same convention as cli's configSearchStartDir override).
var (
	lockWaitTimeout   = 3 * time.Second
	lockRetryInterval = 20 * time.Millisecond
)

// Lock is a simple, cross-process PID-file lock around ledger-mutating
// operations. Unlike a bare OS flock(2), a PID file lets a blocked caller
// tell a genuinely still-running holder apart from a killed one that
// never got to clean up after itself. ttl is ignored -- a single machine
// can always ask "is the holder's process still alive" directly, a
// strictly better signal than any TTL.
//
// Blocks up to lockWaitTimeout (retrying every lockRetryInterval) if the
// lock is held by a still-running process, then returns ErrLedgerLocked.
// Returns ErrStaleLedgerLock immediately, without waiting out the full
// timeout, if the holder's PID isn't running -- there's nothing to wait
// for once that's confirmed.
func (g *gitLedgerStore) Lock(ctx context.Context, ttl time.Duration) (release func(context.Context) error, err error) {
	path := g.lockFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("acquire ledger lock: %w", err)
	}

	deadline := time.Now().Add(lockWaitTimeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			if closeErr := f.Close(); closeErr != nil {
				return nil, fmt.Errorf("acquire ledger lock: %w", closeErr)
			}
			return func(context.Context) error { return os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("acquire ledger lock: %w", err)
		}

		holderPID, readErr := readLockPID(path)
		switch {
		case readErr != nil:
			// The file exists but couldn't be read (removed between the
			// exclusive-create failing and this read, or genuinely
			// corrupt) -- either way, not evidence of live contention;
			// retry the create rather than failing on a transient blip.
		case !processRunning(holderPID):
			return nil, fmt.Errorf("%w: %s names pid %d, which is not running -- remove %s to recover (confirm no ubx process actually holds it first)",
				ErrStaleLedgerLock, path, holderPID, path)
		}

		if time.Now().After(deadline) {
			if readErr == nil {
				return nil, fmt.Errorf("%w: %s (held by pid %d)", ErrLedgerLocked, path, holderPID)
			}
			return nil, fmt.Errorf("%w: %s", ErrLedgerLocked, path)
		}
		time.Sleep(lockRetryInterval)
	}
}

// readLockPID reads and parses the PID a lock file at path names.
func readLockPID(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("lock file %s: %w", path, err)
	}
	return pid, nil
}

// processRunning reports whether pid is a live process on this machine.
// Sending signal 0 changes nothing about the target process -- it's pure
// existence-and-permission probing, the standard Unix idiom for this
// (os.FindProcess itself always succeeds on Unix regardless of whether
// the PID exists; the real check only happens on Signal).
func processRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

func (g *gitLedgerStore) ReadSalt(ctx context.Context) ([]byte, bool, error) {
	b, err := os.ReadFile(g.saltPath())
	if err == nil {
		return b, true, nil
	}
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return nil, false, err
}

// WriteSaltIfAbsent uses an exclusive create so two racing first-uses
// can't both "win" and silently clobber each other's salt -- a real,
// if previously untested, race the pre-extraction code (plain
// ReadFile-then-WriteFile) left open; closed here for both stores at
// once, never exercised by any existing test so never a behavior change
// to anything previously relied upon.
func (g *gitLedgerStore) WriteSaltIfAbsent(ctx context.Context, salt []byte) error {
	path := g.saltPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return ErrStoreObjectExists
		}
		return err
	}
	if _, err := f.Write(salt); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return ensureGitignored(g.dir, ".ubx/salt")
}
