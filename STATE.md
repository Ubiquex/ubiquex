# STATE.md — living project state

> Updated as the last act of every working session. This file is the handoff.

## Current phase

**UBI-9 batch 2 done: 7 of 51 AWS resource types now conformance-tested
against the real account, plus one deliberately parked.** Batch 1 (last
session) proved the harness across storage/IAM/network
(`aws_s3_bucket`/`aws_iam_role`/`aws_vpc`). Batch 2 added four more, all
create-and-destroy-per-test-run rather than adopt-something-pre-existing:
`aws_sqs_queue`, `aws_sns_topic` (messaging), `aws_iam_policy`,
`aws_iam_user` (IAM) — each verified free/negligible-cost, each cleaned up
completely afterward (checked, not assumed). `aws_iam_group` was
investigated and explicitly **parked**, not forced or silently skipped:
IAM groups have no tagging API at all (confirmed empirically — there is no
`aws iam tag-group`) and nothing else in the schema is both mutable and
observable, so there's no real out-of-band mutation to test drift
detection against. This is exactly the "types that fight back get
documented + parked" case UBI-9 was scoped to expect.

Live conformance tests remain gated behind `UBX_CONFORMANCE_LIVE=1` and
skipped by default — `go test ./...` stays hermetic and credential-free
project-wide.

UBI-8 (provider acquisition) and UBI-7 (Slice 3 + follow-ups) remain done
from prior sessions (see below).

## Current focus

7 of 51 types implemented (one per storage/network, three IAM, two
messaging), one parked. Next is batch 3 — see Next steps for a suggested
order (the remaining cheap/free real-safe candidates, e.g.
`aws_cloudwatch_log_group`/`aws_kms_key`/`aws_secretsmanager_secret`,
before the fake-only compute/network/DB/DNS types, which need per-type
fakeprovider schema fixtures rather than just a real resource to create).

## Open decisions

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

## Next steps

1. **UBI-9 batch 3**: pick up the next slice of `conformance/registry.go`'s
   44 not-yet-`Implemented` types (7 of 51 done; one of the 44,
   `aws_iam_group`, is parked rather than merely pending — stays
   fake-only until a fakeprovider fixture covers its mutate step).
   Suggested order, continuing batch 2's logic (cheap/free real
   types before fakeprovider-fixture work): `aws_cloudwatch_log_group`,
   `aws_kms_key`, `aws_secretsmanager_secret` (all negligible-cost,
   create+destroy per run, same pattern as batch 2's four).
   `aws_cloudwatch_metric_alarm` too, though it may need an existing metric
   to attach to — check before assuming. After that, the remaining IAM
   types (`aws_iam_role_policy_attachment`, `aws_iam_instance_profile`,
   `aws_iam_openid_connect_provider`) are all still free but likely need
   a companion resource (a role/policy to attach to) rather than standing
   alone — probably still real-safe, just slightly more setup per test.
   Then the fake-only compute/network/db/dns types, which need per-type
   fakeprovider schema fixtures rather than just a real resource to create
   — expect that to be slower per-type than batches 1-2. Every new
   `Implemented: true` entry needs `IdentityFields`+`Notes` filled in for
   real (registry_test.go enforces this), verified empirically every time
   — never assumed from a similar-looking type, even within the same
   batch (batch 2's four all turned out to follow one of exactly two
   shapes — id-is-the-arn/url vs id+name-duplication — but that's an
   observation from four data points, not a rule to stop checking against).
2. Still not started: Core IR + resolver (component map #1-2), and
   CloudTrail correlation (UBI-10, per docs/plan.md's own M1-2 scope) —
   `IdentityFields` is being captured per type specifically so UBI-10 has
   ARN/equivalent identity data to correlate against once it starts.
   `status --drift` (a read-only report over what `ubx scan` would find
   across multiple resources) is also still M1-2 scope, not started.
3. A `ubx provider ...` dev-facing CLI verb was deliberately never added
   across four sessions — still not part of the eventual product CLI
   surface (see docs/architecture.md component map). `ubx scan` now covers
   the "read one resource" use case anyway; revisit only if something else
   still needs raw schema/read access outside of scan/accept.
4. Not addressed, deliberately out of scope: PlanResourceChange/
   ApplyResourceChange (write path — deferred per docs/architecture.md
   "wedge reads and records before it ever writes"), AutoMTLS in provider/
   (still opt-in/unimplemented), cryptographic signing tier for acceptance
   (docs/architecture.md calls this out as "optional... later"; `ubx
   accept` only does the "local" tier). Note that `FoldState`'s O(chain)
   walk (see Open decisions) is an *accepted* limit, not deferred work —
   its own revisit trigger is stated there; don't re-open it as a TODO
   without something actually hitting that trigger.
5. UBI-8 gaps, not addressed this session either: no `UBX_PROVIDER_MIRROR`
   signature verification (by design — see docs/architecture.md, a local
   file is trusted differently); no cache invalidation/eviction; `ubx scan
   --source` doesn't route to a non-default registry hostname even though
   `ParseSource` would parse one. See prior entries for full detail.

## Surprises / findings

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
