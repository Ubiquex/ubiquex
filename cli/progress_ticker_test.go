package cli

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core/executor"
)

// TestNewProgressPrinter_TicksDuringLongShippingWait is UBI-63 bug 3's own
// regression test for the ticking-timer half, updated for UBI-70's own
// noise cut: an ordinary transition to in_flight (now rendered under the
// "shipping" label, UBI-61 comment thread's rename) with no reconcile loop
// of its own must never sit frozen at "0:00" while a real, slow provider
// call is in flight -- a live ticker re-renders the one "shipping" line in
// place at least once before the terminal event ever arrives. UBI-70 also
// means the in_flight transition itself no longer prints anything at
// all -- the ticker is the ONLY source of a visible line during this
// phase, and "in_flight" must never appear anywhere in the output. This
// drives newProgressPrinter's own callback directly (tty forced true)
// rather than through a real `ubx ship` invocation, since the printer's
// own internal ticker is what's under test, not the executor around it.
func TestNewProgressPrinter_TicksDuringLongShippingWait(t *testing.T) {
	var buf bytes.Buffer
	printer := newProgressPrinter(&buf, plainStyler(), true, nil)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "in_flight"})

	time.Sleep(tickInterval + 300*time.Millisecond)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "applied"})

	out := buf.String()

	if strings.Contains(out, "in_flight") {
		t.Fatalf("UBI-70: pending/in_flight must never render their own line, got:\n%s", out)
	}
	if !strings.Contains(out, "shipping") {
		t.Fatalf("expected the live \"shipping\" ticker to have rendered at least once, got:\n%s", out)
	}
	if !strings.Contains(out, "0:01") {
		t.Fatalf("expected the ticked line to show real elapsed time past 0:00 before the terminal event, got:\n%s", out)
	}
	if !strings.Contains(out, "applied") {
		t.Fatalf("expected the terminal \"applied\" line to still print, got:\n%s", out)
	}
}

// TestNewProgressPrinter_NonTTY_NeverTicks is this session's own
// companion: the pre-UBI-70 ticker started unconditionally, regardless of
// tty -- on a piped/logged run that meant a fresh, near-duplicate
// "in_flight"/"shipping" line flooding the log every tickInterval for as
// long as a slow provider call ran, since in-place overwrite (what the
// ticker is FOR) is a terminal-only concept a log file has no use for.
// Fixed alongside UBI-70's own noise cut: the ticker is TTY-only now, so a
// non-TTY run during the exact same long wait renders nothing at all for
// this phase -- no in_flight, no shipping, no ticked line -- only the
// eventual terminal transition.
func TestNewProgressPrinter_NonTTY_NeverTicks(t *testing.T) {
	var buf bytes.Buffer
	printer := newProgressPrinter(&buf, plainStyler(), false, nil)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "in_flight"})

	time.Sleep(tickInterval + 300*time.Millisecond)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "applied"})

	out := buf.String()
	if strings.Contains(out, "in_flight") || strings.Contains(out, "shipping") {
		t.Fatalf("expected nothing rendered for the pending/in_flight/shipping phase on a non-TTY run, got:\n%s", out)
	}
	if !strings.Contains(out, "applied") {
		t.Fatalf("expected the terminal \"applied\" line to still print, got:\n%s", out)
	}
}

// TestNewProgressPrinter_ConcurrentResources_EachTicksIndependently is
// UBI-67's own regression test for the printer's pre-UBI-67 assumption
// ("at most one resource is ever in_flight/verifying at a time" -- its
// own former doc comment said so directly): two DIFFERENT addresses'
// own in_flight tickers now run concurrently, each emitting its own
// discrete, address-prefixed line every tickInterval, and must never
// collide, deadlock, or lose either address's own final "applied" line
// -- the exact shape a real concurrent `ubx ship` batch (UBI-67's own
// scheduler) produces. Drives the printer directly, from two goroutines,
// mirroring how executor.WithProgress's own callback is genuinely
// invoked from N concurrent node goroutines today.
func TestNewProgressPrinter_ConcurrentResources_EachTicksIndependently(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	printer := newProgressPrinter(&safeWriter{mu: &mu, w: &buf}, plainStyler(), true, nil)

	addrA, addrB := "fake_widget.a", "fake_widget.b"

	var wg sync.WaitGroup
	for _, addr := range []string{addrA, addrB} {
		addr := addr
		wg.Add(1)
		go func() {
			defer wg.Done()
			printer(executor.ProgressEvent{Address: addr, Kind: "transition", State: "in_flight"})
			time.Sleep(tickInterval + 300*time.Millisecond)
			printer(executor.ProgressEvent{Address: addr, Kind: "transition", State: "applied"})
		}()
	}
	wg.Wait()

	out := buf.String()
	for _, addr := range []string{addrA, addrB} {
		if !strings.Contains(out, addr+":") {
			t.Errorf("expected every line for %s to be prefixed with its own address, got:\n%s", addr, out)
		}
	}
	if strings.Count(out, "shipping") < 2 {
		t.Errorf("expected both addresses' own ticker to have fired at least once (2+ \"shipping\" lines total), got:\n%s", out)
	}
	if strings.Count(out, "applied") != 2 {
		t.Errorf("expected exactly one \"applied\" line per address (2 total), got %d:\n%s", strings.Count(out, "applied"), out)
	}
}

// safeWriter serializes concurrent Write calls -- bytes.Buffer itself
// is not safe for concurrent use, and this test's own two goroutines
// both write through the same printer's own io.Writer (the printer
// serializes its OWN internal state via its own mutex, but two
// goroutines each holding that lock in turn still both reach this
// io.Writer, one after another -- never truly concurrently, but go
// test -race checks the underlying io.Writer implementation itself has
// no assumption of single-goroutine use baked in, so this wrapper keeps
// the test honestly race-clean without relying on that).
type safeWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (s *safeWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
