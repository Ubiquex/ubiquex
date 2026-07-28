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

## Bulk onboarding (UBI-18)

Production ladder step 3: a team with 300 resources cannot adopt them one
`--lookup` at a time. `ubx scan --all --tfstate <path>` walks every managed
resource in a Terraform state file and generates one `adoption` proposal
per resource, reusing the *entire* existing single-resource pipeline
(`core.RunScan` + `core.GenerateProposal`) unchanged — bulk onboarding is
an orchestration layer over what already exists, not a new proposal
pipeline.

### Enumeration source: `.tfstate`, read once, never depended on again

Decided in the design room (Linear UBI-18) before any code: **the state
file is a border-crossing artifact, read exactly once, at onboarding.**
After that read, the ledger owns everything — `ubx` never opens the state
file again, never watches it, never reconciles against it. This is a
deliberate, narrow scope, not a first step toward `.tfstate` as an ongoing
source of truth (that would contradict the thesis's own "no `.tfstate`
exists anywhere" invariant — see Execution layer above): the state file's
only job here is telling `ubx` *which resources exist and how to look them
up*, once, for a team that already has a pile of resources under
Terraform management and needs a fast on-ramp into the ledger.

**Cloud-side discovery (tag-based enumeration, per-type list APIs) is
explicitly out of scope for this issue** — a different feature, a
different epic, for teams whose resources were never under Terraform
management at all. Conflating the two would have doubled this design
session's surface area for no benefit to either.

**State provides identity, never truth.** For every resource the state
file names, `ubx` builds a lookup key from state attributes and then reads
that resource live from the actual provider — the proposal's recorded
observed state comes from that live `ReadResource` call, exactly like
every other `ubx scan` invocation, never from the state file's own
recorded attribute values. A resource whose state entry is stale (edited
outside Terraform since the last `terraform apply`) still gets adopted
with its *current* reality, not last-known Terraform state — the same
"reads and records before it ever writes" posture as everywhere else in
this trust chain.

### Building a lookup from state, per type

State's own `attributes` map always includes `id`; that alone is the
correct `ReadResource` lookup for most types (the default). A handful of
types need more, per the same empirical findings already recorded in
`conformance/registry.go`'s `Notes` and pinned in ubiquex-docs'
`cli/lookup.mdx`: `aws_s3_bucket` also needs `bucket`, `aws_iam_role`/
`aws_iam_user` also need `name` — both cases where state already carries
that second attribute under its own name, so it's a matter of also
including it, not deriving it. The other `RealSafe` types with additional
`IdentityFields` (`aws_iam_policy`, `aws_sqs_queue`, `aws_sns_topic`,
`aws_vpc`) need no augmentation at all: their `id` attribute in state
already *is* the value those extra fields would have contributed (the ARN,
the queue URL), confirmed against the same empirical findings, not
re-derived from `IdentityFields` mechanically — `IdentityFields` records
which attributes carry stable identity for CloudTrail attribution
purposes (UBI-8/UBI-10), a related but distinct question from "what does
`ReadResource` need," and the two don't always coincide (`aws_sqs_queue`'s
`IdentityFields` includes `url` as a distinct field, but the lookup needs
no separate `url` key at all since `id` already equals it) — conflating
them would have silently produced a wrong lookup for exactly that type.
This is a small, explicit, separately-maintained table for that reason,
not a mechanical reading of the conformance registry's existing field.

A resource whose type the live provider's schema doesn't recognize at all
is caught by the exact same `ErrUnknownResourceType` path `ubx scan`
already has (`core.RunScan` calls the provider's own `GetProviderSchema`)
— no separate type allowlist is needed to reject it up front.

### Stack inference and module paths

`--stack` wins if given. Otherwise, every resource in the state file is
assigned to one stack, named after the state file's own basename with its
extension stripped (`prod.tfstate` → stack `prod`), falling back to the
literal `default` if that's empty or unusable. **This is a v1 decision,
made and documented rather than left implicit**: Terraform module paths
(`module.network`, `module.network.module.subnet`, ...) never become an
automatic per-module *stack* split. Modules are an authoring-time
organization concept in Terraform; conflating them with `ubx`'s own stack
concept would be a second opinion `ubx` doesn't need to have yet, and —
since `--stack` already exists as an escape hatch — not one this session
had to resolve to ship something useful. A module path shows up two other
ways instead: as a plain-text hint appended to the generated proposal's
`intent.summary` ("... (from module network)"), and folded into the
resource's own `Address.Name` (`network.web`, `network.subnet.web`) —
the latter isn't optional the way the summary hint is. Two different
modules can each declare a resource named `web` of the same type; without
folding the module path into the name, both would collide into the exact
same `ubx` address the moment they share a stack (a real "duplicate
addresses" case caught by writing the adversarial test for it, not
foreseen from the design session's own framing alone) — silently
overwriting one's proposal file with the other's, or making the second
look like it was "already known to the ledger" once the first is
accepted. Folding the module path into the name is a disambiguation `ubx`
has to perform to keep every address genuinely unique, distinct from
(and not a contradiction of) the decision not to let modules drive stack
assignment.

`count`/`for_each` instances address the same way Terraform itself does:
`<name>[<index>]` (`aws_instance.web[0]`, `aws_instance.web["us-east-1"]`)
— folded into `ubx`'s own `Address.Name` the same way module paths are,
since `Address` has no separate index concept and every instance needs
its own distinct, stable address to be adopted as its own resource.
Any address that still collides after both foldings (a genuinely
malformed or hand-edited state file, since Terraform itself never
produces two resources with the same full address) is caught explicitly
and skipped rather than silently overwriting an already-processed
resource's proposal.

### Scale: bounded memory, not streaming

`tfstate.Parse` decodes the entire state file with one `json.Unmarshal`
call — the whole file in memory at once, not a streaming/incremental
parse. Accepted deliberately at foundational-slice scale (verified against
a synthetic 1000-resource state; real teams onboarding "300 resources" per
the issue's own framing are well inside that), the same posture
`FoldState`'s own doc comment already takes about its linear, unindexed
ledger walk — a real scale problem to revisit if a state file ever
approaches sizes where whole-file-in-memory genuinely stops being
reasonable, not a design gap being silently carried forward unnoticed.

### What gets skipped, and why — the walk never aborts

Three things stop a state resource from producing a proposal, none of
them fatal to the rest of the batch:

- Its type isn't in the live provider's schema at all.
- `ReadResource` returns no state — the resource was deleted from real
  cloud since the state file was last written, an ordinary and expected
  thing to find while onboarding a team's actual, messy history.
- A lookup can't be built for it at all (state entry missing its own `id`
  attribute — genuinely malformed, not just an edge case of the
  augmentation table above).

Every skip is recorded with its address and reason in a skipped-summary,
alongside the count of proposals actually generated — the same "a failure
on one resource never hides the rest of the report" posture `ubx status`
(UBI-17) already established for its own fleet walk.

**`data` sources, `outputs`, and any non-`"mode": "managed"` entry in the
state file are ignored outright** — they aren't resources `ubx` could
adopt into a ledger (a data source is a read, not a piece of infrastructure
under management; an output is a computed value, not a resource at all).

### Batch output, and why acceptance stays out of scope

`--out-dir <dir>` writes one proposal JSON file per adopted resource, plus
a summary (counts, not full proposal bodies) to stdout. **`ubx accept`
remains per-proposal and deliberate — bulk *acceptance* is explicitly not
part of this issue.** Generating 300 proposals in one pass is a genuine
time-saver; auto-accepting 300 proposals would be exactly the kind of
authority-assumption this trust chain has refused to grant itself anywhere
else (see Business frame: "wedge reads and records before it ever
writes") — bulk-accept, if it ever exists, is a separate, later design
decision, not a default this issue backs into by omission.

**A real bug the live-verification test caught, not a hand-traced
one**: `core.GenerateProposal` sets a proposal's `parent` from
`Ledger.Head()`, read fresh — but nothing gets accepted *during* an
`--all` walk, only proposal files get written, so the ledger's real
on-disk head never moves across the whole batch. Left uncorrected, every
one of N generated proposals shared the exact same (real) parent, and
only the first one anyone tried to `ubx accept` would ever succeed — the
rest failed as parent-mismatched, discovered only once the live
end-to-end test tried to accept a second real, onboarded resource, not by
reasoning about the flow in the abstract beforehand. Fixed by tracking,
purely within the `--all` orchestration itself, what the head *will be*
after accepting every proposal generated so far in this same batch, in
order — a proposal's hash is a pure function of its content (`parent`
included, `id`/`acceptance`/`status` excluded, docs/schema.md), so this
hash is computable the moment a proposal is generated, before it's ever
written to a file or accepted by anyone. The result: a batch of N
proposal files that accept cleanly, one after another, in the order
`--all` printed them — not something the operator has to reorder or
patch by hand first.

## Config defaults (UBI-19)

Production ladder step 4: daily commands were carrying five flags
(`--stack`, `--source`/`--provider-version` or `--provider`,
`--provider-config`, `--github-repo`, `--tf-dir`) that are almost always
the same value, invocation after invocation, for one project. `.ubx/config`
lets a team set them once.

### Format: TOML, not YAML

Both were on the table; TOML won for reasons that matter specifically
here, not as a general preference: its grammar has no significant
whitespace and no implicit type coercion (YAML's well-known `no`/`yes`,
`on`/`off`, and bare-Norway-problem string-vs-boolean ambiguities have no
TOML equivalent — every value's type is unambiguous from its own
syntax). That's a direct match for "determinism is a feature"
(docs/schema.md, CLAUDE.md) applied to a new surface: a config file whose
meaning could shift depending on quoting style is exactly the kind of
ambiguity this project has refused to accept anywhere else. TOML is also
comment-native (so is YAML) and precedented for exactly this role in
other developer tooling (`Cargo.toml`, `pyproject.toml`) — a config file a
human is expected to hand-edit, not a data interchange format. Parsed with
`github.com/BurntSushi/toml`, the first non-Go-standard-library dependency
this project has added purely for config parsing.

### Discovery: nearest `.ubx/config` wins

Resolution starts at the current working directory and walks upward
through parent directories, stopping at the first `.ubx/config` found —
the same convention `.git` itself uses to find a repository root.
Deliberately *not* tied to `--ledger-dir`: a project's defaults (which
provider, which region, which stack) are a property of *where the
operator is standing* when they run `ubx`, not of wherever `--ledger-dir`
happens to point (which can legitimately be a completely different path,
e.g. shared ledger storage outside the project checkout). No config
file anywhere up to the filesystem root is not an error — every value
config could have supplied simply falls through to its own flag's
ordinary default, or to "required and absent," per command.

### Precedence: CLI flag, then config, then required-and-absent is an error

For every flag config can supply, the rule is fixed and applies
uniformly: an explicitly-passed CLI flag always wins (checked via cobra's
own `cmd.Flags().Changed(...)`, not by comparing against a zero value —
a flag explicitly set *to* its zero value must still count as "explicitly
given," the same discipline `ubx scan`'s existing `--stack`/`--type`/
`--name` validation already uses); otherwise config fills the gap if it
has a value; otherwise, whatever the flag's own existing "is this
required" rule already says applies unchanged — config is a second place
to find a value, never a new rule about which values are required. `ubx
scan --all`'s own filename-derived stack default (UBI-18) sits *after*
config in this chain: CLI flag, then config, then the state file's own
basename, matching how each layer only ever fills a gap the one before it
left open.

### What it covers, and what it deliberately doesn't

Five keys, matching the five flags this session set out to stop
repeating: a `[provider]` table (`path`, or `source`+`version` — the same
mutual exclusivity `ubx scan`'s own flags already enforce), a
`[provider_config]` table (freeform, marshaled straight to JSON and handed
to the exact same `--provider-config` string every provider-touching
command already accepts), `stack`, `github_repo`, and `tf_dir`.
**`--ledger-dir` is deliberately not one of them** — not an oversight,
the issue's own scope named exactly these five, and a ledger's location
is arguably a more consequential default to get silently wrong than the
others (a wrong provider region fails loudly on the first read; a wrong
ledger directory could make a command silently look at the wrong ledger
entirely). If that ever needs to change, it's a separate, deliberate
decision, not a quiet scope-creep of this one.

### Unknown keys warn, they don't fail

A config file's own forward/backward compatibility matters more than
strict schema enforcement here: an unrecognized key (a typo, a field from
a future `ubx` version, a field a downgraded `ubx` no longer knows about)
is reported as a warning naming the exact key, and otherwise ignored —
never a hard failure that would block every command in a project until
someone fixes a typo. `github.com/BurntSushi/toml`'s own `MetaData.Undecoded()`
makes this a direct, no-guesswork check: every key the decoder didn't
assign to a known struct field, named exactly. A config file that isn't
valid TOML *syntax* at all is a different matter — that's reported as a
hard error, since there's no partial, best-effort way to read a file that
doesn't parse.

### `ubx init`: a new verb, not an extension of an existing one

No `init`-shaped command existed before this session. `ubx init [--dir]
[--force] [--stack] [--source] [--provider-version] [--provider]
[--provider-config] [--github-repo] [--tf-dir]` writes `.ubx/config`
(refusing to overwrite an existing one without `--force`, the same
"never silently destroy what's already there" posture as everywhere else
in this trust chain) — every key the operator supplies a flag for is
written as a real, active value; every key they don't is written as a
commented-out example showing the correct syntax, so the file is
immediately useful as its own documentation of what's possible, not just
a blank template.

## Hardening pass (UBI-20)

Production ladder step 5, "the credibility layer": four workstreams that
don't add a new capability so much as make the existing ones trustworthy
to script against, debug, and run concurrently. Each is independently
shippable; each gets its own commit.

### 1. Exit-code contract, everywhere

`ubx status` (UBI-17) established 0/1/2 for one command. This workstream
extends the same three-way split to every verb, as a single documented
contract:

- **0 — success, nothing to flag.** The operation completed and found
  nothing that needs a human's attention beyond its own normal output.
- **1 — an actionable finding.** The operation completed *correctly*, but
  surfaced something a human should look at: drift found (`scan`), a
  stale reverify block (`accept --reverify-*`), a rejected PR-merge
  acceptance (`accept --from-merge`'s hash mismatch or a merge commit no
  longer in history), a `--verify-acceptance` check that no longer holds,
  a `writeback`/`revert-plan` that had to decline an attribute. None of
  these are the tool malfunctioning — they're the tool doing its job and
  reporting something real.
- **2 — an error.** Bad input, a missing/malformed file, a provider that
  couldn't be launched or resolve a resource, a ledger it couldn't append
  to, malformed config. The operation could not be completed as asked.

**This changes what a plain (unclassified) error means.** Before this
session, every command funneled any returned error through
`cmd/ubx/main.go`'s single fallback, `os.Exit(1)` — so "exit 1" meant
nothing more specific than "something went wrong." Under the new
contract that's `cli.ExitCodeError`'s job everywhere: commands return an
explicit `ExitCodeError{Code: 1, ...}` at the specific points listed
above, and the fallback itself moves to `os.Exit(2)` — a plain, unclassified
error is now unambiguously "an error," never confusable with "a finding."
This is a deliberate breaking change to what "exit 1" has meant for every
command except `status` since this project began, made explicitly rather
than silently, and documented as the CI contract (`docs/plan.md` and
ubiquex-docs' own exit-codes reference) — anyone scripting against the
old "any error is exit 1" convention needs `exit 2`, not `exit 1`, for
that check going forward.

Every verb was audited against this, not just the ones that needed new
`ExitCodeError` call sites: `version`, `init`, and `propose` have no
"finding" concept at all and stay 0/2-only, which is a complete audit
outcome, not an oversight.

### 2. `--json` on `scan`, `status`, `why`

Stable, machine-readable output for the three verbs a CI pipeline is most
likely to parse. Human output stays the default and is unchanged;
`--json` switches the *entire* stdout stream to one JSON document (or, for
`why`'s chain view, one JSON array) instead of the existing text format —
never a mix of the two on one invocation.

Every JSON payload carries a top-level `"format": 1` field — a schema
version, not a product version, bumped only if the shape of that verb's
JSON output changes incompatibly. A consumer checks `format` once and
knows exactly what shape to expect for the rest of the fields; a future
incompatible change bumps it rather than silently changing meaning under
an unversioned consumer's feet.

`why --json` emits a resource's full proposal chain as structured data —
an array of proposal objects (newest first, matching the human view's own
order) — not a bare re-serialization of one proposal's own JSON with no
wrapping (which would give a consumer no reliable way to distinguish "one
proposal" from "a chain," or to find the format version at all).

### 3. Teaching errors: promote the lookup data, not the package

`ubx scan`'s "provider returned no state" (`core.ErrResourceUnreadable`)
is the single most likely first real error a new user hits — sent a
lookup missing the field a type actually needs (docs/architecture.md's
own Bulk onboarding section, and `cli/lookup.mdx`, already document this
per-type shape). The fix flagged during demo dry-run: name the likely
missing field *in the error itself*, not just in a docs page the user has
to already know exists.

The knowledge already exists — `conformance/registry.go`'s per-type
`Notes` — but that package is explicitly test-only tooling ("this is
project-internal tooling, not shipped product code — it lives outside
core/ and cli/ deliberately," its own doc comment). Importing it into
`cli/`/`core/` (shipped product code) would make a test harness a runtime
dependency of the binary, exactly the boundary that comment exists to
hold. **The data is promoted, not the package**: `Notes` is free prose,
not mechanically generatable from, so `TypeSpec` gained a new structured
field, `LookupHint`, populated only where a live `ReadResource` round
trip actually confirmed the failure mode (not assumed from Notes' text) —
`conformance/gentool` (a `go generate`-invoked generator, never imported
by anything else) reads `LookupHint` and writes a small, shipped table,
`core/lookuphints/hints.go`, committed as ordinary Go source with no
runtime dependency on `conformance/` at all.

Only three types get this: `aws_s3_bucket`, `aws_iam_role`, `aws_iam_user`
— the ones where the mistake is a genuinely missing field, not just a
surprising id value. (`cli/lookup.mdx`'s other four "confirmed
non-default lookup shape" types — `aws_iam_policy`, `aws_sqs_queue`,
`aws_sns_topic`, `aws_vpc` — use an ARN/URL/vpc-id as `id` and need
nothing else; that's a "use the right value" surprise, not a "you're
missing a field" one, and isn't safe to promote as a shipped hint without
a plausible specific fix to name.) `ErrResourceUnreadable`'s error
message gains the hint and a link to `cli/lookup`'s docs page for these
three; every other type gets an honest "check the provider's schema"
fallback rather than a fabricated guess.

**Live verification caught the hint direction backwards before it
shipped.** The Notes prose for these three types reads "id and bucket/
name are both the natural key; lookup needs BOTH set" — which, read
without actually calling `ReadResource`, could suggest either half is the
"missing" one. A real scan against the "ubx-states" bucket
(`conformance/lookuphints_live_test.go`) proved `{"id": "..."}` alone
succeeds, and `{"bucket": "..."}` alone (the type's own natural,
Terraform-attribute-shaped key — an easy thing to reach for instead of
`id`) reads back null. So the hint teaches "make sure `id` is included,"
never "you're missing bucket/name" — the opposite of the first draft.

### 4. Ledger lock

A per-ledger-directory lockfile (`.ubx/lock`, alongside the existing
`.ubx/config` and `.ubx/ledger.lock` — three different files, three
different purposes, deliberately not conflated) makes concurrent `ubx`
processes against the same ledger directory safe — a person running
`ubx accept` locally while CI runs `ubx scan --all` against the same
ledger is exactly the scenario this exists for. Acquired around every
ledger-mutating operation (`Accept`/`AcceptFromMerge`'s append,
specifically — read-only operations like `scan`'s comparison, `why`, and
`status` don't need it, since they never write). A blocked process waits
briefly (a short, bounded retry window — this is lock *contention*
between two legitimate, cooperating processes, not a hang to paper over)
and then fails clearly with `ExitCodeError{Code: 2}` naming the lock file
and, where determinable, the PID holding it.

**Stale-lock detection, not just stale-lock avoidance**: a lock file
whose recorded PID no longer corresponds to a running process (the holder
was killed, not released cleanly) is detected and reported with explicit
recovery guidance (remove the file) rather than either blocking forever
or silently breaking the lock — a silently-broken lock would defeat the
entire point for the one case (a genuinely still-running process) it
exists to protect against.

**Mechanism: a PID file, not a bare OS-level `flock(2)`.** A real
`flock()` is automatically released by the kernel the instant a holding
process dies, for any reason including a hard kill — which would make
"stale lock from a killed process" invisible rather than a real, testable
scenario: the very next contender's retry would just succeed silently,
with nothing left to detect. Writing the holder's own PID into the lock
file, and checking that PID's liveness (`os.FindProcess` +
`Signal(syscall.Signal(0))`, a pure existence probe, not an actual
signal) when contended, keeps "confirmed dead, here's how to recover"
observable as its own outcome rather than folding it into ordinary
contention.

## GCP support (UBI-21)

The wedge has been AWS-only through every prior session (UBI-7 through
UBI-20). UBI-21 is the first cross-provider generalization — not a new
capability so much as a check that the trust chain's own abstractions
(`core.StateReader`, `core.EventLookup`, the conformance registry) were
actually provider-agnostic, not AWS-shaped and merely undocumented as
such. Two design decisions, made before any code:

### 1. Identity stays opaque; the KNOWLEDGE layer generalizes

`core.Address`/`--lookup` do not gain a "provider" field. A resource's
identity to `ubx` is still exactly what it always was: a stack, a type
name (e.g. `aws_s3_bucket` or `google_storage_bucket`), a name, and an
opaque provider-agnostic lookup JSON `core.RunScan` hands straight to
`ReadResource` without interpreting. Nothing about `core.ScanResult`,
`Proposal`, or the ledger format changes for this reason — a second
provider is exactly the kind of thing this boundary was supposed to
absorb without a schema change, and it does.

What DOES change is the KNOWLEDGE this project keeps about specific
types — `conformance.Registry` (per-type identity fields, quirks,
lookup hints) and its generated shipped table, `core/lookuphints`
(UBI-20 workstream 3). Both were keyed by bare type name alone, an
implicit "there's only one provider" assumption that a second provider
would make load-bearing rather than academic: nothing today actually
requires `aws_*`/`google_*` prefixes to stay collision-free forever, and
even where names don't collide, a single flat table conflates two
providers' identity knowledge as if it were one namespace. Both are now
keyed by **(provider source, type)** — `"hashicorp/aws"`/`"aws_s3_bucket"`
as one entry, `"hashicorp/google"`/`"google_storage_bucket"` as a
distinct one — see `conformance.TypeSpec.Source` and
`core/lookuphints.For(source, resourceType string)`.

This does mean `core` needs to know a scan's provider source to look up
a teaching-error hint for it — previously nothing in `core` needed to
know anything about provider identity at all. `ScanRequest` gains an
optional `ProviderSource` field (e.g. `"hashicorp/google"`), populated by
the CLI from whichever of `--source`/`--provider` was used. When
`--provider <path>` (a raw binary, no registry source known) is used
instead, `ProviderSource` is simply empty and the teaching-error path
falls through to its existing honest "check the provider's schema"
fallback — exactly the same fallback an unrecognized *type* already gets,
now also covering an unrecognized *source*. This is a real, accepted
narrowing (a raw `--provider` invocation gets a slightly less specific
error than a `--source` one), not an oversight: inferring provider
identity from the schema itself (e.g. guessing from type-name prefixes)
would be exactly the kind of "fabricated guess dressed up as a known
fact" the teaching-error feature was built to avoid in the first place.

### 2. Attribution backends are per-platform packages behind `EventLookup`

`core.EventLookup` (UBI-10) was already provider-agnostic in shape — one
method, `LookupEvents(ctx, resourceID string, since, until time.Time)
([]CloudTrailEvent, error)` — and CloudTrail-specific only in naming
(`CloudTrailEvent`, `cloudtrail`/`cloudtrail_unattributed` kind
literals, `ActorARN`). The decision: keep the interface, add a second,
independent implementation — a new `gcpaudit/` package implementing
`EventLookup` against GCP's Cloud Audit Logs (via Cloud Logging's
`entries.list`, correlating by resource name the same way `cloudtrail/`
correlates by ARN/id) — plus a small registry mapping provider source
(`"hashicorp/aws"` → `cloudtrail/`, `"hashicorp/google"` → `gcpaudit/`)
so `ubx scan` picks the right backend for whatever it's scanning,
exactly the same shape as `conformance.Registry`'s own (source, type)
generalization above.

**`docs/schema.md` gains a purely additive amendment**: two new
`intent.sources[].kind` values, `gcp_audit` (a matched event, GCP's
counterpart to `cloudtrail`) and `audit_unattributed` (generalizing
`cloudtrail_unattributed` with a new `backend` field,
`"gcp_audit_logs"` today). `cloudtrail_unattributed` remains a
permanently valid kind, and `cloudtrail.Backend` keeps emitting it
unchanged — no migration, no `schema_version` bump, same purely-additive
discipline every prior schema.md amendment has followed. See
docs/schema.md's own amendment for the full writeup, including a real
implementation decision this document originally left open: whether
`gcp_audit` would need GCP-specific fields, or could reuse `cloudtrail`'s
existing ones. It reuses them (`actor_arn` carries the GCP principal's
email, not an ARN) — decided once `gcpaudit/` was actually built and
both backends turned out to produce the exact same `core.CloudTrailEvent`
shape, not assumed in advance.

**`EventLookup`'s single-method interface held up completely** —
`gcpaudit/` implements it against Cloud Logging's `ListLogEntries` with
no interface changes at all, confirming the "already provider-agnostic"
read above rather than requiring a stop-and-flag reshape. What did
surface a real, unanticipated gap: **which value a GCP service's own
audit log entry uses to name the affected resource is not consistent
across services**, in a way `core.AttributeDrift`'s existing
`identityCandidates` (id/arn/name from the resource's own observed
state) can't always bridge. Confirmed live: Pub/Sub's audit entries name
a topic using the project *ID* (`projects/<PROJECT_ID>/topics/<name>`,
matching `google_pubsub_topic`'s own observed `id` exactly — correlation
works), but Secret Manager's entries instead use the numeric project
*number* (`projects/<PROJECT_NUMBER>/secrets/<name>`) — a value that
never appears anywhere in `google_secret_manager_secret`'s own observed
state, so every secret's drift is silently unattributable via this
backend today (indistinguishable from a genuine no-event case). This is
flagged, not silently resolved — see `gcpaudit/client.go`'s own doc
comment and STATE.md. It needs either a per-service resourceName-shape
table or a project-number lookup added as a correlation candidate;
neither was built under time pressure here.

### Stage 1 (this session, hermetic — no GCP account needed)

- The (provider, type) keying refactor above, across
  `conformance/registry.go`, `core/lookuphints`, and
  `core/scan.go`'s teaching-error path. Every existing AWS-only test
  stays green — this is a refactor of the KEY, not the DATA; no AWS
  entry's `IdentityFields`/`Notes`/`LookupHint` content changed.
- The provider layer verified empirically against `hashicorp/google`
  via `provider.Acquire` (same acquisition path `hashicorp/aws` already
  uses, registry.opentofu.org, checksum-verified) — schema pull,
  protocol handshake. **Empirical finding**: `hashicorp/google` 7.40.0
  negotiates tfplugin **v5**, the same version `hashicorp/aws` was found
  to speak in Slice 1 — dual v5/v6 support earns its keep a second time,
  not just accepted on faith that "some future provider might need v6."
- `conformance.Registry` gains ~40 `hashicorp/google` `TypeSpec` entries
  (`Safety: FakeOnly`, `Implemented: false` — mirroring UBI-9 session 1's
  own bootstrapping precedent for AWS exactly: seed the list first, work
  through it in later batches), spanning six categories with the same
  "real GCP shop" bias the AWS list used: compute (`google_compute_instance`
  and friends, plus Cloud Run/GKE/Cloud Functions as GCP's own
  compute-adjacent surface), network (VPC/subnet/route/router/NAT/
  firewall/addresses/forwarding rules/backend services), IAM (service
  accounts and keys, project IAM bindings/members, custom roles),
  storage (bucket and its IAM/object sub-resources, persistent disks,
  Filestore), SQL/database (Cloud SQL instance/database/user, Spanner,
  Firestore), and DNS (managed zone, record set, SSL certificate), plus
  messaging/observability/secrets (Pub/Sub topic/subscription, log-based
  metrics, alerting policies, Secret Manager, KMS). `IdentityFields` for
  every entry come from a real `GetProviderSchema` call against the
  acquired `hashicorp/google` binary — free, no credentials, no live GCP
  API round trip, the same "verified against the real schema, not
  assumed" standard `conformance.Registry`'s AWS entries already hold to.

### Stage 2 (needed a real GCP project, credentials, and Cloud Audit Logs
enabled — done this same session, once Roozbeh set up Application Default
Credentials against his own `personal-273114` project)

- Five types promoted to `RealSafe` and live-verified via the same
  adopt→mutate→scan-diff harness (`conformance.RunAdoptMutateScanDiff`)
  UBI-9's AWS batches used: `google_storage_bucket`, `google_pubsub_topic`,
  `google_service_account`, `google_secret_manager_secret`,
  `google_project_iam_custom_role`. Real per-type lookup-shape findings,
  each the kind of thing only a live `ReadResource` call surfaces:
  - `google_service_account`/`google_project_iam_custom_role`: `id` alone
    (the full resource path) is sufficient, matching `aws_iam_policy`'s
    own shape.
  - `google_storage_bucket`: `id` and `name` are BOTH required together —
    neither alone works (`id` alone errors outright; `name` alone reads
    back null). The opposite direction from `aws_s3_bucket`, where `id`
    alone already works — confirming this genuinely needed a live check
    per cloud, not an assumption ported from AWS.
  - `google_pubsub_topic`/`google_secret_manager_secret`: a materially
    more dangerous shape than anything AWS showed. `id` alone doesn't
    error and doesn't read back null — `ReadResource` succeeds, but the
    resource's own natural-key attribute (`name`/`secret_id`) comes back
    empty/null anyway. `core.ErrResourceUnreadable` never fires, so this
    silently ledgers an incomplete proposal rather than erroring — the
    UBI-20 teaching-error mechanism can't help here at all, since it only
    engages on an actual read failure. None of these three types were
    given a `LookupHint` / promoted into `core/lookuphints` for exactly
    this reason: that mechanism's message hardcodes "make sure `id` is
    included," which would be actively wrong advice for
    `google_storage_bucket` (where `id` alone is often what the user
    already has) and can't fire at all for the silent-incomplete-read
    cases. Generalizing the teaching-error mechanism to express "both
    required together" and "silently incomplete, not just unreadable" is
    real follow-up work, not attempted under time pressure here.
  - GCP IAM's own read-after-write consistency lags the write itself,
    confirmed live: a `google_service_account` display-name update was
    visible via `gcloud describe` before it was visible to the
    Terraform provider's own `ReadResource` call — two different read
    paths becoming consistent at different times, not one moment.
- `gcpaudit/` implemented and live-verified: a real drift on a real
  Pub/Sub topic, correlated against Cloud Audit Logs with the actual
  caller's real GCP account email recorded, via the actual `ubx scan`
  command end to end — not a synthetic fixture, the same "prove it
  against a real account, not just a fake" bar `cloudtrail/` was held to
  in UBI-10. Cloud Audit Logs' own delivery latency was measured
  directly: a Pub/Sub `CreateTopic` admin-activity entry became queryable
  roughly 18 seconds after the API call returned — far faster than
  CloudTrail's measured ~2 minutes, and with no GCP-published ceiling
  the way CloudTrail has a documented ~15-minute one.
  `gcpaudit.Backend.DeliveryLag` is set to 3 minutes, a safety margin
  above that one measurement, not tuned tightly to it.
- Every fixture created for live verification was destroyed afterward
  (confirmed via a post-session sweep of every resource type touched),
  the GCP project left exactly as found — same discipline every AWS
  `RealSafe` conformance test already follows.

## Secrets (UBI-23)

"Every infra change is a typed, hashed, signed proposal recorded in an
append-only per-stack ledger" (this document's own thesis) is in tension
with a provider schema's own `Sensitive` flag the moment a resource with
real secret material — a DB password, an API key, a TLS private key — gets
adopted or drift-recorded: the ledger is forever, git history is forever,
and a secret that enters either is compromised the moment anyone else ever
clones the repo. UBI-23's whole job is closing that gap without weakening
what the ledger is for: still able to *detect* that a sensitive value
changed, never able to *reveal* what it changed to or from.

### The mechanism: redact at the read boundary, before core ever sees it

`core` (this project's proposal/ledger/hashing layer) deliberately has no
dependency on `provider` and no schema knowledge at all — `StateReader`'s
`resourceSchema any` is intentionally opaque to it (see `core/scan.go`'s
own doc comment). That boundary is preserved, not bent, for UBI-23:
redaction happens in the one layer that already sits between the two and
already holds the concrete provider schema type before it gets
type-erased to `any` — the `core.StateReader` adapters
(`cli/stateadapter.go`'s `stateReaderAdapter`, `conformance/harness.go`'s
own copy). Each adapter's `ReadResource` calls the real
`provider.Client.ReadResource` exactly as before, then passes the result
through a new pure function, `provider.Redact(block provider.Block, salt
[]byte, observed json.RawMessage) (json.RawMessage, error)`, before
returning it to `core`. By the time `RunScan`/`VerifyFreshness`/
`GenerateProposal` ever touch observed state, every `Sensitive`-flagged
attribute is already in `$redacted` form (docs/schema.md's amendment) —
`core` never learns what "sensitive" means, it only recognizes a JSON
shape convention that's already there, the same posture `IntentSource.Kind`
string literals already establish across `cloudtrail`/`gcpaudit`/`core`
without a shared Go symbol.

`provider.Redact` walks `Block.Attributes` (any attribute with
`Sensitive: true` gets its whole value replaced, regardless of that
attribute's own type — a scalar, a list, a map, or an object-typed
attribute expressed via the schema's own `Type` field all redact the same
way, wholesale, since a cty attribute type carries no per-sub-field
sensitivity of its own) and recurses into `Block.NestedBlocks`, dispatched
on `NestingMode` exactly as `ctyvalue.go`'s `blockObjectType` already does
for encoding: `Single`/`Group` → the nested value is a bare JSON object;
`List`/`Set` → an array of objects, walked per element; `Map` → an object
keyed by string, walked per value.

### Verified against real provider schemas, not assumed

The design's own instruction was to verify nesting shapes against real
providers rather than assume from the schema alone. A throwaway
introspection tool (acquire the real binary via `provider.Acquire`, launch
it, call `Schema(ctx)`, walk `Block.Attributes`/`Block.NestedBlocks`
counting `Sensitive` flags by depth) against both integrated providers
found nested sensitivity is common, not a hypothetical edge case:

- `hashicorp/aws` 6.54.0: 131 top-level, 115 nested `Sensitive` attributes,
  nesting as deep as 4 levels (e.g.
  `aws_appflow_connector_profile.connector_profile_config.connector_profile_credentials.slack.access_token`).
  A whole `List`-typed attribute marked sensitive as one unit also occurs
  in practice (`aws_elasticache_user.authentication_mode.passwords`),
  confirming "redact the whole attribute value regardless of its own
  type" is a real requirement, not over-engineering. Non-obviously-secret
  field names get flagged too (`aws_quicksight_data_source`'s
  `credentials.credential_pair.username`, alongside its sibling
  `password`) — redaction must never assume "sensitive" means only the
  classically-named secret fields.
- `hashicorp/google` 7.40.0: 46 top-level, 207 nested — proportionally
  even more nested than AWS, up to 3 levels
  (`google_datastream_connection_profile.postgresql_profile.ssl_config.server_and_client_verification.client_key`).

Both confirm the existing `Block`/`Attribute`/`NestedBlock` model (built
for cty-msgpack encode/decode, UBI-7) already correctly surfaces every one
of these, via `BlockTypes`-based nesting — no schema-translation change
was needed to make them reachable, only the new `Redact` walk that mirrors
the same recursion.

**A real gap was investigated and found not to apply, rather than assumed
away**: `tfplugin6.Schema_Attribute` has a `NestedType *Schema_Object`
field (the modern terraform-plugin-framework nested-attribute mechanism)
that `blockFromV6`'s translation has never read — a `Sensitive` flag
living only inside a `NestedType` structure would be invisible to any
redaction built on top of `Block`/`Attribute` as they stand today. This
was checked directly (a throwaway probe reading the raw
`tfplugin6.GetProviderSchema` response, bypassing the lossy translation
entirely) rather than left as a caveat: **both `hashicorp/aws` and
`hashicorp/google` negotiate wire protocol v5** (confirmed live, matching
this project's own standing "Dual v5/v6, not v6-only" finding under
Execution layer above — real provider binaries, including modern
framework-native ones, serve v5 on the wire even when v6 is available),
and `tfplugin5.Schema_Attribute` has no `NestedType` field at all — it's
strictly a v6 wire concept. `NestedType` is therefore architecturally
impossible to encounter with either provider `ubx` integrates with today.
This is scoped out honestly, not silently: if `ubx` ever integrates a
provider that negotiates v6, `blockFromV6`'s translation would need
extending to also read `NestedType` before redaction could claim the same
completeness it has today. Not needed now; flagged for whoever adds the
first v6-negotiating provider.

### The salt: per-ledger, generated on first use, never committed

A redacted value's whole point is still detecting change without
revealing material — `sha256(salt || canonical-value-bytes)` (docs/schema.md's
`$redacted` amendment) needs a salt so a redacted hash isn't just an
unsalted fingerprint an attacker could dictionary-attack against likely
secret values. The salt is ledger-directory-scoped (`.ubx/salt`, alongside
the existing `.ubx/config`/`.ubx/ledger.lock`/`.ubx/lock` — a fourth,
distinct file), generated via `crypto/rand` on first call to
`Ledger.Salt()` and persisted at `0600`. `Ledger.Salt()` also ensures a
`.gitignore` entry for `.ubx/salt` exists in the ledger directory
(creating a minimal one if none exists, appending the line if one exists
and lacks it) — a safety net against an operator's own `git add -A` habit,
not the only line of defense (the file is `0600` and never itself becomes
part of any hashed/ledgered content regardless).

**Why the salt itself lives outside `core`'s dependency-inversion
boundary concerns**: `Ledger.Salt()` is a `core.Ledger` method (core
already owns `.ubx/`-relative file management — see `core/lock.go`'s
identical `.ubx/lock` pattern) — but core never calls it itself. The
salt is threaded through by the CLI/conformance adapter layer (the same
layer that owns calling `provider.Redact`), read once per command
invocation and passed to the adapter's constructor, alongside the
already-established `newStateReader(p provider.Provider, salt []byte)`
signature.

**Recovery implication, stated honestly rather than glossed over**: losing
or regenerating the salt makes every subsequently-computed `$redacted`
hash for an unchanged real secret differ from what's already in ledger
history — the next scan reports it as drifted. This is a false positive,
not a missed detection: it degrades equality comparison across history,
it never causes real material to leak (nothing about the salt's loss
touches what gets written anywhere), and it never makes a genuine change
go undetected (a scan always re-reads live state fresh). See
docs/schema.md's amendment for the full statement.

### What every affected command does differently

- **`scan`/`scan --all`/drift/revert (proposal generation)**: nothing
  explicit changes in `core/scan.go` itself — `RunScan`'s `ScanResult.Observed`
  is already redacted by the time core sees it (the adapter boundary
  above), so `GenerateProposal`/`GenerateRevertProposal` construct
  `delta.creates`/`delta.modifies` from already-safe data without knowing
  it. The one real `core` change: `core/state.go`'s `diffObjects` (the
  recursive engine behind `diffAttributes`, hence every drift/revert
  delta) now recognizes a `$redacted`-shaped object and treats it as
  atomic rather than recursing into it — otherwise a changed secret would
  diff as `attr.$redacted.sha256: <hash1> -> <hash2>`, technically
  accurate but the wrong granularity (docs/schema.md's amendment has the
  full reasoning). `FoldState` needed no changes at all: it's pure
  dot-path JSON manipulation, and a `$redacted` object is just a value
  like any other from its perspective.
- **`writeback`**: `tfwrite.ApplyModification` declines any
  `Modification.After` dot-path whose value is `$redacted`-shaped,
  *before* attempting to resolve or render it — never handing a redacted
  marker to `hclwrite.TokensForValue`, which would otherwise happily
  render `{ "$redacted" = { "sha256" = "..." } }` as a literal into a real
  `.tf` file. This is the one guarantee that must hold absolutely: a
  redacted attribute is reported in the same `Declined` mechanism an
  expression-valued attribute already uses, with a reason naming it as
  redacted and pointing at manual restoration, never a rendering attempt.
- **`revert-plan`**: reuses the same `tfwrite.ApplyModification` decline
  path for its `--tf-dir` diff (so a redacted attribute lands in its
  existing "manual steps" section automatically), and its plain-text plan
  (`printPlan`, always printed regardless of `--tf-dir`) renders
  `(redacted)` in place of the raw `$redacted` JSON for both the current
  and restore-to value — visible as "this changed" without ever printing
  the hash inline next to a human-readable attribute name.
- **`why`**: renders `(redacted)` for any modified attribute whose
  before/after value is `$redacted`-shaped, reusing the same rendering
  rule `revert-plan` uses.
- **`scan --all` (bulk onboarding)**: the batch summary now reports how
  many attributes were redacted across the whole walk, alongside the
  existing adopted/skipped counts — visibility that adoption is quietly
  doing the right thing with sensitive fields, not a silent side effect.
- **`--json` (`scan`/`why`/`status`)**: no code changes needed. Every
  `--json` payload already marshals the real `*core.Proposal` structure
  directly; since redaction already happened upstream of everything that
  builds a `Proposal`, the JSON a script consumes contains the same
  `$redacted` markers the human view describes in words — never raw
  material, and never a separate code path that could diverge from the
  human view's own safety guarantee.

### A deliberate, checked scope boundary: `resolution.inputs[].lookup` is never redacted

Writing the adversarial test for this exact area surfaced a real question,
not a hypothetical one: `resolution.inputs[].lookup` (the UBI-7 follow-up
amendment — the JSON object passed to `ReadResource` to identify a
resource, persisted so a later `ubx accept --reverify-with`/
`--reverify-source` can re-read the same resource without the caller
re-supplying it) is populated directly from `ScanRequest.CurrentState` in
`core.RunScan`, completely independent of the adapter-layer redaction path
above — it is never passed through `provider.Redact`, deliberately.

An initial version of the adversarial test put the fake sensitive
attribute's value directly into `--lookup` (to double-check redaction
covered "every place a value could end up," per the task's own framing)
and caught it immediately: the raw value showed up in
`resolution.inputs[].lookup`, unredacted, in the generated proposal file.
Rather than silently loosening the test or reflexively redacting the
lookup field too, this was checked against real schemas first: **across
every type in `conformance/registry.go` — both AWS and GCP, everything
this project has empirically verified `LookupHint`/`IdentityFields`
for — no identity/lookup-worthy attribute is ever `Sensitive`-flagged.**
Real lookup keys are `id`, `name`, `arn`, `bucket` — never a password, key,
or credential. This matches the same lesson the live schema introspection
above already established: `Sensitive` marks credential-shaped material,
which no real provider ever also needs to *locate* a resource by.

Given that, redacting `lookup` unconditionally would cost real capability
(a redacted marker can't be re-supplied to a live `ReadResource` call as a
working identifier — `VerifyFreshness` would break) to guard against a
scenario no real provider schema actually produces. The scope boundary is
therefore: **`ubx` never redacts `resolution.inputs[].lookup`; a caller
who deliberately supplies real secret material via `--lookup` anyway (not
something any real schema requires) is persisting a value they already
typed into the command themselves — categorically different from `ubx`
observing and recording something from the live resource without the
caller's own action already putting that material in their hands.** The
adversarial test that caught this (`cli/redact_test.go`) was corrected to
reflect the realistic case — `--lookup` carrying only `id` throughout,
the sensitive value originating from the (simulated) live read instead —
and now asserts the persisted lookup is exactly `{"id": "..."}`, with no
trace of the secret, as a permanent regression check on this boundary.

## Kubernetes support (UBI-22)

The wedge has been cloud-provider-shaped (AWS, GCP) through every prior
session. UBI-22 is the first genuinely different KIND of provider:
`hashicorp/kubernetes` and `hashicorp/helm` don't manage cloud
infrastructure at all — they manage objects inside an already-running
cluster (Deployments, Services, Secrets) and chart releases on top of it.
The question this section answers is how much of that difference is real
(needs new mechanism) versus apparent (the existing (provider, type)-keyed
machinery already generalizes).

### 1. Identity stays exactly as generalized in UBI-21 — no new mechanism

`conformance.Registry`/`core/lookuphints` are already keyed by (provider
source, type), not bare type name (UBI-21) — `hashicorp/kubernetes` and
`hashicorp/helm` are simply two more values that key can take. No change
to `core.Address`, `core.ScanRequest`, or the lookup-hint machinery itself
was needed or made.

**A real, empirically-confirmed nesting shape worth naming explicitly**:
every `kubernetes_*` resource type checked (`hashicorp/kubernetes` 2.35.1 —
`kubernetes_secret_v1`, `kubernetes_deployment_v1`, `kubernetes_service_v1`,
`kubernetes_namespace_v1`, `kubernetes_stateful_set_v1`,
`kubernetes_daemon_set_v1`, `kubernetes_cluster_role_v1`,
`kubernetes_cluster_role_binding_v1`, `kubernetes_role_v1`,
`kubernetes_role_binding_v1`, `kubernetes_service_account_v1`,
`kubernetes_persistent_volume_claim_v1`, `kubernetes_config_map_v1`,
`kubernetes_ingress_v1`) models its `metadata` block (and, for workload
types, `spec`) as `NestingList` — a real SDKv2-era "one-item list simulates
an optional single block" convention — not `NestingSingle`, which every
AWS/GCP `NestedBlock` checked in this project so far uses for an
analogous "exactly one of these" relationship. This was checked directly,
not assumed: `timeouts` (also present on several of these types) IS
`NestingSingle`, confirming the List-shape on `metadata`/`spec` is a real,
type-specific schema choice, not a blanket difference between providers.

Practical consequence for OBSERVED STATE: `name`/`namespace`/`uid` — the
values `ubx` would otherwise expect as flat top-level attributes
(matching every AWS/GCP type documented in `cli/lookup.mdx` so far) —
live inside `metadata[0].name` etc. in whatever a `kubernetes_*`
`ReadResource` call returns.

**`--lookup` itself, however, turned out simpler than the schema shape
alone predicted — a real correction, not a confirmation, made in Stage 2
against a real cluster (`kind`), stated honestly rather than left as the
Stage-1 guess**: every `kubernetes_*` type tested (`kubernetes_config_map_v1`,
`kubernetes_secret_v1`, `kubernetes_deployment_v1`, `kubernetes_service_v1`,
`kubernetes_namespace_v1`) reads back correctly from `{"id": "<value>"}`
ALONE — a flat, single-key lookup, exactly like `aws_s3_bucket`/
`aws_iam_role`'s own shape, never the `{"metadata": [{"name": ...,
"namespace": ...}]}` list-wrapped form Stage 1's schema-only reasoning
assumed would be required. `id`'s own value for a namespaced resource is
`<namespace>/<name>` (e.g. `"ubx-test/app-config"`); for a cluster-scoped
one (`kubernetes_namespace_v1`, `kubernetes_cluster_role_v1`,
`kubernetes_cluster_role_binding_v1`) it's the bare name alone (no
namespace prefix, no separator). The provider's own `ReadResource`
implementation parses `id` into namespace+name internally — a caller
never needs to pre-populate `metadata` at all, even though `metadata` is
exactly where namespace/name live once the resource comes back. This
`<namespace>/<name>` composite is also, conveniently, the exact string
`k8saudit.parseEvent`'s own defensive `Resources` candidate-building
(`objectRef.namespace + "/" + objectRef.name`) already produces —
confirmed by this same Stage 2 work, not assumed, closing (for
`kubernetes_*` types specifically) the correlation gap §3 below flags for
the general case.

`helm_release`, by contrast, has NO `metadata`-list nesting at all — `id`,
`name`, `namespace` are all flat top-level attributes (`name` required,
`namespace` optional). Its real lookup requirement, confirmed live, is
the opposite of the `kubernetes_*` finding above: `id` alone is NOT
sufficient — the confirmed-working shape is all three together,
`{"id": "<release-name>", "name": "<release-name>", "namespace":
"<namespace>"}`. Worth stating plainly since it would be easy to assume
either that "Kubernetes-flavored" resources share one lookup convention
(they don't, even within this session's own two new providers), or that
whichever one needs less turns out to need less everywhere (here it's
the reverse of `kubernetes_*`'s own simplification).

**A minor but real finding worth naming, confirmed live**: because
`metadata` is a whole `NestingList` value (§1) and every real Kubernetes
mutation bumps the object's own `resourceVersion`, ANY drift on a
`kubernetes_*` resource's own semantic attributes (a ConfigMap's `data`,
a Deployment's `replicas`, ...) always shows a `metadata` change
alongside it too — `diffAttributes`' atomic array comparison (arrays are
compared as a whole, not recursed into further) means the whole
`metadata` array shows up as changed the moment `resource_version`
differs, confirmed directly by scanning a real ConfigMap before/after a
`kubectl patch`. This isn't a bug — every real mutation genuinely does
bump `resourceVersion` — but it means a `kubernetes_*` drift's rendered
diff always includes what looks like a second, unrelated "metadata
changed" entry, worth knowing before it looks like noise.

**A second real finding, resolved rather than left ambiguous**: several
`kubernetes_*` types exist in both a bare form (`kubernetes_secret`) and a
`_v1`-suffixed form (`kubernetes_secret_v1`) with byte-for-byte identical
schemas (confirmed directly, not assumed, for `kubernetes_secret`). The
registry seeds only the `_v1` forms — the provider's own actively
recommended naming going forward — rather than both, to avoid a
registry with two entries per resource that would always report
identical conformance results.

### 2. Sensitive attributes: verified, not assumed (the UBI-23 cross-check)

UBI-23's redaction mechanism (`provider.Redact`, walking a schema's
`Sensitive` flags) requires no Kubernetes-specific code at all — it's
generic over any `Block`/`NestedBlock` shape, confirmed by this session's
own live schema check: `kubernetes_secret_v1.data` and
`kubernetes_secret_v1.binary_data` are both `Sensitive: true` in the real
provider schema. This was the explicit thing to verify, not assume — had
the provider NOT flagged these, UBI-22 would have needed its own
type-level redaction override (a design decision requiring its own stop-
and-flag session, per this project's own standing discipline), since
`kubernetes_secret` is exactly the resource `ubx` most needs to redact
correctly. It does; no override needed. `kubernetes_config_map_v1.data`/
`binary_data`, by contrast, are correctly NOT `Sensitive` — ConfigMaps are
explicitly the non-secret counterpart to Secrets by Kubernetes' own
design, and the schema agrees.

`helm_release` contributes its own confirmation, and its own new gap: a
`NestingSet` block (`set_sensitive`) whose `value` attribute is
`Sensitive: true` — the first *Set*-nested sensitive value found in a
currently-integrated provider's real schema (AWS/GCP's own nested
sensitivity, UBI-23, was all List/Map/Single; `provider.Redact`'s
Set-handling branch existed already but had no real-schema exercise until
now). `repository_password` (a flat top-level attribute) is also
correctly `Sensitive`.

**A real, disclosed limitation, found while reading `helm_release`'s
schema and then confirmed live in Stage 2, not glossed over**: `manifest`
(the chart's full rendered Kubernetes YAML, computed) and
`metadata[0].notes`/`metadata[0].values` are plain strings, NOT
`Sensitive` — even though a chart's templates commonly interpolate a
`set_sensitive` value (or a plain, non-sensitive `values`/`set` string
that happens to contain a password) directly into rendered manifest
output. Schema-level `Sensitive` flags mark the *input* attribute only;
they don't propagate to everywhere that value might get echoed into a
derived, computed text blob. `provider.Redact`'s walk correctly redacts
`set_sensitive.value` itself — it has no way to know, and does not
attempt to guess, whether that same material reappears inside `manifest`.
This is a real, meaningful boundary of schema-driven redaction as a
general strategy, not specific to Kubernetes, but Helm is where this
session's own schema reading surfaced it concretely enough to state
plainly rather than leave implicit.

**Confirmed live in Stage 2, not just predicted from the schema**: the
top-level `values`/`chart` attributes stay `null` on an ordinary adopt
scan (they're write-only config the provider never backfills from a live
read) — the field that actually carries a values change, and the one
`ubx`'s own drift detection genuinely keys off, is
`metadata[0].values` — a computed JSON string of the release's fully
resolved values, confirmed by a real `helm upgrade --set replicaCount=3`
showing up as `metadata[0].values` changing from `{"replicaCount":1}` to
`{"replicaCount":3}` in the generated `drift_adopt` proposal. Since none
of `metadata`'s sub-attributes are `Sensitive`-flagged (§1's list is
exhaustive), this is exactly the field the limitation above describes in
the concrete: a `set_sensitive` value baked into a release at apply time
would appear here, in the resolved values a normal drift scan reads back,
unredacted — not a hypothetical, the actual mechanism by which Helm
values-drift is detected today.

### 3. Attribution: k8saudit/, one configured backend, dispatched by provider source

A `kubernetes_*`/`helm_release` drift's "who/when" story lives in the EKS
control plane's own **audit** log stream (Kubernetes' own `audit.k8s.io`
event schema — `objectRef`, `user`, `sourceIPs`, `verb`, timestamps),
delivered to CloudWatch Logs when EKS control-plane logging is enabled —
not CloudTrail (which sees AWS API calls like `CreateCluster`, not
`kubectl apply`) and not GCP Cloud Audit Logs. A third backend,
`k8saudit/`, implements the same `core.EventLookup` interface
`cloudtrail/`/`gcpaudit/` already do — **held up unchanged again**, the
third time this exact interface has generalized to a new platform without
modification.

**Dispatch is by `ScanRequest.ProviderSource`, exactly like AWS-vs-GCP —
not by resource type prefix.** This resolves what would otherwise be a
real ambiguity: EKS itself is an AWS resource (`aws_eks_cluster`, scanned
via `--source hashicorp/aws`), so `providerSource == "hashicorp/aws"`
can't be the signal to pick `k8saudit` over `cloudtrail` — an EKS
*cluster's own* drift (say, its control-plane logging config) is exactly
as CloudTrail-attributable as any other AWS resource, and should stay
that way. The actual signal is which provider *scanned* the drifted
resource: a `kubernetes_*`/`helm_release` resource is necessarily scanned
via `--source hashicorp/kubernetes`/`hashicorp/helm`, since that's the
only way `ubx` can read one at all — so `newAttributionBackend`'s
existing switch on `providerSource` (UBI-21's own registry/dispatch
mechanism) gains two more cases, `"hashicorp/kubernetes"` and
`"hashicorp/helm"`, alongside the existing `"hashicorp/google"` special
case and the AWS/everything-else default.

**"ONE configured backend in v1," stated as a real constraint, not a
placeholder for "figure it out"**: unlike AWS (a region is always
knowable) or GCP (a project is always in `provider_config`), there is no
way to derive "which EKS cluster, which CloudWatch log group" from
anything `ubx` already has on hand — this has to be operator-configured,
and configuring it is optional. A new `.ubx/config` table, `[k8s_audit]`
(`cluster`, `region`, `log_group` — `log_group` optional, defaulting to
EKS's own `/aws/eks/<cluster>/cluster` convention when the cluster's
logging setup hasn't been customized), is threaded from `cli/scan.go`'s
already-loaded `*Config` through `attributeDrift`/`newAttributionBackend`
(a new parameter on each — the one real plumbing change this stage
requires, named explicitly rather than left implicit, mirroring UBI-21's
own "`ScanRequest` gains `ProviderSource`" call-out). **When `[k8s_audit]`
is absent or `cluster` is empty, `newAttributionBackend` returns a
sentinel "not configured" condition** — `attributeDrift` recognizes it
(`errors.Is`) and records `audit_unattributed`/`not_configured`
(`core.ReasonNotConfigured`, a new, additive fourth reason value —
`docs/schema.md`'s own amendment covers the wire shape) instead of
`not_logged`, the same non-blocking, always-recorded-as-evidence posture
every other attribution failure mode already has. **Drift detection
itself is never affected either way** — attribution is best-effort by
construction (UBI-10), and an unconfigured backend is exactly as
non-blocking as a denied-credentials or no-matching-event outcome.

**A correlation gap that Stage 2 partially closed, stated honestly rather
than declared fully resolved (mirroring GCP's own Pub/Sub-vs-Secret-Manager
precedent, UBI-21)**: `core.AttributeDrift`'s `identityCandidates` tries a
resource's top-level `id`/`arn`/`name` observed attributes. For
`kubernetes_*` types, per §1 above, `name`/`namespace`/`uid` live nested
inside `metadata[0]`, not at the top level — so `identityCandidates` only
ever has `id` to offer as a search term for these types. **Stage 2
confirmed `id`'s real shape is `<namespace>/<name>`** (§1) — exactly one
of the candidate forms `k8saudit.Client.LookupEvents` already builds
defensively (`objectRef.namespace + "/" + objectRef.name`), so the
mitigation this package shipped in Stage 1 (offering every plausible
`Resources` shape rather than picking one) turned out to cover the real
case, for the one signal `identityCandidates` actually has access to.
**What remains genuinely unverified**: this confirms the *shape* of `id`
matches one candidate `k8saudit` offers — it does not confirm an actual
Kubernetes audit event's `objectRef.name`/`objectRef.namespace` come back
in exactly the casing/form a real EKS control plane emits them in, since
this session's Stage 2 used a local `kind` cluster for conformance
(free, fast, sufficient for the identity-shape question above) and had no
real EKS cluster with control-plane audit logging available to correlate
an actual audit event against — see below. The correlation mechanism is
therefore believed sound, not confirmed end-to-end the way CloudTrail's
and GCP's own attribution were each confirmed against a real match.
`helm_release`'s own `id`/`name` are flat and unambiguous (§1), so this
gap is specific to `kubernetes_*` types, not Helm.

**The EKS audit-log leg itself was not attempted this session, and that
decision is recorded here rather than silently skipped**: no EKS cluster
existed in the AWS account already (`aws eks list-clusters` returned
empty in both regions checked), and provisioning one — a real, hourly-
billed, ~15-20-minute-to-create piece of cloud infrastructure, categorically
more consequential than the free/instant local `kind` cluster this
session's other Stage 2 work used, or the Secrets Manager
secret/IAM access key created and destroyed in seconds during UBI-23's
own live verification — was judged out of proportion to attempt
autonomously without checking with the operator first, matching this
project's own "measure twice" posture on hard-to-reverse, billed actions.
`k8saudit.Backend.DeliveryLag` therefore ships as a documented,
conservative **placeholder** (5 minutes), stated plainly as unmeasured
rather than presented as if it were — the same honest-placeholder
posture CloudTrail's and GCP's own delivery-lag figures earned only after
a real measurement, not before.

### 4. `helm_release` as a resource, and chart-aware diffing's explicit non-scope

`helm_release` is a resource type like any other in this trust chain —
adopted, drift-detected, and diffed purely on its own Terraform-observed
attributes (`values`, `version`, `manifest`, `status`, `metadata`, ...),
the same `core.RunScan`/`GenerateProposal` pipeline every other type
already uses, no special-casing. **What this deliberately does NOT do**:
parse the chart's rendered Kubernetes manifests to discover or track the
individual objects (Deployments, Services, ...) a release actually
creates as their own `ubx` resources, or diff *inside* `values`/`manifest`
at anything finer than "the whole string changed." A change to what's
running in the cluster because a chart's own template logic shifted
(a new default image tag in a chart version bump, say, with `values`
itself unchanged) is invisible to `ubx` unless it happens to also show up
in one of `helm_release`'s own observed top-level attributes. Adopting
the individual Kubernetes objects a Helm release manages — as their own
`kubernetes_*` resources, alongside the `helm_release` itself — is
possible today via the exact same `kubernetes_*` conformance types this
session seeds, just not automatic or chart-aware; `ubx` treats a
`helm_release` and any `kubernetes_*` objects it happens to manage as
entirely separate, uncorrelated resources, exactly as it would for any
two independently-scanned resources.

## Sensitive overrides (UBI-24)

UBI-22's own Helm finding — `helm_release.manifest` and
`metadata[0].values`/`metadata[0].notes` are computed text blobs that can
carry a `set_sensitive` value's plaintext once a chart template renders
it into output, yet none of them are `Sensitive`-flagged in the real
provider schema — is a real gap in "secrets must never enter the ledger"
(UBI-23) as originally built: `provider.Redact` only ever consulted the
provider's OWN schema flags, which means an upstream provider's own
under-flagging becomes ubx's own leak. UBI-24 closes it by making the
provider's schema the *floor*, not the *ceiling*: **redaction is the
union of the provider's own `Sensitive` flags AND a new, ubx-owned
override table** — never the schema flags alone, and never a way to
un-redact something the schema already flags.

### The override table: data, not code

`provider/overrides.go`'s `SensitiveOverrides` is a plain slice of
`{Source, Type, Path}` entries — deliberately the same "data, not code"
posture `core/lookuphints` already established for teaching-error hints
(UBI-20): a small, committed, human-readable table, not a mechanism that
needs its own decision logic per entry. `Path` is a dot-notation string
into a resource's *observed JSON*, matching the same convention
`Modification.Before`/`After` and `core.dotSet` already use elsewhere in
this project — not a `provider.Block`/`Attribute` reference, since an
override exists precisely for an attribute the schema itself doesn't (or
can't) mark, so there's no `Attribute.Sensitive` field to point at in the
first place.

Seeded from UBI-22's own finding:

```go
{Source: "hashicorp/helm", Type: "helm_release", Path: "manifest"},
{Source: "hashicorp/helm", Type: "helm_release", Path: "metadata.values"},
{Source: "hashicorp/helm", Type: "helm_release", Path: "metadata.notes"},
```

**An audit pass, run directly against both real schemas rather than
asserted, checked every one of the ~20 registered `kubernetes_*` types
plus `helm_release` for computed attributes whose name suggests an echo
of rendered/free-form input** (`notes`, `manifest`, `output`, `log`,
`message`, `content`, `template`, `script`, `yaml`, `json`, `result`,
`rendered`, and similar). **Kubernetes: no further candidates.** Every
computed attribute matching those terms across all 20 types turned out
to be a config *enum* incidentally containing a suspect substring
(`termination_message_policy`, a `File`/`FallbackToLogsOnError`-valued
policy knob, not user data) — every genuinely computed `kubernetes_*`
attribute checked (`status`, `resource_version`, `uid`, load-balancer
`ingress` blocks, ...) is structural/identity data the provider itself
derives, never a rendering of operator-supplied *values* the way Helm's
`manifest` is. This tracks with why the gap is Helm-specific in the
first place: Helm renders a chart's *own template*, which can
interpolate literally any input value, sensitive or not, into arbitrary
output; a `kubernetes_*` resource's own attributes map directly to the
Kubernetes API object's own fields, never to a third-party template's
rendered output. **Helm: no further candidates beyond the three already
seeded** — the same targeted check against `helm_release`'s own schema
found nothing past `manifest`/`metadata.values`/`metadata.notes`
themselves. This is a documented, honest audit scope — "checked directly
and found none further," not "unchecked" or "assumed clean."

### A precise correction: `helm_release.metadata` isn't a `NestedBlock` at all

Worth stating exactly, not glossed over as "the same `NestingList`
thing" — `metadata.values`/`metadata.notes` are NOT reached via
`Block.NestedBlocks` the way `kubernetes_*`'s own `metadata`/`spec` are.
Checked directly against the real schema: `helm_release.metadata` is a
plain `Attribute` whose `Type` is the compound cty type
`["list",["object",{"values":"string","notes":"string", ...}]]` — the
attribute's *own* `Type` field describes "a list of objects," entirely
independent of the `NestedBlocks`/`NestingMode` mechanism
`kubernetes_*`'s `metadata` genuinely uses. tfplugin's `Sensitive` flag
is a single bool per top-level `Attribute` — for a compound-typed
attribute like this one, there is **no wire-protocol mechanism at all**
for the provider to flag one sub-field of it as sensitive without
flagging the *entire* attribute (losing `metadata`'s own useful,
non-sensitive fields — `name`, `namespace`, `chart`, `version`, ...) in
the process. This isn't hashicorp/helm merely forgetting a flag on
`values`/`notes` specifically — the schema shape they chose has no way
to express that granularity upstream at all. (`manifest`, by contrast,
IS a plain string attribute with no such limitation — flagging it
`Sensitive` would have been straightforwardly possible upstream; see
`docs/upstream/helm-sensitive-flags.md`.) This is exactly why a ubx-owned
override table, operating on the decoded JSON tree rather than the
schema's own `Block`/`Attribute` structure, is the right fix regardless:
it needs no cooperation from, and is not blocked by any limitation of,
the wire protocol's own attribute model.

### Nested paths reuse UBI-22's own list-nesting handling, by JSON shape not schema mechanism

`metadata.values`/`metadata.notes` still land in the observed JSON tree
exactly like a `NestingList`-nested value would — cty's own encoding
rules produce the same "one-item array of objects" shape for
`list(object(...))`-typed attributes as `ctyvalue.go`'s `blockObjectType`
produces for a real `NestingList` block (confirmed directly: the earlier
`kubernetes_secret_v1.data`/`aws_secretsmanager_secret_version` style
"$redacted" checks and this Helm case reach the same array-of-one-object
JSON shape by two different schema mechanisms). The override
path-walker exploits exactly that: it operates purely on the *decoded
JSON*, never on `Block`/`Attribute`/`NestedBlock` at all, so it doesn't
need to know or care whether an array it encounters came from a real
`NestedBlock` or a compound-typed `Attribute` — at each path segment, if
the current JSON node is an array, the *remaining* path (not a new
segment) is applied to every element, rather than assuming index `[0]`
specifically. This is the one place UBI-24 deliberately does NOT mirror
`Redact`'s own schema-driven walk mechanically — it mirrors the JSON
*shape* that walk already knows how to produce, which turns out to be
necessary rather than optional, since `helm_release.metadata` couldn't
be reached by walking `Block.NestedBlocks` even in principle.

### Union semantics: overrides can only add, never remove

`provider.Redact`'s schema-driven pass (`redactBlock`, walking
`Block.Attributes`/`NestedBlocks`) runs first, exactly as before UBI-24;
the override table is then consulted *in addition*, redacting whatever
paths are registered for that `(source, type)` regardless of whether the
schema already flagged them. An override path that lands on an
already-redacted value (a real, if not-yet-observed, overlap between a
schema flag and a registered override) is left alone rather than
re-hashing an already-opaque marker — a no-op, not a correctness issue,
but checked directly rather than assumed harmless. There is no mechanism
in the other direction: nothing in this table, or anywhere else, can mark
a real `Sensitive: true` schema attribute as safe to reveal. The
provider's own schema is a floor UBI-24 builds on top of, never a ceiling
UBI-24 could be talked down from.

### Threading `(source, type)` to the redaction boundary

`provider.Redact` gains two new leading parameters, `source, typeName
string` — the override table is keyed by exactly the same `(provider
source, type)` pair `conformance.Registry`/`core/lookuphints` already use
(UBI-21), so no new keying convention was invented. `typeName` was
already available at the call site (`core.StateReader.ReadResource`'s own
`typeName` parameter); `source` was not — `cli/stateadapter.go`'s
`stateReaderAdapter` and `conformance/harness.go`'s own copy each gain a
`source` field, populated by `newStateReader`/`AdoptMutateScanDiffConfig`
from the same `--source` string already threaded through for
`ScanRequest.ProviderSource` (UBI-21) — no new plumbing concept, the
existing one reaching one step further.

## MCP server (UBI-25)

Every prior session's audience was a human at a terminal, or a CI runner
consuming an exit code and `--json` payload. UBI-25 adds a third: an AI
assistant (Claude Code, Claude Desktop, any MCP-speaking client) asking
`ubx` questions directly, in the same conversation it's already helping
with — "who changed this bucket and when," without the human needing to
already know `ubx why`'s argument shape.

### One verb, one binary — not a second binary

`ubx mcp` is a new subcommand, not a separate `cmd/ubx-mcp` binary.
Every other capability this project has ever shipped lives in the one
`ubx` binary as a cobra subcommand — a second binary would mean
goreleaser building, signing, and publishing a second artifact, the
install docs describing two things to acquire instead of one, and a
second `PATH` entry to manage, for a command that is otherwise no
different from any other long-running `ubx` invocation (`ubx mcp` blocks
serving requests over stdio until the client disconnects, exactly the
same shape as any other CLI tool that blocks until its work is done — it
doesn't need a different process model, just a different transport).
There is no reuse benefit to a second binary either: `ubx mcp`'s tool
handlers import exactly the same `core`/`provider` packages every other
`cli/*.go` file already does.

### Three tools, wrapping the existing `--json` contract — not a parallel API

UBI-20's `--json` payloads (`whyJSON`, `statusJSON`, `scanJSON`,
`format: 1`) are not just "one more output mode" for this feature — they
ARE the API. Each MCP tool is a thin wrapper that calls the exact same
underlying primitives (`core.Ledger.Read`/`ProposalsForAddress`,
`core.Ledger.Fleet`/`core.RunScan`, `core.RunScan`/`core.GenerateProposal`/
`attributeDrift`) the CLI's own `--json` code path already calls, and
returns the exact same struct types, JSON-encoded, as the tool's result
content. A new `computeWhyJSON`/`computeStatusJSON`/`computeScanJSON`
function per command (`cli/mcp_why.go`/`cli/mcp_status.go`/`cli/mcp_scan.go`)
holds the shared "do the work, build the payload" logic; the existing
cobra `RunE` handlers are untouched (their own human-text and `--json`
paths keep working exactly as before) — the MCP handler is a second,
independent caller of the same underlying ledger/scan primitives, never
a different computation or a different JSON shape. `runVerifyAcceptance`
needed one small, mechanical signature change (`*cobra.Command` →
`context.Context`, its only real use of the former) to be callable from
a non-cobra caller at all; nothing about its behavior changed.

- **`ubx_why`** — input: a resource address (`<stack>.<type>.<name>`) or
  a 64-hex-char proposal ID, exactly like `ubx why`'s own single
  argument. Output: `whyJSON` — a `chain` (resource address form, newest
  first) or a single `proposal` (proposal-ID form), attribution already
  rendered inline in `intent.sources` (the same data `ubx why`'s human
  view narrates in words).
- **`ubx_status`** — input: an optional `stack` filter and an optional
  `drift` flag, exactly like `ubx status`'s own flags. Ledger-only by
  default (no provider, no credentials, matches `ubx status`'s own
  posture); `drift: true` requires provider configuration (`provider`/
  `source`+`provider_version`, `provider_config`) the same way `ubx
  status --drift` does — supplied as tool input, not silently defaulted,
  since an MCP client has no `.ubx/config` of its own to fall back on the
  way a human's shell session might. A missing/wrong provider
  configuration surfaces as an ordinary tool error with `ubx`'s own
  message — the teaching-error mechanism (UBI-20) already names the
  likely fix; there was nothing to add for MCP specifically.
- **`ubx_scan`** — input: a single resource's `stack`/`type`/`name`/
  `lookup`, provider identity, exactly like `ubx scan`'s own
  single-resource flags. Output: the classification (`new`/`drifted`/
  `unchanged`) and the generated proposal(s), inline in the tool result
  — never written anywhere the caller didn't ask for (an `out` input
  mirrors `--out`, writing to disk only if explicitly given), and **never
  accepted**. This is the one tool capable of a real network read against
  live infrastructure; it never becomes a write.

### Boundary by omission: signatures and mutations are human acts

`ubx accept`, `ubx propose`'s ship path, `ubx writeback`, `ubx
revert-plan`'s `--tf-dir` application, and `ubx scan --surface-as` (which
opens a real GitHub issue/PR) are deliberately **not** exposed as MCP
tools — not because they couldn't be wired up mechanically, but because
this trust chain's entire thesis is that accepting a proposal is a
recorded human (or PR-merge-derived) decision, never something an
assistant does on a human's behalf mid-conversation. This is stated
plainly in both `ubx mcp --help` and the docs page, not left to be
inferred from what's simply missing from the tool list — an omission
this deliberate is worth naming, not just leaving quiet.

### Tool descriptions are the model's UX

An MCP tool's `Description` field is the only thing standing between an
assistant reaching for the right tool at the right moment and either
guessing wrong or not realizing `ubx` can answer the question at all —
so each of the three says plainly what question it answers ("who
changed this and when," "what does the ledger think is true right now,
optionally checked against live state," "does this one resource's live
state match the ledger") and what its receipt means, not just a
mechanical parameter list. Input field descriptions carry the same
weight `--help` text does for the CLI flags they mirror.

### Configuration: the server's own cwd, same discovery as any other invocation

`ubx mcp` looks up `.ubx/config` exactly the way every other `ubx`
command does — nearest-wins discovery from the server process's own
working directory (UBI-19), not a new configuration surface. An MCP
client (Claude Desktop, Claude Code) that launches `ubx mcp` with a
`cwd` set to a real ledger checkout gets the same defaults a human
sitting in that directory would; a client that doesn't still works, the
same way a bare `ubx why <id> --ledger-dir <path>` always has —
`ledger_dir` is an explicit input on every tool for exactly this reason,
never assumed from an ambient shell state an MCP server doesn't have.

## Ledger stores (decided 2026-07-17; config cascade/formats built UBI-32 Arc A; LedgerStore interface + git reference impl + s3 store + addressing (including `$cross` by stack name + `[ledger.external]`) built UBI-32 Arc B, live-verified against real S3 including a real two-stack cross-stack pin and neighbor-advance staleness catch; the full primary CLI surface -- resolve/accept (local and --from-merge)/ship/why/status/scan/revert-plan/writeback/the MCP surface -- all wired onto `.ubx/config`'s own `[ledger]` table; PR-acceptance ceremony built and live-verified against a real GitHub PR + real S3 -- gs/azblob still designed, not wired, its own follow-up; see STATE.md for the full session-by-session history)

Two storage questions, decided separately:

**Authoring mediums (md intents, diagrams, SDK code, dialogues) always
live in git as repo assets.** They are human-authored, PR-reviewed
artifacts; proposals pin them by `content_hash` in `intent.sources`, so
the coupling is cryptographic, not locational. Nothing to build — this
is already the design.

**The ledger's own JSON (proposals/, applies/) gets a configurable
store** behind a `LedgerStore` interface (the operations `core.Ledger`
already implicitly defines: read/append proposal, apply-record storage,
head pointer, lock, salt). The in-repo git directory — today's behavior —
is the reference implementation and permanent default. Remote object-store
backed stores are planned for larger-than-repo ledgers, org-central
ledgers without a checkout, and WORM/retention compliance tiers:

```toml
[ledger]
store = "git"                                  # default — in-repo
# store = "s3://acme-ubx-ledger/payments/"     # AWS
# store = "gs://acme-ubx-ledger/payments/"     # GCP
# store = "azblob://ubxledger/payments/"       # Azure
```

Vocabulary: this is a ledger **store** (the place the files live —
matching the `LedgerStore` interface name), not a "backend" — there is
no server, no database; every store holds the same content-addressed,
hash-chained JSON objects.

What a remote store must solve before it can claim support (each store
earns "supported" via a conformance suite against the real service —
lock contention, CAS races, interrupted appends — the same per-provider
discipline the tfplugin layer uses): distributed locking
(the PID-file lock doesn't span machines — conditional writes / a lock
object with TTL), a compare-and-swap head pointer (S3 conditional puts,
GCS generation preconditions, Azure ETags — realistically via one
`gocloud.dev/blob` dependency, not three SDKs), and the PR-acceptance
ceremony (proposals must exist as files in the merged PR for
`accept --from-merge` to derive signatures — the likely shape: git stays
the signing surface, the remote store becomes the system of record,
mirrored on accept). Databases remain ruled out as truth (see the
founding files-vs-database reasoning); object stores qualify because they
preserve content-addressing, append-only posture, and independent
verifiability. A hosted ledger operated by Nexus is this same interface
with Nexus as the operator — the abstraction pays twice.

### `LedgerStore` interface (built, UBI-32 Arc B session 1)

Extracted from `core.Ledger`'s own actual filesystem calls — read
directly, not assumed from the design sketch above — across
`core/ledger.go`, `core/lock.go`, `core/salt.go`, and `core/apply.go`.
One finding worth stating plainly before the interface itself: **every
read path that walks the whole chain — `Chain()`, `Fleet()`,
`ProposalsForAddress()`, `LastObservedHash()`, `FoldState()` — does it by
following `Parent` links back from `Head()`, calling `Read(id)`
repeatedly. None of them ever lists a directory.** The *only* operation
that needs a real list is `ApplyAttempts` (discovering which attempt
numbers exist for one proposal). This matters directly for a remote
store: no "list every proposal" primitive is needed at all, which is the
one operation object stores are worst at making cheap and consistent.

```go
type LedgerStore interface {
    ReadProposal(ctx context.Context, id string) ([]byte, error)
    WriteProposalIfAbsent(ctx context.Context, id string, data []byte) error

    Head(ctx context.Context) (string, error)
    AdvanceHead(ctx context.Context, expectedPrev, newHead string) error

    ReadApply(ctx context.Context, proposalID string, attempt int64) ([]byte, error)
    WriteApply(ctx context.Context, proposalID string, attempt int64, data []byte) error
    ListApplyAttempts(ctx context.Context, proposalID string) ([]int64, error)

    Lock(ctx context.Context, ttl time.Duration) (release func(context.Context) error, err error)

    ReadSalt(ctx context.Context) ([]byte, bool, error)
    WriteSaltIfAbsent(ctx context.Context, salt []byte) error
}
```

Byte-level, deliberately: JSON marshal/unmarshal (and `ErrCorruptLedgerEntry`/
`ErrCorruptLedgerHead`/`ErrCorruptApplyRecord`'s own decoding) stays exactly
where it already lives in `core/ledger.go`/`core/apply.go` — the store
only ever moves bytes, so corrupted-content detection is identical
regardless of which store produced the bytes, never duplicated per store.

**`WriteProposalIfAbsent`/`WriteSaltIfAbsent` are create-only** — the
store refuses (a distinguishable error) rather than overwrites if the key
already exists, which is what makes proposal immutability
(`ErrDuplicateProposal`) and the salt's own race-safe first-use generation
store-agnostic properties rather than something the git store alone
enforces via a `Stat`-then-write race window. `WriteApply` is the one
write that's deliberately NOT create-only: `SaveApplyProgress` calls it
repeatedly for the *same* attempt number as an apply progresses
(docs/executor.md's own THE invariant — durable before every risky step),
so it's a plain idempotent overwrite by design, sealed only once
`SealApply` writes its final content.

#### `Head`/`AdvanceHead`: a compare-and-swap, not a mutable pointer file

The git store keeps today's exact behavior — `Head` reads
`.ubx/ledger.lock`; `AdvanceHead` writes it, entirely safe because the
existing `.ubx/lock` PID-file lock already serializes every caller before
either runs (mutual exclusion, not optimism — zero behavior change).

A remote store can't assume that kind of exclusion survives a lock's own
TTL expiring mid-write (see Locking, below) or a network partition, so its
`Head`/`AdvanceHead` need a real, independent correctness guarantee, not
just "the lock should have prevented this." The design: **every
`AdvanceHead` call creates one new, permanent, content-addressed object —
never overwrites a mutable one** — keyed by the *previous* head it's
advancing from:

```
<prefix>/heads/genesis          → the first proposal's own ID
<prefix>/heads/<proposal-A-id>  → proposal B's ID (B's parent is A)
<prefix>/heads/<proposal-B-id>  → proposal C's ID (C's parent is B)
```

`AdvanceHead(ctx, expectedPrev, newHead)` writes `heads/<expectedPrev-or-
"genesis">` with `newHead` as its content, using the store's own
create-only write. Two callers racing to advance from the *same*
`expectedPrev` can't both succeed: the second one's create-only write
fails outright (this is a genuine, portable guarantee, confirmed directly
against `gocloud.dev/blob`'s own source before relying on it — its
`WriterOptions.IfNotExist` is honored identically across every driver,
returning `gcerrors.FailedPrecondition` on conflict; `s3blob` implements
it via S3's own native `If-None-Match: *` conditional write, a real
server-side atomic guarantee, not a client-side check-then-write race).
This is a genuine compare-and-swap, expressed entirely through one
portable primitive every `gocloud.dev/blob` driver already exposes — no
provider-specific ETag/generation-precondition code needed anywhere, the
exact "one dependency, not three SDKs" property the design room wanted.

Resolving the *current* head is then "follow `heads/` edges forward from
genesis until one is missing" — correct, but `O(chain length)` requests
in the worst case. A `head-hint` object (a plain, non-atomically-
overwritten cache of the last-known head, updated best-effort after every
successful `AdvanceHead`) bounds the common case to "however stale the
hint is" rather than always walking from genesis — `Head()` reads the
hint first, then keeps following edges forward from there. Correctness
never depends on the hint being right or even present; it's purely a cost
optimization, checked directly, not assumed, against
`core/state.go`'s own `Chain()` doc comment: "Ledgers are expected to be
small at this stage" — the same foundational-slice assumption already
governing the existing full-chain linear walk, extended to the remote
case rather than contradicted by it.

#### Locking: a TTL, not a PID, is the staleness signal

The concrete interface method is `Lock(ctx, ttl) (release, error)`. A
remote store implements it with the identical create-only primitive
`AdvanceHead` uses — a `lock` object, created only if absent, holding an
expiry timestamp — but its staleness check is necessarily weaker than
git-local's: `acquireLedgerLock`'s PID-liveness check
(`processRunning`) only works because git-local is inherently
single-machine; a remote store has no equivalent "is the holder's process
still alive" signal at all, so **an expired TTL, not a dead PID, is the
only staleness signal a remote store can ever have.** A lock past its own
recorded expiry is treated as abandoned and reclaimed outright — real,
honest boundary, not glossed over: a holder that's simply slow (a large
apply, a network hiccup) past its own TTL looks identical to one that's
truly dead, which is exactly why `ubx ship`'s own freshness/CAS checks
(never just the lock) are what actually prevent a reclaimed-too-early
lock from corrupting anything — the lock is a contention/fairness
optimization on top of `AdvanceHead`'s own CAS, never the sole correctness
guarantee, for either store.

#### A real gap found while building this, fixed for both stores

`Append`'s existing duplicate check (`os.Stat` the proposal path, refuse
if present) was written assuming "the proposal file exists" and "this
proposal has already been fully accepted" are the same fact. They aren't:
a crash between writing the proposal object and advancing the head
leaves the first true but not the second — and retrying the identical
`Append` call, before this session, would have hit `ErrDuplicateProposal`
on a proposal that was *never actually accepted* (the head never moved),
with no path to recovery at all. Not a hypothetical: `core/ledger_test.go`
already had `TestLedger_DuplicateProposalRejected` covering the *genuinely
already-accepted* case, but nothing covered the crash-in-between case —
confirmed by writing a new test,
`TestLedger_InterruptedAppendResumes`, that reproduces the exact crash
shape (a proposal file written directly, bypassing `Append`, with the
head deliberately left at that proposal's own `Parent`) against the
pre-session code before fixing anything. Fixed once, store-agnostically,
in `core/ledger.go`'s own `Append`: a proposal whose object already exists
is only `ErrDuplicateProposal` if the *head already reflects it* (this ID
is the current head or reachable from it); if the proposal object exists
but the head still names its own `Parent` as current, `Append` resumes —
verifies the existing object's content matches, then just completes the
head-advance step — rather than refusing a recoverable, interrupted
operation as if it were a genuine duplicate. Applies identically to
git-local (a real, if rare, gap that existed before this session, now
closed) and every remote store alike.

#### Salt: a git-local side effect stays git-local

`Salt`'s existing `.gitignore` maintenance (`ensureGitignored`) is a real
behavior, but it's a *git-directory-specific* one — a remote store's salt
object was never at risk of an accidental `git add -A` in the first
place, so `WriteSaltIfAbsent` itself stays a pure store operation; the
`.gitignore` bookkeeping lives one layer up, in the git store's own
implementation, called only there, never promoted into the shared
interface as if every store needed an equivalent.

#### PR-acceptance ceremony: designed (UBI-32 Arc B session 1), built (session 3)

`ubx accept --from-merge`'s own verification (docs/schema.md's `pr_merge`
amendment; [`ubx accept`](https://github.com/Ubiquex/ubiquex-docs)'s own
CLI reference) is entirely about **git and GitHub history** — the merge
commit exists, `--proposal-file` at that commit still hashes to the PR
body's trailer, the reviewers are whatever the GitHub API says right now
— and none of that changes one bit for a stack whose store is remote.
The proposal file the ceremony verifies against still has to exist as a
real, committed file in the merged PR; a remote store changes *where the
accepted record ends up living*, never *what's being verified* or *how*.

The designed shape, now built exactly as designed: **git stays the
signing surface, the remote store becomes the system of record, mirrored
on accept.** `AcceptFromMerge`'s own git/GitHub verification runs
completely unchanged; only *afterward* does `cli/accept.go`'s
`acceptFromMerge` open the stack's configured `LedgerStore` (via the same
`openLedgerForStack` local `ubx accept` already used) and write through
it — `Ledger.Append`'s own `WriteProposalIfAbsent`+`AdvanceHead` CAS
mechanism was already store-agnostic since Arc B session 1's own
extraction, so this needed no new mirroring mechanism at all, only
opening the right store at the right point (after verification, before
the write). The git-committed proposal file is never deleted or treated
as disposable afterward — git history is permanent regardless — but for
a remote-store stack, `ubx why`/`ubx status`/a subsequent accept's own
`Head()` check all read the mirrored, authoritative copy in the
configured store instead, never the git-committed file directly. `ubx why
--verify-acceptance` keeps re-deriving its git/GitHub checks exactly as
before, entirely independent of which store holds the accepted record —
the ceremony's own evidence trail was never the store's concern to begin
with.

Hermetic adversarial rows (docs/ledgerstore-adversarial.md rows 13-16):
a merge without the proposal file never even opens the store; a tampered
trailer's hash mismatch is caught before `Ledger.Append` ever touches the
store (validated proposal or nothing — no partial write); a CAS conflict
between two merge-derived proposals resolves exactly like local accept's
own `ErrParentMismatch`; the identical merge commit accepted twice
resolves exactly like local accept's own `ErrDuplicateProposal`. Live:
a real PR opened and merged against `Ubiquex/ubiquex-cli` itself,
`accept --from-merge` genuinely mirroring the accepted record into a
real S3 bucket (confirmed via a direct `aws s3api get-object`, not just
trusting `ubx`'s own report), then the merge commit reverted and the
scratch branch deleted, leaving the repo and the bucket both clean.

### Addressing: derived by rule, never mapped per stack

A ledger address is always `<base store>/<stack>/` — the stack name
itself is the address segment, derived, never declared:

```
s3://acme-ledger/acme/prod/          ← base store (config)
├── payments/{proposals,applies,head}   ← --stack payments
└── network/{proposals,applies,head}    ← --stack network
```

Consequences, all deliberate: a new stack needs zero setup (its prefix
appears on first append); nothing is ever written to the bare base;
`$cross` resolves by NAME against the same base
(`{"stack": "network", "pinned_head": ...}` → `<base>/network/head`) —
no relative filesystem paths in pins, killing the checkout-layout
fragility; environments are not a ubx concept — an "env" is just a
deeper base prefix (`.../acme/prod/` vs `.../acme/staging/`), which is
also the future promotion-model hook, no schema needed; and **the chain
is per-store**: each stack under a base gets its own head and its own
chain — for remote stores, docs/schema.md's founding "per-stack hash
chain" sentence becomes true by construction. Git-local keeps today's
flat single-chain layout as the supported legacy shape (read forever);
the `ledger/<stack>/` subdirectory layout is the forward shape.

The one thing ever declared: a cross-stack ref to a stack living in a
*different* base (another repo, bucket, or team) — genuinely external
information, undeducible by rule:

```hcl
ledger = {
  external = {
    network = "s3://other-team-bucket/net/prod"
  }
}
```

(corrected from this section's own earlier sketch, which showed `ledger
{ external { network = ... } }` as nested HCL blocks — never actually
parsed against `hclsyntax` until this session, when it turned out
unnecessary to use blocks here at all: `network` happens to be a bare
identifier so a block *would* parse, but attribute-object syntax is
what every other `.ubx/config` table already uses (`provider = { ... }`,
`providers = { ... }`), and `[ledger.external]`'s own keys are stack
names — arbitrary strings, not always bare identifiers — so the
attribute form is what's actually implemented, kept consistent with
the rest of config rather than introducing the one exception.)

#### `$cross` by stack name, built (UBI-32 Arc B): `"stack"` alongside `"ledger_dir"`

`$cross`'s own inner object gained a second, mutually exclusive way to
name its neighbor — `{"stack": "network", "to": "..."}` — resolved
against the CURRENT stack's own configured `[ledger]` store (or
`[ledger.external]`'s own override for that stack name, if one exists),
via `deriveStackAddress(base, stack)` (`core/resolver/refs.go`, plain
string concatenation — deliberately not URL-aware, since the query-param
translation only matters once the resulting address string is actually
*opened*, one layer down). `{"ledger_dir": "..."}` is unchanged and
permanent — git-local's own explicit-path shape, forever supported,
never deduced.

Opening either shape uses the same new `core.OpenRef(ctx, ref)`: a plain
directory path (no `://`) opens git-local exactly as `core.Open` always
has; anything else is handed to whichever opener
`core.RegisterRemoteLedgerOpener` installed — a small registry
(`core/openref.go`), the identical "core stays dependency-free" inversion
`StateReader`/`EventLookup` already establish, since `core/resolver`
itself imports nothing beyond `core` (confirmed by reading its own
existing package doc comment, not assumed) and must never import
`ledgerstore`/`gocloud.dev/blob` directly. The concrete opener is
registered once, by the `cli` package's own `init()`, wrapping
`ledgerstore.Open`. `core.Ledger` itself carries the addressing metadata
`resolveCross` needs (`BaseStore()`/`ExternalStack()`), set only via a
new `OpenStoreForStack(store, base, external)` constructor —
`cli.openLedgerForStack` uses it for a remote-store-backed ledger;
`Open`/`OpenStore` (git-local, or a remote ledger opened with no
addressing context) leave it empty, `$cross`'s own `"stack"` field
refused with a clear error naming the gap rather than silently resolving
against nothing.

`VerifyPins` and a destroy's own `known_dependents` cross-stack orphan
check both moved to `core.OpenRef` too, uniformly — a recorded
`resolution.inputs` pin's `LedgerDir` is already a fully-resolved address
by the time either ever reads it (whichever shape `$cross` used at
resolve time), so neither needs any addressing metadata of its own to
verify a pin or check for orphans, regardless of which store backs the
neighbor.

#### URI prefix, built (UBI-32 Arc B session 1): path-style, translated internally

`s3://acme-ledger/acme/prod/`'s own path segment (`acme/prod/`) is **not**
something any `gocloud.dev/blob` driver understands natively — checked
directly against `s3blob`'s own `URLOpener` before relying on it: it reads
only the URL's host as the bucket name, nothing from the path. The
generic `blob.OpenBucket` mux does support a prefix, but only via its own
`?prefix=` query parameter, uniformly across every scheme. `ubx`'s own
store-URI parser is the translation layer: it splits a configured
`store` URI into `scheme://host` (opened via `blob.OpenBucket` unchanged)
and the path component (appended as `?prefix=<path>` before opening) —
giving the nicer, doc-matching path-style syntax the design already
committed to, without that syntax needing to be something every cloud's
own driver has to understand. `--stack`/config's `stack` value is then
appended as a further prefix segment on top of whatever the configured
`store` already carries, exactly matching the diagram above.

#### A real, deliberate consequence: `--stack` is required to *open* a remote ledger

Git-local's flat, single-chain legacy layout means one `--ledger-dir`
can hold several stacks' proposals interleaved in one chain
([concepts/ledger.mdx](https://github.com/Ubiquex/ubiquex-docs)'s own
"stacks are independent, but not physically separated" — unaffected by
this arc, kept forever) — so a command like `ubx why <proposal-id>` never
needed to know a stack ahead of time to open the right ledger: there's
only ever one, for that `--ledger-dir`. A remote store's own per-stack
chain (`<base>/<stack>/`, above) breaks that assumption on purpose — each
stack is a genuinely separate chain, at a separate address, so *opening*
one at all requires knowing which stack first. This is a real, honest
API consequence, not smoothed over: a command whose argument itself names
a stack (a resource address, `<stack>.<type>.<name>`) can still derive it
directly and needs nothing new; a command whose argument is a bare
proposal ID (no stack encoded anywhere in it) genuinely cannot resolve
which remote chain to open without an explicit `--stack` — cheap and
already-supported for a git-local, multi-stack-in-one-chain setup where
it's optional, newly *required* the moment `.ubx/config`'s `[ledger]`
table names a non-`git` store. See docs/ledgerstore-adversarial.md for the
required-outcome row this becomes.

### Config: cascading, per-key, child overrides parent (built, UBI-32 Arc A session 1)

Config discovery upgrades from nearest-file-wins to an editorconfig-style
cascade: walk from cwd to root collecting every `.ubx/config*`; resolve
**per key** (not per file) — the nearest definition of each key wins;
tables merge key-wise; CLI flags beat everything (UBI-19's precedence,
per-key). A repo root holds shared `[ledger]`/`[provider]`; an env
directory overrides only `store`; a stack directory holds `stack =
"payments"` and any per-stack overrides. Because cascades are powerful
and invisibly wrong, a provenance surface ships with them: a resolved-
config view printing every effective value AND which file supplied it.

**Built, UBI-32 Arc A session 1.** The merge happens on a **generic
tree**, not the typed `Config` struct directly: each discovered file
parses into its own `map[string]any` (nested tables are nested maps,
recursively — a TOML `[provider_configs."hashicorp/aws"]` table, an
HCL `provider_configs = { "hashicorp/aws" = { ... } }` attribute, and a
YAML `provider_configs: {hashicorp/aws: {...}}` mapping all parse to the
*identical* Go shape before merge ever runs), so the merge/provenance
logic is written exactly once and never needs to know which format
produced a given layer. The fold walks layers **root-to-nearest**
(reverse of discovery order) and merges recursively: if both the
accumulator and the new layer hold a map at some key, recurse into it
key-by-key; otherwise the new layer's value replaces the accumulator's
outright and its own file is recorded as that key's provenance. This is
what makes "tables merge key-wise" true at every nesting depth, not just
the top level — a child directory setting only
`provider_configs."hashicorp/aws".region` leaves every *other* key of
that same source's config (and every other source's config entirely)
intact from whatever parent supplied them. Once folded, the final
generic tree is JSON-round-tripped into `Config` (`json.Marshal` then
`json.Unmarshal`, `Config`'s own struct tags now carry matching `json`
tags alongside `toml`) — one decode step for all three formats, never
three separate struct-decode paths.

**Provenance keys are dotted paths**, the same *style*
`Modification.After`/`core/state.go`'s `dotSet` and `tfwrite`'s own
attribute-path splitting already use elsewhere in this project for
locating a value inside nested JSON — but neither of those has to
quote a segment, since a resource attribute name is always a bare
identifier. A provider source string (`hashicorp/aws`) is not, so
provenance paths need their own, new quoting rule: any map key that
isn't a bare identifier (`[A-Za-z_][A-Za-z0-9_]*`) is rendered
double-quoted. Examples: `stack`, `provider.source`,
`providers."hashicorp/aws"`, `provider_configs."hashicorp/aws".region`,
`k8s_audit.cluster`.

`ubx config` (the provenance view itself) prints every key the merged
generic tree actually holds, including one the unknown-key check just
warned about — deliberate, not an oversight: the warning already flags
it as unrecognized, and the view's whole job is showing what the
cascade genuinely contains, not silently filtering to `Config`'s own
known fields a second time.

**Unknown-key detection moved from BurntSushi's `MetaData.Undecoded()`
to a hand-written schema-shape walk**, because that mechanism is
struct-decode-specific and stops applying the moment parsing targets a
generic map instead (confirmed empirically: decoding into
`map[string]any` reports *every* key as "undecoded," since no struct
field ever consumes any of them — useless as a signal in this shape).
Each layer's own generic tree is checked, at parse time, against the
known top-level keys (`stack`, `github_repo`, `tf_dir`, `provider`,
`provider_config`, `providers`, `provider_configs`, `k8s_audit`) and the
known sub-keys of `provider`/`k8s_audit` specifically; `provider_config`/
`providers`/`provider_configs` are freeform by design (arbitrary
provider-defined keys) and never checked below their own top level. A
warning still names the exact file the typo came from — checked per
layer before the merge, not after, so provenance of the warning itself
never blurs across files the way a post-merge check would.

### Cascade ceiling: where the upward walk stops (design-room decision, 2026-07-19; built, UBI-32 Arc B addendum)

An unbounded upward walk is exactly the kind of powerful-and-invisibly-
wrong thing the provenance surface above already exists to mitigate —
but provenance only explains a cascade *after* it ran; the walk itself
still needs an explicit stop rule, or a config value can silently arrive
from a directory nobody realized was still being read (a parent
monorepo, a stray `.ubx/config` in `$HOME`, an unrelated ancestor
directory on a shared machine). Three rules, checked in this order, at
every directory the walk visits, on top of `.ubx/config*`'s own
per-directory discovery already documented above:

1. **`root = true`** — a new, ordinary top-level config key
   (`Config.Root`, boolean, cascade-merged and provenance-tracked like
   any other key). The editorconfig precedent this borrows directly:
   the directory declaring it is still fully included — its own other
   keys apply exactly as they would anywhere else in the cascade — but
   the walk stops immediately afterward, never reading anything farther
   up. Checked as each layer is parsed, during the walk itself, not
   deferred to the merge step afterward — the merge doesn't decide how
   far the walk went, the walk decides what the merge ever sees. A
   `root` key present but not a literal boolean (`root = "true"`, a
   string) is a hard error naming the file — the same "ambiguity
   rejected loudly, never guessed" standard YAML strict mode already
   holds itself to elsewhere on this exact surface.
2. **No `root` marker anywhere → the git repo boundary is the implicit
   ceiling.** The directory containing `.git` (a directory for an
   ordinary checkout, a file for a worktree or submodule — either one is
   sufficient signal; its content is never read, only its presence) is
   still included, then the walk stops. This is a good default because
   it matches what "one project" already means structurally in this
   codebase's own world (one repo, one or more stacks) without asking an
   operator to remember to write `root = true` themselves for the common
   case.
3. **Outside any repo, `$HOME` or `/` is the ceiling** — reached
   naturally by the same walk, not a special-cased lookahead: if neither
   a `root` marker nor a `.git` boundary was ever found, the walk simply
   keeps going, and eventually arrives at `$HOME` or the filesystem root
   either of which it also treats as an inclusive stop, for exactly the
   reason rule 2 doesn't fire outside a checkout at all — there's no
   repo boundary to find. This is what keeps a `ubx` invocation from
   directories outside any git checkout (a scratch directory, a `/tmp`
   experiment) from walking all the way to `/` reading unrelated
   ancestor directories' config by accident forever, the same concern
   rule 2 addresses for the common inside-a-repo case.

`ubx config` (the provenance view) reports which of the three rules
actually stopped the walk, and where — `root marker (<file>)`, `repo
boundary (<dir>)`, `$HOME (<dir>)`, or `filesystem root (<dir>)` — since
"where did this stop and why" is exactly the kind of question a
provenance surface exists to answer honestly, not just "here are the
values."

### User-global `~/.ubx/config`: personal preference only, never project truth (design-room decision, 2026-07-19; built, UBI-32 Arc B addendum)

`~/.ubx/config*` (the same three-format discovery order, rooted at
`$HOME` specifically) is consulted, but **outside** the cascade proper —
never a layer the upward walk itself passes through or stops at, always
folded in as the single lowest-priority source underneath whatever the
project cascade supplies. The reason it's kept structurally separate,
not just prioritized last: **a checkout must resolve identically on
every machine.** A project-truth key — `stack`, `providers`,
`provider_configs`, `ledger` (its `store`), and `intent` (the
intent-provider config — `adapter`/`model`/`key_ref`, designed UBI-41
session 1, docs/intent-provider.md; not yet implemented) — read from a
per-user file would mean the same commit resolves two different ways for
two different people, or for the same person on two different laptops,
which is precisely the failure mode this project's own "files, not a
database, independently verifiable" posture exists to prevent one level
down. So user-global config is **allowlist-only**: every top-level key
found there is checked against a fixed, currently-tiny allowlist of
genuinely personal-preference settings (today: `init_format`, `ubx
init`'s own default write format, since which format an *operator*
prefers to hand-edit is exactly the kind of thing that can differ
person-to-person without the project itself meaning anything different)
— anything else, whether it's a real project-truth key or simply a typo,
is a **hard error**, not a warning, naming the file and the key: the
normal cascade's "unknown keys warn, they don't fail" leniency doesn't
apply here on purpose, because a project-truth key silently leaking in
from `$HOME` is a correctness problem, not a forward-compatibility
convenience.

`ubx init --format` falls back to `~/.ubx/config`'s own `init_format`
(if present) before falling back to `hcl`, when `--format` itself isn't
given — the one concrete instance, today, of a personal-preference key
actually changing `ubx`'s own behavior.

**A real subtlety found while testing this, not assumed away:** if
`$HOME` itself turns out to be the cascade's own ceiling (rule 3 above
— a `ubx` invocation with no repo structure above it at all, run
directly from or under `$HOME`), `$HOME`'s own config was *already*
read and folded in as an ordinary, unrestricted cascade layer by the
time the separate user-global consultation would run. Consulting the
identical file a second time, this time under the personal-preference
allowlist, would wrongly reject a legitimate project-truth key that was
never really a "user-global" concern in the first place — caught by a
hermetic test before shipping, not discovered later. Fixed by having the
user-global loader compare its own resolved file path against the
cascade walk's already-consumed layers and skip entirely if they match:
whichever one runs first, a file is only ever a cascade layer or a
user-global one, never both at once.

### Config formats: HCL canonical, TOML supported, YAML supported (strict) (built, UBI-32 Arc A session 1)

Three formats, one internal config struct — the cascade/merge/provenance
logic is format-agnostic, written once. Order of preference and
discovery per directory: `config.hcl` → `config.toml` → `config`
(legacy TOML name) → `config.yaml` — first found wins for that
directory; formats never merge within one directory (cascading merges
across directories only).

- **HCL — canonical.** What `ubx init` writes by default, what docs
  examples show. The audience's native format, nests cleanly, and
  `hclsyntax` is already a dependency. Hard constraint: **literal
  attributes only** — no variables, functions, or interpolation,
  enforced with the same `expr.Value(nil)` literalness check
  `tfwrite` already uses; an expression in config is a hard error.
  **Every table is a top-level attribute holding an object-constructor
  expression** (`provider = { source = "...", version = "..." }`,
  `providers = { "hashicorp/aws" = "6.60.0" }`), never an HCL block —
  confirmed necessary, not a stylistic choice: `hclsyntax` blocks don't
  permit a quoted string as an argument name at all (verified directly
  against the parser; see the Multi-provider stacks section's own
  correction above), so `providers`/`provider_configs`'s source-string
  keys have no valid block-argument spelling. Using attribute-object
  syntax uniformly also means literalness checking is a single
  `attr.Expr.Value(nil)` call per top-level attribute — `Value(nil)`
  already recurses through an entire object-constructor expression, so
  one call proves an whole table's every nested value is a literal, no
  separate per-block walk needed.
- **TOML — fully supported, forever.** Already shipped (UBI-19), fully
  deterministic; existing configs never break. `ubx init --format=toml`.
- **YAML — supported, strict mode only.** UBI-19's determinism
  objections are real and stay documented — but confirmed, not assumed,
  against the actual library this session (`gopkg.in/yaml.v3`): its own
  implicit-typing resolver already treats `no`/`yes`/`on`/`off` (any
  case) as `!!str`, never `!!bool` — the classic YAML 1.1 Norway-problem
  ambiguity this project worried about turns out not to exist in this
  specific library at all, so nothing has to reject that case for it to
  be safe. What's real and confirmed: decoding a bare `6.60` into a
  generic value silently produces `float64(6.6)` — the trailing zero,
  and therefore the distinction between "6.60" and "6.6" as a provider
  version string, is gone the instant it's parsed, with no error raised
  anywhere. The parser refuses this specific case with a round-trip
  check on every plain (unquoted) numeric scalar: format the resolved
  number back to text canonically and compare against the token actually
  written; any mismatch (`6.60`→`6.6`, `007`→`7`, `1e3`→`1000`) is a hard
  error naming the offending value, never a silent narrowing. A quoted
  scalar (`"6.60"`) is exempt by construction — quoting is exactly how a
  YAML author already asserts "treat this as a string," so a quoted
  value is never coerced or checked, only a bare one. `ubx init
  --format=yaml` writes fully-quoted, unambiguous output.

### Multi-provider stacks (decided 2026-07-17, design room — resolve/ship built UBI-43 sessions 2-4; scan/status/fleet built session 5; live finale done session 6)

A stack is conceptually multi-provider (the payments example: RDS + S3 +
helm_release), but every verb today takes one provider per invocation.
The decided design:

**Config declares the stack's provider set** — a `providers` map of
source → pinned version (cascade content like any other config table;
explicit pins only, per standing rule):

```toml
[providers]
"hashicorp/aws"        = "6.60.0"
"hashicorp/helm"       = "3.0.2"
"hashicorp/kubernetes" = "2.35.1"
```

(shown in TOML — the still-current shipped format as of UBI-43. UBI-32
Arc A's own "Config formats" section below found this exact sketch's own
HCL rendering invalid the moment it was actually parsed: HCL native
syntax refuses a quoted string as a *block argument name*
(`providers { "hashicorp/aws" = ... }` → hard parse error, "Argument
names must not be quoted"). Corrected there to
`providers = { "hashicorp/aws" = "6.60.0", ... }` — an attribute holding
an object-constructor expression, where quoted keys ARE valid — never
silently left as a sketch nobody had actually run through the parser.)

**Intent files name only types** — no provider on any resource.
Inference is type → provider, resolved by asking each declared
provider's schema which types it owns (never name-prefix guessing);
ambiguity or an unowned type is a hard error naming the gap. The
resolver records the winner into each IR node's `provider` field —
present in the node schema since the founding draft, waiting for exactly
this — so which binary executes each resource is part of what gets
reviewed and signed.

**Executor: one dependency walk, a lazily-launched client pool** — the
graph is provider-agnostic (a `$ref` edge from `aws_db_instance.main.
endpoint` into a helm_release's values is just an edge); the walk hands
each node to its own provider client, launched on first use, outputs
flowing across provider boundaries exactly as within one.
Scan/status/fleet walks generalize the same way: group by each
resource's own recorded provider instead of one `--source` flag — which
flags then retire from resolve (the `providers` block is the truth).

Sequencing: config-block portion lands with UBI-32's cascade work;
resolver inference + executor pool is its own session, expected before
or with the SDK (whose codegen shape depends on the multi-provider
answer).

**Built, UBI-43 sessions 2-5, real code**: resolver inference
(docs/resolver.md's own amendment), the executor's client pool
(docs/executor.md's own amendment), and `.ubx/config`'s `[providers]`
table wiring for `ubx resolve`/`ubx ship` specifically — the config-block
portion did **not** end up waiting for UBI-32's own cascade work after
all (found while actually building it: a flat `[providers]` table in the
nearest `.ubx/config` resolves correctly today via the loader UBI-19
already shipped; UBI-32, whenever it unparks, only changes *how* that
table is found and merged across directories, not whether it works now).
Per-provider *configuration* — the design's own open question, "likely
per-source config values" — resolved as a sibling `[provider_configs]`
table, source-keyed, additive alongside `[providers]` rather than
reopening its own already-decided shape; see docs/executor.md's own
session-4 addendum for the full mechanism
(`executor.ApplierPool.Get` returns an Applier and its own resolved
config together, never a single global blob). `--source`/
`--provider-version` retirement is staged, not a breaking cutover:
stage 1 (both mechanisms coexist) and stage 2 (a deprecation warning when
both a `[providers]` table and the singular flags are given) are both
built; stage 3 (the flags retired for good) is explicitly not scheduled.

**Built, UBI-43 session 5**: `ubx status --drift`/`ubx scan --all`'s own
multi-provider fleet-grouping — each Fleet entry routes to its own
recorded provider (`core.Ledger.Fleet`'s new `Provider` field), or, for a
legacy/adopted entry with no recorded provider of its own, one inferred
fresh by type against the declared set (the identical `resolver.InferProvider`
mechanism a brand-new resource's own resolve already uses, now exported
for this reuse) — see docs/executor.md's own session-5 addendum for the
full mechanism and its live verification against two real provider
subprocesses.

**Live finale done, UBI-43 session 6**: a real `aws_sqs_queue` +
`google_service_account` (real AWS account, real GCP project
`personal-273114`), one intent file, a genuine cross-provider `$ref`,
resolved → accepted → shipped as ONE signed proposal, real drift on both
providers correctly detected and attributed, account left clean
afterward. The originally-planned second provider (`hashicorp/time`) was
swapped for a real second cloud provider mid-session after a live probe
found `time_static` structurally can't support `ubx`'s own drift model at
all (every attribute but `id` comes back null from a minimal lookup) —
see docs/executor.md's own session-6 addendum for the full finding and
two further, real, GCP-specific gaps found along the way (filed as
UBI-44 for the more serious one, a destroy that silently doesn't destroy).

## Intent provider + md medium (built, UBI-41 — docs/intent-provider.md; closed)

Phase 3's opener: the first session where an LLM enters the product.
Full design in docs/intent-provider.md (the transcription-only boundary,
the adapter interface, config, the conformance suite) and
docs/intent-provider-adversarial.md (the required-outcome program);
docs/schema.md's own amendment pins the wire-format half. Summarized here
at the system-model level, matching how every other headline section in
this document cross-links its own detail doc rather than duplicating it.

**The boundary, restated at the level of this document's own founding
invariants**: trust-chain invariant #3 (*"The LLM operates in intent-space
only; the deterministic resolver computes all values; nothing the LLM
emits reaches apply without resolution + human signature"*) was stated at
this project's very first session and has held, untested, until now. This
arc is where it gets tested for real: `ubx propose --from-doc payments.md`
runs a markdown document through an LLM adapter (Claude first; OpenAI,
Gemini, and eventually a local/ollama-class adapter follow, each earning
"supported" via the same conformance suite the ledger-store backends and
provider types already earn it through) to produce an `intent/v1`
**draft** — never a proposal, never anything that touches a ledger or a
provider directly. Everything from `ubx resolve` onward is the existing,
unmodified pipeline.

**The design center**: where a document is genuinely ambiguous ("like
staging but smaller"), the interpretive choice the intent provider makes
becomes explicit, listed, reviewable content inside the draft itself —
new `assumptions`/`defaults`/`questions` fields on `Proposal.Intent`
(docs/schema.md's amendment), carried unchanged through `resolve`/`accept`
into the final hashed, signed proposal. A human reviewing and accepting a
draft reviews and signs its own stated assumptions along with everything
else — never a silent guess baked into an otherwise-ordinary-looking
resource config with no trace anywhere.

**Config**: a new `[intent]` table (`adapter`, `model`, `key_ref`),
cascade content like `[providers]`/`[provider_configs]` — `key_ref` is
never material, the same "config stores references only" extension of
this project's own secrets rule (see "Business frame," below). Gemini's
own Vertex AI access mode (ambient GCP Application Default Credentials,
no key at all) is settled explicitly, matching the GCP provider binary's
own existing credential posture rather than inventing a second one.

**Sequencing**: after destroys (UBI-30) and multi-provider stacks
(UBI-43) — both would otherwise hit the "one provider per stack" wall
markdown-authored proposals immediately run into. Chat rides the
identical adapter interface afterward (docs/plan.md's own medium-order
decision), nearly free — same transcription job, a dialogue transcript
instead of a file as input.

## SDK program (built, UBI-33/34 — docs/sdk.md; TypeScript closed as UBI-34, Go/Python UBI-33 open)

Component map #7's first real design: the first authoring frontend that
is ordinary, typed, human-authored code rather than prose transcribed by
an LLM. Full design in docs/sdk.md (the multi-language contract — golden
`intent/v1` fixtures as the spec — the `sdk/` monorepo layout, the
describe-only `@ubx/sdk` runtime surface, the hermetic evaluator decided
**empirically** this session against three real candidates, `core.
DoubleRun` reused at the evaluation boundary, and the codegen design:
provider schema → a language-neutral IR model → per-language templates,
generated locally by `ubx sdk gen` at the config-pinned provider version,
never published). Summarized here at the system-model level, matching
how every other headline section in this document cross-links its own
detail doc rather than duplicating it.

**The boundary, restated at the level of this document's own founding
invariants**: the same trust-chain invariant #3 the intent provider arc
tested for prose ("the LLM operates in intent-space only... nothing it
emits reaches apply without resolution + human signature") applies here
without an LLM in the loop at all — an SDK program is just another
`intent/v1` **producer**, handed to the exact same, completely
unmodified `core/resolver` pipeline every other producer already uses.
The hard part this arc actually adds is a different one: the evaluator
that runs a program author's own TypeScript has to be genuinely hermetic
(no network, filesystem, environment, or wall-clock reach) even though
nothing about the trust chain requires it to be adversarial-proof against
the program's own author — it's defense against what a describe-only
program should never need to do, not defense against a malicious author
with legitimate ledger access already.

**Decided empirically, not from documentation**: Node's `--permission`
model, Deno, and `isolated-vm` were each actually run against the real
requirement this session, in this environment — Node disqualified
outright (its permission model has no network or environment gate at
any flag combination); Deno chosen (closes three of four requirements by
default with zero flags, needed exactly one additional flag once a real
gap — remote module imports bypassing `--deny-net` entirely — was found
empirically rather than assumed closed); `isolated-vm` recorded as the
stronger-but-costlier fallback (memory-isolated by construction, but a
native-compiled dependency whose install script didn't even run cleanly
under this session's own npm lockdown, and no native TypeScript support).
Every number behind this decision is a probe this session actually ran,
not asserted from either tool's own documentation.

**Sequencing**: after multi-provider stacks (UBI-43, already built) —
the SDK's own multi-provider inference (`resources[]` name only types;
the resolver's existing `InferProvider` mechanism supplies the rest) was
always going to depend on that landing first, per docs/plan.md's own
medium-order note. Unlike the intent provider, the SDK has no ambiguity
step to design around — a typed program has no interpretation to make
transparent, so it carries no `assumptions`/`defaults`/`questions` at
all, by construction, a real and named divergence from the md medium's
own design center rather than an oversight.

## Diagram medium (built, UBI-47 — docs/diagram-medium.md; closed)

Component map #7's fourth authoring frontend, and the first that is
bidirectional by construction. Full design in docs/diagram-medium.md
(the canonical D2 subset, the lossy-medium rule applied concretely, the
cross-stack grammar, a new additive `ResourceIntent.DependsOn` wire
field, the render direction's own `render --check` contract).
Summarized here at the system-model level, matching how every other
headline section in this document cross-links its own detail doc rather
than duplicating it. **Built across sessions 1–6** (docs/diagram-
medium.md's own "Slice N: built" sections have the full, session-by-
session account); this header itself went stale for two sessions after
implementation finished, then was fixed in place session 6 — a small,
real instance of "never contradict docs silently" applying to a
system-model summary, not just a design decision.

**Text or it isn't a medium**: D2 is parseable text, parsed and emitted
via `oss.terrastruct.com/d2`'s own narrow parser/compiler/formatter
packages (confirmed this session to pull in none of that library's own
heavy rendering machinery) — PNG/SVG stay render *products*, regenerated
on demand, never read back. **The lossy-medium rule, generalized**: a
diagram authors topology only (nodes → resources, containers → pure
visual grouping, edges → dependencies) — never attributes; this is the
same boundary the intent provider's own "transcription, never
computation" rule already drew for prose, restated here for a structural
medium instead of a prose one, and it's what the founding "every medium
is a projection, never a second source of truth" thesis actually means
in practice: two mediums can never claim the same attribute.

**No LLM in this medium's own path at all — a real, load-bearing
distinction from the md medium, despite reusing its own wire fields.**
A diagram node's type comes from a `class:` attribute, resolved via
`resolver.InferProvider` (UBI-43, reused completely unchanged) exactly
the way a hand-written intent file's own untyped-by-provider `resources[].
type` already is. What's reused from UBI-41 is narrower than the whole
adapter machinery: `core.Intent`'s own `assumptions`/`defaults`/
`questions` wire fields, proven this session to generalize to a second,
entirely different kind of interpretive gap (a deterministic parser's
own structural ambiguity — an uninferable or ambiguous node type) rather
than only an LLM's.

**Sequencing**: filed during the SDK arc, slotted in after UBI-34 closed.
Smaller than the SDK arc (no sandbox, no codegen) — the design work this
session found one genuinely new, additive wire-format need
(`ResourceIntent.DependsOn`, a topology-only dependency signal the
existing `$ref`/`$cross`-scanning mechanism has no way to express
otherwise) and confirmed empirically that D2's own `class:` mechanism
(not a tempting but wrong custom-key approach, found and rejected before
it shipped) is the right vehicle for both parse-side type annotation and
render-side icon classing — one shared vocabulary, not two.

## Independent verification (built, UBI-38 — `ubx verify`)

The auditor's command, and a natural gap-filler after Phase 3 (the
authoring frontends) closed: the product's own core claim is
"independently verifiable" — `ubx verify` makes that claim a demo rather
than a slogan, one command, entirely offline. It re-checks everything
checkable without trusting a single already-computed value anywhere:
every proposal's own content hash against its own `id` (`core.Hash` had
only ever been *called* to compute an id, at accept time — nothing
anywhere re-verified one against stored bytes on read, until this
command); the parent-chain walk itself, done independently rather than
trusting `core.Ledger.Chain`'s own all-or-nothing error return, so a
broken link is a reported finding, never a command crash; every sealed
apply record's own hash and its prior-*sealed*-attempt chaining
(mirroring `BeginApply`'s own exact linkage rule, not a naive N/N-1
walk — a crashed, never-sealed attempt must never be flagged as a broken
chain); every `$redacted` marker's own inner shape (`core.
IsRedactedValue` only ever checked the outer `{"$redacted": ...}` shape;
nothing checked that the inner object is actually `{"sha256": <64-hex>}`
until now).

**A tampered proposal doesn't just fail its own check — every later
proposal in the chain is flagged too**, whether or not any of *their*
own bytes were touched. A later proposal's own hash and parent link can
check out perfectly while still resting on corrupted history; a human
reading the report needs to know that, not just which one file changed.

**Acceptance re-derivation is opportunistic, never rounded up.**
`pr_merge`-accepted proposals get re-derived via the existing
`runVerifyAcceptance` machinery (UBI-11, `ubx why --verify-acceptance`)
reused completely unchanged — this command's own job is to run it once
per `pr_merge`-accepted proposal the chain walk found, not reimplement
it. Given `--repo-dir`, the git-history half runs (no network); given
`--github-repo` too, the reviewer re-check also runs against the GitHub
API. Neither flag given: reported honestly as *inconclusive*, never a
silent pass. `local` acceptance is reported as convenience-tier — there
is nothing to independently re-derive it against, and claiming
otherwise would be exactly the kind of rounding-up this command exists
to refuse.

Exit codes follow the same UBI-20 contract every other verb does: 0
everything checks out; 1 a real finding (a broken hash, a chain break, a
failed acceptance re-derivation); 2 a genuine error (a corrupt head,
a network/tool failure re-deriving acceptance) — "couldn't check" and
"checked and found a problem" are never conflated into the same exit
code. Works against both git-local and remote `LedgerStore` backends
identically (`Chain`/`Read`/`ApplyAttempts` are all implemented purely
in terms of the store-agnostic `LedgerStore` interface already).

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
