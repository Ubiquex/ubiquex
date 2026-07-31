package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core/executor"
)

// TestNewProgressPrinter_TicksDuringLongInFlightWait is UBI-63 bug 3's
// own regression test for the ticking-timer half: an ordinary
// transition to in_flight with no reconcile loop of its own used to
// print exactly one line, frozen at "0:00", until the eventual terminal
// event arrived with the real elapsed time. It must instead re-render a
// live elapsed-time line at least once while nothing else is
// happening, superseded cleanly by the terminal transition once it
// fires. This drives newProgressPrinter's own callback directly rather
// than through a real `ubx ship` invocation, since the printer's own
// internal ticker is what's under test, not the executor around it.
func TestNewProgressPrinter_TicksDuringLongInFlightWait(t *testing.T) {
	var buf bytes.Buffer
	printer := newProgressPrinter(&buf, plainStyler(), false)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "in_flight"})

	time.Sleep(tickInterval + 300*time.Millisecond)

	printer(executor.ProgressEvent{Address: "fake_widget.widget1", Kind: "transition", State: "applied"})

	out := buf.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	// The initial in_flight transition itself always prints one line at
	// elapsed 0:00 -- that's unchanged, expected behavior. The bug this
	// test guards against is that line staying frozen at 0:00 forever;
	// the fix is a SECOND in_flight line, from the ticker, showing real
	// elapsed time (0:01) before the terminal event ever arrives.
	inFlightLines := 0
	tickedPastZero := false
	for _, l := range lines {
		if strings.Contains(l, "in_flight") {
			inFlightLines++
			if !strings.Contains(l, "0:00") {
				tickedPastZero = true
			}
		}
	}
	if inFlightLines < 2 {
		t.Fatalf("expected at least 2 in_flight lines (the initial transition plus at least one tick), got %d in output:\n%s", inFlightLines, out)
	}
	if !tickedPastZero {
		t.Fatalf("expected a ticked in_flight line showing real elapsed time past 0:00, still frozen, got:\n%s", out)
	}
	if !strings.Contains(out, "applied") {
		t.Fatalf("expected the terminal \"applied\" line to still print, got:\n%s", out)
	}
}
