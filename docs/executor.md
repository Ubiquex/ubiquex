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

### Amendment: a JSON-embedded `$computed` template, filled in mid-walk too (2026-07-31, UBI-63 session 2)

`substituteComputed` (`core/executor/ship.go`) originally only handled a
`$computed` marker sitting as the direct decoded value of some
`config[key]` — its `switch` had cases for `map[string]interface{}`/
`[]interface{}` only, and any `string` leaf returned untouched,
unexamined. Found live, the same session: a config attribute the
provider schema types as a plain string but that the resource itself
treats as nested JSON (an IAM policy document) can carry a `$computed`
marker one level down inside that string's own decoded structure — the
resolver now allows this to persist into the signed proposal as a
genuine template (docs/resolver.md's own equivalent amendment; the
resolve-time refusal for this case was removed for `$computed`, kept for
`$secret`), so the executor needs a matching ship-time fill-in step, or
that template would ship to `ApplyResourceChange` still carrying the
literal, un-substituted marker text.

Fixed with a new `case string:` in `substituteComputed`'s own switch: it
`json.Unmarshal`s the string, and — gated by a new `containsComputedMarker`
helper (mirroring `core/resolver`'s own `containsMarker`, scoped to
`$computed` alone) — only recurses into the decoded structure (through
this same `substituteComputed` function; an embedded marker resolves no
differently from a top-level one) and re-encodes if a `$computed` marker
is actually present. A string that merely happens to parse as JSON but
carries no marker is returned completely untouched, byte for byte —
symmetric with `core/resolver`'s own resolve-time discipline for the
identical reason (this is a template-fill step, not a general
"reformat every JSON-shaped string" pass).

Two same-batch dependencies feeding two separate embedded markers in the
same string (a role's inline policy naming both a queue's ARN and a
bucket's ARN) resolve independently and correctly — `substituteComputed`
recurses uniformly through the whole decoded tree regardless of how many
markers it contains or how they're nested. Crash recovery is unaffected
by this amendment: `resultsByAddr`'s own seeding from durable apply-record
history (this section, above) is exactly what a re-derived dependency's
real output comes from on a killed-and-restarted `ubx ship`, whether the
dependent's own marker sits at the top level or embedded inside a string
template — the string-leaf case is just another node `substituteComputed`
walks, reading from the identical `resultsByAddr` map every other case
already does.

### Amendment: a lookup key needs more than "id" sometimes (2026-07-31, UBI-63 session 2)

`core.DeriveLookupFromResult` used to assume "id" alone is always
sufficient to re-find a resource later — true for every type this
codebase had touched, until confirmed otherwise live: `aws_iam_role_policy_attachment`
really does declare an "id" attribute in its own real schema, but its
own real AWS `ReadResource` implementation needs `role`/`policy_arn`
present in the `current_state` it's handed to construct a valid API
call at all. A bare `{"id": ...}` lookup left both blank on a
subsequent destroy/scan read, producing a real, structured AWS error
("roleName is invalid") — a resource that shipped correctly the first
time became un-re-readable afterward.

Fixed generally: `DeriveLookupFromResult(result, requiredAttrNames)`
now also captures every schema-`Required` attribute's own real,
non-empty value alongside "id" (never `Optional`/`Computed` ones —
those can legitimately be absent or provider-defaulted, and baking one
in here would risk a stale value in what's supposed to be a stable
identity key). `core`/`core/executor` both stay provider-import-free
(this section's own established boundary) — the one place a concrete
schema is genuinely in scope is `cli/stateadapter.go`'s own
`ApplyResourceChange` (already the one place that redacts `Sensitive`
attributes for the identical reason), so `executor.Applier`'s own
`ApplyResourceChange` method gained a new `lookup json.RawMessage`
return value: the adapter computes it there, using the real schema's
own `Required` attribute names, and `shipCreate` uses it directly,
falling back to the old id-only derivation only if the Applier returns
nothing (a legitimate answer, or an implementation that hasn't been
updated for this). `shippedCreateFold`'s own legacy-record fallback
(above) is unaffected — an apply record old enough to predate the
`Lookup` field at all has no live schema in scope to consult, an
honest, accepted narrowing for that one specific path.

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

- ~~A clean `ApplyResourceChange` response... resolves the resource's
  terminal state to `applied`, `Outcome: "destroyed"` directly.~~
  **Superseded by "Amendment (UBI-44): universal post-destroy read-back,"
  below** — a clean `ApplyResourceChange` response is no longer sufficient
  on its own; it now earns `destroyed` only via the same post-destroy
  read-back reconcile-by-query a post-timeout resolution already used. The
  paragraph below describes the read-back's own resolution logic, which is
  unchanged; what changed is *when* it runs — universally now, not just
  after an ambiguous `Apply` result. A post-timeout reconcile-by-query
  that reads back not-found resolves the resource's terminal state to
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
half-written one). One honest caveat on this section's own "post-tombstone
`FoldState` fold" phrase, found while actually implementing (session 3):
`FoldState`'s own tombstone-folding (docs/schema.md's "Amendment:
destroys" — a fully-destroyed address folds back to absent) is **not yet
built** — this session shipped `core/executor`'s own destroy execution,
not that follow-on `core` change. A concurrent scan racing a real destroy
today still observes the resource "present" via `FoldState` even after a
successful destroy ships, until that separate change lands; the apply
record's own transitions (what this section's own "no torn read" claim
actually rests on) are unaffected by this gap and remain correct regardless.

**Two things found while actually implementing this (`core/executor`,
UBI-30 session 3), not assumed correct from the design above alone:**

- **The combined topo-walk's own dependency-satisfied check
  (`shipChange`'s `resultsByAddr` map) originally required a non-empty
  `ProviderResult` to consider a dependency "done" — which a destroy can
  never have** (there is nothing left to store once a resource is gone,
  by design). This silently left anything `depends_on`-ing a destroyed
  resource wrongly blocked forever on every subsequent `ubx ship` re-run
  (found by this session's own "re-ship after partial destroy" hermetic
  test, not assumed safe). Fixed to gate purely on the resource's own
  terminal `applied` state, the same signal the rest of the state machine
  already treats as authoritative — `ProviderResult` being empty is now
  simply carried through as-is (nil for a destroy, real bytes for a
  create/modify), never used as a proxy for "did this complete."
- **`fakeApplier` (`core/executor/ship_test.go`) and the real subprocess
  fixture (`provider/internal/fakeprovider`) both needed genuine new
  mechanics, not just a new script value**: a destroy is identified by
  `PlannedState` decoding to a real null value (never an "id" to key
  scripted behavior off, the way every other apply step already does),
  and — unlike every other apply step this fixture models — correctly
  answering "is this resource still there" *after* a destroy requires the
  fixture to remember what it did across the precheck → apply →
  (maybe) reconcile sequence, something no other scripted behavior here
  ever needed (every other case is a pure function of whatever the caller
  supplies on each call). `fakeApplier` already had a natural home for
  this (its own in-memory `resources` map, keyed by id, already mutated on
  apply); the real subprocess fixture gained its first piece of
  cross-call, process-lifetime state for the same reason
  (`destroyedIDs`), never previously needed since every other fixture
  behavior here is stateless by design.

### Session 4: `FoldState`'s tombstone-fold and `ubx why`'s destroyed/already_absent rendering

Both gaps session 3 named and deliberately left open are closed this
session — the two real, remaining pieces of docs/schema.md's own
tombstone posture.

**`core.Ledger.FoldState` folds a fully-destroyed address back to
absent.** A new `core.shippedDestroyFold(proposalID, addr)`
(`core/apply.go`) mirrors `shippedCreateFold`'s own per-resource, not
per-attempt, gating exactly: found is true only if this resource's own
most recent transition is `ResourceApplied` — for a `Delta.Destroys`
entry specifically, reaching `ResourceApplied` at all only ever happens
via `shipDestroyNode`'s own `destroyed`/`already_absent` determination, so
that alone is sufficient to know the address is tombstoned (no need to
additionally inspect *which* of the two — that distinction only matters
for rendering, below). `FoldState`'s own per-proposal walk gained a third
loop, alongside its existing Creates/Modifies ones: for a `Delta.Destroys`
entry matching `addr`, a shipped destroy resets `current = nil; found =
false` right there in the walk, *continuing* rather than stopping — a
later proposal's own create for the identical address (a real, legitimate
"tear down, rebuild under the same name" lifecycle) re-seeds `current`/
`found` from scratch, exactly as if the address had never been recorded
before that later create. The ledger's own chain is never rewritten to
make this true; only `FoldState`'s derived, current-truth view folds
through the tombstone — `ubx why`'s full biography (below) is unaffected
and keeps showing the destroy proposal, and everything before and after
it, unchanged.

**`core.Ledger.Fleet` (what `ubx status`/`ubx scan`'s own ground truth,
via `cli/status.go`/`core/scan.go`, already exclusively consumes) excludes
a tombstoned address from "what to actively watch."** The same single
chronological pass Fleet already walks gained a `tombstoned` map,
updated in lockstep with `latest`: a shipped `Delta.Destroys` entry marks
an address tombstoned; any *later* resolution-input touch or shipped
create un-marks it (the identical recreate-under-the-same-address case
`FoldState` now handles, kept consistent one level up). Entries whose
address is tombstoned are filtered out of Fleet's returned slice
entirely. **`cli/status.go` and `core/scan.go` needed zero changes** —
confirmed, not assumed — the exact repeat of UBI-29's own finding: both
already consume `Fleet`/`FoldState` as their only path to "what does the
ledger know," so fixing those two functions made the whole read path
correct for free, a second time.

**`ubx why`'s own rendering gained two real additions, both purely
presentation-layer — no new ledger mechanism, exactly as this document's
own session-3 addendum anticipated.** First, `Delta.Destroys` itself was
never rendered at all before this session — neither the single-proposal
view (`renderProposal`) nor the resource-chain view
(`renderProposalCompact`) printed anything for it, unlike `Delta.Modifies`
(`renderModifies`, unchanged). New `renderDestroys` prints each entry's
address always, and (single-proposal view only) every attribute of its
carried-inline full state (docs/resolver.md's own reasoning for why that
state is carried at all — a human reviewing a destroy needs to see what's
being lost, and `ubx why` re-reviewing it afterward needs the same). Second,
a destroy's own terminal `applied` transition read identically to a
create's or modify's — "applied at `<time>`" — with the actual
`destroyed`/`already_absent` distinction buried in a separate
`reconcile:` line a reader had to already know to look for and interpret.
New `destroyOutcome` inspects a resource's own last `Reconciliation` entry
(`""` for anything but the two destroy-terminal values, which no
create/modify path ever produces) and annotates the transition line
itself: `applied at <time> (destroyed)` or `applied at <time>
(already_absent)`, so the distinction this whole amendment exists to make
observable is actually visible at a glance, not just present in the raw
data.

**Hermetic coverage**: `core/destroy_tombstone_test.go` (new) —
`TestFoldState_ShippedDestroy_TombstonesAddress`,
`TestFoldState_ShippedDestroy_AlreadyAbsentOutcome_AlsoTombstones` (both
outcomes tombstone identically), `TestFoldState_UnshippedDestroy_DoesNotTombstone`,
`TestFoldState_DestroyKilledMidAttempt_NeverTombstones` (an unresolved,
unsealed destroy attempt must never tombstone — the per-resource gating's
own adversarial edge), `TestFoldState_RecreateAfterDestroy_ReSeedsFresh`,
`TestFleet_ExcludesTombstonedAddress`,
`TestFleet_RecreatedAddress_ReappearsAfterDestroy`. `cli/why_destroy_test.go`
(new) — `TestWhy_RendersDestroyedResource` (a full, real `ubx scan` →
`accept` → `resolve` → `accept --confirm-destroys` → `ship` → `why` chain
against the actual fakeprovider subprocess, both view forms) and
`TestWhy_RendersAlreadyAbsentDestroy` (hand-built directly via `core`,
since no two separate CLI invocations can share one subprocess's
in-memory destroyed-state — confirmed as a real constraint, not assumed:
each `ubx` invocation launches its own fresh provider subprocess, so
"already absent before ubx even tried" can only be demonstrated for real
within a single `ubx ship` invocation, exactly what
`core/executor`'s own hermetic `TestShipDestroy_AlreadyAbsent_ResolvesWithoutInFlight`
(session 3) already proves). Full repo `go build ./...`/`go vet
./...`/`gofmt -l .`/`go test ./... -race -count=1` clean, no regressions.

### Session 5: a real `PlanResourceChange` call before destroy's own `Apply` — the no-plan-phase shortcut does NOT extend to destroy

Session 3's own "Constructing `PlannedState` without planning" section (and
the `time_static` finding folded into it) established that `change`'s
create/modify paths can skip a real `PlanResourceChange` call safely,
confirmed against one genuinely SDKv2-vintage provider. This session found,
empirically, against a real, complex provider (`terraform-provider-aws`
6.54.0) that **destroy is the exception**: calling `ApplyResourceChange`
directly for a destroy, with no prior `PlanResourceChange` call, does not
error — it returns success — but the resource is never actually deleted.
Confirmed via direct debug output: `ApplyResourceChange`'s own response
`NewState` came back as the full, unchanged prior resource, not an absence.
The proximate cause: a genuine destroy `Apply` call, against an SDKv2-shimmed
provider, requires the opaque `PlannedPrivate` bytes only a real `Plan` call
produces — without them, the shim has no diff to act on and silently no-ops
rather than deleting. This is a real, load-bearing correction to session 3's
"no separate plan phase" design, not a quiet patch: **destroy's own path now
requires a genuine `PlanResourceChange` call, unconditionally, immediately
before its own `Apply`** — create/modify's existing no-plan-phase shortcut is
otherwise unaffected.

**`provider.Provider` gained a real `PlanResourceChange` method** (both
`v6Provider` and `v5Provider`), alongside `ApplyResourceChange`'s signature
extended with a `plannedPrivate []byte` parameter it now threads straight to
the wire (`tfplugin6.ApplyResourceChange_Request.PlannedPrivate`, its v5
mirror). `core/executor`'s own `Applier` interface (`ship.go`) mirrors both
changes; every existing create/modify call site passes `nil` for
`plannedPrivate` (`ApplyResourceChange` for a non-destroy has never used it
and still doesn't). `shipDestroyNode` (`core/executor/ship.go`) now calls
`app.PlanResourceChange` unconditionally right after fetching the resource's
schema and *before* recording its own `in_flight` transition — Plan is
read-only, so a Plan failure means the risky `Apply` never runs at all and
the resource correctly never leaves `pending`; a `PlanResourceChange`
diagnostic error classifies exactly like an `Apply` one (`TerminalError` vs.
retryable, the same taxonomy this document's error-classification section
already establishes). `cli/stateadapter.go`'s adapter wires both changes
straight through.

**A second, independent bug surfaced while wiring this up, in
`provider/ctyvalue.go`'s own `encodeUnknownAwareDynamicValue`** (session 3's
UBI-27 addition): given a literal top-level JSON `null` — exactly what
`shipDestroyNode` sends as `PlannedState`/`Config` for a destroy — it never
produced a genuine top-level `cty.NullVal(ty)`. Instead, decoding `null` into
a Go `interface{}` yields a bare `nil`, and the existing per-attribute walk
(`encodeBlockValue`) always builds an *object* from that: each `Computed`
attribute becomes `cty.UnknownVal`, everything else `cty.NullVal` — never a
true top-level null. Two independent problems followed from this, both found
via this session's own hermetic re-verification (the fakeprovider fixture's
new `PlanResourceChange` handler, echoing its request's `ProposedNewState`
straight back, is what exposed it — `decodeDynamicValue`'s `ctyjson.Marshal`
rejects any value carrying an `Unknown` attribute, since JSON has no way to
represent "unknown"): first, a destroy's own `PlannedState`/`Config` never
actually reached the wire as the real destroy signal a provider expects;
second, `provider/internal/fakeprovider`'s own `destroyRequestID` — which
detects a destroy by checking `plannedVal.IsNull()` — could never observe a
true top-level null either, for the identical reason. Fixed by special-casing
a literal top-level JSON `null` input at the top of
`encodeUnknownAwareDynamicValue`, encoding it directly as `cty.NullVal(ty)`
rather than falling through to the per-attribute walk — everything else
about that function (the `$computed`-marker and absent-and-`Computed`
handling for a genuine object) is unchanged. Whether this same
never-a-real-null gap contributed to the original live-AWS no-op (alongside
the missing `PlannedPrivate`) was not separately isolated — both were fixed
together before the live finale was redone — but it is a real, independently
confirmed defect in its own right, not a hypothesis.

**`provider/internal/fakeprovider`'s own fixture gained a matching
`PlanResourceChange` handler** (both `fakeProviderServerV6`/`V5`), returning
a fixed, non-empty `PlannedPrivate` and echoing `ProposedNewState` back as
`PlannedState`. Its existing destroy branch inside `ApplyResourceChange` now
*requires* `PlannedPrivate` non-empty, returning a clear diagnostic instead
of silently no-oping if it's missing — deliberately stricter than the real
provider's own silent-no-op behavior this session found, so a future
regression (`ubx` skipping `PlanResourceChange` again) fails loudly as a
test rather than passing while quietly reproducing the exact bug this
session fixed. `core/executor/ship_test.go`'s own in-process `fakeApplier`
gained the identical strictness on its `PlanResourceChange`/
`ApplyResourceChange` fakes, for the same reason.

**Hermetic coverage**: no new test files — this session's fix is exercised
by the full existing suite (`core/executor`'s eleven adversarial-row tests,
`cli/why_destroy_test.go`'s two destroy-rendering tests, `provider`'s own
`ApplyResourceChange`/`PlanResourceChange` round-trip tests), all of which
now exercise the real `PlanResourceChange` → `ApplyResourceChange` sequence
for every destroy path rather than skipping straight to `Apply`. Full repo
`go build ./...`/`go vet ./...`/`gofmt -l .`/`go test ./... -race -count=1`
clean, no regressions.

## Amendment (2026-07-18, UBI-43): multi-provider stacks — one walk, a lazily-launched client pool

`core/executor`'s own `Applier` — the one provider client every ship node
function (`shipCreate`/`shipModifyNode`/`shipDestroyNode`, plus
`reconcileLoop`/`reconcileDestroyLoop`) closes over — has been exactly one
value per `ubx ship` invocation since UBI-26. docs/architecture.md
§Multi-provider stacks decided the shape; this is `core/executor`'s own
half, mirroring docs/resolver.md's own amendment.

### The client pool: keyed by `(source, version)`, launched on first use

A pool replaces the single `app Applier` parameter — a
`map[providerKey]Applier`, keyed by exactly the `{source, version}` pair
docs/schema.md's own amended
`provider` field records on each node (docs/resolver.md's own amendment:
the resolver already resolved and signed this at resolve time; the
executor never re-infers it, only launches what was recorded). A provider
is launched the first time any node needing it comes up in the walk —
never eagerly at the start, and never more than once for a given
`(source, version)` pair within one `ubx ship` invocation, reusing exactly
`provider.Acquire`/`provider.Launch`'s own existing per-source-version
resolution (`UBX_PROVIDER_MIRROR` → cache → registry) that today's
single-provider path already uses, just keyed N ways instead of once. The
whole pool is torn down (every launched client `Close()`d) when the `ubx
ship` invocation ends, not per-node — a provider serving three nodes in
one walk is launched once and reused for all three.

### One combined topological walk — unchanged, still not two

**Confirmed by reading the actual graph-building code, not assumed**: the
combined topo-sort UBI-30's own amendment already established ("creates
forward, destroys reversed, interleaved with modifies... one combined
topo-sort over one graph produces this for free") stays exactly as it is.
The walk itself has never consulted type or provider — it walks canonical
addresses and `depends_on` edges. Multi-provider changes *which client*
each step in that same walk calls (`pool.Get(ctx, node's own recorded
provider)` instead of the one closed-over `app`), never the walk's own
shape or order. A `$ref`/`$computed` edge from an `aws_db_instance` node
into a `helm_release` node is walked identically to a same-provider edge —
"outputs flowing across provider boundaries exactly as within one"
(docs/architecture.md's own wording) is already true of the existing
`resultsByAddr`/`ApplyAfter` substitution mechanism, which operates on
plain Go values with no provider-awareness at all. No new mechanism
needed here, confirmed rather than assumed.

### Provider launch failure mid-walk: a per-node terminal error, not a whole-walk abort

A launch failure for provider *X* (bad credentials, network failure
acquiring the binary, a crashed handshake) terminally fails every
still-pending node whose recorded provider is *X* — `errors[]` naming the
launch failure, never reaching `in_flight` — while nodes already
in-flight or still pending against a *different, already-launched-fine*
provider proceed in their own dependency order, unaffected. This is not a
new failure category: it's `partially_applied`, the same honest, no-auto-
rollback outcome this document's own "Out of scope" section already
commits to for any partial failure — a provider that fails to launch is
just one more reason a specific node can't proceed, reported exactly like
any other terminal error would be. A human decides the next step (fix
whatever blocked provider *X*'s launch, then re-run `ubx ship` — the
already-applied nodes are skipped via the existing idempotency contract,
untouched).

### Freshness, reconciliation, and destroy's three-way precheck: all per-provider, no new mechanism

Every existing per-resource mechanism this document already specifies —
the freshness recheck before every modify attempt, destroy's own
three-way precheck (present-matching/present-drifted/absent), reconcile-
by-query after an ambiguous timeout — already operates through whichever
`Applier` a given node uses. Multi-provider changes nothing about *how*
these work, only which pool entry supplies the `Applier` for a given
node's own turn. A cross-provider `$ref` chain (an `aws_db_instance`'s
real applied `endpoint` feeding a `helm_release`'s `values`) is not a new
case for this substitution either — it already happens through plain Go
values (`resultsByAddr`), with no provider identity threaded through the
substitution itself.

### Scan/status/fleet: grouping by each resource's own recorded provider

`ubx scan --type/--name` (one resource, one provider) needs no change —
scanning a single, already-known-type resource is inherently a
single-provider operation regardless of how many providers a stack
declares. `ubx scan --all` (bulk onboarding) and `ubx status --drift`
(the whole-fleet walk) generalize the same way the executor's own walk
does: group the fleet's resources by each one's own recorded provider
(read from the ledger's own history — a shipped `change` node's `provider`
field, or, for an adopted/drift-recorded resource that predates this
amendment and never got a `provider` field written, the single provider
that invocation's own `--source`/`--provider` implied, same reasoning as
docs/schema.md's own "no `schema_version` bump" justification), then walk
each group against its own lazily-launched pool entry — one provider
launch per distinct source in the fleet, not one per resource. A single
`--source`/`--provider-version` flag can no longer express "the provider"
for a genuinely multi-provider fleet; see docs/resolver.md's own
retirement-staging section for the deprecation plan (deprecated, staged,
never a breaking cutover in one session) — `ubx status --drift`/`ubx scan
--all` follow the identical staging, not a separate one.

### Session 3 (2026-07-18): the client pool, real code, hermetic

New `ApplierPool` interface (`ship.go`): `Get(ctx, source, version)
(Applier, error)`, lazily launching and reusing exactly as the amendment
above describes. `core/executor` never launches a provider itself, the
same provider-import-free boundary `Applier` already establishes — a
concrete implementation belongs in `cli/`, the one place that already
bridges both packages. `SingleApplierPool(app Applier) ApplierPool` wraps
one already-launched Applier into the trivial, always-succeeds pool a
single-provider stack needs (today's `--provider`/`--source` CLI flow,
unchanged behavior) — `Get` ignores its own source/version arguments
entirely, since there's only one provider to route to regardless of what
was asked.

`Ship`'s own signature changed from `app Applier` to `pool ApplierPool`.
`shipDriftRevert` did **not** change — a drift_revert is single-provider
by construction (docs/resolver.md's own reasoning: it predates this whole
concept, and `core/scan.go` never records a provider on its own
`Modification` entries) — `Ship`'s own dispatcher resolves its one
Applier from the pool once (`pool.Get(ctx, providerSource, "")`) before
calling `shipDriftRevert` unchanged. `shipChange`'s own signature changed
the same way; its own per-node loop now resolves each node's `Applier`
via `pool.Get(ctx, provSource, provVersion)` — reading the winning values
off the new `changeNode.provider` field (copied from whichever of
create/modify/destroy is set; nil for a proposal resolved before this
amendment falls back to the invocation's own `providerSource`, version
`""`, exactly what `SingleApplierPool`'s one entry already answers
regardless of the pair asked for) — immediately before dispatching to
`shipCreate`/`shipModifyNode`/`shipDestroyNode`, which are themselves
**completely unchanged**: they still take one plain `Applier` directly,
now simply the correct per-node one the loop already resolved. `createNode`
gained the matching `Provider *core.ProviderRef` JSON field (`core/resolver`
already emits it; `core/executor` previously just never read it back).

A pool-lookup failure is recorded exactly like the loop's existing
"blocked: dependency ... has not applied" case — `recordTransition`
pending, `recordError` naming the launch failure as terminal,
`resourcesFailed++`, persist, `continue` — never `return`, so every other
node in the same walk, including ones against a different,
already-launched-fine provider, proceeds in its own turn unaffected
(docs/multi-provider-adversarial.md row 4).

**A real, named gap found while implementing, not silently assumed
covered**: `providerConfig` itself stays a single value threaded through
every node's own `Configure`/freshness calls, regardless of which
provider a node actually routes to — correct for today's single-provider
CLI flow (the only config that exists), but not yet correct for a
genuinely multi-provider stack, where AWS's own region config and a
Helm/Kubernetes provider's own config are never going to be the same
JSON blob. This amendment's own scope was the client pool specifically
(docs/multi-provider-adversarial.md rows 4/6/7); per-provider
*configuration* is real, remaining work for the same `.ubx/config`
`providers`-table-wiring session already queued — named here so it isn't
mistaken for solved.

**Hermetic coverage** (`core/executor/multiprovider_test.go`, new): row 4
(a provider launch failure mid-walk fails only that provider's own
nodes, `partially_applied`, a re-run resumes correctly and never
re-launches the already-fine provider); row 6 (`kill -9` between
providers — simulated by a first `Ship` call whose second provider never
launches at all, indistinguishable in the ledger from a genuine launch
failure, followed by a second `Ship` call with a completely fresh pool:
the already-applied node's own provider is never asked for again, zero
`Get` calls; the never-attempted node's provider launches for the first
time, exactly one `Get` call, and proceeds through an ordinary fresh
pending→in_flight→applied cycle); row 7 (two already-signed modifies
against two different providers, one drifts out-of-band before `ubx
ship` reaches it — refused at its own freshness check, live state left
untouched — the other, against a completely different, undrifted
provider, lands normally, unaffected). A new `fakeApplierPool` (real,
multi-entry, keyed by `source@version`, with per-key launch-failure
scripting and call counters) stands in for `SingleApplierPool` in these
tests specifically, since a single-entry pool can't express "provider B
fails while provider A succeeds" at all. All 35 pre-existing hermetic
`Ship(...)` call sites (`ship_test.go`/`destroys_test.go`) updated
**mechanically** via a scripted `sed` transform (every one already passed
the identical `fake` variable, wrapped now in `SingleApplierPool(fake)`),
verified to preserve existing behavior unchanged — all still pass.
`cli/ship.go`'s own one call site does the identical one-entry wrap — no
CLI-visible behavior change this session, same reasoning
docs/resolver.md's own session 2 already established. Full repo `go
build ./...`/`go vet ./...`/`gofmt -l .`/`go test ./... -race -count=1`
clean, no regressions.

### Session 5 (2026-07-18): `ubx status --drift`/`ubx scan --all` fleet-grouping — the last single-provider surface closed

Closes the one gap session 3-4's own "Out of scope" note named explicitly:
`core.Ledger.Fleet` now carries each resource's own recorded provider
(`FleetEntry.Provider *core.ProviderRef`), read back the identical "most
recent wins, falls back to the shipped create's own recorded value"
precedence `Lookup` already established — a `KindChange` proposal's own
`Delta.Modifies`/`Delta.Destroys` entry for the address if the winning
proposal names one directly (`core/fleet.go`'s new
`nodeProviderForAddress`), else whichever provider originally created the
address (`createNodeProvider`, mirroring `createNodeAddress`'s own
permissive decode). `nil` for a resource this ledger only ever adopted or
drift-recorded — `core/scan.go`'s own `Modification`/adoption-create nodes
never carry a provider field at all, matching every prior amendment's own
"additive, no `schema_version` bump" posture.

`resolver.InferProvider` (previously package-private `inferProvider`) is
now exported — the exact mechanism a brand-new resource's own resolve
already uses to answer "which declared provider owns this type," reused
verbatim rather than reinvented in `cli/` for a legacy Fleet entry with no
recorded provider of its own.

**`cli/status.go`/`cli/scanall.go` both branch on `cfg.Providers`**,
mirroring session 4's own `cli/resolve.go`/`cli/ship.go` convention
exactly (`warnIfLegacyProviderFlagsGiven`, empty falls back to today's
`--provider`/`--source` flow byte-for-byte unchanged). `status.go`'s own
walk is genuinely mixed: an entry with its own recorded `Provider` routes
straight to `pool.Get(source, version)`, no inference at all; an entry
with `Provider == nil` (adopted/drift-only history) triggers
`declaredProvidersForInference` — built lazily, once, only on the first
such entry encountered, launching every declared provider via the same
already-open pool (never a second launch for an entry whose provider was
already resolved via `pool.Get` for an earlier entry). `scanall.go`'s own
walk has no such mix — every tfstate-enumerated resource has zero ledger
history by construction, so inference runs eagerly for every one, built
once up front rather than lazily.

**New `cli/schemainspector.go`'s `resourceTypeSchemaInspector`**: a second,
narrower `resolver.SchemaInspector` adapter alongside the existing
`schemaInspectorAdapter` — backed directly by the type-erased
`map[string]any` `executor.Applier.Schema()` returns (each value a
concrete `*provider.Schema` boxed as `any`), not the concrete
`*provider.Schemas` the existing adapter needs and a pool-launched
`Applier` never hands back. Implements only `HasType` for real (a map
lookup); `IsComputed`/`IsSensitive` are harmless always-false stubs,
confirmed sufficient by reading `InferProvider`'s own body — it never
calls either. This is what lets `declaredProvidersForInference`
(`cli/providerpool.go`) reuse the SAME already-launched pool entries for
inference, rather than launching every declared provider a second time
just to answer a schema question.

**Shared classification helpers** (`cli/status.go`): `classifyFleetEntry`/
`unreadableNoLookup`/`unreadableProviderUnavailable` factor the
clean/drifted/unreadable `core.RunScan` classification (and its exact
human/JSON wording) out of the old single-provider-only inline loop, so
both the single- and multi-provider walks report identically-worded
outcomes rather than two copies drifting apart over time.
`unreadableProviderUnavailable`'s own wording deliberately matches
`shipChange`'s `"provider unavailable: %v"` (session 3's own error
taxonomy) — the same underlying condition (a declared provider that
wouldn't launch, or a type no declared provider owns), surfaced from a
different call site.

**Hermetic coverage**: `core/fleet_provider_test.go` (5 new tests) proves
the Provider-precedence logic in isolation — from a shipped create, from
a later modify (current touch wins over stale history), nil for an
adopted resource, persistence through a later provider-less drift touch,
and from an accepted-but-unshipped destroy. `cli/multiprovider_fleet_test.go`
proves `resourceTypeSchemaInspector`'s `HasType` correctness and
`declaredProvidersForInference`'s own lazy-launch-once/cached-on-reuse/
launch-failure-propagates behavior, using the same injectable `launchFunc`
seam `cli/providerpool_test.go` already established — never a real
provider binary or network access. `classifyFleetEntry`'s own clean/
drifted/unreadable classification is a pure extraction of logic
`cli/status_test.go`'s existing 8 cases already exercised end-to-end
through the single-provider branch (which now calls the identical
helper) — all 8 still pass unchanged, proving the extraction didn't
alter behavior.

**Live-verified against the real built binary**, the same
`UBX_PROVIDER_MIRROR`-plus-wrapper-script technique session 4's own
verification used, this time driving `provider/internal/fakeprovider`'s
`conformance-v6` mode with two genuinely distinct `FAKEPROVIDER_RESOURCE_TYPE`
values (`aws_db_instance` for a `hashicorp/aws@6.60.0` mirror entry,
`time_static` for `hashicorp/time@1.0.0`) against a real `.ubx/config`
`[providers]` table declaring both: `ubx resolve` on a `time_static`
create correctly inferred `hashicorp/time` with neither `--provider` nor
`--source` given at all; `ubx status --drift` on a legacy-adopted
`aws_db_instance` entry (`Provider == nil`) correctly launched both
declared providers, inferred `hashicorp/aws` as the sole owner, and
reported `clean`; a real out-of-band mutation (`FAKEPROVIDER_MUTATE_ATTR`)
against that same live subprocess was then correctly reported `drifted`,
exit code 1; `ubx scan --all` against a two-provider `.ubx/config` and a
one-resource tfstate file correctly inferred and routed to
`hashicorp/aws` as well. (A real `ubx ship` of the `time_static` create
wasn't reachable in this smoke test — `conformance-v6` mode has no
`ApplyResourceChange` handler at all, session 4's own already-documented
limitation; the routing/inference machinery this session actually built
is what's under test, and it's what an entry's own recorded `Provider`
short-circuits at ship time regardless — proven separately, hermetically,
by `core/fleet_provider_test.go` and session 3's own `core/executor`
suite.)

Full repo `go build ./...`/`go vet ./...`/`gofmt -l .`/`go test ./... -race
-count=1` clean throughout.

### Session 6 (2026-07-18): the live finale — real AWS + real GCP, one signed proposal, drift on both, both attributed

The last remaining item this arc's own "Out of scope" note named: a
genuinely multi-provider stack shipped on real infrastructure, not
fixtures. `hashicorp/aws@6.54.0` + `hashicorp/google@7.40.0`, declared in
one real `.ubx/config` `[providers]` table, project `personal-273114`
(the same real, billing-enabled GCP project UBI-21's own live gcpaudit
verification used).

**A real design change made mid-session, not silently absorbed**: the
session originally planned to use `hashicorp/time`'s `time_static` as the
second provider (a decision an earlier `AskUserQuestion` had already
settled). Before spending real infrastructure time on it, a direct
empirical probe against the real `hashicorp/time` binary found something
the earlier decision hadn't anticipated: `ReadResource` given only
`{"id": "..."}` — the *universal* shape `core.DeriveLookupFromResult`
derives for every resource type, no exceptions — returns every attribute
except `id` as `null`. Not "attribution comes back unattributed" (the
anticipated, accepted tradeoff), but "drift detection itself is
structurally impossible" — `ubx status --drift`/`ubx scan` would report
permanent, meaningless null-diffs for any shipped `time_static` resource,
never a real signal. Flagged to the user rather than proceeding on a
premise just discovered false; the user chose to switch to a real second
cloud provider instead (GCP), the option the original decision had
explicitly set aside pending confirmed credentials — confirmed available
this session (`gcloud`'s own Application Default Credentials, already
authenticated against `personal-273114`).

**The stack**: `aws_sqs_queue.ubi43-payments-queue` (tags, a real
`Computed` `arn`) + `google_service_account.ubi43-payments-svc` (whose
`description` — a real, mutable, non-`Computed` field — holds a `$ref` to
the queue's own `arn`). Resolved with no `--provider`/`--source` at all
(`cfg.Providers` non-empty); the resolved proposal correctly inferred
`hashicorp/aws` for the queue and `hashicorp/google` for the service
account, and correctly recorded the real cross-provider `depends_on`.
Accepted and shipped as ONE signed proposal — both resources reached
`applied`. Verified independently, directly against each cloud's own API
(never trusting `ubx`'s own report alone): the queue is real
(`aws sqs get-queue-url` resolves it); the service account's own
`description`, read back via the real IAM API, is byte-identical to the
queue's own real, applied ARN.

**A real, unplanned finding surfaced immediately by drift-checking the
freshly-shipped queue**: `ubx status --drift` reported it `drifted`
before any out-of-band change was made at all. Root cause, confirmed by
comparing the apply record's own `provider_result` against a fresh live
read: `aws_sqs_queue`'s real `ApplyResourceChange` response left `region`
absent (`null`), while a subsequent `ReadResource` call populates it —
a genuine provider round-trip completeness gap (the Apply response and
the Read response for the same, unchanged resource don't agree), not any
real out-of-band event. Correctly, honestly reported as
`cloudtrail_unattributed`/`delivery_window` (no real CloudTrail event
exists for it, since nothing actually happened in AWS) — resolved by
accepting that one real, informative `drift_adopt` before proceeding, the
same "record what's actually true" posture this project takes everywhere
else, not a shortcut around it.

**Real out-of-band drift + attribution, both providers**: a real
`aws sqs tag-queue` call (bypassing `ubx`) added a tag; `ubx scan`
correctly detected it and, once CloudTrail delivered the event (a real,
measured delivery lag, well within the existing 5-minute retry budget
this arc's own prior AWS live sessions established), correctly attributed
it to the real IAM principal, event, and timestamp. The GCP half needed
two real, live-discovered course corrections before landing on a resource
type that could genuinely demonstrate both halves at once — see the
"Two real GCP findings" note below; the type that worked cleanly,
`google_project_iam_custom_role`, got a real out-of-band `permissions`
update (via the IAM REST API, `gcloud`'s own CLI session having expired
mid-session — Application Default Credentials, unaffected, were used for
every GCP call instead) that `ubx status --drift` detected automatically
(no manual lookup assistance needed, unlike the two types tried first)
and `gcp_audit` attributed correctly on the very first attempt. `ubx why`
on both addresses shows the complete, honest biography of each, including
the real attribution record.

**Two real GCP findings, surfaced live, not assumed from the design
alone**:
1. `google_service_account`: its own drift *detection* works correctly
   through `ubx`'s ordinary automatic `{"id":...}`-only lookup (a real
   `display_name` mutation was correctly detected as drifted) — but its
   own Cloud Audit Log entries name the resource by a numeric `unique_id`
   (confirmed directly via `logging.googleapis.com` entries), which never
   appears anywhere in the resource's own observed state
   (`identityCandidates` only ever tries `id`/`arn`/`name`) — so its
   drift, though real and correctly detected, is currently
   unattributable (`audit_unattributed`/`no_matching_event`). The same
   class of gap `gcpaudit/client.go`'s own doc comment already named for
   `google_secret_manager_secret` (a numeric project number instead of
   the project ID), now confirmed to also affect service accounts —
   extends, rather than contradicts, that already-documented limitation.
2. `google_pubsub_topic` (the one type already proven, in an earlier
   session, to attribute correctly): its own minimal `{"id":...}` lookup
   — the *only* shape `ubx`'s own automatic pipeline ever uses — returns
   not just `name` empty (the already-documented gap) but `labels` empty
   too, meaning its own drift can never be detected automatically at all,
   only when a caller manually supplies `id`+`name` both (confirmed via a
   direct provider probe) — something `ubx status --drift`'s own
   ledger-recorded lookup never does. **Worse: the identical gap reaches
   the DESTROY path.** A `ubx ship` of this topic's `delta.destroys` entry
   reported `destroyed` in the ledger's own reconciliation record, but the
   real GCP topic was still live afterward — confirmed directly against
   the Pub/Sub API. Not fixed live; the real leaked topic was deleted by
   hand to leave the account clean. Filed as its own issue (UBI-44,
   `ubiquex` team) rather than patched under time pressure mid-session —
   this is a correctness gap in `core/executor`'s own `reconcileDestroyLoop`
   trusting the provider's response, not a conformance-fixture curiosity,
   and deserves its own root-cause investigation. `conformance/registry.go`'s
   own `google_pubsub_topic` entry gained a note recording the destroy-side
   finding alongside its existing read-side one.

`google_project_iam_custom_role` was the one real type found, live, with
neither gap: `id` alone sufficient for real automatic drift detection,
and its own audit-log `resourceName` genuinely matches its own `id`
(a real path, not a numeric surrogate) — added to the same stack
specifically to complete the "both providers, both attributed"
demonstration honestly, once the two structural gaps above were found.

**Cleanup, real, through `ubx`**: every resource this session created —
the queue, the service account, the custom role, and the pubsub topic —
was decommissioned via one real `ubx ship` of a `delta.destroys`
proposal naming all four addresses. Verified independently afterward,
directly against both clouds: the queue and service account are
genuinely gone; the custom role is GCP's own correct soft-deleted state;
the pubsub topic (per the finding above) needed a direct, manual delete
to actually leave the account clean.

docs/executor.md's own "Out of scope" bullet updated (fixed, sessions
3-6, no longer "still open"). ubiquex-docs gained a new guide,
`guides/multi-provider-flow.mdx` (real transcripts throughout, including
the note explaining why the drift/attribution demonstration ended up
using a third GCP resource type rather than the one originally shipped);
`mint validate`/`mint broken-links` both clean. Full repo
`go build ./...`/`go vet ./...`/`gofmt -l .`/`go test ./... -race -count=1`
clean, no regressions (no code changed this session beyond
`conformance/registry.go`'s own new note).

### Session 4 (2026-07-18): the `providerConfig` gap closed — `ApplierPool.Get` returns config too, `.ubx/config` wiring, real CLI verification

Session 3's own named gap (`providerConfig` stayed one global value
across every node regardless of provider) is closed by changing what
`ApplierPool.Get` returns: `(Applier, json.RawMessage, error)`, not just
`(Applier, error)` — each pool entry now carries its own resolved config
alongside its own Applier, read together by the identical `pool.Get` call
`shipChange`'s own loop already made. `Ship`/`shipChange` no longer take
a `providerConfig` parameter at all — it was never anything but a
single-provider stand-in for what the pool now supplies correctly, per
node. `shipDriftRevert` is still untouched internally; `Ship`'s own
dispatcher just resolves its one `(Applier, config)` pair from the pool
once, exactly as before. `shipCreate`/`shipModifyNode`/`shipDestroyNode`
needed no signature changes at all — they already took `providerConfig`
as an explicit parameter (never a package-level global), so threading the
per-node value through was purely `shipChange`'s own loop's concern.
`providerSource` is now threaded per-node too, into `shipDestroyNode`/
`shipModifyNode` (a real, if minor, correctness fix alongside the config
one: a node's own teaching-error hints (`lookupHintText`) previously named
whichever provider the *invocation* was launched with, not necessarily
the one that specific node actually used).

**`SingleApplierPool` gained a second parameter** (`config
json.RawMessage`) to match — a single-provider stack's own config, fixed,
returned alongside its one Applier regardless of what source/version is
asked. `cli/ship.go` passes today's `--provider-config` value straight
through, unchanged in meaning.

**New `cli/providerpool.go`**: the concrete, cli-side `ApplierPool`
implementation `.ubx/config`'s own new `[providers]`/`[provider_configs]`
tables (`cli/config.go`) drive — docs/architecture.md's own open config-
shape question ("likely per-source config values") resolved as a sibling
table, source-keyed, additive alongside `[providers]`, never reopening
that table's own already-decided shape. Lazily launches on first `Get`
for a given source@version (never eagerly, never more than once), refuses
outright — never silently substitutes — a source this stack doesn't
declare or a version that no longer matches the current pin (a proposal
signed against one version, launched against a different one the
operator has since re-pinned, is exactly the silent-drift risk this
whole project exists to catch, not reproduce). The real
`provider.Acquire`/`provider.Launch` machinery is reached through an
injectable `launchFunc` seam — production always uses the real one;
`cli/providerpool_test.go`'s own hermetic tests swap in a fake to prove
the pool's own caching/config-routing/version-mismatch/`Close` logic
without a real provider binary or network access at all.

**`cli/resolve.go`/`cli/ship.go` both branch on `cfg.Providers`**:
non-empty means a real multi-provider stack (resolve launches every
declared provider eagerly to fetch its own schema, in sorted order;
ship's own pool launches lazily, only what a given proposal's nodes
actually need); empty falls back to today's exact `--provider`/`--source`/
`--provider-config` single-provider flow, byte-for-byte unchanged.
`--source`/`--provider-version` retirement stage 2 (docs/resolver.md's
own staged plan) is built: `warnIfLegacyProviderFlagsGiven`
(`cli/config.go`) warns to stderr, naming exactly which flags were
ignored, whenever a stack with a real `[providers]` table also receives
them.

**Live-verified against the real built binary, not just hermetic**: a
real `ubx resolve` → `ubx accept` → `ubx ship` chain against two
genuinely separate provider subprocesses (`UBX_PROVIDER_MIRROR`, no
network — `provider/internal/fakeprovider`'s own `conformance-v6` mode,
one copy per `FAKEPROVIDER_RESOURCE_TYPE`, each wrapped in a small shell
script setting its own env before exec'ing the shared binary, since the
mirror only names a path, not an environment) — confirmed each resource
routes to the correct provider (each reaches its own correct `Configure`/
`ReadResource` freshness precheck, `in_flight`, independently), the
deprecation warning fires and names the right flags, and a version bump
in `.ubx/config` after a proposal was already signed refuses that one
node (`provider unavailable: provider "acme/widget" is pinned to 2.0.0
... but this proposal recorded 1.0.0`) while a sibling node against a
*different*, unaffected provider proceeds to its own independent outcome
— real, live proof of docs/multi-provider-adversarial.md's own row 4
shape, not just the hermetic fake. (Full apply completion wasn't
reachable in this specific smoke test — `conformance-v6` mode was built
for UBI-9's own read-only adopt/mutate/scan-diff testing and has no
`ApplyResourceChange` handler at all; the real apply mechanics themselves
are already exhaustively covered by `core/executor`'s own hermetic suite,
sessions 2-3. This smoke test's own job — proving the new CLI-level
config→pool→routing wiring actually works, live, not just in a fake —
is fully satisfied regardless.)

**Hermetic coverage**: `core/executor`'s own session-3 tests untouched
and still green (the `ApplierPool` signature change didn't invalidate
what they already proved, only added a second return value every fake
already had to start returning). New `cli/providerpool_test.go`: lazy
launch cached on a second `Get`; per-source config returned correctly,
with an explicit assertion that one provider's config never leaks into
another's; a declared source with no `[provider_configs]` entry defaults
to `{}` (the same default `--provider-config` already uses); an
undeclared source is refused, launch never even attempted; a version
mismatch is refused, launch never attempted; an empty version (`Ship`'s
own `pool.Get(ctx, providerSource, "")` for `drift_revert`) resolves
against the currently-pinned version; a launch failure propagates; `Close`
closes every client actually launched and no others (a declared-but-
never-used provider is never even opened, let alone closed). New
`cli/config_test.go` cases: `[providers]`/`[provider_configs]` decode
correctly; their absence decodes to a genuinely nil map, not just an
empty one (the exact signal `cli/resolve.go`/`cli/ship.go` branch on);
`warnIfLegacyProviderFlagsGiven` stays silent when nothing legacy was
given, warns and names the flag when something was. Full repo `go build
./...`/`go vet ./...`/`gofmt -l .`/`go test ./... -race -count=1` clean,
no regressions.

## Amendment (2026-07-19, UBI-44): universal post-destroy read-back — provider-reported success is never sufficient

Found live, not hypothesized: a real `google_pubsub_topic` destroy, shipped
the ordinary way (a create-genesis resource, whose recorded lookup is
always the universal `{"id": "..."}`, never a type-specific companion
field), reported `applied`/`Outcome: "destroyed"` — the exact path Session
5's fix (above) was supposed to have made trustworthy — while the real
topic stayed live in GCP. Root cause confirmed this session by direct
experiment against the real provider (`hashicorp/google` 7.40.0,
protocol v5), not assumed from Session 5's own AWS finding: `Apply`'s
response was `NewState: null`, zero diagnostics, zero error — the
byte-for-byte identical shape a *genuine* destroy produces — yet Cloud
Audit Logs showed **zero** `DeleteTopic` calls across four separate
"successful" attempts (real `ubx ship`, and three isolated variations
driving the wire protocol directly). Filling in the resource's `name`
attribute correctly (the short-form topic ID, distinct from `id`, the
full path — empty in every one of those four attempts, since `ubx`'s
universal lookup only ever records `id`) made the delete genuinely
happen on the next attempt, audit-log-confirmed, both via real Terraform
and via `ubx`'s own wire calls in isolation.

**This is not the same mechanism Session 5 fixed, and conflating them
would have been the real mistake here.** Session 5's bug was an empty
`PlannedPrivate` — the shim never recognizing a genuine destroy at all
without it — and `shipDestroyNode` already calls `PlanResourceChange`
unconditionally before every destroy `Apply`, exactly as that fix
requires. `PlannedPrivate` came back empty in *every* attempt this
session made against `google_pubsub_topic` — including the one that
actually deleted the topic — so it was never the deciding factor here.
The real cause is `PriorState` itself being incomplete in a way this
specific resource's `Delete` implementation can't recognize as a real
target, so it fabricates a clean, diagnostics-free success rather than
erroring. Two genuinely different root causes, one identical symptom:
**a provider can say "success" without it being true, for reasons that
will keep multiplying across types and SDK generations.** Patching this
one cause (e.g., generalizing the lookup-hint mechanism to require
`name` alongside `id` for this type, `conformance/registry.go`'s own
already-named follow-up) would close *this* instance and leave the
class of bug — provider-reported success as the sole signal for a
`destroyed` verdict — open for the next one.

**The fix is structural, not a lookup-hint patch: a post-destroy
read-back is now the universal, only way a `destroyed` verdict is
earned. Provider-reported success is never sufficient by itself.**

- `shipDestroyNode`'s own `Apply` call succeeding (`applyErr == nil`) no
  longer resolves `applied`/`destroyed` directly. It now transitions to
  `unknown_post_timeout` — the pre-existing "ambiguous, go verify"
  state — and always runs the same reconcile-by-query loop a genuinely
  ambiguous `Apply` error already triggered. The read-back is what earns
  the verdict, exactly as an ambiguous outcome already required;
  "provider said success" no longer skips it.
- The reconcile loop's own resolution logic for an **ambiguous RPC result**
  (`applyClaimedSuccess=false`, the pre-existing path) is unchanged: a
  not-found read (with the pre-attempt `present_matches` precondition
  already established) resolves `destroyed`; a read that still finds the
  resource present is *immediately* conclusive — presence itself proves
  the call never landed — and resolves `failed`, no retry.
- For a **clean, provider-claimed success** (`applyClaimedSuccess=true`,
  the new universal path), a present read is deliberately *not*
  immediately conclusive — real eventual-consistency lag (SQS's own ~60s
  figure) can look identical to a lie until the budget's own tail is
  reached, so every attempt but the last records `inconclusive` and
  retries. Only the **final** attempt, still present, earns the
  definitive verdict: `Outcome: "provider_reported_success_but_present"`,
  detail "the provider reported a successful destroy, but a post-destroy
  read-back found the resource still present after the full retry
  budget — the delete never actually happened" — a materially more
  serious finding than an ordinary ambiguous-RPC `failed`, since it means
  the provider actively lied, not just that the network hiccuped, and
  `ubx why`'s own biography rendering should be able to say so plainly
  rather than folding it into the same generic wording an honest
  ambiguous failure gets. This final-attempt verdict is a definitive
  `failed`, deliberately never the vaguer `still_unknown` — a read that
  clearly and repeatedly says "still here" is not genuinely ambiguous the
  way a failing RPC is; it is a clear, repeated "no" to the destroyed
  question.
- **Terminal `Apply` errors are unaffected — no read-back added for
  them.** A real, structured `ERROR`-severity diagnostic is already the
  provider's own honest negative answer (this document's own error
  taxonomy, above: "the provider itself said no"); the asymmetry this
  amendment is built on is specifically about *trusting a rosy answer*,
  not a downbeat one. A false negative (provider claims failure, but the
  delete actually landed) is a categorically safer class of bug than a
  false positive (provider claims success, ledger records the resource
  gone, it's still live) — the first surfaces as a resource `ubx status`
  correctly still shows as present and a retry cleans up; the second is
  exactly the silent, undetectable trust violation this project's whole
  thesis exists to prevent. Adding a read-back to every terminal failure
  too would only add cost, not close a real risk.
- The **already-absent short-circuit** (the three-way freshness
  precheck, above) is unaffected — it never calls `Apply` at all, so
  there's nothing for a lying response to attach to.

**Co-scoped with UBI-42 (the reconcile retry budget), not deferred
separately — the universal read-back makes the existing budget's own
inadequacy load-bearing for every single destroy, not just the rare
ambiguous ones it used to gate.** The pre-existing budget
(`maxReconcileAttempts = 5`, `reconcileRetryInterval = 20ms` — ~100ms
total, still exactly what create/modify's own unrelated `reconcileLoop`
uses, untouched) was already known too short for genuine cloud eventual
consistency (Session 5's own finding: SQS's real deletion-visibility lag
can outlast 60 seconds); shipping the universal read-back on top of that
same fixed interval for destroy would turn *every* destroy against a
provider with any real propagation lag into a spurious `failed`, even
though the delete genuinely happened — a regression this amendment
cannot ship alongside. Fixed together, destroy-specifically: a new,
separate backoff schedule (`destroyReconcileBackoffSchedule`,
`core/executor/ship.go`) — 10 steps from 50ms up to a 15s ceiling,
summing to roughly 64 seconds of total budget, comfortably past AWS's
own documented ~60-second SQS lag. The common case (a provider with
synchronous, immediate consistency — GCP Pub/Sub's own real behavior,
confirmed by this session's own live finding once fixed: real deletion
confirmed via Cloud Audit Logs immediately) resolves on the very first
read, before any sleep at all — the schedule's cost is paid only by the
genuine long tail, never by the ordinary case. A persistent lie (a
provider that never actually deletes, ever) now costs one full
~64-second budget per attempt before resolving a definitive `failed`
(`provider_reported_success_but_present`, above — never rounded down to
the vaguer `still_unknown`), then a hard `retry budget exhausted`
failure once `maxApplyAttemptsPerResource` (3) is spent across re-ship
attempts — a real, bounded cost for surfacing a serious finding loudly,
not a silent infinite hang.

**Hermetic**: `core/executor`'s own local `fakeApplier` (`ship_test.go`)
gains two new fault-injection modes, the package's own established
convention (never `provider/internal/fakeprovider`, which this package
has never imported — the same `core`/`provider` zero-import boundary
UBI-23 established) — `scriptLyingDestroy` (`ApplyResourceChange` for a
destroy reports success, `NewState: null`, no diagnostics, while the
resource stays present, the exact shape found live) and
`scriptDelayedAbsence` (a genuine destroy that keeps reading back
present for exactly N further reads first, modeling real bounded
eventual-consistency lag rather than a lie). New tests prove: a lying
destroy resolves `provider_reported_success_but_present`/`failed`, never
`destroyed`, and a retried re-ship against a still-lying provider fails
again rather than ever accepting the claim; an honest, synchronously-
consistent destroy still resolves `destroyed` on the very first
reconcile read, with no added retries or sleeps; a destroy with genuine,
bounded propagation delay still resolves `destroyed` once
`destroyReconcileBackoffSchedule` reaches it, retried through the
intervening `inconclusive` reads rather than giving up early.
`provider/internal/fakeprovider` (the real, subprocess, wire-protocol
fixture `cli`/`conformance` tests launch) separately gains the identical
lying-destroy fault mode (`FAKEPROVIDER_APPLY_MODE=lying-destroy`),
exercised by `cli/ship_lying_destroy_test.go`, proving the same behavior
through the real tfplugin wire protocol, not just an in-process interface
mock — gated behind `UBX_TEST_SLOW=1`, skipped by default: this path
genuinely pays the full ~64-second production retry budget (unlike
`core/executor`'s own suite, this package can't reach in and shrink an
unexported var from outside), so it's a real, opt-in regression check,
not part of the fast default `go test ./...` run. See
docs/destroys-adversarial.md for the corresponding new adversarial rows.

**Live**: the exact `google_pubsub_topic` destroy that lied was re-run
against real GCP with the fix in place — see docs/reliability-report.md's
own new chapter for the full transcript (create, adopt with the
universal `{"id":...}` lookup, destroy, and this time a genuine
`DeleteTopic` call confirmed via Cloud Audit Logs, the topic actually
gone via `gcloud pubsub topics describe`). The earlier session's false
`destroyed` ledger record is never edited — corrected forward, per this
project's own append-only posture, the same discipline UBI-30's own
false-tombstone recovery already established: a fresh `ubx scan` against
the address rediscovers the resource (`FoldState`'s tombstone-fold
correctly excluded it from `ubx status` while treating the ledger's own
chain as permanently accurate history, not something to rewrite), and a
new, correctly-verified destroy proposal closes it for real.

## Amendment (2026-08-02, UBI-67 session 1): parallel execution — investigation only, go/no-go read

**Scope of this session, stated exactly as sized**: UBI-67 is a real
executor-architecture change (the founder's own sizing: 3-5 sessions).
This session is investigation and design ONLY, per the ticket's own
explicit instruction — no scheduler code was written, and nothing in
`core/executor` or `cli` changed. Two throwaway test files were written,
run under `go test -race`, and deleted before this session's own commit;
their results are recorded below as evidence, not as artifacts anyone can
re-run today. The founder's own live finding motivating this ticket
stands unchanged: a 5-resource terminate where SQS's own deletion lag was
~79s while ECR/role/policy/attachment each took ~1s (total wall time
~80s) is real, measured, and would genuinely improve under a correct
parallel scheduler.

### Finding 1 — the provider client stack is safe for concurrent calls (empirically confirmed)

Traced every layer between `core/executor.Applier` and the real
subprocess: `cli.stateReaderAdapter` (a plain, immutable value struct —
`salt []byte`/`source string`/`p provider.Provider`, every method a pure
pass-through, nothing mutated) → `provider.v5Provider`/`v6Provider` (same
shape: one immutable gRPC stub field, every method builds and sends one
request, no shared buffers, no cached state) → `*grpc.ClientConn`
(grpc-go's own documented contract: a single `ClientConn` is safe for
concurrent RPCs from multiple goroutines by design — this is the
mechanism real Terraform itself already relies on, since Terraform's own
default `-parallelism=10` calls a single provider server instance
concurrently across independent resources every time anyone runs `terraform
apply` without overriding it).

**Verified directly, not assumed** (this session's own throwaway
`provider/zzz_concurrency_investigation_test.go`, deleted after use):
launched one real `fakeprovider` subprocess (`FAKEPROVIDER_MODE=ok-v6`),
fired 50 concurrent goroutines each calling `ReadResource` with its own
distinct resource identity, then a second run doing the same for
`ApplyResourceChange` — both passed clean under `go test -race`
(`TestZZZ_ConcurrentReadResource_SameClient`,
`TestZZZ_ConcurrentApplyResourceChange_SameClient`), zero race-detector
warnings, and every goroutine's own result decoded back to exactly the
value IT requested (no cross-contamination between concurrent calls'
responses — gRPC's own per-RPC stream framing keeps them correctly
isolated).

`provider.Redact`/`provider.OverridePathsFor` (the redaction path every
`ReadResource`/`ApplyResourceChange` result already passes through,
`cli/stateadapter.go`) touch only `SensitiveOverrides`, a package-level
`var` that is a static, never-mutated-after-init table (`provider/
overrides.go`) — safe for unlimited concurrent reads by construction, no
lock needed or missing.

**Residual risk, named honestly**: this confirms ubx's own CLIENT-side
code is race-free. It does not independently re-verify that an arbitrary
real provider SERVER binary (`terraform-provider-aws`, etc.) handles
concurrent requests correctly on its own end — doing that meaningfully
would need real credentials and a real `ApplyResourceChange` against
live infrastructure, which CLAUDE.md's own standing rule forbids for
verification purposes. This rests on Terraform's own well-established
precedent (default parallelism already exercises exactly this shape
against real provider binaries, at scale, for every user who has ever run
plain `terraform apply`) rather than a fresh empirical check here — a
real, if low-probability, gap, not a blocker.

### Finding 2 — `ApplierPool.Get` is ALREADY safe for concurrent use (no change needed), with one minor lock-scope note

`cli.providerPool` (`cli/providerpool.go`) already wraps its entire `Get`
body in `p.mu sync.Mutex` — the lazy launch-and-cache map (`p.launched`)
is already correctly protected against a concurrent double-launch race
for the same `(source, version)` key. **Point 2 of the ticket's own ask
("verify the pool... is safe for concurrent use, or serialize per-
provider") is already satisfied by existing code, not something this
arc needs to build.**

One real, minor inefficiency worth naming for the scheduler design,
below: the mutex is held for the ENTIRE `Get` call, including the actual
subprocess launch + handshake (`p.launch(ctx, source, pinned)`, which can
take up to `defaultHandshakeTimeout` = 10s). A scheduler that calls
`pool.Get` from many worker goroutines at the very start of a multi-
provider ship would have every OTHER provider's first `Get` call block
behind whichever provider happens to launch first, even though they're
completely independent processes — serializing exactly the "cold start"
phase this ticket exists to parallelize away. Not a correctness bug, a
missed-parallelism opportunity: a future fix could use a per-key lock
(e.g. `sync.Map` of per-source mutexes, or launch every declared provider
eagerly up front before the walk starts, rather than lazily on first
`Get`) — named here so it isn't rediscovered as a surprise once real
scheduler code exists.

### Finding 3 — the REAL blocker: today's persistence path is not safe for concurrent access, and silently LOSES data under naive parallelization (empirically confirmed, severe)

This is the load-bearing finding of this session. `shipChange`'s own
per-node loop (`ship.go`) does three things after every single
transition, not just once per resource:

1. `rec.Resources = append(rec.Resources, ra)` — mutates a plain Go
   slice header held on the shared `*core.ApplyRecord` passed into every
   node's own step function.
2. `persist()` → `l.SaveApplyProgress(rec)` → `writeApplyFile` →
   `json.MarshalIndent(rec, ...)` then one atomic file write — this
   marshals the ENTIRE `rec`, including every OTHER node's own
   `*core.ResourceApply` entries already appended to `rec.Resources`,
   and writes the whole record to the SAME on-disk attempt file every
   time, from wherever `persist()` happens to be called (this is called
   many times per resource — once per transition — not once at the end).
3. `resultsByAddr[key] = ra.ProviderResult` — a plain
   `map[string]json.RawMessage` write, read by both the dependency-ready
   check (`missingDep`) and `substituteComputed`.

None of these three is synchronized in any way today — correct only
because today's whole walk is a single `for` loop, one node at a time.
**Verified directly, not assumed** (this session's own throwaway
`core/executor/zzz_concurrency_investigation_test.go`, deleted after
use): took 8 independent `delta.creates` nodes (no `depends_on` between
them — exactly the "these could obviously run concurrently" case this
ticket's own SQS/ECR example names), fanned each one's existing,
UNMODIFIED `shipCreate` call into its own goroutine, sharing the same
`rec`/`persist`/`resultsByAddr` the real `shipChange` loop already
threads through today, and ran under `go test -race`:

- The race detector fired repeatedly and unambiguously: concurrent
  reads/writes on `rec.Resources`'s slice header (including one
  `runtime.growslice` race caught mid-append), and a concurrent read
  inside `json.MarshalIndent` (walking one goroutine's own `ra.Transitions`)
  racing against a DIFFERENT goroutine's write to `rec.Resources` itself.
- **Worse than a race-detector-only finding: real data was silently
  lost.** `len(rec.Resources)` came back **3, not 8** — 5 of 8
  concurrently-appended `*core.ResourceApply` entries vanished
  completely, the classic lost-update shape of an unsynchronized
  concurrent slice append (multiple goroutines read the same slice
  header, each appends to its own local copy, whichever write lands last
  wins, the rest are silently overwritten). This is not a benign,
  cosmetic race — a resource genuinely applied against a real provider
  could disappear from its own `ApplyRecord` entirely under naive
  parallelization, breaking `docs/executor.md`'s own THE invariant
  (a transition must be durably on disk), `ubx why`/`ubx status`'s
  ability to ever show that resource's outcome, and the idempotency
  contract this whole package exists to guarantee (a resumed `ubx ship`
  re-reads `ApplyAttempts` to decide what's already done — an entry that
  was silently dropped looks exactly like a resource that was never
  attempted at all, inviting a duplicate create/destroy against a real
  provider on the next run).

**This is the real go/no-go question this ticket's point 2 was
investigating, and the honest answer is: parallelizing the walk is
NOT safe as a "wrap today's per-node functions in goroutines" change.**
Every one of `shipCreate`/`shipModifyNode`/`shipDestroyNode`'s own
signatures was designed around an implicit single-writer contract —
`rec.Resources`, `resultsByAddr`, and the three `*int64` outcome
counters (`resourcesApplied`/`resourcesFailed`/`resourcesStillUnknown`,
also unsynchronized non-atomic increments, the identical hazard class as
`rec.Resources`) are all shared, directly-mutated state, threaded by
pointer/closure into what would become N concurrent callers. A real fix
needs an architectural change to this contract, not a mutex bolted onto
the outside — see the scheduling sketch below.

### Finding 4 — `resultsByAddr`/the dependency-ready check: correct in shape, needs a different mechanism under concurrency

Today's `missingDep` check (`for _, dep := range n.dependsOn { if _,
ok := resultsByAddr[dep]; !ok { ... } }`) is checked exactly ONCE per
node, at the moment that node's own turn in the serial walk begins — this
is only correct because the serial walk's own topo order already
guarantees every dependency was fully processed (and, if applied,
already written into `resultsByAddr`) before this node's turn ever
starts. Under concurrency this same single-check-then-proceed shape is
NOT sufficient on its own: a node's dependency may not have started yet,
or may still be mid-flight, at the exact moment a naive scheduler
launches this node's own goroutine — checking `resultsByAddr` once and
either proceeding or refusing (today's binary outcome: proceed, or a
terminal "blocked: dependency has not applied" failure) would incorrectly
refuse perfectly-appliable resources just because their dependency
hadn't finished YET, not because it never would.

**What must replace it**: the check itself (comparing `n.dependsOn`
against a set of already-completed addresses) stays exactly the same
semantically — what changes is that a node must genuinely WAIT for its
own dependencies' completion (a signal, not a single point-in-time
poll) before starting, and only then treat a still-missing dependency as
a real, terminal "blocked" failure (i.e., the dependency itself failed
or was never eligible, not merely "hasn't gotten to it yet"). This is
exactly what a parallel topo-sort scheduler's own "ready" set/channel
mechanism is for (see the scheduling sketch below) — the missingDep
check's own logic doesn't need to change, only WHEN it's evaluated
relative to sibling completion.

### Finding 5 — `reconcileSameBatchEffects` and the destroy freshness precheck: correct under concurrency, PROVIDED the scheduler preserves two specific invariants

Traced both functions against a concurrent-completion scenario, per the
ticket's own explicit ask.

**`reconcileSameBatchEffects`** already runs strictly AFTER the entire
walk finishes (`shipChange` calls it once, synchronously, right after
`sealOutcome` — never mid-walk). This means it is unaffected by whether
the WALK itself was serial or concurrent, **as long as the scheduler
correctly joins/barriers every worker goroutine before calling it** — by
the time it runs, `rec.Resources` and `resultsByAddr` are fully populated
and stable either way. The one thing that changes, purely in wording,
not mechanism: the function's own doc comment currently reasons about "a
LATER same-batch dependent's own apply" mutating an earlier resource —
under concurrent completion, "later" no longer names a single
unambiguous point in a timeline (two dependents could complete in either
order, or simultaneously). The underlying claim this function's whole
mechanism depends on — "any observed diff on a depended-on resource is
attributable to a sibling in THIS SAME BATCH, not something external" —
does not actually depend on serial ordering at all; only the doc
comment's own phrasing needs updating (to "a sibling in this same batch,
completed at some point during the walk" rather than "a later" one) once
real scheduler code lands.

**The destroy freshness precheck** (`destroyDiffExplainedByNormalization`
+ `sameBatchDependentsDestroyed`, UBI-63 session 5's own fix) is called
from `shipDestroyNode`, which — critically — is only ever reached AFTER
the `missingDep` check has already confirmed every one of `n.dependsOn`'s
addresses is present in `resultsByAddr` (i.e., already destroyed, for a
destroy node's own reverse-edge dependency set). `sameBatchDependentsDestroyed
:= len(n.dependsOn) > 0` is a STATIC property of the node (does this
destroy have any declared reverse dependency at all in this proposal?),
not something computed from real-time completion order — so this
argument's own correctness transfers to a concurrent scheduler
UNCHANGED, **provided that scheduler enforces the exact same
"never start a node before every one of its declared dependencies has
durably completed" invariant the serial walk gets for free from its own
loop structure.** This is not a new invariant a concurrent scheduler
would need to invent — it is the DEFINING correctness property any
parallel topo-sort executor must already guarantee, or it isn't
implementing dependency order at all. Named explicitly because it means
this precheck needs zero logic changes once a correct scheduler exists —
its correctness is inherited for free from the same property that makes
the scheduler a correct topo-sort executor in the first place, not
something that needs its own separate proof.

**Conclusion for point 2's whole investigation**: neither
`reconcileSameBatchEffects` nor the destroy freshness precheck is a
reason to be MORE cautious about this ticket than Finding 3 already
requires — both are already correct under concurrency, conditioned on
invariants ("barrier before post-walk reconciliation," "never start a
node ahead of its dependencies' durable completion") that a correct
scheduler must provide anyway, for reasons unrelated to these two
functions specifically.

### Scheduling approach sketch (design only, no code this session)

**Goroutines + a dependency-ready mechanism, not a fixed-size worker
pool with a work queue.** The graph is already known in full up front
(`changeNodesOf` decodes and topo-validates it before any node starts) —
a worker-pool-plus-queue design (N long-lived workers pulling from a
shared channel) is the right shape when work items arrive over time or
are homogeneous; here, the *dependency edges themselves* are the
scheduling constraint, which maps far more directly onto "launch a
goroutine per node, gated on a channel/WaitGroup per dependency" than
onto a generic queue a pool drains. Sketch:

1. For every node, compute its in-degree (count of `dependsOn` entries)
   and its dependents (reverse edges) — a one-time, cheap graph
   transform over `changeNodesOf`'s own output, computed once before any
   node starts.
2. Every node with in-degree 0 is immediately eligible; launch its own
   goroutine right away (bounded by the concurrency limit, below).
3. Each node's own goroutine does exactly what `shipCreate`/
   `shipModifyNode`/`shipDestroyNode` already do TODAY, with one
   structural change: instead of directly mutating `rec.Resources`/
   `resultsByAddr`/the three counters, it returns its own result (or
   sends it down a single results channel) to be applied by exactly ONE
   place.
4. That one place — a single aggregator goroutine (or a mutex-guarded
   funnel; a channel-fed single writer is the cleaner match for this
   codebase's existing "durably persist, then proceed" sequencing, and
   sidesteps needing a lock around the JSON-marshal-and-write itself) —
   is the ONLY code that ever appends to `rec.Resources`, writes
   `resultsByAddr`, increments the three counters, or calls `persist()`.
   This directly fixes Finding 3: there is exactly one writer, so the
   lost-append/torn-marshal races this session proved empirically simply
   cannot occur, by construction, not by adding synchronization around
   the existing multi-writer shape.
5. When a node completes (successfully applied, in the sense that
   satisfies a dependent — see Finding 4), the aggregator decrements the
   in-degree of every one of its dependents; any dependent that reaches
   0 is now eligible and gets its own goroutine launched (still gated by
   the concurrency limit). A node whose dependency FAILED (terminal, not
   merely slow) is what actually produces today's "blocked: dependency
   has not applied" outcome — now a real, permanent decision (the
   dependency will never complete), not a premature poll.
6. The walk finishes when every node has reached a terminal state
   (applied/failed/still_unknown/blocked). The aggregator's own
   completion is the correct join point for `sealOutcome` and
   `reconcileSameBatchEffects` (Finding 5's own barrier requirement).

**Concurrency limit**: a config/flag-controlled max-parallel (the
ticket's own point 6), enforced via a plain buffered channel used as a
counting semaphore (acquire a slot before a node's goroutine starts real
provider work, release when it completes) — never unbounded, since a
real provider's own rate limits are real (this codebase already has a
live-verified precedent for respecting a provider's own pace: the
destroy reconcile backoff schedule's own escalating waits, UBI-42/44).
Default value not decided this session — a real, deliberate open
question for the implementation session, not silently assumed (a
reasonable starting point mirroring Terraform's own long-standing
`-parallelism=10` default is a defensible anchor, not a decision made
here).

**Error aggregation** (the ticket's own point 5): already correct in
SHAPE today — an independent node's own failure doesn't currently stop
unrelated siblings (`shipChange`'s own doc comment: "a blocked resource
here only blocks its OWN dependents"). Under the aggregator design
above, this property is preserved directly: a failed node still reports
its own terminal outcome to the aggregator and still correctly
propagates "blocked" to its OWN dependents' in-degree bookkeeping,
exactly as it does serially today — no new design needed here beyond
what Finding 4 already covers.

### Progress UI: today's design assumes exactly one resource in flight at a time — must change, but there's a known-good reference design

`cli/ship.go`'s `newProgressPrinter` (UBI-70's own polish) is EXPLICITLY
built on "at most one resource is ever in_flight/verifying at a time" (its
own doc comment says so directly): one shared ticker (not per-address),
a single `\r`-based in-place-overwrite convention for the live "shipping"/
"verifying via read-back" line, and an implicit assumption that a
resource's own header line prints, then its transitions run to
completion, before the next resource's header ever appears. None of this
survives N genuinely simultaneous in-flight resources: multiple tickers
would need to coexist, and a single-line `\r` overwrite has no way to
represent more than one concurrently-updating line on a real terminal at
once.

**This is a well-understood problem with a well-known reference
solution, not a novel design question**: real Terraform's own `apply`
output, under its own default concurrent execution, solves this
identically — it never uses in-place overwrite for anything. Every
progress line is fully discrete, always prefixed with the resource's own
address (`aws_instance.foo: Still creating... [10s elapsed]`), so N
concurrent resources' own lines interleave in whatever real order they
actually occur, and remain completely legible without any terminal
cursor coordination at all. Adopting the identical convention here means:

- Drop the single shared ticker in favor of either (a) one ticker per
  currently in-flight resource, each independently emitting its own
  fully-discrete, address-prefixed line per tick (not overwritten), or
  (b) a single ticker that, once per interval, emits one discrete line
  per CURRENTLY in-flight resource (closer to Terraform's own actual
  behavior, and avoids a burst of N simultaneous lines at the same
  instant looking like uncoordinated noise).
- Keep the `mu sync.Mutex` this printer already has (already correctly
  guards the shared `starts`/`seen` maps against concurrent
  `ProgressEvent` delivery) — concurrency here was never the map-safety
  problem, it's the RENDERING MODEL that assumed serial narration.
- The one-blank-line-between-resources convention and the "print this
  resource's own header exactly once, on first sight" logic both still
  work unchanged under concurrent delivery (each keyed by `ev.Address`
  already) — they don't assume serial completion, only that a given
  address's OWN events arrive in that address's own internal order,
  which any correct scheduler still guarantees per-node.
- Non-TTY mode (already "discrete lines only, no ticker" today) needs
  literally no change at all — it was already address-agnostic-safe
  under concurrent delivery, since it never assumed a single shared
  live-updating line to begin with.

This is real, scoped implementation work for a future session (a
`newProgressPrinter` rewrite + new adversarial rows for "5+ concurrent
updates stay legible"), not a design risk — the reference solution
already exists and is proven at scale (every `terraform apply` anyone
has ever run with default parallelism).

### Go/no-go read

**Go, but not as originally scoped.** The two real risks this session
was asked to resolve:

1. **Provider client concurrency** — de-risked. Confirmed safe
   empirically at the client layer; `ApplierPool.Get` already safe
   (existing code, no change needed). Residual risk (real provider
   SERVER-side concurrent-request handling) rests on Terraform's own
   long-established precedent, not fresh verification here, and is a
   low-probability, named gap rather than a blocker.
2. **`reconcileSameBatchEffects`/destroy freshness precheck under
   concurrent completion** — both ALREADY correct under concurrency,
   conditioned on invariants (barrier before post-walk reconciliation;
   never start a node ahead of its dependencies' durable completion) any
   correct scheduler must provide regardless. Not a blocker, and needs no
   new logic beyond a doc-comment wording update.

**The real, previously-unnamed risk this session surfaced instead**:
today's persistence path (`rec.Resources` append, `resultsByAddr`, the
three outcome counters, `persist()`/`SaveApplyProgress` itself) has an
implicit single-writer contract baked into `shipCreate`/`shipModifyNode`/
`shipDestroyNode`'s own signatures, and naively parallelizing by wrapping
today's functions in goroutines does not just risk a benign data race —
it was empirically shown to silently DROP resource entries from the
apply record (3 of 8 survived a concurrent fan-out in this session's own
throwaway test). This is fixable — the single-aggregator-writer sketch
above is a known, standard pattern, not a research problem — but it
means the real scope of "the concurrency-safe walk" (the ticket's own
point 1) is larger than "add goroutines to the existing loop": every
`shipCreate`/`shipModifyNode`/`shipDestroyNode` signature needs to stop
directly mutating shared state and instead return an isolated result for
a single aggregator to apply, which touches every call site and every
existing hermetic test in `core/executor/ship_test.go` that constructs
one of these calls directly.

**Revised sizing**: the founder's own 3-5 session estimate looks right,
maybe still light — the aggregator rework (Finding 3) is a genuine
refactor of `core/executor`'s own internals touching all three ship*Node
functions and their shared-state parameters, not a wrapper added around
them, and the progress-UI rewrite (while a known reference design, not a
research question) is real, separate implementation work with its own
adversarial program (5+ concurrent updates, provider pool
exhaustion/contention, a resource whose dependency fails mid-flight
rather than up front). Recommended next session: build the aggregator +
scheduler core in `core/executor` first (Findings 3/4, the load-bearing
correctness work), proven hermetically against the existing adversarial
program PLUS new concurrent-specific rows, before touching the CLI
progress printer at all — the printer rewrite has no correctness
dependency on the scheduler's own internals beyond "N `ProgressEvent`s
for different addresses can now arrive interleaved," so it can be
verified against a scripted fake emitting exactly that shape without
waiting for the real scheduler to exist.

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
  proposals/stacks. Serial, delta/dependency order, still true as of this
  writing — **UBI-67 session 1 (2026-08-02)** investigated the real
  design/risk questions (see this document's own "Amendment (2026-08-02,
  UBI-67 session 1)" section, above) and produced a go/no-go read plus a
  scheduling sketch, but wrote no scheduler code — still fully serial
  until a future implementation session lands.
- ~~Multi-provider stacks (one `ubx ship` invocation, one `Applier`, no
  client pool; `providerConfig` one global value regardless of provider;
  no `.ubx/config` wiring) — executor + CLI code.~~ — **fixed, UBI-43
  sessions 3-6** (see the "Session 3"/"Session 4"/"Session 5"/"Session 6"
  addenda above/below): `ApplierPool`, `SingleApplierPool`, per-node pool
  dispatch in `shipChange`, `ApplierPool.Get` returning each provider's
  own config, `.ubx/config`'s `[providers]`/`[provider_configs]` tables
  live-wired into `ubx resolve`/`ubx ship`, `--source`/`--provider-version`
  deprecation staging, `ubx status --drift`/`ubx scan --all`'s own
  fleet-grouping by each resource's own recorded or freshly type-inferred
  provider — all hermetic, and live-verified against the real binary with
  two genuinely separate provider subprocesses — and, finally, the live
  finale against real cloud infrastructure (session 6): a real
  `aws_sqs_queue` + `google_service_account` in one intent file, a
  genuine cross-provider `$ref`, resolved → accepted → shipped as ONE
  signed proposal on real AWS + real GCP, drift on both providers
  detected and attributed, account left clean afterward.
- A `--dry-run`/preview mode for `ship` itself — `ubx revert-plan` already
  fills that role, pre-acceptance; once accepted, `ship` executes.
- Automatic rollback on partial failure. A `partially_applied` outcome is
  reported honestly; nothing auto-reverts what already landed. A human
  decides the next step (retry the remainder via another `ship`, or
  re-scan/re-resolve if reality has moved on).
- ~~`delta.destroys`, for a `change` proposal or any other kind — no kind
  this codebase produces today carries a real destroy, and shipping one
  needs its own adversarial thinking (docs/resolver.md's own Scope
  section).~~ — **fixed, UBI-30 sessions 3-5**: `shipDestroyNode`/
  `reconcileDestroyLoop` (see "Amendment (2026-07-17, UBI-30): shipping
  destroys," above), all eleven docs/destroys-adversarial.md rows green
  hermetically (session 3); `core.Ledger.FoldState`'s own tombstone-folding
  and `ubx why`'s `destroyed`/`already_absent` rendering (session 4, its
  own addendum above); a real `PlanResourceChange` call before destroy's own
  `Apply`, and a `PlannedState`/`Config` top-level-null encoding fix found
  alongside it (session 5, its own addendum above); a live full-lifecycle
  finale on real AWS (see docs/reliability-report.md's own UBI-30 section).
