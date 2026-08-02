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
	printer, finish := newProgressPrinter(&buf, plainStyler(), true, nil)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "in_flight"})

	time.Sleep(tickInterval + 300*time.Millisecond)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "applied"})
	finish()

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
	if !strings.Contains(out, "shipped") {
		t.Fatalf("expected the terminal \"shipped\" line to still print, got:\n%s", out)
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
	printer, finish := newProgressPrinter(&buf, plainStyler(), false, nil)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "in_flight"})

	time.Sleep(tickInterval + 300*time.Millisecond)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "applied"})
	finish()

	out := buf.String()
	if strings.Contains(out, "in_flight") || strings.Contains(out, "shipping") {
		t.Fatalf("expected nothing rendered for the pending/in_flight/shipping phase on a non-TTY run, got:\n%s", out)
	}
	if !strings.Contains(out, "shipped") {
		t.Fatalf("expected the terminal \"shipped\" line to still print, got:\n%s", out)
	}
}

// TestNewProgressPrinter_TicksUpdateInPlace_NotAppendedPerTick is UBI-83's
// own regression test for the CORE noise-reduction defect UBI-75's fix
// left unaddressed: every tick of a live "shipping" wait must redraw ONE
// terminal row in place (real ANSI cursor-clear-and-rewrite bytes), never
// append a brand-new line -- a real 26s SQS create was producing 20+
// near-duplicate lines before this fix. The proof is a raw newline COUNT,
// per the ticket's own explicit instruction ("count actual printed
// newlines, don't just eyeball it") -- NOT a substring check, since every
// redraw's own bytes necessarily still appear in the raw captured stream
// (that's what makes a real terminal emulator able to render them in
// place at all); what must NOT appear is an extra literal "\n" between
// ticks. Five real ticks fire in this run (5.3s); if the fix regressed
// back to one Fprintln per tick, this would assert a count of ~1 against
// an actual count of 5+ and fail loudly.
func TestNewProgressPrinter_TicksUpdateInPlace_NotAppendedPerTick(t *testing.T) {
	var buf bytes.Buffer
	printer, finish := newProgressPrinter(&buf, plainStyler(), true, nil)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "in_flight"})
	time.Sleep(5*tickInterval + 300*time.Millisecond)
	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "applied"})
	finish()

	out := buf.String()
	if got := strings.Count(out, "\n"); got != 1 {
		t.Fatalf("UBI-83: expected exactly 1 real newline for a single resource's whole shipping->shipped lifecycle (every tick redraws in place, only the terminal state seals the row), got %d newlines in:\n%q", got, out)
	}
	if got := strings.Count(out, "\x1b[2K"); got < 5 {
		t.Fatalf("UBI-83: expected at least 5 in-place line-clear redraws (one per real tick), got %d -- the ticker isn't redrawing in place at all:\n%q", got, out)
	}
	// The spinner must actually animate (UBI-83's own exact spec: "an
	// actual animated spinner", not the old static "·") -- at least two
	// DISTINCT frames from spinnerFrames must appear across the 5 ticks.
	distinct := map[string]bool{}
	for _, f := range spinnerFrames {
		if strings.Contains(out, f) {
			distinct[f] = true
		}
	}
	if len(distinct) < 2 {
		t.Fatalf("UBI-83: expected the spinner to animate across multiple distinct frames, got only %d distinct frame(s) in:\n%q", len(distinct), out)
	}
}

// TestNewProgressPrinter_ConcurrentResources_EachTicksIndependently is
// UBI-67's own regression test for the printer's pre-UBI-67 assumption
// ("at most one resource is ever in_flight/verifying at a time" -- its
// own former doc comment said so directly): two DIFFERENT addresses'
// own in_flight tickers now run concurrently, each redrawing its OWN row
// in place (UBI-83) every tickInterval, and must never collide, deadlock,
// corrupt each other's own row, or lose either address's own final
// "applied" line -- the exact shape a real concurrent `ubx ship` batch
// (UBI-67's own scheduler) produces. Drives the printer directly, from
// two goroutines, mirroring how executor.WithProgress's own callback is
// genuinely invoked from N concurrent node goroutines today.
func TestNewProgressPrinter_ConcurrentResources_EachTicksIndependently(t *testing.T) {
	var mu sync.Mutex
	var buf bytes.Buffer
	printer, finish := newProgressPrinter(&safeWriter{mu: &mu, w: &buf}, plainStyler(), true, nil)

	addrA, addrB := "fake_widget.a", "fake_widget.b"

	var wg sync.WaitGroup
	for _, addr := range []string{addrA, addrB} {
		addr := addr
		wg.Add(1)
		go func() {
			defer wg.Done()
			printer(executor.ProgressEvent{Address: addr, Kind: "transition", State: "in_flight"})
			time.Sleep(3*tickInterval + 300*time.Millisecond)
			printer(executor.ProgressEvent{Address: addr, Kind: "transition", State: "applied"})
		}()
	}
	wg.Wait()
	finish()

	out := buf.String()
	for _, addr := range []string{addrA, addrB} {
		if !strings.Contains(out, addr+":") {
			t.Errorf("expected every line for %s to be prefixed with its own address, got:\n%s", addr, out)
		}
	}
	if strings.Count(out, "shipping") < 2 {
		t.Errorf("expected both addresses' own ticker to have fired at least once (2+ \"shipping\" appearances total across every redraw), got:\n%s", out)
	}
	if strings.Count(out, "shipped") != 2 {
		t.Errorf("expected exactly one final \"shipped\" appearance per address (2 total -- each address's own terminal state is written exactly once, never redrawn again), got %d:\n%s", strings.Count(out, "shipped"), out)
	}
	// UBI-83/UBI-84: total real newlines must stay small and INDEPENDENT
	// of tick count (3 ticks/address here vs. TestNewProgressPrinter_
	// TicksUpdateInPlace_NotAppendedPerTick's 5 -- same 2-row shape, same
	// newline count either way) -- exactly one row transition each for:
	// B's row created (sealing whichever of A/B was still open -- UBI-84
	// dropped the blank-line spacer between DIFFERENT resources' own
	// rows that used to add a second newline here), and B's own final
	// seal once it's the bottom row (whichever address finishes last is
	// the one that self-seals; the other was already implicitly sealed
	// the moment the second row was created). 2 newlines total,
	// regardless of which goroutine wins the race to create/finish first.
	if got := strings.Count(out, "\n"); got != 2 {
		t.Errorf("UBI-83/UBI-84: expected exactly 2 real newlines for this 2-resource run (row-transition count, not tick count, zero blank-line spacers), got %d in:\n%q", got, out)
	}
}

// TestNewProgressPrinter_UBI84_NoBlankLinesColumnsAligned is UBI-84's own
// regression test for its second and third findings, driven directly
// (sequential, no concurrency -- that's TestNewProgressPrinter_
// ConcurrentResources_EachTicksIndependently's own job) against a batch
// of deliberately VARYING-length addresses, matching the ticket's own
// verification instruction ("a 5-resource batch with varying address
// lengths, confirming visual alignment, not just correct values"): four
// resources, address lengths 1/2/9/23, colors disabled (plainStyler) so
// the raw column math is directly comparable across rows.
func TestNewProgressPrinter_UBI84_NoBlankLinesColumnsAligned(t *testing.T) {
	addrs := []string{"a", "bb", "ccccccccc", "dddddddddddddddddddddd"}
	kinds := map[string]resourceOpKind{}
	for _, a := range addrs {
		kinds[a] = opCreate
	}

	var buf bytes.Buffer
	printer, finish := newProgressPrinter(&buf, plainStyler(), true, kinds)
	for _, a := range addrs {
		printer(executor.ProgressEvent{Address: a, Kind: "transition", State: "applied"})
	}
	finish()

	out := buf.String()

	// Finding 2: zero blank lines between resource rows -- a blank line
	// would show up as two consecutive newlines with nothing between.
	if strings.Contains(out, "\n\n") {
		t.Fatalf("UBI-84: expected zero blank lines between resource rows, got:\n%q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(addrs) {
		t.Fatalf("expected exactly %d lines (one per resource, no extras), got %d:\n%q", len(addrs), len(lines), out)
	}

	// Finding 3: every row's own ": " (ending the padded address column)
	// starts at the IDENTICAL rune index -- the longest address (23
	// chars) sets the shared column width every shorter address must be
	// padded out to.
	wantColon := -1
	for i, line := range lines {
		idx := strings.Index(line, ": ")
		if idx < 0 {
			t.Fatalf("line %d has no \": \" column at all: %q", i, line)
		}
		if wantColon == -1 {
			wantColon = idx
		} else if idx != wantColon {
			t.Fatalf("UBI-84: expected every row's \": \" column to align at rune index %d (set by the longest address), line %d has it at %d instead:\n%q", wantColon, i, idx, out)
		}
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
