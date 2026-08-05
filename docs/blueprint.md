# Blueprints — signed, reusable, parameterized proposal templates (UBI-74)

> **Slices 1–8 (this document): UBI-74's own original eight-slice plan,
> closed end to end.** Author a blueprint (Ubxfile: `lang`/`params`/
> `resources`) → **build** it once through the intent-provider pipeline
> into real, compilable SDK packages (Go/TS/Python, `--lang go|ts|py|all`)
> → **call** it, zero AI, from any medium (a hand-written SDK program via
> `ubx resolve --from-code`; a diagram's own `ubx_blueprint`-classed node;
> an md draft's own "Use blueprint X with..." recognition — all three
> compiling down to the SAME `blueprint_calls` wire field, expanded by
> literally invoking the target blueprint's own compiled function) →
> **distribute** it via any of four real mechanisms (a local path; a git
> repo+ref; a real OCI registry via ORAS, `ubx blueprint push`/`pull
> oci://...`; or a bare offline tarball file, `ubx blueprint pull
> <file>.tar.gz`) → every call **provenance-tagged**
> (`{"kind": "blueprint", "ref": "<name>:<content_hash>"}` on every
> resource it produces, `ubx why` rendering the full chain honestly,
> `ubx render` visually grouping it) → and content-hash **verified**
> (`ubx blueprint verify`, one hash scheme throughout — `core.
> CanonicalJSON`-based, unchanged since Slice 3, the SAME check whether a
> blueprint arrived via git, OCI, or a bare tarball) — real infrastructure
> shipped on real AWS, real artifacts pushed to real GHCR, at every stage
> along the way. Full design context (naming, trust model, the
> eight-slice breakdown, the rejected intermediate designs) lives in
> UBI-74's own Linear comment thread — read it before touching this arc
> again, later comments supersede earlier ones. Nesting (`uses:`) is
> UBI-121, tracked and designed separately, never touched here. The
> override mechanism and `render --sync-overrides` are UBI-86, a separate
> ticket, not touched here either. A real Strata registry SERVICE was
> never built — every registries operation in this arc talks to an
> EXISTING OCI registry (GHCR proven live) directly; see "OCI push/pull:
> Slice 7" below for why that's a deliberate, not a deferred, design.

## Scope: what Slices 1–8 build, and what they don't

**Slice 1** builds: parsing an `Ubxfile` (three keys only — `lang`,
`params`, `resources`); `ubx blueprint build .` (finds the Ubxfile the
same way `docker build .` finds a Dockerfile, resolves `resources:`
through the existing intent-provider pipeline — UBI-41's `DraftWithRetry`
— exactly once, then compiles the resulting draft into real, compilable
Go source: a typed function whose parameters match `params:`, whose body
makes real `sdk.Resource()` calls with real `Computed` refs between them,
reusing `sdk/go/runtime` — UBI-35's own evaluator — completely unchanged).

**Slice 2** builds: closing Slice 1's own named open point — `params:`
`default` values become genuinely load-bearing at call time via a
functional-options pattern (below) — and proving the built package is
actually callable: a plain Go SDK program (`sdk.Stack`/`sdk.Main`, no
blueprint machinery of its own) imports the locally-built package via an
ordinary Go module `replace` directive and calls its function with real
parameter values, resolving through `ubx resolve --from-code` exactly as
any other Go SDK program does.

**Slice 3** builds: `ubx blueprint package <dir> -o <file>.tar.gz`
(computes a content hash over a built blueprint directory's own files,
writes it into `dir/blueprint.lock.json`, archives the directory —
including that manifest — into a gzipped tar); `ubx blueprint pull
<source> <dest>` (a local filesystem path, copied as-is; or a git
repository, cloned and checked out at `--ref`, with `--path` naming the
blueprint's own location within it); `ubx blueprint verify <dir>`
(recomputes a pulled directory's own content hash and confirms it matches
its `blueprint.lock.json`'s declared hash). See "Package/pull/verify:
distribution" below for the full design.

**Slice 4** builds: `--lang go|ts|py|all` on `ubx blueprint build` — the
SAME single AI draft (drafted exactly once, regardless of how many
languages are requested) compiled into up to three sibling package
directories (`go/`, `ts/`, `py/`) by three independent generators
(`GenerateGo`/`GenerateTS`/`GeneratePython`) sharing one language-neutral
decode/dependency/topo-sort layer (`blueprint/decode.go`). No flag means
build all three — the resolved "--lang default" design (UBI-74's own
Linear comment thread), since the draft's own cost is paid once either
way. See "Multi-language codegen" below for the full design, including
the real per-language adaptations this needed (native default parameters
for TS/Python vs. Go's own functional-options workaround;
`ResourceBinding<any, any>` instead of a generated Config/Attrs interface
pair for TS; a mandatory dataclass Config for Python, matching Go's own
requirement for a reason TS doesn't share).

**Breaking change from Slices 1–3**: build output now ALWAYS nests under
a `<lang>/` subdirectory (even `--lang go` alone writes `go/go.mod` etc,
not a flat `go.mod` at `dir`'s own root) — sibling per-language
directories, consistent with how the per-provider SDK repos already
separate by language, rather than the flat, single-language layout
Slices 1–3 established. `ubx blueprint package`/`pull`/`verify` needed NO
code changes for this — `filepath.WalkDir`-based file discovery already
treated a directory tree generically, nested or not.

**Slice 5** builds: cross-medium calling — a diagram's own
`ubx_blueprint`-classed node (`diagram/parse.go`, zero AI, pure structural
attribute reading, reusing UBI-91's own `ubx_required` mechanism) and an
md draft's own "Use blueprint X with..." recognition (`intentprovider`'s
own system prompt + wire schema, a thin AI mapping step that never
re-drafts the blueprint's own resources) both compile down to the SAME
new `resolver.IntentFile.BlueprintCalls` wire field, expanded by
`blueprint.ExpandCalls` — spliced into `ubx resolve` right before
`resolver.Resolve` — into real resources by literally RUNNING the target
blueprint's own already-compiled function through the identical
goeval/tseval/pyeval machinery `ubx resolve --from-code` already runs for
a hand-written SDK program (UBI-74 Slice 2's own real invocation
mechanism, never a second, parallel one). See "Cross-medium calling"
below for the full design.

**Slice 6** builds: provenance tagging + `why`/`render` integration —
every resource a blueprint call produces (`blueprint.ExpandCalls`, any
medium, any language) is stamped with a `{"kind": "blueprint", "ref":
"<name>:<content_hash>"}` entry in a new per-RESOURCE
`resolver.ResourceIntent.Sources` field (reusing `core.IntentSource`'s
own existing multi-kind shape verbatim, a new `"blueprint"` kind value,
never a new field shape), threaded through `resolveOnce` into each
create node's own `"sources"` key; `ubx why <address>` renders the full
chain, honestly distinguishing the (not-yet-implemented) blueprint
author's own signing from the real calling-stack acceptance; `ubx
render` groups a blueprint call's own resources inside one dashed-border
D2 container, labeled with the blueprint's own ref. See "Provenance:
Slice 6" below for the full design.

**Slice 7** builds: `ubx blueprint push <tarball> --to oci://<registry>/
<repo>:<tag>` (uploads Slice 3's own tarball, unmodified, as a real OCI
artifact via ORAS — `oras.land/oras-go/v2`, one manifest wrapping the
tarball as its one content-addressed blob layer) and extends `ubx
blueprint pull` to also resolve `oci://` references (a third source type
alongside Slice 3's local-path and git). Authenticates using the SAME
credentials a real `docker login`/`oras login` against that registry
already established — read from the real Docker credential store, never
a second, ubx-specific login. See "OCI push/pull: Slice 7" below for the
full design.

**Slice 8** builds: `ubx blueprint pull <path-to-tarball>` — a fourth
`Pull` source type, a bare tarball FILE (not a directory, not a URL),
extracted directly with zero network involved at all — the standalone
offline/email/support-ticket delivery mode; and real redistribution, at
least the re-tag/mirror-unchanged case: the SAME tarball pushed to a
SECOND, independent OCI location, `ubx blueprint verify` on the mirrored
copy still confirming the ORIGINAL author's own content hash, unchanged.
Fork-with-modification (a genuine derivative, its own provenance showing
fork lineage) is designed but not built this slice — see "Offline
delivery + redistribution: Slice 8" below for the full design, including
that design.

Not built here, named so it isn't assumed covered: a stack's own `uses:`
key naming a blueprint by ref (UBI-121); a real Strata registry SERVICE
(this arc uses existing OCI registries directly, never a bespoke
ubx-hosted one); fork-with-modification's own real implementation (design
recorded, not built — "Offline delivery + redistribution: Slice 8"
below); alias/pointer redistribution (the design record's own third
pattern — a thin manifest referencing the original with no content
copy — named but not built, since the re-tag/mirror case already proves
the load-bearing "trust survives redistribution" property this slice's
own success bar requires); the bound policy engine (UBI-118, split off
UBI-74 entirely); the override mechanism and `render --sync-overrides`
(UBI-86, a separate ticket); a blueprint call participating in a diagram
edge (Slice 5's own diagram parsing treats one as topologically inert,
the same way an unresolved node already is — its own resource addresses
aren't known until expansion, well after the diagram medium's own
edge-translation pass runs); nested blueprint calls (a blueprint calling
another blueprint) — provenance stamping only ever records the ONE
blueprint that directly produced a resource, never a chain, since nesting
itself (UBI-121) isn't built; recording a blueprint's own build-time
package/function name in `blueprint.lock.json` (named as a Slice-6
candidate in "Cross-medium calling" above, for a DIFFERENT limitation —
deriving `blueprintNameFromCall`'s own function identifier — never
attempted across Slices 6-8, since it's a separate concern from
provenance/why/render or distribution); list-typed params/iteration
(recorded on UBI-74, separately scoped future work); a Terraform
converter (UBI-125).

## The Ubxfile format

**Filename: `Ubxfile`.** No prefix/suffix, capitalized, matching Docker's
own `Dockerfile` convention — found automatically by `ubx blueprint build
.` in the given directory, never searched for recursively or guessed from
a differently-named file. Named generically ("Ubxfile," not
"Blueprintfile") deliberately, per UBI-74's own resolved-format comment,
leaving room if this format ever covers more than blueprints later (that
comment thread's own "a stack calling a blueprint" shape — `uses:` with
no `lang:`/`params:` — is exactly that future use, out of scope here).

**Grammar: YAML, strict.** Not HCL, not a bespoke Dockerfile-instruction
language — both explicitly considered and rejected in UBI-74's own
comment thread in favor of "plain key structure, no new syntax to learn."
Parsed via `gopkg.in/yaml.v3` with `KnownFields(true)` (the same strict-
decode discipline `cli/configyaml.go` already established for `.ubx/
config`'s own YAML cascade layer, applied independently here rather than
reused directly — Ubxfile has no cascade/merge semantics at all, so
reusing `genericTree`'s merge-oriented machinery across that internal
package boundary would import complexity this format doesn't need). Any
key besides `lang`, `params`, `resources` is a hard parse error naming
the unrecognized key — this is deliberate, not an oversight: it's what
makes `uses:` (UBI-121, explicitly out of scope this slice) a loud,
immediate rejection instead of a silently-ignored key once that key
exists in a real Ubxfile someone hand-writes early.

```
lang: go

params:
  repo_name: string, required
  queue_name: string, required
  retention_days: number, default 1

resources: ./platform.md
```

**`lang:`** — a single scalar value, one of `go`/`ts`/`py`/`all`; any
other value is a hard error (never a silent no-op). Validated here, but
**not currently consulted by `ubx blueprint build`'s own language
selection** — that's governed entirely by the CLI's own `--lang` flag
(default "all" when omitted, per UBI-74's own resolved "--lang default"
design), independent of whatever this field declares. A real, named open
point: a future session may wire `lang:` in as the flag's own default
when `--lang` is omitted (letting an Ubxfile pin its own default target
language(s) without requiring `--lang` on every invocation) — deliberately
not done this slice, since the ticket's own resolved design named the
flag's own default ("all") directly, not a fallback chain through this
field.

**`params:`** — a mapping, parsed via `yaml.Node` (never decoded straight
into a Go `map[string]string`) specifically to preserve declaration
order: a Go map has no iteration order, and this project treats
determinism as a feature (CLAUDE.md) — the generated function's own
parameter order must be the Ubxfile author's own written order, every
time, not whatever a map happens to yield. Each value is `<type>,
required` or `<type>, default <value>`. Recognized types: `string`,
`number`, `bool`. **`number` always compiles to Go `int`** — every real
example in UBI-74's own design record (`retention_days`) is an integer
count, and Slice 1 deliberately doesn't invent float support nobody has
asked for yet (YAGNI, extend when a real blueprint needs a fractional
parameter). `bool` isn't in any example in the design record either but
costs nothing to support now (`true`/`false` default). Anything else
(`list`, `map`, a typo) is a hard parse error, never a best-effort guess.

**`resources:`** — either a path to an existing `.md` file (resolved
relative to the Ubxfile's own directory) or inline prose written directly
in the Ubxfile. Disambiguated the same way a human would read it, not by
a sigil: a single-line value ending in `.md` that actually resolves to a
real file on disk is treated as a path and that file's content is read
in its place; anything else (multi-line, or a `.md`-looking path that
doesn't exist, or prose with no `.md` suffix at all) is treated as
literal inline prose, verbatim. A YAML block scalar (`resources: |`)
works for multi-line inline prose the same way it would for any other
YAML value.

## The build pipeline: UBI-41's own machinery, exactly once

`ubx blueprint build .` resolves `resources:` through the identical
`intentprovider.DraftWithRetry` call `cli/propose.go`'s `draftFromDoc`
already makes (zero-config Claude adapter/model default per UBI-87,
`.ubx/config`'s own `[intent]` table honored if present, though a bare
blueprint directory with no config at all works too — the whole point of
a zero-config default). No ledger, no `resolver.Resolve` call: a
blueprint isn't bound to any stack's own reality yet (UBI-74's own design:
"re-resolves against the calling stack's own reality" happens at CALL
time, Slice 2+, never at build time) — `ubx blueprint build` stops at the
raw `*resolver.IntentFile` draft, the same stopping point `ubx plan
--from-doc` passes through on its way to a real `resolver.Resolve`, just
never taking that next step here.

**One real, deliberate addition to the content handed to the intent
provider, not a new pipeline:** the raw prose (from `resources:`,
whichever source it came from) is wrapped with a short preamble before
being passed to `DraftWithRetry`, naming every declared `params:` entry
and instructing the model to preserve each `{param_name}` token appearing
in the prose **literally, unresolved**, in its own resolved config output
— never to invent a concrete example value for it. This is what makes
"build once (AI), call many (no AI)" actually parameterized rather than
freezing whatever sample value the one build-time draft happened to pick:
the resources.md/inline prose is written using ordinary
`{param_name}`-style placeholders (matching UBI-74's own worked example,
`"An ECR repository called \"{repo_name}\""`), and the wrapped instruction
is what keeps those tokens intact through the LLM round-trip instead of
resolving them to a plausible-looking literal. This is still "the intent
provider pipeline exactly once" — the underlying `Adapter`/`DraftWithRetry`
call is completely unchanged; only the content handed to it (which
`draftFromDoc` also just passes through verbatim from its own caller) is
different.

## Codegen design: resolved intent → Go source

This is genuinely new work — confirmed by search before writing a line of
it: nothing in `sdk/codegen` already goes resolved-intent → source (that
package's `ir.FromSchema` + `sdk/codegen/templates/go` go the other
direction, provider schema → binding library, for `ubx sdk gen`). UBI-74's
own framing — "a blueprint's compiled function is structurally similar to
a generated resource binding, just parameterized" — is honored by reusing
that machinery's real DESIGN (a `ResourceBinding`/`Config` struct pair per
resource type, a `sdk.FieldMap` of Go-field-name → wire-name), not its
literal code path, for one load-bearing reason:

**No provider schema fetch, deliberately.** `gotmpl.ResourceFile` (`ubx
sdk gen`'s own per-type renderer) needs a real `*ir.ResourceType`, which
needs a real provider binary launched and its schema fetched — real
credentials/binary-acquisition machinery a bare blueprint directory (no
`[providers]` table, often no `.ubx/config` at all) has no reason to
carry. Every `Config` struct field `sdk/go/runtime` actually reads is
typed `any` regardless of the real schema's own type (confirmed by
reading `ResourceFile`'s own output before relying on this) — the ONLY
thing a real schema fetch would add for Slice 1's purposes is the exact
set of real wire field NAMES, and those are already present, directly, as
the resolved `ResourceIntent.Config`'s own JSON object keys (the intent
provider is trained on real wire-attribute-keyed conformance fixtures —
`intentprovider/conformance/fixtures/platform-iam-attach.md` and
siblings — so its draft output already uses real provider wire names).
So Slice 1's own binding/config-struct generation derives its `FieldMap`
straight from each resource's own resolved `Config` keys, sorted for
determinism, never from a live schema call. This is a real, load-bearing
scope decision, not a corner cut silently: **a field the AI never
mentioned in this one blueprint's own resources prose will never appear
in the generated binding**, unlike `ubx sdk gen`'s bindings, which cover
every settable field a real schema has whether or not any example ever
uses it. Acceptable for Slice 1's own success bar (compiles, typed
params) and named here so nobody assumes blueprint bindings are as
complete as `ubx sdk gen` bindings without checking.

**Per resource:** a `<Name>Config` struct (fields = Go-PascalCase of each
top-level `Config` key, all typed `any`, matching `gotmpl.ResourceFile`'s
own convention exactly) and a `var <Name> = sdk.ResourceBinding{WireType:
"<type>", Fields: sdk.FieldMap{...}}`, written into the same generated
package as the blueprint's own function (a flat, single-package layout —
UBI-98's own per-service-directory split exists to survive a real
provider's hundreds of types in one repo; a single blueprint's own
handful of resource types never approaches that scale, so the added
directory structure buys nothing here and was deliberately not carried
over). `<Name>` is derived from the resource's own intent `Name` (e.g.
`ci-artifacts` → `CiArtifacts`), independent per-resource identifier
collisions are a hard build error (never silently overwritten) — real,
but judged unlikely enough within one blueprint's own flat resource list
to not need an automatic disambiguation scheme this slice, matching
`gotmpl`'s own "detect and report, don't invent a rename scheme
speculatively" posture for the sibling-`*Config` collision it does handle.

**Value translation, per `Config` field, recursively (not just top-level):**
- A `{"$ref": {"to": "<stack>.<type>.<name>[.<path>...]"}}` marker
  (`core/resolver/refs.go`'s own `$ref` shape, read directly — not
  resolved, since resolution is a ledger-aware step this command never
  runs) becomes a Go expression referencing the earlier resource's own
  `*Computed` return value, drilled via `.Field(...)` once per path
  segment beyond `<type>.<name>` — e.g. `ciArtifacts.Field("arn")`. This
  is the one place resource creation ORDER matters: Go requires the
  referenced `sdk.Resource()` call's return value to already be a
  variable, so generated calls are topologically sorted (implicit edges
  from every `$ref` found, unioned with any explicit
  `ResourceIntent.DependsOn` the draft happened to carry — mirroring
  `core/resolver/refs.go`'s own `unionDependsOn`, independently
  reimplemented here since blueprint codegen has no reason to depend on
  `core/resolver`'s resolve-time internals for a build-time-only
  concern); a genuine cycle is a hard build error, never silently broken.
- A bare string value that is *exactly* one `{param_name}` token (after
  trimming) naming a declared `params:` entry becomes a direct reference
  to that parameter's own Go variable (unquoted — the field is typed
  `any`, so the parameter's own real Go type, `string`/`int`/`bool`,
  passes straight through). A string value that CONTAINS one or more
  `{param_name}` tokens mixed with other literal text becomes a
  `fmt.Sprintf` call, substituting `%v` per token in order — chosen
  deliberately over forcing every mixed-placeholder attribute into a
  parameter of its own, since real prose (`"An ECR repository called
  \"{repo_name}\" for the {team} team"` or similar) can legitimately
  interpolate a parameter into a longer literal string.
- A string value that itself decodes as JSON carrying a `$ref` marker one
  level down (a JSON-embedded ref, typically an IAM policy document's own
  "Resource" field — see "Adversarial cases" below) is split into literal
  text segments plus the referenced resource's own runtime `Computed.
  Address()` calls, `+`-concatenated (`renderEmbeddedRefString`, Slice
  2) — never rendered as one opaque literal; see "Adversarial cases"
  below for why that distinction is load-bearing, not cosmetic.
- Anything else (a plain literal, or an object/array with no `$ref`/
  `{param}` anywhere inside it) is rendered as a literal Go value —
  `map[string]any{...}`/`[]any{...}`/scalar, recursively, mechanically
  from the decoded JSON — never hand-massaged per type.

**`go.mod`:** written with `module <sanitized-directory-name>` and
`require github.com/ubiquex/ubx-sdk-go v0.0.0` — the identical placeholder
convention `sdk/codegen/templates/go/go.go`'s own `GeneratedRepo` already
uses for exactly the same reason (a real consumer pins a real published
version; this command has no business guessing one). This project's own
existing Go codegen tests (`sdk/codegen/templates/go/collision_test.go`)
already establish the pattern for verifying such a placeholder-versioned
module actually compiles, hermetically, with no network: append a
`replace github.com/ubiquex/ubx-sdk-go => <local sdk/go path>` and build
with `GOPROXY=off` — Slice 1's own hermetic build test and live
verification both reuse that exact mechanism rather than inventing a
second one.

## Adversarial cases considered

- Unknown Ubxfile key (e.g. a hand-written `uses:` before UBI-121 ships)
  — hard parse error naming the key, never silently ignored.
- `lang:` naming an unsupported language — hard error, not a silent no-op.
- A `params:` entry with neither `required` nor `default <value>`, or
  with both, or naming an unrecognized type — hard parse error.
- `resources:` naming a `.md` path that doesn't exist — treated as inline
  prose (per the disambiguation rule above), which will read strangely to
  the intent provider but is never a silent file-not-found; if this proves
  confusing in practice, a stricter opt-in path marker is future work, not
  designed here.
- Two resources whose intent `Name` normalizes to the same Go identifier
  — hard build error, never a silent overwrite.
- A dependency cycle between resources (via `$ref` or `DependsOn`) — hard
  build error during topological sort, never a silently-arbitrary order.
- A `{param_name}` token in prose naming a parameter NOT declared in
  `params:` — the intent provider has no way to know this is meant as a
  placeholder at all (nothing in the wrapped instruction names an
  undeclared token), so it is very likely resolved to a literal
  concrete-looking string instead of preserved — codegen has no way to
  distinguish that from a real literal after the fact. Not solved this
  slice; a future slice could scan the RAW prose for `{...}` tokens before
  drafting and hard-fail if one doesn't match a declared param, catching
  the mismatch before spending an AI call on it — named here as a real
  gap, not silently assumed safe.
- A resource `Config` value that is a JSON-embedded STRING containing its
  own escaped JSON with a `$ref` inside (`core/resolver/refs.go`'s own
  "JSON-embedded refs" amendment, relevant to IAM policy documents
  specifically) — checked directly, not left speculative: `intentprovider/
  validate.go`'s own `wireResourceIntent.Config` is `string` at the wire
  level (what an adapter must literally emit — `"config":
  "{\"instance_class\":\"db.t3.small\"}"`, one level of JSON-string
  escaping around the whole config object), but `parseAndValidate`
  unwraps that string into a real `json.RawMessage` object BEFORE
  `DraftWithRetry` ever returns — confirmed by reading `parseAndValidate`
  and by a real hermetic test failure caught live (`cli/blueprint_test.go`
  originally handed its fake adapter a raw JSON object for `config`
  instead of a JSON-encoded string, and validation correctly rejected it:
  "cannot unmarshal object into Go struct field wireResourceIntent.
  resources.config of type string" — fixed by matching
  `propose_from_doc_test.go`'s own fixture convention). So by the time
  `GenerateGo` ever sees a `resolver.IntentFile`, every resource's own
  `Config` is already a genuine JSON object one level deep — Slice 1's
  value-translation walk (recursing through real JSON structure, never
  into a string value's own embedded-JSON content) is exactly the right
  depth for this pipeline's own real shape, not a lucky guess.

  A DIFFERENT case genuinely does reach `GenerateGo` as a plain string
  with a `$ref` embedded inside its own escaping: an attribute whose OWN
  real wire type is itself a JSON-encoded string at the PROVIDER level
  (`aws_iam_policy.policy`) — this slice's own live CI-platform
  verification hit it directly, not hypothetically (see the
  implementation-slices log below). Checked against `core/resolver/
  refs.go`'s own `containsMarker` doc comment before concluding
  anything: the resolver's own reference walk and the executor's own
  `substituteComputed` already know how to find and substitute one level
  into a string attribute's own decoded JSON, at call/resolve time — that
  part was right.

  **What Slice 1 got wrong, corrected in Slice 2 once actually called
  live**: Slice 1 concluded `renderString` should render this string
  VERBATIM as a quoted Go literal, reasoning that reproducing the
  embedded `$ref` text unchanged was "exactly correct." That conclusion
  was only ever checked against `TestGenerateGo_CompilesClean` — a
  COMPILE check, never a real call — and it's wrong: the embedded
  address's own stack-name prefix (`ci-platform`, the Ubxfile
  directory's own build-time name) is baked into that literal forever,
  but a real calling stack is essentially never also named `ci-platform`
  — Slice 2's own real-AWS verification hit this directly, calling from
  a stack named `payments` and getting "reference does not resolve to
  any known resource or attribute: ci-platform.aws_ecr_repository....".
  Fixed by `renderEmbeddedRefString` (`gogen.go`): a JSON-embedded `$ref`
  is no longer rendered as one opaque literal — it's split into literal
  text segments plus the referenced resource's own real, RUNTIME
  `Computed.Address()` calls, `+`-concatenated, so the address reflects
  whatever stack the function is ACTUALLY called from, exactly like
  every non-string-embedded `$ref` already did via `renderRef`. See
  "Resolved defaults: functional options" and "Calling convention"
  below, and `TestGenerateGo_EmbeddedRefUsesRuntimeStackName`, for the
  full account and the regression test.

- **`params:` `default` values are now genuinely load-bearing at call
  time (Slice 2), closing Slice 1's own named open point.** See "Resolved
  defaults: functional options" below for the design and why.

## Resolved defaults: functional options (Slice 2)

Slice 1 shipped with `params:` `default` values parsed and validated but
compiled to an equally-required positional Go argument — Go has no native
default-argument syntax, so `retention_days: number, default 1` produced
`retentionDays int`, indistinguishable at the call site from a `required`
param. Closing that gap needed a real convention for "what does a caller
not passing a value for a `default` param actually get" — decided here,
not invented from scratch: **checked against how the rest of this
codebase already handles an optional/defaulted call-time value before
choosing anything new**, per this session's own instruction. A search
turned up exactly one established precedent, `provider/acquire.go`'s own
`AcquireOption`/`acquireConfig`/`With*` triple (`WithHTTPClient`,
`WithRegistryAPIBase`, `WithCacheRoot`, `WithPlatform`, all folding into
`Acquire(ctx, src, version, opts ...AcquireOption)`) — Go's own standard
functional-options idiom, already real and shipped in this repo, not a
convention this session introduced. `sdk/go/runtime`'s own Config structs
(`gogen.go`'s `<Name>Config` output) are a different, non-competing
pattern — every field typed `any`, an unset field simply omitted from the
wire config (`serializeConfig`'s "not set — omitted" branch) — but that
shape exists to describe a resource's own WIRE attributes, addressed by
provider field name, not a Go FUNCTION's own call-time argument list;
it has no notion of a parameter's own declared default value at all, so
it doesn't answer this question on its own.

**The design, mirroring `AcquireOption` exactly:**

- **Required params stay direct, positional Go arguments** — unchanged
  from Slice 1, in `params:`'s own declared order.
- **Every `default` param moves onto a generated, unexported `options`
  struct**, seeded with the Ubxfile's own declared default value (a real
  Go literal — string/int/bool, per the param's `Type`), and the
  generated function gains a trailing `opts ...Option` parameter.
- **One `With<PascalParam>(v <type>) Option` function per `default`
  param** — e.g. `retention_days` produces `WithRetentionDays(v int)
  Option` — each a closure setting exactly its own field on the
  `options` struct, the same shape every `With*` in `acquire.go` already
  has.
- The function body seeds `cfg := options{retentionDays: 1, ...}`, then
  `for _, opt := range opts { opt(&cfg) }` before any `sdk.Resource()`
  call — identical to `Acquire`'s own `cfg := acquireConfig{...}` +
  options-apply-loop shape.
- Any reference to a `default` param inside the resources prose (a
  `{param_name}` token, resolved the same way Slice 1 already resolves
  one) now renders as `cfg.<field>` instead of a bare argument identifier
  — `paramRef` branches on `Param.Required` to choose between the two.

```go
type Option func(*options)

type options struct {
	retentionDays int
}

// WithRetentionDays overrides the "retention_days" params: default.
func WithRetentionDays(v int) Option {
	return func(o *options) { o.retentionDays = v }
}

func CiPlatform(repoName string, queueName string, opts ...Option) {
	cfg := options{
		retentionDays: 1,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	// ... sdk.Resource() calls, referencing cfg.retentionDays wherever
	// {retention_days} appeared in the resources prose ...
}
```

A caller wanting the default writes `CiPlatform("payments-ci-artifacts",
"payments-notifications")`; a caller overriding it writes
`CiPlatform("payments-ci-artifacts", "payments-notifications",
WithRetentionDays(30))`. A blueprint with zero `default` params (all
`required`) gets no `Option`/`options`/`With*` machinery generated at
all — the function signature is exactly Slice 1's own shape, unchanged;
this is a pure additive convention, not a breaking one.

**Identifier collisions, guarded, not assumed impossible.** Introducing
`Option`/`options`/`With<Param>`/`cfg`/`opts` as new synthetic
identifiers creates a real, if narrow, new collision surface once a
blueprint author can freely choose resource and param names — a resource
literally named `option` would otherwise collide with the generated
`type Option`, or a param literally named `opts` with the generated
function's own variadic parameter. `checkOptionIdentCollisions`
(`gogen.go`) checks every synthetic identifier this pattern introduces
against every identifier already derived from resource/param names before
emitting anything, and fails loudly, naming the exact collision, if any
overlap — matching this codebase's own "hard build error, never silently
overwritten" posture for identifier collisions (the same posture
`gotmpl`'s own duplicate-`*Config`-name detection already established),
rather than inventing a rename scheme. Covered by
`TestGenerateGo_DefaultParamOptionIdentCollision`.

**Live-verified, not just compile-checked.**
`TestGenerateGo_DefaultParamOptionsPattern` runs the generated function
for real (`go run`, not `go build`) twice — once with no `opts` (asserts
the wire config carries the declared default, `1`) and once with
`WithRetentionDays(30)` (asserts the override reaches the wire config
instead) — decoding the emitted `intent/v1` document's own JSON to check
the actual value, not a string match against the generated source. This
is what makes the claim "defaults are load-bearing" a runtime fact, not
just a codegen-shape fact.

## Calling convention (Slice 2)

Slice 1 stopped at a compiled, but never-run, Go package. Slice 2's own
job was proving that package is genuinely usable — and the answer,
confirmed live, is that it needs **zero new mechanism**: a blueprint's
own compiled output is an ordinary Go package exporting an ordinary Go
function: nothing about it is special to `ubx` at the calling end. A real
stack calls it exactly the way it would call any other Go library.

**The calling stack is a plain SDK program** — `sdk.Stack`/`sdk.Main`,
the same shape every other Go SDK program in this project already uses
(`goeval/testdata/happy/main.go`, `cli/testdata/sdk_resolve_go/
create_widget.go`) — with one addition: it imports the blueprint's own
built package and calls its function from inside the `Stack` closure,
exactly like calling any other helper that happens to make `sdk.Resource`
calls of its own:

```go
package main

import (
	ciplatform "github.com/acme/ci-platform" // the blueprint's own built package
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "payments' own CI platform, via blueprint"})
		ciplatform.CiPlatform("payments-ci-artifacts", "payments-notifications",
			ciplatform.WithRetentionDays(30))
	}))
}
```

**Local addressing is an ordinary Go module `replace` directive** — per
UBI-74's own "three source types" comment (local/git/Strata, addressing
not decided for the other two until their own slice), Slice 2 only proves
the LOCAL case: the calling stack's own `go.mod` `require`s the
blueprint's own module path and `replace`s it to a local filesystem path,
precisely the same mechanism `sdk/codegen/templates/go/collision_test.go`
and `blueprint/gogen_test.go`'s own `TestGenerateGo_CompilesClean` already
use to build hermetically against this repo's own `sdk/go` — no new
addressing syntax, no new resolution code, ordinary `go.mod`:

```
module payments-stack

go 1.23

require github.com/ubiquex/ubx-sdk-go v0.0.0
require github.com/acme/ci-platform v0.0.0

replace github.com/ubiquex/ubx-sdk-go => ../path/to/ubx-sdk-go
replace github.com/acme/ci-platform => ../path/to/ci-platform
```

**Resolution needs zero new resolver machinery — the ticket's own core
claim, confirmed live.** `ubx resolve --from-code <entry>.go` (UBI-27/
UBI-35) already compiles and runs ANY Go SDK program under `goeval` and
hands its emitted `intent/v1` document to the exact same resolver every
other medium uses; a blueprint call is invisible to that pipeline —
by the time `sdk.Main` writes the document to stdout, the blueprint's
own `sdk.Resource()` calls have already expanded into ordinary resources
with ordinary `$ref`-marker dependency edges, indistinguishable from
resources a human hand-wrote directly in the same `Stack` closure.
`TestBlueprintCall_ResolveAcceptShip_RealFakeProvider` (`cli/
blueprint_call_test.go`) is this claim made concrete and hermetic: a real
`blueprint.GenerateGo`-built package, imported by a real calling stack,
resolved via the unmodified `ubx resolve --from-code` CLI path, accepted,
and shipped end to end against `fakeprovider` — zero errors, the
blueprint's two resources (one cross-referencing the other via a real
`$ref`, proving intra-blueprint dependency edges survive the round trip)
shipping exactly as a hand-authored equivalent already does
(`ship_change_test.go`'s own primary/mirror shape).

**Real-AWS verification.** The identical CI-platform blueprint from
Slice 1's own live verification (ECR + SQS + IAM role + policy +
attachment, drafted against the real Claude API, no fake adapter) was
called from a real stack and resolved against the real `hashicorp/aws`
provider's own real schema (`ubx resolve --from-code`, a safe schema-
fetch-only operation per this project's own standing ship-verification
doctrine) and accepted into a real ledger, ready to ship — see the
Implementation slices log below for what was and wasn't run live this
session, and why.

## Package/pull/verify: distribution (Slice 3)

Three commands, three independent primitives — `package` produces a
verifiable artifact, `pull` gets a blueprint's own files onto disk from
one of two sources, `verify` confirms a directory's own files still match
what `package` originally declared. None of the three depends on any of
the others being used first except through `blueprint.lock.json` itself
(below) — a directory `pull` produced can be `verify`d without ever
having gone through `package` locally, since the manifest travels WITH
the directory through git exactly as it does through a tarball.

**`blueprint.lock.json` — the manifest, and why it's a plain file in the
blueprint's own directory, not a side channel.** `ubx blueprint package
<dir>` walks every real file in `dir` (skipping dot-prefixed entries —
`.git`, mainly — and its own filename), hashes each one
(`"sha256:<hex>"`, docs/schema.md's own established content-hash format),
and canonicalizes `{schema_version, name, files}` through
`core.CanonicalJSON` — core/canonical.go's own JCS-style
canonicalization, the SAME approach `core.Hash` already uses for a
Proposal's own hash (RFC 8785/JCS-style sorted-key JSON, no HTML
escaping, no trailing newline) — before hashing the canonical bytes into
one overall `content_hash`. This is deliberately NOT a hash of the
tarball's own raw bytes: a tarball's header metadata (mtimes, uid/gid,
entry order) isn't real package content, and CLAUDE.md's own
"determinism is a feature" rule means a content hash should track
content, not incidental packaging noise. The manifest — files, per-file
hashes, and the overall `content_hash` together — is written into
`dir/blueprint.lock.json` as an ordinary file, which is what makes it
travel through ANY distribution mechanism for free: a local copy, a git
commit, or the tarball `package` also produces all just carry it along as
one more file in the tree, with no distribution-mechanism-specific
awareness of "there's a manifest, remember to carry it separately."

`Verify(dir)` recomputes the SAME canonicalization over `dir`'s CURRENT
files and compares against `blueprint.lock.json`'s own declared
`content_hash` — the same tamper-evidence principle `core.Hash`/
`core.Validate` already give a ledger entry, applied here to a pulled
blueprint's own files instead. One deliberate design point: the
recomputation uses the manifest's own declared `name`, never
`filepath.Base(dir)` — a pulled or renamed directory very plausibly isn't
named identically to the blueprint's own build-time name (a git clone
lands wherever `pull`'s own `dest` argument says), and the content hash
needs to stay invariant under "which directory did you put this in,"
exactly the same portability lesson Slice 2's own embedded-`$ref`
stack-name fix already established for a different field. A mismatch
names exactly which files differ (added, removed, or changed) rather than
just "hashes differ somewhere."

**`package`'s own tarball.** `writeTarGz` archives `dir`'s own file set —
sorted for deterministic entry order, every header's `ModTime`/`Uid`/`Gid`
zeroed — so re-`package`ing unchanged content reproduces a byte-identical
tarball, not just an identical `content_hash`. The tarball is a
standalone, self-describing artifact (it carries `blueprint.lock.json`
inside it) primarily for Slice 7 (OCI push — see "OCI push/pull: Slice
7" below, which pushes this exact tarball unmodified as an OCI artifact's
one blob layer) and Slice 8 (offline/email delivery — see "Offline
delivery + redistribution: Slice 8" below, which pulls this exact
tarball back in directly, as a bare file) to consume — Slice 3 itself
never reads a tarball back in (see the Scope section above); the
git-distribution path pulls a blueprint's own UNPACKED directory tree
directly, matching the design record's own "tar/zip is a fourth DELIVERY
mode, not a fourth SOURCE type" framing.

**`pull`'s source types, as Slice 3 originally scoped them (a third, OCI,
was added in Slice 7; a fourth, a bare tarball file, in Slice 8 — see
their own sections below).** A `source` that already exists on local disk
as a directory is resolved as a local path — copied into `dest` verbatim
(dot-prefixed entries excluded, same convention `hashFiles` uses),
`--ref`/`--path` unused. Anything else is treated as a git repository:
cloned into a scratch temp
directory (shelling out to the real `git` binary, matching `github/
git.go`'s own "no pure-Go git reimplementation needed" precedent — every
real environment this runs in already has a working git install),
checked out at `--ref` (branch/tag/commit; empty means whatever `git
clone` itself checks out by default) if given, then the directory at
`--path` within it (default `"."`, for a repo whose root IS the
blueprint) is copied into `dest`. Either way `dest` ends up holding an
ordinary local blueprint directory, indistinguishable afterward from one
authored locally to begin with. `dest` must not already exist, or must be
empty — `pull` never overwrites existing content.

**Live verification, the ticket's own required bar, genuinely met.**
Slice 1/2's own already-built, already-live-AWS-verified CI-platform
package (`~/ubx-playground-ubi74-slice2/ci-platform/`) was copied into a
fresh scratch directory and packaged (`ubx blueprint package` — 5 files,
a real `content_hash`). That directory (now including
`blueprint.lock.json`) was committed and pushed to a REAL, newly created
GitHub repository (`github.com/Ubiquex/ubx-sdk-blueprints`, `ci-platform/`
subdirectory, real commit history — `gh repo create` + a plain `git
push`, matching how every other real SDK repo in this project got its
own history). `ubx blueprint pull
https://github.com/Ubiquex/ubx-sdk-blueprints.git <dest> --ref main
--path ci-platform` — a completely separate local directory, an ordinary
HTTPS clone, no local filesystem shortcut — reproduced the identical file
set; `ubx blueprint verify <dest>` recomputed the SAME content hash the
original `package` step reported (`sha256:6a120fff...`) — byte-for-byte
match, confirmed by comparing the two command outputs directly, not
assumed. `go build ./...` and `go vet ./...` against the pulled copy, with
NO local `replace` directive (the real, already-published
`github.com/ubiquex/ubx-sdk-go` module, real network, real module proxy)
both succeeded cleanly — the pulled package is genuinely usable, not just
byte-identical. Tamper-detection was also checked live, not just in the
hermetic suite: editing one file in a throwaway copy of the pulled
directory and re-running `verify` produced a clear mismatch naming that
exact file (`~ ciplatform.go (content changed)`) and exited non-zero.

## Multi-language codegen (Slice 4)

**One draft, three generators, one shared decode layer.** `ubx blueprint
build`'s own AI draft (`draftBlueprint`, unchanged) runs EXACTLY ONCE
regardless of `--lang`'s own value — `--lang all` (or no flag at all,
its own default) compiles that SAME `*resolver.IntentFile` through
`GenerateGo`, `GenerateTS`, and `GeneratePython` independently, one call
each, in `cli/blueprint.go`'s own `blueprintGenerators` map. `--lang go`/
`ts`/`py` narrows to exactly one. Every generator returns a flat
`filename -> content` map prefixed with its own language directory
(`"go/..."`/`"ts/..."`/`"py/..."`); `newBlueprintBuildCmd` merges all
three maps before writing (`os.MkdirAll` per file's own directory, since
Slice 1-3's flat layout never needed one).

**Confirmed, not assumed: `sdk/codegen`'s own IR/template machinery does
NOT drop in cleanly here — a real, deliberate non-reuse, checked before
building anything new.** `sdk/codegen/ir` translates a real PROVIDER
SCHEMA (every possible field of a resource TYPE, Required/Optional/
Computed flags and all) into a language-neutral type model that
`sdk/codegen/templates/go|ts|py` each turn into a GENERIC, reusable
binding library (`ResourceBinding`/`Config`/`Attrs` per type, meant to be
imported and called with ANY caller-supplied values). Blueprint codegen
solves a genuinely different problem: it has no schema at all, only the
CONCRETE, already-resolved field/value pairs one specific drafted
resource instance happens to carry, and needs to render those exact
literal values (or `{param}` placeholders, or `$ref` markers) into
source syntax — the reverse direction (resolved intent → source, not
schema → binding library) Slice 1's own docs already named as genuinely
new work, confirmed again here for TS/Python rather than assumed to
follow from the Go precedent. What DID carry over, and how:
- **Identifier casing conventions, reused directly where they're
  byte-identical, reimplemented separately where they're not** (`blueprint/
  identifier.go`): TypeScript's own PascalCase/camelCase convention is
  identical to Go's, so `tsgen.go` reuses `pascalCase`/`camelCase`/
  `lowerFirst` directly, no TS-specific variant needed at all. Python's
  own snake_case convention is genuinely different (`pythonIdentifier`,
  new) — mirroring `sdk/codegen/templates/py`'s own established
  `pythonIdentifier`/`pythonKeywords` precedent (independently
  reimplemented, never imported across the package boundary — matching
  how `sdk/codegen/templates/go` and `.../ts` and `.../py` don't share
  their own copies with EACH OTHER either).
- **The manifest/topo-sort/dependency-collection layer, factored out
  once** (`blueprint/decode.go`, new): `decodeBlueprint` validates every
  resource is `op: create`, decodes each resource's own Config into
  sorted `(wireKey, value)` pairs, walks every value (including one level
  down inside a JSON-embedded-ref string) for `$ref`/`depends_on`
  addresses, and topologically sorts — ALL genuinely language-neutral,
  now shared by all three generators instead of tripled. Per-language
  identifiers are deliberately NOT decided here — each generator wraps a
  `decodedResource` with its own derived ident and does its own
  collision check, since two resource names could collide under one
  language's own casing convention without colliding under another's.
- **`{param_name}`/`{param_name <op> <literal>}` placeholder grammar,
  shared** (`decode.go`'s own `placeholderToken`/`placeholderWholeString`/
  `embeddedRefPattern`) — the TEXT a caller writes is language-neutral;
  only how a match gets RENDERED differs (Go: `fmt.Sprintf`; TS: a
  template literal; Python: an f-string — each language's own native
  string-interpolation syntax, needing no shared helper function at all).
- **Number/string literal rendering, shared where the TEXT is
  byte-identical across all three target languages** (`numberLiteral`,
  `jsonStringLiteral` in `decode.go`) — a plain integer/float literal and
  JSON-style double-quoted string escaping are both valid syntax in
  Go/TS/Python as-is (confirmed, not assumed — Go's own pre-existing
  `%q`-based string quoting was deliberately left untouched rather than
  migrated onto the shared helper, since Go's own escaping rules aren't
  always byte-identical to JSON's, even though TS/Python's happen to be
  close enough to reuse directly).

**Real per-language adaptations, each a deliberate design decision, not
an oversight:**

1. **Native default parameters (TS/Python) replace Go's own functional-
   options workaround.** Go has no default-argument syntax, which is
   WHY Slice 2 built the `Option`/`With<Param>` pattern in the first
   place (`renderGoOptions`). TypeScript and Python both have real native
   default parameters (`function f(a: string, b: number = 1)` / `def
   f(a: str, b: int = 1):`) — `tsgen.go`/`pygen.go` use them directly,
   reordering `params:` into required-then-defaulted first (both
   languages reject a required parameter/argument after a defaulted
   one, so declared Ubxfile order alone can't be trusted to already
   satisfy that). No `Option`-shaped synthetic identifiers exist in
   either language's own output at all — a real simplification, not a
   missing feature.
2. **`ResourceBinding<any, any>` (TS) instead of a generated per-resource
   Config/Attrs interface pair.** `sdk/go/runtime`'s own `serializeConfig`
   uses Go reflection over a concrete, named struct type — Go's runtime
   NEEDS that struct to exist, which is why Go generates one. TypeScript's
   own runtime (`sdk/ts/runtime`) walks a plain object literal's own keys
   directly at RUNTIME, no compile-time type reification required at all
   — so `tsgen.go` skips generating a Config/Attrs interface pair
   entirely, typing every binding `ResourceBinding<any, any>`. One real,
   live-found typechecking wrinkle this surfaced (caught by a real `deno
   check` failure, not predicted): TypeScript's own `Computed<T>` is a
   conditional type that only resolves to a property-indexable shape when
   `T` is a CONCRETE object type — a genuinely naked `T = any` instead
   distributes into `ComputedMarker | {indexed...}`, a union whose
   `ComputedMarker` branch has no index signature, so ordinary property
   access (`ciRunner.arn`) fails to typecheck even though it's exactly as
   permissive at runtime as Go's own untyped `Computed.Field(string)`.
   Fixed by casting each referenced resource's own local `const` `as any`
   once, at its own declaration (`renderTSFunction`) — the direct TS
   equivalent of Go's "no compile-time attribute validation, ever"
   posture, confirmed empirically (the un-cast form's own real `deno
   check` error text is preserved in `tsgen.go`'s own comment) rather than
   assumed correct from the conditional-type theory alone.
3. **A mandatory dataclass Config (Python) — matching Go's own
   requirement, for the SAME underlying reason TS doesn't share.**
   `ubx_sdk`'s own `resource()` (`sdk/py/ubx_sdk/__init__.py`,
   `_serialize_config`) hard-requires its third argument to be a REAL
   dataclass instance (`dataclasses.is_dataclass(value)`), never a plain
   dict — so `pygen.go` generates a `@dataclasses.dataclass` Config per
   resource, matching Go's own struct requirement, NOT TypeScript's
   simpler plain-object-literal shortcut. Every field defaults to `Any =
   None` uniformly (mirroring `cli/testdata/sdk_resolve_py/bindings.py`'s
   own established fixture convention) — a blueprint's own dataclass has
   no required-vs-optional distinction the way a real schema-driven
   binding would, every field is always fully populated by the generated
   function itself, so there's no Python-side field-ordering constraint
   to work around the way TS/Go's own parameter ordering needed.
4. **A real, live-found bug, caught by a hermetic test's own literal
   string assertion, not a type checker:** Python's own LOCAL VARIABLE
   naming initially reused `lowerFirst(pascalIdent)` (Go/TS's own
   PascalCase → camelCase derivation, e.g. `"CiRunner"` → `"ciRunner"`) —
   WRONG for Python, whose own local-variable convention is snake_case
   (`"ci_runner"`), a genuinely different derivation, not just a casing
   tweak. `TestGeneratePython_CiPlatform`'s own literal assertion on the
   exact generated variable name caught this before it shipped; fixed
   with a dedicated `pyLocalVarName` (`pythonIdentifier(dr.RI.Name)`
   applied directly, never derived from the PascalCase binding
   identifier).
5. **Ref/embedded-ref access syntax, one idiom per language, all three
   equally permissive:** Go's explicit `.Field("arn")` method chain; TS's
   Proxy-based `Computed<T>` property access (`.arn`, direct, no method
   call — `sdk/ts/runtime`'s own `makeComputed`); Python's
   `__getattr__`-based `Computed` attribute access (`.arn`, matching TS's
   own ergonomics via Python's own idiomatic mechanism, not an imitation
   of JS's Proxy trap semantics). For the JSON-embedded-ref case
   specifically, getting the referenced resource's own literal RUNTIME
   address string needs a real accessor in every language, since none of
   the three `Computed`/`Computed<T>` types expose their own address as a
   plain value otherwise: Go's `.Address()` method, TS's exported
   `addressOf()` helper (`Computed<T>` is an opaque Proxy, never a real
   string), Python's `.address` property.

**Live verification, the ticket's own required bar.** The identical
CI-platform Ubxfile (ECR + SQS + IAM role + policy + attachment,
`retention_days` default 1) built with `--lang all` against the REAL
Claude API (no fake adapter) — 5 resources, one draft, three sibling
package directories. `go build`/`go vet` (real network, real published
`github.com/ubiquex/ubx-sdk-go` module), `deno check --no-remote` (a real
import map onto this repo's own `sdk/ts/runtime/src/index.ts`), and a
real Python `import` of every generated module (`PYTHONPATH` onto this
repo's own `sdk/py`) all succeeded cleanly against this SAME live-drafted
output — not three separate synthetic fixtures.

The TS-compiled function was then called from a real TS calling stack
(`stack("payments", () => { ciPlatform("payments-ci-artifacts",
"payments-notifications", 14) })` — `retention_days: 14`, UBI-123's own
corrected value, the real AWS 14-day/1,209,600-second maximum, never
`30`) and resolved via `ubx resolve --from-code` against the REAL
`hashicorp/aws@6.54.0` provider's own real schema (`--source`/
`--provider-version`, a safe schema-fetch-only operation per this
project's own standing ship-verification doctrine) — `payments: 5
create(s), 0 change(s), 0 terminate(s)`, `message_retention_seconds:
1209600` in the resolved proposal (14 × 86400, confirmed by reading the
resolved JSON directly, not assumed), and the cross-resource `$computed`
reference correctly addressing `payments.aws_iam_policy.ci-runner-access.arn`
(the REAL calling stack, "payments" — not the build-time blueprint
directory name "ci-platform") — direct, real confirmation the TS
embedded-ref/`addressOf()` design is correct against a real provider
schema, not just unit-tested. `ubx accept`ed into a real ledger
(`~/ubx-playground-ubi74-slice4/ledger/`, change id
`1f4a4f6310119ac...`) — resolved and accepted, ready for `ubx ship`.

**Why `ubx ship` itself was not run, deliberately, not an oversight** —
the same reasoning UBI-74 Slice 2's own entry already gives in full:
CLAUDE.md's own ship-verification rule is honored as absolute, and the
founder runs the real `ubx ship`/`ubx terminate` themselves, once
everything up to accept is prepared. **This session does not claim the
live-ship bar was met — only resolve/accept, against a real TS-compiled
call, deliberately.** Python's own live-AWS leg was intentionally NOT
attempted this session either — the ticket's own required bar names only
the TS-compiled path for the real-AWS leg (Go already proved it in Slice
2); Python's own UBI-123 cross-language arithmetic correctness is
verified hermetically (`TestGeneratePython_PlaceholderArithmetic`, a real
`python3` execution, not a real-AWS one) and via the same `--lang all`
live draft/compile/import leg above.

## Cross-medium calling (Slice 5)

**One shared wire field, two structural producers, one expansion
mechanism.** `resolver.IntentFile` gains `BlueprintCalls
[]BlueprintCall` (`core/resolver/resolver.go`) — purely additive and
optional, matching `DependsOn`'s own precedent exactly: every other
intent/v1 producer (a hand-written file, `ubx blueprint build`'s own
draft, an SDK program's own evaluated output) leaves it nil and is
completely unaffected. A `BlueprintCall` names a blueprint reference
(`Blueprint`/`Ref`/`Path`, mirroring `blueprint.Pull`'s own three
parameters exactly, UBI-74 Slice 3) plus `Args map[string]string` —
ALWAYS string-valued regardless of a param's own real declared type,
since neither producer (a D2 node's own raw attribute text, an LLM
extracting a value from prose) has the target blueprint's own Ubxfile in
hand at the point a call is first recorded; type coercion is deferred
entirely to expansion time, once the target blueprint's own declared
params are actually known.

**`resolver.Resolve` itself has no notion of a blueprint call at all, by
design** — `resolveOnce` hard-refuses (`ErrUnexpandedBlueprintCalls`) if
`BlueprintCalls` is still non-empty by the time it runs, rather than
silently ignoring an un-expanded entry. Expansion is `blueprint.
ExpandCalls`'s own job, spliced into `cli/resolve.go`'s `RunE` right
before `resolver.Resolve` is called — the ONE shared point every
intent/v1 document passes through regardless of which medium produced
it (a hand-written file, a diagram-produced draft via `ubx propose
--from-diagram`, or an md-drafted document via `ubx propose --from-doc`
— all three are read as a plain positional argument to `ubx resolve`
eventually). `--from-code` never needs expansion at all: an SDK
program's own direct function call to a blueprint package already
happened in-process by the time its own intent/v1 is emitted (UBI-74
Slice 2's own mechanism, unchanged) — there is nothing left to expand.
`core` stays entirely dependency-free of the `blueprint` package this
way too (the same `core`→leaf-package inversion discipline
`core.StateReader`/`EventLookup` already establish elsewhere): `blueprint`
already imports `core/resolver`, so the reverse would be a real import
cycle, not just a style preference — expansion living one layer up, in
`cli`, is the only place it can live at all.

### The diagram calling convention

A node classed `ubx_blueprint` is NEVER classified as a resource —
`diagram/parse.go` checks for this class BEFORE `resolver.InferProvider`
is ever called, so a blueprint-calling node needs no declared provider
schema at all. Its own attributes are read directly (D2's own
dotted-path shorthand — `blueprint: value` inside the node's braces
compiles to a real child object per attribute, `Label.Value` holding the
raw text — the EXACT mechanism `ubx_required` already established,
UBI-91, reused verbatim rather than reimplemented): `blueprint:`
(required — which blueprint to call), `blueprint_ref:`/`blueprint_path:`
(both optional, git-source-only, mirroring `blueprint.Pull`'s own
`--ref`/`--path`), and every OTHER attribute is a call parameter,
verbatim raw text. Zero AI anywhere in this path, per the original
design.

```d2
platform: "ci-platform call" {
  class: ubx_blueprint
  blueprint: "../ci-platform"
  repo_name: "payments-ci-artifacts"
  queue_name: "payments-notifications"
  retention_days: "14"
}
```

A relative (non-URL, non-absolute) `blueprint:` value is resolved
against `Options.BaseDir` once, at parse time — the same "relative to
what" convention this package's own neighbor-ledger resolution already
establishes — so a `BlueprintCall.Blueprint` is always either an
already-absolute local path or a genuine URL by the time `Parse`
returns. A `ubx_blueprint` node's own children are always leaves (never
mistaken for a container the way an unadorned resource node's own
`ubx_required` subtree needed a dedicated `onlyUbxRequiredChild` fix
for) — `sortedLeaves` special-cases the class directly. A blueprint-
calling node participating in a topology edge is inert (Scope section
above has the reasoning) — no changes needed to the existing edge-
translation pass at all, since it already treats "anything that isn't
`nodeKindResource`/`nodeKindReference`" as having no ubx-legible meaning
as an edge endpoint.

### The md calling convention

`intentprovider`'s own wire validation (`validate.go`) gains
`wireBlueprintCall` — mirrors `wireResourceIntent`'s own "Config is a
JSON-encoded STRING" workaround exactly, for the identical reason
(`Args` is fundamentally open-shaped, and a structured-output schema
must close every object node). The system prompt (`intentprovider/
claude/adapter.go`) gains one new explicit rule, styled after the
existing IAM-attach-language rule: recognizing "Use, call, or
instantiate an existing blueprint by name" phrasing and extracting
EXACTLY the blueprint's own reference plus every named parameter value
into one `blueprint_calls` entry — explicitly instructed to NEVER draft
resources for a blueprint call itself (the model has no way to know
what resources a blueprint actually contains; its own build step
already fixed that, separately, once, outside the conversation
entirely).

```md
# CI platform for payments

Use blueprint /path/to/ci-platform with:
  repo_name = payments-ci-artifacts
  queue_name = payments-notifications
  retention_days = 14
```

`parseAndValidate`'s own "draft has no resources and no destroys"
empty-draft check (the UBI-85-carved-out real bug guard) now also
treats a non-empty `blueprint_calls` as a legitimate, complete draft —
a blueprint-call-only document correctly has an EMPTY `resources[]`,
which must never be flagged as the original "described but never
declared" bug.

### Invocation: reusing Slice 2's own mechanism, never a second one

`blueprint.ExpandCalls` (`blueprint/invoke.go`, new) is the ONE place
either medium's own call actually gets INVOKED. For each `BlueprintCall`:

1. **Always pulled into a fresh, ephemeral temp directory first** —
   `blueprint.Pull` (UBI-74 Slice 3), reused UNCONDITIONALLY for both a
   local and a git reference, never a special-cased "local means no
   copy" shortcut. Invoking a blueprint never mutates its own source,
   local or git, under any circumstance — the whole temp directory is
   removed before `ExpandCalls` returns.
2. **Language picked by a fixed preference order — `ts`, then `py`,
   then `go`** (`callLanguagePreference`) — whichever sibling directory
   the pulled copy actually has built. TypeScript needs no manifest or
   module-resolution setup at all beyond `@ubx/sdk`'s own embedded
   import map (the most robust, zero-configuration path); Python's own
   WASI sandbox needs the calling driver copied alongside the
   blueprint's own `py/` files (never the original — `copyDir`, the
   same "always a throwaway copy" discipline `goeval` itself already
   applies for Go); Go needs a real `go.mod` resolving
   `github.com/ubiquex/ubx-sdk-go`, reusing whatever version the
   blueprint's own already-built `go/go.mod` declares — confirmed live
   that `v0.0.0` is a genuinely real, resolvable version (not a
   placeholder needing a `replace`), so this only needs the module
   already present in the local Go module cache (true on any machine
   that has ever built or run a real Go SDK program against it before)
   since `goeval`'s own `GOPROXY=off` blocks a fresh network fetch.
   Since all three languages produce equivalent resolved deltas
   (UBI-74 Slice 4's own live-verified guarantee), which one actually
   executes a given call is an implementation detail a caller never
   needs to control.
3. **Args resolved and type-coerced against the target blueprint's own
   declared params** (re-parsed from its own `Ubxfile`, on the pulled
   copy) — a missing REQUIRED param is a hard, named error, never
   silently defaulted.
4. **A throwaway calling program is synthesized and run through the
   SAME evaluator `ubx resolve --from-code` already uses** — `tseval.
   Evaluate`/`pyeval.Evaluate`/`goeval.Evaluate`, completely unchanged,
   never a second, parallel invocation mechanism. The synthesized
   program wraps the target function in a real `stack()`/`intent()`/
   evaluate() cycle using the CALLING intent's own stack name (never
   the blueprint's own build-time directory name), so every address/
   embedded-ref the call produces threads the real calling stack
   through exactly like a hand-written SDK program already does
   (UBI-74 Slice 2's own embedded-ref fix applies identically here, for
   the identical reason). Each language's own native calling
   convention is used as-is: TypeScript passes every param positionally
   in required-then-defaulted order (TS has no keyword-argument syntax,
   so an unoverridden default earlier in the list still needs its own
   literal value filled in, reusing `tsDefaultLiteral` directly — the
   SAME renderer `GenerateTS`'s own codegen already uses); Python uses
   native keyword arguments for every param that's either required or
   explicitly overridden (sidesteps the positional-ordering question
   entirely); Go stays positional for required params, `With<Param>(...)`
   options only for an actually-overridden default (Go's own compiled
   options struct already seeds the Ubxfile's own declared default, so
   omitting an un-overridden one is correct and sufficient).
5. **The evaluated intent/v1's own `Resources` are appended to the
   calling document**, address-collision-checked against everything
   already there (both plain `resources[]` entries and any other
   blueprint call's own output) — a hard, named error on collision,
   never a silent overwrite.

A real, named limitation, not silently swept under: deriving the target
blueprint's own exported function/package identifiers requires knowing
its build-time directory basename, which neither a diagram node nor an
md draft actually records — `blueprintNameFromCall` falls back to the
git subdirectory's own basename (or the repo/local-path's own basename)
as a best-effort convention match. A blueprint whose own directory was
renamed after building fails with a clear "function not found"-shaped
error at invocation time, not a silent wrong call. Slice 6's own
provenance tagging is the natural place to close this for real
(recording the blueprint's own build-time name in `blueprint.lock.json`),
not attempted here.

**Live verification, the ticket's own required bar.** A hermetic,
byte-comparison test first
(`TestBlueprintCrossMedium_SDKGoDiagramTSAndMDTS_IdenticalDelta`,
`cli/blueprint_cross_medium_test.go`): the SAME blueprint fixture UBI-74
Slice 2's own SDK-calling test already proves against real fakeprovider,
called via THREE different mechanisms — a hand-written Go SDK program
(Slice 2's own proven path, completely unchanged), a real `.d2`
topology's own `ubx_blueprint` node (`diagram.Parse`, real structural
parsing), and a real fake-adapter md draft (`intentprovider.
DraftWithRetry`, real validation) — all resolve, through the real `ubx
resolve` CLI command, to the IDENTICAL delta shape (same resources, same
`$ref`-derived config values), confirmed by direct, normalized
comparison, not assumed.

Then the SAME real CI-platform blueprint already built and live-proven
in Go (Slice 2) and in TS (Slice 4) — `~/ubx-playground-ubi74-slice4/
ci-platform/` — was called for real over the md path (the leg that
hadn't yet been live-verified against a real model anywhere in this
arc — diagram's own "zero AI" claim carries less real-world risk, since
it's directly checked structurally with no model involved at all): a
real `.md` document (`Use blueprint <path> with: repo_name = ...,
queue_name = ..., retention_days = 14`) drafted via `ubx propose
--from-doc` against the REAL Claude API — the real model correctly
recognized the pattern, extracted the blueprint reference and all three
args verbatim, and emitted `resources: []` (zero hallucinated
resources) plus exactly one `blueprint_calls` entry. `ubx resolve`
against that real draft, against the REAL `hashicorp/aws@6.54.0`
provider's own real schema — `payments: 5 create(s), 0 change(s), 0
terminate(s)`, `message_retention_seconds: 1209600` (14 × 86400,
UBI-123's own corrected value, confirmed by reading the resolved JSON
directly), and the cross-resource reference correctly addressing
`payments.aws_iam_policy.ci-runner-access.arn` (the REAL calling stack,
never the build-time blueprint directory name) — an IDENTICAL shape to
Slice 2's own Go-SDK-proven and Slice 4's own TS-SDK-proven resolves,
this time reached via a real model reading real prose, not a hand-
written program. `ubx accept`ed into a real ledger
(`~/ubx-playground-ubi74-slice5/ledger/`, change id
`08d2bbf32434d000...`) — resolved and accepted, ready for `ubx ship`.
**`ubx ship` itself deliberately not run this session** — the same
CLAUDE.md-mandated, UBI-67-established, every-prior-slice-precedented
founder-runs-it-themselves handoff.

**The diagram leg's own hermetic proof stands as its required
equivalence check, explicitly** — the ticket's own bar names ONE medium
for the real-AWS leg; diagram is the other, and its correctness is
established by the SAME hermetic byte-comparison test named above
(`TestBlueprintCrossMedium_...`), which resolves a real `.d2` file's own
`ubx_blueprint` node through the real `ubx resolve` CLI path and
confirms its output is byte-identical (post-normalization) to the SDK
leg's own real-fakeprovider-resolved delta — not a live-AWS run itself,
but a real, direct structural proof this session did complete, not a
gap left unverified.

## Provenance: Slice 6

Slice 6's own scope is narrow and explicit: tag every blueprint-produced
resource with which blueprint made it, render that provenance in `ubx
why`, and visually group it in `ubx render`. It does NOT touch OCI/Strata
(Slice 7), tarball redistribution (Slice 8), the override mechanism or
`render --sync-overrides` (both UBI-86, a separate ticket), or nesting
(UBI-121).

### Tagging: reusing `core.IntentSource`, not inventing a new field

`core.IntentSource` already exists — `document`/`intent_provider`/
`dialogue`/`sdk`/`promotion`/`cloudtrail` kinds already coexist on
`core.Intent.Sources`, a DOCUMENT-level field (docs/schema.md). Slice 6
adds a new `"blueprint"` kind, but NOT on that document-level field — a
document-level source can't express "resource A came from blueprint X,
but sibling resource B in the same document didn't," a real, ordinary
scenario once a diagram or md document mixes a blueprint call with a
hand-authored resource (`diagram/parse_test.go`'s own
`TestParse_UbxBlueprint_MixedWithOrdinaryResource` already proves this
mixing is legal).

So Slice 6 adds a new, purely additive, per-RESOURCE field instead:
`resolver.ResourceIntent.Sources []core.IntentSource` — the exact same
struct shape, just attached one level down. `resolveOnce` threads it into
each create node's own `"sources"` JSON key (a sibling of `stack`/`type`/
`name`/`config`/`depends_on`, the identical "ride along, resolver has zero
special awareness of what it means" posture `depends_on` itself already
has). Every producer except `blueprint.ExpandCalls` leaves it nil —
`ExpandCalls` is the ONE place that ever populates it, stamping every
resource a call produces with exactly one entry:

```json
{"kind": "blueprint", "ref": "ci-platform:sha256:f893af6e945f..."}
```

`ref` is `"<blueprint_name>:<content_hash>"` — the SAME `buildManifest`
Slice 3's own `package`/`verify` machinery already uses, computed FRESH
over the pulled blueprint directory's own current files at every call
(never requiring the blueprint to have already been explicitly
`package`d first — `blueprint.lock.json` need not exist at all). This is
"version" without inventing a separate versioning scheme: two calls to
the identical blueprint content get the identical ref; a changed
blueprint gets a different one, automatically.

Because `blueprintRef` is computed once per `invokeCall` and stamped onto
every resource that ONE call produces, it's identical regardless of which
medium (diagram/md, via `BlueprintCalls`) or which language
(`callLanguagePreference`: ts, py, go) actually executed it — the ref
describes the blueprint itself, never the calling mechanism.

**A real, named scope boundary**: this only covers the `BlueprintCalls`
mechanism (Slice 5). A stack that imports a generated blueprint package
DIRECTLY (Slice 1/2's own original calling convention —
`writeBlueprintCallingStack`'s own pattern, `ubx resolve --from-code`
against a hand-written program that calls the blueprint's function
inline, in the SAME compiled binary) produces resources with NO
`sources` at all — there is no `BlueprintCalls`/`ExpandCalls` step in
that path to stamp anything, by Slice 1/2's own original design (the
call IS native language composition, not a separate expansion step).
Confirmed directly: `ubx resolve --from-code` against
`writeBlueprintCallingStack`'s own fixture produces a resolved proposal
with zero `"sources"` entries. Closing this gap — if it's even wanted,
since the direct-import path's whole point was "it's just a normal Go/TS/
Python program, no blueprint machinery of its own" — is not attempted
here.

### `ubx why`: the provenance chain, dual-signature honesty

`renderCreates` (`cli/why.go`) decodes each create entry's own `sources`
field (alongside its existing `type`/`name`/`config` decode) and renders
each one via `renderIntentSource`'s existing kind-switch, now with a new
`case "blueprint":` branch:

```
+ aws_ecr_repository.container-repo create
  source: blueprint ci-platform:sha256:f893af6e945f…
  (this resource's own creation is signed by the CALLING stack's own
  acceptance below; the blueprint's own authorship has no separate
  signing ceremony in this build yet)
  name: payments-ci-artifacts-md
  ...
```

UBI-74's own design record names a "dual-signature" story: the blueprint
AUTHOR's own original signing, kept separate from the CALLING stack's own
instantiation signing. This build has no real ceremony for the FIRST
half at all — no `ubx blueprint accept`, no ledger entry for a blueprint's
own definition independent of any call — so Slice 6 renders this
HONESTLY rather than fabricating a signature: the note above names the
gap explicitly, and only the SECOND half (the calling stack's own real
`core.Acceptance` — `Approvers`/`Method`/`AcceptedAt`) is rendered, as the
surrounding proposal's own "accepted by [...] via ... at ..." line.

That line was previously only rendered by the bare-proposal-ID view
(`renderProposal`) — the address-chain view (`renderProposalCompact`,
what `ubx why <stack>.<type>.<name>` actually uses) never rendered
Acceptance at all before Slice 6. Fixed so both views show it: a
blueprint-created resource's own full provenance chain needs the real
calling-stack signature visible, not just which blueprint made it.

### `ubx render`: dashed-border grouping

`diagram/emit.go`'s own stated principle — "no synthetic containers... no
canonical grouping basis to invent" — is about not GUESSING at structure
the diagram medium has no way to know. Blueprint-sourced grouping doesn't
violate this: it's real, resolved-time truth pulled from the resource's
own creating proposal (the same `sources` field `ubx why` reads), exactly
the same posture `depends_on`-derived edges already have.

`emitResource` gained a `blueprintRef string` field (`""` for an ordinary
resource); a new `blueprintSourceFor` helper mirrors `dependsOnFor`
exactly, extracting `sources[].ref` where `kind == "blueprint"` from the
resource's own creating proposal. Resources sharing the same
`blueprintRef` group into one D2 container (`bp0`, `bp1`, ... in
ref-sorted order), styled:

```d2
bp0: "ci-platform:sha256:f893af6e945f…" {
  style.stroke-dash: 3
  style.fill: transparent
  r0: "container-repo" {
    class: aws_ecr_repository
  }
  ...
}
```

— verified empirically that `style.stroke-dash`/`style.fill: transparent`
parse and reformat cleanly through this project's own real
`d2parser`/`d2format` pipeline before committing to this styling; no
prior "dashed" convention existed anywhere in this codebase, so this is a
genuinely new (but validated) styling choice, not a literal reuse of an
existing pattern. The container's own label uses a short-hash form of the
ref (12-char truncation + "…", mirroring `cli/why.go`'s own `displayHash`
convention — `diagram` can't import `cli` to reuse it directly, since
`cli` already imports `diagram`, so `shortBlueprintRef` is a small,
deliberately independent mirror of that one rule, not a shared helper).
An ordinary resource (`blueprintRef == ""`) renders top-level, unchanged
— a stack with zero blueprint-sourced resources produces byte-identical
output to pre-Slice-6 `Emit`.

**A real, pre-existing bug found and fixed along the way, not introduced
by this slice**: `Emit` originally read `depends_on`/`sources` from
`core.FleetEntry.ProposalID` — Fleet's own documented "the LATEST
proposal that touched this address," NOT necessarily the proposal that
CREATED it. For the standard two-resource blueprint fixture (`primary` +
`mirror`, mirror's own `$ref` reading primary's post-apply state), ship
generates a later reconciliation touch on `primary` whose own
`Delta.Creates` is empty — silently dropping `primary`'s own `depends_on`
AND `sources` data, since `entry.ProposalID` no longer pointed at the
create. This was ALREADY latent in `dependsOnFor` before this slice (no
existing fixture happened to exercise it — no shipped resource with both
a real `depends_on` and a later touching proposal), only surfacing once a
real, live end-to-end blueprint-call render test
(`TestRender_BlueprintCall_GroupsInDashedContainer_RealFakeProvider`)
exercised exactly that shape. Fixed with a new `creatingProposalFor`
helper: walks `core.Ledger.ProposalsForAddress` (the address's own full
recorded history, oldest-first) to find the ONE proposal whose own
`Delta.Creates` actually contains the address, independent of whatever
touched it most recently — `dependsOn`/`blueprintRef` now both read from
that, while `crossPins` (correctly "most recent touch," since a
cross-stack pin IS re-recorded on every resolve/ship that reads it) stays
on `entry.ProposalID` as before.

### Hermetic tests

`cli/blueprint_provenance_test.go`: a real shipped-create ledger fixture
(via `core.Accept`/`BeginApply`/`SaveApplyProgress`/`SealApply`, matching
`core/ubi29_test.go`'s own pattern) with a per-resource `sources` field —
confirms `ubx why <id>` AND `ubx why <address>` both render the blueprint
source line, the honest dual-signature note, and the real acceptance
line; confirms an ordinary resource (no `sources`) renders unchanged.

`diagram/emit_test.go`: `TestEmitD2_BlueprintSourcedResources_
GroupInDashedContainer` (two blueprint-sourced resources nest under one
`bp0`, the depends_on edge uses the container-qualified path),
`TestEmitD2_MixedBlueprintAndOrdinaryResources_OnlyBlueprintOnesGroup`
(an ordinary resource stays top-level even in a stack that also has a
blueprint call), `TestEmitD2_NoBlueprintResources_NoContainer_
ByteIdenticalToBefore` (zero blueprint resources → zero container
syntax).

`cli/render_blueprint_test.go`:
`TestRender_BlueprintCall_GroupsInDashedContainer_RealFakeProvider` — a
real diagram-medium blueprint call (`diagram.Parse`'s own `ubx_blueprint`
recognition, real `ExpandCalls` invocation, a real locally-built Go-only
blueprint package, no synthetic shortcuts) resolves, accepts, and ships
two real resources through the real `fakeprovider` pipeline end to end,
then confirms `ubx render` groups both inside one dashed container with
the real depends_on edge drawn between container-qualified paths — the
exact test that caught the `creatingProposalFor` bug above.

Full account of exactly which tests, and the real-AWS live verification
this slice required, is in this file's own "Implementation slices" entry
below.

## OCI push/pull: Slice 7

The founder's own design (UBI-74 Linear comment, 2026-08-04): the tarball
`ubx blueprint package` already produces (Slice 3) can be pushed as a
real OCI artifact, reusing the ORAS pattern already proven in production
for Helm charts, WASM modules, SBOMs, and Cosign signatures — every
existing OCI registry (GHCR, Docker Hub, Artifactory, Harbor) becomes a
working blueprint-distribution backend with zero new server code, no
bespoke Strata registry service required for v1.

### Library: `oras.land/oras-go/v2`, confirmed live before use

Two real ORAS Go modules exist: `oras.land/oras-go` (v1, legacy) and
`oras.land/oras-go/v2` (the actively maintained line, what the real
`oras` CLI itself uses). Confirmed directly via `go doc` against the real
downloaded module — not assumed from memory — before writing any
production code: `oras.land/oras-go/v2` v2.6.2 is current.
`github.com/oras-project/oras-credentials-go` (a separate, older
credential-helper package) turned out to be deprecated in favor of
functionality now built directly into `oras-go/v2` itself
(`oras.land/oras-go/v2/registry/remote/credentials`) — confirmed via its
own `go doc` output, which states so explicitly — so that dependency was
added, then dropped again (`go mod tidy`) once the non-deprecated path
was found.

### One manifest, one blob layer — the tarball, unmodified

```
ubx blueprint push ci-platform-v1.tar.gz --to oci://ghcr.io/ubiquex/ci-platform:v1
```

`Push` (`blueprint/oci.go`) does NOT re-encode or re-wrap the tarball —
it's added as-is, as the artifact's one blob layer, via
`oras.land/oras-go/v2/content/file.Store.Add` (media type
`application/vnd.ubx.blueprint.v1.tar+gzip`, the founder's own literal
example). `oras.PackManifest` (OCI 1.1, `PackManifestVersion1_1`, no
separate config blob — `artifactType:
"application/vnd.ubx.blueprint.v1"`) builds the manifest wrapping that
one layer; `file.Store.Tag` tags it locally; `oras.Copy` copies the whole
DAG (manifest + blob) to the real remote target.

`Pull` (`blueprint/pull.go`) gained a third source-type branch: an
`oci://registry/repo:tag` reference is detected before the existing
local-path/git dispatch, `--ref`/`--path` (git-specific — meaningless
here, since the tag is already embedded in the `oci://` reference itself)
are refused if set rather than silently ignored. `oras.Copy` pulls the
manifest+blob into a throwaway local `file.Store`, then a new
`extractTarGz` (`blueprint/oci.go`, `writeTarGz`'s own converse —
genuinely new code, nothing before this slice ever had to read a tarball
back) expands the one pulled blob into `dest`. Guards against a tar entry
naming a path that escapes `dest` (a malicious/corrupted tarball) — this
runs before `Verify`'s own content-hash check would ever get a chance to
reject tampered content, so extraction itself can't yet trust the
tarball's own filenames. Either way, `dest` ends up holding an ordinary
local blueprint directory (`blueprint.lock.json` included),
indistinguishable afterward from Slice 3's own local-path/git output —
`ubx blueprint verify` needs zero awareness of which of the three source
types actually produced it.

### Content-hash verification: one hash scheme, not two

The task's own explicit instruction — confirm OCI's native digest maps
cleanly onto the SAME `core.CanonicalJSON` hash already used, never
introduce a second hashing scheme — resolves cleanly once the two
digests' actual JOBS are named separately, not conflated:

- **OCI's own blob digest** (computed natively by `oras-go` from the
  tarball's real bytes, verified by the registry on every push/pull) is
  a TRANSPORT-integrity check — "did these exact bytes survive the round
  trip to the registry and back." Zero new code needed for this: it's
  `oras-go`'s own job, entirely.
- **`content_hash`** (`blueprint.lock.json`'s own field, `core.
  CanonicalJSON`-based, already established by Slice 3) stays the
  APPLICATION-level check — "is this genuinely the blueprint content I
  built/reviewed, unmodified" — exactly the same hash `Verify` already
  checks, completely unchanged by this slice. It travels INSIDE the
  tarball (since the tarball itself is pushed unmodified), so it's
  already present after a pull+extract, verified the identical way
  regardless of source type.

The one real addition: `content_hash` (and the blueprint's own `name`)
are ALSO recorded as OCI manifest annotations
(`dev.ubiquex.blueprint.content_hash`/`dev.ubiquex.blueprint.name`, plus
a fixed `org.opencontainers.image.created` — CLAUDE.md's own
"determinism is a feature" rule, extended to the OCI layer the same way
`writeTarGz` already zeroes every tar header's own `ModTime`, so pushing
identical content twice produces a byte-identical manifest, not just an
identical `content_hash`). Purely a visibility convenience — a `docker
manifest inspect`/`oras manifest fetch` can cross-check the content hash
without pulling+extracting first — never a second, competing
verification path.

### Hermetic tests

`blueprint/oci_test.go`: `stripOCIScheme` table test (scheme required,
non-empty after it); `extractTarGz` round-trips byte-for-byte with a real
`Package`-produced tarball, and rejects a hand-crafted path-traversal tar
entry; `manifestFromTarball` refuses a tarball with no
`blueprint.lock.json` inside (points the caller at `ubx blueprint
package`); `pushToTarget`/`pullFromTarget` (the target-agnostic halves of
`Push`/`pullOCI`, split out specifically so the real ORAS mechanics —
`fs.Add`/`oras.PackManifest`/`fs.Tag`/`oras.Copy` — are exercised against
a REAL local target, `oras.land/oras-go/v2/content/oci.Store`, a real
on-disk OCI image layout, never a hand-rolled fake of `oras-go`'s own
internals) round-trip a real packaged blueprint end to end, `Verify`
confirmed passing on the pulled result with the identical `ContentHash`;
a separate test confirms the pushed manifest's own raw JSON genuinely
carries the `content_hash`/name annotations. `cli/blueprint_oci_test.go`:
CLI-level flag validation, all hermetic (no network) since every check
exercised — missing `--to`, a `--to` missing the `oci://` scheme, an
unpackaged tarball, `--ref`/`--path` against an `oci://` source, a
tagless `oci://` reference — is refused before `Push`/`pullOCI` ever
attempts a real registry connection.

### Live verification, the ticket's own required bar, genuinely met

Before writing any production code: confirmed the founder's own real
`docker login ghcr.io` credential actually works (`docker login ghcr.io`
re-run, reusing the cached credential — "Login Succeeded", `Username:
roozbehshafiee`) and confirmed a real, unauthenticated-looking pull
against a nonexistent tag returns "not found" rather than "denied"
(the real signal auth is working, not just that the tag doesn't exist).
Confirmed `oras-go/v2`'s own real API surface against its actual
downloaded source (`go doc`, not memory) before committing to a design,
including a full LOCAL round trip (a real tarball, `fs.Add` →
`oras.PackManifest` → `fs.Tag` → `oras.Copy` into a local on-disk
`oci.Store` → `oras.Copy` back out into a second `file.Store`, digests
matching) and a real credential-resolution check
(`credentials.NewStoreFromDocker` + `credentials.Credential` genuinely
resolving `Username: roozbehshafiee`, `PasswordSet: true` for `ghcr.io`)
— both run as standalone throwaway Go programs, entirely offline/local
except the credential read, before either was wired into production
code.

Then the real bar itself: the real `ci-platform` blueprint (ECR+SQS+IAM
role+policy+attachment, proven live across Slices 1–6 — the identical
directory at `~/ubx-playground-ubi74-slice4/ci-platform/`) packaged fresh
(`content hash sha256:f893af6e945fe1e708af03dd60fe5372b76969579b3bdc8b70
c3b4238968c885` — the EXACT same hash Slice 6's own real render output
already stamped onto real shipped AWS resources, confirming this is
genuinely the same proven content, not a fresh/different build) and
pushed for real to `ghcr.io/ubiquex/ci-platform:v1`. Independently
confirmed landed via a real `docker manifest inspect
ghcr.io/ubiquex/ci-platform:v1` (not just trusting `ubx blueprint push`'s
own success message) — real manifest JSON, `artifactType:
"application/vnd.ubx.blueprint.v1"`, one layer at
`application/vnd.ubx.blueprint.v1.tar+gzip`, and the
`dev.ubiquex.blueprint.content_hash` annotation visible natively,
matching the local hash exactly. Pulled back via `ubx blueprint pull
oci://ghcr.io/ubiquex/ci-platform:v1` into a genuinely separate directory
(`~/ubx-playground-ubi74-slice7/pulled`) — `ubx blueprint verify`
confirmed the content hash matches (11 files, identical hash), and a real
`go build ./...`/`go vet ./...` against the pulled copy's own `go/`
subdirectory succeeded cleanly — real network, the actual published
`github.com/ubiquex/ubx-sdk-go` module, no local `replace` directive, the
identical bar Slice 3 met for git, now met for a real OCI registry.

`ghcr.io/ubiquex/ci-platform:v1` is left published, deliberately — unlike
a real-AWS playground stack (which must be terminated), a pushed OCI
artifact IS this slice's own intended deliverable, not a transient test
resource.

## Offline delivery + redistribution: Slice 8

UBI-74's own original eight-slice plan closes here. The founder's own
redistribution design (Linear comment, 2026-08-04) names three real
patterns, borrowed from git/npm/Docker precedent: (1) fork+republish
under a new name/namespace, with provenance recording fork lineage; (2)
re-tag/mirror unchanged — the trust-preserving default when no changes
are needed (air-gapped/compliance mirroring); (3) alias/pointer — a thin
manifest referencing the original with no content copy. Non-negotiable
across all three: the original author's signature is NEVER erased or
reattributed. Slice 8 builds pattern (2) for real; documents pattern (1)
as a real design, not built; doesn't touch pattern (3) at all (the
re-tag/mirror case already proves the load-bearing property this slice's
own success bar requires — "trust survives redistribution" — an
alias/pointer's own value is purely storage-cost, not a new trust
property to prove).

### A fourth `Pull` source type: a bare tarball FILE

```
ubx blueprint pull ci-platform-v1.tar.gz ./pulled
```

`Pull`'s existing dispatch (`blueprint/pull.go`) already distinguished
"a local filesystem path that's a directory" (Slice 3) from "everything
else, tried as git" — Slice 7 added a `oci://`-prefix check ahead of
that. Slice 8 slots into the EXACT gap that dispatch already implied but
never filled: `if info, err := os.Stat(source); err == nil { if
!info.IsDir() { ... } }` — a `source` that stats successfully but is a
FILE, not a directory, is a bare tarball. No ambiguity is possible: a
directory can never satisfy `!info.IsDir()`, and a bare tarball file can
never satisfy `info.IsDir()` — the same `os.Stat`-based check Slice 3
already used to distinguish "local path" from "try git" cleanly extends
to a third local-disk shape with zero new heuristics. `--ref`/`--path`
(git-specific) are refused if set, mirroring the `oci://` branch's own
refusal.

The tarball is extracted directly via `extractTarGz` (Slice 7's own
`writeTarGz` converse, `blueprint/oci.go` — reused verbatim, not
reimplemented) into `dest`, then checked for a real `Ubxfile` (mirroring
the git branch's own "not a blueprint package" refusal) — no network
call anywhere on this path, the genuine offline/air-gapped/email/
support-ticket delivery mode the original design record names.

### Content-hash verification: unchanged, and it's what actually protects this path

Slice 8 adds ZERO new hashing code. `Pull` itself neither trusts nor
rejects a tarball's own declared `content_hash` — it only extracts,
exactly like every other source type leaves verification as a separate,
explicit `ubx blueprint verify` step. This matters MORE here than for any
other source type: git has commit history: an OCI registry validates its
own blob digest natively on every push/pull. A bare tarball file has
NEITHER — the original design record's own framing, "the one delivery
mode where tampering is genuinely easy without this check," is exactly
right. `Verify` (unchanged since Slice 3, `core.CanonicalJSON`-based)
recomputes the hash from whatever's actually on disk after extraction and
compares it against `blueprint.lock.json`'s own declared value — a
tarball corrupted or tampered with in transit (an email attachment
swapped, a support-ticket upload truncated) extracts without complaint,
but `Verify` catches the mismatch every time, the identical mechanism
Slices 3 and 7 already rely on.

### A real, live-found subtlety: `content_hash` is a function of NAME too, not just file content

`Package` derives a blueprint's own `name` from `filepath.Base(absDir)`
at package time — never carried over from a prior manifest. Found live,
not predicted: re-packaging an identical set of files into a
DIFFERENTLY-NAMED directory produces a DIFFERENT `content_hash`, even
though nothing about the actual blueprint content changed (`buildManifest`
canonicalizes `{schema_version, name, files}` together — `name` is
genuinely part of what gets hashed, per `Verify`'s own existing doc
comment: "the content hash must stay invariant under which directory you
pull this into," which is about `Verify` recomputing against the
manifest's OWN `declared.Name`, not about two independent `Package` calls
against differently-named directories agreeing). Confirmed directly this
session: pulling the real `ci-platform` blueprint from GHCR into a
directory named `fresh-from-ghcr` and re-packaging it produced
`sha256:fe80b25...` — a DIFFERENT hash from the original
`sha256:f893af6e945f...` — purely from the directory rename, not any
content change. Re-pulling into a directory correctly named `ci-platform`
reproduced the exact original hash. **Operational implication, worth
stating plainly for anyone re-exporting a blueprint for offline
delivery**: name the destination directory to match the blueprint's own
declared name before re-`package`ing it, or the resulting tarball's own
hash won't trace back to the original published artifact even though the
content is byte-identical. Not a bug — `name` genuinely identifying WHICH
blueprint this is (not just what bytes it contains) is the correct
design (`ubx why`'s own `blueprint:<name>:<content_hash>` ref format
already depends on `name` being meaningful) — but it's a real, easy-to-
trip-on interaction between two independently-reasonable design points,
worth naming explicitly rather than leaving as a silent surprise.

### Re-tag/mirror-unchanged redistribution, built and live-verified

The re-tag/mirror-unchanged pattern needs NO new code at all — it's
`ubx blueprint push` (Slice 7) called a second time, against a second
`--to` reference, with the identical tarball file, never re-packaged or
modified:

```
ubx blueprint push ci-platform-v1.tar.gz --to oci://ghcr.io/ubiquex/ci-platform:v1        # original
ubx blueprint push ci-platform-v1.tar.gz --to oci://ghcr.io/ubiquex/ci-platform-mirror:v1  # mirror, unchanged
```

Both pushes upload the SAME bytes, so the SAME OCI blob digest AND the
SAME `content_hash` land at both locations — confirmed live this
session, not assumed (see below): `docker manifest inspect` against both
`ci-platform:v1` and `ci-platform-mirror:v1` reported the IDENTICAL blob
digest, not merely an identical `content_hash` annotation. `ubx blueprint
verify` against a copy pulled from EITHER location reports the identical
content hash — the ORIGINAL author's own signature (in this build's own
vocabulary: content-hash-based tamper evidence, since no cryptographic
signing tier exists yet — see "A blueprint call's own provenance" in
`cli/why.mdx`/docs/blueprint.md's own "Provenance: Slice 6" section for
the honest account of what "signature" means in this build today),
genuinely surviving redistribution to a second, independent location.

### Fork-with-modification: designed, not built (stretch goal, explicitly not attempted)

A genuine derivative — someone takes a published blueprint, changes its
resources, and republishes it under a related identity — is a real,
different case from mirroring: the CONTENT changes, so `content_hash`
correctly changes too (it's supposed to — a fork that alters resources
producing the identical hash as the original would be a REAL bug, not a
feature). What must NOT change is the fork's own honesty about its
lineage: the founder's own non-negotiable rule is "the original author's
signature is NEVER erased or reattributed — even a fork's own provenance
must show 'originally authored by X, forked by Y.'"

**The design, reusing existing mechanism rather than inventing a new
field shape** (the SAME posture every prior slice in this arc already
took — Slice 6's own `core.IntentSource` reuse is the direct precedent):
`Manifest` (`blueprint/manifest.go`) would gain an optional
`ForkedFrom *ForkOrigin` field —

```go
type ForkOrigin struct {
	Name        string `json:"name"`
	ContentHash string `json:"content_hash"`
}
```

— populated by a new `ubx blueprint fork <source> <dest> --as <new-name>`
command: pulls `source` exactly like `Pull` already does (reusing it
unchanged), records the SOURCE's own declared `Name`/`ContentHash` into
the NEW manifest's `ForkedFrom` before `buildManifest` computes the
fork's own (necessarily different, since `name` changed too) hash. `ubx
why`'s own `case "blueprint":` rendering (`cli/why.go`, Slice 6) would
gain a parallel line whenever a resource's own blueprint ref traces back
to a forked manifest — "forked from `<original-name>:<original-hash>`,
originally authored there" — surfacing the ORIGINAL author's own
attribution every time a forked blueprint's own provenance is inspected,
never silently dropped. This is a real, buildable design (every
mechanism it reuses — `Pull`, `buildManifest`, the `sources`/why-
rendering convention — already exists and is already proven) — not
attempted this session, per this slice's own explicit "stretch goal, not
required" framing, named here so it isn't silently left unaddressed.

### Hermetic tests

`blueprint/pull_test.go`: `TestPull_BareTarballFile_
ExtractsWithoutNetwork` (a real `Package`-produced tarball, pulled by
file path with `HTTP_PROXY`/`HTTPS_PROXY` pointed at an unreachable
address — proving zero network dependency empirically, not just by code
inspection); `TestPull_BareTarballFile_RefPathFlagsRefused`;
`TestPull_BareTarballFile_MissingUbxfile_Errors` (a real, structurally
valid gzipped tarball that just isn't a blueprint); `TestPull_
BareTarballFile_TamperedContent_VerifyFails` (a tarball's own tar-level
bytes altered AFTER a real `Package` call — extracts without complaint,
`Verify` catches the mismatch). `blueprint/oci_test.go`:
`TestMirrorRedistribution_ContentHashSurvivesToASecondLocation` (the
SAME tarball pushed to TWO independent local `oci.Store` targets,
pulling from either and running `Verify` reports the identical original
content hash). `cli/blueprint_offline_test.go`: CLI-level package →
pull(bare file) → verify round trip, plus the `--ref` refusal.

### Live verification, the ticket's own required bar, genuinely met

Pulled the real, currently-live `ci-platform` blueprint fresh from
`ghcr.io/ubiquex/ci-platform:v1` (into a directory correctly named
`ci-platform` — see the live-found naming subtlety above) and
re-packaged it into a standalone tarball file — `content hash
sha256:f893af6e945fe1e708af03dd60fe5372b76969579b3bdc8b70c3b4238968c885`,
the EXACT same hash the original push carries, confirming the export is
genuinely byte-identical to what's live on GHCR, not a fresh/different
build. Pulled that bare tarball FILE with `HTTP_PROXY`/`HTTPS_PROXY`
pointed at `127.0.0.1:1` (an address nothing listens on) — succeeded,
proving no network dependency for real, not just by code inspection.
`ubx blueprint verify` against the offline-pulled copy confirmed the
identical content hash, and a real `go build ./...`/`go vet ./...`
against its own `go/` subdirectory succeeded cleanly (real network for
module resolution, no local `replace` — the identical bar every prior
slice's own live leg met).

Then the real mirror: the same tarball pushed to a SECOND, independent
real GHCR location, `ghcr.io/ubiquex/ci-platform-mirror:v1`.
Independently confirmed via `docker manifest inspect` against BOTH
locations — the blob digest (`sha256:f22c6e87ed30cbcaa4300372d3a9b3f28
df18c7e14c7115435291c8b349dd49c`) is IDENTICAL at both, not merely an
identical `content_hash` annotation, confirming the mirror is genuinely
the same bytes, not a re-derived equivalent. Pulled from the mirror
location into a separate directory — `ubx blueprint verify` confirmed
the identical original content hash, proving content-hash-based trust
survives redistribution to a real, independent second location, exactly
the property this slice's own success bar required.

## Provenance parity across all three calling mediums: UBI-126

UBI-126 (filed at the close of UBI-74's own 8-slice retrospective,
2026-08-05, the #1 gap named there) — a real correctness/consistency
gap, not a new feature: Slice 6's own provenance tagging only ever fired
for a diagram/md call (`blueprint.ExpandCalls`'s own external stamping,
which has full knowledge of the blueprint's own ref because it just
pulled and hashed it before running the caller). A direct SDK-import call
(Slice 2's own ORIGINAL calling convention, proven first, live on real
AWS) never got the same treatment — its resolved resources carried no
`sources` at all, even though Slice 5's own hermetic proof
(`TestBlueprintCrossMedium_SDKGoDiagramTSAndMDTS_IdenticalDelta`) already
established all three calling conventions produce byte-identical resolved
DELTAS. `ubx why`/`ubx render` on a direct-SDK-called resource showed
nothing a diagram/md call's own identical resource already did.

### Why this needed a genuinely different mechanism, not just a copy-paste

`ExpandCalls` (Slice 6) can safely stamp EVERY resource its own
synthesized calling program produces, because that synthesized program's
one and only job is calling the target blueprint function — nothing else
ever appears in its own result. A real, hand-written direct-SDK-import
stack has no such guarantee: it can freely mix a blueprint call with its
own directly-declared resources in the same program, so "stamp
everything goeval returns" is never correct there. The compiled,
sandboxed binary that actually executes a direct-SDK-import call also
has no way to compute its own blueprint's real content hash — it doesn't
know its own on-disk blueprint directory at runtime, and baking a
hash into source code that then gets hashed AS PART of that same
directory is circular (the same reasoning `blueprint.lock.json`'s own
exclusion from `hashFiles`'s walk already establishes, one level up).

### The fix: an implicit, in-process scope + an external hash resolution step

**Inside the evaluated program** (`sdk/go/runtime/runtime.go`): a new
package-level scope stack, `PushBlueprintSource(name)`/
`PopBlueprintSource()` — implicit, exactly like the existing `current
*collector` (the active `Stack()` pointer) already is, deliberately NOT
a new parameter on `sdk.Resource()` itself (which would force every
existing caller, blueprint or not, to change). `blueprint/gogen.go`'s
own `renderGoFunction` wraps EVERY generated blueprint function's body in
this scope, using the blueprint's own bare, unsanitized declared name
(`filepath.Base` of its directory — the SAME identifier `buildManifest`/
`ubx why`/`ubx render` already use, never the Go-identifier-sanitized
module/package name). `addResource` (inside `sdk.Resource()`) checks
this scope and, if active, stamps the new `intentResource.Sources` field
with `{"kind":"blueprint","ref":"<bare-name>"}` — deliberately
INCOMPLETE (no content hash yet), since the compiled binary genuinely
cannot compute one for itself.

**Outside the evaluated program**, after it returns
(`blueprint/sdkprovenance.go`, new — `StampDirectCallProvenance`, called
from `cli/resolve.go`'s own `--from-code` handling, Go entries only): a
fast-path check first — if no resource carries an incomplete blueprint
source, this is a no-op, `go list` never runs, an ordinary Go SDK program
pays nothing extra. Otherwise, walks the entry program's own real Go
module graph (`go list -m -f '{{.Path}}|{{.Dir}}' all`, run directly
against the entry file's own real directory and go.mod — not goeval's
own already-torn-down ephemeral build copy; module resolution is
deterministic from the same go.mod/module cache/replace directives
either way, `GOFLAGS=-mod=mod` matching `goeval`'s own `buildProgram`
exactly, since a freshly-written, not-yet-`go mod tidy`d go.mod is the
normal state here). For every resolved module whose own directory's
PARENT contains a real `Ubxfile` (Slice 4's own established go/ts/py
sibling-directory convention — a blueprint's Go module root is always
exactly one level below the blueprint's own root), computes its real
content hash via `buildManifest` — the SAME function `ExpandCalls`
already calls, never a second hashing mechanism — and rewrites every
matching incomplete `Sources[].Ref` in place to the full
`"<name>:<content_hash>"` form.

**A real, named, remaining scope boundary**: this only works for a
blueprint reachable via a local directory or a local `go.mod` `replace`
directive — the pattern every real direct-SDK-import example in this
project already uses (Slice 2's own live verification included). A
blueprint distributed as a standalone published Go module, with no
adjacent `Ubxfile`/`blueprint.lock.json` inside that module's own
boundary, isn't supported yet — a real, honest limitation, refused with
a clear, named error rather than silently producing a wrong or missing
ref.

**Go only, for now.** This whole mechanism — the module-graph walk
specifically — is inherently Go-specific. TS/Python's own direct-import
calling paths still have the ORIGINAL gap (no provenance at all);
extending this to them needs an analogous, language-appropriate discovery
mechanism (TS: its own import-map/`package.json` resolution; Python:
`sys.path`/module resolution) — real, separate future work, not attempted
here, named rather than silently left unaddressed.

### A real regression found and fixed along the way

A direct-SDK-import call and `ExpandCalls`'s own synthesized Go caller
turn out to hit BOTH provenance mechanisms at once — the synthesized
caller is, after all, just another caller of the SAME generated blueprint
function, which now ALWAYS pushes its own scope regardless of who
invokes it. `invokeCall`'s own pre-existing stamping loop (Slice 6)
previously APPENDED its own complete, authoritative ref rather than
replacing whatever was already there — meaning a Go-language diagram/md
call briefly produced TWO source entries (one bare, from the new
SDK-level tag; one complete, from `invokeCall` itself), with renderers
that read `sources[0]` (e.g. `ubx render`'s own container label) picking
up the WRONG, incomplete one. Caught by this session's own
`TestRender_BlueprintCall_GroupsInDashedContainer_RealFakeProvider`
(pre-existing, from Slice 6/7) failing for real before any new test was
even written. Fixed by having `invokeCall` filter out any existing
`"blueprint"`-kind source before appending its own — its own externally-
computed ref is always strictly more authoritative than anything the
evaluated program could produce internally. A second, related fix was
needed alongside it: `writeGoCaller`'s own synthesized caller previously
resolved `github.com/ubiquex/ubx-sdk-go` however the blueprint's own
go.mod's `require` line said (never any `replace`), which broke the
FIRST time a real sdk/go runtime change (this one) needed testing before
being published — fixed by also carrying forward an optional `replace`
line if the blueprint's own go.mod has one, matching (and never
overriding) production behavior, where no such `replace` exists at all.

### Hermetic tests

`cli/blueprint_call_test.go` (new):
`TestBlueprintCall_DirectSDKImport_HasBlueprintProvenance` (the resolved
JSON itself, both resources sharing the identical complete ref);
`TestBlueprintCall_DirectSDKImport_WhyAndRenderShowProvenance` (one level
further — a REAL shipped ledger, `ubx why` showing `source: blueprint
platform:sha256:...`, `ubx render` showing the dashed-border grouping,
exactly matching what a diagram call already produced before this fix).
`blueprint/invoke_test.go`:
`TestExpandCalls_ProvenanceStamped_Go` (the Go-language sibling of the
existing TS/Python tests — the ONE language combination that exercises
BOTH provenance mechanisms at once, and the exact regression proof for
the double-tagging bug above). `cli/blueprint_cross_medium_test.go`:
`TestBlueprintCrossMedium_ProvenanceIdentical` (extends Slice 5's own
identical-delta proof to provenance specifically — the SAME shared
on-disk blueprint called via all three mediums produces BYTE-IDENTICAL
`sources`, not merely same-shaped).

### Live verification

The direct-SDK-import path's real generated code (`ubx blueprint build`,
the real `ci-platform` pattern, a real Claude draft) was rebuilt with
this fix in place and confirmed to emit the new
`sdk.PushBlueprintSource("ci-platform")`/`defer sdk.PopBlueprintSource()`
wrapping around its own function body. `TestBlueprintCall_
DirectSDKImport_WhyAndRenderShowProvenance` is itself a real, live,
end-to-end run — real `ubx resolve --from-code`, real `ubx accept`, a
real `ubx ship` against `fakeprovider` (this project's own standing
"never real cloud for verification" discipline applies identically here;
fakeprovider is the sanctioned stand-in, the same one every prior
slice's own hermetic ship-and-verify test already uses) — captured real,
unedited output:

```text
$ ubx why platform.fake_widget.primary --ledger-dir .
platform.fake_widget.primary: 2 proposal(s), newest first
- drift_adopt 9e99845520ac… (2026-08-05T11:31:57Z): platform.fake_widget.primary's own real, same-batch side effect from shipping f6a2de0bafc3f3dc2fe9f69460fafda333d021527d899bcc06514958e540c1db (post-chain re-observation, UBI-63)
    accepted by [roozbeh] via local at 2026-08-05T11:31:57Z
    ~ platform.fake_widget.primary change
        tags.env: "prod" -> (absent)
- change f6a2de0bafc3… (2026-08-05T11:31:57Z): platform, via a called blueprint
    source: document create_platform.go (content_hash=sha256:8d377a41fa173de885f47c40c8d0e0a75651046144ed44b48efb5bf59d8873ab)
    accepted by [roozbeh] via local at 2026-08-05T11:31:57Z
    + fake_widget.primary create
      source: blueprint platform:sha256:e1aaf06dd3c0…
        (this resource's own creation is signed by the CALLING stack's own acceptance below; the blueprint's own authorship has no separate signing ceremony in this build yet)
        name: "widget1"
        tags:
        {
          "env": "prod"
        }
```

```text
$ ubx render --stack platform --ledger-dir .
classes: {
  fake_widget
}
bp0: "platform:sha256:e1aaf06dd3c0…" {
  style.stroke-dash: 3
  style.fill: transparent
  r0: "mirror" {
    class: fake_widget
    tooltip: "id: computed-id; name: widget1; tags: {\"peer_id\":\"computed-id\"}"
  }
  r1: "primary" {
    class: fake_widget
    tooltip: "id: computed-id; name: widget1; tags: {\"env\":\"prod\"}"
  }
}
bp0.r0 -> bp0.r1
```

A resource created by a direct SDK import now shows EXACTLY the same
provenance shape a diagram or md call's own identical resource already
did (Slice 6) — the calling convention that started this whole arc
(Slice 2) is, at last, no longer the one silently missing it.

## Python blueprint dependency resolution: UBI-130

Python has no native "import from a URL" mechanism — unlike Go modules
resolving straight from git, or Deno's own import-map-based JSR
resolution. A Python stack calling a blueprint published to a registry
(git, OCI/GHCR, or a bare local path) needs a real, CI-pipeline-friendly
way to declare and resolve that dependency, distinct from UBI-107
(publishing the shared `ubx_sdk` RUNTIME to PyPI — a completely separate
concern from resolving a blueprint PACKAGE dependency).

### The design: pip's own `<name> @ <url>` syntax, resolved at plan time

`requirements.txt`, sitting next to the entry `.py` file, is the primary
— and for now the only — supported manifest format. A `pyproject.toml`
`[tool.ubx.blueprints]` table was considered as an alternative and
rejected for now: `requirements.txt` is already the ubiquitous, zero-
config file every real Python CI pipeline reads with no extra tooling,
and it's exactly the format this ticket's own resolved design commits to
in its full worked example. A `pyproject.toml`-based path is a real,
deferred alternative — it can be added later without changing
`requirements.txt`'s own meaning at all.

No new syntax is invented: a blueprint dependency is declared with pip's
own real `<name> @ <url>` specifier (PEP 508 + pip's documented VCS-URL
support), recognizing three forms:

```
ci-platform @ oci://ghcr.io/ubiquex/ci-platform:v3
widget-lib  @ git+https://github.com/org/blueprints.git@v2#subdirectory=widget-lib
widget-lib  @ ../shared/widget-lib
```

- `oci://registry/repo:tag` — passed straight through to `Pull`'s own
  existing `oci://` branch (Slice 7).
- `git+<transport>://...[@<ref>][#subdirectory=<path>]` — pip's own real
  VCS-URL grammar. The `git+` prefix, `@<ref>`, and `#subdirectory=<path>`
  are all stripped before handing the bare transport URL to `Pull`'s
  existing git-clone branch (Slice 3) as `(source, ref, path)`. An `@` in
  the URL's own user-info (`git+ssh://git@github.com/...`) is left
  untouched — a ref never contains `/`, so only an `@` found in the URL's
  *final path segment* is treated as `@<ref>`.
  ("git" is the second of the ticket's own three named schemes; the
  `git+` form was chosen over a bespoke `git://` scheme specifically
  because it's pip's own real, already-documented syntax, not a new
  invention.)
- A bare local path, or an explicit `file://` URL — `Pull`'s existing
  local-directory/tarball-file branch either way.

Every one of these reuses `Pull`/`Verify`/`buildManifest`
(`blueprint/pull.go`, `verify.go`, `manifest.go` — Slice 3/7/8's own
mechanism) directly. There is deliberately no second, parallel pull
mechanism.

### Resolution happens inside `ubx plan`/`ubx resolve --from-code`, not a separate step

`cli/resolve.go`'s and `cli/plan.go`'s own `--from-code` dispatch (both
share `evaluateSDKProgram`) routes a `.py` entry file through
`blueprint.EvaluatePythonWithDeps` (`blueprint/pydeps.go`) instead of
calling `pyeval.Evaluate` directly:

1. `ResolvePyDependencies` reads the entry file's own sibling
   `requirements.txt` (if any) and, for each declared dependency, pulls
   (or reuses an already-verified local-cache hit — see below) +
   verifies it, in file order.
2. Every resolved dependency's own built `py/` directory is handed to
   `pyeval.Evaluate` as an `ExtraDep` — a new, minimal type
   (`{HostDir string}`) `pyeval` itself understands with zero knowledge
   of blueprints, pulling, or verification at all (`blueprint` already
   depends on `pyeval`, so the reverse dependency would be an import
   cycle). `pyeval`'s own WASI runner (`runOnce`, `pyeval/runner.go`)
   mounts each one at its own fresh top-level guest preopen
   (`/ubxdep0`, `/ubxdep1`, …, matching the same "one top-level preopen
   per real directory tree" rule the `ubx_sdk` runtime mount already
   established empirically) and prepends its guest path to `PYTHONPATH`
   — all BEFORE the entry script itself ever runs, so a plain
   `from <pkg> import <fn>` in the caller's own hand-written `.py`
   resolves with zero special import syntax.
3. Every dependency, cached or freshly pulled, produces a real,
   non-empty receipt line, printed by the CLI (`cmd.OutOrStdout()`)
   BEFORE resolution proceeds — this project's own "never a silent
   network call" discipline, matching `provider.Acquire`'s own framing:

   ```
   pulled ci-platform @ oci://ghcr.io/ubiquex/ci-platform:v3, verified: content hash sha256:… matches (11 file(s))
   ```

   (a cache hit's own receipt names it explicitly: `... (cached), verified: ...`).

A Python program with no `requirements.txt` — or one with no `"@ url"`
entries — pays nothing extra: `ResolvePyDependencies` returns `(nil, nil)`
and `pyeval.Evaluate` runs exactly as it always has.

Note the entry `.py` file itself uses a PLAIN import, no URL at all —
`requirements.txt`'s own declared LHS name is checked against the pulled
blueprint's own `blueprint.lock.json` name (a real integrity check — a
mismatch is a clear, named error, never silently accepted), but is
otherwise just a label. Unlike `invokeCall`'s own synthesized callers
(`blueprint/invoke.go`, Slice 5), nothing here generates any calling
code — the calling script is hand-written by a real user, who is
expected to already know the blueprint's own real, build-time-derived
Python module/function names, exactly like any real pip package's
distribution name and its importable module name are already allowed to
differ.

### Local cache: mirrors `provider.Acquire`'s own discipline

`~/.ubx/blueprints/by-spec/<sha256 of the declared name+url>/` — the
same `~/.ubx/<kind>/...` cache-root convention `provider/cache.go`'s own
`~/.ubx/providers` already established, applied here to a pulled
blueprint instead of a provider binary. Unlike a provider binary, a
blueprint dependency has no registry-signed version to trust before ever
pulling, so the cache is keyed by the declared spec itself (there is
nothing else to key it by ahead of a real pull); `Verify`, run on every
hit — cached or fresh — is what actually establishes trust, same as
every other `Verify` call in this codebase. Once verified, a cache hit
is never re-pulled or re-verified from the network again.

A **local-path dependency is never cached** — unlike a git ref or an OCI
tag, a bare local path names something a blueprint author may be
actively editing; caching it under a spec-derived key would silently
serve stale content after an edit. It's pulled into a fresh scratch
directory and verified fresh on every resolve instead.

### A real, live-found finding: CPython writes `__pycache__` into whatever it imports from

Required live verification (below) surfaced a real bug, not a
hypothetical one: wasmtime's own `--dir host::guest` preopen is
read-write by default, and CPython's own import machinery writes
`__pycache__/*.pyc` bytecode-cache files into the directory a module was
imported FROM as a side effect of merely running the calling script.
Harmless for `invoke.go`'s own throwaway scratch copies (discarded right
after use) — but for a **cached** blueprint dependency, the very act of
using it once mutated its own cache directory, so every subsequent
`Verify` against that same cache entry failed content-hash comparison
and forced a full re-pull on every single resolve, silently defeating
the cache entirely (still correct, just never actually cached — the
receipt line would never say `(cached)`).

Fixed at the actual cause (`pyeval/runner.go`): `PYTHONDONTWRITEBYTECODE=1`
is now set for every `pyeval` guest process, unconditionally — CPython
never writes a bytecode cache into ANY mounted directory, not just a
UBI-130 dependency mount. Pure performance optimization CPython
otherwise does silently, so this has no other observable effect, and it
required no change to `blueprint/manifest.go`'s own hashing contract —
the real, already-published `ghcr.io/ubiquex/ci-platform:v1` artifact's
own `blueprint.lock.json` still declares two `py/__pycache__/*.pyc`
entries from whenever it was originally built (a real, pre-existing,
harmless quirk of that one already-shipped artifact, left untouched
rather than "fixed" by excluding `__pycache__` from `hashFiles`
globally, which would have broken verification of that historical
artifact instead).

### Hermetic tests

`blueprint/pydeps_test.go`: parsing (`ParsePyDependencies` — all three
schemes, an ssh user-info `@` correctly NOT mistaken for a ref, ordinary
non-`"@ url"` requirement lines and comments skipped, missing-file
returns `(nil, nil)`, malformed lines and unrecognized schemes both
error); resolution (`ResolvePyDependencies` — a local-path dependency
mounts and verifies for real; a declared-name/manifest-name mismatch
errors; a dependency built without a `py/` package errors by name;
a real local git repository, tagged, with `#subdirectory=` — proves a
first resolve is a fresh pull, a second is a cache hit, and — the
strongest form of that proof — the cache hit still succeeds after the
git repository itself is deleted entirely); one real end-to-end
subprocess test (`TestEvaluatePythonWithDeps_RealPullBeforeImport`,
gated on `wasmtime` being present) proving the actual pull-before-import
sequencing against a real `wasmtime` run — a hand-written driver script
in one directory genuinely imports a function from a dependency pulled
from a completely separate directory, with the resulting resource
present in the evaluated output.

### Live verification, the ticket's own required bar, genuinely met

The exact worked example named in the ticket, run for real: a real
`requirements.txt` (`ci-platform @ oci://ghcr.io/ubiquex/ci-platform:v1`
— the real, already-published tag; the ticket's own illustrative `:v3`
never existed) and a real `create_ci_platform.py`, calling `ubx plan
--from-code create_ci_platform.py`:

- Against the real, live `ghcr.io/ubiquex/ci-platform:v1` artifact
  (Slice 7's own real, already-published artifact — no new artifact was
  pushed for this ticket), with the local blueprint cache cleared first:
  a genuinely fresh pull, the real required receipt line rendered
  (`pulled ci-platform @ oci://ghcr.io/ubiquex/ci-platform:v1, verified:
  content hash sha256:f893af6e945fe1e708af03dd60fe5372b76969579b3bdc8b70c3b4238968c885
  matches (11 file(s))`), and a second run of the identical command
  showing the cache-hit form (`... (cached), verified: ...`) with an
  IDENTICAL resolved delta either way.
- Against `fakeprovider` first (a separate, minimal `widget-lib`
  blueprint dependency, `fake_widget`-typed so fakeprovider's own schema
  actually covers it — the real `ci-platform` blueprint's resources are
  real AWS types fakeprovider has no schema for at all): a real, full
  `ubx plan --from-code` → `ubx accept` → `ubx ship --yes` against a real
  `fakeprovider` subprocess, confirmed shipped via `ubx why`.
- Against the real `hashicorp/aws@6.54.0` provider's own real schema
  (resolve/plan only, per this project's own standing "never `ubx ship`
  against a real cloud provider" rule — `resolve`/`plan` are schema-fetch-
  only and explicitly exempt): the real `ci-platform` blueprint's own
  `ci_platform(...)` function, pulled from the real OCI artifact and
  imported with zero special syntax in the calling script, executed for
  real and produced all 5 real AWS resources
  (`aws_ecr_repository`/`aws_sqs_queue`/`aws_iam_role`/`aws_iam_policy`/
  `aws_iam_role_policy_attachment`) with correct cross-resource `$ref`s
  and the blueprint's own documented days→seconds conversion
  (`retention_days=7` → `message_retention_seconds: 604800`) — never
  shipped.

**A real, live-found discrepancy in the ticket's own worked example,
confirmed against the real published artifact, not assumed**: the
ticket's illustrative `.py` source (`from ci_platform import CiPlatform`)
does not match the blueprint's own real, live Slice-4 codegen naming —
`packageIdent`/`pythonIdentifier` (`blueprint/identifier.go`) actually
produce `py/ciplatform.py` (no separator) and `def ci_platform(...)`
(snake_case), confirmed both by this repo's own hermetic
`pygen_test.go` fixtures and by pulling and inspecting the real
`ghcr.io/ubiquex/ci-platform:v1` artifact directly. This is purely a
naming detail in the ticket's own illustrative prose, not a design or
mechanism issue — a distribution name (`requirements.txt`'s own LHS,
`ci-platform`) and its importable module/function names are already
allowed to differ in real Python packaging, exactly like any real pip
package. The worked example above uses the real, correct names.

### A real, named, remaining scope boundary

UBI-130 resolves blueprint dependencies for Python; it does not add
Python-side blueprint-call PROVENANCE (UBI-126's own concern, Go only)
— a resource produced by a Python program's own pulled-dependency call
carries ordinary `document`-kind source provenance (the calling script
itself), not a `blueprint`-kind one, exactly the same real, already-named
gap UBI-126's own retrospective called out: "TS/Python's own direct-
import calling paths still have the ORIGINAL gap." Closing it for Python
is real, separate, future work — matching the honest, un-glossed framing
this whole arc has kept throughout.

## Implementation slices

- 2026-08-04 (UBI-74 Slice 1): built and live-verified. `blueprint/`
  (Ubxfile parser + Go codegen) and `cli/blueprint.go` (`ubx blueprint
  build`) match the design above exactly, including one design point that
  only got settled from a real hermetic test failure, not predicted in
  advance: `intentprovider/validate.go`'s own wire-level `Config` field is
  a JSON-encoded STRING (`cli/blueprint_test.go`'s first draft fixture
  used a raw object and was correctly rejected — "cannot unmarshal object
  into Go struct field wireResourceIntent.resources.config of type
  string") — but `parseAndValidate` unwraps it into a real
  `json.RawMessage` object before `DraftWithRetry` ever returns, so
  `GenerateGo`'s own assumption (Config is a real JSON object one level
  deep) was correct once traced through, not a lucky guess.

  **Live verification, per the ticket's own required bar**: a hand-
  authored Ubxfile for this project's own recurring CI-platform pattern
  (ECR + SQS + IAM role + policy + attachment, matching
  `intentprovider/conformance/fixtures/platform-iam-attach.md`'s own
  shape, parameterized: `repo_name`/`queue_name` required,
  `retention_days` defaulting to 1) built against the REAL Claude API (no
  fake adapter) via `ubx blueprint build .` — 5 resources (the real draft
  split the policy and its attachment into two resources, not the fixture
  prose's implied one). `go mod tidy` + `go build ./...` + `go vet ./...`
  all run for real against the actual published
  `github.com/ubiquex/ubx-sdk-go` module (real network, real
  `go.sum` hashes fetched from the real module proxy — not this
  repo's own hermetic local-`replace` trick, which stays reserved for
  `go test ./...`) — clean, no errors, confirmed by reading the compiled
  output, not assumed from "the code looks right."

  **One real finding from the live draft that updates this document's own
  earlier speculation, not just confirms it**: `aws_iam_policy`'s own
  `policy` attribute came back from the real Claude draft as a plain Go
  string whose OWN content is escaped JSON with a `$ref` marker embedded
  inside it (`"Resource\":{\"$ref\":{\"to\":\"ci-platform.
  aws_ecr_repository.ci-repo.arn\"}}"`) — exactly the shape flagged above
  as a possible gap. Checked against `core/resolver/refs.go`'s own
  `containsMarker` doc comment before concluding anything: this is NOT a
  bug — it's the exact, already-supported "JSON-embedded refs" shape that
  comment names directly ("a JSON-envelope string that needs marker
  resolution, typically an IAM-policy-shaped document with a $ref
  standing in for what would otherwise be a literal ARN"), which the
  resolver's own reference walk and the executor's own `substituteComputed`
  both already know how to find and substitute one level into a string
  attribute's own decoded JSON. Slice 1's codegen never tries to parse or
  rewrite a string value's own embedded content — it renders the resolved
  config's string value VERBATIM as a quoted Go literal — which turns out
  to be exactly correct here: the generated Go source reproduces the
  identical embedded-`$ref` text the real resolver pipeline already
  expects to find and resolve later, at Slice 2+'s own call/resolve time.
  Corrects this document's own earlier "not solved this slice, a real
  gap" framing above, which was written before live verification, not
  after.

  Full test suite green (`go test ./...`), gofmt/go vet clean. Committed
  this session -- see STATE.md for the commit and any docs-debt entries.

- 2026-08-04 (UBI-74 Slice 2): built and live-verified, both hermetically
  and against real AWS (resolve/accept only -- see below for why `ship`
  itself was deliberately not run this session). Closes Slice 1's own
  named open point (`params:` default) and proves the calling convention.

  **Functional-options defaults** (`gogen.go`: `renderOptions`,
  `defaultLiteral`, `checkOptionIdentCollisions`, `paramRef`'s own
  `Required`-branch): matches `provider/acquire.go`'s own `AcquireOption`
  precedent, confirmed to be this codebase's own established idiom for
  optional/defaulted call-time values before inventing anything new (the
  session's own explicit instruction). See "Resolved defaults: functional
  options" above for the full design.

  **Two real, live-found findings, neither predicted in Slice 1's own
  "adversarial cases" section, both fixed this session:**

  1. **The JSON-embedded-`$ref` case Slice 1 believed was "exactly
     correct" was wrong once actually called from a real, differently-
     named stack.** Slice 1's own live verification never actually RAN
     the generated function (its own success bar was build+compile
     only); Slice 2's first real-AWS attempt did, from a stack named
     `payments` calling a blueprint built as `ci-platform`, and got a
     real resolver refusal: `reference does not resolve to any known
     resource or attribute: ci-platform.aws_ecr_repository....`. Root
     cause: `renderString` rendered a JSON-embedded `$ref`'s own address
     VERBATIM, baking in the BUILD-time blueprint stack name forever.
     Fixed by `renderEmbeddedRefString` (see "Adversarial cases
     considered" above for the full account) -- splits the string into
     literal segments plus the referenced resource's own runtime
     `Computed.Address()` calls, so the address reflects whichever stack
     actually calls the function. Regression-tested deterministically
     (`TestGenerateGo_EmbeddedRefUsesRuntimeStackName`, a real `go run`
     proving the emitted document's own address uses the CALLING stack's
     name, not the build-time one) and confirmed against the real fix
     applied to a real live Claude draft before accepting the real-AWS
     leg below as done.
  2. **A live Claude draft, given a resource "named `{repo_name}`,"
     genuinely used the literal token `{repo_name}` as the resource's
     own IDENTITY (`ResourceIntent.Name`), not just its `name` attribute
     VALUE** -- correctly rejected by `pascalCase`'s own existing
     character validation (unrelated to this session's own code changes;
     pure prose ambiguity), but worth recording since it recurred
     identically across two separate live draft attempts before the
     Ubxfile's own prose was tightened to explicitly separate a
     resource's own fixed internal "slug" from the parameterized
     attribute VALUE named inside it. Not a code bug -- a reminder that a
     blueprint author's own prose needs to disambiguate resource
     identity from resource content wherever a param could plausibly be
     read as either, the same category of lesson UBI-74 Slice 1 already
     learned once for `{param}`-preservation itself.

  **Hermetic proof, required by the ticket's own success bar, fully
  met**: `TestBlueprintCall_ResolveAcceptShip_RealFakeProvider` (`cli/
  blueprint_call_test.go`) -- a real `blueprint.GenerateGo`-built
  package (not a hand-written stand-in), imported by a real `sdk.Stack`/
  `sdk.Main` calling stack via an ordinary local `go.mod` `replace`,
  resolved through the completely unmodified `ubx resolve --from-code`
  CLI path, accepted, and shipped end to end against `fakeprovider` --
  zero errors, both resources (one cross-referencing the other via a
  real `$ref`) shipping exactly as `ship_change_test.go`'s own
  hand-authored equivalent already does.
  `TestBlueprintCall_DefaultOverride_ResolveUnchanged` covers the same
  path with a `With<Param>` override reaching the resolved proposal.

  **Real-AWS verification, resolve/accept only -- ship deliberately not
  run this session, per CLAUDE.md's own standing rule.** The identical
  CI-platform blueprint (ECR + SQS + IAM role + policy + attachment)
  built against the REAL Claude API (no fake adapter, `~/ubx-playground-
  ubi74-slice2/ci-platform/`), called from a real stack
  (`~/ubx-playground-ubi74-slice2/stack/create_ci_platform.go`, stack
  name `payments`, `repo_name="payments-ci-artifacts"`,
  `queue_name="payments-notifications"`,
  `WithRetentionDays(30)`), resolved via `ubx resolve --from-code`
  against the REAL `hashicorp/aws@6.54.0` provider's own real schema
  (`--source`/`--provider-version`, a safe schema-fetch-only operation --
  CLAUDE.md's own doctrine explicitly names `resolve`/`propose`/`sdk gen`
  as safe against a real provider) -- `payments: 5 create(s), 0
  change(s), 0 terminate(s)`, real `repo_name`/`queue_name` values, real
  `message_retention_seconds: 30` (the override, not the default,
  confirming functional options reach the real pipeline too), and the
  JSON-embedded `$ref`s correctly addressing `payments.aws_ecr_repository
  .container-repo.arn`/`payments.aws_sqs_queue.pipeline-events.arn` (the
  REAL calling stack, not `ci-platform`) -- direct, real confirmation
  the embedded-ref fix above is correct, not just unit-tested. `ubx
  accept`ed into a real ledger (`~/ubx-playground-ubi74-slice2/ledger/`,
  change id `45f63ce10849...`) -- resolved and accepted, ready for `ubx
  ship`.

  CLAUDE.md's own ship-verification rule ("never run `ubx ship`... against
  a real cloud provider... even one already credentialed on the machine...
  always, no exceptions") was deliberately honored as absolute, not
  reinterpreted for this ticket's own "start hermetic... THEN a real live
  leg" framing -- this project's own prior session (UBI-67, see this
  file's own STATE.md entry) already found that framing insufficient once
  tested against the harness's own independent safety classifier: an
  attempt to draft and act on a specific named real-`ship` exception in
  the same turn was blocked as self-authorized permission-widening, and
  the established resolution recorded there is that the founder runs the
  real `ubx ship`/`ubx terminate` themselves, in their own shell, once
  everything up to accept is prepared and ready. That is exactly this
  session's own stopping point: `~/ubx-playground-ubi74-slice2/ledger/`
  is ready for `ubx ship 45f63ce10849... --ledger-dir
  ~/ubx-playground-ubi74-slice2/ledger` (then, after inspecting the real
  result, `ubx terminate`/an `aws` CLI check to confirm the account
  clean) whenever the founder runs it. **This session does not claim the
  live-ship bar was met -- only resolve/accept, deliberately.**

  Full test suite green (`go test ./...`), `gofmt -l .`/`go vet ./...`
  clean. `make build` run and `ubx version` checked before every live
  verification step above, per this project's own standing rebuild
  discipline. Committed this session -- see STATE.md.

- 2026-08-04 (UBI-123, reopened): **the encode-path bug did not exist.**
  The ticket's own reopening comment reported the resolved proposal
  genuinely carrying `"message_retention_seconds": 2592000` on disk (file-
  read verified, confirmed correct) yet the real `ubx ship` against real
  AWS still failing with `InvalidAttributeValue: Invalid value for the
  parameter MessageRetentionPeriod` three separate times, including once
  with a brand-new queue name ruling out name-reuse -- reasonably
  concluding the bug must be downstream of resolution, in whatever encodes
  the resolved intent into the real tfplugin `ApplyResourceChange` wire
  call, and instructing this session not to re-diagnose codegen.

  **Traced the actual encode path first** (`core/executor/ship.go`'s
  `shipCreate` -> `Applier.ApplyResourceChange` -> `cli/stateadapter.go`'s
  `stateReaderAdapter` -> `provider/provider.go`'s v5/v6 `ApplyResourceChange`
  -> `provider/ctyvalue.go`'s `encodeUnknownAwareDynamicValue`/
  `encodePrimitiveValue`) before writing a line of test code. Found one
  real, general (but NOT applicable to this specific value) precision
  hazard along the way: `shipCreate` decodes `cn.Config` with a plain
  `json.Unmarshal` (no `UseNumber()`), so every JSON number becomes a Go
  `float64` before `encodeUnknownAwareDynamicValue`'s own `UseNumber()`-
  based decoder ever gets a chance to preserve it exactly -- a real
  precision-loss risk for numbers beyond float64's exact-integer range
  (2^53), confirmed empirically (`123456789012345678` round-trips to
  `123456789012345680`). But `2592000` is nowhere near that boundary --
  Go's `float64`/JSON round-trip is provably lossless for it, confirmed
  empirically before concluding anything.

  **Built the exact hermetic repro the ticket required, not a substitute
  for it** -- and it's what settled this. `fake_widget` (this repo's own
  standing create-path fixture) has no numeric-typed attribute at all, so
  a real `cty.Number` value had never once gone through
  `encodePrimitiveValue`'s own `cty.Number` branch in this repo's test
  suite before now. Extended `provider/internal/fakeprovider`'s existing
  dynamic conformance-mode fixture (env-var-driven arbitrary resource
  type/attrs, previously read-only -- adopt/mutate/scan-diff only) with a
  real `"number"` `FAKEPROVIDER_ATTR_TYPES` kind (`cty.Number`, both v5
  and v6 schema builders) and a genuine `ApplyResourceChange` handler
  (previously unimplemented for conformance mode -- it only ever needed
  `ReadResource`), reusing the existing `echoConformanceState` helper.
  One real bug surfaced building this fixture itself (not in production
  code): `echoConformanceState`'s own Computed-`id` fill-in checked
  `IsNull()` only, correct for `ReadResource`'s own `CurrentState` (never
  unknown) but wrong for `ApplyResourceChange`'s own `PlannedState` (a
  Computed-and-unset attribute is genuinely UNKNOWN there, not null, per
  the tfplugin protocol) -- fixed to check `IsNull() || !IsKnown()`.

  **`TestResolveAcceptShip_NumericConfig_SurvivesEncodeUnchanged`**
  (`cli/ship_numeric_config_test.go`, UBI-123's own required permanent
  regression guard for this bug CLASS -- "correct at rest, corrupted in
  flight"): a real `aws_sqs_queue`-typed, `message_retention_seconds:
  2592000`-configured resource, resolved, accepted, and shipped through
  the completely real `ubx resolve`/`accept`/`ship` CLI path against a
  REAL fakeprovider subprocess (conformance-v6 mode, not a mock) --
  checked at two independent points: (1) `UBX_SHIP_DEBUG_TRACE_CONFIG`
  (new, `core/executor/ship.go`'s `traceShipCreateConfig` -- the "real
  logging/tracing at the encode boundary" this investigation's own first
  required step asked for, kept as a permanent, zero-cost-when-unset
  diagnostic aid, not a one-off print deleted afterward) shows
  `"message_retention_seconds":2592000` in the exact `plannedState` bytes
  immediately before `ApplyResourceChange` fires; (2) `ubx why`'s own
  rendering of the real fake provider's own echoed-back
  `provider_result` -- proof the value survived a REAL cty-msgpack
  encode, a REAL gRPC call across a REAL process boundary, and a REAL
  decode back, not just what ubx's own client claims to have sent. Both
  show `2592000`, unchanged, every time.

  **The real root cause, once the encode-corruption hypothesis was ruled
  out empirically rather than just re-asserted: `message_retention_seconds:
  2592000` is not a corrupted value reaching AWS -- it is a genuinely
  INVALID one, confirmed directly against AWS's own `SetQueueAttributes`
  API reference.** SQS's real `MessageRetentionPeriod` bound is
  documented as "an integer representing seconds, from 60 (1 minute) to
  1,209,600 (14 days)" -- `retention_days: 30` converts (correctly, per
  UBI-123's own original fix) to 2,592,000 seconds, more than DOUBLE the
  real maximum. `InvalidAttributeValue: Invalid value for the parameter
  MessageRetentionPeriod` is AWS's SQS service correctly rejecting an
  out-of-range value -- not ubx corrupting, dropping, or misencoding
  anything. This is why the bug "round-tripped to the founder three
  times on unverified fixes": every prior fix (the arithmetic conversion
  itself, and the embedded-ref stack-name fix before it) was genuinely
  correct, and none of them could ever have made 30 days of SQS retention
  valid, because it structurally isn't.

  **Corrected live-verification guidance, not attempted with the
  original value**: the founder's own next `ubx resolve --from-code`
  against `~/ubx-playground-ubi74-slice2/stack/create_ci_platform.go`
  must call `ciplatform.WithRetentionDays` with a value that converts to
  at most 1,209,600 seconds (14 days) -- e.g. `WithRetentionDays(14)`
  (the real maximum, 1,209,600 seconds exactly) or `WithRetentionDays(7)`
  (604,800 seconds, a safer margin) -- never `30` again; it will fail
  identically against real AWS regardless of anything in this codebase,
  every time, forever, no matter how many more times codegen/executor are
  independently re-verified correct.

  Full suite green (`go test ./... -count=1`), `gofmt -l .`/`go vet
  ./...` clean. This session's own live-AWS leg (ship the corrected
  value, confirm the SQS queue creates, terminate everything, confirm the
  account clean) was not run by this session itself -- see STATE.md for
  why and for the exact handoff.

- 2026-08-04 (UBI-74 Slice 3): built and live-verified. `blueprint/manifest.go`
  (`Manifest`, `hashFiles`/`buildManifest`/read-write-manifest),
  `blueprint/package.go` (`Package`, `writeTarGz`), `blueprint/verify.go`
  (`Verify`, `diffFiles`), `blueprint/pull.go` (`Pull`, git clone/checkout
  shell-outs, `copyDir`), and `cli/blueprint.go`'s three new subcommands
  match the design in "Package/pull/verify: distribution" above.

  **One deliberate design call, not obvious from the ticket's own
  wording**: `package`'s content hash is computed over a canonical
  `{schema_version, name, files}` manifest via `core.CanonicalJSON`
  (per-file `sha256:<hex>` content hashes, JCS-canonicalized), NOT over
  the tarball's own raw bytes -- reusing the exact hashing approach
  `core.Hash` already established for a Proposal, per this session's own
  explicit instruction not to invent a new hashing convention. This also
  turned out to matter practically, not just for consistency: it's what
  makes `writeTarGz`'s own deterministic-tar-bytes property (sorted entry
  order, zeroed `ModTime`/`Uid`/`Gid`) a nice-to-have rather than
  load-bearing -- the content hash would be identical either way, since
  it never depends on tar framing at all.

  **Hermetic tests** (`blueprint/package_test.go`, `blueprint/
  verify_test.go`, `blueprint/pull_test.go`, plus CLI-level round trips in
  `cli/blueprint_test.go`): tarball structure (exact entry set, byte-
  identical file content, zeroed `ModTime`), deterministic re-packaging
  (byte-identical tarball across two runs over identical content),
  tamper/mismatch detection (a changed, missing, or added file each
  produce a `Verify` error naming that exact file), content-hash
  invariance under directory rename, and a real git clone/checkout round
  trip via `file://` (forces `Pull` down the git-clone code path even
  though the "repository" is itself a local directory -- `os.Stat`
  succeeding would otherwise misroute it into the plain local-copy path
  and never exercise `gitClone`/`gitCheckout` at all) with `--ref`/
  `--path` both genuinely scoping the pull (a root-level file outside
  `--path` is confirmed NOT to leak into the pulled copy). The CLI-level
  git test goes one step further and runs a real, hermetic `go build`
  against the pulled copy too (a local `replace` onto this repo's own
  `sdk/go`, `GOPROXY=off` -- the same hermetic pattern `blueprint/
  gogen_test.go`'s own `TestGenerateGo_CompilesClean` already
  established), so the suite proves "pulled from git and genuinely
  compiles," not just "the right bytes arrived."

  **Live verification, the ticket's own required bar, genuinely met** --
  full account in "Package/pull/verify: distribution" above: Slice 1/2's
  own already-live-AWS-verified CI-platform package, packaged, pushed to
  a real, newly created GitHub repository
  (`github.com/Ubiquex/ubx-sdk-blueprints`) with real commit history,
  pulled from a completely separate local directory via a real HTTPS
  clone (not a local-filesystem shortcut), verified (`content_hash`
  matched byte-for-byte against what `package` originally reported), and
  confirmed genuinely usable via a real `go build ./...`/`go vet ./...`
  against the actual published `ubx-sdk-go` module -- real network, no
  local `replace`. Tamper-detection was also confirmed live on a
  throwaway copy of the pulled directory, not just in the hermetic suite.

  Full test suite green (`go test ./... -count=1`), `gofmt -l .`/`go vet
  ./...` clean. `make build` run and `ubx version` checked before every
  live verification step, per this project's own standing rebuild
  discipline. Committed this session -- see STATE.md.

- 2026-08-04 (UBI-74 Slice 4): built and live-verified across all three
  languages, per "Multi-language codegen" above. `blueprint/decode.go`
  (new -- the shared language-neutral decode/dependency/topo-sort layer,
  factored out of `gogen.go`), `blueprint/tsgen.go`/`pygen.go` (new --
  `GenerateTS`/`GeneratePython`), `blueprint/identifier.go` (extended --
  `pythonIdentifier`/`pythonKeywords`/`tsReservedIdent`), `blueprint/
  ubxfile.go` (`ParamType.TSType()`/`PyType()`, `lang:` grammar broadened
  to accept ts/py/all), and `cli/blueprint.go`'s new `--lang` flag
  (`blueprintGenerators`, `parseLangFlag`) match the design above.

  **Confirmed before writing any new codegen, not assumed**: `sdk/codegen`'s
  own IR/template machinery solves a structurally different problem
  (schema -> generic binding library) from blueprint codegen's own
  (resolved concrete values -> source) -- not a trivial drop-in, exactly
  as this session's own instructions anticipated. What genuinely carried
  over (identifier-casing precedent, the placeholder grammar, shared
  numeric/string-literal rendering) and what didn't (a schema-driven
  Config/Attrs interface pair -- blueprint has no schema to derive one
  from) is recorded in full above.

  **Two real, live-found bugs, both caught by this session's own hermetic
  tests before shipping, neither predicted in advance:**
  1. TypeScript's own `Computed<any>` fails to typecheck property access
     at all (a real `deno check` error, not a hypothetical) -- TS's
     conditional-type distribution over a naked `any` produces a union
     whose `ComputedMarker` branch has no index signature. Fixed with an
     `as any` cast at each referenced resource's own local `const`
     declaration.
  2. Python's own local variable naming initially reused Go/TS's
     `lowerFirst(pascalIdent)` (camelCase) instead of genuine snake_case
     -- caught by `TestGeneratePython_CiPlatform`'s own literal
     assertion on the exact generated variable name, fixed with a
     dedicated `pyLocalVarName`.

  **Hermetic tests** (`blueprint/tsgen_test.go`, `blueprint/pygen_test.go`,
  new; `blueprint/gogen_test.go` updated for the `go/`-prefixed output):
  each language gets its own real compile/typecheck/import proof (`go
  build`, `deno check --no-remote` against a real `@ubx/sdk` import map,
  a real `python3` import with `PYTHONPATH` onto `sdk/py`), its own
  real-execution proof that defaults are load-bearing and the embedded-ref
  fix threads the real calling stack's own name through (`deno run`/
  `python3` driver scripts calling the generated function inside a real
  `stack()`/`intent()`/evaluate() cycle, decoding the emitted intent/v1
  JSON), and UBI-123's own required cross-language regression test
  (`TestGenerateTS_PlaceholderArithmetic`/`TestGeneratePython_
  PlaceholderArithmetic` -- the SAME `retention_days * 86400` fixture
  Go's own test already covers, run for real in each language, not
  assumed to carry over by analogy).

  **Live verification, the ticket's own required bar, genuinely met for
  Go/TS/Python's own compile/typecheck/import leg, and for TS's own
  real-AWS leg** -- full account in "Multi-language codegen" above: the
  SAME CI-platform Ubxfile built `--lang all` against the real Claude API
  (one real, live-found flakiness retry -- "response had no text content
  block," the same intermittent failure mode Slice 2's own session
  already documented, recovered on a plain retry, not investigated
  further as a new finding), all three languages' own generated output
  compiling/typechecking/importing cleanly, the TS-compiled function
  called from a real TS stack and resolved against the real
  `hashicorp/aws@6.54.0` provider's own schema with the UBI-123-corrected
  `retention_days: 14` reaching the resolved proposal correctly
  (`1209600` seconds), accepted into a real ledger. `ubx ship` itself
  deliberately not run this session -- same CLAUDE.md-mandated founder-
  runs-it-themselves handoff as every other real-AWS leg in this project.

  Full test suite green (`go test ./... -count=1`), `gofmt -l .`/`go vet
  ./...` clean. `make build` run and `ubx version` checked before every
  live verification step. Committed this session -- see STATE.md.

- 2026-08-04 (UBI-74 Slice 5): built and live-verified, per
  "Cross-medium calling" above. `core/resolver/resolver.go`
  (`IntentFile.BlueprintCalls`/`BlueprintCall`, `ErrUnexpandedBlueprintCalls`),
  `diagram/parse.go` (the `ubx_blueprint` node kind, `blueprintCallFromNode`),
  `intentprovider/validate.go`+`schema.go`+`claude/adapter.go`
  (`wireBlueprintCall`, the schema addition, the new system-prompt rule),
  `blueprint/invoke.go` (new -- `ExpandCalls` and the three per-language
  caller synthesizers), and `cli/resolve.go`'s new `ExpandCalls` splice
  match the design above.

  **Confirmed before writing any expansion code, not assumed**: routing
  diagram and md calls through Slice 2's own SDK-invocation mechanism
  meant literally reusing `goeval`/`tseval`/`pyeval`'s own `Evaluate`
  functions -- checked each one's own real entry-file requirements
  directly (`goeval/build.go`'s own module-root search + `GOPROXY=off`;
  `tseval/runner.go`'s own absolute-path entry import, meaning a
  synthesized TS caller needs no copy of the blueprint's own `ts/` files
  at all; `pyeval/runner.go`'s own WASI `--dir entryDir::/prog` mount,
  meaning a synthesized Python caller DOES need to sit alongside the
  blueprint's own `py/` files) before designing `writeTSCaller`/
  `writePyCaller`/`writeGoCaller` around what each evaluator actually
  needs, not a guess.

  **One real assumption checked live and found wrong, corrected before
  it became a design liability**: this session's own first pass assumed
  `github.com/ubiquex/ubx-sdk-go`'s placeholder `v0.0.0` version
  (`GenerateGo`'s own emitted `go.mod`) would need a real `go mod tidy`
  (or a local `replace`) before a synthesized Go caller could resolve
  it -- checked directly with a real `go build` against a real import
  before writing that constraint into `writeGoCaller`'s own design, and
  it's wrong: `v0.0.0` is a genuinely real, resolvable version of the
  real published module. The only real condition is that module already
  being in the local Go module cache, since `goeval`'s own `GOPROXY=off`
  blocks a fresh network fetch during evaluation -- true on this
  machine (confirmed: `TestExpandCalls_LocalBlueprint_GoFallback`
  passes for real, not skipped), and documented as the real, narrower
  condition it actually is, not left as the broader, incorrect
  "needs go mod tidy first" claim the first pass would have shipped.

  **Hermetic tests**: `core/resolver` (unchanged -- the new field is
  purely additive, every existing test stays green); `diagram/
  parse_test.go` (`ubx_blueprint` node classification, reserved-attribute
  extraction, git ref/path, `BaseDir`-relative resolution, mixed with an
  ordinary resource node); `intentprovider/validate_test.go`
  (`blueprint_calls` wire parsing, the empty-draft-check carve-out,
  missing-blueprint-reference refusal); `blueprint/invoke_test.go`
  (`ExpandCalls` genuinely invoking a LOCAL blueprint through all
  THREE languages -- not just the preferred one -- a GIT-referenced one
  via the same `file://` trick `pull_test.go` established, UBI-123's
  own `retention_days * 86400` arithmetic reaching the wire through the
  full expansion path, missing-required-param/no-built-language/
  resource-collision refusals); `cli/blueprint_cross_medium_test.go`
  (the required identical-delta-shape proof, SDK vs. diagram vs. md, all
  three real, none hand-waved).

  **Live verification, the ticket's own required bar, genuinely met --
  md got the real-AWS leg, diagram's equivalence is the hermetic proof
  above** -- full account in "Cross-medium calling" above: a real `.md`
  document drafted against the REAL Claude API correctly recognized the
  blueprint-call pattern and extracted it with zero hallucinated
  resources; resolved against the REAL `hashicorp/aws@6.54.0` provider's
  own schema with UBI-123's own corrected `retention_days: 14` reaching
  the resolved proposal correctly; accepted into a real ledger, ready to
  ship. `ubx ship` itself deliberately not run this session -- the same
  founder-runs-it-themselves handoff as every other real-AWS leg in this
  project.

  Full test suite green (`go test ./... -count=1`), `gofmt -l .`/`go vet
  ./...` clean. `make build` run and `ubx version` checked before every
  live verification step. Committed this session -- see STATE.md.

- 2026-08-05 (UBI-74 Slice 6): built and live-verified against real
  `hashicorp/aws@6.54.0`, per "Provenance: Slice 6" above.
  `core/resolver/resolver.go` (`ResourceIntent.Sources`),
  `blueprint/invoke.go` (`ExpandCalls`'s own stamping), `cli/why.go`
  (`renderCreates`'s own `sources` decode, the new `case "blueprint":` in
  `renderIntentSource`, `renderProposalCompact`'s own new Acceptance
  line), and `diagram/emit.go` (`emitResource.blueprintRef`,
  `blueprintSourceFor`, the container-grouping restructure of `emitD2`,
  `shortBlueprintRef`) match the design above.

  **A real, live-found bug, not predicted in advance**: the ticket's own
  premise ("Slice 4 or 5's playground stack is still live") turned out to
  be false -- checked directly against both ledgers' own `applies/`
  records and, independently, real read-only `aws` CLI calls before
  trusting either: Slice 4 was fully shipped AND already terminated by
  the founder; Slice 5 was only ever resolved+accepted, `ubx ship` never
  run (no `applies/` directory at all). Surfaced to the founder rather
  than silently substituting a synthetic fixture for the required
  real-AWS leg -- the founder chose "prepare a new real-AWS leg, I'll
  ship it," the same prepare/hand-off/verify pattern Slices 2 and 5
  already established.

  **A second real, live-found bug, this one in code, not premise**: the
  first live render against the real shipped result grouped only ONE of
  the two resources in the initial hermetic fixture (`primary`/`mirror`)
  under its own blueprint container -- `primary` rendered top-level,
  ungrouped, despite carrying the identical `sources` entry in its own
  accepted proposal (confirmed by reading the accepted ledger proposal
  directly before assuming the tagging code itself was at fault). Traced
  to `Emit` resolving `depends_on`/provenance via `core.FleetEntry.
  ProposalID` -- Fleet's own documented "the LATEST proposal that
  touched this address," not necessarily its CREATING one. `mirror`'s own
  `$ref` read of `primary`'s real post-apply state during ship generates
  a later touch on `primary` (a resolution-inputs entry, zero
  `Delta.Creates`) that becomes Fleet's own "latest," silently emptying
  what `dependsOnFor`/`blueprintSourceFor` could find. This was ALREADY
  latent in `dependsOnFor` before this slice -- no existing fixture
  happened to combine a real `depends_on` with a later touching proposal
  -- only surfaced once a real end-to-end blueprint-call render test
  exercised exactly that shape. Fixed with `creatingProposalFor` (walks
  `core.Ledger.ProposalsForAddress`, oldest-first, to find the one
  proposal whose own `Delta.Creates` actually contains the address);
  `crossPins` deliberately stays on the ORIGINAL "latest touch" source,
  since a cross-stack pin genuinely IS re-recorded on every resolve/ship
  that reads it, unlike `depends_on`/`sources`, which only ever exist on
  the create itself.

  **Hermetic tests**: `cli/blueprint_provenance_test.go` (new -- a real
  shipped-create ledger fixture via `core.Accept`/`BeginApply`/
  `SaveApplyProgress`/`SealApply`, matching `core/ubi29_test.go`'s own
  pattern, confirms `ubx why` renders the blueprint source line, the
  honest dual-signature note, and the real acceptance line, both by
  bare proposal-id and by address; an ordinary resource with no
  `sources` renders unchanged); `diagram/emit_test.go` (three new tests:
  two blueprint-sourced resources group under one container with
  container-qualified edges; an ordinary resource in a mixed stack stays
  top-level; a stack with zero blueprint resources produces zero
  container syntax); `cli/render_blueprint_test.go` (new -- a real
  diagram-medium blueprint call, real `ExpandCalls` invocation, a real
  Go-only locally-built package, ships two real resources through
  `fakeprovider` end to end and confirms the render grouping -- the exact
  test that caught the `creatingProposalFor` bug above, before any real
  AWS leg was attempted).

  **Live verification, the ticket's own required bar, genuinely met
  against real signed data from a real playground stack**: the real
  CI-platform blueprint (ECR+SQS+IAM role+policy+attachment, the same
  blueprint content Slices 2/4/5 already live-verified), called once more
  for the `payments` stack via the SAME real `.md`-drafted `blueprint_
  calls` document Slice 5's own real-AWS leg produced (re-resolved fresh
  against today's Slice-6-carrying binary rather than re-drafted --
  the draft itself was already real and unchanged, only the resolve
  needed to be a fresh run) -- resolved against the REAL
  `hashicorp/aws@6.54.0` provider's own schema (`payments: 5 create(s),
  0 change(s), 0 terminate(s)`, all five creates carrying the identical
  `{"kind": "blueprint", "ref": "ci-platform:sha256:f893af6e945f..."}`
  entry), accepted into a fresh real ledger
  (`~/ubx-playground-ubi74-slice6/ledger`, change id
  `6ece75b09ee254fd00db903d5a15932e2571338f66d4399bf69754a73afe05d9`) --
  then `ubx ship` itself run by the founder (per CLAUDE.md's own standing
  rule), all five resources shipped clean. `ubx why` against both the
  bare proposal id and the `payments.aws_ecr_repository.container-repo`
  address confirmed the real provenance chain -- the blueprint ref, the
  honest dual-signature note, the real `accepted by [roozbeh] via local
  at ...` line, and (address form) the real ship history. `ubx render
  --stack payments` confirmed all five real resources correctly grouped
  under one `bp0` container with real AWS ARNs in their tooltips and the
  real `depends_on` edges (the IAM policy's own real references to the
  ECR repo and SQS queue) drawn between container-qualified paths. The
  playground ledger is left for the founder's own `ubx terminate` (per
  standing doctrine) once this session's verification is recorded.

  Full test suite green (`go test ./... -count=1`), `gofmt -l .`/`go vet
  ./...` clean. `make build` run and `ubx version` checked before every
  live verification step. `mint validate`/`mint broken-links` both clean
  on `ubiquex-docs`. Committed this session -- see STATE.md.

  **Docs debt, NOT closed this session (flagged, not silently skipped)**:
  ubiquex-docs has never had a dedicated blueprints guide across ANY of
  Slices 1-6 -- `cli/why.mdx` and `cli/render.mdx` gained scoped sections
  for this slice's own new behavior (blueprint provenance, dashed-border
  grouping), but the full calling-convention/multi-language/package-pull-
  verify/cross-medium story (Slices 1-5) remains undocumented in
  ubiquex-docs entirely, a pre-existing gap this session did not
  originate and a full write-up of which is out of this slice's own
  scope.

- 2026-08-05 (UBI-74 Slice 7): built and live-verified against real
  `ghcr.io`, per "OCI push/pull: Slice 7" above. `blueprint/oci.go` (new
  -- `Push`, `pushToTarget`/`pullFromTarget`, `pullOCI`,
  `newAuthenticatedRepository`, `stripOCIScheme`, `extractTarGz`,
  `manifestFromTarball`, `soleFile`), `blueprint/pull.go` (`Pull`'s new
  `oci://` dispatch branch, ahead of the existing local/git checks), and
  `cli/blueprint.go` (`newBlueprintPushCmd`, `newBlueprintPullCmd`'s
  updated doc comment) match the design above.

  **Confirmed before writing any production code, not assumed**: the
  founder's own real `docker login ghcr.io` credential genuinely works
  (re-run, "Login Succeeded," reusing the cached credential -- a real
  check, not just trusting the handoff's own claim) and a real pull
  against a nonexistent GHCR tag returns "not found" (not "denied") --
  the actual signal that distinguishes "auth is fine, the tag just
  doesn't exist" from "auth is broken." `oras-go/v2`'s real current API
  surface confirmed via `go doc` against its own actual downloaded
  source, twice over: once by tracing the full local push/pull mechanics
  through a real on-disk `oci.Store` (no network at all), once by
  confirming `credentials.NewStoreFromDocker` genuinely resolves the
  founder's real GHCR credential (`Username: roozbehshafiee, PasswordSet:
  true`) -- both as standalone throwaway programs, before either
  mechanism was wired into `blueprint/oci.go` itself.

  **One real correction along the way**:
  `github.com/oras-project/oras-credentials-go` (the credential-helper
  package memory would have reached for first) turned out to be
  deprecated in favor of functionality now built directly into
  `oras-go/v2` itself (`oras.land/oras-go/v2/registry/remote/
  credentials`) -- caught via that package's own `go doc` output stating
  so explicitly, not discovered after the fact; the dependency was added,
  then dropped again (`go mod tidy`) once the non-deprecated path was
  confirmed to provide the identical `NewStoreFromDocker`/`Credential`
  functionality.

  **Design choice, stated explicitly**: `pushToTarget`/`pullFromTarget`
  are deliberately split out as target-agnostic halves of `Push`/
  `pullOCI` (taking any `oras.Target`/`oras.ReadOnlyTarget`, not
  hardcoding `*remote.Repository`) specifically so the real ORAS
  mechanics -- manifest construction, blob tagging, the actual `oras.Copy`
  DAG walk -- are hermetically tested against `oras-go`'s own real local
  target implementation (`content/oci.Store`, a real on-disk OCI image
  layout), never a hand-rolled fake standing in for registry behavior.
  Only the thin `newAuthenticatedRepository` wrapper (building a real
  `*remote.Repository` + Docker-credential-backed `auth.Client`) is
  untested hermetically -- covered instead by this slice's own required
  live GHCR round trip.

  **Hermetic tests**: `blueprint/oci_test.go` (`stripOCIScheme`,
  `extractTarGz` round-tripping a real `Package`-produced tarball AND
  rejecting a hand-crafted path-traversal entry, `manifestFromTarball`'s
  own missing-lockfile refusal, `soleFile`'s own zero/multiple-file
  refusals, `pushToTarget`/`pullFromTarget`'s full round trip against a
  real local `oci.Store` with `Verify` confirming the pulled result, and
  a direct check that the pushed OCI manifest's own raw JSON carries the
  `content_hash`/name annotations); `cli/blueprint_oci_test.go` (CLI-level
  flag validation -- missing `--to`, a `--to` missing the `oci://` scheme,
  an unpackaged tarball, `--ref`/`--path` refused against an `oci://`
  source, a tagless `oci://` reference -- all hermetic, refused before any
  network attempt).

  **Live verification, the ticket's own required bar, genuinely met
  against a real OCI registry**: the real `ci-platform` blueprint (the
  identical directory proven live across Slices 1-6,
  `~/ubx-playground-ubi74-slice4/ci-platform/`) packaged fresh -- content
  hash `sha256:f893af6e945fe1e708af03dd60fe5372b76969579b3bdc8b70
  c3b4238968c885`, the EXACT same hash Slice 6's own real render output
  already stamped onto real shipped AWS resources, confirming this is
  genuinely the same proven content -- and pushed for real to
  `ghcr.io/ubiquex/ci-platform:v1`. Independently confirmed landed via a
  real `docker manifest inspect ghcr.io/ubiquex/ci-platform:v1` (not just
  trusting `ubx blueprint push`'s own success message): real manifest
  JSON, `artifactType: "application/vnd.ubx.blueprint.v1"`, one layer at
  `application/vnd.ubx.blueprint.v1.tar+gzip`, the
  `dev.ubiquex.blueprint.content_hash` annotation visible natively and
  matching. Pulled back via `ubx blueprint pull
  oci://ghcr.io/ubiquex/ci-platform:v1` into a genuinely separate
  directory (`~/ubx-playground-ubi74-slice7/pulled`) -- `ubx blueprint
  verify` confirmed the content hash matches (11 files, identical hash),
  and a real `go build ./...`/`go vet ./...` against the pulled copy's
  own `go/` subdirectory succeeded cleanly -- real network, the actual
  published `github.com/ubiquex/ubx-sdk-go` module, no local `replace`
  directive, the identical bar Slice 3 met for git, now met for a real
  OCI registry. `ghcr.io/ubiquex/ci-platform:v1` is left published
  deliberately -- this slice's own real deliverable, not a transient test
  resource requiring teardown (unlike a real-AWS playground stack).

  Full test suite green (`go test ./... -count=1`), `gofmt -l .`/`go vet
  ./...` clean. `make build` run and `ubx version` checked before the live
  verification above. Committed this session -- see STATE.md.

- 2026-08-05 (UBI-74 Slice 8): built and live-verified against real
  `ghcr.io` -- the FINAL slice of UBI-74's original eight-slice plan, per
  "Offline delivery + redistribution: Slice 8" above. `blueprint/pull.go`
  (`Pull`'s new bare-tarball-file branch, slotted into the exact gap the
  existing `os.Stat`/`IsDir` dispatch already implied) and
  `cli/blueprint.go` (`newBlueprintPullCmd`'s updated doc comment) match
  the design above. No new production code was needed for the re-tag/
  mirror redistribution case at all -- it's `blueprint.Push` (Slice 7)
  called a second time against a second `--to`, proven correct by
  composing already-built mechanisms, not by inventing a new one.

  **A real, live-found subtlety, not predicted in advance**:
  `content_hash` turns out to be a function of the blueprint's own
  declared `name` (derived from `filepath.Base` at `Package` time), not
  purely file content -- re-packaging the real `ci-platform` blueprint
  into a directory named `fresh-from-ghcr` (rather than `ci-platform`)
  produced a genuinely DIFFERENT hash from the original, purely from the
  rename, caught live during this session's own required verification
  (not a hermetic test) before it could be mistaken for a real content
  discrepancy. Documented explicitly above as a real, easy-to-trip-on
  operational detail for anyone re-exporting a blueprint for offline
  delivery -- not a bug (`name` genuinely identifying WHICH blueprint
  this is is the correct design, `ubx why`'s own `ref` format already
  depends on it), but worth naming rather than leaving as a silent
  surprise.

  **Design choice, stated explicitly**: fork-with-modification (the
  design record's own pattern 1) is designed in full above (a new
  `Manifest.ForkedFrom` field, a new `ubx blueprint fork` command
  reusing `Pull` unchanged, a parallel `ubx why` rendering line) but NOT
  built this session -- an explicit stretch goal per this slice's own
  scope, named rather than silently left unaddressed. Alias/pointer
  redistribution (pattern 3) wasn't attempted at all -- the re-tag/mirror
  case already proves the load-bearing "trust survives redistribution"
  property this slice's own success bar required; an alias/pointer's own
  value is purely storage-cost, not a new trust property.

  **Hermetic tests**: `blueprint/pull_test.go` (four new tests -- a real
  tarball pulled by file path with `HTTP_PROXY`/`HTTPS_PROXY` pointed at
  an unreachable address, proving zero network dependency empirically;
  `--ref`/`--path` refused; a real gzipped tarball with no `Ubxfile`
  refused; a tarball tampered with AFTER packaging extracts cleanly but
  fails `Verify`); `blueprint/oci_test.go`
  (`TestMirrorRedistribution_ContentHashSurvivesToASecondLocation` -- the
  SAME tarball pushed to TWO independent local `oci.Store` targets,
  `Verify` against either reporting the identical original hash);
  `cli/blueprint_offline_test.go` (CLI-level package -> pull(bare file)
  -> verify round trip, `--ref` refusal).

  **Live verification, the ticket's own required bar, genuinely met**:
  the real `ci-platform` blueprint pulled fresh from
  `ghcr.io/ubiquex/ci-platform:v1`, re-packaged into a standalone tarball
  -- content hash `sha256:f893af6e945fe1e708af03dd60fe5372b76969579b3bdc8
  b70c3b4238968c885`, the EXACT original hash, confirming a genuinely
  byte-identical export. Pulled that bare tarball FILE with
  `HTTP_PROXY`/`HTTPS_PROXY` pointed at an unreachable address --
  succeeded, real proof of zero network dependency. `ubx blueprint
  verify` confirmed the identical hash; a real `go build`/`go vet`
  against the offline-pulled copy's own `go/` subdirectory succeeded
  cleanly (real network for module resolution only, no local `replace`).
  Then the real mirror: the same tarball pushed to
  `ghcr.io/ubiquex/ci-platform-mirror:v1` -- `docker manifest inspect`
  against BOTH locations confirmed an IDENTICAL blob digest (not merely
  an identical `content_hash` annotation), and `ubx blueprint verify`
  against a copy pulled from the mirror confirmed the identical original
  content hash -- content-hash-based trust genuinely surviving
  redistribution to a real, independent second location.

  Full test suite green (`go test ./... -count=1`), `gofmt -l .`/`go vet
  ./...` clean. `make build` run and `ubx version` checked before the
  live verification above. Committed this session -- see STATE.md, which
  also carries the required closing retrospective across all 8 slices.

- 2026-08-05 (UBI-126): fixed and live-verified -- per "Provenance parity
  across all three calling mediums: UBI-126" above. `sdk/go/runtime/
  runtime.go` (`intentResource.Sources`, `PushBlueprintSource`/
  `PopBlueprintSource`, `addResource`'s own stamping), `blueprint/
  gogen.go` (`renderGoFunction` wraps every generated function body),
  `blueprint/sdkprovenance.go` (new -- `StampDirectCallProvenance`,
  `discoverImportedBlueprints`, `pendingBlueprintNames`), `blueprint/
  invoke.go` (`invokeCall`'s own stamping loop now REPLACES rather than
  appends; `writeGoCaller`/`extractReplaceLine` carry forward an
  optional sdk-go `replace`), and `cli/resolve.go` (the new `--from-code`
  splice, Go entries only) match the design above.

  **A real regression found and fixed before it shipped wrong, not
  after**: the FIRST full-suite run after the core fix landed failed a
  PRE-EXISTING test (`TestRender_BlueprintCall_GroupsInDashedContainer_
  RealFakeProvider`, Slice 6/7) with `undefined: sdk.PushBlueprintSource`
  -- `ExpandCalls`'s own synthesized Go caller resolves `ubx-sdk-go`
  however the blueprint's own go.mod says, with NO local override,
  meaning it always pointed at the REAL PUBLISHED module (stale, lacking
  this session's own unpublished sdk/go changes) regardless of what a
  direct-import caller's own separate go.mod carried. Traced to root
  cause (`writeGoCaller` never copies a blueprint's own optional
  `replace` line, only its `require`) before attempting any fix, and
  fixed there specifically, not by loosening the underlying design (which
  correctly keeps production blueprints resolving the real published
  SDK, never a local override). A SECOND regression surfaced once that
  one was fixed: the SAME render test now failed a DIFFERENT way (the
  rendered container was labeled bare `"platform"`, no hash) --
  `invokeCall`'s own stamping loop was APPENDING its own complete ref
  alongside the new SDK-level bare tag rather than replacing it, so
  `sources[0]` (what `ubx render`'s own container label reads) picked up
  the wrong, incomplete one. Both caught by the full suite, in order,
  before either could reach a commit.

  **Hermetic tests**: `cli/blueprint_call_test.go` (two new tests --
  resolved-JSON-level and real-shipped-ledger-level proof, the latter
  literally running `ubx why`/`ubx render` against a real fakeprovider
  ship); `blueprint/invoke_test.go`
  (`TestExpandCalls_ProvenanceStamped_Go`, closing a real pre-existing
  test-coverage gap -- TS and Python each had their own sibling, Go
  never did, and Go specifically is where the double-tagging regression
  above lived); `cli/blueprint_cross_medium_test.go`
  (`TestBlueprintCrossMedium_ProvenanceIdentical`, using one shared
  on-disk blueprint across all three legs -- deliberately NOT reusing the
  sibling delta-shape test's own separately-built SDK-leg copy, since a
  byte-identical PROVENANCE comparison specifically needs the exact same
  content hash, which two independently-built copies of identical
  content don't guarantee).

  **Live verification**: the real `ci-platform` blueprint (`ubx blueprint
  build`, a real Claude draft) rebuilt with this fix in place, confirmed
  to emit the real `sdk.PushBlueprintSource("ci-platform")` wrapping.
  `TestBlueprintCall_DirectSDKImport_WhyAndRenderShowProvenance` itself
  is a real, live, end-to-end run (real resolve/accept/ship against
  `fakeprovider`, this project's own sanctioned real-cloud stand-in) --
  captured real, unedited `ubx why`/`ubx render` output showing full
  blueprint provenance on a direct-SDK-imported resource for the first
  time in this arc's own history.

  Full test suite green (`go test ./... -count=1`, both this repo and
  `sdk/go`'s own separate module), `gofmt -l .`/`go vet ./...` clean.
  `make build` run and `ubx version` checked before live verification.
  Committed this session -- see STATE.md.

  **Not done this session, named so it isn't assumed covered**: TS/
  Python's own direct-import calling paths still have the ORIGINAL
  gap (no provenance mechanism at all) -- a real, separate, honestly
  named follow-up, not attempted here. `sdk/go`'s own changes are NOT
  yet published to the real `github.com/ubiquex/ubx-sdk-go` repo -- every
  real blueprint built against the real published SDK today still lacks
  `PushBlueprintSource`/`PopBlueprintSource` until that publish happens
  (a separate release process this session didn't trigger); every test
  and live-verification run above used a local `replace` to this repo's
  own `sdk/go`, matching this project's own established hermetic-testing
  convention, not a claim that the real published module already has
  this fix.

## Override mechanism: UBI-86 Part 2

Design comment on UBI-86 (2026-08-04), extending the ticket originally
scoped for `ubx render --md` alone (see `docs/render-md.md` for that
half). The real problem, precisely stated: a blueprint's own internal
resource attributes are owned by the blueprint's AUTHOR, not the caller
— only call-site parameters are editable by whoever instantiates it. If
a caller drifts (via console/CLI) a value the blueprint never exposed as
a parameter and adopts it, the ledger is correct but there's no
source-of-truth text the caller owns to edit, so that adoption can't
"stick" against a future re-call of the same blueprint.

### The mechanism: same idea as Terraform's `*_override.tf`

`override(address, {field: value, ...})` — a caller patches any resolved
attribute by address, applied AFTER a blueprint call resolves
(`blueprint.ExpandCalls`), BEFORE `resolver.Resolve` ever runs. Three
call sites, matching `BlueprintCalls`' own "three mediums, one wire
shape" precedent exactly:

- **SDK (Go/TS/Python), zero AI** — a direct function call:
  `sdk.Override("payments.aws_sqs_queue.pipeline-events", map[string]any{"some_hardcoded_field": "new_value"})`
  (`sdk/go/runtime`, `sdk/ts/runtime`, `sdk/py/ubx_sdk` — all three,
  identical shape, `config`'s own keys are the target's real WIRE
  attribute names, never translated through any `ResourceBinding`'s own
  `FieldMap`, since there is none in scope at override time — the target
  need not even be declared in the calling document at all).
- **Diagram, zero AI, structural attribute read** — a `ubx_override`-
  classed node (`diagram/parse.go`), mirroring `ubx_blueprint`'s own
  reserved-class/direct-children-read pattern exactly (not `ubx_required`'s
  nested-subtree dance — an override node has nothing else on it to be
  ordinary topology, the same reasoning `ubx_blueprint` already
  established):
  ```d2
  fix: "fix pipeline-events" {
    class: ubx_override
    address: "payments.aws_sqs_queue.pipeline-events"
    some_hardcoded_field: "new_value"
  }
  ```
  `address` is the one reserved attribute; every other child is one
  config field. A config value can reference a sibling node via the SAME
  `"ref:<node>.<attr-path>"` sigil `ubx_required` already established
  (UBI-95) — reused, not reinvented. A real, inherited scope boundary:
  since every non-ref value is JSON-string-encoded (matching
  `ubxRequiredAttrValue`'s own established rule), the diagram medium's
  own override syntax only expresses STRING-valued overrides — a
  number/bool/object drift target needs Go/TS/Python instead. Named
  here, not silently worked around.
- **md, the ONLY call site needing AI, thin mapping only** —
  `intentprovider/schema.go`'s own JSON Schema gained an `overrides`
  array (mirroring `blueprint_calls` exactly) and
  `intentprovider/claude/adapter.go`'s own system prompt gained an
  explicit rule: "Override the pipeline-events queue's
  some_hardcoded_field to new_value" maps to one `overrides[]` entry,
  never a re-draft of the target resource, never touching any other
  attribute of it. Hermetically tested (`intentprovider/validate_test.go`);
  not live-round-tripped through a real Claude API call this session — a
  real, named gap, not silently claimed covered.

### Wire shape and application

`resolver.Override{Address, Config map[string]json.RawMessage}`, a new
`IntentFile.Overrides []Override` field, additive and optional exactly
like `BlueprintCalls`. `blueprint.ApplyOverrides` (new,
`blueprint/override.go`) merges each entry into its target
`ResourceIntent.Config` **by address, found in `intent.Resources` —
i.e. a resource this SAME document creates** (a blueprint call's own
just-expanded output, or an ordinary hand-authored create) — then clears
`Overrides`, called immediately after `blueprint.ExpandCalls` in both
`cli/resolve.go` and `cli/plan.go` (the latter previously never called
`ExpandCalls` at all — a pre-existing gap, closed here since the
override round trip needs to work via `ubx plan` too). Applied to the
RAW (unresolved) config, before `resolver.Resolve` runs — an override's
own value flows through ordinary `$ref`/`$cross` resolution like any
other config value, never a bolted-on post-resolve patch (confirmed live:
`sdk.Override("...", map[string]any{"owner_ref": primary})` — a real
`*Computed` reference — resolves to a real `$ref` object end to end).

`Config`'s own keys are TOP-LEVEL wire attribute names only, never a
dot-path into a nested value — overriding one key of a nested object
(a `tags` map, say) means supplying that whole top-level attribute's own
complete new value, never a deep merge.

### A real, named, remaining scope boundary — found during this
### ticket's own required live verification, not assumed away

`ApplyOverrides` only ever targets a resource **created within the same
resolving document** — a blueprint call's own just-expanded output, or a
fresh hand-authored create. It does **not** retroactively patch an
address that already exists in the ledger from a PRIOR resolve/ship.
Confirmed live: `ubx plan --from-diagram` against a diagram naming only
an `ubx_override` node (no matching resource node) for an
already-shipped address fails cleanly — `override "...": no such
resource in this document` — never silently no-oping.

This matches the mechanism's own real motivating framing on closer
reading of the design comment: "make that adoption stick against a
FUTURE RE-CALL of the blueprint" is forward-looking insurance (the
override travels with the calling stack's own source, so the NEXT time
this document creates that resource — a fresh stack, a disaster-recovery
rebuild, a sibling environment — the drifted-to value is baked in from
the start), not a retroactive ledger patch for an already-shipped
resource. `render --sync-overrides`' own required live round trip
(below) proves exactly this shape: terminate the drifted resource, then
recreate it via a document carrying the override, confirmed clean
afterward — never a same-address in-place patch.

A real, separate, deferred extension this session identified but did
NOT build: making `ApplyOverrides` ledger-aware (accepting a
`*core.Ledger`, synthesizing an `op: modify` `ResourceIntent` — current
state merged with the override's own patch — for an address found in
the ledger but not in `intent.Resources`) would close this gap for real,
letting an override apply in-place without a terminate/recreate cycle
first. Real, buildable, not attempted here — named rather than silently
assumed to already work.

### REAL SECURITY REQUIREMENT: policy-bypass protection, documented, not yet enforced

An override must not be able to bypass a field a blueprint's own BOUND
POLICY protects (UBI-118's own future job). UBI-118 (the bound + org-wide
policy engine) was split off UBI-74 entirely before Slice 1 even started
and remains HELD — as of this ticket, zero policy-engine scaffolding
exists anywhere in this codebase (`core/`, `blueprint/`) to enforce
against. Per this ticket's own explicit instruction ("this may mean
documenting the requirement and adding a TODO enforcement hook rather
than full enforcement — decide and state explicitly which"): **decided
here as documenting + a real, named, currently-permissive hook**,
`blueprint.checkOverridePolicy(addr, field) error` (`blueprint/override.go`)
— the ONE call site every field-level override ever routes through,
always returning `nil` today. Wiring in real enforcement once UBI-118
exists is a one-function change, not a redesign.

### `render --sync-overrides`/`--from-drift`: mechanical generation, zero AI

`ubx render --md --from-drift <address>` generates a single override
statement for one drifted resource; `ubx render --md --sync-overrides
[--write]` walks the WHOLE stack's drift and generates one per drifted
resource, in whichever medium the calling stack is authored in
(auto-detected — reuses `autodetectMedium`, `cli/plan.go`'s own
established file-sniffing, unchanged, not a second detector). Both
require `--md` (the design comment's own literal example command).
Confirmed, as item 10 of the design comment required: **entirely
mechanical, zero AI** — `cli/drift.go`'s `scanDrift` reuses `status
--drift`'s own exact mechanism (`core.RunScan`/`core.DiffAttributes`/
`core.FilterNormalizationNoise`), never a second drift algorithm;
`cli/overridetemplate.go` only templates that already-computed data into
each medium's own grammar (a real, live-found subtlety: a NESTED drifted
path, e.g. `tags.mutated`, is collapsed to its whole CONTAINING
top-level attribute's own complete CURRENT value, pulled from the scan's
own `Observed` state, not just the diffed sub-key — otherwise a
generated override would silently DROP every other key already in a
live `tags` map the moment `ApplyOverrides` replaces it).

`--write` appends the generated statement(s) directly into the calling
stack's own source file for diagram/md authoring (always syntactically
safe to append — a new top-level D2 node, a new prose paragraph) — and
**refuses, clearly, for a Go/TS/Python-authored stack**: the statement
belongs inside the stack's own describe-function body, which this
command has no safe way to splice into without real AST-aware editing
(`writeback/`'s own level of sophistication, not attempted here). A real,
named limitation, never a silent bad write.

### Live verification, the ticket's own required bar, genuinely met

A real fakeprovider-shipped `fake_widget` (conformance mode for the
text-correctness checks; ok-v6 mode — the only fixture mode with real
`PlanResourceChange`/destroy support — for the full apply round trip,
confirmed live: conformance mode's own `ApplyResourceChange` exists but
`PlanResourceChange` does not, so `ubx ship` cannot destroy or otherwise
plan-then-apply against it) drifted for real
(`FAKEPROVIDER_MUTATE_ATTR`/`FAKEPROVIDER_EXTRA_TAG`, confirmed via a
real `ubx status --drift`). `ubx render --md --sync-overrides` generated
the correct statement in **Go** (exact byte match asserted hermetically)
and in **diagram** (round-tripped through the real `diagram.Parse`,
confirmed it parses back into the identical address+value). Applied for
real: `ubx terminate` the drifted resource, `ubx plan --from-diagram`
against a fresh diagram carrying both a baseline resource declaration
and the override, `ubx ship` — the shipped resource's own live value
matched the override exactly (`payments-widget-v2-drifted`, not the
diagram's own baseline value), and a subsequent `ubx status --drift`
showed it clean. A real, live-found, unrelated fixture quirk surfaced
along the way and worked around, not silently absorbed: a resource's own
recorded `resolution.inputs.lookup` from a plain `ubx resolve`/`ubx ship`
create is narrower than its full config (omits `tags` entirely), so ANY
later drift-scan of a plain create-shipped resource shows spurious
"phantom" drift on any attribute the lookup didn't carry — confirmed
live, unrelated to this ticket, worked around here by reconciling
(`ubx scan --propose drift_adopt`) before relying on a clean baseline;
named here since it's a real, pre-existing property of this project's
own ship-time lookup recording, not something UBI-86 introduced or
fixed.

Hermetic tests: `blueprint/override_test.go` (6 cases — merge, no-op,
cross-stack refusal, unknown-address refusal, malformed-address refusal,
targeting-a-blueprint-produced-resource); `sdk/go/runtime/runtime_test.go`
(5 new `TestOverride_*` cases, including a `*Computed` reference value);
`sdk/py/ubx_sdk/test_runtime.py` (6 new); `sdk/ts/runtime/src/index_test.ts`
(6 new); `diagram/parse_override_test.go` (6 cases, including the
`ref:` sigil and mixed-with-blueprint-call-and-resource); `intentprovider/
validate_test.go` (3 new); `cli/render_sync_overrides_test.go` (9 cases —
Go/diagram text correctness, `--write` success and SDK-authored refusal,
`--from-drift` single-address success/not-drifted, `--md` required,
ambiguous-medium refusal, config-file provider default). Full suite
green (`go test ./... -count=1`, this repo, `sdk/go`'s own separate
module, `python3 -m unittest`, `deno test`), `gofmt -l .`/`go vet ./...`
clean.

## Outputs: cross-medium blueprint output references (UBI-128)

**The gap this closes.** Every calling medium (Go/TS/Python SDK import,
diagram `ubx_blueprint`, md's own "Use blueprint... with:" phrasing) could
already CALL a blueprint and let its own internal resources be referenced
INSIDE that blueprint (one resource pointing at a sibling via an ordinary
`$ref`, entirely within `blueprint.ExpandCalls`'s own expansion). What was
missing: a way for something OUTSIDE the blueprint — a sibling resource
the calling stack itself declares — to reference one of the blueprint's
own resource attributes, by a name the blueprint itself chooses to expose,
rather than the caller having to know (and hardcode) the blueprint's own
internal resource slugs and types. `outputs:` is that declaration.

```yaml
# Ubxfile
outputs:
  repo_arn: container-repo.arn
  queue_url: pipeline-events.url
```

Each entry maps a caller-facing output name to `<resource-slug>.<attribute>`
— the SAME internal resource slugs `resources:`'s own prose already fixes
(`ubxfile.go`'s own doc comment), never the `{param_name}` tokens. Parsed
by `blueprint.parseOutputs` (`ubxfile.go`) as an ordered mapping (a raw
`yaml.Node` walk, mirroring `params:`'s own order-preserving parse — never
a plain Go map, whose iteration order this project's own determinism
discipline never allows near anything that reaches codegen or a hash).
Validated for SHAPE only at parse time (`"<slug>.<attr>"`, non-empty
halves, no duplicate output names) — the slug's own EXISTENCE can't be
checked yet, since `ParseUbxfile` has zero resource-type knowledge at all
(`resources:` is free-form prose at that stage); that's checked later,
independently, by whichever consumer actually has real resources in hand
(`decodeBlueprint` for Go/TS/Python codegen, `resolveCallOutputs` for a
diagram/md call — see below).

**Deliberately, structurally distinct from `@stack.type.name` (UBI-47)
cross-stack references — never unified, on purpose.** `@stack` crosses a
real trust boundary: a DIFFERENT team's separately-signed ledger, pinned
to their own current head at accept time, with its own staleness-by-
neighbor-advance model (`ubx accept`'s own pin re-verification). An
output stays entirely WITHIN the same proposal being resolved right now —
no separate ledger, nothing pinned, no staleness concept at all, because
there's nothing separately-signed to go stale relative to: the blueprint
call and the resource referencing its output are both part of the SAME
document, expanded and resolved in the SAME pass. Using `@stack` syntax
for an output, or resolving an output through `@stack`'s own cross-ledger
machinery, would silently claim a trust/staleness property that doesn't
exist here — kept visibly and mechanically separate instead: different
sigil (`ref:`/an ordinary embedded `$ref` vs. `@`), different wire marker
(`$blueprint_output:<CallName>:<outputKey>` vs. a real
`<stack>.<type>.<name>` address), different resolution code path
(`blueprint.ExpandCalls`/`blueprint/outputs.go`, entirely before
`resolver.Resolve` ever runs, vs. `core/resolver/refs.go`'s own
`resolveCross`, which never sees an output reference at all — by the time
`resolveRef`/`resolveCross` run, an output marker has always already been
rewritten into a real address).

### Go codegen: native named return values

`GenerateGo` (`gogen.go`) emits one named `*sdk.Computed` return value per
declared output, in declaration order — Go's own idiomatic collapsed
multi-name-same-type syntax, no wrapper type:

```go
func CiPlatform(repoName string, queueName string, opts ...Option) (repoArn, queueUrl *sdk.Computed) {
	...
	containerRepo := sdk.Resource(ContainerRepo, "container-repo", ContainerRepoConfig{...})
	pipelineEvents := sdk.Resource(PipelineEvents, "pipeline-events", PipelineEventsConfig{...})
	...
	return containerRepo.Field("arn"), pipelineEvents.Field("url")
}
```

A caller uses the return values exactly like any other Go function's —
zero new runtime mechanism, confirmed live (below):

```go
repoArn, _ := ciplatform.CiPlatform("payments-ci-artifacts", "payments-notifications")
sdk.Resource(Downstream, "downstream-attachment", DownstreamConfig{PolicyArn: repoArn})
```

The output-target resource (`container-repo` above) gets a local variable
assigned even if nothing INSIDE the blueprint references it — `decodeBlueprint`
marks an output's own target address `Referenced` (the same flag an
ordinary internal `$ref` sets), so `renderGoFunction`'s existing "only
assign a local var to a referenced resource" logic picks it up for free,
no separate codegen path. `checkGoOutputIdentCollisions` guards each
output's own derived camelCase identifier against every other identifier
this same file derives (resource vars, `Config` struct names, param
names, the functional-options `cfg`/`opts` locals) and against each other
— a hard build error, never silently renamed around.

### TypeScript codegen: a plain object literal

TS has no separate named-return-value construct — a plain object literal
IS the native multi-value return:

```ts
export function ciPlatform(repoName: string, queueName: string, retentionDays: number = 1): { repoArn: any; queueArn: any } {
  ...
  const ciArtifacts = resource(CiArtifacts, "ci-artifacts", { ... }) as any;
  ...
  return { repoArn: ciArtifacts.arn, queueArn: ciNotifications.arn };
}
```

Unlike Go's named return VALUES (real declared variables sharing the
function body's own scope), an object-literal key is never a new
identifier declared into scope — so a TS output can't collide with a
resource/param identifier the way a Go one can; the only real risk left
is two DIFFERENT outputs deriving the same camelCase key (a silent
last-write-wins object literal), guarded separately. Confirmed with a
real `deno check` (the outputs-bearing return type itself typechecks) and
a real `deno run` driver that destructures `{ repoArn }` from the call
and passes it straight into another `resource()` call.

### Python codegen: a bare value or a native tuple

Python's own multi-value return is a plain tuple — no wrapper class
needed there either. Exactly one output returns a bare value (unpacked
`x = f(...)`, matching Go's own single-named-return ergonomics); two or
more return a real tuple (unpacked `x, y = f(...)`):

```python
def ci_platform(repo_name: str, queue_name: str, retention_days: int = 1) -> tuple[Any, Any]:
    ...
    ci_artifacts = sdk.resource(CiArtifacts, "ci-artifacts", CiArtifactsConfig(...))
    ...
    return ci_artifacts.arn, ci_notifications.arn
```

Confirmed with a real Python import (the `tuple[Any, Any]` return-type
annotation itself needs no `from __future__ import annotations` on the
pinned CPython this project targets — checked live, not assumed) and a
real driver that unpacks the tuple and uses `repo_arn` directly as
another `sdk.resource()`'s own config value.

### Diagram: the EXISTING `ref:` sigil, extended — never a second mechanism

A blueprint call node's own CallName (`resolver.BlueprintCall.CallName`,
new field) is, for the diagram medium, always its bare D2 identifier —
implicit, the SAME identifier `ref:` already resolves an ordinary sibling
resource by. `ref:<blueprint-node>.<output-key>` reuses `ubxRequiredAttrValue`/
`resolveRefTarget` (`diagram/parse.go`) completely unchanged in shape —
only the resolved `to` value differs: a real
`<stack>.<type>.<name>.<attr>` address for an ordinary resource, or the
provisional `$blueprint_output:<CallName>:<outputKey>` marker for a
blueprint-call node (a new `blueprintCallRefTargetType` sentinel lets
`resolveRefTarget` tell the two apart without a second reference-parsing
path).

```d2
platform: "ci-platform call" {
  class: ubx_blueprint
  blueprint: "../ci-platform"
  repo_name: "payments-ci-artifacts"
  queue_name: "payments-notifications"
}
downstream: "downstream attachment" {
  class: aws_iam_role_policy_attachment
  ubx_required.policy_arn: "ref:platform.repo_arn"
  ubx_required.role: "downstream-role"
}
```

**A real, pre-existing bug found and fixed by this ticket's own required
live verification, not a unit test:** `sortedLeaves`'s existing
`hasClass(obj.Classes, ubxBlueprintClass)`/`ubxOverrideClass` branches only
ever matched the CLASSED NODE ITSELF — a `ubx_blueprint`/`ubx_override`
node's own attribute children (`platform.blueprint`, `platform.widget_name`,
...) are SEPARATE `*d2graph.Object` values in the underlying graph, with
no class of their own, so they fell through to the ordinary "no `class:`
attribute" leaf classification and were refused as unresolved topology
nodes — a spurious BLOCKING `Question` on every diagram ever using either
node kind, since the day Slice 5/UBI-86 Part 2 shipped. Silently
undetected until now because every prior test asserted only that
`intent.Resources` stayed empty (true either way — `nodeKindUnresolved`
never appends to `Resources` either), never that `intent.Intent.Questions`
was ALSO empty. Fixed with a new `inBlueprintOrOverrideSubtree` check
(walks `obj.Parent` upward looking for either class), added to
`sortedLeaves` alongside the existing `ubx_required`-subtree exclusion;
regression-tested directly (`TestParse_UbxBlueprint_ChildrenNeverIndependentlyClassified`/
`TestParse_UbxOverride_ChildrenNeverIndependentlyClassified`,
`diagram/parse_output_test.go`).

### md: a real, new grammar — "Call blueprint X as 'name' with:"

Honestly flagged as real new scope in the ticket itself, and it is:
nothing like this existed before UBI-128. `wireBlueprintCall`
(`intentprovider/validate.go`) gains `call_name` (wire name, JSON key
`call_name`); the structured-output schema's own `blueprintCall` node
(`intentprovider/schema.go`) gains a matching required-but-may-be-empty
`call_name` string property. The system prompt
(`intentprovider/claude/adapter.go`) gains two new paragraphs, immediately
after the existing blueprint-call rule:

1. Recognize an explicit `as '<name>'`/`as "<name>"` alias clause on the
   SAME sentence as a blueprint call, and extract it verbatim into
   `call_name` — explicitly distinguished from the pre-existing `name`
   field (a free-text label for error messages only; `call_name` is a
   real identifier other prose can reference). Empty string when no such
   alias is given — never invented, never a fallback to `name`.
2. Recognize LATER prose referencing that alias's own output — "platform's
   own repo_arn output," or similar — and emit the IDENTICAL
   `{"$ref": {"to": "..."}}` object an ordinary `@<address>` reference
   already uses (including one level inside a JSON-embedded string, the
   same embedding rule), but with `to` set to the literal string
   `"$blueprint_output:<call_name>:<output_key>"` instead of a
   `<stack>.<type>.<name>.<attr>` address.

No change needed to how a resource's own `config` is validated at all —
`config`/`args` stay a JSON-encoded string all the way through
`parseAndValidate`, exactly as opaque to the md-medium layer as an
ordinary `$ref` already is; the marker is only ever interpreted later, by
`blueprint.ExpandCalls`. `validate.go` gained one new check specific to
`call_name`: two calls in the same document sharing one alias is a hard,
named validation error (`blueprint_calls[N].call_name: ... is also used
by blueprint_calls[M]`) — `blueprint.ExpandCalls` aggregates every call's
own outputs into one document-wide map keyed by `call_name`, so a silent
collision there would produce a wrong, last-write-wins output resolution
with no clear cause; caught here instead, at draft time.

```md
# Platform CI, via a named blueprint call

Call blueprint `ci-platform` as `platform` with: repo_name =
payments-ci-artifacts, queue_name = payments-notifications.

We also need an IAM role policy granting a downstream service access to
that repository -- attach a policy to the `downstream-role` role using
platform's own `repo_arn` output as the `Resource` in an inline policy
granting `s3:GetObject`.
```

A real, live Claude Sonnet 5 run against this exact document (below)
produced, on the FIRST attempt, no retry needed:

```json
{
  "blueprint_calls": [
    {"name": "Platform CI blueprint call", "call_name": "platform", "blueprint": "ci-platform",
     "args": {"repo_name": "payments-ci-artifacts", "queue_name": "payments-notifications"}}
  ],
  "resources": [
    {"type": "aws_iam_role_policy", "name": "downstream-role-repo-access", "op": "create",
     "config": {"name": "downstream-role-repo-access", "role": "downstream-role",
       "policy": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":\"s3:GetObject\",\"Resource\":{\"$ref\":{\"to\":\"$blueprint_output:platform:repo_arn\"}}}]}"}}
  ]
}
```

### Hermetic tests

`blueprint/ubxfile_test.go` (5 new — `outputs:` parsing, no-outputs
no-op, duplicate-output refusal, malformed-target refusal, non-mapping
refusal); `blueprint/gogen_test.go` (4 new — signature/return,
no-outputs unaffected, `go build` compiles clean, a real `go run`
proving the return value usable by a sibling resource); `blueprint/
outputs_test.go` (9 unit tests for `resolveCallOutputs`/
`rewriteBlueprintOutputRefs` plus a real end-to-end
`blueprint.ExpandCalls` integration test against a real on-disk Go
blueprint package); `blueprint/tsgen_output_test.go` (5 new, including a
real `deno check` and a real `deno run` proving runtime usability);
`blueprint/pygen_output_test.go` (5 new, including a real Python import
and a real driver proving runtime usability); `diagram/parse_output_test.go`
(9 new — `ref:` resolving a blueprint-call output for both `ubx_required`
and `ubx_override`, nested-in-container resolution, the CallName-is-
bare-ID rule, the `sortedLeaves` regression fix above, still-refused for
a genuinely unknown identifier); `intentprovider/validate_test.go` (3
new — `call_name` decoding, empty-by-default, duplicate-`call_name`
refusal); `intentprovider/conformance/` (1 new fixture,
`platform-blueprint-output.md` + `checkPlatformBlueprintOutputNamedCall`,
run through the full hermetic fake-adapter harness). Full suite green
(`go test ./... -count=1`), `gofmt -l .`/`go vet ./...` clean.

### Live verification, the ticket's own required bar, genuinely met — per-medium depth stated explicitly, not blurred

**Go SDK — live, twice, against two real backends:**

1. The real `ci-platform` blueprint (`~/ubx-playground-ubi74-slice4/ci-platform`,
   the same blueprint every prior UBI-74 session used), extended with a
   real `outputs:` block (`repo_arn: container-repo.arn`,
   `queue_url: pipeline-events.url`), rebuilt via a real
   `ubx blueprint build --lang go` (a real, live Claude Sonnet 5 draft
   call). A hand-written Go stack program imported the built package,
   called `CiPlatform(...)`, and passed the returned `repoArn` DIRECTLY
   as another resource's own config value — resolved via a real
   `ubx resolve --from-code` against the real, live
   `hashicorp/aws@6.54.0` schema (resolve-only, never `ubx ship`, per
   this project's own standing rule against a real cloud apply). The
   resolved proposal's own `downstream-attachment.policy_arn` correctly
   showed `{"$computed":{"from":"payments.aws_ecr_repository.container-repo.arn"}}`.
2. A separate, minimal `fake_widget`-typed `widget-lib` blueprint (the
   real `ci-platform` blueprint's own resources are real AWS types
   `fakeprovider` has no schema for at all — the same substitution
   UBI-130's own live verification already established, for the
   identical reason) with a real `widget_id: primary-widget.id` output,
   also built via a real `ubx blueprint build --lang go`. A real, full
   `ubx plan --from-code` → `ubx accept` → `ubx ship --yes` round trip
   against a real `fakeprovider` subprocess (`UBX_PROVIDER_MIRROR`,
   `FAKEPROVIDER_MODE=ok-v6`) — the downstream widget's own `name`
   attribute, the blueprint's own returned `widgetId` used directly,
   shipped as `{"$computed":{"from":"payments.fake_widget.primary-widget.id"}}`,
   confirmed via a real `ubx why`.

**Diagram — live, twice, mirroring the Go SDK's own two legs exactly:**

1. `ref:platform.repo_arn` against the real `ci-platform` blueprint and
   the real `hashicorp/aws@6.54.0` schema, via a real
   `ubx plan --from-diagram` (resolve/plan only, never `ubx ship`, same
   real-AWS-provider rule as the Go SDK leg above) — the resulting
   `downstream attachment.policy_arn` correctly showed
   `$ref:payments.aws_ecr_repository.container-repo.arn`, zero blocking
   questions (confirming the `sortedLeaves` fix above holds against the
   real blueprint, not just the widget-lib fixture).
2. `ref:platform.widget_id` against the widget-lib substitute, a real,
   full `ubx plan --from-diagram` → `ubx accept` → `ubx ship --yes`
   round trip against real `fakeprovider` — `downstream widget.name`
   shipped as `{"$computed":{"from":"payments.fake_widget.primary-widget.id"}}`,
   confirmed via a real `ubx why`.

**TypeScript SDK — live, via the real `deno` toolchain (not a stub):** a
real `deno check` against the outputs-bearing generated module, and a
real `deno run` driver that calls the generated function, destructures
its own returned outputs, and passes one directly into another
`resource()` call — the emitted document's own `$ref` resolved to the
correct address. NOT run against a real cloud provider or fakeprovider
apply (TS has no CLI-level `ubx ship` path exercised in this session);
this is direct-runtime verification of the generated module itself, one
level below the Go SDK leg's own full CLI-pipeline proof.

**Python SDK — live, via the real `python3` toolchain (not a stub):** a
real Python import of the outputs-bearing generated module, and a real
driver script that calls the generated function, unpacks the returned
tuple, and uses one value directly as another `sdk.resource()`'s own
config value — the emitted document's own `$ref` resolved to the correct
address. Same depth as the TypeScript leg: direct-runtime verification of
the generated module, not a full CLI pipeline run.

**md — live, once, against the real Claude Sonnet 5 API, hermetic
otherwise:** the exact worked-example document above, run through the
real `intentprovider.DraftWithRetry` against the real Claude API
(`UBX_TEST_SLOW`-gated in the committed test suite; run manually this
session), produced the correct `call_name`/output-reference shape on the
FIRST attempt, no retry needed. This single live run is the full extent
of live verification for the md medium this session — the rest of its
coverage (schema, `validate.go`, the new duplicate-`call_name` refusal)
is hermetic only, run against the fake adapter and direct wire-JSON, per
this project's own standing "hermetic acceptable, but say so" allowance
for the md medium specifically (UBI-86's own precedent). Stated
explicitly, not blurred with the Go SDK/diagram legs' own full
resolve→accept→ship proof: one real API call confirming the PROMPT
elicits correct behavior is meaningfully less coverage than a real ship
against a real backend, even though both are "live."

## List-typed parameters + iteration (UBI-129)

**The gap this closes.** Every blueprint built and exercised across
UBI-74's own 8 closed slices (and UBI-126/128/130's own follow-ons)
creates a FIXED, single-instance set of resources — the founder's own
separate design session (2026-08-04, recorded on UBI-74, filed as its own
ticket at UBI-74's close so it wouldn't be lost) explicitly named this as
real, unproven scope: a blueprint that needs to create N resources (one
subnet per availability zone, say) had no proven path at all. This
section closes it: a new `list(string)`/`list(number)` params: type, a
genuinely new build-time AI recognition capability (an iteration pattern
in prose compiles to a real loop, not N separately-resolved instances),
and a decision — not a deferral — on how a lossy, no-list-attribute
medium (D2 diagram) represents a list-typed call argument at all.

**Core design insight, taken directly from UBI-129's own filed
description**: in a real SDK language, "count"/"for_each" isn't a special
construct ubx needs to invent the way Terraform did — Terraform needed
dedicated loop syntax because HCL has none; a blueprint compiles to real
Go/TypeScript/Python source, and all three already have a native `for`
loop. The entire feature is "recognize the iteration pattern, then emit
an ordinary loop" — no new runtime construct in `sdk/go`, `sdk/ts`, or
`sdk/py` at all (confirmed by search before writing a line of this: zero
changes needed to any of the three runtimes).

### `params:` gains two list types

```yaml
params:
  vpc_id: string, required
  availability_zones: list(string), required
```

`list(string)`/`list(number)` (`blueprint/ubxfile.go`'s `ParamListString`/
`ParamListNumber`) join the existing `string`/`number`/`bool` set —
`list(bool)` is deliberately not added, matching this file's own
established "extend when a real blueprint needs it, never speculatively"
discipline (the identical reasoning `number`'s own "always Go `int`, no
float" decision already used). **A list-typed param can only ever be
declared `required`** — `params: default` is a hard parse error naming
the reason (`parseDefaultValue`'s new explicit refusal, not a fallthrough
to a generic "unrecognized type" message): a list param is always
consumed by exactly one `for_each` resource, which has no notion of an
un-given default to fall back to. `GoType()`/`TSType()`/`PyType()` gain
the obvious per-language mappings (`[]string`/`string[]`/`list[str]`,
`[]int`/`number[]`/`list[int]`) — and, because a param's declared TYPE is
the only thing the outer function-signature-building code in
`gogen.go`/`tsgen.go`/`pygen.go` ever consults, the required-param
positional-argument codegen for a list param needed **zero changes at
all** beyond those two methods returning the right string.

### `resources:` gains a new `for_each` field, and a new synthetic-token grammar

The wire shape this needs (`resolver.ResourceIntent.ForEach string`,
`json:"for_each,omitempty"`) is purely additive, matching every prior
blueprint-specific field's own precedent (`DependsOn`/`Sources`/
`BlueprintCalls`/`Overrides`) — empty for an ordinary, non-iterating
resource (every resource before this ticket, and the overwhelming common
case after it), consumed exclusively by `blueprint/decode.go`'s own
`decodeBlueprint` (never by `resolver.Resolve` itself, which has zero
awareness this field means anything, exactly like `DependsOn`).

```json
{
  "type": "aws_subnet",
  "name": "subnet-{availability_zones}",
  "op": "create",
  "for_each": "availability_zones",
  "config": {
    "vpc_id": "{vpc_id}",
    "availability_zone": "{availability_zones}",
    "cidr_block": "10.0.{availability_zones_index}.0/24"
  }
}
```

`for_each` names a declared list-typed param's own bare name (never
wrapped in braces — this is a structured field, not embedded prose).
Inside THAT SAME resource's own `name`/`config` fields (never any other
resource's), the placeholder grammar the build pipeline already
established (`{param_name}` tokens, `blueprint/decode.go`'s
`placeholderToken` regex, unchanged) gains two new, per-resource-scoped
meanings, resolved by each language's own `paramRef`:

- The bare `{availability_zones}` token — the SAME token that would
  otherwise mean "the whole declared param" — means **the current loop
  element's own value** while rendering the ONE resource whose own
  `for_each` names it. This reuses the existing token grammar rather than
  inventing a second one: a list param's bare token has no other legal
  meaning anywhere else in a blueprint (a scalar Config field can't hold
  a whole list any more than Terraform's own `each.key`/`each.value`
  would make sense outside a `for_each` block), so there's no real
  ambiguity to resolve.
- `{availability_zones_index}` (that same param name, `_index` appended —
  matched by the identical `[a-zA-Z0-9_]+` identifier regex with zero
  changes, since an underscore was always a legal identifier character)
  means the loop's own zero-based position.

Both tokens are refused with a clear, specific error anywhere OUTSIDE
their own `for_each` resource — referencing a list param's bare token on
an ordinary resource (`decodeBlueprint` never expanded it into one), or
referencing either synthetic token on a DIFFERENT resource than the one
that declared `for_each` for it, or referencing a list param's bare token
at all when NO resource declares `for_each` for it — every one of these
is a hard build error, never a silent wrong substitution (`goGenerator.
paramRef`/`tsGenerator.paramRef`/`pyGenerator.paramRef`, each
independently, matching this file's own "each language re-derives/re-
validates independently, self-contained" precedent). Deliberately,
narrowly out of scope: the `{param op literal}` arithmetic form UBI-123
added (`{retention_days * 86400}`) is refused on EITHER synthetic token
with a clear message pointing at the plain form instead — a real,
deliberate boundary (this ticket's own worked example needs no such
conversion; supporting it would also need to know the list's own element
type, genuinely more design than any real worked example has asked for
yet).

### A genuinely new build-time AI capability, scoped to `cli/blueprintDraftPrompt` only

The system prompt teaching a model to recognize "for each value in
`{list_param}`, create..." and draft a `for_each`-bearing resource lives
entirely inside `blueprintDraftPrompt` (`cli/blueprint.go`) — the SAME
place the param-preservation preamble already lives, added ONLY when at
least one declared param is list-typed (`hasListParam`), never touching
`intentprovider`'s own general system prompt (`claude/adapter.go`) or the
general md-calling grammar at all. This mirrors the existing
param-preservation preamble's own precedent exactly: a per-blueprint-
build instruction, not a global one, since ordinary stack documents never
declare list params in the first place. `intentprovider/schema.go`'s
structured-output schema gains one new required-but-may-be-empty
`for_each` string property on the `resource` object (mirroring
`call_name`'s own "required, empty string legal" shape exactly);
`intentprovider/validate.go`'s `wireResourceIntent`/`parseAndValidate`
decode it straight through, unvalidated — real semantic validation (does
this param exist, is it list-typed, is it declared on at most one
resource) is deliberately deferred to `blueprint.decodeBlueprint`, the
one place that already has the Ubxfile's own declared params in hand,
matching `outputs:`'s own identical two-layer "shape here, existence
there" validation split.

### Go codegen: a real `for` loop, `[]*sdk.Computed` return

```go
func VpcSubnets(vpcId string, availabilityZones []string) []*sdk.Computed {
	sdk.PushBlueprintSource("vpc-subnets")
	defer sdk.PopBlueprintSource()

	var subnetList []*sdk.Computed
	for availabilityZonesIndex, availabilityZonesValue := range availabilityZones {
		item := sdk.Resource(Subnet, fmt.Sprintf("subnet-%v", availabilityZonesValue), SubnetConfig{
			AvailabilityZone: availabilityZonesValue,
			CidrBlock:        fmt.Sprintf("10.0.%v.0/24", availabilityZonesIndex),
			VpcId:            vpcId,
		})
		subnetList = append(subnetList, item)
	}
	return subnetList
}
```

Go's own `for i, v := range slice` gives the index for free, matching
`{list_param}`/`{list_param_index}`'s own two-token design exactly — the
loop header's own variable names (`camelCase(param)+"Value"`/`+"Index"`,
`newGoForEach`) are EXACTLY the identifiers `paramRef` resolves those two
tokens to while rendering this one resource's fields, by construction,
never re-derived independently. **Explicit per-instance resource
naming, never Terraform-style indexed addressing** (the ticket's own
explicit requirement): a `for_each` resource's own `Name` is a TEMPLATE
(`"subnet-{availability_zones}"`), rendered through the SAME `renderString`
mechanism an ordinary Config field value already uses (`renderResourceName`,
new — byte-identical `%q`-literal behavior for every ordinary resource,
completely unaffected). One real, load-bearing subtlety this surfaced:
a `for_each` resource's own per-language IDENTIFIER (its `Subnet`/
`SubnetConfig` binding name) can't be derived from its own templated Name
directly (`pascalCase` rejects `{`/`}` outright) — `forEachIdentifierBasis`
(`identifier.go`) strips every `{...}` token and collapses the separator
left behind (`"subnet-{availability_zones}"` → `"subnet"`) before deriving
the identifier, one binding shared by every instance regardless of its
own per-iteration runtime name.

**Deliberately, narrowly out of scope, checked and refused, not silently
broken:** at most ONE `for_each` resource per blueprint (multiple
simultaneous iterations is real, unproven complexity, never attempted
here); a `for_each` resource can never be the target of a sibling's own
`$ref`/`depends_on` OR of an `outputs:` entry (an individual iteration's
own instance isn't addressable that way — only the compiled function's
own returned LIST exposes instances to a caller); a blueprint with a
`for_each` resource cannot ALSO declare `outputs:` at all (combining a
per-iteration return list with named single-instance outputs is real,
valid future work, not attempted this ticket — every one of these is a
hard `decodeBlueprint`-time build error, reused unchanged by every
language's own codegen, never re-checked independently three times).

### TypeScript codegen: `Array.prototype.forEach`, `any[]` return

```ts
export function vpcSubnets(vpcId: string, availabilityZones: string[]): any[] {
  const subnetList: any[] = [];
  availabilityZones.forEach((availabilityZonesValue, availabilityZonesIndex) => {
    const item = resource(Subnet, `subnet-${availabilityZonesValue}`, {
      availabilityZone: availabilityZonesValue,
      cidrBlock: `10.0.${availabilityZonesIndex}.0/24`,
      vpcId: vpcId,
    });
    subnetList.push(item);
  });
  return subnetList;
}
```

TS's own idiomatic index+value iteration form (`Array.prototype.forEach`'s
own two-argument callback) needs no separate index-tracking variable the
way a plain `for` loop would — chosen over a C-style `for` loop for
exactly that idiomatic-fit reason. Unlike Go's named return VALUES (real
declared variables sharing the function body's own scope, `newGoForEach`'s
own reason to collision-check against every other identifier), pushing
into a `const subnetList: any[]` needs no collision guard against
resource/param identifiers the way Go's does — `newTSForEach`'s own
collision check is correspondingly narrower (TS reserved words plus every
resource/param identifier this same file derives), matching
`checkTSIdentCollisions`' own already-smaller-than-Go's precedent.

### Python codegen: `enumerate`, `list[Any]` return

```python
def vpc_subnets(vpc_id: str, availability_zones: list[str]) -> list[Any]:
    subnet_list: list[Any] = []
    for availability_zones_index, availability_zones_value in enumerate(availability_zones):
        item = sdk.resource(Subnet, f"subnet-{availability_zones_value}", SubnetConfig(
            availability_zone=availability_zones_value,
            cidr_block=f"10.0.{availability_zones_index}.0/24",
            vpc_id=vpc_id,
        ))
        subnet_list.append(item)
    return subnet_list
```

Python's own native `enumerate()` mirrors Go's own free index exactly,
matching the ticket's own "already have a for loop, no new construct"
framing a third time. One real, live-found subtlety (caught by a real
`GeneratePython` run, not assumed): `pyLocalVarName` (used everywhere
ELSE this file derives a resource's own local-variable name) assumes an
ordinary, non-templated `Name` and fails outright on a `for_each`
resource's own `"subnet-{availability_zones}"` — `newPyForEach` derives
its own accumulator variable name via the SAME `forEachIdentifierBasis`
Go's `wrap()` already uses, never via `pyLocalVarName` directly.

### SDK callers: a real list/array/slice, confirmed zero new mechanism

```go
vpcsubnets.VpcSubnets("vpc-123", []string{"eu-central-1a", "eu-central-1b", "eu-central-1c"})
```

```ts
vpcSubnets("vpc-123", ["eu-central-1a", "eu-central-1b", "eu-central-1c"]);
```

```python
vpc_subnets("vpc-123", ["eu-central-1a", "eu-central-1b", "eu-central-1c"])
```

Exactly as the ticket's own success bar predicted: a caller in any of the
three languages passes a real, native list/array/slice literal directly
— no new SDK mechanism, no wrapper type, confirmed live (below) in all
three.

### Cross-medium calling: a comma-separated string, the SAME `Args` shape every scalar param already uses

**The diagram design decision, made this session, not deferred again.**
The ticket named two candidates and flagged this as genuinely unresolved:
a comma-separated string in a single D2 attribute value, or repeated
child nodes. **Decision: comma-separated string.** Reasoning, checked
against the real code before deciding, not assumed:

1. **Zero new parsing code in `diagram/parse.go`.** `blueprintCallFromNode`'s
   existing child-attribute loop already does `call.Args[child.ID] =
   child.Label.Value` — a plain string, verbatim, for every non-reserved
   attribute, exactly like every scalar param already works. A
   comma-separated value (`availability_zones: "eu-central-1a,
   eu-central-1b, eu-central-1c"`) needs this package to change NOTHING
   AT ALL — confirmed by reading the function before deciding, not
   assumed from the shape of the problem. Repeated child nodes would need
   a genuinely new attribute-shape concept (parsing N children under one
   attribute name into a list), new schema/parsing code, and a real
   asymmetry with the md medium (which has no "repeated node" concept of
   its own to mirror).
2. **Symmetric with the md medium.** Both mediums already produce the
   IDENTICAL `resolver.BlueprintCall.Args map[string]string` shape — a
   comma-separated string keeps that symmetry exactly; repeated child
   nodes would fork the wire representation across mediums for a
   capability neither medium's own underlying model actually needs to
   express two different ways.
3. **`invoke.go`'s own real invocation mechanism makes this nearly free.**
   `blueprint.ExpandCalls` doesn't string-substitute against a blueprint's
   own raw prose at call time at all — it literally RUNS the blueprint's
   own ALREADY-COMPILED function (Slice 5's own real mechanism,
   unchanged) via a synthesized throwaway caller program. The real for
   loop from the previous three sections already executes for free, at
   RUNTIME, inside that synthesized program, the moment `renderArgLiteral`
   (the ONE place this ticket needed to change in `invoke.go`) renders
   the list-typed arg as a real language-native list literal.

```d2
platform: "vpc subnets call" {
  class: ubx_blueprint
  blueprint: "../vpc-subnets"
  vpc_id: "vpc-0123456789abcdef0"
  availability_zones: "eu-central-1a, eu-central-1b, eu-central-1c"
}
```

```md
Use blueprint `vpc-subnets` with:
  vpc_id = vpc-0123456789abcdef0
  availability_zones = eu-central-1a, eu-central-1b, eu-central-1c
```

`renderArgLiteral` (`blueprint/invoke.go`) gains two new cases —
`ParamListString`/`ParamListNumber` — splitting the raw comma-separated
text (`splitListArg`, tolerant of `"a,b,c"` and `"a, b, c"`
interchangeably) and rendering each element per language
(`[]string{...}`/`[]int{...}` for Go, `[...]` for TS/Python, reusing
`jsonStringLiteral`/`numberLiteral` unchanged). Every OTHER function in
`invoke.go` — `resolveCallArgs`, `argLiteralOrDefault`,
`writeGoCaller`/`writeTSCaller`/`writePyCaller` — needed **zero changes**:
a list-typed param is always `Required` (no default to fall back to), so
it flows through the exact same "required param → positional literal"
path every scalar required param already takes. The md medium's own
system-prompt schema (`intentprovider/schema.go`'s `blueprintCall.args`
description) gained one clarifying sentence — copy the comma-separated
text verbatim as one string value, never a JSON array — a documentation
addition only, since the underlying `Args map[string]string` shape
already supported this without any code change at all.

### Hermetic tests

`blueprint/ubxfile_test.go`-adjacent coverage lives in a new
`blueprint/foreach_test.go` (mirroring `gogen_test.go`'s own established
per-language test-file convention): `TestGenerateGo/TS/Python_ForEach_
SignatureAndReturn` (the exact rendered source, string-matched);
`TestGenerateGo/TS_ForEach_CompilesClean` (a real `go build`/`deno check`);
`TestGenerateGo/TS/Python_ForEach_RuntimeUsable` (a real `go run`/
`deno run`/`python3` producing the correct 3 `aws_subnet` resources with
correct per-instance `availability_zone`/`cidr_block` values, sharing one
`assertVpcSubnetsDoc` assertion helper across all three languages — proof
all three generated loops are behaviorally EQUIVALENT, not just
independently plausible); seven validation-error tests (multiple
`for_each` resources, a non-list-typed `for_each` param, an undeclared
`for_each` param, `for_each`+`outputs:` combined, a sibling `$ref`
targeting a `for_each` resource, a bare list token referenced outside its
own `for_each` resource — both with and without any `for_each` resource
declared at all — and an index token referenced outside its own
`for_each` resource); `TestExpandCalls_ForEach_CommaSeparatedListArg`
(the cross-medium proof: a real `deno run` through the FULL `ExpandCalls`
path, a comma-separated `Args` value producing the correct 3 resources —
UBI-129's own required "confirm this works with zero new mechanism"
bar, for the calling-convention half). `diagram/parse_test.go` gained
`TestParse_UbxBlueprint_ListParamCommaSeparated`, proving the design
decision's own central claim directly: the comma-separated text survives
`blueprintCallFromNode` completely unmodified, through the identical code
path `TestParse_UbxBlueprint_NodeBecomesBlueprintCall` already exercises
for an ordinary scalar param. Full suite green (`go test ./... -count=1`),
`gofmt -l .`/`go vet ./...` clean.

### Live verification, the ticket's own required bar — met for two legs, honestly blocked for two, not blurred together

**Go SDK — live**: a real `go build`/`go run` (this session's own
hermetic `TestGenerateGo_ForEach_RuntimeUsable`/`_CompilesClean`) proves
the generated `VpcSubnets` function, called with a real
`[]string{"eu-central-1a", "eu-central-1b", "eu-central-1c"}` literal,
produces exactly 3 `aws_subnet` resources with correct per-instance
`availability_zone`/`cidr_block` values against the real `sdk/go`
runtime — not a mock. The SAME package was additionally staged for a
real `ubx resolve --from-code` run against the real `hashicorp/
aws@6.54.0` schema (resolve-only, never `ubx ship`, per this project's
own standing rule) — see this session's own real-world account below for
why that specific step's own execution is recorded honestly as
hand-off, not this session's own direct action.

**TypeScript/Python SDK — live**: a real `deno run`/`python3` (this
session's own hermetic `TestGenerateTS/Python_ForEach_RuntimeUsable`)
confirms direct runtime usability with the identical 3-AZ input and the
identical correct-output assertion helper as the Go leg — the same "one
level less depth than a full CLI pipeline" honesty this project's own
Outputs/md precedent already established, named here rather than blurred
with the Go leg's own additional real-AWS-schema staging.

**Diagram — live, a full real `ubx plan`→`ubx ship`→`ubx why` round trip
against the real, hermetic `fakeprovider` subprocess, the strongest proof
this ticket produced.** A second, minimal blueprint (`widget-list` — one
`fake_widget`-typed `for_each` resource, `fakeprovider`'s own real
shippable type, since `aws_subnet` has no fakeprovider schema — the SAME
UBI-128-established substitution pattern) and a real `.d2` file:

```d2
platform: "widget list call" {
  class: ubx_blueprint
  blueprint: "../widget-list"
  widget_names: "alpha, beta, gamma"
}
```

A real `ubx plan --from-diagram widgets.d2 --stack widget-list-demo`
against the real `fakeprovider` subprocess produced, correctly, on the
first attempt:

```
Plan  widget-list-demo · from widgets.d2

  + fake_widget.widget-alpha create
    source: blueprint widget-list:sha256:6485ca3e79b6…
      name: "alpha"

  + fake_widget.widget-beta create
    source: blueprint widget-list:sha256:6485ca3e79b6…
      name: "beta"

  + fake_widget.widget-gamma create
    source: blueprint widget-list:sha256:6485ca3e79b6…
      name: "gamma"

delta: +3 create(s), ~0 change(s), -0 terminate(s)
```

— three correctly-named, correctly-valued resources from ONE
comma-separated D2 attribute value, exactly as designed. `ubx ship
52604e58ff41 --yes` then shipped all 3 for real against the fakeprovider
subprocess (`3 resource(s), 3 shipped, 0 failed`), and `ubx why
widget-list-demo.fake_widget.widget-beta` confirmed correct provenance
(the real blueprint content hash, the real diagram document's own content
hash, all three sibling instances listed) — a full resolve→accept→ship→
provenance round trip, matching UBI-74 Slice 6/UBI-128's own established
live-verification bar for this medium exactly, not a lesser one.

**md calling convention — hermetic only, via `ExpandCalls` directly, for
the reason named below.** `TestExpandCalls_ForEach_CommaSeparatedListArg`
runs the REAL `blueprint.ExpandCalls` path (a real `deno run` underneath,
the identical mechanism Slice 5 already proved for scalar params) with a
genuine comma-separated `Args` value, confirming the exact 3-resource,
correct-per-instance output for the vpc-subnets fixture — this is "live"
in every sense that matters functionally (real subprocess, real generated
code, no test double) for the CONSUMING half of the md medium, even
though (unlike the diagram leg just proven above) it doesn't originate
from a real `.md` file drafted by the real Claude API — see the honest
account below for why that specific half is blocked this session, not
attempted around.

**Two real, named gaps, not glossed over:**

1. **The build-time AI recognition itself — "confirm the AI correctly
   drafts the iteration pattern" — was NOT live-verified this session.**
   A real `ubx blueprint build .` run against the exact worked-example
   Ubxfile above (staged at `~/ubx-playground-ubi129-list-params/
   vpc-subnets`, ready to run) failed twice, identically, with a real
   `400` from the Anthropic API: `"Your credit balance is too low to
   access the Anthropic API."` — the project's own configured `[intent]`
   Claude adapter key, not this session's own Claude Code billing, and
   not something this session can resolve on its own. The generated Go
   package this section's own worked examples quote is byte-identical to
   what `blueprintDraftPrompt`'s own new instructions are DESIGNED to
   elicit (this session hand-constructed the exact intent/v1 JSON the
   model would need to produce and fed it straight to `GenerateGo`,
   bypassing the draft step) — proving the CODEGEN side works
   correctly, but not that the MODEL reliably produces that shape from
   the prose unaided. A real, first-priority follow-up once the
   project's own API credits are restored: re-run `ubx blueprint build .`
   in that same staged directory (one command, everything else already
   in place) and confirm the model's own real output matches.
2. **The md calling-convention leg — "call ... from md with
   comma-separated prose" — is hermetic only, for the identical reason.**
   Extracting a `blueprint_calls[].args` entry from md prose is itself an
   Anthropic API call (`intentprovider.DraftWithRetry`), blocked by the
   same credit exhaustion. `TestExpandCalls_ForEach_CommaSeparatedListArg`
   hermetically proves the CONSUMING half (once `Args["availability_zones"]`
   contains the right comma-separated string, everything downstream is
   correct) — matching the schema.go clarifying-sentence addition's own
   underlying bet: this is a thin, low-risk mapping step following an
   already-proven pattern for scalar params (UBI-86's own precedent), not
   a genuinely new capability the way `for_each` recognition is. Real,
   named, not assumed equivalent to actually having run it.

**Resolve-only against real AWS (SDK leg), handed off, not self-executed
this session:** this session's own attempt to `go build` a real calling
program in a directory the auto-mode permission classifier read as
heading toward `ubx ship` against real AWS (CLAUDE.md's own absolute,
no-exceptions rule) was blocked before it reached `go run`/`ubx resolve`
at all — correctly, per this project's own standing safety posture, even
though the actual intended operation was resolve-only (never applies).
Rather than working around that block, the founder was asked directly
and chose to run the real `ubx resolve --from-code create_vpc_subnets.go
--ledger-dir . --out resolved.json` command in `~/ubx-playground-
ubi129-list-params/stack-real-aws` personally, against the real,
already-generated `VpcSubnets` package and the real `hashicorp/
aws@6.54.0` schema — [PLACEHOLDER: result to be recorded here once
received].
