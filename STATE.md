# STATE.md — current state

> Rewritten, not appended, as the LAST act of every session. Holds only what's
> current — in flight, blocked, and what a fresh session needs before touching
> anything. History moves to `HISTORY.md` (narrative archive, consulted only when
> a session needs to know why a decision was made — not read on every open).

## In flight

**UBI-196: docs corpus bindings_status recompute, done for resources and the
structural half of data sources.** See the ticket for the full report —
summary:

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

All three UBI-196 landed as direct commits to `ubiquex-docs` `main` (that repo's own
confirmed direct-push convention), each independently verified via `gh api`
after pushing, not trusted from local git alone.

**Regeneration/republish of the six `ubx-sdk-<provider>` repos: in flight,
blocked on founder PR review.** The real mechanism is `--only <name>` for
single-entry providers (kubernetes, github), `--only <group>_all` for
group-shaped ones (`azure_all`/`google_all`/`datadog_all`/`aws_data_all`) —
found in each SDK repo's own `hash-watch.yml`, not reinvented. AWS's own
`hash-watch.yml` had a real, separate gap (regenerated only `--only aws`,
the CFN half, never referencing `aws_data_all` at all — its data-source half
had never been in any automated regeneration cycle) — fixed, PR open:
`ubx-sdk-aws#18` adds a `smithy-merged-spec-sha256` drift check for the 429
Smithy members alongside the existing CFN-zip hash, and switches
regeneration to `--only aws_data_all` alone (confirmed live: `"aws"` is
itself a declared member of that group, so one invocation produces both the
CFN resource half — 1,715 real `ResourceBinding` files — and the full
Smithy data-source half — 4,884 real `DataSourceBinding` files — together).
Checked the other five for the same gap: kubernetes_all/github_all are
single-member groups already matching their bare-name usage; azure_all/
datadog_all/google_all were already correctly used. AWS was the sole gap.

**A second, deeper blocker found live while dispatching real regeneration**:
none of the six could actually regenerate successfully, even after the
AWS fix, because `ubx-sdk-go`'s own latest published tag (`v0.1.2`, cut
2026-08-10) predates `DataSourceBinding` — it only existed on that repo's
`main` (merged via its own PR #3, UBI-178) and had never been tagged or
released. Confirmed live: dispatching `hash-watch.yml` on `ubx-sdk-github`
and `ubx-sdk-datadog` both correctly detected drift and regenerated real
`DataSourceBinding`-carrying code, then failed their own `go build` sanity
check with `undefined: ubx.DataSourceBinding` (420 occurrences in datadog
alone) — every one of the six repos' `go.mod` pinned `ubx-sdk-go v0.0.0`,
older still. Founder decision (asked live, confirmed): cut a real release.
**`ubx-sdk-go` v0.2.0 tagged and pushed from main** (10 commits ahead of
v0.1.2 — DataSourceBinding + CrossStack/UBI-134 + three state-file
commits), verified against the real Go module proxy (`proxy.golang.org`),
not just GitHub's own tag listing — resolvable, and its `runtime.go`
carries `DataSourceBinding` at that tag. `ubx-sdk-go` has no CI/publish
workflow of its own; this was a direct, manual tag push under Roozbeh's own
git identity, matching how v0.1.1/v0.1.2 were themselves cut.

Six PRs open now, all real, verified via `gh api`, none merged, never
self-merged:

| Repo | PR | Content |
|---|---|---|
| ubx-sdk-kubernetes | #14 | go.mod bump to ubx-sdk-go v0.2.0 |
| ubx-sdk-github | #14 | go.mod bump to ubx-sdk-go v0.2.0 |
| ubx-sdk-datadog | #13 | go.mod bump to ubx-sdk-go v0.2.0 |
| ubx-sdk-azure | #17 | go.mod bump to ubx-sdk-go v0.2.0 |
| ubx-sdk-google | #20 | go.mod bump to ubx-sdk-go v0.2.0 (also added a missing `sdk/go/go.sum` — none was committed before) |
| ubx-sdk-aws | #18 | hash-watch.yml Smithy coverage + go.mod bump (also added a missing `sdk/go/go.sum`) |

`go build ./...` verified clean locally for all six against the v0.2.0 bump
before pushing. Next, once the founder merges these: re-dispatch each
repo's `hash-watch.yml` (`workflow_dispatch`, already proven live this
session to correctly detect drift and regenerate for github/datadog/azure/
google; kubernetes found no live spec drift on its own first dispatch —
its regeneration will need a forced/manual run once the go.mod bump is in,
since the drift gate is content-based and won't itself notice a
dependency-only change), verify the sanity checks pass for real this time,
confirm each repo's `publish.yml` (manual `workflow_dispatch`) actually
publishes to npm/PyPI/the Go proxy, then re-measure UBI-197's wire match
rates against what's genuinely published. See UBI-196 for the full report.

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

**`ubx-sdk-go`** (shared runtime, not itself provider-specific): latest real
tag `v0.2.0` (2026-08-27, this session — carries `DataSourceBinding`/
UBI-178 and `CrossStack`/UBI-134), verified against the real Go module
proxy. No CI/publish workflow of its own — tags are cut manually.

**SDK repos** (`ubx-sdk-<provider>`, three languages per repo): latest Go
module tag per repo — **none of these six have picked up `ubx-sdk-go`
v0.2.0 yet**, all still pinned at `v0.0.0` pending the six open PRs above —

| Repo | Latest Go tag |
|---|---|
| kubernetes | sdk/go/v1.0.0 |
| datadog | sdk/go/v1.1.0 |
| azure | sdk/go/v1.0.0 |
| google | sdk/go/v1.1.0 |
| github | sdk/go/v1.1.0 |
| aws | sdk/go/v2.0.0 (module path itself ends `/v2`, real semantic-import-versioning requirement — the one provider where this matters) |

None of these six carry `DataSourceBinding` yet (confirmed live via GitHub
code search across all six, zero hits, 2026-08-27) — this is what UBI-196's
still-open regeneration work closes.

PyPI (`pypi.org`) and JSR (`jsr.io`) versions are NOT verified here — check
directly before trusting parity with the Go tag above.

**Open PRs across the org**: none as of the last check this session.
