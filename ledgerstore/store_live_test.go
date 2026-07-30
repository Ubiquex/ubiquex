package ledgerstore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// liveBucketURI is the real, already-existing S3 bucket prior live
// sessions in this codebase have used for exactly this kind of
// verification (cli/attribution_live_test.go's own payments.aws_s3_bucket.
// ubx-states) -- reused rather than provisioning a new bucket, since
// nothing about this arc's own conformance needs a dedicated one. Region
// pinned explicitly (confirmed via `aws s3api get-bucket-location`
// rather than assumed) rather than left to driver auto-detection.
const liveBucketURI = "s3://ubx-states?region=us-east-1"

var lockprobeBinary string

// requireLedgerStoreLive gates every test in this file behind the same
// env var every other real-cloud-touching test in this codebase uses.
func requireLedgerStoreLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_CONFORMANCE_LIVE") != "1" {
		t.Skip("skipping: set UBX_CONFORMANCE_LIVE=1 to run LedgerStore conformance against real S3")
	}
}

// buildLockprobe compiles ./internal/lockprobe once per test binary run
// (same convention as provider/client_test.go's own fakeProviderBinary),
// so this file's own concurrency tests can spawn it as a genuinely
// separate OS process -- goroutines inside one process could never
// actually prove a distributed lock/CAS works the way independent
// clients hitting the real store would use it.
func buildLockprobe(t *testing.T) string {
	t.Helper()
	if lockprobeBinary != "" {
		return lockprobeBinary
	}
	// Deliberately NOT t.TempDir(): that's removed at the end of the
	// single test that created it, but this binary is cached in a
	// package-level var and reused by every other test in this file's
	// own run. os.MkdirTemp's directory simply outlives the process --
	// left behind in the OS temp dir, same as any other `go build`
	// scratch output.
	dir, err := os.MkdirTemp("", "ubx-ledgerstore-lockprobe")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	bin := filepath.Join(dir, "lockprobe")
	build := exec.Command("go", "build", "-o", bin, "./internal/lockprobe")
	build.Dir = "."
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("building lockprobe: %v\n%s", err, out)
	}
	lockprobeBinary = bin
	return bin
}

// liveTestPrefix returns a fresh, unique base store URI under
// liveBucketURI for one test -- never shared across tests or runs, and
// cleaned up via t.Cleanup so a failed run doesn't leave permanent
// clutter in the real bucket.
func liveTestPrefix(t *testing.T) string {
	t.Helper()
	unique := fmt.Sprintf("ledgerstore-conformance/%d-%s", time.Now().UnixNano(), strings.ReplaceAll(t.Name(), "/", "-"))
	storeURI, err := WithStack(liveBucketURI, unique)
	if err != nil {
		t.Fatalf("WithStack: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		store, closeFn, err := Open(ctx, storeURI)
		if err != nil {
			t.Logf("cleanup: open: %v", err)
			return
		}
		defer closeFn()
		iter := store.bucket.List(nil)
		for {
			obj, err := iter.Next(ctx)
			if err != nil {
				break
			}
			if delErr := store.bucket.Delete(ctx, obj.Key); delErr != nil {
				t.Logf("cleanup: delete %s: %v", obj.Key, delErr)
			}
		}
	})
	return storeURI
}

// TestLive_S3_BasicRoundTrip proves the real driver, not just memblob,
// end to end: Accept/Append/Read/Head and an apply record, through
// core.Ledger exactly as any real command would use it.
func TestLive_S3_BasicRoundTrip(t *testing.T) {
	requireLedgerStoreLive(t)
	ctx := context.Background()
	storeURI := liveTestPrefix(t)

	store, closeFn, err := Open(ctx, storeURI)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer closeFn()

	l := core.OpenStore(store)
	p := sampleProposal()
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != accepted.ID {
		t.Fatalf("head = %q, want %q", head, accepted.ID)
	}
	got, err := l.Read(accepted.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Intent.Summary != p.Intent.Summary {
		t.Fatalf("read back summary = %q, want %q", got.Intent.Summary, p.Intent.Summary)
	}

	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("BeginApply: %v", err)
	}
	if _, err := l.SealApply(rec, core.ApplySummary{Outcome: "applied"}); err != nil {
		t.Fatalf("SealApply: %v", err)
	}
	attempts, err := l.ApplyAttempts(accepted.ID)
	if err != nil {
		t.Fatalf("ApplyAttempts: %v", err)
	}
	if len(attempts) != 1 || !attempts[0].Sealed() {
		t.Fatalf("got %d attempts, want exactly 1 sealed one", len(attempts))
	}
}

// TestLive_S3_CASRace_RealConcurrentProcesses is docs/ledgerstore-
// adversarial.md's row 1, against real S3, as two genuinely independent
// OS processes rather than goroutines -- the actual claim being tested is
// that S3's own native conditional write (If-None-Match) decides the
// winner, not anything cooperating in-process code arranges.
func TestLive_S3_CASRace_RealConcurrentProcesses(t *testing.T) {
	requireLedgerStoreLive(t)
	bin := buildLockprobe(t)
	storeURI := liveTestPrefix(t)

	headA := strings.Repeat("a", 64)
	headB := strings.Repeat("b", 64)

	run := func(newHead string) *exec.Cmd {
		return exec.Command(bin, "-store", storeURI, "-action", "advance-head", "-prev", "", "-new", newHead)
	}
	cmdA, cmdB := run(headA), run(headB)
	var outA, outB []byte
	var errA, errB error
	// Launch both as close to simultaneously as practical.
	doneCh := make(chan struct{}, 2)
	go func() { outA, errA = cmdA.CombinedOutput(); doneCh <- struct{}{} }()
	go func() { outB, errB = cmdB.CombinedOutput(); doneCh <- struct{}{} }()
	<-doneCh
	<-doneCh

	aOK := errA == nil && strings.Contains(string(outA), "ADVANCE_OK")
	bOK := errB == nil && strings.Contains(string(outB), "ADVANCE_OK")
	if aOK == bOK {
		t.Fatalf("expected exactly one of the two real processes to win the CAS race, got A_ok=%v (out=%s) B_ok=%v (out=%s)", aOK, outA, bOK, outB)
	}

	ctx := context.Background()
	store, closeFn, err := Open(ctx, storeURI)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer closeFn()
	head, err := store.Head(ctx)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head != headA && head != headB {
		t.Fatalf("head = %q, want either %q or %q", head, headA, headB)
	}
}

// TestLive_S3_LockContention_RealConcurrentProcesses is row 2: one real
// process holds the lock for a real, measurable duration; a second real
// process must block until the first releases, never both proceeding
// unsynchronized.
func TestLive_S3_LockContention_RealConcurrentProcesses(t *testing.T) {
	requireLedgerStoreLive(t)
	bin := buildLockprobe(t)
	storeURI := liveTestPrefix(t)

	holdMillis := 3000
	first := exec.Command(bin, "-store", storeURI, "-action", "lock", "-ttl-ms", "60000", "-hold-ms", strconv.Itoa(holdMillis))
	if err := first.Start(); err != nil {
		t.Fatalf("start first: %v", err)
	}
	// Give the first process a real head start to actually acquire
	// before the second even tries.
	time.Sleep(500 * time.Millisecond)

	start := time.Now()
	second := exec.Command(bin, "-store", storeURI, "-action", "lock", "-ttl-ms", "60000", "-hold-ms", "0")
	secondOut, secondErr := second.CombinedOutput()
	elapsed := time.Since(start)

	firstErr := first.Wait()
	if firstErr != nil {
		t.Fatalf("first process: %v", firstErr)
	}
	if secondErr != nil {
		t.Fatalf("second process: %v\n%s", secondErr, secondOut)
	}
	if !strings.Contains(string(secondOut), "LOCK_ACQUIRED") {
		t.Fatalf("second process never acquired the lock: %s", secondOut)
	}
	// It must have waited out roughly the remainder of the first
	// process's own hold -- not returned near-instantly, which would mean
	// it never actually contended at all.
	if elapsed < time.Duration(holdMillis-600)*time.Millisecond {
		t.Errorf("second process acquired after only %v, want it to have waited for the first's real hold (~%dms)", elapsed, holdMillis)
	}
}

// TestLive_S3_LockTTLExpiry_RealReclaim is row 3: a real process acquires
// a short-TTL lock and exits without releasing (a real crash simulation,
// not a mocked one), and a second real process must still be able to
// acquire once that TTL has genuinely elapsed -- there is no
// process-liveness signal available across two real, independent OS
// processes (potentially on different machines in a real deployment), so
// only the TTL itself can ever justify the reclaim.
func TestLive_S3_LockTTLExpiry_RealReclaim(t *testing.T) {
	requireLedgerStoreLive(t)
	bin := buildLockprobe(t)
	storeURI := liveTestPrefix(t)

	const ttlMillis = 2000
	crashed := exec.Command(bin, "-store", storeURI, "-action", "lock-and-crash", "-ttl-ms", strconv.Itoa(ttlMillis))
	out, err := crashed.CombinedOutput()
	if err != nil {
		t.Fatalf("lock-and-crash: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "LOCK_ACQUIRED_THEN_CRASHED") {
		t.Fatalf("expected the crash simulation to have acquired first, got: %s", out)
	}

	// A second real process's Lock call blocks and retries on its own --
	// there's no separate "check if it's expired yet" step to call out of
	// band, so the proof that this is genuine TTL-based reclaim (not an
	// instant, meaningless success) is timing: it must take roughly the
	// crashed lock's own TTL to succeed, never return near-instantly.
	start := time.Now()
	reclaim := exec.Command(bin, "-store", storeURI, "-action", "lock", "-ttl-ms", "60000", "-hold-ms", "0")
	reclaimOut, reclaimErr := reclaim.CombinedOutput()
	elapsed := time.Since(start)
	if reclaimErr != nil {
		t.Fatalf("expected the expired lock to eventually be reclaimed, got: %v\n%s", reclaimErr, reclaimOut)
	}
	if !strings.Contains(string(reclaimOut), "LOCK_ACQUIRED") {
		t.Fatalf("expected LOCK_ACQUIRED after real TTL expiry, got: %s", reclaimOut)
	}
	if elapsed < (ttlMillis-500)*time.Millisecond {
		t.Errorf("reclaimed after only %v, want it to have waited out roughly the %dms TTL first -- an instant success here would mean this proved nothing about TTL expiry", elapsed, ttlMillis)
	}
}
