#!/usr/bin/env python3
"""Fail when a provider's snapshot is stranded between here and its repo.

A dynamic provider's schema travels through three places, and each one
can move without the others:

  1. the schema repo's committed manifest.json  (what a merged PR set)
  2. its latest GitHub Release                  (what a consumer can fetch)
  3. this repo's [dynamic_providers.<p>] pin    (what ubx actually resolves)

Nothing watched any gap between them. On 2026-09-07, checked by hand
during UBI-241, five of the eight were stranded:

    github        released v1.0.1   committed 1.1.0   pin 1.0.1
    datadog       released v1.0.1   committed 2.0.1   pin 1.0.1
    azure         released v2.0.0   committed 2.0.1   pin 2.0.0
    google        released v1.0.1   committed 2.1.0   pin 1.0.1
    aws           released v1.0.0   committed 2.0.1   pin 1.0.0

Snapshot regeneration PRs were being merged, which moves (1), but
publish.yml is manual dispatch and was never run afterwards, so (2)
stayed put and (3) correctly tracked (2). Three of those were a full
major version unpublished, meaning members had been removed. The content
sat on main for over a week where no consumer could reach it, and
nothing anywhere said so.

TWO CONDITIONS, BOTH FAILURES, because they are different mistakes:

  MERGED BUT NOT PUBLISHED (committed > released). Someone merged a
  regeneration and did not dispatch publish.yml. The work exists and is
  unreachable.

  PUBLISHED BUT NOT ADOPTED (released > pin). Someone published a
  snapshot and did not move the pin here. ubx keeps resolving the older
  one, so the release is real and inert.

A pin AHEAD of the release is also a failure, and a louder one: it names
something that cannot be fetched at all.
"""

from __future__ import annotations

import json
import os
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path

CONFIG = Path(__file__).resolve().parent / ".ubx" / "config"
ORG = "Ubiquex"


def api(url: str):
    req = urllib.request.Request(url, headers={"Accept": "application/vnd.github+json"})
    if tok := os.environ.get("GITHUB_TOKEN"):
        req.add_header("Authorization", f"Bearer {tok}")
    return json.load(urllib.request.urlopen(req, timeout=30))


def semver(v: str) -> tuple[int, ...]:
    return tuple(int(p) for p in re.findall(r"\d+", v.lstrip("v")))


def pins() -> dict[str, str]:
    """Every [dynamic_providers.<name>] version = "..." in the config."""
    text = CONFIG.read_text()
    out: dict[str, str] = {}
    for m in re.finditer(r"^\[dynamic_providers\.([a-z0-9_]+)\]\s*$", text, re.M):
        name = m.group(1)
        rest = text[m.end() :]
        nxt = re.search(r"^\[", rest, re.M)
        block = rest[: nxt.start()] if nxt else rest
        if v := re.search(r'^version\s*=\s*"([^"]+)"', block, re.M):
            out[name] = v.group(1)
    return out


def main() -> int:
    found = pins()
    if not found:
        # Never pass by finding nothing: a config that parsed to zero
        # providers means the format moved, not that all is well.
        print(f"no [dynamic_providers.*] pins found in {CONFIG}", file=sys.stderr)
        return 1

    problems: list[str] = []
    rows: list[str] = []
    for name, pin in sorted(found.items()):
        repo = f"{ORG}/ubx-schema-{name}"
        try:
            manifest = api(f"https://api.github.com/repos/{repo}/contents/manifest.json")
            import base64

            committed = json.loads(base64.b64decode(manifest["content"]))["version"]
        except Exception as err:  # noqa: BLE001
            print(f"  {name:<14} could not read manifest.json ({err})", file=sys.stderr)
            continue
        try:
            released = api(f"https://api.github.com/repos/{repo}/releases/latest")["tag_name"]
        except urllib.error.HTTPError as err:
            if err.code == 404:
                released = None
            else:
                print(f"  {name:<14} could not read releases ({err})", file=sys.stderr)
                continue

        rel_s = released or "none"
        rows.append(f"  {name:<14} released={rel_s:<10} committed={committed:<10} pin={pin}")

        if released is None:
            problems.append(f"{name}: committed {committed} but nothing is released at all")
            continue
        if semver(committed) > semver(released):
            problems.append(
                f"{name}: committed {committed} is newer than released {released} "
                f"-- merged but never published, so no consumer can fetch it"
            )
        if semver(released) > semver(pin):
            problems.append(
                f"{name}: released {released} is newer than the pin {pin} "
                f"-- published but not adopted, so ubx still resolves the older one"
            )
        if semver(pin) > semver(released):
            problems.append(
                f"{name}: pin {pin} is newer than released {released} "
                f"-- the pin names something that cannot be fetched"
            )

    print("\n".join(rows))
    if not problems:
        print(f"\nok: all {len(found)} provider pins agree with what is committed and released")
        return 0

    print(f"\n{len(problems)} provider(s) stranded:", file=sys.stderr)
    for p in problems:
        print(f"  {p}", file=sys.stderr)
    print(
        "\nMerging a snapshot regeneration is not publishing it: dispatch the schema\n"
        "repo's publish.yml, then move the pin here. Both steps, every time.",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
