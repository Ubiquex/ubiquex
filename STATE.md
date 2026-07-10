# STATE.md — living project state

> Updated as the last act of every working session. This file is the handoff.

## Current phase

**Slice 1, in progress, blocked on a decision.** `provider/` package speaks
tfplugin v6 (handshake + GetProviderSchema), fully tested against fake plugin
binaries. Verified against two real HashiCorp provider binaries that both
serve v5, not v6 — see Surprises below. Need a call on how to proceed before
ReadResource work continues.

## Current focus

Slice 1 — talk to one Terraform provider directly:
- Launch the AWS provider binary, tfplugin v6 handshake
- `GetProviderSchema` → dump schema for one resource type
- `ReadResource` against one real AWS resource (S3 bucket or RDS instance)

## Open decisions

- [ ] Canonical hashing serialization format (see docs/schema.md §Hashing — draft
      proposes canonical JSON / JCS-style; confirm before first hash is written)
- [ ] Provider binary acquisition: download from registry.terraform.io with
      signature verification vs. vendored for dev
- [ ] Go module path final confirmation (`github.com/ubiquex/ubiquex-cli`)
- [ ] **NEW, blocking further Slice 1 work — protocol v6-only premise.**
      docs/architecture.md says "Scope containment: AWS provider first,
      protocol v6 only." Empirically, real provider binaries (both
      terraform-provider-aws 6.54.0 and terraform-provider-time 0.9.2, tested
      directly, bypassing ubx entirely) serve tfplugin protocol **v5** over
      the wire regardless of what `PLUGIN_PROTOCOL_VERSIONS` the client
      advertises — see Surprises below for the evidence. Options: (a) add v5
      support (tfplugin5 proto + a version-negotiating client that requests
      "5,6" and accepts whichever comes back), (b) keep v6-only and accept
      that today's real AWS provider builds won't work until/unless they
      change, (c) something else. This changes the shape of `provider/`
      (dual protocol stub, or a v5-primary client) if (a).

## Done

- 2026-07-10: Repo founded. CLAUDE.md, STATE.md, docs/ (architecture, schema v0.1,
  plan, prompts) written from the v2 design session.
- 2026-07-10: Go module (`github.com/ubiquex/ubiquex-cli`) initialized. Cobra CLI
  skeleton added: `cmd/ubx/main.go` entrypoint, `cli/root.go` root command,
  `cli/version.go` (`ubx version`, `Version` var overridable via ldflags). Tests
  in `cli/version_test.go` and `cli/root_test.go`, all green (`go build ./...`,
  `go vet ./...`, `go test ./...`).
- 2026-07-10: Slice 1 first cut — `provider/` package.
  - `provider/tfplugin6/`: tfplugin6.proto vendored verbatim from
    `github.com/hashicorp/terraform-plugin-go@v0.31.0` (per that file's own
    "copy this into your codebase" instruction), Go/gRPC stubs generated via
    protoc + protoc-gen-go + protoc-gen-go-grpc. No dependency on
    hashicorp/go-plugin — the handshake is hand-rolled (see below) for full
    control over failure semantics, matching the "native executor owns
    failure semantics" principle in docs/architecture.md.
  - `provider/handshake.go`: parses the go-plugin handshake line
    (`CORE|APP|NETWORK|ADDR|PROTOCOL`). Magic cookie key/value, core protocol
    version (1), and app protocol version (6) verified directly against
    terraform-plugin-go's tf6server source, not assumed from memory.
  - `provider/client.go`: `Launch(ctx, path, opts...)` starts the provider
    binary, sets the magic cookie env var, reads the handshake line with a
    configurable timeout, dials gRPC over the reported unix/tcp socket
    (plaintext — AutoMTLS/PLUGIN_CLIENT_CERT deliberately not implemented
    yet, since it's opt-in and providers fall back to plaintext without it).
    `Client.GetProviderSchema`, `Client.Close`.
  - `provider/internal/fakeprovider/`: a small fixture binary (real gRPC
    server, real handshake line) used by tests instead of a real Terraform
    provider, selectable via `FAKEPROVIDER_MODE` env: ok / bad-core / bad-app
    / bad-protocol / malformed / hang / crash.
  - Tests (`provider/handshake_test.go`, `provider/client_test.go`): pure
    parser unit tests plus adversarial Launch tests — binary missing,
    handshake timeout, core/app/wire protocol mismatch, malformed line,
    plugin exiting before handshake — plus one happy-path test that actually
    calls GetProviderSchema over a real (fake) gRPC connection and checks
    the returned resource schema. All green.

## Next steps

1. **Decide the v5/v6 question above before continuing.** Flagged to Roozbeh;
   waiting on a call.
2. Once decided: finish Slice 1 — `ReadResource` against one real AWS
   resource (S3 bucket or RDS instance). Exit: attributed real-world read in
   a single CLI command.
3. A `ubx provider schema <path> <type>` (or similar) dev-facing CLI command
   was deliberately NOT added this session — it's not part of the eventual
   product CLI surface (see docs/architecture.md component map) and its
   shape depends on the v5/v6 decision above (single-stub vs. dual-stub
   client). Add once decided.

## Surprises / findings

- 2026-07-10: **docs/architecture.md's "protocol v6 only" premise does not
  hold against real provider binaries as they exist today.** Downloaded and
  tested two official HashiCorp binaries directly (env vars + raw exec, not
  through ubx, to rule out a bug on our side):
  - `terraform-provider-aws` v6.54.0 (darwin_arm64, from
    releases.hashicorp.com) — reports `1|5|unix|...|grpc`. It's built via
    SDKv2+Framework mux (tf5muxserver internally bridging v6-native
    resources down to v5), so v5 may be structurally the only protocol it
    ever externally offers.
  - `terraform-provider-time` v0.9.2 (darwin_arm64) — a **pure
    terraform-plugin-framework** provider, no SDKv2 dependency — also
    reports `1|5|unix|...|grpc`, even when the client explicitly sends
    `PLUGIN_PROTOCOL_VERSIONS=6`. Confirmed by tracing go-plugin's own
    `protocolVersion()` negotiation logic (hashicorp/go-plugin@v1.8.0,
    server.go) — a requested version only wins if the server actually
    registered it; absence of a match falls through to the server's lowest
    registered version. Since asking for "6" explicitly still returned "5",
    this provider build must not register version 6 at all.
  - Conclusion: as of today, real-world Terraform provider binaries — even
    modern framework-native ones — appear to default to serving tfplugin
    **v5** externally, not v6. This directly affects the "AWS provider,
    protocol v6 only" scope containment decision in docs/architecture.md and
    the wedge's viability if left unaddressed (the wedge is drift
    attribution on *existing* Terraform/OpenTofu repos, i.e. real provider
    binaries, not synthetic ones). Recorded as an open decision above per
    session protocol (stop, record, flag — not silently resolved here).
