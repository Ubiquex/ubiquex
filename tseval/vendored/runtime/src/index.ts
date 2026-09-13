// @ubx/sdk -- the describe-only runtime surface docs/sdk.md pins:
// stack/resource/secret/cross/intent, Computed<T>, the collector. A
// program using this package never computes, never reaches a provider,
// never touches a ledger -- it describes a desired end-state (an
// intent/v1 document, docs/schema.md's own `ubx:intent/v1` shape) and
// stops. This module is executed INSIDE the hermetic Deno evaluator
// (docs/sdk.md's "The hermetic evaluator" section, slice 4, not yet
// built) alongside the program that imports it, so it deliberately never
// uses fs/net/env/subprocess itself -- doing so would throw exactly the
// same NotCapable error a malicious program's own attempt would (the
// sandbox does not distinguish "the runtime library" from "the program,"
// by design).

// ---------------------------------------------------------------------
// Computed<T>: a reference, never a value (docs/sdk.md's own section).
// ---------------------------------------------------------------------

const COMPUTED_BRAND: unique symbol = Symbol("ubx.Computed");

/**
 * Computed<T> is what resource()'s own return value's fields are typed
 * as -- NOT T. At evaluation time nothing has been created yet, so
 * (for example) a just-declared database's own `.id` genuinely has no
 * value to hand back; it can only ever be a reference to where a value
 * will eventually be (docs/resolver.md: a $ref resolves to either a
 * concrete literal or a $computed marker, depending on whether the
 * referenced attribute is schema-Computed -- a decision made later, by
 * the resolver, not here).
 *
 * Every field of a Computed<T> is itself Computed<T[K]> (recursively) --
 * this is what lets a program drill into a nested block's own sub-field
 * (`primary.settings.enabled`) and still get a valid, traceable address,
 * matching docs/sdk.md's own "needed so property access on a
 * Computed<object> ... still produces a valid, traceable address rather
 * than undefined."
 *
 * Deliberately has no method that produces a real T -- no .valueOf()
 * override, no coercion path (see ComputedCoercionError, below),
 * mirroring this project's existing, accepted stance that neither
 * `$ref`/`$computed` nor `secret()`'s own output carry a stronger
 * runtime guarantee against misuse (docs/resolver.md, Open decisions).
 */
export type Computed<T> = T extends object
  ? { readonly [K in keyof T]: Computed<T[K]> } & ComputedMarker
  : ComputedMarker;

interface ComputedMarker {
  readonly [COMPUTED_BRAND]: true;
}

/** Thrown when code tries to use a Computed<T> as a real JS value
 * (string concatenation, arithmetic, JSON.stringify, template
 * interpolation, ...) instead of only ever passing it through into
 * another resource()'s own config -- docs/sdk.md's own adversarial row
 * 5 requires this to be a clear, named, immediate failure, never a
 * silent "[object Object]"/NaN. */
export class ComputedCoercionError extends Error {
  constructor(address: string, attemptedOperation: string) {
    super(
      `Computed<T> value for "${address}" was used as a real JS value (${attemptedOperation}) -- ` +
        "a Computed<T> is a reference to a not-yet-created resource's own attribute, never a real " +
        "value; pass it through directly into another resource()'s own config field instead.",
    );
    this.name = "ComputedCoercionError";
  }
}

const computedProxies = new WeakSet<object>();

/** isComputed reports whether v is a real Computed<T> produced by this
 * module's own makeComputed -- checked by identity (WeakSet membership),
 * never by structural duck-typing, so a hand-rolled object that happens
 * to look like one is never mistaken for a real reference (docs/sdk.md's
 * own explicit requirement). */
export function isComputed(v: unknown): v is Computed<unknown> {
  return typeof v === "object" && v !== null && computedProxies.has(v as object);
}

// Symbols/well-known coercion hooks a JS engine or `%s`/template-literal
// context might invoke on an object without going through a plain string
// property access -- each one must fail loudly via ComputedCoercionError,
// never silently coerce.
const COERCION_TRAPS = new Set<PropertyKey>([
  Symbol.toPrimitive,
  "valueOf",
  "toString",
  "toJSON",
  Symbol.iterator,
]);

function makeComputed(address: string): Computed<unknown> {
  const target = { __ubxAddress: address };
  const proxy = new Proxy(target, {
    get(t, prop, receiver) {
      if (prop === "__ubxAddress") return t.__ubxAddress;
      if (COERCION_TRAPS.has(prop)) {
        throw new ComputedCoercionError(address, `accessed ${String(prop)}`);
      }
      if (typeof prop === "symbol") {
        return Reflect.get(t, prop, receiver);
      }
      return makeComputed(`${address}.${prop}`);
    },
  });
  computedProxies.add(proxy);
  return proxy as unknown as Computed<unknown>;
}

/** addressOf returns a real Computed<T>'s own resolved address string
 * (e.g. "payments.aws_db_instance.payments-db.id") -- used by the
 * collector's own serializer to build the {"$ref": {"to": ...}} marker;
 * throws if handed anything that isn't actually one of this module's own
 * Computed<T> proxies. */
export function addressOf(v: Computed<unknown>): string {
  if (!isComputed(v)) {
    throw new TypeError("addressOf: not a real Computed<T> value");
  }
  return (v as unknown as { __ubxAddress: string }).__ubxAddress;
}

// ---------------------------------------------------------------------
// secret()/cross(): thin, typed front ends to existing, already-pinned
// wire markers (docs/schema.md) -- no new wire shape either introduces.
// Both are typed loosely (T defaults to any) rather than checked against
// a specific field's own sensitivity -- this project has no stronger
// Secret<T> runtime/type check anywhere else either (docs/resolver.md,
// Open decisions), and inventing one just for these two constructors
// would be a stronger guarantee than the rest of the wire format has.
// ---------------------------------------------------------------------

export interface SecretMarker {
  readonly $secret: { readonly backend: string; readonly path: string };
}

/** secret(backend, path) builds the exact {"$secret": {"backend", "path"}}
 * marker docs/schema.md's UBI-27 intent-file amendment pins (corrected
 * this session, UBI-33/34, to match every real worked example -- see
 * that document's own "Amendment: intent files" section). */
export function secret<T = unknown>(backend: string, path: string): T {
  return { $secret: { backend, path } } as unknown as T;
}

/** CrossMarker is a union of $cross's two mutually exclusive real wire
 * shapes (core/resolver/refs.go's own resolveCross accepts exactly one
 * of ledger_dir/stack, never both) -- an explicit ledger_dir path, or a
 * stack resolved by NAME against the base store (UBI-32 Arc B,
 * docs/architecture.md -- Addressing). */
export interface CrossMarker {
  readonly $cross:
    | { readonly ledger_dir: string; readonly to: string }
    | { readonly stack: string; readonly to: string };
}

/** cross(ledgerDir, address) builds the exact {"$cross": {"ledger_dir",
 * "to"}} marker docs/schema.md's UBI-27 amendment pins. address is a
 * hand-typed string in v1 (docs/sdk.md's own "Out of scope" -- a typed
 * cross-stack handle is real, useful, deferred future work). */
export function cross<T = unknown>(ledgerDir: string, address: string): T {
  return { $cross: { ledger_dir: ledgerDir, to: address } } as unknown as T;
}

/** crossStack(stack, address) builds the exact {"$cross": {"stack",
 * "to"}} marker (UBI-32 Arc B's own "resolve by NAME against the
 * base") -- the form UBI-134's own blueprint cross_ref params need: a
 * blueprint is built once and called from many different stacks/
 * environments, so a call-site "@<stack>.<type>.<name>" reference
 * (matching diagram/crossref.go's own "@" grammar for a reference
 * node's label) can only ever name a neighbor by stack, never by a
 * ledger_dir path the blueprint has no way to know at build time. */
export function crossStack<T = unknown>(stack: string, address: string): T {
  return { $cross: { stack, to: address } } as unknown as T;
}

function isSecretMarker(v: unknown): v is SecretMarker {
  return typeof v === "object" && v !== null && "$secret" in v && Object.keys(v).length === 1;
}

function isCrossMarker(v: unknown): v is CrossMarker {
  return typeof v === "object" && v !== null && "$cross" in v && Object.keys(v).length === 1;
}

// ---------------------------------------------------------------------
// The codegen'd binding shape -- what a `ubx sdk gen`-produced module
// actually exports for resource() to consume (sdk/codegen/templates/ts).
// ---------------------------------------------------------------------

/** FieldSpec is one settable config field's own wire-name mapping. A
 * plain string is a scalar (or list/set/map-of-scalar) field's wire
 * name -- no recursion needed, its value passes straight through. An
 * object form names a nested object (or a list/set/map of nested
 * objects) needing its own fields walked recursively. Mirrors
 * sdk/codegen/templates/ts's own fieldSpecLiteral output exactly --
 * this type and that Go function must stay in sync by construction, not
 * by convention alone, since ts_test.go asserts on the literal text this
 * type is meant to parse back. */
export type FieldSpec =
  | string
  | {
      readonly wireName: string;
      readonly kind: "object" | "list" | "set" | "map";
      readonly fields: FieldMap;
    };

export type FieldMap = Record<string, FieldSpec>;

/** ResourceBinding is what a codegen'd module exports per resource type
 * (e.g. `AwsDbInstance`) -- wireType for resources[].type, fields for
 * config-key -> wire-name mapping at evaluation time. TConfig/TAttrs are
 * type-only (never read at runtime) -- they're what give resource()'s
 * own generic signature real type inference.
 *
 * blueprintName (UBI-225) is undefined for an ordinary provider SDK
 * binding (sdk/codegen/templates/ts never sets it) -- set only by a
 * blueprint's own generated bindings.ts (blueprint/tsgen.go's own
 * renderTSBindings), to that blueprint's own bare declared name, the
 * SAME value its generated wrapper function passes to
 * pushBlueprintSource. This is provenance carried on the binding
 * itself, not just on the call scope: before this field existed, a
 * resource built by calling resource(someBlueprintBinding, name,
 * someConfig) directly -- importing a blueprint's own exported
 * binding/config and constructing the resource by hand, never calling
 * the blueprint's own wrapper function at all -- got zero provenance,
 * indistinguishable from an ordinary hand-written resource. addResource
 * below checks this field as a fallback exactly when
 * blueprintSourceStack is empty. */
export interface ResourceBinding<TConfig = unknown, TAttrs = unknown> {
  readonly wireType: string;
  readonly fields: FieldMap;
  readonly blueprintName?: string;
  // Phantom, type-only -- never assigned, never read; TypeScript infers
  // TConfig/TAttrs from a binding literal's own declared type
  // (sdk/codegen/templates/ts's `ResourceBinding<XConfig, XAttrs>`
  // annotation) even though nothing here actually holds a value of
  // either type.
  readonly __config?: TConfig;
  readonly __attrs?: TAttrs;
}

/** DataSourceBinding is ResourceBinding's own real sibling for a data
 * source (e.g. `AwsDataEc2Instance`) -- structurally identical
 * (wireType for data_sources[].type, fields for lookup-key ->
 * wire-name mapping, the same role fields plays for a resource's own
 * config), a distinct interface rather than a reused ResourceBinding
 * only so a codegen'd module can never pass a resource's own binding
 * to data() or vice versa by accident -- TypeScript catches that at the
 * call site instead of it becoming a runtime address-shape bug
 * (docs/schema.md's own "Amendment: data sources" -- wireType is
 * expected to already carry the real "data_"-prefixed convention that
 * section pins, this type does not prepend it itself). TLookup/TAttrs
 * are phantom, type-only, the same posture ResourceBinding's own
 * __config/__attrs already have. */
export interface DataSourceBinding<TLookup = unknown, TAttrs = unknown> {
  readonly wireType: string;
  readonly fields: FieldMap;
  readonly __lookup?: TLookup;
  readonly __attrs?: TAttrs;
}

// ---------------------------------------------------------------------
// intent/v1 document shape (docs/schema.md's own `ubx:intent/v1`) --
// only the fields this runtime actually populates; resources[].config
// values are the already-serialized wire-shaped JSON the collector
// produces, not TConfig itself.
// ---------------------------------------------------------------------

export interface IntentSource {
  readonly kind: string;
  readonly ref: string;
  readonly content_hash?: string;
}

export interface IntentDocument {
  readonly schema_version: 1;
  readonly kind: "ubx:intent/v1";
  readonly stack: string;
  readonly intent: {
    readonly summary: string;
    readonly sources: readonly IntentSource[];
  };
  readonly resources: ReadonlyArray<{
    readonly type: string;
    readonly name: string;
    readonly op: "create";
    readonly config: unknown;
    readonly sources?: readonly IntentSource[];
  }>;
  // data_sources is optional (omitted, never an empty array, when
  // data() was never called) -- docs/schema.md's own "Amendment: data
  // sources" -- a sibling array to resources[], never a variant of it:
  // there is no "op" field on an entry here at all, since this array
  // itself is the discriminator (always a read, never a create/modify).
  readonly data_sources?: ReadonlyArray<{
    readonly type: string;
    readonly name: string;
    readonly lookup: unknown;
    readonly sources?: readonly IntentSource[];
  }>;
  readonly overrides?: ReadonlyArray<{
    readonly address: string;
    readonly config: Record<string, unknown>;
  }>;
  // blueprint_outputs is output name -> the resolved address that
  // output's own Computed refers to (UBI-261). Omitted, never an empty
  // object, for every program that never calls blueprintOutputs, which
  // is every program except a blueprint caller.
  readonly blueprint_outputs?: Readonly<Record<string, string>>;
}

// ---------------------------------------------------------------------
// stack()/resource()/intent(): the describe-only surface itself.
// ---------------------------------------------------------------------

export interface StackDefinition {
  readonly name: string;
  /** evaluate() runs this stack's own describe function exactly once and
   * returns the resulting intent/v1 document. Never called by
   * stack() itself at import time -- deferred to an explicit call so the
   * evaluator harness (slice 4, not yet built) fully controls when and
   * how many times a program's own code actually runs, and can catch a
   * thrown exception itself (docs/sdk.md's own adversarial row 4: "no
   * partial intent/v1 is ever emitted"). */
  evaluate(): IntentDocument;
}

class Collector {
  readonly stackName: string;
  private resources: IntentDocument["resources"][number][] = [];
  private dataSources: NonNullable<IntentDocument["data_sources"]>[number][] = [];
  private overrides: NonNullable<IntentDocument["overrides"]>[number][] = [];
  private blueprintOutputs: Record<string, string> = {};

  recordBlueprintOutput(name: string, address: string): void {
    this.blueprintOutputs[name] = address;
  }
  private seenAddresses = new Set<string>();
  private intentInfo: IntentDocument["intent"] | undefined;
  private intentCalled = false;

  constructor(stackName: string) {
    this.stackName = stackName;
  }

  setIntent(summary: string, sources: readonly IntentSource[]): void {
    if (this.intentCalled) {
      throw new Error(
        "intent() called more than once in a single stack() body -- there is exactly one summary per stack, same as a hand-written intent file.",
      );
    }
    this.intentCalled = true;
    if (!summary || summary.trim() === "") {
      throw new Error("intent(): summary is required and cannot be empty.");
    }
    this.intentInfo = { summary, sources };
  }

  addResource<TAttrs>(binding: ResourceBinding<unknown, TAttrs>, name: string, config: unknown): Computed<TAttrs> {
    if (!name || name.trim() === "") {
      throw new Error(`resource(): name is required (type ${binding.wireType}).`);
    }
    const address = `${this.stackName}.${binding.wireType}.${name}`;
    if (this.seenAddresses.has(address)) {
      throw new Error(`resource(): duplicate resource ${binding.wireType} "${name}" in stack "${this.stackName}".`);
    }
    this.seenAddresses.add(address);

    const serialized = serializeConfig(binding.fields, config, address);
    // UBI-126/UBI-225: ref is deliberately the blueprint's own bare
    // declared name here, NOT yet "name:content_hash" -- this sandboxed
    // evaluator has no way to compute a real content hash for itself
    // (see pushBlueprintSource's own doc comment) -- ubx resolve's own
    // external StampDirectCallProvenanceTS step (blueprint package)
    // resolves the bare name to a real content hash afterward, mirroring
    // sdk/go/runtime's own identical mechanism.
    const base = { type: binding.wireType, name, op: "create" as const, config: serialized };
    const blueprintSource = currentBlueprintSource(binding);
    if (blueprintSource !== undefined) {
      this.resources.push({ ...base, sources: [{ kind: "blueprint", ref: blueprintSource }] });
    } else {
      this.resources.push(base);
    }

    return makeComputed(address) as Computed<TAttrs>;
  }

  /** addDataSource is addResource's own real sibling for data() -- same
   * duplicate-address check (the identical shared seenAddresses set, so
   * a resource and a data source can never collide with each other
   * either, though in practice they never do: DataSourceBinding.wireType
   * always carries the real "data_" prefix docs/schema.md's own
   * amendment pins, so the two address spaces never actually overlap),
   * same blueprint-provenance wiring, same serializeConfig walk
   * (identical call, not a reimplementation -- a lookup value gets the
   * exact same $ref/$secret/$cross marker recognition a resource's own
   * config already gets, docs/schema.md's own "data_sources[].lookup"
   * bullet). The one real difference from addResource: no "op" field on
   * the built entry at all (IntentDocument["data_sources"]'s own doc
   * comment has why) and it pushes onto this.dataSources, a separate
   * array, never this.resources. */
  addDataSource<TAttrs>(binding: DataSourceBinding<unknown, TAttrs>, name: string, lookup: unknown): Computed<TAttrs> {
    if (!name || name.trim() === "") {
      throw new Error(`data(): name is required (type ${binding.wireType}).`);
    }
    const address = `${this.stackName}.${binding.wireType}.${name}`;
    if (this.seenAddresses.has(address)) {
      throw new Error(`data(): duplicate data source ${binding.wireType} "${name}" in stack "${this.stackName}".`);
    }
    this.seenAddresses.add(address);

    const serialized = serializeConfig(binding.fields, lookup, address);
    const base = { type: binding.wireType, name, lookup: serialized };
    if (blueprintSourceStack.length > 0) {
      this.dataSources.push({ ...base, sources: [{ kind: "blueprint", ref: blueprintSourceStack[blueprintSourceStack.length - 1] }] });
    } else {
      this.dataSources.push(base);
    }

    return makeComputed(address) as Computed<TAttrs>;
  }

  addOverride(address: string, config: Record<string, unknown>): void {
    if (!address || address.trim() === "") {
      throw new Error("override(): address is required.");
    }
    if (!config || Object.keys(config).length === 0) {
      throw new Error(`override("${address}"): config is required and cannot be empty.`);
    }
    const serialized: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(config)) {
      serialized[k] = serializeOpaque(v, address);
    }
    this.overrides.push({ address, config: serialized });
  }

  finish(): IntentDocument {
    if (!this.intentInfo) {
      throw new Error(
        "stack(): intent() was never called -- a missing summary is a collection-time hard failure, matching resources[].op's own \"always explicit, never inferred\" discipline.",
      );
    }
    let doc: IntentDocument = {
      schema_version: 1,
      kind: "ubx:intent/v1",
      stack: this.stackName,
      intent: this.intentInfo,
      resources: this.resources,
    };
    if (this.dataSources.length > 0) {
      doc = { ...doc, data_sources: this.dataSources };
    }
    if (this.overrides.length > 0) {
      doc = { ...doc, overrides: this.overrides };
    }
    if (Object.keys(this.blueprintOutputs).length > 0) {
      doc = { ...doc, blueprint_outputs: this.blueprintOutputs };
    }
    return doc;
  }
}

let currentCollector: Collector | null = null;

/** stack(name, fn) is the module's default export. fn's only legitimate
 * side effect is calling resource()/intent(), which append to an
 * in-memory collector -- never written to disk from inside the sandbox
 * (the evaluator harness owns that, slice 4). */
export function stack(name: string, fn: () => void): StackDefinition {
  if (!name || name.trim() === "") {
    throw new Error("stack(): name is required.");
  }
  return {
    name,
    evaluate(): IntentDocument {
      const collector = new Collector(name);
      const previous = currentCollector;
      currentCollector = collector;
      try {
        fn();
      } finally {
        currentCollector = previous;
      }
      return collector.finish();
    },
  };
}

function requireCollector(caller: string): Collector {
  if (!currentCollector) {
    throw new Error(`${caller}() called outside of an active stack() evaluation.`);
  }
  return currentCollector;
}

/** resource(binding, name, config) declares one resource -- always
 * op: "create" in v1 (docs/sdk.md names this a real, deliberate scope
 * limit: a hermetic, describe-only program has no way to read ledger
 * state to know a resource already exists, so it has no way to express
 * "modify" intent; see this package's own README/docs/sdk.md addendum).
 * Returns a Computed<TAttrs> handle for wiring into sibling resources'
 * own config -- every field, not just the schema-Computed ones (see
 * Computed<T>'s own doc comment for why that's correct). */
export function resource<TConfig, TAttrs>(
  binding: ResourceBinding<TConfig, TAttrs>,
  name: string,
  config: TConfig,
): Computed<TAttrs> {
  return requireCollector("resource").addResource(binding, name, config);
}

/** data(binding, name, lookup) declares one data source -- a lookup,
 * never a create/modify (docs/schema.md's own "Amendment: data
 * sources"). Returns the identical Computed<TAttrs> handle resource()
 * returns, on purpose: a data source's own result wires into a sibling
 * resource's config exactly the way another resource's own output
 * would, no separate reference type or API for a caller to learn. */
export function data<TLookup, TAttrs>(
  binding: DataSourceBinding<TLookup, TAttrs>,
  name: string,
  lookup: TLookup,
): Computed<TAttrs> {
  return requireCollector("data").addDataSource(binding, name, lookup);
}

/** intent({summary, sources}) populates the emitted document's own
 * intent.summary/intent.sources. Required, called at most once per
 * stack() body (docs/sdk.md). */
export function intent(info: { summary: string; sources?: readonly IntentSource[] }): void {
  requireCollector("intent").setIntent(info.summary, info.sources ?? []);
}

/** override(address, config) declares a caller-owned attribute patch
 * against address (the canonical "<stack>.<type>.<name>" form) -- UBI-86
 * Part 2's own mechanism, zero AI, a direct function call exactly like
 * resource(). config's own keys are the target resource's own real WIRE
 * attribute names (e.g. "message_retention_seconds"), never a
 * ResourceBinding's own idiomatic (camelCase) config key -- there is no
 * ResourceBinding for the TARGET resource in scope here, since it need
 * not even be declared in this same document at all (the motivating
 * case: overriding a field inside a called blueprint's own internal,
 * unparameterized resource). Applied by blueprint.ApplyOverrides (the Go
 * `blueprint` package) after any blueprint call in this document has
 * already resolved, before ship -- see docs/blueprint.md's own "Override
 * mechanism" section for the full design record. */
export function override(address: string, config: Record<string, unknown>): void {
  requireCollector("override").addOverride(address, config);
}

// ---------------------------------------------------------------------
// pushBlueprintSource/popBlueprintSource: UBI-126's own fix, mirroring
// sdk/go/runtime's identical blueprintSourceStack mechanism exactly --
// see that file's own doc comment for the full design account. An
// implicit, module-level scope stack (never a new parameter on
// resource() itself, which would force every existing caller, blueprint
// or not, to change) that a compiled blueprint's own generated function
// (blueprint/tsgen.go's own renderTSFunction) wraps its entire body in,
// using the blueprint's own bare, unsanitized declared name. A plain
// stack that never imports a blueprint never pushes anything, so this
// stack stays empty and a resource's own `sources` stays unset unless
// the binding itself carries a blueprintName (currentBlueprintSource
// below) -- zero wire-format change for the overwhelming majority of
// ordinary SDK programs.
// ---------------------------------------------------------------------

let blueprintSourceStack: string[] = [];

/** currentBlueprintSource (UBI-225) is what addResource actually
 * checks: the innermost active pushBlueprintSource scope if one is
 * open, or -- exactly when it isn't -- the binding's own carried
 * blueprintName. In every case ubx blueprint build's own generated code
 * produces, the two never disagree: a wrapper function's own
 * resource() calls run against that SAME blueprint's own bindings, both
 * stamped with the identical name by the same codegen run, so which one
 * wins is only ever load-bearing for the one case this field exists to
 * fix -- a binding used OUTSIDE any open scope at all (no wrapper
 * function call), where the scope check alone would find nothing and
 * the binding is the only signal left. Returns undefined (no
 * provenance) for an ordinary resource: no open scope, and a binding
 * with no blueprintName -- the overwhelming common case, completely
 * unaffected. */
function currentBlueprintSource(binding: ResourceBinding<unknown, unknown>): string | undefined {
  if (blueprintSourceStack.length > 0) {
    return blueprintSourceStack[blueprintSourceStack.length - 1];
  }
  return binding.blueprintName;
}

/** pushBlueprintSource marks every resource() call for the duration of
 * the current scope (until the matching popBlueprintSource) as produced
 * by the blueprint named name -- name is the blueprint's own declared
 * name (Ubxfile directory basename, the SAME identifier
 * buildManifest/ubx why/ubx render already use), never a module
 * specifier, and never (cannot be) a content hash -- this sandboxed
 * evaluator has no way to compute its own blueprint's real content hash
 * (it doesn't know its own on-disk blueprint directory, and baking a
 * hash into source that then gets hashed itself is circular, the same
 * reasoning blueprint.lock.json's own hash-exclusion already
 * establishes). Called only by generated blueprint code, never meant to
 * be called by a stack author's own code directly. */
/** blueprintOutputs records what a called blueprint returned, so the
 * caller of that blueprint can reference its outputs (UBI-261).
 *
 * This exists because the address a blueprint's output refers to is
 * knowable only while the blueprint RUNS. An Ubxfile declared each
 * output as a literal "<resource-slug>.<attribute>" pair, which could
 * be read without running anything; a blueprint that is code returns a
 * Computed, and which attribute of which resource that points at can
 * depend on the blueprint's own branching. So it is reported from
 * inside the evaluation rather than derived from outside it.
 *
 * Called by the synthesized caller program ubx writes to invoke a
 * blueprint, never by a person. An undefined or null entry is skipped
 * rather than recorded as an empty address: a blueprint may
 * legitimately return no value for a declared output, and the caller
 * reports that as its own named error, where it can say which output
 * and which blueprint. Calling it twice merges, last write winning per
 * key. */
export function blueprintOutputs(
  outputs: Record<string, Computed<unknown> | undefined | null>,
): void {
  const collector = requireCollector("blueprintOutputs");
  for (const [name, value] of Object.entries(outputs)) {
    if (value === undefined || value === null) continue;
    collector.recordBlueprintOutput(name, addressOf(value));
  }
}

export function pushBlueprintSource(name: string): void {
  blueprintSourceStack.push(name);
}

/** popBlueprintSource ends the innermost still-open pushBlueprintSource
 * scope. Throws on an unbalanced call (more pops than pushes) -- a real
 * bug in generated code, not something to silently tolerate. */
export function popBlueprintSource(): void {
  if (blueprintSourceStack.length === 0) {
    throw new Error(
      "popBlueprintSource: called with no matching pushBlueprintSource -- this should only ever be called by ubx blueprint build's own generated code.",
    );
  }
  blueprintSourceStack.pop();
}

// ---------------------------------------------------------------------
// The recursive config serializer -- config's own idiomatic (camelCase)
// keys, mapped back to the provider's real wire names via the binding's
// own FieldMap, recursively for nested objects/lists/sets/maps of
// objects. Also where docs/sdk.md's own adversarial row 5 (functions,
// symbols, circular references, undefined, un-marked Computed<T> misuse)
// gets a first, defensive line of enforcement -- full intent/v1
// JSON-Schema validation is the evaluator harness's own job (slice 4),
// not duplicated here, but a value this serializer flatly cannot turn
// into JSON at all is caught here rather than produced as silently
// wrong output.
// ---------------------------------------------------------------------

// genericResult is serializeGenericOrMarker's own return shape: handled
// is true for every value SHAPE common to both field-map-translated and
// opaque serialization (a Computed<T> reference, a $secret/$cross
// marker, null/undefined, an unrepresentable primitive that must throw,
// or an ordinary scalar) -- false only for a plain object or array,
// which serializeConfig and serializeOpaque each then walk their own,
// genuinely different way (key-translated vs. pass-through-as-is).
interface GenericResult {
  handled: boolean;
  result?: unknown;
}

function serializeGenericOrMarker(value: unknown, addressForErrors: string): GenericResult {
  if (isComputed(value)) {
    return { handled: true, result: { $ref: { to: addressOf(value as Computed<unknown>) } } };
  }
  if (isSecretMarker(value) || isCrossMarker(value)) {
    return { handled: true, result: value };
  }
  if (value === null || value === undefined) {
    return { handled: true, result: null };
  }
  if (typeof value === "function") {
    throw new TypeError(`resource "${addressForErrors}": a function cannot appear in a resource's own config -- intent/v1 has no representation for it.`);
  }
  if (typeof value === "symbol") {
    throw new TypeError(`resource "${addressForErrors}": a symbol cannot appear in a resource's own config -- intent/v1 has no representation for it.`);
  }
  if (typeof value === "bigint") {
    throw new TypeError(`resource "${addressForErrors}": a bigint cannot appear in a resource's own config -- use a decimal string instead (docs/schema.md's own canonical hashing rule: numbers are int64 or decimal-string, never a float/bigint literal).`);
  }
  if (typeof value !== "object") {
    // string | number | boolean, the only remaining primitive shapes.
    return { handled: true, result: value };
  }
  return { handled: false };
}

/** serializeConfig walks a value whose own shape IS described by fields
 * (the top-level resource config, or a nested field typed KindObject) --
 * every plain-object key is translated from its idiomatic (camelCase)
 * name to the provider's real wire name via fields, recursively. */
function serializeConfig(fields: FieldMap, value: unknown, addressForErrors: string): unknown {
  const generic = serializeGenericOrMarker(value, addressForErrors);
  if (generic.handled) {
    return generic.result;
  }
  if (Array.isArray(value)) {
    // A field-map-shaped array reaching this point (rather than via the
    // explicit "list"/"set" spec.kind branch in serializeFieldValue,
    // below) means the caller's own types were bypassed -- defensively
    // walk each element opaquely rather than guessing at a field map
    // for it that was never named.
    return (value as unknown[]).map((el) => serializeOpaque(el, addressForErrors));
  }
  const out: Record<string, unknown> = {};
  for (const [idiomaticKey, raw] of Object.entries(value as Record<string, unknown>)) {
    // undefined means "not set", so the key is omitted entirely -- the
    // same answer Go and Python already give.
    //
    // Object.entries() keeps a key that was explicitly written as
    // undefined, unlike a key that was never written at all, so without
    // this the two differ: `{ deadLetter: undefined }` serialized as
    // `"dead_letter": null` while omitting the key serialized as
    // nothing. Go's serializeConfig skips a nil interface field
    // ("not set -- omitted") and Python's skips `raw is None`, so
    // TypeScript was the only runtime turning "not set" into an explicit
    // null, and two semantically identical programs produced different
    // intent/v1 and therefore different proposal hashes.
    //
    // An explicit `null` still serializes as null and is unaffected.
    // That remains the only way to say "set this field to null" in any
    // of the three runtimes, since Go and Python cannot express it at a
    // config field at all: there, nil/None already mean omitted.
    if (raw === undefined) {
      continue;
    }
    const spec = fields[idiomaticKey];
    if (spec === undefined) {
      throw new TypeError(`resource "${addressForErrors}": unrecognized config field "${idiomaticKey}" -- not present in this resource type's own generated binding.`);
    }
    const wireKey = typeof spec === "string" ? spec : spec.wireName;
    out[wireKey] = serializeFieldValue(spec, raw, addressForErrors, idiomaticKey);
  }
  return out;
}

/** serializeFieldValue serializes one already-looked-up field's own raw
 * value, dispatching on its FieldSpec: a plain string spec (scalar, or a
 * list/set/map of scalars -- codegen never emits a string spec for a
 * collection of OBJECTS, see sdk/codegen/templates/ts's own
 * fieldSpecLiteral) walks opaquely; an object-form spec recurses through
 * serializeConfig, field-map-translated, per element/value as needed. */
function serializeFieldValue(spec: FieldSpec, raw: unknown, addressForErrors: string, idiomaticKey: string): unknown {
  if (typeof spec === "string") {
    return serializeOpaque(raw, addressForErrors);
  }
  switch (spec.kind) {
    case "object":
      return serializeConfig(spec.fields, raw, addressForErrors);
    case "list":
    case "set": {
      const generic = serializeGenericOrMarker(raw, addressForErrors);
      if (generic.handled) {
        // e.g. raw is itself a Computed<T> reference (the whole list
        // wired to a sibling resource's own list-typed attribute) or
        // null -- never walked element-by-element in that case.
        return generic.result;
      }
      if (!Array.isArray(raw)) {
        throw new TypeError(`resource "${addressForErrors}": field "${idiomaticKey}" expects an array.`);
      }
      return raw.map((el) => serializeConfig(spec.fields, el, addressForErrors));
    }
    case "map": {
      const generic = serializeGenericOrMarker(raw, addressForErrors);
      if (generic.handled) {
        return generic.result;
      }
      if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
        throw new TypeError(`resource "${addressForErrors}": field "${idiomaticKey}" expects an object (map).`);
      }
      const mapped: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(raw as Record<string, unknown>)) {
        mapped[k] = serializeConfig(spec.fields, v, addressForErrors);
      }
      return mapped;
    }
  }
}

/** serializeOpaque walks a value with NO field-map-driven structure at
 * all -- a scalar, or (recursively) anything nested inside a
 * scalar/list/set/map-of-scalar field, e.g. a `tags: Record<string,
 * string>` field's own arbitrary, user-chosen map keys. Object/array
 * keys pass through completely unchanged; only Computed<T>/marker
 * recognition and representability are checked. This is the exact
 * distinction TestFunctionInTagsValue-shaped bugs live in: `tags`'s own
 * keys ("env", "owner", ...) are never resource config field names and
 * must never be looked up in a FieldMap or rejected as "unrecognized." */
function serializeOpaque(value: unknown, addressForErrors: string): unknown {
  const generic = serializeGenericOrMarker(value, addressForErrors);
  if (generic.handled) {
    return generic.result;
  }
  if (Array.isArray(value)) {
    return (value as unknown[]).map((el) => serializeOpaque(el, addressForErrors));
  }
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    out[k] = serializeOpaque(v, addressForErrors);
  }
  return out;
}
