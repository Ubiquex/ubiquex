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
      { "kind": "dialogue", "ref": "d-99f2" },
      { "kind": "manual_edit", "ref": "PR #315" },
      { "kind": "issue", "ref": "UBX-241" }
    ]
  },
  "delta": {
    "creates": [ "<IR nodes>" ],
    "modifies": [ { "target": "...", "before": {}, "after": {} } ],
    "destroys": [ "..." ]
  },
  "resolution": {
    "resolved_at": "2026-07-10T...Z",
    "inputs": [ { "kind": "live_state", "resource": "...", "observed_hash": "..." } ]
  },
  "cost_delta": { "monthly_usd": 59 },
  "blast_radius": { "creates": 0, "modifies": 1, "destroys": 0 },
  "invariants_checked": [ { "policy": "no_public_db", "verdict": "pass" } ],
  "acceptance": {
    "method": "pr_merge | local | crypto",
    "merge_sha": "8c1d2e...",
    "approvers": [ "roozbeh" ],
    "accepted_at": "..."
  },
  "status": "draft | refined | accepted | applied | stale | rejected"
}
```

Notes:
- `id` is a content hash (git's lesson) — no sequential numbering; human-friendly
  aliases allowed as labels.
- `parent` forms the per-stack hash chain. `ledger.lock` records the current head.
- Staleness: any `resolution.inputs` observed_hash mismatch, parent advancement
  conflict, or pinned cross-stack head advancement ⇒ status becomes `stale`;
  re-resolution required before acceptance/ship.
- Adoption proposals MUST have blast_radius all-zero and `delta.creates/destroys`
  empty against cloud (record-only).
- `acceptance` binds a signature to the exact hash. Timestamps and acceptance data
  live OUTSIDE the hashed content (see below).

## Canonical hashing (draft — ratify before Slice 2)

- Hash function: SHA-256.
- Hashed content: the proposal object EXCLUDING `acceptance`, `status`, and any
  field that changes after refinement freezes (`resolved_at` IS included — it is
  part of what was reviewed).
- Serialization: canonical JSON — RFC 8785 (JCS) style: UTF-8, sorted object keys,
  no insignificant whitespace, canonical number formatting.
- Determinism rules feeding the hash: no map-iteration ordering anywhere upstream;
  arrays are semantically ordered (dependency order for delta lists, defined sort
  otherwise); no environment or clock leakage except explicit recorded fields.
- The double-run rule: any evaluator producing hashed content runs twice; byte
  mismatch = hard failure at propose time.

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

- Exact JCS edge cases in Go (number canonicalization) — pick/vendor a library.
- Dialogue format & privacy tiering.
- Cross-stack workspace index format.
- Environment/promotion model (same proposal re-resolved per env) — design before
  the wedge grows environments.
