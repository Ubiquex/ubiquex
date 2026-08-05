package blueprint

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// GeneratePython compiles a resolved intent draft (the SAME already-
// drafted value GenerateGo/GenerateTS consume -- Slice 4's own "the AI
// draft happens exactly once, regardless of how many languages are
// requested" rule) into a real, self-contained Python module pair: one
// file with a dataclass Config + ResourceBinding pair per resource type,
// one file with the blueprint's own parameterized function.
// docs/blueprint.md's own "Multi-language codegen" section has the full
// design.
//
// Unlike TypeScript's runtime (sdk/ts/runtime), Python's own
// ubx_sdk.resource() requires its third argument to be a REAL dataclass
// instance (ubx_sdk's own _serialize_config hard-checks
// dataclasses.is_dataclass(value)) -- so GeneratePython needs a
// generated Config dataclass per resource, matching Go's own
// requirement (and reason) for a named Config struct, NOT TypeScript's
// simpler plain-object-literal shortcut (tsgen.go's own doc comment
// explains why TS alone can skip it).
//
// Returns a flat map of filename -> content, every key prefixed "py/"
// ("py/bindings.py", "py/<pkg>.py"), sibling to "go/"/"ts/"'s own
// output.
func GeneratePython(blueprintName string, ubxfile *Ubxfile, intent *resolver.IntentFile) (map[string]string, error) {
	pkgName, err := packageIdent(blueprintName)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}
	funcName, err := pythonIdentifier(blueprintName)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}

	b, err := decodeBlueprint(intent, ubxfile.Outputs)
	if err != nil {
		return nil, err
	}

	paramByName := map[string]Param{}
	for _, p := range ubxfile.Params {
		paramByName[p.Name] = p
	}

	g := &pyGenerator{blueprint: b, params: paramByName, byAddress: map[string]*pyResource{}}
	if err := g.wrap(); err != nil {
		return nil, err
	}
	if err := g.render(); err != nil {
		return nil, err
	}

	files := map[string]string{
		"py/bindings.py": renderPyBindings(g.ordered()),
	}
	fnSrc, err := renderPyFunction(funcName, ubxfile.Params, g)
	if err != nil {
		return nil, err
	}
	files["py/"+pkgName+".py"] = fnSrc

	return files, nil
}

// pyResource wraps one shared decodedResource with everything Python's
// own codegen needs that IS language-specific: its own PascalCase
// dataclass/binding identifier (the same casing convention Go/TS both
// use -- Python classes are conventionally PascalCase too, PEP 8) and
// each field's own already-rendered Python value expression, keyed by
// its snake_case idiomatic name.
type pyResource struct {
	dr         *decodedResource
	ident      string // PascalCase binding/dataclass name, e.g. "ContainerRepo"
	fields     []pyFieldEntry
	valueExprs map[string]string // by idiomatic (snake_case) name
}

type pyFieldEntry struct {
	wireKey   string
	idiomatic string
}

type pyGenerator struct {
	blueprint *decodedBlueprint
	params    map[string]Param
	byAddress map[string]*pyResource
}

func (g *pyGenerator) ordered() []*pyResource {
	out := make([]*pyResource, 0, len(g.blueprint.Resources))
	for _, dr := range g.blueprint.Resources {
		out = append(out, g.byAddress[dr.Address])
	}
	return out
}

func (g *pyGenerator) topoOrdered() []*pyResource {
	out := make([]*pyResource, 0, len(g.blueprint.Order))
	for _, dr := range g.blueprint.Order {
		out = append(out, g.byAddress[dr.Address])
	}
	return out
}

// wrap derives this blueprint's own PascalCase Python binding
// identifiers -- checked independently here rather than assumed to
// follow from GenerateGo/GenerateTS's own checks, since GeneratePython
// can be called alone (--lang py).
func (g *pyGenerator) wrap() error {
	seenIdent := map[string]string{}
	for _, dr := range g.blueprint.Resources {
		ident, err := pascalCase(dr.RI.Name)
		if err != nil {
			return fmt.Errorf("blueprint: resource %s.%s: %w", dr.RI.Type, dr.RI.Name, err)
		}
		if other, ok := seenIdent[ident]; ok {
			return fmt.Errorf("blueprint: resource names %q and %q both derive the Python identifier %q -- rename one in resources.md", other, dr.RI.Name, ident)
		}
		seenIdent[ident] = dr.RI.Name
		g.byAddress[dr.Address] = &pyResource{dr: dr, ident: ident, valueExprs: map[string]string{}}
	}
	return nil
}

func (g *pyGenerator) render() error {
	for _, dr := range g.blueprint.Resources {
		pr := g.byAddress[dr.Address]
		for _, f := range dr.Fields {
			idiomatic, err := pythonIdentifier(f.WireKey)
			if err != nil {
				return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.RI.Type, dr.RI.Name, f.WireKey, err)
			}
			pr.fields = append(pr.fields, pyFieldEntry{wireKey: f.WireKey, idiomatic: idiomatic})

			expr, err := g.renderAny(f.Value)
			if err != nil {
				return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.RI.Type, dr.RI.Name, f.WireKey, err)
			}
			pr.valueExprs[idiomatic] = expr
		}
	}
	return nil
}

// renderAny renders one already-JSON-decoded value into a Python source
// expression, recursively -- a $ref marker becomes a Computed attribute-
// access chain (Python's own Computed.__getattr__ lets
// `primary.settings.enabled`-style drilling happen via ordinary
// attribute access, the direct Python equivalent of Go's explicit
// .Field() method chain -- docs/blueprint.md's "Multi-language codegen"
// section), a string containing {param_name} tokens becomes a parameter
// reference or an f-string, anything else becomes a literal Python
// value.
func (g *pyGenerator) renderAny(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "None", nil
	case bool:
		if t {
			return "True", nil
		}
		return "False", nil
	case float64:
		return numberLiteral(t), nil
	case string:
		return g.renderString(t)
	case map[string]any:
		if to, ok := refTarget(t); ok {
			return g.renderRef(to)
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("{")
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
		b.WriteString("}")
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

func (g *pyGenerator) renderRef(to string) (string, error) {
	target, path, err := g.blueprint.resolveAddress(to)
	if err != nil {
		return "", fmt.Errorf("$ref %q: %w", to, err)
	}
	return g.propertyExpr(target, path)
}

// propertyExpr renders target's own local variable name plus a dotted
// attribute-access path suffix -- the SAME Computed attribute-drilling
// expression renderRef builds for an ordinary $ref, reused as-is for an
// outputs: entry's own return expression (renderPyFunction, below).
func (g *pyGenerator) propertyExpr(target *decodedResource, path []string) (string, error) {
	varName, err := pyLocalVarName(target)
	if err != nil {
		return "", err
	}
	expr := varName
	for _, seg := range path {
		expr += "." + seg
	}
	return expr, nil
}

// pyLocalVarName derives a referenced resource's own Python LOCAL
// VARIABLE name -- snake_case (pythonIdentifier applied to the
// resource's own wire Name), deliberately NOT lowerFirst(dr.ident): that
// shared helper only ever lowercases an already-PascalCase identifier's
// first byte (correct for Go/TS, whose local-variable convention IS
// camelCase-from-PascalCase), but Python's own local-variable convention
// is snake_case, a genuinely different derivation, not just a casing
// tweak -- caught by TestGeneratePython_CiPlatform's own live assertion
// on the exact generated variable name ("ci_runner", not "ciRunner")
// before this was fixed.
func pyLocalVarName(dr *decodedResource) (string, error) {
	return pythonIdentifier(dr.RI.Name)
}

// renderString handles a Config string value's own {param_name}
// placeholder substitution: a value that IS one bare token becomes a
// direct (unquoted) parameter reference; a value containing one or more
// tokens mixed with literal text becomes an f-string (Python's own
// native string-interpolation syntax -- the direct equivalent of Go's
// fmt.Sprintf); anything else is an ordinary quoted Python string
// literal -- UNLESS s is itself a JSON-embedded-ref string
// (renderEmbeddedRefString below), checked first.
func (g *pyGenerator) renderString(s string) (string, error) {
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
	b.WriteString(`f"`)
	cursor := 0
	locs := placeholderToken.FindAllStringSubmatchIndex(s, -1)
	for i, loc := range locs {
		start, end := loc[0], loc[1]
		b.WriteString(pyFStringLiteralSegment(s[cursor:start]))
		ref, err := g.paramExpr(matches[i][1], matches[i][2], matches[i][3])
		if err != nil {
			return "", err
		}
		b.WriteString("{")
		b.WriteString(ref)
		b.WriteString("}")
		cursor = end
	}
	b.WriteString(pyFStringLiteralSegment(s[cursor:]))
	b.WriteString(`"`)
	return b.String(), nil
}

// pyFStringLiteralSegment escapes one piece of an f-string's own LITERAL
// (non-{}) text: reuses jsonStringLiteral's own escaping for quotes/
// backslashes/control characters (valid Python double-quoted-string
// escaping too, same claim gogen.go's own jsonStringLiteral doc comment
// makes), stripped of its surrounding quotes, plus Python f-strings' own
// additional requirement that a literal "{" or "}" be doubled so it
// isn't mistaken for a real {expr} placeholder.
func pyFStringLiteralSegment(s string) string {
	raw := jsonStringLiteral(s)
	inner := raw[1 : len(raw)-1]
	inner = strings.ReplaceAll(inner, "{", "{{")
	inner = strings.ReplaceAll(inner, "}", "}}")
	return inner
}

// renderEmbeddedRefString detects and translates a JSON-embedded $ref
// into a Python expression that reconstructs the identical text at RUN
// time, splicing in the referenced resource's own real, RUNTIME-computed
// address (Computed's own .address property -- the direct Python
// equivalent of Go's Computed.Address() method and TS's addressOf()
// helper).
func (g *pyGenerator) renderEmbeddedRefString(s string) (expr string, handled bool, err error) {
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

		varName, verr := pyLocalVarName(target)
		if verr != nil {
			return "", false, verr
		}
		refExpr := varName
		for _, seg := range path {
			refExpr += "." + seg
		}
		refExpr += ".address"

		parts = append(parts,
			jsonStringLiteral(s[matchStart:addrStart]),
			refExpr,
			jsonStringLiteral(s[addrEnd:matchEnd]),
		)
		cursor = matchEnd
	}
	if cursor < len(s) {
		parts = append(parts, jsonStringLiteral(s[cursor:]))
	}

	return strings.Join(parts, " + "), true, nil
}

// paramRef returns the Python expression referencing param name's own
// value -- a bare identifier either way: Python has NATIVE default
// argument syntax (def f(a: str, b: int = 1)), so like TypeScript (and
// unlike Go) a params: default value needs no functional-options
// indirection at all.
func (g *pyGenerator) paramRef(name string) (string, error) {
	p, ok := g.params[name]
	if !ok {
		return "", fmt.Errorf("prose references {%s}, but no such param is declared in the Ubxfile's own params: block", name)
	}
	return pythonIdentifier(p.Name)
}

// paramExpr returns the Python expression for one {param_name} or
// {param_name <op> <literal>} token match (UBI-123: the same
// deliberately narrow "unit conversion or derived value" form
// GenerateGo/GenerateTS's own paramExpr implements).
func (g *pyGenerator) paramExpr(name, op, literal string) (string, error) {
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

// renderPyBindings renders bindings.py: one @dataclasses.dataclass
// Config + one sdk.ResourceBinding per resource, in the draft's own
// declaration order -- mirrors sdk_resolve_py's own established fixture
// convention (cli/testdata/sdk_resolve_py/bindings.py) exactly: every
// field typed Any, defaulted to None (an unset field is what lets
// ubx_sdk's own serializer omit it -- "a field left at its default
// (None) is omitted from the output entirely," ubx_sdk/__init__.py's
// own _serialize_config doc comment), never split into
// required-vs-defaulted the way a real schema-driven binding would --
// a blueprint's own dataclass has no such distinction, every one of its
// fields is fully populated by the generated function itself, never
// left for an external caller to set.
func renderPyBindings(resources []*pyResource) string {
	var b strings.Builder
	b.WriteString("# Code generated by `ubx blueprint build`. DO NOT EDIT.\n")
	b.WriteString("import dataclasses\n")
	b.WriteString("from typing import Any\n\n")
	b.WriteString("import ubx_sdk as sdk\n\n")

	for _, pr := range resources {
		fmt.Fprintf(&b, "@dataclasses.dataclass\nclass %sConfig:\n", pr.ident)
		if len(pr.fields) == 0 {
			b.WriteString("    pass\n\n")
		} else {
			for _, f := range pr.fields {
				fmt.Fprintf(&b, "    %s: Any = None\n", f.idiomatic)
			}
			b.WriteString("\n")
		}

		fmt.Fprintf(&b, "%s = sdk.ResourceBinding(\n", pr.ident)
		fmt.Fprintf(&b, "    wire_type=%s,\n", jsonStringLiteral(pr.dr.RI.Type))
		b.WriteString("    fields={\n")
		for _, f := range pr.fields {
			fmt.Fprintf(&b, "        %s: sdk.FieldSpec(wire_name=%s),\n", jsonStringLiteral(f.idiomatic), jsonStringLiteral(f.wireKey))
		}
		b.WriteString("    },\n")
		b.WriteString(")\n\n")
	}
	return b.String()
}

// renderPyFunction renders <pkg>.py: the blueprint's own exported,
// parameterized function -- every param (required AND defaulted alike)
// as an ordinary Python function parameter, defaulted ones carrying a
// native "= <default>" initializer -- then one sdk.resource() call per
// resource in TOPOLOGICAL order, assigning a local variable only for a
// resource a sibling actually references (matching GenerateGo/GenerateTS's
// own convention for consistency across languages).
func renderPyFunction(funcName string, params []Param, g *pyGenerator) (string, error) {
	if err := checkPyIdentCollisions(g, params); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Code generated by `ubx blueprint build`. DO NOT EDIT.\n")
	if len(g.blueprint.Outputs) > 0 {
		b.WriteString("from typing import Any\n")
	}
	b.WriteString("import ubx_sdk as sdk\n")

	names := make([]string, 0, len(g.byAddress)*2)
	for _, pr := range g.ordered() {
		names = append(names, pr.ident, pr.ident+"Config")
	}
	if len(names) > 0 {
		fmt.Fprintf(&b, "from bindings import %s\n", strings.Join(names, ", "))
	}
	b.WriteString("\n")

	// Required params first, defaulted params trailing -- Python itself
	// enforces "a non-default argument cannot follow a default
	// argument," so declared order alone can't be trusted to already
	// satisfy that; matches GenerateGo/GenerateTS's own required-then-
	// defaulted split for consistency.
	var required, defaulted []Param
	for _, p := range params {
		if p.Required {
			required = append(required, p)
		} else {
			defaulted = append(defaulted, p)
		}
	}
	ordered := append(append([]Param{}, required...), defaulted...)

	var sig strings.Builder
	sig.WriteString("def ")
	sig.WriteString(funcName)
	sig.WriteString("(")
	for i, p := range ordered {
		if i > 0 {
			sig.WriteString(", ")
		}
		cname, _ := pythonIdentifier(p.Name) // already validated during ParseUbxfile
		fmt.Fprintf(&sig, "%s: %s", cname, p.Type.PyType())
		if !p.Required {
			lit, err := pyDefaultLiteral(p)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&sig, " = %s", lit)
		}
	}
	// UBI-128: a declared output becomes part of the function's own
	// native return -- a bare value for exactly one output (unpacked with
	// "x = f(...)", mirroring Go's own single named return), or a plain
	// Python tuple for two or more (unpacked with "x, y = f(...)",
	// Python's own native multi-value-return construct -- no new
	// language feature, no wrapper class).
	switch len(g.blueprint.Outputs) {
	case 0:
		sig.WriteString(") -> None:")
	case 1:
		sig.WriteString(") -> Any:")
	default:
		types := make([]string, len(g.blueprint.Outputs))
		for i := range types {
			types[i] = "Any"
		}
		fmt.Fprintf(&sig, ") -> tuple[%s]:", strings.Join(types, ", "))
	}

	fmt.Fprintf(&b, "%s\n", sig.String())
	if len(g.blueprint.Resources) == 0 && len(g.blueprint.Outputs) == 0 {
		b.WriteString("    pass\n")
		return b.String(), nil
	}
	for _, pr := range g.topoOrdered() {
		varName, err := pyLocalVarName(pr.dr)
		if err != nil {
			return "", err
		}
		var call strings.Builder
		fmt.Fprintf(&call, "sdk.resource(%s, %s, %sConfig(\n", pr.ident, jsonStringLiteral(pr.dr.RI.Name), pr.ident)
		for _, f := range pr.fields {
			fmt.Fprintf(&call, "        %s=%s,\n", f.idiomatic, pr.valueExprs[f.idiomatic])
		}
		call.WriteString("    ))")
		if g.blueprint.Referenced[pr.dr.Address] {
			fmt.Fprintf(&b, "    %s = %s\n", varName, call.String())
		} else {
			fmt.Fprintf(&b, "    %s\n", call.String())
		}
	}
	if len(g.blueprint.Outputs) > 0 {
		exprs := make([]string, len(g.blueprint.Outputs))
		for i, o := range g.blueprint.Outputs {
			expr, err := g.propertyExpr(o.Target, o.Path)
			if err != nil {
				return "", fmt.Errorf("blueprint: output %q: %w", o.Name, err)
			}
			exprs[i] = expr
		}
		fmt.Fprintf(&b, "    return %s\n", strings.Join(exprs, ", "))
	}
	return b.String(), nil
}

// pyDefaultLiteral renders a params: default entry's own already-parsed
// Default value as a Python literal -- the native default-argument
// initializer GeneratePython uses in place of Go's own functional-
// options seeding.
func pyDefaultLiteral(p Param) (string, error) {
	switch v := p.Default.(type) {
	case string:
		return jsonStringLiteral(v), nil
	case int:
		return fmt.Sprintf("%d", v), nil
	case bool:
		if v {
			return "True", nil
		}
		return "False", nil
	default:
		return "", fmt.Errorf("param %q: unrecognized default value type %T", p.Name, p.Default)
	}
}

// checkPyIdentCollisions guards every top-level identifier this file
// derives (each resource's own PascalCase binding import, each param's
// own snake_case parameter name) against each other -- Python reserved
// words are already guarded per-identifier by pythonIdentifier's own
// trailing-underscore escape, so (unlike checkTSIdentCollisions) this
// only needs to check for a genuine COLLISION between two independently-
// derived identifiers, not reserved-word membership on its own.
func checkPyIdentCollisions(g *pyGenerator, params []Param) error {
	seen := map[string]string{}
	for _, pr := range g.ordered() {
		seen[pr.ident] = fmt.Sprintf("resource %s.%s", pr.dr.RI.Type, pr.dr.RI.Name)
	}
	for _, p := range params {
		cname, err := pythonIdentifier(p.Name)
		if err != nil {
			return err
		}
		if owner, ok := seen[cname]; ok {
			return fmt.Errorf("blueprint: param %q's own identifier %q collides with %s -- rename one", p.Name, cname, owner)
		}
		seen[cname] = fmt.Sprintf("param %q", p.Name)
	}
	return nil
}
