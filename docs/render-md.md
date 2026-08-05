# `ubx render --md` — human-readable current-state document (UBI-86 Part 1)

> Founder finding, real gap: `ubx status`/`blame`/`why` are all correct
> and readable, but verb-shaped (run a command, get an answer) — there's
> no single artifact a human can open and read top-to-bottom to
> understand "what does my infrastructure look like right now." This is
> a RENDER, not a new medium — the fourth projection surface
> (`docs/architecture.md`'s "every medium is a projection, never a
> second source of truth"), sibling to `ubx render`'s own existing D2
> diagram output (`docs/diagram-medium.md`'s "render direction," UBI-47),
> sharing the identical ledger read.

## The design principle, stated plainly (also in the rendered document's own header)

This document is **CURRENT STATE, generated**. It is never hand-edited.
The authoring document(s) that created these resources (an SDK program,
a diagram, an md draft) remain separate, historical evidence of
individual decisions — they are not meant to stay current and should
not be hand-edited to match reality. This render answers "what exists
now"; `ubx why`/`ubx blame` remain the answer to "how did it get this
way." Neither replaces the other.

## Shared walk: `diagram.Walk`

`diagram/emit.go`'s own `Emit` (D2) and the new `mdrender` package share
one ledger read, extracted into `diagram.Walk(l *core.Ledger, stack
string) ([]diagram.Resource, error)` — `Fleet` → `FoldState` → each
resource's own **creating** proposal (not merely its latest-touching one
— a later `drift_adopt` never carries `depends_on`/blueprint provenance,
only the create node does; `creatingProposalFor` walks the address's
full history to find it, unchanged from D2's own established mechanism).
Both output formats can never disagree about WHICH resources exist or
how they're grouped, only how that same real data is presented — the two
render targets are proven to reuse identical resolved-time truth by
construction, not by convention alone.

`diagram.Resource.Attrs` is `map[string]json.RawMessage` (one raw JSON
value per top-level attribute), not `map[string]interface{}` the way
D2's own pre-UBI-86 code decoded straight to — markdown needs the RAW
bytes to check `core.IsRedactedValue` per attribute before rendering it,
a distinction lost once decoded into `interface{}`. `diagram.Emit`
itself now calls `Walk` and converts to its own D2-specific shape;
zero behavior change, confirmed by the full pre-existing D2 test suite
staying green unchanged.

## Grouping: blueprint origin first, then type

Item 2's own explicit requirement: blueprint-sourced resources group
visually, reusing Slice 6's exact grouping basis
(`diagram.Resource.BlueprintRef`, real resolved-time truth read off each
resource's own creating proposal's `sources[]` — never a guessed
structure) — a `## Blueprint: <name>:sha256:<short>` heading, one per
distinct ref, `diagram.ShortBlueprintRef` (newly exported so `mdrender`
reuses the exact same 12-char truncation rule D2's own dashed container
label already established, rather than a third independent mirror of
it). Within each group (including the top-level ungrouped bucket),
resources sub-group by TYPE, alphabetically (`### aws_ecr_repository`) —
the simpler of the two options the ticket named ("by type or dependency
structure"): a dependency-structure TREE reads well in a diagram's own
2D layout, but in a strictly linear document it degrades to exactly the
information a "depends on: ..." line under each resource already
carries, so the added structural complexity of a tree wasn't worth it.
A stack with zero blueprint calls renders with no extra "## Resources"
heading at all — byte-identical in spirit to D2's own "zero blueprint
calls, unchanged output" invariant.

## Attributes: readable, formatted, never a raw JSON dump

Per resource: `#### <name>` heading, then one bullet per top-level
attribute, sorted. A nested object/array attribute value recurses into
FURTHER indented bullets (`writeAttrLines`, `mdrender/mdrender.go`) —
taken literally: even a nested value (a `tags` map, a JSON policy
document decoded from its own string attribute) renders as bullets,
never squeezed into one inline JSON blob. A `$redacted` marker
(`core.IsRedactedValue`, UBI-23) renders `(redacted)` — checked at every
recursion level, not just the top — the identical wording
`cli/revertplan.go`'s own `rawOrAbsent` already established for
`why`/`revert-plan` output, kept consistent rather than inventing a
second phrase for the same fact. Dependency edges (`depends on: ...`)
and cross-stack references (`references (cross-stack): ...`) render as
their own bullets, canonical addresses, sorted.

Deliberately does **not** attempt to mark an attribute `(computed)`:
that's schema metadata (`provider.Block.Attributes[i].Computed`), not a
property of the state JSON itself, and `Walk`'s own read is
provider-free by design (matching `diagram.Emit`'s own "no provider
needed, pure ledger read" posture) — adding it would mean launching a
provider just to annotate a render, a real scope expansion this ticket's
own success bar didn't ask for. Named here, not silently dropped.

## `--md` and `--check`

`ubx render --md [--stack <s>] [--out <file>] [--check]` — a new bool
flag on the existing `render` command (not a separate verb): `--check`
is completely format-agnostic (it always operated on rendered bytes,
whichever format produced them), so it's reused unchanged for markdown.
Deterministic: every map iterated during emission is sorted first,
nothing reads a clock or random source — two `Emit` calls against an
unchanged ledger produce byte-identical output, confirmed by a dedicated
`TestRenderMd_Determinism_RepeatedRunsByteIdentical`.

## Live verification, the ticket's own required bar, genuinely met

Against a real fakeprovider-shipped, blueprint-sourced stack (the SAME
real Go blueprint package `render_blueprint_test.go`'s own Slice 6 proof
uses — `platform` calling a real `ubx_blueprint`-classed diagram node,
resolved, accepted, shipped for real): `ubx render --md` produced a
readable document with a correct `## Blueprint: platform:sha256:...`
heading grouping both blueprint-produced resources, correct real applied
attributes, and correct real `depends on:` edges. Redaction confirmed
against a real `Sensitive`-flagged attribute (`FAKEPROVIDER_SENSITIVE_
ATTRS`) — `(redacted)`, never the real secret material, never even the
salted `$redacted` hash.

## Related

`docs/blueprint.md`'s own "Override mechanism: UBI-86 Part 2" section —
`ubx render --md --from-drift`/`--sync-overrides` extend this same
command with a second, genuinely different render mode (override-
statement generation from real drift, mechanical, zero AI), documented
there rather than duplicated here.
