# Plan — wedge & slices

## Changelog

- 2026-07-10 — v1 of plan, from founding design session. Wedge chosen: drift
  attribution. Executor strategy: tfplugin direct, no TF/OpenTofu/Pulumi engines.
- 2026-07-10 — Slice 1 revised from tfplugin v6-only to dual v5/v6. Real
  provider binaries (terraform-provider-aws 6.54.0, terraform-provider-time
  0.9.2) were found to serve v5 on the wire regardless of what protocol
  version a client requests — v6-only would not have worked against any
  current real provider. See docs/architecture.md — Execution layer, and
  STATE.md for the empirical finding. provider/ now exposes one
  protocol-agnostic interface backed by tfplugin5 and tfplugin6 wire
  implementations, version selected from the handshake.
- 2026-07-10 — UBI-9 session 1: M1-2's "top ~50 AWS resource types" pinned
  to an explicit, categorized list (see §M1-2 below) plus a table-driven
  conformance harness (`conformance/`) to work through it in batches. Three
  types verified end-to-end against the real account this session
  (aws_s3_bucket, aws_iam_role, aws_vpc — one per required bias category:
  storage, IAM, network); the other ~47 are registered but not yet
  implemented. See STATE.md for per-batch progress as it accumulates.
- 2026-07-10 — UBI-9 batch 2: four more types verified against the real
  account (aws_sqs_queue, aws_sns_topic, aws_iam_policy, aws_iam_user —
  all create-and-destroy-per-test-run, unlike batch 1's adopt-something-
  pre-existing pattern). aws_iam_group investigated and explicitly parked
  (no tagging API exists at all; nothing else in its schema is both
  mutable and observable) rather than forced or silently skipped — see
  §M1-2 below. 7 of 51 types implemented.
- 2026-07-10 — UBI-10: CloudTrail attribution wired into `ubx scan`'s
  drift-proposal generation. Two new intent.sources kinds (`cloudtrail`,
  `cloudtrail_unattributed`) per docs/schema.md's amendment; new
  `core/attribution.go` (EventLookup interface + AttributeDrift decision
  logic, no AWS SDK dependency) and `cloudtrail/` package (the real
  aws-sdk-go-v2 client, the only place in the codebase that imports one).
  Best-effort by construction — attribution never blocks proposal
  generation. Verified live against the real account: tagged the real
  `ubx-states` bucket, scanned, confirmed the generated drift_adopt
  proposal carried the real caller's actor ARN — see §CloudTrail
  attribution below and STATE.md for the full writeup, including a
  corrected assumption (CloudTrail's ResourceName lookup wants the
  resource's `id`, not its ARN, for the three types checked) and measured
  real delivery latency (~2-3 minutes in this account).
- 2026-07-10 — UBI-9 batch 3, closing out the milestone: all 51 types now
  resolved (48 verified, 3 parked — see §M1-2, no type left pending).
  Batches 1-2 only covered real-safe types; this batch's real addition is
  a FakeOnly conformance methodology, not just more types: every
  remaining type's real attribute schema was inspected for free (a real
  AWS provider's `GetProviderSchema`, no Configure/credentials/AWS API
  call needed) to derive schema-verified `IdentityFields` and a genuine
  mutable+observable attribute, then a new generic, env-var-driven
  `fakeprovider` mode ("conformance-v5"/"conformance-v6") serves exactly
  that attribute shape and simulates the drift with an injected mutation
  — the same adopt→mutate→scan-diff sequence RealSafe types run for
  real, driving the identical `core.RunScan`/`GenerateProposal` pipeline.
  41 types verified this way (`conformance/fake_test.go`, table-driven).
  Two more types were found to have no genuine mutable+observable field
  at all — `aws_iam_role_policy_attachment` and
  `aws_route_table_association` are pure joins whose only "change" is a
  replace, the same shape `aws_iam_group` already fought back with — so
  they join it as parked, for the same reason, discovered via free schema
  inspection rather than a live API call this time. See STATE.md's UBI-9
  closing entry for the full methodology writeup and its explicitly
  documented scope limit (FakeOnly types prove ubx's own pipeline is
  correct for that schema shape; they do NOT prove the live ReadResource
  lookup convention the way RealSafe types do — that's exactly the
  cost/risk being avoided).

## Strategy

**Wedge:** drift attribution on existing Terraform/OpenTofu repos.
Pitch: *"Your infra changed outside of code. Here's who, when — and a signed
record of what you decided about it."*
Delivery: CLI + (later) GitHub App. Zero migration required; every resolved drift
appends to a ledger, installing the proposal format as a side effect.

**Success criteria (month 6):** ~10 teams running against real prod accounts.
Thesis metric: % of surfaced drifts resolved through the signed flow —
>60% validates proposals as the unit of change; <20% falsifies cheaply.

## Foundational slices (~2-3 weeks each, end-to-end, ugly, real)

### Slice 1 — talk to one provider
- Launch AWS provider binary, tfplugin handshake (dual v5/v6, version
  negotiated from the handshake — see changelog)
- GetProviderSchema; dump one resource type's schema
- ReadResource against one real AWS resource
- Exit: attributed real-world read in a single CLI command

### Slice 2 — trust core
- Hand-written proposal JSON (no SDK/chat) → canonical hash → `ubx accept`
  (local signing) → ledger append → `ubx why` reads it back
- Exit: schema.md hashing rules ratified; first real ledger exists

### Slice 3 — close the loop (wedge skeleton)
- `ubx scan`: provider reality vs ledger → drift detected
- Drift → adoption proposal generated → accept → ledger updated → `why` explains
- Exit: the demo — point at a messy account, resolve a drift with a signed record

## Wedge buildout (months 1–6)

- **M1–2 (detection core):** top ~50 AWS resource types via ReadResource
  (done, UBI-9 — see below); CloudTrail correlation (drift → actor,
  timestamp, session; done, UBI-10 — see §CloudTrail attribution below);
  `scan` (done since Slice 3), `status --drift`. Milestone: attributed
  drift on a real messy account in <5 min.

### CloudTrail attribution (UBI-10)

`ubx scan`'s drift-proposal path now attempts CloudTrail attribution for
every `drift_adopt` proposal it generates: two new `intent.sources` kinds,
`cloudtrail` (a matched management event — event id/name/time, actor ARN,
source IP, session context) and `cloudtrail_unattributed` (attribution was
attempted and failed, with a `reason`: `no_matching_event` |
`delivery_window` | `not_logged`) — see docs/schema.md's "CloudTrail
attribution intent sources" amendment for the full field/reason
definitions.

Architecture: `core/attribution.go` defines `EventLookup` (core's own
minimal interface, mirroring `StateReader`'s inversion for the tfplugin
provider client — core still doesn't import an AWS SDK) and
`AttributeDrift`, the deterministic decision logic (which identity value to
search by, exact-match filtering, newest-first ordering, reason
classification) — all unit-tested against a fake `EventLookup`, no network
involved. The new `cloudtrail/` package is the one place in this codebase
that imports an AWS SDK directly (`aws-sdk-go-v2`), implementing
`EventLookup` against the real CloudTrail `LookupEvents` API.
`cli/attribution.go` wires the two together into `ubx scan`
(`--no-attribution` opts out); best-effort by construction, so a
CloudTrail failure of any kind never blocks a scan from producing its
proposal.

Scope, deliberately narrow per this milestone: management events via
`LookupEvents` only (CloudTrail's ~90-day default event history) — no
trail configuration, no CloudTrail Lake, no data events. Correlation
identity value: empirically, NOT the resource's ARN for the three types
checked live (`aws_s3_bucket`/`aws_iam_role`/`aws_vpc`) — CloudTrail's
`ResourceName` lookup attribute wants the resource's own `id` (bucket
name, role name, VPC id); searching by the full ARN returned nothing even
for genuinely matching events. `id` is tried first for that reason, with
`arn`/`name` as fallbacks rather than assumed to generalize to every AWS
service. See STATE.md's UBI-10 entry for the full empirical writeup,
including the real, measured CloudTrail delivery latency (~2-3 minutes in
this account, not the near-instant a first manual probe happened to see).

### M1-2 resource type list (UBI-9)

The ~50 types below are `conformance/registry.go`'s canonical source of
truth in executable form (`conformance.Registry`) — this list is the
rationale; that file is what actually runs. Biased toward what real
Terraform shops run day to day: compute, network, IAM, storage, database,
DNS, plus messaging/observability/secrets types that show up in nearly
every real account regardless of what the account is *for*.

Each type is either `real-safe` (free/negligible-cost, safe to read and
tag-mutate, conformance-tested against the actual AWS account behind
`UBX_CONFORMANCE_LIVE=1`) or `fake-only` (expensive, slow, or risky to
create/destroy just for a schema-conformance test — tested against a
fakeprovider fixture instead). Safety is a property of *testing* the type,
not of the type itself; it says nothing about whether `ubx scan` is safe to
run against one for real (reads are always safe — see docs/architecture.md,
"wedge reads and records before it ever writes").

As of UBI-9 batch 3 (closing the milestone), every `fake-only` type below is
also conformance-tested — against a `fakeprovider` fixture shaped by that
type's *real* AWS provider schema (inspected for free, no AWS API call
needed — see STATE.md's closing UBI-9 entry), not an invented one. ✓ marks
verified (real-safe types against the live account, fake-only types against
the schema-shaped fixture); ⚠ marks parked. No type is left unmarked.

**Compute** (fake-only — all hourly/slow-provisioning; fixture-verified):
`aws_instance`✓, `aws_launch_template`✓, `aws_autoscaling_group`✓,
`aws_ecs_cluster`✓, `aws_ecs_service`✓, `aws_ecs_task_definition`✓,
`aws_eks_cluster`✓, `aws_eks_node_group`✓, `aws_lambda_function`✓.

**Network** (`aws_vpc` real-safe — the account's default VPC; the rest
fixture-verified fake-only, mostly because they depend on a VPC/subnet
graph that's tedious to stand up disposably just for a conformance test):
`aws_vpc`✓, `aws_subnet`✓, `aws_route_table`✓,
`aws_route_table_association`⚠, `aws_route`✓, `aws_internet_gateway`✓,
`aws_nat_gateway`✓, `aws_eip`✓, `aws_security_group`✓,
`aws_security_group_rule`✓, `aws_lb`✓, `aws_lb_target_group`✓,
`aws_lb_listener`✓, `aws_vpc_endpoint`✓.

**IAM** (`aws_iam_role`/`aws_iam_policy`/`aws_iam_user` real-safe — the
first adopts the account's real `aws-codestar-service-role`, the other two
are created and destroyed per test run, all free; `aws_iam_group` and
`aws_iam_role_policy_attachment` are *parked*; the rest fixture-verified
fake-only):
`aws_iam_role`✓, `aws_iam_policy`✓, `aws_iam_role_policy_attachment`⚠,
`aws_iam_user`✓, `aws_iam_group`⚠, `aws_iam_instance_profile`✓,
`aws_iam_openid_connect_provider`✓.

**Storage** (`aws_s3_bucket` real-safe — the account's real `ubx-states`
bucket, proven since UBI-7; the rest fixture-verified fake-only):
`aws_s3_bucket`✓, `aws_s3_bucket_policy`✓, `aws_s3_bucket_versioning`✓,
`aws_s3_bucket_public_access_block`✓, `aws_ebs_volume`✓,
`aws_efs_file_system`✓.

**Database** (all fixture-verified fake-only — hourly-billed, slow to
provision for real):
`aws_db_instance`✓, `aws_db_subnet_group`✓, `aws_rds_cluster`✓,
`aws_elasticache_cluster`✓, `aws_dynamodb_table`✓.

**DNS / CDN / certs** (all fixture-verified fake-only — no hosted zone
exists in the test account, and creating one solely for this suite would
add a real recurring charge; revisit if a zone exists for another reason):
`aws_route53_zone`✓, `aws_route53_record`✓, `aws_cloudfront_distribution`✓,
`aws_acm_certificate`✓.

**Messaging / observability / secrets** (`aws_sqs_queue`/`aws_sns_topic`
real-safe — created and destroyed per test run, free/negligible-cost; the
rest fixture-verified fake-only):
`aws_sqs_queue`✓, `aws_sns_topic`✓, `aws_cloudwatch_log_group`✓,
`aws_cloudwatch_metric_alarm`✓, `aws_secretsmanager_secret`✓,
`aws_kms_key`✓.

51 types total, all resolved as of UBI-9 batch 3 (no type left pending, per
UBI-9's own completion criterion — `conformance/registry_test.go`'s
`TestRegistry_NoThirdState` enforces this going forward): 48 implemented
(✓ — 7 real-safe against the live account, 41 fake-only against
schema-shaped `fakeprovider` fixtures) and 3 explicitly **parked** (⚠)
rather than silently skipped:

- `aws_iam_group`: no tagging API exists at all (confirmed empirically —
  there is no `aws iam tag-group`) and no other schema field is both
  mutable and observable.
- `aws_iam_role_policy_attachment`: its real schema is exactly
  `{id, policy_arn (required), role (required)}` — a pure join with
  nothing optional besides `id`; "changing" which policy is attached is a
  replace in AWS's own model, not an in-place modify.
- `aws_route_table_association`: its real schema is
  `{gateway_id, id, region, route_table_id (required), subnet_id}` — same
  join-resource shape, same replace-not-modify reasoning.

All three are documented in `conformance/registry.go`'s `Notes`. This is the
"types that fight back get documented + parked, not hacked" case UBI-9 was
scoped to expect — the last two were found via free schema inspection
rather than a live API call, but the reasoning is the same.
- **M3–4 (decision loop):** adopt/revert proposals signed via PR-merge or CLI;
  adopt writes corrected attributes back to existing .tf files (narrow-scope
  bidirectionality); revert emits plan — apply via the team's own tooling at this
  stage (executor trust comes later). GitHub App surfaces drift as issue/PR with
  receipt.
- **M5–6 (retention layer):** `why` over drift history, Slack notifications,
  policy stubs (auto-adopt sandbox / require-approval prod).

## Deferred (explicitly not now)

SDK + codegen, chat/intent provider, diagrams, markdown intents, full executor
(ApplyResourceChange path), policies beyond stubs, environments/promotion,
Nexus SaaS, naming of proposal ledger format for external publication.

## Risks being managed

- Category creation cost → wedge is findable pain ("terraform drift"), not a new
  concept to explain.
- Solo-founder scope → slices are small, sellable, compounding; giants ignore
  narrow wedges.
- Executor trust → deferred; wedge reads and records before it ever writes.
  Adversarial reliability testing becomes the credibility engine when the
  executor lands (publish results, Jepsen-style).
