# SDK program — the multi-language contract, complete across TypeScript, Go, and Python (UBI-33/34/35/36)

> **All three languages shipped, and UBI-33 (the multi-language
> contract) is closed.** UBI-34 (TypeScript) closed 2026-07-28, session
> 4 — all seven implementation slices built, tested, and live-verified;
> see "Slices 1–3: built," "Slice 4: built," and "Slices 5–7: built,"
> below. UBI-35 (Go) closed 2026-07-30, session 1 — the compiled-program
> evaluator hypothesis tested empirically and confirmed; see "The Go
> evaluator: decided empirically," below. UBI-36 (Python) closed
> 2026-07-30, session 1 — the evaluator decision reversed its own
> expected outcome (WASI beat the expected subprocess-sandbox
> front-runner); see "The Python evaluator: decided empirically," below.
> Each language's own runtime, codegen templates, `resolve --from-code`
> dispatch, and conformance case were built the same session as its own
> evaluator decision — real, not just designed. Session 1's own original
> design intent below is preserved as the historical record of what was
> decided before any code existed; it is superseded wherever a later
> "built" section says so, not silently.
>
> Session 1, design only, no code (historical). This document is the
> contract half of UBI-33 (the umbrella: multi-language contract + shared
> codegen) and the TypeScript-specific design half of UBI-34 (`@ubx/sdk`,
> first language to ship). Two hard constraints came pre-decided from the
> ticket's own design room (Pulumi case-study comments) and are not
> relitigated here: the **monorepo** (`sdk/` inside `ubiquex`, all
> languages, one CI) and **codegen'd bindings are generated locally,
> never published** (`ubx sdk gen` runs against the config-pinned
> provider version on the user's own machine; only the tiny `@ubx/sdk`
> runtime ships to npm). **This second constraint's own PREMISE — one
> flat file per provider, with no reasonable path to becoming a real repo
> at all — is superseded by UBI-98 (2026-08-03/04); read "Current,
> authoritative summary (UBI-100)" immediately below before trusting
> anything in this document about output shape, publishing, or import
> paths.** The distinction itself (locally generated, never a Ubiquex-
> maintained release) still holds, revised: generation is now
> repo-*shaped*, not flat, specifically so it CAN become a real pushed
> repo at the user's own discretion — and, in practice, already has, for
> all 12 real provider/language pairs (UBI-99 and its own ports). Language
> order is also pre-decided: TypeScript, then Go, then Python
> (`docs/architecture.md`'s own "What carries over from v1" already names
> `Computed<T>`/`secret()`/typed refs as v1 design worth keeping — this is
> where that design gets rebuilt, in code, for the first time in v2).

## Current, authoritative summary (2026-08-05, UBI-100) — read this first

This document is a real, chronological engineering record — 20+ session
amendments below, each with its own live-verified findings, none deleted
or rewritten after the fact, per this project's own standing discipline.
That makes it a poor *first* read for "what's actually true today." This
section is that answer, in one place; the amendments below remain the
full supporting narrative (why each decision was made, every real bug
found along the way), not superseded, just not where a new reader should
start.

**The revised posture, stated precisely (UBI-98's own ticket language,
reused verbatim, not re-derived)**: bindings are generated as a
repo-**shaped** local directory (its own `go.mod`/`package.json`/
`pyproject.toml` stub) — still 100% locally generated from the
config-pinned provider version, still never a versioned/maintained
release *by Ubiquex* — but now structured so it CAN become a real pushed
repo at the user's own discretion. `ubx sdk gen` itself never pushes
anywhere or needs git credentials; whether/how the local output becomes
a real repo is left entirely to the user (or CI) — the same posture as
any other generated code being reviewable before it's committed.

**`--out`'s real behavior**: writes a full repo-shaped tree, never a
single flat file. `ubx sdk gen --lang <go|ts|py> --out <dir>` writes
`<dir>/<lang>/<source-sanitized>/` (`--lang` is its own path segment —
generating multiple languages against one shared `--out` would otherwise
interleave their manifests into one directory) — its own manifest stub,
one directory per derived service boundary (`aws_iam_*` → `iam`,
`aws_ecr_*` → `ecr`, ...), one file per resource type, every service
package nested under one provider-namespace directory (`aws/iam/`, never
`iam/` at the repo root — UBI-106, a real repo-browsing fix). The
redundant `Aws<Service>` prefix is dropped from every generated type name
since the import path already encodes provider+service.

**The per-service-package import pattern, a real worked example pulled
directly from the actual live repo** (`https://github.com/Ubiquex/
ubx-sdk-aws-go`, `aws/iam/role.go`, fetched live via the GitHub API while
writing this, not copied from an old transcript):

```go
import (
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
	iam "github.com/ubiquex/ubx-sdk-aws-go/aws/iam"
)

sdk.Resource(iam.Role, "ci-runner", iam.RoleConfig{
	AssumeRolePolicy: "...",
})
```

`iam.Role`, never `generated.AwsIamRole` — the redundant prefix is gone,
the import path itself (`aws/iam`) carries the provider+service context
instead.

**A real, load-bearing naming distinction, easy to conflate, checked
directly rather than assumed**: `ubx sdk gen`'s own MECHANICAL module
name (what it writes into a freshly-generated `go.mod`/`package.json`/
`pyproject.toml`) is always `ubx-sdk-<shortName>` — e.g.
`github.com/ubiquex/ubx-sdk-aws` for `hashicorp/aws` — with **no
per-language suffix at all** (`sdk/codegen/templates/go/go.go`'s own
`GeneratedRepo`, confirmed by reading the source directly). The REAL,
separately-hosted repos this project actually publishes (below) carry a
manually-chosen `-go`/`-ts`/`-py` suffix instead
(`ubx-sdk-aws-go`/`ubx-sdk-aws-ts`/`ubx-sdk-aws-py`) — a one-time rename
the founder applies before establishing each repo as its own real,
independent (provider, language) pair, never something `ubx sdk gen`
derives on its own. This is exactly why the version-watch automation
(next) excludes each repo's own manifest file from its regeneration
diff — regenerating it in place would silently revert the module path
back to the mechanical, unsuffixed name.

**12 real, live, separately-hosted provider-binding repos exist today**,
one per (provider, language) pair, all seeded via a real `ubx sdk gen
--out .` and kept current by UBI-99's own real GitHub Actions automation
(a scheduled + manually-dispatchable workflow that queries the real
Terraform Registry API for a newer provider version, regenerates, and
opens a PR — never auto-merges):

| Provider | Go | TypeScript | Python |
| --- | --- | --- | --- |
| AWS | [ubx-sdk-aws-go](https://github.com/Ubiquex/ubx-sdk-aws-go) | [ubx-sdk-aws-ts](https://github.com/Ubiquex/ubx-sdk-aws-ts) | [ubx-sdk-aws-py](https://github.com/Ubiquex/ubx-sdk-aws-py) |
| Google | [ubx-sdk-google-go](https://github.com/Ubiquex/ubx-sdk-google-go) | [ubx-sdk-google-ts](https://github.com/Ubiquex/ubx-sdk-google-ts) | [ubx-sdk-google-py](https://github.com/Ubiquex/ubx-sdk-google-py) |
| Kubernetes | [ubx-sdk-kubernetes-go](https://github.com/Ubiquex/ubx-sdk-kubernetes-go) | [ubx-sdk-kubernetes-ts](https://github.com/Ubiquex/ubx-sdk-kubernetes-ts) | [ubx-sdk-kubernetes-py](https://github.com/Ubiquex/ubx-sdk-kubernetes-py) |
| Azure | [ubx-sdk-azure-go](https://github.com/Ubiquex/ubx-sdk-azure-go) | [ubx-sdk-azure-ts](https://github.com/Ubiquex/ubx-sdk-azure-ts) | [ubx-sdk-azure-py](https://github.com/Ubiquex/ubx-sdk-azure-py) |

**Recommended way to keep a real pushed bindings repo current**: rely on
UBI-99's own version-watch automation (already running, unchanged, in
all 12 repos above) rather than re-running `ubx sdk gen` by hand — it
already handles the version-detection/regenerate/PR/never-auto-merge
cycle, with the manifest-exclusion behavior named above already built
in.

**Runtime package publish status, checked live against the real
registries while writing this, not assumed from an old amendment**:
TypeScript's `@ubx/sdk` is genuinely published — on **JSR**
(`jsr:@ubx/sdk@0.1.0`, confirmed via `https://jsr.io/@ubx/sdk/meta.json`)
— **not** on npm in any real sense: `@ubx/sdk` exists on the npm registry
too, but as a deliberate placeholder (version `0.0.1`, its own
description literally reads "Placeholder — see jsr.io/@ubx/sdk for the
real package"), confirmed via a live `registry.npmjs.org` query. Go's
`github.com/ubiquex/ubx-sdk-go` is genuinely published to the real public
Go module proxy — confirmed live via `proxy.golang.org/github.com/
ubiquex/ubx-sdk-go/@latest`, resolves `v0.0.0`, zero credentials, zero
`replace` directive needed. **Both directly contradict this document's
own Session-1-era "only the tiny `@ubx/sdk` runtime ships to npm" line
above** — corrected here, not silently. Python's `ubx_sdk` runtime is
now genuinely published too, on **PyPI** (`https://pypi.org/project/ubx-sdk/`,
UBI-107, real `twine upload`, confirmed live via `pypi.org/pypi/ubx_sdk/json`
and a real `pip install ubx-sdk` in a fresh venv) — all four real live
Python bindings repos (`ubx-sdk-{aws,google,kubernetes,azure}-py`) have
been switched from vendoring (`vendor/ubx_sdk/`) to a real pinned
dependency (`ubx_sdk>=0.1.0,<0.2.0` in `pyproject.toml`), matching TS's
own JSR-switch precedent (UBI-110) — see this document's own UBI-107
amendment below for the full account.

**Conformance fixtures, current real shape** (`sdk/conformance/programs/
{go,ts,py}/generated/`, re-verified directly against the actual on-disk
files while writing this, not trusted from prose alone): Go and
TypeScript both nest under `hashicorp-aws/aws/db/` (a nested repo-shaped
module/package, `db` being `aws_db_instance`'s own mechanically-derived
service); Python's own fixture is `generated/aws/db/` directly, deliberately
NOT nested under a hyphenated `hashicorp-aws/` intermediate directory —
Python's dotted `import` syntax cannot traverse a hyphenated path segment
at all, a real, load-bearing, already-documented deviation from the real
CLI's own output shape (Amendment "UBI-98 session 2," below), not an
inconsistency.

**Python's real package layout, superseding every earlier `aws/iam/role.py`-
style example in this document for Python specifically**: every provider's
Python bindings nest under a shared `ubx` PEP 420 implicit namespace
package (no `ubx/__init__.py` — real precedent, `google.cloud.*`/
`azure.mgmt.*`), so the real import is `from ubx.aws.iam import Role,
RoleConfig` — never `aws.iam.role`, and never the file-stutter
`ubx.aws.iam.role`. See this document's own newest amendment, near the end of this
document ("Python namespace-package layout"), for the full account;
the Go/TS worked examples above are unaffected.

**Consistency with `docs/blueprint.md`**: confirmed, no contradiction — that
document's own generated `go.mod`s already correctly `require
github.com/ubiquex/ubx-sdk-go v0.0.0` and repeatedly confirm real,
credential-free resolution against the real published module (e.g. "the
actual published `ubx-sdk-go` module — real network, no `replace`
directive"), consistent with the real publish status confirmed above.

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

> **Superseded — real output shape, UBI-98/99/106 (see "Current,
> authoritative summary (UBI-100)" at the top of this document).** The
> `sdk/generated/<source>/` single-directory path below, and the
> unstated (implicitly single-file) shape it describes, are both Session
> 1's own original, historical design intent — not what `ubx sdk gen`
> actually writes today. The real path is `<out>/<lang>/<source-
> sanitized>/`, a repo-shaped tree (own manifest, one directory per
> service, one file per resource type, nested under one provider-
> namespace directory), and the real output is routinely pushed to a
> real, separately-hosted, per-(provider,language) GitHub repo — 12 of
> them exist today, kept current by real automation (UBI-99). Preserved
> below verbatim as the historical record of what was designed before any
> of that existed.

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
| 2b | Sandbox escape — remote module resolution | A program's own `import` (static or dynamic `await import(...)`) names an `https://` specifier, or an unpinned bare npm specifier requiring registry resolution. | Blocked unconditionally by `--no-remote`, confirmed this session as the one flag that actually closes this path (`--deny-net` alone does not — see the evaluator section's own empirical finding). Every specifier a program can legally import is either a literal, statically-analyzable path the evaluator's own generated runner script already resolved (the entry file, its own local relative imports, `ubx sdk gen`'s generated bindings) or the `@ubx/sdk` runtime via the harness's own import map (session 3's own build: embedded in the `ubx` binary, extracted to a temp directory once per process) — nothing is ever fetched at evaluate time, and (session 3's own correction to this row's original text) no `--allow-read` carve-out is needed for any of it at all; see the evaluator section's own "Slice 4: built" account for the full, corrected mechanism. |
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

### Slice 4: built (2026-07-28, session 3)

The evaluator harness — real, spawning real `deno` subprocesses, all
five of this document's own required adversarial rows confirmed against
them, not simulated. One design correction this session found, rigorous
enough to be worth walking through in full, since it directly contradicts
what session 1 believed and this session initially re-asserted before
actually testing the real, parameterized shape.

**Session 1's own `--allow-read` question, finally settled — and settled
differently than either session initially believed.** Session 1's
original design speculated a narrow `--allow-read` carve-out would be
needed for "the program's own directory tree." Early in this session, a
quick re-probe (a fixed script statically importing one hardcoded
sibling file) seemed to show full `--deny-read` working with no carve-out
at all — a tempting, simpler answer. **That probe was insufficiently
rigorous, and relying on it would have shipped a broken harness design:**
a fixed script can't parameterize which entry file to evaluate, and the
real harness necessarily needs to. A follow-up probe — dynamically
importing a path built from `Deno.args` via `new URL(...)`, the shape the
harness actually needs — failed immediately with `NotCapable`, even
pointing at a file that loaded fine moments earlier as a literal. Four
more isolated probes pinned the real, precise rule down: **Deno's
pre-execution static module-graph analysis (which needs no read
permission at all) applies to any import specifier that is a literal
string directly in an `import`/`import()` — same directory, a `../`
sibling directory, or an absolute path, all confirmed working under full
`--deny-read` — but NOT to a specifier assembled at runtime, even via
trivial string concatenation**, which requires real, permission-gated
file access indistinguishable from arbitrary user-directed reads. This
is a directory-independent rule (not "same folder is trusted, elsewhere
isn't," which was this session's own first, wrong hypothesis) — it's
about whether Deno can resolve the whole module graph before the program
ever starts running.

**The fix this finding drove: `runner.go` generates a fresh runner
script per evaluation, with the entry file's own absolute path baked in
as a literal `import` specifier — never a fixed script that dynamically
imports a path read from argv.** This is safe specifically because
`stack()` (slice 3) already defers running a program's own describe
function to an explicit `.evaluate()` call: even though ES module
evaluation order means every statically-imported module (including the
entry file) finishes evaluating before the runner script's own top-level
statements run, the entry file's own module body is just `export default
stack(...)`, which touches nothing nondeterministic — the eager
`Date`/`Math.random` guards only need to be installed before
`.evaluate()` is explicitly *called*, a statement safely ordered after
`installNondeterminismGuards()` in the runner's own body, regardless of
import timing. Confirmed with the full, realistic shape combined in one
probe (a generated runner with a literal absolute entry import, the
entry file's own relative sibling import, and the `@ubx/sdk` runtime
resolved via an import map) — all under complete `--deny-read` — before
writing any Go code against it.

**The result is a stronger sandbox than session 1's own original design
even asked for: zero `--allow-read` carve-out, not a narrow one.**
`evaluatorFlags` (`sdkeval/runner.go`) is exactly `--no-remote` plus
every `--deny-*` flag, unconditionally, with no scoped exception at all.

**`core.CanonicalJSON`/`CanonicalJSONBytes`** (`core/canonical.go`): the
general-purpose canonicalizer this document named as real, unstarted
work, built by factoring `canonicalProposalBytes`'s own JCS logic (sorted
keys via a single `map[string]interface{}` marshal, the same
int64/decimal-string number rule) out of its Proposal-specific field-
exclusion/delta-sorting wrapper — `canonicalProposalBytes` itself now
calls the new exported function rather than duplicating the logic.
Six new hermetic Go tests (`core/canonical_test.go`) cover key-order/
whitespace insensitivity, float rejection, and large-integer precision
directly, independent of `Proposal`.

**`sdk/ts/evaluator/guards.ts`**: the eager override, a `GuardedDate`
class extending the real `Date` (throwing `NondeterministicAPIError`
only for the zero-argument constructor form and `Date.now()` — a Date
built from explicit arguments, e.g. a parsed ISO string, is fully
deterministic and stays legal, a real, deliberate distinction, not
over-blocking) plus a `Math.random` override with the same error.
Evaluator-harness-only code — never part of `@ubx/sdk` itself, never
published, imported only by a generated runner script, never by a
program author. Five hermetic `deno test` cases.

**`sdk/ts/embed.go`** (new `tsassets` package) embeds `guards.ts` and
`@ubx/sdk`'s own `runtime/src/index.ts` directly into the `ubx` binary —
evaluation never depends on a separate file living alongside the binary,
or on the user having run `npm install @ubx/sdk` (still unpublished).
`sdkeval/assets.go` extracts both to a temp directory once per process
(`sync.OnceValues`) and writes a `deno.json` there mapping the bare
`@ubx/sdk` specifier to the extracted runtime file — confirmed this
session that an import map discovered relative to the runner script
governs resolution for the entire module graph, including the entry
file's own directory, wherever that is.

**`sdkeval`** (new top-level Go package, not folded into `cli/` — this
document's own open question, decided: a standalone package matches this
project's established shape for a substantial, independently testable
subsystem, the same as `intentprovider/`/`cloudtrail/`/`gcpaudit/`).
`Evaluate(ctx, entryFile) ([]byte, error)`: `core.DoubleRun` (reused
completely unchanged) wraps two real `runOnce` subprocess launches,
canonicalizing via the new `core.CanonicalJSONBytes` inside the closure —
exactly the shape this document's own "Double-run determinism" section
already pinned. `runOnce` looks up `deno` via `PATH` (a clear, actionable
error — naming `https://deno.com` — if it isn't installed, matching
UBI-20's own "teaching errors" discipline), generates and writes the
per-evaluation runner script, and surfaces the subprocess's own stderr
verbatim in any failure (row 4's own requirement).

**Row 5 ("output exceeding the intent/v1 schema") also required a real
correction, found while implementing it, not assumed correct from this
document's own original text.** The original design said this row would
reuse "the identical schema `--from-doc`'s own structured-output
validation already uses" (`intentprovider.IntentDraftJSONSchema`) —
reading that package's own code directly (not just its name) found this
to be actively wrong: that schema is *deliberately* a different,
incompatible shape, per its own doc comment — a resource's own `config`
is a JSON-encoded **string** there (an LLM structured-output API can't
express an open-ended nested object), `sources` is entirely absent, and
`intent.assumptions`/`defaults`/`questions` are *required* even when
empty. None of that matches what `@ubx/sdk`'s own runtime actually
emits (`config` is a real nested object; `sources` is present;
assumptions/defaults/questions are never populated at all). Reusing it
verbatim would have rejected every valid SDK document. **The corrected,
better design** (`sdkeval/validate.go`): strict-unmarshal
(`DisallowUnknownFields`) against `core/resolver.IntentFile` — the REAL,
load-bearing Go type `ubx resolve` itself already parses a hand-written
intent file into — plus a handful of direct structural checks
(`schema_version`, `kind`, non-empty `stack`/`summary`, non-empty
resource `type`/`name`, `config` decodes as a JSON object, `op` is
always exactly `"create"` — this session's own new, real enforcement of
the `op: "create"`-only decision, at the Go boundary, not just left as a
TS-side convention). One canonical Go source of truth for the wire
shape, not a second, hand-maintained JSON Schema that could silently
drift from it — a genuinely better reuse than what was originally
planned. Nine hermetic Go tests (`sdkeval/validate_test.go`) exercise
this directly with hand-crafted JSON, not a real subprocess — @ubx/sdk's
own runtime (slice 3) already preemptively blocks every one of these
shapes at its own `resource()`/`intent()` API boundary (empty
name/summary throw; `op` is hardcoded by the collector itself, never
read from program input), so there is no honest way for a normal SDK
program to reach this Go-side check with bad output at all through the
legitimate surface — it is real defense-in-depth, not a demonstrated
live bypass, and is documented as such rather than overclaimed.

**All five of this document's own required-for-this-slice adversarial
rows, confirmed against real `deno` subprocesses** (`sdkeval/sdkeval_test.go`,
fixture programs under `sdkeval/testdata/`): row 1 nondeterminism, in
both its layers — `Date.now()` caught immediately by the eager guard
(never even reaching a second subprocess run), and a **separate,
concrete backstop test**: `Deno.pid` (real, legitimate process
introspection the guards don't and shouldn't block) leaking into a
resource's own config, differing for real between `DoubleRun`'s two
independent subprocess runs, correctly producing `core.ErrDoubleRunMismatch`
— proof the second layer of defense actually catches what the first
layer structurally cannot see. Row 2, fs/env/net reach, each blocked
individually with a real `NotCapable` error. Row 2b, the dynamic
remote-import escape session 1 found empirically — confirmed blocked
again here, through the real end-to-end harness, not just a standalone
probe. Row 4, a program throwing after one resource already ran — the
real thrown message surfaced verbatim, and confirmed `Evaluate` returns
**no output at all** alongside the error, never a partial document.

`go build/vet/test`, `gofmt -l .` clean across the whole repo (20 new
tests in `sdkeval`, 6 new in `core`); `deno test --no-remote
guards_test.ts` (5 tests) and `deno check --no-remote guards.ts` clean
for `sdk/ts/evaluator`.

### Slices 5–7: built (2026-07-28, session 4) — CLI wiring, real convergence, closing UBI-34

`ubx resolve --from-code` turned out to be exactly wiring, as slice 5's
own text predicted — no resolver changes needed. One real, deliberate
simplification was made along the way, and one real live comparison
against the md medium closes the arc's own central claim for real, not
as an aspiration.

**`intent.sources` gets a single `"document"` entry, not the `"sdk"`/
`"sdk_evaluator"` pair session 1 originally sketched.** `sdkeval/provenance.go`'s
`stampDocumentSource` (new) computes the entry file's own real SHA-256,
Go-side (the sandboxed evaluator has no fs access to hash its own file,
by design) and appends `{"kind": "document", "ref": "<basename>",
"content_hash": "sha256:..."}` — reusing the exact same `"document"` kind
the md medium already uses for its own source file, rather than inventing
a bespoke SDK-only vocabulary. The reasoning: code is self-describing and
fully deterministic, with no analogous "which adapter drafted this" fact
worth a second entry the way `intent_provider`'s own kind names which LLM
transcribed a document — there is no LLM in this path at all. This is
what makes "three independent producers converge" a checkable claim using
one shared provenance vocabulary, not three bespoke ones. Appends to
(never overwrites) whatever sources a program's own `intent()` call may
already have set. Four new hermetic tests (`sdkeval/provenance_test.go`).

**`cli/resolve.go` gained `--from-code`, mutually exclusive with the
existing positional argument** (`cobra.MaximumNArgs(1)`, was
`ExactArgs(1)`) — `sdkeval.Evaluate` produces the `resolver.IntentFile`
the exact same, completely unmodified pipeline already consumes; nothing
downstream of that point changed at all. `--timeout`'s default doubled
(60s → 120s) and its help text now names both roles it covers (provider
schema fetch AND, with `--from-code`, evaluation) — both share one
budget, an existing pattern (the multi-provider loop already shares one
timeout across several provider fetches) extended, not a new one.
Hermetically tested end to end (`cli/resolve_from_code_test.go`): evaluate
→ resolve → accept → why, through the real `UBX_PROVIDER_MIRROR` fake
provider, confirming the real provenance stamp lands in the real accepted
proposal's own `ubx why` rendering.

**`sdk/conformance` built for real** (`programs/ts/`, `golden/`,
`runner/`) — a first golden case, `payments`, matching this session's own
live finale (below). `programs/ts/generated/hashicorp-aws.ts` is real
codegen output (`sdk/codegen/ir` + `sdk/codegen/templates/ts`, the exact
same machinery `ubx sdk gen` uses) against the real, cached
`hashicorp/aws@6.54.0` schema — filtered to `aws_db_instance` alone
(confirmed live this session: a real, unfiltered `ubx sdk gen` run
against this exact provider produced 1,682 resource types; committing all
of them for a one-resource fixture would dwarf what it supports).
`runner/runner_test.go`'s `TestPaymentsGoldenCase_TS` evaluates the real,
committed `payments.ts` through the real Deno harness and byte-compares
against `golden/payments.json` after canonicalizing both sides (the
committed fixture is pretty-printed for reviewability, never assumed to
already be in canonical form) — a real, ongoing regression test, not a
one-time manual check: if a future runtime/codegen change ever drifts
from this golden shape, this test catches it immediately.

**The live finale, real, live, end to end — the strongest form of the
claim, not an approximation.** No committed "golden" transcript existed
anywhere to compare against beforehand (drafts are ephemeral by design,
never persisted past review — confirmed by checking, not assumed); rather
than approximate one, this session ran the real thing:

1. `ubx propose --from-doc payments.md` against the **real Claude API**,
   fresh, this session. Real output: a standalone `aws_db_instance`
   named `"payments"` (op: create, no `$ref` to staging at all — the
   `@payments.aws_db_instance.staging` mention in the doc is read purely
   as *context* for the model's own reasoning, never becoming a live
   reference, since the intent provider has no ledger access to query
   staging's real values in the first place, confirmed empirically, not
   assumed from the design docs' own older, hypothetical illustration
   sketch, which showed a different, replica-shaped example entirely
   and was never itself a real transcript). Real, live-drafted values:
   `engine: "postgres"`, `instance_class: "db.t3.small"`,
   `allocated_storage: 20`, `db_name: "payments"`,
   `username: "payments_admin"` — with real `assumptions`/`defaults`/
   `questions` explaining each choice in the model's own words.
2. Resolved for real (`ubx resolve draft.json`, real `hashicorp/aws@6.54.0`
   schema) — `delta.creates[0].config` matches the drafted values exactly,
   no schema rejections, no missing-required-field errors.
3. A TypeScript program (`sdk/conformance/programs/ts/payments.ts`)
   authored with the **identical concrete values**, copied verbatim from
   step 1's own real output — the human author's own decision, made *after*
   seeing what the real LLM said, so this is a genuine convergence check,
   not a coincidence engineered backwards from a value nobody actually
   produced independently.
4. Evaluated for real (`ubx resolve --from-code payments.ts`, the same
   real provider schema) — `delta.creates[0]` matches step 2's shape
   field for field.
5. **Checked rigorously, not eyeballed**: both resolved proposals'
   `delta.creates` arrays, canonicalized via the same `core.CanonicalJSON`
   this arc built in slice 4, compared byte-for-byte —

   ```json
   [{"config":{"allocated_storage":20,"db_name":"payments","engine":"postgres","instance_class":"db.t3.small","username":"payments_admin"},"name":"payments","provider":{"source":"hashicorp/aws","version":"6.54.0"},"stack":"payments","type":"aws_db_instance"}]
   ```

   **identical**, for both. `intent.summary` also matches exactly (copied
   verbatim). The one honest, structural, expected difference: `intent.
   sources`/`assumptions`/`defaults`/`questions` — the md-drafted proposal
   carries `document`+`intent_provider` sources and three real ambiguity
   notes explaining the model's own choices; the TS-authored one carries
   one `document` source and no ambiguity notes at all, because there was
   no ambiguity for a typed program to resolve — the human author simply
   wrote `db.t3.small` directly, and the program's own source is the
   reviewable artifact, ordinary code review, not a post-hoc
   reviewable-assumption list. This is exactly the difference slice 6's
   own original design predicted, now confirmed against real output
   rather than only asserted.

**UBI-34 closed in Linear — TypeScript is complete.** All seven of this
document's own implementation slices are built, tested (hermetically and
live), and documented, closing the loop this arc's own framing opened:
"the SDK is a producer of `intent/v1`, nothing more" — now demonstrated,
not just designed. **UBI-33 stays open** — the multi-language contract's
own Go (UBI-35) and Python (UBI-36) futures are unstarted; this session's
own `sdk/codegen/ir` and canonical-JSON discipline are exactly the shared
foundation those languages will build against, per this document's own
"language-neutral, no TS-isms" design.

`go build/vet/test`, `gofmt -l .` clean across the whole repo (4 new
`sdkeval` tests, 3 new `cli` tests, 1 new `sdk/conformance/runner` test).
`ubiquex-docs` gained `cli/sdk-gen.mdx` (new), a new "Authoring in
TypeScript" section on `cli/resolve.mdx`, and a full rewrite of
`sdk/index.mdx` from its old "not yet released" placeholder — every
example real, taken directly from this session's own live transcripts;
`mint validate`/`mint broken-links` both clean. Both repos committed and
pushed. See STATE.md for the full session account.

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
4. **The evaluator harness** — **built.** The Deno subprocess wrapper
   (flag set exactly as pinned above — turned out to need NO
   `--allow-read` carve-out at all, a real correction to this document's
   own original speculation, found and explained in full in "Slice 4:
   built," above), the eager `Date`/`Math.random` override
   (`sdk/ts/evaluator/guards.ts`), the general-purpose canonical-JSON
   function (`core.CanonicalJSON`/`CanonicalJSONBytes`, factored out of
   `canonicalProposalBytes`'s own JCS logic), `core.DoubleRun` wired in
   exactly as shown above (`sdkeval.Evaluate`). This session's own
   adversarial table's five in-scope rows (1, 2, 2b, 4, 5) are each a
   real hermetic test against a real `deno` subprocess (row 5 at the Go
   level, for the reasons explained above), all passing.
5. **`ubx resolve --from-code <entry>.ts`** — **built.** CLI wiring only,
   exactly as predicted — the SDK's evaluator is just another `intent/v1`
   producer, handed to the existing, completely unmodified `core/resolver`
   pipeline exactly as a hand-written file or an intent-provider draft
   (after human review) already is. No resolver changes were needed.
6. **`sdk/conformance`** — **built.** The golden-fixture harness for
   real: the new canonical-JSON comparator (`core.CanonicalJSON`/
   `CanonicalJSONBytes`, slice 4), a first golden case (`payments`).
   **This first case deliberately reuses the existing md-medium payments
   example** (`intentprovider/conformance/fixtures/payments.md`) as its
   own target shape, with one honest, structural difference named rather
   than papered over: an SDK-authored program has no interpretation
   step, so its own `intent.assumptions`/`defaults`/`questions` are
   absent entirely, by construction — there is no ambiguity for a typed
   program to resolve, the human author simply writes `db.t3.small`
   directly, and the program's own source is the reviewable artifact
   (ordinary code review) rather than a post-hoc reviewable-assumption
   list. **`intent.sources` gets a single `"document"` entry — not the
   `"sdk"`/`"sdk_evaluator"` kind pair originally sketched here.** Real,
   deliberate simplification, decided this session (see "Slices 5–7:
   built," above, for the full reasoning): code is self-describing and
   fully deterministic, with no analogous "which adapter drafted this"
   fact worth a second entry the way `intent_provider`'s own kind names
   which LLM transcribed a document. `sdkeval/provenance.go`'s
   `stampDocumentSource` builds it: `{"kind": "document", "ref":
   "payments.ts", "content_hash": "sha256:..."}`, the exact same kind the
   md medium already uses — no `docs/schema.md` amendment needed at all,
   since no new kind was introduced.
7. **Live finale** — **built, real, live, end to end.** A real
   TypeScript payments program, evaluated for real through the real Deno
   harness, resolved into a draft whose canonicalized `delta.creates[]`
   is byte-identical to a **real, freshly-run, live** `ubx propose
   --from-doc payments.md` transcript's own resolved shape — the full
   real transcript, the exact canonicalized bytes compared, and the
   honest account of what does and doesn't match are all in "Slices
   5–7: built," above. Three independent producers (a hand-written
   intent file, the md medium's real LLM transcription, and this arc's
   own typed TS program) converging on the same resolved infrastructure
   is the strongest available proof that "the SDK is a producer of
   `intent/v1`, nothing more" (UBI-33's own framing) actually holds —
   demonstrated this session, not just an aspiration stated in this
   document. **UBI-34 closed in Linear.**

## The Go evaluator: decided empirically (UBI-35 session 1)

UBI-35's own ticket framed the central open question as a hypothesis to
falsify before anything else got built: unlike a TypeScript program (needs
a sandboxed *interpreter* — Deno — because the runtime that executes it is
shared, general-purpose, and otherwise capable of anything), a Go SDK
program is *compiled* and runs as an ordinary OS process. If real
OS-level restriction of that child *process* is achievable on both target
platforms, hermeticity doesn't need a language-level permission system at
all — the same `core.DoubleRun` two-run byte-compare TS's own evaluator
already uses becomes the determinism half, and process-level sandboxing
becomes the isolation half. This was tested empirically, with the same
rigor as the Deno probes (real commands, real error strings, real gaps
named), before any runtime/codegen/CLI code was written. **The hypothesis
is confirmed, not falsified**, on both platforms, with one real portability
caveat on Linux (below).

### macOS: `sandbox-exec`, real and working

A first, naive "deny everything" profile —

```scheme
(version 1)
(deny default)
(allow process-exec)
(allow process-fork)
```

 — crashed the probe binary before it printed a single line: `exit 134`
(`SIGABRT`), no stdout, no stderr. The real crash report (macOS writes one
to `~/Library/Logs/DiagnosticReports/*.ips` for every abort — read
directly, not guessed at) pointed at `dyld4::CacheFinder` on the faulting
thread: the sandbox had denied `dyld` itself the read access it needs to
open the shared library cache, so the process never reached `main` at
all. This is the macOS-side analog of the Deno remote-import gap — a
naive profile looks like it's just "extra safe," but it's actually
blocking something load-bearing the runtime needs before your own code
ever executes.

The fix, found by reading Apple's own shipped profiles
(`/System/Library/Sandbox/Profiles/`, not guessed at): `system.sb` —
Apple's own base profile for first-party daemons — imports
`dyld-support.sb` and grants exactly the bootstrap access a process needs
to start (shared cache, `/usr/lib`, `/System`), plus a narrowly-scoped,
unix-socket-only `network-outbound` rule for `/private/var/run/syslog`
(confirmed by reading the rule directly — not a real network permission).
Importing it as a base and layering explicit denies on top is a real,
working profile:

```scheme
(version 1)
(import "system.sb")
(allow process-exec)
(allow process-fork)
(allow file-read* file-map-executable (subpath "<the evaluator's own scratch/binary dir>"))
(deny file-write*)
(deny file-read* (subpath "/Users"))
(deny file-read* (subpath "/private/etc"))
(deny file-read* (subpath "/private/var"))
(deny network-outbound)
(deny network-inbound)
```

Run for real against a compiled Go probe binary that attempts six things
a hermetic evaluator must deny — reading a home-directory file, reading
`/etc/hosts`, writing to `/tmp`, reading an env var, dialing
`8.8.8.8:53` over TCP, and resolving DNS for `example.com` — every
capability except env-var visibility came back cleanly blocked, and the
process ran to completion (`exit 0`) rather than crashing:

```text
READ_HOME_FILE: BLOCKED (open /Users/roozbeh/.zshrc: operation not permitted)
READ_ETC_HOSTS: BLOCKED (open /etc/hosts: operation not permitted)
WRITE_TMP_FILE: BLOCKED (open /tmp/ubx-go-probe-write-test: operation not permitted)
READ_ENV_HOME: ALLOWED (/Users/roozbeh)
NET_DIAL_TCP: BLOCKED (dial tcp 8.8.8.8:53: connect: operation not permitted)
DNS_LOOKUP: BLOCKED (lookup example.com: no such host)
```

**Subprocess-escape checked directly, not assumed**: an adversarial SDK
program doesn't have to make the denied syscall itself — it could shell
out to `curl` or `nc` and let a different binary try. Tested for real:
`sandbox-exec -f <profile> /usr/bin/curl ...` and `sandbox-exec -f
<profile> nc -z 8.8.8.8 53`, run directly (not even via the Go probe),
both denied identically (`nc` exits 1, no connection). macOS sandbox
profiles apply to the whole process tree by default — there is no
separate "scope it to just this binary" step needed, and no equivalent of
the Deno dynamic-import gap found on this path.

Env-var visibility is real and expected to stay allowed here —
`sandbox-exec` is a filesystem/network/Mach-IPC policy, not an
environment-variable filter. That's a separate, simpler mechanism (the
evaluator sets `exec.Cmd.Env` directly), not a sandbox-profile concern.

### Linux: namespaces (`CLONE_NEWNET`, `bubblewrap`), real and working, with a real nesting caveat

Tested for real inside a Linux container (no bare-metal Linux host
available in this session's environment — a real, honestly-recorded
scoping limit, unlike the macOS results above which ran directly on the
host). Baseline first, unrestricted: the same six-check probe, cross-
compiled for `linux/amd64`, run in a plain `alpine:3.20` container —
everything allowed, confirming the probe itself is a valid negative
control before testing any restriction.

**Network namespace isolation** (`CLONE_NEWNET` — exactly what Go's own
`syscall.SysProcAttr.Cloneflags` exposes directly, no external tool
required in principle) blocks both TCP dial and DNS cleanly:

```text
NET_DIAL_TCP: BLOCKED (dial tcp 8.8.8.8:53: connect: network is unreachable)
DNS_LOOKUP: BLOCKED (lookup example.com on 192.168.65.7:53: dial udp 192.168.65.7:53: connect: network is unreachable)
```

verified two ways: Docker's own `--network none` flag, and directly via
`unshare --net <probe>` (the same underlying kernel primitive, invoked
without Docker's own flag doing anything special) — both produced the
identical `network is unreachable` result, confirming this is the real
namespace primitive at work, not a Docker-specific behavior.

**`bubblewrap` (`bwrap`)** — the real, already-widely-packaged Linux tool
built for exactly this ("sandbox one child process": unprivileged user +
mount + net namespaces together, the same mechanism Flatpak uses) — gives
the cleanest result of the whole probe, and a structurally *stronger*
form of denial than macOS's policy-based `EPERM`: bind-mounting only
`/lib`, `/proc`, `/dev`, and the binary itself means denied paths don't
exist in the sandboxed process's view at all, so opens fail with `no such
file or directory`, not `operation not permitted` — "deny by
nonexistence," the same category `docs/sdk.md`'s own TS section named as
`isolated-vm`'s strongest property:

```text
READ_HOME_FILE: BLOCKED (open /root/.zshrc: no such file or directory)
READ_ETC_HOSTS: BLOCKED (open /etc/hosts: no such file or directory)
WRITE_TMP_FILE: BLOCKED (open /tmp/ubx-go-probe-write-test: no such file or directory)
NET_DIAL_TCP: BLOCKED (dial tcp 8.8.8.8:53: connect: network is unreachable)
DNS_LOOKUP: BLOCKED (... connection refused)
```

**The real caveat, found empirically, not assumed**: both `unshare --net`
and `bwrap` failed outright with `Operation not permitted` when run
inside this session's own (unprivileged) Docker container — creating a
*new* namespace requires either root or `CAP_SYS_ADMIN`-equivalent
privilege, which this session's default container didn't have; both
worked once re-run with `--privileged`. On a bare Linux host this is
normally a non-issue (unprivileged user namespaces are enabled by default
on most modern distros, which is exactly what lets `bwrap` run
unprivileged in the first place) — but **a Go evaluator running inside
someone else's already-hardened container** (a customer's own CI image,
a locked-down build agent) may find namespace creation denied for the
same reason found here. This is this arc's own version of the Deno
remote-import gap: a real, non-obvious edge worth documenting rather than
silently assuming "Linux always works." The evaluator must fail loudly
(refuse to run unsandboxed) rather than silently degrade when this
happens — never a silent fallback to unrestricted execution.

**Capabilities alone are not a substitute**, confirmed directly:
`--cap-drop=ALL` with an otherwise-normal network namespace still allowed
both the TCP dial and the DNS lookup. Capabilities gate privileged
operations (raw sockets, mount, `ptrace`); they don't gate an ordinary
`connect()`. This rules out "just drop capabilities" as sufficient on its
own — namespace isolation is the load-bearing mechanism.

Raw hand-authored `seccomp` (a custom BPF syscall-number allowlist,
built from scratch against the Go runtime's own syscall surface) was
evaluated but not hand-built this session: `bwrap` already layers a
seccomp filter underneath its namespace isolation as part of what it
does, so the marginal isolation value of hand-rolling one directly is
low, while the engineering cost (Go's runtime syscall surface is broad
and version-dependent, so a hand-built allowlist is a real ongoing
maintenance burden) is high. `bwrap`/namespaces solve the actual
requirement (no network, no arbitrary file access) more directly; noting
this as a scoped decision, not a silent gap.

### Plain env-scrubbing: real, but confirmed insufficient alone

Tested directly (`env -i <probe>`, no sandbox of any kind): env-var
visibility for `HOME` correctly disappeared, but file read, file write,
and network dial **all still succeeded** — env-scrubbing removes ambient
*configuration* (an `AWS_ACCESS_KEY_ID`, an `http_proxy`), it does not
touch the process's raw ability to open a file descriptor or a socket.
Confirmed, not assumed: this rules out "env-scrubbing as the honest
floor" as this arc's actual mechanism, precisely because both platforms
proved a real syscall-level mechanism *is* achievable — env-scrubbing
remains a real, cheap, additional layer (the evaluator still clears the
child's environment down to an explicit minimal set), just not the
primary one.

### The determinism story turns out simpler than TypeScript's

TS's evaluator needs two defense layers because JavaScript's
nondeterministic entry points (`Date.now()`, `Math.random()`) are
*ambient, monkey-patchable globals* — `guards.ts`'s `GuardedDate` overrides
them eagerly, with `core.DoubleRun` as the backstop for whatever the
override can't see. **Compiled Go has no equivalent ambient global to
patch**: `time.Now()` and `math/rand`'s auto-seeded top-level functions
are direct, statically-linked calls the program's own source imports
explicitly — there is no runtime-reachable hook to intercept them from
outside the program, the way a JS engine's global object can be mutated
before the program runs. This isn't a gap; it's a structural
simplification. `core.DoubleRun` doesn't need to know *why* two runs
diverged — any nondeterminism, from `time.Now()` or anywhere else,
produces different bytes across the two runs and gets caught by the same
byte-compare TS already relies on as its own backstop layer. For Go,
that backstop **is** the whole determinism story — one layer, not two.
(A `go vet`-style static lint flagging `time`/`math/rand` imports in an
SDK program is real, cheap, future defense-in-depth — not required for
soundness, since `DoubleRun` alone is already a complete guarantee.)

### Decision

Ship a Go evaluator built on: **`sandbox-exec` on macOS** (fully proven
above), **`bubblewrap` on Linux when present on `PATH`** (fully proven
above; the evaluator checks for it and fails loudly, refusing to run
unsandboxed, rather than silently degrading, if it's missing or if
namespace creation is denied) — both wrapped in `core.DoubleRun` exactly
as TS's evaluator already does, reusing `core.CanonicalJSONBytes`
unchanged. No language-level guard layer is needed or possible for Go;
`DoubleRun` alone carries the full determinism guarantee. This is a
smaller, simpler evaluator than TS's — no embedded runtime assets to
extract, no generated-runner-script indirection, no clock/random
override module — because the compiled-program "cheat" really does hold.

## The Python evaluator: decided empirically (UBI-36 session 1)

Python is the arc's own acknowledged hard case — UBI-36's own description
names it "famously miserable": no compiled-program cheat (unlike Go —
CPython is an interpreter, general-purpose and otherwise capable of
anything, the same structural problem TS's own evaluator has), and no
built-in permission model the way Deno has. Two real candidates were
probed empirically, with the same rigor as the Go session's own probes,
before either was assumed to be the answer: **subprocess restriction
applied to CPython** (the Go arc's own `sandbox-exec`/`bubblewrap`
wrappers, retargeted — expected, going in, to be the front-runner "now
that UBI-35 built the machinery") and **WASI** (CPython compiled to
WebAssembly, run under `wasmtime` — the "maturity check" candidate).
**The result reverses the expectation**: WASI won, decisively, on real
evidence — stronger isolation, a genuinely simpler cross-platform story,
and (checked, not assumed) real enough today to ship. Container-level
isolation (the ticket's own named fallback) was not built or probed
separately — everything it would have offered (namespace-level process
restriction) is what candidate 1 already tests directly, and WASI made
neither candidate necessary as the final answer.

### Candidate 1 — subprocess + `sandbox-exec`/`bubblewrap` applied to CPython: real, working, but structurally weaker

Retargeting the Go arc's own macOS profile at the real, installed
`python3` (not a compiled binary) surfaced a genuinely new failure mode
within minutes: even a maximally permissive `(allow file-read* (subpath
...))` scoped to Python's own install tree wasn't enough — `python3`
failed at startup with `realpath: ... Operation not permitted`, tracing
through several Homebrew symlink hops to compute `sys.executable`/
`sys.prefix`. The fix, found empirically (not assumed from the Go
profile, which never needed it — a statically-linked Go binary does
essentially no filesystem introspection of its own installation at
startup): `(allow file-read-metadata)`, **unscoped**, matching a real
precedent already sitting in Apple's own `bsd.sb` ("allow processes to
traverse symlinks") that went unused in the Go session because it was
never needed there. Metadata-only reads (existence, size, permissions —
never content) are conventionally allowed broadly while actual *data*
reads stay scoped; this is Python's own first real, language-specific
gap the Go probe never had to close.

With that fix, the profile works, and closes exactly what UBI-36 named:

```text
READ_ETC_HOSTS: BLOCKED (Operation not permitted)
WRITE_TMP_FILE: BLOCKED (Operation not permitted)
NET_DIAL_TCP: BLOCKED (Operation not permitted)
IMPORT_PIP: BLOCKED (No module named 'pip')
SUBPROCESS_SPAWN: ALLOWED, but inherits the same sandbox (a spawned `nc`
  denied network identically — confirmed, not assumed, the same "whole
  process tree" property the Go session found)
```

`IMPORT_PIP` blocked by construction, not by a rule naming `pip`
specifically: Homebrew keeps the stdlib (`.../Cellar/python@3.14/.../
lib/python3.14`) and site-packages (`/opt/homebrew/lib/python3.14/
site-packages`) at two genuinely different top-level paths — allowing
only the former (and explicitly denying the latter, `deny` after
`allow`, last-match-wins) closes "site-packages reach" *and* "no `pip`"
in one real, structural move: `pip` itself is a site-packages install,
never reachable from an interpreter that can't see that directory at
all. `python3 -I` (isolated mode: ignores `PYTHONPATH`, the user's own
site-packages, all `PYTHON*` env vars) closes the "`PYTHONPATH` reach"
half honestly, but *as a language flag, not a sandbox property* — a
second, independent layer this candidate needs that Go's own evaluator
never did (Go has no comparable "trust me, ignore my own environment"
runtime flag to depend on, because the sandbox alone was already
sufficient there). Real, working — but every one of the guarantees above
is a **policy decision that could be gotten wrong** (as the
`file-read-metadata` gap itself just demonstrated, live, this session),
not a structural absence of the capability.

### Candidate 2 — WASI (`wasmtime` + a prebuilt CPython-WASI build): the strongest isolation of any candidate probed across this whole arc

A real, current, version-matched prebuilt exists — not a hypothetical:
[`brettcannon/cpython-wasi-build`](https://github.com/brettcannon/cpython-wasi-build)
(a real CPython core developer's maintained release channel) ships
`v3.14.6`, byte-matching this session's own installed CPython version,
built against WASI SDK 24. CPython's own WASI support is real and
improving — [PEP 816](https://peps.python.org/pep-0816/) (accepted,
targeting 3.15) formalizes it as an officially supported Tier 2
platform, checked via a real web search this session, not assumed
current. Downloaded and run for real, via `wasmtime` (installed via
`brew install wasmtime`, itself a real, actively maintained project):

```text
$ wasmtime run python.wasm -c "print('hi')"
Could not find platform independent libraries <prefix>
Fatal Python error: Failed to import encodings module
ModuleNotFoundError: No module named 'encodings'
```

— confirms the baseline WASI posture *before* granting anything at all:
**zero ambient filesystem access of any kind**, not even enough for
Python to find its own standard library. Granting exactly two
directories (`--dir <stdlib>::/lib`, `--dir <program-dir>::/prog`) and
setting `PYTHONHOME=/lib` is enough to run real programs correctly —
`json`, `hashlib`, `dataclasses`, `typing`, `enum` (everything this
arc's own runtime needs) all work, in ~220ms including WASM startup.
Against that minimal, correctly-scoped grant, every capability the
subprocess candidate needed a policy to deny is **structurally absent**
instead:

```text
READ_ETC_HOSTS:  BLOCKED (No such file or directory) -- ENOENT, not EPERM: "/" itself has no listing
WRITE_TMP_FILE:  BLOCKED (No such file or directory) -- same
READ_ENV_HOME:   BLOCKED_OR_UNSET -- wasmtime forwards ZERO host env vars unless passed via --env
NET_DIAL_TCP:    BLOCKED (module '_socket' has no attribute 'getaddrinfo')
raw connect() to a numeric IP (no DNS involved at all): BLOCKED (OSError: [Errno 58] Not supported)
IMPORT_PIP:      BLOCKED (No module named 'pip') -- site-packages was never preopened, full stop
SUBPROCESS_SPAWN: BLOCKED ([Errno 58] wasi does not support processes.) -- not a policy, a missing WASI capability
os.listdir("/"): BLOCKED (FileNotFoundError: No such file or directory: '/') -- no root filesystem view exists at all
```

Every one of these is **deny by nonexistence** — the same category
`docs/sdk.md`'s own TS section named as `isolated-vm`'s strongest
property, but here backed by a real, working build rather than a native
addon that failed to compile under this project's own npm lockdown.
Network and subprocess-spawning in particular aren't *policy-denied*,
the way `sandbox-exec`/`bwrap` deny them — they are **capabilities that
do not exist in the WASI Preview 1 surface at all**, closing the whole
"a sandbox rule could be misconfigured" risk class structurally, for
exactly the two capabilities that matter most.

**`PYTHONPATH` reach, checked directly, not assumed closed for free**: a
first test deliberately mounted an "evil" directory *and* pointed
`PYTHONPATH` at its guest path — the import succeeded (`EVIL MODULE
LOADED`), proving WASI's own sandboxing is entirely about **which host
directories the evaluator itself chooses to preopen**, not about
`PYTHONPATH` being inert. Re-run with `PYTHONPATH` still set to the same
path but that directory **never preopened**: `ModuleNotFoundError`,
cleanly. The real, correct conclusion: `PYTHONPATH` is neutralized
structurally by the evaluator only ever preopening exactly the stdlib
and program directories — never by a language flag (unlike `-I` for
candidate 1) — so a malicious program setting it to anything is
inherently harmless as long as the evaluator's own grant stays minimal.

**Genuinely cross-platform, verified, not assumed**: the exact same
`python.wasm` artifact and the exact same `wasmtime run` invocation,
tested on this session's own macOS host and inside a real Ubuntu 24.04
Linux container (a separately-installed Linux `wasmtime` binary,
`v47.0.2`, same version) — **byte-identical probe output on both
platforms**. This is a real structural simplification over Go's own
answer: Go needed two genuinely different platform mechanisms
(`sandbox-exec` vs. `bubblewrap`, two separate source files, two
separately-verified behaviors); Python's WASI evaluator needs exactly
one, because WASM's whole premise — host-OS independence — held up
under a real test, not just its own marketing.

### PYTHONHASHSEED: the determinism trap, probed explicitly as asked

Python randomizes `str`/`bytes` hashing per-process by default (since
3.3, a hash-flooding DoS mitigation) — confirmed live, three consecutive
runs of a script building a `set` of stack/resource names produced three
different iteration orders with `PYTHONHASHSEED` unset, on both native
CPython and under WASI identically. `PYTHONHASHSEED=0` (any fixed value
works) pins it — three runs, byte-identical, again on both native and
WASI. **The precise, real scope of the trap, worth stating exactly**:
this affects `set`/`frozenset` iteration order only — a plain `dict`'s
own iteration order is insertion-order, guaranteed by the language spec
since 3.7, *not* hash-seed-dependent, and stayed stable across every run
regardless of `PYTHONHASHSEED` in this session's own tests. A program
that never builds a `set`/`frozenset` whose iteration order leaks into
output has nothing to fix here; the evaluator pins `PYTHONHASHSEED=0`
unconditionally anyway (via `wasmtime run --env PYTHONHASHSEED=0`,
`wasmtime` forwards no host env otherwise) as the cheap, always-safe
default, with `core.DoubleRun` as the backstop for this and everything
else — the exact same two-layer shape as Go's `time.Now()` finding: a
real nondeterminism source pinned where practical, caught unconditionally
where it isn't.

### Decision

Ship a Python evaluator built on **WASI via `wasmtime`**, not the
expected subprocess-sandbox front-runner. Structurally the strongest
isolation this whole three-language arc has produced (network and
subprocess-spawning absent as capabilities, not merely denied by
policy); one mechanism instead of two platform-specific ones; a real,
version-matched prebuilt CPython-WASI build available today, not a
someday. `wasmtime` is a new required external tool (`PATH` lookup,
exactly like Go's own `bwrap` requirement and TS's own `deno`
requirement — not a new category of dependency this project hasn't
already accepted twice), and the CPython-WASI build itself
(python.wasm + its own stdlib tree, ~42MB) is **acquired and cached
locally on first use**, not embedded into the `ubx` binary — the same
`provider.Acquire`-style "fetch a pinned, versioned artifact once, reuse
the local cache after" precedent this project already trusts, chosen
over embedding specifically because embedding would grow every `ubx`
install by ~42MB regardless of whether its own user ever touches Python
SDK programs at all. `PYTHONHASHSEED=0` pinned unconditionally,
`core.DoubleRun` as the backstop, exactly mirroring the discipline
Go's own `time.Now()` finding established. The subprocess-sandbox
candidate is real and documented above, not discarded lightly — it
loses on the evidence, not on a guess.

### A real implementation-time bug: a mount that *looked* like it worked, but wasn't tested for what it claimed

Building `pyeval` (the Go-side harness) surfaced a genuine "verify, don't
assume it worked because the output looked right" lesson, worth
recording with the same honesty as the probe findings themselves. The
runtime source (`ubx_sdk`) needs to be preopened into the sandbox at a
fixed guest path so a program's own `import ubx_sdk` resolves — the
first attempt mounted it two guest path segments deep
(`--dir <host>/ubx_sdk::/ubxsdk/ubx_sdk`, no separate preopen for
`/ubxsdk` itself) and a manual smoke test of exactly that shape appeared
to pass: the program printed a correct `intent/v1` document. It was
wrong. `os.listdir("/ubxsdk")` inside the sandbox raised
`FileNotFoundError` — the nested guest path was never actually
independently listable — and the "passing" smoke test had been finding
`ubx_sdk` by accident, via the test script's OWN directory (also
preopened, and which happened to also contain a copy of the package for
unrelated reasons), not via the intended mount at all. The real fix,
found by checking `ubx_sdk.__file__` explicitly rather than trusting
that non-error output meant success: **preopen at exactly one guest path
segment per real directory tree** — mount the runtime source's own
parent directory directly at `/ubxsdk` (a single top-level preopen), not
nested under a second, ungranted parent segment. Once fixed, `pyeval`'s
own real Go test suite (which checks `ubx_sdk.__file__`/output content,
not just exit code) catches this class of regression directly. The
larger lesson, consistent with this whole arc's own standing discipline
(the Deno read-permission probe, the Go `dyld` crash, the
`file-read-metadata` gap earlier in this very session): a sandboxed
program producing plausible-looking output is not proof a mount/grant
did what it was intended to do — assert on what actually resolved, not
just whether something ran without an error.

## Out of scope for v1, named so it isn't assumed covered

A full Linux mount-namespace filesystem jail built from scratch (this
session's Linux answer relies on `bubblewrap`, an existing, already-
hardened tool, rather than hand-rolling `pivot_root`/mount-namespace code
directly — a deliberate, smaller build, not an oversight); typed
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
`@ubx/sdk` to npm, `ubx-sdk-go` to a real Go module proxy, or `ubx_sdk`
to PyPI for real (mentioned in each language's own scope, no release
plumbing designed for any of the three); extracting `stampDocumentSource`/
`validateIntentShape` into one shared package instead of three small,
duplicated copies (`sdkeval`, `goeval`, `pyeval` each carry their own —
a real, deliberate "rule of three" deferral, not an oversight, now that
all three copies exist).

## Amendment (2026-08-03, UBI-96): nested-block type names weren't globally unique at full-provider scale — root cause, fix, and a second, separate scale limit found along the way

**P1, founder-found live**: the first real `ubx sdk gen --lang go` run
against a FULL provider (hashicorp/aws@6.54.0, 1,682 types — every
earlier codegen session, including this doc's own examples above, only
ever exercised `aws_db_instance` alone) failed to `go build`, ~10+ "X
redeclared in this block" errors.

**Root cause, confirmed by live schema inspection, not assumed**: every
nested-block-derived type name (`sdk/codegen/templates/go`'s own
`goFieldMeta`, mirrored identically in the `ts`/`py` templates) was
derived as `pathPrefix + fieldPascal` — collision-free *within* one
resource's own render call, but nothing distinguished that from another
resource's own bare `pascalCase(wireType)` name, or another resource's
own nested tree, once every resource shares one flat package/module. AWS's
own convention of splitting a legacy inline nested block out into its
own standalone resource (`aws_s3_bucket`'s `logging` block vs. the
separate `aws_s3_bucket_logging` resource; `aws_autoscaling_group`'s
`tag` block vs. `aws_autoscaling_group_tag`; `aws_wafv2_web_acl`'s
recursive `rule.statement...` tree vs. `aws_wafv2_web_acl_rule`'s own
copy of the identical tree; and more) means the two independently-
derived names are the SAME STRING by construction, not by accident.
Verified exhaustively against the real provider: **6,278 colliding
names**, **100% of them within a single AWS service** (a "namespace by
service package" restructure — the direction `UBI-98` was independently
considering — would NOT have fixed this; verified with a direct
same-service-vs-cross-service classification over every collision, zero
were cross-service).

**Fix**: every nested-block name now joins `pathPrefix` and `fieldPascal`
with `"_"` (`sdk/codegen/templates/go/ts/py`'s own `resourceRenderer`,
each package's doc comment has the full uniqueness proof) — `pascalCase`
never emits an underscore, so a nested name can never equal any bare
resource-level name, and two different resources' own nested trees can
never collide either (the substring up to the first inserted `_` is
always that resource's own distinct pascal name). Verified against the
full real schema: 0 collisions across all 72,960 names this scheme
produces. Applied identically to Go, TypeScript, and Python — Go fails
this class of bug as a hard `go build` redeclaration; TypeScript can fail
SILENTLY instead (interface declaration merging, when two colliding
shapes happen to be compatible); Python has no error at all (a later
`class`/module-level assignment silently overwrites an earlier one) —
all three were real, checked directly, not assumed safe by
extrapolation from Go's own failure.

**Defense in depth**: each template package now exports
`CheckNoDuplicateDeclarations(src)` (Go: real `go/parser` AST walk;
TS/Python: a regex scan matched to each language's own known declaration
shapes and namespace rules), wired into `ubx sdk gen`'s own production
path (`cli/sdk.go`) for all three languages — generation now refuses to
write a file with a real collision, rather than only ever catching this
in a test after the fact.

**A second, separate, previously-undiscovered problem found while
verifying the fix at true full-provider scale**: even with zero
redeclarations, `go build` on the real, full 1,682-type/~40MB/~73,000-
package-level-declaration output still fails — a genuine Go compiler
crash (`internal compiler error: NewBulk too big`), reproduced
independently via the real `ubx sdk gen --lang go` CLI path (not just a
test harness). Confirmed scale-dependent, not a fluke: a synthetic half-
size split (~840 types, ~20MB) still crashes, at a smaller internal
threshold; a real single-service-sized subset (AWS's own largest real
service, `ec2`, 56 types/~74KB) builds clean and instantly. This is a
hard Go-toolchain ceiling on how much can live in ONE package, entirely
independent of naming — it is exactly the kind of problem `UBI-98`'s own
per-service-package restructure would fix (verified directly above), for
a reason that ticket never named (it argued per-service packaging for
reviewability and, incorrectly per this amendment, for the naming
collision — not "the whole provider can never compile as a single
package no matter what the names are"). Not fixed in this session
(out of scope for UBI-96's own diagnosed root cause); tracked as a
comment on the UBI-98 Linear thread rather than left undiscovered again.
`sdk/codegen/templates/go/fullprovider_live_test.go`'s own permanent,
`UBX_CONFORMANCE_LIVE=1`-gated CI check asserts zero redeclarations hard
(the actual UBI-96 regression class) and treats this specific, separate
compiler-crash signature as a named, non-blocking skip rather than either
silently passing or permanently red-flagging the whole check over an
unrelated, already-tracked issue.

## Amendment (2026-08-03, UBI-98): Go bindings restructured to a per-provider repo, per-AWS-service package layout — fixes the NewBulk crash for real; TypeScript and Python left unrestructured

**Scope**: `--lang go` only. TS/Python still write one flat file per
provider source, exactly as before this session — restructuring them the
same way is real, separately-scoped follow-up work, explicitly not done
this session (STATE.md; do not assume TS/Python were left silently
inconsistent, this is a deliberate, named boundary).

**The confirmed hard blocker, verified reproduced before anything was
built**: UBI-96's own closing comment reported a genuine Go compiler
crash ("internal compiler error: NewBulk too big") compiling the full
1,682-type provider as one flat package/file, unrelated to naming.
Re-ran `TestFullProvider_Go_CompilesClean` against the current,
post-UBI-96-fix generated output before starting any design work — it
reproduced identically, confirming a real, still-open blocker, not a
stale or already-fixed hypothesis.

**`ubx sdk gen --lang go`'s own `--out` semantics changed**: writes a
repo-shaped tree per declared provider source now, not one flat file --
`<out>/<source-sanitized>/` (e.g. `hashicorp-aws/`), with its own `go.mod`
stub (`module github.com/ubiquex/ubx-sdk-<shortName>`, e.g.
`ubx-sdk-aws` for `hashicorp/aws` -- the founder's own worked example on
the ticket, `shortName` derived mechanically as the source's own last
`/`-segment, never a hand-curated friendlier rename like `google` ->
`gcp`, since this session only generates against and verifies against
the real `hashicorp/aws` provider), one Go package per derived
AWS-service boundary (`iam/`, `ecr/`, `sqs/`, ...), one file per resource
type within its own service package. `--lang ts`/`--lang py` are
UNCHANGED.

**Naming scheme within a service package, per the founder's own locked
comment on UBI-98**: the redundant `Aws<Service>` prefix is dropped from
every generated type name, since the import path already encodes
provider+service --

```
generated.AwsEcrRepository        -> ecr.Repository
generated.AwsEcrRepositoryConfig  -> ecr.RepositoryConfig
```

`WireType` inside the runtime `ResourceBinding` literal still carries the
REAL, full, unshortened wire type string (`"aws_ecr_repository"`) --
only the Go identifier drops the prefix, never the wire-protocol value a
real provider actually reads.

**Service-boundary derivation, verified against the real 1,682-type
schema before being trusted, not assumed clean** (the ticket's own
explicit ask): a wire type's own token structure --
`<provider>_<service>_<local...>` -- splits into `(service, local)`
mechanically (`sdk/codegen/ir.ServiceAndLocalName`), deliberately with NO
external, network-fetched AWS-service-taxonomy lookup (`ubx sdk gen` must
stay 100% local/offline). Checked exhaustively, not spot-checked: **zero
`(service, local)` collisions across all 1,682 real wire types** -- this
derivation is genuinely unambiguous, mechanically.

It is, however, confirmed NOT a faithful reproduction of AWS's own real
service taxonomy the way a hand-curated table (Pulumi's own bridge
metadata, for one real example) would be -- named explicitly rather than
silently passed off as taxonomy-accurate: 11 real wire types are bare
two-token names (`aws_vpc`, `aws_instance`, `aws_route`, `aws_subnet`,
...) with no third token to serve as a local name at all (handled: the
local name falls back to the service token itself); and roughly 130 of
the 1,682 types, dominated by AWS's own EC2/VPC "core" resource family
(`aws_vpc`, `aws_subnet`, `aws_instance`, `aws_security_group`,
`aws_route_table`, `aws_internet_gateway`, `aws_eip`, and more), carry
NO explicit service token in their wire name at all -- Terraform's own
AWS provider predates its later services' `aws_<service>_*` convention
for exactly this resource family -- so this mechanical split fragments
them into many small, sometimes idiosyncratically-named single/few-type
packages (`key` for `aws_key_pair` alone, `flow` for `aws_flow_log`
alone, `main` for `aws_main_route_table_association` alone) rather than
one curated `ec2` package. This does not block either of this
restructure's two hard requirements (a full-provider `go build`
compiling clean -- MORE, smaller packages only helps that; the founder's
own locked naming scheme, which holds regardless of where the boundary
lines fall) -- a hand-curated wire-type -> canonical-AWS-service
exception table remains real, separately-scopable follow-up work, tracked
here rather than silently shipped as if it were taxonomy-accurate.

**Two real, live-verified Go-identifier edge cases in the derived service
name itself**, found running the real full-provider generation, not
anticipated by the ticket: `aws_default_vpc` and five sibling real types
derive service `"default"` -- a Go keyword, `package default` is a
syntax error. `aws_main_route_table_association` derives service
`"main"` -- not a keyword, but special to the `go` tool itself (a package
literally named `main` is treated as a command requiring `func main()`,
so `go build ./...` failed with "function main is undeclared in the main
package"). Both resolved with a trailing underscore
(`sdk/codegen/templates/go`'s own `goPackageIdent`), the same convention
this package's own `pythonIdentifier` already established for a wire
name colliding with a Python keyword. `internal`/`vendor`/`testdata` are
guarded the same way defensively (real, well-known go-tool-special
directory names) even though none occur in `hashicorp/aws@6.54.0`'s own
real schema -- not verified against a real occurrence this session,
unlike `default`/`main`, named as such rather than presented as
equally-confirmed.

**A third, separate, previously-undiscovered problem, found verifying
THIS restructure at true full-provider scale, not anticipated by UBI-98's
own ticket text (which only ever discussed aggregate package size)**:
even with per-service packages, `wafv2` and `quicksight` STILL crashed
the Go compiler identically, despite being small by resource-type count
(15 and 25 types respectively) -- because AWS's own recursive schema
shapes (`aws_wafv2_web_acl_rule`'s own "statement"-inside-"statement"
tree, the real, extreme case) physically repeat an IDENTICAL nested block
shape at every depth level a real provider schema can express recursion
to at all (there is no true self-reference in a real tfplugin schema;
"recursion" is always statically unrolled to some fixed depth, each level
re-declaring the literal same block verbatim). `aws_wafv2_web_acl_rule`
ALONE rendered to over 10MB / ~250,000 lines before a fix -- enough on
its own, independent of any sibling type sharing its package, to
reproduce the identical Go compiler crash, a problem no amount of
per-service-package splitting could ever fix by itself.

Fixed in two halves, verified separately (the first alone was NOT
enough, confirmed live, not assumed): (1) nested Go struct declarations
(cosmetic documentation only -- confirmed by reading `sdk/go/runtime`'s
own `serializeConfig` before relying on this, which walks a Config value
purely by Go struct FIELD NAME via reflection, never by a nested value's
own declared TYPE NAME) now dedupe by a canonical STRUCTURAL signature of
the shape, not just its derived path -- two positions in the tree with
the identical shape share one declaration. Deduplicating this alone
shrank `aws_wafv2_web_acl_rule` from >10MB to ~6.5MB, but the real
full-provider build still crashed identically -- (2) the runtime
`sdk.FieldMap{...}` literal `ResourceBinding.Fields` is actually built
from (NOT cosmetic -- this is what the runtime reads) was still being
re-inlined in full at every depth with no dedup at all, and THAT, not the
struct declarations, was what was actually driving the crash. Fixed
identically in spirit: a repeated shape's `sdk.FieldMap{...}` literal is
now hoisted into one shared top-level `var` the first time it's seen,
and every later occurrence of the identical shape references that var
instead of re-inlining -- `aws_wafv2_web_acl_rule` dropped to ~69KB
(~150x smaller than the original unfixed output), confirmed by direct
measurement, not inferred from the compiler no longer crashing alone.

**Required verification, run for real, exactly as specified**: `ubx sdk
gen --lang go` against the REAL full `hashicorp/aws@6.54.0` provider
(1,682 types) produces a repo-shaped tree (1,941 files across 258 service
packages) where `go build ./...` compiles every package clean --
confirmed twice, independently: (1) `sdk/codegen/templates/go/fullprovider_live_test.go`'s
own `TestFullProvider_Go_CompilesClean`, rewritten this session to be a
HARD pass/fail (the UBI-96-era `NewBulk`-crash named skip is gone --
per-service packages, plus the shape-dedup fixes above, mean no known
crash remains to carve out); (2) independently, outside the test harness
entirely, via the real built `ubx` binary: `ubx sdk gen --lang go --out
sdk/generated` against a real `.ubx/config` pinning `hashicorp/aws@6.54.0`,
followed by a real `go build ./...` against the actual on-disk output.
Both passed clean. This is now a permanent, always-available (behind
`UBX_CONFORMANCE_LIVE=1`) hermetic/CI check, per the ticket's own
explicit requirement.

**Conformance fixtures updated**: `sdk/conformance/programs/go/generated/`
is now `hashicorp-aws/` (a nested repo-shaped module, `db/` package --
`aws_db_instance`'s own derived service is `"db"`, not `"rds"`, since the
derivation is wire-name-mechanical, not AWS's own product-family naming)
with its own `go.mod`; `sdk/conformance/programs/go/go.mod` gained a
`require`+`replace` for it (a nested `go.mod` is a real module boundary
Go enforces, so importing it needs the same treatment as
`github.com/ubiquex/ubx-sdk-go`'s own existing replace); `payments.go`'s
import/usage updated to `db.Instance`/`db.InstanceConfig`. Only the
`payments.go` file's own content — and therefore its
`intent.sources[].content_hash` in `golden/payments_go.json` — changed;
the resolved `resources`/`stack`/`intent.summary` are byte-identical,
re-verified via the real `goeval` evaluator.

**Not done this session, named explicitly, not silently**: TypeScript
and Python codegen are unrestructured (still one flat file per provider
source) -- UBI-98's own scope item 5 explicitly permits this ("do TS/
Python as a following session's scope" if Go alone doesn't leave room),
and this session's own diagnosis work (the service-derivation-ambiguity
finding, the two Go-identifier edge cases, and especially the recursive-
shape blowup) took the whole session on its own. A hand-curated
wire-type -> canonical-AWS-service exception table (closing the
taxonomy-accuracy gap named above) is also real, separately-scopable
follow-up work.

## Amendment (2026-08-04, UBI-98 session 2): TypeScript and Python restructured the same way — the Go compiler crash does NOT reproduce in either, confirmed not assumed; the recursive-shape dedup fix still applied; a new cross-language `--out` collision found and fixed

**Scope**: `--lang ts` and `--lang py`, completing UBI-98 (the prior
session's own amendment above did Go only, explicitly deferring TS/
Python). Both now write the identical repo-shaped tree layout Go does:
own manifest stub (`package.json` for TS, `pyproject.toml` for Python),
one directory per derived AWS-service boundary, one file per resource
type, the `Aws<Service>` prefix dropped identically.

**The instruction's own explicit ask, done first, not assumed**: before
any design work, checked whether Go's own `NewBulk too big` compiler
crash-class problem (a single deeply-recursive resource type's own
generated output growing pathologically large, independent of
package/directory boundaries) also affects TS's `deno check` or a real
Python `import`. It does NOT, confirmed empirically, not inferred from
Go's own finding: a synthetic single-type reproduction of the exact real
`aws_wafv2_web_acl_rule` shape that broke Go rendered to 16.7MB/
~253,000 lines of naively-unrolled TypeScript (type-checked clean in
~2 seconds) and 21.2MB/~258,000 lines/21,026 dataclasses of Python
(imported clean in ~4.4 seconds) — neither `deno check` nor CPython's
own class/dataclass machinery has anything resembling Go's own internal
compiler limit. The structural shape-deduplication fix (both halves --
the cosmetic nested-declaration dedup AND the load-bearing runtime
FieldMap-literal-hoisting dedup, ported identically from Go) was applied
to TS and Python anyway, not because either would otherwise crash, but
because a single 16–21MB generated file is still a real reviewability/
git-diff/repo-bloat problem on its own terms (docs/sdk.md's own
"committed to git like any other reviewable generated code" design
goal) — `aws_wafv2_web_acl_rule` dropped to a small fraction of its
original size in both languages after the fix, mirroring Go's own
~150x reduction, confirmed by direct measurement, not assumed to follow
from the Go result.

**A real, load-bearing structural difference from Go, confirmed not
assumed**: ES modules (TypeScript) and Python modules both give every
FILE its own independent namespace — unlike Go, where UBI-96's original
cross-resource collision bug required the package-wide `"_"` join fix to
avoid a hard `go build` redeclaration, moving to one file per resource
type makes cross-resource collision structurally IMPOSSIBLE for TS and
Python regardless of directory grouping or naming, the moment each type
gets its own file. The per-service-DIRECTORY grouping is therefore, for
TS and Python, purely an organizational/reviewability choice matching
the founder's own Pulumi-parity naming goal — not a compile-correctness
requirement the way Go's package split was. `CheckNoDuplicateDeclarations`
(both languages) now checks ONE generated file's own declarations (the
still-real within-one-resource nested-shape collision risk the `"_"`
join guards against); `CheckRepoNoDuplicateDeclarations` runs that check
independently per file across a whole repo tree, never treating same-
named declarations in different files as a collision, confirmed by a
dedicated test in each language.

**Two real, live-verified per-language identifier edge cases, neither
assumed to transfer from Go's own findings**: checked directly, not
extrapolated. TypeScript needs NO service/local-name escaping at all,
confirmed live against the real full 258-service schema — ES module
resolution is purely string-based (a directory literally named
`default/`, `main/`, or `lambda/` is completely unremarkable to Deno).
Python, by contrast, has a REAL, distinct keyword collision Go never
hit: `lambda` is both a genuine Python keyword and a genuine AWS service
(`aws_lambda_*`, 20 real types) — `import lambda.function` is a
`SyntaxError`. Resolved with the same trailing-underscore convention
`pythonIdentifier` already established for a keyword-colliding FIELD
name, now also applied to service/local MODULE names (`pyModuleIdent`,
new) — `lambda_/function.py`. A leading-digit guard was also added to
`pyModuleIdent` defensively (a real, universal Python grammar
constraint), though — checked explicitly, not left unstated — no real
wire-derived name in `hashicorp/aws@6.54.0`'s own schema actually starts
with a digit.

**A third, separate problem, found only by testing all three languages
against the SAME `--out` end to end, not anticipated by either this or
the prior session's own scope**: `--out` defaults IDENTICALLY across
`--lang go/ts/py`. Under the OLD flat-file scheme this was harmless — a
single file's own extension naturally disambiguated
`hashicorp-aws.go`/`hashicorp-aws.ts`/`hashicorp_aws.py` sharing one
directory. A repo-shaped TREE has no such built-in per-language
distinction at its top level: generating all three languages against the
same `--out` (the obvious thing to do if you want all three, and this
session's own end-to-end verification did exactly that) interleaved
three different ecosystems' manifests (`go.mod`/`package.json`/
`pyproject.toml`) and source trees into ONE directory — not silent data
loss (no two languages ever emit an identically-named file), but a real
break of the "ready to become its own real repo" promise this whole
restructure exists to keep. Fixed by making `--lang` its own path
segment: `<out>/<lang>/<source-sanitized>/`, never folded into the
source-sanitized directory name. `TestSDKGen_MultipleLanguagesSameOut_DoNotCollide`
(new, cli/sdk_test.go) generates all three languages against one shared
`--out` and verifies each survives independently, real-compiling/
-checking/-importing all three.

**Required verification, met for both languages, twice each, matching
Go's own precedent exactly**: `ubx sdk gen --lang ts` and `--lang py`
against the REAL full `hashicorp/aws@6.54.0` provider (1,682 types) each
produce a repo-shaped tree (1,941 files, 258 service directories) that
type-checks/imports clean in full — (1) `TestFullProvider_TS_ChecksClean`
/`TestFullProvider_Py_ImportsClean` (new, permanent,
`UBX_CONFORMANCE_LIVE=1`-gated, mirroring Go's own
`TestFullProvider_Go_CompilesClean` exactly); (2) independently, via the
real built `ubx` binary, generating all three languages against one
shared `.ubx/config` pinning `hashicorp/aws@6.54.0` and one shared
`--out`, then real `deno check --no-remote`/a real recursive Python
`importlib.import_module` walk over the actual on-disk output for each.
All passed clean; TS in ~1.5s, Python importing all 1,940 real modules
clean.

**Verification**: `go test ./...` (whole repo) green. `gofmt -l .`
clean. `go vet ./...` clean. `sdk/conformance/programs/{ts,py}/generated/`
regenerated to the new shape. TS: `generated/hashicorp-aws/db/{doc.ts,
instance.ts}` plus `package.json`, matching the real CLI output shape
exactly (no obstacle to doing so, confirmed above); `payments.ts` updated
to import `Instance` from the new nested path. Python: `generated/db/`
directly (own `__init__.py` + `instance.py`) — deliberately NOT nested
under a `generated/hashicorp-aws/` intermediate directory the way TS/Go
are, for a real, load-bearing reason named explicitly in `payments.py`'s
own updated comment: Python's dotted `import` syntax cannot traverse a
hyphenated path segment at all, so a literal copy of the real CLI's own
nested shape would not be importable via `from generated.hashicorp-aws.db.instance
import ...` (a `SyntaxError`) — this is a genuine, documented deviation
from what `ubx sdk gen` itself produces, consistent with this fixture's
own pre-existing, already-established "filtered/hand-fitted to this one
case" posture (its own prior comment: "filtered to aws_db_instance only,
unlike a real `ubx sdk gen` run"), not an inconsistency. `payments.py`
updated to `Instance`/`InstanceConfig` from `generated.db.instance`. Only
each entry file's own content (and therefore its
`intent.sources[].content_hash` in `golden/payments.json`/
`golden/payments_py.json`) changed; resolved `resources`/`stack`/
`intent.summary` byte-identical in both, re-verified via the real
`tseval`/`pyeval` evaluators; the Go golden case, untouched this session,
still passes.

**UBI-98 is now fully closed across all three languages.** Remaining,
real, separately-scopable follow-up work, named across both sessions'
amendments: a hand-curated wire-type → canonical-AWS-service exception
table (closing the taxonomy-accuracy gap the first amendment named,
e.g. grouping the ~130 unprefixed EC2/VPC-family types under one real
`ec2` boundary the way a hand-maintained bridge like Pulumi's own would,
rather than the many small mechanically-derived packages this session's
own wire-name-only, fully-offline derivation produces).

## Amendment (2026-08-03, UBI-99): `ubx-sdk-aws-go` is now a real, separately hosted and automated repo, not just locally-generated `ubx sdk gen` output

**Founder decision, per the ticket**: the "auto-detect a provider version
bump, regenerate, open a PR" automation lives INSIDE each per-provider
bindings repo (UBI-98's repo-shaped output), not in `ubiquex` itself and
not as a generic user-facing guide only.

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-aws-go** (public),
seeded with a real `ubx sdk gen --lang go --out .` against
`hashicorp/aws@6.54.0`. The provider version this repo was last
generated from is tracked in a `VERSION` file at the repo root, not
`.ubx/config.hcl` — this repo carries no ubx stack/config of its own,
only generated bindings.

**A real, load-bearing dependency this ticket's own scope didn't name,
found rather than assumed**: every generated file's `go.mod` already
hard-requires `github.com/ubiquex/ubx-sdk-go` (UBI-98's own
`sdk/codegen/templates/go/go.go` bakes this in) — but that module had
never been published anywhere; `TestFullProvider_Go_CompilesClean` only
ever built against it via a test-only `replace` directive to the local
`sdk/go/` tree. A standalone `ubx-sdk-aws-go` repo, built with no such
replace available, cannot `go build` at all without a real
`github.com/ubiquex/ubx-sdk-go` to resolve. Fixed by publishing exactly
that already-isolated nested module (`sdk/go/`, unchanged) as its own
real repo: **https://github.com/Ubiquex/ubx-sdk-go** (public), tagged
`v0.0.0` to match the pinned require version. Verified for real, not
assumed: a clean `go mod tidy && go build && go vet` of `ubx-sdk-aws-go`
against the real public Go module proxy (`proxy.golang.org`), zero
credentials, zero `replace` directive.

**Repo naming, and a real second-order bug it caused**: founder chose
`ubx-sdk-aws-go` (not `ubx-sdk-aws`) to leave room for future
`ubx-sdk-aws-ts`/`ubx-sdk-aws-py` siblings — one repo per (provider,
language) pair, not one repo per provider containing all three
languages. Since `ubx sdk gen`'s `go.mod` module name is derived
mechanically from the provider source alone (`hashicorp/aws` →
`ubx-sdk-aws`, per UBI-98's own `shortName` derivation — never a
hand-curated rename), it never matches this naming scheme by
construction. The workflow (below) excludes `go.mod`/`go.sum` from its
regeneration diff for exactly this reason — regenerating them in place
every run would silently revert the module path back to the mechanical
name.

**The version-watch workflow itself**
(`.github/workflows/version-watch.yml` in `ubx-sdk-aws-go`): weekly
(Monday 06:00 UTC) + `workflow_dispatch`. Queries the confirmed-real
Terraform Registry API
(`https://registry.terraform.io/v1/providers/hashicorp/aws/versions`,
no auth), compares the latest version against `VERSION` via `sort -V`,
and on a newer version: builds `ubx` from `ubiquex`'s own `main` HEAD
(decided explicitly, not defaulted into — see below), re-runs
`ubx sdk gen --lang go`, sanity-builds the regenerated tree before
committing anything, and opens a PR in `ubx-sdk-aws-go` itself (never
auto-merges).

**Item 6, decided as the ticket asked**: `ubx` is built from source
every run, checking out `Ubiquex/ubiquex`'s own `main` branch — not a
tagged release. Checked, not assumed: the two existing release tags
(`v0.1.0`, `v0.2.0`) are 177+ commits stale and predate UBI-98 entirely
— pinning to either would regenerate the OLD flat-file layout, not this
repo's own real shape. There's no release cadence yet that tracks
feature landings, so source-from-main is the only way to get
`ubx sdk gen`'s current, correct behavior; accepted tradeoff for v1
(an `ubiquex` regression on `main` could affect this workflow) until a
real release process exists.

**Cross-repo auth**: `ubiquex` is private, so checking it out from
`ubx-sdk-aws-go`'s own workflow needs a credential beyond the default
`GITHUB_TOKEN` (repo-scoped only). A fine-grained PAT
(`UBIQUEX_SOURCE_TOKEN`, `contents: read` only, scoped to `Ubiquex/
ubiquex` alone) stored as an Actions secret on `ubx-sdk-aws-go`. Item 4
(no cloud credentials needed) holds — confirmed in a real run, not just
by the CLI's own `--help` text: the job never sets any `AWS_*`/cloud
credential anywhere.

**Item 5, the known limitation, held as-is per the ticket**: `sort -V`
on semver strings isn't bulletproof against pre-release tags —
commented in the workflow at the exact call site, not hardened further.

**A real bug found and fixed live, not caught by `actionlint` (which
only checks YAML/expression syntax, not runtime behavior)**: the first
real dispatch failed with `rsync ... file has vanished` (exit 24).
Root cause: the workflow originally generated into `.gen/` *inside* the
repo checkout, so `rsync --delete`'s destination scan treated its own
still-being-read source directory as extraneous destination content and
deleted it mid-transfer — a source/destination overlap, not a registry
or codegen bug. Fixed by moving every scratch path (the `ubiquex`
clone, the built `ubx` binary, the generation output) under
`$RUNNER_TEMP`, structurally outside `$GITHUB_WORKSPACE`. A second,
smaller bug (`go build -C` must be the first flag, not `-o` before it)
surfaced next and was fixed the same way — live, in a real run, not
guessed at.

**Required verification, met for real, not simulated**: a real
`workflow_dispatch` run
(https://github.com/Ubiquex/ubx-sdk-aws-go/actions) queried the real
registry (found `6.57.1`, newer than the seeded `6.54.0`), built `ubx`
from real `ubiquex` source, regenerated for real (1682 → 1687 resource
types), sanity-built the result, and opened a real PR:
**https://github.com/Ubiquex/ubx-sdk-aws-go/pull/1** — 284 files
changed, new files landing under genuinely new AWS service
directories (`bedrock/`, `mailmanager/`, ...), `main`'s own `VERSION`
left untouched at `6.54.0` (confirmed after the run — never
auto-merged).

**A repo-provisioning surprise worth recording, not a codegen finding**:
creating `ubx-sdk-aws-go` for real needed one org-level GitHub policy
change beyond anything `ubx` or this workflow controls —
"Allow GitHub Actions to create and approve pull requests" is an
org-wide setting (`Ubiquex` org, Settings → Actions) that overrides any
per-repo attempt to enable it; `gh pr create` fails outright
(`GitHub Actions is not permitted to create or approve pull requests`)
until the org owner enables it. One-time, not something this workflow
or a future sibling repo (`ubx-sdk-gcp`, etc.) needs to repeat.

## Amendment (2026-08-03, UBI-104): `ubx-sdk-aws-ts` ported UBI-99's exact pattern, first real proof the pattern generalizes — and a genuinely different answer on runtime publishing

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-aws-ts** (public),
seeded via a real `ubx sdk gen --lang ts --out .` against the same
`hashicorp/aws@6.54.0` `ubx-sdk-aws-go` was seeded from (cross-language
consistency — both per-provider sibling repos start from the same
provider snapshot).

**Item 2's own question, answered — genuinely different from Go, not
identical**: Go needed `ubx-sdk-go` published as a real module because
`go build`/`go get` resolve import paths via real VCS/proxy fetch, with
no local override outside a test-only `replace`. TypeScript/Deno
resolves the bare specifier `@ubx/sdk` a completely different way — an
explicit `deno.json` **import map**, not automatic registry resolution.
The *original* pre-UBI-98 design intent (this doc's own session-1 text)
already called for publishing `@ubx/sdk` to npm; that was never
executed. Checked for real, not assumed: `npm view @ubx/sdk` → 404 (no
such package), and this machine has zero npm auth at all (`npm whoami`
→ not logged in). Publishing needs a real npm account/org owning the
`@ubx` scope — asked the founder directly rather than guessing;
decision: **vendor a local copy for now**, not publish to npm this
session. `sdk/ts/runtime/src/index.ts` (self-contained, zero imports)
copied into `ubx-sdk-aws-ts/vendor/ubx-sdk-runtime/index.ts`, with
`deno.json`'s import map pointing `@ubx/sdk` there. Verified for real:
`deno check --no-remote` across all 1940 generated files, zero errors,
zero network access. Documented as a deliberate, known stopgap (this
repo's own README) — the npm-publish gap named in this doc's own
"Out of scope for v1" section remains genuinely open, now for a second
language too.

**A real consequence of vendoring, handled the same way Go's
`go.mod`/`go.sum` mismatch was**: `ubx sdk gen`'s own output never
touches `deno.json` or `vendor/` (neither is ever codegen'd — both are
hand-maintained), so both are excluded from the version-watch
workflow's regeneration `rsync --delete`, the same way Go excluded
`go.mod`/`go.sum` to stop a mechanical shortName mismatch from
silently reverting the module path every run. Missing this exclusion
would have silently deleted the vendored runtime and the import map on
the very first real regeneration.

**Items 3–4, ported and re-verified live, not just copy-pasted and
assumed to still work**: the identical `$RUNNER_TEMP` scratch-isolation
pattern (the fix for UBI-99's own `rsync ... file has vanished`
overlap bug), the identical `go build -C <dir> -o <out>` flag order
(the fix for UBI-99's own flag-ordering bug) — both carried over
correctly on the FIRST real dispatched run this session, no repeat of
either bug. The sanity-check step is genuinely different, not a
find-and-replace of Go's: `deno check --no-remote` (matching this
project's own real TS tooling, `sdk/ts/`'s existing `deno.json` tasks —
not `tsc`, which this project doesn't use), needing `denoland/setup-deno`
added as a new step (Go's `actions/setup-go` has no TS equivalent).
Caught before it could fail in CI: `denoland/setup-deno@v2` doesn't
exist as a real tag (only fully-qualified versions like `v2.0.5` do,
confirmed via the real GitHub API before relying on it) — pinned to
the real `v2.0.5` tag instead.

**Item 5, confirmed clean, no new org-policy work needed**: the
`Ubiquex` org's "Allow GitHub Actions to create and approve pull
requests" setting (enabled org-wide during UBI-99) already applied to
this brand-new repo automatically — confirmed via a real API check
(`can_approve_pull_request_reviews: true`) before dispatching, not
assumed. Only a per-repo `UBIQUEX_SOURCE_TOKEN` secret (same PAT value,
already scoped to `Ubiquex/ubiquex` alone, not repo-specific) needed
re-setting — secrets don't carry across repos automatically the way
the org policy does.

**Required verification, met for real on the first dispatched run,
no repeat bugs**: a real `workflow_dispatch` run
(https://github.com/Ubiquex/ubx-sdk-aws-ts/actions) queried the real
registry, found `6.57.1` newer than the seeded `6.54.0`, built `ubx`
from real `ubiquex` source, regenerated for real (1682 → 1687 resource
types), typechecked the result with zero errors, and opened a real PR:
**https://github.com/Ubiquex/ubx-sdk-aws-ts/pull/1** — 287 files
changed, genuinely new AWS-service directories (`bedrock/`,
`mailmanager/`, ...) landing as new files, `vendor/`/`deno.json`
untouched in the diff, `main`'s own `VERSION` left at `6.54.0`
afterward (never auto-merged).

## Amendment (2026-08-03, UBI-105): `ubx-sdk-aws-py` closes all three languages on UBI-99's pattern — same runtime-publishing gap as TS, a genuinely simpler resolution mechanism, zero repeat bugs on the first real run

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-aws-py** (public),
seeded via a real `ubx sdk gen --lang py --out .` against the same
`hashicorp/aws@6.54.0` snapshot both `ubx-sdk-aws-go` and
`ubx-sdk-aws-ts` were seeded from.

**Item 2's own question, answered — a THIRD distinct resolution
mechanism, not identical to either prior language**: traced the real
import mechanism before assuming anything (`sdk/codegen/templates/py/py.go`'s
own doc comments, then `collision_test.go`'s real subprocess-import
test) — generated Python bindings do a plain `import ubx_sdk as sdk`,
resolved via standard Python module search (`PYTHONPATH`), not a
module-proxy fetch (Go) or an explicit import-map config file (TS).
Simpler than both: no manifest of any kind needs to name where the
runtime lives, a directory named `ubx_sdk` reachable on `PYTHONPATH` is
sufficient. The same underlying gap as TS still applies once outside
this monorepo, though: locally, `PYTHONPATH` points at this repo's own
private `sdk/py/`; a standalone `ubx-sdk-aws-py` repo has no access to
that path. Checked for real, not assumed to match TS's npm finding:
`pip index versions ubx_sdk` / `curl .../pypi/ubx_sdk/json` → 404, no
`.pypirc`, no `twine` installed — no PyPI account/credentials exist on
this machine, same absence as npm. Per this ticket's own explicit
instruction (unlike UBI-104, which asked the founder first), vendored
directly: `sdk/py/ubx_sdk/` copied into
`ubx-sdk-aws-py/vendor/ubx_sdk/`, both the repo root and `vendor/` on
`PYTHONPATH`. **A real PyPI/npm-publish follow-up ticket filed, not
silently deferred a second time**: UBI-107, covering both the still-open
npm gap from UBI-104 and this session's own PyPI gap in one ticket,
since both are the identical underlying "runtime never actually
published" problem and both need the founder's own new-account action.

**UBI-98's own Python-specific `lambda` finding, confirmed still holding
in THIS real generated output, not re-discovered from scratch**:
`aws_lambda_*` (20 real types) lands under `lambda_/` (trailing
underscore, `pyModuleIdent`) — verified directly in the real generated
tree (`lambda_/__init__.py`, `lambda_/function.py` importing `ubx_sdk`
cleanly), not assumed from the codegen source alone.

**Item 4, a genuinely different verification bar from TS's, done
correctly, not copy-pasted**: UBI-98's own bar is a REAL import of
every module (`importlib.import_module`, not a syntax check) — ported
exactly as such, not swapped for a lighter check. A real, live-caught
gotcha specific to this mechanism: the first local dry run of this
exact import script left `__pycache__`/`.pyc` files scattered through
every service directory (Python's own bytecode-cache side effect of a
real import), which `git add -A` swept into the initial commit before
being caught and amended out (a never-yet-pushed commit, so amending
was safe) — `.gitignore` (`__pycache__/`, `*.pyc`) added, and the CI
workflow's own sanity-check step sets `PYTHONDONTWRITEBYTECODE=1` to
avoid the problem outright rather than relying on `.gitignore` alone
to clean up after it every run.

**Everything UBI-99/UBI-104 already solved carried over clean on the
FIRST real dispatched run — zero repeat bugs, including the ported
lessons this time (`$RUNNER_TEMP` isolation, `go build -C` flag order,
pinning `actions/setup-python@v5` against its real tag before trusting
it, mirroring the `setup-deno@v2` lesson)**. Item 6, confirmed clean
without re-solving: the `Ubiquex` org's PR-creation policy
(`can_approve_pull_request_reviews: true`) already applied to this
brand-new repo automatically; only the per-repo `UBIQUEX_SOURCE_TOKEN`
secret needed re-setting.

**Required verification, met for real on the first dispatched run**:
queried the real registry (found `6.57.1`, newer than the seeded
`6.54.0`), built `ubx` from real `ubiquex` source, regenerated for real
(1682 → 1687 resource types), ran a real recursive import of every
generated module (1946 imported, zero errors), and opened a real PR:
**https://github.com/Ubiquex/ubx-sdk-aws-py/pull/1** — 285 files
changed, genuinely new AWS-service directories landing as new files,
`vendor/`/`.gitignore` untouched in the diff, `main`'s own `VERSION`
left at `6.54.0` afterward (never auto-merged).

**All three languages of UBI-103's first (provider, language) sequence
now real, live, and automated**: https://github.com/Ubiquex/ubx-sdk-aws-go
(UBI-99), https://github.com/Ubiquex/ubx-sdk-aws-ts (UBI-104),
https://github.com/Ubiquex/ubx-sdk-aws-py (UBI-105). Remaining, per
UBI-103's own umbrella: the same three-language rollout for every other
supported provider.

## Amendment (2026-08-03, UBI-106): every generated service package nests under one provider-namespace directory — a real repo-browsing fix, applied to all three live AWS repos before Google's own repos exist

**Founder finding**: UBI-98's own per-service-package layout put every
AWS service directory (200+ for `hashicorp/aws`) directly at the repo
ROOT. GitHub's file browser sorts directories before files
alphabetically, so `README.md` sat below the fold behind `accessanalyzer/`
through `xray/` — a real, live-verified repo-browsing problem, not a
hypothetical.

**Fix**: `sdk/codegen/templates/{go,ts,py}`'s own `GeneratedRepo`
functions now nest every service package under one namespace directory
named after the provider's own `shortName` (`aws/` for
`hashicorp/aws`) — `aws/iam/`, never `iam/` at the repo root.
`go.mod`/`package.json`/`pyproject.toml`/`README.md`/`VERSION` and (per
UBI-104/105's own vendoring decisions) `vendor/`/`deno.json`/`.gitignore`
all stay at the true repo root — only service packages move.

**A real, Python-specific consequence, guarded before it could ever
occur, not discovered after the fact**: unlike Go/TS (directory names
carry no identifier-validity requirement), Python's own dotted
`import aws.iam.role` requires "aws" itself to parse as a valid Python
identifier. New `pyShortNameIdent` guards the namespace segment the
same way `pyModuleIdent` already guards service/local names, PLUS a
hyphen-to-underscore replacement neither of those needs (`shortName`'s
own `pyproject.toml` package NAME conventionally keeps hyphens, e.g.
`ubx-sdk-aws` — fine there, a TOML string, not a Python identifier).
Not exercised by "aws" itself (no hyphen), but a real, universal
constraint for any future provider whose `shortName` has one (e.g. a
hypothetical `google-beta`) — guarded now, before Google's own repos
exist, the same defensive-not-yet-confirmed posture `pyModuleIdent`'s
own leading-digit guard already established. Python also gets a new
namespace-level `aws/__init__.py` (a real package marker, not relying
on PEP 420 implicit namespace packages) — Go/TS need no analogous
marker (directories with no `package`/module-init requirement).
**Superseded — see this document's own "Python namespace-package
layout" amendment near the end of this document**: `aws/` (this
UBI-106 amendment's own namespace segment) is itself now nested one
level further under a shared `ubx` PEP 420 implicit namespace package
(`ubx/aws/`), which DOES rely on PEP 420 (deliberately, at that outer
level only) — this paragraph's own `aws/__init__.py` claim is still
accurate for the `aws/` level itself, just no longer the outermost
namespace segment.

**Existing tests updated, not just the generator**: every unit test in
`sdk/codegen/templates/{go,ts,py}` and `cli/sdk_test.go` asserting a
flat path (`"iam/doc.go"`, `"widget/doc.ts"`, ...) now asserts the
nested one (`"aws/iam/doc.go"`, `"widget/widget/doc.ts"`, ...) — caught
by simply running the suite after the codegen change, not inferred in
advance. The full-provider live tests' own service-count logging
(`TestFullProvider_{Go,TS,Py}_*Clean`) needed its own fix too: grouping
by the FIRST `/`-segment alone would always report "1" once every
service nests under one shared namespace directory — fixed to group by
each file's own full directory (`LastIndex`, not `IndexByte`), confirmed
against the real full-provider schema afterward (258/259/258 real
service packages, not the misleading "1" the unfixed logging would
have shown).

**Conformance fixtures regenerated, not hand-edited**: `sdk/conformance/programs/{go,ts,py}/generated`
rebuilt via a real `ubx sdk gen` run against the same `hashicorp/aws@6.54.0`
pin, filtered back down to just `db`/`db.instance` (this fixture's own
long-standing "filtered to aws_db_instance only" curation, unchanged).
Python's own fixture — which already skips the hyphenated
`hashicorp-aws` source directory entirely, a real, load-bearing
constraint named in UBI-98's own restructure — keeps that skip but
gains the new `aws/` namespace segment (no hyphen, no constraint
against it): `generated/aws/db/instance.py`, not `generated/db/instance.py`.
Only each entry file's own import-path line changed
(`payments.go`/`.ts`/`.py`); golden fixture `content_hash` values
updated to match (resolved `resources`/`stack`/`intent.summary` stayed
byte-identical, re-verified via all three real evaluators, the same
"only content_hash moves" pattern UBI-98 session 2 already established
for a renaming change).

**Applied to all three ALREADY-LIVE AWS repos, via the real automation
itself, not a manual file move**: rather than hand-moving each repo's
existing tree, the `ubiquex` codegen fix was pushed first, each repo's
own pre-existing, unmodified `version-watch.yml` was dispatched fresh
against it, and the SAME real automation that already proved itself in
UBI-99/104/105 both re-verified itself as unbroken by this change AND
produced the retrofit as a byproduct of a genuine regeneration — no
`mv`, no hand-authored tree surgery. This also meant closing each
repo's own still-open, now-superseded PR #1 (the pre-UBI-106
`6.54.0`→`6.57.1` regen from the prior session) first, so the fresh
dispatch could open a clean PR #2 on the same deterministic branch
name rather than colliding with it.

**Required verification, met for real, per repo, zero repeat bugs from
UBI-99/104/105**: a real `workflow_dispatch` run per repo (all three
green on the first try) produced a real PR per repo showing the nested
tree for real —
[ubx-sdk-aws-go#2](https://github.com/Ubiquex/ubx-sdk-aws-go/pull/2),
[ubx-sdk-aws-ts#2](https://github.com/Ubiquex/ubx-sdk-aws-ts/pull/2),
[ubx-sdk-aws-py#2](https://github.com/Ubiquex/ubx-sdk-aws-py/pull/2) —
confirmed via the real GitHub API (each branch's own root tree: `aws/`
alongside `go.mod`/`package.json`/`pyproject.toml`/`README.md`/`VERSION`/
`vendor/`, never nested), Python's `lambda_` fix confirmed still
correct under the new `aws/lambda_/` path, and the real sanity-check
step (build/typecheck/recursive-import) green against the new paths in
all three. Founder confirmed merging all three (the "README below the
fold" finding can only be verified against `main`, and these workflows
never auto-merge by design) — merged, and the real GitHub file-browser
view of all three repos' own root confirmed the README now renders
immediately, no scrolling, in every one.

## Amendment (2026-08-03, UBI-108): first non-AWS provider — `ubx-sdk-google-go` — surfaces a real, new naming-collision class hashicorp/aws's own 1,682 types never happened to hit

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-google-go** (public),
seeded via a real `ubx sdk gen --lang go --out .` against
`hashicorp/google@7.40.0`, UBI-106's nested `google/` layout from birth
(no retrofit needed — the first repo born after that fix). Naming
locked per UBI-103: `google`, never `gcp` — matches the real Terraform
provider source (`hashicorp/google`) exactly, both as the repo suffix
and the nested namespace directory.

**Real schema scale, confirmed not assumed**: **1,319 resource types**
for `hashicorp/google@7.40.0` — the ticket's own "likely 400-600+"
guess was wrong by a wide margin; Google's real schema is much closer
to AWS's own 1,682 than to a mid-size provider. 118 real service
packages (vs. AWS's 258/259).

**Item 5's own explicit ask — a real, new collision class found,
NOT assumed clean by extrapolating AWS's own fixes**: running the real
full-schema generation hit a genuine, live self-check failure UBI-96's
fix does NOT cover — 3 real instances (`google/spanner`,
`google/workstations`, `google/migration`) where an independent,
TOP-LEVEL sibling resource's own wire name is exactly
`<other-resource>_config` (e.g. `google_spanner_instance` +
`google_spanner_instance_config`, both real, both independently
addressable resources — not one resource's own nested block, which is
what UBI-96 originally fixed). The `_config`-suffixed resource's own
real binding var (`spanner.InstanceConfig`, PascalCase of its own real
wire name) collides with its sibling's entirely CODEGEN-INVENTED
`<Name>Config` companion struct (`spanner.Instance`'s own auto-derived
`InstanceConfig`) — both package-level Go identifiers in the same
service package.

**Fixed at the source, not routed around**: `GeneratedRepo` (Go only —
TS/Python are structurally immune, confirmed not assumed: ES modules
and Python modules each give every FILE its own independent namespace,
the exact reasoning UBI-98 already established for why UBI-96 itself
was Go-only) now checks every sibling's own real binding-var name
*before* rendering any file in a service package; when a resource's
default `<Name>Config` struct would collide with a real sibling's own
binding var, only the CODEGEN-INVENTED struct name is disambiguated
(trailing underscore — `InstanceConfig_` — the same escape convention
`goPackageIdent`/`pyModuleIdent` already use for a keyword collision),
never the real resource's own wire-derived identity. A dedicated
regression test (`TestGeneratedRepo_SiblingConfigCollision_Escaped`)
mirrors the real `spanner.Instance`/`instance_config` shape exactly.
Re-verified against the real full `hashicorp/google@7.40.0` schema
after the fix: zero collisions, real `go build ./...`/`go vet ./...`
clean across all 1,319 types (1,437 files) — confirmed via a real
`replace`-free build against the actual published `ubx-sdk-go`.
AWS's own full-provider live tests re-run afterward too: no regression.

**Item 5's other half — the oversized-recursive-type compiler-crash
class (UBI-98) — confirmed NOT present in Google's real schema,
checked not assumed**: largest real generated file is
`google/container/cluster.go` at 2,495 lines — nowhere near the
>10MB/~250,000-line pathological scale `aws_wafv2_web_acl_rule` hit
before UBI-98's own shape-dedup fix. Also checked and confirmed clean:
no Go-keyword/go-tool-special service-name collision (AWS's own
`default`/`main` finding) anywhere in Google's real 118 derived service
names.

**Item 6, confirmed not re-solved**: `ubx-sdk-go` (the shared Go
runtime) is already real and published — this repo's `go.mod` depends
on it directly, verified via a real credential-free `go build`/`go vet`
against the real public Go proxy, no `replace` directive, exactly like
`ubx-sdk-aws-go`. No new publishing decision needed for Go (unlike
TS/Python's own vendoring precedent from UBI-104/105).

**Required verification, met for real on the first dispatched run**:
seeded one version behind the real latest (`7.40.0`, latest `7.42.0`)
to get a genuine bump, same discipline as every prior repo in this
family. A real `workflow_dispatch` run
(https://github.com/Ubiquex/ubx-sdk-google-go/actions) queried the real
registry, found `7.42.0`, built `ubx` from real `ubiquex` source,
regenerated for real, ran a real `go build ./...`/`go vet ./...`
against the full regenerated tree, and opened a real PR —
**https://github.com/Ubiquex/ubx-sdk-google-go/pull/1** — 152 files
changed, `google/` nesting and `go.mod`'s own corrected module name
both intact in the diff. Founder confirmed merging (same as UBI-106) —
merged; `main` confirmed at `7.42.0` via the real GitHub API (not the
`raw.githubusercontent.com` CDN, which lagged behind the merge by a few
minutes — a real, if minor, verification gotcha worth naming: check the
API, not the CDN mirror, when confirming a just-merged change).

**UBI-103's rollout, second provider now underway**: AWS (all three
languages, UBI-99/104/105/106) and Google/Go (UBI-108) both real and
live. Remaining: Google's own TS/Python siblings (their own runtime-
publishing questions to investigate fresh, not assumed to match AWS's
answers), then Azure and Kubernetes.

## Amendment (2026-08-03, UBI-109): `ubx-sdk-google-ts` — UBI-108's Go-side collision fix confirmed structurally unnecessary for TS, checked directly rather than trusted by analogy; runtime-publishing status re-checked live, still unpublished

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-google-ts**
(public), seeded via a real `ubx sdk gen --lang ts --out .` against
`hashicorp/google@7.42.0` — UBI-106's nested `google/` layout from
birth, matching `ubx-sdk-google-go`'s own precedent.

**Runtime-publishing status, re-checked live, not assumed from
UBI-104's own finding**: `npm view @ubx/sdk` still 404 — unpublished.
Vendored again (`vendor/ubx-sdk-runtime/`), the identical UBI-104
stopgap. **UBI-107 updated with a real, load-bearing note**: this is
now the SECOND repo carrying a separately-vendored copy of the same
unpublished runtime — the drift risk that ticket's own scope names
stops being theoretical the moment a second copy exists.

**Item 4's own explicit ask — checked directly against Google's real
schema from the TS side, not trusted by analogy from Go's own
conclusion**: generated the full real `hashicorp/google@7.42.0` schema
via `--lang ts` and inspected the exact three files UBI-108 found
colliding in Go (`spanner/instance(_config)`,
`workstations/workstation(_config)`, `migration/center_report(_config)`)
directly. Confirmed structurally, not inferred: `instance.ts` declares
a bare `export interface InstanceConfig` (no disambiguation needed —
unlike Go, no trailing underscore), and `instance_config.ts`
separately declares `export const InstanceConfig: ResourceBinding<...>`
in its own, completely independent module scope. Real proof, not just
"no error surfaced": TS's per-file ES-module namespacing (the same
structural reasoning UBI-98 already established for why UBI-96 was
Go-only) holds for this NEW collision shape too — zero special-casing
needed in `sdk/codegen/templates/ts`, confirmed rather than assumed.

**UBI-98's own compiler-crash-class check, also confirmed clean for
TS specifically**: largest real generated file, `google/container/cluster.ts`,
2,615 lines — comparable to Go's own 2,495-line finding, nowhere near
crash scale.

**Required verification, met for real**: a real `workflow_dispatch` run
(https://github.com/Ubiquex/ubx-sdk-google-ts/actions) queried the real
registry, built `ubx` from real `ubiquex` source, regenerated for real,
ran a real `deno check --no-remote` across the full ~1,330-type tree
(1,448 files, zero errors), and opened a real PR —
**https://github.com/Ubiquex/ubx-sdk-google-ts/pull/1**, a genuine
`7.41.0`→`7.42.0` regeneration (138 files changed), `vendor/`/`deno.json`
untouched. Founder confirmed merging — merged; `main` confirmed at
`7.42.0` via the real GitHub API.

**A real mistake made and caught mid-session, not silently smoothed
over**: this repo was seeded at `7.42.0` — the actual real latest at
seed time — leaving no room for a genuine version-bump dispatch (the
first real dispatch correctly no-op'd, "already current"). First fix
attempt was sloppy: changed only the `VERSION` tracker file to `7.41.0`
without regenerating content, leaving every generated file's own
`// Code generated from hashicorp/google@7.42.0` banner literally
contradicting the tracker — caught before it could mislead a real
reviewer, corrected by actually regenerating real `7.41.0` content so
the tracker and the code agree, restoring an honest bump path to the
dispatch that followed.

**All of AWS + Google/Go + Google/TS now real and live**; remaining in
UBI-103's rollout: Google's own Python sibling (its own runtime-
publishing question — PyPI, not assumed to match TS's npm answer),
then Azure and Kubernetes.

## Amendment (2026-08-03, UBI-110): `@ubx/sdk` genuinely published on JSR — both TS bindings repos switched from vendored copy to a real `jsr:@ubx/sdk` dependency, surfacing a real incompatibility between `deno check --no-remote` and any remote import

**Publish confirmed live, checked directly before touching anything**:
`https://jsr.io/@ubx/sdk/meta.json` reports `"latest":"0.1.0"`, published
`2026-08-03T20:22:05Z` (minutes before this session started) — UBI-107's
own npm/JSR publish, superseding both UBI-104's and UBI-109's vendoring
findings (accurate when written, not since).

**Applied to both `ubx-sdk-aws-ts` (PR
[#3](https://github.com/Ubiquex/ubx-sdk-aws-ts/pull/3)) and
`ubx-sdk-google-ts` (PR
[#2](https://github.com/Ubiquex/ubx-sdk-google-ts/pull/2))**: `deno.json`'s
import map now reads `"@ubx/sdk": "jsr:@ubx/sdk@^0.1.0"` (was
`./vendor/ubx-sdk-runtime/index.ts`); a new `deno.lock` pins the
resolved version + integrity hash; `vendor/ubx-sdk-runtime/` removed
from both repos, resolution confirmed working before deletion, not
after. `deno check` across each repo's full generated tree is unchanged
before/after (aws-ts: 1,946 files; google-ts: 1,448 files; zero errors
both times) — a dependency-resolution swap only, no generated-code
behavior change.

**A real, load-bearing Deno constraint found and worked around, not
assumed**: JSR's own `@ubx/sdk/meta.json` (the unversioned package
index used to resolve a semver range like `^0.1.0`) is fetched fresh on
every resolution — not something a lockfile alone lets you skip — and
Deno's *minimum dependency age* policy (24h default, unstable flag
`--minimum-dependency-age`) refuses to resolve a version published
under 24h ago. `@ubx/sdk@0.1.0` was ~10 minutes old at the time of this
session. Worked around with a one-time `--minimum-dependency-age=0`
override to generate the *initial* `deno.lock`; confirmed empirically
afterward that once a version is locked, later `deno cache`/`deno
check` runs (including a from-scratch cache, tested by deleting the
local JSR cache directory and re-resolving) succeed with no override
needed — the age gate applies to *range* resolution against live
`meta.json`, not to fetching an already-locked, cached-by-hash version.
This is why the age-override flag appears nowhere in either repo's
committed `deno.json`/CI — it was only ever needed once, locally, to
produce the lockfile that ships in the PR.

**A second, more structural finding, confirmed directly not
assumed**: `deno check --no-remote` cannot pass against a `jsr:` import
under any circumstances — tested directly (warm module cache, `deno.lock`
already pinned to the exact resolved version, `--frozen` passed too) and
it still fails with `"A remote specifier was requested ... but
--no-remote is specified"`. `--no-remote` structurally disables *all*
remote-specifier resolution, not just "disallow new network calls" —
lockfile and cache state are irrelevant to it. The old vendored setup's
`deno check --no-remote` only ever worked because `@ubx/sdk` was a plain
local file specifier (`./vendor/...`), never actually exercising
`--no-remote`'s real remote-refusal behavior. Both repos' CI
(`version-watch.yml`) sanity-check step is now `deno cache` (network,
warms the lockfile/module cache) followed by `deno check --frozen`
(cache-only from there, fails if the lockfile would need to change) —
the genuine offline-safe equivalent for a real remote dependency.
`rsync`'s own exclude list in both workflows gained `deno.lock`
alongside the pre-existing `deno.json` exclude (neither is ever
`ubx sdk gen` output).

**Required verification, met for real, per repo**: rather than risk
main's real, correct `6.57.1`/`7.42.0` state, each repo's throwaway
verification ran on a disposable branch forked from the real PR branch
— `VERSION` and generated content genuinely regenerated one real
release behind (`aws@6.56.0`, `google@7.41.0`) via `ubx sdk gen`, pushed,
then a real `workflow_dispatch` against that branch exercised the full
pipeline for real: registry query, `ubx` built from real `ubiquex`
source, real regeneration, and — the part that mattered most, since it
only runs when a version bump is detected — a real `deno cache` + `deno
check --frozen` pass against the live `jsr:@ubx/sdk` dependency inside
GitHub Actions' own ephemeral, cold-cache runner
([aws-ts run](https://github.com/Ubiquex/ubx-sdk-aws-ts/actions/runs/30851344064),
[google-ts run](https://github.com/Ubiquex/ubx-sdk-google-ts/actions/runs/30851779485),
both green). Each dispatch opened its own bot PR regenerating back to
the real latest version; each PR's diff against `main` was byte-for-byte
identical in shape to the real UBI-110 PR it was forked from (aws-ts:
60/-534 both; google-ts: 62/-531 both), confirming the round trip landed
exactly back on the intended state. Both verification-only PRs closed
without merging (correctly refused by the harness when a remote-branch
deletion was attempted afterward, matching UBI-106's own precedent —
closing the PR is sufficient, deleting the branch isn't required). The
two real UBI-110 PRs (`#3`/`#2` above) are left open pending the
founder's own merge decision, same protocol as every prior repo in this
rollout.

## Amendment (2026-08-03, UBI-111): `ubx-sdk-google-py` — Google's Python sibling, PyPI status re-checked via the real API endpoints (not the bot-challenged HTML page), the sibling-`_config` collision confirmed structurally immune for Python too, checked directly not by analogy

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-google-py**
(public), founder-created per this rollout's standing protocol —
confirmed to exist and cloned before any work started (it genuinely
didn't exist on the first check this session; the founder created it
mid-session, re-confirmed via `gh api repos/Ubiquex/ubx-sdk-google-py`
before proceeding). Seeded via a real `ubx sdk gen --lang py --out .`
against `hashicorp/google@7.42.0` — UBI-106's nested `google/` layout
from birth, matching both Google siblings' own precedent. 1,330 real
resource types, 1,449 real Python modules.

**PyPI publishing status, checked via the real API, not the page a
browser would render**: `pypi.org/project/ubx-sdk/`'s plain HTML page
returns HTTP 200 — but that's PyPI's own bot-challenge shell (a
"Client Challenge" JS page served for the URL regardless of whether the
underlying project exists), not evidence of a real project. The two
authoritative endpoints both say **not published**:
`pypi.org/pypi/ubx-sdk/json` → 404, `pypi.org/simple/ubx-sdk/` → 404 —
cross-checked against a known-real package (`requests`) on the same two
endpoints, both 200. Vendored again (`vendor/ubx_sdk/`, byte-identical
to this repo's own canonical `sdk/py/ubx_sdk/__init__.py`, confirmed by
diff, not assumed copied correctly) — the identical UBI-105 stopgap.
This is now the **second** Python repo carrying a separately-vendored
copy of the same unpublished runtime; **UBI-107** should be checked
before any future Python sibling repeats this vendoring rather than
depending on a real published package.

**Item 4's own explicit ask — checked directly against Python's real
generated output, not trusted by analogy from either Go's original
finding or TS's own UBI-109 confirmation**: inspected the exact
`spanner`/`workstations`/`migration` files UBI-108 found colliding in
Go. `google/spanner/instance.py` declares a bare `class InstanceConfig`
(the `google_spanner_instance` resource's own args dataclass);
`google/spanner/instance_config.py` separately declares
`InstanceConfig = sdk.ResourceBinding(...)` (the *different*
`google_spanner_instance_config` resource's own binding) — same
identifier, different files. Neither service directory's `__init__.py`
re-exports submodule symbols into a shared namespace (checked directly:
each `__init__.py` only carries `SOURCE_PROVENANCE`), so the two
`InstanceConfig`s never actually share a namespace —
`google.spanner.instance.InstanceConfig` and
`google.spanner.instance_config.InstanceConfig` are distinct,
independently-addressable names. Real proof, not just "no error
surfaced": a genuine `importlib.import_module` of both files (and all
1,449 real modules) succeeded, zero errors — if the collision applied,
this import would have failed loudly. Third language, third
independent confirmation of the same structural immunity (per-file/
per-module namespacing) TS already established in UBI-109 — Python's
own module system holds for a reason distinct from TS's ES-module
scoping, but the practical result is identical.

**UBI-98's compiler-crash-class check, also confirmed clean for
Python specifically**: largest real generated file,
`google/container/cluster.py`, 2,520 lines — matches Go's 2,495 and
TS's 2,615 findings for the exact same resource, nowhere near crash
scale.

**UBI-98/105's Python-keyword-collision finding (AWS's `lambda` →
`lambda_/`) does NOT recur for Google**: checked all 118 real Google
service directory names against `keyword.iskeyword`/
`keyword.issoftkeyword` directly — zero collisions. Not assumed
exhaustive from AWS's own single finding; Google's own service names
were checked fresh.

**Required verification, met for real**: rather than risk `main`'s own
real, correct `7.42.0` state, the dispatched-run proof ran on a
disposable branch forked from `main`, genuinely regenerated one real
release behind (`7.41.0`) via `ubx sdk gen`, confirmed to import cleanly
locally first, then a real `workflow_dispatch`
(https://github.com/Ubiquex/ubx-sdk-google-py/actions/runs/30853315736,
green) exercised the full pipeline: registry query, `ubx` built from
real `ubiquex` source, real regeneration, and a real recursive
`importlib.import_module` of every generated module inside GitHub
Actions' own runner (matching UBI-105's own "real import, not a syntax
lint" bar). The dispatch's own bot PR regenerating back to `7.42.0` had
an **empty diff against `main`** (0 files) — stronger confirmation than
either TS sibling's own dispatch got, since this repo's `main` already
held the real `7.42.0` content directly (no separate feature-branch
step, unlike UBI-110's existing repos): the `7.41.0`→`7.42.0` round trip
is byte-for-byte deterministic. Verification-only PR closed without
merging (nothing to merge, given the empty diff).

**All of AWS (Go/TS/Python) + Google (Go/TS/Python) now real and
live**; UBI-103's rollout has just Azure and Kubernetes remaining.

## Amendment (2026-08-03, UBI-112): `ubx-sdk-kubernetes-go` — first non-cloud-provider in the rollout, two genuinely new schema-shape findings neither AWS nor Google ever surfaced, both fixed at the source

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-kubernetes-go**
(public) — didn't exist at session start (`gh api
repos/Ubiquex/ubx-sdk-kubernetes-go` → 404, confirmed against the full
org repo listing); unlike every prior repo in this rollout, the founder
explicitly authorized the agent itself to create it this session rather
than founder-creating it beforehand. Scope confirmed via the ticket's
own comment thread before anything else: **`hashicorp/kubernetes`
ONLY** — `hashicorp/helm` is a separate provider, explicitly out of
scope, its own future ticket. Repo-scoped `UBIQUEX_SOURCE_TOKEN` PAT
added by the founder, same per-repo pattern as every sibling (checked
via `gh secret list`, genuinely empty before, present after).

**Real schema scale, confirmed not assumed**: **81 resource types** for
`hashicorp/kubernetes@3.2.0` — a real, architecturally smaller and
flatter schema than AWS (1,682) or Google (1,330), exactly as UBI-103
predicted going in, now confirmed rather than guessed. 37 derived
service packages.

**Item 3(a), the compiler-crash-class check (UBI-98) — confirmed
absent, not assumed smaller just because there are fewer resource
types**: largest real generated file is `kubernetes/cron/job_v1.go` at
1,855 lines, nowhere near the pathological ~250,000-line scale that hit
`aws_wafv2_web_acl_rule` pre-UBI-98.

**Item 3(b), the sibling-`_config` collision class (UBI-96/108) —
checked for real, confirmed absent**: zero collisions, `go
build ./...`/`go vet ./...` clean across the full 81-type tree, both
before and after the two fixes below.

**Item 3(c), a genuinely new schema-shape class, found and fixed at the
source, not routed around**: the real full-schema generation hit a
live failure neither AWS nor Google's own schemas have ever produced —
`sdk gen: hashicorp/kubernetes@3.2.0: sdk/codegen/ir:
FromSchema("kubernetes_validating_admission_policy_v1"): attribute
"metadata": parse type: EOF`. Root cause: `provider/schema.go`'s
tfplugin6 translation only ever read `Schema_Attribute.Type`; one real
resource, `kubernetes_validating_admission_policy_v1`, carries several
attributes (15 total) via `Schema_Attribute.NestedType` instead — the
Terraform Plugin Framework's structured/object-attribute wire encoding,
mutually exclusive with `Type` on the wire — leaving `Type` empty and
failing to parse as JSON. Quantified before fixing, not just patched
blind: a throwaway diagnostic against the real launched provider binary
found exactly 1 of 81 real resource types affected, 15 nested-type
attributes total, real recursion depth 5
(`kubernetes_validating_admission_policy_v1.spec.match_constraints.object_selector.label_selector.match_expressions.key`
— the PodSpec-containing-containers self-referential shape the ticket
named as a real risk up front, confirmed present). Fixed at the
translation boundary: `NestedType` (itself recursive — a
`Schema_Object`'s own attributes may carry further `NestedType`) is now
converted to the equivalent `cty` object/collection type (`SINGLE` →
bare object, `LIST`/`SET`/`MAP` → the matching cty collection wrapping
it) before anything downstream ever sees it, so IR, codegen, and
`ctyvalue.go`'s own config-encoding all keep working unmodified —
zero changes needed outside `provider/schema.go` and the error-plumbing
its now-fallible `blockFromV6`/`schemaFromV6`/`schemaMapFromV6`
required. `tfplugin5` confirmed to carry no equivalent field at all
(`NestedType` is a protocol-v6-only addition) — no v5 translation
changes needed. New regression tests
(`provider/schema_test.go`): single/list/set/map nesting, real depth-2
recursion, and a malformed-child-type error case. Re-verified against
AWS's real full `hashicorp/aws@6.54.0` schema afterward (live test,
`UBX_CONFORMANCE_LIVE=1`): identical 1,941 files/258 packages, zero
regression.

**A second, real, novel finding — a reviewability defect, not a
compile error, found and also fixed at the source, this time
generalized rather than Kubernetes-specific**: `ir.ServiceAndLocalName`
(UBI-98) assumes a stable `<provider>_<service>_<resource>` wire-name
shape, mechanically splitting on the first two tokens. Kubernetes' real
provider instead widely uses a flatter
`kubernetes_<resource>[_v1|_v2|_v2beta2]` convention, and before a fix
this reduced the local name to literally nothing but the version marker
for many real resources — `kubernetes_deployment_v1` → service
`deployment`, local name just `v1`, producing a generated file named
`v1.go` that carries zero information about which resource it defines.
The generated tree had *nine* such bare-version files
(`deployment/v1.go`, `job/v1.go`, `namespace/v1.go`, `pod/v1.go`,
`role/v1.go`, `secret/v1.go` alongside `secret/v1_data.go`,
`service/v1.go`, `endpoints/v1.go`, `ingress/v1.go`) before the fix.
Fixed generally, not hardcoded to Kubernetes: when the entire remaining
local-name portion of a wire type is a bare API-version marker (`v1`,
`v2beta2`, `v1alpha1`, ... — regex `^v[0-9]+(alpha|beta)?[0-9]*$`), the
service token folds back into the local name (`deployment_v1.go`, not
`v1.go`). Confirmed live, not assumed, that this changes nothing for
either existing provider: AWS's real full schema re-run (above) is
byte-identical in file/package count; a live diagnostic against
Google's real `hashicorp/google@7.42.0` schema found exactly one wire
type ending in a version-like token
(`google_apigee_security_profile_v2`) and confirmed it already has more
than one token ahead of the version marker (`security_profile_v2`, not
a bare `v2`), so it never hits this fold's narrow trigger — zero
behavior change for Google. New regression test in
`sdk/codegen/ir/ir_test.go` covers both the fold and the confirmed-unaffected
cases side by side.

**A related, deliberately NOT "fixed" limitation, named rather than
silently smoothed over**: Kubernetes' own real provider is inconsistent
about underscore-separating compound resource names —
`kubernetes_daemonset` (no underscore) vs. `kubernetes_daemon_set_v1`
(underscore) both real, both meaning the same real-world `DaemonSet`
kind. The mechanical, non-curated wire-name split has no way to know
these are the same concept without a hand-maintained synonym table —
exactly the class of limitation `ServiceAndLocalName`'s own doc comment
already accepts for AWS's EC2/VPC "core" resource family (`aws_vpc`,
`aws_instance`, and friends fragmenting into many small packages rather
than one curated "ec2" package). The result: `DaemonSet` lands in two
differently-named service directories (`daemonset/`, `daemon/`) in the
real generated tree. Real, accepted, documented — not a regression from
this session's fixes, and out of scope for a purely mechanical
derivation by design (docs/sdk.md's and `ServiceAndLocalName`'s own
"cannot ever consult an external...taxonomy" constraint, since `ubx sdk
gen` must stay 100% local/offline).

**Item 6, confirmed not re-solved**: `ubx-sdk-go` (the shared Go
runtime) is already real and published — this repo's `go.mod` depends
on it directly (module corrected post-gen to
`github.com/ubiquex/ubx-sdk-kubernetes-go`, matching UBI-108's own
precedent — codegen's mechanical shortName derivation always writes
`.../ubx-sdk-kubernetes`), verified via a real credential-free `go
build ./...`/`go vet ./...` against the real public Go proxy, no
`replace` directive.

**Required verification, met for real**: seeded at `3.2.0`, one real
version behind the real latest (`3.2.1`) at seed time, same discipline
as every prior repo. Initial seed pushed directly to `main` (a brand
new, empty repo — nothing to risk, same as UBI-108's own first-seed
precedent, unlike later sessions' disposable-branch verification
against an already-correct `main`). First dispatched run genuinely
failed, not silently retried past: it built `ubx` from `ubiquex`'s real
`main` on GitHub, which didn't yet have this session's local fixes
committed — the exact same `NestedType` `EOF` failure reproduced in CI
after reproducing locally first. Fixed by actually pushing the fix to
`ubiquex` `main` (`adfa68d..42ece27`) before re-dispatching, not by
weakening the workflow. Second dispatched run green:
https://github.com/Ubiquex/ubx-sdk-kubernetes-go/actions/runs/30855489963
— queried the real registry, found `3.2.1`, built `ubx` from the
now-fixed `ubiquex` source, regenerated for real, ran a real `go
build ./...`/`go vet ./...` against the full regenerated 81-type tree,
opened a real PR —
**https://github.com/Ubiquex/ubx-sdk-kubernetes-go/pull/1** — a clean,
deterministic `3.2.0`→`3.2.1` provenance-only bump (38 files, 75
additions/75 deletions — exactly 2 lines × 37 `doc.go` files' version
stamps + 1 `VERSION` line, zero resource-schema content changes,
confirming the round trip is byte-for-byte deterministic here too).
Founder confirmed merging — merged; `main` confirmed at `3.2.1` via the
real GitHub API.

**UBI-103's rollout**: AWS and Google (all three languages each) real
and live; Kubernetes/Go (UBI-112) now real and live, first non-cloud
provider proven. `hashicorp/helm` explicitly deferred to its own future
ticket (not part of this one's scope). Remaining: Kubernetes' own
TS/Python siblings, then Azure.

## Amendment (2026-08-03, UBI-113): `ubx-sdk-kubernetes-ts` — UBI-112's two real `ubiquex`-core fixes confirmed to hold for TS's own template path, checked directly not assumed; first TS repo in this family born directly on the real published `@ubx/sdk`, no vendoring stopgap ever existed here

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-kubernetes-ts**
(public) — this time founder-created beforehand per the ticket's own
scope (item 1), reverting to this rollout's standing protocol rather
than UBI-112's one-time agent-creates exception (confirmed via `gh api`
→ 404 before the founder created it, then re-confirmed after). Its own
repo-scoped `UBIQUEX_SOURCE_TOKEN` PAT was already present at session
start (added by the founder alongside repo creation, not a separate
ask this time).

**Scope carried forward, not re-litigated**: `hashicorp/kubernetes`
ONLY — `hashicorp/helm` stays deferred to its own future ticket, per
UBI-112's own confirmed scope.

**Item 3's own explicit ask, done for real, not assumed from Go**:
verified both of UBI-112's `ubiquex`-core fixes directly against TS's
own generated output, not trusted by analogy —

1. **`NestedType`/structured-attribute translation**: generated
   `kubernetes_validating_admission_policy_v1` and inspected the actual
   TS output directly — a full, correctly-typed, real recursion-depth-5
   interface tree
   (`AdmissionPolicyV1_Spec_MatchConstraints_ObjectSelector` down to
   `..._NamespaceSelector_MatchExpressions`). Confirmed structurally:
   the fix lives entirely in `provider/schema.go`'s tfplugin6
   translation layer, shared by every language ahead of any
   per-language template — TS needed zero template-level changes,
   exactly as the shared-translation-boundary design intended.
2. **Bare-version-suffix filenames**: checked the full generated tree
   directly for any `v1.ts`/`v2beta2.ts`-style bare-version file — zero
   found; every versioned resource's own local name stays
   self-descriptive (`deployment_v1.ts`, not `v1.ts`), same
   `ir.ServiceAndLocalName` fix, same result as Go.

**Item 5, the compiler-crash-class (UBI-98) and sibling-`_config`
collision (UBI-96/108) checks — done for real, specifically for TS**:
`deno check --frozen` clean across the full 81-type/118-file tree, both
at the initial seed and after the verification bump. Largest file
`kubernetes/cron/job_v1.ts` at 1,860 lines (Go's own equivalent: 1,855
lines) — no language-specific blowup, no collision.

**Real schema scale, confirmed independently of Go's own count**: 81
resource types for `hashicorp/kubernetes@3.2.0` — same real count, 37
derived service packages, confirmed by direct generation, not assumed
to match Go's number just because it's the same provider version.

**Item 3's runtime decision — the first TS repo in this family with
nothing to migrate**: depends on `jsr:@ubx/sdk@^0.1.0` directly from
the initial commit (UBI-107/110's real publish already existed at this
repo's birth) — no vendored `vendor/ubx-sdk-runtime/` stopgap ever
existed here, unlike every prior TS repo (`ubx-sdk-aws-ts`,
`ubx-sdk-google-ts`) which started vendored and migrated later.
`deno.lock` needed the same one-time
`--minimum-dependency-age=0` override UBI-110 found and documented
(`@ubx/sdk@0.1.0` was still under 24h old at generation time) — the
override was only ever a local, one-time step to produce the initial
lockfile; neither the committed `deno.json` nor the CI workflow carries
it, matching UBI-110's own confirmed "once locked, no override needed
again" finding, re-confirmed here (the CI dispatch's own `deno cache` +
`deno check --frozen` ran with no override and passed clean).

**Required verification, met for real, clean on the first dispatch this
time (unlike UBI-112's Go repo, where the first dispatch genuinely
failed because the `ubiquex` fix hadn't been pushed to `main` yet — that
fix has been on `main` since UBI-112, so there was nothing left to
discover here)**: seeded at `3.2.0`, one real version behind the real
latest (`3.2.1`, unchanged since UBI-112 — no new release in between).
Initial seed pushed directly to `main` (a brand-new empty repo).
Dispatched run green on the first try:
https://github.com/Ubiquex/ubx-sdk-kubernetes-ts/actions/runs/30856441168
— queried the real registry, found `3.2.1`, built `ubx` from
`ubiquex`'s real `main`, regenerated for real, ran a real `deno cache` +
`deno check --frozen` against the full regenerated 81-type tree, opened
a real PR — **https://github.com/Ubiquex/ubx-sdk-kubernetes-ts/pull/1**
— a clean, deterministic `3.2.0`→`3.2.1` provenance-only bump (39
files, 76/-76 lines: 37 `doc.ts` version stamps + `VERSION` +
`package.json`'s own embedded version string, zero resource-schema
content changes). Founder confirmed merging — merged; `main` confirmed
at `3.2.1` via the real GitHub API.

**UBI-103's rollout**: AWS and Google (all three languages each) real
and live; Kubernetes now real and live in Go (UBI-112) and TS
(UBI-113) — `hashicorp/helm` still deferred to its own future ticket.
Remaining: Kubernetes' own Python sibling, then Azure.

## Amendment (2026-08-04, UBI-114): `ubx-sdk-kubernetes-py` — Kubernetes' Python sibling, both of UBI-112's real `ubiquex`-core fixes confirmed to hold for Python's own template path, checked directly not assumed; completes Kubernetes' full Go/TS/Python row

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-kubernetes-py**
(public) — founder-created beforehand per the ticket's own scope (item
1), standing protocol (confirmed via `gh api` returning the repo before
the agent touched anything, not a 404 requiring a stop-and-ask this
time). Its own repo-scoped `UBIQUEX_SOURCE_TOKEN` PAT was already
present at session start (`gh secret list` confirmed it, same as
UBI-113's repo) — item 7's "org-level policy/PAT already resolved"
check passed cleanly, nothing to ask for.

**Item 3's real ask, done for real, not trusted by analogy from Go/TS**:
`pypi.org/pypi/ubx-sdk/json` and `pypi.org/simple/ubx-sdk/` — the real
API endpoints, not the bot-challenge HTML page — both re-checked live
before deciding vendor vs. depend, both still genuine 404s as of this
session. `ubx-sdk` remains unpublished; vendored `vendor/ubx_sdk/`
(byte-identical to `sdk/py/ubx_sdk/__init__.py`, confirmed by `diff`),
same UBI-105/111 stopgap, now a THIRD Python repo carrying it.

**Item 4's explicit ask, done for real, not trusted by analogy from
Go/TS**: verified both of UBI-112's `ubiquex`-core fixes directly
against Python's own generated output —

1. **`NestedType`/structured-attribute translation**: generated
   `kubernetes_validating_admission_policy_v1` and inspected the actual
   Python output directly
   (`kubernetes/validating/admission_policy_v1.py`) — a full,
   correctly-typed, real recursion-depth-5 dataclass/`FieldSpec` tree
   (`AdmissionPolicyV1_Spec_MatchConstraints_ObjectSelector` down to
   `..._NamespaceSelector_MatchExpressions`). The fix lives entirely in
   `provider/schema.go`'s tfplugin6 translation layer, shared by every
   language ahead of any per-language template — Python needed zero
   template-level changes, same result as Go/TS.
2. **Bare-version-suffix filenames**: checked the full generated tree
   directly for any `v1.py`/`v2beta2.py`-style bare-version file — zero
   found; every versioned resource's own local name stays
   self-descriptive (`deployment_v1.py`, not `v1.py`), same
   `ir.ServiceAndLocalName` fix, same result as Go/TS.
   `kubernetes/config/map_v1.py` and `kubernetes/config/map_v1_data.py`
   correctly stay unfolded — neither local name is purely a version
   token.

**Item 5, the compiler-crash-class (UBI-98) and sibling-`_config`
collision (UBI-96/108) checks — done for real, specifically for
Python**: a real recursive `importlib.import_module` of all 119
generated modules (81 resource types + 37 service `__init__.py` + the
top-level package), zero errors — the actual verification bar this
project uses in place of a compiler, matching UBI-98/105/111's own bar.
Largest file `kubernetes/cron/job_v1.py` at 1,858 lines (Go's own
equivalent: 1,855 lines) — no language-specific blowup. The
sibling-`_config` collision class doesn't even have a candidate to be
immune to here: `hashicorp/kubernetes`'s real 81-type schema has zero
wire types ending in `_config` at all (checked directly against every
generated `wire_type=` string) — a genuinely different finding from
Go/TS's "collision exists but doesn't reproduce because of
namespacing," since Kubernetes carries no Google-Spanner-shaped
`<type>`/`<type>_config` pair in the first place.

**A genuinely new, Python-specific finding, not a repeat of any prior
language in this rollout**: Go's own tree needed a `default_/` service
directory (Go reserves `default` as a keyword, used in `switch`
statements). Python has no such reservation — `default` is neither a
hard keyword (`keyword.iskeyword`) nor a soft keyword
(`keyword.issoftkeyword`) — so this repo's `kubernetes/default/`
directory stays unmodified, unlike its Go sibling. Checked directly
(all 37 real service directory names against both keyword lists), not
assumed from AWS's own unrelated `lambda`-collides-with-Python finding
(UBI-98/105) — a keyword collision in one language's own reserved-word
list doesn't imply anything about a different directory name in a
different language.

**Real schema scale, confirmed independently of Go/TS's own count**: 81
resource types for `hashicorp/kubernetes@3.2.0`, same real count, 37
derived service packages — confirmed by direct generation, not assumed
to match just because it's the same provider version.

**Required verification, met for real, clean on the first dispatch**
(no `ubiquex`-core fix needed this session — both of UBI-112's fixes
have been on `main` since UBI-112/113, nothing left to discover):
seeded at `3.2.0`, one real version behind the real latest (`3.2.1`,
unchanged since UBI-112/113 — no new release in between). Initial seed
pushed directly to `main` (a brand-new empty repo, nothing to risk).
Dispatched run green on the first try:
https://github.com/Ubiquex/ubx-sdk-kubernetes-py/actions/runs/30857078322
— queried the real registry, found `3.2.1`, built `ubx` from
`ubiquex`'s real `main`, regenerated for real, ran a real recursive
import-check against the full regenerated 119-module tree, opened a
real PR — **https://github.com/Ubiquex/ubx-sdk-kubernetes-py/pull/1** —
a clean, deterministic `3.2.0`→`3.2.1` provenance-only bump (40 files,
78/-78 lines: 37 `__init__.py` `SOURCE_PROVENANCE` version stamps +
`VERSION` + `pyproject.toml`'s own embedded version string, zero
resource-schema content changes). Founder confirmed merging — merged;
`main` confirmed at `3.2.1` via the real GitHub API.

`docs/sdk.md` (this amendment) is the only `ubiquex`-repo change this
session — `go test ./...` untouched, trivially green (no core-package
fix needed, unlike UBI-112).

**UBI-103's rollout**: AWS and Google (all three languages each) and
Kubernetes (all three languages: Go/UBI-112, TS/UBI-113, Python/UBI-114)
now real and live — `hashicorp/helm` still deferred to its own future
ticket. Kubernetes' full Go/TS/Python row is complete. Remaining: Azure.

## Amendment (2026-08-03, UBI-115): `ubx-sdk-azure-go` — final row of the original four-provider rollout; the NestedType fix's generality claim resolved with a definitive negative, not a "tested and passed" positive — azurerm doesn't speak the wire protocol NestedType lives on

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-azure-go** (public)
— founder-created beforehand per the ticket's own scope (item 1),
confirmed via the real GitHub API (`pushedAt` matched `createdAt`,
genuinely empty) before anything else, then cloned locally. Its own
repo-scoped `UBIQUEX_SOURCE_TOKEN` PAT was already present at session
start (`gh secret list`) — item 6's "org-level policy/PAT already
resolved" check passed cleanly, nothing to ask for.

**Item 3, the ticket's own real, priority ask, done for real — resolved
with a definitive negative, not a positive**: checked directly against
the real launched `hashicorp/azurerm@5.0.0` binary, before assuming
anything: it negotiates **tfplugin protocol v5**, not v6 (confirmed via
`client.Provider.ProtocolVersion()` on the actual handshake result, not
inferred). `NestedType` — the Terraform Plugin Framework
structured/object-attribute wire encoding UBI-112's fix
(`provider/schema.go`) translates — exists only on
`tfplugin6.Schema_Attribute`; `tfplugin5.Schema_Attribute` has no
equivalent field at all (confirmed by reading both generated proto
structs side by side). A v5 session cannot carry a `NestedType`-encoded
attribute on the wire, structurally, regardless of whether azurerm's own
resource implementations internally use the Plugin Framework for some
resources (HashiCorp's own azurerm is known to be migrating pieces of
its SDKv2-era resources onto the Plugin Framework, same as many
providers) — the *provider server*, not the per-resource
implementation, decides which protocol version it registers, and this
one only ever registered v5 in the tested build. **Report: the fix's
generality claim is not exercised by this provider at this version, full
stop — not "checked and found zero real NestedType usage" (a claim about
schema content, which was Google's version-suffix finding's shape,
below), but "checked and found the wire mechanism this fix operates on
isn't even in play here."** This is a materially different, stronger
kind of finding than either "present and handled" (Kubernetes/UBI-112)
or "structurally immune by design" (TS/Python's per-file namespacing
immunity to the `_config` collision) — it's a protocol-negotiation fact
about this specific provider binary at this specific version, not a
schema-content fact or a codegen-architecture fact, and it could change
in a future azurerm release if HashiCorp ever registers v6 for it (the
same "protocol v6 only premise did not hold" finding from early in this
project, generalized one more time: assume nothing about which protocol
version any given provider binary will actually speak, re-check per
provider per version).

**Item 4, the other three collision classes, checked for real,
specifically for Azure, not assumed from any prior provider's
pattern**:

1. **Compiler-crash-class (UBI-98)**: confirmed absent. Largest real
   generated file: `azurerm/kubernetes/cluster.go`, 1,057 lines —
   nowhere near the pathological ~250,000-line scale that hit
   `aws_wafv2_web_acl_rule`.
2. **Sibling-`_config` collision (UBI-96/108)**: checked against the
   full real wire-name set (every one of the 1,103 real type names
   paired against `<type>_config`), not just inferred from a clean `go
   build` — zero collision candidates found. `go build ./...`/`go vet
   ./...` clean across the full tree either way.
3. **Bare-version-suffix-filename collision (UBI-112)**: azurerm has
   exactly 3 real version-suffixed wire names
   (`azurerm_app_service_environment_v3`,
   `azurerm_monitor_scheduled_query_rules_alert_v2`,
   `azurerm_stream_analytics_stream_input_eventhub_v2`), checked
   directly against the real schema — same shape as Google's one
   `google_apigee_security_profile_v2` instance, all three already carry
   more than one token ahead of the version marker, so none hit the
   fold's narrow bare-marker trigger. Zero bare `vN.go` files in the real
   generated tree.

**No new Azure-specific collision class found** — the ticket named this
as a real possibility ("azurerm has its own real naming conventions,
may have its own equivalent quirk"), checked explicitly, found none this
time.

**Real schema scale, not guessed**: **1,103 resource types** for
`hashicorp/azurerm@5.0.0`, 144 derived service packages — recalling
UBI-108's own lesson (Google's "likely 400-600" guess was badly wrong at
1,319), this number came from direct generation, never estimated.
Largest schema in the family after Google (1,330); AWS (1,682) is still
larger; Kubernetes (81) is far smaller.

**Zero `ubiquex`-core changes needed this session** — unlike Kubernetes
(UBI-112), which needed two real fixes at the source, Azure's real
schema hit no new schema-shape class the existing pipeline couldn't
already handle. `go test ./...` untouched, trivially green.

**Required verification, met for real, clean on the first dispatch**:
seeded at `5.0.0`, one real version behind the real latest (`5.0.1`,
confirmed live against `registry.terraform.io/v1/providers/hashicorp/azurerm/versions`
at seed time — note this rollout's first genuine major-version jump,
`4.x`→`5.0.x`, real and confirmed, not a version-string oddity). Initial
seed pushed directly to `main` (a brand-new empty repo, nothing to
risk). Dispatched run green on the first try:
https://github.com/Ubiquex/ubx-sdk-azure-go/actions/runs/30858026095 —
queried the real registry, found `5.0.1`, built `ubx` from `ubiquex`'s
real `main`, regenerated for real, ran a real `go build ./...`/`go vet
./...` against the full regenerated 1,103-type tree, opened a real PR —
**https://github.com/Ubiquex/ubx-sdk-azure-go/pull/1** — a clean,
deterministic `5.0.0`→`5.0.1` provenance-only bump (144 files, 287/-287
lines: every changed file confirmed by name to be a `doc.go` version
stamp or `VERSION`, zero resource-schema content changes — checked by
listing every changed file in the diff, not just trusting the line
count). Founder confirmed merging — merged; `main` confirmed at `5.0.1`
via the real GitHub API.

**UBI-103's rollout**: AWS, Google, and Kubernetes are real and live
across all three languages (Go/TS/Python); Azure's Go row (UBI-115) is
now live, its TS/Python siblings not yet started as of this session.
`hashicorp/helm` remains explicitly out of scope, deferred to its own
future ticket, as it has been since UBI-112 first named it.

**Correction (2026-08-03, caught in UBI-116)**: the paragraph above
originally read "twelve repos total... the original four-provider
rollout plan is done" — wrong at the time it was written, this session's
own start-of-session STATE.md read caught it before it propagated
further. Only Azure's Go row existed when UBI-115 closed (ten repos
total: AWS/Google/Kubernetes ×3 + Azure ×1), not all three Azure
languages. Left corrected here rather than silently rewritten, per this
project's own "flag docs errors, don't silently fix" discipline.

## Amendment (2026-08-03, UBI-116): `ubx-sdk-azure-ts` — Azure's TS sibling; UBI-115's v5-protocol finding confirmed identical for TS's own generation path; a real correction to this ticket's own premise (Kubernetes was the v6 outlier, not the norm — AWS/Google were v5 all along); a genuine regen-workflow bug found and fixed before merging

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-azure-ts** (public)
— founder-created beforehand per the ticket's own scope (item 1),
confirmed via the real GitHub API (`pushedAt` == `createdAt`, genuinely
empty) before anything else, then cloned locally. Repo-scoped
`UBIQUEX_SOURCE_TOKEN` PAT already present (`gh secret list`) — item 7's
"org-level policy/PAT already resolved" check passed cleanly.

**Item 4, the ticket's own real, priority ask — confirmed, then
corrected**: re-verified directly (not assumed from "schema-fetch is
shared code, so it must be the same") that `hashicorp/azurerm@5.0.0`
negotiates tfplugin **protocol v5** on the exact same code path
`--lang ts` generation uses — identical to UBI-115's own Go-side
finding, as expected, but checked live via a dedicated probe against the
real binary, not inferred. `deno check --frozen` ran clean across the
full regenerated tree with no v5-specific gap in the TS template path.

**A real, worth-recording correction to the ticket's own stated
premise**: the ticket text assumed "AWS/Google/Kubernetes may all have
been v6," framing this repo as "TS's first v5-only provider." Checked
directly against all four real provider binaries at their actual
pinned rollout versions, not trusted as background color:
`hashicorp/aws@6.54.0` → **v5**, `hashicorp/google@7.42.0` → **v5**,
`hashicorp/kubernetes@3.2.1` → **v6**, `hashicorp/azurerm@5.0.0` →
**v5**. **Kubernetes was the outlier, not the norm** — this repo is the
*third* v5-protocol TS repo in the rollout (after AWS-TS/UBI-104 and
Google-TS/UBI-109), not the first. The `NestedType` shape UBI-112 fixed
was always a Kubernetes-specific risk, never a TS-wide one — now
confirmed directly rather than left as an unchecked assumption embedded
in a ticket description. (This finding traces back to a 2026-07-10
surprise recorded early in this project, before the SDK rollout even
began: `terraform-provider-aws@6.54.0` was already known to report v5
even when v6 is requested — this session connected that dot forward
rather than re-discovering it from scratch.)

**Item 5, the other collision classes — wire-name-level facts,
language-independent, reconfirmed not just assumed from Go**:
compiler-crash-class (UBI-98) absent — largest file
`azurerm/kubernetes/cluster.ts`, 1,139 lines; sibling-`_config`
collision (UBI-96/108) and bare-version-suffix collision (UBI-112) —
both zero-candidate facts about the real wire-name set that don't change
per language, reconfirmed here by a clean `deno check --frozen` (a
collision would fail typechecking, not just `go build`) rather than
re-derived from scratch.

**Real schema scale, confirmed independently**: **1,103 resource
types** for `hashicorp/azurerm@5.0.0`, exactly matching Go's own count
— 144 derived service packages, 1,246 files.

**Runtime**: `jsr:@ubx/sdk@^0.1.0` from the initial commit, same as
Kubernetes-TS (UBI-113) — no vendoring stopgap, born directly on the
real JSR publish. Hit the same one-time JSR minimum-dependency-age gate
UBI-110/113 found (`--minimum-dependency-age=0` needed only to produce
the initial `deno.lock`; the real CI dispatch's own `deno cache`/`deno
check --frozen` ran with no override and passed, reconfirming "no
override needed once locked").

**A genuine bug found and fixed before merging, not shipped and
discovered later**: `ubx sdk gen`'s mechanical shortName derivation
writes `hashicorp/azurerm` → `azurerm` — matching UBI-115's own
already-known go.mod-module-path correction, package.json's `name`
field needed the same one-time correction (`@ubx/sdk-azurerm` →
`@ubx/sdk-azure`, matching this repo's real name, `ubx-sdk-azure-ts`).
Unlike go.mod (excluded from the regen `rsync` entirely, since its
module path is pure identity, never needs a version bump), package.json
can't simply be excluded — its `description` field carries a version
stamp that legitimately needs updating on every regen, the same as
`doc.ts`'s own stamp. The version-watch workflow, ported mechanically
from `ubx-sdk-kubernetes-ts` (whose own shortname never diverged from
its repo name, so this class of bug never had a chance to surface
there), silently clobbered the `name` field back to the wrong
mechanical value on its very first real dispatch — caught by diffing
the opened PR line-by-line rather than trusting the line-count summary,
exactly the same discipline that caught UBI-11's Linear-label mismatch
and UBI-90's stale-binary re-test earlier in this project. Fixed by
`sed`-correcting `package.json`'s name back in place after the `rsync`
step (mirroring the reasoning class of go.mod's fix, not its exact
mechanism); the first, buggy PR (#1) was closed and its branch deleted,
the workflow fix pushed to `main`, and the workflow re-dispatched to
produce a clean PR (#2) before anything was merged.

**Required verification, met for real — two dispatches, first caught a
real bug, second clean**: seeded at `5.0.0`, one version behind real
latest (`5.0.1`, unchanged since UBI-115). Pushed directly to `main`
(brand-new empty repo). First dispatched run
(https://github.com/Ubiquex/ubx-sdk-azure-ts/actions/runs/30859055971)
was green (CI-wise) but its PR #1 carried the `package.json` name
regression above — closed, not merged. Workflow fix pushed to `main`,
re-dispatched
(https://github.com/Ubiquex/ubx-sdk-azure-ts/actions/runs/30859286297),
green again, opened a real, clean, deterministic `5.0.0`→`5.0.1`
provenance-only bump PR —
**https://github.com/Ubiquex/ubx-sdk-azure-ts/pull/2** (145 files,
288/-288 lines: every changed file confirmed by name to be a `doc.ts`
stamp, `VERSION`, or `package.json`'s version-stamped `description` —
`name` field confirmed unchanged in the diff this time). Founder
confirmed merging — merged; `main` confirmed at `5.0.1` via the real
GitHub API.

**Zero `ubiquex`-core changes needed this session** — the fix lived
entirely in the external repo's own workflow file. `go test ./...`
untouched, trivially green.

**UBI-103's rollout**: AWS, Google, and Kubernetes real and live across
Go/TS/Python; Azure now real and live in Go (UBI-115) and TS (UBI-116).
Remaining: Azure's Python sibling — the one row left before the
original four-provider rollout plan is genuinely, fully complete.

## Amendment (2026-08-03, UBI-117): `ubx-sdk-azure-py` — Azure's Python sibling, final row of UBI-103's original rollout; the shortname/mechanical-name divergence bug checked and fixed BEFORE the first dispatch this time, not discovered via a bad PR diff

**Real repo**: **https://github.com/Ubiquex/ubx-sdk-azure-py** (public)
— founder-created beforehand, confirmed via the real GitHub API
(`pushedAt` == `createdAt`, genuinely empty) before anything else, then
cloned locally.

**Item 3, checked pre-emptively this time — the ticket's own explicit
instruction, learned the hard way in UBI-116**: ran `ubx sdk gen --lang
py` locally against `hashicorp/azurerm@5.0.0` before touching the real
repo at all, and confirmed directly that the generated `pyproject.toml`
carries the identical latent bug UBI-116 found in `package.json`:
`providerShortName` (`cli/sdk.go`) derives mechanically from the
source's own last `/`-segment (`"hashicorp/azurerm"` → `"azurerm"`),
so `pyproject.toml`'s `name` field comes out `ubx-sdk-azurerm`, not
this repo's real name, `ubx-sdk-azure`. Corrected in the initial commit
(`sed`) and, more importantly, baked into `version-watch.yml`'s
regeneration step from the start (the same `sed`-after-`rsync`
correction UBI-116 added to `package.json`, ported to `pyproject.toml`
before the workflow ever ran for real) — this is the whole point of
item 3's instruction: fix it before the first dispatch, not after a
bad PR diff.

**Item 5, the v5-protocol finding — reconfirmed directly for Python's
own generation path, not assumed from TS/Go**: a raw handshake against
the real `hashicorp/azurerm@5.0.0` binary
(`TF_PLUGIN_MAGIC_COOKIE`+`PLUGIN_PROTOCOL_VERSIONS=6,5`) returned
`1|5|unix|...|grpc` — **protocol v5**, identical to UBI-115/116's own
findings. Expected, since protocol negotiation happens once in
`provider/` — shared, language-independent code every `--lang` runs
through identically — but checked live via a dedicated probe rather
than inferred by analogy, same discipline as UBI-116.

**Item 6, the other collision classes — wire-name-level facts,
language-independent, reconfirmed not re-derived**: compiler-crash-class
(UBI-98) absent, largest file `azurerm/kubernetes/cluster.py`, 1,060
lines; sibling-`_config` collision (UBI-96/108) — 0/1,103 candidates
against the real generated local-name list, checked directly (not
inferred from Go/TS's own 0/1,103); bare-version-suffix collision
(UBI-112) — zero bare `v<N>.py`-style filenames anywhere in the tree.
Real recursive `importlib.import_module` of every generated module:
1,247 modules, zero errors.

**Item 4, runtime dependency check**: `pypi.org/pypi/ubx-sdk/json` and
`pypi.org/simple/ubx-sdk/` both still return 404 as of this session
(checked live, not assumed stale from UBI-114's own finding) — `ubx_sdk`
vendored into `vendor/ubx_sdk/` per UBI-105/111/114's precedent, byte-
identical to `sdk/py/ubx_sdk/__init__.py` in this repo (`ubiquex`),
resolved via `PYTHONPATH`.

**Real schema scale, confirmed independently**: **1,103 resource
types** for `hashicorp/azurerm@5.0.0`, exactly matching Go's and TS's
own counts — 144 derived service packages, 1,247 files.

**Item 8, org-level policy/PAT**: applied cleanly — the ported
`UBIQUEX_SOURCE_TOKEN` secret worked on the first real dispatch (private
`ubiquex` checkout succeeded inside the workflow run), no new grant
needed.

**Required verification, clean on the first dispatch — the pre-emptive
fix meant no second round was needed this time**: seeded at `5.0.0`,
one version behind real latest (`5.0.1`, unchanged since UBI-115/116).
Pushed directly to `main` (brand-new empty repo). Dispatched run green
first try
(https://github.com/Ubiquex/ubx-sdk-azure-py/actions/runs/30860199232),
found `5.0.1`, regenerated for real, the full 1,247-file tree re-
imported clean, opened a real, clean, deterministic `5.0.0`→`5.0.1`
provenance-only bump PR —
**https://github.com/Ubiquex/ubx-sdk-azure-py/pull/1** (146 files,
290/-290 lines, every changed file confirmed by name AND by diff
content to be a service `__init__.py` provenance stamp, `VERSION`, or
`pyproject.toml`'s version-stamped `description` — `name` field
confirmed unchanged, spot-checked across a random sample, not just the
line-count summary). Founder confirmed merging — merged; `main`
confirmed at `5.0.1` via the real GitHub API.

**Zero `ubiquex`-core changes needed this session** — `go test ./...`
untouched, trivially green.

**UBI-103's rollout: genuinely, fully complete.** AWS, Google,
Kubernetes, and Azure all real and live across Go/TS/Python — twelve
repos total (`ubx-sdk-{aws,google,kubernetes,azure}-{go,ts,py}`), plus
the two shared runtimes (`ubx-sdk-go`, and TS's `@ubx/sdk` on JSR).
Verified against the actual repo list before writing this, not asserted
from memory of the plan — the exact mistake UBI-115's own entry made
and UBI-116 had to correct.

## Amendment (2026-08-05, UBI-107): `ubx_sdk` genuinely published to PyPI — the Python half of UBI-107's own long-open runtime-publishing gap, closed for real; all four vendored Python bindings repos switched, live-verified, not just JS/Go's own already-closed halves

**Ticket re-read fully, including its own comment thread, per the
handoff's own explicit instruction — the thread itself corrects the
ticket's own title**: `@ubx/sdk` (TypeScript) already closed for real
via **JSR**, not npm (UBI-110's own amendment above has the full
account) — this session's own real, remaining scope was exclusively
Python's own half: `ubx_sdk` to PyPI, then switch every real vendored
Python bindings repo to depend on it for real, mirroring UBI-110's own
TS switch exactly.

**PyPI status re-checked live before touching anything, per this
project's own standing discipline**: `pypi.org/pypi/ubx_sdk/json`,
`pypi.org/pypi/ubx-sdk/json`, and both `/simple/` variants all still
real `404`s at session start — UBI-117's own last finding still held,
not stale.

**New, real packaging added — `sdk/py/pyproject.toml` (hatchling
backend) + `sdk/py/README.md`**, package name `ubx_sdk` (the ticket's
own explicit description names this exact PyPI project name), version
`0.1.0` matching `@ubx/sdk`'s own first-publish version. Built and
verified hermetically before any publish attempt: `python -m build`
(sdist + wheel), `twine check` clean, and — the real proof, not assumed
from a successful build — a genuinely fresh venv installing the built
wheel resolved `ubx_sdk.__file__` to its own `site-packages/`, not this
repo's source tree (the first naive check ran from `sdk/py/` itself and
was silently shadowed by cwd-on-`sys.path`; caught before trusting it,
re-run from `/tmp` to confirm for real).

**Real PyPI account/token setup — founder's own action, same division
of labor as every credential step in this project's history (JSR, npm
placeholder, GHCR)**: founder created a PyPI account, generated an
account-scoped API token (no project existed yet to scope it to
narrower), and handed it to this session directly rather than running
`twine upload` themselves. Used once, immediately, via a private temp
file outside the repo (never printed, never committed, shredded
immediately after the one `twine upload` invocation) — real upload
succeeded on the first attempt:
**https://pypi.org/project/ubx-sdk/0.1.0/**. Founder should follow up
by creating a project-scoped token for `ubx_sdk` specifically and
revoking the account-wide one, now that the project exists (flagged,
not done automatically — token rotation is the founder's own call).

**Real live confirmation, the ticket's own success bar**: `pypi.org/pypi/ubx_sdk/json`
now returns `{"info": {"name": "ubx-sdk", "version": "0.1.0"}}` (PyPI's
own PEP 503 name normalization displays the project as `ubx-sdk`;
`ubx_sdk`/`ubx-sdk` are the same normalized project and both resolve).
A genuinely fresh venv, `pip install ubx-sdk`, real import, real
`Computed` construction and attribute-drilling — all against the real
published package, zero local source on `sys.path`.

**The actual source-of-truth fix, found before touching any external
repo — `sdk/codegen/templates/py/py.go`'s own `pyprojectTOML` template
was still emitting an unpinned, aspirational `dependencies = ["ubx_sdk"]`
with a doc comment reading "were it ever published; it isn't"**: fixed
at the source (now `dependencies = ["ubx_sdk>=0.1.0,<0.2.0"]`, a
caret-equivalent range matching `@ubx/sdk`'s own `^0.1.0` JSR pin,
comment corrected) rather than hand-patching each external repo's
`pyproject.toml` independently — every FUTURE `ubx sdk gen --lang py`
regeneration (including UBI-99's own automated version-watch bumps)
now emits the correct pin automatically, closing the drift risk UBI-107's
own comment thread named as the whole reason NOT to let vendoring scale
past the first repo.

**All four real vendored Python bindings repos switched, same
mechanical change each time, mirroring UBI-110's own TS switch
exactly**: `ubx-sdk-aws-py`, `ubx-sdk-google-py`, `ubx-sdk-kubernetes-py`,
`ubx-sdk-azure-py` — confirmed to be the FULL real list via the GitHub
API (`gh repo list Ubiquex`), not assumed from the ticket's own
enumeration (which turned out to be exactly right, no additional
provider landed since). Per repo: `pyproject.toml`'s dependency pinned
to the real range; `vendor/ubx_sdk/` removed entirely;
`.github/workflows/version-watch.yml`'s sanity-check step now installs
the real dependency (reading the pin straight out of `pyproject.toml`
via `tomllib`, so the workflow can never drift from the declared range)
instead of inserting `vendor/` onto `sys.path`; `pyproject.toml` is no
longer excluded from the regeneration `rsync` (it's real codegen output
now that the template is fixed); `README.md` updated to describe the
real dependency. A real, live-found gotcha along the way, not assumed
from the plan: `pip install .` on any of these repos fails outright —
setuptools' own flat-layout auto-discovery correctly refuses to build a
single distribution out of 100+ top-level service-directory packages
(`s3/`, `iam/`, `lambda_/`, ...) — these repos were never meant to be
`pip install`-able as one package (consumed via `PYTHONPATH` against
the source tree directly, same as before); only the runtime dependency
itself needs installing, not the repo.

**Live verification, per repo, real dispatched CI runs, not assumed
from a green local check**: `ubx-sdk-aws-py`
([PR #3](https://github.com/Ubiquex/ubx-sdk-aws-py/pull/3), dispatched
run [31046850652](https://github.com/Ubiquex/ubx-sdk-aws-py/actions/runs/31046850652),
green — this repo's own `VERSION` (6.54.0) was genuinely behind the real
registry latest (6.58.0), so the dispatch naturally exercised the full
regenerate→install→sanity-check path); `ubx-sdk-google-py`
([PR #2](https://github.com/Ubiquex/ubx-sdk-google-py/pull/2), run
[31047255384](https://github.com/Ubiquex/ubx-sdk-google-py/actions/runs/31047255384),
green, same natural version-behind trigger). `ubx-sdk-kubernetes-py` and
`ubx-sdk-azure-py` were BOTH already at the real registry's own latest
version (3.2.1, 5.0.1) — a plain dispatch on the switch branch would
have no-op'd before ever reaching the new steps, so each got a second,
disposable branch (`sdk-switch/real-ubx-sdk-pypi-verify-dispatch`) with
`VERSION` rolled back to the immediately-prior REAL published version
(3.2.0, 5.0.0 — confirmed real via the registry API before using them,
not guessed) purely to force the `newer=true` path, mirroring UBI-110's
own "disposable branch forked from the real PR branch" technique
exactly: `ubx-sdk-kubernetes-py`
([PR #2](https://github.com/Ubiquex/ubx-sdk-kubernetes-py/pull/2), run
[31047661327](https://github.com/Ubiquex/ubx-sdk-kubernetes-py/actions/runs/31047661327),
green); `ubx-sdk-azure-py`
([PR #2](https://github.com/Ubiquex/ubx-sdk-azure-py/pull/2), run
[31048009382](https://github.com/Ubiquex/ubx-sdk-azure-py/actions/runs/31048009382),
green). Every disposable side-effect PR the dispatches opened (real
regeneration bot PRs, an unavoidable side effect of exercising
`version-watch.yml` for real) was closed with an explanation comment,
never merged — same UBI-106/UBI-110 precedent. None of the four real
switch PRs (#3/#2/#2/#2 above) were merged either — left for the
founder to review, same protocol as every prior repo in this rollout.

**UBI-130 orthogonality, checked directly, not assumed from the two
tickets' similar-sounding names**: UBI-130 (Python blueprint DEPENDENCY
resolution — `requirements.txt`'s own `<name> @ <url>` syntax, resolving
a separately-published BLUEPRINT package at `ubx plan`/`ubx resolve`
time) is genuinely unaffected by this ticket. `blueprint/pydeps.go`'s
own `PyDependency` parser only ever interprets `"@ url"` lines; a plain
`ubx_sdk` line (no `@`) is left alone entirely, and more fundamentally,
`ubx_sdk` is never resolved through UBI-130's mechanism at all inside
`ubx plan`/`ubx resolve --from-code`'s own WASI sandbox — `pyeval`
embeds it directly (`sdk/py/embed.go`'s own `go:embed`), independent of
PyPI publish status, both before and after this ticket. The real PyPI
package matters only OUTSIDE that sandbox — the standalone generated
`ubx-sdk-*-py` bindings repos this amendment switches. Both
`blueprint/pydeps.go` and `sdk/py/embed.go`'s own doc comments (stale
"not published to PyPI yet" framing, predating this ticket) corrected
to state the real current status and this exact non-interaction,
per this project's own "never contradict a doc silently" discipline —
no behavior changed in either file, comment-only.

**Zero `ubiquex`-core behavior changes beyond the codegen template
fix** — `go build ./...`/`gofmt -l .`/`go vet ./...` clean; full
`go test ./...` green (including `sdk/codegen/templates/py`'s own
existing suite, confirming the `pyprojectTOML` pin change didn't break
any exact-string assertion — none existed). `sdk/py/ubx_sdk`'s own
hermetic `python3 -m unittest` suite (18 tests) re-run directly,
unaffected (zero runtime code changed, only packaging metadata added
alongside it).

Docs-first, per protocol: this document's own up-front "Runtime package
publish status" summary (above) corrected in place, not just amended at
the bottom, so a new reader hits the accurate status first — matching
UBI-100's own standing policy for this exact document. `ubiquex-docs`
updated in the same session (see its own commit for the guide-facing
account).

## Amendment (2026-08-10): Python namespace-package layout — `ubx.<provider>`, real precedent (`google.cloud.*`, `azure.mgmt.*`), fixes the `aws.alb.alb` file-stutter

No Linear ticket ID given in this session's handoff — per CLAUDE.md's
own rule, none inferred and none referenced here.

**The founder's own finding**: `ubx-sdk-aws-py`'s real, already-live
package (0.1.0, genuinely published to PyPI as `ubx-sdk-aws`, see the
UBI-107 amendment above for how that publish happened) had a package
root of bare `aws/` — `from aws.alb.alb import Alb, AlbConfig`. Two
real problems named together: (1) `aws` as a bare top-level import name
has no room for `ubx-sdk-google-py`/`-azure-py`/`-kubernetes-py` to
each contribute their own sibling later without every provider
colliding in the SAME flat namespace; (2) `aws.alb.alb` repeats the
resource-file's own name twice (module path `alb.alb`, since
UBI-98/UBI-106's per-resource-type file is named after the resource
itself) — a real, visible awkwardness real strong precedent
(`google.cloud.storage.Bucket`, `azure.mgmt.compute.ComputeManagementClient`)
doesn't have, because those libraries' own `__init__.py`s re-export.

**Both fixed together, at the source (`sdk/codegen/templates/py/py.go`),
not hand-patched in the live repo alone**:

1. **A shared `ubx` PEP 420 implicit namespace package root.**
   `GeneratedRepo` now emits `ubx/<ns>/...` instead of `<ns>/...`, and
   deliberately never writes `ubx/__init__.py` — a directory WITH an
   `__init__.py` is a *regular* package, ownable by exactly one
   distribution; a directory WITHOUT one, discovered via
   `[tool.setuptools.packages.find]`'s new `namespaces = true`, is a
   real PEP 420 implicit namespace package multiple independently
   installed distributions can each contribute a piece of. Confirmed
   this is the correct mechanism, not just asserted: built the real
   wheel and inspected it directly — `ubx/aws/__init__.py` present,
   `ubx/__init__.py` absent, `top_level.txt` declares `ubx`. This
   directly supersedes the prior UBI-106 amendment's own claim (above,
   now cross-referenced with a pointer at that exact paragraph, per
   this document's own "never rewrite historical prose, add a
   superseding pointer" policy) that Python's namespace segment "is not
   relying on PEP 420 implicit namespace packages" — that claim was
   correct at the `aws/` level itself; the new `ubx/` level above it now
   does rely on PEP 420, deliberately.
2. **Service `__init__.py` re-exports its own resource classes.** A new
   `ServicePackageDoc` (replacing a bare `PackageDoc` call for service
   packages specifically — `PackageDoc` itself is unchanged, still used
   for the namespace-root `ubx/<ns>/__init__.py`) emits one `from
   .<local> import <Pascal>, <Pascal>Config` line per resource type in
   that service, so the real, final import is `from ubx.aws.alb import
   Alb, AlbConfig` — never the `ubx.aws.alb.alb` stutter.

**A real, new collision class this re-export aggregation introduces,
found and closed before it could ship silently broken, not assumed
safe by analogy to Go/TS**: two different resource types in the SAME
service whose local (file-basename) names `pascalCase` identically
would silently shadow each other in one shared `__init__.py` — Python
raises no error for `from .a import Foo` followed by `from .b import
Foo`, structurally analogous to UBI-96's own original flat-module
collision bug, and to the `*_config` collision UBI-108 found in Go's
single-package-per-directory namespace (STATE.md). `duplicates.go`'s
own `CheckNoDuplicateDeclarations` gained a third regex
(`fromImportRe`) checking every re-export line uniformly alongside the
two it already checked (`class X:` / `X = sdk.ResourceBinding(`) — a
new hermetic test (`TestCheckNoDuplicateDeclarations_ReExportCollision`)
proves it fires; the real, live check that matters more, run against
the actual full `hashicorp/aws@6.57.1` schema (`UBX_CONFORMANCE_LIVE=1`,
1,687 real resource types, `TestFullProvider_Py_ImportsClean`), found
**zero real collisions** — 1,942 files across 259 service packages, all
importing clean.

**Regenerated for real, not hand-edited**: `ubx-sdk-aws-py`'s own
`aws/` tree was fully replaced by a real `ubx sdk gen --lang py --out`
run against the same pinned `hashicorp/aws@6.57.1` (`VERSION`
unchanged) — 1,947 real generated modules, confirmed importing clean
both via a direct recursive `importlib` sweep and via a real
`python3 -m build` + fresh-venv `pip install` of the built wheel
(`from ubx.aws.alb import Alb, AlbConfig` succeeds; `ubx_sdk` resolves
from the real PyPI index as an ordinary dependency). Version bumped
0.1.0 → **0.2.0** (a real, deliberate breaking change to every existing
import statement — more than a patch bump). Landed as
[PR #7](https://github.com/Ubiquex/ubx-sdk-aws-py/pull/7), merged to
`main` this session (not left open for later review — the founder's
own handoff was an explicit, itemized, in-session directive to land and
publish, a different posture from the automated weekly regen sweeps
this repo's own prior amendments describe, which stay unmerged for
founder review by design).

**`ubiquex`-core changes**: `sdk/codegen/templates/py/py.go`
(`GeneratedRepo`, `pyprojectTOML`, new `ServicePackageDoc`/
`exportedName`), `duplicates.go` (`fromImportRe`), and every existing
test in `sdk/codegen/templates/py` and `cli/sdk_test.go` asserting an
exact `aws/...`-rooted path or dotted-import string, updated to the new
`ubx/aws/...` shape — `go build ./...`/`gofmt -l .`/`go vet ./...`
clean, full `go test ./...` green, `UBX_CONFORMANCE_LIVE=1` full-schema
test green (above).

**Deliberately not done this session, per the founder's own explicit
instruction**: `ubiquex-docs`' existing per-resource Python-tab
examples are NOT updated here — the founder wants to review and update
those personally once this landed live, and separately wants to decide
whether TS's already-namespaced `@ubx/sdk-aws` import shape deserves a
similar consistency pass. Flagged back directly, not silently deferred.

**Google/Azure/Kubernetes's own Python repos are NOT yet switched to
this layout** — only `ubx-sdk-aws-py` was in this session's explicit
scope. The codegen fix is real and applies to every future regen of
any provider automatically (same "fix once at the source" precedent as
UBI-106/UBI-108), but the other three live Python repos' `main`
branches still serve the OLD `<ns>/<service>/<local>.py`-with-no-
re-export shape until their own version-watch regen (or a dedicated
follow-up session) runs against the now-fixed codegen — a real,
named, not-yet-closed gap, not silently assumed covered by "the
codegen is fixed."
