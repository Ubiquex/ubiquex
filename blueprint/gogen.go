package blueprint

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// GenerateGo compiles a resolved intent (parsed from an Ubxfile's own
// resources: JSON, exactly once, by the caller -- this function makes
// no intent-provider call of its own) into a real, self-contained Go
// package: one file with a ResourceBinding+Config struct pair per
// resource type, one file with the blueprint's own parameterized
// function. docs/blueprint.md has the full design and the reasoning
// behind every decision named in this file's own comments.
//
// blueprintName is the caller's own chosen name for this blueprint
// (Slice 1: the Ubxfile's own directory basename) -- GenerateGo has no
// opinion on where that name comes from, only that it's a valid
// hyphen/underscore-separated identifier source.
//
// Returns a flat map of filename -> content, every key prefixed "go/"
// (Slice 4: sibling per-language output directories -- "go/go.mod",
// "go/bindings.go", "go/<pkg>.go" -- superseding Slice 1-3's own flat,
// single-language layout now that --lang can request TS/Python
// alongside Go from the same build; see docs/blueprint.md's own
// "Multi-language codegen" section). Deliberately NOT a per-service-
// directory tree the way sdk/codegen/templates/go's own GeneratedRepo
// produces: that structure exists to survive a real provider's hundreds
// of types in one repo, a single blueprint's own handful of resources
// never approaches that scale.
func GenerateGo(blueprintName string, ubxfile *Ubxfile, intent *resolver.IntentFile) (map[string]string, error) {
	pkgName, err := packageIdent(blueprintName)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}
	funcName, err := pascalCase(blueprintName)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}

	b, err := decodeBlueprint(intent, ubxfile.Params, ubxfile.Outputs)
	if err != nil {
		return nil, err
	}

	paramByName := map[string]Param{}
	for _, p := range ubxfile.Params {
		paramByName[p.Name] = p
	}

	g := &goGenerator{blueprint: b, params: paramByName, byAddress: map[string]*goResource{}}
	if err := g.wrap(); err != nil {
		return nil, err
	}
	if err := g.render(); err != nil {
		return nil, err
	}

	files := map[string]string{
		"go/go.mod":      fmt.Sprintf("module %s\n\ngo 1.23\n\nrequire github.com/ubiquex/ubx-sdk-go v0.0.0\n", pkgName),
		"go/bindings.go": renderGoBindings(pkgName, g.ordered()),
	}
	fnSrc, err := renderGoFunction(pkgName, funcName, blueprintName, ubxfile.Params, g)
	if err != nil {
		return nil, err
	}
	files["go/"+pkgName+".go"] = fnSrc

	if g.usesCidrsubnet {
		files["go/cidrsubnet.go"] = fmt.Sprintf(cidrsubnetGoSource, pkgName)
	}

	return files, nil
}

// goResource wraps one shared decodedResource with everything Go's own
// codegen needs that IS language-specific: its own PascalCase
// identifiers (collision-checked against every OTHER resource's own
// derived Go identifier -- two differently-hyphenated resource names can
// legally collide once PascalCased) and each field's own already-
// rendered Go value expression.
type goResource struct {
	dr         *decodedResource
	ident      string
	configName string
	fields     []fieldEntry
	valueExprs map[string]string

	// nameExpr (UBI-129) is this resource's own already-rendered Go
	// expression for its sdk.Resource() call's own name argument -- a
	// plain %q-quoted literal for an ordinary resource (byte-identical
	// to every prior slice's own behavior), or a real runtime expression
	// (an fmt.Sprintf call, or a bare loop-variable reference) for the
	// one resource whose own dr.ForEach is set, since that resource's
	// own name must vary per iteration (renderResourceName below).
	nameExpr string
}

type fieldEntry struct {
	wireKey string
	goName  string
}

// goGenerator holds the state one GenerateGo call threads through
// rendering every resource's own fields into Go source -- the shared,
// already-decoded/topo-sorted blueprint, this language's own per-
// resource wrapper (byAddress), and the declared params (for
// {param_name} substitution).
type goGenerator struct {
	blueprint *decodedBlueprint
	params    map[string]Param
	byAddress map[string]*goResource

	// currentDR (UBI-129) is the resource whose own name/config fields
	// are being rendered RIGHT NOW -- nil outside of render()'s own
	// per-resource loop below. paramRef/paramExpr consult this to decide
	// whether a bare {list_param}/{list_param_index} token is legal here
	// (only inside the blueprint's own ForEach resource, if any) --
	// exactly the same "which resource is this rendering for" context
	// decodeBlueprint's own Referenced/Deps bookkeeping already tracks
	// structurally, just needed here too, transiently, for THIS
	// language's own token resolution.
	currentDR *decodedResource

	// usesCidrsubnet (UBI-125) records whether this blueprint's own
	// draft used the cidrsubnet() ported helper at least once (via
	// tfconvert's own {"$fn":{"name":"cidrsubnet",...}} marker,
	// fnCallMarker/renderFnCall below) -- GenerateGo only emits
	// go/cidrsubnet.go when this is true, so an ordinary blueprint
	// (every one before this ticket, and every one hand-drafted rather
	// than converted) never carries dead, unused generated code.
	usesCidrsubnet bool

	// usedForEachValue/usedForEachIndex (UBI-129) record whether the
	// for_each resource's own fields/name actually referenced the
	// per-element value/index token at least once -- set by
	// forEachTokenIdent as rendering happens, consulted by
	// newGoForEach/renderGoFunction's own loop-header emission, which
	// substitutes Go's own blank identifier ("_") for whichever one
	// never got referenced (Go, unlike TS/Python, hard-refuses to
	// compile a declared-but-unused range variable).
	usedForEachValue bool
	usedForEachIndex bool
}

func (g *goGenerator) ordered() []*goResource {
	out := make([]*goResource, 0, len(g.blueprint.Resources))
	for _, dr := range g.blueprint.Resources {
		out = append(out, g.byAddress[dr.Address])
	}
	return out
}

func (g *goGenerator) topoOrdered() []*goResource {
	out := make([]*goResource, 0, len(g.blueprint.Order))
	for _, dr := range g.blueprint.Order {
		out = append(out, g.byAddress[dr.Address])
	}
	return out
}

// wrap derives this blueprint's own Go identifiers for every resource
// (declaration order, so a collision error always names the SECOND
// resource to claim an identifier, matching Slice 1-3's own established
// wording) -- rendering each field's own value expression happens
// separately, in render, once every resource's own ident is known (a
// $ref can point forward OR backward in the draft's own resource order).
func (g *goGenerator) wrap() error {
	seenIdent := map[string]string{} // ident -> the resource Name that first claimed it
	for _, dr := range g.blueprint.Resources {
		identSource := dr.RI.Name
		if dr.ForEach != "" {
			// UBI-129: a for_each resource's own Name is a TEMPLATE
			// ("subnet-{availability_zones}"), not plain text -- derive
			// the shared binding/config identifier from its own
			// placeholder-stripped basis instead (every instance shares
			// ONE binding regardless of its own per-iteration runtime
			// name).
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
			return fmt.Errorf("blueprint: resource names %q and %q both derive the Go identifier %q -- rename one in resources.md", other, dr.RI.Name, ident)
		}
		seenIdent[ident] = dr.RI.Name
		g.byAddress[dr.Address] = &goResource{dr: dr, ident: ident, configName: ident + "Config", valueExprs: map[string]string{}}
	}
	return nil
}

func (g *goGenerator) render() error {
	for _, dr := range g.blueprint.Resources {
		gr := g.byAddress[dr.Address]
		g.currentDR = dr

		nameExpr, err := g.renderResourceName(dr)
		if err != nil {
			return fmt.Errorf("blueprint: resource %s.%s: name: %w", dr.RI.Type, dr.RI.Name, err)
		}
		gr.nameExpr = nameExpr

		for _, f := range dr.Fields {
			goName, err := pascalCase(f.WireKey)
			if err != nil {
				return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.RI.Type, dr.RI.Name, f.WireKey, err)
			}
			gr.fields = append(gr.fields, fieldEntry{wireKey: f.WireKey, goName: goName})

			expr, err := g.renderAny(f.Value)
			if err != nil {
				return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.RI.Type, dr.RI.Name, f.WireKey, err)
			}
			gr.valueExprs[goName] = expr
		}
	}
	g.currentDR = nil
	return nil
}

// renderResourceName renders dr's own sdk.Resource() name argument
// (UBI-129). An ordinary resource (ForEach == "") renders byte-
// identically to every prior slice: a plain %q-quoted literal, never
// scanned for {param_name} tokens at all (a resource's own Name was
// never templated before this ticket). The one resource with ForEach
// set runs through the SAME renderString machinery Config field values
// already use -- its own Name must genuinely vary per iteration
// (docs/blueprint.md), using the bare {list_param}/{list_param_index}
// tokens paramRef resolves specially while currentDR is this resource.
func (g *goGenerator) renderResourceName(dr *decodedResource) (string, error) {
	if dr.ForEach == "" {
		return fmt.Sprintf("%q", dr.RI.Name), nil
	}
	return g.renderString(dr.RI.Name)
}

// renderAny renders one already-JSON-decoded value into a Go source
// expression, recursively -- a $ref marker becomes a Computed.Field()
// chain, a string containing {param_name} tokens becomes a parameter
// reference or an fmt.Sprintf call, anything else becomes a literal Go
// value (docs/blueprint.md's "Value translation" section has the full
// account of each case).
func (g *goGenerator) renderAny(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "nil", nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		return numberLiteral(t), nil
	case string:
		return g.renderString(t)
	case map[string]any:
		if to, ok := refTarget(t); ok {
			return g.renderRef(to)
		}
		if name, args, ok := fnCallMarker(t); ok {
			return g.renderFnCall(name, args)
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("map[string]any{")
		for i, k := range keys {
			expr, err := g.renderAny(t[k])
			if err != nil {
				return "", err
			}
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q: %s", k, expr)
		}
		b.WriteString("}")
		return b.String(), nil
	case []any:
		var b strings.Builder
		b.WriteString("[]any{")
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
		b.WriteString("}")
		return b.String(), nil
	default:
		return "", fmt.Errorf("unsupported JSON value type %T", v)
	}
}

// renderFnCall renders one {"$fn":{...}} marker (UBI-125, tfconvert) --
// currently the only recognized name is "cidrsubnet", matching
// blueprint/cidrsubnet.go's own Go-only porting scope (docs/blueprint.md
// names TS/Python as a real, separate, not-silently-done follow-up).
// Args are rendered exactly like any other Config value, recursively via
// renderAny -- a literal number, a {param} token, or another for_each
// synthetic token all already produce valid Go expressions for
// cidrsubnet's own (string, int, int) parameter types.
func (g *goGenerator) renderFnCall(name string, args []any) (string, error) {
	switch name {
	case "cidrsubnet":
		if len(args) != 3 {
			return "", fmt.Errorf("cidrsubnet(): expected 3 arguments, got %d", len(args))
		}
		g.usesCidrsubnet = true
		parts := make([]string, len(args))
		for i, a := range args {
			expr, err := g.renderAny(a)
			if err != nil {
				return "", err
			}
			parts[i] = expr
		}
		return fmt.Sprintf("cidrsubnet(%s)", strings.Join(parts, ", ")), nil
	default:
		return "", fmt.Errorf("unsupported ported function %q", name)
	}
}

func (g *goGenerator) renderRef(to string) (string, error) {
	target, path, err := g.blueprint.resolveAddress(to)
	if err != nil {
		return "", fmt.Errorf("$ref %q: %w", to, err)
	}

	expr := lowerFirst(g.byAddress[target.Address].ident)
	for _, seg := range path {
		expr += fmt.Sprintf(".Field(%q)", seg)
	}
	return expr, nil
}

// renderString handles a Config string value's own {param_name}
// placeholder substitution (docs/blueprint.md's "Value translation"):
// a value that IS one bare token becomes a direct (unquoted) parameter
// reference; a value containing one or more tokens mixed with literal
// text becomes an fmt.Sprintf call; anything else is an ordinary quoted
// Go string literal -- UNLESS s is itself a JSON-embedded-ref string
// (renderEmbeddedRefString below), checked first.
func (g *goGenerator) renderString(s string) (string, error) {
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
		return fmt.Sprintf("%q", s), nil
	}

	var args []string
	for _, m := range matches {
		ref, err := g.paramExpr(m[1], m[2], m[3])
		if err != nil {
			return "", err
		}
		args = append(args, ref)
	}
	literal := placeholderToken.ReplaceAllString(s, "%v")
	return fmt.Sprintf("fmt.Sprintf(%q, %s)", literal, strings.Join(args, ", ")), nil
}

// renderEmbeddedRefString detects and translates a JSON-embedded $ref --
// a string whose own decoded content carries a real {"$ref":{"to":...}}
// marker one level down (e.g. an IAM policy document's "Resource" field
// naming a sibling resource's ARN) -- into a Go expression that
// reconstructs the identical text at RUN time, splicing in the
// referenced resource's own real, RUNTIME-computed address (Computed.
// Address(), which threads the CALLING stack's own actual name --
// sdk.Stack's own argument, never known at blueprint build time) in
// place of each match's own literal "to" text (UBI-74 Slice 2's own
// real-AWS-verified fix, unchanged this slice).
func (g *goGenerator) renderEmbeddedRefString(s string) (expr string, handled bool, err error) {
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
			parts = append(parts, fmt.Sprintf("%q", s[cursor:matchStart]))
		}

		addr := s[addrStart:addrEnd]
		target, path, rerr := g.blueprint.resolveAddress(addr)
		if rerr != nil {
			return "", false, fmt.Errorf("embedded $ref %q: %w", addr, rerr)
		}

		refExpr := lowerFirst(g.byAddress[target.Address].ident)
		for _, seg := range path {
			refExpr += fmt.Sprintf(".Field(%q)", seg)
		}
		refExpr += ".Address()"

		parts = append(parts,
			fmt.Sprintf("%q", s[matchStart:addrStart]),
			refExpr,
			fmt.Sprintf("%q", s[addrEnd:matchEnd]),
		)
		cursor = matchEnd
	}
	if cursor < len(s) {
		parts = append(parts, fmt.Sprintf("%q", s[cursor:]))
	}

	return strings.Join(parts, " + "), true, nil
}

// forEachTokenIdent reports whether name is one of the two synthetic
// per-iteration tokens a for_each resource's own name/config may
// reference (UBI-129) -- the bare list param name itself (the current
// loop element's own value) or that same name with "_index" appended
// (its own zero-based position) -- and, if so, returns the Go loop-
// variable identifier it resolves to. Only legal while g.currentDR IS
// the blueprint's own ForEach resource; referencing either token from
// any OTHER resource is refused with a clear error, never silently
// treated as an ordinary (and undeclared) param name.
func (g *goGenerator) forEachTokenIdent(name string) (ident string, matched bool, err error) {
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
	// A real, live-found subtlety (caught by a real `ubx resolve
	// --from-code` run against a for_each blueprint that only ever
	// references the per-element VALUE, never the index -- e.g. one
	// resource attribute per name, no index-derived value at all):
	// unlike Python/TS, Go hard-refuses to compile a declared-but-unused
	// range variable ("declared and not used"). Tracking which of the
	// two loop variables a real reference actually used lets the loop
	// header itself (newGoForEach/the emission in renderGoFunction)
	// substitute Go's own blank identifier for whichever one never got
	// referenced, instead of assuming both are always used just because
	// a for_each resource exists.
	if suffix == "Value" {
		g.usedForEachValue = true
	} else {
		g.usedForEachIndex = true
	}
	return cname + suffix, true, nil
}

// paramRef returns the Go expression referencing param name's own value: a
// bare identifier for a required param (a direct function argument), or
// a cfg.<field> selector for a params: default (docs/blueprint.md's
// "Resolved defaults" section -- a default param is no longer a bare
// function argument, its value lives on the options struct instead).
//
// UBI-129: name is checked against the blueprint's own for_each
// synthetic tokens FIRST -- see forEachTokenIdent above -- since neither
// token is a real params: entry at all (g.params has no such key), and
// the ordinary "no such param declared" error below would otherwise be
// misleading for what's actually a scoping mistake (referencing an
// iteration token outside its own for_each resource).
func (g *goGenerator) paramRef(name string) (string, error) {
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
	cname, err := camelCase(p.Name)
	if err != nil {
		return "", err
	}
	if p.Required {
		return cname, nil
	}
	return "cfg." + cname, nil
}

// paramExpr returns the Go expression for one {param_name} or {param_name
// <op> <literal>} token match (UBI-123: a simple, deliberately narrow
// "unit conversion or derived value" form -- exactly one operator, one
// already-declared param, one integer literal constant, never a general
// expression language). op/literal empty means an ordinary bare
// reference, identical to paramRef's own existing behavior.
//
// UBI-129: a for_each synthetic token (either one) never supports the
// arithmetic form -- a real, deliberately narrow scope boundary (this
// ticket's own worked examples need only plain substitution; adding
// arithmetic here would also need to know the list's own element type,
// genuinely more design than a real worked example has asked for yet).
func (g *goGenerator) paramExpr(name, op, literal string) (string, error) {
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
	return fmt.Sprintf("%s %s %s", ref, op, literal), nil
}

// renderGoBindings renders bindings.go: one Config struct + one
// ResourceBinding var per resource, in the draft's own declaration
// order (not topo order -- this file's own ordering has no compile-time
// constraint, declaration order reads most naturally).
func renderGoBindings(pkgName string, resources []*goResource) string {
	var b strings.Builder
	b.WriteString("// Code generated by `ubx blueprint build`. DO NOT EDIT.\n")
	fmt.Fprintf(&b, "package %s\n\n", pkgName)
	b.WriteString("import sdk \"github.com/ubiquex/ubx-sdk-go/runtime\"\n\n")

	for _, gr := range resources {
		fmt.Fprintf(&b, "type %s struct {\n", gr.configName)
		for _, f := range gr.fields {
			fmt.Fprintf(&b, "\t%s any\n", f.goName)
		}
		b.WriteString("}\n\n")

		fmt.Fprintf(&b, "var %s = sdk.ResourceBinding{\n", gr.ident)
		fmt.Fprintf(&b, "\tWireType: %q,\n", gr.dr.RI.Type)
		b.WriteString("\tFields: sdk.FieldMap{\n")
		for _, f := range gr.fields {
			fmt.Fprintf(&b, "\t\t%q: sdk.FieldSpec{WireName: %q},\n", f.goName, f.wireKey)
		}
		b.WriteString("\t},\n")
		b.WriteString("}\n\n")
	}
	return b.String()
}

// renderGoFunction renders <pkg>.go: the blueprint's own exported,
// parameterized function -- required params directly, in the Ubxfile's
// own declared order, as positional Go arguments; any params: default
// entries via a trailing "opts ...Option" (docs/blueprint.md's "Resolved
// defaults" design, renderGoOptions below) -- then one sdk.Resource() call
// per resource in TOPOLOGICAL order (so every $ref-derived .Field()
// reference already has a variable to read), assigning a local variable
// only for a resource a sibling actually references, avoiding an
// unused-local Go compile error.
//
// blueprintName (UBI-126) wraps the function body in
// sdk.PushBlueprintSource(blueprintName)/defer sdk.PopBlueprintSource()
// -- the SAME raw, unsanitized name (never pkgName, which is a
// Go-identifier-sanitized derivative) buildManifest/ubx why/ubx render
// already use, so every sdk.Resource() call inside this function --
// direct SDK import, the ONLY calling convention that previously had no
// provenance mechanism at all (diagram/md get theirs externally, from
// blueprint.ExpandCalls) -- self-tags without the calling stack's own
// code needing to change in any way.
func renderGoFunction(pkgName, funcName, blueprintName string, params []Param, g *goGenerator) (string, error) {
	var required, defaulted []Param
	for _, p := range params {
		if p.Required {
			required = append(required, p)
		} else {
			defaulted = append(defaulted, p)
		}
	}
	hasOptions := len(defaulted) > 0

	if err := checkGoOptionIdentCollisions(g, params, defaulted, hasOptions); err != nil {
		return "", err
	}
	if err := checkGoOutputIdentCollisions(g, params, hasOptions); err != nil {
		return "", err
	}
	forEach, err := newGoForEach(g, params, hasOptions)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("// Code generated by `ubx blueprint build`. DO NOT EDIT.\n")
	fmt.Fprintf(&b, "package %s\n\n", pkgName)

	usesSprintf := false
	for _, gr := range g.byAddress {
		if strings.HasPrefix(gr.nameExpr, "fmt.Sprintf(") {
			usesSprintf = true
		}
		for _, expr := range gr.valueExprs {
			if strings.HasPrefix(expr, "fmt.Sprintf(") {
				usesSprintf = true
			}
		}
	}
	b.WriteString("import (\n")
	if usesSprintf {
		b.WriteString("\t\"fmt\"\n\n")
	}
	b.WriteString("\tsdk \"github.com/ubiquex/ubx-sdk-go/runtime\"\n")
	b.WriteString(")\n\n")

	if hasOptions {
		if err := renderGoOptions(&b, defaulted); err != nil {
			return "", err
		}
	}

	// UBI-128: each declared output becomes a named return value, ALL
	// typed *sdk.Computed (an output is always a reference into a
	// resource's own attribute -- never a resolved literal, matching
	// Resource()'s own return type exactly) -- Go's own idiomatic
	// collapsed syntax for consecutive same-typed params/returns
	// ("repoArn, queueUrl *sdk.Computed", not "repoArn *sdk.Computed,
	// queueUrl *sdk.Computed"). Named purely for the generated source's
	// own readability (a caller reading it sees which value is which);
	// the function body still returns an explicit "return expr, expr"
	// statement below, never relying on Go's own naked-return convention.
	outputIdents := make([]string, len(g.blueprint.Outputs))
	for i, o := range g.blueprint.Outputs {
		ident, err := camelCase(o.Name)
		if err != nil {
			return "", fmt.Errorf("blueprint: output %q: %w", o.Name, err)
		}
		outputIdents[i] = ident
	}

	var sig strings.Builder
	sig.WriteString(funcName)
	sig.WriteString("(")
	for i, p := range required {
		if i > 0 {
			sig.WriteString(", ")
		}
		cname, _ := camelCase(p.Name) // already validated during ParseUbxfile
		fmt.Fprintf(&sig, "%s %s", cname, p.Type.GoType())
	}
	if hasOptions {
		if len(required) > 0 {
			sig.WriteString(", ")
		}
		sig.WriteString("opts ...Option")
	}
	sig.WriteString(")")
	switch {
	case len(outputIdents) > 0:
		fmt.Fprintf(&sig, " (%s *sdk.Computed)", strings.Join(outputIdents, ", "))
	case forEach != nil:
		// UBI-129: mutually exclusive with outputIdents above --
		// decodeBlueprint already refuses a blueprint that declares both
		// outputs: and a for_each resource, so at most one of these two
		// branches ever fires.
		sig.WriteString(" []*sdk.Computed")
	}

	fmt.Fprintf(&b, "func %s {\n", sig.String())
	fmt.Fprintf(&b, "\tsdk.PushBlueprintSource(%q)\n\tdefer sdk.PopBlueprintSource()\n\n", blueprintName)
	if hasOptions {
		b.WriteString("\tcfg := options{\n")
		for _, p := range defaulted {
			cname, _ := camelCase(p.Name) // already validated during ParseUbxfile
			lit, err := defaultLiteral(p)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "\t\t%s: %s,\n", cname, lit)
		}
		b.WriteString("\t}\n")
		b.WriteString("\tfor _, opt := range opts {\n\t\topt(&cfg)\n\t}\n\n")
	}
	for _, gr := range g.topoOrdered() {
		if gr.dr.ForEach != "" {
			// UBI-129: a real for loop, not a single sdk.Resource() call --
			// forEach's own fields (below) are exactly the loop-variable
			// identifiers paramRef already resolves {list_param}/
			// {list_param_index} to while rendering THIS resource's own
			// name/fields (render, above), so the loop header below and
			// every reference inside the call agree by construction.
			call := fmt.Sprintf("sdk.Resource(%s, %s, %s{\n", gr.ident, gr.nameExpr, gr.configName)
			for _, f := range gr.fields {
				call += fmt.Sprintf("\t\t\t%s: %s,\n", f.goName, gr.valueExprs[f.goName])
			}
			call += "\t\t})"
			// UBI-129: Go hard-refuses to compile a declared-but-unused
			// range variable -- substitute "_" for whichever of
			// index/value this resource's own fields/name never actually
			// referenced (g.usedForEachIndex/usedForEachValue, set live
			// while render() ran, above).
			headerIndex, headerValue := forEach.indexIdent, forEach.valueIdent
			if !g.usedForEachIndex {
				headerIndex = "_"
			}
			if !g.usedForEachValue {
				headerValue = "_"
			}
			fmt.Fprintf(&b, "\tvar %s []*sdk.Computed\n", forEach.accumIdent)
			fmt.Fprintf(&b, "\tfor %s, %s := range %s {\n", headerIndex, headerValue, forEach.paramIdent)
			fmt.Fprintf(&b, "\t\titem := %s\n", call)
			fmt.Fprintf(&b, "\t\t%s = append(%s, item)\n", forEach.accumIdent, forEach.accumIdent)
			b.WriteString("\t}\n")
			continue
		}
		varName := lowerFirst(gr.ident)
		call := fmt.Sprintf("sdk.Resource(%s, %s, %s{\n", gr.ident, gr.nameExpr, gr.configName)
		for _, f := range gr.fields {
			call += fmt.Sprintf("\t\t%s: %s,\n", f.goName, gr.valueExprs[f.goName])
		}
		call += "\t})"
		if g.blueprint.Referenced[gr.dr.Address] {
			fmt.Fprintf(&b, "\t%s := %s\n", varName, call)
		} else {
			fmt.Fprintf(&b, "\t%s\n", call)
		}
	}
	switch {
	case len(g.blueprint.Outputs) > 0:
		exprs := make([]string, len(g.blueprint.Outputs))
		for i, o := range g.blueprint.Outputs {
			exprs[i] = outputReturnExpr(g, o)
		}
		fmt.Fprintf(&b, "\treturn %s\n", strings.Join(exprs, ", "))
	case forEach != nil:
		fmt.Fprintf(&b, "\treturn %s\n", forEach.accumIdent)
	}
	b.WriteString("}\n")
	return b.String(), nil
}

// goForEach holds the four Go identifiers a for_each resource's own
// loop needs (UBI-129) -- computed once by newGoForEach and threaded
// into both the loop-emission code above and its own collision check,
// rather than re-derived at each use site independently.
type goForEach struct {
	paramIdent string // the source list param's own function-argument identifier, e.g. "availabilityZones"
	valueIdent string // the loop's own per-element variable, e.g. "availabilityZonesValue"
	indexIdent string // the loop's own index variable, e.g. "availabilityZonesIndex"
	accumIdent string // the accumulator slice variable, e.g. "subnetList"
}

// newGoForEach returns nil, nil when this blueprint declares no
// for_each resource at all (the overwhelming common case) -- otherwise
// it derives every identifier the loop-emission code in
// renderGoFunction needs and guards all four against collision with
// every OTHER identifier this same generated file derives (resources,
// params, outputs, and -- when present -- the functional-options
// plumbing), matching checkGoOptionIdentCollisions/
// checkGoOutputIdentCollisions' own established "hard build error,
// never silently renamed" posture.
func newGoForEach(g *goGenerator, allParams []Param, hasOptions bool) (*goForEach, error) {
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

	used := map[string]string{}
	for _, dr := range g.blueprint.Resources {
		gr := g.byAddress[dr.Address]
		used[gr.ident] = fmt.Sprintf("resource %s.%s", gr.dr.RI.Type, gr.dr.RI.Name)
		used[gr.configName] = fmt.Sprintf("resource %s.%s's own Config struct", gr.dr.RI.Type, gr.dr.RI.Name)
		if g.blueprint.Referenced[dr.Address] {
			used[lowerFirst(gr.ident)] = fmt.Sprintf("resource %s.%s's own local variable", gr.dr.RI.Type, gr.dr.RI.Name)
		}
	}
	for _, p := range allParams {
		cname, err := camelCase(p.Name)
		if err != nil {
			return nil, err
		}
		used[cname] = fmt.Sprintf("param %q", p.Name)
	}
	if hasOptions {
		used["cfg"] = "the generated functional-options config local"
		used["opts"] = "the generated functional-options parameter"
	}

	used["item"] = "the generated for_each loop's own per-iteration temporary"
	for _, candidate := range []string{valueIdent, indexIdent, accumIdent} {
		if owner, ok := used[candidate]; ok {
			return nil, fmt.Errorf("blueprint: for_each resource %s.%s derives the generated identifier %q, which collides with %s -- rename one", fe.RI.Type, fe.RI.Name, candidate, owner)
		}
	}
	if valueIdent == indexIdent || valueIdent == accumIdent || indexIdent == accumIdent {
		return nil, fmt.Errorf("blueprint: for_each resource %s.%s derives colliding generated identifiers (%q/%q/%q) -- rename the resource or its for_each param", fe.RI.Type, fe.RI.Name, valueIdent, indexIdent, accumIdent)
	}

	return &goForEach{paramIdent: paramIdent, valueIdent: valueIdent, indexIdent: indexIdent, accumIdent: accumIdent}, nil
}

// outputReturnExpr renders one decodedOutput's own return expression --
// the SAME resource-variable + .Field(path) chain renderRef already
// builds for an ordinary $ref (both are, structurally, "a Computed
// reference into a sibling resource's own attribute" -- an output is
// simply one the blueprint's own AUTHOR chose to expose, not the
// resolver's own $ref translation, so this is a small, deliberately
// separate mirror of that logic rather than a shared helper, matching
// this file's own established precedent of small, local, single-purpose
// renderers over premature sharing).
func outputReturnExpr(g *goGenerator, o decodedOutput) string {
	expr := lowerFirst(g.byAddress[o.Target.Address].ident)
	for _, seg := range o.Path {
		expr += fmt.Sprintf(".Field(%q)", seg)
	}
	return expr
}

// checkGoOutputIdentCollisions guards each output's own derived camelCase
// identifier against every OTHER identifier this same generated file
// already derives (resource variables/idents/Config struct names, param
// names, and -- when present -- the functional-options plumbing's own
// "cfg"/"opts" locals) and against each other -- the same "hard build
// error, never silently renamed" posture checkGoOptionIdentCollisions
// already established, applied to a genuinely different identifier set:
// outputs are independent of whether the blueprint has any params:
// default entries at all, so this check always runs, never gated behind
// hasOptions the way that one is.
func checkGoOutputIdentCollisions(g *goGenerator, allParams []Param, hasOptions bool) error {
	if len(g.blueprint.Outputs) == 0 {
		return nil
	}
	used := map[string]string{}
	for _, dr := range g.blueprint.Resources {
		gr := g.byAddress[dr.Address]
		used[gr.ident] = fmt.Sprintf("resource %s.%s", gr.dr.RI.Type, gr.dr.RI.Name)
		used[gr.configName] = fmt.Sprintf("resource %s.%s's own Config struct", gr.dr.RI.Type, gr.dr.RI.Name)
		if g.blueprint.Referenced[dr.Address] {
			used[lowerFirst(gr.ident)] = fmt.Sprintf("resource %s.%s's own local variable", gr.dr.RI.Type, gr.dr.RI.Name)
		}
	}
	for _, p := range allParams {
		cname, err := camelCase(p.Name)
		if err != nil {
			return err
		}
		used[cname] = fmt.Sprintf("param %q", p.Name)
	}
	if hasOptions {
		used["cfg"] = "the generated functional-options config local"
		used["opts"] = "the generated functional-options parameter"
	}

	seen := map[string]string{} // ident -> output name that first claimed it
	for _, o := range g.blueprint.Outputs {
		ident, err := camelCase(o.Name)
		if err != nil {
			return fmt.Errorf("blueprint: output %q: %w", o.Name, err)
		}
		if owner, ok := used[ident]; ok {
			return fmt.Errorf("blueprint: output %q derives the generated identifier %q, which collides with %s -- rename one in the Ubxfile", o.Name, ident, owner)
		}
		if other, ok := seen[ident]; ok {
			return fmt.Errorf("blueprint: outputs %q and %q both derive the generated identifier %q -- rename one in the Ubxfile's own outputs: block", other, o.Name, ident)
		}
		seen[ident] = o.Name
	}
	return nil
}

// defaultLiteral renders a params: default entry's own already-parsed
// Default value (Param.Default: string/int/bool, matching Type -- see
// ubxfile.go's own parseDefaultValue) as a Go literal, seeding the
// generated options struct's own zero-value defaults.
func defaultLiteral(p Param) (string, error) {
	switch v := p.Default.(type) {
	case string:
		return fmt.Sprintf("%q", v), nil
	case int:
		return strconv.Itoa(v), nil
	case bool:
		return strconv.FormatBool(v), nil
	default:
		return "", fmt.Errorf("param %q: unrecognized default value type %T", p.Name, p.Default)
	}
}

// renderGoOptions renders the functional-options plumbing for this
// blueprint's own params: default entries -- Option/options/With<Param> --
// deliberately matching provider/acquire.go's own AcquireOption/
// acquireConfig/With* shape, the established Go idiom this codebase
// already uses for optional/defaulted call-time values (docs/blueprint.md's
// "Resolved defaults" section has the full account, including why this was
// chosen over a Config-struct-of-pointers alternative). Required params
// stay direct positional arguments; only params: default entries move onto
// the options struct, since Go has no native default-argument syntax to
// give them for free -- TS/Python's OWN generators (tsgen.go/pygen.go) do
// NOT need an equivalent: both languages have native default parameters,
// a real, deliberate per-language divergence, not an oversight
// (docs/blueprint.md's own "Multi-language codegen" section).
func renderGoOptions(b *strings.Builder, defaulted []Param) error {
	b.WriteString("// Option overrides one params: default value not explicitly passed by the caller.\n")
	b.WriteString("type Option func(*options)\n\n")

	b.WriteString("type options struct {\n")
	for _, p := range defaulted {
		cname, err := camelCase(p.Name)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "\t%s %s\n", cname, p.Type.GoType())
	}
	b.WriteString("}\n\n")

	for _, p := range defaulted {
		cname, err := camelCase(p.Name)
		if err != nil {
			return err
		}
		wname, err := pascalCase(p.Name)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "// With%s overrides the %q params: default.\n", wname, p.Name)
		fmt.Fprintf(b, "func With%s(v %s) Option {\n", wname, p.Type.GoType())
		fmt.Fprintf(b, "\treturn func(o *options) { o.%s = v }\n", cname)
		b.WriteString("}\n\n")
	}
	return nil
}

// checkGoOptionIdentCollisions guards the new package-level (Option,
// With<Param>) and function-local (cfg, opts) identifiers the functional-
// options pattern introduces against every identifier this same generated
// file already derives from resource/param names -- a real, if narrow,
// risk once a blueprint author can freely choose resource and param names
// (e.g. a resource literally named "option", or a param literally named
// "opts"). Matches this codebase's own "hard build error, never silently
// overwritten" posture for identifier collisions rather than silently
// renaming around it.
func checkGoOptionIdentCollisions(g *goGenerator, allParams []Param, defaulted []Param, hasOptions bool) error {
	if !hasOptions {
		return nil
	}
	used := map[string]string{}
	for _, dr := range g.blueprint.Resources {
		gr := g.byAddress[dr.Address]
		used[gr.ident] = fmt.Sprintf("resource %s.%s", gr.dr.RI.Type, gr.dr.RI.Name)
		used[gr.configName] = fmt.Sprintf("resource %s.%s's own Config struct", gr.dr.RI.Type, gr.dr.RI.Name)
		if g.blueprint.Referenced[dr.Address] {
			used[lowerFirst(gr.ident)] = fmt.Sprintf("resource %s.%s's own local variable", gr.dr.RI.Type, gr.dr.RI.Name)
		}
	}
	for _, p := range allParams {
		cname, err := camelCase(p.Name)
		if err != nil {
			return err
		}
		used[cname] = fmt.Sprintf("param %q", p.Name)
	}

	candidates := []string{"Option", "options", "cfg", "opts"}
	for _, p := range defaulted {
		wname, err := pascalCase(p.Name)
		if err != nil {
			return err
		}
		candidates = append(candidates, "With"+wname)
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		if owner, ok := used[c]; ok {
			return fmt.Errorf("blueprint: generated identifier %q (needed for the params: default/functional-options pattern) collides with %s -- rename it", c, owner)
		}
		if seen[c] {
			return fmt.Errorf("blueprint: two default params both derive the generated identifier %q -- rename one in the Ubxfile's own params: block", c)
		}
		seen[c] = true
	}
	return nil
}
