package provider

import (
	"encoding/json"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"
)

// Real provider binaries (both tfplugin5 and tfplugin6) decode Configure
// and ReadResource request payloads as cty-msgpack, not the DynamicValue
// "json" field — confirmed empirically against terraform-provider-aws
// 6.54.0 (a JSON-encoded config produced an immediate EOF diagnostic,
// consistent with an SDKv2-vintage msgpack decoder being handed zero
// bytes). ubx therefore always encodes requests as cty-msgpack and prefers
// msgpack when decoding responses, using go-cty (MIT-licensed, the same
// library the Terraform ecosystem itself uses for this) rather than
// reimplementing cty's msgpack/JSON encoding rules by hand.
//
// Nested blocks matter here, not just attributes: a real provider's own
// object type includes one attribute per nested block type (object/list/
// set/map of the nested block's own object type), and it rejects a config
// whose attribute count doesn't match — confirmed empirically too
// (terraform-provider-aws returned "an object with 35 attributes is
// required (30 given)" until nested blocks were included).

// blockObjectType builds the cty object type implied by a schema block,
// recursively: each attribute becomes an object attribute of its declared
// type, and each nested block becomes an object attribute whose type
// depends on its nesting mode.
func blockObjectType(b Block) (cty.Type, error) {
	atys := make(map[string]cty.Type, len(b.Attributes)+len(b.NestedBlocks))
	for _, a := range b.Attributes {
		ty, err := ctyjson.UnmarshalType(a.Type)
		if err != nil {
			return cty.NilType, fmt.Errorf("attribute %q: parse type: %w", a.Name, err)
		}
		atys[a.Name] = ty
	}
	for _, nb := range b.NestedBlocks {
		inner, err := blockObjectType(nb.Block)
		if err != nil {
			return cty.NilType, fmt.Errorf("nested block %q: %w", nb.TypeName, err)
		}
		switch nb.Nesting {
		case NestingList:
			atys[nb.TypeName] = cty.List(inner)
		case NestingSet:
			atys[nb.TypeName] = cty.Set(inner)
		case NestingMap:
			atys[nb.TypeName] = cty.Map(inner)
		default: // Single, Group
			atys[nb.TypeName] = inner
		}
	}
	return cty.Object(atys), nil
}

// encodeDynamicValue turns an ubx-level JSON object — free to omit any
// attribute, since ctyjson.Unmarshal treats a missing key as null, matching
// the schema's optional/computed semantics — into the cty-msgpack bytes a
// real provider binary expects for a DynamicValue payload.
func encodeDynamicValue(block Block, in json.RawMessage) ([]byte, error) {
	ty, err := blockObjectType(block)
	if err != nil {
		return nil, err
	}
	if len(in) == 0 {
		in = json.RawMessage("{}")
	}
	val, err := ctyjson.Unmarshal(in, ty)
	if err != nil {
		return nil, fmt.Errorf("encode value: %w", err)
	}
	out, err := ctymsgpack.Marshal(val, ty)
	if err != nil {
		return nil, fmt.Errorf("encode value: msgpack: %w", err)
	}
	return out, nil
}

// decodeDynamicValue is encodeDynamicValue's inverse: given a schema block
// and a DynamicValue's raw bytes, produces ubx-level JSON. Prefers the
// msgpack field, since that's what real provider binaries return; falls
// back to the json field for any provider that does populate it.
func decodeDynamicValue(block Block, msgpackBytes, jsonBytes []byte) (json.RawMessage, error) {
	if len(msgpackBytes) > 0 {
		ty, err := blockObjectType(block)
		if err != nil {
			return nil, err
		}
		val, err := ctymsgpack.Unmarshal(msgpackBytes, ty)
		if err != nil {
			return nil, fmt.Errorf("decode value: msgpack: %w", err)
		}
		out, err := ctyjson.Marshal(val, ty)
		if err != nil {
			return nil, fmt.Errorf("decode value: %w", err)
		}
		return json.RawMessage(out), nil
	}
	if len(jsonBytes) > 0 {
		return json.RawMessage(jsonBytes), nil
	}
	return nil, nil
}
