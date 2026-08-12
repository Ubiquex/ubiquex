// Package sdk is the Go half of UBI-33's own multi-language SDK
// contract (docs/sdk.md, "The Go evaluator: decided empirically") --
// stack/resource/secret/cross/intent, semantically mirroring
// sdk/ts/runtime/src/index.ts's @ubx/sdk. A program using this package
// never computes, never reaches a provider, never touches a ledger -- it
// describes a desired end-state (an intent/v1 document) and stops.
//
// Unlike @ubx/sdk, this package does not run inside a sandboxed
// interpreter alongside the program that imports it -- the whole Go
// binary (this package linked in, statically) IS the sandboxed unit,
// hermeticity coming from process-level restriction (sandbox-exec on
// macOS, bubblewrap on Linux) applied to the compiled binary itself, not
// from a language-level permission system. See docs/sdk.md for the full
// empirical account of why that's sound.
package sdk

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
)

// ---------------------------------------------------------------------
// Computed: a reference, never a value.
//
// TypeScript's Computed<T> is a Proxy that lets `primary.settings.enabled`
// keep drilling into a nested attribute and still produce a valid,
// traceable address. Go has no Proxy -- the direct mirror is an explicit
// Field() method rather than implicit property access. This is a real,
// named simplification versus TS, not an oversight (docs/sdk.md).
// ---------------------------------------------------------------------

// Computed is what Resource()'s own return value is -- never a real
// attribute value (nothing has been created yet at describe time), only
// a reference to where a value will eventually be. Identified by Go type
// (a type assertion to *Computed), never by structural duck-typing --
// the same non-negotiable identity check @ubx/sdk's own isComputed uses.
type Computed struct {
	address string
}

func newComputed(address string) *Computed {
	return &Computed{address: address}
}

// Field drills into one nested attribute of a Computed reference,
// returning a new Computed addressed one level deeper -- the explicit
// Go equivalent of `primary.settings.enabled` in TS.
func (c *Computed) Field(name string) *Computed {
	return &Computed{address: c.address + "." + name}
}

// Address returns this Computed reference's own resolved address string
// (e.g. "payments.aws_db_instance.payments.id").
func (c *Computed) Address() string {
	return c.address
}

// ---------------------------------------------------------------------
// Secret/Cross: thin, typed front ends to existing, already-pinned wire
// markers (docs/schema.md) -- no new wire shape either introduces.
// ---------------------------------------------------------------------

// SecretMarker is Secret()'s own return value -- serializes to the exact
// {"$secret": {"backend", "path"}} marker docs/schema.md pins.
type SecretMarker struct {
	Backend string
	Path    string
}

// Secret builds a {"$secret": {"backend", "path"}} marker.
func Secret(backend, path string) SecretMarker {
	return SecretMarker{Backend: backend, Path: path}
}

// CrossMarker is Cross()'s/CrossStack()'s own return value -- serializes
// to the exact {"$cross": {"ledger_dir", "to"}} or {"$cross": {"stack",
// "to"}} marker docs/schema.md pins (core/resolver/refs.go's own
// resolveCross accepts exactly one of the two, never both -- Stack and
// LedgerDir are mutually exclusive here for the identical reason, and
// exactly one constructor below ever sets either). to is a hand-typed
// address string in v1, the same posture TS's own cross() has
// (docs/sdk.md's own "Out of scope": a typed cross-stack handle is
// real, useful, deferred future work).
type CrossMarker struct {
	LedgerDir string
	Stack     string
	To        string
}

// Cross builds a {"$cross": {"ledger_dir", "to"}} marker -- an explicit
// path to the neighbor's own ledger, known and fixed at the call site.
func Cross(ledgerDir, to string) CrossMarker {
	return CrossMarker{LedgerDir: ledgerDir, To: to}
}

// CrossStack builds a {"$cross": {"stack", "to"}} marker (UBI-32 Arc B's
// own "resolve by NAME against the base," docs/architecture.md --
// Addressing) -- the neighbor is named by stack, resolved against
// whichever ledger this program's own intent ultimately runs against
// (its [ledger.external] entries or base store), never a filesystem/URL
// path baked into the program itself. This is the form UBI-134's own
// blueprint cross_ref params need: a blueprint is built once and called
// from many different stacks/environments, so a call-site "@<stack>.
// <type>.<name>" reference (matching diagram/crossref.go's own "@"
// grammar for a reference node's label) can only ever name a neighbor by
// stack, never by a ledger_dir path the blueprint has no way to know at
// build time.
func CrossStack(stack, to string) CrossMarker {
	return CrossMarker{Stack: stack, To: to}
}

// ---------------------------------------------------------------------
// The codegen'd binding shape -- what a `ubx sdk gen --lang go`-produced
// package actually exports for Resource() to consume
// (sdk/codegen/templates/go). Mirrors @ubx/sdk's own FieldSpec/FieldMap/
// ResourceBinding, keyed by the Go Config struct's own exported field
// name (PascalCase) rather than TS's camelCase JS property name.
// ---------------------------------------------------------------------

// FieldSpec is one settable config field's own wire-name mapping. Kind
// == "" means a scalar (or list/set/map-of-scalar) leaf -- WireName is
// used directly, Fields is unused. A non-empty Kind ("object", "list",
// "set", "map") names a nested object (or a list/set/map of nested
// objects) needing its own Fields walked recursively.
type FieldSpec struct {
	WireName string
	Kind     string
	Fields   FieldMap
}

// FieldMap maps a Config struct's own exported Go field name to its
// FieldSpec.
type FieldMap map[string]FieldSpec

// ResourceBinding is what a codegen'd package exports per resource type
// (e.g. AwsDbInstance) -- WireType for resources[].type, Fields for
// Go-field-name -> wire-name mapping at evaluation time.
type ResourceBinding struct {
	WireType string
	Fields   FieldMap
}

// ---------------------------------------------------------------------
// intent/v1 document shape (docs/schema.md's own ubx:intent/v1) -- only
// the fields this runtime actually populates.
// ---------------------------------------------------------------------

// IntentSource is one entry in an emitted document's intent.sources.
type IntentSource struct {
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	ContentHash string `json:"content_hash,omitempty"`
}

// IntentInfo is Intent()'s own argument shape.
type IntentInfo struct {
	Summary string
	Sources []IntentSource
}

type intentDoc struct {
	SchemaVersion int              `json:"schema_version"`
	Kind          string           `json:"kind"`
	Stack         string           `json:"stack"`
	Intent        intentDocIntent  `json:"intent"`
	Resources     []intentResource `json:"resources"`
	Overrides     []intentOverride `json:"overrides,omitempty"`
}

// intentOverride is one Override() call's own wire shape -- mirrors
// resolver.Override exactly (core/resolver/resolver.go): Config keys are
// the target resource's own real WIRE attribute names, never this SDK's
// own PascalCase Go Config-struct field names -- there is no
// ResourceBinding in play at override time (the target is an
// already-resolved resource reached by address, not a freshly-declared
// one), so there is no FieldMap to translate through, unlike Resource().
type intentOverride struct {
	Address string         `json:"address"`
	Config  map[string]any `json:"config"`
}

type intentDocIntent struct {
	Summary string         `json:"summary"`
	Sources []IntentSource `json:"sources"`
}

type intentResource struct {
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Op      string         `json:"op"`
	Config  any            `json:"config"`
	Sources []IntentSource `json:"sources,omitempty"`
}

// ---------------------------------------------------------------------
// Stack/Resource/Intent: the describe-only surface itself.
// ---------------------------------------------------------------------

// StackDefinition is Stack()'s own return value -- Evaluate() runs the
// stack's own describe function exactly once and returns the resulting
// intent/v1 document (or the error a describe-time failure produced;
// docs/sdk.md's own adversarial row: no partial intent/v1 is ever
// emitted).
type StackDefinition struct {
	name string
	fn   func()
}

// Stack declares one stack's own describe function. name must be
// non-empty.
func Stack(name string, fn func()) *StackDefinition {
	if strings.TrimSpace(name) == "" {
		panic("sdk.Stack: name is required")
	}
	return &StackDefinition{name: name, fn: fn}
}

type collector struct {
	stackName     string
	resources     []intentResource
	overrides     []intentOverride
	seenAddresses map[string]bool
	intentInfo    *intentDocIntent
}

func newCollector(stackName string) *collector {
	return &collector{stackName: stackName, seenAddresses: map[string]bool{}}
}

func (c *collector) setIntent(summary string, sources []IntentSource) {
	if c.intentInfo != nil {
		panic("sdk.Intent: called more than once in a single Stack() body -- there is exactly one summary per stack")
	}
	if strings.TrimSpace(summary) == "" {
		panic("sdk.Intent: summary is required and cannot be empty")
	}
	if sources == nil {
		sources = []IntentSource{}
	}
	c.intentInfo = &intentDocIntent{Summary: summary, Sources: sources}
}

func (c *collector) addResource(binding ResourceBinding, name string, config any) *Computed {
	if strings.TrimSpace(name) == "" {
		panic(fmt.Sprintf("sdk.Resource: name is required (type %s)", binding.WireType))
	}
	address := c.stackName + "." + binding.WireType + "." + name
	if c.seenAddresses[address] {
		panic(fmt.Sprintf("sdk.Resource: duplicate resource %s %q in stack %q", binding.WireType, name, c.stackName))
	}
	c.seenAddresses[address] = true

	serialized := serializeConfig(binding.Fields, config, address)
	ir := intentResource{Type: binding.WireType, Name: name, Op: "create", Config: serialized}
	if len(blueprintSourceStack) > 0 {
		// UBI-126: this Resource() call is executing from inside a
		// compiled blueprint's own generated function body (pushBlueprintSource
		// below, called only by ubx blueprint build's own generated code --
		// never by a stack author directly). The innermost (most recently
		// pushed) scope wins, matching ordinary lexical-scoping intuition
		// and staying correct if a blueprint ever calls another blueprint
		// (nesting itself is UBI-121, unbuilt, but this doesn't need to
		// special-case its absence). Ref is deliberately the blueprint's
		// own bare declared name here, NOT yet "name:content_hash" -- this
		// process has no way to compute a real content hash for itself
		// (the compiled binary doesn't know its own on-disk blueprint
		// directory, and baking a hash into source that gets hashed
		// itself is circular) -- ubx resolve's own external
		// StampDirectCallProvenance step (blueprint package) resolves the
		// bare name to a real content hash afterward, the same way
		// blueprint.ExpandCalls already computes one externally for
		// diagram/md calls, just at a different pipeline stage.
		ir.Sources = []IntentSource{{Kind: "blueprint", Ref: blueprintSourceStack[len(blueprintSourceStack)-1]}}
	}
	c.resources = append(c.resources, ir)

	return newComputed(address)
}

// addOverride records one Override() call -- UBI-86 Part 2's own
// mechanism: a caller-declared attribute patch against an already-named
// resource, applied AFTER a blueprint call resolves and BEFORE ship
// (blueprint.ApplyOverrides, the `blueprint` package -- this runtime
// stays dependency-free of it, the same "sdk never imports a leaf
// package" discipline every other seam in this codebase already
// follows). config's own values are serialized via serializeOpaque, not
// serializeConfig -- there is no ResourceBinding/FieldMap for the TARGET
// resource in scope here (it may not even be declared in THIS document
// at all -- the common case is a blueprint-produced resource), so
// Computed/Secret/Cross markers are still recognized and nested
// structures still walk correctly, but map keys pass through completely
// unchanged as the real wire attribute names the caller wrote.
func (c *collector) addOverride(address string, config map[string]any) {
	if strings.TrimSpace(address) == "" {
		panic("sdk.Override: address is required")
	}
	if len(config) == 0 {
		panic(fmt.Sprintf("sdk.Override: %q: config is required and cannot be empty", address))
	}
	serialized := make(map[string]any, len(config))
	for k, v := range config {
		serialized[k] = serializeOpaque(v, address)
	}
	c.overrides = append(c.overrides, intentOverride{Address: address, Config: serialized})
}

// blueprintSourceStack is UBI-126's own fix: a compiled blueprint's own
// generated function (blueprint/gogen.go's own renderGoFunction) wraps
// its entire body in PushBlueprintSource(name)/defer PopBlueprintSource()
// -- an implicit, package-level side channel exactly like `current`
// above (never a new sdk.Resource parameter, which would force every
// existing caller, blueprint or not, to change), so a stack author
// calling a blueprint's exported function directly needs to do nothing
// different at all: the scope is entirely internal to code `ubx
// blueprint build` itself generates. A plain stack that never imports a
// blueprint never pushes anything, so blueprintSourceStack stays empty
// and intentResource.Sources stays unset -- zero wire-format change for
// the overwhelming majority of ordinary SDK programs.
var blueprintSourceStack []string

// PushBlueprintSource marks every sdk.Resource() call for the duration
// of the current scope (until the matching PopBlueprintSource) as
// produced by the blueprint named name -- name is the blueprint's own
// declared name (Ubxfile directory basename, the SAME identifier
// buildManifest/ubx why/ubx render already use), never a Go
// module/import path, and never (cannot be, see addResource's own
// comment) a content hash. Called only by generated blueprint code, never
// meant to be called by a stack author's own code directly.
func PushBlueprintSource(name string) {
	blueprintSourceStack = append(blueprintSourceStack, name)
}

// PopBlueprintSource ends the innermost still-open PushBlueprintSource
// scope. Panics on an unbalanced call (more pops than pushes) -- a real
// bug in generated code, not something to silently tolerate.
func PopBlueprintSource() {
	if len(blueprintSourceStack) == 0 {
		panic("sdk.PopBlueprintSource: called with no matching PushBlueprintSource -- this should only ever be called by ubx blueprint build's own generated code")
	}
	blueprintSourceStack = blueprintSourceStack[:len(blueprintSourceStack)-1]
}

func (c *collector) finish() (*intentDoc, error) {
	if c.intentInfo == nil {
		return nil, fmt.Errorf("sdk.Stack(%q): Intent() was never called -- a missing summary is a collection-time hard failure", c.stackName)
	}
	resources := c.resources
	if resources == nil {
		resources = []intentResource{}
	}
	return &intentDoc{
		SchemaVersion: 1,
		Kind:          "ubx:intent/v1",
		Stack:         c.stackName,
		Intent:        *c.intentInfo,
		Resources:     resources,
		Overrides:     c.overrides,
	}, nil
}

var current *collector

// Evaluate runs this stack's own describe function exactly once and
// returns the resulting intent/v1 document. A panic during fn (an
// invariant violation -- duplicate resource, missing intent, an
// unrepresentable config value) is recovered and returned as an error,
// never a partial document.
func (s *StackDefinition) Evaluate() (doc *intentDoc, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc = nil
			err = fmt.Errorf("%v", r)
		}
	}()

	c := newCollector(s.name)
	previous := current
	current = c
	defer func() { current = previous }()

	s.fn()
	return c.finish()
}

func requireCollector(caller string) *collector {
	if current == nil {
		panic(fmt.Sprintf("sdk.%s: called outside of an active Stack() evaluation", caller))
	}
	return current
}

// Resource declares one resource -- always op "create" in v1 (a
// hermetic, describe-only program has no way to read ledger state to
// know a resource already exists, so it has no way to express "modify"
// intent -- the same deliberate scope limit TS's own resource() has).
// Returns a *Computed handle for wiring into sibling resources' own
// config.
func Resource(binding ResourceBinding, name string, config any) *Computed {
	return requireCollector("Resource").addResource(binding, name, config)
}

// Intent populates the emitted document's own intent.summary/sources.
// Required, called at most once per Stack() body.
func Intent(info IntentInfo) {
	requireCollector("Intent").setIntent(info.Summary, info.Sources)
}

// Override declares a caller-owned attribute patch against
// address (the canonical "<stack>.<type>.<name>" form,
// core.Address.String()'s own shape) -- UBI-86 Part 2's own mechanism,
// zero AI, a direct function call exactly like Resource(). Config keys
// are the target resource's own real wire attribute names (e.g.
// "message_retention_seconds"), not this SDK's own PascalCase Go
// Config-struct field names -- there is no ResourceBinding for the
// TARGET resource in scope here, since it need not even be declared in
// this same document (the motivating case: overriding a field inside a
// called blueprint's own internal, unparameterized resource). Applied by
// blueprint.ApplyOverrides after any blueprint call in this document has
// already resolved, before ship -- see docs/blueprint.md's own "Override
// mechanism" section for the full design record.
func Override(address string, config map[string]any) {
	requireCollector("Override").addOverride(address, config)
}

// Main is the standard entry point a Go SDK program's own main() calls:
// evaluates def, and on success writes the resulting intent/v1 document
// to stdout as JSON, on failure writes the error to stderr and exits 1.
// This IS the evaluator's own subprocess entry -- unlike TS, there is no
// separate generated-runner-script indirection, because the compiled
// binary already is the whole program.
func Main(def *StackDefinition) {
	doc, err := def.Evaluate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------
// The recursive config serializer -- a Config struct's own exported
// field names, mapped back to the provider's real wire names via the
// binding's own FieldMap, recursively for nested objects/lists/sets/
// maps of objects. Mirrors sdk/ts/runtime's serializeConfig/
// serializeOpaque split exactly, walked via reflection instead of
// Object.entries() (Go has no dynamic object-literal introspection
// without it).
// ---------------------------------------------------------------------

func serializeGenericOrMarker(value any, addressForErrors string) (result any, handled bool) {
	if value == nil {
		return nil, true
	}
	switch v := value.(type) {
	case *Computed:
		return map[string]any{"$ref": map[string]any{"to": v.address}}, true
	case SecretMarker:
		return map[string]any{"$secret": map[string]any{"backend": v.Backend, "path": v.Path}}, true
	case CrossMarker:
		if v.Stack != "" {
			return map[string]any{"$cross": map[string]any{"stack": v.Stack, "to": v.To}}, true
		}
		return map[string]any{"$cross": map[string]any{"ledger_dir": v.LedgerDir, "to": v.To}}, true
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Func, reflect.Chan, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
		panic(fmt.Sprintf("resource %q: a %s cannot appear in a resource's own config -- intent/v1 has no representation for it", addressForErrors, rv.Kind()))
	case reflect.Ptr:
		if rv.IsNil() {
			return nil, true
		}
		return serializeGenericOrMarker(rv.Elem().Interface(), addressForErrors)
	case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array:
		return nil, false
	default:
		// string, bool, every int/uint/float width -- ordinary scalars,
		// pass through as-is.
		return value, true
	}
}

// serializeConfig walks a value whose own shape IS described by fields
// (the top-level resource Config struct, or a nested field typed as a
// generated nested struct) -- every exported field is translated from
// its Go identifier to the provider's real wire name via fields,
// recursively. An unset field (nil interface value) is omitted from the
// output entirely, matching TS's own "only keys actually present in the
// object literal are walked."
func serializeConfig(fields FieldMap, value any, addressForErrors string) any {
	generic, handled := serializeGenericOrMarker(value, addressForErrors)
	if handled {
		return generic
	}

	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		// A field-map-shaped value reaching this point as a slice means
		// the caller bypassed the generated Config type -- defensively
		// walk each element opaquely rather than guessing at a field map
		// for it that was never named (mirrors sdk/ts/runtime's own
		// defensive branch in serializeConfig).
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, serializeOpaque(rv.Index(i).Interface(), addressForErrors))
		}
		return out
	}

	if rv.Kind() != reflect.Struct {
		panic(fmt.Sprintf("resource %q: expected a struct-shaped config value, got %s", addressForErrors, rv.Kind()))
	}

	rt := rv.Type()
	out := map[string]any{}
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if field.PkgPath != "" {
			continue // unexported
		}
		fieldValue := rv.Field(i)
		if fieldValue.Kind() == reflect.Interface && fieldValue.IsNil() {
			continue // not set -- omitted, matching TS's "key not present at all"
		}

		spec, ok := fields[field.Name]
		if !ok {
			panic(fmt.Sprintf("resource %q: unrecognized config field %q -- not present in this resource type's own generated binding", addressForErrors, field.Name))
		}
		out[spec.WireName] = serializeFieldValue(spec, fieldValue.Interface(), addressForErrors, field.Name)
	}
	return out
}

// serializeFieldValue serializes one already-looked-up field's own raw
// value, dispatching on its FieldSpec.
func serializeFieldValue(spec FieldSpec, raw any, addressForErrors, fieldName string) any {
	switch spec.Kind {
	case "":
		return serializeOpaque(raw, addressForErrors)
	case "object":
		return serializeConfig(spec.Fields, raw, addressForErrors)
	case "list", "set":
		generic, handled := serializeGenericOrMarker(raw, addressForErrors)
		if handled {
			return generic
		}
		rv := reflect.ValueOf(raw)
		for rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			panic(fmt.Sprintf("resource %q: field %q expects a slice", addressForErrors, fieldName))
		}
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, serializeConfig(spec.Fields, rv.Index(i).Interface(), addressForErrors))
		}
		return out
	case "map":
		generic, handled := serializeGenericOrMarker(raw, addressForErrors)
		if handled {
			return generic
		}
		rv := reflect.ValueOf(raw)
		for rv.Kind() == reflect.Ptr {
			rv = rv.Elem()
		}
		if rv.Kind() != reflect.Map {
			panic(fmt.Sprintf("resource %q: field %q expects a map", addressForErrors, fieldName))
		}
		out := map[string]any{}
		iter := rv.MapRange()
		for iter.Next() {
			out[fmt.Sprint(iter.Key().Interface())] = serializeConfig(spec.Fields, iter.Value().Interface(), addressForErrors)
		}
		return out
	default:
		panic(fmt.Sprintf("resource %q: field %q has unrecognized FieldSpec kind %q", addressForErrors, fieldName, spec.Kind))
	}
}

// serializeOpaque walks a value with NO field-map-driven structure at
// all -- a scalar, or (recursively) anything nested inside a scalar/
// list/set/map-of-scalar field, e.g. a Tags map[string]string field's
// own arbitrary, user-chosen map keys. Map/slice keys and shape pass
// through completely unchanged; only Computed/marker recognition and
// representability are checked.
func serializeOpaque(value any, addressForErrors string) any {
	generic, handled := serializeGenericOrMarker(value, addressForErrors)
	if handled {
		return generic
	}

	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, serializeOpaque(rv.Index(i).Interface(), addressForErrors))
		}
		return out
	case reflect.Map:
		out := map[string]any{}
		iter := rv.MapRange()
		for iter.Next() {
			out[fmt.Sprint(iter.Key().Interface())] = serializeOpaque(iter.Value().Interface(), addressForErrors)
		}
		return out
	case reflect.Struct:
		rt := rv.Type()
		out := map[string]any{}
		for i := 0; i < rt.NumField(); i++ {
			field := rt.Field(i)
			if field.PkgPath != "" {
				continue
			}
			fv := rv.Field(i)
			if fv.Kind() == reflect.Interface && fv.IsNil() {
				continue
			}
			out[field.Name] = serializeOpaque(fv.Interface(), addressForErrors)
		}
		return out
	default:
		panic(fmt.Sprintf("resource %q: unrepresentable config value of kind %s", addressForErrors, rv.Kind()))
	}
}
