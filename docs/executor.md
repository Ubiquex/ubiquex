# Executor v1 — failure-state machine (UBI-26)

> This document is the spec, written before any code. docs/schema.md's
> "Amendment: apply records" pins the ledger object this machine produces;
> this document pins the machine itself. docs/executor-adversarial.md pins
> the program every implementation of it must pass.

## Scope: `drift_revert` only, and why that's not an arbitrary restriction

`ubx ship <proposal-id>` v1 executes exactly one proposal kind:
`drift_revert`. Every other kind is out of scope for a real, structural
reason, not just sequencing convenience:

- `adoption`/`drift_adopt` are record-only by construction (all-zero
  `blast_radius`, docs/schema.md) — there is nothing to ship; accepting one
  already is the entire action.
- `change`/`revert` (hand-authored, resolver-produced proposals) don't
  structurally exist yet — the resolver (component map #2) hasn't been
  built, and a hand-authored change can legitimately contain `$computed`/
  `$secret`/unresolved-reference values that only a real plan phase can
  settle. Shipping those is real future work, not this ticket's.
- `drift_revert` is the one kind where "what to write back" is *already
  fully resolved and concrete*: `delta.modifies[].after` holds the ledger's
  already-recorded, already-concrete value for every attribute being
  restored — never a placeholder, never something requiring a provider's
  own plan-time unknown-resolution. This is what lets v1 skip a distinct
  `PlanResourceChange` phase entirely and go straight to
  `ApplyResourceChange` — see "Constructing `PlannedState` without
  planning," below. That shortcut is only sound for exactly this kind.

## Preconditions

`ubx ship <proposal-id>` refuses to start unless:

1. The proposal exists, `Kind == drift_revert`, and `Acceptance != nil`
   (`Status == accepted` in the stored, immutable sense — see
   docs/schema.md's "`Proposal.status` is never rewritten" note; ship reads
   the stored status directly here, since this check runs *before* any
   apply record exists to fold over).
2. It is not already fully applied — every resource address in
   `Delta.Modifies` already resolved to `applied` in some prior sealed
   apply record for this proposal. If so, `ship` is a no-op: it reports
   this and exits 0, touching nothing. This is the idempotency contract's
   simplest case (see "Idempotency," below).

## The state machine

Per resource (one `Delta.Modifies` entry, keyed by its `Address`):

```
pending ──► in_flight ──► applied
                       ├─► failed
                       └─► unknown_post_timeout
                                 │
                                 ▼
                     reconcile-by-query (ReadResource)
                                 │
                 ┌───────────────┼───────────────┐
                 ▼               ▼               ▼
              applied         failed        still_unknown
                                          (bounded retries,
                                        then terminal for
                                          this attempt)
```

- **`pending`** — this resource's turn hasn't come yet, or (see "Staleness,"
  below) it was refused before reaching `in_flight`.
- **`in_flight`** — the risky operation is about to be, or has been,
  attempted. **THE invariant of this whole design: the transition to
  `in_flight` is durably written to the apply record on disk *before*
  `ApplyResourceChange` is called** — not after, not concurrently. This is
  what makes "crash between the write and the call" and "crash between the
  call and the result" two distinct, individually testable, individually
  recoverable scenarios instead of one ambiguous blur (docs/executor-adversarial.md
  rows 4–5). Verified live, not only in hermetic tests (UBI-26 session 4,
  docs/reliability-report.md): a real `kill -9` at each of these two exact
  points (before the call, and after it succeeds but before the result is
  recorded), reproduced on demand via `UBX_SHIP_DEBUG_DELAY_AFTER_INFLIGHT`/
  `UBX_SHIP_DEBUG_DELAY_AFTER_APPLY_SUCCESS` (package-level test seams, zero
  by default — see `core/executor/ship.go`'s own doc comment — the same
  "env var gates a test-only knob" convention
  this codebase already uses elsewhere, e.g. `FAKEPROVIDER_MODE`).
- **`applied`** — terminal, success. `ApplyResourceChange` returned cleanly,
  or reconciliation independently confirmed the restored value is live.
- **`failed`** — terminal for this attempt, but not for the proposal: a
  future `ubx ship` re-run retries a `failed` resource (within budget — see
  "Idempotency").
- **`unknown_post_timeout`** — the RPC didn't resolve into a clear
  answer before its own deadline (dead provider process, killed `ubx`,
  network partition to a remote provider, ...). Never treated as `failed`
  or `applied` directly: reality is asked, not assumed.
- **reconcile-by-query** — a fresh `ReadResource` call against the same
  lookup key already recorded in `Resolution.Inputs` (exactly
  `core.VerifyFreshness`'s own read path, reused, not reinvented). Its
  result is compared against the restore target (`after`): a match means
  `applied` (the change landed, whatever `ubx`'s own view of the RPC
  thought); a match against the *original drifted* value means `failed`
  (it never landed); anything else (unreadable, a third value neither
  matches) is inconclusive.
- **`still_unknown`** — reconciliation was inconclusive after exhausting a
  bounded retry budget (a package-level var, not a hardcoded constant — same
  convention as `core.lockWaitTimeout`, so tests can shrink it). Terminal
  for *this* attempt; a future `ubx ship` re-run reconciles again first,
  before attempting anything new for that resource.

## Freshness: re-verified before every attempt, not just the first

`core.VerifyFreshness` already exists (built for `ubx accept`) and is reused
here unchanged, but invoked differently: **once per resource, immediately
before that resource's own `pending → in_flight` transition** — not once
at the top of the whole `ship` run. A multi-resource `drift_revert`
proposal (legal per docs/schema.md: "at least one `delta.modifies` entry,"
no upper bound) can take long enough, resource to resource, that reality
moves *during* the run — the second resource's live state can drift a
second time while the first is still being applied. Re-checking only once,
up front, would miss exactly that.

**Stale detected mid-partial-apply is refused, never bulldozed**: if
resource *N* fails freshness after resources *1..N-1* already reached
`applied` in this same attempt, resources *1..N-1*'s success stands
untouched (it already happened — nothing to undo), resource *N* (and
everything after it, in delta order) is refused *before ever reaching
`in_flight`* — it stays `pending`, with an attached `errors[]` entry
(`classification: "terminal"` for this attempt — reality actually moved,
retrying the *same* plan blindly would be wrong; a fresh `ubx scan`/
`ubx accept` cycle is what's needed, not a `ship` retry). The attempt seals
as `partially_applied`, not `failed` — partial, real progress is reported
honestly as partial progress.

## Serial execution in delta order — a precise definition

"Delta order" means the same canonical `(stack, type, name)` lexicographic
sort docs/schema.md's ratified hashing rules already define for
`delta.modifies` — **not** whatever order the stored `.prop.json` file's
JSON array happens to be in. This distinction is real and worth stating
plainly: `core.canonicalProposalBytes`/`sortDeltaElements` only sorts a
*transient decoded copy* of `Delta.Modifies` for the purpose of computing
the hash — it never mutates the `Proposal` struct's own field, so a
proposal's stored array order is not guaranteed to already be sorted.
`ubx ship` must independently apply the same `(stack, type, name)` sort to
`Delta.Modifies` before iterating, rather than trusting stored order —
otherwise two structurally identical proposals (same hash, since hashing
already ignores stored order) could execute their resources in a different
sequence depending on incidental array order, which would make "serial,
delta order" an unenforced claim rather than a real guarantee. v1 has no
cross-resource dependency graph (that's a resolver/executor concern this
project has already ruled out of the *hashing* layer, per schema.md — and
v1's executor doesn't reintroduce one either); the sort exists purely to
make execution order deterministic and reproducible across runs, not to
express dependency.

## Constructing `PlannedState` without planning

`tfplugin{5,6}.ApplyResourceChange_Request` requires `PriorState`,
`PlannedState`, and `Config` (all `DynamicValue`, cty-msgpack — same
encoding lessons UBI-7 already established for `ReadResource`). Real
Terraform usage always produces `PlannedState` via a prior
`PlanResourceChange` call, which resolves defaults and unknown/computed
values the provider itself must fill in. `drift_revert`'s narrow shape
makes that unnecessary in v1: `PlannedState` is mechanically "the freshly
re-verified `PriorState`, with `Delta.Modifies[].after`'s already-concrete
dot-path values substituted in" — the same "apply a `Modification` onto a
state blob" operation `tfwrite.ApplyModification` and `core.diffObjects`'s
dot-path convention already model, just producing a JSON value to encode
rather than an HCL edit. No unknowns are ever introduced by a revert
(every value being restored was, by definition, already concretely observed
once before), so there is nothing for a real plan phase to resolve that
this substitution doesn't already capture. `Config` is set identically to
`PlannedState` for the same reason (a revert has no separate "desired
config" distinct from the value being restored to).

This is a v1-scope shortcut, stated as such: a future `change`/`revert` kind
executing hand-authored config, once the resolver exists, will need a real
`PlanResourceChange` phase and cannot reuse this substitution shortcut.

**Verified empirically against a real provider binary, not assumed sound
from the mechanism's own description** (`provider/apply_live_test.go`,
`hashicorp/time`'s `time_static` resource — pure local computation, no
cloud credentials needed, gated `UBX_CONFORMANCE_LIVE=1` like every other
network-touching test in this codebase): a realistic `PriorState` (every
computed attribute already concrete, as a genuine `ReadResource` call
against an already-existing resource would return) plus a `PlannedState`
built via this exact substitution correctly applies the one changed
attribute while carrying every other attribute forward unchanged — no
error, no silent data loss.

A real false start along the way, worth recording rather than quietly
fixed and forgotten: an earlier attempt at this same test used
`PriorState = null` (modeling a from-scratch create via
`ApplyResourceChange` directly, no prior `ReadResource` at all) and found
every computed attribute came back `null`, not computed. This briefly
looked like a real gap in this section's whole approach — an SDKv2-vintage
provider's `Apply` only fills in a computed attribute it finds *unknown* in
`PlannedState` (the marker a real `PlanResourceChange` call produces), and
`encodeDynamicValue` has no way to express "unknown," only "null" (an
absent JSON key). It isn't a gap: `drift_revert` never creates a resource
from scratch, so `PriorState` never comes from anywhere but a real,
already-successful `ReadResource` call (`core.ReadAndFingerprint`, exactly
what `Ship`'s own loop uses) against an *already-existing* resource — which
already carries every computed attribute's real, concrete value, never
`null`. The corrected test models exactly that shape and passes.

## Redacted after values are declined, not applied

A `drift_revert` whose restore target (`Delta.Modifies[].after`) is itself
a `$redacted` value (docs/schema.md's `$redacted` value encoding, UBI-23)
can never be shipped automatically: the ledger holds a salted fingerprint
of the real secret, never the material itself, so there is nothing for
`ubx ship` to substitute into `PlannedState` even in principle. Checked
per resource, before any read/freshness/apply work at all (earlier than
even the reconciliation-needed check) — if any dot-path in `after` is
`core.IsRedactedValue`, the *entire* resource is declined for this attempt
(never a partial apply of just the non-redacted paths: `ApplyResourceChange`
is one whole-state operation, not an independent per-attribute one, unlike
`.tf` write-back's own per-attribute decline). The resource stays `pending`
— it never reaches `in_flight`, and never counts toward the per-resource
retry budget — with a terminal error naming the affected path(s) and
pointing at `ubx revert-plan`'s existing manual-reconciliation path
(docs/architecture.md's Revert path: emits a human-readable plan and,
where possible, a corrective `.tf` diff — outside of `ubx`'s own apply
path entirely). This is permanent, not a transient failure: the same
`drift_revert` will decline identically on every future `ubx ship` re-run,
since the redacted value never changes — the only way past it is a human
restoring the real value out-of-band and recording the correction through
a fresh `ubx scan`/`accept` cycle.

## Idempotency contract

`ubx ship <proposal-id>` is safe to re-run, by contract, any number of
times. Per resource, keyed by the union of every prior sealed apply
record's final state for that address:

| Prior final state | Re-run behavior |
| --- | --- |
| `applied` | Skipped entirely — no new transitions recorded for it in the new attempt. |
| `failed` | Retried from `pending`, if within the per-resource retry budget (a package var); freshness re-verified first, exactly like a first attempt. |
| `still_unknown` | Reconciliation runs again first (bounded retries, again), *before* any new `ApplyResourceChange` call — never a blind re-apply on top of an unresolved unknown. |
| Never reached `in_flight` (refused for staleness) | Freshness re-verified fresh; proceeds exactly as a first attempt, once it passes. |
| Never reached `in_flight` (declined for a redacted `after` value) | Declined identically again — permanent, not retried (see "Redacted after values are declined," above). |
| No apply record exists yet (first run) | Normal first attempt. |

A `drift_revert` proposal whose every resource is `applied` (across one or
more attempts) is fully shipped; `ubx ship` reports this and exits 0
without writing a new apply record at all — a genuine no-op, not an empty
one.

## Error taxonomy: retryable vs. terminal

- **Retryable**: a transient signal that doesn't rule out the change having
  landed or being about to — `context.DeadlineExceeded` before a response,
  a transport-level reset, `unknown_post_timeout` itself. These feed
  reconciliation and the retry budget; they never immediately fail a
  resource.
- **Terminal**: a real, structured diagnostic from the provider
  (`ApplyResourceChange_Response.Diagnostics`, `Severity: ERROR`) — the
  provider itself said no. Ends that resource's attempt at `failed`
  immediately; no retry is attempted within the same `ship` invocation even
  if retry budget remains (a provider that has already said "this attribute
  is invalid" is not going to change its answer on an immediate retry with
  the same input — retrying is a future-`ubx accept`-cycle concern, not a
  `ship`-loop one).
- **Stale** (a `VerifyFreshness` mismatch) is its own classification,
  distinct from both: it means reality changed, not that the provider
  rejected anything — see "Freshness," above.

## Redaction applies at the apply boundary in both directions

Two independent, complementary rules — one for what comes *out* of an
apply, one for what could go *into* one — both a pure reuse of mechanisms
this codebase already built (UBI-23/24), never a new redaction path:

- **Out**: `provider_result` (whatever attributes `ApplyResourceChange`
  returns) goes through `provider.Redact` — the same
  schema-`Sensitive`-flags-plus-override-table union, same per-ledger
  salt — before ever being written into an apply record
  (`cli/stateadapter.go`'s `ApplyResourceChange`, mirroring `ReadResource`'s
  own redaction call exactly). A live secret is exactly as reachable
  through an apply's returned attributes as through a read's.
- **In**: a `$redacted` restore target is declined outright, never handed
  to a real provider at all — see "Redacted after values are declined,"
  above. `core/executor` itself never redacts anything (it has no
  knowledge of `provider.Redact` or schema `Sensitive` flags at all,
  preserving the same core/provider zero-import boundary UBI-23
  established) — it only recognizes the `$redacted` *shape*
  (`core.IsRedactedValue`) already present in a proposal's own recorded
  content, the same wire-convention-only knowledge `core`'s diffing logic
  already has.

Together these mean the ledger's own security posture — "stores hashes,
never material" — holds at every point the apply boundary touches, not
just at scan/read time.

## Concurrency: the same ledger lock, reused

Two concurrent `ubx ship <same-id>` invocations must not race to pick the
same attempt number or write conflicting apply records. `ship` acquires the
existing `.ubx/lock` PID-file lock (`core`'s `acquireLedgerLock`, built for
UBI-20's `Append` contention) for the "read the highest existing sealed
attempt number for this proposal, decide the next one, create that
attempt's working file" sequence — the same class of check-then-write race
`Append` already closes for concurrent `Accept` calls, one level down (per
proposal, not per stack). Released once the new attempt's working file
exists; the (possibly long-running) apply loop itself does not hold the
lock for its whole duration, so a `ubx scan`/`why`/`status` invocation is
never blocked by an in-progress `ship`, matching UBI-20's existing
"read-only commands are never blocked by ledger-mutating ones" posture.

## Amendment (2026-07-17, UBI-27): shipping resolved `change` proposals

docs/resolver.md's own resolver produces `kind: "change"` proposals
(creates + modifies, no destroys) whose config may carry `$computed`
markers — values genuinely unknowable until apply (a new resource's own
`id`/`arn`, say). Shipping one is a real extension of the same state
machine above, not a different one: the failure states, freshness
re-verification, redaction, and idempotency contract all apply unchanged.
Three things are genuinely new.

### `PlannedState` carries real tfplugin unknowns for `$computed`

Checked directly against the actual library this codebase already uses
(`github.com/zclconf/go-cty/cty/msgpack`), not assumed: `ctymsgpack.Marshal`
already fully supports encoding `cty.UnknownVal(ty)` — a real, distinct
msgpack extension-type encoding (`unknown.go`), not a workaround. The real
gap is upstream of that: `provider/ctyvalue.go`'s existing
`encodeDynamicValue` builds its `cty.Value` tree via `ctyjson.Unmarshal`
straight from a JSON `json.RawMessage` — and JSON has no "unknown" literal
at all, only `null`. A `$computed` marker in a resolved config can never
survive that path; it would decode as some JSON object value (the marker
itself), never as `cty.UnknownVal`.

The fix (session 2+ implementation, pinned here as the design): a new
construction path that walks the resolved config's JSON tree itself,
recognizes a `$computed` marker at a given position, and substitutes
`cty.UnknownVal(<that attribute's cty type, from the schema>)` directly
into the `cty.Value` tree being assembled — bypassing `ctyjson.Unmarshal`'s
ordinary null-mapping for exactly those positions, falling through to the
existing path for everything else. This is the same "strictness lesson"
UBI-26 already found once (cty-msgpack rejects sloppy encoding) applying a
second time, for a different reason: not "the shape must match the
schema exactly" but "there is a real wire-level distinction between null
and unknown, and JSON can only ever express one of them."

**A second, real gap found while actually implementing this (not named in
the paragraph above, which only ever describes the explicit-marker case)**:
an explicit `$computed` marker is not the only place `PlannedState` needs
an `Unknown`. A brand-new resource's own never-referenced attributes — its
`id`/`arn`/`url`, on a from-scratch create nothing in the same batch even
points at — are simply *absent* from the resolver's own emitted `config`
(the resolver only ever marks `$computed` on a **reference** to a
not-yet-known attribute; it has no reason to annotate a resource's own
untouched attributes at all). Left alone, an absent-but-schema-`Computed`
attribute encodes as `Null` — indistinguishable, on the wire, from "this
provider doesn't support this attribute" — and (confirmed empirically the
same way the false start above was) a real SDKv2-vintage provider's
`Apply` returns it as `null`, never actually computing it. The fix
(`provider/ctyvalue.go`'s `encodeUnknownAwareDynamicValue`) treats these
as one mechanism, not two: walking the resolved config against the
schema's own `Block` (not just its flattened `cty.Type`, which erases the
`Computed` flag), any attribute that is either an explicit `$computed`
marker OR schema-`Computed` and simply absent from the config becomes
`cty.UnknownVal`. Verified empirically against a real provider for both
cases (`hashicorp/time`'s `time_static`, `provider/apply_live_test.go`):
`TestApplyResourceChange_RealProvider_TimeStatic_Create` (a genuine
from-scratch create — `PriorState` the real JSON `null` literal, not an
all-null object — every schema-`Computed` attribute the config never set
comes back a real computed value, not `null`) and
`TestApplyResourceChange_RealProvider_TimeStatic_ComputedMarker` (an
explicit `{"$computed": {...}}` marker left in `PlannedState`, as it would
sit for a same-batch dependency not yet applied, also resolves correctly).
This settles docs/resolver-adversarial.md's row 10 both ways.

A related, easily-missed detail confirmed alongside this: `PriorState` for
a genuine create must be the literal JSON `null` token
(`json.RawMessage("null")`), not an empty/absent input — `encodeDynamicValue`'s
existing "empty input defaults to `{}`" convenience (unchanged, still used
for `PriorState` here) would otherwise silently produce an all-null
*object* value, not the true top-level `cty.NullVal(ty)` a real provider's
`Apply` needs to recognize "this doesn't exist yet." `ctyjson.Unmarshal`
already returns the correct `cty.NullVal` given the literal token; the
only change needed was at the *call site* (core/executor), not in
`encodeDynamicValue` itself.

**Resolved empirically (docs/resolver-adversarial.md row 10), not assumed
safe in advance**: whether a real provider's `ApplyResourceChange` — called
directly, still with no separate `PlanResourceChange` phase (the same
shortcut `drift_revert` already takes, docs/executor.md's own
"Constructing `PlannedState` without planning" section) — actually accepts
and correctly resolves a directly-constructed unknown the way it would one
produced by its own real `PlanResourceChange` response. Real Terraform
usage never skips `Plan`; some providers might rely on `PlannedPrivate`
(opaque bytes only a real `PlanResourceChange` call produces) to know how
to resolve an unknown correctly during `Apply`, in ways a
directly-constructed one couldn't satisfy — that concern turned out not to
apply to `hashicorp/time`'s `time_static` (the two tests named above): a
directly-constructed `Unknown`, with no prior `PlanResourceChange` call at
all, resolves into a real, correctly-computed value on `Apply`. This is
one real provider, not an exhaustive survey — the no-separate-plan-phase
shortcut is confirmed to extend safely to at least one genuinely
SDKv2-vintage provider's real unknowns, not proven true of every provider
`ubx` might ever ship a `change` proposal against. If a future provider is
found that requires a genuine `PlanResourceChange` call first, that call
would need to be added to `provider.Provider` for `change` proposals
specifically — `drift_revert`'s own no-plan-needed shortcut would be
unaffected (its restore values are never unknown in the first place).

### Dependent resources: applied outputs feed the next `PlannedState`, mid-walk

Serial, dependency order (docs/resolver.md's own topo-sort) — not the
`(stack, type, name)` canonical order `drift_revert` uses, since a
`change` proposal's resources can genuinely depend on each other.
When a resource with a `$computed`-marked dependent (recorded via
`depends_on`, docs/schema.md's amendment) finishes `applied`, the
executor substitutes its real `provider_result` value into every
sibling's still-pending `PlannedState` wherever that sibling's own config
named it via `$computed`'s `from` pointer — the same `core.ApplyAfter`-shaped
substitution `drift_revert` already performs, generalized from "restore a
recorded value" to "fill in a value that just became known mid-walk."
A resource is never attempted while any of its `depends_on` entries hasn't
yet reached `applied` — the existing per-resource freshness/reconciliation
machinery is unchanged; this only adds a new precondition (dependencies
satisfied) before a resource's own attempt begins at all.

### Apply records: `$computed` replaced by concrete results

An apply record's `provider_result` (already real, redacted, UBI-26)
naturally carries the real, concrete value where the resolved config once
had `$computed` — no new mechanism needed there. What's new: `ubx why`'s
own rendering of a `change` proposal's `delta.creates`/`modifies` should
show the `$computed` marker's *resolved* value once shipped, the same way
it already renders `$redacted` as `(redacted)` rather than the raw marker
— a presentation-layer concern for the session that actually builds this,
not a new ledger mechanism.

## Amendment (2026-07-17, UBI-29): a shipped create registers itself for future discovery

UBI-27 closed with a real, named gap: `shipChange` correctly creates a
resource and durably records its full applied state in the apply record,
but nothing about that record was ever *discoverable* later — `ubx
status`/`ubx why <address>`/a future `ubx scan` all key off
`resolution.inputs[].resource`, which a create never populates for its
own address (see docs/schema.md's own "Amendment: apply-record lookup key
+ Fleet discovery" for the full design). This section pins the one
executor-side change that amendment depends on.

### `shipCreate` records a lookup key on success

The moment a create's `ApplyResourceChange` call succeeds — same place
`ra.ProviderResult` is already set — `shipCreate` also sets `ra.Lookup`:
`{"id": <provider_result.id>}`, or left unset if `provider_result` has no
non-empty `id` (honest, not guessed). This is the *only* new behavior
`ship` itself needs; every reader-side fold (`Fleet`, `FoldState`,
`ProposalsForAddress`, `LastObservedHash`/`LastObservationTime`) lives
entirely in `core`, not here — `shipChange` doesn't need to know anything
changed downstream of it.

### Why gating is per-resource, not per-attempt

The natural worry — "does this let a half-created resource, `kill -9`'d
mid-create, get surfaced as if it were done?" — is already answered by
this package's own existing invariant, not a new check: `ra.ProviderResult`/
`ra.Lookup` are only ever set immediately before `recordTransition(ra,
core.ResourceApplied, "")`. A resource killed before that line has no
`Lookup` recorded at all and its own last transition is `pending`/
`in_flight`/`unknown_post_timeout` — never `applied` — so every
`core`-side discovery fold (which all gate on "this resource's own last
transition is `applied`," not on whether the *enclosing* multi-resource
`ApplyRecord` has been sealed) correctly excludes it. The distinction
matters because sealing describes the whole attempt, and — proven live in
UBI-27's own kill test — one resource in an attempt can be genuinely,
durably `applied` while the attempt itself stays unsealed because a
sibling resource hasn't had its own turn yet. Gating discovery on "sealed"
would have hidden a resource that, in the real world, already exists;
gating on the resource's own state is both correct and sufficient.

## Amendment (2026-07-17, UBI-30): shipping destroys — reversed order, wire mechanics, the destroy state-machine branch

Design only, this session (docs/plan.md's own wedge subsection has the full
session breakdown) — every mechanism named below is session 2+ `core/executor`
work of the same ticket; nothing here is built yet. docs/resolver.md's own
amendment pins the intent/orphan-protection half that produces a resolved
destroy; docs/schema.md's own amendment pins the wire shape and validation;
this section pins how `ubx ship` actually executes one. docs/destroys-adversarial.md
pins the required-outcome program every implementation of this section must
pass.

### One combined topological walk, not two

`shipChange`'s existing `changeNodesOf` (`core/executor/ship.go`, built for
UBI-27) already does the hard part this needs: it decodes
`Delta.Creates`/`Delta.Modifies` into one `byAddr` map keyed by each entry's
own `depends_on`, and calls a single `topoSortAddresses` over the combined
key set — re-deriving execution order from the graph itself, never trusting
stored array position (docs/resolver.md's own array-order-mirrors-dependency-
order convention notwithstanding — the executor independently verifies it,
the same "don't trust stored order, re-derive the guarantee" discipline
"Serial execution in delta order," above, already established for
`drift_revert`'s simpler canonical sort). `Delta.Destroys` extends the exact
same map: each destroy entry becomes a `changeNode` (a new `destroy
*core.DestroyEntry` field alongside `create`/`modify`, exactly one of the
three ever set on a given node), keyed by its own `depends_on` — docs/resolver.md's
own orphan-protection walk populates that list with the **reverse** edge set
(which surviving resources currently depend on this destroy target), not the
forward set a create/modify's own `depends_on` carries.

"Creates forward, destroys reversed" is therefore not two separate walks
glued together — it is what one topo-sort over one graph produces for free,
because `depends_on`'s *meaning* never changes ("this node's own operation
must not execute before every named address's own operation has completed")
while the edge set feeding a destroy node happens to run the opposite
direction from the edge set feeding a create/modify node. A worked example
makes the interleaving concrete: one proposal creates a replacement resource,
re-points a dependent at it, and destroys the original — `create(new)` has no
dependency (eligible to run first); `modify(dependent)` depends_on
`create(new)` (must repoint only after the replacement exists);
`destroy(original)` depends_on `modify(dependent)` (the reverse edge: the
dependent, while it still pointed at `original`, is exactly what made
`original` non-orphan; only once the dependent has repointed away is
destroying `original` safe). One topo-sort produces
`create(new) → modify(dependent) → destroy(original)` — correctly
interleaved across all three arrays, never "all creates, then all modifies,
then all destroys" the way a naive phase-based walk would assume, and never
requiring a proposal-level field beyond what `depends_on` already provides.
`topoSortAddresses`'s own cycle detection (unchanged, reused) still applies
across the combined graph — a cycle spanning a create and a destroy (only
possible from a malformed or hand-tampered proposal file; docs/resolver.md's
own resolver would already have refused to produce one) is refused the same
way, `ErrDependencyCycle` naming the full path.

### Wire mechanics: `ApplyResourceChange` with a null `PlannedState`

Checked directly against the real `tfplugin{5,6}` protocol convention (the
same discipline "Constructing `PlannedState` without planning," above,
already applied for `drift_revert`), not invented: a provider recognizes a
destroy specifically by `PriorState` being non-null while `PlannedState` is
the literal `null` (`cty.NullVal` — the same "the literal JSON `null` token,
not an empty/absent input" distinction UBI-27's own amendment already got
right for a from-scratch create's `PriorState`, the mirror-image case here).
Concretely: `PriorState` is the resource's own freshly re-read live state
(from the mandatory pre-attempt freshness recheck's own `ReadResource` call,
reused — never `delta.destroys[].state`'s resolve-time snapshot directly,
which may be stale by ship time, exactly the same "freshness re-verified,
not just asserted" posture already governing every other kind); `PlannedState`
is `null`; `Config` is `null` (there is no desired end-state config for a
resource being removed — the mirror image of `drift_revert`'s own "`Config`
set identically to `PlannedState`" reasoning, here neither carries anything
since nothing is being configured). No separate `PlanResourceChange` call —
the same v1-wide shortcut every other kind this codebase ships already takes.

### Freshness before every destroy attempt: three-way, not binary

Every other kind's freshness check is binary — matches, or refused as stale.
A destroy target's pre-attempt recheck is three-way, and the third outcome
is not a refusal:

1. **Present, matches the recorded `destroy_target` state exactly**
   (docs/schema.md's own new `resolution.inputs[].kind == "destroy_target"`,
   `observed_hash` re-verified against a fresh read) — proceed: durably write
   `pending → in_flight` (THE invariant, unchanged, reused for a destroy
   exactly as for a create/modify) *before* calling `ApplyResourceChange`, as
   always.
2. **Present, but drifted from the recorded state** — refused, exactly like
   "Stale detected mid-partial-apply," above, generalized: the resource stays
   `pending`, gains a terminal `errors[]` entry for this attempt. This is
   docs/destroys-adversarial.md's "destroy target drifted since acceptance"
   row: destroying a resource that changed since it was signed away would
   mean losing state the operator never actually reviewed — the whole reason
   `delta.destroys[].state` is carried inline in the proposal at all
   (docs/resolver.md's own reasoning) is defeated if the executor ships a
   destroy against a target that no longer matches what was shown at
   sign-time.
3. **Absent already** — short-circuits to a terminal **`already_absent`**
   outcome directly: never reaches `in_flight`, never calls
   `ApplyResourceChange` (there is nothing to destroy). Sharply distinguished
   from outcome 2 — a resource that is already gone carries none of the
   "operator never got to review this" risk that drifted-but-still-present
   does (there is nothing left to lose), so this is a legitimate, low-friction
   terminal success, not a refusal. This is docs/destroys-adversarial.md's
   "destroy of already-absent resource" row, and it is deliberately a
   *different* mechanism from outcome 2's refusal, not a variant of it —
   conflating "gone" with "changed" would either refuse a harmless
   already-done destroy (wrong) or silently ship a destroy against
   unexpectedly-different live state (worse).

Per docs/schema.md's own "Reconciliation, reused" section: this pre-attempt
check, for a destroy resource specifically, is itself recorded as a
`ReconciliationAttempt` (`present_matches` | `present_drifted` | `absent`) —
a genuine reuse of the existing `ResourceApply.Reconciliation` mechanism one
step earlier than its only prior use (post-`unknown_post_timeout`), not a
new field.

### The destroy branch of the state machine: `destroyed` vs. `already_absent`, disambiguated by the prior observation

The core `pending → in_flight → applied | failed | unknown_post_timeout`
machine (the diagram, above) is reused entirely unchanged for a destroy — no
new top-level state. What's new is the *meaning* layered onto
`applied`/reconciliation for a destroy resource specifically, via
`Reconciliation[].Outcome`'s two new legal values:

- A clean `ApplyResourceChange` response (or a post-timeout reconcile-by-query
  that reads back not-found) resolves the resource's terminal state to
  `applied`, `Outcome: "destroyed"` — but **only when the immediately
  preceding `Reconciliation` entry for this same resource recorded
  `present_matches`.** That preceding entry is the pre-attempt freshness check
  itself (above) — it is what makes a bare "not found" read, inherently
  ambiguous in isolation (was it just destroyed, or was it already gone and
  nobody checked?), attributable to this specific attempt: reality was
  confirmed present and matching, immediately before the call, so its absence
  now is this attempt's own doing. "Immediately preceding" is resolved by
  folding the resource's own transition/reconciliation history across the
  `parent` chain of apply records for this proposal (`foldResourceHistory`,
  UBI-27's own mechanism, reused unchanged, not re-derived) — not scoped to
  the current attempt's own array alone: a resource durably written
  `in_flight` in attempt N, then `kill -9`'d before attempt N's own result was
  recorded (docs/destroys-adversarial.md's own "kill -9 mid-destroy, after
  the call" row), has no reconciliation entry of its own yet in the *new*
  attempt N+1 — folding across `parent` finds attempt N's pre-attempt
  `present_matches` check regardless, so N+1's own reconcile-by-query still
  resolves `destroyed`, correctly, rather than falling back to treating a
  bare not-found as fresh `already_absent` just because this particular
  attempt never ran its own pre-check. A resource that never reached
  `in_flight` in any prior attempt (docs/destroys-adversarial.md's "kill -9
  mid-destroy, before the call" row) has no such history to fold over, so its
  next attempt runs an ordinary fresh pre-check instead, exactly as a true
  first attempt would. This is docs/destroys-adversarial.md's "timeout with
  destroy actually landed" row, extended with the disambiguation the plain
  create/modify version of this same mechanism never needed (a create/modify
  reconciles against a specific *value*; a destroy
  reconciles against *presence itself*, which "not found" alone can't
  attribute without the prior observation).
- A post-timeout reconcile-by-query that reads back the *original*
  (pre-destroy, still-present) state resolves `failed` — the call never
  actually landed, exactly the same "match against the original value means
  failed" pattern reconciliation already uses for `drift_revert`, mirrored:
  for a destroy, "the original value" is presence itself, not a specific
  attribute's old value. Docs/destroys-adversarial.md's "timeout with it not
  landed" row.
- Anything else (a third value, or a genuinely unreadable target) resolves
  `still_unknown`, same bounded-retry, terminal-for-this-attempt posture as
  every other kind.
- The **already-absent short-circuit** (above) never enters this branch at
  all — it resolves directly from the pre-attempt check, `Outcome:
  "already_absent"`, without ever reaching `in_flight` or calling
  `ApplyResourceChange`.

`ubx why <address>`'s own rendering (UBI-27's own amendment already
established the convention of rendering resolved/applied values in place of
markers) gains the equivalent duty here: rendering a destroyed address's
terminal apply record should show which of `destroyed`/`already_absent`
actually happened, not just "applied" — the whole point of recording the
distinction is that a human reading the biography later can tell "`ubx` did
this" from "this was already gone by the time `ubx` got here." Presentation-layer
work for the session that actually builds it, not a new ledger mechanism,
the same framing UBI-27's "Apply records: `$computed` replaced by concrete
results" section already used for its own rendering gap. This is also
docs/destroys-adversarial.md's "why on a destroyed address" row: the full
biography, including which of the two outcomes closed the chain.

### Idempotency: a re-run's own destroy behavior

Extending the existing "Idempotency contract" table, above, with the
destroy-specific rows: a resource whose most recent transition is
`applied`/`Outcome: destroyed` or `Outcome: already_absent` is skipped
entirely on re-run, identical to any other kind's `applied` row; a resource
left `still_unknown` re-runs reconciliation first, before any new
`ApplyResourceChange` call, identical to any other kind; a resource refused
for staleness (outcome 2, above) re-verifies freshness fresh on the next
`ubx ship` invocation, exactly as today. Docs/destroys-adversarial.md's
"re-ship after partial destroy" row is this table's own existing
`partially_applied` handling, applied to a mixed create+destroy proposal
unchanged — no destroy-specific idempotency exception exists; the existing
table's rows already cover every case once `destroyed`/`already_absent` are
understood as flavors of `applied`, not new top-level terminal states.

### Concurrency: unchanged

Docs/destroys-adversarial.md's "destroy racing a concurrent scan" row needs
no new mechanism: `ubx scan`/`status --drift` are pure readers against
`FoldState`/`Fleet` and never take the ledger lock (docs/executor.md's own
"Concurrency" section, above, unchanged) — a scan racing an in-progress
destroy either observes the resource still present (pre-`in_flight` or
mid-flight) or already gone (post-tombstone `FoldState` fold, docs/schema.md's
own amendment), both individually consistent reads; there is no window where
a concurrent scan can observe a torn or partially-destroyed state, since the
apply record's own transitions are the only thing that changes and a scan
never reads mid-write apply-record content (`.apply.json` files mid-attempt
have no sealed `id` yet, docs/schema.md's own "Sealed vs. live" section — a
reader either sees the last *sealed* apply record or none, never a
half-written one).

## Out of scope for v1, named so it isn't assumed covered

- Any proposal kind other than `drift_revert` or `change` — **as of UBI-27**
  (`change` shipping is now built: `core/executor`'s `shipChange`, real
  tfplugin unknowns via `provider/ctyvalue.go`'s
  `encodeUnknownAwareDynamicValue`, live-verified against a real two-resource
  AWS chain -- see STATE.md for the full session writeup).
- ~~A shipped create becoming discoverable by `ubx status`/`ubx why
  <address>` afterward~~ — **fixed, UBI-29**: see this document's own
  "Amendment (2026-07-17, UBI-29)" section above.
- Parallel execution — across resources within one proposal, or across
  proposals/stacks. Serial, delta/dependency order, full stop.
- A `--dry-run`/preview mode for `ship` itself — `ubx revert-plan` already
  fills that role, pre-acceptance; once accepted, `ship` executes.
- Automatic rollback on partial failure. A `partially_applied` outcome is
  reported honestly; nothing auto-reverts what already landed. A human
  decides the next step (retry the remainder via another `ship`, or
  re-scan/re-resolve if reality has moved on).
- ~~`delta.destroys`, for a `change` proposal or any other kind — no kind
  this codebase produces today carries a real destroy, and shipping one
  needs its own adversarial thinking (docs/resolver.md's own Scope
  section).~~ — **designed, UBI-30** (see "Amendment (2026-07-17, UBI-30):
  shipping destroys," above); execution code is still session 2+ work of
  that ticket, not built in the session that wrote the design.
