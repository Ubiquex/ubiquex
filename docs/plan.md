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
- 2026-07-18 — UBI-24 (Linear-verified): sensitive-override table,
  closing UBI-22's own `helm_release` redaction gap. Redaction is now
  the union of a provider's own `Sensitive` schema flags AND a new,
  ubx-owned `provider/overrides.go` table (`(source, type, path)` →
  force-redact) — the schema is a floor, never a ceiling; overrides can
  only add, never remove. Seeded with `helm_release.manifest`/
  `metadata.values`/`metadata.notes`. A direct audit of both
  `hashicorp/kubernetes`'s ~20 registered types and `hashicorp/helm`
  found no further candidates. A precise root-cause correction: Helm's
  `metadata` isn't a real `NestedBlock` (unlike Kubernetes' own) — it's a
  compound-typed `Attribute` (`list(object(...))`), a shape tfplugin's
  wire protocol has no mechanism to flag a sub-field of at all upstream,
  which is exactly why a ubx-owned, JSON-shape-driven override (not a
  schema-walk one) is the right fix. Live-verified end to end on a real
  local `kind` cluster: a `helm_release` with a `set_sensitive` value
  adopted, and its values-drift path, both grepped by hand for zero real
  material. A draft (unsubmitted) upstream issue for the Helm provider is
  saved at docs/upstream/helm-sensitive-flags.md. This gates the
  `v0.2.0` tag. See docs/architecture.md's "Sensitive overrides" section
  and STATE.md for the full writeup.
- 2026-07-18 — UBI-25 (Linear-verified): read-only MCP server. A new
  `ubx mcp` verb (one binary, not a second executable) serves the Model
  Context Protocol over stdio via `github.com/modelcontextprotocol/go-sdk`
  — three tools (`ubx_why`/`ubx_status`/`ubx_scan`), each a thin wrapper
  over the exact `whyJSON`/`statusJSON`/`scanJSON` payload the equivalent
  `--json` CLI command already produces (new `computeWhyJSON`/
  `computeStatusJSON`/`computeScanJSON` functions shared by both callers
  — no parallel API, no new JSON shape). `ubx accept`/`ship`/`writeback`/
  `revert-plan`/`scan --surface-as` are deliberately not exposed —
  "boundary by omission," stated in both `--help` and the docs page. A
  real, load-bearing SDK gotcha found by actually calling the tools over
  the real protocol, not assumed safe from the Go types alone: automatic
  output-schema generation from `*core.Proposal`'s own `json.RawMessage`
  fields (used throughout for canonical-JSON hashing) infers "array" from
  the underlying `[]byte`, which then fails validation against the real
  (often-object-shaped) runtime value — fixed by using `any` as each
  tool's output type, which the SDK's own docs already name as the way to
  skip a schema it can't correctly generate here. Live-verified against
  the real `ubx-states` ledger: a real `PutBucketTagging`/`DeleteBucketTagging`
  mutation, scanned and accepted with real CloudTrail attribution, then
  asked "who changed this bucket and when" via a real MCP client
  connected to the real `ubx mcp` subprocess over real stdio — captured
  for the docs page, cleaned up after. See docs/architecture.md's "MCP
  server" section and STATE.md for the full writeup.
- 2026-07-17 — UBI-26 session 1 (docs-only): Phase 2 opens, the executor —
  v1 scoped to shipping accepted `drift_revert` proposals only. Design
  landed across docs/schema.md ("Amendment: apply records" — a new
  hash-chained `ledger/applies/<id>.apply.json` object family), the new
  docs/executor.md (the pending→in_flight→applied/failed/unknown_post_timeout
  failure-state machine spec), and the new docs/executor-adversarial.md
  (the required-outcome program, also meant to double as a future
  published reliability report). Two real design findings resolved and
  documented, not glossed over: `Proposal.status` can never be rewritten
  to `applied`/`partially_applied` in place (ledger entries are immutable
  by construction) — resolved by making those values derived/reported,
  folded from the latest apply record over the stored `accepted` status;
  and the real `tfplugin{5,6}` `ApplyResourceChange_Request` proto requires
  a `PlannedState` a real plan phase would normally produce — `drift_revert`'s
  always-concrete restore values are what let v1 construct it directly
  instead, a shortcut scoped to this kind only. See docs/plan.md's own new
  "Executor v1 (UBI-26)" wedge subsection for the full summary, and
  STATE.md for the session writeup.
- 2026-07-17 — UBI-26 session 2: `core/apply.go` (the `ApplyRecord` type
  family, its own `ubx:apply:v1\n`-domain content hash, and `Ledger`'s
  apply-attempt storage — `BeginApply`/`SaveApplyProgress`/`SealApply`/
  `ApplyAttempts`/`ReadApply`, reusing the same PID-file ledger lock
  `Append` already uses) and the new `core/executor` package (the
  pending→in_flight→applied/failed/unknown_post_timeout state machine
  itself, `Ship`, against a hermetic fake `Applier`). All ten rows of
  docs/executor-adversarial.md's program pass as real, hermetic Go tests
  (the "provider killed" row simulated via a generic transport-style
  error, not a literal process kill — that's reserved for the later live
  session's real `kill -9`). `core.ReadAndFingerprint` and `core.ApplyAfter`
  were added as small, additive exports so the executor could reuse
  `RunScan`/`VerifyFreshness`'s own read pipeline and `FoldState`'s own
  dot-path substitution, rather than duplicating either. See STATE.md for
  the full session writeup, including a real per-resource idempotency
  refinement found while implementing (folding "last known state" over
  *every* attempt file, sealed or not, not just sealed ones).
- 2026-07-17 — UBI-26 session 3: real `ApplyResourceChange` wiring
  (`provider.Provider` gains it for v5/v6, a new `provider.DiagnosticError`
  distinguishes a real provider diagnostic from a transport failure) behind
  the executor's `Applier` interface (`cli/stateadapter.go`'s
  `stateReaderAdapter`, now also an `executor.Applier` — redaction on the
  way out, `provider.DiagnosticError`→`executor.TerminalError` on the way
  in). Redacted restore targets are declined outright, both directions of
  docs/executor.md's redaction requirement now real. Then the CLI itself:
  `ubx ship <proposal-id>`, exit codes 0/1/2, `--json`. A real, load-bearing
  bug found live-testing the whole path end to end against the real
  built binary (not just unit tests): `core.ApplyAfter` only ever *set*
  `Modification.After`'s dot-paths, never removing one that existed only in
  `Before` (an attribute added out-of-band, which the ledger's own truth
  never had) — a shipped revert reported "applied" while silently leaving
  the added attribute in place. Fixed (`core.dotDelete`, a permanent
  regression test both at the `core.ApplyAfter` unit level and end-to-end
  through `Ship`), and a real `tfplugin{5,6}`/`hashicorp/time` empirical
  verification (`provider/apply_live_test.go`, gated
  `UBX_CONFORMANCE_LIVE=1`, no cloud credentials needed) confirms the
  underlying "construct `PlannedState` without planning" mechanism is sound
  once given a realistic prior state. See STATE.md for the full session
  writeup, including a second real false start (`PriorState=null`) that
  briefly looked like a design gap and wasn't one.
- 2026-07-17 — UBI-26 session 4 (closing): the live adversarial program
  against real AWS (`ubx-states`, `us-east-1`) — docs/reliability-report.md,
  drafted from docs/executor-adversarial.md's own table plus every
  hermetic and live result. `ubx`'s first real cloud write (a real
  `drift_revert`, independently verified via `aws s3api`, not just
  trusted from the tool's own report); the centerpiece, a real `kill -9`
  between a real `ApplyResourceChange` call succeeding and `ubx` recording
  it (`core/executor/ship.go` gained two zero-by-default, env-var-gated
  debug delay seams to make the exact window reproducible on demand); a
  real stale-mid-flow refusal. Two more real bugs found and fixed live,
  same class as session 3's: `reconciliationVerdict` could never conclude
  `applied` for a pure-deletion revert (empty `After`), and `ubx why`
  never rendered anything about a proposal's own apply history at all
  (`cli/why.go` gains `renderApplies`/`whyJSON.Applies`). Account restored
  to match the ledger's own recorded truth, confirmed clean via `ubx
  status --drift`. Closes UBI-26. See STATE.md and
  docs/reliability-report.md for the full writeup and every transcript.
- 2026-07-17 — UBI-27 session 1 (docs-only): Phase 2 continues, the
  resolver — v1 scoped to `change` proposals (creates + modifies, no
  destroys) from hand-written `ubx:intent/v1` files. Design landed across
  docs/resolver.md (new), docs/schema.md ("Amendment: intent files and
  resolved `change` proposals"), docs/executor.md ("Amendment: shipping
  resolved `change` proposals"), and docs/resolver-adversarial.md (new,
  ten rows). A real correction found before any design work: CLAUDE.md/
  docs/architecture.md's "v1 XCL typechecker" points at the wrong repo by
  name — `xcl` is lexer/parser/AST/formatter only (confirmed directly, not
  assumed); the real type system and graph algorithms live in a separate,
  Pulumi-targeting repo, `ubx`. Rules lifted from *that* repo's real code
  instead, with two real gaps found and NOT carried forward as-is: v1's
  own single-stack resource graph never detected cycles at all (only its
  workspace-level multi-stack one did), and v1 had neither cross-stack
  pinning/staleness nor double-run/determinism enforcement — all three are
  deliberate v2 improvements, reusing `core.DoubleRun`/`VerifyFreshness`'s
  own staleness shape rather than inventing new mechanisms. See STATE.md
  for the full session writeup.
- 2026-07-17 — UBI-27 session 2: `core/resolver` built hermetic against
  fake schemas/ledger state — type rules, the dependency graph with real
  cycle detection, `core.DoubleRun` reused unchanged. All nine hermetic
  rows of docs/resolver-adversarial.md's program pass as real tests (row
  10 is real-provider live-session work). A real gap found and fixed while
  implementing, not assumed correct from the session-1 design alone: the
  `$cross` marker's drafted shape never actually named the target
  resource's `type`/`name` — corrected to reuse `$ref`'s own `to` shape;
  `ResolutionInput` gained `LedgerDir` alongside `PinnedHead`, and a new
  `resolver.VerifyPins` makes neighbor-advance staleness real and tested.
  `core.DiffAttributes` exported (a real third caller now exists, not
  duplicated). See STATE.md for the full session writeup.
- 2026-07-17 — UBI-27 session 3: CLI surface (`ubx resolve <intent-file>`,
  a new verb, not a flag on `ubx propose`) + `resolver.VerifyPins` wired
  into both `ubx accept` paths (local file and `--from-merge`), as an
  unconditional check — reading a neighbor ledger's head is a free local
  read, unlike `--reverify-with`'s real provider round trip.
  `acceptErrorCode` now classifies `resolver.ErrCrossStackPinStale` as exit
  `1`. A real gap surfaced against the session-1 design doc: docs/
  resolver.md names `core.StateReader` as an input, but `Resolve()` never
  actually uses one — only `l.FoldState()` — so `ubx resolve` needs no
  `--provider-config` and never configures/reads through the provider,
  only fetches its schema (`cli/schemainspector.go`, a new
  `SchemaInspector` adapter). New CLI-level tests
  (`cli/resolve_test.go`), plus a real, built-binary verification of the
  whole cross-stack pin loop against real ledger directories on disk:
  resolve with `$cross`, accept while fresh (passes), advance the
  neighbor, accept again (refused, exit 1, nothing written). ubiquex-docs
  gained `cli/resolve.mdx` and an accept.mdx "Cross-stack pin
  verification" section, both with real transcripts;
  `cli/exit-codes.mdx` updated. `mint validate`/`mint broken-links` pass.
  See STATE.md for the full session writeup.
- 2026-07-17 — UBI-27 session 4 (closes UBI-27): executor unknown-value
  wiring + the live create finale. `provider/ctyvalue.go`'s
  `encodeUnknownAwareDynamicValue` (real `cty.UnknownVal` for `$computed`
  markers AND schema-`Computed`-but-unset attributes, the latter found
  live, not in the original design) verified against a real provider
  (`hashicorp/time`, resolver-adversarial row 10, settled both ways).
  `core/executor/ship.go` gained `shipChange` — creates + modifies
  together, real dependency order, applied outputs fed forward via
  `foldResourceHistory`'s new `lastProviderResult` (survives a kill
  between resources). Two real bugs found and fixed live: `shipCreate`
  never called `Applier.Configure` (a real AWS provider crashed with a
  bare transport EOF rather than a clean error); `core/resolver.Resolve`
  called `time.Now()` fresh per `DoubleRun` call, a rare false-positive
  mismatch across a second boundary. Live-verified on real AWS (account
  `839333509514`): a real `aws_sqs_queue`+`aws_sqs_queue_policy` chain
  shipped for real — the first real cloud creates this codebase has ever
  made — plus a real `kill -9` between the two resources, correctly
  recovered on re-run, verified independently via `aws sqs`. Cleaned up
  via plain `aws` CLI (destroys stay out of v1 scope). One real,
  unresolved gap found doing that cleanup, recorded as a follow-up rather
  than rushed: a shipped create is invisible to `ubx status`/`ubx why
  <address>` afterward (`Fleet`'s discovery keys entirely on
  `resolution.inputs`, which a create never populates for itself).
  docs/reliability-report.md gained a full UBI-27 section; ubiquex-docs
  gained `cli/ship.mdx` change-proposal coverage and
  `guides/create-flow.mdx`. See STATE.md for the full session writeup.
- 2026-07-17 — UBI-29 (files and closes): Fleet visibility for shipped
  creates. `core.Ledger.Fleet`/`FoldState`/`ProposalsForAddress`/
  `LastObservedHash`/`LastObservationTime` all now fold a `change`
  proposal's own apply records as a second discovery source, gated on the
  specific resource's own last transition being `applied` — never on the
  enclosing multi-resource attempt being sealed. `ResourceApply` gains an
  additive `lookup` field, recorded explicitly by `shipCreate` at ship
  time (Slice 3's own "record explicitly, never derive at need-time"
  lesson), with a graceful read-time fallback for pre-amendment apply
  records. A deeper, related gap found designing the fix: `FoldState`
  itself never recognized a change-proposal create's own node shape at
  all, fixed alongside. Hermetic coverage for all three named adversarial
  rows plus the design's own key per-resource-not-per-attempt gating
  claim. Live-verified on real AWS: `ubx status` sees a shipped chain
  immediately; a real out-of-band mutation was detected, attributed, and
  corrected; `ubx why <address>` shows the full create-genesis chain where
  it used to report nothing at all. docs/reliability-report.md gained a
  UBI-29 section; ubiquex-docs gained a `cli/status.mdx` note and a
  `cli/why.mdx` "genesis is a shipped create" section. See STATE.md for
  the full session writeup.

- 2026-07-17 — Design-room decision (no session): Nexus execution
  topology. Recorded in §Execution topology below. Initially decided as
  two modes with hosted execution refused; REVISED same day by founder
  decision to a three-mode model — self-hosted agent, managed agent, and
  Nexus-hosted execution ("Nexus Runs") as the convenience tier. The
  surviving unqualified invariant across all modes: Nexus can never
  apply anything a human didn't sign (no signing authority, signed-hash-
  only execution). Hosted mode's guardrails: OIDC dynamic federation
  only, never stored keys, per-tenant ephemeral runners, ubx-agent as
  the single runner codebase. Trust framing per mode disclosed honestly,
  never blurred.
- 2026-07-17 — UBI-30 session 1: destroys, the executor's last verb —
  design landed docs-only, no code. Filed as its own ticket (UBI-30), team
  `ubiquex`. docs/resolver.md gained "Amendment (UBI-30): destroys" — a
  dedicated `destroys[]` list in `ubx:intent/v1` (never an `op: "destroy"`
  on `resources[]`, and never inferred from a resource's absence, now or
  ever — a permanent boundary, not a v1 scope line), resolve-time orphan
  protection (intra-stack via the existing `depends_on` reverse-edge walk
  across the whole ledger chain; cross-stack best-effort via an explicit
  `known_dependents` list, honestly recorded as `not_performed` when
  omitted rather than silently assumed clear). docs/schema.md gained
  "Amendment: destroys" — `Delta.Destroys`' element shape re-pinned from a
  bare `Address` to `{address, state, depends_on}` (a real hashed-content
  shape change, `schema_version` bump to 2 — checked, the migration cost
  is genuinely near-zero since no proposal of any kind has ever populated
  `delta.destroys` under the old shape), two new `resolution.inputs[]`
  kinds (`destroy_target`, required; `cross_stack_orphan_check`,
  evidence-only), the `--confirm-destroys` accept-time flag (this
  project's first hardcoded acceptance-friction invariant, distinct from
  every other validation/staleness check in the schema), and the tombstone
  posture (`FoldState` folds a fully-destroyed address back to absent,
  enabling recreation under the same address later, while the ledger
  chain itself is never rewritten — `ubx why` renders the complete
  biography forever). docs/executor.md gained "Amendment: shipping
  destroys" — one combined topological walk across creates/modifies/
  destroys (`changeNodesOf`'s existing `byAddr`/`topoSortAddresses`
  extended, not duplicated — "reversed" ordering falls out of destroy
  entries' `depends_on` carrying the reverse edge set, not a second
  mechanism), `ApplyResourceChange` wire mechanics for a destroy (`PriorState`
  freshly re-read, `PlannedState`/`Config` both the literal `null`), a
  three-way freshness precheck (matches / drifted-refuse / already-absent-
  short-circuit), and the `destroyed`-vs-`already_absent` disambiguation
  (reusing `ResourceApply.Reconciliation` one step earlier than its only
  prior use, folded across the `parent` attempt chain via the existing
  `foldResourceHistory`, never a new field). docs/destroys-adversarial.md
  is new: eleven required-outcome rows (drift since acceptance; kill -9
  before/after the call; timeout landed/not-landed; already-absent target;
  orphan-protection refusal; mixed create+destroy ordering; destroy racing
  a concurrent scan; re-ship after partial destroy; `ubx why` on a
  destroyed address), plus named gaps this table doesn't yet cover. See
  §Destroys v1 (UBI-30) below, STATE.md for the session writeup, and
  Linear UBI-30 for the full session breakdown (sessions 2+: resolver
  destroy support, executor reversed-walk + destroy state machine, accept
  friction + CLI surface, then a live full-lifecycle finale on real AWS).

- 2026-07-17 — Design-room decision (no session): ledger stores.
  Recorded in docs/architecture.md §Ledger stores — authoring mediums
  always live in git as repo assets (hash-pinned evidence, already the
  design); the ledger's own JSON gets a configurable store behind a
  future LedgerStore interface: git directory (default, reference
  implementation) or object stores (s3:// / gs:// / azblob://), each
  earning support via its own conformance suite (locking, CAS head, PR-
  acceptance ceremony). Vocabulary: "store" (config key `store`, matching
  the LedgerStore interface), never "backend" or "location." Filed as
  a parked ticket; nothing built yet.

- 2026-07-17 — Design-room decision (no session): ledger addressing +
  config cascade + config formats, extending §Ledger stores in
  docs/architecture.md. Addressing: `<base store>/<stack>/` derived by
  rule, never mapped; $cross pins resolve by stack NAME against the base
  (relative-path fragility dies); envs are just deeper base prefixes;
  chain becomes per-store (per-stack true by construction on
  remotes); one `external` table only for cross-base refs. Config:
  editorconfig-style cascade — per-key resolution, child overrides
  parent, tables merge key-wise, flags beat all; ships with a
  provenance view (every value + which file supplied it). Formats: HCL
  canonical (literal-only, enforced), TOML supported forever, YAML
  supported strict-mode-only (implicit coercion = hard error) —
  discovery config.hcl → config.toml → config → config.yaml, first
  found per directory. UBI-32's scope updated to match.

- 2026-07-17 — Design-room decision (no session): multi-provider stacks.
  Recorded in docs/architecture.md §Multi-provider stacks — a `providers`
  config map (source → pinned version) declares a stack's provider set;
  intent files name only types; type→provider inference via schema
  ownership (never prefix guessing, ambiguity is a hard error); the
  resolver records each node's provider into the IR's founding-draft
  `provider` field (signed per resource); executor runs one dependency
  walk over a lazily-launched provider-client pool with outputs flowing
  across provider boundaries. --source/--provider-version retire from
  resolve when this lands. Config portion rides UBI-32; resolver/executor
  portion is its own session, before or with the SDK.

- 2026-07-17 — Design-room decision (no session): Phase 3 medium order
  reversed — markdown before SDK. Founding order (SDK → chat → md) was
  preference, not dependency; revised path after UBI-30 (destroys) is:
  multi-provider session (unchanged, still first — md stacks hit the
  one-provider wall exactly like SDK would) → intent provider + md
  medium (the LLM interface: adapters, structured-output validation,
  conformance gating, BYO keys — md-first means intent-provider-first;
  extraction quality is gated on that conformance suite, which IS the
  work) → chat (nearly free once the intent provider exists — same
  interface, different input shape) → SDK after (typed authoring, IDE
  safety, round-trip projection, codegen wait behind it, accepted
  consciously). Rationale: md is the demo-gold AI-native medium, less
  build than SDK (no codegen/npm/hermetic-JS-sandbox), and delivers two
  mediums (md + chat) for one infrastructure build.

- 2026-07-17 — Design-room decision (no session): SDK languages — TS,
  Go, and Python all supported, in that ship order; filed as UBI-33
  (umbrella: the multi-language contract — a language-neutral conformance
  suite of golden intent/v1 JSON IS the spec, written before any language
  ships; shared codegen IR model with per-language templates, no
  TS-isms), UBI-34 (TypeScript first, ≈6–9 sessions, hermetic sandbox is
  the hard part), UBI-35 (Go second, ≈3–5 — compiled-program evaluator
  cheat may make it cheaper than TS, verify empirically), UBI-36 (Python
  last, demand-gated — hardest sandbox, no cheat). Frictionless-future
  prep noted in UBI-33: intent/v1 emission stays the stable importable
  contract; golden files stored language-neutrally. Sequencing: after
  the md/chat mediums per the medium-order reversal above.

- 2026-07-17 — UBI-30 session 2: `core/resolver` destroy support, real
  code, hermetic. `Delta.Destroys` re-pinned to `{address, state,
  depends_on}` (`core.SchemaVersion` 1 → 2, this project's first
  non-additive hashed-content shape change — migration cost near-zero,
  checked: no proposal of any kind ever populated the old shape).
  `core.Validate` now lets `KindChange` carry destroys; `core/resolver`
  gained `IntentFile.Destroys`, `Resolve`'s new `knownDependents`
  parameter, presence validation, intra-stack orphan protection (a real
  `depends_on` ledger walk), cross-stack orphan protection
  (`known_dependents`, honest `not_performed`/`checked_clear`), and a new
  `ErrRefToDestroyTarget` rule (found necessary while implementing, not
  in session 1's design) rejecting a `$ref`/`$cross` into a same-batch
  destroy target. New `ubx resolve --known-dependent` flag. A real bug
  found building ubiquex-docs' own CLI transcripts, not just a docs
  polish item: orphan protection originally treated a `depends_on` edge
  as permanent once recorded, wrongly re-refusing a destroy whose
  dependent had since been repointed away by a separate proposal — fixed
  to track each address's own most-recently-recorded `depends_on` only,
  with a dedicated regression test. Full suite (`go build`/`go vet`/
  `gofmt -l .`/`go test ./... -race -count=1`) clean. docs/resolver.md and
  docs/destroys-adversarial.md gained session-2 addenda recording both
  real findings above. ubiquex-docs' `cli/resolve.mdx` updated with real
  transcripts against the actual built binary (`mint validate`/`mint
  broken-links` clean). See §Destroys v1 (UBI-30) above and STATE.md for
  the full session writeup.

- 2026-07-18 — UBI-30 session 3: `core/executor` destroy support, real
  code, hermetic — all eleven docs/destroys-adversarial.md rows green.
  `changeNodesOf` extended with a `destroy` node type sharing the exact
  same combined `topoSortAddresses` walk creates/modifies already use
  ("reversed ordering" is not a second mechanism); new `shipDestroyNode`
  (three-way freshness precheck, `ApplyResourceChange` wire mechanics
  needing zero `provider`/`cli/stateadapter.go` changes at all) and
  `reconcileDestroyLoop` (the `destroyed`-vs-`already_absent`
  disambiguation, folded across the `parent` attempt chain via a new
  `resourceHistory.lastReconciliationOutcome`). A real, load-bearing bug
  found by this session's own hermetic "re-ship after partial destroy"
  test: `shipChange`'s `resultsByAddr` dependency-satisfied gate required
  a non-empty `ProviderResult`, which a destroy can never have — silently
  re-blocking anything depending on a destroyed resource forever; fixed to
  gate on terminal `applied` state alone. `fakeApplier` and the real
  subprocess `provider/internal/fakeprovider` fixture both gained genuine
  destroy mechanics — the subprocess fixture's first piece of cross-call,
  process-lifetime state (`destroyedIDs`), since confirming absence after
  a destroy is the one behavior here that isn't a pure function of what
  the caller supplies per call. `cli/accept.go` gained `--confirm-destroys`
  (exit 1, both acceptance tiers). Full repo build/vet/fmt/test clean.
  Two real, named gaps deliberately not closed this session:
  `core.Ledger.FoldState`'s own tombstone-folding, and `ubx why`'s
  destroyed/already_absent rendering (the ledger already records it
  correctly; only the human-output rendering is deferred).
  docs/executor.md gained a session-3 addendum; ubiquex-docs'
  `cli/accept.mdx`/`cli/ship.mdx`/`cli/exit-codes.mdx` updated with real
  transcripts against the actual built binary (`mint validate`/`mint
  broken-links` clean). See §Destroys v1 (UBI-30) above and STATE.md for
  the full session writeup.
- 2026-07-18 — UBI-30 sessions 4-5: `FoldState`'s tombstone-fold, `ubx
  why`'s destroyed/already_absent rendering, a critical live-AWS
  `PlanResourceChange` bug found and fixed, UBI-30 closed. Session 3's two
  deferred gaps closed hermetically first (`core.shippedDestroyFold`
  folding a shipped destroy's address back to absent in both
  `FoldState`/`Fleet`; `ubx why`'s new `renderDestroys`/`destroyOutcome`).
  Then the live full-lifecycle finale hit a real bug no hermetic test had
  caught: `ApplyResourceChange` for a destroy, with no prior
  `PlanResourceChange` call, silently no-ops against a real, complex
  SDKv2 provider (`terraform-provider-aws` 6.54.0) instead of deleting
  anything — the "no separate plan phase" shortcut session 3 confirmed
  safe for create/modify does not extend to destroy. Fixed properly:
  `provider.Provider` gained a real `PlanResourceChange` method (both
  protocol versions), `shipDestroyNode` calls it unconditionally before
  every destroy `Apply` and threads the real `PlannedPrivate` through. A
  second, independent bug surfaced fixing the first:
  `encodeUnknownAwareDynamicValue` never produced a genuine top-level
  `cty.NullVal` for destroy's own literal-`null` signal — very likely the
  actual cause of a live `aws_sqs_queue_policy` destroy failure this same
  session had already hit and left unexplained. Both fixed; full repo
  build/vet/fmt/test clean. Live finale re-verified for real against the
  exact resources the bug had touched (a per-resource retry-budget
  exhaustion — 3 attempts — on the original failing proposal required a
  fresh one, a real hard limit, not a bug), plus a genuine `kill -9`
  mid-destroy (after the real AWS call had landed), reconciled correctly.
  Account left genuinely clean, verified via direct `aws sqs list-queues`,
  not just ubx's own status. docs/executor.md gained a session-5 addendum;
  docs/reliability-report.md gained a full UBI-30 section (real
  transcripts). See STATE.md for the full session writeup.

- 2026-07-18 — UBI-43 session 1: multi-provider stacks, docs-first.
  Recorded in docs/architecture.md §Multi-provider stacks (2026-07-17,
  design room); this session lands the resolver/executor design in the
  two documents that govern them. docs/resolver.md: type→provider
  inference against each declared provider's own schema (never
  name-prefix guessing), a `providers` config map riding the config
  loader UBI-19 already shipped (doesn't block on UBI-32's own cascade
  work), a rare explicit `"provider"` hint to break a genuine ambiguity,
  the dependency graph confirmed already provider-agnostic (checked
  directly, zero changes needed), destroys inferring their provider fresh
  against the currently-declared set rather than trusting history, and a
  staged `--source`/`--provider-version` retirement plan (deprecated, not
  broken, no cutover committed to a session number). docs/executor.md: a
  lazily-launched client pool keyed by `{source, version}`, the existing
  combined topo-walk confirmed unchanged (walks addresses, never
  consults provider), a provider launch failure classified as a per-node
  terminal error rather than a whole-walk abort (the existing
  `partially_applied` outcome, not a new failure category), and
  scan/status/fleet's own generalization to grouping by each resource's
  recorded provider. A real design tension found and resolved, not
  glossed over: docs/schema.md's own UBI-27 pinning had explicitly
  dropped a `provider` field from the IR node shape as "redundant" —
  true only under the single-provider invariant that amendment predates;
  reinstated (all three delta kinds, additive, no `schema_version` bump)
  in docs/schema.md's own new amendment. New
  docs/multi-provider-adversarial.md: seven required-outcome rows
  (ambiguous type with/without a hint, unowned type, provider launch
  failure mid-walk, a cross-provider `$ref` chain, `kill -9` between
  providers, per-provider freshness independence). Filed as its own
  ticket, **UBI-43**, team `ubiquex`. No code this session — see §Multi-
  provider stacks below and STATE.md for the full session writeup.

- 2026-07-18 — UBI-43 session 2: `core/resolver`'s own type→provider
  inference, real code, hermetic. `Resolve`'s signature changed from a
  single `SchemaInspector` to `[]DeclaredProvider` -- no separate
  single-provider code path left, just a provider set of size one. New
  `inferProvider`: exactly one owner wins; zero owners is `ErrUnknownType`
  (reused from the existing single-provider sentinel, not duplicated),
  naming every provider checked; more than one owner is `ErrAmbiguousType`
  unless an explicit `"provider"` hint names a real owner
  (`ErrProviderHintUnknown`/`ErrProviderHintDoesNotOwnType` for the two
  ways a hint can itself be wrong). The winner lands in every create/
  modify/destroy node's own `provider` field; destroys infer fresh
  against the currently-declared set, never inherited from history. A
  real bug found implementing, not assumed correct from the design alone:
  `resolveRef`'s own `IsComputed` check on a `$ref` target's attribute was
  reading a single globally-passed schema -- invisible until a `$ref`
  could cross a provider boundary for the first time; fixed to read the
  *referenced* sibling's own resolved provider schema, never the
  *referencing* entry's. New `core/resolver/multiprovider_test.go` covers
  docs/multi-provider-adversarial.md's rows 1, 2, 3, and 5; all 40
  pre-existing hermetic call sites updated mechanically via a new
  `singleProvider(s)` test helper, unchanged behavior, all still pass.
  `cli/resolve.go`'s own call site wraps today's single `--provider`/
  `--source` flow into the same one-element case -- no CLI-visible
  behavior change this session. docs/resolver.md gained a session-2
  addendum; docs/plan.md's own §Multi-provider stacks updated. Full repo
  build/vet/fmt/test clean, no regressions. See STATE.md for the full
  session writeup.

- 2026-07-18 — UBI-43 session 3: `core/executor`'s own client pool, real
  code, hermetic. New `ApplierPool` interface (`Get(ctx, source, version)
  (Applier, error)`, lazily launching, core/executor still never launches
  a provider itself) and `SingleApplierPool` (the trivial always-succeeds
  wrapper a single-provider stack needs -- today's CLI flow, unchanged).
  `Ship`'s own signature changed from `app Applier` to `pool ApplierPool`;
  `shipDriftRevert` untouched (single-provider by construction, resolves
  its one Applier from the pool once at the top); `shipChange`'s own
  per-node loop resolves each node's `Applier` via the pool, reading a
  new `changeNode.provider` field (nil falls back to the invocation's own
  `providerSource`, matching `SingleApplierPool`'s own single-entry
  answer) -- `shipCreate`/`shipModifyNode`/`shipDestroyNode` themselves
  completely unchanged, still just taking one plain `Applier` directly. A
  pool-lookup failure is a per-node terminal error (`continue`, never
  `return`) mirroring the loop's existing "blocked" case exactly. A real,
  named gap found implementing, not silently assumed covered:
  `providerConfig` stays one global value across every node regardless of
  provider -- correct for today's single-provider flow, real remaining
  work for the same config-wiring session already queued. New
  `core/executor/multiprovider_test.go` covers docs/multi-provider-
  adversarial.md's rows 4 (launch failure mid-walk, per-node terminal,
  `partially_applied`, a clean re-run), 6 (`kill -9` between providers --
  simulated via a first `Ship` call whose second provider never launches
  at all, then a second call with a fresh pool proving zero re-launch
  calls for the already-applied provider and exactly one fresh launch for
  the untouched one), and 7 (per-provider freshness independence -- one
  provider's own out-of-band drift refuses only its own node, a sibling
  against a different, undrifted provider lands normally). A new
  `fakeApplierPool` (real, multi-entry, per-key launch-failure scripting
  and call counters) stands in for `SingleApplierPool` in these tests,
  since a single-entry pool can't express "provider B fails while
  provider A succeeds" at all. All 35 pre-existing hermetic `Ship(...)`
  call sites updated mechanically via a scripted `sed` transform,
  unchanged behavior, all still pass. `cli/ship.go`'s own call site does
  the identical one-entry wrap -- no CLI-visible behavior change this
  session. docs/executor.md gained a session-3 addendum; its own "Out of
  scope" bullet updated from designed to fixed. Full repo build/vet/fmt/
  test clean, no regressions. See STATE.md for the full session writeup.

- 2026-07-18 — UBI-43 session 4: session 3's own named gap closed
  (`ApplierPool.Get` now returns `(Applier, json.RawMessage, error)` --
  each pool entry carries its own resolved config, never a single global
  blob), `.ubx/config`'s `[providers]`/`[provider_configs]` tables live-
  wired into `ubx resolve`/`ubx ship`, `--source`/`--provider-version`
  deprecation staging built. `Ship`/`shipChange` no longer take a
  `providerConfig` parameter at all; `shipCreate`/`shipModifyNode`/
  `shipDestroyNode` needed no changes (already took it as an explicit
  param). `SingleApplierPool` gained a second `config` parameter to
  match. New `cli/providerpool.go`: the concrete `ApplierPool`
  `.ubx/config`'s own new tables drive, lazily launching via an
  injectable `launchFunc` seam (real `provider.Acquire`/`Launch` in
  production, a fake in `cli/providerpool_test.go`'s own hermetic
  suite), refusing outright -- never silently substituting -- an
  undeclared source or a version that no longer matches the current pin
  (a proposal signed against one version, launched against a different
  one, is exactly the silent drift this project exists to catch).
  `cli/resolve.go`/`cli/ship.go` both branch on `cfg.Providers`: non-empty
  means a real multi-provider stack (resolve launches every declared
  provider eagerly, sorted, to fetch its own schema; ship's own pool
  launches lazily, only what a proposal's nodes actually need); empty
  falls back to today's exact single-provider flow, byte-for-byte
  unchanged. New `warnIfLegacyProviderFlagsGiven` (`cli/config.go`) warns
  to stderr, naming exactly which flags were ignored, when a stack with a
  real `[providers]` table also receives `--provider`/`--source`/etc.
  **Live-verified against the real built binary**, not just hermetic: a
  real `ubx resolve` -> `ubx accept` -> `ubx ship` chain against two
  genuinely separate provider subprocesses (`UBX_PROVIDER_MIRROR`, no
  network -- two `conformance-v6` fakeprovider copies, each wrapped in a
  small shell script setting its own `FAKEPROVIDER_RESOURCE_TYPE` before
  exec) confirmed correct per-node routing, the deprecation warning
  firing and naming the right flags, and a version-mismatch refusal that
  only blocks the one affected node while a sibling against a different
  provider proceeds independently. New `cli/providerpool_test.go`
  (8 tests: lazy launch/caching, per-source config with an explicit
  no-cross-contamination assertion, missing-config default, undeclared
  source, version mismatch, empty-version-uses-pinned, launch-failure
  propagation, `Close` closing only launched clients) and new
  `cli/config_test.go` cases (table parsing, nil-vs-empty-map, the
  deprecation warning's own silent/warning paths). docs/architecture.md's
  own status line updated (built, not "not yet built"); docs/resolver.md/
  docs/executor.md each gained a session-4 addendum; docs/multi-provider-
  adversarial.md's own "what this table doesn't cover" updated.
  ubiquex-docs updated same session (new `.ubx/config` tables are
  user-visible). Full repo build/vet/fmt/test clean, no regressions. See
  STATE.md for the full session writeup.
- 2026-07-18 — UBI-43 session 5: the last single-provider surface closed
  -- `ubx status --drift`/`ubx scan --all`'s own multi-provider fleet-
  grouping. `core.Ledger.Fleet` gained `FleetEntry.Provider
  *core.ProviderRef` (`core/fleet.go`'s new `nodeProviderForAddress`/
  `createNodeProvider`), read back with the identical "most recent wins,
  falls back to the shipped create's own recorded value" precedence
  `Lookup` already established -- nil for a resource this ledger only
  ever adopted or drift-recorded (`core/scan.go` never populates a
  provider). `resolver.inferProvider` exported as `InferProvider` for
  reuse (no behavior change to its own 3 existing call sites) -- the same
  type-to-provider inference a brand-new resource's own resolve already
  uses, now also driving a legacy Fleet entry with no recorded provider
  of its own. `cli/status.go`/`cli/scanall.go` both branch on
  `cfg.Providers`, mirroring session 4's own `resolve.go`/`ship.go`
  convention exactly (`warnIfLegacyProviderFlagsGiven`, empty falls back
  to today's single-provider flow unchanged). New
  `cli/schemainspector.go`'s `resourceTypeSchemaInspector`: a second,
  narrower `resolver.SchemaInspector` adapter backed directly by
  `executor.Applier.Schema()`'s own type-erased `map[string]any` (`HasType`
  real, `IsComputed`/`IsSensitive` harmless stubs -- confirmed sufficient
  since `InferProvider` never calls either), letting
  `declaredProvidersForInference` (`cli/providerpool.go`) reuse the SAME
  already-launched pool entries rather than launching every declared
  provider a second time. New shared classification helpers in
  `cli/status.go` (`classifyFleetEntry`/`unreadableNoLookup`/
  `unreadableProviderUnavailable`) so the single- and multi-provider walks
  report identically-worded outcomes. New `core/fleet_provider_test.go`
  (5 tests: Provider from a shipped create, from a modify -- current touch
  wins over stale history --, nil for an adopted resource, persistence
  through a later provider-less drift touch, from an accepted-but-
  unshipped destroy) and new `cli/multiprovider_fleet_test.go`
  (`resourceTypeSchemaInspector`'s `HasType`, `declaredProvidersForInference`'s
  lazy-launch-once/cached-on-reuse/launch-failure-propagation, via the
  same injectable `launchFunc` seam `cli/providerpool_test.go` already
  established). `classifyFleetEntry`'s own clean/drifted/unreadable
  classification is a pure extraction -- `cli/status_test.go`'s existing 8
  cases all still pass unchanged, proving it. **Live-verified against the
  real built binary**: the same `UBX_PROVIDER_MIRROR`-plus-wrapper-script
  technique session 4 used, this time with two distinct
  `FAKEPROVIDER_RESOURCE_TYPE` values (`aws_db_instance`/`time_static`)
  against a real two-entry `.ubx/config` `[providers]` table -- `ubx
  resolve` correctly inferred `hashicorp/time` with no `--provider`/
  `--source` given at all; `ubx status --drift` on a legacy-adopted entry
  (`Provider == nil`) correctly inferred `hashicorp/aws` and reported
  `clean`, then correctly reported `drifted` (exit 1) after a real
  out-of-band mutation; `ubx scan --all` against the same two-provider
  config correctly inferred and routed too. (A real `ubx ship` of the
  `time_static` create wasn't reachable -- `conformance-v6` mode has no
  `ApplyResourceChange` handler, session 4's own already-documented
  limitation; the routing/inference machinery under test here is
  independent of that gap.) docs/architecture.md's own status line
  updated (built, not "still not built"); docs/executor.md gained a
  session-5 addendum; docs/multi-provider-adversarial.md's own "what this
  table doesn't cover" updated to note the code now exists (formal
  per-row adversarial treatment of scan/status specifically still not
  built out). Full repo build/vet/fmt/test clean, no regressions.
- 2026-07-18 — UBI-43 session 6: the live finale, real infrastructure, no
  code changes (this session found and documented, rather than fixed,
  three real gaps -- see below). A real `aws_sqs_queue` +
  `google_service_account` in one intent file (real AWS account, real GCP
  project `personal-273114`, billing-enabled, already used by UBI-21's
  own live gcpaudit verification), a genuine cross-provider `$ref` (the
  service account's `description` holding the queue's own real `Computed`
  `arn`), resolved with no `--provider`/`--source` at all -> accepted ->
  shipped as ONE signed proposal, both resources reaching `applied`.
  Verified independently against both clouds' own APIs, not just `ubx`'s
  own report. **A real plan change, found live, not silently absorbed**:
  the originally-chosen second provider (`hashicorp/time`, an earlier
  session's own `AskUserQuestion` decision) was swapped for a real second
  cloud provider (GCP) after a direct empirical probe against the real
  `hashicorp/time` binary found `time_static`'s `ReadResource` returns
  every attribute but `id` as null when given only the universal
  `{"id":...}` lookup `core.DeriveLookupFromResult` always derives --
  drift detection is structurally impossible for this type, not merely
  "unattributable" as the earlier decision anticipated. Flagged to the
  user before spending real infrastructure time on a premise just found
  false; the user chose GCP. **Two further real GCP findings, live**:
  `google_service_account`'s own drift detection works correctly through
  `ubx`'s ordinary automatic lookup, but its Cloud Audit Log entries name
  the resource by a numeric `unique_id` never present in its own observed
  state, so its real, correctly-detected drift is currently
  unattributable (extends the already-documented `google_secret_manager_secret`
  gap to service accounts too). `google_pubsub_topic` (previously proven
  to attribute correctly) has the opposite problem -- its own minimal
  `{"id":...}` lookup can't observe a real `labels` mutation at all, and,
  more seriously, a real `ubx ship` destroy of one reported `destroyed`
  in the ledger's own reconciliation record while the real GCP topic
  stayed live -- found only because this session verified independently
  rather than trusting the report; the real leaked topic was deleted by
  hand, and the finding filed as **UBI-44** (`ubiquex` team) rather than
  patched under time pressure, since it's a `core/executor`
  `reconcileDestroyLoop` correctness gap, not a fixture quirk.
  `google_project_iam_custom_role` was the one type found, live, with
  neither gap -- added to the same stack to complete the demonstration
  honestly: a real out-of-band `permissions` mutation, correctly detected
  by `ubx status --drift` with zero manual assistance, correctly
  attributed to a real `UpdateRole` event on the first attempt. `ubx why`
  on both the queue and the custom role shows the complete, honest
  biography of each, real attribution included. Every resource this
  session created (four addresses total) was decommissioned via one real
  `ubx ship` destroy proposal; verified independently afterward that both
  accounts are genuinely clean (the one exception being the `google_pubsub_topic`
  finding above, hand-cleaned). `conformance/registry.go`'s own
  `google_pubsub_topic` entry gained a note recording the destroy-side
  finding. docs/executor.md gained a session-6 addendum; docs/architecture.md's
  own multi-provider section marked the live finale done. ubiquex-docs
  gained a new guide, `guides/multi-provider-flow.mdx` (real transcripts
  throughout, including the mid-guide provider swap and both GCP
  findings); `mint validate`/`mint broken-links` both clean. UBI-43
  closed in Linear this session, arc complete (sessions 1-6).
- 2026-07-18 — UBI-32 Arc A session 1: config cascade + formats, design,
  real code, live-verified, ubiquex-docs -- all in one session. New arc,
  unparked by founder decision after UBI-43 closed; runs as two sub-arcs
  per the ticket, config first (Arc A, done this session), `LedgerStore`/
  remote stores/addressing second (Arc B, not started). Cascade
  discovery upgraded from UBI-19's nearest-file-wins to a per-key,
  editorconfig-style merge across every `.ubx/config*` from cwd to the
  filesystem root, three formats (HCL canonical -- now `ubx init`'s own
  default, a real behavior change -- TOML forever, YAML strict-only), a
  new provenance view (`ubx config`). A real design bug found and fixed
  before any code existed to hide it: docs/architecture.md's own
  Multi-provider stacks section had sketched
  `providers { "hashicorp/aws" = "6.60.0" }` as HCL — parsed directly
  against `hclsyntax` this session, it's a hard parse error (block
  arguments can't be quoted); corrected to
  `providers = { "hashicorp/aws" = "6.60.0" }`, an attribute holding an
  object-constructor expression, confirmed to parse. A second real gap
  found live and fixed the same session: `ubx init`'s own overwrite
  check only compared the exact target filename, so it could have
  silently shadowed an existing config under a different format's name;
  now checks discovery's own winner instead. See §Config cascade +
  formats below for the full session and STATE.md for the empirical
  findings (yaml.v3's own resolver, BurntSushi's `Undecoded()` not
  applying to generic-map decode).
- 2026-07-19 — UBI-32 Arc B session 1: `LedgerStore` interface extracted
  from `core.Ledger`'s own actual filesystem operations (confirmed by
  reading the code directly: every chain-walking read goes through
  `Head()`+`Read()`, never a directory listing), a git-directory
  reference implementation (zero behavior change, full pre-existing
  suite passes unmodified), and a new `ledgerstore` package implementing
  the same interface against `gocloud.dev/blob` (s3 wired and
  conformance-tested hermetically and live against real S3; gs/azblob
  tried and deliberately backed out after `go mod tidy` showed dozens of
  new transitive dependencies apiece — real evidence the harness doesn't
  make them cheap, not silently forced through). A real gap found and
  fixed for both stores: `Append`'s duplicate check used to treat a
  crash between writing a proposal object and advancing the head as an
  unrecoverable duplicate; it now resumes correctly. `Head`/`AdvanceHead`
  is a genuine compare-and-swap for the blob store (immutable
  parent-keyed edge objects, S3's own native `If-None-Match` conditional
  write under the hood), not an optimistic overwrite. A first CLI slice
  wired (`.ubx/config`'s new `[ledger]` table, `ubx resolve`/`ubx accept`/
  `ubx ship --stack`), live-verified end to end against real S3; the rest
  of the CLI surface still opens git-local unconditionally, named as
  follow-up. See §Ledger stores below for the full session and STATE.md
  for the empirical findings (gocloud.dev/blob's own `IfNotExist`
  semantics, the live-only lock-timeout tuning).
- 2026-07-19 — UBI-32 addendum: the config cascade (Arc A) gained an
  explicit stop rule it never had — `root = true` (editorconfig's own
  precedent), else the git repo boundary, else `$HOME`/filesystem root,
  checked in that order at every directory the upward walk visits.
  `~/.ubx/config` landed the same day, structurally outside the cascade:
  allowlist-only (`init_format` today), every other key a hard error,
  since a project-truth key leaking in from a per-user file would break
  "the same checkout resolves identically on every machine." A real
  subtlety found by a hermetic test before shipping: `$HOME` coinciding
  with the cascade's own ceiling could have double-read and wrongly
  rejected a legitimate project key; fixed by having the user-global
  loader skip any file the cascade walk already consumed. `ubx config`
  now reports which ceiling rule fired and where. See §Ledger stores'
  own addendum entry below for the full session.
- 2026-07-19 — UBI-44 (co-scoped with UBI-42): a real `google_pubsub_topic`
  destroy found live (UBI-43 session 5) reporting `destroyed` while the
  real topic stayed alive — diagnosed for real (not assumed a UBI-30
  repeat: `PlannedPrivate` was empty in every attempt, including the one
  that worked), root cause isolated via Cloud Audit Logs to an incomplete
  `PriorState` this session's own universal lookup never fills. Fixed
  structurally: a provider's claimed destroy success is never sufficient
  by itself — `shipDestroyNode`'s own post-destroy read-back is now
  universal, not just for ambiguous `Apply` results, with a new
  `destroyReconcileBackoffSchedule` (~64s, co-scoped with UBI-42's own
  named retry-budget gap) so the fix doesn't turn real propagation lag
  into a false failure. Live re-run against real GCP confirms the exact
  scenario that lied now honestly reports `failed` instead. See §A
  destroy that lied below for the full session.
- 2026-07-19 — UBI-32 Arc B session 3: the PR-acceptance ceremony built
  exactly as designed, and the rest of the CLI surface (`revert-plan`/
  `writeback`/the MCP surface) wired onto `.ubx/config`'s own `[ledger]`
  table — this arc's own remainder list, closed. `acceptFromMerge` opens
  the stack's configured `LedgerStore` after (never before) its
  unchanged git/GitHub verification succeeds; no new mirroring mechanism
  needed, since `Ledger.Append`'s own CAS write was already
  store-agnostic. Live-verified against a real merged GitHub PR and real
  S3, then fully reverted/cleaned up. A real, if narrow, divergence
  found and fixed along the way: the MCP surface's own
  `computeWhyJSON`/`computeStatusJSON`/`computeScanJSON` had quietly
  stopped being literally shared code with `ubx why`/`status`/`scan`'s
  own CLI RunE the moment those grew their own `[ledger]`-aware open in
  an earlier session — the three MCP-only functions were the actual
  last unwired readers, not what the prior session's own remainder list
  named. `ubx propose` was also named in that list but never touches a
  ledger at all — removed from the remainder as a correction, not
  wired. See §PR-acceptance ceremony (docs/architecture.md) for the full
  design-to-build story and STATE.md for the full session.
- 2026-07-27 — UBI-41 session 1: intent provider + md medium, design
  only, no code. docs/intent-provider.md (new): the transcription-only
  boundary applied for real for the first time (LLM emits an `intent/v1`
  draft, never resolves/computes/touches a ledger or provider); the
  `Adapter` interface (Claude first — real, current API surface checked
  before writing the design down, not assumed; OpenAI/Gemini/local
  follow, each earning "supported" via the conformance suite); the
  `[intent]` config table (`adapter`/`model`/`key_ref` — never material,
  cascade content like `[providers]`; Gemini's own API-key-vs-Vertex
  auth split settled explicitly, per the design-room comment on the
  ticket); the ambiguity-as-visible-content design center
  (`assumptions`/`defaults`/`questions`, reviewable and signed as part
  of the proposal, never a silent choice — and never a resolver-side
  enforcement gate either, a considered-and-rejected alternative named
  explicitly); redaction-at-capture for secret material pasted into a
  doc (a genuinely different, pattern-based mechanism from UBI-23's
  schema-driven redaction, since prose has no schema to consult); the
  conformance-suite design (golden md→intent fixtures with per-fixture
  assertion functions, not a byte-exact golden diff — LLM output isn't
  deterministic the way a tfplugin provider's is, a real and checked
  divergence from the existing `conformance/` harness's own discipline;
  fixture #1 is the payments doc from this arc's own design transcript).
  docs/schema.md gained the matching amendment: `Proposal.Intent`'s new
  additive `assumptions`/`defaults`/`questions` fields, and two new
  `intent.sources[].kind` values (`document`, `intent_provider`) — no
  `schema_version` bump. docs/intent-provider-adversarial.md (new): the
  required-outcome program named in the ticket's own handoff (ambiguous
  sizing, contradictory requirements, secret material pasted into a
  doc, unknown resource types, cost ceiling exceeded, adapter
  unavailable/timeout, invalid JSON thrice) plus an eighth row this
  session added — prompt injection embedded in doc content, whose
  required outcome is deliberately structural (the trust chain bounds
  the blast radius) rather than a claimed detection capability.
  docs/architecture.md gained a new headline section cross-linking the
  design doc, and its own stale "`intent` (reserved... not yet
  implemented)" config note corrected to point at the real design.
  Implementation sized at 2-3 further sessions (named in
  docs/intent-provider.md's own "Implementation slices"): interface +
  Claude adapter + conformance harness; the md pipeline (`ubx propose
  --from-doc`) + ambiguity UX, live-verified against the real Claude
  API; docs + polish. See STATE.md for the full session account.
- 2026-07-27 — UBI-41 session 2: interface + Claude adapter + conformance
  harness, real code, hermetic. New `intentprovider` package (`Adapter`,
  `DraftWithRetry`'s own retry-with-errors/hard-fail contract,
  `IntentDraftJSONSchema`, `PopulateSources`); `core.Intent` gained the
  three additive ambiguity fields session 1's own schema.md amendment
  pinned; `intentprovider/claude` (the real adapter, new dependency
  `anthropic-sdk-go`); `intentprovider/conformance` (the fixture-runner
  harness, fixture #1 embedded via `go:embed`). Hermetic tests throughout
  (a scripted fake `Adapter`, no network); a `UBX_TEST_SLOW=1`-gated live
  test wires the real adapter through both a direct smoke test and the
  full conformance suite. One deliberate scope deferral, named rather
  than silently made: `[intent]` config-cascade wiring was NOT built this
  session (no consumer yet — deferred to the md-pipeline session). Two
  real findings: Claude's own structured-output constraint forces a
  resource's `config` to be a JSON-encoded string rather than a nested
  object (not live-verified this session — no credentials in the build
  environment, flagged explicitly to confirm on the first real live run);
  and a real bug found BY running the live test with no credentials
  present — `classifyError` originally lumped "no credentials resolvable
  at all" under a generic network-error bucket, fixed the same session.
  See docs/intent-provider.md's own "Session 2" subsection and its own
  wedge subsection above for the full account, and STATE.md for the
  complete session narrative. Full repo build/vet/gofmt/test clean
  throughout. Session 3 (the md pipeline) is next.
- 2026-07-27 — UBI-41 session 3: live-validated session 2's own unverified
  structured-output design decision FIRST (a real credential obtained;
  confirmed correct on the first live call — `resources[].config` really
  does round-trip as a JSON-encoded string), then built the md pipeline:
  `[intent]` config wiring (`cli/config.go`'s `IntentConfig`,
  `cli/configcascade.go`'s known-keys extension,
  `cli/intentadapter.go`'s `buildIntentAdapter`), `ubx propose
  --from-doc` (a new mode on the existing `propose` verb, disambiguated
  from its pre-existing hash-a-resolved-proposal mode),
  redaction-at-capture (`intentprovider/redact.go`, pattern-based, run
  before any network call), and ambiguity-content rendering
  (`cli/intentrender.go`). Three real findings, all from actually running
  against the real API, none assumed: (1) the model's own first live
  response put the literal word "placeholder" in an assumption's text —
  root-caused to the system prompt's own wording (describing the `$ref`
  marker "as a placeholder") priming that exact word; fixed by rewording
  the prompt and adding an explicit no-generic-filler instruction; (2) a
  more serious bug — a real draft's `intent.assumptions`/`.defaults`
  described concrete resource decisions in full detail while
  `resources[]` was completely empty, because nothing required the two
  to agree; fixed with a new hard validation rule
  (`len(resources)==0 && len(destroys)==0` is now a rejection) plus a
  stronger system-prompt check-before-you-finish instruction, re-verified
  live twice after the fix; (3) a genuinely surprising, non-bug finding —
  one live run returned a real safety-classifier refusal (category
  "bio") on an entirely innocuous database-provisioning doc; the
  adapter's own existing refusal handling worked exactly as designed
  (a clear, named, non-retried error), named honestly as a real reliability
  data point rather than smoothed over. Full live finale, twice: the real
  payments fixture doc through the real `ubx propose --from-doc` binary
  with a real `.ubx/config` `[intent]` table (`key_ref.env` naming a
  deliberately non-default env var, proving the config-cascade
  dereferencing path itself, not just the SDK's own ambient fallback),
  producing a complete, correctly-provenanced draft; a second run with a
  real-shaped (AWS's own public example) secret injected into the doc
  confirmed redaction-at-capture survives a genuine round trip (a direct
  `grep` of the written draft found zero occurrences of the secret, not
  assumed from the warning alone). Hermetic tests throughout (fake
  adapters via a package-level DI seam, `buildIntentAdapter`, mirroring
  `configSearchStartDir`'s own precedent); full repo
  build/vet/gofmt/`test -race` clean. See docs/intent-provider.md's own
  "Session 3" subsection and STATE.md for the complete narrative. No
  ubiquex-docs update this session (deferred to slice 3, "docs +
  polish," per docs/intent-provider.md's own Implementation slices).
- 2026-07-28 — UBI-41 session 4: ubiquex-docs (two new guides —
  `guides/md-medium.mdx`, `guides/md-authoring-conventions.mdx`, a new
  "AI-Assisted Authoring" nav group — plus `cli/config.mdx`'s new
  `intent` section and `cli/propose.mdx`'s `--from-doc` documentation,
  every transcript real), `docs/intent-provider-conformance-report.md`
  (Claude's own real published numbers: 5 of 6 fixture-suite runs
  passed, the one failure named and explained, three real live findings
  written up honestly), `mint validate`/`mint broken-links` both clean.
  **UBI-41 closed in Linear** — chat (rides this arc's own `Adapter`
  interface, its own session) and OpenAI/Gemini/local (parked on the
  roster) both named explicitly as what stays open. See
  docs/intent-provider.md's own "Implementation slices" (slice 3 marked
  built) and STATE.md for the full session account.
- 2026-07-28 — UBI-46: chat medium — `ubx chat`, dialogue capture
  (`dialogues/<hash>.dlg.json`, top-level, sibling of `ledger/` per
  docs/architecture.md's own "Ledger stores" authoring-mediums split),
  and `ubx why --dialogue`, riding UBI-41's `Adapter`/`DraftWithRetry`
  interface unchanged. Four new adversarial rows (secret mid-conversation,
  contradictory turns/later-wins, abandoned session/no orphan capture,
  dialogue tampering post-pin — docs/intent-provider-adversarial.md rows
  9-12). Live-verified against the real Claude API: a real two-turn
  payments-stack refinement ("like staging but smaller" then "make it
  multi-az") produced a draft whose provenance chained to the real
  captured dialogue file, accepted as a real proposal, and walked back
  end to end with `ubx why --dialogue` rendering the actual conversation
  verbatim. A separate real contradiction probe (`db.t3.large` then
  "actually, use db.t3.micro instead") confirmed later-turn-wins with the
  override named in `intent.assumptions`. Both repos updated same
  session; **UBI-46 closed in Linear**. See docs/intent-provider.md's
  new "Amendment: the chat medium" section and STATE.md for the full
  account.
- 2026-07-28 — UBI-33/34 session 1: SDK program design, docs-first, no
  code. docs/sdk.md (new): the multi-language contract (golden `intent/v1`
  fixtures as the spec, enforced as byte-identical-after-canonicalization);
  the `sdk/` monorepo layout (`conformance/`, `codegen/` shared IR model,
  `ts/`); the describe-only `@ubx/sdk` runtime (`stack`/`resource`/
  `secret`/`cross`/`intent`, `Computed<T>` as a branded, never-coercible
  reference); the codegen design (provider schema → a language-neutral IR
  model whose only name is the provider's real wire attribute name → per-
  language templates); `ubx sdk gen`, local and offline-after-generation,
  never publishing bindings. The hermetic evaluator was decided
  **empirically** — Node's `--permission` model, Deno, and `isolated-vm`
  were each actually probed against the real no-net/fs/env/clock
  requirement in this session's own environment; Node disqualified (no
  network/env gate exists at any flag combination); Deno chosen (closes
  fs/env/net by default with zero flags; one real gap found and closed —
  dynamic remote `import()` bypasses `--deny-net` entirely, needs `--no-
  remote`); `isolated-vm` recorded as the stronger-but-costlier fallback
  (strongest structural isolation, but its native build script didn't run
  under this session's own npm lockdown, and no native TypeScript). Clock/
  random, unblocked by all three (a JS-engine built-in, not a host
  resource any of them gates), closed by an eager override plus
  `core.DoubleRun` reused unchanged as the backstop, run across two real
  subprocesses. A six-row required-outcome adversarial table lives inside
  docs/sdk.md itself. docs/architecture.md gained a matching cross-linking
  headline section. Implementation sized at 7 slices toward a real
  TypeScript payments program converging with the existing md-medium
  fixture's own resolved shape — see docs/sdk.md's own "Implementation
  slices" and STATE.md for the full session account including the actual
  probe output.
- 2026-07-28 — UBI-33/34 session 2: slices 1–3 built — `sdk/codegen/ir`
  (real `provider.Schema` → IR translation, reusing `ctyjson.
  UnmarshalType`), `sdk/codegen/templates/ts` (idiomatic TS `Config`/
  `Attrs` interfaces + a runtime `ResourceBinding` descriptor), `ubx sdk
  gen` (new `cli/sdk.go`; `[providers]`-driven, `sdk/generated/` default
  output, one file per source), and `@ubx/sdk`'s own runtime
  (`stack`/`resource`/`secret`/`cross`/`intent`, a `Computed<T>` Proxy, a
  declarative `FieldMap` serializer -- refined from the original
  per-binding `toConfig()` sketch after actually building it). Two real
  bugs found by tests asserting on real output (nested blocks silently
  excluded from `Config`; a scalar-map field's own keys wrongly run
  through wire-name translation), both fixed with regression tests;
  docs/schema.md's own stale `$secret` inner-shape example corrected in
  passing. Live-verified against the real, already-cached
  `hashicorp/aws@6.54.0` (1,682 types, zero errors) and
  `hashicorp/time@0.9.2` (a hand-written program against the real
  generated output, `deno check`/`deno run` clean, under the exact
  locked-down flag set docs/sdk.md's evaluator section commits to).
  `go build/vet/test`, `gofmt -l .`, `deno test`/`deno check` all clean.
  See docs/sdk.md's own "Implementation slices" section and STATE.md for
  the full account.
- 2026-07-28 — UBI-33/34 session 3: slice 4 built -- the evaluator
  harness, real `deno` subprocesses, all five in-scope adversarial rows
  (1, 2, 2b, 4, 5) confirmed against them. The session's own central
  finding: session 1's `--allow-read` question, re-litigated and
  corrected twice over -- session 1's own original speculation (a narrow
  carve-out needed) AND this session's own first, too-easy re-probe
  (assumed none needed, based on a fixed, non-parameterized script) were
  both wrong. Five isolated probes found the real rule: Deno's read
  permission gates a dynamically-computed import specifier, never a
  literal one, regardless of directory -- fixed by generating a fresh
  runner script per evaluation with the entry file's own absolute path
  baked in as a literal import (safe because `stack()`'s own deferred-
  execution design, slice 3, means import-time ordering never touches
  program code). Net: the shipped flag set needs zero `--allow-read`
  carve-out, stronger than either prior guess. `core.CanonicalJSON`/
  `CanonicalJSONBytes` (new, factored out of `canonicalProposalBytes`'s
  own JCS logic); `sdk/ts/evaluator/guards.ts` (new, harness-only, the
  eager `Date`/`Math.random` override); `sdk/ts/embed.go` (new
  `tsassets` package, embeds the harness's own TS assets into the `ubx`
  binary); `sdkeval` (new top-level Go package, `core.DoubleRun` wired
  across two real subprocess launches). Row 5 needed its own correction
  too: reusing `intentprovider.IntentDraftJSONSchema` (this document's
  own original plan) turned out wrong -- a deliberately different,
  incompatible shape -- corrected to strict-unmarshal against the real
  `core/resolver.IntentFile` Go type instead, plus a new, real Go-side
  enforcement of the "op is always create" decision. All required rows
  live-verified, including a `Deno.pid`-leaking fixture proving
  `core.DoubleRun`'s own backstop genuinely catches what the eager guard
  structurally can't see. `go build/vet/test`, `gofmt -l .`, `deno
  test`/`deno check` all clean (20 new Go tests in `sdkeval`, 6 in
  `core`, 5 new `deno test` cases). See docs/sdk.md's own "Slice 4:
  built" section and STATE.md for the full account.
- 2026-07-28 — UBI-33/34 session 4: slices 5-7 built -- `ubx resolve
  --from-code` (CLI wiring only, no resolver changes), `sdk/conformance`
  (a real golden case + a real, ongoing Go regression test), and a real
  live convergence finale. `intent.sources` gets a single `"document"`
  entry (`sdkeval/provenance.go`'s new `stampDocumentSource`, hashing the
  entry file Go-side since the sandboxed evaluator can't) -- a real
  simplification from the original `"sdk"`/`"sdk_evaluator"` kind-pair
  sketch, reusing the md medium's own existing kind since code has no
  LLM-adapter analog worth a second entry. The live finale: no committed
  "golden" transcript existed from the md medium's own prior sessions
  (drafts are ephemeral, confirmed by checking), so this session ran `ubx
  propose --from-doc payments.md` against the real Claude API fresh, got
  a real drafted `aws_db_instance` (`db.t3.small`, 20 GiB,
  `payments_admin`, no `$ref` to staging -- the intent provider has no
  ledger access, confirmed empirically), resolved it for real, authored a
  TypeScript program with the identical values (copied from the real
  output, not invented), evaluated and resolved that too, and confirmed
  both resolved `delta.creates[]` are byte-identical via
  `core.CanonicalJSON` -- the strongest available proof "the SDK is a
  producer of intent/v1, nothing more" actually holds. **UBI-34 closed in
  Linear** (TypeScript complete, all 7 slices built/tested/live-verified);
  **UBI-33 stays open** (Go/Python unstarted). `ubiquex-docs` gained
  `cli/sdk-gen.mdx`, a new "Authoring in TypeScript" section on
  `cli/resolve.mdx`, and a full `sdk/index.mdx` rewrite from its "not yet
  released" placeholder -- every example real; `mint validate`/`mint
  broken-links` clean. See docs/sdk.md's own "Slices 5-7: built" section
  and STATE.md for the full account, including the real transcripts.
- 2026-07-28 -- UBI-47 session 1: diagram medium design, docs-first, no
  code. docs/diagram-medium.md (new): the canonical D2 subset (verified
  empirically against the real `oss.terrastruct.com/d2` library -- a
  tempting custom-key type-annotation design found and rejected before
  shipping, since D2 silently treats any unrecognized key as a nested
  child shape rather than erroring; D2's own `class:`/`classes: {}`
  keyword is the real, working mechanism, and `d2format.Format` is
  confirmed genuinely idempotent, the property `render --check`'s own
  byte-compare needs); the lossy-medium rule generalized from prose to
  structure (topology authors in, attributes render out, never in); the
  cross-stack grammar (`@stack.type.name` as a D2 label, never a key --
  the same nesting trap the custom-key finding already caught, applied
  again); type inference via `resolver.InferProvider` (UBI-43) reused
  completely unchanged; ambiguity-as-content reusing UBI-41's own wire
  fields (`assumptions`/`defaults`/`questions`) for a deterministic
  parser's own structural ambiguity, with no LLM anywhere in this
  medium's path at all. docs/schema.md gained a real, additive amendment
  this design found necessary, not invented for convenience:
  `ResourceIntent.DependsOn`, a topology-only dependency signal routed
  into the resolver's existing dependency graph (cycle detection needs
  no new code as a direct result). A second, genuinely different hash
  from `content_hash` is named explicitly: the topology hash
  (`core.CanonicalJSON` over resolved `resources[]`+`depends_on`,
  styling excluded) for "did the meaning change," vs. `content_hash`
  (raw bytes, styling included) for tamper-evidence -- conflating the
  two was a real wrong shortcut caught before it became load-bearing.
  docs/architecture.md gained a matching cross-linking headline section;
  the "Deferred" list's "diagrams" line struck (designed now, code is
  session 2+ work). A seven-row adversarial table lives in docs/diagram-
  medium.md itself, most rows reusing an existing resolver-side
  mechanism unchanged. Implementation sized at 7 slices toward a real
  `.d2` payments stack converging with the same golden values the md
  medium and the SDK arc's own TypeScript program already converged on
  -- see docs/diagram-medium.md's own "Implementation slices" and
  STATE.md for the full account including the real D2-library probe
  output.
- 2026-07-28 -- UBI-47 session 2: slices 1-2 built -- the topology
  parser (new `diagram/` package) and `ResourceIntent.DependsOn` (`core/
  resolver`), both real code, hermetically and end-to-end tested.
  `DependsOn` landed exactly as designed: unions into the same
  dependency graph `$ref`/`$cross` scanning already builds, reusing
  `ErrRefNotFound`/`ErrRefToDestroyTarget` verbatim. The parser
  (`d2compiler.Compile` -> classify -> translate -> `resolver.
  IntentFile`) landed with one real, honest correction found while
  building it: a topology-only edge into a cross-stack reference node
  cannot express a real `$cross` marker at all in v1 (no config
  attribute to hold it, no ordering-based substitute the way DependsOn
  provides intra-stack) -- a genuine structural limit, not a bug; it
  becomes a visible, non-blocking note instead. Two end-to-end tests
  confirm real `Parse` output resolved through the real, unmodified
  `resolver.Resolve` actually triggers `ErrCycleDetected`/
  `ErrDuplicateResource` -- the adversarial table's own row 1/3 claims
  proven, not just asserted. `go build/vet/test`, `gofmt -l .` clean (8
  new tests in `core/resolver`, 16 new in `diagram`). See docs/diagram-
  medium.md's own "Slices 1-2: built" section and STATE.md for the full
  account.
- 2026-07-28 -- UBI-47 session 3: slice 3 built -- `ubx propose
  --from-diagram <file>.d2 --stack <stack>` CLI wiring (`cli/
  propose.go`), matching `--from-doc`'s own shape exactly: read, parse,
  populate a single `"document"`-kind sources entry (`intentprovider.
  HashDocument`, reused unchanged), render ambiguity content
  (`cli/intentrender.go`'s existing `renderAmbiguity`, zero new rendering
  code -- it already operates on the same `*resolver.IntentFile` type
  `diagram.Parse` returns), write the draft. No corrections to session
  2's own design -- the parser, `DependsOn`, and the `$cross`
  structural-limitation note all wired through unchanged. Two-step, not
  one-step: stops at a written draft like `--from-doc`, never
  auto-resolves, since a diagram parse can produce real ambiguity needing
  a human-review checkpoint first, unlike `--from-code`'s own one-step
  shape. No legacy single-provider fallback (matching `ubx sdk gen`'s own
  precedent -- both post-UBI-43 features); a new standalone
  `loadDiagramProviders` helper rather than a `cli/resolve.go` refactor,
  closing each provider client immediately after fetching its schema
  (confirmed safe via `newSchemaInspector`'s own no-live-client
  implementation). Five hermetic CLI tests (`cli/
  propose_from_diagram_test.go`), including a real end-to-end run via the
  `UBX_PROVIDER_MIRROR` seam; also live-verified by hand against a real
  built binary. `go build/vet/test`, `gofmt -l .` clean. See docs/
  diagram-medium.md's own "Slice 3: built" section and STATE.md for the
  full account.
- 2026-07-28 -- UBI-47 session 4: slice 4 built -- `diagram.Emit`
  (`diagram/emit.go`, new) plus `ubx render --stack <stack> [--out
  <path>] [--check]` (`cli/render.go`, new): the render half of the
  medium, `FoldState`/`Fleet` walk -> D2 source text -> `d2parser.Parse`
  -> `d2format.Format` for the canonical byte form, one flat top-level
  node per live resource. A real, load-bearing gap found while building
  this slice: `resolution.inputs[].pinned_head` alone wasn't enough to
  draw a `$cross` edge from the correct node -- a `cross_stack_pin`
  entry's own `resource` field has always named the neighbor's address,
  never the local resource that made the reference, and resolution
  inputs across a whole resolve batch were flattened with no back-
  reference at all. Fixed at the source: `core.ResolutionInput` gained a
  new, additive `From` field, threaded through
  `resolveValue`/`resolveCross` from `resolveOnce`'s own per-resource
  loop (docs/schema.md's new "Amendment: `ResolutionInput.From`"). Real,
  deliberate rendering decisions: synthetic `r0`/`r1`/... D2 keys (never
  the resource's own name, since two different-typed resources can
  legally share a name, and a dotted `type.name` key would collide with
  D2's own container-nesting separator); attribute annotations via
  `tooltip:`; no per-resource cost annotation (checked directly -- no
  such field exists anywhere in the ledger, `CostDelta` is proposal-level
  only and presently always `"0"`); reference nodes deduplicated by
  neighbor address. `TestEmitD2_RoundTripsThroughParse` proves the
  medium's own "render/parse share one convention, not two" claim for
  real, not just per-direction. Nine unit tests (`diagram/emit_test.go`)
  plus ten CLI tests (`cli/render_test.go`, a real resolve -> accept ->
  ship pipeline against the hermetic `fakeprovider` binary), including a
  real two-ledger cross-stack scenario proving the `From` fix end to end.
  `go build/vet/test`, `gofmt -l .` clean. **A real, costly mistake made
  and corrected this session**: initial by-hand live verification ran
  `ubx ship` against the real, already-credentialed `hashicorp/aws`
  provider instead of the hermetic `fakeprovider` mirror, creating three
  real AWS VPCs and starting a real RDS instance in the user's live
  account -- caught by checking real AWS state directly, all four
  resources confirmed and deleted with the user's explicit go-ahead; a
  standing feedback memory now records "never `ubx ship` against a real
  provider for verification purposes." See docs/diagram-medium.md's own
  "Slice 4: built" section and STATE.md for the full account, including
  the incident.
- 2026-07-28 -- UBI-47 session 5: slice 5 built -- `diagram.Topology`
  (`diagram/topology.go`, new), the "topology hash" concept's own first
  real code: `core.CanonicalJSON` over `resources[]` (type, name, op,
  depends_on) + stack, sorted by `(type, name)` internally, excluding
  `intent.summary`/`sources`/ambiguity/`config` entirely. Conformance
  fixtures, `payments` as fixture #1, both directions, deliberately split
  across two packages: the parse direction (new `diagram/conformance/
  golden/payments.d2` <-> `payments-topology.json`, tested in new
  `diagram/conformance/runner/`) is fully self-contained -- no
  subprocess, no real provider binary, since `diagram.Parse`'s own type
  inference only ever calls `SchemaInspector.HasType`; the render
  direction (the identical topology shipped for real through the
  hermetic `fakeprovider` binary, emitted, compared against new
  `diagram/conformance/golden/payments-rendered.d2`) lives in
  `cli/render_conformance_test.go` instead, since `Emit` needs a real,
  shipped `Fleet` entry, which needs the full `core/executor.Applier`
  adapter `cli/stateadapter.go` already owns correctly -- reimplementing
  a second copy elsewhere would risk a real divergence for no benefit.
  **A real, deliberate departure from every other medium's own
  "payments" fixture**: both golden `.d2` files use `fake_widget`
  throughout, never `aws_vpc`/`aws_db_instance`, per this session's own
  explicit "hermetic only" instruction -- a direct, standing consequence
  of session 4's own real AWS incident; reconciling this fixture's own
  values with the `aws_*` golden values every other medium already
  converged on is explicitly named as slice 6's own job, not this one's.
  **The standing ship-verification rule from session 4's own incident,
  now codified in CLAUDE.md and docs/prompts.md** -- both gained a line
  naming the rule directly, so a future session (or a different agent
  entirely) reads it automatically rather than depending on a memory
  file carrying forward. Eight new tests
  (`diagram/topology_test.go` x5, `diagram/conformance/runner` x2,
  `cli/render_conformance_test.go` x1). `go build/vet/test`, `gofmt -l .`
  clean. See docs/diagram-medium.md's own "Slice 5: built" section and
  STATE.md for the full account.
- 2026-07-28 -- UBI-47 session 6: slice 6 built -- the live finale, real
  end to end, closing the arc. Convergence leg: a one-resource diagram
  (`aws_db_instance` named "payments"), proposed and resolved for real
  against the real, cached `hashicorp/aws@6.54.0` schema (never shipped,
  per doctrine) -- `delta.creates[0]` canonicalized and checked, not
  eyeballed, against the SDK arc's own committed golden value:
  `name`/`stack`/`type`/`provider` byte-identical, `config` the one
  honest, structural, expected difference (empty vs. real attribute
  values) -- exactly the lossy-medium rule made concrete, not a gap.
  `diagram.Topology` (slice 5, reused unchanged) confirms the same at
  the topology-only layer the medium is actually scoped to. Render leg,
  fully hermetic (`fakeprovider` + `UBX_PROVIDER_MIRROR`, since it needs
  a real `ship`): the `payments` chain shipped and rendered for real,
  `render --check` green. No real cloud resources exist afterward --
  verified directly (`aws ec2 describe-vpcs`/`aws rds describe-db-
  instances`, both empty), not assumed from following the rule.
  **UBI-47 closed in Linear. Phase 3 (the authoring frontends) complete**
  -- md (UBI-41), chat (UBI-46), SDK/TS (UBI-33/34), and diagram (UBI-47)
  all live; see the new "Phase 3 status" section below for the full
  scoreboard. A real, small doc-staleness finding fixed while closing:
  docs/architecture.md's own md/SDK/diagram headline sections each still
  said "not yet implemented" long after each medium was built -- fixed
  in place this session. No new code this session (a verification
  exercise, not an implementation one); existing test suite unchanged
  and still green. See docs/diagram-medium.md's own "Slice 6: built --
  the live finale" section and STATE.md for the full account, including
  the real transcripts.

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

### MCP server (UBI-25)

A new `ubx mcp` verb (one binary, not a second executable) serves the
Model Context Protocol over stdio, so an AI assistant can ask `ubx`
questions directly instead of a human already knowing the CLI's own
argument shapes. Three read-only tools —  `ubx_why`, `ubx_status`,
`ubx_scan` — each a thin wrapper over the exact same `--json` payload
(`whyJSON`/`statusJSON`/`scanJSON`, UBI-20's `format: 1` contract) the
CLI itself already produces; no parallel API, no new JSON shape.
`ubx accept`/`ship`/`writeback`/`revert-plan` (and `scan --surface-as`,
which opens a real GitHub issue/PR) are deliberately not exposed —
"boundary by omission: signatures and mutations are human acts," stated
in both `--help` and the docs page, not left to be inferred from what's
simply missing. See docs/architecture.md's "MCP server" section for the
full design, and STATE.md for the live-verification transcript.

### Executor v1 (UBI-26)

Phase 2 opens: the native executor (component map #4), scoped narrowly to
shipping *accepted* `drift_revert` proposals — not the general
`ApplyResourceChange` path for every proposal kind, which stays deferred
(see below) until a real resolver exists to produce `change`/`revert`
proposals safely. Design landed first, docs-only, across three documents:
docs/schema.md ("Amendment: apply records" — a new hash-chained
`ledger/applies/<id>.apply.json` object family, its own `ubx:apply:v1\n`
hash domain, chained two ways: to the proposal it executes, and to the
prior attempt for the same proposal), docs/executor.md (the
pending→in_flight→applied/failed/unknown_post_timeout failure-state
machine; THE invariant that a state transition is durably persisted
*before* the risky provider call it precedes; freshness re-verified before
every attempt, not just the first; serial execution in the same canonical
`(stack, type, name)` order hashing already defines), and
docs/executor-adversarial.md (the required-outcome program every
implementation must pass — also written to double as the project's future
published reliability report).

A real, load-bearing design resolution, not glossed over: `Proposal.status`
moving to `applied`/`partially_applied` cannot mean rewriting a proposal's
stored, hash-chained file in place — `core.Ledger.Append` enforces
immutability structurally (`ErrDuplicateProposal`), and nothing else in
this codebase ever mutates an already-written ledger entry. Resolved by
making `applied`/`partially_applied` **derived, reported** values, folded
from the most recent sealed apply record's outcome over the stored
`accepted` status — the same "immutable history, current truth computed by
folding over it" posture `core.FoldState`/`core.Ledger.Chain` already
establish, applied one level up (proposal → apply record, not just
address → proposal chain). See docs/schema.md for the full reasoning.

A second real finding, checked against the actual `tfplugin{5,6}` proto
rather than assumed: `ApplyResourceChange_Request` requires `PriorState`,
`PlannedState`, *and* `Config`, all as cty-msgpack `DynamicValue` — real
Terraform usage always derives `PlannedState` via a separate
`PlanResourceChange` call. `drift_revert`'s narrow shape (every restored
value is already concrete, recorded, and observed — never a placeholder)
is exactly what lets v1 skip a distinct plan phase and construct
`PlannedState` directly (prior state with the `Modification`'s `after`
values substituted in, the same dot-path mechanism `tfwrite.ApplyModification`
already uses) — a shortcut sound only for this one kind, stated as such,
not assumed to generalize once a resolver-driven `change`/`revert` kind
exists. See docs/executor.md.

**Closed (2026-07-17, session 4)**: `core/executor` (hermetic,
fake-provider-scripted failures) → provider `ApplyResourceChange` wiring →
`ubx ship <proposal-id>` CLI → live verification against real drift on
`ubx-states`, including a real `kill -9` mid-apply, proving the re-run
reconciles. v1 scope (`drift_revert` only) is complete and
live-verified end to end; see docs/reliability-report.md for the full
program's status against both the hermetic suite and real infrastructure,
and STATE.md for the session-by-session writeup. The general executor path
for `change`/`revert` (needs a real resolver) remains deferred, unchanged
from the scope this wedge always named.

### Resolver v1 (UBI-27)

Phase 2 continues: the resolver (component map #2) — v1 scoped to
producing `kind: "change"` proposals (creates + modifies, no destroys)
from hand-written, machine-shaped `ubx:intent/v1` files, not yet from any
real frontend (diagram/markdown/SDK/LLM — component map #7/#10, still
future work). Design landed first, docs-only: docs/resolver.md (the
resolver's own contract and rules), docs/schema.md ("Amendment: intent
files and resolved `change` proposals" — the intent-file wire format, the
`Delta.Creates` full node shape pinned for real, a new
`cross_stack_pin`/`pinned_head` resolution-input kind, `change`'s own
propose-time validation), docs/executor.md ("Amendment: shipping resolved
`change` proposals" — real tfplugin unknowns for `$computed`, dependent
resources fed mid-walk, apply records naturally carrying the resolved
concrete value), and docs/resolver-adversarial.md (the required-outcome
program, ten rows).

A real, honest correction made before any design work, not glossed over:
CLAUDE.md and docs/architecture.md both point at "v1 XCL's typechecker" as
the source to lift rules from, by name — checked directly rather than
assumed, `/Users/roozbeh/Ubiquex/xcl` (the repo literally named `xcl`) is
only ever a lexer/parser/AST/formatter, confirmed by its own README and by
grepping for `Computed`/`Pending`/graph code and finding none. The real
type system and graph algorithms this document's own "What carries over
from v1" section describes live in a *different*, separate repo,
`/Users/roozbeh/Ubiquex/ubx` (a Pulumi-targeting compiler product, itself
distinct from both `xcl` and this project). docs/resolver.md lifts its
rules from *that* repo's real code (`internal/xcl/typechecker`,
`internal/xcl/ir`, `internal/xcl/scope`, `internal/xcl/crossstack`,
`internal/xcl/workspace`) instead, with real file:line grounding. Two real
gaps found there, not carried forward as-is: v1's own single-stack
resource graph never actually detected cycles (only its separate,
workspace-level multi-stack graph did — docs/resolver.md's own cycle
detection is genuinely new code, not a port); and v1 had no cross-stack
pinning/staleness concept, and no double-run/determinism enforcement, at
all — both are deliberate v2 improvements over v1, using mechanisms this
project already built for other reasons (`core.DoubleRun`,
`VerifyFreshness`'s own staleness shape) rather than inventing new ones.

**Session 2 (2026-07-17): `core/resolver` built hermetic against fake
schemas/ledger state**, exactly the shape above — type rules
($ref/$cross/$secret/$computed/$ephemeral, checked against a
`SchemaInspector` interface, never a concrete `*provider.Schema` — the same
provider-import-free shape `core/executor.Applier` already established),
the dependency graph with real cycle detection (a DFS `path`/`inStack`
pattern borrowed from v1's own *workspace-level* detector, since its
single-stack one never had this at all), and `core.DoubleRun` reused
unchanged. All nine hermetic rows of docs/resolver-adversarial.md's own
program pass as real tests (row 10, a real provider's `PlanResourceChange`/
`ApplyResourceChange` round trip, is explicitly live-session work, not
this slice's). A real gap found and fixed *while implementing*, not
assumed correct from the session-1 design doc alone: the `$cross` marker's
own drafted shape (`{stack, ledger_dir, path}`) never actually named the
target resource's `type`/`name` at all — corrected to reuse `$ref`'s own
`to` shape (`{ledger_dir, to}`); `ResolutionInput` also gained a
`LedgerDir` field alongside `PinnedHead` (re-verifying a pin needs to know
*where* to re-derive the neighbor's current head from) and a new
`resolver.VerifyPins` function makes neighbor-advance staleness
(adversarial row 5) real and hermetically tested, ahead of the CLI session
that will wire it into `ubx accept`. `core.DiffAttributes` was exported
(a real second caller now exists, alongside drift's own two) rather than
duplicated.

**Session 3 (2026-07-17): CLI surface + cross-stack pinning wired into
`ubx accept`.** New verb `ubx resolve <intent-file>`, not a flag on
`ubx propose` — justified inline in cli/resolve.go's own doc comment:
`ubx propose`'s one narrow, pre-established job (hash an already-resolved
draft for a PR trailer, refusing anything not already fully resolved)
would be conflated with a genuinely different operation, the same way
scan/accept/ship are never merged into one multi-purpose verb; `ubx
resolve` instead slots into the pipeline exactly like `ubx scan` already
does (reads some input, produces a draft proposal, unchanged for
propose/accept to consume). A real gap surfaced against the session-1
design doc while building this: docs/resolver.md's own contract text names
"live state via core.StateReader" as an input, but session 2's actual
`Resolve()` never uses one — only `l.FoldState()` — so `ubx resolve` never
configures or reads through the provider at all, only fetches its schema
(no `--provider-config` flag needed, unlike scan/accept). `cli/
schemainspector.go` bridges `core/resolver.SchemaInspector` to a real
`*provider.Schemas` dump, the same boundary role `stateReaderAdapter`
already established for `core.StateReader`/`executor.Applier`.
`resolver.VerifyPins` (built hermetic in session 2) is now wired into both
`ubx accept` paths — the local-file path and `acceptFromMerge` — as an
unconditional check, not opt-in behind a flag the way `--reverify-with`
is: re-deriving a neighbor ledger's current head is a free, local
filesystem read, not a real provider round trip, so there's no cost
reason to make an operator ask for it. `acceptErrorCode` now classifies
`resolver.ErrCrossStackPinStale` as exit `1`, the same "actionable
finding" tier as a blocked reverify or a `parent` mismatch. All three new
CLI-level tests (`cli/resolve_test.go`) pass, plus a live, built-binary
verification of the full loop — resolve with a `$cross` reference,
accept while fresh (passes), advance the neighbor ledger, accept the same
pinned proposal again (refused, exit 1, nothing written) — run directly
against real ledger directories on disk, not just `go test`.
ubiquex-docs gained `cli/resolve.mdx` (new) and an accept.mdx
"Cross-stack pin verification" section, both with transcripts from the
actual built binary; `cli/exit-codes.mdx` updated for the new verb and
the new exit-1 case. `mint validate`/`mint broken-links` both pass.

**Session 4 (2026-07-17): executor unknown-value wiring + the live create
finale on real AWS. UBI-27 closed.** `provider/ctyvalue.go`'s
`encodeUnknownAwareDynamicValue` fixes the JSON-path gap named in session
1 — a `$computed` marker OR any schema-`Computed` attribute the resolved
config never set (the second case found live, not in the original
design) both become a real `cty.UnknownVal`, verified empirically against
`hashicorp/time` (docs/resolver-adversarial.md row 10, settled both ways).
`core/executor/ship.go` gained `shipChange` (creates + modifies together,
real dependency order re-derived from `depends_on`, applied outputs fed
into still-pending siblings via `foldResourceHistory`'s new
`lastProviderResult` — recovering a dependency's real output across a
crash/kill, not just within one invocation). Two real bugs found and
fixed live: `shipCreate` never called `Applier.Configure` (surfaced
against real AWS as a bare transport EOF, not a clean error — drift_revert
gets `Configure` for free through `ReadAndFingerprint`, a create never
reads anything first); and `core/resolver.Resolve` called `time.Now()`
fresh on each `DoubleRun` call, a rare but real false-positive mismatch
when the two calls straddle a second boundary. Live-verified on real AWS
(account `839333509514`): a real `aws_sqs_queue` + `aws_sqs_queue_policy`
chain, shipped for real (the first real cloud creates this codebase has
ever made), a real `kill -9` between the two resources (a new
`UBX_SHIP_DEBUG_DELAY_BETWEEN_RESOURCES` hook plus a poll loop pinpointed
the exact window), correctly recovered on re-run — verified independently
via `aws sqs`, never just `ubx`'s own report. Cleaned up via plain `aws`
CLI (destroys stay out of v1 scope). One real, unresolved gap found doing
that cleanup: a shipped create is invisible to `ubx status`/`ubx why
<address>` afterward (`core.Ledger.Fleet`'s discovery is keyed entirely on
`resolution.inputs`, which a create never populates for its own address) —
recorded in docs/resolver.md/docs/executor.md's "Out of scope" sections
and STATE.md, left for a follow-up ticket rather than a rushed patch.
docs/reliability-report.md gained a full UBI-27 section; ubiquex-docs
gained `cli/ship.mdx`'s change-proposal coverage and a new
`guides/create-flow.mdx`. See STATE.md for the full session writeup.

### Fleet visibility for shipped creates (UBI-29) — closed

The one gap UBI-27 closed with, fixed as its own ticket rather than
reopening UBI-27: `core.Ledger.Fleet`/`FoldState`/`ProposalsForAddress`/
`LastObservedHash`/`LastObservationTime` all now fold a `change`
proposal's own apply records as a second discovery source, alongside
`resolution.inputs` — gated on the specific resource's own last transition
being `applied`, never on the enclosing multi-resource attempt being
sealed (a resource's own completion and its attempt's overall summary are
different things, proven live in UBI-27's own kill test). `ResourceApply`
gains an additive `lookup` field, recorded explicitly by `shipCreate` at
ship time (the Slice 3 lookup-key lesson: never depend on derivation at
need-time) — with a graceful, read-time derivation fallback for any apply
record that predates this amendment. A deeper, related gap found while
designing the fix: `FoldState` itself never recognized a change-proposal
create's own `config`-keyed node shape at all (only adoption's
`state`-keyed one) — fixed alongside, not left as a second ticket, since
the same fold mechanism serves both. Hermetic coverage for all three named
adversarial rows (created-then-drifted lifecycle, an apply record
predating this amendment, a `kill -9` mid-create's unsealed record never
surfacing) plus the design's own key claim (per-resource, not per-attempt,
gating) in `core/ubi29_test.go`. Live-verified on real AWS: `ubx status`
now sees a shipped chain immediately; a real out-of-band `aws sqs
tag-queue` mutation was detected, attributed, and corrected; `ubx why
<address>` shows the full create-genesis chain where it used to report
"no proposals found." See docs/schema.md's own amendment and
docs/executor.md's own UBI-29 section for the full design; STATE.md for
the session writeup.

### Destroys v1 (UBI-30)

Phase 2 continues: destroys, the executor's last verb — the one operation
named and deliberately deferred at every prior mention of destroys in this
plan (UBI-27's own scope line; UBI-29's own out-of-scope note) since a
create/modify can be retried safely and a destroy usually can't. Design
landed first, docs-only, across four documents, the same "spec before
code" discipline UBI-26/UBI-27 already established: docs/resolver.md
("Amendment: destroys" — a dedicated intent-file `destroys[]` list,
never an `op` value and never inferred from absence, permanently, not just
for v1; resolve-time orphan protection checked against the whole ledger,
not just the current batch), docs/schema.md ("Amendment: destroys" —
`Delta.Destroys`' element shape re-pinned to carry full folded state plus
`depends_on`, requiring this project's first-ever `schema_version` bump;
two new `resolution.inputs[]` kinds; the `--confirm-destroys` accept-time
invariant; the tombstone posture), docs/executor.md ("Amendment: shipping
destroys" — one combined topological walk, real `tfplugin` wire mechanics
for a destroy, the three-way freshness precheck, the `destroyed`-vs-
`already_absent` disambiguation), and docs/destroys-adversarial.md (the
required-outcome program, eleven rows).

A real design resolution worth restating plainly, not left implicit:
"reversed ordering" (this ticket's own title) is not a second execution
mode bolted onto the existing one. `core/executor`'s `changeNodesOf`
(UBI-27) already builds one combined dependency graph from creates' and
modifies' own `depends_on` edges and topo-sorts it once; destroys extend
the identical map, keyed by the identical field, with the *reverse* edge
set (which surviving resources depend on the destroy target) rather than
the forward set. One topo-sort, over one graph, produces "creates forward,
destroys reversed, correctly interleaved with modifies" as a single
emergent order — never three separately-ordered phases. The other real
resolution: the old-vs-new-state ambiguity a destroy's own `unknown_post_timeout`
reconciliation faces (a bare "not found" read means nothing on its own —
was it just destroyed, or already gone?) is resolved by reusing
`ResourceApply.Reconciliation` one step earlier than its only prior use
(the mandatory pre-attempt freshness recheck, now recorded for a destroy
specifically), folded across the `parent` attempt chain via the existing
`foldResourceHistory` — no new ledger field, the same "reuse the
mechanism, extend its use" instinct this project has applied at every
prior amendment.

Filed as its own ticket, **UBI-30**, team `ubiquex` (referenced throughout
per the handoff's own instruction — no other ID inferred). **Closed,
sessions 1-5** (see session 4-5 write-up below for the close-out,
including a critical live-AWS bug found and fixed).

**Session 2 (2026-07-17): `core/resolver` destroy support, hermetic —
orphan protection real and tested.** `Delta.Destroys`' element shape
re-pinned for real (`core.DestroyEntry{Address, State, DependsOn}`,
`core.SchemaVersion` bumped 1 → 2 — this project's first non-additive
hashed-content shape change, migration cost genuinely near-zero since no
proposal of any kind had ever populated the old shape). `core/validate.go`
now lets `KindChange` carry destroys (blast_radius checked across all
three delta arrays) and requires a `destroy_target` resolution input
(observed_hash + lookup) per destroy entry, mirroring modifies' own rule.
`core/resolver` gained `IntentFile.Destroys []string`, `Resolve`'s own new
`knownDependents []string` parameter, and the full design: presence
validation, intra-stack orphan protection (a historical `depends_on` walk
over the ledger's own chain), cross-stack orphan protection
(`known_dependents`, honestly recording `not_performed`/`checked_clear`),
and `$ref`/`$cross` rejection into a same-batch destroy target
(`ErrRefToDestroyTarget` — a new rule found necessary while implementing,
not named in session 1's design, without which the "handled" same-batch
case wouldn't actually be sound). New `ubx resolve --known-dependent`
(repeatable) CLI flag. Full repo `go build`/`go vet`/`gofmt -l .`/`go test
./... -race -count=1` clean, no regressions.

A real bug found and fixed while building real CLI transcripts for
ubiquex-docs, not caught by the hermetic suite alone: the intra-stack
orphan walk originally accumulated every historical `depends_on` mention
forever, so a destroy stayed wrongly refused even after its dependent had
genuinely been repointed away by a later, separate proposal. Fixed to
track each address's own most recently recorded `depends_on` only (the
same "current truth folded from history" precedence `FoldState`/`Fleet`
already use elsewhere), with a new hermetic regression test
(`core/resolver/destroys_test.go`) added specifically to catch this
scenario. docs/resolver.md gained a session-2 addendum recording this and
a second real scope-limit finding (intra-stack orphan protection can only
ever see a dependency that was itself recorded via `$ref` in the same
batch as its target — a plain hardcoded-literal reference leaves no edge
to find); docs/destroys-adversarial.md's own "what this table doesn't yet
cover" section gained the matching entry. ubiquex-docs' `cli/resolve.mdx`
updated with the new flag and a full "Destroying a resource" section,
every transcript real against the actual built binary (`mint
validate`/`mint broken-links` both pass).

**Session 3 (2026-07-18): `core/executor` destroy support — all eleven
docs/destroys-adversarial.md rows green, hermetically.** `changeNodesOf`
extended with a `destroy *core.DestroyEntry` field on `changeNode`,
sharing the exact same `byAddr` map and single `topoSortAddresses` call
creates/modifies already use — "creates forward, destroys reversed" is
what falls out of that one combined walk, not a second mechanism. New
`shipDestroyNode`: a three-way freshness precheck (present-matching
proceeds; present-but-drifted refuses, recorded `errors[]`, never reaches
`in_flight`; already-absent short-circuits straight to a terminal success)
and `ApplyResourceChange` wire mechanics needing zero changes to
`provider`/`cli/stateadapter.go` at all — `PlannedState` the literal JSON
`"null"` already correctly encodes to a real `cty.NullVal` through the
exact same path UBI-27's own create-`PriorState` convention established,
and `Config==PlannedState` already follows through unchanged. New
`reconcileDestroyLoop` disambiguates `destroyed` from `already_absent`
after an ambiguous timeout by folding `ResourceApply.Reconciliation`
history across the `parent` attempt chain (`resourceHistory` gained
`lastReconciliationOutcome`) — a `kill -9` between a destroy landing and
its result being recorded still resolves correctly on the very next
attempt.

A real, load-bearing bug found by this session's own hermetic "re-ship
after partial destroy" test, not assumed safe from the design alone:
`shipChange`'s `resultsByAddr` dependency-satisfied gate required a
non-empty `ProviderResult` to consider a dependency done — which a destroy
can never have (nothing left to store once a resource is gone) — silently
re-blocking anything `depends_on`-ing a destroyed resource forever on
every re-run. Fixed to gate on the resource's own terminal `applied` state
alone. `core/executor/ship_test.go`'s `fakeApplier` gained real destroy
mechanics (a null-`PlannedState` branch, `scriptDestroyOutcome` for the
two timeout rows); `provider/internal/fakeprovider` (the real subprocess,
used for CLI-level transcripts) gained its own destroy support and its
first piece of cross-call process-lifetime state (`destroyedIDs`) — every
other fixture behavior there is stateless by design, but confirming
absence *after* a destroy genuinely needs the fixture to remember what it
did. `cli/accept.go` gained `--confirm-destroys` (`ErrDestroysNotConfirmed`,
exit 1, the same tier as a stale reverify or cross-stack pin) for both
acceptance tiers (local file and `--from-merge`). Full repo `go build`/`go
vet`/`gofmt -l .`/`go test ./... -race -count=1` clean, no regressions.

Two real, named gaps deliberately not closed this session, not silently
skipped: `core.Ledger.FoldState`'s own tombstone-folding (docs/schema.md's
amendment) isn't built yet, so a destroyed address still reads "present"
via `FoldState` until that separate `core` change lands; `ubx why`'s own
rendering of `destroyed`/`already_absent` is presentation-layer work for a
future session (the ledger already records the distinction correctly —
confirmed via `--json`, just not surfaced in `ubx why`'s human output
yet). docs/executor.md gained a session-3 addendum recording both findings
above plus these two gaps. ubiquex-docs' `cli/accept.mdx` gained a
"Confirming a destroy" section, `cli/ship.mdx` gained a "Shipping a
destroy" section (a real end-to-end transcript: adopt → resolve a destroy
→ accept `--confirm-destroys` → ship → clean `applied`, `--json` showing
the `present_matches`/`destroyed` reconciliation pair), and
`cli/exit-codes.mdx` gained the new exit-1 cause (`mint validate`/`mint
broken-links` both pass). See STATE.md for the full session writeup.

**Sessions 4-5 (2026-07-18): both deferred gaps closed, hermetically —
then a critical live-AWS bug found and fixed, UBI-30 closed.** Session 4:
`core.shippedDestroyFold(proposalID, addr)` (`core/apply.go`) mirrors
`shippedCreateFold`'s per-resource gating exactly, folding the last
`Reconciliation` entry's outcome instead of `ProviderResult`; `FoldState`
gained a third loop over `Delta.Destroys` that resets `current`/`found` to
absent on a shipped destroy; `Fleet` gained a matching `tombstoned` map,
filtering tombstoned addresses out of its returned slice entirely — `ubx
status`/`ubx scan` needed zero changes, the exact repeat of UBI-29's own
finding. `ubx why` gained `renderDestroys` (prints `Delta.Destroys`,
previously never rendered at all) and `destroyOutcome` (annotates a
destroy's terminal `applied` line `(destroyed)`/`(already_absent)`,
previously buried in a `reconcile:` line a reader had to already know to
look for). Hermetic: `core/destroy_tombstone_test.go`,
`cli/why_destroy_test.go` (new), full repo build/vet/fmt/test clean.

Session 5: the live full-lifecycle finale (create a chain, drift it,
resolve, destroy through `--confirm-destroys`, `kill -9` mid-destroy,
reconcile, verify via the `aws` CLI, `ubx why` reading the complete
biography) hit a real bug no hermetic test had caught — `ApplyResourceChange`
for a destroy, called with no prior `PlanResourceChange`, silently no-ops
against a real, complex SDKv2 provider (`terraform-provider-aws` 6.54.0)
instead of deleting anything; the "no separate plan phase" shortcut
session 3's own design carried forward from create/modify (confirmed safe
there against a simpler provider, docs/executor.md's own session-3
addendum) does not extend to destroy. Fixed properly, per explicit
direction, not patched around: `provider.Provider` gained a real
`PlanResourceChange` method (both protocol versions); `core/executor`'s
`Applier` interface mirrors it; `shipDestroyNode` calls it unconditionally
right after fetching the resource's schema and before recording
`in_flight` (Plan is read-only, so a Plan failure means the risky Apply
never runs), threading the real `PlannedPrivate` through to
`ApplyResourceChange`; `cli/stateadapter.go` wires both through. A second,
independent bug surfaced fixing the first: `provider/ctyvalue.go`'s
`encodeUnknownAwareDynamicValue` never produced a genuine top-level
`cty.NullVal` for a literal JSON `null` input (destroy's own signal),
instead building a per-attribute object (`Unknown` for `Computed` fields,
`Null` for the rest) — very likely the actual cause of a live
`aws_sqs_queue_policy` destroy failure (`NonExistentQueue` against an
empty queue reference) this same session had already hit and left
unexplained; fixed by special-casing a literal top-level `null` into a
genuine `cty.NullVal` before the existing per-attribute walk.
`provider/internal/fakeprovider` gained a matching `PlanResourceChange`
handler (both protocol versions) and now strictly requires non-empty
`PlannedPrivate` on its own destroy branch — deliberately stricter than
the real provider's silent no-op, so a regression fails loudly as a test.
Full repo build/vet/fmt/test clean.

The live finale then re-ran for real, against the exact resources the bug
had touched, not fresh ones: the original `aws_sqs_queue_policy`'s destroy
(three failed pre-fix attempts had exhausted that proposal's own
per-resource retry budget — a real, hard limit requiring a fresh proposal,
not a bug) now actually deletes, verified via a direct `aws sqs
get-queue-attributes` call; a dedicated single-resource chain got a real
`kill -9` mid-destroy (after the real AWS call had already landed,
confirmed by wall-clock timestamps and a direct `aws sqs get-queue-url`
call), reconciling correctly on the next `ubx ship` via
`reconcileDestroyLoop`'s not-found-read-implies-destroyed path — live-
verified for the first time this session. Three other resources this
session's own pre-fix investigation had left falsely "destroyed" in their
ledgers (real queues still alive in AWS, sealed with a false `applied`
outcome — `FoldState`'s own tombstone-fold correctly excludes a
sealed-destroyed address from `ubx status` regardless of whether the
underlying delete was real) were re-discovered via a fresh `ubx scan`
(each correctly reports `new`), re-adopted, and destroyed for real through
fresh signed proposals — every queue this session ever touched ended up
deleted *through* `ubx`, not a raw `aws sqs delete-queue` fallback. `ubx
why` against the kill-9 target shows the complete, honest biography —
including the pre-fix false tombstone exactly as it was actually recorded,
not rewritten. Account left genuinely clean: `ubx status` across all four
scratch ledgers and a direct `aws sqs list-queues --queue-name-prefix
ubx-ubi30` both confirm it. A real, separate gap named, not fixed: SQS's
own real deletion-visibility lag exposed that `reconcileDestroyLoop`'s
retry budget (5 attempts, 20ms apart) is too short for genuine eventual
consistency in a real account — left for a future session's own
retry-budget tuning. docs/executor.md gained a session-5 addendum;
docs/reliability-report.md gained a full "UBI-30" section, real
transcripts throughout. See STATE.md for the full session writeup.

### A destroy that lied: universal post-destroy read-back (UBI-44, co-scoped with UBI-42)

Found live in UBI-43 session 5's own finale: a real `google_pubsub_topic`
destroy reported `applied`/`Outcome: "destroyed"` while the real GCP
topic stayed live — filed as its own issue rather than patched under
time pressure. Diagnosed for real, not assumed a repeat of UBI-30's own
`PlannedPrivate` no-op: `shipDestroyNode` already calls
`PlanResourceChange` unconditionally (UBI-30's own fix), and
`PlannedPrivate` came back empty in every attempt this session made
against the real provider, including the one that later actually
deleted the topic — ruling that mechanism out directly rather than by
assumption. Root cause isolated by direct experiment against real GCP,
four separate ways (a real `ubx ship` plus three isolated wire-protocol
variations): `google_pubsub_topic`'s own `Delete` needs its `name`
attribute (the short-form topic ID, distinct from `id`) populated in
`PriorState`, which `ubx`'s universal `{"id": "..."}`-only lookup never
supplies — confirmed via Cloud Audit Logs showing zero real
`DeleteTopic` calls across all four attempts, and a genuine `DeleteTopic`
call appearing the moment `name` was filled in correctly.

The real finding: two genuinely different root causes (UBI-30's empty
`PlannedPrivate`; this session's incomplete `PriorState`) produce the
identical symptom — a clean, diagnostics-free provider "success" that
isn't true. Patching this one type's own lookup gap would close this
instance and leave the class of bug open for the next SDK quirk. Fixed
structurally instead: `shipDestroyNode`'s own `Apply` call succeeding no
longer resolves `destroyed` directly — it now runs the same
reconcile-by-query loop an ambiguous `Apply` result already required,
universally. A present read after a *claimed* success is deliberately
not immediately conclusive (real propagation lag can look identical to a
lie) — only the read-back's own final attempt, still present, earns the
distinctly-worded `provider_reported_success_but_present` verdict, never
silently upgraded to `destroyed`, and never rounded down to the vaguer
`still_unknown` either (a read that clearly and repeatedly says "still
here" is not genuinely ambiguous). Terminal `Apply` errors are
unaffected — a real, structured diagnostic is already the provider's own
honest negative answer, and adding a read-back there would only cost
without closing a real risk.

**Co-scoped with UBI-42, not deferred separately**, since the universal
read-back makes the pre-existing retry budget's own inadequacy
load-bearing for every destroy, not just the rare ambiguous ones it used
to gate: destroy's own reconcile budget became a ten-step backoff
schedule (`destroyReconcileBackoffSchedule`, `core/executor/ship.go`,
~64 seconds total, comfortably past AWS's own documented ~60-second SQS
lag UBI-30 found), separate from create/modify's own unrelated
`reconcileLoop` budget, untouched. The common, honest case (a
synchronously-consistent provider) resolves on the very first read, no
added cost at all.

New hermetic tests, `core/executor/destroys_test.go` (a lying destroy
never resolves `destroyed`, retried and still fails on re-ship; the
honest case adds zero extra reads; a genuine bounded propagation delay
still resolves `destroyed` once the schedule reaches it) plus
`core/executor/ship_test.go`'s new `scriptLyingDestroy`/
`scriptDelayedAbsence` fault-injection helpers. `provider/internal/fakeprovider`
gained the identical lying-destroy mode (`FAKEPROVIDER_APPLY_MODE=lying-destroy`),
proven through the real tfplugin wire protocol by
`cli/ship_lying_destroy_test.go` — gated behind `UBX_TEST_SLOW=1` since
it genuinely pays the real ~64-second budget and can't reach in to
shrink `core/executor`'s own unexported var. Three new
docs/destroys-adversarial.md rows (12-14). Live re-run against real GCP
with the fix in place: the exact scenario that lied now correctly
reports `failed`/`provider_reported_success_but_present` instead —
confirmed honest via `gcloud` (the topic genuinely still there, matching
the report) and Cloud Audit Logs (still zero `DeleteTopic` calls — the
underlying lookup-completeness gap is a real, separate, still-open
follow-up, not fixed by this session's own read-back-honesty work). The
original diagnosis session's own false `destroyed` record stands
permanently, uncorrected by edit, per this project's own append-only
posture — docs/reliability-report.md's own new "UBI-44" section has the
full transcripts. docs/executor.md gained the full design amendment.
ubiquex-docs updated the same session (`cli/ship.mdx`'s own new "A
provider that reports success without it being true" section,
`guides/destroy-flow.mdx` cross-linked); `mint validate`/`mint
broken-links` both clean. Account left clean. See STATE.md for the full
session writeup.

### Multi-provider stacks (UBI-43)

Phase 2 continues: every prior session (UBI-26 through UBI-30) built
`ubx` against exactly one provider per invocation — real, but not what
docs/architecture.md's own payments example actually is (RDS + S3 +
`helm_release`, one stack, three provider binaries). Design landed first,
docs-only, across three documents, the same "spec before code" discipline
every prior amendment in this project has used: docs/resolver.md
("Amendment: multi-provider stacks — type→provider inference" — the
`providers` config map, schema-ownership inference with a hard error on
ambiguity or an unowned type, a rare explicit hint to break a genuine
ambiguity, destroys inferring fresh rather than trusting history, a
staged flag-retirement plan), docs/executor.md ("Amendment: multi-provider
stacks — one walk, a lazily-launched client pool" — the pool keyed by
`{source, version}`, the existing combined topo-walk confirmed unchanged,
a launch failure classified as a per-node terminal error, scan/status/
fleet's own grouping generalization), docs/schema.md ("Amendment: the
`provider` field returns — no longer redundant" — reinstating a field
UBI-27 had explicitly dropped, additive, no `schema_version` bump), and
docs/multi-provider-adversarial.md (the required-outcome program, seven
rows).

A real design tension found and resolved while writing this, not silently
glossed over: docs/schema.md's own UBI-27 pinning of `Delta.Creates`'
node shape had explicitly called a `provider` field "redundant with
information the outer `Proposal` already carries" — true at the time
(one provider per invocation, so which binary executed a node was never
in question), false now that one `change` proposal can span providers.
Reinstated on all three delta kinds (creates, modifies, destroys — a
destroy needs to know which provider to call exactly as much as a create
does), resolver-populated, never hand-authored except the narrow
ambiguity-breaking hint.

A real design resolution worth restating plainly, the same "one
mechanism, not a second one" instinct this project keeps applying:
neither the resolver's own dependency graph nor the executor's own
combined topo-walk needed *any* change to become multi-provider-capable —
confirmed by reading the actual code (`core/resolver/graph.go`,
`core/executor/ship.go`), not assumed from the design alone. Both already
operate purely on canonical addresses and `depends_on`/`$ref`/`$computed`
edges; type and provider were never consulted while building or walking
either. Multi-provider changes *which client* the executor's own walk
calls at each step (a pool lookup instead of one closed-over `Applier`),
never the walk's own shape, order, or the graph's own construction.

Filed as its own ticket, **UBI-43**, team `ubiquex`. Session 1 was
docs-only.

**Session 2 (2026-07-18): `core/resolver`'s own type→provider inference,
real code, hermetic.** `Resolve`'s own signature changed from a single
`SchemaInspector` to `[]DeclaredProvider` — a stack's whole declared
provider set, each paired with its own schema — with no separate
single-provider code path left; today's single-provider CLI flow is
simply the one-element case. New `inferProvider` implements the
three-way rule design landed: exactly one owner wins outright; zero
owners is `ErrUnknownType` (reused, not duplicated, from the existing
single-provider sentinel — the two claims collapse into the same one once
every resolve goes through a provider set of at least one), naming every
provider checked; more than one owner is `ErrAmbiguousType` unless an
intent-file entry's own narrow `"provider"` hint names one of the real
owners (`ErrProviderHintUnknown`/`ErrProviderHintDoesNotOwnType` for the
two ways a hint itself can be wrong). The winner is recorded into every
create/modify/destroy node's own `provider` field
(`core.ProviderRef{Source, Version}`, reinstated on `Modification`/
`DestroyEntry` per docs/schema.md's own amendment). Destroys infer fresh
against the currently-declared set, exactly as designed — no per-entry
hint support for `destroys[]` (docs/schema.md scoped that escape hatch to
`resources[]` only).

A real thing found while actually implementing this, not assumed correct
from the design alone: `resolveRef`'s own `IsComputed` check on a `$ref`
target's attribute was reading a single globally-passed schema, invisible
as a bug until a `$ref` could cross a provider boundary for the first
time — fixed to read the *referenced* sibling's own resolved provider
schema (`target.provider.Schema`, set on every batch entry before any
value resolution begins), never the *referencing* entry's. A dedicated
regression test (`TestResolve_CrossProviderRef_ComputedSubstitution`)
uses two providers with genuinely disjoint type sets specifically so a
naive implementation using the wrong schema would have failed loudly, not
passed silently with a wrong answer. New
`core/resolver/multiprovider_test.go`: type inference recording the
correct winner across creates/modifies/destroys, docs/multi-provider-
adversarial.md's rows 1 (ambiguous, no hint), 2 (ambiguous, resolved via
hint, both ways a hint can itself be wrong), 3 (unowned type, both fresh
and for a destroy whose original provider has since been dropped from
config), and 5 (a real cross-provider `$ref` chain, `$computed`
substitution, correct `depends_on`). Every pre-existing hermetic test (40
call sites) updated mechanically via a new `singleProvider(s)` test
helper, preserving each one's own single-provider behavior unchanged; all
still pass. `cli/resolve.go`'s own call site does the identical
one-element wrap — no CLI-visible behavior change this session, since
there's still no way to declare more than one provider from the CLI.
docs/resolver.md gained a session-2 addendum recording the `resolveRef`
finding and the hermetic coverage; its own "Out of scope" bullet updated
from designed to fixed. Full repo build/vet/fmt/test clean, no
regressions.

**Session 3 (2026-07-18): `core/executor`'s own client pool, real code,
hermetic.** New `ApplierPool` interface (`Get(ctx, source, version)
(Applier, error)`, lazily launching, core/executor still never launches a
provider itself — the concrete implementation belongs in `cli/`) and
`SingleApplierPool` (the trivial always-succeeds wrapper a single-
provider stack needs, today's CLI flow unchanged). `Ship`'s signature
changed from `app Applier` to `pool ApplierPool`; `shipDriftRevert`
untouched (single-provider by construction); `shipChange`'s own per-node
loop resolves each node's `Applier` via a new `changeNode.provider` field
(nil falls back to the invocation's own `providerSource`) immediately
before dispatching to `shipCreate`/`shipModifyNode`/`shipDestroyNode`,
which stay completely unchanged — still just taking one plain `Applier`
directly, the pool routing entirely the loop's own concern. A pool-lookup
failure is a per-node terminal error, `continue` not `return`, mirroring
the existing "blocked" case exactly. A real, named gap found
implementing, not silently assumed covered: `providerConfig` stays one
global value across every node regardless of provider — correct for
today's single-provider flow, real remaining work for the config-wiring
session below. New `core/executor/multiprovider_test.go` covers
docs/multi-provider-adversarial.md's rows 4 (launch failure mid-walk),
6 (`kill -9` between providers — a new `fakeApplierPool` with per-key
launch-failure scripting and call counters proves the already-applied
provider is never re-launched and the untouched one launches exactly
once, fresh), and 7 (per-provider freshness independence). All 35
pre-existing hermetic `Ship(...)` call sites updated mechanically; all
still pass. `cli/ship.go`'s own call site does the identical one-entry
wrap — no CLI-visible behavior change this session. docs/executor.md
gained a session-3 addendum; its own "Out of scope" bullet updated from
designed to fixed. Full repo build/vet/fmt/test clean, no regressions.

**Session 4 (2026-07-18): the `providerConfig` gap closed, `.ubx/config`
wiring, deprecation staging, real code, live-verified.**
`ApplierPool.Get` now returns `(Applier, json.RawMessage, error)` — each
pool entry carries its own resolved config, never a single global blob;
`Ship`/`shipChange` dropped the `providerConfig` parameter entirely (the
per-node functions already took it explicitly, so only the loop's own
sourcing changed); `SingleApplierPool` gained a matching second
parameter. New `cli/providerpool.go`: the concrete `ApplierPool`
`.ubx/config`'s own new `[providers]`/`[provider_configs]` tables drive
(the config-shape decision this arc's own design left open — a sibling
table, source-keyed, additive alongside `[providers]`, never reopening
that table's own already-ratified shape), lazily launching via an
injectable `launchFunc` seam, refusing outright rather than silently
substituting an undeclared source or a version that no longer matches
the current pin. `cli/resolve.go`/`cli/ship.go` both branch on
`cfg.Providers`: non-empty is a real multi-provider stack (resolve
launches every declared provider eagerly, sorted, for its own schema;
ship's own pool stays lazy); empty is today's exact single-provider flow,
byte-for-byte unchanged. `--source`/`--provider-version` deprecation
stage 2 built: a stderr warning naming exactly which flags were ignored,
config always winning regardless. **Live-verified against the real built
binary**, not just hermetic: a real `ubx resolve` → `ubx accept` →
`ubx ship` chain against two genuinely separate provider subprocesses
(`UBX_PROVIDER_MIRROR`, no network) confirmed correct per-node routing,
the deprecation warning, and a version-mismatch refusal blocking only the
one affected node while an unrelated sibling proceeds independently. New
`cli/providerpool_test.go` (8 tests) and `cli/config_test.go` cases.
docs/architecture.md/docs/resolver.md/docs/executor.md/docs/multi-
provider-adversarial.md all updated; ubiquex-docs updated same session
(new config tables are user-visible). Full repo build/vet/fmt/test
clean, no regressions.

**Session 5 (2026-07-18): `ubx status --drift`/`ubx scan --all`'s own
multi-provider fleet-grouping, real code, live-verified.** The one
remaining gap session 4 left named explicitly: walking a whole historical
*fleet* across mixed providers is a different problem from routing a
single proposal's own nodes (session 3-4's own work) — a fleet entry's
provider has to come from somewhere other than a single invocation's own
`--source` flag. `core.Ledger.Fleet` gained `FleetEntry.Provider
*core.ProviderRef`, read back with the identical "most recent wins, falls
back to the shipped create's own recorded value" precedence `Lookup`
already established; nil for a resource this ledger only ever adopted or
drift-recorded (`core/scan.go` never populates one). `resolver.inferProvider`
exported as `InferProvider` — no behavior change to its own three existing
call sites — so a legacy Fleet entry with no recorded provider of its own
gets one inferred fresh by type, the identical mechanism a brand-new
resource's own resolve already uses, never a second one invented in
`cli/`. `cli/status.go`/`cli/scanall.go` both branch on `cfg.Providers`,
mirroring session 4's own convention exactly. New
`cli/schemainspector.go`'s `resourceTypeSchemaInspector` lets
`declaredProvidersForInference` (`cli/providerpool.go`) reuse the SAME
already-launched pool entries for inference — never a second launch of
every declared provider just to answer a schema question — since a
pool-returned `Applier.Schema()` is type-erased differently than the
existing `schemaInspectorAdapter` needs. New shared `classifyFleetEntry`/
`unreadableNoLookup`/`unreadableProviderUnavailable` helpers so the
single- and multi-provider walks report identically-worded outcomes,
rather than two copies drifting apart. New `core/fleet_provider_test.go`
(5 tests) and `cli/multiprovider_fleet_test.go` (3 tests, using the same
injectable `launchFunc` seam `cli/providerpool_test.go` already
established); `cli/status_test.go`'s existing 8 cases all still pass
unchanged, proving `classifyFleetEntry`'s extraction didn't alter
behavior. **Live-verified against the real built binary**: the same
`UBX_PROVIDER_MIRROR`-plus-wrapper-script technique session 4 used, this
time with two distinct `FAKEPROVIDER_RESOURCE_TYPE` values against a real
two-entry `.ubx/config` `[providers]` table — `ubx resolve` correctly
inferred the right provider with no `--provider`/`--source` given at all;
`ubx status --drift` on a legacy-adopted entry correctly inferred its
provider and reported `clean`, then correctly reported `drifted` after a
real out-of-band mutation; `ubx scan --all` routed correctly too.
docs/architecture.md's own status line updated (built, not "still not
built"); docs/executor.md gained a session-5 addendum; docs/multi-
provider-adversarial.md's own "what this table doesn't cover" updated.
Full repo build/vet/fmt/test clean, no regressions.

**Session 6 (2026-07-18): the live finale, real infrastructure, arc
complete.** A real `aws_sqs_queue` + `google_service_account` (real AWS
account, real GCP project `personal-273114`), one intent file, a genuine
cross-provider `$ref` (the service account's `description` holding the
queue's own real `Computed` `arn`), resolved with no `--provider`/
`--source` at all -> accepted -> shipped as ONE signed proposal, both
resources reaching `applied` -- verified independently against both
clouds' own APIs, not just `ubx`'s own report.

**A real plan change, found live, not silently absorbed.** The
originally-chosen second provider (`hashicorp/time`, an earlier session's
own `AskUserQuestion` decision) was swapped for a real second cloud
provider after a direct empirical probe against the real `hashicorp/time`
binary found something the earlier decision hadn't anticipated:
`ReadResource` given only `{"id":...}` -- the *universal* shape
`core.DeriveLookupFromResult` derives for every resource type, no
exceptions -- returns every other attribute as `null`. Not "attribution
comes back unattributed" (the anticipated, accepted tradeoff), but
"drift detection itself is structurally impossible" for this type.
Flagged to the user before spending real infrastructure time on a
premise just found false; GCP was chosen instead, the option the earlier
decision had explicitly set aside pending confirmed credentials --
confirmed available this session (`gcloud`'s own Application Default
Credentials, already authenticated against `personal-273114`).

**Two further real GCP findings, live, not assumed from the design
alone.** `google_service_account`'s own drift detection works correctly
through `ubx`'s ordinary automatic lookup (a real `display_name`
mutation was correctly detected as drifted) -- but its Cloud Audit Log
entries name the resource by a numeric `unique_id` never present in the
resource's own observed state, so its real, correctly-detected drift is
currently unattributable. This extends, rather than contradicts, an
already-documented limitation (`gcpaudit/client.go`'s own doc comment
already named the identical class of gap for
`google_secret_manager_secret`, a numeric project number instead of the
project ID). `google_pubsub_topic` (previously proven, in an earlier
session, to attribute correctly) has the opposite problem: its own
minimal `{"id":...}` lookup can't observe a real `labels` mutation at
all -- and, more seriously, a real `ubx ship` destroy of one reported
`destroyed` in the ledger's own reconciliation record while the real GCP
topic stayed live, found only because this session verified
independently rather than trusting the report. Not fixed live -- the
real leaked topic was deleted by hand to leave the account clean, and
the finding filed as its own issue, **UBI-44** (`ubiquex` team), rather
than patched under time pressure: it's a `core/executor`
`reconcileDestroyLoop` correctness gap (trusting a provider's response
without verifying it), not a conformance-fixture curiosity, and deserves
its own root-cause investigation. `conformance/registry.go`'s own
`google_pubsub_topic` entry gained a note recording this destroy-side
finding alongside its already-documented read-side one.

`google_project_iam_custom_role` was the one real type found, live, with
neither gap -- added to the same stack specifically to complete the
"both providers, both attributed" demonstration honestly, once the two
structural gaps above were found: a real out-of-band `permissions`
mutation, correctly detected by `ubx status --drift` with zero manual
assistance, correctly attributed to a real `UpdateRole` event on the
first attempt. `ubx why` on both the queue and the custom role shows the
complete, honest biography of each, real attribution included.

**Cleanup, real, through `ubx`.** Every resource this session created
(the queue, the service account, the custom role, and the pubsub topic --
four addresses total) was decommissioned via one real `ubx ship` of a
`delta.destroys` proposal. Verified independently afterward, directly
against both clouds: the queue and service account are genuinely gone;
the custom role is GCP's own correct soft-deleted state; the pubsub
topic (per the finding above) needed a direct, manual delete to actually
leave the account clean.

docs/executor.md gained a session-6 addendum; docs/architecture.md's own
multi-provider section marked the live finale done. ubiquex-docs gained
a new guide, `guides/multi-provider-flow.mdx` (real transcripts
throughout, including the mid-session provider swap and both GCP
findings); `mint validate`/`mint broken-links` both clean. No code
changed this session beyond `conformance/registry.go`'s own new note --
this was a live verification and documentation session, not an
implementation one. **UBI-43 closed in Linear, arc complete (sessions
1-6)**.

### Config cascade + formats (UBI-32 Arc A)

UBI-32 unparked by founder decision the moment UBI-43 closed, running as
two sub-arcs, config first. Arc A's own scope, per the ticket: upgrade
`.ubx/config` discovery from UBI-19's nearest-file-wins to a per-key,
editorconfig-style cascade (child overrides parent, tables merge
key-wise, CLI flags still beat everything); a provenance surface
(resolved value + which file supplied it); a per-directory stack
default; and three supported formats (HCL canonical, TOML forever, YAML
strict-only) sharing one internal struct. Explicitly sequenced to land
*before* UBI-41 (the markdown intent provider), so `[intent]` config
never has to touch the legacy nearest-file-wins loader at all. Arc B
(`LedgerStore` extraction, remote stores, addressing) is the larger,
separate arc and has not started.

**Session 1 (2026-07-18): design, real code, live-verified, ubiquex-docs
-- all in one session.** docs/architecture.md's own "Config: cascading,
per-key, child overrides parent" section gained the settled
implementation design before any code existed to hide it: the cascade
merges on a **generic tree** (`map[string]any`, nested tables as nested
maps), not the typed `Config` struct directly, so the merge/provenance
logic is written exactly once and is genuinely format-agnostic — each
format's own parser only has to produce that one shared shape. Its own
"Config formats" section gained the concrete per-format mechanics:
HCL's literal-only enforcement reusing `tfwrite`'s own `expr.Value(nil)`
technique, and a real correction to how HCL renders `providers`/
`provider_configs` (an attribute holding an object-constructor
expression, `key = { ... }`, never an HCL block — quoted keys aren't
valid as block argument names at all, found by parsing the design's own
prior sketch directly against `hclsyntax` rather than assuming it would
work). YAML's strict mode got a real, confirmed-not-assumed narrowing of
scope: `gopkg.in/yaml.v3`'s own implicit resolver already treats
`no`/`yes`/`on`/`off` as `!!str`, never `!!bool` (checked directly against
the library), so the only real coercion risk strict mode has to guard
against is numeric precision loss (a bare `6.60` silently becoming
`float64(6.6)`) — caught with a round-trip format-and-compare check on
every plain numeric scalar, quoted values exempt by construction. New
docs/config-cascade-adversarial.md: 11 rows covering conflicting keys at
different cascade levels (both a top-level key and a key nested inside
a table, proving sibling keys survive a partial override), cross-format
cascade chains, same-directory multi-format precedence, both YAML
coercion cases (the real one refused, the assumed-but-not-actually-real
one proven safe instead of just left untested), an HCL literal-only
violation failing the whole file (matching the existing malformed-TOML
precedent, no partial per-key salvage), the per-directory stack default,
format-blind provenance correctness, and unknown-key warnings checked
identically across all three formats (necessary because
`BurntSushi/toml`'s own `MetaData.Undecoded()` — the mechanism UBI-19's
original loader used — turns out not to apply once parsing targets a
generic map instead of the `Config` struct directly; confirmed by
decoding a real TOML fixture into `map[string]interface{}` and observing
every key reported as "undecoded," not just the genuinely-unknown one).

**Real code, same session.** New `cli/configcascade.go` (discovery,
generic-tree merge, provenance, unknown-key checks — `LoadConfig` itself
now a thin wrapper over `LoadConfigResolved`, so every existing call site
across the codebase kept working unchanged), `cli/confighcl.go` (the HCL
generic parser plus `ctyToGeneric`), `cli/configyaml.go` (the YAML strict
parser, `yaml.Node`-level, empirically confirmed against
`gopkg.in/yaml.v3` before being written, not assumed), `cli/configtoml.go`
(a thin wrapper — BurntSushi already decodes into the exact
`map[string]any` shape the cascade needs, no conversion required). New
`gopkg.in/yaml.v3` dependency — the second non-stdlib dependency this
project has added purely for config parsing, after `BurntSushi/toml`.
`Config`'s own struct gained matching `json` tags alongside its existing
`toml` ones, since the merged generic tree now decodes into it via one
JSON round-trip rather than three separate format-specific struct
decoders. All 11 adversarial rows became real hermetic tests in
`cli/configcascade_test.go`; every pre-existing config/init/status/
providerpool test (40+ across the package) still passes unchanged,
proving the cascade is a strict superset of UBI-19's nearest-file-wins
for every case that only ever had one file in play.

**`ubx init --format=hcl|toml|yaml` (HCL default) and `ubx config` (the
provenance view), both built and live-verified.** `ubx init`'s own
default written format changed from an extensionless TOML `.ubx/config`
to canonical `.ubx/config.hcl` — a real, deliberate behavior change,
not silently absorbed (the legacy name stays fully supported for
*reading*, forever, per configcascade.go's own discovery order). New
`ubx config`: walks the same cascade, prints every effective value and
exactly which file supplied it. A real gap found live, fixed the same
session (not left for later): `ubx init`'s own overwrite-protection
only ever compared against the exact target filename, so a bare
`ubx init` run a second time in a directory that already had a working
legacy `.ubx/config` would have silently written an empty
`.ubx/config.hcl` right alongside it — and since `config.hcl` wins
discovery, every value the existing config supplied would have vanished
from `ubx`'s own point of view with no error or warning anywhere. Fixed
by checking discovery's own winner, not just the target path, before
writing; `--force` still proceeds once a caller has explicitly opted
in. **Live-verified against the real built binary**: a genuine
multi-level, cross-format cascade (root `.ubx/config.hcl`, an
intermediate `.ubx/config.toml`, a leaf `.ubx/config.yaml`, one key each)
resolved correctly with correct per-key provenance; both YAML violation
cases (`version: 6.60` refused naming the file and token, `github_repo:
no` loading cleanly as a string) and both HCL violation cases
(interpolation, a function call) reproduced exactly as designed; the
`ubx init` shadow-conflict refusal and its `--force` override both
confirmed. Full repo `go build ./...`/`go vet ./...`/`gofmt -l .`/
`go test ./... -race -count=1` clean throughout.

ubiquex-docs updated the same session (user-visible: new command, new
default format, new flag): `cli/config.mdx` rewritten for the cascade,
all three formats, and the `ubx config` provenance view (every
transcript re-verified against the real built binary); `cli/init.mdx`
gained `--format` and the shadow-conflict warning; `cli/exit-codes.mdx`
added `ubx config` to the no-finding-concept group. `mint validate`/
`mint broken-links` both clean. Both repos committed and pushed.

Queued for a later session: Arc B (`LedgerStore` extraction, remote
stores, addressing) — not started.

### Ledger stores: `LedgerStore` + remote stores + addressing (UBI-32 Arc B)

The larger of UBI-32's two arcs, per the ticket: extract a `LedgerStore`
interface from `core.Ledger`'s own implicit filesystem operations (the
git-directory implementation becomes the reference implementation, zero
behavior change proved by the full pre-existing test suite passing
unmodified); remote object stores via `gocloud.dev/blob` (s3:// first,
gs:///azblob:// through the identical code path if the harness makes them
cheap); per-store conformance against real infrastructure, not just
design — distributed locking, a compare-and-swap head pointer, interrupted
appends, corrupted objects; the `<base store>/<stack>/` addressing rule;
and a PR-acceptance ceremony design pass for a remote-store stack (git
stays the signing surface, the remote store becomes the system of record,
mirrored on accept).

**Session 1 (2026-07-19): design landed, LedgerStore extracted with a
real git reference implementation, s3 support built and
conformance-tested hermetically and live, a first slice of the CLI
wired.** See docs/architecture.md's own "`LedgerStore` interface,"
"Addressing," and "PR-acceptance ceremony" sections (all amended this
session) for the settled design, and docs/ledgerstore-adversarial.md
(new, 12 rows) for the required-outcome program.

**The interface, extracted from what `core.Ledger` actually does, not
designed from the sketch alone.** Read directly across
`core/ledger.go`/`core/lock.go`/`core/salt.go`/`core/apply.go` before
writing anything: every chain-walking read (`Chain`/`Fleet`/
`ProposalsForAddress`/`LastObservedHash`/`FoldState`) goes through
`Head()`+`Read()` repeatedly, never a directory listing -- only
`ApplyAttempts` needs one. `LedgerStore` (new `core/ledgerstore.go`) is
byte-level (`ReadProposal`/`WriteProposalIfAbsent`, `Head`/`AdvanceHead`,
`ReadApply`/`WriteApply`/`ListApplyAttempts`, `Lock`, `ReadSalt`/
`WriteSaltIfAbsent`) -- JSON marshal/unmarshal and corruption detection
stay exactly where they already lived. New `core/gitledgerstore.go`: the
git-directory reference implementation, today's exact filesystem code
moved verbatim (same paths, same PID-file lock, same temp+fsync+rename
apply durability). `core/ledger.go`/`core/apply.go`/`core/salt.go`
rewritten to delegate to `l.store`; `core/lock.go` deleted (content
moved). **Zero behavior change proved**: the full pre-existing test
suite passes unmodified; `lock_test.go`'s own direct tests of
`acquireLedgerLock`/`lockFilePath` mechanically retargeted at
`gitLedgerStore` (the only test file that touched unexported git-specific
methods directly -- every other existing test used the public `Ledger`
API or literal on-disk paths, unaffected).

**A real gap found and closed for both stores, not assumed away.**
`Append`'s existing duplicate check conflated "the proposal object
exists" with "this proposal was actually accepted" -- a crash between
writing the object and advancing the head left no path to recovery: a
retry reported `ErrDuplicateProposal` for a proposal whose head never
actually moved. Fixed store-agnostically in `core.Ledger.Append`: an
existing object is only a genuine duplicate if the head no longer names
its own `Parent` as current; otherwise `Append` resumes, verifying the
object's content before completing the head-advance. New
`TestLedger_InterruptedAppendResumes` (git) and
`TestStore_InterruptedAppendResumes` (blob) both prove it. Salt's own
first-use generation gained the identical race-safety
(`WriteSaltIfAbsent`'s create-only semantics, a real gap the original
plain read-then-write left open, never exercised by any prior test).

**`Head`/`AdvanceHead`: a genuine compare-and-swap for the blob store, not
an optimistic overwrite.** Confirmed directly against `gocloud.dev/blob`'s
own source before relying on it: `WriterOptions.IfNotExist` is honored
identically across every driver, returning `gcerrors.FailedPrecondition`
on conflict; `s3blob` implements it via S3's own native `If-None-Match: *`
conditional write (a real 2024-era S3 feature, not a client-side
check-then-write race). The design: every `AdvanceHead` call creates one
new, permanent, content-addressed `heads/<parent-id>` edge object --
never overwrites a mutable pointer -- so two callers racing to advance
from the same parent can't both win; resolving the current head is
"follow edges forward from a best-effort cached hint until one is
missing," never a directory listing. Locking uses the identical
create-only primitive for a TTL'd lock object -- an expired TTL, not a
dead PID, is the only staleness signal a multi-machine store can ever
have (git-local's own PID-liveness check has no equivalent once more than
one machine is involved); an expired lock is reclaimed via a best-effort
delete-then-retry, safe because the actual winner is still decided by the
create-only write underneath regardless of who deletes first.

**New `ledgerstore` package, kept out of `core` deliberately** (core stays
dependency-free, the same inversion `cloudtrail`/`gcpaudit`/`k8saudit`
already establish for their own heavy SDK dependencies): one generic
`Store` implementing `core.LedgerStore` against any `*blob.Bucket`, so
s3/gs/azblob all reach the identical code once `blob.OpenBucket` hands
one back. `Open`'s own path-style-to-query-param URI translation
(`s3://bucket/acme/prod/` → `s3://bucket?prefix=acme%2Fprod%2F`) is a
real, necessary translation layer, not a style choice -- confirmed
directly against `s3blob`'s `URLOpener` that no driver understands a
path segment as a prefix at all; only the generic `?prefix=` query
parameter does. **gs/azblob tried and deliberately backed out**: their
own driver packages pull in the full Azure SDK and GCP's own
monitoring/tracing/OpenTelemetry exporter stacks, checked directly via
`go mod tidy` (dozens of new transitive dependencies) before deciding --
real evidence the harness does not make them cheap in the way this
arc's own scope conditioned adding them on, not silently forced through.
Only `s3blob` is wired (10 lines in `go.mod`, 40 in `go.sum`).

**Conformance, hermetic then live, matching every adversarial row.** New
`ledgerstore/store_test.go`: the full suite run against a `memblob`
bucket standing in for every cloud (CAS races via real goroutines,
lock contention/TTL-expiry reclaim, interrupted-append resume, corrupted
proposal/head-edge/apply objects, `ListApplyAttempts` pagination
correctness, salt races) -- 17 tests, all passing. **Live, against real
S3** (the existing `ubx-states` bucket, a fresh key prefix per test,
cleaned up after): new `ledgerstore/internal/lockprobe`, a tiny real
subprocess, so lock contention and CAS races are proven across genuinely
independent OS processes, not goroutines sharing one process's memory --
`TestLive_S3_BasicRoundTrip`, `TestLive_S3_CASRace_RealConcurrentProcesses`,
`TestLive_S3_LockContention_RealConcurrentProcesses`, and
`TestLive_S3_LockTTLExpiry_RealReclaim` all pass against the real service.
A real, live-only finding along the way: the package's own default
lock-wait timeout (3s, copied from git-local's own tight local-file
retry budget) was too short for genuine real-network contention --
every poll here is an actual S3 round trip, not a local stat -- raised to
30s/250ms, confirmed against the real service before trusting the new
defaults.

**A first slice of the CLI wired, the rest named as follow-up.** New
`.ubx/config` `[ledger]` table (`store` key, cascade-validated,
`ubx init` templates updated in all three formats) and
`cli/ledgeropen.go`'s `openLedgerForStack` -- git/absent store unchanged,
a remote store opens at `<store>/<stack>/` and requires `stack`
non-empty (a real, deliberate API consequence: a remote store's own
per-stack chain means opening one at all requires knowing which stack
first, unlike git-local's flat, shared, stack-agnostic chain). Wired into
`ubx resolve` (stack from the intent file), `ubx accept` for local
acceptance (stack from the parsed proposal file -- `--from-merge`/the
PR-ceremony path untouched, matching this session's own design-only
scope for it), and `ubx ship` (a new `--stack` flag, since a bare
proposal ID carries no stack of its own to derive one from). **Live-verified
end to end**: a real `ubx resolve` → `ubx accept` → `ubx ship` chain
against a stack backed by real S3 (a fake-provider resource, so the
*infrastructure* is fake but the *ledger* is genuinely real S3, verified
independently by reading the exact `proposals/`/`heads/`/`applies/`
objects back via `aws s3api`), plus the `--stack`-required refusal firing
correctly when omitted against a remote store. `ubx why`, `ubx status`,
`ubx scan`/`--all`, `ubx revert-plan`, `ubx writeback`, `ubx propose`,
the MCP surface, and `accept --from-merge` all still open git-local
unconditionally -- queued, not forgotten (see STATE.md).

Full repo `go build ./...`/`go vet ./...`/`gofmt -l .`/
`go test ./... -race -count=1` clean throughout. ubiquex-docs updated the
same session: new `concepts/ledger-stores.mdx` (the `[ledger]` table,
addressing, the `--stack` consequence, real transcripts against the
built binary), cross-linked from `concepts/ledger.mdx`/`cli/config.mdx`/
`cli/ship.mdx`. `mint validate`/`mint broken-links` both clean. Both
repos committed and pushed.

Queued for a later session: Arc B's own remaining CLI wiring (the
commands named above), gs/azblob (if a lighter-weight path into their
own SDKs is ever found, or the dependency cost is judged acceptable
later), and the PR-acceptance ceremony's real implementation.

**Addendum (2026-07-19, filed under Arc B, substance is Arc A's own
config cascade): the cascade ceiling.** A design-room gap named directly:
the upward-walking cascade (Arc A, above) had no explicit stop rule at
all before this — it would walk every ancestor directory all the way to
the filesystem root, silently reading whatever `.ubx/config*` happened
to exist anywhere above a project, a real invisible-wrongness risk
exactly the provenance surface exists to mitigate but can't prevent by
itself. Three rules, checked in this order at every directory the walk
visits: `root = true` (a new, ordinary, cascade-merged, provenance-
tracked `Config.Root` key — editorconfig's own precedent, inclusive
stop, a non-boolean value is a hard error); no marker anywhere → the git
repo boundary (`.git`, directory or file, presence only, never read); no
repo either → `$HOME` or the filesystem root, reached naturally by the
same walk rather than a separate lookahead. `ubx config` now reports
which rule fired and where.

**User-global `~/.ubx/config` landed the same addendum**, structurally
outside the cascade walk entirely: allowlist-only (today, exactly one
entry, `init_format` — `ubx init`'s own default write format), every
other top-level key a **hard error**, never the normal cascade's own
"unknown keys warn" leniency, because a project-truth key (`stack`,
`providers`, `provider_configs`, `ledger`, the reserved future `intent`)
leaking in from a per-user file would mean the same commit resolves
differently on different machines — the exact correctness property this
whole design exists to hold. `ubx init --format`'s own default now falls
back to `~/.ubx/config`'s `init_format` before `hcl`, the first real
personal-preference key actually changing behavior.

**A real subtlety found by a hermetic test before it ever shipped:** if
`$HOME` itself turns out to be the cascade's own ceiling (no repo
structure above the invocation at all), `$HOME`'s config is already read
once as an ordinary cascade layer — consulting the identical file a
*second* time under the restrictive user-global allowlist would wrongly
reject a legitimate project-truth key that was never really a
"user-global" concern. Fixed by having the user-global loader compare
its own resolved file path against the cascade walk's already-consumed
layers and skip entirely on a match — a file is only ever one or the
other, never both.

New hermetic tests (`cli/configceiling_test.go`, 15 tests: root-marker
mid-tree stop with sibling-key survival, a non-boolean `root` hard
error, repo-boundary stop via both a `.git` directory and a `.git` file
(worktree/submodule pointer), the `$HOME` fallback, the filesystem-root
fallback, both user-global refusal shapes, the `init_format` positive
case plus `--format` still overriding it, and provenance rendering all
four ceiling reasons) plus a new `userHomeDir` package var (mirroring
`configSearchStartDir`'s own existing test seam, defaulted safely in
this package's shared `TestMain`). Live-verified against the real built
binary: a real nested directory tree with an actual `.git` directory,
a real `root = true` file mid-tree, and a real `HOME=...`-overridden
user-global refusal and `init_format` application. Full repo build/vet/
fmt/test clean throughout.

**Session 2 (2026-07-19): the rest of the primary CLI surface wired,
`$cross` addressing by stack name built, a real two-stack cross-stack
pin live-verified against real S3, the PR-acceptance ceremony reconfirmed
design-only.** `ubx why` (a bare proposal id gains a new `--stack` flag,
required only for a remote store, since unlike a resource address it
carries no stack of its own; the address branch derives it directly),
`ubx status` (its own existing `--stack` now genuinely required for a
remote store — "every stack" has no meaning once addressing is one chain
per stack, a real, honest, documented consequence, not glossed over),
`ubx scan` and `ubx scan --all` (already resolved `--stack` before
opening the ledger in both cases; `scanAllOptions` gained a `Config`
field so the filename-derived stack fallback still picks the right
store) all now read `.ubx/config`'s own `[ledger]` table exactly like
`ubx resolve`/`ubx accept` (local)/`ubx ship` already did. `ubx config`
gained a derived-address line (`ledger address (stack "payments"):
s3://...`) — the fully-resolved `<store>/<stack>/` address, not just the
raw configured `store` string, using the identical `--stack`/config
fallback every other command now shares.

**`$cross` by stack name, the addressing design's own last untested
piece, built.** `$cross`'s inner object gained `{"stack": "...", "to":
"..."}` as a mutually-exclusive alternative to `{"ledger_dir": "...",
"to": "..."}` (unchanged, permanent) — resolved against the current
stack's own configured `[ledger]` store, or a new `[ledger.external]`
table's override for that stack name. Built via dependency inversion,
not a parameter thread through `Resolve`'s own signature (avoiding a
40-call-site mechanical update this time): new `core.OpenRef` +
`core.RegisterRemoteLedgerOpener` (a small registry, `gocloud.dev/blob`'s
own `URLMux` the direct precedent), registered once by `cli`'s own
`init()`; `core.Ledger` gained `BaseStore()`/`ExternalStack()` accessors
and a new `OpenStoreForStack` constructor carrying that metadata, so
`core/resolver`'s own `resolveCross` (which already received the current
ledger as a parameter) needed no new parameter of its own, no signature
change to `Resolve`, and zero updates to any pre-existing test —
confirmed by running the full suite unchanged before writing a single
new test. `VerifyPins` and a destroy's own `known_dependents` orphan
check both moved to `core.OpenRef` too, uniformly. A real, corrected
design-doc sketch along the way: `ledger { external { network = ... } }`
(nested HCL blocks, from the original design room text) was never
actually parsed against `hclsyntax` until this session — corrected to
`ledger = { external = { network = "..." } }`, matching every other
config table's own attribute-object convention, since a stack name isn't
always a bare identifier the way `network` happens to be.

**Live finale, real S3, both required claims proven.** Two stacks
(`payments`, `networking`) under one real S3 base
(`s3://ubx-states/<prefix>/`): `networking` resolved/accepted/shipped a
real fake-provider resource; `payments` resolved a `$cross` by
`"stack": "networking"` (no `ledger_dir` anywhere in the intent file),
correctly recording `pinned_head` and the fully-derived real address
(`s3://ubx-states/<prefix>/networking/`) — verified independently by
listing the real bucket's own objects afterward, not just trusting
`ubx`'s own report. Accepted cleanly while `networking` hadn't moved;
a second `payments` proposal, resolved against the same pin, was then
correctly refused (`ErrCrossStackPinStale`, exit 1) once `networking`'s
real head genuinely advanced via a real accepted change — the exact
"neighbor advance staleness" claim docs/resolver-adversarial.md's row 5
already made for git-local, now proven for a real remote neighbor too.
Account left clean afterward (real S3 objects only — no real cloud
infrastructure was involved, the resources themselves were
fake-provider).

**PR-acceptance ceremony: reconfirmed design-only, its own future
slice.** No implementation this session, matching its own explicit
scope — `cli/accept.go`'s `acceptFromMerge` still opens git-local
unconditionally, confirmed by reading the code. docs/architecture.md's
own section reworded to name it precisely as the *one* remaining
git-local-only acceptance path, now that every other primary command is
wired.

New hermetic tests: `core/resolver/crossstack_addressing_test.go` (5
tests, using a shared in-memory bucket + a test-only fake remote opener
so two stacks genuinely share one base — `mem://`'s own URL opener hands
back a fresh, unshared bucket per call, confirmed before relying on
anything else); 3 new rows in docs/resolver-adversarial.md (11-13);
`cli/configcmd_test.go` (4 tests) for the derived-address line. Full
repo `go build ./...`/`go vet ./...`/`gofmt -l .`/
`go test ./... -race -count=1` clean throughout. ubiquex-docs updated
the same session. Both repos committed and pushed.

**Session 3 (2026-07-19): the PR-acceptance ceremony built exactly as
designed, and the arc's own remainder list closed.** Session 2's own
"reconfirmed design-only" scope line is exactly what this session
picked up. `cli/accept.go`'s `acceptFromMerge` gained a `cfg *Config`
parameter and now opens the stack's configured `LedgerStore` via
`openLedgerForStack` — the identical call local `ubx accept` already
made — right after `AcceptFromMerge`'s own git/GitHub verification
succeeds, never before it. No new mirroring mechanism exists: `Ledger.
Append`'s own `WriteProposalIfAbsent`+`AdvanceHead` CAS write was
already store-agnostic (Arc B session 1's own extraction), so opening
the right store at the right point is the *entire* change — confirmed
by reading the code before writing anything, not assumed.

**Hermetic adversarial rows (docs/ledgerstore-adversarial.md 13-16),
plus a genuine tooling detour finding its own root cause.** New CAS-race/
idempotency/tamper tests needed a store backend that genuinely shares
state across separate opens within one test, unlike `mem://` (confirmed
again, unshared per call) — `file://` was tried next and found broken
for a different reason: `ledgerstore.Open`'s own bucket+prefix transform
(designed for s3/gs/azblob's "host=bucket, path=prefix" shape) strips
`file://`'s own path into an unusable `?prefix=` param, silently
defaulting to relative-to-cwd writes instead of the intended directory —
confirmed directly by tracing where objects actually landed, not
assumed from the transform's own doc comment. Solved properly: a new
`openRemoteLedgerStore` package-level seam in `cli/ledgeropen.go` (same
convention as `configSearchStartDir`), letting tests inject one real,
held-onto `*blob.Bucket` (`memblob.OpenBucket`, not a URL) that every
`openLedgerForStack` call in a test resolves to. A second real
`gocloud.dev/blob` behavior confirmed directly along the way, not
assumed: `blob.PrefixedBucket` *consumes* its own `*Bucket` argument
(marks it closed as a side effect) and returns a new wrapper — a bucket
held directly and prefixed more than once becomes unusable for anything
but a fresh `PrefixedBucket` call; sidestepped by never prefixing at all
in a single-stack fixture.

**Live finale: a real GitHub PR, merged, mirrored into real S3, fully
reverted.** A real PR opened against `Ubiquex/ubiquex-cli` itself (via
`git worktree`, so this session's own uncommitted work-in-progress
changes were never disturbed), merged via `gh pr merge`, then `ubx
accept --from-merge` against a stack configured with a real S3 store —
confirmed genuinely mirrored via a direct `aws s3api get-object`, not
just `ubx`'s own report, at exactly the address the design predicts.
Cleaned up completely afterward: the merge commit reverted on `main`
(pushed directly, not through another PR — matching the prior UBI-11
live-verification session's own precedent), the scratch branch deleted
locally and on the remote, the S3 prefix emptied.

**The rest of the arc's own remainder list, resolved — with one real
correction to what that list actually was.** `ubx revert-plan`/`ubx
writeback` both gained the identical `--stack` flag `ubx why`/`ubx ship`
already have (a bare proposal ID carries no stack of its own). The MCP
surface's own `computeWhyJSON`/`computeStatusJSON`/`computeScanJSON`
(`cli/mcp_why.go`/`mcp_status.go`/`mcp_scan.go`) all gained the same
`openLedgerForStack` wiring — but a real, if narrow, divergence was
found first: these three functions' own doc comments claimed they were
still literally shared code with `ubx why`/`status`/`scan`'s own CLI
`RunE`, which stopped being true the moment those commands grew their
own `[ledger]`-aware lookups in an earlier session — the doc comments
were never corrected, so this session's own read of the code (not the
prior remainder list) is what found the MCP surface as the real last
gap, not just confirmed a known one. `ubx_why` gained a new optional
`stack` MCP input field to match. `ubx propose`, also named in the
prior session's own remainder list, was checked directly and found to
never touch a ledger at all (it only computes a hash from a file) —
removed from the remainder as a correction, not wired, since there was
never anything to wire.

New hermetic tests: `cli/accept_frommerge_remote_test.go` (5 tests),
`cli/mcp_remote_test.go` (4 tests), `cli/revertplan_writeback_remote_
test.go` (4 tests) — all against the shared-bucket seam above. Full
repo `go build ./...`/`go vet ./...`/`gofmt -l .`/
`go test ./... -race -count=1` clean throughout. docs/architecture.md's
own PR-ceremony section updated from "designed, still not built" to
built; docs/ledgerstore-adversarial.md gained rows 13-16 and its own
"what this table doesn't yet cover" trimmed accordingly. ubiquex-docs
updated the same session. Both repos committed and pushed. **UBI-32
closed** — the three remaining named gaps from session 2's own list
(gs/azblob live conformance, orphaned-proposal GC, a cross-stack pin
chain longer than one hop) were never part of this arc's own core scope
and remain open, named honestly in the closing Linear comment rather
than silently folded in or silently dropped.

### Intent provider + md medium (UBI-41)

Phase 3's opener — AI enters the product. Sequenced after destroys
(UBI-30) and multi-provider stacks (UBI-43), per the design-room decision
recorded above ("Phase 3 medium order reversed — markdown before SDK"):
both prior arcs remove the "one provider per stack" wall a markdown-
authored proposal would otherwise hit immediately. Sized ~3-4 sessions
(interface+adapter+conformance, md pipeline+ambiguity UX, docs+polish);
chat rides the same interface afterward for ~1 more session.

**Session 1 (2026-07-27): design only, no code — see the changelog entry
above for the full account.** docs/intent-provider.md (new, the full
design: the transcription-only boundary, the `Adapter` interface, the
`[intent]` config table, the ambiguity-as-visible-content design center,
redaction-at-capture, the conformance suite); docs/intent-provider-adversarial.md
(new, 8 required rows — the 7 named in the ticket's own handoff plus one
this session added, prompt injection embedded in doc content);
docs/schema.md's amendment (`Proposal.Intent`'s new
`assumptions`/`defaults`/`questions` fields, two new
`intent.sources[].kind` values — `document`, `intent_provider` — no
`schema_version` bump); docs/architecture.md's new headline section and
corrected `[intent]` config note. Sessions 2-4 (interface+Claude
adapter+conformance harness; the md pipeline live-verified against the
real Claude API; docs+polish) were still queued as of this session —
all three sessions are now built (below) and the arc is closed.

**Session 2 (2026-07-27): interface + Claude adapter + conformance
harness — real code, hermetic, one deliberate scope deferral, two real
findings.** New `intentprovider` package (`Adapter`, `DraftWithRetry`,
`IntentDraftJSONSchema`, `PopulateSources`); `core.Intent` gained the
three additive `Assumptions`/`Defaults`/`Questions` fields
docs/schema.md's own session-1 amendment pinned; `intentprovider/claude`
(the real adapter, new dependency `github.com/anthropics/anthropic-sdk-go`);
`intentprovider/conformance` (the fixture-runner harness, fixture #1 —
the payments doc — embedded via `go:embed`). Hermetic tests throughout
(a fully scripted fake `Adapter`, no network) prove `DraftWithRetry`'s
own retry-with-errors/hard-fail contract end to end, including recovery
on a second attempt and the exact prior-output/prior-errors feedback
loop; a `UBX_TEST_SLOW=1`-gated live test (`intentprovider/claude`)
wires the real adapter through both a direct `Draft` smoke test and the
full conformance suite.

One deliberate deviation from session 1's own slice-1 description, named
rather than silently made: `[intent]` config-cascade wiring
(`cli/configcascade.go`'s known-keys extension) was NOT built this
session — deferred to the md-pipeline session, since it would have no
consumer until `ubx propose --from-doc` exists to read it.

Two real findings, both documented in docs/intent-provider.md's own new
"Session 2" subsection: (1) Claude's own structured-output constraint
(every JSON Schema object node needs `"additionalProperties": false`,
even ones with no declared properties) makes a genuinely open-shaped
value like a resource's own `config` inexpressible as a nested object —
resolved by encoding it as a JSON string in the wire shape handed to the
model, decoded back into a real value by `validate.go`; flagged as
**not live-verified this session** (no Anthropic credentials in the
build environment), to be confirmed on the first real live run. (2) A
real bug found BY running the live test with no credentials present
(not assumed from reading the SDK's source): with zero credentials
resolvable, the SDK never reaches the server at all, so there's no
`*anthropic.Error` to branch a status code on, and `classifyError`'s
first cut silently lumped this under a generic "network/connection"
bucket — exactly the undifferentiated failure
docs/intent-provider-adversarial.md row 6 forbids. Fixed the same
session with a string-prefix check (the SDK's own typed sentinel for
this lives under an `internal/` package this module cannot import).

Full repo `go build ./...`/`go vet ./...`/`gofmt -l .`/
`go test ./... -race -count=1` clean throughout. No ubiquex-docs update
this session (still nothing user-visible — no CLI verb, no config key
actually read by any command yet). Both repos: code committed and
pushed. Session 3 (the md pipeline: `[intent]` config wiring, `ubx
propose --from-doc`, redaction-at-capture, live-verified end to end) is
next.

**Session 3 (2026-07-27): live-validated first, then the md pipeline
built — three real findings, full live finale twice.** Per this
session's own explicit instruction, session 2's own unverified
structured-output shape decision (`resources[].config` as a JSON-encoded
string) was confirmed correct against the real API before anything else
was built — a real credential was obtained, and the very first live call
round-tripped the config string cleanly. That same first call surfaced a
real bug: the model's own assumption text was the literal word
"placeholder," root-caused to the system prompt's own wording (which
described the `$ref` marker "as a placeholder") priming the model to
echo it — fixed by rewording the prompt.

`[intent]` config wiring (`cli/config.go`'s `IntentConfig`/`KeyRefConfig`/
`VertexConfig`, `cli/configcascade.go`'s known-keys extension,
`cli/intentadapter.go`'s `buildIntentAdapter` — a package-level DI seam
for hermetic tests, the same pattern `configSearchStartDir` already
establishes); `ubx propose --from-doc <file>.md --stack <stack>
[--out ...]`, a new mode on the existing `propose` verb (disambiguated
from its own pre-existing "hash a resolved proposal for a PR trailer"
mode — `--from-doc` and a positional argument are mutually exclusive,
checked); `intentprovider/redact.go` (pattern-based redaction-at-capture
— AWS/Anthropic/OpenAI-style key patterns, PEM blocks, Bearer tokens, a
generic labeled-credential heuristic — run before any network call
leaves this machine); `cli/intentrender.go` (the human-facing render of
`assumptions`/`defaults`/`questions`, printed before the raw JSON draft).

**A second, more serious bug found live**: a real draft's own
`intent.assumptions`/`.defaults` described concrete decisions about
`aws_db_instance.payments.instance_class` and related attributes in
full detail, while the draft's own `resources` array was completely
empty — nothing in `parseAndValidate` required the two to agree. Fixed
with a new hard validation rule (`len(resources) == 0 &&
len(destroys) == 0` is a rejection, forcing a retry) and a
strengthened system-prompt "every address you name must correspond to
a real resources[] entry" instruction; re-verified live twice more,
confirmed fixed both times.

**A third, genuinely surprising but non-bug finding**: one live run
returned a real safety-classifier refusal (`category: "bio"`) on a
completely innocuous database-provisioning smoke-test doc — the
adapter's own existing refusal-handling code worked exactly as
designed (a clear, distinct, non-retried error), so nothing needed
fixing; named honestly as a real reliability data point (this project's
own "publish real numbers" culture) rather than smoothed over. A
production deployment would reduce this via Claude's own server-side
`fallbacks` parameter — named as a real, concrete, explicitly
out-of-scope follow-up.

**Full live finale, twice, through the real built `ubx` binary**: the
payments fixture doc, a real `.ubx/config` `[intent]` table whose
`key_ref.env` named a deliberately non-default environment variable
(proving the config-cascade's own dereferencing path, not just the
SDK's own ambient-credential fallback), producing a complete,
correctly-provenanced draft (`document`/`intent_provider` sources, a
populated `resources` array matching every `affects` path named in the
assumptions/defaults). A second run — the identical doc with a
real-shaped (AWS's own public example) access key injected into the
prose — confirmed redaction-at-capture survives a genuine round trip:
the CLI's own warning fired, and a direct `grep` of the written draft
file found zero occurrences of the secret, not assumed from the
warning alone.

Hermetic tests throughout, no network in the default suite:
`intentprovider/redact_test.go` (every pattern, never leaks the
original text, always returns a fresh copy), `cli/intentadapter_test.go`
(`resolveKeyRef`'s three cases, `buildIntentAdapter`'s defaulting and
error propagation), `cli/propose_from_doc_test.go` (a fake
`intentprovider.Adapter` injected via `buildIntentAdapter`'s own DI
seam — draft-writing, ambiguity rendering, `--out`, `--stack` required,
`--from-doc`+positional-arg mutual exclusivity, redaction firing before
the adapter ever sees the raw content, adapter-error propagation).
`intentprovider/validate_test.go` gained the empty-resources regression
case. Full repo `go build ./...`/`go vet ./...`/`gofmt -l .`/
`go test ./... -race -count=1` clean throughout. No ubiquex-docs update
this session — deferred to slice 3 ("docs + polish," docs/intent-provider.md's
own Implementation slices), per protocol's docs-debt exception. Both
repos: code committed and pushed. **UBI-41 left open, not closed** —
slice 3 (ubiquex-docs, the per-adapter conformance report) is next.

**Session 4 (2026-07-28): ubiquex-docs, the per-adapter conformance
report, UBI-41 closed.** New `guides/md-medium.mdx` (the full walkthrough
— setup, a real transcript with all three ambiguity sections populated,
the redaction-at-capture demonstration with a direct `grep`-confirmed
zero-leak claim, "what happens next") and `guides/md-authoring-conventions.mdx`
(`@refs`, requirement phrasing, cost ceilings — guidance, not grammar,
matching docs/intent-provider.md's own posture), both in a new "AI-Assisted
Authoring" nav group. `cli/config.mdx` gained an `### intent` section;
`cli/propose.mdx` gained the `--from-doc` mode throughout (synopsis,
flags, a real transcript, the three real error cases — `--stack`
required, mutual exclusivity, an unresolvable `key_ref`). `concepts/proposal.mdx`
cross-links the new `assumptions`/`defaults`/`questions` fields. Every
transcript real, captured against the actual built binary through a real
Claude API call — none hand-written. `mint validate`/`mint broken-links`
both clean.

`docs/intent-provider-conformance-report.md` (this repo, matching
docs/reliability-report.md's own "internal engineering artifact, not
end-user docs" precedent): Claude's own real, published numbers — 5 of 6
fixture-suite runs passed; the one real failure named and explained, not
discarded from the count; all three real findings from this arc's own
live verification work (the system-prompt self-priming bug, the
"confidence isn't the test" gap, the empty-`resources[]` bug, and a real,
non-bug safety-classifier refusal) written up in one place. OpenAI/
Gemini/local rows named "not built," the report format ready for them.

**UBI-41 closed in Linear** — the closing comment names what stays open
honestly: chat rides this arc's own `Adapter`/`DraftWithRetry` interface
next, its own session, not started; OpenAI/Gemini/local adapters remain
on the roster, parked, no code. See STATE.md for the full session
account and the exact closing comment text.

### Chat medium (UBI-46)

Built 2026-07-28, one session, riding UBI-41's `Adapter`/`DraftWithRetry`
interface with zero changes to that interface — `DraftRequest.Content`
already being "just bytes" rather than "a file path" (UBI-41's own load-
bearing decision) is what made this true, not a new abstraction. `ubx
chat --stack <stack>`: an interactive loop, each turn re-drafts from the
full accumulated transcript via the same adapter; `/save` finalizes
(writes `dialogues/<hash>.dlg.json` and the draft), `/quit`/EOF abandons
(writes nothing — structural, not policy: the only write path is inside
`/save`'s own handler). Redaction runs per-turn at capture, never post-
hoc. `dialogues/` lands top-level, a sibling of `ledger/` — settled by
docs/architecture.md's own pre-existing "Ledger stores" decision naming
dialogues explicitly as an authoring medium that "always lives in git as
a repo asset," never coupled to the ledger's own swappable remote-store
backend. New `intent.sources[].kind: "dialogue"` entries pin the
captured file's hash; `ubx why --dialogue` walks change proposal → draft
→ the real conversation behind it. Four new adversarial rows built and
passing (docs/intent-provider-adversarial.md rows 9-12): secret pasted
mid-conversation (redacted, never reaches the adapter or the file),
contradictory turns (later turn wins, named in `intent.assumptions`),
abandoned session (zero orphan files, by construction), dialogue
tampering post-pin (existing content-hash mechanism catches it
unchanged, no new verification command). Live-verified against the real
Claude API twice: a real two-turn payments-stack conversation ("like our
staging database but smaller," then "make it multi-az") produced a real
captured dialogue, a real draft with real provenance, a real accepted
`change` proposal, and a real `ubx why --dialogue` render of the actual
turns; a separate real contradiction probe confirmed later-turn-wins
end to end. Both repos updated; **UBI-46 closed in Linear.** See
docs/intent-provider.md's own new "Amendment: the chat medium" section
and STATE.md for the full session account.

### SDK program: multi-language contract + TypeScript (UBI-33/34)

Designed 2026-07-28, session 1, docs-first, no code — docs/sdk.md (new)
is the full design; docs/architecture.md gained a matching cross-linking
headline section. Two hard constraints came pre-decided from the
ticket's own design room and were not relitigated: the monorepo (`sdk/`
inside `ubiquex-cli`, every language, one CI — golden conformance fixtures
are the shared spec, syncing them across repos would be misery) and
codegen'd bindings generated locally by `ubx sdk gen`, never published
(only the tiny `@ubx/sdk` runtime ever ships to npm — Pulumi's own
per-provider-package version-matrix pain, named explicitly as the
anti-pattern this sidesteps structurally). Language order: TypeScript,
then Go, then Python.

The contract: golden `intent/v1` JSON fixtures ARE the spec (UBI-33's own
framing), enforced as byte-identical-after-canonicalization — a new,
general-purpose canonical-JSON function (factored out of `core.Hash`'s
own JCS logic, not a second divergent implementation) makes "semantic
identity across languages" and "byte-identical" the same operational
claim. The `@ubx/sdk` describe-only runtime surface (`stack`/`resource`/
`secret`/`cross`/`intent`, `Computed<T>` as a branded reference — never a
real value, mirroring `$ref`'s own resolved-or-`$computed` split) and the
codegen design (real provider schema → a shared, language-neutral IR
model with exactly one deliberate rule — it only ever carries the
provider's real wire attribute name, no per-language identifier
convention baked in — → per-language templates) are both designed in
full.

**The hermetic evaluator was decided empirically this session, not from
documentation**: Node's `--permission` model, Deno, and `isolated-vm`
were each actually run against the real requirement (no net/fs/env/
clock) in this environment. Node disqualified outright — its permission
model has no flag that gates network or environment access at all,
confirmed against the real `--help` output and a real probe. Deno chosen
— closes fs/env/net by default with zero flags, plus one real gap found
and closed empirically: dynamic `import("https://...")` bypasses `--deny-
net` entirely (confirmed twice, once with zero flags and once with
`--deny-net` passed explicitly, same result both times) and needs `--no-
remote` specifically to close. `isolated-vm` recorded as the stronger-
but-costlier fallback — a bare V8 isolate has nothing host-provided by
default (no `require`/`process`/`fetch` exist at all, versus Node/Deno's
"everything exists, specific things are permission-gated"), but its
native build script didn't even run under this session's own npm
lockdown, and it has no native TypeScript support. `Date.now()`/`Math.
random()` were unblocked by all three (a JS-engine-built-in, not a host
resource any permission system treats as gate-able) — closed instead by
an eager global override inside the evaluator's own injected scope, with
`core.DoubleRun` (reused completely unchanged) as the backstop for
whatever the override can't foresee, run as two entirely separate `deno`
subprocesses (not two in-process calls), a stronger guarantee than the
resolver's own existing `DoubleRun` use.

A required-outcome adversarial table lives inside docs/sdk.md itself
(not a separate file, per this session's own explicit instruction):
nondeterminism, fs/env/net sandbox escape, the remote-import gap found
this session (its own row, not folded silently into the net row),
codegen against an unknown/mismatched provider version, a program
throwing mid-evaluation, and output exceeding the `intent/v1` schema —
each with a real required outcome, not a hope. Implementation sized at
7 named slices (docs/sdk.md's own "Implementation slices"): the shared
IR + `provider.Schema` translation; `ubx sdk gen` live-verified against a
real `hashicorp/aws` schema; the `@ubx/sdk` runtime; the evaluator
harness (this session's own adversarial table becomes its required test
program); `ubx resolve --from-code` CLI wiring (no resolver changes
expected — SDK is just another `intent/v1` producer); the conformance
harness's first golden case, deliberately reusing the existing md-medium
payments example as its own target shape (with one honest, structural
difference named: an SDK program has no interpretation step, so its own
`assumptions`/`defaults`/`questions` stay empty by construction — the
program's own source is the reviewable artifact instead); a live finale,
a real TypeScript payments program evaluated for real, converging with
the md medium's own resolved shape. docs/schema.md's own formal amendment
for the SDK's new `intent.sources` kind pair (`sdk`/`sdk_evaluator`,
mirroring `document`/`intent_provider`'s existing pairing) is deliberately
deferred to the slice that actually produces that content, not pinned
prematurely this session — named explicitly in docs/sdk.md so it isn't
forgotten. See STATE.md for the full session account, including this
session's own probe output.

**Session 2 (2026-07-28): slices 1–3 built — `sdk/codegen/ir`, `ubx sdk
gen`, `@ubx/sdk`'s own runtime.** Real code, real tests, real live
verification, not a design pass. `sdk/codegen/ir.FromSchema` translates
a real `provider.Schema` into the shared IR model, reusing `ctyjson.
UnmarshalType` (already a `provider` package dependency) rather than
hand-parsing raw ctyjson type specs; `sdk/codegen/templates/ts.
GeneratedFile` renders idiomatic TypeScript `Config`/`Attrs` interfaces
plus a runtime `ResourceBinding` descriptor per resource type. `ubx sdk
gen` (new `cli/sdk.go`, a parent command per docs/sdk.md's own naming)
reads `.ubx/config`'s `[providers]` table, reuses `provider.Acquire`
unchanged, and writes one file per declared source to `sdk/generated/`
by default (both CLI details docs/sdk.md's own "Out of scope" list had
left open, decided for real this session). `sdk/ts/runtime/src/index.ts`
builds `stack`/`resource`/`secret`/`cross`/`intent` and a `Computed<T>`
Proxy exactly as designed, with one refinement found while building it:
a declarative `FieldMap` data structure plus one shared runtime
serializer, not N generated `toConfig()` methods (docs/sdk.md's own
"Codegen design" section, corrected in place).

Two real bugs found by tests actually asserting on real output, not
caught by inspection: nested-block fields were silently excluded from a
resource's own `Config` interface entirely (a `NestedBlock`-derived
field carries no `Required`/`Optional` flags at all, a real schema fact
the first settability rule didn't account for); a `tags`-shaped
scalar-map field's own arbitrary keys were incorrectly run through the
same wire-name-translation path real config fields use, throwing
"unrecognized config field" on a key that was never supposed to be
translated at all. Both fixed, both now covered by regression tests
naming the exact failure mode. A third, unrelated inconsistency was
found and fixed in passing: docs/schema.md's own founding `$secret`
inner shape (`{"ref": ...}`) had never matched any real worked example
since UBI-27 (`{"backend", "path"}`) — corrected there, with the real
resolver code confirmed fully opaque to the inner shape either way.

Live-verified against real schemas, not fixtures: `ubx sdk gen` against
the real, already-cached `hashicorp/aws@6.54.0` (1,682 resource types,
zero errors, deterministic across reruns) and `hashicorp/time@0.9.2`
(4 types); the small `time` output then used as the real import target
of a hand-written TypeScript program that `deno check` type-checks and
`deno run` evaluates correctly — including a real same-stack `Computed<T>`
`$ref` between two resources — under the exact locked-down Deno flag set
this document's own evaluator section commits to. `go build/vet/test`
and `gofmt -l .` clean across the whole repo; `deno test`/`deno check`
clean for the runtime package. Next: slice 4, the evaluator harness
itself (the Deno subprocess wrapper, the `Date`/`Math.random` override,
`core.DoubleRun` wired in, this session's own adversarial table as its
required test program). See STATE.md and docs/sdk.md's own
"Implementation slices" section for the full account.

**Session 3 (2026-07-28): slice 4 built — the evaluator harness, real
`deno` subprocesses, all five in-scope adversarial rows confirmed.** The
session's own most consequential finding: session 1's `--allow-read`
question got re-litigated and settled for real, correcting BOTH session
1's own original speculation (a narrow carve-out needed) AND this
session's own first, too-easy re-probe (assumed no carve-out needed at
all, based on a FIXED script's static import — not the REAL,
parameterized shape the harness actually needs). Five isolated probes
pinned the actual rule: Deno's read permission gates a *dynamically*
computed import specifier (built from `Deno.args`, even via plain string
concatenation) but not a *literal* one, regardless of directory —
same-dir, `../sibling`, absolute path, and import-map-resolved bare
specifiers all load fine under full `--deny-read`. Fixed by having
`sdkeval/runner.go` generate a fresh runner script per evaluation with
the entry file's own absolute path baked in as a literal import — safe
specifically because `stack()` (slice 3) defers running a program's own
code to an explicit `.evaluate()` call, so import-time ordering doesn't
matter. Net result: the shipped evaluator flag set needs NO
`--allow-read` carve-out at all — stronger than either the original
design or this session's own first guess.

`core/canonical.go` gained the general-purpose `CanonicalJSON`/
`CanonicalJSONBytes` this document named as unstarted work, factored
cleanly out of `canonicalProposalBytes`'s own JCS logic (which now calls
it internally — one canonicalizer, not two). `sdk/ts/evaluator/guards.ts`
(new, harness-only, never published) is the eager `Date`/`Math.random`
override — a `Date` subclass that only blocks the zero-arg/`.now()`
forms, correctly leaving explicit-argument (fully deterministic)
construction legal. `sdk/ts/embed.go` (new `tsassets` package) embeds
guards.ts + the runtime source into the `ubx` binary itself; `sdkeval`
(new top-level Go package, resolving this document's own open "where
does the Go side live" question) extracts them once per process and
wires `core.DoubleRun` across two real subprocess launches exactly as
designed.

Row 5 ("output exceeding intent/v1 schema") needed a real correction
too: this document's own original text said it would reuse
`intentprovider.IntentDraftJSONSchema` — reading that package directly
found it to be a **deliberately different, incompatible shape** (a
JSON-string `config`, no `sources`, required-even-when-empty
assumptions/defaults/questions — shaped for an LLM structured-output
API, not for what `@ubx/sdk` actually emits). Corrected to strict-
unmarshal against `core/resolver.IntentFile` — the real Go type `ubx
resolve` already uses — plus direct structural checks, including a new,
real Go-side enforcement of this arc's own "op is always `create`"
decision. Documented as honest defense-in-depth, not a demonstrated live
bypass: `@ubx/sdk`'s own runtime (slice 3) already preemptively blocks
every one of these shapes at its own API boundary, so there's no way for
a normal program to reach this check with bad output through legitimate
use.

All five required rows confirmed against real `deno` subprocesses
(`sdkeval/sdkeval_test.go` + fixtures under `sdkeval/testdata/`): row 1
in both its layers (`Date.now()` caught by the eager guard immediately;
a **separate `Deno.pid`-leaking fixture**, which the guard can't and
shouldn't catch, confirmed `core.DoubleRun` genuinely detects the
resulting cross-subprocess mismatch — the concrete proof the two-layer
design's second layer actually does its job); row 2 (fs/env/net, each
blocked individually); row 2b (the session-1-found remote-import escape,
reconfirmed through the real end-to-end harness); row 4 (a program
throwing after one resource already ran — real message surfaced
verbatim, confirmed zero partial output). `go build/vet/test`/`gofmt -l .`
clean (20 new tests in `sdkeval`, 6 new in `core`); `deno test`/`deno
check` clean for `sdk/ts/evaluator`. Next: slice 5, `ubx resolve
--from-code` CLI wiring — `sdkeval.Evaluate` is already a complete, real,
tested `intent/v1` producer. See STATE.md and docs/sdk.md's own "Slice 4:
built" section for the full account, including every probe's own output.

**Session 4 (2026-07-28): slices 5–7 built — `ubx resolve --from-code`,
real conformance golden case, a real live convergence finale. UBI-34
closed.** `cli/resolve.go` gained `--from-code` (mutually exclusive with
the positional intent-file argument) -- CLI wiring only, exactly as
predicted, no resolver changes needed. `sdkeval/provenance.go` (new)
stamps a single `intent.sources: {"kind": "document", "ref", "content_
hash"}` entry, Go-side (the sandboxed evaluator can't hash its own
file) -- a real, deliberate simplification from this document's own
original "sdk"/"sdk_evaluator" kind-pair sketch: code has no LLM-adapter
analog worth a second entry, so it reuses the exact kind the md medium
already uses. `sdk/conformance/` built for real (`programs/ts/`,
`golden/`, `runner/`) -- a first golden case, `payments`, with a real,
ongoing Go regression test (`TestPaymentsGoldenCase_TS`) evaluating the
committed program through the real Deno harness and byte-comparing
against the committed golden fixture after canonicalizing both sides.

**The live finale was run for real, not approximated**: no committed
"golden" transcript existed anywhere from the md medium's own prior
sessions (drafts are ephemeral, never persisted, confirmed by checking
rather than assumed) -- so this session ran `ubx propose --from-doc
payments.md` against the real Claude API, fresh, got a real drafted
`aws_db_instance` (`db.t3.small`, 20 GiB, `payments_admin`, no `$ref` to
staging at all -- the intent provider has no ledger access to query it,
confirmed empirically), resolved it for real against the real
`hashicorp/aws@6.54.0` schema, then authored a TypeScript program with
the *identical* concrete values (copied from the real LLM output, not
invented independently), evaluated and resolved it too, and compared
both resolved `delta.creates[]` arrays through `core.CanonicalJSON` --
**byte-identical**. `intent.summary` matched too (copied verbatim); the
one honest, expected difference is `intent.sources`/assumptions/defaults/
questions, exactly as slice 6's own original design predicted, now
confirmed against real output rather than only asserted.

**UBI-34 closed in Linear -- TypeScript is complete**, all seven slices
built, tested, and live-verified. **UBI-33 stays open** -- Go (UBI-35)
and Python (UBI-36) are unstarted; this arc's own IR model and
canonical-JSON discipline are their shared foundation. `ubiquex-docs`
gained `cli/sdk-gen.mdx` (new), a new "Authoring in TypeScript" section
on `cli/resolve.mdx`, and a full rewrite of `sdk/index.mdx` from its old
"not yet released" placeholder -- every example real, taken from this
session's own live transcripts; `mint validate`/`mint broken-links` both
clean. `go build/vet/test`/`gofmt -l .` clean across the whole repo.
Both repos committed and pushed. See STATE.md and docs/sdk.md's own
"Slices 5–7: built" section for the full account, including the real
transcripts and the real byte-comparison.

### Diagram medium: D2 only (UBI-47) — closed

Designed 2026-07-28, session 1, docs-first, no code — docs/diagram-
medium.md (new) is the full design; docs/architecture.md gained a
matching cross-linking headline section. v1 scope is **D2 only** —
Mermaid and other formats deferred, each earning entry later via the
same conformance-fixture discipline every other pluggable surface
already uses. Go-native: `oss.terrastruct.com/d2` as a library —
confirmed this session, empirically, to offer a narrow parser/compiler/
formatter surface with none of the module's own heavy rendering
machinery pulled in when only those subpackages are imported.

**The lossy-medium rule, generalized from prose to structure**: a
diagram authors topology only (nodes → resources, containers → pure
visual grouping, edges → dependencies) — never attributes; annotations
render from ledger truth, never author into it. **No LLM anywhere in
this medium's path** — a node's type comes from a `class:` attribute,
resolved via `resolver.InferProvider` (UBI-43) completely unchanged,
the exact same schema-ownership inference a hand-written intent file's
own untyped `resources[].type` already gets. What's reused from UBI-41
is narrower than its adapter machinery: `core.Intent`'s own
`assumptions`/`defaults`/`questions` wire fields, now proven (design-
level, real code next session) to generalize to a deterministic parser's
own structural ambiguity, not only an LLM's interpretive one.

**A real trap found and avoided before it shipped**: the first, tempting
type-annotation design (an arbitrary custom key on a D2 shape) was
tested directly against the real `oss.terrastruct.com/d2` library before
committing to it — D2 has no free-form custom-key channel at all; any
unrecognized `key: value` inside a shape's body silently creates a
*nested child shape* instead, corrupting the topology rather than
erroring. The real, working mechanism is D2's own `class:`/`classes: {}`
keyword (its CSS-like styling-class system, repurposed by convention: a
class named after a real provider type string, with an empty body,
carries zero styling and IS the type) — confirmed by actually compiling
and round-tripping a real diagram, including a cross-stack reference
node (`@stack.type.name` as a **label**, never a D2 **key** — the same
kind of trap, `.` is D2's own container-nesting separator in a key path)
through the real library.

**`d2format.Format` is genuinely idempotent — confirmed directly, not
assumed**: format → re-parse → format again produced byte-identical
output. This is exactly the property `render --check`'s own byte-compare
contract needs, and it means ubx reuses D2's own canonical formatter
rather than hand-rolling one.

**A genuinely new, additive wire capability found by design, not
invented for convenience**: `ResourceIntent.DependsOn` (docs/schema.md's
own new amendment this session) — a topology-only dependency signal the
existing `$ref`/`$cross`-config-attribute-scanning mechanism has no way
to express, since a diagram edge names no attribute at all. Routes into
the *same* dependency graph the resolver already builds, so cycle
detection needs zero new code. A second, genuinely different hash from
`content_hash` is also named explicitly: the **topology hash**
(`core.CanonicalJSON` over resolved `resources[]`+`depends_on`,
excluding summary/sources/ambiguity content) answers "did the meaning
change," while `content_hash` (the raw file, styling included) answers
"were these the exact bytes parsed" — conflating the two was a real
wrong shortcut a first pass took, corrected before it became load-
bearing anywhere.

A seven-row required-outcome adversarial table is in docs/diagram-
medium.md itself, most rows citing an *existing* resolver-side
mechanism reused unchanged (cycle detection, `ErrAmbiguousType`,
`ErrDuplicateResource`, the existing content-hash tamper-detection) —
only styling-only-change and D2-parse-error handling are genuinely new.
Implementation sized at 7 slices toward a real `.d2` payments stack that
converges with the SAME golden values the md medium and the SDK arc's
own TypeScript program already converged on (UBI-33/34 session 4) —
four independent producers on one shared resolved shape, the complete
set this project's own "every medium is a projection" thesis promised.
See docs/diagram-medium.md's own "Implementation slices" and STATE.md
for the full session account including the real D2-library probe
output.

**Session 2 (2026-07-28): slices 1-2 built -- the topology parser
(`diagram/`, new top-level package) and `ResourceIntent.DependsOn`
(`core/resolver`), both real code, hermetically tested end to end.**
`DependsOn` landed exactly as designed, no corrections -- merges into
the *same* dependency graph `$ref`/`$cross` scanning already builds,
reusing `ErrRefNotFound`/`ErrRefToDestroyTarget` verbatim for a dangling
or destroy-conflicting dependency. The topology parser
(`d2compiler.Compile` -> a two-pass classify-then-translate walk ->
`resolver.IntentFile`) built exactly as designed too, with one real,
honest correction found while building it: this document's own original
text left "how a topology-only edge reaches a $cross marker" unresolved,
and on inspection it doesn't resolve at all -- `$cross`'s own wire shape
requires a specific config attribute, and a topology-only edge names
none, with no ordering-based substitute the way `DependsOn` provides for
intra-stack edges. **A cross-stack edge cannot express a real `$cross`
marker in v1** -- a genuine structural limit, not a bug; the reference
node is still fully recognized (type, `ledger_dir` resolved and
existence-checked), and an edge into it becomes a visible, non-blocking
note instead, matching this arc's own ambiguity-as-content design center
applied to a structural limitation rather than an interpretive gap. Two
end-to-end tests (`diagram/integration_test.go`) confirm real `Parse`
output, resolved through the real, unmodified `resolver.Resolve`,
actually triggers `ErrCycleDetected`/`ErrDuplicateResource` -- the
adversarial table's own "reused, not reinvented" claims for rows 1 and 3
proven, not just asserted. `go build/vet/test`, `gofmt -l .` clean (8 new
tests in `core/resolver`, 16 new in `diagram`). See docs/diagram-
medium.md's own "Slices 1-2: built" section and STATE.md for the full
account.

**Session 3 (2026-07-28): slice 3 built -- `ubx propose --from-diagram
<file>.d2 --stack <stack>` CLI wiring (`cli/propose.go`), matching
`--from-doc`'s own shape and flag conventions exactly.** Read the .d2
file, parse it via the real, unmodified `diagram.Parse`, populate a
single `"document"`-kind `intent.sources` entry (`intentprovider.
HashDocument`, reused unchanged -- the same single-entry precedent the
SDK arc's own `stampDocumentSource` established), render whatever
ambiguity content the parse produced (`cli/intentrender.go`'s existing
`renderAmbiguity`, zero new rendering code needed since it already
operates on the same `*resolver.IntentFile` type `diagram.Parse`
returns -- the `$cross` structural-limitation note found in session 2
surfaces here exactly as designed, an ordinary `defaults[]` entry),
write the draft. No corrections to session 2's own design -- pure CLI
glue. Two-step, not one-step: stops at a written draft the same way
`--from-doc` does, never auto-resolving, since a diagram parse can
produce real, visible ambiguity needing a human-review checkpoint first
-- deliberately not `--from-code`'s own one-step shape. No legacy
single-provider fallback, matching `ubx sdk gen`'s own precedent (both
are post-UBI-43, multi-provider-only features); a new, standalone
`loadDiagramProviders` helper rather than a `cli/resolve.go` refactor --
out of scope for a CLI-wiring slice to touch that existing, tested code
-- closing each provider client immediately after fetching its schema
(confirmed safe via `newSchemaInspector`'s own no-live-client-dependency
implementation, a deliberate improvement over `resolve.go`'s own
hold-open-until-exit pattern). Five hermetic CLI tests (`cli/
propose_from_diagram_test.go`): missing `[providers]`, missing
`--stack`, three-way mutual exclusivity, a real end-to-end run via the
`UBX_PROVIDER_MIRROR` seam (`cli/sdk_test.go`'s own mechanism) proving
sources/ambiguity/`--neighbor-ledger` all wire correctly, and an
unambiguous-diagram/`--summary`-override case. Also live-verified by
hand against a real built binary. `go build/vet/test`, `gofmt -l .`
clean. See docs/diagram-medium.md's own "Slice 3: built" section and
STATE.md for the full account.

**Session 4 (2026-07-28): slice 4 built -- the emitter (`diagram.Emit`,
new `diagram/emit.go`) and `ubx render --stack <stack> [--out <path>]
[--check]` (new `cli/render.go`), the render half of the medium and the
literal converse of `Parse`.** `core.Ledger.Fleet(stack)` + `FoldState`
walk (the same read `ubx status`'s own fleet walk already performs) ->
deterministic D2 source text -> `d2parser.Parse` -> `d2format.Format` for
the canonical byte form, one flat top-level node per live resource, no
synthetic containers, exactly as designed. **A real, load-bearing gap
found while building this slice, not present in session 1's own
design**: the render direction's own text assumed
`resolution.inputs[].pinned_head` alone was enough to annotate a `$cross`
edge, but reading the real `resolveCross`/`resolveOnce` code (not
assumed) showed a `cross_stack_pin` entry's own `resource` field has
always named the *neighbor's* address, never the *local* resource whose
config held the `$cross` marker -- and every resource's own resolution
inputs get flattened into one proposal-wide slice with no back-reference
at all. Fixed at the source rather than worked around in the emitter:
`core.ResolutionInput` gained a new, additive `From` field (the
referencing resource's own address), `resolveValue`/`resolveCross`
(`core/resolver/refs.go`) both gained a `from string` parameter threaded
from `resolveOnce`'s own per-resource loop -- purely additive, no
`schema_version` bump, same reasoning as every prior amendment to that
struct (docs/schema.md's new "Amendment: `ResolutionInput.From`"). One
hermetic regression test proves the attribution is genuinely
per-resource, not merely "a pin happened somewhere in this proposal."

Real, deliberate rendering decisions, named rather than left implicit:
synthetic `r0`/`r1`/... D2 keys (never the resource's own name, since two
different-typed resources can legally share a `Name`, and a dotted
`type.name` key would collide with D2's own container-nesting separator
-- the exact trap the canonical-subset section already found and avoided
on the parse side); attribute annotations via `tooltip:`, not
`label:`/a suffix; **no per-resource cost annotation** -- checked
directly before assuming the design's own "cost, where a resource's own
recorded cost data exists" line was implementable, and it isn't yet: no
per-resource cost field exists anywhere in the ledger (`core.CostDelta`
is proposal-level only, presently always hardcoded to `"0"` at every call
site) -- named here explicitly, not silently skipped; reference nodes
deduplicated by neighbor address (two resources pinning the same neighbor
share one reference node); a depends_on/cross-pin lookup that degrades
gracefully (a resource whose creating proposal recorded neither simply
renders without edges, never a hard failure for the whole diagram).

`TestEmitD2_RoundTripsThroughParse` (`diagram/emit_test.go`) feeds
`Emit`'s own output back through the real, unmodified `Parse` and
confirms resources/`depends_on`/the `$cross` note all come back
correctly -- real proof of "render/parse share one convention, not two,"
not just each direction tested in isolation. Nine unit tests
(`diagram/emit_test.go`, `emitD2` exercised directly) plus ten CLI tests
(`cli/render_test.go`, a real resolve -> accept -> ship pipeline against
the hermetic `fakeprovider` binary via `UBX_PROVIDER_MIRROR`), including
a real two-ledger cross-stack scenario proving the `From` fix works end
to end, not just at the `emitD2` unit level. `go build/vet/test`,
`gofmt -l .` clean across the whole repo.

**A real, costly mistake made and corrected this session, recorded
honestly**: initial by-hand live verification of `ubx render` ran the
full `resolve -> accept -> ship` pipeline against the real,
already-credentialed `hashicorp/aws` provider instead of the hermetic
`fakeprovider` mirror -- unlike `resolve`/`propose` (read-only
schema-fetch, safe against a real provider), `ship` actually applies,
and this created three real AWS VPCs and started a real RDS instance in
the user's live account. Caught by checking real AWS state directly
before going further; all four resources confirmed and deleted with the
user's explicit go-ahead (paused the session, asked before any deletion).
Every real transcript in docs/diagram-medium.md's own "Slice 4: built"
section and in ubiquex-docs' render guide/reference page comes from a
redone, fully hermetic live verification against `fakeprovider` instead.
A standing feedback memory now records: never run `ubx ship` against a
real provider for verification purposes, only `fakeprovider` +
`UBX_PROVIDER_MIRROR`. See docs/diagram-medium.md's own "Slice 4: built"
section and STATE.md for the full account, including the incident.

**Session 5 (2026-07-28): slice 5 built — `diagram.Topology`
(`diagram/topology.go`, new) and conformance fixtures, `payments` as
fixture #1, both directions, hermetic throughout.** `Topology` is the
"topology hash" concept's own first real code (previously only prose):
`core.CanonicalJSON` over `resources[]` (type, name, op, depends_on) +
stack, sorted by `(type, name)` internally so its own determinism never
depends on caller ordering, excluding `intent.summary`/`sources`/
ambiguity/`config` entirely. Conformance fixtures deliberately split
across two packages, not an oversight: the parse direction
(`diagram/conformance/golden/payments.d2` ↔ `payments-topology.json`,
tested in new `diagram/conformance/runner/`, mirroring `sdk/
conformance/runner`'s own shape) is fully self-contained — `diagram.
Parse`'s own type inference only ever calls `SchemaInspector.HasType`,
never needing a real provider subprocess at all; the render direction
(the identical topology shipped for real through the hermetic
`fakeprovider` binary, emitted, compared against new
`diagram/conformance/golden/payments-rendered.d2`) lives in
`cli/render_conformance_test.go` instead, since `Emit` needs a real,
shipped `Fleet` entry — which needs the full `core/executor.Applier`
adapter `cli/stateadapter.go` already owns correctly, and reimplementing
a second copy elsewhere would risk a real, silent divergence for no
benefit over importing the same golden fixtures by relative path.

**A real, deliberate, documented departure from every other medium's own
"payments" fixture**: both golden `.d2` files use `fake_widget`
throughout, never `aws_vpc`/`aws_db_instance` — this session's own
explicit "hermetic only, no real cloud" instruction, a direct, standing
consequence of session 4's own real AWS incident. Conformance fixtures
are exactly the kind of "just checking it still works" context where a
verification session's own scope tends to creep toward `ship` without
that being the actual intent; using `fake_widget` throughout removes the
temptation structurally rather than relying on discipline alone.
Reconciling this fixture's own values with the `aws_*` golden values
every other medium already converged on is explicitly named as slice 6's
own job, not this one's.

**The standing ship-verification rule from session 4's own incident, now
codified where every future session actually reads it**: CLAUDE.md's own
"Code conventions" and docs/prompts.md's own "Rules embedded in every
session" both gained a line naming the rule directly — `ubx ship` (or
anything else reaching a provider's own `ApplyResourceChange`) is never
run against a real cloud provider for verification, only the hermetic
`fakeprovider` binary via `UBX_PROVIDER_MIRROR`; `resolve`/`propose`/
`sdk gen` remain safe against a real provider. Previously only a standing
memory outside this repo — now project doctrine a future session (or a
different agent entirely) reads automatically.

Eight new tests (`diagram/topology_test.go` ×5, `diagram/conformance/
runner` ×2, `cli/render_conformance_test.go` ×1, plus the existing suite
unchanged). `go build/vet/test`, `gofmt -l .` clean across the whole
repo. See docs/diagram-medium.md's own "Slice 5: built" section and
STATE.md for the full account.

**Session 6 (2026-07-28): slice 6 built — the live finale, real end to
end, closing the arc. UBI-47 closed in Linear.** Two independent legs,
per this session's own doctrine: convergence against real schema
(`resolve`/`propose` only, never `ship`), render fully hermetic (needs a
real `ship`, so `fakeprovider` + `UBX_PROVIDER_MIRROR` only).

**Convergence leg**: `db: payments { class: aws_db_instance }`,
`ubx propose --from-diagram` + `ubx resolve`, real against the real,
cached `hashicorp/aws@6.54.0` schema. Real `delta.creates[0]`:

```json
{"config":{},"name":"payments","provider":{"source":"hashicorp/aws","version":"6.54.0"},"stack":"payments","type":"aws_db_instance"}
```

Checked rigorously (`core.CanonicalJSON` on both sides, not eyeballed)
against the SDK arc's own committed golden value
(`{"config":{"allocated_storage":20,"db_name":"payments","engine":"postgres","instance_class":"db.t3.small","username":"payments_admin"},"name":"payments","provider":{"source":"hashicorp/aws","version":"6.54.0"},"stack":"payments","type":"aws_db_instance"}`):
`name`/`stack`/`type`/`provider` byte-identical; `config` the one
honest, structural, expected difference — a diagram was never going to
independently reproduce real attribute values, by design, since
session 1's own "two mediums can never claim the same attribute"
framing. `diagram.Topology` (slice 5, reused unchanged, zero new code)
confirms the same at the topology-only layer the medium is actually
scoped to: `{"resources":[{"name":"payments","op":"create","type":"aws_db_instance"}],"stack":"payments"}`,
matching the golden's own topology-relevant fields exactly. Verified
directly afterward that no real AWS resources exist — the convergence
leg never shipped anything.

**Render leg, fully hermetic**: the `payments` chain (`main-vpc`,
`payments-db` depending on it) shipped for real through `fakeprovider`,
rendered, `render --check` green — real, unedited:
`render --check: rendered/payments.d2 matches the current resolved
state`.

**"The four-medium equality" precisely stated, not overclaimed**: not a
fourth independent producer of the same attribute values (structurally
impossible for a topology-only medium, by design), but proof a diagram
never contradicts what the other three producers established, correctly
identifying the same resource by type, name, stack, and provider — the
only form of convergence honestly available to this medium, and exactly
what "every medium is a projection, never a second source of truth"
always meant.

A real, small doc-staleness finding fixed while closing, not left for a
future session to notice: docs/architecture.md's own md-medium/SDK-
program/diagram-medium headline sections each still carried their
session-1 "designed... not yet implemented" markers, unrevised across
every session that actually built them — fixed in place for all three.
No new code this session (verification, not implementation); existing
test suite unchanged, still green. See docs/diagram-medium.md's own
"Slice 6: built — the live finale" section and STATE.md for the full
account, including every real transcript.

## Phase 3 status: all four authoring mediums live

Phase 3 opened with UBI-41 (docs/plan.md's own 2026-07-17 design-room
decision, above: "AI enters the product") and closes here, session 6 of
UBI-47, with the fourth and final medium live. Each entry names its own
ticket status precisely — a medium can be *live* (proven, real, usable
today) while its own umbrella ticket stays open for later, demand-gated
expansion; the two are not the same claim.

| Medium | Ticket | Status | Entry point |
| --- | --- | --- | --- |
| Markdown (prose, LLM-transcribed) | UBI-41 | **Closed** | `ubx propose --from-doc` |
| Chat (interactive dialogue) | UBI-46 | **Closed** | `ubx chat` |
| SDK (typed code) | UBI-33/34 (TS) | **UBI-34 closed** — TypeScript live; UBI-33 (the multi-language umbrella) stays open for Go (UBI-35)/Python (UBI-36), demand-gated, unstarted | `ubx sdk gen` + `ubx resolve --from-code` |
| Diagram (D2 topology) | UBI-47 | **Closed** | `ubx propose --from-diagram` + `ubx render` |

Four independent `intent/v1` producers — a hand-written file, an
LLM-transcribed document, a typed program, and a diagram — proven,
session by session, to converge on one shared resolved shape (or, for
the diagram medium's own topology-only scope, to never contradict it):
the founding "every medium is a projection, never a second source of
truth" thesis, demonstrated four times over, not just stated once at the
start. Mermaid/other diagram formats, Go/Python SDK languages, and any
future medium each earn their own entry the same way — the conformance-
fixture discipline every medium above already used, never a shortcut.

## Deferred (explicitly not now)

a real policy engine (UBI-27's resolver carries a policy-stub hook,
always empty for now), environments/promotion, Nexus SaaS, naming of
proposal ledger format for external publication.

~~diagrams~~ — **designed, UBI-47 session 1** (see its own wedge
subsection below and docs/diagram-medium.md); parser/emitter/CLI *code*
is still session 2+ work of that ticket, not deferred any longer as a
design question. Mermaid/other formats and the Studio-style live canvas
stay deferred (docs/diagram-medium.md's own "Out of scope").

~~SDK + codegen~~ — **designed, UBI-33/34 session 1** (see its own wedge
subsection above and docs/sdk.md); Go/Python's own evaluators (UBI-35/36)
and all runtime/codegen/evaluator *code* are still session 2+ work of
these tickets, not deferred any longer as a design question.

~~Intent provider, markdown intents (an LLM-authored `intent/v1` draft,
never resolves/computes/touches a ledger or provider)~~ — **designed,
UBI-41 session 1** (see its own wedge subsection above); interface,
adapter, and CLI *code* are still session 2+ work of that ticket, not
deferred any longer as a design question.

~~`delta.destroys` for any proposal kind (needs its own adversarial
thinking — a create can be retried safely, a destroy usually can't;
UBI-27 above is creates+modifies only, not this)~~ — **designed, UBI-30**
(see its own wedge subsection above); resolver/executor *code* is still
session 2+ work of that ticket, not deferred any longer as a design
question.

~~A shipped `change` proposal's creates becoming `ubx status`/`ubx why
<address>` discoverable~~ — fixed, UBI-29 (see its own wedge subsection
below).

## Execution topology (decided 2026-07-17, revised same day)

The invariant that holds unqualified on every tier: **Nexus can never
apply anything a human didn't sign** — execution, wherever it runs,
consumes only accepted, hash-bound proposals; Nexus holds no signing
authority and cannot mint acceptance.

Customer-facing execution modes (customer chooses):

1. **Agent (self-hosted)**: customer-operated ubx-agent (UBI-28,
   parked), customer credentials, Nexus coordinates and observes.
   The zero-inbound-access mode for security-sensitive buyers.
2. **Managed agent**: Nexus operates the agent's lifecycle (config,
   upgrades, health, scheduling); the container runs inside the
   customer's environment; credentials never cross the boundary.
   Control plane ours, data plane theirs (the GitHub Actions runner
   model).
3. **Nexus-hosted execution ("Nexus Runs")**: Nexus-operated runners
   execute accepted proposals — the convenience tier (click-to-ship,
   zero customer-side setup). Guardrails, non-negotiable: credentials
   via OIDC dynamic federation ONLY (customer cloud trusts the runner
   identity per-workspace, short-lived tokens — stored access keys are
   never offered under any mode); per-tenant ephemeral runners; the
   runner is ubx-agent operated by Nexus, one codebase; execution
   consumes signed hashes only, identical to modes 1–2.

Trust framing per mode, stated honestly in security docs: modes 1–2
carry "Nexus cannot touch your cloud"; mode 3 carries "Nexus can only
execute what you cryptographically approved" — qualified, disclosed,
never blurred.

Customer AWS access for Nexus's own (read-only) features: ExternalId-
scoped cross-account IAM role, reviewable as code, read-only permissions
plus `cloudtrail:LookupEvents` — or agent-push mode with zero inbound
access for customers whose security posture requires it. Never stored
access keys, never write permissions outside mode 3's OIDC-federated
runner sessions.

Corollary: UBI-28 (ubx-agent) is the execution engine of all three
modes — self-hosted, managed, and Nexus-operated are one binary with
three operators. Its unparking should be evaluated against the Nexus
timeline, not standalone.

## Risks being managed

- Category creation cost → wedge is findable pain ("terraform drift"), not a new
  concept to explain.
- Solo-founder scope → slices are small, sellable, compounding; giants ignore
  narrow wedges.
- Executor trust → deferred; wedge reads and records before it ever writes.
  Adversarial reliability testing becomes the credibility engine when the
  executor lands (publish results, Jepsen-style).
