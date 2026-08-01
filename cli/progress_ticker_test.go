package cli

import (
	"bytes"
	"strings"
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
