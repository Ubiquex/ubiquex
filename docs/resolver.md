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

## Scope: `change` proposals, creates + modifies, ~~no destroys~~

v1 of the resolver takes a hand-written, machine-shaped **intent file**
(`ubx:intent/v1`, docs/schema.md's own new amendment) and produces a
resolved `kind: "change"` proposal — `core.KindChange`, already a legal
enum value in this codebase since the very first schema draft, never
actually produced by anything until now. In scope: `delta.creates` (new
resources) and `delta.modifies` (declarative updates to resources the
ledger already has). ~~Explicitly out of scope: `delta.destroys`.~~ A
`change` proposal this resolver produced had to have zero destroys, always
— a deliberate v1 scope line, not a technical limitation; destroys needed
their own adversarial thinking (a create can be retried safely, a destroy
usually can't) before being taken on. **That design now exists**: see
"Amendment (2026-07-17, UBI-30): destroys," below — the design is pinned,
but resolver *code* implementing it is still session 2+ work of that same
ticket (this document is amended docs-first, per this project's own
session protocol, before any of it is built).

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
Pulumi-targeting compiler product, not `ubiquex` and not `xcl`. This
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
// knows a provider's schema" -- exactly the questions resolving a
// change proposal ever needs answered, never the concrete *provider.Schema
// itself.
type SchemaInspector interface {
    HasType(typeName string) bool
    IsComputed(typeName, attrPath string) bool
    IsSensitive(typeName, attrPath string) bool
    UnknownConfigKeys(typeName string, config map[string]interface{}) []ConfigKeyIssue
}
```

A concrete adapter (`cli`, alongside `stateReaderAdapter`) implements this
against a real `*provider.Schemas` dump. `core/resolver`'s own hermetic
tests implement it against a small fake — no real provider binary needed
to test type rules, graph logic, or determinism.

### Amendment (2026-08-01, UBI-66): schema-key validation at resolve time

`SchemaInspector.UnknownConfigKeys` is a later addition (the interface above
already shows it in place, not as a diff) — added because a real live run
(Claude Haiku) drafted plausible-sounding, entirely fictional attribute
names (`repository_name` for `aws_ecr_repository`'s real `name`; `role_name`
and `assume_role_policy_document` for `aws_iam_role`'s real `name` and
`assume_role_policy`; `queue_name` for `aws_sqs_queue`'s real `name`), and
**nothing in the pipeline caught any of them before a real
`ApplyResourceChange` call**.

**Confirmed empirically, not assumed, why the two existing validation
layers upstream of resolve never had a chance to catch this**
(`intentprovider/schema.go`/`validate.go`, docs/intent-provider.md's own
"Structured-output validation" section): a resource's own `config` is typed
as an opaque JSON-encoded **string** in the structured-output JSON Schema
handed to an adapter (`IntentDraftJSONSchema`) — deliberately, since a
JSON-Schema object node must declare a closed shape, and `config`'s real
shape is different per resource *type*, unknowable to that schema. So the
API-level constraint has no way to see inside `config` at all, let alone
check per-type attribute names against a real provider schema. `ubx`'s own
second validation layer (`parseAndValidate`) is deliberately
ledger/provider-independent (only structural checks: well-formed addresses,
`op` in `{create, modify}`, valid JSON) — attribute-key correctness needs a
real provider schema, which that function was never given and never should
be (see its own doc comment). Neither layer was ever positioned to catch
this; **resolve is the first point in the whole pipeline that has both a
concrete provider schema (already fetched, for type inference) and a
decoded config to check it against.**

`provider.UnknownConfigKeys` (`provider/schemakeys.go`) walks a resource's
config against its schema `Block` recursively — attributes and nested
blocks alike, the identical recognition rules `encodeBlockValue`
(`provider/ctyvalue.go`, UBI-63 session 2's own `ErrUnrecognizedConfigKey`)
already enforces at *encode* time — and collects **every** unrecognized key
in one pass, each with a fuzzy-match suggestion (`closestKey`: substring
containment first, e.g. `"repository_name"` → `"name"`, a large edit
distance but an unambiguous real-world signal; a Levenshtein-based typo
check only as a fallback when no candidate has that relationship at all —
see the file's own doc comments for why a blended score gets this
backwards). `cli/schemainspector.go`'s `schemaInspectorAdapter` is the one
place `provider.UnknownConfigKeys`'s real result gets translated into this
package's own `ConfigKeyIssue` shape; `resourceTypeSchemaInspector`
(`cli/status.go`'s/`cli/scanall.go`'s multi-provider fleet-grouping adapter)
stubs it to `nil`, matching its existing `IsComputed`/`IsSensitive` stubs —
`InferProvider` is the only thing that adapter is ever used for.

`resolveOnce` calls `UnknownConfigKeys` once per resource, in the same pass
that decodes each resource's raw config for `$ref` edge-scanning — **before
topo-sort**, and collecting issues across the **whole batch** (every wrong
key, on every resource) before returning, joined into one refusal via
`errors.Join` (`ErrUnknownConfigKey`, wrapping one `configKeyError` per
issue: `"<addr>: config key "<path>" does not exist on <type> (did you
mean "<suggestion>"?)"`, suggestion clause omitted when none is close
enough) — a drafted resource with 3 wrong keys is refused with 3 distinct
teaching errors, not a one-at-a-time whack-a-mole. `ship` time's own
`ErrUnrecognizedConfigKey` (`provider/ctyvalue.go`) is unchanged and stays
in place as encode-time's own defense-in-depth backstop for any input that
somehow bypasses resolve — the two checks now share exactly one "what
counts as a known key at this level" implementation (`knownKeySet`,
`provider/schemakeys.go`), so they can never silently diverge on what's
recognized.

Model-agnostic by construction, not by intent: `resolveOnce` has no
provenance information about a `ResourceIntent` at all (a hand-written
file, an evaluated SDK program, and any `intentprovider.Adapter`'s own
draft all arrive as the identical `resolver.IntentFile` shape) — there is
no branch to add or forget. `intentprovider/conformance/regression_test.go`
keeps the original haiku + `platform.md` repro runnable forever as a
hermetic regression case, alongside a `core/resolver` hermetic suite
(`resolver_test.go`) and a `cli/resolve_test.go` end-to-end test against
the real `fakeprovider` binary (`FAKEPROVIDER_MODE=conformance-v6`,
configured with `aws_iam_role`'s real attribute names) proving the actual
`schemaInspectorAdapter` wiring, not just an in-process fake.
See docs/resolver-adversarial.md row 14.

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

### Amendment: `$ref`/`$cross` embedded inside a JSON-encoded config string (2026-07-31, UBI-63)

A config attribute the provider schema types as a plain string but that
the resource itself treats as nested JSON (an IAM policy document, a
trust policy) can carry a `$ref`/`$cross`/etc. marker one level down,
inside that string's own decoded structure — full convention and
rationale in docs/schema.md's own equivalent amendment. Mechanically,
both resolver passes treat this identically to a top-level marker: the
edge-scanning pass (`scanRefEdges`'s `walk`) attempts a JSON decode of
every string value and recurses into it when the decoded structure
contains a marker (`containsMarker`), so a JSON-embedded ref contributes
the exact same dependency edge a top-level one would; the value-resolving
pass (`resolveValue`'s new `case string`) does the same decode-resolve-
re-encode round trip. A string that merely happens to parse as JSON but
contains no marker is never touched — this is not a general "prettify
every JSON-shaped string" pass, only a marker-resolution one.

This same code path is also where the resolver now hard-refuses the
broken shape found live in UBI-63 (a plain string like
`"$ref:stack.type.name.attr"`, instead of the real `{"$ref": {"to":
"..."}}` object) — `resolveStringValue` checks for the marker-key-plus-
colon prefix *before* attempting any JSON decode, so this refusal fires
for both a bare top-level string value and one nested inside a
JSON-embedded attribute, symmetrically.

### Amendment: a JSON-embedded `$ref` to a not-yet-applied `Computed` sibling is a template, not a refusal (2026-07-31, UBI-63 session 2)

The amendment above originally hard-refused a JSON-embedded ref that
resolved to an unresolved `$computed` marker — the same reasoning
`unsafeToEmbedMarker` applied to `$secret`, generalized (wrongly, found
live) to `$computed` too. Found live: this made the flagship same-batch
AWS pattern (a role's inline policy naming a sibling queue's ARN,
created in the same batch) impossible without a two-proposal
workaround. Fixed: `unsafeToEmbedSecret` (renamed from
`unsafeToEmbedMarker`) now checks only for `$secret` — a JSON-embedded
`$computed` marker is allowed to persist into the re-encoded string,
producing a genuine template in the signed proposal, still contributing
the identical dependency edge `scanRefEdges` already computed for it
(unchanged by this amendment). `core/executor`'s own `substituteComputed`
fills the template in for real at ship time — see docs/schema.md's own
"Amendment: deferred materialization" for the full design and the
ship-time half, and docs/executor.md's equivalent note for
`substituteComputed`'s own new string-leaf case. `$secret` is
unaffected: still a hard resolve-time error, `ErrSecretEmbeddedInString`,
for exactly the same reason as before (no redaction path for a marker
buried inside an opaque string attribute).

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

### Amendment (UBI-32 Arc B): `"stack"` alongside `"ledger_dir"`

`$cross`'s own inner object now accepts `{"stack": "<name>", "to": "..."}`
as a mutually-exclusive alternative to `{"ledger_dir": "...", "to": "..."}`
— never both at once (a hard error naming the contradiction). `ledger_dir`
is unchanged, permanent, git-local's own explicit-path shape; `stack`
resolves by NAME against the CURRENT stack's own `.ubx/config` `[ledger]`
store (or its `[ledger.external]` override for that name, if one is
configured) — docs/architecture.md's own "Addressing" section has the
full mechanism (`core.OpenRef`'s registry-based dispatch, so
core/resolver never has to import anything ledgerstore-shaped).
`"stack"` against a git-local current stack (no configured base store,
and no matching `[ledger.external]` entry) is refused, naming the gap
and suggesting `ledger_dir` directly — never silently treated as if it
resolved to nothing. Every other part of this section (pinned-head
staleness, the `$computed`-propagates-forward edge case) applies
identically regardless of which shape named the neighbor.

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

## Amendment (2026-07-17, UBI-30): destroys — explicit intent, resolve-time orphan protection

Design only, session 1 of UBI-30 — no code lands with this amendment (see
docs/plan.md's own wedge subsection for the full session breakdown). This
closes the line "Scope," above, has always drawn — not by revisiting
*whether* destroys belong in `change` proposals (they always did,
structurally: the very first `Proposal` draft's `delta.destroys` array
predates this resolver by a full session, docs/schema.md's original "IR —
resource node" section), but by giving them the same design rigor
creates/modifies already got in UBI-27, rather than shipping a destroy path
that's just "creates, but backwards" and calling it done. docs/schema.md's
own amendment (below) pins the wire-format/validation half of this;
docs/executor.md's own amendment pins how a resolved destroy actually
executes; docs/destroys-adversarial.md pins the required-outcome program.

### Destroys are a distinct, explicit intent-file list — never inferred

`ubx:intent/v1` (docs/schema.md's own amendment) gains a new top-level
field, a sibling to `resources[]`, not a new `op` value on an existing
resource entry:

```json
{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": { "summary": "decommission the old standby replica", "sources": [...] },
  "resources": [ "...": "creates/modifies, unchanged" ],
  "destroys": [
    "payments.aws_db_instance.old-payments-db"
  ]
}
```

Each `destroys[]` entry is a canonical address string (`Address.String()`
form, `<stack>.<type>.<name>` — the exact convention `$ref`'s own `to`
field and `Modification.Target` already use) naming a resource the intent
author wants gone. This was a real design choice, not the obvious one:
putting `op: "destroy"` on a `resources[]` entry (matching `create`/
`modify`'s own shape) was the first design considered and rejected, for the
same reason `op` itself is explicit rather than inferred (see "`op: create
| modify` — explicit, not inferred," above) taken one step further — a
destroy has no `config` to submit, only an address to remove, so folding it
into `resources[]` would mean either a `resources[]` entry with a dangling,
meaningless `config` field, or a special-cased "config not required if op
is destroy" branch in every consumer of `resources[]` from here on. A
dedicated list has no such awkwardness, and keeps the intent file's own
top-level shape self-documenting: `resources[]` is "what to build or
change," `destroys[]` is "what to remove," never conflated.

**Never inferred from absence — a permanent boundary, not a v1-scope
one.** The obvious alternative — a resource the ledger already has, that
this intent's `resources[]` simply doesn't mention, is implicitly a
destroy — is rejected outright, not just for this ticket but as a
permanent design posture: `ubx scan`'s own `ScanNew`/`ScanDrifted`
classification already treats "not yet observed" as exactly that, never as
"gone," and destroy is the one operation this codebase's own architecture
("wedge reads and records before it ever writes," docs/architecture.md)
must never guess at from a negative signal. A resource silently dropped
from a hand-authored intent file (a typo, an incomplete edit, a stale
copy-paste) must never be read as "destroy this" — the cost of guessing
wrong in the destroy direction is categorically worse than guessing wrong
in the create/modify direction (a wrongly-inferred create/modify is
corrected by a subsequent proposal; a wrongly-inferred destroy has already
deleted real infrastructure by the time anyone notices). Absence-based
detection has a real, legitimate future home — a **WARN-only** diagnostic
(a future `ubx resolve`/`ubx scan` mode surfacing "the ledger knows about
`payments.aws_db_instance.old-standby`; this intent doesn't mention it —
did you mean to destroy it?") — but that is strictly advisory, never a
resolver-internal trigger for actually populating `delta.destroys`, now or
ever. Unlike every other "later" in this document, this one names a
boundary that stays fixed even after the advisory diagnostic exists.

### Resolve-time validation: symmetric to `op`, opposite direction

Exactly like `op: "create"` requires the address to be absent from
`FoldState` and `op: "modify"` requires it present (see "`op: create |
modify`," above, unchanged): every `destroys[]` entry requires its address
to be **present** in `FoldState` — a destroy naming an address the ledger
has never recorded, or one already tombstoned by a prior destroy proposal
(docs/schema.md's own tombstone posture, below), is a hard resolve-time
error, never silently ignored or treated as a no-op. This is the
destroy-specific instance of docs/resolver-adversarial.md's row 9 pattern
("declared operation doesn't match ledger reality"), extended to a third
operation — a new adversarial row for this table, not a reinterpretation of
an existing one (docs/destroys-adversarial.md's own table, not
docs/resolver-adversarial.md's, since it's destroy-specific machinery being
tested, not the create/modify resolver path).

### Orphan protection: reverse edges, checked against the whole ledger, not just this batch

This is the real, new design work this amendment exists for — not just
"add a third array and reverse the loop." A destroy target may be
referenced by another resource's already-recorded dependency edge, and
removing it out from under that reference would silently orphan the
referencing resource — exactly the kind of blast this resolver's whole
"typed intent, checked before anything ships" premise exists to catch
before a human signs it, not after `ubx ship` finds out the hard way from
the provider's own rejection (or, worse, doesn't — some providers happily
leave a dangling reference rather than erroring).

Two edge sources, checked differently, both against the **ledger**, never
against just the intent file currently being resolved:

1. **Intra-stack.** `depends_on` (docs/schema.md's "Amendment: intent files
   and resolved `change` proposals," UBI-27 — recorded on any
   `Delta.Creates` or `Modification` entry, in any proposal this stack's
   ledger has ever accepted) is already the exact record of "resource X's
   own resolved config named resource Y via `$ref`." Resolving a destroy
   for Y walks every accepted proposal in this stack's chain
   (`core.Ledger.Chain`, the same full-ledger walk `FoldState` itself
   already performs) for any `depends_on` entry naming Y's address, from a
   resource that is (a) not itself already tombstoned by a prior destroy,
   and (b) not *also* being destroyed in this same destroy batch. A
   same-batch mutual destroy (destroying Y and its dependent X together) is
   not an orphan — the executor's own reversed-dependency walk
   (docs/executor.md's amendment, below) guarantees X is destroyed before Y
   in that case, so nothing is ever left pointing at a hole. Only a destroy
   that would leave a **surviving** referencing resource pointed at
   nothing is refused. The addresses collected here (X, Y's own
   dependents) become Y's destroy entry's own `depends_on` list — see
   "`delta.destroys` carries full folded state," below, and
   docs/executor.md's amendment for why this is the one mechanism that also
   makes destroy *ordering* correct, not a separate concern from orphan
   detection.
2. **Cross-stack.** The mirror-image problem is structurally harder, and
   this amendment is honest about exactly where it stops rather than
   quietly assuming a solve. A `$cross` reference is recorded entirely on
   the *consuming* stack's own ledger
   (`resolution.inputs[].kind == "cross_stack_pin"`, carrying `ledger_dir`
   — the producing stack's location, as seen by the consumer, "Cross-stack
   refs," above) — the producing stack (the one being asked to destroy
   something) has no built-in index of who, if anyone, has ever pinned
   against it; docs/schema.md's own "Open questions" already names this
   precise gap ("cross-stack workspace index format," tracked, not
   blocking, since founding). This resolver does not invent a registry to
   close that gap now. Instead, cross-stack orphan protection is
   **best-effort and explicit** — the same "simple, explicit convention
   over a fancy registry" instinct this document has already applied twice
   (`ledger_dir` itself; `$cross`'s own shape): the resolver's own contract
   gains an optional `known_dependents []string` input (ledger
   directories), supplied by the operator resolving the destroy — exactly
   as explicit as `$cross.ledger_dir` already is from the consuming side —
   and resolving a destroy walks each named neighbor's own ledger chain for
   a `cross_stack_pin` entry pinning this stack's destroy target. **Naming
   zero dependents does not mean "no dependents exist"** — it means none
   were checked, and this is surfaced honestly rather than silently: a
   destroy resolved with an empty (or omitted) `known_dependents` list
   still succeeds, but the resolved proposal's own `resolution.inputs`
   records a `cross_stack_orphan_check` entry with `status:
   "not_performed"` (docs/schema.md's own amendment, below) — never
   silently absent, never presented as equivalent to a real check. This
   mirrors `cloudtrail_unattributed`'s own "record the gap as evidence,
   don't just omit it" discipline (docs/schema.md, UBI-10) applied to a
   structural gap instead of a network one.

A cycle-detection-shaped concern worth naming, not a new mechanism: unlike
the intra-stack dependency *graph* built for ordering creates ("The
dependency graph and real cycle detection," above), orphan protection
itself is not a graph search — it's a direct reverse-index lookup ("who
points at this one address"), computed once per destroy target. No new
cycle-detection logic is needed for the lookup itself; the combined
graph produced *from* that lookup (creates/modifies' forward edges plus
destroys' reverse edges, all in one proposal) is what still needs the
existing cycle detector run over it once, unchanged — see docs/executor.md's
amendment for why this stays one topo-sort, not two.

**Two things found while actually implementing this (`core/resolver`,
UBI-30 session 2), not assumed correct from the design above alone:**

- **Intra-stack `depends_on` only ever exists for a ref recorded while its
  target was in the *same* resolve batch — never for a ref to an
  already-ledgered resource the batch didn't touch.** Re-checked directly
  against `scanRefEdges`'s own code, not assumed: a `$ref` to an address
  outside the current batch resolves against `FoldState` (case 2 of
  "Intra-stack refs," above) and contributes **no** `depends_on` edge at
  all — only a same-batch sibling ref does. This means the intra-stack
  orphan walk above is real and correct exactly as scoped ("`depends_on`...
  is already the exact record of 'resource X's own resolved config named
  resource Y via `$ref`'" — true as written), but its actual reach is
  narrower than it might first read: it only ever catches a dependent that
  was created or modified *in the same original proposal* as the resource
  it depends on, never a dependent from some later, separate proposal that
  happened to reference an already-existing resource's value (that
  reference leaves no recorded edge anywhere to walk). This is the same
  "best-effort, not exhaustive" honesty this section already gives
  cross-stack orphan protection, just discovered to apply to the
  intra-stack half too, once actually implemented — named here rather than
  quietly assumed complete. See docs/destroys-adversarial.md's own "what
  this table doesn't yet cover" for the adversarial-program-level
  consequence.
- **A same-batch `$ref` into a destroy target must be rejected outright, or
  the "handled" case above isn't actually sound.** The "handled" branch
  (a historical dependent X is being modified in this same batch, so its
  own operation — not its destruction — is what Y's destroy waits on)
  silently assumes X's *new* config no longer points at Y. Nothing in the
  design above enforced that until this session's implementation added it:
  `core/resolver/refs.go`'s `resolveRef` now refuses (`ErrRefToDestroyTarget`)
  any `$ref`/`$cross` that resolves to an address this same proposal is
  also destroying — a real, load-bearing validation rule, not previously
  named. This is what makes "handled" true rather than merely hoped: by
  the time a same-batch modify's config is fully resolved, it is
  *provably* free of any reference to the destroy target, never just
  assumed to be.

`ubx resolve`'s own CLI surface (session 2) made `known_dependents`
concrete as a repeatable `--known-dependent <ledger_dir>` flag, threaded
straight into `resolver.Resolve`'s own new parameter of the same name —
the plain, direct instantiation of "operator-supplied ledger directories"
this section already named, not a new decision.

### `delta.destroys` carries full folded state, not just an address — a deliberate divergence from `Modification`'s terser shape

`Modification.Before`/`.After` deliberately hold only the attributes that
*changed* (docs/schema.md's own pinned "Delta element shapes" section) — a
destroy has no such economy available, and shouldn't reach for one:
**everything** about the resource is being lost, not a subset of its
attributes, so a human reviewing and signing a destroy proposal needs to
see the resource's own full, real, `FoldState`-folded config inline, in the
proposal itself — not a hash they'd have to separately dereference against
a ledger they may not have open at review time. This is a hashed-content
shape change to `Delta.Destroys`' pinned element type — see docs/schema.md's
own amendment for the exact new shape, the `schema_version` bump this
requires, and why the migration cost is unusually low (no proposal, of any
kind, has ever actually populated `delta.destroys` — `core.Validate`
forbids it unconditionally for every kind that exists today).

## Amendment (2026-07-18, UBI-43): multi-provider stacks — type→provider inference

Every verb today launches exactly one provider per invocation
(`--provider`, or `--source`+`--provider-version`) — a real, load-bearing
simplification that held fine through UBI-26/27/29/30, but doesn't match
docs/architecture.md's own payments example (RDS + S3 + `helm_release`,
one stack, three provider binaries). docs/architecture.md §Multi-provider
stacks (design room, 2026-07-17) decided the shape; this amendment is
`core/resolver`'s own half of it.

### The `providers` config map — declared, not inferred, from where

A stack's provider set is a `providers` table (source → pinned version,
explicit pins only — the same standing rule every other version pin in
this project already follows) in `.ubx/config`. This rides the config
loader UBI-19 already shipped (TOML today, HCL/YAML per
docs/architecture.md §Config formats) — it does **not** need to
wait for UBI-32's own cascade semantics (child-overrides-parent, a
provenance view) to unpark. A flat `providers` table in the nearest
`.ubx/config` resolves correctly today, exactly like every other config
key this project already reads without cascade; UBI-32, whenever it
unparks, upgrades *how* that table is found and merged across directories,
not *whether* it works. This session's own design doesn't block on it.

```toml
[providers]
"hashicorp/aws" = "6.60.0"
"hashicorp/helm" = "3.0.2"
"hashicorp/kubernetes" = "2.35.1"
```

### Type→provider inference: schema ownership, never name-prefix guessing

`ubx resolve` already launches a provider for exactly one reason today —
"its schema is what tells the resolver which attributes are `Computed` or
`Sensitive`" (this document's own opening description) — a free, no-
credentials, no-state-mutating round trip (`GetProviderSchema` only,
docs/architecture.md's own "a provider's schema costs nothing" finding,
UBI-9). Multi-provider generalizes this from one round trip to N: resolve
launches every provider named in the stack's `providers` map, and builds
one `SchemaInspector` (this document's own "Schema boundary" section,
above — `HasType`/`IsComputed`/`IsSensitive`) per provider instead of one
globally. No new interface — the existing one, instantiated N times.

For each `resources[]`/`destroys[]` entry's own `type`, every declared
provider's `HasType(type)` is asked. The three outcomes:

- **Exactly one match** — that provider owns the type. Used, no friction.
- **Zero matches** — hard error naming the type and every provider
  checked: `resolve: no declared provider owns type "aws_db_instance" —
  checked: hashicorp/helm, hashicorp/kubernetes`. Never silently skipped,
  never guessed from the type name's own prefix (`aws_*` "looking like"
  AWS is exactly the kind of heuristic this design explicitly rejects —
  see docs/architecture.md's own wording: "never name-prefix guessing").
- **More than one match** — a real ambiguity (two declared providers both
  advertise the same type name — plausible for forked/vendored providers,
  or two versions of a provider family declared under different sources)
  — hard error naming every owner found, *unless* the entry carries an
  explicit `"provider": {"source": "..."}` hint (docs/schema.md's own
  amendment) naming which declared source to use. The hint is consulted
  only to break a tie; it's refused at resolve time if it doesn't name one
  of the stack's own declared providers, and never accepted as a way to
  route a type to a provider that doesn't actually claim it (`HasType`
  still has to return true for the hinted provider, or the hint itself is
  the error).

The winner is recorded into the resolved node's own `provider` field —
`{source, version}`, the version being the `providers` map's own pin *at
resolve time*, not re-derived later — signed as part of the proposal
exactly like `depends_on` already is.

### Destroys infer their provider exactly like creates/modifies — never inherited from history

A destroy target's provider is **not** read from whichever provider
originally created it (no proposal recorded that historically before this
amendment, and even once every new proposal does, trusting stale history
over the stack's own current declared set would be exactly the kind of
silent drift this project exists to catch, not reproduce). It's inferred
fresh, the identical schema-ownership check, against the *currently*
declared `providers` map. If the provider that used to own this type has
since been quietly dropped from config, destroy resolution hard-errors
exactly like any other unowned-type case — a deliberate friction point: a
stack's own provider set narrowing shouldn't let a destroy sail through
unnoticed against a provider nobody declares anymore.

### The dependency graph stays exactly as it is — already provider-agnostic, checked directly

**Zero changes needed here — confirmed by reading the actual code, not
assumed from the design alone.** `topoSort`/`edgesOf` (this document's own
"dependency graph" section, above) operate purely on canonical addresses
(`Address.String()`) and `$ref`/`$computed`/`depends_on` edges between
them — type and provider are never consulted while building or walking
the graph. A `$ref` edge from an `aws_db_instance` node into a
`helm_release` node's `values` is exactly the same kind of edge as two
same-provider nodes referencing each other; the graph has no way to know,
and no reason to care, that the two ends resolve to different provider
binaries. This is the same "one combined mechanism, not a second one"
shape docs/executor.md's own destroy amendment already established for
reversed ordering — multi-provider gets it for free here too.

### Determinism: provider selection is a pure function, the existing double-run guarantee already covers it

This document's own "Determinism" section (above) already requires
`ubx resolve` to run its whole resolution twice and hard-fail on any byte
divergence. Type→provider inference introduces no new source of
nondeterminism to that guarantee: for a fixed `(declared providers, type)`
input, `HasType` always returns the same answer (a provider's own schema
doesn't change mid-resolve), so the winner is always the same winner on
both runs. Nothing new to test here beyond the existing double-run
coverage exercising a multi-provider resolve at all.

### `--source`/`--provider-version` retirement: deprecated, staged, never a breaking cutover

A single-provider stack (no `providers` config present) keeps working
*exactly* as it does today — `--provider`/`--source`+`--provider-version`
unambiguous, one provider, unchanged behavior, unchanged flags. Once a
stack declares a `providers` map, that map is the authority for that
stack, and the singular flags stop being meaningful for it (multiple
providers now genuinely need launching, not one) — but retirement is
staged, not a breaking cutover in one session:

1. **Built, session 4**: both mechanisms coexist. `providers` config,
   when present, is used; the singular flags remain fully functional as
   the single-provider path for every stack that hasn't adopted the
   config map yet.
2. **Built, session 4**: using the singular flags against a stack that
   also declares `providers` config emits a deprecation warning
   (`warnIfLegacyProviderFlagsGiven`, `cli/config.go`) rather than
   silently picking one source over the other — config still wins either
   way, the flags are simply ignored, loudly.
3. **Eventually (a major-version-shaped change, explicitly not scheduled
   by this amendment)**: the singular flags retire for good, `providers`
   config becomes required for every stack.

Scan/status/fleet's own equivalent generalization — grouping by each
resource's own recorded provider instead of a single `--source` — is
docs/executor.md's own half of this amendment, not resolver's.

**One real thing found while actually implementing this (`core/resolver`,
UBI-43 session 2), not assumed correct from the design above alone:**
`resolveRef`'s own `IsComputed` check on a `$ref` target's attribute must
consult the *target's own* resolved provider schema, never the schema of
whichever entry happens to be resolving right now. Before this session,
`resolveValue`/`resolveRef` shared one globally-passed `SchemaInspector`
parameter, so this distinction was invisible — there was only ever one
schema to consult, whoever asked. Once a `$ref` can cross a provider
boundary (an `aws_db_instance` node referenced from a `helm_release`
node's own config, docs/multi-provider-adversarial.md's own row 5), using
the *referencing* entry's schema to answer "is the *referenced* entry's
attribute Computed" would silently query the wrong provider's schema
entirely — for two providers with genuinely disjoint type sets (the
common case), that schema wouldn't even recognize the target's own type
name, which is either a hard crash or a wrong answer depending on how
defensively the caller happens to be written, neither of which is
"resolve correctly." Fixed by recording each batch entry's own resolved
provider *before* any value resolution begins (the same preliminary pass
that infers and records `provider` for the JSON output), and having
`resolveRef` read `target.provider.Schema` directly off the sibling batch
entry it already has in hand, rather than trusting a parameter passed down
from the caller's own context. `TestResolve_CrossProviderRef_ComputedSubstitution`
(`core/resolver/multiprovider_test.go`) reproduces this exactly — the
referencing entry's own provider (`helmSchema`) doesn't even declare
`aws_db_instance` as a known type at all, so a naive implementation using
the wrong schema there would have failed this test immediately, not
silently passed with a wrong answer.

**Hermetic coverage** (`core/resolver/multiprovider_test.go`, new):
type→provider inference recording the correct winner on creates/modifies/
destroys (docs/multi-provider-adversarial.md rows implicitly covered by
every row below succeeding at all); row 1 (ambiguous type, no hint,
refused); row 2 (ambiguous type, resolved via an explicit hint, including
both ways a hint itself can be wrong — naming an undeclared source, and
naming a real declared provider that doesn't actually own the type); row
3 (unowned type, both for a fresh create and for a destroy target whose
original provider has since been dropped from the declared set); row 5
(a cross-provider `$ref` chain, `$computed` substitution, correct
`depends_on`). `Resolve`'s own signature changed from a single
`SchemaInspector` to `[]DeclaredProvider` — every existing hermetic test
predating this session (40 call sites across `resolver_test.go`/
`destroys_test.go`) updated mechanically via a new `singleProvider(s)`
test helper wrapping a lone schema into a one-element slice, preserving
each test's own single-provider behavior unchanged; all still pass.
`cli/resolve.go`'s own call site does the identical one-element wrap
around today's `--provider`/`--source` flow — no CLI-visible behavior
change this session, since there's no way yet to declare more than one
provider from the CLI (that's `.ubx/config`'s own `providers` table
wiring, still queued). Full repo `go build`/`go vet`/`gofmt -l .`/`go test
./... -race -count=1` clean, no regressions.

### Session 4 (2026-07-18): `.ubx/config`'s `[providers]` table, live-wired into `ubx resolve`

`cli/resolve.go` now branches on `cfg.Providers` (`.ubx/config`'s own new
`[providers]` table, `cli/config.go`): non-empty means a real
multi-provider stack — every declared source is launched *eagerly*
(unlike the executor's own lazy pool, resolve needs every declared
provider's own schema to correctly perform ambiguity/ownership checks,
not just whichever ones a specific intent file happens to touch), in
sorted-source order (`sortedProviderSources`, `cli/providerpool.go` —
determinism is a feature; a Go map's own iteration order is not), each
one's schema wrapped into a `resolver.DeclaredProvider`. Empty falls back
to today's exact `--provider`/`--source`+`--provider-version` single-launch
flow, byte-for-byte unchanged. `--source`/`--provider-version` retirement
stage 2 is built: `warnIfLegacyProviderFlagsGiven` (`cli/config.go`)
warns to stderr, naming exactly which flags were ignored, whenever a
stack with a real `[providers]` table also receives them — config always
wins, the flags are never silently overridden nor honored instead.

**Amendment (2026-08-20)**: the `[providers]` table this section
describes is now `[thirdparty_providers]` (`cfg.ThirdpartyProviders`);
`[providers]` itself is a real, different, new meaning (ubx's own,
dynamic-provider-backed sources). See docs/architecture.md's own
"Amendment (2026-08-20): `[providers]` splits into two namespaces" for
the full design and real precedence rule — not restated here, since
`resolve.go`'s own eager-launch behavior described above is otherwise
unchanged (it still only ever eagerly launches thirdparty sources;
`core/resolver`'s own inference does not yet consider `[providers]`,
named as real, separate, not-yet-done work there).

**Live-verified, not just hermetic**: a real multi-provider `ubx resolve`
→ `ubx accept` → `ubx ship` chain against two genuinely separate
provider subprocesses (via `UBX_PROVIDER_MIRROR`, no network), each
advertising a disjoint type (`provider/internal/fakeprovider`'s own
`conformance-v6` mode, one copy per `FAKEPROVIDER_RESOURCE_TYPE`) —
confirmed the resolved proposal records the correct `provider` field on
each node (`acme/widget`→`widget_a`, `acme/gadget`→`widget_b`, never
crossed), the deprecation warning fires and names the right flags when
both a table and `--source` are given, and a version bump in
`.ubx/config` after a proposal was already signed against the old pin is
refused at ship time (docs/executor.md's own session-4 half of this
finding) rather than silently launching a different version than what was
reviewed. See docs/executor.md's own session-4 addendum for the
executor-side half (`cli/providerpool.go`, `ApplierPool.Get`'s own
config-returning signature) this CLI wiring drives.

## Out of scope for v1, named so it isn't assumed covered

- ~~`delta.destroys` (see Scope, above).~~ — **designed, UBI-30** (see the
  Amendment below); resolver *code* producing a populated `delta.destroys`
  is still session 2+ of that ticket, not this document's own session.
- ~~Multi-provider stacks (a single `change` proposal spanning more than
  one provider binary) — resolver code.~~ — **fixed, UBI-43 session 2**
  (see the Amendment above): `core/resolver` performs real type→provider
  inference and populates each node's `provider` field, hermetic. Still
  queued, other sessions of the same ticket: `core/executor`'s own client
  pool, `.ubx/config`'s `providers` table wiring, CLI deprecation staging,
  and the live finale.
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
- ~~A created resource becoming discoverable by `ubx status`/`ubx why
  <address>` after it ships~~ — **fixed, UBI-29**: `core.Ledger.Fleet`/
  `ProposalsForAddress`/`LastObservedHash`/`LastObservationTime`/`FoldState`
  now all fold a `change` proposal's own apply records as a second
  discovery source, gated on the specific resource's own last transition
  being `applied` (not on the whole multi-resource attempt being sealed).
  See docs/schema.md's own "Amendment: apply-record lookup key + Fleet
  discovery" and docs/executor.md's equivalent note for the full design —
  this was a discovery-layer gap, not a resolver one, so nothing in this
  document's own contract changed.
