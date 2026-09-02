#!/usr/bin/env python3
"""Reproduce the refactoring-audit measurements (§1, §2.1-§2.8).

Pure measurement. This script writes nothing to the repository and nothing
outside stdout; the single file it creates is an ephemeral golangci-lint config
inside a `tempfile.TemporaryDirectory()` (see metrics_complexity.py for why).

The denominator is `git ls-files` minus everything matched by `.refactorignore`
at the repo root. `.refactorignore` is the source of record for that exclusion
set — nothing here hard-codes it.

Run via `just metrics`. It is deliberately not part of `just ci` — it is a
report, not a gate. Compare its output against
docs/internals/refactor-baseline.md.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from metrics_complexity import report_go_complexity, report_ts_complexity
from metrics_corpus import IGNORE_FILE, Corpus
from metrics_duplication import report_duplication
from metrics_exports import report_deadcode, report_go_exports, report_ts_exports
from metrics_size import (
    report_churn,
    report_debt,
    report_denominator,
    report_file_size,
    report_package_size,
    report_test_ratio,
)

SECTIONS = {
    "denominator": report_denominator,
    "size": report_file_size,
    "packages": report_package_size,
    "churn": report_churn,
    "complexity-go": report_go_complexity,
    "complexity-ts": report_ts_complexity,
    "duplication": report_duplication,
    "deadcode": report_deadcode,
    "exports-go": report_go_exports,
    "exports-ts": report_ts_exports,
    "test-ratio": report_test_ratio,
    "debt": report_debt,
}


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Report the refactoring measurements (audit §1, §2.1-§2.8)."
    )
    parser.add_argument(
        "--section",
        action="append",
        choices=sorted(SECTIONS),
        help="run only the named section (repeatable); default is all",
    )
    args = parser.parse_args()

    root = Path(__file__).resolve().parent.parent
    corpus = Corpus(root)

    head = corpus.git("rev-parse", "--short", "HEAD").strip()
    print(f"bn refactoring metrics — HEAD {head}")
    print(f"Denominator: git ls-files minus {IGNORE_FILE}")

    for name in args.section or SECTIONS:
        SECTIONS[name](corpus)
    print()
    return 0


if __name__ == "__main__":
    sys.exit(main())
