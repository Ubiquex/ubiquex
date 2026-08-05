package tfconvert

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// exprCtx is the per-resource context convertExpr needs to resolve the
// two synthetic for_each/count tokens (each.value/each.key/count.index)
// -- mirrors blueprint/gogen.go's own goGenerator.currentDR precedent:
// "which resource is this rendering for" threaded transiently rather
// than stored globally.
type exprCtx struct {
	// forEachParam is the declared list-typed param name the CURRENT
	// resource iterates over, or "" for an ordinary (non-iterating)
	// resource -- set by resources.go before translating that resource's
	// own Config fields, exactly mirroring blueprint/decode.go's own
	// ForEach field.
	forEachParam string
}

// localResult memoizes one `local.X` resolution -- either its own
// successfully converted value, or the error that made it unsupported --
// so a local referenced from N different resources is only ever
// evaluated once, and a genuinely cyclic local chain is caught rather
// than recursing forever.
type localResult struct {
	value any
	err   error
}

// convertExpr translates one already-parsed HCL expression into a
// decoded, JSON-marshalable value in the SAME shape blueprint/decode.go
// already expects from an intent-provider draft: nil/bool/float64/string/
// map[string]any/[]any, a bare "{param_name}" string for a scalar
// reference (blueprint/decode.go's own placeholder grammar), or a
// {"$ref":{"to":"<addr>"}} marker map for a cross-resource reference
// (core/resolver/refs.go's own markerRef shape) -- see the package doc
// comment for the full "why this correspondence exists" account.
//
// Returns an error for anything outside this package's own deliberately
// narrow, mechanically-translatable vocabulary -- the caller (resources.go/
// outputs.go) turns that error into a core.Question and declines the one
// affected attribute, never propagating it into a hard Convert failure.
func (c *converter) convertExpr(expr hclsyntax.Expression, ctx exprCtx) (any, error) {
	switch e := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		return ctyToAny(e.Val)

	case *hclsyntax.TemplateExpr:
		return c.convertTemplate(e, ctx)

	case *hclsyntax.ScopeTraversalExpr:
		return c.convertTraversal(e.Traversal, ctx)

	case *hclsyntax.ParenthesesExpr:
		return c.convertExpr(e.Expression, ctx)

	case *hclsyntax.FunctionCallExpr:
		return c.convertFunctionCall(e, ctx)

	case *hclsyntax.IndexExpr:
		if name, ok := varRefName(e.Collection); ok && ctx.forEachParam != "" && name == ctx.forEachParam && isForEachIndexExpr(e.Key) {
			return "{" + ctx.forEachParam + "}", nil
		}
		return nil, fmt.Errorf("indexed expression %s isn't supported by the converter (only var.<for_each source>[count.index], the per-element value, is recognized)", c.exprText(e))

	case *hclsyntax.TupleConsExpr:
		out := make([]any, 0, len(e.Exprs))
		for _, sub := range e.Exprs {
			v, err := c.convertExpr(sub, ctx)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil

	case *hclsyntax.ObjectConsExpr:
		out := map[string]any{}
		for _, item := range e.Items {
			kv, diags := item.KeyExpr.Value(nil)
			if diags.HasErrors() || kv.Type() != cty.String {
				return nil, fmt.Errorf("object literal has a non-literal key (%s) -- not supported by the converter", c.exprText(item.KeyExpr))
			}
			v, err := c.convertExpr(item.ValueExpr, ctx)
			if err != nil {
				return nil, err
			}
			out[kv.AsString()] = v
		}
		return out, nil

	default:
		return nil, fmt.Errorf("%s (%T) needs real judgment, not mechanical translation -- not supported by the converter", c.exprText(expr), expr)
	}
}

// convertTemplate handles an HCL template/string expression. Per the HCL
// native-syntax spec, a template consisting of exactly one interpolation
// sequence and no surrounding literal text ("${x}" alone, or the
// unquoted bare expression the parser folds to the same shape) evaluates
// to x's OWN value, type preserved -- e.g. "${aws_vpc.this.id}" must
// still become a real {"$ref":...} marker map, not a string containing
// one, so that case is delegated to convertExpr directly rather than
// forced through the string-building path below.
//
// Every other template -- literal text mixed with one or more
// interpolations, e.g. "subnet-${var.name}-${count.index}" -- is built
// into ONE Go string, reusing the EXACT SAME embedding conventions
// blueprint/gogen.go's renderString/renderEmbeddedRefString already
// recognize: a var/each/count reference becomes an inline "{param_name}"
// token (renderString's own placeholder grammar), and a cross-resource
// reference becomes an inline {"$ref":{"to":"..."}} JSON marker
// substring (the SAME "JSON-embedded ref" convention an IAM policy
// document's own "Resource" field already relies on, core/resolver/
// refs.go) -- codegen splits and translates both at BUILD time, this
// package never needs its own second rendering path for mixed content.
func (c *converter) convertTemplate(e *hclsyntax.TemplateExpr, ctx exprCtx) (any, error) {
	if len(e.Parts) == 1 {
		if _, isLiteral := e.Parts[0].(*hclsyntax.LiteralValueExpr); !isLiteral {
			return c.convertExpr(e.Parts[0], ctx)
		}
	}

	var out string
	for _, part := range e.Parts {
		if lit, ok := part.(*hclsyntax.LiteralValueExpr); ok {
			s, err := ctyToAny(lit.Val)
			if err != nil {
				return nil, err
			}
			text, ok := s.(string)
			if !ok {
				return nil, fmt.Errorf("template literal segment isn't a string (%T)", s)
			}
			out += text
			continue
		}
		v, err := c.convertExpr(part, ctx)
		if err != nil {
			return nil, err
		}
		text, err := templateFragmentText(v)
		if err != nil {
			return nil, fmt.Errorf("interpolating %s: %w", c.exprText(part), err)
		}
		out += text
	}
	return out, nil
}

// templateFragmentText renders one already-converted interpolation
// value as the literal text to splice into a template's own resulting
// Go string -- a bare "{param}" token or plain string passes through
// verbatim, a $ref marker map is re-encoded as its own canonical JSON
// marker text (embeddedRefPattern's own expected shape, blueprint/
// decode.go), a scalar renders as its own textual form. A $fn marker
// (cidrsubnet(), etc.) or a list/object value has no sensible textual
// form to interpolate -- declined.
func templateFragmentText(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case float64:
		return numberText(t), nil
	case bool:
		return boolText(t), nil
	case map[string]any:
		if _, ok := t["$ref"]; ok {
			b, err := marshalCompact(t)
			if err != nil {
				return "", err
			}
			return string(b), nil
		}
		return "", fmt.Errorf("can't interpolate a non-$ref object into a string")
	default:
		return "", fmt.Errorf("can't interpolate a %T into a string", v)
	}
}

// convertTraversal handles a bare (non-template) reference -- var.X,
// each.value/each.key, count.index, local.X, or a cross-resource
// "<type>.<name>[.<attr>...]" address.
func (c *converter) convertTraversal(t hcl.Traversal, ctx exprCtx) (any, error) {
	if len(t) == 0 {
		return nil, fmt.Errorf("empty reference")
	}
	root := t.RootName()

	switch root {
	case "var":
		if len(t) != 2 {
			return nil, fmt.Errorf("var.* reference has an unsupported shape (%s) -- only a plain var.<name> reference is recognized", traversalText(t))
		}
		name, ok := attrName(t[1])
		if !ok {
			return nil, fmt.Errorf("var[...] indexed reference isn't supported")
		}
		if c.unsupportedParams[name] {
			return nil, fmt.Errorf("references var.%s, which was dropped from the converted blueprint (see the question about variable %q)", name, name)
		}
		if _, ok := c.paramType[name]; !ok {
			return nil, fmt.Errorf("references var.%s, which isn't declared in any variable {} block", name)
		}
		return "{" + name + "}", nil

	case "each":
		if ctx.forEachParam == "" {
			return nil, fmt.Errorf("each.* referenced outside a for_each/count resource")
		}
		if len(t) != 2 {
			return nil, fmt.Errorf("each.* reference has an unsupported shape (%s)", traversalText(t))
		}
		name, ok := attrName(t[1])
		if !ok || (name != "value" && name != "key") {
			return nil, fmt.Errorf("only each.value/each.key are recognized (got %s)", traversalText(t))
		}
		return "{" + ctx.forEachParam + "}", nil

	case "count":
		if ctx.forEachParam == "" {
			return nil, fmt.Errorf("count.* referenced outside a for_each/count resource")
		}
		if len(t) != 2 {
			return nil, fmt.Errorf("count.* reference has an unsupported shape (%s)", traversalText(t))
		}
		name, ok := attrName(t[1])
		if !ok || name != "index" {
			return nil, fmt.Errorf("only count.index is recognized (got %s)", traversalText(t))
		}
		return "{" + ctx.forEachParam + "_index}", nil

	case "local":
		if len(t) < 2 {
			return nil, fmt.Errorf("malformed local.* reference")
		}
		name, ok := attrName(t[1])
		if !ok {
			return nil, fmt.Errorf("local[...] indexed reference isn't supported")
		}
		val, err := c.resolveLocal(name)
		if err != nil {
			return nil, err
		}
		if len(t) > 2 {
			return nil, fmt.Errorf("local.%s.%s: nested access into a resolved local isn't supported -- reference the whole local, or restructure the module", name, traversalText(t[2:]))
		}
		return val, nil

	case "data":
		return nil, fmt.Errorf("data source references aren't supported by the converter (out of scope, docs/blueprint.md)")

	default:
		if len(t) < 2 {
			return nil, fmt.Errorf("unrecognized reference %q", root)
		}
		resName, ok := attrName(t[1])
		if !ok {
			return nil, fmt.Errorf("%s[...]: indexed resource reference isn't supported (only a plain <type>.<name>.<attr> reference is recognized)", root)
		}
		addr := root + "." + resName
		if !c.resourceAddrs[addr] {
			return nil, fmt.Errorf("references %s, which isn't a resource declared in this module (module/data/provider blocks aren't supported)", addr)
		}
		info, ok := c.resourceInfo[addr]
		if !ok {
			return nil, fmt.Errorf("references %s before it's been translated -- internal ordering bug", addr)
		}
		if info.skipped {
			return nil, fmt.Errorf("references %s, which the converter couldn't translate (see its own question) -- this reference can't be translated either", addr)
		}
		if info.forEachParam != "" {
			return nil, fmt.Errorf("references %s directly, but it's a for_each resource -- an individual iteration's own instance isn't addressable (matches blueprint's own decode-time restriction); only the compiled function's own returned list exposes its instances", addr)
		}
		if len(t) < 3 {
			return nil, fmt.Errorf("%s: a bare resource reference with no attribute isn't supported", addr)
		}
		var path []string
		for _, step := range t[2:] {
			seg, ok := attrName(step)
			if !ok {
				return nil, fmt.Errorf("%s: indexed attribute access isn't supported", addr)
			}
			path = append(path, seg)
		}
		return map[string]any{"$ref": map[string]any{"to": c.stack + "." + root + "." + info.slug + "." + joinDot(path)}}, nil
	}
}

// convertFunctionCall recognizes a deliberately narrow allow-list of
// Terraform built-in functions -- everything else (merge(), format(),
// concat(), try(), coalesce(), jsonencode(), lookup(), join(), ...) is
// declined, matching UBI-125's own itemized scope ("Built-in functions
// used inside these blocks need porting as real generated helper
// functions... confirm which built-ins are common enough to be worth
// porting now vs. flagging as unsupported").
//
//   - element(var.<for_each source>, count.index) / var.<source>[count.index]
//     -- the per-element value, exactly what a for_each resource's own bare
//     "{param}" token already means (docs/blueprint.md's UBI-129 section).
//   - index(var.<for_each source>, each.value) -- the per-element position,
//     exactly what "{param}_index" already means.
//   - cidrsubnet(prefix, newbits, netnum) -- the one built-in UBI-125's own
//     ticket names explicitly as worth porting -- becomes a {"$fn":{"name":
//     "cidrsubnet","args":[...]}} marker, a real, additive, blueprint-
//     package-private convention blueprint/gogen.go's own renderAny
//     recognizes and compiles into a call to a real ported Go helper
//     (blueprint/cidrsubnet.go) -- Go codegen only this ticket, a real,
//     named, not-silently-done gap for TS/Python (see docs/blueprint.md).
func (c *converter) convertFunctionCall(e *hclsyntax.FunctionCallExpr, ctx exprCtx) (any, error) {
	switch e.Name {
	case "element":
		if len(e.Args) == 2 {
			if name, ok := varRefName(e.Args[0]); ok && ctx.forEachParam != "" && name == ctx.forEachParam && isForEachIndexExpr(e.Args[1]) {
				return "{" + ctx.forEachParam + "}", nil
			}
		}
	case "index":
		if len(e.Args) == 2 {
			if name, ok := varRefName(e.Args[0]); ok && ctx.forEachParam != "" && name == ctx.forEachParam && isForEachValueExpr(e.Args[1]) {
				return "{" + ctx.forEachParam + "_index}", nil
			}
		}
	case "cidrsubnet":
		if len(e.Args) != 3 {
			return nil, fmt.Errorf("cidrsubnet() expects exactly 3 arguments, got %d", len(e.Args))
		}
		args := make([]any, 0, 3)
		for _, a := range e.Args {
			v, err := c.convertExpr(a, ctx)
			if err != nil {
				return nil, fmt.Errorf("cidrsubnet(): %w", err)
			}
			args = append(args, v)
		}
		return map[string]any{"$fn": map[string]any{"name": "cidrsubnet", "args": args}}, nil
	}
	return nil, fmt.Errorf("function call %s isn't supported by the converter (only element()/index() against the current for_each source, and cidrsubnet(), are recognized)", c.exprText(e))
}

// varRefName reports whether expr is a plain "var.<name>" reference.
func varRefName(expr hclsyntax.Expression) (string, bool) {
	st, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(st.Traversal) != 2 || st.Traversal.RootName() != "var" {
		return "", false
	}
	return attrName(st.Traversal[1])
}

func isForEachIndexExpr(expr hclsyntax.Expression) bool {
	st, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(st.Traversal) != 2 || st.Traversal.RootName() != "count" {
		return false
	}
	name, ok := attrName(st.Traversal[1])
	return ok && name == "index"
}

func isForEachValueExpr(expr hclsyntax.Expression) bool {
	st, ok := expr.(*hclsyntax.ScopeTraversalExpr)
	if !ok || len(st.Traversal) != 2 || st.Traversal.RootName() != "each" {
		return false
	}
	name, ok := attrName(st.Traversal[1])
	return ok && (name == "value" || name == "key")
}

func attrName(step hcl.Traverser) (string, bool) {
	if a, ok := step.(hcl.TraverseAttr); ok {
		return a.Name, true
	}
	return "", false
}

func joinDot(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "."
		}
		out += p
	}
	return out
}

func traversalText(t hcl.Traversal) string {
	out := ""
	for i, step := range t {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			out += s.Name
		case hcl.TraverseAttr:
			if i > 0 {
				out += "."
			}
			out += s.Name
		default:
			out += "[...]"
		}
	}
	return out
}

// resolveLocal resolves and memoizes local.<name> -- see localResult's
// own doc comment.
func (c *converter) resolveLocal(name string) (any, error) {
	if r, ok := c.localCache[name]; ok {
		return r.value, r.err
	}
	if c.localStack == nil {
		c.localStack = map[string]bool{}
	}
	if c.localStack[name] {
		err := fmt.Errorf("local.%s participates in a dependency cycle with another local", name)
		c.localCache[name] = localResult{err: err}
		return nil, err
	}

	var expr hclsyntax.Expression
	for _, lb := range c.mod.locals {
		if attr, ok := lb.Body.Attributes[name]; ok {
			expr = attr.Expr
			break
		}
	}
	if expr == nil {
		err := fmt.Errorf("local.%s is referenced but never declared in any locals {} block", name)
		c.localCache[name] = localResult{err: err}
		return nil, err
	}

	c.localStack[name] = true
	val, err := c.convertExpr(expr, exprCtx{}) // locals never have their own count/each scope
	delete(c.localStack, name)
	if err != nil {
		err = fmt.Errorf("local.%s: %w", name, err)
	}
	c.localCache[name] = localResult{value: val, err: err}
	return val, err
}

// exprText renders expr's own exact original source text, for an error/
// Question message -- best-effort (returns a placeholder rather than
// panicking if the range can't be sliced, which should never happen in
// practice since every expr here comes from a body this package itself
// parsed and kept the source of).
func (c *converter) exprText(expr hclsyntax.Expression) string {
	rng := expr.Range()
	src, ok := c.mod.src[rng.Filename]
	if !ok || rng.Start.Byte < 0 || rng.End.Byte > len(src) || rng.Start.Byte > rng.End.Byte {
		return "<expression>"
	}
	return string(src[rng.Start.Byte:rng.End.Byte])
}
