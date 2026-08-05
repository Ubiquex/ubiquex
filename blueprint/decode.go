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

// decodedField is one resource Config key, already JSON-decoded into a
// generic Go value (string/float64/bool/nil/map[string]any/[]any) --
// language-neutral; turning Value into source syntax is each language
// generator's own job (gogen.go/tsgen.go/pygen.go), never this file's.
// Slice 1-3 built exactly this decode step directly inside gogen.go,
// Go-only; Slice 4 factors out everything that ISN'T Go-specific here so
// GenerateTS/GeneratePython (new this slice) don't re-derive it
// independently -- see docs/blueprint.md's own "Multi-language codegen"
// section for the full account of what's shared vs. what genuinely isn't.
type decodedField struct {
	WireKey string
	Value   any
}

// decodedResource is one ResourceIntent, decoded into everything every
// language's codegen needs that ISN'T language-specific: its own
// declaration-order Config fields (sorted by wire key, for determinism)
// and (via decodedBlueprint.Deps bookkeeping, not a field here) the set
// of sibling resource addresses it depends on. Per-language identifiers
// (a Go PascalCase type name, a TS camelCase property, a Python
// snake_case module) are deliberately NOT decided here -- each generator
// derives (and collision-checks) its own from RI.Name/WireKey
// independently, since casing conventions genuinely differ per language
// target (docs/blueprint.md).
type decodedResource struct {
	RI      resolver.ResourceIntent
	Address string // "<type>.<name>" -- this blueprint's own resources are always same-stack
	Fields  []decodedField
	Deps    map[string]bool // sibling addresses this resource's own Config/depends_on references
}

// decodedOutput is one outputs: entry (UBI-128), already resolved
// against this blueprint's own resources -- shared by every codegen
// target exactly like decodedResource's own fields are, so "which
// resource+path does this output name" is answered once, not
// re-derived independently per language.
type decodedOutput struct {
	Name   string
	Target *decodedResource
	Path   []string
}

// decodedBlueprint is one resolved intent draft's own language-neutral
// decode -- shared by every codegen target (GenerateGo/GenerateTS/
// GeneratePython).
type decodedBlueprint struct {
	Stack      string
	Resources  []*decodedResource // declaration order
	Order      []*decodedResource // topological order (every dependency before its dependent)
	Referenced map[string]bool    // address -> true if some sibling resource depends on it OR is targeted by an output
	Outputs    []decodedOutput    // outputs:, in declaration order

	byAddress map[string]*decodedResource
}

// decodeBlueprint decodes intent into a decodedBlueprint -- validates
// every resource is op create (a blueprint template only ever describes
// resources to create, never modify/destroy), decodes each resource's
// own Config into sorted decodedFields, and resolves+validates every
// $ref/depends_on address against this SAME blueprint's own resources (a
// blueprint template has no notion of a cross-stack reference). Every
// language's own codegen can trust every address it later encounters
// while rendering a Field's own Value already resolves cleanly -- this
// is the ONE place that validation happens, not re-done independently by
// every language's own renderer.
//
// outputs (UBI-128) is the Ubxfile's own outputs: declaration, resolved
// here against the SAME resources (by slug, resolveOutputTarget below,
// never repeating the stack/type prefix an ordinary $ref address needs
// -- an output's own Target already names its resource unambiguously by
// the blueprint author's own declared slug) -- every output's own
// target resource is marked Referenced exactly like a sibling $ref
// would be, since the generated function needs a local variable to
// return its own .Field(...) from regardless of whether anything ELSE
// inside the blueprint also references it.
func decodeBlueprint(intent *resolver.IntentFile, outputs []Output) (*decodedBlueprint, error) {
	b := &decodedBlueprint{
		Stack:      intent.Stack,
		Referenced: map[string]bool{},
		byAddress:  map[string]*decodedResource{},
	}

	for _, ri := range intent.Resources {
		if ri.Op != resolver.OpCreate {
			return nil, fmt.Errorf("blueprint: resource %s.%s: op %q not supported -- a blueprint template only ever describes new resources (op %q)", ri.Type, ri.Name, ri.Op, resolver.OpCreate)
		}
		address := ri.Type + "." + ri.Name
		if _, dup := b.byAddress[address]; dup {
			return nil, fmt.Errorf("blueprint: duplicate resource %s.%s in resolved draft", ri.Type, ri.Name)
		}
		dr := &decodedResource{RI: ri, Address: address, Deps: map[string]bool{}}
		b.Resources = append(b.Resources, dr)
		b.byAddress[address] = dr
	}

	for _, dr := range b.Resources {
		if err := b.decodeFields(dr); err != nil {
			return nil, err
		}
		for _, dep := range dr.RI.DependsOn {
			target, _, err := b.resolveAddress(dep)
			if err != nil {
				return nil, fmt.Errorf("blueprint: resource %s.%s: depends_on %q: %w", dr.RI.Type, dr.RI.Name, dep, err)
			}
			dr.Deps[target.Address] = true
			b.Referenced[target.Address] = true
		}
	}

	order, err := topoSortResources(b.Resources)
	if err != nil {
		return nil, err
	}
	b.Order = order

	for _, o := range outputs {
		target, path, err := b.resolveOutputTarget(o.Target)
		if err != nil {
			return nil, fmt.Errorf("blueprint: output %q: %w", o.Name, err)
		}
		b.Referenced[target.Address] = true
		b.Outputs = append(b.Outputs, decodedOutput{Name: o.Name, Target: target, Path: path})
	}

	return b, nil
}

// resolveOutputTarget parses one outputs: entry's own "<resource-slug>.
// <attribute>[.<nested>...]" Target against this blueprint's own
// resources, by SLUG (RI.Name) -- distinct from resolveAddress's own
// "<stack>.<type>.<name>[.path]" address form: an output's own Target
// never repeats the stack/type prefix, since the blueprint author
// already knows which resource they mean by its own declared slug
// alone (docs/blueprint.md's own "outputs:" section has the full
// account).
func (b *decodedBlueprint) resolveOutputTarget(target string) (dr *decodedResource, path []string, err error) {
	parts := strings.Split(target, ".")
	if len(parts) < 2 {
		return nil, nil, fmt.Errorf("malformed output target %q -- want \"<resource-slug>.<attribute>\"", target)
	}
	slug := parts[0]
	var found *decodedResource
	for _, r := range b.Resources {
		if r.RI.Name == slug {
			if found != nil {
				return nil, nil, fmt.Errorf("target %q: resource slug %q is ambiguous -- more than one resource in this blueprint shares that name", target, slug)
			}
			found = r
		}
	}
	if found == nil {
		return nil, nil, fmt.Errorf("target %q: no resource with slug %q in this blueprint", target, slug)
	}
	return found, parts[1:], nil
}

func (b *decodedBlueprint) decodeFields(dr *decodedResource) error {
	var configMap map[string]json.RawMessage
	if err := json.Unmarshal(dr.RI.Config, &configMap); err != nil {
		return fmt.Errorf("blueprint: resource %s.%s: config is not a JSON object: %w", dr.RI.Type, dr.RI.Name, err)
	}
	keys := make([]string, 0, len(configMap))
	for k := range configMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		var v any
		if err := json.Unmarshal(configMap[k], &v); err != nil {
			return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.RI.Type, dr.RI.Name, k, err)
		}
		dr.Fields = append(dr.Fields, decodedField{WireKey: k, Value: v})

		for _, addr := range collectRefs(v) {
			target, _, err := b.resolveAddress(addr)
			if err != nil {
				return fmt.Errorf("blueprint: resource %s.%s: config field %q: %w", dr.RI.Type, dr.RI.Name, k, err)
			}
			dr.Deps[target.Address] = true
			b.Referenced[target.Address] = true
		}
	}
	return nil
}

// resolveAddress parses a canonical "<stack>.<type>.<name>[.<path>...]"
// address (core/resolver/refs.go's own $ref shape; ResourceIntent.
// DependsOn's own doc comment names the identical convention) against
// this blueprint's own resources -- shared by every language's own
// renderAny (never re-validated independently per language, since
// decodeBlueprint already confirmed every reference found while decoding
// resolves cleanly; a language's own renderAny calls this again anyway,
// cheaply, so each generator stays self-contained/defensive rather than
// trusting a global invariant silently).
func (b *decodedBlueprint) resolveAddress(addr string) (target *decodedResource, path []string, err error) {
	parts := strings.Split(addr, ".")
	if len(parts) < 3 {
		return nil, nil, fmt.Errorf("malformed address %q", addr)
	}
	if parts[0] != b.Stack {
		return nil, nil, fmt.Errorf("address %q: cross-stack references aren't supported in a blueprint template", addr)
	}
	target, ok := b.byAddress[parts[1]+"."+parts[2]]
	if !ok {
		return nil, nil, fmt.Errorf("address %q: target resource not found among this blueprint's own resources", addr)
	}
	return target, parts[3:], nil
}

// refTarget reports whether v is exactly a {"$ref": {"to": "..."}}
// marker (core/resolver/refs.go's own markerRef shape) and, if so,
// returns its "to" address.
func refTarget(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return "", false
	}
	inner, ok := m["$ref"]
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

// containsRefMarker reports whether v (an already-decoded JSON value)
// carries a {"$ref": {...}} marker anywhere within it, at any depth --
// the same shape-only walk core/resolver/refs.go's own containsMarker
// performs, narrowed to $ref only (a blueprint template only ever
// describes new, same-stack resources -- op create, no cross-stack
// addressing -- so $cross/$secret/$computed markers never appear in a
// blueprint's own drafted Config at build time in the first place).
func containsRefMarker(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		if _, ok := refTarget(t); ok {
			return true
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

// collectRefs walks an already-JSON-decoded value and returns every $ref
// address it finds, at any depth -- a direct marker (map/array
// recursion) OR one embedded one level down inside a string value's own
// re-parsed JSON content (docs/blueprint.md's "JSON-embedded refs"
// adversarial case, e.g. an IAM policy document's "Resource" field
// naming a sibling resource's ARN). Shared by decodeBlueprint
// (dependency/topo-order bookkeeping) and every language's own renderAny
// (which re-walks the SAME shape to emit that language's own ref-access
// syntax) -- deliberately the same walk, so "does this value contain a
// ref" is answered identically in both places.
func collectRefs(v any) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		if to, ok := refTarget(t); ok {
			return []string{to}
		}
		for _, vv := range t {
			out = append(out, collectRefs(vv)...)
		}
	case []any:
		for _, vv := range t {
			out = append(out, collectRefs(vv)...)
		}
	case string:
		var decoded any
		if err := json.Unmarshal([]byte(t), &decoded); err == nil && containsRefMarker(decoded) {
			out = append(out, collectRefs(decoded)...)
		}
	}
	return out
}

// topoSortResources orders resources so every dependency (via $ref or
// depends_on) is created before its dependent -- every generated
// language requires a referenced resource's own call to already have run
// before its return value can be drilled into. Kahn's algorithm,
// deterministic: the initial queue and every dependents[] list are both
// built by walking resources in the draft's own declaration order, never
// map iteration order.
func topoSortResources(resources []*decodedResource) ([]*decodedResource, error) {
	byAddr := make(map[string]*decodedResource, len(resources))
	inDegree := make(map[string]int, len(resources))
	dependents := map[string][]string{}
	for _, r := range resources {
		byAddr[r.Address] = r
		inDegree[r.Address] = 0
	}
	for _, r := range resources {
		deps := make([]string, 0, len(r.Deps))
		for d := range r.Deps {
			deps = append(deps, d)
		}
		sort.Strings(deps)
		for _, dep := range deps {
			inDegree[r.Address]++
			dependents[dep] = append(dependents[dep], r.Address)
		}
	}

	var queue []string
	for _, r := range resources {
		if inDegree[r.Address] == 0 {
			queue = append(queue, r.Address)
		}
	}

	order := make([]*decodedResource, 0, len(resources))
	for len(queue) > 0 {
		addr := queue[0]
		queue = queue[1:]
		order = append(order, byAddr[addr])
		for _, dep := range dependents[addr] {
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

// ---------------------------------------------------------------------
// {param_name} placeholder tokens -- the SAME grammar regardless of
// which language ultimately renders a match (docs/blueprint.md's own
// "Value translation"): only the rendered SYNTAX differs per language
// (fmt.Sprintf vs. a template literal vs. an f-string), never which text
// counts as a placeholder.
// ---------------------------------------------------------------------

// placeholderToken/placeholderWholeString match one {param_name} token,
// OPTIONALLY followed by a single arithmetic operator and an integer
// literal constant -- {param_name} (a bare reference) or {param_name *
// 86400}/{param_name / 60}/etc (UBI-123: a simple, deliberately narrow
// "unit conversion or derived value" form -- exactly one operator, one
// param, one literal constant, never a general expression language; see
// each language's own paramExpr for the full account of why this exists
// and what it deliberately doesn't support). Capture groups: (1) param
// name, (2) operator (empty if bare), (3) literal (empty if bare).
var placeholderToken = regexp.MustCompile(`\{([a-zA-Z0-9_]+)(?:\s*([+\-*/])\s*([0-9]+)\s*)?\}`)
var placeholderWholeString = regexp.MustCompile(`^\{([a-zA-Z0-9_]+)(?:\s*([+\-*/])\s*([0-9]+)\s*)?\}$`)

// embeddedRefPattern matches one {"$ref":{"to":"<addr>"}} marker's own
// textual appearance inside a JSON-embedded string value (tolerant of
// whitespace, since this text is drafted by the intent provider, not
// re-marshaled by this package) -- the exact, narrow shape
// core/resolver/refs.go's own containsMarker/asMarker(markerRef) walk
// already expects to find embedded in a string leaf's own decoded JSON
// (docs/blueprint.md's "JSON-embedded refs" adversarial case). Only
// $ref is matched, deliberately: a blueprint template only ever
// describes new, same-stack resources (op create, no cross-stack
// addressing), so $cross/$secret/$computed markers never appear in a
// blueprint's own drafted Config at build time in the first place.
var embeddedRefPattern = regexp.MustCompile(`\{\s*"\$ref"\s*:\s*\{\s*"to"\s*:\s*"([^"]*)"\s*\}\s*\}`)

// numberLiteral renders a decoded JSON number (always float64, per
// encoding/json's own default decode) into source text valid across
// Go/TypeScript/Python's own numeric literal syntax -- all three accept
// the identical plain-integer and 'g'-format-float text this produces,
// so this ONE implementation (unlike param/ref/string rendering, which
// need real per-language syntax) is genuinely reusable rather than
// reimplemented three times.
func numberLiteral(t float64) string {
	if t == math.Trunc(t) && !math.IsInf(t, 0) {
		return strconv.FormatInt(int64(t), 10)
	}
	return strconv.FormatFloat(t, 'g', -1, 64)
}

// jsonStringLiteral renders s as JSON-quoted text -- valid TypeScript AND
// Python double-quoted string-literal syntax as-is (both accept the same
// backslash/unicode escape rules JSON itself uses), so gogen.go's own
// Go-specific %q keeps its own separate, already-proven implementation
// (Go's own quoting rules aren't always byte-identical to JSON's), but
// TS/Python codegen share this one rather than each reimplementing
// string-escaping from scratch.
func jsonStringLiteral(s string) string {
	raw, _ := json.Marshal(s) // a Go string can always be JSON-marshaled
	return string(raw)
}
