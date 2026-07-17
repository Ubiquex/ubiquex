# Resolver v1 — typed intent → resolved change proposals (UBI-27)

> This document is the spec, written before any code. It lifts v1's real
> type-system and graph RULES as design — not v1's code, which this
> project's own architecture doc (`docs/architecture.md`'s "What carries
> over from v1") already expected to need a 40–60% rewrite for
> ledger-shaped pressures. docs/schema.md's own amendment pins the
> intent-file wire format and the `Delta.Creates` IR-node shape this
> resolver produces; docs/executor.md's own amendment pins how a resolved
> `$computed` value becomes a real tfplugin unknown at apply time.
> docs/resolver-adversarial.md pins the required-outcome program.

## Scope: `change` proposals, creates + modifies, no destroys

v1 of the resolver takes a hand-written, machine-shaped **intent file**
(`ubx:intent/v1`, docs/schema.md's own new amendment) and produces a
resolved `kind: "change"` proposal — `core.KindChange`, already a legal
enum value in this codebase since the very first schema draft, never
actually produced by anything until now. In scope: `delta.creates` (new
resources) and `delta.modifies` (declarative updates to resources the
ledger already has). **Explicitly out of scope: `delta.destroys`.** A
`change` proposal this resolver produces must have zero destroys, always —
this is a v1 scope line, not a technical limitation; destroys need their
own adversarial thinking (a create can be retried safely, a destroy
usually can't) that's real future work, not this ticket's.

Also out of scope, named so it isn't assumed covered: diagram/markdown/SDK
frontends (docs/architecture.md's component map #7 — "Authoring
frontends"), an intent provider (LLM-authored intent, component map #10's
neighbor), and a real policy engine (component map #9) — the resolver's own
contract names a policy-stub hook (below) purely so nothing about its
shape has to change once one exists.

## A real, honest correction before any design: where "v1" actually lives

CLAUDE.md and docs/architecture.md both say "v1 XCL typechecker" and cite
its graph algorithms as something to keep. Checked directly rather than
assumed: `/Users/roozbeh/Ubiquex/xcl` (the repo literally named `xcl`) is
**only** a lexer/parser/AST/formatter — its own README says so explicitly
("no infrastructure semantics... those live in the `ubx` tool") — and
confirmed empirically (`grep`, not guessed): no typechecker, no graph, no
`Computed`/`Pending` anywhere in that repo. The real type system and graph
algorithms live in a **separate** repo, `/Users/roozbeh/Ubiquex/ubx`
(`internal/xcl/typechecker`, `internal/xcl/ir`, `internal/xcl/scope`,
`internal/xcl/crossstack`, `internal/xcl/workspace`) — a different,
Pulumi-targeting compiler product, not `ubiquex-cli` and not `xcl`. This
document lifts rules from *that* repo. Worth stating plainly since the
docs pointed at the wrong one by name.

## What v1 actually does, and what it doesn't (checked directly)

v1 has no `Pending<T>`/`Computed<T>` wrapper type at all. Every named
symbol is tagged with a binary `ValueKind` (`scope/scope.go`:
`Resolved` | `Pending`) that flows through reference resolution — a
resource output, a module output, a remote/data-source output, and a
non-`env`-backend `secret()` are always `Pending`; everything else
(inputs, locals, literals, an `env`-backend secret) is `Resolved`. The
typechecker hard-errors if a `Pending` value is used somewhere that
requires `Resolved` — v1's own instance of this is narrow (`when`
conditions and `extend` conditions must be `Resolved`) but the *rule shape*
— "a value's known-now/known-after-apply status is tracked, and using a
known-after-apply value somewhere that needs known-now is a hard error,
not a guess" — is exactly `docs/schema.md`'s own `$computed` marker
convention, drafted since this project's founding session and never
implemented until now.

Two real gaps found in v1, not carried forward as-is:

- **v1's intra-stack (single-stack) resource graph has no cycle
  detection at all** (`internal/xcl/ir/build.go`'s `topoSort` is a plain
  DFS with a `visited` set — it silently produces *some* order for a
  cyclic graph rather than detecting the cycle; only the *workspace-level*,
  multi-stack graph (`internal/xcl/workspace/workspace.go`'s `TopoSort`)
  actually detects cycles, via a `path`/`inStack` DFS that reports the full
  cycle on first detection). v1's own multi-stack detector is the pattern
  worth keeping; its single-stack one is the gap this resolver closes for
  real (see "Intra-stack dependency graph," below).
- **v1 has no pinning or staleness concept for cross-stack refs at all.**
  `@stack.output` resolves against whatever a sibling *directory*'s `.xcl`
  files currently say, live, at compile time — there is no version, no
  head, no "this went stale because the neighbor changed" detection
  anywhere in v1. `pinned_head` and neighbor-advance staleness
  (docs/architecture.md's own "Cross-stack refs" core concept, already
  named at founding) are a deliberate improvement this resolver makes
  real, not a port.
- **v1 has no double-run/determinism check at all** — "determinism" in v1
  means "sort your maps before iterating them" by convention, unenforced.
  `core.DoubleRun` (built for canonical hashing, UBI-1-era) already exists
  and is reused here unchanged — a real improvement, not a port.

Two things v1 gets right, that this resolver keeps: the *shape* of the
`ValueKind`/`Pending` rule (state it once, check it everywhere a value is
consumed, hard error otherwise) and the *shape* of cross-stack discovery
being a simple, explicit convention rather than a fancy registry — v1's
sibling-directory-by-name is exactly that; this resolver's own explicit
`ledger_dir` field (below) is the same kind of simple, explicit answer to
"where do I find that."

## The resolver's contract

```
(intent file, live state via core.StateReader, provider schema, policy-stub hook)
  → resolved Delta (creates + modifies, dependency-ordered)
  , deterministic (core.DoubleRun), never silently guessed
```

- **Live state** comes in exactly the way every other reader in this
  codebase gets it — `core.StateReader` (already built for
  `RunScan`/`VerifyFreshness`), never a new read path.
- **Provider schema** is consulted only for what the resolver actually
  needs to decide (is this attribute `Computed`? Is it `Sensitive`? Does
  this type exist at all?) — never for anything `core` doesn't already
  know how to stay agnostic about. See "Schema boundary," below, for why
  this can't just be `*provider.Schema` directly.
- **Policy-stub hook**: a resolved delta passes through an optional
  `[]InvariantCheck`-producing hook before being returned — v1 (this
  ticket) always gets an empty slice back (no real policy engine exists
  yet, component map #9), but the resolver's own signature includes the
  hook now so nothing about its shape needs to change once one does.
- **Deterministic**: the whole resolve function runs through
  `core.DoubleRun` — call it twice, require byte-identical canonical
  output, hard-fail otherwise. Reused, not reinvented (see "What v1
  doesn't do," above).

## Schema boundary: `core/resolver` stays provider-import-free

`core` does not import package `provider` — a deliberate, load-bearing rule
since UBI-7/UBI-23, and `core/executor` (UBI-26) kept it by making its own
`Applier` interface's schema handles `any`-typed, with the concrete
`*provider.Schema` type assertion happening only in `cli/stateadapter.go`,
the one place that needs both packages. `core/resolver` follows the exact
same shape, not a new pattern:

```go
// SchemaInspector is core/resolver's own minimal view of "something that
// knows a provider's schema" -- exactly the three questions resolving a
// change proposal ever needs answered, never the concrete *provider.Schema
// itself.
type SchemaInspector interface {
    HasType(typeName string) bool
    IsComputed(typeName, attrPath string) bool
    IsSensitive(typeName, attrPath string) bool
}
```

A concrete adapter (`cli`, alongside `stateReaderAdapter`) implements this
against a real `*provider.Schemas` dump. `core/resolver`'s own hermetic
tests implement it against a small fake — no real provider binary needed
to test type rules, graph logic, or determinism.

## Intra-stack refs: the dependency graph comes home, with a fix

A `$ref` in an intent file's config (docs/schema.md's amendment) names
another resource in the *same* intent file/proposal batch, or an
already-ledgered resource this batch doesn't touch. Two cases, resolved
differently:

1. **Ref to a sibling create in this same batch.** Look up whether the
   referenced attribute path is `Computed` (`SchemaInspector.IsComputed`,
   e.g. `id`/`arn`, an attribute the intent author could never have
   supplied a literal for). If computed: mark `$computed` with `from` set
   to the referenced resource's canonical address + path
   (`<stack>.<type>.<name>.<attr-path>`, docs/schema.md's own existing
   `$computed` shape, unchanged) — and add a dependency-graph edge (this
   resource depends on the referenced one). If *not* computed (the intent
   author gave the sibling a literal value for that path directly):
   substitute that literal value directly — no `$computed` marker at all,
   since the value is already fully known at resolve time. Both cases
   record the same dependency edge, for order (see below) — a ref to an
   already-known literal costs nothing to make explicit.
2. **Ref to an already-ledgered resource not in this batch.** Resolved
   against `core.Ledger.FoldState(addr)` — always concrete (an existing
   resource's folded state is, by definition, already fully resolved; this
   is the exact same posture `drift_revert`'s own restore values already
   have, UBI-26). Never `$computed`.

`$ref` never appears in the *resolved* output — it is purely an
intent-file-side notation the resolver walks and replaces, either with a
concrete value or a `$computed` marker. A resolved `Delta` contains only
concrete/`$computed`/`$secret`/`$ephemeral` values, never an unresolved
reference marker; "resolved" means exactly that.

### The dependency graph and real cycle detection

Every ref (to a sibling create) is an edge; the resolver builds this graph
and topo-sorts it before emitting `delta.creates` — **in dependency order,
not the `(stack, type, name)` lexicographic order canonical hashing already
uses.** These are two different orderings for two different purposes, and
neither changes the other: `core.canonicalProposalBytes`'s own
`sortDeltaElements` re-sorts a *transient decoded copy* purely for hashing
(docs/schema.md's ratified rule, unchanged, no `schema_version` bump) —
the *stored* `delta.creates` array order is what the executor actually
walks, and for a `change` proposal (unlike `drift_revert`, where order
never mattered because every resource's restore value was already fully
known) that order is genuinely meaningful: a dependent must be created
after what it depends on.

Cycle detection is new, real code — v1's own single-stack `topoSort` never
had it (see above). The DFS pattern to borrow is v1's *workspace-level*
one (`path`/`inStack`, report the full cycle on first detection), applied
one level down (per-proposal resource graph, not per-workspace stack
graph): a cycle is a hard resolve-time error naming the full cycle path
(`aws_instance.a → aws_instance.b → aws_instance.a`), never silently
broken or arbitrarily ordered.

## Cross-stack refs: pinned against a real ledger, for real

A `$cross` marker names `{stack, ledger_dir, path}` — `ledger_dir` an
explicit filesystem path to the neighbor stack's ledger directory (the
same convention `--ledger-dir` already uses for the local one; docs/schema.md
"Open questions" still lists a fancier "cross-stack workspace index format"
as unresolved — this stays exactly as unresolved as before, an explicit
path is v1's own honest, sufficient answer, matching v1 XCL's own
"simple, explicit convention over a fancy registry" instinct, not a
resolution of that open question).

Resolution: `core.Open(ledger_dir).FoldState(addr)` — the neighbor's own
already-applied, already-concrete truth, exactly like an intra-stack ref to
an existing resource. **`pinned_head` is recorded**: the neighbor ledger's
`Head()` at resolve time, carried into the resolved delta (a new field —
see docs/schema.md's amendment) — this is what activates neighbor-advance
staleness for real: if the neighbor's head has moved by the time this
proposal is accepted, re-verifying `pinned_head` against the neighbor's
current `Head()` catches it, the same "resolved-time truth vs. accept-time
reality" staleness shape `VerifyFreshness` already established for live
state, one level up (a ledger, not a cloud resource).

A cross-stack ref is concrete almost always — **unless the neighbor's own
`FoldState` at that exact path is itself a `$computed` marker** (the
neighbor stack has its own accepted-but-not-yet-shipped `change` proposal
whose create used `$computed` there). In that case the marker propagates
forward into this stack's own resolved delta unresolved — honest, not
guessed; this is a real, if rare, edge case worth naming precisely rather
than assuming cross-stack always means concrete (v1's own model, by
contrast, treated *every* cross-stack ref as `Pending` — always
"computed," never concrete — because v1 resolved against a live,
unapplied sibling source, not an already-applied ledger; this is a real
and precise difference, not accidental phrasing).

## `$computed`: never guessed, never silently concretized

Exactly docs/schema.md's own existing (drafted at founding, never
implemented) convention: `{"$computed": {"from": "<address>.<path>"}}`.
The resolver never invents a concrete value for something it cannot
actually know yet — an attribute is `$computed` if and only if
`SchemaInspector.IsComputed` says so for that exact path, checked, never
assumed from the attribute's name. A `$computed` value used somewhere a
concrete value is structurally required (an identity/lookup field; a
`when`-clause-shaped policy predicate, if one existed yet; a field a
*different* resource's config needs to interpolate a string around, not
just pass through whole) is a hard resolve-time error — the direct lift of
v1's own `Pending`-in-a-`Resolved`-context rule, generalized from "when
conditions" (v1's only instance of it) to "wherever this resolver's own
schema-driven rules require a concrete value."

## Secrets: a real check v1 never had

`$secret` (docs/schema.md's existing marker) may only be placed at a
config path `SchemaInspector.IsSensitive` confirms the real provider
schema flags `Sensitive` for that exact attribute. v1 had no equivalent
check at all — no `Secret<T>` type, nothing preventing `secret(...)` from
flowing into an arbitrary field (confirmed directly against v1's own
typechecker: the only secret-related validation is a backend-name
warning). This resolver's version is new, real validation, grounded in
infrastructure this project already built for a different reason
(`provider.Schema.Attribute.Sensitive`, UBI-23/24's own redaction work) —
a secret ref in a non-`Sensitive` field is a hard resolve-time error, not a
warning.

## Ephemeral

`$ephemeral` (docs/schema.md's existing marker: `{"$ephemeral": true, ...}`)
carries over v1's own posture directly: excluded from persisted state, not
a type distinct from `Resolved`/`Pending`/computed-ness — a value can be
concrete-and-ephemeral or `$computed`-and-ephemeral; ephemeral is an
orthogonal "don't persist this" flag layered on top of whatever else is
true about the value, exactly as v1's own `IRInput.Ephemeral` boolean
worked alongside (not instead of) `ValueKind`.

## `op: create | modify` — explicit, not inferred

Each intent-file resource entry declares `op: "create" | "modify"`
explicitly (docs/schema.md's amendment) — **not** inferred from whether
the address already exists in the ledger. This was a real design choice,
not the obvious one: inferring create-vs-modify from ledger presence
(mirroring exactly how `ubx scan` already classifies `ScanNew`/`ScanDrifted`)
was the first design considered, and rejected, because
docs/resolver-adversarial.md's own required program names "modify intent
whose target isn't in the ledger" as a distinct error case — which is only
a *catchable authoring mistake* if `op` is a real, separately-stated claim
the resolver can check against reality, not something the resolver itself
silently decides. Validated at resolve time: `op: "create"` requires the
address to be absent from `FoldState`; `op: "modify"` requires it to be
present. Either mismatch is a hard resolve-time error.

`op: "modify"` supplies the resource's full desired end-state config (not
a before/after diff the author computes by hand) — the resolver diffs it
against the ledger's own `FoldState`-reconstructed current config via the
existing `diffAttributes` (already shared by `GenerateProposal`/
`GenerateRevertProposal`), producing `Modification.Before`/`After` the same
way drift detection already does. One mechanism, three callers now, not
three.

## Determinism

The whole resolve function — ref resolution, graph build, topo-sort,
`$computed`/`$secret` marking, the policy-stub hook — runs through
`core.DoubleRun` exactly once, at the top, the same "any evaluator feeding
hashed content goes through this" posture canonical hashing already
established. A resolver whose own logic isn't actually deterministic
(unsorted map iteration leaking into output order, say) fails hard at
resolve time, never silently produces an unstable hash later. See
docs/resolver-adversarial.md's own row for this.

## Out of scope for v1, named so it isn't assumed covered

- `delta.destroys` (see Scope, above).
- A real policy engine — the hook exists, always returns empty for now.
- Diagram/markdown/SDK/LLM-authored intent frontends — the intent-file
  format is deliberately machine-shaped for exactly this reason (a pretty
  frontend emits this JSON; it never needs to be hand-typed by an end
  user in production, only by this ticket's own sessions).
- A real cross-stack workspace index/registry (`ledger_dir` stays
  explicit, per-ref).
- Anything about *shipping* a resolved `change` proposal's `$computed`
  values as real tfplugin unknowns — that's docs/executor.md's own
  amendment, a distinct concern from resolving them.
