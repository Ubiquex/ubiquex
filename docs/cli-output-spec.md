# CLI Output Specification — the ubx visual language

> Design-room spec (2026-07-30), born from the founder's first end-user test.
> Authoritative for UBI-61/62 and the UBI-49 findings arc. Terraform/Pulumi are
> reference points for audience expectations, not templates.

## Principles

1. **Color by verb, everywhere deltas render**: green `+` creates, yellow/orange
   `~` modifies, red `-` destroys. One visual language across plan, ship, scan,
   status, terminate, promote, render.
2. **TTY-only decoration**: NO_COLOR and non-TTY get today's plain output;
   `--json` untouched; docs transcripts captured from the plain variant.
3. **The hash is the handoff**: every command that produces or implies a next
   step ends with `next: ubx <verb> …`. Short hashes (12 chars) everywhere;
   `--full-hashes` opts into full. Short-form input accepted wherever hashes are
   arguments.
4. **The AI's judgment gets visual rank**: assumptions/defaults/questions are
   what the human signs — a titled block ("AI defaults — you are signing
   these:"), never a trailing list.
5. **The honesty machinery narrates**: long operations (provider calls,
   read-back reconciliation, intent-provider drafting, fleet walks) show live
   progress with elapsed time. The read-back verification line is mandatory —
   "provider reported success — verifying via read-back (attempt N/M)" is a
   product differentiator currently working in silence.
6. **Teaching errors enumerate all modes** and name config alternatives with
   the file consulted ("pass --stack or set stack=… in .ubx/config.hcl").
7. **Internal ticket numbers never appear in user-facing output.**

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

  aws_sqs_queue.playground-test
    ✓ in_flight                                    0:01
    ⠧ provider reported success — verifying
      via read-back (attempt 3/8)                  0:41
    ✓ destroyed · confirmed by read-back           1:20

  Σ  1 applied · 0 failed · outcome: applied
```
- Interactive confirmation (UBI-62): receipt summary re-rendered, typed `yes`
  (terraform's pattern), acceptance recorded only after consent. `--yes` for
  automation; non-TTY without `--yes` refuses with a teaching error.
- `--confirm-destroys` stays additive on top (double consent for destroys).
- Bare `ubx ship` = latest plan for current stack, shown explicitly in the
  confirmation; multiple candidates → list + prompt, never guess.
- Per-resource live transitions with elapsed; spinner during provider calls
  and reconcile loops.

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
Timeline as styled cards, newest first; sources / acceptance / apply-trace as
distinct indented blocks; the apply trace keeps its transition lines (they are
the reliability story) with the read-back confirmation highlighted; hashes
short; `--dialogue` unchanged.

### terminate (new verb, UBI-49 finding #7)
`ubx terminate <address>…` — ledger-state-derived destroy proposal (no AI, the
address is the spec), plan-style red-led receipt with full last-known state,
saved to plan store, standard double consent at ship. Schema vocabulary stays
destroys[]/tombstone; docs state the pairing once. Bare `ubx destroy` teaches
toward terminate (or aliases — decide at build).

### config
Provenance table gains alignment + dimmed file paths + the cascade-ceiling line
styled as a footer. No content changes (it was the one command that tested
well).

## Progress narration (cross-verb)

- plan --from-doc: "drafting via claude:claude-opus-4-8… ✓ validated · resolving…"
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

- `ubx plan` bare (no positional argument, no `--from-*` flag): if
  exactly ONE medium file exists in `--ledger-dir` (default `.`), plan
  it automatically — no flag needed.
- Multiple candidates: never guess — a teaching error lists every
  candidate with its own correct flag ("plan: multiple mediums found:
  platform.md, topology.d2 — pick one: ubx plan --from-doc platform.md
  | ubx plan --from-diagram topology.d2").
- Zero candidates: the ordinary "requires exactly one of ..." error,
  unchanged.
- **Detection rules, as built** (`autodetectMedium`, cli/plan.go):
  - `.md` → `--from-doc` candidate, **except** a fixed, case-insensitive
    denylist of well-known project-meta markdown basenames (with or
    without the `.md` extension): `readme`, `changelog`, `license`,
    `contributing`, `code_of_conduct`, `security`, `authors`, `notice`.
    This is the "README.md-class files must never false-positive" rule
    — a project's own README (or CHANGELOG/LICENSE/etc.) sitting next
    to a real authoring doc is never itself mistaken for one.
  - `.d2` → `--from-diagram` candidate, unconditionally (no ambiguous
    non-authoring `.d2` convention exists to guard against).
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
  - `--from-*` flags remain for explicit selection, unconditionally —
    auto-detection only ever fires when none of them (and no positional
    argument) was given.

## plan — progress

Before the receipt, a progress line renders for the `--from-doc` case
specifically (the intent-provider call is a real, seconds-long network
round trip; parsing a `.d2` diagram or evaluating an SDK program is
effectively instant, so neither gets one):
```
drafting via claude:claude-opus-4-8… ✓ · resolving…
```
Printed by `ubx plan` itself (not inside the shared `draftFromDoc`,
which `ubx propose --from-doc` also calls) — `ubx propose --from-doc`
never resolves, so "· resolving…" would be a lie there; this keeps
`propose`'s own output completely untouched.

## plan — receipt format (exact, from the founder's markup, resolved)

- Header: `Plan  <stack> · from <file>` — the "from &lt;file&gt;" segment
  dim.
- NO AI summary sentence under the header (removed) — the field itself
  (`Intent.Summary`) is untouched in the stored proposal; only the
  render was dropped, once every resource block below it already shows
  the real content in full.
- Each resource block:
  - `+ <type>.<name> create` — green AND bold.
  - attributes indented beneath, then ONE empty line before the next
    resource (never a trailing blank line after the last one).
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
  delta: +5 create(s), ~0 modify(ies), -0 destroy(s)

  blast radius: +5 ~0 -0
  cost delta: $0/mo
  ```
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
