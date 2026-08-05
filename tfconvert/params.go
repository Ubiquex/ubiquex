package tfconvert

import (
	"fmt"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ubiquex/ubiquex/blueprint"
)

// convertParams walks every `variable "X" { ... }` block -- direct 1:1
// per UBI-125's own itemized scope: name, type, default, required. A
// variable whose own `type:` isn't one of blueprint's five recognized
// param types (string/number/bool/list(string)/list(number)) -- a map,
// object, set, tuple, list(any)/list(bool), or a variable with no `type:`
// at all (Terraform's own implicit "any") -- is a real, named limitation
// of the TARGET format, not a bug in this converter: recorded as a
// non-blocking core.Question and the param is dropped entirely (any
// resource attribute later referencing it becomes its own Question too,
// via c.unsupportedParams -- see resources.go/expr.go).
func (c *converter) convertParams() error {
	c.paramType = map[string]blueprint.ParamType{}
	c.unsupportedParams = map[string]bool{}

	for _, vb := range c.mod.variables {
		if len(vb.Labels) != 1 {
			return fmt.Errorf("tfconvert: %s: variable block wants exactly 1 label, got %d", vb.TypeRange.Filename, len(vb.Labels))
		}
		name := vb.Labels[0]

		typeAttr, hasType := vb.Body.Attributes["type"]
		if !hasType {
			c.addQuestion(fmt.Sprintf("variable %q has no type: -- Terraform treats this as \"any\", which blueprint params: has no equivalent for; declare an explicit type in the source module to convert it", name))
			c.unsupportedParams[name] = true
			continue
		}
		typ, ok := paramTypeFromExpr(typeAttr.Expr)
		if !ok {
			c.addQuestion(fmt.Sprintf("variable %q declares type %s, which blueprint params: doesn't support (only string, number, bool, list(string), list(number)) -- dropped from the converted blueprint", name, c.exprText(typeAttr.Expr)))
			c.unsupportedParams[name] = true
			continue
		}

		if _, hasValidation := findBlock(vb.Body, "validation"); hasValidation {
			c.addQuestion(fmt.Sprintf("variable %q has a validation {} block -- blueprint params: has no validation-rule concept; the rule was NOT carried over, callers can pass any value", name))
		}
		if sensAttr, hasSens := vb.Body.Attributes["sensitive"]; hasSens {
			if v, diags := sensAttr.Expr.Value(nil); !diags.HasErrors() && v.Type() == cty.Bool && v.True() {
				c.addQuestion(fmt.Sprintf("variable %q is marked sensitive = true in Terraform -- blueprint params: has no secret-handling concept of its own; treat this parameter's value as sensitive by convention when calling the converted blueprint", name))
			}
		}

		p := blueprint.Param{Name: name, Type: typ}
		defaultAttr, hasDefault := vb.Body.Attributes["default"]
		switch {
		case !hasDefault:
			p.Required = true
		case typ.IsList():
			// ubxfile.go's own parseDefaultValue refuses a default on a
			// list-typed param outright (UBI-129: a list param is always
			// consumed by exactly one for_each resource, which has no
			// un-given-default concept) -- so a Terraform default here
			// can't be carried over as a real default, only downgraded to
			// "required", a genuine behavioral change worth naming.
			c.addQuestion(fmt.Sprintf("variable %q has a Terraform default, but blueprint list-typed params: must always be required (UBI-129) -- callers must now supply it explicitly on every call", name))
			p.Required = true
		default:
			def, ok := scalarDefaultValue(typ, defaultAttr.Expr)
			if !ok {
				c.addQuestion(fmt.Sprintf("variable %q's default value isn't a plain literal blueprint can represent -- declared required instead", name))
				p.Required = true
			} else {
				p.Default = def
			}
		}

		c.paramType[name] = typ
		c.params = append(c.params, p)
	}
	return nil
}

// paramTypeFromExpr recognizes exactly the five HCL type-constraint
// shapes blueprint's own params: vocabulary supports -- a bare
// string/number/bool identifier, or a single-argument list(...) call
// wrapping one of the two list-eligible element types. Anything else
// (map(...), object({...}), set(...), tuple([...]), list(bool),
// list(any), optional(...)) returns ok=false, never a best-effort guess.
func paramTypeFromExpr(expr hclsyntax.Expression) (blueprint.ParamType, bool) {
	switch e := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if len(e.Traversal) != 1 {
			return "", false
		}
		switch e.Traversal.RootName() {
		case "string":
			return blueprint.ParamString, true
		case "number":
			return blueprint.ParamNumber, true
		case "bool":
			return blueprint.ParamBool, true
		}
		return "", false
	case *hclsyntax.FunctionCallExpr:
		if e.Name != "list" || len(e.Args) != 1 {
			return "", false
		}
		inner, ok := e.Args[0].(*hclsyntax.ScopeTraversalExpr)
		if !ok || len(inner.Traversal) != 1 {
			return "", false
		}
		switch inner.Traversal.RootName() {
		case "string":
			return blueprint.ParamListString, true
		case "number":
			return blueprint.ParamListNumber, true
		}
		return "", false
	default:
		return "", false
	}
}

// scalarDefaultValue evaluates a variable's own default: expression --
// always a plain literal in real Terraform (a variable's default: can't
// reference another variable or call a function returning a
// non-constant) -- into blueprint.Param.Default's own expected Go shape
// (string/int/bool, matching Type, per ubxfile.go's parseDefaultValue).
// ok=false for anything that doesn't evaluate as a pure literal (a
// defensive fallback -- declared required instead, never a guessed
// value) or whose evaluated type doesn't match typ.
func scalarDefaultValue(typ blueprint.ParamType, expr hclsyntax.Expression) (any, bool) {
	v, diags := expr.Value(nil)
	if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() {
		return nil, false
	}
	switch typ {
	case blueprint.ParamString:
		if v.Type() != cty.String {
			return nil, false
		}
		return v.AsString(), true
	case blueprint.ParamBool:
		if v.Type() != cty.Bool {
			return nil, false
		}
		return v.True(), true
	case blueprint.ParamNumber:
		if v.Type() != cty.Number {
			return nil, false
		}
		f, _ := v.AsBigFloat().Int64()
		return int(f), true
	default:
		return nil, false
	}
}

// findBlock reports whether body has at least one nested block of the
// given type -- used for meta-blocks (validation {}, lifecycle {},
// provisioner {}, connection {}) whose presence alone is worth a Question
// even though this package makes no attempt to interpret their own
// content.
func findBlock(body *hclsyntax.Body, blockType string) (*hclsyntax.Block, bool) {
	for _, b := range body.Blocks {
		if b.Type == blockType {
			return b, true
		}
	}
	return nil, false
}
