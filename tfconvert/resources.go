package tfconvert

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ubiquex/ubiquex/blueprint/spec"
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
	// createIfTerms are the signed bool param names that must all hold for
	// this resource to be created -- nil for an unconditional one. Each
	// entry is "name" or "!name". Mutually exclusive with forEachParam by
	// construction (both come from the same count attribute, and
	// decodeBlueprint refuses the combination too).
	createIfTerms []string
	skipped       bool
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

	c.disambiguateSlugs()

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
		if terms, ok := conditionalCountTerms(expr); ok {
			// UBI-125: `count = var.create ? 1 : 0`, the house style
			// across terraform-aws-modules and the single shape that
			// blocked converting essentially that whole ecosystem.
			for _, term := range terms {
				name, _ := strings.CutPrefix(term, "!")
				if reason := c.checkCreateIfSource(name); reason != "" {
					return resourceInfo{skipped: true}, reason
				}
			}
			return resourceInfo{slug: label, createIfTerms: terms}, ""
		}
		name, ok := lengthOfVarList(expr)
		if !ok {
			return resourceInfo{skipped: true}, fmt.Sprintf("count = %s isn't a recognized shape -- only count = length(var.<list param>) and count = var.<bool param> ? 1 : 0 convert", c.exprText(expr))
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

// disambiguateSlugs rewrites any blueprint slug that more than one
// converted resource would otherwise claim.
//
// Terraform labels are unique per TYPE, so `aws_sqs_queue.this` and
// `aws_sqs_queue_policy.this` are distinct addresses and both perfectly
// legal. A blueprint slug is not type-qualified for identifier purposes:
// every generator derives its own resource identifier from the name
// alone, so both would produce `This` and codegen refuses the collision.
// "this" is the near-universal Terraform label, so real modules hit this
// constantly -- terraform-aws-sqs has five resources named "this".
//
// Only colliding slugs are rewritten, so a module without collisions
// converts byte-identically to before. The rewrite prefixes the resource
// type with its provider prefix stripped ("aws_sqs_queue_policy" ->
// "sqs_queue_policy-this"), which is derived from the source rather than
// invented, and stays stable across runs.
//
// The underlying limitation is in codegen rather than here: an identifier
// derived from the name alone cannot distinguish two types sharing a
// label. Fixing it there would also help authored blueprints, and is
// deliberately not attempted in this change.
func (c *converter) disambiguateSlugs() {
	bySlug := map[string][]string{} // slug -> addrs claiming it
	for addr, info := range c.resourceInfo {
		if info.skipped {
			continue
		}
		bySlug[info.slug] = append(bySlug[info.slug], addr)
	}
	for slug, addrs := range bySlug {
		if len(addrs) < 2 {
			continue
		}
		sort.Strings(addrs) // deterministic, though every one is rewritten
		for _, addr := range addrs {
			info := c.resourceInfo[addr]
			typeName := addr[:strings.Index(addr, ".")]
			info.slug = stripProviderPrefix(typeName) + "-" + slug
			c.resourceInfo[addr] = info
		}
	}
}

// stripProviderPrefix drops the leading provider segment from a Terraform
// resource type ("aws_sqs_queue_policy" -> "sqs_queue_policy"), which is
// noise in a slug: every resource in one converted module shares it.
func stripProviderPrefix(typeName string) string {
	if i := strings.Index(typeName, "_"); i >= 0 {
		return typeName[i+1:]
	}
	return typeName
}

// checkCreateIfSource confirms name is a real, declared bool param --
// empty return means OK. Mirrors checkForEachSource exactly.
func (c *converter) checkCreateIfSource(name string) string {
	if c.unsupportedParams[name] {
		return fmt.Sprintf("its own conditional-count source var.%s was dropped from the converted blueprint (see the question about that variable)", name)
	}
	typ, ok := c.paramType[name]
	if !ok {
		return fmt.Sprintf("its own conditional-count source var.%s isn't declared in any variable {} block", name)
	}
	if typ != spec.ParamBool {
		return fmt.Sprintf("its own conditional-count source var.%s is declared %q, not bool -- only a bool param converts to a conditional resource", name, typ)
	}
	return ""
}

// conditionalCountTerms recognizes "<conjunction> ? 1 : 0", the shape
// Terraform modules use to make a resource optional, where the condition
// is a conjunction of declared bool params, each optionally negated:
//
//	var.create ? 1 : 0
//	var.create && var.create_dlq ? 1 : 0
//	var.create && !var.create_dlq ? 1 : 0
//
// Returns one signed term per conjunct, in source order.
//
// Deliberately closed. A conjunction with optional negation is what
// terraform-aws-modules actually writes, and it is the largest shape that
// stays mechanically translatable. Everything else falls through to the
// unrecognized-count question rather than being approximated:
//
//   - `? 0 : 1` (inverted) reads as "create when false", which is
//     expressible as a negated term but is not what the source says, and
//     a converter that silently rewrites the polarity of a create flag is
//     changing which resources exist.
//   - `a || b` (disjunction) has no conjunction to express it.
//   - `length(var.x) > 0` (derived) is not a declared bool param at all.
//
// A converter that guessed at any of these would silently change which
// resources a module creates.
func conditionalCountTerms(expr hclsyntax.Expression) ([]string, bool) {
	cond, ok := expr.(*hclsyntax.ConditionalExpr)
	if !ok {
		return nil, false
	}
	if !isIntLiteral(cond.TrueResult, 1) || !isIntLiteral(cond.FalseResult, 0) {
		return nil, false
	}
	return conjunctionTerms(cond.Condition)
}

// conjunctionTerms flattens a left-nested && chain into signed param
// names. Any operand that is not `var.<name>` or `!var.<name>` makes the
// whole condition unrecognized, rather than converting the recognizable
// half and silently dropping the rest.
func conjunctionTerms(expr hclsyntax.Expression) ([]string, bool) {
	if op, ok := expr.(*hclsyntax.BinaryOpExpr); ok && op.Op == hclsyntax.OpLogicalAnd {
		left, ok := conjunctionTerms(op.LHS)
		if !ok {
			return nil, false
		}
		right, ok := conjunctionTerms(op.RHS)
		if !ok {
			return nil, false
		}
		return append(left, right...), true
	}
	if unary, ok := expr.(*hclsyntax.UnaryOpExpr); ok && unary.Op == hclsyntax.OpLogicalNot {
		name, ok := varRefName(unary.Val)
		if !ok {
			return nil, false
		}
		return []string{"!" + name}, true
	}
	name, ok := varRefName(expr)
	if !ok {
		return nil, false
	}
	return []string{name}, true
}

// isIntLiteral reports whether expr is exactly the given integer literal.
func isIntLiteral(expr hclsyntax.Expression, want int64) bool {
	lit, ok := expr.(*hclsyntax.LiteralValueExpr)
	if !ok {
		return false
	}
	bf := lit.Val.AsBigFloat()
	if !bf.IsInt() {
		return false
	}
	got, _ := bf.Int64()
	return got == want
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

	// Attribute retention, recorded per resource so the caller can report
	// what a conversion actually preserved rather than only how many
	// resources it produced. A resource converting is not the same as a
	// resource surviving: terraform-aws-sqs converts six resources, of
	// which four retain exactly one attribute each (region) having lost
	// queue_url and their entire policy document. Counting resources alone
	// reported that as a success.
	source := 0
	for _, k := range keys {
		switch k {
		case "count", "for_each", "provider", "depends_on":
			continue
		}
		source++
	}
	c.retention = append(c.retention, Retention{
		Address:     addr,
		Slug:        rb.Labels[0] + "." + info.slug,
		SourceAttrs: source,
		KeptAttrs:   len(config),
		Kept:        sortedKeys(config),
	})

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
		CreateIf:  info.createIfTerms,
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

// sortedKeys returns a map's keys in a stable order, so a retention
// report never differs between two runs over the same module.
func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
