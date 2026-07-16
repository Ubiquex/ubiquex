package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// withShortLockTimeouts shrinks the lock's wait window for the duration of
// one test, instead of a multi-second sleep for every contention test --
// same convention as cli's configSearchStartDir override.
func withShortLockTimeouts(t *testing.T) {
	t.Helper()
	origTimeout, origRetry := lockWaitTimeout, lockRetryInterval
	lockWaitTimeout = 200 * time.Millisecond
	lockRetryInterval = 10 * time.Millisecond
	t.Cleanup(func() { lockWaitTimeout, lockRetryInterval = origTimeout, origRetry })
}

func TestAcquireLedgerLock_AcquireThenRelease(t *testing.T) {
	l := Open(t.TempDir())

	release, err := l.acquireLedgerLock()
	if err != nil {
		t.Fatalf("acquireLedgerLock: %v", err)
	}
	if _, statErr := os.Stat(l.lockFilePath()); statErr != nil {
		t.Fatalf("expected lock file to exist while held: %v", statErr)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, statErr := os.Stat(l.lockFilePath()); !os.IsNotExist(statErr) {
		t.Fatalf("expected lock file removed after release, stat err = %v", statErr)
	}

	// Re-acquiring after release must succeed -- the file is genuinely
	// gone, not just conceptually "available."
	release2, err := l.acquireLedgerLock()
	if err != nil {
		t.Fatalf("second acquireLedgerLock: %v", err)
	}
	release2()
}

// TestAcquireLedgerLock_HeldByLiveProcess_TimesOut is the "two concurrent
// accepts" adversarial case (UBI-20 workstream 4): a lock genuinely held
// by a still-running process (our own test process's real PID stands in)
// must be waited out, then reported clearly -- never silently broken.
func TestAcquireLedgerLock_HeldByLiveProcess_TimesOut(t *testing.T) {
	withShortLockTimeouts(t)
	l := Open(t.TempDir())

	if err := os.MkdirAll(filepath.Dir(l.lockFilePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.lockFilePath(), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err := l.acquireLedgerLock()
	elapsed := time.Since(start)

	if !errors.Is(err, ErrLedgerLocked) {
		t.Fatalf("got %v, want ErrLedgerLocked", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Errorf("expected the holder's pid named in the error, got: %v", err)
	}
	if elapsed < lockWaitTimeout {
		t.Errorf("returned after %v, before the %v wait window elapsed -- contention must be waited out, not failed immediately", elapsed, lockWaitTimeout)
	}
}

// TestAcquireLedgerLock_StaleFromKilledProcess is UBI-20's other named
// adversarial case: a lock file naming a PID that has already exited
// (killed, crashed, or otherwise never reached its own release) must be
// detected and reported with explicit recovery guidance, immediately --
// not waited out for the full contention timeout, since there is nothing
// to wait for once the holder is confirmed dead.
func TestAcquireLedgerLock_StaleFromKilledProcess(t *testing.T) {
	withShortLockTimeouts(t)
	l := Open(t.TempDir())

	// A short-lived subprocess whose PID is guaranteed to have exited by
	// the time Wait() returns -- a real "killed process," not a made-up
	// PID number that might collide with something on the host.
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run throwaway subprocess: %v", err)
	}
	deadPID := cmd.Process.Pid

	if err := os.MkdirAll(filepath.Dir(l.lockFilePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.lockFilePath(), []byte(strconv.Itoa(deadPID)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err := l.acquireLedgerLock()
	elapsed := time.Since(start)

	if !errors.Is(err, ErrStaleLedgerLock) {
		t.Fatalf("got %v, want ErrStaleLedgerLock", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(deadPID)) {
		t.Errorf("expected the dead pid named in the error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "remove") || !strings.Contains(err.Error(), l.lockFilePath()) {
		t.Errorf("expected recovery guidance naming the lock file, got: %v", err)
	}
	if elapsed >= lockWaitTimeout {
		t.Errorf("took %v -- a confirmed-stale lock should be reported immediately, not waited out like live contention", elapsed)
	}

	// Confirmed stale, but NOT auto-removed -- the file must still be
	// there; recovery is an explicit, deliberate operator action.
	if _, statErr := os.Stat(l.lockFilePath()); statErr != nil {
		t.Errorf("a stale lock must not be silently removed by the failed acquirer: %v", statErr)
	}
}

// TestLedgerAppend_TwoConcurrentAccepts is UBI-20's "two concurrent
// accepts" case end to end, through Accept/Append rather than the lock
// primitive directly: two proposals racing to append against the same
// ledger must serialize, not corrupt the chain -- exactly one succeeds
// with parent "", the other must see the moved head and fail with
// ErrParentMismatch, never a torn/duplicated ledger.lock head file.
func TestLedgerAppend_TwoConcurrentAccepts(t *testing.T) {
	ledgerDir := t.TempDir()
	l := Open(ledgerDir)

	newDraft := func(summary string) *Proposal {
		return &Proposal{
			SchemaVersion: SchemaVersion,
			Stack:         "payments",
			Parent:        "",
			Kind:          KindChange,
			Intent:        Intent{Summary: summary},
			Resolution:    Resolution{ResolvedAt: "2026-07-16T00:00:00Z"},
			BlastRadius:   BlastRadius{Creates: 1},
			Status:        StatusDraft,
		}
	}

	var wg sync.WaitGroup
	results := make([]error, 2)
	proposals := []*Proposal{newDraft("first"), newDraft("second")}
	for i := range proposals {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Accept(l, proposals[i])
			results[i] = err
		}(i)
	}
	wg.Wait()

	successes, mismatches := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrParentMismatch):
			mismatches++
		default:
			t.Errorf("unexpected error from a concurrent Accept: %v", err)
		}
	}
	if successes != 1 || mismatches != 1 {
		t.Fatalf("expected exactly one success and one ErrParentMismatch, got %d successes and %d mismatches (results: %v)", successes, mismatches, results)
	}

	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if len(head) != 64 {
		t.Fatalf("ledger head is not a well-formed hash after concurrent appends: %q", head)
	}
}

// TestRunScan_NotBlockedByHeldLedgerLock is UBI-20's "accept-during-scan"
// case: scan/why/status never call Append, so a ledger lock held by an
// in-progress accept must never block or slow down a concurrent scan.
func TestRunScan_NotBlockedByHeldLedgerLock(t *testing.T) {
	ledgerDir := t.TempDir()
	l := Open(ledgerDir)

	release, err := l.acquireLedgerLock()
	if err != nil {
		t.Fatalf("acquireLedgerLock: %v", err)
	}
	defer release()

	fp := &fakeProvider{state: json.RawMessage(`{"id":"x"}`)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)}); err != nil {
			t.Errorf("RunScan while ledger lock is held: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("RunScan did not complete promptly while the ledger lock was held -- scan must never contend for it")
	}
}
