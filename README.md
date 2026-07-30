# ubiquex

`ubx` — infrastructure change management via a proposal ledger.

Every infrastructure change is a typed, hashed, signed proposal in an append-only
ledger. Current infrastructure = fold(applied proposals). Talks to Terraform
providers directly (tfplugin v5/v6) — no Terraform, no state files.

**Status:** pre-alpha. Releases (`v0.1.0` on) publish via GitHub Releases.

**Docs:** https://github.com/Ubiquex/ubiquex-docs (Mintlify source; not yet publicly hosted).

## Layout

- `core/` — IR, ledger, canonical hashing
- `provider/` — tfplugin v5/v6 client
- `cli/` — the `ubx` binary
- `sdk/` — the multi-language SDK monorepo (TypeScript/Go/Python)
- `docs/` — architecture, schema constitution, plan

See `docs/architecture.md` for the system model, `docs/source-tree.md`
for the full package-by-package layout.
