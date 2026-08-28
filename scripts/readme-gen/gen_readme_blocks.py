#!/usr/bin/env python3
"""Generates the volatile part of a README: resource/data-source counts,
real current published versions, and the standing cross-repo links block.

UBI-192's own decision, not assumed: rather than hand-write counts and
versions into eleven-plus READMEs (guaranteed to go stale the moment
anything regenerates, matching the exact problem UBI-191's own
sync-drift mechanism exists to prevent), the volatile parts are
generated fresh and spliced between two marker lines:

  <!-- README-GEN:BEGIN -->
  ...generated content, safe to overwrite...
  <!-- README-GEN:END -->

Everything outside the markers is real, hand-written content and is
never touched by this script.

Real counts (resources/data sources per provider) come from a real
`ubx sdk gen --dump-ir` run -- this script does not run that itself
(Azure alone needs ~12.5GB peak memory, confirmed UBI-204, not
something to shell out to from a lightweight docs script) -- it reads
a pre-generated counts.json in the shape this script's own
`--counts-from-dump-ir` helper mode produces. Versions come from a
real, live query against the target repo's own GitHub release, npm
package, or PyPI package -- never a hand-maintained table -- at the
moment this script runs, matching this project's own established
"verify against real, current source" discipline (CLAUDE.md rule 8).
"""

import argparse
import json
import subprocess
import sys

BEGIN_MARKER = "<!-- README-GEN:BEGIN -->"
END_MARKER = "<!-- README-GEN:END -->"

LINKS_BLOCK = """- Docs: https://docs.ubiquex.io
- Internals (architecture and design): https://github.com/Ubiquex/ubiquex-internals
- Linear board: https://linear.app/ubiquex"""


def counts_from_dump_ir(dump_dir):
    """Real counts per provider from a real `--dump-ir` output directory.

    A data source is identified the same way `extract_idents.py` (in
    ubiquex-docs) and every real schema in this org already does: its
    wire type starts with `data_`. Confirmed against known-good figures
    from a real coverage-watch.yml run before trusting this (aws
    1715/4884, datadog 176/528, github 80/262, google 1546/792,
    kubernetes 92/116 all matched exactly) -- a naive OR-check against
    `<provider>_data_` also tried and rejected: it misclassified 39 real
    AWS resources (`aws_data_brew_dataset`, `aws_data_pipeline_pipeline`,
    genuine AWS "Data..."-branded services) as data sources purely
    because their own name happens to start with `aws_data_`.
    """
    import os

    counts = {}
    for provider in sorted(os.listdir(dump_dir)):
        schema_path = os.path.join(dump_dir, provider, "schema.json")
        if not os.path.isfile(schema_path):
            continue
        with open(schema_path) as f:
            wires = json.load(f)
        data_sources = sum(1 for k in wires if k.startswith("data_"))
        resources = len(wires) - data_sources
        counts[provider] = {"resources": resources, "data_sources": data_sources}
    return counts


def real_github_release(repo):
    out = subprocess.run(
        ["gh", "api", f"repos/Ubiquex/{repo}/releases/latest", "--jq", ".tag_name"],
        capture_output=True, text=True,
    )
    if out.returncode == 0 and out.stdout.strip():
        return out.stdout.strip()
    return None


def real_go_tag(repo, prefix="sdk/go/v"):
    """Go bindings in this org are versioned by git tag, never a GitHub
    Release -- confirmed live, `ubx-sdk-aws`'s own real /releases is
    empty while its real tags carry `sdk/go/v2.1.0`. Tags API order is
    not a documented sort guarantee, so this does a real semver compare
    rather than trusting list order."""
    out = subprocess.run(
        ["gh", "api", f"repos/Ubiquex/{repo}/tags", "--jq", ".[].name"],
        capture_output=True, text=True,
    )
    if out.returncode != 0:
        return None
    candidates = [n for n in out.stdout.splitlines() if n.startswith(prefix)]
    if not candidates:
        return None

    def semver_key(tag):
        raw = tag[len(prefix):].lstrip("v")
        parts = raw.split(".")
        return tuple(int(p) if p.isdigit() else 0 for p in (parts + ["0", "0", "0"])[:3])

    return max(candidates, key=semver_key)


def real_npm_version(package):
    out = subprocess.run(["npm", "view", package, "version"], capture_output=True, text=True)
    if out.returncode != 0:
        return None
    return out.stdout.strip() or None


def real_pypi_version(package):
    out = subprocess.run(
        ["curl", "-fsSL", f"https://pypi.org/pypi/{package}/json"],
        capture_output=True, text=True,
    )
    if out.returncode != 0 or not out.stdout:
        return None
    try:
        return json.loads(out.stdout)["info"]["version"]
    except Exception:
        return None


def sdk_repo_block(provider, counts):
    c = counts.get(provider)
    if c is None:
        counts_line = "resource/data-source counts unavailable this run"
    else:
        counts_line = f"{c['resources']} resource types, {c['data_sources']} data source types"
    go_tag = real_go_tag(f"ubx-sdk-{provider}")
    go_ver = go_tag.split("/")[-1] if go_tag else None
    npm_ver = real_npm_version(f"@ubx/sdk-{provider}")
    pypi_ver = real_pypi_version(f"ubx-sdk-{provider}")
    lines = [
        BEGIN_MARKER,
        f"**Real, current counts** (`ubx sdk gen --dump-ir`): {counts_line}.",
        "",
        "**Real, current published versions:**",
        f"- Go: `{go_ver or 'unreleased'}`",
        f"- npm (`@ubx/sdk-{provider}`): `{npm_ver or 'unreleased'}`",
        f"- PyPI (`ubx-sdk-{provider}`): `{pypi_ver or 'unreleased'}`",
        "",
        "## Links",
        "",
        LINKS_BLOCK,
        END_MARKER,
    ]
    return "\n".join(lines)


def schema_repo_block(repo):
    ver = real_github_release(repo)
    lines = [
        BEGIN_MARKER,
        f"**Real, current published version:** `{ver or 'unreleased'}`",
        "",
        "## Links",
        "",
        LINKS_BLOCK,
        END_MARKER,
    ]
    return "\n".join(lines)


def links_only_block():
    return "\n".join([BEGIN_MARKER, "## Links", "", LINKS_BLOCK, END_MARKER])


def splice(readme_text, generated_block):
    if BEGIN_MARKER in readme_text and END_MARKER in readme_text:
        pre = readme_text.split(BEGIN_MARKER)[0]
        post = readme_text.split(END_MARKER)[1]
        return pre + generated_block + post
    return readme_text.rstrip() + "\n\n" + generated_block + "\n"


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--dump-ir-dir", help="real ubx sdk gen --dump-ir output directory")
    p.add_argument("--write-counts", help="write parsed counts.json here and exit")
    p.add_argument("--sdk-provider", help="emit an SDK repo block for this provider")
    p.add_argument("--schema-repo", help="emit a schema repo block for this repo name")
    p.add_argument("--links-only", action="store_true", help="emit just the links block")
    p.add_argument("--counts-file", help="real counts.json to read instead of --dump-ir-dir")
    p.add_argument("--splice-into", help="README file to splice the block into, in place")
    args = p.parse_args()

    counts = {}
    if args.dump_ir_dir:
        counts = counts_from_dump_ir(args.dump_ir_dir)
    elif args.counts_file:
        with open(args.counts_file) as f:
            counts = json.load(f)

    if args.write_counts:
        with open(args.write_counts, "w") as f:
            json.dump(counts, f, indent=2, sort_keys=True)
        return

    if args.sdk_provider:
        block = sdk_repo_block(args.sdk_provider, counts)
    elif args.schema_repo:
        block = schema_repo_block(args.schema_repo)
    elif args.links_only:
        block = links_only_block()
    else:
        p.error("one of --sdk-provider / --schema-repo / --links-only is required")
        return

    if args.splice_into:
        with open(args.splice_into) as f:
            current = f.read()
        with open(args.splice_into, "w") as f:
            f.write(splice(current, block))
    else:
        print(block)


if __name__ == "__main__":
    main()
