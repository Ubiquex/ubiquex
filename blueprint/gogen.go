package blueprint

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// GenerateGo compiles a resolved intent draft (already drafted from an
// Ubxfile's own resources: prose, exactly once, by the caller -- this
// function makes no intent-provider call of its own) into a real,
// self-contained Go package: one file with a ResourceBinding+Config
// struct pair per resource type, one file with the blueprint's own
// parameterized function. docs/blueprint.md has the full design and the
// reasoning behind every decision named in this file's own comments.
//
// blueprintName is the caller's own chosen name for this blueprint
// (Slice 1: the Ubxfile's own directory basename) -- GenerateGo has no
// opinion on where that name comes from, only that it's a valid
// hyphen/underscore-separated identifier source.
//
// Returns a flat map of filename -> content ("go.mod", "bindings.go",
// "<pkg>.go") -- deliberately NOT a per-service-directory tree the way
// sdk/codegen/templates/go's own GeneratedRepo produces: that structure
// exists to survive a real provider's hundreds of types in one repo, a
// single blueprint's own handful of resources never approaches that
// scale (docs/blueprint.md).
func GenerateGo(blueprintName string, ubxfile *Ubxfile, intent *resolver.IntentFile) (map[string]string, error) {
	pkgName, err := packageIdent(blueprintName)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}
	funcName, err := pascalCase(blueprintName)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}

	if len(intent.Destroys) > 0 {
		return nil, fmt.Errorf("blueprint: resolved draft names %d destroy(s) -- a blueprint template describes resources to create, never to destroy", len(intent.Destroys))
	}

	paramByName := map[string]Param{}
	for _, p := range ubxfile.Params {
		paramByName[p.Name] = p
	}

	g := &generator{
		stack:      intent.Stack,
		byAddress:  map[string]*decodedResource{},
		params:     paramByName,
		referenced: map[string]bool{},
	}

	resources, err := g.decodeResources(intent)
	if err != nil {
		return nil, err
	}
	order, err := topoSort(resources)
	if err != nil {
		return nil, err
	}

	files := map[string]string{
		"go.mod":      fmt.Sprintf("module %s\n\ngo 1.23\n\nrequire github.com/ubiquex/ubx-sdk-go v0.0.0\n", pkgName),
		"bindings.go": renderBindings(pkgName, resources),
	}
	fnSrc, err := renderFunction(pkgName, funcName, ubxfile.Params, resources, order, g.referenced)
	if err != nil {
		return nil, err
	}
	files[pkgName+".go"] = fnSrc

	return files, nil
}

// decodedResource is one ResourceIntent, decoded into everything
// codegen needs: its own Go identifiers, its Config's own fields (sorted
// by wire key, for determinism), each field's already-rendered Go value
// expression, and the set of sibling resource identifiers it depends on
// (from $ref markers found while rendering, unioned with any explicit
// DependsOn -- docs/blueprint.md).
type decodedResource struct {
	ri         resolver.ResourceIntent
	ident      string
	configName string
	fields     []fieldEntry
	valueExprs map[string]string
	deps       map[string]bool
}

type fieldEntry struct {
	wireKey string
	goName  string
}

// generator holds the state one GenerateGo call threads through
// decoding every resource: the draft's own stack name (every $ref/
// depends_on address is expected to be prefixed with it -- a blueprint
// template has no notion of a cross-stack reference, docs/blueprint.md),
// the address->resource index (populated before any value is rendered,
// so a $ref can point forward OR backward in the draft's own resource
// order), the declared params (for {param_name} substitution), and which
// resource identifiers are actually referenced by a sibling (so
// renderFunction only assigns a local variable where one is actually
// used -- an unused local is a Go compile error, not just untidy).
type generator struct {
	stack      string
	byAddress  map[string]*decodedResource
	params     map[string]Param
	referenced map[string]bool
}

func (g *generator) decodeResources(intent *resolver.IntentFile) ([]*decodedResource, error) {
	out := make([]*decodedResource, 0, len(intent.Resources))
	seenIdent := map[string]string{} // ident -> the resource Name that first claimed it
	for _, ri := range intent.Resources {
		if ri.Op != resolver.OpCreate {
			return nil, fmt.Errorf("blueprint: resource %s.%s: op %q not supported -- a blueprint template only ever describes new resources (op %q)", ri.Type, ri.Name, ri.Op, resolver.OpCreate)
		}
		ident, err := pascalCase(ri.Name)
		if err != nil {
			return nil, fmt.Errorf("blueprint: resource %s.%s: %w", ri.Type, ri.Name, err)
		}
		if other, ok := seenIdent[ident]; ok {
			return nil, fmt.Errorf("blueprint: resource names %q and %q both derive the Go identifier %q -- rename one in resources.md", other, ri.Name, ident)
		}
		seenIdent[ident] = ri.Name

		dr := &decodedResource{ri: ri, ident: ident, configName: ident + "Config"}
		out = append(out, dr)
		g.byAddress[ri.Type+"."+ri.Name] = dr
	}

	for _, dr := range out {
		if err := g.build(dr); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (g *generator) build(dr *decodedResource) error {
	var configMap map[string]json.RawMessage
	if err := json.Unmarshal(dr.ri.Config, &configMap); err != nil {
		return fmt.Errorf("blueprint: resource %s.%s: config is not a JSON object: %w", dr.ri.Type, dr.ri.Name, err)
	}

	keys := make([]string, 0, len(configMap))
	for k := range configMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	dr.deps = map[string]bool{}
	dr.valueExprs = map[string]string{}
	for _, k := range keys {
		goName, err := pascalCase(k)
		if err != nil {
			return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.ri.Type, dr.ri.Name, k, err)
		}
		dr.fields = append(dr.fields, fieldEntry{wireKey: k, goName: goName})

		var v any
		if err := json.Unmarshal(configMap[k], &v); err != nil {
			return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.ri.Type, dr.ri.Name, k, err)
		}
		expr, err := g.renderAny(dr, v)
		if err != nil {
			return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.ri.Type, dr.ri.Name, k, err)
		}
		dr.valueExprs[goName] = expr
	}

	for _, dep := range dr.ri.DependsOn {
		target, _, err := g.resolveAddress(dep)
		if err != nil {
			return fmt.Errorf("blueprint: resource %s.%s: depends_on %q: %w", dr.ri.Type, dr.ri.Name, dep, err)
		}
		dr.deps[target.ident] = true
		g.referenced[target.ident] = true
	}
	return nil
}

// resolveAddress parses a canonical "<stack>.<type>.<name>[.<path>...]"
// address (core/resolver/refs.go's own $ref shape; ResourceIntent.
// DependsOn's own doc comment names the identical convention) against
// this draft's own resources.
func (g *generator) resolveAddress(addr string) (target *decodedResource, path []string, err error) {
	parts := strings.Split(addr, ".")
	if len(parts) < 3 {
		return nil, nil, fmt.Errorf("malformed address %q", addr)
	}
	if parts[0] != g.stack {
		return nil, nil, fmt.Errorf("address %q: cross-stack references aren't supported in a blueprint template (Slice 1)", addr)
	}
	target, ok := g.byAddress[parts[1]+"."+parts[2]]
	if !ok {
		return nil, nil, fmt.Errorf("address %q: target resource not found among this blueprint's own resources", addr)
	}
	return target, parts[3:], nil
}

var placeholderToken = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)
var placeholderWholeString = regexp.MustCompile(`^\{([a-zA-Z0-9_]+)\}$`)

// renderAny renders one already-JSON-decoded value into a Go source
// expression, recursively -- a $ref marker becomes a Computed.Field()
// chain (recording a dependency edge as a side effect on dr), a string
// containing {param_name} tokens becomes a parameter reference or an
// fmt.Sprintf call, anything else becomes a literal Go value
// (docs/blueprint.md's "Value translation" section has the full account
// of each case).
func (g *generator) renderAny(dr *decodedResource, v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "nil", nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return strconv.FormatInt(int64(t), 10), nil
		}
		return strconv.FormatFloat(t, 'g', -1, 64), nil
	case string:
		return g.renderString(dr, t)
	case map[string]any:
		if to, ok := refTarget(t); ok {
			return g.renderRef(dr, to)
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString("map[string]any{")
		for i, k := range keys {
			expr, err := g.renderAny(dr, t[k])
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
			expr, err := g.renderAny(dr, e)
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

// refTarget reports whether v is exactly a {"$ref": {"to": "..."}}
// marker (core/resolver/refs.go's own markerRef shape) and, if so,
// returns its "to" address.
func refTarget(v map[string]any) (string, bool) {
	if len(v) != 1 {
		return "", false
	}
	inner, ok := v["$ref"]
	if !ok {
		return "", false
	}
	innerMap, ok := inner.(map[string]any)
	if !ok {
		return "", false
	}
	to, ok := innerMap["to"].(string)
	if !ok {
		return "", false
	}
	return to, true
}

func (g *generator) renderRef(dr *decodedResource, to string) (string, error) {
	target, path, err := g.resolveAddress(to)
	if err != nil {
		return "", fmt.Errorf("$ref %q: %w", to, err)
	}
	dr.deps[target.ident] = true
	g.referenced[target.ident] = true

	expr := lowerFirst(target.ident)
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
func (g *generator) renderString(dr *decodedResource, s string) (string, error) {
	if expr, handled, err := g.renderEmbeddedRefString(dr, s); err != nil {
		return "", err
	} else if handled {
		return expr, nil
	}

	if m := placeholderWholeString.FindStringSubmatch(s); m != nil {
		return g.paramRef(m[1])
	}

	matches := placeholderToken.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return fmt.Sprintf("%q", s), nil
	}

	var args []string
	for _, m := range matches {
		ref, err := g.paramRef(m[1])
		if err != nil {
			return "", err
		}
		args = append(args, ref)
	}
	literal := placeholderToken.ReplaceAllString(s, "%v")
	return fmt.Sprintf("fmt.Sprintf(%q, %s)", literal, strings.Join(args, ", ")), nil
}

// embeddedRefPattern matches one {"$ref":{"to":"<addr>"}} marker's own
// textual appearance inside a JSON-embedded string value (tolerant of
// whitespace, since this text is drafted by the intent provider, not
// re-marshaled by this package) -- the exact, narrow shape
// core/resolver/refs.go's own containsMarker/asMarker(markerRef) walk
// already expects to find embedded in a string leaf's own decoded JSON
// (docs/blueprint.md's "JSON-embedded refs" adversarial case). Only
// $ref is matched, deliberately: a blueprint template only ever
// describes new, same-stack resources (op create, no cross-stack
// addressing -- resolveAddress's own stack-mismatch check), so $cross/
// $secret/$computed markers never appear in a blueprint's own drafted
// Config at build time in the first place.
var embeddedRefPattern = regexp.MustCompile(`\{\s*"\$ref"\s*:\s*\{\s*"to"\s*:\s*"([^"]*)"\s*\}\s*\}`)

// renderEmbeddedRefString detects and translates a JSON-embedded $ref --
// a string whose own decoded content carries a real {"$ref":{"to":...}}
// marker one level down (e.g. an IAM policy document's "Resource" field
// naming a sibling resource's ARN) -- into a Go expression that
// reconstructs the identical text at RUN time, splicing in the
// referenced resource's own real, RUNTIME-computed address (Computed.
// Address(), which threads the CALLING stack's own actual name --
// sdk.Stack's own argument, never known at blueprint build time) in
// place of each match's own literal "to" text.
//
// This closes a real, live-verified gap Slice 1's own docs left open:
// Slice 1 rendered a JSON-embedded-ref string VERBATIM as an opaque Go
// literal on the reasoning that the resolver's own reference walk
// resolves it later, at call/resolve time -- true, but that walk
// resolves the address exactly as written, and Slice 1's own literal
// bakes in the BUILD-time blueprint stack name (e.g. "ci-platform", the
// Ubxfile directory's own name) as the address prefix. A real stack
// calling the built function under any OTHER name (the ordinary case --
// "payments", say) then fails resolution outright: "reference does not
// resolve to any known resource or attribute" (confirmed live, UBI-74
// Slice 2's own real-AWS verification, not hypothesized). Every
// non-string $ref (a structural map value, handled by renderRef) already
// gets this right, since Computed.Address() is always computed fresh at
// RUN time from the actual calling stack's own name -- this brings the
// JSON-embedded case to the same correctness bar.
func (g *generator) renderEmbeddedRefString(dr *decodedResource, s string) (expr string, handled bool, err error) {
	var decoded any
	if err := json.Unmarshal([]byte(s), &decoded); err != nil {
		return "", false, nil
	}
	if !containsRefMarker(decoded) {
		return "", false, nil
	}

	matches := embeddedRefPattern.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return "", false, fmt.Errorf("resource %s.%s: config field decodes as JSON containing a $ref marker, but its own literal text doesn't match the expected {\"$ref\":{\"to\":\"...\"}} shape -- can't safely translate it", dr.ri.Type, dr.ri.Name)
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
		target, path, rerr := g.resolveAddress(addr)
		if rerr != nil {
			return "", false, fmt.Errorf("embedded $ref %q: %w", addr, rerr)
		}
		dr.deps[target.ident] = true
		g.referenced[target.ident] = true

		refExpr := lowerFirst(target.ident)
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

// containsRefMarker reports whether v (an already-decoded JSON value)
// carries a {"$ref": {...}} marker anywhere within it, at any depth --
// the same shape-only walk core/resolver/refs.go's own containsMarker
// performs, narrowed to $ref only (see embeddedRefPattern's own doc
// comment for why the narrowing is deliberate here).
func containsRefMarker(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 1 {
			if _, ok := t["$ref"]; ok {
				return true
			}
		}
		for _, vv := range t {
			if containsRefMarker(vv) {
				return true
			}
		}
		return false
	case []any:
		for _, vv := range t {
			if containsRefMarker(vv) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// paramRef returns the Go expression referencing param name's own value: a
// bare identifier for a required param (a direct function argument, same
// as Slice 1), or a cfg.<field> selector for a params: default (Slice 2 --
// see renderOptions/the functional-options design in docs/blueprint.md's
// "Resolved defaults" section; a default param is no longer a bare
// function argument, its value lives on the options struct instead).
func (g *generator) paramRef(name string) (string, error) {
	p, ok := g.params[name]
	if !ok {
		return "", fmt.Errorf("prose references {%s}, but no such param is declared in the Ubxfile's own params: block", name)
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

// topoSort orders resources so every dependency (via $ref or
// depends_on) is created before its dependent -- Go requires a
// referenced resource's own sdk.Resource() call to already have run
// before .Field() can be called on its return value. Kahn's algorithm,
// deterministic: the initial queue and every dependents[] list are both
// built by walking resources in the draft's own declaration order, never
// map iteration order.
func topoSort(resources []*decodedResource) ([]*decodedResource, error) {
	byIdent := make(map[string]*decodedResource, len(resources))
	inDegree := make(map[string]int, len(resources))
	dependents := map[string][]string{}
	for _, r := range resources {
		byIdent[r.ident] = r
		inDegree[r.ident] = 0
	}
	for _, r := range resources {
		depNames := make([]string, 0, len(r.deps))
		for d := range r.deps {
			depNames = append(depNames, d)
		}
		sort.Strings(depNames)
		for _, dep := range depNames {
			inDegree[r.ident]++
			dependents[dep] = append(dependents[dep], r.ident)
		}
	}

	var queue []string
	for _, r := range resources {
		if inDegree[r.ident] == 0 {
			queue = append(queue, r.ident)
		}
	}

	order := make([]*decodedResource, 0, len(resources))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, byIdent[id])
		for _, dep := range dependents[id] {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(order) != len(resources) {
		return nil, fmt.Errorf("blueprint: dependency cycle detected among resources (a $ref or depends_on chain loops back on itself)")
	}
	return order, nil
}

// renderBindings renders bindings.go: one Config struct + one
// ResourceBinding var per resource, in the draft's own declaration
// order (not topo order -- this file's own ordering has no compile-time
// constraint, declaration order reads most naturally).
func renderBindings(pkgName string, resources []*decodedResource) string {
	var b strings.Builder
	b.WriteString("// Code generated by `ubx blueprint build`. DO NOT EDIT.\n")
	fmt.Fprintf(&b, "package %s\n\n", pkgName)
	b.WriteString("import sdk \"github.com/ubiquex/ubx-sdk-go/runtime\"\n\n")

	for _, dr := range resources {
		fmt.Fprintf(&b, "type %s struct {\n", dr.configName)
		for _, f := range dr.fields {
			fmt.Fprintf(&b, "\t%s any\n", f.goName)
		}
		b.WriteString("}\n\n")

		fmt.Fprintf(&b, "var %s = sdk.ResourceBinding{\n", dr.ident)
		fmt.Fprintf(&b, "\tWireType: %q,\n", dr.ri.Type)
		b.WriteString("\tFields: sdk.FieldMap{\n")
		for _, f := range dr.fields {
			fmt.Fprintf(&b, "\t\t%q: sdk.FieldSpec{WireName: %q},\n", f.goName, f.wireKey)
		}
		b.WriteString("\t},\n")
		b.WriteString("}\n\n")
	}
	return b.String()
}

// renderFunction renders <pkg>.go: the blueprint's own exported,
// parameterized function -- required params directly, in the Ubxfile's
// own declared order, as positional Go arguments; any params: default
// entries via a trailing "opts ...Option" (docs/blueprint.md's "Resolved
// defaults" design, renderOptions below) -- then one sdk.Resource() call
// per resource in TOPOLOGICAL order (so every $ref-derived .Field()
// reference already has a variable to read), assigning a local variable
// only for a resource referenced has, this avoids an unused-local Go
// compile error.
func renderFunction(pkgName, funcName string, params []Param, resources []*decodedResource, order []*decodedResource, referenced map[string]bool) (string, error) {
	var required, defaulted []Param
	for _, p := range params {
		if p.Required {
			required = append(required, p)
		} else {
			defaulted = append(defaulted, p)
		}
	}
	hasOptions := len(defaulted) > 0

	if err := checkOptionIdentCollisions(resources, referenced, params, defaulted, hasOptions); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("// Code generated by `ubx blueprint build`. DO NOT EDIT.\n")
	fmt.Fprintf(&b, "package %s\n\n", pkgName)

	usesSprintf := false
	for _, dr := range resources {
		for _, expr := range dr.valueExprs {
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
		if err := renderOptions(&b, defaulted); err != nil {
			return "", err
		}
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

	fmt.Fprintf(&b, "func %s {\n", sig.String())
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
	for _, dr := range order {
		varName := lowerFirst(dr.ident)
		call := fmt.Sprintf("sdk.Resource(%s, %q, %s{\n", dr.ident, dr.ri.Name, dr.configName)
		for _, f := range dr.fields {
			call += fmt.Sprintf("\t\t%s: %s,\n", f.goName, dr.valueExprs[f.goName])
		}
		call += "\t})"
		if referenced[dr.ident] {
			fmt.Fprintf(&b, "\t%s := %s\n", varName, call)
		} else {
			fmt.Fprintf(&b, "\t%s\n", call)
		}
	}
	b.WriteString("}\n")
	return b.String(), nil
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

// renderOptions renders the functional-options plumbing for this
// blueprint's own params: default entries -- Option/options/With<Param> --
// deliberately matching provider/acquire.go's own AcquireOption/
// acquireConfig/With* shape, the established Go idiom this codebase
// already uses for optional/defaulted call-time values (confirmed by
// searching for it before inventing a new convention -- docs/blueprint.md's
// "Resolved defaults" section has the full account, including why this was
// chosen over a Config-struct-of-pointers alternative). Required params
// stay direct positional arguments (renderFunction); only params: default
// entries move onto the options struct, since Go has no native default-
// argument syntax to give them for free.
func renderOptions(b *strings.Builder, defaulted []Param) error {
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

// checkOptionIdentCollisions guards the new package-level (Option,
// With<Param>) and function-local (cfg, opts) identifiers the functional-
// options pattern introduces against every identifier this same generated
// file already derives from resource/param names -- a real, if narrow,
// risk once a blueprint author can freely choose resource and param names
// (e.g. a resource literally named "option", or a param literally named
// "opts"). Matches this codebase's own "hard build error, never silently
// overwritten" posture for identifier collisions (docs/blueprint.md,
// gotmpl's own "detect and report, don't invent a rename scheme
// speculatively" precedent) rather than silently renaming around it.
func checkOptionIdentCollisions(resources []*decodedResource, referenced map[string]bool, allParams []Param, defaulted []Param, hasOptions bool) error {
	if !hasOptions {
		return nil
	}
	used := map[string]string{}
	for _, dr := range resources {
		used[dr.ident] = fmt.Sprintf("resource %s.%s", dr.ri.Type, dr.ri.Name)
		used[dr.configName] = fmt.Sprintf("resource %s.%s's own Config struct", dr.ri.Type, dr.ri.Name)
		if referenced[dr.ident] {
			used[lowerFirst(dr.ident)] = fmt.Sprintf("resource %s.%s's own local variable", dr.ri.Type, dr.ri.Name)
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
