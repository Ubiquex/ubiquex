package blueprint

import (
	"fmt"
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
