# STATE.md — living project state

> Updated as the last act of every working session. This file is the handoff.

## Current phase

**Pre-Slice 1.** Go module + cobra CLI skeleton in place (`ubx version` only).
No Slice 1 provider code yet.

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

## Done

- 2026-07-10: Repo founded. CLAUDE.md, STATE.md, docs/ (architecture, schema v0.1,
  plan, prompts) written from the v2 design session.
- 2026-07-10: Go module (`github.com/ubiquex/ubiquex-cli`) initialized. Cobra CLI
  skeleton added: `cmd/ubx/main.go` entrypoint, `cli/root.go` root command,
  `cli/version.go` (`ubx version`, `Version` var overridable via ldflags). Tests
  in `cli/version_test.go` and `cli/root_test.go`, all green (`go build ./...`,
  `go vet ./...`, `go test ./...`).

## Next steps

1. Begin Slice 1 (see docs/plan.md §Slice 1): AWS provider binary, tfplugin v6
   handshake, `GetProviderSchema`, `ReadResource` against one real resource.
   This will introduce the `provider/` package.

## Surprises / findings

(none yet)
