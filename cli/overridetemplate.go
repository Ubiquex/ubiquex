// overridetemplate.go is `ubx render --sync-overrides`/`--from-drift`'s
// own templating half (UBI-86 Part 2, design comment item 10, CONFIRMED
// as its own requirement): status --drift (by way of scanDrift,
// drift.go) already computes the exact address/field/old/new values --
// turning that into "override X's Y to Z" syntax in a target medium is
// templating into that medium's own grammar, never interpretation. Zero
// AI anywhere in this file.
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ubiquex/ubiquex/core"
)

// overrideMediumFromDetected maps one autodetectMedium (plan.go) result
// to the override-statement medium key renderOverrideStatement expects --
// reused, not reimplemented: the SAME real file-sniffing autodetectMedium
// already uses for `ubx plan`'s own bare-invocation auto-detection is
// what "in whichever medium the calling stack is authored in" (UBI-86's
// own design comment) means here too.
func overrideMediumFromDetected(d detectedMedium) (string, error) {
	switch d.flag {
	case "--from-doc":
		return "md", nil
	case "--from-diagram":
		return "diagram", nil
	case "--from-code":
		switch strings.ToLower(filepath.Ext(d.path)) {
		case ".go":
			return "go", nil
		case ".ts":
			return "ts", nil
		case ".py":
			return "py", nil
		}
	}
	return "", fmt.Errorf("render: %s: unrecognized authoring medium", d.path)
}

// topLevelOverrideFields collapses d.Fields (a per-PATH diff, which may
// include dotted nested paths like "tags.mutated") into one DriftedField
// per distinct TOP-LEVEL wire attribute, each carrying that whole
// attribute's own complete CURRENT value pulled from d.Observed -- never
// the partial sub-path value DiffAttributes reported. This is what makes
// resolver.Override.Config's own "top-level attribute, never a dot-path,
// never a deep merge" contract (its own doc comment) actually correct
// for a nested drift: overriding "tags.mutated" alone would silently
// DROP every other key already in the live "tags" map the moment
// ApplyOverrides replaces it; pulling the attribute's own full observed
// value instead preserves everything else in it, exactly matching what
// a real out-of-band console edit actually left behind.
func topLevelOverrideFields(d DriftedResource) ([]DriftedField, error) {
	var observed map[string]json.RawMessage
	if len(d.Observed) > 0 {
		if err := json.Unmarshal(d.Observed, &observed); err != nil {
			return nil, fmt.Errorf("%s: decode observed state: %w", d.Address, err)
		}
	}

	seen := map[string]bool{}
	var out []DriftedField
	for _, f := range d.Fields {
		attr := f.Path
		if i := strings.IndexByte(attr, '.'); i >= 0 {
			attr = attr[:i]
		}
		if seen[attr] {
			continue
		}
		seen[attr] = true

		val, ok := observed[attr]
		if !ok {
			// attr.Path IS attr (a top-level scalar path with no
			// nesting) and Observed didn't carry it (e.g. the attribute
			// was removed entirely) -- After is already its own correct
			// whole-attribute value in that case.
			val = f.After
		}
		out = append(out, DriftedField{Path: attr, After: val})
	}
	return out, nil
}

// renderOverrideStatement dispatches to one medium-specific renderer,
// each producing ONE complete statement/snippet -- a single override()
// call, a single ubx_override node, or a single "Override ... to ..."
// sentence -- patching every one of fields at once (the resolver.Override
// wire shape is one address -> a map of ALL its own patched fields, never
// one entry per field), in fields' own address-then-path sort order for
// determinism.
func renderOverrideStatement(medium string, addr core.Address, fields []DriftedField) (string, error) {
	sorted := append([]DriftedField(nil), fields...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	switch medium {
	case "go":
		return renderOverrideGo(addr.String(), sorted), nil
	case "ts":
		return renderOverrideTS(addr.String(), sorted), nil
	case "py":
		return renderOverridePython(addr.String(), sorted), nil
	case "diagram":
		return renderOverrideDiagram(addr, sorted)
	case "md":
		return renderOverrideMd(addr.String(), sorted), nil
	default:
		return "", fmt.Errorf("render: unrecognized override medium %q", medium)
	}
}

func renderOverrideGo(addr string, fields []DriftedField) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sdk.Override(%q, map[string]any{\n", addr)
	for _, f := range fields {
		fmt.Fprintf(&b, "\t%q: %s,\n", f.Path, jsonToGoLiteral(f.After))
	}
	b.WriteString("})\n")
	return b.String()
}

func renderOverrideTS(addr string, fields []DriftedField) string {
	var b strings.Builder
	fmt.Fprintf(&b, "override(%q, {\n", addr)
	for _, f := range fields {
		fmt.Fprintf(&b, "  %q: %s,\n", f.Path, jsonToTSLiteral(f.After))
	}
	b.WriteString("});\n")
	return b.String()
}

func renderOverridePython(addr string, fields []DriftedField) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sdk.override(%q, {\n", addr)
	for _, f := range fields {
		key, _ := json.Marshal(f.Path)
		fmt.Fprintf(&b, "    %s: %s,\n", key, jsonToPyLiteral(f.After))
	}
	b.WriteString("})\n")
	return b.String()
}

// renderOverrideDiagram templates a real ubx_override node
// (diagram/parse.go's own overrideFromNode is what actually consumes
// this syntax on the parse side -- kept in sync by construction: both
// this renderer and that parser exist in the same UBI-86 change).
// Diagram's own structural-attribute-reading mechanism, like
// ubx_required before it, only ever expresses a plain STRING-valued
// attribute (ubxRequiredAttrValue always JSON-string-encodes raw D2
// text) -- a real, pre-existing, documented scope boundary this
// function inherits rather than works around; a non-string drifted
// value is a clear, named refusal here, not a silently wrong render.
func renderOverrideDiagram(addr core.Address, fields []DriftedField) (string, error) {
	key := diagramNodeKey(addr)
	var b strings.Builder
	fmt.Fprintf(&b, "%s: \"fix %s\" {\n", key, addr.Name)
	b.WriteString("  class: ubx_override\n")
	fmt.Fprintf(&b, "  address: %s\n", d2QuoteForOverride(addr.String()))
	for _, f := range fields {
		s, ok := jsonStringValue(f.After)
		if !ok {
			return "", fmt.Errorf("render --sync-overrides: %s.%s: diagram override syntax only supports string-valued overrides (matching ubx_required's own established scope) -- use --md against a Go/TS/Python-authored stack for a non-string value instead", addr, f.Path)
		}
		fmt.Fprintf(&b, "  %s: %s\n", f.Path, d2QuoteForOverride(s))
	}
	b.WriteString("}\n")
	return b.String(), nil
}

func renderOverrideMd(addr string, fields []DriftedField) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = fmt.Sprintf("%s to %s", f.Path, mdValueText(f.After))
	}
	return fmt.Sprintf("Override %s's %s.\n", addr, strings.Join(parts, ", "))
}

// diagramNodeKey derives a legal, deterministic D2 identifier from addr
// -- addr.Name may itself contain characters D2's own bare-identifier
// grammar doesn't accept (a hyphen, say -- "pipeline-events" is a
// perfectly normal ubx resource name); "fix_" plus every non-
// alphanumeric character folded to "_" is always safe, mirroring
// diagram/emit.go's own synthetic "rN"/"bpN" key convention (a legible-
// enough key is nice to have, but never load-bearing -- the node's own
// D2 label, and its "address:" attribute, are what actually carry the
// real address).
func diagramNodeKey(addr core.Address) string {
	var b strings.Builder
	b.WriteString("fix_")
	for _, r := range addr.Name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// d2QuoteForOverride mirrors diagram/emit.go's own unexported d2Quote --
// package boundaries mean this is a small, deliberately independent
// mirror (diagram can't import cli, cli already imports diagram), the
// same precedent diagram.ShortBlueprintRef's own doc comment already
// established for the reverse direction.
func d2QuoteForOverride(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return `"` + s + `"`
}

// jsonStringValue reports whether raw decodes to a plain JSON string,
// returning its own decoded value.
func jsonStringValue(raw json.RawMessage) (string, bool) {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// mdValueText renders raw for the md medium's own generated prose -- a
// string renders as its own quoted text (matching the design comment's
// own literal worked example, "...to new_value."); anything else renders
// as its own compact JSON text, since md prose has no separate literal
// grammar of its own to borrow from the way Go/TS/Python each do.
func mdValueText(raw json.RawMessage) string {
	if s, ok := jsonStringValue(raw); ok {
		return fmt.Sprintf("%q", s)
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// jsonToGoLiteral/jsonToTSLiteral/jsonToPyLiteral render one JSON value
// as a literal expression in each target language -- handling every
// JSON value kind a real drifted attribute is ever shaped like (string/
// number/bool/null/object/array), recursively.

func jsonToGoLiteral(raw json.RawMessage) string {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Sprintf("%q", string(raw))
	}
	return goLiteral(v)
}

func goLiteral(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "nil"
	case string:
		return fmt.Sprintf("%q", t)
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = fmt.Sprintf("%q: %s", k, goLiteral(t[k]))
		}
		return "map[string]any{" + strings.Join(parts, ", ") + "}"
	case []interface{}:
		parts := make([]string, len(t))
		for i, el := range t {
			parts[i] = goLiteral(el)
		}
		return "[]any{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", t)
	}
}

// jsonToTSLiteral needs no recursive renderer at all -- JSON is already
// valid TS/JS object/array/string/number/bool/null expression syntax
// verbatim; only whitespace is normalized.
func jsonToTSLiteral(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

func jsonToPyLiteral(raw json.RawMessage) string {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Sprintf("%q", string(raw))
	}
	return pyLiteral(v)
}

func pyLiteral(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		b, _ := json.Marshal(t) // double-quoted -- valid Python string syntax too
		return string(b)
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		if t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			kb, _ := json.Marshal(k)
			parts[i] = fmt.Sprintf("%s: %s", kb, pyLiteral(t[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case []interface{}:
		parts := make([]string, len(t))
		for i, el := range t {
			parts[i] = pyLiteral(el)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%v", t)
	}
}
