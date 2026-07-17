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
  `type`/`name` come from on each array's element shape. Any other array with
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
<stack>/
  ledger/
    proposals/<id>.prop.json
    dialogues/<id>.dlg.json        (intent evidence; tierable to object store later)
  rendered/                        (projections; regenerated, byte-checked in CI)
  .ubx/ledger.lock                 (current head hash)
```

- `rendered/` is never read by the executor. Humans and diffs only.
- `render --check`: ledger → render → byte-compare; CI-blocking and pre-commit.

## Open questions (tracked, not blocking Slice 1)

- Dialogue format & privacy tiering.
- Cross-stack workspace index format.
- Environment/promotion model (same proposal re-resolved per env) — design before
  the wedge grows environments.
