// Package blueprint is UBI-74 Slice 1's own home: parsing an Ubxfile and
// compiling its resolved intent draft into real Go SDK source
// (docs/blueprint.md has the full design). Named after the CLI verb it
// implements one-to-one (`ubx blueprint build`), matching this project's
// own package-naming convention (CLAUDE.md) -- writeback/ implements
// `ubx writeback` the same way.
package blueprint

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// UbxfileName is the literal filename `ubx blueprint build .` looks for
// in the given directory -- no prefix/suffix, capitalized, matching
// Docker's own Dockerfile convention (docs/blueprint.md).
const UbxfileName = "Ubxfile"

// ParamType is one of Ubxfile's own recognized params: types.
type ParamType string

const (
	ParamString ParamType = "string"
	ParamNumber ParamType = "number"
	ParamBool   ParamType = "bool"

	// ParamListString/ParamListNumber (UBI-129) are the two list-typed
	// params: types -- "list(<element>)", the literal spelling
	// parseParamSpec matches, matching this file's own "type is an
	// opaque, exact-string literal, never parsed into parts" convention
	// (ParamString/ParamNumber/ParamBool are handled identically). A
	// list-typed param can ONLY be declared "required" -- see
	// parseDefaultValue's own explicit refusal below -- since it's
	// always consumed by exactly one for_each resource (docs/blueprint.md's
	// own "List-typed parameters + iteration" section), never by a
	// functional-options-style optional value. list(bool) is deliberately
	// not added: no real worked example in this ticket's own design
	// record needs one, matching this file's own established "extend
	// when a real blueprint needs it, not speculatively" discipline
	// (ParamNumber's own "always int, no float" precedent above).
	ParamListString ParamType = "list(string)"
	ParamListNumber ParamType = "list(number)"

	// ParamCrossRef (UBI-134) is a param whose call-site value is always
	// a real "@<stack>.<type>.<name>[.<attr-path>]" cross-stack address
	// (diagram/crossref.go's own parseCrossRefLabel established this
	// exact "@" grammar first, for a diagram reference node's label --
	// reused here verbatim rather than inventing a second one), never a
	// plain string a caller could accidentally pass instead. This is
	// this project's own established "explicit typed markers, never
	// silent string-sniffing" discipline (CLAUDE.md; the same posture
	// $ref/$cross/$secret/$computed already hold to at the resolver
	// level, core/resolver/refs.go) applied one layer up, at a
	// blueprint's own declared param surface -- a param wanting a
	// cross-stack reference says so explicitly in its own type, rather
	// than every string-typed param silently being probed for an "@"
	// prefix.
	ParamCrossRef ParamType = "cross_ref"
)

// IsList reports whether t is one of the two list-typed params: types
// (UBI-129) -- a list param is consumed exclusively via a for_each
// resource's own synthetic per-element/index tokens (blueprint/decode.go),
// never as an ordinary bare {param_name} reference the way a scalar
// param is.
func (t ParamType) IsList() bool {
	return t == ParamListString || t == ParamListNumber
}

// GoType returns the Go type a param of this type compiles to in the
// generated function's own signature. ParamNumber always compiles to
// Go's int (docs/blueprint.md: "number always compiles to Go int" --
// every real example in UBI-74's own design record is an integer count,
// float support is deliberately not invented ahead of a real need).
func (t ParamType) GoType() string {
	switch t {
	case ParamString:
		return "string"
	case ParamNumber:
		return "int"
	case ParamBool:
		return "bool"
	case ParamListString:
		return "[]string"
	case ParamListNumber:
		return "[]int"
	// sdk.CrossMarker (github.com/ubiquex/ubx-sdk-go/runtime), a real,
	// already-exported concrete type -- generated Go code already
	// imports this package unconditionally (invoke.go's writeGoCaller,
	// gogen.go's own header), matching the SAME "typed *sdk.Computed"
	// precedent this file's own outputs: support already established
	// for another opaque, runtime-only SDK value, rather than falling
	// back to Go's untyped "any" the way an unrecognized type does.
	case ParamCrossRef:
		return "sdk.CrossMarker"
	default:
		return "any"
	}
}

// TSType returns the TypeScript type a param of this type compiles to in
// the generated function's own signature (Slice 4). Unlike Go,
// ParamNumber -> "number" carries no int/float distinction at all --
// TypeScript has exactly one numeric type, so there's no Go-style
// "always int" decision to make here in the first place.
func (t ParamType) TSType() string {
	switch t {
	case ParamString:
		return "string"
	case ParamNumber:
		return "number"
	case ParamBool:
		return "boolean"
	case ParamListString:
		return "string[]"
	case ParamListNumber:
		return "number[]"
	// "any", matching the SAME opaque-runtime-value convention this
	// file's own outputs: support already uses for every declared
	// output ("{ repoArn: any; ... }", GenerateTS) -- TS's own cross()
	// (sdk/ts/runtime/src/index.ts) is itself generic ("T = unknown"),
	// so typing the param strictly here would force every real call
	// site to spell out an explicit cross<CrossMarker>(...) instantiation
	// for no real type-safety gain (a cross-stack reference's own real
	// value is never available at typecheck time either way).
	case ParamCrossRef:
		return "any"
	default:
		return "any"
	}
}

// PyType returns the Python type annotation a param of this type
// compiles to in the generated function's own signature (Slice 4).
// Mirrors GoType's own "number always compiles to int" decision (every
// real example in UBI-74's own design record is an integer count; float
// support is deliberately not invented ahead of a real need) -- applied
// here too, for consistency across all three generated languages rather
// than letting Python's own native float default quietly diverge.
func (t ParamType) PyType() string {
	switch t {
	case ParamString:
		return "str"
	case ParamNumber:
		return "int"
	case ParamBool:
		return "bool"
	case ParamListString:
		return "list[str]"
	case ParamListNumber:
		return "list[int]"
	// "Any", mirroring TSType's own reasoning above -- Python's own
	// cross() (sdk/py/ubx_sdk/__init__.py) is already declared -> Any,
	// the same established opaque-runtime-value convention this file's
	// own outputs: support already uses ("-> Any:", GeneratePy).
	case ParamCrossRef:
		return "Any"
	default:
		return "Any"
	}
}

// Param is one params: entry, in the Ubxfile's own declared order.
type Param struct {
	Name     string
	Type     ParamType
	Required bool
	// Default holds the parsed default value (string, int, or bool,
	// matching Type) when !Required; nil when Required.
	Default any
}

// Output is one outputs: entry (UBI-128), in the Ubxfile's own
// declaration order -- never a map, matching Params' own determinism
// discipline: a generated function's own return-value ORDER must be
// stable across builds. Target is "<resource-slug>.<attribute>",
// verbatim -- the resource slug is only checked against the blueprint's
// own real resolved resources later (blueprint.ExpandCalls, once the
// blueprint has actually been invoked and its real resources are
// known); ParseUbxfile has no resource-slug knowledge of its own to
// check against (resources: is free-form prose at this stage).
type Output struct {
	Name   string
	Target string
}

// Ubxfile is one parsed Ubxfile -- four keys (lang, params, resources,
// outputs), per docs/blueprint.md. uses: (UBI-121, nesting) is
// explicitly out of scope and rejected as an unrecognized key.
type Ubxfile struct {
	// Dir is the directory this Ubxfile was loaded from -- resources:
	// paths resolve relative to it.
	Dir string
	// Lang is the blueprint's own declared target language(s) -- one of
	// "go"/"ts"/"py"/"all" (Slice 4). Validated here (a real value from
	// this set), but NOT currently consulted by `ubx blueprint build`'s
	// own language selection -- that's governed entirely by the CLI's
	// own --lang flag (default "all" when omitted), per UBI-74's own
	// resolved "--lang default" design. Left as a real, named open point
	// rather than silently wired together with a guessed precedence
	// (docs/blueprint.md).
	Lang string
	// Params is params:, in file declaration order (never a map --
	// determinism, docs/blueprint.md).
	Params []Param
	// Resources is the resolved resource prose -- either read verbatim
	// from resources:'s own inline value, or (when resources: names an
	// existing .md file) that file's own content.
	Resources string
	// ResourcesSource is "inline" or the resolved .md file path,
	// recorded for provenance/logging only.
	ResourcesSource string
	// Outputs is outputs: (UBI-128), in file declaration order -- empty
	// for a blueprint that declares none, the overwhelming common case
	// until this ticket, and completely unaffected by it (every codegen
	// path stays byte-identical to before Outputs existed when this is
	// empty).
	Outputs []Output
}

// rawUbxfile is the strict-decode target -- KnownFields(true) rejects
// any key besides these four, which is what makes uses: (UBI-121) a
// loud, immediate parse error rather than a silently-ignored key.
// Params/Outputs are captured as raw yaml.Node, not map[string]string,
// specifically to preserve declaration order (a Go map has none) --
// ParseUbxfile walks their own .Content pairs directly.
type rawUbxfile struct {
	Lang      string    `yaml:"lang"`
	Params    yaml.Node `yaml:"params"`
	Resources string    `yaml:"resources"`
	Outputs   yaml.Node `yaml:"outputs"`
}

// ParseUbxfile reads and parses the Ubxfile in dir.
func ParseUbxfile(dir string) (*Ubxfile, error) {
	path := filepath.Join(dir, UbxfileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %w", err)
	}

	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	var raw rawUbxfile
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("blueprint: %s: %w", path, err)
	}

	if strings.TrimSpace(raw.Lang) == "" {
		return nil, fmt.Errorf("blueprint: %s: lang: is required", path)
	}
	switch raw.Lang {
	case "go", "ts", "py", "all":
	default:
		return nil, fmt.Errorf("blueprint: %s: lang: %q not recognized -- want one of go, ts, py, all", path, raw.Lang)
	}

	params, err := parseParams(&raw.Params, path)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(raw.Resources) == "" {
		return nil, fmt.Errorf("blueprint: %s: resources: is required", path)
	}
	resources, source, err := resolveResources(dir, raw.Resources)
	if err != nil {
		return nil, fmt.Errorf("blueprint: %s: %w", path, err)
	}

	outputs, err := parseOutputs(&raw.Outputs, path)
	if err != nil {
		return nil, err
	}

	return &Ubxfile{
		Dir:             dir,
		Lang:            raw.Lang,
		Params:          params,
		Resources:       resources,
		ResourcesSource: source,
		Outputs:         outputs,
	}, nil
}

// parseOutputs walks node's own key/value pairs in FILE ORDER (never a
// map -- see rawUbxfile's own doc comment for why), each value a plain
// "<resource-slug>.<attribute>" scalar string. outputs: entirely absent
// is legal (node.Kind == 0) -- most blueprints declare none.
func parseOutputs(node *yaml.Node, path string) ([]Output, error) {
	if node.Kind == 0 {
		return nil, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("blueprint: %s: outputs: must be a mapping (key: value pairs)", path)
	}

	var outputs []Output
	seen := map[string]bool{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		name := keyNode.Value
		if seen[name] {
			return nil, fmt.Errorf("blueprint: %s: outputs.%s: declared more than once", path, name)
		}
		seen[name] = true
		if valNode.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf(`blueprint: %s: outputs.%s: expected a scalar "<resource-slug>.<attribute>" value, got a %s`, path, name, yamlKindName(valNode.Kind))
		}
		target := valNode.Value
		slug, attr, ok := strings.Cut(target, ".")
		if !ok || slug == "" || attr == "" {
			return nil, fmt.Errorf(`blueprint: %s: outputs.%s: %q must be "<resource-slug>.<attribute>"`, path, name, target)
		}
		outputs = append(outputs, Output{Name: name, Target: target})
	}
	return outputs, nil
}

// parseParams walks node's own key/value pairs in FILE ORDER (never a
// map) -- see rawUbxfile's own doc comment for why.
func parseParams(node *yaml.Node, path string) ([]Param, error) {
	if node.Kind == 0 {
		return nil, nil // params: entirely absent -- a blueprint with zero parameters is legal
	}
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("blueprint: %s: params: must be a mapping (key: value pairs)", path)
	}

	var params []Param
	seen := map[string]bool{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		name := keyNode.Value
		if seen[name] {
			return nil, fmt.Errorf("blueprint: %s: params.%s: declared more than once", path, name)
		}
		seen[name] = true
		if valNode.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf(`blueprint: %s: params.%s: expected a scalar spec like "string, required", got a %s`, path, name, yamlKindName(valNode.Kind))
		}
		p, err := parseParamSpec(name, valNode.Value)
		if err != nil {
			return nil, fmt.Errorf("blueprint: %s: params.%s: %w", path, name, err)
		}
		params = append(params, p)
	}
	return params, nil
}

// parseParamSpec parses one params: value, "<type>, required" or
// "<type>, default <value>".
func parseParamSpec(name, spec string) (Param, error) {
	typePart, rest, ok := strings.Cut(spec, ",")
	if !ok {
		return Param{}, fmt.Errorf(`expected "<type>, required" or "<type>, default <value>", got %q`, spec)
	}
	typ := ParamType(strings.TrimSpace(typePart))
	switch typ {
	case ParamString, ParamNumber, ParamBool, ParamListString, ParamListNumber, ParamCrossRef:
	default:
		return Param{}, fmt.Errorf("unrecognized type %q -- must be \"string\", \"number\", \"bool\", \"list(string)\", \"list(number)\", or \"cross_ref\"", typePart)
	}

	rest = strings.TrimSpace(rest)
	p := Param{Name: name, Type: typ}
	switch {
	case rest == "required":
		p.Required = true
	case strings.HasPrefix(rest, "default "):
		defaultText := strings.TrimSpace(strings.TrimPrefix(rest, "default "))
		v, err := parseDefaultValue(typ, defaultText)
		if err != nil {
			return Param{}, fmt.Errorf("default value: %w", err)
		}
		p.Default = v
	default:
		return Param{}, fmt.Errorf(`expected "required" or "default <value>" after the type, got %q`, rest)
	}
	return p, nil
}

func parseDefaultValue(typ ParamType, text string) (any, error) {
	switch typ {
	case ParamListString, ParamListNumber:
		return nil, fmt.Errorf("list-typed params don't support a default value yet -- declare it \"required\" instead (UBI-129: a list param is always consumed by exactly one for_each resource, which has no notion of an un-given default)")
	case ParamCrossRef:
		return nil, fmt.Errorf("cross_ref params don't support a default value -- declare it \"required\" instead (UBI-134: there is no sensible default cross-stack address)")
	case ParamNumber:
		n, err := strconv.Atoi(text)
		if err != nil {
			return nil, fmt.Errorf("%q is not a whole number", text)
		}
		return n, nil
	case ParamBool:
		b, err := strconv.ParseBool(text)
		if err != nil {
			return nil, fmt.Errorf("%q is not \"true\"/\"false\"", text)
		}
		return b, nil
	case ParamString:
		if len(text) < 2 || text[0] != '"' || text[len(text)-1] != '"' {
			return nil, fmt.Errorf("a string default must be double-quoted, e.g. default \"foo\" -- got %q", text)
		}
		return text[1 : len(text)-1], nil
	default:
		return nil, fmt.Errorf("unrecognized type %q", typ)
	}
}

// resolveResources disambiguates resources:'s own value -- a path to an
// existing .md file, or literal inline prose -- the same way a human
// reading the Ubxfile would: a single-line value ending in .md that
// actually resolves to a real file is a path; anything else is prose,
// verbatim (docs/blueprint.md).
func resolveResources(dir, value string) (resources, source string, err error) {
	trimmed := strings.TrimSpace(value)
	if !strings.Contains(trimmed, "\n") && strings.HasSuffix(trimmed, ".md") {
		candidate := trimmed
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(dir, candidate)
		}
		if data, err := os.ReadFile(candidate); err == nil {
			return string(data), candidate, nil
		}
	}
	return value, "inline", nil
}

func yamlKindName(k yaml.Kind) string {
	switch k {
	case yaml.SequenceNode:
		return "a list"
	case yaml.ScalarNode:
		return "a scalar"
	case yaml.AliasNode:
		return "an alias"
	case yaml.DocumentNode:
		return "a document"
	default:
		return "an unrecognized node"
	}
}
