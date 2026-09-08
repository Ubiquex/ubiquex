# Manual test plan for ubx

A pre-release checklist to work through by hand. Every command here was
derived from the real binary at commit `fa490ca` by reading `ubx --help`
and all 44 subcommand help outputs, then running the commands. Where a
step below shows expected output, that output was produced by actually
running it, not written from memory.

Work top to bottom. Each section assumes the ones before it, and the
order is deliberate: commands with no side effects first, then
authoring, then the loop that changes things, then the larger surfaces.

---

## 0. Setting up a lab you can safely break

### 0.1 Build the binary and confirm it is the one you are testing

```
cd ~/Ubiquex/ubiquex
make build
```

**Correct:** the last line prints a version ending in the short commit
of your current `HEAD`, for example `dev+fa490ca`. Confirm it matches
`git log -1 --format=%h`.

**Failure:** a version whose commit is not your `HEAD`. You are testing
a stale binary and every result below is meaningless. This has happened
before (UBI-63 session 4), which is why `make build` prints the version
itself.

### 0.2 Build the hermetic provider

**Read this before anything else in this document.** CLAUDE.md forbids
running `ubx ship`, or anything else that reaches a provider's
`ApplyResourceChange`, against a real cloud provider. Not for demos, not
for verification, not even against credentials already sitting on the
machine. A by-hand `ship` against real `hashicorp/aws` credentials once
created real AWS resources during what was meant to be routine checking.

Everything in sections 1 through 8 uses `fakeprovider` instead, a test
fixture that speaks the real tfplugin wire protocol and serves one
resource type. It is a real provider as far as ubx is concerned, and it
touches nothing outside a directory you name.

```
cd ~/Ubiquex/ubiquex
go build -o /tmp/ubxlab/fakeprovider ./provider/internal/fakeprovider
```

**Correct:** a binary at `/tmp/ubxlab/fakeprovider`, roughly 18MB.

### 0.3 Set up the lab directory

```
mkdir -p /tmp/ubxlab/lab/state
cd /tmp/ubxlab/lab
export FAKEPROVIDER_MODE=ok-v6
export FAKEPROVIDER_STATE_DIR=/tmp/ubxlab/lab/state
export UBX=~/Ubiquex/ubiquex/ubx
export FAKE=/tmp/ubxlab/fakeprovider
```

`FAKEPROVIDER_MODE=ok-v6` selects a valid v6 handshake serving a real
schema. `FAKEPROVIDER_STATE_DIR` matters more than it looks: without it
the fixture is a stateless echo, and a create in one `ubx` invocation is
invisible to a freshness read in the next, because each invocation
launches a fresh provider process. Two separate real findings (UBI-238,
UBI-239) came from that single cause.

The one resource type is `fake_widget`, with three attributes:

| attribute | type | |
|---|---|---|
| `id` | string | computed |
| `name` | string | required |
| `tags` | map of string | optional |

### 0.4 What the fake provider cannot tell you

The fake provider gives you real wire traffic, real schema handling,
real dependency ordering, real freshness re-verification and real
persistence. It cannot tell you anything about a specific cloud's own
behaviour: eventual consistency, real error shapes, real
`ApplyResourceChange` semantics, IAM, or rate limits.

Section 9 lists the things that genuinely need real credentials, and
which of those are still forbidden even then.

---

## 1. Read-only commands, no ledger, no provider

Nothing here writes anything or contacts anything. Run them first
because a failure here means something basic is wrong.

### 1.1 Version

```
$UBX version
```

**Correct:** one line, `dev+<commit>`. Exit code 0.

**Failure:** anything else, or a non-zero exit.

### 1.2 Top-level help lists every command

```
$UBX --help
```

**Correct:** 34 commands listed under `Available Commands`, including
`accept`, `plan`, `ship`, `why`, `blame`, `restore`, `terminate`.

**Failure:** a command you expect is missing, or the binary prints a
usage error instead.

### 1.3 Every subcommand's help renders

```
for c in accept addresses alias blame blueprint config destroy history \
         init mcp plan promote propose providers render resolve restore \
         revert-plan scan sdk server ship stats status store terminate \
         verify version why writeback; do
  echo "== $c"; $UBX $c --help >/dev/null || echo "FAILED: $c"
done
```

**Correct:** no `FAILED` lines.

**Failure:** any `FAILED` line, or a panic. A command whose help does not
render is broken before you have even used it.

### 1.4 The teaching command

```
$UBX destroy some.address.here
```

**Correct:** it refuses and teaches toward `ubx terminate`. `destroy` is
deliberately not an alias.

**Failure:** it actually tries to destroy something, or gives a generic
unknown-command error with no teaching.

### 1.5 Config cascade

```
cd /tmp/ubxlab/lab && $UBX config
```

**Correct:** at this point there is no config yet, so it reports that
honestly, naming where the walk stopped and why.

**Failure:** a crash, or silence.

---

## 2. Authoring: init and resolve

### 2.1 Write a config

```
cd /tmp/ubxlab/lab
$UBX init --stack payments --dir .
```

**Correct:** `.ubx/config.hcl` is written, containing `stack =
"payments"` and commented examples for providers.

**Failure:** it overwrites an existing config without `--force`, or
writes a file that later commands cannot read.

**Known defect, expect to see it.** The generated file ends with a
next-step hint reading `ubx plan --from-doc <file>.md --stack payments`.
That flag does not exist. Running it gives `unknown flag: --from-doc`,
exit code 2. This is the first command a new user runs, and its own
advice does not work. See section 10.1.

### 2.2 Re-read the cascade now that a config exists

```
$UBX config
```

**Correct:** `stack` shows as `payments`, sourced from
`.ubx/config.hcl`, and the output names where the cascade walk stopped.

**Failure:** the key is missing, or attributed to the wrong file.

### 2.3 Resolve an intent file

Write `/tmp/ubxlab/lab/intent.json`:

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

```
$UBX resolve intent.json --provider $FAKE --ledger-dir . --out resolved.json
```

**Correct:** a summary line containing `1 create(s), 0 change(s)`, and
`resolved.json` written containing `"kind": "change"`. The proposal is a
draft: no id, no acceptance.

**Failure:** a schema error against `fake_widget` (the provider is not
being launched or its schema is not being read), or an output file whose
`kind` is not `change`.

### 2.4 Resolve refuses an unknown attribute

Add `"colour": "blue"` to the config block and re-run 2.3.

**Correct:** it refuses with exit code 2, naming the attribute:

```
resolve: resolve: unrecognized config key: payments.fake_widget.w3: config key "colour" does not exist on fake_widget
```

The schema is real and is being enforced.

**Failure:** it accepts the unknown attribute silently. That would mean
schema validation is not happening at all.

---

## 3. Plan and the receipt

`ubx plan` fuses `propose` and `resolve` with a preview render. It never
touches a ledger.

### 3.1 Plan the same intent

```
$UBX plan intent.json --provider $FAKE --ledger-dir .
```

**Correct:** a rendered receipt, ending in something like:

```
delta: +1 create(s), ~0 change(s), -0 terminate(s)

blast radius: +1 ~0 -0

ubx-proposal: 2dcf39ddb319…
next: ubx ship 2dcf39ddb319
```

The plan is also written to `.ubx/plans/<full-hash>.json`. Confirm that
file exists and that its name starts with the short hash shown.

**Failure:** no plan file written, or a hash in the file name that does
not match the one printed. The whole `ship <hash>` flow depends on those
agreeing.

### 3.2 Bare plan auto-detects

```
$UBX plan --ledger-dir .
```

**Correct:** with no SDK program in the directory it says so clearly. It
must never guess between multiple candidates: with two, it lists them
and asks you to name one.

**Failure:** it silently picks one.

### 3.3 The receipt shows full state, not just the diff

Look at the 3.1 output. Every attribute of the new resource should be
visible, including `tags`.

**Failure:** only changed keys shown. Review requires the full picture.

---

## 4. The propose, sign and ship loop

This is the core of the product. Take your time here.

### 4.1 Ship the plan

```
$UBX ship 2dcf39ddb319 --provider $FAKE --ledger-dir . --yes
```

Substitute the hash your own run printed.

**Correct:**

```
Ship  payments · 7s old · +1 create(s) ~0 change(s) -0 terminate(s)
accepted 2dcf39ddb319… (stack payments) via local plan
  ✓ payments.fake_widget.widget1: shipped                                     0:00

1 resource(s), 1 shipped, 0 failed, 0 still unknown -- outcome: shipped
```

The age (`7s old`) and the per-resource line with its own elapsed timer
are both required by the output spec.

**Failure:** a silent success with no per-resource line, no read-back
verification, or an `outcome` other than `shipped`.

### 4.2 Ship without --yes prompts

Re-plan something and run `ubx ship <hash>` with no `--yes`, on a real
terminal.

**Correct:** the full receipt renders again and a typed `yes` is
required. That prompt is the local-tier signing moment.

**Failure:** it ships without asking. On a non-TTY it must refuse
outright rather than hang or proceed.

### 4.3 Ship is idempotent

Run the exact same `ubx ship` command from 4.1 a second time.

**Correct:** the already-shipped resource is skipped, not re-created.

**Failure:** a duplicate create, or an error that leaves the ledger
inconsistent.

### 4.4 The ledger records it

```
$UBX status --ledger-dir .
$UBX history --ledger-dir .
```

**Correct:**

```
payments.fake_widget.widget1: change 2dcf39ddb319… (accepted 2026-09-08T…)
1 resource(s) (ledger-only, no live comparison)
```

and a history entry showing the summary, the resolve time, and
`accepted by [roozbeh] via local`.

**Failure:** the resource missing from `status`, or a history entry with
no acceptance recorded.

### 4.5 Chain integrity

```
$UBX verify --ledger-dir .
```

**Correct:**

```
verify: 1 proposal(s) checked
chain: intact
acceptance: 0 re-derived, 1 convenience-tier (local), 0 inconclusive
```

Exit code 0.

**Failure:** `chain: broken`, or a non-zero exit. This command is the
independent check that the ledger has not been tampered with, so a
failure here is the most serious kind in this document.

### 4.6 Tamper with the ledger and confirm verify catches it

Make a backup first. Then edit any accepted proposal JSON under
`ledger/` by hand, changing one character in a config value, and re-run
`ubx verify`.

**Correct:** it reports the chain as broken and names the proposal.

**Failure:** it still says `intact`. That would mean the hash chain is
not actually being checked, which is the single most load-bearing
property in the system.

Restore your backup afterward.

### 4.7 Explain a decision

```
$UBX why payments.fake_widget.widget1 --ledger-dir .
$UBX blame payments.fake_widget.widget1 --ledger-dir .
```

**Correct:** `why` shows the proposal, who accepted it and how, the
rendered change, and a ship history with `outcome=shipped`. `blame`
shows three attributes grouped under the proposal that set them:

```
Blame  payments.fake_widget.widget1

▸ 3 attribute(s) · set by 2dcf39ddb319… (change) · … · local
    id: "computed-id"
    name: "widget1"
    tags.env: "prod"
```

**Failure:** either command reporting the address as unknown, or `blame`
attributing an attribute to the wrong proposal.

### 4.8 Failure exit codes are a contract

```
$UBX blame payments.fake_widget.nosuch --ledger-dir .   # expect exit 2
$UBX why nosuchproposal --ledger-dir .                  # expect exit 2
```

Check with `echo $?` immediately after, and do not put these in a pipe:
a pipe reports the exit code of the last command in it, not of `ubx`.

**Correct:** exit code 2 for both, with a teaching error naming what was
not found.

**Failure:** exit 0 on a missing address, or a bare stack trace.

---

## 5. Destroy, and the two consents

Destroy is the irreversible class, and it is gated twice deliberately.

### 5.1 Terminate builds a destroy plan

```
$UBX terminate payments.fake_widget.widget1 --provider $FAKE --ledger-dir .
```

**Correct:** a full receipt showing the resource's own last-known state
(not just its name), then:

```
delta: +0 create(s), ~0 change(s), -1 terminate(s)
blast radius: +0 ~0 -1
next: ubx ship 8701eccc268f --confirm-terminate
```

**Failure:** a plan that shows only the address. You cannot review a
destroy you cannot see.

### 5.2 Shipping a destroy without confirmation must refuse

```
$UBX ship <that-hash> --provider $FAKE --ledger-dir . --yes
```

**Correct:** refused, exit code 1:

```
ship: this proposal has blast_radius.destroys > 0 -- pass --confirm-destroys to accept it
```

**Failure:** it destroys. `--yes` covers the signing prompt only, never
the destroy consent. These are two distinct consents by design.

**Minor inconsistency, expect it.** The refusal says
`--confirm-destroys` while the `next:` hint in 5.1 says
`--confirm-terminate`. Both flags work and set the same bool, but the
two messages disagree about which name to show.

### 5.3 Shipping a destroy with confirmation

```
$UBX ship <hash> --provider $FAKE --ledger-dir . --yes --confirm-terminate
```

**Correct:** the destroy runs, with a read-back verification line. Real
destroys wait honestly for eventual consistency and must narrate rather
than sit silent.

**Failure:** a silent destroy, or one that reports success without
reading back.

---

## 6. Multi-stack, history and restore

### 6.1 Aliases

Get the full head hash first. This matters:

```
$UBX history --ledger-dir . --full-hashes
$UBX alias set v1 <full-64-char-hash> --ledger-dir .
$UBX alias resolve v1 --ledger-dir .
$UBX alias list --ledger-dir .
```

**Correct:** `payments: v1 -> 2dcf39ddb319…`, then `resolve` prints the
full hash, then `list` shows the alias.

**Known defect, expect it.** `alias set` requires the full 64-character
hash. Passing the 12-character short form that every other command
prints and accepts fails with a misleading error:

```
alias set: no alias "2dcf39ddb319" in stack "payments" -- list known aliases with `ubx alias list --stack payments`
```

It reports the hash as a missing alias rather than as a hash it will not
accept. See section 10.1.

### 6.2 Aliases work anywhere a head does

```
$UBX why v1 --ledger-dir .
```

**Correct:** the same output as passing the hash.

**Failure:** the alias is only understood by `alias resolve`.

### 6.3 Restore to an earlier head

Ship a second resource first so there is something to roll back:

```
# write intent2.json creating fake_widget "widget2", then
$UBX plan intent2.json --provider $FAKE --ledger-dir .
$UBX ship <hash> --provider $FAKE --ledger-dir . --yes
$UBX restore v1 --provider $FAKE --ledger-dir .
```

**Correct:**

```
restoring payments -> ledger head 2dcf39ddb319…
Plan  payments

  - payments.fake_widget.widget2 destroy
      id: "computed-id"
      name: "widget2"
      tags: { "env": "prod" }

delta: +0 create(s), ~0 change(s), -1 terminate(s)
next: ubx ship d2d746192d2c --confirm-terminate
```

Restore is exact-state, not a merge: a resource created after the target
head is destroyed unconditionally. It resolves against current reality
rather than replaying the old proposal, so a resource that has since
drifted comes out as a modify, not a no-op.

**Failure:** widget2 left alone (that would be a merge, not a restore),
or widget1 shown as a create when it already exists.

### 6.4 Restore of a restore

Ship the restore, then restore to `v1` again.

**Correct:** the second restore is a no-op, since the stack already
matches that head.

**Failure:** it proposes changes anyway.

### 6.5 Addresses inventory

```
$UBX addresses --ledger-dir . --stack payments
```

**Known defect in the lab setup, expect it.** Because everything above
used `--provider <path>` (a local binary), the ledger records no
registry source, and `addresses` fails per resource with:

```
attributes unknown: provider @: invalid provider source address: ""
```

To exercise `addresses` properly you need the mirror path instead of
`--provider`. Place the fake provider where a registry source would
resolve:

```
mkdir -p /tmp/ubxlab/mirror/fake/widget/0.1.0/$(go env GOOS)_$(go env GOARCH)
cp /tmp/ubxlab/fakeprovider /tmp/ubxlab/mirror/fake/widget/0.1.0/$(go env GOOS)_$(go env GOARCH)/
export UBX_PROVIDER_MIRROR=/tmp/ubxlab/mirror
```

Then re-run the section 3 and 4 steps using `--source fake/widget
--provider-version 0.1.0` instead of `--provider $FAKE`, in a fresh
ledger directory, and try `addresses` again.

**Correct, on the mirror path:** one line per resource with a
copy-paste-ready `$cross` form.

Worth doing both ways regardless: the mirror path is what a real user
has, and the local-binary path is what most of this document uses.

---

## 7. Scan, drift and the write-back family

These read live state through the provider. Against `fakeprovider` that
is real wire traffic against a fixture, which is enough to exercise
classification and proposal generation.

### 7.1 Scan a resource the ledger has never seen

```
$UBX scan --type fake_widget --name widget9 --lookup '{"name":"widget9"}' \
  --provider $FAKE --ledger-dir . --stack payments
```

**Correct:** classified as a new resource, an adoption proposal printed,
exit code 1. Exit 1 here means "something to act on", not failure.

**Failure:** exit 0 with no proposal, or a crash on an unknown resource.

### 7.2 Scan a resource that matches the ledger

```
$UBX scan --type fake_widget --name widget1 --lookup '{"name":"widget1"}' \
  --provider $FAKE --ledger-dir . --stack payments
```

**Correct:** classified unchanged, exit code 0, no proposal.

**Failure:** spurious drift. If `tags` shows as gone, check that
`FAKEPROVIDER_STATE_DIR` is still exported. That exact symptom is the
fixture being stateless, not a real bug (UBI-239).

### 7.3 Fleet drift walk

```
$UBX status --drift --provider $FAKE --ledger-dir .
```

**Correct:** clean resources collapse to a trailing `N clean (--all to
show)` count. Exit 0 when everything is clean, 1 if anything drifted, 2
if anything was unreadable. Whichever is worse wins.

**Failure:** the walk aborting on the first unreadable resource instead
of recording it and continuing.

### 7.4 Render

```
$UBX render --md --stack payments --ledger-dir .
$UBX render --stack payments --ledger-dir . > stack.d2
```

**Correct:** `--md` produces a current-state markdown document listing
each resource and its attributes. Without `--md` you get a D2 diagram.

**Failure:** an empty document for a non-empty ledger.

### 7.5 Render check mode

```
$UBX render --md --stack payments --ledger-dir . --out state.md
$UBX render --md --stack payments --ledger-dir . --out state.md --check
```

**Correct:** the second exits 0. Change one character in `state.md` and
re-run: it exits 1.

**Failure:** `--check` passing on a modified file, which would make it
useless in CI.

### 7.6 Writeback and revert-plan

Both need an accepted `drift_adopt` or `drift_revert` proposal, so
produce drift first by scanning with `--propose both`. Then:

```
$UBX writeback <proposal-id> --ledger-dir . --tf-dir <dir>
$UBX revert-plan <proposal-id> --ledger-dir . --tf-dir <dir>
```

**Correct:** `writeback` prints a diff and writes nothing unless
`--write` is passed. `revert-plan` never writes at all, and separates
what is safely rewritable from what needs manual steps.

**Failure:** either one modifying a file you did not ask it to, or
`writeback` committing anything. Neither ever touches git.

---

## 8. Blueprints, MCP and the HCL wrapper

### 8.1 Build a blueprint from an Ubxfile

`resources:` in an Ubxfile is not prose and not a placeholder. It names a
pre-resolved intent/v1 document, the same wire shape `ubx resolve
--from-code --out <file>` produces, either inline or as a sibling
`.json` file. `blueprint build` has had no drafting step since UBI-224:
it only ever parses that document.

```
mkdir -p /tmp/ubxlab/bp/ci-platform
cd /tmp/ubxlab/bp

cat > ci-platform/resources.json <<'JSON'
{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "ci-platform",
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
```

**Correct:**

```
built 1 resource(s) -> /tmp/ubxlab/bp/ci-platform (go: go/bindings.go, go/ciplatform.go, go/go.mod)
```

Three files under `ci-platform/go/`. `{repo_name}` in the JSON is the
parameter placeholder, threaded through to the generated function.

**Failure:** `resources: is not a valid pre-resolved intent/v1 document`
means `resources:` is pointing at prose or a bare word rather than JSON.
That is the error, not a bug, and it is worth triggering once so you
recognise it.

**Also try:** `--lang rust`, which must be refused, and bare `blueprint
build` with no `--lang`, which builds all three languages.

### 8.2 Package and verify round trip

```
$UBX blueprint package ci-platform -o ci-platform.tar.gz
$UBX blueprint verify ci-platform
```

**Correct:** a `packaged "ci-platform" -> ci-platform.tar.gz (N file(s),
content hash sha256:…)` line, then a `verified … matches (N file(s))`
line carrying the identical hash.

The two hashes must be identical, and the file count must include
everything `build` wrote plus `Ubxfile`, `resources.json` and
`blueprint.lock.json`.

**Failure:** hashes that differ, or `verify` passing after you edit a
file in the directory. Try that second one deliberately: change one byte
in `go/ciplatform.go` and re-run `verify`. It must fail.

### 8.3 Offline pull

```
$UBX blueprint pull ci-platform.tar.gz pulled
$UBX blueprint verify pulled
```

**Correct:** `pulled ci-platform.tar.gz -> pulled`, then verification
matches. This is the offline delivery mode, the one with no git history
or registry integrity behind it, so verification is what protects it.

**Failure:** `pull` overwriting a non-empty destination. It must refuse.

### 8.4 Convert a Terraform module

```
$UBX blueprint convert --from-terraform <a-real-tf-module-dir> --out /tmp/ubxlab/converted
```

**Correct:** an Ubxfile plus built packages in `--out`. Anything the
converter cannot handle mechanically (a conditional, a data source, an
unported function) is recorded as a Question and the one affected
attribute is dropped, printed clearly, rather than guessed at or failing
the whole conversion. There is no AI in this path.

**Failure:** a silently wrong translation, or a whole-conversion failure
over one unsupported expression.

Note that running `blueprint build` on a converted directory fails with
a JSON parse error, and that is intentional: for a converted blueprint
the generated packages are the source of truth and `resources:` is
documentation only.

### 8.5 MCP server

```
cd /tmp/ubxlab/lab
{ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}'
  sleep 2
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  sleep 2
} | $UBX mcp
```

**Correct:** an initialize response naming `ubx` and its version, then
exactly eight tools:

```
build_blueprint, describe_blueprint, draft_ubxfile, list_blueprints,
ubx_scan, ubx_status, ubx_why, validate_ubxfile
```

**Failure:** any of `accept`, `ship`, `writeback`, `revert-plan`, `scan
--surface-as` or `blueprint push` appearing as a tool. Their absence is
a deliberate boundary: an assistant never signs, never writes to a live
resource, never appends to the ledger. If one shows up, that boundary
has been broken.

Also worth testing through a real client: point Claude Code's MCP config
at `ubx mcp` with `cwd` set to your lab directory, and ask it "who
changed widget1 and when". It should answer from `ubx_why`.

### 8.6 The HCL wrapper

`.ubx.hcl` is parsed, never evaluated. It is a thin deterministic
wrapper for calling blueprints, not a fourth authoring medium, and it
cannot hold a hand-written resource. The same bytes always compile to
the same intent document.

It needs a real built blueprint package to call, so do 8.3 first. Then
write `stack.ubx.hcl`:

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

**Correct:** it resolves into a draft proposal containing the
blueprint's resources, with parameters threaded through.

**Failure:** it evaluating anything (no code should run), or accepting a
hand-written `resource` block in the HCL file. Try adding one: it must
be refused.

---

## 9. What needs real credentials, and what stays forbidden

### 9.1 Forbidden regardless of credentials

**`ubx ship` against a real cloud provider.** Not for demos, not for doc
transcripts, not for verification, even against credentials already on
the machine. Use `fakeprovider`. This rule exists because it was broken
once and created real AWS resources.

Everything in sections 1 through 8 respects this.

### 9.2 Safe against a real provider, and worth doing

These fetch schemas or draft proposals. None of them applies anything.

| command | what it does |
|---|---|
| `ubx sdk gen` | fetches a real schema, generates bindings |
| `ubx resolve` | drafts a proposal, no ledger, no apply |
| `ubx plan` | drafts and renders, writes only a plan file |
| `ubx providers check` | queries the Terraform Registry |

Worth running `ubx plan` once against a real `hashicorp/aws` schema, to
confirm real-world schema size and shape do not break the renderer. Stop
before `ship`.

### 9.3 Needs real credentials, cannot be exercised in the lab

Untestable without the real thing, so treat these as unverified by this
document:

- **`ubx scan --discover`.** Enumerates via the AWS Resource Groups
  Tagging API. Needs real AWS credentials and real tagged resources.
- **Audit-log attribution** (drift and genesis, the `--no-attribution`
  opposite). Needs real CloudTrail or the equivalent per cloud.
- **`ubx accept --from-merge`.** Derives acceptance from a real merge
  commit on GitHub, GitLab, Azure DevOps, Bitbucket Server or Bitbucket
  Cloud. Needs a real repo and a real merged PR on each platform.
- **`ubx why --verify-acceptance` and `ubx verify --repo-dir`.** The
  reviewer half re-checks against a real forge API.
- **`ubx server`.** Five platforms, each with its own webhook signature
  scheme, tokens and bot identity. Needs a real installable GitHub App,
  a GitLab Group Access Token, an Azure DevOps PAT plus shared-secret
  header, and Bitbucket Server and Cloud tokens.
- **`ubx store gc`.** Only meaningful against a real S3, GCS or Azure
  Blob store. A git-local ledger's equivalent is `git gc`.
- **`ubx blueprint push` and OCI `pull`.** Need a real registry and real
  `docker login` credentials.
- **`ubx sdk gen --describe`.** Billed Claude API calls.

Together that is a large surface: the whole VCS acceptance path, the
whole server, cloud discovery, attribution, and remote stores. The
hermetic lab covers the ledger, the resolver, the executor, the
renderers and the blueprint machinery, which is the core, but it is
worth being clear-eyed that the integration edges are exactly what it
cannot reach.

---

## 10. Known defects and untested paths

### 10.1 Found while writing this document

All three were reproduced against commit `fa490ca`. None is filed yet.

1. **`ubx init` writes a hint for a flag that does not exist.** The
   generated `.ubx/config.hcl` ends with `then ubx plan --from-doc
   <file>.md --stack payments`. Running that gives `unknown flag:
   --from-doc`, exit 2. The correct flag is `--from-code`, and it takes a
   `.ts`/`.go`/`.py`/`.ubx.hcl` file, not a markdown document, so the
   hint is wrong twice over. This is the first thing a new user reads.

2. **`ubx alias set` rejects the short hash every other command
   prints.** It needs the full 64 characters. Given the 12-character form
   it reports `no alias "<hash>" in stack "payments"`, treating a hash as
   a missing alias name rather than saying it wants the full hash.

3. **The destroy refusal and the destroy hint name different flags.**
   `ubx terminate` ends with `next: ubx ship <hash> --confirm-terminate`,
   but shipping without it says `pass --confirm-destroys`. Both work, and
   the inconsistency is only in the messages.

### 10.2 Automated coverage exists, human runs do not

`ubx restore` and `ubx blame` both have real automated tests against the
fake provider, contrary to having shipped with nothing at all:

- `restore`: `TestRestore_ExactState_CreateModifyDestroy_RealFakeProvider`,
  `TestRestore_OfARestore`
- `blame`: nine tests, covering multi-touch attribution, CloudTrail
  actors, redacted attributes, genesis, JSON shape, exit codes and TTY
  rendering

What they have never had is a human running them against a real stack
and looking at the output. That is the gap, and it is a real one: an
automated test asserts what someone expected, and reading the output
yourself is what catches an answer that is technically correct and
useless. Sections 4.7 and 6.3 are where you close it.

`ubx restore` in particular deserves care. It is exact-state by design,
so it destroys anything created after the target head, unconditionally.
That is the correct behaviour and also the most surprising thing in the
CLI. Confirm the receipt makes the destruction obvious before you ship
one.

### 10.3 Sequencing note

Work sections 1 through 5 in one sitting if you can. They build one
ledger, and the later checks (`verify`, `why`, `blame`, `restore`) are
much more meaningful against a ledger with real history than against a
fresh one. Sections 6 through 8 can be picked up separately.
