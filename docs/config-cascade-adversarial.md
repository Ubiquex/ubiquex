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
