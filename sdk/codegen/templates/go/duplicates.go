package gotmpl

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// CheckNoDuplicateDeclarations parses every file in files (filename ->
// source, ALL belonging to one package -- see CheckPackageNoDuplicateDeclarations
// for the map-of-packages-shaped entry point GeneratedRepo's own output
// actually needs) and reports every package-level identifier declared
// more than once across the whole set -- UBI-96's own real failure mode:
// Go shares ONE identifier namespace across var/type/const/func at
// package level (unlike TypeScript's separate type/value namespaces), AND
// that namespace spans every file in the package's own directory
// combined, not just one file -- so a `var Foo` in one file and a `type
// Foo struct` in a sibling file of the SAME package collide just as hard
// as if they were in the same file. UBI-98's own per-service-package
// restructure emits many small files per package (one per resource
// type) rather than UBI-96's one giant file, which is exactly why this
// now takes a map instead of a single source string.
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
func CheckNoDuplicateDeclarations(files map[string]string) error {
	fset := token.NewFileSet()

	positions := map[string][]token.Position{}
	record := func(name string, pos token.Pos) {
		if name == "" || name == "_" {
			return
		}
		positions[name] = append(positions[name], fset.Position(pos))
	}

	filenames := make([]string, 0, len(files))
	for filename := range files {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames) // deterministic parse order -- position reporting order must not depend on Go map iteration

	for _, filename := range filenames {
		file, err := parser.ParseFile(fset, filename, files[filename], 0)
		if err != nil {
			return fmt.Errorf("sdk/codegen/templates/go: CheckNoDuplicateDeclarations: parse %s: %w", filename, err)
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
			lines[i] = fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
		}
		b.WriteString(strings.Join(lines, ", "))
		b.WriteString("\n")
	}
	return fmt.Errorf("%s", b.String())
}

// CheckRepoNoDuplicateDeclarations is CheckNoDuplicateDeclarations' own
// entry point for a full GeneratedRepo output (path -> content, spanning
// every service package in one provider's repo tree, "go.mod" included)
// -- it groups files by their own directory (Go's own package boundary
// -- one package per directory, unconditionally) and runs
// CheckNoDuplicateDeclarations independently per directory, since a
// collision in one service package can never be a collision with a
// DIFFERENT service package (they are, by construction, separate Go
// packages/compilation units -- that structural separation is this whole
// restructure's own point). Non-.go files (go.mod) are skipped.
func CheckRepoNoDuplicateDeclarations(repo map[string]string) error {
	byDir := map[string]map[string]string{}
	for path, content := range repo {
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		dir := "."
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			dir = path[:idx]
		}
		if byDir[dir] == nil {
			byDir[dir] = map[string]string{}
		}
		byDir[dir][path] = content
	}

	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		if err := CheckNoDuplicateDeclarations(byDir[dir]); err != nil {
			return fmt.Errorf("package %s: %w", dir, err)
		}
	}
	return nil
}
