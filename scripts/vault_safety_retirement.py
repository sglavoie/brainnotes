"""Retiring the old File Provider root demonstrably lost nothing.

The old root kept being written to after the pre-migration baseline was sealed.
`bn` refuses every write under File Provider storage, but an editor does not, so
notes continued to land there and were migrated forward later. "The Trash copy
still equals the sealed archive" is therefore false, and it was never the
property that mattered anyway. What matters is that every file in the trashed
copy is byte-present either in the live vault's Git object database — at any
commit, so a superseded version still counts — or in the sealed archive.

The remainder is machinery, and it is classified by path here rather than
waived, because "everything left over is fine" is exactly how a stranded note
gets retired. Anything outside these classes is unaccounted and fails.
"""

from __future__ import annotations

import os
from pathlib import Path, PurePosixPath

from vault_safety_gitio import (
    CheckFailure,
    HarnessError,
    blobs_present,
    git_blob_oid,
    regular_files,
    run,
    sha256_file,
)
from vault_safety_record import HEX64

MACHINERY_PREFIXES = (
    ".bnotes/index/",  # derived note cache, deliberately untracked
    ".bnotes/locks/",  # process bookkeeping
    ".bnotes/journal/",  # the old root's own undo journal
    ".codegraph/",  # a code index that was pointed at the directory
    "backups/",  # `bn`'s own archive tarballs of this same content
)
MACHINERY_PATHS = frozenset(
    {
        ".bnotes/brief.json",
        ".bnotes/cockpit.json",
        ".bnotes/recall.json",
        ".gitignore",
        "brainnotes.toml",
    }
)
MACHINERY_NAMES = frozenset({".DS_Store"})


def verify_retirement(*, record: dict, old_root: Path, archive: Path) -> str:
    gate = record["gates"].get("old_vault_trashed_recoverably")
    if not isinstance(gate, dict):
        raise CheckFailure("no old_vault_trashed_recoverably gate in the evidence record")

    if old_root.exists():
        raise CheckFailure(f"the old File Provider root is still in place: {old_root}")

    location = gate.get("trash_location")
    if not isinstance(location, str) or not location.startswith("~/"):
        raise CheckFailure("trash_location must be a redacted home-relative path beginning with ~/")
    trashed = Path(location).expanduser()
    if not trashed.is_dir():
        raise CheckFailure(
            "the recorded Trash location does not exist, so the old vault is not recoverable"
        )

    digest = gate.get("old_vault_manifest_sha256")
    if not isinstance(digest, str) or not HEX64.match(digest):
        raise CheckFailure("old_vault_manifest_sha256 must be 64 lowercase hex characters")
    manifest = archive / "SHA256SUMS"
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


def verify_trashed_copy_is_fully_accounted(*, record: dict, live_vault: Path, archive: Path) -> str:
    """Nothing was lost by retiring the old root.

    `outcome: true` on a retirement gate means "this looked safe when I looked".
    This re-reads the bytes, because the whole reason the old copy went to Trash
    rather than to `rm` is that it is the last resort.

    Files that are in neither surviving source are classified as machinery by
    path, and anything that is not machinery either fails the check by count —
    never by name, since an unaccounted file is by definition one whose name
    might be a note.
    """
    gate = record["gates"].get("old_vault_trashed_recoverably", {})
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
    accounted = blobs_present(live_vault, list(oids))
    remaining = [oid for oid in oids if oid not in accounted]
    if remaining:
        archive_oids = {git_blob_oid(path) for path in regular_files(archive)}
        accounted |= {oid for oid in remaining if oid in archive_oids}

    machinery = 0
    unaccounted = 0
    for oid, rels in oids.items():
        if oid in accounted:
            continue
        for rel in rels:
            if (
                rel.startswith(MACHINERY_PREFIXES)
                or rel in MACHINERY_PATHS
                or PurePosixPath(rel).name in MACHINERY_NAMES
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


def verify_no_process_uses_the_old_root(*, old_root: Path, harness_script_name: str) -> str:
    completed = run(["ps", "-Ao", "pid=,command="], timeout=60)
    if completed.returncode != 0:
        raise HarnessError("could not read the process table")
    needle = str(old_root)
    offenders = []
    for line in completed.stdout.splitlines():
        pid, _, command = line.strip().partition(" ")
        if needle not in command:
            continue
        # This harness is invoked with --old-root on its own argv, so it and
        # anything that spawned it match by construction. Excluding by script
        # name rather than by pid also covers the shell and the task runner in
        # between.
        if harness_script_name in command or pid == str(os.getpid()):
            continue
        offenders.append(pid)
    if offenders:
        raise CheckFailure(
            f"{len(offenders)} process(es) still reference the old root: {', '.join(offenders)}"
        )
    return "no process references the old root"


def verify_config_points_only_at_the_live_vault(*, live_vault: Path, old_root: Path) -> str:
    marker = live_vault / "brainnotes.toml"
    if not marker.is_file():
        raise CheckFailure(f"{live_vault} has no brainnotes.toml, so it is not a vault")
    if (old_root / "brainnotes.toml").exists():
        raise CheckFailure(
            "the old root still contains brainnotes.toml, so `bn` run from inside it "
            "would still resolve there"
        )
    agents = Path.home() / "Library/LaunchAgents"
    offenders = []
    for plist in sorted(agents.glob("*.plist")):
        try:
            if str(old_root) in plist.read_text(encoding="utf-8", errors="replace"):
                offenders.append(plist.name)
        except OSError:
            continue
    if offenders:
        raise CheckFailure(f"LaunchAgents still name the old root: {', '.join(offenders)}")
    return "the live vault is the only configured root"
