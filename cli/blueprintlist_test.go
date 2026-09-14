package cli

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

// columnOf reports where sub starts in COLUMNS, not bytes.
//
// The distinction is the whole point of the alignment this file tests:
// "…" is three bytes and one column, so a byte-offset check reports a
// correctly aligned table as two columns out from the hash onward. The
// first version of this test did exactly that.
func columnOf(line, sub string) int {
	i := strings.Index(line, sub)
	if i < 0 {
		return -1
	}
	return utf8.RuneCountInString(line[:i])
}

func plainRows(t *testing.T, rows []blueprintListRow, termWidth int) []string {
	t.Helper()
	var buf bytes.Buffer
	writeBlueprintTable(&buf, &styler{}, rows, termWidth)
	return strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
}

// TestBlueprintTable_ColumnsAlign is the property the table exists for.
//
// Every column has to start at the same offset on every line, including
// the header. A truncated hash ends in "…", one column wide and three
// bytes long, so byte-counted padding bent every row two columns left of
// the header. Found by rendering a real cache.
func TestBlueprintTable_ColumnsAlign(t *testing.T) {
	rows := []blueprintListRow{
		{Name: "ci-platform", Tag: "v3", Hash: "sha256:a1b2c…", Size: "12.4 KB", Used: "2h old", Source: "oci://ghcr.io/acme/ci-platform"},
		{Name: "a", Tag: "v10.2.1-rc1", Hash: "sha256:ffff0…", Size: "3 B", Used: "unknown", Source: "git+https://github.com/acme/x"},
	}
	lines := plainRows(t, rows, 0)
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got %d lines: %q", len(lines), lines)
	}

	// Column starts are derived from the header, then required of every
	// row. Comparing rows to each other would pass if all of them were
	// wrong in the same way.
	header := lines[0]
	var starts []int
	for _, col := range []string{"NAME", "TAG", "HASH", "SIZE", "USED", "SOURCE"} {
		i := columnOf(header, col)
		if i < 0 {
			t.Fatalf("header is missing %s: %q", col, header)
		}
		starts = append(starts, i)
	}
	for _, line := range lines[1:] {
		fields := []string{"ci-platform", "v3", "sha256:", "12.4 KB", "2h old", "oci://"}
		if strings.HasPrefix(line, "a ") {
			fields = []string{"a", "v10.2.1-rc1", "sha256:", "3 B", "unknown", "git+"}
		}
		for i, f := range fields {
			if got := columnOf(line, f); got != starts[i] {
				t.Errorf("column %d (%q) starts at %d, header puts it at %d\n  header: %q\n  row:    %q",
					i, f, got, starts[i], header, line)
			}
		}
	}
}

// TestBlueprintTable_TwoTagsOneHash: two tags resolving to the same bytes
// should read as one thing reached two ways. The aligned columns do that
// for free, which is worth pinning so a later change cannot quietly
// re-merge or re-split the rows.
func TestBlueprintTable_TwoTagsOneHash(t *testing.T) {
	const hash = "sha256:aaaaa…"
	const source = "oci://ghcr.io/acme/ci-platform"
	rows := []blueprintListRow{
		{Name: "ci-platform", Tag: "v3", Hash: hash, Size: "12 KB", Used: "2h old", Source: source},
		{Name: "ci-platform", Tag: "v2", Hash: hash, Size: "12 KB", Used: "2h old", Source: source},
	}
	lines := plainRows(t, rows, 0)[1:]
	if len(lines) != 2 {
		t.Fatalf("want one row per tag, got %q", lines)
	}
	// Identical but for the tag: that IS the message.
	a := strings.Replace(lines[0], "v3", "vX", 1)
	b := strings.Replace(lines[1], "v2", "vX", 1)
	if a != b {
		t.Errorf("rows sharing a hash should differ only in the tag:\n  %q\n  %q", lines[0], lines[1])
	}
}

// TestBlueprintTable_ElidesTheMiddle: on a narrow terminal the scheme and
// the blueprint's own name are the two things that must survive. The
// middle is registry org and path structure, usually shared by every row
// on screen, so truncating from the right (the reflex) would throw away
// exactly the half that tells rows apart.
func TestBlueprintTable_ElidesTheMiddle(t *testing.T) {
	rows := []blueprintListRow{
		{Name: "bp", Tag: "v1", Hash: "sha256:a…", Size: "1 KB", Used: "2h old",
			Source: "oci://ghcr.io/acme-platform-engineering/shared/blueprints/ci-platform"},
	}
	row := plainRows(t, rows, 80)[1]
	if w := utf8.RuneCountInString(row); w > 80 {
		t.Errorf("row is %d columns on an 80-column terminal: %q", w, row)
	}
	if !strings.Contains(row, "oci://") {
		t.Errorf("the scheme must survive elision: %q", row)
	}
	if !strings.HasSuffix(row, "ci-platform") {
		t.Errorf("the blueprint name must survive elision: %q", row)
	}
	if !strings.Contains(row, "…") {
		t.Errorf("expected an elision marker: %q", row)
	}
}

// TestBlueprintTable_NoWidthNoElision: a pipe has no width to respect,
// and truncating output something else is reading would lose data for no
// benefit.
func TestBlueprintTable_NoWidthNoElision(t *testing.T) {
	const long = "oci://ghcr.io/acme-platform-engineering/shared/blueprints/ci-platform"
	rows := []blueprintListRow{{Name: "bp", Tag: "v1", Hash: "h", Size: "1 KB", Used: "2h old", Source: long}}
	if row := plainRows(t, rows, 0)[1]; !strings.HasSuffix(row, long) {
		t.Errorf("a non-terminal writer must get the full source: %q", row)
	}
}

func TestSplitSourceTag(t *testing.T) {
	for _, tc := range []struct{ raw, source, tag string }{
		{"oci://ghcr.io/acme/ci-platform:v3", "oci://ghcr.io/acme/ci-platform", "v3"},
		// A registry port's colon is not in the final segment, so it is
		// left alone. Getting this wrong would split the host.
		{"oci://localhost:5000/acme/bp:v1", "oci://localhost:5000/acme/bp", "v1"},
		{"oci://ghcr.io/acme/ci-platform", "oci://ghcr.io/acme/ci-platform", ""},
		{"git+https://github.com/acme/bp@v1.2.0", "git+https://github.com/acme/bp", "v1.2.0"},
		// ssh user-info carries an "@" before the final segment, and it
		// is not a ref.
		{"git+ssh://git@github.com/acme/bp@main", "git+ssh://git@github.com/acme/bp", "main"},
		{"git+ssh://git@github.com/acme/bp", "git+ssh://git@github.com/acme/bp", ""},
		// The subdirectory stays with the source: in a monorepo of
		// blueprints it is the only part saying WHICH one, so dropping it
		// would make two rows identical.
		{"git+https://github.com/acme/bps@v1#subdirectory=ci", "git+https://github.com/acme/bps#subdirectory=ci", "v1"},
		{"/local/path/bp", "/local/path/bp", ""},
	} {
		source, tag := splitSourceTag(tc.raw)
		if source != tc.source || tag != tc.tag {
			t.Errorf("splitSourceTag(%q) = (%q, %q), want (%q, %q)", tc.raw, source, tag, tc.source, tc.tag)
		}
	}
}

// TestEmptyCacheNoteNamesWhatFillsIt: "nothing here" is a dead end. The
// reader has just asked what is cached, and the cache is never filled by
// hand, so the reply has to name the thing that fills it.
func TestEmptyCacheNoteNamesWhatFillsIt(t *testing.T) {
	var buf bytes.Buffer
	writeEmptyCacheNote(&buf, &styler{})
	out := buf.String()
	for _, want := range []string{"ubx plan", "[blueprints]", ".ubx/config.hcl"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty-cache note does not name %q:\n%s", want, out)
		}
	}
}

// TestElideMiddle_SchemeAndNameSurvive is the guarantee the column makes.
//
// The scheme is why this column is in the default table at all: oci and
// git are different distribution stories, and which one a blueprint came
// through is the fact worth seeing. The name is what tells two rows
// apart. Both have to survive any width that can hold them, and an
// earlier version dropped the scheme entirely on a narrow terminal
// precisely for the source that most needed it.
func TestElideMiddle_SchemeAndNameSurvive(t *testing.T) {
	for _, src := range []string{
		"oci://ghcr.io/acme-platform-engineering/shared/blueprints/ci-platform",
		"git+https://github.com/acme/blueprints#subdirectory=alerts",
		"git+ssh://git@github.com/acme/very/deeply/nested/path/to/a/blueprint",
	} {
		scheme := schemePrefix(src)
		for _, w := range []int{100, 72, 40, 24} {
			got := elideMiddle(src, w)
			if n := utf8.RuneCountInString(got); n > w {
				t.Errorf("elideMiddle(%q, %d) is %d columns: %q", src, w, n, got)
			}
			if !strings.HasPrefix(got, scheme) {
				t.Errorf("width %d dropped the scheme %q: %q", w, scheme, got)
			}
			// The end of the name survives, which is where a name is most
			// specific: "…tory=alerts" still says alerts.
			if !strings.HasSuffix(src, strings.TrimPrefix(got[strings.LastIndex(got, "…")+len("…"):], "")) {
				t.Errorf("width %d did not keep the end of the source: %q", w, got)
			}
		}
	}
}

// TestElideMiddle_FillsTheWidth: an over-eager elision is as unreadable
// as a truncation and quieter about it. At a width with room to spare,
// the result should use it.
func TestElideMiddle_FillsTheWidth(t *testing.T) {
	const src = "oci://ghcr.io/acme-platform-engineering/shared/blueprints/ci-platform"
	got := elideMiddle(src, 48)
	if n := utf8.RuneCountInString(got); n != 48 {
		t.Errorf("elideMiddle to 48 produced %d columns, leaving %d unused: %q", n, 48-n, got)
	}
}

// TestRenderSource_SchemeColour: oci and git are different distribution
// stories with different trust and different failure modes, and telling
// them apart should not require reading the line. The host is dim
// because it is usually identical on every row and the least interesting
// part of the line once it is.
func TestRenderSource_SchemeColour(t *testing.T) {
	st := &styler{enabled: true}
	for _, tc := range []struct{ src, wantCode, wantScheme string }{
		{"oci://ghcr.io/acme/bp", ansiGreen, "oci://"},
		{"git+https://github.com/acme/bp", ansiBlue, "git+https://"},
		{"git+ssh://git@github.com/acme/bp", ansiBlue, "git+ssh://"},
	} {
		got := renderSource(st, tc.src, 0)
		if !strings.HasPrefix(got, tc.wantCode+tc.wantScheme) {
			t.Errorf("renderSource(%q) did not open with the scheme in its own colour: %q", tc.src, got)
		}
		if !strings.Contains(got, ansiDim) {
			t.Errorf("renderSource(%q) should dim the host: %q", tc.src, got)
		}
	}

	// A bare local path has no scheme to colour and no host to dim: the
	// path is the whole value and the whole point.
	if got := renderSource(st, "/local/bp", 0); got != "/local/bp" {
		t.Errorf("a local path should render unstyled, got %q", got)
	}
}
