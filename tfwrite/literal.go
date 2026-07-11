package tfwrite

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// resolveTarget locates the byte range of the value a dot-path refers to
// within block, validating every step along the way. path is split on "."
// — the same dot-notation docs/schema.md's Modification.After (and
// core/state.go's dotSet/diffAttributes) already use, so a path here is
// exactly what a drift_adopt proposal's delta.modifies already carries.
//
// If the path's *final* segment names a key that doesn't exist yet, but
// everything before it resolved to an existing literal object, resolveTarget
// returns an *insertableKey error instead of a generic decline — the
// common "someone added a whole new tag in the console" shape (UBI-11
// stage 2 follow-up). A missing *intermediate* segment (there's more path
// left after it) is still a hard decline: inserting a new nested
// structure, not just one scalar key, stays out of scope.
//
// The returned expression is whichever one resolveTarget got furthest
// with before failing (nil if not even the top-level attribute exists) —
// exprText uses it to render the "expression it can't safely overwrite"
// for a decline report, even when the failure is deep in a nested path.
func resolveTarget(block *hclsyntax.Block, path string) (hcl.Range, hclsyntax.Expression, error) {
	segments := strings.Split(path, ".")

	attr, ok := block.Body.Attributes[segments[0]]
	if !ok {
		return hcl.Range{}, nil, fmt.Errorf("attribute %q is not present in this resource block (write-back never adds new attributes)", segments[0])
	}

	expr := attr.Expr
	rest := segments[1:]
	for i, seg := range rest {
		obj, ok := expr.(*hclsyntax.ObjectConsExpr)
		if !ok {
			return hcl.Range{}, expr, fmt.Errorf("%q is not a literal object, can't navigate into %q",
				strings.Join(segments[:i+1], "."), seg)
		}
		item, ok := findObjectItem(obj, seg)
		if !ok {
			isFinalSegment := i == len(rest)-1
			if isFinalSegment {
				return hcl.Range{}, expr, &insertableKey{obj: obj, key: seg}
			}
			return hcl.Range{}, expr, fmt.Errorf("key %q not present in the existing %q object, and it isn't the final segment of %q (write-back only inserts a single new leaf key, not a new nested structure)",
				seg, strings.Join(segments[:i+1], "."), path)
		}
		expr = item.ValueExpr
	}

	if _, diags := expr.Value(nil); diags.HasErrors() {
		return hcl.Range{}, expr, fmt.Errorf("current value is not a literal: %s", diagSummary(diags))
	}
	return expr.Range(), expr, nil
}

// findObjectItem finds the item in obj whose key evaluates to the literal
// string key. A key that isn't itself a literal string (a computed key —
// rare, but HCL technically allows `{ (var.k) = v }`) is skipped rather
// than erroring: it simply can't be the key we're looking for by name, any
// more than a differently-named literal key could be.
func findObjectItem(obj *hclsyntax.ObjectConsExpr, key string) (*hclsyntax.ObjectConsItem, bool) {
	for i := range obj.Items {
		item := &obj.Items[i]
		keyVal, diags := item.KeyExpr.Value(nil)
		if diags.HasErrors() || keyVal.Type() != cty.String {
			continue
		}
		if keyVal.AsString() == key {
			return item, true
		}
	}
	return nil, false
}

// renderLiteral converts a Modification.After JSON value into HCL literal
// source bytes (via go-cty, already a project dependency — see
// provider/ctyvalue.go — and hclwrite.TokensForValue, the one place this
// package uses hclwrite at all: rendering a value's tokens, never editing
// a file's own token stream directly).
func renderLiteral(raw json.RawMessage) ([]byte, error) {
	ty, err := ctyjson.ImpliedType(raw)
	if err != nil {
		return nil, fmt.Errorf("implied type: %w", err)
	}
	val, err := ctyjson.Unmarshal(raw, ty)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return hclwrite.TokensForValue(val).Bytes(), nil
}

func diagSummary(diags hcl.Diagnostics) string {
	if len(diags) == 0 {
		return "unknown reason"
	}
	return diags[0].Summary
}
