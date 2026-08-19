// fieldSignal mirrors ubx-provider-dynamic's own internal/schema.FieldSignal
// JSON shape exactly -- deliberately a LOCAL, JSON-contract-only mirror,
// not a shared Go type: this repo intentionally never imports
// ubx-provider-dynamic as a Go module dependency (cli/dynamicprovider.go's
// own doc comment explains why the two repos stay decoupled, launched as
// a subprocess rather than linked), so the two sides only ever agree on
// a JSON shape, never a compiled type.
package cli

import "fmt"

// fieldSignal carries the real enum/constraint data a field's own source
// OpenAPI schema expressed but tfplugin's own protocol v6 wire type has
// no field for at all (ubx-provider-dynamic's own
// internal/schema/signals.go doc comment has the full real finding) --
// dumped by that binary's own --dump-signals mode, keyed by
// ToSnakeCase(name) at every level, matching ir.Field.WireName exactly.
type fieldSignal struct {
	Enum      []string                `json:"enum,omitempty"`
	Minimum   *float64                `json:"minimum,omitempty"`
	Maximum   *float64                `json:"maximum,omitempty"`
	MinLength *uint64                 `json:"min_length,omitempty"`
	MaxLength *uint64                 `json:"max_length,omitempty"`
	Pattern   string                  `json:"pattern,omitempty"`
	Nested    map[string]*fieldSignal `json:"nested,omitempty"`
}

// enumStrings returns f's own real enum values, nil-safe for a field
// with no matched signal at all (the common case -- most fields carry
// no real enum/constraint data).
func (f *fieldSignal) enumStrings() []string {
	if f == nil {
		return nil
	}
	return f.Enum
}

// constraintStrings renders f's own real min/max/length/pattern bounds
// as short, human-readable, LLM-prompt-ready strings -- the exact shape
// describe.FieldContext.Constraints already expects (see that type's own
// doc comment), never raw numbers a caller would have to format itself.
func (f *fieldSignal) constraintStrings() []string {
	if f == nil {
		return nil
	}
	var out []string
	if f.Minimum != nil {
		out = append(out, fmt.Sprintf("minimum: %v", *f.Minimum))
	}
	if f.Maximum != nil {
		out = append(out, fmt.Sprintf("maximum: %v", *f.Maximum))
	}
	if f.MinLength != nil {
		out = append(out, fmt.Sprintf("minimum length: %d", *f.MinLength))
	}
	if f.MaxLength != nil {
		out = append(out, fmt.Sprintf("maximum length: %d", *f.MaxLength))
	}
	if f.Pattern != "" {
		out = append(out, fmt.Sprintf("pattern: %s", f.Pattern))
	}
	return out
}
