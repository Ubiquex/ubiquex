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

## Revert path (UBI-16)

M3-4's other resolution to a detected drift (§Core concepts — Drift: "two
resolutions: adopt ... or revert (signed restore)"). Where `drift_adopt`
records that reality's new state (Y) is now the ledger's truth, `drift_revert`
records the opposite decision: the ledger's existing truth (X) is correct,
and reality needs to be corrected back to it. Same detection (`ubx scan`),
same staleness discipline, opposite direction.

### `ubx scan --propose revert|adopt|both`

New flag, default `adopt` (current behavior, byte-for-byte unchanged). Only
meaningful on a `drifted` outcome — a `new` (never-seen-before) resource has
nothing to revert to, so `--propose` has no effect there; `ubx scan` always
generates an `adoption` proposal for a new resource regardless of the flag's
value.

On drift:
- `adopt` (default) — generates `drift_adopt` only, exactly as before.
- `revert` — generates `drift_revert` only: a proposal whose
  `delta.modifies` describes the *corrective* change, `before` = the
  observed (drifted) value, `after` = the ledger's already-recorded value
  — the reverse of `drift_adopt`'s own before/after (mechanically:
  `diffAttributes(observed, ledgerState)` instead of
  `diffAttributes(ledgerState, observed)` — same function, arguments
  swapped).
- `both` — generates both, from the same scan/observation, as two
  independent draft proposals sharing the same `parent` (the current
  ledger head). They are alternative resolutions to *one* detected drift,
  not two changes to append separately: whichever gets accepted first
  advances the stack's ledger chain; the other becomes stale the moment
  that happens (ordinary parent-mismatch staleness, no new mechanism
  needed) and would need re-resolving (a fresh `ubx scan`) before it could
  still be accepted.

### `drift_revert`'s blast radius is real

Unlike `adoption`/`drift_adopt` (all-zero blast_radius, record-only against
the cloud by construction), a `drift_revert`'s `blast_radius.modifies` equals
its `delta.modifies` count exactly, and its `creates`/`destroys` stay zero
(revert only ever corrects existing attributes, never creates or destroys a
resource). This isn't a record of something that already happened — it's a
real, live, prospective change: **accepting a `drift_revert` is a decision to
change cloud**, which is exactly the M3-4 framing ("revert emits plan —
apply via the team's own tooling at this stage; executor trust comes
later"). ubx itself never applies it; see `ubx revert-plan` below.

### Staleness applies doubly

A `drift_revert`'s `resolution.inputs` entry carries the same shape and the
same value as its `drift_adopt` sibling would from the same scan: the
*observed* (drifted) state's hash, not the restore target. This is
deliberate, not an oversight — it's what `accept --reverify-with`/
`--reverify-source` need to keep meaning what they already mean elsewhere in
this codebase: "has reality moved again since this proposal was drafted?"
Recording the restore-to hash instead would make that check compare against
something that was never live, defeating its purpose. Reverifying a
`drift_revert` before accepting it therefore blocks exactly when reality
drifted a *second* time between `ubx scan --propose revert` and `ubx accept`
— the same mechanism, unmodified, that already protects every other
acceptance path.

### A necessary correction: drift detection compares against ledger truth, not last observation

Building this surfaced a real divergence that didn't exist before
`drift_revert` did: `RunScan` used to classify drift by comparing a fresh
read against `Ledger.LastObservedHash` — the most recently *recorded*
`resolution.inputs[].observed_hash`, walked directly, independent of
`FoldState`. For every kind that existed before this session
(`adoption`/`drift_adopt`), "the last thing we recorded observing" and "what
the ledger's fold reconstructs as current truth" were always the same
value, because accepting either one *is* the decision that the observed
value becomes the ledger's truth — the two mechanisms coincided by
construction, not by any explicit design choice tying them together.

`drift_revert` breaks that coincidence on purpose: its `resolution.inputs`
entry (by the rule just above) records the *observed/drifted* hash, while
its `delta.modifies[].after` — what `FoldState` folds forward — is the
*restored* (ledger-truth) value. Accepting a `drift_revert` is a decision
that hasn't been applied to cloud yet, so immediately afterward reality is
still drifted; `FoldState` and `LastObservedHash` now correctly disagree
about "what's true" versus "what we last saw."

The fix: `RunScan` now classifies drift by comparing the fresh read's hash
against `ObservedHash(FoldState(addr))` — the ledger's actual reconstructed
truth — rather than `LastObservedHash(addr)`. This is provably a no-op for
every proposal shape that predates `drift_revert` (both mechanisms compute
the same canonical hash for `adoption`/`drift_adopt` chains, verified by the
full existing test suite passing unchanged), and it's also the semantically
correct baseline regardless — "drift" is defined here (§Core concepts) as
*reality diverging from the ledger*, which is exactly what `FoldState`
answers and `LastObservedHash` only ever approximated. It's what makes the
revert path's whole point work end to end: after a `drift_revert` is
accepted and a human (or their own tooling) actually applies the correction
outside of ubx, the next `ubx scan` reads reality matching `FoldState`'s
already-restored truth and correctly reports no drift — not a phantom
"drifted away from the last thing we happened to see."

### `ubx revert-plan`: emits, never applies

Takes an *accepted* `drift_revert` proposal and produces the reconciliation
artifact a human (or their own tooling) needs to actually fix cloud — and
nothing more:

1. **Human-readable plan** — always produced: resource address, attribute,
   current (drifted) value → restore-to (ledger-truth) value, one line per
   changed attribute across every `delta.modifies` entry.
2. **Corrective `.tf` diff**, only if `--tf-dir` is given — reuses the exact
   same `tfwrite` machinery `ubx writeback` already uses
   (`tfwrite.FindAndApply`), just fed a `Modification` whose `after` is
   already the restore target rather than the newly-observed value; the
   function itself needs no changes; "reverse direction" describes the
   semantic meaning (moving `.tf` back toward original truth), not a
   different code path.
3. **Manual-steps section** for anything the diff machinery can't safely
   resolve on its own: an attribute whose current `.tf` expression isn't a
   literal (declined, same rule as write-back), or a resource block
   `--tf-dir` doesn't contain at all (revert can target a resource that was
   only ever adopted via `ubx scan`, never written to `.tf`). A revert with
   some literal and some non-literal attributes produces both a partial
   diff and a manual-steps entry for the rest — never one at the expense
   of the other.

**`ubx revert-plan` never writes a file, never touches cloud, and has no
`--write` flag at all** — unlike `ubx writeback`, which does apply to `.tf`
given `--write`. This isn't an oversight; it's the whole point of "revert
emits plan" (docs/plan.md M3-4): the corrective action targets *cloud*, and
applying changes to cloud is explicitly out of scope until the native
executor (§Component map) earns that trust. The command's own `--help` text
says so plainly.

### `ubx why` and revert chains

No rendering code changes were needed: `Proposal.Kind` already prints
verbatim in both the single-proposal and resource-chain views (`(drift_adopt)`
vs `(drift_revert)`), and a `drift_revert`'s non-zero blast radius
(`+0 ~1 -0`, say) already reads differently from a `drift_adopt`'s
always-zero one — the two were distinguishable at a glance the moment
`drift_revert` started carrying real data, without touching `cli/why.go`.
Covered by a new test asserting exactly that (a chain mixing `adoption`,
`drift_adopt`, and `drift_revert` entries renders each recognizably), rather
than left as an unverified assumption.

## Fleet status (UBI-17)

M1-2's other unstarted piece (docs/plan.md: "`status --drift`"): a
read-only report over every resource the ledger already knows about, not
one address per `ubx scan` invocation. `ubx status [--drift] [--stack
<name>]` is deliberately the simplest possible thing that's actually
useful across a whole fleet, reusing every mechanism this codebase already
built rather than inventing new ones.

### Discovering "every resource the ledger knows about"

Every scan-generated proposal (`adoption`, `drift_adopt`, `drift_revert`)
carries exactly one `resolution.inputs` entry naming the address it
observed (docs/schema.md's pinned cross-reference rule) — the same field
`Ledger.LastObservedHash`/`LastObservationTime`/`ProposalsForAddress`
already key off. `ubx status` walks the whole chain once
(`Ledger.Chain()`) and keeps, per distinct address seen in any
`resolution.inputs[].resource`, the *latest* proposal that touched it —
one pass, not one `Chain()` walk per address. A hand-authored proposal
with a malformed or missing address string is skipped rather than
guessed at (`core.ParseAddress`'s existing `ok` return already makes this
a non-panic, non-fatal check).

**A confirmed (not assumed) finding this surfaced**: `core.Ledger`'s own
doc comment describes it as "a per-stack append-only proposal chain," and
docs/schema.md's Ledger layout diagram roots each stack at its own
directory — but nothing in `Ledger.Head()`/`Append()` actually partitions
by `Proposal.Stack` at the storage layer. One ledger directory holds one
flat hash chain; `Stack` is just a field recorded on each proposal.
Because `GenerateProposal`/`GenerateRevertProposal` always read the
*current* head fresh via `Ledger.Head()` before building a proposal
(regardless of which stack it's for), proposals for different stacks
chain together correctly in temporal order within a single directory —
this was previously untested (every prior test and live verification used
exactly one `--stack` value per ledger directory) and is now covered by a
real multi-stack test. `--stack <name>` on `ubx status` filters the
*discovered addresses* by `Address.Stack` after the walk; it does not
change how or where the ledger itself is read. Whether one ledger
directory per stack (matching the schema.md diagram) or one shared
directory holding several interleaved stacks is the better real-world
deployment shape is a separate, later decision — both work correctly
today, and `ubx status`'s "all stacks by default" framing depends on the
shared-directory shape actually being sound, which this session is the
first to verify rather than assume.

### Ledger-only vs. `--drift`

Without `--drift`: purely a read of the ledger's own accepted history —
for each discovered address, its latest recorded kind, short proposal ID,
and acceptance time. No provider is launched, no credentials are needed,
nothing touches the network. Fast by construction, and a genuinely
different capability from `--drift`, not just a default value for it.

With `--drift`: one provider is launched (resolved exactly like `ubx
scan`'s own `--provider`/`--source`+`--provider-version`), and for every
discovered address, `core.RunScan` runs against it using the address's
own **persisted `resolution.inputs[].lookup`** — the exact reason that
field was added in the first place (docs/schema.md's UBI-7 follow-up
amendment): so a caller other than the original `ubx scan` invocation
never has to already know, or re-derive, what identifies a resource to
its provider. `RunScan`'s own comparison baseline is
`ObservedHash(FoldState(addr))` (UBI-16's correction) — the same "does
reality diverge from the ledger's own reconstructed truth" question `ubx
scan` answers, just asked for every known resource in one pass instead of
one CLI invocation per address. One provider launch/handshake serves the
whole fleet (`Configure` is still called once per resource inside
`RunScan`, same as every existing call site — accepted as a known,
bounded inefficiency at foundational-slice scale, the same posture
`FoldState`'s own doc comment already takes about its linear ledger walk,
not a design gap left to discover later).

Each resource classifies as:
- **clean** — `RunScan` reports `ScanUnchanged`.
- **drifted** — `RunScan` reports `ScanDrifted`.
- **unreadable** — anything that stops a real comparison from happening
  at all: no lookup was ever recorded for this address (a proposal
  authored before the lookup amendment existed), the provider fails to
  read it (credentials, unknown resource type, a transient failure), or
  — genuinely malformed ledger content — `resolution.inputs` names an
  address whose state `FoldState` can never reconstruct (no adoption ever
  seeded it, so `RunScan` would otherwise report the surprising `ScanNew`
  for a resource `ubx status` already knows about). **A failure on any one
  resource is recorded and the walk continues** — one unreadable or
  unknown-type resource in a large fleet must never hide the rest of the
  report.

### Exit code: the CI contract

`ubx status` is meant to gate a pipeline step, not just print a report a
human reads — so its exit code carries meaning beyond "the command ran
without crashing," the convention every other `ubx` command uses today:

- **0** — clean (or ledger-only mode, which has nothing to report drift on).
- **1** — at least one resource drifted, nothing unreadable.
- **2** — at least one resource was unreadable, or the command failed
  outright (e.g. the provider itself couldn't be resolved/launched) —
  whichever is worse always wins if both apply.

This required a small, deliberately narrow addition to how `ubx` itself
maps errors to process exit codes: `cli.ExitCodeError{Code, Err}` — a
sentinel a command's `RunE` can return to request a *specific* exit code
(with or without a message) instead of the blanket "any error means exit
1" every other command relies on via `cmd/ubx/main.go`. Every existing
command is completely unaffected: `errors.As` only matches this new type,
so a plain error from any other command still falls through to exactly
the same `os.Exit(1)` path it always has. This couldn't be `os.Exit`
called directly inside `ubx status`'s own `RunE` — this codebase's CLI
tests run every command in-process (`cli.NewRootCmd().Execute()`, see
`cli/scan_test.go`'s `runUbx`), and an in-process `os.Exit` would kill the
test binary itself, not just "the command" — so the exit-code contract
had to be a value `RunE` returns and `main.go` interprets, not a side
effect `RunE` performs, for the adversarial exit-code tests to be
possible at all as ordinary Go tests.

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
