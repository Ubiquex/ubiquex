package ir

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ubiquex/ubiquex-cli/provider"
)

// attr is a small test-only constructor -- fixture schemas here are
// fabricated Go literals, no real provider binary needed for this
// package's own tests (docs/sdk.md's own "fake for unit tests, real for
// live verification" split, slice 1).
func attr(name, ctyType string, required, optional, computed, sensitive bool) provider.Attribute {
	return provider.Attribute{
		Name:      name,
		Type:      json.RawMessage(ctyType),
		Required:  required,
		Optional:  optional,
		Computed:  computed,
		Sensitive: sensitive,
	}
}

func TestFromSchema_FlatScalarAttributes(t *testing.T) {
	schema := &provider.Schema{
		Block: provider.Block{
			Attributes: []provider.Attribute{
				attr("id", `"string"`, false, false, true, false),
				attr("instance_class", `"string"`, false, true, false, false),
				attr("allocated_storage", `"number"`, false, true, false, false),
				attr("multi_az", `"bool"`, false, true, false, false),
				attr("master_password", `"string"`, false, true, false, true),
			},
		},
	}

	rt, err := FromSchema("aws_db_instance", schema)
	if err != nil {
		t.Fatalf("FromSchema: %v", err)
	}
	if rt.WireType != "aws_db_instance" {
		t.Fatalf("WireType = %q, want aws_db_instance", rt.WireType)
	}
	if len(rt.Fields) != 5 {
		t.Fatalf("len(Fields) = %d, want 5", len(rt.Fields))
	}

	id := rt.Fields[0]
	if id.WireName != "id" || !id.Computed || id.Required || id.Optional || id.Sensitive {
		t.Fatalf("id field = %+v, want computed-only", id)
	}
	if id.Type.Kind != KindScalar || id.Type.Scalar != ScalarString {
		t.Fatalf("id.Type = %+v, want scalar string", id.Type)
	}

	storage := rt.Fields[2]
	if storage.WireName != "allocated_storage" || storage.Type.Kind != KindScalar || storage.Type.Scalar != ScalarNumber {
		t.Fatalf("allocated_storage = %+v, want optional scalar number", storage)
	}

	pw := rt.Fields[4]
	if !pw.Sensitive {
		t.Fatalf("master_password.Sensitive = false, want true")
	}
}

func TestFromSchema_ListSetMapOfScalar(t *testing.T) {
	schema := &provider.Schema{
		Block: provider.Block{
			Attributes: []provider.Attribute{
				attr("security_group_ids", `["list","string"]`, false, true, false, false),
				attr("availability_zones", `["set","string"]`, false, true, false, false),
				attr("tags", `["map","string"]`, false, true, false, false),
			},
		},
	}

	rt, err := FromSchema("aws_db_instance", schema)
	if err != nil {
		t.Fatalf("FromSchema: %v", err)
	}

	cases := []struct {
		idx  int
		kind TypeKind
	}{
		{0, KindList},
		{1, KindSet},
		{2, KindMap},
	}
	for _, c := range cases {
		f := rt.Fields[c.idx]
		if f.Type.Kind != c.kind {
			t.Fatalf("field %d (%s).Type.Kind = %v, want %v", c.idx, f.WireName, f.Type.Kind, c.kind)
		}
		if f.Type.Element == nil || f.Type.Element.Kind != KindScalar || f.Type.Element.Scalar != ScalarString {
			t.Fatalf("field %d (%s).Type.Element = %+v, want scalar string", c.idx, f.WireName, f.Type.Element)
		}
	}
}

func TestFromSchema_NestedBlock_SingleGroupListSetMap(t *testing.T) {
	innerBlock := provider.Block{
		Attributes: []provider.Attribute{
			attr("enabled", `"bool"`, false, true, false, false),
		},
	}

	for _, tc := range []struct {
		name    string
		nesting provider.NestingMode
		want    TypeKind
	}{
		{"single_nested", provider.NestingSingle, KindObject},
		{"group_nested", provider.NestingGroup, KindObject},
		{"list_nested", provider.NestingList, KindList},
		{"set_nested", provider.NestingSet, KindSet},
		{"map_nested", provider.NestingMap, KindMap},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := &provider.Schema{
				Block: provider.Block{
					NestedBlocks: []provider.NestedBlock{
						{TypeName: "settings", Nesting: tc.nesting, Block: innerBlock},
					},
				},
			}
			rt, err := FromSchema("aws_thing", schema)
			if err != nil {
				t.Fatalf("FromSchema: %v", err)
			}
			if len(rt.Fields) != 1 {
				t.Fatalf("len(Fields) = %d, want 1", len(rt.Fields))
			}
			f := rt.Fields[0]
			if f.WireName != "settings" {
				t.Fatalf("WireName = %q, want settings", f.WireName)
			}
			// A NestedBlock-derived field carries none of
			// Required/Optional/Computed/Sensitive itself -- only its own
			// inner Attributes do (real schema fact, see ir.go's own
			// Field doc comment).
			if f.Required || f.Optional || f.Computed || f.Sensitive {
				t.Fatalf("nested-block field = %+v, want all four flags false", f)
			}

			var obj *TypeRef
			switch tc.want {
			case KindObject:
				if f.Type.Kind != KindObject {
					t.Fatalf("Type.Kind = %v, want KindObject", f.Type.Kind)
				}
				obj = &f.Type
			default:
				if f.Type.Kind != tc.want {
					t.Fatalf("Type.Kind = %v, want %v", f.Type.Kind, tc.want)
				}
				if f.Type.Element == nil || f.Type.Element.Kind != KindObject {
					t.Fatalf("Type.Element = %+v, want a KindObject element", f.Type.Element)
				}
				obj = f.Type.Element
			}
			if len(obj.Object) != 1 || obj.Object[0].WireName != "enabled" {
				t.Fatalf("nested object fields = %+v, want one field named enabled", obj.Object)
			}
		})
	}
}

func TestFromSchema_DeeplyNestedBlocks_Recurse(t *testing.T) {
	// Mirrors the real, empirically-confirmed shape UBI-23 found nesting
	// as deep as 4 levels (docs/architecture.md's own "Sensitive
	// overrides" section) -- this package's own recursion needs to handle
	// real depth, not just one level.
	schema := &provider.Schema{
		Block: provider.Block{
			NestedBlocks: []provider.NestedBlock{
				{
					TypeName: "connector_profile_config",
					Nesting:  provider.NestingList,
					Block: provider.Block{
						NestedBlocks: []provider.NestedBlock{
							{
								TypeName: "connector_profile_credentials",
								Nesting:  provider.NestingSingle,
								Block: provider.Block{
									NestedBlocks: []provider.NestedBlock{
										{
											TypeName: "slack",
											Nesting:  provider.NestingSingle,
											Block: provider.Block{
												Attributes: []provider.Attribute{
													attr("access_token", `"string"`, false, true, false, true),
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	rt, err := FromSchema("aws_appflow_connector_profile", schema)
	if err != nil {
		t.Fatalf("FromSchema: %v", err)
	}
	// list -> object -> "connector_profile_credentials" object ->
	// "slack" object -> "access_token" scalar.
	level0 := rt.Fields[0]
	if level0.Type.Kind != KindList {
		t.Fatalf("level0.Type.Kind = %v, want KindList", level0.Type.Kind)
	}
	level1 := level0.Type.Element.Object[0]
	if level1.WireName != "connector_profile_credentials" || level1.Type.Kind != KindObject {
		t.Fatalf("level1 = %+v, want connector_profile_credentials object", level1)
	}
	level2 := level1.Type.Object[0]
	if level2.WireName != "slack" || level2.Type.Kind != KindObject {
		t.Fatalf("level2 = %+v, want slack object", level2)
	}
	level3 := level2.Type.Object[0]
	if level3.WireName != "access_token" || !level3.Sensitive || level3.Type.Kind != KindScalar {
		t.Fatalf("level3 = %+v, want sensitive scalar access_token", level3)
	}
}

func TestFromSchema_ObjectTypedAttribute_Defensiveness(t *testing.T) {
	// Real provider schemas never declare an object-typed Attribute
	// directly (provider/ctyvalue.go's own encodeGenericValue comment) --
	// this is the same defensive path that comment describes, exercised
	// here so it's provably correct rather than merely unreachable.
	schema := &provider.Schema{
		Block: provider.Block{
			Attributes: []provider.Attribute{
				attr("weird", `["object",{"b":"string","a":"number"}]`, false, true, false, false),
			},
		},
	}
	rt, err := FromSchema("aws_thing", schema)
	if err != nil {
		t.Fatalf("FromSchema: %v", err)
	}
	weird := rt.Fields[0].Type
	if weird.Kind != KindObject {
		t.Fatalf("Kind = %v, want KindObject", weird.Kind)
	}
	// Sorted -- "a" before "b" -- even though the raw ctyjson type spec
	// above declared "b" first, proving this doesn't depend on encoding
	// order or map iteration.
	if len(weird.Object) != 2 || weird.Object[0].WireName != "a" || weird.Object[1].WireName != "b" {
		t.Fatalf("Object = %+v, want sorted [a, b]", weird.Object)
	}
}

func TestFromSchema_MalformedType_Errors(t *testing.T) {
	schema := &provider.Schema{
		Block: provider.Block{
			Attributes: []provider.Attribute{
				attr("broken", `not-json`, false, true, false, false),
			},
		},
	}
	if _, err := FromSchema("aws_thing", schema); err == nil {
		t.Fatal("FromSchema: got nil error, want a parse error for malformed attribute type")
	}
}

func TestFromSchema_NilSchema_Errors(t *testing.T) {
	if _, err := FromSchema("aws_thing", nil); err == nil {
		t.Fatal("FromSchema(nil): got nil error, want an error")
	}
}

func TestFromSchema_Deterministic_AcrossRepeatedCalls(t *testing.T) {
	// The conformance suite's own byte-identical-after-canonicalization
	// discipline (docs/sdk.md) depends on codegen output never varying
	// run to run for the same real schema -- confirmed directly here,
	// not just assumed from the sorted-keys fix inside typeRefFromCty.
	schema := &provider.Schema{
		Block: provider.Block{
			Attributes: []provider.Attribute{
				attr("weird", `["object",{"z":"string","m":"number","a":"bool"}]`, false, true, false, false),
			},
			NestedBlocks: []provider.NestedBlock{
				{TypeName: "settings", Nesting: provider.NestingList, Block: provider.Block{
					Attributes: []provider.Attribute{attr("enabled", `"bool"`, false, true, false, false)},
				}},
			},
		},
	}

	first, err := FromSchema("aws_thing", schema)
	if err != nil {
		t.Fatalf("FromSchema: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := FromSchema("aws_thing", schema)
		if err != nil {
			t.Fatalf("FromSchema (run %d): %v", i, err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d differs from run 0:\n%+v\nvs\n%+v", i, again, first)
		}
	}
}
