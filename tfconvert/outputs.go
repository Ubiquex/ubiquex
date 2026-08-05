package tfconvert

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ubiquex/ubiquex/blueprint"
)

// convertOutputs walks every `output "X" { value = ... }` block --
// UBI-128's own real outputs: mechanism (blueprint/decode.go's
// resolveOutputTarget) needs a target shaped "<resource-slug>.<attr...>",
// by SLUG, never a stack-qualified address the way an ordinary $ref
// needs -- so this is a small, deliberately separate mirror of
// convertTraversal's own resource-reference recognition rather than a
// reuse of it (the two need genuinely different return shapes).
//
// An output whose value isn't EXACTLY a plain, non-indexed reference into
// one non-for_each resource's own attribute -- a conditional, a function
// call, an interpolated string, or a reference into a for_each/count
// resource's own per-instance attribute (UBI-128's own real, permanent
// boundary: "a blueprint with a for_each resource cannot also declare
// outputs:", blueprint/decode.go) -- can't be mechanically represented at
// all and is declined via a core.Question, the output simply omitted from
// the converted Ubxfile's own outputs: block.
func (c *converter) convertOutputs() {
	if c.hasForEach && len(c.mod.outputs) > 0 {
		// blueprint/decode.go's own boundary is blanket, not per-output:
		// a blueprint with ANY for_each resource can't declare outputs:
		// at all, even one targeting a completely different, ordinary
		// resource (combining a per-iteration return list with named
		// outputs isn't supported, UBI-128/UBI-129) -- so every output {}
		// block in this module is declined up front, together, rather
		// than discovering the same blanket refusal independently once
		// per output.
		for _, ob := range c.mod.outputs {
			if len(ob.Labels) != 1 {
				continue
			}
			c.addQuestion(fmt.Sprintf("output %q: this module has a for_each/count resource, and a blueprint can't combine a for_each resource with outputs: (blueprint/decode.go's own permanent boundary, UBI-128/UBI-129) -- NOT carried over", ob.Labels[0]))
		}
		return
	}
	for _, ob := range c.mod.outputs {
		if len(ob.Labels) != 1 {
			continue
		}
		name := ob.Labels[0]
		valAttr, ok := ob.Body.Attributes["value"]
		if !ok {
			c.addQuestion(fmt.Sprintf("output %q has no value -- skipped", name))
			continue
		}

		if sensAttr, hasSens := ob.Body.Attributes["sensitive"]; hasSens {
			if v, diags := sensAttr.Expr.Value(nil); !diags.HasErrors() && v.Type() == cty.Bool && v.True() {
				c.addQuestion(fmt.Sprintf("output %q is marked sensitive = true in Terraform -- blueprint outputs: has no secret-handling concept of its own; treat this output's value as sensitive by convention", name))
			}
		}

		target, err := c.outputTarget(valAttr.Expr)
		if err != nil {
			c.addQuestion(fmt.Sprintf("output %q: %v -- this output was NOT carried over to the converted blueprint's outputs:", name, err))
			continue
		}
		c.outputs = append(c.outputs, blueprint.Output{Name: name, Target: target})
	}
}

// outputTarget recognizes exactly a plain, non-indexed "<type>.<name>.
// <attr...>" reference into one of this module's own resources, and
// renders it into resolveOutputTarget's own expected "<resource-slug>.
// <attr...>" wire form -- never a stack/type-qualified address (an
// output's own Target already names its resource unambiguously by the
// blueprint author's own declared slug alone, blueprint/decode.go).
func (c *converter) outputTarget(expr hclsyntax.Expression) (string, error) {
	if tmpl, ok := expr.(*hclsyntax.TemplateExpr); ok && len(tmpl.Parts) == 1 {
		if _, isLiteral := tmpl.Parts[0].(*hclsyntax.LiteralValueExpr); !isLiteral {
			expr = tmpl.Parts[0]
		}
	}
	st, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok {
		return "", fmt.Errorf("value %s isn't a plain resource reference", c.exprText(expr))
	}
	t := st.Traversal
	if len(t) < 3 {
		return "", fmt.Errorf("value %s isn't a plain <resource_type>.<name>.<attribute> reference", c.exprText(expr))
	}
	typeName := t.RootName()
	resName, ok := attrName(t[1])
	if !ok {
		return "", fmt.Errorf("indexed resource reference (%s) isn't supported", c.exprText(expr))
	}
	addr := typeName + "." + resName
	if !c.resourceAddrs[addr] {
		return "", fmt.Errorf("%s isn't a resource declared in this module", addr)
	}
	info, ok := c.resourceInfo[addr]
	if !ok {
		return "", fmt.Errorf("%s: internal ordering bug", addr)
	}
	if info.skipped {
		return "", fmt.Errorf("%s couldn't be converted (see its own question), so this output can't be either", addr)
	}
	if info.forEachParam != "" {
		return "", fmt.Errorf("%s is a for_each resource -- a blueprint with a for_each resource can't also declare outputs: (blueprint/decode.go's own permanent boundary, UBI-128/UBI-129)", addr)
	}
	var path []string
	for _, step := range t[2:] {
		seg, ok := attrName(step)
		if !ok {
			return "", fmt.Errorf("indexed attribute access (%s) isn't supported", c.exprText(expr))
		}
		path = append(path, seg)
	}
	return info.slug + "." + joinDot(path), nil
}
