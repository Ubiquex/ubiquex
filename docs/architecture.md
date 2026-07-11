# Architecture — ubiquex-cli (ubx v2)

## Thesis

Infrastructure change management. The unit of meaning is the **change** (proposal),
not the state. Every change is a typed, hashed, signed contract; the append-only
**ledger** of accepted proposals is the sole source of truth. Current infrastructure
= fold(applied proposals).

One sentence: *a compiler where code, diagrams, documents, and conversation are all
frontends to one typed, signed IR.*

## The trust chain

```
author (any medium) → resolver → PROPOSAL (typed, resolved, hashed)
→ acceptance (signature bound to hash) → ledger append → executor (ship)
→ cloud, via Terraform providers directly
```

Invariants:
1. What applies is exactly what was signed (hash match), never a file, never chat.
2. Every resource traces to a proposal, its dialogue/intent, and its approver (`ubx why`).
3. The LLM operates in intent-space only; the deterministic resolver computes all
   values; nothing the LLM emits reaches apply without resolution + human signature.
4. Staleness by construction: if referenced live state or a neighbor ledger changes
   after resolution, the hash breaks and the proposal must be re-resolved.

## Core concepts

- **IR** — the typed resource graph schema. Promoted from v1 XCL's type system
  (Computed<T>, secret refs, ephemeral, cross-stack pinned refs). XCL the *syntax*
  is dead; at most a debug rendering of the IR.
- **Proposal** — intent + resolved IR delta + cost_delta + blast_radius +
  invariants_checked + hash + lifecycle (draft → refined → accepted → applied;
  or stale). Adoption proposals (import/drift) have zero blast radius by construction.
- **Ledger** — per-stack append-only chain in git (`ledger/` + `ledger.lock` head).
  Acceptance = PR merge binding (approvers + merge SHA) or local `ubx accept`;
  optional hardened cryptographic signing tier later.
- **Cross-stack refs** — pinned imports ("consumes @network.vpc_id as of head 7fc2"),
  never live pointers. Neighbor advancement ⇒ staleness, detected not discovered.
- **Drift** — reality diverging from ledger becomes an implicit proposal with two
  resolutions: adopt (record reality, with CloudTrail attribution) or revert
  (signed restore). No silent auto-revert by default; policy decides tiers.
- **Import** — adoption proposals for existing resources; bulk `--scan` infers stack
  grouping. Import, drift, and manual-edit reconciliation are the same operation.
- **Policies** — typed predicates over whole proposals (not just resources): cost,
  blast radius, approver requirements. Evaluated at author/propose/ship time.
  Policies are themselves ledger content. Authored via SDK.
- **Projections** — hub-and-spoke. Ledger IR is the hub; code (SDK), markdown docs,
  diagrams, canonical IR dump are spokes. `render --check` byte-compare is the CI
  invariant. Lossy mediums author only their slice (diagram = topology, md = intent).
- **SDK** — describe-only (Pulumi ergonomics, zero execution authority). Code
  evaluates in a hermetic sandbox (no net/env/fs — a security boundary) with a
  double-run determinism check; the frozen IR snapshot is what gets hashed.
  Codegen'd from provider schemas. TS first, Go second.
- **Intent provider** — LLM behind a plugin interface (Claude/OpenAI/local),
  structured-output validated against the proposal schema.

## Execution layer

**No Terraform, OpenTofu, or Pulumi engines in the background.** ubx speaks the
tfplugin gRPC protocol directly to Terraform *providers* (standalone MPL-2.0
binaries): GetProviderSchema, ReadResource, PlanResourceChange, ApplyResourceChange,
ImportResourceState. No .tfstate exists anywhere — the ledger is the only record.

**Dual v5/v6, not v6-only.** Originally scoped as v6-only, but real provider
binaries as they exist today — including modern terraform-plugin-framework-native
ones, not just SDKv2 legacy — were empirically found to serve tfplugin **v5** on
the wire (see STATE.md 2026-07-10 surprise entry for the evidence: both
terraform-provider-aws 6.54.0 and terraform-provider-time 0.9.2 report v5 even
when a client explicitly requests v6 via `PLUGIN_PROTOCOL_VERSIONS`). ubx's
provider layer therefore exposes one protocol-agnostic `Provider` interface
(schema dump, configure, read) backed by two wire implementations — tfplugin5
and tfplugin6 — selected from whichever version the plugin actually negotiates
during the handshake. Callers never branch on protocol version. Both wire
implementations decode/encode resource and provider config values as
cty-msgpack (via go-cty), including nested schema blocks as typed object/
collection attributes — confirmed necessary empirically too: JSON-encoded
DynamicValue payloads and flattened (attributes-only) object types were both
rejected by the real AWS provider binary.

The native executor owns failure semantics end-to-end: per-resource state machine
(pending / in_flight / unknown_post_timeout / failed), reconcile-by-query on
ambiguous failures, partial-apply as modeled state. This is the reliability
differentiator; adversarial failure tests are first-class.

Scope containment: AWS provider first, dual tfplugin v5/v6, conformance suite
grows per provider.

### Provider binary acquisition (UBI-8)

ubx downloads provider binaries from **registry.opentofu.org**, not
registry.terraform.io, even though a provider's canonical *source address*
(what's recorded in the IR, in proposals, in everything a human reads) stays
the Terraform-standard form — e.g. `registry.terraform.io/hashicorp/aws`.
This is a deliberate split between identity and download mechanism:

- **Why not registry.terraform.io directly:** HashiCorp's registry ToS and
  the BSL-era licensing posture around it are not something a third-party,
  non-Terraform tool should build a load-bearing dependency on — the
  registry's terms are oriented around Terraform/HCP client use, and
  building an unrelated product's core acquisition path on it risks the
  rug being pulled with no warning and no recourse. This isn't a
  hypothetical: it's exactly the kind of platform-dependency risk a
  solo-founder wedge (see Business frame) can't absorb.
- **Why registry.opentofu.org works as the substitute:** OpenTofu is the
  Linux Foundation-governed, MPL-2.0 fork of Terraform, and its registry
  mirrors the same provider ecosystem (same namespaces, same versions, same
  releases — often literally re-publishing the upstream release artifacts)
  via the **same provider registry protocol** Terraform itself defined
  (service discovery at `/.well-known/terraform.json`, then
  `/v1/providers/<namespace>/<type>/<version>/download/<os>/<arch>`).
  It's explicitly built and governed for third-party tool use, not just
  OpenTofu-the-CLI. Verified live against the real protocol (not assumed
  from memory) while implementing this.
- **Verification, not just trust-the-CDN:** every acquisition downloads the
  release's `SHA256SUMS` file and its detached OpenPGP signature, verifies
  the signature against a registry-served signing public key, extracts the
  expected digest for the requested platform's exact filename from
  `SHA256SUMS`, and only then checks the downloaded archive's own SHA-256
  against that (signature-covered) digest — never against the bare
  `shasum` field alone, which isn't itself signed. A provider binary is
  cached (`~/.ubx/providers/<hostname>/<namespace>/<type>/<version>/<os_arch>/`)
  only after this succeeds; once cached, it's trusted again without
  re-verifying (content-addressed by source/version/platform, which is
  exactly what was checked).
- **Explicit version pins only — no "latest" resolution.** A proposal's
  provider version is part of what was reviewed and hashed into the
  ledger (see `resolution.inputs`); silently resolving "latest" at
  acquisition time would make that reviewed version meaningless. Every
  acquisition names an exact version.
- **`UBX_PROVIDER_MIRROR`** (a local directory) is checked first, before
  any network call, preserving the plain "download it yourself, hand ubx
  the binary" workflow used before this existed — a mirror hit is trusted
  as-is (a local file an operator placed there is trusted differently than
  a network download, the same reasoning Terraform's own filesystem-mirror
  feature uses), with no signature verification performed on it. A mirror
  miss is not an error — it falls through to the cache, then the network,
  unchanged.
- **Attribution:** the verified provider binary's own SHA-256 (of the
  extracted executable, not the archive — that's literally what gets
  exec'd and produces a reading) is recorded in the generated proposal's
  `resolution.inputs[].provider_checksum` — see docs/schema.md's
  "ProviderChecksum" amendment. `ubx why` can eventually answer not just
  "what did we observe" but "which exact build of which provider observed
  it."

### CloudTrail attribution (UBI-10)

Every `drift_adopt` proposal `ubx scan` generates gets a best-effort attempt
at CloudTrail attribution — the "attribute via CloudTrail" half of the
wedge pitch (see Business frame below), not deferred behind a later
milestone. Two new `intent.sources` kinds carry the result: `cloudtrail`
(a matched management event — actor ARN, event id/name/time, source IP,
session context) or `cloudtrail_unattributed` (attempted, failed, with a
`reason` — see docs/schema.md's amendment for the three reasons and what
distinguishes them). **Best-effort by construction**: attribution failure
of any kind never blocks generating or accepting the underlying drift
record — the drift itself is always recorded; who/when caused it is
evidence layered on top.

Same dependency-inversion discipline as the tfplugin provider client
(`core.StateReader`, UBI-7 follow-up): `core/attribution.go` defines
`EventLookup`, a minimal interface core owns, and `AttributeDrift`, which
holds all of the actual decision logic (which resource identity value to
search by, exact-match filtering against a possibly-fuzzy lookup, ordering
multiple matches newest-first, classifying an unmatched search) —
fully unit-testable against a fake `EventLookup`, no AWS SDK, no network.
The new `cloudtrail/` package is the only place in this codebase that
imports an AWS SDK (`aws-sdk-go-v2`) directly, implementing `EventLookup`
against the real CloudTrail `LookupEvents` API; `cli/attribution.go` wires
the two together into `ubx scan` (`--no-attribution` opts out per-invocation).

Scope, deliberately narrow: management events via `LookupEvents` only
(CloudTrail's ~90-day default event history, no trail configuration
required) — no CloudTrail Lake, no data events, no S3-object-level
logging. Correlation always runs over `[last ledger observation, scan
time]`, read back from the ledger itself (`Ledger.LastObservationTime`),
not an arbitrary lookback.

**Which identity value to search by is not a static per-type table** —
`core.AttributeDrift` derives it from the resource's own just-observed
state (`id`, then `arn`, then `name`, whichever are present), because which
attribute CloudTrail's `ResourceName` lookup attribute actually recognizes
turns out to vary by AWS service and was confirmed empirically, not
assumed: for `aws_s3_bucket`/`aws_iam_role`/`aws_vpc`, searching by the
resource's `id` (bucket name / role name / vpc-id) works, but searching by
the full ARN returns **nothing at all**, even for genuinely matching
events (verified live against the real account — see STATE.md). `id` is
tried first for that reason, with `arn`/`name` kept as fallbacks rather
than trusted to generalize from three data points. Real-world CloudTrail
delivery latency was also measured directly against this account while
building this feature: a live `PutBucketTagging` call took a little over
two minutes to become queryable via `LookupEvents` — well under the
documented ~15-minute upper bound, but far from instant — which is why
`delivery_window` exists as its own distinct, non-`no_matching_event`
outcome (a narrow correlation window can't rule out "the event just
hasn't propagated yet" the way a wide one that still finds nothing can).

## Decision loop (UBI-11)

M3-4's "decision loop" (docs/plan.md) turns a detected drift (UBI-7/UBI-10)
into a resolved, recorded decision without ubx itself becoming the thing
that enforces process. Three stages, each independently shippable:

1. **PR-merge acceptance binding** — a second acceptance tier alongside
   `local` (docs/schema.md's `acceptance.method`), for teams whose real
   review process already happens in a pull request.
2. **`.tf` write-back** — once a drift is accepted, correct the team's own
   Terraform source to match reality, narrowly.
3. **GitHub App skeleton** — surfaces drift as an issue/PR automatically,
   reusing stage 1's binding for a receipt on the resulting PR.

### PR-merge acceptance: derived, never asserted

The organizing principle, stated once so every design choice below can be
checked against it: **ubx never trusts a claim of acceptance — it verifies
one.** `local` acceptance (existing) trusts the operator running `ubx
accept` on their own machine, which is honest about what it is: a
convenience tier with no external witness. `pr_merge` acceptance instead
*re-derives* the whole `Acceptance` record from artifacts ubx doesn't
control (git history, the GitHub API) every time it's written, and can
re-derive it again anytime after — a `pr_merge` proposal's acceptance is
a claim that's supposed to be independently checkable for as long as the
ledger exists, not a one-time assertion trusted at write time and never
looked at again.

**The flow:**

1. An author resolves a proposal (any tool: `ubx propose` computes the
   canonical hash without touching the ledger) and commits the draft
   proposal JSON as an ordinary file on a branch — anywhere in the repo
   the team likes; it isn't part of `ledger/proposals/` yet, since that
   directory holds *accepted* entries keyed by their hash, and this one
   isn't accepted yet.
2. The PR body carries a trailer line: `ubx-proposal: <hash>` — the exact
   value `ubx propose` printed. This is the claim; nothing here is
   trusted yet.
3. Ordinary GitHub review happens. Branch protection, required reviewers,
   CODEOWNERS — all of that is **GitHub's job**, completely outside ubx.
   ubx has no opinion on whether a merge was "properly" reviewed by
   whatever the team's policy is; it only records what actually happened.
4. Once merged, `ubx accept --from-merge <sha> --proposal-file <path>
   --github-repo <owner/name>` derives acceptance:
   - Verifies `<sha>` exists in the local git history (`--repo-dir`, a
     clone/working tree ubx can query — no GitHub API call needed for
     this check).
   - Reads the proposal file's content *at that commit* (`git show
     <sha>:<path>`) — not whatever's on disk right now, which could have
     moved on.
   - Recomputes the proposal's canonical hash from that exact content and
     requires it to equal the trailer's claimed hash. A mismatch is a
     hard failure — the trailer is what a human reviewed; if the file
     hashes to something else, review happened over different content
     than what's being accepted, and this is fabricated evidence,
     handled the same way (rejected) whether it's an authoring bug or an
     attempt to substitute content after review.
   - Finds the merged pull request associated with `<sha>` via the
     GitHub API and reads its **current** review state: every reviewer
     whose most recent review is `APPROVED` (a later `CHANGES_REQUESTED`
     supersedes an earlier `APPROVED` from the same person — ubx doesn't
     count a withdrawn approval).
   - Writes `acceptance = {method: "pr_merge", merge_sha, approvers,
     accepted_at}` and appends to the ledger, exactly like `local`
     acceptance's final step.
5. **Zero approvers is a valid, recorded outcome, not a rejection.** If
   the PR merged with no approving reviews at all (branch protection
   disabled, an admin override, a solo-maintainer repo), ubx records
   `approvers: []` and proceeds. Whether that *should* have been
   possible is a policy question for GitHub's branch protection to
   answer, not something ubx enforces after the fact — enforcing it here
   would be ubx quietly assuming an authority (blocking a decision
   someone already made) it explicitly does not have (see Business
   frame: "wedge reads and records before it ever writes" — acceptance
   binding records a decision, it does not gate one).
6. **Re-verification, anytime.** `ubx why <id> --verify-acceptance
   --repo-dir <path>` re-runs the git-history and hash checks against a
   `pr_merge` proposal's current state, and — network/token permitting —
   re-fetches the PR's current reviews and reports whether the recorded
   approver set still matches (a review dismissed after the fact is
   exactly the kind of thing "derived, not asserted" exists to catch).
   If the GitHub API leg can't run (no token, offline), that's reported
   as *inconclusive*, not silently treated as a pass — an acceptance
   claim that can only be partially re-checked right now is weaker than
   one that was fully re-checked, and `ubx why` says so rather than
   rounding up.

**What ubx explicitly does not do here:** enforce required reviewers,
enforce branch protection, block a merge, or have any opinion on GitHub's
process before the merge happens. All of that remains entirely GitHub's
job. ubx's contribution is exactly the sentence in the wedge pitch: "a
signed record of what you decided" — decided by the team, through
whatever process they already run, recorded faithfully by ubx afterward.

### `.tf` write-back: narrow-scope, surgical

Triggered only by an *accepted* `drift_adopt` proposal — never a `change`
or any proposal a human is still authoring; write-back records reality
into existing source, it doesn't propose new infrastructure. Scope is
deliberately narrow, matching M3-4's own framing ("narrow-scope
bidirectionality," not a general HCL code generator):

- **In scope:** overwriting a literal attribute value (string, number,
  bool, or a literal map/list of those) on an existing resource block,
  located by address (`type "name" { ... }` matching the drifted
  resource), including nested attribute paths (e.g. `tags.hotfix`, the
  same dot-notation `delta.modifies` already uses). Also in scope
  (UBI-11 stage 2 follow-up, once real usage made clear it's the single
  most common drift shape): **inserting a brand-new key into an existing
  literal map attribute** — someone tags a resource in the console with a
  key the `.tf` file's `tags = { ... }` never had. The new key matches the
  existing object's own formatting — its indentation, and whether its
  items are comma-terminated — rather than an arbitrary default; an empty
  `{}` gets a sensible first entry.
- **Still never:** creating a *new* top-level attribute that isn't in the
  file at all, or creating/deleting/reordering blocks, or reformatting a
  file, or touching anything that isn't the specific drifted attribute/
  key. Only ever growing an *existing* literal object's key set is in
  scope — inserting a wholly new attribute, or a new nested structure more
  than one level deep, means picking a position/indentation with no
  existing anchor to match, which is a meaningfully different (and
  currently out-of-scope) problem from surgically editing something that
  already has an exact, unambiguous location. Edits go through `hclsyntax`
  for exact byte-range discovery, not `hclwrite`'s higher-level
  `Body.SetAttributeValue` — that regenerates an entire attribute's
  tokens and would reformat/lose comments on anything with internal
  structure (an object or list literal) — so comments, formatting, blank
  lines, whatever idiosyncrasies a real `.tf` file has survive untouched
  by construction, not by best-effort diffing.
- **If the drifted attribute's current value in the `.tf` file is itself
  an expression** — a variable reference, a function call, an
  interpolation, anything that isn't a literal — write-back **declines**,
  producing a manual-reconciliation report naming the attribute and the
  expression it can't safely overwrite. The same rule governs a new-key
  insertion: if the *parent* map itself is an expression (`tags =
  merge(...)`, `tags = var.common_tags`) rather than a literal `{ ... }`,
  insertion declines too — there's no literal object to safely grow.
  Overwriting `var.instance_type` with a literal string would silently
  sever that attribute from whatever the variable was for; that's a
  decision for a human, not something a
  drift-recorder does on their behalf.
- **Output is a diff (or a commit on a branch), never a silent push** —
  the same "human in the loop" posture as everything else in the trust
  chain. A team applies it exactly like any other change: review, then
  merge.

### GitHub App skeleton

Read-only repository permissions are the security story, not an
afterthought bolted on: because acceptance is *derived* (see above), a
GitHub App that only ever reads (contents, pull requests, checks) can
still do everything stage 3 needs — detect drift, open an issue/PR with
the proposal and a human-readable receipt, and — once a human merges that
PR — derive acceptance the exact same way `ubx accept --from-merge`
already does for a manually-opened PR. The App never needs write access
to *apply* anything, because it never applies anything; it records. This
is the same containment "wedge reads and records before it ever writes"
already establishes for the executor, extended to the App's own
permission scope.

Skeleton scope for this milestone: one repo, a scan triggered on a
schedule or manually, drift surfaced as an issue or PR containing the
generated proposal plus a receipt (diff, attribution if any, blast
radius). Explicitly deferred: webhook-driven (vs. scheduled/manual)
triggering, installation-flow hardening, multi-repo fan-out — enough to
prove the loop end-to-end on one repo, not a general-purpose App yet.

## Component map (build order)

1. Core IR + proposal schema (versioned; canonical hashing)
2. Resolver (v1 typechecker reborn; ~40-60% rewrite expected — ledger-schema
   pressures differ from compiler pressures)
3. Ledger (git chains, acceptance binding, workspace index)
4. Native executor (graph walk, failure state machine)
5. Provider layer (tfplugin v6 client, lifecycle, AWS)
6. Projection engine (renderers + render --check)
7. Authoring frontends (SDK, intent provider, diagram/md parsers)
8. CLI (propose / refine / accept / ship / why / import / render / status / revert)
9. Policy engine
10. Nexus (SaaS: hosted index, 24/7 drift watchers, approval routing, UI,
    integrations) — later; never in the apply path.

## What carries over from v1

- Keep as-is: graph algorithms (topo sort, cycle detection, cross-stack resolution).
- Keep the design, rewrite the code: type system → IR (Computed<T>, ephemeral,
  secret(), typed refs).
- Dead: XCL lexer/parser/formatter, Tree-sitter/editor extensions, v1 CLI verbs
  (except `ship`, `init` by name), v1 backlog (archived, not migrated).

## Business frame

- Category: infrastructure change management ("every change is a signed, costed,
  explainable contract").
- Wedge (first 6 months): **drift attribution** — GitHub App + CLI on existing
  Terraform repos; detect drift, attribute via CloudTrail, resolve as signed
  adopt/revert proposals. Zero-migration entry; every resolution builds a ledger.
- Open-core split: everything a solo engineer needs to trust the tool is free
  (trust core never paywalled); Nexus owns coordination (approval routing, watchers,
  RBAC/SSO, UI, managed LLM).
- Secrets rule: ledger stores references only, never material.
