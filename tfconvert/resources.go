package tfconvert

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// resourceInfo is one resource block's own translation OUTCOME, decided
// in a planning pass BEFORE any Config attribute is translated (see
// convertResources below) -- every OTHER resource's own attribute
// translation needs to know a referenced resource's final slug/for_each
// shape (or that it was skipped) regardless of whether that resource's
// own block appears earlier or later in the module's declaration order,
// exactly mirroring blueprint/decode.go's own two-pass "wrap then
// render" precedent (gogen.go).
type resourceInfo struct {
	slug         string // this resource's own final blueprint Name (== the HCL label, or "<label>-{param}" for a for_each resource)
	forEachParam string // "" for an ordinary resource
	skipped      bool
}

// convertResources translates every `resource "<type>" "<name>" { ... }`
// block into a resolver.ResourceIntent, in two passes -- see
// resourceInfo's own doc comment for why a single pass isn't enough.
func (c *converter) convertResources() {
	for _, rb := range c.mod.resources {
		addr := rb.Labels[0] + "." + rb.Labels[1]
		info, reason := c.planResource(rb)
		c.resourceInfo[addr] = info
		if info.skipped {
			c.skipped = append(c.skipped, addr)
			c.addQuestion(fmt.Sprintf("resource %q: %s -- this resource was NOT converted; nothing referencing it can be either", addr, reason), c.stack+"."+addr)
		}
	}

	for _, rb := range c.mod.resources {
		addr := rb.Labels[0] + "." + rb.Labels[1]
		info := c.resourceInfo[addr]
		if info.skipped {
			continue
		}
		if info.forEachParam != "" {
			c.hasForEach = true
		}
		c.resources = append(c.resources, c.translateResource(rb, addr, info))
	}
}

// planResource decides ONE resource block's own translation outcome --
// in particular, whether its count/for_each meta-argument (if any)
// matches one of the two deterministically-convertible shapes UBI-129's
// own for_each design already provides a real target for:
//
//   - count = length(var.<list param>) -- the most common real-world
//     idiom (confirmed live against the real, public
//     terraform-aws-modules/terraform-aws-vpc module while designing this
//     package -- its own private/public subnet resources use exactly this
//     shape) -- becomes a for_each resource iterating that param.
//   - for_each = var.<list param> or for_each = toset(var.<list param>)
//     -- Terraform's own more idiomatic for_each form -- same target.
//
// Anything else (count/for_each driven by a local, a conditional, a
// literal number, a map, an expression combining two variables, ...)
// needs real judgment UBI-125's own ticket explicitly reserves for a
// human, not a guess -- the whole resource is skipped, loudly, via a
// core.Question, rather than silently mistranslating a numeric/
// conditional count into some approximation.
func (c *converter) planResource(rb *hclsyntax.Block) (resourceInfo, string) {
	label := rb.Labels[1]
	_, hasCount := rb.Body.Attributes["count"]
	_, hasForEach := rb.Body.Attributes["for_each"]

	switch {
	case hasCount && hasForEach:
		return resourceInfo{skipped: true}, "declares both count and for_each (mutually exclusive in Terraform itself) -- malformed source, or two conflicting edits"
	case hasCount:
		expr := rb.Body.Attributes["count"].Expr
		name, ok := lengthOfVarList(expr)
		if !ok {
			return resourceInfo{skipped: true}, fmt.Sprintf("count = %s isn't a recognized shape -- only count = length(var.<list param>) converts to a for_each resource", c.exprText(expr))
		}
		if reason := c.checkForEachSource(name); reason != "" {
			return resourceInfo{skipped: true}, reason
		}
		return resourceInfo{slug: label + "-{" + name + "}", forEachParam: name}, ""
	case hasForEach:
		expr := rb.Body.Attributes["for_each"].Expr
		name, ok := forEachSourceVar(expr)
		if !ok {
			return resourceInfo{skipped: true}, fmt.Sprintf("for_each = %s isn't a recognized shape -- only for_each = var.<list param> or for_each = toset(var.<list param>) converts", c.exprText(expr))
		}
		if reason := c.checkForEachSource(name); reason != "" {
			return resourceInfo{skipped: true}, reason
		}
		return resourceInfo{slug: label + "-{" + name + "}", forEachParam: name}, ""
	default:
		return resourceInfo{slug: label}, ""
	}
}

// checkForEachSource confirms name is a real, declared list(string)/
// list(number) param -- empty return means OK.
func (c *converter) checkForEachSource(name string) string {
	if c.unsupportedParams[name] {
		return fmt.Sprintf("its own for_each source var.%s was dropped from the converted blueprint (see the question about that variable)", name)
	}
	typ, ok := c.paramType[name]
	if !ok {
		return fmt.Sprintf("its own for_each source var.%s isn't declared in any variable {} block", name)
	}
	if !typ.IsList() {
		return fmt.Sprintf("its own for_each source var.%s is declared %q, not a list type -- only iterating over a list(string)/list(number) param converts", name, typ)
	}
	return ""
}

// lengthOfVarList recognizes exactly "length(var.<name>)".
func lengthOfVarList(expr hclsyntax.Expression) (string, bool) {
	fc, ok := expr.(*hclsyntax.FunctionCallExpr)
	if !ok || fc.Name != "length" || len(fc.Args) != 1 {
		return "", false
	}
	return varRefName(fc.Args[0])
}

// forEachSourceVar recognizes "var.<name>" or "toset(var.<name>)".
func forEachSourceVar(expr hclsyntax.Expression) (string, bool) {
	if name, ok := varRefName(expr); ok {
		return name, true
	}
	fc, ok := expr.(*hclsyntax.FunctionCallExpr)
	if !ok || fc.Name != "toset" || len(fc.Args) != 1 {
		return "", false
	}
	return varRefName(fc.Args[0])
}

// translateResource converts rb's own top-level Config attributes
// (every attribute besides the meta-arguments handled specially below)
// and depends_on, per-attribute -- one unsupported attribute is
// recorded as its own core.Question and dropped (writeback/writeback.go's
// own established "decline the one unsafe piece, not the whole
// document" precedent), never aborting the whole resource, matching
// planResource's own already-committed decision that this resource IS
// convertible at all.
func (c *converter) translateResource(rb *hclsyntax.Block, addr string, info resourceInfo) resolver.ResourceIntent {
	ctx := exprCtx{forEachParam: info.forEachParam}

	keys := make([]string, 0, len(rb.Body.Attributes))
	for k := range rb.Body.Attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	config := map[string]any{}
	var deps []string
	for _, k := range keys {
		switch k {
		case "count", "for_each":
			continue
		case "provider":
			c.addQuestion(fmt.Sprintf("resource %q has an explicit provider = ... meta-argument -- blueprint has no provider-alias concept; ignored (the converted blueprint always uses whatever provider the calling stack configures)", addr), c.stack+"."+addr)
			continue
		case "depends_on":
			ds, ok := c.convertDependsOn(rb.Body.Attributes[k].Expr)
			if !ok {
				c.addQuestion(fmt.Sprintf("resource %q's depends_on isn't a recognized shape (want a literal list of plain resource references) -- dropped", addr), c.stack+"."+addr)
			} else {
				deps = ds
			}
			continue
		}
		attr := rb.Body.Attributes[k]
		v, err := c.convertExpr(attr.Expr, ctx)
		if err != nil {
			c.addQuestion(fmt.Sprintf("resource %q attribute %q: %v -- attribute dropped from the converted blueprint", addr, k, err), c.stack+"."+addr+"."+k)
			continue
		}
		config[k] = v
	}

	for _, nb := range rb.Body.Blocks {
		switch nb.Type {
		case "lifecycle":
			c.addQuestion(fmt.Sprintf("resource %q has a lifecycle {} block -- blueprint has no lifecycle-rule concept; the rule was NOT carried over", addr), c.stack+"."+addr)
		case "provisioner", "connection":
			c.addQuestion(fmt.Sprintf("resource %q has a %s {} block -- not supported by the converter; NOT carried over", addr, nb.Type), c.stack+"."+addr)
		default:
			c.addQuestion(fmt.Sprintf("resource %q has a nested %q {} block -- the converter only translates top-level attributes; this nested configuration was NOT carried over", addr, nb.Type), c.stack+"."+addr)
		}
	}

	cfgJSON, err := json.Marshal(config)
	if err != nil {
		// config only ever holds values convertExpr itself already
		// produced (nil/bool/float64/string/map[string]any/[]any, all
		// unconditionally JSON-marshalable) -- a failure here would be
		// an internal bug in this package, not a real module's own
		// fault, so this fails loudly rather than silently emitting an
		// empty/wrong config.
		panic(fmt.Sprintf("tfconvert: resource %q: config failed to marshal: %v", addr, err))
	}

	return resolver.ResourceIntent{
		Type:      rb.Labels[0],
		Name:      info.slug,
		Op:        resolver.OpCreate,
		Config:    cfgJSON,
		DependsOn: deps,
		ForEach:   info.forEachParam,
	}
}

// convertDependsOn recognizes a literal depends_on = [aws_x.y, ...] list
// of plain resource references -- anything else declines the WHOLE list
// (ok=false), never a partial one.
func (c *converter) convertDependsOn(expr hclsyntax.Expression) ([]string, bool) {
	tc, ok := expr.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil, false
	}
	var out []string
	for _, sub := range tc.Exprs {
		st, ok := sub.(*hclsyntax.ScopeTraversalExpr)
		if !ok || len(st.Traversal) != 2 {
			return nil, false
		}
		typeName := st.Traversal.RootName()
		resName, ok := attrName(st.Traversal[1])
		if !ok {
			return nil, false
		}
		targetAddr := typeName + "." + resName
		if !c.resourceAddrs[targetAddr] {
			return nil, false
		}
		info, ok := c.resourceInfo[targetAddr]
		if !ok || info.skipped || info.forEachParam != "" {
			return nil, false
		}
		out = append(out, c.stack+"."+typeName+"."+info.slug)
	}
	return out, true
}
