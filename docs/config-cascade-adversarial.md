# Config cascade + formats adversarial program (UBI-32 Arc A)

> Every row here is a failure or edge case injected on purpose, with a
> REQUIRED observable outcome — written as the spec, before any code
> exists to pass it, the same discipline docs/multi-provider-adversarial.md,
> docs/executor-adversarial.md, docs/resolver-adversarial.md, and
> docs/destroys-adversarial.md already established. Each row becomes a
> test in `cli/config_test.go`'s hermetic suite (real temp-directory
> trees with real files in all three formats — no fake filesystem, since
> the whole point is the cascade's own directory-walking and per-format
> parsing) before it becomes a claim about `ubx`'s own behavior. This
> document is also a future published reliability report, alongside its
> four siblings: read each row as a claim `ubx` makes about its own
> config-loading behavior, not just a test-plan checklist.
>
> This is the *minimum* required program per UBI-32 Arc A's own scope —
> not a claim that failure space around config loading is exhausted. See
> "What this table doesn't yet cover," below, for named gaps.

## How to read this table

"Injection" describes exactly what's forced to happen — which files
exist, in which formats, at which directory levels, relative to
docs/architecture.md's own "Config: cascading, per-key, child overrides
parent" and "Config formats" sections (both amended 2026-07-18, UBI-32
Arc A session 1). "Required outcome" is the full observable contract:
what `LoadConfig` must return, what provenance must report, and what
must be warned or hard-refused. A row that can't be made to produce its
required outcome is a bug, not an acceptable gap — this table has no
"known limitation" column, same standard as its four siblings.

## The program

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 1 | Conflicting scalar key, two levels | Root `.ubx/config` sets `stack = "root-stack"`. A child directory's own `.ubx/config` sets `stack = "child-stack"`. `ubx` is invoked from the child directory. | `LoadConfig` returns `Stack == "child-stack"` — the nearest definition of *this key* wins outright, the root's own value never partially blends in. Provenance for `stack` names the child's file, never the root's. |
| 2 | Conflicting table key, sibling keys preserved | Root sets `[provider_configs."hashicorp/aws"]` with both `region = "us-east-1"` and `retries = "3"`. A child directory's own config sets only `[provider_configs."hashicorp/aws"]` `region = "us-west-2"` — no `retries` key at all. | The merged config's `ProviderConfigs["hashicorp/aws"]` has **both** keys: `region` from the child (nearest definition of that specific key wins), `retries` still present from the root (the child's own file never having mentioned it must never delete it). Provenance for `provider_configs."hashicorp/aws".region` names the child's file; provenance for `provider_configs."hashicorp/aws".retries` names the root's — proving the merge is per-key even inside a nested nested table, not per-table. |
| 3 | Cross-format directories in one cascade chain | Root directory's config is `config.toml`; an intermediate directory's is `config.hcl`; the leaf (invocation) directory's is `config.yaml`. Each sets a different, non-overlapping key (`github_repo` at root, `tf_dir` at the intermediate level, `stack` at the leaf). | All three values are present in the final merged config, each correctly typed and each correctly attributed to its own file (by path, distinguishable by format-specific extension) in provenance — proving the merge algorithm operates on the shared generic-tree shape and never on a format-specific representation that would only compose within one format. |
| 4 | Same directory, multiple format files present | One directory contains both `.ubx/config.hcl` and `.ubx/config.toml` (e.g. a stray leftover from switching formats via `ubx init --force --format=...` twice). The HCL file sets `stack = "hcl-stack"`; the TOML file sets `stack = "toml-stack"`. | `config.hcl` wins for that directory outright (discovery order: `config.hcl` → `config.toml` → `config` → `config.yaml`, first found wins) — `config.toml`'s content is never read, never merged, never contributes to provenance, and its presence produces no warning of its own (an unused sibling file is not this loader's concern). `Stack == "hcl-stack"`. |
| 5 | YAML coercion refusal — numeric precision loss | `.ubx/config.yaml` sets a `[provider]` `version` value as a bare, unquoted `6.60` (no surrounding quotes). | `LoadConfig` returns a hard error naming the file, the key path (`provider.version`), and the literal token found (`6.60`) — never silently narrowing to the float `6.6` and never silently stringifying it either. The same file with the value quoted (`version: "6.60"`) loads cleanly with `Version == "6.60"`, proving quoting is the escape hatch, not a workaround for a bug. |
| 6 | YAML ambiguous-boolean tokens are NOT rejected — because they're not ambiguous in this parser | `.ubx/config.yaml` sets `github_repo: no` (bare, unquoted) — an attempt to use a literal repository-ish string that happens to collide with YAML 1.1's boolean-alias vocabulary. | Loads cleanly, `GithubRepo == "no"` — `gopkg.in/yaml.v3`'s own implicit resolver already treats `no`/`yes`/`on`/`off` (any case) as `!!str`, confirmed directly against the library before this row was written, not assumed from YAML's general reputation. This row exists specifically so a future dependency bump that changed that resolver behavior would be caught immediately by a failing test, not discovered by a user's config silently misbehaving. |
| 7 | HCL literal-only violation | `.ubx/config.hcl` sets `stack = "${env.STACK_NAME}"` (an interpolation) in one variant of this row, and `tf_dir = upper("./terraform")` (a function call) in another. | `LoadConfig` returns a hard error for the whole file — naming the offending attribute and the fact that config permits literal values only, no variables/functions/interpolation (`expr.Value(nil)` on that attribute's expression fails, exactly the mechanism `tfwrite` already uses on resource attributes). The file contributes **nothing** to the merged config on this path — not even its other, genuinely-literal keys — matching the existing "a file that isn't valid at all is a hard error" precedent already established for malformed TOML, never a partial per-key salvage. |
| 8 | Per-directory stack default, rest inherited | A stack directory's own config is exactly `stack = "payments"` and nothing else. A parent directory's config supplies `[provider]` `source`/`version` and `tf_dir`. `ubx` is invoked from the stack directory with no CLI flags at all. | The merged config has `Stack == "payments"` (from the stack directory) AND the parent's `provider.source`/`provider.version`/`tf_dir` all present and correctly attributed to the parent's own file in provenance — proving a directory needs to declare only the ONE key it actually differs on, never having to repeat everything a parent already established. |
| 9 | Provenance correctness is format-blind | Three cascade levels, three different formats (as in row 3), each setting a distinct key. | The provenance view (`ubx config`) prints each key's resolved value AND the exact absolute file path that supplied it, and that path's extension matches whichever format actually won discovery at that level (`.hcl`/`.toml`/`.yaml`/extensionless legacy `config`) — proving provenance tracking happens on the generic tree (format-agnostic) rather than being hard-coded to expect TOML paths specifically, a regression that would be invisible until someone actually adopted HCL or YAML and checked provenance. |
| 10 | Unknown-key warning, identical typo, three formats | The same typo key (`stcak = "payments"`, a misspelling of `stack`) is written once each in a TOML file, an HCL file, and a YAML file, each loaded in isolation (single-directory, no cascade). | All three emit a warning naming the exact key (`stcak`) and the exact file it came from, and all three otherwise load every OTHER, correctly-spelled key in that same file normally — proving unknown-key detection is implemented once, generically, against the parsed tree's shape, not three times against three different libraries' own differing "undecoded keys" APIs (`BurntSushi/toml`'s `MetaData.Undecoded()` is structurally unavailable once parsing targets a generic map rather than the `Config` struct directly — confirmed empirically, not assumed, before this row was written). |
| 11 | Freeform tables never spuriously warn | A config file sets `[provider_config]` with an entirely invented key (`totally_custom_flag = true`) and a `[provider_configs."some/vendor"]` table with an equally invented key. | No warning for either — `provider_config`/`providers`/`provider_configs` are freeform by design (arbitrary provider-defined keys), and the unknown-key check never descends into them past their own top-level table name. Only a truly unrecognized *top-level* key, or an unrecognized key inside `[provider]`/`[k8s_audit]` specifically (the two tables with a fixed, known shape), ever warns. |

## The cascade ceiling program (UBI-32 Arc B addendum, 2026-07-19)

The rows below cover docs/architecture.md's own "Cascade ceiling" and
"User-global `~/.ubx/config`" sections (both amended the same day) —
where the upward walk stops, and the separate, allowlist-only
`$HOME`-rooted layer outside it entirely.

| # | Scenario | Injection | Required observable outcome |
| --- | --- | --- | --- |
| 12 | `root = true` stops collection mid-tree | A three-level directory chain: grandparent sets `github_repo = "acme/infra"`; parent sets `root = true` AND `tf_dir = "./terraform"`; child (invocation directory) sets `stack = "payments"`. | The merged config has `Stack == "payments"` and `TFDir == "./terraform"` (the `root = true` directory's own other keys still apply — inclusive stop, not "ignore this file too") but `GithubRepo == ""` — the grandparent's own file is never read at all, not merged, not warned about, not present in `Files`. Provenance has no entry naming the grandparent for any key. |
| 13 | `root` present but not a literal boolean | A config file sets `root = "true"` (a quoted string, in whichever format still lets that type-check as *something* other than a bool — TOML/HCL/YAML all distinguish a quoted string from a bare boolean). | A hard error naming the file and explaining `root` must be a literal boolean — never silently treated as truthy, never silently treated as absent; the whole file contributes nothing to the cascade, same "malformed file, no partial salvage" posture as every other hard-error row in this program. |
| 14 | No `root` marker anywhere → the git repo boundary is the ceiling | A directory tree with a `.git` directory at some ancestor (a real, if empty, git repository) and a config file exactly at that same directory setting `stack = "payments"`; a further ancestor, *outside* the repo, sets `github_repo = "should-never-be-read"`. No `root` key anywhere. | The repo-root directory's own `stack = "payments"` is read and applied (inclusive stop — the boundary directory's own config still counts); the further-out ancestor's `github_repo` is never read, never merged, never contributes to `Files` or provenance — proving `.git`'s mere presence (a directory is sufficient; contents are never inspected) is enough to stop the walk with no explicit `root` marker needed. |
| 15 | Outside any repo, `$HOME` is the ceiling | The invocation directory (and every ancestor up to a fake `$HOME`, via the test-only `userHomeDir` override) contains no `.git` anywhere and no `root` marker; `$HOME` itself has a config setting `stack = "home-fallback"`; `$HOME`'s own parent has a config setting `github_repo = "should-never-be-read"`. | `$HOME`'s own `stack = "home-fallback"` is read (inclusive stop, same as the `.git` case); `$HOME`'s parent is never read — proving the fallback ceiling activates correctly once neither of the first two rules ever fired, not just when explicitly asked for. |
| 16 | Outside any repo, no `$HOME` match either → filesystem root is the ceiling | The walk starts and continues through directories that are neither inside a git repo, nor ever reach the (test-overridden, deliberately unrelated) fake `$HOME` path at all, all the way to the real filesystem root. | The walk terminates at the filesystem root without error (`parent == dir`, the same loop-termination condition the original nearest-file-wins walk already used) — proving the ceiling logic degrades gracefully to exactly today's original top-level behavior when none of the first three rules ever apply, never an infinite loop or a crash. |
| 17 | User-global config: a project-truth key attempt is refused loudly | `~/.ubx/config` (via the test-only `userHomeDir` override) sets `stack = "should-never-apply"` — a real project-truth key, not a typo. | `LoadConfigResolved` returns a hard error naming the file and the key, explaining it's a project-truth key not allowed in user-global config — never a warning, never silently dropped, and never merged into the resolved config (a caller checking `err != nil` sees this before ever looking at `Config.Stack`). |
| 18 | User-global config: an unrecognized key is *also* refused loudly, not just warned | `~/.ubx/config` sets `totally_made_up_key = "x"` — not a project-truth key by name, but also not on the personal-preference allowlist (which has exactly one entry, `init_format`, today). | Also a hard error, identically to row 17 — user-global config's own allowlist enforcement doesn't distinguish "a real project key snuck in" from "a plain unrecognized key snuck in": both are outside what's allowed there, and the normal cascade's own separate "unknown keys warn, they don't fail" leniency does not apply to this file at all. |
| 19 | User-global config: the one real personal-preference key works, and only from `$HOME` | `~/.ubx/config` sets `init_format = "yaml"`. `ubx init` is run with no `--format` flag, from a project directory whose own cascade never mentions `init_format` at all (it isn't a project-cascade key). | `ubx init` writes `.ubx/config.yaml` (not the hardcoded `hcl` default) — proving `init_format` actually changes behavior, and does so from the user-global file specifically, never by being placed in a project's own `.ubx/config` (which would just warn as an unrecognized key there, since `init_format` is not on *that* surface's known-key list at all). |
| 20 | Provenance renders the ceiling, all four reasons | Four separate scenarios, one per ceiling reason (root marker, repo boundary, `$HOME`, filesystem root), each run through `ubx config`. | Each invocation's output names which rule stopped the walk and where — `root marker (<file>)`, `repo boundary (<dir>)`, `$HOME (<dir>)`, or `filesystem root (<dir>)` respectively — never a generic "cascade complete" with no explanation of why it stopped where it did. |

## A real gap found live, fixed the same session

Row 4 (above) established what discovery does when a directory already
holds two format files: first-found wins, silently. Live-verifying
`ubx init` against that same shape found a real, separate gap in `ubx
init` itself, not in discovery: `ubx init`'s own overwrite-protection
check only ever compared against the *exact target filename*
(`.ubx/config.hcl` for the default format) — a directory that already
had a working legacy `.ubx/config` (no extension, TOML) was not "already
exists" from that check's point of view at all, so a bare `ubx init` run
a second time in an already-configured directory would silently write a
brand-new `.ubx/config.hcl` right alongside it. Per row 4's own
discovery order, that new (empty, since no flags were given) file would
then silently win discovery going forward — every value the existing
legacy config supplied would vanish from `ubx`'s own point of view,
without a single error or warning anywhere. Fixed the same session:
`ubx init` now also checks `pickConfigFile` (configcascade.go's own
per-directory discovery function) and refuses, naming the conflicting
file and requiring `--force`, whenever it would write a config file into
a directory that already has one under a *different* name — regardless
of which one would end up winning discovery. See
`TestInit_RefusesToShadowADifferentFormatInTheSameDirectory` in
`cli/init_test.go`.

## What this table doesn't yet cover, named rather than assumed

Not covered by a row above, and therefore not yet a claim `ubx` makes
about itself: two directories in the same cascade chain declaring
genuinely incompatible shapes for the *same* nested key (one directory's
`provider_configs."hashicorp/aws"` is a table, an ancestor's own
`provider_configs."hashicorp/aws"` was somehow a bare scalar — not
expressible through this loader's own template/`ubx init` path today,
so not yet exercised as a deliberate injection); a config file that is
syntactically valid in its own format but decodes to a shape the generic
merge can't reconcile at all (e.g. a YAML document whose top level is a
list, not a mapping) — presumed a hard parse-time error identical in
spirit to malformed TOML, but not yet its own explicit row; extremely
deep cascade chains (more than a handful of directory levels) — the
algorithm is recursive per key but not depth-bounded, and no row yet
proves there's no practical depth where this becomes slow or wrong;
symlinked directories in the cwd→root walk (a symlink could make the
"same" directory appear at two different points in the walk, double
counted) — not yet a deliberately injected case; and the interaction
between this cascade and `ubx scan --all`'s own filename-derived stack
default (UBI-18) — docs/architecture.md's own precedence chain places
that filename fallback strictly *after* config in the resolution order,
but this table's own rows are all plain `LoadConfig` scenarios, not
`ubx scan --all`'s specific three-way precedence.

Added by the UBI-32 Arc B addendum, also not yet covered: a `.git`
*file* (a real worktree/submodule pointer, not a directory) as the
detected repo boundary — the detection code accepts either, but no row
yet injects the file-shaped case specifically, only a plain directory;
nested repos (a git checkout inside another git checkout, e.g. a
vendored submodule) — which `.git` the walk finds first going upward is
whichever is nearest, presumed correct by the same "nearest wins"
logic everything else in this cascade already follows, but not exercised
as its own deliberate row; and `root = true` written inside
`~/.ubx/config` itself — user-global config sits outside the cascade
walk entirely, so a `root` key there has no walk to stop and is simply
rejected by the same allowlist enforcement every other non-`init_format`
key there is (row 18's own shape), but no row names this specific case
explicitly.
