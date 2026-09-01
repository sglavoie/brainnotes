"""Process, Git and hashing primitives the vault-safety harness re-derives from.

Nothing here decides whether a gate passed. These are the readers the harness
uses to look at something outside the evidence record: a subprocess, a Git object
database, the bytes of a file. They raise `HarnessError` when they cannot answer
at all, and leave `CheckFailure` to the callers that know what the answer means.
"""

from __future__ import annotations

import hashlib
import os
import subprocess
from pathlib import Path


class CheckFailure(Exception):
    """A gate did not hold. Carries the operator-facing reason."""


class HarnessError(Exception):
    """The harness could not run a check at all."""


def run(argv: list[str], *, cwd: Path | None = None, timeout: int = 120, stdin: str | None = None):
    env = dict(os.environ)
    env["GIT_TERMINAL_PROMPT"] = "0"
    # A prompt under an automated harness is an indefinite hang, not a failure.
    env.setdefault("GIT_ASKPASS", "/usr/bin/true")
    return subprocess.run(
        argv,
        cwd=str(cwd) if cwd else None,
        env=env,
        input=stdin,
        text=True,
        capture_output=True,
        check=False,
        timeout=timeout,
    )


def git(args: list[str], *, cwd: Path, timeout: int = 120) -> str:
    completed = run(["git", *args], cwd=cwd, timeout=timeout)
    if completed.returncode != 0:
        raise HarnessError(
            f"git {' '.join(args)} failed in {cwd}: "
            f"{completed.stderr.strip() or completed.stdout.strip()}"
        )
    return completed.stdout


def tracked_paths(repo: Path, commit: str) -> list[str]:
    """Tracked paths at a commit, NUL-separated so filenames survive intact.

    `git ls-tree` without `-z` quotes anything non-ASCII, and a vault of prose
    notes is full of names that trip that. A quoted path would silently miss its
    file and the manifest would agree with itself about the wrong bytes.
    """
    raw = git(["ls-tree", "-r", "-z", "--name-only", commit], cwd=repo)
    return [entry for entry in raw.split("\0") if entry]


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def git_blob_oid(path: Path) -> str:
    """The object name Git would give this file's bytes.

    Computed rather than shelled out to, so a directory of a thousand notes is
    one pass instead of a thousand processes, and streamed so a 40 MB backup
    tarball does not have to be held in memory.
    """
    size = path.stat().st_size
    digest = hashlib.new("sha1", usedforsecurity=False)
    digest.update(b"blob %d\0" % size)
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def blobs_present(repo: Path, oids: list[str]) -> set[str]:
    """Which of these object names the repository actually holds.

    `--batch-check` answers for every name in one process and reports the ones
    it does not have as `missing`, which is the answer this is after: a blob is
    reachable history if the object store has it.
    """
    if not oids:
        return set()
    completed = run(
        ["git", "cat-file", "--batch-check"], cwd=repo, stdin="\n".join(oids) + "\n", timeout=900
    )
    if completed.returncode != 0:
        raise HarnessError(f"git cat-file failed in {repo}: {completed.stderr.strip()}")
    present = set()
    for line in completed.stdout.splitlines():
        parts = line.split()
        if len(parts) >= 2 and parts[1] != "missing":
            present.add(parts[0])
    return present


def regular_files(root: Path) -> list[Path]:
    return sorted(p for p in root.rglob("*") if p.is_file() and not p.is_symlink())


def canonical_manifest(root: Path, relpaths: list[str]) -> bytes:
    """A stable `<sha256>  <path>` manifest over an exact path set.

    Sorted by path so two sources that hold the same bytes produce the same
    bytes here, and a path that is missing from a source is recorded rather than
    skipped — a manifest that silently omitted what it could not find would
    report a truncated restore as a perfect one.
    """
    lines = []
    for rel in sorted(relpaths):
        candidate = root / rel
        if candidate.is_file() and not candidate.is_symlink():
            lines.append(f"{sha256_file(candidate)}  {rel}")
        else:
            lines.append(f"{'MISSING'.ljust(64)}  {rel}")
    return ("\n".join(lines) + "\n").encode("utf-8")
