# STATE.md — current state

> Rewritten, not appended, as the LAST act of every session. Holds only what's
> current — in flight, blocked, and what a fresh session needs before touching
> anything. History moves to `HISTORY.md` (narrative archive, consulted only when
> a session needs to know why a decision was made — not read on every open).

## In flight

UBI-191 (developer documentation site): two slices built and pushed --
new repo `github.com/Ubiquex/ubiquex-internals` (private, Mintlify),
scaffolded on `ubiquex-docs`'s own real shape. Confirmed founder
decision: `docs/architecture.md`, `docs/schema.md`, `docs/plan.md` stay
in `ubiquex`, the site links out rather than becoming a second copy.
Sync mechanism decided and built, not just designed: `sync-state.json`
records the `ubiquex` commit each mirrored file was last reviewed
against; `.github/workflows/sync-drift-watch.yml` (modeled on
`ubiquex-docs`'s own `coverage-watch.yml`) runs weekly and opens/updates
one standing issue if a mirrored source moves on -- verified with a real
dry run each slice.

Three sections live so far: Overview (slice 1), Concepts (nine pages --
proposal, ledger, IR, resolver, executor, drift, cross-stack references,
staleness, blueprints, one each) and Architecture (the trust chain as a
real Mermaid diagram, the four trust-chain invariants, why the execution
layer speaks tfplugin directly, failure semantics) (slice 2). Concepts
pulled from two source docs not previously mirrored -- `docs/resolver.md`
and `docs/executor.md` -- plus `docs/blueprint.md` for the blueprints
page; all three registered into `sync-state.json` as their content was
drawn from, not retrofitted after. Deliberately different register from
`ubiquex-docs`'s own existing `concepts/` pages (which are user-facing,
second-person "how do I use this" content) -- these are third-person,
system-design pages for someone trying to understand the build, not use
the product. `mint validate` and `mint broken-links` both clean each
slice. Pushed, verified via `gh api repos/Ubiquex/ubiquex-internals/commits/main`
-- `d86efd2` current.

Confirmed NOT done this pass, named so a fresh session doesn't assume
otherwise: Schema constitution, Repository map, Provider system, SDK and
codegen, Docs pipeline, Workflows, Decisions -- seven sections remain,
plus the provider-sources-converging diagram named for whichever section
covers the provider system. Whether the Decisions section is authored
fresh or extracted from Linear history is still an open question in the
ticket itself, unresolved. UBI-191 left **In Progress** in Linear, not
closed -- two slices of what the ticket names as ten sections total.

UBI-196/197/198/199/202 fully closed this
session; UBI-200/201 filed, not built -- see `HISTORY.md`'s own
"UBI-196/197/198/199/200: docs corpus bindings_status arc, full close" entry
for the complete arc through UBI-199's merge. Short version: all six
providers' schema generation is now pinned to a real, published snapshot
(`ubiquex` `b40beb2`) instead of live-fetching, closing UBI-197's own
naming-divergence category for good (98 pages regenerated and verified,
`ubiquex-docs` `e5581fb5f`); the pinning fix's own two recurrence gaps are
closed too (`ubiquex` `2371b4d`, `ubiquex-docs` `336285fd9`). UBI-198's own
candidate-discovery "fix" turned out to have no real target once tested
empirically -- verified live (real, throwaway Go tests against the actual
specs) that `DiscoverDataSources` cannot structurally produce an unreachable
candidate at all, so the 380 held-back wires it named were never a live bug,
just stale content from the same dirty, since-reverted WIP checkout the
GitHub pilot finding already identified. Removed all 380 pages
(`ubiquex-docs` `df5d9b424`).

UBI-199's own 908-page placement problem, now fully closed: removed 859 with
a real resource page elsewhere (`ubiquex-docs` `230b08771`, same commit
fixed the 17 stale nav references); created Azure's 10
`network/virtualnetwork` resource pages, blocked only on this session's own
earlier UBI-193 bundling fix, no code change needed (`ubiquex-docs`
`fe59fe82d`). AWS's own 39 needed a real root-cause fix, not a workaround:
`--dump-namespaces`' snapshot path never got the mixed-source dispatch fix
`Summarize`/`buildMixedSourceServer` already had, so it failed outright
against AWS's real CloudFormation+Smithy group (the only mixed-source group
in this org, only pinned this session -- this exact path had never run
against a real mixed group before). Fixed in `internal/snapshot.Namespaces`,
hermetically tested, verified live against the real pinned snapshot -- PR
`ubx-provider-dynamic#31`, merged (`105a5ba4a`). The 39 pages generated
against the fix and verified live: `--dump-ir` confirms DataZone/
DataPipeline/DataSync/GlueDataBrew/DataExchange all resolve correctly and
land under the right directories, not `/data/`. 33 published
(`ubiquex-docs` `d891e93a4`), 6 held back -- 1 for a newly-found, separate
bug (Go's own `_windows.go` implicit build-constraint suffix silently
excludes a real file from non-Windows builds, already live in published
`ubx-sdk-aws@2.1.0`, filed as UBI-201, not fixed), 5 for the already-known
"Computed-branded field-shape mismatch" `deno check` failure category.

**Consequence measured, not assumed, and larger than UBI-199's own scope**:
921 of 1,715 real AWS resource types (54%) got a wrong service under the old
mechanical-split fallback, not just the 39 originally visible. UBI-202
(closed) covered the other 882, with real, full verification this time, not
a sample: extracted and checked every one of the 880 misfiled pages'
existing Go/TS/Python code against the real, currently-published SDK
(`go build` against the real v2.1.0 module, `deno check` against the real
npm package, `python ast.parse`) -- **732 of 880 clean across all three
languages, relocated** (`ubiquex-docs` `a8d737d3b`); **148 held back**, not
regenerated this pass -- 4 for real missing Go SDK bindings (undefined
symbols against the published package, real content gaps, not a namespace
artifact), 144 for the pre-existing "Computed-branded field-shape mismatch"
class already tracked below. The earlier 20-item sample (100% match)
undercounted the real failure rate; full verification was the right call.
Real finding that narrowed the fix: the pre-existing nav already had all
732 under their correct display group via `artifacts/aws/categories.json`'s
own per-wire labels, independent of the broken path derivation -- only the
file path and its one nav string were wrong, so this was a path rename, not
group restructuring (299 top-level AWS nav groups before and after,
unchanged). 732 redirects added for the old published URLs. Of the 2
originally-missing wires, `aws_identity_store_user` generated and
published (verified clean in all three languages);
`aws_support_auth_z_support_permit` generated but held back, same TS
mismatch class as the 144.

Also caught and fixed during this pass: `gen_provider_docs.py`'s Go
import-path template never included a package's real major-version path
segment -- confirmed live against the Go module proxy that AWS's SDK is
genuinely at `v2` (every other provider still pre-v2), so every AWS Go
example generated this session (UBI-199's 33 plus the new
`identitystore/user` page) named a package the real, published module
can't resolve. Patched the generator (`REAL_SDK_GO_MODULE_MAJOR`) and
retroactively fixed the import line in all 33 already-published UBI-199
pages, re-verified `go build` clean against the real v2.1.0 module for all
34.

Remaining, not done: 148 pages still misfiled at their old paths (4 need
real Go SDK binding generation, 144 need the same TS-example fix as the
existing 316-page "Field-level content staleness" follow-up below -- these
144 have not yet been folded into that item or given their own ticket),
plus `aws_support_auth_z_support_permit`.

Real, named follow-up work, not yet started:

- **Field-level content staleness**, found live this session, distinct from
  wire-naming divergence: even a data-source page whose wire type genuinely
  resolves can have example field values that no longer match the real
  current schema (real, live example: kubernetes singular lookups whose real
  `Config` struct is now empty, but old pages still show list-style
  pagination fields). Affected 423 pages this session, held back from the
  publish flip. No systematic fix built — would need real field data from a
  fresh `--dump-ir` per affected page, not just import-path patching. The
  144 AWS pages UBI-202 held back for the same "Computed-branded
  field-shape mismatch" `deno check` failure belong in this same bucket --
  not yet folded in or separately ticketed.
- AWS: 4 resource types confirmed to have zero real Go SDK bindings in the
  published `ubx-sdk-aws@2.1.0` package despite being real, valid
  `ResourceBinding` types (`aws_network_firewall_logging_configuration`,
  `aws_resource_groups_tag_sync_task`, `aws_vpc_lattice_auth_policy`,
  `aws_vpc_lattice_resource_policy`), plus `aws_support_auth_z_support_
  permit`'s own generated page failing the Computed-branded `deno check`
  class above -- found via UBI-202's full verification pass, not yet
  ticketed.
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
- UBI-201: Go's own `_windows.go` implicit build-constraint suffix silently
  excludes a generated file from non-Windows builds — confirmed live in
  published `ubx-sdk-aws@2.1.0` (3 real bindings affected), fix likely
  belongs in `sdk/codegen/templates/go`'s file-naming logic, not built.
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

**Open PRs across the org**: none. `ubx-provider-dynamic#31` (the real
UBI-199 AWS namespace mixed-source fix) merged this session (`105a5ba4a`),
verified via `gh pr view` as of 2026-08-28. Every PR opened this session has
been merged.
