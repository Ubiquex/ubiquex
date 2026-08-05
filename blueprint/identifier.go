package blueprint

import (
	"fmt"
	"regexp"
	"strings"
)

// splitIdentifierParts splits a hyphen/underscore-separated lowercase
// name into its component parts (e.g. "ci-artifacts" -> ["ci",
// "artifacts"], "message_retention_seconds" -> ["message", "retention",
// "seconds"]) -- shared by resource names (hyphenated, as an intent
// provider draft's own ResourceIntent.Name convention) and provider wire
// field names (underscored) alike, since neither alphabet can ever
// collide with the other's own separator. Mirrors
// sdk/codegen/templates/go's own splitWireName discipline (never a
// best-effort coercion -- reject anything outside lowercase ascii +
// digits, don't guess), broadened to accept "-" as a second separator
// this package's own dir/resource names actually use.
func splitIdentifierParts(name string) ([]string, error) {
	if name == "" {
		return nil, fmt.Errorf("empty name")
	}
	fields := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' })
	if len(fields) == 0 {
		return nil, fmt.Errorf("name %q has no identifier characters", name)
	}
	for _, f := range fields {
		for _, r := range f {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
				return nil, fmt.Errorf("name %q: unsupported character %q (only lowercase ascii + digits + hyphen/underscore supported)", name, r)
			}
		}
	}
	return fields, nil
}

// pascalCase converts a hyphen/underscore-separated lowercase name into
// Go's own exported identifier casing ("ci-artifacts" -> "CiArtifacts").
func pascalCase(name string) (string, error) {
	parts, err := splitIdentifierParts(name)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String(), nil
}

// camelCase converts a hyphen/underscore-separated lowercase name into
// Go's own unexported identifier casing ("repo_name" -> "repoName") --
// used for the generated function's own parameter names and the local
// variables holding each resource's *Computed handle.
func camelCase(name string) (string, error) {
	p, err := pascalCase(name)
	if err != nil {
		return "", err
	}
	return strings.ToLower(p[:1]) + p[1:], nil
}

// lowerFirst lower-cases just the first byte of an already-PascalCase
// identifier -- used to derive a resource's own local variable name
// (e.g. "CiArtifacts" -> "ciArtifacts") without re-deriving it from the
// original wire/resource name a second time.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// goReservedIdent guards a derived package/identifier name against a Go
// syntax keyword or a name special to the go TOOL itself -- the same
// class of real, live-verified collision
// sdk/codegen/templates/go/go.go's own goPackageIdent guards
// (`package default`/`package main` with no func main()), reused here at
// smaller scope (this package only ever derives ONE top-level package
// name per blueprint, not hundreds of AWS-service names, so the fuller
// reserved list there is deliberately not duplicated -- only the classes
// that could plausibly collide with a hand-chosen blueprint directory
// name).
func goReservedIdent(name string) bool {
	switch name {
	case "break", "case", "chan", "const", "continue",
		"default", "defer", "else", "fallthrough", "for",
		"func", "go", "goto", "if", "import",
		"interface", "map", "package", "range", "return",
		"select", "struct", "switch", "type", "var",
		"main", "internal", "vendor", "testdata":
		return true
	}
	return false
}

// placeholderStrip/repeatedSeparator back forEachIdentifierBasis below.
var placeholderStrip = regexp.MustCompile(`\{[^}]*\}`)
var repeatedSeparator = regexp.MustCompile(`[-_]{2,}`)

// forEachIdentifierBasis derives the identifier-safe basis for a
// for_each resource's own TEMPLATED Name (UBI-129) -- e.g.
// "subnet-{availability_zones}" -> "subnet" -- by stripping every
// {...} placeholder token and collapsing/trimming the separator left
// behind. Needed because a for_each resource's own per-language
// IDENTIFIER (its Go/TS/Python binding/config type name, derived via
// pascalCase below) has no reason to vary per iteration the way its own
// RUNTIME name does -- every instance shares the SAME binding -- but
// pascalCase (via splitIdentifierParts) rejects "{"/"}" outright, so an
// ordinary resource's "just pascalCase(RI.Name) directly" derivation
// would hard-fail on a for_each resource's own templated Name. An
// ordinary (non-for_each) resource never calls this at all -- its own
// ident derivation is completely unaffected, byte-identical to every
// prior slice.
func forEachIdentifierBasis(name string) (string, error) {
	stripped := placeholderStrip.ReplaceAllString(name, "")
	stripped = repeatedSeparator.ReplaceAllString(stripped, "-")
	stripped = strings.Trim(stripped, "-_")
	if stripped == "" {
		return "", fmt.Errorf("name %q has no identifier-safe text left once its own {param} tokens are removed -- give it a literal prefix, e.g. \"subnet-{list_param}\"", name)
	}
	return stripped, nil
}

// packageIdent derives a valid Go package identifier from a hyphen/
// underscore-separated name (Slice 1: the Ubxfile's own directory
// basename) -- parts joined with no separator, since Go package names
// are conventionally one bare lowercase word ("ci-platform" ->
// "ciplatform").
func packageIdent(name string) (string, error) {
	parts, err := splitIdentifierParts(name)
	if err != nil {
		return "", fmt.Errorf("blueprint name %q: %w", name, err)
	}
	joined := strings.Join(parts, "")
	if joined[0] >= '0' && joined[0] <= '9' {
		return "", fmt.Errorf("blueprint name %q: derived package name %q starts with a digit -- not a valid Go identifier, rename the directory", name, joined)
	}
	if goReservedIdent(joined) {
		return "", fmt.Errorf("blueprint name %q: derived package name %q is a Go keyword or tool-reserved name -- rename the directory", name, joined)
	}
	return joined, nil
}

// ---------------------------------------------------------------------
// Slice 4: TypeScript and Python identifier conventions -- each its own
// small, separate function (matching this file's own established
// precedent above: a deliberately separate reimplementation per
// language/package boundary, rather than reaching into
// sdk/codegen/templates/ts|py's own unexported pascalCase/camelCase/
// pythonIdentifier for the same reason those two packages don't share
// their own copies with EACH OTHER either -- see docs/blueprint.md's own
// "Multi-language codegen" section). PascalCase/camelCase themselves
// need no TS-specific variant at all: JavaScript/TypeScript's own
// identifier casing convention is byte-identical to Go's (both
// PascalCase types, camelCase values), so tsgen.go reuses pascalCase/
// camelCase/lowerFirst above directly.
// ---------------------------------------------------------------------

// tsReservedIdent guards a derived top-level TS identifier (the
// blueprint's own exported function name, ultimately) against a real
// JavaScript/TypeScript reserved word -- smaller-scope than the full ES
// spec list, matching goReservedIdent's own stated precedent: this
// package only ever derives a handful of top-level identifiers per
// blueprint, not hundreds, so only the words that could plausibly
// collide with a hand-chosen blueprint directory name are listed.
func tsReservedIdent(name string) bool {
	switch name {
	case "break", "case", "catch", "class", "const", "continue",
		"debugger", "default", "delete", "do", "else", "export",
		"extends", "false", "finally", "for", "function", "if",
		"import", "in", "instanceof", "new", "null", "return",
		"super", "switch", "this", "throw", "true", "try",
		"typeof", "var", "void", "while", "with", "yield",
		"let", "static", "await", "async", "enum", "interface",
		"type", "namespace", "module", "declare", "public",
		"private", "protected", "implements", "package":
		return true
	}
	return false
}

// pythonKeywords is the lowercase-only subset of Python 3's reserved-word
// set (keyword.kwlist) that a real, lowercase-ascii-only wire/resource
// name could ever actually collide with -- capitalized reserved words
// (False/None/True) can never match, since splitIdentifierParts only
// ever accepts lowercase ascii + digits (matching
// sdk/codegen/templates/py/py.go's own pythonKeywords, independently
// reimplemented here for the same package-boundary reason every other
// identifier helper in this file already is).
var pythonKeywords = map[string]bool{
	"and": true, "as": true, "assert": true, "async": true, "await": true,
	"break": true, "class": true, "continue": true, "def": true, "del": true,
	"elif": true, "else": true, "except": true, "finally": true, "for": true,
	"from": true, "global": true, "if": true, "import": true, "in": true,
	"is": true, "lambda": true, "nonlocal": true, "not": true, "or": true,
	"pass": true, "raise": true, "return": true, "try": true, "while": true,
	"with": true, "yield": true,
}

// pythonIdentifier converts a hyphen/underscore-separated lowercase name
// into Python's own snake_case identifier convention -- usually IS the
// original name verbatim with hyphens folded to underscores (real wire
// names are already lowercase-with-underscores, i.e. already valid,
// idiomatic Python identifiers), except for the real, live-relevant
// case of a reserved-word collision (e.g. a param or resource named
// "lambda" -- both a real Python keyword AND a real AWS service),
// escaped with a trailing underscore, PEP 8's own established
// convention for exactly this collision (matching
// sdk/codegen/templates/py's own pythonIdentifier precedent).
func pythonIdentifier(name string) (string, error) {
	parts, err := splitIdentifierParts(name)
	if err != nil {
		return "", err
	}
	joined := strings.Join(parts, "_")
	if pythonKeywords[joined] {
		return joined + "_", nil
	}
	return joined, nil
}
