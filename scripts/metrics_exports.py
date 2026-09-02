#!/usr/bin/env python3
"""Dead code and over-export: audit §2.6.

`deadcode` is authoritative for Go reachability. The export scans are regex
approximations — `knip` is not installed — so the TypeScript figure in
particular has error bars both ways: dynamic access counts as unused, and
re-export chains count as used.
"""

from __future__ import annotations

import collections
import os
import re
import shutil
import subprocess

from metrics_corpus import Corpus, section

GO_FUNC = re.compile(r"^func\s+([A-Z]\w*)\s*[(\[]", re.M)
GO_METHOD = re.compile(r"^func\s+\([^)]*\)\s*([A-Z]\w*)\s*[(\[]", re.M)
GO_TYPE = re.compile(r"^type\s+([A-Z]\w*)\b", re.M)
GO_TYPE_BLOCK = re.compile(r"^type\s*\(\n(.*?)^\)", re.M | re.S)
GO_TYPE_INNER = re.compile(r"^\t([A-Z]\w*)\b", re.M)

TS_EXPORT_VALUE = re.compile(
    r"^export\s+(?:default\s+)?(?:async\s+)?"
    r"(?:function\*?|const|let|var|class|enum)\s+(\w+)",
    re.M,
)
TS_EXPORT_TYPE = re.compile(r"^export\s+(?:type|interface)\s+(\w+)", re.M)


def report_deadcode(c: Corpus) -> None:
    section("§2.6 Dead code")
    if shutil.which("deadcode") is None:
        print("  deadcode unavailable — skipped")
        return
    for label, argv in (
        ("unreachable from binary entrypoints", ["deadcode", "./..."]),
        ("unreachable including tests", ["deadcode", "-test", "./..."]),
    ):
        proc = subprocess.run(argv, cwd=c.root, capture_output=True, text=True)
        lines = [l for l in proc.stdout.splitlines() if l.strip()]
        print(f"  {len(lines):4d}  {label}")
        if "-test" in argv:
            counter = collections.Counter(
                "/".join(l.split(":", 1)[0].split("/")[:2]) for l in lines
            )
            for pkg, n in counter.most_common():
                print(f"        {n:3d}  {pkg}")


def report_go_exports(c: Corpus) -> None:
    section("§2.6 Exported-but-package-local (Go)")
    owners: dict[str, set[str]] = collections.defaultdict(set)
    for f in c.go_source:
        text = c.text(f)
        names = (
            set(GO_FUNC.findall(text))
            | set(GO_METHOD.findall(text))
            | set(GO_TYPE.findall(text))
        )
        for block in GO_TYPE_BLOCK.findall(text):
            names |= set(GO_TYPE_INNER.findall(block))
        owners[os.path.dirname(f)] |= names

    go_files = c.by_suffix(".go")
    total = sum(len(v) for v in owners.values())
    local: collections.Counter = collections.Counter()
    for directory, names in owners.items():
        blob = "\n".join(c.text(f) for f in go_files if os.path.dirname(f) != directory)
        for name in names:
            if not re.search(r"\.%s\b" % re.escape(name), blob):
                local[directory] += 1
    unreferenced = sum(local.values())
    pct = 100.0 * unreferenced / total if total else 0.0
    print(
        f"  {unreferenced} of {total} exported non-test funcs/types "
        f"({pct:.0f}%) are never referenced from another package"
    )
    for directory, n in local.most_common(8):
        print(f"    {n:4d}  {directory}")


def report_ts_exports(c: Corpus) -> None:
    section("§2.6 Exported-but-file-local (TypeScript)")
    ts_files = c.by_suffix(".ts")
    values = {(f, n) for f in ts_files for n in TS_EXPORT_VALUE.findall(c.text(f))}
    types = {(f, n) for f in ts_files for n in TS_EXPORT_TYPE.findall(c.text(f))}
    unique = len({n for _, n in values}) + len({n for _, n in types})
    print(
        f"  {unique} unique exported symbol names "
        f"({len(values) + len(types)} declarations)"
    )
    per_file: collections.Counter = collections.Counter()
    dead = {"values": 0, "types": 0}
    for kind, pairs in (("values", values), ("types", types)):
        for f, name in pairs:
            blob = "\n".join(c.text(o) for o in ts_files if o != f)
            if not re.search(r"\b%s\b" % re.escape(name), blob):
                per_file[f] += 1
                dead[kind] += 1
    print(
        f"  {dead['values'] + dead['types']} referenced nowhere outside their "
        f"defining file ({dead['values']} values + {dead['types']} types)"
    )
    for f, n in per_file.most_common(4):
        print(f"    {n:4d}  {os.path.basename(f)}")
