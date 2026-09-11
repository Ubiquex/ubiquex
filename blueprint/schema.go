package blueprint

import (
	"github.com/ubiquex/ubiquex/blueprint/spec"
)

// schema.go is the blueprints-as-code schema: what `ubx blueprint
// package` derives from a blueprint's own function signature and writes
// beside the code (docs/blueprint.md, "Blueprint schema").
//
// It replaces the Ubxfile's machine-readable half. Nobody writes it. It
// is derived, so it cannot disagree with the function it describes, and
// it is covered by the content hash like every other file in the
// packaged directory.
//
// Four consumers, all of which the Ubxfile used to serve:
//
//   - the HCL `blueprint` block and blueprint_calls, which bind named
//     arguments to typed positional ones and need names, types, order
//     and required-ness to do it
//   - describe_blueprint, which reports what a pulled blueprint takes
//   - list_blueprints, and `resolve`'s own direct-call provenance walk,
//     both of which need a marker saying "this directory is a
//     blueprint" and used to look for an Ubxfile
//   - per-language wrappers, later, which would be generated FROM this
//     rather than parsed back out of code

// SchemaFileName is what package writes and every consumer looks for.
// It is the blueprint marker, replacing UbxfileName in that role.
const SchemaFileName = "blueprint.schema.json"

// SchemaVersion is this document's own version, bumped when its shape
// changes in a way a reader must notice.
const SchemaVersion = 1

// Schema is the derived description of one blueprint.
type Schema struct {
	SchemaVersion int            `json:"schema_version"`
	Name          string         `json:"name"`
	Entrypoint    Entrypoint     `json:"entrypoint"`
	Params        []SchemaParam  `json:"params"`
	Outputs       []SchemaOutput `json:"outputs"`
	Defaults      Defaults       `json:"defaults"`
	Derivation    Derivation     `json:"derivation"`
}

// Entrypoint names what to call and how to import it.
//
// The import specifier is a separate field per language rather than one
// field meaning three different things. They are genuinely different
// kinds of value: a TypeScript module specifier and a Python import
// root are what a caller writes in an import statement, while a Go
// package NAME is not importable at all, so one shared field would be
// an import path in two languages and a symbol namespace in the third.
// Exactly one of the three is set, chosen by Language.
type Entrypoint struct {
	// Language is "go", "ts" or "py". A blueprint is single-language:
	// the multi-language build is gone with the Ubxfile, and comes back
	// later as wrappers generated from this document if it comes back.
	Language string `json:"language"`

	// GoModule is the module path from the blueprint's own go.mod, which
	// is what a caller puts in a require directive and imports. GoPackage
	// is the package clause, needed to qualify the config and outputs
	// types at the call site.
	GoModule  string `json:"go_module,omitempty"`
	GoPackage string `json:"go_package,omitempty"`

	// TSEntry is the entry file's own path relative to the blueprint
	// root, which is what a TypeScript caller imports: TypeScript
	// resolves a relative specifier, so there is no module name to name.
	TSEntry string `json:"ts_entry,omitempty"`

	// PyModule is the module name a caller imports, which is the entry
	// file's own basename without .py: Python imports by module name
	// with the blueprint's directory on the path.
	PyModule string `json:"py_module,omitempty"`

	// Function is the exported entrypoint.
	Function string `json:"function"`
	// ConfigType/OutputsType are the types the function takes and
	// returns. Recorded because a caller has to name them to construct a
	// literal, and because the extractor found them by rule rather than
	// by naming convention, so they are not derivable from Function.
	// OutputsType is empty for a blueprint that returns nothing, which
	// is legal and has no outputs.
	ConfigType  string `json:"config_type"`
	OutputsType string `json:"outputs_type,omitempty"`
}

// SchemaParam is one parameter, in declaration order.
//
// There is deliberately no Default field. See Defaults below.
type SchemaParam struct {
	// Name is the language-neutral snake_case name a caller binds an
	// argument to, e.g. the HCL block's own `target_arn = "..."`.
	Name string `json:"name"`
	// SourceName is the identifier as written in the blueprint's own
	// source, e.g. the Go field "TargetARN".
	//
	// Both are carried because neither derives the other. A caller that
	// constructs a config literal, which blueprint_calls and the HCL
	// block both must do, needs the exact identifier, and PascalCasing
	// the snake_case name does not recover it for any name containing an
	// acronym: TargetARN becomes target_arn becomes TargetArn, which
	// does not compile. That breaks on ordinary AWS naming (ARN, ID,
	// URL, KMS, HTTP), so it is the common case rather than an edge.
	SourceName string `json:"source_name"`
	// Type is the existing language-neutral vocabulary, unchanged from
	// the Ubxfile's own params: types (blueprint/spec): string, number,
	// bool, list(string), list(number), cross_ref. "number" means
	// integer.
	Type spec.ParamType `json:"type"`
	// Required is true for a non-pointer Go field, a TypeScript
	// parameter with no default, or a Python parameter with no default.
	Required bool `json:"required"`
}

// SchemaOutput is one returned value. No type field: every output is a
// *sdk.Computed, so it would carry no information.
type SchemaOutput struct {
	// Name is the language-neutral snake_case name.
	Name string `json:"name"`
	// SourceName is the identifier as written, e.g. "QueueURL". Carried
	// for the same reason as SchemaParam.SourceName: the snake_case name
	// does not round-trip back to it.
	SourceName string `json:"source_name"`
}

// Defaults records that default VALUES are not in this document, and
// that their absence is a decision.
//
// Under the Ubxfile a default was declared ("retention: number, default
// 30") and describe_blueprint reported it. Under the config-struct
// convention an optional parameter is a pointer field and its default
// lives in the function body as `if cfg.Retention == nil`, where no
// extractor can see it. That is a real regression, accepted rather than
// paid for with a Defaults() function the extractor would have to
// parse, which is exactly the parse-a-convention requirement the config
// struct was chosen to avoid.
//
// This exists so a reader can tell a deliberate absence from a failed
// extraction, and so describe_blueprint can say "optional, default not
// derivable" instead of an ambiguous "optional".
type Defaults struct {
	Derivable bool   `json:"derivable"`
	Reason    string `json:"reason"`
}

// DefaultsNotDerivable is the only value Defaults ever takes today,
// named rather than spelled out at each construction site so the reason
// stays identical across extractors and across languages.
var DefaultsNotDerivable = Defaults{
	Derivable: false,
	Reason:    "an optional parameter's default value lives in the function body, not in its signature, so no extractor can read it",
}

// Derivation records anything the extractor asserted but could not
// verify from the source it read.
type Derivation struct {
	// Assumptions is empty for Go and Python, which both state `int`
	// outright. TypeScript carries exactly one: this vocabulary's
	// "number" means integer, and TS's own number type admits fractions,
	// so a TS-derived schema asserts int-ness the type cannot express.
	// Recorded rather than fixed with a branded type, which would buy
	// little for a real author.
	Assumptions []string `json:"assumptions"`
}

// TSNumberIsIntAssumption is the one assumption any extractor records
// today.
const TSNumberIsIntAssumption = "number params are integers; TypeScript's number type cannot express this"
