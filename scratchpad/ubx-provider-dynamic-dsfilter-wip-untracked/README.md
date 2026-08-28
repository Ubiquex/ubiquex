# Preserved WIP: UBI-181 five-rule data-source filter

Recovered 2026-08-28 from a stale, dirty local checkout of
`ubx-provider-dynamic` at `~/Ubiquex/ubx-provider-dynamic`
(HEAD `66671b7`, dated 2026-08-26 13:03, 33 commits behind
`origin/main` at recovery time). Never committed anywhere real.

## What it is

A new `internal/dsfilter` package implementing the five real exclusion
rules for stage-one data-source candidates that UBI-186 (Done, merged
as `ubx-provider-dynamic#13`) already cites filtered counts for
(azure 259, kubernetes 1, github 73, datadog 64, google 472) but whose
actual enforcement code was never committed anywhere -- confirmed by a
direct grep sweep of `ubiquex`, `ubiquex-docs`, and a fresh clone of
`ubx-provider-dynamic` at recovery time.

The five rules: watch/streaming paths, operation-status shapes,
execution/event records, computed non-stored values, high-volume
reference duplication (location/region/zone).

Wired into all three schema-source discovery paths in the diff:
`internal/discoverydoc/datasource.go`, `internal/resourcemap/datasource.go`
(plus a real collection-envelope unwrapping helper,
`collectionItemRefName`), and `internal/smithy/datasource.go`
(`internal/smithy/builddatasource.go` updated to thread the new notes
return value through).

## Files

- `ubx-provider-dynamic-dsfilter-wip.diff` (one level up): the diff
  against modified tracked files (`internal/discoverydoc/datasource.go`,
  `internal/resourcemap/datasource.go`, `internal/smithy/builddatasource.go`,
  `internal/smithy/datasource.go`, `internal/smithy/datasource_test.go`).
- `dsfilter/dsfilter.go`, `dsfilter/dsfilter_test.go`: the new package,
  untracked in the source checkout.
- `discoverydoc_datasource_test.go`, `resourcemap_datasource_test.go`:
  untracked new test files for the two other call sites.

## Status when recovered

Not applied against current `origin/main` -- the source checkout was
33 commits stale, so this needs a real rebase, not a straight apply,
before it can land. Not tested against current code. Real, substantial,
apparently complete work (has its own real unit tests, including one
proving live wiring against a real Smithy model), not a fragment or
false start -- but unverified against current `main` until someone
does that rebase.
