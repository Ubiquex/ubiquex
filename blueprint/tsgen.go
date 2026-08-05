package blueprint

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// GenerateTS compiles a resolved intent draft (the SAME already-drafted
// value GenerateGo consumes -- Slice 4's own "the AI draft happens
// exactly once, regardless of how many languages are requested" rule)
// into a real, self-contained TypeScript module pair: one file with a
// ResourceBinding literal per resource type, one file with the
// blueprint's own parameterized function. docs/blueprint.md's own
// "Multi-language codegen" section has the full design and the real
// adaptations this needed versus GenerateGo (native default parameters
// instead of functional options; ResourceBinding<any, any> instead of a
// generated per-resource Config/Attrs interface pair; property-access
// Computed<T> drilling instead of .Field()).
//
// Returns a flat map of filename -> content, every key prefixed "ts/"
// ("ts/bindings.ts", "ts/<pkg>.ts"), sibling to "go/"'s own output --
// see GenerateGo's own doc comment for why this output structure
// replaces Slice 1-3's flat, single-language layout.
func GenerateTS(blueprintName string, ubxfile *Ubxfile, intent *resolver.IntentFile) (map[string]string, error) {
	pkgName, err := packageIdent(blueprintName)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}
	funcName, err := camelCase(blueprintName)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}
	if tsReservedIdent(funcName) {
		return nil, fmt.Errorf("blueprint: blueprint name %q derives the TypeScript identifier %q, a reserved word -- rename the directory", blueprintName, funcName)
	}

	b, err := decodeBlueprint(intent, ubxfile.Params, ubxfile.Outputs)
	if err != nil {
		return nil, err
	}

	paramByName := map[string]Param{}
	for _, p := range ubxfile.Params {
		paramByName[p.Name] = p
	}

	g := &tsGenerator{blueprint: b, params: paramByName, byAddress: map[string]*tsResource{}}
	if err := g.wrap(); err != nil {
		return nil, err
	}
	if err := g.render(); err != nil {
		return nil, err
	}

	files := map[string]string{
		"ts/bindings.ts": renderTSBindings(g.ordered()),
	}
	fnSrc, err := renderTSFunction(funcName, ubxfile.Params, g)
	if err != nil {
		return nil, err
	}
	files["ts/"+pkgName+".ts"] = fnSrc

	return files, nil
}

// tsResource wraps one shared decodedResource with everything TS's own
// codegen needs that IS language-specific: its own PascalCase binding
// identifier (collision-checked the same way goResource's own ident is)
// and each field's own already-rendered TS value expression, keyed by
// its camelCase idiomatic name.
type tsResource struct {
	dr         *decodedResource
	ident      string // PascalCase binding const name, e.g. "ContainerRepo"
	fields     []tsFieldEntry
	valueExprs map[string]string // by idiomatic (camelCase) name

	// nameExpr (UBI-129) mirrors goResource's own field of the same
	// name -- a jsonStringLiteral-quoted literal for an ordinary
	// resource (byte-identical to every prior slice), or a real runtime
	// template-literal/bare-reference expression for the one resource
	// whose own dr.ForEach is set.
	nameExpr string
}

type tsFieldEntry struct {
	wireKey   string
	idiomatic string
}

type tsGenerator struct {
	blueprint *decodedBlueprint
	params    map[string]Param
	byAddress map[string]*tsResource

	// currentDR (UBI-129) mirrors goGenerator's own field of the same
	// name -- see its doc comment.
	currentDR *decodedResource
}

func (g *tsGenerator) ordered() []*tsResource {
	out := make([]*tsResource, 0, len(g.blueprint.Resources))
	for _, dr := range g.blueprint.Resources {
		out = append(out, g.byAddress[dr.Address])
	}
	return out
}

func (g *tsGenerator) topoOrdered() []*tsResource {
	out := make([]*tsResource, 0, len(g.blueprint.Order))
	for _, dr := range g.blueprint.Order {
		out = append(out, g.byAddress[dr.Address])
	}
	return out
}

// wrap derives this blueprint's own PascalCase TS binding identifiers --
// the SAME casing convention Go uses (JS/TS PascalCase types are
// byte-identical to Go's own), so a resource-name collision under
// pascalCase is exactly as likely (and exactly as real) in either
// language; checked independently here rather than assumed to follow
// from GenerateGo's own check, since GenerateTS can be called without
// GenerateGo ever running (--lang ts alone).
func (g *tsGenerator) wrap() error {
	seenIdent := map[string]string{}
	for _, dr := range g.blueprint.Resources {
		identSource := dr.RI.Name
		if dr.ForEach != "" {
			basis, err := forEachIdentifierBasis(dr.RI.Name)
			if err != nil {
				return fmt.Errorf("blueprint: resource %s.%s: %w", dr.RI.Type, dr.RI.Name, err)
			}
			identSource = basis
		}
		ident, err := pascalCase(identSource)
		if err != nil {
			return fmt.Errorf("blueprint: resource %s.%s: %w", dr.RI.Type, dr.RI.Name, err)
		}
		if other, ok := seenIdent[ident]; ok {
			return fmt.Errorf("blueprint: resource names %q and %q both derive the TypeScript identifier %q -- rename one in resources.md", other, dr.RI.Name, ident)
		}
		seenIdent[ident] = dr.RI.Name
		g.byAddress[dr.Address] = &tsResource{dr: dr, ident: ident, valueExprs: map[string]string{}}
	}
	return nil
}

func (g *tsGenerator) render() error {
	for _, dr := range g.blueprint.Resources {
		tr := g.byAddress[dr.Address]
		g.currentDR = dr

		nameExpr, err := g.renderResourceName(dr)
		if err != nil {
			return fmt.Errorf("blueprint: resource %s.%s: name: %w", dr.RI.Type, dr.RI.Name, err)
		}
		tr.nameExpr = nameExpr

		for _, f := range dr.Fields {
			idiomatic, err := camelCase(f.WireKey)
			if err != nil {
				return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.RI.Type, dr.RI.Name, f.WireKey, err)
			}
			tr.fields = append(tr.fields, tsFieldEntry{wireKey: f.WireKey, idiomatic: idiomatic})

			expr, err := g.renderAny(f.Value)
			if err != nil {
				return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.RI.Type, dr.RI.Name, f.WireKey, err)
			}
			tr.valueExprs[idiomatic] = expr
		}
	}
	g.currentDR = nil
	return nil
}

// renderResourceName mirrors goGenerator's own method of the same name
// (UBI-129) -- byte-identical (jsonStringLiteral-quoted) for an
// ordinary resource, run through renderString for the one resource
// whose own dr.ForEach is set.
func (g *tsGenerator) renderResourceName(dr *decodedResource) (string, error) {
	if dr.ForEach == "" {
		return jsonStringLiteral(dr.RI.Name), nil
	}
	return g.renderString(dr.RI.Name)
}

// renderAny renders one already-JSON-decoded value into a TypeScript
// source expression, recursively -- a $ref marker becomes a Computed<T>
// property-access chain (TS's own Proxy-based Computed<T> lets
// `primary.settings.enabled`-style drilling happen via ordinary property
// access, the direct TS equivalent of Go's explicit .Field() method
// chain -- docs/blueprint.md's "Multi-language codegen" section), a
// string containing {param_name} tokens becomes a parameter reference or
// a template literal, anything else becomes a literal TS value.
func (g *tsGenerator) renderAny(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "null", nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case float64:
		return numberLiteral(t), nil
	case string:
		return g.renderString(t)
	case map[string]any:
		if to, ok := refTarget(t); ok {
			return g.renderRef(to)
		}
		if name, _, ok := fnCallMarker(t); ok {
			// UBI-125: a ported built-in function (cidrsubnet(), currently
			// the only one) is Go-only so far (blueprint/cidrsubnet.go) --
			// a hard, named refusal here, never a silent (and WRONG)
			// literal-object rendering of the marker itself.
			return "", fmt.Errorf("blueprint: this draft uses the ported function %q, which is only supported in Go so far (UBI-125) -- build --lang go, or wait for a TypeScript port", name)
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{ ")
		for i, k := range keys {
			expr, err := g.renderAny(t[k])
			if err != nil {
				return "", err
			}
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s: %s", jsonStringLiteral(k), expr)
		}
		b.WriteString(" }")
		return b.String(), nil
	case []any:
		var b strings.Builder
		b.WriteString("[")
		for i, e := range t {
			expr, err := g.renderAny(e)
			if err != nil {
				return "", err
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(expr)
		}
		b.WriteString("]")
		return b.String(), nil
	default:
		return "", fmt.Errorf("unsupported JSON value type %T", v)
	}
}

func (g *tsGenerator) renderRef(to string) (string, error) {
	target, path, err := g.blueprint.resolveAddress(to)
	if err != nil {
		return "", fmt.Errorf("$ref %q: %w", to, err)
	}
	return g.propertyExpr(target, path), nil
}

// propertyExpr renders target's own local var name plus a dotted path
// suffix -- the SAME Computed<T> property-drilling expression renderRef
// builds for an ordinary $ref, reused as-is for an outputs: entry's own
// return expression (renderTSFunction, below): a declared output is just
// another address+path pointing at a resource this same function already
// declared a local const for, never a second expression-rendering path.
func (g *tsGenerator) propertyExpr(target *decodedResource, path []string) string {
	expr := lowerFirst(g.byAddress[target.Address].ident)
	for _, seg := range path {
		expr += "." + seg
	}
	return expr
}

// renderString handles a Config string value's own {param_name}
// placeholder substitution: a value that IS one bare token becomes a
// direct (unquoted) parameter reference; a value containing one or more
// tokens mixed with literal text becomes a template literal (TypeScript's
// own native string-interpolation syntax -- the direct equivalent of
// Go's fmt.Sprintf, needing no helper function at all); anything else is
// an ordinary quoted TS string literal -- UNLESS s is itself a
// JSON-embedded-ref string (renderEmbeddedRefString below), checked
// first.
func (g *tsGenerator) renderString(s string) (string, error) {
	if expr, handled, err := g.renderEmbeddedRefString(s); err != nil {
		return "", err
	} else if handled {
		return expr, nil
	}

	if m := placeholderWholeString.FindStringSubmatch(s); m != nil {
		return g.paramExpr(m[1], m[2], m[3])
	}

	matches := placeholderToken.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return jsonStringLiteral(s), nil
	}

	var b strings.Builder
	b.WriteString("`")
	cursor := 0
	locs := placeholderToken.FindAllStringSubmatchIndex(s, -1)
	for i, loc := range locs {
		start, end := loc[0], loc[1]
		b.WriteString(templateLiteralEscape(s[cursor:start]))
		ref, err := g.paramExpr(matches[i][1], matches[i][2], matches[i][3])
		if err != nil {
			return "", err
		}
		b.WriteString("${")
		b.WriteString(ref)
		b.WriteString("}")
		cursor = end
	}
	b.WriteString(templateLiteralEscape(s[cursor:]))
	b.WriteString("`")
	return b.String(), nil
}

// templateLiteralEscape escapes the three characters a TS template
// literal's own LITERAL (non-${}) text must never contain unescaped:
// backtick, backslash, and a bare "${" sequence.
func templateLiteralEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "`", "\\`", "${", "\\${")
	return r.Replace(s)
}

// renderEmbeddedRefString detects and translates a JSON-embedded $ref --
// a string whose own decoded content carries a real {"$ref":{"to":...}}
// marker one level down -- into a TS expression that reconstructs the
// identical text at RUN time, splicing in the referenced resource's own
// real, RUNTIME-computed address. TS's own Computed<T> is an opaque
// Proxy (never a real string), so getting its literal address string
// needs @ubx/sdk's own exported addressOf() helper -- the direct TS
// equivalent of Go's Computed.Address() method (docs/blueprint.md).
func (g *tsGenerator) renderEmbeddedRefString(s string) (expr string, handled bool, err error) {
	var decoded any
	if err := json.Unmarshal([]byte(s), &decoded); err != nil {
		return "", false, nil
	}
	if !containsRefMarker(decoded) {
		return "", false, nil
	}

	matches := embeddedRefPattern.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return "", false, fmt.Errorf("config field decodes as JSON containing a $ref marker, but its own literal text doesn't match the expected {\"$ref\":{\"to\":\"...\"}} shape -- can't safely translate it")
	}

	var parts []string
	cursor := 0
	for _, m := range matches {
		matchStart, matchEnd := m[0], m[1]
		addrStart, addrEnd := m[2], m[3]
		if matchStart > cursor {
			parts = append(parts, jsonStringLiteral(s[cursor:matchStart]))
		}

		addr := s[addrStart:addrEnd]
		target, path, rerr := g.blueprint.resolveAddress(addr)
		if rerr != nil {
			return "", false, fmt.Errorf("embedded $ref %q: %w", addr, rerr)
		}

		refExpr := lowerFirst(g.byAddress[target.Address].ident)
		for _, seg := range path {
			refExpr += "." + seg
		}

		parts = append(parts,
			jsonStringLiteral(s[matchStart:addrStart]),
			"addressOf("+refExpr+")",
			jsonStringLiteral(s[addrEnd:matchEnd]),
		)
		cursor = matchEnd
	}
	if cursor < len(s) {
		parts = append(parts, jsonStringLiteral(s[cursor:]))
	}

	return strings.Join(parts, " + "), true, nil
}

// forEachTokenIdent mirrors goGenerator's own method of the same name
// (UBI-129) -- see its doc comment for the full account.
func (g *tsGenerator) forEachTokenIdent(name string) (ident string, matched bool, err error) {
	fe := g.blueprint.ForEach
	if fe == nil {
		return "", false, nil
	}
	base := fe.RI.ForEach
	var suffix string
	switch name {
	case base:
		suffix = "Value"
	case base + "_index":
		suffix = "Index"
	default:
		return "", false, nil
	}
	if g.currentDR != fe {
		return "", true, fmt.Errorf("prose references {%s}, but that's only valid inside its own for_each resource (%s.%s)", name, fe.RI.Type, fe.RI.Name)
	}
	cname, err := camelCase(base)
	if err != nil {
		return "", true, err
	}
	return cname + suffix, true, nil
}

// paramRef returns the TS expression referencing param name's own value --
// a bare identifier either way: TypeScript has NATIVE default parameter
// syntax (function f(a: string, b: number = 1)), so unlike Go a
// params: default value needs NO functional-options indirection at all --
// it's just an ordinary parameter reference, identical for required and
// defaulted params alike (docs/blueprint.md's own "Multi-language
// codegen" section names this as a real, deliberate simplification, not
// an oversight).
//
// UBI-129: name is checked against the blueprint's own for_each
// synthetic tokens first -- see forEachTokenIdent and goGenerator.paramRef's
// own doc comment for the full account.
func (g *tsGenerator) paramRef(name string) (string, error) {
	if ident, matched, err := g.forEachTokenIdent(name); matched {
		return ident, err
	}
	p, ok := g.params[name]
	if !ok {
		return "", fmt.Errorf("prose references {%s}, but no such param is declared in the Ubxfile's own params: block", name)
	}
	if p.Type.IsList() {
		return "", fmt.Errorf("prose references {%s} directly, but %q is a list-typed param -- it can only be referenced inside its own for_each resource, as the per-element value (declare a for_each resource naming it first)", name, name)
	}
	return camelCase(p.Name)
}

// paramExpr returns the TS expression for one {param_name} or
// {param_name <op> <literal>} token match (UBI-123: the same
// deliberately narrow "unit conversion or derived value" form
// GenerateGo's own paramExpr implements -- exactly one operator, one
// already-declared param, one integer literal constant).
//
// UBI-129: a for_each synthetic token never supports the arithmetic
// form -- matching goGenerator.paramExpr's own deliberately narrow scope.
func (g *tsGenerator) paramExpr(name, op, literal string) (string, error) {
	if _, matched, _ := g.forEachTokenIdent(name); matched {
		ref, err := g.paramRef(name)
		if err != nil {
			return "", err
		}
		if op != "" {
			return "", fmt.Errorf("prose references {%s %s %s} -- arithmetic on a for_each element/index token isn't supported; use a plain {%s} reference instead", name, op, literal, name)
		}
		return ref, nil
	}

	ref, err := g.paramRef(name)
	if err != nil {
		return "", err
	}
	if op == "" {
		return ref, nil
	}
	p := g.params[name] // paramRef above already confirmed name is declared
	if p.Type != ParamNumber {
		return "", fmt.Errorf("prose references {%s %s %s}, but %q is declared %q, not %q -- arithmetic on a placeholder is only supported for number params", name, op, literal, name, p.Type, ParamNumber)
	}
	return fmt.Sprintf("(%s %s %s)", ref, op, literal), nil
}

// renderTSBindings renders bindings.ts: one ResourceBinding<any, any>
// const per resource, in the draft's own declaration order.
//
// ResourceBinding<any, any> (never a generated per-resource Config/Attrs
// interface pair, unlike sdk/codegen/templates/ts's own schema-driven
// ResourceFile) is a real, deliberate simplification specific to
// blueprint codegen: GenerateGo's own Config struct exists only because
// Go's runtime NEEDS a concrete named struct type to reflect over
// (sdk/go/runtime's own serializeConfig) -- TypeScript's runtime walks a
// plain object literal's own keys directly, no compile-time type
// reification required, so there is nothing here for an interface to
// buy beyond documentation value a real schema would be needed to make
// precise anyway (a blueprint has no schema, only the concrete fields
// its own resolved draft happened to observe). `any` also keeps
// Computed<T> property-drilling (`ciRunner.arn`) typechecking against
// an attribute name blueprint codegen has no schema to validate in the
// first place -- exactly as permissive as Go's own untyped
// Computed.Field(string) (docs/blueprint.md).
func renderTSBindings(resources []*tsResource) string {
	var b strings.Builder
	b.WriteString("// Code generated by `ubx blueprint build`. DO NOT EDIT.\n")
	b.WriteString("import type { ResourceBinding } from \"@ubx/sdk\";\n\n")

	for _, tr := range resources {
		fmt.Fprintf(&b, "export const %s: ResourceBinding<any, any> = {\n", tr.ident)
		fmt.Fprintf(&b, "  wireType: %s,\n", jsonStringLiteral(tr.dr.RI.Type))
		b.WriteString("  fields: {\n")
		for _, f := range tr.fields {
			fmt.Fprintf(&b, "    %s: %s,\n", f.idiomatic, jsonStringLiteral(f.wireKey))
		}
		b.WriteString("  },\n")
		b.WriteString("};\n\n")
	}
	return b.String()
}

// renderTSFunction renders <pkg>.ts: the blueprint's own exported,
// parameterized function -- every param (required AND defaulted alike)
// as an ordinary TypeScript function parameter, defaulted ones carrying
// a native "= <default>" initializer (no Option/functional-options
// machinery -- see paramRef's own doc comment) -- then one resource()
// call per resource in TOPOLOGICAL order, assigning a local const only
// for a resource a sibling actually references (matching GenerateGo's
// own convention for consistency across languages, even though
// TypeScript itself doesn't require it the way Go's compiler does).
func renderTSFunction(funcName string, params []Param, g *tsGenerator) (string, error) {
	if err := checkTSIdentCollisions(g, params); err != nil {
		return "", err
	}
	forEach, err := newTSForEach(g, params)
	if err != nil {
		return "", err
	}
	// UBI-128: unlike Go's named RETURN VALUES (real declared variables,
	// sharing one function-body scope with every resource/param
	// identifier -- checkGoOutputIdentCollisions' own reason to exist),
	// a TS output becomes an OBJECT LITERAL KEY (the return type's own
	// property name) -- never a new identifier declared into scope, so it
	// can't collide with a resource/param identifier at all, and a
	// reserved word is completely legal as an object key (`{ class: x }`
	// typechecks fine). The one real risk left is two DIFFERENT outputs
	// deriving the SAME camelCase key -- a silent last-write-wins object
	// literal, not a syntax error, so still checked here explicitly.
	outputIdents := make([]string, len(g.blueprint.Outputs))
	seenOutputIdent := map[string]string{}
	for i, o := range g.blueprint.Outputs {
		ident, err := camelCase(o.Name)
		if err != nil {
			return "", fmt.Errorf("blueprint: output %q: %w", o.Name, err)
		}
		if other, ok := seenOutputIdent[ident]; ok {
			return "", fmt.Errorf("blueprint: outputs %q and %q both derive the generated identifier %q -- rename one in the Ubxfile's own outputs: block", other, o.Name, ident)
		}
		seenOutputIdent[ident] = o.Name
		outputIdents[i] = ident
	}

	var b strings.Builder
	b.WriteString("// Code generated by `ubx blueprint build`. DO NOT EDIT.\n")
	b.WriteString("import { resource")
	if usesAddressOf(g) {
		b.WriteString(", addressOf")
	}
	b.WriteString(" } from \"@ubx/sdk\";\n")

	names := make([]string, 0, len(g.byAddress))
	for _, tr := range g.ordered() {
		names = append(names, tr.ident)
	}
	if len(names) > 0 {
		fmt.Fprintf(&b, "import { %s } from \"./bindings.ts\";\n", strings.Join(names, ", "))
	}
	b.WriteString("\n")

	// Required params first, defaulted params trailing -- TypeScript
	// itself enforces "a required parameter cannot follow an optional
	// one," so declared order alone (unlike Go, which never puts a
	// default param positionally at all) can't be trusted to already
	// satisfy that; reordering here guarantees valid syntax regardless
	// of the Ubxfile's own params: declaration order, matching
	// GenerateGo's own required-then-defaulted split for consistency.
	var required, defaulted []Param
	for _, p := range params {
		if p.Required {
			required = append(required, p)
		} else {
			defaulted = append(defaulted, p)
		}
	}

	var sig strings.Builder
	sig.WriteString(funcName)
	sig.WriteString("(")
	ordered := append(append([]Param{}, required...), defaulted...)
	for i, p := range ordered {
		if i > 0 {
			sig.WriteString(", ")
		}
		cname, _ := camelCase(p.Name) // already validated during ParseUbxfile
		fmt.Fprintf(&sig, "%s: %s", cname, p.Type.TSType())
		if !p.Required {
			lit, err := tsDefaultLiteral(p)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&sig, " = %s", lit)
		}
	}
	sig.WriteString("): ")
	switch {
	case len(outputIdents) > 0:
		sig.WriteString("{ ")
		for i, ident := range outputIdents {
			if i > 0 {
				sig.WriteString("; ")
			}
			fmt.Fprintf(&sig, "%s: any", ident)
		}
		sig.WriteString(" }")
	case forEach != nil:
		// UBI-129: mutually exclusive with outputIdents above --
		// decodeBlueprint already refuses a blueprint declaring both.
		sig.WriteString("any[]")
	default:
		sig.WriteString("void")
	}

	fmt.Fprintf(&b, "export function %s {\n", sig.String())
	for _, tr := range g.topoOrdered() {
		if tr.dr.ForEach != "" {
			// UBI-129: a real loop (Array.prototype.forEach, TS's own
			// idiomatic index+value iteration form), not a single
			// resource() call -- tr's own fields/nameExpr (render, above)
			// already reference the loop-variable identifiers below by
			// construction (paramRef resolves {list_param}/
			// {list_param_index} to these SAME names while rendering
			// THIS resource).
			var call strings.Builder
			fmt.Fprintf(&call, "resource(%s, %s, {\n", tr.ident, tr.nameExpr)
			for _, f := range tr.fields {
				fmt.Fprintf(&call, "      %s: %s,\n", f.idiomatic, tr.valueExprs[f.idiomatic])
			}
			call.WriteString("    })")
			fmt.Fprintf(&b, "  const %s: any[] = [];\n", forEach.accumIdent)
			fmt.Fprintf(&b, "  %s.forEach((%s, %s) => {\n", forEach.paramIdent, forEach.valueIdent, forEach.indexIdent)
			fmt.Fprintf(&b, "    const item = %s;\n", call.String())
			fmt.Fprintf(&b, "    %s.push(item);\n", forEach.accumIdent)
			b.WriteString("  });\n")
			continue
		}
		varName := lowerFirst(tr.ident)
		var call strings.Builder
		fmt.Fprintf(&call, "resource(%s, %s, {\n", tr.ident, tr.nameExpr)
		for _, f := range tr.fields {
			fmt.Fprintf(&call, "    %s: %s,\n", f.idiomatic, tr.valueExprs[f.idiomatic])
		}
		call.WriteString("  })")
		if g.blueprint.Referenced[tr.dr.Address] {
			// "as any": Computed<T>'s own conditional type only resolves to
			// an indexable shape when T is a CONCRETE object type -- TAttrs
			// here is deliberately `any` (bindings.ts has no schema to
			// derive a real one from, renderTSBindings' own doc comment),
			// and TypeScript's conditional-type distribution rule over a
			// naked `any` produces `ComputedMarker | {indexed...}`, a union
			// where the ComputedMarker branch has NO index signature -- so
			// arbitrary property access (`.arn`, `.id`) on a genuinely
			// untyped Computed<any> fails to typecheck even though it's
			// exactly as permissive at RUNTIME as Go's own untyped
			// Computed.Field(string). Casting the local const itself once,
			// here, is the direct TS equivalent of Go's own "no compile-time
			// attribute validation, ever" posture -- confirmed empirically
			// (a live `deno check` failure on the un-cast form, not assumed).
			fmt.Fprintf(&b, "  const %s = %s as any;\n", varName, call.String())
		} else {
			fmt.Fprintf(&b, "  %s;\n", call.String())
		}
	}
	switch {
	case len(outputIdents) > 0:
		exprs := make([]string, len(outputIdents))
		for i, o := range g.blueprint.Outputs {
			exprs[i] = fmt.Sprintf("%s: %s", outputIdents[i], g.propertyExpr(o.Target, o.Path))
		}
		fmt.Fprintf(&b, "  return { %s };\n", strings.Join(exprs, ", "))
	case forEach != nil:
		fmt.Fprintf(&b, "  return %s;\n", forEach.accumIdent)
	}
	b.WriteString("}\n")
	return b.String(), nil
}

func usesAddressOf(g *tsGenerator) bool {
	for _, tr := range g.byAddress {
		for _, expr := range tr.valueExprs {
			if strings.Contains(expr, "addressOf(") {
				return true
			}
		}
	}
	return false
}

// tsDefaultLiteral renders a params: default entry's own already-parsed
// Default value as a TS literal -- the native default-parameter
// initializer GenerateTS uses in place of Go's own functional-options
// seeding (renderGoOptions's own cfg := options{...}).
func tsDefaultLiteral(p Param) (string, error) {
	switch v := p.Default.(type) {
	case string:
		return jsonStringLiteral(v), nil
	case int:
		return fmt.Sprintf("%d", v), nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("param %q: unrecognized default value type %T", p.Name, p.Default)
	}
}

// checkTSIdentCollisions guards every top-level identifier this file
// derives (each resource's own PascalCase binding import, each param's
// own camelCase parameter name) against a real TypeScript reserved word,
// and against each other -- matching checkGoOptionIdentCollisions' own
// "hard build error, never silently renamed around" posture, scaled down
// to what TS's own simpler (no synthetic Option/With* identifiers)
// codegen actually introduces.
func checkTSIdentCollisions(g *tsGenerator, params []Param) error {
	seen := map[string]string{}
	for _, tr := range g.ordered() {
		if tsReservedIdent(tr.ident) {
			return fmt.Errorf("blueprint: resource %s.%s derives the TypeScript identifier %q, a reserved word -- rename it in resources.md", tr.dr.RI.Type, tr.dr.RI.Name, tr.ident)
		}
		seen[tr.ident] = fmt.Sprintf("resource %s.%s", tr.dr.RI.Type, tr.dr.RI.Name)
	}
	for _, p := range params {
		cname, err := camelCase(p.Name)
		if err != nil {
			return err
		}
		if tsReservedIdent(cname) {
			return fmt.Errorf("blueprint: param %q derives the TypeScript identifier %q, a reserved word -- rename it in the Ubxfile's own params: block", p.Name, cname)
		}
		if owner, ok := seen[cname]; ok {
			return fmt.Errorf("blueprint: param %q's own identifier %q collides with %s -- rename one", p.Name, cname, owner)
		}
		seen[cname] = fmt.Sprintf("param %q", p.Name)
	}
	return nil
}

// tsForEach mirrors goForEach (UBI-129) -- see its doc comment. TS has
// no functional-options plumbing to guard against (checkTSIdentCollisions'
// own "simpler than Go" precedent), so this check is correspondingly
// smaller: just the resources/params every generated file already
// derives, plus TS's own reserved-word list.
type tsForEach struct {
	paramIdent string
	valueIdent string
	indexIdent string
	accumIdent string
}

func newTSForEach(g *tsGenerator, allParams []Param) (*tsForEach, error) {
	fe := g.blueprint.ForEach
	if fe == nil {
		return nil, nil
	}

	paramIdent, err := camelCase(fe.RI.ForEach)
	if err != nil {
		return nil, err
	}
	valueIdent := paramIdent + "Value"
	indexIdent := paramIdent + "Index"
	accumIdent := lowerFirst(g.byAddress[fe.Address].ident) + "List"

	used := map[string]string{"item": "the generated for_each loop's own per-iteration temporary"}
	for _, tr := range g.ordered() {
		used[tr.ident] = fmt.Sprintf("resource %s.%s", tr.dr.RI.Type, tr.dr.RI.Name)
	}
	for _, p := range allParams {
		cname, err := camelCase(p.Name)
		if err != nil {
			return nil, err
		}
		used[cname] = fmt.Sprintf("param %q", p.Name)
	}

	for _, candidate := range []string{valueIdent, indexIdent, accumIdent} {
		if tsReservedIdent(candidate) {
			return nil, fmt.Errorf("blueprint: for_each resource %s.%s derives the TypeScript identifier %q, a reserved word -- rename the resource or its for_each param", fe.RI.Type, fe.RI.Name, candidate)
		}
		if owner, ok := used[candidate]; ok {
			return nil, fmt.Errorf("blueprint: for_each resource %s.%s derives the generated identifier %q, which collides with %s -- rename one", fe.RI.Type, fe.RI.Name, candidate, owner)
		}
	}
	if valueIdent == indexIdent || valueIdent == accumIdent || indexIdent == accumIdent {
		return nil, fmt.Errorf("blueprint: for_each resource %s.%s derives colliding generated identifiers (%q/%q/%q) -- rename the resource or its for_each param", fe.RI.Type, fe.RI.Name, valueIdent, indexIdent, accumIdent)
	}

	return &tsForEach{paramIdent: paramIdent, valueIdent: valueIdent, indexIdent: indexIdent, accumIdent: accumIdent}, nil
}
