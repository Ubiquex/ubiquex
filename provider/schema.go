package provider

import (
	"encoding/json"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/ubiquex/ubiquex/provider/tfplugin5"
	"github.com/ubiquex/ubiquex/provider/tfplugin6"
)

// Schemas is ubx's protocol-agnostic view of a provider's full schema dump,
// translated from whichever wire protocol (tfplugin5 or tfplugin6) the
// launched binary actually speaks.
type Schemas struct {
	Provider    *Schema
	Resources   map[string]*Schema
	DataSources map[string]*Schema
}

// Schema is one resource/data-source/provider schema.
type Schema struct {
	Version int64
	Block   Block
}

// Block is a schema block: flat attributes plus nested blocks (which are
// themselves blocks, recursively). Real provider config decoding treats
// nested blocks as ordinary object/collection-typed attributes of the
// enclosing object — see blockObjectType in ctyvalue.go — so ubx must
// model them, not just flatten to attributes; a provider like AWS rejects
// a config object with the wrong attribute count otherwise.
type Block struct {
	Attributes   []Attribute
	NestedBlocks []NestedBlock
}

// Attribute is one attribute of a schema block. Type is the raw ctyjson
// type spec (e.g. `"string"`, `["list","string"]`) — byte-identical on the
// wire in both tfplugin5 and tfplugin6, so it is carried through as-is.
//
// Description is the provider's own real, wire-carried prose (UBI-152) —
// present in both protocols' Schema_Attribute (proto field 3), but
// genuinely empty for many real providers (confirmed live: 0/15 non-empty
// for hashicorp/aws's aws_iam_role, 0/100 for hashicorp/azurerm's
// azurerm_storage_account) and genuinely populated for others (29/30 for
// hashicorp/google's google_compute_instance, 516/528 recursively for
// hashicorp/kubernetes's kubernetes_deployment_v1). Carried through
// verbatim, empty string when the real provider genuinely didn't set
// one — never a placeholder invented here.
type Attribute struct {
	Name        string
	Type        json.RawMessage
	Description string
	Required    bool
	Optional    bool
	Computed    bool
	Sensitive   bool
}

// NestingMode mirrors tfplugin's Schema.NestedBlock.NestingMode, unified
// across v5 and v6 (identical enum in both).
type NestingMode int

const (
	NestingInvalid NestingMode = iota
	NestingSingle
	NestingList
	NestingSet
	NestingMap
	NestingGroup
)

// NestedBlock is one nested block type within a Block. MaxItems is
// carried through from the wire (tfplugin's own Schema.NestedBlock.max_items
// -- unlike the newer plugin-framework Schema.Object.max_items, which the
// tfplugin6 proto itself documents as "never used in the protocol, and
// have no effect on validation," the classic NestedBlock field is real,
// SDKv2-era metadata, confirmed live against the real hashicorp/aws
// provider). 0 means "not declared" (no real limit, or a provider that
// never bothered setting it -- confirmed live, UBI-63 session 2: this
// same resource's own aws_ecr_repository.encryption_configuration
// reports max_items=0 despite AWS's own real API only ever allowing one,
// so "not declared" genuinely happens even for a conventionally
// single-instance block) -- never treated as "exactly zero items
// allowed." Retained as real, informational schema metadata even though
// ctyvalue.go's own bare-object-acceptance no longer branches on it
// (see encodeNestedBlockValue's own doc comment for why: a bare object
// is unconditionally valid shorthand for one instance regardless of
// MaxItems, not just when MaxItems==1).
type NestedBlock struct {
	TypeName string
	Nesting  NestingMode
	Block    Block
	MaxItems int64
}

// IsAttrComputed reports whether attrName is a top-level attribute of s's
// own Block, and marked Computed there -- satisfies core.AttrComputedFlags
// (UBI-63 session 3) through plain structural typing: core.RunScan already
// threads this same *Schema through as an opaque `any` (core deliberately
// never imports this package, see core/scan.go's own StateReader doc
// comment), so adding this method lets that opaque handle answer core's
// one real question -- "is this attribute the provider's to decide, not
// mine?" -- without core ever needing to know Schema's concrete shape.
func (s *Schema) IsAttrComputed(attrName string) bool {
	if s == nil {
		return false
	}
	for _, a := range s.Block.Attributes {
		if a.Name == attrName {
			return a.Computed
		}
	}
	return false
}

func schemaFromV6(s *tfplugin6.Schema) (*Schema, error) {
	if s == nil {
		return nil, nil
	}
	block, err := blockFromV6(s.Block)
	if err != nil {
		return nil, err
	}
	return &Schema{Version: s.Version, Block: block}, nil
}

func blockFromV6(b *tfplugin6.Schema_Block) (Block, error) {
	if b == nil {
		return Block{}, nil
	}
	out := Block{}
	for _, a := range b.Attributes {
		typeJSON, err := attributeTypeJSONFromV6(a)
		if err != nil {
			return Block{}, fmt.Errorf("attribute %q: %w", a.Name, err)
		}
		out.Attributes = append(out.Attributes, Attribute{
			Name:        a.Name,
			Type:        typeJSON,
			Description: a.Description,
			Required:    a.Required,
			Optional:    a.Optional,
			Computed:    a.Computed,
			Sensitive:   a.Sensitive,
		})
	}
	for _, nb := range b.BlockTypes {
		nested, err := blockFromV6(nb.Block)
		if err != nil {
			return Block{}, fmt.Errorf("block %q: %w", nb.TypeName, err)
		}
		out.NestedBlocks = append(out.NestedBlocks, NestedBlock{
			TypeName: nb.TypeName,
			Nesting:  nestingModeFromV6(nb.Nesting),
			Block:    nested,
			MaxItems: nb.MaxItems,
		})
	}
	return out, nil
}

// attributeTypeJSONFromV6 returns a's ctyjson-encoded type. Most attributes
// carry it directly on the wire (Type); Terraform Plugin Framework-style
// "structured"/object attributes (first real instance found: UBI-112,
// hashicorp/kubernetes@3.2.0's kubernetes_validating_admission_policy_v1 —
// zero AWS/Google attributes ever hit this) carry NestedType instead, and
// leave Type empty — the two are mutually exclusive on the wire. NestedType
// is recursively converted to the equivalent cty object/collection type
// here, at the translation boundary, so every downstream consumer (IR,
// codegen, ctyvalue's own config-encoding) keeps working against a single
// cty.Type shape and never needs to know NestedType existed.
func attributeTypeJSONFromV6(a *tfplugin6.Schema_Attribute) (json.RawMessage, error) {
	if a.NestedType == nil {
		return json.RawMessage(a.Type), nil
	}
	ty, err := nestedObjectCtyTypeFromV6(a.NestedType)
	if err != nil {
		return nil, err
	}
	marshaled, err := ctyjson.MarshalType(ty)
	if err != nil {
		return nil, fmt.Errorf("marshal nested type: %w", err)
	}
	return json.RawMessage(marshaled), nil
}

// nestedObjectCtyTypeFromV6 converts one Schema_Object (itself recursive —
// a nested attribute's own NestedType may nest further, exactly the
// PodSpec-containing-containers-containing-volumeMounts shape UBI-112 named
// as a real risk, confirmed present here at depth 5:
// kubernetes_validating_admission_policy_v1.spec.match_constraints
// .object_selector.label_selector.match_expressions.key) into the
// equivalent cty.Type. Nesting mirrors NestedBlock's own SINGLE/LIST/SET/MAP
// handling: SINGLE is a bare object, the others wrap it in the matching cty
// collection type.
func nestedObjectCtyTypeFromV6(o *tfplugin6.Schema_Object) (cty.Type, error) {
	attrTypes := make(map[string]cty.Type, len(o.Attributes))
	for _, a := range o.Attributes {
		typeJSON, err := attributeTypeJSONFromV6(a)
		if err != nil {
			return cty.NilType, fmt.Errorf("nested attribute %q: %w", a.Name, err)
		}
		ty, err := ctyjson.UnmarshalType(typeJSON)
		if err != nil {
			return cty.NilType, fmt.Errorf("nested attribute %q: parse type: %w", a.Name, err)
		}
		attrTypes[a.Name] = ty
	}
	obj := cty.Object(attrTypes)
	switch o.Nesting {
	case tfplugin6.Schema_Object_LIST:
		return cty.List(obj), nil
	case tfplugin6.Schema_Object_SET:
		return cty.Set(obj), nil
	case tfplugin6.Schema_Object_MAP:
		return cty.Map(obj), nil
	default: // SINGLE, or an unrecognized/future mode -- treat as a bare object
		return obj, nil
	}
}

func nestingModeFromV6(m tfplugin6.Schema_NestedBlock_NestingMode) NestingMode {
	switch m {
	case tfplugin6.Schema_NestedBlock_SINGLE:
		return NestingSingle
	case tfplugin6.Schema_NestedBlock_LIST:
		return NestingList
	case tfplugin6.Schema_NestedBlock_SET:
		return NestingSet
	case tfplugin6.Schema_NestedBlock_MAP:
		return NestingMap
	case tfplugin6.Schema_NestedBlock_GROUP:
		return NestingGroup
	default:
		return NestingInvalid
	}
}

func schemaMapFromV6(m map[string]*tfplugin6.Schema) (map[string]*Schema, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]*Schema, len(m))
	for k, v := range m {
		schema, err := schemaFromV6(v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = schema
	}
	return out, nil
}

func schemaFromV5(s *tfplugin5.Schema) *Schema {
	if s == nil {
		return nil
	}
	return &Schema{Version: s.Version, Block: blockFromV5(s.Block)}
}

func blockFromV5(b *tfplugin5.Schema_Block) Block {
	if b == nil {
		return Block{}
	}
	out := Block{}
	for _, a := range b.Attributes {
		out.Attributes = append(out.Attributes, Attribute{
			Name:        a.Name,
			Type:        json.RawMessage(a.Type),
			Description: a.Description,
			Required:    a.Required,
			Optional:    a.Optional,
			Computed:    a.Computed,
			Sensitive:   a.Sensitive,
		})
	}
	for _, nb := range b.BlockTypes {
		out.NestedBlocks = append(out.NestedBlocks, NestedBlock{
			TypeName: nb.TypeName,
			Nesting:  nestingModeFromV5(nb.Nesting),
			Block:    blockFromV5(nb.Block),
			MaxItems: nb.MaxItems,
		})
	}
	return out
}

func nestingModeFromV5(m tfplugin5.Schema_NestedBlock_NestingMode) NestingMode {
	switch m {
	case tfplugin5.Schema_NestedBlock_SINGLE:
		return NestingSingle
	case tfplugin5.Schema_NestedBlock_LIST:
		return NestingList
	case tfplugin5.Schema_NestedBlock_SET:
		return NestingSet
	case tfplugin5.Schema_NestedBlock_MAP:
		return NestingMap
	case tfplugin5.Schema_NestedBlock_GROUP:
		return NestingGroup
	default:
		return NestingInvalid
	}
}

func schemaMapFromV5(m map[string]*tfplugin5.Schema) map[string]*Schema {
	if m == nil {
		return nil
	}
	out := make(map[string]*Schema, len(m))
	for k, v := range m {
		out[k] = schemaFromV5(v)
	}
	return out
}
