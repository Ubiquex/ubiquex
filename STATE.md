# STATE.md — living project state

> Updated as the last act of every working session. This file is the handoff.

## Current phase

**Slice 2, done.** `core/` package: typed `Proposal`, canonical hashing per
the now-RATIFIED docs/schema.md rules, an append-only per-stack ledger, and
local acceptance. `ubx accept`/`ubx why` close the loop end-to-end: a
hand-written proposal JSON file → canonical hash → local signing → ledger
append → read back. Slice 1 (dual-protocol provider client) remains done
from last session.

## Current focus

Slice 2 exit criteria are met. Next up per docs/plan.md is Slice 3 (close
the loop — `ubx scan` drift detection, adoption proposals), unless Roozbeh
wants to build out the Core IR / resolver first (component map #1-2, still
not started — Slice 2 kept delta payloads as opaque JSON specifically to
avoid needing them yet, see Next steps).

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
    Next steps for why.
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

## Next steps

1. A `ubx provider ...` dev-facing CLI verb was again deliberately NOT added
   last session — still not part of the eventual product CLI surface (see
   docs/architecture.md component map). Revisit once the CLI verb surface
   itself is being designed.
2. Begin Slice 3 (close the loop) per docs/plan.md: `ubx scan` (provider
   reality vs ledger → drift detected), drift → adoption proposal generated
   → accept → ledger updated → `why` explains. This is the slice's stated
   demo exit: "point at a messy account, resolve a drift with a signed
   record."
3. Still not started: Core IR + resolver (component map #1-2). Slice 2
   deliberately kept `delta.creates/modifies/destroys` as opaque
   `json.RawMessage` rather than typed IR nodes, since hand-written JSON was
   explicitly this slice's input and the IR/resolver don't exist yet. Slice
   3's adoption-proposal generation will likely be the first real pressure
   to build at least a minimal typed IR resource-node type.
4. `delta.modifies`/`delta.destroys` element shape is still schema.md's
   "..." placeholder (only `delta.creates`, matching the IR resource-node
   example, has a pinned (stack,type,name) shape). `core.deltaSortKey`
   (core/canonical.go) has a best-effort fallback for the other two shapes
   (a `target` field, string or object) — flagged as an interpretation, not
   a ratified decision. Worth pinning down before real adoption/revert
   proposals start populating those arrays for real.
5. Not addressed, deliberately out of scope: CloudTrail attribution (M1-2
   milestone), PlanResourceChange/ApplyResourceChange (write path —
   deferred per docs/architecture.md "wedge reads and records before it
   ever writes"), AutoMTLS in provider/ (still opt-in/unimplemented),
   cryptographic signing tier for acceptance (docs/architecture.md calls
   this out as "optional... later"; `ubx accept` only does the "local"
   tier — records approver/method/timestamp, no actual signature).

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
