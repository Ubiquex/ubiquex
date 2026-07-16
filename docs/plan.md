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
- 2026-07-11 — "UBI-11" (mislabeled — see STATE.md's correction; this
  ticket ID was never actually verified against Linear): `ubx why`
  polished ahead of demo recording. Now accepts a `<stack>.<type>.<name>`
  resource address as an alternative to a proposal ID, rendering that
  resource's full proposal chain (adoption + every subsequent drift,
  newest first) — proposal-ID lookup unchanged. `cloudtrail`/
  `cloudtrail_unattributed` intent.sources (UBI-10) now render the human
  attribution story inline instead of a bare kind/ref/hash line. See
  STATE.md for the full writeup, including the actual before/after
  rendering.
- 2026-07-11 — UBI-11 (real, Linear-verified — "M3–4 decision loop")
  Stage 1: PR-merge acceptance binding. `ubx propose`/`ubx accept
  --from-merge`/`ubx why --verify-acceptance`; acceptance derived from
  git history + the GitHub API, never asserted. New `github/` package
  (git history checks + `google/go-github`). Verified live: opened and
  merged a real PR against `Ubiquex/ubiquex-cli`, ran `ubx accept
  --from-merge` against the real merge SHA, correctly recorded zero
  approvers (unreviewed merge), cleaned up after. Backfilled into this
  changelog now — the session that did this work updated
  docs/architecture.md and docs/schema.md directly but missed this file;
  noted here rather than silently left out. See STATE.md for the full
  writeup.
- 2026-07-11 — UBI-11 Stage 2: `.tf` write-back. New `tfwrite/` package —
  `hclsyntax` locates the exact byte range of a literal attribute value
  (or a specific key within a literal object/list, e.g. `tags.hotfix`)
  and validates it's actually a literal by attempting `expr.Value(nil)`:
  an expression referencing a variable, function call, or interpolation
  fails to evaluate against a nil context, which is exactly "not a
  literal" — confirmed empirically before building on it. Replacement
  values are rendered via `hclwrite.TokensForValue` and spliced directly
  into the original bytes at that exact range — never a whole-attribute
  regeneration via `hclwrite`'s own `Body.SetAttributeValue`, which would
  reformat/lose comments on anything with internal structure. New `ubx
  writeback <proposal-id> --tf-dir <dir> [--write]` triggers only on an
  accepted `drift_adopt` proposal, prints a diff by default (never writes
  without `--write`, never commits/pushes). Every named adversarial case
  covered: attribute-is-expression (declines, reports the offending
  expression, leaves the file untouched), resource block absent/found in
  multiple places (hard error, no guessing), nested attribute paths,
  unusual-but-valid formatting (tabs, no spaces around `=`, compact
  single-line objects) surviving byte-for-byte. See STATE.md for the full
  writeup and a real before/after diff.
- 2026-07-16 — UBI-16 (Linear-verified): the revert path, M3-4's other
  resolution to a detected drift. `ubx scan --propose revert|adopt|both`
  (default `adopt`, unchanged) can generate a `drift_revert` proposal — the
  corrective direction (before=observed/drifted, after=ledger-recorded),
  real (non-zero) blast_radius, since accepting one is a decision to
  actually change cloud. New `ubx revert-plan <accepted-drift_revert-id>
  [--tf-dir]` emits (never applies) the reconciliation artifact: a
  human-readable plan always, a corrective `.tf` diff via the existing
  `tfwrite` machinery where `--tf-dir` is given and the attribute is a
  literal, and an honest manual-steps section otherwise. A real correction
  fell out of this work: `RunScan`'s drift baseline moved from
  `Ledger.LastObservedHash` to `ObservedHash(FoldState(addr))` — the two
  coincided for every proposal kind that existed before `drift_revert`
  (verified: the full pre-existing test suite passes unchanged), but a
  `drift_revert` can make them diverge on purpose (accepted-but-not-yet-
  applied), and the ledger's actual reconstructed truth is the
  semantically correct baseline for "did reality drift from the ledger"
  regardless. See docs/architecture.md's "Revert path" section and
  docs/schema.md's "Amendment: drift_revert proposals" for full design;
  STATE.md for the adversarial tests and the live end-to-end verification
  against the real `ubx-states` account.
- 2026-07-16 — UBI-17 (Linear-verified): `ubx status`, the fleet drift
  view — M1-2's last unstarted piece. Walks every resource the ledger
  knows about (discovered via `resolution.inputs[].resource`, one ledger
  walk); ledger-only by default, `--drift` adds a live comparison per
  resource using the exact same `ObservedHash(FoldState)` baseline `ubx
  scan` uses and each resource's own persisted `resolution.inputs[].lookup`.
  A per-resource failure is recorded as `unreadable`, never aborts the
  walk. New CI-facing exit-code contract (0 clean / 1 drift / 2
  unreadable-or-error), which needed a small, narrowly-scoped
  `cli.ExitCodeError` addition to how `cmd/ubx/main.go` maps errors to
  process exit codes — every other command's plain-error-means-exit-1
  behavior is unaffected. Surfaced a confirmed (not assumed) finding:
  `core.Ledger` is documented as "per-stack" but doesn't actually
  partition storage by stack at all — multiple stacks chain correctly
  within one shared ledger directory because proposal generation always
  reads the live current head, previously untested since every prior
  session used one stack per ledger directory. See
  docs/architecture.md's "Fleet status" section for full design; STATE.md
  for the adversarial tests and the live multi-resource verification
  (real `ubx-states` bucket plus a throwaway SQS queue) against the real
  account.
- 2026-07-16 — UBI-18 (Linear-verified): `ubx scan --all --tfstate <path>`,
  bulk onboarding — production ladder step 3. Enumeration source decided
  in the design room: the team's existing `.tfstate`, read once at
  onboarding as a border-crossing artifact, never depended on again;
  cloud-side discovery is explicitly a different epic. State provides
  identity only — every proposal's observed state still comes from a
  live `ReadResource` call, reusing `core.RunScan`/`core.GenerateProposal`
  unchanged. New `tfstate/` package parses Terraform state v4 JSON
  (modules, `count`/`for_each` instances addressed `name[index]`,
  `data`/output entries ignored outright). A small, explicit per-type
  lookup-augmentation table (not derived from `conformance/registry.go`'s
  `IdentityFields`, which answers a related but distinct question) covers
  the same empirically-known cases `cli/lookup.mdx` already documents.
  Stack defaults to the state file's own basename (`--stack` overrides);
  module paths become an `intent.summary` hint AND get folded into the
  resource's own address (for uniqueness — two different modules can
  declare a same-type same-name resource, a real "duplicate addresses"
  case the adversarial tests caught) — never an automatic stack split, a
  documented v1 decision. Unknown type / deleted-since-state / unbuildable
  lookup are recorded in a skipped-summary and never abort the walk.
  `--out-dir` batches one proposal file per resource, each one's `parent`
  chained to the precomputed hash of the one before it in the same batch
  (a real bug the live-verification test caught: left at the ledger's
  real, unmoving head, only the first of N proposals would ever accept).
  Bulk *acceptance* is explicitly out of scope. See docs/architecture.md's
  "Bulk onboarding" section for full design; STATE.md for the adversarial
  tests (synthetic 1000-resource state, malformed/truncated state,
  duplicate addresses, nested modules) and the live verification against
  a real, disposable Terraform config (fixture generator only, never a
  runtime dependency).
- 2026-07-16 — UBI-19 (Linear-verified): `.ubx/config` — production ladder
  step 4. TOML (not YAML — no implicit type coercion, matching this
  project's own determinism posture; see docs/architecture.md's "Config
  defaults" section for the full justification), parsed with
  `github.com/BurntSushi/toml`, the first dependency added purely for
  config parsing. Discovery walks from the current working directory
  upward, nearest `.ubx/config` wins, independent of `--ledger-dir`.
  Covers exactly five keys: provider (`path`, or `source`+`version`),
  `provider_config`, `stack`, `github_repo`, `tf_dir` — deliberately not
  `--ledger-dir`, which the issue never named and which is more
  consequential to get silently wrong than the others. Precedence is
  fixed everywhere it applies: CLI flag (checked via `cmd.Flags().Changed`,
  not a zero-value guess), then config, then whatever "required and
  absent" already meant for that flag. Unknown keys warn and are ignored;
  malformed TOML is a hard error. New `ubx init` writes a starter file,
  real values for whatever flags were supplied, commented examples for
  everything else. See docs/architecture.md for full design; STATE.md for
  the adversarial tests and per-verb integration.
- 2026-07-16 — UBI-20 (Linear-verified): the hardening pass, production
  ladder step 5 — "the credibility layer." Four independently-committed
  workstreams. (1) Exit-code contract extended from `status` alone to
  every verb (0 success, 1 an actionable finding, 2 error) — a deliberate
  breaking change to what "exit 1" meant for every other command
  (`cmd/ubx/main.go`'s fallback moves from `os.Exit(1)` to `os.Exit(2)`).
  (2) `--json` on `scan`/`status`/`why`: one versioned (`"format": 1`)
  JSON document on stdout, never mixed with human text; `why --json`'s
  resource-address form emits a `"chain"` array, newest first.
  (3) Teaching errors: `core.ErrResourceUnreadable` now names the likely
  fix for `aws_s3_bucket`/`aws_iam_role`/`aws_iam_user` plus a docs link,
  sourced from a new generated (`go:generate`), shipped `core/lookuphints`
  table — promoting the DATA out of `conformance/registry.go` (still
  test-only), not the package itself. Live verification against the real
  "ubx-states" bucket caught the hint direction backwards before it
  shipped: `{"id": ...}` alone succeeds, `{"bucket": ...}` alone (the
  natural-but-wrong key) reads back null — the opposite of what the
  Notes prose alone would have suggested. (4) Ledger lock: a PID-file
  lock at `.ubx/lock` (a third, distinct file alongside `.ubx/config` and
  `.ubx/ledger.lock`) wraps `Ledger.Append`'s whole check-then-write
  sequence, so two concurrent `Accept`/`AcceptFromMerge` calls serialize
  instead of racing; a live-held lock is waited out then reported with
  the holder's PID, a lock naming a dead PID is detected immediately and
  reported with recovery guidance, never auto-removed. `scan`/`why`/
  `status` never acquire it. See STATE.md for the full writeup, including
  the live-verification finding above.
- 2026-07-16 — UBI-21 (Linear-verified): GCP support, the first
  cross-provider generalization, both stages completed this session.
  `conformance.Registry`/`core/lookuphints` re-keyed from bare type name
  to (provider source, type) — AWS regression green throughout, including
  against the real account; `core.ScanRequest` gained an optional
  `ProviderSource`. `hashicorp/google` verified via `provider.Acquire`:
  negotiates tfplugin v5, same as `hashicorp/aws`. ~40 GCP resource types
  seeded into `conformance.Registry` (Stage 1); five of them
  (`google_storage_bucket`, `google_pubsub_topic`, `google_service_account`,
  `google_secret_manager_secret`, `google_project_iam_custom_role`)
  live-verified end to end and promoted to `RealSafe` (Stage 2), surfacing
  real per-type lookup-shape quirks distinct from anything AWS showed —
  including a "reads back successfully but with incomplete data, no error
  at all" failure mode for two types that the existing UBI-20
  teaching-error mechanism structurally can't address. New `gcpaudit/`
  package implements `core.EventLookup` against GCP Cloud Audit Logs,
  live-verified against a real Pub/Sub drift with the real caller's GCP
  account email recorded; `docs/schema.md` gained the purely-additive
  `gcp_audit`/`audit_unattributed` kinds (`cloudtrail`/`cloudtrail_unattributed`
  unchanged, still what `cloudtrail.Backend` emits). A real, confirmed gap
  was found and flagged rather than silently resolved: GCP audit log
  entries don't consistently use the same resource-identifier shape across
  services (project ID for Pub/Sub, project number for Secret Manager),
  breaking correlation for the latter until a per-service fix lands. See
  docs/architecture.md's "GCP support" section and STATE.md for the full
  writeup, including every empirical finding.
- 2026-07-17 — UBI-23 (Linear-verified): redact provider-`Sensitive`
  attributes in observed state — secrets must never enter the ledger.
  `provider.Redact` walks a resource schema's `Sensitive` flags over
  observed state at the `core.StateReader` adapter boundary
  (`cli/stateadapter.go`, `conformance/harness.go`), replacing each
  flagged value wholesale with `{"$redacted": {"sha256": "<salted
  hash>"}}` before `core` ever sees it — `core` stays schema-ignorant,
  recognizing only the resulting JSON shape (docs/schema.md's new
  amendment). Salt is per-ledger-directory (`.ubx/salt`, `Ledger.Salt()`),
  generated on first use, gitignored, never committed. Verified against
  real `hashicorp/aws`/`hashicorp/google` schemas that nested sensitivity
  is common (115/207 nested `Sensitive` attributes respectively, up to
  depth 4/3) — the existing `Block`/`NestedBlock` model already surfaces
  all of it. A real gap (the modern `NestedType` nested-attribute
  mechanism, unread by `blockFromV6`) was checked directly and found not
  to apply: both integrated providers negotiate wire protocol v5, and
  `NestedType` is v6-only — scoped out honestly, not assumed away. See
  docs/architecture.md's "Secrets" section and STATE.md for the full
  writeup.
- 2026-07-17 — UBI-22 (Linear-verified): Kubernetes support, the first
  non-cloud-provider provider (`hashicorp/kubernetes`, `hashicorp/helm`),
  both stages completed this session. Identity generalized with zero new
  mechanism; the real finding is `kubernetes_*`'s `metadata`/`spec`
  modeled as `NestingList`, yet `--lookup` needs only `{"id":
  "<namespace>/<name>"}` (confirmed live against a local `kind` cluster,
  correcting an initial Stage-1 guess that `metadata` itself would need
  pre-populating). `provider.Redact` (UBI-23) needed no Kubernetes-specific
  code: `kubernetes_secret_v1.data`/`binary_data` confirmed real
  `Sensitive` attributes, verified end to end (adopt, rotate, drift,
  grep-for-zero-material) against a real cluster; `helm_release.set_sensitive`
  contributed the first real Set-nested sensitive value in any
  currently-integrated provider, alongside a disclosed limitation
  (`manifest`/`metadata[0].values` aren't `Sensitive`-flagged, so a
  sensitive value can still surface there in plaintext if a chart
  template renders it). New `k8saudit/` package, a third `core.EventLookup`
  backend (EKS control-plane audit logs via CloudWatch Logs), dispatched
  by `ProviderSource` exactly like AWS-vs-GCP; a new, entirely optional
  `.ubx/config` `[k8s_audit]` table, unconfigured degrading to
  `audit_unattributed`/`not_configured` (docs/schema.md's new amendment),
  never blocking detection. Six real conformance tests (five
  `kubernetes_*` kinds + `helm_release`) live-verified against a real,
  local `kind` cluster and promoted to `RealSafe`. The EKS audit-log leg
  itself was deliberately not attempted — no EKS cluster existed already,
  and provisioning one is real, hourly-billed infrastructure judged out
  of proportion to create autonomously; `k8saudit.Backend.DeliveryLag`
  ships as a documented, unmeasured placeholder pending that. See
  docs/architecture.md's "Kubernetes support" section and STATE.md for
  the full writeup, including every empirical finding.

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
  `scan` (done since Slice 3), `status --drift` (done, UBI-17 — see §Fleet
  status below). Milestone complete: attributed drift on a real messy
  account in <5 min.

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
- **M3–4 (decision loop):** adopt/revert proposals signed via PR-merge or CLI
  (done, UBI-11 — see §Decision loop above); adopt writes corrected
  attributes back to existing .tf files (narrow-scope bidirectionality;
  done, UBI-11 stage 2); revert emits plan — apply via the team's own
  tooling at this stage, executor trust comes later (done, UBI-16 — see
  §Revert path below). GitHub App surfaces drift as issue/PR with receipt
  (done, UBI-11 stage 3). Milestone complete.
- **M5–6 (retention layer):** `why` over drift history, Slack notifications,
  policy stubs (auto-adopt sandbox / require-approval prod).

### Revert path (UBI-16)

`ubx scan --propose revert|adopt|both` (default `adopt`, unchanged) can now
generate a `drift_revert` proposal alongside or instead of `drift_adopt` —
the corrective direction: `before` = observed/drifted, `after` = the
ledger's existing truth being restored to. Unlike every other
drift/adoption kind, its `blast_radius` is real (accepting it is a decision
to actually change cloud, not a record of something that already
happened). New `ubx revert-plan <accepted-drift_revert-id> [--tf-dir]`
emits — never applies — the reconciliation artifact: a human-readable
plan, a corrective `.tf` diff via the same `tfwrite` machinery `ubx
writeback` uses (reversed direction — the file gets ledger truth, not the
drifted value) where attributes are literal, and an explicit manual-steps
section for anything that isn't. See docs/architecture.md's "Revert path"
section for the full design, including a real correction this session made
to `RunScan`'s own drift-detection baseline (compares against
`ObservedHash(FoldState(addr))` now, not `LastObservedHash` — provably a
no-op for every pre-existing proposal kind, necessary once `drift_revert`
can make the two diverge) and docs/schema.md's "Amendment: drift_revert
proposals" for the pinned validation rules. Verified live end to end on
the real `ubx-states` account: adopt → mutate → `scan --propose both` →
accept the revert → `revert-plan` output correct → manual `aws` CLI
correction → `scan` reports clean. See STATE.md for the full writeup.

### Fleet status (UBI-17)

`ubx status [--drift] [--stack <name>]` is M1-2's last unstarted piece: a
read-only report over every resource the ledger already knows about
(discovered via `resolution.inputs[].resource`, one ledger walk, latest
proposal per address wins), not one address per `ubx scan` invocation.
Ledger-only by default (kind/short-hash/accepted-at per resource, no
provider, no credentials); `--drift` adds a live comparison per resource
via the exact same `ObservedHash(FoldState)` baseline `ubx scan` uses,
reusing each resource's own persisted `resolution.inputs[].lookup` — the
entire reason that field exists. A per-resource failure (missing lookup,
unreadable provider, unknown type) is recorded as `unreadable` and the
walk continues; it never aborts the report. See docs/architecture.md's
"Fleet status" section for the full design, including a confirmed (not
assumed) finding about how multiple stacks actually chain together
correctly within one shared ledger directory, and the new
`cli.ExitCodeError` mechanism `ubx status`'s CI exit-code contract (0
clean / 1 drift / 2 unreadable-or-error) needed. Verified live against the
real `ubx-states` account plus a throwaway SQS queue (created and deleted
for this test, same pattern `conformance/aws_live_test.go` already uses),
so the fleet walk is genuinely multi-resource, not a single address
dressed up as a fleet. See STATE.md for the full writeup.

### Bulk onboarding (UBI-18)

`ubx scan --all --tfstate <path> [--stack <name>] [--propose adopt]
[--out-dir <dir>]` is production ladder step 3: a team with 300 resources
can't adopt them one `--lookup` at a time. Enumeration source, decided in
the design room before any code: the team's existing `.tfstate`, read
*once* at onboarding as a border-crossing artifact — the ledger owns
everything after, `ubx` never opens or depends on the file again.
Cloud-side discovery (tag-based enumeration, per-type list APIs) is a
different feature, a different epic, explicitly out of scope here. State
provides identity (how to look a resource up), never truth — every
resource's recorded observed state still comes from a live `ReadResource`
call, reusing the *exact same* `core.RunScan`/`core.GenerateProposal`
pipeline a single `ubx scan` already runs; bulk onboarding is an
orchestration layer, not a new proposal pipeline. See
docs/architecture.md's "Bulk onboarding" section for the full design,
including the small explicit per-type lookup-augmentation table (distinct
from, and not mechanically derived from, `conformance/registry.go`'s
`IdentityFields`), the module-paths-are-a-summary-hint-not-a-stack-split
decision, and exactly what gets skipped (unknown type, deleted-since-state,
unbuildable lookup) versus ignored outright (data sources, outputs).
Bulk *acceptance* is deliberately not part of this issue. A real bug
surfaced only by live-verifying against a real, disposable Terraform
config (Terraform used only as a test-fixture generator, never a runtime
dependency): every proposal in one batch shared the same stale `parent`
(the ledger's real head never moves mid-walk, since nothing gets accepted
until later), so only the first one anyone accepted would ever succeed —
fixed by chaining each generated proposal's `parent` to the precomputed
hash of the one before it in the same batch, entirely within the `--all`
orchestration itself. See STATE.md for the full writeup.

### Config defaults (UBI-19)

`.ubx/config` (TOML — determinism-motivated, see docs/architecture.md's
"Config defaults" section for the full justification against YAML) lets a
team stop repeating `--stack`, `--source`/`--provider-version` (or
`--provider`), `--provider-config`, `--github-repo`, and `--tf-dir` on
every daily command. Discovery walks from the current working directory
upward, nearest `.ubx/config` wins — independent of `--ledger-dir`, since
a project's defaults are a property of where the operator is standing,
not of wherever the ledger happens to live. Precedence is fixed: CLI flag,
then config, then whatever "required and absent" already meant for that
flag (unchanged). Unknown keys warn and are ignored, never a hard
failure; a config file that isn't valid TOML at all is. New `ubx init`
writes a starter file — every key the caller supplies a flag for is
written as a real value, everything else as a commented example. See
docs/architecture.md for the full design; STATE.md for the adversarial
tests and per-verb integration.

### Hardening pass (UBI-20)

Production ladder step 5, "the credibility layer," four independently
shippable workstreams: (1) a documented 0/1/2 exit-code contract across
every verb, not just `status` — a deliberate, documented breaking change
to what a plain error's exit code meant everywhere else (1 → 2; 1 is now
reserved for actionable findings specifically); (2) `--json` on
`scan`/`status`/`why`, every payload versioned with `"format": 1`, human
output unchanged and still the default; (3) teaching errors — `scan`'s
"provider returned no state" now names the likely fix for the three
empirically-known types whose mistake is a missing field, not just a
surprising id value (`aws_s3_bucket`, `aws_iam_role`, `aws_iam_user`;
`cli/lookup.mdx`'s other four "confirmed non-default" types use a
surprising but sufficient id value and aren't a missing-field mistake, so
they're deliberately not in this table), sourced from a small generated,
shipped table (`core/lookuphints/`) rather than importing the test-only
`conformance/` package into product code; (4) a per-ledger-directory
lockfile (`.ubx/lock`) making concurrent `ubx` processes safe, with
explicit stale-lock detection (a dead holder's PID) rather than either
hanging forever or silently breaking a live lock. See docs/architecture.md's
"Hardening pass" section for the full design of all four; STATE.md for
the adversarial tests and live verification.

### GCP support (UBI-21)

The first cross-provider generalization: `conformance.Registry`/
`core/lookuphints` re-keyed from bare type name to (provider source,
type), `core.ScanRequest` gains an optional `ProviderSource`, and a
second attribution backend (`gcpaudit/`, against GCP Cloud Audit Logs)
is designed — see docs/architecture.md's "GCP support" section for the
full design, including the `audit_unattributed` schema.md amendment.
Two stages, gated on GCP account availability:

- **Stage 1 (hermetic)**: the keying refactor (AWS regression green);
  `hashicorp/google` verified via `provider.Acquire` — empirically
  negotiates tfplugin **v5**, same as `hashicorp/aws`; ~40 GCP
  `conformance.Registry` entries seeded (see the type list below),
  `IdentityFields` from real schema inspection, `Safety: FakeOnly`,
  `Implemented: false` — mirroring UBI-9 session 1's own AWS
  bootstrapping exactly.
- **Stage 2 (needed a real GCP project + credentials + Cloud Audit Logs
  enabled — done this same session)**: five types live-verified
  (adopt→mutate→scan-diff): `google_storage_bucket`, `google_pubsub_topic`,
  `google_service_account`, `google_secret_manager_secret`,
  `google_project_iam_custom_role` — each promoted to `RealSafe`, real
  per-type lookup-shape findings recorded in `conformance/registry.go`'s
  own `Notes` (see docs/architecture.md for the full per-type writeup,
  including a materially more dangerous "silently reads back incomplete
  data, no error at all" shape two of the five types have that no AWS
  type ever showed). `gcpaudit/` implemented and live-verified against a
  real Pub/Sub drift with the real caller's actual GCP account email
  recorded, via the actual `ubx scan` command; Cloud Audit Logs' own
  delivery latency measured directly (~18s for one Pub/Sub mutation —
  the CloudTrail lesson, UBI-10, applied to a second platform rather
  than assumed to transfer, and confirmed much faster this time).

#### M1-2 GCP resource type list (UBI-21 Stage 1)

The ~40 types below mirror `docs/plan.md`'s own AWS list's category
spread and "real GCP shop" bias — `conformance/registry.go`'s
`hashicorp/google`-sourced entries are the executable counterpart, this
list is the rationale. All seeded `Safety: FakeOnly`, `Implemented:
false` this session (Stage 1 is hermetic — no live GCP account touched);
`IdentityFields` verified against the real `hashicorp/google` 7.40.0
schema (free, no credentials, same standard the AWS list holds to).

**Compute**: `google_compute_instance`, `google_compute_instance_template`,
`google_container_cluster` (GKE), `google_cloudfunctions2_function`,
`google_cloud_run_v2_service`, `google_cloud_run_v2_job`.

**Network**: `google_compute_network` (VPC), `google_compute_subnetwork`,
`google_compute_route`, `google_compute_router`,
`google_compute_router_nat`, `google_compute_firewall`,
`google_compute_address`, `google_compute_global_address`,
`google_compute_forwarding_rule`, `google_compute_backend_service`.

**IAM**: `google_service_account`, `google_service_account_key`,
`google_project_iam_member`, `google_project_iam_binding`,
`google_project_iam_custom_role`.

**Storage**: `google_storage_bucket`, `google_storage_bucket_iam_member`,
`google_storage_bucket_object`, `google_compute_disk`,
`google_filestore_instance`.

**SQL / database**: `google_sql_database_instance`, `google_sql_database`,
`google_sql_user`, `google_spanner_instance`, `google_firestore_database`.

**DNS / certs**: `google_dns_managed_zone`, `google_dns_record_set`,
`google_compute_ssl_certificate`.

**Messaging / observability / secrets**: `google_pubsub_topic`,
`google_pubsub_subscription`, `google_logging_metric`,
`google_monitoring_alert_policy`, `google_secret_manager_secret`,
`google_kms_crypto_key`.

40 types total. Unlike the AWS list at this same bootstrapping stage,
none are marked real-safe or parked yet — that classification (which
ones are cheap enough to live-verify, which ones "fight back" the way
`aws_iam_group`/`aws_route_table_association` did) is Stage 2 work,
done against a real account rather than guessed from schema inspection
alone, the same discipline UBI-9 followed.

### Secrets (UBI-23)

Every `Sensitive`-flagged attribute in a resource's observed state is
replaced with a salted fingerprint (`{"$redacted": {"sha256": "..."}}`)
before it ever reaches `core` — see docs/architecture.md's "Secrets"
section for the full mechanism (redaction at the `core.StateReader`
adapter boundary, `provider.Redact`, the per-ledger `.ubx/salt`) and
docs/schema.md's `$redacted` value-encoding amendment for the wire shape
and hashing rule. Drift detection is preserved end to end: an unchanged
secret's redacted hash matches across scans (same salt, same real value),
a genuinely changed one doesn't. `writeback`/`revert-plan` both decline
ever writing a redacted marker into `.tf` source, surfacing it as a
manual-restoration step instead.

### Kubernetes support (UBI-22)

The first non-cloud-provider provider: `hashicorp/kubernetes` and
`hashicorp/helm`, both empirically confirmed to negotiate tfplugin wire
protocol v5 (dual v5/v6 support earning its keep a third time). Identity
generalizes with zero new mechanism (UBI-21's (provider source, type)
keying already covers it) — the real finding is that `kubernetes_*`
types model `metadata`/`spec` as `NestingList`, not `NestingSingle`,
unlike every AWS/GCP type checked so far, while `helm_release` has a
flat, AWS/GCP-shaped identity with no such nesting at all. `provider.Redact`
(UBI-23) needed no Kubernetes-specific code — confirmed live that
`kubernetes_secret_v1.data`/`binary_data` are both real `Sensitive`
attributes (no upstream gap, no per-type override needed), and
`helm_release`'s `set_sensitive` block contributed the first real
Set-nested sensitive value seen in any currently-integrated provider —
alongside a disclosed limitation: `helm_release.manifest`'s rendered
output isn't itself `Sensitive`-flagged, so a value that started sensitive
can still appear in plaintext there if a chart template renders it.
`k8saudit/` is a third `core.EventLookup` backend (against EKS
control-plane audit logs in CloudWatch), dispatched by `ProviderSource`
exactly like AWS-vs-GCP, requiring one new, explicitly optional `.ubx/config`
table (`[k8s_audit]`) since — unlike AWS/GCP — there's no way to derive
"which cluster" from anything `ubx` already has; unconfigured degrades to
`audit_unattributed`/`not_configured` (docs/schema.md's new amendment),
never blocking detection. `helm_release` is a resource like any other;
chart-aware diffing (tracking the individual Kubernetes objects a release
manages, or diffing inside rendered manifests) is explicitly out of
scope. See docs/architecture.md's "Kubernetes support" section for the
full design and every empirical finding, and STATE.md for the live
Stage 2 conformance/attribution results.

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
