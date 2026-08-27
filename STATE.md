# STATE.md — current state

> Rewritten, not appended, as the LAST act of every session. Holds only what's
> current — in flight, blocked, and what a fresh session needs before touching
> anything. History moves to `HISTORY.md` (narrative archive, consulted only when
> a session needs to know why a decision was made — not read on every open).

## In flight

**UBI-196: docs corpus bindings_status recompute, done for resources and the
structural half of data sources; per-resource data-source accuracy and full
regeneration+republish of the six SDK repos are real, separate, unstarted
work.** See the ticket for the full report — summary:

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
  confirmed live). Does NOT fix per-page wire/binding accuracy against a
  fresh local generation — spot-checked at 52% (kubernetes) / 85% (github) /
  4% (datadog) match rates, real and unresolved.
- 5 GitHub resources held back (`github_repository_ruleset`,
  `github_network_configuration`, `github_get_budget`,
  `github_actions_hosted_runner`, `github_custom_property`) — a real "Org"
  field shown in their example doesn't exist in the real published Config
  struct. Genuine content drift, not a naming issue this pass could safely
  correct.

All three landed as direct commits to `ubiquex-docs` `main` (that repo's own
confirmed direct-push convention), each independently verified via `gh api`
after pushing, not trusted from local git alone.

**Not done, real and separate**: full regeneration and republish of the six
`ubx-sdk-<provider>` repos so their real packages carry `DataSourceBinding`
and every data-source page can go `local_only -> published` for real. See the
ticket for the scope report. Kubernetes/GitHub/Datadog are single-entry and
fast to regenerate (`ubx sdk gen --only <name> --lang <lang>`, seconds each,
confirmed live). Azure/Google/AWS are GROUP-shaped — a bare `--only
<provider>` only covers ONE of their many declared entries (confirmed live:
`--only azure` produced 63 resources against a real 604-member group) — full
regeneration needs either enumerating every group member in `--only` or
whatever mechanism originally built these six repos' own initial publish,
which this session did not need to identify and has not verified.

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

**SDK repos** (`ubx-sdk-<provider>`, three languages per repo): latest Go
module tag per repo —

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
