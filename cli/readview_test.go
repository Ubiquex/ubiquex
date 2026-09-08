package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRelativeTime_Buckets(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		ago  time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{60 * time.Second, "a minute ago"},
		{9 * time.Minute, "9 minutes ago"},
		{90 * time.Minute, "an hour ago"},
		{5 * time.Hour, "5 hours ago"},
		{30 * time.Hour, "yesterday"},
		{5 * 24 * time.Hour, "5 days ago"},
		{40 * 24 * time.Hour, "a month ago"},
		{400 * 24 * time.Hour, "a year ago"},
	}
	for _, c := range cases {
		ts := now.Add(-c.ago).Format(time.RFC3339)
		if got := relativeTime(ts, now); got != c.want {
			t.Errorf("relativeTime(%s ago) = %q, want %q", c.ago, got, c.want)
		}
	}
}

// A malformed timestamp is shown, not swallowed. A ledger holding one is
// telling you something, and rendering a plausible "just now" over it
// would hide exactly the anomaly worth seeing.
func TestRelativeTime_MalformedPassesThrough(t *testing.T) {
	if got := relativeTime("not-a-time", time.Now()); got != "not-a-time" {
		t.Errorf("got %q, want the input verbatim", got)
	}
}

// Clock skew, or a doctored ledger, must not render as "-3 minutes ago".
func TestRelativeTime_FutureIsCalledOut(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	ts := now.Add(time.Hour).Format(time.RFC3339)
	if got := relativeTime(ts, now); !strings.Contains(got, "in the future") {
		t.Errorf("got %q, want it called out as a future timestamp", got)
	}
}

func TestFlattenAttrs_DottedLeaves(t *testing.T) {
	raw := []byte(`{"env":"prod","nested":{"a":1,"b":[true,null]},"empty":{},"none":[]}`)
	got := map[string]string{}
	flattenRaw("tags", raw, got)
	want := map[string]string{
		"tags.env":         `"prod"`,
		"tags.nested.a":    "1",
		"tags.nested.b[0]": "true",
		"tags.nested.b[1]": "null",
		"tags.empty":       "{}",
		"tags.none":        "[]",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d leaves, want %d: %v", len(got), len(want), got)
	}
}

// The quoting is what tells a reader a string from a number, so it has
// to survive.
func TestFlattenAttrs_QuotingDistinguishesTypes(t *testing.T) {
	got := map[string]string{}
	flattenRaw("v", []byte(`{"s":"1","n":1}`), got)
	if got["v.s"] != `"1"` || got["v.n"] != "1" {
		t.Errorf(`got s=%q n=%q, want "1" and 1`, got["v.s"], got["v.n"])
	}
}

// The case docs/cli-output-spec.md exists for: a string that itself
// contains JSON is a policy document, and it renders as a readable block
// rather than an escaped single line. Flattening treated it as an
// ordinary scalar and produced exactly the escaped form the spec
// forbids, which is why renderConfigAttr discriminates on the decoded
// type rather than flattening everything.
func TestRenderConfigAttr_EmbeddedJSONStringStaysABlock(t *testing.T) {
	var buf bytes.Buffer
	policy, _ := json.Marshal(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow"}]}`)
	renderConfigAttr(&buf, plainStyler(), "  ", "policy", policy)
	out := buf.String()
	if strings.Contains(out, `\"Version\"`) {
		t.Fatalf("an embedded JSON string rendered escaped, which the spec forbids:\n%s", out)
	}
	if !strings.Contains(out, `"Version": "2012-10-17"`) {
		t.Fatalf("expected the decoded policy formatted, got:\n%s", out)
	}
}

func TestRenderConfigAttr_ObjectFlattens(t *testing.T) {
	var buf bytes.Buffer
	renderConfigAttr(&buf, plainStyler(), "  ", "tags", []byte(`{"env":"prod","team":"billing"}`))
	out := buf.String()
	if !strings.Contains(out, `tags.env: "prod"`) || !strings.Contains(out, `tags.team: "billing"`) {
		t.Fatalf("expected flattened leaves, got:\n%s", out)
	}
	if strings.Contains(out, "{") {
		t.Fatalf("expected no JSON block for a plain object, got:\n%s", out)
	}
}

// Every ordinary ship writes pending, in_flight and shipped inside one
// second, so printing a line each printed the same timestamp three times
// and said nothing.
func TestCollapseTransitions_SameSecondCollapses(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Minute).Format(time.RFC3339)
	lines := collapseTransitions(plainStyler(), []transitionView{
		{State: "pending", At: at},
		{State: "in_flight", At: at},
		{State: "applied", At: at, OK: true},
	}, "", now)
	if len(lines) != 1 {
		t.Fatalf("expected one collapsed line, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "shipped") || !strings.Contains(lines[0], "a minute ago") {
		t.Errorf("got %q, want the terminal state and a relative time", lines[0])
	}
}

// A transition carrying a real Detail is where a retry or a provider
// message lives, and those are worth their own lines.
func TestCollapseTransitions_DetailKeepsEveryStep(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Minute).Format(time.RFC3339)
	lines := collapseTransitions(plainStyler(), []transitionView{
		{State: "pending", At: at},
		{State: "unknown_post_timeout", At: at, Detail: "verifying via a read-back"},
		{State: "applied", At: at, OK: true},
	}, "destroyed", now)
	if len(lines) != 3 {
		t.Fatalf("expected every step when one carries a detail, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[2], "(destroyed)") {
		t.Errorf("expected the terminal outcome annotated, got %q", lines[2])
	}
}

// Structure is dim, hashes yellow, values red, the approver blue. A
// disabled styler emits none of it, which is what NO_COLOR and a non-TTY
// both resolve to.
func TestReadViewPalette_PlainStylerEmitsNoANSI(t *testing.T) {
	st := plainStyler()
	line := readGroup(st, st.Hash("abc123"), st.Approver("roozbeh"), st.Dim("just now"))
	if strings.Contains(line, "\x1b[") {
		t.Fatalf("a disabled styler emitted ANSI: %q", line)
	}
	var buf bytes.Buffer
	writeAttrs(&buf, st, "  ", map[string]string{"a": "1"})
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("a disabled styler emitted ANSI for attributes: %q", buf.String())
	}
}

func TestReadViewPalette_EnabledUsesTheRatifiedColors(t *testing.T) {
	st := &styler{enabled: true}
	if !strings.Contains(st.Hash("abc123"), ansiYellow) {
		t.Error("a hash should render yellow")
	}
	if !strings.Contains(st.Approver("roozbeh"), ansiBlue) {
		t.Error("an approver should render blue")
	}
	attr := st.Attr("tags.env", `"prod"`)
	if !strings.Contains(attr, ansiYellow) || !strings.Contains(attr, ansiRed) {
		t.Errorf("an attribute should render a yellow name and a red value, got %q", attr)
	}
}
