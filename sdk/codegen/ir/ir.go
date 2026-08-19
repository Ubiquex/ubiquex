// Package ir is the shared, language-neutral type model docs/sdk.md's own
// "Codegen design" section pins: provider.Schema -> IR, no TS-isms (or
// Go-isms, or Py-isms) baked in anywhere here. FromSchema is the only
// thing that reads a real provider.Schema; everything downstream (a
// per-language template, e.g. sdk/codegen/templates/ts) consumes only
// the types in this file, never provider.Schema directly -- the same
// "one shared core, swappable per-language plugin" shape
// intentprovider.Adapter/core.StateReader/audit/cloudtrail+audit/gcp's
// EventLookup already establish elsewhere in this codebase.
package ir

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/ubiquex/ubiquex/provider"
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
// provider/ctyvalue.go's encodeNestedBlockValue comment). The same is
// true of Description below -- a NestedBlock's own Description
// (provider.NestedBlock.Block.Description, the container's prose, not
// any inner attribute's) has no home in this flat Field shape and is
// not carried here.
//
// Description is the provider's own real, wire-carried attribute prose
// (UBI-152), carried through verbatim from provider.Attribute -- empty
// string when the real provider genuinely didn't set one (confirmed
// live: hashicorp/aws and hashicorp/azurerm both report this empty for
// nearly every real attribute; hashicorp/google and hashicorp/
// kubernetes both populate it richly). Never invented here.
type Field struct {
	WireName    string
	Type        TypeRef
	Description string
	Required    bool
	Optional    bool
	Computed    bool
	Sensitive   bool

	// DescriptionSource records where Description actually came from --
	// the real, visible integrity guarantee the provider-onboarding
	// pipeline's own founder-set requirement asks for ("the label is the
	// integrity guarantee, make it genuinely visible, not a footnote").
	// FromSchema itself always sets this to DescriptionSourceModel when
	// Description is non-empty (the provider's own real, wire-carried
	// prose) and DescriptionSourceNone when it's empty -- FromSchema
	// itself never calls an LLM or invents text; a caller that DOES fill
	// an empty Description in afterward (sdk/codegen/describe, wired
	// from cli/sdk.go, never from this package) is responsible for
	// setting this to DescriptionSourceAIInferred at the same time it
	// sets Description, and leaving it DescriptionSourceNone when that
	// generation step itself abstained.
	DescriptionSource DescriptionSource
}

// DescriptionSource is Field.DescriptionSource's own closed set --
// exactly three real states, never a bare bool, since "has a
// description" and "where did it come from" are two genuinely different
// real questions a reader of generated documentation needs answered
// separately.
type DescriptionSource string

const (
	// DescriptionSourceModel means Description is the real provider's
	// own, wire-carried prose -- FromSchema's own default whenever
	// Description is non-empty.
	DescriptionSourceModel DescriptionSource = "source"
	// DescriptionSourceAIInferred means Description was generated by
	// sdk/codegen/describe because the real provider left it empty --
	// never set by this package itself.
	DescriptionSourceAIInferred DescriptionSource = "ai-inferred"
	// DescriptionSourceNone means there is no description at all --
	// either FromSchema's own default (nothing has tried to fill it in
	// yet) or a real, honest LLM abstention (insufficient signal to say
	// something real) -- both real, legitimate, expected states, not
	// distinguished further here (a caller that cares which one applies
	// already knows, from whether it ran the description-generation step
	// at all).
	DescriptionSourceNone DescriptionSource = ""
)

// ResourceType is one provider resource type's own IR -- WireType is the
// real provider type string (e.g. "aws_db_instance"), not carried by
// provider.Schema itself, so every caller of FromSchema supplies it
// explicitly (the caller already knows it -- it's the map key in
// provider.Schemas.Resources).
type ResourceType struct {
	WireType string
	Fields   []Field

	// SkippedFields records every real, dot-path-named field (or nested
	// block) this resource's own real schema declared but that FromSchema
	// could not safely represent -- never silently dropped without a
	// trace. Real, confirmed finding: Kubernetes' own real
	// CustomResourceDefinition type embeds JSONSchemaProps, its own
	// real, official representation of "an arbitrary JSON Schema as
	// data" -- whose own real property list literally includes "$ref"
	// and "$schema" as genuine field names (JSON Schema's own real
	// keywords, modeled as K8s API fields, not a translation artifact).
	// Every per-language template's own splitWireName already refuses
	// (never guesses/coerces) a wire name outside plain ASCII
	// lowercase+digit+underscore -- this is that same real, deliberate
	// "abstain rather than silently mangle" discipline applied one layer
	// up: skip the individual unrepresentable field, keep generating
	// every other real, valid field on the same resource, and report
	// exactly what was skipped rather than either failing the whole
	// resource type or inventing an unverified rename.
	SkippedFields []string
}

// ServiceAndLocalName splits a real provider wire type name into the
// derived package-boundary UBI-98's per-provider-repo/per-service-package
// restructure uses (e.g. "aws_ecr_repository" -> service "ecr", local
// "repository"; "aws_iam_role" -> "iam"/"role") and the resource's own
// LOCAL wire name within that boundary -- language-neutral, wire-name-
// only, the same "no per-language identifier convention lives here"
// posture as the rest of this package (a template still owns turning
// "repository" into "Repository"/"repository"/whatever its own language
// idiom is).
//
// Deliberately mechanical, not a curated AWS-service taxonomy lookup:
// token[0] is the provider's own wire prefix (whatever it is -- "aws" for
// hashicorp/aws, "google" for hashicorp/google, etc., never hardcoded
// here), token[1] is the service slug, and every remaining token is the
// resource's own local name within that service. This MUST stay a pure
// function of the wire type string alone -- ubx sdk gen is required to
// stay 100% local/offline (docs/sdk.md's own "local, pinned,
// offline-after-generation"), so this cannot ever consult an external,
// network-fetched AWS-service taxonomy (the registry's own per-service
// doc grouping, e.g. -- real, but not available from a provider's own
// GetProviderSchema response, and fetching it would be exactly the
// network dependency ubx sdk gen exists to avoid).
//
// Verified directly against the real, full hashicorp/aws@6.54.0 schema
// (1,682 types) before this derivation was trusted, not assumed clean:
// zero (service, local) collisions across all 1,682 real wire types --
// this scheme is genuinely unambiguous, mechanically. It is NOT, however,
// a faithful reproduction of AWS's own real service taxonomy -- confirmed
// the same way, not assumed: 11 real wire types are bare two-token names
// (e.g. "aws_vpc", "aws_instance", "aws_route", "aws_subnet") with no
// third token to serve as a local name at all (handled below: local name
// falls back to the service token itself, non-empty, still valid), and a
// much larger group -- roughly 130 of the 1,682, dominated by AWS's own
// EC2/VPC "core" resource family (aws_vpc, aws_subnet, aws_instance,
// aws_security_group, aws_route_table, aws_internet_gateway, aws_eip, and
// more) -- carry NO explicit service token in their wire name at all
// (Terraform's own AWS provider predates its later services'
// "aws_<service>_*" naming convention for exactly this resource family),
// so this mechanical split fragments them into many small,
// idiosyncratically-named single/few-type packages (e.g. a package named
// "key" for aws_key_pair alone, "flow" for aws_flow_log alone) rather
// than the one curated "ec2" package a hand-maintained taxonomy (Pulumi's
// own bridge, for one real example) would produce. This is a real,
// deliberate scope boundary for this session, not a silently-dropped
// gap: it does not block either of this restructure's two hard
// requirements (a full-provider `go build` compiling clean -- MORE,
// smaller packages only helps that; and the founder's own locked
// per-package naming scheme, which holds regardless of how the boundary
// lines are drawn) -- but a hand-curated wire-type -> canonical-AWS-
// service exception table, matching real per-service reviewability more
// closely, remains real, separately-scopable follow-up work. See
// STATE.md and the UBI-98 Linear thread for the full account.
// apiVersionSuffixPattern matches a bare Kubernetes/Terraform-style API
// version marker token -- "v1", "v2beta2", "v1alpha1", and so on. Used only
// to detect the UBI-112 finding below; not a Kubernetes-specific check --
// any future provider whose wire names carry the same trailing-version
// convention hits the same fold.
var apiVersionSuffixPattern = regexp.MustCompile(`^v[0-9]+(alpha|beta)?[0-9]*$`)

func ServiceAndLocalName(wireType string) (service, localWireName string, err error) {
	tokens := strings.Split(wireType, "_")
	if len(tokens) < 2 || tokens[0] == "" || tokens[1] == "" {
		return "", "", fmt.Errorf("sdk/codegen/ir: ServiceAndLocalName(%q): wire type must be at least \"<provider>_<service>\"", wireType)
	}
	service = tokens[1]
	switch {
	case len(tokens) == 2:
		localWireName = service
	case len(tokens) == 3 && apiVersionSuffixPattern.MatchString(tokens[2]):
		// UBI-112 (hashicorp/kubernetes@3.2.0): a bare version-marker
		// remainder (e.g. "kubernetes_deployment_v1" -> service
		// "deployment", tokens[2:] just "v1") would otherwise become the
		// ENTIRE local name -- a generated file literally named "v1.go",
		// carrying zero information about which resource it defines.
		// Folding the service token back in ("deployment_v1") costs
		// nothing for providers that never hit this shape (AWS/Google's
		// real full schemas confirmed to have zero wire types ending in a
		// bare version token) and fixes every real instance found across
		// Kubernetes' own 81 real resource types.
		localWireName = service + "_" + tokens[2]
	default:
		localWireName = strings.Join(tokens[2:], "_")
	}
	return service, localWireName, nil
}

// FromSchema translates one real provider resource schema
// (provider.Schema, already-shipped, unchanged) into this package's own
// IR model. wireType is the provider's own type string for this schema.
func FromSchema(wireType string, schema *provider.Schema) (*ResourceType, error) {
	if schema == nil {
		return nil, fmt.Errorf("sdk/codegen/ir: FromSchema(%q): nil schema", wireType)
	}
	fields, skipped, err := blockFields(schema.Block, "")
	if err != nil {
		return nil, fmt.Errorf("sdk/codegen/ir: FromSchema(%q): %w", wireType, err)
	}
	return &ResourceType{WireType: wireType, Fields: fields, SkippedFields: skipped}, nil
}

// blockFields walks one schema block's own Attributes and NestedBlocks
// into IR Fields -- Attributes first, then NestedBlocks, both in the
// schema's own declared order (provider.Block's slices, not a map --
// already deterministic, nothing to sort here; sorting only becomes
// necessary once a cty.Type's own AttributeTypes() map is walked, inside
// typeRefFromCty, below). pathPrefix is this block's own dot-path
// location within the resource (e.g. "spec.versions" for a nested block
// reached that way) -- purely for SkippedFields' own reporting, never
// consulted for translation itself.
func blockFields(b provider.Block, pathPrefix string) ([]Field, []string, error) {
	fields := make([]Field, 0, len(b.Attributes)+len(b.NestedBlocks))
	var skipped []string

	for _, a := range b.Attributes {
		if !isValidWireName(a.Name) {
			skipped = append(skipped, dotPath(pathPrefix, a.Name))
			continue
		}
		cty, err := ctyjson.UnmarshalType(a.Type)
		if err != nil {
			return nil, nil, fmt.Errorf("attribute %q: parse type: %w", a.Name, err)
		}
		ref, innerSkipped, err := typeRefFromCty(cty, dotPath(pathPrefix, a.Name))
		if err != nil {
			return nil, nil, fmt.Errorf("attribute %q: %w", a.Name, err)
		}
		skipped = append(skipped, innerSkipped...)
		fields = append(fields, Field{
			WireName:          a.Name,
			Type:              ref,
			Description:       a.Description,
			Required:          a.Required,
			Optional:          a.Optional,
			Computed:          a.Computed,
			Sensitive:         a.Sensitive,
			DescriptionSource: descriptionSourceFor(a.Description),
		})
	}

	for _, nb := range b.NestedBlocks {
		if !isValidWireName(nb.TypeName) {
			// The whole nested object is unrepresentable without a valid
			// container name, regardless of whether its own inner fields
			// would individually be fine -- reported as one skip, not
			// descended into.
			skipped = append(skipped, dotPath(pathPrefix, nb.TypeName))
			continue
		}
		inner, innerSkipped, err := blockFields(nb.Block, dotPath(pathPrefix, nb.TypeName))
		if err != nil {
			return nil, nil, fmt.Errorf("nested block %q: %w", nb.TypeName, err)
		}
		skipped = append(skipped, innerSkipped...)
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

	return fields, skipped, nil
}

// isValidWireName reports whether name is safe to carry through into a
// generated identifier in every real, supported target language -- the
// identical real character set each language template's own
// splitWireName already enforces (plain ASCII lowercase letters, digits,
// underscore), checked once here so an unrepresentable field is skipped
// before it ever reaches template generation, rather than failing the
// whole resource type there.
//
// Real, structural finding, checkpoint 10: a character-set check alone
// is not sufficient -- a name whose own first non-underscore character
// is a digit produces an INVALID Go/Python identifier once translated,
// even though every individual character in it is individually allowed.
// Confirmed live, GitHub's own real, published "reaction-rollup" schema
// (github_commit_comment.reactions, among others): the real wire
// property "-1" (GitHub's own thumbs-down reaction count) is
// ToSnakeCase'd, upstream in ubx-provider-dynamic, into "_1" (the
// leading "-" becomes "_", identical to every other separator
// character) -- "_1" passes THIS check (both "_" and "1" are
// individually allowed), but pascalCase's own segment-splitting (on
// "_") then treats the leading underscore as an empty first segment,
// silently drops it, and emits a bare "1" as the exported Go field
// name -- a real Go parse failure ("expected '}', found 1"), not caught
// until this field's own PARENT ("reactions") -- itself computed-only
// at the top level -- became reachable for the first time via the new
// Attrs struct (sdk/codegen/templates/go and .../py); Go/Python's own
// prior, settable-fields-only nested-struct collection had never
// visited this real field at all before.
func isValidWireName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
			return false
		}
	}
	// A name that is entirely underscores, or whose first non-underscore
	// character is a digit, has no real, safe identifier to become in
	// any target language -- pascalCase's own leading-underscore-strip
	// (every language template applies the identical rule) would either
	// produce an empty identifier or one starting with a digit.
	trimmed := strings.TrimLeft(name, "_")
	if trimmed == "" || (trimmed[0] >= '0' && trimmed[0] <= '9') {
		return false
	}
	return true
}

// descriptionSourceFor is FromSchema's own default DescriptionSource --
// see Field.DescriptionSource's own doc comment for why this package
// only ever sets Model/None, never AIInferred.
func descriptionSourceFor(description string) DescriptionSource {
	if description == "" {
		return DescriptionSourceNone
	}
	return DescriptionSourceModel
}

// dotPath joins a path prefix and a field name for SkippedFields'
// own reporting -- "" prefix means top-level, no leading dot.
func dotPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
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
// typeRefFromCty also returns skipped -- real field names encountered
// inside the IsObjectType branch, below, that isValidWireName rejects
// (the identical real "$ref"/"$schema" case blockFields' own attribute
// loop already handles, but reachable a SECOND, genuinely different way
// here: a schema-level object appearing as the VALUE of a List/Set/Map
// element or another Object's own attribute, never wrapped in a
// NestedBlock at all -- confirmed live against Kubernetes' own real
// CustomResourceDefinition, whose JSONSchemaProps-shaped fields reach
// this branch, not blockFields' own NestedBlocks one). pathPrefix
// threads the same dot-path SkippedFields reporting blockFields already
// establishes down through this recursion too.
func typeRefFromCty(ty cty.Type, pathPrefix string) (TypeRef, []string, error) {
	switch {
	case ty == cty.String:
		return TypeRef{Kind: KindScalar, Scalar: ScalarString}, nil, nil
	case ty == cty.Number:
		return TypeRef{Kind: KindScalar, Scalar: ScalarNumber}, nil, nil
	case ty == cty.Bool:
		return TypeRef{Kind: KindScalar, Scalar: ScalarBool}, nil, nil
	case ty == cty.DynamicPseudoType:
		return TypeRef{Kind: KindScalar, Scalar: ScalarDynamic}, nil, nil
	case ty.IsListType():
		el, skipped, err := typeRefFromCty(ty.ElementType(), pathPrefix)
		if err != nil {
			return TypeRef{}, nil, err
		}
		return TypeRef{Kind: KindList, Element: &el}, skipped, nil
	case ty.IsSetType():
		el, skipped, err := typeRefFromCty(ty.ElementType(), pathPrefix)
		if err != nil {
			return TypeRef{}, nil, err
		}
		return TypeRef{Kind: KindSet, Element: &el}, skipped, nil
	case ty.IsMapType():
		el, skipped, err := typeRefFromCty(ty.ElementType(), pathPrefix)
		if err != nil {
			return TypeRef{}, nil, err
		}
		return TypeRef{Kind: KindMap, Element: &el}, skipped, nil
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
		var skipped []string
		for _, name := range names {
			if !isValidWireName(name) {
				skipped = append(skipped, dotPath(pathPrefix, name))
				continue
			}
			el, innerSkipped, err := typeRefFromCty(atys[name], dotPath(pathPrefix, name))
			if err != nil {
				return TypeRef{}, nil, err
			}
			skipped = append(skipped, innerSkipped...)
			fields = append(fields, Field{WireName: name, Type: el})
		}
		return TypeRef{Kind: KindObject, Object: fields}, skipped, nil
	default:
		return TypeRef{}, nil, fmt.Errorf("unsupported cty type %s", ty.FriendlyName())
	}
}
