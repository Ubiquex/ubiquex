package ts

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// interfaceDeclRe / constDeclRe match this package's own two top-level
// declaration shapes (renderResource, above): `export interface Name {`
// and `export const Name: ...` / `export const Name = ...`. TS keeps
// separate type and value namespaces (an interface and a const CAN share
// a name safely -- unlike Go/Python, see sdk/codegen/templates/go's own
// resourceRenderer doc comment for the full account), so each is checked
// for duplicates independently, never against each other.
var (
	interfaceDeclRe = regexp.MustCompile(`(?m)^export interface (\w+)`)
	constDeclRe     = regexp.MustCompile(`(?m)^export const (\w+)`)
)

// CheckNoDuplicateDeclarations reports every TS identifier this package's
// own GeneratedFile declares more than once WITHIN the same namespace
// (interface names against each other, const names against each other).
// UBI-96's real failure mode here: two DIFFERENT resources' own nested
// interfaces (KindObject fields, at any depth) can derive the identical
// name (the bare "parentPascal+fieldPascal" concatenation, before this
// session's own "_" join fix) -- TS's `interface` declaration merging
// means this can fail SILENTLY (shapes merge rather than erroring) rather
// than a clean compile error, which is worse, not better, than Go's hard
// redeclaration failure. See sdk/codegen/templates/go's own
// CheckNoDuplicateDeclarations (the same idea, via go/parser -- this is
// the regex-based equivalent, since there is no TS parser readily
// available from Go here; sufficient because this package's own output
// shape is fully known and constrained, unlike arbitrary user TS).
func CheckNoDuplicateDeclarations(src string) error {
	var b strings.Builder
	n := 0
	if names := duplicateNames(interfaceDeclRe, src); len(names) > 0 {
		n += len(names)
		fmt.Fprintf(&b, "interface declared more than once: %s\n", strings.Join(names, ", "))
	}
	if names := duplicateNames(constDeclRe, src); len(names) > 0 {
		n += len(names)
		fmt.Fprintf(&b, "const declared more than once: %s\n", strings.Join(names, ", "))
	}
	if n == 0 {
		return nil
	}
	return fmt.Errorf("sdk/codegen/templates/ts: %d duplicate top-level declaration(s):\n%s", n, b.String())
}

func duplicateNames(re *regexp.Regexp, src string) []string {
	counts := map[string]int{}
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		counts[m[1]]++
	}
	var dupes []string
	for name, count := range counts {
		if count > 1 {
			dupes = append(dupes, name)
		}
	}
	sort.Strings(dupes)
	return dupes
}
