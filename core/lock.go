package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

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

func (l *Ledger) lockFilePath() string { return filepath.Join(l.dir, ".ubx", "lock") }

// acquireLedgerLock is a simple, cross-process PID-file lock around
// ledger-mutating operations (Append, via Accept/AcceptFromMerge) --
// scan/why/status never call this, since they never write
// (docs/architecture.md — Ledger lock: "only around ledger-mutating
// operations"). Unlike a bare OS flock(2), a PID file lets a blocked
// caller tell a genuinely still-running holder apart from a killed one
// that never got to clean up after itself -- exactly the distinction
// "stale lock from a killed process" (UBI-20's own adversarial case)
// needs, and a bare flock's silent, kernel-level release on process death
// would paper over rather than surface.
//
// Blocks up to lockWaitTimeout (retrying every lockRetryInterval) if the
// lock is held by a still-running process, then returns ErrLedgerLocked.
// Returns ErrStaleLedgerLock immediately, without waiting out the full
// timeout, if the holder's PID isn't running -- there's nothing to wait
// for once that's confirmed.
func (l *Ledger) acquireLedgerLock() (release func() error, err error) {
	path := l.lockFilePath()
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
			return func() error { return os.Remove(path) }, nil
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
