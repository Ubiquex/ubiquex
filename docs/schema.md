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
- `acceptance` binds a signature to the exact hash. Timestamps and acceptance data
  live OUTSIDE the hashed content (see below).
- `intent.sources[].content_hash` is a SHA-256 content hash (`sha256:<hex>`) of the
  referenced dialogue/PR/issue content at resolution time — tamper-evidence for
  intent evidence, which otherwise lives outside the proposal's own hash chain
  (dialogues can be edited or deleted upstream; the content_hash catches that).

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
