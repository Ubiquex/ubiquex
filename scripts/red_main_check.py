#!/usr/bin/env python3
"""Report every repository whose main branch is failing its own CI.

# Why this exists

CI already catches a broken main. Nothing says so.

On 2026-09-15 two pull requests merged cleanly and left `main` unable to
compile its tests. One added a test calling a function with its
then-current signature; the other changed that signature and updated
every call site that existed on its own branch. Textually disjoint,
semantically conflicting, so git merged them without a conflict and both
PRs were green against their own bases.

The `ci` run on `main` for that merge commit was a failure, correctly,
immediately. It sat red for hours because a red `main` is only visible
to someone who goes and looks at it, and nobody had a reason to.

This is the same shape orphan-branch-watch already covers for a
different question: something is wrong, something already knows, and
nothing tells anyone.

# Why it covers every repository rather than this one

A red main is silent everywhere, not only here. This repository
coordinates eighteen others, most of which are regenerated rather than
edited by hand, so a red main there is even less likely to be noticed by
someone happening to look.

Issues are opened in THIS repository rather than in the offending one,
because a workflow's own GITHUB_TOKEN is scoped to the repository it
runs in. Reading another repository's runs needs no special access while
it is public; writing to it would need a token this org has not issued,
and inventing one is a bigger decision than a watch script should make.

# What counts as red

Only the workflow named by --workflow, defaulting to `ci`, and only its
most recent completed run on main.

Deliberately not every workflow. Several of this org's scheduled watches
signal by exiting non-zero on purpose, orphan-branch-watch among them, so
counting every failing workflow would report a watch doing its job as a
broken main. A repository with no such workflow is skipped rather than
reported: absence of a gate is a different problem from a failing one.
"""

import argparse
import json
import subprocess
import sys


def gh_api(path):
    """One authenticated GitHub API call, returning parsed JSON or None.

    None rather than an exception for a call that fails: a repository
    that has been archived, renamed or made private mid-run should not
    take the whole sweep down with it, and the alternative to a partial
    answer here is no answer at all.
    """
    try:
        out = subprocess.run(
            ["gh", "api", path],
            capture_output=True, text=True, check=True, timeout=60,
        ).stdout
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        return None
    try:
        return json.loads(out)
    except json.JSONDecodeError:
        return None


def active_repos(org):
    """Every non-archived repository in the org, by name."""
    data = gh_api(f"orgs/{org}/repos?per_page=100&type=all")
    if data is None:
        return []
    return sorted(r["name"] for r in data if not r.get("archived"))


def latest_main_run(org, repo, workflow):
    """The most recent COMPLETED run of `workflow` on main, or None.

    Completed only: a run still in progress is not a verdict, and
    reporting one would make the watch flap against whatever merged
    most recently.
    """
    data = gh_api(
        f"repos/{org}/{repo}/actions/runs"
        f"?branch=main&status=completed&per_page=20"
    )
    if data is None:
        return None
    for run in data.get("workflow_runs", []):
        if run.get("name") == workflow:
            return run
    return None


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--org", default="Ubiquex")
    ap.add_argument(
        "--workflow", default="ci",
        help="workflow name that gates main (default: ci)",
    )
    ap.add_argument(
        "--repo", action="append", default=None,
        help="check only these repositories (repeatable); default is every active one",
    )
    args = ap.parse_args()

    repos = args.repo if args.repo else active_repos(args.org)
    if not repos:
        print("could not list repositories", file=sys.stderr)
        return 2

    red, checked, skipped = [], 0, []
    for repo in repos:
        run = latest_main_run(args.org, repo, args.workflow)
        if run is None:
            skipped.append(repo)
            continue
        checked += 1
        if run.get("conclusion") != "success":
            red.append({
                "repo": repo,
                "conclusion": run.get("conclusion"),
                "sha": (run.get("head_sha") or "")[:7],
                "title": (run.get("display_title") or "").strip(),
                "url": run.get("html_url", ""),
                "when": run.get("updated_at", ""),
            })

    print(f"checked {checked} repo(s) for a failing `{args.workflow}` on main")
    if skipped:
        # Named rather than silent: a repository with no gating workflow
        # is not covered by this watch, and a reader counting repositories
        # should be able to see which ones those are.
        print(f"skipped {len(skipped)} with no `{args.workflow}` workflow: {', '.join(skipped)}")

    if not red:
        print("every main is green")
        return 0

    print()
    print(f"{len(red)} repository/repositories have a red main:")
    for r in red:
        print()
        print(f"  {r['repo']}: {r['conclusion']} at {r['sha']}")
        if r["title"]:
            print(f"    {r['title']}")
        print(f"    {r['url']}")
    return 1


if __name__ == "__main__":
    sys.exit(main())
