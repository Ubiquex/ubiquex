# Generated conformance harness (UBI-50)

> This document is the spec, written before any code — session 1 of this
> arc is docs-first by explicit instruction, the same protocol
> docs/resolver.md/docs/destroys-adversarial.md were both written under.
> It designs a machine that GENERATES conformance coverage from a
> provider's own schema, generalizing `conformance/registry.go`'s current
> 154 hand-written `TypeSpec` entries (51 AWS, 42 Azure, 40 GCP, 20
> Kubernetes, 1 Helm — real, hard-won, live-verified-where-`RealSafe`
> knowledge, never discarded) into a system where every type of every
> supported provider carries a machine verdict, with that hand-verified
> knowledge layered on top wherever it exists. Cross-references:
> `docs/reliability-report.md`'s UBI-44 chapter and
> `docs/destroys-adversarial.md` rows 12–14 pin the destroy-honesty class
> this generalizes; `core/lookuphints`' generated-from-`registry.go`
> pipeline (`conformance/gentool`) is the existing, narrow precedent this
> extends; `provider/overrides.go`'s `SensitiveOverrides` table is the
> existing, entirely-hand-written table the sensitive-flag-audit probe
> feeds where mechanical. STATE.md's own UBI-43 finding on
> `hashicorp/time` (a provider rejected as UBI-43's second platform
> specifically because `ubx status --drift` would report permanent,
> meaningless null-diffs for it) is the real, already-live-verified
> precedent the drift-detectability probe mechanizes.

## Scope

**In scope, this arc**: schema-driven probe generation for four
lie-classes (identity-shape/incomplete-read, sensitive-flag-vs-echo,
destroy honesty, drift-detectability); two execution tiers (hermetic,
all types, CI-runnable, no cloud; live, real create→read→mutate→destroy,
cost-aware, sandbox accounts); a generated-and-annotated registry format
keyed by `(source, version, type, verb)`, with hand-verified findings
layered on top, never replaced; a typed failure taxonomy feeding
`core/lookuphints`/`provider/overrides.go` automatically where mechanical
and flagging for a human session where not; rerun-on-version-bump delta
detection.

**In scope, provider-agnostically, by construction** (per the founder's
own design-room comment on the ticket): the probe generator, failure
taxonomy, registry format, and version-bump rerun logic are ALL
provider-agnostic — they run identically against `hashicorp/aws` (1,682
resource types at 6.54.0), `hashicorp/google` (~1,100),
`hashicorp/azurerm` (~1,400+), `hashicorp/kubernetes`, `hashicorp/helm`,
and any future source, because they're built entirely from
`provider.Schemas`/`provider.Block`/`provider.Attribute` — the same
provider-agnostic shape `core.StateReader`/`core.RunScan` themselves are
already built on. AWS is merely the first bulk run, not a design
assumption baked into the harness itself.

**Out of scope, this session**: any code. This is the design; session
2+ builds it, per this arc's own "~3–4 sessions design+build+first bulk
run" sizing (the ticket's own honest estimate, not revised down here).

**Out of scope, this arc generally, named per-platform work rather than
silently assumed included**: sandbox credentials and a cost-tier table
per cloud (a GCP project, an Azure subscription, a `kind`/EKS cluster for
Kubernetes — this project already has working precedent for all three,
UBI-21/UBI-22/UBI-37's own live-tier sessions), and Helm's own probe
fixture (`helm_release` needs a pinned, trivial chart to install as its
create-probe, the same role a throwaway resource group/GCP project/EKS
cluster plays for the other three platforms). These are real work, but
they're PLUMBING for a specific platform's live-tier run, not part of
the shared harness this document designs — building the generator once
and running it against a specific platform's live tier are different
sessions with different scope.

## The four lie-classes

Each of these is a class of thing a provider's schema *claims*, that
turns out not to be true in practice — discovered, historically, one
type at a time, by a human actually running a live conformance test and
writing the finding down in a `TypeSpec.Notes` string. This section
designs mechanizing that discovery, not replacing the human judgment
that interprets a finding once found.

### 1. Identity-shape / incomplete-read

**What it catches**: a resource's own natural identity attributes
(`id`, `self_link`, `arn`, `name` — the same ordered candidate list
`core.AttributeDrift`'s own `identityCandidates` already tries) don't
actually suffice to read the resource back completely, even though the
schema makes them look like they should. Two distinct failure shapes,
both real, both already found by hand:

- **Loud**: `ReadResource` errors outright given `{"id": "..."}` alone
  (`google_storage_bucket`'s own confirmed live finding — `"Storage
  Bucket \"\": googleapi: Error 400"`). Already caught today by
  `core.ErrResourceUnreadable`'s existing exit-2 path — not a gap this
  probe needs to newly detect, only to newly AUTOMATE detecting across
  every type instead of one type per live-verification session.
- **Silent** — the dangerous one: `ReadResource` succeeds, returns a
  non-null result, but a real, `Required`-or-set attribute comes back
  empty or `null` anyway (`google_pubsub_topic`'s own `"name": ""`,
  `google_secret_manager_secret`'s own `"name": "", "secret_id": null`).
  `core.ErrResourceUnreadable` never fires — nothing distinguishes this
  from a genuinely-empty, correctly-read resource. This is the shape
  worth mechanizing, because a human only ever caught it by manually
  diffing a proposal's own generated JSON against what was actually
  created, one type at a time.

**Hermetic half**: for every type in a provider's schema, confirm the
candidate identity attributes (`id` always; `self_link`/`arn`/`name`
wherever the schema surfaces one) actually EXIST as real schema
attributes. This is schema PRESENCE only — the same honest, deliberately
narrow claim `conformance/registry.go`'s own `azureSeedNote`/`gcpSeedNote`
already make for every `FakeOnly` entry today ("IdentityFields verified
against the real schema... not yet... live-account verification"). It
cannot prove sufficiency — only a live call can, which is exactly why
`google_storage_bucket`'s and `google_pubsub_topic`'s own real gaps were
never catchable hermetically in the first place.

**Live half**: real create with a full, known config; then read back via
EACH candidate identity value alone (`{"id": ...}` alone, then `{"id":
..., "name": ...}` together, etc. — the same escalating shape this
project's own hand-run live tests already follow); diff every returned
attribute against the config that was actually set. Any attribute the
create config populated that comes back empty/null/mismatched with the
minimal `{"id": ...}` lookup, but correctly with a fuller one, is a
confirmed incomplete-read finding — mechanically reproducing exactly
what a human already found four times by hand this project's own
history (`google_storage_bucket`, `google_pubsub_topic`,
`google_secret_manager_secret`, and `azurerm_resource_group`'s own ARM-id
shape surprise, UBI-37).

### 2. Sensitive-flag audit vs. echo attributes

**What it catches**: an attribute the schema itself never flags
`Sensitive`, but that can carry real secret material anyway — either
because it's a free-form, no-per-key-schema bag an operator is known to
plaintext-stash secrets in (`azurerm_linux_web_app`'s own `app_settings`,
UBI-37 Stage 2), or because it's a computed echo of rendered/free-form
input that a template can interpolate literally anything into
(`helm_release`'s own `manifest`/`metadata.values`/`metadata.notes`,
UBI-22/24). `provider/overrides.go`'s `SensitiveOverrides` table exists
entirely because of this class, and is entirely hand-written today — 5
entries, seeded from two manual audit sessions.

**Hermetic half**: walk every COMPUTED, non-`Sensitive` attribute's own
name against the keyword corpus this project's own manual audits already
used by hand (`notes`, `manifest`, `output`, `log`, `message`, `content`,
`template`, `script`, `yaml`, `json`, `result`, `rendered`, `secret`,
`password`, `key`, `token`, `credential`, `connection`, `cert`,
`private`) and flag every match as a CANDIDATE for human triage — not a
finding. Honesty check, already proven this session (UBI-37's own
sensitive-attribute audit): most keyword matches are false positives on
inspection (`private_ip_address` — an IP classification, not secret
material; `login_server` — a public hostname; `public_key_openssh` — PUBLIC
key material, the opposite of secret; `administrator_login` — a
username, never a secret on its own). The hermetic half's own output
MUST be presented as "candidates for a human to check," never as
findings — a keyword match alone earned zero real overrides this
session; the one real gap found (`app_settings`) was found by
understanding what the attribute actually IS, not by the keyword match
alone naming it a winner among many false positives.

**Live half — a genuinely stronger, mechanical check the hermetic half
can't do alone**: create a resource with a known, unique marker string
planted in one of its OPTIONAL, free-form attributes (the same role
`app_settings`/`metadata.values` play). Read the resource back and
search EVERY OTHER attribute's own value (recursively, through nested
blocks) for that exact marker string appearing verbatim under a
DIFFERENT, non-`Sensitive`-flagged path. A verbatim echo is a
mechanically PROVABLE leak, not a keyword guess — this generalizes the
Helm `manifest` finding (a `set_sensitive` value's plaintext showing up
in `manifest` once a chart template rendered it) into an automatable
check any type can run, not something that requires a human to already
suspect templating is happening.

### 3. Destroy honesty via read-back absence (the UBI-44 class)

**What it catches**: a provider's `ApplyResourceChange` response for a
destroy reports clean success — no error, the correct literal `null`
`NewState` — while the resource is still genuinely reachable on the very
next `ReadResource`. Found live, real, once (`google_pubsub_topic`,
confirmed via Cloud Audit Logs showing zero real `DeleteTopic` calls
despite four "successful"-looking attempts) — `docs/reliability-report.md`'s
UBI-44 chapter and `docs/destroys-adversarial.md` rows 12–14 are the full
account. The fix that shipped is structural, in `core/executor`'s own
`shipDestroyNode`/`reconcileDestroyLoop` (a universal post-destroy
read-back, never trusting a clean `ApplyResourceChange` response alone) —
this probe's job is naming WHICH types are actually exposed to the
underlying gap (a lookup that doesn't fully identify the resource for
its own destroy call, `google_pubsub_topic`'s specific root cause: `name`
never populated because the universal `{"id": ...}` lookup shape never
supplies it), not re-fixing the structural gap itself.

**No hermetic half exists for this probe** — destroy is inherently a
live operation against real (or realistically faked) state; there is no
schema-only signal that predicts whether a provider's own destroy
implementation will lie. Named here explicitly as a real gap the
hermetic tier cannot close, not silently absent from the design.

**Live half, and the harness's own real gap this design surfaces**: the
existing `conformance.RunAdoptMutateScanDiff` harness (`conformance/
harness.go`) runs adopt→mutate→scan-diff against real `core.RunScan`,
but **never destroys anything** — there is no destroy step in the
current harness at all. A destroy-honesty probe needs new plumbing:
build a real `core.KindChange` proposal carrying a `Delta.Destroys`
entry, `core.Accept` it, and run it through `core/executor.Ship` (which
needs a real `executor.ApplierPool`, not just the raw `provider.Launch`
+ `stateReaderAdapter` pair `RunAdoptMutateScanDiff` uses today for
reads) — then verify the resulting `ApplyRecord`'s own terminal
`Reconciliation` outcome is `destroyed`, not
`provider_reported_success_but_present` (UBI-44's own distinctly-worded,
more-serious finding) and not a false `destroyed` reported before a real
read-back actually confirmed absence. This is genuinely new harness
code, not a reuse of anything that exists today — see "Ship doctrine,"
below, for why it must go through `executor.Ship` specifically and never
a shortcut.

### 4. Drift-detectability (the `hashicorp/time` class)

**What it catches**: a resource type whose entire observable shape is
provider-owned computation, disconnected from anything a real "live
drift" could mean — `ubx status --drift`/`ubx scan` would report
permanent, meaningless diffs for it, not a real signal about anything
that actually changed. Not a new discovery: this project already found
and rejected exactly this, live, during UBI-43 — `hashicorp/time`
(specifically `time_static` and its siblings) given only `{"id": ...}`
(the universal lookup shape `core.DeriveLookupFromResult` always
derives) returns every other attribute as `null` on `ReadResource`,
confirmed directly against the real binary before it was rejected as
UBI-43's second provider in favor of GCP (STATE.md, docs/plan.md,
docs/architecture.md all record this finding). "Undriftable"/
"drift-detectability" are new vocabulary this document is introducing —
the underlying phenomenon and its first real example are not new.

**Hermetic half**: flag any type whose ENTIRE schema block has no
`Optional`-or-`Required` attribute at all — every single attribute
`Computed` — as a CANDIDATE undriftable type. `core.dotSet`/`FoldState`'s
own diff can only ever be meaningful where a user's own declared config
is compared against a live read; a type with nothing a user could ever
legitimately set has no config-vs-live comparison to make in the first
place, only two different snapshots of the provider's own internal
computation.

**Live half**: real create, then a real RESCAN with zero real-world
mutation in between (no `Mutate()` step at all — the opposite of
`RunAdoptMutateScanDiff`'s own adopt→MUTATE→scan-diff shape). If
consecutive reads of the identical, untouched resource return DIFFERENT
values — `time_static`'s own confirmed behavior, fresh
provider-computed values disconnected from any real, stable "current
state" — that CONFIRMS undriftable, not merely candidate. A confirmed
verdict here is a type CLASSIFICATION, not a failure the way the other
three probes produce one: the generated registry marks the type
"drift-detection not meaningful," and `ubx status --drift`'s own
recommendations (a future consumer, not built here) could route around
it, rather than the harness treating it as something to fix.

## Execution tiers

### Hermetic tier

Runs for **every** type in a provider's schema, in CI, with no cloud
account and no credentials — the same "free, no credentials, no live API
round trip" standard every existing `FakeOnly` `TypeSpec.IdentityFields`
already holds to (`GetProviderSchema` alone). Covers probes 1, 2, and 4's
own hermetic halves; probe 3 (destroy honesty) has no hermetic
contribution at all, named explicitly above.

Network access is needed only to `provider.Acquire` a not-yet-locally-
cached provider version — the identical, already-accepted exception
`conformance/gcp_provider_test.go`'s/`azure_provider_test.go`'s own
`RequireLive` gating already carries ("this one specifically doesn't
need [a cloud] account at all... it does need real network access to
registry.opentofu.org"). Once acquired, every subsequent run against the
same pinned version is fully offline, matching `provider.Acquire`'s own
local-cache-first resolution order.

### Live tier

Real create→read→mutate→destroy per type, against a real sandbox
account — the same 4-verb sequence `RunAdoptMutateScanDiff` already runs
for create/read/mutate, extended with a genuine destroy step (probe 3's
own newly-designed plumbing, above).

- **Cost-aware batching**: free/cheap types (the existing `RealSafe`
  classification, unchanged) auto-batch; genuinely priced types
  (anything hourly-billed — compute instances, managed databases,
  Kubernetes clusters, and similar) are NEVER auto-created by a bulk
  run, regardless of flags — see adversarial row 2, below.
- **Quota-respecting**: a real cloud account has real API rate limits
  and resource quotas; a bulk run across hundreds of types needs real
  throttling/backoff between calls, not naive full-parallel
  fire-and-forget — the same genre of real-world friction UBI-21's own
  GCP IAM read-after-write consistency lag and UBI-37's own Key Vault
  soft-delete/resource-provider-registration surprises already proved
  this project's live work regularly runs into.
- **Sandbox accounts**: per-platform plumbing, explicitly out of THIS
  document's own scope (see "Scope," above) — a GCP project, an Azure
  subscription, a `kind`/EKS cluster for Kubernetes, each with its own
  established precedent from UBI-21/UBI-22/UBI-37's own live-tier
  sessions.

## The generated registry format

**A new axis today's `TypeSpec` doesn't have**: `(source, version,
type, verb)`, not just `(source, type)`. `version` matters because a
provider upgrade can change behavior — the entire reason for "rerun on
version bump," below — and `conformance/registry.go`'s own doc comment
already establishes `(Source, Type)` keying is deliberate, not
incidental (UBI-21: "a second provider makes 'there's only one provider'
an assumption worth naming rather than leaving implicit"). `verb`
matters because a type can pass one verb and fail another independently
— `google_pubsub_topic`'s own real history: read and mutate work fine
via `{"id": ...}` alone; destroy silently lied. Today's single
`Safety`/`Notes`/`Implemented` triple per `(source, type)` cannot express
that a type is simultaneously `RealSafe` for read/mutate and a known
destroy-lie risk — this is a real structural gap the new format closes,
not a stylistic change.

**Layering, never replacing**: a machine verdict is a DIFFERENT tier of
confidence than a hand-verified finding, and the generated registry must
say so explicitly, never silently promote one to look like the other.
Today's 154 hand-written entries — each backed by a real human session
that actually ran a live probe and wrote down what happened, in
`Notes` prose no schema-walk could produce on its own (GCP IAM's own
read-after-write lag; Key Vault's soft-delete reservation window; a
resource group's own top-level ARM scope) — are layered ON TOP of
whatever the generator would independently produce for the same
`(source, type)`, never overwritten by it. A machine verdict that
CONTRADICTS an existing hand-verified `Notes` entry is a flagged
discrepancy for a human session to resolve (the machine probe could
itself be wrong — a real, live-verified human finding has earned more
trust than an automated pass, not less), never an automatic overwrite.

**Published output, per-type, per-verb, honest**: every type in a
provider's schema gets a real entry — even if that entry's own verdict
is "hermetic-only, not yet live-verified" — replacing today's
"default-assumption silence" (a type with no `Registry` entry at all
implies nothing today; a caller has no way to distinguish "never
checked" from "checked and found fine"). Zero types stay unverified in
the sense of "nobody looked" — the tier distinction becomes
machine-verified vs. machine-and-human-verified, exactly the framing the
ticket itself names as the intended end state.

## Failure taxonomy

Every probe failure lands as one of four typed findings, mapping 1:1 to
the four lie-classes: `incomplete-read`, `sensitive-underflag`,
`destroy-lie`, `undriftable`. Each has its own real, distinct mechanical
destination — or an honest absence of one:

- **`incomplete-read`**: does **not** feed `core/lookuphints` fully
  automatically, and this is a real, already-documented limit inherited
  from existing code, not a new one invented here. `core/scan.go`'s own
  `lookupHintText` hardcodes "make sure `\"id\"` is included" — correct
  for AWS's own shape (where the OTHER field is always the trap, `id`
  alone always works), actively WRONG for `google_storage_bucket`'s "both
  required together" shape and unable to say anything useful at all for
  the silent-incomplete-read cases (`google_pubsub_topic`/
  `google_secret_manager_secret`), which is exactly why those three were
  deliberately never given a `LookupHint` despite being fully understood
  (`conformance/registry.go`'s own `Notes` on all three say so). A
  generated `incomplete-read` finding CAN auto-populate `IdentityFields`
  candidates for human review, but auto-generating a `LookupHint` (and
  therefore a shipped teaching-error message) stays flagged for a human
  session until `core/lookuphints`' own message-generation is itself
  generalized to express "both required together" and "silently
  incomplete, not just unreadable" as distinct shapes — named as
  necessary follow-up work, not assumed already solved by this arc.
- **`sensitive-underflag`**: the live-tier's marker-string echo
  confirmation (a mechanically PROVEN leak) CAN feed
  `provider/overrides.go`'s `SensitiveOverrides` table automatically —
  unambiguous enough to auto-generate a real override entry, the same
  confidence level `app_settings`/`manifest` were manually promoted at.
  The hermetic tier's own keyword-match-only candidates CANNOT — too
  many false positives, confirmed directly this session (most keyword
  matches across 42 real azurerm types were IP addresses, public keys,
  and usernames, not leaks) — these stay flagged for human triage,
  never auto-written into a table that gates real secret redaction.
- **`destroy-lie`**: always flagged for a human session, never
  auto-fixed. The STRUCTURAL fix for a confirmed destroy-lie belongs in
  `core/executor` (as UBI-44's own fix already landed, universally, for
  every type at once) — a per-type registry finding's own job is naming
  WHICH types are actually exposed to the underlying root cause (an
  insufficient destroy lookup, `google_pubsub_topic`'s own specific
  shape), informing where a smarter, per-type destroy lookup is worth
  building next, never itself becoming the fix.
- **`undriftable`**: a live-confirmed verdict CAN feed a new, mechanical
  registry annotation automatically (this type's own drift detection is
  not meaningful) — hermetic-only candidates stay provisional, since the
  hermetic check alone (no `Optional`/`Required` attributes at all)
  cannot distinguish a genuinely undriftable type from one that's simply
  entirely server-assigned but stable (a type whose id is the only thing
  that ever exists, with truly nothing else to observe, is a degenerate
  case of "nothing to diff," not necessarily "actively lies on
  rescan").

## Rerun on version bump

Conformance is already, deliberately, per `(source, version)` — every
existing live-tier test pins an explicit version constant
(`gcpProviderVersion`, `azurermProviderVersion`, and similar), never
"latest," per this project's own standing "explicit version pins only,
reproducibility" rule (UBI-8). When a pinned version bumps (`hashicorp/
azurerm` 5.0.0 → 5.1.0, say), the hermetic tier reruns automatically
against the new version's own real schema — cheap, no cloud, the same
free `GetProviderSchema` call every hermetic check already makes — and
diffs against the PRIOR version's own generated verdicts, flagging:

- a type REMOVED from the new schema entirely (a real breaking change
  worth naming loudly, not silently dropping from the registry);
- a type ADDED (a real gap in coverage until its own hermetic pass
  runs, named rather than silently absent);
- a SCHEMA SHAPE CHANGE for an already-tracked type — most importantly,
  an attribute that WAS `Sensitive`-flagged and no longer is (a real
  regression a provider upgrade could silently introduce, worth flagging
  with real urgency, not treated the same as a routine schema diff).

Live-tier delta reruns stay opt-in, same cost-awareness as any live run
— but the hermetic delta is cheap enough to run on every version bump as
a matter of course, and cheap enough to be a real CI-scheduled job once
this arc's own session 2+ builds it, not just a manually-triggered
one-off.

## Ship doctrine

Probes use the harness's own real execution path, never a shortcut —
the identical discipline every prior conformance session already
follows, made explicit here because destroy probes are genuinely new
plumbing (see probe 3, above) with a real temptation to shortcut:

- **Read/mutate probes** already go through the real path today:
  `conformance.RunAdoptMutateScanDiff` calls `core.RunScan` →
  `core.GenerateProposal` → `core.Accept` directly — the same functions
  `ubx scan`/`ubx accept` themselves call, never a hand-rolled
  read-and-compare shortcut. This stays unchanged; the generated harness
  reuses it as-is for probes 1, 2, and 4's own live halves.
- **Destroy probes must go through `core/executor.Ship`'s real
  `shipDestroyNode`/`reconcileDestroyLoop` path** — never a raw
  `provider.ApplyResourceChange` call the way, e.g.,
  `provider/apply_live_test.go`'s own `hashicorp/time` wire-protocol
  tests do (a legitimate shortcut THERE, since those tests are about the
  wire protocol itself, not about `ubx`'s own destroy honesty). A
  destroy probe that shortcuts to a raw `ApplyResourceChange` call would
  test the PROVIDER's own honesty in isolation, but not `ubx`'s own real
  destroy path's honesty — missing `shipDestroyNode`'s own
  `PlanResourceChange`-before-`Apply` sequencing and, most importantly,
  `reconcileDestroyLoop`'s own universal post-destroy read-back entirely.
  That read-back IS the UBI-44 fix; a probe that bypasses it would be
  structurally incapable of ever finding another UBI-44-shaped bug, the
  exact opposite of this arc's own purpose.
- **Live-tier runs are designated live legs, explicit and opt-in,
  never a default.** The same standing rule CLAUDE.md's own
  ship-verification amendment already establishes for `ubx ship` itself
  ("never... against a real cloud provider... even one already
  credentialed on the machine... use the hermetic `fakeprovider`...
  always, no exceptions") generalizes here: a live-tier conformance run
  touches real infrastructure and can incur real cost, and must be gated
  the same way `UBX_CONFORMANCE_LIVE=1` already gates every existing
  live conformance test — an explicit, deliberate environment variable
  or flag, never bundled silently into a broader "run everything"
  command, and never triggered by CI on a schedule the way the hermetic
  tier's own delta reruns could safely be. A live-tier run's own summary
  output must also name, explicitly, which sandbox account/subscription/
  project it ran against and what it created/destroyed — the same
  transparency this project's own STATE.md entries already give every
  live-tier session by hand, mechanized rather than dropped.

## The program: adversarial rows

### How to read this table

"Injection" describes exactly what's forced to happen. "Required
observable outcome" is the full contract: what the probe's own generated
finding must say, what state real infrastructure must end in, and what a
re-run must do next. A row that can't be made to produce its required
outcome is a bug in the harness design, not an acceptable gap — matching
`docs/destroys-adversarial.md`'s own standard.

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 1 | A type whose create needs required attributes the schema can't default | A live-tier probe reaches a type with a `Required` attribute that has no sensible universal placeholder — a foreign-key reference to another resource that must already exist (`azurerm_storage_container`'s own real `storage_account_id`), or a `Required` enum with no safe default value, or a `Required` attribute the schema's own type spec gives no hint how to synthesize at all. | The probe records a real, honest `skipped: cannot synthesize a valid create payload for <attribute>` verdict of its own — never invents a fake or guessed value just to force SOME create to succeed. A fabricated value producing a misleading downstream pass (or a misleading, unrelated failure blamed on the wrong cause) is treated as a harness bug, not an acceptable corner case — "skip and say" is itself a real, first-class probe outcome, not a fallback to be minimized. |
| 2 | Priced types never auto-created | A live-tier bulk run is invoked, including or defaulting to `--all` (or equivalent), against a provider whose schema/registry classification names a type as hourly-billed (compute instances, managed databases, Kubernetes clusters, and similar categories). | The bulk run NEVER includes a priced type in its own auto-batch, regardless of `--all` or any other bulk flag — only an explicit, individually-named `--include-priced=<type>` (repeatable) opts one in per invocation. The run's own summary output names every priced type it skipped, by type name, not silently — "skipped N priced types, none created" is itself part of the required observable output, not an implementation detail left out of the summary. |
| 3 | Probe resource leakage — sweep verification | A live-tier probe run is killed (`SIGKILL`) mid-way through create→destroy for some type, or a probe's own destroy step itself genuinely fails (a real UBI-44-shaped destroy-lie, or an ordinary transient failure) — real, throwaway infrastructure is left behind in the sandbox account. | Every resource a live probe ever creates is tagged/named with a discoverable, greppable convention (the same `ubx-*` naming precedent this project's own GCP/Azure live-tier sessions already established by hand). A separate, real sweep command — not a manual memory-based check — enumerates every resource matching that convention across the sandbox account and reports (or, with explicit confirmation, destroys) anything left behind. The harness's own run summary distinguishes three real, distinguishable outcomes: clean (nothing left), leaked-and-swept (found and cleaned up), and leaked-sweep-failed (found, but cleanup itself didn't succeed) — never silently reporting overall success while real, billing-incurring resources remain live in the account. |
| 4 | A provider serving inconsistent schema between calls | Two `GetProviderSchema` calls against what's supposed to be the SAME acquired provider binary, same pinned version, return materially different results for the same type — a real (if rare) provider-side bug, or a silent binary substitution mid-run (e.g. a `UBX_PROVIDER_MIRROR` swap partway through a long bulk run). | The harness hashes the schema once, at the very start of a bulk run, and re-verifies that hash before trusting any later-arriving result within the SAME run — never silently generating half a registry against schema A and half against schema B. A detected mismatch fails the whole run loudly, naming exactly what changed (which type, which attribute, which flag), rather than producing a registry that looks complete but is internally self-contradictory. |

## Amendment (session 2): the probe generator + hermetic tier, built

The design above is now real code: `conformance/probe.go` (the `Finding`
type and its `FindingClass`/`Tier`/`Confidence` enums, `ProbeType`,
`ProbeSchema`, and the three hermetic-half probes — `probeIdentityShape`,
`probeSensitiveEcho`, `probeDrift`), `conformance/probe_test.go` (18
hermetic unit tests against hand-built `provider.Block` fixtures, no
network, always runs), and `conformance/probe_live_schema_test.go`
(gated `RequireLive`, same network-only reason as
`gcp_provider_test.go`/`azure_provider_test.go`). Probe 3 (destroy
honesty) still has no code at all, per its own "no hermetic half" design
— nothing in this amendment touches it.

**A few real decisions made concrete while building, not fully pinned by
the design above**: the registry format's own `verb` axis resolves to
`"read"` for all three hermetic-tier lie-classes (incomplete-read,
sensitive-underflag, undriftable are all questions about what a
`ReadResource` call returns), except `probeDrift`, which uses `"drift"`
specifically — a real rescan operation, distinct enough from a plain read
to name separately; `"destroy"` stays reserved, unused by any code that
exists yet. `identityCandidateAttrs` (`id`, `self_link`, `arn`, `name`)
is deliberately broader than `core.AttributeDrift`'s own narrower
`identityCandidates` (`id`, `arn`, `name` only, for a different purpose)
— matching how `TypeSpec.IdentityFields` has actually been populated by
hand across every platform, not how attribution happens to search.
`probeDrift`'s "settable attribute" check walks nested blocks
recursively (a `provider.NestedBlock` carries no `Optional`/`Required`
flag of its own in this project's schema shape — only attributes, at any
depth, can be user-settable).

**Live-verified against all five real, currently-onboarded providers**
(`UBX_CONFORMANCE_LIVE=1`, this session), proving the design's own
central "provider-agnostic by construction" claim against real schemas,
not just hand-built fixtures — real, unedited counts:

| Source | Types | Findings | incomplete-read (confirmed) | sensitive-underflag (candidate) | undriftable (candidate) |
| --- | --- | --- | --- | --- | --- |
| `hashicorp/aws` 6.54.0 | 1,682 | 742 | 134 | 607 | 1 |
| `hashicorp/google` 7.40.0 | 1,319 | 278 | 0 | 278 | 0 |
| `hashicorp/azurerm` 5.0.0 | 1,103 | 263 | 0 | 263 | 0 |
| `hashicorp/kubernetes` 2.35.1 | 82 | 51 | 1 | 50 | 0 |
| `hashicorp/helm` 2.17.0 | 1 | 1 | 0 | 1 | 0 |

Two spot-checks against real, hand-verified ground truth this project
already had on file confirm the mechanism reproduces known-real findings,
not just that it runs without error: `helm_release`'s own
`metadata.notes` (UBI-22/24's own hand-verified sensitive-echo finding)
is caught as a `sensitive-underflag` candidate; `azurerm_resource_group`
(UBI-37's own hand-verified `id`-alone-sufficient finding) correctly
produces NO `incomplete-read` finding, since both `id` and `name` are
present in its real schema. Determinism (running the identical real
schema through `ProbeSchema` twice produces byte-identical output) is
asserted directly against all five real schemas, not just the hermetic
fixtures.

**134 AWS types with zero recognized identity candidate at all**, and
1 Kubernetes type — real, confirmed findings, not yet individually
triaged (that's exactly the kind of "flagged, not silently resolved"
handoff this design always intended a `Confirmed` finding to produce;
which specific types these are, and whether they're genuinely
unreadable or simply named differently than `id`/`self_link`/`arn`/
`name`, is unstarted human-triage work, not resolved by this session).
Zero GCP or Azure types tripped the hermetic identity check at all —
consistent with both platforms' own prior, hand-verified finding that
every sampled type carries a flat `id` (GCP: `id`/`self_link`/`name`;
Azure: `id`/`name`).

**Layering into `conformance.Registry` itself (writing a `Finding` back
onto an existing `TypeSpec`, or reconciling a contradiction) is
deliberately not attempted this session** — `Finding` stays a wholly
separate, additive output for now, exactly as scoped in "What this
doesn't yet cover" below (still true after this amendment, only
narrower: the generator now genuinely exists; wiring it INTO the
existing hand-written registry is real, unstarted follow-up).

## What this doesn't yet cover, named rather than assumed

Updated after the session-2 amendment above (the hermetic-tier probe
generator now exists; what follows is what STILL doesn't). Not built,
and therefore not yet a claim this arc makes about itself: probe 3's own
destroy-honesty plumbing (`core/executor.Ship`-based, no hermetic half,
untouched by session 2); layering `Finding`s back into
`conformance.Registry`'s own hand-written `TypeSpec` entries (session
2's own `Finding` output is wholly separate/additive, not yet wired into
the existing registry at all — see the amendment above); a live-tier
probe for ANY of the four lie-classes (nothing in session 2 creates,
mutates, or destroys a single real resource); any specific platform's real
bulk live-tier run (a
GCP-project-scale or full-AWS-account-scale execution against ~1,000+
real types is real, ongoing infrastructure cost and time, explicitly
scoped as per-platform follow-up, not this design session); Helm's own
probe fixture (a pinned trivial chart) or any other platform-specific
live-tier plumbing (named in "Scope," above, as explicitly out of this
document's own reach); how a future `ubx status --drift` (or similar)
consumer should actually PRESENT an `undriftable`-classified type to a
user (this document designs the classification, not its own downstream
UX); and whether/how the generated registry's own published output
(the ticket's own "Registry output published — per-type, per-verb,
honest" line) should be surfaced anywhere user-facing (ubiquex-docs, a
public dashboard, or similar) versus staying `ubiquex-cli`-internal —
a real open question, not resolved here.
