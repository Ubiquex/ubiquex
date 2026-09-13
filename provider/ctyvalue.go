package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"
)

// ErrUnrecognizedConfigKey means a config object carried a key that
// matches no real Attribute or NestedBlock name in the resource's own
// schema (UBI-63 session 2, found live: a model wrote "repository_name"
// for aws_ecr_repository, whose real schema only has "name") -- a hard
// encode-time error instead of silently dropping the key and letting a
// real Required attribute go missing without a trace.
var ErrUnrecognizedConfigKey = errors.New("provider: config key does not match any real schema attribute or nested block")

// ErrRequiredAttributeMissing means a schema attribute flagged Required
// is genuinely absent from config -- never sent to a real provider as an
// explicit null and never silently defaulted, since "Required" is
// exactly the schema's own promise that a real provider needs a real
// value here.
var ErrRequiredAttributeMissing = errors.New("provider: a required attribute is missing from config")

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
	return encodeValueForWire(block, in, true)
}

// encodeValueForWire is the one implementation both of those names call.
//
// They were two implementations until UBI-267, and the cost of that
// arrived exactly where you would expect: a top-level JSON null has to
// reach the wire as a genuine cty.NullVal(ty) rather than an object
// whose attributes all happen to be null, one of them had the guard for
// that, and the other did not. A create passes the literal "null" as its
// PriorState to mean "this does not exist yet"; encoded as an object of
// nulls, a real provider reads it as a resource that exists, takes its
// update branch, and fails for want of an identifier the object does not
// carry.
//
// The original reason for two functions is gone rather than ignored.
// They split (UBI-27) because planned state and config need a
// schema-Computed attribute the config never set to reach the wire as
// Unknown, while a prior state needs it Null, and that distinction now
// lives in encodeBlockValue's own priorState parameter. What was left
// were two shells differing in one boolean and one guard, which is the
// shape that lets a subtle property hold in one place and not the other.
//
// priorState selects between the two documents, and encodeBlockValue's
// own doc comment has the account of why they are not the same thing.
func encodeValueForWire(block Block, in json.RawMessage, priorState bool) ([]byte, error) {
	ty, err := blockObjectType(block)
	if err != nil {
		return nil, err
	}

	// A literal top-level JSON null, whichever direction it came from.
	// For a planned state it is core/executor's own destroy signal; for
	// a prior state it is a create saying the resource does not exist
	// yet. Both need a real cty.NullVal(ty), and the per-attribute path
	// below cannot produce one: it always builds an object, even from a
	// nil generic.
	if bytes.Equal(bytes.TrimSpace(in), []byte("null")) {
		return ctymsgpack.Marshal(cty.NullVal(ty), ty)
	}

	var generic interface{}
	if len(in) > 0 {
		dec := json.NewDecoder(bytes.NewReader(in))
		dec.UseNumber()
		if err := dec.Decode(&generic); err != nil {
			return nil, fmt.Errorf("encode value: decode json: %w", err)
		}
	}
	val, err := encodeBlockValue(block, generic, priorState)
	if err != nil {
		return nil, fmt.Errorf("encode value: %w", err)
	}
	out, err := ctymsgpack.Marshal(val, ty)
	if err != nil {
		return nil, fmt.Errorf("encode value: msgpack: %w", err)
	}
	return out, nil
}

// computedMarkerKey mirrors core/resolver's own "$computed" marker key
// (docs/schema.md's value-encoding conventions). provider and core/resolver
// don't import each other -- this string is the wire-format convention
// both independently conform to, the same pattern provider/redact.go's
// redactedMarkerKey already establishes for "$redacted".
const computedMarkerKey = "$computed"

// isComputedMarker reports whether v -- a decoded-generic JSON value -- is
// exactly {"$computed": {...}}, mirroring core/resolver's own
// isComputedMarker (refs.go).
func isComputedMarker(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	if !ok || len(m) != 1 {
		return false
	}
	_, ok = m[computedMarkerKey]
	return ok
}

// encodeUnknownAwareDynamicValue is encodeDynamicValue's own JSON-path,
// unknown-aware sibling (docs/executor.md's UBI-27 amendment) -- used only
// for constructing a `change` proposal's PlannedState/Config, never for
// ReadResource's currentState/Configure's config (where an attribute
// simply absent from the caller's JSON genuinely means "unknown to ubx,
// ask the provider," and Null is the correct encoding, exactly as
// encodeDynamicValue already does).
//
// A real create's PlannedState needs a wire-level distinction JSON alone
// can never express: "the provider will compute this" (Unknown) versus
// "genuinely absent, leave it null" (Null). ctyjson.Unmarshal always maps
// a missing key to Null; a real SDKv2-vintage provider's Apply only fills
// in a Computed attribute it finds Unknown, never one it finds Null
// (confirmed empirically, provider/apply_live_test.go's own false start).
// Two independent situations both resolve to Unknown here, walking the
// resolved config against the schema's own Block (not just its flattened
// cty.Type, which erases the Computed flag): an explicit
// {"$computed": {...}} marker (a same-batch dependency's not-yet-applied
// output, docs/resolver.md), and any schema-Computed attribute the
// resolved config simply never set at all (a brand-new resource's own
// id/arn/url on a from-scratch create) -- the second case is NOT named in
// docs/executor.md's original amendment text, which only describes the
// marker case; it was found empirically while actually wiring this up
// (core/executor session, UBI-27) and is recorded as a real, load-bearing
// correction to that design, not silently patched in.
//
// Everything else encodes identically to encodeDynamicValue (a present,
// non-marker value decodes exactly like ctyjson.Unmarshal would; a present
// null stays null; only an ABSENT-and-Computed attribute differs).
func encodeUnknownAwareDynamicValue(block Block, in json.RawMessage) ([]byte, error) {
	return encodeValueForWire(block, in, false)
}

// encodeBlockValue builds one object-typed cty.Value for block, driven by
// its own Attributes/NestedBlocks (not just the flattened cty.Type
// blockObjectType produces) -- the only way to know, per attribute,
// whether it's Computed and therefore eligible for the absent-means-Unknown
// treatment above.
//
// UBI-63 session 2's own real gap, found live: this used to silently
// ignore any config key that didn't match a real Attribute/NestedBlock
// name at all (a model/author typo -- "repository_name" instead of the
// real "name", confirmed empirically against hashicorp/aws's own live
// aws_ecr_repository schema, which has no "repository_name" attribute)
// and silently sent an explicit `null` for a Required-but-absent
// attribute (the same typo's other half: the real "name" was never
// supplied at all, so it fell through the `!present` case above as
// though it were legitimately Optional). Both reached a real provider as
// a cryptic, generic rejection ("repositoryName ... must have length
// >= 2") instead of a clear, immediate, ubx-side error naming the exact
// problem. Both are now hard, encode-time errors: an unrecognized config
// key, and a `Required` attribute genuinely absent from config (never
// for one satisfied by a $computed marker -- that's a present key, just
// not yet a concrete value, an entirely different, already-handled
// case above).
// priorState selects between the two things this encoder is asked to
// build, which are not the same document.
//
// A CONFIG is authored, so it gets the authoring checks: a required
// attribute that is absent is the author's mistake, and an absent
// Computed attribute is a value the provider will decide, which reaches
// the wire as unknown.
//
// A PRIOR STATE is a recording of what already exists. Neither check
// belongs on it. An attribute can be legitimately absent from a state
// written before the schema declared it required, and a prior state
// must never carry an unknown at all, since nothing about an existing
// resource is undecided.
//
// This distinction used to be implicit in WHICH encoder each path
// called, and became load-bearing when the prior-state path moved here
// to gain dynamic-attribute support. Caught by the existing alias
// tests, which ship a resource whose prior state does not carry every
// required attribute.
func encodeBlockValue(block Block, generic interface{}, priorState bool) (cty.Value, error) {
	m, _ := generic.(map[string]interface{})
	vals := make(map[string]cty.Value, len(block.Attributes)+len(block.NestedBlocks))

	// knownKeySet (schemakeys.go) is the SAME "what counts as a known key
	// at this level" logic UnknownConfigKeys' resolve-time walk uses
	// (UBI-66) -- one implementation, so the two checks (this one, a
	// fail-fast single-error encode-time backstop; that one, an
	// exhaustive, suggestion-carrying resolve-time check) can never
	// silently diverge on what's recognized.
	known := knownKeySet(block)
	unrecognized := make([]string, 0)
	for k := range m {
		if !known[k] {
			unrecognized = append(unrecognized, k)
		}
	}
	if len(unrecognized) > 0 {
		sort.Strings(unrecognized)
		return cty.NilVal, fmt.Errorf("%w: %s", ErrUnrecognizedConfigKey, strings.Join(unrecognized, ", "))
	}

	for _, a := range block.Attributes {
		aty, err := ctyjson.UnmarshalType(a.Type)
		if err != nil {
			return cty.NilVal, fmt.Errorf("attribute %q: parse type: %w", a.Name, err)
		}
		raw, present := m[a.Name]
		switch {
		case present && isComputedMarker(raw):
			vals[a.Name] = cty.UnknownVal(aty)
		case !present && a.Required && !priorState:
			return cty.NilVal, fmt.Errorf("%w: %q", ErrRequiredAttributeMissing, a.Name)
		case !present && a.Computed && !priorState:
			vals[a.Name] = cty.UnknownVal(aty)
		case !present:
			vals[a.Name] = cty.NullVal(aty)
		default:
			val, err := encodeGenericValue(aty, raw)
			if err != nil {
				return cty.NilVal, fmt.Errorf("attribute %q: %w", a.Name, err)
			}
			vals[a.Name] = val
		}
	}

	for _, nb := range block.NestedBlocks {
		raw, present := m[nb.TypeName]
		val, err := encodeNestedBlockValue(nb, raw, present, priorState)
		if err != nil {
			return cty.NilVal, fmt.Errorf("nested block %q: %w", nb.TypeName, err)
		}
		vals[nb.TypeName] = val
	}

	if len(vals) == 0 {
		return cty.EmptyObjectVal, nil
	}
	return cty.ObjectVal(vals), nil
}

// encodeNestedBlockValue mirrors blockObjectType's own Single/List/Set/Map
// nesting shapes, recursing encodeBlockValue per element. A NestedBlock
// carries no Computed flag of its own (only its inner Attributes do, per
// tfplugin's schema model) -- an absent nested block encodes exactly like
// encodeDynamicValue's existing behavior (empty collection / null single),
// never Unknown at the block level itself.
func encodeNestedBlockValue(nb NestedBlock, raw interface{}, present bool, priorState bool) (cty.Value, error) {
	inner, err := blockObjectType(nb.Block)
	if err != nil {
		return cty.NilVal, err
	}
	switch nb.Nesting {
	case NestingList, NestingSet:
		if !present || raw == nil {
			if nb.Nesting == NestingSet {
				return cty.SetValEmpty(inner), nil
			}
			return cty.ListValEmpty(inner), nil
		}
		arr, ok := raw.([]interface{})
		if !ok {
			// UBI-63 bug 2 (revised, session 2 -- found live a second
			// time against a DIFFERENT block on the same resource type):
			// a bare object is unconditionally accepted as shorthand for
			// a single-element list/set, for ANY List/Set-nested block,
			// regardless of the schema's own declared MaxItems. HCL's
			// own block syntax lets an author write one instance of a
			// List/Set-nested block as a single bare `name { ... }`
			// stanza no matter what MaxItems the schema declares -- the
			// original fix here gated this on `MaxItems == 1`, reasoning
			// (confirmed live against hashicorp/aws's own
			// aws_ecr_repository.image_scanning_configuration, MaxItems=1)
			// that MaxItems==1 was the signal a block was conventionally
			// single. Found live, the same session: this resource's own
			// sibling block, encryption_configuration, is ALSO always
			// authored as a single bare block in practice (AWS ECR only
			// ever has one encryption configuration per repository) but
			// reports MaxItems=0 ("not declared") in the real schema --
			// the provider's own schema simply never bothered declaring
			// the limit its own API already enforces. A bare object is
			// never ambiguous or lossy regardless of MaxItems: it always
			// means exactly one instance, which is schema-legal for
			// every List/Set block (0/unbounded or a declared max always
			// permits at least 1) -- so gating acceptance on a specific
			// MaxItems value was an unnecessarily narrow proxy for "is a
			// bare object legitimate here," refusing the identical,
			// equally-valid convention this live case needed.
			if m, isMap := raw.(map[string]interface{}); isMap {
				arr = []interface{}{m}
			} else {
				return cty.NilVal, fmt.Errorf("array required, got %T", raw)
			}
		}
		vals := make([]cty.Value, 0, len(arr))
		for i, item := range arr {
			im, _ := item.(map[string]interface{})
			v, err := encodeBlockValue(nb.Block, im, priorState)
			if err != nil {
				return cty.NilVal, fmt.Errorf("[%d]: %w", i, err)
			}
			vals = append(vals, v)
		}
		if nb.Nesting == NestingSet {
			if len(vals) == 0 {
				return cty.SetValEmpty(inner), nil
			}
			return cty.SetVal(vals), nil
		}
		if len(vals) == 0 {
			return cty.ListValEmpty(inner), nil
		}
		return cty.ListVal(vals), nil
	case NestingMap:
		if !present || raw == nil {
			return cty.MapValEmpty(inner), nil
		}
		m, ok := raw.(map[string]interface{})
		if !ok {
			return cty.NilVal, fmt.Errorf("object required, got %T", raw)
		}
		vals := make(map[string]cty.Value, len(m))
		for k, item := range m {
			im, _ := item.(map[string]interface{})
			v, err := encodeBlockValue(nb.Block, im, priorState)
			if err != nil {
				return cty.NilVal, fmt.Errorf("%s: %w", k, err)
			}
			vals[k] = v
		}
		if len(vals) == 0 {
			return cty.MapValEmpty(inner), nil
		}
		return cty.MapVal(vals), nil
	default: // Single, Group
		if !present || raw == nil {
			return cty.NullVal(inner), nil
		}
		m, _ := raw.(map[string]interface{})
		return encodeBlockValue(nb.Block, m, priorState)
	}
}

// encodeGenericValue converts a decoded-generic JSON value (json.Number
// for numbers, since callers decode with UseNumber) into a cty.Value of
// type ty, recognizing a {"$computed": {...}} marker at ANY position
// (not just a top-level attribute) -- a marker can sit nested inside a
// list/map-typed attribute's own value (e.g. a "tags" map whose one entry
// is computed). Mirrors go-cty's own cty/json unmarshal.go rules for
// primitive/list/set/map types (object types never arise here: a schema
// Attribute's own Type is always primitive/list/set/map -- object shapes
// only ever come from NestedBlocks, handled by encodeBlockValue/
// encodeNestedBlockValue above -- the object case below exists purely for
// defensiveness, matching ctyjson.Unmarshal's own missing-key-is-null
// rule, never actually exercised by a real provider schema this codebase
// has seen).
func encodeGenericValue(ty cty.Type, raw interface{}) (cty.Value, error) {
	if isComputedMarker(raw) {
		return cty.UnknownVal(ty), nil
	}
	if raw == nil {
		return cty.NullVal(ty), nil
	}
	switch {
	case ty == cty.DynamicPseudoType:
		// A free-form JSON attribute: the schema declares no shape, so
		// the value carries its own. Every JSON-typed attribute in a
		// CloudFormation-derived schema lands here, which is 707
		// attributes across 482 of the 1,724 AWS resources, every IAM
		// policy document among them.
		//
		// The type is inferred from the value rather than taken from the
		// schema, because there is nothing to take. go-cty's msgpack
		// encoder already knows how to put a concretely-typed value on
		// the wire under a dynamic-typed slot: it writes the type
		// alongside the value, which is exactly what a dynamic attribute
		// means on the protocol.
		return impliedDynamicValue(raw)
	case ty.IsPrimitiveType():
		return encodePrimitiveValue(ty, raw)
	case ty.IsListType(), ty.IsSetType():
		arr, ok := raw.([]interface{})
		if !ok {
			return cty.NilVal, fmt.Errorf("array required, got %T", raw)
		}
		ety := ty.ElementType()
		vals := make([]cty.Value, 0, len(arr))
		for i, item := range arr {
			v, err := encodeGenericValue(ety, item)
			if err != nil {
				return cty.NilVal, fmt.Errorf("[%d]: %w", i, err)
			}
			vals = append(vals, v)
		}
		if ty.IsSetType() {
			if len(vals) == 0 {
				return cty.SetValEmpty(ety), nil
			}
			return cty.SetVal(vals), nil
		}
		if len(vals) == 0 {
			return cty.ListValEmpty(ety), nil
		}
		return cty.ListVal(vals), nil
	case ty.IsMapType():
		m, ok := raw.(map[string]interface{})
		if !ok {
			return cty.NilVal, fmt.Errorf("object required, got %T", raw)
		}
		ety := ty.ElementType()
		vals := make(map[string]cty.Value, len(m))
		for k, v := range m {
			val, err := encodeGenericValue(ety, v)
			if err != nil {
				return cty.NilVal, fmt.Errorf("%s: %w", k, err)
			}
			vals[k] = val
		}
		if len(vals) == 0 {
			return cty.MapValEmpty(ety), nil
		}
		return cty.MapVal(vals), nil
	case ty.IsObjectType():
		m, ok := raw.(map[string]interface{})
		if !ok {
			return cty.NilVal, fmt.Errorf("object required, got %T", raw)
		}
		atys := ty.AttributeTypes()
		vals := make(map[string]cty.Value, len(atys))
		for name, aty := range atys {
			v, present := m[name]
			if !present {
				vals[name] = cty.NullVal(aty)
				continue
			}
			val, err := encodeGenericValue(aty, v)
			if err != nil {
				return cty.NilVal, fmt.Errorf("%s: %w", name, err)
			}
			vals[name] = val
		}
		if len(vals) == 0 {
			return cty.EmptyObjectVal, nil
		}
		return cty.ObjectVal(vals), nil
	default:
		return cty.NilVal, fmt.Errorf("unsupported type %s", ty.FriendlyName())
	}
}

// encodePrimitiveValue mirrors go-cty's own cty/json unmarshalPrimitive
// for the three primitive types a real provider schema ever declares.
func encodePrimitiveValue(ty cty.Type, raw interface{}) (cty.Value, error) {
	switch ty {
	case cty.Bool:
		b, ok := raw.(bool)
		if !ok {
			return cty.NilVal, fmt.Errorf("bool required, got %T", raw)
		}
		return cty.BoolVal(b), nil
	case cty.Number:
		switch v := raw.(type) {
		case json.Number:
			val, err := cty.ParseNumberVal(string(v))
			if err != nil {
				return cty.NilVal, err
			}
			return val, nil
		case float64:
			return cty.NumberFloatVal(v), nil
		default:
			return cty.NilVal, fmt.Errorf("number required, got %T", raw)
		}
	case cty.String:
		switch v := raw.(type) {
		case string:
			return cty.StringVal(v), nil
		case json.Number:
			return cty.StringVal(string(v)), nil
		default:
			return cty.NilVal, fmt.Errorf("string required, got %T", raw)
		}
	default:
		return cty.NilVal, fmt.Errorf("unsupported primitive type %s", ty.FriendlyName())
	}
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
		// Marshalled against the value's OWN type, not the schema's.
		//
		// ctyjson.Marshal(val, ty) writes go-cty's type-tagged wrapper
		// for anything sitting under a dynamic-typed slot, so a policy
		// document came back as
		// {"value":{...},"type":["object",{...}]} instead of the JSON
		// the user wrote. That shape would then be what the ledger
		// stored, what `ubx why` printed, and what the next plan
		// compared against the user's own config, which would never
		// match again.
		//
		// This is the quiet half of the same defect. The two encode
		// paths fail loudly; this one succeeds and returns the wrong
		// thing.
		out, err := ctyjson.Marshal(val, val.Type())
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

// impliedDynamicValue builds a concretely-typed cty.Value from a decoded
// generic JSON value, for an attribute the schema declares as dynamic.
//
// Tuple and object rather than list and map, matching go-cty's own
// ctyjson.ImpliedType: a JSON array of mixed element types has no list
// type, and a JSON object's fields have no single element type. A
// free-form JSON value is exactly where those mixtures turn up, so
// choosing list/map would refuse values the attribute exists to carry.
// An IAM policy statement, for one, is an array of objects whose
// "Resource" is sometimes a string and sometimes an array of them.
func impliedDynamicValue(raw interface{}) (cty.Value, error) {
	switch v := raw.(type) {
	case nil:
		return cty.NullVal(cty.DynamicPseudoType), nil
	case bool:
		return cty.BoolVal(v), nil
	case string:
		return cty.StringVal(v), nil
	case json.Number:
		// Through cty's own parser, not float64: a JSON number decoded
		// with UseNumber keeps its exact text, and cty.ParseNumberVal
		// keeps that precision where a float64 round trip would quietly
		// lose it.
		n, err := cty.ParseNumberVal(v.String())
		if err != nil {
			return cty.NilVal, fmt.Errorf("number %q: %w", v.String(), err)
		}
		return n, nil
	case float64:
		// Only reachable from a caller that decoded without UseNumber.
		return cty.NumberFloatVal(v), nil
	case []interface{}:
		if len(v) == 0 {
			return cty.EmptyTupleVal, nil
		}
		vals := make([]cty.Value, 0, len(v))
		for i, item := range v {
			val, err := impliedDynamicValue(item)
			if err != nil {
				return cty.NilVal, fmt.Errorf("[%d]: %w", i, err)
			}
			vals = append(vals, val)
		}
		return cty.TupleVal(vals), nil
	case map[string]interface{}:
		if len(v) == 0 {
			return cty.EmptyObjectVal, nil
		}
		vals := make(map[string]cty.Value, len(v))
		for k, item := range v {
			val, err := impliedDynamicValue(item)
			if err != nil {
				return cty.NilVal, fmt.Errorf("%s: %w", k, err)
			}
			vals[k] = val
		}
		return cty.ObjectVal(vals), nil
	default:
		return cty.NilVal, fmt.Errorf("cannot represent %T as a dynamic value", raw)
	}
}
