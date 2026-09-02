#!/usr/bin/env python3
"""Prove `just lint-phase-gate` fails on a violating non-constructor function.

This is the regression test for the hole the gate's first version left open. It
matched report lines naming a `New*Cmd`/`new*Cmd` function, so it read green
while 21 complexity violations sat in production `internal/cli` code on ordinary
helpers — `runValues`, `applyRelink`, `compile`. The constructors were the
visible half of Phase 1, never the whole of it, and a gate that enforces the
visible half while reporting on the whole is worse than no gate: it certifies
something it never checked. The only way to know it enforces the real criterion
is to make it fail on demand against a function no name match could see.

Everything happens in a throwaway module under the system temporary directory.
Nothing here reads or writes a vault, and the only things borrowed from this
repository are `.golangci.yml` and the `justfile` — copied rather than
re-declared, so the self-test runs the thresholds and the *actual gate recipe*
this repository uses, not a re-implementation of them that could drift.

Four assertions, in order:

  0. BASELINE. With `internal/cli` clean, the gate exits 0 — and golangci-lint
     really does report the two violations that pass is meant to tolerate (one
     outside `internal/cli`, one in an `internal/cli` `_test.go`). That is what
     makes "narrow verdict" a measurement rather than a claim, and what stops
     assertion 3 from being vacuous.
  1. THE FIXTURE IS INVISIBLE TO A NAME MATCH. The old constructor-name pattern
     finds nothing in the report for the violating function. If this ever fails,
     the fixture drifted into constructor shape and assertion 2 stopped testing
     what it says.
  2. THE GATE FAILS. With one violating non-constructor function in production
     `internal/cli` code, the gate exits non-zero and names the file.
  3. THE VERDICT STAYS NARROW. The tolerated violations from assertion 0 are
     still not reported as gate hits, so the gate has not quietly become
     "the whole tree must be clean".

Run it with `just lint-phase-gate-selftest`. Exit 0 is a pass; exit 1 names the
assertion that failed.
"""

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent

# The pattern the gate used before this self-test existed. Kept here as the
# thing assertion 1 proves the fixture escapes, and nowhere else: the gate
# itself no longer matches on names at all.
LEGACY_CONSTRUCTOR_PATTERN = (
    r"^[^[:space:]]+\.go:[0-9]+:[0-9]+: "
    r".*(^|[^a-z0-9_])new[a-z0-9_]*cmd([^a-z0-9_]|$)"
)

# 10 unnested branches score gocognit 21, one over the threshold of 20; 1 is far
# under. The branches are flat so nestif stays at 1 per branch, which keeps the
# fixture's only issue on the function's declaration line and its message on the
# function's name — the shape a name-matching gate is given every chance to see.
BRANCHES_OVER_THRESHOLD = 10
BRANCHES_UNDER_THRESHOLD = 1

# The violating function. Deliberately *not* constructor-shaped: `runProbe` is
# the kind of helper the old gate was blind to.
PROBE_FUNC = "runProbe"
PROBE_FILE = "internal/cli/probe.go"


class SelfTestError(Exception):
    """An assertion this self-test makes about the gate did not hold."""


def fail(message, *details):
    raise SelfTestError("\n  ".join([message, *details]))


def note(message):
    print(f"lint-phase-gate-selftest: {message}")


def flat_branch_body(branches):
    """A function body whose only complexity is `branches` unnested `if`s."""
    lines = ["\ttotal := 0", "\tfor _, x := range xs {"]
    for n in range(branches):
        lines += [f"\t\tif x == {n} {{", "\t\t\ttotal++", "\t\t}"]
    lines += ["\t}", "\treturn total", ""]
    return "\n".join(lines)


def func(name, branches, doc):
    return f"{doc}func {name}(xs []int) int {{\n{flat_branch_body(branches)}}}\n"


def cli_fixture(probe_branches):
    """The production `internal/cli` file, with runProbe's size chosen.

    NewProbeCmd is here as the control the gate must not need: it is
    constructor-shaped and always under every threshold, so a gate that only
    looked at constructors would have nothing to report in this file no matter
    how bad runProbe gets.
    """
    return (
        "package cli\n\n"
        + func(
            "NewProbeCmd",
            BRANCHES_UNDER_THRESHOLD,
            "// NewProbeCmd is constructor-shaped and always clean. The gate must not\n"
            "// need it to be dirty in order to have something to say about this file.\n",
        )
        + "\n"
        + func(
            PROBE_FUNC,
            probe_branches,
            "// runProbe is the function under test: an ordinary helper, named nothing\n"
            "// like a Cobra constructor, in production internal/cli code.\n",
        )
    )


# The two violations the gate is required to tolerate. Neither is ever edited.
TEST_FILE_FIXTURE = "package cli\n\n" + func(
    "probeTestHelper",
    BRANCHES_OVER_THRESHOLD,
    "// probeTestHelper is over gocognit inside an internal/cli _test.go file. The\n"
    "// gate judges production code; a test's table-driven bulk is not its business.\n",
)

OUTSIDE_FIXTURE = "package other\n\n" + func(
    "LegacyViolation",
    BRANCHES_OVER_THRESHOLD,
    "// LegacyViolation is over gocognit outside internal/cli. It stands for the\n"
    "// tree-wide baseline in docs/internals/refactor-baseline.md, which this gate\n"
    "// deliberately does not require to be zero.\n",
)


def run(argv, cwd, env):
    return subprocess.run(
        argv, cwd=cwd, env=env, capture_output=True, text=True, check=False
    )


def go_directive():
    for line in (REPO / "go.mod").read_text(encoding="utf-8").splitlines():
        if line.startswith("go "):
            return line.strip()
    fail("no `go` directive in go.mod, so the sandbox module cannot pin a version")
    return ""


def write_probe(sandbox, probe_branches):
    (sandbox / PROBE_FILE).write_text(cli_fixture(probe_branches), encoding="utf-8")


def create_sandbox(sandbox):
    (sandbox / "internal" / "cli").mkdir(parents=True)
    (sandbox / "internal" / "other").mkdir(parents=True)
    (sandbox / "go.mod").write_text(
        f"module bnphasegateselftest\n\n{go_directive()}\n", encoding="utf-8"
    )
    # Copied rather than re-declared, both of them: a self-test that invents its
    # own thresholds proves nothing about the config in force, and one that
    # re-implements the gate's filter proves nothing about the recipe that runs.
    shutil.copyfile(REPO / ".golangci.yml", sandbox / ".golangci.yml")
    shutil.copyfile(REPO / "justfile", sandbox / "justfile")
    write_probe(sandbox, BRANCHES_UNDER_THRESHOLD)
    (sandbox / "internal" / "cli" / "probe_test.go").write_text(
        TEST_FILE_FIXTURE, encoding="utf-8"
    )
    (sandbox / "internal" / "other" / "legacy.go").write_text(
        OUTSIDE_FIXTURE, encoding="utf-8"
    )


def gate(sandbox, env):
    return run(["just", "lint-phase-gate"], sandbox, env)


def golangci_issues(sandbox, env):
    """Every violation golangci-lint reports in the sandbox, whole-tree."""
    result = run(
        [
            "golangci-lint",
            "run",
            "--issues-exit-code=0",
            "--show-stats=false",
            "--output.json.path=stdout",
            f"--output.text.path={os.devnull}",
            "./...",
        ],
        sandbox,
        env,
    )
    try:
        report = json.loads(result.stdout)
    except json.JSONDecodeError:
        fail(
            "golangci-lint did not produce a JSON report in the sandbox",
            (result.stderr or result.stdout).strip(),
        )
    return report.get("Issues") or []


def issue_paths(sandbox, env):
    """(path, message) for every issue, paths slash-normalized."""
    return [
        (issue["Pos"]["Filename"].replace(os.sep, "/"), issue["Text"])
        for issue in golangci_issues(sandbox, env)
    ]


def assert_tolerated_violations_exist(sandbox, env):
    paths = issue_paths(sandbox, env)
    reported = {path for path, _ in paths}
    for expected in ("internal/cli/probe_test.go", "internal/other/legacy.go"):
        if expected not in reported:
            fail(
                f"the fixture's {expected} does not violate any threshold",
                "It is what the gate's narrow verdict is measured against; without a "
                "real violation there, a passing gate proves nothing.",
                ", ".join(sorted(reported)) or "(no issues reported at all)",
            )
    if PROBE_FILE in reported:
        fail(
            f"the fixture's {PROBE_FILE} already violates a threshold at the baseline",
            "It must start clean or the negative assertion proves nothing.",
            "; ".join(text for path, text in paths if path == PROBE_FILE),
        )
    note(
        "baseline: internal/cli production code is clean, and both tolerated "
        "violations really are violations"
    )


def assert_gate_passes_when_clean(sandbox, env):
    result = gate(sandbox, env)
    if result.returncode != 0:
        fail(
            "the gate failed with production internal/cli code clean",
            "It must tolerate a violation outside internal/cli and one in a "
            "_test.go file inside it.",
            (result.stdout + result.stderr).strip(),
        )
    note("gate passes with clean production internal/cli code")


def assert_invisible_to_name_match(sandbox, env):
    """The fixture must be something the old constructor-name gate could not see."""
    report = run(
        ["golangci-lint", "run", "--issues-exit-code=0", "./..."], sandbox, env
    )
    matched = subprocess.run(
        ["grep", "-iE", LEGACY_CONSTRUCTOR_PATTERN],
        input=report.stdout + report.stderr,
        capture_output=True,
        text=True,
        check=False,
    )
    if matched.stdout.strip():
        fail(
            "the violating fixture is visible to the old constructor-name pattern",
            "Then this self-test no longer proves the gate catches what that "
            "pattern missed. Rename the fixture function away from *Cmd shape.",
            matched.stdout.strip(),
        )
    note(
        f"the old constructor-name pattern reports nothing for `{PROBE_FUNC}` — "
        "that is the hole this gate closes"
    )


def assert_gate_fails_on_production_violation(sandbox, env):
    result = gate(sandbox, env)
    output = (result.stdout + result.stderr).strip()
    if result.returncode == 0:
        fail(
            f"the gate passed with `{PROBE_FUNC}` over gocognit 20 in {PROBE_FILE}",
            "This is the exact defect it exists to catch.",
            output,
        )
    if PROBE_FILE not in output:
        fail(
            f"the gate failed but did not name {PROBE_FILE}",
            "A gate that cannot say what to fix is not usable.",
            output,
        )
    note(
        f"gate fails on the non-constructor violation (exit {result.returncode}) "
        f"and names {PROBE_FILE}"
    )
    return output


def assert_verdict_stays_narrow(output):
    for tolerated in ("internal/cli/probe_test.go", "internal/other/legacy.go"):
        if re.search(rf"^{re.escape(tolerated)}:", output, re.MULTILINE):
            fail(
                f"the gate reported {tolerated} as a hit",
                "The verdict must stay narrow: production internal/cli code only.",
                output,
            )
    note("verdict stays narrow: neither tolerated violation is reported as a hit")


def selftest(workdir):
    env = dict(os.environ)
    sandbox = workdir / "vault-of-nothing"
    sandbox.mkdir()
    create_sandbox(sandbox)

    assert_tolerated_violations_exist(sandbox, env)
    assert_gate_passes_when_clean(sandbox, env)

    write_probe(sandbox, BRANCHES_OVER_THRESHOLD)
    assert_invisible_to_name_match(sandbox, env)
    output = assert_gate_fails_on_production_violation(sandbox, env)
    assert_verdict_stays_narrow(output)


def main():
    for tool, hint in (
        ("golangci-lint", "Install the pinned version with:  just install-lint"),
        ("just", "Install just: https://github.com/casey/just"),
    ):
        if shutil.which(tool) is None:
            print(
                f"lint-phase-gate-selftest: {tool} not found on PATH; the phase "
                f"gate cannot be self-tested.\n  {hint}",
                file=sys.stderr,
            )
            return 1
    with tempfile.TemporaryDirectory(prefix="bn-phase-gate-selftest-") as tmp:
        try:
            selftest(Path(tmp))
        except SelfTestError as err:
            print(f"lint-phase-gate-selftest: {err}", file=sys.stderr)
            return 1
    note("pass")
    return 0


if __name__ == "__main__":
    sys.exit(main())
