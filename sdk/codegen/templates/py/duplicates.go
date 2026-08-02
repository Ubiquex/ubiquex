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

// CheckNoDuplicateDeclarations reports every Python identifier this
// package's own GeneratedFile declares more than once at module scope.
// UBI-96's real failure mode here is the WORST of the three languages:
// no error, no warning, just a later resource's own class or
// ResourceBinding silently overwriting an earlier one in the generated
// module's namespace, corrupting whichever binding got shadowed with no
// signal at all. See sdk/codegen/templates/go's own
// CheckNoDuplicateDeclarations doc comment for the shared root cause and
// the "_" join fix both packages now use.
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
