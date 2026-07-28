// Package ir is the shared, language-neutral type model docs/sdk.md's own
// "Codegen design" section pins: provider.Schema -> IR, no TS-isms (or
// Go-isms, or Py-isms) baked in anywhere here. FromSchema is the only
// thing that reads a real provider.Schema; everything downstream (a
// per-language template, e.g. sdk/codegen/templates/ts) consumes only
// the types in this file, never provider.Schema directly -- the same
// "one shared core, swappable per-language plugin" shape
// intentprovider.Adapter/core.StateReader/cloudtrail+gcpaudit's
// EventLookup already establish elsewhere in this codebase.
package ir

import (
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/ubiquex/ubiquex-cli/provider"
)

// TypeKind is a field's own shape, independent of any language's own type
// system.
type TypeKind int

const (
	KindInvalid TypeKind = iota
	KindScalar
	KindList
	KindSet
	KindMap
	KindObject
)

// ScalarKind is KindScalar's own sub-shape -- the three primitive cty
// types a real provider schema ever declares (provider/ctyvalue.go's own
// encodePrimitiveValue comment) plus Dynamic, cty's own "type decided at
// the value, not the schema" escape hatch (rare, but a real provider
// schema can declare it -- defensiveness, not a hypothetical, matching
// this project's standing "verified against real schemas, not assumed"
// discipline).
type ScalarKind int

const (
	ScalarInvalid ScalarKind = iota
	ScalarString
	ScalarNumber
	ScalarBool
	ScalarDynamic
)

// TypeRef is one field's own type, recursively -- List/Set/Map carry
// Element, Object carries Object (its own nested fields, recursive).
type TypeRef struct {
	Kind    TypeKind
	Scalar  ScalarKind
	Element *TypeRef
	Object  []Field
}

// Field is one attribute or nested-block-as-a-field of a ResourceType.
//
// WireName is the ONLY name this package ever carries for a field -- the
// provider's real attribute name (or NestedBlock.TypeName), snake_case,
// verbatim from provider.Attribute.Name. No per-language identifier
// convention (TS's own camelCase) exists anywhere in this package; that
// choice belongs entirely to a template, applied at generation time
// (docs/sdk.md's own "The one rule" -- the emitted intent/v1
// resources[].config a program's resource() call ultimately produces is
// handed straight to the real provider, which expects this exact wire
// name, never a language-idiomatic one).
//
// A field derived from a NestedBlock (rather than a flat Attribute)
// always has Required/Optional/Computed/Sensitive all false -- a
// NestedBlock carries none of these itself in a real provider schema,
// only its own inner Attributes do (provider/schema.go; confirmed by
// provider/ctyvalue.go's encodeNestedBlockValue comment).
type Field struct {
	WireName  string
	Type      TypeRef
	Required  bool
	Optional  bool
	Computed  bool
	Sensitive bool
}

// ResourceType is one provider resource type's own IR -- WireType is the
// real provider type string (e.g. "aws_db_instance"), not carried by
// provider.Schema itself, so every caller of FromSchema supplies it
// explicitly (the caller already knows it -- it's the map key in
// provider.Schemas.Resources).
type ResourceType struct {
	WireType string
	Fields   []Field
}

// FromSchema translates one real provider resource schema
// (provider.Schema, already-shipped, unchanged) into this package's own
// IR model. wireType is the provider's own type string for this schema.
func FromSchema(wireType string, schema *provider.Schema) (*ResourceType, error) {
	if schema == nil {
		return nil, fmt.Errorf("sdk/codegen/ir: FromSchema(%q): nil schema", wireType)
	}
	fields, err := blockFields(schema.Block)
	if err != nil {
		return nil, fmt.Errorf("sdk/codegen/ir: FromSchema(%q): %w", wireType, err)
	}
	return &ResourceType{WireType: wireType, Fields: fields}, nil
}

// blockFields walks one schema block's own Attributes and NestedBlocks
// into IR Fields -- Attributes first, then NestedBlocks, both in the
// schema's own declared order (provider.Block's slices, not a map --
// already deterministic, nothing to sort here; sorting only becomes
// necessary once a cty.Type's own AttributeTypes() map is walked, inside
// typeRefFromCty, below).
func blockFields(b provider.Block) ([]Field, error) {
	fields := make([]Field, 0, len(b.Attributes)+len(b.NestedBlocks))

	for _, a := range b.Attributes {
		cty, err := ctyjson.UnmarshalType(a.Type)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: parse type: %w", a.Name, err)
		}
		ref, err := typeRefFromCty(cty)
		if err != nil {
			return nil, fmt.Errorf("attribute %q: %w", a.Name, err)
		}
		fields = append(fields, Field{
			WireName:  a.Name,
			Type:      ref,
			Required:  a.Required,
			Optional:  a.Optional,
			Computed:  a.Computed,
			Sensitive: a.Sensitive,
		})
	}

	for _, nb := range b.NestedBlocks {
		inner, err := blockFields(nb.Block)
		if err != nil {
			return nil, fmt.Errorf("nested block %q: %w", nb.TypeName, err)
		}
		object := TypeRef{Kind: KindObject, Object: inner}
		var ref TypeRef
		switch nb.Nesting {
		case provider.NestingList:
			ref = TypeRef{Kind: KindList, Element: &object}
		case provider.NestingSet:
			ref = TypeRef{Kind: KindSet, Element: &object}
		case provider.NestingMap:
			ref = TypeRef{Kind: KindMap, Element: &object}
		default: // Single, Group -- both surface as a bare object field,
			// exactly matching provider/ctyvalue.go's own blockObjectType
			// switch (the same two nesting modes fall through to "inner"
			// unwrapped there, for the same real-schema reason).
			ref = object
		}
		fields = append(fields, Field{WireName: nb.TypeName, Type: ref})
	}

	return fields, nil
}

// typeRefFromCty converts a parsed cty.Type (already verified against a
// real provider schema by ctyjson.UnmarshalType -- reused, never
// hand-reimplemented) into this package's own TypeRef, recursively.
//
// The IsObjectType case is real, checked defensiveness, not a
// hypothetical: provider/ctyvalue.go's own encodeGenericValue comment
// notes a schema Attribute's Type is always primitive/list/set/map in
// every real provider schema this codebase has seen (object shapes only
// ever arise from NestedBlocks, handled separately in blockFields,
// above) -- this branch exists so a future protocol version or an
// unusual provider that DOES declare an object-typed attribute directly
// degrades to a correct IR translation instead of an opaque error,
// mirroring encodeGenericValue's own "defensiveness, never actually
// exercised" posture exactly.
func typeRefFromCty(ty cty.Type) (TypeRef, error) {
	switch {
	case ty == cty.String:
		return TypeRef{Kind: KindScalar, Scalar: ScalarString}, nil
	case ty == cty.Number:
		return TypeRef{Kind: KindScalar, Scalar: ScalarNumber}, nil
	case ty == cty.Bool:
		return TypeRef{Kind: KindScalar, Scalar: ScalarBool}, nil
	case ty == cty.DynamicPseudoType:
		return TypeRef{Kind: KindScalar, Scalar: ScalarDynamic}, nil
	case ty.IsListType():
		el, err := typeRefFromCty(ty.ElementType())
		if err != nil {
			return TypeRef{}, err
		}
		return TypeRef{Kind: KindList, Element: &el}, nil
	case ty.IsSetType():
		el, err := typeRefFromCty(ty.ElementType())
		if err != nil {
			return TypeRef{}, err
		}
		return TypeRef{Kind: KindSet, Element: &el}, nil
	case ty.IsMapType():
		el, err := typeRefFromCty(ty.ElementType())
		if err != nil {
			return TypeRef{}, err
		}
		return TypeRef{Kind: KindMap, Element: &el}, nil
	case ty.IsObjectType():
		atys := ty.AttributeTypes()
		names := make([]string, 0, len(atys))
		for name := range atys {
			names = append(names, name)
		}
		// Determinism is a feature (CLAUDE.md's own standing rule):
		// cty.Type.AttributeTypes() returns a Go map, so its keys are
		// walked in sorted order here -- codegen output (and the
		// conformance suite's own byte-identical comparison, docs/sdk.md)
		// must never depend on map iteration order, the same discipline
		// cli/providerpool.go's sortedProviderSources already applies.
		sort.Strings(names)
		fields := make([]Field, 0, len(names))
		for _, name := range names {
			el, err := typeRefFromCty(atys[name])
			if err != nil {
				return TypeRef{}, err
			}
			fields = append(fields, Field{WireName: name, Type: el})
		}
		return TypeRef{Kind: KindObject, Object: fields}, nil
	default:
		return TypeRef{}, fmt.Errorf("unsupported cty type %s", ty.FriendlyName())
	}
}
