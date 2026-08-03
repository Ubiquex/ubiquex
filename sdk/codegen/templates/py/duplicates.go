package pytmpl

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// classDeclRe / moduleAssignRe match this package's own two top-level
// declaration shapes (renderResource, above): `class Name:` (always
// preceded by `@dataclasses.dataclass` on its own line, but the class
// line itself is what occupies the module namespace) and a bare
// `Name = sdk.ResourceBinding(` module-level assignment. Unlike TS,
// Python has ONE flat module namespace for both -- a later `class Foo`
// or `Foo = ...` silently REPLACES an earlier one with the same name,
// no error at all (see sdk/codegen/templates/py's own resourceRenderer
// doc comment for the full account) -- so both shapes are checked
// together here, not independently the way TS's separate type/value
// namespaces allow.
var (
	classDeclRe    = regexp.MustCompile(`(?m)^class (\w+):`)
	moduleAssignRe = regexp.MustCompile(`(?m)^(\w+) = sdk\.ResourceBinding\(`)
)

// CheckNoDuplicateDeclarations reports every Python identifier ONE
// generated file (ResourceFile's own output -- UBI-98's per-resource-
// type-file restructure) declares more than once at module scope.
//
// UBI-96's own real failure mode here was the WORST of the three
// languages: no error, no warning, just a later resource's own class or
// ResourceBinding silently overwriting an earlier one in a flat module's
// namespace, corrupting whichever binding got shadowed with no signal at
// all. That specific cross-resource shape can no longer happen ACROSS
// files at all under UBI-98's restructure -- confirmed, not assumed:
// Python modules give every FILE its own independent namespace, so two
// different resource types' own declarations, now always in separate
// files, can never collide with each other regardless of naming. What
// this function still checks for real, WITHIN one file: the same
// "_"-joined nested-dataclass naming scheme still needs to stay
// collision-free within a single resource type's own recursive tree,
// which is exactly what it verifies. See sdk/codegen/templates/go's own
// CheckNoDuplicateDeclarations doc comment for the shared root cause and
// the "_" join fix all three packages now use.
func CheckNoDuplicateDeclarations(src string) error {
	counts := map[string]int{}
	for _, m := range classDeclRe.FindAllStringSubmatch(src, -1) {
		counts[m[1]]++
	}
	for _, m := range moduleAssignRe.FindAllStringSubmatch(src, -1) {
		counts[m[1]]++
	}
	var dupes []string
	for name, count := range counts {
		if count > 1 {
			dupes = append(dupes, name)
		}
	}
	if len(dupes) == 0 {
		return nil
	}
	sort.Strings(dupes)
	return fmt.Errorf("sdk/codegen/templates/py: %d module-level identifier(s) declared more than once: %s", len(dupes), strings.Join(dupes, ", "))
}

// CheckRepoNoDuplicateDeclarations is CheckNoDuplicateDeclarations' own
// entry point for a full GeneratedRepo output (path -> content, spanning
// every service package in one provider's repo tree, "pyproject.toml"
// included) -- runs the single-file check independently against every
// ".py" file. Deliberately NOT a cross-file check (unlike Go's own
// CheckRepoNoDuplicateDeclarations, which groups by directory because
// Go's package-level namespace spans a whole directory): confirmed, not
// assumed, that no such cross-file namespace exists for Python at all
// (every module has its own independent namespace) -- kept as a
// uniform, per-language API shape cli/sdk.go's own production path can
// call identically across all three languages.
func CheckRepoNoDuplicateDeclarations(repo map[string]string) error {
	paths := make([]string, 0, len(repo))
	for path := range repo {
		if strings.HasSuffix(path, ".py") {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	for _, path := range paths {
		if err := CheckNoDuplicateDeclarations(repo[path]); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return nil
}
