<!--
DRAFT — NOT SUBMITTED. This is a draft issue report for
https://github.com/hashicorp/terraform-provider-helm, prepared for
Roozbeh's review (UBI-24). Submitting it to the real upstream tracker is
his call, not something done automatically as part of this session.
-->

# `helm_release`: `manifest` and revision `metadata` can silently carry
# `set_sensitive` values in plaintext

## Summary

`helm_release`'s `set_sensitive` block exists specifically to keep a
sensitive value out of Terraform's own plan/apply output and state
diffs. However, once that value flows into a chart's rendered output —
which is exactly what `set_sensitive`/`set`/`values` are *for* — it can
still surface, in full plaintext, in two places the schema does not mark
`Sensitive`:

- `manifest` (the release's full rendered Kubernetes manifest, a
  top-level `computed` string attribute)
- `metadata[0].values` and `metadata[0].notes` (the release's fully
  resolved values, as a JSON string, and any chart NOTES.txt output,
  both inside the `metadata` attribute's own nested object)

Confirmed directly against `hashicorp/helm` 2.17.0's real
`GetProviderSchema` response:

```
attr name=manifest  computed=true  sensitive=false  type="string"
attr name=metadata   computed=true  sensitive=false
  type=["list",["object",{"values":"string","notes":"string", ...}]]
```

Neither is flagged `Sensitive`. Any tool (or human) reading Terraform
state, a `terraform show -json` dump, or any downstream consumer of this
provider's own read-back data can see the real value in plaintext via
either field, entirely independent of whether it was set via `set` or
`set_sensitive`.

## Reproduction

```hcl
resource "helm_release" "example" {
  name       = "example"
  chart      = "./mychart"
  namespace  = "default"

  set_sensitive {
    name  = "dbPassword"
    value = "hunter2"
  }
}
```

Given a chart whose templates render `.Values.dbPassword` anywhere in
their output (a Secret, a ConfigMap, a startup script, ...), after
`terraform apply`:

- `terraform show -json` (or any direct read of `helm_release.example`'s
  own state/attributes) shows `dbPassword` correctly redacted wherever
  `set_sensitive` itself is echoed back — but the *same string*
  `"hunter2"` is fully visible in:
  - `manifest` — the rendered Kubernetes YAML this provider itself
    computes and stores.
  - `metadata[0].values` — the resolved values JSON, e.g.
    `"{\"dbPassword\":\"hunter2\"}"`.

We found this while building [`ubx`](https://github.com/Ubiquex/ubiquex-cli),
a tool that reads Terraform providers' own `Sensitive` schema flags to
decide what never to persist to its own storage. Because `manifest`/
`metadata.values`/`metadata.notes` aren't flagged, our first
implementation redacted `set_sensitive` values correctly wherever they
appeared as themselves, but reproduced the same plaintext value verbatim
via these two other fields — the exact class of bug `set_sensitive`
exists to prevent in the first place. We've since added our own,
tool-side override table to force-redact these three specific paths
regardless of the schema (happy to share the mechanism if useful), but
the more durable fix belongs upstream, in the schema itself.

## Why this happens (and why `metadata` is the harder case)

- **`manifest`** is a plain, top-level `string` attribute. Flagging it
  `Sensitive: true` is schema-shape-compatible today — no restructuring
  needed, just the flag.
- **`metadata`**, however, is a *compound-typed* attribute
  (`list(object({...}))`), not a nested block built from its own
  sub-attributes. The tfplugin wire protocol's `Sensitive` flag is a
  single bool per top-level `Attribute` — there is no mechanism to flag
  one field *inside* a compound-typed attribute's object shape without
  flagging the entire attribute. Flagging all of `metadata` `Sensitive`
  would also hide genuinely non-sensitive, useful fields on it (`name`,
  `namespace`, `chart`, `version`, `revision`, `first_deployed`,
  `last_deployed`) — a real, if smaller, usability cost.

## Suggested options (any of these would close the gap; not prescribing which)

1. Flag `manifest` as `Sensitive: true`. Low cost, no schema shape
   change; the main tradeoff is Terraform's own CLI diff output no
   longer shows a manifest change inline (a `computed` attribute's
   change is normally shown for review) — arguably an acceptable
   tradeoff given what it can carry today.
2. Flag the whole `metadata` attribute `Sensitive: true`, accepting that
   its non-sensitive fields become opaque too — the "least engineering,
   most conservative" fix.
3. Restructure `metadata` from a compound-typed attribute into a real
   nested block with its own per-sub-attribute schema (each of `values`/
   `notes` individually flaggable `Sensitive`, everything else left
   alone) — the most correct fix, and a breaking schema change requiring
   a major version bump.
4. Add a documentation note on `helm_release` calling this out
   explicitly, so consumers relying on schema `Sensitive` flags (state
   inspection tools, policy engines, anything like `ubx`) know not to
   treat `manifest`/`metadata.values`/`metadata.notes` as safe by
   default. Cheapest option, weakest guarantee — worth doing regardless
   of which other option is chosen, since it costs nothing.

Happy to open a PR for whichever direction the maintainers prefer.

## Environment

- `terraform-provider-helm` 2.17.0
- Found via direct `GetProviderSchema` inspection (tfplugin protocol v5),
  not a guess from documentation.
