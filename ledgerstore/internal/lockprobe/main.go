// Command lockprobe is a small, real subprocess used only by
// ledgerstore's own live conformance tests (UBI-32 Arc B) to exercise
// lock contention, TTL-expiry reclaim, and CAS races against a real
// remote store as genuinely separate OS processes -- not goroutines
// inside one process, which could never actually prove a distributed
// lock/CAS works across independent clients the way a real deployment
// would use it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ubiquex/ubiquex/ledgerstore"
)

func main() {
	storeURI := flag.String("store", "", "store URI to open")
	action := flag.String("action", "", "lock | lock-and-crash | advance-head")
	holdMillis := flag.Int("hold-ms", 0, "milliseconds to hold the lock before releasing (action=lock)")
	ttlMillis := flag.Int("ttl-ms", 0, "lock TTL in milliseconds")
	prevHead := flag.String("prev", "", "expected previous head (action=advance-head)")
	newHead := flag.String("new", "", "new head to advance to (action=advance-head)")
	flag.Parse()

	ctx := context.Background()
	store, closeFn, err := ledgerstore.Open(ctx, *storeURI)
	if err != nil {
		fmt.Println("ERROR_OPEN:", err)
		os.Exit(2)
	}
	defer closeFn()

	switch *action {
	case "lock":
		release, err := store.Lock(ctx, time.Duration(*ttlMillis)*time.Millisecond)
		if err != nil {
			fmt.Println("LOCK_FAILED:", err)
			os.Exit(1)
		}
		fmt.Println("LOCK_ACQUIRED")
		time.Sleep(time.Duration(*holdMillis) * time.Millisecond)
		if err := release(ctx); err != nil {
			fmt.Println("RELEASE_FAILED:", err)
			os.Exit(1)
		}
		fmt.Println("LOCK_RELEASED")
	case "lock-and-crash":
		// Acquires and exits immediately without releasing -- simulates a
		// holder that crashed before it could clean up after itself. The
		// lock object is left behind on real S3 with its own TTL; only
		// that TTL, never a process-liveness check, can ever reclaim it
		// (docs/architecture.md -- "Locking: a TTL, not a PID").
		_, err := store.Lock(ctx, time.Duration(*ttlMillis)*time.Millisecond)
		if err != nil {
			fmt.Println("LOCK_FAILED:", err)
			os.Exit(1)
		}
		fmt.Println("LOCK_ACQUIRED_THEN_CRASHED")
		os.Exit(0)
	case "advance-head":
		if err := store.AdvanceHead(ctx, *prevHead, *newHead); err != nil {
			fmt.Println("ADVANCE_FAILED:", err)
			os.Exit(1)
		}
		fmt.Println("ADVANCE_OK")
	default:
		fmt.Println("unknown -action:", *action)
		os.Exit(2)
	}
}
