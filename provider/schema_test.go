package provider

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/ubiquex/ubiquex/provider/tfplugin6"
)

// TestBlockFromV6_NestedTypeAttribute is UBI-112's own real finding
// (hashicorp/kubernetes@3.2.0's kubernetes_validating_admission_policy_v1
// is the first real attribute ubx has ever seen carry NestedType instead of
// Type -- zero AWS/Google attributes across their real full schemas ever
// hit this): a Terraform Plugin Framework "structured"/object attribute
// carries its shape on Schema_Attribute.NestedType, leaving Type empty on
// the wire (the two are mutually exclusive), which previously produced a
// silent empty-JSON parse failure ("EOF") the moment codegen tried to read
// Type directly. blockFromV6 must synthesize the equivalent cty type from
// NestedType instead of erroring.
func TestBlockFromV6_NestedTypeAttribute(t *testing.T) {
	block := &tfplugin6.Schema_Block{
		Attributes: []*tfplugin6.Schema_Attribute{
			{
				Name: "metadata",
				NestedType: &tfplugin6.Schema_Object{
					Nesting: tfplugin6.Schema_Object_SINGLE,
					Attributes: []*tfplugin6.Schema_Attribute{
						{Name: "name", Type: []byte(`"string"`), Required: true},
						{Name: "labels", Type: []byte(`["map","string"]`), Optional: true},
					},
				},
			},
		},
	}

	got, err := blockFromV6(block)
	if err != nil {
		t.Fatalf("blockFromV6: %v", err)
	}
	if len(got.Attributes) != 1 {
		t.Fatalf("got %d attributes, want 1", len(got.Attributes))
	}

	ty, err := ctyjson.UnmarshalType(got.Attributes[0].Type)
	if err != nil {
		t.Fatalf("unmarshal synthesized type: %v", err)
	}
	want := cty.Object(map[string]cty.Type{
		"name":   cty.String,
		"labels": cty.Map(cty.String),
	})
	if !ty.Equals(want) {
		t.Fatalf("synthesized type = %#v, want %#v", ty, want)
	}
}

// TestBlockFromV6_NestedTypeAttribute_Nesting covers the three collection
// nesting modes NestedType shares with NestedBlock's own Nesting field --
// LIST/SET/MAP wrap the object type; SINGLE (and any unrecognized future
// mode) leaves it bare.
func TestBlockFromV6_NestedTypeAttribute_Nesting(t *testing.T) {
	cases := []struct {
		name    string
		nesting tfplugin6.Schema_Object_NestingMode
		want    cty.Type
	}{
		{"single", tfplugin6.Schema_Object_SINGLE, cty.Object(map[string]cty.Type{"x": cty.String})},
		{"list", tfplugin6.Schema_Object_LIST, cty.List(cty.Object(map[string]cty.Type{"x": cty.String}))},
		{"set", tfplugin6.Schema_Object_SET, cty.Set(cty.Object(map[string]cty.Type{"x": cty.String}))},
		{"map", tfplugin6.Schema_Object_MAP, cty.Map(cty.Object(map[string]cty.Type{"x": cty.String}))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			block := &tfplugin6.Schema_Block{
				Attributes: []*tfplugin6.Schema_Attribute{
					{
						Name: "attr",
						NestedType: &tfplugin6.Schema_Object{
							Nesting:    c.nesting,
							Attributes: []*tfplugin6.Schema_Attribute{{Name: "x", Type: []byte(`"string"`)}},
						},
					},
				},
			}
			got, err := blockFromV6(block)
			if err != nil {
				t.Fatalf("blockFromV6: %v", err)
			}
			ty, err := ctyjson.UnmarshalType(got.Attributes[0].Type)
			if err != nil {
				t.Fatalf("unmarshal synthesized type: %v", err)
			}
			if !ty.Equals(c.want) {
				t.Fatalf("synthesized type = %#v, want %#v", ty, c.want)
			}
		})
	}
}

// TestBlockFromV6_NestedTypeAttribute_Recursive mirrors the real depth-5
// shape UBI-112 found live: kubernetes_validating_admission_policy_v1
// .spec.match_constraints.object_selector.label_selector.match_expressions
// .key -- a NestedType attribute whose own children are themselves
// NestedType attributes, exactly the PodSpec-containing-containers
// self-referential risk the ticket named up front.
func TestBlockFromV6_NestedTypeAttribute_Recursive(t *testing.T) {
	block := &tfplugin6.Schema_Block{
		Attributes: []*tfplugin6.Schema_Attribute{
			{
				Name: "spec",
				NestedType: &tfplugin6.Schema_Object{
					Nesting: tfplugin6.Schema_Object_SINGLE,
					Attributes: []*tfplugin6.Schema_Attribute{
						{
							Name: "selector",
							NestedType: &tfplugin6.Schema_Object{
								Nesting:    tfplugin6.Schema_Object_LIST,
								Attributes: []*tfplugin6.Schema_Attribute{{Name: "key", Type: []byte(`"string"`)}},
							},
						},
					},
				},
			},
		},
	}

	got, err := blockFromV6(block)
	if err != nil {
		t.Fatalf("blockFromV6: %v", err)
	}
	ty, err := ctyjson.UnmarshalType(got.Attributes[0].Type)
	if err != nil {
		t.Fatalf("unmarshal synthesized type: %v", err)
	}
	want := cty.Object(map[string]cty.Type{
		"selector": cty.List(cty.Object(map[string]cty.Type{"key": cty.String})),
	})
	if !ty.Equals(want) {
		t.Fatalf("synthesized type = %#v, want %#v", ty, want)
	}
}

// TestBlockFromV6_NestedTypeAttribute_MalformedChildType_Errors confirms a
// malformed child Type still surfaces as a real error rather than being
// silently swallowed while synthesizing the parent object type.
func TestBlockFromV6_NestedTypeAttribute_MalformedChildType_Errors(t *testing.T) {
	block := &tfplugin6.Schema_Block{
		Attributes: []*tfplugin6.Schema_Attribute{
			{
				Name: "attr",
				NestedType: &tfplugin6.Schema_Object{
					Nesting:    tfplugin6.Schema_Object_SINGLE,
					Attributes: []*tfplugin6.Schema_Attribute{{Name: "bad", Type: []byte(``)}},
				},
			},
		},
	}
	if _, err := blockFromV6(block); err == nil {
		t.Fatal("blockFromV6: expected an error for a malformed nested child type, got nil")
	}
}
