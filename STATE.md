# STATE.md — living project state

> Updated as the last act of every working session. This file is the handoff.

## Current phase

**UBI-29 is done (this session): Fleet visibility for shipped creates —
closes the one gap UBI-27 left open.** A shipped `change` proposal's
created resources are now fully first-class: `ubx status`, `ubx why
<address>`, and a future `ubx scan` all discover them, exactly like an
adopted resource.

**Root cause, precisely**: `core.Ledger.Fleet`/`ProposalsForAddress`/
`LastObservedHash`/`LastObservationTime` all discover a resource
exclusively via a `resolution.inputs[].resource` entry — and a create
never gets one for its own address (it was never *observed*, and its real
identity isn't known until `ship` applies it, well after the proposal's
content hash is sealed). A DEEPER, related gap found while designing the
fix, not just the Fleet-visibility one UBI-27 named: `core.Ledger.FoldState`
(what `ubx scan`/`status --drift` diff live reality against, and what
`core/resolver` itself checks before allowing a `create`/`modify`) only
ever recognized `Delta.Creates`' adoption-shaped `{state: ...}` key —
never `Delta.Creates`' change-proposal shape (`{config: ...}`) at all. A
shipped create was therefore invisible to FoldState too, not just Fleet —
meaning a future `modify` targeting it would have wrongly resolved as if
the address didn't exist, and `RunScan` would have classified it `ScanNew`
forever, never `ScanDrifted`.

**The fix, one mechanism, five call sites**: `core.ResourceApply` gains an
additive `Lookup json.RawMessage` field (docs/schema.md's own amendment),
recorded explicitly by `core/executor`'s `shipCreate` the moment a
create's `ApplyResourceChange` call succeeds — `{"id": <applied id>}`,
via a new `core.DeriveLookupFromResult` — the same "record explicitly,
never derive at need-time" lesson Slice 3 already established for
`resolution.inputs[].lookup`. A new private `core.Ledger.shippedCreateFold`
(mirroring `core/executor`'s own `foldResourceHistory`, duplicated rather
than imported since `core` can't import `core/executor`) folds a specific
`(proposal, address)` pair's own apply records down to: real applied
result, lookup (falling back to `DeriveLookupFromResult` for an apply
record that predates this amendment — row 2's own graceful-degrade
requirement), and the applied-transition timestamp — found if and only if
that resource's own MOST RECENT transition is `applied`, independent of
whether the *enclosing* multi-resource `ApplyRecord` itself has been
sealed. This gating decision matters and is deliberate: UBI-27's own kill
test already proved one resource can be genuinely, durably `applied` while
a sibling in the same unsealed attempt is still pending — gating on
"sealed" would have hidden a resource that, in the real world, already
exists. `Fleet`, `FoldState`, `ProposalsForAddress`, `LastObservedHash`,
and `LastObservationTime` (`core/fleet.go`, `core/state.go`) all now fold
this in alongside their existing `resolution.inputs`-based discovery —
`FoldState` seeds a shipped create's state from its real applied
`provider_result`, never from the pre-ship `config` (which may still carry
stale `$computed` markers). `cli/why.go`'s `renderProposalCompact` also
gained apply-history rendering for the address-chain view (previously
only the single-proposal-ID view ever showed it, for any kind) — a small,
adjacent gap found while making the create-genesis chain actually visible
end to end.

**`cli/status.go`/`cli/scan.go`/`core/scan.go` needed ZERO changes** —
confirmed, not assumed: both already consume `Fleet`/`FoldState` as their
only path to "what does the ledger know," so fixing those two functions
made the whole rest of the read path correct for free. `GenerateProposal`/
`GenerateRevertProposal` already produce the right kind (`drift_adopt`/
`drift_revert`) for anything `RunScan` classifies `ScanDrifted`, regardless
of a resource's own genesis — no new proposal-generation logic needed
either.

**Design landed in docs first**: docs/schema.md gained "Amendment:
apply-record lookup key + Fleet discovery" (the new field, the
per-resource-not-per-attempt gating rationale, the graceful-fallback
design, no `schema_version` bump); docs/executor.md gained its own
amendment section (`shipCreate`'s one new behavior, the sealed-vs-applied
distinction restated in executor terms); docs/resolver.md's and
docs/executor.md's own stale "Out of scope" bullets from UBI-27 struck
through and marked fixed; docs/plan.md gained a "Fleet visibility for
shipped creates (UBI-29)" wedge subsection and its Deferred-list entry
resolved.

**Hermetic adversarial coverage** (`core/ubi29_test.go`, new — `core`
package tests can't import `core/executor`, so a `change` proposal +
its own apply record is hand-built directly via `Accept`/`BeginApply`/
`SaveApplyProgress`/`SealApply`, the same convention `core/executor/ship_test.go`'s
own crash-simulation tests already use): `TestUBI29_Fleet_ShippedCreate_Discoverable`,
`TestUBI29_FoldState_ShippedCreate_SeedsFromApplyRecord`,
`TestUBI29_ProposalsForAddress_ShippedCreate_IncludesCreateProposal`,
`TestUBI29_LastObservedHashAndTime_ShippedCreate` (the happy paths);
`TestUBI29_CreatedThenDrifted_FullLifecycle` (row 1: a shipped-create
resource participates in `RunScan`/`GenerateProposal`/`Accept` exactly
like an adopted one, using `fakeProvider`'s own mutable `.state` field to
simulate out-of-band drift); `TestUBI29_OldApplyRecord_NoLookupField_DerivesGracefully`
(row 2: an apply record built with `recordLookup=false`, proving the
`DeriveLookupFromResult` fallback); `TestUBI29_KillMidCreate_UnsealedRecord_NeverDiscoverable`
(row 3: a resource whose only transitions are `pending`/`in_flight`, left
unsealed, confirmed invisible to `Fleet`/`FoldState`/`ProposalsForAddress`/
`LastObservedHash`); `TestUBI29_MixedAttempt_OnlyAppliedResourceDiscoverable`
(the design's own key claim, proven directly: within ONE unsealed
multi-resource attempt, the resource that reached `applied` is
discoverable while its sibling that didn't is not — gating is
per-resource, never per-attempt). All pass; full repo `go build ./...`/
`go vet ./...`/`gofmt -l .`/`go test ./... -race -count=1` clean, no
regressions anywhere.

**Live-verified on real AWS** (account `839333509514`, a fresh scratch
ledger `ubi29-live`, the same `aws_sqs_queue`+`aws_sqs_queue_policy`
pattern as UBI-27's own finale): shipped the chain, confirmed `ubx status`
sees both resources immediately (`kind: change`, not `adoption`) — before
this session, the exact same command reported "0 resource(s)." A real,
honest surprise along the way, not a bug: `--drift` fired immediately
after a clean ship, because shipping two interdependent resources in one
chain left the queue's own recorded `provider_result` (captured before its
sibling policy existed) genuinely stale relative to live reality (the
queue's own `policy` attribute changes once a policy attaches to it, plus
a benign `region: null` → `"us-east-1"` SDKv2 computed-fill-in quirk) —
accepted as two honest `drift_adopt`s, establishing a clean baseline. Then
the actually-requested test: a real `aws sqs tag-queue` out-of-band
mutation, detected by `status --drift`, CloudTrail attribution attempted
(fired correctly, honestly reported `cloudtrail_unattributed`/
`delivery_window` — real delivery lag, the same expected outcome UBI-10's
own report already documented), resolved and accepted. `ubx why <address>`
then shows the full genesis chain: `change` (with its own apply history:
resolve → accept → ship) at the bottom, two `drift_adopt`s above it —
before this session, `ubx why` on this exact address would have reported
"no proposals found" (nothing in `resolution.inputs` ever named a
create's own address). Cleaned up via plain `aws sqs delete-queue`
(destroys stay out of v1 scope); account confirmed clean via `aws sqs
list-queues` (zero queues) — `ubx status --drift`, run once more, honestly
reports both resources `unreadable` afterward (the ledger still thinks
they exist; the same named, accepted v1 limitation UBI-27's own report
already established, not a new gap).

Full docs/reliability-report.md gained a new UBI-29 section with every
transcript above in full, including the "real, honest surprise" writeup.
ubiquex-docs gained a new `cli/status.mdx` note (a shipped create shows up
immediately, real transcript) and a new `cli/why.mdx` "A resource's
genesis is a shipped create" section (the full real create-then-drift
transcript) — cross-linked both ways; `mint validate`/`mint broken-links`
both pass. Filed and closed in the same session: **UBI-29**, team
`ubiquex` (referenced throughout, per the handoff's own instruction — no
other ID inferred).

## Current phase (previous)

**UBI-27 session 4 is done (this session): executor unknown-value wiring
+ the live create finale on real AWS. UBI-27 is closed.** Sessions 1-3
built and CLI-surfaced the resolver; this session made `ubx ship` actually
execute a resolved `change` proposal for real, then proved it on real AWS
infrastructure — the first time this codebase has ever created cloud
resources, not just read or reverted them.

**`provider/ctyvalue.go` gained `encodeUnknownAwareDynamicValue`** (plus
`encodeBlockValue`/`encodeNestedBlockValue`/`encodeGenericValue`/
`encodePrimitiveValue`), the fix docs/executor.md's session-1 amendment
named but didn't build: a `$computed` marker, or any schema-`Computed`
attribute the resolved config simply never set, both become a real
`cty.UnknownVal` in `PlannedState`/`Config` — never `Null`, which a real
SDKv2-vintage provider's `Apply` silently leaves un-computed. **A second
gap, not named in the original amendment, found by actually building
this**: the amendment only ever described the explicit-marker case;
"a brand-new resource's own never-referenced Computed attribute" (its own
`id`/`arn` on a from-scratch create) needed the exact same treatment,
walking the schema's own `Block` (not just its flattened `cty.Type`, which
erases `Computed`) rather than the JSON tree alone. Wired into
`provider.go`'s `ApplyResourceChange` for `plannedState`/`config` only —
`priorState` keeps the plain encoder (a genuine create's `PriorState` is
literal JSON `"null"`, which `ctyjson.Unmarshal` already turns into the
correct top-level `cty.NullVal` given the right input; the fix needed was
at the call site, not in encoding `null` itself). Verified empirically
against a real provider, not assumed from the fixture alone
(`provider/apply_live_test.go`, `hashicorp/time`'s `time_static`):
`TestApplyResourceChange_RealProvider_TimeStatic_Create` (absent-Computed
attributes) and `_ComputedMarker` (an explicit marker) — both settle
docs/resolver-adversarial.md's row 10 empirically: a directly-constructed
`Unknown`, no separate `PlanResourceChange` call, resolves correctly
during a real provider's `Apply`.

**`core/executor/ship.go` gained `shipChange`**, a new code path for
`core.KindChange` proposals, alongside the renamed (body otherwise
unchanged) `shipDriftRevert` — `Ship()` itself is now a 3-line dispatcher.
`changeNodesOf` decodes `delta.creates`/`delta.modifies` into one combined
list and re-derives execution order from each entry's own `depends_on`
field via `topoSortAddresses` (a small, independent DFS instance,
mirroring `core/resolver`'s own `topoSort` pattern without importing it —
`core/executor` stays resolver-import-free). A resource is never attempted
while any `depends_on` entry hasn't reached `applied` — checked against
both this invocation's own progress and every prior attempt's durably
recorded result (`foldResourceHistory` gained `lastProviderResult`,
folding the most recent NON-EMPTY `ProviderResult` across all attempts,
never just "whatever the latest attempt says," since a later attempt's own
pass-through confirmation never re-records it). `substituteComputed`
walks a create's config (or a modify's `After` map, via
`substituteModificationComputed`) replacing every `$computed` marker with
its dependency's real value via `core.DotGet` against `resultsByAddr` —
mutating nothing in place, the proposal's own recorded content stays
exactly as resolved. `shipCreate` constructs `PriorState = json.RawMessage("null")`
and calls `Applier.ApplyResourceChange` with the substituted config.

**A real, load-bearing bug found live-shipping this against AWS for the
first time**: `shipCreate` never called `Applier.Configure` at all.
`shipDriftRevert`'s own modify path gets it for free, indirectly, through
`core.ReadAndFingerprint`/`core.VerifyFreshness` (both call it internally
before every read) — but a create never reads anything first, so it
reached a real, never-configured `terraform-provider-aws`. This didn't
surface as a clean error: the request killed the provider subprocess
outright, surfacing to `ubx` as a bare transport EOF
(`rpc error: code = Unavailable desc = error reading from server: EOF`),
indistinguishable at first glance from a genuine mid-call crash. Found by
reproducing the exact `ApplyResourceChange` call directly against the
acquired binary (bypassing the CLI) and noticing it succeeded there, where
the only difference was an explicit `Configure` call the debug harness
made and the CLI path never did. Fixed by calling `app.Configure(ctx,
providerSchema, providerConfig)` explicitly in `shipCreate`, before the
first create's own schema is even used.

**A second, unrelated real bug found running the full test suite the same
session**: `core/resolver`'s `TestResolve_RefToExistingLedgeredResource_AlwaysConcrete`
failed once with a `DoubleRun` mismatch, non-deterministically.
`resolveOnce` called `time.Now()` itself on each of `DoubleRun`'s two
internal calls — a resolve whose two calls straddled a one-second boundary
(RFC3339's own resolution) produced two genuinely different `resolved_at`
strings, a false-positive mismatch over a value never meant to be checked
for run-to-run stability. Rare (didn't reproduce in 15 runs after first
appearing, nor a 50-run stress loop after the fix) but real and
load-bearing for anyone running `ubx resolve` for real. Fixed by capturing
`resolvedAt` once in `Resolve()`, before either `DoubleRun` call, and
threading it into `resolveOnce` as a parameter.

**A new debug-only hook**: `UBX_SHIP_DEBUG_DELAY_BETWEEN_RESOURCES`
(`core/executor/ship.go`), sleeping in `shipChange`'s own loop after one
resource is fully durably persisted and before the next is even looked at
— the multi-resource counterpart to the existing
`UBX_SHIP_DEBUG_DELAY_AFTER_INFLIGHT`/`_AFTER_APPLY_SUCCESS` hooks, needed
because "kill -9 between resource 1 and resource 2" (this session's own
ask) is a window BETWEEN two resources, not within one resource's own
apply call. Zero (no delay, invisible) unless explicitly set.

**Hermetic adversarial coverage** (`core/executor/ship_test.go`, a
`fakeApplier` extended to support creates — auto-assigning an id when
`plannedState` has none and `priorState` is literal `"null"`, plus
`scriptCreateFailure` keyed by a create's own content since no id exists
yet to key by): `TestShip_ChangeProposal_CreateChain_FeedsComputedOutputForward`
(happy path), `TestShip_ChangeProposal_DependentNeverAttemptedBeforeDependencyApplies`
(the "dependent shipped before dependency's output exists" adversarial
row — structurally impossible, proven by an empty `fake.resources` map),
`TestShip_ChangeProposal_KillBetweenDependencyAppliedAndDependentStarting_RecoversRealOutputOnRerun`
(the kill row, hand-building a crashed attempt-1 file the same way this
file's own pre-existing crash tests do). `provider/internal/fakeprovider`'s
`decodeWidgetState` extended to recognize genuinely-Unknown (not just
Null) `id`/`tags` and fill them in — the fixture's own stand-in for a real
provider's Computed-attribute behavior. A new CLI-level test
(`cli/ship_change_test.go`) proves the full `resolve → accept → ship → why`
pipeline against the real `fakeprovider` binary (not just Go-level fakes)
before spending real AWS calls on the same mechanism. `cli/why.go` fixed
alongside (`ubx why` never fetched apply history for `KindChange`
proposals at all — only `KindDriftRevert` — a one-line gap found while
building the CLI test).

**Live, on real AWS** (account `839333509514`, region `us-east-1`, a
fresh scratch ledger, not the long-lived `payments`/`ubx-states` one):
`aws_sqs_queue` + `aws_sqs_queue_policy` (the policy's `queue_url`
depending on the queue's own computed `url`) — resolved, accepted,
shipped for real (after the Configure fix above; the first two attempts,
before the fix, failed exactly as described and are preserved honestly in
the apply record, not scrubbed). Verified independently via `aws sqs
get-queue-attributes`, and the flowed value cross-checked directly in the
sealed apply record (the policy's `queue_url` byte-identical to the
queue's own applied `url`). A second, fresh chain was shipped with a REAL
`kill -9` (via the new debug hook + a poll loop watching the live apply
file) between the queue applying and the policy's own turn beginning;
`ubx why` shows the full, honest two-attempt story; the queue's own AWS
`CreatedTimestamp` proves it was never re-created on re-run, and the
policy's own applied `queue_url` proves the dependency's REAL output was
recovered from attempt 1's history, not lost or re-derived. Both queues
deleted via plain `aws sqs delete-queue` afterward (destroys are out of
v1 scope) — `aws sqs list-queues` confirms the account is genuinely clean.

**A real, unresolved gap found doing that same cleanup check, not fixed
this session**: `ubx status --drift` reported "0 resource(s)" for the
stack that had just had two real resources shipped into it — not because
they don't exist, but because `core.Ledger.Fleet` (what `status` walks)
and `ProposalsForAddress`/`LastObservedHash` (what `why <address>` and
freshness re-verification key off) all discover a resource exclusively via
a `resolution.inputs[].resource` entry, and a `change` proposal's own
create never gets one for its own address (a create was never observed —
its real lookup key isn't even known until `ship` applies it, well after
the proposal's content hash is sealed, so nothing can retroactively add
one). A shipped create is correctly applied and durably recorded in its
own apply record, but **invisible** to `status`/`why <address>` going
forward. Recorded in docs/resolver.md and docs/executor.md's own "Out of
scope" sections — the likely fix is `ubx ship` durably recording something
ledger-chain-visible once a create lands (functionally a synthetic
adoption), not a resolver-time change. Left for a follow-up session/ticket,
not silently patched around.

Full docs/reliability-report.md now has a UBI-27 section with every
transcript above in full (both real bugs, both live chains, the cleanup
gap). ubiquex-docs gained `cli/ship.mdx`'s own "Shipping a change
proposal" section (a blocked-dependency example generated for real, not
hand-written) and a new `guides/create-flow.mdx` walkthrough built from
the real AWS transcripts; `mint validate`/`mint broken-links` both pass.
Full repo `go build ./...`/`go vet ./...`/`gofmt -l .`/`go test ./...
-race -count=1` clean.

**UBI-27 is closed** — all four sessions' work (design, hermetic
resolver, CLI surface + cross-stack pinning, executor + live finale) is
built, tested, live-verified, and documented. The one real, open item
(Fleet/status discoverability of shipped creates) is recorded as a named
gap for a future ticket, not a loose end pretending to be closed.

## Current phase (previous)

**UBI-27 session 3 is done (this session): CLI surface + cross-stack
pinning wired into `ubx accept`.** Session 2 built `core/resolver`
hermetic; this session gave it a real CLI surface and closed the loop
docs/resolver.md's own "neighbor-advance staleness" mechanism promised.

**New verb: `ubx resolve <intent-file>`** (`cli/resolve.go`), not a flag on
`ubx propose`. Decision, justified inline in the command's own doc
comment: `ubx propose` has one narrow, already-established job — hash an
already-resolved draft proposal for a PR trailer, explicitly refusing
anything not already fully resolved — folding intent-file resolution into
it as a mode flag would conflate two genuinely different operations, the
same way this codebase never merges scan/accept/ship into one
multi-purpose verb. `ubx resolve` instead slots into the pipeline exactly
like `ubx scan` already does: reads some input (an intent file, not live
drift), produces a draft proposal, which `ubx propose`/`ubx accept` then
take unchanged.

**A real gap surfaced against the session-1 design doc, found before
writing a line of the command**: docs/resolver.md's own contract text
lists "live state via `core.StateReader`" as an input to resolution, but
session 2's actual `Resolve()` implementation never uses a `StateReader`
at all — only `l.FoldState()` (ledger state; a real cloud read happens
later, at `ubx ship` time, the same way it already does for
`drift_revert`). Consequence: `ubx resolve` needs a provider launched for
exactly one reason (fetching its schema via `client.Provider.Schema(ctx)`)
and never calls `Configure`/`ReadResource` — so, unlike `ubx scan`/`ubx
accept --reverify-with`, it needs no `--provider-config` flag at all.
`cli/schemainspector.go` bridges `core/resolver.SchemaInspector` to a real
`*provider.Schemas` dump — the one place needing both `core/resolver` and
`provider`, the same boundary role `stateReaderAdapter` already
established for `core.StateReader`/`executor.Applier`.

**`resolver.VerifyPins` (built hermetic in session 2) is now wired into
both `ubx accept` code paths** — the local-file path (`cli/accept.go`'s
main `RunE`) and `acceptFromMerge` — called right before `core.Accept`/
`core.AcceptFromMerge`, unconditionally, not gated behind an opt-in flag
the way `--reverify-with`'s live-state reverify is. Reasoning (recorded
inline in the code): checking whether a neighbor ledger's head has moved
is a free, local filesystem read, unlike a real provider round trip, so
there's no cost/latency reason to make an operator ask for it explicitly.
`acceptErrorCode` now also classifies `resolver.ErrCrossStackPinStale` via
`errors.Is` as exit `1` — the same "actionable finding" tier as a blocked
reverify or a `parent` mismatch, not a generic exit-`2` error.

**New CLI-level tests** (`cli/resolve_test.go`, using the same
`fakeProviderBinary`/`runUbx` fixtures every other CLI test package
already uses): `TestResolveAccept_SimpleCreate` (resolve → accept → why,
full loop), `TestResolve_UnknownType` (exit 2, the plain-error tier),
`TestResolveAccept_CrossStackPin_StaleBlocksAccept` (two real ledger
dirs, a `$cross` reference, the neighbor ledger advanced between resolve
and accept, accept refused at exit 1, `ledger.lock` confirmed absent
from the stack ledger). All pass; full repo `go build ./...`/`go vet
./...`/`gofmt -l .`/`go test ./... -race -count=1` clean, no regressions.

**Live verification against the actual built binary** (`go build -o
/tmp/ubx-verify ./cmd/ubx`), not just `go test`, run manually against
real filesystem ledger directories under `/tmp` (cleaned up after): a
single create resolved and accepted, confirmed via `ubx why`; a
two-resource intra-stack `$ref` chain showing both mechanisms at once
(concrete substitution for a non-`Computed` target, a `$computed` marker
propagated for a `Computed` one, plus the emitted `depends_on`); an
`op: "modify"` intent producing a real before/after diff against
`FoldState`; an unknown-type resolve error (exit 2); and the full
cross-stack pin scenario — resolve with `$cross` against a real neighbor
ledger, accept while fresh (passes), advance the neighbor ledger via a
second real accept, attempt to accept the same pinned proposal again
(refused, exit 1, exact `ErrCrossStackPinStale` message, ledger untouched).
Every transcript in ubiquex-docs below is real output from this run, not
hand-written.

**ubiquex-docs updated same-session**: new `cli/resolve.mdx` (flags, the
intent-file format, intra-stack refs — both the concrete-substitution and
`$computed`-propagation cases — cross-stack refs and pinning, `op:
"modify"`, failure modes, all with real transcripts); `cli/accept.mdx`
gained a "Cross-stack pin verification" section with the fresh-pass/
stale-block transcript pair; `cli/exit-codes.mdx` updated (`ubx resolve`
added to the no-finding-concept group, `ubx accept`'s exit-1 row
mentions stale cross-stack pins); `docs.json` nav updated (`cli/resolve`
between `cli/scan` and `cli/accept`). `mint validate` and `mint
broken-links` both pass clean.

**Next slice** (per docs/plan.md's "Resolver v1 (UBI-27)" wedge): executor
unknown-value wiring — fakeprovider first, then a real, cheap two-resource
create chain on `ubx-states` with one resource's computed output feeding
the other, shipped, a real `kill -9` mid-chain, reconciled.

## Current phase (previous)

**UBI-27 session 2 is done (this session): `core/resolver` — type rules,
graph, double-run, hermetic against fake schemas and ledger state.** Built
exactly against docs/resolver.md's spec and docs/resolver-adversarial.md's
program, both written in session 1. No CLI surface yet (no `ubx resolve`/
`propose --from-intent` command exists until a later session), so no
ubiquex-docs update this session — correct per protocol, not deferred
debt.

**Package layout**: `core/resolver` (a subpackage of `core`, mirroring the
`core/executor`/`core/lookuphints` precedent) — `resolver.go` (types,
errors, `Resolve`/`resolveOnce`, `VerifyPins`), `refs.go` (marker
recognition + `$ref`/`$cross`/`$secret`/`$computed`/`$ephemeral`
resolution), `graph.go` (`topoSort`, the real cycle detection v1 XCL's own
single-stack graph never had). Provider-import-free by design: a
`SchemaInspector` interface (`HasType`/`IsComputed`/`IsSensitive`) stands
in for a concrete `*provider.Schema`, the exact same shape
`core/executor.Applier` already established for the same reason (`core`
never imports `provider`) — hermetic tests use a small fake; a real
adapter (alongside `stateReaderAdapter`) is later CLI-session work.

**`core` gained four small, additive pieces this session, each because a
real second caller now needed them, not speculatively**:

1. **`core.DiffAttributes`** — exported (previously private `diffAttributes`).
   `core/resolver` needed exactly the same before/after diff
   `GenerateProposal`/`GenerateRevertProposal` already use for drift, for
   an `op: "modify"` intent's full-desired-config against `FoldState` —
   one mechanism, three callers now, not three, per docs/resolver.md's own
   plan.
2. **`core.DotGet`** (`state.go`, alongside `dotSet`/`dotDelete`) — reads a
   dot-notation path out of a `json.RawMessage` state blob, returning
   another `json.RawMessage`. Used by `$ref`/`$cross` resolution against an
   already-ledgered (not-in-this-batch) resource's `FoldState`.
3. **`core.Ledger.Exists()`** — whether a ledger directory has ever
   actually been used (checks for `ledger/`'s own existence), distinct
   from `FoldState`'s `found=false` (which never distinguishes "no ledger
   here at all" from "a real ledger that just never recorded this
   address"). Needed for docs/resolver-adversarial.md row 4's two
   distinguishable sub-cases.
4. **`core.Validate`'s `validateChange`** — `KindChange` proposals now have
   real structural rules for the first time (destroys forbidden
   unconditionally, blast radius must equal real create/modify counts),
   exactly matching docs/schema.md's own amendment. Three existing tests
   that had been using `KindChange` as a stand-in "arbitrary kind with no
   rules" (an implicit assumption that stopped being true) were fixed —
   two adjusted their own `Delta`/`BlastRadius` to stay internally
   consistent, one switched to `KindRevert` (still genuinely unvalidated)
   to preserve its original intent.

**Two new, purely additive fields**: `Modification.DependsOn []string` and
`ResolutionInput.PinnedHead`/`LedgerDir string` — exactly as
docs/schema.md's own amendment described, except for one correction (next).

**A real, honest correction found by actually implementing the design, not
assumed correct from the session-1 spec alone**: the `$cross` marker's
drafted shape (`{"$cross": {"stack": "...", "ledger_dir": "...", "path":
"..."}}`) never actually named the target resource's own `type`/`name` at
all — structurally incomplete, impossible to resolve as written. Corrected
to reuse `$ref`'s own `to` shape exactly (`{"$cross": {"ledger_dir": "...",
"to": "<stack>.<type>.<name>.<path>"}}`), with `ledger_dir` as the one
genuinely new field — one convention, not two. Found and fixed
immediately upon writing `resolveCross`, before any test was written
against the wrong shape. A second, related gap: re-verifying a cross-stack
pin later needs to know *which* neighbor ledger directory to re-open —
nothing in the original amendment recorded this, so `ResolutionInput`
gained `LedgerDir` alongside `PinnedHead`. Both corrections are recorded
directly in docs/schema.md's own amendment text (not silently patched),
per protocol.

**`resolver.VerifyPins`** (new, `resolver.go`) makes docs/resolver-adversarial.md
row 5 ("neighbor advances between resolve and accept") real and
hermetically tested this session, ahead of schedule — re-derives every
`cross_stack_pin` resolution-input's neighbor ledger's current `Head()`
and compares against the recorded `PinnedHead`, `ErrCrossStackPinStale` on
a mismatch. Wiring this into `ubx accept` itself is explicit later
CLI-session work (docs/plan.md's own "Resolver v1" wedge), not this one's
— the mechanism is built and proven now, the integration comes with the
CLI surface.

**All nine hermetic rows of docs/resolver-adversarial.md's program pass as
real tests** (`core/resolver/resolver_test.go`, a `fakeSchema`
`SchemaInspector` plus real `*core.Ledger`s via `t.TempDir()`, seeded
through the real `GenerateProposal`/`Accept` pipeline via a `seedLedger`
helper — never hand-constructed ledger content):

1. Double-run divergence — a `flakySchema` wrapping the fake, alternating
   `IsComputed`'s answer for one specific (deliberately non-computed) path
   across calls so *both* of `core.DoubleRun`'s two runs succeed but
   disagree on the actual resolved bytes (an earlier version of this test
   alternated a genuinely-computed path instead, which just made the
   *first* run error out — `DoubleRun` never even attempts a second run if
   the first one fails, so that version could never have exercised the
   mismatch path at all; caught by actually running it, not assumed
   correct from the test's own intent).
2. Circular intra-stack refs — two resources referencing each other;
   `ErrCycleDetected` naming the full cycle path. Genuinely new code, not
   ported from anywhere in v1 (see session 1's own finding).
3. Ref to nonexistent resource — `ErrRefNotFound`.
4. Cross-stack pin against an empty/missing neighbor ledger — two distinct
   sub-cases, both tested: a `ledger_dir` that's never been touched at all
   (`ErrNeighborLedgerMissing`, via `Ledger.Exists()`) vs. a real neighbor
   ledger that simply never recorded the target address
   (`ErrCrossStackAddressNotFound`).
5. Neighbor advances between resolve and accept — folded into the
   cross-stack happy-path test: `VerifyPins` passes right after resolving,
   then a second real proposal accepted against the neighbor ledger makes
   a second `VerifyPins` call correctly fail with `ErrCrossStackPinStale`.
6. `$computed` used where a concrete value is required — a real, natural
   three-resource chain: `c` refs `b`'s `peer` field (not schema-`Computed`
   on `aws_db_instance`), but `b`'s own resolved value there is itself
   `$computed` (inherited from `b`'s own ref to `a`'s real `Computed`
   `id`) — `c`'s ref correctly fails, since the schema's promise that this
   attribute is concrete doesn't hold in the actual resolved chain.
7. Secret ref in a non-`Sensitive` field — `ErrSecretNotSensitive`; a
   positive test (a genuinely `Sensitive` field) confirms the marker is
   preserved unchanged in the output.
8. Intent for a type the schema lacks — `ErrUnknownType`.
9. Modify intent whose target isn't in the ledger — `ErrModifyTargetMissing`;
   the symmetric `create`-but-already-exists case
   (`ErrCreateTargetExists`) is also tested.

Plus structural tests beyond the named rows: unrecognized intent-file
`kind`, an invalid `op` string, a duplicate resource address in one
intent file, `$ephemeral`'s atomic pass-through, and `core.Validate`
correctly rejecting a `change` proposal carrying `delta.destroys`.

`go build ./...`, `go vet ./...`, `gofmt -l .` (clean), and `go test ./...
-race` all pass across the whole repository — no regressions in any
existing package.

**Next slice** (per docs/plan.md's "Resolver v1 (UBI-27)" wedge):
intent-file loading + a CLI surface (verb vs. flag — decide and justify),
then cross-stack pinning wired against real ledgers (including
`VerifyPins` actually plugged into `ubx accept`), then executor
unknown-value wiring.

## Current phase (previous)

**UBI-27 session 1 is done (this session, docs-only, Linear filed and
tracked): Phase 2 continues — the resolver.** Filed as a new Linear issue
(`UBI-27`, ubiquex team) per the handoff's own instruction (no other ID was
inferred). v1 scope, pinned in the issue and in docs: `kind: "change"`
proposals (creates + modifies, NO destroys) resolved from hand-written,
machine-shaped `ubx:intent/v1` files. Docs-only by design (per the
handoff: "design lands in docs BEFORE any code... commit session 1
docs-only"); no code was written, no ubiquex-docs update needed (no
user-visible CLI surface exists yet).

**A real, honest correction, found before any design work and worth
stating plainly rather than quietly working around**: CLAUDE.md and
docs/architecture.md both name "v1 XCL's typechecker" as the thing whose
type system and graph algorithms inform v2. Checked directly, not
assumed: `/Users/roozbeh/Ubiquex/xcl` (the repo literally named `xcl`) is
**only** a lexer/parser/AST/formatter — its own README says exactly that
("no infrastructure semantics... those live in the `ubx` tool"), and
confirmed empirically by grepping the whole repo for `Computed`/`Pending`/
any graph/typecheck code and finding none. The real type system and graph
algorithms live in a **separate** repo, `/Users/roozbeh/Ubiquex/ubx`
(distinct from both `xcl` and this project, `ubiquex-cli`) — a
Pulumi-targeting infrastructure-DSL compiler, with real
`internal/xcl/typechecker`, `internal/xcl/ir`, `internal/xcl/scope`,
`internal/xcl/crossstack`, and `internal/xcl/workspace` packages. Every
rule in docs/resolver.md is lifted from *that* repo's real code (a
dedicated research pass read it directly, with file:line citations), not
from `xcl`, which has nothing to lift. This is exactly the kind of
finding CLAUDE.md's own session protocol asks to be surfaced rather than
silently worked around ("if implementation reveals a doc is wrong, stop,
record the finding... and flag it") — flagged here, in docs/plan.md's own
new wedge subsection, and in docs/resolver.md's own opening section.

**Real rules lifted from v1 (the `ubx` repo), verified by direct reading,
not assumed from the doc's own one-line summary:**

1. **No `Pending<T>`/`Computed<T>` wrapper type at all** — a binary
   `ValueKind` (`Resolved` | `Pending`) tags every reference; a resource
   output, module output, remote/data-source output, or non-`env`-backend
   `secret()` is always `Pending`. The typechecker hard-errors on a
   `Pending` value used where `Resolved` is required (v1's only instance:
   `when`/`extend` conditions) — the *rule shape* this project's own
   `$computed` marker (drafted at founding, never implemented) already
   anticipated, now actually built.
2. **Two real gaps, found and deliberately NOT carried forward as-is**:
   v1's own single-stack resource graph (`ir/build.go`'s `topoSort`) never
   detected cycles at all — only its separate, workspace-level multi-stack
   graph (`workspace.go`'s `TopoSort`, a real `path`/`inStack` DFS that
   reports the full cycle) did. docs/resolver.md's own cycle detection for
   the *intra-stack* graph is genuinely new code, borrowing the
   workspace-level DFS pattern one level down, not a port of anything v1
   already had there. And v1 has **no pinning or staleness concept for
   cross-stack refs at all** (`@stack.output` resolves against whatever a
   live sibling directory currently says, no version, no head, no
   staleness) and **no double-run/determinism enforcement** (only an
   unenforced "sort your maps" convention) — both are deliberate v2
   improvements, reusing mechanisms this project already built for other
   reasons (`core.DoubleRun`, `VerifyFreshness`'s own resolved-time-vs-
   accept-time staleness shape) rather than inventing anything new.
3. **`secret()` and `ephemeral`**: v1 has no `Secret<T>` type and no
   validation at all preventing a secret from flowing into an arbitrary
   field (only a backend-name warning) — docs/resolver.md's own
   `$secret`-must-target-a-schema-`Sensitive`-attribute check is new, real
   validation, grounded in infrastructure this project already built for a
   different reason (UBI-23/24's `provider.Schema.Attribute.Sensitive`).
   `ephemeral` carries over directly as an orthogonal "don't persist this"
   flag, exactly matching v1's own `IRInput.Ephemeral` posture (layered on
   top of, never instead of, resolved-vs-computed status).

**Three new/amended documents, plus one wholly new one, in the order the
handoff specified:**

1. **docs/resolver.md** (new) — the resolver's contract:
   `(intent file, core.StateReader, provider schema via a new
   SchemaInspector interface, a policy-stub hook) → resolved Delta,
   dependency-ordered, run through core.DoubleRun`. `core/resolver` stays
   provider-import-free by design — the same `any`-typed-schema-handle
   pattern `core.StateReader`/`core/executor.Applier` already established,
   a `SchemaInspector` interface (`HasType`/`IsComputed`/`IsSensitive`)
   standing in for a concrete `*provider.Schema`, with the real adapter
   living in `cli` later, not `core`. Full intra-stack ref resolution
   rules (sibling create vs. already-ledgered target; literal substitution
   vs. `$computed` marking, checked against schema `Computed`, never
   assumed from a name), the dependency graph + real cycle detection,
   cross-stack pinning against a neighbor ledger's `FoldState` (recording
   `pinned_head`, with a precise note on when a cross-stack ref can itself
   still be `$computed` — the neighbor's own unshipped `change` proposal),
   and the `op: create | modify` design decision (explicit, not inferred
   from ledger presence — inferring it would make "modify intent whose
   target isn't in the ledger" uncatchable, since the resolver would just
   silently treat it as a create instead).
2. **docs/schema.md** — "Amendment: intent files and resolved `change`
   proposals." Pins `ubx:intent/v1`'s wire shape (deliberately
   machine-shaped, not for hand-typing in production — the pretty
   frontends are explicit future phases); `Delta.Creates`' full node shape
   for a resolver-produced create (`config` alongside — never replacing —
   adoption's own `state` key, plus a new `depends_on` field, also added to
   the already-pinned `Modification` shape); a new `cross_stack_pin`
   `resolution.inputs[]` kind carrying the one genuinely new field,
   `pinned_head`; and `change`'s own propose-time validation (destroys
   forbidden, unconditionally — not just zero for now; blast radius real,
   matching `drift_revert`'s own precedent). Purely additive throughout, no
   `schema_version` bump — the same reasoning as every prior amendment.
3. **docs/executor.md** — "Amendment: shipping resolved `change`
   proposals." Three real pieces: (a) `PlannedState` carrying genuine
   tfplugin unknowns for `$computed` — checked directly against the real
   `go-cty/cty/msgpack` library (not assumed): `ctymsgpack.Marshal` already
   fully supports encoding `cty.UnknownVal`, but `encodeDynamicValue`'s
   existing `ctyjson.Unmarshal`-from-JSON path can never produce one (JSON
   has no "unknown" literal, only `null`) — the real gap, and the fix (a
   new JSON-tree-walking construction path substituting `cty.UnknownVal`
   at exactly a `$computed` marker's position), pinned as design, left for
   session 2+ to implement. Explicitly flagged as genuinely open, not
   assumed safe: whether a real provider's `ApplyResourceChange` — called
   directly, no separate `PlanResourceChange`, the same shortcut
   `drift_revert` already takes — actually resolves a directly-constructed
   unknown correctly, since some providers may depend on `PlannedPrivate`
   bytes only a real `Plan` call produces; this is precisely
   docs/resolver-adversarial.md's own row 10, to be settled empirically,
   not asserted in advance. (b) Dependent resources fed mid-walk: a
   resource's real `provider_result` substituted into every
   `depends_on`-linked sibling's still-pending `PlannedState` once it
   reaches `applied`, generalizing `core.ApplyAfter`'s own substitution
   from "restore a recorded value" to "fill in a value that just became
   known." (c) Apply records naturally carry the resolved concrete value
   where `$computed` once was — no new ledger mechanism, a presentation
   concern for `ubx why` when that session comes.
4. **docs/resolver-adversarial.md** (new) — the required-outcome program,
   ten rows exactly as the handoff named them (double-run divergence;
   circular intra-stack refs; ref to nonexistent resource; cross-stack pin
   against an empty/missing neighbor ledger; neighbor-advances staleness;
   `$computed` used where concrete is required; secret ref in a
   non-secret-capable field; intent for an unrecognized type; modify
   intent whose target isn't in the ledger; a real unknown-value round
   trip through `PlanResourceChange`/`ApplyResourceChange`) — row 10
   explicitly framed as "find out," not "assumed to pass," since it's the
   one row this session's own research couldn't settle from library code
   alone. A closing section names what this table doesn't yet cover
   (multi-hop cross-stack pin chains, a `$computed` value that's itself the
   target of a third stack's cross-reference, a provider schema changing
   shape between resolve and ship) rather than silently implying
   completeness.

**Linear**: `UBI-27`, "Resolver v1: typed intent → resolved change
proposals (creates/modifies)," filed against team `ubiquex`, full handoff
scope recorded in the issue description. Left in its default `Backlog`
state (session 1 of a multi-session ticket; will move once the v1 slices
land).

## Current phase (previous)

**UBI-26 is closed (this session, session 4, Linear-verified): the live
adversarial program against real AWS infrastructure.** New
docs/reliability-report.md, drafted from docs/executor-adversarial.md's own
table plus the hermetic suite's results plus this session's own live runs —
every transcript real, no adjectives, exactly as instructed. This is the
final slice of a four-session ticket (design → hermetic state machine →
real provider wiring/CLI → this).

**Real AWS, `ubx-states` bucket (`us-east-1`), the same demo
ledger/account (`839333509514`) every prior live session on this project
has used** (`/Users/roozbeh/demo/payments`): three sections, all captured
into docs/reliability-report.md verbatim.

1. **`ubx`'s first-ever real cloud write.** Real out-of-band drift (an
   `incident` tag added via `aws s3api`), `scan --propose revert`,
   `accept`, `ship` — independently verified via `aws s3api
   get-bucket-tagging`, never just trusted from `ubx`'s own report (the
   session's own standing instruction, learned from session 3's
   `ApplyAfter` bug). Matched exactly. A real gap found here: `ubx why`
   rendered nothing about the apply at all — fixed same-session (below).
   Idempotent re-`ship` confirmed (`already fully applied`, no second
   attempt file).
2. **The centerpiece: a real `kill -9` between a real `ApplyResourceChange`
   call succeeding and `ubx` recording it.** `core/executor/ship.go` gained
   two package-level, env-var-gated debug delay seams (zero by default,
   same convention as `FAKEPROVIDER_MODE`/`UBX_CONFORMANCE_LIVE`):
   `UBX_SHIP_DEBUG_DELAY_AFTER_INFLIGHT` and
   `UBX_SHIP_DEBUG_DELAY_AFTER_APPLY_SUCCESS` — the second is what made
   this scenario reproducible on demand against a real network call rather
   than a matter of chance timing. First attempt at the live kill missed
   the window (the artificial delay elapsed before the kill command ran;
   recorded honestly in the report rather than discarded — a real,
   uneventful successful ship). Redone with a longer delay and a single
   tight `launch & sleep 15; kill -9 $!` shell sequence: confirmed via `aws
   s3api` that the real mutation had already landed while `ubx`'s own apply
   record still only showed `pending`→`in_flight`. Re-running `ubx ship`
   surfaced a **second real bug**: `reconciliationVerdict` could never
   conclude `applied` for a pure-deletion revert (`After` empty — the same
   "attribute added out-of-band, nothing to add back" shape as session 3's
   `ApplyAfter` bug, this time in reconciliation) — reported `still_unknown`
   forever despite reading a live state that had, in fact, already been
   corrected. Fixed (`matchesRestoreTarget`/`matchesOriginalDrift`,
   replacing the old `matchesAll`), rebuilt, re-run against the *same*
   still-unresolved real proposal (attempt 3): resolved `applied` via
   reconciliation alone, no new `ApplyResourceChange` call issued, real
   cloud state unchanged from immediately after the kill. `parent`
   correctly chains attempt 3 to attempt 2's sealed ID.
3. **Stale mid-flow, live.** Real drift induced, accepted, then reality
   mutated a second time for real before shipping. `ubx ship` refused
   correctly: terminal `ErrStaleObservation`, resource never reached
   `in_flight`, real cloud state confirmed untouched.

**Two real bugs found and fixed this session, both the same class as
session 3's `ApplyAfter` bug (a revert that only ever removes an
out-of-band addition, `After` empty) — one layer over, in reconciliation
instead of in constructing the plan:**

1. `reconciliationVerdict`'s original `matchesAll(state, mod.After)` could
   never return true for an empty `want` map — meaning a pure-deletion
   revert could never be confirmed `applied` by reconciliation, no matter
   how long it retried. Fixed with `matchesRestoreTarget`/
   `matchesOriginalDrift`, which also check that every `Before`-only path
   is genuinely absent (or, for "never landed," still present) from the
   observed state — not just that `After`'s own paths match. New permanent
   hermetic regression:
   `TestShip_CrashAfterApplyLanded_PureDeletionRevert_ReconciliationResolvesApplied`.
2. `ubx why` never rendered anything about a proposal's own apply history —
   a real, user-facing incompleteness the ticket's own acceptance criteria
   named directly ("why tells the full story including the apply"), found
   the moment Section 1's transcript was being assembled. Fixed:
   `cli/why.go` gains `renderApplies` (human view: "apply history:", one
   block per attempt, sealed or not, with every transition/reconciliation/error)
   and `whyJSON.Applies` (fetched via `ledger.ApplyAttempts(p.ID)` whenever
   `p.Kind == KindDriftRevert`, regardless of whether it was ever shipped —
   an empty/nil slice renders nothing). Two new hermetic tests
   (`TestWhy_ShippedDriftRevert_ShowsApplyHistory`,
   `TestWhy_JSON_IncludesApplies`) plus live re-verification against the
   real proposal from Section 1.

**Account left matching the ledger's own recorded truth, not literally
"untagged"** — this session's own throwaway test tags (`incident`,
`killtest`, `killtest2`, `staletest`) were all removed; the two tags
(`hotfix`, `purpose`) the ledger's own `FoldState` already considered
current truth (recorded by prior sessions, never previously written back
to the cloud until this session's Section 1) were kept, matching the exact
convention every prior live-verification session on this same demo ledger
has followed. Confirmed clean with a final `ubx status --drift`. No IAM
changes; no resources created or destroyed; every real mutation was a tag
change on the same pre-existing bucket.

`go build ./...`, `go vet ./...`, `gofmt -l .` (clean), and `go test ./...
-race` all pass across the whole repository, including the two new bug's
regression tests.

**ubiquex-docs updated same session** (the new `ubx why` apply-history
rendering is a real, user-visible behavior change): `cli/why.mdx` gained a
new "A shipped `drift_revert`'s apply history" section (the real transcript
from Section 1/2 above) and a `--json` `applies` field example;
`concepts/apply-record.mdx` and `concepts/why.mdx` cross-link to it.
`mint validate`/`mint broken-links` both pass clean.

**UBI-26 closed in Linear.** Both repos committed and pushed.

## Current phase (previous)

**UBI-26 session 3 is done (this session): real `ApplyResourceChange`
wiring + `ubx ship` — the executor's first user-visible surface.** Builds
directly on session 2's hermetic `core/executor`; every mechanism there is
unchanged, just now driven by a real provider instead of a fake `Applier`.
ubiquex-docs updated same session (new `cli/ship.mdx` + `concepts/apply-record.mdx`,
`mint validate`/`mint broken-links` both clean) — this is the first UBI-26
session with real user-visible CLI surface, so the docs-same-session rule
applies for the first time on this ticket.

**`provider` package — `ApplyResourceChange` for both wire protocols:**

1. **`provider.Provider` interface gains `ApplyResourceChange`**, implemented
   on both `v6Provider`/`v5Provider` exactly mirroring `ReadResource`'s own
   shape: `encodeDynamicValue` for `PriorState`/`PlannedState`/`Config` (all
   three, cty-msgpack — the real wire requirement, confirmed empirically,
   see below), the real RPC, `decodeDynamicValue` on the response.
2. **New `provider.DiagnosticError`** — `diagnosticError[D]` (the generic
   helper `Configure`/`ReadResource`/`Schema` already shared) now returns
   this concrete type instead of a bare `fmt.Errorf`, byte-identical
   `Error()` text, zero behavior change for existing callers. This is the
   signal `docs/executor.md`'s error taxonomy depends on: a real,
   structured provider diagnostic vs. a transport/RPC-level failure, and
   until this session there was no way to tell them apart from the
   returned `error` alone.
3. **`provider/internal/fakeprovider` gained `ApplyResourceChange` handlers**
   (both protocols), driven by a new `FAKEPROVIDER_APPLY_MODE` env var
   (`ok`/`diagnostic-error`/`hang`) — reusing the same "scriptable failure"
   spirit session 2's own fake `Applier` established, now at the real
   subprocess/gRPC/cty-msgpack level. A real design correction made while
   building this, caught by a confusing manual test result before it became
   a permanent test bug: `ApplyResourceChange`'s "ok" path initially reused
   `ReadResource`'s own `echoWidgetState` (which unconditionally re-injects
   `FAKEPROVIDER_EXTRA_TAG` into its response, simulating "an out-of-band
   change is newly visible") — reusing that for *apply* made it impossible
   to tell "ubx correctly reverted this" apart from "the fixture re-injected
   the drift into its own apply response regardless." Split into a separate
   `echoAppliedState` (no `FAKEPROVIDER_EXTRA_TAG` reinjection — a real
   apply is deterministic in its own inputs) sharing the computed-id
   fill-in logic via a new `decodeWidgetState` helper.
4. **Hermetic tests** (`provider/apply_test.go`): happy path (both
   protocols, real cty-msgpack round trip through the fake subprocess),
   diagnostic-error → `*DiagnosticError` specifically, and a real
   client-context-timeout → a plain gRPC `DeadlineExceeded` status, never a
   `*DiagnosticError` (confirming the retryable/terminal signal is correct
   at the wire level, not just asserted).
5. **A real empirical verification against a real provider binary**
   (`provider/apply_live_test.go`, `hashicorp/time`'s `time_static` — no
   cloud credentials needed, pure local computation, gated
   `UBX_CONFORMANCE_LIVE=1` like every other network-touching test in this
   codebase): confirms docs/executor.md's "Constructing `PlannedState`
   without planning" mechanism is sound once given a realistic prior state
   (every computed attribute already concrete, as a genuine `ReadResource`
   call against an already-existing resource would return) — the real
   provider correctly carries every attribute *not* named in
   `Modification.After` forward unchanged while applying the one that is.
   **A real false start along the way, recorded rather than quietly
   fixed**: an earlier version of this same test used `PriorState = null`
   (a from-scratch create via `ApplyResourceChange` directly, no prior
   `ReadResource`) and found every computed attribute came back `null`, not
   computed — briefly looking like a real gap in the whole "no separate
   plan phase" design. It wasn't: an SDKv2-vintage provider's `Apply` only
   fills in a computed attribute it finds *unknown* in `PlannedState` (the
   marker a real `PlanResourceChange` call produces, which
   `encodeDynamicValue` has no way to express — only `null`, meaning
   "explicitly absent," not "compute this"). `drift_revert` never creates
   from scratch, so this never actually applies to it — the corrected test
   models the real shape and passes. Both attempts are documented directly
   in the test's own doc comment, not just here.

**`cli/stateadapter.go` — the adapter, extended, not duplicated:**
`stateReaderAdapter` gains `ApplyResourceChange`, satisfying
`executor.Applier` on top of the exact same struct `core.StateReader`
already uses (`newApplier`, alongside the existing `newStateReader`, both
views of one adapter). Redaction on the way out
(`provider.Redact(a.source, typeName, rs.Block, a.salt, result)`, identical
call shape to `ReadResource`'s own); `provider.DiagnosticError` wrapped as
`executor.TerminalError` on the way in, anything else passed through as
retryable — `cli` is the one place that imports both `provider` and
`core/executor`, exactly the same "adapter lives where both are needed"
posture `core`/`provider`'s own boundary already established.

**`core/executor` — redacted restore targets declined, both redaction
directions now real** (docs/executor.md gained two new sections: "Redacted
after values are declined, not applied" and the reframed "Redaction applies
at the apply boundary in both directions"): checked per resource, before
any read/freshness/apply work at all — `redactedAfterPaths(mod)` finds any
`core.IsRedactedValue` dot-path in `Modification.After`, and if any exist,
the *whole* resource is declined (never a partial apply of just the
non-redacted paths — `ApplyResourceChange` is one whole-state operation,
unlike `.tf` write-back's own per-attribute decline). Declined permanently,
not retried: the resource stays `pending` forever on every future `ubx
ship` re-run, since the redacted value never changes — the only way past it
is a human restoring the real value out-of-band through `ubx revert-plan`'s
existing manual path, named directly in the error message. Two new
hermetic tests confirm this (declined-not-applied-or-read, and
retried-forever-never-silently-skipped across three re-runs).

**A second real, load-bearing bug found live-testing the whole path end to
end (not just via unit tests) — fixed in already-committed session-2 code,
not new code**: `core.ApplyAfter` (added session 2) only ever *set*
`Modification.After`'s dot-paths onto the prior state; it never removed a
path that existed only in `Before` (docs/schema.md's own diff convention:
an attribute present in observed/drifted reality but absent from the
ledger's own recorded truth — e.g. a tag added out-of-band — has a `Before`
entry and *no* `After` entry at all, since there's no ledger value to
record for something the ledger never had). Reverting that means deleting
the attribute from live reality; `ApplyAfter`'s pure-substitution model had
no way to express that. Manually shipping a real revert against the real
built binary (fakeprovider subprocess, `FAKEPROVIDER_EXTRA_TAG` simulating
an added tag) surfaced this directly: `ubx ship` reported "applied"
cleanly, but the added tag was still there afterward. Fixed with a new
`core.dotDelete` (state.go, alongside `dotSet`) and `ApplyAfter` now
deleting every `Before`-only path after applying every `After` one; a path
present in *both* (an ordinary changed value) is unaffected. Three new
permanent regression tests: two direct `core.ApplyAfter` unit tests
(delete case, and the changed-value case staying non-deleted) plus one
full end-to-end test through `Ship` itself
(`TestShip_RevertRemovesAttributeAddedOutOfBand`), not just the unit level.

**`ubx ship <proposal-id>`** (`cli/ship.go`, registered in `root.go`): the
CLI wrapper over `executor.Ship`, in the same shape every other UBI-20-audited
command uses (`LoadConfig`/`applyProviderDefaults`/`resolveProviderBinary`/
`provider.Launch`/`ledger.Salt()`). A friendly early exit (wrong kind, not
accepted) before ever launching a provider, even though `executor.Ship`
enforces both authoritatively too. Exit codes: `0` applied (including the
`ErrAlreadyApplied` no-op, reported with the latest sealed apply record as
evidence, not an empty response), `1` `partially_applied`/`failed` (an
actionable finding), `2` a genuine error. `--json` wraps the sealed
`*core.ApplyRecord` directly under `format`/`apply_record` — the same
"wrap, don't re-derive" convention `whyJSON`'s `Proposal` field already
established, not a bespoke shape.

**Live-verified against the real built binary, not just `go test`**
(fakeprovider subprocess standing in for a real provider, exactly the same
posture UBI-9's own early conformance work used before real cloud accounts
were available): the full `scan --propose revert` → `accept` → `ship`
sequence, an idempotent re-`ship` (`already fully applied`), `--json`
output, a simulated terminal provider diagnostic (`FAKEPROVIDER_APPLY_MODE=diagnostic-error`,
exit `1`), and a hand-constructed redacted-restore-target proposal (exit
`1`, declined) — every transcript in the new docs pages is real output from
these runs, not hand-written. This is also exactly the process that found
both the `FAKEPROVIDER_EXTRA_TAG`-reinjection fixture wrinkle and the real
`core.ApplyAfter` deletion bug above — neither was visible from the
hermetic test suite alone, both were caught before shipping by actually
running the built binary end to end.

**ubiquex-docs updated same session**: new `cli/ship.mdx` (every transcript
real, captured from the built binary) and new `concepts/apply-record.mdx`
(the state machine, idempotency table, both-directions redaction, scope);
`cli/exit-codes.mdx` gained a `ubx ship` section; cross-links added from
`cli/revert-plan.mdx` (a `<Note>` distinguishing the two commands now that
both exist), `concepts/proposal.mdx`, and `concepts/secrets.mdx`. `docs.json`
nav updated. `mint validate`/`mint broken-links` both pass clean.

`go build ./...`, `go vet ./...`, `gofmt -l .` (clean), and `go test ./...
-race` all pass across the whole repository, including the new
`UBX_CONFORMANCE_LIVE=1`-gated real-provider test.

**Next slice** (per docs/plan.md's "Executor v1 (UBI-26)" wedge): turn the
full docs/executor-adversarial.md program green against a *real* cloud
resource on `ubx-states` (not just the fakeprovider subprocess this session
used) — real drift, accept revert, `ubx ship` restores it, second ship a
no-op, `ubx why` showing the full story including the apply record, and a
real `kill -9` mid-apply proving the re-run reconciles.

## Current phase (previous)

**UBI-26 session 2 is done (this session): `core/executor` — the state
machine + apply-record ledger ops, hermetic against a fake provider with
scripted failures.** Built exactly against docs/executor.md's spec and
docs/executor-adversarial.md's program, both written in session 1. No CLI
surface yet (`ubx ship` doesn't exist as a command until the next slice),
so no ubiquex-docs update this session — correct per protocol, not deferred
debt.

**`core/apply.go` (new file, package `core`)**: the `ApplyRecord` type
family (`ResourceApply`/`Transition`/`ReconciliationAttempt`/`ErrorRecord`/
`ApplySummary`) exactly as pinned in docs/schema.md's amendment, plus
`ApplyHash` (own `ubx:apply:v1\n` domain, `Resources` sorted by `(stack,
type, name)` before hashing — mirroring `canonicalProposalBytes`'s own
`sortDeltaElements`, a fresh implementation since `ResourceApply`'s
`address` field shape differs from `Modification`'s `target`). Five new
`*Ledger` methods, all reusing the existing `acquireLedgerLock`/directory
conventions rather than inventing new ones:

- `BeginApply(proposalID)` — under the ledger lock, scans every attempt
  file for this proposal (`ApplyAttempts`), assigns the next attempt
  number, sets `Parent` to the most recently *sealed* attempt's ID (`""`
  if none), writes an initial empty record, releases the lock.
- `SaveApplyProgress(rec)` — durably rewrites an unsealed record (temp file
  in the same directory, `fsync`, atomic rename) — the concrete mechanism
  behind docs/executor.md's THE invariant: every call site in `Ship`
  persists a transition this way *before* attempting the operation it
  precedes.
- `SealApply(rec, summary)` — sets `Summary`, computes `ApplyHash`, sets
  `ID`, persists the final content. Both `SaveApplyProgress`/`SealApply`
  refuse to touch an already-sealed record (`rec.Sealed()`).
- `ApplyAttempts(proposalID)` / `ReadApply(proposalID, attempt)` — read
  paths; a malformed attempt file is `ErrCorruptApplyRecord`, never
  silently skipped (docs/executor-adversarial.md row 9).

Two small, additive exports on existing `core` files, each a genuine reuse
rather than a new code path: **`core.ReadAndFingerprint`** (scan.go) simply
exports the existing private `readAndFingerprint` — `VerifyFreshness` only
ever compared a hash and discarded the body, but the executor needs the
full observed JSON itself to build an `ApplyResourceChange` request.
**`core.ApplyAfter`** (apply.go) factors out the same dot-path substitution
`FoldState` already performs internally (`dotSet`, unexported, shared) so
the executor can construct `PlannedState` from a `Modification.After` map
without a second implementation of "apply a dot-path diff onto a state
blob." Also added: `Ledger.Dir()`, a one-line accessor (previously
`dir` was fully private) — needed by `core/executor`'s own tests to
hand-corrupt an attempt file at its real path for row 9's test.

**`core/executor` (new package)**: `Applier` interface (embeds
`core.StateReader` for `Schema`/`Configure`/`ReadResource` — reused
unchanged for both `VerifyFreshness` and this package's own
reconcile-by-query — plus one new `ApplyResourceChange` method).
`TerminalError` wraps a provider error that must never be retried within
one `Ship` invocation (docs/executor.md's "terminal" classification); any
other error is treated as retryable and triggers reconciliation. `Ship`
implements the full loop docs/executor.md specifies: canonical
`(stack, type, name)` execution order (a real, independent sort of
`Delta.Modifies` — confirmed by reading `canonicalProposalBytes` that
stored array order is never actually guaranteed sorted, only a transient
hash-time copy is), per-resource freshness re-verification via
`core.VerifyFreshness` immediately before each resource's own attempt (not
once at the top), THE invariant (`in_flight` persisted before
`ApplyResourceChange` is called), reconcile-by-query via
`core.ReadAndFingerprint` + a same-package `reconciliationVerdict`
comparing observed state against `Modification.Before`/`After`'s dot-paths,
bounded reconciliation retries, and the full idempotency contract.

**A real refinement to the idempotency design, found while implementing,
not a contradiction of docs/executor.md**: the design doc's idempotency
table says "keyed by... every prior *sealed* apply record's final state."
Implementing crash recovery (rows 4/5) showed this needs to be looser in
one respect: a crashed, *never-sealed* attempt still durably recorded real
transitions (e.g. `in_flight`) before it died, and those are exactly what
a re-run must see to know reconciliation is needed at all. `foldResourceHistory`
therefore folds a resource's last-known state over **every** attempt file
for a proposal, sealed or not (`ApplyAttempts` already returns unsealed
ones too, by design) — only the `Parent` hash-chain link itself requires a
*sealed* predecessor. The doc's own intent ("reconciliation is always the
source of truth, not the transition log's own say-so") is unchanged; this
is a precise statement of *which* transitions feed that reconciliation
decision, not a new behavior.

**All ten rows of docs/executor-adversarial.md's program pass as real,
hermetic tests** (`core/executor/ship_test.go`, against a `fakeApplier`
whose resources are keyed by their own `"id"` attribute, mirroring how a
real provider identifies resources from within state rather than a
side-channel parameter):

1. Provider killed mid-apply — simulated via a generic transport-style
   error (`errors.New("connection reset")`), the closest hermetic proxy for
   "the RPC never resolved"; a literal `kill -9` is explicitly reserved for
   the later live-verification session against a real provider subprocess.
2/3. Timeout where the change did/didn't land — `TestShip_TimeoutWhereChangeLanded_ResolvesApplied`/
   `...DidNotLand_ResolvesFailed`, using a new `fakeApplier.scriptApplyTimeoutButLanded`
   that returns an error *while also* mutating live state as a side effect,
   modeling "the provider committed the change before/while the RPC itself
   failed" precisely (an earlier draft of this test pre-mutated state
   *before* calling Ship, which — caught by the test actually failing —
   was indistinguishable from an ordinary second-drift/staleness case
   rather than what row 2 means; fixed by moving the state mutation inside
   the scripted apply call itself).
4/5. Crash between the `in_flight` write and the call / between the call
   and the result write — both simulated by hand-writing an unsealed
   attempt file via the real `BeginApply`/`SaveApplyProgress` (never a
   fabricated shortcut), containing exactly the transitions a real crash at
   that point would leave, then calling `Ship` again and asserting the new
   attempt's `Parent` is `""` (attempt 1 was never sealed) and
   reconciliation — not the stale transition log — decides the outcome.
6a/6b. Retryable vs. terminal error classification — a terminal error skips
   reconciliation entirely (asserted directly: `len(ra.Reconciliation) == 0`).
7. Stale detected mid-partial-apply — a two-resource proposal where the
   second resource drifts a second time after acceptance but before ship;
   the first resource's `applied` transition stands, the second is refused
   before ever reaching `in_flight`, live state on the second is asserted
   untouched.
8. Double `ship` invocation racing — two goroutines calling `Ship`
   concurrently against the same proposal; asserted no attempt-number
   collision and every attempt file left on disk parses cleanly
   (`go test -race` clean too).
9. Apply record corrupted/truncated on re-run — hand-corrupted attempt
   file, asserted `Ship` returns `core.ErrCorruptApplyRecord` rather than
   guessing.

Plus two correctness tests beyond the named rows: `ErrAlreadyApplied` is a
genuine no-op (asserted no new attempt file is written), and the
per-resource retry budget (`maxApplyAttemptsPerResource`, a package var)
is enforced — a resource that fails terminally three times is refused a
fourth attempt entirely, staying `pending` with a budget-exhausted error
rather than calling `ApplyResourceChange` again.

**`core/apply_test.go` (new, package `core`)**: direct hermetic coverage of
`ApplyHash` (deterministic regardless of `Resources` order, changes with
content, excludes only `id`), `BeginApply`/`SealApply`'s attempt-numbering
and parent-chaining (including the sealed-vs-unsealed distinction above),
double-seal/save-after-seal rejection, `ApplyAttempts`'s ordering and
corruption handling, and `ApplyAfter`'s dot-path substitution.

`go build ./...`, `go vet ./...`, `gofmt -l .` (clean), and `go test ./...
-race` all pass across the whole repository — no regressions in any
existing package.

**Next slice** (per docs/plan.md's "Executor v1 (UBI-26)" wedge): provider
`ApplyResourceChange` wiring (v5 + v6, cty-msgpack lessons apply) — a
concrete `executor.Applier` backed by a real `provider.Client`, including
the redaction pass (`provider.Redact`) on `ApplyResourceChange`'s returned
attributes docs/schema.md's amendment requires, which this hermetic slice
correctly does not yet do (the fake provider's results are never
persisted-as-if-real). Then `ubx ship <proposal-id>` (CLI, exit codes 0/1/2
per docs/executor.md), then live verification against real drift on
`ubx-states`, including a real `kill -9` mid-apply.

## Current phase (previous)

**UBI-26 session 1 is done (this session, docs-only, Linear filed and
tracked): Phase 2 opens — the executor.** Filed as a new Linear issue
(`UBI-26`, ubiquex team) per the handoff's own instruction (no other ID was
inferred). v1 scope, pinned in the issue and in docs: shipping *accepted*
`drift_revert` proposals only — not the general `ApplyResourceChange` path
for every proposal kind (that stays deferred, docs/plan.md, until a real
resolver exists). This session is docs-only by design (per the handoff:
"design lands in docs BEFORE any code... commit session 1 as docs-only");
no code was written, no ubiquex-docs update was needed (nothing user-visible
shipped — no new CLI surface exists yet).

**Three new/amended documents, in the order the handoff specified:**

1. **docs/schema.md** — "Amendment: apply records (2026-07-17, UBI-26)."
   Pins a new hash-chained object family, `ledger/applies/<id>.apply.json`
   (own `schema_version`, own `ubx:apply:v1\n` hash domain — distinct from
   `Proposal`'s `ubx:proposal:v1\n`), chained two ways: `proposal` (which
   proposal this attempt executes) and `parent` (the previous *sealed*
   apply record for the *same* proposal — "" for attempt 1). Per-resource
   entries carry `address`, `transitions[]` (state + timestamp), `provider_result`
   (redacted — see below), `reconciliation[]`, and `errors[]`; a top-level
   `summary` carries the terminal outcome and per-resource counts.
2. **docs/executor.md** (new) — the failure-state machine spec:
   `pending → in_flight → applied | failed | unknown_post_timeout`;
   `unknown_post_timeout → reconcile-by-query (ReadResource) → applied |
   failed | still_unknown`, bounded retries. THE invariant: `in_flight` is
   durably persisted *before* `ApplyResourceChange` is called, never after.
   Freshness (`core.VerifyFreshness`, reused unchanged) is re-verified once
   per resource, immediately before *that* resource's own attempt — not
   once at the top of the whole run — so a multi-resource `drift_revert`
   (legal per docs/schema.md: "at least one `delta.modifies` entry," no
   upper bound) can correctly detect reality moving mid-partial-apply.
   `ubx ship` is idempotent by contract (table in docs/executor.md: `applied`
   skipped, `failed` retried within budget, `still_unknown`/`unknown_post_timeout`
   reconciled again before any new attempt, staleness-refused resources
   retried fresh).
3. **docs/executor-adversarial.md** (new) — the required-outcome program,
   ten rows (the handoff's nine named scenarios, with the error-taxonomy
   row split into 6a/6b for retryable vs. terminal, since they have
   genuinely different required outcomes and deserve separate rows rather
   than one row asserting two different things). Written in the handoff's
   own words as "the future published reliability report" — each row states
   a claim `ubx` makes about its own behavior under a specific injected
   failure, not just a test-plan checklist. A closing section names what's
   explicitly *not* yet covered (sub-process-kill-granularity disk faults,
   indefinitely-hanging providers, clock skew) rather than silently implying
   the nine/ten rows are failure-space-complete.

**Two real design findings, resolved and documented rather than glossed
over — exactly the "stop and flag anything that fights the existing schema"
instruction, though both turned out resolvable without contradicting
anything already ratified:**

1. **`Proposal.status` can never be rewritten to `applied`/`partially_applied`
   in place.** `core.Ledger.Append` enforces ledger-entry immutability
   structurally (`ErrDuplicateProposal`; there is no update path anywhere
   in this codebase for an already-written proposal file) — and nothing
   about that invariant should change just because the executor now
   exists. Resolved by making `applied`/`partially_applied` **derived,
   reported** values only: any consumer wanting a proposal's effective
   status folds the most recent *sealed* apply record's `summary.outcome`
   over the proposal's stored, immutable `accepted` status — the exact
   same "immutable history, current truth computed by folding over it"
   posture `core.FoldState`/`core.Ledger.Chain` already establish, applied
   one level up (proposal → its own apply history, not just address →
   proposal chain). The stored `.prop.json` file's `status` field is never
   touched again after `Accept` writes it once. Fully written up in
   docs/schema.md's own amendment.
2. **`ApplyResourceChange_Request` (checked directly against the real
   `tfplugin5`/`tfplugin6` generated Go structs, not assumed) requires
   `PriorState`, `PlannedState`, *and* `Config`** — all cty-msgpack
   `DynamicValue`, matching UBI-7's own encoding lessons. Real Terraform
   usage always derives `PlannedState` via a separate `PlanResourceChange`
   call that resolves provider-side defaults/unknowns. `drift_revert`'s
   narrow shape — every restored value is already concrete, already
   observed once, never a placeholder — is exactly what lets v1 skip a
   distinct plan phase: `PlannedState` is constructed directly as "the
   freshly re-verified `PriorState`, with `Modification.After`'s dot-path
   values substituted in," the same substitution `tfwrite.ApplyModification`
   already performs for `.tf` writeback, just producing a JSON value to
   encode instead of an HCL edit. Stated explicitly as a v1-scope shortcut,
   sound only for `drift_revert`: a future resolver-driven `change`/`revert`
   kind will need a real plan phase and cannot reuse this substitution.

**Also pinned, both cross-cutting reuses rather than new mechanisms**:
`provider_result` (whatever `ApplyResourceChange` returns) MUST go through
the same `provider.Redact` schema-flags-plus-override-table union
(UBI-23/24) and the same per-ledger salt before ever being written into an
apply record — secrets are exactly as reachable through an apply as through
a read. Concurrent `ubx ship` invocations against the same proposal reuse
the existing `.ubx/lock` PID-file lock (`core.acquireLedgerLock`, built for
UBI-20's `Append` contention) for attempt-number assignment, the same class
of check-then-write race closed one level up (per-proposal rather than
per-stack) — never a second lock file invented for this.

**"Delta order" pinned precisely, a real gap in the existing code surfaced
while writing this**: `core.canonicalProposalBytes`/`sortDeltaElements` only
sorts a *transient decoded copy* of `Delta.Modifies` when computing the
hash — it never mutates the `Proposal` struct's own field, so a proposal's
*stored* `delta.modifies` array order is not guaranteed to already be
`(stack, type, name)`-sorted. docs/executor.md pins that `ubx ship` must
independently re-sort `Delta.Modifies` the same way before iterating,
rather than trusting stored array order — otherwise "serial, delta order"
would be an unenforced claim. This is a session-2+ implementation note, not
a code change made this session (session 1 is docs-only, per the handoff).

**Linear**: `UBI-26`, "Executor v1: ship drift_revert — failure-state
machine + apply records," filed against team `ubiquex`, full handoff scope
recorded in the issue description. Left in its default `Backlog` state
(not moved to Done — session 1 of a multi-session ticket; will move once
the full v1 slices land).

## Current phase (previous)

**UBI-25 is done (this session, Linear-verified): read-only MCP server —
`ubx` as assistant tools.** Design landed first in docs/architecture.md
(new "MCP server" section) and docs/plan.md, per session protocol.

**One verb, one binary**: `ubx mcp` — a cobra subcommand like every other
verb, not a second `cmd/ubx-mcp` executable. Justified plainly: a second
binary would mean goreleaser building/signing/publishing a second
artifact, a second install-docs entry, a second `PATH` concern, for a
command that's otherwise just another `ubx` invocation that happens to
block on stdio instead of running once and exiting.

**Three tools, wrapping the existing `--json` contract, not a parallel
API**: `ubx_why`/`ubx_status`/`ubx_scan`, each a thin wrapper over new
`computeWhyJSON`/`computeStatusJSON`/`computeScanJSON` functions
(`cli/mcp_why.go`/`cli/mcp_status.go`/`cli/mcp_scan.go`) that hold the
exact same "do the work, build the payload" logic the CLI's own
`--json` code path already runs — same `whyJSON`/`statusJSON`/`scanJSON`
struct types, same underlying `core.Ledger`/`core.RunScan`/
`core.GenerateProposal`/`attributeDrift` calls, never a different
computation or JSON shape. The existing cobra `RunE` handlers for
`why`/`status`/`scan` are untouched (their own human-text and `--json`
paths keep working exactly as before) — the MCP handler is a second,
independent caller of the same primitives. One small, mechanical,
pre-existing signature change was needed to make this possible:
`runVerifyAcceptance` took a `*cobra.Command` purely for `.Context()`;
now takes `context.Context` directly, its only real caller (`cli/why.go`)
updated accordingly, behavior unchanged.

**Boundary by omission, stated plainly, not left to be inferred**: `ubx
accept`/`ship`/`writeback`/`revert-plan` (and `scan --surface-as`, which
opens a real GitHub issue/PR) are not exposed as tools. Accepting a
proposal is a recorded human (or PR-merge-derived) decision, never
something an assistant does mid-conversation. Both `ubx mcp --help` and
the new docs page say this directly.

**Tool descriptions written as the model's own UX**: each of the three
names plainly what question it answers ("who changed this and when,"
"what's the current fleet, optionally checked against live state," "does
this one resource's live state match the ledger") and what its receipt
means — not a mechanical parameter list. Every input field carries the
same descriptive weight `--help` text gives the CLI flag it mirrors.
Config discovery is unchanged from every other `ubx` command: nearest
`.ubx/config` from the server process's own working directory (UBI-19) —
an MCP client pointed at a real ledger checkout gets the same defaults a
human sitting there would; `ledger_dir` (and friends) stay explicit tool
inputs regardless, since a server has no ambient shell state to fall back
on the way a human's session might.

**A real, load-bearing SDK gotcha, found by actually calling the tools
over the real protocol, not assumed safe from the Go types alone**:
`mcp.AddTool`'s automatic output-schema generation (via reflection) infers
"array" from `json.RawMessage`'s underlying `[]byte` — but
`core.Proposal` uses `json.RawMessage` throughout for canonical-JSON
encoding (`CostDelta.MonthlyUSD`, `Modification.Before/After`,
`ResolutionInput.Lookup`, ...), and the real runtime value is often a
JSON *object* (a lookup key, say), which then fails the generator's own
output validation — a real, reproducible crash
(`session.CallTool` panicking with `"type: ...has type \"object\", want
one of \"null, array\""`), caught during this session's own hermetic
smoke-testing before it ever reached a real client. Fixed by using `any`
as every tool handler's output type — the SDK's own doc comment names
this exact escape hatch ("if the output type is 'any', no output schema
is generated"); the payload is still returned as structured JSON content
exactly as before, just unvalidated against a schema that couldn't
correctly describe canonical-JSON's own "anything, by design" fields in
the first place.

**Hermetic tests** (`cli/mcp_test.go`, connected via
`mcp.NewInMemoryTransports` — never a real stdio subprocess): tool
listing (all three present, every mutating verb confirmed absent),
`ubx_why` by resource address and by proposal ID, the adversarial cases
this ticket named explicitly (unknown address, no ledger, drift without
provider — every one surfaces as `IsError: true` with a real message,
not a panic or a silent empty result), `ubx_status` ledger-only and
drift-checked, `ubx_scan` on a never-seen resource (with a follow-up
`ubx_status` call confirming it was never accepted — `ubx_scan` must
never mutate the ledger), and a tool-description substantiveness check.

**Live-verified against the real `ubx-states` ledger**
(`/Users/roozbeh/demo/payments`, the same real ledger/S3 bucket this
project's own live-verification work has used since UBI-9/UBI-10): tagged
the real bucket (`purpose=ubx-mcp-live-verify`), scanned it for real
(`--source hashicorp/aws`, real CloudTrail attribution enabled) against
the ledger, accepted the resulting `drift_adopt`, then — using a real MCP
client (the same `github.com/modelcontextprotocol/go-sdk`, over a real
stdio subprocess, `mcp.CommandTransport`, not the in-memory harness the
hermetic tests use) — called `ubx_why` for real, asking "who changed this
bucket and when." The real response named the real IAM identity
(`arn:aws:iam::839333509514:user/roozbeh`), a real `PutBucketTagging`
event, and a real timestamp — alongside an honestly `cloudtrail_unattributed`
drift from an earlier session (reason `delivery_window`), preserved in
the same chain. `claude mcp add`/`list`/`remove` were also verified for
real (a scratch project, a real built `ubx` binary on `PATH`, `✔
Connected`) before writing the docs page's own client-setup instructions
— not assumed correct from memory of the command's shape. Bucket restored
to untagged afterward (`aws s3api delete-bucket-tagging`, confirmed via
`get-bucket-tagging` → `NoSuchTagSet`). The demo ledger's own
last-recorded state now includes this session's real tag as "current" —
deliberately not scanned again to record the cleanup itself, matching
every prior live-verification session's own convention (the next real
scan against this bucket will correctly detect it's untagged again, drift
detection doing exactly its job).

ubiquex-docs updated same session: new `guides/mcp.mdx` ("ubx + your AI
assistant") with the real transcript above (trimmed to the two newest
CloudTrail events of 24 real matches, for readability), Claude Desktop
config JSON and a verified `claude mcp add` command, the boundary-by-
omission stance, and the full `--help` text; cross-links added from
`cli/why.mdx`/`cli/status.mdx`/`cli/scan.mdx`. `mint validate`/`mint
broken-links` both pass clean.

## Current phase (previous)

**UBI-24 is done (this session, Linear-verified): sensitive-override
table, closing UBI-22's own `helm_release` redaction gap.** This gates
the `v0.2.0` tag. Design landed first in docs/schema.md (a short
amendment to the existing `$redacted` section) and docs/architecture.md
(new "Sensitive overrides" section), per session protocol.

**The union model**: `provider.Redact` now consults a small, ubx-owned
override table (`provider/overrides.go`'s `SensitiveOverrides`, `(source,
type, path) → force-redact`, the same "data, not code" posture
`core/lookuphints` already established) alongside the provider's own
schema `Sensitive` flags — the schema is a floor, never a ceiling.
Redaction is the union of both; the table can only ADD an attribute to
what gets redacted, never remove one the schema itself flags (checked
directly: an attribute both schema-`Sensitive` and override-listed is
redacted once, the override pass recognizes an already-`$redacted` value
and leaves it alone rather than re-hashing it). Seeded from UBI-22's own
finding: `helm_release.manifest`/`metadata.values`/`metadata.notes`.

**A real audit, run directly not asserted**: checked every one of the
~20 registered `kubernetes_*` types plus `helm_release` for computed
attributes whose name suggests an echo of rendered/free-form input.
Found no further candidates on either side — every genuinely computed
`kubernetes_*` attribute is structural/identity data the provider itself
derives (a few "message"/"values"-substring matches turned out to be
config enums like `termination_message_policy`, not real hits); Helm's
own schema had nothing past the three already seeded.

**A precise correction to how the UBI-22 finding was originally
described**: `helm_release.metadata` is NOT a real `NestedBlock` the way
`kubernetes_*`'s own `metadata`/`spec` are — checked directly against the
schema, it's a plain `Attribute` whose `Type` is the compound cty type
`list(object({...}))`. tfplugin's `Sensitive` flag is a single bool per
top-level `Attribute` — there is no wire-protocol mechanism at all to
flag one sub-field of a compound-typed attribute without flagging the
entire thing (losing `metadata`'s useful non-sensitive fields in the
process). This is exactly why the override table operates on the
*decoded JSON tree* directly (`redactOverridePath`, walking dot-notation
paths, generalizing the same "if the current node is an array, apply the
remaining path to every element" rule `provider.Redact`'s own schema-driven
List/Set handling already uses) rather than on `Block`/`Attribute`/
`NestedBlock` at all — it needs no schema cooperation and isn't blocked
by any limitation of the wire protocol's own attribute model. `manifest`,
by contrast, is a plain string attribute with no such limitation —
flagging it `Sensitive` upstream would have been straightforward.

**Plumbing**: `provider.Redact` gained two new leading parameters,
`source, typeName string` (the same `(provider source, type)` key
`conformance.Registry`/`core/lookuphints` already use, UBI-21).
`typeName` was already available at the call site; `source` was not —
`cli/stateadapter.go`'s `stateReaderAdapter` and `conformance/harness.go`'s
own copy each gained a `source` field, threaded from the same `--source`
string already used for `ScanRequest.ProviderSource`. All four
`newStateReader` call sites (`cli/scan.go`, `cli/scanall.go`,
`cli/status.go`, `cli/accept.go`) and `conformance.AdoptMutateScanDiffConfig`
(a new `Source` field) updated accordingly.

**Hermetic tests** (`provider/overrides_test.go`): override-only
attribute redacted (no schema flag at all), override+schema-flag union
(both redacted, neither skipped), a nested path inside a real
list-wrapped block (`metadata.values`, matching the exact `helm_release`
shape), the same path applied across every element of a multi-item list
(not just index 0), a non-matching `(source, type)` left completely
untouched, and the already-redacted-value skip. Plus a permanent
regression guard on the real seed data itself
(`TestSensitiveOverrides_HelmReleaseSeeded`).

**Live-verified on a real, local `kind` cluster** (created and destroyed
for the run, `kind create cluster`/`kind delete cluster`): a real
`helm_release` with a secret-looking value (`--set-string dbPassword=...`,
standing in for a real `set_sensitive` value — what actually lands in
the release's own resolved state is identical either way, since
Terraform's `set_sensitive` only hides the value from Terraform's own
plan output, not from what Helm itself stores) adopted cleanly with
`metadata.values`/`metadata.notes` both `$redacted`; the generated
proposal file grepped by hand for the real secret string — zero matches.
Rotated the value via a real `helm upgrade`; the drift-path proposal
showed `before`/`after` both `$redacted` at different hashes, grepped
again for both the old and new real values — zero matches, both times.
`ubx why` (human and `--json`) confirmed clean too. A new permanent
conformance test, `TestConformance_HelmReleaseSensitiveOverride`
(gated `UBX_CONFORMANCE_LIVE=1` + `requireKubeContext`), captures this
exact sequence as a repeatable regression check — run for real against a
fresh cluster to confirm it passes, not just written and assumed. The
pre-existing `TestConformance_HelmRelease` also gained `Source:
"hashicorp/helm"` (a real gap: it hadn't been exercising the override
path at all until this session, since `AdoptMutateScanDiffConfig.Source`
didn't exist before UBI-24).

**A draft (unsubmitted) upstream issue** for `terraform-provider-helm`
is saved at `docs/upstream/helm-sensitive-flags.md` — documents the gap,
a real reproduction, and several fix options (flag `manifest` directly;
flag all of `metadata`; restructure it into a real nested block; or, at
minimum, document the limitation). Submitting it is explicitly Roozbeh's
call, not done as part of this session.

ubiquex-docs updated same session: `concepts/secrets.mdx` gained a new
"The provider's schema is a floor, not a ceiling" section (the exact
"we do not treat upstream flags as the ceiling" framing); `cli/scan.mdx`'s
own Helm section had its example transcript and `Warning` corrected —
they previously showed `metadata[0].values` as *raw* JSON and described
the gap as still-open; now, correctly, redacted (unconditionally, since
the override applies regardless of what a given chart's values actually
contain) and closed. `mint validate`/`mint broken-links` both pass clean.

## Current phase (previous)

**UBI-22 is done (this session, Linear-verified): Kubernetes support —
the first non-cloud-provider provider, both stages completed.** Design
landed first in docs/architecture.md ("Kubernetes support") and
docs/schema.md (new `k8s_audit`/`not_configured` amendment), per session
protocol.

**Stage 1 (hermetic — schema-only verification, no cluster needed):**

1. **`hashicorp/kubernetes` (2.35.1, 82 resource types) and
   `hashicorp/helm` (2.17.0, 1 resource type) both verified via
   `provider.Acquire`: both negotiate tfplugin **v5** — dual v5/v6
   support earning its keep a third time, matching AWS/GCP.
2. **A real, empirically-confirmed schema-shape finding**: every
   `kubernetes_*` type checked models `metadata` (and, for workload
   types, `spec`) as `NestingList`, not `NestingSingle` — a real
   SDKv2-era "one-item list simulates an optional single block"
   convention, confirmed by checking that `timeouts` (present on several
   of the same types) IS `NestingSingle`. `helm_release`, by contrast,
   has NO such nesting — flat `id`/`name`/`namespace`, a genuinely
   simpler AWS/GCP-shaped identity. Also found: several `kubernetes_*`
   types exist in both a bare (`kubernetes_secret`) and `_v1`-suffixed
   (`kubernetes_secret_v1`) form with byte-for-byte identical schemas;
   only the `_v1` forms (the provider's own recommended naming) were
   seeded, to avoid duplicate registry entries.
3. **The explicit UBI-23 cross-check, verified not assumed**:
   `kubernetes_secret_v1.data`/`binary_data` are both confirmed
   `Sensitive: true` in the real schema — had they not been, UBI-22
   would have needed its own type-level redaction override (a stop-and-flag
   design decision). No upstream gap, no override needed.
   `kubernetes_config_map_v1`'s `data`/`binary_data` are correctly NOT
   `Sensitive` (ConfigMaps are Kubernetes' own non-secret counterpart to
   Secrets). `helm_release.set_sensitive` (a `NestingSet` block) is the
   first real *Set*-nested sensitive value found in any
   currently-integrated provider's schema — `provider.Redact`'s
   Set-handling branch existed already (UBI-23) but had no real-schema
   exercise until now.
4. **A disclosed limitation, found reading `helm_release`'s schema**:
   `manifest` (rendered chart YAML) and `metadata[0].notes`/
   `metadata[0].values` are NOT `Sensitive`-flagged, even though a chart
   template can render a `set_sensitive` value directly into them.
   Schema-level `Sensitive` flags mark the input attribute only, not
   everywhere it might get echoed into a derived, computed text blob — a
   real, meaningful boundary of schema-driven redaction as a strategy,
   confirmed concretely (not just predicted) once Stage 2 showed
   `metadata[0].values` is, in fact, the field a real Helm values-drift
   actually surfaces through (see Stage 2, below).
5. **~20 `kubernetes_*`/`helm_release` types seeded into
   `conformance.Registry`** (`Source: hashicorp/kubernetes`/
   `hashicorp/helm`), `IdentityFields: ["id"]`, `Safety: FakeOnly`,
   `Implemented: false` — mirroring UBI-9/UBI-21's own bootstrapping.
6. **`k8saudit/` package**: a third `core.EventLookup` backend (EKS
   control-plane audit logs — Kubernetes' own `audit.k8s.io/v1` event
   schema — delivered to CloudWatch Logs, queried via `FilterLogEvents`).
   Dispatched in `cli/attribution.go`'s `newAttributionBackend` by
   `ProviderSource == "hashicorp/kubernetes"/"hashicorp/helm"` — **not**
   by resource type, since an EKS cluster's own drift (`aws_eks_cluster`,
   scanned via `hashicorp/aws`) must stay exactly as
   CloudTrail-attributable as before. New, entirely optional
   `.ubx/config` `[k8s_audit]` table (`cluster`/`region`/`log_group`) —
   the one config table with no CLI flag equivalent, since (unlike AWS's
   region or GCP's project) there's nothing to derive "which cluster"
   from. Unconfigured (`cluster == ""`) degrades to
   `audit_unattributed`/`not_configured` (new `core.ReasonNotConfigured`,
   additive), via a sentinel error (`errK8sAuditNotConfigured`) —
   `attributeDrift`/`newAttributionBackend` never block, matching the
   best-effort-attribution posture every other backend already has.
7. **Hermetic tests**: `k8saudit/client_test.go` (real K8s audit-event
   JSON → `core.CloudTrailEvent` parsing, including the defensive
   multi-candidate `Resources` building), `cli/k8sattribution_test.go`
   (dispatch: not-configured for both providers, configured dispatches
   correctly, AWS regression guard), `cli/config_test.go` additions
   (`[k8s_audit]` parsing, absence → zero value).

**Stage 2 (needed a real cluster — a local `kind` cluster, free/instant,
was used for conformance; a real EKS cluster was judged out of scope for
the audit-log leg, see below):**

1. **A real correction to the Stage-1 hermetic guess, not just a
   confirmation** — worth stating exactly like that, not softened:
   Stage 1 reasoned that `metadata` being `NestingList` would require
   `--lookup` shaped `{"metadata": [{"name": ..., "namespace": ...}]}`.
   **Live against a real (`kind`) cluster, this turned out unnecessary**:
   `{"id": "<namespace>/<name>"}` alone is sufficient for every
   `kubernetes_*` type tested — the provider's own `ReadResource` parses
   `id` internally. Cluster-scoped types (`kubernetes_namespace_v1`) use
   the bare name, no prefix. `helm_release` is the reverse case: `id`
   alone is NOT sufficient — the confirmed shape needs `id`+`name`+
   `namespace` together.
2. **Five `kubernetes_*` kinds + `helm_release` live-verified end to
   end** (adopt→mutate→scan-diff, via the same `RunAdoptMutateScanDiff`
   harness UBI-9/UBI-21 used), against a real, local `kind` cluster
   (created via `kind create cluster`, Docker Desktop already
   installed/running; `kind`/`helm` CLIs installed via `brew`):
   `kubernetes_config_map_v1`, `kubernetes_secret_v1`,
   `kubernetes_deployment_v1`, `kubernetes_service_v1`,
   `kubernetes_namespace_v1`, `helm_release` — all promoted to
   `RealSafe` in `conformance/registry.go`, plus a new, permanent
   `conformance/k8s_live_test.go` (gated `UBX_CONFORMANCE_LIVE=1` +
   `requireKubeContext`, which skips rather than fails if no
   `kubectl` context is configured — `ubx` itself never creates/destroys
   clusters).
3. **`kubernetes_secret_v1` end to end, the critical redaction
   cross-check**: adopted a real Secret (`data` correctly redacted to a
   `$redacted` marker); re-scanning against the unrotated secret reported
   no drift (same salt, same value, same hash); rotating the real secret
   (`kubectl patch ... stringData`) correctly fired drift, `before`/`after`
   both `$redacted` at different hashes; the generated proposal file was
   grepped by hand for the real secret string AND its base64 encoding,
   both before and after rotation — zero matches, every time. `ubx why`
   rendered `(redacted)` correctly on both sides; `--json` carried the
   same marker, no material.
4. **`helm_release` adopt + a real values-drift**: adopted a real
   release (a throwaway `helm create`-generated chart); a real
   `helm upgrade --set replicaCount=3` correctly showed up as
   `metadata[0].values` changing from `{"replicaCount":1}` to
   `{"replicaCount":3}` in the generated `drift_adopt` proposal — **the
   top-level `values`/`chart` attributes stayed `null` throughout**, since
   the provider never backfills them from a live read; `metadata[0].values`
   is the field that actually carries a values-drift signal. This
   directly confirms the disclosed redaction limitation above is
   concrete, not hypothetical: neither `metadata[0].values` nor
   `manifest` is `Sensitive`-flagged, so this exact mechanism would
   surface a `set_sensitive` value in plaintext if a chart rendered it
   into output.
5. **A real, if minor, finding worth naming**: any `kubernetes_*`
   mutation always shows a `metadata` change alongside the semantic one,
   since every real mutation bumps `resourceVersion` and `metadata`
   (a whole `NestingList` value) is compared atomically — not a bug, but
   worth knowing before it reads as unrelated noise in a diff.
6. **The EKS audit-log leg was deliberately not attempted, recorded
   honestly rather than silently skipped**: `aws eks list-clusters`
   confirmed no EKS cluster already exists in the account (both regions
   checked); provisioning one — a real, hourly-billed, ~15-20-minute
   piece of cloud infrastructure, categorically more consequential than
   the free/instant local `kind` cluster used above, or the
   Secrets-Manager-secret/IAM-access-key created and destroyed in
   seconds during UBI-23's own live verification — was judged out of
   proportion to attempt autonomously. `k8saudit.Client.LookupEvents`'s
   defensive `Resources`-candidate-building (offering `objectRef.name`,
   `objectRef.namespace + "/" + objectRef.name`, `objectRef.uid` all at
   once) was validated as far as a local cluster allows: `id`'s
   confirmed `<namespace>/<name>` shape (finding #1 above) IS one of the
   candidates this backend already builds — so the mechanism is believed
   sound, but a real EKS audit event was never actually correlated
   against it. `k8saudit.Backend.DeliveryLag` ships as a documented,
   conservative placeholder (5 minutes) pending that measurement, stated
   plainly as unmeasured.
7. **A live-side finding along the way**: `hashicorp/helm`'s own
   provider-level config nests its Kubernetes connection inside a
   `NestingList` block too (`kubernetes = [{config_path=..., ...}]`) —
   confirmed directly against the real schema, the same list-wrapping
   convention `kubernetes_*`'s own `metadata` uses, this time at the
   provider-config layer rather than a resource schema.

Every hermetic test (`go test ./...`) and all six live conformance tests
(`UBX_CONFORMANCE_LIVE=1 go test ./conformance/...`, against a real `kind`
cluster created and destroyed for the run) pass. `gofmt -l .`/`go vet
./...` clean. ubiquex-docs updated same session: Kubernetes/Helm sections
on `cli/lookup.mdx`/`cli/scan.mdx`, a new `[k8s_audit]` subsection on
`cli/config.mdx`, the `not_configured` reason and a new "Kubernetes and
Helm: EKS audit logs" section on `concepts/attribution.mdx`, a
cross-reference on `concepts/secrets.mdx`, and provider/credential
mentions on `getting-started/installation.mdx` — every transcript real
(captured from the actual built binary against the real `kind` cluster
during Stage 2, not hand-written). `mint validate`/`mint broken-links`
both pass clean.

**A pre-existing bug caught and fixed while working in this area, unrelated
to UBI-22's own scope**: an earlier UBI-23 session edit to docs/schema.md
had accidentally deleted the `## Canonical hashing — RATIFIED v1` heading
line itself (an old_string/new_string replacement that consumed the
heading without restoring it). Found by `grep`ping for the heading while
inserting this session's own new amendment nearby, and fixed as part of
this session's first docs commit.

## Current phase (previous)

**UBI-23 is done (2026-07-17): redact provider-`Sensitive`
attributes in observed state — secrets must never enter the ledger.**
Design landed first in docs/schema.md (new "`$redacted` value encoding"
amendment) and docs/architecture.md (new "Secrets" section), per session
protocol.

**Mechanism**: redaction happens at the `core.StateReader` adapter
boundary (`cli/stateadapter.go`'s `stateReaderAdapter`,
`conformance/harness.go`'s own copy) — the one place that still holds the
concrete `*provider.Schema` (hence its `Sensitive` flags) before it's
type-erased to `core.StateReader`'s opaque `any`. New `provider.Redact(block,
salt, observed)` walks `Block.Attributes`/`Block.NestedBlocks` (dispatched
on `NestingMode`, mirroring `ctyvalue.go`'s own encode-side shapes) and
replaces every `Sensitive`-flagged value wholesale with `{"$redacted":
{"sha256": "<salted hash>"}}`. `core` itself gains only two things: a
small `$redacted`-shape recognition helper (`core/redacted.go`:
`IsRedactedValue`/`CountRedacted`, plus an internal `isRedactedMarker`) and
a fix to `core/state.go`'s `diffObjects` so it treats a `$redacted` object
as atomic rather than recursing into it — `core` never learns what
"sensitive" means, only the resulting JSON shape, preserving the
core/provider zero-import boundary (the same wire-convention pattern
`IntentSource.Kind` string literals already establish). `FoldState` needed
no changes at all.

**Salt**: new `core/salt.go`'s `Ledger.Salt()` — per-ledger-directory,
`.ubx/salt`, generated via `crypto/rand` on first use, `0600`, and ensures
a `.gitignore` entry for it exists (creating a minimal `.gitignore` if
none exists, appending the line otherwise). All four `newStateReader`
call sites (`cli/scan.go`, `cli/scanall.go`, `cli/status.go`,
`cli/accept.go`) now fetch the ledger's salt and pass it through;
`conformance/harness.go`'s `RunAdoptMutateScanDiff` does the same.

**Verified against real provider schemas, not assumed** (the task's own
instruction): a throwaway introspection tool against real `hashicorp/aws`
6.54.0 and `hashicorp/google` 7.40.0 found nested sensitivity is common,
not hypothetical — 115 nested (of 131 top-level) for AWS, 207 nested (of
46 top-level) for GCP, up to depth 4/3, including a whole `List`-typed
attribute marked sensitive as one unit
(`aws_elasticache_user.authentication_mode.passwords`) and
non-obviously-secret field names also flagged sensitive
(`aws_quicksight_data_source`'s `credential_pair.username` alongside
`password`). The existing `Block`/`NestedBlock` model (built for
cty-msgpack encode/decode, UBI-7) already correctly surfaces all of this
via `BlockTypes`-based nesting — no schema-translation change needed. A
real gap was checked directly rather than assumed away: `tfplugin6.Schema_Attribute`
has an unread `NestedType` field (the modern terraform-plugin-framework
nested-attribute mechanism); confirmed both integrated providers negotiate
wire protocol **v5** (matching this project's own standing "dual v5/v6"
finding), and `tfplugin5.Schema_Attribute` has no `NestedType` field at
all — it's architecturally impossible to encounter with either provider
`ubx` supports today. Flagged, not silently dropped: a future v6-negotiating
provider would need `blockFromV6` extended to also read `NestedType` first.

**Per-command behavior**: `writeback`/`revert-plan` (via
`tfwrite.ApplyModification`) decline any redacted `Modification.After`
value unconditionally, before ever attempting to resolve/render it —
never handing a `$redacted` marker to `hclwrite.TokensForValue`. `why`
gained a new `renderModifies` (both the single-proposal and
resource-address chain views) rendering `change: <addr>: <path>: <before>
-> <after>` for every kind carrying a real delta — not previously
rendered at all — with `(redacted)` substituted for a `$redacted` value,
reusing `revert-plan`'s own `rawOrAbsent` helper (now redaction-aware).
`scan --all`'s batch summary gained a third count,
`N attribute(s) redacted`. `--json` needed zero code changes anywhere:
every payload already marshals the real, already-redacted `*core.Proposal`.

**A real, checked scope boundary found while writing the adversarial
test, not silently swept under**: `resolution.inputs[].lookup` (the UBI-7
follow-up field) is populated straight from `ScanRequest.CurrentState` in
`core.RunScan`, independent of the adapter-layer redaction path — it is
never redacted. An early version of the adversarial test put the fake
sensitive value directly into `--lookup` and caught the raw value showing
up unredacted in the generated proposal file. Checked against real schemas
before "fixing" anything: across every type in `conformance/registry.go`
(AWS and GCP), no identity/lookup attribute is ever `Sensitive`-flagged —
real lookup keys are `id`/`name`/`arn`/`bucket`, never a credential.
Redacting `lookup` unconditionally would break `VerifyFreshness`'s
re-read (a redacted marker can't be re-supplied as a working identifier)
to guard against a scenario no real schema produces. Scope boundary
recorded in docs/architecture.md's own subsection; the test itself was
corrected to the realistic shape (`--lookup` carrying only `id`
throughout) and now asserts the persisted lookup is exactly
`{"id": "..."}`, permanently guarding this boundary.

**Adversarial tests, all hermetic except the one explicit live check**:
`provider/redact_test.go` (top-level/nested block/list/set/map,
whole-value-regardless-of-type, missing-attribute-skipped,
same-salt-same-value determinism, different-salt-different-hash),
`core/redacted_test.go` + `core/salt_test.go` (marker recognition, count,
generate/persist/reread, `.gitignore` creation/append/no-duplicate,
salt-loss-regenerates-different), `core/state_test.go` additions
(`diffObjects` atomic-not-recursive both directions, a full
adopt→drift→fold chain over redacted values with drift firing only on a
real hash change), `tfwrite/tfwrite_test.go` (decline path, byte-identical
file, no hash/marker written), `cli/redact_test.go` (full CLI adoption +
drift-both-directions + `why`/`--json` rendering + writeback decline +
`scan --all` redaction-count summary, all via a new `provider/internal/fakeprovider`
knob, `FAKEPROVIDER_SENSITIVE_ATTRS`).

**Live-verified against the real AWS account** (`839333509514`,
`arn:aws:iam::...:user/roozbeh` — same account every prior live session
used), by hand via the actual built binary, not a permanent gated test
(judged out of proportion to this ticket's scope — `aws_secretsmanager_secret_version`
isn't in `conformance/registry.go`, and adding/promoting a new type there
is UBI-9/UBI-18-shaped work, not this one's mandate): first tried
`aws_iam_access_key` (the task's own suggested example) and found a
genuinely interesting negative result — AWS never returns an IAM access
key's `secret` on an ordinary read after creation (only at
`CreateAccessKey` time), so `ubx scan` structurally can never observe it
at all; nothing to redact because there's nothing to see. Switched to a
real `aws_secretsmanager_secret_version` (created, rotated via
`put-secret-value`, destroyed after): adoption's `delta.creates.state.secret_string`
came back a real `$redacted` marker; `grep`ping the generated proposal
file for the real secret string, both before and after rotation, found
zero matches both times; re-scanning against the unrotated secret
reported no drift; after rotation, drift fired with `before`/`after` both
`$redacted` at genuinely different hashes; `ubx why` rendered
`(redacted)` on both sides, and `--json` carried the same marker with,
again, zero matches for either real secret value. Account confirmed left
exactly as found (secret deleted without recovery, IAM access keys back
to only the pre-existing one).

ubiquex-docs updated same session: new `concepts/secrets.mdx` (added to
nav), `cli/scan.mdx`/`cli/writeback.mdx`/`cli/revert-plan.mdx`/`cli/why.mdx`
gained redaction examples and cross-links (all transcripts regenerated
against the actual built binary, not hand-written), `cli/lookup.mdx`
gained the lookup-never-redacted note. Two pre-existing `cli/why.mdx`
examples and two pre-existing `cli/scan.mdx --all` summary-line examples
were also regenerated/corrected, since `why`'s new `renderModifies` and
`scan --all`'s new summary count are both real, unconditional output
changes (not redaction-specific) that made the old committed transcripts
stale the moment this session's code shipped. `mint validate`/`mint
broken-links` both pass clean.

## Current phase (previous)

**UBI-21 is done, both stages, this session (Linear-verified): GCP
support, the first cross-provider generalization.** Design landed in
docs/architecture.md ("GCP support (UBI-21)") and docs/plan.md before
code. Stage 1 (hermetic) and Stage 2 (needs a real GCP account) were
both originally scoped to possibly span sessions — Stage 2 ran this same
session once Roozbeh set up Application Default Credentials against his
own `personal-273114` GCP project (billing already enabled) partway
through, at the "Decide Stage 2 feasibility with user" checkpoint.

**Design decisions (both made before any code, see docs/architecture.md
for the full reasoning):**

1. `core.Address`/`--lookup` stay exactly as they were — a resource's
   identity is still an opaque, provider-agnostic lookup JSON, no new
   "provider" field. What generalizes is the KNOWLEDGE `ubx` keeps about
   specific types (`conformance.Registry`, `core/lookuphints`) — both
   re-keyed from bare type name to **(provider source, type)**, since a
   second provider makes "there's only one provider" an assumption worth
   naming rather than leaving implicit.
2. Attribution backends become per-platform packages behind the existing
   `core.EventLookup` interface (already shape-agnostic: one method,
   `LookupEvents(resourceID, since, until)` — **held up with zero
   interface changes**, confirmed once `gcpaudit/` was actually built,
   not just assumed). A new `gcpaudit/` package (against GCP Cloud Audit
   Logs) plus a small provider-source→backend registry
   (`cli/attribution.go`'s `newAttributionBackend`); `docs/schema.md`
   gained a purely-additive `gcp_audit`/`audit_unattributed` pair
   (`cloudtrail`/`cloudtrail_unattributed` unchanged, still what
   `cloudtrail.Backend` emits — no back-compat risk to AWS's existing
   output at all).

**Stage 1 work (hermetic — no GCP account touched), four commits:**

1. **(Provider source, type) keying refactor.** `conformance.TypeSpec`
   gains `Source string`; all 51 existing AWS entries migrated to
   `Source: "hashicorp/aws"` (a pure key change — no AWS entry's
   `IdentityFields`/`Notes`/`LookupHint` content touched). `ByType` and
   `core/lookuphints.For` both now take `(source, type)`.
   `core.ScanRequest` gains an optional `ProviderSource`, threaded
   through `RunScan`/`VerifyFreshness` into the teaching-error hint path
   (UBI-20 workstream 3) — populated by the CLI from `--source`; empty
   (honest generic fallback, never a guess) for a raw `--provider` path,
   since `ubx` has no way to know a hand-picked binary's registry
   identity. Full AWS regression (including `UBX_CONFORMANCE_LIVE=1`
   against the real account) re-run clean — confirms the refactor
   changed no AWS-observable behavior, only the internal key shape.
2. **`hashicorp/google` verified empirically.** New
   `conformance/gcp_provider_test.go` (`UBX_CONFORMANCE_LIVE=1`-gated,
   like every other network-touching conformance test, even though this
   one needs no GCP account/credentials at all — see the test's own doc
   comment for that categorization call): acquires the real
   `hashicorp/google` 7.40.0 binary via `provider.Acquire`
   (registry.opentofu.org, checksum-verified, same path `hashicorp/aws`
   already uses), launches it, and asserts the negotiated protocol.
   **Empirical finding: `hashicorp/google` speaks tfplugin v5** — same
   as `hashicorp/aws` (Slice 1's own finding) — dual v5/v6 support earns
   its keep a second time.
3. **~40 GCP `conformance.Registry` entries seeded** (docs/plan.md's own
   §M1-2 GCP resource type list: compute, network, IAM, storage, SQL,
   DNS, messaging — mirroring the AWS list's category spread and "real
   GCP shop" bias). `Safety: FakeOnly`, `Implemented: false` for every
   one — deliberately mirroring UBI-9 session 1's own AWS bootstrapping:
   seed the list first, work through it in batches later. `IdentityFields`
   come from a real `GetProviderSchema` call against the acquired binary
   (free, no credentials), not guessed.
4. **ubiquex-docs updated same-session** (later superseded by Stage 2's
   own docs commit, below).

**Stage 2 work (needed a real GCP project + credentials), two commits:**

1. **Five GCP types live-verified and promoted to `RealSafe`**:
   `google_storage_bucket`, `google_pubsub_topic`, `google_service_account`,
   `google_secret_manager_secret`, `google_project_iam_custom_role` — via
   the same `conformance.RunAdoptMutateScanDiff` harness UBI-9's AWS
   batches used, against a real, throwaway resource per type
   (`conformance/gcp_live_test.go`), destroyed after each run. Real,
   type-specific lookup-shape findings — see Surprises below for the
   full writeup, since two of these turned out materially more dangerous
   than anything AWS showed.
2. **`gcpaudit/` implemented and live-verified.** New package, `core.EventLookup`
   against real Cloud Logging `ListLogEntries` (Admin Activity audit logs
   only, mirroring `cloudtrail/`'s own management-events-only scoping).
   `core.AttributeDrift` gained an `AttributionBackend` parameter
   (`SuccessKind`/`UnattributedKind`/`DeliveryLag`/`Name`) so `cloudtrail/`
   and `gcpaudit/` each own their platform's specifics; `cloudtrail.Backend`'s
   own output is byte-identical to before this change. Live-verified end
   to end via the actual `ubx scan` command against a real Pub/Sub topic:
   the generated `drift_adopt` proposal correctly carried a `gcp_audit`
   source with the real GCP account email that made the change. Cloud
   Audit Logs' delivery latency measured directly (~18s for one mutation).
   A real, confirmed correlation gap was found and documented, not
   silently resolved — see Surprises below.
3. **A real UBI-20 regression, caught by this session's own live runs and
   fixed**: `cli/attribution_live_test.go`'s AWS test still checked
   `err != nil` after a successful adopt/drift scan, unaware that UBI-20
   made those return `ExitCodeError{Code: 1}` now. Nobody had actually run
   this specific live test with `UBX_CONFORMANCE_LIVE=1` since UBI-20
   shipped — exactly the kind of gap "audit every verb" exists to catch,
   caught here by actually running it rather than assuming the earlier
   audit was complete.
4. **ubiquex-docs updated same-session**: `cli/lookup.mdx`'s GCP section
   now has a real five-type table (with a `Warning` for the two
   silently-incomplete-read types); `concepts/attribution.mdx`'s "Beyond
   AWS" section is no longer a future plan — a real transcript, real
   caller email, real event. `mint validate`/`mint broken-links` both
   pass clean.

## Current phase (previous)

**UBI-20 is done (this session, Linear-verified): the hardening pass —
"the credibility layer" — production ladder step 5.** Four independently
committed workstreams, design landed in docs/architecture.md ("Hardening
pass (UBI-20)") and docs/plan.md before code, per session protocol.

1. **Exit-code contract, everywhere.** `ubx status` (UBI-17) already had
   0 (clean)/1 (drift)/2 (unreadable-or-error); every other verb now
   follows the same contract explicitly via `cli.ExitCodeError`: 0
   success, 1 an actionable finding, 2 error.
   `cmd/ubx/main.go`'s fallback for a plain (non-`ExitCodeError`) error
   moves from `os.Exit(1)` to `os.Exit(2)` — **a deliberate breaking
   change**: every command except `status` used to exit 1 for *any*
   error; a script gating on "exit 1 means something went wrong" needs to
   gate on exit 2 instead, going forward. Per-verb classification (see
   `docs/exit-codes.mdx` in ubiquex-docs for the full table):
   - `scan`: 1 for `new`/`drifted` (a proposal was generated), 0 for
     `unchanged`. `--all`: 1 if anything was skipped, 0 if the whole walk
     adopted cleanly.
   - `accept`: 1 for a stale reverify block, a `parent`-mismatched
     proposal (the ledger moved since it was resolved), or a
     `--from-merge` claim that doesn't check out (trailer hash mismatch,
     commit/file/PR/trailer gone — every `github.Err*` sentinel
     `ghub.DeriveAcceptance` can return classified into this same
     family). 2 for a malformed/already-accepted/duplicate proposal, or a
     genuine tool/network failure.
   - `why --verify-acceptance`: 1 if the claimed acceptance doesn't check
     out (git-history failure, or a reviewer-approval `MISMATCH` — this
     used to be reported but never affect the exit code; it does now).
   - `writeback`/`revert-plan`: 1 if any attribute was declined or a
     resource block couldn't be located (manual steps needed) — same
     "used to be silently exit 0" fix as `why`'s reviewer mismatch.
   - `version`/`init`/`propose`: audited and left 0/2-only, deliberately
     — they have no "finding" concept, and that's a complete audit
     outcome, not an oversight.
   - Every command that returns `ExitCodeError` also sets
     `SilenceUsage`/`SilenceErrors` (the pattern `status` established),
     so a finding doesn't dump a flag-usage block or double-print
     "Error: ...".
2. **`--json` on `scan`/`status`/`why`.** New `cli/jsonformat.go`:
   `jsonFormatVersion = 1` (a schema version, not the product version —
   bumped only on an incompatible shape change) and `addressJSON`/
   `writeJSON` shared across all three. Human output is unchanged and
   still the default; `--json` replaces it entirely — never a mix of the
   two on one invocation, verified by unmarshaling the *whole, untrimmed*
   stdout in every `--json` test. `scan --json` (single-resource only —
   rejected in combination with `--all` or `--surface-as`, a deliberate
   scope limit, documented, not silently ignored) emits `{format,
   address, outcome, observed_hash, proposals}`. `status --json` emits
   `{format, drift_checked, resources[], summary}` —
   `resources[].status` is *omitted entirely* in ledger-only mode, not
   set to an empty string, so a consumer can't mistake "didn't check" for
   "0 drifted." `why --json` emits `{format, proposal}` for the single-id
   form or `{format, chain[]}` (newest first) for the resource-address
   form, never both; `--verify-acceptance --json` required restructuring
   `runVerifyAcceptance` (`cli/verify.go`) to take a `jsonMode bool` and
   return a `*verifyAcceptanceJSON` result alongside the same
   `ExitCodeError` classification either way, so the checks and their
   exit-code logic live in exactly one place regardless of output mode.
3. **Teaching errors.** `core.ErrResourceUnreadable` ("provider returned
   no state") now names the likely fix for three types with a confirmed
   missing-field mistake (`aws_s3_bucket`, `aws_iam_role`,
   `aws_iam_user`) plus a link to `cli/lookup`'s docs page, instead of a
   bare sentinel. Mechanism, decided and documented in
   docs/architecture.md: `conformance.TypeSpec` gained a new structured
   `LookupHint []string` field (Notes is free prose, not mechanically
   generatable from); a new `conformance/gentool` (a `go generate`-invoked
   generator, never imported by anything else) reads it and writes
   `core/lookuphints/hints.go` — a small, committed, generated table with
   *zero* runtime dependency on `conformance/`, which stays test-only.
   `conformance/gentool_test.go` guards against the committed file
   drifting from a hand-edited `Registry` without re-running `go
   generate`. See Surprises below for a real bug live verification caught
   in this workstream before it shipped.
4. **Ledger lock.** New `core/lock.go`: a PID-file lock at `.ubx/lock` (a
   *third*, distinct file — `.ubx/config` and `.ubx/ledger.lock` already
   exist for unrelated reasons, none of the three conflated) wraps
   `Ledger.Append`'s whole check-then-write sequence (not just the final
   write — two concurrent callers that both read the same head before
   either writes would otherwise both think they're building on the
   current head). A blocked caller waits up to `lockWaitTimeout` (3s,
   package var, shrunk in tests) for a genuinely live holder, then fails
   with the file path and holder PID; a lock naming a PID that's no
   longer running is detected *immediately* (no need to wait out
   contention for a holder that isn't there) and reported with explicit
   recovery guidance (`remove <path> to recover`) — **never auto-removed
   by the failed acquirer**, since deleting a lock file is a deliberate
   operator action. Deliberately a PID file, not a bare OS `flock(2)`: a
   real `flock` is released by the kernel the instant a holding process
   dies for any reason, which would make "stale lock from a killed
   process" invisible rather than a real, testable scenario — see
   docs/architecture.md for the full reasoning. `scan`/`why`/`status`
   never call `Append`, so they never acquire this lock and are never
   blocked by an in-progress `accept` — proven directly in
   `TestRunScan_NotBlockedByHeldLedgerLock` (release held for the
   duration, `RunScan` still completes promptly).

Every workstream committed separately, adversarial tests throughout
(including two-concurrent-accepts and accept-during-scan for the lock,
and a genuine `errors.Is`/exit-code audit across every affected file), and
verified against the real, built binary as well as `go test`. Live
verification: `conformance/lookuphints_live_test.go` (Teaching errors,
against the real `ubx-states` S3 bucket) and manual real-binary exit-code/
lock smoke tests (recorded below in Surprises, since one of them changed
the actual implementation). ubiquex-docs updated same-session, per
protocol: new `cli/exit-codes.mdx` (the full per-verb table), `--json`
schema sections on `cli/scan.mdx`/`cli/status.mdx`/`cli/why.mdx`, a new
"Concurrent access" section on `concepts/ledger.mdx`, and a cross-link
from `cli/lookup.mdx` to the new teaching-error text. `mint validate`/
`mint broken-links` both pass clean.

**Consciously out of scope, not silently dropped**: the Linear issue's
fuller description also mentions "timeout/retry behavior reviewed under
real network conditions" — the user's own 4-workstream breakdown for this
session didn't include it, and it was treated as the authoritative scope.
Worth picking up as its own pass if it becomes a real question (nothing
shipped this session makes existing timeout/retry behavior more or less
correct than before).

## Current phase (previous)

**UBI-19 is done (this session, Linear-verified): `.ubx/config` defaults
and `ubx init` — production ladder step 4.** Design landed first in
docs/architecture.md ("Config defaults") and docs/plan.md before code,
per session protocol. Four pieces:

1. **TOML, not YAML** (`github.com/BurntSushi/toml`, the first dependency
   added purely for config parsing): no implicit type coercion, matching
   this project's own determinism posture — justified in full in
   docs/architecture.md, not just asserted.
2. **`cli/config.go`**: discovery walks from the current working
   directory upward, nearest `.ubx/config` wins (same convention `.git`
   itself uses), independent of `--ledger-dir` on purpose. Covers exactly
   five keys the issue named: `[provider]` (`path`, or `source`+`version`),
   `[provider_config]` (freeform, marshaled to the same JSON string
   `--provider-config` already accepts), `stack`, `github_repo`, `tf_dir`
   — deliberately not `--ledger-dir`. Precedence fixed everywhere it
   applies: CLI flag (via `cmd.Flags().Changed`, never a zero-value
   guess), then config, then whatever "required and absent" already
   meant for that flag. Unknown keys warn (`toml.MetaData.Undecoded()`)
   and are ignored; malformed TOML is a hard error.
3. **Wired into every verb that takes these flags**: `scan` (stack,
   provider, provider-config, github-repo, tf-dir — config's stack sits
   *before* `--all`'s own filename-derived fallback in the precedence
   chain), `accept` (provider-config and github-repo; config's provider
   only fills a gap in an *already-opted-into* `--reverify-source`, never
   turns reverification on by itself — accept's reverify stays
   per-invocation opt-in regardless of what config holds), `why`
   (github-repo), `writeback`/`revert-plan` (tf-dir), `status` (provider
   and provider-config only — **deliberately not stack**: its absence
   there means "every stack," not "required and missing," and applying a
   configured default would silently turn status's whole selling point
   inside out).
4. **New `ubx init [--dir] [--force] [--stack] [--source] [--provider-version]
   [--provider] [--provider-config] [--github-repo] [--tf-dir]`**: a real
   value for every key a flag was given, a commented example for
   everything else, refusing to overwrite an existing config without
   `--force`.

**A real bug caught by writing a test that actually decodes the generated
file back, not by eyeballing the template**: `renderConfigTemplate`
originally emitted `stack`/`github_repo`/`tf_dir` *after* the
`[provider]`/`[provider_config]` table headers — valid TOML, but TOML
itself assigns a bare key written after a `[table]` header to that table,
not the document root, no matter how many blank lines separate them. Every
one of those three values read back empty. Fixed by emitting all
root-level keys before any table header; `TestLoadConfig_RootKeysAfterTableGetSwallowed`
locks in the underlying TOML behavior itself as a permanent regression
test, not just the fix.

**A hermeticity fix applied proactively, not reactively**: `configSearchStartDir`
is a package var (not a bare `os.Getwd()` call) specifically so
`cli/scan_test.go`'s `TestMain` can pin the whole test suite to an empty
scratch directory. Without this seam, every test in the package would
silently depend on whether some ambient `.ubx/config` happens to exist
anywhere from the real test-runner cwd up to the filesystem root (a
developer's home directory, say) — exactly the kind of host-machine-state
leak `go test ./...` staying hermetic is supposed to rule out. Caught by
thinking through the discovery mechanism's implications before writing
any tests against it, not after one failed mysteriously on someone else's
machine.

## Current phase (previous)

**UBI-18 is done (this session, Linear-verified): `ubx scan --all`, bulk
onboarding from Terraform state — production ladder step 3.** Design
landed first in docs/architecture.md ("Bulk onboarding") and docs/plan.md
before code, per session protocol. Four pieces:

1. **New `tfstate/` package** parses Terraform state v4 JSON into
   `ManagedResource`s: modules (nested, dotted-path extraction from
   Terraform's own `module.x.module.y` form), `count`/`for_each`
   instances addressed `name[index]`/`name["key"]` matching Terraform's
   own convention exactly, `data` sources and any non-`"managed"` entry
   dropped outright. One `json.Unmarshal` of the whole file — bounded
   memory, not streaming, accepted at foundational-slice scale and
   verified against a synthetic 1000-resource state.
2. **A small, explicit per-type lookup-augmentation table**
   (`aws_s3_bucket`→`+bucket`, `aws_iam_role`/`aws_iam_user`→`+name`) —
   deliberately NOT derived from `conformance/registry.go`'s
   `IdentityFields`, which answers a related but distinct question (which
   attributes carry stable identity for CloudTrail attribution) and would
   have silently produced a wrong lookup for `aws_sqs_queue` if conflated
   (its `IdentityFields` names a distinct `url` field the actual lookup
   never needs, since `id` already equals it). Every other `RealSafe` type
   needs no augmentation: its bare `id` in state already is what an extra
   field would have contributed.
3. **`ubx scan --all --tfstate <path> [--stack] [--out-dir]`**
   (`cli/scanall.go`) reuses `core.RunScan`/`core.GenerateProposal`
   unchanged, once per resource — state provides identity, never truth;
   every proposal's observed state still comes from a live `ReadResource`
   call. Stack defaults to the state file's own basename. A module path
   gets folded into the resource's own address (not just noted in
   `intent.summary`) — a real "duplicate addresses" case the adversarial
   tests caught: two different modules can declare a same-type,
   same-name resource, and without folding, both would collide into the
   exact same `ubx` address the instant they share a stack. Unknown
   type / deleted-since-state / unbuildable-lookup resources go into a
   skipped-summary; the walk never aborts. Bulk *acceptance* stays
   explicitly out of scope.
4. **A real bug the live-verification test caught, not a hand-traced
   one**: every proposal in one `--all` batch shared the exact same
   (real, unmoving) ledger head as its `parent`, since nothing gets
   accepted mid-walk — only the first of N generated proposals would
   ever actually accept. Found only once the live test tried to accept a
   *second* real onboarded resource (an SNS topic, after an SQS queue
   already succeeded), not by reasoning through the flow beforehand.
   Fixed by tracking, purely within the `--all` orchestration, what the
   head *will be* after accepting every proposal generated so far in the
   same batch — a proposal's hash is a pure function of its content
   (`parent` included, `id`/`acceptance`/`status` excluded), so it's
   computable the moment a proposal is generated, before anyone accepts
   anything. A regression test (`TestScanAll_AllGeneratedProposalsAcceptInSequence`)
   now guards this directly, not just via the live test.

**Live-verified end to end against the real account**
(`TestScanAll_LiveEndToEnd`, gated behind `UBX_CONFORMANCE_LIVE=1`):
built a small, disposable Terraform config (an SQS queue + an SNS topic —
Terraform used *only* as a test-fixture generator here, never a runtime
dependency of `ubx` itself), `terraform apply`d it for real, onboarded
from the real resulting `terraform.tfstate`, accepted both generated
proposals, confirmed `ubx status --drift` reported both clean, then
`terraform destroy`d everything. Account confirmed left exactly as found
(no matching SQS queues or SNS topics remained).

Filed and tracked in Linear before this session started (`UBI-18`, team
`ubiquex`, filed directly by Roozbeh) — referenced, not re-typed from
memory.

## Current phase (previous)

**UBI-17 is done (this session, Linear-verified): `ubx status`, the fleet
drift view — M1-2's last unstarted piece (production-readiness step 2).**
Design landed first in docs/architecture.md ("Fleet status") before code,
per session protocol. Four pieces:

1. **`core.Ledger.Fleet(stack string) ([]FleetEntry, error)`**
   (`core/fleet.go`): one pass over `Chain()`, keeping the *latest*
   proposal per distinct address (discovered via
   `resolution.inputs[].resource` — the same field
   `LastObservedHash`/`LastObservationTime`/`ProposalsForAddress` already
   key off, so `ubx status` reports exactly the same "known resources"
   set `ubx why <address>` can already look up). Sorted by canonical
   address string. A malformed/unparseable address string is skipped, not
   guessed at. `stack` filters after discovery, not during — it doesn't
   change how or where the ledger is read.
2. **A confirmed (not assumed) finding**: `core.Ledger`'s own doc comment
   calls it "a per-stack append-only proposal chain," and
   docs/schema.md's layout diagram roots each stack at its own directory
   — but `Head()`/`Append()` don't actually partition storage by
   `Proposal.Stack` at all; one ledger directory is one flat chain, and
   `Stack` is just a recorded field. Because `GenerateProposal`/
   `GenerateRevertProposal` always read the *live* current head before
   building a proposal, multiple stacks chain together correctly within
   one shared directory — previously untested (every prior session used
   exactly one `--stack` per ledger directory), now covered by a real
   multi-stack test (`TestFleet_MultiStack`, `TestStatus_MultiStack_
   FilterByStack`). `ubx status`'s "all stacks by default" framing
   depended on this actually being true, not just plausible.
3. **`ubx status [--drift] [--stack <name>]`** (`cli/status.go`):
   ledger-only by default (no provider, no credentials); `--drift` runs
   `core.RunScan` per resource using each resource's own persisted
   `resolution.inputs[].lookup` (the entire reason that field was added,
   UBI-7 follow-up) against one provider launched once for the whole
   walk. Classifies clean / drifted / unreadable; a missing lookup skips
   the provider call entirely (an immediate, specific "unreadable" rather
   than an unpredictable provider call with nothing to look up), any
   other per-resource failure (unknown type, transient provider error, a
   malformed proposal `FoldState` can't reconstruct) is caught and
   recorded the same way — **the walk always continues**, verified by a
   real unknown-resource-type failure mid-fleet, not just a
   hand-constructed error.
4. **A new, narrowly-scoped exit-code mechanism**: `cli.ExitCodeError{Code,
   Err}`, which `cmd/ubx/main.go` now checks for via `errors.As` before
   falling through to the existing blanket "any error means exit 1" —
   every other command is completely unaffected (a plain error still
   takes the same path it always did). This is what makes `ubx status`'s
   CI exit-code contract (0 clean, 1 drift, 2 unreadable-or-error,
   whichever's worse always winning) possible without an in-process
   `os.Exit` call, which would have killed this codebase's own CLI test
   harness (`runUbx` executes commands via cobra's `Execute()` in the same
   process). **A real UX bug caught and fixed along the way**: an
   `ExitCodeError{Code: 1, Err: nil}` for a "drift found, nothing else
   wrong" result still triggered cobra's default error handling — a blank
   `Error: ` line followed by the entire flag-usage block, for a
   perfectly normal report outcome, not a misuse of the command. Fixed by
   (a) always giving `ExitCodeError` a real one-line message
   (`"status: N resource(s) drifted (see above)"` etc.) instead of nil,
   and (b) setting `SilenceUsage`/`SilenceErrors` on the `status` command
   specifically (not project-wide) — without the latter, the message
   printed twice (once from cobra's own default handling, once from
   `main.go`'s `ExitCodeError`-aware print). Caught by actually running
   the built binary end-to-end and reading its output, not just checking
   `err != nil` in a test.

**Live-verified end to end against the real account**
(`TestStatus_LiveEndToEnd`, gated behind `UBX_CONFORMANCE_LIVE=1`): adopted
the real `ubx-states` bucket *and* a throwaway SQS queue (created and
deleted for this test, reusing `conformance/aws_live_test.go`'s own
create-tag-destroy pattern) into one ledger — a genuinely multi-resource,
multi-type fleet, not a single address dressed up as one. Confirmed
ledger-only mode lists both with no provider launched; confirmed `--drift`
reports both clean; mutated only the bucket's tag out of band and
confirmed the fleet report correctly distinguished it (drifted) from the
untouched queue (still clean), with the right summary counts and exit code
1. Bucket confirmed back to its original untagged state
(`GetBucketTagging` → `NoSuchTagSet`, before and after); queue confirmed
deleted (`list-queues` with its name prefix returns nothing) — via
`t.Cleanup`.

Filed and tracked in Linear from the start (`UBI-17`, team `ubiquex`),
verified via the Linear MCP tool itself before any commit referenced it —
same discipline as UBI-16.

## Current phase (previous)

**UBI-16 is done (this session, Linear-verified): the revert path — M3-4's
other resolution to a detected drift.** Design landed first in
docs/architecture.md ("Revert path") and docs/schema.md ("Amendment:
drift_revert proposals") before any code, per session protocol. Four
pieces:

1. **`core.GenerateRevertProposal`** (`core/scan.go`): the corrective
   counterpart to `GenerateProposal`'s `drift_adopt` from the same
   observation — `before`=observed(drifted), `after`=ledger-recorded
   (restore-to), the exact reverse of `drift_adopt`'s own convention
   (mechanically: `diffAttributes(observed, ledgerState)` instead of
   `diffAttributes(ledgerState, observed)`, arguments swapped, same
   function). Only valid on `ScanDrifted` — a never-seen resource has
   nothing to revert to. `core/validate.go` gained `validateDriftRevert`:
   unlike every other drift/adoption kind, `blast_radius` must be REAL
   (`modifies` == `len(delta.modifies)` exactly, `creates`/`destroys`
   zero, at least one modifies entry required) — accepting a revert is a
   decision to actually change cloud, not a record of something that
   already happened.
2. **`ubx scan --propose revert|adopt|both`** (default `adopt`, byte-for-
   byte unchanged): on drift, generates `drift_adopt`, `drift_revert`, or
   both (two draft proposals sharing one `parent` — alternative
   resolutions to the same detected drift; accepting one stales the
   other via ordinary parent-mismatch, no new mechanism). No effect on a
   `new` outcome (always adoption). CloudTrail attribution and
   `--surface-as` both stay drift_adopt-specific (per docs/schema.md's
   existing pinned scope) — `--surface-as revert` is a hard error with a
   clear message, since its receipt is built entirely around a
   drift_adopt proposal that mode doesn't generate. `--out` with
   `--propose both`'s two proposals is also a hard error (print to stdout
   instead).
3. **A real, necessary correction to `RunScan` itself**: drift
   classification now compares a fresh read against
   `ObservedHash(FoldState(addr))` — the ledger's actual reconstructed
   truth — instead of `Ledger.LastObservedHash` (the last thing a scan
   happened to literally observe). These two coincided for every kind
   that predates `drift_revert` (verified: the full pre-existing test
   suite passes byte-for-byte unchanged), but a `drift_revert` can make
   them diverge on purpose — accepting one is a decision that hasn't
   been applied to cloud yet, so `FoldState` (ledger says "restored")
   and `LastObservedHash` (last literal read, still drifted) genuinely
   disagree immediately afterward. Caught this by writing the exact
   live-verify sequence as a test first (`TestRunScan_
   AfterRevertAccepted_ManualCorrection_ScanClean`) and watching it fail
   with the wrong outcome at the wrong step — not by reasoning it through
   in the abstract and assuming it'd work.
4. **New `ubx revert-plan <accepted-drift_revert-id> [--tf-dir]`**
   (`cli/revertplan.go`): emits, never applies — no `--write` flag exists
   at all, unlike `ubx writeback`. Always prints a human-readable plan
   (resource, attribute, current → restore-to). With `--tf-dir`, reuses
   `tfwrite.FindAndApply` unmodified (fed a `Modification` whose `After`
   is already the restore target — "reverse direction" is semantic, not a
   different code path) for a corrective diff on literal attributes, and
   collects both declined (non-literal) attributes and "resource block
   not found in `--tf-dir`" cases into one manual-steps section — neither
   is a command failure, since a revert can target a resource that was
   only ever adopted via `ubx scan`, never written to `.tf`.
   `cli/why.go` needed **no rendering changes**: `Kind` already prints
   verbatim (`drift_adopt` vs `drift_revert`), and a revert's real blast
   radius already reads differently from adopt's always-zero one —
   confirmed by a new test with a 3-entry mixed-kind chain, not assumed.

**Live-verified end to end against the real `ubx-states` account**
(`TestRevertPath_LiveEndToEnd`, gated behind `UBX_CONFORMANCE_LIVE=1`, same
convention as every other real-account test): tagged the bucket
`Environment=prod`, adopted it, mutated the tag to `staging` out of band,
ran `ubx scan --propose both`, accepted the `drift_revert`, confirmed
`ubx revert-plan`'s output named the right resource/attribute/values,
applied the correction manually via the `aws` CLI (standing in for "the
team's own tooling"), and confirmed a final `ubx scan` reported clean.
Bucket confirmed back to its original untagged state
(`GetBucketTagging` → `NoSuchTagSet`, both before and after) via
`t.Cleanup`.

Filed and tracked in Linear from the start (`UBI-16`, team `ubiquex`) —
verified via the Linear MCP tool itself, not assumed/typed from memory
(see the 2026-07-11 Surprises entry below about a prior session's
"UBI-11" mislabeling — this time the ticket existed before any commit
referenced it).

## Current phase (previous)

**UBI-12 is done (this session): release cut v0.1.0 — goreleaser +
tag-triggered CI wired up, the tag itself not yet pushed.** Four pieces:

1. `cli/version.go`: `Version`/`Commit` package vars, both overridable via
   `-ldflags -X`. `versionString()` prints `<Version>` alone, or
   `<Version>+<commit>` once a commit is known — from `Commit` if ldflags
   set it (every goreleaser build does), otherwise from
   `buildInfoRevision()`, which reads Go's own automatic VCS build-info
   stamping (`debug.ReadBuildInfo()`'s `vcs.revision`, present for any
   `go build` run inside a git checkout with no ldflags at all). Verified
   both paths for real: a plain `go build -o ubx ./cmd/ubx` prints
   `dev+99236a7`; a build with `-ldflags "-X ...Version=0.1.0 -X
   ...Commit=abc1234"` prints `0.1.0+abc1234`. `buildInfoRevision` is a
   package var (not a bare call) specifically so tests can stub it instead
   of depending on the real running-under-`go test` VCS stamp —
   `cli/version_test.go` covers all four combinations (bare `dev`,
   overridden `Version` alone, `Version`+`Commit` both set, `Commit` unset
   falling back to a stubbed `buildInfoRevision`).
2. `.goreleaser.yaml`: darwin/linux × amd64/arm64 archives (`tar.gz`),
   `checksums.txt`, GitHub Releases only — deliberately no `brews:`/
   `dockers:` sections at all, not disabled-but-present ones. `release.github`
   pins `owner: Ubiquex` explicitly (the org's actual case) rather than
   trusting auto-detection from the git remote's literal casing. Verified
   with `goreleaser check` (config validates) and two dry runs, neither
   published: `goreleaser release --snapshot --clean` (no tag needed,
   confirms the build/archive/checksum pipeline works at all) and a
   real-tag dry run (`git tag v0.1.0` locally, `goreleaser release --clean
   --skip=publish,announce,validate`, then `git tag -d v0.1.0` — never
   pushed) — the second one is what actually confirmed the ldflags produce
   `0.1.0+99236a7` end to end, checksums verify (`shasum -a 256 -c`), and
   archive/checksum filenames are exactly what the install docs reference.
   One bug caught and fixed during this: an initial `snapshot.version_template`
   of `"{{ .Tag }}-dev+{{ .ShortCommit }}"` double-appended the commit
   (once from the template, once from `versionString()` itself) — only
   visible in snapshot mode, since a real tag's `{{.Version}}` never
   contains a commit suffix on its own; fixed by dropping the commit
   from the template and letting ldflags own it exclusively.
3. `.github/workflows/release.yml`: triggers on `v*` tag pushes only, runs
   `goreleaser-action` with `fetch-depth: 0` (needed for changelog
   generation) and `go-version-file: go.mod`. Not run this session — CI
   workflow files execute on GitHub's runners, not locally; correctness
   here rests on the goreleaser config being independently dry-run
   verified per #2, plus the workflow's own shape matching goreleaser's
   documented GitHub Actions convention.
4. `README.md`: status line now says releases publish via GitHub Releases
   starting at `v0.1.0` (previously "foundational slices in progress" with
   no release story at all), a docs link to ubiquex-docs (not yet publicly
   hosted, said so honestly), and a small accuracy fix carried along
   (`tfplugin v6` → `tfplugin v5/v6`, matching the dual-protocol client
   this project has actually shipped since UBI-7's real-provider work).
   ubiquex-docs' `getting-started/installation.mdx` and `cli/version.mdx`
   updated to match — real `gh release download`/checksum/PATH
   instructions replacing the source-only placeholder, labeled honestly as
   pre-alpha; see that repo's own STATE.md for detail.

**The `v0.1.0` tag itself has not been pushed.** Everything above is
release *infrastructure* — the tag push (`git tag v0.1.0 && git push
origin v0.1.0`), which is what actually triggers the GitHub Actions
workflow and publishes anything, is Roozbeh's manual act, done after
reviewing this session's dry-run output. Until that happens, there is no
real `v0.1.0` release on GitHub — the install docs describe the release
process that will exist once he does, not a claim that it exists already.

**UBI-9 is done (prior session): all 51 AWS resource types resolved — 48
verified (7 real-safe, 41 fake-only), 3 parked, 0 left pending.** Batches
1-2 established the real-safe types; batch 3 built a generalized
fakeprovider fixture (`conformance-v5`/`conformance-v6` modes, env-var
driven) so every remaining fake-only type gets a genuine
adopt→mutate→scan-diff test, not just a registry entry with no test
behind it. `conformance/registry.go`'s Registry — every type's verified
`IdentityFields`/`Notes` or a documented parked reason, enforced by
`TestRegistry_NoThirdState` — is the milestone's actual deliverable. Full
writeup preserved in Done below; not repeated here now that UBI-10 has
built directly on top of it (see immediately below).

**UBI-10 is done (this session): CloudTrail attribution is wired into `ubx scan`'s
drift-proposal generation, verified against the real account.** Every
`drift_adopt` proposal now gets a best-effort attribution attempt: two new
`intent.sources` kinds, `cloudtrail` (a matched management event — actor
ARN, event id/name/time, source IP, session context) and
`cloudtrail_unattributed` (attempted, failed, with a `reason` —
`no_matching_event` | `delivery_window` | `not_logged`). Same
dependency-inversion discipline as `core.StateReader`: `core/attribution.go`
owns the deterministic decision logic behind a minimal `EventLookup`
interface (no AWS SDK, fully unit-tested against a fake), `cloudtrail/` is
the one package that imports `aws-sdk-go-v2` directly, `cli/attribution.go`
wires the two into `ubx scan` (`--no-attribution` opts out). Best-effort by
construction — attribution failure of any kind never blocks generating or
accepting the underlying drift proposal.

Building the first real integration surfaced two empirical corrections
before they became bugs (see Surprises): CloudTrail's `ResourceName`
lookup attribute wants the resource's own `id` (bucket name, role name,
vpc-id), not its ARN — an initial assumption that ARN would be the more
precise match turned out backwards, caught by testing against the real
account before writing the matching logic, not after. And real CloudTrail
delivery latency in this account measured ~2-3 minutes for a live
`PutBucketTagging` call to become queryable — enough to make the first
live-test attempt fail on a too-short retry budget, fixed by widening it
rather than by weakening what the test actually checks.

**Verified live, exactly as asked**: tagged the real `ubx-states` bucket
(a genuine out-of-band mutation, same pattern as every prior real-world
verification in this codebase), ran `ubx scan` through the actual CLI
command without `--no-attribution`, and confirmed the generated
`drift_adopt` proposal's `intent.sources` carried a `cloudtrail` entry
whose `actor_arn` was Roozbeh's real IAM identity
(`arn:aws:iam::839333509514:user/roozbeh`) — not a fake/simulated one.
This is captured as an actual repeatable test
(`cli/attribution_live_test.go`'s `TestScan_AttributesRealDrift_LiveCloudTrail`,
gated behind `UBX_CONFORMANCE_LIVE=1`, same convention as every other
real-account test), not just a one-off manual check. Bucket tag confirmed
removed afterward.

UBI-9 (51-type conformance), UBI-8 (provider acquisition), and UBI-7
(Slice 3 + follow-ups) remain done from prior sessions (see below).

**Correction (2026-07-11): the `ubx why` polish below was mislabeled
"UBI-11" — that was never verified against Linear, and turns out to be
wrong.** The real UBI-11 (confirmed via Linear this session, per an
explicit "verify, don't infer" instruction) is "M3–4 decision loop:
adopt/revert proposals, PR-merge signing, .tf write-back" — see this
session's own entry further down. The `ubx why` work below has no
confirmed Linear ID; it's left labeled as originally written (wrongly)
rather than renumbered, since rewriting an already-pushed commit's
reference would be its own kind of historical inaccuracy. Lesson applied
going forward: verify a ticket ID against Linear before using it, don't
infer the next number in sequence.

**"UBI-11" (mislabeled, see correction above) — `ubx why` polished ahead
of demo recording, closing two gaps a dry run surfaced:**

1. **Resource-address support.** `ubx why <stack>.<type>.<name>` now
   resolves and renders the resource's *entire* recorded history —
   adoption plus every subsequent drift_adopt — newest first, instead of
   requiring the operator to already have a specific proposal ID in hand.
   `ubx why <proposal-id>` (the 64-hex-char form) is completely
   unchanged: same output, byte-for-byte, for every existing intent
   source kind. New `core.ParseAddress` (the inverse of `Address.String`)
   and `Ledger.ProposalsForAddress` do the actual work; `cli/why.go`
   decides which path to take by regex-matching the argument against the
   64-hex-char shape first, falling back to address parsing.
2. **Attribution rendering.** `intent.sources` entries used to print as a
   bare `kind ref (content_hash=...)` line regardless of kind — fine for
   dialogue/PR/issue sources, useless for the whole point of UBI-10's
   `cloudtrail` sources, which carry an actor ARN, event name/time, and
   source IP that were previously invisible unless you opened the raw
   JSON. `cloudtrail` sources now render the human story inline (who, did
   what, when, from where), with the event id/content_hash demoted to an
   indented detail line rather than dropped; `cloudtrail_unattributed`
   sources render their `reason` in words (e.g. "too recent for
   CloudTrail to have delivered a matching event yet") instead of the
   bare enum value. Every other kind renders exactly as before.

Verified by hand against the actual built binary, not just the test
suite, specifically because this was a "will it read well on camera"
polish pass: built a real chain (adopt → drift, via fakeprovider) and
confirmed the newest-first, two-entry rendering; hand-accepted a proposal
carrying one `cloudtrail` and one `cloudtrail_unattributed` source and
confirmed both render as intended (see Done for the exact output).

**UBI-11 (real, Linear-confirmed this session): "M3–4 decision loop" —
docs landed, Stage 1 (PR-merge acceptance binding) done and verified live
against the real repo; Stages 2 (.tf write-back) and 3 (GitHub App
skeleton) queued for future sessions.**

Design for all three stages landed first, as its own commit, before any
implementation (`docs/architecture.md`'s new "Decision loop (UBI-11)"
section, `docs/schema.md`'s "pr_merge acceptance fields" amendment) — per
the session's own explicit sequencing.

Stage 1 implements the `pr_merge` acceptance tier: **derived, never
asserted.** An author resolves a proposal (`ubx propose <file>`, prints
the canonical hash without touching the ledger), commits the draft as an
ordinary file on a branch, and puts `ubx-proposal: <hash>` in the PR
body. Ordinary GitHub review happens — branch protection, required
reviewers, all of that is entirely GitHub's job, ubx has no opinion on it
before the merge. Once merged, `ubx accept --from-merge <sha>
--repo-dir <path> --proposal-file <path-in-repo> --github-repo
<owner/name>`:

- Verifies `<sha>` exists in local git history (`github.CommitExists`,
  shells out to the real `git` binary — no pure-Go git reimplementation
  needed for read-only plumbing this simple).
- Reads the proposal file's content *at that commit*
  (`github.FileAtCommit`, `git show <sha>:<path>`) — not whatever's on
  disk now.
- Recomputes the proposal's canonical hash from that exact content and
  requires it to equal the trailer's claimed hash
  (`core.ErrTrailerHashMismatch` on any mismatch — the actual enforcement
  of "derived, never asserted," not just a description of intent).
- Finds the merged PR via the GitHub API (`google/go-github`, the new
  `github/` package's only external dependency besides `git` itself) and
  computes approvers as every reviewer whose *most recent* review is
  `APPROVED` — a later `CHANGES_REQUESTED` from the same person
  supersedes an earlier approval, so a withdrawn approval never counts.
- Writes `acceptance = {method: "pr_merge", merge_sha, pr_number,
  proposal_file, approvers, accepted_at}` and appends to the ledger.

**Zero approvers is a valid, recorded outcome, not a rejection** — a
merge with no approving reviews at all is recorded exactly as it
happened; ubx never enforces review requirements after the fact, that's
GitHub's job entirely. `ubx why <id> --verify-acceptance --repo-dir
<path> [--github-repo <owner/name>]` re-runs the git-history and hash
checks anytime after acceptance (hard failure if the commit or its hash
no longer checks out — the acceptance record can no longer be verified
against the history it claims, a serious finding) and, given
`--github-repo`, re-fetches current approvers and reports a mismatch
without failing the command (the ledger correctly recorded what was true
then; reality having moved on since is exactly what this check exists to
surface).

**Verified live, for real, on the actual repo** (the task didn't
explicitly ask for this the way UBI-10's did — stopped and asked before
doing it, given opening/merging a real PR is more consequential and
harder to fully undo than tagging a scratch AWS resource; user said to go
ahead): opened `Ubiquex/ubiquex-cli#1` from a scratch branch with a real
`ubx-proposal: <hash>` trailer (the hash `ubx propose` actually printed),
merged it via `gh pr merge --merge`, then ran `ubx accept --from-merge`
against the real merge SHA with a real `GITHUB_TOKEN` (`gh auth token`).
It worked end to end — `accepted 2d9ad652... (stack verify) via PR #1, 0
approver(s)`, a genuine live instance of "unreviewed merge recorded, not
blocked" (nobody reviewed my own scratch PR). `ubx why --verify-acceptance`
against the same real commit/PR confirmed both the git and API legs pass.
Cleaned up immediately after: reverted the merge commit on `main`
(`git revert -m 1`), deleted the scratch branch locally and on the
remote. See Done below for the full transcript.

**UBI-11 Stage 2 is done (this session): `.tf` write-back.** New `tfwrite/`
package surgically overwrites a literal attribute value — including
nested paths like `tags.hotfix` — on an existing resource block, driven
by an already-*accepted* `drift_adopt` proposal's `delta.modifies`. New
`ubx writeback <proposal-id> --tf-dir <dir> [--write]`.

The surgical mechanism, worked out and verified empirically before
building on it (see Surprises): `hclsyntax` (not `hclwrite`'s own
`Body.SetAttributeValue`) locates the exact byte range of the specific
sub-expression being changed — either a whole top-level attribute's value,
or one key's value inside an existing literal object/list — and only that
byte range is ever replaced, using `hclwrite.TokensForValue` purely to
render the replacement's tokens correctly. `SetAttributeValue` would have
regenerated the *entire* attribute's tokens, losing comments/formatting on
anything with internal structure (a map or list) — confirmed this would be
a real problem, not a theoretical one, by testing it against a real object
literal with an inline comment before choosing the byte-splice approach
instead. Literal-vs-expression detection (declining on any variable
reference, function call, or interpolation) turned out to have an
elegant, already-built-in mechanism: `hclsyntax.Expression.Value(nil)`
(evaluate against a nil context) fails with a clear diagnostic
("Variables not allowed" / "Function calls not allowed") for anything
that isn't a pure literal, and succeeds — with the actual value — for
anything that is, including a template string with no interpolation and
an object/list literal whose members are all themselves literal.

Scope, as first shipped (revised in this same session's follow-up — see
immediately below, kept here rather than deleted since it's what actually
shipped that day): write-back only ever modified attributes/keys that
**already existed** in the file — a brand-new tag key drift added was
declined ("write-back never adds new attributes/keys") rather than
inserted. Output is a diff by default, or an actual file write with
`--write` — never a git commit; the docs' "(or a commit on a branch)"
phrasing describes a future option, not something this session built.

Verified by hand against the built binary in addition to the test suite
(see Done for the exact transcript): a two-attribute drift (a top-level
scalar plus a nested map key) applied to a real-shaped `.tf` file with
comments on both the changed lines, `--write`d, and confirmed the
resulting file kept every comment and every untouched attribute exactly
as it was, with only the two drifted values changed.

**UBI-11 Stage 2 follow-up (this session): write-back may now insert a
brand-new key into an existing literal map attribute** — the single most
common real drift shape (someone tags a resource in the console with a
key the `.tf` file never had), and a design-room decision that revised
the "never adds new attributes/keys" line above. Still declined,
unchanged: a *top-level* attribute the file never set at all, a new
nested structure more than one key deep, and anything where the parent
map is itself an expression rather than a literal. The new key matches
the existing object's own formatting — indentation, and whether its items
are comma-terminated — rather than an arbitrary default; an empty `{}`
gets a sensible first entry. `resolveTarget` now distinguishes a missing
*final* path segment inside an existing literal object (safe to grow) from
a missing *intermediate* one (still declined — inserting a whole new
nested structure is a different, larger problem). Verified empirically
before implementing (see Surprises) that `hclwrite`'s own
`Body.SetAttributeValue` is unsuitable here — confirmed separately from
last session's replace-path finding — and switched `edits`' sort from
`sort.Slice` to `sort.SliceStable` once it became clear two simultaneous
insertions into the same map produce identically-valued byte ranges,
which an unstable sort's tie-breaking doesn't guarantee to order the same
way twice.

**UBI-11 Stage 3 (this session): GitHub App skeleton.** New `ubx scan
--surface-as issue|pr --github-repo <owner/name> [--tf-dir <dir>]` —
firing only on a `ScanDrifted` outcome, never `ScanNew`. Issue mode needs
only "issues: write" on the target repo, never "contents: write" at
all — the read-only-on-content security story docs/architecture.md
describes literally, not just in spirit. PR mode automates stage 1's own
by-hand flow exactly (commit the draft proposal to a new branch, put the
`ubx-proposal: <hash>` trailer in the PR body), so once a human merges the
result, the *existing, unmodified* `ubx accept --from-merge` from stage 1
derives acceptance from it the same way it would for a manually-opened
PR — no new acceptance mechanism needed, only a new trigger for the
existing one. The shared receipt (intent, blast radius, attribution, and
a best-effort `.tf` write-back preview) reuses `tfwrite.FindAndApply` in
pure dry-run mode — the same function `ubx writeback` calls, just never
given `--write` — so the preview and the eventual real write-back can
never silently diverge into two different code paths.

**Verified live, for real, end to end, closing the loop stages 1 and 3
share** (asked first, since PR mode creates a real branch/commit, the
same category of action as stage 1's live PR test): opened a real issue
on `Ubiquex/ubiquex-cli` (closed immediately after) and a real draft PR
(`Ubiquex/ubiquex-cli#3`), confirmed both carried exactly the expected
receipt content, then **merged that PR and ran the existing `ubx accept
--from-merge` against the real merge SHA** — accepted with 0 approvers,
the same real "unreviewed merge recorded, not blocked" outcome stage 1's
own live verification produced, this time triggered by the App skeleton's
own PR instead of a manually-authored one. Cleaned up immediately after:
reverted the merge commit on `main`, deleted the scratch branch locally
and on the remote, closed the scratch issue.

## Current focus

UBI-11 Stages 1, 2, and 3 are all done — the whole "M3–4 decision loop"
ticket's scoped work is shipped and verified live. Also still queued from
before UBI-9: the Core IR + resolver work, and `status --drift` (a
read-only multi-resource drift report, M1-2 scope per docs/plan.md).

## Open decisions

- [x] **RESOLVED 2026-07-11 — issue mode is the conservative default,
      PR mode is opt-in (UBI-11 stage 3).** `ubx scan --surface-as`
      requires an explicit value (`issue` or `pr`) rather than defaulting
      to whichever is "more useful" — deliberately, since they need
      different GitHub App permission scopes (`issues: write` only, vs.
      `contents: write` + `pull_requests: write`) and docs/architecture.md's
      own security-story framing ("a GitHub App that only ever reads...")
      is strongest when the *default* posture genuinely never writes repo
      content. Making the more-privileged option something an operator
      opts into by name, rather than something that happens unless
      disabled, matches the same "never enforced by default, always an
      explicit choice" posture the whole PR-merge acceptance design
      already established in stage 1.
- [x] **RESOLVED 2026-07-11 — `sort.SliceStable`, not `sort.Slice`, for
      `tfwrite`'s pending edits (UBI-11 stage 2 follow-up).** Two brand-new
      keys inserted into the *same* existing map in one `Modification`
      resolve to two zero-width byte ranges at the identical offset (both
      insert "after the current last item"). `sort.Slice`'s tie-breaking
      for equal elements isn't guaranteed by the standard library, so two
      runs over the exact same input could, in principle, order those two
      insertions differently — output would still be valid HCL either way,
      but non-deterministic output is exactly the kind of thing this
      project has refused to accept anywhere else (see docs/schema.md's
      canonical hashing rules). Switched to `sort.SliceStable`, which,
      combined with `paths` already being alphabetically sorted before
      edits are gathered, makes the tie-break outcome fixed and repeatable
      — confirmed with a dedicated test that runs the same insert twice
      and requires byte-identical output.
- [x] **RESOLVED 2026-07-11 — byte-range splice via `hclsyntax`, not
      `hclwrite`'s `Body.SetAttributeValue` (UBI-11 stage 2).** The
      obvious-looking approach for ".tf write-back" is `hclwrite`'s own
      high-level API: parse with `hclwrite`, call
      `body.SetAttributeValue(name, ctyValue)`. Tested this against a real
      object-literal attribute with an inline comment on one of its keys
      before committing to it as the mechanism, per the project's
      standing "verify before implementing" discipline — and it fails the
      one thing this feature exists to guarantee: `SetAttributeValue`
      regenerates the *entire* attribute's token stream from the given
      `cty.Value`, so replacing one key inside a `tags = { ... }` map
      loses that key's (or any sibling's) inline comment and reformats
      the object's layout. Went with exact byte-range replacement instead:
      `hclsyntax` gives precise byte offsets for any sub-expression,
      including one specific item inside an object/list constructor;
      splicing only that range, using `hclwrite.TokensForValue` solely to
      render the replacement's bytes (never to edit the file's own token
      stream), preserves everything outside that one range with byte-for-
      byte fidelity — verified this empirically too (a throwaway repro
      confirming the round trip re-parses cleanly and the untouched
      comment survives) before writing `tfwrite`'s real implementation.
- [x] **RESOLVED 2026-07-11 — write-back never inserts new syntax, only
      ever replaces existing literal values (UBI-11 stage 2).** The docs
      landed last session scope write-back to "overwriting a literal
      attribute value... including nested attribute paths" — read
      generously, that could include adding a brand-new tag key drift
      introduced, or a top-level attribute the `.tf` file never set at
      all. Decided against supporting either in this first cut: inserting
      new syntax safely (correct indentation, correct position relative
      to sibling keys, trailing-comma conventions) is a meaningfully
      different and larger problem than surgical byte-range replacement
      of something that already has an exact, unambiguous location — and
      neither case was in the task's own named adversarial list. Declines
      with a clear "write-back never adds new attributes/keys" reason
      instead of guessing at placement; left as a named, explicit gap in
      Next steps, not a silent limitation.
- [x] **RESOLVED 2026-07-11 — shell out to `git`, don't vendor a git
      library (UBI-11 stage 1).** `github/git.go`'s `CommitExists`/
      `FileAtCommit` run the real `git` binary via `os/exec` rather than a
      pure-Go implementation (e.g. `go-git`). Reasoning: the only
      operations needed are read-only plumbing (`cat-file -e`, `show
      <sha>:<path>`) that every git installation already implements
      correctly and stably; a repo ubx is deriving acceptance for is
      necessarily already a real git checkout with a working `git` binary
      available (unlike CloudTrail, where no local-binary equivalent
      exists at all, which is why that integration went through a real
      SDK instead — see UBI-10's precedent for the opposite call, made for
      the opposite reason). Verified the exact error-message text `git
      show` uses for "commit exists, path doesn't" empirically
      (`does not exist in`) before writing `looksLikeMissingPath`, not
      assumed from memory.
- [x] **RESOLVED 2026-07-11 — no core-level interface for PR-merge
      derivation, unlike StateReader/EventLookup (UBI-11 stage 1).**
      `core.AcceptFromMerge` takes plain, already-verified data
      (`MergeAcceptance` + a claimed hash) rather than an interface core
      calls out through. Unlike CloudTrail attribution (a single swappable
      operation — "look up events" — that benefits from a fake in
      `core`'s own tests), PR-merge derivation is an inherently
      CLI-orchestrated multi-step process (git check, then API call, then
      trailer parse, then another API call) that doesn't reduce to one
      interface method without either a leaky abstraction or an interface
      nobody but this one call site would implement. `core` stays
      dependency-free either way; the difference is where the
      orchestration lives (`github.DeriveAcceptance`, not inside `core`).
- [x] **RESOLVED 2026-07-11 — `ubx why --verify-acceptance`'s git check is
      a hard failure, its GitHub API check is not (UBI-11 stage 1).** A
      merge commit that no longer exists, or a proposal file that no
      longer hashes correctly, means the acceptance record can no longer
      be verified against the history it claims — treated as a command
      failure (non-zero exit). A reviewer's approval having been
      withdrawn *after* acceptance is a different kind of finding: the
      ledger entry correctly recorded what was true when `ubx accept
      --from-merge` ran; reality moving on since doesn't retroactively
      make that entry wrong. Reported clearly (a `MISMATCH` line) but
      exit 0 — the same reasoning that makes CloudTrail's
      `cloudtrail_unattributed` a valid outcome rather than a failure:
      recording reality honestly, including its own inconclusiveness, is
      the point, not forcing every check into pass/fail.
- [x] **RESOLVED 2026-07-10 — CloudTrail identity matching is derived, not
      a static per-type table (UBI-10).** The task framing for this
      session said to match "on per-type identity fields (ARN/name from
      registry)" — read most literally, that could mean promoting
      `conformance/registry.go`'s `IdentityFields` into product code so
      `core/attribution.go` could depend on it. Decided against that:
      `conformance/` is explicitly documented as project-internal test
      tooling, not shipped product code (see the UBI-9 harness-shape
      decision below), and importing it from `core`/`cli` would break
      that boundary for a table that (a) doesn't need to be static at all
      — almost every AWS resource type carries `id` and `arn` directly in
      its own observed state, which is more precise and more current than
      a lookup table could be — and (b) can't fully capture the thing that
      actually matters here anyway, which is CloudTrail's own
      `ResourceName` semantics (empirically NOT the same per type as
      ubx's own `ReadResource` lookup shape — see Surprises). Instead,
      `identityCandidates` (core/attribution.go) derives search values
      directly from the resource's just-observed state (`id`, `arn`,
      `name`, in that order, deduped) — genuinely "per type" in the sense
      that the actual value differs per resource instance and type, just
      not via a maintained table. `conformance/` stays test-only,
      untouched by this decision.
- [x] **RESOLVED 2026-07-10 — attribution is a separate step, not built
      into `GenerateProposal` (UBI-10).** `core.GenerateProposal`'s
      signature and behavior are completely unchanged by this session —
      CloudTrail attribution is a new, separate function
      (`core.AttributeDrift`) that a caller invokes afterward and appends
      the result into the already-built proposal's `Intent.Sources`.
      Reasons: (1) `GenerateProposal` is called from ~50 existing
      conformance tests and `cli/scan.go`; keeping its signature stable
      avoided a mechanical, no-value edit to all of them. (2) It keeps
      "detect+diff" and "attribute" as separately testable, separately
      optional steps — exactly what "best-effort, never blocks proposal
      generation" means structurally, not just as a runtime guarantee.
      `cli/scan.go` calls `attributeDrift` (cli/attribution.go) right
      after `GenerateProposal` returns, only for `ScanDrifted` outcomes,
      only when `--no-attribution` isn't set.
- [x] **RESOLVED 2026-07-10 — what "verified" means for a FakeOnly
      conformance type (UBI-9 batch 3).** This came up while designing the
      first fake-only fixture and is worth recording explicitly rather than
      leaving as an implicit assumption baked into 41 registry entries:
      FakeOnly's `IdentityFields`/mutable-attribute claims are verified
      against the *real* AWS provider's schema (`GetProviderSchema` — free,
      no Configure/credentials/AWS API call needed), but NOT against a real
      `ReadResource` call, so the live lookup-convention quirks batches 1-2
      found empirically (e.g. `aws_iam_role` needing `id`+`name` duplicated,
      `name` alone reading back `null`) are *not* independently checked for
      FakeOnly types — checking that would require a real instance, which
      is exactly the cost/risk FakeOnly exists to avoid. Decision: FakeOnly
      conformance proves ubx's own `RunScan`/`GenerateProposal`/`FoldState`
      pipeline is correct for that type's real attribute shape; it does not
      prove the same thing about live lookup semantics that RealSafe
      conformance does. Documented directly in `conformance/registry.go`'s
      `FakeOnly` doc comment, not left implicit. See Done below for what
      shipped on this basis.
- [x] **RESOLVED 2026-07-10 — conformance harness shape (UBI-9).**
      Decisions made building it, recorded rather than left implicit:
      `conformance/` is a new top-level package (parallel to core/provider/
      cli) — project-internal test tooling, not shipped product code, so it
      doesn't live under core/ or cli/. It imports both `core` and
      `provider` freely (no architectural conflict — the UBI-7 inversion
      only requires `core` itself to stay provider-agnostic; nothing stops
      a test-harness package from depending on both). Live (real-AWS) tests
      are gated behind `UBX_CONFORMANCE_LIVE=1` and skip by default, so
      `go test ./...` never needs network/credentials — consistent with
      every other test in the project so far. "real-safe" vs "fake-only"
      is a property of *testing* a type (cost/risk of standing up a
      disposable instance), decided per type, not a blanket rule by
      category.
- [x] **RESOLVED 2026-07-10 — provider binary acquisition (UBI-8).**
      Decision: download from registry.opentofu.org (not
      registry.terraform.io — ToS risk for a third-party tool; OpenTofu's
      registry mirrors the same providers via the same protocol and is
      built for exactly this) with SHA256SUMS + OpenPGP signature
      verification, `~/.ubx/providers/<hostname>/<namespace>/<type>/
      <version>/<os_arch>/` cache, `UBX_PROVIDER_MIRROR` local-directory
      override checked first, explicit version pins only (no "latest"
      resolution). docs/architecture.md and docs/schema.md updated. See
      Done below for what shipped.
- [ ] Go module path final confirmation (`github.com/ubiquex/ubiquex-cli`)
- [x] **RESOLVED 2026-07-10 — protocol v6-only premise.** Decision: dual-protocol
      client. `provider/` now has tfplugin5 and tfplugin6 wire implementations
      behind one `Provider` interface, version selected from the handshake.
      docs/architecture.md and docs/plan.md updated accordingly. See Done below
      for what shipped.
- [x] **RESOLVED 2026-07-10 — canonical hashing serialization format.**
      docs/schema.md §Canonical hashing is now RATIFIED v1: SHA-256 over
      canonical JCS-style JSON, domain prefix `ubx:proposal:v1\n`, hash
      excludes exactly `id`/`acceptance`/`status`, numbers restricted to
      int64/decimal-strings (floats rejected at propose time), delta arrays
      sorted lexicographically by `(stack, type, name)`, intent.sources carry
      content hashes. Any further change requires a schema_version bump.
- [x] **RESOLVED 2026-07-10 — `delta.modifies`/`delta.destroys` element
      shape.** docs/schema.md §"Delta element shapes — PINNED": destroys =
      `Address {stack,type,name}`; modifies = `{target: Address, before,
      after}` with before/after holding only changed attributes
      (dot-notation for nested paths). Every modifies entry now requires a
      matching `resolution.inputs` entry with a non-empty `observed_hash`,
      enforced at propose time (`core.Validate`, called from `core.Accept`).
      `core.deltaSortKey` no longer guesses — see Done below.
- [x] **RESOLVED 2026-07-10 — the three Slice 3 architectural
      interpretations (UBI-7 follow-up).** All three closed the same day
      they were flagged:
      1. **core→provider dependency inverted.** `core` no longer imports
         package `provider` at all (verified: `grep -rn
         ubiquex-cli/provider core/*.go` returns nothing). `core/scan.go`
         now defines its own minimal `StateReader` interface (`Schema`/
         `Configure`/`ReadResource`, using `any` for opaque schema handles
         core never inspects); `cli/stateadapter.go` adapts a
         `provider.Provider` to it at the one call site that needs both.
         `core`'s own tests (`fakeProvider` in scan_test.go) implement
         `StateReader` directly now, with zero dependency on `provider`.
      2. **`FoldState`'s O(chain) walk: accepted, not deferred.** Documented
         directly in its doc comment (core/state.go) as a deliberate choice
         at current scale (one stack, resources addressed individually),
         with an explicit revisit trigger (an indexed/incremental
         alternative, once M1-2's auto-discovery makes per-address and
         per-ledger proposal counts grow enough to matter) — a decision on
         record, not an open worry.
      3. **Resource lookup key now persisted.** docs/schema.md §"Amendment:
         persist resource lookup key" adds `resolution.inputs[].lookup`
         (the JSON passed to `ReadResource`) — additive/optional, so
         explicitly does NOT require a schema_version bump (see the
         amendment's own reasoning for why that's different from the
         RATIFIED hashing rules or the PINNED delta shapes). `ScanResult`/
         `GenerateProposal` populate it; `core.VerifyFreshness` reads it
         back from the proposal instead of taking a `currentState` param —
         `ubx accept --reverify-with` no longer takes (or needs) `--lookup`
         at all.

## Done

- 2026-07-10: Repo founded. CLAUDE.md, STATE.md, docs/ (architecture, schema v0.1,
  plan, prompts) written from the v2 design session.
- 2026-07-10: Go module (`github.com/ubiquex/ubiquex-cli`) initialized. Cobra CLI
  skeleton added: `cmd/ubx/main.go` entrypoint, `cli/root.go` root command,
  `cli/version.go` (`ubx version`, `Version` var overridable via ldflags). Tests
  in `cli/version_test.go` and `cli/root_test.go`, all green (`go build ./...`,
  `go vet ./...`, `go test ./...`).
- 2026-07-10: Slice 1 first cut — `provider/` package, tfplugin v6-only. (Superseded
  same day by the dual-protocol refactor below, once real binaries turned out
  not to speak v6 — see Surprises.)
- 2026-07-10: Slice 1 completed — dual-protocol `provider/` package.
  - `provider/tfplugin5/`, `provider/tfplugin6/`: both proto files vendored
    verbatim from `github.com/hashicorp/terraform-plugin-go@v0.31.0` (per
    those files' own "copy this into your codebase" instruction), Go/gRPC
    stubs generated via protoc + protoc-gen-go + protoc-gen-go-grpc.
  - `provider/handshake.go`: parses the go-plugin handshake line. Magic
    cookie, core protocol version (1), verified against terraform-plugin-go's
    server source. App protocol version is no longer hardcoded to 6 — any
    version in `supportedAppProtocolVersions` (5, 6) is accepted, and
    `Launch` advertises both via `PLUGIN_PROTOCOL_VERSIONS` so a
    dual-protocol-capable plugin can pick the best mutually supported one.
  - `provider/provider.go`: protocol-agnostic `Provider` interface
    (`ProtocolVersion`, `Schema`, `Configure`, `ReadResource`) with two
    backing implementations, `v5Provider`/`v6Provider`, chosen by
    `newProvider(negotiatedVersion, conn)`. Callers never branch on protocol
    version.
  - `provider/schema.go`: protocol-agnostic `Schemas`/`Schema`/`Block`/
    `Attribute`/`NestedBlock` types, translated from either wire protocol's
    generated structs. Nested blocks are modeled recursively (not flattened)
    — required for real provider config encoding, see Surprises.
  - `provider/ctyvalue.go`: encodes/decodes DynamicValue payloads as
    cty-msgpack (via `github.com/zclconf/go-cty`, MIT-licensed — the same
    library the Terraform ecosystem itself uses for this), building the cty
    object type from a Block (attributes + nested blocks, recursively).
    ubx's own callers see plain JSON in/out; the cty/msgpack machinery is
    entirely internal.
  - `provider/client.go`: `Launch` now negotiates protocol version from the
    handshake and builds the matching `Provider`. Also raised the gRPC
    message size limit to 256MiB (`maxProviderMessageSize`, matching
    `grpcMaxMessageSize` in tf5server/tf6server) — the real AWS provider's
    full schema dump exceeds gRPC's 4MiB default.
  - `provider/internal/fakeprovider/`: fixture binary extended to serve
    either protocol (`ok-v5`/`ok-v6` modes) with a real gRPC server, plus a
    real ReadResource implementation that decodes/re-encodes cty-msgpack
    (not just echoing bytes) so the round trip is genuinely exercised.
    `unsupported-version` mode (reports app protocol 4) replaces the old
    `bad-app` mode, which reported v5 — no longer wrong now that v5 is
    supported.
  - Tests: `handshake_test.go` covers version-negotiation edge cases (v5
    accepted, v6 accepted, unsupported versions 4/7/99 rejected, non-numeric
    version malformed). `client_test.go` covers Launch happy-path for both
    protocols (schema dump + a real ReadResource round trip through cty
    encoding) plus all adversarial paths (binary missing, handshake timeout,
    core/app/wire protocol mismatch, malformed line, plugin exits early). All
    green (`go build ./...`, `go vet ./...`, `go test ./...`).
  - **Real-world verification** (manual harness, not part of the automated
    suite — same pattern used for the schema-dump verification last
    session): downloaded `terraform-provider-aws` 6.54.0 (darwin_arm64) and
    ran the full sequence — Launch → negotiated v5 → GetProviderSchema
    (1682 resource types) → Configure (region=us-east-1) → ReadResource
    against the real S3 bucket `ubx-states` in Roozbeh's own AWS account,
    using his already-configured `~/.aws/credentials`. Got back a fully
    populated, real bucket state (arn, region, versioning, server-side
    encryption config, grants, etc.) — a real, read-only, attributed-in-spirit
    infrastructure read via ubx's own protocol client, no Terraform/OpenTofu
    involved. This satisfies Slice 1's ReadResource exit bullet.
- 2026-07-10: docs/schema.md §Canonical hashing RATIFIED v1 (separate commit
  `schema: ratify canonical hashing v1`), incorporating the design-session
  amendments: numbers restricted to int64/decimal-strings (floats rejected
  at propose time); hash-excluded fields exactly `id`/`acceptance`/`status`;
  domain-separation prefix `ubx:proposal:v1\n`; `intent.sources[].content_hash`
  for dialogue/PR/issue tamper-evidence; `delta` arrays sorted
  lexicographically by `(stack, type, name)` instead of dependency order. Any
  further change now requires a schema_version bump + migration.
- 2026-07-10: Slice 2 completed — `core/` package (trust core).
  - `core/proposal.go`: typed `Proposal` per docs/schema.md, including the
    ratified `IntentSource.ContentHash` field. `Delta.Creates/Modifies/
    Destroys` are `[]json.RawMessage` (opaque), not typed IR nodes — see
    Next steps for why. (Modifies/Destroys' opaque shape was superseded the
    same day the shapes got pinned — see the entry below.)
  - `core/canonical.go`: canonical-hashing pipeline. Marshals the proposal,
    re-decodes with `json.Decoder.UseNumber()` (preserves int-vs-float
    literal shape), deletes the three excluded fields, sorts delta arrays,
    then walks the tree rejecting any float-shaped number (`.`/`e`/`E` in
    the literal, or an integer too big for int64) and converting surviving
    integers to `int64`. The final `map[string]interface{}`/`[]interface{}`
    tree is marshaled once — Go's `encoding/json` sorts map keys at every
    nesting level, which is what makes a single `Marshal` call produce
    JCS-style canonical output with no separate canonicalizer needed.
  - `core/hash.go`: `Hash(*Proposal) (string, error)` — domain-prefixed
    SHA-256 of the canonical bytes, full 64-hex-char digest (no truncation;
    a short display form is a presentation concern, not part of the ID).
  - `core/doublerun.go`: `DoubleRun(func() ([]byte, error))` — a standalone,
    reusable component per the session's explicit ask, not just inlined
    into Hash. Runs a computation twice, hard-fails on any byte mismatch.
    Meant to be reused later by the resolver, not just proposal hashing.
  - `core/ledger.go`: `Ledger` over `<dir>/ledger/proposals/<id>.prop.json` +
    `<dir>/.ubx/ledger.lock`, matching docs/schema.md's layout exactly.
    `Append` checks duplicate-ID before parent-match (a duplicate is a more
    specific, more useful error than "parent mismatch" once the head has
    already moved past it). `Head`/`Read` distinguish "doesn't exist yet"
    from "exists but corrupt" (`ErrCorruptLedgerEntry`/`ErrCorruptLedgerHead`)
    — never a panic, never silently wrong data.
  - `core/accept.go`: `Accept(*Ledger, *Proposal)` — computes the hash, fills
    in ID/Status/Acceptance (method "local": approver from `os/user`,
    UTC timestamp — no cryptographic signature; that's explicitly a later
    tier per docs/architecture.md), appends to the ledger.
  - `cli/accept.go`, `cli/why.go`: `ubx accept <proposal.json> [--ledger-dir
    dir]` and `ubx why <id> [--ledger-dir dir]`, wired into the root command.
  - Tests: `core/canonical_test.go` (hash stability across map/array
    ordering, float rejection incl. exponent-form and nested-config floats,
    decimal-string accepted, mutation detection, id/acceptance/status
    exclusion, domain-prefix sanity), `core/doublerun_test.go`,
    `core/ledger_test.go` (accept→append→read round trip, genesis/parent-
    chain/duplicate rejection, missing proposal, truncated proposal file,
    corrupted (non-JSON) proposal file, corrupted ledger-head file),
    `cli/proposal_flow_test.go` (full `ubx accept` → `ubx why` CLI round
    trip). All green (`go build ./...`, `go vet ./...`, `go test ./...`).
  - Manually verified via the built binary too (see transcript in this
    session): hand-written proposal → `ubx accept` → real ledger files on
    disk → `ubx why` printing intent/acceptance/blast-radius back out.
  - Added `.gitignore` (`.DS_Store`, `/ubx`, `/dist/`) — stray macOS files
    had started showing up untracked.
- 2026-07-10: Pinned `delta.modifies`/`delta.destroys` element shapes
  (docs/schema.md §"Delta element shapes — PINNED", separate commit
  `schema: pin delta element shapes`).
  - `core/proposal.go`: new `Address{Stack,Type,Name}` type (with
    `.String()` → `<stack>.<type>.<name>`, the canonical cross-reference
    form). `Delta.Destroys` is now `[]Address`; `Delta.Modifies` is now
    `[]Modification{Target Address, Before/After map[string]json.RawMessage}`
    (dot-notation keys, changed attributes only). `Delta.Creates` unchanged
    (still opaque `[]json.RawMessage` — no typed IR node yet).
  - `core/canonical.go`: `deltaSortKey` simplified now that all three delta
    shapes are pinned — no more guessing/fallback logic, just reads
    `stack`/`type`/`name` directly (from `target` for modifies elements).
  - `core/validate.go` (new): `Validate(*Proposal) error`. Cross-references
    every `delta.modifies` entry's target address against
    `resolution.inputs[].resource`, requiring a match with a non-empty
    `observed_hash`. Kind-specific rule for `KindAdoption`: all-zero
    blast_radius, empty modifies/destroys (creates may still be populated).
    Wired into `core.Accept`, called before `Hash` — an invalid proposal
    never gets hashed or reaches the ledger.
  - Tests: `core/validate_test.go` (modifies missing/wrong-address/empty-hash
    resolution.inputs, adoption blast-radius/modifies/destroys rules,
    non-adoption kinds unaffected, Accept rejects before hashing) plus new
    hash-stability cases in `core/canonical_test.go` for destroys/modifies
    array-order independence under the pinned shapes. All green.
- 2026-07-10: Slice 3 (UBI-7) completed — `ubx scan`, drift detection,
  adoption/drift_adopt proposal generation.
  - `core/observed.go`: `ObservedHash` — a permissive (floats allowed)
    canonical-JSON fingerprint of a provider's ReadResource result.
    Deliberately a separate pipeline from `Hash` (proposal hashing, which
    rejects floats) — this fingerprints real API data, not resolver-authored
    proposal content.
  - `core/state.go`: `Ledger.Chain()` (oldest-first walk of the whole
    chain), `Ledger.LastObservedHash(addr)` (most recent recorded
    observed_hash for one address), `Ledger.FoldState(addr)` (reconstructs
    an address's full current recorded state: seed from its adoption
    snapshot, replay every subsequent drift_adopt's after-diff on top —
    architecture.md's "current infrastructure = fold(applied proposals)",
    restricted to one resource). `diffAttributes`/`dotSet`: the dot-notation,
    changed-attributes-only diff the pinned Modification shape requires.
  - `core/scan.go` (imports `provider` — see Open decisions): `RunScan`
    (fetch schema → configure → read resource → fingerprint → classify
    against the ledger as new/drifted/unchanged, each step's failure
    wrapped so "provider errors mid-scan" is diagnosable), `GenerateProposal`
    (builds the zero-blast-radius `adoption`/`drift_adopt` proposal —
    adoption's `delta.creates` carries the full snapshot, drift_adopt's
    `delta.modifies` carries the real diff against `FoldState`'s
    reconstruction), `VerifyFreshness` (re-reads live state and compares
    against a proposal's recorded observed_hash — the staleness guard).
    `ErrResourceUnreadable`, `ErrUnknownResourceType`, `ErrStaleObservation`
    sentinels.
  - `core/validate.go`: extended `validateKind` for `KindDriftAdopt` —
    all-zero blast_radius and no destroys (record-only against the cloud,
    like adoption), but modifies IS expected (that's the whole point).
    docs/schema.md updated with a parallel "Drift-adopt proposals" note.
  - `provider/internal/fakeprovider/`: added `Configure`/`ConfigureProvider`
    implementations (previously unimplemented — fine for Slice 1/2's tests,
    which never called Configure, but `core.RunScan` always does).
  - `cli/scan.go` (new): `ubx scan --provider --stack --type --name --lookup
    [--provider-config] [--ledger-dir] [--out]`. Prints "no drift" and exits
    cleanly when unchanged; otherwise prints the classification and writes
    the generated proposal (stdout or `--out` file).
  - `cli/accept.go`: added optional `--reverify-with <provider-binary>`
    (plus `--resource-type`/`--resource-name`/`--lookup`/`--provider-config`)
    — when set, re-reads the resource live and refuses to accept
    (`ErrStaleObservation`) if it no longer matches what the proposal
    recorded, before any hashing/ledger work happens.
  - Tests: `core/scan_test.go` (new/drifted/unchanged classification via an
    in-memory fake — no subprocess needed at this layer — plus all the
    adversarial paths: unreadable resource, both `nil` and
    JSON `null` forms, provider errors at each of Schema/Configure/
    ReadResource, unknown resource type), `core/state_test.go` (diff
    correctness incl. nested paths/added/removed keys/atomic arrays,
    multi-level fold across two drifts, per-address isolation),
    `core/validate_test.go` (drift_adopt kind rules), `cli/scan_test.go`
    (full `ubx scan` → `ubx accept` → `ubx why` CLI round trip against the
    fakeprovider fixture, including the `--reverify-with` staleness block
    and its fresh-passes counterpart). All green (`go build ./...`,
    `go vet ./...`, `go test ./...`).
  - **Real-world verification, exactly as asked**: adopted the real
    `ubx-states` S3 bucket (`ubx scan` → "new" → `ubx accept`), tagged it
    directly via `aws s3api put-bucket-tagging` (a real out-of-band mutation
    ubx had no part in), scanned again — correctly classified as "drifted"
    with a generated `drift_adopt` proposal whose diff was exactly
    `{"tags.ubx-demo": "slice3", "tags_all.ubx-demo": "slice3"}` (both
    added, nothing else touched) — accepted it, and `ubx why` explained both
    the adoption and the drift resolution. Scanning a third time correctly
    reported "no drift." Bucket tags removed afterward to leave the real
    account as found.
- 2026-07-10: UBI-7 follow-up — resolved all three Slice 3 architectural
  flags instead of carrying them forward as open worries.
  - `core/scan.go`: replaced the `provider.Provider` parameter on `RunScan`/
    `VerifyFreshness` with a new `core.StateReader` interface (`Schema`
    returns `(providerSchema any, resourceSchemas map[string]any, error)`;
    `Configure`/`ReadResource` take `any` schema handles) — `core` no
    longer imports package `provider` anywhere.
  - `cli/stateadapter.go` (new): `stateReaderAdapter` wraps a
    `provider.Provider` to satisfy `core.StateReader`, type-asserting the
    `any` handles back to `*provider.Schema` at the boundary. `cli/scan.go`
    and `cli/accept.go` both go through `newStateReader(client.Provider)`
    now instead of passing `client.Provider` straight through.
  - `core/state.go`: `FoldState`'s doc comment now explicitly calls out the
    O(chain) linear walk as an accepted tradeoff at current scale, with a
    stated revisit trigger, rather than leaving it as an unresolved
    "worth reconsidering."
  - `core/proposal.go`: `ResolutionInput` gained `Lookup json.RawMessage`
    (`json:"lookup,omitempty"`). `core/scan.go`: `ScanResult` gained a
    `Lookup` field (populated from `ScanRequest.CurrentState` in `RunScan`);
    `GenerateProposal` writes it into the generated proposal's
    `resolution.inputs` entry; `VerifyFreshness` dropped its `currentState`
    parameter entirely, reading the lookup back from the proposal's own
    `resolution.inputs[].Lookup` instead.
  - `cli/accept.go`: removed the now-redundant `--lookup` flag from
    `--reverify-with` — it reads the lookup key the proposal already
    carries.
  - docs/schema.md: new "Amendment: persist resource lookup key" subsection
    (with the reasoning for why this doesn't need a schema_version bump,
    unlike the RATIFIED hashing rules or PINNED delta shapes), plus the
    `resolution.inputs` example updated to show `lookup`.
  - `provider/internal/fakeprovider/`: added `FAKEPROVIDER_EXTRA_TAG`
    ("key=value") — merges an extra tag into ok-v5/ok-v6's ReadResource
    response regardless of current_state, so a test can simulate a real
    out-of-band mutation between two separate process launches that pass
    the *same* lookup both times (varying `--lookup` itself, the previous
    test trick, no longer models "reality changed" now that lookup is
    persisted and reused automatically at reverify time).
  - Tests: `core/scan_test.go`'s `fakeProvider` now implements
    `core.StateReader` directly (no `provider` import at all — a stronger
    proof the dependency inversion actually worked, not just compiles);
    `cli/scan_test.go`'s staleness tests rewritten around
    `FAKEPROVIDER_EXTRA_TAG` instead of varying `--lookup`, plus a new
    `TestGenerateProposal_PersistsLookup` confirming the round trip. All
    green (`go build ./...`, `go vet ./...`, `go test ./...`).
- 2026-07-10: UBI-8 completed — provider binary acquisition (download,
  verify, cache).
  - `provider/source.go`: `Source{Hostname,Namespace,Type}` +
    `ParseSource` — parses both `"hashicorp/aws"` (hostname defaults to
    `registry.terraform.io`, Terraform's own default) and the fully
    qualified form.
  - `provider/registry.go`: registry protocol client. Verified live against
    registry.opentofu.org (`GET /.well-known/terraform.json`, then `GET
    /v1/providers/hashicorp/aws/6.54.0/download/darwin/arm64`) rather than
    assumed from memory — response shape (`filename`, `download_url`,
    `shasums_url`, `shasums_signature_url`, `signing_keys.gpg_public_keys[]
    .ascii_armor`) matches exactly what got implemented.
  - `provider/verify.go`: signature + checksum verification, using
    `github.com/ProtonMail/go-crypto/openpgp` (MIT/BSD-3-style — the
    maintained fork; `golang.org/x/crypto/openpgp` is frozen/deprecated).
    Confirmed live that `*_SHA256SUMS.sig` is a raw binary detached
    signature, not ASCII-armored — used `openpgp.CheckDetachedSignature`,
    not the Armored variant. Verifies signature over the SHA256SUMS file
    first, then extracts the expected digest for the requested platform's
    filename from that (signature-covered) content — never trusts the
    registry response's bare top-level `shasum` field alone, since that
    field isn't itself signed.
  - `provider/cache.go`: `~/.ubx/providers/<hostname>/<namespace>/<type>/
    <version>/<os_arch>/` cache and `UBX_PROVIDER_MIRROR` local-directory
    override, sharing one "exactly one file in this directory" convention
    (`findSingleFile`) for both — simpler than agreeing on Terraform's
    upstream archive filename convention ahead of time, and lets an
    operator hand-populate a mirror with just the extracted binary.
  - `provider/acquire.go`: `Acquire(ctx, src, version, opts...)` —
    mirror → cache → registry (download SHA256SUMS + signature, verify,
    download archive, verify its checksum, extract) — resolution order,
    each a documented, deliberate fallthrough not an error. Explicit
    version only, no "latest" resolution (`WithHTTPClient`/
    `WithRegistryAPIBase`/`WithCacheRoot`/`WithPlatform` options exist
    purely for tests).
  - `core/proposal.go`/`core/scan.go`: `ResolutionInput` gained
    `ProviderChecksum string` (`"sha256:<hex>"`); `ScanRequest`/`ScanResult`
    thread it through as a plain opaque string (core still doesn't import
    `provider` — see the UBI-7 follow-up inversion, unaffected by this).
  - `cli/providerresolve.go` (new): `resolveProviderBinary` — exactly one
    of `--provider <path>` (unchanged manual/dev workflow) or `--source`+
    `--provider-version` (new: calls `provider.Acquire`, returns its
    checksum) — shared by `cli/scan.go` and `cli/accept.go`
    (`--reverify-with`/`--reverify-source`+`--reverify-provider-version`).
  - docs/architecture.md: new "Provider binary acquisition (UBI-8)"
    subsection — registry choice rationale (ToS risk avoided; OpenTofu
    mirrors the same protocol), verification model, cache/mirror layout,
    explicit-version-only rule, attribution via `provider_checksum`.
    docs/schema.md: matching "Amendment: record verified provider binary
    checksum" (additive/optional, no schema_version bump, same reasoning
    as the UBI-7 lookup-key amendment).
  - Tests (`provider/acquire_test.go`): a throwaway OpenPGP keypair signs
    fixture `SHA256SUMS` content exactly like a real registry would, served
    from an `httptest.Server`. Covers the happy path; corrupted download
    (truncated archive vs. its signed checksum); bad checksum (SHA256SUMS
    itself wrong, still validly signed — the signature can't save a wrong
    checksum); bad signature two ways (signed by the wrong key, and
    corrupted signature bytes); missing platform (404); mirror hit with no
    network call possible (unreachable API base proves it); mirror miss
    correctly falling through to network; cache hit on a second call with
    no second network call (same unreachable-API-base proof). All green.
  - **Real-world verification, exactly as asked**: cleared `~/.ubx/providers`
    and ran `ubx scan --source hashicorp/aws --provider-version 6.54.0`
    against the real `ubx-states` bucket — real network round trip to
    registry.opentofu.org, real SHA256SUMS + OpenPGP signature
    verification, real extraction, cached at
    `~/.ubx/providers/registry.terraform.io/hashicorp/aws/6.54.0/
    darwin_arm64/terraform-provider-aws`, and the generated proposal's
    `resolution.inputs[].provider_checksum` correctly populated
    (`sha256:4b74277739913f...`). A second identical scan completed in
    ~5s instead of ~35s, confirming the cache hit (no second download).
    This surfaced a real bug (see Surprises) that got fixed before this
    counted as verified.
- 2026-07-10: UBI-9 session 1 — per-type conformance harness + the ~50-type
  list, batch 1 (3 of 51 types).
  - `docs/plan.md`: new "M1-2 resource type list (UBI-9)" section under
    §Wedge buildout — the ~50 types, categorized (compute/network/iam/
    storage/db/dns/messaging), each marked real-safe or fake-only with
    rationale. 51 types total (`conformance.Registry`'s exact count) —
    "~50" was always approximate, not a hard target.
  - `conformance/registry.go` (new package): `Safety` (`RealSafe`/
    `FakeOnly`), `TypeSpec{Type, Category, Safety, IdentityFields, Notes,
    Implemented}`, `Registry` (the 51-entry list, matching docs/plan.md
    exactly), `ByType`. `IdentityFields`/`Notes` populated only for
    `Implemented` types — enforced by a registry test (see below), so an
    entry can't claim a quirk without actually having verified it.
  - `conformance/harness.go`: `RunAdoptMutateScanDiff` — scan (expect new)
    → accept the adoption → caller's `Mutate` callback → scan again (expect
    drifted) → accept the drift_adopt → scan a third time (expect
    unchanged), against a fresh per-call ledger in `t.TempDir()`. Fully
    reusable across both real-safe and fake-only types — only the
    `ProviderPath`/`Mutate` differ. `RequireLive` gates real-AWS tests
    behind `UBX_CONFORMANCE_LIVE=1`, skipping (not failing) otherwise —
    `go test ./...` stays hermetic project-wide.
  - `conformance/aws_live_test.go`: the 3 seeded types.
    - `aws_s3_bucket` (storage): reuses the real `ubx-states` bucket
      (proven since UBI-7), now via the harness instead of a manual
      transcript.
    - `aws_iam_role` (iam): adopts the account's real, pre-existing
      `aws-codestar-service-role`. Needs `id`+`name` both set in the
      lookup — `name` alone reads back null, confirmed empirically (an
      initial guess that "just name" would work was wrong and caught
      before it went in the registry — see Surprises).
    - `aws_vpc` (network): adopts the account's real default VPC
      (`vpc-b75be9cd`). Needs only `id` — the framework-style/simpler
      convention, unlike the two SDKv2-style types above.
    - All three acquire the real AWS provider via `provider.Acquire`
      (dogfooding UBI-8, not a manual scratch download), mutate via a real
      `aws` CLI tag call, and clean up via `t.Cleanup` — verified the
      account was left exactly as found (`aws s3api get-bucket-tagging` /
      `aws iam list-role-tags` / `aws ec2 describe-tags` all empty again
      after the run).
  - `conformance/registry_test.go`: no-duplicate-types, valid-category,
    every `Implemented` entry has `Notes`+`IdentityFields`, `ByType` hit/
    miss, and a `40 <= len(Registry) <= 60` sanity bound on the "~50" scope.
  - All green (`go build ./...`, `go vet ./...`, `go test ./...` — live
    tests correctly skip without `UBX_CONFORMANCE_LIVE=1`), plus a real run
    with it set: `UBX_CONFORMANCE_LIVE=1 go test ./conformance/... -run
    TestConformance -v` — all three passed against the real account.
- 2026-07-10: UBI-9 batch 2 — four more real-safe types implemented, one
  type investigated and parked.
  - Verified each type's exact `ReadResource` lookup shape empirically
    (via the same ad hoc lookup-checker script pattern as batch 1) before
    writing anything into the registry, same discipline as always:
    `aws_sqs_queue` needs only `{"id": "<queue-url>"}`; `aws_sns_topic` and
    `aws_iam_policy` need only `{"id": "<arn>"}`; `aws_iam_user` needs
    `id`+`name` both (same shape as `aws_iam_role`); `aws_iam_group` also
    needs `id`+`name` for the adopt half, but see below.
  - `conformance/registry.go`: `aws_sqs_queue`, `aws_sns_topic`,
    `aws_iam_policy`, `aws_iam_user` marked `Implemented: true` with
    verified `IdentityFields`/`Notes`. `aws_iam_group` stays
    `Implemented: false` but gained a detailed `Notes` entry explaining
    why it's parked: no `aws iam tag-group` API exists at all (checked by
    trying it, not assumed), and the schema itself has no other
    mutable-and-observable field (path is immutable after creation, no
    tags field) — there's no real out-of-band mutation to test drift
    detection against, so the mutate half of adopt→mutate→scan-diff has
    nothing to stand on for this type without a fakeprovider fixture.
  - `conformance/aws_live_test.go`: `runAWSOutput` (capture stdout, for
    commands whose result — a queue URL, an ARN — the test needs
    afterward) and `uniqueName` (timestamp-suffixed names, since these four
    types create-and-destroy a fresh fixture per run rather than adopting
    something already there like batch 1). Four new test functions
    following the same create → `RunAdoptMutateScanDiff` → `t.Cleanup`
    shape.
  - docs/plan.md: §M1-2 list updated (7 ✓, one ⚠ parked with a symbol
    distinguishing it from "not yet attempted"), plus a changelog entry.
  - All green (`go build ./...`, `go vet ./...`, `go test ./...` — live
    tests skip by default). Real run:
    `UBX_CONFORMANCE_LIVE=1 go test ./conformance/... -run TestConformance
    -v` — all 7 implemented types passed against the real account.
    Confirmed after the run that every created fixture (SQS queue, SNS
    topic, IAM policy, IAM user) was actually deleted and every tag
    (S3/IAM role/VPC from batch 1, plus SQS/SNS/policy/user tags from
    batch 2) was actually removed — checked via `aws` CLI queries, not
    assumed from `t.Cleanup` existing.
- 2026-07-10: UBI-9 batch 3 — closed the milestone: all remaining 43 types
  resolved (41 fixture-verified, 2 newly parked), completing the 51-type
  list at 48 verified / 3 parked / 0 pending.
  - `cmd/schemadump` (throwaway, deleted before this commit): launches the
    real cached AWS provider and dumps `GetProviderSchema`'s attribute list
    (name + required/optional/computed flags) for a list of type names — no
    Configure call, no credentials, no AWS API round trip, so safe/free to
    run against every remaining type at once. Ran it once against all 43
    types to get real, schema-verified identity/mutable-field data before
    writing anything into the registry — same "verify before recording"
    discipline as batches 1-2's ad hoc lookup-checker script, applied to
    schema inspection instead of a live `ReadResource` call.
  - `provider/internal/fakeprovider/main.go`: two new modes,
    `conformance-v5`/`conformance-v6`. Unlike the existing fixed
    `fake_widget` schema (ok-v5/ok-v6, unchanged), these serve a schema
    built entirely from env vars: `FAKEPROVIDER_RESOURCE_TYPE` (the type
    name to advertise — must match the test's `Address.Type`, since
    `core.RunScan` looks up `resourceSchemas[addr.Type]`),
    `FAKEPROVIDER_ATTRS` (comma-separated attribute names — "tags"/
    "tags_all" become string maps, everything else a plain string;
    scalar type-fidelity to AWS's real attribute types doesn't matter since
    ubx's own core layer treats `ReadResource`'s output as opaque JSON, per
    `core/scan.go`/`core/state.go`, never type-checked against schema),
    `FAKEPROVIDER_MUTATE_ATTR`/`FAKEPROVIDER_MUTATE_VALUE` (which attribute
    to change on the next `ReadResource` call — map-typed attributes get
    `key=value` merged in, same convention `FAKEPROVIDER_EXTRA_TAG` already
    used; everything else gets replaced directly). One mechanism serves all
    41 types, driven by data, not 41 separate schemas.
  - `conformance/harness.go`: `AdoptMutateScanDiffConfig` gained
    `ProviderEnv []string`, threaded into `provider.Launch` via
    `provider.WithEnv` — static per-run env (the three above) for FakeOnly
    cases; `RealSafe` cases leave it empty. `FAKEPROVIDER_MUTATE_ATTR`/
    `_VALUE` are set dynamically from within each case's `Mutate` callback
    via `t.Setenv` (auto-restoring, unlike the manual `os.Setenv`+
    `t.Cleanup` pattern `FAKEPROVIDER_EXTRA_TAG` needed) — each scan launches
    a fresh subprocess that reads its env at call time, so this changes what
    only the second/third scan see, exactly like `FAKEPROVIDER_EXTRA_TAG`
    already proved out in UBI-7's follow-up.
  - `conformance/fake_test.go` (new): a `fakeConformanceCase` table (41
    entries: `Type`, `Attrs`, `MutateAttr`, `MutateValue`) instead of 41
    hand-written Go test functions — the registry's own table-driven ethos
    applied to the test file, not just the type list. `stdCase(type)` covers
    the overwhelmingly common shape (`id`/`arn`/`tags`/`tags_all`, mutate
    `tags`); seven types needed a bespoke entry because their real schema
    genuinely lacks `arn`/`tags` (see below). `TestConformance_FakeOnly`
    runs `RunAdoptMutateScanDiff` per case via `t.Run`; a second test,
    `TestFakeConformanceCases_MatchRegistry`, cross-checks the table against
    `conformance/registry.go` both directions (every case must be a
    `FakeOnly`+`Implemented` registry entry; every such entry must have a
    case) so the fixture and the registry can't silently drift apart. New
    package-level `TestMain` builds the `fakeprovider` binary once, same
    pattern `provider/client_test.go` already established.
  - Special-shaped cases, each individually schema-verified rather than
    forced into the standard shape: `aws_route` (no arn/tags; mutates
    `gateway_id`), `aws_nat_gateway` (no arn; mutates `tags`),
    `aws_security_group_rule` (no arn/tags; mutates `description`),
    `aws_s3_bucket_policy` (no arn/tags; mutates `policy`, the actual JSON
    document — the real-world drift vector for this type),
    `aws_s3_bucket_versioning` (real schema nests the mutable field inside
    a `versioning_configuration` block; fixture flattens it to a `status`
    attribute — documented as a deliberate simplification, since what's
    being tested is ubx's diff pipeline on opaque JSON, not nested-block
    wire fidelity, which is already proven elsewhere against a real
    provider via `provider/ctyvalue.go`), `aws_s3_bucket_public_access_block`
    (mutates `block_public_acls`), `aws_route53_record` (no arn/tags;
    mutates `ttl`).
  - Two more types found to have no genuine mutable-and-observable field at
    all, discovered via the schema dump rather than a live API call:
    `aws_iam_role_policy_attachment` (`{id, policy_arn (required), role
    (required)}` — a pure join, nothing optional besides `id`) and
    `aws_route_table_association` (`{gateway_id, id, region, route_table_id
    (required), subnet_id}` — same join shape; picking a target is a
    replace in AWS's own model, not an in-place modify). Parked in
    `conformance/registry.go` alongside `aws_iam_group`, same reasoning.
  - `conformance/registry.go`: `FakeOnly`'s doc comment now states
    explicitly what a FakeOnly entry's `Implemented: true` does and does not
    prove (see Open decisions above) — not left as something a reader has to
    infer. All 43 remaining entries updated: 41 with verified
    `IdentityFields`/`Notes`/`Implemented: true`, 2 newly parked with
    `Notes` explaining why (`Implemented` stays `false`).
  - `conformance/registry_test.go`: new `TestRegistry_NoThirdState` — every
    entry must have either `Implemented: true` or non-empty `Notes`;
    enforces UBI-9's own completion criterion going forward, not just for
    this session's count.
  - docs/plan.md: §M1-2 list rewritten to final reality (every type ✓ or
    ⚠, none unmarked), plus a changelog entry explaining the methodology,
    not just the count.
  - All green: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty),
    `go test ./...` (all 41 new fake-only tests run un-gated, ~2s total,
    no `UBX_CONFORMANCE_LIVE` needed). Also re-ran the full real-account
    suite once more with `UBX_CONFORMANCE_LIVE=1 go test ./conformance/...
    -run TestConformance -v` — all 48 implemented types (7 real + 41 fake)
    passed; confirmed via direct `aws` CLI queries afterward that the
    account was left exactly as found (no bucket/role/VPC tags, no
    lingering SQS queue/SNS topic/IAM policy/user from any batch).
- 2026-07-10/11: UBI-10 completed — CloudTrail attribution wired into
  `ubx scan`'s drift-proposal generation.
  - docs/schema.md: new "Amendment: CloudTrail attribution intent sources"
    subsection — two new `intent.sources[].kind` values, `cloudtrail`
    (`event_id`/`event_name`/`event_time`/`actor_arn`/`source_ip`/
    `session_context`, plus the existing `ref`/`content_hash`) and
    `cloudtrail_unattributed` (`reason`: `no_matching_event` |
    `delivery_window` | `not_logged`), both attached to `drift_adopt`
    proposals only. Additive/optional, no `schema_version` bump — same
    reasoning as the lookup-key/provider-checksum amendments.
  - `core/proposal.go`: `IntentSource` gained the new fields above
    (`omitempty` throughout; existing dialogue/manual_edit/issue sources
    completely unaffected).
  - `core/attribution.go` (new): `CloudTrailEvent` (core's own plain-Go
    view of one event — no AWS SDK), `EventLookup` interface (mirrors
    `core.StateReader`'s dependency inversion for the tfplugin provider
    client), `AttributeDrift` (the deterministic decision logic —
    `identityCandidates` derives search values from the resource's own
    observed `id`/`arn`/`name`, tried in that order; `filterExactMatch`
    defends against a lookup returning events for a similarly-named-but-
    different resource; `cloudTrailSources` sorts matches newest-first),
    reason constants, `cloudTrailDeliveryLag` (15 min). `core/state.go`
    gained `Ledger.LastObservationTime(addr)`, mirroring
    `LastObservedHash` but returning the resolved_at of the proposal that
    last recorded addr — the correlation window's "since" bound.
  - `cloudtrail/` (new package): `Client`, the only place in this codebase
    that imports an AWS SDK (`aws-sdk-go-v2`) directly. `New(ctx, region)`
    loads AWS config the standard way (no credential-discovery
    reinvention); `LookupEvents` calls the real `LookupEvents` API with a
    `ResourceName` lookup attribute, paginates, and parses each event's
    nested `CloudTrailEvent` JSON record (not just the flat SDK fields,
    which lack actor ARN/source IP/session context) into
    `core.CloudTrailEvent`.
  - `cli/attribution.go` (new): `attributeDrift` — reads the correlation
    window from the ledger and the just-generated proposal's own
    `resolved_at`, builds a `cloudtrail.Client` for the provider config's
    region, calls `core.AttributeDrift`, appends the result to the
    proposal's `Intent.Sources`. Every failure path (can't build a client,
    lookup errors) degrades to a `cloudtrail_unattributed`/`not_logged`
    source rather than propagating an error — best-effort all the way out
    to the CLI, not just inside `core.AttributeDrift`.
  - `cli/scan.go`: new `--no-attribution` flag; `attributeDrift` is called
    right after `GenerateProposal`, only when `res.Outcome ==
    core.ScanDrifted` (attribution only means something once a drift is
    already detected) and the flag isn't set.
  - **Two empirical corrections, both caught before they shipped wrong**
    (see Surprises for the full detail): CloudTrail's `ResourceName`
    lookup attribute wants the resource's `id` (bucket name/role name/
    vpc-id), not its ARN — confirmed by testing both against the real
    account before writing `identityCandidates`, not assumed; and real
    CloudTrail delivery latency in this account measured ~2-3 minutes,
    not the near-instant a first manual probe happened to show, which
    surfaced when the live test's initial 15-second retry budget wasn't
    enough and had to be widened to 5 minutes.
  - Tests: `core/attribution_test.go` — single match, multiple matches
    (newest-first ordering), no_matching_event, delivery_window (narrow
    window), not_logged (two distinct failure inputs — API error and a
    "no visibility" error — both map to the same reason), a
    similar-name-different-resource case proving `filterExactMatch`
    rejects it, an id-fails/arn-succeeds fallback case, and a table test
    for `identityCandidates` itself (dedup, fallback, malformed input).
    `cli/attribution_test.go`:
    `TestScan_AttributionDegradesGracefully_NoCredentials` — blanks every
    AWS credential source (env vars, config/credentials file paths
    pointed at nonexistent files, IMDS disabled) so credential resolution
    fails synchronously with no real network call, proving the CLI wiring
    degrades to `cloudtrail_unattributed`/`not_logged` without blocking
    `ubx scan`, and stays hermetic (0.36s, confirmed no network I/O).
    `cli/scan_test.go`'s existing `TestScanAcceptWhy` drift scan updated
    to pass `--no-attribution` — without it, that test would have made a
    real, credentialed CloudTrail call on every `go test ./...`, breaking
    the hermetic-by-default invariant this project has held since Slice 1.
  - `cli/attribution_live_test.go` (new): `TestScan_AttributesRealDrift_LiveCloudTrail`,
    gated behind `UBX_CONFORMANCE_LIVE=1` like every other real-account
    test. Tags the real `ubx-states` bucket, runs `ubx scan` through the
    actual CLI (no `--no-attribution`), retries (up to 5 minutes, 20s
    apart — sized from the measured real delivery latency, not guessed)
    until a `cloudtrail`-kind source appears, and asserts its `actor_arn`
    matches `aws sts get-caller-identity`'s real ARN. Cleans up the tag
    via `t.Cleanup`.
  - All green: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty),
    `go test ./...` (hermetic, ~2s, confirmed no network I/O). Live run:
    `UBX_CONFORMANCE_LIVE=1 go test ./cli/... -run
    TestScan_AttributesRealDrift_LiveCloudTrail -v` passed in 137s;
    confirmed afterward via `aws s3api get-bucket-tagging` that the
    account was left exactly as found (`NoSuchTagSet`).
  - go.mod: added `github.com/aws/aws-sdk-go-v2`,
    `.../config`, `.../service/cloudtrail` (direct deps) plus their
    transitive requirements — the first AWS SDK dependency in this
    codebase; every prior AWS interaction went through either the
    tfplugin provider protocol (`provider/`) or a subprocess `aws` CLI
    call (test-only). CloudTrail's `LookupEvents` has no tfplugin
    equivalent — it's a plain AWS management API, not a Terraform
    provider concern — so a direct SDK client, isolated to the new
    `cloudtrail/` package, was the right scope for this dependency rather
    than trying to force it through either existing mechanism.
- 2026-07-11: UBI-11 completed — `ubx why` polish (resource-address
  support, attribution rendering) ahead of demo recording.
  - `core/proposal.go`: new `ParseAddress(s string) (Address, bool)` — the
    inverse of `Address.String()`. Splits on the first two dots only
    (`strings.SplitN(s, ".", 3)`, not `strings.Split`), so a resource name
    that itself contains a dot round-trips correctly; `ok` is false unless
    all three components are non-empty.
  - `core/state.go`: new `Ledger.ProposalsForAddress(addr Address)
    ([]*Proposal, error)` — walks `Chain()` (same pattern as
    `LastObservedHash`/`LastObservationTime`) collecting every proposal
    with a `resolution.inputs` entry whose `resource` matches `addr`'s
    canonical string form. Returns proposals in chain (oldest-first)
    order; an address the ledger has never recorded returns an empty
    slice with no error — "not found" is a decision left to the caller's
    layer (`ubx why` treats it as an error; a future caller might not).
  - `cli/why.go`: argument dispatch now checks a 64-hex-char regex first
    (unchanged proposal-ID path, `renderProposal` — verified
    byte-identical output for every pre-existing intent-source kind) and
    falls back to `core.ParseAddress`; a string that's neither reports
    `"%q is not a valid proposal ID (64-char hex) or resource address
    (<stack>.<type>.<name>)"`. A resolved address prints a one-line
    summary (`<addr>: N proposal(s), newest first`) then one compact
    block per proposal (`renderProposalCompact`: kind, a presentation-only
    truncated id via new `shortID` — never used to look anything back up
    — `resolved_at`, intent summary, then the same attribution rendering
    as the full view).
  - `cli/why.go`: `renderIntentSource` replaces the old bare
    `kind ref (content_hash=...)` line for two kinds specifically:
    `cloudtrail` now prints `source: cloudtrail -- <actor_arn>
    <event_name> at <event_time> from <source_ip>` followed by an
    indented `event <id> (content_hash=...)` detail line;
    `cloudtrail_unattributed` prints `source: cloudtrail_unattributed --
    <reason in words>` via new `unattributedReason`, which maps each of
    the three schema reasons to a sentence (falling back to the raw
    string for anything unrecognized, so a future reason never renders as
    nothing). `dialogue`/`manual_edit`/`issue` (and any other kind) render
    exactly as before — same format string, same indent.
  - Tests: `core/proposal_test.go` (new) — `ParseAddress` table test
    (simple, name-containing-dots, missing/empty components, a bare
    64-hex string with no dots correctly failing to parse as an address)
    plus a round-trip-through-`String()` case. `core/state_test.go` — 
    `TestLedger_ProposalsForAddress_ChainOrder` (adopt then drift via
    `fakeProvider`, confirms both proposals returned in chain order) and
    `TestLedger_ProposalsForAddress_UnknownAddressIsEmptyNotError`.
    `cli/why_test.go` (new) — `TestWhy_ResourceAddress_ChainOrdering`
    (real scan→accept→scan→accept sequence through the CLI, confirms the
    drift proposal's short id renders before the adoption's),
    `TestWhy_ResourceAddress_Unknown`, `TestWhy_InvalidArgument` (neither
    id nor address), and `TestWhy_RendersAttributedCloudTrailSource`/
    `TestWhy_RendersUnattributedReasonInWords` against a hand-written
    drift_adopt proposal carrying one of each new source kind (same
    hand-written-JSON pattern `TestAcceptThenWhy` already established).
  - **Verified by hand against the built binary**, not just the test
    suite (this was explicitly a "how does it look on camera" pass):
    built a real two-entry chain via `ubx scan`/`ubx accept` against the
    fakeprovider fixture and confirmed
    `ubx why payments.fake_widget.demo-widget` renders
    ```
    payments.fake_widget.demo-widget: 2 proposal(s), newest first
    - drift_adopt 4e7c88296758… (2026-07-11T10:53:37Z): record drift on payments.fake_widget.demo-widget observed outside the ledger
    - adoption b25c8affa2ca… (2026-07-11T10:53:37Z): adopt existing payments.fake_widget.demo-widget into the ledger (discovered by scan)
    ```
    and hand-accepted a proposal carrying one `cloudtrail` and one
    `cloudtrail_unattributed` source, confirming:
    ```
      source: cloudtrail -- arn:aws:iam::839333509514:user/roozbeh PutBucketTagging at 2026-07-10T21:42:30Z from 93.228.76.41
        event 9910b32a-2f22-44b9-8d18-88cd3b95841a (content_hash=sha256:deadbeef)
      source: cloudtrail_unattributed -- too recent for CloudTrail to have delivered a matching event yet
    ```
  - All green: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty),
    `go test ./...` (hermetic, all packages pass).
- 2026-07-11: UBI-11 (real, Linear-verified) — docs landed, Stage 1
  (PR-merge acceptance binding) implemented and verified live.
  - Verified the ticket against Linear before starting, per the session's
    own explicit instruction ("verify against Linear title... do not
    infer other IDs"): `UBI-11` = "M3–4 decision loop: adopt/revert
    proposals, PR-merge signing, .tf write-back," status Backlog before
    this session. This caught that the *previous* session's use of
    "UBI-11" for the `ubx why` polish was an unverified guess — see the
    correction note under Current phase and the Surprises entry below.
  - **Docs landed first, as its own commit** (`docs: UBI-11 -- land the
    decision-loop design before implementation`), before any code:
    `docs/architecture.md`'s new "Decision loop (UBI-11)" section covers
    all three stages — PR-merge acceptance's derived-never-asserted
    principle and full flow, `.tf` write-back's narrow scope (literal
    attributes only, decline on any expression, hclwrite for surgical
    edits), and the GitHub App skeleton's security story (read-only
    permissions suffice because acceptance is derived, not asserted — the
    App never needs write access to apply anything because it never
    applies anything). `docs/schema.md`'s new "pr_merge acceptance
    fields" amendment: `acceptance.pr_number`/`acceptance.proposal_file`
    (additive, no `schema_version` bump — `acceptance` is entirely
    excluded from the content hash already), plus the `ubx-proposal:
    <hash>` PR-body trailer convention pinned as part of what "derived"
    means in practice.
  - **Stage 1 implementation** (`core,cli,github: UBI-11 stage 1 --
    PR-merge acceptance binding`):
    - `core/proposal.go`: `Acceptance` gains `PRNumber`/`ProposalFile`.
    - `core/accept.go`: `Accept` (local tier) refactored to share a
      `validateAndHash`/`finalizeAndAppend` preflight/finalize pair with
      new `AcceptFromMerge` (pr_merge tier). `AcceptFromMerge` takes a new
      `MergeAcceptance{MergeSHA, PRNumber, ProposalFile, Approvers}`
      struct plus the trailer's claimed hash, recomputes `Hash(p)`, and
      returns `ErrTrailerHashMismatch` on any disagreement — the actual
      enforcement of "derived, never asserted," not just a design
      statement. `Approvers` may be empty; that's accepted, not rejected.
    - `github/` (new package, alongside `cloudtrail/` the only two places
      in this codebase that talk to an external system directly):
      `git.go` (`CommitExists`/`FileAtCommit`, shelling out to the real
      `git` binary — see Open decisions for why not a git library),
      `client.go` (wraps `google/go-github/v78`: `pullRequestForCommit`,
      exported `ApprovingReviewers` computing latest-review-per-user so a
      withdrawn approval never counts), `trailer.go`
      (`ParseProposalTrailer`, the `ubx-proposal: <hash>` regex),
      `derive.go` (`DeriveAcceptance` ties git+API together into the one
      call `cli/accept.go` makes).
    - `cli/propose.go` (new): `ubx propose <file>` prints the trailer
      line for a draft proposal — no ledger interaction at all, since
      this runs before any PR exists to embed the hash into.
    - `cli/accept.go`: `--from-merge <sha> --repo-dir --proposal-file
      --github-repo` derives and records `pr_merge` acceptance end to
      end; the existing local-file path is completely unchanged (same
      `core.Accept` call as before).
    - `cli/why.go`/`cli/verify.go` (new): `--verify-acceptance
      [--repo-dir] [--github-repo]` re-runs the git-history+hash check
      (hard failure) and, given `--github-repo`, the reviewer re-check
      (reported, not fatal, on mismatch) — see Open decisions for why
      those two checks fail differently.
    - `UBX_GITHUB_API_BASE_URL` (test-only env seam, same convention as
      `UBX_PROVIDER_MIRROR`): points the GitHub client at an
      `httptest.Server` instead of the real API, so nothing in this
      codebase's test suite ever makes a real GitHub API call by default.
    - Tests, matching every adversarial case the task named: merge SHA
      not in history, proposal file absent from the merge, hash mismatch
      between trailer and file, unreviewed merge (recorded with empty
      approvers), a withdrawn approval no longer counting, and
      re-verification catching a commit rewritten out of history — all
      hermetic (`github/`'s own tests use real throwaway local git repos,
      since read-only git plumbing needs no mocking, plus an
      `httptest`-served fake GitHub API for the reviews/PR-lookup legs;
      `cli/`'s tests wire the same fakes through the actual command
      layer).
  - **Verified live, for real, on `Ubiquex/ubiquex-cli`** (stopped and
    asked first — see Open decisions/the correction note above for why
    this one got a check-in when UBI-10's live verification didn't):
    opened PR #1 from a scratch branch (`verify/ubi-11-stage1-live`)
    carrying a real `ubx propose`-computed trailer, merged it for real
    (`gh pr merge --merge`, merge SHA `ef89992dafad05c811f5766b091db6742432e417`),
    then ran the real `ubx accept --from-merge` against it with a real
    `GITHUB_TOKEN` (`gh auth token`). Output:
    ```
    accepted 2d9ad652b14614b8c265633f8afe6c011d49d413ec85264d988dbc57fd475704 (stack verify) via PR #1, 0 approver(s)
    ```
    0 approvers because nobody reviewed the scratch PR — a genuine live
    instance of "unreviewed merge recorded, not blocked," not a
    fabricated test case. `ubx why <id> --verify-acceptance --repo-dir .
    --github-repo Ubiquex/ubiquex-cli` against the same real commit/PR
    confirmed both legs:
    ```
    git: merge commit ef89992dafad05c811f5766b091db6742432e417 exists in .
    git: scratch/ubi11-verify-proposal.json at that commit still hashes to 2d9ad652b14614b8c265633f8afe6c011d49d413ec85264d988dbc57fd475704
    github API: PR #1's approvers are unchanged ([])
    ```
    Cleaned up immediately after: `git revert -m 1` the merge commit on
    `main` (removing the scratch fixture file), pushed; deleted
    `verify/ubi-11-stage1-live` locally and on the remote. Confirmed
    afterward (`git status`, `ls scratch/`) that nothing scratch-related
    remained.
  - All green: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty),
    `go test ./...` (hermetic — confirmed no network calls: `github/`'s
    and `cli/`'s new tests all pass with no `GITHUB_TOKEN`/network
    reachable, using real local git repos and fake HTTP servers only).
- 2026-07-11: UBI-11 Stage 2 completed — `.tf` write-back.
  - Verified the core mechanism empirically before writing any real code,
    per the project's standing discipline (three throwaway experiments,
    deleted before committing — same disposable-tool pattern used for
    `cmd/schemadump` in UBI-9): (1) `hclsyntax.Expression.Value(nil)`
    correctly distinguishes literals from expressions across every shape
    tried — plain string/number/bool, a plain (non-interpolated) template
    string, an object/list literal whose members are all literal, a
    variable reference, a function call, and a template *with*
    interpolation — the last of these confirming that a quoted string
    isn't automatically "safe" just because it's a string literal
    syntactically. (2) exact byte-range splicing (via `hcl.Range.Start/
    End.Byte`) plus `hclwrite.TokensForValue` for rendering the
    replacement reproduces a file with only the target range changed,
    confirmed by re-parsing the result and checking an untouched inline
    comment survived. (3) `ctyjson.ImpliedType`/`Unmarshal` (already a
    transitive dependency via `go-cty`, no new import needed) correctly
    turns a `Modification.After` JSON value — string, number, bool, or
    array — into a `cty.Value` `TokensForValue` can render.
  - `tfwrite/tfwrite.go` (new package): `ApplyModification(src []byte,
    filename string, addr core.Address, mod core.Modification) ([]byte,
    *Report, error)` — parses once, locates addr's resource block
    (`ErrResourceBlockNotFound`/`ErrMultipleResourceBlocks` if it doesn't
    appear exactly once), resolves each `mod.After` dot-path to a byte
    range (or a decline reason), and applies all edits in one pass —
    gathered from a single parse, then applied in descending byte-offset
    order so earlier edits in the file never invalidate the ranges of
    edits still to come. `FindAndApply(dir, addr, mod)` is the
    directory-level wrapper: globs `*.tf` in `dir` (non-recursive,
    matching Terraform's own single-directory-per-module convention), and
    treats a match count of anything other than exactly 1 across the
    whole directory — whether 0, 2 in one file, or 1 each in two files —
    as `ErrResourceBlockNotFound`/`ErrMultipleResourceBlocks` the same way
    a single file would.
  - `tfwrite/literal.go`: `resolveTarget` walks a dot-path segment by
    segment (`strings.Split` on "."), navigating into nested
    `*hclsyntax.ObjectConsExpr` values for each additional segment
    (`findObjectItem` matches an object key by evaluating
    `item.KeyExpr.Value(nil)` — confirmed this correctly treats a bare
    identifier key like `hotfix` as the literal string `"hotfix"`, not a
    variable reference, which is exactly HCL's own object-constructor
    semantics), and validates only the *final* target expression is
    literal — a sibling key in the same object that isn't literal doesn't
    block writing back a different, literal sibling.
  - `cli/writeback.go` (new): `ubx writeback <proposal-id> --tf-dir <dir>
    [--write] [--ledger-dir dir]`. Refuses anything that isn't an
    *accepted* `drift_adopt` proposal (write-back records reality, it
    doesn't apply a still-being-authored change). Iterates every
    `delta.modifies` entry, calls `tfwrite.FindAndApply`, and reports
    per-attribute applied/declined outcomes — a declined attribute is
    reported, never silently dropped, and never fails the command by
    itself; only a whole modification's resource block being
    absent/ambiguous does (collected across all modifications, reported
    per-target, and returned as one error naming every unresolved
    target). Without `--write`, shells out to the system `diff -u` (same
    "use the real tool" comfort already established for `git`/`aws`
    elsewhere in this codebase, rather than vendoring a diff algorithm)
    to print a unified diff and leaves every file on disk untouched;
    `--write` actually writes the modified content — never a git
    commit/push, matching every other "human in the loop" surface in
    this trust chain.
  - Tests (`tfwrite/tfwrite_test.go`): every named adversarial case —
    attribute-is-expression (declines, file byte-identical when it's the
    only attribute), interpolated string (also correctly non-literal),
    resource block absent, multiple matching blocks (within one file, and
    split across two files via `FindAndApply`), nested attribute paths
    (`tags.hotfix`, including a literal key applied despite a non-literal
    sibling in the same map), a key not present in an existing object
    (declined, not inserted), a top-level attribute not present at all
    (declined, not inserted), an atomic list replacement, a mixed
    applied+declined modification (partial application), and a
    weird-but-valid file (tabs, no spaces around `=`, a compact same-line
    object literal) surviving byte-for-byte outside the changed range.
    `cli/writeback_test.go`: dry-run leaves the file untouched and prints
    a real diff, `--write` actually modifies it, a declined attribute is
    reported without failing the command, a missing resource block does
    fail it, and both `Kind != drift_adopt` and `Status != accepted` are
    refused up front.
  - **Verified by hand against the built binary**, not just the test
    suite: accepted a hand-written `drift_adopt` proposal recording a
    top-level scalar change (`instance_type`) and a nested map-key change
    (`tags.hotfix`) against a `.tf` file with comments on both of those
    exact lines, then ran `ubx writeback` twice — once without `--write`
    (confirmed the printed diff was correct and the file on disk was
    untouched afterward) and once with `--write` (confirmed the file
    changed to exactly the expected content, both comments intact):
    ```
    @@ -1,9 +1,9 @@
     resource "aws_instance" "web" {
       # pinned by the platform team, do not change lightly
    -  instance_type = "t3.medium" # last bumped 2026-06
    +  instance_type = "t3.large" # last bumped 2026-06
       ami           = var.ami_id
       tags = {
    -    hotfix = "false"
    +    hotfix = "true"
         owner  = "team-a"
       }
     }
    ```
  - All green: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty),
    `go test ./...` (hermetic — no git/GitHub network access needed for
    this stage at all, everything operates on local files only).
  - go.mod: added `github.com/hashicorp/hcl/v2` (direct dep, for
    `hclsyntax`/`hclwrite`) plus its transitive requirements.
    `go-cty`/`ctyjson` were already present (via `provider/ctyvalue.go`),
    reused rather than duplicated.
- 2026-07-11: UBI-11 Stage 2 follow-up completed — insert new keys into
  existing literal maps.
  - Verified the mechanics with a throwaway repro (deleted before
    committing) across five formatting shapes before writing the real
    implementation: multi-line with no trailing comma, multi-line with a
    trailing comma on every item, a single-line compact object, an empty
    `{}`, and a comment attached to what was the last item before the
    insertion. All five re-parsed cleanly and matched the intended output
    by hand-inspection before being encoded as real tests.
  - `tfwrite/insert.go` (new): `insertableKey` (an error type wrapping the
    parent `*hclsyntax.ObjectConsExpr` and the missing key name — the
    signal `resolveTarget`, literal.go, now returns instead of a generic
    decline when the path's *final* segment is missing from an otherwise-
    navigable literal object), `planInsertion` (computes the exact byte
    position and text to splice in, branching on: object has 0 items →
    first entry using the attribute's own indentation + 2; object has
    items and is multi-line → new line matching the last item's
    indentation, with a trailing comma only if the last item already had
    one; object has items and is single-line → `, key = value` appended
    after the last item's value), `lineIndent`/`firstNonSpaceIsComma`
    (the small byte-scanning helpers both branches share).
  - `tfwrite/literal.go`: `resolveTarget` now distinguishes a missing
    *final* path segment (→ `*insertableKey`, insertable) from a missing
    *intermediate* one (→ still a hard decline — "isn't the final segment
    of %q (write-back only inserts a single new leaf key, not a new
    nested structure)").
  - `tfwrite/tfwrite.go`: `ApplyModification`'s loop now checks
    `errors.As` for `*insertableKey` and calls `planInsertion` instead of
    declining; `edits`' sort switched from `sort.Slice` to
    `sort.SliceStable` (see Open decisions).
  - Tests (`tfwrite/insert_test.go`, new): every named adversarial case —
    multi-line no comma, multi-line preserving trailing-comma style,
    single-line, empty map, a comment on the previously-last item
    surviving, map-is-a-function-call and map-is-a-variable-reference
    (both still decline), a missing intermediate segment (still declines),
    a missing top-level attribute (still declines) — plus two new keys
    inserted into the same map in one call, confirmed both present and
    output byte-identical across two runs (the determinism check the
    `sort.SliceStable` fix exists for). `tfwrite/tfwrite_test.go`'s old
    `TestApplyModification_DeclinesKeyNotPresent` renamed/rewritten to
    `TestApplyModification_InsertsNewKeyIntoExistingLiteralMap`, reflecting
    the behavior actually changing, not just gaining a new case alongside
    the old one.
  - docs/architecture.md's Decision loop section updated in place (not a
    separate docs-first commit — a same-session follow-up to an
    already-shipped stage, not new stage design) to describe insertion as
    in-scope, with the same still-declined boundaries restated precisely.
  - **Verified by hand against the built binary**: a real `.tf` file's
    `tags` map (with an existing key and an inline comment) gained a
    brand-new `hotfix` key via `ubx writeback`, with the existing key and
    its comment surviving untouched:
    ```
    @@ -2,5 +2,6 @@
       instance_type = "t3.medium"
       tags = {
         owner = "team-a" # do not remove
    +    hotfix = "true"
       }
     }
    ```
  - All green: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty),
    `go test ./...` (hermetic).
- 2026-07-11: UBI-11 Stage 3 completed — GitHub App skeleton.
  - `github/issue.go` (new): `CreateIssue` — one `Issues.Create` call,
    needing only `issues: write`.
  - `github/pr.go` (new): `OpenDraftPR` — `Repositories.Get` (find the
    default branch) → `Git.GetRef` (its HEAD SHA) → `Git.CreateRef` (a new
    branch from that SHA) → `Repositories.CreateFile` (commit the draft
    proposal JSON to the new branch — the simpler Contents API, not the
    lower-level Git Data API's blob/tree/commit dance, since committing
    exactly one new file doesn't need it) → `PullRequests.Create`. Needs
    `contents: write` + `pull_requests: write`.
  - `cli/receipt.go` (new): `buildReceipt` — intent summary, blast radius,
    a condensed per-source attribution line (reusing
    `unattributedReason` from `cli/why.go` rather than duplicating the
    reason-to-words mapping), an optional `.tf` write-back preview
    section, and the full proposal JSON in a collapsible details block.
    `trailerHash` is empty for issue mode (nothing to derive acceptance
    from — an issue is never merged) and the real proposal hash for PR
    mode.
  - `cli/surface.go` (new): `surfaceDrift` — parses `--github-repo`,
    computes `driftDiffPreview` (calls `tfwrite.FindAndApply` in pure
    dry-run mode — the exact same function `ubx writeback` calls, just
    never given `--write`, so the preview and a later real write-back
    can't silently diverge into two different code paths), then either
    `github.CreateIssue` or (computing `core.Hash` first, for the
    trailer) `github.OpenDraftPR`. Reuses the `UBX_GITHUB_API_BASE_URL`
    test seam and `GITHUB_TOKEN` convention stage 1 already established.
  - `cli/scan.go`: new `--surface-as issue|pr --github-repo <owner/name>
    [--tf-dir <dir>]` flags; `surfaceDrift` is called only when
    `res.Outcome == core.ScanDrifted`, right alongside (not instead of)
    the existing CloudTrail attribution step.
  - Tests: `github/issue_test.go`/`github/pr_test.go` (httptest-served
    fake API, verifying the exact request bodies — issue title/body, the
    created ref's name/SHA, the committed file's branch/content, the PR's
    head/base/body). `cli/surface_test.go` (through the actual `ubx scan`
    command, fakeprovider fixture on the scan side + the same fake GitHub
    API pattern): issue-mode receipt content, PR-mode trailer/branch/
    committed-proposal content, drift-only triggering (a `ScanNew`
    outcome never calls the GitHub API at all — asserted by a mux handler
    that would otherwise have fired), an invalid `--surface-as` value, a
    missing `--github-repo`, and the write-back preview actually
    appearing (with real diff content) when `--tf-dir` matches.
  - **Verified live, for real, end to end, on `Ubiquex/ubiquex-cli`**
    (asked first, since PR mode is the same category of consequential
    action as stage 1's live PR test — user said to verify both issue and
    PR): opened a real issue (`#2`) with `--surface-as issue`, confirmed
    its title/body matched exactly what `buildReceipt` was designed to
    produce, closed it. Opened a real draft PR (`#3`) with `--surface-as
    pr`, confirmed its head branch (`ubx-drift/payments-fake_widget-
    widget-live-stage3`), base (`main`), body (leading `ubx-proposal:`
    trailer, then the receipt), and committed file
    (`ubx-drift/payments.fake_widget.widget-live-stage3.json`, valid
    `drift_adopt` proposal JSON) all matched exactly what was designed —
    then, to prove stage 3 actually reuses stage 1's binding and not just
    in theory, **merged PR #3 for real and ran the unmodified `ubx accept
    --from-merge` from stage 1 against the real merge SHA**:
    ```
    accepted a81e554ab255b13c83d67fe01a6eb731a6ac5b5064b72e933bdf718ac385b1ac (stack payments) via PR #3, 0 approver(s)
    ```
    0 approvers because nobody reviewed the scratch PR — the same real
    "unreviewed merge recorded, not blocked" outcome stage 1's own live
    verification produced, this time from the App skeleton's own PR
    rather than a manually-authored one; the exact proof point the
    Decision loop design promised ("reuses stage 1's binding directly").
    Cleaned up immediately after: reverted the merge commit on `main`
    (`git revert -m 1`), deleted the scratch branch locally and on the
    remote, closed the scratch issue. Confirmed afterward (`gh pr list`,
    `gh issue list`, `gh api .../branches`) that only `main` remained and
    both scratch artifacts were in their expected final states (merged +
    reverted; closed).
  - All green: `go build ./...`, `go vet ./...`, `gofmt -l .` (empty),
    `go test ./...` (hermetic by default — no GitHub API access unless
    `UBX_GITHUB_API_BASE_URL`/`GITHUB_TOKEN` are explicitly set for a
    real run).

## Next steps

**UBI-21 is done, both stages.** Stage 2 ran this session after Roozbeh
set up Application Default Credentials against his own `personal-273114`
GCP project mid-session (his own call, at the checkpoint this session
raised rather than proceeding unilaterally with new credentials in a
real billed project).

Real gaps worth remembering, not quietly forgetting:

- Only 5 of the ~40 seeded GCP types are `RealSafe`/live-verified; the
  other ~35 are still `FakeOnly`/`Implemented: false`, exactly where
  UBI-9's own AWS batches started before working through the list over
  several sessions — expect the same here, not a one-session sweep.
- None of the 5 live-verified GCP types were given a `LookupHint` /
  promoted into `core/lookuphints` — that mechanism's shipped message
  hardcodes "make sure `id` is included," which is wrong for
  `google_storage_bucket` (needs `name` too, not instead) and can't fire
  at all for `google_pubsub_topic`/`google_secret_manager_secret` (their
  mistake produces no error to attach a hint to). Generalizing
  `core/lookuphints`/`lookupHintText` to express "both required
  together" and "silently incomplete, not just unreadable" is real,
  unstarted follow-up work.
- `gcpaudit/`'s correlation only actually works today for GCP services
  whose audit log entries name the resource using the project ID
  (confirmed for Pub/Sub) — Secret Manager's entries use the numeric
  project number instead, which never appears in the resource's own
  observed state, so `google_secret_manager_secret` drift is silently
  unattributable via this backend (always `audit_unattributed`/
  `no_matching_event`, indistinguishable from a genuine no-event case).
  Needs a per-service resourceName-shape table or a project-number
  lookup added as a correlation candidate — not built here.
- `gcpaudit.Backend.DeliveryLag` (3 minutes) is a safety margin above a
  single ~18-second measurement, not a documented GCP ceiling the way
  CloudTrail's ~15 minutes is (GCP publishes no equivalent number) —
  worth widening the sample size if this ever proves too tight.

**Production ladder step 5 (UBI-20, hardening pass) is done.** Real gaps
worth remembering, not quietly forgetting: `--json` covers `scan`
(single-resource only, not `--all`), `status`, and `why` — not
`writeback`/`revert-plan`, which the user's own workstream 2 scope never
named; `scan --all --json` was deliberately rejected rather than
implemented this session (documented scope limit, revisit if a real CI
pipeline needs it); the lock only guards `Accept`/`AcceptFromMerge` —
nothing else writes to a ledger directory today, but if that ever
changes, the new writer needs to acquire it too, not assumed automatic;
"timeout/retry behavior under real network conditions" (named in Linear's
fuller UBI-20 description, not in the user's own 4-workstream scope for
this session) is still unreviewed. Production ladder step 6, if there is
one, isn't scoped yet.

**Production ladder step 4 (UBI-19, `.ubx/config`) is done.** Real gaps
worth remembering: `--ledger-dir` is deliberately not a config key (the
issue never named it, and it's more consequential to get silently wrong
than the other five); `accept`'s reverify flow only reads config when
already opted into via `--reverify-source` on the CLI, never turned on by
config alone; `status` deliberately never reads config's `stack` default.
None of these are gaps to close later — they're the actual scope
boundary, documented as such. Production ladder step 5, if there is one,
isn't scoped yet.

**Production ladder step 3 (UBI-18, bulk onboarding) is done.** Real gaps
worth remembering, not quietly forgetting: bulk *acceptance* is
deliberately out of scope (see docs/architecture.md's Business-frame
reasoning — a decision to make separately and later, if ever, not a
default this issue backed into); cloud-side discovery (tag-based
enumeration, per-type list APIs, for teams with no Terraform state at
all) is explicitly a different epic, not started; `--all` walks are
bounded-memory (one `json.Unmarshal` of the whole state file), same
accepted scale posture as `FoldState`'s own linear ledger walk and `ubx
status`'s own fleet walk — revisit together if a real state file or
ledger ever approaches a size where that stops being reasonable, not
separately.

**M1-2 ("detection core") is fully done too, `ubx status` (UBI-17) having
landed its last unstarted piece.** docs/plan.md's M1-2 bullet is annotated
"Milestone complete" alongside M3-4's. Both of the wedge's first two
milestones are done. Next wedge-buildout milestone per docs/plan.md is M5-6
(retention layer: `why` over drift history, Slack notifications, policy
stubs), not started. `status --drift`'s own real gaps worth remembering
rather than quietly forgetting: no CloudTrail attribution wired in (UBI-10's
`core.AttributeDrift` is drift_adopt-generation-specific today; `ubx status`
is a pure read-only report and was scoped that way deliberately this
session, not by oversight — see docs/architecture.md); `Configure` is still
called once per resource inside each `RunScan` call even though one
provider process serves the whole fleet (accepted, bounded inefficiency at
foundational-slice scale, same posture as `FoldState`'s own linear-walk
note); no pagination/streaming for a very large ledger (one `Chain()` walk
holds the whole chain in memory, same accepted scale posture).

**M3-4 ("decision loop") is fully done, all three UBI-11 stages plus
UBI-16's revert path.** Nothing further queued under it as a milestone —
docs/plan.md's M3-4 bullet is annotated "Milestone complete."

**Immediate, manual, not a coding session:** push the `v0.1.0` tag (`git tag
v0.1.0 && git push origin v0.1.0`) once the UBI-12 goreleaser dry-run output
above has been reviewed — that's what actually triggers
`.github/workflows/release.yml` and publishes the release. Nothing in this
repo is waiting on that happening first; it's recorded here so it isn't
forgotten, not because anything is blocked on it.

1. **UBI-11 (real) is fully done: all three stages of "M3–4 decision
   loop" are shipped and verified live.** Nothing further queued under it
   as a milestone, but real gaps remain, worth remembering rather than
   quietly forgetting now that the ticket reads "done":
   - **Stage 2:** write-back still never inserts a *new top-level
     attribute*, or a nested structure more than one key deep (only a
     single new leaf key in an *existing* literal object — see Open
     decisions); no `--commit` option to have `ubx writeback` create a
     git commit directly (only `--write`, a plain file write) — the
     docs' own "(or a commit on a branch)" phrasing anticipated this as
     an option, not a requirement.
   - **Stage 3:** explicitly out of scope per the original task framing,
     unchanged: webhook-driven (vs. scheduled/manual) triggering,
     installation-flow hardening (real GitHub App manifest/OAuth/
     installation tokens — this session's live verification used a
     personal access token via `GITHUB_TOKEN`, same convention as stage
     1), multi-repo fan-out. `ubx scan --surface-as` is a CLI command
     today, invokable by a scheduled job (e.g. GitHub Actions) or by
     hand — not a hosted, always-on service; that's Nexus/SaaS territory
     per docs/architecture.md's component map (#10), deliberately later.
     Also: `--surface-as` only fires for a single resource per `ubx scan`
     invocation, same as everything else `ubx scan` does — multi-resource
     drift surfacing depends on `status --drift` (still not started, see
     below) existing first.
2. UBI-9/UBI-10 are both closed — nothing further queued under either. If
   a type's fixture-verified shape ever turns out wrong once real usage
   exercises it, or if CloudTrail's `ResourceName` matching behaves
   differently for a type not yet checked live, fix it as a normal bug
   (`conformance/registry.go`'s `Notes` / `core/attribution.go`
   respectively), not a reason to reopen a milestone.
3. Still queued from before UBI-9, now further behind: Core IR + resolver
   (component map #1-2). `status --drift` (a read-only report over what
   `ubx scan` would find across multiple resources) is also still M1-2
   scope, not started — would naturally reuse `core.AttributeDrift` per
   resource the same way `ubx scan` does now, and the (mislabeled-"UBI-11")
   `ubx why <address>` chain view for showing that report's history per
   resource.
4. UBI-10 gaps, not addressed this session, deliberately deferred: no
   caching/dedup of `EventLookup` calls across multiple scans in a batch
   (each `ubx scan` invocation currently builds its own `cloudtrail.Client`
   and searches independently — fine at "one resource per CLI invocation"
   scale, worth revisiting once `status --drift` scans many resources per
   run); `session_context` is still passed through opaquely (UBI-11 didn't
   change this — `ubx why` prints the actor ARN/event name/time/source IP
   now, but not session_context specifically, which stays available in
   the raw ledger JSON only); only tested live against `aws_s3_bucket`
   (one type) — the `id`-not-`arn` finding is recorded as an empirical
   fact about that type (and `aws_iam_role`/`aws_vpc`, tested via the
   manual CloudTrail probe but not through a full live `ubx scan` run),
   not assumed to hold for every AWS service.
5. A `ubx provider ...` dev-facing CLI verb was deliberately never added
   across seven sessions — still not part of the eventual product CLI
   surface (see docs/architecture.md component map). `ubx scan` now covers
   the "read one resource" use case anyway; revisit only if something else
   still needs raw schema/read access outside of scan/accept.
6. Not addressed, deliberately out of scope: PlanResourceChange/
   ApplyResourceChange (write path — deferred per docs/architecture.md
   "wedge reads and records before it ever writes"), AutoMTLS in provider/
   (still opt-in/unimplemented), cryptographic signing tier for acceptance
   (docs/architecture.md calls this out as "optional... later"; `ubx
   accept` now has two tiers, `local` and `pr_merge` (UBI-11 stage 1) —
   `crypto` is still unimplemented). Note that `FoldState`'s O(chain)
   walk (see Open decisions) is an *accepted* limit, not deferred work —
   its own revisit trigger is stated there; don't re-open it as a TODO
   without something actually hitting that trigger.
7. UBI-8 gaps, not addressed this session either: no `UBX_PROVIDER_MIRROR`
   signature verification (by design — see docs/architecture.md, a local
   file is trusted differently); no cache invalidation/eviction; `ubx scan
   --source` doesn't route to a non-default registry hostname even though
   `ParseSource` would parse one. See prior entries for full detail.

## Docs debt

**UBI-21's ubiquex-docs work was done in this same session, per protocol,
both stages**: `getting-started/installation.mdx` mentions `--source
hashicorp/google` alongside AWS; `cli/lookup.mdx` gained a real GCP
table (five live-verified types, their actual confirmed `--lookup`
shapes, a `Warning` for the two silently-incomplete-read types);
`concepts/attribution.mdx`'s "Beyond AWS" section carries a real
transcript (real caller email, real event) rather than a future plan,
plus a `Warning` about the Secret Manager correlation gap. `mint
validate`/`mint broken-links` both pass clean. See ubiquex-docs' own
STATE.md for the full writeup. No debt carried.

**UBI-20's ubiquex-docs work was done in this same session, per
protocol, across all four workstreams**: new `cli/exit-codes.mdx` (the
full per-verb 0/1/2 table, cross-linked from every affected page,
including a fix to `cli/writeback.mdx`/`cli/revert-plan.mdx` wording that
had assumed a declined attribute never affects the exit code — it does
now); `--json` schema sections with real transcripts on `cli/scan.mdx`,
`cli/status.mdx`, `cli/why.mdx`; a new "Concurrent access" section on
`concepts/ledger.mdx` for the ledger lock, cross-linked from
`cli/accept.mdx`; and a cross-link from `cli/lookup.mdx` to the new
teaching-error runtime text. `mint validate`/`mint broken-links` both
pass clean. See ubiquex-docs' own STATE.md for the full writeup.

**UBI-19's ubiquex-docs work was done in this same session, per
protocol**: new `cli/config.mdx` and `cli/init.mdx`, short daily-command-
form examples added to `cli/scan.mdx` and `cli/status.mdx` (the latter
also documenting the deliberate stack-default exception), and a brief
config-fallback note + cross-link added to `cli/writeback.mdx`,
`cli/revert-plan.mdx`, `cli/accept.mdx`, and `cli/why.mdx` each for their
own relevant key. `getting-started/installation.mdx` now points to `ubx
init` as the natural first step post-install. `mint validate`/`mint dev`/
`mint broken-links` all pass clean. See ubiquex-docs' own STATE.md for
the full writeup.

**UBI-18's ubiquex-docs work was done in this same session, per protocol,
and it also cleared UBI-16's carried debt (below) while in the docs
repo**: new `cli/revert-plan.mdx` and a `concepts/drift.mdx` "Two
resolutions to a drift" section closed the UBI-16 gap. Discovered along
the way: `cli/scan.mdx` had never gotten `--propose` documented at all
(it shipped entirely under the old protocol, and the UBI-16 debt entry
below only ever named the missing `revert-plan` page, not the missing
flag on an already-published page) — added alongside this session's own
`--all`/`--tfstate`/`--out-dir` flags and a full "Bulk onboarding"
section, plus new `guides/onboarding.mdx` with a real, complete
walkthrough transcript. `mint validate`/`mint dev`/`mint broken-links`
all pass clean. See ubiquex-docs' own STATE.md for the full writeup, and
its own new lesson recorded there: a docs-debt entry should name every
already-published page a change touches, not just the new ones needed.

**Mid-session protocol change (2026-07-16): CLAUDE.md's session protocol
now requires ubiquex-docs updates in the SAME session, not batched as
debt** — discovered as an uncommitted working-tree edit partway through
this session (not made by this session's work), confirmed with Roozbeh
before acting on it, then committed. This reverses the "During
foundational slices: do NOT write user docs inline" rule that governed
every prior session referenced below (UBI-13's whole existence was to pay
down exactly that kind of backlog) — a docs-debt entry is now the
documented exception, not the default.

**UBI-17's ubiquex-docs work was done in this same session, under the new
rule**: `cli/status.mdx` (both modes documented distinctly, every example
a real transcript, the exit-code CI contract as its own table with a
worked `bash`/`case` example), plus the "one ledger directory can hold
multiple stacks" clarification added to `concepts/ledger.mdx`'s "Stacks
are independent" section. `mint validate`/`mint dev`/`mint broken-links`
all pass clean. See ubiquex-docs' own STATE.md for the full writeup.

~~**UBI-16's docs debt (previous session, predates the protocol change)
remains genuinely open** — not touched this session, since it wasn't this
session's scope: `ubx revert-plan` still has no CLI reference page, and no
concepts-level page explains `drift_revert`'s corrective-direction
semantics. Tracked in ubiquex-docs' own STATE.md now too, carried forward
until picked up.~~ **Cleared in the UBI-18 session above.**

**UBI-16 (prior session) opened new debt in ubiquex-docs, deliberately not
written inline** — the revert path is foundational-slice work (a whole new
wedge verb, docs/plan.md M3-4), not a docs session, per CLAUDE.md's session
protocol. Batch for the next ubiquex-docs session:

- `ubx scan`'s new `--propose revert|adopt|both` flag: needs both flag
  reference and a concepts-level explanation of what a `drift_revert`
  proposal actually means (the corrective direction, real blast radius —
  this is a bigger conceptual shift than a typical new flag, arguably
  deserves its own `concepts/revert.mdx` alongside the existing
  `concepts/drift.mdx`, not just a flag-table entry).
- New `ubx revert-plan` command: full CLI reference page needed
  (`cli/revert-plan.mdx`, following the same skeleton-then-full-reference
  pattern UBI-13 established for every other verb). The "emits, never
  applies" distinction from `ubx writeback` is exactly the kind of thing
  worth a real worked example, not just a one-line flag description —
  probably also worth a forward link from `cli/writeback.mdx` and
  `concepts/drift.mdx`.
- `ubx why`'s chain rendering now shows three kinds instead of two
  (adoption/drift_adopt/drift_revert) — no rendering code changed, but the
  existing why-focused docs pages should probably show a mixed-kind chain
  example now that one is possible, not just adoption+drift_adopt.

**UBI-13 closed out 2026-07-11, three sessions, no open debt remaining.**
Per CLAUDE.md's session protocol: user-visible CLI changes create a docs
obligation in the ubiquex-docs (Mintlify) repo, batched and cleared per
slice rather than written inline during foundational work. UBI-13 was that
batch, covering everything accumulated from UBI-8/UBI-10/UBI-11.

- **Session 1** — scaffold + conceptual layer: `docs.json` navigation,
  landing page, install placeholder, five concept pages, skeleton `cli/`
  pages. Cleared the conceptual half of `ubx why`'s resource-address lookup
  and `cloudtrail`/`cloudtrail_unattributed` rendering debt.
- **Session 2** — full per-verb reference: every flag from the built
  binary's `--help`, every example a real captured transcript. Cleared
  UBI-11 stages 1–3's flag-level debt (`propose`, `accept --from-merge`,
  `why --verify-acceptance`, `writeback --tf-dir`/`--write`,
  `scan --surface-as`) and wrote the conformance-registry lookup table
  (`cli/lookup.mdx`).
- **Session 3** — closed the last two items: `guides/pr-merge-acceptance.mdx`
  (a full draft→PR→merge→accept walkthrough, real transcripts throughout,
  including a real zero-approvers-accepted case) and a real
  `--source`/`--provider-version` worked example (via `UBX_PROVIDER_MIRROR`,
  documented honestly as the mirror-resolution path rather than a live
  registry round trip) in `cli/scan.mdx`, plus the matching
  `--reverify-source`/`--reverify-provider-version` example in
  `cli/accept.mdx`. This example surfaced `resolution.inputs[].provider_checksum`
  — a real field, present only for source+version-acquired scans, that had
  no prior debt entry at all (see Surprises below).

Nothing from UBI-8/UBI-10/UBI-11 remains undocumented. The next docs
obligation starts fresh from whatever slice lands next.

## Surprises / findings

- 2026-07-16 (UBI-21): **`hashicorp/google` speaks tfplugin v5, same as
  `hashicorp/aws`.** Not a surprise exactly (dual v5/v6 support exists
  precisely because this kind of thing was already found once, in Slice
  1), but worth recording as confirmation rather than assumption: a
  second real provider, from a different vendor's SDK generation and
  release cadence than AWS's, still negotiates the same wire version.
  `conformance/gcp_provider_test.go` asserts this directly (fails loudly
  if a future `hashicorp/google` release ever changes it) rather than
  just noting it in prose.
- 2026-07-16 (UBI-21): **The GCP project available for Stage 2
  (`personal-273114`) has billing enabled and most needed APIs on, but
  no Application Default Credentials configured at all** — a real live
  scan attempt (`ubx scan --source hashicorp/google ...`) got exactly as
  far as `Configure`, then failed on `google: could not find default
  credentials`. This is exactly the boundary Stage 1 vs. Stage 2 was
  designed around: everything up to and including a live provider
  handshake needs no GCP account at all; anything that actually reads a
  GCP resource does. Setting up ADC (either `gcloud auth
  application-default login` or minting a service account key) wasn't
  done unilaterally this session — see "Next steps."
- 2026-07-16 (UBI-21 Stage 2): **`google_storage_bucket`'s lookup
  requirement is the opposite shape from `aws_s3_bucket`'s, discovered
  only by testing both directions live.** `{"id": "<name>"}` alone
  errors outright (`Storage Bucket "": googleapi: Error 400: Required
  parameter: project`, even with `project` supplied in provider config);
  `{"name": "<name>"}` alone reads back `null`. Only `{"id": ...,
  "name": ...}` together works — genuinely both-required, unlike
  `aws_s3_bucket` where `id` alone already succeeds. This directly
  broke an assumption baked into UBI-20's own teaching-error mechanism
  (`core/lookuphints`'s shipped hint text hardcodes "make sure `id` is
  included"), which would have been actively wrong advice here — the
  type was deliberately left out of `core/lookuphints` rather than
  forcing it in. Lesson: "the same amendment's shape applies to a new
  platform" is worth checking in both directions, not just porting the
  AWS finding's polarity.
- 2026-07-16 (UBI-21 Stage 2): **Two GCP types (`google_pubsub_topic`,
  `google_secret_manager_secret`) have a lookup-mistake failure mode
  AWS never showed at all: `ReadResource` succeeds with `id` alone, but
  silently returns incomplete data** (`name: ""` for the topic,
  `name: "", secret_id: null` for the secret) rather than erroring or
  returning null. `core.ErrResourceUnreadable` never fires — there is
  no error for the UBI-20 teaching-error mechanism to attach a hint to,
  and no signal at all that anything went wrong short of noticing the
  proposal's own recorded state looks wrong. This is a structurally
  different, more dangerous class of mistake than anything the existing
  teaching-error design was built to catch (which only ever engages on
  an actual read failure) — flagged as real follow-up work, not
  patched under time pressure here.
- 2026-07-16 (UBI-21 Stage 2): **GCP IAM's own read-after-write
  consistency lags the write itself, confirmed live while writing
  `conformance/gcp_live_test.go`'s `google_service_account` case.**
  `gcloud iam service-accounts update ... --display-name=X` followed
  immediately by `gcloud ... describe` returned the OLD display name —
  `update`'s own response already echoed the new value, but a
  subsequent read (even via the same `gcloud` CLI) hadn't caught up
  yet. The Terraform provider's own `ReadResource` call lagged further
  still, on a different occasion, than `gcloud describe` did — two
  different read paths becoming consistent at different times, not one
  global moment. Fixed with an explicit poll-until-consistent wait (up
  to 90s) rather than a fixed sleep, since a fixed guess would only ever
  be "usually enough."
- 2026-07-16 (UBI-21 Stage 2): **`gcpaudit/`'s correlation logic
  (unchanged `core.AttributeDrift`/`identityCandidates`, carried over
  from UBI-10's AWS-only design) doesn't work for every GCP service,
  discovered only by actually trying to attribute a Secret Manager
  drift and getting `audit_unattributed`/`no_matching_event` for an
  event that demonstrably existed.** `gcloud logging read` showed why:
  Pub/Sub's Admin Activity entries name the resource as
  `projects/<PROJECT_ID>/topics/<name>` (matching the resource's own
  observed `id` exactly), but Secret Manager's entries name it as
  `projects/<PROJECT_NUMBER>/secrets/<name>` — the *numeric* project
  number, which appears nowhere in `google_secret_manager_secret`'s own
  observed state. `identityCandidates`'s id/arn/name derivation can
  never produce a matching candidate for this service. This is close to
  (but distinct from) the "stop and flag if `EventLookup` doesn't hold"
  instruction this session was given: the *interface* held up
  perfectly; what broke is the *caller-side candidate derivation*,
  which quietly assumed every service's own resourceName is reachable
  from the resource's own observed attributes. Documented in
  `gcpaudit/client.go`, docs/schema.md, and here rather than silently
  declaring GCP attribution "done" — real follow-up work (a per-service
  resourceName-shape table, or fetching the project number as an extra
  candidate) is needed before Secret Manager drift can be attributed.
- 2026-07-16 (UBI-21 Stage 2): **A real UBI-20 regression, sitting
  undetected since that session shipped, caught by this session's own
  live test runs**: `cli/attribution_live_test.go`'s
  `TestScan_AttributesRealDrift_LiveCloudTrail` still asserted
  `err != nil` as a failure after a successful adopt/drift scan — but
  UBI-20 changed exactly that outcome to return
  `ExitCodeError{Code: 1}` (an actionable finding, not an error). Nobody
  had run this specific test with `UBX_CONFORMANCE_LIVE=1` since UBI-20
  landed (it's not part of the default `go test ./...` run, by design).
  Fixed here, alongside the `gcpaudit/` work that happened to touch the
  same file. Lesson: a hermetic regression suite passing clean doesn't
  mean every gated live test was actually re-run after a
  behavior-changing session — worth a periodic full live sweep across
  every package, not just the ones a given session's own diff touches.
- 2026-07-16 (UBI-20): **The teaching-error hint (workstream 3) was
  backwards in its first draft, caught only by an actual live scan
  against the real "ubx-states" S3 bucket, not by reading
  `conformance/registry.go`'s existing Notes prose.** The Notes text for
  `aws_s3_bucket`/`aws_iam_role`/`aws_iam_user` reads "id and
  bucket/name are both the natural key; lookup needs BOTH set" — true,
  but read without actually calling `ReadResource`, it doesn't say *which
  one* is safe to treat as "the missing field" when only one is given. A
  live `core.RunScan` against the real bucket with `{"bucket": "..."}`
  alone read back `null`; the exact same call with `{"id": "..."}` alone
  succeeded. The first version of `core/lookuphints` had this backwards
  — it told a user hitting the error to add `"bucket"`/`"name"`, when the
  real fix is `"id"` (which they may not have included at all if they
  reached for the type's own Terraform attribute name instead, exactly
  the mistake this teaching error exists to catch). Fixed before
  committing, with `conformance/lookuphints_live_test.go` pinning both
  directions (the failing case, and — new — a passing `{"id": ...}`-alone
  case, so this can't silently flip back). Lesson for next time: a Notes
  field written as prose can be *correct* and still not answer the
  specific structured question a generator needs — verify the generated
  data's direction against a real call, don't just trust the prose it was
  paraphrased from.
- 2026-07-16 (UBI-20): **A real `flock(2)` would have made "stale lock
  from a killed process" — one of the user's own named adversarial cases
  — impossible to observe.** The kernel releases a process's `flock`s the
  instant it exits for any reason, including `SIGKILL`; a contender's very
  next retry would just silently succeed, with nothing left to detect or
  report. Realized during design, before writing any lock code, from
  first principles about how `flock` actually behaves — worth recording
  since "flock-style lockfile" (the user's own phrasing) could easily have
  been implemented as a literal `flock(2)` call and only failed to satisfy
  the stated adversarial requirement once someone tried to write the test
  for it. Built as a PID file instead: write the holder's PID into the
  lock file, check that PID's liveness (`Signal(syscall.Signal(0))`) on
  contention, so "confirmed dead, here's how to recover" stays a real,
  distinct, testable outcome.
- 2026-07-16 (UBI-19): **`ubx init`'s own generated `.ubx/config` silently
  discarded `stack`/`github_repo`/`tf_dir` — every one of them read back
  empty — because `renderConfigTemplate` wrote them *after* the
  `[provider]`/`[provider_config]` table headers.** This is completely
  valid TOML syntax; it's just not what it looks like to a human reading
  the template string. TOML assigns a bare key to whichever table was
  most recently opened, and blank lines between sections don't close a
  table the way they might in, say, an INI-adjacent mental model — only
  the next `[table]` header or end-of-file does. Found only by writing a
  test that actually round-trips `ubx init`'s output back through
  `toml.DecodeFile` and checking the parsed struct's fields, not by
  reading the generated string and confirming it "looked right" (which it
  did, by eye). Fixed by moving every root-level key before any table
  header; kept as a permanent regression test
  (`TestLoadConfig_RootKeysAfterTableGetSwallowed`) that documents the
  underlying TOML behavior itself, not just this one template's fix — the
  same mistake is trivially easy to reintroduce by hand-editing a real
  config, not just by generating one.
- 2026-07-16 (UBI-18): **Every proposal generated in one `ubx scan --all`
  batch shared the exact same `parent` — the ledger's real, on-disk head,
  which never moves mid-walk since nothing gets accepted until later —
  so only the first of N proposals anyone tried to accept would ever
  succeed.** Found only by live-verifying the full flow (an SQS queue's
  proposal accepted fine; the SNS topic's, generated in the same batch,
  failed as parent-mismatched), not by reasoning through the orchestration
  beforehand — the bug is invisible in any test that only accepts *one*
  generated proposal, which is exactly what every test written before the
  live one happened to do. Fixed by tracking, within the `--all`
  orchestration itself, what the head *will be* after accepting every
  proposal generated so far in the batch, since a proposal's hash is a
  pure function of its content and is therefore computable the moment
  it's generated — added a regression test
  (`TestScanAll_AllGeneratedProposalsAcceptInSequence`) that accepts all
  three proposals from a batch, specifically so this can't regress
  silently behind single-proposal-only test coverage again.
- 2026-07-16 (UBI-18): **`conformance/registry.go`'s `IdentityFields`
  looked like the obvious thing to reuse for building a lookup from state
  attributes, and would have been wrong for at least one type.**
  `aws_sqs_queue`'s `IdentityFields` names `id`, `arn`, and `url` — but
  the actual required lookup is `{"id": "<queue-url>"}` alone, no
  separate `url` key at all, since `id` already equals it. `IdentityFields`
  answers "which attributes carry stable identity for CloudTrail
  attribution" (UBI-8/UBI-10), a related but genuinely different question
  from "what does `ReadResource` need" — checked this by re-deriving the
  correct lookup shape per type from `cli/lookup.mdx`'s already-verified
  table instead of assuming `IdentityFields` would mechanically transfer,
  and built a small, separate, explicitly-scoped table instead of
  importing `conformance` (project-internal test tooling, by its own doc
  comment) into shipped product code at all.
- 2026-07-16 (mid-session, not tied to any single ticket): **CLAUDE.md had
  an uncommitted working-tree edit that neither this session's work nor
  any prior commit made** — the session protocol's rule 5 (docs debt is
  batched, not written inline) had been rewritten to require same-session
  ubiquex-docs updates instead, plus a new rule 6 about Linear issue
  discipline. `git log -1 -- CLAUDE.md` showed the last real commit
  touching it was 2026-07-10; the file's mtime was from earlier today.
  Almost certainly Roozbeh editing it directly while this session was
  already in progress. Stopped and asked before acting on it rather than
  either silently following the new rule or silently ignoring an edit
  sitting in the working tree — confirmed intentional, then committed it
  and applied it to the rest of this session (UBI-17's ubiquex-docs work
  landed same-session as a direct result; see Docs debt below for what
  that changed). Worth remembering generally: a project's own governing
  file can change mid-session, and the working tree is the place that
  would show it before any commit does.
- 2026-07-16 (UBI-17): **`core.Ledger`'s own doc comment ("a per-stack
  append-only proposal chain") and docs/schema.md's layout diagram both
  describe one directory per stack, but `Head()`/`Append()` never actually
  partition storage by `Proposal.Stack` — one directory is one flat hash
  chain regardless of how many different stacks' proposals live in it.**
  Every prior session (UBI-7 through UBI-16) happened to use exactly one
  `--stack` value per ledger directory in every test and every live
  verification, so this was simply never exercised, not verified safe.
  `ubx status`'s "all stacks by default" framing needed it to actually
  work, so this session tested it directly rather than assuming the doc
  comment was either accurate or load-bearing: it works, because
  `GenerateProposal`/`GenerateRevertProposal` always read the *current*
  head fresh via `Head()` before building a proposal, regardless of which
  stack it's for, so proposals for different stacks correctly chain in
  temporal order within one shared directory. Whether that's actually the
  *intended* real-world deployment shape (vs. one directory per stack,
  matching the schema.md diagram) is still an open question — this
  session only confirmed both shapes work, not which one teams should
  use.
- 2026-07-16 (UBI-17): **Returning `&ExitCodeError{Code: 1, Err: nil}` from
  a command's `RunE` still triggered cobra's default error-handling path
  — a blank `Error: ` line followed by the entire flag-usage block —
  because cobra only checks whether the returned error is `nil`, not
  whether it carries a message.** For `ubx status`, "drift found" is a
  normal, working-as-designed report outcome, not a command misuse the
  usage block would help with; a blank error line made it look like
  something had actually gone wrong. Caught only by running the real
  built binary end-to-end and reading its actual stderr output, not by
  checking `err != nil` in a Go test (which is exactly why this went
  unnoticed through several rounds of unit/CLI tests first — they all
  correctly asserted the exit code, none of them looked at what a human
  running this at a terminal would actually see). Fixed two ways
  together: `ExitCodeError` always carries a real one-line message now
  (never nil for a reportable outcome), and `status`'s own
  `SilenceUsage`/`SilenceErrors` are set (not project-wide) — without
  `SilenceErrors` specifically, the message printed twice, once from
  cobra's own default handling and again from `cmd/ubx/main.go`'s
  `ExitCodeError`-aware print, since both paths run unless silenced.
- 2026-07-16 (UBI-16): **`RunScan`'s drift baseline (`Ledger.LastObservedHash`)
  and the ledger's actual reconstructed truth (`FoldState`) had always
  computed the same hash for every proposal kind that existed before
  `drift_revert` — not because anything tied them together on purpose, but
  because accepting `adoption`/`drift_adopt` IS the decision that the
  observed value becomes the ledger's truth, so "last thing we recorded
  observing" and "what the ledger's fold reconstructs" were the same value
  by construction.** `drift_revert` breaks that coincidence on purpose: its
  `resolution.inputs` entry (correctly, per the schema amendment) records
  the *observed/drifted* hash for staleness-checking purposes, while its
  `delta.modifies[].after` — what `FoldState` folds forward — is the
  *restored* value. Accepting a `drift_revert` doesn't itself touch cloud,
  so immediately afterward the two genuinely disagree about "what's true"
  (FoldState: restored) versus "what we last saw" (LastObservedHash: still
  drifted). Found this by writing the live-verify sequence as a test
  first — `TestRunScan_AfterRevertAccepted_ManualCorrection_ScanClean` —
  and watching the *second* assertion (scan after manual correction should
  show clean) fail with `ScanDrifted` instead, not by reasoning it through
  in the abstract and trusting the reasoning. Fixed by switching `RunScan`
  to compare against `ObservedHash(FoldState(addr))` instead of
  `LastObservedHash(addr)` directly — verified as a true no-op for every
  pre-existing case by running the full test suite unchanged (it passed),
  and it's the semantically correct baseline regardless: docs/architecture.md
  already defines drift as "reality diverging from the ledger," which is
  exactly what `FoldState` answers.
- 2026-07-11 (UBI-13 session 2): **`ubx accept`'s own `--help` output named
  five flags (`--reverify-with`, `--reverify-source`,
  `--reverify-provider-version`, `--resource-type`, `--resource-name`) that
  no prior "docs debt" entry in this file ever mentioned.** Discovered only
  because ubiquex-docs' session protocol requires verifying every flag
  against the actual built binary rather than against this file's own
  running list — the debt list itself isn't a reliable inventory of the
  shipped flag surface, it's only a log of what was *noticed* as
  user-visible at the time a slice landed. Worth remembering next time a
  "what needs documenting" pass starts here: cross-check against
  `--help`/`cmd.Flags()`, don't just work the debt list.
- 2026-07-11 (UBI-13 session 3): **`ScanRequest.ProviderChecksum` round-trips
  into the generated proposal as `resolution.inputs[].provider_checksum`
  (`core/proposal.go`), but only when the provider was acquired via
  `--source`/`--provider-version` — a `--provider`-direct scan has no
  checksum to attribute, so the field is simply absent (`omitempty`).**
  Found only by actually running `--source`/`--provider-version` end to end
  for the docs' worked example — every prior scan/accept example (including
  all of UBI-13 sessions 1–2) used `--provider` with a local binary, so this
  field never showed up in any transcript captured before now. Same lesson
  as the reverify-flags finding above: exercising a flag combination for
  real surfaces things a code-reading pass over the CLI flag list alone
  would miss.
- 2026-07-11: **Inserting two new keys into the same map in one call
  produces two identical byte offsets — not a rare edge case, but the
  direct, guaranteed consequence of how insertion is computed (both
  "after the current last item," from the same unmodified parse).**
  Noticed this while writing the determinism test for simultaneous
  insertions (UBI-11 stage 2 follow-up), not before — the existing
  descending-byte-order sort (built for the *replace* case, where
  distinct paths almost always land at distinct offsets) had never needed
  to think about ties. `sort.Slice`'s docs are explicit that equal
  elements' relative order is unspecified, which is exactly the kind of
  guarantee this project doesn't accept implicitly anywhere else. Fixed
  by switching to `sort.SliceStable`; the actual output order for tied
  insertions turned out to be the *reverse* of processing order (the
  first-processed edit's text ends up appended *after* the second's, since
  each new splice at an already-spliced-into position lands before what's
  already there) — deterministic and correct, just not the naive
  "alphabetical" ordering intuition would suggest, so this is recorded
  rather than left to be independently rediscovered.
- 2026-07-11: **GitHub's Contents API (`Repositories.CreateFile`) commits
  one new file in a single call — no need to touch the lower-level Git
  Data API's blob/tree/commit dance at all for UBI-11 stage 3's PR mode.**
  Worth naming explicitly since the more "correct-looking" way to commit
  programmatically (used by most from-scratch git tooling, and the
  approach a first guess reached for) is the Git Data API: create a blob,
  create a tree referencing it, create a commit referencing the tree,
  update a ref to point at the commit. For committing exactly one new
  file to a fresh branch — everything stage 3 actually needs — the
  Contents API's `CreateFile` collapses all of that into one request.
  Confirmed via the real API during live verification (`Ubiquex/ubiquex-cli#3`
  really did end up with the one intended file, correctly committed),
  not just the hermetic fake-server tests.
- 2026-07-11: **`hclwrite`'s own `Body.SetAttributeValue` is the wrong
  tool for editing one key inside an existing map/list attribute — it
  silently reformats the whole thing and can lose comments.** Tested this
  directly (a throwaway repro, deleted before committing, same pattern as
  UBI-9's `cmd/schemadump`) against a `tags = { hotfix = "true" # note
  \n owner = "team-a" }`-shaped attribute: calling
  `SetAttributeValue("tags", newCtyObjectValue)` to change just `hotfix`
  regenerates the *entire* attribute's tokens from the `cty.Value`,
  which has no notion of the original inline comment at all — it's gone
  in the output. This is exactly the failure mode the "surgical,
  preserves formatting" promise exists to prevent, and it would have
  shipped silently wrong if not checked before choosing the mechanism.
  Fixed by using `hclsyntax` for byte-range discovery and a direct byte
  splice instead (see Open decisions) — confirmed the fix by re-running
  the same repro and checking the comment survived.
- 2026-07-11: **`hclsyntax.Expression.Value(nil)` is a complete,
  already-built literal-detector — no need to hand-write an expression-
  type switch.** Evaluating any HCL expression against a `nil`
  `hcl.EvalContext` (no variables, no functions available) succeeds
  exactly when the expression is a pure literal — a plain string/number/
  bool, a template with no interpolation, or an object/list constructor
  whose members are *all* themselves literal — and fails with a specific,
  legible diagnostic ("Variables not allowed", "Function calls not
  allowed") for anything that references a variable, calls a function, or
  interpolates a variable into a string. Verified this across seven
  distinct expression shapes in a throwaway repro before relying on it
  for real, including the trickiest case — a plain-looking quoted string
  that turns out to contain `${var.x}` interpolation, which correctly
  fails rather than being mistaken for a safe literal just because it's
  syntactically inside quotes.
- 2026-07-11: **A previous session's "UBI-11" label (the `ubx why` polish
  — resource-address support + attribution rendering) was never checked
  against Linear, and turns out to be wrong.** This session's own task
  came with an explicit instruction to verify the ticket ID against
  Linear's title rather than infer the next number in sequence — doing
  that surfaced that the real `UBI-11` is "M3–4 decision loop," a
  different, larger piece of work, still in Backlog status when this
  session started. The earlier session had inferred "UBI-11" purely from
  it being the next unused number after UBI-10, which was never actually
  true. Nothing was rewritten (the earlier commit already references the
  wrong number, and rewriting pushed history to fix a label would be its
  own kind of inaccuracy) — just corrected going forward, with a note left
  at the point in STATE.md where the confusion would otherwise resurface.
  Lesson banked: check Linear before writing a ticket ID into a commit
  message or STATE.md, every time, not just when explicitly told to.
- 2026-07-11: **`git show <sha>:<path>` reports a missing path with the
  literal text `fatal: path '<path>' does not exist in '<sha>'`, exit code
  128 — indistinguishable, by exit code alone, from `<sha>` itself being
  an invalid revision.** Confirmed by actually running it against a
  throwaway repo before writing `github.looksLikeMissingPath`'s string
  match, not assumed from git documentation or memory — the same
  "verify before implementing" discipline this project has followed since
  Slice 1's protocol-version surprises. This is why `FileAtCommit` and
  `CommitExists` are two separate checks in `DeriveAcceptance`'s flow
  (commit existence checked first, independently) rather than trying to
  infer both facts from one `git show` call's exit code.
- 2026-07-10/11: **CloudTrail's `ResourceName` lookup attribute wants the
  resource's own `id` (bucket name / role name / vpc-id), not its ARN —
  confirmed directly against the real account, and the opposite of the
  first assumption.** Reasoning going in: ARNs are globally unique and
  more "correct" as an identity, so searching by ARN seemed like the
  obviously right choice. Tested empirically before writing
  `identityCandidates`: `aws cloudtrail lookup-events
  --lookup-attributes AttributeKey=ResourceName,AttributeValue=<bucket
  name>` returned real, correct events (`PutBucketTagging`,
  `DeleteBucketTagging`); the identical query with
  `AttributeValue=arn:aws:s3:::<bucket name>` (the full ARN) returned
  **zero events**, even though the events genuinely existed and were
  queryable by name. Repeated the same test for `aws_iam_role` (name
  works, ARN returns nothing) and `aws_vpc` (vpc-id works — it has no
  separate "name" to compare against). This is why `identityCandidates`
  tries `id` first, with `arn`/`name` kept only as fallbacks — and why it
  wasn't promoted into a static table (see Open decisions): a rule that's
  only been checked against three resource types, all AWS-managed
  identity conventions that could easily differ for another service, is
  exactly the kind of thing this project has repeatedly learned not to
  generalize from a handful of data points.
- 2026-07-10/11: **CloudTrail's real event-delivery latency in this
  account measured ~2-3 minutes, not the near-instant response an
  earlier manual probe happened to show.** Building the identity-matching
  finding above, a manual `aws s3api put-bucket-tagging` followed
  immediately by `aws cloudtrail lookup-events` returned the matching
  event right away — this shaped an initial (wrong) assumption that
  delivery was effectively instant. The first version of the live
  verification test used a 5-attempt/3-second retry budget (~15 seconds
  total) based on that assumption and failed: the real account's
  `PutBucketTagging` event from that specific test run took roughly two
  minutes forty seconds to become queryable, confirmed by polling
  manually and watching it appear. Fixed by widening the live test's
  retry budget to 5 minutes (15 attempts, 20 seconds apart) rather than
  weakening the assertion — the test now passes reliably (137s in the
  run that shipped this). This is also exactly why `delivery_window`
  exists as a distinct reason from `no_matching_event` in the schema
  amendment: real accounts can't be assumed to deliver CloudTrail events
  fast just because a single manual check once looked instant.
- 2026-07-10: **A provider's `GetProviderSchema` costs nothing and needs no
  credentials — it's a pure local gRPC call against the launched binary,
  no `Configure`, no AWS API round trip.** This is what made UBI-9 batch
  3's whole approach possible: rather than guessing at FakeOnly types'
  attribute shapes, the real AWS provider's schema could be inspected for
  all 43 remaining types in one shot (`cmd/schemadump`, a throwaway tool,
  deleted before committing) with zero cost/risk — the same "real
  provider, safe operation" category `ubx scan`'s own reads already occupy
  (docs/architecture.md's "wedge reads and records before it ever
  writes"), just one layer up (schema vs. instance state). This produced a
  finding worth stating plainly: nearly every AWS resource type in this
  list carries `tags`/`tags_all` in its real schema — confirmed
  individually per type, not assumed — which is why the fakeprovider
  fixture's default shape converged on "id + arn + tags/tags_all, mutate
  tags" almost everywhere; the handful of types that don't (join/
  attachment resources, and a few sub-resource-of-a-bucket types) needed
  their own bespoke fixture attribute, or turned out to have no mutable
  field at all and got parked (see below).
- 2026-07-10: **Two more types are joins with nothing to mutate, exactly
  like `aws_iam_group` — but found through free schema inspection instead
  of a live API call.** `aws_iam_role_policy_attachment`'s real schema is
  exactly `{id, policy_arn (required), role (required)}`; nothing is
  optional besides the computed `id`. `aws_route_table_association`'s is
  `{gateway_id, id, region, route_table_id (required), subnet_id}` — the
  two optional fields are mutually-exclusive selectors for *what* it's
  associated with, and changing that is a replace in AWS's own model, not
  an in-place modify (matching how Terraform providers generally implement
  these join resources — `ForceNew` on the target field). Parked in
  `conformance/registry.go` with the schema-derived reasoning, same
  discipline as `aws_iam_group`.
- 2026-07-10: **IAM groups have no tagging API at all — `aws iam
  tag-group` doesn't exist — and the `aws_iam_group` schema has no other
  field that's both mutable and observable.** Discovered by trying it
  (`aws iam tag-group --group-name ... --tags ...` → "Found invalid choice
  'tag-group'"), not by assuming groups work like roles/users/policies
  just because they're all IAM. Path is set at creation and immutable
  after; there's no tags field in the schema either. This means
  `aws_iam_group`'s *adopt* half works fine (same `id`+`name` lookup shape
  as role/user), but there's no real out-of-band mutation available to
  drive the *mutate* half of adopt→mutate→scan-diff — parked as
  `fake-only` with the reasoning recorded in `conformance/registry.go`,
  per UBI-9's own "types that fight back get documented + parked, not
  hacked" framing, rather than inventing a fake mutation or skipping the
  type silently.
- 2026-07-10: **Batch 2's four real-safe types split cleanly into two
  lookup shapes, matching a pattern first hinted at in batch 1 — but still
  checked individually, not assumed to generalize.** Resources whose `id`
  attribute already **is** the ARN or a URL (`aws_sqs_queue`'s queue URL,
  `aws_sns_topic`/`aws_iam_policy`'s ARN) need only `{"id": "..."}`.
  Resources whose `id` is a **name** rather than an ARN/URL
  (`aws_iam_user`, matching `aws_iam_role` from batch 1) need `id`+`name`
  both, `name` alone reading back `null`. Framework-style `aws_vpc` (batch
  1) needing only `id` fits the first shape too, despite not being
  ARN/URL-identified — its `id` is just the `vpc-*` identifier directly,
  with no separate "name" attribute to omit in the first place. Recorded
  per-type in `conformance/registry.go`, not generalized into a rule the
  harness relies on — six data points across two protocol generations is
  still not enough to trust as a blanket assumption for the next 44 types.
- 2026-07-10: **`aws_iam_role`'s lookup needs `id`+`name` both set, exactly
  like `aws_s3_bucket` needed `id`+`bucket` — but this was checked
  empirically before writing it down, not assumed from the S3 precedent,
  and a first guess ("just `name`") was wrong.** Sent `{"name":
  "aws-codestar-service-role"}` alone first (reasoning: SDKv2 resources
  often use a natural-name field) and got back `null` — same failure shape
  as S3's original `{"bucket": "..."}`-alone finding from Slice 1. Adding
  `id` alongside fixed it. `aws_vpc`, tested the same way, needed only
  `id` — no natural-name duplication at all. Recorded in
  `conformance/registry.go`'s `Notes` for both types specifically so this
  doesn't need re-discovering for the next SDKv2-style type in batch 2 (and
  so nobody assumes "SDKv2 needs id+name" as a blanket rule from a sample
  of two — it might not hold for every type either).
- 2026-07-10: **OpenTofu's mirrored provider release archives aren't always
  the one-file-only zips HashiCorp's original releases are.** First real
  (non-test) `Acquire` call against `hashicorp/aws@6.54.0` failed with
  "expected exactly one file in the provider archive, found 4" — the
  OpenTofu-mirrored zip also ships `CHANGELOG.md`/`LICENSE`/`README.md`
  alongside the actual `terraform-provider-aws` binary. Fixed by picking
  the entry named with Terraform's own `terraform-provider-*` binary
  convention (which every provider binary is required to follow for
  Terraform's own provider discovery to work at all) instead of assuming
  archive-has-exactly-one-file; kept the one-file case as a fallback for
  any oddly-packaged release. Added a dedicated test
  (`TestAcquire_ArchiveWithExtraFiles`) reproducing this exact shape so it
  can't regress silently. Caught before real-world verification counted as
  done, not after — the "verify against reality" step earns its keep again.
- 2026-07-10: **docs/architecture.md's "protocol v6 only" premise did not
  hold against real provider binaries.** Downloaded and tested two official
  HashiCorp binaries directly (env vars + raw exec, not through ubx, to rule
  out a bug on our side): `terraform-provider-aws` 6.54.0 and
  `terraform-provider-time` 0.9.2 (a pure terraform-plugin-framework
  provider, no SDKv2) both report `1|5|unix|...|grpc` — v5 — even when the
  client explicitly requests v6 via `PLUGIN_PROTOCOL_VERSIONS`. Traced into
  go-plugin's own `protocolVersion()` negotiation (server.go): a requested
  version only wins if the server actually registered it; neither binary
  registers v6 at all in the tested builds. Resolved this session: dual
  v5/v6 client (see Done above); docs/architecture.md and docs/plan.md
  updated with the finding and the decision.
- 2026-07-10: **Real providers require cty-msgpack, not the DynamicValue
  JSON field, for Configure/ReadResource request payloads.** A
  JSON-encoded provider config produced an immediate `EOF` diagnostic from
  terraform-provider-aws — consistent with an SDKv2-vintage decoder handed
  zero bytes because it only ever reads the msgpack field. Switched to
  encoding all requests as cty-msgpack via `github.com/zclconf/go-cty`
  (MIT), decoding responses the same way (preferring msgpack, falling back
  to json if a provider ever populates it).
- 2026-07-10: **Nested schema blocks aren't optional to model, even for a
  "just read one resource" milestone.** Sending a cty object built only from
  a schema's flat `attributes` (ignoring `block_types`) got a hard rejection
  from terraform-provider-aws: `"an object with 35 attributes is required
  (30 given)"`. A real provider's own decoded object type includes one
  attribute per nested block (object/list/set/map of the nested block's own
  type, depending on nesting mode) — ubx's `Schema`/`Block` model and
  `blockObjectType` now handle this recursively.
- 2026-07-10: **gRPC's 4MiB default message size is too small for a real
  provider schema dump.** AWS's full `GetProviderSchema` response is
  ~7MiB. Real provider binaries configure a 256MiB server-side limit
  (`grpcMaxMessageSize` in tf5server/tf6server); ubx's client now matches it.
- 2026-07-10: **ReadResource needs the resource's `id` in current_state, not
  just its natural-language identifier.** For `aws_s3_bucket`, sending only
  `{"bucket": "..."}` got back a null state (provider's Read function
  couldn't find anything to read); sending `{"id": "...", "bucket": "..."}`
  worked. SDKv2-style Read functions key off the state's `id` attribute
  specifically. Relevant for whatever builds import/adoption proposals later
  (docs/architecture.md's Import concept) — the resource ID convention is
  per-resource-type and not always the same attribute name.
