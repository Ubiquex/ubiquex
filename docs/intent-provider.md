# Intent provider + md medium — AI transcription behind the deterministic line (UBI-41)

> This document is the spec, written before any code, per this project's own
> session protocol (CLAUDE.md: "a plan change is not real until it lands in
> docs/plan.md," and design decisions live in docs/ before implementation).
> docs/schema.md's own amendment (below, this session) pins the wire-format
> half — the intent/v1 draft's new ambiguity content, and two new
> `intent.sources[].kind` values. docs/intent-provider-adversarial.md pins
> the required-outcome program. docs/architecture.md's "Intent provider +
> md medium" section cross-links this document from the system-model level.

## Scope: what this session designs, and what it doesn't

Session 1 of UBI-41, per the ticket's own instruction ("design session first
per protocol... before code"). This document designs:

1. **The intent provider interface** — an adapter boundary for N LLM
   backends (Claude first), the exact shape of the transcription-only
   boundary, structured-output validation and its retry/hard-fail contract,
   and the `[intent]` config table (BYO key, never material).
2. **The md medium** — `ubx propose --from-doc <file>.md`'s pipeline shape,
   the ambiguity-as-visible-content design (the design center of this whole
   arc), and doc-authoring conventions as guidance, not grammar.
3. **The conformance suite design** — golden md→intent fixtures, what
   "supported" means for an adapter, and why this conformance discipline
   necessarily differs from the tfplugin provider layer's byte-exact one.

No code lands with this document. Implementation is sized at 2-3 further
sessions (see "Implementation slices," below) toward a real, working
`ubx propose --from-doc payments.md` against the real Claude API.

**Out of scope for this arc, named so it isn't assumed covered:**

- **Chat** (dialogue-shaped intent, not doc-shaped) — docs/plan.md's own
  medium-order decision (2026-07-17) sequences it *after* this arc,
  "nearly free once the intent provider exists — same interface, different
  input shape." The `Adapter` interface designed below is deliberately
  input-shape-agnostic for exactly this reason (see "The adapter interface,"
  below), but chat's own turn-taking, session state, and
  `intent.sources[].kind: "dialogue"` privacy tiering (docs/schema.md's
  still-open "Dialogue format & privacy tiering" question) are not designed
  here.
- **A real cost/policy engine.** Component map #9 (docs/architecture.md)
  is still an empty stub hook. Where this document's own ambiguity design
  touches cost ceilings, it is explicit that the intent provider's own cost
  reasoning is a rough, untrusted, transcription-shaped estimate — never an
  authoritative computation, and never a substitute for a real policy
  engine when one exists.
- **Diagram and SDK frontends** — component map #7's other two members,
  each its own arc.
- **Local-model adapters (ollama-class).** The adapter interface is
  designed so one can be added later without changing the boundary, but no
  local adapter is built or even stubbed this session — per the ticket's
  own roster ordering (Claude → OpenAI → Gemini → local), and per this
  project's standing rule that a thing "earns support through the [same]
  suite, not by existing" (the ticket's own words, echoing exactly how
  `LedgerStore` backends and provider types already earn "supported" in
  this codebase — see "Conformance suite," below).

## The boundary that shapes everything: transcription, never computation

The thesis, applied to a fourth authoring frontend after intent files
(hand-written), diagrams (future), and SDK (future): **the LLM's only job
is transcription.** Prose in, an `intent/v1` **draft** out. It never
computes a value, never resolves a `$ref`, never touches a ledger, never
calls a provider, never applies anything.

This is not a new invariant — it is docs/architecture.md's own founding
"trust chain" invariant #3 (*"The LLM operates in intent-space only; the
deterministic resolver computes all values; nothing the LLM emits reaches
apply without resolution + human signature"*), stated at this project's
very first session, now made concrete for the first time an LLM actually
exists in the pipeline. Everything below is that one sentence, worked out
in full:

```
markdown doc (git-tracked, human-authored)
  → [redaction at capture — pattern-scanned, secrets never leave this machine]
  → intent provider (LLM, structured-output-constrained)
  → intent/v1 DRAFT (schema-valid, human-reviewable, NOT yet a proposal)
  → [human review — the draft's own ambiguity content is the reviewable surface]
  → ubx resolve   (existing, unchanged, deterministic — docs/resolver.md)
  → ubx accept    (existing, unchanged — a human signature binds the hash)
  → ubx ship      (existing, unchanged — docs/executor.md)
```

Everything below the "human review" line is **existing, unmodified code**.
This document adds exactly two new components above it: the intent
provider (a new package) and the redaction-at-capture step (a small, new,
pattern-based scanner — see "Secret material in a doc," below). Nothing
about `core/resolver`, `core/ledger`, or `core/executor` changes to make
this arc work — the entire design center is keeping it that way.

**A `draft` is not a `proposal`, and never silently becomes one.** An
intent/v1 draft is exactly `ubx:intent/v1` (docs/schema.md's own
UBI-27 amendment), plus the new ambiguity content this session's
schema amendment adds. It sits on disk as an ordinary file — reviewable,
diffable, editable by a human before it is ever handed to `ubx resolve` —
exactly like a hand-written intent file always has been. `ubx propose
--from-doc` (below) writes this file and stops; it never chains
automatically into `resolve`/`accept`, for the same reason `ubx resolve`
itself never chains into `accept`: each step in this trust chain is a
deliberate human checkpoint, not a pipeline stage to be automated away.

## Component 1 — the intent provider interface

### The tfplugin lesson, applied to LLMs

docs/architecture.md's execution layer already solved "one interface, N
real, independently-versioned backends, selected without the caller
branching on which one": `provider.Provider`, one Go interface, backed by
two wire implementations (tfplugin5, tfplugin6) today and N provider
binaries at runtime, with the AWS/GCP/Kubernetes specifics entirely behind
`GetProviderSchema`. `core/resolver`'s own `SchemaInspector` (docs/resolver.md)
is the same shape one layer up: three questions, answered identically
however many providers are declared.

The intent provider interface is the same pattern for LLMs, minimal by the
same discipline:

```go
// package intentprovider — new top-level package, kept out of core
// deliberately (core stays dependency-free; this needs an HTTP client and
// vendor SDKs, the same inversion cloudtrail/gcpaudit/k8saudit already
// establish for platform-specific I/O).

// Adapter is one LLM backend's transcription capability. Every adapter --
// Claude first, then OpenAI, Gemini, and eventually a local (ollama-class)
// backend -- implements exactly this, and nothing else. An adapter never
// sees a ledger, a provider schema, or a resolved value; DraftRequest's
// own fields are the entire surface it can act on.
type Adapter interface {
	// Name identifies this adapter for config selection ([intent].adapter),
	// conformance results, and error messages -- "claude", "openai",
	// "gemini", "ollama".
	Name() string

	// Draft transcribes one authoring document into a single intent/v1
	// draft attempt. It returns the adapter's raw structured-output JSON,
	// UNVALIDATED against intent/v1 -- validation is the caller's job
	// (see "Structured-output validation," below), never the adapter's own,
	// so every adapter is validated by the identical mechanism regardless
	// of which vendor's own claimed schema-conformance guarantee backs it.
	Draft(ctx context.Context, req DraftRequest) (json.RawMessage, error)
}

// DraftRequest is deliberately the entire boundary: everything an adapter
// is allowed to know about, once redaction-at-capture (below) has already
// run. No ledger handle, no provider schema, no filesystem access --
// an adapter Go type physically cannot resolve a $ref even if it wanted to.
type DraftRequest struct {
	Stack   string // the target stack name, transcribed into the draft's own "stack" field
	Content []byte // the REDACTED document content -- never the raw file (see "Secret material in a doc")

	// Attempt is 1 on the first call. A value >1 means this is a
	// retry-with-errors round (see "Structured-output validation"):
	// PriorOutput/PriorErrors carry the previous attempt's own rejected
	// output and exactly what was wrong with it, verbatim -- an adapter
	// implementation MUST feed both back to the model, never silently
	// discard them and just try again from scratch.
	Attempt     int
	PriorOutput json.RawMessage
	PriorErrors []string
}
```

`Draft` returning only `(json.RawMessage, error)` — not an already-parsed
`IntentFile` — is deliberate: parsing, schema validation, and the
retry decision all live in one shared, adapter-agnostic driver (below),
not duplicated per adapter. This is the same "one mechanism, N callers"
discipline `diffAttributes`/`core.DoubleRun`/`openLedgerForStack` already
established elsewhere in this codebase — an adapter's only job is turning
bytes into other bytes; everything downstream of that is generic.

### Structured-output validation: retry-with-errors, then hard fail — never silently repaired

A shared driver, not adapter-specific code, owns the whole attempt loop:

```go
// DraftWithRetry drives one adapter through up to maxAttempts structured-
// output rounds, validating each raw response against intent/v1 (this
// session's own schema amendment, below) before accepting it. maxAttempts
// is 3 -- one first attempt plus two retries-with-errors -- matching this
// arc's own required adversarial row ("invalid JSON thrice" hard-fails,
// docs/intent-provider-adversarial.md row 7).
func DraftWithRetry(ctx context.Context, a Adapter, stack string, content []byte, maxAttempts int) (*resolver.IntentFile, json.RawMessage, error)
```

Two layers of validation run on every attempt, deliberately never trusting
either one alone — the same "a claimed success is never sufficient by
itself" lesson UBI-44's own post-destroy read-back generalized, applied
here to a claimed schema conformance instead of a claimed apply result:

1. **API-level constraint** (adapter-specific, where the vendor supports
   it — Claude does, see below): the adapter is instructed to emit JSON
   conforming to a hand-maintained intent/v1 JSON Schema, the same "one
   canonical hand-authored spec, not derived from Go structs by
   reflection" discipline docs/schema.md's own wire formats already
   follow. This sharply reduces malformed-JSON-shape failures before they
   ever reach step 2, but it is never trusted as sufficient by itself —
   every vendor's own "guaranteed schema-valid" claim is exactly the kind
   of single unverified guarantee this project has learned, repeatedly, not
   to take on faith (UBI-30's `PlannedPrivate` no-op, UBI-44's silently-false
   destroy).
2. **ubx-side semantic validation** (adapter-agnostic, always runs,
   trusted unconditionally): the raw JSON is unmarshaled into
   `resolver.IntentFile` (extended, this session, with the new
   `Intent.Assumptions`/`.Defaults`/`.Questions` fields — see "Ambiguity as
   visible content," below) and checked against the same presence/shape
   rules `ubx resolve` itself already enforces before it will touch a
   proposal (well-formed addresses, `op` in `{create, modify}`, no
   `$ref`/`$cross` outside `config`, etc.) — reusing the existing type,
   never a parallel one, so "validated against intent/v1" means literally
   "the same struct, the same rules, the identical code path a hand-written
   intent file already goes through," not a second, drift-prone
   reimplementation of the same schema.

On a step-2 failure, the driver retries: `Attempt+1`, `PriorOutput` set to
the exact rejected JSON, `PriorErrors` set to the exact validation
failures (field paths, not vague prose). **Never a client-side patch or
"best effort" repair of the adapter's own broken output** — the driver
never touches the JSON itself, only re-asks the model, because a
silently-repaired draft is precisely the "AI's guesses are auditable text"
design center (below) turned into "AI's guesses, quietly rewritten by
ubx's own heuristics" — the one thing this arc exists to prevent. On the
third consecutive failure, `DraftWithRetry` returns a hard error naming
every one of the three attempts' own validation failures — `ubx propose
--from-doc` exits non-zero, and **no draft file is ever written to disk**
for a run that never produced a valid one. Never a partial, corrupted, or
last-best-guess file left behind to be mistaken for a real draft.

### The Claude adapter (first)

Verified against the real, current Claude API surface before writing this
section (not assumed from training-data recall — the same "check reality
before writing the design down as fact" discipline this project holds
itself to everywhere else):

- **Structured output via `output_config.format`** (`json_schema`,
  `additionalProperties: false` required at every object level) —
  supported directly by the Messages API on Claude Opus 4.8 and Claude
  Sonnet 5 (both current-generation, either is a reasonable adapter
  default; `[intent].model`, below, is user-configurable either way). No
  beta header required. This is the "API-level constraint" layer above.
- **Model + effort**: `claude-opus-4-8` as the adapter's own hardcoded
  fallback default when `[intent].model` is unset — this codebase's
  standing default for anything not explicitly pinned by the user, same
  posture `ubx init`'s own template defaults already take. `effort:
  "high"` as the request default (this is a reasoning-shaped task —
  surfacing genuine ambiguity, not a bare classification/extraction call —
  so `low`/`medium` risk under-thinking the exact cases this arc's whole
  design center cares most about getting right); not user-configurable in
  v1, named as a future config knob if a real workload ever needs it
  tuned.
- **Retry-round prompt caching**: within one `DraftWithRetry` loop, the
  system prompt (the intent/v1 JSON Schema + the doc-authoring convention
  guidance, below — large, and byte-identical across all three possible
  attempts for one draft) carries `cache_control` on its own last block;
  only the user turn changes between attempts (the doc content stays
  fixed too; what's added is the prior-output-plus-errors block). A
  three-attempt draft therefore pays one cache write and up to two cheap
  cache reads, never three full-price system-prompt sends — a direct,
  free consequence of designing the retry loop as "same conversation,
  new turn" rather than "three independent requests," worth stating
  explicitly since it shapes `DraftRequest`'s own shape (a single
  adapter call per attempt that the *adapter* is responsible for
  appending to its own running message history, not the driver
  reconstructing a fresh request each time).
- **BYO key, dependency**: `github.com/anthropics/anthropic-sdk-go` — a
  new external dependency, the first one added purely for an LLM vendor
  (as opposed to a cloud provider or a storage backend). Every other
  adapter (OpenAI, Gemini) adds its own vendor SDK the same way, each one
  isolated behind `Adapter` — `core`/`cli` never import a vendor SDK
  directly, exactly the `ledgerstore` precedent ("kept out of core
  deliberately... the same inversion cloudtrail/gcpaudit/k8saudit already
  establish").

### Session 2 (2026-07-27): built, two real findings

`intentprovider`/`intentprovider/claude`/`intentprovider/conformance`
landed largely as designed above, with one real, checked design
constraint resolved concretely and one real bug found live:

- **`resources[].config` is a JSON-encoded string in the wire shape
  handed to Claude's own `output_config.format`, never a nested object.**
  Checked against the documented structured-output constraint before
  writing `schema.go` (every JSON Schema object node must carry
  `"additionalProperties": false`, including one with no declared
  `"properties"`) — a resource's config is fundamentally open-shaped, so
  there is no way to express "any object shape at all" under a schema
  that must close every object node. `validate.go`'s own `wireIntentFile`
  decodes the string back into a real `json.RawMessage` before handing a
  caller a real `resolver.IntentFile`; a malformed config string is
  itself a step-2 validation failure, feeding the retry loop like any
  other. **Not live-verified this session** (no Anthropic credentials in
  the build environment) — flagged explicitly in `validate.go`'s own doc
  comment, to be confirmed the first time a real live run actually
  happens, not assumed correct from documentation alone.
- **A real gap found live, fixed the same session**: with zero Anthropic
  credentials resolvable at all, the SDK never reaches the server — there
  is no HTTP response, so no `*anthropic.Error` to branch a status code
  on, and the adapter's first cut of `classifyError` silently lumped this
  under the generic "network/connection" bucket, exactly the
  undifferentiated failure docs/intent-provider-adversarial.md row 6
  exists to forbid. Found by actually running the live test
  (`UBX_TEST_SLOW=1`, no credentials in this environment) rather than
  assumed correct from reading the SDK's source — the real error message
  is prefixed `"no Anthropic credentials found"`; the SDK's own typed
  sentinel for it (`auth.ErrNoCredentials`) lives under an `internal/`
  package this module cannot import, so `classifyError` detects it via an
  exact string-prefix check instead (documented in code as a deliberate,
  fail-safe choice: mis-bucketing under "network/connection" is harmless,
  since the full underlying message still reaches the caller either way,
  while a looser substring match risks mis-firing on an unrelated error).

## Component 2 — the `[intent]` config table

Rides the identical config-cascade machinery every other table already
uses (`[provider]`, `[providers]`, `[ledger]`) — cascade content, per-key,
nearest-directory-wins, provenance-tracked by `ubx config`, no new loader.
Fixed, known shape (like `[provider]`/`[ledger]`, not freeform like
`[provider_configs]`) since these are ubx-defined knobs, not
adapter-vendor-defined arbitrary keys:

```hcl
intent = {
  adapter = "claude"                            # required: "claude" | "openai" | "gemini" | "ollama"
  model   = "claude-opus-4-8"                    # optional: adapter's own hardcoded default otherwise
  key_ref = { env = "ANTHROPIC_API_KEY" }        # required: NEVER a literal key
}
```

**`key_ref` is never material — the secrets rule extends to ubx's own
config, not just resolved resource state.** docs/architecture.md's
"Business frame" bullet already states this project's own secrets rule
plainly: *"ledger stores references only, never material."* `[intent]`
config is not the ledger, but it is a file this project's own convention
already assumes gets committed and reviewed like any other `.ubx/config`
layer — a literal API key sitting in a `git`-tracked cascade file is
exactly the "compromised the moment anyone else ever clones the repo"
failure docs/architecture.md's own "Secrets (UBI-23)" section names for
resource state, transplanted one layer up. `key_ref.env` names an
environment variable ubx reads *at the moment of the API call only* —
never persisted, logged, or hashed into anything. This is the same shape
`$secret`'s own `{"ref": "..."}` marker already establishes (a reference,
checked and dereferenced at the point of use, never carried as a value)
— reused as a naming convention, not a new one invented for this table.

### Gemini/Vertex: two genuinely different auth models, settled here

The design-room comment on this ticket flagged this explicitly and asked
for it to be settled in the design session — done:

```hcl
intent = {
  adapter = "gemini"
  model   = "gemini-2.5-pro"
  auth    = "api_key"                           # "api_key" | "vertex"
  key_ref = { env = "GEMINI_API_KEY" }           # required when auth = "api_key"; absent/refused when auth = "vertex"
}
```

```hcl
intent = {
  adapter = "gemini"
  model   = "gemini-2.5-pro"
  auth    = "vertex"
  vertex  = { project = "acme-prod", location = "us-central1" }
}
```

`auth: "vertex"` never carries a `key_ref` at all — Vertex AI's own
identity model is Google's Application Default Credentials (a service
account, ambient to the environment `ubx` runs in), not a bearer key
`ubx` could hold a reference to in the first place. This is not a new
pattern for this codebase: it is the identical posture the GCP *provider*
binary already takes (`GOOGLE_APPLICATION_CREDENTIALS`, ambient, never a
config-held key) — Gemini/Vertex just makes the intent provider's own
auth surface look like the cloud-provider surface it already sits next to
in a GCP-centric shop, rather than inventing a second convention for
"credentials, ambient" alongside the first. A config carrying both
`key_ref` and `vertex` at once, or `auth: "vertex"` with a `key_ref`
present, is a hard config-validation error naming the contradiction —
never a silent "whichever one happens to be checked first" resolution,
the same discipline `providers`/`provider_configs`' own mutual-exclusivity
checks already hold elsewhere in this loader.

### Unknown-key checking, extended (implementation note, not this session's own code)

When this lands in code, `cli/configcascade.go`'s existing
`knownTopLevelKeys` map gains `"intent": true`, and a new
`knownIntentKeys = map[string]bool{"adapter": true, "model": true,
"key_ref": true, "auth": true, "vertex": true}` joins
`knownProviderKeys`/`knownK8sAuditKeys`/`knownLedgerKeys` — the exact same
mechanism, a fourth table, no new pattern. Named here so the eventual
implementation session isn't rediscovering a shape this document already
settled.

## Component 3 — ambiguity as visible content: the design center

**This is the one decision that shapes the entire UX, and it was designed
first, per the ticket's own instruction.** The scenario driving it: a doc
says *"provision a database like staging but smaller."* "Like staging"
names a real, dereferenceable resource (`$ref`-able, in principle) — but
"smaller" is not a value; it is a comparative the LLM must resolve into
one. The tempting design is for the LLM to just pick something reasonable
and move on. **That design is rejected, permanently, not just for this
session.**

### Why silent choices are unacceptable here specifically

Every other value an LLM-authored draft could produce eventually passes
through `ubx resolve`'s own deterministic, schema-checked, hard-erroring
pipeline — a wrong resource *type* is caught (unowned-type refusal), a
wrong *reference* is caught (dangling `$ref`), a wrong *operation* is
caught (`create` against an address that already exists). But a
*plausible, valid, schema-conforming, wrong-guess concrete value* —
`db.t3.small` instead of the `db.t3.medium` the doc's author actually had
in mind — is invisible to every one of those checks. It is a completely
ordinary, well-formed resource configuration that happens to not be what
anyone asked for, and nothing downstream of the intent provider has any
way to know that, or even to know a guess was ever made at all. This is
the single failure mode unique to putting an LLM in the authoring path
that no existing invariant in this codebase already guards against — and
it is exactly the failure mode "the AI's guesses are auditable text"
exists to close.

### The mechanism: ambiguity is content, not a side channel

Every assumption, default, and open question the intent provider had to
resolve to produce a complete draft becomes **explicit, listed, reviewable
content inside the draft itself** — not a log line, not a separate
report, not something `ubx propose --from-doc` prints to stderr and
discards. It rides inside `Proposal.Intent` — the exact same struct field
an intent file's own `intent.summary`/`intent.sources` already occupies,
carried unchanged all the way through `resolve`/`accept` into the final,
hashed, signed proposal (docs/schema.md's own amendment, this session,
below, pins the field shapes). **Reviewable and signed** is not a slogan
here — it is a direct, structural consequence of putting this content on
the one struct that is already part of the canonical hash: a human
accepting a proposal with an unreviewed assumption baked into it is
signing that assumption too, by construction, the same way they already
sign `cost_delta`/`blast_radius`/every resolved config value.

```json
{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {
    "summary": "provision a database for payments, modeled on staging, smaller",
    "sources": [
      { "kind": "document", "ref": "payments.md", "content_hash": "sha256:..." },
      { "kind": "intent_provider", "ref": "claude:claude-opus-4-8", "content_hash": "sha256:..." }
    ],
    "assumptions": [
      {
        "text": "\"smaller\" interpreted as one instance-class step down from staging's db.t3.medium — chose db.t3.small.",
        "affects": ["payments.aws_db_instance.payments-db.instance_class"]
      }
    ],
    "defaults": [
      {
        "text": "No storage size mentioned; carried forward staging's own 100 GiB unchanged (the doc's \"smaller\" was read as compute, not storage — see assumption above).",
        "affects": ["payments.aws_db_instance.payments-db.allocated_storage"]
      }
    ],
    "questions": [
      {
        "text": "The doc doesn't say whether this database needs multi-AZ. Staging is single-AZ; defaulted to single-AZ here too, but this is a real production-readiness decision worth confirming explicitly before accepting.",
        "affects": ["payments.aws_db_instance.payments-db.multi_az"],
        "blocking": false
      }
    ]
  },
  "resources": [ "...": "unchanged shape, docs/resolver.md" ]
}
```

- **`assumptions[]`** — a real interpretive choice was made where the doc
  was ambiguous, and the draft records both the choice and the reasoning.
- **`defaults[]`** — the doc simply didn't address something at all, and
  the intent provider filled the gap from context (an explicit reference
  like "staging," a schema default, a sane platform convention) rather
  than inventing one from nothing. The distinction from `assumptions[]`
  is real, not cosmetic: an assumption resolves stated ambiguity;
  a default fills stated silence — different review postures for a
  human (an assumption is "did you interpret my words correctly?", a
  default is "did you notice I never said anything about this at all?").
- **`questions[]`** — every entry `affects` names the exact config
  path(s) touched, `text` is the plain-language question, and `blocking`
  is a **review-affordance signal only, never an enforcement gate.** A
  `blocking: true` question does not stop resolution, does not stop
  acceptance, does not add a new refusal anywhere in `core/resolver` —
  the pipeline below the draft is unchanged, and stays unchanged, by this
  arc's own design center. What it does: it is a field `ubx why` and any
  future review surface can render prominently (the same posture
  `cloudtrail_unattributed`'s own "record the gap as evidence, don't just
  omit it" discipline already established for a structural gap, UBI-10,
  reused here for an interpretive one). The considered-and-rejected
  alternative — auto-refusing `ubx resolve` on any `blocking: true`
  question — was rejected outright: it would hand the LLM veto power over
  what a human is allowed to review and sign, precisely inverting the
  trust chain this arc's whole boundary exists to preserve. The human is
  the only gate; `blocking` only shapes what they see first.

### Contradictions get the same treatment, one level up

When two stated requirements are structurally incompatible (a $50/mo cost
ceiling and a requirement that structurally can't fit under it; "keep it
simple" and a list of five distinct integrations), the intent provider
still must produce exactly one complete, valid draft — it never emits
"nothing," and it never emits two competing drafts for a human to choose
between (that would just move the ambiguity into a file-selection
problem, not resolve it). It picks the single interpretation it judges
most likely to be correct, and records the conflict as a `questions[]`
entry with `blocking: true`, naming both requirements explicitly and
which one the draft honored. See docs/intent-provider-adversarial.md
row 2 for the required-outcome program this is checked against.

### `intent_provider`'s own draft never gates on cost

Where a doc states an explicit cost ceiling (docs/plan.md's own "cost
ceilings" authoring-convention note, below), the intent provider's own
rough cost reasoning is exactly as untrusted as every other value it
produces — **it is a transcription of a stated constraint, never an
enforcement of one.** Component map #9 (a real policy engine) does not
exist yet; when it does, a real, resolved `cost_delta` check against a
stated ceiling is its job, not this arc's. Until then, a plausible
ceiling violation the intent provider itself notices becomes a
`questions[]` entry (see docs/intent-provider-adversarial.md row 5) —
visible, never silently absorbed, but never a refusal either.

## Doc authoring conventions: guidance, not grammar

The whole premise of md as a medium is "prose in" — a doc that has to
follow a rigid grammar to parse correctly is not meaningfully different
from a hand-written `ubx:intent/v1` file with worse ergonomics. So these
conventions are documented as **things that reliably improve extraction
quality**, never as syntax the intent provider requires to function at
all — **the conformance suite (below) is what actually defines "reliably
works,"** not this list. Three conventions, drawn directly from the
ticket's own naming:

- **`@refs`** — an inline `@<address>` mention (`@payments.aws_vpc.main`,
  the same canonical-address string `$ref`'s own `to` field and
  `Modification.Target` already use) is a strong, unambiguous signal to
  transcribe as a real `$ref`/`$cross` marker rather than free text. An
  `@ref` naming an address the intent provider cannot resolve to
  something it recognizes as plausible (a typo, a stack that doesn't
  exist) is never silently dropped or guessed at — it becomes a
  `questions[]` entry naming the unresolved mention verbatim.
- **Cost ceilings** — a stated dollar figure ("keep this under
  $200/month") is transcribed as context for the intent provider's own
  ambiguity reasoning (see above), never as a computed, authoritative
  gate.
- **Requirement phrasing** — imperative, resource-shaped statements
  ("provision a `db.t3.medium` Postgres instance," "destroy the old
  standby replica") map most reliably onto `resources[]`/`destroys[]`
  entries directly; comparative or descriptive phrasing ("like staging
  but smaller," "something cost-effective for a low-traffic service")
  reliably triggers the assumptions/defaults path above. Both are legal
  input — the second is simply where this arc's own design center does
  its real work.

## Secret material in a doc: redaction at capture, a genuinely different mechanism from UBI-23

UBI-23's existing redaction (`provider.Redact`, docs/architecture.md's
"Secrets" section) is **schema-driven**: it walks a real provider
schema's `Sensitive`-flagged attributes and redacts exactly those, because
it knows, precisely, which JSON paths in a resource's observed state can
ever carry secret material. **A markdown document has no schema at all.**
A human could paste a real database password, an API key, or a private
key block directly into prose, in any shape, at any position — there is
no `Sensitive` flag to consult. This is a real, structurally different
problem, worth stating plainly rather than assuming UBI-23's existing
mechanism generalizes to it (it doesn't — this is exactly the kind of
"checked directly, not assumed to generalize" moment this project's own
STATE.md history is full of).

**Mechanism: a pattern-based pre-flight scanner, run on the raw document
before any network call ever leaves this machine.** Recognizable
secret-shaped patterns — cloud-provider access-key formats (e.g.
`AKIA[0-9A-Z]{16}`-style AWS access key IDs), PEM private-key block
headers, common `Bearer`/API-key-prefixed tokens, and a generic
high-entropy quoted-string heuristic anchored near words like
`key`/`secret`/`password`/`token` — are replaced with a
`[REDACTED: <pattern-name>]` placeholder in the copy that is actually
transmitted to the intent provider. This is deliberately a best-effort,
defense-in-depth scan, not a claimed-complete one — named explicitly as
such (see docs/intent-provider-adversarial.md row 3's own required
outcome, and "What this table doesn't yet cover," below).

**Two distinct byte sequences exist from this point on, deliberately never
confused:**

1. **`content_hash` in the `document` intent.sources entry** hashes the
   **raw, unredacted** file exactly as committed in git — unchanged from
   how every other `content_hash` in this schema already works (tamper
   evidence against the git-tracked artifact a reviewer can actually go
   look at). Redacting what gets hashed would break that tamper-evidence
   property for no benefit — the raw file is never transmitted anywhere
   outside this machine's own filesystem read.
2. **`DraftRequest.Content`** (what actually crosses the network to the
   intent provider, and what the adapter's own conversation history
   retains) is the **redacted** copy. Secret material that the scanner
   catches never leaves this machine, full stop — never sent to a vendor
   API, never present in the adapter's own request/response logs, never
   in an error message.

**A second pass, defense-in-depth**: the *drafted output itself* (the raw
JSON `Draft` returns) is scanned with the identical pattern set before it
is ever written to disk, on the theory that a model could, in principle,
echo back something secret-shaped it saw (even a redacted placeholder
being creatively "filled in" by the model is a scenario worth guarding
against, however unlikely) — a match here is treated exactly like a
step-2 semantic validation failure (see "Structured-output validation,"
above): rejected, retried with the specific match named in `PriorErrors`
so the model is told plainly not to reproduce it, same 3-attempt budget,
same hard-fail-naming-the-reason on exhaustion.

## Conformance suite: golden fixtures, a genuinely different discipline from provider conformance

"Supported" for an adapter means exactly what it already means for a
provider type or a `LedgerStore` backend in this codebase — earned by
passing a suite, never claimed by existing (docs/architecture.md's own
"What a remote store must solve before it can claim support... each store
earns 'supported' via a conformance suite" is the direct precedent; the
ticket's own wording — "'supported' = passes the suite — same discipline
as providers/stores" — states the intent explicitly).

**But the comparison stops at "a suite exists," not at "the suite works
the same way."** `conformance/`'s existing harness (docs/plan.md §M1-2,
docs/architecture.md's provider-type registry) is a **byte-exact**
discipline: a real or fake provider is asked to do something, and the
result either matches exactly or the test fails — because a tfplugin
provider's own behavior, for a fixed input, is deterministic. **An LLM's
output is not.** Two runs of the identical adapter against the identical
fixture doc can legitimately produce two different, equally-valid
`instance_class` choices for "something cost-effective." A conformance
harness that required byte-identical JSON output would either be
constantly, uninformatively red, or would train whoever maintains it to
over-fit adapters to one fixture's own exact wording rather than to
extraction quality generally — the wrong thing to optimize for.

**Fixtures instead carry a per-fixture assertion function, not a golden
diff.** A fixture is a directory: the source `.md` file, plus a small,
hand-written Go checker (`func(t *testing.T, draft *resolver.IntentFile)`)
asserting the *structural and semantic* properties that actually matter —
right resource type(s), right `op`, the specific ambiguous phrase produces
*some* non-empty `assumptions[]` or `questions[]` entry touching the
right config path (never asserting its exact wording), no secret material
anywhere in the output, the draft is schema-valid. This is the same
underlying judgment call docs/config-cascade-adversarial.md's own YAML
strict-mode work already made once in this codebase (checking a
*property* — round-trip stability — rather than an exact rendered string)
applied to a much less deterministic domain.

**Fixture #1 is the payments doc from this arc's own design transcript** —
a doc describing a payments-database provisioning request with exactly
the "like staging but smaller" ambiguity this document's own worked
example (above) walks through in full. It is the canonical first fixture
precisely because its own ambiguity-handling requirement is the design
center this whole arc exists to prove out, not an incidental example
chosen for convenience.

**Per-adapter published results.** Each adapter that passes the full
fixture suite gets a "supported" line and a real, published pass/fail
record — the same posture docs/reliability-report.md already established
for the provider layer (real transcripts, not hand-waved claims). This
document does not draft that report; it exists once real adapters exist
to run it against, in a later session, matching this project's own
"don't scaffold an empty doc" discipline.

## Implementation slices, toward `ubx propose --from-doc payments.md`

Sized per the ticket's own "~3-4 sessions" estimate. Named here so a
future session picks this up without re-deriving the shape:

1. **Interface + Claude adapter + conformance harness — built, session 2
   (2026-07-27).** `intentprovider` package (`Adapter`, `DraftWithRetry`,
   the hand-maintained intent/v1 JSON Schema), `intentprovider/claude`
   (the real adapter, `anthropic-sdk-go`), `intentprovider/conformance`
   (the fixture-runner harness and fixture #1, the payments doc).
   Hermetic tests against a fake adapter implementing `Adapter` directly
   (no network); a `UBX_TEST_SLOW=1`-gated live test against the real
   Claude API, matching `cli/ship_lying_destroy_test.go`'s own precedent
   for a real-but-slow/costly path that shouldn't run by default. See
   STATE.md for the full session account, including two real findings
   (the structured-output config-as-string constraint, and a live-only
   credential-resolution error-classification gap found and fixed the
   same session) and one deliberate, named deviation from this slice's
   own original scope: `[intent]` config wiring
   (`cli/configcascade.go`'s known-keys extension) was NOT built this
   session — deferred to slice 2, since it would have no consumer until
   `ubx propose --from-doc` exists to read it (`claude.Config.APIKey` is
   this session's own stand-in, resolved by whatever calls
   `claude.New` directly).
2. **The md pipeline + ambiguity UX.** `[intent]` config wiring
   (`cli/configcascade.go`'s known-keys extension, deferred from slice 1
   above); new `ubx propose --from-doc <file>.md [--stack ...]` verb:
   reads the doc, runs redaction-at-capture, resolves `key_ref` into a
   real API key and constructs `claude.New`, calls `DraftWithRetry`,
   writes the resulting draft file, and stops (never auto-chains into
   `resolve`). `ubx why`/a review-facing render
   of `assumptions`/`defaults`/`questions` (today's plain-JSON draft is
   already reviewable; a nicer human-facing rendering is this slice's
   own polish, not a schema change). Live-verified end to end against
   the real Claude API and a real stack, matching every other arc's own
   "hermetic, then live" discipline.
3. **Docs + polish.** ubiquex-docs gets a new guide (the real `ubx
   propose --from-doc` transcript, per this project's own "user-visible
   changes update ubiquex-docs in the same session" rule); the
   per-adapter conformance report; any adapter-specific rough edges found
   only by hitting the real API repeatedly.

Chat (docs/plan.md's own medium-order decision) rides the identical
`Adapter`/`DraftWithRetry` machinery afterward — `DraftRequest.Content`
already being "just bytes" rather than "a file path" is what makes that
true; a dialogue transcript is another byte sequence to hand the same
interface, with its own new `intent.sources[].kind: "dialogue"` evidence
entry (already a legal kind, docs/schema.md's founding draft) replacing
`document`'s.

## Out of scope for v1, named so it isn't assumed covered

- Chat, diagrams, SDK — see "Scope," above.
- A real cost/policy engine — the intent provider's own cost reasoning is
  transcription-shaped, never authoritative; see "ambiguity as visible
  content."
- Local-model (ollama-class) adapters — designed for, not built.
- A claimed-complete secret-detection guarantee — the pre-flight scanner
  is explicitly best-effort/defense-in-depth, not a substitute for "never
  paste real secrets into a doc" as the actual, human-side rule.
- Prompt-injection-hardened parsing of doc content. See
  docs/intent-provider-adversarial.md row 8 for why this arc's own trust
  chain bounds the blast radius of a successful injection without
  claiming to prevent one.
