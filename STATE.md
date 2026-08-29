# STATE.md — current state

> Rewritten, not appended, as the LAST act of every session. Holds only what's
> current — in flight, blocked, and what a fresh session needs before touching
> anything. History moves to `HISTORY.md` (narrative archive, consulted only when
> a session needs to know why a decision was made — not read on every open).

## In flight

**UBI-204 closed: dump-ir memory fix landed, all six providers measured
for real post-fix.** Root cause (found via a real, throwaway
`runtime.ReadMemStats` instrument, deleted after use): NOT translation,
NOT merging (Azure's single `[dynamic_providers.azure]` entry never
goes through `generateDynamicProviderGroup` -- the 302-member fetch/
bundle happens inside the separate `ubx-provider-dynamic` subprocess),
NOT the 2718 per-type file writes. One line: the final
`json.MarshalIndent(combined, ...)` serializing the whole combined
`schema.json` in one shot. Fixed: `cli/sdk.go` now uses compact
`json.Marshal` for that one combined write (the per-type `<wire>.json`
files stay indented, unchanged -- never showed a problem). Full `cli`/
`sdk/codegen` test suite passes. Committed and pushed directly to
`ubiquex` main, verified via the real GitHub API: `4171a5d`.

**Real peak RSS, all six providers, real rebuilt binary, `/usr/bin/time
-l`, sequential runs**: azure 1,507,518 fields **3.09GB (was 10.17GB)**,
aws 381,093 fields 680MB, google 144,668 fields 522MB, datadog 18,656
fields 438MB, kubernetes 33,857 fields 440MB, github 13,931 fields
435MB. `ubuntu-latest` (what both `ubiquex-docs` CI workflows actually
use) is 4-core/16GB -- 3.09GB fits with real margin. No streaming-
rewrite follow-up ticket filed; 3.09GB isn't "still too high" per the
founder's own explicit conditional. Full per-stage breakdown and both
runs' numbers in UBI-204's own Linear comments.

**Azure exclusion lifted from both `ubiquex-docs` CI workflows and
verified by real dispatch, not assumed.** `golden-page-gate.yml`/
`coverage-watch.yml` both re-include azure in their per-provider loops,
committed and pushed directly to `ubiquex-docs` main, verified via the
real GitHub API: `c444718`. Real dispatched runs (not just "should
work"): `coverage-watch.yml` run 33242929140 -- azure's dump-ir and
coverage-check steps both completed for real, azure's own real content
came through (1106 resources, 1612 data sources, 2054 real coverage
gaps found and reported to the standing tracking issue, the mechanism
working as intended now that it can see azure). `golden-page-gate.yml`
run 33242899981 -- azure's dump-ir, fresh go/py/ts binding generation,
AND golden-page comparison all completed for real:
`azure/azure_dedicated_host: IDENTICAL to committed golden, static
checks: clean`. Both jobs still show overall "failure," for reasons
unrelated to azure and pre-existing before this change: coverage-watch
by design (real gaps found -> exit 1 -> tracking issue), golden-page-
gate on a real, separate `datadog/datadog_monitor` golden-page
text-wrap drift (13 diff lines, confirmed already present in the last
push-triggered run before azure was touched at all) -- flagged, not
fixed, out of this ticket's scope.

**UBI-198 closed, re-verified independently against current `main`, not
just re-reading the prior comment.** The code-fix half was already
confirmed unnecessary in this ticket's own prior comment (rigorous,
empirical proof: a candidate's own component is by construction always
a real top-level response at the moment `DiscoverDataSources` mints it,
so a reachability check could never fire -- checked against real,
current Datadog/GitHub/Azure specs, 0 overlap with any of the 380
historical held-back wires in any of the three). The cleanup half
(`df5d9b424`, "remove 380 data-source pages with no reproducible
binding") is confirmed on current `main` (`git merge-base
--is-ancestor`), three of the deleted files spot-checked genuinely gone
from disk, `mint validate` clean. 229 datadog + 20 github + 131 azure,
380 total, matching the ticket's own confirmed count.

**UBI-203 closed: resource/data-source WireType collision in `ubiquex-docs`'
`extract_idents.py` fixed and audited, real scope much larger than the
ticket's own "confirmed: Datadog, Kubernetes" framing.** `scan_go`/
`scan_py`/`scan_ts` keyed their output dict by wire alone, no
resource-vs-data-source disambiguation, and `glob.glob()`'s undefined
order meant whichever file won was a function of filesystem/OS
directory-listing order, not the input -- exactly why the ticket's own
local-vs-CI runs disagreed. Fixed: a file is now skipped outright
unless its binding-kind regex actually matches (a `DataSourceBinding`
file can no longer register a null binding/config under a resource's
wire), `glob.glob()` output sorted for determinism, and a genuine
same-kind collision now refuses loudly instead of picking one silently.
Same defense ported to `scan_go_data` in `gen_all_data_source_pages.py`.

**Real audit, all six providers, real freshly-pulled `ubx-sdk-<provider>`
repos**: 270 real collisions (azure 138, aws 49, kubernetes 36, github
34, google 8, datadog 5), not 2. **Currently-wrong pages: zero** --
checked all 270 against the live committed `.mdx` page, every one
(including `kubernetes_apps_replica_set`/`datadog_monitor`) currently
resolves to correct content; whatever built what's committed today
happened to land on the resource file every time. That was luck, the
fix removes the luck requirement. Two azure resources
(`azure_network_virtualnetwork_network_interface`/`..._public_ipaddress`)
are missing a page entirely, same plausible root cause, different
symptom class, not fixed here (needs the same descriptions/intros/
categories/nav rigor as a real onboarding batch). Committed and pushed
directly to `ubiquex-docs` main, verified via the real GitHub API:
`a91654045`. Full writeup in UBI-203's own Linear comments.

**UBI-181: closed, all the way through docs.** Final count held at **42**
(azure 16, github 11, google 14, datadog 1) from generation through
publish through docs. All four schema-snapshot repos published at real
`v1.0.1` releases (`ubx-schema-azure`/`-github`/`-google`/`-datadog`, PRs
#7/#7/#7/#9 all merged), `sdk/providers/.ubx/config` pins bumped to
`1.0.1` and resolving cleanly against the real releases (zero-network on
cache hit, no mirror). SDK bindings regenerated and republished for all
four providers x three languages (`bindings_status=local_only` -- no
`ubx-sdk-<provider>` repo exists for any of these families yet, confirmed
via `gh repo view`, not assumed).

42 real `resource-reference` pages generated in `ubiquex-docs`
(committed and pushed directly to main, `2d07166e2`, verified via the
real GitHub API), depth-zero descriptions/intros/categories.json written
for all 42, docs.json nav updated, `mint validate` clean. Full write-up
in UBI-181's own Linear comments, including two process gaps found and
worked around, not fixed: (1) a GROUP dynamic provider's `Discover()`
"member" attribution is not a real `--only` target, only the bare
`[dynamic_providers.<name>]` key is; (2) `ubiquex-docs`'
`rebuild_provider_index` still resurrects the per-service landing pages
commit `e667fd502` deliberately removed, and clobbers real resources
whose own local name is `index` (hit `google_firestore_index`,
`datadog_logs_index`, `google_aiplatform_index` again) -- every side
effect reverted before committing, but the tool itself still needs a fix.

**UBI-206 (real path-param PascalCase collision, found generating this
batch): fixed, tested, pushed -- PR #40 on `ubx-provider-dynamic`, still
open, not merged.**

Go build (real, page-level, against the regenerated local SDK) clean
42/42. Python `ast.parse` clean 42/42. TypeScript has no established
type-check bar in this pipeline; a full `deno check` run anyway found
37/42 clean, the other 5 (plus the Python runtime-execution check) hit a
pre-existing, corpus-wide gap in nested-object-typed example field
rendering -- confirmed via an unrelated, already-shipped page
(`gcp/aiplatform/deployment-resource-pool.mdx`) hitting the identical
error, not a regression from this batch. Worth its own ticket against
`gen_provider_docs.py`'s field-literal renderer; not attempted this
session.

**UBI-195 closed: the real 41s cost was never the RPC, it was
`ubiquex`'s own client-side schema conversion.** All three of the
ticket's own named candidates (gRPC/protobuf wire-encoding, TLS/
AutoMTLS overhead, tfprotov6/tf6server library inefficiency) were
wrong -- confirmed directly: this client dials with
`insecure.NewCredentials()` unconditionally (`provider/client.go`),
never `hashicorp/go-plugin`'s own client wrapper at all, so TLS/
AutoMTLS overhead was never structurally possible. A real, live,
throwaway instrument (deleted after use) isolated the raw gRPC
`GetProviderSchema` call at 652ms against Azure's real, pinned
604-member group -- the `schemaFromV6`/`schemaMapFromV6` conversion
that follows it took 40.9s alone. The prior session's own "`Schema()`
RPC call itself took ~41.3s" measurement wrapped both together as one
black box and blamed the RPC for a cost it never paid.

Root cause, found by direct code reading: for every NESTED (non-leaf)
attribute, the recursive walk built a real `cty.Type`, immediately
marshaled it to JSON (`attributeTypeJSONFromV6`), and the caller
(`nestedObjectCtyTypeFromV6`) immediately unmarshaled it right back
into a `cty.Type` -- one full, wasted round trip at every one of a
schema's own nesting levels (39,714 total recursive attribute nodes,
max depth 15, in Azure's real response alone).

Fixed (`ubiquex` `f0af587`, direct push): new `attributeCtyTypeFromV6`
returns `cty.Type` directly and is what the recursive path now calls,
no JSON at intermediate levels; `attributeTypeJSONFromV6` stays the
one real public boundary that marshals to `json.RawMessage`, exactly
once, where `Attribute.Type` genuinely needs it.

**Verified by measuring, not assumed**, real before/after against the
real, live-cached Azure pinned snapshot: `Schema()` 40.7s -> 15.6s
(61.6% reduction), `Launch`+`Schema()` total 51.9s -> 27.2s (47.6%
reduction). Output confirmed byte-for-byte identical -- MD5 match
across the full ~10M-line JSON-encoded schema (1,090 resources, 2,177
data sources) -- a pure performance change, zero behavior change.

**The 120s handshake timeout override stays.** It bounds `Launch`, not
`Schema()` -- `Launch` alone measured 11.1s before this fix and 11.6s
after (unaffected by it, real, separate server-side schema-building
cost, `ubx-provider-dynamic`'s own `LoadSplit`+`openapi.Parse`+
`Build`/`MergeOpenAPIGroup`), already over the original 10s default on
its own, either way.

**UBI-201's real fix regenerated and published, on the founder's own
explicit override of never-self-merge.** Real, full regeneration
against all three affected providers found real, unrelated upstream
drift beyond the fix itself (a new `lambda_microvms` AWS service, real
service-directory renames, field-level changes) -- checked directly
before trusting a "small diff," and it wasn't one. Applied the fix
surgically instead: content confirmed byte-identical between the old
and newly-escaped filename (diff, zero output) for all 5 known files
before touching anything, so each PR is a pure `git mv`, git itself
reporting 0 insertions/0 deletions. `ubx-sdk-aws#24` (3 files),
`ubx-sdk-azure#23` (1 file), `ubx-sdk-datadog#19` (1 file) -- real
upstream drift named, not silently folded in or silently discarded.

Verified locally before opening each PR, not assumed: `go build ./...`
and `go vet ./...` both clean, `go list -f '{{.GoFiles}}'` confirms
each renamed file is now included in its package (silently excluded
before), and every previously-undefined symbol
(`LocationFsxWindows`, `MaintenanceWindows`,
`ApplicationSignalsServiceLevelObjectiveExclusionWindows`,
`SqlpoolMaintenanceWindows`, `SitterWasm`) confirmed real and
resolvable via `go doc` on this real, non-Windows machine.

All three PRs merged (confirmed via `gh pr view` showing `MERGED` with
real merge commits) and each repo's own `publish.yml` dispatched for
real -- npm + PyPI + a real Go module tag, each workflow's own
built-in registry-agreement check passing. **Real, live, independently
re-verified versions, not just trusted from the workflow's own exit
status**: `ubx-sdk-aws` v2.1.1, `ubx-sdk-azure` v1.1.1,
`ubx-sdk-datadog` v1.2.1, all three PATCH bumps (real rename, zero new
files, matching the version-bump logic's own real rule). A first
direct PyPI query showed the prior version for all three; a real,
known propagation lag `publish.yml`'s own comments already document,
resolved on a second query 15 seconds later, all three registries
agreeing with npm and the Go tags.

**UBI-201 closed: generated Go filenames escaped against GOOS/GOARCH
build-constraint collisions.** `hasReservedOSArchSuffix`
(`sdk/codegen/templates/go/go.go`) mirrors `go/build.Context.
goodOSArchFile`'s own real matching rule (a generated filename ending
in `_<GOOS>`, `_<GOARCH>`, or `_<GOOS>_<GOARCH>` is silently excluded
by Go's own toolchain everywhere except that platform), checked against
the installed Go toolchain's own `internal/syslist/syslist.go` rather
than a hand-derived list -- confirmed that source's real GOOS set
includes `hurd`/`nacl`/`zos` the ticket's own list missed, and its
GOARCH set (never checked before this ticket) is real and complete.
Scoped to the filename only, the identical trailing-underscore escape
UBI-151's own `_test.go` fix already established -- the exported Go
identifier is unaffected. `ubiquex` `9a2bdd0`.

**Real scope, verified rigorously, not trusted from a first pass.** An
initial synthetic scan (calling `ir.ServiceAndLocalNameForType` against
a bare `ResourceType{WireType: ...}` with no other context) found
6 hits across 4 providers and was reported in that commit's own
message -- checked further before trusting it, and it was wrong. It
doesn't reliably reproduce the real pipeline's own service/local split
for every provider (confirmed live: the real, already-published
`ubx-sdk-google` package generates a bare `chromemanagement/android.go`,
no underscore prefix at all, which Go's own real rule explicitly
exempts -- "a file called linux.go... is not tagged", Go 1.4's own
documented exception -- the synthetic scan's isolated call had
computed a different, wrong local name that looked constrained but
wasn't real). Re-verified against the real, full pipeline instead: a
pre-fix worktree built (`dd93fa0`, before this fix), a real
`ubx sdk gen --lang go --out` run for all six providers, the real
generated tree walked directly for actually-constrained filenames,
then the identical run repeated post-fix and diffed file-for-file.
**Real, ground-truth result: exactly 5 files rename, across 3
providers, aws 3, azure 1, datadog 1, github 0, google 0, kubernetes 0
-- zero constrained files remain anywhere in the full six-provider
corpus after the fix, and the diff shows those 5 renames are the ONLY
files that changed anywhere in the run.** The commit message for
`9a2bdd0` itself still states the earlier, wrong 6/4 figure (not
rewritten -- this project never force-pushes a published commit); this
entry is the corrected, real number. Not yet regenerated/republished
against any of the three affected SDK repos (`ubx-sdk-aws@2.1.0` is the
one already confirmed live-broken; `ubx-sdk-azure@1.1.0`/
`ubx-sdk-datadog@1.2.0` confirmed via the GitHub API to already carry
the same real, live bug too) -- codegen is fixed, the publish rollout
is a separate, real follow-up not done this pass.

**UBI-200 closed: schema staleness detection built and verified in real
CI.** Design question (which of three named options) reported and
confirmed before building: option 1, `ubiquex-docs` queries each real
`ubx-schema-<name>` repo's own current GitHub Release directly, never
`ubiquex`'s own `sdk/providers/.ubx/config` -- that config can itself
lag a schema repo's real latest release, which would make it report
false-clean on exactly the case this exists to catch, and would only
relocate the staleness problem one repo up rather than closing it.
Before building, confirmed directly (not assumed) which of the
ticket's own two possible causes explained zero `PROVENANCE.json`
files being committed anywhere: the write path (`write_provenance_record`)
is real and reachable, both real drivers call it unconditionally
whenever a batch writes at least one page; the last real commit to
either driver is the UBI-199 provenance fix itself, and the two most
recent `resource-reference/aws` commits are relocations, not
regenerations -- no full batch has run through either driver's `main()`
since the fix landed, the mechanism was never broken.

`provenance_check.py` gained `check_staleness` (`fetch_latest`
injectable, so its own classification logic is hermetically testable
without mocking the real network boundary, matching this file's own
"no mocks" convention just moved to a plain function parameter) and
`real_latest_schema_release`. New `staleness_check.py` reads real,
committed `resource-reference/<provider>/PROVENANCE.json` files with
four distinct exit codes, never collapsed into a pass/fail binary: `0`
genuinely clean, `1` real staleness found, `2` only inconclusive
(every live query failed), `3` zero real records found -- explicitly
NOT the same as clean, the exact failure class ("reports clean because
it found nothing") this session already hit more than once. New
`schema-staleness-watch.yml` (weekly, warns via one real tracking
issue, never fails the job, this check is inherently non-hermetic and
a permanently-red build the moment it lands helps nobody) confirmed in
real dispatched CI (`33206151029`): hit the real, current exit-3 state,
opened a real GitHub issue (`ubiquex-docs#49`) with the explicit
"NOTHING WAS CHECKED, this is not the same as clean" message, job
stayed green. `ubiquex-docs` `29a028e91`.

**`docs-structure-gate.yml` built and verified in real CI** (`ubiquex-docs`
`0c04d9131`): wires `mint validate` and `mint broken-links` into CI on
every push to `main`, the identical gap the golden-page CI gate closed
for content drift. `mint validate` fails the job. `mint broken-links`
warns instead, opening/updating one tracking issue rather than failing
the build -- a real fresh full-corpus run found real, pre-existing
broken links, ~70 file locations, all in GCP resource-reference pages
generated before this session, none touching anything this session
edited. Confirmed via a real dispatched CI run (33202456477): validate
passed, broken-links ran and opened the tracking issue, job stayed
green. UBI-175 remained closed; this was UBI-173's own recommendation
converted to a real mechanism.

**UBI-192 (consistent READMEs) built across all 19 repos.** Real
survey before writing anything, not assumed: 17 of 19 already had
substantial README content (only `ubiquex-docs` and `ubiquex-internals`
had none), so the ticket's own "write eleven from scratch" framing was
wrong -- the real gap was consistency and staleness-proofing, not
absence. Scope narrowed accordingly: `scripts/readme-gen/gen_readme_blocks.py`
(new, `ubiquex` `31da904`) generates the volatile parts (real
resource/data-source counts from a real `ubx sdk gen --dump-ir` run,
real published versions queried live per repo via GitHub tags/releases,
npm, or PyPI, and the standing cross-repo links block) spliced between
`<!-- README-GEN:BEGIN/END -->` markers, so a README's own volatile
facts are never hand-typed and never go stale silently. Real, fresh
counts confirmed against known-good figures from earlier this session
(5 of 6 providers matched exactly; Azure's own count, previously
unmeasured, is now real: 1,090 resources, 2,177 data sources) -- one
real bug caught and fixed in the counting logic itself before trusting
it: a naive `<provider>_data_` prefix check misclassified 39 real AWS
resources (`aws_data_brew_dataset`, genuine "AWS Data..."-branded
services) as data sources.

Full six-part structure applied to the four repos that needed it:
`ubiquex` (was 838 bytes, `ubiquex` `7c2a1b5`), `ubiquex-docs` (no
README existed, now includes the artifact model explanation the ticket
calls out specifically, `ubiquex-docs` `1f5f73e9d`), `ubiquex-internals`
(no README existed, `ubiquex-internals` `203ad90`), all three direct
push and verified via the GitHub API. `ubx-sdk-go` (was 391 bytes, also
had a misleading "Generated by ubx" line for a repo that's actually
hand-written) got the same treatment via a real PR, `ubx-sdk-go#8`.

The other 15 repos already had good, substantial content -- edited
additively, never rewritten, matching the founder's own explicit
instruction: a generated block plus the specific missing structural
piece each one was actually missing (an Install section, a
depends-on/depended-by section, a directory layout). `ubx-sdk-{aws,
azure,google,kubernetes,github,datadog}#{23,22,25,20,19,18}`,
`ubx-schema-{aws,azure,google,kubernetes,github,datadog}#{6,6,6,12,6,8}`,
`ubx-provider-dynamic#33`, `ubx-sdk-typescript#11`,
`ubx-sdk-python#9` -- 16 real PRs total (`ubx-sdk-go#8` plus these 15).

**All 16 merged**, on the founder's own explicit confirmation (the
bare "merge all PRs" instruction conflicts with this project's own
standing never-self-merge rule -- confirmed via `AskUserQuestion`
before acting, matching the identical CLAUDE.md rule 10 rollout
precedent earlier this session, not assumed authorized). Every merge
verified two ways, not trusted from the merge command's own exit
status alone: `gh pr view` showing `MERGED` with a real merge commit
SHA for all 16, and real file content read back from `main` after,
spot-checked on `ubx-sdk-go`, `ubx-schema-kubernetes`, and
`ubx-provider-dynamic`. UBI-192 closed in Linear with all 16 PR links
attached.

**UBI-175 fully closed.** Its last two open items both resolved without
building anything speculative. Provider tier labels: checked
`sdk/providers/.ubx/config` directly before deciding -- exactly six
`[dynamic_providers.<name>]` entries, all six official, zero
verified/community entries, the one historical verified-tier candidate
(the HashiCorp-sourced AWS corpus) was built then deliberately removed.
Building the label now would mean real schema/generator/template work
for a value that reads identically on every page in the corpus, zero
discriminating power. Split off to UBI-205 (no priority, revisit once a
real second-tier provider exists) rather than held open on an Urgent
ticket for something with no current subject. Audit-vs-coverage-check
question re-confirmed with fresh checks, same finding as before:
substantively superseded, two real exceptions still open and un-closed
by anything built this session -- page-load/structural checking
(`mint validate`/`mint broken-links` both exist, both wired into zero
CI workflows, confirmed directly against `coverage-watch.yml`/
`golden-page-gate.yml`, the only two that exist) and intro-quality
judgment (not mechanically checkable, `check_intro`/`coverage_check.py`
both only check structure/existence, never prose quality).

**Stale/dirty `ubx-provider-dynamic` local checkout resolved.** Found
33 commits behind `origin/main`, dirty, with uncommitted changes to
`internal/discoverydoc/datasource.go`, `internal/resourcemap/datasource.go`,
`internal/smithy/builddatasource.go`, `internal/smithy/datasource.go`,
`internal/smithy/datasource_test.go`, plus untracked
`internal/discoverydoc/datasource_test.go`,
`internal/resourcemap/datasource_test.go`, `internal/dsfilter/`. Checked
before touching anything, not assumed abandoned: real, substantial,
tested work implementing UBI-181's own "five-rule" data-source
candidate filter (watch paths, operation-status shapes, execution/event
records, computed values, high-volume location/region/zone
duplication) -- UBI-186 (Done) already cites real filtered counts for
it (259/1/73/64/472) but the actual enforcement code was never
committed anywhere real, confirmed by grepping `ubiquex`, `ubiquex-docs`,
and a fresh clone of `ubx-provider-dynamic`, zero hits. NOT UBI-198 WIP
(that's confirmed dead/superseded, unrelated). Preserved rather than
discarded, per founder's own choice: the diff and untracked files are
saved at `scratchpad/ubx-provider-dynamic-dsfilter-wip.diff` and
`scratchpad/ubx-provider-dynamic-dsfilter-wip-untracked/` (with its own
README explaining provenance and status) in this repo, before the
checkout was reset clean to `origin/main` (`25a754f`). Needs a real
rebase before it can land, not a straight commit -- not attempted this
session.

**UBI-173 (Blueprints docs) closed, real scope much narrower than
filed.** The ticket's own premise ("zero blueprint pages exist") was
checked against real git history and was already false when filed --
1,112 lines of real, committed, nav-wired blueprint documentation
existed in `ubiquex-docs` (`931f12e0f`, 2026-08-11) days before the
ticket's own 2026-08-15 search date. Real gap, confirmed by grep: UBI-129
(list params/`for_each`/iteration) and UBI-86 (the override mechanism)
both landed after the ticket was filed and were genuinely undocumented
anywhere, in either `ubiquex-docs` or `ubiquex-internals`. Two new
concept pages built (`concepts/blueprint-list-params.mdx`,
`concepts/blueprint-overrides.mdx`), cross-reference touch-ups across 7
existing pages, `docs.json` nav updated -- `ubiquex-docs` `372e0fc75`.
A real accuracy bug found and fixed along the way: `concepts/blueprints.mdx`
showed a fabricated `ubx why` dual-signature example that doesn't match
real tool output -- fixed to show the actual output and state honestly
that only the calling stack's acceptance is signed. `ubiquex-internals`'
own `concepts/blueprints.mdx` carried the identical UBI-129/UBI-86 gap,
also closed (`c43ad111b`). Both pushes verified via the GitHub API
against the real repos. `mint validate`/`broken-links` clean in both,
zero em dashes. UBI-173 closed in Linear, its own description rewritten
to record why the original premise was wrong rather than silently
reduced in scope.

**Three tickets checked against real, current state, two closed, one left
open with an itemized remainder that then shrank to one real item**:
UBI-193 (Azure schema pinning) and UBI-176 (stale SDK bindings) both
fully verified done and closed -- UBI-193 via three confirmed-merged
PRs (`ubx-provider-dynamic` #23/#24/#26), a real `ubx-schema-azure`
v1.0.0 release, and a real `[dynamic_providers.azure]` pin in the
current `sdk/providers/.ubx/config`; UBI-176 via every specifically-named
missing resource confirmed present in the real, currently published
packages, downloaded and inspected directly, plus a real scale check
(AWS now 1,715 files vs. the ticket's own 1,705-live figure).

UBI-175 (docs pipeline spec) stayed open. Two corrections to the
original four-item remainder, checked against real history rather than
left standing: the AWS dual-corpus split is confirmed DEAD, not
pending -- built (`ubiquex-docs` `6096016c7`), then deliberately
removed nine days later (`ed9a04133`, "documented resources with no
SDK bindings behind them"), not something to re-build. The pre-rebuild
audit is confirmed substantively superseded by the coverage check --
missing-artifact detection is now continuous, not a one-time sweep;
what genuinely remains outside it is page-load/structural checking
(cheap fix, `mint validate` already exists, just isn't wired into any
workflow) and intro-quality judgment (not mechanically checkable at
all, same class of problem as CLAUDE.md rule 10's own
architectural-vs-bugfix distinction).

That left two real items; built the first, now fully verified end to
end across four real dispatched CI runs, not just a local dry run.
**Golden-page CI gate**: `.github/workflows/golden-page-gate.yml`
(`ubiquex-docs`, final `b2b9cbfeb`) wires the already-existing
`verify_against_golden.py` (real since 2026-08-22/23, never run by
anything) into a real build gate -- builds `ubx` from source, dumps
schema, generates fresh local bindings per golden candidate, fails the
job on any diff or static-check failure. Real infra problems hit and
fixed along the way, each confirmed via a real dispatched run, not
assumed fixed from reading the YAML: a single bulk `--dump-ir` across
all six providers died (split into one `--only <name>` call per
provider); that still died specifically on Azure -- measured locally
at 12.5GB peak memory (`/usr/bin/time -l`) for Azure alone, filed as
UBI-204, Azure excluded from both this workflow's and
`coverage-watch.yml`'s per-provider loops with a named, visible
comment, not silently dropped; the runner then hit `deno: command not
found` (`gen_provider_docs.py`'s own TypeScript block shells out to a
bare `deno`, only present on this session's own dev machine, not a
standard runner) -- fixed with `denoland/setup-deno@v2`.
`coverage-watch.yml` hit the identical multi-provider and Azure-specific
OOM (confirmed via `gh run list` it had literally never completed a
real run before, despite being treated as "wired into CI" since
UBI-187) and separately a missing `docs-coverage-drift` GitHub label
(the workflow had never reached that step before to create it) --
both fixed the same way, both confirmed via real dispatched runs.

Final confirmed real state of both, run 2026-08-28: **golden-page-gate**
(run `33190488249`) shows the correct, fully-understood five-candidate
picture -- `github_full_repository`'s real new field
(`secret_scanning_validity_checks`, added upstream since the golden
page was committed) reviewed and accepted (`ubiquex-docs` `e03eccc70`,
now IDENTICAL); `gcp` IDENTICAL, unchanged; `aws_launch_template` still
has NO GOLDEN FILE (stale relative to this session's own AWS namespace
fix changing its slug from `template` to `launch-template` -- a new
golden page needs authoring at the new path, not a diff to accept, not
done); `datadog_monitor` and `kubernetes_apps_replica_set` both DIFFER
-- both real, both UBI-203 (see below), not accepted, correctly still
red. **coverage-watch** (run `33189696382`) ran the real coverage
check across the five non-Azure providers, found real, large,
pre-existing content gaps (7,625 total -- missing intros, category
overrides, depth-0 field descriptions, schema/page mismatches, none of
it new content debt introduced this session, just never surfaced
before because the workflow never ran), opened
https://github.com/Ubiquex/ubiquex-docs/issues/47 with the
`docs-coverage-drift` label, and correctly failed the job (exit 1 by
design) -- this is the mechanism working as built, not a bug; the
7,625 gaps are real, un-actioned follow-up work, not touched this
session.

**UBI-203 broadened**, not just Datadog: the same golden-page run
that flagged `datadog_monitor` also flagged `kubernetes_apps_replica_set`
as DIFFERS, with the identical `None`/`None` broken-identifier
signature. Checked directly against a fresh, clean, pushed
`ubx-provider-dynamic` checkout (not the stale local one, see below):
confirmed `ubx-sdk-kubernetes` really does carry two files declaring
the identical `WireType: "kubernetes_apps_replica_set"` --
`kubernetes/apps/replica_set.go` (`ResourceBinding`) and
`kubernetes/data/apps/replica_set.go` (`DataSourceBinding`) -- the
same collision class as `datadog_monitor`, confirmed live, not
inferred. Also explains a real discrepancy: an earlier local (macOS)
dry run of this exact pipeline reported `kubernetes_apps_replica_set`
as IDENTICAL, while real CI (Linux) reported it DIFFERS. Root cause:
Python's `glob.glob()` (used by `extract_idents.py`'s `scan_go`/
`scan_py`/`scan_ts`) has no defined return order -- confirmed directly
from its own docs ("The order of the returned list is undefined") --
so which of two wire-colliding files wins is a function of
filesystem/OS directory-listing order, not the input. Same underlying
bug, different winner on different machines. Ticket retitled and its
description patched to record both confirmed instances and the real
root cause; still not fixed, two real fix locations named (prefix
Datadog/Kubernetes-style data-source wire types at the generator, and/or
key `extract_idents.py`'s own dict by `(wire, is_data_source)` instead
of `wire` alone so a future collision fails loud instead of silently
producing `None`).

**Loose end found, not touched**: the local `~/Ubiquex/ubx-provider-dynamic`
checkout is 33 commits behind `origin/main` (`105a5ba4a`) and dirty --
uncommitted changes to `internal/discoverydoc/datasource.go`,
`internal/resourcemap/datasource.go`, `internal/smithy/builddatasource.go`,
`internal/smithy/datasource.go`, `internal/smithy/datasource_test.go`,
plus untracked `internal/discoverydoc/datasource_test.go`,
`internal/resourcemap/datasource_test.go`, and a new `internal/dsfilter/`
directory. All of it touches data-source identification/type-naming
logic, the same area as the UBI-203 collision above -- worth checking
whether this is abandoned WIP toward a real fix before assuming it's
safe to discard. Not investigated further or acted on this session;
a fresh clone was used instead for all real verification work above so
this stale/dirty state never touched anything committed or published.

One real item remains open on UBI-175: provider tier labels, not
started.

**New standing rule, live in all 19 repos**: CLAUDE.md rule 10
(`ubiquex`) -- an architectural change (a new schema source, a
naming-derivation change, a new mechanism, a change to what the ledger
records) gets its `ubiquex-internals` page written or updated in the
SAME body of work, never a follow-up; a bug fix inside an
already-documented mechanism doesn't qualify. Landed directly in the
three direct-push repos (`ubiquex` `a9f7583`, also added to
`docs/prompts.md`; `ubiquex-docs` `77d06ffa8`; `ubiquex-internals`
`5c66ccb`, phrased as this repo's own real target). Opened as real PRs
against the other 16 -- `ubx-provider-dynamic` #32; six
`ubx-sdk-<provider>` #22/#21/#24/#19/#17/#18 aws/azure/google/
kubernetes/datadog/github; three shared runtimes `ubx-sdk-go` #7/
`ubx-sdk-typescript` #10/`ubx-sdk-python` #8; six `ubx-schema-<provider>`
#5/#5/#5/#11/#7/#5 aws/azure/google/kubernetes/datadog/github -- then,
on the founder's own explicit override of this session's own
never-self-merge default (confirmed via `AskUserQuestion` before acting,
not assumed), merged all 16. Every merge verified two ways: `gh pr view`
showing `MERGED` with a real `mergeCommit`, and the real file content
read back from `main` after (`gh api .../contents/CLAUDE.md`, not
inferred from the merged flag alone) -- spot-checked on
`ubx-provider-dynamic`, `ubx-sdk-aws`, `ubx-sdk-go`,
`ubx-schema-kubernetes`, all four showing the real, correct per-category
rule text live. Rule 10 is now real and enforceable everywhere, not
just the three monorepo-adjacent repos.

**Checked whether the sync mechanism should enforce rule 10, real
finding, not assumed**: it can't, mechanically -- telling an
architectural change apart from a bug fix inside an already-documented
mechanism needs judgment a diff alone doesn't encode, the identical
reason CLAUDE.md rule 5's own "same session" ask has no CI check
either. `sync-drift-watch.yml` (`ubiquex-internals` `b3a0fe4`) stays
what it always was -- a backstop for an already-TRACKED source file
drifting without its page following, nothing about a mechanism whose
file was never registered in the first place. The one real,
low-risk improvement made: cadence tightened from weekly to daily,
since rule 10's own "same body of work" intent is undercut more by a
week-long detection lag than a check with no same-work expectation
behind it would be. The workflow's own header comment now states this
relationship explicitly so it doesn't get silently oversold later.

UBI-191 (developer documentation site) closed this session, all eleven
named sections built across five slices -- see `HISTORY.md`'s own
"UBI-191: DONE -- developer documentation site built end to end" entry.
Short version: new repo `github.com/Ubiquex/ubiquex-internals` (private,
Mintlify), a real multi-repo sync-drift mechanism (`sync-state.json` +
`check_drift.py` + `sync-drift-watch.yml`, now daily) tracking 11 real
source files across `ubiquex` and `ubx-provider-dynamic`, verified with
real dry runs and a real negative test at every slice. Two of the
ticket's six named diagrams not built (end-to-end change flow,
staleness) -- both prose-covered already, named as real, small, optional
follow-up, not silently claimed done.

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
carried forward from memory, as of 2026-08-29.

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

**Open PRs across the org**: one — `ubx-provider-dynamic#40` (UBI-206, real
path-param PascalCase collision fix, tested and pushed, deliberately not
merged per "never self-merge"). The four `ubx-schema-<provider>` PRs from
this same UBI-181 batch (`#7`/`#7`/`#7`/`#9` in azure/github/google/datadog)
all merged, verified via `gh pr list --state all` as of 2026-08-29.
