# Executor reliability report (UBI-26)

> Drafted from docs/executor-adversarial.md's program, the hermetic test
> suite's results, and this session's own live runs against real AWS
> infrastructure (account `839333509514`, bucket `ubx-states`, region
> `us-east-1`). Every transcript below is real command output, not
> reconstructed — proposal IDs, hashes, and timestamps are exactly what
> `ubx` produced. Written to be published: claims, injections, observed
> outcomes. No adjectives.

## Scope

v1 executes exactly one proposal kind: `drift_revert` (docs/executor.md —
Scope). This report covers `ubx ship`'s behavior under the failure
injections named in docs/executor-adversarial.md, both hermetically
(`core/executor`'s own test suite, a fake `Applier`) and live (a real
`hashicorp/aws` provider binary, a real S3 bucket).

## The program: hermetic status

Every row below has a passing hermetic test in `core/executor/ship_test.go`
(`go test ./core/executor/... -race`, run as part of every commit to this
repository — none of these are stale claims).

| # | Scenario | Hermetic test | Live-verified this session |
| --- | --- | --- | --- |
| 1 | Provider killed mid-apply | `TestShip_RetryableError_TriggersReconciliation_ResolvesFailed` (a transport-style error, the closest hermetic proxy for an RPC that never resolved) | Yes — Section 2, real `kill -9` |
| 2 | Timeout where the change actually landed | `TestShip_TimeoutWhereChangeLanded_ResolvesApplied` | Yes — Section 2 |
| 3 | Timeout where it did not land | `TestShip_TimeoutWhereChangeDidNotLand_ResolvesFailed` | No (hermetic only) |
| 4 | Crash between the `in_flight` write and the call | `TestShip_CrashBetweenInFlightWriteAndCall_NeverLanded_RetriedAsFailed` | No (hermetic only) |
| 5 | Crash between the call returning and the result being written | `TestShip_CrashBetweenCallAndResultWrite_AlreadyLanded_ResolvesApplied`, `TestShip_CrashAfterApplyLanded_PureDeletionRevert_ReconciliationResolvesApplied` | Yes — Section 2 (the centerpiece) |
| 6a | Provider error taxonomy: retryable | `TestShip_RetryableError_TriggersReconciliation_ResolvesFailed` | No (hermetic only) |
| 6b | Provider error taxonomy: terminal | `TestShip_TerminalError_FailsImmediately_NoReconciliation` | No (hermetic only; live-verified in an earlier session against fakeprovider's own subprocess, see STATE.md UBI-26 session 3) |
| 7 | Stale detected mid-partial-apply | `TestShip_StaleDetectedMidPartialApply_HaltsRemainingResources` | Yes — Section 3 (single-resource variant: stale before the resource's own attempt, not mid-multi-resource-apply) |
| 8 | Double `ship` invocation racing | `TestShip_ConcurrentInvocations_NeverCollideOnAttemptNumber` | No (hermetic only) |
| 9 | Apply record corrupted/truncated on re-run | `TestShip_CorruptApplyRecord_RefusesToGuess` | No (hermetic only) |

Two additional, permanent hermetic tests beyond the named rows:
`TestShip_AlreadyFullyApplied_IsANoOp` (idempotency) and
`TestShip_RetryBudgetExhausted_RefusesFurtherAttempts` (the per-resource
retry budget). Two more guard the redaction requirement:
`TestShip_RedactedAfterValue_Declined_NeverAppliesOrReads` and
`TestShip_RedactedAfterValue_RetriedForever_NeverSilentlySkipped`.

## Real bugs found and fixed by this verification work

Neither of these was visible from `go test ./...` alone. Both were caught
by running the actual built binary end to end — one against a local
subprocess fixture (session 3), one against real AWS (session 4, this
report) — before either shipped as a permanent regression.

1. **`core.ApplyAfter` never deleted a `Before`-only path** (session 3).
   An attribute added out-of-band (present in observed/drifted reality,
   absent from the ledger's own recorded truth) has a `Before` entry and no
   `After` entry at all — there is no ledger value to record for something
   the ledger never had. `ApplyAfter`'s original version only ever *set*
   `After`'s paths onto the prior state, so a shipped revert reported
   `applied` while silently leaving the added attribute in place. Fixed
   with `core.dotDelete`; `ApplyAfter` now deletes every `Before`-only path
   after applying every `After` one.
2. **`reconciliationVerdict` never concluded `applied` for a pure-deletion
   revert** (session 4, found live — see Section 2 below). The same class
   of gap as (1), one layer over: when a `drift_revert`'s `After` map is
   empty (nothing to add back, only something to remove), the original
   `matchesAll(state, mod.After)` check could never return true for an
   empty `want` map — reconciliation reported `still_unknown` forever, even
   reading a live state that had, in fact, already been correctly
   corrected. Fixed with `matchesRestoreTarget`/`matchesOriginalDrift`,
   which also check that every `Before`-only path is genuinely absent (or,
   for the "never landed" case, still present) from the observed state.

Both fixes are covered by permanent hermetic regression tests
(`core/apply_test.go`'s `TestApplyAfter_DeletesBeforeOnlyPaths`;
`core/executor/ship_test.go`'s
`TestShip_CrashAfterApplyLanded_PureDeletionRevert_ReconciliationResolvesApplied`)
in addition to the live re-verification captured below.

## Live verification: real AWS

Real AWS account `839333509514`, IAM user `roozbeh`, bucket `ubx-states`
(`us-east-1`), the same ledger/bucket this project's live-verification work
has used since UBI-9/UBI-10. Ledger directory: `/Users/roozbeh/demo/payments`.
Provider: `hashicorp/aws` 6.54.0, acquired via `provider.Acquire`
(checksum-verified). Baseline confirmed before starting: the bucket carried
no tags (`NoSuchTagSet`) plus whatever the ledger's own `FoldState` already
recorded as current truth from prior sessions' own live work (`hotfix`,
`purpose` — recorded but not yet written back to the cloud until this
session; see Section 1).

### Section 1 — first real cloud write

```text
$ aws s3api get-bucket-tagging --bucket ubx-states
An error occurred (NoSuchTagSet) when calling the GetBucketTagging operation: The TagSet does not exist

$ aws s3api put-bucket-tagging --bucket ubx-states --tagging '{"TagSet":[{"Key":"incident","Value":"UBI-26-live-verify"}]}'
(exit 0)

$ aws s3api get-bucket-tagging --bucket ubx-states
{ "TagSet": [ { "Key": "incident", "Value": "UBI-26-live-verify" } ] }
```

```text
$ ubx scan --ledger-dir . --stack payments --type aws_s3_bucket --name ubx-states \
    --lookup '{"id":"ubx-states","bucket":"ubx-states"}' \
    --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}' \
    --propose revert --out /tmp/revert1.json
drifted: payments.aws_s3_bucket.ubx-states (2e5a9c4dfa23461cf616c5497adc5d13095be6904b5264b8cadc9385f8650a4a) -- generated a "drift_revert" proposal
(exit 1)
```

The ledger's own recorded truth already carried `hotfix`/`purpose` tags
from prior sessions' own drift_adopt recordings, never previously written
back to the cloud. The generated proposal's real effect: remove `incident`,
restore `hotfix`/`purpose`:

```json
"before": { "tags.incident": "UBI-26-live-verify", "tags_all.incident": "UBI-26-live-verify" },
"after": {
  "tags.hotfix": "incident-412", "tags.purpose": "ubx-mcp-live-verify",
  "tags_all.hotfix": "incident-412", "tags_all.purpose": "ubx-mcp-live-verify"
}
```

```text
$ ubx accept /tmp/revert1.json --ledger-dir .
accepted 52eabf53cfe7cf2b159608cc2da39bf8b8705ff3eb0703232ad89a913d022410 (stack payments)

$ ubx ship 52eabf53cfe7cf2b159608cc2da39bf8b8705ff3eb0703232ad89a913d022410 --ledger-dir . \
    --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
applied: payments.aws_s3_bucket.ubx-states
1 resource(s), 1 applied, 0 failed, 0 still unknown -- outcome: applied
(exit 0)
```

Independent verification, not just the report:

```text
$ aws s3api get-bucket-tagging --bucket ubx-states
{
  "TagSet": [
    { "Key": "purpose", "Value": "ubx-mcp-live-verify" },
    { "Key": "hotfix", "Value": "incident-412" }
  ]
}
```

Matches the proposal's `after` exactly. The apply record's own
`provider_result.tags`/`tags_all` match this too, byte for byte.

`ubx why` did not originally render anything about the apply at all — a
real gap found here, fixed same session (`cli/why.go` gains
`renderApplies`, `whyJSON.Applies`), re-verified against this same real
proposal:

```text
$ ubx why 52eabf53cfe7cf2b159608cc2da39bf8b8705ff3eb0703232ad89a913d022410 --ledger-dir .
proposal 52eabf53cfe7cf2b159608cc2da39bf8b8705ff3eb0703232ad89a913d022410 (drift_revert)
stack:  payments
status: accepted
intent: revert payments.aws_s3_bucket.ubx-states back to the ledger's recorded state
accepted by [roozbeh] via local at 2026-07-17T11:34:19Z
blast radius: +0 ~1 -0
change: payments.aws_s3_bucket.ubx-states: tags.hotfix: (absent) -> "incident-412"
change: payments.aws_s3_bucket.ubx-states: tags.incident: "UBI-26-live-verify" -> (absent)
change: payments.aws_s3_bucket.ubx-states: tags.purpose: (absent) -> "ubx-mcp-live-verify"
change: payments.aws_s3_bucket.ubx-states: tags_all.hotfix: (absent) -> "incident-412"
change: payments.aws_s3_bucket.ubx-states: tags_all.incident: "UBI-26-live-verify" -> (absent)
change: payments.aws_s3_bucket.ubx-states: tags_all.purpose: (absent) -> "ubx-mcp-live-verify"
apply history:
  attempt 1: outcome=applied
    payments.aws_s3_bucket.ubx-states:
      pending at 2026-07-17T11:34:28Z
      in_flight at 2026-07-17T11:34:35Z
      applied at 2026-07-17T11:34:37Z
```

Second `ship`, idempotent:

```text
$ ubx ship 52eabf53cfe7cf2b159608cc2da39bf8b8705ff3eb0703232ad89a913d022410 --ledger-dir . \
    --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
52eabf53cfe7cf2b159608cc2da39bf8b8705ff3eb0703232ad89a913d022410: already fully applied -- nothing to do
(exit 0)

$ ls ledger/applies/
52eabf53cfe7cf2b159608cc2da39bf8b8705ff3eb0703232ad89a913d022410.attempt-1.apply.json
```

No second attempt file was written.

### Section 2 — the centerpiece: a real `kill -9` mid-apply

Fresh drift induced (`killtest2=before-kill-2`), scanned, accepted
(proposal `f0aae5503e5657007f5086196b7cf880b5dc0c4753a8d536c1b5fa45a5ce5e2c`).
`ubx ship` was run with `UBX_SHIP_DEBUG_DELAY_AFTER_APPLY_SUCCESS=30s` — a
package-level test seam (`core/executor/ship.go`, zero by default, gated by
an env var following this codebase's existing `UBX_*` test-knob
convention) that sleeps immediately after `ApplyResourceChange` returns
successfully, before the `applied` transition and `provider_result` are
persisted. This makes the exact window docs/executor-adversarial.md row 5
describes ("crash between the call returning and the result being
written") reproducible on demand against a real network call, rather than
a matter of chance timing.

```text
$ UBX_SHIP_DEBUG_DELAY_AFTER_APPLY_SUCCESS=30s ubx ship f0aae5503e5657007f5086196b7cf880b5dc0c4753a8d536c1b5fa45a5ce5e2c \
    --ledger-dir . --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}' &
pid=39631
$ sleep 15
$ kill -9 39631
confirmed: process no longer exists
```

Checked immediately: the real mutation had already landed, but `ubx`'s own
apply record had not yet recorded it —

```text
$ aws s3api get-bucket-tagging --bucket ubx-states
{ "TagSet": [ { "Key": "purpose", ... }, { "Key": "hotfix", ... } ] }
    -- "killtest2" already gone: the real ApplyResourceChange call succeeded

$ cat ledger/applies/f0aae550....attempt-1.apply.json
{
  "resources": [{
    "address": {"stack": "payments", "type": "aws_s3_bucket", "name": "ubx-states"},
    "transitions": [
      {"state": "pending", "at": "2026-07-17T11:43:43Z"},
      {"state": "in_flight", "at": "2026-07-17T11:43:52Z"}
    ]
  }]
}
    -- unsealed: no "id", no "applied" transition, no provider_result
```

Re-running `ubx ship` at this point surfaced the second bug named above —
reconciliation could not conclude `applied` for this pure-deletion revert
(`After` empty), reporting `still_unknown` after exhausting its retry
budget:

```text
$ ubx ship f0aae5503e5657007f5086196b7cf880b5dc0c4753a8d536c1b5fa45a5ce5e2c ...
still_unknown: payments.aws_s3_bucket.ubx-states
1 resource(s), 0 applied, 0 failed, 1 still unknown -- outcome: failed
(exit 1)
```

Fixed (`matchesRestoreTarget`/`matchesOriginalDrift`, see above), rebuilt,
re-run against this same still-unresolved real proposal:

```text
$ ubx ship f0aae5503e5657007f5086196b7cf880b5dc0c4753a8d536c1b5fa45a5ce5e2c ...
applied: payments.aws_s3_bucket.ubx-states
1 resource(s), 1 applied, 0 failed, 0 still unknown -- outcome: applied
(exit 0)
```

```text
$ aws s3api get-bucket-tagging --bucket ubx-states
{ "TagSet": [ { "Key": "purpose", ... }, { "Key": "hotfix", ... } ] }
    -- unchanged from immediately after the kill: no second mutation was issued
```

```text
$ cat ledger/applies/f0aae550....attempt-3.apply.json
{
  "parent": "1b60f1a42e47ee5beec1e9e76aabf631959df979477847c3ed5511021d0b011a",
  "attempt": 3,
  "resources": [{
    "transitions": [
      {"state": "pending", "at": "2026-07-17T11:47:27Z"},
      {"state": "applied", "at": "2026-07-17T11:47:31Z", "detail": "confirmed by reconciliation"}
    ],
    "reconciliation": [{"at": "2026-07-17T11:47:31Z", "outcome": "applied"}]
  }],
  "summary": {"outcome": "applied", "resources_applied": 1, "resources_failed": 0, "resources_still_unknown": 0}
}
```

`attempt` correctly counts 3 (the killed attempt, the still-buggy
reconciliation attempt, and this one); `parent` correctly chains to attempt
2's sealed ID. No `in_flight` transition in attempt 3 at all — resolved
entirely by reconciliation's first read, no new `ApplyResourceChange` call
issued.

Full story, `ubx why`, across all three attempts:

```text
$ ubx why f0aae5503e5657007f5086196b7cf880b5dc0c4753a8d536c1b5fa45a5ce5e2c --ledger-dir .
...
apply history:
  attempt 1: unsealed (interrupted or still in progress)
    payments.aws_s3_bucket.ubx-states:
      pending at 2026-07-17T11:43:43Z
      in_flight at 2026-07-17T11:43:52Z
  attempt 2: outcome=failed
    payments.aws_s3_bucket.ubx-states:
      pending at 2026-07-17T11:45:00Z
      still_unknown at 2026-07-17T11:45:20Z -- reconciliation exhausted its retry budget without a conclusive answer
      reconcile: inconclusive at 2026-07-17T11:45:05Z
      reconcile: inconclusive at 2026-07-17T11:45:09Z
      reconcile: inconclusive at 2026-07-17T11:45:13Z
      reconcile: inconclusive at 2026-07-17T11:45:16Z
      reconcile: inconclusive at 2026-07-17T11:45:20Z
  attempt 3: outcome=applied
    payments.aws_s3_bucket.ubx-states:
      pending at 2026-07-17T11:47:27Z
      applied at 2026-07-17T11:47:31Z -- confirmed by reconciliation
      reconcile: applied at 2026-07-17T11:47:31Z
```

An earlier attempt at this same scenario (proposal
`9f9b5b72508cad70ae1754736128d98fbfb1f89e7a3de9c5fa48230845f2d3a3`) missed
the timing window entirely: the `kill -9` command ran after the 15-second
artificial delay had already elapsed, and the process exited normally,
sealed `applied`. Recorded rather than discarded — a real, if uneventful,
successful ship. The scenario was redone with a longer delay (30s) and a
single tight launch-sleep-kill shell sequence, producing the run captured
above.

### Section 3 — stale mid-flow

Fresh drift induced (`staletest=before-accept`), scanned, accepted
(proposal `15483568a555d9a33cdca197df899d0598cec8b9431419e886ece04a69fa6ed8`).
Reality then mutated a second time, for real, before shipping:

```text
$ aws s3api put-bucket-tagging --bucket ubx-states --tagging '{"TagSet":[...,{"Key":"staletest","Value":"after-accept-drift-again"}]}'
```

```text
$ ubx ship 15483568a555d9a33cdca197df899d0598cec8b9431419e886ece04a69fa6ed8 ...
pending: payments.aws_s3_bucket.ubx-states
  terminal: stale observation: reality changed since this proposal was generated: payments.aws_s3_bucket.ubx-states recorded ce3049d5a5f7c5933015840d07f0200e116a77fc09f68b87b1ba24d076405c60, now 1bf4f8f14d79f250fbf6794d94613719edce51cd35eb9305d2c64ac2b781c870
1 resource(s), 0 applied, 1 failed, 0 still unknown -- outcome: failed
(exit 1)
```

```text
$ aws s3api get-bucket-tagging --bucket ubx-states
    -- unchanged: still the second, unshipped drift ("staletest" present)

$ cat ledger/applies/15483568....attempt-1.apply.json
{
  "resources": [{
    "transitions": [{"state": "pending", "at": "2026-07-17T11:51:27Z"}],
    "errors": [{"message": "stale observation: ...", "classification": "terminal"}]
  }],
  "summary": {"outcome": "failed", "resources_applied": 0, "resources_failed": 1}
}
```

Never reached `in_flight`. Refused, not bulldozed.

## Account state at the end of this session

The bucket carries exactly `{hotfix: incident-412, purpose: ubx-mcp-live-verify}`
— matching the ledger's own `FoldState`-reconstructed truth exactly, and
matching the convention every prior live-verification session on this same
demo ledger has followed (STATE.md history): the ledger's own recorded
truth is the baseline going forward, not a literal return to "untagged."
This session's own throwaway test artifacts (`incident`, `killtest`,
`killtest2`, `staletest`) were all removed. Confirmed with a final
`ubx status --drift`:

```text
$ ubx status --ledger-dir . --drift --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
clean: payments.aws_s3_bucket.ubx-states: drift_revert 15483568a555… (accepted 2026-07-17T11:51:01Z)
1 resource(s), 0 drifted, 0 unreadable
(exit 0)
```

No IAM changes were made. No resources were created or destroyed — every
mutation this session made was a tag change on the same pre-existing
bucket, three times reverted or confirmed by `ubx` itself, once by direct
`aws s3api` calls to simulate out-of-band drift (the same technique every
prior live-verification session on this codebase has used).

## What this report doesn't cover

Same disclaimer as docs/executor-adversarial.md's own, extended: rows 3,
4, 6a, 8, and 9 are hermetic-only as of this report — real infrastructure
was not used to reproduce a real provider timeout (3), a crash before the
call (4), a genuinely retryable transport error rather than a terminal
diagnostic (6a) as a *separate* case from what Section 2 already covers, a
real two-process race (8), or a real corrupted apply-record file (9). None
of these need real cloud infrastructure specifically to be meaningfully
tested — they're process-level/filesystem-level scenarios already exactly
reproduced hermetically — so this is a scope note, not a gap being carried
forward silently.
