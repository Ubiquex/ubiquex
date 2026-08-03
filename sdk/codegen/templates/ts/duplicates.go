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

// CheckNoDuplicateDeclarations reports every TS identifier ONE generated
// file (ResourceFile's own output -- UBI-98's per-resource-type-file
// restructure) declares more than once WITHIN the same namespace
// (interface names against each other, const names against each other).
//
// UBI-96's own real failure mode (two DIFFERENT resources' own nested
// interfaces deriving the identical bare name, silently merging rather
// than erroring) can no longer happen ACROSS files at all under UBI-98's
// restructure -- confirmed, not assumed: ES modules give every FILE its
// own independent scope, so two different resource types' own
// declarations, now always in separate files, can never collide with
// each other regardless of naming (ir/resourceRenderer's own doc
// comments have the full account). What this function still checks for
// real, WITHIN one file: the same "_"-joined nested-interface naming
// scheme still needs to stay collision-free within a single resource
// type's own recursive tree, which is exactly what it verifies. See
// sdk/codegen/templates/go's own CheckNoDuplicateDeclarations (the same
// idea, via go/parser -- this is the regex-based equivalent, since there
// is no TS parser readily available from Go here; sufficient because
// this package's own output shape is fully known and constrained, unlike
// arbitrary user TS).
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

// CheckRepoNoDuplicateDeclarations is CheckNoDuplicateDeclarations' own
// entry point for a full GeneratedRepo output (path -> content, spanning
// every service directory in one provider's repo tree, "package.json"
// included) -- runs the single-file check independently against every
// ".ts" file. Deliberately NOT a cross-file check (unlike Go's own
// CheckRepoNoDuplicateDeclarations, which groups by directory because
// Go's package-level namespace spans a whole directory): confirmed, not
// assumed, that no such cross-file namespace exists for TS at all (ES
// modules give every file its own independent scope) -- kept as a
// uniform, per-language API shape cli/sdk.go's own production path can
// call identically across all three languages, not because TS has
// anything analogous to check across files.
func CheckRepoNoDuplicateDeclarations(repo map[string]string) error {
	paths := make([]string, 0, len(repo))
	for path := range repo {
		if strings.HasSuffix(path, ".ts") {
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
