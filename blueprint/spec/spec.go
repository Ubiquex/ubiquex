// Package spec holds the Ubxfile's own leaf value types: the params:
// type vocabulary, one params: entry, and one outputs: entry.
//
// It exists so a consumer that only needs these types does not have to
// import the whole blueprint package. Blueprint packaging and invocation
// live there too, and invoke.go/pydeps.go pull in all three language
// evaluators plus two embedded SDK runtimes: importing "blueprint" for a
// single Param costs 2.7MB of binary (5.6MB against 2.9MB, measured), for
// machinery the consumer never runs.
//
// The Terraform converter is the consumer that made this worth doing. It
// uses exactly twelve symbols from blueprint, all of them here, and none
// of the codegen or invocation surface.
//
// Deliberately types only. The generators are entangled with invoke.go
// through sixteen unexported helpers (camelCase, defaultLiteral,
// requiredFirstOrder and the rest), because invoke.go synthesizes a
// calling stack using the same identifier and literal machinery codegen
// uses. Splitting THOSE apart means exporting all sixteen, which is its
// own decision on its own merits, not something to smuggle in here.
//
// blueprint aliases every type below, so blueprint.Param and
// spec.Param are the same type and no existing call site changes.
package spec

// ParamType is one of Ubxfile's own recognized params: types.
type ParamType string

const (
	ParamString ParamType = "string"
	ParamNumber ParamType = "number"
	ParamBool   ParamType = "bool"

	// ParamListString/ParamListNumber (UBI-129) are the two list-typed
	// params: types -- "list(<element>)", the literal spelling
	// parseParamSpec matches, matching this file's own "type is an
	// opaque, exact-string literal, never parsed into parts" convention
	// (ParamString/ParamNumber/ParamBool are handled identically). A
	// list-typed param can ONLY be declared "required" -- see
	// parseDefaultValue's own explicit refusal below -- since it's
	// always consumed by exactly one for_each resource (docs/blueprint.md's
	// own "List-typed parameters + iteration" section), never by a
	// functional-options-style optional value. list(bool) is deliberately
	// not added: no real worked example in this ticket's own design
	// record needs one, matching this file's own established "extend
	// when a real blueprint needs it, not speculatively" discipline
	// (ParamNumber's own "always int, no float" precedent above).
	ParamListString ParamType = "list(string)"
	ParamListNumber ParamType = "list(number)"

	// ParamCrossRef (UBI-134) is a param whose call-site value is always
	// a real "@<stack>.<type>.<name>[.<attr-path>]" cross-stack address
	// (diagram/crossref.go's own parseCrossRefLabel established this
	// exact "@" grammar first, for a diagram reference node's label --
	// reused here verbatim rather than inventing a second one), never a
	// plain string a caller could accidentally pass instead. This is
	// this project's own established "explicit typed markers, never
	// silent string-sniffing" discipline (CLAUDE.md; the same posture
	// $ref/$cross/$secret/$computed already hold to at the resolver
	// level, core/resolver/refs.go) applied one layer up, at a
	// blueprint's own declared param surface -- a param wanting a
	// cross-stack reference says so explicitly in its own type, rather
	// than every string-typed param silently being probed for an "@"
	// prefix.
	ParamCrossRef ParamType = "cross_ref"
)

// IsList reports whether t is one of the two list-typed params: types
// (UBI-129) -- a list param is consumed exclusively via a for_each
// resource's own synthetic per-element/index tokens (blueprint/decode.go),
// never as an ordinary bare {param_name} reference the way a scalar
// param is.
func (t ParamType) IsList() bool {
	return t == ParamListString || t == ParamListNumber
}

// GoType returns the Go type a param of this type compiles to in the
// generated function's own signature. ParamNumber always compiles to
// Go's int (docs/blueprint.md: "number always compiles to Go int" --
// every real example in UBI-74's own design record is an integer count,
// float support is deliberately not invented ahead of a real need).
func (t ParamType) GoType() string {
	switch t {
	case ParamString:
		return "string"
	case ParamNumber:
		return "int"
	case ParamBool:
		return "bool"
	case ParamListString:
		return "[]string"
	case ParamListNumber:
		return "[]int"
	// sdk.CrossMarker (github.com/ubiquex/ubx-sdk-go/runtime), a real,
	// already-exported concrete type -- generated Go code already
	// imports this package unconditionally (invoke.go's writeGoCaller,
	// gogen.go's own header), matching the SAME "typed *sdk.Computed"
	// precedent this file's own outputs: support already established
	// for another opaque, runtime-only SDK value, rather than falling
	// back to Go's untyped "any" the way an unrecognized type does.
	case ParamCrossRef:
		return "sdk.CrossMarker"
	default:
		return "any"
	}
}

// TSType returns the TypeScript type a param of this type compiles to in
// the generated function's own signature (Slice 4). Unlike Go,
// ParamNumber -> "number" carries no int/float distinction at all --
// TypeScript has exactly one numeric type, so there's no Go-style
// "always int" decision to make here in the first place.
func (t ParamType) TSType() string {
	switch t {
	case ParamString:
		return "string"
	case ParamNumber:
		return "number"
	case ParamBool:
		return "boolean"
	case ParamListString:
		return "string[]"
	case ParamListNumber:
		return "number[]"
	// "any", matching the SAME opaque-runtime-value convention this
	// file's own outputs: support already uses for every declared
	// output ("{ repoArn: any; ... }", GenerateTS) -- TS's own cross()
	// (sdk/ts/runtime/src/index.ts) is itself generic ("T = unknown"),
	// so typing the param strictly here would force every real call
	// site to spell out an explicit cross<CrossMarker>(...) instantiation
	// for no real type-safety gain (a cross-stack reference's own real
	// value is never available at typecheck time either way).
	case ParamCrossRef:
		return "any"
	default:
		return "any"
	}
}

// PyType returns the Python type annotation a param of this type
// compiles to in the generated function's own signature (Slice 4).
// Mirrors GoType's own "number always compiles to int" decision (every
// real example in UBI-74's own design record is an integer count; float
// support is deliberately not invented ahead of a real need) -- applied
// here too, for consistency across all three generated languages rather
// than letting Python's own native float default quietly diverge.
func (t ParamType) PyType() string {
	switch t {
	case ParamString:
		return "str"
	case ParamNumber:
		return "int"
	case ParamBool:
		return "bool"
	case ParamListString:
		return "list[str]"
	case ParamListNumber:
		return "list[int]"
	// "Any", mirroring TSType's own reasoning above -- Python's own
	// cross() (sdk/py/ubx_sdk/__init__.py) is already declared -> Any,
	// the same established opaque-runtime-value convention this file's
	// own outputs: support already uses ("-> Any:", GeneratePy).
	case ParamCrossRef:
		return "Any"
	default:
		return "Any"
	}
}

// Param is one params: entry, in the Ubxfile's own declared order.
type Param struct {
	Name     string
	Type     ParamType
	Required bool
	// Default holds the parsed default value (string, int, or bool,
	// matching Type) when !Required; nil when Required.
	Default any
}

// Output is one outputs: entry (UBI-128), in the Ubxfile's own
// declaration order -- never a map, matching Params' own determinism
// discipline: a generated function's own return-value ORDER must be
// stable across builds. Target is "<resource-slug>.<attribute>",
// verbatim -- the resource slug is only checked against the blueprint's
// own real resolved resources later (blueprint.ExpandCalls, once the
// blueprint has actually been invoked and its real resources are
// known); ParseUbxfile has no resource-slug knowledge of its own to
// check against (resources: is free-form prose at this stage).
type Output struct {
	Name   string
	Target string
}
