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

// Entrypoint names what to call and how, which is what the synthesized
// calling program a blueprint_calls expansion builds needs to import.
type Entrypoint struct {
	// Language is "go", "ts" or "py". A blueprint is single-language:
	// the multi-language build is gone with the Ubxfile, and comes back
	// later as wrappers generated from this document if it comes back.
	Language string `json:"language"`
	// Package is the Go package name, the TS module specifier, or the
	// Python import root.
	Package string `json:"package"`
	// Function is the exported entrypoint.
	Function string `json:"function"`
	// ConfigType/OutputsType are the struct types the function takes and
	// returns. Recorded because a caller has to name them to construct a
	// literal, and because the extractor found them by rule rather than
	// by convention-of-naming, so they are not derivable from Function.
	ConfigType  string `json:"config_type"`
	OutputsType string `json:"outputs_type"`
}

// SchemaParam is one parameter, in declaration order.
//
// There is deliberately no Default field. See Defaults below.
type SchemaParam struct {
	Name string `json:"name"`
	// Type is the existing language-neutral vocabulary, unchanged from
	// the Ubxfile's own params: types (blueprint/spec): string, number,
	// bool, list(string), list(number), cross_ref. "number" means
	// integer.
	Type spec.ParamType `json:"type"`
	// Required is true for a non-pointer Go field, a TypeScript
	// parameter with no default, or a Python parameter with no default.
	Required bool `json:"required"`
}

// SchemaOutput is one returned value. Only the name: every output is a
// *sdk.Computed, so a type field would carry no information.
type SchemaOutput struct {
	Name string `json:"name"`
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
