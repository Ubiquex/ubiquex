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
	Destroys      []string         `json:"destroys,omitempty"`
}

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
type ResourceIntent struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Op        string          `json:"op"`
	Config    json.RawMessage `json:"config"`
	Provider  *ProviderHint   `json:"provider,omitempty"`
	DependsOn []string        `json:"depends_on,omitempty"`
}

const (
	OpCreate = "create"
	OpModify = "modify"
)

var (
	// ErrUnknownIntentKind means IntentFile.Kind isn't "ubx:intent/v1".
	ErrUnknownIntentKind = errors.New("resolve: unrecognized intent file kind")

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
	addr           core.Address
	provider       DeclaredProvider // which declared provider owns ri.Type, set before any value resolution (docs/resolver.md's own amendment)
	rawEdges       []string         // canonical addresses this entry's raw $ref targets, restricted to this batch
	resolvedConfig map[string]interface{}
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
	_, err := core.DoubleRun(func() ([]byte, error) {
		p, err := resolveOnce(l, providers, intent, knownDependents, resolvedAt)
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

func resolveOnce(l *core.Ledger, providers []DeclaredProvider, intent *IntentFile, knownDependents []string, resolvedAt string) (*core.Proposal, error) {
	if intent.Kind != IntentFileKind {
		return nil, fmt.Errorf("%w: got %q", ErrUnknownIntentKind, intent.Kind)
	}

	batch := make(map[string]*batchEntry, len(intent.Resources))
	order := make([]string, 0, len(intent.Resources))

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
		if err := json.Unmarshal(e.ri.Config, &raw); err != nil {
			return nil, fmt.Errorf("resolve %s: decode config: %w", e.addr, err)
		}
		// UBI-66: schema-key validation runs here, at resolve time,
		// against the exact schema InferProvider already resolved this
		// resource's own provider from -- collected across the WHOLE
		// batch (every wrong key, on every resource) before any of it is
		// returned, so a single refusal names every mistake at once
		// rather than one-at-a-time.
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
		edges, err := scanRefEdges(raw, batch)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
		}
		edges, err = unionDependsOn(edges, e.ri.DependsOn, batch, destroySet, l, e.addr)
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
		if err := json.Unmarshal(e.ri.Config, &raw); err != nil {
			return nil, fmt.Errorf("resolve %s: decode config: %w", e.addr, err)
		}
		resolvedVal, inputs, err := resolveValue(raw, "", e.ri.Type, e.addr.String(), l, e.provider.Schema, batch, destroySet)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", e.addr, err)
		}
		resolvedMap, ok := resolvedVal.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("resolve %s: config must be a JSON object", e.addr)
		}
		e.resolvedConfig = resolvedMap
		resolutionInputs = append(resolutionInputs, inputs...)
	}

	var creates []json.RawMessage
	var modifies []core.Modification
	for _, key := range topoOrder {
		e := batch[key]
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
