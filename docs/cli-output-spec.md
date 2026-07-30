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
