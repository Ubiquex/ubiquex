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

export interface CrossMarker {
  readonly $cross: { readonly ledger_dir: string; readonly to: string };
}

/** cross(ledgerDir, address) builds the exact {"$cross": {"ledger_dir",
 * "to"}} marker docs/schema.md's UBI-27 amendment pins. address is a
 * hand-typed string in v1 (docs/sdk.md's own "Out of scope" -- a typed
 * cross-stack handle is real, useful, deferred future work). */
export function cross<T = unknown>(ledgerDir: string, address: string): T {
  return { $cross: { ledger_dir: ledgerDir, to: address } } as unknown as T;
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
 * own generic signature real type inference. */
export interface ResourceBinding<TConfig = unknown, TAttrs = unknown> {
  readonly wireType: string;
  readonly fields: FieldMap;
  // Phantom, type-only -- never assigned, never read; TypeScript infers
  // TConfig/TAttrs from a binding literal's own declared type
  // (sdk/codegen/templates/ts's `ResourceBinding<XConfig, XAttrs>`
  // annotation) even though nothing here actually holds a value of
  // either type.
  readonly __config?: TConfig;
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
  }>;
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
    this.resources.push({ type: binding.wireType, name, op: "create", config: serialized });

    return makeComputed(address) as Computed<TAttrs>;
  }

  finish(): IntentDocument {
    if (!this.intentInfo) {
      throw new Error(
        "stack(): intent() was never called -- a missing summary is a collection-time hard failure, matching resources[].op's own \"always explicit, never inferred\" discipline.",
      );
    }
    return {
      schema_version: 1,
      kind: "ubx:intent/v1",
      stack: this.stackName,
      intent: this.intentInfo,
      resources: this.resources,
    };
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

/** intent({summary, sources}) populates the emitted document's own
 * intent.summary/intent.sources. Required, called at most once per
 * stack() body (docs/sdk.md). */
export function intent(info: { summary: string; sources?: readonly IntentSource[] }): void {
  requireCollector("intent").setIntent(info.summary, info.sources ?? []);
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
