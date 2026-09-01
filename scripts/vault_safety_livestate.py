"""Live state: what the installed health command says, and the same again directly.

The health command is asked first, and then every property it reported is
derived here from `git status`, from the safety state directory, from
`git ls-remote`, and from `launchctl`. A reporting tool that had itself gone
wrong would otherwise be the only witness to the vault being healthy.
"""

from __future__ import annotations

import json
import os
import re
from pathlib import Path

from vault_safety_gitio import CheckFailure, git, run


def verify_health_command(*, command: Path, live_vault: Path, launchagent_label: str) -> str:
    if not command.is_file() or not os.access(command, os.X_OK):
        raise CheckFailure(f"health command is not installed or not executable: {command}")
    completed = run(
        [
            str(command),
            "--vault",
            str(live_vault),
            "--label",
            launchagent_label,
            "--json",
        ],
        timeout=90,
    )
    try:
        report = json.loads(completed.stdout)
    except ValueError as exc:
        raise CheckFailure(f"health command did not emit JSON: {exc}") from exc
    if completed.returncode != 0:
        raise CheckFailure(
            "health command reported a problem: " + "; ".join(report.get("problems", []))
        )
    for key in (
        "worktree_clean",
        "guard_running",
        "agent_loaded",
        "remote_reachable",
        "healthy",
    ):
        if report.get(key) is not True:
            raise CheckFailure(f"health command reports {key} is not true")
    if report.get("blocked") is not False:
        raise CheckFailure("health command reports the safety engine is blocked")
    for key in ("ahead", "behind"):
        if report.get(key) != 0:
            raise CheckFailure(f"health command reports {report[key]} commit(s) {key}")
    return f"health command exited 0 with guard pid {report.get('guard_pid')}"


def verify_live_vault_directly(live_vault: Path) -> str:
    """The same four properties, derived here rather than reported to us."""
    status = git(["status", "--porcelain"], cwd=live_vault)
    if status.strip():
        raise CheckFailure(
            f"the live working tree has {len(status.strip().splitlines())} uncommitted change(s)"
        )

    git_dir = Path(git(["rev-parse", "--absolute-git-dir"], cwd=live_vault).strip())
    safety_dir = git_dir / "brainnotes-safety"
    for marker, meaning in (
        ("blocked.json", "the safety engine is blocked"),
        ("intent.json", "a snapshot was interrupted and has not been recovered"),
    ):
        if (safety_dir / marker).exists():
            raise CheckFailure(meaning)

    head = git(["rev-parse", "HEAD"], cwd=live_vault).strip()
    branch = git(["rev-parse", "--abbrev-ref", "HEAD"], cwd=live_vault).strip()
    remote_line = git(["ls-remote", "--heads", "origin", branch], cwd=live_vault, timeout=90).split()
    if not remote_line:
        raise CheckFailure(f"origin has no branch {branch}")
    if remote_line[0] != head:
        raise CheckFailure(
            f"origin/{branch} is at {remote_line[0][:12]} but the vault is at {head[:12]}"
        )
    return f"clean, unblocked, and pushed at {head[:12]}"


def verify_launchagent(*, launchagent_label: str, live_vault: Path) -> str:
    label = launchagent_label
    completed = run(["launchctl", "print", f"gui/{os.getuid()}/{label}"], timeout=60)
    if completed.returncode != 0:
        raise CheckFailure(f"LaunchAgent {label} is not loaded")
    state = re.search(r"^\s*state\s*=\s*(\S+)", completed.stdout, re.MULTILINE)
    pid = re.search(r"^\s*pid\s*=\s*(\d+)", completed.stdout, re.MULTILINE)
    if not pid:
        raise CheckFailure(f"LaunchAgent {label} is loaded but has no running process")
    try:
        os.kill(int(pid.group(1)), 0)
    except OSError as exc:
        raise CheckFailure(f"the guard pid {pid.group(1)} is not alive: {exc}") from exc

    # The same dump also carries the job's environment, which is where the guard
    # is told which vault to watch. A loaded, running agent pointed at a
    # different directory would satisfy every other check here.
    if str(live_vault) not in completed.stdout:
        raise CheckFailure(
            f"LaunchAgent {label} does not name {live_vault}, so it is guarding something else"
        )
    return f"{label} is {state.group(1) if state else 'loaded'} as pid {pid.group(1)}"
