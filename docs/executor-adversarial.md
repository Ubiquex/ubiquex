# Executor adversarial program (UBI-26)

> Every row here is a failure injected on purpose, with a REQUIRED
> observable outcome — written as the spec, before any code exists to pass
> it. Each row becomes a test in `core/executor`'s hermetic suite (against a
> fake provider capable of scripting exactly these failures) before it
> becomes a claim about the real one. This document is also the future
> published reliability report: read each row as a claim `ubx` makes about
> its own behavior under failure, not just a test-plan checklist.
>
> **This program's own status against both the hermetic suite and real AWS
> infrastructure is tracked in docs/reliability-report.md** (UBI-26 session
> 4) — including two real bugs this table's own live verification found and
> fixed, and the real transcripts of every row exercised against real
> cloud.

## How to read this table

"Injection" describes exactly what's forced to happen and when, relative to
the state machine in docs/executor.md. "Required outcome" is the full
observable contract: what the apply record must show, what state the
resource must end this attempt in, and what a re-run must do next. A row
that can't be made to produce its required outcome is a bug, not an
acceptable gap — this table has no "known limitation" column.

## The program

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 1 | Provider killed mid-apply | The provider subprocess is killed (`SIGKILL`) after `ubx ship` has issued `ApplyResourceChange` but before any response is received. | The resource's `in_flight` transition is already durably on disk (written before the call — THE invariant). This attempt ends the resource at `unknown_post_timeout`. Reconciliation (`ReadResource` against the restore target) runs: if the kill happened to land the change anyway, resolves `applied`; if not, resolves `failed`; if reconciliation itself can't get a clear read within its bounded retries, `still_unknown`. The attempt seals `applied`/`partially_applied`/`failed` accordingly — never silently reported as if nothing happened. |
| 2 | Timeout where the change actually landed | `ApplyResourceChange`'s response is delayed past `ubx ship`'s own deadline, but the provider committed the change server-side before the deadline fires. | Reconciliation's `ReadResource` observes the restore target's value live and resolves `applied` — proving reconciliation trusts freshly observed reality over the timeout's own apparent failure, not the other way around. |
| 3 | Timeout where it did not land | Same deadline-exceeded shape as #2, but the provider never actually committed anything. | Reconciliation observes the original (pre-revert, drifted) value unchanged and resolves `failed` — never `applied`. The resource is eligible for retry on the next `ubx ship` invocation, within the per-resource retry budget. |
| 4 | Crash between the `in_flight` write and the call | `ubx ship` is killed (`SIGKILL`) after durably writing the `in_flight` transition but strictly before the `ApplyResourceChange` RPC is issued at all. | Re-run's reconciliation observes live state unchanged (the call genuinely never happened) and resolves `failed`. Retried normally on the next attempt — no special-case branch needed, and critically: no false `applied` is ever reported, since reconciliation always checks live truth rather than trusting the transition log's own say-so. |
| 5 | Crash between the call returning and the result being written | `ApplyResourceChange` succeeds over the wire (the provider commits the change and returns cleanly), but `ubx ship` is killed before writing `applied` + `provider_result` into the apply record. | A new attempt (new apply record, `parent`-chained to the incomplete prior one) runs reconciliation first for this resource, observes the already-landed change, and resolves `applied` on the *new* attempt. The resource is never re-applied a second time once reconciliation confirms it already landed — proving reconciliation, not blind retry, is what idempotency actually rests on. |
| 6a | Provider error taxonomy: retryable | `ApplyResourceChange` returns (or the transport itself produces) a transient failure — a `context.DeadlineExceeded` before any response, or a transport reset — with no diagnostic explicitly marking it unrecoverable. | Classified `retryable`. The resource remains eligible for a further attempt within budget; no permanent `failed` is recorded yet from this signal alone. |
| 6b | Provider error taxonomy: terminal | `ApplyResourceChange_Response` returns a structured diagnostic with `Severity: ERROR` (the provider explicitly rejected the change — e.g. an invalid attribute value). | Classified `terminal`. The resource resolves `failed` immediately; no retry is attempted within this `ship` invocation even if retry budget remains. |
| 7 | Stale detected mid-partial-apply | A three-resource `drift_revert` proposal; resources 1 and 2 apply cleanly; before resource 3's own `VerifyFreshness` check, its live state is mutated out-of-band (a real, independent drift) so the check fails. | Resources 1–2's `applied` transitions stand untouched (already happened, nothing to undo). Resource 3 is refused *before ever reaching `in_flight`* — stays `pending`, gains an `errors[]` entry classified `terminal` for this attempt (retrying the same plan is wrong; reality moved). The attempt seals `partially_applied`, not `failed` — real partial progress reported as exactly that. |
| 8 | Double `ship` invocation racing | Two `ubx ship <same-proposal-id>` processes are launched concurrently against the same proposal. | The ledger lock (reused from `Append`'s own contention handling) serializes attempt-number assignment. The loser either (a) blocks, then proceeds against the winner's now-sealed outcome (skipping already-`applied` resources per the idempotency contract), or (b) if the winner is still running past the wait window, fails with the same `ErrLedgerLocked`-shaped error `Accept` already produces. Two attempts never write the same attempt number, and no apply record is ever partially overwritten by the other process. |
| 9 | Apply record corrupted/truncated on re-run | The most recent (highest-attempt) apply record file for a proposal is hand-corrupted or truncated (simulating a disk fault or an interrupted write that wasn't durably completed) before `ubx ship` is re-run. | `ubx ship` refuses to proceed by guessing at what state resources were actually left in. It fails hard, naming the corrupt file's path — mirroring `core.Ledger.Read`'s own `ErrCorruptLedgerEntry` posture exactly. Recovery is a human decision (consistent with the ledger lock's own "never auto-removed" precedent, docs/architecture.md — Ledger lock) — `ubx ship` never silently starts a fresh attempt over a record it can't actually read, since that could silently re-apply something the corrupt record had already recorded as done. |

## What this table doesn't yet cover, named rather than assumed

This is the *minimum* program, per the handoff — not a claim that failure
space is exhausted. Explicitly not yet covered by a row (and therefore not
yet a claim `ubx` makes about itself): partial writes at the filesystem
level below the "process killed" granularity (e.g. a torn write from power
loss mid-`fsync`); provider processes that hang indefinitely without ever
timing out at the transport layer; clock skew between `ubx`'s own
timestamps and a remote provider's. These are candidates for a later
extension of this same table, not silently assumed handled by rows 1–9
above.
