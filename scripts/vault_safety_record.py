"""Everything the harness checks about the evidence record and its files.

Shape, integrity, the scenario each gate is supposed to have exercised, the
claims the record makes about itself, and the redaction it asserts. The
deliberate split inside this module is between *shape* and *verdict*:
`validate_gate` proves a narrative is present, intact and parseable and stops
there, and `validate_scenarios` / `validate_recorded_claims` are what re-decide
whether the gate held. A record is never allowed to be the reason a gate passes.
"""

from __future__ import annotations

import os
import re
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path, PurePosixPath

import vault_safety_scenarios as scenarios
from vault_safety_gitio import CheckFailure, git, sha256_file, tracked_paths

EVIDENCE_DIR_NAME = "docs/operations/vault-safety-evidence"
HEX64 = re.compile(r"\A[0-9a-f]{64}\Z")

# Tracked paths that are repository or BrainNotes machinery rather than note
# content. These are allowed to appear verbatim in a retained evidence file: an
# evidence record that could not name `blocked.json` or `brainnotes.toml` would
# be unreadable, and neither one reveals anything about what the user writes.
STRUCTURAL_PREFIXES = (".bnotes/",)
STRUCTURAL_PATHS = frozenset({".gitignore", "brainnotes.toml"})

# A basename shorter than this is not distinctive enough to be worth scanning
# for: "ok.md" or "b.md" would collide with ordinary English in an evidence
# narrative and turn the redaction check into noise. Full relative paths are
# checked at every length, which is what a real leak would look like.
MIN_SCANNED_BASENAME = 8


@dataclass(frozen=True)
class GateEvidence:
    """A gate's record, the retained file it names, and that file read back."""

    name: str
    gate: dict
    path: Path
    parsed: scenarios.ParsedEvidence


def validate_evidence_path(raw: object, repo_root: Path, evidence_root: Path) -> Path:
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
    resolved = (repo_root / raw).resolve()
    if not str(resolved).startswith(str(evidence_root.resolve()) + os.sep):
        raise CheckFailure(f"evidence_path resolves outside the evidence directory: {raw}")
    return resolved


def validate_gate(name: str, gate: object, repo_root: Path, evidence_root: Path) -> GateEvidence:
    """Shape, integrity, and readability. Deliberately not the gate's verdict.

    Nothing here decides whether the gate passed: `outcome` and `exit_status`
    are claims, and they are judged in `validate_recorded_claims` against the
    narrative this function only proves is intact and parseable.
    """
    if name not in scenarios.SCENARIOS:
        raise CheckFailure(
            f"gate {name}: no scenario of that name exists in the repository-owned catalogue, "
            "so nothing defines what it is supposed to have shown"
        )
    if not isinstance(gate, dict):
        raise CheckFailure(f"gate {name} must be an object")
    if gate.get("paths_redacted") is not True:
        raise CheckFailure(f"gate {name}: paths_redacted is not true")
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

    path = validate_evidence_path(gate.get("evidence_path"), repo_root, evidence_root)
    if not path.is_file():
        raise CheckFailure(f"gate {name}: evidence file is missing: {gate.get('evidence_path')}")
    actual = sha256_file(path)
    if actual != digest:
        raise CheckFailure(
            f"gate {name}: evidence file does not hash to its recorded digest "
            f"(recorded {digest}, actual {actual})"
        )
    try:
        parsed = scenarios.parse_evidence(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, scenarios.EvidenceFormatError) as exc:
        raise CheckFailure(f"gate {name}: evidence file cannot be read as a gate record: {exc}")
    return GateEvidence(name=name, gate=gate, path=path, parsed=parsed)


def validate_gates(record: dict, repo_root: Path, evidence_root: Path) -> dict[str, GateEvidence]:
    """The whole gate set, not merely the gates someone chose to record.

    A missing gate is the cheapest possible way to make this harness pass: drop
    the one that stopped holding and every remaining record still hashes. The
    required set is therefore the catalogue's, not the record's.
    """
    gates = record["gates"]
    missing = sorted(scenarios.REQUIRED_GATES - set(gates))
    if missing:
        raise CheckFailure(f"the record is missing required gate(s): {', '.join(missing)}")
    return {name: validate_gate(name, gates[name], repo_root, evidence_root) for name in sorted(gates)}


def validate_scenarios(gates: dict[str, GateEvidence]) -> str:
    """Re-decide every gate from its own narrative."""
    failures: list[str] = []
    for name, evidence in sorted(gates.items()):
        for problem in scenarios.validate_evidence(scenarios.SCENARIOS[name], evidence.parsed):
            failures.append(f"gate {name}: {problem}")
    if failures:
        raise CheckFailure("; ".join(failures))
    observations = sum(len(found) for e in gates.values() for found in e.parsed.observations.values())
    return f"{len(gates)} scenario(s) re-derived from {observations} recorded observation(s)"


def validate_recorded_claims(gates: dict[str, GateEvidence]) -> str:
    """Every claim in the record is either substantiated by its evidence or refused."""
    failures: list[str] = []
    for name, evidence in sorted(gates.items()):
        scenario = scenarios.SCENARIOS[name]
        for problem in scenarios.unsupported_claims(scenario, evidence.gate, evidence.parsed):
            failures.append(f"gate {name}: {problem}")
    failures.extend(
        scenarios.cross_gate_disagreements({n: e.parsed for n, e in gates.items()})
    )
    if failures:
        raise CheckFailure("; ".join(failures))
    substantiated = sum(1 for name in gates if scenarios.SCENARIOS[name].exit_status_key)
    return (
        f"{len(gates)} outcome(s) and {substantiated} exit status(es) substantiated by their "
        f"own evidence; {len(scenarios.CROSS_GATE_AGREEMENTS)} cross-gate agreement(s) hold"
    )


def verify_no_orphan_evidence(evidence_root: Path, evidence_files: dict[str, Path]) -> str:
    """Every retained file belongs to a gate.

    An evidence file with no gate pointing at it is unhashed and unreviewed,
    and it is exactly where a file that failed its check would end up if
    someone quietly dropped the gate instead of fixing it.
    """
    if not evidence_root.is_dir():
        raise CheckFailure(f"evidence directory is missing: {EVIDENCE_DIR_NAME}")
    claimed = {path.resolve() for path in evidence_files.values()}
    found = {path.resolve() for path in evidence_root.rglob("*") if path.is_file()}
    orphans = sorted(str(path.relative_to(evidence_root)) for path in found - claimed)
    if orphans:
        raise CheckFailure(f"retained evidence with no gate record: {', '.join(orphans)}")
    return f"{len(found)} retained file(s), all claimed by a gate"


def verify_redaction(live_vault: Path, evidence_files: dict[str, Path]) -> str:
    """Prove `paths_redacted`, do not take its word for it."""
    head = git(["rev-parse", "HEAD"], cwd=live_vault).strip()
    needles: set[str] = set()
    for rel in tracked_paths(live_vault, head):
        if rel in STRUCTURAL_PATHS or rel.startswith(STRUCTURAL_PREFIXES):
            continue
        needles.add(rel)
        base = PurePosixPath(rel).name
        if len(base) >= MIN_SCANNED_BASENAME:
            needles.add(base)
    if not needles:
        raise CheckFailure("no tracked note paths found, so redaction cannot be proven")

    leaks: list[str] = []
    for name, path in sorted(evidence_files.items()):
        text = path.read_text(encoding="utf-8", errors="replace")
        for needle in needles:
            if needle in text:
                # Deliberately does not echo the needle: an error message that
                # printed the leaked filename would itself become the leak, in a
                # log nobody redacts.
                leaks.append(f"{name} contains a tracked note path")
                break
    if leaks:
        raise CheckFailure("; ".join(leaks))
    return f"{len(evidence_files)} evidence file(s) scanned against {len(needles)} note path(s)"
