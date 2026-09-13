package cli

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func styled() *styler { return &styler{enabled: true} }

// Off a TTY the bar is omitted entirely rather than degraded, because a
// log full of carriage returns is worse than no bar. The completion
// line still prints, so a non-interactive run still records what moved.
func TestTransferBar_NonTTYDrawsNothingButStillCompletes(t *testing.T) {
	var buf bytes.Buffer
	start := time.Now()
	b := newTransferBar(&buf, plainStyler(), false, 80, "pushing bp.tar.gz", start)

	for i := 1; i <= 10; i++ {
		b.Update(int64(i)*100_000, 1_000_000)
	}
	if buf.Len() != 0 {
		t.Fatalf("a non-TTY run drew a bar: %q", buf.String())
	}

	summary := b.Done(start.Add(2 * time.Second))
	if summary == "" {
		t.Fatal("no transfer summary was produced")
	}
	fmt.Fprintf(&buf, "    %s\n", summary)
	out := buf.String()
	if strings.Contains(out, "\r") || strings.Contains(out, "\x1b[2K") {
		t.Errorf("the completion line carries redraw control codes: %q", out)
	}
	if !strings.Contains(out, "976.6 KB") || !strings.Contains(out, "2.0s") {
		t.Errorf("completion line missing the total or the elapsed time: %q", out)
	}
}

// On a TTY the running bar redraws in place, and the completion line
// replaces it rather than appearing beneath a frozen final frame.
func TestTransferBar_TTYRedrawsInPlaceAndCollapses(t *testing.T) {
	var buf bytes.Buffer
	start := time.Now()
	b := newTransferBar(&buf, styled(), true, 120, "pushing bp.tar.gz", start)

	b.Update(500_000, 1_000_000)
	if !strings.Contains(buf.String(), "\r\x1b[2K") {
		t.Fatalf("the running bar does not redraw in place: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "50%") {
		t.Errorf("the running bar does not show a percentage: %q", buf.String())
	}

	buf.Reset()
	summary := b.Done(start.Add(1500 * time.Millisecond))
	if !strings.HasPrefix(buf.String(), "\r\x1b[2K") {
		t.Errorf("Done does not erase the running bar: %q", buf.String())
	}
	if !strings.Contains(summary, "1.5s") {
		t.Errorf("summary missing elapsed time: %q", summary)
	}
	// Returned, not printed: the caller places it among the details of
	// the receipt it belongs to.
	if strings.Contains(buf.String(), "1.5s") {
		t.Errorf("Done printed the summary instead of returning it: %q", buf.String())
	}
}

// A failure says where it stopped. The numbers are the diagnosis: an
// error alone does not distinguish a connection dying early from a
// registry rejecting the last step.
func TestTransferBar_FailureSaysWhereItStopped(t *testing.T) {
	var buf bytes.Buffer
	start := time.Now()
	b := newTransferBar(&buf, plainStyler(), false, 80, "pulling oci://x/y:v1", start)
	b.Update(300_000, 1_000_000)
	b.Fail(start.Add(time.Second))

	out := buf.String()
	if !strings.Contains(out, "stopped after 293.0 KB of 976.6 KB") {
		t.Errorf("failure does not say where it stopped: %q", out)
	}
}

// A pull does not know its total until the manifest names the layer, so
// there is a real moment with bytes moving and no denominator. It must
// not render a percentage it cannot compute.
func TestTransferBar_UnknownTotalShowsNoPercentage(t *testing.T) {
	var buf bytes.Buffer
	b := newTransferBar(&buf, styled(), true, 120, "pulling oci://x/y:v1", time.Now())
	b.Update(50_000, 0)

	out := buf.String()
	if strings.Contains(out, "%") {
		t.Errorf("a percentage was rendered with no total: %q", out)
	}
	if !strings.Contains(out, "48.8 KB") {
		t.Errorf("bytes moved are not shown: %q", out)
	}
}

// Nothing the bar draws may exceed the terminal width. A wrapped line
// leaves the cursor on a row the next carriage return cannot reach, so
// the redraw would stack copies of itself down the terminal.
func TestTransferBar_NeverExceedsTerminalWidth(t *testing.T) {
	var buf bytes.Buffer
	b := newTransferBar(&buf, styled(), true, 40, "pulling oci://a-very-long-registry-host/org/blueprint:v1.2.3", time.Now())
	b.Update(500_000, 1_000_000)

	line := strings.TrimPrefix(buf.String(), "\r\x1b[2K")
	if got := len(stripANSI(line)); got > 40 {
		t.Errorf("drew %d visible columns into a 40-column terminal: %q", got, line)
	}
}

func TestHumanBytes(t *testing.T) {
	for _, c := range []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{3 * 1024 * 1024 * 1024, "3.0 GB"},
	} {
		if got := humanBytes(c.in); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	if got := humanDuration(4100 * time.Millisecond); got != "4.1s" {
		t.Errorf("got %q, want 4.1s", got)
	}
	if got := humanDuration(125 * time.Second); got != "2:05" {
		t.Errorf("got %q, want 2:05", got)
	}
}

// truncateVisible has to keep escape sequences it passes and terminate
// the line, or a truncated bar leaks its color into everything printed
// after it.
func TestTruncateVisible_KeepsStylingAndResets(t *testing.T) {
	in := "\x1b[32mgreen text here\x1b[0m and more"
	out := truncateVisible(in, 5)
	if got := stripANSI(out); got != "green" {
		t.Errorf("visible text = %q, want %q", got, "green")
	}
	if !strings.HasSuffix(out, ansiReset) {
		t.Errorf("truncated line does not reset: %q", out)
	}
	if !strings.Contains(out, "\x1b[32m") {
		t.Errorf("truncated line dropped its color: %q", out)
	}
}

// A failure before any byte moved says nothing about the transfer. The
// real error is a bad reference or a rejected credential, and "stopped
// after 0 B" would point at the wrong thing.
func TestTransferBar_FailureBeforeAnyBytesIsSilent(t *testing.T) {
	var buf bytes.Buffer
	b := newTransferBar(&buf, plainStyler(), false, 80, "pushing bp.tar.gz", time.Now())
	b.Fail(time.Now())
	if buf.Len() != 0 {
		t.Errorf("a failure with nothing transferred still printed: %q", buf.String())
	}
}

// oras copies from its own goroutines, so Update has to be safe from
// any of them. Run under -race this is the only thing that proves it.
func TestTransferBar_ConcurrentUpdatesAreSafe(t *testing.T) {
	var buf bytes.Buffer
	b := newTransferBar(&buf, styled(), true, 120, "pushing bp.tar.gz", time.Now())

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 1; j <= 50; j++ {
				b.Update(int64(n*j)*1000, 1_000_000)
			}
		}(i)
	}
	wg.Wait()
	b.Done(time.Now())
}
