# STATE.md — current state

> Rewritten, not appended, as the LAST act of every session. Holds only what's
> current — in flight, blocked, and what a fresh session needs before touching
> anything. History moves to `HISTORY.md` (narrative archive, consulted only when
> a session needs to know why a decision was made — not read on every open).

## In flight

Nothing currently in flight. UBI-196 and UBI-197 both closed this session —
see `HISTORY.md` for the full arc (docs corpus bindings_status reconciled
against real published packages for resources and data sources both, all six
`ubx-sdk-<provider>` packages regenerated/republished carrying real
`DataSourceBinding`, real generation provenance built into `ubx sdk gen` and
propagated into all six repos' CI and the docs pipeline, UBI-197's own root
cause found and fixed).

Real, named follow-up work, not yet started:

- **UBI-197's own naming-divergence fix**: the docs corpus's openapi-sourced
  providers (azure/datadog/github/kubernetes) still disagree with real,
  current wire types because `ubiquex-docs`' own generation scripts were last
  run against a since-superseded revision of `ubx-provider-dynamic`. The
  provenance-check work this session makes this detectable (refuses a dirty/
  disagreeing batch) but does not itself re-run the generators — that's a
  real, separate regeneration pass once someone wants to spend the real
  `ubx-provider-dynamic` build + fetch time (azure/google took minutes each
  earlier this session).
- **Field-level content staleness**, found live this session, distinct from
  wire-naming divergence: even a data-source page whose wire type genuinely
  resolves can have example field values that no longer match the real
  current schema (real, live example: kubernetes singular lookups whose real
  `Config` struct is now empty, but old pages still show list-style
  pagination fields). Affected 423 pages this session, held back from the
  publish flip. No systematic fix built — would need real field data from a
  fresh `--dump-ir` per affected page, not just import-path patching.
- `--dump-ir`'s own `schema.json` could carry per-language identifiers
  directly, so `ubiquex-docs`' generators stop needing a full separate
  `--lang go --out` run just to recover them — would collapse each real
  batch to one `ubx sdk gen` invocation instead of two, closing the
  disagreeing-commit provenance failure mode at the root instead of only
  detecting it.
- Resource-side doubling correction (a real, different bug from the
  data-source naming divergence): `ubiquex-docs`' own `gcp_corrected_key`/
  `azure_corrected_wire` (`build_regen_schema.py`) and a second,
  differently-behaving copy of `gcp_corrected_key` (`gap_fill_apply.py`) are
  now almost entirely dead code against fresh dumps (`typename.Combine` in
  `ubx-provider-dynamic` already fixed this upstream) — retiring them needs
  pairing with a redirect pass for the already-published corpus's real mix
  of corrected/uncorrected paths.
- UBI-194: publish and acquire `ubx-provider-dynamic` for the other five
  providers (kubernetes already done) — recommendation on record is to wait
  for natural regeneration rather than forcing a metadata-only republish.

## Blocked

Nothing currently blocked.

## Before touching anything

- Never trust a "published"/"live" claim for a shared runtime or per-provider
  bindings repo from this monorepo's own state alone — verify against the real,
  separate repo/registry directly: a real `git log`/`diff` against the actual
  separate repo, or a real registry query (the Go module proxy, `jsr.io`,
  `pypi.org`), never infer "published" from a commit to the monorepo's own
  copy alone (CLAUDE.md rule 8). Same discipline for a branch with an open
  PR: confirm it's still open before pushing more commits to it.
- `ubx sdk gen` against a `[dynamic_providers.<name>]`/group source now warns
  (or, with `--require-clean-provenance`, refuses) when `ubx-provider-dynamic`'s
  local checkout is dirty or unpushed, and stamps real provenance into
  `--dump-ir` output and `--out`/`PROVENANCE.json` — do not assume a real
  generation's output is trustworthy without checking that stamp first,
  especially for anything meant to be committed or published.
- `docs/plan.md` and `docs/architecture.md` are the design-decision record for
  `ubiquex` itself; this file is not a substitute for either.

## Cross-repo state

`ubiquex` is the coordinating repo — this section is its responsibility to keep
current, not any other repo's own `STATE.md`. Verified directly (`gh api`), not
carried forward from memory, as of 2026-08-27/28.

**Schema repos** (`ubx-schema-<provider>`, real `manifest.json` + `members/`
group snapshots consumed via `provider.AcquireSchema`):

| Repo | Latest release | Carries real `min_binary_version`? |
|---|---|---|
| kubernetes | v3.0.1 | yes (`1.0.1`) |
| datadog | v1.0.0 | no — bootstrap fallback |
| azure | v1.0.0 | no — bootstrap fallback |
| google | v1.0.0 | no — bootstrap fallback |
| github | v1.0.0 | no — bootstrap fallback |
| aws | v1.0.0 | no — bootstrap fallback |

**`ubx-provider-dynamic`**: latest release `v1.0.1`, published per platform
with checksums, acquired via `provider.AcquireDynamicProviderBinary` — no
`UBX_PROVIDER_DYNAMIC_REPO` checkout required on the normal path.

**Shared runtimes** (not provider-specific — every one of the six providers
depends on all three):

| Repo | Package | Latest real version | Registry |
|---|---|---|---|
| `ubx-sdk-go` | `github.com/ubiquex/ubx-sdk-go` | `v0.2.0` | Go proxy (no CI, tags cut manually) |
| `ubx-sdk-typescript` | `@ubx/sdk` | `1.0.1` on npm, `0.1.2` on JSR | npm is real/current; JSR is frozen, not the six providers' own dependency target anymore |
| `ubx-sdk-python` | `ubx_sdk` | `0.2.0` | PyPI (no CI, published manually) |

All three verified to carry `DataSourceBinding` by downloading and
inspecting the real published artifact, not just querying the registry's
version number.

**SDK repos** (`ubx-sdk-<provider>`, three languages per repo) — latest
real version per repo, verified directly against PyPI/npm/the Go module
proxy:

| Repo | PyPI | npm | Go |
|---|---|---|---|
| kubernetes | 1.1.0 | 1.1.0 | v1.1.0 |
| github | 1.2.0 | 1.2.0 | v1.2.0 |
| datadog | 1.2.0 | 1.2.0 | v1.2.0 |
| azure | 1.1.0 | 1.1.0 | v1.1.0 |
| google | 1.2.0 | 1.2.0 | v1.2.0 |
| aws | 2.1.0 | 2.1.0 | v2.1.0 (module path `/v2`) |

All six confirmed to carry real `DataSourceBinding` content (downloaded and
inspected the real published artifact, not inferred from the version number
alone). Every one migrated `deno.json`/`package.json` from `jsr:@ubx/sdk` to
`npm:@ubx/sdk`, and `hash-watch.yml` now passes `--require-clean-provenance`
and commits a real `PROVENANCE.json`.

**Open PRs across the org**: none as of the last check this session — every
PR opened this session (`ubx-sdk-typescript#9` included) has been merged.
