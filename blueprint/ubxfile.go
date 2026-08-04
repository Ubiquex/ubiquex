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
)

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
	default:
		return "any"
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

// Ubxfile is one parsed Ubxfile -- three keys only (lang, params,
// resources), per docs/blueprint.md. uses: (UBI-121, nesting) is
// explicitly out of scope and rejected as an unrecognized key.
type Ubxfile struct {
	// Dir is the directory this Ubxfile was loaded from -- resources:
	// paths resolve relative to it.
	Dir string
	// Lang is the target language. Slice 1 only accepts "go".
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
}

// rawUbxfile is the strict-decode target -- KnownFields(true) rejects
// any key besides these three, which is what makes uses: (UBI-121) a
// loud, immediate parse error rather than a silently-ignored key.
// Params is captured as a raw yaml.Node, not map[string]string,
// specifically to preserve declaration order (a Go map has none) --
// ParseUbxfile walks its .Content pairs directly.
type rawUbxfile struct {
	Lang      string    `yaml:"lang"`
	Params    yaml.Node `yaml:"params"`
	Resources string    `yaml:"resources"`
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
	if raw.Lang != "go" {
		return nil, fmt.Errorf("blueprint: %s: lang: %q is not supported this slice -- only \"go\" is built by UBI-74 Slice 1 (--lang all/ts/py is Slice 4's own scope)", path, raw.Lang)
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

	return &Ubxfile{
		Dir:             dir,
		Lang:            raw.Lang,
		Params:          params,
		Resources:       resources,
		ResourcesSource: source,
	}, nil
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
	case ParamString, ParamNumber, ParamBool:
	default:
		return Param{}, fmt.Errorf("unrecognized type %q -- must be \"string\", \"number\", or \"bool\"", typePart)
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
