# STATE.md — current state

> Rewritten, not appended, as the LAST act of every session. Holds only what's
> current — in flight, blocked, and what a fresh session needs before touching
> anything. History moves to `HISTORY.md` (narrative archive, consulted only when
> a session needs to know why a decision was made — not read on every open).

## In flight

**UBI-196: fully done.** All six `ubx-sdk-<provider>` repos regenerated,
published, and independently verified against the real registries (npm,
PyPI, the Go module proxy) carrying real `DataSourceBinding` content —

| Repo | PyPI | npm | Go |
|---|---|---|---|
| kubernetes | 1.1.0 | 1.1.0 | v1.1.0 |
| github | 1.2.0 | 1.2.0 | v1.2.0 |
| datadog | 1.2.0 | 1.2.0 | v1.2.0 |
| azure | 1.1.0 | 1.1.0 | v1.1.0 |
| google | 1.2.0 | 1.2.0 | v1.2.0 |
| aws | 2.1.0 | 2.1.0 | v2.1.0 (module path `/v2`) |

Real content drift confirmed on every provider except kubernetes (pure
toolchain lag, zero spec-content change) — github (a real rename,
`team.go` -> `team_with_member_count.go`), datadog (three new service
families: elastic/teams/twilio, one rename), azure (one field-level
schema change), aws (CFN registry grew 1716 -> 1722 files, Smithy corpus
also drifted), google (real content drift across the 262-member corpus).

Three further real gaps found and fixed live along the way, beyond what
was originally scoped:

1. **`ubiquex`'s own codegen templates hardcoded stale runtime pins** —
   `go.mod`'s `require` (`v0.0.0`), `pyproject.toml`'s dependency bound
   (`<0.2.0`, which structurally EXCLUDED the fix), and `package.json`'s
   dependency (`0.0.0`) would have kept reintroducing these gaps on
   every future regeneration for all six providers. Fixed at the source
   (`sdk/codegen/templates/{go,py,ts}/*.go`), committed to `ubiquex`
   main, whole `go test ./...` clean.
2. **All three shared runtimes (`ubx-sdk-go`, `ubx-sdk-typescript`'s
   `@ubx/sdk`, `ubx-sdk-python`'s `ubx_sdk`) had never published a
   release carrying `DataSourceBinding`** — not just Go. `ubx-sdk-go`
   v0.2.0 tagged and pushed (manual, no CI there). `@ubx/sdk` turned out
   to already be real and current on npm (`1.0.1` after a redundant
   patch republish triggered before this was noticed) — the workflow's
   own comments calling npm a placeholder were stale, fixed
   (`ubx-sdk-typescript#9`, open, not self-merged). `ubx_sdk` needed a
   real manual PyPI publish (`0.2.0`, credentials supplied by the
   founder directly in-session, used transiently via env var, never
   written to disk). All six providers' `deno.json`/`package.json`
   migrated from the frozen `jsr:@ubx/sdk@^0.1.0` to `npm:@ubx/sdk@^1.0.0`
   as part of each regeneration PR.
3. **A real bug in every provider's own `publish.yml`**: its
   "already tagged" branch checks only whether the committed version
   STRING already has a tag, never whether real content changed since
   that tag. Since codegen deliberately leaves the version number
   unbumped (publish.yml's own job), this branch fires on exactly the
   shape a real regeneration produces and silently no-ops while still
   reporting success — confirmed live on kubernetes's own first publish
   attempt (the `sdk/go/v1.0.0` tag still pointed at the
   pre-regeneration commit after a "successful" run). Sidestepped this
   pass via an explicit MINOR version bump per repo (matching each
   repo's own stated new-files-added policy) rather than leaving it to
   the buggy branch. **Real fix now landed as a PR on all six** (adds a
   `git diff --quiet` check against the tagged commit's own `sdk/`
   trees — the backfill branch only fires when content genuinely
   hasn't changed, otherwise falls through to the existing routine bump
   logic), tested directly against a constructed git repo for both
   cases, none merged, none self-merged: kubernetes#17, github#16,
   datadog#15, azure#19, google#22, aws#20.

AWS's own hash-watch.yml gap (never referenced `aws_data_all`, only
`--only aws`) was fixed and merged first (`ubx-sdk-aws#18`) — confirmed
live that `"aws"` is itself a declared member of `aws_data_all`, so one
`--only aws_data_all` invocation covers both the CFN resource half and
the full Smithy data-source half together.

**UBI-197, re-measured against the real regenerated/published content —
the correlation holds, confirmed, not regeneration lag:**

| Provider | Source | Match % (before) | Match % (after regen) |
|---|---|---|---|
| gcp | discoverydoc | 99.8% | 99.8% |
| aws | cloudformation+smithy | 99.1% | 99.0% |
| github | openapi | 85% | 85.1% |
| kubernetes | openapi | 52% | 52.0% |
| datadog | openapi | 43% | 42.7% |
| azure | openapi | 36% | 36.0% |

Essentially unchanged before vs. after a real, full regeneration —
regenerating the SDK packages doesn't touch the docs corpus's own
data-source page titles at all, so "regeneration lag" is ruled out.

**Root cause confirmed and posted to UBI-197 (comment, not the
description — preserves the original investigation)**: the docs corpus
was built from a dirty, uncommitted, never-merged local checkout of
`ubx-provider-dynamic`. That checkout was sitting on branch
`typename/dlp-triple-repeat-fix` with real WIP (an untracked
`internal/dsfilter/` package, modified `resourcemap/datasource.go` and
`discoverydoc/datasource.go`) at the exact moment the docs corpus's
data-source pages were generated (2026-08-26 00:07-00:49) — confirmed
via `git log -S` on the WIP's own key function returning zero commits
anywhere, and via file mtimes bracketing the docs-generation timestamp.
The WIP includes real collection-envelope unwrapping wired into the
openapi path only (`ConfigurationListResult` -> `ConfigData`,
`AnalysisServicesServers` -> `AnalysisServicesServer`) — gcp/aws don't
go through that code path, which is exactly why they're unaffected.
`ubx sdk gen` builds `ubx-provider-dynamic` from whatever's in the
sibling checkout, dirty or not, no pinning, no provenance record
(`cli/dynamicprovider.go`'s own doc comment already admits this gap).
Every regeneration since (including this session's) ran against a
clean checkout, so the corpus and fresh reality diverged the moment the
WIP was written, not gradually. Quantitative confirmation (azure): 1,310
of 1,551 real-wires-not-in-docs (84.5%) match the wrapper/list-result/
operation-status shape the unwrap logic would have renamed.

Two-implementations framing: confirmed for GCP/Azure **resource**-side
naming (`typename.Combine` in `ubx-provider-dynamic` has two more
independent, mutually-inconsistent reimplementations on the docs side —
`build_regen_schema.py`'s `gcp_corrected_key`/`azure_corrected_wire` and
a second, differently-behaving copy of `gcp_corrected_key` in
`gap_fill_apply.py` — now almost entirely dead code against fresh
dumps, 0/1095 azure wires altered, 1/1542 gcp). Refuted for openapi
**data sources** specifically — `gen_all_data_source_pages.py` reads the
real `WireType` straight from freshly generated Go source plus a real
`--dump-ir` dump, no independent derivation at all; one implementation,
built from an inconsistent revision.

**Provenance fix built and landed** (`26610b9`), after reporting the
design and getting explicit confirmation first: `ubx sdk gen` now
resolves and stamps a real provenance record (source: local-checkout or
explicit-binary, commit, dirty, unpushed) into `--dump-ir`'s own output
directory and a new `PROVENANCE.json` in `--out`, for every
`[dynamic_providers.<name>]`/group generation — unconditionally, printed
to stderr too. Dirty or unpushed warns loudly by default and generation
proceeds (the local-checkout-build path exists specifically for
iterating on `ubx-provider-dynamic` itself, which needs a dirty tree by
construction — refusing unconditionally would block that real,
intended workflow); a new `--require-clean-provenance` flag turns the
same condition into a hard refusal, for any generation meant to be
committed or published (CI, a batch regeneration). Real, hermetic tests
against constructed git repos for every condition (clean+pushed, dirty
via a modified file, dirty via an untracked file, no upstream
configured, real commits ahead of upstream, explicit-binary); verified
live against both a clean and a deliberately dirtied real checkout, in
both warn and refuse mode.

Same discipline UBI-194 already established for the real, pinned-release
path (a resolvable version + checksum) applied one layer up, to the
local-checkout-build path that's what every real generation this
session (and, by file mtimes, the docs corpus's own past batch runs)
actually used.

**Separate finding, posted to UBI-197**: this means every real artifact
produced before today has unknown provenance and cannot be
retroactively verified — the current published docs corpus, and the six
`ubx-sdk-<provider>` packages this session regenerated and published,
were all built through the same unpinned, unstamped path this fix
closes. This session's own six regenerations are very likely clean (a
freshly cloned, unmodified checkout, not the dirty WIP branch that
produced the original corpus), but there is no real record confirming
it, only the absence of a known reason to doubt it.

Not yet built, named as explicit follow-up: `PROVENANCE.json` landing in
each of the six published `ubx-sdk-<provider>` repos requires their own
`hash-watch.yml` rsync steps to pick it up (currently only the three
language subdirectories are copied). Separately, `--dump-ir`'s own
`schema.json` could carry per-language identifiers directly so the docs
generator stops needing a full SDK generation just to recover them.
Separately again (resource-side, different bug, not provenance-related):
retire the three redundant docs-side doubling correctors, paired with a
redirect pass for the already-published corpus's mix of corrected/
uncorrected paths.

---

**UBI-196's earlier (pre-regeneration) docs-corpus work, done first —
summary:**

- 4,098 resource pages flipped `local_only -> published` (verified: real `go
  build` against real local checkouts, `ast.parse`, a real-struct-field
  cross-check, `mint validate` — all clean).
- 449 more (GCP version-qualified families, previously thought to need a
  `ubx-provider-dynamic` naming fix) flipped too, once a real, live
  regeneration proved the naming-synthesis bug was already fixed upstream
  (UBI-185) and the stale pages just needed reprocessing against real
  published ground truth — no code change needed or made.
- 7,310 data-source pages flipped `published -> local_only` (structural fix:
  `DataSourceBinding` doesn't exist in any of the six published packages yet,
  confirmed live). Does NOT fix per-page wire/binding accuracy — filed
  separately as UBI-197 (see below).
- 5 GitHub resources held back (`github_repository_ruleset`,
  `github_network_configuration`, `github_get_budget`,
  `github_actions_hosted_runner`, `github_custom_property`) — a real "Org"
  field shown in their example doesn't exist in the real published Config
  struct. Genuine content drift, not a naming issue this pass could safely
  correct.

The real group-shaped regeneration mechanism is now known (found in each SDK
repo's own `hash-watch.yml`, not reinvented) — see Cross-repo state below.
Full regeneration+republish of the six SDK repos remains real, separate,
unstarted work.

**UBI-197 (new): the docs corpus's data-source pages disagree with the real,
current schema, and the size varies enormously by provider** — real match
rates against a fresh `ubx sdk gen` dump, verified live for all six: gcp
99.8%, aws 99.1%, github 85%, kubernetes 52%, datadog 43%, azure 36%. In
every provider the corpus is more INCOMPLETE (real data sources with no docs
page) than WRONG (docs pages matching nothing real) — for Kubernetes
specifically, 34 of its 36 "wrong" pages are actually renamed (a real
`_list`-suffixed entry exists), not removed. Report only, no fix proposed —
see the ticket.

Corrected a real measurement error from this session's own earlier report:
Datadog's data-source match rate was first reported as 4%, measured against
`--only datadog` (the bare name), which silently covers only one of
`datadog_all`'s real declared entries. The real rate against the full group
is 43%.

All three UBI-196 docs-corpus commits landed as direct commits to
`ubiquex-docs` `main` (that repo's own confirmed direct-push convention),
each independently verified via `gh api` after pushing, not trusted from
local git alone. The regeneration/republish work described above closes
the rest of UBI-196 and the "regeneration lag" half of UBI-197's own
hypothesis.

## Blocked

Nothing currently blocked.

## Before touching anything

- Never trust a "published"/"live" claim for a shared runtime or per-provider
  bindings repo from this monorepo's own state alone — verify against the real,
  separate repo/registry directly: a real `git log`/`diff` against the actual
  separate repo, or a real registry query (the Go module proxy, `jsr.io`,
  `pypi.org`), never infer "published" from a commit to the monorepo's own
  copy alone (CLAUDE.md rule 8). Same discipline for a branch with an open
  PR: confirm it's still open before pushing more commits to it — hit three
  times in one session, caught only by accident each time.
- `docs/plan.md` and `docs/architecture.md` are the design-decision record for
  `ubiquex` itself; this file is not a substitute for either.

## Cross-repo state

`ubiquex` is the coordinating repo — this section is its responsibility to keep
current, not any other repo's own `STATE.md`. Verified directly (`gh api`), not
carried forward from memory, as of 2026-08-27.

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

Recommendation on record (UBI-194): wait for these five to regenerate
naturally rather than forcing a metadata-only republish.

**`ubx-provider-dynamic`**: latest release `v1.0.1`, published per platform
with checksums, acquired via `provider.AcquireDynamicProviderBinary` — no
`UBX_PROVIDER_DYNAMIC_REPO` checkout required on the normal path.

**Shared runtimes** (not provider-specific — every one of the six providers
depends on all three):

| Repo | Package | Latest real version | Registry |
|---|---|---|---|
| `ubx-sdk-go` | `github.com/ubiquex/ubx-sdk-go` | `v0.2.0` | Go proxy (no CI, tags cut manually) |
| `ubx-sdk-typescript` | `@ubx/sdk` | `1.0.1` on npm, `0.1.2` on JSR | npm is real/current (verified); JSR is frozen, not the six providers' own dependency target anymore |
| `ubx-sdk-python` | `ubx_sdk` | `0.2.0` | PyPI (no CI, published manually this session) |

All three verified to carry `DataSourceBinding` by downloading and
inspecting the real published artifact, not just querying the registry's
version number.

**SDK repos** (`ubx-sdk-<provider>`, three languages per repo) — latest
real version per repo, verified directly against PyPI/npm/the Go module
proxy, 2026-08-27:

| Repo | PyPI | npm | Go |
|---|---|---|---|
| kubernetes | 1.1.0 | 1.1.0 | v1.1.0 |
| github | 1.2.0 | 1.2.0 | v1.2.0 |
| datadog | 1.2.0 | 1.2.0 | v1.2.0 |
| azure | 1.1.0 | 1.1.0 | v1.1.0 |
| google | 1.2.0 | 1.2.0 | v1.2.0 |
| aws | 2.1.0 | 2.1.0 | v2.1.0 (module path `/v2`) |

All six confirmed to carry real `DataSourceBinding` content (spot-checked
by downloading and inspecting the real published artifact, not inferred
from the version number alone). Every one of the six also migrated its
`deno.json`/`package.json` from `jsr:@ubx/sdk` to `npm:@ubx/sdk`.

**Open PRs across the org**: `ubx-sdk-typescript#9` (stale npm-placeholder
comment fix, open, not self-merged — everything else from this session's
own regeneration work is merged).
