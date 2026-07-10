# STATE.md — living project state

> Updated as the last act of every working session. This file is the handoff.

## Current phase

**Slice 3 (UBI-7), done and its three follow-up flags resolved.**
`ubx scan` closes the loop: reads a resource's live state, compares it
against the ledger (`core.RunScan`), classifies it as new/drifted/
unchanged, and generates a zero-blast-radius `adoption`/`drift_adopt`
proposal (`core.GenerateProposal`) that `ubx accept` (now with an optional
`--reverify-with` staleness guard) and `ubx why` handle exactly as they
already did. Verified for real: adopted the real `ubx-states` S3 bucket,
mutated a tag on it directly via the AWS CLI, scanned again, and got back a
precise `tags.ubx-demo`/`tags_all.ubx-demo` diff. This is docs/plan.md's
stated exit for the three foundational slices: "point at a messy account,
resolve a drift with a signed record."

UBI-7 follow-up (same day): the three architectural flags from that
session were each resolved rather than left open — `core` no longer
imports `provider` (inverted via a `core.StateReader` interface), the
resource lookup key is now persisted in `resolution.inputs[].lookup`
(docs/schema.md amended, no schema_version bump needed — see below for
why), and `Ledger.FoldState`'s O(chain) walk is now a documented, accepted
tradeoff rather than an unresolved worry. See Open decisions and Done.

## Current focus

All three foundational slices (talk to a provider, trust core, close the
loop) are done. Next per docs/plan.md is the M1-2 wedge buildout (top ~50
AWS resource types, CloudTrail correlation, `status --drift`) — a
meaningfully bigger scope step up from "one resource at a time by explicit
CLI flags," which has been this arc's deliberate, honest scaling limit so
far. See Next steps for what M1-2 will actually require that doesn't exist
yet (auto-discovery, CloudTrail attribution, a real IR/resolver).

## Open decisions

- [ ] Provider binary acquisition: download from registry.terraform.io with
      signature verification vs. vendored for dev
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

## Next steps

1. Begin M1-2 (wedge buildout) per docs/plan.md: top ~50 AWS resource types
   via ReadResource, CloudTrail correlation (drift → actor/timestamp/
   session), `status --drift`. This is a real scope jump from Slice 3's
   "one resource, named explicitly on the CLI" — it needs auto-discovery
   (enumerate what exists in an account, not be told), which in turn wants
   at least a minimal typed IR resource-node (component map #1, still not
   built — see below) rather than `ubx scan`'s current opaque
   `--type/--name/--lookup` flags per resource.
2. Still not started: Core IR + resolver (component map #1-2). Every slice
   so far has deliberately deferred this (Slice 2: hand-written JSON input;
   Slice 3: one resource, one CLI invocation) — M1-2's auto-discovery is
   likely the point where it can't be deferred further.
3. A `ubx provider ...` dev-facing CLI verb was deliberately never added
   across three sessions — still not part of the eventual product CLI
   surface (see docs/architecture.md component map). `ubx scan` now covers
   the "read one resource" use case anyway; revisit only if something else
   still needs raw schema/read access outside of scan/accept.
4. Not addressed, deliberately out of scope: CloudTrail attribution (M1-2
   milestone), PlanResourceChange/ApplyResourceChange (write path —
   deferred per docs/architecture.md "wedge reads and records before it
   ever writes"), AutoMTLS in provider/ (still opt-in/unimplemented),
   cryptographic signing tier for acceptance (docs/architecture.md calls
   this out as "optional... later"; `ubx accept` only does the "local"
   tier — records approver/method/timestamp, no actual signature). Note
   that `FoldState`'s O(chain) walk (see Open decisions) is an *accepted*
   limit, not deferred work — its own revisit trigger is stated there;
   don't re-open it as a TODO without something actually hitting that
   trigger.

## Surprises / findings

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
