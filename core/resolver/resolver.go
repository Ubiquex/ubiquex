// Package resolver implements docs/resolver.md's contract: a hand-written,
// machine-shaped ubx:intent/v1 file, live ledger state, and a provider
// schema resolve into a typed, hashed kind:"change" proposal
// (docs/schema.md's own amendment) -- creates, modifies, and (2026-07-17,
// UBI-30) destroys, explicit intent only, orphan-protected. This package
// stays provider-import-free the same way core/executor does:
// SchemaInspector stands in for a concrete *provider.Schema, with the real
// adapter living in cli, not here.
package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// IntentFileKind is the only legal IntentFile.Kind value for v1.
const IntentFileKind = "ubx:intent/v1"

// SchemaInspector is core/resolver's own minimal view of "something that
// knows a provider's schema" -- exactly the three questions resolving a
// change proposal ever needs answered, never the concrete *provider.Schema
// itself (docs/resolver.md — "Schema boundary"). A concrete adapter lives
// in cli, alongside stateReaderAdapter; hermetic tests here use a small
// fake.
type SchemaInspector interface {
	HasType(typeName string) bool
	IsComputed(typeName, attrPath string) bool
	IsSensitive(typeName, attrPath string) bool

	// UnknownConfigKeys reports every config key -- checked recursively
	// through nested blocks, the identical recognition rules a real
	// provider encode enforces at ship time -- that matches no real
	// attribute or nested-block name in typeName's own schema (UBI-66:
	// found live, a model drafted repository_name/role_name/
	// assume_role_policy_document/queue_name, none of them real). config
	// is a resource's raw, not-yet-resolved top-level config object; nil
	// or a non-object config reports no issues here -- resolveOnce's own
	// later "config must be a JSON object" check is where that shape
	// mismatch is caught instead, this method is never asked to make
	// that call itself.
	UnknownConfigKeys(typeName string, config map[string]interface{}) []ConfigKeyIssue

	// MissingRequiredKeys reports every attribute typeName's own schema
	// marks Required with no value present anywhere in config -- checked
	// recursively through nested blocks, the identical presence rule a
	// real provider encode already enforces at ship time (UBI-90: found
	// live, a diagram-authored aws_iam_role/aws_ecr_repository -- the
	// diagram medium's own topology-only design structurally can't
	// express a non-topology attribute like assume_role_policy/name at
	// all -- shipped with the gap silently present, caught only by a real
	// ApplyResourceChange rejection partway through a real ship, after
	// other resources in the same batch had already applied). config is a
	// resource's raw, not-yet-resolved top-level config object; nil or a
	// non-object config reports no issues here, same "later 'config must
	// be a JSON object' check catches that shape mismatch instead"
	// convention UnknownConfigKeys' own doc comment already states.
	MissingRequiredKeys(typeName string, config map[string]interface{}) []RequiredAttributeIssue
}

// ConfigKeyIssue is one SchemaInspector.UnknownConfigKeys finding -- a
// config key that matches no real attribute or nested-block name at its
// own level in the resource type's schema, paired with the closest
// same-level real key name when one is close enough to be worth
// suggesting (empty otherwise). UBI-66.
type ConfigKeyIssue struct {
	// Path is the dot-notation path to the offending key, from the
	// resource's own top-level config (e.g.
	// "encryption_configuration.repository_name" for a key nested one
	// level inside a NestedBlock).
	Path string
	// Suggestion is the closest real key at the same level, "" if none
	// is close enough.
	Suggestion string
}

// RequiredAttributeIssue is one SchemaInspector.MissingRequiredKeys
// finding -- a schema-Required attribute with no value present anywhere
// in a resource's drafted config. UBI-90. No Suggestion field: unlike an
// unrecognized key (which might be a typo of a real one), there is
// nothing to suggest for a value that's simply, genuinely absent.
type RequiredAttributeIssue struct {
	// Path is the dot-notation path to the missing attribute, matching
	// ConfigKeyIssue's own Path convention.
	Path string
}

// DeclaredProvider is one provider a stack declares (docs/architecture.md
// §Multi-provider stacks: the `providers` config map, source → pinned
// version) paired with its own SchemaInspector -- docs/resolver.md's own
// "Amendment: multi-provider stacks — type→provider inference". Resolve
// asks every DeclaredProvider's own Schema.HasType, never guessing a
// type's owner from its name. A single-provider stack (today's
// --provider/--source path, docs/resolver.md's own staged retirement
// plan) is simply a one-element slice -- there is no separate
// single-provider code path in this package anymore, only a set of size
// one.
type DeclaredProvider struct {
	Source  string
	Version string
	Schema  SchemaInspector

	// Reader and ProviderConfig were added 2026-08-25 (docs/schema.md's
	// own "Amendment: data sources", UBI-178) -- both nil for every
	// DeclaredProvider before this and for the overwhelming majority
	// after it: Resolve has never needed a live connection (only ever a
	// schema snapshot, fetched once by the caller and the connection
	// closed immediately -- see cli's own loadResolveProviders), and
	// still doesn't for a stack with no data_sources[]. Populated only
	// by a caller resolving a stack that DOES declare one, for whichever
	// declared provider(s) actually own a referenced data source type --
	// core.StateReader (not the wider executor.Applier a real caller's
	// own provider pool hands back; Reader is declared against the
	// narrower read-only interface deliberately, so this package can
	// never accidentally apply anything, and never needs to import
	// core/executor to accept the value at all -- Go's structural typing
	// lets an Applier satisfy this field directly). See
	// resolveDataSources' own doc comment for how these get used, and
	// dataSourceReadCache's own for why a real, live read only ever
	// happens once per Resolve call despite DoubleRun calling resolveOnce
	// twice.
	Reader         core.StateReader
	ProviderConfig json.RawMessage
}

// schemaComputedAdapter curries a SchemaInspector to one resource type,
// adapting it to core.FilterNormalizationNoise's own AttrComputedFlags
// (single-arg IsAttrComputed(attrName)) via a plain duck-typed method --
// core.FilterNormalizationNoise takes resourceSchema as `any` and type-
// asserts it itself, so this package never needs to import core's
// interface to satisfy it (UBI-88).
type schemaComputedAdapter struct {
	schema   SchemaInspector
	typeName string
}

func (a schemaComputedAdapter) IsAttrComputed(attrName string) bool {
	return a.schema.IsComputed(a.typeName, attrName)
}

// ProviderHint is ResourceIntent's own narrow escape hatch (docs/schema.md's
// "Amendment: the provider field returns", UBI-43): consulted ONLY to
// break a genuine type-ownership ambiguity between two or more declared
// providers that both claim the same type. Absent in every ordinary case.
// Source must name one of the stack's own declared providers, AND that
// provider's own schema must genuinely claim the type (HasType true) --
// the hint selects WHICH declared owner to use among real owners, it
// never routes a type to a provider that doesn't actually own it.
type ProviderHint struct {
	Source string `json:"source"`
}

// InferProvider implements docs/resolver.md's own type→provider inference:
// ask every declared provider's own schema whether it owns typeName.
// Exactly one match wins outright, no friction. Zero matches is
// ErrUnknownType, naming every provider checked -- the same sentinel a
// single-provider resolve has always used for "this type doesn't exist,"
// since "no declared provider owns it" and "the one provider doesn't
// recognize it" are the identical claim once every resolve goes through a
// provider set of at least one. More than one match is ErrAmbiguousType
// unless hint names one of the actual owners (ErrProviderHintUnknown if
// hint's source isn't declared at all; ErrProviderHintDoesNotOwnType if
// it's declared but doesn't genuinely own typeName -- the hint can only
// select among real owners, never manufacture ownership).
//
// Exported (2026-07-18, UBI-43 session 5) for `ubx status --drift`/
// `ubx scan --all`'s own multi-provider fleet-grouping (cli/status.go,
// cli/scanall.go): a legacy/adopted Fleet entry with no recorded provider
// of its own needs its provider inferred fresh by type, the identical
// mechanism a brand-new resource's own resolve already uses -- one
// mechanism, reused, not a second one invented in cli/.
func InferProvider(providers []DeclaredProvider, typeName string, hint *ProviderHint) (DeclaredProvider, error) {
	var owners []DeclaredProvider
	for _, p := range providers {
		if p.Schema.HasType(typeName) {
			owners = append(owners, p)
		}
	}
	switch len(owners) {
	case 0:
		checked := make([]string, 0, len(providers))
		for _, p := range providers {
			checked = append(checked, p.Source)
		}
		return DeclaredProvider{}, fmt.Errorf("%w: %q -- checked: %s", ErrUnknownType, typeName, strings.Join(checked, ", "))
	case 1:
		return owners[0], nil
	default:
		if hint == nil {
			names := make([]string, 0, len(owners))
			for _, o := range owners {
				names = append(names, o.Source)
			}
			return DeclaredProvider{}, fmt.Errorf("%w: %q -- owned by: %s", ErrAmbiguousType, typeName, strings.Join(names, ", "))
		}
		declared := false
		for _, p := range providers {
			if p.Source == hint.Source {
				declared = true
				break
			}
		}
		if !declared {
			return DeclaredProvider{}, fmt.Errorf("%w: %q", ErrProviderHintUnknown, hint.Source)
		}
		for _, o := range owners {
			if o.Source == hint.Source {
				return o, nil
			}
		}
		return DeclaredProvider{}, fmt.Errorf("%w: %q does not own type %q", ErrProviderHintDoesNotOwnType, hint.Source, typeName)
	}
}

// IntentFile is the ubx:intent/v1 wire format (docs/schema.md's
// "Amendment: intent files and resolved change proposals", UBI-27) --
// deliberately machine-shaped, not for hand-typing in production.
//
// Destroys was added 2026-07-17 (docs/schema.md/docs/resolver.md --
// "Amendment: destroys", UBI-30): a dedicated list of canonical address
// strings (Address.String() form, the same convention $ref's own "to"
// field and Modification.Target already use) naming resources to remove --
// deliberately a sibling to Resources, never an "op": "destroy" value on a
// ResourceIntent (docs/resolver.md's own reasoning: a destroy has no
// config to submit). Never inferred from a resource's absence from
// Resources, now or ever -- a permanent boundary, not a v1 scope line.
type IntentFile struct {
	SchemaVersion int64            `json:"schema_version"`
	Kind          string           `json:"kind"`
	Stack         string           `json:"stack"`
	Intent        core.Intent      `json:"intent"`
	Resources     []ResourceIntent `json:"resources"`
	// DataSources was added 2026-08-25 (docs/schema.md's own "Amendment:
	// data sources", UBI-178): a sibling array to Resources, never a
	// variant of it -- see DataSourceIntent's own doc comment for the
	// full account. Purely additive and optional; every intent file
	// produced before this amendment leaves it nil and is completely
	// unaffected -- resolveOnce's own data-source pass is a no-op when
	// this is empty.
	DataSources []DataSourceIntent `json:"data_sources,omitempty"`
	Destroys    []string           `json:"destroys,omitempty"`

	// BlueprintCalls was added 2026-08-04 (UBI-74 Slice 5): one entry per
	// blueprint invocation this document names -- a diagram's own
	// ubx_blueprint-classed node (diagram/parse.go), or an md draft's own
	// "Use blueprint X with..." recognition (intentprovider/validate.go),
	// both compile down to this SAME shape. An SDK program's own direct
	// function call to a blueprint package (UBI-74 Slice 2) never
	// produces one -- the call already happened in-process by the time
	// its own intent/v1 is emitted, so there is nothing left to expand.
	//
	// Purely additive and optional, matching DependsOn's own precedent
	// (above): every OTHER producer of an intent file (a hand-written
	// JSON file, ubx blueprint build's own draft, an SDK program's own
	// evaluated output) leaves this nil and is completely unaffected.
	// blueprint.ExpandCalls (the blueprint package -- core stays
	// dependency-free of it, the same "core never imports a leaf
	// package" discipline every other StateReader/EventLookup-shaped
	// seam in this codebase already follows) expands every entry here
	// into real Resources BEFORE Resolve (below) ever sees them --
	// resolveOnce hard-refuses to run against a document that still
	// carries an unexpanded entry, rather than silently ignoring it.
	BlueprintCalls []BlueprintCall `json:"blueprint_calls,omitempty"`

	// Overrides was added 2026-08-06 (UBI-86 Part 2, design comment on
	// UBI-86 2026-08-04): one entry per caller-declared attribute patch
	// against an already-resolved resource, by address -- the answer to
	// "a blueprint's own internal resource attributes are owned by the
	// blueprint's AUTHOR, not the caller; only call-site parameters are
	// editable" (the design comment's own framing). An SDK program's
	// direct `override(address, {...})` call (Go/TS/Python, zero AI), a
	// diagram's own ubx_override-classed node (structural read, zero
	// AI), or an md draft's "Override the X's Y to Z" prose (the ONLY
	// call site needing AI, and only a thin mapping step -- never
	// re-drafting the blueprint's own resources) all compile down to
	// this SAME shape, mirroring BlueprintCalls' own "three mediums, one
	// wire shape" precedent exactly.
	//
	// Purely additive and optional, matching BlueprintCalls' own
	// precedent: every OTHER producer of an intent file leaves this nil
	// and is completely unaffected. blueprint.ApplyOverrides expands
	// every entry here into a patch against its own target
	// ResourceIntent's Config BEFORE Resolve (below) ever sees them --
	// applied immediately after blueprint.ExpandCalls (so an override
	// can target a resource a blueprint call just produced), before ship
	// (docs/blueprint.md's own "Override mechanism" section). Unlike
	// BlueprintCalls, resolveOnce does NOT hard-refuse an unexpanded
	// entry left here -- a hand-written intent file naming an override
	// against one of its OWN plain resources is legitimate even without
	// ever going through blueprint.ApplyOverrides, so nothing here forces
	// every caller through that one function the way ExpandCalls is
	// forced.
	Overrides []Override `json:"overrides,omitempty"`
}

// Override is one caller-declared attribute patch against an
// already-named resource, applied by address -- see IntentFile.
// Overrides's own doc comment for the full account. Address is the
// canonical "<stack>.<type>.<name>" form (core.Address.String()), always
// within the SAME document's own Stack -- an override cannot reach into
// a different stack (core.Address has no cross-stack concept at all;
// docs/architecture.md's own stack-isolation boundary applies here
// unchanged). Config maps a TOP-LEVEL wire attribute name (a bare key,
// never a dot-path into a nested value) to its own new raw JSON value --
// merged into the target resource's existing Config by key, replacing
// whatever was there, never a deep merge: overriding one field of a
// nested object (e.g. one key of a "tags" map) means supplying that
// whole top-level attribute's own complete new value, not a dotted
// sub-path -- the same "$ref"-shaped resolution values already receive
// no special deep-merge treatment either.
type Override struct {
	Address string                     `json:"address"`
	Config  map[string]json.RawMessage `json:"config"`
}

// BlueprintCall is one blueprint invocation an intent/v1 document names
// -- see IntentFile.BlueprintCalls's own doc comment for the full
// account of where this comes from and how it gets expanded. Args is
// always string-keyed/string-valued regardless of a param's own real
// declared type (docs/blueprint.md's own "Cross-medium calling"
// section) -- neither the diagram medium (a D2 node attribute's own raw
// text) nor the md medium (an LLM extracting a value from prose) has
// the target blueprint's own Ubxfile in hand at the point this struct
// is populated, so type coercion (string -> the param's own real
// string/number/bool type) is deferred entirely to expansion time, once
// the target blueprint's own declared params are actually known.
type BlueprintCall struct {
	// Name is a short, human-readable label for this call (a diagram
	// node's own label, or an md draft's own short description) -- used
	// only for error messages and the synthesized calling stack's own
	// intent.summary, never part of any resource's own address.
	Name string `json:"name"`
	// Blueprint names the blueprint to call -- an absolute or relative
	// local filesystem path, or a git repository URL (Ref/Path below
	// name which ref/location within it, mirroring blueprint.Pull's own
	// parameter shape exactly, UBI-74 Slice 3). No OCI/Strata reference
	// syntax yet (Slice 7).
	Blueprint string `json:"blueprint"`
	// Ref is the git ref (branch/tag/commit) to check out -- ignored
	// for a local Blueprint path, optional for a git one (empty means
	// whatever `git clone` itself checks out by default, matching
	// blueprint.Pull's own "ref" parameter).
	Ref string `json:"ref,omitempty"`
	// Path is the blueprint's own location within the git repo --
	// ignored for a local Blueprint path, optional for a git one
	// (empty/"." means the repo root IS the blueprint, matching
	// blueprint.Pull's own "path" parameter).
	Path string `json:"path,omitempty"`
	// Args maps a declared param name to its own raw string value.
	Args map[string]string `json:"args"`
	// CallName was added 2026-08-06 (UBI-128): the caller-chosen name
	// this specific call's own declared outputs (blueprint/ubxfile.go's
	// outputs: key) can be referenced by, WITHIN THIS SAME DOCUMENT --
	// the diagram medium's own ubx_blueprint node identifier (implicit,
	// always present), or the md medium's own explicit "Call blueprint X
	// as 'name' with:" clause (a real, new grammar addition, never part
	// of Name above, which is a free-text label only). Empty is legal --
	// a call whose own outputs are never referenced needs no CallName at
	// all, and every producer of a BlueprintCall before this ticket
	// (a direct SDK-import call has none of these at all; Slice 2's own
	// calling convention isn't a BlueprintCall in the first place) leaves
	// this empty and is completely unaffected. Purely additive, matching
	// every other BlueprintCall field's own precedent -- resolveOnce
	// itself has zero awareness this exists; blueprint.ExpandCalls is
	// the only place it's ever read (blueprint/outputs.go).
	//
	// Deliberately NOT the same mechanism as @stack.type.name (UBI-47)
	// cross-stack references: a CallName only ever resolves an output
	// WITHIN the same proposal being resolved right now, never crosses a
	// ledger/trust boundary, and carries no staleness/pinning concept at
	// all -- see docs/blueprint.md's own "outputs:" section for the full
	// account of why these two are kept visibly, structurally distinct.
	CallName string `json:"call_name,omitempty"`
}

// BlueprintOutputRefPrefix marks a PROVISIONAL $ref.to value -- not yet a
// real resolved address -- naming a blueprint call's own declared output
// (blueprint/ubxfile.go's outputs: key) rather than an ordinary resource
// attribute. The full value is BlueprintOutputRefPrefix + "<CallName>:
// <outputKey>". Exported here (rather than living privately in blueprint/
// outputs.go, its only original owner) because BOTH producers of this
// marker -- diagram/parse.go's own ref: sigil resolution (UBI-128) and
// the md medium's own named-call grammar -- already depend on
// core/resolver for BlueprintCall itself, while diagram deliberately does
// NOT depend on the much heavier blueprint package (its own zero-I/O
// parsing discipline, docs/diagram-medium.md). blueprint.ExpandCalls
// (blueprint/outputs.go) is the sole CONSUMER, rewriting every instance
// of this marker into a real resolved address once a call's own outputs
// are actually known -- by the time core/resolver's own resolveRef (refs.
// go) ever sees a $ref.to value, this marker has always already been
// rewritten away.
const BlueprintOutputRefPrefix = "$blueprint_output:"

// ResourceIntent is one entry of IntentFile.Resources. Op is always
// explicit ("create" | "modify"), never inferred from ledger presence --
// see docs/resolver.md's own "op: explicit, not inferred" section for why.
// Config is the resource's full desired end-state (never a hand-computed
// before/after diff), whose values may be plain JSON or one of $ref/
// $cross/$secret/$ephemeral (docs/schema.md's amendment).
//
// DependsOn was added 2026-07-28 (docs/schema.md -- "Amendment:
// ResourceIntent.DependsOn", UBI-47): canonical addresses this resource
// depends on, with no config-attribute opinion at all -- a topology-only
// dependency signal the diagram medium's own lossy-medium rule needs
// (a diagram edge names no attribute, so it can't be expressed as a
// $ref/$cross the way every other dependency source is). Purely
// additive and optional; every other producer of an intent file (a
// hand-written JSON file, `ubx propose --from-doc`'s own draft, `ubx
// resolve --from-code`'s own evaluated document) leaves it empty and is
// completely unaffected -- see unionDependsOn (refs.go) for how it
// merges into the identical dependency graph $ref/$cross scanning
// already builds, not a second one.
//
// Sources was added 2026-08-05 (docs/blueprint.md's own "Provenance"
// section, UBI-74 Slice 6): per-RESOURCE provenance, reusing
// core.IntentSource's own existing multi-kind shape verbatim (a new
// "blueprint" kind value, additive, the same way every prior kind --
// dialogue/document/intent_provider/promotion/... -- was added, never a
// new field shape) rather than the DOCUMENT-level core.Intent.Sources
// this same type already populates. A document-level source can't
// express "resource A came from blueprint X, but sibling resource B in
// the SAME document didn't" -- a real, ordinary shape once a diagram or
// md document mixes a blueprint call with hand-authored resources
// (confirmed real, not hypothetical: diagram/parse_test.go's own
// TestParse_UbxBlueprint_MixedWithOrdinaryResource). Purely additive and
// optional, matching DependsOn's own precedent exactly: every producer
// except blueprint.ExpandCalls (which stamps every resource IT produces
// with exactly one {Kind: "blueprint", Ref: "<name>:<content-hash>"}
// entry) leaves this nil. Threaded into the resolved create node's own
// "sources" key by resolveOnce, below -- resolver.Resolve otherwise has
// zero awareness this field means anything blueprint-specific; it's
// just data that rides along, the same way DependsOn already does.
type ResourceIntent struct {
	Type      string              `json:"type"`
	Name      string              `json:"name"`
	Op        string              `json:"op"`
	Config    json.RawMessage     `json:"config"`
	Provider  *ProviderHint       `json:"provider,omitempty"`
	DependsOn []string            `json:"depends_on,omitempty"`
	Sources   []core.IntentSource `json:"sources,omitempty"`

	// ForEach was added 2026-08-06 (UBI-129): the bare name of a
	// declared list(string)/list(number) params: entry this resource
	// TEMPLATE iterates over -- "" for an ordinary, non-iterating
	// resource, the overwhelming common case and completely unaffected
	// (every producer before this ticket leaves it empty, matching every
	// other purely-additive ResourceIntent field's own precedent above).
	// Set only by `ubx blueprint build`'s own intent-provider draft, when
	// the Ubxfile's own resources: prose describes a "for each value in
	// {list_param}, create..." iteration pattern (docs/blueprint.md's own
	// "List-typed parameters + iteration" section) -- never by a hand-
	// written intent file, never by resolver.Resolve itself (which has
	// zero awareness this field means anything -- it rides along exactly
	// like DependsOn/Sources already do). Consumed exclusively by
	// blueprint/decode.go's own decodeBlueprint, which validates it names
	// a real declared list-typed param and is set on AT MOST one resource
	// per blueprint, then by each language's own codegen (gogen.go/
	// tsgen.go/pygen.go), which compiles this ONE ResourceIntent into a
	// real for loop rather than a single resource-creation call. Never
	// survives past BUILD-time codegen: once a built blueprint is
	// actually INVOKED (direct SDK import, or blueprint.ExpandCalls'
	// own synthesized caller -- Slice 5), the real for loop already
	// executed inside the compiled program, emitting N ordinary
	// ResourceIntent values with ForEach empty on every one of them, the
	// same as if a caller had hand-written N separate resource() calls.
	ForEach string `json:"for_each,omitempty"`
}

const (
	OpCreate = "create"
	OpModify = "modify"
)

// DataSourceIntent is one entry of IntentFile.DataSources
// (docs/schema.md's own "Amendment: data sources"). Deliberately its
// own type, not ResourceIntent with Op left blank: a data_sources[]
// entry carries no Op at all, since the array itself is the
// discriminator (every entry in it is a read, never a create/modify),
// the same reasoning Destroys already established for why a destroy is
// a sibling array rather than an "op" value.
//
// Lookup plays the role Config plays for a ResourceIntent -- the query
// parameters sent to the provider's real read -- but is named
// differently because a data source has no desired end-state to
// submit, only something to look up with; its own legal values are
// identical to Config's (plain JSON, or $ref/$secret/$cross markers,
// resolved by the exact same machinery).
//
// No DependsOn field, unlike ResourceIntent: v1 scope is SDK-authored
// data sources only (Collector.addDataSource, all three runtimes),
// which always express a dependency through $ref inside Lookup itself
// -- there is no lossy authoring medium yet (the diagram/md mediums
// ResourceIntent.DependsOn exists for) that could need a topology-only
// escape hatch for a data source. A real, named, deliberately deferred
// gap, not an oversight -- add if a future medium needs it, mirroring
// DependsOn's own real addition history rather than guessing ahead of
// time.
//
// No Provider *ProviderHint field either, for the identical reason:
// not yet needed by anything real, added the same way ResourceIntent's
// own Provider hint was -- when a genuine ambiguous-ownership case is
// found, not speculatively.
type DataSourceIntent struct {
	Type    string              `json:"type"`
	Name    string              `json:"name"`
	Lookup  json.RawMessage     `json:"lookup"`
	Sources []core.IntentSource `json:"sources,omitempty"`
}

var (
	// ErrUnknownIntentKind means IntentFile.Kind isn't "ubx:intent/v1".
	ErrUnknownIntentKind = errors.New("resolve: unrecognized intent file kind")

	// ErrUnexpandedBlueprintCalls means IntentFile.BlueprintCalls is
	// still non-empty at Resolve time -- blueprint.ExpandCalls (or an
	// equivalent) must run first and clear it; Resolve itself has no
	// notion of a blueprint call at all (UBI-74 Slice 5), by design --
	// see IntentFile.BlueprintCalls's own doc comment.
	ErrUnexpandedBlueprintCalls = errors.New("resolve: intent file still has unexpanded blueprint_calls -- expand them into resources first")

	// ErrInvalidOp means a resource intent's Op isn't "create" or "modify".
	ErrInvalidOp = errors.New("resolve: resource op must be \"create\" or \"modify\"")

	// ErrDuplicateResource means two resource intents in the same file
	// name the same (stack, type, name) address.
	ErrDuplicateResource = errors.New("resolve: duplicate resource address in intent file")

	// ErrUnknownType means no declared provider's schema has such a
	// resource type at all (docs/resolver-adversarial.md row 8; extended
	// 2026-07-18, UBI-43, to a set of one or more declared providers --
	// see inferProvider).
	ErrUnknownType = errors.New("resolve: provider schema has no such type")

	// ErrAmbiguousType means more than one declared provider's schema
	// claims the same type, and no "provider" hint was given to break the
	// tie (docs/multi-provider-adversarial.md row 1).
	ErrAmbiguousType = errors.New("resolve: type is ambiguous across declared providers")

	// ErrProviderHintUnknown means a ResourceIntent's own "provider" hint
	// names a source that isn't one of the stack's own declared providers.
	ErrProviderHintUnknown = errors.New("resolve: provider hint names a source this stack does not declare")

	// ErrProviderHintDoesNotOwnType means a ResourceIntent's own
	// "provider" hint names a declared provider whose schema does NOT
	// actually claim the type -- the hint selects among real owners, it
	// never manufactures ownership.
	ErrProviderHintDoesNotOwnType = errors.New("resolve: provider hint names a provider that does not own this type")

	// ErrCreateTargetExists means op "create" names an address the ledger
	// already has (docs/resolver.md's "op: explicit, not inferred").
	ErrCreateTargetExists = errors.New("resolve: op \"create\" names an address the ledger already has -- use op \"modify\"")

	// ErrModifyTargetMissing means op "modify" names an address the
	// ledger has never recorded (docs/resolver-adversarial.md row 9).
	ErrModifyTargetMissing = errors.New("resolve: op \"modify\" names an address the ledger has never recorded -- did you mean \"create\"?")

	// ErrDuplicateDataSource means two data_sources[] entries in the same
	// file name the same (stack, type, name) address -- ErrDuplicateResource's
	// own real sibling (docs/schema.md's own "Amendment: data sources"),
	// kept as a distinct error rather than reused so the message names
	// the right array; a data source can never collide with a resource
	// address either way (DataSourceIntent.Type always carries the real
	// "data_" prefix that amendment pins, so the two address spaces never
	// actually overlap).
	ErrDuplicateDataSource = errors.New("resolve: duplicate data source address in intent file")

	// ErrDataSourceLookupNotConcrete means a data_sources[] entry's own
	// resolved Lookup still carries a $ref/$cross/$secret/$computed/
	// $ephemeral marker somewhere within it -- unlike a resource's own
	// resolvedConfig (where a surviving $computed marker is legitimate,
	// deferred to real ship-time substitution) a data source's own read
	// happens synchronously, right now, during resolve: there is no
	// later ship-time step to fill in a deferred value, and nothing to
	// look up with a $secret marker's own backend/path envelope instead
	// of the real secret material. A lookup referencing a sibling
	// resource's own schema-Computed attribute (its real "id," not known
	// until that resource ships) is the expected, common real trigger --
	// a data source cannot look something up by a value that doesn't
	// exist yet.
	ErrDataSourceLookupNotConcrete = errors.New("resolve: data source lookup still references a value that is not yet concrete (a sibling resource's own not-yet-created attribute, or an unresolved marker)")

	// ErrDataSourceReadFailed means a data_sources[] entry's own real,
	// live provider read failed mid-resolution -- an ordinary, real
	// failure (the provider rejected the lookup, the resource genuinely
	// doesn't exist, a network error, ...), never wrapped in or confused
	// with core.ErrDoubleRunMismatch: DoubleRun's own short-circuit ("a,
	// err := fn(); if err != nil { return nil, err }") means an error
	// returned from the FIRST of its own two resolveOnce invocations
	// propagates directly, never reaching the byte-comparison line at
	// all -- this error can only ever surface as itself, exactly what
	// "a provider read failing is an ordinary error with its own
	// message, not anything resembling nondeterminism" requires.
	ErrDataSourceReadFailed = errors.New("resolve: data source read failed")

	// ErrRefNotFound means a $ref/$cross doesn't resolve to any known
	// resource or attribute path (docs/resolver-adversarial.md row 3).
	ErrRefNotFound = errors.New("resolve: reference does not resolve to any known resource or attribute")

	// ErrCycleDetected means the intra-stack dependency graph has a cycle
	// (docs/resolver-adversarial.md row 2) -- genuinely new code; v1 XCL's
	// own single-stack graph never detected this at all.
	ErrCycleDetected = errors.New("resolve: circular reference between resources")

	// ErrComputedWhereConcreteRequired means a $computed value was used
	// somewhere docs/resolver.md's own rules require a concrete value
	// (docs/resolver-adversarial.md row 6) -- the direct generalization of
	// v1's own "Pending used where Resolved required" rule.
	ErrComputedWhereConcreteRequired = errors.New("resolve: a $computed value was used where a concrete value is required")

	// ErrSecretNotSensitive means a $secret marker sits at a config path
	// the real provider schema does not flag Sensitive
	// (docs/resolver-adversarial.md row 7) -- a real check v1 never had.
	ErrSecretNotSensitive = errors.New("resolve: $secret placed at an attribute the provider schema does not flag Sensitive")

	// ErrNeighborLedgerMissing means a $cross's ledger_dir has never been
	// initialized at all (docs/resolver-adversarial.md row 4, the "no
	// ledger here at all" half).
	ErrNeighborLedgerMissing = errors.New("resolve: cross-stack ledger_dir has never been initialized")

	// ErrCrossStackAddressNotFound means the neighbor ledger exists but
	// has never recorded the referenced address (docs/resolver-adversarial.md
	// row 4, the "ledger exists, but this wasn't recorded" half --
	// distinguished from ErrNeighborLedgerMissing on purpose).
	ErrCrossStackAddressNotFound = errors.New("resolve: cross-stack address not recorded in the neighbor ledger")

	// ErrCrossStackPinStale means a cross_stack_pin's neighbor ledger has
	// advanced since resolve time (docs/resolver-adversarial.md row 5) --
	// VerifyPins' own sentinel.
	ErrCrossStackPinStale = errors.New("resolve: cross-stack pin is stale -- the neighbor ledger has advanced since this proposal was resolved")

	// The following are docs/resolver.md's "Amendment (2026-07-17, UBI-30):
	// destroys" sentinels -- see resolveDestroys (destroys.go).

	// ErrDuplicateDestroy means two destroys[] entries in the same intent
	// file name the same address.
	ErrDuplicateDestroy = errors.New("resolve: duplicate destroy address in intent file")

	// ErrDestroyResourceConflict means an address appears in both
	// resources[] and destroys[] in the same intent file -- structurally
	// contradictory (create/modify and destroy the same thing at once).
	ErrDestroyResourceConflict = errors.New("resolve: address cannot be both created/modified and destroyed in the same intent file")

	// ErrDestroyTargetMissing means destroys[] names an address the
	// ledger has never recorded (or one already tombstoned by a prior
	// destroy) -- the destroy-specific instance of ErrModifyTargetMissing's
	// own "declared operation doesn't match ledger reality" pattern.
	ErrDestroyTargetMissing = errors.New("resolve: destroy names an address the ledger has never recorded")

	// ErrRefToDestroyTarget means a $ref/$cross resolved to an address this
	// same proposal is also destroying -- referencing a value that's being
	// removed in the same breath is never sound.
	ErrRefToDestroyTarget = errors.New("resolve: $ref/$cross target is being destroyed in this same proposal")

	// ErrDestroyOrphaned means a destroy target is still referenced by a
	// live resource (intra-stack, via a historically recorded depends_on
	// edge, or cross-stack, via a named known_dependents neighbor's own
	// cross_stack_pin) that this proposal neither also destroys nor
	// updates -- docs/resolver.md's own "orphan protection," checked
	// against the whole ledger, not just this intent file.
	ErrDestroyOrphaned = errors.New("resolve: destroy target is still referenced by a resource this proposal does not also destroy or update")

	// ErrDependentLedgerMissing means a known_dependents entry (an operator-
	// supplied ledger_dir for cross-stack orphan protection) has never been
	// initialized -- distinct from "not performed at all" (an honest,
	// expected gap when known_dependents is empty): a NAMED dependent that
	// doesn't actually exist is far more likely a real mistake (a typo, a
	// stale path) than an intentional gap, so it's a hard error rather than
	// silently skipped.
	ErrDependentLedgerMissing = errors.New("resolve: known_dependents ledger_dir has never been initialized")

	// ErrDestroyTargetNoLookup means a destroy target has no recorded
	// lookup key anywhere in its own ledger history to carry into
	// resolution.inputs["destroy_target"].Lookup (docs/schema.md requires
	// one, non-empty) -- an ancient apply record predating UBI-29, with no
	// derivable "id" either. Genuinely rare; failing resolve here, with a
	// precise error, beats producing a proposal core.Validate would reject
	// anyway with a less specific message.
	ErrDestroyTargetNoLookup = errors.New("resolve: destroy target has no recorded lookup key in ledger history")

	// ErrModifyTargetNoLookup is ErrDestroyTargetNoLookup's own twin for a
	// modify target (UBI-85 live finale: shipping ANY modify failed ship-time
	// freshness verification -- "resolution.inputs entry ... has no recorded
	// lookup key" -- because this resolutionInputs' own "live_state" entry
	// never set Lookup at all, unlike destroys.go's "destroy_target" entry a
	// few lines below, which already does via the same core.Ledger.LastLookup
	// this case now also calls). A modify target is, by construction,
	// already-ledgered (same precondition as a destroy target), so its
	// lookup key was already recorded when it was first observed/adopted/
	// shipped -- never derived new here.
	ErrModifyTargetNoLookup = errors.New("resolve: modify target has no recorded lookup key in ledger history")

	// ErrMarkerStringLiteral means a config value is a plain string that
	// LOOKS like an attempt at a $ref/$cross/$secret/$computed/$ephemeral
	// marker (starts with the marker's own key + ":", e.g. "$ref:payments.
	// aws_iam_role.ci-runner.arn") rather than the real wire-shape object
	// ({"$ref": {"to": "..."}}) -- UBI-63's own bug 1: an upstream
	// producer (the md-medium intent pipeline, confirmed live; anything
	// else that ever produces an intent file is equally capable of the
	// same mistake) that means to reference another resource but never
	// reaches the actual marker encoding. A string shaped like this is
	// never a plausible, intended literal value -- refused here,
	// unconditionally, regardless of which upstream producer it came
	// from, rather than trusted to reach a real provider as a broken,
	// literal placeholder (docs/resolver-adversarial.md's own "hard
	// refusal, not a silent pass-through" standard, applied to a failure
	// mode found live rather than designed in from the start).
	ErrMarkerStringLiteral = errors.New("resolve: config value looks like a mis-encoded marker string, not the real wire-shape object")

	// ErrSecretEmbeddedInString means a $secret marker survived resolution
	// (still unresolved -- $secret is input-preserved verbatim, never
	// substituted with real material, docs/resolver.md's own "Secrets"
	// section) somewhere inside a JSON-encoded string attribute (UBI-63
	// session 2's own "deferred materialization" amendment, docs/
	// resolver.md). Unlike $computed (which is now allowed to persist
	// into the signed proposal as a template, substituted for real at
	// ship time), $secret has no defined redaction path once buried
	// inside an opaque string attribute the provider schema doesn't
	// itself flag Sensitive -- core.Redact's own walk only ever inspects
	// attributes the schema flags Sensitive directly, never a string's own
	// decoded-JSON interior. Refused unconditionally, the same "hard
	// refusal, not a silent pass-through" standard as every other marker
	// safety check in this file.
	ErrSecretEmbeddedInString = errors.New("resolve: a $secret value can't be embedded inside a JSON-encoded string attribute -- no redaction path exists for it there")

	// ErrUnknownConfigKey means a resource intent's config carries a key
	// that matches no real attribute or nested-block name in its type's
	// own provider schema at the pinned version (UBI-66, docs/resolver-
	// adversarial.md row 14). Checked here, at resolve time, against the
	// exact schema InferProvider already fetched for type inference --
	// never left to surface first as a cryptic, generic rejection from a
	// real ApplyResourceChange call partway through a real ship,
	// potentially after other resources in the same batch have already
	// applied (provider.ErrUnrecognizedConfigKey, UBI-63 session 2,
	// remains unchanged as ship time's own defense-in-depth backstop for
	// any input that somehow bypasses resolve). Model-agnostic:
	// resolveOnce has no way to tell, and doesn't need to, whether a
	// ResourceIntent came from a hand-written file, an evaluated SDK
	// program, or any intentprovider.Adapter's own draft -- the same
	// check runs over all of them.
	ErrUnknownConfigKey = errors.New("resolve: unrecognized config key")

	// ErrMissingRequiredAttribute means a resource intent's config omits
	// a value for an attribute its type's own provider schema marks
	// Required (UBI-90, docs/resolver-adversarial.md's own new row).
	// Checked here, at resolve time, the exact same chokepoint
	// ErrUnknownConfigKey already uses, against the exact schema
	// InferProvider already fetched -- never left to surface first as a
	// cryptic, generic rejection from a real ApplyResourceChange call
	// partway through a real ship, potentially after other resources in
	// the same batch have already applied
	// (provider.ErrRequiredAttributeMissing, UBI-63 session 2, remains
	// unchanged as ship time's own defense-in-depth backstop). Model-
	// agnostic, same as ErrUnknownConfigKey: resolveOnce has no way to
	// tell, and doesn't need to, whether a ResourceIntent came from a
	// hand-written file, an evaluated SDK program, or a diagram/md/
	// intentprovider.Adapter's own draft -- the same check runs over all
	// of them. Confirmed as the correct design (UBI-90's own "root design
	// question," not assumed): this is a hard, mechanical, resolve-time
	// refusal, deliberately NOT routed through Intent.Questions -- a
	// Question's own Blocking field carries zero resolver-side
	// enforcement by explicit, considered design (Question's own doc
	// comment, docs/intent-provider.md's "Component 3" section: reserved
	// for a genuine interpretive ambiguity a human must judge, never for
	// a mechanically-checkable schema defect -- the same bucket as a
	// wrong resource type or a dangling $ref, both of which already hard-
	// refuse here, never render as a Question).
	ErrMissingRequiredAttribute = errors.New("resolve: missing required attribute")
)

// configKeyError is one ErrUnknownConfigKey occurrence. resolveOnce
// collects every issue found across the WHOLE batch (every wrong key, on
// every resource) and joins them into a single refusal via errors.Join,
// rather than surfacing only the first mistake and making a drafter
// fix-and-resubmit one key at a time -- UBI-66's own hermetic bar: "a
// drafted resource with 3 wrong keys refused with 3 distinct teaching
// errors."
type configKeyError struct {
	addr     core.Address
	typeName string
	issue    ConfigKeyIssue
}

func (e configKeyError) Error() string {
	base := fmt.Sprintf("%s: %s: config key %q does not exist on %s", ErrUnknownConfigKey, e.addr, e.issue.Path, e.typeName)
	if e.issue.Suggestion != "" {
		return fmt.Sprintf("%s (did you mean %q?)", base, e.issue.Suggestion)
	}
	return base
}

func (e configKeyError) Unwrap() error { return ErrUnknownConfigKey }

// requiredAttributeError is one ErrMissingRequiredAttribute occurrence --
// configKeyError's own sibling, same "collect every issue across the WHOLE
// batch, join into one refusal" discipline (UBI-90).
type requiredAttributeError struct {
	addr     core.Address
	typeName string
	issue    RequiredAttributeIssue
}

func (e requiredAttributeError) Error() string {
	return fmt.Sprintf("%s: %s: config has no value for required attribute %q on %s -- a medium that structurally can't express this attribute (e.g. the diagram medium's own topology-only design) needs it filled in some other way: a hand-edited plan file, or authoring this resource via a different medium (--from-doc, an SDK program, or a hand-written intent file) instead",
		ErrMissingRequiredAttribute, e.addr, e.issue.Path, e.typeName)
}

func (e requiredAttributeError) Unwrap() error { return ErrMissingRequiredAttribute }

// VerifyPins re-derives every cross-stack pin recorded in p (every
// resolution.inputs entry with kind "cross_stack_pin") and confirms its
// neighbor ledger's CURRENT head still matches the PinnedHead recorded at
// resolve time -- docs/resolver.md's own "neighbor-advance staleness"
// mechanism, made checkable (docs/resolver-adversarial.md row 5), the same
// "resolved-time truth vs. accept-time reality" shape core.VerifyFreshness
// already enforces for live cloud state, one level up. Returns
// ErrCrossStackPinStale (wrapping which pin and its two head values) on
// the first mismatch found. in.LedgerDir is already a fully-resolved
// address by the time it's recorded here (resolveCross's own job, at
// resolve time) -- a git-directory path or a store URI alike, so
// core.OpenRef handles both uniformly, no addressing metadata needed at
// verify time. Wired into `ubx accept` (both acceptance tiers).
func VerifyPins(p *core.Proposal) error {
	for _, in := range p.Resolution.Inputs {
		if in.Kind != "cross_stack_pin" {
			continue
		}
		neighbor, closeNeighbor, err := core.OpenRef(context.Background(), in.LedgerDir)
		if err != nil {
			return fmt.Errorf("verify pin for %s: %w", in.Resource, err)
		}
		head, err := neighbor.Head()
		closeNeighbor()
		if err != nil {
			return fmt.Errorf("verify pin for %s: %w", in.Resource, err)
		}
		if head != in.PinnedHead {
			return fmt.Errorf("%w: %s (ledger %s) pinned at %s, now %s",
				ErrCrossStackPinStale, in.Resource, in.LedgerDir, in.PinnedHead, head)
		}
	}
	return nil
}

// batchEntry is one resource intent's working state during resolution --
// package-private, never exposed.
type batchEntry struct {
	ri             ResourceIntent
	di             *DataSourceIntent // non-nil for a data_sources[] entry; ri is then the zero value, never read directly -- use typeName()/rawValue()/sources() instead
	addr           core.Address
	provider       DeclaredProvider // which declared provider owns ri.Type/di.Type, set before any value resolution (docs/resolver.md's own amendment)
	rawEdges       []string         // canonical addresses this entry's raw $ref targets, restricted to this batch
	resolvedConfig map[string]interface{}
}

// isDataSource reports whether e came from data_sources[] rather than
// resources[].
func (e *batchEntry) isDataSource() bool { return e.di != nil }

// typeName returns e's own real wire type, regardless of which array it
// came from -- the one accessor every shared pass (edge scanning, value
// resolution, resolveRef) uses instead of reaching into ri.Type/di.Type
// directly, so neither array's own zero-value fields for the OTHER kind
// ever leak into a real decision by accident.
func (e *batchEntry) typeName() string {
	if e.di != nil {
		return e.di.Type
	}
	return e.ri.Type
}

// rawValue returns e's own not-yet-resolved raw JSON -- Config for a
// resource, Lookup for a data source. Both carry the identical legal
// value shapes (plain JSON, or $ref/$secret/$cross markers) and go
// through the exact same resolveValue/scanRefEdges machinery either
// way.
func (e *batchEntry) rawValue() json.RawMessage {
	if e.di != nil {
		return e.di.Lookup
	}
	return e.ri.Config
}

// sources returns e's own real provenance list (docs/blueprint.md's
// own "Provenance" section) -- ResourceIntent.Sources and
// DataSourceIntent.Sources are the identical shape, reused, not two
// conventions.
func (e *batchEntry) sources() []core.IntentSource {
	if e.di != nil {
		return e.di.Sources
	}
	return e.ri.Sources
}

// rawValueLabel names e's own rawValue() for an error message -- purely
// cosmetic (which real field a decode failure is about), never a
// behavioral branch.
func rawValueLabel(e *batchEntry) string {
	if e.isDataSource() {
		return "lookup"
	}
	return "config"
}

// dataSourceReadCache memoizes a real, live data-source read across
// resolveOnce's own two core.DoubleRun invocations -- scoped to exactly
// one Resolve call (a fresh instance is created inside Resolve itself,
// immediately before calling core.DoubleRun, and passed into both
// invocations via the closure). DoubleRun requires resolveOnce to
// produce byte-identical output across two calls, to catch genuine code
// nondeterminism (map iteration, uninitialized state) as a hard failure
// -- a real, live provider read is not "nondeterministic code," it's an
// external, honestly-changing input, and if resolveOnce performed it
// twice, the world could legitimately have moved between the two calls
// (a live system, not a frozen snapshot the way ledger state and
// provider schema already are for every other part of resolution),
// which DoubleRun would then misreport as ErrDoubleRunMismatch -- a
// code-determinism bug -- when the real cause is an ordinary, benign
// external state change. Reading once and reusing the identical bytes
// on both invocations keeps DoubleRun checking exactly what it was
// built to check.
//
// A cache instance must never outlive one Resolve call. One that did
// would silently serve a LATER Resolve call's own data source lookup a
// STALE observed value an EARLIER call read -- exactly the staleness
// data sources exist to catch, reintroduced by the one mechanism this
// package uses to keep DoubleRun from misfiring on them. This is why
// newDataSourceReadCache is only ever called from inside Resolve
// itself, never at package scope, never memoized across calls.
type dataSourceReadCache struct {
	reads map[string]dataSourceRead
}

type dataSourceRead struct {
	observed json.RawMessage
	hash     string
}

func newDataSourceReadCache() *dataSourceReadCache {
	return &dataSourceReadCache{reads: map[string]dataSourceRead{}}
}

// get returns addr's own real observed read (and its ObservedHash),
// performing the real, live read via reader only the first time this
// exact address is requested from this cache instance -- every
// subsequent call (the second DoubleRun invocation, or a genuine second
// reference to the same address within one resolveOnce pass) reuses the
// cached bytes, never touching the provider again. A real read failure
// is never cached (so a transient failure on the first invocation
// doesn't silently poison a retry within the same process -- though in
// practice DoubleRun's own short-circuit-on-error means a failure here
// already ends the whole Resolve call before a second invocation could
// ever occur).
func (c *dataSourceReadCache) get(ctx context.Context, reader core.StateReader, addr core.Address, providerSource string, providerConfig, lookup json.RawMessage) (observed json.RawMessage, hash string, err error) {
	key := addr.String()
	if cached, ok := c.reads[key]; ok {
		return cached.observed, cached.hash, nil
	}
	observed, hash, err = core.ReadAndFingerprint(ctx, reader, addr, providerSource, providerConfig, lookup)
	if err != nil {
		return nil, "", err
	}
	c.reads[key] = dataSourceRead{observed: observed, hash: hash}
	return observed, hash, nil
}

// Resolve resolves intent against l's current ledger state and schema
// into a draft kind:"change" proposal -- creates, modifies, and (2026-07-17,
// UBI-30) destroys, dependency ordered (docs/resolver.md's own contract).
// The whole resolution runs through core.DoubleRun (docs/resolver-adversarial.md
// row 1): called twice, byte-compared, a hard failure on any divergence,
// v1 XCL never had an equivalent check at all.
//
// knownDependents is docs/resolver.md's own "Amendment (UBI-30): destroys"
// cross-stack orphan-protection input: an explicit, operator-supplied list
// of neighbor ledger_dirs to check for a cross_stack_pin against any of
// intent's own destroys[] -- the same "explicit path over a fancy
// registry" instinct $cross's own ledger_dir already established, since
// this stack has no built-in index of who has ever pinned against it. Nil
// or empty is legal (no destroys, or none checked) -- see
// resolveDestroys/crossStackOrphanCheck (destroys.go) for what gets
// recorded either way.
//
// resolvedAt is captured ONCE here, before either DoubleRun call, and
// threaded into both -- found live (UBI-27, executor session): resolveOnce
// used to call time.Now() itself, so a resolve whose two DoubleRun calls
// happened to straddle a second boundary (RFC3339's own resolution)
// produced two genuinely different resolved_at strings, a false-positive
// ErrDoubleRunMismatch over a real value that was never supposed to be
// checked for run-to-run stability in the first place -- rare (didn't
// reproduce in 15 back-to-back runs) but real, and load-bearing for any
// caller running `ubx resolve` for real. Fixed at the one place that
// actually varies between the two calls, not by weakening DoubleRun's own
// comparison.
//
// providers is docs/resolver.md's own "Amendment (2026-07-18, UBI-43):
// multi-provider stacks" input: every provider a stack declares, each
// paired with its own SchemaInspector. A single-provider stack is simply
// a one-element slice -- there is no separate code path for it anymore.
func Resolve(l *core.Ledger, providers []DeclaredProvider, intent *IntentFile, knownDependents []string) (*core.Proposal, error) {
	resolvedAt := time.Now().UTC().Format(time.RFC3339)
	var resolved *core.Proposal
	// dsCache is scoped to exactly this one Resolve call -- created
	// fresh here, on every call, never a package-level or otherwise
	// longer-lived cache. See dataSourceReadCache's own doc comment for
	// why it exists at all (DoubleRun calls resolveOnce twice; this is
	// what keeps a real, live data source read from happening twice) and
	// why living any longer than one Resolve call would be a real bug,
	// not just an inefficiency: a cache reused across separate Resolve
	// calls would silently serve a LATER plan's own data source lookup a
	// STALE value an EARLIER call observed -- exactly the staleness data
	// sources exist to catch, reintroduced by the one mechanism meant to
	// keep DoubleRun from misfiring on them.
	dsCache := newDataSourceReadCache()
	_, err := core.DoubleRun(func() ([]byte, error) {
		p, err := resolveOnce(l, providers, intent, knownDependents, resolvedAt, dsCache)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		resolved = p
		return b, nil
	})
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func resolveOnce(l *core.Ledger, providers []DeclaredProvider, intent *IntentFile, knownDependents []string, resolvedAt string, dsCache *dataSourceReadCache) (*core.Proposal, error) {
	if intent.Kind != IntentFileKind {
		return nil, fmt.Errorf("%w: got %q", ErrUnknownIntentKind, intent.Kind)
	}
	if len(intent.BlueprintCalls) > 0 {
		return nil, fmt.Errorf("%w (%d entries)", ErrUnexpandedBlueprintCalls, len(intent.BlueprintCalls))
	}

	batch := make(map[string]*batchEntry, len(intent.Resources)+len(intent.DataSources))
	order := make([]string, 0, len(intent.Resources)+len(intent.DataSources))

	for _, ri := range intent.Resources {
		prov, err := InferProvider(providers, ri.Type, ri.Provider)
		if err != nil {
			return nil, err
		}
		if ri.Op != OpCreate && ri.Op != OpModify {
			return nil, fmt.Errorf("%w: %q (resource %s.%s)", ErrInvalidOp, ri.Op, ri.Type, ri.Name)
		}
		addr := core.Address{Stack: intent.Stack, Type: ri.Type, Name: ri.Name}
		key := addr.String()
		if _, dup := batch[key]; dup {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateResource, addr)
		}

		_, found, err := l.FoldState(addr)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", addr, err)
		}
		switch ri.Op {
		case OpCreate:
			if found {
				return nil, fmt.Errorf("%w: %s", ErrCreateTargetExists, addr)
			}
		case OpModify:
			if !found {
				return nil, fmt.Errorf("%w: %s", ErrModifyTargetMissing, addr)
			}
		}

		batch[key] = &batchEntry{ri: ri, addr: addr, provider: prov}
		order = append(order, key)
	}

	// data_sources[] -- no Op, no FoldState existence check: a data
	// source is never expected to be present or absent in the ledger,
	// it isn't a managed resource at all (docs/schema.md's own
	// "Amendment: data sources"). Real, deliberate scope note: a
	// provider hint isn't accepted here yet (DataSourceIntent's own doc
	// comment has why) -- InferProvider is called with a nil hint,
	// exactly like a ResourceIntent with no "provider" field set.
	for i := range intent.DataSources {
		di := &intent.DataSources[i]
		prov, err := InferProvider(providers, di.Type, nil)
		if err != nil {
			return nil, err
		}
		addr := core.Address{Stack: intent.Stack, Type: di.Type, Name: di.Name}
		key := addr.String()
		if _, dup := batch[key]; dup {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateDataSource, addr)
		}

		batch[key] = &batchEntry{di: di, addr: addr, provider: prov}
		order = append(order, key)
	}

	// docs/resolver.md's "Amendment (2026-07-17, UBI-30): destroys" --
	// parsed and presence-validated before any resources[] value
	// resolution, so its target set can be threaded into resolveValue/
	// resolveRef below (a $ref into a resource this same proposal is also
	// destroying is refused there, ErrRefToDestroyTarget).
	destroyByKey, destroyOrder, err := parseDestroyBatch(l, intent.Destroys, batch)
	if err != nil {
		return nil, err
	}
	destroySet := destroyAddrSet(destroyByKey)

	var keyErrs []error
	for _, key := range order {
		e := batch[key]
		var raw interface{}
		if err := json.Unmarshal(e.rawValue(), &raw); err != nil {
			return nil, fmt.Errorf("resolve %s: decode %s: %w", e.addr, rawValueLabel(e), err)
		}
		// UBI-66: schema-key validation runs here, at resolve time,
		// against the exact schema InferProvider already resolved this
		// resource's own provider from -- collected across the WHOLE
		// batch (every wrong key, on every resource) before any of it is
		// returned, so a single refusal names every mistake at once
		// rather than one-at-a-time.
		//
		// Real, deliberate scope note (UBI-178): skipped entirely for a
		// data source entry. UnknownConfigKeys/MissingRequiredKeys both
		// validate against a RESOURCE's own create-shaped schema
		// (SchemaInspector's real, only implementation today) -- no
		// provider this pipeline talks to publishes a schema for a data
		// source's own lookup-parameter shape yet (discovery, UBI-186,
		// found the real candidates; serving that schema over a real
		// provider RPC is separate, unbuilt work). A malformed lookup
		// still fails loudly, just later, at the real read below
		// (ErrDataSourceReadFailed), rather than being silently
		// pretended validated here against a schema that doesn't exist.
		if !e.isDataSource() {
			if configMap, ok := raw.(map[string]interface{}); ok {
				for _, issue := range e.provider.Schema.UnknownConfigKeys(e.ri.Type, configMap) {
					keyErrs = append(keyErrs, configKeyError{addr: e.addr, typeName: e.ri.Type, issue: issue})
				}
				// UBI-90: the same batch, the same schema, the same "collect
				// everything, join into one refusal" discipline -- confirms
				// EVERY Required attribute actually has a value, not just that
				// every present key is a real one. Runs for both create and
				// modify (a modify's own drafted config is expected to
				// reproduce every currently-recorded attribute unchanged
				// unless intentionally changed, UBI-85's own "full-state
				// config, matching create's own convention" -- a Required
				// attribute is never also Computed in a real schema, so this
				// never conflicts with the modify-only Computed-attribute
				// auto-fill below).
				for _, issue := range e.provider.Schema.MissingRequiredKeys(e.ri.Type, configMap) {
					keyErrs = append(keyErrs, requiredAttributeError{addr: e.addr, typeName: e.ri.Type, issue: issue})
				}
			}
		}
		edges, err := scanRefEdges(raw, batch)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
		}
		var dependsOnHint []string
		if !e.isDataSource() {
			dependsOnHint = e.ri.DependsOn
		}
		edges, err = unionDependsOn(edges, dependsOnHint, batch, destroySet, l, e.addr)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
		}
		e.rawEdges = edges
	}
	if len(keyErrs) > 0 {
		return nil, errors.Join(keyErrs...)
	}

	topoOrder, err := topoSort(order, func(key string) []string { return batch[key].rawEdges })
	if err != nil {
		return nil, err
	}

	var resolutionInputs []core.ResolutionInput
	for _, key := range topoOrder {
		e := batch[key]
		var raw interface{}
		if err := json.Unmarshal(e.rawValue(), &raw); err != nil {
			return nil, fmt.Errorf("resolve %s: decode %s: %w", e.addr, rawValueLabel(e), err)
		}
		resolvedVal, inputs, err := resolveValue(raw, "", e.typeName(), e.addr.String(), l, e.provider.Schema, batch, destroySet)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
		}
		resolvedMap, ok := resolvedVal.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("resolve %s: %s must be a JSON object", e.addr, rawValueLabel(e))
		}
		resolutionInputs = append(resolutionInputs, inputs...)

		if !e.isDataSource() {
			e.resolvedConfig = resolvedMap
			continue
		}

		// UBI-178: a data source's own real read happens HERE, in topo
		// order, interleaved with ordinary value resolution -- not in
		// the create/modify loop below, and not deferred to after this
		// loop finishes. This is load-bearing, not a style choice:
		// resolveRef (refs.go) dot-gets into a REFERENCED entry's own
		// resolvedConfig, and topo order only guarantees a dependency's
		// resolvedConfig is set before a DEPENDENT's own value resolves
		// -- if the real read happened in a later, separate pass, a
		// sibling resource's own $ref into this data source's result
		// would find resolvedConfig still nil and hit resolveRef's own
		// "referenced before it was resolved (topo order bug)" internal
		// error, even though nothing is actually wrong.
		//
		// Unlike a resource's own resolvedConfig (the submitted create
		// config -- a partial, desired-state view; genuinely
		// schema-Computed attributes like "id" are deferred to $computed,
		// docs/schema.md's own deferred-materialization amendment), a
		// data source's resolvedConfig is set to the FULL real observed
		// read result: nothing about a data source is ever "submitted,"
		// every attribute is equally already-known the moment the read
		// returns, so a sibling's $ref into ANY of its attributes always
		// resolves to a real, concrete value immediately -- see
		// resolveRef's own `target.di == nil` guard for the other half
		// of this (never defers a data source's own attribute to
		// $computed, since there is nothing to defer).
		if containsMarker(resolvedMap) {
			return nil, fmt.Errorf("%w: %s", ErrDataSourceLookupNotConcrete, e.addr)
		}
		resolvedLookupBytes, err := json.Marshal(resolvedMap)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: marshal resolved lookup: %w", e.addr, err)
		}
		if e.provider.Reader == nil {
			return nil, fmt.Errorf("%w: %s: provider %q has no live read access for this resolve call -- a stack with data_sources[] needs a caller that keeps a real connection open (docs/schema.md's own \"Amendment: data sources\")", ErrDataSourceReadFailed, e.addr, e.provider.Source)
		}
		// context.Background(), not a caller-supplied ctx: Resolve has
		// never taken a context parameter (a deliberate "pure
		// computation over already-fetched inputs" signature, ~85 real
		// call sites across this package's own tests plus 4 real
		// production callers) -- threading one through now, for a
		// capability the overwhelming majority of stacks don't use at
		// all, would touch every one of them for a real but secondary
		// benefit (mid-read cancellation). A real, named, deliberately
		// deferred gap, not hidden: a data source read currently cannot
		// be interrupted by a caller's own ctx cancellation the way
		// other real I/O in this codebase can.
		observed, hash, err := dsCache.get(context.Background(), e.provider.Reader, e.addr, e.provider.Source, e.provider.ProviderConfig, resolvedLookupBytes)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrDataSourceReadFailed, e.addr, err)
		}
		var observedMap map[string]interface{}
		if err := json.Unmarshal(observed, &observedMap); err != nil {
			return nil, fmt.Errorf("%w: %s: provider returned a non-object read result: %w", ErrDataSourceReadFailed, e.addr, err)
		}
		e.resolvedConfig = observedMap
		resolutionInputs = append(resolutionInputs, core.ResolutionInput{
			Kind:         "data_source",
			Resource:     e.addr.String(),
			ObservedHash: hash,
			Lookup:       resolvedLookupBytes,
		})
	}

	var creates []json.RawMessage
	var modifies []core.Modification
	for _, key := range topoOrder {
		e := batch[key]
		// A data source entry already produced its own real
		// resolution.inputs entry above (the only output it ever has --
		// there is nothing to create or modify, docs/schema.md's own
		// "Amendment: data sources": "resolves into a resolution.inputs[]
		// entry only").
		if e.isDataSource() {
			continue
		}
		dependsOn := append([]string(nil), e.rawEdges...)
		sort.Strings(dependsOn)

		switch e.ri.Op {
		case OpCreate:
			node := map[string]interface{}{
				"stack":    e.addr.Stack,
				"type":     e.addr.Type,
				"name":     e.addr.Name,
				"provider": core.ProviderRef{Source: e.provider.Source, Version: e.provider.Version},
				"config":   e.resolvedConfig,
			}
			if len(dependsOn) > 0 {
				node["depends_on"] = dependsOn
			}
			if len(e.ri.Sources) > 0 {
				node["sources"] = e.ri.Sources
			}
			b, err := json.Marshal(node)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			creates = append(creates, b)

		case OpModify:
			current, _, err := l.FoldState(e.addr)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			// UBI-85: a modify's own config is expected to reproduce every
			// currently-recorded attribute unchanged unless the intent
			// actually changes it (an intent provider's own system prompt
			// now instructs this explicitly for a drafted modify) -- but an
			// LLM's own compliance with that instruction isn't 100%
			// reliable, confirmed LIVE: a real Claude response correctly
			// detected and changed the one attribute that actually
			// differed, but genuinely omitted an unrelated, schema-
			// Computed "id" attribute from its own modify config anyway,
			// despite the explicit instruction to reproduce it -- which
			// DiffAttributes would otherwise read as "id: removed," a
			// spurious diff entry, not the clean before/after diff this
			// feature exists to produce. A schema-Computed attribute is
			// never something a human OR a model is expected to set in the
			// first place (a real provider computes it, nothing else may),
			// so silently auto-filling one back in from the ledger's own
			// current recorded value whenever a modify's resolved config
			// omits it entirely is always safe -- it can never mask a
			// genuine, intentional change, since there is no such thing as
			// intentionally "changing" a computed attribute by naming a
			// value for it. This is deterministic Go code guaranteeing
			// what prompt-engineering alone can't fully guarantee, matching
			// this project's own standing "the LLM reasons about intent,
			// deterministic code guarantees mechanical correctness" split
			// -- never applied to CREATE, which legitimately omits
			// computed attributes since they don't exist yet, and never
			// applied to a non-Computed attribute a modify's config omits,
			// which stays exactly as drafted (an intentional decision, or
			// the model's own responsibility per its own instructions --
			// not this resolver's to silently second-guess).
			if len(current) > 0 {
				var currentMap map[string]interface{}
				if err := json.Unmarshal(current, &currentMap); err == nil {
					for k, v := range currentMap {
						if _, present := e.resolvedConfig[k]; present {
							continue
						}
						if e.provider.Schema.IsComputed(e.ri.Type, k) {
							e.resolvedConfig[k] = v
						}
					}
				}
			}
			resolvedBytes, err := json.Marshal(e.resolvedConfig)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			before, after, err := core.DiffAttributes(current, resolvedBytes)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			// UBI-88: current is full ledger state (every schema key
			// present, possibly null); resolvedBytes is a partial drafted
			// config that legitimately omits an attribute nobody touched.
			// Without this, an attribute the ledger recorded as explicit
			// null renders as "<attr>: null -> (absent)" -- representation
			// noise, the SAME null<->absent equivalence class UBI-63 fixed
			// for drift comparison, just never applied to a modify's own
			// diff before now. Also strips the identical null<->zero-value/
			// materialization noise `ubx status --drift` already never
			// shows.
			before, after = core.FilterNormalizationNoise(before, after, schemaComputedAdapter{schema: e.provider.Schema, typeName: e.ri.Type})
			observedHash, err := core.ObservedHash(current)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			lookup, found, err := l.LastLookup(e.addr)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
			}
			if !found {
				return nil, fmt.Errorf("%w: %s", ErrModifyTargetNoLookup, e.addr)
			}
			modifies = append(modifies, core.Modification{
				Target:    e.addr,
				Before:    before,
				After:     after,
				DependsOn: dependsOn,
				Provider:  &core.ProviderRef{Source: e.provider.Source, Version: e.provider.Version},
			})
			resolutionInputs = append(resolutionInputs, core.ResolutionInput{
				Kind:         "live_state",
				Resource:     e.addr.String(),
				ObservedHash: observedHash,
				Lookup:       lookup,
			})
		}
	}

	destroys, destroyInputs, err := resolveDestroys(l, destroyByKey, destroyOrder, batch, knownDependents, providers)
	if err != nil {
		return nil, err
	}
	resolutionInputs = append(resolutionInputs, destroyInputs...)

	head, err := l.Head()
	if err != nil {
		return nil, fmt.Errorf("resolve: %w", err)
	}

	return &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         intent.Stack,
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        intent.Intent,
		Delta: core.Delta{
			Creates:  creates,
			Modifies: modifies,
			Destroys: destroys,
		},
		Resolution: core.Resolution{
			ResolvedAt: resolvedAt,
			Inputs:     resolutionInputs,
		},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: core.BlastRadius{
			Creates:  int64(len(creates)),
			Modifies: int64(len(modifies)),
			Destroys: int64(len(destroys)),
		},
		Status: core.StatusDraft,
	}, nil
}
