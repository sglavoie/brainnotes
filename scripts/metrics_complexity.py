#!/usr/bin/env python3
"""Function complexity: audit §2.4, Go and TypeScript.

Go uses golangci-lint with the audit's thresholds. TypeScript uses the repo's
own `eslint.config.js`, added by S03; the brace-depth top-level-function length
scan that stood in for it before then is kept as a cross-check, because the
pre-ESLint baseline was recorded with it and because the gap between the two is
itself a measurement — a length ranking cannot see a short, widely-branching
function.
"""

from __future__ import annotations

import collections
import json
import os
import re
import shutil
import subprocess
import tempfile
from pathlib import Path

from metrics_corpus import BLOCK_COMMENT, Corpus, section

# --- Go ---------------------------------------------------------------------

# golangci-lint v2 exposes no command-line flags for per-linter thresholds, so
# the settings must come from a config file. Writing an ephemeral one keeps
# this script free of repository writes AND pins the thresholds to the ones the
# baseline was measured with, so a repo-level `.golangci.yml` cannot silently
# move the numbers docs/internals/refactor-baseline.md compares against.
GOLANGCI_CONFIG = """version: "2"
# The same `synctransport` and `syncrecovery` build tags .golangci.yml sets: the
# ratchet judges tagged files, so the recorded floor has to count them too.
# Without this the two would measure different file sets, and the first tagged
# function to cross a threshold would appear in `just lint-full` and be invisible
# to `just metrics` — which is the number the baseline compares against.
run:
  build-tags:
    - synctransport
    - syncrecovery
linters:
  default: none
  enable: [cyclop, gocognit, nestif, funlen]
  settings:
    cyclop:
      max-complexity: 10
    gocognit:
      min-complexity: 20
    nestif:
      min-complexity: 4
    funlen:
      lines: 100
      statements: 50
  exclusions:
    generated: lax
    paths:
      # See .golangci.yml: `eslint` pulls in `flatted`, which vendors Go source
      # under node_modules, and `./...` walks into it. Excluded here as well,
      # because this config is what the recorded baseline is measured with.
      - node_modules/
"""

COGNIT = re.compile(r"cognitive complexity (\d+) of func `?([\w.()*]+)`?")
CYCLO = re.compile(r"complexity for function `?([\w.()*]+)`? is (\d+)")


def run_golangci(root: Path) -> list[dict] | None:
    if shutil.which("golangci-lint") is None:
        return None
    with tempfile.TemporaryDirectory() as tmp:
        config = Path(tmp) / "golangci.yml"
        config.write_text(GOLANGCI_CONFIG, encoding="utf-8")
        proc = subprocess.run(
            [
                "golangci-lint", "run",
                "--config", str(config),
                "--path-mode", "abs",
                "--max-issues-per-linter", "0",
                "--max-same-issues", "0",
                "--issues-exit-code", "0",
                "--show-stats=false",
                "--output.json.path", "stdout",
                "./...",
            ],
            cwd=root,
            capture_output=True,
            text=True,
        )
    for line in proc.stdout.splitlines():
        if line.startswith("{"):
            issues = json.loads(line).get("Issues") or []
            for issue in issues:
                issue["file"] = os.path.relpath(issue["Pos"]["Filename"], root)
            return issues
    return None


def _report_worst(src: list[dict]) -> dict[str, int]:
    cognit: dict[tuple[str, int, str], int] = {}
    cyclo: dict[tuple[str, str], int] = {}
    for issue in src:
        m = COGNIT.search(issue["Text"])
        if m:
            cognit[(issue["file"], issue["Pos"]["Line"], m.group(2))] = int(m.group(1))
        m = CYCLO.search(issue["Text"])
        if m:
            cyclo[(issue["file"], m.group(1))] = int(m.group(2))
    print("\n  Ten worst source functions by cognitive complexity:")
    for (f, line, name), score in sorted(cognit.items(), key=lambda kv: -kv[1])[:10]:
        extra = cyclo.get((f, name))
        print(f"  {score:5d}  {f}:{line}  {name}" + (f" (cyclo {extra})" if extra else ""))
    return {
        name: score
        for (_, _, name), score in cognit.items()
        if re.fullmatch(r"New\w+Cmd", name)
    }


def report_go_complexity(c: Corpus) -> None:
    section("§2.4 Function complexity — Go (golangci-lint)")
    issues = run_golangci(c.root)
    if issues is None:
        print("  golangci-lint unavailable — skipped")
    else:
        src = [i for i in issues if not i["file"].endswith("_test.go")]
        print("  funlen 100/50, cyclop 10, gocognit 20, nestif 4; generated excluded")
        print(f"  {'Linter':<10}{'Total':>7}{'Source':>8}{'Test':>7}")
        for linter in ("cyclop", "gocognit", "nestif", "funlen"):
            total = sum(1 for i in issues if i["FromLinter"] == linter)
            source = sum(1 for i in src if i["FromLinter"] == linter)
            print(f"  {linter:<10}{total:>7}{source:>8}{total - source:>7}")
        print(f"  {'TOTAL':<10}{len(issues):>7}{len(src):>8}{len(issues) - len(src):>7}")

        ctors = _report_worst(src)
        declared = sum(
            len(re.findall(r"^func New\w+Cmd\(", c.text(f), re.M)) for f in c.go_source
        )
        print(f"\n  New*Cmd constructors over gocognit 20: {len(ctors)} of {declared}")
        print(
            "    "
            + ", ".join(
                f"{n} {s}" for n, s in sorted(ctors.items(), key=lambda kv: -kv[1])
            )
        )
        print("\n  Files with most source violations:")
        for f, n in collections.Counter(i["file"] for i in src).most_common(9):
            print(f"  {n:5d}  {f}")

    if shutil.which("gocyclo"):
        proc = subprocess.run(
            # -ignore for the same reason .golangci.yml excludes the path: an npm
            # dependency ships Go source under node_modules and every Go tool
            # here walks the filesystem or `./...` straight into it.
            ["gocyclo", "-over", "10", "-ignore", "node_modules/", "."],
            cwd=c.root,
            capture_output=True,
            text=True,
        )
        n = len([l for l in proc.stdout.splitlines() if l.strip()])
        print(f"\n  gocyclo -over 10 (independent cross-check): {n} functions")
    else:
        print("\n  gocyclo unavailable — cross-check skipped")


# --- TypeScript -------------------------------------------------------------

TS_STRING = re.compile(r"\"(?:\\.|[^\"\\])*\"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`", re.S)
TS_DECL = re.compile(
    r"^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\*?\s+(\w+)"
    r"|^(?:export\s+)?(?:default\s+)?(?:const|let|var)\s+(\w+)\b"
)
TS_FUNC_KW = re.compile(r"^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\b")


def ts_mask(text: str) -> list[str]:
    """Blank out comments and string/template bodies, preserving line structure."""
    text = BLOCK_COMMENT.sub(lambda m: re.sub(r"[^\n]", " ", m.group()), text)
    text = TS_STRING.sub(lambda m: re.sub(r"[^\n]", " ", m.group()), text)
    return [re.sub(r"//.*$", "", line) for line in text.split("\n")]


def ts_body_open(lines: list[str], start: int) -> tuple[int, int] | None:
    """Locate the `{` opening a function body, or None if this is not a function.

    Only a `{` at paren/bracket depth 0 that ends its line counts, and only once
    `function` or `=>` has been seen — so an object literal, a type annotation,
    and a `= {}` default parameter are all skipped. A `;` at depth 0 ends the
    declaration and rules it out.
    """
    depth = 0
    seen = bool(TS_FUNC_KW.match(lines[start]))
    for row in range(start, min(start + 25, len(lines))):
        line = lines[row]
        for col, char in enumerate(line):
            if char in "([":
                depth += 1
            elif char in ")]":
                depth -= 1
            elif char == ">" and col and line[col - 1] == "=":
                seen = True
            elif char == ";" and depth <= 0:
                return None
            elif char == "{" and depth == 0 and seen and not line[col + 1 :].strip():
                return row, col
    return None


def ts_functions(c: Corpus) -> list[tuple[int, str, int, str]]:
    found = []
    for f in c.ts_source:
        lines = ts_mask(c.text(f))
        depth = 0
        for i, line in enumerate(lines):
            match = TS_DECL.match(line) if depth == 0 else None
            if match:
                opened = ts_body_open(lines, i)
                if opened:
                    row, col = opened
                    braces = 0
                    for j in range(row, len(lines)):
                        for char in lines[j][col:] if j == row else lines[j]:
                            braces += (char == "{") - (char == "}")
                        if braces <= 0:
                            found.append(
                                (j - i + 1, f, i + 1, match.group(1) or match.group(2))
                            )
                            break
            depth = max(depth + line.count("{") - line.count("}"), 0)
    found.sort(reverse=True)
    return found


ESLINT_BIN = Path("node_modules") / "eslint" / "bin" / "eslint.js"
TS_ROOT = "internal/web/static_src"
ESLINT_COMPLEXITY = re.compile(r"complexity of (\d+)")


def run_eslint(root: Path) -> list[dict] | None:
    """Real TypeScript complexity, measured through the repo's eslint.config.js.

    Deliberately unlike run_golangci, which writes an ephemeral config of its
    own: the Go thresholds predate `.golangci.yml`, so pinning them here keeps a
    repo config from silently moving the recorded baseline. On the TypeScript
    side `eslint.config.js` is where those thresholds were first written, and a
    second copy here would only be a third place to change and a new way for the
    two to disagree.

    Invoked through `node` and the package's own entry script rather than the
    `.bin` shim, which is a `.cmd` on Windows.
    """
    binary = root / ESLINT_BIN
    if shutil.which("node") is None or not binary.is_file():
        return None
    proc = subprocess.run(
        [
            "node", str(binary),
            "--no-error-on-unmatched-pattern",
            "--format", "json",
            TS_ROOT,
        ],
        cwd=root,
        capture_output=True,
        text=True,
    )
    try:
        return json.loads(proc.stdout)
    except json.JSONDecodeError:
        return None


def eslint_rules(root: Path) -> list[str]:
    """The enabled rule names, resolved by ESLint itself rather than restated.

    A rule that reports nothing is still a measurement — `max-depth` finding
    zero is the finding — so the table has to list the enabled rules, not just
    the ones that fired. Asking ESLint for the resolved config is the only way
    to do that without a second copy of eslint.config.js living here.
    """
    binary = root / ESLINT_BIN
    if shutil.which("node") is None or not binary.is_file():
        return []
    proc = subprocess.run(
        ["node", str(binary), "--print-config", f"{TS_ROOT}/main.ts"],
        cwd=root,
        capture_output=True,
        text=True,
    )
    try:
        rules = json.loads(proc.stdout).get("rules") or {}
    except json.JSONDecodeError:
        return []
    return sorted(
        name
        for name, setting in rules.items()
        if (setting[0] if isinstance(setting, list) else setting) not in (0, "off")
    )


def _eslint_rows(report: list[dict], root: Path) -> list[tuple[str, int, str, str]]:
    """Flatten the ESLint report to (relpath, line, rule, message) rows.

    `*.test.ts` is the repo's test convention (metrics_corpus splits the corpus
    the same way), so a `*.test-helpers.ts` file counts as source here.
    """
    rows = []
    for entry in report:
        rel = os.path.relpath(entry["filePath"], root)
        for message in entry["messages"]:
            rows.append((rel, message["line"], message.get("ruleId") or "(fatal)", message["message"]))
    return rows


def _report_proxy_gap(src: list[tuple[str, int, str, str]], found: list) -> None:
    """How much of the real complexity the length proxy could not see.

    The proxy ranks whole top-level functions by line count. A short function
    with flat, wide branching scores fine by length and badly by complexity, and
    those are exactly the ones a length-ordered decomposition plan would skip.
    """
    spans: dict[str, list[tuple[int, int, int]]] = {}
    for n, f, line, _ in found:
        spans.setdefault(f, []).append((line, line + n - 1, n))
    missed = []
    for rel, line, rule, message in src:
        if rule != "complexity":
            continue
        enclosing = [n for start, end, n in spans.get(rel, []) if start <= line <= end]
        if not enclosing or min(enclosing) < 60:
            missed.append((rel, line, message))
    total = sum(1 for r in src if r[2] == "complexity")
    print("\n  Source `complexity` violations outside any proxy-flagged (>=60 line)")
    print(f"  function: {len(missed)} of {total} — the proxy's blind spot")
    for rel, line, message in sorted(missed)[:10]:
        score = ESLINT_COMPLEXITY.search(message)
        print(f"  {score.group(1) if score else '?':>5}  {os.path.basename(rel)}:{line}")


def report_ts_complexity(c: Corpus) -> None:
    section("§2.4 Function complexity — TypeScript (ESLint)")
    report = run_eslint(c.root)
    found = ts_functions(c)
    if report is None:
        print("  eslint unavailable — skipped (install with `npm ci`)")
    else:
        rows = _eslint_rows(report, c.root)
        src = [r for r in rows if not r[0].endswith(".test.ts")]
        print("  eslint.config.js: complexity 10, max-lines-per-function 100,")
        print("  max-depth 4, no-unused-vars; bundle and globals.d.ts excluded")
        print(f"  {'Rule':<36}{'Total':>7}{'Source':>8}{'Test':>7}")
        for rule in sorted(set(eslint_rules(c.root)) | {r[2] for r in rows}):
            total = sum(1 for r in rows if r[2] == rule)
            source = sum(1 for r in src if r[2] == rule)
            print(f"  {rule:<36}{total:>7}{source:>8}{total - source:>7}")
        print(f"  {'TOTAL':<36}{len(rows):>7}{len(src):>8}{len(rows) - len(src):>7}")

        print("\n  Ten worst source functions by cyclomatic complexity:")
        scored = []
        for rel, line, rule, message in src:
            if rule != "complexity":
                continue
            m = ESLINT_COMPLEXITY.search(message)
            if m:
                scored.append((int(m.group(1)), rel, line))
        for score, rel, line in sorted(scored, reverse=True)[:10]:
            print(f"  {score:5d}  {os.path.basename(rel)}:{line}")

        _report_proxy_gap(src, found)

    print("\n  Brace-depth length proxy (superseded by the table above, kept as")
    print("  the cross-check the pre-ESLint baseline was recorded with):")
    print(f"  Top-level functions: {len(found)}")
    print(f"  >=60 lines: {sum(1 for x in found if x[0] >= 60)}")
    print("\n  Ten longest:")
    for n, f, line, name in found[:10]:
        print(f"  {n:5d}  {os.path.basename(f)}:{line}  {name}")
