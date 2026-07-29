# Cloud-side discovery — tag/list-based adoption without tfstate (UBI-45)

> Session 1, design only, no code. The wedge's second front door: teams
> with ClickOps-heavy, half-terraformed, or inherited/acquired accounts —
> infrastructure `ubx` currently can't onboard at all, since bulk
> onboarding (UBI-18) needs a tfstate file to source identity from. This
> arc makes the cloud account itself the discovery source: "point `ubx`
> at the account, not at the repo."
>
> The mechanism decision below is made empirically, against a real AWS
> account (`839333509514`), not assumed from documentation: the AWS
> Resource Groups Tagging API, AWS Config, and the dormant tfplugin
> `ListResource` RPC were each actually called this session — one real,
> throwaway, tagged `aws_sqs_queue` created, probed, and destroyed;
> `hashicorp/aws@6.54.0`'s real `GetMetadata`/`ListResource` responses
> captured directly over the wire. GCP/Azure equivalents (Cloud Asset
> Inventory, Resource Graph) are named, not empirically probed this
> session — GCP's own CLI session had expired non-interactively at probe
> time; each platform gets its own future session's empirical pass,
> mirroring the `audit/` backend arc's own per-platform-module precedent.
> **Adoption stays record-only, blast-radius zero by construction** —
> discovery only ever produces `adoption`/`drift_adopt` proposals through
> the exact same, completely unmodified `core.RunScan`/`core.
> GenerateProposal` pipeline UBI-7/UBI-18 already built; this arc adds a
> new identity SOURCE, never a new proposal kind, never a new apply path.

## Scope: what this session designs, and what it doesn't

Designed here: the mechanism decision (tagging-API-primary hybrid,
below) and the real evidence behind it; the identity bridge (ARN/cloud-
native-ID → provider lookup shape) as the arc's actual hard problem, its
three-tier structure, and the honest state of the conformance registry's
own current lookup-shape knowledge; tag-scoped filtering as the primary
UX (`--tag`, a type allowlist, region); the provisional `ubx scan
--discover` verb shape and flags; stack-grouping inference as a
human-reviewed suggestion, never an auto-assignment; the attribution
bonus (genesis provenance reusing the existing `audit/` backends,
unchanged); the adversarial program; implementation slices toward a
real, tagged, ClickOps-style AWS resource set discovered and adopted
live.

Not designed here, named so it isn't assumed covered: GCP Cloud Asset
Inventory / Azure Resource Graph's own empirical mechanism validation
(each is its own future session, per-platform, matching `audit/gcp`/
`audit/azure`'s own arrival order relative to `audit/cloudtrail`); a
structured `TypeSpec` field replacing the three duplicated lookup-hint
tables this session's research surfaced (named as a real, recommended
follow-up below, not built); bulk *acceptance* of discovered proposals
(stays per-proposal and deliberate, exactly like `--all --tfstate`'s own
explicit non-goal); any policy engine or auto-accept rule (none exists
anywhere in this project yet, and discovery doesn't introduce the
first one).

## The mechanism decision — made empirically

Three real candidates, each actually exercised against the account
above before choosing.

### AWS Resource Groups Tagging API — the primary mechanism

A real `aws_sqs_queue` was created, tagged
`ubx-discovery-probe=<marker>`, and queried:

```text
$ aws resourcegroupstaggingapi get-resources --tag-filters Key=ubx-discovery-probe,Values=<marker>
{
  "ResourceTagMappingList": [
    {
      "ResourceARN": "arn:aws:sqs:us-east-1:839333509514:ubx-discovery-1785357707",
      "Tags": [{"Key": "ubx-discovery-probe", "Value": "<marker>"}]
    }
  ]
}
```

Real, confirmed properties:

- **Zero setup barrier.** No opt-in, no recorder, no per-account
  enablement — it answered immediately, in an account that had never
  used it before. This matters specifically *because* of who this arc
  is for: a ClickOps-heavy or half-terraformed account is, by the same
  reasoning that makes it a `ubx` prospect, less likely to already have
  an inventory service turned on.
- **Cross-type, one call family, tag-scoped** — exactly the "whole-
  account enumeration is a firehose, tag-scoped is the realistic path"
  shape the ticket names. `--resource-type-filters` narrows server-side
  too (confirmed: `--resource-type-filters sqs` returned the same
  single result) — but its filter strings are AWS *service* namespaces
  ("sqs", "s3"), not Terraform type names ("aws_sqs_queue"), a second,
  smaller translation the design below resolves client-side instead of
  by hand-maintaining a second filter-string table (see "Type
  filtering," below).
- **Returns identity only, never the resource's own attributes** — the
  response above is the *entire* payload: an ARN and the tags used to
  find it. This is a feature, not a gap: it matches this project's own
  "state provides identity, never truth" principle (docs/
  architecture.md's UBI-18 section) exactly — discovery, like tfstate
  onboarding before it, only ever supplies identity; the proposal's
  actual recorded content still comes from a real `ReadResource` call
  through the unmodified `core.RunScan` pipeline, never from the
  tagging API's own response.
- **Not universal coverage.** `conformance/registry.go`'s own existing
  `aws_iam_group` entry already documents a real, empirically-confirmed
  gap: IAM groups have no tagging API at all. Any untaggable type is
  structurally invisible to this mechanism, full stop — named here, not
  solved (see "Coverage gaps," below).

### AWS Config — evaluated, not chosen as primary

```text
$ aws configservice describe-configuration-recorders
{"ConfigurationRecorders": []}
```

Real, in the same account: **zero configuration recorders**. AWS Config
is a real, richer capability (point-in-time resource inventory,
configuration history, relationship graphs) — but it is opt-in,
ongoing-cost, and requires its own recorder + delivery channel already
running. That is a genuine adoption barrier precisely for this ticket's
own target market: an account with real Terraform discipline is more
likely to also have Config running; a ClickOps-heavy or inherited
account — the account this arc exists for — is the least likely to
already have it. **Decision: not the primary mechanism.** Named as a
future, opportunistic enrichment path only — if a target account
already has Config running, its resource history could one day
supplement the receipt or attribution — never required, not built this
arc.

### Per-type List loops, and the dormant tfplugin `ListResource` RPC

The "per-type List APIs via provider `ReadResource` loops" alternative
the ticket names was checked two ways.

**Cloud-SDK-side** (calling each AWS service's own native List/Describe
API directly, one per service): confirmed working (`aws sqs
list-queues` returns the queue URL directly — the *exact* lookup shape
needed, no translation at all) but does not scale as the *primary*
mechanism: it has no cross-type, tag-scoped shape at all — one call per
service, then client-side tag filtering against every service in the
account, for however many services a real account touches. Kept as the
**necessary fallback for untaggable types** (the `aws_iam_group` case
above) — never the front door.

**Protocol-side**: the tfplugin wire protocol this project already
speaks turns out to define exactly this feature natively —
`Provider.ListResource`/`Provider.GetMetadata.ListResources`, a real,
non-experimental part of `tfplugin5`/`tfplugin6`'s generated stubs
(`provider/tfplugin5`, `provider/tfplugin6`) — but never called
anywhere in this codebase before this session. Genuinely promising *in
principle*: a provider's own `ListResource` stream returns
`ResourceIdentityData` — the provider's own understanding of a
resource's identity, which would eliminate the ARN-translation problem
below entirely, for free, provider-agnostically (no AWS/GCP/Azure-
specific module needed at all, unlike the `audit/` backends).

**Tested live, this session, against the real `hashicorp/aws@6.54.0`
binary** (a throwaway same-package probe test, deleted after — never
committed): `GetMetadata` reports **1,682 total resource types, of
which exactly 53 implement `ListResource`** — about 3%. None of the
four free-tier types this project's own conformance-harness bulk run
already established as safe fixtures (`aws_sqs_queue`/`aws_sns_topic`/
`aws_iam_policy`/`aws_iam_user`) are among the 53. Calling
`ListResource` directly for `aws_sqs_queue` anyway (a type absent from
the 53) didn't error outright — it opened a stream and then closed with
`EOF`, no events, no diagnostic — a real, silent ambiguity worth naming
on its own: **nothing distinguishes "this type has zero resources"
from "this type doesn't support listing at all"** from the stream's own
behavior; a caller must always cross-check against `GetMetadata.
ListResources` first, never infer support from a clean-looking empty
stream. Calling it for a type that *is* in the 53 surfaced real
`Invalid Provider Server Combination` diagnostic errors — a symptom of
`hashicorp/aws`'s own muxed SDKv2/Plugin-Framework server architecture,
not a bug in this project's own (throwaway) probe.

**Decision: not viable as v1's mechanism.** 3% coverage, none of it
overlapping this project's own already-trusted fixture types, and real
diagnostic noise even where it claims support — this is early,
still-settling upstream surface, not something to build a primary
discovery path on top of today. **Named as the clearest single
"revisit" trigger for a future session**: re-run this exact probe
against future `hashicorp/aws` (and other) provider releases; if
`ListResource` coverage approaches completeness, it could replace the
tagging-API-plus-identity-bridge combination below entirely for
whichever provider gets there first, since it needs no per-type
translation table at all.

### Decision

**A hybrid, tagging-API-primary mechanism**: AWS Resource Groups Tagging
API for cross-type, tag-scoped enumeration (identity only); the
identity bridge below to translate each returned ARN into a provider
lookup shape; the exact, unmodified `core.RunScan`/`core.
GenerateProposal` pipeline for everything after that (the real
`ReadResource` call, fingerprinting, proposal shape). Untaggable types
fall back to a per-type List call, named honestly as reduced-coverage,
never silently promoted to the same tag-scoped UX. AWS Config and the
tfplugin `ListResource` RPC are both named, evaluated, and explicitly
deferred — not because they're bad ideas, but because this session
actually checked, rather than assumed, that neither is ready today.

## The identity bridge — enumeration returns ARNs, adoption needs lookup shapes

This is the arc's actual hard problem, not a formality. The tagging API
returns an ARN; `core.RunScan`'s `ReadResource` call needs whatever
`json.RawMessage` object that type's own provider `ReadResource`
actually requires to identify the resource — sometimes the ARN itself,
often something else entirely.

### What the conformance registry actually knows today — and doesn't

The ticket frames "the conformance registry, now machine-complete via
UBI-50" as the join. Checked directly rather than assumed: UBI-50's own
`conformance.AllVerdicts()`/`GeneratedFindings` genuinely covers every
resource type of every onboarded provider now (1,335 findings across
4,187 types) — but what it's *complete about* is narrower than "lookup
shape." `TypeSpec.IdentityFields` names which attributes carry a
resource's stable identity (CloudTrail-attribution-scoped, not
lookup-scoped); `TypeSpec.LookupHint` is a narrow, *negative* signal
("don't send this attribute alone — it reads back null") populated for
exactly three hand-verified AWS types (`aws_s3_bucket`, `aws_iam_role`,
`aws_iam_user`) and explicitly *not* generalized to express "both X and
Y required together" — `docs/conformance-harness.md`'s own text says
so directly. **There is no single structured field, anywhere in this
codebase today, that answers "what is this type's lookup shape."** The
actual answer, where it exists at all, lives in free-text `Notes` on
the 154 hand-written `Registry` entries — real, trustworthy, but prose,
not data a discovery pipeline can consume mechanically.

Worse than a single gap: this session's research found **three
separately-maintained copies of the same small fact** —
`conformance.Registry[i].LookupHint`, the generated `core/lookuphints`
table (its own strict, `go:generate`-produced subset, 3 entries), and
`tfstate.BuildLookup`'s own hand-written `extraLookupAttrs` map (a
*fourth*, independently-authored copy of the identical 3 entries,
`docs/architecture.md`'s own UBI-18 section already flags the
`IdentityFields`-vs-lookup distinction in prose but doesn't unify the
tables). **Recommendation, not built this session**: before or during
discovery's own build slices, promote this into one real, structured
`TypeSpec` field — e.g. a tri-state `LookupShape` capturing "id alone
suffices" / "id + these fields together" / "unknown, needs a live
probe" — generated once, consumed by `tfstate.BuildLookup`,
`core/lookuphints`, *and* discovery's own ARN-translation table, rather
than a fourth hand-maintained copy. This session doesn't build it; it
names the debt honestly rather than adding to it silently.

### The translation table — three real tiers, empirically confirmed

An ARN's own structure (`arn:aws:<service>:<region>:<account>:
<resource-id>` or `.../<resource-type>/<resource-id>`) already carries
service, region, account, and a trailing resource-id segment. Checked
against `conformance.Registry`'s own live-verified (`RealSafe`,
`Implemented`) entries, every AWS type's lookup shape falls into one of
three tiers:

| Tier | Meaning | Confirmed example | Translation needed |
| --- | --- | --- | --- |
| **A** | `id` **is** the ARN | `aws_iam_policy` — Registry's own Notes: *"id IS the ARN... lookup only needs `{\"id\": \"<policy-arn>\"}`"* | None — the tagging API's own `ResourceARN` is already the lookup. |
| **B** | `id` is the ARN's own trailing segment (± an augmented duplicate field) | `aws_vpc` (`id` = the `vpc-*` id, the trailing segment); `aws_iam_role`/`aws_iam_user` (`id` **and** `name` both = the trailing name segment — Registry: *"name alone reads back null, empirically confirmed"*); `aws_s3_bucket` (`id` **and** `bucket` both = the trailing bucket name) | A generic ARN-parser (split on `:`/`/`, take the last segment) plus a small "which extra field(s) to duplicate it into" table — the *same* table `tfstate.extraLookupAttrs` already hand-maintains today (see recommendation above). |
| **C** | `id` is constructed from ARN components, not a substring of the ARN | `aws_sqs_queue` — this session's own live probe: ARN `arn:aws:sqs:us-east-1:839333509514:ubx-discovery-1785357707`, real lookup `id` is the queue URL `https://sqs.us-east-1.amazonaws.com/839333509514/ubx-discovery-1785357707` | A real, per-type constructor function — but the ARN's own components (service/region/account/resource-id) are always sufficient *inputs*; confirmed no extra API round-trip is needed to build this specific example. |

Every tier is a small, well-scoped, per-type piece of work — never a
structural blocker — **except knowing which tier a given type is in**,
which is exactly the "structured `LookupShape` field" gap named above.
Where a type's tier is genuinely unknown (no `Registry` entry, no live
probe, nothing in `conformance.GeneratedFindings` strong enough to
infer it), or is Tier C without a built constructor yet, discovery does
not guess.

### Coverage gaps surface honestly: "discovered, not yet adoptable"

Per the ticket's own explicit instruction, a type discovery can't
bridge to a lookup shape is never silently dropped from the discovery
report. It surfaces as its own, named line — the exact same "skip,
with a reason, never abort the batch" posture `--all --tfstate` already
established (docs/architecture.md's UBI-18 section):

```text
discovered: arn:aws:someservice:us-east-1:839333509514:thing/abc123 (aws_some_type) -- not yet adoptable: no known lookup shape for this type
```

This is structurally identical to `--all`'s own three-skip taxonomy
(unknown type / no identity / read failure), with one new member: "no
known lookup shape" sits *before* any `ReadResource` call is ever
attempted (a lookup-bridge failure, not a read failure) — named as its
own distinct reason so a future operator (or a future session closing
this gap for one more type) can tell exactly which layer failed.

## Tag-scoped filtering as the primary UX

`--tag key=value` (repeatable, AND-combined) is the realistic default
scope — matching the ticket's own framing and this session's own probe,
which used exactly this shape (`--tag-filters Key=...,Values=...`)
against the real API. A provisional flag surface for the eventual `ubx
scan --discover` verb (verb shape per the ticket, confirmed
provisional, not finalized here):

```text
ubx scan --discover --tag <key=value> [--tag <key=value> ...] \
    [--type <resource-type> ...] --region <region> \
    --stack <stack> (--provider <path> | --source <src> --provider-version <v>) \
    [--limit <n>] [--yes] --out-dir <dir>
```

- **`--tag`** (repeatable) maps directly onto the tagging API's own
  `TagFilters` — the primary scoping mechanism, matching the ticket.
- **`--type`** (repeatable, Terraform type names — `aws_sqs_queue`, not
  AWS service namespaces) filters **client-side**, against the
  service segment already present in each returned ARN, rather than
  hand-maintaining a second "Terraform type name → AWS tagging-API
  service-namespace filter string" table alongside the identity
  bridge's own translation table. One ARN-parsing pass answers both
  "which tier" and "does this match `--type`."
- **`--region`** — the tagging API is itself regional; a real account
  discovery run needs to state which region(s) explicitly, never
  silently defaulted to "wherever the provider config happens to
  point," matching this project's own "no environment awareness,
  nothing implicit" posture elsewhere.
- **Whole-account enumeration is a firehose, and stays named as one,
  not solved here**: omitting `--tag` entirely is not an "adopt
  everything" mode this arc builds — the realistic path is tag-scoped,
  full stop; nothing here prevents a future, explicitly-separate
  "list every taggable type with no tag filter" verb, but it isn't this
  one.

## Adversarial program

Matching every prior arc's own discipline: each row is a required
observable outcome, reused-mechanism citations included.

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 1 | No lookup shape | A discovered ARN whose type has no known tier (unclassified, or Tier C with no constructor built) | Surfaces as `discovered, not yet adoptable: no known lookup shape` — never silently skipped, never a fabricated adoption. The rest of the batch continues (same "skip, don't abort" posture as `--all --tfstate`). |
| 2 | Tag matches thousands of resources | A `--tag` scope broad enough to return many pages | Tagging API pagination (`PaginationToken`, per AWS's own documented contract — **not empirically triggered this session**, since only one resource was tagged for the identity-bridge probe; a scripted multi-page test is real follow-up work for session 2+, named honestly rather than assumed proven) is followed to completion before anything else happens; the **matched count is printed and a `--limit`-gated confirmation is required** before any per-resource `ReadResource` call runs — the same "friction by default, not silently unbounded" posture `--confirm-destroys` already established for a different risk. |
| 3 | Permission denied mid-enumeration | The tagging API's own broad `tag:GetResources` call succeeds, but a specific resource's own type-specific read permission (e.g. `sqs:GetQueueAttributes`) is denied when `core.RunScan`'s `ReadResource` call reaches it | That one resource is skipped with a named permission-denied reason; the rest of the batch continues uninterrupted — identical posture to `--all --tfstate`'s own "provider read failed" skip category, reused, not reinvented. |
| 4 | Resource deleted between list and read | The tagging API lists a resource that's since been deleted by the time its own turn in a large batch arrives | `ReadResource` returns not-found; skipped with that reason. Structurally identical to `--all --tfstate`'s own "deleted from cloud since the state file was last written" case (`core/scan.go`/`cli/scanall.go`) — the exact same skip, a different identity source feeding it. |
| 5 | Already-adopted resource rediscovered | A tag-scoped discovery run re-covers a resource this ledger already has an `adoption`/`drift_adopt` entry for | `core.RunScan`'s own existing, unmodified address-based ledger lookup and fingerprint comparison already classify this correctly (`ScanUnchanged` if nothing changed, `ScanDrifted` if it has) — discovery needs zero new idempotency logic; this is the identical mechanism a second plain `ubx scan` against the same resource already goes through. |

## Stack-grouping inference — human-reviewable, never auto-assigned

Direct precedent already exists and is followed, not reinvented:
docs/architecture.md's own UBI-18 section made a deliberate, documented
choice to *never* auto-split a Terraform module path into its own
`ubx` stack — module-path information only ever becomes a plain-text
summary hint and a name-collision-avoidance suffix, never a silent
stack assignment. Discovery's own tags/naming signal is weaker evidence
than a real Terraform module path (a human-authored organizational
unit) was already judged to be, so it gets *at least* the same
caution, not less:

- **`--stack <name>` is required to actually write proposals** — no
  change from every other command's existing convention. Discovery
  never invents a stack assignment on its own initiative.
- **A separate, read-only `--suggest-stacks` preview** groups
  discovered resources by a configurable grouping tag (a `Project`/
  `Stack`/`Environment`-style key, operator-named, never guessed) or,
  absent one, a naming-prefix heuristic, and prints a report — how many
  resources per suggested group, which ARNs are ungrouped — **writing
  nothing**. The operator reads it, then re-invokes discovery per
  group with an explicit `--stack`. This keeps "suggestion" and
  "assignment" as two distinct, separately-gated actions, exactly the
  boundary the ticket asks for.

## The attribution bonus — genesis provenance, existing backends reused

`core.EventLookup`/`core.AttributeDrift`/the per-source `audit/cloudtrail`
/`audit/gcp`/`audit/azure`/`audit/k8s` dispatch (`cli/attribution.go`)
already do exactly the mechanical work genesis attribution needs —
search a real audit backend for events matching a resource's own
identity, defensively re-filter, emit a typed `IntentSource`. Two real
differences from the existing drift-attribution call site, both
additive, neither touching the existing machinery:

- **What's searched for**: drift attribution searches for *any* event
  touching the resource within a window around when drift was
  detected; genesis attribution searches for a **creation-verb** event
  specifically (`CreateBucket`/`RunInstances`/`CreateQueue`-shaped
  `EventName`s) — a small, curated per-service table, the same kind of
  hand-curated knowledge every `audit/` backend's own dispatch already
  requires, not a new category of problem.
- **What window**: drift attribution's window is anchored to a known
  drift-detection time; genesis attribution has no such anchor — it
  searches the earliest available point in whatever retention window
  the backend actually has (CloudTrail's default trail, Cloud Logging's
  own retention, etc.), and a resource created before that window began
  (or before the backend was ever turned on) is an honest, named
  `UnattributedKind` outcome — a real gap, not a failure to silently
  round up from.

Purely a bonus, exactly per the ticket: a discovered resource adopts
successfully with or without genesis attribution, identical to how
`--no-attribution` already makes drift attribution itself optional
today.

## Implementation slices, toward a real live finale

### Slices 1-3: built (2026-07-30, session 2)

**The identity bridge, real code** (`discovery/arn.go`, `discovery/
tiers.go`): `ParseARN` splits an ARN into partition/service/region/
account/resource, further splitting resource on its first `/` into a
resource-type prefix and id — the same `(service, resourceTypePrefix)`
pair, not service alone, that lets `aws_iam_role`/`aws_iam_user`/
`aws_iam_policy` (all `iam`) disambiguate without needing `--type` at
all, a refinement found while implementing, not assumed from the design
session. `tierTable`, keyed by that pair, seeded with exactly this
session's own confirmed examples: `aws_iam_policy` (Tier A),
`aws_vpc`/`aws_iam_role`/`aws_iam_user`/`aws_s3_bucket` (Tier B),
`aws_sqs_queue` (Tier C, its own real queue-URL constructor). An
unclassified `(service, prefix)` pair — or a classified Tier C entry
whose constructor itself fails — surfaces as `ErrNotYetAdoptable`,
never a fabricated lookup. Honestly named as this session's own fourth
copy of the same tiny lookup-hint fact (alongside `conformance.
Registry.LookupHint`, `core/lookuphints`, `tfstate.extraLookupAttrs`)
— the structured `TypeSpec.LookupShape` consolidation remains
recommended, not built.

**`discovery.Discover`** (`discovery/discover.go`): a `TaggingAPI`
interface shaped to match `*resourcegroupstaggingapi.Client.
GetResources`'s own real method signature exactly, so the real SDK
client satisfies it with zero adapter code (the same dependency
inversion `core.StateReader`/`core.EventLookup` already establish) —
hermetic tests script multi-page responses against a fake
implementation, never a real AWS account. Paginates to completion
before returning anything; `--type` allowlist resolved to expected
`(service, prefix)` keys up front (`ErrUnknownDiscoveryType` if a
named type has no tier-table entry at all — a fail-fast, whole-request
error, distinct from a per-resource `ErrNotYetAdoptable`); every
tag-matched, non-excluded ARN is classified and returned, adoptable or
not. `CheckLimit` is its own small, pure confirmation-gate function
(`DefaultLimit` 100), deliberately separated from pagination so the
gate itself is hermetically testable without needing thousands of real
tagged resources — exactly as session 1 named this gap honestly.

**`ubx scan --discover`** (`cli/scan.go`, `cli/scandiscover.go`):
wired as a third mode alongside single-resource and `--all`, mutually
exclusive with both (mirrors `--all`'s own existing exclusivity
check). `--tag key=value` (repeatable, same key OR-combines into the
tagging API's own `TagFilters.Values`, different keys AND-combine —
confirmed against the real API's own documented semantics via `go
doc`, not assumed), `--discover-type` (a dedicated flag name, not a
second meaning for the existing single-resource `--type` — avoids any
ambiguity), `--region`, `--limit`/`--yes` for the confirmation gate,
`--suggest-stacks`/`--stack-tag` for the read-only grouping preview
(`runScanSuggestStacks`, a separate, simpler path — no ledger, no
provider, nothing written, exactly per design). `runScanDiscover`
mirrors `runScanAll`'s own structure closely: the identical
`core.RunScan`/`core.GenerateProposal` pipeline, the identical
`nextParent` chaining, the identical multi-provider-vs-single-provider
branch — genuinely a new identity source only. Two package-level
seams (`newDiscoveryTaggingAPI`, `newDiscoveryStateReader`), the same
convention `openRemoteLedgerStore` already establishes, let hermetic
CLI tests fully control both the tagging API and the provider-read
half without touching real AWS or fakeprovider's own shared
`fake_widget` fixture (whose schema has no bearing on discovery's own
AWS-specific tier-table types) — zero risk introduced to the wide
existing fakeprovider-based suite elsewhere in this project. A real,
found-while-building refinement: a provider is only ever launched if
at least one discovered resource actually bridged to a lookup shape —
a `--tag` scope that turns up nothing adoptable never requires
`--provider`/`--source` at all.

**All five adversarial rows, hermetically verified**, `discovery/
discover_test.go` + `cli/scandiscover_test.go`: no lookup shape (never
silently dropped, `NotAdoptableReason` always populated); pagination
(a real scripted 3-page fake, followed to completion) plus the
confirmation gate (`CheckLimit`, both refusing and `--yes`-overridden);
permission denied mid-enumeration and resource deleted between list
and read — found, while testing, to be **the identical code path**:
both are ordinary `core.RunScan` errors, both land in the same
"provider read failed" skip category `runScanAll`'s own equivalent
case already uses, confirmed with one combined test rather than two
artificially separated ones; already-adopted rediscovery (a real `ubx
accept` in between two discovery runs, confirming `core.RunScan`'s own
existing idempotent classification needs zero new logic). 17 new tests
total (10 in `discovery`, 7 in `cli`), all passing on first real run
after fixes for two real bugs found by running them: a provider was
being required even when nothing was adoptable, and the idempotency
test's own premise was wrong on the first attempt (a merely-*generated*
proposal was never actually accepted, so "already adopted" wasn't
really being tested at all until a real `ubx accept` was inserted).

**Live-verified, read-only, no ship**: a real, hand-created (`aws sqs
create-queue`, never via `ubx`) SQS queue, tagged with a distinctive
marker, discovered end to end via `ubx scan --discover --tag ...
--source hashicorp/aws --provider-version 6.54.0` — the real tagging
API found it, the real Tier C constructor built its real queue URL,
the real `hashicorp/aws` provider read its real live state, and a
real, complete `adoption` proposal was generated (`blast_radius`: all
zero) — confirmed via the filesystem that no `ledger/` directory was
ever created at all (nothing accepted, nothing appended, purely
record-only). The queue was destroyed afterward and the sweep
confirmed clean via both `aws sqs list-queues` and the tagging API.

### Slices 4-5: not started

**Genesis attribution** (reusing `core.EventLookup` unchanged, new
creation-verb tables per backend) and **the live finale** (a larger,
multi-resource, tagged, ClickOps-style set, adopted with genesis
attribution where available) remain session 3+ work.

## Out of scope for v1, named so it isn't assumed covered

Whole-account (untagged) enumeration as a supported mode; GCP/Azure
mechanism validation (each its own future session); a structured
`TypeSpec.LookupShape` field unifying the four duplicated lookup-hint
tables (recommended, not built — discovery's own `tierTable` is now
the fourth); bulk acceptance of discovered proposals; any policy
engine, auto-accept rule, or environment awareness; re-adopting the
tfplugin `ListResource` RPC as the primary mechanism (revisit trigger
named above, not a decision made now); AWS Config as anything more
than a possible future enrichment source; genesis attribution and the
live finale (slices 4-5, above).
