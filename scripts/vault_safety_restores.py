"""The three-way restore identity check.

Fresh temporary restores are made from the local Git repository and from the
content remote, and canonical SHA-256 manifests are computed over the exact
tracked vault paths. At HEAD, the two restores and the live working tree must be
byte-for-byte identical. At the pre-migration baseline commit, the two restores
and the sealed archive must be byte-for-byte identical.
"""

from __future__ import annotations

import hashlib
import tempfile
from pathlib import Path

from vault_safety_gitio import CheckFailure, canonical_manifest, git, tracked_paths


def require_identical(label: str, manifests: dict[str, bytes]) -> None:
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


def verify_restores(
    *,
    live_vault: Path,
    archive: Path,
    github_repository: str,
    manifest_exclusions,
    clone_timeout: int,
) -> str:
    live_head = git(["rev-parse", "HEAD"], cwd=live_vault).strip()
    baseline = git(["rev-list", "--max-parents=0", "HEAD"], cwd=live_vault).strip()
    if len(baseline.split()) != 1:
        raise CheckFailure("the vault has more than one root commit; the baseline is ambiguous")

    origin = git(["remote", "get-url", "origin"], cwd=live_vault).strip()
    if github_repository not in origin:
        raise CheckFailure(
            f"the vault's origin does not name {github_repository}; refusing to verify against a "
            "repository the vault does not actually push to"
        )

    head_paths = tracked_paths(live_vault, live_head)
    base_paths = [
        rel for rel in tracked_paths(live_vault, baseline) if rel not in manifest_exclusions
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
        git(["clone", "--no-hardlinks", "--quiet", str(live_vault), str(local)], cwd=root)
        git(["clone", "--quiet", origin, str(remote)], cwd=root, timeout=clone_timeout)

        for name, clone in (("local", local), ("github", remote)):
            got = git(["rev-parse", "HEAD"], cwd=clone).strip()
            if got != live_head:
                raise CheckFailure(
                    f"the {name} restore is at {got[:12]} but the live vault is at "
                    f"{live_head[:12]}"
                )

        manifests = {
            "live vault": canonical_manifest(live_vault, head_paths),
            "local Git restore": canonical_manifest(local, head_paths),
            "GitHub restore": canonical_manifest(remote, head_paths),
        }
        require_identical("HEAD", manifests)
        head_digest = hashlib.sha256(manifests["live vault"]).hexdigest()

        for clone in (local, remote):
            git(["checkout", "--quiet", "--detach", baseline], cwd=clone)

        baseline_manifests = {
            "local Git restore": canonical_manifest(local, base_paths),
            "GitHub restore": canonical_manifest(remote, base_paths),
            "sealed archive": canonical_manifest(archive, base_paths),
        }
        require_identical("the pre-migration baseline", baseline_manifests)
        base_digest = hashlib.sha256(baseline_manifests["sealed archive"]).hexdigest()

    return (
        f"HEAD {live_head[:12]}: {len(head_paths)} paths identical across live vault, "
        f"local Git and GitHub (manifest {head_digest[:12]}); baseline {baseline[:12]}: "
        f"{len(base_paths)} paths identical across local Git, GitHub and the sealed "
        f"archive (manifest {base_digest[:12]})"
    )
