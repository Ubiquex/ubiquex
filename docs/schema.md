# Schema Constitution — Proposal & IR (v0.1 DRAFT)

> The most load-bearing document in the project. Hashing rules are effectively
> unfixable after the first real ledger exists — settle them before Slice 2 writes
> a hash. Everything here is draft until marked ratified.

## Versioning

Every persisted object carries `schema_version` (integer). The ledger is forever;
readers must support all prior versions or provide migration. Start at `1`.

## IR — resource node (draft)

```json
{
  "schema_version": 1,
  "kind": "resource",
  "type": "aws_db_instance",
  "name": "payments-db",
  "stack": "payments",
  "provider": { "source": "registry.terraform.io/hashicorp/aws", "version": "6.x" },
  "config": { "...": "typed values; see value encoding below" },
  "refs": [
    { "kind": "intra", "to": "aws_vpc.main.id" },
    { "kind": "cross", "to": "@network.vpc_id", "pinned_head": "7fc2..." }
  ],
  "lifecycle": { "status": "pending | in_flight | unknown_post_timeout | applied | failed" }
}
```

Value encoding (draft):
- Concrete values: JSON scalars/objects/arrays.
- Computed: `{ "$computed": { "from": "aws_db_instance.payments-db.endpoint" } }` —
  type-level distinction between known-now and known-after-apply.
- Secret refs: `{ "$secret": { "ref": "db-password" } }` — NEVER material.
- Ephemeral: `{ "$ephemeral": true, ... }` — excluded from persisted state.

## Proposal (draft)

```json
{
  "schema_version": 1,
  "id": "<content-hash, short form for display>",
  "stack": "payments",
  "parent": "<previous ledger head hash>",
  "kind": "change | adoption | drift_adopt | drift_revert | revert",
  "intent": {
    "summary": "postgres for payments, modeled on staging, ~50% capacity",
    "sources": [
      { "kind": "dialogue", "ref": "d-99f2", "content_hash": "sha256:9f2a..." },
      { "kind": "manual_edit", "ref": "PR #315", "content_hash": "sha256:1b7c..." },
      { "kind": "issue", "ref": "UBX-241", "content_hash": "sha256:4e0d..." }
    ]
  },
  "delta": {
    "creates": [ "<IR nodes>" ],
    "modifies": [
      {
        "target": { "stack": "payments", "type": "aws_db_instance", "name": "payments-db" },
        "before": { "instance_class": "db.t3.medium" },
        "after":  { "instance_class": "db.t3.large" }
      }
    ],
    "destroys": [
      { "stack": "payments", "type": "aws_db_instance", "name": "old-payments-db" }
    ]
  },
  "resolution": {
    "resolved_at": "2026-07-10T...Z",
    "inputs": [
      {
        "kind": "live_state",
        "resource": "payments.aws_db_instance.payments-db",
        "observed_hash": "sha256:9f2a...",
        "lookup": { "id": "payments-db", "bucket": "payments-db" }
      }
    ]
  },
  "cost_delta": { "monthly_usd": 59 },
  "blast_radius": { "creates": 0, "modifies": 1, "destroys": 0 },
  "invariants_checked": [ { "policy": "no_public_db", "verdict": "pass" } ],
  "acceptance": {
    "method": "pr_merge | local | crypto",
    "merge_sha": "8c1d2e...",
    "pr_number": 42,
    "approvers": [ "roozbeh" ],
    "accepted_at": "..."
  },
  "status": "draft | refined | accepted | applied | stale | rejected"
}
```

### Delta element shapes — PINNED (2026-07-10)

`delta.creates` stays an array of IR resource nodes (see §IR above); the IR
node schema itself isn't fully built out yet, but its `stack`/`type`/`name`
fields are already load-bearing (canonical hashing sorts by them).
`delta.modifies` and `delta.destroys` were the draft's "..." placeholders;
both are now pinned:

- **Address** — `{ "stack": "...", "type": "...", "name": "..." }`. This is
  `delta.destroys`' element shape directly, and `delta.modifies[].target`'s
  shape. Its canonical string form is `<stack>.<type>.<name>` (e.g.
  `payments.aws_db_instance.payments-db`) — this is what
  `resolution.inputs[].resource` must contain when it backs a modification
  (see below).
- **Modification** — `{ "target": Address, "before": {...}, "after": {...} }`.
  `before`/`after` hold ONLY the attributes that changed, not full resource
  state, keyed by dot-notation attribute path for nested values (e.g.
  `"tags.Environment"`, not a nested `"tags": {"Environment": ...}` object).
- **Every `delta.modifies` entry MUST have a corresponding
  `resolution.inputs` entry** whose `resource` equals that entry's
  `target`'s canonical address string, with a non-empty `observed_hash`
  covering that resource's observed state. This is enforced as **propose-time
  validation** (a proposal missing this is rejected before it can be
  hashed/accepted, not just discouraged by convention) — a modification's
  claimed "before" must be provable against what was actually observed, not
  merely asserted. `delta.creates` has no equivalent requirement.

Since these shapes feed the canonical hash's `(stack, type, name)` sort
directly, changing them again is a hashed-content shape change — same
migration bar as §Canonical hashing (schema_version bump required).

### Amendment: persist resource lookup key (2026-07-10, UBI-7 follow-up)

`resolution.inputs[].lookup` is added: the JSON object passed to the
provider's `ReadResource` to identify the resource (e.g.
`{"id": "...", "bucket": "..."}`), for `kind: "live_state"` entries.
Without it, re-verifying a proposal's observation later (`ubx accept
--reverify-with`) required the caller to already know, and re-supply
byte-for-byte, exactly what `ubx scan` used the first time — workable but
brittle, and a dead end for anything beyond one CLI invocation talking to
itself.

This does **not** require a `schema_version` bump, unlike the delta
element shapes above: it's a purely additive, optional field. Existing
ledger entries authored before this amendment simply don't have it and
remain exactly as valid as they always were (`omitempty`, and no reader
ever required it) — their hashes are unaffected because their content is
unaffected; only proposals authored from now on will include it, and their
hash correctly reflects that they now carry more information than before.
That's ordinary content hashing working as intended, not a rule change.
Contrast with §Canonical hashing's RATIFIED rules (domain prefix, exclusion
list, number encoding, array sort) or the pinned delta shapes above:
changing any of *those* changes how hashing itself works, or what "the
address of a modifies entry" even means, which does need a version bump
and a migration path. Adding an optional field elsewhere in the tree does
not.

### Amendment: CloudTrail attribution intent sources (2026-07-10, UBI-10)

Two new `intent.sources[].kind` values, both attached to `drift_adopt`
proposals only (attribution answers "who/when did this drift," which only
means something once a drift has already been detected and recorded):

- **`cloudtrail`** — a matched CloudTrail management event. Carries, in
  addition to the existing `ref`/`content_hash` fields:
  `event_id`, `event_name`, `event_time` (RFC3339), `actor_arn`
  (`userIdentity.arn` from the event record — the IAM principal that made
  the change), `source_ip`, and `session_context` (the raw
  `userIdentity.sessionContext` object, opaque — present for assumed-role
  callers, e.g. SSO, absent for long-lived IAM users). `ref` is the event
  ID (a human-usable locator, same role `ref` plays for dialogue/PR/issue
  sources); `content_hash` is `sha256:<hex>` of the raw `CloudTrailEvent`
  JSON record — tamper-evidence, same reasoning as every other
  `content_hash` use in this document. **Multiple matching events attach
  as multiple `cloudtrail` sources, newest `event_time` first** — ubx never
  guesses which of several candidate events caused the drift; if more than
  one real management event touched the resource in the correlation
  window, all of them are recorded and a human decides.
- **`cloudtrail_unattributed`** — attribution was attempted and failed,
  recorded as evidence in its own right rather than silently producing a
  drift_adopt proposal with no intent.sources at all. Carries `reason`,
  one of:
  - `no_matching_event` — the correlation window was searched successfully
    and wide enough to rule out delivery latency (see below), but no
    event matched this resource's identity.
  - `delivery_window` — the correlation window is narrower than
    CloudTrail's known event-delivery latency (~15 minutes for
    `LookupEvents`), so a real causal event may simply not have
    propagated yet; distinct from `no_matching_event`, which asserts a
    search that could actually rule something out.
  - `not_logged` — the CloudTrail `LookupEvents` call itself could not be
    made (denied credentials, no CloudTrail visibility in this
    account/region, or any other API-level failure) — ubx has no
    visibility into whether an event exists at all, as opposed to having
    searched and found nothing.

Correlation always runs over `[last ledger observation, scan time]` — the
resolved_at of whichever proposal most recently recorded this address's
state, through the current scan's own `resolved_at`. Matching is against
whichever of the resource's own observed `id`/`arn`/`name` attributes
CloudTrail's `ResourceName` lookup attribute actually recognizes for that
service — empirically NOT always the ARN (see Surprises in STATE.md: for
`aws_s3_bucket`/`aws_iam_role`/`aws_vpc`, `id` is the value CloudTrail
expects; searching by the full ARN returns nothing even for genuinely
matching events). ubx tries the resource's own `id` first, falling back to
`arn` then `name` only if `id` doesn't turn up a match — never assumed
from one resource type, checked per attempt.

Attribution is **best-effort by construction**: a `cloudtrail_unattributed`
source is exactly as valid a proposal as one with `cloudtrail` sources —
attribution failure of any kind (denied credentials, no matching event,
delivery lag) never blocks or delays generating and accepting the
underlying drift_adopt proposal. The drift itself is always recorded;
who/when caused it is best-effort evidence layered on top, not a
precondition.

Same reasoning as the lookup-key/provider-checksum amendments above:
purely additive and optional (new `kind` values, new optional fields on
`IntentSource`), no `schema_version` bump — existing proposals with
`dialogue`/`manual_edit`/`issue` sources are entirely unaffected, and the
hash of a proposal that does carry these sources reflects exactly the
content it actually has, same as any other content hash.

### Amendment: `audit_unattributed` and `gcp_audit` (2026-07-16, UBI-21)

`ubx` gaining a second platform (GCP, docs/architecture.md — GCP support)
means attribution failure can no longer be assumed to mean "CloudTrail
had nothing" — it might mean GCP's Cloud Audit Logs backend (`gcpaudit/`,
implemented and live-verified in Stage 2 of this same session) had
nothing instead. Two new `intent.sources[].kind` values:

- **`gcp_audit`** — a matched Cloud Audit Log entry, GCP's counterpart to
  `cloudtrail`. Reuses `cloudtrail`'s own fields rather than introducing
  GCP-specific ones: `actor_arn` carries the GCP principal's email
  address (not literally an ARN), `event_id`/`event_name`/`event_time`/
  `source_ip` map onto Cloud Logging's insert ID/method name/timestamp/
  caller IP directly, and `session_context` is always absent (an
  AWS-assumed-role-specific concept with no GCP equivalent). This was a
  live implementation decision, not assumed in advance: both backends
  implement the same `core.EventLookup` interface and produce the same
  `core.CloudTrailEvent` shape (see `gcpaudit/client.go`'s own doc
  comment), so reusing the existing fields was the honest, non-forcing
  choice once it was actually built, rather than the GCP-specific-fields
  possibility this document originally left open.
- **`audit_unattributed`** — generalizes `cloudtrail_unattributed`:
  - Same `reason` enum (`no_matching_event`, `delivery_window`,
    `not_logged`) — an audit-log backend's own delivery latency and
    correlation semantics are the same *shape* of problem regardless of
    platform, even though the actual latency numbers differ (CloudTrail's
    ~15-minute documented ceiling, ~2 minutes measured in UBI-10; GCP
    Cloud Audit Logs has no equivalent published ceiling — measured
    directly in Stage 2 at roughly 18 seconds for one Pub/Sub mutation,
    `gcpaudit.Backend`'s own `DeliveryLag` set well above that single
    measurement as a safety margin, not tuned tightly to it).
  - One new field, **`backend`**: `"gcp_audit_logs"` today, more values
    as more backends are added — which platform's attribution attempt
    this records the failure of. `cloudtrail_unattributed` (the
    pre-existing kind) carries no `backend` field at all, since it was
    never ambiguous which backend it meant.

**`cloudtrail_unattributed` is not deprecated or migrated** — it remains
a permanently valid kind, and `cloudtrail/`'s own backend (`cloudtrail.Backend`)
keeps emitting it unchanged, not `audit_unattributed` — a deliberate,
conservative choice made during Stage 2 implementation: every existing
and newly-generated AWS proposal's attribution shape stays byte-identical
to before UBI-21, and only the newer GCP backend uses the generalized
kind, since there's no existing AWS output to preserve compatibility with
there. `audit_unattributed`/`gcp_audit` are additive: `schema_version`
does not bump, existing tooling reading `cloudtrail`/`cloudtrail_unattributed`
needs no update, and a reader that doesn't yet know about the two new
kinds simply doesn't recognize kinds it hasn't been taught yet — the same
purely-additive posture as every prior amendment in this document.

**Known gap, found during Stage 2 live verification, not silently
resolved**: which value a GCP service's own audit log entry uses for its
resource identifier is not consistent across services. Pub/Sub's audit
entries name a topic as `"projects/<PROJECT_ID>/topics/<name>"` —
matching `google_pubsub_topic`'s own observed `id` attribute exactly, so
correlation (`core.AttributeDrift`'s `identityCandidates`, unchanged
since UBI-10) works. Secret Manager's entries instead name a secret as
`"projects/<PROJECT_NUMBER>/secrets/<name>"` — the numeric project
number, which never appears in `google_secret_manager_secret`'s own
observed state at all, so `identityCandidates` can never produce a
matching candidate; every secret's drift is unattributable via
`gcpaudit/` today, indistinguishable from a genuine no-event case (always
`audit_unattributed`/`no_matching_event`). See `gcpaudit/client.go`'s own
doc comment and STATE.md for the full writeup — this needs either a
per-service resourceName-shape table or a project-number lookup added as
a candidate, follow-up work, not done under time pressure here.

### Amendment: record verified provider binary checksum (2026-07-10, UBI-8)

`resolution.inputs[].provider_checksum` is added: `"sha256:<hex>"` of the
exact provider binary (the extracted executable, not the release archive)
that produced this observation, once acquired and signature-verified via
`provider.Acquire` (see docs/architecture.md — Provider binary acquisition).
Distinct from `observed_hash`, which fingerprints the *resource's* state —
this fingerprints the *tool* that read it, attribution evidence for `ubx
why` to eventually surface alongside who/when. Empty when the caller used a
hand-picked `--provider` path rather than an acquired/verified one — there
is nothing to attribute in that case beyond what the operator already
knows.

Same reasoning as the lookup-key amendment above: purely additive and
optional, no `schema_version` bump.

### Amendment: `pr_merge` acceptance fields (2026-07-11, UBI-11 stage 1)

`acceptance.pr_number` is added: the GitHub pull request number the
`merge_sha` belongs to, for `method: "pr_merge"` acceptances. Purely a
convenience — everything needed to re-derive acceptance is already
derivable from `merge_sha` alone (the GitHub API resolves a commit to its
merged PR), but recording the PR number directly means `ubx why` and any
future re-verification don't need that extra lookup just to print a link
or re-fetch reviews. Additive/optional, no `schema_version` bump — same
reasoning as every amendment above: `acceptance` is entirely excluded
from the content hash (see §Canonical hashing), so nothing about its
shape is load-bearing for the hash chain in the first place.

The full `pr_merge` acceptance shape, all fields already present in the
Proposal draft's own example at the top of this document:

```json
"acceptance": {
  "method": "pr_merge",
  "merge_sha": "8c1d2e...",
  "pr_number": 42,
  "approvers": ["roozbeh"],
  "accepted_at": "2026-07-11T00:00:00Z"
}
```

**Derived, never asserted** (docs/architecture.md — Decision loop, PR-merge
acceptance): every field above except `accepted_at` is independently
re-checkable for the life of the ledger, against git history (`merge_sha`
must exist, and the proposal file's content at that commit must hash to
exactly this proposal's `id`) and the GitHub API (`approvers` is every
reviewer whose *most recent* review on `pr_number` is `APPROVED` — a
later `CHANGES_REQUESTED` from the same person supersedes an earlier
approval; nothing here is asserted by whoever ran `ubx accept
--from-merge` and trusted at face value the way `method: "local"`
trusts `os/user`). `approvers` MAY be an empty array — a merge with zero
approving reviews is recorded exactly as it happened, not rejected;
enforcing "this needed N approvals" is entirely GitHub's job (branch
protection), never ubx's. See docs/architecture.md for the full flow and
the `--verify-acceptance` re-check `ubx why` gains alongside this.

The convention this acceptance tier depends on, also pinned here since
it's part of what "derived" means in practice: the PR body that gets
merged MUST carry a line matching `^ubx-proposal: [0-9a-f]{64}$` (the
exact hash `ubx propose` printed when the proposal was first resolved,
before review). `ubx accept --from-merge` treats a missing trailer, or one
whose hash doesn't match the proposal file's own recomputed hash, as a
hard failure — see docs/architecture.md's "hash mismatch" case.

### Amendment: `drift_revert` proposals (2026-07-16, UBI-16)

`kind: "drift_revert"` was already an enumerated value in this document's
own Proposal example (`"kind": "change | adoption | drift_adopt |
drift_revert | revert"`) with no rules behind it. This amendment pins those
rules — see docs/architecture.md's "Revert path" section for the full
design and rationale; this is the schema-constitution half.

A `drift_revert` proposal is the *corrective* counterpart to a
`drift_adopt` generated from the same drift observation (`ubx scan
--propose revert|both`):

- **`delta.modifies`** uses the same `Modification` shape as every other
  modifies entry (§Delta element shapes), but with `before`/`after`
  reversed relative to `drift_adopt`'s convention: `before` is the
  *observed* (drifted) value, `after` is the *ledger-recorded* value being
  restored to. At least one `delta.modifies` entry is required — a revert
  with nothing to revert is invalid. `delta.creates` and `delta.destroys`
  MUST both be empty — revert only ever corrects existing attributes.
- **`blast_radius` is real, not all-zero** — the one place `drift_revert`
  differs from every other drift/adoption kind. `blast_radius.modifies`
  MUST equal the number of `delta.modifies` entries exactly;
  `blast_radius.creates`/`.destroys` MUST both be zero (still no
  creates/destroys). This is enforced as propose-time validation, same as
  every other kind-specific rule in this document. The reason: accepting a
  `drift_adopt`/`adoption` records something that already happened (hence
  zero blast radius — nothing is *about* to change); accepting a
  `drift_revert` is a decision that something is *about to* change in
  cloud (via `ubx revert-plan`'s emitted plan, applied by the operator's
  own tooling) — that's a real blast radius by the same definition every
  other non-zero blast radius in this document already uses.
- **`resolution.inputs`** follows the existing cross-reference rule
  unchanged (§Delta element shapes: every `delta.modifies` target needs a
  matching `resolution.inputs` entry with a non-empty `observed_hash`) —
  and that `observed_hash` is the *observed/drifted* state's hash, the same
  value a `drift_adopt` generated from the same scan would carry, **not**
  the restore-target's hash. This is what lets `accept --reverify-with`/
  `--reverify-source` keep meaning "has reality moved again since this was
  drafted?" — comparing against the restore target instead would make that
  check meaningless (it would never match anything currently live, since
  the whole point of the drift is that live state doesn't match the
  restore target yet).
- **No `schema_version` bump** — `drift_revert` was already a legal enum
  value in the schema's own Proposal example; this amendment only pins
  behavior for a value that already existed, the same posture as every
  other post-ratification amendment in this document.

Notes:
- `id` is a content hash (git's lesson) — no sequential numbering; human-friendly
  aliases allowed as labels.
- `parent` forms the per-stack hash chain. `ledger.lock` records the current head.
- Staleness: any `resolution.inputs` observed_hash mismatch, parent advancement
  conflict, or pinned cross-stack head advancement ⇒ status becomes `stale`;
  re-resolution required before acceptance/ship.
- **Adoption proposals** (kind `adoption`) MUST have all-zero `blast_radius`
  and empty `delta.modifies`/`delta.destroys` (record-only, enforced as
  propose-time validation, kind-specific). `delta.creates` MAY be populated —
  adoption records the resource's IR node into the ledger — but since
  `blast_radius` is all-zero, the executor must never treat it as a real
  create.
- **Drift-adopt proposals** (kind `drift_adopt`, pinned 2026-07-10 alongside
  `ubx scan` — see docs/plan.md §Slice 3) MUST also have all-zero
  `blast_radius` and empty `delta.destroys` (record-only against the cloud,
  same as adoption). Unlike adoption, `delta.modifies` IS expected — a
  drift_adopt's whole point is recording a change that already happened in
  reality, using the same `Modification` shape as any other modifies entry
  (and therefore the same `resolution.inputs` cross-reference requirement
  above).
- **Drift-revert proposals** (kind `drift_revert`, pinned 2026-07-16,
  UBI-16 — see "Amendment: drift_revert proposals" below) are the opposite
  of drift-adopt in exactly one respect: `blast_radius` is real, not
  all-zero. Everything else about the record-only/cross-reference
  discipline above still applies.
- `acceptance` binds a signature to the exact hash. Timestamps and acceptance data
  live OUTSIDE the hashed content (see below).
- `intent.sources[].content_hash` is a SHA-256 content hash (`sha256:<hex>`) of the
  referenced dialogue/PR/issue content at resolution time — tamper-evidence for
  intent evidence, which otherwise lives outside the proposal's own hash chain
  (dialogues can be edited or deleted upstream; the content_hash catches that).

### Amendment: `$redacted` value encoding (2026-07-17, UBI-23)

Secrets must never enter the ledger — not even as an accidental byproduct
of adopting or drift-recording a resource that happens to have one. This
amendment pins the JSON *value* shape any provider-`Sensitive`-flagged
attribute takes, wherever it would otherwise appear in hashed content:
`resolution.inputs[].observed_hash`'s source data, `delta.creates`' state
snapshots, and `delta.modifies[].before`/`after`.

A redacted value is:

```json
{ "$redacted": { "sha256": "<hex>" } }
```

replacing the ENTIRE attribute value — whatever its own type (scalar,
list, set, map, nested object) — never a partial/field-level redaction
within it. `sha256` is `sha256(salt || canonical-json-bytes-of-the-real-value)`,
where `salt` is a 32-byte value generated once per ledger directory on
first use and persisted at `.ubx/salt` (never itself part of any hashed
content, never committed — see docs/architecture.md's "Secrets" section
for the salt's own lifecycle and the honest recovery-implication tradeoff
of losing it). "Canonical" here means the same decode-then-remarshal
convention `ObservedHash` already uses (sorted object keys, no
insignificant whitespace) — consistent with every other canonicalization
rule in this document, not a new one.

**This is a value-shape convention, not a new field** — `$redacted` can
appear anywhere an attribute value already could, so no `schema_version`
bump is needed, the same reasoning as every additive amendment above. A
reader that has never heard of `$redacted` still sees perfectly valid
JSON at that position; it just won't know to render it specially. What a
reader/writer of this document's canonical hashing rules DOES need to
know: a `$redacted` object is **atomic** for diff purposes — comparing two
resource states never recurses into one (see below); it is compared and
hashed exactly like any scalar value would be.

**Where this applies**: `provider.Redact` (see docs/architecture.md)
walks a resource's schema `Sensitive` flags over its observed state
immediately after every live `ReadResource` call, before that state is
handed back to `core` for fingerprinting/hashing/diffing. By the time
`core` ever sees observed state, any sensitive attribute is already in
`$redacted` form — `core` has no schema knowledge and never decides what's
sensitive; it only recognizes the `$redacted` shape once it's already
there.

**Amendment (2026-07-18, UBI-24): the provider's own schema is a floor,
not a ceiling.** `provider.Redact` also consults a small, ubx-owned
override table (`provider/overrides.go`'s `SensitiveOverrides`, keyed by
the same `(provider source, type)` pair `conformance.Registry`/
`core/lookuphints` already use) naming additional attribute paths to
redact regardless of what the upstream schema flags — closing a real gap
UBI-22 found: `helm_release.manifest`/`metadata.values`/`metadata.notes`
can carry a sensitive value's plaintext (rendered into a chart's output)
without ever being `Sensitive`-flagged upstream. Redaction is the
**union** of schema flags and this table; the table can only ever ADD an
attribute to what gets redacted, never remove one the schema itself
flags. This is purely a `provider`-internal decision about *what*
triggers a `$redacted` value — the wire shape above, and everything
`core` does with it, is completely unchanged. See docs/architecture.md's
"Sensitive overrides" section for the full mechanism.

**Diff behavior**: `core`'s attribute-diff (`diffAttributes`/`diffObjects`)
treats a `$redacted`-shaped value as atomic, not as a nested object to
recurse into. Two equal `$redacted` values (same salt, same underlying
secret — same `sha256`) diff as unchanged, exactly like two equal scalars
would; two different `$redacted` values (the underlying secret actually
changed) diff as a whole-attribute change, `before`/`after` both holding
the complete `$redacted` object at that dot-path — never a spurious
`attr.$redacted.sha256: <hash1> -> <hash2>` sub-path, which would be
technically accurate but violate "before/after hold only the attributes
that changed" at the wrong granularity for what's actually a single
attribute's value changing.

**Recovery implication of losing the salt, stated honestly**: regenerating
`.ubx/salt` (lost, deleted, or a fresh checkout of a ledger directory
whose salt was never itself committed — correctly so) means every
subsequently-computed `$redacted` hash for a given real secret value no
longer matches what's already recorded in ledger history, even though the
real value hasn't changed. The next scan reports every sensitive attribute
as drifted — a false positive, not a missed detection. This is the
deliberate, disclosed tradeoff: losing the salt degrades *equality
comparison across history*, it does not ever cause a real secret to leak,
and it does not cause a real change to go undetected (a scan always
re-reads live state fresh; nothing about salt loss makes drift detection
blind, only noisier for sensitive attributes specifically, exactly once,
until the next observation re-settles against the new salt).

### Amendment: `k8s_audit` and `not_configured` (2026-07-17, UBI-22)

`ubx` gaining Kubernetes/Helm support (docs/architecture.md — Kubernetes
support) means a third attribution backend, alongside `cloudtrail`
(UBI-10) and `gcp_audit` (UBI-21): `k8saudit/`, against EKS control-plane
audit logs delivered to CloudWatch Logs. One new `intent.sources[].kind`
value, plus one new `reason`:

- **`k8s_audit`** — a matched Kubernetes audit event
  (`audit.k8s.io/v1`'s own `Event` schema). Reuses `cloudtrail`/`gcp_audit`'s
  own fields rather than inventing Kubernetes-specific ones, the same
  choice UBI-21 made for GCP: `actor_arn` carries the acting user's
  `user.username` (not an ARN or email — whatever identity string the
  cluster's authentication layer reports), `event_name` carries the
  audit event's `verb` (`create`/`update`/`patch`/`delete`, Kubernetes'
  own vocabulary, not an AWS/GCP-style `PutBucketTagging`-shaped name),
  `event_time` from `requestReceivedTimestamp`, `event_id` from
  `auditID`, `source_ip` from `sourceIPs[0]` when present.
  `session_context` is always absent (an AWS-assumed-role-specific
  concept with no Kubernetes equivalent, same as GCP's own sources).
- **`not_configured`** — a fourth value for the existing `reason` enum
  (`no_matching_event`, `delivery_window`, `not_logged`, now
  **`not_configured`**), used only by `audit_unattributed` sources whose
  `backend` names a backend that requires operator configuration `ubx`
  has no way to derive on its own (`k8s_audit_logs` in v1 — unlike AWS's
  region or GCP's project, there is nothing in `provider_config` that
  implies "which EKS cluster, which CloudWatch log group"). Recorded
  exactly like every other unattributed reason — evidence in its own
  right, never silently omitted — and, per this project's own
  best-effort-attribution posture (UBI-10), never blocks generating or
  accepting the drift proposal it's attached to. Distinguishing
  `not_configured` from `not_logged` matters for the same reason the
  other three reasons stay distinct from each other: an operator reading
  `ubx why` should be able to tell "I haven't set this up" apart from "I
  set it up and it's failing," without having to guess from context.

Both are purely additive: `schema_version` does not bump, an existing
reader that doesn't recognize `k8s_audit`/`not_configured` simply doesn't
render them specially, the same posture as `gcp_audit`/`audit_unattributed`
before it. `cloudtrail`/`cloudtrail_unattributed` and `gcp_audit`/
`audit_unattributed`'s existing three reasons are entirely unchanged.

**A correlation gap, checked and partially closed, not assumed clean
(mirroring UBI-21's own GCP precedent)**: which identity value a
Kubernetes audit event's `objectRef` can be matched against — a
`kubernetes_*` resource's own observed `id` attribute, since
`name`/`namespace`/`uid` live nested inside `metadata[0]` rather than at
the top level `identityCandidates` (`core/attribution.go`) reads from —
was checked against a real (local `kind`) cluster in Stage 2: `id`'s real
shape is `<namespace>/<name>`, exactly one of the candidate forms
`k8saudit.Client`'s own defensive `Resources`-building already offers.
What remains unverified is the EKS-audit-log leg itself (no real EKS
cluster with control-plane audit logging was available/provisioned this
session — a deliberate, recorded decision, not a silent gap). See
docs/architecture.md's "Kubernetes support" section for the full
reasoning, and STATE.md for the complete Stage 2 writeup.

### Amendment: apply records (2026-07-17, UBI-26)

Phase 2 opens: the executor. v1 scope is narrow and explicit — shipping
*accepted* `drift_revert` proposals only (see docs/executor.md for the full
design). This amendment pins the ledger object that records what actually
happened when `ubx ship` executes one: the **apply record**.

An apply record is a new, distinct object family — `ledger/applies/<id>.apply.json`
— not a modification to `Proposal`'s own shape. `Proposal.schema_version`
does not bump; nothing about what a proposal *is* changes. `ApplyRecord`
carries its own `schema_version`, starting at `1`, per this document's own
general versioning rule.

```json
{
  "schema_version": 1,
  "id": "<content hash, sealed only>",
  "proposal": "<the drift_revert proposal's own id>",
  "parent": "<previous sealed apply record's id for this proposal, or \"\" for attempt 1>",
  "attempt": 1,
  "resources": [
    {
      "address": { "stack": "payments", "type": "aws_db_instance", "name": "payments-db" },
      "transitions": [
        { "state": "pending", "at": "2026-07-17T18:00:00Z" },
        { "state": "in_flight", "at": "2026-07-17T18:00:01Z" },
        { "state": "applied", "at": "2026-07-17T18:00:04Z" }
      ],
      "provider_result": { "...": "ApplyResourceChange's returned attributes, redacted exactly like ReadResource's own observed state" },
      "reconciliation": [
        { "at": "2026-07-17T18:00:32Z", "outcome": "applied", "detail": "ReadResource confirms restored value" }
      ],
      "errors": [
        { "at": "2026-07-17T18:00:30Z", "message": "context deadline exceeded", "classification": "retryable" }
      ]
    }
  ],
  "summary": {
    "outcome": "applied",
    "started_at": "2026-07-17T18:00:00Z",
    "finished_at": "2026-07-17T18:00:32Z",
    "resources_applied": 1,
    "resources_failed": 0,
    "resources_still_unknown": 0
  }
}
```

### Hash-chained, two ways, one new domain

Like a Proposal, an apply record's `id` is a content hash — domain-prefixed
SHA-256 (`ubx:apply:v1\n`, same construction as `hashDomainPrefix`, its own
distinct domain so an apply record's hash can never collide with a
proposal's, or with any other object's, over the same canonical bytes),
excluding exactly `id` itself (the only self-referential field — an apply
record carries no `acceptance`/`status` fields of its own the way a
Proposal does, since it never needs a separate acceptance step; it *is* the
evidence, not something layered on top of one).

Two independent chain links, not one:

- **`proposal`** — the accepted `drift_revert` proposal's own `id`. This is
  the link "which proposal did this attempt execute," not a position in any
  chain.
- **`parent`** — the previous *sealed* apply record's `id`, for the *same*
  proposal. `""` for a proposal's first ship attempt. **Records are
  append-per-attempt: a re-run creates a new apply record chained to the
  prior one via `parent`, never edits an already-sealed one.** This mirrors
  `Proposal.Parent`'s own per-stack chain, one level down: where a stack's
  ledger chain is proposals chained by stack, one proposal's *apply history*
  is apply records chained by proposal.

### Sealed vs. live: why an apply record's `id` cannot exist from the start

Every other content-hashed object in this document (Proposal) is complete
and immutable from the moment it's first written — its hash is computed
once, over its full final content, before the first byte ever touches disk.
An apply record cannot work that way, and this is a deliberate divergence,
not an oversight: **THE invariant** driving the whole executor design
(docs/executor.md) is that a resource's state transition must be durably
persisted *before* the risky provider call it precedes (`in_flight` written
before `ApplyResourceChange` is called) — which means the apply record's
content necessarily accretes, transition by transition, over the course of
one running attempt. Its content isn't fixed, so its hash isn't fixed,
until the attempt reaches a terminal outcome.

Consequently: the on-disk filename during a live attempt is
`<proposal-id>.attempt-<N>.apply.json` — a deterministic name assigned
*before* content exists (`N` a monotonic per-proposal attempt counter,
assigned under the same ledger lock `Append` already uses, see
docs/executor.md's "double ship invocation racing" case), never a content
hash. `id` is populated, and the record is renamed/finalized, only once
`summary` is written and the attempt is sealed. A reader encountering a
`.apply.json` file with no `id` field yet is looking at an in-progress
attempt, not a corrupt one; a crash mid-attempt leaves exactly this
half-written state on disk, which is expected and handled (see the
adversarial program, docs/executor-adversarial.md), never silently
resumed by guessing.

### `Proposal.status` is never rewritten — "applied"/"partially_applied" are reported, not stored

`docs/schema.md`'s own draft Proposal shape has always listed `applied` as
a legal `status` value (`"status": "draft | refined | accepted | applied |
stale | rejected"`), and this session adds `partially_applied` alongside
it. But `core.Ledger.Append` enforces — structurally, not just by
convention — that **ledger entries are immutable once written**
(`ErrDuplicateProposal`; there is no update path anywhere in this codebase
for a proposal already on disk). Those two facts would conflict if
"status may move to applied" meant rewriting the stored `.prop.json`
file's own `status` field after the fact.

It doesn't. The stored proposal file's `status` is fixed forever at
`Accept`-time (`accepted`, for everything this codebase generates today)
and is **never** rewritten to `applied`/`partially_applied` in place —
doing so would mean mutating a hash-chained ledger entry, which nothing
else in this system ever does. Instead, `applied`/`partially_applied` are
**derived, reported values**: any consumer wanting a proposal's effective
status (`ubx why`, `ubx status`, `ubx ship` itself) computes it by walking
`ledger/applies/` for that proposal's `id`, taking the most recent sealed
apply record's `summary.outcome`, and folding it over the stored
`accepted` status — the same "immutable history, current truth computed by
folding over it" posture `core.FoldState` and `core.Ledger.Chain` already
use elsewhere in this codebase, applied one level up. **The evidence lives
in the apply record; the proposal file's own `status` field is not the
evidence and is never asked to be.**

### Redaction applies here too

`provider_result` (the attributes `ApplyResourceChange` returns) is exactly
as capable of carrying a live secret as `ReadResource`'s own observed state
— it comes from the same provider, over the same wire protocol, describing
the same resource. It MUST be passed through `provider.Redact` (the same
schema-`Sensitive`-flags-plus-override-table union UBI-23/24 already built)
before ever being written into an apply record, using the same per-ledger
salt (`.ubx/salt`). No new redaction mechanism is needed or introduced —
this is a reuse, not an amendment to how `$redacted` itself works.

### No `schema_version` bump on `Proposal`

Everything above is either an entirely new object family (`ApplyRecord`,
own `schema_version`) or a purely additive `status` enum value
(`partially_applied` — `draft`/`refined`/`accepted`/`applied`/`stale`/
`rejected` were already legal values in the schema's own draft; adding one
more to the documented set changes no existing proposal's meaning, exactly
the same reasoning as every additive amendment above). Nothing about
`Proposal`'s own hashed-content shape changes.

### Amendment: intent files and resolved `change` proposals (2026-07-17, UBI-27)

`kind: "change"` has been a legal `Proposal.Kind` enum value since this
document's very first draft, never produced by anything until now
(docs/resolver.md — Resolver v1). This amendment pins two things: the
hand-authored input format the resolver consumes, and the exact shape of
the resolved output it produces (closing real gaps the original IR-node
draft, at the top of this document, left open).

#### The intent file: `ubx:intent/v1`

Deliberately machine-shaped — the pretty frontends (diagram/markdown/SDK/
LLM-authored intent) are explicit future phases (docs/architecture.md's
component map #7/#10); this format exists to be emitted by a resolver
session's own tests today and by a real frontend later, not hand-typed by
an end user in production.

```json
{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {
    "summary": "provision a read replica for the payments database",
    "sources": [{ "kind": "manual_edit", "ref": "PR #412" }]
  },
  "resources": [
    {
      "type": "aws_db_instance",
      "name": "payments-db-replica",
      "op": "create",
      "config": {
        "instance_class": "db.t3.medium",
        "replicate_source_db": { "$ref": { "to": "payments.aws_db_instance.payments-db.id" } },
        "master_password": { "$secret": { "backend": "aws_secrets_manager", "path": "payments/replica-password" } }
      }
    },
    {
      "type": "aws_db_instance",
      "name": "payments-db",
      "op": "modify",
      "config": { "instance_class": "db.t3.large" }
    }
  ]
}
```

- **`intent`** reuses `Proposal.Intent`'s own shape exactly (`summary`,
  `sources[]`) — no new intent-evidence convention.
- **`resources[].op`** is `"create"` or `"modify"`, always explicit, never
  inferred from ledger presence — a real design choice, see
  docs/resolver.md's own "`op`: explicit, not inferred" section for why
  (inferring it would make "modify intent whose target isn't in the
  ledger," docs/resolver-adversarial.md's own required row, uncatchable —
  the resolver would just silently treat it as a create instead).
  Validated at resolve time: `create` requires the address to be absent
  from the ledger's `FoldState`; `modify` requires it to be present.
- **`resources[].config`** is the resource's full desired end-state
  (never a hand-computed before/after diff) — for `modify`, the resolver
  diffs this against `FoldState` via the existing `diffAttributes`
  (already shared by `GenerateProposal`/`GenerateRevertProposal`), the
  same mechanism drift detection already uses, now a third caller of it
  rather than a new one.
- **`config` values** may be plain JSON scalars/objects/arrays (concrete),
  or one of two new **input-only** markers, resolved away and never
  appearing in the output proposal:
  - **`$ref`** — `{ "$ref": { "to": "<address>.<path>" } }`, an intra-stack
    reference (docs/resolver.md's own resolution rules: substituted with a
    concrete literal, or a `$computed` marker, depending on whether the
    referenced attribute is schema-`Computed`).
  - **`$cross`** — `{ "$cross": { "ledger_dir": "...", "to": "<address>.<path>" } }`,
    a cross-stack reference, resolved against the neighbor ledger's own
    `FoldState` (docs/resolver.md's own cross-stack section). `ledger_dir`
    is an explicit filesystem path — this does not resolve
    docs/schema.md's own still-open "cross-stack workspace index format"
    question (see "Open questions," below); it's v1's own simple, explicit,
    sufficient answer for now, same posture v1 XCL's own sibling-directory
    convention already had. **Corrected during implementation** (UBI-27
    session 2, found by actually writing the resolver, not assumed correct
    from the design doc alone): the session-1 draft of this shape had a
    separate `"stack"` field alongside `"path"`, which never actually named
    the target resource's own `type`/`name` at all — structurally
    incomplete. Fixed to reuse `$ref`'s own `to` shape exactly
    (`<stack>.<type>.<name>.<path>`), with `ledger_dir` as the one
    genuinely new field — consistent with `$ref`, not a second convention.
  - Existing `$secret`/`$ephemeral` markers (already drafted in this
    document's own founding "IR — resource node" section) are used exactly
    as originally drafted — no shape change.

#### `Delta.Creates`' full node shape, pinned for real

The original IR-node draft (top of this document) sketched a much richer
shape (`schema_version`, `kind`, `provider`, `refs`, `lifecycle`) than what
`adoption`/`drift_adopt` proposals have ever actually produced
(`core.GenerateProposal`: `{stack, type, name, state}` — record-only,
never needed dependency info). A `change` proposal's own creates genuinely
need dependency information `adoption` never did, but not the full
original sketch either — `lifecycle` is superseded by apply records
(UBI-26: a proposal's own status is never rewritten; evidence lives in the
apply record, not in a per-node lifecycle field), and `schema_version`/
`kind`/`provider` are redundant with information the outer `Proposal`
already carries. The pinned shape, additive alongside (never replacing)
adoption's own `state`-keyed one:

```json
{
  "stack": "payments",
  "type": "aws_db_instance",
  "name": "payments-db-replica",
  "config": { "...": "resolved: concrete/$computed/$secret/$ephemeral values, never $ref/$cross" },
  "depends_on": ["payments.aws_db_instance.payments-db"]
}
```

- **`config`** replaces `state` for a resolver-produced create (`state`
  describes what a resource *already, concretely* has — adoption's whole
  point; `config` describes what's being *submitted* to create it, which
  may include `$computed` placeholders `state` never needed to represent).
  Adoption's own `state`-keyed shape is completely unchanged.
- **`depends_on`** is an explicit list of canonical addresses (`Address.String()`
  form) — not inferred from array position, even though the *stored* array
  order also reflects dependency order (docs/resolver.md's own "dependency
  graph" section: array order is what the executor actually walks;
  `depends_on` is the authoritative, explicit, position-independent record
  of why). A `Modification` entry (`delta.modifies`) MAY also carry
  `depends_on`, for the same reason — an existing resource's own update
  can depend on a sibling create in the same batch (e.g. referencing a
  newly created resource's computed ID). This is a purely additive field
  on the already-pinned `Modification` shape (docs/schema.md's own
  "Delta element shapes — PINNED" section) — no re-pin, no version bump.

#### Cross-stack pin evidence: a new `resolution.inputs[]` kind

`resolution.inputs[]` (already a flexible, extensible list of "what this
proposal was resolved against") gains a new `kind`: `"cross_stack_pin"`,
alongside the existing `"live_state"`:

```json
{
  "kind": "cross_stack_pin",
  "resource": "networking.aws_vpc.main",
  "observed_hash": "sha256:<hex, of the resolved value pulled from the neighbor's FoldState>",
  "pinned_head": "<the neighbor ledger's Head() at resolve time>",
  "ledger_dir": "<the neighbor's own ledger directory, from $cross.ledger_dir>"
}
```

`pinned_head` and `ledger_dir` are the two genuinely new fields
(`ResolutionInput` gains both, optional, populated only for
`cross_stack_pin` entries) — purely additive, no `schema_version` bump,
same reasoning as every prior amendment to this struct (`lookup`,
`provider_checksum`). `ledger_dir` was found missing from the original
draft of this amendment while actually implementing it (`core/resolver`,
UBI-27 session 2): re-verifying a pin needs to know *where* to re-derive
the neighbor's current head from, and nothing else in a resolved proposal
records that. This is what activates neighbor-advance staleness for real:
re-deriving the neighbor ledger's current `Head()` (via `ledger_dir`) and
comparing against `pinned_head` catches "the neighbor moved since this was
resolved" the same way `VerifyFreshness` already catches "live cloud state
moved since this was resolved," one level up (a ledger, not a cloud
resource). `core/resolver.VerifyPins` implements exactly this re-check,
hermetically tested; wiring it into `ubx accept` is later CLI-session
work, not this one's.

#### ~~Validation: `change` proposals never carry destroys~~ — superseded, UBI-30

~~New propose-time structural rule (`core.Validate`, alongside the existing
per-kind switch): a `KindChange` proposal MUST have `len(delta.destroys) ==
0` and `blast_radius.destroys == 0` — unconditionally, not just "zero for
now" — matching docs/resolver.md's own v1 scope line (destroys are out of
scope entirely, not merely unproduced by today's resolver).~~ This rule
held from UBI-27 until this session; see "Amendment: destroys" below for
the rule that replaces it. `blast_radius.creates`/`.modifies` MUST equal
`len(delta.creates)`/`len(delta.modifies)` exactly — a `change` proposal's
blast radius is real (same posture `drift_revert` already established:
accepting one is a decision to actually change cloud, not a record of
something that already happened) — this half is unchanged and still
enforced.

#### No `schema_version` bump

Everything above is either a new, self-contained object family (the intent
file, its own `ubx:intent/v1` `kind` tag, never itself hashed or stored in
the ledger — it's the resolver's *input*, not ledger content) or purely
additive fields on already-pinned shapes (`Modification.depends_on`,
`ResolutionInput.pinned_head`, `Delta.Creates`' new but parallel `config`
key). Nothing about `Proposal`'s own ratified hashed-content shape, domain
prefix, or canonicalization rules changes.

### Amendment: apply-record lookup key + Fleet discovery (2026-07-17, UBI-29)

UBI-27 closed with one named, unfixed gap (STATE.md, docs/resolver.md and
docs/executor.md's own "Out of scope" entries): a shipped `change`
proposal's created resources are correctly applied and durably recorded
in their own apply record, but invisible afterward to `ubx status`/`ubx
why <address>`/a future `ubx scan`. `core.Ledger.Fleet` (what `status`
walks), `ProposalsForAddress`/`LastObservedHash`/`LastObservationTime`
(what `why <address>` and CloudTrail attribution key off), and
`FoldState` (what `scan`/`status --drift` diff live reality against) all
discover a resource's existence exclusively via a
`resolution.inputs[].resource` entry or `Delta.Creates`' adoption-shaped
`state` key — and a `change` proposal's own create populates neither: it
was never *observed*, and its real identity isn't known until `ship`
applies it, well after the proposal's content hash is sealed, so nothing
could retroactively add either one.

#### `ResourceApply` gains `lookup`

```json
{
  "address": { "stack": "payments", "type": "aws_sqs_queue", "name": "chain-a" },
  "transitions": [ "...": "unchanged" ],
  "provider_result": { "id": "https://sqs.../ubx-ubi27-chain-a", "url": "...", "...": "..." },
  "lookup": { "id": "https://sqs.../ubx-ubi27-chain-a" }
}
```

Recorded explicitly, at ship time, the moment a create's `ApplyResourceChange`
call succeeds — never derived on demand by a later reader. This is a
deliberate bias, not the only option considered: Fleet/why/status could
instead re-derive a lookup key from `provider_result` every time they read
one, with no new field at all. The **Slice 3 lookup-key lesson**
(docs/schema.md's own "Amendment: persist resource lookup key," UBI-7
follow-up — the same reasoning that put `resolution.inputs[].lookup` on
the ledger in the first place, rather than re-deriving a scan's lookup key
from the resource's own state every time it was needed) applies again
here unchanged: re-reading a resource must never depend on derivation at
need-time. `lookup` is `{"id": "<value>"}` — every real provider schema
this codebase has touched declares an `id` attribute as the resource's own
primary identifier (`core/lookuphints`' own stored table is about a
*misleading alternative* key a user might reach for instead of `id`, never
about `id` itself being insufficient — see `lookupHintText`'s own doc
comment). If `provider_result` has no non-empty `id` at all, `lookup` is
left unset — an honest gap (the same "unreadable: no lookup key
recorded" status `ubx status --drift` already reports for any
lookup-less resource), never a guessed key.

#### Fleet/why/status/scan: apply records as a second discovery source

`core.Ledger.Fleet`, `ProposalsForAddress`, `LastObservedHash`,
`LastObservationTime`, and `FoldState` are all extended, identically: for
a `KindChange` proposal's own `Delta.Creates` entries (recognized by their
`config` key, `Delta.Creates`' change-proposal shape — never confused with
adoption's `state`-keyed one), fold that proposal's own apply records
(`Ledger.ApplyAttempts`) for the resource's **own** most recent transition.
Discoverable if and only if that resource's own last transition is
`applied` — **independent of whether the enclosing multi-resource
`ApplyRecord` itself has been sealed**. A resource's own completion and its
attempt's overall summary are different things: `core/executor`'s own
`foldResourceHistory` already established (UBI-27) that a resource can be
genuinely, durably `applied` — real `provider_result`, real identity —
while sibling resources in the very same unsealed attempt are still
pending, in-flight, or failed after a `kill -9`. Gating on "attempt
sealed" would incorrectly hide a resource that is, in the real world,
already fully created; gating on "this resource's own last transition is
`applied`" is the correct and sufficient check, and is exactly what
prevents a half-created resource (docs/resolver-adversarial.md-style row:
`kill -9` mid-create) from being surfaced as watchable before it's
actually done — it simply never reaches `applied` in the first place, no
special-casing needed.

An apply record predating this amendment (no `lookup` recorded on its
`ResourceApply` entries — every real apply record shipped before this
session) is handled gracefully, not as an error: the same fold falls back
to deriving `{"id": ...}` from `provider_result` directly, exactly the
logic `shipCreate` itself now runs at ship time. This is not a
contradiction of "never depend on derivation at need-time" above — that
principle governs how *new* ship runs record data going forward; tolerating
an old, already-sealed record via a best-effort read-time derivation is an
ordinary migration/compatibility concern, the same posture `ResourceApply`
itself already takes toward any struct field a truly ancient record
predates (absent, zero-valued, never a hard error). If `provider_result`
itself has no derivable `id` either (a genuinely old or malformed record),
the resource is honestly reported as lookup-less, same as above.

#### No `schema_version` bump

`ResourceApply.lookup` is a purely additive, `omitempty` field — the same
posture every prior additive amendment to this document has taken
(`ResolutionInput.lookup`/`provider_checksum`/`pinned_head`/`ledger_dir`,
`Modification.depends_on`). Nothing about `ApplyRecord`'s own hash-relevant
shape, domain prefix, or canonicalization changes; `canonicalApplyRecordBytes`
already canonicalizes generically (no fixed field enumeration to update).

### Amendment: destroys (2026-07-17, UBI-30)

Phase 2 continues: destroys, the executor's last verb. Design only, this
session — docs/resolver.md's own "Amendment (2026-07-17, UBI-30)" pins the
intent-file/orphan-protection half; this amendment pins the ratification
half (the wire shape, its validation, and the tombstone posture); docs/
executor.md's own amendment pins execution; docs/destroys-adversarial.md
pins the required-outcome program. No code lands with this session; every
`core.Validate`/struct change named below is session 2+ implementation
work of the same ticket.

#### `Delta.Destroys`' element shape, re-pinned — a `schema_version` bump

The original "Delta element shapes — PINNED (2026-07-10)" section fixed
`delta.destroys`' element as a bare `Address` (`{stack, type, name}`). That
shape is superseded — docs/resolver.md's own amendment explains why a
destroy needs to carry more than an address (a human signing away a
resource needs to see what's being lost, inline, not a hash to separately
chase down) and why ordering needs to be explicit, the same way
`Modification.DependsOn` already is:

```json
{
  "address": { "stack": "payments", "type": "aws_db_instance", "name": "old-payments-db" },
  "state": { "...": "the resource's full FoldState-folded config at resolve time -- what will be lost" },
  "depends_on": ["payments.aws_db_instance.replica-of-old"]
}
```

- **`address`** — unchanged in shape (`Address`), now nested rather than
  being the whole element.
- **`state`** — the resource's complete folded state, the same shape
  `adoption`/`drift_adopt`'s own `Delta.Creates[].state` already carries
  (record-only, concrete, no `$computed`/`$ref` markers — a destroy target
  is by definition an already-ledgered, already-concrete resource,
  `FoldState`'s own postcondition). Deliberately the *whole* state, not a
  changed-attributes-only diff the way `Modification.Before`/`.After` are —
  see docs/resolver.md's own reasoning for why that economy doesn't apply
  here.
- **`depends_on`** — reuses `Modification.DependsOn`'s exact field name and
  meaning ("this element's own operation must not execute before every
  named address's own operation, in this same proposal, has completed") —
  populated by docs/resolver.md's own orphan-protection walk with the
  **reverse**-edge set (which surviving resources currently depend on this
  destroy target), not the forward set a create/modify's own `depends_on`
  would carry. The meaning of the field never changes across creates,
  modifies, and destroys — only which edge set populates it does, and that
  difference is exactly what makes "destroys execute in reversed order"
  fall out of one topological walk instead of needing a second mechanism.
  See docs/executor.md's own amendment for the walk itself.

**This is a hashed-content shape change** (`Delta.Destroys`' own element
type), and per this document's own hashing ratification below ("Any
further change to hashed-content shape... requires a `schema_version` bump
and an explicit migration path"), `core.SchemaVersion` bumps from `1` to
`2` the moment destroy support ships (session 2+ code, not this session).
**The migration cost is genuinely unusual — close to zero, checked, not
assumed:** `core.Validate` has forbidden a non-empty `delta.destroys` for
*every* proposal kind this codebase has ever produced, unconditionally,
since before this pinned shape even existed (`validateKind`,
`core/validate.go`) — there is no real ledger entry anywhere, in this
project's own history or in any deployment it's ever had, with a populated
`delta.destroys` under the old bare-`Address` shape to migrate. The
version bump exists on principle (a hashed array-element shape changed,
full stop — the rule doesn't carve out an exception for "but nothing used
it yet") and to protect any external tooling that might have been built
against the original 2026-07-10 draft shape, not because a real migration
script is needed for this codebase's own ledgers.

#### New `resolution.inputs[]` kinds: `destroy_target`, `cross_stack_orphan_check`

Mirroring `delta.modifies`' own existing rule (every modify needs a
corresponding `resolution.inputs` entry proving its "before" was actually
observed): every `delta.destroys` entry requires a matching
`resolution.inputs` entry, a new `kind`:

```json
{
  "kind": "destroy_target",
  "resource": "payments.aws_db_instance.old-payments-db",
  "observed_hash": "sha256:<hex, of the same full state recorded in delta.destroys[].state>",
  "lookup": { "id": "old-payments-db" }
}
```

`observed_hash` gives the same "provable against what was actually
observed, not merely asserted" guarantee `delta.modifies` already has;
`lookup` is required (not merely `omitempty` the way `ResolutionInput.Lookup`
is for `live_state` entries) because the executor's freshness recheck and
reconcile-by-query — both mandatory for every destroy attempt, docs/
executor.md's own amendment — have no other way to find the resource
again. **Enforced as propose-time validation**, extending
`validateModifiesHaveResolutionInputs` (renamed in code to reflect it now
covers both arrays, or given a sibling function — an implementation
decision for session 2+, not fixed here) the same way: a `delta.destroys`
entry with no matching `destroy_target` resolution input, or one with an
empty `observed_hash` or `lookup`, is rejected before it can be hashed.

The second new kind, `cross_stack_orphan_check`, is **not** required —
it's evidence, not a claim of correctness the resolver can't actually back:

```json
{
  "kind": "cross_stack_orphan_check",
  "resource": "payments.aws_db_instance.old-payments-db",
  "status": "not_performed | checked_clear",
  "checked_ledger_dirs": ["../networking/ledger"]
}
```

`status: "checked_clear"` means every `ledger_dir` in docs/resolver.md's
own `known_dependents` input was walked and none pinned this address;
`"not_performed"` means `known_dependents` was empty or omitted — the
resolve still succeeds (cross-stack orphan protection is best-effort by
design, docs/resolver.md's own reasoning), but the gap is recorded as
honest evidence a human reviewing the proposal can see, never silently
absent. If a named neighbor's ledger *does* pin this address, resolve
fails hard instead (docs/resolver.md's "Orphan protection" section) — no
`cross_stack_orphan_check` entry is ever recorded for a destroy that didn't
happen; there is nothing to record evidence about.

#### Validation: destroys are now legal for `change` proposals

Supersedes the struck-through section above. A `KindChange` proposal MAY
now carry `delta.destroys`; `blast_radius.destroys` MUST equal
`len(delta.destroys)` exactly — the same "a change proposal's blast radius
is real" posture already governing `.creates`/`.modifies`. Every
`delta.destroys` entry's `address` MUST be present in `FoldState` at
resolve time (docs/resolver.md's own resolve-time validation) — this is a
resolver-side check, not a `core.Validate` one (`core.Validate` has no
`StateReader`/ledger access; it validates proposal-internal consistency
only, the same boundary every other kind-specific rule in this file
already respects).

#### Accept-time friction: `--confirm-destroys`

The first real invariant with teeth this codebase gives an operator,
distinct from every other check in this document (which are all either
structural validation or freshness/staleness detection — things the
system itself verifies): **`ubx accept` refuses to accept any proposal
whose `blast_radius.destroys > 0` unless invoked with an explicit
`--confirm-destroys` flag** — hardcoded in v1, not policy-engine-driven
(docs/resolver.md's own policy-stub hook stays an empty slice for
everything named in this amendment too; a real policy engine, component
map #9, generalizes this into a configurable rule later, but v1 ships the
narrow, hardcoded version rather than waiting on a policy engine that
doesn't exist yet). This is deliberately **friction**, not validation —
the proposal is already fully valid, resolved, orphan-checked, and
fresh; `--confirm-destroys`'s only job is making sure a human actually
meant to accept something with teeth, the same spirit as a cloud
console's own "type the resource name to confirm deletion" pattern,
adapted to a CLI's own idiom (a flag, not an interactive prompt — `ubx`'s
other commands are already scriptable/non-interactive by convention, and
this shouldn't be the one exception). Missing the flag is a hard refusal,
exit code in the same "actionable finding" tier `resolver.ErrCrossStackPinStale`
already occupies — never a warning that lets acceptance proceed anyway.

#### Reconciliation, reused: the pre-attempt freshness check disambiguates a post-timeout read

No new field on `ResourceApply`/`ApplyRecord` is needed for this — a
genuine reuse, not an amendment to either struct's shape. `ResourceApply.Reconciliation`
(docs/schema.md's own "Amendment: apply records," UBI-26) has, until now,
only ever been populated *after* an `unknown_post_timeout`. For a
`delta.destroys` resource specifically, docs/executor.md's own amendment
extends its use one step earlier: the mandatory pre-attempt freshness
recheck (every resource gets one before `in_flight`, docs/executor.md's
existing "Freshness" section, unchanged) is itself recorded as a
`ReconciliationAttempt` for a destroy target, with `Outcome` one of three
new legal values — `present_matches` (proceed to `in_flight`),
`present_drifted` (refuse, terminal for this attempt — docs/executor.md's
amendment), or `absent` (short-circuit straight to a terminal
`already_absent` outcome, described below, never reaching `in_flight` at
all, since there's nothing to call `ApplyResourceChange` against). This
pre-check entry is what later disambiguates an otherwise-ambiguous
not-found read from post-timeout reconciliation: a `destroyed` outcome is
only ever recorded when the *immediately preceding* reconciliation entry
for the same resource was `present_matches` — proving the resource
existed, matching its signed state, right before this attempt, so its
absence now is attributable to this attempt and not to some earlier,
unrelated disappearance. `Outcome`'s existing legal values (`applied` |
`failed` | `inconclusive`) gain `destroyed` | `already_absent` alongside
them — purely additive to a free-form string field, no Go struct change,
same posture as every other additive amendment in this document.

#### Tombstone posture: permanent history, terminal record, nothing erased

A destroyed address's own proposal chain — genesis (adoption or a
change-proposal create) through every drift/modify in between, ending at
the accepted destroy proposal — is never rewritten, never removed, and
never collapsed. The destroy proposal is that chain's **terminal record**:
`ubx why <address>` continues to render the complete biography, oldest to
newest, exactly as it does today for any other address (docs/schema.md's
own "`Proposal.status` is never rewritten" section already established
this posture for a proposal's own *status*; this extends the same
"immutable history, current truth computed by folding over it" discipline
to a resource's entire existence, not just one proposal's derived state).
Nothing in `core.Ledger.Append`'s own append-only contract changes to make
this true — a destroy proposal, and the apply record that ships it, are
ledger entries exactly like any other, subject to the exact same
immutability `ErrDuplicateProposal` already enforces.

**`FoldState` folds a fully-destroyed address back to absent** — this is
the one real behavioral extension this posture requires, named explicitly
rather than left ambiguous: once a `delta.destroys` entry's own apply
record reaches a sealed, terminal `destroyed` or `already_absent` outcome
(and that is the address's own *most recent* transition — the same
"per-resource, not per-attempt" gating principle docs/schema.md's UBI-29
amendment already established for shipped creates, reused unchanged, not
reinvented for a third time), `FoldState(addr)` reports the resource does
not exist. This is what a "tombstone" means here, the same sense a
tombstone carries in any append-only/log-structured store (Cassandra's
own usage is the direct analogy): a permanent marker recording that a
deletion happened, retained forever in history, while the *live* view
built by folding over that history correctly reports "gone." This is also
what makes docs/resolver.md's own `op: "create"` validation
(`FoldState`-absent required) behave correctly for a re-created resource
under the same `(stack, type, name)` address after a genuine destroy —
without this fold, a destroyed address would incorrectly refuse every
future create against it forever, which is not what "destroyed" should
mean. Whether `ubx why <address>` renders a second genesis-through-tombstone
lifecycle, if one is later created under the same address, as one
continuous chain or as two visually distinct lifecycles sharing one
address is a real, open presentation question — named here rather than
silently assumed either way, left for the session that actually implements
`ubx why`'s rendering of this case (not blocking `FoldState`'s own
correctness, which doesn't depend on the answer).

#### `schema_version` bump: exactly what changes

`Delta.Destroys`' element shape (`Address` → `{address, state, depends_on}`)
is the one change requiring `core.SchemaVersion`'s bump to `2`. Everything
else in this amendment is additive by the same reasoning every prior
amendment in this document has used: two new `resolution.inputs[].kind`
values (`destroy_target`, `cross_stack_orphan_check`), two new
`ReconciliationAttempt.Outcome` string values, and a CLI-layer acceptance
gate (`--confirm-destroys`) that touches no hashed-content shape at all.
None of the canonical hashing rules themselves change (domain prefix,
exclusion list, number encoding) — only the ratified array-sort rule's own
target shape does, which is exactly the class of change the ratification
below already anticipates needing a version bump for.

### Amendment: the `provider` field returns — no longer redundant (2026-07-18, UBI-43)

The founding IR-node draft (top of this document) sketched a `provider:
{source, version}` field on every resource node. `Delta.Creates`' own
pinning (UBI-27, above) dropped it as "redundant with information the
outer `Proposal` already carries" — true at the time, since every verb
launched exactly one provider per invocation, so which binary executed a
given node was never in question; it was whichever one the CLI was told
to use. **Multi-provider stacks (docs/architecture.md §Multi-provider
stacks) break that invariant**: a single `change` proposal can now name
resources whose types are owned by different provider binaries entirely
(`aws_db_instance` + `helm_release` in one proposal, one signed unit).
Which binary executes which node is real, resolver-decided information
again — the same category of fact `depends_on` already is: computed, not
authored, and worth a human reviewing and signing off on, because a wrong
inference (the resolver picking the wrong provider for an ambiguous type)
is exactly the kind of mistake this project's whole thesis exists to catch
before it reaches `ubx ship`.

Reinstated, unchanged from the founding draft's own shape, on all three
delta element kinds — `Delta.Creates`, `Modification` (`delta.modifies`),
and `Delta.Destroys`' element (symmetry: a destroy needs to know which
provider to call exactly as much as a create does):

```json
{
  "stack": "payments",
  "type": "aws_db_instance",
  "name": "payments-db-replica",
  "provider": {"source": "registry.terraform.io/hashicorp/aws", "version": "6.60.0"},
  "config": { "...": "unchanged" },
  "depends_on": ["payments.aws_db_instance.payments-db"]
}
```

**Resolver-populated, never hand-authored** — the intent file's own
`resources[]`/`destroys[]` entries never carry a `provider` field
themselves, the same "computed, not authored" posture `depends_on` already
has. The one narrow exception: an intent file MAY set a same-shaped
`"provider": {"source": "..."}` hint directly on an individual `resources[]`
entry, consulted *only* to break a genuine type-ownership ambiguity
between two or more declared providers that both claim the same type —
ordinarily absent, and refused at resolve time if the named source isn't
one of the stack's own declared `providers` (docs/architecture.md's own
config-map). See docs/resolver.md's own amendment for the full
type→provider inference algorithm this field is the recorded output of.

**No `schema_version` bump.** Purely additive, the same reasoning as every
prior additive amendment in this document (`lookup`, `provider_checksum`,
`Modification.depends_on`): every proposal recorded before this amendment
was resolved against exactly one provider per the single-provider
invariant that held at the time, so an absent `provider` field on an old
proposal is never ambiguous — it unambiguously means "the one provider
that CLI invocation was given," recoverable from context even though it
was never written down. A reader doesn't need to migrate anything to keep
interpreting old proposals correctly; the field is simply absent where it
was always implied. Hashing: `provider` participates in the canonical hash
like any other content field — no exclusion, no special treatment — which
means altering a signed proposal's own provider assignment after the fact
(without a fresh resolve/accept) is caught exactly like altering any other
field would be.

### Amendment: intent-provider drafts — ambiguity content + two new `intent.sources[].kind` values (2026-07-27, UBI-41)

Design only, session 1 of UBI-41 — no code lands with this amendment (see
docs/intent-provider.md, docs/plan.md's own wedge subsection for the
session breakdown). Pins the wire-format half of docs/intent-provider.md's
own "ambiguity as visible content" design center — the intent provider's
LLM-authored draft is exactly `ubx:intent/v1` (docs/schema.md's UBI-27
amendment, above) plus the additive fields below, riding the identical
`Proposal.Intent` struct a hand-written intent file's own `intent` object
already occupies, so this content survives unchanged through
`resolve`/`accept` into the final hashed, signed proposal — never a
separate, unsigned side-channel.

#### `Proposal.Intent` gains three new, optional, array fields

```json
{
  "intent": {
    "summary": "...",
    "sources": [ "...": "unchanged shape, plus two new kind values below" ],
    "assumptions": [
      { "text": "...", "affects": ["<address>.<path>", "..."] }
    ],
    "defaults": [
      { "text": "...", "affects": ["<address>.<path>", "..."] }
    ],
    "questions": [
      { "text": "...", "affects": ["<address>.<path>", "..."], "blocking": true }
    ]
  }
}
```

- **`assumptions[]`** — an explicit interpretive choice the intent
  provider made where the source document was genuinely ambiguous.
- **`defaults[]`** — a gap the source document left unaddressed
  entirely, filled from context rather than invented from nothing. A
  distinct field from `assumptions[]`, not a synonym — docs/intent-provider.md's
  own "The mechanism" section states the review-posture difference this
  split exists to preserve.
- **`questions[]`** — an unresolved tension (a genuine contradiction, or
  an ambiguity too open-ended to responsibly guess at) the draft still
  had to produce *some* concrete resolution for. `blocking` is a
  **review-affordance field only** — it carries zero resolver-side
  enforcement; `core.Validate`/`core/resolver` never inspect it, never
  refuse a proposal because of it. See docs/intent-provider.md's own
  "Component 3" section for why an auto-refusing design was considered
  and rejected.
- **`affects`** on all three is a list of canonical address+path strings
  (`<stack>.<type>.<name>.<path>` — the identical dot-path convention
  `Modification.Before`/`.After` and `$ref`'s own `to` field already use),
  never a free-text pointer — so a review surface can highlight the
  exact config value an assumption/default/question is about.
- **Origin-agnostic by construction.** Nothing about this shape is
  specific to an LLM — a hand-written intent file MAY populate these
  fields too (an author noting their own assumption explicitly), and a
  resolved proposal produced by any resolver session before this
  amendment simply has all three fields empty/absent, exactly the same
  "absent means it was never applicable, not that something is missing"
  posture the `provider` field amendment (UBI-43, above) already
  established for the identical reason.

#### Two new `intent.sources[].kind` values

Alongside the existing `dialogue`/`manual_edit`/`issue`/`cloudtrail`/
`gcp_audit` kinds (docs/schema.md's own founding draft and UBI-10/UBI-21
amendments):

- **`document`** — `{ "kind": "document", "ref": "<repo-relative path>",
  "content_hash": "sha256:<hex>" }`. The authoring markdown file itself —
  docs/architecture.md's own "Authoring mediums... always live in git as
  repo assets... proposals pin them by content_hash" rule, made concrete
  for md for the first time. `content_hash` is computed over the **raw,
  unredacted** file exactly as committed — never the redacted copy
  actually transmitted to the intent provider (docs/intent-provider.md's
  own "Secret material in a doc" section states why these are two
  deliberately distinct byte sequences, never conflated).
- **`intent_provider`** — `{ "kind": "intent_provider", "ref":
  "<adapter>:<model>", "content_hash": "sha256:<hex>" }`. Records which
  adapter and model produced the draft, and pins the adapter's own raw
  structured-output response as tamper-evident audit content — evidence
  of provenance, exactly like `dialogue`/`manual_edit` already are, never
  an enforced binding (a human editing the draft file before `ubx
  resolve` is legitimate and expected; this source's own `content_hash`
  simply continues to name what the AI actually said, unchanged by a
  later human edit, the same way a `manual_edit` source's `content_hash`
  doesn't move if the underlying PR is later amended).

No `schema_version` bump — purely additive, the same reasoning as every
prior additive amendment in this document (`lookup`, `provider_checksum`,
`Modification.depends_on`, the `provider` field's own return above): a
proposal recorded before this amendment simply has none of these fields,
never ambiguously.

### Amendment: the chat medium — dialogue capture (2026-07-28, UBI-46)

Real code, live-verified the same session (see docs/intent-provider.md's
own "Amendment: the chat medium" for the full design account and
docs/plan.md for the session narrative). Pins the wire format this
document's own founding session (2026-07-10) left as an open question
("Dialogue format & privacy tiering") and gives `IntentSource.Kind ==
"dialogue"` — a legal enum value in this schema since its very first
draft, never actually produced by anything until now — its first real
producer.

#### `dialogues/<hash>.dlg.json`

Content-addressed, matching this schema's own proposal-ID convention:
the filename is the hex digest of the file's own full JSON content
(hashed once, at the moment of writing — a dialogue is captured entirely
in memory during an `ubx chat` session and written to disk exactly once,
atomically, only at explicit finalization; see docs/intent-provider.md
for why this is what makes "no orphan file from an abandoned session"
true by construction).

```json
{
  "schema_version": 1,
  "stack": "payments",
  "adapter": "claude",
  "model": "claude-opus-4-8",
  "started_at": "2026-07-28T00:00:00Z",
  "turns": [
    { "text": "We need a Postgres database for payments, like staging but smaller.", "at": "2026-07-28T00:00:01Z" },
    { "text": "Make it multi-az.", "at": "2026-07-28T00:01:00Z" }
  ],
  "draft": { "...": "the final ubx:intent/v1 draft this dialogue produced -- see below" }
}
```

- **`turns[].text` is always the REDACTED text**, per turn, at the moment
  of capture — never post-hoc, and there is no separate "raw" copy kept
  anywhere on disk for a chat turn to have its own tamper-evidence hash
  against (a real, deliberate divergence from `ubx propose --from-doc`'s
  own document/redacted-copy split, where the raw file already exists on
  disk independently before `ubx` ever touches it — a typed turn has no
  such independent original). The redacted text IS the authoritative
  captured record.
- **`draft`** embeds the final `ubx:intent/v1` draft this dialogue
  produced — but deliberately the PRE-provenance version, with an empty
  `intent.sources`. This is not an oversight: the draft `ubx chat` hands
  back to the caller (stdout or `--out`) is a *separate* copy whose own
  `intent.sources` names THIS file's own content hash — embedding that
  same provenance-bearing copy inside the file being hashed would be
  circular (the file's hash would depend on its own hash). Two distinct
  objects for two distinct purposes, never conflated.

#### `dialogues/` lives at the top level, a sibling of `ledger/` — never nested inside it

See this document's own "Ledger layout" section, above, for the full
correction: the founding draft nested dialogues under `ledger/`; the
real, later-ratified "authoring mediums always live in git as repo
assets, only the ledger's own JSON gets a configurable store" split
(docs/architecture.md, 2026-07-17) means a dialogue can never sit inside
the one directory that gets swapped to a remote `LedgerStore`. Built
exactly this way: `ubx chat --ledger-dir <dir>` writes to
`<dir>/dialogues/`, a plain sibling of `<dir>/ledger/`, regardless of
whether `.ubx/config`'s own `[ledger].store` points at git or a remote
store for that same stack.

#### The `intent.sources` entry — the existing `dialogue` kind, no schema change needed

```json
{ "kind": "dialogue", "ref": "dialogues/bcb5b373....dlg.json", "content_hash": "sha256:bcb5b373..." }
```

Exactly the shape this document's own founding draft already sketched
for a `dialogue` source (`{"kind": "dialogue", "ref": "d-99f2",
"content_hash": "sha256:9f2a..."}`) — `ref` is now a real relative path
instead of a placeholder short ID, `content_hash` is now a real hash
instead of a placeholder value, and this is the first proposal kind that
ever actually populates it. No new `IntentSource` field, no
`schema_version` bump. A `change` proposal drafted via `ubx chat` also
gets an `intent_provider` source alongside it, identical in shape to
`ubx propose --from-doc`'s own (UBI-41) — the same two-source pattern,
generalized over which authoring-medium kind (`document` or `dialogue`)
names the first entry.

#### Contradictory turns: later wins, recorded as an assumption, not silently resolved

Not a new schema construct — the existing `intent.assumptions[]` (UBI-41)
is exactly where this lives. When a later turn in a captured dialogue
changes or contradicts an earlier one, the resolved draft's own
`instance_class`/whichever attribute reflects the LATER turn's value,
and an `assumptions[]` entry names both turns and states which one won.
Live-verified, not assumed: a real two-turn conversation ("instance
class db.t3.large", then "actually, use db.t3.micro instead, not large")
produced a resolved `config.instance_class` of `db.t3.micro` and an
assumption reading "Turn 1 requested instance class db.t3.large; Turn 2
overrode it with db.t3.micro... Following the later turn." See
docs/intent-provider-adversarial.md's own "contradictory turns" row for
the required-outcome program this is checked against.

## Canonical hashing — RATIFIED v1

> See "Ratification — Hashing (2026-07-10)" below. This section is no longer
> draft; changes require a `schema_version` bump and a migration, not an edit
> in place.

- Hash function: SHA-256.
- Domain separation: the literal prefix bytes `ubx:proposal:v1\n` (UTF-8) are
  prepended to the canonical serialization before hashing. This scopes the hash
  to "ubx proposal, schema v1" so it can never collide with a hash of the same
  bytes computed for a different purpose (a different object kind, a future
  incompatible v2 encoding, another tool's use of the same canonical-JSON bytes,
  etc.) — the classic cross-protocol hash confusion this guards against.
- Hashed content: the proposal object EXCLUDING exactly these fields:
  `id`, `acceptance`, `status`. Nothing else is excluded — in particular
  `resolved_at` and every other `resolution.*` field ARE included, since they
  are part of what was reviewed at acceptance time. (`id` is excluded because
  it IS the hash — a self-reference would be circular. `acceptance` and
  `status` are excluded because they are recorded after the hash exists, and
  must not perturb it.)
- Serialization: canonical JSON — RFC 8785 (JCS) style: UTF-8, sorted object
  keys, no insignificant whitespace.
- Number encoding (resolves the prior open question — no generic float
  canonicalization is needed): every number in hashed content MUST be either
  a JSON integer representable exactly as a signed 64-bit integer (int64), or
  a decimal value encoded as a JSON **string** (e.g. `cost_delta.monthly_usd`
  becomes `"59.00"`, not `59.00`), never a JSON float literal. A resolver that
  produces a float for a hashed field is a hard failure at propose time
  (reject before hashing, not a silent coercion) — this sidesteps float
  serialization ambiguity (trailing zeros, exponent form, -0, NaN/Inf)
  entirely rather than trying to canonicalize it away.
- Array ordering: `delta.creates`, `delta.modifies`, `delta.destroys` are
  sorted **lexicographically by `(stack, type, name)`** of each element, not by
  dependency/topological order. (Supersedes the draft's "dependency order for
  delta lists" — dependency order is a resolver/executor concern for planning
  apply sequence, not a hashing concern; deriving it from a topo sort would
  make the hash sensitive to resolver internals and graph algorithm choice,
  which is exactly the nondeterminism this section exists to rule out.) See
  §Proposal's "Delta element shapes — PINNED" for exactly where `stack`/
  `type`/`name` come from on each array's element shape — for
  `delta.destroys` specifically, from its own nested `address` field since
  the "Amendment: destroys" (UBI-30) re-pin, not from the element's
  top level (`schema_version` 2 only; `deltaSortKey`'s own destroy-element
  branch reads `element.address.{stack,type,name}`, not
  `element.{stack,type,name}`). Any other array with
  hash-significant order must have its own explicit, documented sort key —
  no array may rely on insertion/map-iteration order.
- Determinism rules feeding the hash: no map-iteration ordering anywhere
  upstream; no environment or clock leakage except explicit recorded fields.
- The double-run rule: any evaluator producing hashed content runs twice; byte
  mismatch = hard failure at propose time.

### Ratification — Hashing (2026-07-10)

`§Canonical hashing` above is **RATIFIED** as of 2026-07-10, incorporating the
amendments agreed in that day's design session:

1. Numbers restricted to int64 or decimal strings; floats rejected at propose
   time (no float canonicalization).
2. Hash-excluded fields are exactly `id`, `acceptance`, `status` — no broader
   "anything that changes after refinement" carve-out.
3. Domain-separation prefix `ubx:proposal:v1\n` prepended to canonical bytes
   before hashing.
4. `intent.sources[].content_hash` added for dialogue/PR/issue tamper-evidence.
5. `delta` arrays sorted lexicographically by `(stack, type, name)`, not
   dependency order.

Any further change to hashed-content shape, exclusion list, domain prefix,
number encoding, or array ordering after this point requires a
`schema_version` bump and an explicit migration path for existing ledger
entries — not an in-place edit of this section.

## Ledger layout (draft)

```
<ledger-dir>/
  ledger/
    proposals/<id>.prop.json
    applies/<id>.attempt-<n>.apply.json
  dialogues/<hash>.dlg.json        (intent evidence -- UBI-46, see the amendment below
                                     for why this moved out from under ledger/)
  rendered/                        (projections; regenerated, byte-checked in CI)
  .ubx/ledger.lock                 (current head hash)
```

- `rendered/` is never read by the executor. Humans and diffs only.
- `render --check`: ledger → render → byte-compare; CI-blocking and pre-commit.
- **A real correction, caught while implementing UBI-46, not left as a stale
  sketch**: this section's own original draft (2026-07-10, this project's
  founding session) nested `dialogues/<id>.dlg.json` *inside* `ledger/`,
  alongside `proposals/`. That was never carried into the real
  implementation (`core/gitledgerstore.go`'s own doc comment: `ledger/`
  holds exactly `proposals/` and `applies/`, nothing else) — and, more
  importantly, it contradicts a later, more authoritative decision this
  document's own founding sketch simply predates: docs/architecture.md's
  "Ledger stores" section (2026-07-17, UBI-32 Arc B) ratifies "Authoring
  mediums (md intents, diagrams, SDK code, dialogues) always live in git
  as repo assets... proposals pin them by content_hash... The ledger's
  own JSON (proposals/, applies/) gets a configurable store." A dialogue
  is explicitly named as an authoring medium in that sentence — meaning
  it must never sit *inside* `ledger/`, the one directory that gets
  swapped to a remote `LedgerStore` (S3/GCS/Azure) per stack. `dialogues/`
  is promoted to a sibling of `ledger/` instead (UBI-46, "Amendment: the
  chat medium," below) — not a new decision, the correct application of
  one already made.

## Open questions (tracked, not blocking Slice 1)

- Dialogue format: **resolved, UBI-46** (see "Amendment: the chat
  medium," below). Privacy tiering (moving dialogue content to a
  different retention/access tier, the founding sketch's own "tierable
  to object store later" note) stays open — `dialogues/` is git-local
  only in UBI-46's own build, unlike `ledger/`'s own real `LedgerStore`
  abstraction; a remote-tierable dialogues store is a real, named,
  unbuilt follow-up, not assumed covered by this resolution.
- Cross-stack workspace index format.
- Environment/promotion model (same proposal re-resolved per env) — design before
  the wedge grows environments.
