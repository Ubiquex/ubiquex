package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/blueprint/spec"
	"github.com/ubiquex/ubiquex/tseval"
)

// extractts.go derives a Schema from a TypeScript blueprint's own
// source (docs/blueprint.md, "Blueprint schema"), via `deno doc --json`
// (tseval/declarations.go). It reads declarations only: no execution.
//
// The convention is the same one sentence the Go extractor enforces,
// spelled in TypeScript: a property with no `?` is required, a property
// with `?` is optional. Everything below is the machinery for refusing
// clearly when source does not follow it.
//
// One thing is genuinely better here than in Go. Deno reports where
// each type reference RESOLVED FROM, so a CrossMarker imported from
// @ubx/sdk is distinguishable from a local interface that happens to
// share the name. The Go extractor cannot tell those apart, because it
// matches on source spelling to avoid type-checking. So the TypeScript
// extractor checks the provenance of its marker types and the Go one
// does not, which is a difference in strictness, not in vocabulary:
// both accept exactly the same set of well-formed blueprints.

// tsKeywordVocabulary maps a TypeScript keyword type to this project's
// own language-neutral param vocabulary.
var tsKeywordVocabulary = map[string]spec.ParamType{
	"string":  spec.ParamString,
	"number":  spec.ParamNumber,
	"boolean": spec.ParamBool,
}

// tsListVocabulary maps the ELEMENT type of an array to the matching
// list param type. A list of anything else is not a param type.
var tsListVocabulary = map[string]spec.ParamType{
	"string": spec.ParamListString,
	"number": spec.ParamListNumber,
}

// sdkSpecifier is the module a blueprint's marker types must come from.
// The published provider SDKs re-export nothing from it, so a
// CrossMarker or Computed that resolved anywhere else is a different
// type wearing the same name.
const sdkSpecifier = "@ubx/sdk"

// denoDoc is the subset of `deno doc --json`'s own output this reads.
// Named after Deno's shape rather than ubx's because that is exactly
// what it is: a decoder for another tool's wire format, nothing more.
type denoDoc struct {
	Nodes map[string]denoNode `json:"nodes"`
}

type denoNode struct {
	Symbols []denoSymbol `json:"symbols"`
}

type denoSymbol struct {
	Name         string            `json:"name"`
	Declarations []denoDeclaration `json:"declarations"`
}

type denoDeclaration struct {
	Location denoLocation `json:"location"`
	Kind     string       `json:"kind"`
	Def      denoDef      `json:"def"`
}

type denoLocation struct {
	Filename string `json:"filename"`
}

type denoDef struct {
	Properties []denoProperty `json:"properties"`
	Params     []denoParam    `json:"params"`
	ReturnType *denoType      `json:"returnType"`
}

type denoProperty struct {
	Name     string    `json:"name"`
	Optional bool      `json:"optional"`
	TSType   *denoType `json:"tsType"`
}

type denoParam struct {
	Name   string    `json:"name"`
	TSType *denoType `json:"tsType"`
}

// denoType is one type expression. Kind selects which of the other
// fields carries the meaning: "keyword" uses Repr, "array" uses
// ArrayValue, "typeRef" uses TypeRef.
type denoType struct {
	Repr       string       `json:"repr"`
	Kind       string       `json:"kind"`
	ArrayValue *denoType    `json:"-"`
	TypeRef    *denoTypeRef `json:"-"`
}

type denoTypeRef struct {
	TypeName   string `json:"typeName"`
	Resolution struct {
		Kind      string `json:"kind"`
		Specifier string `json:"specifier"`
		Name      string `json:"name"`
	} `json:"resolution"`
}

// UnmarshalJSON decodes denoType's own polymorphic "value" field, which
// holds a nested type for an array and a type reference for a typeRef.
func (t *denoType) UnmarshalJSON(data []byte) error {
	var raw struct {
		Repr  string          `json:"repr"`
		Kind  string          `json:"kind"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t.Repr, t.Kind = raw.Repr, raw.Kind
	switch raw.Kind {
	case "array":
		var elem denoType
		if err := json.Unmarshal(raw.Value, &elem); err != nil {
			return err
		}
		t.ArrayValue = &elem
	case "typeRef":
		var ref denoTypeRef
		if err := json.Unmarshal(raw.Value, &ref); err != nil {
			return err
		}
		t.TypeRef = &ref
	}
	return nil
}

// ExtractTS derives the schema for the TypeScript blueprint in dir.
func ExtractTS(ctx context.Context, dir, name string) (*Schema, error) {
	files, err := tsSourceFiles(dir)
	if err != nil {
		return nil, err
	}

	raw, err := tseval.Declarations(ctx, files...)
	if err != nil {
		return nil, fmt.Errorf("blueprint: extract %s: %w", dir, err)
	}
	var doc denoDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("blueprint: extract %s: %w", dir, err)
	}

	interfaces, err := tsInterfaces(doc, dir)
	if err != nil {
		return nil, err
	}

	fnName, fn, fnFile, err := tsEntrypoint(doc, dir)
	if err != nil {
		return nil, err
	}

	configName, err := tsSoleParamType(fnName, fn, dir)
	if err != nil {
		return nil, err
	}
	configIface, ok := interfaces[configName]
	if !ok {
		return nil, fmt.Errorf("blueprint: extract %s: %s takes %s, which is not an exported interface in this blueprint -- a blueprint takes one exported Config interface", dir, fnName, configName)
	}
	params, err := tsParams(configIface, configName, dir)
	if err != nil {
		return nil, err
	}

	outputsName, outputs, err := tsOutputs(fnName, fn, interfaces, dir)
	if err != nil {
		return nil, err
	}

	entry, err := filepath.Rel(dir, fnFile)
	if err != nil {
		return nil, fmt.Errorf("blueprint: extract %s: %w", dir, err)
	}

	assumptions := []string{}
	for _, p := range params {
		if p.Type == spec.ParamNumber || p.Type == spec.ParamListNumber {
			assumptions = append(assumptions, TSNumberIsIntAssumption)
			break
		}
	}

	return &Schema{
		SchemaVersion: SchemaVersion,
		Name:          name,
		Entrypoint: Entrypoint{
			Language:    "ts",
			TSEntry:     filepath.ToSlash(entry),
			Function:    fnName,
			ConfigType:  configName,
			OutputsType: outputsName,
		},
		Params:     params,
		Outputs:    outputs,
		Defaults:   DefaultsNotDerivable,
		Derivation: Derivation{Assumptions: assumptions},
	}, nil
}

// tsSourceFiles lists the .ts files a blueprint's own signature could
// be declared in, sorted so extraction is deterministic.
//
// Test files are skipped, matching the Go extractor. Declaration files
// are skipped too: a .d.ts describes something else's types and
// declaring the blueprint in one would mean the implementation lives
// somewhere this never reads.
func tsSourceFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("blueprint: extract %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".ts") || strings.HasSuffix(n, ".d.ts") {
			continue
		}
		if strings.HasSuffix(n, ".test.ts") || strings.HasSuffix(n, "_test.ts") {
			continue
		}
		files = append(files, filepath.Join(dir, n))
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("blueprint: extract %s: no .ts files here", dir)
	}
	sort.Strings(files)
	return files, nil
}

// tsInterfaces indexes every exported interface by name, refusing a name
// declared twice. Two files declaring the same interface name is legal
// TypeScript and ambiguous here, since a type reference is matched by
// name.
func tsInterfaces(doc denoDoc, dir string) (map[string]denoDef, error) {
	out := map[string]denoDef{}
	seen := map[string]string{}
	for _, node := range doc.Nodes {
		for _, sym := range node.Symbols {
			for _, dec := range sym.Declarations {
				if dec.Kind != "interface" {
					continue
				}
				file, err := fileFromURL(dec.Location.Filename)
				if err != nil {
					return nil, fmt.Errorf("blueprint: extract %s: %w", dir, err)
				}
				if other, dup := seen[sym.Name]; dup {
					names := []string{other, file}
					sort.Strings(names)
					return nil, fmt.Errorf("blueprint: extract %s: the interface %s is declared in both %s and %s -- a type is matched by name here, so rename one", dir, sym.Name, filepath.Base(names[0]), filepath.Base(names[1]))
				}
				seen[sym.Name] = file
				out[sym.Name] = dec.Def
			}
		}
	}
	return out, nil
}

// tsEntrypoint returns the one exported function that could be the
// blueprint, and the file it is declared in.
func tsEntrypoint(doc denoDoc, dir string) (string, denoDef, string, error) {
	type candidate struct {
		name string
		def  denoDef
		file string
	}
	var candidates []candidate
	for _, node := range doc.Nodes {
		for _, sym := range node.Symbols {
			for _, dec := range sym.Declarations {
				if dec.Kind != "function" || len(dec.Def.Params) != 1 {
					continue
				}
				file, err := fileFromURL(dec.Location.Filename)
				if err != nil {
					return "", denoDef{}, "", fmt.Errorf("blueprint: extract %s: %w", dir, err)
				}
				candidates = append(candidates, candidate{sym.Name, dec.Def, file})
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].name < candidates[j].name })
	switch len(candidates) {
	case 1:
		return candidates[0].name, candidates[0].def, candidates[0].file, nil
	case 0:
		return "", denoDef{}, "", fmt.Errorf("blueprint: extract %s: no exported function taking exactly one parameter -- a blueprint is one exported function taking one Config interface", dir)
	default:
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.name
		}
		return "", denoDef{}, "", fmt.Errorf("blueprint: extract %s: %d exported functions take one parameter (%s) -- exactly one is the blueprint, so stop exporting the others or move them", dir, len(candidates), strings.Join(names, ", "))
	}
}

// tsSoleParamType returns the interface name of the entrypoint's own
// single parameter.
func tsSoleParamType(fnName string, fn denoDef, dir string) (string, error) {
	t := fn.Params[0].TSType
	if t == nil {
		return "", fmt.Errorf("blueprint: extract %s: %s's parameter has no type annotation -- a blueprint takes one Config interface", dir, fnName)
	}
	if t.Kind != "typeRef" || t.TypeRef == nil {
		return "", fmt.Errorf("blueprint: extract %s: %s's parameter is %s, not an interface -- a blueprint takes one exported Config interface, since a caller has to name the type to construct one", dir, fnName, t.Repr)
	}
	return t.TypeRef.TypeName, nil
}

// tsParams turns the config interface's properties into schema params,
// in declaration order.
func tsParams(iface denoDef, typeName, dir string) ([]SchemaParam, error) {
	var params []SchemaParam
	seen := map[string]string{}
	for _, prop := range iface.Properties {
		pt, err := tsParamType(prop.TSType)
		if err != nil {
			return nil, fmt.Errorf("blueprint: extract %s: %s.%s %s", dir, typeName, prop.Name, err)
		}
		wire := snakeCase(prop.Name)
		if other, dup := seen[wire]; dup {
			return nil, fmt.Errorf("blueprint: extract %s: %s.%s and %s.%s both become the param name %q -- rename one", dir, typeName, other, typeName, prop.Name, wire)
		}
		seen[wire] = prop.Name
		params = append(params, SchemaParam{
			Name:       wire,
			SourceName: prop.Name,
			Type:       pt,
			Required:   !prop.Optional,
		})
	}
	return params, nil
}

// tsParamType maps one TypeScript type onto the param vocabulary.
func tsParamType(t *denoType) (spec.ParamType, error) {
	const fix = "use one of string, number, boolean, string[], number[], CrossMarker, with ? for an optional param"
	if t == nil {
		return "", fmt.Errorf("has no type annotation -- %s", fix)
	}
	switch t.Kind {
	case "keyword":
		if pt, ok := tsKeywordVocabulary[t.Repr]; ok {
			return pt, nil
		}
	case "array":
		if t.ArrayValue != nil && t.ArrayValue.Kind == "keyword" {
			if pt, ok := tsListVocabulary[t.ArrayValue.Repr]; ok {
				return pt, nil
			}
		}
	case "typeRef":
		if t.TypeRef != nil && t.TypeRef.TypeName == "CrossMarker" {
			if t.TypeRef.Resolution.Specifier != sdkSpecifier {
				return "", fmt.Errorf("is a CrossMarker from %q, not from %q -- a cross-stack reference has to be the SDK's own marker type, since that is what the evaluator recognizes", t.TypeRef.Resolution.Specifier, sdkSpecifier)
			}
			return spec.ParamCrossRef, nil
		}
	}
	return "", fmt.Errorf("is %s, which is not a param type -- %s", t.Repr, fix)
}

// tsOutputs reads the entrypoint's return type. A blueprint returning
// void, or nothing annotated, is legal and has no outputs.
func tsOutputs(fnName string, fn denoDef, interfaces map[string]denoDef, dir string) (string, []SchemaOutput, error) {
	rt := fn.ReturnType
	if rt == nil || (rt.Kind == "keyword" && (rt.Repr == "void" || rt.Repr == "undefined")) {
		return "", []SchemaOutput{}, nil
	}
	if rt.Kind != "typeRef" || rt.TypeRef == nil {
		return "", nil, fmt.Errorf("blueprint: extract %s: %s returns %s -- a blueprint returns one exported Outputs interface, or void", dir, fnName, rt.Repr)
	}
	name := rt.TypeRef.TypeName
	iface, ok := interfaces[name]
	if !ok {
		return "", nil, fmt.Errorf("blueprint: extract %s: %s returns %s, which is not an exported interface in this blueprint", dir, fnName, name)
	}

	var outputs []SchemaOutput
	seen := map[string]string{}
	for _, prop := range iface.Properties {
		t := prop.TSType
		if t == nil || t.Kind != "typeRef" || t.TypeRef == nil || t.TypeRef.TypeName != "Computed" {
			repr := "nothing"
			if t != nil {
				repr = t.Repr
			}
			return "", nil, fmt.Errorf("blueprint: extract %s: %s.%s is %s -- every output is a Computed, since an output is always a reference to a resource attribute", dir, name, prop.Name, repr)
		}
		if t.TypeRef.Resolution.Specifier != sdkSpecifier {
			return "", nil, fmt.Errorf("blueprint: extract %s: %s.%s is a Computed from %q, not from %q -- an output has to be the SDK's own type, since that is what the evaluator recognizes", dir, name, prop.Name, t.TypeRef.Resolution.Specifier, sdkSpecifier)
		}
		if prop.Optional {
			return "", nil, fmt.Errorf("blueprint: extract %s: %s.%s is optional -- an output is always present, since it is a reference to an attribute the blueprint's own resources produce", dir, name, prop.Name)
		}
		wire := snakeCase(prop.Name)
		if other, dup := seen[wire]; dup {
			return "", nil, fmt.Errorf("blueprint: extract %s: %s.%s and %s.%s both become the output name %q -- rename one", dir, name, other, name, prop.Name, wire)
		}
		seen[wire] = prop.Name
		outputs = append(outputs, SchemaOutput{Name: wire, SourceName: prop.Name})
	}
	return name, outputs, nil
}

// fileFromURL turns deno doc's own file:// location back into a host
// path.
func fileFromURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse declaration location %q: %w", raw, err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("declaration location %q is not a local file -- a blueprint is declared in its own directory", raw)
	}
	return u.Path, nil
}
