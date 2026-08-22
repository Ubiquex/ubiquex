# UBI-175: survey of the 190 unconfigured GCP APIs

Reconciles the "1,111/1,159 non-Compute wires covered" claim in commit
`901c4f5` (no supporting research file or STATE.md entry was ever
written alongside it, so it could not be reconstructed directly --
this survey exists so the next discrepancy like it has something to
check against).

## Method

Google's public Discovery Service directory (`GET
https://www.googleapis.com/discovery/v1/apis?preferred=true`) lists
**312 real, preferred APIs**. 128 are configured (`google` bare +
127 `google_<api>` families). **190 are not** (191 raw, minus 1 --
`siteVerification`, a case-sensitivity artifact: the API directory's
own `name` field for that one API is mixed-case, and it is in fact
already configured as `google_siteverification`).

Each of the 190 was surveyed with an exact, line-for-line Python
replication of `internal/discoverydoc.Discover()` (`ubx-provider-dynamic`),
using the same `ToSnakeCase`, `singularize`, and create-method matching
(`"create"`/`"insert"`, exact or prefixed) the real Go function uses --
not a heuristic approximation. Verified against the 128 already-configured
families first: exact match with live `--dump-ir` output, 782 = 782
resources total, including an exact per-family match (Apigee: 45 = 45).

## Results

- 186 of 190 fetched successfully. **5 unreachable**, confirmed on
  retry, not transient: `area120tables`, `integrations`,
  `mybusinessqanda` (HTTP 404 -- retired, still stale-listed in the
  directory index), `datalabeling`, `poly` (HTTP 502, reproducible).
- **98 of 186 have zero real discoverable resources.** Full accounting
  in `unconfigured_survey_results.json` (attached to this file's own
  commit); every one has a real reason, not silently assumed:
  - Most: every resource node found has a `get` but no matching
    `create`/`insert` method -- read-only, correctly excluded by
    `Discover()`'s own real rule (not a bug, not a loss).
  - Some: no `resources` tree in the discovery doc at all -- the API
    is not CRUD-shaped (e.g. pure RPC/reporting APIs).
- **88 of 186 have real, discoverable, CRUD-shaped resources: 309
  total** (308 after removing the `siteVerification` duplicate).

## Scope decision (founder confirmed)

The 87 real APIs split into cloud-infrastructure APIs and
Workspace/consumer/ad-tech APIs -- none of the existing 128 configured
families are Workspace/ad-tech, confirming this project's own
established scope. Founder confirmed: **infrastructure only.**

- **34 infra APIs, 97 real resources** -- configured this session (see
  companion `ubiquex-docs/artifacts/gcp/manifest.json` note and
  `sdk/providers/.ubx/config`'s own new block for the full list and
  real live-verified `--dump-ir` counts, generated with zero failures,
  exact match with the Discover() replication: 97 = 97).
- **52 Workspace/consumer/ad-tech APIs, 194 resources** (includes
  `walletobjects`, reclassified after an initial pass placed it in
  infra by mistake -- it is a consumer passes/tickets product, not
  cloud infrastructure) -- deliberately not configured. Real examples:
  Gmail (10), Drive (6), Calendar (4), Classroom (11), DFA Reporting
  (28), Display Video (20), Tag Manager (13), AdSense (2+2).
- **1 duplicate test variant skipped**: `prod_tt_sasportal` (3
  resources, identical shape to the real `sasportal`, which was
  configured instead -- a `(Testing)`-suffixed API in Google's own
  directory, not a distinct product).

## What "1,111/1,159" almost certainly was, and the honest limit of this reconstruction

The 34-API, 97-resource infra survey above is real, live, and
reproducible -- it is *not* an attempt to hit 1,111. Spot-checking 5 of
the 190 unconfigured APIs earlier this session (before the full
survey) found real, substantial resource counts consistent with a much
larger candidate pool than "128 families" -- the full 190-API survey
confirms this: 309 real resources existed outside config before this
session, most of them (194) in Workspace/ad-tech APIs this project has
never configured a single one of. The commit's own two numbers are
internally consistent (1,111 + 48 auth-gated/blocked = 1,159 exactly),
consistent with a broader research pass that surveyed more APIs than
ultimately got written into `.ubx/config` -- but the exact APIs and
wires that made up that original 1,159 cannot be reconstructed from
this repo; no supporting file was ever committed alongside `901c4f5`
and no STATE.md entry recorded it.

## Real observability gap found and fixed

`internal/discoverydoc.Discover()` and `internal/cloudformation`'s own
equivalent both compute real, structured skip-reason `Note`s (why a
candidate resource was excluded) -- confirmed by reading the source,
not assumed. `ubx sdk gen`'s own CLI never surfaced them anywhere
(confirmed: zero references to `Note` in `cli/sdk.go`, and a live run
with full stderr visibility printed none). This is exactly why the
1,111/687 discrepancy took a full Discover() reimplementation to
resolve instead of a five-minute `--dump-ir --verbose` check -- see the
companion commit adding `--dump-ir`'s own real notes output.
