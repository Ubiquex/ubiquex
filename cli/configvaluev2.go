package cli

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/core"
)

// formatConfigValueV2 renders raw (one config attribute's value from a
// resolved Delta.Creates node) for docs/cli-output-spec.md §v2's own
// receipt format (UBI-63): a resolved $computed marker renders as the
// friendly `$ref:<stack>.<type>.<name>.<attr>` notation the founder's
// markup specifies (never the raw {"$computed":{"from":"..."}}
// envelope), a JSON-encoded string value (an IAM/trust policy document)
// renders as a formatted, multi-line JSON block instead of an escaped
// single-line string, and everything else renders as ordinary indented
// JSON -- recursing into a JSON-embedded string exactly like core/
// resolver's own JSON-embedded-refs mechanism (docs/schema.md's UBI-63
// amendment) walks one, so a $ref/$computed buried inside a policy
// document's own string content gets the identical friendly rendering.
func formatConfigValueV2(indent string, raw json.RawMessage) string {
	if raw == nil {
		return "(absent)"
	}
	if core.IsRedactedValue(raw) {
		return "(redacted)"
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return formatValueV2(indent, v)
}

// computedFromV2 reports the `from` address a resolved $computed marker
// ({"$computed": {"from": "<address>.<path>"}}, docs/resolver.md) names,
// if v is exactly that shape.
func computedFromV2(v interface{}) (string, bool) {
	m, ok := v.(map[string]interface{})
	if !ok || len(m) != 1 {
		return "", false
	}
	inner, ok := m["$computed"]
	if !ok {
		return "", false
	}
	innerMap, ok := inner.(map[string]interface{})
	if !ok {
		return "", false
	}
	from, ok := innerMap["from"].(string)
	return from, ok
}

// formatValueV2 is formatConfigValueV2's own recursive core -- indent is
// the CURRENT node's own indentation (a nested object/array's closing
// brace lines up with it; child entries get one level deeper).
func formatValueV2(indent string, v interface{}) string {
	if from, ok := computedFromV2(v); ok {
		return "$ref:" + from
	}
	switch t := v.(type) {
	case string:
		// A JSON-embedded value (docs/schema.md's UBI-63 amendment): an
		// attribute the provider schema types as a plain string but that
		// the resource itself treats as nested JSON (an IAM/trust policy
		// document). Rendered as a formatted block, not the escaped
		// single-line string it's stored as.
		var inner interface{}
		if err := json.Unmarshal([]byte(t), &inner); err == nil {
			switch inner.(type) {
			case map[string]interface{}, []interface{}:
				return formatValueV2(indent, inner)
			}
		}
		b, _ := json.Marshal(t)
		return string(b)
	case map[string]interface{}:
		if len(t) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		childIndent := indent + "  "
		var b strings.Builder
		b.WriteString("{\n")
		for i, k := range keys {
			b.WriteString(childIndent)
			kb, _ := json.Marshal(k)
			b.Write(kb)
			b.WriteString(": ")
			b.WriteString(formatValueV2(childIndent, t[k]))
			if i < len(keys)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(indent + "}")
		return b.String()
	case []interface{}:
		if len(t) == 0 {
			return "[]"
		}
		childIndent := indent + "  "
		var b strings.Builder
		b.WriteString("[\n")
		for i, e := range t {
			b.WriteString(childIndent)
			b.WriteString(formatValueV2(childIndent, e))
			if i < len(t)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(indent + "]")
		return b.String()
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}
