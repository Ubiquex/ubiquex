# Blueprints — signed, reusable, parameterized proposal templates (UBI-74)

> **Slices 1–2 (this document): the Ubxfile format + `ubx blueprint build
> .` (Go only), the resolved functional-options defaults design, and a
> real stack calling a locally-built blueprint package through `ubx
> resolve --from-code`, completely unchanged.** No publishing/
> distribution/nesting/multi-language/cross-medium/provenance yet. Full
> design context (naming, trust model, the eight-slice breakdown, the
> rejected intermediate designs) lives in UBI-74's own Linear comment
> thread — read it before touching this arc again, later comments
> supersede earlier ones. Slices 3–8 (package/distribute, multi-language,
> cross-medium calling, provenance/render, OCI push, tarball delivery) are
> each their own future session; nesting (`uses:`) is UBI-121, tracked and
> designed separately, never touched here.

## Scope: what Slices 1–2 build, and what they don't

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

Not built here, named so it isn't assumed covered: distribution of any
kind — a "local import" is the ONLY calling shape Slice 2 proves; a
stack's own `uses:` key naming a blueprint by ref (UBI-121), package/pull/
verify (`ubx blueprint package`/`pull`/`verify`, Slice 3), `--lang ts`/
`--lang py`/`--lang all` (Slice 4), diagram/md call sites (Slice 5),
provenance tagging and `why`/`render` integration (Slice 6), OCI push
(Slice 7), tarball delivery (Slice 8); the bound policy engine (UBI-118,
split off UBI-74 entirely).

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
