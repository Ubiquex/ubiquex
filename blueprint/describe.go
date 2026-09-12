package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/core"
)

// describe.go is the one place that answers "what is this blueprint,
// and what does it take", for every consumer that used to open the
// Ubxfile itself: describe_blueprint, list_blueprints, the provenance
// walkers, `pull`, `package`, and blueprint_calls' own argument
// binding.
//
// It exists because two models are live at once. A blueprint built the
// old way has an Ubxfile; a blueprint written the new way has a derived
// blueprint.schema.json (docs/blueprint.md, "Blueprint schema") and no
// Ubxfile at all. Centralizing the fallback here is what keeps that
// from becoming the same two-branch conditional copied into six files,
// each free to disagree about which wins.
//
// The schema wins where both exist, because it is derived from the
// function and the Ubxfile is written by hand: when they disagree, the
// derived one is the one that cannot be stale.

// Description is what a consumer needs in order to talk about a
// blueprint without building or running it.
type Description struct {
	// Name is the blueprint's own name, the directory basename
	// everywhere in this package.
	Name string
	// Lang is "go", "ts" or "py". A schema-described blueprint is
	// single-language; an Ubxfile may say "all", and that value is
	// passed through unchanged rather than resolved here.
	Lang string
	// Params and Outputs are in declaration order.
	//
	// A schema-derived Param never carries a Default, because a default
	// value lives in the function body where no extractor can read it
	// (see Defaults). Consumers reporting parameters to a person should
	// say so rather than print an ambiguous "optional": DefaultsKnown
	// is how they tell the two cases apart.
	Params  []Param
	Outputs []Output
	// DefaultsKnown is true only for an Ubxfile-described blueprint,
	// where defaults were declared rather than derived.
	DefaultsKnown bool
	// Schema is the derived schema, set only when one was read. It
	// carries what Param and Output cannot: the source identifiers, the
	// entrypoint, and any recorded assumption.
	Schema *Schema
	// Source is "schema" or "Ubxfile", so a consumer can say which
	// model a blueprint follows rather than leaving a person to guess
	// why a default is missing.
	Source string
}

// IsBlueprintDir reports whether dir is the root of a blueprint, under
// either model.
//
// This is the marker test every walker and every extraction check uses.
// It deliberately does not parse: a directory with a malformed Ubxfile
// or an unreadable schema is still a blueprint, and reporting it as
// broken is far more useful than silently not finding it at all.
func IsBlueprintDir(dir string) bool {
	for _, marker := range []string{SchemaFileName, UbxfileName} {
		if info, err := os.Stat(filepath.Join(dir, marker)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// blueprintMarkers is what a refusal names when neither marker is
// present, so the message says what was actually looked for.
func blueprintMarkers() string {
	return SchemaFileName + " or " + UbxfileName
}

// ReadSchema reads the derived schema from dir, or returns false when
// there is none.
func ReadSchema(dir string) (*Schema, bool, error) {
	data, err := os.ReadFile(filepath.Join(dir, SchemaFileName))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", SchemaFileName, err)
	}
	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, false, fmt.Errorf("parse %s in %s: %w", SchemaFileName, dir, err)
	}
	if s.SchemaVersion != SchemaVersion {
		return nil, false, fmt.Errorf("%s in %s is schema_version %d, this ubx understands %d", SchemaFileName, dir, s.SchemaVersion, SchemaVersion)
	}
	return &s, true, nil
}

// WriteSchema writes the derived schema into dir.
//
// Canonical JSON, like everything else this project hashes: the schema
// sits inside the packaged directory and is covered by the content
// hash, so two extractions of the same source have to produce the same
// bytes.
func WriteSchema(dir string, s *Schema) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode %s: %w", SchemaFileName, err)
	}
	canonical, err := core.CanonicalJSONBytes(raw)
	if err != nil {
		return fmt.Errorf("encode %s: %w", SchemaFileName, err)
	}
	// A trailing newline, so the file is a well-formed text file for
	// every tool that reads the packaged directory. It is part of the
	// hashed content and therefore has to be unconditional.
	canonical = append(canonical, '\n')
	if err := os.WriteFile(filepath.Join(dir, SchemaFileName), canonical, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", SchemaFileName, err)
	}
	return nil
}

// Extract derives the schema for the blueprint in dir, dispatching on
// the language its own files are written in.
func Extract(ctx context.Context, dir, name string) (*Schema, error) {
	lang, err := DetectLanguage(dir)
	if err != nil {
		return nil, err
	}
	switch lang {
	case "go":
		return ExtractGo(dir, name)
	case "ts":
		return ExtractTS(ctx, dir, name)
	case "py":
		return ExtractPy(ctx, dir, name)
	}
	return nil, fmt.Errorf("blueprint: extract %s: unknown language %q", dir, lang)
}

// DetectLanguage reports which language a blueprint's own source is
// written in, by looking at the files present.
//
// A blueprint is single-language under the blueprints-as-code model, so
// more than one is an error rather than a choice to resolve: the
// refusal names what it found instead of picking a winner by some
// precedence nobody wrote down.
func DetectLanguage(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("blueprint: %w", err)
	}
	found := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch n := e.Name(); {
		case strings.HasSuffix(n, ".go"):
			found["go"] = true
		case strings.HasSuffix(n, ".d.ts"):
			// A declaration file describes something else's types and is
			// never where a blueprint's own function lives.
		case strings.HasSuffix(n, ".ts"):
			found["ts"] = true
		case strings.HasSuffix(n, ".py"):
			found["py"] = true
		}
	}
	langs := make([]string, 0, len(found))
	for l := range found {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	switch len(langs) {
	case 1:
		return langs[0], nil
	case 0:
		return "", fmt.Errorf("blueprint: %s has no .go, .ts or .py source", dir)
	default:
		return "", fmt.Errorf("blueprint: %s holds %s source together -- a blueprint is written in one language", dir, strings.Join(langs, " and "))
	}
}

// Describe reads whichever description dir actually carries.
func Describe(dir string) (*Description, error) {
	schema, ok, err := ReadSchema(dir)
	if err != nil {
		return nil, err
	}
	if ok {
		return DescriptionFromSchema(dir, schema), nil
	}

	ubxfile, err := ParseUbxfile(dir)
	if err != nil {
		if !IsBlueprintDir(dir) {
			return nil, fmt.Errorf("%s has no %s", dir, blueprintMarkers())
		}
		return nil, err
	}
	return &Description{
		Name:          filepath.Base(dir),
		Lang:          ubxfile.Lang,
		Params:        ubxfile.Params,
		Outputs:       ubxfile.Outputs,
		DefaultsKnown: true,
		Source:        UbxfileName,
	}, nil
}

// DescriptionFromSchema projects a derived schema onto the shape every
// existing consumer already speaks.
//
// Param.Default stays nil throughout, which is the honest projection: a
// schema carries no default values by construction, and inventing a
// zero value here would make an unset optional indistinguishable from
// one explicitly set to zero, which is the exact bug nil-omission
// exists to prevent.
//
// Output.Target stays empty for the same kind of reason. A target is a
// "<resource-slug>.<attribute>" pair that only the Ubxfile's
// pre-resolved resources document could name; a blueprint that is code
// resolves its own outputs when it runs, so there is nothing to record
// and nothing that needs it. Only codegen reads Target, and codegen is
// what this model removes.
func DescriptionFromSchema(dir string, s *Schema) *Description {
	params := make([]Param, 0, len(s.Params))
	for _, p := range s.Params {
		params = append(params, Param{Name: p.Name, Type: p.Type, Required: p.Required})
	}
	outputs := make([]Output, 0, len(s.Outputs))
	for _, o := range s.Outputs {
		outputs = append(outputs, Output{Name: o.Name})
	}
	name := s.Name
	if name == "" {
		name = filepath.Base(dir)
	}
	return &Description{
		Name:          name,
		Lang:          s.Entrypoint.Language,
		Params:        params,
		Outputs:       outputs,
		DefaultsKnown: false,
		Schema:        s,
		Source:        "schema",
	}
}
