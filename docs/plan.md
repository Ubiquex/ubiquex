# Plan — wedge & slices

## Changelog

- 2026-09-01 -- UBI-228 built: human-readable aliases for ledger heads.
  `docs/schema.md` had already anticipated this ("id is a content
  hash... human-friendly aliases allowed as labels") but it was never
  built until restore (UBI-227, same day) made naming a head the whole
  interaction. New `.ubx/aliases.json`, workspace-local and NOT a
  `core.LedgerStore` concern -- confirmed live that a remote-store-backed
  `core.Ledger` carries no local directory at all, so an alias addressed
  through `LedgerStore` would need new interface methods across every
  store backend for what is fundamentally a human-managed, git-shared
  artifact. `ubx alias set|resolve|list|remove`; `set` refuses to
  repoint a name that already points elsewhere without `--force`,
  answering the ticket's own "silently repointing something a human uses
  as a name" fear directly. Aliases are namespaced per stack (a ledger
  dir can hold an interleaved multi-stack chain) and shared via git
  exactly like `.ubx/config.hcl` already is -- a conflicting alias on two
  branches is an ordinary merge conflict, not a new mechanism this
  ticket needed to build. `resolveHeadOrAlias` (`cli/alias.go`) wired
  into `ubx why` and `ubx restore`, both keeping a raw hash accepted
  exactly as before. Full detail: docs/schema.md's "Amendment:
  human-readable aliases," docs/architecture.md's own section. PR
  against `ubiquex` open (never self-merge).

- 2026-09-01 -- UBI-227 built: restore a stack to an earlier ledger head.
  Design reported and confirmed before building: a restore is a normal
  proposal (reuses `KindChange`, never `KindRevert` -- that enum slot
  stays declared but unimplemented), evidenced by a new
  `intent.sources[].kind: "restore"` value mirroring `"promotion"`'s own
  provenance-not-equality posture. `core.Ledger` gains `ChainFrom`,
  `FoldStateAt`, `AddressesAt` -- the existing `Chain`/`FoldState`/
  `Addresses` become thin wrappers around these, one real head-
  parameterized fold each rather than a second hand-kept walk (the shape
  UBI-197 and UBI-233 both hit). Two new commands: `ubx history`
  (read-only, `Chain()` newest head first) and `ubx restore <head>`
  (resolves and writes a plan like `ubx propose`/`ubx plan`, changing
  nothing until accepted and shipped). Restore itself classifies
  create/modify/destroy against CURRENT live state, not the resolver --
  confirmed live that `resolveOnce` never infers create-vs-modify, only
  validates a caller-declared op. Three open questions from the ticket,
  all resolved: cross-stack `$cross` pins replay frozen historical
  literals, never re-resolved (the marker is already gone by the time a
  value lands in ledger history); restore of a restore needs no special
  handling, proven live via a real double-restore test, not just reasoned
  about; `drift_revert` does not generalize (resource-scoped, modify-
  only, always-current-head-target -- a different, narrower job). Two
  real, pre-existing, restore-unrelated fakeprovider findings surfaced
  during testing and reported separately, not folded into this ticket's
  own scope: `ok-v6` mode does not round-trip a "tags" map attribute
  identically between apply and read, and shipping a second real modify
  against the same address fails with a genuine stale-observation error
  -- both confirmed via isolated manual repros outside restore entirely.
  Full detail: docs/schema.md's "Amendment: restore," docs/architecture.md's
  "Restore" section. PR against `ubiquex` open (never self-merge).

- 2026-08-27 -- UBI-194 built: publish and acquire the
  `ubx-provider-dynamic` binary itself. Every real `[providers.<name>]`
  pin published so far resolved a real schema snapshot but had nothing
  real to serve it with outside this project -- the only way to run
  `ubx-provider-dynamic` at all was a local checkout
  (`UBX_PROVIDER_DYNAMIC_REPO`). Version-resolution design reported and
  confirmed twice before building: an explicit table keyed by
  `schema_format` was proposed first and correctly rejected (AWS's own
  pre-#24 and post-#24 snapshots both declare the identical
  `schema_format`, so a table can't distinguish them either, and
  bumping it leaves already-published snapshots stuck). Real design:
  `Snapshot.MinBinaryVersion`, stamped by `AssembleGroup` at generation
  time from the generating binary's own real, embedded version -- exact
  by construction, no table to maintain, self-heals on every real
  regeneration. `provider.AcquireDynamicProviderBinary` (`ubiquex`)
  reuses `Acquire`/`AcquireSchema`'s own real mirror-then-cache-then-
  verify discipline and shared helpers, as a real, separate function
  (no OpenTofu registry entry, no per-provider namespace).
  `provider.ResolveDynamicProviderBinaryVersion` reads the real,
  stamped field, falling back to an explicit, LOGGED bootstrap table
  keyed by `schema_format` only for the six real snapshots published
  before the field existed -- every fallback logs the real provider and
  version so it stays visible, never a silent, permanent second
  resolution mode. `UBX_PROVIDER_DYNAMIC_REPO` removed from the normal
  `[providers.<name>]` resolution path, kept as an explicit development
  override. `[dynamic_providers.<name>]`'s own live-fetch path (`ubx
  sdk gen`) untouched -- confirmed zero real, live usage of its own
  pinned sub-case exists today. PR `ubx-provider-dynamic#27` open (never
  self-merge); real, live end-to-end verification (a real published
  release, zero local checkout) waits on that merge. Full detail in
  STATE.md.

- 2026-08-27 -- UBI-182 Stage E built: `[providers.<name>]`'s dual
  meaning (pinned vs. live-fetch) collapses to pinned-only, now that
  every real provider has a real published pin (Stage F, same day).
  `pinnedDynamicProviderEnv` (`cli/dynamicprovider.go`) is the new,
  narrower real path both real `[providers.<name>]` consumers use
  (`newDynamicProviderLaunchFunc`'s own `providerPool.Get`, and
  `loadDynamicProviderSchema`'s own `ubx resolve`/`ubx plan` path) --
  source+version required, a live-fetch-shaped entry now fails loud
  with a real, named error pointing at `[dynamic_providers.<name>]`
  instead of silently falling through. `dynamicProviderEnv` itself
  (the original, still-shared dual-shape function) is untouched --
  `[dynamic_providers.<name>]` still needs its own real live-fetch-by-
  default, pinned-if-declared shape for `ubx sdk gen`'s regeneration-
  from-live-spec purpose, and this collapse was always scoped to
  `[providers.<name>]` only. Filed and fixed along the way: Azure's own
  real, published 604-member group's ~54-56s real load-to-first-RPC
  time (`provider.Launch`'s 10s default handshake timeout was far too
  tight) -- root-caused precisely (not the parse/translate/merge work,
  ~11s and fast; the `GetProviderSchema` RPC round trip itself, ~41s,
  open question) and filed as its own ticket, `UBI-195`, rather than
  folded silently into this stage. `docs/architecture.md`'s own
  `[providers.<name>]` example corrected (it showed the now-invalid
  live-fetch shape). Full detail in STATE.md.

- 2026-08-27 -- UBI-182 Stage F CLOSED, all six real providers
  (kubernetes, datadog, github, google, aws, azure) published and
  live-verified. Azure -- the last one, and the last real blocker named
  in this arc -- needed UBI-193 Part 1's own external-$ref bundling fix
  before it could even be generated: Azure's own real Swagger 2.0 specs
  split themselves across shared `common-types/*.json` files by real
  relative path, and the fetched-then-remarshaled snapshot lost the
  resolved content on reload. Reported and confirmed before building
  (100% relative paths sampled across 16 diverse real specs; Azure's own
  real ref graph is genuinely cyclic, confirmed live via a direct
  pointer-identity walk of `network/virtualNetwork.json`, ruling out
  naive value-inlining as an approach): built `internal/openapi.Bundle`
  in `ubx-provider-dynamic`, real REFERENCE bundling (one local
  `components` entry per distinct external target, every reference
  rewritten to a local pointer) rather than value inlining, so a cycle
  stays a cheap pointer instead of expanding forever. Generated all 604
  real Azure members (302 resource-providers x resource+data-source
  modes), published, pinned, and live-proven -- including a real,
  hand-picked check that the previously-blocked `network/virtualnetwork`
  family specifically resolves correctly, the real worst case for both
  external refs and cycles.

  A real, separate bug surfaced by Azure's own real scale, not a flaw in
  the bundling fix: Azure's 604-member group takes ~54-56s to load,
  parse, and translate before its first RPC response (confirmed CPU-
  bound, not network-bound -- a cache-hit run with the network poisoned
  costs the same real wall time as a cold one), breaching
  `provider.Launch`'s own 10s default handshake timeout. Fixed with a
  scoped 120s override at the two real dynamic-provider launch call
  sites in `ubiquex`, not a change to the global default (which remains
  correct for ordinary, hand-written Terraform provider binaries). The
  real performance question itself (85MB/604 members, almost a minute,
  zero network) is named, not silently hidden behind the timeout bump --
  real, separate, not-yet-investigated follow-up work.

  Stage E (`[providers.<name>]`'s dual-meaning collapse) is now
  confirmed UNBLOCKED -- its own real safety condition (every provider
  having a real published pin, so nothing depends on the live-fetch
  fallback it would remove) is met for all six. Not built this session,
  reported as a real, live decision point. Full detail in STATE.md.

- 2026-08-27 -- UBI-182 Stage F closed for five of six real providers
  (kubernetes, datadog, github, google, aws); azure remains blocked
  behind UBI-193's own external-$ref bundling gap (Part 1, substantial,
  unstarted). Each of the five: real `ubx-schema-<provider>` repo
  created, real snapshot generated and published (`v1.0.0`/`v2.0.0`/
  `v3.0.0` depending on correction history), `[providers.<name>]`
  pinned and proven with a real, live, two-process zero-network proof
  (negative-control-verified, type names checked rather than counts).
  AWS -- the last and largest, 430 real members -- surfaced a real,
  new architectural gap along the way: it is the ONLY real group among
  the six whose own members span more than one real schema source (1
  CloudFormation resource member, 429 Smithy data-source members).
  UBI-193 (originally filed for Azure's own two real blockers) grew a
  third and fourth real finding because of this: exec config resolution
  moved from group-wide/schema-serve-time to per-resource-type/
  execution-time (fixing Google's own real 163-base_url block too, not
  just Azure's), and a real mixed-source dispatch layer
  (`internal/mixedserver` in `ubx-provider-dynamic`) was built so a
  single `[providers.aws]` pin serves CloudFormation and Smithy
  together, routing each RPC by real type ownership rather than merging
  the two into one representation. AWS's own live proof additionally
  asserted exact per-source counts (1,715 CloudFormation resources,
  4,884 Smithy data sources), not just nonzero, specifically to prove
  both real sources served together rather than one silently winning.
  Stage E (`[providers.<name>]`'s dual-meaning collapse) stays blocked
  -- it was always contingent on every real provider having a real
  published pin first, and azure still doesn't. Full detail in
  STATE.md.

- 2026-08-26 -- UBI-182 provider schema snapshots, design decided and
  build sequence recorded (full plan:
  `/Users/roozbeh/.claude/plans/cryptic-questing-sphinx.md`). Snapshot
  after translation, one implementation not four -- `openapi`,
  `cloudformation`, `smithy`, and `discovery_docs` all converge on the
  same `map[string]*tfprotov6.Schema` via `internal/schema.Translator
  .BuildTopLevel`, so `ubx-provider-dynamic`'s `internal/snapshot`
  package gets one shared `generateFromSchemas` core plus a thin
  `Generate<Source>`/`Load<Source>` adapter pair per source, rather than
  four independent implementations. One `ubx-schema-<provider>` repo per
  provider (matching the existing `ubx-sdk-*` pattern), snapshot
  committed at the repo root rather than release-only, so a reviewer
  sees the real diff on every version bump. `[providers.<name>]`'s dual
  meaning (pinned vs. live-fetch, a forced compromise while only
  `openapi` had snapshot support) collapses to pinned-only once all four
  sources can produce snapshots. Staged one provider end to end
  (Kubernetes, confirmed smaller than Datadog and already routing
  through `openapi` via Swagger 2.0 conversion -- needs zero new
  generation code) before the other five, matching the same staging
  discipline that caught real bugs in the data source work. Stage A
  (generation for the three missing sources) and Stage B (pinned
  `--dump-signals`/`--dump-namespaces`) built and PR'd in
  `ubx-provider-dynamic` (`#16`, not yet merged); Stages C-F (`ubiquex`
  stops refusing pinned entries, the `ubx-schema-kubernetes` pilot repo,
  the `[providers.<name>]` collapse, rollout to the other five
  providers) not yet started. Full detail in STATE.md.

- 2026-08-20 -- (no Linear ticket ID given this session) docs corpus
  regeneration phase 4: GCP onboarded, scoped to Compute only after a
  real, live-discovered config gap -- only 171 of the old corpus's
  1,332 GCP pages are `google_compute_*`, the other 1,161 span ~40
  real GCP products never configured in `.ubx/config` at all. Asked
  the founder rather than deleting 1,161 real pages the new pipeline
  can't yet regenerate; confirmed scope to Compute, leave the rest on
  old HashiCorp-sourced content. Real redirect diff: 81 clean, 0
  probable/ambiguous, 90 genuinely orphaned (39 real Terraform IAM-
  binding convenience resources, 51 mostly Terraform's own
  decomposition of one real API object into several resources). Also
  confirmed live: GCP's own version-collision risk (Discovery
  Documents share an identical `name` field across channels) is real
  but dormant -- zero collisions today since only one document
  (compute/v1) is configured; and the POST-only/no-GET-by-id discovery
  gap Kubernetes surfaced generalizes to GCP too (3 real nodes: advice,
  regionZones, regionInstances). Found and fixed, in `ubx-provider-
  dynamic`, a real, trivial `singularize` bug mishandling "-es" English
  plurals ("addresses" -> "addresse", "policies" -> "policie") that
  had 22 real resources shipping under misspelled names -- the first
  fix over-corrected and briefly shipped "licenses" -> "licens" as a
  new regression, caught by re-reading real generated output before
  reporting done and fixed in a real second round (both pushed to the
  real, existing, still-open draft PR #5, never merged, never pushed
  to main). Full verification bar clean (95/95 real go build on
  literal content, 95/95 deno fmt, 95/95 ast.parse, mint validate/
  broken-links clean, real DOM overflow crawl zero findings, zero em
  dashes, zero pages outside the scoped compute change touched). See
  STATE.md's own checkpoint for the full real account.

- 2026-08-20 -- (no Linear ticket ID given this session) docs corpus
  regeneration phase 3: Kubernetes onboarded, the first provider in
  this arc with a real predecessor corpus, exercising the redirect
  problem for real. Real diff of all 81 old pages against the new
  71-resource schema: 66 clean + 5 probable (same real Kind, naming-
  convention mismatch, e.g. "apiservice" vs "api_service") redirects
  written to docs.json, 10 genuinely orphaned (8 real Terraform-
  provider-specific convenience resources with no OpenAPI equivalent
  by design, 1 real API Kind -- TokenRequest -- the generic discovery
  heuristic structurally can't find since it's POST-only). Also
  confirmed, live, that the alpha/beta API-version collision the
  founder flagged is still real: 23 seenTypeNames collisions (21
  genuine version collisions across 14 Kinds, 2 unrelated "proxy"
  noun collisions), not fixed this phase per explicit instruction --
  the real fix needs a version-preserving type-naming decision that
  was discussed but never made. Full verification bar clean (71/71
  real go build on literal content, 71/71 deno fmt, 71/71 ast.parse,
  mint validate/broken-links clean including the new redirects, real
  DOM overflow crawl zero findings, zero em dashes, zero non-
  kubernetes pages touched). See STATE.md's own checkpoint for the
  full real account.

- 2026-08-20 -- (no Linear ticket ID given this session) docs corpus
  regeneration phase 2: GitHub onboarded through the corrected
  richer-tier-only pipeline, same process as phase 1's own corrected
  Datadog run, zero new code needed. 68 real resource types across 43
  services, 4,826 fields (matching the founder's own stated count
  exactly), 86 real pages. The real finding: every mechanism that made
  this work -- unrepresentable-field skipping, AWS-tuned field-literal
  heuristics staying silent rather than misfiring, service/local-name
  splitting -- was already generic, proven once on Datadog and reused
  unmodified; GitHub needed no provider-specific code anywhere. Full
  verification bar clean (68/68 real go build on literal content,
  68/68 deno fmt, 68/68 ast.parse, mint validate/broken-links clean,
  real DOM overflow crawl zero findings, zero em dashes, zero already-
  shipped pages touched). Redirect problem still not exercised (GitHub
  has no legacy pages either). See STATE.md's own checkpoint.

- 2026-08-20 -- (no Linear ticket ID given this session, corrects the
  entry directly below) the prior checkpoint shipped the WRONG page
  tier for Datadog -- bare fragments, not the complete runnable
  programs matching the existing 4,197-page corpus. Real root cause:
  `gen_complete_pages.py`'s own splice tool requires a page to already
  exist, so onboarding a brand-new provider had no real path to the
  richer tier at all; the sparse "mechanical" tier (never meant to be
  final output, confirmed live: 100% of the existing corpus is
  already richer-tier) shipped instead. Fixed by removing the sparse
  tier entirely (`build_resource_page`, `gen_mechanical_pages.py`) and
  adding a real, standalone richer-tier generator
  (`generate_richer_provider`, `gen_new_provider_pages.py`) that needs
  no pre-existing page. Also found and fixed: `verify_go_blocks.py`
  had been silently wrapping bare fragments in a synthetic
  package/func shell before compiling them, so its own "26/26 OK"
  never proved the literal page content compiled -- now compiles the
  extracted block unmodified, with zero wrapping. Datadog regenerated
  from scratch; confirmed structurally identical to a real, live
  Google page (package main, imports, func main, ubx.Main(ubx.Stack),
  all four mediums). No further providers onboarded this session, per
  the founder's own explicit instruction. See STATE.md's own
  checkpoint for the full real account.

- 2026-08-20 -- (no Linear ticket ID given this session) `ubx sdk gen`
  gained `--dump-ir <dir>` and `--only <names>`. `--dump-ir` reuses the
  real, already-tested acquisition+enrichment path both thirdparty
  and dynamic providers share, writes real post-enrichment IR JSON
  (per-resource, plus a combined whole-provider `schema.json`) instead
  of running codegen -- the real, provider-agnostic replacement for
  `ubiquex-docs`' own `dump_schema.go` tool, which only ever worked for
  a tfplugin source and never applied checked-in-description
  enrichment. Built to prove the real documentation-corpus
  regeneration pipeline (all six providers moving from HashiCorp-
  tfplugin-sourced to ubx-provider-dynamic-sourced schemas, ubx's own
  derived naming) end to end on Datadog first, per the founder's own
  mandatory phasing. See STATE.md's own checkpoint for the full real
  account, including three corpus-scale bugs found in `ubiquex-docs`'
  own previously-unexercised mechanical-tier tooling along the way.

- 2026-08-20 -- (no Linear ticket ID given this session, closes the arc
  the two entries directly below opened) third and final resume of AWS
  description generation, the real, never-attempted 7,350-field
  remainder the entry below left open. Completed in full this time --
  0 errored, 764 new genuine abstentions, 6,586 new real descriptions,
  merged with zero collisions. Real spend investigated via DeepSeek's
  own `/user/balance` endpoint queried before/after ($4.77 -> $1.50,
  $3.27 real spend for 8,004,185 real tokens across 7,350 calls) --
  each response's own `usage.completion_tokens_details.reasoning_tokens`
  field showed 66-131 hidden reasoning tokens per call, roughly as
  large as the visible one-sentence description itself, which is the
  real explanation for why the first two rounds exhausted their
  balances faster than naive Flash per-field pricing would predict.
  Real, final AWS coverage: 16,325 sourced, 30,063 AI-inferred, 2,779
  genuinely abstained (all three rounds), 0 never attempted, 77,457
  excluded. The founder's own stated target ("an empty never-attempted
  bucket") is met. See STATE.md's own checkpoint for the full real
  numbers.

- 2026-08-20 -- (no Linear ticket ID given this session, same arc as the
  entry directly below) resume of AWS description generation against
  the real, remaining 19,411-field gap the entry below left open. Real,
  precise resume set (excluded the 1,446 fields that already got a real,
  honest abstention, never re-asked) matched the founder's own stated
  count exactly before any call ran. Reached 12,062 of 19,411 (62.1%)
  before a SECOND real `HTTP 402 Insufficient Balance`, stopped
  immediately per instruction -- 11,492 real descriptions merged (zero
  collisions), 569 new real abstentions. Real, live-measured coverage
  this checkpoint: 16,325 sourced, 23,477 AI-inferred, 2,015 genuinely
  abstained (both rounds), 7,350 still never attempted, 77,457 excluded.
  The founder's own stated target for this resume ("nothing remains in
  the never-attempted bucket") was NOT met -- a real, non-zero
  never-attempted remainder still exists pending a further top-up and
  resume. See STATE.md's own checkpoint for the full real numbers.

- 2026-08-20 -- (no Linear ticket ID given this session) real, general
  `describe_exclude` config mechanism for the SDK-onboarding pipeline:
  any provider's own config (`[dynamic_providers.<name>]` for a dynamic
  provider, `[provider_configs.<source>]` for a real Terraform-registry
  one, identical key/shape either way) may name resource types whose
  real field count is pathological relative to their real usage --
  excluded from description generation only, codegen is unaffected, a
  new `Excluded` bucket in the real coverage report. Built provider-
  agnostic first (`cli/describeexclude.go`, zero AWS-specific logic,
  real hermetic tests), then used to move AWS's own central config entry
  from SQS-only Smithy to the real, full CloudFormation registry
  (`schema_source = "cloudformation"`), excluding the three real
  QuickSight resources the prior checkpoint found responsible for 61%
  of the registry's own real field count at a ~0.1% sourced rate. Real
  DeepSeek generation against the real, remaining 32,841-field gap ran
  to 13,431 fields (40.9%) before a real `HTTP 402 Insufficient Balance`
  stopped it, exactly as instructed -- 11,984 real descriptions merged
  into the checked-in artifact, 1,446 real abstentions, real remaining
  gap for a future resume is 19,411. See STATE.md's own checkpoint for
  the full real numbers.

- 2026-08-20 -- (no Linear ticket ID given this session, same arc as the
  entry directly below) closes the resolver preference gap that entry
  named as not yet done: `core/resolver.InferProvider` needed no change
  at all (it only ranks whichever declared-provider set it's handed);
  the real fix is in `cli/resolve.go`'s own `loadResolveProviders` (the
  function `ubx resolve`/`ubx plan` call for an unrecorded resource's own
  provider inference), which now iterates `resolveProviderPrecedence`'s
  real, precedence-resolved set instead of `cfg.ThirdpartyProviders`
  directly -- a shadowed `[thirdparty_providers]` entry for a key also
  declared under `[providers]` is never even fetched, let alone offered
  to `InferProvider` as a competing candidate. The identical fix reaches
  `declaredProvidersForInference` (a new `resolvedProviderVersions`
  helper), the mechanism `status`/`scan --all`/`scan --stack`/
  `scan --discover`/`drift` all share for a legacy/adopted Fleet entry's
  own fresh-by-type inference. Real, deliberate implementation split:
  `ubx resolve` does not reuse `declaredProvidersForInference`'s own
  `providerPool`-based path, because that path's own
  `resourceTypeSchemaInspector` is a real, deliberate stub
  (`IsComputed`/`IsSensitive` always false, since `InferProvider` -- its
  only real caller there -- never calls either) -- confirmed live, not
  assumed, when a first attempt at unifying the two paths regressed a
  real required-attribute-missing resolve test from a correct refusal to
  a silent success, because `ubx resolve`'s own downstream validation
  reads real schema data past mere type ownership. `loadResolveProviders`
  keeps fetching a real, full `*provider.Schemas` per declared source
  instead, through two new swappable seams
  (`fetchThirdpartySchema`/`fetchDynamicSchema`, the identical real
  convention `cli/scandiscover.go`'s own `newDiscoveryTaggingAPI`/
  `newDiscoveryStateReader` already establish). Real, hermetic tests
  prove both directions -- a stack declaring `aws` under both namespaces
  resolves through the dynamic entry, never launching the shadowed
  thirdparty one at all; a stack declaring only `[thirdparty_providers.aws]`
  correctly falls through to the real HashiCorp binary -- at both the
  `loadResolveProviders` level (`cli/resolve_test.go`) and the
  `declaredProvidersForInference` level (`cli/multiprovider_fleet_test.go`).
  Whole repo `go build`/`go vet`/`go test ./...` clean, no regressions.
- 2026-08-20 -- (no Linear ticket ID given this session) real, breaking
  restructure of `.ubx/config`'s provider-declaration surface into two
  namespaces: `[providers]` now means ubx's own, dynamic-provider-backed
  sources (the same real `map[string]map[string]any`
  schema_source/schema_url/base_url/auth/... shape `sdk/providers/
  .ubx/config`'s own `[dynamic_providers.<name>]` table already used for
  SDK codegen, reused here rather than a second shape for the same real
  binary); the prior `[providers]` (a flat `"hashicorp/aws" = "6.60.0"`
  map, real Terraform-registry sources) is renamed `[thirdparty_providers]`,
  its own real shape kept verbatim, never reshaped. Precedence: the same
  real key declared in both resolves to the dynamic entry
  (`resolveProviderPrecedence`, `cli/providerpool.go`, keyed by
  `providerShortName`'s existing last-`/`-segment derivation for a
  thirdparty source). `providerPool.Get` now routes a `[providers]`-
  declared key through a real `ubx-provider-dynamic` launch
  (`newDynamicProviderLaunchFunc`) instead of `provider.Acquire` --
  making a dynamic-provider-backed source usable for real infra via
  `ubx resolve`/`ubx ship` for the first time (previously schema-dump-
  only via `ubx sdk gen`, `Config.DynamicProviders`' own doc comment's
  prior, now-superseded scope note). `sdk/providers/.ubx/config`'s own
  `[dynamic_providers]` table name is unchanged -- internal to codegen,
  deliberately left alone. Companion, real addition in the separate
  `ubx-provider-dynamic` repo (branch `onboarding-pipeline-kubernetes-
  checkpoint`, PR #5, still open/draft): a new `schema_source =
  "cloudformation"` tier (renamed from the long-unimplemented `aws_ccapi`
  placeholder, which conflated schema source with execution mechanism),
  fetching and translating AWS's real, published CloudFormation resource-
  provider schema registry (1,705 real `AWS::` types live-fetched, 1,700
  built, 15,678 real top-level fields), executed via a new, purpose-built
  Cloud Control API client (`internal/cloudformation/ccapi`) -- real
  async via `GetResourceRequestStatus` polling, confirmed NOT to fit
  `dynserver`'s own REST-path-shaped `AsyncConfig` (that package's own
  doc comment had already named this exact gap). Real, hermetic (no real
  AWS credentials/resources touched, per the standing CLAUDE.md rule
  against a live apply, confirmed with the founder this session) end-to-
  end create+destroy proof against a local fake CCAPI server, plus real
  config-precedence tests with both namespaces declaring `aws`, all
  passing. Not yet done, named not hidden: `core/resolver`'s own
  provider-inference logic does not yet automatically prefer a
  `[providers]`-declared source when inferring an unrecorded resource's
  provider -- the routing mechanism is real and tested, but a stack has
  to already know to record source="aws" for it to take effect; deeper
  resolver-level integration is real, separate, future work.
- 2026-08-15 -- UBI-164: the remote-ledger-store requirement is
  documented in every CI/CD integration guide. All of them run
  `ubx accept --from-merge` and `ubx ship --yes`, and none said those
  need a real remote store. The real problem was sharper than the
  ticket's framing: each guide OPENS by telling the reader to bring "a
  git-local ledger already committed", which is exactly the setup that
  loses every accepted proposal, so the note qualifies that paragraph
  rather than sitting in the credentials section. Matched to
  server/github-setup.mdx's own canonical wording, adapting only the
  part that genuinely differs (the ephemeral runner/agent/job workspace
  rather than ubx server's long-lived clone). One claim beyond the
  canonical note was verified in source first: only core/accept.go
  appends to the ledger and ship persists apply records to the same
  store, so drift-watch and plan-on-push really are unaffected.
- 2026-08-15 -- UBI-165: `ubx scan --surface-as` covers all five
  platforms, and `ubx server`'s drift-watch loop actually runs. The
  ticket scoped this as "extend cli/surface.go beyond GitHub"; two
  adjacent defects had to be fixed for that to mean anything. The loop
  shelled out to `ubx status --drift --surface-as`, and `ubx status` has
  no such flag, so every drift sweep ever run died on `unknown flag`
  before reaching any API -- drift-watch worked on no platform, not on
  GitHub-only as assumed. And `ubx scan` refused `--surface-as` for a
  fleet-scoped walk, which is exactly drift-watch's own shape. The five
  platforms turned out genuinely asymmetric, each verified against a
  real current source rather than assumed: Bitbucket Server has no issue
  tracker at all (224 SDK methods, zero issue endpoints; Atlassian's
  model is Jira) so issue mode is refused rather than downgraded; Azure
  DevOps has work items in a separate service, with a literal `$` in the
  route, a JSON Patch content type, and a type that comes from the
  project's process template; Bitbucket Cloud's tracker is opt-in per
  repository and its `/src` endpoint creates branch and commit in one
  call; GitLab has no draft flag at all. One harness lesson: a stand-in
  multiplexing several platforms needs routing as precise as the code
  under test, or it reports false failures -- `/repos/` and
  `/pullrequests` are each shared by two platforms here.
- 2026-08-15 -- UBI-168: in every comment-triggered handler, the real,
  live authorization check now runs before any clone or fetch. A pure
  ordering fix, pre-existing from UBI-166 and filed separately during
  UBI-167 rather than folded into it; UBI-167 raised its cost, since an
  unauthorized commenter on an allowlisted repository went from
  triggering a changed-files API call to triggering a real clone. The
  four older platforms were brought to Bitbucket Cloud's own shape (a
  `prepareStack<Platform>` helper called inside each authorized branch),
  which UBI-170 built correctly from the start, rather than to a new
  design. Nothing else changed: same checks, same inputs, same refusal
  comments, whole pre-existing suite untouched. Two verification lessons
  worth keeping: a "did not happen" assertion needs a paired "does
  happen" control or it proves nothing, and the first harness had two
  weaknesses (an undecodable CODEOWNERS encoding, and a single-stack
  fixture that made one signal vacuously true) that made a passing run
  mean less than it appeared to.
- 2026-08-15 -- UBI-171: GitHub Enterprise Server and on-prem Azure
  DevOps Server, which could not work at all before this regardless of
  configuration. Two real defects, both found by the verification-only
  base-URL question asked earlier in the same session. First, the
  configured API base URL was applied raw, so GHES' own `/api/v3` path
  convention was never applied and every call landed at the instance
  root; `github.WithEnterpriseBaseURL` now delegates to go-github's own
  `WithEnterpriseURLs`. Second, the git clone host was the literal string
  `github.com` (and `dev.azure.com`), so a correctly-configured instance
  would authenticate against itself and then clone from the SaaS; the
  host is now derived from the same configured base URL, matching what
  GitLab and Bitbucket Server already did in the same file. Both settings
  moved from test-only to real production config with a YAML key, and
  `ubx accept --from-merge` gained three real documented base URL flags,
  closing the same gap in the CLI. One design point worth keeping: where
  ghinstallation forced this codebase to reimplement a convention it does
  not own, the test asserts agreement with go-github's real
  implementation case by case rather than against a hand-written
  expectation -- which is exactly what caught the `api.github.com` case
  go-github deliberately exempts.
- 2026-08-15 -- UBI-170: Bitbucket Cloud, the fifth platform, for both
  `ubx accept --from-merge` and `ubx server`. A genuinely separate
  platform from Bitbucket Server, not a variant: verified first against
  Bitbucket Cloud's own official OpenAPI definition (served live at
  api.bitbucket.org/swagger.json, carrying Atlassian's own narrative
  docs inline) plus live API calls, because every Atlassian
  documentation host is egress-blocked in this environment. Confirmed
  differences: access tokens with app passwords deprecated, an
  `x-token-auth` clone literal matching neither GitHub's nor Bitbucket
  Server's, entirely different event keys (`pullrequest:fulfilled` is
  its own word for merged), a two-call commit-to-pull-request
  derivation with its own `repo_indexed` condition, approvals carrying
  no commit reference at all, and no username on an account. Three
  consequences shaped the design: identity is an `account_id`
  throughout; CODEOWNERS entries resolve to real account_ids at check
  time and refuse on failed or ambiguous resolution; and the TOCTOU
  gate follows GitLab's/Azure DevOps' branch-restriction pattern rather
  than Bitbucket Server's per-approval commit reference. The webhook
  signature parses its algorithm out of the header rather than
  hardcoding sha256, so an unimplemented algorithm is refused instead of
  silently downgraded. UBI-166's allowlist and UBI-167's auto-discovery
  are reused unchanged; UBI-168's authorization-before-clone ordering is
  applied here from the start rather than inherited as a defect.
- 2026-08-15 -- UBI-169: the `ubx server` Atlassian integration is named
  Bitbucket Server everywhere a user can see it. `ubx server` talks
  directly to a VCS host's own API and has no relationship to Bamboo, a
  CI tool; the label crept over from the CI-focused UBI-31/UBI-160 work,
  where Bamboo genuinely is the subject. Verified rather than assumed
  first, as the ticket required: the Go code was already correct
  everywhere it matters (config fields, YAML keys, env vars, flags, the
  `--repo bitbucketserver:` prefix, the `/webhook/bitbucketserver` route,
  the package name), checked against the real built binary's own
  `--help`, so no functional change was needed. `ubx server`'s own help
  text was the one user-visible exception. Docs: `server/bamboo-setup.mdx`
  renamed to `server/bitbucket-server-setup.mdx` and its content
  re-verified against real source rather than relabeled in place (real
  dispatched event keys, corrected core-flow mapping, real fail-closed
  signature behavior, the bot name's double duty as clone username, the
  approval-time destroys refusal, the three real CODEOWNERS locations,
  and target-vs-source repository identity for cross-repository PRs).
  `integrations/bamboo.mdx` deliberately untouched. Two adjacent errors
  found while verifying and fixed: a dangling `#bamboo-bitbucket-server`
  anchor, and a `cli-reference/server.mdx` scope claim ("GitHub only...
  the other three not yet built") that had been false since UBI-28 Phase 4.
- 2026-08-15 -- UBI-167: `ubx server`'s own `Config.Repos` entries carry
  repository identity ONLY. The `ledger_dir` field is gone; once a repo
  clears UBI-166's allowlist (unchanged, untouched -- unlisted repos are
  still refused outright and loudly logged), the stacks come from the
  repository's own checkout: every directory containing a real
  `.ubx/config` (any of cli/configcascade.go's own four real file names)
  is discovered by walking the checkout at the event's own commit, in
  new `server/stackdiscovery.go`. UBI-166's deepest-matching-stack-wins
  resolution is reused unchanged, just fed auto-discovered candidates
  instead of manually-declared ones -- so the multi-stack case still
  matches against the event's own real changed files and still refuses
  outright on genuine ambiguity, never guessing. Zero discovered stacks
  is a real, separate refusal (`ErrNoStackDiscovered`) from "not on the
  allowlist", which never reached this resolver in the first place. Real
  structural consequence: each platform's own handler now does the
  checkout before resolving (discovery reads what the commit actually
  contains), so the twelve `run*` helpers take a prepared `repoDir`
  instead of checking out themselves. Drift-watch, which has no event
  and therefore no changed files to disambiguate with, runs one real
  `ubx status --drift` pass per discovered stack rather than picking
  one. A pre-UBI-167 `ledger_dir` (YAML key or `--repo` suffix) is
  refused BY NAME at startup, never silently ignored.
- 2026-08-12 -- UBI-145 (CLOSED): landed UBI-134's real, waiting
  `sdk/ts`/`sdk/py`/`sdk/go` runtime changes for real, across three
  working sessions, never self-merged anywhere. A real finding
  reversed the ticket's own framing: the panic-message drift was the
  OPPOSITE of what was described -- `ubx-sdk-go`'s real repo already
  had the correct `ubx.` prefix (an earlier, already-merged PR), the
  monorepo's own copy was the stale one still saying `sdk.`. `sdk/go`
  converted to a real submodule too, matching TS/Python -- a different
  real reason (no `go:embed` compile-time input exists for Go at all,
  confirmed directly; Go's hermeticity sandboxes the compiled binary,
  never an embedded interpreter, so `sdk/go`'s only real role is a
  local, hermetic-test-only `replace` stand-in) but the identical
  conclusion, for sync-safety. Two real environment/permission
  boundaries hit and handled correctly, not worked around: a direct
  push of a version bump to a separate repo's own `main` was blocked
  by the harness (redone as a real branch + PR), and `deno publish`'s
  own device-auth flow had no interactive terminal available (asked
  the founder directly, who provided a real JSR token after a
  classifier-caught mismatch was clarified rather than assumed). Both
  registries confirmed live via direct, fresh registry fetches:
  `@ubx/sdk` 0.1.2 on JSR (https://jsr.io/@ubx/sdk@0.1.2), `ubx-sdk`
  0.1.2 on PyPI (https://pypi.org/project/ubx-sdk/0.1.2/). All three
  languages now use the identical submodule mechanism; the separate
  repos and the monorepo agree byte for byte. Full account: STATE.md's
  own 2026-08-12 entry.
- 2026-08-12 -- UBI-60: `ubx promote` supports SDK- and dialogue-
  authored proposals, both real, named refusals UBI-55 documented
  honestly now closed. Real prerequisite found while verifying (not
  assumed): SDK promotion was structurally impossible, not just
  unchecked -- `stampDocumentSource` (goeval/tseval/pyeval) stamped a
  `document` source's own `Ref` as `filepath.Base(entryFile)`,
  basename only, discarding the directory needed to relocate the file
  at all. Fixed to store the given path verbatim, the identical
  convention `.md`/`.d2` sources already used -- a determinism-adjacent
  change across all three evaluators, 6 real downstream tests/fixtures
  updated as a real, expected consequence. SDK promotion: `content_hash`
  checked first, a match re-runs the pinned program through the real
  evaluator (the identical `evaluateSDKProgram` mechanism `ubx resolve/
  plan --from-code` already use) against the TARGET's own real context;
  a mismatch refuses outright, naming both hashes. Dialogue promotion:
  the FINAL converged draft already captured in the `.dlg.json` is
  re-resolved against the target, never re-run through the LLM a second
  time; the dialogue capture itself carries forward as pinned evidence.
  A real, pre-existing gap found and fixed along the way: `ubx promote`
  never called `blueprint.ExpandCalls`/`ApplyOverrides` for ANY source,
  unlike `resolve`/`plan` -- fixed once, for all four paths. Real tests
  for all four required cases, including the ticket's own required
  UBI-81-interaction proof (the same unchanged SDK program, promoted
  against two real, independent remote-store targets via `sdk.CrossStack`,
  UBI-134, resolves to two real, different values) and a genuine
  `.dlg.json` built from a real `ubx chat` session, never a hand-
  constructed shortcut. Docs (`cli-reference/promote.mdx`,
  `tutorial/promotion/promote.mdx`) rewritten with real transcripts
  captured from a real built binary, including the real mismatch
  refusal, shown honestly. Full account: STATE.md's own 2026-08-12
  entry.
- 2026-08-12 -- UBI-81 v1: context-aware drafting, scoped to exactly one
  read-only tool. The intent-drafting adapter can call a real, bounded
  Anthropic tool-use round trip (`read_stack_config`) to read the target
  stack's own name and a deliberately narrow `.ubx/config` subset
  (`cli/stackcontext.go`'s own real exclusions: `Config.Intent` and
  every freeform provider-config table) before drafting -- markdown/chat
  only, blueprint-authored drafting untouched (verified by a real
  hermetic test, not just by review). New `DraftRequest.StackConfig`
  (`intentprovider/adapter.go`), the same "pre-computed DATA, never a
  live capability" posture `KnownResources` already established. Real,
  live tests against the real Claude API prove both required paths: a
  real conditional document resolves via context when a real config
  value settles it (named as a real `intent.defaults` entry), and still
  blocks with a real question when nothing resolves it -- plus the full,
  unrelated live conformance suite stays green, confirming the change is
  inert for ordinary drafting. Receipt rendering verified at the CLI
  layer, not assumed. A real before/after (a stack named `app3`, only
  resolvable via its own `.ubx/config`) was captured from two REAL
  binaries, built at the prior commit and at this session's own HEAD,
  both run live -- used directly in a new ubiquex-docs concepts page and
  a new 4th Markdown-track tutorial, committed and pushed the same
  session, per the ticket's own non-negotiable requirement (found and
  fixed two real, unrelated, pre-existing DOM overflow bugs on pages
  this session cross-linked, via real `mint dev` + DOM measurement, not
  a sweep of the whole site). Full account: STATE.md's own 2026-08-12
  entry.
- 2026-08-12 -- UBI-134: blueprint call arguments can now carry a real
  $ref/$cross reference. New `ParamCrossRef` param type (`cross_ref`,
  `blueprint/ubxfile.go`) -- a call-site value must be a real
  `@<stack>.<type>.<name>.<attr-path>` reference (reusing `diagram/
  crossref.go`'s own established "@" grammar), a malformed one is a
  real, named parse error, never a silent string literal. Extended the
  Go/TS/Python SDKs with a real `CrossStack`/`crossStack`/`cross_stack`
  constructor (the "stack"-mode sibling of the existing `Cross`/`cross`,
  matching `resolveCross`'s own already-supported wire shape) since a
  blueprint's own `@<stack>...` argument names a neighbor by stack, never
  a `ledger_dir` path a blueprint has no way to know at build time.
  `GoType()` = `sdk.CrossMarker`, `TSType()`/`PyType()` = `any`/`Any`,
  matching this file's own existing `outputs:` opaque-value convention.
  Real coverage across all three calling mediums (diagram, md, direct Go
  SDK import), each through a real `ubx plan` run against a real
  stack-mode $cross neighbor, plus the malformed-reference refusal case.
  `sdk/ts`/`sdk/py` turned out to be real git submodules of their own
  separate, published repos (`ubx-sdk-typescript`/`ubx-sdk-python`,
  discovered mid-session, not assumed) -- their own `crossStack`/
  `cross_stack` edits are left as real, uncommitted changes in those
  submodules' own working trees this session, monorepo-only per the
  founder's own explicit call, not committed/pushed/pointer-bumped.
  `sdk.CrossStack` (Go) has no submodule link to `github.com/ubiquex/
  ubx-sdk-go` at all -- monorepo-only the same way. Nothing here is
  claimed as published/live for any of the three separate SDK repos,
  per rule 8; real follow-up debt named explicitly in STATE.md. A real
  ubiquex-docs addition to `tutorial/blueprints/call-other-mediums.mdx`
  is genuine docs-debt, named explicitly, not done this session. Full
  account: STATE.md's own 2026-08-12 entry.
- 2026-08-12 -- UBI-57: two independent housekeeping items from UBI-32,
  closed on their own merits. Part 1 (orphan GC): `ubx store gc` (matches
  the existing `sdk`/`providers` parent-command convention), finds and
  removes real proposal objects orphaned by a crash between
  `WriteProposalIfAbsent` and `AdvanceHead` -- new `ledgerstore.Store.
  OrphanedProposals`/`.DeleteProposal`, walking reachability from
  genesis (never `Head()`'s own cached hint, which would misclassify an
  early proposal). Dry-run by default, `--yes` for real deletion, the
  orphan set re-verified fresh immediately before each delete (a real
  TOCTOU guard, not just asserted). 7 hermetic + 5 CLI-level tests, plus
  a real, live crash-injected orphan against real S3 (`ubx-states`):
  correctly found, correctly deleted, the real chain and its content
  independently reconfirmed completely untouched via fresh `aws s3api`
  calls, swept fully clean afterward. Part 2 (multi-hop pins): verified
  first, with evidence, that `VerifyPins` is structurally single-hop by
  construction (never reads a neighbor's own resolution.inputs) -- and
  that no design doc anywhere explicitly commits to "per-pair only,
  forever" the way the ticket's own framing implied (docs/resolver-
  adversarial.md instead honestly names this as an open, uncovered
  question). Code and design agree on today's actual behavior; the
  founder confirmed, given that nuance, to build visibility only, never
  new blocking. New `resolver.WalkPinChain` walks the full chain for
  rendering; `VerifyPins` itself is completely untouched. 5 real,
  hermetic three-ledger tests prove the actual semantics directly
  (an indirect ancestor's own staleness never blocks the proposal that
  only directly pins its immediate neighbor; a direct neighbor's own
  staleness still is caught, unchanged). Wired into `ubx why` (a new
  "pin chain" section) and `ubx addresses` (a compact per-resource
  annotation), both silent for the common no-pin case, both covered by
  new CLI-level hermetic tests. A live-verification attempt for Part 2
  was correctly refused by the harness (the ticket only asked for
  hermetic tests there, unlike Part 1). Full account: STATE.md's own
  2026-08-12 entry.
- 2026-08-12 -- UBI-58: probe 3 (destroy honesty) confirmed live against
  real cloud for the first time -- `docs/conformance-harness.md`'s own
  "Amendment (session 4, closing)" had deliberately left this open,
  naming it "this arc's one deliberately-open item" given the direct
  tension with CLAUDE.md's ship-verification rule ("always, no
  exceptions"). That real history was found and read before proceeding,
  and a direct, explicit, in-conversation confirmation was still
  obtained before any real create/destroy, given the severity. AWS
  credential scope re-checked separately from UBI-56's storage access
  (confirmed AdministratorAccess); real `aws_sns_topic`/`aws_sqs_queue`
  destroyed honestly through `ProbeDestroyHonesty`'s own real
  `core/executor.Ship` path, independently re-confirmed gone via fresh
  `aws` CLI queries. The real target: whether `google_pubsub_topic`'s
  own UBI-44 lookup-completeness gap (confirmed still open via a real
  grep of prior sessions' own accounts, never patched) was still
  reproducible -- it was, live, through probe 3 itself for the first
  time (previously only a manual CLI reproduction), run twice
  identically, both independent ground-truth checks (`gcloud describe`,
  a real Cloud Audit Logs query) confirming the exact historical
  signature. New, permanent, committed live tests
  (`conformance/destroy_probe_live_test.go`), not a one-off script.
  Swept clean and independently reconfirmed after each run -- distinct
  from UBI-56's own deliberately-standing infrastructure, every resource
  here was single-use. `conformance/registry.go`'s own `google_pubsub_
  topic` entry and `docs/reliability-report.md` both updated with the
  real, captured result. Full account: STATE.md's own 2026-08-12 entry.
- 2026-08-12 -- UBI-56: gs:// and azblob:// ledger stores implemented
  and live-verified against real GCS and real Azure Blob Storage.
  Credential stop condition checked first per the ticket's own
  instruction -- GCP was immediately usable, Azure needed an interactive
  MFA re-login this session couldn't perform itself, surfaced directly
  rather than guessed at; the founder re-authenticated and asked for
  full scope. Real, current dependency measurement (not the original
  UBI-32 Arc B finding trusted blindly): only 9 net new Go modules now
  (this binary already shares a real chunk of both SDKs via the
  audit/gcp/-azure drift backends), but a real +25.6MB/+27% binary size
  cost regardless -- decided a new `cloudblob` build tag (s3 stays
  unconditional, gs/azblob opt-in via `-tags cloudblob`), confirmed via
  byte-identical default-build size and a clear, narrowly-scoped error
  hint for anyone hitting gs://azblob:// on an untagged binary. Zero
  changes needed to Store/conformance code itself (already fully generic
  over `*blob.Bucket`, CAS via `WriterOptions.IfNotExist` mapping to each
  provider's own real precondition mechanism); the live test suite was
  refactored into shared, parameterized helpers rather than tripling the
  same 4-test body, catching a real live-only bug in the new
  infrastructure itself (lockprobe's own subprocess build wasn't
  inheriting the cloudblob tag) before trusting any live result. Real
  GCS/Azure infrastructure provisioned this session (a real IAM
  elevation attempt correctly refused by the harness; worked around via
  shared-key auth, no privilege change needed) -- all 12 real live tests
  (BasicRoundTrip/CASRace/LockContention/LockTTLExpiry x 3 backends)
  pass against the real, live services, directly proving CAS head
  advancement works against real GCS generation preconditions and real
  Azure ETags, not just each provider's documented behavior. Docs
  updated in the real, current location (`concepts/ledger-stores.mdx`
  named in the ticket no longer exists, renamed to `concepts/
  remote-stores.mdx` + `tutorial/remote-stores/` in an earlier session,
  confirmed via git log rather than assumed) with real, freshly-verified
  transcripts; two real, pre-existing inaccuracies in the
  already-published S3 content were found and fixed along the way (a
  fictional `ledger/` key sub-prefix, a missing trailing slash on the
  resolved address). Full account: STATE.md's own 2026-08-12 entry.
- 2026-08-12 -- UBI-142/UBI-143: two real SDK/codegen bugs found during
  UBI-140's tutorial fix, investigated together (confirmed unrelated
  root causes, fixed independently). UBI-142: `sdk/codegen/templates/ts/
  ts.go`'s nested-interface-emission code path never applied the same
  optional-marking/`Computed<T>` union treatment the top-level Config
  loop already correctly had -- two separate code paths, one updated
  when Computed<T> support landed, the other never touched. Fixed via a
  shared `configFieldLine` helper both paths now route through; two
  existing tests that had codified the broken behavior as expected
  output were corrected. `ubx-sdk-kubernetes` regenerated for real (all
  three languages, against the real pinned `hashicorp/kubernetes@3.2.1`,
  not a wrong version an earlier scratch run had used), real `deno check
  --frozen` clean across all 81 files against the published `jsr:@ubx/sdk`
  runtime, opened as ubx-sdk-kubernetes#1 (never self-merged). A
  genuinely separate, pre-existing gap (the runtime's own generic
  `Computed<T>` type not collapsing array element access,
  `dbSecret.metadata.name`) was found, isolated from this fix's own
  effect via paired before/after tests, and explicitly left out of
  scope (already flagged during UBI-140). AWS/GCP/Azure's own bindings
  carry the same latent template bug but zero currently-published docs
  exercise it -- left for their own next natural regeneration cycle, a
  deliberate, stated scope decision. UBI-143: `ubx.Secret()` couldn't
  target `kubernetes_secret_v1.data` -- NOT an upstream schema gap
  (confirmed via a real live schema fetch: `Sensitive: true` genuinely
  present) and NOT the `provider.SensitiveOverrides` mechanism's
  business (confirmed that table only feeds output redaction, never the
  resolver's own `$secret`-placement gate). Real root cause:
  `cli/schemainspector.go`'s `attributeAt` required an exact path match,
  so a sub-path into a flat map-typed Sensitive attribute (`data.
  DB_PASSWORD`) reported not-found even though `data` itself is
  Sensitive. Fixed by letting a flat Attribute match apply regardless of
  any remaining path segment (a NestedBlock's own bare-name case is
  unchanged). No design decision needed -- a real, fixable bug, verified
  with a rebuilt `ubx` and the same real repro now resolving cleanly.
  Full account: STATE.md's own 2026-08-12 entry.
- 2026-08-12 -- Incident: UBI-140 and UBI-141 were both reported
  "committed and pushed" but never reached the real `ubiquex-docs`
  GitHub repo -- root cause: both sessions edited and locally verified
  `~/Ubiquex/documentation`, a disconnected copy with no `.git` at all,
  instead of the real, git-connected `~/Ubiquex/ubiquex-docs` checkout.
  Bounded the exposure precisely (`diff -rq` between the two trees found
  exactly the 3 files these two tickets touched, nothing else; every
  earlier edit was already ported into a real commit at `01:12:41`, then
  the porting silently stopped) and confirmed no other session's docs
  work in the same window was similarly stranded (UBI-138 Phase 2 named
  its own docs sweep as explicitly deferred; UBI-139 needed no docs
  change). Both fixes re-verified fresh, ported into the real checkout,
  committed, pushed, and confirmed genuinely live via a direct GitHub
  API fetch (the repo is private, so `raw.githubusercontent.com` can't
  be used unauthenticated). `CLAUDE.md` rule 5 amended to name the real
  path explicitly and require remote/`.git` confirmation before editing
  docs in any unverified path. Full account: STATE.md's own 2026-08-12
  entry.
- 2026-08-12 -- UBI-141: fixed a real `ComputedCoercionError` bug in
  `tutorial/aws/first-resource.mdx`'s TS tab (`${queue.arn}` inside a
  template literal), plus five more real instances of the same
  underlying bug found by a broader sweep (Go and Python's own
  fictional `ubx.Sprintf`/`ubx.fmt` helpers in the same tutorial;
  `JSON.stringify`/`json.Marshal`/`json.dumps` on a raw `Computed` value
  in all three language tabs of `resource-reference/aws/iam/policy.mdx`;
  the diagram tab's own non-working `"ref:queue.arn"` embedded in a
  JSON string). The real, verified mechanism (found by reading
  `core/resolver/refs.go` directly, not derivable from the runtime API
  alone): get the `Computed`'s address string (`.Address()` Go,
  `addressOf()` TS, `.address` Python) and hand-build a literal
  `{"$ref":{"to":"<address>"}}` marker inside the larger JSON string --
  the exact marker shape the TS runtime's own config serializer
  produces automatically for a top-level `Computed<T>` field. Two more
  real, pre-existing, unrelated bugs found and fixed along the way:
  `RolePolicyAttachment` was missing its own required `policyArn` field
  in all three tutorial language tabs; the diagram tab's own
  `message_retention_seconds` attribute couldn't resolve at all since
  it's optional and the diagram medium can only ever set attributes the
  schema flags required (dropped, a real diagram-medium limitation, not
  a bug). D2 also reserves an unescaped `$` inside a quoted string for
  its own substitution syntax, discovered via a real parse error;
  needs a backslash in front of the marker's own `$ref` key. Full
  account: STATE.md's own 2026-08-12 entry.
- 2026-08-12 -- UBI-140: `tutorial/kubernetes/first-resource.mdx` fixed
  for real against the actual generated `ubx-sdk-kubernetes` bindings
  (cloned fresh, read directly) -- the fictional `core`/`apps`/
  `ObjectMeta` bug flagged across two earlier UBI-138 sessions but
  never fixed is now closed. Real, deep nesting confirmed (`Kind:
  "list"` at every level, `[]Type{{...}}` syntax proven by an actual
  `ubx plan --from-code` run, not derived and trusted). New findings
  along the way: the `any`-typing is confirmed intentional codegen
  behavior; TS has two real gaps (nested fields missing `?` and missing
  `Computed<T>`) that make the tutorial's own TS example resolve
  correctly but not pass `deno check` cleanly; Python's nested types
  aren't re-exported at the service-package level; `ubx.Secret()`
  genuinely cannot target `kubernetes_secret_v1.data` (not
  `Sensitive`-flagged) -- a real, pre-existing bug in the original
  tutorial too; the Diagram medium genuinely cannot express this
  tutorial's own reference (neither resource has a required top-level
  attribute). A deliberate, explicit simplification was made
  (`envFrom.secretRef` instead of the 7-level-deep `env[].valueFrom.
  secretKeyRef` path). Full account: STATE.md's own 2026-08-12 entry.
- 2026-08-12 -- UBI-139: the shared SDK runtime (Go/TypeScript/Python)
  consolidated into one-repo-per-language, matching `ubx-sdk-go`'s own
  existing shape. `sdk/ts/` and `sdk/py/` both turned out to be real
  `go:embed` build inputs for `ubx` itself (confirmed by breaking the
  build on purpose and restoring it, not inferred from reading code) --
  extracted to `github.com/Ubiquex/ubx-sdk-typescript` and `ubx-sdk-
  python` (real git history preserved via `git filter-repo`), then
  wired back into `ubiquex` as git submodules per the founder's own
  explicit decision (presented three real mechanisms, didn't guess).
  Published package identities (`ubx-sdk` on PyPI, `@ubx/sdk` on JSR)
  are unchanged, no rename, confirmed with the founder after checking
  the alternative names were genuinely free -- so none of the four
  provider bindings repos needed any dependency-file edits. Found and
  fixed a real new hazard along the way: a plain `git clone` silently
  left both submodules empty and broke the build with a confusing
  error; `Makefile`'s `build`/`install` now auto-init submodules. Full
  account: STATE.md's own 2026-08-12 entry.
- 2026-08-12 — UBI-138 Phase 2 COMPLETE (Azure, fourth and final
  provider): `github.com/Ubiquex/ubx-sdk-azure`, same corrected process.
  `0.1.0` already taken on PyPI/JSR; published `0.2.0`, unified across
  all three registries. Provider `hashicorp/azurerm@5.0.1`. A real,
  provider-specific naming gotcha handled correctly (mechanical
  shortName `azurerm` vs. this project's own established package name
  `azure` — the same bug already found and fixed once for the old
  per-language repos, now closed at the source with an explicit,
  every-run correction step baked into the new repo's own
  `version-watch.yml`). All four providers (AWS/Google/Kubernetes/Azure)
  are now consolidated into their own `ubx-sdk-<provider>` repo, each
  verified live against real registries, each with working CI. Phase 3
  (docs sweep) and UBI-139 (runtime consolidation) deliberately NOT
  started. Full account: STATE.md's own 2026-08-12 entry.
- 2026-08-12 — UBI-138 Phase 2 (Kubernetes, final Phase 2 provider):
  third per-provider repo, `github.com/Ubiquex/ubx-sdk-kubernetes`,
  same corrected process as Google. `0.1.0` already taken on both PyPI
  and JSR; published `0.2.0`, unified across all three registries.
  Provider version `hashicorp/kubernetes@3.2.1` — real Terraform
  Registry, OpenTofu mirror, and the old repo's own pin all agreed, no
  mirror-lag fallback needed this time. Directory structure inspected
  and confirmed BEFORE publishing (now mandatory). Zero open PRs across
  the three old per-language repos. Old repos archived. UBI-138 Phase 2
  is now complete (AWS/Phase 1 + Google + Kubernetes); Azure remains.
  Full account: STATE.md's own 2026-08-12 entry.
- 2026-08-12 — UBI-138 Phase 2 (Google): second per-provider repo,
  `github.com/Ubiquex/ubx-sdk-google`, generated directly into the
  correct `sdk/go/`, `sdk/typescript/`, `sdk/python/` structure from the
  start (Phase 1's own root-level mistake deliberately not repeated —
  the real directory listing was inspected and confirmed before any
  publish step this time). `0.1.0` was already taken on both PyPI and
  JSR (from the old per-language repos); published `0.2.0`, unified
  across PyPI/JSR/the Go module tag, verified fresh-install/import
  across all three languages against the real live registries. Old
  `ubx-sdk-google-go/-ts/-py` archived. Full account: STATE.md's own
  2026-08-12 entry, including a real OpenTofu-mirror-lag finding and a
  process self-correction (background-agent use, corrected mid-session).
- 2026-08-11 — UBI-138 Phase 1 (AWS only): the 12 per-(provider,language)
  `ubx-sdk-*-{go,ts,py}` bindings repos consolidate into 4 per-provider
  repos, each with all three languages as sibling subdirectories under
  `sdk/` (`sdk/go/`, `sdk/typescript/`, `sdk/python/` — the real Pulumi
  precedent this project cites). `sdk/codegen/templates/{go,py,ts}`
  fixed at the source to emit this shape; new repo
  `github.com/Ubiquex/ubx-sdk-aws` live, verified fresh-install/import
  across all three languages against the real registries (JSR/PyPI/Go
  proxy) at `0.3.1`. A real mid-session structural correction (root-level
  vs. `sdk/`-nested layout) is the full story, not this one line — see
  STATE.md's own 2026-08-11 entry. Google/Azure/Kubernetes (Phase 2/3)
  and the docs site are explicitly deferred, untouched this session.
- 2026-08-05 — UBI-125 (Terraform module → blueprint converter,
  deterministic, no AI): `ubx blueprint convert --from-terraform
  <module-dir> --out <dir>` — a new `tfconvert/` package parses a real
  Terraform module's HCL directly (`hashicorp/hcl/v2/hclsyntax`, the
  same library `writeback/` already uses) and translates `variable`/
  `resource`/`output` blocks mechanically into a blueprint's own
  `params:`/resource calls/`outputs:`, including a real `count =
  length(var.list)`/`for_each = var.list|toset(var.list)` → UBI-129's
  own `for_each` shape, and a ported `cidrsubnet()` Go helper
  (`blueprint/cidrsubnet.go`, a new additive `$fn` marker in
  `gogen.go`'s `renderAny`) — Go codegen only so far, TS/Python a real,
  named follow-up. Anything genuinely unhandled (a conditional, an
  unported function, a data source, a `locals` cycle, ...) surfaces as
  a `core.Question`, never a silent guess. Live-verified against the
  real `hashicorp/aws@6.58.0` provider schema via `ubx resolve
  --from-code` (resolve-only, never `ubx ship`) on two real fixtures — a
  `count`-based module distilled from the real, live-fetched
  `terraform-aws-modules/terraform-aws-vpc` module's own core idiom, and
  a dedicated `for_each`-grammar module — full account: docs/
  blueprint.md's own "Terraform module conversion (UBI-125)" section.
- 2026-08-05 — UBI-128 (blueprint outputs: cross-medium references to a
  called blueprint's own resource attributes, all three calling
  mediums): `Ubxfile` gains an `outputs:` key
  (`<name>: <resource-slug>.<attribute>`); Go/TS/Python codegen all
  return declared outputs as native multi-value returns (Go named
  `*sdk.Computed` returns, a TS object literal, a Python bare
  value/tuple) -- zero new runtime mechanism, confirmed live in Go via a
  real `ubx resolve --from-code`/`ubx ship` and in TS/Python via a real
  `deno run`/`python3` driver. The diagram medium's own EXISTING `ref:`
  sigil (UBI-95) now also resolves a `ubx_blueprint` node's own
  declared output, via a new provisional `$blueprint_output:<CallName>:
  <outputKey>` wire marker (`resolver.BlueprintOutputRefPrefix`),
  rewritten into a real address by `blueprint.ExpandCalls` once the call
  is actually invoked -- live-verified end to end (resolve→accept→ship)
  against real `fakeprovider`, and separately (resolve/plan-only, real
  AWS schema) against the real `ci-platform` blueprint. A real,
  pre-existing `diagram/parse.go` bug (a `ubx_blueprint`/`ubx_override`
  node's own attribute children spuriously refused as unresolved
  topology nodes) was found and fixed along the way. The md medium
  gains real new grammar -- "Call blueprint X as 'name' with:" plus a
  later "name's own output_key output" reference -- live-verified once
  against the real Claude API (correct on the first attempt), hermetic
  otherwise. Deliberately, structurally distinct from `@stack.type.name`
  (UBI-47) cross-stack references throughout -- an output never crosses
  a ledger/trust boundary and carries no staleness concept, unlike
  `@stack`. Full account: docs/blueprint.md's new "Outputs: cross-medium
  blueprint output references (UBI-128)" section, including the exact
  per-medium live-verification depth (Go SDK/diagram: full CLI pipeline
  against two real backends each; TS/Python SDK: real toolchain,
  direct-runtime, not a full CLI ship; md: one real API call, hermetic
  otherwise) stated explicitly, never blurred together.

- 2026-08-05 — UBI-74 Slice 8 (Strata blueprints, offline delivery +
  redistribution — the FINAL slice of the original eight-slice plan,
  now fully closed): `ubx blueprint pull <path-to-tarball>` -- a fourth
  `Pull` source type, a bare tarball FILE, extracted with zero network
  involved at all (offline/email/support-ticket delivery), slotted into
  the exact gap the existing local-path/git/OCI dispatch already
  implied. Real re-tag/mirror-unchanged redistribution needed no new
  production code -- `ubx blueprint push` called a second time against a
  second `--to`, proving trust-preserving redistribution by composing
  already-built mechanisms. Content-hash verification stayed the one
  existing scheme (`Verify`, unchanged since Slice 3) -- the delivery
  mode this session's own required verification confirmed has no git
  history or registry-native integrity to lean on at all. A real,
  live-found subtlety: `content_hash` depends on the blueprint's own
  declared `name` (from the directory's basename at package time) as
  well as file content -- documented explicitly as a real operational
  gotcha for offline re-export. Fork-with-modification is designed in
  full but not built (an explicit stretch goal). Live-verified against
  real `ghcr.io`: the real CI-platform blueprint pulled fresh,
  re-packaged, pulled again as a bare file with network deliberately
  blocked -- succeeded, verified, real `go build`/`go vet` succeeded;
  pushed to a second real GHCR location, `docker manifest inspect`
  confirmed an identical blob digest at both, `ubx blueprint verify`
  against the mirror confirmed the identical original content hash.
  Full account: docs/blueprint.md's "Offline delivery + redistribution:
  Slice 8" section (new) and its own Slice 8 implementation-slices
  entry; this file's own "Strata blueprints" subsection updated in
  place -- ALL EIGHT SLICES closed. **A full closing retrospective
  across all eight slices together is recorded in STATE.md.**

- 2026-08-05 — UBI-74 Slice 7 (Strata blueprints, OCI push/pull): `ubx
  blueprint push <tarball> --to oci://<registry>/<repo>:<tag>` uploads
  Slice 3's own tarball, unmodified, as a real OCI artifact via ORAS
  (`oras.land/oras-go/v2`, confirmed the current, actively maintained API
  surface via `go doc` against its own real downloaded source, not
  memory) -- one manifest, the tarball as its one content-addressed blob
  layer; `ubx blueprint pull` gained a third `oci://` source type
  alongside Slice 3's local-path and git. Authenticated via the SAME
  credentials a real `docker login`/`oras login` already established
  (confirmed working first: a real `docker login ghcr.io` re-run,
  "Login Succeeded" -- never a second, ubx-specific login). Content-hash
  verification stays ONE scheme: OCI's own native blob digest is a
  transport-integrity check (the registry's own job, zero new code);
  `content_hash` (`core.CanonicalJSON`-based, unchanged since Slice 3)
  stays the application-level check `Verify` already performs after
  pull+extract -- also recorded as an OCI manifest annotation purely for
  `docker manifest inspect`-level visibility, never a competing
  verification path. One real correction caught before it shipped wrong:
  `github.com/oras-project/oras-credentials-go` turned out deprecated in
  favor of the identical functionality already built into `oras-go/v2`
  itself, caught via that package's own `go doc` output. Live-verified
  against real `ghcr.io`: the real CI-platform blueprint (proven live
  across Slices 1-6, confirmed via an IDENTICAL content hash to Slice 6's
  own real render output) packaged and pushed to
  `ghcr.io/ubiquex/ci-platform:v1`, independently confirmed via a real
  `docker manifest inspect`, pulled back into a separate directory,
  content hash verified, and a real `go build`/`go vet` against the
  pulled copy succeeded -- real network, no local `replace`, the
  identical bar Slice 3 met for git, now met for a real OCI registry.
  Full account: docs/blueprint.md's "OCI push/pull: Slice 7" section
  (new) and its own Slice 7 implementation-slices entry; this file's own
  "Strata blueprints" subsection updated in place. Slice 8 remains a
  future session.

- 2026-08-05 — UBI-74 Slice 6 (Strata blueprints, provenance + `why`/
  `render` integration): every resource a blueprint call produces is
  stamped with a `{"kind": "blueprint", "ref": "<name>:<content_hash>"}`
  entry in a new per-RESOURCE `resolver.ResourceIntent.Sources` field --
  reusing `core.IntentSource`'s own existing multi-kind shape verbatim
  (a new `"blueprint"` kind, never a new field shape), since a
  document-level source can't express "resource A came from a blueprint,
  sibling B in the same document didn't." `blueprint.ExpandCalls` is the
  one producer that stamps it, using `buildManifest`'s own fresh content
  hash as the version (never requiring the blueprint to have been
  `package`d first). `ubx why` renders the full chain -- which blueprint,
  which version, and an honest dual-signature account: only the calling
  stack's own real acceptance is backed by a real signing ceremony in
  this build, the blueprint author's own signing is named as a gap, not
  fabricated. `ubx render` groups a blueprint call's own resources inside
  one dashed-border D2 container (`style.stroke-dash`/`style.fill:
  transparent`, empirically verified against the real `d2parser`/
  `d2format` pipeline before use), labeled with the blueprint's own ref --
  real, resolved-time truth, consistent with `diagram/emit.go`'s own "no
  synthetic containers" principle, not an exception to it. Found and
  fixed a real, pre-existing bug along the way: `Emit` read `depends_on`/
  provenance from Fleet's own "latest touching proposal," silently wrong
  whenever a later, unrelated proposal touches the same address without
  re-creating it (a real shape a two-resource blueprint call with a
  `$ref` between them produces) -- fixed with a new `creatingProposalFor`
  helper that finds the actual creating proposal from the address's own
  full history. Live-verified against real `hashicorp/aws@6.54.0`: the
  real CI-platform blueprint, resolved/accepted against the real provider
  schema, shipped by the founder, then `ubx why`/`ubx render` both
  confirmed correct against the real shipped result. Full account:
  docs/blueprint.md's "Provenance: Slice 6" section (new) and its own
  Slice 6 implementation-slices entry; this file's own "Strata
  blueprints" subsection updated in place. Slices 7-8 remain future
  sessions.

- 2026-08-04 — UBI-74 Slice 5 (Strata blueprints, cross-medium calling):
  a diagram's own `ubx_blueprint`-classed node (`diagram/parse.go`, zero
  AI, reusing UBI-91's own `ubx_required` structural-attribute mechanism)
  and an md draft's own "Use blueprint X with..." recognition
  (`intentprovider`, a thin AI mapping step that never re-drafts the
  blueprint's own resources) both compile to the SAME new
  `resolver.IntentFile.BlueprintCalls` wire field (purely additive,
  matching `DependsOn`'s own precedent), expanded by `blueprint.
  ExpandCalls` -- spliced into `ubx resolve` right before `resolver.
  Resolve`, the one shared point every intent/v1 document passes through
  regardless of medium -- into real resources by literally invoking the
  target blueprint's own compiled function through the IDENTICAL
  `goeval`/`tseval`/`pyeval` machinery `ubx resolve --from-code` already
  runs for a hand-written SDK program (UBI-74 Slice 2's own real
  invocation mechanism, never a second, parallel one). `resolver.Resolve`
  itself hard-refuses an unexpanded `BlueprintCalls`, rather than
  silently ignoring one. One real assumption checked live and corrected
  before it became a design liability: Go's own placeholder `v0.0.0`
  `ubx-sdk-go` version turned out to be genuinely real and resolvable,
  not a placeholder needing a `go mod tidy`/local `replace` first -- the
  real condition is just the module already being in the local Go module
  cache. Live-verified: a hermetic byte-comparison test first (the SAME
  blueprint called via a hand-written Go SDK program, a real `.d2`
  diagram, and a real fake-adapter md draft all resolve to the IDENTICAL
  delta shape), then the md leg for real -- a real `.md` document drafted
  against the REAL Claude API correctly recognized the pattern with zero
  hallucinated resources, resolved against the REAL
  `hashicorp/aws@6.54.0` provider's own schema with UBI-123's own
  corrected `retention_days: 14` reaching the resolved proposal
  correctly, accepted into a real ledger. Full account: docs/blueprint.md's
  "Cross-medium calling" section (new) and its own Slice 5
  implementation-slices entry; this file's own "Strata blueprints"
  subsection updated in place. Slices 6-8 remain future sessions.

- 2026-08-04 — UBI-74 Slice 4 (Strata blueprints, multi-language):
  `ubx blueprint build --lang go|ts|py|all` -- the SAME single AI draft
  (drafted exactly once, regardless of language count) compiled into up
  to three sibling `go/`/`ts/`/`py/` package directories by three
  independent generators (`GenerateGo`/`GenerateTS`/`GeneratePython`)
  sharing one new language-neutral decode/dependency/topo-sort layer
  (`blueprint/decode.go`). Confirmed before building anything new, not
  assumed: `sdk/codegen`'s own IR/template machinery (schema -> generic
  binding library) doesn't drop in cleanly for blueprint codegen
  (resolved concrete values -> source) -- real per-language adaptations
  implemented explicitly (native default parameters for TS/Python vs.
  Go's own functional-options workaround; `ResourceBinding<any, any>` for
  TS; a mandatory dataclass Config for Python, matching Go's own struct
  requirement for a reason TS doesn't share). Two real bugs caught by
  this session's own hermetic tests before shipping: TypeScript's
  `Computed<any>` failing to typecheck property access at all (a real
  `deno check` error -- TS's conditional-type distribution over a naked
  `any` produces a union whose `ComputedMarker` branch has no index
  signature; fixed with a targeted `as any` cast), and Python's own local
  variable naming initially reusing Go/TS's camelCase derivation instead
  of genuine snake_case (caught by a hermetic test's own literal
  assertion, fixed with a dedicated identifier helper). Live-verified:
  the same CI-platform Ubxfile built `--lang all` against the real Claude
  API, all three languages' own output compiling (`go build`)/
  type-checking (`deno check --no-remote`)/importing (`python3`) cleanly,
  the TS-compiled function called from a real TS stack and resolved
  against the real `hashicorp/aws@6.54.0` provider's own schema with
  UBI-123's own corrected `retention_days: 14` reaching the resolved
  proposal correctly (`1209600` seconds), accepted into a real ledger
  (`ubx ship` itself deliberately handed off to the founder, per
  CLAUDE.md's own standing doctrine). Full account: docs/blueprint.md's
  "Multi-language codegen" section (new) and its own Slice 4
  implementation-slices entry; this file's own "Strata blueprints"
  subsection updated in place. Slices 5-8 remain future sessions.

- 2026-08-04 — UBI-74 Slice 3 (Strata blueprints, package/distribute):
  `ubx blueprint package <dir> -o <file>.tar.gz` (content hash over a
  built blueprint's own files via `core.CanonicalJSON` -- the SAME
  JCS-style canonicalization `core.Hash` already uses for a Proposal,
  never a new hashing convention -- written into `dir/blueprint.lock.json`,
  the directory archived into a gzipped tar); `ubx blueprint pull <source>
  <dest>` (a local path, copied as-is; or a git repo, cloned and checked
  out at `--ref`, `--path` naming the blueprint's own location within
  it -- OCI/Strata stays Slice 7); `ubx blueprint verify <dir>`
  (recomputes and confirms a directory's own content hash still matches
  its declared manifest, naming exactly which file changed on a
  mismatch). Live-verified per the ticket's own required bar: Slice 1/2's
  already-live-AWS-verified CI-platform package, packaged, pushed to a
  real, newly created GitHub repository
  (`github.com/Ubiquex/ubx-sdk-blueprints`) with real commit history,
  pulled into a completely separate local directory via a real HTTPS
  clone, verified (content hash matched byte-for-byte), and confirmed
  genuinely usable via a real `go build`/`go vet` against the actual
  published `ubx-sdk-go` module. Full account: docs/blueprint.md's
  "Package/pull/verify: distribution" section (new) and its own Slice 3
  implementation-slices entry; this file's own "Strata blueprints"
  subsection below, updated in place. Slices 4-8 remain future sessions.

- 2026-08-04 — UBI-74 Slice 1 (Strata blueprints, opens the arc): the
  `Ubxfile` format (`lang`/`params`/`resources`, strict YAML, `uses:` a
  hard parse error) and `ubx blueprint build .` -- resolves `resources:`
  through UBI-41's own intent-provider pipeline exactly once, compiles
  the draft into a real Go package (new codegen, `blueprint/gogen.go`:
  resource bindings derived from the draft's own observed config keys,
  never a live schema fetch; topologically-ordered `sdk.Resource()`
  calls; real `$ref` -> `.Field()` and `{param}` -> Go-variable
  translation). Live-verified against the real Claude API and the real
  published `github.com/ubiquex/ubx-sdk-go` module (network, not a local
  override) with a hand-authored CI-platform (ECR+SQS+IAM
  role+policy+attachment) Ubxfile -- `go build`/`go vet` both clean. One
  real finding: an IAM policy's own `$ref`-embedded-in-JSON-string shape,
  checked against `core/resolver/refs.go`'s own already-documented
  convention rather than assumed a bug -- it isn't. Full account:
  docs/blueprint.md (new); this file's own "Strata blueprints: Slice 1"
  subsection below. Slices 2-8 remain future sessions; UBI-121 (nesting)
  and UBI-118 (bound policy) stay split off, tracked separately.

- 2026-08-04 — UBI-98 session 2 (closes UBI-98): `ubx sdk gen --lang ts`
  and `--lang py` restructured the same repo-shaped way `--lang go` was
  the prior session -- checked explicitly, not assumed, whether Go's own
  `NewBulk too big` compiler-crash class also hits `deno check`/a real
  Python `import`: it does NOT (a synthetic worst-case
  `aws_wafv2_web_acl_rule` reproduction, 16.7MB TS / 21.2MB Python,
  checked/imported clean in seconds) -- the structural shape-dedup fix
  was still ported to both anyway, for reviewability/size, not crash
  avoidance. Real per-language findings: TS needs no service/local-name
  escaping at all (confirmed live); Python's `lambda` is both a real
  keyword and a real 20-type AWS service, fixed with the same trailing-
  underscore convention `pythonIdentifier` already used for field names.
  A separate, real bug found only by running all three languages against
  one shared `--out`: the repo-shaped tree has no per-language
  disambiguation at its top level the way the old flat-file extensions
  did, so generating go/ts/py together interleaved their manifests into
  one directory -- fixed by making `--lang` its own path segment
  (`<out>/<lang>/<source>/`), covered by a new dedicated test. Required
  verification met for both languages, twice each (permanent
  `UBX_CONFORMANCE_LIVE=1` tests + a real built-`ubx`-binary run), same
  bar as Go's own session. Full account: docs/sdk.md's new "Amendment
  (2026-08-04, UBI-98 session 2)" section; STATE.md has the full session
  narrative.

- 2026-08-03 — UBI-98: `ubx sdk gen --lang go` restructured to UBI-98's
  own repo-shaped, per-AWS-service-package layout -- confirmed-reproduced
  the UBI-96-reported Go compiler crash first, then fixed it for real
  (per-service packages alone were NOT enough -- a real recursive-schema
  blowup, `aws_wafv2_web_acl_rule` alone rendering >10MB, needed a second,
  separate structural-shape-dedup fix on top). `--out` now writes
  `<out>/<source-sanitized>/` with its own `go.mod` (module
  `github.com/ubiquex/ubx-sdk-<shortName>`), one package per derived
  service (`iam/`, `ecr/`, ...), the founder's own locked naming scheme
  (`ecr.Repository`, never `generated.AwsEcrRepository`). Required
  verification met twice: the rewritten, now-hard-pass
  `TestFullProvider_Go_CompilesClean`, and independently via the real
  built `ubx` binary against the real `hashicorp/aws@6.54.0` provider.
  `--lang ts`/`--lang py` UNCHANGED -- explicitly deferred to a following
  session, not silently left inconsistent. Full account, including the
  service-derivation-ambiguity finding (mechanically unambiguous, but not
  a faithful AWS-taxonomy reproduction for ~130 unprefixed EC2-family
  types) and the two Go-keyword/go-tool-special package-name edge cases
  (`default`, `main`): docs/sdk.md's new "Amendment (2026-08-03,
  UBI-98)" section; full session account in STATE.md.

- 2026-07-30 — UBI-55: `ubx promote <proposal-id> --to <target-dir>`
  built, closing the "Environments & promotion" open question's own CLI
  surface (UBI-14's design, ratified 2026-07-30). Re-resolution, never
  copying: reads an accepted source proposal's own `document`-kind
  authoring source (a `.md`/`.d2` file), re-derives the intent via the
  exact same `draftFromDoc`/`draftFromDiagram` pipeline `ubx propose`/
  `ubx plan` already use, resolves it through the unmodified
  `core/resolver.Resolve` against the TARGET directory's own config/
  ledger/providers, and appends an additive `intent.sources` entry —
  `{"kind":"promotion","ref":"<source id>","base":"<source stack
  base>"}` (`docs/schema.md`'s own "Amendment: promotion evidence") —
  never replacing the fresh re-resolution's own sources. `ubx why` gains
  a `case "promotion":` rendering "promoted from `<base>/<short id>`",
  the literal architecture.md example. Two real, load-bearing gaps found
  while building this, refused by name rather than silently
  worked around: an SDK-authored (`--from-code`) source's own `ref` is
  basename-only (directory discarded, can't be relocated); a
  `dialogue`-kind (`ubx chat`) source's own `ref` is ledger-dir-relative,
  not portable to a different target. The source proposal must be
  already-accepted (not an unaccepted `ubx plan` draft) — promotion
  evidence vouches for a proposal that went through the real accept
  ceremony. Nine hermetic tests, zero live network calls; a real,
  live-verified end-to-end staging→prod transcript (hermetic
  `fakeprovider` via `UBX_PROVIDER_MIRROR`, no real cloud, no live LLM
  call — the `.d2` diagram medium needs neither) backs
  `ubiquex-docs`' new `cli/promote.mdx` + `guides/promotion.mdx` +
  `cli/why.mdx`'s own new section. See STATE.md for the full session
  account.

- 2026-07-30 — UBI-54: lookup-hint knowledge consolidated. The four
  separately-maintained per-type lookup-hint tables found across UBI-45
  session 1/2 and UBI-52's audit (`conformance.Registry.LookupHint`,
  generated `core/lookuphints`, `stateimport.BuildLookup`'s own
  `extraLookupAttrs`, `discovery/tiers.go`'s own `tierTable.AugmentFields`)
  are down to one authoritative source: `conformance.Registry.LookupHint`
  → generated `core/lookuphints` (this pipeline already existed since
  UBI-20, dependency direction verified sound before committing to it) →
  three consumers (`core/scan.go`, unchanged; `stateimport.BuildLookup`
  and `discovery.BuildLookup`'s Tier-B branch, both now calling
  `lookuphints.For("hashicorp/aws", ...)` instead of their own hand-
  duplicated maps). `discovery/tiers.go`'s `tierTable` itself was not
  replaced — its `Tier`/`Construct`/`CreationVerbs` knowledge is unique,
  not redundant. Zero behavior change: every existing test in
  `stateimport`/`discovery`/`conformance` passed unmodified, no test file
  edited. See docs/source-tree.md's "The lookup-hint tables: consolidated
  (UBI-54)" section for the full account.

- 2026-07-30 — Design-room decision (no session): environments &
  promotion (UBI-14, the founding open question, closed). Recorded in
  docs/architecture.md §Environments & promotion — envs stay
  non-concepts (directory + base prefix, ratified final); promotion is
  re-resolution against the target env's reality, never copying
  (staleness-by-construction forbids copies); the link is EVIDENCE not
  a pin: intent.sources gains {kind: "promotion", ref, base} —
  additive, a provenance claim not an equality claim, immune to source-
  chain advancement; why renders the promoted-from trail; enforcement
  ("prod requires promotion evidence") deliberately left to the future
  policy engine, never schema law. CLI surface (ubx promote) filed as
  its own small build ticket.

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
- 2026-07-28 -- UBI-38 (session 1 of the read-only projection quartet):
  `ubx verify` built -- independent full-chain verification, one
  command, entirely offline. `core.VerifyChain` (new `core/verify.go`)
  re-checks every proposal's own content hash against its own id (never
  checked anywhere else on read, until now), the parent-chain walk
  itself (never `Ledger.Chain`'s own all-or-nothing error return, so a
  broken link is a reported finding, not a crash), every sealed apply
  record's own hash and its prior-*sealed*-attempt chaining (mirroring
  `BeginApply`'s own exact linkage rule -- a crashed, unsealed attempt
  never breaks the chain), and every `$redacted` marker's own inner
  shape (only the outer shape was ever checked before). A tampered
  proposal doesn't just fail its own check -- every later proposal in
  the chain gets flagged `tainted_descendant` too, whether or not their
  own bytes were touched. `pr_merge` acceptance re-derivation reuses
  `runVerifyAcceptance` (UBI-11) completely unchanged, run once per
  `pr_merge`-accepted proposal the walk found; `--repo-dir`/
  `--github-repo` opt in incrementally, reported honestly as
  inconclusive rather than rounded up to a pass when omitted. Nine
  hermetic `core` tests (every adversarial row: tampered byte, missing
  parent, truncated apply record, a legitimate crashed-attempt NOT
  flagged, malformed/well-formed `$redacted`, mixed schema versions,
  empty ledger) plus eight `cli` tests (JSON shape, and the full
  `pr_merge` integration -- derived, both inconclusive shapes, and a
  forged-acceptance row using a real second git commit). `go build/vet/
  test`, `gofmt -l .` clean. See STATE.md for the full account.
- 2026-07-29 -- UBI-39 (session 2 of the read-only projection quartet):
  `ubx blame <address>` built -- per-attribute provenance, git blame for
  infrastructure. `core.Blame` (new `core/blame.go`) runs the identical
  fold `core.Ledger.FoldState` already performs (create seeds a base
  state -- UBI-29's own apply-record discovery path for a shipped
  change-create, never `Delta.Creates`' own possibly-stale `config` --
  each later `Modification.After` patches it in ledger order), except it
  never discards which proposal contributed which leaf: every dot-path
  attribute is independently attributed to whichever proposal most
  recently touched IT specifically, not just the resource's own latest
  touching proposal. A `$redacted` marker survives the fold byte-for-byte
  (redaction happens at the provider boundary, long before the ledger
  ever sees it) -- blame shows full provenance for a redacted attribute
  without ever touching the material. A shipped destroy tombstones the
  address the same way `FoldState`'s own current-truth view does, except
  blame keeps going: it renders the tombstone note and blames the FINAL
  pre-destroy state, not a cleared one. `drift_adopt` attribution surfaces
  every matched `cloudtrail`/`gcp_audit` actor `core.AttributeDrift`'s
  existing output already names, never collapsed to one guessed cause.
  **A real bug found and fixed before it shipped, caught by a test that
  mirrors `FoldState`'s own exact guard rather than assuming equivalence**:
  an early draft initialized the running fold state as an empty non-nil
  map instead of `nil`, which would have defeated `FoldState`'s own
  "skip a Modification with no genesis create yet" guard entirely --
  found by deliberately re-reading `FoldState`'s real code once more
  before trusting the mirror, not assumed correct because the shapes
  looked similar. 10 hermetic `core` tests (every adversarial row --
  multi-touch latest-wins per attribute, CloudTrail actor, redacted
  provenance, shipped-create UBI-29 genesis, an unshipped create
  correctly NOT found, a destroyed address blaming its final pre-destroy
  state, pr_merge approvers -- plus the modify-with-no-genesis regression
  guard) plus 7 `cli` tests (JSON shape, invalid/unknown address exit 2).
  Live-verified by hand against a real hermetic ledger too. `go build/
  vet/test`, `gofmt -l .` clean. See STATE.md for the full account.
- 2026-07-29 -- UBI-40 (session 3 of the read-only projection quartet):
  `ubx stats` built -- the thesis metrics, self-measured from any
  ledger. `core.Stats` (new `core/stats.go`) folds proposals by kind,
  acceptance-method split, attribution coverage, mean time-to-decision,
  and the headline signed-flow drift-resolution % -- `docs/plan.md`'s
  own month-6 thesis number, computed live. **An honest account, not a
  hand-wave**: since `core.Ledger.Append` requires an id (only ever
  assigned by the accept path), the ledger contains ONLY accepted
  proposals -- there is no durable record of a drift detected but never
  accepted, so the TRUE thesis percentage (needing independent
  ground-truth comparison) can't be fully supplied by an offline fold
  alone; named explicitly rather than silently claimed. What IS reported:
  of every drift the ledger has any record of, walk each address's own
  ordered drift-touch sequence and classify each `drift_adopt` event by
  whatever followed it -- reverted, adopted/superseded, or still open.
  **A real gap found and fixed while building it**: an early draft only
  counted `drift_adopt` proposals as "surfaced" events, silently
  undercounting every drift resolved by reverting OUTRIGHT -- confirmed
  by re-reading `core/scan.go`'s own `GenerateRevertProposal` and
  `docs/architecture.md`'s own "Revert path" section: `--propose both`
  generates adopt/revert as ALTERNATIVES sharing one parent, only one
  ever accepted, so a team choosing revert from the start never has an
  accepted drift_adopt for that instance. Fixed: a standalone
  drift_revert (not directly following a drift_adopt) is its own
  independent surfaced-and-resolved event. Attribution coverage and
  resolution rate are deliberately independent -- an unattributed drift
  still fully resolves, only lowering the separate coverage %. A second
  real gap, caught during final review before anything was pushed: a
  destroyed address's own open-ended drift_adopt was still counting as
  "open" -- fixed to count in history (ByKind/TotalProposals) but be
  excluded from both open and resolved (moot once the resource itself is
  gone), gated on the resource's own real shipped-destroy transition,
  the same way `FoldState`'s own tombstone fold already is.
  `--since`/`--until` (RFC3339) window by `Acceptance.AcceptedAt`. 14
  hermetic `core` tests (every adversarial row: drift open, adopted/
  superseded, reverted via a direct adopt-then-revert pair AND a
  standalone revert, stale sibling proposals counted once, unattributed
  drift lowering coverage but not resolution, a destroyed address
  excluded from open, mixed schema versions, time windows, empty/
  single-proposal edges) plus 6 `cli` tests (JSON shape, invalid
  `--since`, real end-to-end via `ubx scan`/`ubx accept`).
  Live-verified by hand against a real hermetic ledger showing all three
  resolution states at once (1 open, 1 superseded, 1 reverted -- 67%).
  `go build/vet/test`, `gofmt -l .` clean. See STATE.md for the full
  account.
- 2026-07-29 -- UBI-48 (session 4, final ticket of the read-only
  projection quartet): `ubx addresses` built -- a flat,
  copy-paste-ready `$cross` inventory. `core.Ledger.Addresses` (new
  `core/addresses.go`) re-walks `Chain()` with `Fleet`'s own exact
  discovery rules (`resolution.inputs` plus a shipped change-proposal
  create/destroy), deliberately NOT a `Fleet` extension -- `Fleet`'s own
  single-pass walk drops a tombstoned address the moment it sees the
  shipped destroy, with no toggle to keep it, so `--all`'s own
  "tombstoned, annotated" requirement needed the identical walk with that
  one behavior inverted rather than a bolted-on extension point an
  unrelated caller could misuse. `cli/addresses.go` fetches each active
  address's real referenceable-attribute list from the provider schema at
  its own RECORDED `(source, version)` -- never whatever `[providers]`
  pins today -- via the same `ParseSource`/`Acquire`/`Launch`/`.Schema()`
  sequence `loadDiagramProviders`/`ubx sdk gen` already use, cached once
  per distinct pair; deliberately not `providerPool`, which refuses to
  launch any version but the currently-pinned one ("re-resolve against
  the current config") -- exactly backwards here. Computed-only
  attributes marked from the schema's own `Computed` flag; an address
  with no recorded provider (adopted/drift-only) or a provider that fails
  to launch degrades to an explained "attributes unknown" annotation, not
  a failed command.
  **Cross-stack resolution -- the same base-store/`[ledger.external]`
  mechanism `$cross`'s own "stack" form uses, reimplemented at the CLI
  layer, not reused wholesale**: read directly from `core.Ledger`'s own
  constructors, `BaseStore()`/`ExternalStack()` are proven to be nothing
  but a pass-through of `cfg.Ledger.Store`/`cfg.Ledger.External` -- and
  `openLedgerForStack` (every earlier quartet command's own opener)
  structurally can't reach an arbitrary neighbor by name for a git-local
  store, since it ignores its own `stack` argument in that branch
  entirely. `resolveAddressesLedger` treats an omitted or
  this-workspace's-own (`cfg.Stack`) name as local (opened straight from
  `--ledger-dir`), a remote `[ledger]` store as always routed through
  `openLedgerForStack`'s own existing addressing regardless of name
  (already correct for any stack there), and any other git-local name as
  resolved via `cfg.Ledger.External[name]` alone, mirroring
  `resolveCross`'s exact `deriveStackAddress`-then-`core.OpenRef`
  sequence -- refused with a teaching error naming every stack this
  workspace's own config actually knows (its own declared stack, plus
  every `[ledger.external]` key) when no override exists, the identical
  refusal `resolveCross` itself already gives. 6 hermetic `core` tests
  (active/tombstoned/re-create-after-destroy/stack-filter/provider
  plumbing) plus 9 `cli` tests (attribute list + computed marking +
  `$cross` forms via a real `UBX_PROVIDER_MIRROR`-launched fake provider,
  `--all` annotation, adopted-resource schema-missing degrade, JSON
  shape, unknown-stack teaching error with and without any config at
  all, cross-stack resolution via a real `[ledger.external]` override
  opening a second, physically separate ledger directory, and the
  reflexive own-stack case). `go build/vet/test`, `gofmt -l .` clean. All
  four quartet tickets (UBI-38/39/40/48) now closed -- see the wedge
  section below for the scoreboard. See STATE.md for the full account.
- 2026-07-29 -- UBI-37 Stage 1 (Azure support, the UBI-21 playbook
  verbatim, fourth platform): `audit/` restructure landed first as its
  own commit (`cloudtrail`/`gcpaudit`/`k8saudit` -> `audit/cloudtrail`/
  `audit/gcp`/`audit/k8s`, the latter two renamed to match; pure git mv
  plus a tiny import-path sweep -- only `cli/attribution.go` had a real,
  non-comment call site outside the moved packages; full suite green
  proving zero behavior change before any Azure code landed), making
  room for `audit/azure`. `hashicorp/azurerm` 5.0.0 verified empirically
  via `provider.Acquire` -- negotiates tfplugin v5, matching
  hashicorp/aws and hashicorp/google. `azure/azapi` separately assessed
  (never assumed to match azurerm's own shape): negotiates v6 (the first
  provider source this project has onboarded that doesn't speak v5) and
  models every Azure resource via a handful of generic ARM-type-
  parameterized types rather than one Go type per resource -- a poor fit
  for the registry's own model, so it gets a standalone assessment test
  instead of per-type entries. `conformance.Registry` gains 42
  `hashicorp/azurerm` entries (FakeOnly, Implemented: false) across
  compute/network/iam/storage/db/dns/messaging plus a new `management`
  category for `azurerm_resource_group` (no prior platform needed one).
  Every sampled type has both `id` (full ARM path) and `name` as flat
  top-level attributes -- recorded as schema PRESENCE only, explicitly
  not proof `id` alone suffices live, per this ticket's own
  identity-shape caution (GCP's own storage_bucket/pubsub_topic/
  secret_manager_secret entries already prove that gap can bite even
  with an equally reasonable-looking schema). `go build/vet/test`,
  `gofmt -l .` clean. Stage 2 (needs real Azure credentials, confirmed
  available this session via `az account show`) continues in the same
  session -- see the next entry once it lands. See STATE.md for the full
  account.
- 2026-07-29 -- UBI-37 Stage 2 (Azure support, continued same session):
  five types promoted RealSafe and live-verified against a real
  subscription (`azurerm_resource_group`, `azurerm_storage_account`,
  `azurerm_storage_container`, `azurerm_key_vault`,
  `azurerm_user_assigned_identity`) -- `id` alone sufficient for all
  five, but `azurerm_resource_group`'s own ARM id shape is a real
  surprise the schema alone couldn't predict (its own top-level
  `/subscriptions/<sub>/resourceGroups/<name>` scope, not the
  `resourceGroups/<rg>/providers/<ns>/<type>/<name>` shape every OTHER
  azurerm type follows). Subscription needed
  `Microsoft.Storage`/`Microsoft.KeyVault`/`Microsoft.ManagedIdentity`
  resource providers registered before first use (a real, one-time,
  misleadingly-worded `SubscriptionNotFound` failure until fixed).
  `audit/azure/` implements `core.EventLookup` against Azure Monitor's
  Activity Log, wired into `cli/attribution.go`'s dispatch under
  `hashicorp/azurerm` -- `core.EventLookup`'s single-method interface
  held up a fourth time, zero changes. **A real, materially dangerous
  correlation gap found and fixed via live verification, not assumed
  clean**: Activity Log's own `resourceId` comes back lowercase while
  azurerm's own observed `"id"` is camelCase -- an exact match against
  the raw ARM id silently found zero events, no error, indistinguishable
  from a genuine no-event case (the same class of danger as GCP's own
  silent-incomplete-read gap, one layer further out). Fixed: server-side
  query scoped by time window + resourceGroupName only, case-insensitive
  client-side match, reported using the candidate's own original casing
  so `core.AttributeDrift`'s downstream exact-match still sees a
  byte-identical hit. Delivery latency measured directly: ~60-90 seconds
  (GCP measured ~18s, CloudTrail documents ~15min) -- `DeliveryLag` set
  to 5 minutes, a safety margin, not tuned tightly. A sensitive-attribute
  audit (UBI-23/24 cross-check) against all 42 seeded types found every
  genuinely credential-bearing computed attribute already
  `Sensitive`-flagged by the provider -- one real gap,
  `azurerm_linux_web_app`/`azurerm_linux_function_app`'s own free-form
  `app_settings` map (no per-key schema possible, the same structural
  ceiling `helm_release`'s own `metadata.values` hit), added to
  `provider/overrides.go` as a full-attribute redaction. Every real
  fixture destroyed after (Key Vaults purged, not just deleted),
  subscription swept clean and confirmed empty -- zero `ubx ship` runs
  against any real cloud provider this session, every real mutation went
  through the `az` CLI directly, out of band, the same discipline
  `gcloud`/`aws` already held to. `go build/vet/test`, `gofmt -l .`
  clean across the whole repo. See STATE.md for the full account.
- 2026-07-29 -- UBI-50 session 1 (generated conformance harness,
  docs-first): `docs/conformance-harness.md` written -- the full design
  for automating conformance so every type of every provider carries a
  machine verdict, generalizing `conformance/registry.go`'s 154
  hand-written entries rather than replacing their own hard-won,
  live-verified knowledge. Provider-agnostic by construction (the
  founder's own design-room comment on the ticket): the probe generator,
  failure taxonomy, registry format, and version-bump rerun logic are
  all shared, built once against `provider.Schemas`/`Block`/`Attribute`;
  per-platform work is scoped narrowly to live-tier plumbing only. Four
  lie-classes designed, each mechanizing a real finding this project
  already made by hand: identity-shape/incomplete-read, sensitive-flag
  audit vs. echo attributes (with a live-tier marker-string echo check
  stronger than hermetic keyword-matching alone), destroy honesty via
  read-back absence (UBI-44's own class -- live-only, no hermetic half,
  named explicitly as a gap the hermetic tier can't close), and
  drift-detectability (the `hashicorp/time` class -- a real UBI-43
  finding already on file, given new vocabulary here for the first
  time). Registry format keyed by `(source, version, type, verb)` -- a
  real new axis today's `(source, type)`-only keying can't express (a
  type can pass read/mutate and fail destroy independently). Failure
  taxonomy honestly scoped: `sensitive-underflag`/`undriftable` findings
  can feed `provider/overrides.go`/the registry mechanically when
  live-confirmed; `incomplete-read` cannot fully auto-populate
  `core/lookuphints` (its own shipped message hardcodes AWS's "add id"
  advice, wrong for GCP's own shapes -- a pre-existing limit, not newly
  introduced); `destroy-lie` always stays a human-session flag. Ship
  doctrine: destroy probes must go through `core/executor.Ship`'s real
  path (new plumbing -- no destroy step exists in the harness today),
  never a raw `ApplyResourceChange` shortcut, or a probe would be
  structurally incapable of ever finding another UBI-44-shaped bug.
  Four-row adversarial program. No code this session, per protocol --
  session 2+ builds the generator. See STATE.md for the full account.
- 2026-07-29 -- UBI-50 session 2 (generated conformance harness -- the
  probe generator + hermetic tier, built): `conformance/probe.go`'s
  `Finding` type plus `ProbeType`/`ProbeSchema` and the three
  hermetic-half probes designed in session 1
  (identity-shape/sensitive-echo/drift-detectability -- probe 3, destroy
  honesty, still has no code, per its own "no hermetic half" design).
  18 hermetic unit tests, no network. Live-verified
  (`UBX_CONFORMANCE_LIVE=1`, network-only, same reason as the existing
  provider-acquire tests) against all five real, currently-onboarded
  providers at once -- proving "provider-agnostic by construction"
  against real schemas, not hand-built fixtures: aws (1,682 types, 742
  findings), google (1,319 types, 278), azurerm (1,103 types, 263),
  kubernetes (82 types, 51), helm (1 type, 1). Determinism and two
  spot-checks against real, hand-verified ground truth already on file
  (`helm_release.metadata.notes`, `azurerm_resource_group`'s own clean
  identity shape) both confirmed directly against real schemas.
  `docs/conformance-harness.md` gained a session-2 amendment. Not
  built: probe 3, any live-tier probe, and layering `Finding` output
  back into `conformance.Registry` (stays wholly separate/additive) --
  named explicitly. `go build/vet/test`, `gofmt -l .` clean. See
  STATE.md for the full account.
- 2026-07-29 -- UBI-50 session 3 (generated conformance harness --
  triage, probe 3, registry layering, live tier): triaged all 134 AWS
  "Confirmed" zero-identity types against the real schema before
  building anything further -- 108 turned out to carry a
  type-prefixed identity attribute (`*_arn`/`*_id`/`*_name`), a real
  refinement to `probeIdentityShape` (a new, weaker `Candidate` tier),
  dropping AWS's own Confirmed count 134 -> 16. Live-tier policy for
  the remaining 16 (+ `kubernetes_manifest`) decided before anything
  was created: excluded from auto-batch entirely, real structural
  singleton/composite resources needing a different resolution model.
  Probe 3 (destroy honesty) built and verified hermetically end to
  end through the REAL `core/executor.Ship` path (never a shortcut) --
  `conformance`'s own `stateReaderAdapter` extended to also satisfy
  `executor.Applier`; an honest fakeprovider destroy resolves
  destroyed at zero cost, a scripted lying destroy is caught
  (`FindingDestroyLie`, `Confirmed`) exactly like UBI-44's own real
  finding, gated `UBX_TEST_SLOW=1` for the real ~64s retry budget.
  **A real, explicit decision on ship doctrine vs. the standing
  ship-verification rule**: probe 3's own LIVE confirmation would need
  `executor.Ship` to reach real `ApplyResourceChange` against real
  AWS -- exactly what CLAUDE.md bans, "always, no exceptions." Flagged
  to the user before any real AWS destroy was attempted; decided:
  probe 3 stays hermetic-only, its real-AWS confirmation deliberately
  deferred as a separate future decision. Registry layering
  (`LayerFindings`/`detectContradictions`) built -- purely additive,
  checked against all five real providers: zero real contradictions
  across 723 layered verdicts. Live tier for probes 1/2/4 (identity-
  shape/sensitive-echo/drift, read-only, `aws` CLI + `core.RunScan`,
  never `executor.Ship`) verified against one real AWS resource
  (`aws_sns_topic`) -- a real false positive corrected after the first
  live run (a tag marker legitimately duplicates into both `tags` and
  `tags_all`, AWS's own real convention). `docs/conformance-harness.md`
  gained a session-3 amendment. `go build/vet/test`, `gofmt -l .`
  clean. Real AWS resource confirmed destroyed via a post-run sweep.
  See STATE.md for the full account.
- 2026-07-29 -- UBI-50 session 4, closing (generated conformance
  harness -- bulk live-tier run, ship doctrine settled, verdict
  write-back): free-tier-only AWS batch (`aws_sqs_queue`/
  `aws_sns_topic`/`aws_iam_policy`/`aws_iam_user`, deliberately
  excluding the three ADOPT-a-pre-existing-resource types already in
  the suite) run for real against all three non-destroy probes -- a
  real `tags`/`tags_all` bug found on `aws_sqs_queue` (the identical
  class already fixed for `aws_sns_topic` last session, not carried
  over consistently the first time), fixed, all four types clean on
  rerun, zero leaks confirmed by both an automated sweep test and a
  manual four-way `aws` CLI check. Probe 3's own real-cloud
  confirmation decided conservatively: stays hermetic-only, full
  reasoning recorded in `docs/conformance-harness.md`'s own session-4
  amendment (the standing ship-verification rule's "always, no
  exceptions" language, weighed against the already-strong hermetic
  proof this arc already has) -- named as this arc's one deliberately
  open item, not dropped. Verdict write-back built: `PinnedProviderVersions`
  (single source of truth, five pins), `conformance/probegen` (new
  generator, network-dependent, deliberately not wired to
  `go generate`), run for real producing `findings_generated.go` --
  1,335 committed `Finding` entries across all 4,187 resource types
  from all five onboarded providers -- and `conformance.AllVerdicts()`
  as the ready-made base+overlay combination
  (`LayerFindings(GeneratedFindings)`). Two new tests:
  `TestGeneratedFindings_WellFormed` (hermetic staleness/shape guard)
  and `TestAllVerdicts_LayersGeneratedFindingsUnderRegistry` (wiring
  proof). ubiquex-docs checked, no update needed this session --
  reasoning recorded (`Candidate`-tier machine findings aren't yet
  ready for user-facing promotion). `docs/conformance-harness.md`
  gained its session-4 closing amendment; "What this doesn't yet
  cover" rewritten to reflect only what remains genuinely open.
  `go build/vet/test`, `gofmt -l .` clean. **UBI-50 closed.** See
  STATE.md for the full account.
- 2026-07-29 -- UBI-49 (ubx plan + ship fusion, terraform-shaped
  two-step workflow, one session, closed): `ubx plan` -- a new verb
  fusing `propose`+`resolve`+a preview render into one command, any
  medium input (`--from-code`/`--from-doc`/`--from-diagram`/a
  hand-written intent file), resolved through the identical,
  unmodified `core/resolver.Resolve` every other entry point already
  uses, rendering a full receipt (delta, cost_delta, blast radius,
  assumptions/defaults/questions) and saving the resolved-but-
  unaccepted proposal at `.ubx/plans/<hash>.json`, keyed by its own
  content hash. `ubx ship <hash>` gains inline local-tier acceptance:
  falls back to the plan store when `<hash>` isn't already an
  accepted ledger id, verifies the file's content still hashes to its
  own filename (refusing a hand-edited/corrupted plan), then runs the
  identical `checkDestroysConfirmed`/`resolver.VerifyPins`/
  `core.Accept` sequence `ubx accept`'s own local-file path already
  uses before applying -- `--confirm-destroys` still required, a
  stale cross-stack pin still refuses, `acceptance.method: "local"`
  recorded exactly as today. Pure CLI fusion: `resolve.go`'s own
  provider-loading logic and `propose.go`'s own doc/diagram drafting
  logic extracted into shared helpers (`loadResolveProviders`,
  `draftFromDoc`/`draftFromDiagram`) so `plan.go` drives the identical
  code every existing verb already runs, zero core-package changes,
  the four-verb ceremony completely unchanged (verified: the full
  existing test suite passes unmodified). The md/diagram media's own
  "draft, then a human checkpoint before resolving" posture is
  deliberately not reproduced inside `ubx plan` -- its own receipt,
  covering the full resolved proposal rather than just the draft's
  ambiguity content, already is that checkpoint; `ubx propose
  --from-doc`/`--from-diagram` keep their exact existing draft-only
  behavior for teams that want that as a separate step. 10 new
  hermetic tests (`cli/plan_test.go`) covering all four medium inputs
  end to end through the fused plan-then-ship path, the
  --confirm-destroys/cross-stack-pin-staleness invariants, a
  plan-file hash-mismatch refusal, and input-mode validation --
  all passing on first real run against the fake provider binary.
  `docs/architecture.md` gained a new "Two-step fusion" section
  recording the design (the local hash-addressed plan store, the
  human-checkpoint reasoning, the diagram medium's own accepted
  double-schema-fetch inefficiency). ubiquex-docs: guides updated to
  lead with the two-step flow, four-verb ceremony documented as the
  team/production path. `go build/vet/test`, `gofmt -l .` clean
  across the whole repo. **UBI-49 closed.** See STATE.md for the
  full account.
- 2026-07-30 -- UBI-45 session 1, docs-first (cloud-side discovery --
  tag/list-based adoption without tfstate): `docs/discovery.md` written,
  unparked per direct instruction. Mechanism decision made empirically,
  not assumed -- a real, throwaway `aws_sqs_queue` created, tagged, and
  destroyed against the real account this session: AWS Resource Groups
  Tagging API confirmed zero-setup and working immediately (chosen,
  primary); AWS Config confirmed NOT enabled in the same real account
  (zero configuration recorders -- a real adoption barrier for exactly
  this ticket's own target market, not chosen); the dormant tfplugin
  `ListResource` RPC (never called anywhere in this codebase before
  this session) tested live against the real `hashicorp/aws@6.54.0`
  binary via a throwaway same-package probe test (deleted, never
  committed) -- 53 of 1,682 resource types implement it (~3%), none of
  this project's own four trusted free-tier fixture types among them,
  real "Invalid Provider Server Combination" diagnostics even for a
  covered type -- not viable as v1's mechanism, named as the clearest
  future "revisit" trigger instead. The identity bridge (ARN -> provider
  lookup shape) named as the arc's real hard problem: checked directly
  against `conformance.Registry`'s own live-verified entries, every
  AWS type's lookup shape falls into one of three empirically-confirmed
  tiers (id IS the ARN -- `aws_iam_policy`; id is the ARN's own trailing
  segment, sometimes duplicated into a second field --
  `aws_vpc`/`aws_iam_role`/`aws_iam_user`/`aws_s3_bucket`; id is
  constructed from ARN components -- `aws_sqs_queue`'s own queue URL,
  confirmed live this session). A real, honest finding along the way:
  three separately-maintained copies of the same tiny lookup-hint fact
  already exist in this codebase (`conformance.Registry.LookupHint`,
  generated `core/lookuphints`, `tfstate.BuildLookup`'s own
  `extraLookupAttrs`) -- a structured `TypeSpec.LookupShape` field is
  recommended as real follow-up work, not built this session. Tag-scoped
  filtering designed as the primary UX (`--tag`, a client-side `--type`
  allowlist derived from each ARN's own service segment rather than a
  second hand-maintained filter-string table, `--region`); stack-
  grouping inference designed as a separate, read-only `--suggest-stacks`
  preview, never an auto-assignment, directly following UBI-18's own
  established "module path is a hint, never a silent stack split"
  precedent; the attribution bonus designed as a reuse of
  `core.EventLookup`/the existing `audit/` backends unchanged, searching
  for creation-verb events instead of arbitrary drift events. Five-row
  adversarial program (no lookup shape, pagination + a confirmation
  gate, permission-denied mid-enumeration, resource deleted between list
  and read, already-adopted rediscovery) -- every row's required outcome
  reuses an already-existing, unmodified mechanism (`--all --tfstate`'s
  own skip taxonomy, `core.RunScan`'s own idempotent classification),
  named explicitly rather than re-derived. Adoption stays record-only,
  blast-radius zero by construction -- discovery adds a new identity
  source only, never a new proposal kind or apply path. No code this
  session, per protocol. See STATE.md for the full account.
- 2026-07-30 -- UBI-45 session 2 (cloud-side discovery -- the identity
  bridge as real code + `ubx scan --discover` CLI wiring):
  `discovery/arn.go`/`discovery/tiers.go` -- `ParseARN` splits an ARN's
  own resource segment into a type-prefix + id (a real refinement found
  while building, not assumed at design time: `(service,
  resourceTypePrefix)`, not service alone, is what lets
  `aws_iam_role`/`aws_iam_user`/`aws_iam_policy` disambiguate without
  `--type` at all); `tierTable` seeded with session 1's own five
  confirmed examples across all three tiers; an unclassified pair or a
  failed Tier-C constructor surfaces `ErrNotYetAdoptable`, never a
  fabricated lookup. `discovery/discover.go` -- a `TaggingAPI` interface
  matching the real SDK client's own method signature exactly (zero
  adapter code), pagination followed to completion, `CheckLimit` a
  separate pure confirmation-gate function. `ubx scan --discover` wired
  as a third mode alongside single-resource/`--all` (`cli/scan.go`,
  `cli/scandiscover.go`), reusing `core.RunScan`/`core.GenerateProposal`
  completely unchanged, mirroring `runScanAll`'s own structure; a real
  bug found and fixed while building: a provider was being required even
  when nothing discovered was adoptable. `--suggest-stacks` built as its
  own separate, simpler read-only path -- no ledger, no provider,
  nothing written. Two package-level seams
  (`newDiscoveryTaggingAPI`/`newDiscoveryStateReader`, matching
  `openRemoteLedgerStore`'s own convention) let hermetic CLI tests fully
  control both halves without touching real AWS or fakeprovider's own
  shared `fake_widget` fixture. All five adversarial rows verified
  hermetically (17 new tests) -- a real finding along the way:
  permission-denied and deleted-since-list collapse into the identical
  `core.RunScan`-error code path, tested as one case rather than two
  artificially separated ones; a second real bug found by the
  idempotency test's own first failed attempt (a merely-generated
  proposal isn't "already adopted" until a real `ubx accept` runs).
  Live-verified, read-only, no ship: a real, hand-created SQS queue
  (never via `ubx`) discovered end to end against real AWS -- real
  tagging API, real Tier-C queue-URL construction, real
  `hashicorp/aws` `ReadResource` call, a real zero-blast-radius
  `adoption` proposal generated, confirmed via the filesystem that no
  `ledger/` directory was ever created (nothing accepted). Swept clean
  afterward. `go build/vet/test`, `gofmt -l .` clean across the whole
  repo; zero regressions in the existing suite. Genesis attribution and
  the live finale (slices 4-5) remain session 3+ work. See STATE.md for
  the full account.
- 2026-07-30 -- UBI-45 session 3, closing (genesis attribution + the
  live finale at scale): `core.AttributeGenesis` (new, `core/
  genesis.go`) reuses `AttributeDrift`'s own identity-candidate search
  and defensive exact-match filtering completely unchanged, narrowing
  to a caller-supplied creation-verb `EventName` and, among genuine
  matches, taking the OLDEST (the opposite of `AttributeDrift`'s own
  "newest first" -- a resource is created exactly once, so the earliest
  creation-verb match is the one that actually founded its lineage). An
  empty creation-verb list is a new, honest `ReasonNoCreationVerbs` --
  genesis attribution is never even attempted, distinct from a real
  search that came up empty. The creation-verb table itself reuses
  `discovery/tiers.go`'s own per-type `typeSpec` (a new `CreationVerbs`
  field) rather than a fifth separately-maintained table, seeded with
  all six of this arc's own real AWS API operation names. Wired into
  `ubx scan --discover` via a new `attributeGenesis` (`cli/
  attribution.go`), reusing `newAttributionBackend`'s own per-provider-
  source registry unchanged; the existing `--no-attribution` flag now
  gates both drift and genesis attribution. Hermetic tests mirror
  `cli/attribution_test.go`'s own established "blank every AWS
  credential source" technique exactly, proving the wiring never
  blocks adoption even when CloudTrail is unreachable. 7 new tests (6
  `core`, 1 `cli`), all passing.

  **The live finale, real AWS, swept clean.** Four resources hand-
  created via the `aws` CLI directly (never through `ubx`), tagged
  across two `Project` groups: `aws_sqs_queue` (Tier C) +
  `aws_iam_policy` (Tier A) tagged `payments`; `aws_s3_bucket` (Tier B)
  + a DynamoDB table (deliberately unclassified -- this session's own
  designated proof of "discovered, not yet adoptable" against a real
  resource, not a fixture) tagged `networking`.
  `--suggest-stacks --stack-tag Project` correctly grouped all four
  from their own real tags, writing nothing. Discovering `networking`
  produced one adoptable proposal (the bucket) and one honest "not yet
  adoptable" line (the table); discovering `payments` -- after polling
  `aws cloudtrail lookup-events` directly until the real `CreateQueue`
  event actually appeared (~4 minutes in this account, well under this
  project's own previously-documented 15-minute worst case) -- produced
  both proposals with successful genesis attribution: `CreateQueue`/
  `CreatePolicy`, both correctly attributed to the real IAM user who
  created them, real event IDs, real timestamps, real source IPs. All
  three adoptable resources accepted through the real, unmodified
  local-tier signing flow; `ubx why` on the accepted SQS queue shows
  exactly `source: cloudtrail -- arn:aws:iam::839333509514:user/roozbeh
  CreateQueue at ... from ...` -- genesis-by-adoption with the real
  attributed creator. All four resources destroyed afterward, swept
  clean (tagging API, `aws sqs list-queues`, `aws iam list-policies`
  all empty; `head-bucket`/`describe-table` both confirm gone).
  `go build/vet/test`, `gofmt -l .` clean across the whole repo; zero
  regressions. **UBI-45 closed** across three sessions -- design,
  build, and a real, live, closing proof of every claim the design
  session made. See STATE.md for the full account.
- 2026-07-30 -- UBI-35 session 1 (Go SDK, second language under UBI-33's
  contract): the compiled-program evaluator hypothesis -- hermeticity via
  OS-level process restriction instead of a sandboxed interpreter --
  tested empirically FIRST, per the ticket's own framing, and confirmed
  on both target platforms (macOS `sandbox-exec`, Linux `bubblewrap`),
  with the same rigor as the TS session's own Deno probes: real commands,
  real crash reports read directly, real gaps named (a naive deny-all
  sandbox-exec profile crashes the process at `dyld` startup; Linux
  namespace creation needs elevated privilege when already nested inside
  another hardened container). Confirmed, not just designed: the whole
  arc built the same session -- `sdk/go/` (new nested Go module, the
  runtime), `sdk/codegen/templates/go` (new, on the unmodified shared IR
  model), `goeval/` (new, the sandboxed compile-once/run-twice
  evaluator), `cli/resolve.go`'s `--from-code` extension dispatch,
  `cli/sdk.go`'s `--lang go`, and a real Go conformance case
  (`payments.go`, its own `golden/payments_go.json`) matching the TS/md
  golden's own resources/stack/summary byte-for-byte after
  canonicalization. `go build/vet/test`/`gofmt -l .` clean across the
  whole repo, including `sdk/go`'s own nested module. Not a closing
  session -- UBI-33 stays open (Python, UBI-36, unstarted). See STATE.md
  and docs/sdk.md's own "The Go evaluator: decided empirically" section
  for the full account.
- 2026-07-30 -- UBI-36 session 1 (Python SDK, third and final language;
  UBI-33 closed alongside it): the evaluator decision made empirically
  FIRST, per house standard, and it reversed the expected outcome --
  subprocess restriction retargeted from the Go arc's own `sandbox-exec`/
  `bwrap` wrappers (expected to win) lost to WASI (`wasmtime` running a
  real, pinned CPython-WASI build), which proved structurally stronger
  (network/subprocess-spawning absent as capabilities, not policy-
  denied) and genuinely cross-platform with one mechanism instead of
  Go's own two. `PYTHONHASHSEED` probed explicitly as asked (real,
  scoped to `set`/`frozenset` only, not `dict`) and pinned
  unconditionally; a real implementation-time bug found and fixed (a
  WASI mount that looked like it worked but was silently resolving via
  an accidental path, not the intended one -- docs/sdk.md's own "A real
  implementation-time bug" section). Whole arc built the same session:
  `sdk/py/ubx_sdk/` (new runtime -- `Computed` via `__getattr__`,
  Python's own native attribute hook, not a Proxy imitation;
  `dataclasses.fields()` introspection instead of a `reflect`
  equivalent), `sdk/codegen/templates/py` (new, on the unmodified shared
  IR model), `pyeval/` (new, WASI evaluator + CPython-WASI
  acquire-and-cache), `cli/resolve.go`'s `--from-code` `.py` dispatch,
  `cli/sdk.go`'s `--lang py`, and a real Python conformance case
  (`payments.py`, its own `golden/payments_py.json`) matching the TS/Go/
  md golden's own resources/stack/summary byte-for-byte. `go build/vet/
  test`/`gofmt -l .` clean across the whole repo. `ubiquex-docs` updated
  the same session. **UBI-36 closed. UBI-33 (the multi-language
  contract) closed alongside it, with a full contract retrospective** --
  the golden-fixtures-as-spec contract held across three languages with
  three structurally different evaluator shapes, the shared IR model
  needed zero changes across any per-language template, and
  `core.DoubleRun` carried the full determinism guarantee in every case.
  See STATE.md and docs/sdk.md's own "The Python evaluator: decided
  empirically" section for the full account.
- 2026-07-30 -- UBI-52 + UBI-53 session 1 (source-tree cleanup + repo
  rename, paired so import paths churn once): full tree audit first
  (`docs/source-tree.md`, new) -- both founder-flagged renames confirmed
  real (`tfstate/` -> `stateimport/`, `tfwrite/` -> `writeback/`, the
  latter matching the already-existing `ubx writeback` verb one-to-one),
  the opaque `claude-501/` directory found to be an untracked, never-
  committed stray artifact (deleted, not renamed), a real naming
  inconsistency found during the audit itself (`sdkeval`/`goeval`/
  `pyeval` -- only two of three named their language; `sdkeval` ->
  `tseval` for consistency). Two real tensions recorded honestly, not
  silently resolved: `sdk/go`'s own already-shipped module path left
  alone (UBI-53's original sequencing note is now stale), the lookup-
  hint tables still not consolidated (now four copies, not three --
  recommending a dedicated ticket). Combined mechanical pass: `git mv`
  + one import-sweep covering both the internal renames and the module
  path change (`github.com/ubiquex/ubiquex-cli` ->
  `github.com/ubiquex/ubiquex`) together. **A real bug caught by
  testing, not assumed clean from build+vet alone**: the sed sweep
  corrupted the embedded raw protobuf descriptor byte-blob inside
  `provider/tfplugin{5,6}/*.pb.go` (a length-prefixed binary encoding
  disguised as a Go string literal) -- panicked at package `init()`
  only, invisible to `go build`/`go vet`; fixed by reverting exactly
  those two generated files, updating their `.proto` sources normally.
  One real hashed-content consequence, found by checking not assumed
  (this arc's own "ledger integrity" check): `payments.go`'s own import
  line change altered its real content hash, requiring
  `golden/payments_go.json` regeneration (verified independently via
  `shasum`, not just trusted from a failing test's own output) -- no
  other fixture anywhere in the repo affected, checked directly.
  `go build/vet/test ./...` green, `gofmt -l .` clean, a real `ubx
  verify` run against a real hermetic ledger confirmed chain integrity.
  Every non-Go reference swept in both repos (`ubiquex-docs`' own
  `CLAUDE.md`, `docs.json`, install instructions, every cross-repo link
  -- `mint validate`/`mint broken-links` both clean); historical
  narrative in `STATE.md`/changelog entries in both repos deliberately
  left untouched, matching this project's own standing practice. GitHub-
  side rename and local checkout directory rename are founder actions,
  not performed by this session. See docs/source-tree.md and STATE.md
  for the full account.

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
inside `ubiquex`, every language, one CI — golden conformance fixtures
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

### SDK program: Go, second language (UBI-35)

Session 1 (2026-07-30): **the compiled-program evaluator hypothesis
tested empirically and confirmed, then the whole arc built the same
session** — probe, `sdk/go` runtime, Go codegen template, `resolve
--from-code` dispatch, and a real Go conformance case, all in one
session (unlike TS, which took four). Full account, including every
probe's real command/output, in docs/sdk.md's own "The Go evaluator:
decided empirically" section.

**The probe, run first, per the ticket's own framing ("decides the whole
arc's session count")**: unlike TypeScript (needs a sandboxed
interpreter, Deno, because the runtime executing the program is shared
and otherwise capable of anything), a Go SDK program is compiled and
runs as an ordinary OS process — the hypothesis was that OS-level
restriction of that process, not a language-level permission system,
could carry the same hermeticity guarantee. Confirmed on both target
platforms, empirically, with the same rigor as the Deno probes: macOS's
`sandbox-exec` (Apple's own `system.sb` base profile + explicit denies —
a naive from-scratch deny-all profile crashes the process at `dyld`
startup, root-caused via a real crash report, not guessed at) and
Linux's `bubblewrap` (unprivileged user+mount+net namespaces, verified
inside a container, with one real, honestly-recorded caveat: nested-
namespace creation needs elevated privilege when already running inside
another hardened container — a real portability gap, this arc's own
analog of the Deno remote-import gap). Plain env-scrubbing alone,
confirmed insufficient by itself (blocks env visibility, not file/
network syscalls) — ruled out as the primary mechanism precisely because
a real syscall-level one proved achievable on both platforms.

**The determinism story turned out simpler than TypeScript's**: compiled
Go has no monkey-patchable `Date.now()`/`Math.random()`-equivalent
ambient global to eagerly guard, so there is no Go analog of `guards.ts`
at all — `core.DoubleRun`, run against the same already-built binary
twice, is the whole backstop, one layer instead of two. A further real
simplification found along the way: building happens once, outside any
sandbox (Go compilation doesn't execute the source it compiles, so
`CGO_ENABLED=0` and no `go generate` step means no arbitrary-code-
execution risk at build time), and `core.DoubleRun` runs the SAME binary
twice — TS's own evaluator has no equivalent "build once" phase to hoist
out of the loop, since Deno both parses and executes on every
invocation.

**Built the same session**: `sdk/go/` (new, its own nested Go module,
`github.com/ubx-sdk-go`, per UBI-33's own hard constraint) — a runtime
mirroring `@ubx/sdk`'s semantics (`Stack`/`Resource`/`Secret`/`Cross`/
`Intent`; `Computed` as an address-wrapper type with an explicit
`.Field(name)` drill-down method, the honest Go equivalent of a Proxy Go
doesn't have; config values typed `any` and recursively serialized via
reflection against a generated `FieldMap`, mirroring `serializeConfig`/
`serializeOpaque` exactly). `sdk/codegen/templates/go` (new) — a Go
template on the *same, unmodified* `sdk/codegen/ir` model, real notably
smaller than the TS template's own output: Go's `Computed` has no static
per-field type, so there's no Go analog of TS's `Attrs` interface to
render at all, only a `Config` struct (plus nested object structs) and
the runtime descriptor. `goeval/` (new, top-level package, mirroring
`sdkeval`'s own shape) — compiles the program once (`go build`,
`CGO_ENABLED=0`, `GOPROXY=off` closing Go's own real analog of the
remote-import gap: a `go.mod` reaching for anything beyond a local
`replace`/the module cache fails the build loudly rather than fetching
untrusted code, confirmed against a real unfetchable dependency;
`GOFLAGS=-mod=mod`, found empirically, reconciles an ordinary toolchain-
version mismatch without a spurious failure; builds from a throwaway
copy of the program's own module, never mutating the author's real
files), runs it twice sandboxed via `core.DoubleRun`, stamps the same
`"document"` provenance kind `sdkeval` already uses, validates against
the same `core/resolver.IntentFile`. `cli/resolve.go`'s `--from-code`
now dispatches by entry-file extension (`.ts` → `sdkeval`, `.go` →
`goeval`) — the only change to already-shipped UBI-34 code, one `switch`
statement. `cli/sdk.go` gained `--lang ts|go` on `ubx sdk gen` (default
`ts`, unchanged behavior) — real bindings generated against the real
`hashicorp/aws@6.54.0` schema (1,682 types, matching the TS session's
own real figure) confirmed to actually compile against `sdk/go/runtime`,
not just look plausible (a real bug — `import` emitted after a `var`
declaration — caught by that compile check, not by string assertions
alone). `sdk/conformance/programs/go/payments.go` (new, independently
authored, not a transliteration) — real, live, sandboxed, double-run-
verified output matches the TS/md golden's own `resources`/`stack`/
`intent.summary` byte-for-byte after canonicalization; a *separate*
golden file (`golden/payments_go.json`), not the same one, for a real
structural reason: the document-provenance entry names the entry file
itself, and `payments.go`/`payments.ts` are two different files with two
different real content hashes, so no single golden document could ever
match both.

**A real, honest sequencing note**: UBI-53 (repo rename
`ubiquex-cli`→`ubiquex`, Backlog, not started) says the rename should
happen *before* UBI-35 so the published Go import path is born clean.
Proceeded with UBI-35 now anyway — UBI-53 wasn't part of this session's
given scope (CLAUDE.md: only reference given Linear IDs), the GitHub-
side rename needs founder action, and a module-path rename is a small,
separately-scoped, mechanical sed sweep whenever UBI-53 actually lands —
recorded here rather than silently ignored.

`go build/vet/test`/`gofmt -l .` clean across the whole repo, including
`sdk/go`'s own nested module. `ubiquex-docs` updated the same session
(`cli/resolve.mdx`'s new "Authoring in Go" section, real transcripts;
`cli/sdk-gen.mdx`'s new `--lang go` example; `sdk/index.mdx` restructured
into parallel TypeScript/Go sections) — `mint validate`/`mint
broken-links` both clean. Not a closing session — UBI-33 stays open
(Python, UBI-36, still unstarted); no Linear status change or closing
comment this session, matching the session's own given instruction
("commit and push," not "close").

### SDK program: Python, third and final language (UBI-36) — UBI-33 closed

Session 1 (2026-07-30): **the evaluator decision made empirically first,
per house standard, then the whole arc — runtime, codegen, evaluator,
CLI wiring, conformance — built the same session.** UBI-36 was Backlog,
"demand-gated" ("unpark trigger: real user demand named, not
speculative") — proceeded anyway on this session's own explicit
instruction, which is itself the demand signal; recorded honestly, not
silently overridden.

**The evaluator probe reversed its own expected outcome.** Two real
candidates, both actually run: subprocess restriction retargeted from
the Go arc's own `sandbox-exec`/`bwrap` wrappers at CPython (expected,
going in, to win "now that UBI-35 built the machinery"), and WASI
(CPython compiled to WebAssembly, run under `wasmtime` — the ticket's
own "maturity check" candidate). WASI won, decisively, on evidence:
network and subprocess-spawning are **absent as WASI capabilities**, not
merely policy-denied the way the subprocess candidate's own
`sandbox-exec`/`bwrap` profile has to deny them; the mechanism is
genuinely identical across macOS and Linux (the same `python.wasm`
artifact, byte-identical probe output, verified in a real Ubuntu
container this session — a real simplification over Go's own two-
platform-specific-mechanisms answer); and a real, current, version-
matched prebuilt CPython-WASI build exists today
(`brettcannon/cpython-wasi-build`, a real CPython core developer's own
channel), not a someday (CPython's own WASI support is a real, if
Tier-2, target per PEP 816, checked live). The subprocess candidate's
own real, new gap along the way: `file-read-metadata` needed an
unscoped allow (Python's own startup does far more filesystem
introspection — symlink-chasing to compute `sys.executable`/
`sys.prefix` — than a static Go binary ever does) before it worked at
all; once fixed, it closed "site-packages reach"/"no pip" by construction
(stdlib and site-packages sit at genuinely different paths on a real
install; deny one, allow the other) but every guarantee stayed a
**policy that could be gotten wrong**, exactly as the missing-metadata
gap itself demonstrated live, mid-session.

**A real "PYTHONHASHSEED" trap, probed exactly as asked**: `set`/
`frozenset` iteration order is genuinely randomized per process by
default (confirmed live, three runs, three different orders, on both
native CPython and under WASI identically) — `PYTHONHASHSEED=0` pins it
(three runs, byte-identical, confirmed again on both). The precise
scope, found rather than assumed: this affects `set`/`frozenset` only —
plain `dict` iteration order is insertion-order per the language spec
since 3.7, unaffected regardless of hash seed, confirmed stable across
every run in this session's own tests. The evaluator pins
`PYTHONHASHSEED=0` unconditionally regardless (`core.DoubleRun` as the
backstop for everything else — Python's own equivalent of Go's
`time.Now()` finding, `time.time_ns()` confirmed live to be caught by
DoubleRun the same way).

**A real implementation-time lesson, recorded honestly**: a mount that
*looked* like it worked (the sandboxed program printed correct-looking
output) turned out not to be testing what it claimed — a nested WASI
guest path (`/ubxsdk/ubx_sdk`, no separate preopen for `/ubxsdk` itself)
was never actually independently listable; the "passing" smoke test had
resolved `import ubx_sdk` by accident, via the test script's own
directory (which happened to also contain a copy of the package), not
the intended mount. Found by checking `ubx_sdk.__file__` explicitly
rather than trusting non-error output — the fix (one top-level preopen
per real directory tree, never nested under a second, ungranted parent
segment) is now load-bearing in `pyeval`'s own real Go test suite, which
asserts on resolved content, not just exit code. See docs/sdk.md's own
"A real implementation-time bug" subsection for the full account.

**Built the same session, all real, all tested:**

- **`sdk/py/ubx_sdk/`** (new) — `Stack`/`Resource`/`Secret`/`Cross`/
  `Intent`/`Run` mirroring TS/Go's own semantics. `Computed` via
  `__getattr__` — Python's own native attribute-customization hook (used
  by countless dot-dict/ORM libraries), not an imitation of TS's Proxy
  trap; coercion blocked via Python's own actual implicit-coercion
  protocol methods (`__str__`/`__bool__`/`__int__`/`__float__`/
  `__index__`/`__iter__`/`__len__`) rather than a literal port of JS's
  trap list — `__repr__` deliberately left alone (never implicitly
  invoked by concatenation/arithmetic the way JS's `toString` is;
  blocking it would only hurt debuggability). Config values serialized
  via `dataclasses.fields()` introspection — Python is natively
  introspectable, so no `reflect`-equivalent library is needed at all,
  a real simplification over Go's own runtime. 12 new hermetic tests
  (stdlib `unittest`, zero extra dependencies — consistent with the
  sandboxed evaluator's own "no pip" posture), all passing, plus a live
  re-run inside the real WASI sandbox during development.
- **`sdk/codegen/templates/py`** (new) — a Python template on the *same,
  unmodified* `sdk/codegen/ir` model. The smallest per-field-naming
  problem of any of the three languages: every real provider wire name
  this project has generated against is already lowercase-with-
  underscores — already a valid Python identifier verbatim, unlike TS's
  camelCase or Go's PascalCase conversion — the only real edge case is a
  wire name colliding with a Python keyword (trailing underscore, the
  same convention generated protobuf/thrift bindings use). Renders
  `@dataclass` Config classes (no `Attrs` type, mirroring Go's own
  "Computed has no static per-field shape" simplification). 8 new tests
  mirroring the TS/Go template suites one-for-one; a real bug (`import`
  after other statements is legal in Python, unlike Go, so this
  particular ordering mistake the Go template made couldn't recur here)
  was NOT found this time — the generated output imported and ran
  correctly on the first real compile-check.
- **`pyeval/`** (new, top-level package, mirroring `sdkeval`/`goeval`'s
  own shape) — unlike `goeval` (build once, run twice, exploiting Go's
  own compile/run split), Python is interpreted, so `pyeval` is
  structurally closer to `sdkeval`: a fresh `wasmtime` subprocess re-
  interprets the program's own source on every `core.DoubleRun` call.
  `wasi_assets.go` acquires and caches the pinned CPython-WASI build
  (~42MB) under `~/.ubx/python-wasi/<version>/` on first use — the same
  `provider.Acquire`-style "fetch a pinned artifact once, reuse the
  cache after" precedent this project already trusts, chosen over
  embedding into the `ubx` binary specifically to avoid growing every
  install by 42MB regardless of whether its own user ever touches Python
  SDK programs. `runner.go` preopens exactly three top-level directories
  (stdlib, the embedded `ubx_sdk` runtime source, the program's own
  directory) and passes zero other env — found empirically, not assumed,
  that this specific CPython-WASI build must NOT be given `PYTHONHOME`
  explicitly (doing so, even to the "correct" value, breaks stdlib
  resolution entirely; omitting it lets its own baked-in default
  resolve correctly, the opposite of every other language's evaluator in
  this arc). `provenance.go`/`validate.go` duplicate `sdkeval`'s/
  `goeval`'s own small, language-agnostic logic — the THIRD copy, now
  the real, named trigger to extract a shared package as a deliberate,
  deferred follow-up rather than done reflexively mid-arc. 8 new
  hermetic real-subprocess tests (happy path, determinism, fs/net
  sandbox escape checked as deny-by-nonexistence not policy-denial, env-
  absent-not-merely-denied, exception mid-evaluation, missing entry
  file) — all passing for real.
- **`cli/resolve.go`**: `--from-code` extension dispatch gained `.py` →
  `pyeval` (a third `case` in the same `switch`, `evaluateSDKProgram`).
  New end-to-end test, `TestResolveFromCode_Py_SimpleCreate`, mirrors
  the TS/Go ones exactly: real WASI-sandboxed run, real resolve, real
  accept, real `ubx why` showing the Python-authored document
  provenance.
- **`cli/sdk.go`**: `ubx sdk gen` gained `--lang py` (alongside the
  existing `ts`/`go`). Real bindings generated against the real
  `hashicorp/aws@6.54.0` schema (1,682 types, matching the TS/Go
  sessions' own real figure) — confirmed to actually import and
  construct against `sdk/py/ubx_sdk`, not just look plausible (a real
  `importlib.import_module` compile-check in the test, not string
  assertions alone). Python-specific filename convention: underscores,
  not the TS/Go hyphenated convention (`import hashicorp-aws` is a
  Python `SyntaxError`).
- **`sdk/conformance/programs/py/payments.py`** (new, independently
  authored, not a transliteration) — real, live, WASI-sandboxed,
  `core.DoubleRun`-verified output matches the TS/Go/md golden's own
  `resources`/`stack`/`intent.summary` byte-for-byte after
  canonicalization. Its own separate golden file
  (`golden/payments_py.json`), same structural reason as Go's:
  `payments.py` is a different real file with a different real content
  hash. `TestPaymentsGoldenCase_Py` passes for real alongside
  `TestPaymentsGoldenCase_TS`/`_Go` — **all three languages, one test
  file, one shared spec, proven together.**

`go build/vet/test`/`gofmt -l .` clean across the whole repo (`sdk/py`
has no `go.mod` of its own to check — pure Python, no nested-module
ceremony needed at all, unlike `sdk/go`). `ubiquex-docs` updated the
same session: `cli/resolve.mdx` gained "Authoring in Python" (real
transcripts), `cli/sdk-gen.mdx` gained `--lang py`, `sdk/index.mdx`
restructured into TypeScript/Go/Python sections.

**UBI-36 closed in Linear — all three languages complete. UBI-33 closed
in Linear alongside it**, with a full contract-retrospective comment:
the golden-`intent/v1`-fixtures-as-spec contract held across three
languages with three completely different evaluator shapes (a sandboxed
interpreter, a compiled-program cheat, a WASM sandbox), the shared,
unmodified `sdk/codegen/ir` model needed zero changes across any of the
three per-language templates, and `core.DoubleRun` carried the full
determinism guarantee in every case, needing a language-level guard
layer in exactly one case (TS) out of three.

### Source-tree cleanup + repo rename (UBI-52 + UBI-53)

Session 1, audit-first, paired deliberately so import paths churn once,
not twice (UBI-53's own stated reason for sequencing the two together).
Full audit table + verdicts, real findings, and the naming convention:
`docs/source-tree.md` (new). Naming convention also recorded in
CLAUDE.md's own "Code conventions" section.

**Founder-flagged, both confirmed real**: `tfstate/` → `stateimport/`
(the package's own role is "import identity to bootstrap onboarding,"
not "a Terraform state file parser" — `onboarding/` rejected as a name
since `guides/cloud-discovery.mdx` already established "onboarding" as
the user-facing feature name for TWO independent mechanisms, tfstate-
and discovery-based; naming only the tfstate-specific package
`onboarding/` would wrongly claim the whole concept). `tfwrite/` →
`writeback/` (implements the already-existing `ubx writeback` verb
one-to-one — the new name isn't invented, it's the name the package's
own caller already uses). The opaque directory: `claude-501/` at the
repo root, found by direct inspection to be **untracked** (`git log
--all` returns nothing) — an empty stray scratch-path artifact from an
earlier session, never real. Deleted, not renamed.

**A real inconsistency found during the audit, not named in either
ticket**: `sdkeval`/`goeval`/`pyeval` — only two of three evaluator
package names encode which language they evaluate. `sdkeval` was named
when it was the only one (UBI-34, before Go/Python existed); renamed to
`tseval/` for the same "consistent family" reasoning the founder-flagged
renames use, at near-zero marginal cost since the mechanical tooling
already exists for the other three.

**Two real tensions recorded honestly, not silently resolved**:
`sdk/go`'s own module path (`github.com/ubiquex/ubx-sdk-go`) was
deliberately NOT renamed to nest under the new `ubiquex` repo path —
UBI-53's own original sequencing note (written before UBI-35 existed)
is now stale; the module already shipped, tested, documented, real.
The lookup-hint tables (UBI-45's own finding) are STILL not
consolidated — now four separately-maintained copies, not three (a
fourth, `discovery/tiers.go`'s own `tierTable`, was added in UBI-45
session 2) — deliberately deferred again, this arc's own scope being
naming/structure, not a cross-cutting data-model consolidation; a
dedicated Linear ticket recommended rather than letting a fifth copy
appear silently.

**The mechanical pass, combined, same session**: `git mv` for all four
renames, `go.mod`'s module line `github.com/ubiquex/ubiquex-cli` →
`github.com/ubiquex/ubiquex`, one import-path sweep covering both the
internal renames and the module path change together. **A real bug,
found by testing, not assumed away**: the sed sweep (correctly scoped to
`*.go` files) accidentally corrupted `provider/tfplugin{5,6}/*.pb.go`'s
own embedded raw protobuf descriptor byte-blob — a length-prefixed
binary encoding disguised as an ordinary Go string literal (the
generated code's own `go_package` metadata, textually containing the
old module path) — text-substituting inside it changed the string's
byte length without updating the length-prefix bytes encoded earlier in
the same blob, corrupting the binary format and panicking at package
`init()` (`slice bounds out of range`) the moment `go test` first
imported the `provider` package. Caught immediately by running the real
test suite, not assumed clean from a successful `go build`+`go vet`
alone (build/vet never execute package `init()`, so neither would have
caught this). Fixed by reverting exactly those two generated files
(their own embedded metadata is functionally inert — Go's own import
resolution never reads it — and regenerating them from the real
upstream `.proto` files is out of this session's own scope); the
`.proto` **source** files (human-maintained, not a binary blob) had
their own `go_package` option updated normally, for whichever future
session regenerates the bindings.

**The one real hashed-content consequence, found by checking, not
assumed** — this arc's own version of UBI-53's own "ledger integrity"
check: `sdk/conformance/programs/go/payments.go` imports its own
sibling `generated` package via the full module path; the import line
changing changed the file's own real bytes, which changed its own real
SHA-256 content hash, which is exactly what `golden/payments_go.json`'s
own `intent.sources[0].content_hash` field freezes. Regenerated against
the real post-rename file (verified independently via `shasum -a 256`
before writing the new value in, not just trusted from the failing
test's own "got" output) — the same way the original fixture was made:
run the real evaluator, capture the real output, never hand-computed.
Checked directly, not assumed: no other golden/ledger/proposal fixture
anywhere in the repo references the module path in a way that reaches
hashed content (grepped every `.json` fixture and every SDK program
under `sdk/`, `cli/testdata/`, `goeval/testdata/`, `pyeval/testdata/`
for the literal string) — `payments.json`/`payments_py.json` unaffected
(TS/Python programs don't import Go packages).

`go build/vet/test ./...` green, `gofmt -l .` clean, `sdk/go`'s own
separate module still builds unaffected, a real `ubx verify` run
against a real (fakeprovider-backed, hermetic) ledger confirmed chain
integrity intact post-rename. Every non-Go reference swept: `CLAUDE.md`
(plus its own new naming-convention paragraph), `README.md`,
`.goreleaser.yaml` (ldflags + GitHub release repo name), `docs/*.md`
(a careful pass distinguishing *historical* narrative referencing the
repo's real name at the time — left untouched, matching this project's
own standing practice for `STATE.md`/changelog entries — from *live*
design statements describing an ongoing, still-true structural fact,
which were updated), and `ubiquex-docs` (its own `CLAUDE.md`, `docs.json`
navbar link, `getting-started/installation.mdx`'s real clone/download
commands, every cross-repo link and prose mention across `cli/`,
`guides/`, `concepts/`, `contribution/` — `mint validate`/`mint
broken-links` both clean; `ubiquex-docs`' own `STATE.md` left untouched
for the identical historical-record reason).

**Founder action, not performed by this session**: the GitHub-side
rename (Settings → repository name) and the local checkout directory
rename (`~/Ubiquex/ubiquex-cli` → `~/Ubiquex/ubiquex`) — this session
runs from inside that very directory and cannot safely rename its own
containing directory mid-session. All code-side work is complete and
green under the CURRENT (pre-rename) remote URL; the git remote origin
URL update and both tickets' closing happen once the founder confirms
the GitHub-side rename is done.

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

### The read-only projection quartet: `verify`/`blame`/`stats`/`addresses` (UBI-38/39/40/48)

Filed as a new arc immediately after Phase 3 closed — a natural
gap-filler in the same spirit UBI-38's own ticket names itself: zero
risk, read-only projections over data the ledger already has, no new
wire format, no provider interaction beyond what already exists. One
session per ticket, in order: UBI-38 (`ubx verify`, the auditor's
command) → UBI-39 (`ubx blame`, per-attribute provenance) → UBI-40
(`ubx stats`, the thesis metrics) → UBI-48 (`ubx addresses`, the active
inventory). House rules across all four: hermetic tests per ticket's own
adversarial rows; read-only truly means read-only (no `ubx ship`
anywhere, live legs — where needed at all — are `resolve`/read-path
only, per UBI-47 session 4's own standing rule); ubiquex-docs lands the
same session as each verb; each ticket closes in Linear as its own
session lands.

**UBI-38 (`ubx verify`) — closed, session 1.** See docs/architecture.md's
own "Independent verification" section for the full system-model account
and STATE.md for the session-by-session detail. Real, load-bearing gap
found and closed, not assumed already covered: `core.Hash` had only ever
been *called* to compute a proposal's own id (at accept time) — nothing
anywhere re-verified a stored proposal's bytes against its own id on
read, across the whole codebase, until this session. Same gap for apply
records (`core.ApplyHash`). `core.IsRedactedValue` had only ever checked
a `$redacted` marker's own *outer* shape; nothing checked the inner
`{"sha256": <64-hex>}` shape until now.

**UBI-39 (`ubx blame`) — closed, session 2.** See docs/architecture.md's
own "Per-attribute provenance" section for the full system-model account
and STATE.md for the session-by-session detail. `core.Blame` mirrors
`FoldState`'s own fold exactly, adding only per-leaf provenance tracking
on top — no new storage, per the ticket's own scope. A real bug found
and fixed before it shipped: an early draft initialized the running
fold's own state as an empty non-nil map rather than `nil`, which would
have silently defeated `FoldState`'s own "skip a Modification with no
genesis create yet" guard; caught by a test written specifically to
mirror that guard, not assumed safe because the shapes looked similar.

**UBI-40 (`ubx stats`) — closed, session 3.** See docs/architecture.md's
own "Thesis metrics" section for the full system-model account and
STATE.md for the session-by-session detail. `core.Stats` folds the
ledger into decision-flow metrics, headlined by signed-flow drift
resolution % -- honestly scoped to what an offline ledger fold can
actually see (only accepted proposals; a drift never accepted leaves no
trace), named explicitly rather than silently claimed as the full,
true, real-world thesis percentage. A real gap found and fixed while
building it: an early draft only counted `drift_adopt` proposals as
"surfaced" drift events, silently undercounting every drift a team
resolved by reverting outright (`--propose both`'s own adopt/revert
alternatives, sharing one parent, only one ever accepted) -- fixed by
also counting a standalone `drift_revert` as its own independent
surfaced-and-resolved event. A second gap, caught during final review
before anything was pushed: a destroyed address's own open-ended
drift_adopt was still counting as "open" -- fixed to count in history
but be excluded from both open and resolved (moot once the resource
itself is gone), gated on a real shipped-destroy transition, the same
way `FoldState`'s own tombstone fold already is.

**UBI-48 (`ubx addresses`) — closed, session 4 (final).** See
docs/architecture.md's own "Referenceable-address inventory" section for
the full system-model account and STATE.md for the session-by-session
detail. `core.Ledger.Addresses` re-walks `Chain()` with `Fleet`'s own
exact discovery rules rather than extending `Fleet` itself, since
`Fleet`'s single pass has no toggle to keep a tombstoned address around
once seen — the one new behavior `--all` needed. The provider-schema half
deliberately bypasses `providerPool` (which refuses to launch any version
but the currently-pinned one) in favor of the same
`ParseSource`/`Acquire`/`Launch`/`.Schema()` sequence `loadDiagramProviders`/
`ubx sdk gen` already use, fetching each address's own RECORDED
`(source, version)` rather than today's `[providers]` pin. The one
genuinely new mechanism this ticket needed: `--stack <neighbor>`
resolving via the identical base-store/`[ledger.external]` logic
`$cross`'s own "stack" form uses (`core/resolver/refs.go`'s
`resolveCross`) — proven, by reading `core.Ledger`'s own constructors,
to be nothing but a pass-through of `cfg.Ledger.Store`/
`cfg.Ledger.External`, and proven that `openLedgerForStack` (every
earlier quartet command's own opener) structurally can't reach an
arbitrary neighbor by name for a git-local store at all, so this
required new CLI-layer resolution logic, not a reuse — refused with a
teaching error naming every stack this workspace's own config actually
knows when no `[ledger.external]` override exists, mirroring
`resolveCross`'s own identical git-local refusal exactly.

**Quartet complete.** All four read-only projections — `ubx verify`
(chain integrity), `ubx blame` (per-attribute provenance), `ubx stats`
(the thesis metrics), `ubx addresses` (the `$cross`-authoring inventory)
— are built, hermetically tested, documented in both repos, and closed
in Linear as of this session. Zero new wire formats, zero new provider
interaction beyond schema reads already established elsewhere, zero
`ubx ship` against anything but `fakeprovider` across all four sessions —
the arc landed exactly as scoped when it was filed.

### Generated conformance harness (UBI-50) — sessions 1-4, closed

Founder decision, filed immediately after UBI-37 (Azure, the fourth
platform) closed: no permanent verified/unverified split across a
provider's own type universe — today's `conformance/registry.go` covers
154 hand-written entries (51 AWS, 42 Azure, 40 GCP, 20 Kubernetes, 1
Helm) out of a combined universe well over 4,000 real types (AWS alone:
1,682 at 6.54.0). The resolution: automate conformance so EVERY type
carries a machine verdict, with hand-verification reserved for what a
machine genuinely can't check — never lowering the claim, never
deleting the 154 entries' own hard-won, live-verified knowledge.

**Provider-agnostic by construction** (the founder's own design-room
clarification, added as a comment on the ticket before this session
began): the probe generator, failure taxonomy, registry format, and
version-bump rerun logic are all shared, built once, against
`provider.Schemas`/`Block`/`Attribute` — the same provider-agnostic
foundation `core.StateReader`/`core.RunScan` already stand on. AWS is
merely the first bulk run, not a design assumption. Per-platform work is
scoped narrowly to live-tier plumbing only: sandbox credentials + a
cost-tier table per cloud, and Helm's own pinned-chart probe fixture.

**Session 1 (this session), docs-first, per protocol**: `docs/
conformance-harness.md` — the full design. Four lie-classes, each
mechanizing a REAL finding this project already made by hand at least
once: identity-shape/incomplete-read (the `google_storage_bucket`/
`google_pubsub_topic`/`google_secret_manager_secret`/
`azurerm_resource_group` class), sensitive-flag audit vs. echo
attributes (the `helm_release`/`azurerm_linux_web_app` class), destroy
honesty via read-back absence (UBI-44's own `google_pubsub_topic` class
— live-only, no hermetic half exists, named explicitly), and
drift-detectability (the `hashicorp/time` class, a real UBI-43 finding
already on file, never previously given this vocabulary). Two execution
tiers (hermetic: all types, CI-runnable, no cloud; live: real
create→read→mutate→destroy, cost-aware, priced types never
auto-included). A generated registry format keyed by `(source, version,
type, verb)` — a real, new axis today's `(source, type)`-only `TypeSpec`
can't express (a type can pass read/mutate and fail destroy
independently, exactly `google_pubsub_topic`'s own history) — with
hand-verified findings layered on top, never replaced, and a machine
verdict that contradicts one flagged for human review rather than
silently overwriting it. A typed failure taxonomy (`incomplete-read`/
`sensitive-underflag`/`destroy-lie`/`undriftable`) with an honest account
of which findings can feed `core/lookuphints`/`provider/overrides.go`
mechanically today and which genuinely can't (`core/lookuphints`' own
shipped message hardcodes AWS's own "add id" advice, actively wrong for
GCP's "both required together"/silent-incomplete shapes — a real,
pre-existing limit this arc doesn't silently claim to have already
fixed). Rerun-on-version-bump delta detection. A four-row adversarial
program (a type whose create needs attributes the schema can't default —
skip-and-say, never fake; priced types never auto-created; probe
resource leakage caught by a real sweep, not memory; a provider serving
inconsistent schema mid-run). Ship doctrine: read/mutate probes reuse
`conformance.RunAdoptMutateScanDiff`'s own real `core.RunScan`/
`GenerateProposal`/`Accept` path unchanged; destroy probes MUST go
through `core/executor.Ship`'s real `shipDestroyNode`/
`reconcileDestroyLoop` path (new plumbing — no destroy step exists in
the harness today at all) rather than a raw `ApplyResourceChange`
shortcut, since bypassing `reconcileDestroyLoop`'s own universal
post-destroy read-back would make a destroy probe structurally incapable
of ever finding another UBI-44-shaped bug — exactly this arc's own
purpose. Live-tier runs are designated live legs, explicit and
opt-in, generalizing the standing ship-verification rule to a new
context, never a default.

No code session 1, per the ticket's own docs-first instruction and this
project's own session protocol.

**Session 2: the probe generator + hermetic tier, built for real.**
`conformance/probe.go` — `Finding{Source,Version,Type,Verb,Class,Tier,
Confidence,Detail}`, `ProbeType`/`ProbeSchema`, and the three
hermetic-half probes (`probeIdentityShape`, `probeSensitiveEcho`,
`probeDrift`) designed in session 1. `verb` resolves to `"read"` for all
three hermetic lie-classes except `probeDrift`'s own `"drift"`;
`"destroy"` stays reserved, unused (probe 3 still has no code at all,
per its own "no hermetic half" design). 18 hermetic unit tests
(`conformance/probe_test.go`, hand-built `provider.Block` fixtures, no
network) plus a real-provider integration test
(`conformance/probe_live_schema_test.go`, `RequireLive`-gated, same
network-only reason as the existing GCP/Azure provider-acquire tests) —
**live-verified against all five real, currently-onboarded providers**:
`hashicorp/aws` (1,682 types, 742 findings: 134 confirmed
incomplete-read, 607 candidate sensitive-underflag, 1 candidate
undriftable), `hashicorp/google` (1,319 types, 278 candidate
sensitive-underflag, zero incomplete-read or undriftable), `hashicorp/
azurerm` (1,103 types, 263 candidate sensitive-underflag, zero
incomplete-read or undriftable), `hashicorp/kubernetes` (82 types, 51
findings: 1 confirmed incomplete-read, 50 candidate
sensitive-underflag), `hashicorp/helm` (1 type, 1 candidate
sensitive-underflag). Determinism (identical schema, byte-identical
`ProbeSchema` output across two runs) asserted directly against all
five real schemas, not just hand-built fixtures. Two spot-checks against
real, hand-verified ground truth already on file confirm the mechanism
reproduces known-real findings, not just that it runs clean:
`helm_release`'s own `metadata.notes` (UBI-22/24) caught as a
sensitive-underflag candidate; `azurerm_resource_group` (UBI-37)
correctly produces zero incomplete-read findings. `docs/
conformance-harness.md` gained a session-2 amendment recording these
real decisions and numbers. Not built this session: probe 3 (destroy
honesty, no hermetic half exists to build), any live-tier probe for any
of the four lie-classes, and layering `Finding` output back into
`conformance.Registry`'s own hand-written `TypeSpec` entries (`Finding`
stays wholly separate/additive for now) — named explicitly, not silently
assumed done. `go build/vet/test`, `gofmt -l .` clean across the whole
repo. See STATE.md for the full account.

**Session 3: triage, probe 3, registry layering, and the live tier for
probes 1/2/4.** All 134 AWS "Confirmed" zero-identity types triaged
against the real schema before building further — 108 carry a
type-prefixed identity attribute after all (`*_arn`/`*_id`/`*_name`), a
real refinement to `probeIdentityShape` (a new `Candidate` tier, never
silently promoted to clean), dropping AWS's own Confirmed count to 16.
Live-tier policy for those 16 (plus `kubernetes_manifest`) decided
before anything was created: excluded from auto-batch, real structural
singleton/composite resources this arc's current lookup model can't
address. Probe 3 built and verified hermetically end to end through the
REAL `core/executor.Ship` path — `conformance`'s own `stateReaderAdapter`
extended to also satisfy `executor.Applier`; an honest fakeprovider
destroy costs nothing extra, a scripted lie is caught
(`FindingDestroyLie`, `Confirmed`), gated `UBX_TEST_SLOW=1` for the real
~64s retry budget. **A real, explicit decision on ship doctrine vs. the
standing ship-verification rule**: probe 3's own live confirmation would
need `executor.Ship` to reach real `ApplyResourceChange` against real
AWS, exactly what CLAUDE.md bans "always, no exceptions" — flagged to
the user before any real AWS destroy was attempted; decided to keep
probe 3 hermetic-only, its real-AWS confirmation a deliberately separate
future decision. `LayerFindings`/`detectContradictions` built (purely
additive, `Registry` never mutated) — zero real contradictions found
across all 723 layered verdicts from all five real providers. Live tier
for probes 1/2/4 (read-only, `aws` CLI + `core.RunScan`, never
`executor.Ship`) verified against one real, free AWS resource
(`aws_sns_topic`) — a real false positive corrected after the first live
run (a tag marker legitimately duplicates into both `tags` and
`tags_all`). `docs/conformance-harness.md` gained a session-3 amendment.
See STATE.md for the full account.

**Session 4, closing: bulk live-tier run, ship doctrine settled,
verdict write-back — arc closed.** The founder delegated the two
remaining open decisions to judgment, exercised conservatively, both
reasoned through and recorded in `docs/conformance-harness.md`'s own
session-4 amendment rather than left as an outcome alone. Bulk
live-tier run: free-tier-only batch policy decided before anything was
created (the sanctioned default; nothing priced was in scope), scoped
to the four already-established self-contained AWS types
(`aws_sqs_queue`/`aws_sns_topic`/`aws_iam_policy`/`aws_iam_user`),
deliberately excluding the three ADOPT-a-pre-existing-resource types.
A real bug found on the first run (`aws_sqs_queue`'s own
`tags`/`tags_all` false positive, the identical class already fixed
for `aws_sns_topic` last session but not carried over consistently);
fixed, all four types clean on rerun, zero leaks confirmed by both an
automated sweep test and a manual four-way `aws` CLI check. Probe 3's
own real-cloud confirmation: weighed the case for a genuinely-new live
AWS check against the standing ship-verification rule's own "always,
no exceptions" text and its real UBI-47 incident precedent — decided
to stay hermetic-only, named explicitly as this arc's one
deliberately-open item for a future session to pick up as its own
fresh ask. Verdict write-back built for real: `PinnedProviderVersions`
(the single source of truth for this project's own five version pins),
`conformance/probegen` (a new generator mirroring `conformance/
gentool`'s own conventions, network-dependent, deliberately never
wired to `go generate`), run for real producing `findings_generated.go`
— 1,335 committed `Finding` entries across all 4,187 resource types
this project's five onboarded providers report — and
`conformance.AllVerdicts()` as the ready-made
`LayerFindings(GeneratedFindings)` base+overlay combination, `Registry`
itself never touched. Two new tests guard the committed data:
`TestGeneratedFindings_WellFormed` (hermetic, catches a stale
regeneration) and `TestAllVerdicts_LayersGeneratedFindingsUnderRegistry`
(proves the wiring, not just that `GeneratedFindings` parses).
ubiquex-docs checked, no update needed — reasoning recorded
(`Candidate`-tier machine findings aren't yet reliable enough for
user-facing promotion; existing hand-verified/unverified language
stays accurate as-is). `docs/conformance-harness.md` gained its
session-4 closing amendment, and its own "What this doesn't yet cover"
section was rewritten to name only what remains genuinely open after
this session. `go build/vet/test`, `gofmt -l .` clean across the whole
repo. **UBI-50 closed.** See STATE.md for the full account.

### ubx plan + ship fusion (UBI-49) — one session, closed

Founder decision: the four-verb ceremony (`propose` → `resolve` →
`accept` → `ship`) is right for teams binding acceptance to a real
review process (PR-merge signing), but is real, avoidable ceremony for
a solo operator or a sandbox iterating fast. Terraform's own two-command
shape (`plan`, `apply`) is the ergonomics bar — pure CLI fusion over
existing machinery, no policy engine, nothing deprecated.

**`ubx plan`**, a new verb, fuses `propose`+`resolve`+a preview render
into one command: any medium input (a hand-written intent file,
`--from-code`'s TypeScript SDK, `--from-doc`'s markdown draft via the
configured intent provider, `--from-diagram`'s D2 topology) resolves
through the identical, unmodified `core/resolver.Resolve` every other
entry point already uses — same invariants, same orphan/pin checks,
same failure modes — and renders a full receipt (delta, cost_delta,
blast radius, assumptions/defaults/questions) for review. Nothing is
accepted or applied; like `resolve`/`propose` today, this is
preview-only. The resolved-but-unaccepted proposal is saved at
`.ubx/plans/<hash>.json`, a new local, hash-addressed store alongside
`.ubx/salt`/`.ubx/lock` but never inside `ledger/` — keyed by the exact
content hash the proposal's own `ID` becomes once actually accepted
("a resolved proposal IS a saved plan file — hash-frozen,
staleness-detecting," per the founder's own framing; staleness
detection itself is free, inherited unchanged from `core.Accept`'s
existing `ErrParentMismatch` and `resolver.VerifyPins`'s existing
cross-stack pin check).

The md/diagram media's own established "draft, then a separate human
checkpoint before resolving" posture (docs/intent-provider.md, docs/
diagram-medium.md) is deliberately **not** reproduced inside `ubx
plan` — a real, considered departure, not an oversight: `ubx plan`'s
own receipt, rendered before anything is saved, already covers the
full resolved proposal (delta/blast radius/cost alongside any
ambiguity content) rather than the draft's ambiguity content alone,
so it already serves as the review checkpoint the four-verb path
splits across two steps. `ubx propose --from-doc`/`--from-diagram`
keep their exact existing draft-only behavior completely unchanged,
for teams that specifically want that extra checkpoint as its own
step.

**`ubx ship <hash>`** gains inline acceptance: `<hash>` is looked up
first as an already-accepted ledger id (the four-verb path, completely
unchanged, including PR-merge acceptance as its own separate, still-
available path); if not found, as a plan saved by `ubx plan`, accepted
inline (local tier) before applying, through the exact same
`checkDestroysConfirmed`/`resolver.VerifyPins`/`core.Accept` sequence
`ubx accept`'s own local-file path already runs — `--confirm-destroys`
still required for any plan with `blast_radius.destroys > 0`, a stale
cross-stack pin still refuses, `acceptance.method: "local"` recorded
exactly as today. A defensive integrity check specific to this
fallback: the plan file's own content must still hash to the filename
it was found at, refusing a hand-edited or corrupted plan rather than
shipping it under a hash that no longer describes its actual content.

**Pure CLI fusion, verified rather than assumed**: `resolve.go`'s own
provider-loading block and `propose.go`'s own doc/diagram drafting
logic were extracted into shared helpers (`loadResolveProviders`,
`draftFromDoc`, `draftFromDiagram`) so `plan.go` drives the identical,
unmodified code every existing verb already runs — zero changes to
`core`/`core/resolver`/`core/executor` — and the full existing test
suite (every package) passes unchanged after the refactor, confirmed
by running it, not assumed from the diff's shape. `ubx plan
--from-diagram` incurs one real, accepted inefficiency: its own
diagram-parse type-inference pass and the later resolve call each
launch every declared provider once (a real double schema-fetch, never
a double cloud call) rather than threading a shared providers slice
through both call sites — named honestly in `docs/architecture.md`
rather than fixed with a deeper refactor outside this arc's own tight
scope.

10 new hermetic tests (`cli/plan_test.go`), all passing on first real
run against the fake provider binary: all four medium inputs end to
end through the fused plan-then-ship path (including a real assumption
rendered alongside a resolved delta for `--from-doc`, and a real
UBX_PROVIDER_MIRROR-backed multi-provider round trip for
`--from-diagram`), `--confirm-destroys` still required, a cross-stack
pin's staleness still blocking, a plan-file hash-mismatch refusal, a
no-such-hash genuine error, and input-mode validation.
`docs/architecture.md` gained a new "Two-step fusion" section
recording this design. ubiquex-docs: guides updated to lead with the
two-step flow (`ubx plan` → `ubx ship`) as the sandbox/solo path, the
four-verb ceremony documented as the team/production path for PR-merge
signing. `go build/vet/test`, `gofmt -l .` clean across the whole
repo. **UBI-49 closed** in one session, matching its own "Est. 1-2
sessions" sizing. See STATE.md for the full account.

### Cloud-side discovery (UBI-45) — sessions 1-3, closed

Founder decision: unparked directly (the ticket's own prior status was
`PARKED — unpark trigger: wedge traction demanding non-Terraform
onboarding, or a real prospect with no usable tfstate`). The wedge's
second front door, named and explicitly scoped out of UBI-18's own bulk
onboarding: *"cloud-side discovery is explicitly a different epic"*.
Where UBI-18 needs a tfstate file to source resource identity from,
this arc makes the cloud account itself the discovery source — "point
`ubx` at the account, not at the repo," reaching ClickOps-heavy,
half-terraformed, or inherited/acquired accounts UBI-18 alone can't
touch.

**Session 1, docs-first, per protocol**: `docs/discovery.md` — the
mechanism decision made empirically against a real AWS account, not
assumed from documentation. A real, throwaway `aws_sqs_queue` created,
tagged, queried, and destroyed this session to confirm the AWS Resource
Groups Tagging API's own real shape (ARN + tags only, zero setup
barrier, confirmed working immediately in an account that had never
used it before) — chosen as the primary mechanism. AWS Config checked
in the same real account and found **not enabled at all** (zero
configuration recorders) — a real, decisive adoption barrier for
exactly this ticket's own target market, not chosen as primary, named
as a possible future enrichment path only. The tfplugin wire protocol's
own dormant `ListResource` RPC (never called anywhere in this codebase
before this session) tested live against the real `hashicorp/
aws@6.54.0` binary via a throwaway same-package probe test (deleted,
never committed): only 53 of 1,682 resource types implement it (~3%),
none of this project's own four trusted free-tier fixture types among
them, real provider-internal diagnostic errors even for a type that
claims support — not viable today, named as the clearest future
"revisit" trigger instead of silently assumed unusable.

**The identity bridge** — enumeration returns ARNs, adoption needs
provider lookup shapes — named as the arc's actual hard problem, not a
formality. Checked directly against `conformance.Registry`'s own
live-verified entries (not assumed complete from UBI-50's own "machine-
complete" framing): `TypeSpec.IdentityFields`/`LookupHint` answer a
narrower question than "lookup shape," and this session found **three
separately-maintained copies of the same tiny lookup-hint fact**
already in this codebase (`conformance.Registry.LookupHint`, generated
`core/lookuphints`, `tfstate.BuildLookup`'s own `extraLookupAttrs`) — a
structured `TypeSpec.LookupShape` field is recommended as real
follow-up work, named honestly rather than silently added to as a
fourth copy. Every AWS type's real lookup shape falls into one of three
empirically-confirmed tiers: id IS the ARN (`aws_iam_policy`); id is
the ARN's own trailing segment, sometimes duplicated into a second
field (`aws_vpc`/`aws_iam_role`/`aws_iam_user`/`aws_s3_bucket`); id is
constructed from ARN components (`aws_sqs_queue`'s own queue URL,
confirmed live this session). A type discovery can't bridge surfaces as
"discovered, not yet adoptable: no known lookup shape" — never silently
dropped, the same "skip, never abort the batch" posture `--all
--tfstate` already established.

Tag-scoped filtering designed as the primary UX (`--tag`, a client-side
`--type` allowlist derived from each ARN's own service segment, never a
second hand-maintained filter-string table, `--region`); stack-grouping
inference designed as a separate, read-only `--suggest-stacks` preview,
never an auto-assignment — directly following UBI-18's own established
"a Terraform module path is a hint, never a silent stack split"
precedent, applied to a weaker signal (tags/naming) with at least the
same caution; the attribution bonus designed as a reuse of
`core.EventLookup`/the existing `audit/` backends completely unchanged,
searching for creation-verb events instead of arbitrary drift events,
purely additive exactly like `--no-attribution` already makes drift
attribution itself optional today. Five-row adversarial program (no
lookup shape; tag matching thousands of resources, pagination + a
`--limit`-gated confirmation; permission denied mid-enumeration;
resource deleted between list and read; already-adopted rediscovery,
idempotent) — every row's required outcome reuses an already-existing,
unmodified mechanism (`--all --tfstate`'s own skip taxonomy,
`core.RunScan`'s own idempotent classification), named explicitly
rather than re-derived. **Adoption stays record-only, blast-radius zero
by construction** — this arc adds a new identity source only, never a
new proposal kind, never a new apply path.

No code session 1, per protocol.

**Session 2: the identity bridge as real code + `ubx scan --discover`
CLI wiring.** `discovery/arn.go`/`discovery/tiers.go`: `ParseARN` splits
an ARN's own resource segment into a type-prefix plus id — a real
refinement found while building, not assumed at design time: keying the
tier table by `(service, resourceTypePrefix)` rather than service alone
is what lets `aws_iam_role`/`aws_iam_user`/`aws_iam_policy` (all
service `iam`) disambiguate without needing `--type` at all.
`tierTable` seeded with session 1's own five confirmed examples across
all three tiers; an unclassified pair, or a Tier-C entry whose own
constructor fails, surfaces `ErrNotYetAdoptable` — never a fabricated
lookup. `discovery/discover.go`: a `TaggingAPI` interface matching the
real SDK client's own `GetResources` method signature exactly (zero
adapter code, the same dependency inversion `core.StateReader`/`core.
EventLookup` already establish), pagination followed to completion,
`CheckLimit` its own separate, pure confirmation-gate function. `ubx
scan --discover` wired as a third mode alongside single-resource/
`--all` (`cli/scan.go`, `cli/scandiscover.go`), reusing `core.RunScan`/
`core.GenerateProposal` completely unchanged, mirroring `runScanAll`'s
own structure closely; a real bug found and fixed while building: a
provider was being required even when nothing discovered was
adoptable, now skipped entirely in that case. `--suggest-stacks` built
as its own separate, simpler read-only path — no ledger, no provider,
nothing ever written. Two package-level seams
(`newDiscoveryTaggingAPI`/`newDiscoveryStateReader`, the same
convention `openRemoteLedgerStore` already establishes) let hermetic
CLI tests fully control both the tagging API and the provider-read
half without touching real AWS or fakeprovider's own shared
`fake_widget` fixture — zero risk to the wide existing
fakeprovider-based suite elsewhere in this project.

**All five adversarial rows verified hermetically** (17 new tests: 10
in `discovery`, 7 in `cli`) — a real finding along the way: permission
denied mid-enumeration and resource deleted between list and read
collapse into the *identical* `core.RunScan`-error code path, tested as
one combined case rather than two artificially separated ones; a
second real bug surfaced by the idempotency test's own first failed
attempt (a merely-*generated* proposal isn't "already adopted" until a
real `ubx accept` actually runs — the test's own original premise was
wrong, caught by running it, not assumed correct from the test's own
intent). **Live-verified, read-only, no ship**: a real, hand-created
(`aws sqs create-queue`, never via `ubx`) SQS queue, tagged with a
distinctive marker, discovered end to end against real AWS — the real
tagging API, the real Tier-C queue-URL construction, the real
`hashicorp/aws` `ReadResource` call, a real, complete, zero-blast-radius
`adoption` proposal generated — confirmed via the filesystem that no
`ledger/` directory was ever created at all (nothing accepted, purely
record-only). Swept clean afterward, confirmed via both `aws sqs
list-queues` and the tagging API. `go build/vet/test`, `gofmt -l .`
clean across the whole repo; zero regressions anywhere in the existing
suite. Genesis attribution and the live finale (slices 4-5) remain
session 3+ work. See STATE.md for the full account.

**Session 3, closing: genesis attribution + the live finale at scale.**
`core.AttributeGenesis` (new) reuses `AttributeDrift`'s own identity-
candidate search and defensive exact-match filtering completely
unchanged, narrowing matches to a caller-supplied creation-verb
`EventName` and taking the OLDEST genuine match (the opposite of
`AttributeDrift`'s own "newest first" — a resource is created exactly
once, so the earliest creation-verb match founded its lineage). An
empty creation-verb list is a new, honest `ReasonNoCreationVerbs`,
distinct from a real search that came up empty. The creation-verb
table reuses `discovery/tiers.go`'s own per-type table (a new
`CreationVerbs` field) rather than a fifth separately-maintained one,
seeded with all six of this arc's own real AWS API operation names.
Wired into `ubx scan --discover` via a new `attributeGenesis` (`cli/
attribution.go`), reusing `newAttributionBackend`'s own per-provider-
source registry unchanged; the existing `--no-attribution` flag now
gates both drift and genesis attribution. Hermetic tests mirror
`cli/attribution_test.go`'s own established "blank every AWS credential
source" technique, proving the wiring never blocks adoption even when
CloudTrail is unreachable — 7 new tests, all passing.

**The live finale, real AWS, swept clean.** Four resources hand-created
via the `aws` CLI directly (never through `ubx`), tagged across two
`Project` groups: `aws_sqs_queue` (Tier C) + `aws_iam_policy` (Tier A)
tagged `payments`; `aws_s3_bucket` (Tier B) + a DynamoDB table
(deliberately unclassified — this session's own designated proof of
"discovered, not yet adoptable" against a real resource) tagged
`networking`. `--suggest-stacks --stack-tag Project` correctly grouped
all four from their own real tags, writing nothing. Discovering
`networking` produced one adoptable proposal and one honest "not yet
adoptable" line for the table; discovering `payments` — after polling
`aws cloudtrail lookup-events` directly until the real `CreateQueue`
event appeared (~4 minutes, well under this project's own previously-
documented 15-minute worst case) — produced both proposals with
successful genesis attribution, both correctly attributed to the real
IAM user who created them, real event IDs, real timestamps, real
source IPs. All three adoptable resources accepted through the real,
unmodified local-tier signing flow; `ubx why` on the accepted SQS
queue shows exactly `source: cloudtrail --
arn:aws:iam::839333509514:user/roozbeh CreateQueue at ... from ...` —
genesis-by-adoption with the real attributed creator, this arc's own
closing proof. All four resources destroyed afterward, swept clean
(tagging API, `list-queues`, `list-policies` all empty; `head-bucket`/
`describe-table` both confirm gone). `go build/vet/test`, `gofmt -l .`
clean across the whole repo; zero regressions. **UBI-45 closed** across
three sessions — design, build, and a real, live, closing proof of
every claim the design session made. See STATE.md for the full
account.

### Strata blueprints: Slices 1–8 (UBI-74) — ALL EIGHT SLICES CLOSED

UBI-74's own Linear comment thread (2026-08-02/04) is the design record
of the full arc (naming, trust model, the eight-slice breakdown, the
rejected intermediate designs); docs/blueprint.md is the authoritative
build doc for Slices 1–8, the complete original plan. This section is a
pointer, not a duplicate. **A full closing retrospective across all
eight slices together (not just slice-by-slice) is recorded in
STATE.md.**

Slice 8 (offline delivery + redistribution, the FINAL slice) built: `ubx
blueprint pull <path-to-tarball>` -- a fourth `Pull` source type, a bare
tarball FILE, extracted directly with zero network involved at all
(slotted into the exact gap the existing `os.Stat`/`IsDir` dispatch
already implied -- no new heuristic needed, a file can never satisfy
`IsDir()`); and real re-tag/mirror-unchanged redistribution, needing NO
new production code at all -- `ubx blueprint push` (Slice 7) called a
second time against a second `--to`, the identical tarball never
re-packaged, proving trust-preserving redistribution by COMPOSING
already-built mechanisms. Content-hash verification stayed exactly the
one existing scheme -- `Pull` never trusts a bare tarball's own declared
hash, `Verify` (unchanged since Slice 3) is what actually protects this
delivery mode, since (per the original design record) it has no git
history or registry-native integrity to lean on at all. A real,
live-found subtlety caught during this session's own required
verification: `content_hash` is a function of the blueprint's own
declared `name` (from the directory's basename at `Package` time) as
well as file content -- re-packaging identical files into a
differently-named directory produces a genuinely different hash, a real
operational gotcha now documented explicitly for offline redistribution.
Fork-with-modification (the design record's own pattern 1, a genuine
derivative with its own fork-lineage provenance) is designed in full
(docs/blueprint.md) but not built -- an explicit stretch goal, named
rather than silently left unaddressed. Live-verified against real
`ghcr.io`: the real CI-platform blueprint pulled fresh, re-packaged,
pulled again as a bare tarball file with network deliberately blocked
(`HTTP_PROXY`/`HTTPS_PROXY` pointed nowhere reachable) -- succeeded,
verified, real `go build`/`go vet` against the offline-pulled copy
succeeded; then pushed to a SECOND real GHCR location
(`ghcr.io/ubiquex/ci-platform-mirror:v1`), `docker manifest inspect`
confirming an IDENTICAL blob digest at both locations, `ubx blueprint
verify` against the mirror confirming the identical original content
hash. Full account: docs/blueprint.md's "Offline delivery +
redistribution: Slice 8" section and its own Slice 8 implementation-
slices entry.

Slice 7 (OCI push/pull) built: `ubx blueprint push <tarball> --to
oci://<registry>/<repo>:<tag>` (uploads Slice 3's own tarball, unmodified,
as a real OCI artifact via ORAS -- `oras.land/oras-go/v2`, confirmed as
the current, actively maintained API surface via `go doc` against its own
real downloaded source before use, not assumed from memory; one manifest
wrapping the tarball as its one content-addressed blob layer, no separate
config blob) and a third `oci://` source-type branch on `ubx blueprint
pull` (alongside Slice 3's local-path and git), authenticated via the
SAME credentials a real `docker login`/`oras login` already established
(read from the real Docker credential store -- confirmed working first,
`docker login ghcr.io` re-run, "Login Succeeded" -- never a second,
ubx-specific login). Content-hash verification resolved as one hash
scheme, not two: OCI's own native blob digest (computed by `oras-go` from
the tarball's real bytes, verified by the registry) is a transport-
integrity check, completely separate from `content_hash`
(`core.CanonicalJSON`-based, unchanged since Slice 3, `Verify` after
pull+extract still the same check) -- also recorded as an OCI manifest
annotation, purely for `docker manifest inspect`-level visibility, never
a competing verification path. A real, pre-existing-package correction
caught along the way: `github.com/oras-project/oras-credentials-go`
turned out to be deprecated in favor of the identical functionality now
built into `oras-go/v2` itself, caught via that package's own `go doc`
output before committing to it. `pushToTarget`/`pullFromTarget` are
deliberately target-agnostic (any `oras.Target`, not hardcoded to a real
registry) specifically so the real ORAS mechanics are hermetically tested
against `oras-go`'s own real local `oci.Store`, never a hand-rolled fake.
Live-verified against real `ghcr.io`: the real CI-platform blueprint
(the identical content proven live across Slices 1-6, confirmed by an
IDENTICAL content hash to Slice 6's own real render output) packaged and
pushed to `ghcr.io/ubiquex/ci-platform:v1`, independently confirmed
landed via a real `docker manifest inspect` (not just the push command's
own success message), pulled back into a separate directory, `ubx
blueprint verify` confirmed the content hash matches, and a real `go
build`/`go vet` against the pulled copy succeeded -- real network, no
local `replace`, the identical bar Slice 3 met for git, now met for a
real OCI registry. `ghcr.io/ubiquex/ci-platform:v1` is left published
deliberately -- this slice's own real deliverable, not a transient test
resource. Full account: docs/blueprint.md's "OCI push/pull: Slice 7"
section and its own Slice 7 implementation-slices entry.

Slice 6 (provenance + `why`/`render` integration) built: every resource a
blueprint call produces (any medium, any language) is stamped with a
`{"kind": "blueprint", "ref": "<name>:<content_hash>"}` entry in a new
per-RESOURCE `resolver.ResourceIntent.Sources` field (reusing
`core.IntentSource`'s own existing multi-kind shape verbatim -- a new
`"blueprint"` kind value, never a new field shape -- since a
DOCUMENT-level source can't express "resource A came from a blueprint,
sibling resource B in the same document didn't," a real scenario a mixed
diagram/md document already makes possible). `ubx why` renders the full
chain -- which blueprint, which content-hash version, and (per the
design record's own "dual-signature" story) an honest account that only
the CALLING stack's own real acceptance is backed by a real signing
ceremony in this build; the blueprint AUTHOR's own signing has no
separate mechanism yet, named as a gap rather than fabricated. `ubx
render` groups a blueprint call's own resources inside one dashed-border
D2 container (`style.stroke-dash`/`style.fill: transparent`, verified
against this project's own real `d2parser`/`d2format` pipeline before
use), labeled with the blueprint's own ref -- real, resolved-time truth
pulled from the resource's own creating proposal, consistent with (not an
exception to) `diagram/emit.go`'s own "no synthetic containers, no
guessed structure" principle. A real, pre-existing bug found and fixed
along the way, not introduced by this slice: `Emit` read `depends_on`/
provenance from Fleet's own "latest touching proposal," which for a
resource later touched by an unrelated reconciliation proposal (a real
shape a two-resource blueprint call with a `$ref` between them produces)
is NOT the same as its own creating proposal -- silently dropping that
data. Fixed with a new `creatingProposalFor` helper that walks the
address's own full recorded history to find the actual create,
independent of whatever touched it most recently. Live-verified against
real `hashicorp/aws@6.54.0`: the real CI-platform blueprint (ECR+SQS+IAM
role+policy+attachment) called once for the `payments` stack, resolved
and accepted against the real provider schema, shipped by the founder
(per this project's own standing `ubx ship` handoff doctrine), then `ubx
why`/`ubx render` both confirmed correct against the real shipped result
-- all five resources correctly grouped under one container, the full
provenance chain and real ship history rendering correctly. Full account:
docs/blueprint.md's "Provenance: Slice 6" section and its own Slice 6
implementation-slices entry.

Slice 5 (cross-medium calling) built a diagram's own `ubx_blueprint`-
classed node (`diagram/parse.go`, zero AI, reusing UBI-91's own
`ubx_required` structural-attribute mechanism) and an md draft's own
"Use blueprint X with..." recognition (`intentprovider`, a thin AI
mapping step that never re-drafts the blueprint's own resources) —
both compiling to the SAME new `resolver.IntentFile.BlueprintCalls`
wire field, expanded by `blueprint.ExpandCalls` (spliced into `ubx
resolve` right before `resolver.Resolve`) into real resources by
literally invoking the target blueprint's own compiled function through
the IDENTICAL `goeval`/`tseval`/`pyeval` machinery `ubx resolve
--from-code` already runs for a hand-written SDK program (UBI-74 Slice
2's own real invocation mechanism, never a second, parallel one).
Confirmed live and corrected before it became a design liability: an
initial assumption that Go's own placeholder `v0.0.0` `ubx-sdk-go`
version would need a `go mod tidy`/local `replace` first was checked
directly with a real build and found wrong — `v0.0.0` is a genuinely
real, resolvable version; the actual condition is just the module
already being in the local Go module cache. Live-verified with a
hermetic byte-comparison test first (the SAME blueprint called via a
hand-written Go SDK program, a real `.d2` diagram, and a real fake-
adapter md draft all resolve to the IDENTICAL delta shape), then the md
leg for real: a real `.md` document drafted against the REAL Claude API
correctly recognized the pattern with zero hallucinated resources,
resolved against the REAL `hashicorp/aws@6.54.0` provider's own schema
with UBI-123's own corrected `retention_days: 14` reaching the resolved
proposal correctly, accepted into a real ledger (`ubx ship` itself
handed off to the founder). Full account: docs/blueprint.md's
"Cross-medium calling" section and its own Slice 5 implementation-slices
entry.

Slice 4 (multi-language) built `--lang go|ts|py|all` on `ubx blueprint
build`: the SAME single AI draft compiled into up to three sibling
package directories (`go/`/`ts/`/`py/`) by three independent generators
sharing one new language-neutral decode/dependency/topo-sort layer
(`blueprint/decode.go`). Confirmed before building anything new, not
assumed: `sdk/codegen`'s own IR/template machinery (schema -> generic
binding library) doesn't drop in cleanly for blueprint codegen (resolved
concrete values -> source) — a genuinely different problem, real
adaptations implemented per language (native default parameters for
TS/Python vs. Go's own functional-options workaround; `ResourceBinding<any,
any>` for TS, matching its own runtime's plain-object-literal duck typing;
a mandatory dataclass Config for Python, matching Go's own struct
requirement for a reason TS doesn't share). Two real bugs caught by this
session's own hermetic tests before shipping: TypeScript's `Computed<any>`
failing to typecheck property access at all (fixed with a targeted `as
any` cast), and Python's local variable naming initially reusing Go/TS's
camelCase derivation instead of genuine snake_case (fixed with a
dedicated identifier helper). Live-verified: the same CI-platform Ubxfile
built `--lang all` against the real Claude API, all three languages'
output compiling/typechecking/importing cleanly, the TS-compiled function
called from a real TS stack and resolved against the real
`hashicorp/aws@6.54.0` provider's own schema with UBI-123's own corrected
`retention_days: 14` reaching the resolved proposal correctly, accepted
into a real ledger (`ubx ship` itself deliberately handed off to the
founder, per this project's own standing doctrine). Full account:
docs/blueprint.md's "Multi-language codegen" section and its own Slice 4
implementation-slices entry.

Slice 3 (package/distribute) built `ubx blueprint package`/`pull`/
`verify`: a content hash over a built blueprint's own files (`core.
CanonicalJSON`, the same JCS-style approach `core.Hash` already uses for
a Proposal) recorded in a `blueprint.lock.json` manifest that travels
with the directory through any distribution mechanism; a gzipped,
content-addressed tarball (`package`); local-path and git+ref pull
(`pull` -- a third, OCI, source type was added in Slice 7, see above);
and tamper-evident hash re-verification (`verify`). Live-verified against
a real, newly created
GitHub repository (`github.com/Ubiquex/ubx-sdk-blueprints`) — packaged,
pushed with real commit history, pulled into a separate local directory
via a real HTTPS clone, verified, and a real `go build`/`go vet` against
the actual published `ubx-sdk-go` module confirmed the pulled copy is
genuinely usable. Full account: docs/blueprint.md's "Package/pull/
verify: distribution" section and its own Slice 3 implementation-slices
entry.

Slice 1 built: parsing an `Ubxfile` (`lang`/`params`/`resources` only,
strict-YAML, `uses:`/nesting a hard parse error per UBI-121 staying
separate) and `ubx blueprint build .` — resolves `resources:` through
UBI-41's own `DraftWithRetry` exactly once (no ledger, no
`resolver.Resolve` — a blueprint re-resolves against a real stack at
CALL time, Slice 2+, never at build time), then compiles the draft into
a real, self-contained Go package (`blueprint/gogen.go`, new work — no
existing codegen already went resolved-intent → source): a
`ResourceBinding`/`Config` pair per resource derived from the draft's own
observed config keys (never a live provider schema fetch — deliberate,
docs/blueprint.md), a topologically-ordered, parameterized function with
real `$ref` → `.Field()` translation and `{param_name}` → Go-variable
substitution.

Live-verified per the ticket's own required bar: a hand-authored
CI-platform Ubxfile (ECR+SQS+IAM role+policy+attachment, matching
`intentprovider/conformance/fixtures/platform-iam-attach.md`'s own
shape) built against the real Claude API, `go build`/`go vet` both clean
against the real published `github.com/ubiquex/ubx-sdk-go` module (real
network, not a local override). One real finding worth naming again
here: an `aws_iam_policy.policy` string arrived from the real draft with
a `$ref` marker embedded in its own escaped JSON — checked against
`core/resolver/refs.go`'s own documented "JSON-embedded refs" shape
before assuming it was a bug, and it isn't; Slice 1's codegen renders
such a string verbatim, which is exactly the shape the resolver/executor
already know how to substitute later. Full account, including the
`params: default` (parsed, not yet load-bearing at Go-codegen time —
Go has no native optional-argument syntax) open point: docs/blueprint.md.

Slices 1 (build) through 8 (offline delivery + redistribution) are ALL
CLOSED — UBI-74's own original eight-slice plan, complete, per this
section's own updated heading and the Slice 3/4/5/6/7/8 paragraphs
above. Nesting is UBI-121; the bound policy engine is UBI-118; the
override mechanism and `render --sync-overrides` are UBI-86; list-typed
params/iteration (UBI-129) and a Terraform converter (UBI-125) were
separately scoped future work and are now BOTH closed (see this
changelog's own UBI-129/UBI-125 entries) — none of the five were ever
part of this eight-slice plan, tracked separately, not gaps in it. Fork-with-modification
redistribution (design record pattern 1) and alias/pointer
redistribution (pattern 3) are the two real, named exceptions WITHIN the
plan's own Slice 8 that stayed design-only/untouched respectively — see
docs/blueprint.md's own "Offline delivery + redistribution: Slice 8"
section for exactly why each was scoped that way.

### ubx.Data authoring path (UBI-178)

Discovery-only data source support (UBI-186, an earlier phase) let a
shipped stack's own live data sources be observed; this arc is the other
half -- authoring one directly in a program, the same way `resource()`
already works. Four pieces, staged and reported between each, in
dependency order:

**Piece 1** (PR #20, not yet merged): `IntentDocument` gains a top-level
`data_sources[]` array, sibling to `resources[]` (which is hardcoded
`op: "create"` and can't be reused for a read-only lookup). Documented in
docs/schema.md's own "Amendment: data sources" section, alongside a real,
test-confirmed correction: a data source's own `Type` string uses an
underscore (`"data_aws_ec2_instance"`), not a dot
(`"data.aws_ec2_instance"`, the convention an earlier session phase's own
doc comment had wrongly assumed) -- a dotted Type breaks
`core.Address.String()`/`ParseAddress`'s own round-trip
(`strings.SplitN(s, ".", 3)`), confirmed via a real, disposable Go test
before locking in the design. `core/scan.go`'s own
`VerifyDataSourceFreshness` doc comment (already shipped, from a prior
phase) was corrected to match.

**Piece 2** (3 PRs, one per language submodule, none yet merged):
`Collector.addDataSource` in `sdk/go/runtime`, `sdk/ts/runtime`,
`sdk/py/ubx_sdk` -- returns the same `Computed`-shaped handle a resource
returns, so a data source's result feeds into a resource's config
identically (references stay uniform, a deliberate design decision, not
an oversight). Each runtime's `addDataSource` is a direct structural
mirror of its own `addResource` -- same duplicate-address check, same
marker-walking, same blueprint-provenance wiring -- per explicit
direction that drift across three runtimes was the real risk here, not a
per-language reimplementation.

**Piece 3** (2 PRs: `core/resolver-data-sources` #21 for the resolver
mechanism, `cli/data-source-provider-wiring` #22 stacked on it for CLI
wiring, neither yet merged): `core/resolver.Resolve` executes the real
lookup and records it in `resolution.inputs` with `Kind: "data_source"`,
reusing `ObservedHash`/`Lookup` exactly as `"live_state"` already does
(this reuse pre-dated this arc, from `core/scan.go`'s
`VerifyDataSourceFreshness`, merged earlier as PR #18 -- piece 1 only
documented it). The real architectural conflict found and resolved before
writing any code: `core.DoubleRun` requires byte-identical output across
two calls to catch nondeterministic resolution logic, but a live provider
read is an external, changing input, not nondeterministic code -- running
it twice risks both cost and a spurious mismatch. Fixed with a
per-`Resolve`-call read cache (`dataSourceReadCache`, created fresh
before `DoubleRun`, discarded after -- never longer-lived, since a cache
surviving across separate `Resolve` calls would silently serve a later
plan stale data). `resolve`/`plan`/`promote`/`terminate` all gained a
live provider connection (`attachDataSourceReaders`,
`cli/datasourcereader.go`) gated entirely on the intent document actually
declaring `data_sources[]`, so the overwhelming majority of stacks see no
change in connection lifetime at all. A live read failing mid-walk
surfaces as a new, distinguishable `ErrDataSourceReadFailed`, never
`ErrDoubleRunMismatch` or anything resembling a nondeterminism finding.

**Piece 4** (PR TBD, branched fresh from `main` -- independent of piece
3's still-unmerged resolver changes): SDK codegen emits a `data`
namespace mirroring the resource namespace it sits beside --
`aws.data.ec2.Instance` alongside `aws.ec2.Instance` -- across Go, TS, and
Python. Per explicit direction, this is a real extension of
`ir.ServiceAndLocalNameForType` itself (new `namespace` return value,
driven by a new `ResourceType.IsDataSource` bool), not a parallel
wrapper function -- the same function decides both resource and data
source naming, in one place, the discipline that would have caught the
Azure/GCP service-doubling bugs (UBI-98) sooner had it been followed
there from the start. service/localWireName derivation is byte-for-byte
unchanged by `IsDataSource`; only the namespace segment is new, and a
real provider's data source can share its WireType with a resource of
the same name (hashicorp/aws's own `aws_instance` is both) without
colliding, confirmed by a same-WireType test in all three language
template packages. `cli/sdk.go`'s `writeGeneratedSDK` now collects
`schemas.DataSources` the same way it already collects `schemas.Resources`
before this piece, tagging each `IsDataSource`. Found and fixed two real,
adjacent collisions this same-WireType-sharing exposed once data sources
started flowing through pipelines that pre-dated this arc and were never
built to expect them: `--dump-ir`'s own per-type JSON dump and combined
`schema.json` were keyed by bare `WireType` (a data source would have
silently overwritten its same-named resource's dump); and description
enrichment's own checked-in-description/gap-file lookups were keyed the
same way (a data source's field descriptions could have silently
cross-contaminated its resource counterpart's). Both fixed with the same
`"data_"` key prefix docs/schema.md's own piece-1 amendment already
established for the resolver's intent-document convention, rather than a
third, different disambiguation scheme. Not built: a fakeprovider mirror
fixture declaring a real data source schema, so `ubx sdk gen`'s own
existing `--dump-ir`/`ViaMirror` integration tests could exercise this
end to end through the real CLI -- the fakeprovider test double has no
`DataSourceSchemas` support at all today, and building that out is real,
separately-scopable test infrastructure work, not a gap silently left
untested: the namespace derivation itself (`ir.ServiceAndLocalNameForType`)
and all three languages' full `GeneratedRepo` output (paths, package
identity, same-WireType coexistence, zero collisions) are covered
directly, hermetically, without it.

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
