# ubx_sdk

The describe-only Python runtime for [Ubiquex](https://github.com/ubiquex/ubiquex) SDK programs.

A program built on `ubx_sdk` never computes, never reaches a provider, and
never touches a ledger -- it describes a desired end-state (an
`ubx:intent/v1` document) and stops. `resource()` returns a `Computed`
reference, never a real value, so a resource's not-yet-known attribute can
still be wired into a sibling resource's config at describe time.

This package is the runtime shared by every `ubx sdk gen --lang py`
generated bindings package (e.g. `ubx-sdk-aws-py`, `ubx-sdk-google-py`) --
generated code does `import ubx_sdk as sdk` and calls `sdk.resource(...)`,
`sdk.stack(...)`, `sdk.run(...)` against the binding's own generated
`ResourceBinding`/`Config` types.

See [docs.ubiquex.io](https://docs.ubiquex.io) for the full SDK guide.

## License

Apache-2.0
