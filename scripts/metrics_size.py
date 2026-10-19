#!/usr/bin/env python3
"""Size-shaped measurements: audit §1, §2.1, §2.2, §2.3, §2.7, §2.8."""

from __future__ import annotations

import collections
import re

from metrics_corpus import IGNORE_FILE, LANGUAGES, Corpus, is_test, section

DEBT = re.compile(r"\b(TODO|FIXME|HACK|XXX)\b")


def report_denominator(c: Corpus) -> None:
    section("§1 Denominator")
    print(f"Tracked  {len(c.tracked):5d} files")
    print(f"REAL text {len(c.files):5d} files   {sum(c.loc.values()):7,d} LOC")
    print(f"Assets   {len(c.assets):5d} binary files (excluded from LOC and text scans)")
    print(f"Excluded {len(c.excluded):5d} files   (matched by {IGNORE_FILE})")
    print()
    print(f"{'Language':<12}{'Files':>7}{'LOC':>10}{'Source':>10}{'Test':>10}")
    claimed = set()
    for name, suffixes in LANGUAGES:
        files = [f for f in c.files if f.endswith(suffixes)]
        claimed.update(files)
        src = sum(c.loc[f] for f in files if not is_test(f))
        tst = sum(c.loc[f] for f in files if is_test(f))
        print(f"{name:<12}{len(files):>7}{src + tst:>10,}{src:>10,}{tst:>10,}")
    other = [f for f in c.files if f not in claimed]
    print(f"{'Other':<12}{len(other):>7}{sum(c.loc[f] for f in other):>10,}")


def report_file_size(c: Corpus) -> None:
    section("§2.1 File size")
    sizes = sorted(((n, f) for f, n in c.loc.items()), reverse=True)
    for threshold in (500, 400, 300, 200):
        print(f"  >{threshold} LOC: {sum(1 for n, _ in sizes if n > threshold):4d} files")
    print("\n  Largest 10:")
    for n, f in sizes[:10]:
        print(f"  {n:5d}  {f}")


def report_package_size(c: Corpus) -> None:
    section("§2.2 Package size (top 10 directories, non-recursive)")
    print(f"  {'Total':>8}{'Files':>7}{'Source':>8}{'Test':>8}  Directory")
    ranked = sorted(c.directories().items(), key=lambda kv: -kv[1][0])
    for name, (total, files, src, tst) in ranked[:10]:
        print(f"  {total:>8,}{files:>7}{src:>8,}{tst:>8,}  {name}")


def report_churn(c: Corpus) -> None:
    section("§2.3 Churn x size (top 15)")
    commits: collections.Counter[str] = collections.Counter()
    for line in c.git("log", "--format=%H", "--name-only").splitlines():
        line = line.strip()
        if line and len(line) != 40:
            commits[line] += 1
    rows = [
        (commits[f] * c.loc[f], commits[f], c.loc[f], f) for f in c.files if commits[f]
    ]
    for score, n, loc, f in sorted(rows, reverse=True)[:15]:
        print(f"  {score:6d} commits={n:<3d} loc={loc:<4d} {f}")


def report_test_ratio(c: Corpus) -> None:
    section("§2.7 Test ratio")
    print(f"  {'':<12}{'Test LOC':>10}{'Source LOC':>12}{'Ratio':>8}")
    all_test = all_source = 0
    for label, test_files, source_files in (
        ("Go", c.go_test, c.go_source),
        ("TypeScript", c.ts_test, c.ts_source),
    ):
        t = sum(c.loc[f] for f in test_files)
        s = sum(c.loc[f] for f in source_files)
        all_test += t
        all_source += s
        print(f"  {label:<12}{t:>10,}{s:>12,}{t / s:>8.2f}")
    print(
        f"  {'Combined':<12}{all_test:>10,}{all_source:>12,}"
        f"{all_test / all_source:>8.2f}"
    )
    total = sum(c.loc.values())
    print(f"\n  Test code is {100.0 * all_test / total:.0f}% of REAL LOC")
    print("\n  Largest test files:")
    for n, f in sorted(((c.loc[f], f) for f in c.go_test + c.ts_test), reverse=True)[:6]:
        print(f"  {n:5d}  {f}")


def report_debt(c: Corpus) -> None:
    section("§2.8 Debt markers")
    hits = []
    for f in c.files:
        for number, line in enumerate(c.text(f).splitlines(), 1):
            if DEBT.search(line):
                hits.append((f, number, line.strip()))
    print(f"  {len(hits)} matches for TODO|FIXME|HACK|XXX")
    for f, number, line in hits:
        print(f"    {f}:{number}: {line[:88]}")
