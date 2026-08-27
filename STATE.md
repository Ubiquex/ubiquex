# STATE.md — current state

> Rewritten, not appended, as the LAST act of every session. Holds only what's
> current — in flight, blocked, and what a fresh session needs before touching
> anything. History moves to `HISTORY.md` (narrative archive, consulted only when
> a session needs to know why a decision was made — not read on every open).

## In flight

**UBI-183: session-handoff files split, real PRs open, none merged yet.**
`ubiquex`'s own `STATE.md` had grown to 1.87MB as one append-only narrative
log — moved wholesale to `HISTORY.md` (zero content lost) and replaced with
this file; `CLAUDE.md` amended (rule 3) to require rewrite-not-append going
forward. A full org audit (`gh repo list --json isArchived`, not the
unfiltered list) found 22 real, non-archived repos, only `ubiquex` itself
already carrying a `CLAUDE.md` — 8 more repos than the ticket's own original
13-repo scope: the three real shared-runtime repos (`ubx-sdk-go`,
`ubx-sdk-typescript`, `ubx-sdk-python` — separate from the ARCHIVED
per-language bindings repos of similar name, e.g. `ubx-sdk-azure-go`) plus
`ubiquex-docs`, `ubiquex-web`, `ubiquex.io`, `ubx-providers-check-demo`,
`ubx-sdk-blueprints`.

Real PRs open in every one of those repos (19 total), none merged yet:
`ubx-provider-dynamic#29`, `ubx-schema-kubernetes#9`, `ubx-schema-datadog#5`,
`ubx-schema-azure#3`, `ubx-schema-google#3`, `ubx-schema-github#3`,
`ubx-schema-aws#3`, `ubx-sdk-kubernetes#11`, `ubx-sdk-datadog#10`,
`ubx-sdk-azure#14`, `ubx-sdk-google#17`, `ubx-sdk-github#11`,
`ubx-sdk-aws#15`, `ubx-sdk-go#4`, `ubx-sdk-typescript#6`,
`ubx-sdk-python#4`, `ubiquex-web#1`, `ubiquex.io#1`,
`ubx-providers-check-demo#4`, `ubx-sdk-blueprints#1`. `ubiquex-docs` got its
`CLAUDE.md` via a direct push to `main`, matching that repo's own confirmed
direct-push convention (no PR needed there). The three shared-runtime repos
got the full `CLAUDE.md`+`STATE.md`+`HISTORY.md` triple, matching the six
SDK/schema repos; the other 5 (docs/web/site/demo/blueprints) got a
right-sized `CLAUDE.md` only — no invented git-workflow rule for repos where
none has been confirmed by any session yet.

## Blocked

Nothing currently blocked. UBI-183's own 19 PRs above are open, awaiting
founder review — not blocking anything else in the meantime.

## Known, deliberately not acted on

**UBI-194's own remaining five providers.** `datadog`/`azure`/`google`/`github`/`aws`
schema repos all resolve their `ubx-provider-dynamic` binary version via the
bootstrap fallback (`schema_format 3 -> 1.0.0`), not a real `min_binary_version` —
confirmed working correctly, logs visibly on every use, no functional harm. The
recorded recommendation (UBI-194 ticket, 2026-08-27) is to let each regenerate
naturally via its own weekly `hash-watch.yml` cron rather than forcing a
metadata-only republish now — Azure/Google/AWS track fast-moving upstream APIs
and are near-certain to bump for real content reasons within a cycle or two
anyway, picking up `min_binary_version` for free. Only Kubernetes has actually
been regenerated past the fallback (`v3.0.1`, carries `min_binary_version: 1.0.1`).

**UBI-195: Azure's own real RPC-layer load-time cost, filed not fixed.** A pinned
`[providers.azure]` resolution takes ~54-56s wall time before its first RPC
response, zero network, root-caused to the `GetProviderSchema` gRPC call itself
(~41s of the total) via three separate live instruments — NOT parsing/
translation/merging (~11s), which was the original, now-corrected hypothesis.
Filed as its own Linear ticket with the full root-cause breakdown. Nobody has
picked this up yet.

## Before touching anything

- Never trust a "published"/"live" claim for a shared runtime or per-provider
  bindings repo from this monorepo's own state alone — verify against the real,
  separate repo/registry directly: a real `git log`/`diff` against the actual
  separate repo, or a real registry query (the Go module proxy, `jsr.io`,
  `pypi.org`), never infer "published" from a commit to the monorepo's own
  copy alone (CLAUDE.md rule 8). This bit the project twice: UBI-131's own Go
  fix was reported "committed and pushed" across multiple session summaries
  when only the monorepo's own copy had changed — the separate, real
  `ubx-sdk-go` repo was never touched, still showing its original scaffold
  commit a full day later, caught only when the founder pushed back and a
  real `git log` was run against the actual separate repo.
- Every `ubx-schema-*`/`ubx-sdk-*`/`ubx-provider-dynamic` PR is opened, never
  self-merged — the founder merges. Direct commits to `ubiquex`'s own `main` are
  the one allowed exception (CLAUDE.md's git rules).
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

**`ubx-provider-dynamic`**: latest release `v1.0.1`, published per platform
(linux/darwin × amd64/arm64) with checksums, acquired via
`provider.AcquireDynamicProviderBinary` — no `UBX_PROVIDER_DYNAMIC_REPO`
checkout required on the normal path (kept only as an explicit dev override).

**SDK repos** (`ubx-sdk-<provider>`, three languages per repo): latest Go module
tag per repo, directly verified via the GitHub API —

| Repo | Latest Go tag |
|---|---|
| kubernetes | sdk/go/v1.0.0 |
| datadog | sdk/go/v1.1.0 |
| azure | sdk/go/v1.0.0 |
| google | sdk/go/v1.1.0 |
| github | sdk/go/v1.1.0 |
| aws | sdk/go/v2.0.0 |

PyPI (`pypi.org`) and JSR (`jsr.io`) versions are NOT verified here — check
directly before trusting parity with the Go tag above (CLAUDE.md rule 8; a
mismatch across the three languages would not be visible from this table alone).

**Open PRs across the org**: none, as of the last check this session (excluding
whatever UBI-183 itself opens while landing the files described above).
