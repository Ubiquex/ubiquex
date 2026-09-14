package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/ubiquex/ubiquex/blueprint"
)

// newBlueprintListCmd reports what is in the local blueprint cache.
//
// It exists because the cache became content-addressed. Storage keyed by
// content hash is the only sound layout (a declaration string is a
// mutable pointer, and keying storage on one meant a repointed tag left
// a machine on old content indefinitely), but a directory of bare
// hashes says nothing about what any of it is. Without something to name
// the entries, the layout that fixed the correctness problem would have
// been a straight regression in legibility.
//
// So the origin index and this command arrived together, deliberately.
// An index nothing reads is a file that rots, and a hash-named directory
// nothing can explain is worse than the unsound layout it replaced.
//
// # Why a table rather than a stanza per entry
//
// The first version printed a paragraph per blueprint. That reads fine
// for two entries and stops scanning at ten, because the eye has to
// re-find each field at a different indent every time. An aligned table
// puts every value in a fixed column, which is what makes a cache
// listing answerable at a glance: what is big, what is old, what came
// from where.
//
// It also earns one thing for free that the stanza form had to state.
// Two tags resolving to the same bytes is ordinary, and in a table it
// shows as two rows with one hash and one source, differing only in the
// tag column: one thing reached two ways, which is exactly what it is.
//
// Reporting only. Removing anything is a separate command that does not
// exist yet, and the fields this prints (last used, size) are chosen so
// that it can.
func newBlueprintListCmd() *cobra.Command {
	var fullHashes bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List blueprints in the local content-addressed cache",
		Long: `Lists every blueprint in the local cache (~/.ubx/blueprints), one row per source,
with the content hash it resolved to.

The cache is content-addressed: one directory per content hash, never per name or
tag. A tag is a pointer that can be repointed at any time, so it names storage for
nobody, and a stack resolves a tag to a content hash through its own
.ubx/blueprints.lock.

One content hash can have several sources, which is ordinary: two tags pointing at
the same bytes, or a tag that has since moved on but explains why the content is
here at all. Those appear as rows sharing a hash.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			idx, err := blueprint.LoadIndex()
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("blueprint list: %w", err)}
			}
			out := cmd.OutOrStdout()
			st := newStylerFull(cmd, fullHashes)

			entries := idx.SortedIndexEntries()
			if len(entries) == 0 {
				writeEmptyCacheNote(out, st)
				return nil
			}

			rows := make([]blueprintListRow, 0, len(entries))
			var total int64
			for _, e := range entries {
				total += e.Entry.SizeBytes

				name := e.Entry.Name
				if name == "" {
					// An entry whose manifest carried no name is printable
					// rather than hidden: it is still taking up disk, and a
					// listing that silently omits things cannot be used to
					// reason about what is there.
					name = "(unnamed)"
				}
				base := blueprintListRow{
					Name: name,
					Hash: displayHash(e.Hash, fullHashes),
					Size: humanBytes(e.Entry.SizeBytes),
					Used: lastUsedAge(e.Entry.LastUsed),
				}
				if len(e.Entry.Sources) == 0 {
					// Reached with no recorded declaration. Rare, and worth
					// a row rather than a hidden entry: the disk is still
					// used and the absence is itself the useful fact.
					rows = append(rows, base)
					continue
				}
				// One row per source, so two tags over one hash line up as
				// two rows sharing every other column.
				for _, src := range e.Entry.Sources {
					r := base
					r.Source, r.Tag = splitSourceTag(src)
					rows = append(rows, r)
				}
			}

			writeBlueprintTable(out, st, rows, terminalWidth(out))
			fmt.Fprintf(out, "\n%s\n", st.Dim(fmt.Sprintf("%d blueprint(s) · %s total", len(entries), humanBytes(total))))
			return nil
		},
	}
	cmd.Flags().BoolVar(&fullHashes, "full-hashes", false, "render every hash in full instead of the default 12-char short form")
	return cmd
}

// blueprintListRow is one line of the table, all fields already
// rendered as the plain text they will occupy.
type blueprintListRow struct {
	Name   string
	Tag    string
	Hash   string
	Size   string
	Used   string
	Source string
}

// blueprintListGap is the space between columns. Two, not one: a single
// space between a right-ragged column and the next reads as a wrapped
// value rather than a boundary.
const blueprintListGap = "  "

// minSourceWidth is the narrowest SOURCE column worth rendering. Below
// this, eliding leaves nothing recognisable, so the column is allowed to
// overrun the terminal instead. Overflowing is recoverable by widening
// the window; a column of "o…p" is not.
const minSourceWidth = 24

// writeBlueprintTable renders the aligned table.
//
// termWidth <= 0 means the width could not be determined, which is every
// non-terminal writer including every test's buffer. Nothing is elided
// then: a pipe has no width to respect, and truncating output being read
// by something else would lose data to no benefit.
func writeBlueprintTable(out io.Writer, st *styler, rows []blueprintListRow, termWidth int) {
	w := func(header string, get func(blueprintListRow) string) int {
		n := cellWidth(header)
		for _, r := range rows {
			if v := cellWidth(get(r)); v > n {
				n = v
			}
		}
		return n
	}
	nameW := w("NAME", func(r blueprintListRow) string { return r.Name })
	tagW := w("TAG", func(r blueprintListRow) string { return r.Tag })
	hashW := w("HASH", func(r blueprintListRow) string { return r.Hash })
	sizeW := w("SIZE", func(r blueprintListRow) string { return r.Size })
	usedW := w("USED", func(r blueprintListRow) string { return r.Used })
	sourceW := w("SOURCE", func(r blueprintListRow) string { return r.Source })

	if termWidth > 0 {
		fixed := nameW + tagW + hashW + sizeW + usedW + 5*len(blueprintListGap)
		if avail := termWidth - fixed; avail < sourceW {
			sourceW = max(avail, minSourceWidth)
		}
	}

	header := strings.Join([]string{
		padPlain("NAME", nameW),
		padPlain("TAG", tagW),
		padPlain("HASH", hashW),
		padPlain("SIZE", sizeW),
		padPlain("USED", usedW),
		"SOURCE",
	}, blueprintListGap)
	fmt.Fprintf(out, "%s\n", st.Dim(header))

	for _, r := range rows {
		// The name is the thing being looked up, so it carries the only
		// bright cell. The tag is the thing that varies between rows
		// sharing a hash, so it gets the one accent colour. Everything
		// else is dim: it is there to be compared, not scanned for.
		line := strings.Join([]string{
			padStyled(st.Bold(r.Name), nameW),
			padStyled(st.Yellow(orDash(r.Tag)), tagW),
			padStyled(st.Dim(r.Hash), hashW),
			padStyled(st.Dim(r.Size), sizeW),
			padStyled(st.Dim(r.Used), usedW),
			renderSource(st, r.Source, sourceW),
		}, blueprintListGap)
		fmt.Fprintln(out, line)
	}
}

// orDash renders an absent value as something a column can align on. An
// empty cell in a table reads as a rendering fault; a dash reads as an
// answer.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// splitSourceTag separates a declaration's own version pointer from the
// thing it points at, so the two can be separate columns.
//
// Worth splitting rather than printing the raw URL: with the tag in its
// own column, two tags over one hash produce two rows identical in every
// column but that one, which is precisely the fact worth seeing.
func splitSourceTag(raw string) (source, tag string) {
	// A git ref is the fragment-free tail after "@", and only in the
	// final path segment: an "@" earlier is ssh user-info
	// ("git@github.com"), not a ref. Same rule blueprint's own URL parser
	// applies, and for the same reason.
	if strings.HasPrefix(raw, "git+") {
		base, frag, hasFrag := strings.Cut(raw, "#")
		if lastSlash := strings.LastIndex(base, "/"); lastSlash >= 0 {
			if at := strings.Index(base[lastSlash:], "@"); at >= 0 {
				tag = base[lastSlash+at+1:]
				base = base[:lastSlash+at]
			}
		}
		if hasFrag {
			// The subdirectory stays in the source: for a monorepo of
			// blueprints it is the only part that says WHICH one, so
			// dropping it would make two rows look identical.
			base += "#" + frag
		}
		return base, tag
	}

	// An OCI tag is after the last ":" in the final path segment. A
	// registry port ("localhost:5000/x") has its colon earlier, so
	// scanning only the final segment leaves it alone.
	rest := strings.TrimPrefix(raw, "oci://")
	if lastSlash := strings.LastIndex(rest, "/"); lastSlash >= 0 {
		if c := strings.LastIndex(rest[lastSlash:], ":"); c >= 0 {
			idx := len(raw) - len(rest) + lastSlash + c
			return raw[:idx], raw[idx+1:]
		}
	}
	return raw, ""
}

// renderSource colours and, when it has to, elides one source.
//
// The scheme is coloured because it is the field's own most load-bearing
// bit: oci and git are different distribution stories with different
// trust and different failure modes, and telling them apart should not
// require reading. The host is dim because it is usually the same for
// every row and the least interesting part of a line once it is.
func renderSource(st *styler, source string, width int) string {
	if source == "" {
		return st.Dim("-")
	}
	if width > 0 && cellWidth(source) > width {
		source = elideMiddle(source, width)
	}

	scheme := schemePrefix(source)
	rest := source[len(scheme):]
	colour := st.Green
	if strings.HasPrefix(scheme, "git+") {
		colour = st.Blue
	}
	if scheme == "" {
		// A bare local path. No scheme to colour and no host to dim: the
		// whole value is the path, and the path is the point.
		return source
	}

	host, tail, found := strings.Cut(rest, "/")
	if !found {
		return colour(scheme) + st.Dim(host)
	}
	return colour(scheme) + st.Dim(host+"/") + tail
}

// sourceSchemes are the prefixes renderSource colours and elideMiddle
// protects, longest first so "git+https://" wins over "git+".
var sourceSchemes = []string{"oci://", "git+https://", "git+ssh://", "git+http://", "git+", "file://"}

// schemePrefix returns s's own scheme, or "" for a bare local path.
func schemePrefix(s string) string {
	for _, p := range sourceSchemes {
		if strings.HasPrefix(s, p) {
			return p
		}
	}
	return ""
}

// lastSegment is the part that names the blueprint: everything after the
// final "/", or the whole string when there is none.
func lastSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 && i+1 < len(s) {
		return s[i+1:]
	}
	return s
}

// elideMiddle shortens a source to width, cutting from the middle so
// both ends survive.
//
// The ends are what identify it. The scheme says how it is distributed,
// with different trust and different failure modes behind each, and the
// last segment is the blueprint's own name. The middle is registry org
// and path structure, usually shared by every row on screen. Truncating
// from the right, which is the reflex, throws away exactly the half that
// tells one row from another.
//
// It fills the width it is given rather than cutting to some minimum: at
// 100 columns there is room for most of the path, and an over-eager
// elision is just as unreadable as a truncation, only quieter about it.
func elideMiddle(s string, width int) string {
	const ellipsis = "…"
	if width <= 1 {
		return ellipsis
	}
	if cellWidth(s) <= width {
		return s
	}

	head := schemePrefix(s)
	headW := cellWidth(head)
	tail := []rune(lastSegment(s))
	whole := []rune(s)

	// Keep as much of the left as fits once the name and the marker are
	// reserved. The scheme comes free with it, being the leftmost thing.
	if left := width - 1 - len(tail); left >= headW && left > 0 && left < len(whole) {
		return string(whole[:left]) + ellipsis + string(tail)
	}

	// The name alone does not fit. The scheme still must: it is the field
	// this column exists to distinguish. So keep the scheme, the marker,
	// and the END of the name, which is where a name is most specific.
	if keep := width - headW - 1; keep > 0 {
		if keep > len(tail) {
			keep = len(tail)
		}
		return head + ellipsis + string(tail[len(tail)-keep:])
	}

	// Narrower than the scheme itself. Nothing useful survives either
	// way, so keep the end of the name.
	keep := width - 1
	if keep > len(tail) {
		keep = len(tail)
	}
	return ellipsis + string(tail[len(tail)-keep:])
}

// writeEmptyCacheNote answers the question an empty listing raises.
//
// "Nothing here" on its own is a dead end: the reader has just asked
// what is cached, and the useful reply names the thing that would put
// something in it. The cache is never filled by hand, so a note that
// only said it was empty would leave them looking for a command that
// does not exist.
func writeEmptyCacheNote(out io.Writer, st *styler) {
	fmt.Fprintf(out, "%s\n", st.Dim("no blueprints cached yet."))
	fmt.Fprintf(out, "\n%s\n", "Declare one in .ubx/config.hcl and `ubx plan` pulls, verifies and caches it:")
	fmt.Fprintf(out, "\n  %s\n", st.Dim("[blueprints]"))
	fmt.Fprintf(out, "  %s\n", st.Dim(`ci-platform = "oci://ghcr.io/acme/ci-platform:v1"`))
}

// lastUsedAge renders an index entry's RFC3339 timestamp through the
// same humanAge every other elapsed-time line in this package uses,
// rather than a second spelling of "how long ago".
func lastUsedAge(ts string) string {
	if ts == "" {
		return "unknown"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "unknown"
	}
	return humanAge(t)
}
