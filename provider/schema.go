package provider

import (
	"encoding/json"

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
type Attribute struct {
	Name      string
	Type      json.RawMessage
	Required  bool
	Optional  bool
	Computed  bool
	Sensitive bool
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

func schemaFromV6(s *tfplugin6.Schema) *Schema {
	if s == nil {
		return nil
	}
	return &Schema{Version: s.Version, Block: blockFromV6(s.Block)}
}

func blockFromV6(b *tfplugin6.Schema_Block) Block {
	if b == nil {
		return Block{}
	}
	out := Block{}
	for _, a := range b.Attributes {
		out.Attributes = append(out.Attributes, Attribute{
			Name:      a.Name,
			Type:      json.RawMessage(a.Type),
			Required:  a.Required,
			Optional:  a.Optional,
			Computed:  a.Computed,
			Sensitive: a.Sensitive,
		})
	}
	for _, nb := range b.BlockTypes {
		out.NestedBlocks = append(out.NestedBlocks, NestedBlock{
			TypeName: nb.TypeName,
			Nesting:  nestingModeFromV6(nb.Nesting),
			Block:    blockFromV6(nb.Block),
			MaxItems: nb.MaxItems,
		})
	}
	return out
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

func schemaMapFromV6(m map[string]*tfplugin6.Schema) map[string]*Schema {
	if m == nil {
		return nil
	}
	out := make(map[string]*Schema, len(m))
	for k, v := range m {
		out[k] = schemaFromV6(v)
	}
	return out
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
			Name:      a.Name,
			Type:      json.RawMessage(a.Type),
			Required:  a.Required,
			Optional:  a.Optional,
			Computed:  a.Computed,
			Sensitive: a.Sensitive,
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
