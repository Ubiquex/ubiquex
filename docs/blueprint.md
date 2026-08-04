# Blueprints — signed, reusable, parameterized proposal templates (UBI-74)

> **Slice 1 (this document): the Ubxfile format + `ubx blueprint build .`,
> Go only, no publishing/distribution/nesting/multi-language/cross-medium/
> provenance yet.** Full design context (naming, trust model, the eight-
> slice breakdown, the rejected intermediate designs) lives in UBI-74's own
> Linear comment thread — read it before touching this arc again, later
> comments supersede earlier ones. This document only pins down what
> Slice 1 actually built: the Ubxfile grammar and the resolved-intent →
> Go codegen. Slices 2–8 (local call, package/distribute, multi-language,
> cross-medium calling, provenance/render, OCI push, tarball delivery) are
> each their own future session; nesting (`uses:`) is UBI-121, tracked and
> designed separately, never touched here.

## Scope: what Slice 1 builds, and what it doesn't

Builds: parsing an `Ubxfile` (three keys only — `lang`, `params`,
`resources`); `ubx blueprint build .` (finds the Ubxfile the same way
`docker build .` finds a Dockerfile, resolves `resources:` through the
existing intent-provider pipeline — UBI-41's `DraftWithRetry` — exactly
once, then compiles the resulting draft into real, compilable Go source:
a typed function whose parameters match `params:`, whose body makes real
`sdk.Resource()` calls with real `Computed` refs between them, reusing
`sdk/go/runtime` — UBI-35's own evaluator — completely unchanged).

Not built here, named so it isn't assumed covered: calling the built
package from a real stack (Slice 2 — this session never runs the
generated function, only compiles it); `--lang ts`/`--lang py`/`--lang
all` (Slice 4); packaging/pulling/verifying (`ubx blueprint
package`/`pull`/`verify`, Slice 3); diagram/md call sites (Slice 5);
provenance tagging and `why`/`render` integration (Slice 6); OCI push
(Slice 7); tarball delivery (Slice 8); `uses:` nesting (UBI-121); the
bound policy engine (UBI-118, split off UBI-74 entirely).

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

**`lang:`** — a single scalar value. Slice 1 accepts only `go`; any other
value is a hard error naming that only Go is supported this slice (never
a silent no-op) — `--lang all`/`ts`/`py` is Slice 4's own scope per UBI-
74's implementation breakdown, not inferred early here.

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
  anything: this is NOT a gap needing a future fix — it's the exact,
  already-supported "JSON-envelope string that needs marker resolution"
  shape that comment names directly, which the resolver's own reference
  walk and the executor's own `substituteComputed` already know how to
  find and substitute one level into a string attribute's own decoded
  JSON, at Slice 2+'s own call/resolve time. Slice 1's own `renderString`
  never tries to parse or rewrite a string leaf's own content — it
  renders the resolved value VERBATIM as a quoted Go literal — which is
  exactly correct here, since that reproduces the identical embedded-
  `$ref` text the resolver pipeline already expects to find.

- **`params:` `default` values are parsed but not yet load-bearing at
  codegen time.** Go has no native optional/default-argument syntax, so
  every declared param — `required` or `default` alike — compiles to an
  equally required, positional Go function argument (confirmed by this
  slice's own live verification: `retention_days: number, default 1`
  produced `retentionDays int` in the generated signature, no different
  from the two `required` params next to it). `Param.Default` is parsed
  and stored on `Ubxfile.Params` (validated, never silently dropped at
  PARSE time) but genuinely unused by `GenerateGo`. What a caller not
  passing a value for a `default` param should actually mean — a
  functional-options pattern, a second generated overload, something
  else — is a real open question, deliberately deferred to whatever
  Slice 2's own calling convention turns out to need, not decided here.

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
