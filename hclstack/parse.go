// Package hclstack implements UBI-226: `.ubx.hcl`, a thin HCL wrapper for
// calling blueprints in a stack. Not a fourth authoring medium (the
// package intentionally does not sit alongside diagram/md, both removed
// by UBI-224 for nondeterminism) -- a parser, deterministic by
// construction, the same bytes always compile to the same intent/v1
// document. Comparable to Terragrunt: the real complexity lives in a
// blueprint's own SDK-authored definition, where a real programming
// language earns its keep (functions, loops, conditionals, type
// checking); this package is composition only.
//
// The grammar is deliberately closed, not extensible: one block type,
// `blueprint "<type>" "<name>" { ... }`, holding literals (string,
// number, bool, list) and cross-stack references ("@<stack>.<type>.
// <name>.<attr-path>", the SAME four-part form blueprint/invoke.go's own
// parseCrossRefArg already requires for an SDK-authored ParamCrossRef
// param -- never a shorthand of its own). A top-level `stack = "<name>"`
// attribute is required -- the file declares its own stack, never the
// directory (see "Why the file declares its own stack" below). No
// locals, loops, conditionals, functions, or string interpolation exist
// in this grammar at all, and none of the usual escape hatches either --
// there is no way to write one.
//
// # Why the file declares its own stack
//
// Every other authored artifact already works this way: an SDK program
// declares its stack in source (sdk.Stack("name", func() {...}),
// sdk/go/runtime/runtime.go), and a hand-written intent/v1 JSON file
// carries its own top-level "stack" field -- ubx resolve/ship/accept all
// read intent.Stack straight off the document, never from .ubx/config.
// .ubx/config's own stack field is a narrower thing: a per-directory
// default ubx init seeds once from the directory name (cli/init.go's
// deriveStackFromDir), consulted only by a command that needs a stack
// name and has no document to read one from (ubx why, ubx status
// --stack) -- never the source of truth for what stack an authored
// artifact belongs to. Inheriting from the directory here would also cut
// against the reason HCL was chosen at all: a .ubx.hcl file that got its
// stack from ambient .ubx/config state would resolve differently
// depending on where it's checked out, the same shape of nondeterminism
// UBI-224 removed markdown and chat for, even if not the same mechanism.
// Self-declaring means the same bytes always mean the same stack.
//
// # Why a sibling blueprint call's output is parsed, then refused
//
// The ticket's own worked example passes one call's output straight into
// another call's param (database_url = blueprint.postgres.primary.
// connection_string). Traced all the way through blueprint.ExpandCalls
// (blueprint/invoke.go): every call is invoked by literally running the
// target blueprint's own compiled function, and every declared param is
// coerced to a real, concrete literal (renderArgLiteral) and baked into
// the synthesized calling program's source BEFORE that program runs.
// There is no marker type in that coercion for "a value that doesn't
// exist yet" -- the only param type built for a deferred reference is
// ParamCrossRef (sdk.CrossStack(...)), and it works specifically because
// it names an ALREADY-SHIPPED resource in a SEPARATELY SIGNED ledger, a
// real value already sitting somewhere to look up. A sibling call in the
// SAME document hasn't shipped anything, there is nothing to look up --
// this is UBI-225's own finding restated, confirmed live there as a real
// Go compiler error for the identical shape in the SDK.
//
// The existing $blueprint_output:<CallName>:<outputKey> marker
// (resolver.BlueprintOutputRefPrefix, built for exactly this shape of
// problem, UBI-128) is not an escape hatch here either: it only ever
// gets embedded in an ORDINARY HAND-WRITTEN RESOURCE's config
// (blueprint/outputs.go's own rewriteBlueprintOutputRefs walks
// intent.Resources, never BlueprintCalls[].Args), and this medium
// produces ONLY blueprint calls, never a hand-written resource -- the
// ticket's own scope note. There is no valid landing spot for that
// marker in a document this package can produce, even in the best case.
//
// So the grammar recognizes the traversal (evalAttr below) and refuses
// it with a named error pointing at the SDK, rather than silently
// treating it as a literal string (which would resolve to nonsense) or
// silently dropping it. This is real syntax the parser recognizes and
// rejects, not a feature quietly left out -- worth stating plainly since
// a reader who only sees the shipped grammar, without this account,
// could otherwise conclude it was simply never considered.
//
// # source/version, not the ticket's registry shorthand
//
// The ticket's own example (source = "acme/postgres", version = "1.2.0")
// doesn't match resolver.BlueprintCall's real fields (Blueprint/Ref/
// Path), and there is no default-registry shorthand anywhere in this
// codebase -- an oci:// reference is always a full, explicit
// "oci://registry/repo:tag" URI (blueprint/pull.go). source maps
// directly onto Blueprint (a local path, a git URL, or a full oci://
// URI), version onto Ref (a git ref, or an oci:// tag when the source
// doesn't already carry one), and an additional path attribute (not in
// the ticket, added here for parity with BlueprintCall's own real third
// field) onto Path -- a git-hosted blueprint nested in a subdirectory
// would otherwise be uncallable from this medium at all.
package hclstack

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// reservedBlueprintAttrs are the three attribute names inside a
// blueprint block that map onto resolver.BlueprintCall's own
// Blueprint/Ref/Path fields, rather than becoming a declared param's own
// Args entry -- everything else inside the block is a param.
const (
	attrSource  = "source"
	attrVersion = "version"
	attrPath    = "path"
	attrStack   = "stack"
	blockType   = "blueprint"
)

// Parse reads and compiles one .ubx.hcl file into an intent/v1 document
// naming this stack's own blueprint calls -- Resources is always empty
// (this medium is blueprint calls only, the ticket's own scope note: a
// stack needing a hand-written resource goes to the SDK, where blueprint
// calls and resources already mix, UBI-225). The returned document is
// handed to blueprint.ExpandCalls exactly like any other BlueprintCalls
// producer -- this package never expands a call itself.
func Parse(path string) (*resolver.IntentFile, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("hclstack: %w", err)
	}
	return ParseBytes(src, path)
}

// ParseBytes is Parse's own real logic, split out so a test never needs
// a real file on disk to exercise the grammar.
func ParseBytes(src []byte, filename string) (*resolver.IntentFile, error) {
	hf, diags := hclsyntax.ParseConfig(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("hclstack: %s: %w", filename, diags)
	}
	body, ok := hf.Body.(*hclsyntax.Body)
	if !ok {
		return nil, fmt.Errorf("hclstack: %s: unexpected body type %T", filename, hf.Body)
	}

	stackAttr, ok := body.Attributes[attrStack]
	if !ok {
		return nil, fmt.Errorf("hclstack: %s: top-level %q attribute is required -- the file declares its own stack", filename, attrStack)
	}
	for name := range body.Attributes {
		if name != attrStack {
			return nil, fmt.Errorf("hclstack: %s: unexpected top-level attribute %q -- only %q and %q blocks are allowed here", filename, name, attrStack, blockType)
		}
	}
	stackVal, stackType, err := evalAttr(stackAttr.Expr)
	if err != nil {
		return nil, fmt.Errorf("hclstack: %s: %q: %w", filename, attrStack, err)
	}
	if stackType != cty.String || stackVal == "" {
		return nil, fmt.Errorf("hclstack: %s: %q must be a non-empty string", filename, attrStack)
	}

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         stackVal,
		Resources:     []resolver.ResourceIntent{},
	}

	seen := map[string]bool{}
	for _, block := range body.Blocks {
		if block.Type != blockType {
			return nil, fmt.Errorf("hclstack: %s: unexpected top-level block %q -- only %q blocks are allowed here", filename, block.Type, blockType)
		}
		if len(block.Labels) != 2 {
			return nil, fmt.Errorf("hclstack: %s: %q block requires exactly two labels, \"<type>\" \"<name>\"", filename, blockType)
		}
		typeLabel, nameLabel := block.Labels[0], block.Labels[1]
		key := typeLabel + "." + nameLabel
		if seen[key] {
			return nil, fmt.Errorf("hclstack: %s: duplicate blueprint block %q %q -- rename one", filename, typeLabel, nameLabel)
		}
		seen[key] = true

		call, err := parseBlueprintBlock(filename, typeLabel, nameLabel, block.Body)
		if err != nil {
			return nil, err
		}
		intent.BlueprintCalls = append(intent.BlueprintCalls, *call)
	}

	intent.Intent.Summary = fmt.Sprintf("%s: %d blueprint call(s), via .ubx.hcl", stackVal, len(intent.BlueprintCalls))

	return intent, nil
}

// parseBlueprintBlock compiles one blueprint "<type>" "<name>" { ... }
// block into a resolver.BlueprintCall. CallName is always set to
// "<type>.<name>" -- harmless even though this medium never produces a
// value referencing it (a sibling blueprint output reference is refused
// at evalAttr below, never reaches Args), and it costs nothing to keep
// the block's own address available for whatever consumes CallName in
// the future.
func parseBlueprintBlock(filename, typeLabel, nameLabel string, body *hclsyntax.Body) (*resolver.BlueprintCall, error) {
	if len(body.Blocks) > 0 {
		return nil, fmt.Errorf("hclstack: %s: blueprint %q %q: nested %q blocks are not allowed -- a blueprint block holds only literal and reference attributes",
			filename, typeLabel, nameLabel, body.Blocks[0].Type)
	}

	call := &resolver.BlueprintCall{
		Name:     typeLabel + "." + nameLabel,
		CallName: typeLabel + "." + nameLabel,
		Args:     map[string]string{},
	}

	sourceAttr, hasSource := body.Attributes[attrSource]
	if !hasSource {
		return nil, fmt.Errorf("hclstack: %s: blueprint %q %q: %q is required", filename, typeLabel, nameLabel, attrSource)
	}
	source, err := requireString(filename, typeLabel, nameLabel, attrSource, sourceAttr.Expr)
	if err != nil {
		return nil, err
	}
	call.Blueprint = source

	if versionAttr, ok := body.Attributes[attrVersion]; ok {
		version, err := requireString(filename, typeLabel, nameLabel, attrVersion, versionAttr.Expr)
		if err != nil {
			return nil, err
		}
		call.Ref = version
	}
	if pathAttr, ok := body.Attributes[attrPath]; ok {
		p, err := requireString(filename, typeLabel, nameLabel, attrPath, pathAttr.Expr)
		if err != nil {
			return nil, err
		}
		call.Path = p
	}

	names := make([]string, 0, len(body.Attributes))
	for name := range body.Attributes {
		if name == attrSource || name == attrVersion || name == attrPath {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		val, _, err := evalAttr(body.Attributes[name].Expr)
		if err != nil {
			return nil, fmt.Errorf("hclstack: %s: blueprint %q %q: %q: %w", filename, typeLabel, nameLabel, name, err)
		}
		call.Args[name] = val
	}

	return call, nil
}

// requireString evaluates attr and hard-refuses anything that isn't
// exactly a plain string (a sibling blueprint output reference included
// -- source/version/path never accept one, the ticket never asked for
// that and BlueprintCall.Blueprint/Ref/Path have no way to carry a
// deferred reference at all).
func requireString(filename, typeLabel, nameLabel, attrName string, expr hclsyntax.Expression) (string, error) {
	val, typ, err := evalAttr(expr)
	if err != nil {
		return "", fmt.Errorf("hclstack: %s: blueprint %q %q: %q: %w", filename, typeLabel, nameLabel, attrName, err)
	}
	if typ != cty.String {
		return "", fmt.Errorf("hclstack: %s: blueprint %q %q: %q must be a string", filename, typeLabel, nameLabel, attrName)
	}
	return val, nil
}

// evalAttr renders expr's own value as the raw string BlueprintCall.Args
// already expects regardless of a param's real declared type (Args is
// always string-valued, resolver.BlueprintCall's own doc comment), or
// refuses it with a named error. Returns the value's own cty.Type too,
// so a caller needing a real string (requireString above) can check it
// wasn't a number/bool/list rendered down to a string.
//
// Deliberately type-switches on the concrete hclsyntax expression node,
// never just checks whether expr.Value(nil) succeeds -- that check alone
// is NOT sufficient: hclsyntax evaluates pure-literal arithmetic (14 *
// 86400) and a pure-literal list/object with a nil EvalContext too, since
// neither needs a variable looked up, and it was the ticket's own
// explicit target to exclude, not merely "anything needing a variable."
// Only two shapes are accepted:
//  1. A bare literal -- *hclsyntax.LiteralValueExpr (number, bool), or
//     *hclsyntax.TemplateExpr with IsStringLiteral() true (an ordinary
//     quoted string with no ${...} interpolation -- HCL's native syntax
//     always wraps even a plain "foo" in a TemplateExpr, confirmed
//     empirically against this exact hcl/v2 version, not assumed) -- or
//     a TupleConsExpr each of whose OWN elements is, recursively, one of
//     these same two (a list of literals, never a list of anything
//     else).
//  2. The one fixed four-segment traversal blueprint.<type>.<name>.
//     <output> (*hclsyntax.ScopeTraversalExpr) -- recognized, then
//     always refused (see the package doc comment's own "Why a sibling
//     blueprint call's output is parsed, then refused" section).
//
// Anything else -- a binary/unary operator, a function call, a
// multi-part template (real interpolation), a conditional, an object
// constructor, any other traversal -- is refused with the same named
// error.
func evalAttr(expr hclsyntax.Expression) (string, cty.Type, error) {
	switch e := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		return renderLiteral(e.Val)
	case *hclsyntax.TemplateExpr:
		if !e.IsStringLiteral() {
			return "", cty.NilType, fmt.Errorf("string interpolation (${...}) is not allowed here -- only a plain literal value or blueprint.<type>.<name>.<output> is")
		}
		v, diags := expr.Value(nil)
		if diags.HasErrors() {
			return "", cty.NilType, fmt.Errorf("%s", diags.Error())
		}
		return renderLiteral(v)
	case *hclsyntax.TupleConsExpr:
		items := make([]string, 0, len(e.Exprs))
		for _, elemExpr := range e.Exprs {
			s, _, err := evalAttr(elemExpr)
			if err != nil {
				return "", cty.NilType, fmt.Errorf("list element: %w", err)
			}
			items = append(items, s)
		}
		return strings.Join(items, ", "), cty.List(cty.String), nil
	case *hclsyntax.ObjectConsExpr:
		return "", cty.NilType, fmt.Errorf("object values are not supported for a blueprint call argument -- there is no object-typed blueprint param, declare separate scalar or list attributes instead")
	case *hclsyntax.ScopeTraversalExpr:
		if typeLabel, nameLabel, output, ok := blueprintOutputTraversal(e.Traversal); ok {
			return "", cty.NilType, fmt.Errorf(
				"blueprint.%s.%s.%s references another blueprint call's own output -- "+
					"a blueprint call cannot consume another call's output as a parameter "+
					"(a blueprint's own declared params need a real value when it runs, and "+
					"a sibling call's output isn't one yet) -- author this stack via the SDK "+
					"instead, where blueprint calls and hand-written resources already compose",
				typeLabel, nameLabel, output)
		}
		return "", cty.NilType, fmt.Errorf("%s is not a valid reference here -- only literal values or blueprint.<type>.<name>.<output> are allowed", traversalString(e.Traversal))
	default:
		return "", cty.NilType, fmt.Errorf("only literal values or blueprint.<type>.<name>.<output> are allowed here, no expressions, functions, or interpolation")
	}
}

// blueprintOutputTraversal reports whether trav is exactly the four-part
// shape blueprint.<type>.<name>.<output> -- the ONE traversal shape this
// grammar recognizes at all, everything else falls through to evalAttr's
// own generic refusal.
func blueprintOutputTraversal(trav hcl.Traversal) (typeLabel, nameLabel, output string, ok bool) {
	if len(trav) != 4 {
		return "", "", "", false
	}
	root, isRoot := trav[0].(hcl.TraverseRoot)
	if !isRoot || root.Name != blockType {
		return "", "", "", false
	}
	steps := make([]string, 3)
	for i, step := range trav[1:] {
		attr, isAttr := step.(hcl.TraverseAttr)
		if !isAttr {
			return "", "", "", false
		}
		steps[i] = attr.Name
	}
	return steps[0], steps[1], steps[2], true
}

func traversalString(trav hcl.Traversal) string {
	var b strings.Builder
	for i, step := range trav {
		switch t := step.(type) {
		case hcl.TraverseRoot:
			b.WriteString(t.Name)
		case hcl.TraverseAttr:
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(t.Name)
		default:
			b.WriteString(".<index>")
		}
	}
	return b.String()
}

// renderLiteral converts a pure literal cty.Value into the raw string
// form BlueprintCall.Args expects. A list renders as a comma-separated
// string, matching blueprint/invoke.go's own splitListArg exactly (comma
// split, each element trimmed) -- the SAME convention a hand-written
// intent/v1 file's own Args entry already uses for a list-typed param,
// so nothing downstream needs a third encoding. An object/map literal is
// refused -- there is no object-typed blueprint param anywhere in this
// system (blueprint/ubxfile.go's own ParamType enum: string, number,
// bool, list(string), list(number), cross_ref -- never object).
// renderLiteral converts a bare literal cty.Value (always String, Bool,
// or Number -- the only two callers are LiteralValueExpr's own raw Val
// and a single-part TemplateExpr's evaluated Value, and HCL's native
// syntax never represents a list/object as either of those, always as
// its own TupleConsExpr/ObjectConsExpr node -- evalAttr handles both
// directly, this function never sees one) into the raw string form
// BlueprintCall.Args expects.
func renderLiteral(v cty.Value) (string, cty.Type, error) {
	if v.IsNull() {
		return "", cty.NilType, fmt.Errorf("null is not a valid value")
	}
	if !v.IsWhollyKnown() {
		return "", cty.NilType, fmt.Errorf("value isn't statically known")
	}
	switch v.Type() {
	case cty.String:
		return v.AsString(), cty.String, nil
	case cty.Bool:
		return strconv.FormatBool(v.True()), cty.Bool, nil
	case cty.Number:
		f, _ := v.AsBigFloat().Float64()
		return numberText(f), cty.Number, nil
	default:
		return "", cty.NilType, fmt.Errorf("value of type %s isn't supported", v.Type().FriendlyName())
	}
}

func numberText(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
