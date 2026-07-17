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

# UBI-27: shipping resolved `change` proposals — a real create chain on AWS

> Same discipline as the UBI-26 report above: every transcript below is
> real command output from this session, not reconstructed. Real AWS
> account `839333509514`, region `us-east-1`, provider `hashicorp/aws`
> 6.54.0. Ledger directory: a fresh scratch directory
> (`ubi27-create-chain`), not the long-lived `payments`/`ubx-states` demo
> ledger above — this work creates and destroys real resources, and stays
> out of that ledger's own history on purpose. No adjectives.

## Scope

docs/resolver.md's resolver produces `kind: "change"` proposals (creates +
modifies, no destroys); docs/executor.md's UBI-27 amendment extends `ubx
ship` to execute them — real tfplugin `Unknown` values for `$computed`,
dependency-ordered execution, applied outputs fed into still-pending
siblings mid-walk. This section is that mechanism's own first real-world
test: a genuine two-resource AWS create chain, shipped for real, killed
mid-chain on purpose, and reconciled.

## The chain

`aws_sqs_queue` (a queue) plus `aws_sqs_queue_policy` (a resource-based
policy attached to it via `queue_url`, `Required`, plain string) — cheap,
safe, and a genuine dependency: `queue_url` is only known once the queue
itself has been created (`aws_sqs_queue.url` is schema-`Computed`). The
policy's own `policy` attribute is a static, self-contained IAM document
(`Resource: "*"`, scoped to whichever queue it's attached to via
`queue_url`) — deliberately not templated with the queue's ARN, since
`$computed` substitution only ever replaces a whole config value, never
interpolates into a larger string (docs/resolver-adversarial.md row 6's
own boundary).

```json
{
  "type": "aws_sqs_queue_policy", "name": "chain-a-policy", "op": "create",
  "config": {
    "queue_url": {"$ref": {"to": "ubi27demo.aws_sqs_queue.chain-a.url"}},
    "policy": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Sid\":\"AllowAccountSend\",\"Effect\":\"Allow\",\"Principal\":{\"AWS\":\"arn:aws:iam::839333509514:root\"},\"Action\":\"sqs:SendMessage\",\"Resource\":\"*\"}]}"
  }
}
```

`ubx resolve` correctly marks `queue_url` `$computed` (schema-`Computed`,
referenced before its own resource exists) and records `depends_on`:

```text
$ ubx resolve intent-chain-a.json --source hashicorp/aws --provider-version 6.54.0 --out resolved-chain-a.json
resolved: ubi27demo: 2 create(s), 0 modify(ies)
```

```json
{
  "config": {
    "policy": "...",
    "queue_url": {"$computed": {"from": "ubi27demo.aws_sqs_queue.chain-a.url"}}
  },
  "depends_on": ["ubi27demo.aws_sqs_queue.chain-a"],
  "name": "chain-a-policy", "stack": "ubi27demo", "type": "aws_sqs_queue_policy"
}
```

## A real bug found shipping this for the first time: Configure was never called for a create

`ubx accept` then `ubx ship` against the real AWS provider:

```text
$ ubx ship 15f5a53484d3e413ab6bf1a6a5517e0fd68e898efaf0726a37c8683159f68f8b \
    --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}' --ledger-dir .
unknown_post_timeout: ubi27demo.aws_sqs_queue.chain-a
  retryable: rpc error: code = Unavailable desc = error reading from server: EOF
pending: ubi27demo.aws_sqs_queue_policy.chain-a-policy
  terminal: blocked: dependency ubi27demo.aws_sqs_queue.chain-a has not applied -- refusing to ship a resource ahead of what it depends on
2 resource(s), 0 applied, 1 failed, 1 still unknown -- outcome: failed
```

`aws sqs list-queues` confirmed nothing was actually created — the RPC
genuinely never landed; this wasn't a false negative. Root cause,
diagnosed by reproducing the exact `ApplyResourceChange` call directly
against the acquired provider binary: `core/executor`'s new `shipCreate`
never called `Applier.Configure` at all. Drift_revert's own modify path
gets `Configure` for free, indirectly, through `core.ReadAndFingerprint`/
`core.VerifyFreshness` (both call it internally before every read) — but
a create never reads anything first, so it reached a real AWS provider
that had never been told its region or credentials existed. Against
`terraform-provider-aws` this didn't surface as a clean configuration
error: the request killed the provider subprocess outright, surfacing to
`ubx` as a bare transport EOF, indistinguishable at first glance from a
genuine mid-call crash. Fixed by calling `app.Configure(ctx,
providerSchema, providerConfig)` explicitly in `shipCreate`, before the
first create's own schema is even used — the exact fix, and nothing more,
recorded in docs/executor.md's own amendment.

Re-run, same proposal, same real AWS account, after the fix:

```text
$ ubx ship 15f5a53484d3e413ab6bf1a6a5517e0fd68e898efaf0726a37c8683159f68f8b \
    --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}' --ledger-dir .
applied: ubi27demo.aws_sqs_queue.chain-a
applied: ubi27demo.aws_sqs_queue_policy.chain-a-policy
2 resource(s), 2 applied, 0 failed, 0 still unknown -- outcome: applied
```

`ubx` created real infrastructure for the first time. Verified
independently, never trusting `ubx`'s own report:

```text
$ aws sqs get-queue-attributes --queue-url https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi27-chain-a --attribute-names All --region us-east-1
{
  "QueueArn": "arn:aws:sqs:us-east-1:839333509514:ubx-ubi27-chain-a",
  "Policy": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Sid\":\"AllowAccountSend\",...,\"Resource\":\"*\"}]}",
  ...
}
```

And the flowed value itself, read directly from the sealed apply record
(not from `ubx`'s own summary line): the policy's own applied
`provider_result.queue_url` is byte-identical to the queue's own applied
`provider_result.url` — the `$computed` marker was replaced with the
queue's REAL, just-created URL, never a guess, never left as the marker:

```text
ubi27demo.aws_sqs_queue.chain-a          -> url: https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi27-chain-a
ubi27demo.aws_sqs_queue_policy.chain-a-policy -> queue_url: https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi27-chain-a
```

Both the resolve-time double-run mismatch fix (below) and this Configure
fix are covered by real fixes in the code, not just noted here: see
`core/executor/ship.go`'s `shipCreate`/`shipModifyNode` and
`core/resolver/resolver.go`'s `Resolve`.

## A second, unrelated real bug found the same session: `resolved_at` could break `DoubleRun`

Encountered running the full test suite after the AWS work above, not
during the live run itself: `core/resolver`'s own
`TestResolve_RefToExistingLedgeredResource_AlwaysConcrete` failed once,
non-deterministically, with `resolve: double-run mismatch`. `resolveOnce`
called `time.Now()` itself, fresh, on each of `core.DoubleRun`'s two
internal calls — a resolve whose two calls happened to straddle a
one-second boundary (RFC3339's own resolution) produced two genuinely
different `resolved_at` strings, a false-positive `ErrDoubleRunMismatch`
over a value that was never supposed to be checked for run-to-run
stability. Reproduced deliberately hard to pin down (didn't recur in 15
back-to-back runs after first appearing, nor in a 50-run stress loop after
the fix) — real, rare, and load-bearing for anyone running `ubx resolve`
for real, not a flaky-test annoyance to shrug off. Fixed by capturing
`resolvedAt` once, before either `DoubleRun` call, and threading it
through `resolveOnce` as a parameter instead of letting the function ask
the clock itself.

## The kill: a fresh chain, killed for real between one resource applying and the next starting

A second, independent chain (`chain-b`/`chain-b-policy`, same shape) —
resolved and accepted the same way. `ubx ship` run in the background with
a new debug-only delay hook (`UBX_SHIP_DEBUG_DELAY_BETWEEN_RESOURCES=20s`,
`core/executor/ship.go` — sleeps in `shipChange`'s own loop, after one
resource's processing is fully durably persisted and before the next
resource is even looked at; zero, and invisible, in every real `ubx ship`
that doesn't set it), polling the ledger's own live apply-record file
every two seconds until the queue showed `applied` and the policy had not
yet started:

```text
poll 9: queue_state=applied policy_started=no
WINDOW OPEN - killing now
```

`kill -9` issued at that exact instant. The process died immediately
(confirmed via `ps`). The unsealed attempt file left behind, read
directly off disk — no guessing, no reconstruction:

```json
{
  "attempt": 1,
  "resources": [
    {
      "address": {"stack": "ubi27demo", "type": "aws_sqs_queue", "name": "chain-b"},
      "transitions": [
        {"state": "pending", "at": "2026-07-17T14:24:34Z"},
        {"state": "in_flight", "at": "2026-07-17T14:24:35Z"},
        {"state": "applied", "at": "2026-07-17T14:25:01Z"}
      ],
      "provider_result": {"url": "https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi27-chain-b", "arn": "arn:aws:sqs:us-east-1:839333509514:ubx-ubi27-chain-b", "...": "..."}
    }
  ]
}
```

One resource entry, not two: `chain-b-policy` was never even reached, so
it never got a `ResourceApply` entry in this attempt at all — not "pending
with no transitions," genuinely absent, since `shipChange`'s own loop
appends a resource's entry only when it's actually that resource's turn.
No `summary`/`id` at all — unsealed, exactly what a kill at that instant
leaves behind. Verified independently: the queue existed for real
(`aws sqs list-queues`); no policy was attached yet (`get-queue-attributes
--attribute-names Policy` returned nothing).

Re-run, no delay this time — a human just noticing the process died and
re-running `ubx ship`:

```text
$ ubx ship c532743b0180a15601d50821c8e7e9774b3e8777f95105ac4c49461879f526f5 \
    --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}' --ledger-dir .
applied: ubi27demo.aws_sqs_queue.chain-b
applied: ubi27demo.aws_sqs_queue_policy.chain-b-policy
2 resource(s), 2 applied, 0 failed, 0 still unknown -- outcome: applied
```

`ubx why` shows the full, honest story across both attempts:

```text
$ ubx why c532743b0180a15601d50821c8e7e9774b3e8777f95105ac4c49461879f526f5 --ledger-dir .
apply history:
  attempt 1: unsealed (interrupted or still in progress)
    ubi27demo.aws_sqs_queue.chain-b:
      pending at 2026-07-17T14:24:34Z
      in_flight at 2026-07-17T14:24:35Z
      applied at 2026-07-17T14:25:01Z
  attempt 2: outcome=applied
    ubi27demo.aws_sqs_queue.chain-b:
      applied at 2026-07-17T14:25:41Z -- already applied in a prior attempt
    ubi27demo.aws_sqs_queue_policy.chain-b-policy:
      pending at 2026-07-17T14:25:41Z
      in_flight at 2026-07-17T14:25:42Z
      applied at 2026-07-17T14:26:09Z
```

The queue was never re-created, confirmed via AWS itself, not just via
`ubx`'s own report — `CreatedTimestamp` unchanged across the two attempts
(`1784298275`), only `LastModifiedTimestamp` moving forward (when the
policy attached). And the dependent completed using the FIRST attempt's
real, already-recorded output, not a fresh guess or a duplicate: attempt
2's own apply record shows `chain-b`'s `provider_result` empty (a
pass-through confirmation never re-captures it) while `chain-b-policy`'s
own `queue_url` is the queue's real URL anyway — recovered by
`foldResourceHistory`'s own `lastProviderResult` fold from attempt 1's
history, exactly the mechanism this design depends on, proven live:

```text
ubi27demo.aws_sqs_queue.chain-b               -> no provider_result recorded this attempt (pass-through)
ubi27demo.aws_sqs_queue_policy.chain-b-policy -> url/queue_url: https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi27-chain-b
```

## Cleanup and a real, honest gap found doing it

Destroys are out of v1 scope (docs/resolver.md's own Scope section) — both
queues were deleted via plain `aws sqs delete-queue` calls, never through
`ubx`:

```text
$ aws sqs delete-queue --queue-url https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi27-chain-a --region us-east-1
$ aws sqs delete-queue --queue-url https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi27-chain-b --region us-east-1
$ aws sqs list-queues --region us-east-1
(empty -- zero queues in the account)
```

Attempting to confirm this the way UBI-26's own report did (`ubx status
--drift`) surfaced a real, separate, load-bearing gap rather than a clean
confirmation:

```text
$ ubx status --ledger-dir . --drift --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
0 resource(s), 0 drifted, 0 unreadable
```

This is not "confirmed clean" — it's "invisible." `core.Ledger.Fleet`
(what `ubx status` walks) discovers a resource exclusively via a
`resolution.inputs[].resource` entry, and a `change` proposal's own create
never gets one for its own address (only `cross_stack_pin`/`live_state`
kinds exist today; neither fits a resource that was never observed, only
created). The real lookup key a future scan would need isn't even known
until `ship` applies it — after the proposal's content hash is already
sealed, so nothing can retroactively add to it. The account is genuinely
clean (confirmed authoritatively via `aws sqs list-queues` above, not via
`ubx`), but `ubx` itself has no way to know that, or to have known these
resources existed at all once shipped. Recorded as a real, unresolved
finding in docs/resolver.md and docs/executor.md's own "Out of scope"
sections and in STATE.md — left for a follow-up session, not silently
patched around here.

## Account state at the end of this section

Zero SQS queues in the account (`aws sqs list-queues`, no name filter).
No IAM changes. The two ledger proposals (`15f5a53...`, `c532743b...`) and
their apply-record history remain in the scratch ledger directory as a
permanent, honest record of exactly what happened — including the two
failed attempts before the Configure fix and the one interrupted attempt
before the kill-recovery proof — never rewritten or cleaned up to look
tidier than the real run was.
forward silently.
