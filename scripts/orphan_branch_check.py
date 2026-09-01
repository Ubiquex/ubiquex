#!/usr/bin/env python3
"""Finds branches whose work is real and complete but was never opened
to main -- the gap named directly by the UBI-187 GCP beta/alpha
incident: eight batches (docs/gcp-beta-alpha-batch1 through batch8)
were stacked and fully merged among themselves, batch1 landed on
main, and batches 2-8 then sat complete, valid, and invisible for two
days because nothing ever opened a PR from the stack's own tip to
main. hash-watch/coverage-watch both watch for content the corpus is
MISSING; this watches for content that already exists on a branch and
nobody ever asked to land.

Definition of "orphan tip", checked per branch (excluding `main` and
any `--exclude-prefix`):
  1. NOT already an ancestor of main (nothing to flag -- it landed).
  2. NOT an ancestor of any OTHER branch (if something else is built
     on top of it, it's an interior stacking node someone is still
     actively extending, not a dead end -- e.g. batch2 through batch7
     in the real incident, each of which IS an ancestor of the next
     batch, correctly excluded; only batch8, nothing-builds-on-it,
     would have been flagged).
  3. NOT the head of any OPEN pull request, to any base (an open PR
     against a non-main base is still "on someone's radar" -- this
     check is about branches nobody is tracking at all, not about
     second-guessing a deliberate stacking-order decision).
  4. Its most recent commit is older than --min-age-days (default 2 --
     the real incident's own branches sat exactly 2 days before this
     check would have existed to catch it; a same-day branch is very
     plausibly still being actively built, not yet abandoned).

For every flagged branch that DOES have a merged PR against it (just
not one that ever reached main -- e.g. a stacked PR whose base was
itself abandoned rather than separately landed), the report checks
whether that PR's own recorded merge commit is reachable from main:
reachable means an ordinary squash-merge shape (the branch tip itself
isn't a literal ancestor, but its content really did land) and is
reported as likely benign; NOT reachable means GitHub calls the PR
merged but not even that merge commit ever reached main -- the exact
shape a base-retarget gap leaves behind, and a real, confirmed-live
finding this check surfaced on its first real run (two PRs, genuinely
substantial content -- tens of thousands of description entries --
that GitHub still shows as MERGED today, absent from main).

Requires `git` and `gh` (authenticated) on PATH, run from within a
real clone of the target repo.

Usage:
  python3 orphan_branch_check.py [--repo Ubiquex/ubiquex-docs]
      [--exclude-prefix dependabot/ --exclude-prefix renovate/]
      [--min-age-days 2] [--json out.json]

Exit codes:
  0 -- zero orphan tips found
  1 -- at least one orphan tip found
  2 -- a real usage/setup error (git/gh not usable)
"""
import argparse
import json
import subprocess
import sys
from datetime import datetime, timezone


def run(cmd):
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise RuntimeError(f"command failed: {' '.join(cmd)}\n{result.stderr}")
    return result.stdout


def list_remote_branches():
    out = run(["git", "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin"])
    branches = []
    for line in out.splitlines():
        line = line.strip()
        if not line or line == "origin" or line == "origin/HEAD" or line.endswith("/HEAD"):
            continue
        name = line[len("origin/"):] if line.startswith("origin/") else line
        if name:
            branches.append(name)
    return branches


def is_ancestor(candidate_ref, of_ref):
    result = subprocess.run(
        ["git", "merge-base", "--is-ancestor", candidate_ref, of_ref],
        capture_output=True,
    )
    return result.returncode == 0


def last_commit_age_days(ref):
    out = run(["git", "log", "-1", "--format=%ct", ref]).strip()
    commit_ts = int(out)
    now = datetime.now(timezone.utc).timestamp()
    return (now - commit_ts) / 86400.0


def list_open_pr_heads(repo):
    out = run([
        "gh", "pr", "list", "--repo", repo, "--state", "open",
        "--json", "headRefName", "--limit", "500",
    ])
    return {p["headRefName"] for p in json.loads(out)}


def list_merged_prs(repo):
    """{headRefName: [{number, baseRefName, mergeCommit}]} -- an orphan
    branch can have one or more MERGED prs against it and still never
    have reached main, when the merge commit's own base branch was
    itself later abandoned rather than ever separately landed on main
    (the real, second failure mode this check found live: PRs #11/#12
    here, both showing MERGED on GitHub, whose real content is
    confirmed absent from main -- not a hypothetical, an actual
    UBI-187-follow-up finding). Distinct from "no PR ever opened" --
    the remediation differs (re-open/re-target vs. investigate why an
    already-'merged' PR's content never landed) so the report
    distinguishes them rather than collapsing both into one label."""
    out = run([
        "gh", "pr", "list", "--repo", repo, "--state", "merged",
        "--json", "headRefName,number,baseRefName,mergeCommit", "--limit", "500",
    ])
    by_head = {}
    for p in json.loads(out):
        by_head.setdefault(p["headRefName"], []).append(p)
    return by_head


def merge_commit_reaches_main(pr):
    """True if a MERGED PR's own recorded merge commit is itself
    reachable from main -- true for an ordinary squash/merge commit
    whose branch tip just isn't a literal git ancestor of main (a
    real, common, harmless case: the branch's own commit history and
    main's are related by content, not by a direct parent edge).
    False is the genuinely suspicious case: GitHub calls the PR
    merged, but not even its own merge commit ever reached main --
    this is the shape a base-retarget gap leaves behind (this
    project's own hash-watch/data-source-artifacts arcs have hit this
    exact class of bug before)."""
    sha = (pr.get("mergeCommit") or {}).get("oid")
    if not sha:
        return None  # unknown -- no merge commit recorded at all
    try:
        run(["git", "cat-file", "-e", sha])
    except RuntimeError:
        return None  # not fetched locally -- can't tell, don't guess
    return is_ancestor(sha, "origin/main")


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--repo", required=True, help='"Owner/repo", e.g. Ubiquex/ubiquex-docs')
    p.add_argument("--exclude-prefix", action="append", default=[], help="branch name prefix to skip (repeatable)")
    p.add_argument("--min-age-days", type=float, default=2.0)
    p.add_argument("--json", help="also write the full, machine-readable report to this path")
    args = p.parse_args()

    try:
        run(["git", "fetch", "origin", "--quiet"])
        branches = list_remote_branches()
        open_pr_heads = list_open_pr_heads(args.repo)
        merged_prs = list_merged_prs(args.repo)
    except RuntimeError as e:
        print(f"setup error: {e}", file=sys.stderr)
        sys.exit(2)

    candidates = [
        b for b in branches
        if b != "main" and not any(b.startswith(pfx) for pfx in args.exclude_prefix)
    ]
    refs = {b: f"origin/{b}" for b in candidates}

    orphans = []
    for b in candidates:
        ref = refs[b]
        if is_ancestor(ref, "origin/main"):
            continue  # already landed
        if any(other != b and is_ancestor(ref, refs[other]) for other in candidates):
            continue  # something else is built on top of this -- not a dead end
        if b in open_pr_heads:
            continue  # already on someone's radar
        age = last_commit_age_days(ref)
        if age < args.min_age_days:
            continue  # plausibly still being actively built
        merged = merged_prs.get(b, [])
        for pr in merged:
            pr["merge_commit_reaches_main"] = merge_commit_reaches_main(pr)
        orphans.append({
            "branch": b,
            "age_days": round(age, 1),
            "merged_prs": merged,
        })

    orphans.sort(key=lambda o: -o["age_days"])

    print(f"{len(candidates)} branches checked, {len(orphans)} orphan tip(s) found "
          f"(complete work not reachable from main, no open PR, older than {args.min_age_days} day(s))")
    for o in orphans:
        suspicious = [p for p in o["merged_prs"] if p["merge_commit_reaches_main"] is False]
        benign = [p for p in o["merged_prs"] if p["merge_commit_reaches_main"] is True]
        if suspicious:
            pr_nums = ", ".join(f"#{p['number']}->{p['baseRefName']}" for p in suspicious)
            print(f"  - {o['branch']} ({o['age_days']} days) -- SUSPICIOUS: shows merged ({pr_nums}) but not even that merge commit reaches main -- likely real lost content, investigate directly, do not just re-open a PR")
        elif benign:
            pr_nums = ", ".join(f"#{p['number']}->{p['baseRefName']}" for p in benign)
            print(f"  - {o['branch']} ({o['age_days']} days) -- likely benign: merged ({pr_nums}), merge commit IS on main, branch tip itself just isn't a direct ancestor (ordinary squash-merge shape)")
        else:
            print(f"  - {o['branch']} ({o['age_days']} days) -- no PR to main ever opened")

    if args.json:
        with open(args.json, "w") as f:
            json.dump({"checked": len(candidates), "orphans": orphans}, f, indent=2)
            f.write("\n")
        print(f"\nfull report written to {args.json}")

    sys.exit(1 if orphans else 0)


if __name__ == "__main__":
    main()
