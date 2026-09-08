# Manual test plan for ubx

Work through this as the person it describes: someone who has just
installed `ubx` and is building a real stack for the first time. The
order is the order they would hit things, not the order the codebase is
organised in.

Everything here was run against the real binary, against the real
`ubiquex/aws` 3.0.0 provider pin, with the real published
`@ubx/sdk-aws` package a user installs and a real TypeScript program. Where output is shown, that
output was produced by running the command. Where something did not
work, it is written down as what it did, not as what it should do.

**The one thing to read before starting.** CLAUDE.md forbids `ubx ship`
against a real cloud provider, for any reason, including verification.
So this flow runs a real stack all the way to the moment of applying and
stops there. That is not a gap in the plan, it is the boundary, and
section 7 says exactly where it falls and what is behind it. The fake
provider appears once, in section 8, only because the apply path itself
has to be exercised somewhere.

---

## 0. What you need

| | |
|---|---|
| `ubx` | built from this repo, see 1.1 |
| Node.js | 22.x or newer, to install the bindings |
| Deno | 2.x, which `ubx` invokes as the TypeScript evaluator |
| a provider pin | this plan uses `ubiquex/aws` at `3.0.0` |

You never invoke Deno yourself. `ubx` shells out to it to evaluate an
SDK program, under a locked-down sandbox with no network, filesystem or
environment access.

No cloud credentials are needed for sections 1 through 7. Schema comes
from a pinned, checksum-verified snapshot, and nothing in those sections
contacts AWS.

This flow is four commands and no detours: `ubx init`, `npm install`,
write the program, `ubx plan`. It was not, until 2026-09-08. If any step
in sections 3 through 6 fails, check section 12.2 first: the fix that
made the documented TypeScript path run at all is recent, and a binary
older than it cannot complete this.

---

## 1. First contact

### 1.1 Build it and confirm what you built

```
cd ~/Ubiquex/ubiquex
make build
```

**Correct:** the last line prints a version ending in your current
`HEAD`'s short commit, for example `dev+fa490ca`. Check it against
`git log -1 --format=%h`.

**Failure:** a commit that is not your `HEAD`. You are testing a stale
binary and nothing below means anything. This has happened before
(UBI-63 session 4), which is why the build prints the version itself.

For the rest of this document:

```
export UBX=~/Ubiquex/ubiquex/ubx
```

### 1.2 Find your way around

```
$UBX --help
```

**Correct:** 34 commands. Read the one-line descriptions and see whether
you could guess, from those alone, which one you would run first.

**Failure:** a command missing, or a description that does not tell you
what the command is for.

### 1.3 The command that teaches instead of doing

```
$UBX destroy some.address.here
```

**Correct:** it refuses and points at `ubx terminate`. `destroy` exists
only to teach; it is deliberately not an alias.

---

## 2. A new stack

### 2.1 Initialise with a real provider

```
mkdir -p ~/ubxflow && cd ~/ubxflow
$UBX init --stack billing --dynamic-source ubiquex/aws --provider-version 3.1.0
```

Pin 3.1.0, not 3.0.0. Every snapshot up to and including 3.0.0 pins
ubx-provider-dynamic 1.2.0, which cannot create any AWS resource carrying
a computed attribute (96% of them): the create fails at
`encode planned state: wire: cannot serialize an unknown value`. 3.1.0 is
the first snapshot pinning 1.2.1, which fixes it. The binary a stack runs
is resolved from the snapshot's own manifest, so the pin is what delivers
the fix.

No `--region` here, and that is the point rather than an omission. A ubx
dynamic provider declares no provider-level configuration at all, so a
region written into `provider_configs` is rejected by the provider at the
first read or apply. `ubx init` refuses the flag on this path as of
2026-09-08. Region and credentials come from the pinned snapshot's own
`[dynamic_providers.aws.auth]` block, fixed when the snapshot was
generated. See section 2.1a.

**Correct:** it completes immediately without asking anything, and the
next-step line on stdout matches the comment written into the file. Both
should tell you to install bindings and write `stack.ts`. The config
holds:

```hcl
stack = "billing"
providers = {
  "aws" = {
    source  = "ubiquex/aws"
    version = "3.0.0"
  }
}
```

There is deliberately no `provider_configs` table. If you see one for a
`[providers]` key, that is the bug this section exists to catch: every
later command now refuses to load such a config.

### 2.1a The region flag is refused, loudly

```
$UBX init --stack billing --dynamic-source ubiquex/aws --provider-version 3.1.0 --region us-east-1
```

**Correct:** it refuses, names `--region`, and names
`[dynamic_providers.aws.auth]` as where the setting actually lives. The
same refusal applies to `--provider-config`, since `--region` describes
itself as shorthand for it.

**Failure:** it succeeds. A config that writes a region here plans
cleanly and fails at `ubx ship`, because `plan` never configures a
provider, so the error surfaces at the last command in the flow rather
than the first. Silently dropping the region instead would be worse
still: the stack would run in whichever region the snapshot baked,
without ever saying so.

Hand-write the same shape to check the load-time refusal:

```
printf '\n[provider_configs.aws]\nregion = "us-east-1"\n' >> .ubx/config
$UBX history --stack billing
```

**Correct:** any command refuses at load, naming the auth block. Remove
those two lines before continuing.

**Failure:** it sits at a `Provider, e.g. ...` prompt despite both
provider flags being given. That was the behaviour until 2026-09-08 and
it blocked the first command anyone runs until stdin closed. Also a
failure: stdout and the generated comment disagreeing about what to do
next, or either one naming `--from-doc`, which is not a flag on any
command.

**Try the prompt too**, since it is what a user with no provider in mind
sees. Run bare `ubx init` in an empty directory:

```
Provider, e.g. ubiquex/aws (enter to skip, configure later):
```

**Correct:** it suggests a ubx provider, not a Terraform registry one.
Answering `ubiquex/aws` writes a `providers` table; answering
`hashicorp/aws` writes `thirdparty_providers`. Those are different
tables read by different code, and the namespace is what picks.

### 2.2 Confirm the config is really being read

```
$UBX config
```

**Correct:** every key with the file that supplied it, and where the
cascade walk stopped.

```
provider_configs.aws.region = us-east-1  <- …/.ubx/config.hcl
providers.aws.source = ubiquex/aws       <- …/.ubx/config.hcl
providers.aws.version = 3.0.0            <- …/.ubx/config.hcl
stack = billing                          <- …/.ubx/config.hcl
```

**Failure:** a key missing, or attributed to the wrong file.

## 3. Install the bindings

The SDK is the authoring medium, and the bindings are a published
package. You do not generate them. `ubx sdk gen` exists to produce the
`ubx-sdk-*` repos from a central config; it is not part of authoring and
no documentation asks a user to run it.

```
npm init -y
npm install @ubx/sdk-aws
```

**Correct:** two packages, one copy each.

```
@ubx/sdk-aws     3.0.1
@ubx/sdk         1.0.2
```

One install pulls both, because the bindings declare the runtime as
their own dependency. The runtime is `stack`/`resource`/`intent`; the
bindings are the typed resource classes. Neither works alone.

**Failure:** more than one `@ubx/sdk` in `node_modules`. Check with
`find node_modules -path '*@ubx/sdk/package.json'`. Two copies means two
nominally distinct `Computed` types and cross-resource references stop
type-checking.

### 3.1 Confirm you got current bindings

```
npm ls @ubx/sdk-aws
```

**Correct:** 3.0.1 or later.

**Worth knowing for Go:** the equivalent `go get` must carry the major
version in the module path, `github.com/ubiquex/ubx-sdk-aws/sdk/go/v3`.
Without the `/v3` it silently resolves v1.0.0, two majors behind, with
no error. That was wrong in the docs until 2026-09-08.

---

## 4. Editor types

This is the step that decides whether authoring feels like writing code
or guessing at JSON. Nothing extra to install: the types came with the
package.

### 4.1 Read a real binding

```
less node_modules/@ubx/sdk-aws/aws/sqs/queue.d.ts
```

**Correct:** a typed `QueueConfig` carrying the provider's own
documentation, and a separate `QueueAttrs`:

```ts
export interface QueueConfig {
  /** The time in seconds for which the delivery of all messages in the
      queue is delayed. You can specify an integer value of ``0`` to
      ``900`` (15 minutes). The default value is ``0``. */
  delaySeconds?: number | Computed<number>;
  …
}
```

`QueueConfig` is what you set. `QueueAttrs` is what exists once it is
created. You reference the second.

**Failure:** fields typed `any`, or a `Config` holding only a path
parameter. That is a real shape for some providers, see 12.5, and it
means that provider is not usefully authorable yet.

## 5. Write the stack

Create `billing.ts`:

```ts
import { intent, resource, stack } from "@ubx/sdk";
import { Dbinstance } from "@ubx/sdk-aws/aws/rds/dbinstance";
import { Queue } from "@ubx/sdk-aws/aws/sqs/queue";

export default stack("billing", () => {
  intent({ summary: "billing database and its work queue" });

  const invoices = resource(Queue, "invoices", {
    queueName: "billing-invoices",
    visibilityTimeout: 300,
  });

  resource(Dbinstance, "primary", {
    dbinstanceIdentifier: "billing-primary",
    dbinstanceClass: "db.t3.micro",
    engine: "postgres",
    allocatedStorage: "20",
    tags: [{ key: "queue", value: invoices.arn }],
  });
});
```

### 5.1 Type check it

```
npm install -D typescript
npx tsc --noEmit --module nodenext --moduleResolution nodenext --target es2022 --strict billing.ts
```

**Correct:** no output at all.

**Failure worth causing on purpose:** change `invoices.arn` to
`invoices.queueName` and re-run. You should get a type error, because
`queueName` is a `Config` field and not an `Attrs` field, so there is
nothing to reference. Confirm the compiler catches it before `ubx` does.

Also worth causing: change `visibilityTimeout: 300` to `"300"` and
confirm `TS2322` names the real expected type. If a wrong value type
passes, the bindings are not being type-checked at all.

If you see `Type 'ComputedMarker' is not assignable to type 'string |
ComputedMarker | undefined'`, which reads as a contradiction, you have
two copies of `@ubx/sdk`. Go back to section 3.

### 5.2 What to look for while writing it

- Field names are camelCase in TypeScript and snake_case on the wire.
  You never type the wire names.
- Autocomplete on `resource(Queue, "invoices", { … })` should offer the
  real SQS fields with their documentation.
- `invoices.arn` should autocomplete from `QueueAttrs`.

**Failure:** no autocomplete, which means the editor is not resolving
`@ubx/sdk-aws`.

---

## 6. Plan it

```
$UBX plan --from-code billing.ts --ledger-dir .
```

**Correct:** a real receipt, resolved against the real AWS schema:

```
Plan  billing · from billing.ts

billing database and its work queue

  + aws_sqs_queue.invoices create
      queue_name: "billing-invoices"
      visibility_timeout: 300

  + aws_rds_dbinstance.primary create
      allocated_storage: "20"
      dbinstance_class: "db.t3.micro"
      dbinstance_identifier: "billing-primary"
      engine: "postgres"
      tags:
      [
        {
          "key": "queue",
          "value": $ref:billing.aws_sqs_queue.invoices.arn
        }
      ]

delta: +2 create(s), ~0 change(s), -0 terminate(s)

blast radius: +2 ~0 -0

ubx-proposal: 0fad1a5fdf2e…
next: ubx ship 0fad1a5fdf2e
```

Four things are worth checking in that output specifically:

1. **camelCase became snake_case.** `queueName` is rendered
   `queue_name`. The binding did the mapping.
2. **The cross-reference survived as a reference.** `value` is
   `$ref:billing.aws_sqs_queue.invoices.arn`, not a resolved value and
   not a placeholder. It resolves at ship time.
3. **The queue is listed before the instance that references it.** That
   is dependency order, not source order.
4. **Every attribute is shown, not just a diff.** Review needs the whole
   picture.

**Failure:** an unresolved reference error naming an attribute you
expected to exist means you referenced a `Config` field rather than an
`Attrs` field. The message is precise:

```
plan: resolve billing.aws_rds_dbinstance.primary: resolve: reference does not
resolve to any known resource or attribute: billing.aws_sqs_queue.invoices.queueName
```

### 6.1 The plan is saved, addressed by its own hash

```
ls .ubx/plans/
```

**Correct:** one file whose name is the full hash whose first 12
characters the receipt printed.

**Failure:** a name that does not match. The whole `ship <hash>` flow
depends on those agreeing.

### 6.2 Bare plan finds it

```
$UBX plan --ledger-dir .
```

**Correct:** with one SDK program in the directory it plans it
automatically. With two it lists both and refuses to guess.

---

## 7. Where this stops, and why

You now have a real, reviewed, hash-addressed plan for real AWS
infrastructure. The next command in the flow is `ubx ship`, and **you do
not run it.** CLAUDE.md forbids `ubx ship` against a real cloud
provider, for any reason, including verification and demos, and
including against credentials already sitting on the machine. That rule
exists because it was broken once and created real AWS resources.

So the real-provider flow ends here. What you can still do with it:

### 7.1 Accept it, which records the decision without applying anything

```
$UBX accept .ubx/plans/<full-hash>.json --ledger-dir .
```

**Correct:** `accepted <hash> (stack billing)`. Nothing contacted AWS.

```
$UBX history --ledger-dir .
$UBX verify --ledger-dir .
$UBX why <full-hash> --ledger-dir .
```

**Correct:** history shows the proposal, `verify` reports `chain:
intact`, and `why` shows the full receipt including the provenance stamp
tying it back to your source file:

```
proposal 0fad1a5fdf2e… (change)
status: accepted
intent: billing database and its work queue
  source: document billing.ts (content_hash=sha256:26b26400935c…)
accepted by [roozbeh] via local at 2026-09-08T12:50:05Z
```

That `content_hash` is worth pausing on. Edit `billing.ts` by one
character and the hash no longer matches, which is what makes promotion
refuse a changed source later.

### 7.2 What acceptance does not give you

```
$UBX status --ledger-dir .
$UBX why billing.aws_sqs_queue.invoices --ledger-dir .
```

**Correct, and surprising:**

```
0 resource(s) (ledger-only, no live comparison)
no proposals found for billing.aws_sqs_queue.invoices
```

Accepting records a decision. It does not create a resource. So every
resource-oriented command has nothing to work on until something ships:
`status`, `why <address>`, `blame`, `render`, `addresses`, `scan`.

This is the real shape of the boundary. Against a real provider you can
exercise authoring, resolution, review, acceptance and the whole ledger
chain. Everything downstream of an actual apply needs section 8.

---

## 8. The apply path, with the fixture

This is the one place a fixture is the right tool, because the thing
under test is the executor itself.

### 8.1 Build and configure it

```
cd ~/Ubiquex/ubiquex
go build -o /tmp/ubxlab/fakeprovider ./provider/internal/fakeprovider

mkdir -p /tmp/ubxlab/lab/state && cd /tmp/ubxlab/lab
export FAKEPROVIDER_MODE=ok-v6
export FAKEPROVIDER_STATE_DIR=/tmp/ubxlab/lab/state
export FAKE=/tmp/ubxlab/fakeprovider
$UBX init --stack payments --dir .
```

`FAKEPROVIDER_STATE_DIR` is load-bearing. Without it the fixture is a
stateless echo, and a create in one invocation is invisible to the
freshness read in the next, because each invocation launches a fresh
provider process. Two separate real findings (UBI-238, UBI-239) trace to
that one cause. If you see spurious drift on tags later, check this
variable first.

The fixture serves one type, `fake_widget`, with `id` (computed), `name`
(required) and `tags` (optional map).

Write `intent.json`:

```json
{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": { "summary": "add widget1" },
  "resources": [
    { "type": "fake_widget", "name": "widget1", "op": "create",
      "config": { "name": "widget1", "tags": { "env": "prod" } } }
  ]
}
```

An intent file rather than an SDK program here, deliberately: the
fixture has no published bindings, and what is under test is the
executor, not authoring.

### 8.2 Ship it

```
$UBX plan intent.json --provider $FAKE --ledger-dir .
$UBX ship <hash> --provider $FAKE --ledger-dir . --yes
```

**Correct:**

```
Ship  payments · 7s old · +1 create(s) ~0 change(s) -0 terminate(s)
accepted 2dcf39ddb319… (stack payments) via local plan
  ✓ payments.fake_widget.widget1: shipped                                 0:00

1 resource(s), 1 shipped, 0 failed, 0 still unknown -- outcome: shipped
```

The age, the per-resource line and its elapsed timer are all required by
the output spec.

**Failure:** a silent success with no per-resource line or no read-back
verification.

### 8.3 Without `--yes`, the prompt is the signature

Plan something else and ship it with no `--yes`, on a terminal.

**Correct:** the receipt renders again and a typed `yes` is required.
That prompt is the local-tier signing moment. On a non-TTY it refuses
rather than hanging or proceeding.

### 8.4 Shipping twice is safe

Re-run the exact command from 8.2.

**Correct:** the already-shipped resource is skipped, not re-created.

### 8.5 Now the resource views have something to say

```
$UBX status --ledger-dir .
$UBX why payments.fake_widget.widget1 --ledger-dir .
$UBX blame payments.fake_widget.widget1 --ledger-dir .
```

**Correct:** `status` lists the resource; `why` shows the decision plus
a ship history ending `outcome=shipped`; `blame` attributes each
attribute to the proposal that set it:

```
Blame  payments.fake_widget.widget1

▸ 3 attribute(s) · set by 2dcf39ddb319… (change) · … · local
    id: "computed-id"
    name: "widget1"
    tags.env: "prod"
```

Compare this against 7.2 and the difference between accepted and shipped
is concrete.

### 8.6 Tamper with the ledger

Back up `ledger/` first. Change one character in an accepted proposal's
JSON, then:

```
$UBX verify --ledger-dir .
```

**Correct:** the chain is reported broken, naming the proposal.

**Failure:** still `intact`. That would mean the hash chain is not
really being checked, which is the most load-bearing property in the
system. Restore your backup afterward.

### 8.7 Destroy needs two consents

```
$UBX terminate payments.fake_widget.widget1 --provider $FAKE --ledger-dir .
$UBX ship <hash> --provider $FAKE --ledger-dir . --yes
```

**Correct:** the terminate receipt shows the resource's full last-known
state, not just its address, and the ship refuses with exit code 1:

```
ship: this proposal has blast_radius.destroys > 0 -- pass --confirm-destroys to accept it
```

`--yes` covers the signing prompt only, never the destroy consent. Add
`--confirm-terminate` to proceed.

**Minor inconsistency, expect it:** the hint says `--confirm-terminate`,
the refusal says `--confirm-destroys`. Both work. See 12.4.

### 8.8 Drift and restore

```
$UBX scan --type fake_widget --name widget1 --lookup '{"name":"widget1"}' \
  --provider $FAKE --ledger-dir . --stack payments
```

**Correct:** `no drift`, exit 0. A never-seen name gives an adoption
proposal and exit 1, where exit 1 means "something to act on", not
failure.

For restore, ship a second widget, alias the first head, then:

```
$UBX history --ledger-dir . --full-hashes
$UBX alias set v1 <full-64-char-hash> --ledger-dir .
$UBX restore v1 --provider $FAKE --ledger-dir .
```

**Correct:** the second widget comes out as a destroy.

**Read this before shipping a restore.** It is exact-state, not a merge:
anything created after the target head is destroyed unconditionally.
That is correct and it is the most surprising behaviour in the CLI.
Confirm the receipt makes the destruction obvious.

**Known defect:** `alias set` needs the full 64-character hash and
rejects the 12-character form every other command prints. So does `ubx
why`. See 12.4.

---

## 9. The larger surfaces

Each of these deserves its own sitting. All work against the fixture
ledger from section 8.

### 9.1 Render

```
$UBX render --md --stack payments --ledger-dir . --out state.md
$UBX render --md --stack payments --ledger-dir . --out state.md --check
```

**Correct:** the first writes a current-state document; the second exits
0. Change one character in `state.md` and re-run: exit 1.

### 9.2 Blueprints

`resources:` in an `Ubxfile` names a pre-resolved intent/v1 document,
not prose. `blueprint build` has had no drafting step since UBI-224.

```
mkdir -p /tmp/ubxlab/bp/ci-platform && cd /tmp/ubxlab/bp
cat > ci-platform/resources.json <<'JSON'
{
  "schema_version": 1, "kind": "ubx:intent/v1", "stack": "ci-platform",
  "intent": {"summary": "CI platform blueprint"},
  "resources": [
    {"type": "fake_widget", "name": "ci-artifacts", "op": "create",
     "config": {"name": "{repo_name}"}}
  ]
}
JSON
printf 'lang: go\nparams:\n  repo_name: string, required\nresources: resources.json\n' \
  > ci-platform/Ubxfile

$UBX blueprint build ci-platform --lang go
$UBX blueprint package ci-platform -o ci-platform.tar.gz
$UBX blueprint verify ci-platform
```

**Correct:** `built 1 resource(s) -> … (go: go/bindings.go,
go/ciplatform.go, go/go.mod)`, then a content hash from `package` that
`verify` reproduces identically.

**Failure worth causing:** change one byte in `go/ciplatform.go` and
re-run `verify`. It must fail.

### 9.3 MCP

```
cd /tmp/ubxlab/lab
{ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}'
  sleep 2
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  sleep 2
} | $UBX mcp
```

**Correct:** exactly eight tools:

```
build_blueprint, describe_blueprint, draft_ubxfile, list_blueprints,
ubx_scan, ubx_status, ubx_why, validate_ubxfile
```

**Failure:** any of `accept`, `ship`, `writeback`, `revert-plan` or
`blueprint push` appearing. Their absence is a deliberate boundary: an
assistant never signs, never writes to a live resource, never appends to
the ledger.

Then point a real MCP client at `ubx mcp` with `cwd` set to the lab and
ask it "who changed widget1 and when".

### 9.4 The HCL wrapper

`.ubx.hcl` is parsed, never evaluated: a deterministic wrapper for
calling blueprints, not a fourth authoring medium, and it cannot hold a
hand-written resource. Build a blueprint first (9.2), then:

```hcl
stack = "demo"

blueprint "widget" "platform" {
  source       = "/tmp/ubxlab/bp/ci-platform"
  primary_name = "widget-from-hcl"
}
```

```
$UBX resolve --from-code stack.ubx.hcl --provider $FAKE --ledger-dir . --out out.json
```

**Failure worth causing:** add a hand-written `resource` block. It must
be refused.

---

## 10. Exit codes are a contract

Check these with `echo $?` immediately after, and never through a pipe:
a pipe reports the last command's status, not `ubx`'s.

| command | expected |
|---|---|
| `$UBX version` | 0 |
| `$UBX verify` on an intact chain | 0 |
| `$UBX status --drift`, everything clean | 0 |
| `$UBX status --drift`, something drifted | 1 |
| `$UBX status --drift`, something unreadable | 2 |
| `$UBX scan` on a never-seen resource | 1 |
| `$UBX blame <unknown address>` | 2 |
| `$UBX why <unknown proposal>` | 2 |
| `$UBX ship` refusing a destroy | 1 |

Whichever is worse always wins when more than one condition holds.

---

## 11. What needs real credentials, and what stays forbidden

### 11.1 Forbidden regardless of credentials

**`ubx ship` against a real cloud provider.** Not for demos, not for doc
transcripts, not for verification, not against credentials already on
the machine. Sections 1 through 7 respect this; section 8 is the
fixture.

### 11.2 Safe against a real provider, and exercised above

| command | what it does |
|---|---|
| `ubx sdk gen` | reads a pinned snapshot, generates bindings |
| `ubx resolve` | drafts a proposal, no ledger, no apply |
| `ubx plan` | drafts and renders, writes only a plan file |
| `ubx accept` | records a decision, contacts nothing |
| `ubx providers check` | queries the Terraform Registry |

### 11.3 Needs real credentials, not exercised anywhere in this plan

Treat these as unverified by this document:

- **`ubx scan --discover`**, which enumerates through the AWS Resource
  Groups Tagging API and needs real credentials and real tagged
  resources.
- **Audit-log attribution**, drift and genesis, needing real CloudTrail
  or its per-cloud equivalent.
- **`ubx accept --from-merge`**, deriving acceptance from a real merge
  commit on GitHub, GitLab, Azure DevOps, Bitbucket Server or Bitbucket
  Cloud. Five platforms, each needing a real repo and a real merged PR.
- **`ubx why --verify-acceptance` and `ubx verify --repo-dir`**, whose
  reviewer half re-checks against a real forge API.
- **`ubx server`**, needing an installable GitHub App, a GitLab Group
  Access Token, an Azure DevOps PAT plus shared-secret header, and
  Bitbucket Server and Cloud tokens.
- **`ubx store gc`**, meaningful only against a real S3, GCS or Azure
  Blob store.
- **`ubx blueprint push` and OCI `pull`**, needing a real registry and
  real `docker login` credentials.
- **`ubx sdk gen --describe`**, billed Claude API calls.

That is the whole VCS acceptance path, the whole server, cloud
discovery, attribution and remote stores. The authoring and ledger core
is well covered by this plan. The integration edges are not covered at
all, and no amount of local testing will reach them.

---

## 12. Known defects and rough edges

All reproduced against `fa490ca` while writing this. None filed.

### 12.1 First-contact defects in `ubx init`, fixed 2026-09-08

Three, all on the first command anyone runs. Fixed together; recorded
because a binary older than that date has all of them.

**It prompted for a provider it had already been given.** The guard
checked `--provider` and `--source` but not `--dynamic-source`, so the
exact command the SDK install tutorial gives prompted anyway and blocked
until stdin closed.

**The prompt suggested `hashicorp/aws`.** ubx ships its own pinned
schemas for eight clouds and that is the path most stacks want, so the
first example a new user saw pointed at the fallback. It now suggests
`ubiquex/aws`, and routes by namespace: a `ubiquex/` source writes
`providers`, anything else writes `thirdparty_providers`.

**The generated config advertised `ubx plan --from-doc <file>.md`.** That
flag has never existed on `ubx plan` and never took markdown, and stdout
in the same command printed something else, so one command gave two
contradictory instructions. My earlier note here claimed the
`--dynamic-source` path was correct; that was wrong, and only stdout was.
Both now say the same thing.

### 12.2 The documented TypeScript path did not run, and now does

`npm install @ubx/sdk-aws` plus a bare import, exactly as
docs.ubiquex.io/tutorial/sdk/install describes it, type-checked cleanly
under `tsc --strict` and then failed at `ubx plan`:

```
error: Import "@ubx/sdk-aws/aws/sqs/queue" not a dependency and not in import map
```

Deno resolves a bare npm specifier from the `node_modules` it finds by
walking up from the root of the module graph, which is the generated
runner script, and that script lived in an extracted temp directory. The
runner now lives beside the entry file. Fixed in ubiquex#90 (UBI-252),
with a regression test, and the flow in sections 3 through 6 depends on
it: check `ubx version` carries that fix before concluding anything here.

Two things this turned up are worth knowing while testing. A project
`deno.json` now reaches the evaluator and its `unstable` flags take
effect, though every capability behind them is still denied by the
permission sandbox and an explicit `--import-map` means the project
cannot hijack `@ubx/sdk`. And a read-only project directory now fails,
because the evaluator needs to write one short-lived file there.

### 12.3 The SDK install page was wrong in all three languages

Corrected in ubx-docs-users#25 on 2026-09-08. Worth knowing because
anyone who followed it earlier is carrying the results.

`go get github.com/ubiquex/ubx-sdk-aws/sdk/go` resolved **v1.0.0**, two
majors behind, silently, because Go carries the major in the module path
from v2 onward and the unsuffixed path still resolves to the last
release that had none.

Both the Go and TypeScript hello worlds set a field that does not exist,
`Name` / `name` on an SQS queue, where the real field is `QueueName` /
`queueName` / `queue_name`. So the first program a user copied did not
compile in two of three languages.

Bindings versions were stale by a major on npm and PyPI (2.2.1 against
3.0.1), and the Go runtime line named v0.3.0 where the documented
command actually resolves v0.2.0.

### 12.4 Short hashes are printed everywhere and accepted almost nowhere

`ubx history` and every receipt print a 12-character hash. `ubx alias
set` and `ubx why` both reject it and need all 64. `alias set`'s error
is actively misleading:

```
alias set: no alias "2dcf39ddb319" in stack "payments" -- list known aliases with …
```

It reports a hash as a missing alias name rather than saying it wants
the full hash. `ubx ship` accepts the short form, so the inconsistency
is within the tool, not a global rule.

Separately, `ubx terminate` ends with `next: ubx ship <hash>
--confirm-terminate` while the refusal says `pass --confirm-destroys`.
Both flags work and set the same bool.

### 12.5 Some providers publish bindings that are not authorable

DigitalOcean's droplet binding, from the same pipeline that produces
every published package, comes out as:

```go
type DropletConfig struct {
	// path parameter, not part of the API's own resource representation
	DropletId any
}
```

No typed fields for the resource body at all. This is a schema-shape
problem for OpenAPI-sourced providers, not a Go or codegen problem, and
it means picking that provider for a first stack gives you nothing to
write against. It is also what section 4.1 asks you to check for, since
it is visible the moment you read a binding.

### 12.6 `ubx sdk gen` warnings, for whoever runs the publishing pipeline

Not a user-facing path: `ubx sdk gen` produces the published `ubx-sdk-*`
repos from a central config and no documentation asks a user to run it.
Recorded here for whoever does run it. Two warnings on every AWS
generation:

```
signal collection failed, continuing without enum/constraint context: … exit status 1
group spans more than one real schema source -- merging into one served schema is
  not yet supported: member "aws" is "cloudformation", member(s) already seen are "smithy"
```

Generation completes and produces usable bindings either way. The first
means generated types carry no enum or constraint context. Neither is
explained to the user as harmless, and both look alarming on a first
run.

### 12.7 Automated coverage exists where human runs do not

`ubx restore` and `ubx blame` both have real automated tests against the
fake provider: `restore` has
`TestRestore_ExactState_CreateModifyDestroy_RealFakeProvider` and
`TestRestore_OfARestore`, `blame` has nine covering multi-touch
attribution, CloudTrail actors, redacted attributes, genesis, JSON
shape, exit codes and TTY rendering.

What neither has had is a human running it and reading the output. An
automated test asserts what someone expected; reading the output
yourself is what catches an answer that is technically correct and
useless. Sections 8.5 and 8.8 are where that gets closed.

### 12.8 What this plan does not verify

The Python authoring path. `pip install ubx-sdk-aws` was confirmed to
install and its `QueueConfig` fields read, while correcting the install
page, but no Python program was written or evaluated, so the WASI
evaluator is untested here.

Go is partly covered. A real program using `go get
github.com/ubiquex/ubx-sdk-aws/sdk/go/v3` plus bare module imports
compiles and plans correctly, so the Go evaluator works and the
documented path is sound there. But no Go stack was carried past `ubx
plan`, and sections 7 onward were exercised only in TypeScript.

A future pass should walk sections 5 and 6 in Python, and 7 onward in
Go.
