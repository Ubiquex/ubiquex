# CLAUDE.md — ubiquex-cli

## What this is

`ubiquex-cli` (binary: `ubx`) is a ground-up v2 of the Ubiquex infrastructure engine.
Core thesis: **infrastructure change management via a proposal ledger** — every infra
change is a typed, hashed, signed proposal recorded in an append-only per-stack ledger.
Files, diagrams, chat, and docs are projections/frontends; the ledger is authoritative.

This is NOT a continuation of ubx v1 (the XCL language project). v1 is legacy;
its type system and graph algorithms inform v2, its syntax and CLI do not.

## Session protocol

1. Read `STATE.md` first — it holds current slice, open decisions, and next steps.
2. Design decisions live in `docs/`. Never contradict them silently; if implementation
   reveals a doc is wrong, stop, record the finding in `STATE.md`, and flag it.
3. Update `STATE.md` as the LAST act of every session (what was done, what's next,
   any surprises).
4. A plan change is not real until it lands in `docs/plan.md` (with changelog entry).
5. User-visible changes (new commands, flags, behaviors) create a docs
   obligation in the ubiquex-docs repo (Mintlify, user-facing). During
   foundational slices: do NOT write user docs inline — record a "docs debt"
   entry in STATE.md instead, batched and cleared per slice. Debt must not
   cross a slice boundary. Internal docs (docs/ in this repo) are updated
   immediately as before.

## Git rules (strict)

- Commit and push directly is ALLOWED — always under Roozbeh's own git identity
  and signing key. Never alter `user.name` / `user.email` / signing config.
- NO AI attribution anywhere: no Co-Authored-By trailers, no "Generated with"
  lines, not in commit messages, not in PR bodies. (`includeCoAuthoredBy` is
  disabled in settings; do not re-add manually.)
- Conventional, terse commit messages: `component: what changed` (e.g.
  `provider: tfplugin v6 handshake`).

## Code conventions

- Language: Go (module: `github.com/ubiquex/ubiquex-cli`).
- Layout: `core/` (IR, ledger, hashing), `provider/` (tfplugin client),
  `cli/` (cobra commands), `docs/`.
- Determinism is a feature: anything feeding a hash must have canonical,
  reproducible serialization. No map-iteration ordering, no timestamps in
  hashed content, no environment leakage.
- Tests accompany every slice; adversarial/failure-path tests are first-class
  (provider timeout, partial state, interrupted operations).

## Key docs

- `docs/architecture.md` — the system model (IR, ledger, proposals, components)
- `docs/schema.md` — proposal + IR schema constitution (hashing rules live here)
- `docs/plan.md` — wedge plan, slices, milestones, changelog
- `docs/prompts.md` — handoff prompt conventions for sessions
