package provider

import (
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"
)

// shipCreate passes the literal JSON "null" as PriorState to mean "this
// does not exist yet". Encoded as an object whose attributes are all
// null, a real provider reads it as a resource that exists, takes its
// update branch, and fails for want of an identifier (UBI-267).
//
// The guard existed on one of the two encoders and not the other, which
// is why they are now one implementation.

func nullStateBlock() Block {
	return Block{Attributes: []Attribute{
		{Name: "queue_name", Type: json.RawMessage(`"string"`)},
		{Name: "queue_url", Type: json.RawMessage(`"string"`), Computed: true},
	}}
}

func TestEncode_TopLevelNullReachesTheWireAsNull(t *testing.T) {
	block := nullStateBlock()
	ty, err := blockObjectType(block)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name   string
		encode func(Block, json.RawMessage) ([]byte, error)
	}{
		{"prior state, a create saying the resource does not exist yet", encodeDynamicValue},
		{"planned state, the executor's own destroy signal", encodeUnknownAwareDynamicValue},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := c.encode(block, json.RawMessage("null"))
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			val, err := ctymsgpack.Unmarshal(out, ty)
			if err != nil {
				t.Fatal(err)
			}
			if !val.IsNull() {
				t.Fatalf("a top-level null became %#v, which a provider reads as a resource that exists", val)
			}
		})
	}
}

// Whitespace around it counts too, since this arrives as raw JSON.
func TestEncode_NullWithWhitespaceIsStillNull(t *testing.T) {
	block := nullStateBlock()
	ty, _ := blockObjectType(block)
	out, err := encodeDynamicValue(block, json.RawMessage("  null\n"))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	val, _ := ctymsgpack.Unmarshal(out, ty)
	if !val.IsNull() {
		t.Errorf("a null with surrounding whitespace was not treated as null: %#v", val)
	}
}

// An empty input is NOT a null. It means an all-defaults document, and
// it has to stay an object or a provider config with nothing set would
// start arriving as "no config at all".
func TestEncode_EmptyInputIsAnObjectNotNull(t *testing.T) {
	block := nullStateBlock()
	ty, _ := blockObjectType(block)
	for _, in := range []json.RawMessage{nil, json.RawMessage(""), json.RawMessage("{}")} {
		out, err := encodeDynamicValue(block, in)
		if err != nil {
			t.Fatalf("encode(%q): %v", in, err)
		}
		val, _ := ctymsgpack.Unmarshal(out, ty)
		if val.IsNull() {
			t.Errorf("empty input %q became a top-level null", in)
		}
	}
}

// The one thing the two encoders are genuinely supposed to disagree
// about, kept: a schema-Computed attribute the config never set reaches
// the wire Unknown from the planned-state side and Null from the prior
// state, because nothing about an existing resource is undecided.
func TestEncode_ComputedUnsetStillDiffersByDirection(t *testing.T) {
	block := nullStateBlock()
	ty, _ := blockObjectType(block)
	in := json.RawMessage(`{"queue_name":"orders"}`)

	planned, err := encodeUnknownAwareDynamicValue(block, in)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := encodeDynamicValue(block, in)
	if err != nil {
		t.Fatal(err)
	}

	pv, _ := ctymsgpack.Unmarshal(planned, ty)
	if got := pv.GetAttr("queue_url"); got.IsKnown() {
		t.Errorf("planned state: an unset computed attribute should be unknown, got %#v", got)
	}
	sv, _ := ctymsgpack.Unmarshal(prior, ty)
	if got := sv.GetAttr("queue_url"); !got.IsNull() {
		t.Errorf("prior state: an unset computed attribute should be null, got %#v", got)
	}
	_ = cty.NilVal
}
