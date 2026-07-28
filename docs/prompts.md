# Prompt conventions — session handoffs

Claude Code auto-reads CLAUDE.md; STATE.md carries current context. Prompts are
therefore short pointers, not specs.

## Standard implementation prompt shape

```
Read STATE.md. Work on <slice/task>, per docs/<relevant doc>.md §<section>.
Constraints: <anything session-specific>.
When done: tests green, update STATE.md (done/next/surprises), commit and push.
```

## Rules embedded in every session (via CLAUDE.md, don't repeat in prompts)

- Git identity: Roozbeh's, signed, no AI attribution anywhere.
- STATE.md update is the mandatory last act.
- Doc conflicts: stop and flag in STATE.md, don't silently diverge.
- Never `ubx ship` against a real cloud provider for verification —
  `fakeprovider` + `UBX_PROVIDER_MIRROR` only (see CLAUDE.md's own Code
  conventions; UBI-47 session 4 is why this line exists).

## Design-session outputs (project chats)

Decisions made in chat land in repo docs before implementation acts on them:
- Architecture/model changes → docs/architecture.md
- Schema changes → docs/schema.md (hashing changes require explicit ratification note)
- Plan/scope changes → docs/plan.md + changelog entry

## Sync ritual

At the end of each slice: refresh the Claude Project knowledge copies of
architecture.md / plan.md / STATE.md so design chats stay current.
