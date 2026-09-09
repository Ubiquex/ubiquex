// Package blueprint is UBI-74 Slice 1's own home: parsing an Ubxfile and
// compiling its resolved intent draft into real Go SDK source
// (docs/blueprint.md has the full design). Named after the CLI verb it
// implements one-to-one (`ubx blueprint build`), matching this project's
// own package-naming convention (CLAUDE.md) -- writeback/ implements
// `ubx writeback` the same way.
package blueprint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ubiquex/ubiquex/blueprint/spec"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// UbxfileName is the literal filename `ubx blueprint build .` looks for
// in the given directory -- no prefix/suffix, capitalized, matching
// Docker's own Dockerfile convention (docs/blueprint.md).
const UbxfileName = "Ubxfile"

// The params:/outputs: value types live in blueprint/spec so a consumer
// needing only them does not import this package's own evaluators and
// embedded runtimes with them. Aliased rather than re-declared: these ARE
// spec's types, so every existing blueprint.Param call site is unchanged
// and the two can never drift.
type (
	ParamType = spec.ParamType
	Param     = spec.Param
	Output    = spec.Output
)

const (
	ParamString     = spec.ParamString
	ParamNumber     = spec.ParamNumber
	ParamBool       = spec.ParamBool
	ParamListString = spec.ParamListString
	ParamListNumber = spec.ParamListNumber
	ParamCrossRef   = spec.ParamCrossRef
)

// requiredFirstOrder returns params reordered so every required param
// comes before every defaulted param, each group keeping its own
// relative declaration order -- the one grouping BOTH TypeScript's and
// Go's own generated call conventions depend on:
//   - TypeScript's generated function signature (renderTSFunction,
//     tsgen.go) is always required-first, defaulted-trailing --
//     TypeScript itself enforces "a required parameter cannot follow an
//     optional one," so a purely positional call needs its own
//     arguments in this exact order too, regardless of Ubxfile
//     declaration order.
//   - Go's generated function signature (renderGoFunction, gogen.go) is
//     required params positional, THEN a trailing "opts ...Option" for
//     every defaulted one -- so a caller passing a with*() option
//     BETWEEN two required positional arguments (raw declaration order,
//     interleaved) produces a real compile error, every required
//     positional argument has to be grouped contiguously first.
//
// UBI-149: shared by renderTSFunction (the TS signature itself),
// writeTSCaller (invoke.go, the synthesized diagram/md TS caller's own
// emitted argument list), AND writeGoCaller (invoke.go, the synthesized
// diagram/md Go caller) -- three previously-independent implementations
// of "what order do the params go in," two of which (both callers)
// silently assumed raw declaration order was already safe. A required
// param declared after a defaulted one broke BOTH: TS threaded the
// wrong VALUE into the wrong slot at runtime (never caught by Deno's
// looser typing), Go failed to even compile (a real, stricter, and
// coincidentally louder failure mode for the identical root cause).
// Neither ever affected a direct SDK import (the human wrote that call
// against the real, already-correctly-shaped signature directly), and
// Python's own native kwargs (writePyCaller) were never order-dependent
// in the first place. One shared function, used by every path that
// needs this grouping, per this codebase's own UBI-142 precedent
// (configFieldLine) for closing exactly this class of two-(or
// three-)code-paths drift -- not just patching one symptom in
// isolation.
func requiredFirstOrder(params []Param) []Param {
	ordered := make([]Param, 0, len(params))
	for _, p := range params {
		if p.Required {
			ordered = append(ordered, p)
		}
	}
	for _, p := range params {
		if !p.Required {
			ordered = append(ordered, p)
		}
	}
	return ordered
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
	// Resources is a pre-resolved intent/v1 JSON document (the SAME
	// wire shape "ubx resolve --from-code --out <file>" already
	// produces, unmarshalable directly into resolver.IntentFile) --
	// either read verbatim from resources:'s own inline value, or (when
	// resources: names an existing .json file) that file's own content.
	// UBI-224 removed blueprint build's own intent-provider draft step:
	// a blueprint author now produces this JSON themselves, via the SDK
	// (or "ubx blueprint convert"), before it's ever checked in --
	// build has nothing left to interpret, only to parse.
	Resources string
	// ResourcesSource is "inline" or the resolved .json file path,
	// recorded for provenance/logging only.
	ResourcesSource string
	// Outputs is outputs: (UBI-128), in file declaration order -- empty
	// for a blueprint that declares none, the overwhelming common case
	// until this ticket, and completely unaffected by it (every codegen
	// path stays byte-identical to before Outputs existed when this is
	// empty).
	Outputs []Output
	// SDK is the sdk: block (opt-in): import a published per-provider
	// SDK instead of emitting a bindings file. Nil for every blueprint
	// that omits it, which is every blueprint written before this
	// existed, and those build byte-identically to before.
	SDK *SDKSpec
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
	SDK       *rawSDK   `yaml:"sdk"`
}

// rawSDK is the sdk: block's own strict-decode target.
type rawSDK struct {
	Provider string `yaml:"provider"`
	Go       string `yaml:"go"`
	TS       string `yaml:"ts"`
	Py       string `yaml:"py"`
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

	sdkSpec, err := parseSDKBlock(raw.SDK, path)
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
		SDK:             sdkSpec,
	}, nil
}

// parseSDKBlock validates the optional sdk: block. Absent is the common
// case and yields nil, which every generator treats as "emit bindings",
// exactly as before this existed.
func parseSDKBlock(raw *rawSDK, path string) (*SDKSpec, error) {
	if raw == nil {
		return nil, nil
	}
	if raw.Provider == "" {
		return nil, fmt.Errorf("blueprint: %s: sdk: requires a provider: key naming the snapshot to resolve service packages from, e.g. \"ubiquex/aws@4.0.0\" -- without it the package a resource type lives in cannot be known", path)
	}
	if _, _, _, err := parseSDKProvider(raw.Provider); err != nil {
		return nil, fmt.Errorf("blueprint: %s: %w", path, err)
	}
	if raw.Go == "" && raw.TS == "" && raw.Py == "" {
		return nil, fmt.Errorf("blueprint: %s: sdk: names a provider but no package for any language -- set at least one of go:/ts:/py:, or drop the block to emit bindings instead", path)
	}
	return &SDKSpec{Provider: raw.Provider, Go: raw.Go, TS: raw.TS, Py: raw.Py}, nil
}

// Validate is the one shared front half every blueprint entry point
// uses -- `ubx blueprint build`'s own CLI RunE, and the MCP
// validate_ubxfile/build_blueprint tools (UBI-223): parse the Ubxfile,
// unmarshal resources: into a resolver.IntentFile, and run it through
// decodeBlueprint -- the SAME language-neutral check GenerateGo/
// GenerateTS/GeneratePython already perform internally before their own
// codegen. A caller that only needs to know "is this valid" never has
// to run codegen to find out, and this check has exactly one
// implementation, never one kept separately in sync per caller -- the
// shape UBI-197 and UBI-233 both hit.
func Validate(dir string) (*Ubxfile, *resolver.IntentFile, error) {
	ubxfile, err := ParseUbxfile(dir)
	if err != nil {
		return nil, nil, err
	}
	var draft resolver.IntentFile
	if err := json.Unmarshal([]byte(ubxfile.Resources), &draft); err != nil {
		// Content that does not even begin as a JSON object gets a
		// different message from content that is JSON and wrong: the
		// first is usually prose where a document belongs, and saying so
		// beats reporting the character offset where the parser stopped.
		if !looksLikeJSONObject(ubxfile.Resources) {
			return nil, nil, fmt.Errorf("blueprint: resources: is not a pre-resolved intent/v1 document (%s): it does not even begin as a JSON object. "+
				"resources: must be the intent/v1 JSON that `ubx resolve --out` produces, either inline or as a path to a .json file. Underlying parse error: %w",
				ubxfile.ResourcesSource, err)
		}
		return nil, nil, fmt.Errorf("blueprint: resources: is not a valid pre-resolved intent/v1 document (%s): %w", ubxfile.ResourcesSource, err)
	}
	if _, err := decodeBlueprint(&draft, ubxfile.Params, ubxfile.Outputs); err != nil {
		return nil, nil, err
	}
	return ubxfile, &draft, nil
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
// existing .json file (a pre-resolved intent/v1 document), or literal
// inline JSON -- the same way a human reading the Ubxfile would: a
// single-line value ending in .json that actually resolves to a real
// file is a path; anything else is inline JSON, verbatim
// (docs/blueprint.md).
func resolveResources(dir, value string) (resources, source string, err error) {
	trimmed := strings.TrimSpace(value)
	if !strings.Contains(trimmed, "\n") && strings.HasSuffix(trimmed, ".json") {
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

// looksLikeJSONObject reports whether s begins as a JSON object once
// whitespace is removed. Used only to choose between two error messages,
// never to decide whether to parse: the parse itself is always what
// decides, so a false answer here can make an error less specific and can
// never make a valid blueprint fail.
func looksLikeJSONObject(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "{")
}
