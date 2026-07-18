package cli

import (
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// parseHCLGeneric parses an HCL config file into a genericTree.
// Config's own tables (`provider`, `provider_config`, `providers`,
// `provider_configs`, `k8s_audit`) are all top-level attributes holding
// an object-constructor expression (`provider = { source = "...",
// version = "..." }`), never HCL blocks -- confirmed necessary, not a
// style choice: parsed directly against hclsyntax, `providers {
// "hashicorp/aws" = "6.60.0" }` is a hard parse error (block argument
// names can't be quoted), so a table whose own keys are provider source
// strings has no valid block-argument spelling at all. Using
// attribute-object syntax uniformly also means literal-only enforcement
// is a single `attr.Expr.Value(nil)` call per top-level attribute --
// that call already recurses through an entire object-constructor
// expression, so it proves a whole table's every nested value is a
// literal in one shot, matching tfwrite/literal.go's own technique on
// resource attributes.
func parseHCLGeneric(path string) (genericTree, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, diags := hclsyntax.ParseConfig(data, path, hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s", diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("%s: unexpected HCL body", path)
	}
	if len(body.Blocks) > 0 {
		blk := body.Blocks[0]
		return nil, fmt.Errorf("%s: config supports attribute assignment only (e.g. %s = { ... }), not HCL blocks -- found a %q block", path, blk.Type, blk.Type)
	}

	tree := genericTree{}
	for name, attr := range body.Attributes {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s: %s: config permits literal values only (no variables, functions, or interpolation): %s", path, name, diags.Error())
		}
		tree[name] = ctyToGeneric(val)
	}
	return tree, nil
}

// ctyToGeneric converts a fully-literal cty.Value (guaranteed by the
// expr.Value(nil) check above -- no unknowns, no marks) into the plain
// Go shape genericTree's merge/decode logic expects: string, bool,
// float64, genericTree, []any, or nil.
func ctyToGeneric(v cty.Value) any {
	if v.IsNull() {
		return nil
	}
	t := v.Type()
	switch {
	case t == cty.String:
		return v.AsString()
	case t == cty.Bool:
		return v.True()
	case t == cty.Number:
		f, _ := v.AsBigFloat().Float64()
		return f
	case t.IsObjectType() || t.IsMapType():
		out := genericTree{}
		it := v.ElementIterator()
		for it.Next() {
			k, ev := it.Element()
			out[k.AsString()] = ctyToGeneric(ev)
		}
		return out
	case t.IsTupleType() || t.IsListType() || t.IsSetType():
		out := make([]any, 0)
		it := v.ElementIterator()
		for it.Next() {
			_, ev := it.Element()
			out = append(out, ctyToGeneric(ev))
		}
		return out
	default:
		return nil
	}
}
