package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// The shared shape every read command renders in.
//
// `ubx history`, `ubx status`, `ubx why` and `ubx blame` answer four
// questions about the same ledger, and each one used to print in a
// format of its own: history a two-space-separated line plus two
// indented continuations, status a bare colon-separated line with no
// header at all and a trailing count that read like a warning, why a
// left-aligned key/value block, and blame alone with a real header and
// grouped sections.
//
// blame was the only one with structure, so it became the pattern:
//
//	Title  subject · count
//
//	▸ <hash>  <kind>  · who · when
//	    indented content
//
// Everything below exists so those four cannot drift apart again: one
// header builder, one group builder, one attribute renderer, one clock.
// A fifth read command should reach for these rather than invent a
// sixth format.

// readHeader builds the first line: a title, its subject, and a dim
// trailer of facts about the whole view. The trailer is where a count
// or a qualifier goes, never a line of its own at the bottom -- status
// used to end with "1 resource(s) (ledger-only, no live comparison)",
// which reads as a warning about something having gone wrong rather than
// as a description of what you asked for.
func readHeader(st *styler, title, subject string, facts ...string) string {
	line := title
	if subject != "" {
		line += "  " + subject
	}
	kept := facts[:0]
	for _, f := range facts {
		if f != "" {
			kept = append(kept, f)
		}
	}
	if len(kept) > 0 {
		line += " " + st.Dim("· "+strings.Join(kept, " · "))
	}
	return line
}

// readGroup builds a "▸ " group line: a lead (the hash, or a count of
// attributes), then dim-separated facts. The marker and the separators
// are structure, so both stay dim; the lead carries its own color from
// the caller.
func readGroup(st *styler, lead string, facts ...string) string {
	line := st.Dim("▸ ") + lead
	kept := facts[:0]
	for _, f := range facts {
		if f != "" {
			kept = append(kept, f)
		}
	}
	if len(kept) > 0 {
		line += " " + st.Dim("· ") + strings.Join(kept, st.Dim(" · "))
	}
	return line
}

// relativeTime renders an RFC3339 timestamp as how long ago it was.
//
// Absolute timestamps are for machines. A reader scanning a history
// wants "3 minutes ago", not a UTC instant they have to subtract from
// the current time in their head, and `ubx why` was printing the same
// second five times over because a resource's pending/in_flight/shipped
// transitions all land inside one second.
//
// --json keeps the absolute value, unchanged and byte-identical: that is
// the surface where an exact instant is the point.
//
// An unparseable timestamp is returned verbatim rather than swallowed. A
// ledger that somehow holds a malformed time should show it, not hide it
// behind a plausible-looking "just now".
func relativeTime(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := now.Sub(t)
	if d < 0 {
		// A future timestamp is a real anomaly (clock skew, a doctored
		// ledger). Say so rather than rendering "-3 minutes ago".
		return "in the future (" + ts + ")"
	}
	switch {
	case d < 45*time.Second:
		return "just now"
	case d < 90*time.Second:
		return "a minute ago"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 2*time.Hour:
		return "an hour ago"
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		months := int(d.Hours() / 24 / 30)
		if months == 1 {
			return "a month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	default:
		years := int(d.Hours() / 24 / 365)
		if years == 1 {
			return "a year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
}

// deltaCounts renders the three counts in the receipt's own compact
// form, "+1 ~0 -0".
//
// history rendered these as "+1 create(s) ~0 change(s) -0 terminate(s)",
// a second phrasing for the identical three numbers the receipt already
// prints one way. The long form stays where it belongs, on the receipt's
// own "delta:" line, which is a sentence about one proposal; a history
// listing is a column, and a column wants the compact form.
func deltaCounts(st *styler, creates, modifies, destroys int64) string {
	return fmt.Sprintf("%s %s %s",
		st.Green(fmt.Sprintf("+%d", creates)),
		st.Yellow(fmt.Sprintf("~%d", modifies)),
		st.Red(fmt.Sprintf("-%d", destroys)))
}

// flattenAttrs turns a nested config map into blame's own dotted, one
// line per leaf form: tags.env: "prod", never a nested JSON block.
//
// The receipt used to open a brace for any object-valued attribute, so a
// resource with three tags cost six lines and a reader had to track
// indentation to know which key they were under. blame already flattened
// and was the easiest of the four to read because of it.
//
// Values are rendered as compact JSON so a string keeps its quotes and a
// number does not gain any: the quoting is what tells a reader which is
// which.
func flattenAttrs(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			out[prefix] = "{}"
			return
		}
		for k, sub := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flattenAttrs(key, sub, out)
		}
	case []any:
		if len(t) == 0 {
			out[prefix] = "[]"
			return
		}
		for i, sub := range t {
			flattenAttrs(fmt.Sprintf("%s[%d]", prefix, i), sub, out)
		}
	default:
		out[prefix] = compactJSON(v)
	}
}

// sortedFlatKeys returns a flattened map's keys in a stable order, so
// two runs of the same command never differ.
func sortedFlatKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// writeAttrs prints one flattened attribute per line, in the ratified
// palette, at the given indent.
func writeAttrs(w io.Writer, st *styler, indent string, attrs map[string]string) {
	for _, k := range sortedFlatKeys(attrs) {
		fmt.Fprintf(w, "%s%s\n", indent, st.Attr(k, attrs[k]))
	}
}

// compactJSON renders one leaf value the way a reader expects to see it:
// a string keeps its quotes, everything else does not. Marshaling rather
// than fmt.Sprint, so a string containing a quote or a newline comes out
// escaped instead of breaking the line it is on.
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// flattenRaw is flattenAttrs over raw JSON, the shape most callers
// actually hold. Unparseable JSON is surfaced verbatim under its own
// key rather than dropped.
func flattenRaw(prefix string, raw []byte, out map[string]string) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		out[prefix] = strings.TrimSpace(string(raw))
		return
	}
	flattenAttrs(prefix, v, out)
}

// collapseTransitions renders one resource's ship lifecycle.
//
// pending, in_flight and shipped all land inside the same second on an
// ordinary ship, so `ubx why` printed three lines carrying the identical
// timestamp, and a five-transition destroy printed five. The timestamps
// were not telling the reader anything, and the states were the same
// states every successful ship has.
//
// So the ordinary case collapses to a single line naming the terminal
// state and how long ago it happened. The steps are only worth printing
// individually when one of them carries a real Detail, which is where a
// retry, a wait, or a provider message actually lives.
func collapseTransitions(st *styler, transitions []transitionView, outcome string, now time.Time) []string {
	if len(transitions) == 0 {
		return nil
	}
	interesting := false
	for _, t := range transitions {
		if t.Detail != "" {
			interesting = true
			break
		}
	}
	last := transitions[len(transitions)-1]
	terminal := displayResourceState(last.State)
	if !interesting {
		line := st.Green(terminal)
		if !last.OK {
			line = st.Red(terminal)
		}
		if outcome != "" {
			line += " " + st.Dim("("+outcome+")")
		}
		return []string{line + " " + st.Dim(relativeTime(last.At, now))}
	}
	lines := make([]string, 0, len(transitions))
	for i, t := range transitions {
		line := displayResourceState(t.State) + " " + st.Dim(relativeTime(t.At, now))
		if t.Detail != "" {
			line += " " + st.Dim("--") + " " + t.Detail
		}
		if i == len(transitions)-1 && outcome != "" {
			line += " " + st.Dim("("+outcome+")")
		}
		lines = append(lines, line)
	}
	return lines
}

// transitionView is the minimal shape collapseTransitions needs, so this
// file does not depend on core's own record types and stays testable
// with a literal.
type transitionView struct {
	State  string
	At     string
	Detail string
	OK     bool
}

// sortedRawKeys is the stable iteration order the attribute renderers
// use over a raw-JSON config map. Named apart from configcascade.go's
// own sortedKeys, which is typed for a different map.
func sortedRawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// renderConfigAttr prints one config attribute, choosing between the two
// forms that matter.
//
// Nested objects and arrays flatten to blame's dotted form, one leaf per
// line, which is what makes three tags three lines instead of six.
//
// A STRING value does not, and that distinction is the whole reason this
// function exists rather than a bare call to flattenRaw. A real IAM or
// trust policy arrives as a string that itself contains JSON, and
// docs/cli-output-spec.md requires those render as a formatted, readable
// block, never as an escaped single-line string. Flattening treated such
// a value as an ordinary scalar and printed
// name: "{\"Version\":\"2012-10-17\",...}", which is precisely the shape
// the spec forbids and the thing a reviewer most needs to be able to
// read. formatConfigValueV2 already knew how to decode and pretty-print
// it, so strings defer to it.
//
// A resolved $computed marker is a third case formatConfigValueV2 owns,
// rendering the friendly $ref:<address> notation.
func renderConfigAttr(w io.Writer, st *styler, indent, key string, raw []byte) {
	val := formatConfigValueV2(indent, raw)
	if strings.HasPrefix(strings.TrimSpace(val), "$ref:") {
		fmt.Fprintf(w, "%s%s\n", indent, st.Attr(key, strings.TrimSpace(val)))
		return
	}
	var probe any
	if err := json.Unmarshal(raw, &probe); err == nil {
		switch probe.(type) {
		case map[string]any, []any:
			flat := map[string]string{}
			flattenRaw(key, raw, flat)
			writeAttrs(w, st, indent, flat)
			return
		}
	}
	if strings.Contains(val, "\n") {
		fmt.Fprintf(w, "%s%s\n%s%s\n", indent, st.Yellow(key)+":", indent, st.Red(val))
		return
	}
	fmt.Fprintf(w, "%s%s\n", indent, st.Attr(key, val))
}
