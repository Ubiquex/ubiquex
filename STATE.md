# STATE.md — living project state

> Updated as the last act of every working session. This file is the handoff.

## Current phase

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

**UBI-11 is done (this session): `ubx why` polished ahead of demo
recording, closing two gaps a dry run surfaced.**

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

## Current focus

UBI-11 is closed. Next up (see Next steps): the Core IR + resolver work
that's been queued since before UBI-9, and `status --drift` (a read-only
multi-resource drift report, still M1-2 scope per docs/plan.md).

## Open decisions

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

## Next steps

1. **UBI-9, UBI-10, and UBI-11 are all closed** — nothing further queued
   under any of them. If a type's fixture-verified shape ever turns out
   wrong once real usage exercises it, or if CloudTrail's `ResourceName`
   matching behaves differently for a type not yet checked live, fix it
   as a normal bug (`conformance/registry.go`'s `Notes` /
   `core/attribution.go` respectively), not a reason to reopen a
   milestone.
2. Now unblocked: Core IR + resolver (component map #1-2) — the natural
   next session; nothing else in M1-2's detection core is blocking it.
   `status --drift` (a read-only report over what `ubx scan` would find
   across multiple resources) is also still M1-2 scope, not started —
   would naturally reuse `core.AttributeDrift` per resource the same way
   `ubx scan` does now, and `ubx why <address>`'s new chain view (UBI-11)
   for showing that report's history per resource.
3. UBI-10 gaps, not addressed this session, deliberately deferred: no
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
4. A `ubx provider ...` dev-facing CLI verb was deliberately never added
   across six sessions — still not part of the eventual product CLI
   surface (see docs/architecture.md component map). `ubx scan` now covers
   the "read one resource" use case anyway; revisit only if something else
   still needs raw schema/read access outside of scan/accept.
5. Not addressed, deliberately out of scope: PlanResourceChange/
   ApplyResourceChange (write path — deferred per docs/architecture.md
   "wedge reads and records before it ever writes"), AutoMTLS in provider/
   (still opt-in/unimplemented), cryptographic signing tier for acceptance
   (docs/architecture.md calls this out as "optional... later"; `ubx
   accept` only does the "local" tier). Note that `FoldState`'s O(chain)
   walk (see Open decisions) is an *accepted* limit, not deferred work —
   its own revisit trigger is stated there; don't re-open it as a TODO
   without something actually hitting that trigger.
6. UBI-8 gaps, not addressed this session either: no `UBX_PROVIDER_MIRROR`
   signature verification (by design — see docs/architecture.md, a local
   file is trusted differently); no cache invalidation/eviction; `ubx scan
   --source` doesn't route to a non-default registry hostname even though
   `ParseSource` would parse one. See prior entries for full detail.

## Docs debt

Per CLAUDE.md's session protocol: user-visible CLI changes create a docs
obligation in the ubiquex-docs (Mintlify) repo, batched and cleared per
slice rather than written inline during foundational work. This session's
debt:

- `ubx why` now accepts a `<stack>.<type>.<name>` resource address as an
  alternative to a proposal ID, rendering that resource's full proposal
  chain (newest first) instead of one proposal.
- `ubx why`'s rendering of `intent.sources` changed for two kinds:
  `cloudtrail` sources now show the actor/event/time/source-IP story
  inline; `cloudtrail_unattributed` sources show their reason in words.
  Existing kinds (`dialogue`/`manual_edit`/`issue`) are visually unchanged.

Not addressed this session (pre-existing, from prior slices, noted here
since this is the first time this debt has been tracked in STATE.md at
all — worth clearing alongside the above rather than letting it grow
further): UBI-8's `--source`/`--provider-version` acquisition flags on
`scan`/`accept`, and UBI-10's `--no-attribution` flag and the
`cloudtrail`/`cloudtrail_unattributed` proposal fields themselves, have no
user-facing documentation yet either.

## Surprises / findings

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
