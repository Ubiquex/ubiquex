# Destroys adversarial program (UBI-30)

> Every row here is a failure injected on purpose, with a REQUIRED
> observable outcome — written as the spec, before any code exists to pass
> it, the same discipline docs/executor-adversarial.md and
> docs/resolver-adversarial.md already established for creates/modifies.
> Each row becomes a test in `core/resolver`'s or `core/executor`'s
> hermetic suite (fake schemas/ledger state for resolve-time rows, a fake
> provider capable of scripting exactly these failures for ship-time rows)
> before it becomes a claim about real infrastructure. This document is
> also a future published reliability report, alongside
> docs/executor-adversarial.md/docs/resolver-adversarial.md/
> docs/reliability-report.md: read each row as a claim `ubx` makes about
> its own behavior destroying a resource, not just a test-plan checklist.
>
> This is the *minimum* required program, per UBI-30's own handoff — not a
> claim that failure space around destroys is exhausted. See "What this
> table doesn't yet cover," below, for named gaps.

## How to read this table

"Injection" describes exactly what's forced to happen and when, relative
to docs/resolver.md's own destroy-resolution rules (Amendment,
2026-07-17) and docs/executor.md's own destroy state machine (Amendment,
2026-07-17). "Required outcome" is the full observable contract: what the
resolved proposal or apply record must show, what state the resource must
end in, and what a re-run or a subsequent `ubx why` must do next. A row
that can't be made to produce its required outcome is a bug, not an
acceptable gap — this table has no "known limitation" column, same
standard as its two siblings.

## The program

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 1 | Destroy target drifted since acceptance | A `change` proposal carrying a destroy is accepted; before `ubx ship` reaches this resource's own turn, its live state is mutated out-of-band (a real, independent change) so the pre-attempt recheck finds it present but not matching the `destroy_target` resolution input's recorded state. | Classified `present_drifted`. The resource is refused *before ever reaching `in_flight`* — stays `pending`, gains a terminal `errors[]` entry naming what changed. If other resources in the same attempt already reached a terminal success, their outcomes stand untouched; the attempt seals `partially_applied`, never bulldozing ahead with a destroy against state nobody actually reviewed. |
| 2 | Kill -9 mid-destroy, before the call | `ubx ship` is killed (`SIGKILL`) after durably writing the destroy resource's `in_flight` transition but strictly before the `ApplyResourceChange` RPC is issued at all. | Re-run finds no reconciliation history to fold over for this resource (it never reached a point where the pre-attempt check or the call itself produced evidence beyond the bare `in_flight` write) and runs an ordinary fresh pre-attempt recheck — live state is unchanged (the call genuinely never happened), classified `present_matches` again, and the destroy proceeds normally. No false `destroyed`/`already_absent` is ever reported for a call that never happened. |
| 3 | Kill -9 mid-destroy, after the call | `ApplyResourceChange` succeeds over the wire (the provider actually destroys the resource and returns cleanly), but `ubx ship` is killed before writing the terminal `applied`/`Outcome: destroyed` transition into the apply record. | A new attempt (new apply record, `parent`-chained to the incomplete prior one) folds this resource's history across the chain (`foldResourceHistory`, reused unchanged from UBI-27), finds the prior attempt's own pre-attempt check recorded `present_matches` immediately before the crash, runs its own reconcile-by-query, observes the target genuinely gone, and correctly resolves `applied`/`Outcome: "destroyed"` on the *new* attempt — never re-attempting a destroy against something already gone, and never misreporting it as `already_absent` just because this specific attempt never ran its own pre-check. |
| 4 | Timeout where the destroy actually landed | `ApplyResourceChange`'s response is delayed past `ubx ship`'s own deadline, but the provider committed the destroy server-side before the deadline fires. | Reconciliation's `ReadResource` observes the target genuinely gone. Because the immediately preceding reconciliation entry for this resource (the mandatory pre-attempt recheck) recorded `present_matches`, the not-found read is attributed to this attempt and resolves `applied`/`Outcome: "destroyed"` — proving reconciliation trusts freshly observed reality plus the prior observation over the timeout's own apparent failure. |
| 5 | Timeout where it did not land | Same deadline-exceeded shape as #4, but the provider never actually committed the destroy. | Reconciliation observes the target's original (pre-destroy, still-present) state unchanged and resolves `failed` — never `applied`, never `already_absent`. The resource is eligible for retry on the next `ubx ship` invocation, within the per-resource retry budget, running a fresh pre-attempt recheck first. |
| 6 | Destroy of an already-absent resource | The destroy target is removed out-of-band (or was already gone at accept time in a way freshness at accept didn't happen to catch) before `ubx ship` ever reaches this resource's turn. | The pre-attempt recheck finds the target absent and short-circuits directly to a terminal `applied`/`Outcome: "already_absent"` — the resource never reaches `in_flight`, `ApplyResourceChange` is never called. This is a legitimate success, not a refusal: distinct from row 1 (drifted-but-present, which *is* refused) because there is nothing left to lose that the operator didn't already implicitly ask to lose. |
| 7 | Orphan-protection refusal | An intent file's `destroys[]` names an address that another, still-surviving resource's own recorded `depends_on` (from any previously accepted proposal in this stack's ledger, not just the current batch) names via `$ref`, and that referencing resource is not also being destroyed in the same batch. | Resolve fails hard, before any proposal is even produced — naming the destroy target's address and the surviving resource that still depends on it. Never silently allowed through, never resolved as if the reference didn't exist. Accepting/shipping never enters the picture; the proposal was never valid to begin with. |
| 8 | Mixed create+destroy proposal ordering | One `change` proposal creates a replacement resource, modifies a dependent resource to point at the replacement, and destroys the original — all three in one proposal, with `depends_on` edges recorded exactly as docs/resolver.md's own orphan-protection walk would produce them (`modify` depends_on `create`; `destroy` depends_on `modify`, the reverse edge). | `core/executor`'s single combined topo-sort (`changeNodesOf`/`topoSortAddresses`, extended for `Delta.Destroys`) produces exactly `create → modify → destroy`, never any other order and never three separately-phased passes (all creates, then all modifies, then all destroys) that would violate the recorded `depends_on` edges. The destroy is never attempted while the modify that repoints away from it hasn't yet completed. |
| 9 | Destroy racing a concurrent scan | `ubx scan`/`ubx status --drift` is invoked against the same stack while a `ubx ship` destroy is mid-flight (after `in_flight` is durably written, before the terminal transition is recorded). | The scan/status read observes either the resource still present (a consistent pre-destroy read) or already gone (a consistent post-tombstone `FoldState` read, once sealed) — never a torn or partially-destroyed view, since a reader only ever sees the most recently *sealed* apply record or none at all, never a mid-attempt `.apply.json` with no `id` yet. Neither outcome is reported as an error; both are honest, momentarily-different-but-individually-correct snapshots of an in-progress operation. |
| 10 | Re-ship after partial destroy | A three-resource mixed proposal (two destroys, one create, with a `depends_on` edge between them); the first destroy in topological order completes cleanly, the process is killed before the second resource's own turn begins. | The first resource's `applied`/`Outcome` stands untouched (already happened, nothing to undo) on re-run. The attempt that recorded it sealed `partially_applied`, not `failed`. A subsequent `ubx ship` invocation resumes with the remaining resources in the same topological order, skipping the already-terminal one entirely (the existing idempotency contract's `applied` row, unchanged) — never re-attempting a destroy that already landed, never skipping a resource that hasn't had its own turn yet. |
| 11 | `ubx why` on a destroyed address | A resource with a real multi-proposal history (genesis create, one or more drift/modify proposals, then an accepted-and-shipped destroy) is queried via `ubx why <address>` after the destroy's own apply record seals. | The full biography renders, oldest to newest, unchanged in mechanism from any other address (docs/schema.md's own "tombstone posture" — nothing about the chain is rewritten or collapsed) — with the destroy proposal rendered as the chain's terminal record, and its own apply record's outcome (`destroyed` vs. `already_absent`) shown explicitly, not folded into a generic "applied." `ubx why` never reports "no proposals found" for an address that has real history, regardless of whether the resource itself currently exists. |

## What this table doesn't yet cover, named rather than assumed

Not covered by a row above, and therefore not yet a claim `ubx` makes about
itself: a destroy racing a *second, concurrent* `ubx ship` invocation
against the *same* proposal (docs/executor-adversarial.md's own row 8
covers this generally for the shared ledger lock; a destroy-specific
instance isn't separately exercised here, since nothing about the lock's
own contention handling is destroy-specific); cross-stack orphan protection
actually refusing a real destroy against a real, populated neighbor ledger
(the mechanism is best-effort and explicit by design, docs/resolver.md's
own amendment — this table doesn't yet include a row proving the refusal
path itself against a real second ledger directory, only the intra-stack
case, row 7); re-creating a resource under the same address after a real
tombstone (docs/schema.md's own amendment names this as a real, open
presentation question for `ubx why`, not resolved by this table); a destroy
target whose provider schema has no meaningful "destroy" semantics at all
(a join/attachment resource whose removal is itself a `ForceNew`-shaped
replace in the provider's own model, the same category `conformance/registry.go`
already parks a few AWS types under for adopt/mutate — whether that
category needs its own destroy row is a real question for the session that
first exercises a destroy against a live parked type, not decided here).
These are candidates for a later extension of this table, not silently
assumed handled by rows 1–11 above.
