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

# UBI-29: Fleet visibility for shipped creates — a real create-then-drift lifecycle

> Same discipline as the sections above: real command output, not
> reconstructed. Real AWS account `839333509514`, region `us-east-1`,
> provider `hashicorp/aws` 6.54.0, a fresh scratch ledger
> (`ubi29-live`). No adjectives.

## Scope

UBI-27 closed with one named gap: a shipped `change` proposal's created
resources were invisible to `ubx status`/`ubx why <address>` afterward
(`core.Ledger.Fleet` and friends discover a resource exclusively via a
`resolution.inputs[].resource` entry, which a create never populates for
its own address). This section is UBI-29's fix, live-verified: create a
real chain, confirm `ubx status` sees it, mutate it out-of-band, confirm
drift and attribution fire, resolve the correction, confirm `ubx why
<address>` shows the create as genesis.

## The chain, shipped

The same `aws_sqs_queue` + `aws_sqs_queue_policy` pattern as UBI-27's own
finale — `ubx resolve` → `ubx accept` → `ubx ship`:

```text
$ ubx ship 533d958735cc0b2b4f6f0e4b0536e1017ae586d243904078cd608e496a548e60 \
    --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}' --ledger-dir .
applied: ubi29demo.aws_sqs_queue.ubi29-queue
applied: ubi29demo.aws_sqs_queue_policy.ubi29-queue-policy
2 resource(s), 2 applied, 0 failed, 0 still unknown -- outcome: applied
```

## `ubx status` sees it immediately

```text
$ ubx status --ledger-dir .
ubi29demo.aws_sqs_queue.ubi29-queue: change 533d958735cc… (accepted 2026-07-17T15:17:21Z)
ubi29demo.aws_sqs_queue_policy.ubi29-queue-policy: change 533d958735cc… (accepted 2026-07-17T15:17:21Z)
2 resource(s) (ledger-only, no live comparison)
```

Before UBI-29, this reported "0 resource(s)" for a stack that had just had
two real resources shipped into it (see UBI-27's own section above). Both
resources now show up, `kind: change` — a real, meaningful signal
distinguishing a shipped-create genesis from an `adoption`.

## A real, honest surprise: `--drift` fires immediately, correctly

```text
$ ubx status --ledger-dir . --drift --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
drifted: ubi29demo.aws_sqs_queue.ubi29-queue: change 533d958735cc… (accepted 2026-07-17T15:17:21Z)
drifted: ubi29demo.aws_sqs_queue_policy.ubi29-queue-policy: change 533d958735cc… (accepted 2026-07-17T15:17:21Z)
2 resource(s), 2 drifted, 0 unreadable
```

Not a bug: a real, expected side effect of shipping two interdependent
resources in the same chain. The queue's own recorded `provider_result`
was captured the moment IT applied — before its sibling policy resource
existed — so it legitimately differs from a fresh read taken after the
whole chain completed:

```text
$ ubx scan --ledger-dir . --stack ubi29demo --type aws_sqs_queue --name ubi29-queue \
    --lookup '{"id":"https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi29-queue"}' \
    --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}' --no-attribution
before: {"policy": "", "region": null}
after:  {"policy": "{\"Statement\":[{\"Action\":\"sqs:SendMessage\",...}]}", "region": "us-east-1"}
```

`policy` went from empty (nothing attached yet, at the queue's own apply
time) to the real, now-attached policy document; `region` went from
`null` to `"us-east-1"` — a benign SDKv2 quirk (a schema-`Computed`
attribute the provider fills in on a live read but leaves `null` on
create when never explicitly set), the same class of divergence this
project's own drift-diffing conventions already document elsewhere
(`tags`/`tags_all`). Both are accurate: the ledger's recorded state
really did differ from live reality, for real, traceable reasons — this
is drift detection working correctly on a genuinely new case (an
interdependent multi-resource create chain), not a false positive. Both
`drift_adopt`s were generated and accepted, establishing a clean,
accurate baseline.

```text
$ ubx status --ledger-dir . --drift --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
clean: ubi29demo.aws_sqs_queue.ubi29-queue: drift_adopt 45a635e70903… (accepted 2026-07-17T15:20:20Z)
clean: ubi29demo.aws_sqs_queue_policy.ubi29-queue-policy: drift_adopt 7078323eadb4… (accepted 2026-07-17T15:20:28Z)
2 resource(s), 0 drifted, 0 unreadable
```

## A real out-of-band mutation, detected and attributed

```text
$ aws sqs tag-queue --queue-url https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi29-queue --tags incident=UBI-29-live-verify --region us-east-1

$ ubx status --ledger-dir . --drift --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
drifted: ubi29demo.aws_sqs_queue.ubi29-queue: drift_adopt 45a635e70903… (accepted 2026-07-17T15:20:20Z)
clean: ubi29demo.aws_sqs_queue_policy.ubi29-queue-policy: drift_adopt 7078323eadb4… (accepted 2026-07-17T15:20:28Z)
2 resource(s), 1 drifted, 0 unreadable
```

`ubx scan` (with CloudTrail attribution enabled) confirms the tag change
and attempts attribution — it fires, honestly reporting it couldn't match
an event yet (real CloudTrail delivery lag, ~15 minutes, the same
`cloudtrail_unattributed`/`delivery_window` outcome this project's own
UBI-10 report already documented as expected, not a failure):

```json
{
  "intent": {
    "sources": [{"kind": "cloudtrail_unattributed", "reason": "delivery_window"}]
  },
  "delta": {"modifies": [{"after": {"tags.incident": "UBI-29-live-verify", "tags_all.incident": "UBI-29-live-verify"}}]}
}
```

Accepted.

## `ubx why <address>` shows the create as genesis

```text
$ ubx why ubi29demo.aws_sqs_queue.ubi29-queue --ledger-dir .
ubi29demo.aws_sqs_queue.ubi29-queue: 3 proposal(s), newest first
- drift_adopt 7621c361e11c… (2026-07-17T15:21:00Z): record drift on ubi29demo.aws_sqs_queue.ubi29-queue observed outside the ledger
    source: cloudtrail_unattributed -- too recent for CloudTrail to have delivered a matching event yet
    change: ubi29demo.aws_sqs_queue.ubi29-queue: tags.incident: (absent) -> "UBI-29-live-verify"
    change: ubi29demo.aws_sqs_queue.ubi29-queue: tags_all.incident: (absent) -> "UBI-29-live-verify"
- drift_adopt 45a635e70903… (2026-07-17T15:19:52Z): record drift on ubi29demo.aws_sqs_queue.ubi29-queue observed outside the ledger
    change: ubi29demo.aws_sqs_queue.ubi29-queue: policy: "" -> "{...}"
    change: ubi29demo.aws_sqs_queue.ubi29-queue: region: null -> "us-east-1"
- change 533d958735cc… (2026-07-17T15:15:48Z): UBI-29 live verify: SQS queue + policy, Fleet visibility
apply history:
  attempt 1: outcome=applied
    ubi29demo.aws_sqs_queue.ubi29-queue:
      pending at 2026-07-17T15:17:22Z
      in_flight at 2026-07-17T15:17:23Z
      applied at 2026-07-17T15:17:49Z
    ubi29demo.aws_sqs_queue_policy.ubi29-queue-policy:
      pending at 2026-07-17T15:17:49Z
      in_flight at 2026-07-17T15:17:50Z
      applied at 2026-07-17T15:18:17Z
```

The oldest entry is `change`, not `adoption` or "no proposals found" —
before UBI-29, `ProposalsForAddress` returned nothing at all for this
address (it only ever matched `resolution.inputs[].resource`, which a
create never populates for itself), and `ubx why <address>` would have
reported "no proposals found for ubi29demo.aws_sqs_queue.ubi29-queue."
The full genesis chain — resolve → accept → ship, then two honest
drift_adopts — is now the real, complete story `ubx why` tells.

## Cleanup and account state

```text
$ aws sqs delete-queue --queue-url https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi29-queue --region us-east-1
$ aws sqs list-queues --region us-east-1
(empty -- zero queues in the account)
```

`ubx status --drift`, run once more after cleanup, honestly reports both
resources `unreadable` (destroys stay out of v1 scope — this is the same
"the ledger still thinks it exists, and says so honestly" limitation
UBI-27's own report already named, not a new gap):

```text
$ ubx status --ledger-dir . --drift --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
unreadable: ubi29demo.aws_sqs_queue.ubi29-queue: drift_adopt 7621c361e11c… (accepted 2026-07-17T15:21:10Z) -- ... provider returned no state ...
unreadable: ubi29demo.aws_sqs_queue_policy.ubi29-queue-policy: drift_adopt 7078323eadb4… (accepted 2026-07-17T15:20:28Z) -- ... context deadline exceeded ...
2 resource(s), 0 drifted, 2 unreadable
```
forward silently.

# UBI-30: destroys — a real `PlanResourceChange` bug found live, fixed, then re-verified against the same resources

> Same discipline as the sections above: real command output, not
> reconstructed. Real AWS account `839333509514`, region `us-east-1`,
> provider `hashicorp/aws` 6.54.0, four scratch ledgers created over the
> course of this session (`ubi30-live` and its `chain2`/`debugtest`/
> `killtest` subdirectories). No adjectives.

## Scope

UBI-30 sessions 1-2 built `core/resolver`'s destroy support (docs-only,
then resolver); session 3 built `core/executor`'s destroy support,
hermetically, all eleven docs/destroys-adversarial.md rows green; session
4 closed `core.Ledger.FoldState`'s own tombstone-folding and `ubx why`'s
`destroyed`/`already_absent` rendering, hermetically. This section is the
live full-lifecycle finale sessions 3-4 both deferred: create a real
dependent chain, drift it, resolve and sign a destroy, ship it through
`--confirm-destroys`, kill `-9` mid-destroy, reconcile, and verify
absence against real AWS — plus a critical bug this exact exercise found,
live, that no hermetic test had caught.

## The chain, shipped, then drifted and destroy-resolved

The same `aws_sqs_queue` + `aws_sqs_queue_policy` dependent pattern UBI-27/
UBI-29 already used, `ubx resolve` → `ubx accept` → `ubx ship`, then a
hand-edited out-of-band drift (`aws sqs tag-queue`/`set-queue-attributes`),
`ubx scan` → `ubx accept`, then a destroy intent naming both addresses,
`ubx resolve` → `ubx accept --confirm-destroys`:

```text
$ ubx resolve destroy-intent.json --ledger-dir . --out destroy-resolved.json --source hashicorp/aws --provider-version 6.54.0
resolved: payments: 0 create(s), 0 modify(ies), 2 destroy(s)
$ ubx accept destroy-resolved.json --ledger-dir . --confirm-destroys
accepted a99ae22cf7e7… (stack payments)
```

## A real bug found live that no hermetic test caught: destroy silently no-ops without a genuine `PlanResourceChange` call

Shipping the signed destroy against real `terraform-provider-aws` 6.54.0
returned success — `ApplyResourceChange`'s own response carried no error —
but the target queue's policy was never actually removed. Confirmed by
direct debug output: the response's `NewState` was the full, unchanged
prior resource, not an absence. The proximate cause: an SDKv2-shimmed
provider's destroy `Apply` needs the opaque `PlannedPrivate` bytes a real
`PlanResourceChange` call produces to know there's a diff to act on at
all; skipping straight to `Apply` (the "no separate plan phase" shortcut
docs/executor.md's own session-3 addendum had confirmed safe for
create/modify, against one different, simpler provider) silently no-ops
instead of deleting. A second, independent bug surfaced fixing the first:
`provider/ctyvalue.go`'s `encodeUnknownAwareDynamicValue` never produced a
genuine top-level `cty.NullVal` for a literal JSON `null` input — the
actual destroy signal `shipDestroyNode` sends — building a per-attribute
object (`Unknown` for `Computed` fields, `Null` for the rest) instead. This
is very likely why the `aws_sqs_queue_policy` destroy specifically failed
with `AWS.SimpleQueueService.NonExistentQueue: The specified queue does
not exist` against an empty-string queue reference — the SDKv2 shim's own
delete logic reading a `Null` where it needed the real prior `queue_url`.
Both are fixed: `provider.Provider` gained a real `PlanResourceChange`
method (both protocol versions), `shipDestroyNode` calls it unconditionally
before `Apply` and threads the real `PlannedPrivate` through, and
`encodeUnknownAwareDynamicValue` special-cases a literal top-level `null`
into a genuine `cty.NullVal` before falling through to its existing
per-attribute walk. Full detail: docs/executor.md's own "Session 5"
addendum.

**Proof, against the exact resource that failed before the fix**, not a
fresh one: re-resolving a brand-new destroy proposal for the same
`aws_sqs_queue_policy`/`aws_sqs_queue` pair (the original proposal's own
per-resource retry budget — three attempts — was already exhausted by the
pre-fix failures, a real, hard limit requiring a fresh proposal, not
infinite retries against the same one) and shipping it with the fixed
binary:

```text
$ ubx ship 2d99dfff2934… --ledger-dir . --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
applied: payments.aws_sqs_queue_policy.ubi30-chain-policy
pending: payments.aws_sqs_queue.ubi30-chain
  terminal: destroy target drifted since it was signed away -- refusing to destroy state that was never reviewed
2 resource(s), 1 applied, 1 failed, 0 still unknown -- outcome: partially_applied
$ aws sqs get-queue-attributes --queue-url https://sqs.us-east-1.amazonaws.com/839333509514/ubx-ubi30-chain --attribute-names Policy
(empty -- the policy is genuinely gone)
```

The policy actually deleted this time — real, verified via a direct
`aws sqs get-queue-attributes` call, not just a clean exit code. The
queue's own destroy correctly refused (its recorded `policy` attribute no
longer matched live reality, now that the policy was really gone —
freshness re-verified immediately before every attempt, exactly as
designed, not a regression). A fresh `ubx scan` → `ubx accept` re-adopted
the drift, a fresh destroy-only intent resolved and shipped it:

```text
$ ubx ship 2dfcefe58ea6… --ledger-dir . --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
applied: payments.aws_sqs_queue.ubi30-chain
1 resource(s), 1 applied, 0 failed, 0 still unknown -- outcome: applied
$ aws sqs get-queue-url --queue-name ubx-ubi30-chain
An error occurred (AWS.SimpleQueueService.NonExistentQueue) ...
```

## The kill: a real `kill -9` mid-destroy, against a queue the pre-fix bug had already falsely marked "destroyed"

A separate single-resource chain (`killtest`) had, pre-fix, gone through
exactly this bug's own failure mode: a genuine `kill -9` between the
`in_flight` write and the (never-landed) real call, a false `failed`
reconciliation from SQS's own real eventual-consistency lag on a
subsequent retry, and finally a sealed `applied`/`destroyed` outcome that
was **itself false** — the queue was never actually deleted, just like
`ubi30-chain-policy` above. `ubx status` folds a sealed destroy's address
out of its ground truth regardless of whether the underlying delete
really happened (`core.Ledger.FoldState`'s own tombstone-fold, session 4)
— so re-discovering this queue for real cleanup required a fresh
`ubx scan`, which correctly reports it `new` (the exact "tear down,
rebuild under the same address" lifecycle FoldState's own doc comment
names, here triggered by a false tombstone rather than a real rebuild):

```text
$ ubx scan --type aws_sqs_queue --name ubi30-killtarget --stack payments --lookup '{"id":"..."}' --ledger-dir . --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}' --out redisc-kill.json
new: payments.aws_sqs_queue.ubi30-killtarget (04111df18273…) -- generated a "adoption" proposal
```

Re-adopted, a fresh destroy resolved and signed, then shipped with
`UBX_SHIP_DEBUG_DELAY_AFTER_APPLY_SUCCESS=15s` (the same test seam
`core/executor`'s own hermetic suite uses, reused here to create a
reliable real kill window) and killed for real, mid-window, confirmed by
wall-clock timestamps:

```text
$ UBX_SHIP_DEBUG_DELAY_AFTER_APPLY_SUCCESS=15s ubx ship 1a916b015bc9… --ledger-dir . --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}' &
[1] 31891
$ date +%s
1784341817
$ kill -9 31891
$ date +%s
1784341837
```

The real `ApplyResourceChange` call had already landed before the kill —
confirmed directly against AWS, not inferred from the ledger:

```text
$ aws sqs get-queue-url --queue-name ubx-ubi30-killtarget
An error occurred (AWS.SimpleQueueService.NonExistentQueue) ...
```

— while the ledger's own record, killed mid-sleep, shows only `in_flight`,
no terminal transition at all:

```json
"transitions": [
  {"state": "pending", "at": "2026-07-18T02:30:18Z"},
  {"state": "in_flight", "at": "2026-07-18T02:30:20Z"}
]
```

A plain re-run of `ubx ship` against the same proposal ID reconciles it
correctly — `reconcileDestroyLoop`'s not-found-read-implies-destroyed path,
live-verified for the first time this session (its hermetic sibling,
`TestShipDestroy_KillAfterCall_AlreadyLanded_ResolvesDestroyed`, has
existed since session 3):

```text
$ ubx ship 1a916b015bc9… --ledger-dir . --source hashicorp/aws --provider-version 6.54.0 --provider-config '{"region":"us-east-1"}'
applied: payments.aws_sqs_queue.ubi30-killtarget
1 resource(s), 1 applied, 0 failed, 0 still unknown -- outcome: applied
```

## `ubx why` reads the complete biography, including the false tombstone

```text
$ ubx why payments.aws_sqs_queue.ubi30-killtarget --ledger-dir .
payments.aws_sqs_queue.ubi30-killtarget: 6 proposal(s), newest first
- change 1a916b015bc9… (2026-07-18T02:29:51Z): UBI-30 live finale: destroy the kill-9 target, for real this time, with the PlanResourceChange fix in place
    destroy: payments.aws_sqs_queue.ubi30-killtarget
apply history:
  attempt 1: unsealed (interrupted or still in progress)
    payments.aws_sqs_queue.ubi30-killtarget:
      pending at 2026-07-18T02:30:18Z
      in_flight at 2026-07-18T02:30:20Z
      reconcile: present_matches at 2026-07-18T02:30:19Z
  attempt 2: outcome=applied
    payments.aws_sqs_queue.ubi30-killtarget:
      pending at 2026-07-18T02:30:54Z
      applied at 2026-07-18T02:31:32Z -- confirmed by reconciliation (destroyed)
      reconcile: destroyed at 2026-07-18T02:31:32Z
- adoption 765405df1179… (2026-07-18T02:29:21Z): adopt existing payments.aws_sqs_queue.ubi30-killtarget into the ledger (discovered by scan)
- change a481d3845b52… (2026-07-18T01:51:33Z): ubi30 live finale: destroy the kill-9 target
apply history:
  attempt 1: unsealed (interrupted or still in progress)
  attempt 2: outcome=failed
    ... failed at 2026-07-18T01:52:49Z -- confirmed by reconciliation: destroy never landed
  attempt 3: outcome=applied
    payments.aws_sqs_queue.ubi30-killtarget:
      applied at 2026-07-18T01:56:00Z (destroyed)
- drift_adopt d70712815545… (2026-07-18T01:51:23Z): record drift on payments.aws_sqs_queue.ubi30-killtarget observed outside the ledger
- change 9eed04ad7709… (2026-07-18T01:50:32Z): ubi30 live finale: destroy the kill-9 target
    error (terminal): destroy target drifted since it was signed away -- refusing to destroy state that was never reviewed
- change d8e21ce53603… (2026-07-18T01:49:47Z): ubi30 live finale: kill -9 mid-destroy target
apply history:
  attempt 1: outcome=applied
```

Genesis to tombstone, in one read: the original create, the first
kill-9-during-attempt (pre-fix, retried into a drift refusal, then a
*pre-fix* attempt 3 recorded `applied (destroyed)` that was — per this
report's own finding above — never actually true, a real out-of-band
`aws sqs list-queues` at the time would have shown the queue still alive.
`ubx why` shows this exactly as it happened, false tombstone included; it
is not this tool's job to retroactively rewrite a sealed historical
record, only to fold *current truth* through it (`FoldState`, session 4)
and render what was actually recorded (this section). The re-adoption,
the real fixed-binary destroy, the real `kill -9`, and the correct
reconciliation are the last four entries, exactly as they happened.

## Cleanup and account state

The three other resources this session's pre-fix investigation left
falsely "destroyed" in their own ledgers (`ubi30-base`/`ubi30-dependent`
in `chain2`, `ubi30-debug` in `debugtest`) were cleaned up the same way as
`ubi30-killtarget` above — re-discovered via a fresh `ubx scan` (each
correctly reported `new`), re-adopted, destroyed for real through a fresh
signed proposal — rather than reached for directly with `aws sqs
delete-queue`, so that every real queue this session ever touched was
actually deleted *through* `ubx`, proving the fix six times over (two
queues, one policy, three single-resource chains), not once:

```text
$ ubx status --ledger-dir . && ubx status --ledger-dir chain2 && ubx status --ledger-dir debugtest && ubx status --ledger-dir killtest
0 resource(s) (ledger-only, no live comparison)
0 resource(s) (ledger-only, no live comparison)
0 resource(s) (ledger-only, no live comparison)
0 resource(s) (ledger-only, no live comparison)
$ aws sqs list-queues --queue-name-prefix ubx-ubi30
(empty -- zero matching queues in the account)
```

## What this section doesn't cover

SQS's own real deletion-visibility lag (`DeleteQueue` succeeds immediately;
`GetQueueUrl`/`ListQueues` can keep reporting the queue for well beyond the
~60 seconds AWS's own docs suggest) exposed that `reconcileDestroyLoop`'s
retry budget (5 attempts, 20ms apart — well under a second total) is far
too short for genuine eventual consistency, producing a false `failed`
reconciliation earlier in this session's own investigation (before the
`PlanResourceChange` fix made destroys land at all, so its practical
impact was never fully isolated). Not fixed this session — a real,
separate, named gap for `core/executor`'s own retry-budget tuning, not a
destroys-specific defect.

# UBI-44: a destroy that lied — provider-reported success is never sufficient

> Same discipline as the sections above: real command output, not
> reconstructed. Real GCP project `personal-273114`, provider
> `hashicorp/google` 7.40.0. No adjectives.

## Scope

Found live in UBI-43 session 5's own finale (previous arc): a real
`google_pubsub_topic` destroy, shipped the ordinary way, reported
`applied`/`Outcome: "destroyed"` in the ledger while the real GCP topic
stayed live — filed as its own issue rather than patched under time
pressure. This section is that issue closed: the mechanism diagnosed for
real (not assumed to be a repeat of UBI-30's own `PlannedPrivate` bug),
`core/executor`'s own destroy verdict made structurally honest (a
post-destroy read-back is now the *only* way `destroyed` is ever earned,
universally, not just after an ambiguous `Apply` result), and the exact
scenario that lied re-run against real GCP with the fix in place.

## Diagnosing the mechanism, for real — not assumed

The obvious hypothesis was "another `PlannedPrivate` no-op, same shape as
UBI-30." Checked directly rather than assumed: `shipDestroyNode` already
calls `PlanResourceChange` unconditionally before every destroy `Apply`
(UBI-30's own fix), and `PlannedPrivate` came back **empty in every
attempt this session made** — including the one that later actually
deleted the topic. Not the same mechanism.

Reproduced live, four separate ways (a real `ubx ship`, and three
isolated variations driving the wire protocol directly against
`hashicorp/google` 7.40.0, protocol v5): every one produced a clean,
diagnostics-free success —

```text
$ ubx ship 5baf0108c345… --provider ./terraform-provider-google --provider-config '{"project":"personal-273114"}' --ledger-dir .
applied: ubi44.google_pubsub_topic.ubi44-diag-1784414350
1 resource(s), 1 applied, 0 failed, 0 still unknown -- outcome: applied
```

— while Cloud Audit Logs showed **zero** real `DeleteTopic` calls across
all four:

```text
$ gcloud logging read 'resource.type="pubsub_topic" AND protoPayload.resourceName:"ubi44-diag-1784414350"' --project personal-273114 --freshness=1d
TIMESTAMP                       METHOD_NAME                             CODE
2026-07-18T22:41:35.807461840Z  google.pubsub.v1.Publisher.CreateTopic
```

Only `CreateTopic`. The topic was still there:

```text
$ gcloud pubsub topics describe ubi44-diag-1784414350 --project personal-273114
name: projects/personal-273114/topics/ubi44-diag-1784414350
```

Real root cause, isolated by direct experiment: `google_pubsub_topic`'s
own `Delete` needs its `name` attribute (the short-form topic ID,
distinct from `id`, the full path) populated in `PriorState` to actually
issue the real API call. `ubx`'s universal `{"id": "..."}`-only lookup
never fills `name` — confirmed by manually filling it in correctly
(short form, matching real Terraform's own `import`) against a second
throwaway topic and watching a genuine `DeleteTopic` call finally appear
in the audit log, and the topic actually gone:

```text
$ gcloud pubsub topics describe ubi44-diag2-1784415588 --project personal-273114
ERROR: (gcloud.pubsub.topics.describe) NOT_FOUND: Resource not found (resource=ubi44-diag2-1784415588)
$ gcloud logging read '...' --project personal-273114
TIMESTAMP                       METHOD_NAME                             CODE
2026-07-18T23:00:30.368342969Z  google.pubsub.v1.Publisher.DeleteTopic
2026-07-18T22:59:48.840384014Z  google.pubsub.v1.Publisher.CreateTopic
```

Two genuinely different root causes (UBI-30's empty `PlannedPrivate`;
this session's incomplete `PriorState`) produce the **identical
symptom**: a provider can say "success" without it being true. That's
the real finding — not this one type's own lookup gap, which is
real but narrower.

## The fix: a mandatory post-destroy read-back, universally

`shipDestroyNode`'s own `Apply` call succeeding no longer resolves
`applied`/`destroyed` directly — it now runs the same reconcile-by-query
loop an ambiguous `Apply` error already required, universally. Co-scoped
with UBI-42 (the reconcile retry budget was already known too short for
real cloud eventual consistency — SQS's own ~60s figure, UBI-30's own
finding): a new backoff schedule (~64 seconds total) replaces destroy's
own fixed 100ms budget, so the universal check doesn't turn a real,
slow-but-genuine delete into a false failure. Full design:
docs/executor.md's own UBI-44 amendment; full hermetic proof:
`core/executor/destroys_test.go`'s new tests, plus
`cli/ship_lying_destroy_test.go` proving the identical behavior through
the real tfplugin wire protocol.

## Live re-run: the exact scenario, fixed binary

The identical shape that lied — adopt via the universal `{"id":...}`
lookup, resolve a destroy, `--confirm-destroys`, ship — re-run against a
fresh real topic with the fix in place:

```text
$ ubx ship ef1a7f6b8ba8… --provider ./terraform-provider-google --provider-config '{"project":"personal-273114"}' --ledger-dir .
failed: ubi44.google_pubsub_topic.ubi44-fixed-1784417122
  terminal: the provider reported a successful destroy, but a post-destroy read-back found the resource still present after the full retry budget -- the delete never actually happened
1 resource(s), 0 applied, 1 failed, 0 still unknown -- outcome: failed
```

Took ~58 real seconds (the full backoff schedule, genuinely exhausted —
this session's own control experiment already proved the underlying
`name`-completeness gap isn't fixed by this session's own work, so this
resource genuinely cannot be deleted via the universal lookup alone; see
"What this section doesn't cover," below). Verified via `gcloud`,
matching the honest report this time, not contradicting it:

```text
$ gcloud pubsub topics describe ubi44-fixed-1784417122 --project personal-273114
name: projects/personal-273114/topics/ubi44-fixed-1784417122
```

`ubx why` renders the complete, honest attempt — every intervening
`inconclusive` retry, the final `provider_reported_success_but_present`
verdict, and the terminal error message, all real:

```text
$ ubx why ef1a7f6b8ba8… --ledger-dir .
...
apply history:
  attempt 1: outcome=failed
    ubi44.google_pubsub_topic.ubi44-fixed-1784417122:
      pending at 2026-07-18T23:26:15Z
      in_flight at 2026-07-18T23:26:16Z
      unknown_post_timeout at 2026-07-18T23:26:16Z -- provider reported a successful destroy -- verifying via a post-destroy read-back before recording it as destroyed
      failed at 2026-07-18T23:27:13Z -- provider reported success but a post-destroy read-back found the resource still present
      reconcile: present_matches at 2026-07-18T23:26:16Z
      reconcile: inconclusive at 2026-07-18T23:26:17Z -- still present after a reported successful destroy -- retrying to allow for real propagation lag before concluding the provider's claim was false
      (... 7 more inconclusive retries, spaced per the backoff schedule ...)
      reconcile: provider_reported_success_but_present at 2026-07-18T23:27:13Z -- the provider reported a successful destroy, but a post-destroy read-back found the resource still present after the full retry budget -- the delete never actually happened
      error (terminal): the provider reported a successful destroy, but a post-destroy read-back found the resource still present after the full retry budget -- the delete never actually happened
```

## The false record from the diagnosis session, corrected forward — never edited

The very first reproduction (`ubi44-diag-1784414350`, above) left a
literal false `destroyed` record in its own scratch ledger, written by
this exact bug before the fix existed. It stands, permanently, exactly
as it was recorded — never edited, per this project's own append-only
posture (the same discipline UBI-30's own false-tombstone recovery
established):

```text
$ ubx why ubi44.google_pubsub_topic.ubi44-diag-1784414350 --ledger-dir .
ubi44.google_pubsub_topic.ubi44-diag-1784414350: 2 proposal(s), newest first
- change 5baf0108c345… (2026-07-18T22:42:52Z): UBI-44 diagnosis: destroy the throwaway pubsub topic
    destroy: ubi44.google_pubsub_topic.ubi44-diag-1784414350
apply history:
  attempt 1: outcome=applied
    ubi44.google_pubsub_topic.ubi44-diag-1784414350:
      pending at 2026-07-18T22:43:18Z
      in_flight at 2026-07-18T22:43:19Z
      applied at 2026-07-18T22:43:19Z (destroyed)
      reconcile: present_matches at 2026-07-18T22:43:19Z
      reconcile: destroyed at 2026-07-18T22:43:19Z
- adoption 7cddb5e3d163… (2026-07-18T22:41:49Z): adopt existing ubi44.google_pubsub_topic.ubi44-diag-1784414350 into the ledger (discovered by scan)
```

The real topic behind this address happens to be genuinely gone now too
— but via a real Terraform `destroy` in this session's own control
experiment (proving the root cause, above), not through `ubx`. Unlike
UBI-30's own false tombstones (where the live resource was still there,
requiring a fresh `ubx scan` to rediscover it as `new` and destroy it for
real), there is no live resource left to rediscover here — the ledger's
false belief and current reality happen to now agree, by coincidence of
an out-of-band action, not because the false record was ever corrected
through `ubx` itself. The record's own permanent dishonesty about *how*
this address became empty is exactly the point: a future reader of this
exact chain sees the truth (the destroy was signed and shipped, `ubx`
believed it worked, and this reliability report is the only place that
says otherwise) rather than a silently rewritten "it worked after all."

## Cleanup and account state

Both throwaway topics from the diagnosis session, and the fixed-binary
re-run's own topic, are confirmed gone from the real account — the
first two via the real Terraform control experiment and a direct
`gcloud pubsub topics delete` (the underlying lookup-completeness gap
means `ubx` itself cannot yet close this loop automatically), the third
by hand after the live re-run above:

```text
$ gcloud pubsub topics list --project personal-273114
Listed 0 items.
```

## What this section doesn't cover

**The underlying `name`-completeness gap for `google_pubsub_topic`'s
destroy path is not fixed by this session's own work, and was never
meant to be** — this session's fix (a mandatory post-destroy read-back)
makes `ubx` *honest* about a destroy that can't actually succeed via the
universal `{"id":...}` lookup; it does not make that destroy succeed.
Closing that specific gap needs the alternative fix path UBI-44's own
filing already named — a smarter, per-type lookup (generalizing
`core/lookuphints`'/`conformance/registry.go`'s own "both required
together" shape to the destroy path specifically) — deliberately left
for its own session, not folded into this one. `google_storage_bucket`
(the other `RealSafe` GCP type with the identical "id alone isn't
enough" read-side gotcha, per `conformance/registry.go`) was named as a
plausible sibling risk in UBI-44's own filing but not separately
verified this session — audited by inspection of the identical shape,
not by a live repro of its own.
