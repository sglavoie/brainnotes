#!/usr/bin/env python3
"""Prove the Go complexity ratchet catches a body-only complexity increase.

This is the regression test for the hole `scripts/lintratchet` exists to close.
`golangci-lint --new-from-merge-base` filters issues by changed *line*, and
cyclop, gocognit and funlen all report a function's score on its *declaration*
line — so editing only the body of an existing function leaves that function's
own violation outside the diff range and the gate stays green while the code
gets worse. A gate that silently stops enforcing is worse than no gate, and the
only way to know it still enforces is to make it fail on demand.

Everything happens in a throwaway Git repository under the system temporary
directory. Nothing here reads or writes this repository, and the only thing
borrowed from it is `.golangci.yml`, copied so the self-test cannot pass against
thresholds this repository does not actually use.

Four assertions, in order:

  0. BASELINE. At the baseline commit `Probe` is under every threshold and
     `BaselineViolation` is over gocognit. That is what makes "from below to
     above a configured threshold" a measurement rather than a claim.
  1. BODY-ONLY EDIT. Rewriting only `Probe`'s body leaves its declaration line
     out of the diff. Asserted against `git diff` itself, not assumed.
  2. THE RATCHET FAILS. `lintratchet` exits non-zero and names `Probe`.
     Alongside it, plain `--new-from-merge-base` is run on the identical tree:
     when it passes, the hole this tool closes is confirmed to still be there.
  3. UNTOUCHED VIOLATIONS ARE STILL TOLERATED. A different body-only edit that
     stays under the thresholds exits 0 even though `BaselineViolation` is still
     over gocognit in that same edited file. This is what separates the fix from
     golangci-lint's `issues.whole-files`, which would fail here.

Run it with `just lint-ratchet-selftest`. Exit 0 is a pass; exit 1 names the
assertion that failed.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent

# The fixture module. `Probe` is the function the self-test edits;
# `BaselineViolation` is the negative control it never touches.
#
# Both bodies are built by flat_branch_body below rather than written out, so
# the one property the fixture has to hold — a violation reported on the
# declaration line and on no other line — is stated once, as code.
FIXTURE_HEADER = """package selftest

// BaselineViolation is over the gocognit threshold at the baseline commit, and
// nothing in this self-test ever edits it. It is the negative control: the
// ratchet must keep tolerating it while other lines of this same file change.
func BaselineViolation(xs []int) int {
"""

PROBE_DOC = """// Probe is the function the self-test edits. Only its body ever changes: this
// declaration line stays byte-identical and at the same line number, which is
// exactly the shape a line-anchored filter cannot see.
"""

PROBE_DECL = "func Probe(xs []int) int {"

# 10 branches score gocognit 21, one over the threshold. 1 and 2 are far under.
BRANCHES_OVER_THRESHOLD = 10
BRANCHES_AT_BASELINE = 1
BRANCHES_AFTER_HARMLESS_EDIT = 2


def flat_branch_body(branches):
    """A function body whose only complexity is `branches` unnested `if`s.

    gocognit charges +1 for the loop and +2 for each `if` one level inside it,
    so 10 branches score 21 and clear the threshold of 20. The branches are
    deliberately *flat*: nestif stays at 1 per branch, far under its threshold
    of 4, so the only issue reported anywhere in the function lands on the
    declaration line. That is the whole point of the fixture. A nested body
    would also trip nestif on a line the edit actually touched, and then the
    ratchet could fail for a reason the old line-anchored filter already
    caught — which would prove nothing about the hole being closed.
    """
    lines = ["\ttotal := 0", "\tfor _, x := range xs {"]
    for n in range(branches):
        lines += [f"\t\tif x == {n} {{", "\t\t\ttotal++", "\t\t}"]
    lines += ["\t}", "\treturn total", ""]
    return "\n".join(lines)


class SelfTestError(Exception):
    """An assertion this self-test makes about the ratchet did not hold."""


def fail(message, *details):
    raise SelfTestError("\n  ".join([message, *details]))


def note(message):
    print(f"lint-ratchet-selftest: {message}")


def fixture(probe_branches):
    """The whole fixture file, with `Probe` given the requested branch count."""
    return (
        FIXTURE_HEADER
        + flat_branch_body(BRANCHES_OVER_THRESHOLD)
        + "}\n\n"
        + PROBE_DOC
        + PROBE_DECL
        + "\n"
        + flat_branch_body(probe_branches)
        + "}\n"
    )


def probe_declaration_line(text):
    """The 1-based line number of `Probe`'s declaration in the fixture."""
    for number, line in enumerate(text.splitlines(), start=1):
        if line == PROBE_DECL:
            return number
    fail(f"the fixture no longer contains the line {PROBE_DECL!r}")
    return 0


def run(argv, cwd, env):
    return subprocess.run(
        argv, cwd=cwd, env=env, capture_output=True, text=True, check=False
    )


def git(sandbox, env, *args):
    result = run(["git", *args], sandbox, env)
    if result.returncode != 0:
        fail(
            f"git {' '.join(args)} failed in the sandbox",
            result.stderr.strip() or result.stdout.strip(),
        )
    return result.stdout


def sandbox_env():
    """A hermetic environment for the sandbox.

    The global and system Git configs are taken out of the picture so a
    developer's `commit.gpgsign`, hooks path, or template directory cannot make
    the self-test fail (or, worse, pass) for reasons that have nothing to do
    with the ratchet.
    """
    env = dict(os.environ)
    env["GIT_CONFIG_GLOBAL"] = os.devnull
    env["GIT_CONFIG_SYSTEM"] = os.devnull
    env["GIT_AUTHOR_NAME"] = "ratchet selftest"
    env["GIT_AUTHOR_EMAIL"] = "selftest@example.invalid"
    env["GIT_COMMITTER_NAME"] = env["GIT_AUTHOR_NAME"]
    env["GIT_COMMITTER_EMAIL"] = env["GIT_AUTHOR_EMAIL"]
    return env


def go_directive():
    for line in (REPO / "go.mod").read_text(encoding="utf-8").splitlines():
        if line.startswith("go "):
            return line.strip()
    fail("no `go` directive in go.mod, so the sandbox module cannot pin a version")
    return ""


def build_ratchet(workdir, env):
    """Build the gate under test from this repository's source."""
    binary = workdir / ("lintratchet.exe" if os.name == "nt" else "lintratchet")
    result = run(
        ["go", "build", "-o", str(binary), "./scripts/lintratchet"], REPO, env
    )
    if result.returncode != 0:
        fail("could not build ./scripts/lintratchet", result.stderr.strip())
    return binary


def write_fixture(sandbox, probe_branches):
    (sandbox / "ratchet.go").write_text(fixture(probe_branches), encoding="utf-8")


def create_sandbox(sandbox, env):
    (sandbox / "go.mod").write_text(
        f"module bnratchetselftest\n\n{go_directive()}\n", encoding="utf-8"
    )
    # Copied rather than re-declared: a self-test that invents its own
    # thresholds proves nothing about the gate this repository actually runs.
    shutil.copyfile(REPO / ".golangci.yml", sandbox / ".golangci.yml")
    write_fixture(sandbox, BRANCHES_AT_BASELINE)
    git(sandbox, env, "init", "--quiet")
    git(sandbox, env, "add", "-A", "-f")
    git(sandbox, env, "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "baseline")
    return git(sandbox, env, "rev-parse", "HEAD").strip()


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


def functions_with_issues(sandbox, env):
    return {
        (issue["Text"], issue["FromLinter"]) for issue in golangci_issues(sandbox, env)
    }


def changed_lines(sandbox, env, base):
    """The post-image line numbers `git diff` reports for the fixture."""
    diff = git(sandbox, env, "diff", "--no-prefix", "--unified=0", base, "--", "*.go")
    lines = set()
    for line in diff.splitlines():
        if not line.startswith("@@"):
            continue
        added = line.split("+", 1)[1].split(" ", 1)[0]
        start, _, count = added.partition(",")
        span = int(count) if count else 1
        lines.update(range(int(start), int(start) + span))
    return lines


def assert_baseline_thresholds(sandbox, env):
    texts = " ".join(text for text, _ in functions_with_issues(sandbox, env))
    if "Probe" in texts:
        fail(
            "the fixture's `Probe` already violates a threshold at the baseline",
            "It must start *under* every threshold or the body-only edit proves nothing.",
            texts,
        )
    if "BaselineViolation" not in texts:
        fail(
            "the fixture's `BaselineViolation` is under every threshold at the baseline",
            "It is the untouched violation assertion 3 requires; make it more complex.",
            texts or "(no issues reported at all)",
        )
    note("baseline: `Probe` is clean and `BaselineViolation` is over gocognit")


def assert_declaration_untouched(sandbox, env, base):
    decl = probe_declaration_line(fixture(BRANCHES_OVER_THRESHOLD))
    changed = changed_lines(sandbox, env, base)
    if decl in changed:
        fail(
            f"the edit touched `Probe`'s declaration line ({decl})",
            "The whole point is a body-only change; fix the fixture, not the gate.",
        )
    if not changed:
        fail("the edit changed no line at all, so there is nothing to detect")
    note(
        f"body-only edit: lines {min(changed)}-{max(changed)} changed, "
        f"declaration line {decl} untouched"
    )


def assert_legacy_filter_misses_it(sandbox, env, base):
    """Report whether the hole the ratchet closes is still open upstream.

    This is deliberately not an assertion. golangci-lint could grow a
    function-aware mode, and the day it does this self-test must not turn red
    for the good news.
    """
    result = run(
        [
            "golangci-lint",
            "run",
            f"--new-from-merge-base={base}",
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
        reported = json.loads(result.stdout).get("Issues") or []
    except json.JSONDecodeError:
        fail(
            "the legacy filter did not produce a JSON report in the sandbox",
            (result.stderr or result.stdout).strip(),
        )
    if not any("Probe" in issue["Text"] for issue in reported):
        note(
            f"confirmed: plain --new-from-merge-base reports {len(reported)} issue(s) "
            "and none of them is `Probe` — that is the hole scripts/lintratchet closes"
        )
    else:
        note(
            "note: plain --new-from-merge-base now reports `Probe` too; the ratchet is "
            "belt-and-braces here rather than the only line of defence"
        )


def assert_ratchet_fails(binary, sandbox, env, base):
    result = run([str(binary), "-base", base], sandbox, env)
    if result.returncode == 0:
        fail(
            "the ratchet passed on a function whose body went over gocognit 20",
            "This is the exact defect it exists to catch.",
            result.stdout.strip(),
        )
    if "Probe" not in result.stdout:
        fail(
            "the ratchet failed but did not name `Probe`",
            "A gate that cannot say what to fix is not usable.",
            result.stdout.strip(),
        )
    note(f"ratchet fails on the body-only increase (exit {result.returncode}) and names `Probe`")


def assert_untouched_violation_tolerated(binary, sandbox, env, base):
    result = run([str(binary), "-base", base], sandbox, env)
    if result.returncode != 0:
        fail(
            "the ratchet failed on a file whose only remaining violation is in an "
            "untouched function",
            "A baseline violation must stay tolerated; this is whole-file behaviour.",
            result.stdout.strip() or result.stderr.strip(),
        )
    note("untouched `BaselineViolation` still tolerated in an edited file")


def selftest(workdir):
    env = sandbox_env()
    binary = build_ratchet(workdir, env)
    sandbox = workdir / "vault-of-nothing"
    sandbox.mkdir()

    base = create_sandbox(sandbox, env)
    assert_baseline_thresholds(sandbox, env)

    write_fixture(sandbox, BRANCHES_OVER_THRESHOLD)
    assert_declaration_untouched(sandbox, env, base)
    assert_legacy_filter_misses_it(sandbox, env, base)
    assert_ratchet_fails(binary, sandbox, env, base)

    write_fixture(sandbox, BRANCHES_AFTER_HARMLESS_EDIT)
    assert_untouched_violation_tolerated(binary, sandbox, env, base)


def main():
    if shutil.which("golangci-lint") is None:
        print(
            "lint-ratchet-selftest: golangci-lint not found on PATH; the ratchet "
            "cannot be self-tested.\n"
            "  Install the pinned version with:  just install-lint",
            file=sys.stderr,
        )
        return 1
    with tempfile.TemporaryDirectory(prefix="bn-ratchet-selftest-") as tmp:
        try:
            selftest(Path(tmp))
        except SelfTestError as err:
            print(f"lint-ratchet-selftest: {err}", file=sys.stderr)
            return 1
    note("pass")
    return 0


if __name__ == "__main__":
    sys.exit(main())
