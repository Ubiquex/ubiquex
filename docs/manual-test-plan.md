# Manual test plan for ubx

Work through this as the person it describes: someone who has just
installed `ubx` and is building a real stack for the first time. The
order is the order they would hit things, not the order the codebase is
organised in.

Everything here was run against the real binary at commit `fa490ca`,
against the real `ubiquex/aws` 3.0.0 provider pin, with real generated
bindings and a real TypeScript program. Where output is shown, that
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
| Deno | 2.x, for the TypeScript evaluator and for editor types |
| a provider pin | this plan uses `ubiquex/aws` at `3.0.0` |

No cloud credentials are needed for sections 1 through 7. Schema comes
from a pinned, checksum-verified snapshot, and nothing in those sections
contacts AWS.

Two snags will interrupt this flow. Both are real, both are in section
12, and both are called out again where you hit them. Read section 12
first if you would rather not be surprised.

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
$UBX init --stack billing --dynamic-source ubiquex/aws --provider-version 3.0.0 --region us-east-1
```

**Correct:** `.ubx/config.hcl` is written and the next-step hint names
`ubx plan --from-code <file>.ts`. The config holds:

```hcl
stack = "billing"
providers = {
  "aws" = {
    source  = "ubiquex/aws"
    version = "3.0.0"
  }
}
provider_configs = {
  "aws" = {
    region = "us-east-1"
  }
}
```

**Failure:** it overwrites an existing config without `--force`.

**Worth knowing:** bare `ubx init` with no provider writes a *different*
next-step hint, naming `ubx plan --from-doc <file>.md`. That flag does
not exist. See 12.1. The `--dynamic-source` path above is correct.

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

### 2.3 The first snag: add the table `sdk gen` actually reads

```
cat >> .ubx/config.hcl <<'EOF'

dynamic_providers = {
  aws = {
    source  = "ubiquex/aws"
    version = "3.0.0"
  }
}
EOF
```

You have now declared the same provider twice, under two different key
names, because `ubx init` writes `providers` and `ubx sdk gen` reads
`dynamic_providers`. Both are real: `providers` is what the plan and
ship path consults. This is 12.2, and it is the largest papercut in the
flow.

**Correct after adding it:** `$UBX config` shows both tables.

---

## 3. Generate the bindings

The SDK is the authoring medium, so this is the step that makes the
stack writable at all.

```
$UBX sdk gen --lang ts --out sdk
```

Expect a few minutes for AWS.

**Correct:**

```
ubx-provider-dynamic: serving "aws" (mixed: [cloudformation smithy]) from real group
  snapshot ~/.ubx/schemas/ubiquex/aws/3.0.0 (version 3.0.0, schema_format 3),
  zero network at schema resolution time
generated 6262 resource type(s) for dynamic provider "aws" -> sdk/aws
real description coverage:
  aws: 381573 fields: 21554 sourced (6%), 0 AI-inferred (0%), 360019 none (94%)
```

6,956 `.ts` files under `sdk/aws/sdk/typescript/`, one per resource
type, grouped by service. The snapshot is local and checksum-verified,
so this works offline once cached.

**Failure:** `no [thirdparty_providers] or [dynamic_providers.<name>]
declared` means you skipped 2.3.

**Two warnings you will see and can ignore for now:**

```
sdk gen: dynamic provider "aws": signal collection failed, continuing without
  enum/constraint context: dump signals for dynamic provider "aws": exit status 1
ubx-provider-dynamic: snapshot …: group spans more than one real schema source --
  merging into one served schema is not yet supported: member "aws" is
  "cloudformation", member(s) already seen are "smithy"
```

Generation still completes and the bindings are usable. Both are
recorded in 12.5. The 6% description coverage is an aggregate across all
6,262 types; the common services are much better covered than that
number suggests, which 4.2 lets you check for yourself.

---

## 4. Editor types

This is the step that decides whether authoring feels like writing code
or like guessing at JSON.

### 4.1 Make `@ubx/sdk` resolvable, exactly once

```
deno add npm:@ubx/sdk
```

**Correct:** `@ubx/sdk` at 1.0.2 or later added to a `deno.json` at your
project root.

**Do not** run `deno install` inside `sdk/aws/sdk/typescript/`. The
generated `package.json` there declares its own `@ubx/sdk` dependency
and installing it gives you a second copy, which breaks type checking in
a way whose error message actively misleads. That is 12.3, and it is
worth reproducing once deliberately so you recognise it.

### 4.2 Read a real binding

```
less sdk/aws/sdk/typescript/aws/sqs/queue.ts
```

**Correct:** a typed `QueueConfig` with real AWS documentation on the
fields, carried through from the provider's own schema:

```ts
export interface QueueConfig {
  /** The time in seconds for which the delivery of all messages in the queue
      is delayed. You can specify an integer value of ``0`` to ``900`` (15
      minutes). The default value is ``0``. */
  delaySeconds?: number | Computed<number>;
  …
}
export const Queue: ResourceBinding<QueueConfig, QueueAttrs> = {
  wireType: "aws_sqs_queue",
```

**Failure:** fields typed `any`, or a `Config` holding nothing but a
path parameter. That is a real shape for some providers, see 12.6, and
it means that provider is not usefully authorable yet.

Note the split: `QueueConfig` is what you set, `QueueAttrs` is what
exists after it is created. You reference the second, not the first.

---

## 5. Write the stack

Create `billing.ts`:

```ts
import { intent, resource, stack } from "@ubx/sdk";
import { Dbinstance } from "./sdk/aws/sdk/typescript/aws/rds/dbinstance.ts";
import { Queue } from "./sdk/aws/sdk/typescript/aws/sqs/queue.ts";

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
deno check billing.ts
```

**Correct:** `Check billing.ts` and nothing else.

**Failure worth causing on purpose:** change `invoices.arn` to
`invoices.queueName` and re-run. You should get a type error, because
`queueName` is a `Config` field and not an `Attrs` field, so there is
nothing to reference. Confirm the compiler catches it before `ubx` does.

If instead you see `Type 'ComputedMarker' is not assignable to type
'string | ComputedMarker | undefined'`, you have two copies of
`@ubx/sdk`. See 12.3.

### 5.2 What to look for while writing it

- Field names are camelCase in TypeScript and snake_case on the wire.
  You never type the wire names.
- Autocomplete on `resource(Queue, "invoices", { … })` should offer the
  real SQS fields with their documentation.
- `invoices.arn` should autocomplete from `QueueAttrs`.

**Failure:** no autocomplete, which means the editor is not resolving
either `@ubx/sdk` or the generated tree.

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

### 12.1 `ubx init` with no provider writes a hint for a flag that does not exist

Bare `ubx init` ends its generated config with `then ubx plan --from-doc
<file>.md`. Running that gives `unknown flag: --from-doc`, exit 2. The
real flag is `--from-code`, and it takes a `.ts`, `.go`, `.py` or
`.ubx.hcl` file rather than markdown, so the hint is wrong twice over.

`ubx init --dynamic-source` writes a correct hint, so this only affects
the path a user takes when they have not chosen a provider yet, which is
the more likely first run.

### 12.2 `init` and `sdk gen` do not agree on where a provider is declared

`ubx init --dynamic-source` writes a `providers` table.
`ubx sdk gen` reads `thirdparty_providers` or `dynamic_providers` and
fails with:

```
sdk gen: no [thirdparty_providers] or [dynamic_providers.<name>] declared in .ubx/config
```

Both key names are real. `providers` is read by `providerpool.go` for
the resolve and ship path, so it is not dead. But the two commands a new
user runs back to back do not connect, and the fix is to declare the
same provider twice under two names, which nothing tells you.

This is the largest friction in the flow: it stops a first-time user
between step one and step two with an error that names two keys their
config does not have and does not mention the key it does have.

### 12.3 Two copies of `@ubx/sdk` break type checking with a contradictory error

The generated tree carries its own `package.json` declaring
`"@ubx/sdk": "^1.0.0"`. Running `deno install` there, which that file
invites and which the `ubx-sdk-*` repos' own CI does, gives you a second
copy alongside whatever your project root resolves. Then:

```
TS2322 [ERROR]: Type 'ComputedMarker' is not assignable to type
  'string | ComputedMarker | undefined'.
    tags: [{ key: "queue", value: invoices.arn }],
```

The message says a type is not assignable to a union containing that
same type, because they are two structurally identical but nominally
distinct types from two installations (1.0.1 nested, 1.0.2 at the root).

It breaks precisely the cross-resource reference idiom, which is the
core of the SDK. The fix is one `@ubx/sdk` in the graph: `deno add
npm:@ubx/sdk` at the project root and no nested `node_modules`.

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

### 12.5 `sdk gen` warnings on the AWS group snapshot

Two warnings on every AWS generation:

```
signal collection failed, continuing without enum/constraint context: … exit status 1
group spans more than one real schema source -- merging into one served schema is
  not yet supported: member "aws" is "cloudformation", member(s) already seen are "smithy"
```

Generation completes and produces usable bindings either way. The first
means generated types carry no enum or constraint context. Neither is
explained to the user as harmless, and both look alarming on a first
run.

### 12.6 Some providers generate bindings that are not authorable

DigitalOcean's droplet binding, generated from the same pipeline, comes
out as:

```go
type DropletConfig struct {
	// path parameter, not part of the API's own resource representation
	DropletId any
}
```

No typed fields for the resource body at all. This is a schema-shape
problem for OpenAPI-sourced providers, not a Go or codegen problem, and
it means picking that provider for a first stack gives you nothing to
write against. Worth knowing before recommending a provider to a new
user.

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

The Go and Python authoring paths. `ubx sdk gen --lang go` was confirmed
to generate (225 types for DigitalOcean), but no Go program was written
or evaluated, so the Go evaluator, its OS-level sandbox and the
`ubx-sdk-go` runtime dependency are untested here. Python is untouched.

Both are supported authoring media. A future pass should walk section 5
in each.
