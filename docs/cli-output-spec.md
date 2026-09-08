# CLI Output Specification — the ubx visual language

> Design-room spec (2026-07-30), born from the founder's first end-user test.
> Authoritative for UBI-61/62 and the UBI-49 findings arc. Terraform/Pulumi are
> reference points for audience expectations, not templates.

## Principles

1. **Color by verb, everywhere deltas render**: green `+` creates, yellow/orange
   `~` modifies, red `-` destroys. One visual language across plan, ship, scan,
   status, terminate, promote, render.

   The full ratified palette, which every surface that prints a proposal or a
   resource uses:

   | | |
   |---|---|
   | green | creates, confirmations, a successful outcome |
   | yellow | hashes, and property names |
   | red | destroys, and property values |
   | blue | the approver identity |
   | dim | structure, separators, timestamps, kinds |
   | purple | AI judgment and attribution |

   Hashes were blue until 2026-09-08, when they moved to yellow so blue could
   carry the approver. Blue's ratified meaning was "hashes and identities" and an
   approver is an identity, so the meaning narrows rather than collides: blue is
   the person, yellow is the thing being referred to. The approver had no color
   at all before, and it is the single field a reader scans `ubx why` and `ubx
   history` for.

   Property names and values were uncolored until the same date. A property name
   in a fifty-line receipt is as scannable as an approver name, and the reason
   for coloring one applies to the other.
2. **TTY-only decoration**: NO_COLOR and non-TTY get today's plain output;
   `--json` untouched; docs transcripts captured from the plain variant.

   Relative times in human output, absolute in `--json`. A reader scanning a
   history wants "3 minutes ago", not a UTC instant to subtract in their head;
   `--json` is where an exact instant is the point. Where several transitions
   share one second, which is every ordinary ship, they collapse to one line
   rather than repeating the same timestamp.
3. **The hash is the handoff**: every command that produces or implies a next
   step ends with `next: ubx <verb> …`. Short hashes (12 chars) everywhere;
   `--full-hashes` opts into full. Short-form input accepted wherever hashes are
   arguments.
4. **The AI's judgment gets visual rank**: assumptions/defaults/questions are
   what the human signs — a titled block ("AI defaults — you are signing
   these:"), never a trailing list.
5. **The honesty machinery narrates**: long operations (provider calls,
   read-back reconciliation, fleet walks) show live progress with elapsed
   time. The read-back verification line is mandatory —
   "provider reported success — verifying via read-back (attempt N/M)" is a
   product differentiator currently working in silence.
6. **Teaching errors enumerate all modes** and name config alternatives with
   the file consulted ("pass --stack or set stack=… in .ubx/config.hcl").
7. **Internal ticket numbers never appear in user-facing output.**


### The read commands share one shape

`ubx history`, `ubx status`, `ubx why` and `ubx blame` answer four questions
about the same ledger, and each used to print in a format of its own. `blame`
was the only one with real structure, so it is the pattern the other three
adopted (2026-09-08):

```
Title  subject · count

▸ <hash>  <kind>  · who · when
    indented content
```

A count or a qualifier belongs in the header's dim trailer, never on a trailing
line: `status` used to end with "1 resource(s) (ledger-only, no live
comparison)", which reads as a warning that something went wrong rather than a
description of the view that was asked for.

Resource attributes render flattened, one leaf per line, `tags.env: "prod"`,
never a nested JSON block. The exception is a string value that itself contains
JSON, an IAM or trust policy most often, which still renders as a formatted
block per this document's own §v2 requirement: escaping one of those onto a
single line is exactly what a reviewer cannot read.

The helpers live in `cli/readview.go` so a fifth read command reaches for them
rather than inventing a sixth format.

## Per-verb targets (playground examples)

### plan
```
Plan  playground · from queue.md

  + aws_sqs_queue.playground-test                       create
      name: "playground-test"
      · provider defaults for all other attributes

  Σ  +1 create  ~0 modify  -0 destroy        cost Δ $0/mo

  AI defaults — you are signing these:
  ◦ named the queue "playground-test" as the document specified
  ◦ standard non-FIFO queue, provider defaults ("nothing fancy")

  plan  0509dd5d                              next: ubx ship
```
Questions (blocking) render above defaults in a red-accented block. Modifies
show per-attribute `old → new` lines. Destroys show the full-state block
(UBI-30's review requirement) red-led.

### ship
```
Ship  playground · plan 0509dd5d · 2m old

  - aws_sqs_queue.playground-test                      destroy
      last state: tags {"drift":"byhand"} · created 19:31Z

  Destroys 1 resource. Type yes to continue: yes

  ⠧ aws_sqs_queue.playground-test: provider reported success -- verifying
      via read-back (attempt 3/8)                  0:41
  ✓ aws_sqs_queue.playground-test: shipped · confirmed by reconciliation  1:20

  1 resource(s), 1 shipped, 0 failed, 0 still unknown -- outcome: shipped
```
- Interactive confirmation (UBI-62): receipt summary re-rendered, typed `yes`
  (terraform's pattern), acceptance recorded only after consent. `--yes` for
  automation; non-TTY without `--yes` refuses with a teaching error.
- `--confirm-terminate`/`--confirm-destroys` (the same flag, two accepted
  spellings, UBI-77) stays additive on top (double consent for destroys).
- Bare `ubx ship` = latest plan for current stack, shown explicitly in the
  confirmation; multiple candidates → list + prompt, never guess.
- Per-resource live transitions with elapsed; spinner during provider calls
  and reconcile loops. Every line carries its own bold, operation-colored
  address inline (UBI-75) -- there is no separate un-prefixed header line to
  be mistaken for a different, concurrently-reporting resource's own content
  under real concurrent scheduling (UBI-67).
- "Shipped," never "applied," anywhere a human reads it (UBI-75/UBI-79) --
  `core.ResourceState`/`ApplyRecord`'s own stored values are unchanged, this
  is display-only. The closing summary: blank line before it, bold
  throughout, counts colored per verb (green shipped, red failed, dim still
  unknown).

### status --drift
```
Drift  playground · 1 of 1 resources

  ~ aws_sqs_queue.playground-test          eu-central-1
      tags:  null → {"drift":"byhand"}
      who:   user/roozbeh · TagQueue · 19:37Z (CloudTrail)
      next:  ubx scan --propose both
```
Attribute-level diff inline (detection already knows it); attribution line when
available; region/location always shown; `next:` handoff per drifted resource.

### scan --propose
No JSON dumps. One card per generated proposal:
```
Drift found  playground.aws_sqs_queue.playground-test

  drift_adopt   47799c39fb0e     record reality as signed
      tags: null → {"drift":"byhand"}
      who: user/roozbeh · TagQueue · 19:37Z
  drift_revert  8a2f11c04d21     restore the ledger's state
      ~1 modify · removes tag drift=byhand

  saved to plan store            next: ubx ship 47799c  (or 8a2f11)
```
Drafts SAVED to the plan store (finding #6 fix) — ship's inline accept and
confirmation apply identically to drift proposals.

### blame
Group by identical provenance (proposal, time, acceptance); only divergent
attributes get their own blocks; empty/null/zero-defaults collapsed behind
`--all`:
```
Blame  playground.aws_sqs_queue.playground-test

  ▸ 21 attributes · set by cf6d3b (create) · 19:31Z · local
      name "playground-test" · arn arn:aws:sqs:… · url https://…
      · 18 provider defaults hidden — --all to show

  ▸ 3 attributes · set by 47799c (drift_adopt) · 19:48Z · local
      attributed: user/roozbeh via CloudTrail
      tags.drift      "byhand"
      tags_all.drift  "byhand"
      region          "eu-central-1"
```

### why
Timeline as styled cards, newest first; sources / acceptance / ship history as
distinct indented blocks; the ship history keeps its transition lines (they are
the reliability story) with the read-back confirmation highlighted; hashes
short; `--dialogue` unchanged. "Ship history:", never "apply history:"
(UBI-79).

### terminate (new verb, UBI-49 finding #7)
`ubx terminate <address>…` — ledger-state-derived destroy proposal (no AI, the
address is the spec), plan-style red-led receipt with full last-known state
(JSON-valued attributes formatted, readable blocks -- the same renderer
`ubx plan`'s own create blocks use, UBI-78), saved to plan store, standard
double consent at ship. A blank line separates each `- <address> destroy`
block from its neighbors (UBI-77). Schema vocabulary stays destroys[]/
tombstone; docs state the pairing once. The saved plan's own "next:" hint
shows `--confirm-terminate` -- `ubx ship`'s own human-facing name for the
unchanged wire-level `--confirm-destroys` flag (UBI-77), either spelling
satisfies the other. Bare `ubx destroy` teaches toward terminate (or aliases —
decide at build).

### config
Provenance table gains alignment + dimmed file paths + the cascade-ceiling line
styled as a footer. No content changes (it was the one command that tested
well).

## Progress narration (cross-verb)

- sdk gen / fleet scans: per-item counters ("aws 1,682 types · 214/1,682")
- Any reconcile/backoff loop: attempt counter + elapsed, always.

## Library

fatih/color-class minimal ANSI vs lipgloss decided at build with the
dependency-footprint discipline (measure, record).

---

# v2 — the founder's annotated UX, formalized (2026-07-31, UBI-63)

Marked up by the founder against real transcripts of the complex
5-resource platform.md case (the same session that surfaced UBI-63's
three bugs — broken `$ref` transcription, a nested-block encode gap,
and ship's own repeated-receipt/frozen-timer UX). These override v1
where they conflict. What follows is the founder's original markup,
formalized: every ambiguity it left open is resolved below, named as a
resolution rather than silently picked.

## init

Success is affirmative and green:
```
+ .ubx/config.hcl has been generated successfully        (green)
    see <docs config reference>                           (dim)
```
**Resolved**: the founder's own markup showed a bare two-line success
message, dropping the pre-existing "next: write an intent file and run
`ubx plan` ... — see &lt;docs&gt;" guidance line entirely. That line carries
real, concrete next-step guidance (which command to run, which flag to
add) nothing in the new two-liner replaces — kept as a third line,
unchanged, rather than lost. The "see" line's own docs reference reuses
the existing `docsConfigRef` constant (the same one the kept "next:"
line already cites) rather than introducing a second, different,
unverified docs URL alongside it.

## plan — medium auto-detection

(UBI-224, 2026-09-01: markdown and diagram were also auto-detectable
authoring mediums, through 2026-08. The SDK is now the only one, so
"medium" below means "SDK program" -- there is no longer a real choice
of medium to detect between, only how many SDK-program candidates
`--ledger-dir` contains.)

- `ubx plan` bare (no positional argument, no `--from-code` flag): if
  exactly ONE SDK program exists in `--ledger-dir` (default `.`), plan
  it automatically — no flag needed.
- Multiple candidates: never guess — a teaching error lists every
  candidate with its own correct invocation ("plan: multiple SDK
  programs found: create_widget.ts, create_queue.ts -- pick one: ubx
  plan --from-code create_widget.ts | ubx plan --from-code
  create_queue.ts").
- Zero candidates: the ordinary "requires exactly one of ..." error,
  unchanged.
- **Detection rules, as built** (`autodetectMedium`, cli/plan.go):
  - `.ts`/`.go`/`.py` → `--from-code` candidate, **only** if the file's
    own content contains the real SDK import marker for that language
    (`"@ubx/sdk"` for TS, `"github.com/ubiquex/ubx-sdk-go/runtime"` for
    Go, `import ubx_sdk` for Python) — a bare extension match would
    false-positive constantly on an arbitrary `.go`/`.ts`/`.py` file
    sitting in the same directory (a near-certainty for `.go`, since
    `ubx` itself is a Go module); content sniffing on the one string
    every real SDK program actually carries is precise instead.
  - Scans `--ledger-dir` itself (which defaults to `.`, the working
    directory) rather than the live process's real `os.Getwd()`
    directly — this codebase's own established hermetic-testing
    discipline (`configSearchStartDir`/`userHomeDir`, cli/scan_test.go)
    already rules out consulting real ambient process state in a test;
    scanning the already-flagged, already-hermetic `--ledger-dir`
    concept achieves the identical real-world behavior (bare `ubx plan`
    run from a project directory sees that directory's own files)
    without reintroducing that exact class of gap.
  - `--from-code` remains for explicit selection, unconditionally --
    auto-detection only ever fires when it (and no positional argument)
    was given.

## plan — receipt format (exact, from the founder's markup, resolved)

- Header: `Plan  <stack> · from <file>` — the "from &lt;file&gt;" segment
  dim.
- A summary sentence renders under the header, but ONLY for a proposal
  whose intent came from an authored or AI-derived source
  (`document`, `dialogue`, `intent_provider`). First paragraph only.

  **Amendment (UBI-251).** This rule previously read "NO AI summary
  sentence under the header (removed)", on the reasoning that the line
  was pure noise once every resource block below it already shows the
  real content in full. That was correct about the summary it was judged
  against and wrong as a general rule.

  The real authored summary in the corpus
  (`sdk/conformance/golden/payments.json`) is "Provision a small Postgres
  RDS instance in the payments stack, modeled on the staging database but
  downsized for low initial traffic." The clause after the comma appears
  in no resource block and cannot: rendering attributes never tells a
  reader the shape was derived from staging and deliberately reduced.
  That is interpretation, not paraphrase.

  It does not overlap the AI defaults block, which answers "what did the
  model choose where your document was silent". In that same fixture the
  assumptions, defaults and questions are all empty while the summary is
  substantial, so one cannot be standing in for the other.

  The gate is on SOURCE kind, not proposal kind. Three paths write a
  mechanical template into the same field: scan's "adopt existing
  &lt;address&gt; into the ledger (discovered by scan)" and "record drift on
  &lt;address&gt;…", and restore's "restore &lt;stack&gt; to ledger head &lt;hash&gt;".
  Rendering those would print exactly the paraphrase this rule removed.
  `ubx restore` builds a `KindChange` proposal, so gating on proposal
  kind would print its template; the source kind is what separates them.
  Promotion keeps its summary because `cli/promote.go` appends its own
  source rather than replacing the authored one.

  First paragraph only, because the marketing design's second paragraph
  is a cost claim ("the replica is the largest share of the cost
  increase") and nothing can price a change yet. The stored field is
  untouched either way.
- Each resource block is ONE header line, colored+bolded by its own op
  (the general "+/~/- `<address>` `<op>`" header rule, UBI-88): green
  `+ <type>.<name> create`, yellow/orange `~ <address> change`, red
  `- <address> destroy` (address-then-op order, matching create/change --
  UBI-88; the op word itself deliberately stays "destroy" here, not
  renamed to "terminate" -- see the delta-line vocabulary note below).
  Attributes/diffs render indented beneath the
  header, one shared indent level (4 extra columns beyond the header's
  own indent) across all three ops — create, change, and terminate alike
  — then ONE empty line before the next resource block (never a trailing
  blank line after the last one).
  - A modify's attribute lines are `<path>: <before> -> <after>`, no
    per-line "~"/"change:" repetition (the header already carries both).
    An attribute a modify's own drafted config never mentions, when the
    ledger's own recorded value for it is null or the type's own zero
    value, is filtered out of the diff entirely (UBI-88, the same
    null<->zero-value/materialization normalization noise UBI-63 already
    suppressed for drift comparison, extended to core/resolver's own
    modify diff and to the "key entirely absent" shape a partial drafted
    config produces, not just an explicit `null` literal) — never shown
    as a spurious `<attr>: null -> (absent)` line alongside a real change.
  - JSON-valued attributes (IAM policies, trust policies — a config
    string whose own content decodes as JSON) render as FORMATTED,
    readable, indented JSON blocks — never an escaped single-line
    string.
  - **Resolved**: a resolved `$computed` marker (`{"$computed":
    {"from": "<address>"}}`, docs/resolver.md — the placeholder a
    reference to a not-yet-concrete sibling attribute resolves to)
    renders inline as `$ref:<stack>.<type>.<name>.<attr>` — the
    founder's own markup showed literally this notation, drawn from a
    real transcript of the *broken* pre-fix receipt (UBI-63 bug 1, where
    a literal `"$ref:..."` string was itself the wire-format bug). This
    is a **display-only** convention for an unresolved-but-legitimate
    `$computed` value, never a revival of the broken wire shape: the
    real wire format for an unresolved reference is (and stays) the
    `{"$ref": {"to": "..."}}` object (docs/schema.md), resolved away
    before the ledger ever sees it; only the *display* of the
    `$computed` marker that ref legitimately resolves to borrows this
    same terse dotted-address notation, for readability.
- Summary block, each line BOLD (`forceBold`, style.go — a plain nested
  `Bold(Green(...))` call doesn't compose correctly with this package's
  single-reset-per-call color design; see its own doc comment), one
  empty line between the delta line and the blast-radius/cost block:
  ```
  delta: +5 create(s), ~0 change(s), -0 terminate(s)

  blast radius: +5 ~0 -0
  pinned: network @ 4b1e77a2c3d4
  ```
  **Amendment (UBI-251): pinned heads.** One line per distinct neighbour
  ledger a cross-stack reference resolved against, deduplicated by
  (ledger, head) since several references into one neighbour all pin the
  same head. Nothing renders when a stack has no cross-stack references.

  It reads from `Resolution.Inputs` entries with
  `Kind == "cross_stack_pin"`, which the resolver already records. The
  marketing design wanted this as prose inside the summary paragraph
  ("pinned to the network stack at head 4b1e77"), and the summary cannot
  carry it: `Intent.Summary` is written by the author's own program
  before resolution computes any head, and nothing rewrites it
  afterwards. Previously a pinned head was visible only in `ubx why`'s
  pin chain, after the fact, or as JSON from `ubx addresses`. It belongs
  on the receipt a reader is signing.
  **Amendment (UBI-251): the cost line does not render at all until
  something can price a change.** It used to render unconditionally, as
  `cost delta: $0/mo`, and the markup earlier in this document shows that
  zero because that is what the binary printed. Every writer of
  `CostDelta.MonthlyUSD` sets a literal 0 (`core/scan.go` twice,
  `core/resolver/resolver.go`, `conformance/destroy_probe.go`) and there
  is no pricing source anywhere in the tree. The guard was
  `len(...) > 0`, which never suppressed anything: `json.RawMessage("0")`
  has length 1.

  A visible `$0/mo` reads as free, which is a stronger and more wrong
  claim than saying nothing, since a reader cannot tell it apart from a
  real zero. When a pricing source exists the line returns unchanged, in
  the position shown above. The scope of that work is recorded on
  UBI-251.
  The delta line's own vocabulary is "change(s)"/"terminate(s)" (UBI-88),
  matching the change/terminate wording the op headers above already use
  — not "modify(ies)"/"destroy(s)". The same rename also applies (UBI-88
  vocabulary sweep) to every other spelled-out create/modify/destroy count
  in user-facing text: `ubx scan`'s own card description, `ubx resolve`'s
  "resolved: ..." summary, and `ubx ship`'s confirmation blast-radius line
  (`renderShipConfirmSummary` -- spelled-out words, not the symbol-only
  `+N ~N -N` shape, so it was a real instance of the same inconsistency).
  Left deliberately untouched: the per-resource `- <address> destroy`
  header's own op word (word ORDER now matches create/change, but the
  word itself stays "destroy," not "terminate" -- a scoped decision, not
  an oversight), and blast radius' own `+N ~N -N`, which stays
  symbol-only, no wording either way.
- `AI defaults — you are signing these:` header bold (`forceBold`,
  keeping the existing purple "AI judgment" color, not replacing it);
  ONE empty line after the header and between every entry (never a
  trailing blank line after the last entry).
- Footer, both lines green AND bold (`GreenBold`), no plan-path line:
  ```
  ubx-proposal: <shorthash>…
  next: ubx ship <shorthash>
  ```
  Applies identically to `ubx terminate`'s own receipt (the same
  `renderPlanReceipt`/footer convention) — `ubx promote`'s own separate
  footer (which additionally names the written plan file's own path,
  a real, pre-existing, UBI-63-unrelated divergence) is untouched.

## ship — receipt: a real, resolved conflict with the founder's own note

The founder's original markup ended: *"(Ship's receipt inherits the
same format — it re-renders the plan.)"* This directly conflicts with
UBI-63 bug 3's own fix, landed the same session: `ubx ship`'s
confirmation step no longer re-renders the full receipt at all — a
one-line summary (`renderShipConfirmSummary`, cli/ship.go: stack, plan
age, blast radius) plus the typed-`yes` prompt, since the full receipt
already rendered once, at `ubx plan`/`ubx scan --propose` time, and
re-showing every resource/attribute a human already reviewed is noise,
not review, especially now that a real multi-resource receipt can run
to pages of formatted JSON under this exact v2 format. **Resolved
(founder decision, this session): bug 3's one-line summary stands; this
document's own note is the correction, not the code.** `ubx ship`'s
live per-resource progress narration (transitions, reconcile attempts,
the now-ticking elapsed-time display) is unaffected either way — that
was never part of "the receipt."
