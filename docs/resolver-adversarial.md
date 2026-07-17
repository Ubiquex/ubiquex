# Resolver adversarial program (UBI-27)

> Every row here is a failure injected on purpose, with a REQUIRED
> observable outcome — written as the spec, before any code exists to pass
> it, the same discipline docs/executor-adversarial.md already established
> for the executor. Each row becomes a test in `core/resolver`'s hermetic
> suite (fake schemas, fake ledger state) before it becomes a claim about
> real infrastructure. This document is also a future published
> reliability report, alongside docs/executor-adversarial.md/
> docs/reliability-report.md: read each row as a claim `ubx` makes about
> its own behavior under a specific bad or ambiguous input, not just a
> test-plan checklist.

## How to read this table

"Injection" describes exactly what's wrong with the input or environment,
and when it's introduced relative to docs/resolver.md's own pipeline
(load intent → validate `op` against ledger → resolve refs/graph → topo-sort
→ mark `$computed`/`$secret`/`$ephemeral` → double-run → hash). "Required
outcome" is the full observable contract: what error is produced (type,
message shape), and — critically — what is *not* silently done instead
(guessed, concretized, ordered arbitrarily). A row that can't be made to
produce its required outcome is a bug, not an acceptable gap.

## The program

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 1 | Double-run divergence | The resolver's own logic is instrumented (in a hermetic test only) to produce a different byte sequence on its second run — e.g. iterating a map without sorting somewhere in ref resolution or graph output. | `core.DoubleRun` catches the mismatch and the resolve call fails hard with `ErrDoubleRunMismatch` (reused unchanged from canonical hashing) — never a proposal hashed from a single, unverified run. |
| 2 | Circular intra-stack refs | Two (or more) resource intents in the same batch `$ref` each other, directly or transitively (`a` depends on `b`, `b` depends on `a`). | Resolve fails hard, naming the full cycle path (`payments.aws_instance.a → payments.aws_instance.b → payments.aws_instance.a`) — never an arbitrary order, never a silently-broken edge. This is genuinely new code (docs/resolver.md: v1 XCL's own single-stack graph never detected cycles at all). |
| 3 | Ref to nonexistent resource | A `$ref`/`$cross` names an address (intra- or cross-stack) that doesn't exist — not in this batch's own creates, not in the target ledger's `FoldState`. | Resolve fails hard, naming the unresolved address and which resource's config referenced it. Never resolved as null, never silently dropped from the output config. |
| 4 | Cross-stack pin against empty/missing neighbor ledger | `$cross.ledger_dir` points at a directory with no ledger at all (never initialized), or one whose target address was never recorded (no `FoldState` for it). | Resolve fails hard — distinguishable messages for "no ledger here at all" vs. "ledger exists, but this address was never recorded in it," neither silently treated as an empty/null value. |
| 5 | Neighbor advances between resolve and accept | A `change` proposal is resolved (recording `pinned_head`); before it's accepted, the neighbor ledger's own head advances (a real proposal accepted against it). | Accepting the stale proposal is refused — re-deriving the neighbor's current `Head()` and comparing against the recorded `pinned_head` catches the mismatch, the same "resolved-time truth vs. accept-time reality" shape `VerifyFreshness` already enforces for live cloud state, one level up. |
| 6 | `$computed` value used where a concrete value is required | A resource's config references a sibling's schema-`Computed` attribute (correctly marked `$computed`) in a position docs/resolver.md's own rules say must be concrete (e.g. an identity/lookup field, or interpolated into a larger string rather than passed through whole). | Resolve fails hard, naming the offending path and that it's `$computed` where a concrete value is structurally required — the direct generalization of v1's own "`Pending` used where `Resolved` required" rule (v1's only instance: `when` conditions), never silently concretized with a guess, never silently allowed through as a marker where a real value is needed. |
| 7 | Secret ref in a non-secret-capable field | A `$secret` marker is placed at a config path whose real provider schema attribute is NOT flagged `Sensitive`. | Resolve fails hard, naming the path and that the target attribute isn't `Sensitive` in the provider's own schema. A real check v1 never had (docs/resolver.md: v1's typechecker only warns on an unrecognized secret *backend* name, never validates the target field at all). |
| 8 | Intent for a type the provider schema lacks | A resource intent's `type` doesn't exist in `SchemaInspector.HasType` for the provider being resolved against. | Resolve fails hard, naming the unrecognized type — never silently skipped, never treated as if it were some other type. |
| 9 | Modify intent whose target isn't in the ledger | `op: "modify"` names an address absent from the ledger's `FoldState`. | Resolve fails hard: "modify declared for an address the ledger has never recorded — did you mean `create`?" (or the symmetric case: `op: "create"` for an address the ledger already has). This row only exists because `op` is explicit, not inferred (docs/resolver.md's own design note) — inferring create/modify from ledger presence would make this scenario silently correct instead of a caught mistake. |
| 10 | Unknown-value round-trip through a real provider's `PlanResourceChange`/`ApplyResourceChange` | A real create with a genuinely `$computed` attribute (e.g. a resource whose `id` another resource's config references) is shipped against a real provider binary, `PlannedState` carrying a real `cty.UnknownVal` at that position, constructed directly with no separate `PlanResourceChange` call (docs/executor.md's amendment). | Empirical, not assumed: either the real provider accepts this and resolves the unknown correctly during `Apply` (confirming the no-separate-plan-phase shortcut extends safely to real unknowns, not just to `drift_revert`'s always-concrete case), or it doesn't — in which case that failure is the real finding, recorded honestly, and `PlanResourceChange` becomes required for `change` proposals specifically (docs/executor.md's own contingency, already named). This row is explicitly about finding out, not about a outcome assumed in advance. |

## What this table doesn't yet cover, named rather than assumed

Not covered by a row above, and therefore not yet a claim `ubx` makes about
itself: a cross-stack pin chain longer than one hop (stack A pins B, which
itself pins C — whether `pinned_head` staleness propagates transitively);
a `$computed` value that's itself the target of a *cross-stack* reference
from a third stack (docs/resolver.md's own "propagates forward, unresolved"
case is named as real but not given its own adversarial row here); resolver
behavior under a provider schema that changes shape between resolve time
and ship time (a provider version bump mid-flight). These are candidates
for a later extension of this table, not silently assumed handled.
