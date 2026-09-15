package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// TestShipSummary_TotalDistinguishesParallelFromSerial is the whole
// reason the total is printed.
//
// Each row shows that resource's OWN duration, measured from its first
// progress event. That event only fires once the scheduler starts the
// resource, and the scheduler only starts it once its dependencies have
// applied. Both facts are correct and together they are not enough: two
// resources that each took a minute render identically whether they ran
// at the same time or one after the other.
//
// Measured against the real executor before this was written. An
// independent pair and a dependent pair, both resources delayed equally:
//
//	independent pair    rows: 310ms, 310ms   wall clock 330ms
//	dependent pair      rows: 310ms, 310ms   wall clock 640ms
//
// Identical rows, double the wall clock. The total was the one number
// that told them apart and the one number not on screen.
func TestShipSummary_TotalDistinguishesParallelFromSerial(t *testing.T) {
	report := func(started, finished string) string {
		var buf bytes.Buffer
		printShipReport(&buf, &styler{}, &core.ApplyRecord{
			Resources: []*core.ResourceApply{{}, {}},
			Summary: &core.ApplySummary{
				Outcome:          "applied",
				StartedAt:        started,
				FinishedAt:       finished,
				ResourcesApplied: 2,
			},
		})
		return buf.String()
	}

	// Two resources that each took about a minute. The rows would look the
	// same either way; the summaries must not.
	parallel := report("2026-09-14T10:00:00Z", "2026-09-14T10:01:04Z")
	serial := report("2026-09-14T10:00:00Z", "2026-09-14T10:02:07Z")

	if !strings.Contains(parallel, "total 1:04") {
		t.Errorf("a parallel pair should report its real wall clock:\n%s", parallel)
	}
	if !strings.Contains(serial, "total 2:07") {
		t.Errorf("a serial pair should report its real wall clock:\n%s", serial)
	}
	if parallel == serial {
		t.Error("the two runs must not produce identical summaries: telling them apart is the point")
	}
}

// TestShipTotalElapsed_OmittedRatherThanGuessed: a summary that has lost
// a detail is recoverable, a summary carrying a wrong number is not. Each
// of these would otherwise render as a confident "0:00" or a negative
// duration.
func TestShipTotalElapsed_OmittedRatherThanGuessed(t *testing.T) {
	for name, sum := range map[string]*core.ApplySummary{
		"no summary at all": nil,
		"no timestamps":     {},
		"only a start":      {StartedAt: "2026-09-14T10:00:00Z"},
		"only a finish":     {FinishedAt: "2026-09-14T10:00:00Z"},
		"unparseable":       {StartedAt: "yesterday", FinishedAt: "today"},
		"finished before it started": {
			StartedAt:  "2026-09-14T10:02:00Z",
			FinishedAt: "2026-09-14T10:00:00Z",
		},
	} {
		if got := shipTotalElapsed(sum); got != "" {
			t.Errorf("%s: got %q, want no total rather than a made-up one", name, got)
		}
	}

	// And the ordinary case still works, or the above would pass by
	// always returning "".
	if got := shipTotalElapsed(&core.ApplySummary{
		StartedAt:  "2026-09-14T10:00:00Z",
		FinishedAt: "2026-09-14T10:02:07Z",
	}); got != "2:07" {
		t.Errorf("got %q, want 2:07", got)
	}
}

// TestRenderElapsed_OneSpellingForRowsAndTotal: the total is only useful
// held against the rows, so the two must be formatted by the same code.
// Two formatters would eventually disagree about a boundary and the
// comparison would quietly stop working.
func TestRenderElapsed_OneSpellingForRowsAndTotal(t *testing.T) {
	for _, tc := range []struct {
		secs int
		want string
	}{{0, "0:00"}, {9, "0:09"}, {59, "0:59"}, {60, "1:00"}, {64, "1:04"}, {127, "2:07"}, {3600, "60:00"}} {
		if got := renderElapsed(time.Duration(tc.secs) * time.Second); got != tc.want {
			t.Errorf("renderElapsed(%ds) = %q, want %q", tc.secs, got, tc.want)
		}
	}
}
