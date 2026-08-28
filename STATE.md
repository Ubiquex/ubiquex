# STATE.md — current state

> Rewritten, not appended, as the LAST act of every session. Holds only what's
> current — in flight, blocked, and what a fresh session needs before touching
> anything. History moves to `HISTORY.md` (narrative archive, consulted only when
> a session needs to know why a decision was made — not read on every open).

## In flight

Nothing currently in flight. UBI-196/197 both closed this session; UBI-198,
UBI-199, UBI-200 filed as real, separate follow-up (198/200 not built, 199's
own recurrence-gap half built and verified) -- see `HISTORY.md`'s own
"UBI-196/197/198/199/200: docs corpus bindings_status arc, full close" entry
for the complete arc (root cause, all four verification passes, every
commit). Short version: all six providers' schema generation is now pinned
to a real, published snapshot (`ubiquex` `b40beb2`) instead of live-fetching,
closing UBI-197's own naming-divergence category for good (98 pages
regenerated and verified, `ubiquex-docs` `e5581fb5f`); the pinning fix's own
two recurrence gaps (hardcoded scratch paths, provenance silent on whether a
schema fetch was pinned) are closed too (`ubiquex` `2371b4d`, `ubiquex-docs`
`336285fd9`). UBI-197's own remaining 908-page placement problem (UBI-199)
and UBI-198's own candidate-discovery fix are real, scoped, NOT built.

Real, named follow-up work, not yet started:

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
- UBI-198: candidate discovery (`resourcemap.DiscoverDataSources`) mints a
  data-source candidate for any unclaimed GET with no check for whether the
  response schema is a real top-level operation response — fix scoped, not
  built. See `HISTORY.md`'s own arc entry for the full real breakdown.
- UBI-199: 908 real resources sitting under `/data/` as stale data-source
  pages — 859 need removal (a real resource page already exists elsewhere),
  49 need a resource page created first (no existing page to fall back on).
  Neither built yet.
- UBI-200: a directory pinned at generation time has no way to detect a
  newer real snapshot published since — three real design options named,
  none decided.

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
  especially for anything meant to be committed or published. That record
  now ALSO carries `schema_pinned`/`schema_source`/`schema_version` (or
  `schema_url` when live) per provider (UBI-199) — `ubiquex-docs`' own
  `check_provenance` refuses on unpinned or missing the same way it already
  refused on dirty; a record without `schema_pinned` at all (anything
  generated before this fix) reads as unknown, never as implicitly pinned.
- `docs/plan.md` and `docs/architecture.md` are the design-decision record for
  `ubiquex` itself; this file is not a substitute for either.
- `sdk/providers/.ubx/config` now pins all six providers (`source`/`version`
  against each real, published `ubx-schema-<name>` snapshot) instead of
  live-fetching `schema_url` -- a `--dynamic-provider-bin`/
  `UBX_PROVIDER_DYNAMIC_REPO`-built binary is still required (the pinned
  branch under `[dynamic_providers.<name>]`, unlike `[providers.<name>]`'s
  own `ubx resolve` path, does not yet resolve its own binary via
  `provider.AcquireDynamicProviderBinary`). A provider without a real
  published snapshot yet goes back to the live `schema_source`/`schema_url`
  shape -- see the config file's own top-of-file comment before adding one.

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
