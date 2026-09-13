# STATE.md — current state

> Rewritten, not appended, as the LAST act of every session. Holds only what's
> current — in flight, blocked, and what a fresh session needs before touching
> anything. History moves to `HISTORY.md` (narrative archive, consulted only when
> a session needs to know why a decision was made — not read on every open).

## In flight

**ubx v0.6.0 is released (2026-09-13).** Two things that were quietly
untrue are now true: a blueprint's content hash reaches the ledger on
the path most people use (UBI-257), and this module's own `blueprint`
package can be imported by other Go programs (UBI-254).

Both were verified the only way that proves anything. The hash was
followed through a real `ubx why` on a shipped ledger, not just a
resolved document. Importability was checked by building and RUNNING an
external program against `v0.6.0` fetched from `proxy.golang.org`, since
that defect exists only in the published zip and is invisible from a
working tree by construction.

**Blueprints as code is complete and its follow-on tickets are closed.**
UBI-258, UBI-259, UBI-261, UBI-262, UBI-263 all resolved or closed as
overtaken. The one thing worth carrying forward: a rule shipped in
UBI-261 (an unset blueprint output fails the call) turned out too broad
and was softened in #143, because a code blueprint can legitimately
leave an output unset when the resource producing it was only created on
some branch. Both tickets record why, so a reader finding a rule
shipped and softened two PRs later can see that neither version was
wrong.

**Open PRs, all green, none merged at the time of writing:** ubiquex
**#143** (conditional outputs), **ubx-docs-users#38** (the store page
rename), and **ubx-provider-dynamic#77** (UBI-252's fixture
permissiveness checks).

**What is genuinely still open, walked rather than read off the board.**
The core flow is sound end to end against `fakeprovider`: init, plan,
ship, status, scan, why, history.

- **UBI-255 is deliberately left open.** Its title claims `go test` is
  not hermetic, and a cold cache still reaches the network. #141 fixed
  the flakiness and the failure mode, not the hermeticity. Closing it
  properly means mirroring the asset or gating the tests, and the
  checksum half is untouched: `pyeval` still verifies shape, not
  content, and the CI cache #141 added now PERSISTS an unverified
  extract across runs. The upstream release publishes a digest that
  matches the bytes served, so pinning it is a few lines.
- **UBI-252 is closed for `ubiquex`** and open for the rest. The
  `ubx-provider-dynamic` half is #77. No permissiveness checks were
  written for the `dynserver` and `smithy` fakes on purpose: both are
  genuinely REST-shaped, neither exercises a not-found path at all, and
  inventing narrowness without contact with the real APIs would be
  speculation that looks like coverage.
- **UBI-264** (two `pascalCase` implementations disagreeing on hyphens)
  and **UBI-263**'s remaining half are both low priority.
- **The Ubxfile's own removal stays deliberately undecided.** Both
  models work. UBI-258 and UBI-259 were closed as resolved-for-code and
  both say explicitly that they reopen if the Ubxfile stays first-class.

**This file was 3,197 lines and is now 244 (2026-09-13).** Rule 3 says
it holds only current state; it had grown back into an append-only log,
which is the exact shape UBI-183 corrected once already. Everything
removed was closed work and moved verbatim to `HISTORY.md` under a
dated heading, not summarised, because that file's value is answering
why a decision was made. Line accounting was checked both ways: 2,953
lines left here, 2,976 arrived there, and the difference is exactly the
new headings.

**Two traps that cost real time recently.**

The Go module proxy's `@latest` is cached and lags a published version;
query `@v/<version>.info`, or just `go get` it, which is the real test.
npm and PyPI both take minutes to serve a freshly published version, and
checking too early looks exactly like a failed publish.

A version-bump PR opened by a publish workflow cannot merge until its
bot-triggered CI run is approved: it sits at `action_required` with no
checks reported, which reads as broken branch protection. `gh api -X
POST repos/<owner>/<repo>/actions/runs/<id>/approve` releases it.

**Docs debt (UBI-265), reason narrowed.** Nothing in `ubx-docs-users`
documents the Python blueprint calling path at all, including UBI-130's
`<name> @ <url>` syntax that predates this. The original reason for
holding is gone: provenance for a code blueprint was the open question,
and UBI-266 settled it. What still holds the page is UBI-265's own
undecided part, what happens to a blueprint with third-party
dependencies, since a page describing the calling path would have to
either state that limitation or pretend it away. Writable as soon as
that is decided, or sooner if it is written to name the limitation
plainly.

**Docs debt (UBI-266), cleared.** `as-code.mdx` sent a code-blueprint
author into `call-sdk.mdx`, whose `ubx why` transcript was true only for
the BUILT model. Call-site attribution makes it true for both, and
`ubx-docs-users#39` documents it: a provenance section on
`as-code.mdx`, a note on `call-sdk.mdx`, and the tutorial's go.mod
moved to `ubx-sdk-go` v0.6.0. Transcripts verified against a real
hermetic plan/ship/why cycle. Merged 2026-09-13 at `1e28f80`.

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

`ubiquex` is the coordinating repo -- this section is its responsibility to keep
current, not any other repo's own `STATE.md`. Verified directly against the real
registries rather than carried forward from memory.

**Published runtimes, each content-verified on 2026-09-13**, meaning the
artifact was fetched and the new symbol found inside it, not merely the
version number read:

| package | version | verified by |
|---|---|---|
| `ubx-sdk-go` | **v0.5.0** | proxy zip contains `BlueprintOutputs`, and a fresh module `go get`s it |
| `@ubx/sdk` (npm) | **1.0.3** | `npm pack`ed, `dist/` contains `blueprintOutputs` |
| `ubx-sdk` (PyPI) | **0.2.2** | wheel downloaded, contains `blueprint_outputs` |
| `ubx` itself | **v0.6.0** | downloaded, checksum matched, binary run |

`sdk/ts`'s own `runtime/deno.json` still says 0.1.2 and that is correct:
npm is driven by `package.json`, and JSR is a separate, still-manual
track its own `publish.yml` documents.

**Documentation sites, verified live 2026-09-06.** Three repos, all public.

| repo | role | state |
|---|---|---|
| `ubx-docs-users` | user docs, `docs.ubiquex.io` | live, 147 pages, deploys on push to `main` |
| `ubx-docs-providers` | provider reference, `providers.ubiquex.io` | live, ~26,500 pages |
| `ubx-docs-ui` | shared UI, npm `@ubx/docs-ui` | **0.4.0** published, both sites consume it |

`@ubx/docs-ui` 0.3.0 exists in git history but was never published: 0.4.0
superseded it before a release was cut. Both sites pin `^0.4.0`, and on a
0.x version npm's caret does not cross the minor, so a future 0.5.0 needs
an explicit bump in each site. That is deliberate, and it is also how the
provider site sat on 0.1.0 for a while after 0.2.0 shipped.

Neither `ubx-docs-users` nor `ubx-docs-ui` carries a `CLAUDE.md`, where
`ubx-docs-providers` does. Real gap, not yet filled. None of the three
carries the `STATE.md`/`HISTORY.md` pair, which is correct: rule 3 names
`ubx-provider-dynamic`, the six `ubx-sdk-*` and the six `ubx-schema-*`
**Schema repos** (`ubx-schema-<provider>`, real `manifest.json` + `members/`
group snapshots consumed via `provider.AcquireSchema`):

| Repo | Latest release | Carries real `min_binary_version`? |
|---|---|---|
| kubernetes | v3.0.1 | yes (`1.0.1`) |
| datadog | v1.0.1 | yes (`1.0.2`, UBI-181) |
| azure | v1.0.1 | yes (`1.0.2`, UBI-181) |
| google | v1.0.1 | yes (`1.0.2`, UBI-181) |
| github | v1.0.1 | yes (`1.0.2`, UBI-181) |
| aws | v1.0.0 | no — bootstrap fallback |

`sdk/providers/.ubx/config` pins azure/github/google/datadog at `1.0.1`
(aws stays `1.0.0`, kubernetes stays `3.0.1`), resolving cleanly against
the real releases above.

**`ubx-provider-dynamic`**: latest release `v1.0.2`, published per platform
with checksums, acquired via `provider.AcquireDynamicProviderBinary` — no
`UBX_PROVIDER_DYNAMIC_REPO` checkout required on the normal path. One open
PR against it: #40 (UBI-206, not merged — see "In flight").

**Shared runtimes** (not provider-specific — every one of the six providers
depends on all three):

| Repo | Package | Latest real version | Registry |
|---|---|---|---|
| `ubx-sdk-go` | `github.com/ubiquex/ubx-sdk-go` | `v0.3.0` | Go proxy (no CI, tags cut manually) |
| `ubx-sdk-typescript` | `@ubx/sdk` | `1.0.2` on npm, `0.1.2` on JSR | npm is real/current; JSR is frozen, not the six providers' own dependency target anymore |
| `ubx-sdk-python` | `ubx_sdk` | `0.2.1` | PyPI (now has a real `publish.yml`, UBI-225 -- was manual-only before) |

All three verified live 2026-09-01 (UBI-225's own `BlueprintName` field):
Go proxy resolves `v0.3.0` to the exact merge commit; npm's `1.0.2` and
PyPI's `0.2.1` both confirmed by downloading and inspecting the real
published artifact, not just querying the registry's version number.
`ubiquex`'s own `sdk/go`/`sdk/ts`/`sdk/py` submodule pins are current
with these three releases.

**SDK repos** (`ubx-sdk-<provider>`, three languages per repo) — latest
real version per repo, verified directly against PyPI/npm/the Go module
proxy:

| Repo | PyPI | npm | Go |
|---|---|---|---|
| kubernetes | 1.1.0 | 1.1.0 | v1.1.0 |
| github | **1.2.2** | **1.2.2** | **v1.2.2** (module at `sdk/go`, not repo root) |
| datadog | 1.2.0 | 1.2.0 | v1.2.0 |
| azure | 1.1.0 | 1.1.0 | v1.1.0 |
| google | 1.2.0 | 1.2.0 | v1.2.0 |
| cloudflare | 1.0.1 | 1.0.1 | v1.0.1 |
| digitalocean | 1.0.1 | 1.0.1 | v1.0.1 |
| aws | 2.1.0 | 2.1.0 | v2.1.0 (module path `/v2`) |

All confirmed to carry real `DataSourceBinding` content (downloaded and
inspected the real published artifact, not inferred from the version number
alone). Every one migrated `deno.json`/`package.json` from `jsr:@ubx/sdk` to
`npm:@ubx/sdk`, and `hash-watch.yml` now passes `--require-clean-provenance`
and commits a real `PROVENANCE.json`.

**github only re-verified this session (UBI-249), post-publish, per
registry** — see the UBI-249 entry below for the full account.
kubernetes/datadog/azure/google/cloudflare/digitalocean's own rows above
predate this session and were not re-checked against the registries
this pass; --descriptions-dir and PROVENANCE.json fix PRs merged for
all seven this session (kubernetes/azure/google/datadog/cloudflare/
digitalocean/github) did not necessarily trigger a new publish for the
six not re-verified here — confirm each repo's own committed version
against its live registry before trusting the numbers above as
currently published, not just currently committed.

**Open PRs across the org**: `ubx-provider-dynamic#40` (UBI-206, real
path-param PascalCase collision fix, tested and pushed, deliberately not
merged per "never self-merge"). The four `ubx-schema-<provider>` PRs from
this same UBI-181 batch (`#7`/`#7`/`#7`/`#9` in azure/github/google/datadog)
all merged, verified via `gh pr list --state all` as of 2026-08-29.

Plus nine real, `actionlint`-clean PRs from UBI-225's own second finding
(publish.yml pushed directly to a now-branch-protected main; fixed to
open a PR instead, matching the schema repos' own hash-watch.yml
precedent), all left open, not self-merged: `ubx-sdk-typescript#16`
(closes real version drift from before the fix), `ubx-sdk-typescript#17`,
`ubx-sdk-aws#32`, `ubx-sdk-azure#29`, `ubx-sdk-google#31`,
`ubx-sdk-datadog#25`, `ubx-sdk-github#24`, `ubx-sdk-kubernetes#25`,
`ubx-sdk-digitalocean#7`. `ubx-sdk-python#15` (the version-bump PR the
new, fixed publish.yml itself opened on its first real dispatch) is
also open, same reason.
