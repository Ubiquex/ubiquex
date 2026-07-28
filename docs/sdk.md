# SDK program — the multi-language contract, and TypeScript first (UBI-33/34)

> Session 1, design only, no code. This document is the contract half of
> UBI-33 (the umbrella: multi-language contract + shared codegen) and the
> TypeScript-specific design half of UBI-34 (`@ubx/sdk`, first language to
> ship). Two hard constraints came pre-decided from the ticket's own design
> room (Pulumi case-study comments) and are not relitigated here: the
> **monorepo** (`sdk/` inside `ubiquex-cli`, all languages, one CI) and
> **codegen'd bindings are generated locally, never published** (`ubx sdk
> gen` runs against the config-pinned provider version on the user's own
> machine; only the tiny `@ubx/sdk` runtime ships to npm). Language order
> is also pre-decided: TypeScript, then Go, then Python
> (`docs/architecture.md`'s own "What carries over from v1" already names
> `Computed<T>`/`secret()`/typed refs as v1 design worth keeping — this is
> where that design gets rebuilt, in code, for the first time in v2).

## Scope: what this session designs, and what it doesn't

Designed here: the language-neutral contract (golden `intent/v1` fixtures
as the spec, semantic identity enforced as byte identity via canonical
JSON — see below for why those are the same claim once you canonicalize);
the `sdk/` monorepo layout; the describe-only TypeScript runtime surface
(`stack`/`resource`/`secret`/`cross`, `Computed<T>`); the hermetic
evaluator, decided **empirically** this session (not by reputation or
documentation — three real candidates were actually run against the real
requirement, see below); double-run determinism at the evaluation
boundary; the codegen design (provider schema → language-neutral IR model
→ per-language templates); the adversarial program; implementation slices
toward a real TypeScript payments program.

Not designed here, named so it isn't assumed covered: Go and Python's own
runtime/evaluator (UBI-35/36 — Go's compiled-program "cheat" and Python's
"famously miserable, no cheat" sandbox story are structurally different
problems, not a copy-paste of this session's TS answer); the codegen IR
model's Go/Python template halves (the IR model itself is designed
language-neutral here, but only the TS template is written); `ubx sdk
gen`'s CLI flag surface and config wiring (mentioned, not pinned); the
conformance harness's actual Go code (`sdk/conformance/`'s layout is
designed, its runner isn't built); npm packaging/release plumbing.

## The contract: golden `intent/v1` fixtures ARE the spec

UBI-33's own framing, stated as a hard constraint on the ticket itself:
"an SDK program in ANY language evaluates to identical intent/v1 JSON —
the conformance suite of golden tests... IS the spec. Written before any
language ships; every language implementation passes the same suite."
This session takes that literally rather than as a slogan:

- **The golden files are the JSON, not the program.** `sdk/conformance/`
  holds one canonical `intent/v1` document per test case
  (`sdk/conformance/golden/<case>.json`), authored by hand or lifted from
  an existing resolved example — never generated FROM a language's own
  program and then frozen (that would make the first language's own
  output the accidental spec, exactly the anti-pattern a language-neutral
  contract exists to prevent). Each case also carries one equivalent
  program per shipped language (`sdk/conformance/programs/ts/<case>.ts`,
  later `.../go/<case>.go`, `.../py/<case>.py`) — same logical stack,
  independently authored per language's own idiom, not a mechanical
  transliteration.
- **"Byte-identical" and "semantic identity" are the same claim, once
  canonicalized — this is a real design decision, not a rounding of the
  ticket's own wording.** The ticket's own text says "byte-identical
  frozen intent"; this session's own framing says "semantic identity
  across languages." Two different-looking JSON encodings of the same
  logical document (key order, whitespace, number formatting) are
  semantically identical but not byte-identical as raw text — so a naive
  byte-diff would either force every language's own JSON encoder to
  agree on formatting (a real TS-ism/Go-ism/Py-ism leak into what's
  supposed to be a language-neutral contract) or silently fail on
  cosmetic differences that don't matter. The existing project already
  solved exactly this problem for `Proposal` hashing
  (docs/schema.md's "Canonical hashing — RATIFIED v1": RFC 8785/JCS-style
  canonical JSON, sorted object keys, numbers restricted to
  int64/decimal-strings, no float literals) — reused here, not
  reinvented, for the same reason: canonicalizing removes every axis of
  "different but equivalent," so byte-identical-after-canonicalization
  **is** the operational test for semantic identity. `core.Hash`'s own
  `canonicalProposalBytes` (core/canonical.go) is Proposal-shaped
  (excludes `id`/`acceptance`/`status`, sorts delta arrays) and can't be
  reused as-is on an `intent/v1` document, which has none of those
  fields — the conformance harness needs a new, small, general-purpose
  canonical-JSON function, factored out of the same JCS logic
  `canonicalProposalBytes` already has (sorted-keys object marshal +
  the same number-canonicalization rule), not a second, divergent
  implementation of "canonical JSON" living in a second place. This is
  real, unstarted work — named in Implementation slices, below — not
  claimed done by this document.
- **The conformance harness's own discipline is stricter than
  `intent-provider`'s, and that's a real, checked divergence, not an
  oversight.** docs/intent-provider.md's own conformance suite uses
  per-fixture assertion functions, not a golden byte-diff, because LLM
  output isn't deterministic the way a tfplugin provider's — or an SDK
  evaluator's — is. An SDK program is typed, human-authored code with no
  interpretation step; `core.DoubleRun` (below) enforces that a single
  evaluator run is even internally reproducible before conformance ever
  compares across languages. So SDK conformance gets to be the strictly
  stronger discipline the original `conformance/` harness (UBI-9,
  provider types) already has — byte-exact, not "close enough by some
  assertion" — while `intent-provider`'s own weaker discipline stays
  correctly scoped to the one producer that actually needs it.

## `sdk/` monorepo layout

```
sdk/
  conformance/
    golden/<case>.json          # the spec: one canonical intent/v1 per case
    programs/
      ts/<case>.ts               # equivalent TS program, hand-authored
      go/<case>.go                # (UBI-35, not this session)
      py/<case>.py                 # (UBI-36, not this session)
    runner/                      # Go: evaluate each language's program,
                                  # canonicalize, byte-compare to golden/
  codegen/
    ir/                          # the shared, language-neutral type model
                                  # (provider.Schema -> IR, no TS-isms)
    templates/
      ts/                        # Go text/template or similar, IR -> .ts
      go/                        # (UBI-35)
      py/                        # (UBI-36)
  ts/
    runtime/                     # @ubx/sdk: stack/resource/secret/cross,
                                  # Computed<T>, the collector
    evaluator/                   # the Deno subprocess harness (Go side
                                  # lives in cli/ or a new sdkeval/ package
                                  # -- see Implementation slices)
    package.json                 # @ubx/sdk, the only thing ever published
  go/                            # (UBI-35, own go.mod -- not this session)
  py/                            # (UBI-36 -- not this session)
```

`sdk/codegen` is shared, imported by every language's own `gen` step —
the IR-model half is one Go package with zero per-language assumptions
baked in; a template is a per-language plugin to it, matching the
project's existing "shared core, swappable adapter" shape
(`intentprovider.Adapter`, `core.StateReader`, `gcpaudit`/`cloudtrail`
behind `EventLookup`) rather than inventing a new extension pattern for
this one case.

## The describe-only runtime surface

`@ubx/sdk`'s exported surface is deliberately small — `stack`,
`resource`, `secret`, `cross`, `intent`, and the `Computed<T>` type. A
program using it never computes, never reaches a provider, never touches
a ledger — it **describes** a desired end-state and stops, exactly
`docs/schema.md`'s own `ubx:intent/v1` shape (the resolver's *input*,
never itself hashed), just authored in TypeScript instead of hand-typed
JSON or transcribed from prose.

```typescript
import { stack, resource, secret, cross, intent, Computed } from '@ubx/sdk';
import { AwsDbInstance } from './generated/hashicorp-aws';

export default stack('payments', () => {
  intent({
    summary: 'provision a read replica for the payments database',
  });

  const primary = resource(AwsDbInstance, 'payments-db', {
    instanceClass: 'db.t3.large',
  });

  resource(AwsDbInstance, 'payments-db-replica', {
    instanceClass: 'db.t3.medium',
    replicateSourceDb: primary.id,           // same-stack $ref, via Computed<T>
    masterPassword: secret('aws_secrets_manager', 'payments/replica-password'),
  });
});
```

- **`stack(name, fn)`** — the module's default export. `fn` runs once per
  evaluation (twice per `ubx resolve --from-code` invocation, once per
  `core.DoubleRun` pass — see below); its only legitimate side effect is
  calling `resource()`/`intent()`, which append to an in-memory collector
  closed over by the runtime, never written to disk from inside the
  sandbox (see "Never a filesystem writer," below).
- **`resource(binding, name, config)`** — `binding` is a codegen'd class
  (`AwsDbInstance` above; see Codegen design) carrying the real provider
  `type` string and the field shape codegen derived from the real schema.
  `config`'s keys are the codegen'd binding's own idiomatic (camelCase)
  field names; the collector's serializer maps each one back to the
  provider's real wire name (snake_case, verbatim from
  `provider.Attribute.Name`) when it emits `resources[].config` — the
  program author never sees or writes a wire name directly. Returns a
  typed handle whose fields are `Computed<T>` (see below), for wiring
  into sibling resources' own config.
- **`secret(backend, path)`** — a thin, typed constructor for the exact
  `{"$secret": {"backend": ..., "path": ...}}` marker docs/schema.md
  already pinned (founding IR-node draft) — no new wire shape, a
  TypeScript-ergonomic front end to an existing one.
- **`cross(ledgerDir, address)`** — same relationship to the existing
  `$cross` marker (docs/schema.md's UBI-27 amendment): `{"$cross":
  {"ledger_dir": ledgerDir, "to": address}}`, hand-typed as a string
  address for v1 (a typed cross-stack handle — resolving `address`'s own
  shape against a second stack's own generated bindings — is real,
  useful, and explicitly deferred; see Out of scope).
- **`intent({summary, sources?})`** — populates the emitted document's
  own `intent.summary`/`intent.sources`, required (a missing summary is a
  collection-time hard failure — matching `resources[].op`'s own
  "always explicit, never inferred" discipline, docs/resolver.md). Called
  at most once per `stack()` body; a second call is a hard failure too,
  not a silent overwrite — there is exactly one summary per stack, same
  as a hand-written intent file.

### `Computed<T>`: a reference, never a value

`resource()`'s return value's fields are `Computed<T>` — not `T`. This is
the same design v1 XCL had and docs/architecture.md's own "What carries
over from v1" names explicitly for v2 to rebuild
(`Computed<T>` alongside `ephemeral`, `secret()`, typed refs). The
describe-only boundary makes this a hard requirement, not an ergonomic
nicety: at evaluation time nothing has been created yet, so
`primary.id` genuinely has no value to hand back — it can only ever be a
**reference to where a value will eventually be**, the same thing
`$ref`'s own resolved forms already are (docs/resolver.md: a `$ref`
resolves to either a concrete literal or a `$computed` marker, depending
on whether the referenced attribute is schema-`Computed`).

Mechanically: `Computed<T>` is a branded object (a Proxy in the runtime
implementation, not a plain interface — needed so property access on a
`Computed<object>`, e.g. `someNestedBlock.field`, still produces a valid,
traceable address rather than `undefined`) carrying exactly the resolved
address string (`payments.aws_db_instance.payments-db.id`) it stands for.
It has **no method that produces a real `T`** — no `.valueOf()`
override, no coercion path, deliberately, mirroring docs/resolver.md's
own `$ref`/`$computed` split: this project has no `Secret<T>` runtime
check either (docs/resolver.md, Open decisions — nothing stops
`secret(...)`'s output from being misused inside a template string
today), and the same gap applies here rather than inventing a stronger
guarantee for `Computed<T>` alone. Passing a `Computed<T>` as a
`resource()` config value serializes to `{"$ref": {"to": "<address>"}}`
(same-stack) — the collector recognizes the branded object by identity,
not by structural duck-typing, so a hand-rolled object that happens to
look like one is never mistaken for a real reference. Attempting to use
one as a real JS value (string-concatenating it, arithmetic on it,
`JSON.stringify`-ing it directly instead of through `resource()`) throws
a clear, named error at evaluation time (`ComputedCoercionError`, naming
the address and the attempted operation) rather than silently producing
`"[object Object]"` or `NaN` — a concrete, checked adversarial row (see
below), not an assumed-safe corner.

## The hermetic evaluator: decided empirically

UBI-34's own ticket names the sandbox choice as "the hard part of this
ticket" and instructs deciding "empirically." This session did exactly
that — three real candidates, actually run, against the real
requirement (**no net, no fs, no env, no clock** — UBI-33's own framing)
— rather than picked from documentation or reputation. Every number below
is from a real probe run this session, in this environment (macOS,
Node v24.18.0, Deno 2.9.4 freshly installed, `isolated-vm@6.1.2` via npm);
none of it is asserted from memory of how these tools are "supposed to"
behave.

### Candidate 1 — Node's `--permission` model: disqualified, not a close call

`node --permission <script>`, zero `--allow-*` flags (the maximally
locked-down configuration):

| Capability | Blocked? | Evidence |
| --- | --- | --- |
| `fs.readFileSync` | **Yes** | `Error: Access to this API has been restricted. Use --allow-fs-read...` |
| `fs.writeFileSync` | **Yes** | same shape, `--allow-fs-write` |
| `child_process.execSync` | **Yes** | same shape, `--allow-child-process` |
| `process.env.HOME` | **No** | read cleanly, no error, no flag exists to gate it |
| `http.get(...)` | **No** | call site never threw; nothing in the permission model recognizes network as a resource at all |
| `Date.now()` | **No** | read cleanly (expected — see "The clock/random gap," below, this is not unique to Node) |

`node --help`'s own `--allow-*` flag list (`--allow-fs-read`,
`--allow-fs-write`, `--allow-child-process`, `--allow-addons`,
`--allow-worker`, `--allow-wasi`, `--allow-inspector`) confirms this
isn't a probe artifact: **there is no `--allow-net` or `--allow-env`
flag in this Node version at all** — the permission model was never
built to gate network or environment access, only filesystem and
process-spawning. Two of this session's four hard requirements
(net, env) are structurally outside what Node's permission model can
enforce, at any flag combination. Closing that gap would mean hand-
patching `process.env`, `http`/`https`/`net`/`dgram`/`tls` module
internals from userland before running the program — a second,
custom, unaudited security boundary layered on top of an incomplete
one, which is a worse trust story than picking a sandbox that already
covers the requirement natively. Disqualified.

### Candidate 2 — Deno: the strongest default-deny coverage of the three, with one real gap found and closed

`deno run <script>`, zero `--allow-*` flags (Deno's permission model is
default-deny; nothing is granted unless named):

| Capability | Blocked? | Evidence |
| --- | --- | --- |
| `Deno.readTextFileSync` | **Yes** | `NotCapable: Requires read access to "/etc/hosts"...` |
| `Deno.writeTextFileSync` | **Yes** | same shape, `--allow-write` |
| `Deno.env.get` | **Yes** | same shape, `--allow-env` |
| `Deno.Command` (subprocess) | **Yes** | same shape, `--allow-run` |
| `fetch(...)`, awaited | **Yes** | `NotCapable: Requires net access to "169.254.169.254:80"...` (checked at connect/await time, not at the synchronous `fetch()` call site — the call site itself doesn't throw, but nothing observable ever completes without the permission) |
| `Date.now()` | **No** | read cleanly (universal gap, see below) |
| **`await import("https://...")`, zero flags** | **No — a real gap found empirically, not assumed closed by `--deny-net`** | succeeded, downloaded and evaluated a real remote module, **even under `--deny-net` passed explicitly** |
| `await import("https://...")`, **`--no-remote`** | **Yes** | `TypeError: A remote specifier was requested... but --no-remote is specified.` |

The remote-import finding is the one genuine surprise this session's
probes turned up, and it matters: **Deno treats "network access" (`fetch`,
`Deno.connect`, WebSocket, ...) and "module resolution" as two separate
permission surfaces.** `--deny-net`/omitting `--allow-net` closes the
first; it does nothing to the second. A program's own `import` statement
(static, at the top of the file) or a dynamic `await import(...)` call
naming an `https://` specifier is resolved by Deno's module loader, which
checked this session's probe as unaffected by net permission entirely —
confirmed twice, once with zero flags and once with `--deny-net`
explicitly passed, same result both times. This is exactly the kind of
gap this project's own "verify before implementing" discipline exists to
catch before it ships as an unexamined assumption (the same posture that
caught `hclwrite.SetAttributeValue` silently dropping comments, UBI-11,
or `sort.Slice`'s unspecified tie-breaking, UBI-11 follow-up — see
STATE.md's own accumulated findings). `--no-remote` (`deno run --help`:
"Do not resolve remote modules") closes it completely, confirmed by the
same probe throwing cleanly once the flag is added. **The evaluator
invocation this design commits to always passes `--no-remote`,
unconditionally, alongside explicit `--deny-net --deny-read --deny-write
--deny-env --deny-run --deny-ffi --deny-sys`** (redundant with the
already-default-deny zero-flags posture, confirmed identical behavior by
this session's own probe — kept explicit anyway as defense-in-depth
against a future Deno release quietly changing a default, the same
"never assume a security-relevant default stays the default" instinct
this project already applies to `--allow-fs-write` in `ubx accept`'s
locking, UBI-20).

### Candidate 3 — `isolated-vm`: the strongest structural isolation, real operational cost

`isolated-vm@6.1.2`, a fresh V8 isolate + context, nothing injected:

| Capability | Blocked? | Evidence |
| --- | --- | --- |
| `require` | **Yes (doesn't exist)** | `ReferenceError: require is not defined` |
| `process` | **Yes (doesn't exist)** | `ReferenceError: process is not defined` |
| `fetch` | **Yes (doesn't exist)** | `ReferenceError: fetch is not defined` |
| `Date.now()` | **No** | read cleanly — a bare V8 context still has the standard built-ins (`Date`, `Math`), which are engine features, not host bindings; no isolate-level flag removes them |
| `Math.random()` | **No** | same reasoning |

Structurally the strongest of the three: a fresh isolate starts with
**nothing** host-provided — no filesystem, no network, no environment,
no subprocess, no module system, because none of those are JS-engine
built-ins; every single one has to be explicitly bridged in by the host
(`jail.set(...)`) before the sandboxed code can even see it exists. That
is a fundamentally different security posture from Node's and Deno's
("everything exists, specific things are gated") — deny-by-nonexistence
rather than deny-by-permission-check, which cannot have Deno's own
remote-import-shaped gap (there is no module loader inside the isolate
at all unless the host builds one).

Two real, checked costs, not hypothetical:

1. **Native module, install friction confirmed this session.** `npm
   install isolated-vm` completed, but under this environment's own
   `npm` script-safety lockdown (`allow-scripts`), the package's real
   native build step (`node-gyp-build || node-gyp rebuild`) never ran —
   `npm warn allow-scripts 1 package has install scripts not yet
   covered...`. It happened to still load (`require('isolated-vm')`
   succeeded, likely a bundled prebuild), but that's exactly the kind of
   "worked this time, by luck of the platform/prebuild matrix" outcome
   this project doesn't accept as verification (STATE.md's own
   discipline: "verify before implementing," not "it loaded once").
   `ubx propose`/`ubx resolve --from-code` needs to run reliably on
   arbitrary users' machines (CI runners, every OS `ubx` itself already
   ships for) — a native-compiled dependency with its own
   platform/Node-ABI matrix is real, ongoing operational risk a
   dependency-free evaluator doesn't carry.
2. **No module system, no TypeScript, by design.** Deno runs `.ts` files
   directly; isolated-vm executes only compiled JS handed to it via
   `isolate.compileScript` — a program author's TypeScript needs an
   external transpile step (esbuild/tsc) before it ever reaches the
   isolate, and the *entire* host bridge (how `resource()`/`stack()` even
   get into the sandboxed global scope, how a multi-file program's own
   `import`s resolve) has to be hand-built rather than reusing a runtime
   that already has both.

### Decision: Deno, for `@ubx/sdk` v1

Deno wins on the actual empirical record: it's the only one of the three
that closes **three of the four** hard requirements (fs, env, net) by
default with zero flags, closes the fourth candidate's own worst gap
(Node's complete absence of net/env gating) outright, needed exactly one
additional flag (`--no-remote`) once a real gap was found rather than
assumed away, ships as a single dependency-free binary (no native
compile, no ABI matrix), and runs TypeScript directly — no separate
transpile step between a program author's `.ts` file and evaluation.
`isolated-vm`'s structural isolation is real and stronger in the abstract
(memory-isolated, not just permission-checked, and immune by construction
to Deno's own remote-import gap-shape), but v1 doesn't need a
process-escape-proof boundary on top of Deno's own OS-process isolation —
`ubx resolve --from-code` already runs the evaluator as a subprocess the
Go-side CLI fully controls (spawns, pipes stdout, kills on timeout/OOM),
the same trust boundary every other external binary this project shells
out to already gets (`git`, provider plugin binaries, `gh`). If Deno's
own guarantees ever prove insufficient for a threat model v1 hasn't
identified, `isolated-vm` is the named, evaluated fallback — not
something a future session would need to re-litigate from zero, its own
real costs (native build risk, hand-built bridge, no native TS) already
on record above.

### The clock/random gap: universal, closed by override + `DoubleRun`, not by permission flags

All three candidates left `Date.now()`/`Math.random()` completely
unblocked — this is not a gap specific to whichever sandbox got chosen,
it's a property of every JS engine: wall-clock time and PRNG state are
language built-ins, not host-mediated resources, so no permission system
(Node's, Deno's, or the total absence of one inside a bare `isolated-vm`
context) treats them as something to gate. Closing this needs a
different mechanism, layered twice:

1. **Eager override, inside the evaluator's own injected global scope**
   — before a program's own code runs, the harness replaces `Date` (its
   constructor and `.now()`) and `Math.random` with versions that throw a
   clearly-named `NondeterministicAPIError` immediately on use. This
   turns the overwhelmingly common case — an accidental `Date.now()` or
   `Math.random()` call landing directly in a resource's own config value
   — into an immediate, legible evaluation failure naming the exact
   call, not a value that merely *might* differ on a second run.
2. **`core.DoubleRun`, reused unchanged, as the backstop for what the
   override can't foresee** — the override only catches a nondeterminstic
   *value*; it does nothing for nondeterministic *control flow* that
   happens to produce a same-shaped-but-differently-ordered result (e.g.
   a native `Map`/`Set` whose insertion order depends on something
   incidental to a single run, or genuine `Promise`-ordering races if a
   program introduces artificial concurrency the runtime doesn't forbid
   outright). This is exactly `core.DoubleRun`'s own job already — see
   below — and exactly why UBI-33's own ticket names it as reused
   infrastructure rather than something this arc builds fresh.

## Double-run determinism at the evaluation boundary

`core.DoubleRun` (core/doublerun.go — built for canonical hashing,
already reused by the resolver, docs/resolver.md's own "any evaluator
feeding a hash must be double-run" discipline) is reused here
**unchanged**, exactly the signature it already has:
`func(fn func() ([]byte, error)) ([]byte, error)`.

**What's inside the closure matters, and this project has already been
burned by getting that boundary wrong once** (docs/reliability-report.md
— "A second, unrelated real bug found the same session": `resolvedAt`
called `time.Now()` fresh on each of two `DoubleRun` passes, producing a
false-positive `ErrDoubleRunMismatch` on every single resolve, fixed by
computing it once, outside the closure, and threading the same value into
both). The SDK evaluator's own closure follows the same discipline,
deliberately:

```go
canon, err := core.DoubleRun(func() ([]byte, error) {
    raw, err := runEvaluator(ctx, entryFile, sdkFlags) // spawn deno, capture stdout
    if err != nil {
        return nil, err
    }
    return canonicalIntentBytes(raw) // the new general-purpose canonicalizer, above
})
```

Everything that must be **identical** across the two passes — the
program's own logic, every resource's config, `intent.summary` — lives
inside the closure, spawned as two entirely separate `deno` subprocesses
(not two in-process calls into one running evaluator), which is a
**stronger** guarantee than the resolver's own existing `DoubleRun` use:
it also catches process-level nondeterminism (anything that could leak
between two runs sharing one process's memory/module cache) that an
in-process double-call structurally can't. Anything that's captured
**once** and must never vary between the two passes at all — there is
nothing analogous to `resolvedAt` here today (the SDK evaluator has no
timestamp of its own to stamp; a resolved `change` proposal's own
`resolved_at` is still assigned once by `core/resolver`, downstream of
this boundary, unchanged) — but the shape is named explicitly so a future
addition (e.g. a build/evaluation timestamp someone adds later) gets
threaded in the same correct way rather than rediscovering this bug.

A mismatch is `core.ErrDoubleRunMismatch`, unchanged, surfaced by `ubx
resolve --from-code` as a hard failure naming the two canonicalized
outputs' own diff — never a silently-accepted single-run intent.

## Codegen design: provider schema → IR model → TS templates

Reuses existing, already-verified machinery for the input half —
`provider.Acquire` + a launched binary's real `GetProviderSchema` (free,
no `Configure`, no credentials, confirmed cost-free and already exploited
this way once before, UBI-9 batch 3's `cmd/schemadump`) is the only
source of truth a codegen run ever reads from. No second, hand-maintained
description of what a provider's types look like.

### The IR model — language-neutral, no TS-isms

`provider.Schema`/`Block`/`Attribute`/`NestedBlock` (provider/schema.go)
is the real, already-shipped shape a schema dump arrives in:
`Attribute{Name, Type (raw ctyjson), Required, Optional, Computed,
Sensitive}`, `NestedBlock{TypeName, Nesting, Block}` recursively. The
shared IR model (`sdk/codegen/ir`) is a direct, structural translation of
this — not a reinterpretation — with exactly one deliberate rule to keep
it genuinely language-neutral:

```go
type ResourceType struct {
    WireType string   // e.g. "aws_db_instance" -- the real provider type string, verbatim
    Fields   []Field
}

type Field struct {
    WireName  string   // e.g. "instance_class" -- Attribute.Name, verbatim, snake_case
    Type      TypeRef
    Required  bool
    Optional  bool
    Computed  bool     // output-only in practice -> a Computed<T>-shaped binding
    Sensitive bool
}

type TypeRef struct {
    Kind    TypeKind    // Scalar | List | Set | Map | Object
    Scalar  ScalarKind  // String | Number | Bool | Dynamic
    Element *TypeRef    // List/Set/Map's element type
    Object  []Field     // Object/nested-block's own fields, recursive
}
```

**The one rule: `Field.WireName` is the only name the IR model carries,
and it is always the provider's real attribute name, snake_case,
untouched.** No per-language identifier convention (TS's own camelCase)
exists anywhere in `sdk/codegen/ir` — that choice belongs entirely to a
template, applied at generation time, and it matters for a concrete,
checked reason: the intent/v1 `resources[].config` a program's own
`resource()` call ultimately emits is handed straight to `ubx
resolve`/the executor/the real provider, which all expect the provider's
real wire names (`instance_class`), never a TypeScript-idiomatic one
(`instanceClass`). Baking `instanceClass` into the IR model itself would
mean either the wire format silently changes to match a TS convention
(breaking every non-TS consumer of the identical intent/v1 contract this
whole arc exists to guarantee) or every language's own codegen has to
carry an ad-hoc "translate back to whatever the IR model happened to
assume" step. Instead: the IR model only ever knows the wire name; the TS
template generates an idiomatic camelCase property (`instanceClass`) on
a `Config`/`Attrs` interface pair **and** a small, generated (never
hand-written) runtime descriptor object that maps it back to
`instance_class` — invisible to a program author, who only ever sees and
writes the idiomatic name, and never has to remember or get a wire name
right by hand.

**Refined during implementation (slice 3, this session): a declarative
`FieldMap` object, not a per-binding `toConfig()` method.** This
document's own original sketch, above, imagined codegen emitting one
`toConfig()` *method* per resource type — actually building the runtime
(`sdk/ts/runtime`) found a simpler, more DRY shape: codegen instead emits
one declarative `fields: FieldMap` object per resource type (a plain,
possibly-recursive data structure — a wire-name string leaf for a scalar
field, or `{wireName, kind, fields}` for a nested object/list/set/map of
objects), and `@ubx/sdk`'s own runtime carries exactly ONE shared,
recursive serializer function that walks any resource's own `FieldMap`
the same way. This means every resource type's wire-name translation
logic is *data* generated by `sdk/codegen/templates/ts`, not *code* —
codegen never needs to emit N slightly-different imperative
`toConfig()` method bodies, and the one shared serializer function is
what actually got hermetically tested (`sdk/ts/runtime/src/index_test.ts`),
not N generated ones. See `sdk/ts/runtime/src/index.ts`'s own `FieldSpec`/
`FieldMap` types and `serializeConfig`/`serializeOpaque` functions for
the real, built shape.

### `ubx sdk gen`: local, pinned, offline-after-generation

`ubx sdk gen` reads `.ubx/config`'s already-existing `[providers]` table
(docs/architecture.md's own Multi-provider stacks decision — the exact
same source of truth `ubx resolve`/`ubx ship` already read), acquires
each declared provider's real binary at its pinned version (`provider.
Acquire`, unchanged), dumps its schema, runs it through
`sdk/codegen/ir` + the TS template, and writes the result to disk inside
the user's own repo (e.g. `sdk/generated/<source>/` — exact path a CLI
detail, not pinned this session) — **committed to git, like any other
generated-but-reviewable code, not regenerated on every evaluate.** This
is a deliberate consequence of the hermetic evaluator's own "no network"
requirement: if evaluating a program meant re-fetching or regenerating
bindings on the spot, the evaluator subprocess would need exactly the
network access this whole design exists to deny. Generation is a
separate, explicit, online step (same "the network-touching step is
distinct from, and reviewed before, the trust-boundary step" shape
`--from-doc`'s own `ubx propose` already has relative to `ubx resolve`);
evaluation, afterward, is fully offline against already-generated,
already-on-disk files. Each generated file embeds the exact provider
source+version it was generated from (a comment plus a machine-readable
marker) — real, checked provenance for the "codegen against unknown
provider version" adversarial row, below, not an assumed-fresh default.

## Adversarial program

Matching every prior arc's own discipline
(docs/resolver-adversarial.md, docs/destroys-adversarial.md,
docs/multi-provider-adversarial.md, docs/intent-provider-adversarial.md):
each row is a required observable outcome, not a hope. Rows 1-5 are the
ticket's own named minimum; row 6 is a real, additional gap this
session's own empirical probing actually found (the remote-import finding
above) — named here rather than left implicit in the evaluator section
alone, matching this project's standing rule that a found gap gets a row,
not just a paragraph.

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 1 | Nondeterminism | A program's own resource config computes a value from `Date.now()` or `Math.random()`, directly or via a helper function several calls deep. | The evaluator's own injected global override throws `NondeterministicAPIError` immediately on the call, naming the API and (where feasible) the call site — the overwhelmingly common case never reaches `core.DoubleRun` at all. For anything the override can't see statically (e.g. iteration order of a native `Map` built from timing-dependent insertion), two independent `deno` subprocess runs produce different canonicalized bytes and `core.DoubleRun` hard-fails with `ErrDoubleRunMismatch`, unchanged from its existing behavior — never a single-run intent accepted as final. |
| 2 | Sandbox escape — fs/env/net reach | A program calls `Deno.readTextFileSync`/`Deno.writeTextFileSync`/`Deno.env.get`/`fetch` (awaited) directly inside its `stack()` body. | Every one throws `NotCapable`, confirmed this session's own probes with zero `--allow-*` flags plus the evaluator's explicit `--deny-*` set — never configurable wider by the program itself or by any `[sdk]`-style config table (no such table exists; the flag set is fixed in the harness, not exposed as a setting), matching the same "never enforced by default, always an explicit, narrower-only choice" posture `ubx scan --surface-as`'s own default already established (UBI-11 stage 3). |
| 2b | Sandbox escape — remote module resolution | A program's own `import` (static or dynamic `await import(...)`) names an `https://` specifier, or an unpinned bare npm specifier requiring registry resolution. | Blocked unconditionally by `--no-remote`, confirmed this session as the one flag that actually closes this path (`--deny-net` alone does not — see the evaluator section's own empirical finding). Every specifier a program can legally import resolves to a file already on disk under the evaluator's own narrowly-scoped `--allow-read` path (the program's own directory + `ubx sdk gen`'s already-generated bindings + the vendored `@ubx/sdk` runtime) — nothing is ever fetched at evaluate time. |
| 3 | Codegen against an unknown/mismatched provider version | `.ubx/config`'s `[providers]` pins `hashicorp/aws = 6.60.0`; the bindings actually on disk under `sdk/generated/` were generated against a different version whose schema disagrees (a field renamed, removed, or type-changed). | `ubx sdk gen` always regenerates from the exact config-pinned version's real, freshly-acquired schema — never a stale cache from a different version (same `provider.Acquire` version-pinned cache discipline `ubx scan`/`ubx accept --reverify-with` already trust). A genuine mismatch between what's on disk and current config is a distinct, named condition the CLI checks and reports before evaluation (comparing the generated file's own embedded version marker against `[providers]`), not silently evaluated anyway. If evaluation proceeds regardless (e.g. bindings never regenerated after a version bump) and produces a `resources[].type`/config shape the real pinned provider's schema doesn't actually own or accept, the failure is caught by `ubx resolve`'s own existing, unmodified "unowned type"/schema-mismatch checks (docs/multi-provider-adversarial.md row 3) — the SDK gets no bespoke second error path for a failure category the resolver already owns. |
| 4 | Program throws mid-evaluation | A `stack()` body throws a JS exception after some `resource()` calls already ran (e.g. the third of five resource calls throws). | The whole evaluation is a hard failure — no partial `intent/v1` is ever emitted, matching the project's standing "one whole draft, never a partial one" rule (chat's full-transcript redraft, `--from-doc`'s atomic write). The collector is only ever read by the harness after `stack()`'s body returns normally; a thrown exception is caught by the Deno-side harness wrapper (never left to crash the subprocess uncleanly) and surfaced through to `ubx resolve --from-code`'s own stderr with the program's real stack trace and message, verbatim — never swallowed, matching UBI-20's own "teaching errors" hardening discipline. |
| 5 | Output exceeding the `intent/v1` schema | A `resource()` config value is something `intent/v1` cannot represent at all — a function, a `Symbol`, a value containing a circular JS object reference, `undefined`, a float literal with no valid decimal-string encoding, or a raw, un-marked `Computed<T>` handle used directly as a value (row-1-adjacent, see `Computed<T>`'s own `ComputedCoercionError` above). | The collected document is validated against the existing, hand-maintained `intent/v1` JSON Schema (the identical schema `--from-doc`'s own structured-output validation already uses, reused unchanged — not a second, divergent schema for this producer) before it is ever handed to `ubx resolve` or written anywhere. A violation is a hard failure naming the exact resource, field, and JSON-Schema rule that failed — never a best-effort coercion (silently `JSON.stringify`-ing a function to `null`, dropping an unrepresentable field, etc.). |

**What this table doesn't yet cover, named rather than assumed
exhaustive** (matching docs/intent-provider-adversarial.md's own
"required minimum, not a claim of exhaustiveness" framing): resource
limits on the evaluator subprocess itself (a program that intentionally
infinite-loops or allocates unbounded memory — `deno run`'s own
`--v8-flags`/OS-level `ulimit`/a hard wall-clock timeout on the harness
side are the likely mechanism, not designed in numeric detail this
session); a program that spawns its own Web Worker (`new Worker(...)` is
a JS-standard API Deno also permission-gates, but this session's probes
didn't independently verify a worker inherits the parent's denied
permissions rather than getting its own fresh grant — a real, named gap
in this session's own verification, not assumed safe).

## Implementation slices, toward a real TypeScript payments program

Sized the same way docs/intent-provider.md's own slices were — named,
ordered, each independently a real session's worth of work, not a single
sweep:

### Slices 1–3: built (2026-07-28, session 2)

All three shipped, real code, live-verified, in one session. Two real
bugs were found by actually running the code against real inputs — not
by inspection — and are recorded here rather than silently fixed and
forgotten, matching this project's own "verify before implementing"
discipline:

- **A nested-block settability bug (`sdk/codegen/templates/ts`).** The
  first cut excluded every `NestedBlock`-derived field from a resource's
  own `Config` interface entirely — `fieldIsSettable`'s first version was
  `f.Required || f.Optional`, and `ir.go`'s own Field doc comment already
  states plainly that a nested-block-derived field carries all three
  flags (`Required`/`Optional`/`Computed`) false, a real schema fact, not
  a gap. The bug wasn't caught by reasoning about the rule in the
  abstract — it was caught by a test
  (`TestGeneratedFile_NestedObjectBlock`) that actually asserted on the
  rendered `Config` interface's own content, not just the `Attrs` one.
  Fixed to `f.Required || f.Optional || !f.Computed` — settable unless
  the field is Computed-only (an output-only attribute like `id`/`arn`),
  which correctly includes every nested block (never Computed) without
  a special case for it.
- **A scalar-map key-translation bug (`sdk/ts/runtime`).** The
  serializer's own scalar-leaf branch (used for a plain scalar field, or
  a `Record<string, string>`-typed `tags`-shaped field) originally called
  the SAME field-map-translating serializer recursively with an empty
  `{}` map — meaning a `tags` value like `{env: "prod"}` tried to look up
  `"env"` in an empty `FieldMap` and threw "unrecognized config field,"
  even though `tags`' own keys are arbitrary, user-chosen map keys that
  were never supposed to be translated or validated against a resource's
  field list at all. Found by a hermetic test
  (`a function in config throws`) that happened to nest its function
  value inside a `tags` map — the test failed with the WRONG error
  message ("unrecognized config field" instead of a function-representability
  error), which is what surfaced the real bug underneath a
  superficially-passing-looking assertion. Fixed by splitting the
  serializer into two genuinely different functions:
  `serializeConfig` (field-map-translated, for the top-level resource
  config and nested `KindObject` fields) and `serializeOpaque` (no key
  translation at all, for a scalar value and anything nested inside one)
  — a regression test
  (`a scalar-map field's own keys pass through untranslated`) locks this
  in with a key (`"Owner-Team"`) that would never round-trip through any
  wire-name conversion, proving the fix isn't coincidentally correct for
  simple key names only.

Three real, load-bearing decisions this session made and is recording
here (docs/sdk.md's own earlier "Out of scope" list had explicitly left
these to "the session that builds it"):

- **`resource()` always emits `op: "create"` in v1 — never `"modify"`.**
  Not named explicitly in this document's original design; found to be a
  real, structural necessity while building the runtime, not a
  simplification of convenience. A hermetic, describe-only program
  genuinely cannot know whether an address already exists in the ledger
  (no fs/net access, by design) — it has no basis to decide `create` vs
  `modify` the way `docs/schema.md`'s own hand-written intent file format
  requires that choice to be explicit, never inferred. Expressing
  `modify` intent from an SDK program is real, deferred future work (it
  would need the same kind of design attention `$cross`'s own pinning
  mechanism got), not silently unsupported.
- **`ubx sdk gen --out` defaults to `sdk/generated/`, one file per
  declared provider *source*** (never per resource type, never a
  `source/` subdirectory) — `<out>/<source-sanitized>.ts`, e.g.
  `sdk/generated/hashicorp-aws.ts`. A real provider owns dozens to
  hundreds of types (confirmed: `hashicorp/aws`'s real, current schema
  has 1,682 resource types); one cohesive, git-reviewable file per source
  avoids both a file explosion and an unnecessary extra directory level.
- **`docs/schema.md`'s own `$secret` inner shape was corrected in
  passing**, found while implementing `secret()`: the founding IR-node
  draft's inner shape (`{"ref": "..."}`) was never what any real worked
  example actually used (`{"backend", "path"}`, since this document's own
  UBI-27 intent-file amendment) — see `docs/schema.md`'s own corrected
  passage for the full account. `core/resolver`'s real code was, and
  still is, fully opaque to the inner object either way; the
  inconsistency was between two prose passages in that document, not a
  behavior bug.

Live verification, both real and load-bearing, not simulated: `ubx sdk
gen` ran against the real, already-cached `hashicorp/aws@6.54.0` schema
(1,682 resource types, zero errors, deterministic across repeated runs)
and separately against the real `hashicorp/time@0.9.2` schema, whose
small output was then used as the import target of a real, hand-written
TypeScript program (`time_offset`/`time_rotating`, one same-stack
`Computed<T>` reference wired between them) that `deno check` type-checks
cleanly and `deno run` evaluates correctly into a valid `ubx:intent/v1`
document — run under the EXACT locked-down flag set this document's own
evaluator section commits to (`--no-remote --deny-net --deny-read
--deny-write --deny-env --deny-run --deny-ffi --deny-sys`), confirming
those flags don't accidentally break the program they're meant to merely
sandbox, not just that they block what they're supposed to block.

`go build ./... && go vet ./... && gofmt -l . && go test ./...` clean
throughout (`sdk/codegen/ir`, `sdk/codegen/templates/ts`, `cli`'s own new
`TestSDKGen_*` hermetic tests via the existing `UBX_PROVIDER_MIRROR`
mechanism); `deno test --no-remote src/` (20 tests) and `deno check
--no-remote src/index.ts` clean for `sdk/ts/runtime`.

1. **`sdk/codegen/ir`** — **built.** The shared IR types +
   `provider.Schema` → IR translation, hermetic unit tests against
   fixture schemas (no real provider binary needed for this step — same
   "fake for unit tests, real for live verification" split the rest of
   the project already follows).
2. **`ubx sdk gen`** — **built.** The CLI verb (a new `sdk` parent
   command, per this document's own naming), real `provider.Acquire` +
   `GetProviderSchema` + the IR + a first TS template, live-verified by
   actually generating real bindings from `hashicorp/aws`'s real schema
   and confirming a hand-written `.ts` program using them type-checks and
   evaluates correctly — the same "checked against a real provider
   schema, not assumed" discipline UBI-23's own nested-sensitivity work
   already modeled.
3. **`sdk/ts/runtime` (`@ubx/sdk`)** — **built.** `stack`/`resource`/
   `secret`/`cross`/`intent`, the `Computed<T>` Proxy mechanism, the
   collector, the wire-name-mapping serializer — hermetically tested
   against fake codegen'd bindings (`deno test`, 20 cases), then
   live-verified against real `ubx sdk gen` output (above).
4. **The evaluator harness**: the Deno subprocess wrapper (flag set
   exactly as pinned above), the eager `Date`/`Math.random` override, the
   general-purpose canonical-JSON function (factored out of
   `canonicalProposalBytes`'s own JCS logic), `core.DoubleRun` wired in
   exactly as shown above. This session's own adversarial table (above)
   becomes this slice's own required test program, one hermetic test per
   row, before any of it is claimed working.
5. **`ubx resolve --from-code <entry>.ts`**: CLI wiring only — the SDK's
   evaluator is just another `intent/v1` producer, handed to the
   existing, completely unmodified `core/resolver` pipeline exactly as a
   hand-written file or an intent-provider draft (after human review)
   already is. No resolver changes expected; if one turns out to be
   needed, that itself is a finding worth stopping and recording, not
   silently absorbing.
6. **`sdk/conformance`**: the golden-fixture harness for real — the new
   canonical-JSON comparator, a first golden case. **This first case
   deliberately reuses the existing md-medium payments example**
   (`intentprovider/conformance/fixtures/payments.md` /
   docs/intent-provider.md's own worked JSON) as its own target shape,
   with one honest, structural difference named rather than papered
   over: an SDK-authored program has no interpretation step, so its own
   `intent.assumptions`/`defaults`/`questions` arrays are empty by
   construction — there is no ambiguity for a typed program to resolve,
   the human author simply writes `db.t3.small` directly, and the
   program's own source is the reviewable artifact (ordinary code
   review) rather than a post-hoc reviewable-assumption list. `intent.
   sources` gains its own new kind pair for this, mirroring
   `document`/`intent_provider`'s existing pairing exactly: `{"kind":
   "sdk", "ref": "payments.ts", "content_hash": "sha256:..."}` (the entry
   file only, in v1 — a multi-file program's non-entry imports are not
   independently pinned yet, a named, accepted v1 scope limit, not a
   silent gap) and `{"kind": "sdk_evaluator", "ref": "deno@2.9.4+@ubx/sdk@<version>"}`
   (which runtime produced it — the same provenance role
   `intent_provider`'s own `ref` field already plays for "which adapter
   drafted this"). **Formal pinning of this new `intent.sources` kind
   pair belongs in docs/schema.md, written in the session that actually
   builds slice 6** (UBI-41 session 1's own precedent: a design-only
   session amends docs/schema.md when a wire-format decision is made and
   load-bearing, but this session stops short of that for the SDK kind
   pair specifically, since nothing consumes it as real wire content
   until slice 6 exists to produce it) — named here so it isn't
   forgotten, not pinned prematurely.
7. **Live finale**: a real TypeScript payments program, evaluated for
   real through the real Deno harness, producing canonicalized
   `intent/v1` bytes — resolved, accepted, and (where a live provider
   session makes sense) shipped for real, closing UBI-34. Three
   independent producers (a hand-written intent file, the md medium's LLM
   transcription, and this arc's own typed TS program) converging on the
   same resolved infrastructure is the strongest available proof that
   "the SDK is a producer of `intent/v1`, nothing more" (UBI-33's own
   framing) actually holds, not just an aspiration stated in this
   document.

## Out of scope for v1, named so it isn't assumed covered

Go and Python's own evaluators/runtimes (UBI-35/36, each its own sizing
and its own sandbox story — Go's compiled-program "cheat," Python's "no
cheat, wait for demand," per the ticket's own risk note); typed
cross-stack handles (`cross()` takes a hand-typed address string in v1,
the same posture a hand-written `$cross` marker already has — a
codegen'd handle that type-checks a neighbor stack's own resource shape
is real, useful future work, not designed here); `ubx sdk gen`'s exact
CLI flags and where generated files live inside a user's repo (a real
detail, deliberately left to the session that builds it rather than
guessed at here); resource limits (CPU/memory/wall-clock) on the
evaluator subprocess, beyond noting the harness needs some (see the
adversarial table's own "what this doesn't cover"); a policy engine
gate on anything an SDK program produces (component map #9, still not
built at all, unrelated to this arc specifically); publishing
`@ubx/sdk` to npm for real (mentioned in UBI-34's own scope, no release
plumbing designed this session).
