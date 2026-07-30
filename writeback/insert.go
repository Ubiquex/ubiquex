package writeback

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// insertableKey is resolveTarget's signal that a dot-path's final segment
// names a key that doesn't exist yet, but its parent is an existing
// literal object attribute could could safely gain one — the most common
// real drift shape (UBI-11 stage 2 follow-up): someone tags a resource in
// the console with a key the .tf file's tags = { ... } never had. This is
// deliberately distinct from every other "not found" case, which stays a
// hard decline: only the object's own key set may grow; the object itself
// must already exist as a literal (see resolveTarget), and only the
// path's last segment may be missing (a missing *intermediate* segment
// would mean inserting a whole new nested structure, still out of scope).
type insertableKey struct {
	obj *hclsyntax.ObjectConsExpr
	key string
}

func (e *insertableKey) Error() string {
	return fmt.Sprintf("key %q not present (insertable into an existing literal object)", e.key)
}

// planInsertion computes the exact byte position and bytes to splice in to
// add key=rawValue as a new item in obj, matching obj's own existing
// formatting: multi-line objects get the new key on its own line, indented
// to match the last existing item (and given a trailing comma only if the
// last item already had one — "preserve trailing-comma style"); a
// single-line object gets ", key = value" appended after the last item;
// an empty object gets a new line inside the braces, indented two spaces
// past the attribute's own line. src must be the same bytes obj was
// parsed from — every offset here is relative to it.
func planInsertion(src []byte, obj *hclsyntax.ObjectConsExpr, key string, rawValue json.RawMessage) (hcl.Range, []byte, error) {
	valueBytes, err := renderLiteral(rawValue)
	if err != nil {
		return hcl.Range{}, nil, fmt.Errorf("new value can't be rendered as an HCL literal: %w", err)
	}

	closeByte := obj.SrcRange.End.Byte - 1 // the "}" itself

	if len(obj.Items) == 0 {
		indent := lineIndent(src, obj.OpenRange.Start.Byte)
		text := "\n" + indent + "  " + key + " = " + string(valueBytes) + "\n" + indent
		return zeroRange(obj.OpenRange.End.Byte), []byte(text), nil
	}

	last := obj.Items[len(obj.Items)-1]
	lastEnd := last.ValueExpr.Range().End.Byte

	nlIdx := bytes.IndexByte(src[lastEnd:closeByte], '\n')
	if nlIdx == -1 {
		// Single-line object: `{ a = 1, b = 2 }` — append after the last
		// item's value, before whatever trailing space precedes "}".
		text := ", " + key + " = " + string(valueBytes)
		return zeroRange(lastEnd), []byte(text), nil
	}

	insertAt := lastEnd + nlIdx + 1
	between := src[lastEnd : lastEnd+nlIdx] // between the last value and its line's newline -- a trailing comma and/or comment may live here
	comma := ""
	if firstNonSpaceIsComma(between) {
		comma = ","
	}
	indent := lineIndent(src, last.KeyExpr.Range().Start.Byte)
	text := indent + key + " = " + string(valueBytes) + comma + "\n"
	return zeroRange(insertAt), []byte(text), nil
}

// zeroRange is a zero-width hcl.Range at byte — spliceBytes already treats
// a zero-width range as a pure insertion (src[:byte] + replacement +
// src[byte:]), so insertion and replacement share the exact same
// application code path in ApplyModification.
func zeroRange(byteOffset int) hcl.Range {
	pos := hcl.Pos{Byte: byteOffset}
	return hcl.Range{Start: pos, End: pos}
}

// lineIndent returns the leading whitespace of the line containing pos, up
// to pos itself (or up to the first non-space/tab character, whichever
// comes first) — used both to match an existing item's indentation and,
// for an empty object, to derive a sensible indentation from the
// attribute's own line.
func lineIndent(src []byte, pos int) string {
	lineStart := pos
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	i := lineStart
	for i < pos && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return string(src[lineStart:i])
}

// firstNonSpaceIsComma reports whether the first non-space/tab byte in b
// is a comma — used to detect "this object's items are comma-terminated"
// from the raw bytes between an item's value and the end of its line.
func firstNonSpaceIsComma(b []byte) bool {
	for _, c := range b {
		if c == ' ' || c == '\t' {
			continue
		}
		return c == ','
	}
	return false
}
