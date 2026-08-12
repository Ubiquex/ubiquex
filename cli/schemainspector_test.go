package cli

import (
	"testing"

	"github.com/ubiquex/ubiquex/provider"
)

// TestAttributeAt_SubPathIntoFlatAttribute_MatchesTheWholeAttribute is
// UBI-143's own real reproduction: kubernetes_secret_v1.data is a real,
// live-schema-confirmed Sensitive map[string]string attribute, but
// `ubx.Secret()` targets one specific key within it (e.g.
// "data.DB_PASSWORD"), a path that continues PAST the flat Attribute's
// own name. attributeAt used to require an exact match with nothing
// left over, so this looked up "data" with a remaining "DB_PASSWORD"
// segment and reported not-found, even though "data" itself is
// genuinely Sensitive -- confirmed via a real `ubx plan --from-code`
// run before this fix ("resolve: $secret placed at an attribute the
// provider schema does not flag Sensitive: kubernetes_secret_v1.data.
// DB_PASSWORD"). A trailing segment past a flat Attribute is always a
// map key or list index the schema doesn't itself describe further
// (ir.go's own typeRefFromCty: a real Attribute's Type is never an
// object needing further descent), so the Attribute's own flags apply
// regardless.
func TestAttributeAt_SubPathIntoFlatAttribute_MatchesTheWholeAttribute(t *testing.T) {
	block := provider.Block{
		Attributes: []provider.Attribute{
			{Name: "data", Optional: true, Computed: true, Sensitive: true},
			{Name: "immutable", Optional: true, Sensitive: false},
		},
	}

	a, ok := attributeAt(block, "data.DB_PASSWORD")
	if !ok {
		t.Fatalf("attributeAt(%q): expected a match on the flat \"data\" attribute, got none", "data.DB_PASSWORD")
	}
	if !a.Sensitive {
		t.Fatalf("attributeAt(%q): expected the matched attribute's own Sensitive=true to survive, got %+v", "data.DB_PASSWORD", a)
	}

	// A deeper sub-path (a list index inside a map value, however
	// unrealistic) must resolve the exact same way -- still the same
	// flat Attribute, still Sensitive.
	a2, ok := attributeAt(block, "data.SOME_KEY.nested")
	if !ok || !a2.Sensitive {
		t.Fatalf("attributeAt(%q): expected the same flat \"data\" attribute match, got ok=%v a=%+v", "data.SOME_KEY.nested", ok, a2)
	}

	// A non-Sensitive flat attribute's own sub-path must still resolve
	// to IT, correctly reporting Sensitive=false, not silently picking
	// up an unrelated attribute's flag.
	a3, ok := attributeAt(block, "immutable.whatever")
	if !ok || a3.Sensitive {
		t.Fatalf("attributeAt(%q): expected a match on \"immutable\" with Sensitive=false, got ok=%v a=%+v", "immutable.whatever", ok, a3)
	}
}

// TestAttributeAt_NestedBlockPath_StillResolvesExactly confirms the fix
// above didn't touch NestedBlock recursion: a path continuing into a
// REAL nested block's own inner attribute must still resolve to that
// inner attribute specifically, not fall back to matching the block's
// own bare name.
func TestAttributeAt_NestedBlockPath_StillResolvesExactly(t *testing.T) {
	block := provider.Block{
		NestedBlocks: []provider.NestedBlock{
			{TypeName: "metadata", Block: provider.Block{
				Attributes: []provider.Attribute{
					{Name: "name", Required: true},
					{Name: "annotations", Optional: true, Sensitive: true},
				},
			}},
		},
	}

	a, ok := attributeAt(block, "metadata.annotations")
	if !ok || !a.Sensitive {
		t.Fatalf("attributeAt(%q): expected metadata's own \"annotations\" attribute, Sensitive=true, got ok=%v a=%+v", "metadata.annotations", ok, a)
	}

	a2, ok := attributeAt(block, "metadata.name")
	if !ok || a2.Sensitive {
		t.Fatalf("attributeAt(%q): expected metadata's own \"name\" attribute, Sensitive=false, got ok=%v a=%+v", "metadata.name", ok, a2)
	}
}

// TestAttributeAt_NestedBlockBareName_NotFound: a NestedBlock's own bare
// name, with nothing left of the path, has no Computed/Sensitive flag
// of its own to report -- unchanged by UBI-143's fix, which only
// affects the flat-Attribute-match branch.
func TestAttributeAt_NestedBlockBareName_NotFound(t *testing.T) {
	block := provider.Block{
		NestedBlocks: []provider.NestedBlock{
			{TypeName: "metadata", Block: provider.Block{
				Attributes: []provider.Attribute{{Name: "name", Required: true}},
			}},
		},
	}

	if _, ok := attributeAt(block, "metadata"); ok {
		t.Fatalf("attributeAt(%q): expected not-found for a bare NestedBlock name", "metadata")
	}
}

// TestAttributeAt_UnknownName_NotFound: an entirely unrecognized
// top-level name is still refused, matching UBI-66's own "never a
// silent pass-through" discipline.
func TestAttributeAt_UnknownName_NotFound(t *testing.T) {
	block := provider.Block{
		Attributes: []provider.Attribute{{Name: "name", Required: true}},
	}

	if _, ok := attributeAt(block, "hallucinated_field"); ok {
		t.Fatalf("attributeAt(%q): expected not-found for an unrecognized name", "hallucinated_field")
	}
}

// TestSchemaInspectorAdapter_IsSensitive_SubPathIntoMapAttribute is the
// same UBI-143 fix, exercised through the real production adapter
// (schemaInspectorAdapter.IsSensitive) rather than attributeAt
// directly -- the actual code path core/resolver's $secret-placement
// gate calls.
func TestSchemaInspectorAdapter_IsSensitive_SubPathIntoMapAttribute(t *testing.T) {
	schemas := &provider.Schemas{
		Resources: map[string]*provider.Schema{
			"kubernetes_secret_v1": {
				Block: provider.Block{
					Attributes: []provider.Attribute{
						{Name: "data", Optional: true, Computed: true, Sensitive: true},
					},
				},
			},
		},
	}
	insp := newSchemaInspector(schemas)

	if !insp.IsSensitive("kubernetes_secret_v1", "data.DB_PASSWORD") {
		t.Fatalf("IsSensitive(kubernetes_secret_v1, data.DB_PASSWORD): expected true, matching the real live hashicorp/kubernetes@2.31.0 schema's own Sensitive=true on \"data\"")
	}
}
