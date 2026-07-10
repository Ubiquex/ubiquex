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
