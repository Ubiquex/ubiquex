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

Still queued: `core/executor`'s own client pool and per-node dispatch
(hermetic against a fake `Applier` pool scripting docs/multi-provider-
adversarial.md's remaining rows — 4, 6, 7), `.ubx/config`'s own
`providers` table wiring (rides UBI-19's existing loader, doesn't block
on UBI-32), CLI surface changes (`--source`/`--provider-version`
deprecation staging, never a breaking cutover in one session), and the
live finale: a real payments-shaped stack (`hashicorp/aws` + a second
real provider) shipped as ONE signed proposal on real infrastructure.

## Deferred (explicitly not now)

SDK + codegen, chat/intent provider, diagrams, markdown intents, a real
policy engine (UBI-27's resolver carries a policy-stub hook, always empty
for now), environments/promotion, Nexus SaaS, naming of proposal ledger
format for external publication.

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
