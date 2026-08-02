package gotmpl

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// CheckNoDuplicateDeclarations parses src (a GeneratedFile's own output)
// and reports every package-level identifier declared more than once --
// UBI-96's own real failure mode: a flat package holds every resource's
// own declarations together, and Go shares ONE identifier namespace
// across var/type/const/func at package level (unlike TypeScript's
// separate type/value namespaces), so a `var Foo` and a `type Foo
// struct` collide just as hard as two `type Foo struct`s would.
//
// Called from two places, deliberately: cli/sdk.go's own generateOneProvider
// (production path -- refuse to WRITE a broken file at generation time,
// rather than only catching this in a test) and this package's own tests
// (a fast, hermetic, no-`go build`-needed way to assert "no collisions"
// against a large synthetic fixture, complementary to the real `go build`
// compile tests that also exist here). Reports every duplicate found in
// one pass, not just the first -- the ticket's own founder repro hit
// "too many errors" truncation from `go build` itself; this gives a
// complete, itemized list instead.
func CheckNoDuplicateDeclarations(src string) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generated.go", src, 0)
	if err != nil {
		return fmt.Errorf("sdk/codegen/templates/go: CheckNoDuplicateDeclarations: parse: %w", err)
	}

	positions := map[string][]token.Position{}
	record := func(name string, pos token.Pos) {
		if name == "" || name == "_" {
			return
		}
		positions[name] = append(positions[name], fset.Position(pos))
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					record(s.Name.Name, s.Name.Pos())
				case *ast.ValueSpec:
					for _, name := range s.Names {
						record(name.Name, name.Pos())
					}
				}
			}
		case *ast.FuncDecl:
			if d.Recv == nil { // methods don't occupy the package-level namespace
				record(d.Name.Name, d.Name.Pos())
			}
		}
	}

	names := make([]string, 0, len(positions))
	for name, occurrences := range positions {
		if len(occurrences) > 1 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	var b strings.Builder
	fmt.Fprintf(&b, "sdk/codegen/templates/go: %d package-level identifier(s) declared more than once:\n", len(names))
	for _, name := range names {
		fmt.Fprintf(&b, "  %s: declared at ", name)
		lines := make([]string, len(positions[name]))
		for i, pos := range positions[name] {
			lines[i] = fmt.Sprintf("line %d", pos.Line)
		}
		b.WriteString(strings.Join(lines, ", "))
		b.WriteString("\n")
	}
	return fmt.Errorf("%s", b.String())
}
