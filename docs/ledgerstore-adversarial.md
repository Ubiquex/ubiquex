# LedgerStore adversarial program (UBI-32 Arc B)

> Every row here is a failure or edge case injected on purpose, with a
> REQUIRED observable outcome — written as the spec, before any code
> exists to pass it, the same discipline docs/config-cascade-adversarial.md,
> docs/multi-provider-adversarial.md, docs/executor-adversarial.md,
> docs/resolver-adversarial.md, and docs/destroys-adversarial.md already
> established. Each row becomes a test in `core`'s own hermetic conformance
> suite — run once against the git-directory reference implementation and
> again, unmodified, against a `memblob`-backed store standing in for
> every `gocloud.dev/blob` driver (s3/gs/azblob share one code path, so one
> suite conforms all three) — before it becomes a claim about `ubx`'s own
> behavior against a real remote store. This document is also a future
> published reliability report, alongside its four siblings: read each row
> as a claim `ubx` makes about its own storage-layer behavior, not just a
> test-plan checklist.
>
> This is the *minimum* required program per UBI-32 Arc B's own scope —
> not a claim that failure space around remote ledger storage is
> exhausted. See "What this table doesn't yet cover," below, for named
> gaps.

## How to read this table

"Injection" describes exactly what's forced to happen, relative to
docs/architecture.md's own "`LedgerStore` interface" section (Amendment,
2026-07-18): the `Head`/`AdvanceHead` compare-and-swap (an immutable,
parent-keyed `heads/` edge per advance, never a mutable pointer
overwritten in place), the TTL-based distributed lock (a create-only lock
object; an expired TTL, not a dead PID, is the only staleness signal a
remote store can have), and `WriteProposalIfAbsent`/`WriteSaltIfAbsent`'s
own create-only semantics. "Required outcome" is the full observable
contract: what the store must report, what state it must end in, and
what a retry must do next. A row that can't be made to produce its
required outcome is a bug, not an acceptable gap — this table has no
"known limitation" column, same standard as its four siblings. Rows
marked **(git + blob)** are conformance requirements for every store,
proven identically against both implementations; rows marked **(blob
only)** describe behavior with no git-local equivalent (the PID-file lock
already has a different, adequate staleness signal, so nothing here
applies to it).

## The program

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 1 | CAS race, two concurrent advances **(git + blob)** | Two callers both resolve the same current head, both build a proposal with that head as `Parent`, and both call `AdvanceHead` (blob) / `Append` (git, serialized by its own lock) at effectively the same time. | Exactly one succeeds. The loser gets a clear, distinguishable refusal (a head-moved/parent-mismatch error naming the proposal's own recorded parent and the store's actual current head) — never a silent overwrite, never both succeeding, never a corrupted or ambiguous head. Re-resolving against the now-current head and retrying succeeds cleanly. |
| 2 | Lock contention, genuinely concurrent holders **(git + blob)** | Two processes attempt to acquire the ledger lock for an accept/append at the same time; the first genuinely holds it for a real, measurable duration (not instantaneous). | The second blocks and retries rather than proceeding unsynchronized, succeeding as soon as the first releases — never two lock holders believing they both hold it, never a caller proceeding without ever having acquired it. |
| 3 | Lock TTL expiry, reclaimed **(blob only)** | A lock is acquired (a create-only lock object with a recorded expiry) and its holder then never releases it — simulating a crash — with no process-liveness signal available (unlike git-local's PID check, there is no "is the holder still running" question a remote store can even ask). Time passes beyond the lock's own recorded TTL. | A new caller's lock attempt succeeds once the TTL has genuinely expired — the expired lock object is treated as abandoned and safely reclaimed, never blocking forever waiting for a holder that can't be confirmed dead OR alive. Before the TTL expires, a new caller's attempt still correctly blocks/refuses — the reclaim path only ever fires after genuine expiry, never eagerly. |
| 4 | Interrupted append, resumable **(git + blob)** | A proposal object is durably written (`WriteProposalIfAbsent` succeeds) but the process is killed before `AdvanceHead` ever runs — the head still names this proposal's own `Parent` as current, not this proposal's own ID. A second `Append` call for the *identical* proposal (same content, same ID, same intended `Parent`) is then made, exactly reproducing what a retry after a crash would do. | The second call succeeds — it recognizes the existing proposal object as the *same* pending operation (its own `Parent` still matches the current head), verifies the content, and completes the head-advance step, rather than reporting `ErrDuplicateProposal` for an operation that was never actually accepted the first time. The head ends up correctly advanced to this proposal's ID, exactly once. |
| 5 | Genuine duplicate, correctly refused **(git + blob)** | A proposal is fully accepted (object written, head already advanced past it). The identical `Append` call is made again. | `ErrDuplicateProposal`, unchanged from today's behavior — the fix for row 4 must never weaken this case: a proposal whose own ID the head has *already* moved past (this ID is the current head, or reachable from it via `Parent` links) is always a genuine duplicate, never silently re-accepted. |
| 6 | Corrupted proposal object **(git + blob)** | A proposal object's bytes are overwritten with garbage (truncated JSON, or bytes that aren't JSON at all) after a successful write — simulating disk corruption (git) or an object store's own rare corruption/truncated-upload scenario (blob). | `Read` returns `ErrCorruptLedgerEntry`, naming the id — never a panic, never a partially-parsed or zero-valued `Proposal` silently treated as real, and never treated as "not found" (a corrupt object is a different, more serious fact than a missing one). |
| 7 | Corrupted head-edge object **(blob only)** | A `heads/<parentID>` edge object's content is overwritten with garbage — not a well-formed 64-character hex proposal ID. | `Head`/`AdvanceHead`'s own resolution refuses outright with a clearly-named corruption error (the same `ErrCorruptLedgerHead` class git-local's malformed `.ubx/ledger.lock` already reports) — never silently treated as "no edge here" (which would incorrectly report an *earlier* proposal as the current head, hiding real chain state) and never followed as if it were a valid pointer to whatever garbage-adjacent key happens to exist. |
| 8 | Corrupted apply record **(git + blob)** | One specific attempt file/object among several for the same proposal has its bytes overwritten with garbage. | `ReadApply`/`ApplyAttempts` reports `ErrCorruptApplyRecord` for that specific attempt, naming which one — every *other* attempt for the same proposal, before and after it, still reads back correctly; one corrupt attempt never poisons the whole `ApplyAttempts` result for a proposal that has several. |
| 9 | Apply-attempt discovery via list, not glob **(blob only)** | Several attempts exist for one proposal (`applies/<id>.attempt-1.apply.json` through `.attempt-N.apply.json`), interleaved in the same key-space with other proposals' own attempt objects and the `heads/`/`lock`/`salt` objects the same store holds. | `ListApplyAttempts` returns exactly this proposal's own attempt numbers, in order, never including another proposal's attempts, never including non-apply objects sharing the same prefix, and never missing one due to pagination (a real, multi-page `List` result is followed to completion, not just its first page). |
| 10 | Salt never touches git for a remote store **(blob only)** | A remote-store stack's `WriteSaltIfAbsent` runs inside a directory that also happens to be a git working tree (e.g. the same checkout the code lives in). | No `.gitignore` entry is created or modified — the git-specific safety net (`ensureGitignored`) is git-store-only behavior, confirmed to never fire for a store that was never at risk of an accidental `git add -A` of a salt object living in a bucket, not a local file, in the first place. |
| 11 | Salt race, first use **(git + blob)** | Two callers both discover no salt exists yet and both attempt to generate and persist one, at effectively the same time. | Exactly one generated salt wins (`WriteSaltIfAbsent`'s own create-only semantics); the loser's own generated bytes are discarded and it reads back the winner's salt instead — never two different salts both in play, never a corrupt/merged salt file, never a crash. |
| 12 | Git-directory reference implementation: zero behavior change | The full pre-existing `core`/`cli` test suite (every test that existed before this arc) is run unmodified against the refactored `Ledger`, now backed by the extracted `LedgerStore` interface's git implementation. | Every test passes, unchanged, with no test needing to be rewritten to accommodate the refactor (row 4's fix is additive — a previously-untested crash path now has a defined, correct outcome — never a change to any previously-tested, previously-passing case). |

## What this table doesn't yet cover, named rather than assumed

Not covered by a row above, and therefore not yet a claim `ubx` makes
about itself: the PR-acceptance ceremony's own remote-store mirroring
(docs/architecture.md's own "PR-acceptance ceremony: designed this
session, not yet built") — designed, not built, so there is no code path
yet for a row to exercise; live conformance against GCS (`gs://`) and
Azure Blob Storage (`azblob://`) specifically — the *same* code is
exercised hermetically against `memblob` standing in for all three, and
(if built out this session) live against real S3, but a real-service run
against GCS/Azure themselves is real, credentialed cloud infrastructure
this session may not reach, named as a follow-up rather than silently
assumed to work identically just because the driver is portable;
garbage-collection or pruning of orphaned proposal objects (a proposal
written via `WriteProposalIfAbsent` whose `AdvanceHead` step never
completes and is never retried — e.g. the client crashes for good, not
just the momentary interruption row 4 covers — leaves a permanently
orphaned, harmless-but-never-cleaned-up object; row 4 covers the *retry*
path, not an abandoned one that never gets retried at all); very deep
chains' own resolution cost (the `head-hint` cache bounds the common
case, but no row measures actual latency/cost at a chain length beyond
this project's own stated "foundational-slice scale" assumption); and
the CLI-level consequence docs/architecture.md's own Addressing section
names — `--stack` becoming required to open a remote ledger for a
bare-proposal-ID lookup — which is a real API consequence of the design,
not yet exercised end-to-end against a wired CLI command in this table
(see STATE.md for exactly how far CLI wiring reached this session).
