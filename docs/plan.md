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

- **M1–2 (detection core):** top ~50 AWS resource types via ReadResource;
  CloudTrail correlation (drift → actor, timestamp, session); `scan`,
  `status --drift`. Milestone: attributed drift on a real messy account in <5 min.

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

**Compute** (fake-only — all hourly/slow-provisioning):
`aws_instance`, `aws_launch_template`, `aws_autoscaling_group`,
`aws_ecs_cluster`, `aws_ecs_service`, `aws_ecs_task_definition`,
`aws_eks_cluster`, `aws_eks_node_group`, `aws_lambda_function`.

**Network** (`aws_vpc` real-safe — the account's default VPC; the rest
fake-only, mostly because they depend on a VPC/subnet graph that's tedious
to stand up disposably just for a conformance test):
`aws_vpc`✓, `aws_subnet`, `aws_route_table`, `aws_route_table_association`,
`aws_route`, `aws_internet_gateway`, `aws_nat_gateway`, `aws_eip`,
`aws_security_group`, `aws_security_group_rule`, `aws_lb`,
`aws_lb_target_group`, `aws_lb_listener`, `aws_vpc_endpoint`.

**IAM** (`aws_iam_role` real-safe — the account's real
`aws-codestar-service-role`; the rest fake-only for now, no principled
reason they couldn't move to real-safe in a later batch):
`aws_iam_role`✓, `aws_iam_policy`, `aws_iam_role_policy_attachment`,
`aws_iam_user`, `aws_iam_group`, `aws_iam_instance_profile`,
`aws_iam_openid_connect_provider`.

**Storage** (`aws_s3_bucket` real-safe — the account's real `ubx-states`
bucket, proven since UBI-7; the rest fake-only for now):
`aws_s3_bucket`✓, `aws_s3_bucket_policy`, `aws_s3_bucket_versioning`,
`aws_s3_bucket_public_access_block`, `aws_ebs_volume`, `aws_efs_file_system`.

**Database** (all fake-only — hourly-billed, slow to provision):
`aws_db_instance`, `aws_db_subnet_group`, `aws_rds_cluster`,
`aws_elasticache_cluster`, `aws_dynamodb_table`.

**DNS / CDN / certs** (all fake-only — no hosted zone exists in the test
account, and creating one solely for this suite would add a real recurring
charge; revisit if a zone exists for another reason):
`aws_route53_zone`, `aws_route53_record`, `aws_cloudfront_distribution`,
`aws_acm_certificate`.

**Messaging / observability / secrets** (all fake-only for now — several
of these, e.g. `aws_sqs_queue`/`aws_sns_topic`, are actually free/cheap
enough to become real-safe in a later batch; not done opportunistically
this session):
`aws_sqs_queue`, `aws_sns_topic`, `aws_cloudwatch_log_group`,
`aws_cloudwatch_metric_alarm`, `aws_secretsmanager_secret`, `aws_kms_key`.

50 types total; 3 implemented (✓) as of UBI-9 session 1, proving the
harness across three different bias categories and two different schema
conventions (SDKv2's id+name-duplication quirk vs. framework-style's
plain id) before working through the rest in batches.
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
