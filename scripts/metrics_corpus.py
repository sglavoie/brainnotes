#!/usr/bin/env python3
"""The REAL file set the refactoring metrics are measured over.

`.refactorignore` at the repo root is the source of record for the exclusion
set. Nothing here hard-codes it: a pattern added to that file changes the
denominator, and a wrong denominator makes every metric meaningless.
"""

from __future__ import annotations

import collections
import os
import re
import subprocess
import sys
from pathlib import Path

IGNORE_FILE = ".refactorignore"

BLOCK_COMMENT = re.compile(r"/\*.*?\*/", re.S)
LINE_COMMENT = re.compile(r"//.*$")

LANGUAGES = [
    ("Go", (".go",)),
    ("TypeScript", (".ts",)),
    ("CSS", (".css",)),
    ("Markdown", (".md",)),
    ("Python", (".py",)),
    ("HTML", (".html",)),
]


def translate(pattern: str) -> re.Pattern[str]:
    """Translate the gitignore subset used by `.refactorignore` into a regex.

    Supports `*` (does not cross `/`), `?`, `**` (crosses `/`), and gitignore's
    rule that a pattern containing no `/` matches at any directory depth.
    """
    anchored = "/" in pattern.rstrip("/")
    segments = [s for s in pattern.split("/") if s != ""]
    parts = []
    for index, segment in enumerate(segments):
        last = index == len(segments) - 1
        if segment == "**":
            parts.append(".*" if last else "(?:[^/]+/)*")
            continue
        piece = ""
        for char in segment:
            if char == "*":
                piece += "[^/]*"
            elif char == "?":
                piece += "[^/]"
            else:
                piece += re.escape(char)
        parts.append(piece + ("" if last else "/"))
    body = "".join(parts)
    if not anchored:
        body = "(?:[^/]+/)*" + body
    return re.compile("^" + body + "$")


def load_ignore(root: Path) -> list[re.Pattern[str]]:
    path = root / IGNORE_FILE
    if not path.exists():
        sys.exit(f"missing {IGNORE_FILE} at {root}")
    patterns = []
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if line and not line.startswith("#"):
            patterns.append(translate(line))
    return patterns


def is_test(path: str) -> bool:
    return path.endswith("_test.go") or path.endswith(".test.ts")


def section(title: str) -> None:
    print(f"\n{title}\n{'-' * len(title)}")


class Corpus:
    """Tracked files minus `.refactorignore` matches, plus their line counts."""

    def __init__(self, root: Path) -> None:
        self.root = root
        patterns = load_ignore(root)
        out = subprocess.run(
            ["git", "ls-files", "-z"],
            cwd=root,
            capture_output=True,
            text=True,
            check=True,
        ).stdout
        self.tracked = [f for f in out.split("\0") if f]
        self.excluded = [f for f in self.tracked if any(p.match(f) for p in patterns)]
        dropped = set(self.excluded)
        self.files = [f for f in self.tracked if f not in dropped]
        self.loc = {f: (root / f).read_bytes().count(b"\n") for f in self.files}
        self._text: dict[str, str] = {}

    def text(self, path: str) -> str:
        if path not in self._text:
            self._text[path] = (self.root / path).read_text(
                encoding="utf-8", errors="replace"
            )
        return self._text[path]

    def by_suffix(self, *suffixes: str, exclude: tuple[str, ...] = ()) -> list[str]:
        return [
            f
            for f in self.files
            if f.endswith(suffixes) and not (exclude and f.endswith(exclude))
        ]

    def git(self, *argv: str) -> str:
        return subprocess.run(
            ["git", *argv], cwd=self.root, capture_output=True, text=True, check=True
        ).stdout

    def directories(self) -> dict[str, list[int]]:
        """Per-directory [total, files, source, test] LOC, non-recursive."""
        dirs: dict[str, list[int]] = collections.defaultdict(lambda: [0, 0, 0, 0])
        for f in self.files:
            entry = dirs[os.path.dirname(f) or "."]
            entry[0] += self.loc[f]
            entry[1] += 1
            entry[3 if is_test(f) else 2] += self.loc[f]
        return dirs

    @property
    def go_source(self) -> list[str]:
        return self.by_suffix(".go", exclude=("_test.go",))

    @property
    def go_test(self) -> list[str]:
        return self.by_suffix("_test.go")

    @property
    def ts_source(self) -> list[str]:
        return self.by_suffix(".ts", exclude=(".test.ts",))

    @property
    def ts_test(self) -> list[str]:
        return self.by_suffix(".test.ts")
