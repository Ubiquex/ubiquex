package blueprint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/blueprint/spec"
)

// extractgo.go derives a Schema from a Go blueprint's own source
// (docs/blueprint.md, "Blueprint schema"). It reads declarations only:
// no build, no type checker, no execution.
//
// The convention it enforces is one sentence -- a non-pointer config
// field is required, a pointer field is optional -- and everything below
// is the machinery for saying so precisely when source does not follow
// it. Each refusal names the fix, because the alternative to a clear
// refusal here is a schema that quietly describes something the function
// does not do, and every consumer downstream (HCL argument binding,
// describe_blueprint, provenance) would then be wrong together.

// goTypeVocabulary maps a Go type expression, as written, to this
// project's own language-neutral param vocabulary.
//
// Matched on the source spelling rather than a resolved type, because
// resolving would mean type-checking the package, which would mean
// building it, which would mean the blueprint's own dependencies must be
// fetched before its schema can be read. A published blueprint's schema
// has to be derivable from the files alone.
var goTypeVocabulary = map[string]spec.ParamType{
	"string":              spec.ParamString,
	"int":                 spec.ParamNumber,
	"bool":                spec.ParamBool,
	"[]string":            spec.ParamListString,
	"[]int":               spec.ParamListNumber,
	"sdk.CrossMarker":     spec.ParamCrossRef,
	"runtime.CrossMarker": spec.ParamCrossRef,
}

// ExtractGo derives the schema for the Go blueprint in dir.
//
// name is the blueprint's own declared name, which is the directory
// basename everywhere else in this package (buildManifest, ubx why, ubx
// render) and is passed in rather than re-derived so this function never
// disagrees with them about what a blueprint is called.
func ExtractGo(dir, name string) (*Schema, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return nil, fmt.Errorf("blueprint: extract %s: %w", dir, err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("blueprint: extract %s: no Go package here", dir)
	}
	if len(pkgs) > 1 {
		names := make([]string, 0, len(pkgs))
		for n := range pkgs {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("blueprint: extract %s: %d Go packages in one directory (%s) -- a blueprint is one package", dir, len(pkgs), strings.Join(names, ", "))
	}

	var pkgName string
	var pkg *ast.Package
	for n, p := range pkgs {
		pkgName, pkg = n, p
	}

	structs := map[string]*ast.StructType{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, s := range gd.Specs {
				ts, ok := s.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if st, ok := ts.Type.(*ast.StructType); ok {
					structs[ts.Name.Name] = st
				}
			}
		}
	}

	fn, err := findEntrypoint(pkg, dir)
	if err != nil {
		return nil, err
	}

	configTypeName, err := soleStructParam(fn, dir)
	if err != nil {
		return nil, err
	}
	configStruct, ok := structs[configTypeName]
	if !ok {
		return nil, fmt.Errorf("blueprint: extract %s: %s takes %s, which is not a struct type declared in this package", dir, fn.Name.Name, configTypeName)
	}
	params, err := extractParams(configStruct, configTypeName, dir)
	if err != nil {
		return nil, err
	}

	outputsTypeName, outputs, err := extractOutputs(fn, structs, dir)
	if err != nil {
		return nil, err
	}

	return &Schema{
		SchemaVersion: SchemaVersion,
		Name:          name,
		Entrypoint: Entrypoint{
			Language:    "go",
			Package:     pkgName,
			Function:    fn.Name.Name,
			ConfigType:  configTypeName,
			OutputsType: outputsTypeName,
		},
		Params:     params,
		Outputs:    outputs,
		Defaults:   DefaultsNotDerivable,
		Derivation: Derivation{Assumptions: []string{}},
	}, nil
}

// findEntrypoint returns the one exported function that could be the
// blueprint. Ambiguity is refused rather than resolved by a naming
// convention, which would be a second rule an author has to know.
func findEntrypoint(pkg *ast.Package, dir string) (*ast.FuncDecl, error) {
	var candidates []*ast.FuncDecl
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || !fd.Name.IsExported() {
				continue
			}
			if fd.Type.Params == nil || len(fd.Type.Params.List) != 1 {
				continue
			}
			candidates = append(candidates, fd)
		}
	}
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return nil, fmt.Errorf("blueprint: extract %s: no exported function taking exactly one parameter -- a blueprint is one exported function taking one Config struct", dir)
	default:
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.Name.Name
		}
		sort.Strings(names)
		return nil, fmt.Errorf("blueprint: extract %s: %d exported functions take one parameter (%s) -- exactly one is the blueprint, so unexport the others or move them", dir, len(candidates), strings.Join(names, ", "))
	}
}

// soleStructParam returns the named type of the entrypoint's own single
// parameter.
func soleStructParam(fn *ast.FuncDecl, dir string) (string, error) {
	field := fn.Type.Params.List[0]
	if len(field.Names) > 1 {
		return "", fmt.Errorf("blueprint: extract %s: %s declares more than one parameter name -- a blueprint takes one Config struct", dir, fn.Name.Name)
	}
	ident, ok := field.Type.(*ast.Ident)
	if !ok {
		return "", fmt.Errorf("blueprint: extract %s: %s's parameter is %s, not a struct type declared in this package -- a blueprint takes one Config struct by value", dir, fn.Name.Name, exprString(field.Type))
	}
	return ident.Name, nil
}

// extractParams turns the config struct's fields into schema params, in
// declaration order.
func extractParams(st *ast.StructType, typeName, dir string) ([]SchemaParam, error) {
	var params []SchemaParam
	seen := map[string]string{}
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			return nil, fmt.Errorf("blueprint: extract %s: %s has an embedded field -- every param is a named field, since an embedded one has no name to bind an argument to", dir, typeName)
		}
		for _, ident := range field.Names {
			if !ident.IsExported() {
				return nil, fmt.Errorf("blueprint: extract %s: %s.%s is unexported -- a caller cannot set it, so it cannot be a param", dir, typeName, ident.Name)
			}
			typeExpr := field.Type
			required := true
			if star, isPtr := typeExpr.(*ast.StarExpr); isPtr {
				required = false
				typeExpr = star.X
			}
			written := exprString(typeExpr)
			pt, ok := goTypeVocabulary[written]
			if !ok {
				return nil, fmt.Errorf("blueprint: extract %s: %s.%s is %s, which is not a param type -- use one of string, int, bool, []string, []int, sdk.CrossMarker, or a pointer to one for an optional param", dir, typeName, ident.Name, exprString(field.Type))
			}
			wire := snakeCase(ident.Name)
			if other, dup := seen[wire]; dup {
				return nil, fmt.Errorf("blueprint: extract %s: %s.%s and %s.%s both become the param name %q -- rename one", dir, typeName, other, typeName, ident.Name, wire)
			}
			seen[wire] = ident.Name
			params = append(params, SchemaParam{Name: wire, Type: pt, Required: required})
		}
	}
	return params, nil
}

// extractOutputs reads the entrypoint's return type. A blueprint that
// returns nothing is legal and has no outputs.
func extractOutputs(fn *ast.FuncDecl, structs map[string]*ast.StructType, dir string) (string, []SchemaOutput, error) {
	results := fn.Type.Results
	if results == nil || len(results.List) == 0 {
		return "", []SchemaOutput{}, nil
	}
	if len(results.List) != 1 || len(results.List[0].Names) > 0 {
		return "", nil, fmt.Errorf("blueprint: extract %s: %s returns %d values -- a blueprint returns one Outputs struct, or nothing", dir, fn.Name.Name, len(results.List))
	}
	ident, ok := results.List[0].Type.(*ast.Ident)
	if !ok {
		return "", nil, fmt.Errorf("blueprint: extract %s: %s returns %s, not a struct type declared in this package", dir, fn.Name.Name, exprString(results.List[0].Type))
	}
	st, ok := structs[ident.Name]
	if !ok {
		return "", nil, fmt.Errorf("blueprint: extract %s: %s returns %s, which is not a struct type declared in this package", dir, fn.Name.Name, ident.Name)
	}

	var outputs []SchemaOutput
	seen := map[string]string{}
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			return "", nil, fmt.Errorf("blueprint: extract %s: %s has an embedded field -- every output is a named field", dir, ident.Name)
		}
		written := exprString(field.Type)
		if written != "*sdk.Computed" && written != "*runtime.Computed" {
			return "", nil, fmt.Errorf("blueprint: extract %s: %s.%s is %s -- every output is a *sdk.Computed, since an output is always a reference to a resource attribute", dir, ident.Name, field.Names[0].Name, written)
		}
		for _, fieldIdent := range field.Names {
			if !fieldIdent.IsExported() {
				return "", nil, fmt.Errorf("blueprint: extract %s: %s.%s is unexported -- a caller cannot read it, so it cannot be an output", dir, ident.Name, fieldIdent.Name)
			}
			wire := snakeCase(fieldIdent.Name)
			if other, dup := seen[wire]; dup {
				return "", nil, fmt.Errorf("blueprint: extract %s: %s.%s and %s.%s both become the output name %q -- rename one", dir, ident.Name, other, ident.Name, fieldIdent.Name, wire)
			}
			seen[wire] = fieldIdent.Name
			outputs = append(outputs, SchemaOutput{Name: wire})
		}
	}
	return ident.Name, outputs, nil
}

// exprString renders a type expression back to the source spelling the
// vocabulary is matched against.
func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		if t.Len != nil {
			return "[N]" + exprString(t.Elt)
		}
		return "[]" + exprString(t.Elt)
	case *ast.MapType:
		return "map[" + exprString(t.Key) + "]" + exprString(t.Value)
	case *ast.InterfaceType:
		if t.Methods == nil || len(t.Methods.List) == 0 {
			return "any"
		}
		return "interface{...}"
	default:
		return fmt.Sprintf("%T", e)
	}
}

// blueprintDirName is the name every other part of this package derives
// a blueprint's own identity from.
func blueprintDirName(dir string) string { return filepath.Base(dir) }
