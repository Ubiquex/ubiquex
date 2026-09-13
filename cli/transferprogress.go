package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// transferprogress.go draws the one-line bar an OCI push or pull shows
// while bytes move, and the single line it collapses to afterwards.
//
// One line, redrawn with `\r\x1b[2K`, rather than ship.go's own
// multi-row ANSI cursor addressing. ship tracks N concurrent resources
// and needs to address rows individually; a transfer is one thing
// happening once, so the row arithmetic that makes ship's printer
// delicate buys nothing here.
//
// It draws only on a real terminal. Off a TTY the bar is not degraded
// to something plainer, it is omitted: a progress bar's whole content
// is the redraw, and a log file full of carriage returns or repeated
// percentage lines is worse than no bar. The completion line still
// prints, so a non-interactive run records what moved and how long it
// took.
//
// It is also omitted for a transfer that never happens. Pulling from a
// local directory copies files and returns; a bar that appears and
// completes in the same frame is noise pretending to be feedback, so
// the caller only ever builds one for a path that really transfers.

// transferBar renders one transfer's progress.
type transferBar struct {
	out     io.Writer
	st      *styler
	tty     bool
	width   int
	label   string
	started time.Time

	mu         sync.Mutex
	lastDraw   time.Time
	drawn      bool
	transfer   int64
	total      int64
	finished   bool
	lastRateAt time.Time
	lastRateBy int64
	rate       float64
}

// newTransferBar builds a bar for a transfer that is about to start.
// label names what is moving, e.g. "pushing widget-bp.tar.gz".
func newTransferBar(out io.Writer, st *styler, tty bool, width int, label string, now time.Time) *transferBar {
	return &transferBar{
		out:        out,
		st:         st,
		tty:        tty,
		width:      width,
		label:      label,
		started:    now,
		lastRateAt: now,
	}
}

// minRedraw bounds how often the bar repaints. A blob arrives in many
// small reads, and repainting on every one spends more time writing
// escape codes than transferring.
const minRedraw = 80 * time.Millisecond

// Update is the blueprint.TransferProgress callback. Safe to call from
// any goroutine, which oras requires: it copies from its own workers.
func (b *transferBar) Update(transferred, total int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return
	}
	b.transfer, b.total = transferred, total

	now := time.Now()
	if elapsed := now.Sub(b.lastRateAt); elapsed >= 500*time.Millisecond {
		b.rate = float64(transferred-b.lastRateBy) / elapsed.Seconds()
		b.lastRateAt, b.lastRateBy = now, transferred
	}
	if !b.tty || now.Sub(b.lastDraw) < minRedraw {
		return
	}
	b.lastDraw = now
	b.draw()
}

// draw paints the running bar. Caller holds the lock.
func (b *transferBar) draw() {
	bar := b.renderBar()
	line := fmt.Sprintf("  %s  %s  %s",
		b.st.Dim(b.label),
		bar,
		b.st.Dim(b.progressText()))
	if b.width > 0 && len(stripANSI(line)) > b.width {
		// Never wrap. A wrapped line leaves the cursor on a row the
		// next `\r` cannot reach, so the redraw would start stacking
		// copies of itself down the terminal.
		line = truncateVisible(line, b.width)
	}
	fmt.Fprint(b.out, "\r\x1b[2K"+line)
	b.drawn = true
}

// renderBar is the bracketed bar itself, or an empty string while the
// total is still unknown. A pull learns the size from the manifest,
// which arrives after the copy starts, so there is a real moment with
// bytes moving and no denominator. Showing a full-width empty bar then
// would imply zero progress rather than an unknown fraction.
func (b *transferBar) renderBar() string {
	const cells = 24
	if b.total <= 0 {
		return b.st.Dim(strings.Repeat("·", cells))
	}
	frac := float64(b.transfer) / float64(b.total)
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * cells)
	return b.st.Dim("[") + b.st.Green(strings.Repeat("=", filled)) +
		strings.Repeat(" ", cells-filled) + b.st.Dim("]")
}

// progressText is the numbers beside the bar: how much of how much, the
// percentage, and the current rate.
func (b *transferBar) progressText() string {
	var parts []string
	if b.total > 0 {
		parts = append(parts, fmt.Sprintf("%s / %s", humanBytes(b.transfer), humanBytes(b.total)),
			fmt.Sprintf("%d%%", int(float64(b.transfer)/float64(b.total)*100)))
	} else {
		parts = append(parts, humanBytes(b.transfer))
	}
	if b.rate > 0 {
		parts = append(parts, humanBytes(int64(b.rate))+"/s")
	}
	return strings.Join(parts, "  ")
}

// Done erases the running bar and returns the one line summarising the
// transfer: the total moved and how long it took.
//
// Returned rather than printed, so the caller can place it among the
// other details of the receipt it belongs to. Printing it directly put
// an indented transfer line ABOVE the unindented "pushed ..." line it
// was detail for, which reads as two unrelated events.
//
// Empty when nothing moved, which is a transfer that was skipped
// entirely because the registry already had every blob.
func (b *transferBar) Done(now time.Time) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finished = true
	b.clear()
	if b.transfer == 0 {
		return ""
	}
	return b.st.Yellow(humanBytes(b.transfer)) + " " + b.st.Dim("in "+humanDuration(now.Sub(b.started)))
}

// Fail collapses the bar to where the transfer stopped. The numbers are
// the point: "failed after 1.2 MB of 3.4 MB" says whether a connection
// died early or a registry rejected the last step, and the error alone
// usually does not.
func (b *transferBar) Fail(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finished = true
	b.clear()
	// A failure before the transfer started has nothing to report about
	// it. "stopped after 0 B" reads as a transfer problem, and the real
	// error is something else entirely: a bad reference, an unreadable
	// tarball, a rejected credential. The error itself says that better.
	if b.transfer == 0 {
		return
	}
	text := fmt.Sprintf("stopped after %s", humanBytes(b.transfer))
	if b.total > 0 {
		text = fmt.Sprintf("stopped after %s of %s", humanBytes(b.transfer), humanBytes(b.total))
	}
	fmt.Fprintf(b.out, "    %s %s\n", b.st.Red(text), b.st.Dim("in "+humanDuration(now.Sub(b.started))))
}

// clear erases the running bar's own line so the collapsed line takes
// its place rather than appearing under a frozen final frame. A no-op
// when nothing was ever drawn, which is every non-TTY run.
func (b *transferBar) clear() {
	if b.drawn {
		fmt.Fprint(b.out, "\r\x1b[2K")
		b.drawn = false
	}
}

// humanBytes renders a byte count the way a person reads one. Binary
// units, since that is what a registry and a filesystem both report.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// humanDuration renders elapsed time short: sub-minute in seconds with
// one decimal, longer in m:ss.
func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

// stripANSI returns text's own visible length by dropping escape
// sequences, so a styled line can be measured against a terminal width.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			j := i + 1
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// truncateVisible cuts a styled line to at most width VISIBLE
// characters, keeping every escape sequence it passes so the line stays
// correctly colored and correctly terminated.
func truncateVisible(s string, width int) string {
	var b strings.Builder
	visible := 0
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			j := i + 1
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				j++
			}
			b.WriteString(s[i:j])
			i = j
			continue
		}
		if visible >= width {
			break
		}
		b.WriteByte(s[i])
		visible++
		i++
	}
	return b.String() + ansiReset
}
