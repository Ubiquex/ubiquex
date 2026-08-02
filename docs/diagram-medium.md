# Diagram medium — D2 only (UBI-47)

> Session 1, design only, no code. The fourth authoring medium, and the
> first that is bidirectional by construction: **parse** (a `.d2` file →
> an `intent/v1` draft, via `ubx propose --from-diagram`) and **render**
> (a ledger's own `FoldState` → canonical `.d2`, via a new `render`
> command with `--check`). v1 scope is **D2 only** — a founder decision,
> not revisited here; Mermaid and other formats are deferred, each
> earning entry later via the identical conformance-fixture discipline
> every other pluggable surface in this project already uses (adapters,
> providers, ledger stores).
>
> Two hard constraints came pre-decided from the ticket's own design
> room and are not relitigated here: **text or it isn't a medium** (D2 is
> parseable text; PNG/SVG are render *products*, regenerated on demand,
> never read back — no image interpretation anywhere in this medium's
> own path) and the **lossy-medium rule** (diagrams author topology only
> — nodes, containers, edges — never attributes; annotations render from
> ledger truth, never author into it). Go-native: `oss.terrastruct.com/d2`
> as a library, no subprocess — verified this session to actually offer
> exactly the narrow surface needed (parser/compiler/formatter, zero
> rendering dependencies pulled in), not assumed from the module's own
> name.

## Scope: what this session designs, and what it doesn't

Designed here: the canonical D2 subset (what ubx parses and emits, and
why `full D2` isn't safe for `render --check`); the lossy-medium rule
applied concretely (which D2 constructs map to topology, which are
excluded from the topology hash); the cross-stack grammar (`@stack.type.
name` labels, `class: external`, how a bare reference resolves to a real
`ledger_dir`); a genuinely new, additive wire-format capability this
medium's own "topology only, no attribute guessing" principle requires
(`ResourceIntent.DependsOn`, docs/schema.md's own amendment, below); type
inference for a node's own `class:` value (reusing `resolver.
InferProvider`, UBI-43, unchanged); the render direction's own design
(no synthetic containers, per-provider classes, `render --check`'s
byte-compare contract); the adversarial program; implementation slices
toward a real `.d2` payments stack.

Not designed here, named so it isn't assumed covered: Mermaid or any
second diagram format (the frontend-interface shape is named, not
built); the Studio-style live canvas / drag-to-propose authoring (Nexus-
era, explicitly named in the ticket as sitting *behind* this arc, needing
this arc's parse+emit as its own enabling layer — not started); the
actual `render` command's full flag surface beyond `--check` (e.g. output
targeting, watch mode); PNG/SVG rendering itself (out of scope
permanently, not just this session — see "Text or it isn't a medium,"
above).

## The canonical D2 subset — verified empirically, not assumed

`oss.terrastruct.com/d2` is a real, large module (`d2renderers`,
`d2layouts`, `d2plugin`, `d2exporter` — the actual rendering/layout
engine, pulling in `playwright-go`, image/PDF libraries, the works), but
it also ships the narrow layer this project actually needs as
**separately importable packages with none of that pulled in**:
`d2parser` (text → AST), `d2compiler` (AST → a compiled `d2graph.Graph`:
objects, edges, containment, resolved classes), `d2format` (AST →
canonical text). Confirmed this session, not assumed from the package
names alone, by actually compiling and round-tripping a real diagram
through exactly these three packages and nothing else.

**`d2format.Format` is genuinely idempotent — confirmed directly, the
load-bearing property `render --check`'s whole byte-compare contract
depends on.** A real diagram (containers, typed nodes via `class:`, a
cross-stack reference node, edges with and without labels) was parsed,
formatted, **re-parsed from its own formatted output, and formatted
again** — the second pass's bytes were identical to the first,
byte-for-byte. This means ubx doesn't need to hand-roll a canonical D2
serializer at all: `d2format.Format` already **is** the canonical form,
reused directly, the same "don't reinvent what a real library already
gives you for free" instinct this project applied to `ctyjson.
UnmarshalType` (UBI-33/34) and `hclsyntax`/`d2format`'s own upstream
precedent, not a fresh claim invented for this arc.

**A real trap found and avoided: D2 has no free-form "custom key"
channel on a shape.** The first, tempting design — annotate a node's
resource type with an arbitrary key, e.g. `db: primary-db { ubx_type:
aws_db_instance }` — was tested directly before committing to it.
**It's wrong**: D2's own grammar treats *any* `key: value` pair inside a
shape's body as declaring a **nested child shape** unless the key is one
of D2's own small set of reserved attribute names (`shape`, `style`,
`icon`, `label`, `tooltip`, `link`, `near`, ...). `ubx_type: aws_db_
instance` compiles successfully, but produces a spurious child object
`db.ubx_type` labeled `"aws_db_instance"` — silently corrupting the
topology with a phantom resource, not an error. This would have shipped
a real, silent data-corruption bug if the first idea had gone
unverified.

**The real mechanism, confirmed working: D2's own `class:` keyword.**
D2 supports a top-level `classes: { <name>: { <style attrs> } }` block
and a `class: <name>` attribute on any shape, referencing one — D2's own
CSS-like mechanism for shared styling, per its own doc comment
(`d2graph.Attributes.Classes`: "attached to the rendered elements in SVG
so that users can target them however they like"). Nothing stops a class
definition's own body from being **empty** — `classes: { aws_db_
instance: {} }` — carrying zero actual styling, purely a name. **ubx's
own convention: a node's `class:` value, when it matches a real provider
type string, IS that resource's type** — reusing D2's own idiomatic
mechanism exactly as designed, not repurposing something D2 didn't
intend it for. This is also, not coincidentally, exactly what the
ticket's own render-direction language already named
("per-provider icon classes, `aws_*`/`google_*`/`helm_*`") — parse and
render share one convention, not two.

**This is also why "styling-only change, topology hash unchanged" is a
real, checkable requirement, not a hope**: a class's own *assignment* to
a node (`Classes: []string`, which class NAME a node has) is
topology-relevant — it's how type inference works — but a class's own
*definition body* (its style attributes) is pure presentation. The
topology hash (defined precisely below) is computed over the parsed,
resolved topology model — type/name/depends_on/cross-refs — never over
a class's own style block, so editing `classes.aws_db_instance.style.
fill` changes the `.d2` file's own bytes (and therefore its raw
`content_hash`, see below) without changing the topology hash at all.

## Node naming: the D2 **label**, not the D2 key

A D2 shape's own identifier (`db` in `db: primary-db { ... }`) and its
label (`primary-db`, the string value) are two different things — D2
resolves edges/containment against the identifier, but a diagram author
thinks and writes in labels. **ubx resource names come from the label**
(falling back to the identifier when no distinct label is given — D2's
own default), matching this project's own existing naming convention
throughout (`payments-db-replica`, `payments-db`, `widget1` — human-
readable strings, not short internal handles) and matching what a
diagram author would naturally write. A label that starts with `@` is
never treated as a resource name at all — see "Cross-stack grammar,"
below, where that prefix means something else entirely, checked first.

## Containers: pure grouping, zero effect on resource identity

`containers → grouping` (the ticket's own framing) is taken literally
and narrowly: **D2 containment contributes nothing to a resource's own
name or address.** A node's ubx resource name is its own label, full
stop, regardless of how deeply nested it is in the diagram — container
nesting is organizational/visual only, folded away entirely once parsing
reaches the intent/v1 draft. This is a real, deliberate design choice
against the tempting alternative (fold a node's ancestor container path
into its own name, e.g. `primary.db`) for a concrete reason: ubx
addresses are already `<stack>.<type>.<name>` with `.` as the field
separator; embedding a second, diagram-specific `.`-joined path *inside*
the `name` segment would make the canonical address string genuinely
ambiguous to read back apart, a self-inflicted parsing problem this
design avoids by construction rather than working around later.

**The one stack per diagram rule, matching every prior medium's own
precedent exactly**: `ubx propose --from-diagram <file>.d2 --stack
<stack>` takes the target stack as an **explicit flag**, the identical
shape `ubx propose --from-doc <file>.md --stack <stack>` already has —
never inferred from the diagram's own top-level container structure.
This sidesteps a real, otherwise-open question (which top-level
container, if a diagram has several, would "be" the stack?) by not
asking the diagram to answer it at all. Multiple top-level containers in
one `.d2` file are legal and meaningful only as visual grouping within
the one stack the invocation names — never as a signal of a second
stack; the ticket's own "diagrams author their own stack ... never the
neighbor" rule is enforced structurally this way, not by a check that
could be gotten wrong.

**Containment ambiguity (the ticket's own named adversarial case) is a
real thing, and it's a naming collision — reused, not reinvented.**
Two nodes in different containers sharing the same label (`payments.
primary.db` and `payments.replica.db`, both labeled `"db"`) would
resolve to the identical ubx address (`<stack>.<type>.db`) once
container nesting is folded away. This is caught by `core/resolver`'s
own existing, unmodified `ErrDuplicateResource` check — the same check
a hand-written intent file naming the same `(type, name)` twice already
hits today. No new duplicate-detection code for diagrams; the parser
just needs to let this existing check actually fire (never silently
disambiguate by appending a container-path suffix on the diagram's own
behalf, which would be exactly the kind of silent guess this project's
whole design center refuses).

## Type inference: `resolver.InferProvider`, reused completely unchanged

A node's `class:` value is a **type name**, never a provider — exactly
the same shape a hand-written intent file's own `resources[].type`
already has (docs/architecture.md's own Multi-provider stacks section:
"Intent files name only types... resolved by asking each declared
provider's schema which types it owns"). This is why "type inference for
unlabeled nodes" needs no new inference machinery at all: a node's own
`class:` string is handed to `resolver.InferProvider(providers, class,
nil)` (UBI-43, unchanged) exactly the way a resolved intent file's own
type string already is. The two failure modes this already produces are
reused verbatim, not reimplemented:

- **`ErrUnknownType`** — no declared provider's schema owns this class
  name at all. This is the ticket's own "node with no inferable type"
  row.
- **`ErrAmbiguousType`** — more than one declared provider's schema
  claims it, and the node names no `provider:`-hint the way a hand-
  written intent file's own `ResourceIntent.Provider` field already
  supports.

**A node with no `class:` at all is not a type-inference problem — it's
not a resource.** Given the trap found above (D2 has no safe custom-key
channel), a class-less node has genuinely no ubx-legible type signal;
rather than guess from a free-text label ("database", "cache" — fuzzy
matching that isn't schema-ownership inference at all, and isn't
proposed here), a class-less node is **excluded from `resources[]`
entirely**, with a `blocking: true` question entry (the exact mechanism
below) naming the node and asking the author to add a `class:`. This is
the honest, lossy-medium-consistent answer: a diagram that doesn't say
what something is doesn't get to have ubx guess.

## Ambiguity as visible content — reusing UBI-41's wire fields, not its adapter

**The diagram parser is fully deterministic — there is no LLM in this
medium's own path at all.** This matters for what "reuses UBI-41's
validation/ambiguity machinery" actually means: it is **not** the
`intentprovider.Adapter`/`DraftWithRetry`/Claude-API machinery (there is
nothing here for an LLM to interpret — a `class:` string either resolves
via schema-ownership inference or it doesn't, mechanically). What's
reused is narrower and already generic: `core.Intent`'s own
`Assumptions`/`Defaults`/`Questions` fields (`core/proposal.go`,
`AmbiguityNote{Text, Affects}`, `Question{Text, Affects, Blocking}`) —
wire-format content UBI-41 introduced for LLM interpretive gaps, proven
here to generalize to a second, entirely different KIND of gap: a
deterministic parser's own structural ambiguity (an uninferable or
ambiguous type). A `blocking: true` question is added whenever a node
can't be resolved to a single type, naming the node and the reason
(`ErrUnknownType`/`ErrAmbiguousType`'s own message); the draft is still
produced, complete and valid, with that one node's own resource simply
absent from `resources[]` — never a whole-diagram refusal for one bad
node, the identical "never an empty draft, never a refusal" posture
`docs/intent-provider.md` already established, now demonstrated to be a
property of the *ambiguity-as-content design*, not of having an LLM in
the loop specifically.

**`intent.sources` gets a single `"document"` entry — not a new
diagram-specific kind.** Same reasoning UBI-33/34 session 4 already
established for the SDK medium, extended here rather than re-derived: a
deterministic producer with no adapter in the loop has nothing analogous
to `intent_provider`'s own "which model drafted this" fact worth a
second entry. `{"kind": "document", "ref": "payments.d2", "content_
hash": "sha256:<hex of the RAW .d2 file's own bytes>"}` — computed over
the **raw** file, unmodified, matching every other `document` producer's
own established rule (`docs/intent-provider.md`: "computed over the RAW,
unredacted file") — this is a tamper-evidence hash, not a topology
comparison, and deliberately does NOT exclude styling (see "The topology
hash," below, for the genuinely different, second hash that does).
Three real medium producers (prose, code, diagram) now converge on one
shared provenance vocabulary — not a coincidence, the same unification
decided once and reused.

### `propose --from-diagram`, not `resolve --from-diagram` — a real, deliberate divergence from the SDK's own CLI shape

UBI-33/34 chose `ubx resolve --from-code` — one step, no draft-review
gate — specifically because a typed program has no ambiguity at all
("it says what it says," session 4's own framing). A diagram is
different in exactly the respect that decision hinged on: **a diagram
parse CAN produce real, visible ambiguity** (an uninferable or ambiguous
node type, above) — deterministic, not LLM-interpretive, but genuinely
requiring human review before the draft should be trusted, the same
reason the md medium needs a two-step `propose` (draft, reviewed) then
`resolve` (accepted as input) rather than one. **`ubx propose --from-
diagram <file>.d2 --stack <stack>` is the right shape, matching the md
medium's own two-step flow, not the SDK's one-step one** — the CLI
surface tracks whether a medium can produce visible ambiguity, not
whether an LLM is involved.

## Cross-stack grammar: `@stack.type.name` labels, never `@stack.type.name` keys

**A reference is a labeled node, never a keyed one — checked
empirically, not assumed, because D2's own key-path syntax would make
the obvious-looking alternative silently wrong.** D2 keys use `.` as its
own container-nesting separator; a node keyed literally
`@payments.aws_db_instance.staging` would not create one node with that
identifier at all — D2 would parse it as **three nested containers**
(`@payments` → `aws_db_instance` → `staging`), corrupting the topology
exactly the way the custom-key trap above did. The **label** is D2's own
opaque-string channel — confirmed directly this session: `staging_ref:
"@payments.aws_db_instance.staging" { class: external }` round-trips the
address string byte-for-byte, with `@`, every `.`, all preserved
verbatim, because a quoted label is just a string to D2, never parsed as
a path.

**Grammar, pinned:** a node is a cross-stack reference, never a create,
when **either** its label starts with `@` (the address it names) **or**
its `class` is exactly `external` (readable without needing to parse the
label at all, matching the render direction's own annotation
convention, below) — both together, as in the worked example, is the
canonical/emitted form; either alone is accepted on parse (tolerant
parse, normalized emit, the same "parse a superset, emit the one true
form" rule the canonical subset itself already follows). A reference
node's own D2 key/identifier is never significant beyond letting edges
in the SAME diagram point at it.

**Resolving a bare `@stack.type.name` to a real `ledger_dir` needs new,
explicit input this design provides two ways, matching the destroy
path's own existing `--known-dependent` shape rather than inventing a
new pattern:**

1. **Convention default**: `../<stack>` — a sibling directory named
   after the referenced stack. This is not a guess invented for this
   document; it's already the convention every cross-stack worked
   example elsewhere in this project's own docs uses (`docs/resolver.md`'s
   own `$cross` examples: `"ledger_dir": "../networking"` for a stack
   literally named `networking`).
2. **Explicit override**: a new, repeatable `--neighbor-ledger
   <stack>=<path>` flag on `ubx propose --from-diagram`, for the real
   case where the convention doesn't hold.

A referenced stack matching neither the convention directory nor an
explicit override is the ticket's own "external-node without resolvable
stack" row: a clear, named parse-time failure (which node, which stack
name, which directory was checked and wasn't there) — never a silent
skip, never a guessed path.

**Correction, found during implementation (session 2), not assumed
correct from this paragraph's own original draft**: a reference node
does NOT become a real `$cross` marker wherever a diagram's own edges
point at it — `$cross`'s own wire shape lives inside a specific config
attribute path by definition, and a topology-only edge names no
attribute, the identical gap `DependsOn` exists to close for intra-stack
edges but genuinely can't close here (there's no "wait for creation"
ordering concern to substitute for the missing attribute the way an
intra-stack dependency has). An edge into a reference node becomes a
visible, non-blocking note instead — a real, named v1 limitation, not a
silent drop. See "Slices 1–2: built," below, for the full account of
finding this gap and the exact fix.

## A genuinely new, additive wire capability: `ResourceIntent.DependsOn`

**The lossy-medium rule creates a real gap the existing intent/v1 format
doesn't close, found by trying to design around it rather than assumed
away.** An edge `A -> B` in a diagram is pure topology — "A depends on
B" — but today's `$ref`/`$cross` markers live **inside a specific
config attribute path** (`docs/schema.md`: `"replicate_source_db":
{"$ref": {...}}`), and the resolver only ever derives a create's own
`depends_on` by scanning config for exactly those markers
(`core/resolver.go`, confirmed by reading the real dependency-graph-
building code, not assumed). A diagram edge names no attribute at all —
authoring one would mean guessing a schema-specific attribute name from
a topology-only signal, exactly the "two mediums claiming the same
attribute" failure the lossy-medium rule exists to prevent.

**The honest fix is a new, additive input field, not a workaround**:
`ResourceIntent` (`core/resolver/resolver.go`) gains `DependsOn
[]string` — canonical addresses, the same convention `Modification.
DependsOn`/`Delta.Creates`'s own output-side `depends_on` already use —
an **input-side** sibling naming dependencies with no config-attribute
opinion at all. The resolver honors it directly: added to the same
dependency graph `$ref`/`$cross` scanning already builds (union, not a
second graph), included verbatim in the resolved output's own
`depends_on` (already a real field, already handles multiple
contributing reasons for one edge — see `docs/schema.md`'s own doc
comment: "the authoritative, explicit, position-independent record of
why"). Purely additive, optional (`omitempty`), on an input struct that
has never been part of the ratified hashed-content shape — **no
`schema_version` bump**, the same reasoning every prior additive
amendment in this project's history already established.

```json
{
  "type": "aws_ecs_service", "name": "api", "op": "create",
  "config": {},
  "depends_on": ["payments.aws_db_instance.primary-db"]
}
```

**"Cycle in edges" needs no new detection code at all** — this is
exactly why the fix above routes through the *same* dependency graph
`$ref`/`$cross` scanning already builds, not a parallel one: `core/
resolver`'s own existing cycle detection (`docs/resolver.md`) already
covers every edge in that graph, diagram-sourced or not, unconditionally.

## The topology hash: a second, different hash from `content_hash`

Two genuinely different questions, two genuinely different hashes —
conflating them was the wrong shortcut a first pass at this design took,
corrected here before it became load-bearing anywhere:

- **`intent.sources[].content_hash`** answers "were these the exact
  bytes parsed" — tamper-evidence, computed over the raw `.d2` file,
  styling included, matching every other `document` producer.
- **The topology hash** answers "did the *meaning* change" — computed
  via `core.CanonicalJSON` (UBI-33/34, reused unchanged) over the parsed
  `resources[]` + input-side `depends_on`, **excluding** `intent.
  summary`/`sources`/ambiguity content entirely, the identical "compare
  the resolved shape after canonicalizing, not the whole document"
  technique the SDK arc's own live finale already proved out (session 4:
  `delta.creates[]`, canonicalized, byte-compared across two independent
  producers). Never stored anywhere on the wire; a derived value the
  conformance suite and `render --check` both compute on demand.

This is what makes "styling-only change, topology hash unchanged" a real
adversarial test rather than a definitional dodge: parse the same
diagram before and after a pure `classes.*.style.*` edit, topology-hash
both, require equality, while `content_hash` is allowed (expected) to
differ.

## The render direction: `FoldState` → canonical D2

Walks a stack's own `core.Ledger.FoldState` (already-shipped, unchanged
— the same read `ubx status`'s own fleet walk already performs) and
emits one flat, top-level D2 node per live resource — **no synthetic
containers.** A real, considered decision, not an oversight: there is no
canonical grouping basis to invent (by type? by dependency cluster? by
original diagram structure, which render has no access to and
shouldn't need) that wouldn't be an unreviewable guess baked into every
emitted diagram; a human editing the emitted file by hand can add their
own grouping afterward, same as they can with any other generated,
reviewable artifact this project already produces (`ubx sdk gen`'s own
bindings, committed and human-editable-adjacent even though generated).

Per-resource emission, deterministic (resources sorted by `(type, name)`
before AST construction — `d2format` itself preserves input order
verbatim, confirmed this session; determinism is this project's own
responsibility to supply, not something to assume a formatter grants for
free):

- **`class: <type>`** — the exact type string, the identical convention
  parse already reads, giving render/parse one shared vocabulary instead
  of two.
- **Attribute annotations** — real, current ledger values (this is the
  lossy-medium rule's *other* half: annotations render from truth, they
  just never author into it) via D2's own `tooltip:`/label-suffix
  mechanisms — exact rendering convention left to the implementation
  session (a real, small UI decision, not a wire-format one).
- **Cost**, where a resource's own recorded cost data exists — same
  annotation channel.
- **`$cross` edges** — a reference node (`class: external`, `@stack.
  type.name` label) annotated with the pinned neighbor head
  (`resolution.inputs[].pinned_head`, already real, unchanged) so a
  rendered diagram shows staleness risk at a glance, not just topology.

**`render --check`**: re-emit, byte-compare against a target file
(matching the exact "regenerate and diff" shape `ubx sdk gen`'s own
generated-file discipline already established, and `d2format`'s
confirmed idempotency above is exactly what makes a stable byte-compare
possible at all). Exit non-zero on any difference — the founding
projection invariant (`docs/architecture.md`), enforced for a fourth
projection surface with the identical mechanism, not a bespoke one.

## Adversarial program

Matching every prior arc's own discipline: each row is a required
observable outcome, reused-mechanism citations included so a future
implementer isn't left re-deriving why a row needs no new code.

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 1 | Cycle in edges | Diagram edges `A -> B -> C -> A`, translated to `depends_on` on each create. | Caught by `core/resolver`'s own existing, unmodified cycle detection (the new `ResourceIntent.DependsOn` field feeds the identical dependency graph `$ref`/`$cross` scanning already builds) — a clear cycle error naming the addresses involved, never a resolved-but-unorderable proposal. |
| 2 | No inferable type | A node with no `class:` attribute at all. | Excluded from `resources[]`; a `blocking: true` question names the node and asks for a `class:`. The rest of the diagram still resolves into a complete, valid draft — never a whole-file refusal for one under-specified node. |
| 2b | Type ambiguous across providers | A node's `class:` names a type two declared providers both own, no `provider:` hint given. | `resolver.InferProvider`'s own existing `ErrAmbiguousType` fires, reused unchanged — same outcome a hand-written intent file naming the same ambiguous type already gets today. |
| 3 | Containment ambiguity | Two nodes in different containers share the same label, folding to the identical `(type, name)` address once containment is discarded. | `core/resolver`'s own existing, unmodified `ErrDuplicateResource` fires — never a silent disambiguation (e.g. appending a container-path suffix on the parser's own initiative). |
| 4 | Styling-only change | Two versions of the same diagram differ only in `classes.*.style.*` attributes — identical topology otherwise. | The two versions' own `content_hash` values differ (real bytes changed); their topology hashes (`core.CanonicalJSON` over `resources[]`+`depends_on`) are byte-identical. `render --check` against either version's own re-emitted form succeeds. |
| 5 | Tampered diagram post-pin | A `.d2` file referenced by an already-accepted proposal's `intent.sources` is edited after the fact. | The existing content-hash tamper-detection mechanism (already built, already reused unchanged by every prior `document`-kind producer — chat's dialogues, the md medium) catches it; no new verification code for diagrams specifically. |
| 6 | D2 parse errors | Malformed `.d2` syntax (unclosed brace, invalid key path, ...). | `d2parser`/`d2compiler`'s own real, structured parse error surfaces verbatim (file, line, column — D2's own error type already carries this) — never swallowed, never a partial/best-effort topology. |
| 7 | External node without a resolvable stack | A node labeled `@nonexistent-stack.aws_vpc.main`, no `../nonexistent-stack` directory, no `--neighbor-ledger` override given. | A clear, named parse-time failure: which node, which stack name, which directory (the convention default) was checked and wasn't there — never a silent skip of that one edge, never a guessed path. |

## Implementation slices, toward a real `.d2` payments stack

### Slices 1–2: built (2026-07-28, session 2)

Both real code, hermetically tested, including two real end-to-end
tests confirming the adversarial table's own "reused, not reinvented"
claims actually hold when `diagram.Parse`'s own output flows into the
real, unmodified `resolver.Resolve` — not just asserted from design.

**`ResourceIntent.DependsOn`** (`core/resolver/resolver.go`,
`core/resolver/refs.go`'s new `unionDependsOn`): built exactly as
designed, no corrections needed. Merges into the *same* dependency graph
`scanRefEdges` already builds from `$ref`/`$cross` scanning — an address
inside the current resolve batch becomes a real graph edge (execution
ordering, cycle detection, the resolved output's own `depends_on`); an
address already recorded in the ledger (not in the batch) is validated
via `l.FoldState` — the identical lookup `resolveRef`'s own non-batch
`$ref` case already uses — but never added as an edge, matching that
exact case's own existing behavior (nothing to wait for). A dangling
address (neither in the batch nor the ledger) reuses `ErrRefNotFound`
verbatim, and a dependency naming something the same proposal also
destroys reuses `ErrRefToDestroyTarget` verbatim — both the identical
sentinels a `$ref`'s own equivalent failure already produces, confirming
"one dependency graph, not two" is real, not just a claim. Eight
hermetic tests (`core/resolver/dependson_test.go`).

**The topology parser** (`diagram/parse.go`, new top-level package,
matching this project's own established shape for a substantial,
independently testable subsystem — `intentprovider/`, `sdkeval/`,
`sdk/codegen/`). `d2compiler.Compile` → a two-pass walk (classify every
leaf node first, translate edges second, since an edge's own
translation needs both endpoints already classified) → `resolver.
IntentFile`. Built exactly as designed: containers skipped entirely
(`len(obj.ChildrenArray) == 0` gates every leaf), node names from
`obj.Label.Value` falling back to `obj.ID`, type inference via
`resolver.InferProvider` completely unchanged, uninferable/ambiguous
nodes excluded from `Resources` with a `blocking: true` `core.Question`
naming the node, cross-stack reference nodes (`@`-label or `class:
external`) recognized and excluded from `Resources` too, `../<stack>`
convention + `--neighbor-ledger`-shaped override map for `ledger_dir`
resolution (a real `os.Stat` existence check — a deliberate, named scope
boundary: confirming the *directory* exists, not the deeper "is this
exact address recorded there" check `$cross`'s own resolve-time
`cross_stack_pin` mechanism performs, since v1's own cross-stack edges
never produce a real `$cross` marker at all — see the correction below).
Thirteen hermetic unit tests (`diagram/parse_test.go`) plus three
integration tests (`diagram/integration_test.go`) that feed real `Parse`
output through the real, unmodified `resolver.Resolve` and confirm
`ErrCycleDetected`/`ErrDuplicateResource` fire exactly as the
adversarial table's own rows 1 and 3 claim — `Parse` itself deliberately
never detects either case, proving the "one shared mechanism" claim
rather than merely asserting it.

**A real, honest correction found during implementation, not papered
over**: this document's own original text for cross-stack edges ("see
`depends_on`... for how a topology-only edge reaches a marker at all
without guessing which config attribute it belongs in") turned out not
to actually resolve on inspection — `$cross`'s own wire shape has no
form reducible to a bare address the way an intra-stack dependency does
(there is no "wait for creation" ordering concern to substitute for the
missing attribute; a `$cross` marker's whole reason for existing is
that it *lives in* a specific attribute, resolved to a concrete value
immediately). Thought through properly rather than forced: **a
topology-only edge into a reference node cannot express a real `$cross`
marker in v1 at all** — a genuine, structural limit of what a
diagram can say, not a bug to route around. The reference node is still
fully recognized (type, `ledger_dir`, existence-checked), and an edge
into it becomes a **visible, non-blocking `core.AmbiguityNote`** (a
`defaults[]` entry) naming the relationship and stating plainly that it
isn't wired into the wire-level output — reviewable content, not a
silently dropped edge, matching this arc's own "ambiguity as visible
content" design center exactly, just applied to a structural limitation
instead of an interpretive gap. Named explicitly in "Out of scope,"
below, not left implicit.

`go build/vet/test`, `gofmt -l .` clean across the whole repo (8 new
tests in `core/resolver`, 16 new in the new `diagram` package).

### Slice 3: built (2026-07-28, session 3)

`ubx propose --from-diagram <file>.d2 --stack <stack> [--summary <text>]
[--neighbor-ledger <stack>=<path>] [--out <path>]` (`cli/propose.go`),
matching `--from-doc`'s own shape exactly: read the file, produce an
`intent/v1` draft, render its ambiguity content, write the draft. No
corrections needed — session 2's own design (the parser, `DependsOn`,
the `$cross` structural-limitation note) wired through unchanged, only
new CLI-layer glue.

Three real decisions made this session, none contradicting prior design:

- **Two-step, not one-step.** `--from-diagram` stops at a written draft,
  the *same* shape as `--from-doc` (never auto-resolves), not `ubx
  resolve --from-code`'s own one-step shape — because a diagram parse
  can produce real, visible ambiguity (an uninferable/ambiguous node
  type, or the `$cross` limitation itself), so it needs the same
  human-review checkpoint the md medium's own ambiguity does before
  anything reaches a ledger.
- **No legacy single-provider fallback.** Requires a real `[providers]`
  table, matching `ubx sdk gen`'s own precedent (`cli/sdk.go`) — both are
  post-UBI-43 features with no pre-multi-provider shape to fall back to.
- **A standalone `loadDiagramProviders` helper, not a `cli/resolve.go`
  refactor.** `resolve.go`'s own inline provider-loading block holds
  every client open until command exit; `--from-diagram` only needs each
  provider's already-fetched static schema (confirmed via
  `newSchemaInspector`'s own implementation — it wraps `*provider.
  Schemas`, no live client dependency), so its own helper closes each
  client immediately after fetching schema instead. Matches `cli/sdk.go`'s
  own precedent of a command owning its own provider-loading logic rather
  than sharing `resolve.go`'s; touching that existing, tested code was
  out of scope for a "CLI wiring" slice.

Sources provenance: a single `"document"`-kind `intent.sources` entry
naming the diagram file and its content hash (`intentprovider.
HashDocument`, reused unchanged) — the same single-entry precedent
`sdkeval`'s own `stampDocumentSource` established for the SDK arc,
appropriate here too since there's no LLM adapter round trip to add a
second entry for, unlike `--from-doc`'s own two-entry shape.

Ambiguity rendering: `cli/intentrender.go`'s existing `renderAmbiguity`
reused completely unchanged — it already operates on `*resolver.
IntentFile`, exactly what `diagram.Parse` returns, so the `$cross`
structural-limitation note (a `defaults[]` entry) and any blocking
type-inference questions render to the terminal identically to how
`--from-doc`'s own ambiguity content already does, with zero new
rendering code.

Five hermetic CLI tests (`cli/propose_from_diagram_test.go`): missing
`[providers]` table rejected naming it; missing `--stack` rejected;
three-way mutual exclusivity (`proposal.json` arg, `--from-doc`,
`--from-diagram`) enforced; a real end-to-end run via the
`UBX_PROVIDER_MIRROR` seam (matching `cli/sdk_test.go`'s own mechanism)
proving the `$cross` note renders, the draft's own resources/sources are
correct, and `--neighbor-ledger` resolves a real cross-stack reference;
an unambiguous diagram renders the plain "no assumptions, defaults, or
open questions" message with a `--summary` override honored verbatim.
Also live-verified by hand outside the test suite, via a real built
binary against the `fakeprovider` mirror — the same `Defaults (1):` note
and draft shape the tests assert.

`go build/vet/test`, `gofmt -l .` clean across the whole repo.

### Slice 4: built (2026-07-28, session 4)

`diagram.Emit(l *core.Ledger, stack string) ([]byte, error)`
(`diagram/emit.go`, new) plus `ubx render --stack <stack> [--out <path>]
[--check]` (`cli/render.go`, new) — the render half of the medium, the
literal converse of `Parse`: walk `core.Ledger.Fleet(stack)` + `FoldState`
(the same read `ubx status`'s own fleet walk already performs), build D2
source text, run it through `d2parser.Parse` → `d2format.Format` for the
canonical byte form — reusing D2's own confirmed-idempotent formatter as
the canonical serializer exactly as designed, never a hand-rolled one.
One flat top-level node per live resource, no synthetic containers, as
designed. `render --check`: re-emit, byte-compare against `--out`'s own
current content, `writeback.go`'s own `unifiedDiff` reused unchanged for
the printed diff; exit 1 (a real, actionable "the committed rendered/
copy is stale" finding, UBI-20) on any difference, exit 2 on a real
error — `--check` without `--out` is refused outright (there is nothing
to compare against).

**A real, load-bearing gap found while building this slice, not present
in session 1's own design — `resolution.inputs[].pinned_head` alone
turned out not to be enough.** The render direction's own text says a
`$cross` edge gets "a reference node ... annotated with the pinned
neighbor head," but reading the real `resolveCross`/`resolveOnce` code
(not assuming the UBI-27 amendment already covered this) showed a
`cross_stack_pin` entry's own `resource` field has always named the
**neighbor's** address, never the **local** resource whose config held
the `$cross` marker — and `resolveOnce` flattens every resource's own
resolution inputs into one proposal-wide slice with no back-reference at
all. There was no way, from a resolved proposal alone, to answer "which
of my own resources references this neighbor pin" — exactly the
question `Emit` needs answered to draw the edge from the correct node.

**Fixed at the source, not worked around in the emitter**:
`core.ResolutionInput` gained a new, optional, additive `From` field
(the referencing resource's own address) — `resolveValue`/`resolveCross`
(`core/resolver/refs.go`) both gained a `from string` parameter, threaded
from `resolveOnce`'s own per-resource loop (always `e.addr.String()`,
constant across that resource's whole config walk). Purely additive, no
`schema_version` bump, same reasoning as every prior amendment to that
struct — see docs/schema.md's own "Amendment: `ResolutionInput.From`"
section. One hermetic regression test
(`core/resolver/resolver_test.go`'s
`TestResolve_CrossStack_ResolutionInputRecordsReferencingResource`): two
resources in the same batch, only one cross-referencing, proves the
attribution is genuinely per-resource.

**Other real, deliberate rendering decisions, named so they aren't
assumed obvious**:

- **Synthetic `r0`/`r1`/... D2 keys, never the resource's own name.** Two
  different-typed resources can legally share the same `Name` (only the
  full `(type, name)` pair is unique in a ledger address), and a D2 key
  built by joining `type` and `name` with `.` would collide with D2's own
  container-nesting separator — the exact trap the canonical-subset
  section already found and avoided on the parse side. Sequential keys,
  assigned in the same `(type, name)`-sorted order the design calls for,
  are collision-free by construction; nothing about readability is lost
  since the resource's own name still renders in full as the node's own
  D2 label.
- **Attribute annotations render via `tooltip:`, not `label:`/a suffix.**
  Keeps the diagram itself scannable (label stays just the resource
  name); every top-level resolved attribute, sorted, `key: value; ...`,
  available on hover — a real, small UI choice the design doc explicitly
  left to this session, not a wire-format one.
- **No per-resource cost annotation — a real, honest scope decision, not
  an oversight.** Checked directly before assuming the design's own "cost,
  where a resource's own recorded cost data exists" line was
  implementable: there is no per-resource cost field anywhere in the
  ledger (`core.CostDelta` is proposal-level only, and even that is
  presently always hardcoded to `"0"` at every call site). Nothing to
  annotate from yet — named here explicitly rather than silently
  skipped; revisit only once a real per-resource cost source exists.
- **Reference nodes deduplicated by neighbor address.** Two resources
  pinning the identical neighbor address share one reference node
  (multiple incoming edges) rather than drawing a redundant duplicate —
  never mandated either way by the design text, a deliberate, defensible
  rendering choice.
- **A depends_on/cross-pin lookup that degrades gracefully, never hard-
  fails a whole render.** `Emit` reads each live resource's own
  creating/most-recently-modifying proposal (`FleetEntry.ProposalID`) to
  find its `depends_on` and `cross_stack_pin` entries; if that proposal
  recorded neither (its `resolution.inputs` touched this address some
  other way), the resource simply renders without edges rather than
  erroring — the same "annotate, don't refuse" posture the render
  direction's own text already holds for a missing attribute.

**Proven round-trip, not just each direction tested in isolation**:
`diagram/emit_test.go`'s `TestEmitD2_RoundTripsThroughParse` feeds
`Emit`'s own output back through the real, unmodified `Parse` and
confirms the resources, `depends_on`, and the `$cross` structural-
limitation note all come back correctly — real proof of "render/parse
share one convention, not two," this medium's own bidirectional-by-
construction design center.

Nine unit tests (`diagram/emit_test.go`, `emitD2` exercised directly:
dependency edges, cross-stack annotation, reference-node dedup, the
name-collision-across-types case, empty-stack, no-attrs, determinism,
format-idempotency, the round-trip above) plus ten CLI tests
(`cli/render_test.go`, a real `resolve → accept → ship` pipeline against
the hermetic `fakeprovider` binary via `UBX_PROVIDER_MIRROR` — never a
real cloud provider, see below): a two-resource `$ref`-derived
dependency chain, `--out` writing a file, `--check` matching/stale/
missing, `--check` without `--out` refused, `--stack` required, an empty
stack rendering to empty output, byte-identical output across repeated
runs, and a real two-ledger cross-stack scenario proving the `From` fix
end to end (not just at the `emitD2` unit level) — a real reference node,
annotated with the real pinned head, edged from the correct resource.
`go build/vet/test`, `gofmt -l .` clean across the whole repo.

**A real, costly mistake made and corrected this session, recorded
honestly rather than glossed over**: initial by-hand live verification
of `ubx render` was run against the real, already-credentialed
`hashicorp/aws` provider all the way through `ubx ship` — not just
`resolve`/`propose` (read-only schema-fetch, safe), but a real `apply`,
creating three real AWS VPCs and starting a real RDS instance in the
user's live account. Caught by checking real AWS state directly before
going further; all four resources were confirmed and deleted with the
user's explicit go-ahead (see STATE.md for the full incident account).
Every real transcript in this section and in ubiquex-docs' own render
guide/reference page comes from a redone, fully hermetic live
verification against the `fakeprovider` binary via `UBX_PROVIDER_MIRROR`
instead — the same safe mechanism session 3's own `propose
--from-diagram` verification already used correctly. `hashicorp/aws`
(or any real provider) stays safe for `resolve`/`propose`/`sdk gen`
(schema-fetch or draft-only); never for anything reaching `ship`.

### Slice 5: built (2026-07-28, session 5)

`diagram.Topology(intent *resolver.IntentFile) ([]byte, error)`
(`diagram/topology.go`, new) — the "topology hash" concept's own first
real code, previously only described in prose (above): `core.
CanonicalJSON` over `resources[]` (type, name, op, depends_on) + stack,
sorted by `(type, name)` internally so the function's own determinism
never depends on its caller happening to hand it resources in a stable
order, excluding `intent.summary`/`sources`/ambiguity content and
`config` entirely — a general "did the meaning change" primitive, not
diagram-specific, even though this is the arc that motivated it.

**Conformance fixtures**, `payments` as fixture #1 (matching every other
medium's own fixture name), both directions, split across two packages
deliberately — not an oversight, see below:

- **Parse direction** (`diagram/conformance/golden/payments.d2` ↔
  `diagram/conformance/golden/payments-topology.json`, tested in new
  `diagram/conformance/runner/`, package `runner`, mirroring `sdk/
  conformance/runner`'s own shape exactly): the real, unmodified `Parse`
  → `Topology`, canonicalized, byte-compared against the committed
  topology fixture (itself pretty-printed for reviewability, canonicalized
  at test time on both sides — the same posture `sdk/conformance`'s own
  golden fixture already holds). Self-contained: no subprocess, no real
  provider binary — `diagram.Parse`'s own type inference only ever calls
  `SchemaInspector.HasType` (never `IsComputed`/`IsSensitive`, which only
  matter once a resource's own config VALUES get resolved, and a diagram
  never authors any), so a tiny hermetic fake schema suffices.
- **Render direction** (the identical topology, shipped for real through
  the hermetic `fakeprovider` binary via `UBX_PROVIDER_MIRROR`, emitted,
  byte-compared against `diagram/conformance/golden/payments-rendered.d2`)
  lives in `cli/render_conformance_test.go` instead, a deliberate package
  split: `Emit` needs a real, shipped `Fleet` entry (`FoldState` only
  reports "live" after a real apply), which needs the full `core/
  executor.Applier` adapter (`Schema`/`Configure`/`ReadResource`/
  `PlanResourceChange`/`ApplyResourceChange`, with real redaction and
  diagnostic-to-`TerminalError` classification) that already exists,
  correct and tested, in `cli/stateadapter.go` — reimplementing an
  independent copy purely to keep both directions under one roof would
  risk a real, silent divergence from the one implementation every other
  ship path already relies on, for no benefit over importing the SAME
  `diagram/conformance/golden/` fixtures by relative path instead.

**A real, deliberate, documented departure from every other medium's own
"payments" fixture**: both golden `.d2` files use `fake_widget`
throughout, never `aws_vpc`/`aws_db_instance` the way the md medium's
and SDK arc's own `payments` fixtures do. This session's own explicit
instruction was "hermetic only — no real cloud this slice" — a direct,
standing consequence of session 4's own real AWS incident (the new
CLAUDE.md/docs/prompts.md rule, below). A real `hashicorp/aws` schema
fetch is read-only and safe in isolation (exactly what session 4's own
`resolve`/`propose` calls against it were), but conformance fixtures are
exactly the kind of "just checking it still works" context where a
verification session's own scope tends to creep toward `ship` without
that being the actual intent — using `fake_widget` throughout removes
the temptation structurally rather than relying on discipline alone.
Reconciling this fixture's own values with the `aws_*` golden values
every other medium already converged on is explicitly named as slice 6's
own job (the "live finale"), not this one's.

Eight new tests total: `diagram/topology_test.go` (five unit tests —
excludes summary/sources/ambiguity, sorted independent of input order,
includes `depends_on`, excludes `config`, deterministic across repeated
calls), `diagram/conformance/runner`'s own two conformance cases (the
golden parse + its own determinism guard), plus
`cli/render_conformance_test.go`'s one golden render case. `go build/
vet/test`, `gofmt -l .` clean across the whole repo.

**The standing ship-verification rule from session 4's own incident,
now codified where every future session actually reads it**: CLAUDE.md's
own "Code conventions" section and docs/prompts.md's own "Rules embedded
in every session" section both gained a line — `ubx ship` (or anything
else reaching a provider's own `ApplyResourceChange`) is never run
against a real cloud provider for verification, demos, or doc
transcripts, only the hermetic `fakeprovider` binary via
`UBX_PROVIDER_MIRROR`; `resolve`/`propose`/`sdk gen` remain safe against
a real provider. Previously only a standing memory outside this repo
(session 4's own feedback memory) — now load-bearing project doctrine a
future session (or a different agent entirely) reads automatically via
CLAUDE.md, not something that depended on the same memory carrying
forward.

### Slice 6: built — the live finale (2026-07-28, session 6)

**Real, live, end to end — the strongest form of the claim, not an
approximation**, the same discipline the SDK arc's own live finale
(docs/sdk.md) held to. Two independent legs, per this session's own
explicit doctrine: the convergence leg runs `resolve`/`propose` against
the real, cached `hashicorp/aws@6.54.0` schema (read-only, never
touching a real cloud API); the render leg stays fully hermetic
(`fakeprovider` via `UBX_PROVIDER_MIRROR`) since it needs a real
`ship` — the exact split session 4's own incident and session 5's own
codified rule (CLAUDE.md, docs/prompts.md) require.

**The convergence leg**: a one-resource diagram —

```d2
classes: {
  aws_db_instance: {}
}
db: payments {
  class: aws_db_instance
}
```

— `ubx propose --from-diagram payments.d2 --stack payments`, real output,
real type inference against the real schema:

```json
{
  "resources": [
    { "type": "aws_db_instance", "name": "payments", "op": "create", "config": {} }
  ]
}
```

Resolved for real (`ubx resolve draft.json`, the same real
`hashicorp/aws@6.54.0` schema, no `ship`) — `delta.creates[0]`, real
output:

```json
{"config":{},"name":"payments","provider":{"source":"hashicorp/aws","version":"6.54.0"},"stack":"payments","type":"aws_db_instance"}
```

**Checked rigorously against the SDK arc's own committed golden value,
not eyeballed** — both canonicalized via `core.CanonicalJSON`, the
identical golden `delta.creates[0]` docs/sdk.md's own live finale
established:

```json
{"config":{"allocated_storage":20,"db_name":"payments","engine":"postgres","instance_class":"db.t3.small","username":"payments_admin"},"name":"payments","provider":{"source":"hashicorp/aws","version":"6.54.0"},"stack":"payments","type":"aws_db_instance"}
```

`name`, `stack`, `type`, and `provider` (source **and** version) are
byte-identical across both. **`config` is the one honest, structural,
expected difference** — empty for the diagram, real attribute values for
the golden — and it is *exactly* the lossy-medium rule made concrete,
not a gap: "two mediums can never claim the same attribute"
(docs/architecture.md's own founding framing for this arc) means a
diagram was never going to independently reproduce `engine`/
`instance_class`/`allocated_storage`/`db_name`/`username` from nothing,
by design, on purpose, from session 1 onward. What a diagram *can* and
does claim — the resource's own existence, identity, type, and
provider — matches exactly.

`diagram.Topology` (slice 5's own primitive, reused unchanged, no new
code needed to make this claim) confirms the same thing at the
topology-only layer the medium is actually scoped to:

```json
{"resources":[{"name":"payments","op":"create","type":"aws_db_instance"}],"stack":"payments"}
```

— agreeing with the golden's own topology-relevant fields exactly.
**This is what "the four-medium equality" actually means for a
topology-only producer**: not a fourth independent claim on the same
attribute values (impossible by the medium's own design), but proof that
a diagram never contradicts what the other three producers already
established, and correctly identifies the same resource by type, name,
stack, and provider. Verified directly afterward: no real AWS resources
exist (`aws ec2 describe-vpcs`/`aws rds describe-db-instances`, both
empty) — the convergence leg never shipped anything, exactly as
doctrine requires.

**The render leg, fully hermetic, against a real (shipped) ledger**: the
`payments` chain — `main-vpc` and `payments-db`, `payments-db` depending
on `main-vpc` — resolved, accepted, and shipped for real through the
hermetic `fakeprovider` binary via `UBX_PROVIDER_MIRROR`, then rendered:

```d2
classes: {
  fake_widget
}
r0: "main-vpc" {
  class: fake_widget
  tooltip: "id: computed-id; name: main-vpc; tags: {}"
}
r1: "payments-db" {
  class: fake_widget
  tooltip: "id: computed-id; name: payments-db; tags: {}"
}
r1 -> r0
```

```text
$ ubx render --stack payments --ledger-dir . --out rendered/payments.d2 --check
render --check: rendered/payments.d2 matches the current resolved state
```

Real, unedited, `render --check` green — the projection invariant held
for this fourth surface, matching every other CI-shaped guarantee this
project already documents.

**UBI-47 closed in Linear.** All seven of docs/diagram-medium.md's own
implementation slices are built, tested (hermetically and live), and
documented, closing the loop this arc's own framing opened: a diagram is
a real, bidirectional-by-construction projection of the ledger's own
truth — topology in, topology and annotated truth out, never a second
source of truth for anything it can't itself author. **Phase 3 (the
authoring frontends) is complete**: md (UBI-41), chat (UBI-46), SDK/TS
(UBI-33/34), and now diagram (UBI-47) are all live — four independent
producers of `intent/v1`, one shared resolved shape, exactly the
founding thesis this whole phase set out to prove. See docs/plan.md's
own new "Phase 3 status" section for the full scoreboard.

**A real, small doc-staleness finding fixed while closing this arc, not
left for someone else to notice**: docs/architecture.md's own headline
sections for the md medium, the SDK program, and the diagram medium each
still carried their own session-1 "designed... not yet implemented"
markers, unrevised across every session that actually built them. Fixed
in place this session for all three — a real, if minor, instance of this
project's own "never contradict docs silently" rule applying to a
system-model summary, not just a design decision.

`go build/vet/test`, `gofmt -l .` clean across the whole repo — no new
code this session (the live finale is a verification exercise, not an
implementation one), so no new tests either; the existing suite (Parse,
Emit, Topology, and both conformance directions) already covers
everything this session's own live runs exercised.

1. **The topology model + parser** — **built.** `resolver.IntentFile`
   translation (label → name, `class:` → type via `InferProvider`, edges
   → `DependsOn` for resource-to-resource edges, `@`/`external` nodes
   recognized and excluded from `Resources`); the ambiguity-as-content
   path (uninferable/ambiguous nodes → `questions[]`, never a hard
   refusal). See "Slices 1–2: built," above, for the real package
   (`diagram/`, not `d2/topology`) and the one real correction found
   while building it.
2. **`ResourceIntent.DependsOn`** — **built.** The schema.md amendment
   above, real code in `core/resolver` — union into the existing
   dependency graph, verified against the cycle-detection adversarial
   row directly, end to end, not just at the unit level.
3. **`ubx propose --from-diagram <file>.d2 --stack <stack>
   [--neighbor-ledger <stack>=<path>]`** — **built.** CLI wiring, matching
   `--from-doc`'s own shape and flag conventions exactly; writes a draft
   file, same as every other `propose` mode. See "Slice 3: built," above.
4. **The emitter + `ubx render --check`** — **built.** `FoldState` walk →
   D2 source text (sorted, deterministic) → `d2format.Format`; `--check`'s
   own byte-compare exit-code contract, matching `docs/architecture.md`'s
   founding projection invariant. See "Slice 4: built," above, including
   the real `ResolutionInput.From` fix this slice's own `$cross`-
   annotation feature needed.
5. **Conformance fixtures** — **built.** Golden `.d2` ↔ topology-JSON
   pairs, `payments` as fixture #1, both directions — reusing the SAME
   golden-shape discipline `sdk/conformance`'s own runner already
   established (canonicalize, byte-compare, a real ongoing regression
   test). See "Slice 5: built," above, including why `fake_widget`
   throughout rather than `aws_*` this session.
6. **Live finale** — **built.** The real `payments` stack authored as
   `.d2`, resolved, and — this arc's own strongest possible convergence
   claim — its resolved shape compared against the SAME golden values the
   md medium and the SDK arc's own TypeScript program already converged
   on (UBI-33/34 session 4's own real, live comparison), **and** rendered
   back from the ledger as an annotated `.d2` file, `render --check`
   passing against it. Four independent producers (hand-written JSON, an
   LLM-transcribed document, a typed program, and now a diagram) on one
   shared resolved shape — the complete set this project's own "every
   medium is a projection, never a second source of truth" thesis
   promised from the start. See "Slice 6: built — the live finale,"
   above, for the real transcripts and the one honest, structural
   difference (`config`) named rather than papered over.
7. **`ubiquex-docs`** — **built.** An authoring guide (the real worked
   diagram, both directions) and the projection story (how `render
   --check` fits alongside every other CI-shaped guarantee this project
   already documents) — `guides/diagram-medium.mdx` and `cli/render.mdx`,
   session 4; both still accurate and current, confirmed this session.

## Out of scope for v1, named so it isn't assumed covered

Mermaid or any second diagram format (the ticket's own explicit v1
scope line); the Studio-style live canvas (Nexus-era, needs this arc's
own parse+emit first); PNG/SVG rendering (permanently out of scope for
the *medium* — a render *product*, not something ubx reads back, ever);
`render`'s own full flag surface beyond `--check` (output targeting,
watch mode — real CLI details, deliberately left to the session that
builds slice 4 rather than guessed at here); a diagram-specific
attribute-annotation *format* decision (tooltip vs. label-suffix vs.
something else — a real, small UI choice, not a wire-format one, left to
implementation); fuzzy/free-text label-based type guessing (a class-less
node stays a visible question, never a best-effort NLP guess — a
permanent design boundary, not a v1 limitation to revisit).

**A real, load-bearing v1 limitation found while building slice 1
(session 2), not a design gap left unaddressed**: an edge from a create
into an external/reference node never produces a real `$cross` marker
in the resolved output — `$cross`'s own wire shape requires a specific
config attribute to live in, and a topology-only edge names none, with
no ordering-based substitute the way `DependsOn` provides for
intra-stack edges (a cross-stack reference resolves to a concrete
pinned value immediately; there's nothing to "wait for" to redirect the
missing-attribute problem toward). The relationship is still fully
visible (a `defaults[]` note naming both ends), just not wired into
`config`. A hand-written intent file, or a resource authored via the
SDK/md medium, remains the way to express a real `$cross` reference
today — revisit only if a real, concrete design for "which attribute"
emerges, not by inventing a synthetic one.

**Amendment (2026-07-31, UBI-63 session 2): the "topology only" rule now
surfaces as a clear error, not a silent wrong value, the moment it
actually matters.** `diagram.Parse` always emits `config: {}` — this was
always the documented, intentional lossy-medium rule (above), never a
gap. Before this amendment, though, a real provider schema's own
`Required` attribute silently absent from an empty config was sent to
`ApplyResourceChange` as an explicit `null` (`provider/ctyvalue.go`'s own
encode path never validated a config object's keys against the real
schema at all) — a real `ubx ship` of a diagram-authored resource with
ANY required attribute (true of nearly every real resource type)
reached a real provider as a cryptic, generic rejection, never a clear
ubx-side one. `provider/ctyvalue.go` now hard-refuses this at encode
time (`ErrRequiredAttributeMissing`, naming the exact attribute) — which
means a diagram-authored proposal for a resource type with a `Required`
attribute now correctly refuses to ship at all until enriched some other
way (a hand-edited plan file, `ubx accept`ing a differently-authored
proposal for the same resource, etc.). This doesn't change what the
diagram medium can express (still topology only, unchanged) — it changes
what happens when someone tries to ship that topology directly without
the enrichment step the medium's own design has always required.

**Amendment (2026-08-02, UBI-90): the same refusal now happens at
RESOLVE time, not just at ship-time encode — found live, the founder's
own playground-13 incident, the exact gap this session's own amendment
above left open.** The 2026-07-31 fix (immediately above) hard-refused a
missing-Required-attribute at `provider/ctyvalue.go`'s own encode step —
correct in intent, but arbitrarily late: a real multi-resource `ubx ship`
had already applied one resource (a type with few enough required
attributes that topology alone happened to satisfy it) before the SECOND
resource's own encode failure ever fired, leaving a real,
partially-shipped stack with no way to have caught the problem any
earlier. `core/resolver`'s own `MissingRequiredKeys` check
(`ErrMissingRequiredAttribute`, docs/resolver-adversarial.md row 15) now
runs at the SAME resolve-time chokepoint UBI-66's own `UnknownConfigKeys`
already established — before a proposal is even saved as a plan, let
alone shipped. `provider/ctyvalue.go`'s own encode-time check is
unchanged, kept as ship-time defense-in-depth for any proposal that
somehow bypassed resolve (an old plan file resolved before this fix
existed, say) — but it is no longer the first, or only, place this is
caught.

**The root design question this amendment also answers, confirmed
against this document's own founding text, not assumed:** should a
diagram-authored resource's own required-but-inexpressible attribute
render as a blocking `core.Question` instead of a hard resolve-time
refusal? No — confirmed via two independent lines of evidence in this
project's own existing design record. First, `Question.Blocking` is
explicitly documented (docs/intent-provider.md's "Component 3" section)
as carrying **zero resolver-side enforcement**, a deliberate,
considered-and-rejected alternative ("auto-refusing resolve on a
blocking question... would hand [it] veto power over what a human is
allowed to review and sign") — using it here would not actually satisfy
"never reaches ship," the whole point of this fix. Second, Component 3's
own boundary is explicit: a Question exists for a genuine *interpretive*
ambiguity (a "plausible, valid, schema-conforming, wrong-guess concrete
value" nothing mechanical could catch) — explicitly contrasted, in that
same passage, against "a wrong resource type... a wrong reference... a
wrong operation," all of which the SAME text says are already caught by
resolve's own "deterministic, schema-checked, hard-erroring pipeline." A
missing Required attribute is mechanically checkable with zero legitimate
interpretation — squarely in that hard-erroring bucket (the same one
UBI-66's own wrong-key check already lives in), never the Question one.
This project's own founding lossy-medium rule (UBI-47, above) already
pointed the same direction: diagrams author topology only, BY DESIGN, and
a resource needing more than topology has always required a separate
enrichment step before it's real (this document's own "Live finale" and
every conformance fixture converge on real, complete stacks; nothing in
UBI-47's original scope ever proposed silently defaulting or
Question-ing a required attribute into existence).

**Amendment (2026-08-02, UBI-91): a narrow, in-medium recovery path for
exactly the gap the amendment above hard-refuses, so a diagram-first
workflow never has to abandon the diagram entirely for ONE resource out
of five needing a Required attribute.** UBI-90's own hard refusal is
correct and stays unconditional -- this amendment doesn't relax it, it
adds a way to satisfy it without leaving the medium. A node may carry a
value for an attribute directly via `ubx_required.<attr>: value` -- D2's
own dotted-path shorthand for a nested map (`role: "x" { class:
aws_iam_role; ubx_required.assume_role_policy: |md ... | }`).

**Scope discipline, strict, enforced at PARSE time (not deferred to
resolve):** `diagram.Parse` checks every `ubx_required.<attr>` against
`dp.Schema.MissingRequiredKeys(typeClass, {})` -- the exact set of
attributes the real schema marks Required, the identical schema
`InferProvider` already fetched for type inference. Anything outside that
set (Optional, Computed, or not a real attribute at all) is refused
immediately, hard, naming the attribute: `"description" is not a required
attribute on aws_iam_role -- use --from-doc or an SDK program to set
optional attributes`. This is deliberately NOT a general attribute-
authoring escape hatch -- topology-only stays the rule for everything
else; `ubx_required` closes exactly the UBI-90 gap and no more.

**A real, load-bearing structural fix found empirically before writing
any of this, not assumed:** `ubx_required.<attr>: value` compiles, in
d2graph's own object tree, to a REAL child object named `ubx_required`
under the resource node (D2's own general dotted-path-to-nested-map
mechanism, the same one containers already use) -- which, left
unhandled, makes the resource node itself look like a container (a
non-empty `ChildrenArray`) and silently excludes it from `sortedLeaves`'
own leaf-only filter, the exact same one `class:`-based type inference
depends on. Confirmed via a direct `d2compiler.Compile` probe before
writing the fix: a node with a `ubx_required` block stopped being
classified as a resource at all. Fixed by widening `sortedLeaves`' own
leaf predicate (a node whose ONLY child is the reserved `ubx_required`
name is still a leaf) and excluding the reserved subtree itself (the
`ubx_required` object and everything nested under it -- attribute names
and values) from ever being independently classified as its own
resource/container/reference node.

**Value encoding:** every attribute in this escape hatch's own real-world
scope (`assume_role_policy`, `policy`, `name`, `role`, `policy_arn`) is a
plain STRING-typed provider attribute even when its own content happens
to BE JSON text (an IAM policy document) -- so every `ubx_required` value
is encoded as a JSON string, read verbatim from the leaf node's own
`Label.Value` (a plain scalar for `ubx_required.name: "x"`, or the full
text of a `|md ... |` block-string for a JSON policy document alike),
never re-parsed into a nested object. Confirmed live against a real,
pinned `hashicorp/aws@6.54.0` schema fetch (`ubx propose --from-diagram`,
schema-fetch only, never applying -- this repository's own standing
CLAUDE.md rule), not just hermetically: a real `aws_iam_role` node with
`ubx_required.assume_role_policy` resolves with zero missing-required-
attribute refusal, and `ubx_required.description` (a real Optional
attribute on the real schema) is genuinely refused by the scope-
discipline check, both real transcripts now in ubiquex-docs'
`guides/diagram-medium.mdx`.

**Live finale (hermetic, per this repository's own standing rule --
`fakeprovider`, never real AWS):** the founder's own exact five-resource
`platform.d2` topology (`aws_sqs_queue`/`aws_ecr_repository`/
`aws_iam_role`/`aws_iam_role_policy`/`aws_iam_role_policy_attachment`),
with `ubx_required.*` supplying every one of the five real attribute
names UBI-90's own refusal flagged (`name`, `assume_role_policy`,
`policy`, `role`, `policy_arn`), resolves cleanly with exactly 5 creates
and zero refusal (`diagram/integration_test.go`'s own
`TestParseThenResolve_PlatformD2_UBI91LiveFinale_AllRequiredSupplied_ResolvesClean`
-- fakeprovider's own single shippable type can't individually model five
distinct real AWS types, so this proves the resolve-time claim with the
founder's own exact attribute names). The full execution half -- ship,
terminate, and a confirmed-clean final state -- is proven separately, for
real, against a real fakeprovider subprocess
(`cli/diagram_ubx_required_test.go`'s own
`TestPlanShipTerminate_FromDiagram_UbxRequired_FullLifecycle`, plus a
hand-run `script -q` transcript covering the identical sequence): plan
(zero refusal) → ship (real create, confirmed via `ubx why`) → terminate
→ ship (real destroy, confirmed by reconciliation) → `ubx status --drift`
reports zero resources tracked.

**Conformance fixtures extended permanently**, matching this arc's own
existing golden-fixture discipline: `diagram/conformance/golden/
ubx-required.d2` + `ubx-required-topology.json` (a `fake_widget` node
carrying `ubx_required.name`, fake_widget's own real schema-Required
attribute -- proving, permanently, that the node still resolves to
exactly one real resource in `Topology()`'s own output, never zero
misclassified-as-container, never two with the escape hatch's own
attribute nodes leaking through as if they were real topology).
