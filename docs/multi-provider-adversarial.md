# Multi-provider stacks adversarial program (UBI-43)

> Every row here is a failure or edge case injected on purpose, with a
> REQUIRED observable outcome — written as the spec, before any code
> exists to pass it, the same discipline docs/executor-adversarial.md,
> docs/resolver-adversarial.md, and docs/destroys-adversarial.md already
> established. Each row becomes a test in `core/resolver`'s or
> `core/executor`'s hermetic suite (fake multi-provider `SchemaInspector`
> sets for resolve-time rows, a fake `Applier` pool capable of scripting
> exactly these failures for ship-time rows) before it becomes a claim
> about real infrastructure. This document is also a future published
> reliability report, alongside its three siblings: read each row as a
> claim `ubx` makes about its own behavior across multiple providers, not
> just a test-plan checklist.
>
> This is the *minimum* required program per UBI-43's own handoff — not a
> claim that failure space around multi-provider execution is exhausted.
> See "What this table doesn't yet cover," below, for named gaps.

## How to read this table

"Injection" describes exactly what's forced to happen and when, relative
to docs/resolver.md's own type→provider inference (Amendment,
2026-07-18) and docs/executor.md's own client-pool walk (Amendment,
2026-07-18). "Required outcome" is the full observable contract: what the
resolved proposal or apply record must show, what state each resource
must end in, and what a re-run or a subsequent `ubx why` must do next. A
row that can't be made to produce its required outcome is a bug, not an
acceptable gap — this table has no "known limitation" column, same
standard as its three siblings.

## The program

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 1 | Ambiguous type, no hint | A stack declares two providers whose schemas both advertise the same resource type name (e.g. two independently-sourced providers each claiming `example_widget`). An intent file names a `resources[]`/`destroys[]` entry of that type with no `"provider"` hint. | `ubx resolve` hard-fails before producing any proposal, naming the type and every declared provider that claims it (`resolve: type "example_widget" is ambiguous -- owned by both hashicorp/example and acme/example`). Never silently picks the first one checked, never picks by declaration order, never produces a proposal a human would have to notice was routed to the wrong provider after the fact. |
| 2 | Ambiguous type, resolved via hint | Same setup as #1, but the intent file's entry carries `"provider": {"source": "acme/example"}`. | Resolution succeeds; the resolved node's own `provider` field records `acme/example` at its currently-pinned version from the stack's `providers` config — not the hinted source's own literal string re-used verbatim (the pin still comes from config, the hint only selects *which* declared provider, never overrides its version). A hint naming a source that does NOT actually claim the type (`HasType` false for it) is refused with a clear error naming the mismatch, not silently accepted. |
| 3 | Unowned type | An intent file names a `resources[]`/`destroys[]` entry whose type no declared provider's schema advertises at all. | `ubx resolve` hard-fails before producing any proposal, naming the type and every provider checked (`resolve: no declared provider owns type "gcp_bucket" -- checked: hashicorp/aws, hashicorp/helm`). Adding a provider that owns the type to the stack's `providers` config and re-running resolves cleanly — proving the error is genuinely about declaration, not a permanent block. |
| 4 | Provider launch failure mid-walk | A `change` proposal spans two providers, both already resolved and signed; at `ubx ship` time, the *second* provider (needed by a node with no dependency on the first provider's own nodes, so nothing blocks it from being attempted early in the walk) fails to launch (bad credentials, unreachable registry, a crashed handshake) partway through the walk, after at least one node against the *first* (successfully launched) provider has already reached a terminal `applied`. | Every node needing the failed provider ends `failed`, `errors[]` naming the launch failure, never reaching `in_flight`. Every node against the successfully-launched provider proceeds and reaches its own correct terminal state, in its own dependency order, completely unaffected. The attempt seals `partially_applied`, never aborting the whole walk just because one provider among several couldn't launch. A subsequent `ubx ship` re-run, once whatever blocked the second provider's launch is fixed, resumes correctly — skipping the already-`applied` nodes via the existing idempotency contract, retrying only the ones that failed to launch. |
| 5 | Cross-provider `$ref` chain | A `change` proposal creates a resource via provider A whose `$computed`-marked output (e.g. `aws_db_instance.main.endpoint`) is referenced by a resource created via provider B (e.g. a `helm_release`'s own `values`), with the correct `depends_on` edge recorded between them across the provider boundary. | The combined topo-walk attempts provider A's node first (nothing else it could correctly do, per `depends_on`), and only substitutes the real applied `endpoint` into provider B's still-pending `PlannedState` once provider A's node reaches `applied` — identical in mechanism and timing to a same-provider `$computed` substitution (docs/executor.md's own UBI-27 amendment), never a special cross-provider code path, never a value silently left as an unresolved `$computed` marker when it reaches provider B's own `Apply` call. |
| 6 | Kill -9 between providers | A `change` proposal spans two providers with no dependency between their respective nodes (either walk order would be valid). `ubx ship` is killed (`SIGKILL`) after a node against provider A reaches a sealed `applied` transition but strictly before any node against provider B reaches `in_flight` — including before provider B's own client has been launched at all. | A re-run's client pool launches provider B for the first time on this attempt (provider A's own client is never re-launched needlessly for a node that's already `applied` and needs no further reads) — no re-attempt against provider A's own already-terminal node (existing idempotency contract, unchanged), and provider B's own nodes proceed through an ordinary fresh pre-attempt cycle, exactly as if this were the first attempt for them. Never a launch failure or a stale-pool error just because the pool itself didn't exist yet when the process was killed. |
| 7 | Per-provider freshness, one provider drifts and the other doesn't | A `change` proposal modifies one resource via provider A and one via provider B, both already signed. Before `ubx ship` reaches either resource's own turn, provider A's own resource is mutated out-of-band (real drift); provider B's is untouched. | Provider A's own node is refused at its freshness recheck (`present_drifted`, or the destroy-specific three-way precheck's own drifted outcome if it's a destroy), never reaching `in_flight`; provider B's own node proceeds normally through its own pool-supplied `Applier` and reaches its correct terminal state, completely unaffected by provider A's own drift. The attempt seals `partially_applied`. Each node's freshness check reads only its own provider's own live state — never cross-contaminated by another provider's drift, and never skipped just because a sibling node in the same walk failed its own check. |

## What this table doesn't yet cover, named rather than assumed

Not covered by a row above, and therefore not yet a claim `ubx` makes about
itself: two providers that both declare the *same* source string at
*different* pinned versions within one stack's `providers` config (config
validation's own job, not this table's — presumed a resolve-time config
error, not separately exercised here); a provider whose schema changes
shape *between* resolve and ship (the same staleness class
`VerifyFreshness`/cross-stack pin re-verification already cover for live
resource state and neighbor-ledger heads respectively, but never
separately exercised for a provider's own schema shifting underneath an
already-signed proposal — plausible only if the pinned version itself
changes out from under the acquire cache, which `provider.Acquire`'s own
explicit-pin, no-latest-resolution design already makes unlikely, not
impossible); a genuinely circular cross-provider dependency (provider A's
node depending on provider B's, and vice versa, within one proposal) — the
existing intra-stack cycle detection (docs/resolver.md's own "dependency
graph" section) is address-based and provider-agnostic, so it should catch
this identically to a same-provider cycle, but this table doesn't yet
include a row proving that specific claim for a cross-provider instance;
`ubx status --drift`/`ubx scan --all`'s own multi-provider fleet-grouping
behavior (docs/executor.md's own amendment names the design; this table's
rows are all `ubx ship`/`ubx resolve` scenarios, not scan/status ones — a
future session's own adversarial rows once that code exists); and the
`--source`/`--provider-version` deprecation-warning stage itself (staged,
not yet built — nothing to test adversarially until stage 2 lands).
