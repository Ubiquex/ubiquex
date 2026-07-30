# Source tree audit — role-based package names (UBI-52), paired with the repo rename (UBI-53)

> Session 1, audit-first, no renames performed yet in this document's own
> writing — the mechanical pass (git mv + import-path sweep, both the
> internal package renames below AND the module path change
> `github.com/ubiquex/ubiquex-cli` → `github.com/ubiquex/ubiquex`) happens
> immediately after this audit lands, in the same session, so import
> paths churn exactly once (UBI-53's own stated reason for pairing the
> two tickets). See STATE.md for the real, live account of what the
> mechanical pass actually did.

## Why this document exists

The source tree grew arc-by-arc (UBI-9 through UBI-50+), each session
adding a package named for whatever made sense in that moment. Two real
naming problems accumulated as a result, both founder-flagged directly:
`tfstate/`/`tfwrite/` name an *external product* (Terraform) rather than
ubx's own role for that code, and at least one directory whose name is
opaque — not just unclear, actually undiscoverable from the name alone.
This document walks every top-level and second-level package, states
its role in one sentence, and renders a verdict: **keep** (the name
already says what it does), **rename** (the name is wrong or
inconsistent), or **delete** (dead weight, not even real).

The bar for "investigate further": can this package's purpose be stated
in one sentence from its name and package doc comment alone? If yes,
keep. If the sentence requires reading the actual code to write, that's
the signal something's wrong — either the name, or (rarer) the package
itself has a real coherence problem worth naming honestly even if not
fixed this session.

## The full tree table

| Path | Role (one sentence) | Verdict |
| --- | --- | --- |
| `audit/` | Parent namespace for pluggable per-cloud audit-log backends implementing `core.EventLookup` — no code of its own. | **Keep** — already a good precedent (named in the ticket itself). |
| `audit/azure` | Azure Activity Log backend for drift/genesis attribution. | Keep. |
| `audit/cloudtrail` | AWS CloudTrail backend for drift/genesis attribution. | Keep — see "Not every foreign name is a problem," below. |
| `audit/gcp` | GCP Cloud Audit Logs backend for drift/genesis attribution. | Keep. |
| `audit/k8s` | Kubernetes audit-log backend for drift/genesis attribution. | Keep. |
| `cli/` | The `ubx` command-line interface — every verb, one file per command family. | Keep. |
| `cmd/ubx` | The compiled binary's own `main()` entrypoint. | Keep — standard Go `cmd/<binary-name>` convention. |
| `conformance/` | UBI-9's per-resource-type conformance harness: verifies ubx's provider-level read/scan/adopt correctness against real cloud accounts, batch by batch, for AWS/GCP/Azure/K8s. | Keep — see "The `conformance/` naming tension," below. |
| `conformance/gentool` | Dev-time codegen: reads `conformance.Registry`'s `LookupHint` field, writes `core/lookuphints`' shipped table. | Keep — precisely named already. |
| `conformance/probegen` | Dev-time codegen: runs the hermetic-tier probe generator for real against every pinned provider version, writes `conformance/findings_generated.go`. | Keep. |
| `core/` | The proposal ledger: typed proposal object, canonical hashing, ledger chain, accept/apply state machine. | Keep. |
| `core/executor` | The `ship` failure-state machine (docs/executor.md). | Keep. |
| `core/lookuphints` | Generated, shipped table of per-type "how to identify this resource" teaching-error hints. | Keep — already a good precedent. |
| `core/resolver` | Resolves a hand-written/generated intent file into a draft change proposal (docs/resolver.md). | Keep. |
| `diagram/` | UBI-47's topology parser + emitter for the D2 diagram-authoring medium. | Keep — named in the ticket as a good precedent. |
| `diagram/conformance/runner` | Diagram medium's own golden-fixture conformance runner. | Keep — consistent nested pattern. |
| `discovery/` | UBI-45's cloud-side discovery: ARN → provider lookup shape, tag-scoped enumeration. | Keep. |
| `github/` | UBI-11 stage 1's PR-merge acceptance derivation (git + GitHub API). | Keep. |
| `goeval/` | The Go SDK program evaluator (compile once, sandbox-run twice). | Keep — see "The `sdkeval`/`goeval`/`pyeval` family," below. |
| `intentprovider/` | UBI-41's boundary for LLM-authored intent drafts (the md-authoring medium). | Keep — named in the ticket as a good precedent. |
| `intentprovider/claude` | intentprovider's first real `Adapter` (the Claude API). | Keep. |
| `intentprovider/conformance` | intentprovider's own fixture-runner. | Keep. |
| `ledgerstore/` | `core.LedgerStore` implementations (git-native, S3, ...). | Keep. |
| `ledgerstore/internal/lockprobe` | A real subprocess (not a goroutine) used only by ledgerstore's own live conformance tests to exercise distributed lock/CAS races as genuinely separate OS processes. | Keep — precisely named, correctly `internal/`. |
| `provider/` | Launches real Terraform/OpenTofu provider binaries and speaks their real gRPC wire protocol. | Keep — see "Not every foreign name is a problem," below. |
| `provider/internal/fakeprovider` | A real, in-process fake provider binary for hermetic tests — never a mock at the Go-interface level, a real gRPC server. | Keep — correctly `internal/`. |
| `provider/tfplugin5` | Generated protobuf/gRPC stubs for Terraform's own real plugin protocol v5. | Keep — see "Not every foreign name is a problem," below. |
| `provider/tfplugin6` | Generated protobuf/gRPC stubs for Terraform's own real plugin protocol v6. | Keep. |
| `pyeval/` | The Python SDK program evaluator (WASI via `wasmtime`). | Keep. |
| `sdk/` | The multi-language SDK monorepo: shared IR + per-language runtimes/templates/conformance (UBI-33/34/35/36). | Keep. |
| `sdk/codegen/ir` | The shared, language-neutral provider-schema → IR type model. | Keep. |
| `sdk/codegen/templates/{ts,go,py}` | Per-language codegen templates consuming the shared IR. | Keep. |
| `sdk/conformance/` | The SDK program's own golden-fixture conformance suite (all three languages). | Keep. |
| `sdk/go`, `sdk/py`, `sdk/ts` | Per-language runtime source (Go's own nested module; Python/TS plain source trees). | Keep — see "The `sdk/go` module-path tension," below. |
| `sdkeval/` | The TypeScript SDK program evaluator (sandboxed Deno subprocess). | **Rename → `tseval/`** — see "The `sdkeval`/`goeval`/`pyeval` family," below. |
| `tfstate/` | Parses a Terraform state v4 JSON file, once, as a bulk-onboarding enumeration source. | **Rename → `stateimport/`** — founder-flagged. |
| `tfwrite/` | Surgically edits an existing `.tf` file's literal attribute values to match an accepted drift-adopt proposal — implements the `ubx writeback` verb. | **Rename → `writeback/`** — founder-flagged; matches the CLI verb it implements exactly. |
| `claude-501/` | An empty, **untracked** stray directory (`.DS_Store` + an empty `scratchpad/`) — not in git history at all. | **Delete** — see "The opaque directory," below. |

Not audited as "source tree" packages (out of scope for this ticket, noted for completeness): `.claude/` (local, untracked, Claude Code session settings), `.github/workflows/` (CI, swept separately for the module-path reference, not renamed), `docs/` (documentation, not code — `docs/upstream/` is a real, well-named draft-upstream-report holding area, not opaque).

## The opaque directory: `claude-501/`

Found by direct inspection, not by name alone: `claude-501/` at the repo
root contains only a `.DS_Store` and an empty `scratchpad/` subdirectory.
`git log --all -- claude-501` returns nothing; `git ls-files claude-501`
returns nothing — **this directory was never committed**. It matches
this session's own scratchpad-path naming convention exactly
(`/private/tmp/claude-501/-Users-.../scratchpad`, confirmed by grepping
the same string inside the untracked `.claude/settings.local.json`) —
almost certainly a stray artifact from an earlier session that wrote to
a relative path instead of the real absolute scratch location. It is
not "opaque and alive" (the ticket's other named case, "rename if
alive") — it is dead and was never real. Deleted, not renamed.

## `tfstate/` → `stateimport/`, `tfwrite/` → `writeback/`

Both exactly the founder's own examples. `tfstate/` reads a Terraform
state file exactly once, at onboarding, as an identity-enumeration
source for `ubx scan --all --tfstate` — its role is "import identity
data to bootstrap onboarding," not "a Terraform state file parser" (that
undersells and mis-centers what the package is *for* inside ubx's own
model). `onboarding/` was the ticket's other suggested name, rejected
here for a real reason found during the audit: `docs/discovery.md`
already established "onboarding" as the user-facing *feature* name for
**two** independent mechanisms (`--all --tfstate` AND `--discover`,
cloud-side, no state file at all — see `guides/cloud-discovery.mdx`).
Naming only the tfstate-specific package `onboarding/` would wrongly
imply it owns the whole concept. `stateimport/` names exactly and only
what this package does, with no collision.

`tfwrite/` surgically edits an existing `.tf` file's literal attribute
values — its own package doc comment already describes this precisely;
the problem is purely that the name centers Terraform's own file format
rather than ubx's role. It implements the **already-existing** `ubx
writeback` CLI verb (`cli/writeback.go` calls `tfwrite.FindAndApply`
directly) — `writeback/` isn't a new name being invented, it's the name
this package's own caller already uses for it.

## The `sdkeval`/`goeval`/`pyeval` family: a real inconsistency, found during this audit

Not named in either ticket, found by simply reading the three evaluator
package names side by side. `goeval` and `pyeval` both encode which
language they evaluate; `sdkeval` doesn't — it was named when it was the
*only* SDK evaluator (UBI-34, before Go or Python existed), and
"sdkeval" read as complete and unambiguous then. It no longer is: a
newcomer seeing `sdkeval`/`goeval`/`pyeval` together has a real reason
to wonder whether Go/Python programs are also "the SDK" and why only
TypeScript gets the generic name. Renamed to `tseval/` for the same
"role over technology, consistent family" reasoning the rest of this
audit applies — and because this session already builds the mechanical
git-mv + import-sweep tooling for the other three renames, the marginal
cost of a fourth is small.

## Not every foreign name is a problem

`provider/tfplugin5`, `provider/tfplugin6`, and `audit/cloudtrail` all
name a real, external thing — Terraform's own wire protocol, AWS's own
audit-log product — and are correct to do so, unlike `tfstate/`/
`tfwrite/`. The distinguishing test, applied consistently across this
whole audit: does the name describe **what ubx does with it** (wrong —
`tfstate` describes a file format, not "import identity for
onboarding") or **which real external system this code is a client
for** (right — `tfplugin5` IS the generated binding for Terraform's own
real protocol v5; renaming it to some ubx-role name would be actively
misleading, since these types must match the wire protocol's own real
field names exactly). `cloudtrail` is CloudTrail's own real name for
AWS's own audit service, in a package that only exists because it's a
CloudTrail client — the same shape as `tfplugin5`, not the same shape
as `tfstate`.

## The `conformance/` naming tension — audited, accepted, not renamed

The original, top-level `conformance/` package (UBI-9) predates every
later `<subsystem>/conformance/` package (`diagram/conformance`,
`intentprovider/conformance`, `sdk/conformance`) and is the one
inconsistent case in an otherwise-clean pattern: everywhere else,
"conformance" is a nested suffix naming which subsystem it's testing;
here it's a bare top-level name. Real, but not urgent enough to force a
rename this session for cosmetic gain alone — `conformance/` still
passes the one-sentence-purpose bar on its own (per-resource-type
provider conformance, the original and still-largest of the four), and
a rename here would touch the largest single blast radius of any
package in the tree (dozens of files, all of `conformance/`'s own real,
substantial live-test suite) for a naming win, not a role clarification
the way `tfstate`/`tfwrite`/`sdkeval` are. Recorded honestly as an
audited, accepted inconsistency, not a silent miss.

## The `sdk/go` module-path tension — a real, honest note

UBI-53's own original description (filed before UBI-35 existed) named
its own sequencing reason as "sdk/go's future nested-module path
benefits from doing this BEFORE UBI-35 ships" — i.e., the original
intent was for the Go SDK runtime to live at
`github.com/ubiquex/ubiquex/sdk/go`, a true nested module under the
renamed main repo. UBI-35 (Go SDK) shipped in a prior session, under
direct instruction, before this rename — with its own independent
module identity, `github.com/ubiquex/ubx-sdk-go` (mirroring `@ubx/sdk`'s
own npm-package-style naming, not nested under the CLI repo's own
path). That decision is already real: shipped, tested, documented in
`ubiquex-docs`, referenced by three separate `go.mod` `replace`
directives across the conformance suite and test fixtures. This
session's own given scope is the tree audit + the main module path
swap — not a second rename of `sdk/go`'s own already-shipped, already-
public-facing identity. Recorded honestly, not silently ignored,
matching this project's own established precedent for handling a stale
sequencing note (the same treatment UBI-35's own session gave the
UBI-53-before-UBI-35 tension in the first place).

## The lookup-hint tables: consolidated (UBI-54)

UBI-45's own session 1 found **three** separately-maintained copies of
the same tiny per-type lookup-hint fact
(`conformance.Registry.LookupHint`, generated `core/lookuphints`,
`tfstate.BuildLookup`'s own `extraLookupAttrs`); UBI-45 session 2 added a
**fourth**: `discovery/tiers.go`'s own `tierTable.AugmentFields`. UBI-52's
own audit (above) found the count still at four, still not consolidated,
and recommended a dedicated ticket be filed.

UBI-54 filed that ticket and closed it. The dependency direction the
ticket asked to be verified before committing to it held up:
`conformance.Registry.LookupHint` (hand-maintained, authoritative,
machine-complete/version-aware since UBI-50, deliberately test-only —
never imported by shipped code) → generated `core/lookuphints` (already
existed since UBI-20, already the correct shipped-code-safe view,
nothing new to build) → now genuinely the **one** shipped source, with
three consumers instead of one:

- `core/scan.go`'s teaching-error mechanism (UBI-20) — already wired,
  needed no change.
- `stateimport.BuildLookup` — its own hand-duplicated `extraLookupAttrs`
  map is gone; it now calls `lookuphints.For("hashicorp/aws",
  resourceType)`. The hardcoded source matches this package's own
  pre-existing, real behavior exactly (`BuildLookup` never took a source
  parameter and every augmented type was already AWS-only) — named
  honestly as a pre-existing limitation, not newly introduced or newly
  hidden.
- `discovery/tiers.go`'s `tierTable` — its own `AugmentFields` field is
  gone; `BuildLookup`'s Tier-B branch now calls the same `lookuphints.For`
  call, hardcoded to `"hashicorp/aws"` for the same reason (discovery is
  itself AWS-only: ARN parsing, the tagging API). `tierTable` itself was
  **not** replaced — its `Tier`/`Construct`/`CreationVerbs` knowledge has
  no counterpart in any other table and stays exactly as it was.

Zero behavior change, verified rather than assumed: every existing test
in `stateimport`, `discovery`, and `conformance` (including the live
teaching-error round trip, `conformance/lookuphints_live_test.go`) passed
unmodified — none of those tests asserted the internal map/field shape
directly, only `BuildLookup`'s returned JSON / `ClassifyARN`'s tier / the
teaching-error message text, so swapping the internal data source needed
no test-file edits at all.

## The mechanical pass, paired (UBI-53)

Performed in the same session, immediately after this audit — see
STATE.md for the real, live account (files touched, test results,
verification commands actually run). Scope:

1. `git mv` for all four internal renames (`tfstate/` → `stateimport/`,
   `tfwrite/` → `writeback/`, `sdkeval/` → `tseval/`, `claude-501/`
   deleted).
2. `go.mod`'s own module line: `github.com/ubiquex/ubiquex-cli` →
   `github.com/ubiquex/ubiquex`.
3. One import-path sweep covering both the internal package renames and
   the module path change together — every internal import touched
   exactly once, not twice.
4. **The one real hashed-content consequence, found by checking, not
   assumed** (this arc's own version of UBI-53's own "ledger integrity"
   check): `sdk/conformance/programs/go/payments.go` imports its own
   sibling `generated` package via the full module path
   (`github.com/ubiquex/ubiquex-cli/sdk/conformance/programs/go/generated`)
   — changing that import line changes `payments.go`'s own file bytes,
   which changes its own real SHA-256 content hash, which is exactly
   what `sdk/conformance/golden/payments_go.json`'s own
   `intent.sources[0].content_hash` field freezes. That golden fixture's
   content hash needed regenerating against the real post-rename file
   (the same way the original fixture was generated — run the real
   evaluator, capture the real output — never hand-edited). No other
   golden/ledger/proposal fixture in the repo references the module
   path in a way that reaches hashed content (checked directly: grepped
   every `.json` fixture and every SDK program in `sdk/`,
   `cli/testdata/`, `goeval/testdata/`, `pyeval/testdata/` for the
   literal string).
5. `go build/vet/test ./...` green, `gofmt -l .` clean, `ubx verify`
   green, full existing suite re-run with zero regressions.
6. Every non-Go reference swept: `CLAUDE.md`, `STATE.md`, `docs/*.md`,
   `.goreleaser.yaml`, `.github/workflows/`, and `ubiquex-docs`' own
   pages citing the repo path.
7. GitHub-side rename performed by the founder once this session
   confirms everything above is green (GitHub's own redirect covers
   `git clone`/`git fetch` against the old URL, best-effort, not
   forever — the real fix is this session's own module-path change,
   not reliance on the redirect).
8. **Founder action, not performed by this session**: the local checkout
   directory itself (`~/Ubiquex/ubiquex-cli` → `~/Ubiquex/ubiquex`) —
   this session runs *from inside* that directory and cannot safely
   rename its own containing directory mid-session. Recorded here as an
   explicit manual follow-up, not silently assumed done.

## Naming convention (recorded in CLAUDE.md too, per the ticket)

**Name a package for ubx's own role, in ubx's own vocabulary — not for
whatever external product, file format, or protocol the code happens to
touch — unless the package's entire reason to exist IS being a client
for that exact external thing (a generated protocol binding, a named
cloud product's own API client), in which case the external name IS the
correct, precise one.** Keep names short and lowercase, matching
`audit/`, `diagram/`, `sdkeval` → `tseval`/`goeval`/`pyeval`,
`intentprovider/` — every one states its role in roughly two words.
When a package implements an existing CLI verb one-to-one, prefer
naming it after that verb (`tfwrite` → `writeback`, matching `ubx
writeback`) — the reader already knows that vocabulary. Test the name
against one sentence: if you can't state the package's purpose in one
sentence using the name itself, the name is wrong, not just imprecise.
