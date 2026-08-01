# CLAUDE.md — ubiquex

## What this is

`ubiquex` (binary: `ubx`; repo renamed from `ubiquex-cli` UBI-53, the
product outgrew the "-cli" suffix — it now contains the SDK monorepo,
the conformance harness, the MCP server) is a ground-up v2 of the
Ubiquex infrastructure engine.
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
5. User-visible changes (new commands, flags, behaviors) update ubiquex-docs
   in the SAME session: pages verified against the actual built binary
   (transcripts real, flags from --help), mint validate clean, committed and
   pushed. If genuinely infeasible in-session, record a docs-debt entry in
   STATE.md as the exception — never skip silently. Internal docs (docs/ in
   this repo) are updated immediately as before.
6. Only reference Linear issue IDs given in the handoff prompt; never infer one.
   When filing new issues, verify the title against the Linear board.
7. Background agents are not used in this project's sessions — work is sequential
   by design (a background docs agent wedged mid-session during the UX-fix arc;
   the fix is doing the work inline, in the foreground, every time).

## Git rules (strict)

- Commit and push directly is ALLOWED — always under Roozbeh's own git identity
  and signing key. Never alter `user.name` / `user.email` / signing config.
- NO AI attribution anywhere: no Co-Authored-By trailers, no "Generated with"
  lines, not in commit messages, not in PR bodies. (`includeCoAuthoredBy` is
  disabled in settings; do not re-add manually.)
- Conventional, terse commit messages: `component: what changed` (e.g.
  `provider: tfplugin v6 handshake`).

## Code conventions

- Language: Go (module: `github.com/ubiquex/ubiquex`).
- Layout: `core/` (IR, ledger, hashing), `provider/` (tfplugin client),
  `cli/` (cobra commands), `writeback/` (surgical .tf edits), `github/`
  (acceptance derivation), `audit/` (per-cloud drift/genesis attribution
  backends), `conformance/` (per-type registry + harness, test-only),
  `sdk/` (the multi-language SDK monorepo — TS/Go/Python runtimes,
  codegen, evaluators), `docs/`.
- **Package naming (UBI-52)**: name a package for ubx's own role, in
  ubx's own vocabulary — not for whatever external product, file
  format, or protocol the code happens to touch — unless the package's
  entire reason to exist IS being a client for that exact external
  thing (a generated protocol binding, a named cloud product's own API
  client), in which case the external name IS the correct one (e.g.
  `provider/tfplugin5` correctly names Terraform's own real wire
  protocol; `tfstate/` incorrectly named a file format instead of its
  own role, "import identity for onboarding" — renamed `stateimport/`).
  Keep names short and lowercase (`audit/`, `diagram/`, `tseval/`/
  `goeval/`/`pyeval/`). When a package implements an existing CLI verb
  one-to-one, name it after that verb (`writeback/` implements `ubx
  writeback`). Test: if you can't state the package's purpose in one
  sentence from the name alone, the name is wrong. Full audit + verdicts:
  `docs/source-tree.md`.
- Determinism is a feature: anything feeding a hash must have canonical,
  reproducible serialization. No map-iteration ordering, no timestamps in
  hashed content, no environment leakage.
- Tests accompany every slice; adversarial/failure-path tests are first-class
  (provider timeout, partial state, interrupted operations). Live tests are
  gated behind env vars; `go test ./...` stays hermetic.
- Rebuild and reinstall (`make install`, or `make build`) before re-testing
  any fix against a real stack, and confirm `ubx version`'s printed commit
  actually matches the fix's own commit before trusting the result. (UBI-63
  session 4: a founder re-test against real AWS got an identical,
  pre-fix-looking result from a stale binary — caught only afterward via a
  manual `which`/`version` check, not before. `make build`/`make install`
  now print `ubx version` immediately after rebuilding so this is one
  command, not a separately-remembered step.)
- Never run `ubx ship` (or anything else that reaches a provider's own
  `ApplyResourceChange` — a real apply) against a real cloud provider for
  live verification, demos, or doc transcripts, even one already
  credentialed on the machine. Use the hermetic `fakeprovider` binary via
  `UBX_PROVIDER_MIRROR` instead — always, no exceptions. `resolve`/
  `propose`/`sdk gen` (schema-fetch or draft-only, never applying) remain
  safe against a real provider. (UBI-47 session 4: a by-hand `ship`
  against real `hashicorp/aws` credentials created real AWS resources
  during what was meant to be routine verification.)

## Key docs

- `docs/architecture.md` — the system model (IR, ledger, proposals, components)
- `docs/schema.md` — proposal + IR schema constitution (hashing rules live here)
- `docs/plan.md` — wedge plan, slices, milestones, changelog
- `docs/prompts.md` — handoff prompt conventions for sessions
