# ubiquex-cli

`ubx` — infrastructure change management.

Every infrastructure change is a typed, hashed, signed proposal in an append-only
ledger. Current infrastructure = fold(applied proposals). Talks to Terraform
providers directly (tfplugin v6) — no Terraform, no state files.

**Status:** pre-alpha, foundational slices in progress.

## Layout

- `core/` — IR, ledger, canonical hashing
- `provider/` — tfplugin v6 client
- `cli/` — the `ubx` binary
- `docs/` — architecture, schema constitution, plan

See `docs/architecture.md` for the system model.
