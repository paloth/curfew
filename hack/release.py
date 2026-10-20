#!/usr/bin/env python3
"""Compute the next semantic version and the release notes from conventional commits.

Reads the commits since the latest v* tag. Prints GitHub Actions outputs
(release=true|false, version, tag) on stdout and writes the notes to --notes.
Breaking change -> major, feat -> minor, fix -> patch; any other type alone
does not make a release. Used by .github/workflows/release.yml. Standard library only.
"""

from __future__ import annotations  # "X | None" hints on Python 3.9, the macOS system python3

import argparse
import os
import re
import subprocess
import sys
from dataclasses import dataclass
from enum import IntEnum


class Kind(IntEnum):
    # Ordered by impact: the highest kind among the commits decides the bump.
    CHORE = 0
    FIX = 1
    FEATURE = 2
    BREAKING = 3


SECTIONS = [
    (Kind.BREAKING, "Breaking changes"),
    (Kind.FEATURE, "Features"),
    (Kind.FIX, "Fixes"),
    (Kind.CHORE, "Chores"),
]

HEADER = re.compile(r"^(\w+)(?:\(([^)]*)\))?(!)?: (.+)$")
BREAKING_NOTE = re.compile(r"^BREAKING[ -]CHANGE: ?", re.MULTILINE)
TAG = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")
FIRST = (1, 0, 0)


@dataclass(frozen=True)
class Entry:
    kind: Kind
    desc: str
    hash: str = ""
    scope: str = ""
    note: str = ""


def classify(hash_: str, subject: str, body: str) -> Entry:
    """Read a conventional commit; anything that does not follow the format is a chore.

    Chores keep their whole subject, so the notes still show build:, ci:, docs: and so on.
    """
    m = HEADER.match(subject)
    if not m:
        return Entry(Kind.CHORE, subject, hash_)
    type_, scope, bang, desc = m.groups()
    note = ""
    if found := BREAKING_NOTE.search(body):
        # The note runs until the next blank line.
        note = " ".join(body[found.end():].split("\n\n", 1)[0].split())
    if bang or note:
        kind = Kind.BREAKING
    elif type_ == "feat":
        kind = Kind.FEATURE
    elif type_ == "fix":
        kind = Kind.FIX
    else:
        return Entry(Kind.CHORE, subject, hash_)
    return Entry(kind, desc, hash_, scope or "", note)


def next_version(prev: tuple[int, int, int] | None, entries: list[Entry]) -> tuple[int, int, int] | None:
    """Bump prev for the highest kind in entries; None when nothing warrants a release.

    prev is None before the first release, which is always FIRST.
    """
    top = max((e.kind for e in entries), default=Kind.CHORE)
    if top == Kind.CHORE:
        return None
    if prev is None:
        return FIRST
    major, minor, patch = prev
    if top == Kind.BREAKING:
        return (major + 1, 0, 0)
    if top == Kind.FEATURE:
        return (major, minor + 1, 0)
    return (major, minor, patch + 1)


def render(entries: list[Entry], compare: str = "") -> str:
    """Build the release notes; compare is the "full changelog" link, empty for the first release."""
    out = []
    for kind, title in SECTIONS:
        lines = []
        for e in entries:
            if e.kind != kind:
                continue
            line = "- " + (f"**{e.scope}:** " if e.scope else "") + f"{e.desc} ({e.hash})"
            if e.note:
                line += f"\n  {e.note}"
            lines.append(line)
        if lines:
            out.append(f"## {title}\n\n" + "\n".join(lines) + "\n\n")
    if compare:
        out.append(f"**Full changelog:** {compare}\n")
    return "".join(out)


def git(*args: str) -> str:
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout.strip()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--notes", default="release-notes.md", help="file to write the release notes to")
    parser.add_argument("--repo", default=os.environ.get("GITHUB_REPOSITORY", ""), help="owner/name, for the compare link")
    args = parser.parse_args()

    tags = git("tag", "--list", "v*", "--sort=-v:refname").splitlines()
    prev_tag = tags[0] if tags else ""
    prev, log_range = None, "HEAD"
    if prev_tag:
        m = TAG.match(prev_tag)
        if not m:
            print(f"release: latest tag {prev_tag!r} is not vMAJOR.MINOR.PATCH", file=sys.stderr)
            return 1
        prev, log_range = tuple(int(n) for n in m.groups()), f"{prev_tag}..HEAD"

    # Fields split by \x1f, commits by \x1e: subjects and bodies can hold anything else.
    log = git("log", "--no-merges", "--reverse", "--format=%h%x1f%s%x1f%b%x1e", log_range)
    entries = []
    for record in log.split("\x1e"):
        fields = record.strip().split("\x1f", 2)
        if len(fields) == 3:
            entries.append(classify(*fields))

    version = next_version(prev, entries)
    if version is None:
        print("release=false")
        return 0
    v = ".".join(map(str, version))
    compare = f"https://github.com/{args.repo}/compare/{prev_tag}...v{v}" if prev_tag and args.repo else ""
    with open(args.notes, "w", encoding="utf-8") as f:
        f.write(render(entries, compare))
    print(f"release=true\nversion={v}\ntag=v{v}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
