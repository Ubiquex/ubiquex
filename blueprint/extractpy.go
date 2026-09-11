package blueprint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/blueprint/spec"
	"github.com/ubiquex/ubiquex/pyeval"
)

// extractpy.go derives a Schema from a Python blueprint's own source
// (docs/blueprint.md, "Blueprint schema"), via CPython's own ast module
// (pyeval/declarations.go). It reads declarations only: ast.parse
// compiles to a tree and stops, so nothing in the blueprint runs and
// nothing it imports is resolved.
//
// The convention is the same sentence again, spelled in Python:
// Optional[X] (or X | None) is optional, a bare annotation is required.
//
// Optionality is the one place Python offers two signals where Go and
// TypeScript offer one. A dataclass field can be Optional, and it can
// separately have a default, and the two are independent:
// `enabled: bool = True` has a default and is not Optional. Only
// Optional counts here, because it is the exact analogue of Go's
// pointer and TypeScript's `?`, and because a schema has no way to
// carry the default value anyway (see Defaults). An author who writes a
// default without Optional is refused rather than silently reported as
// required, since the two readings differ for every caller downstream.

// pyTypeVocabulary maps a Python annotation, as ast.unparse spells it,
// to this project's own language-neutral param vocabulary.
//
// Matched on the normalized spelling rather than a resolved type.
// Python has no declaration-time type resolution to appeal to: an
// annotation is an expression, and knowing what `sdk.CrossMarker` names
// would mean importing the module, which would mean running it.
var pyTypeVocabulary = map[string]spec.ParamType{
	"str":                 spec.ParamString,
	"int":                 spec.ParamNumber,
	"bool":                spec.ParamBool,
	"list[str]":           spec.ParamListString,
	"list[int]":           spec.ParamListNumber,
	"List[str]":           spec.ParamListString,
	"List[int]":           spec.ParamListNumber,
	"sdk.CrossMarker":     spec.ParamCrossRef,
	"ubx_sdk.CrossMarker": spec.ParamCrossRef,
	"CrossMarker":         spec.ParamCrossRef,
}

// pyComputedSpellings is every way an output's own type can be written.
// A blueprint's outputs are all Computed, so this is a set membership
// test rather than a vocabulary lookup.
var pyComputedSpellings = map[string]bool{
	"sdk.Computed":     true,
	"ubx_sdk.Computed": true,
	"Computed":         true,
}

// ExtractPy derives the schema for the Python blueprint in dir.
func ExtractPy(ctx context.Context, dir, name string) (*Schema, error) {
	files, err := pySourceFiles(dir)
	if err != nil {
		return nil, err
	}

	type found struct {
		file string
		fn   pyeval.PyFunction
		mod  *pyeval.PyModule
	}
	var candidates []found
	mods := map[string]*pyeval.PyModule{}
	for _, file := range files {
		mod, err := pyeval.Declarations(ctx, file)
		if err != nil {
			return nil, fmt.Errorf("blueprint: extract %s: %w", dir, err)
		}
		mods[file] = mod
		for _, fn := range mod.Functions {
			if strings.HasPrefix(fn.Name, "_") || len(fn.Params) != 1 || fn.HasVarargs {
				continue
			}
			candidates = append(candidates, found{file, fn, mod})
		}
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].fn.Name < candidates[j].fn.Name })
	switch len(candidates) {
	case 1:
	case 0:
		return nil, fmt.Errorf("blueprint: extract %s: no public module-level function taking exactly one parameter -- a blueprint is one function taking one Config dataclass", dir)
	default:
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.fn.Name
		}
		return nil, fmt.Errorf("blueprint: extract %s: %d public functions take one parameter (%s) -- exactly one is the blueprint, so prefix the others with _ or move them", dir, len(candidates), strings.Join(names, ", "))
	}
	entry := candidates[0]
	fn := entry.fn

	if fn.IsAsync {
		return nil, fmt.Errorf("blueprint: extract %s: %s is async -- a blueprint is an ordinary function, since the evaluator calls it directly and never awaits", dir, fn.Name)
	}

	classes, err := pyClasses(mods, dir)
	if err != nil {
		return nil, err
	}

	if fn.Params[0].Annotation == nil {
		return nil, fmt.Errorf("blueprint: extract %s: %s's parameter %s has no annotation -- a blueprint takes one Config dataclass, and the annotation is how its type is known", dir, fn.Name, fn.Params[0].Name)
	}
	configName := *fn.Params[0].Annotation
	configClass, ok := classes[configName]
	if !ok {
		return nil, fmt.Errorf("blueprint: extract %s: %s takes %s, which is not a dataclass declared in this blueprint", dir, fn.Name, configName)
	}
	params, err := pyParams(configClass, configName, dir)
	if err != nil {
		return nil, err
	}

	outputsName, outputs, err := pyOutputs(fn, classes, dir)
	if err != nil {
		return nil, err
	}

	return &Schema{
		SchemaVersion: SchemaVersion,
		Name:          name,
		Entrypoint: Entrypoint{
			Language:    "py",
			PyModule:    strings.TrimSuffix(filepath.Base(entry.file), ".py"),
			Function:    fn.Name,
			ConfigType:  configName,
			OutputsType: outputsName,
		},
		Params:     params,
		Outputs:    outputs,
		Defaults:   DefaultsNotDerivable,
		Derivation: Derivation{Assumptions: []string{}},
	}, nil
}

// pySourceFiles lists the .py files a blueprint's own signature could be
// declared in, sorted so extraction is deterministic.
func pySourceFiles(dir string) ([]string, error) {
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
		if !strings.HasSuffix(n, ".py") || strings.HasPrefix(n, "test_") || strings.HasSuffix(n, "_test.py") {
			continue
		}
		files = append(files, filepath.Join(dir, n))
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("blueprint: extract %s: no .py files here", dir)
	}
	sort.Strings(files)
	return files, nil
}

// pyClasses indexes every dataclass by name across the blueprint's own
// modules, refusing a name declared twice: an annotation is matched by
// name, so two would be ambiguous.
func pyClasses(mods map[string]*pyeval.PyModule, dir string) (map[string]pyeval.PyClass, error) {
	out := map[string]pyeval.PyClass{}
	seen := map[string]string{}
	files := make([]string, 0, len(mods))
	for f := range mods {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, file := range files {
		for _, cls := range mods[file].Classes {
			if other, dup := seen[cls.Name]; dup {
				return nil, fmt.Errorf("blueprint: extract %s: the class %s is declared in both %s and %s -- a type is matched by name here, so rename one", dir, cls.Name, filepath.Base(other), filepath.Base(file))
			}
			seen[cls.Name] = file
			out[cls.Name] = cls
		}
	}
	return out, nil
}

// pyParams turns the config dataclass's fields into schema params, in
// declaration order.
func pyParams(cls pyeval.PyClass, typeName, dir string) ([]SchemaParam, error) {
	if !hasDataclassDecorator(cls) {
		return nil, fmt.Errorf("blueprint: extract %s: %s is not a @dataclass -- a blueprint's Config is a dataclass, so that a caller can construct one by keyword and the evaluator can read its fields", dir, typeName)
	}
	var params []SchemaParam
	seen := map[string]string{}
	// Python's own constraint, which Go and TypeScript do not have: a
	// dataclass field with no default may not follow one with a default.
	// Tracked here so the refusal comes from extraction, where the
	// message can name it, rather than from a TypeError the first time
	// anything imports the module.
	var firstOptional string
	for _, field := range cls.Fields {
		if field.Annotation == nil {
			return nil, fmt.Errorf("blueprint: extract %s: %s.%s has no annotation -- every param is an annotated field, since the annotation is its type", dir, typeName, field.Name)
		}
		inner, optional := pyStripOptional(*field.Annotation)
		pt, ok := pyTypeVocabulary[inner]
		if !ok {
			return nil, fmt.Errorf("blueprint: extract %s: %s.%s is %s, which is not a param type -- use one of str, int, bool, list[str], list[int], sdk.CrossMarker, wrapped in Optional[...] for an optional param", dir, typeName, field.Name, *field.Annotation)
		}
		// The two signals Python offers have to agree, or the schema
		// would report one reading while the function implements the
		// other.
		if optional && !field.HasDefault {
			return nil, fmt.Errorf("blueprint: extract %s: %s.%s is %s but has no default -- an optional param needs `= None`, or a caller has to pass it anyway and it is not optional", dir, typeName, field.Name, *field.Annotation)
		}
		if !optional && field.HasDefault {
			return nil, fmt.Errorf("blueprint: extract %s: %s.%s has a default but is not Optional -- make it Optional[%s] = None and apply the default inside the function, since a schema cannot carry a default value (docs/blueprint.md, \"Format\")", dir, typeName, field.Name, inner)
		}
		if optional && firstOptional == "" {
			firstOptional = field.Name
		}
		if !optional && firstOptional != "" {
			return nil, fmt.Errorf("blueprint: extract %s: %s.%s is required but follows the optional %s.%s -- Python refuses a dataclass field with no default after one with a default, so every required param has to come first (a Go or TypeScript blueprint may interleave them; this is the one ordering difference between the three)", dir, typeName, field.Name, typeName, firstOptional)
		}
		wire := snakeCase(field.Name)
		if other, dup := seen[wire]; dup {
			return nil, fmt.Errorf("blueprint: extract %s: %s.%s and %s.%s both become the param name %q -- rename one", dir, typeName, other, typeName, field.Name, wire)
		}
		seen[wire] = field.Name
		params = append(params, SchemaParam{
			Name:       wire,
			SourceName: field.Name,
			Type:       pt,
			Required:   !optional,
		})
	}
	return params, nil
}

// pyOutputs reads the entrypoint's return annotation. A blueprint
// annotated None, or not annotated at all, has no outputs.
func pyOutputs(fn pyeval.PyFunction, classes map[string]pyeval.PyClass, dir string) (string, []SchemaOutput, error) {
	if fn.Returns == nil || *fn.Returns == "None" {
		return "", []SchemaOutput{}, nil
	}
	name := *fn.Returns
	cls, ok := classes[name]
	if !ok {
		return "", nil, fmt.Errorf("blueprint: extract %s: %s returns %s, which is not a dataclass declared in this blueprint", dir, fn.Name, name)
	}
	if !hasDataclassDecorator(cls) {
		return "", nil, fmt.Errorf("blueprint: extract %s: %s is not a @dataclass -- a blueprint's Outputs is a dataclass", dir, name)
	}

	var outputs []SchemaOutput
	seen := map[string]string{}
	for _, field := range cls.Fields {
		if field.Annotation == nil || !pyComputedSpellings[*field.Annotation] {
			written := "nothing"
			if field.Annotation != nil {
				written = *field.Annotation
			}
			return "", nil, fmt.Errorf("blueprint: extract %s: %s.%s is %s -- every output is a Computed, since an output is always a reference to a resource attribute", dir, name, field.Name, written)
		}
		wire := snakeCase(field.Name)
		if other, dup := seen[wire]; dup {
			return "", nil, fmt.Errorf("blueprint: extract %s: %s.%s and %s.%s both become the output name %q -- rename one", dir, name, other, name, field.Name, wire)
		}
		seen[wire] = field.Name
		outputs = append(outputs, SchemaOutput{Name: wire, SourceName: field.Name})
	}
	return name, outputs, nil
}

// hasDataclassDecorator reports whether cls carries @dataclass, in any
// of the spellings a real module uses.
func hasDataclassDecorator(cls pyeval.PyClass) bool {
	for _, d := range cls.Decorators {
		// A parameterized decorator arrives as "dataclass(frozen=True)";
		// only the name matters, and frozen/slots/kw_only change nothing
		// this reads.
		base, _, _ := strings.Cut(d, "(")
		switch strings.TrimSpace(base) {
		case "dataclass", "dataclasses.dataclass":
			return true
		}
	}
	return false
}

// pyStripOptional unwraps an optional annotation, returning the inner
// type and whether it was optional.
//
// Both spellings are accepted because both are ordinary modern Python
// and ast.unparse preserves whichever the author wrote: Optional[X] is
// the typing form, X | None is the operator form from PEP 604.
func pyStripOptional(annotation string) (string, bool) {
	a := strings.TrimSpace(annotation)
	for _, prefix := range []string{"Optional[", "typing.Optional["} {
		if strings.HasPrefix(a, prefix) && strings.HasSuffix(a, "]") {
			return strings.TrimSpace(a[len(prefix) : len(a)-1]), true
		}
	}
	if inner, found := strings.CutSuffix(a, "| None"); found {
		return strings.TrimSpace(inner), true
	}
	if inner, found := strings.CutPrefix(a, "None |"); found {
		return strings.TrimSpace(inner), true
	}
	return a, false
}
