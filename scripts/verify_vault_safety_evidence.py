#!/usr/bin/env python3
"""Independently verify the vault safety evidence.

This is the acceptance harness for the vault loss-prevention work. Its job is to
be the thing that does *not* believe `docs/operations/vault-safety-evidence.json`.

That file is a record of gates a person ran once. A record is a claim, and a
claim that verifies itself is worth nothing: the failure mode this whole plan
exists to prevent is a vault that everyone believes is backed up. So every
assertion here is re-derived from something outside the record — the bytes of the
retained evidence file, the objects in two independent Git repositories, the
files inside the sealed archive, the live working tree, and the live process
table. The record is used only to say *which* gates are supposed to exist and
*where* their evidence is; whether each one passed is decided here, now.

What it checks
--------------

1. Record shape. Every gate carries `outcome: true`, an ISO-8601 `checked_at`,
   an integer `exit_status: 0`, a repository-relative `evidence_path` under
   `docs/operations/vault-safety-evidence/`, a 64-hex `evidence_sha256`, and
   `paths_redacted: true`. Absolute paths and paths that escape the evidence
   directory are rejected before anything is opened.
2. Evidence integrity. Every `evidence_sha256` is recomputed from the retained
   bytes on disk.
3. Redaction, derived rather than asserted. `paths_redacted: true` is checked by
   taking the vault's own tracked path set and proving that no note path and no
   note filename appears in any retained evidence file. A gate cannot claim to
   be redacted; it has to be.
4. Three-way restore identity. Fresh temporary restores are made from the local
   Git repository and from GitHub, and canonical SHA-256 manifests are computed
   over the exact tracked vault paths. At HEAD, the two restores and the live
   working tree must be byte-for-byte identical. At the pre-migration baseline
   commit, the two restores and the sealed archive must be byte-for-byte
   identical.
5. Live state. The installed health command must report a clean, pushed,
   unblocked, supervised vault — and then each of those is checked again here
   directly, from `git status`, from the safety state directory, from
   `git ls-remote`, and from `launchctl`.
6. Retirement. The old File Provider root must be gone, its recorded Trash
   location must still exist, and every file in the trashed copy must be
   byte-present either in the live vault's Git object database or in the sealed
   archive — so that retiring it demonstrably lost nothing.

Exit status is 0 only when every check passes. Any failure prints the reason and
exits 1; a harness that cannot run its own checks exits 2.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
from datetime import datetime
from pathlib import Path, PurePosixPath

REPO_ROOT = Path(__file__).resolve().parents[1]
EVIDENCE_DIR_NAME = "docs/operations/vault-safety-evidence"
SCHEMA_VERSION = 1
HEX64 = re.compile(r"\A[0-9a-f]{64}\Z")

# Tracked paths that are repository or BrainNotes machinery rather than note
# content. These are allowed to appear verbatim in a retained evidence file: an
# evidence record that could not name `blocked.json` or `brainnotes.toml` would
# be unreadable, and neither one reveals anything about what the user writes.
STRUCTURAL_PREFIXES = (".bnotes/",)
STRUCTURAL_PATHS = frozenset({".gitignore", "brainnotes.toml"})

# `.gitignore` is the one tracked path the sealed archive cannot match. The
# archive is a copy of the pre-migration source, and the vault `.gitignore` was
# written *by* the migration to neutralise a global excludesFile that would
# otherwise have silently dropped `brainnotes.toml` from the history. It is Git
# plumbing, not vault content, so it is excluded from the manifest scope rather
# than waived per-run. Anything else appearing here would be a real divergence.
ARCHIVE_MANIFEST_EXCLUSIONS = frozenset({".gitignore"})

# A basename shorter than this is not distinctive enough to be worth scanning
# for: "ok.md" or "b.md" would collide with ordinary English in an evidence
# narrative and turn the redaction check into noise. Full relative paths are
# checked at every length, which is what a real leak would look like.
MIN_SCANNED_BASENAME = 8

# The old root kept being written to after the pre-migration baseline was
# sealed. `bn` refuses every write under File Provider storage, but an editor
# does not, so notes continued to land there and were migrated forward later.
# "The Trash copy still equals the sealed archive" is therefore false, and it was
# never the property that mattered anyway. What matters is that retiring the old
# root lost nothing: every file in it is byte-present either in the live vault's
# Git object database — at any commit, so a superseded version still counts — or
# in the sealed archive.
#
# The remainder is machinery, and it is classified here rather than waived,
# because "everything left over is fine" is exactly how a stranded note gets
# retired. Anything outside these classes is unaccounted and fails the check.
RETIREMENT_MACHINERY_PREFIXES = (
    ".bnotes/index/",  # derived note cache, deliberately untracked
    ".bnotes/locks/",  # process bookkeeping
    ".bnotes/journal/",  # the old root's own undo journal
    ".codegraph/",  # a code index that was pointed at the directory
    "backups/",  # `bn`'s own archive tarballs of this same content
)
RETIREMENT_MACHINERY_PATHS = frozenset(
    {
        ".bnotes/brief.json",
        ".bnotes/cockpit.json",
        ".bnotes/recall.json",
        ".gitignore",
        "brainnotes.toml",
    }
)
RETIREMENT_MACHINERY_NAMES = frozenset({".DS_Store"})


class CheckFailure(Exception):
    """A gate did not hold. Carries the operator-facing reason."""


class HarnessError(Exception):
    """The harness could not run a check at all."""


# --------------------------------------------------------------------------
# process helpers
# --------------------------------------------------------------------------


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


# --------------------------------------------------------------------------
# record validation
# --------------------------------------------------------------------------


def validate_evidence_path(raw: object, evidence_root: Path) -> Path:
    """Resolve a gate's evidence path, refusing anything that leaves the tree.

    Three distinct escapes are refused: an absolute path, a traversal through
    `..`, and a path that only *resolves* outside the directory because a symlink
    inside it points away. The third is the one a shape check alone would miss,
    and it is the one that would let a gate be "verified" against a file nobody
    reviewed.
    """
    if not isinstance(raw, str) or not raw:
        raise CheckFailure(f"evidence_path must be a non-empty string, got {raw!r}")
    pure = PurePosixPath(raw)
    if pure.is_absolute():
        raise CheckFailure(f"evidence_path must be repository-relative: {raw}")
    if any(part == ".." for part in pure.parts):
        raise CheckFailure(f"evidence_path must not traverse upward: {raw}")
    if not raw.startswith(EVIDENCE_DIR_NAME + "/"):
        raise CheckFailure(f"evidence_path must live under {EVIDENCE_DIR_NAME}/: {raw}")
    resolved = (REPO_ROOT / raw).resolve()
    if not str(resolved).startswith(str(evidence_root.resolve()) + os.sep):
        raise CheckFailure(f"evidence_path resolves outside the evidence directory: {raw}")
    return resolved


def validate_gate(name: str, gate: object, evidence_root: Path) -> Path:
    if not isinstance(gate, dict):
        raise CheckFailure(f"gate {name} must be an object")
    if gate.get("outcome") is not True:
        raise CheckFailure(f"gate {name}: outcome is {gate.get('outcome')!r}, not true")
    if gate.get("paths_redacted") is not True:
        raise CheckFailure(f"gate {name}: paths_redacted is not true")
    status = gate.get("exit_status")
    # `True` is an int in Python and would otherwise sail through `== 0` checks
    # in the other direction; be explicit about the type.
    if not isinstance(status, int) or isinstance(status, bool) or status != 0:
        raise CheckFailure(f"gate {name}: exit_status must be the integer 0, got {status!r}")
    checked_at = gate.get("checked_at")
    if not isinstance(checked_at, str):
        raise CheckFailure(f"gate {name}: checked_at must be an ISO-8601 string")
    try:
        datetime.fromisoformat(checked_at.replace("Z", "+00:00"))
    except ValueError as exc:
        raise CheckFailure(f"gate {name}: checked_at is not ISO-8601: {checked_at}") from exc
    digest = gate.get("evidence_sha256")
    if not isinstance(digest, str) or not HEX64.match(digest):
        raise CheckFailure(f"gate {name}: evidence_sha256 must be 64 lowercase hex characters")

    path = validate_evidence_path(gate.get("evidence_path"), evidence_root)
    if not path.is_file():
        raise CheckFailure(f"gate {name}: evidence file is missing: {gate.get('evidence_path')}")
    actual = sha256_file(path)
    if actual != digest:
        raise CheckFailure(
            f"gate {name}: evidence file does not hash to its recorded digest "
            f"(recorded {digest}, actual {actual})"
        )
    return path


# --------------------------------------------------------------------------
# checks
# --------------------------------------------------------------------------


class Harness:
    def __init__(self, args: argparse.Namespace) -> None:
        self.args = args
        self.live_vault = Path(args.live_vault)
        self.archive = Path(args.sealed_archive)
        self.old_root = Path(args.old_root)
        self.evidence_file = (REPO_ROOT / args.evidence).resolve()
        self.evidence_root = (REPO_ROOT / EVIDENCE_DIR_NAME).resolve()
        self.results: list[tuple[str, bool, str]] = []
        self.record: dict = {}
        self.evidence_files: dict[str, Path] = {}

    # -- plumbing ---------------------------------------------------------

    def check(self, name: str, fn) -> None:
        try:
            detail = fn()
        except CheckFailure as exc:
            self.results.append((name, False, str(exc)))
        except HarnessError as exc:
            self.results.append((name, False, f"could not run: {exc}"))
        except subprocess.TimeoutExpired as exc:
            self.results.append((name, False, f"timed out: {exc}"))
        else:
            self.results.append((name, True, detail or "ok"))

    # -- record -----------------------------------------------------------

    def load_record(self) -> str:
        if not self.evidence_file.is_file():
            raise CheckFailure(f"evidence record not found: {self.args.evidence}")
        try:
            record = json.loads(self.evidence_file.read_text(encoding="utf-8"))
        except ValueError as exc:
            raise CheckFailure(f"evidence record is not valid JSON: {exc}") from exc
        if record.get("schema_version") != SCHEMA_VERSION:
            raise CheckFailure(
                f"evidence record schema_version must be {SCHEMA_VERSION}, "
                f"got {record.get('schema_version')!r}"
            )
        gates = record.get("gates")
        if not isinstance(gates, dict) or not gates:
            raise CheckFailure("evidence record must carry a non-empty gates object")
        self.record = record
        return f"schema version {SCHEMA_VERSION}, {len(gates)} gate(s)"

    def verify_gates(self) -> str:
        gates = self.record["gates"]
        for name in sorted(gates):
            self.evidence_files[name] = validate_gate(name, gates[name], self.evidence_root)
        return f"{len(gates)} gate record(s) validated and re-hashed"

    def verify_no_orphan_evidence(self) -> str:
        """Every retained file belongs to a gate.

        An evidence file with no gate pointing at it is unhashed and unreviewed,
        and it is exactly where a file that failed its check would end up if
        someone quietly dropped the gate instead of fixing it.
        """
        if not self.evidence_root.is_dir():
            raise CheckFailure(f"evidence directory is missing: {EVIDENCE_DIR_NAME}")
        claimed = {path.resolve() for path in self.evidence_files.values()}
        found = {path.resolve() for path in self.evidence_root.rglob("*") if path.is_file()}
        orphans = sorted(str(path.relative_to(self.evidence_root)) for path in found - claimed)
        if orphans:
            raise CheckFailure(f"retained evidence with no gate record: {', '.join(orphans)}")
        return f"{len(found)} retained file(s), all claimed by a gate"

    # -- redaction --------------------------------------------------------

    def verify_redaction(self) -> str:
        """Prove `paths_redacted`, do not take its word for it."""
        head = git(["rev-parse", "HEAD"], cwd=self.live_vault).strip()
        needles: set[str] = set()
        for rel in tracked_paths(self.live_vault, head):
            if rel in STRUCTURAL_PATHS or rel.startswith(STRUCTURAL_PREFIXES):
                continue
            needles.add(rel)
            base = PurePosixPath(rel).name
            if len(base) >= MIN_SCANNED_BASENAME:
                needles.add(base)
        if not needles:
            raise CheckFailure("no tracked note paths found, so redaction cannot be proven")

        leaks: list[str] = []
        for name, path in sorted(self.evidence_files.items()):
            text = path.read_text(encoding="utf-8", errors="replace")
            for needle in needles:
                if needle in text:
                    # Deliberately does not echo the needle: an error message
                    # that printed the leaked filename would itself become the
                    # leak, in a log nobody redacts.
                    leaks.append(f"{name} contains a tracked note path")
                    break
        if leaks:
            raise CheckFailure("; ".join(leaks))
        return f"{len(self.evidence_files)} evidence file(s) scanned against {len(needles)} note path(s)"

    # -- restores ---------------------------------------------------------

    def verify_restores(self) -> str:
        live_head = git(["rev-parse", "HEAD"], cwd=self.live_vault).strip()
        baseline = git(["rev-list", "--max-parents=0", "HEAD"], cwd=self.live_vault).strip()
        if len(baseline.split()) != 1:
            raise CheckFailure("the vault has more than one root commit; the baseline is ambiguous")

        origin = git(["remote", "get-url", "origin"], cwd=self.live_vault).strip()
        slug = self.args.github_repository
        if slug not in origin:
            raise CheckFailure(
                f"the vault's origin does not name {slug}; refusing to verify against a "
                "repository the vault does not actually push to"
            )

        head_paths = tracked_paths(self.live_vault, live_head)
        base_paths = [
            rel
            for rel in tracked_paths(self.live_vault, baseline)
            if rel not in ARCHIVE_MANIFEST_EXCLUSIONS
        ]
        if not head_paths or not base_paths:
            raise CheckFailure("the vault has no tracked paths to compare")

        with tempfile.TemporaryDirectory(prefix="vault-restore-") as tmp:
            root = Path(tmp)
            local = root / "from-local-git"
            remote = root / "from-github"
            # --no-hardlinks makes the local restore a real copy of the objects
            # rather than a set of links back into the vault it is meant to be
            # independent of.
            git(["clone", "--no-hardlinks", "--quiet", str(self.live_vault), str(local)], cwd=root)
            git(["clone", "--quiet", origin, str(remote)], cwd=root, timeout=self.args.clone_timeout)

            for name, clone in (("local", local), ("github", remote)):
                got = git(["rev-parse", "HEAD"], cwd=clone).strip()
                if got != live_head:
                    raise CheckFailure(
                        f"the {name} restore is at {got[:12]} but the live vault is at "
                        f"{live_head[:12]}"
                    )

            manifests = {
                "live vault": canonical_manifest(self.live_vault, head_paths),
                "local Git restore": canonical_manifest(local, head_paths),
                "GitHub restore": canonical_manifest(remote, head_paths),
            }
            self._require_identical("HEAD", manifests)
            head_digest = hashlib.sha256(manifests["live vault"]).hexdigest()

            for clone in (local, remote):
                git(["checkout", "--quiet", "--detach", baseline], cwd=clone)

            baseline_manifests = {
                "local Git restore": canonical_manifest(local, base_paths),
                "GitHub restore": canonical_manifest(remote, base_paths),
                "sealed archive": canonical_manifest(self.archive, base_paths),
            }
            self._require_identical("the pre-migration baseline", baseline_manifests)
            base_digest = hashlib.sha256(baseline_manifests["sealed archive"]).hexdigest()

        return (
            f"HEAD {live_head[:12]}: {len(head_paths)} paths identical across live vault, "
            f"local Git and GitHub (manifest {head_digest[:12]}); baseline {baseline[:12]}: "
            f"{len(base_paths)} paths identical across local Git, GitHub and the sealed "
            f"archive (manifest {base_digest[:12]})"
        )

    @staticmethod
    def _require_identical(label: str, manifests: dict[str, bytes]) -> None:
        reference_name, reference = next(iter(manifests.items()))
        for name, manifest in manifests.items():
            if manifest == reference:
                continue
            differing = [
                line.split("  ", 1)[1]
                for line, other in zip(
                    manifest.decode().splitlines(), reference.decode().splitlines()
                )
                if line != other
            ]
            count = len(differing) or "an unknown number of"
            raise CheckFailure(
                f"at {label} the {name} manifest differs from the {reference_name} "
                f"manifest in {count} path(s)"
            )

    # -- live state -------------------------------------------------------

    def verify_health_command(self) -> str:
        command = Path(self.args.health_command)
        if not command.is_file() or not os.access(command, os.X_OK):
            raise CheckFailure(f"health command is not installed or not executable: {command}")
        completed = run(
            [
                str(command),
                "--vault",
                str(self.live_vault),
                "--label",
                self.args.launchagent_label,
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

    def verify_live_vault_directly(self) -> str:
        """The same four properties, derived here rather than reported to us."""
        status = git(["status", "--porcelain"], cwd=self.live_vault)
        if status.strip():
            raise CheckFailure(
                f"the live working tree has {len(status.strip().splitlines())} uncommitted change(s)"
            )

        git_dir = Path(git(["rev-parse", "--absolute-git-dir"], cwd=self.live_vault).strip())
        safety_dir = git_dir / "brainnotes-safety"
        for marker, meaning in (
            ("blocked.json", "the safety engine is blocked"),
            ("intent.json", "a snapshot was interrupted and has not been recovered"),
        ):
            if (safety_dir / marker).exists():
                raise CheckFailure(meaning)

        head = git(["rev-parse", "HEAD"], cwd=self.live_vault).strip()
        branch = git(["rev-parse", "--abbrev-ref", "HEAD"], cwd=self.live_vault).strip()
        remote_line = git(
            ["ls-remote", "--heads", "origin", branch], cwd=self.live_vault, timeout=90
        ).split()
        if not remote_line:
            raise CheckFailure(f"origin has no branch {branch}")
        if remote_line[0] != head:
            raise CheckFailure(
                f"origin/{branch} is at {remote_line[0][:12]} but the vault is at {head[:12]}"
            )
        return f"clean, unblocked, and pushed at {head[:12]}"

    def verify_launchagent(self) -> str:
        label = self.args.launchagent_label
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

        # The same dump also carries the job's environment, which is where the
        # guard is told which vault to watch. A loaded, running agent pointed at
        # a different directory would satisfy every other check here.
        if str(self.live_vault) not in completed.stdout:
            raise CheckFailure(
                f"LaunchAgent {label} does not name {self.live_vault}, so it is guarding "
                "something else"
            )
        return f"{label} is {state.group(1) if state else 'loaded'} as pid {pid.group(1)}"

    # -- retirement -------------------------------------------------------

    def verify_retirement(self) -> str:
        gate = self.record["gates"].get("old_vault_trashed_recoverably")
        if not isinstance(gate, dict):
            raise CheckFailure("no old_vault_trashed_recoverably gate in the evidence record")

        if self.old_root.exists():
            raise CheckFailure(f"the old File Provider root is still in place: {self.old_root}")

        location = gate.get("trash_location")
        if not isinstance(location, str) or not location.startswith("~/"):
            raise CheckFailure(
                "trash_location must be a redacted home-relative path beginning with ~/"
            )
        trashed = Path(location).expanduser()
        if not trashed.is_dir():
            raise CheckFailure(
                "the recorded Trash location does not exist, so the old vault is not recoverable"
            )

        digest = gate.get("old_vault_manifest_sha256")
        if not isinstance(digest, str) or not HEX64.match(digest):
            raise CheckFailure("old_vault_manifest_sha256 must be 64 lowercase hex characters")
        manifest = self.archive / "SHA256SUMS"
        if not manifest.is_file():
            raise CheckFailure(f"the sealed archive has no SHA256SUMS at {manifest}")
        actual = sha256_file(manifest)
        if actual != digest:
            raise CheckFailure(
                "the sealed archive's manifest does not match the approved old-vault digest "
                f"(approved {digest[:12]}, actual {actual[:12]})"
            )
        return (
            f"the old root is retired to {location}, which still holds the vault approved "
            f"against manifest {digest[:12]}"
        )

    def verify_trashed_copy_is_fully_accounted(self) -> str:
        """Nothing was lost by retiring the old root.

        `outcome: true` on a retirement gate means "this looked safe when I
        looked". This re-reads the bytes, because the whole reason the old copy
        went to Trash rather than to `rm` is that it is the last resort.

        Every file in the Trash copy has to be byte-present somewhere that
        survives: the live vault's object database, or the sealed archive. Files
        that are neither are classified as machinery by path, and anything that
        is not machinery either fails the check by count — never by name, since
        an unaccounted file is by definition one whose name might be a note.
        """
        gate = self.record["gates"].get("old_vault_trashed_recoverably", {})
        trashed = Path(gate.get("trash_location", "")).expanduser()
        if not trashed.is_dir():
            raise CheckFailure("no recoverable Trash copy to compare")

        files = regular_files(trashed)
        if not files:
            raise CheckFailure("the Trash copy is empty, so it is not the old vault")

        oids: dict[str, list[str]] = {}
        for path in files:
            oids.setdefault(git_blob_oid(path), []).append(
                str(path.relative_to(trashed)).replace(os.sep, "/")
            )
        accounted = blobs_present(self.live_vault, list(oids))
        remaining = [oid for oid in oids if oid not in accounted]
        if remaining:
            archive_oids = {git_blob_oid(path) for path in regular_files(self.archive)}
            accounted |= {oid for oid in remaining if oid in archive_oids}

        machinery = 0
        unaccounted = 0
        for oid, rels in oids.items():
            if oid in accounted:
                continue
            for rel in rels:
                if (
                    rel.startswith(RETIREMENT_MACHINERY_PREFIXES)
                    or rel in RETIREMENT_MACHINERY_PATHS
                    or PurePosixPath(rel).name in RETIREMENT_MACHINERY_NAMES
                ):
                    machinery += 1
                else:
                    unaccounted += 1
        if unaccounted:
            raise CheckFailure(
                f"{unaccounted} file(s) in the Trash copy exist in neither the live vault's "
                "history nor the sealed archive, and are not machinery; the old root must be "
                "restored from the Trash and reconciled before it is retired"
            )
        return (
            f"{len(files)} file(s) in the Trash copy are accounted for "
            f"({machinery} machinery file(s) classified, 0 unaccounted)"
        )

    def verify_no_process_uses_the_old_root(self) -> str:
        completed = run(["ps", "-Ao", "pid=,command="], timeout=60)
        if completed.returncode != 0:
            raise HarnessError("could not read the process table")
        needle = str(self.old_root)
        offenders = []
        for line in completed.stdout.splitlines():
            pid, _, command = line.strip().partition(" ")
            if needle not in command:
                continue
            # This harness is invoked with --old-root on its own argv, so it and
            # anything that spawned it match by construction. Excluding by
            # script name rather than by pid also covers the shell and the task
            # runner in between.
            if Path(__file__).name in command or pid == str(os.getpid()):
                continue
            offenders.append(pid)
        if offenders:
            raise CheckFailure(
                f"{len(offenders)} process(es) still reference the old root: {', '.join(offenders)}"
            )
        return "no process references the old root"

    def verify_config_points_only_at_the_live_vault(self) -> str:
        marker = self.live_vault / "brainnotes.toml"
        if not marker.is_file():
            raise CheckFailure(f"{self.live_vault} has no brainnotes.toml, so it is not a vault")
        if (self.old_root / "brainnotes.toml").exists():
            raise CheckFailure(
                "the old root still contains brainnotes.toml, so `bn` run from inside it "
                "would still resolve there"
            )
        agents = Path.home() / "Library/LaunchAgents"
        offenders = []
        for plist in sorted(agents.glob("*.plist")):
            try:
                if str(self.old_root) in plist.read_text(encoding="utf-8", errors="replace"):
                    offenders.append(plist.name)
            except OSError:
                continue
        if offenders:
            raise CheckFailure(f"LaunchAgents still name the old root: {', '.join(offenders)}")
        return "the live vault is the only configured root"

    # -- driver -----------------------------------------------------------

    def run_all(self) -> int:
        self.check("evidence-record-loads", self.load_record)
        if not self.results[-1][1]:
            self.report()
            return 1

        self.check("gate-records-and-hashes", self.verify_gates)
        self.check("no-orphan-evidence", self.verify_no_orphan_evidence)
        self.check("evidence-redaction", self.verify_redaction)
        self.check("restore-manifests-identical", self.verify_restores)
        self.check("health-command", self.verify_health_command)
        self.check("live-vault-clean-and-pushed", self.verify_live_vault_directly)
        self.check("launchagent-supervising", self.verify_launchagent)
        self.check("old-root-retired", self.verify_retirement)
        self.check("trashed-copy-fully-accounted", self.verify_trashed_copy_is_fully_accounted)
        self.check("no-process-uses-old-root", self.verify_no_process_uses_the_old_root)
        self.check("config-points-only-at-live-vault", self.verify_config_points_only_at_the_live_vault)
        self.report()
        return 0 if all(ok for _, ok, _ in self.results) else 1

    def report(self) -> None:
        if self.args.json:
            json.dump(
                {
                    "schema_version": SCHEMA_VERSION,
                    "checks": [
                        {"name": name, "passed": ok, "detail": detail}
                        for name, ok, detail in self.results
                    ],
                    "passed": all(ok for _, ok, _ in self.results),
                },
                sys.stdout,
                indent=2,
            )
            sys.stdout.write("\n")
            return
        width = max(len(name) for name, _, _ in self.results)
        for name, ok, detail in self.results:
            print(f"{'PASS' if ok else 'FAIL'}  {name.ljust(width)}  {detail}")
        failures = [name for name, ok, _ in self.results if not ok]
        print()
        if failures:
            print(f"{len(failures)} check(s) failed: {', '.join(failures)}")
        else:
            print(f"all {len(self.results)} checks passed")


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--evidence", required=True, help="repository-relative evidence record")
    parser.add_argument("--live-vault", required=True)
    parser.add_argument("--github-repository", required=True, help="owner/name of the content remote")
    parser.add_argument("--sealed-archive", required=True)
    parser.add_argument("--old-root", required=True, help="the retired File Provider vault")
    parser.add_argument("--health-command", required=True)
    parser.add_argument("--launchagent-label", required=True)
    parser.add_argument("--clone-timeout", type=int, default=120)
    parser.add_argument("--json", action="store_true", help="emit machine-readable results")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        return Harness(args).run_all()
    except HarnessError as exc:
        print(f"harness error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
