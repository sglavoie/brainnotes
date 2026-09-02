#!/usr/bin/env python3
"""Duplication: audit §2.5.

A normalized sliding-window hash, standing in for `dupl`/`jscpd`/PMD-CPD, none
of which are installed. Exact hashing cannot see renamed clones, so every
number here is a floor. The definition below is pinned so the figure is
comparable session to session even though it is not comparable to the audit's
prose description of the same idea — see docs/internals/refactor-baseline.md.
"""

from __future__ import annotations

import collections
import hashlib
import itertools

from metrics_corpus import BLOCK_COMMENT, LINE_COMMENT, Corpus, section

WINDOW = 12
MIN_DISTINCT = 6


def normalize(text: str) -> list[str]:
    """Strip comments and blank lines, collapse whitespace."""
    out = []
    for line in BLOCK_COMMENT.sub("", text).splitlines():
        line = " ".join(LINE_COMMENT.sub("", line).split())
        if line:
            out.append(line)
    return out


def duplication(c: Corpus, group: list[str]) -> tuple[int, collections.Counter]:
    """Return (cluster count, per-file-pair shared-window counts).

    A cluster is one distinct normalized 12-line window appearing in >=2 files
    of the group. Windows with fewer than 6 distinct lines are discarded.
    """
    occurrences: dict[bytes, set[str]] = collections.defaultdict(set)
    for f in group:
        lines = normalize(c.text(f))
        for i in range(len(lines) - WINDOW + 1):
            window = lines[i : i + WINDOW]
            if len(set(window)) < MIN_DISTINCT:
                continue
            digest = hashlib.blake2b("\n".join(window).encode(), digest_size=16).digest()
            occurrences[digest].add(f)
    clusters = {h: v for h, v in occurrences.items() if len(v) > 1}
    pairs: collections.Counter = collections.Counter()
    for files in clusters.values():
        for a, b in itertools.combinations(sorted(files), 2):
            pairs[(a, b)] += 1
    return len(clusters), pairs


def report_duplication(c: Corpus) -> None:
    section("§2.5 Duplication (normalized 12-line window hash)")
    print(
        "  comments+blanks stripped, whitespace collapsed, "
        "windows with <6 distinct lines discarded"
    )
    print("  cluster = one distinct normalized window appearing in >=2 REAL files")
    for label, group in (
        ("Go source", c.go_source),
        ("Go tests", c.go_test),
        ("TypeScript source", c.ts_source),
    ):
        count, pairs = duplication(c, group)
        print(f"\n  {label}: {count} clusters")
        for (a, b), n in pairs.most_common(6):
            print(f"    {n:3d} windows  {a} <-> {b}")
