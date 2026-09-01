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

1. Record shape and completeness. The record must carry every gate named by the
   repository-owned scenario catalogue in `scripts/vault_safety_scenarios.py`
   and no others, and must have been produced under the catalogue version now
   in force. Every gate carries an ISO-8601 `checked_at`, a repository-relative
   `evidence_path` under `docs/operations/vault-safety-evidence/`, a 64-hex
   `evidence_sha256`, and `paths_redacted: true`. Absolute paths and paths that
   escape the evidence directory are rejected before anything is opened.
2. Evidence integrity. Every `evidence_sha256` is recomputed from the retained
   bytes on disk.
3. The scenario each gate is supposed to have exercised. The retained narrative
   is parsed, and the gate's verdict is re-decided here from its decisive
   observations, the arithmetic between them, the sections that have to be
   present, and the drill-owned paths it names. A gate does not pass because it
   says it passed; it passes because its own evidence still shows what the
   catalogue says that scenario has to show.
4. Recorded claims, only where the evidence substantiates them. `outcome` is
   accepted only against a narrative whose re-derived verdict is a pass;
   `checked_at` must be the narrative's own timestamp; and `exit_status` is
   accepted only when the scenario names an observation that carries it, and
   must equal it. A gate whose narrative records no exit status may not claim
   one. Values that several gates observed independently — the restore
   manifests, the tracked path counts, the baseline commit — must agree.
5. Redaction, derived rather than asserted. `paths_redacted: true` is checked by
   taking the vault's own tracked path set and proving that no note path and no
   note filename appears in any retained evidence file. A gate cannot claim to
   be redacted; it has to be.
6. Three-way restore identity. Fresh temporary restores are made from the local
   Git repository and from GitHub, and canonical SHA-256 manifests are computed
   over the exact tracked vault paths. At HEAD, the two restores and the live
   working tree must be byte-for-byte identical. At the pre-migration baseline
   commit, the two restores and the sealed archive must be byte-for-byte
   identical.
7. Live state. The installed health command must report a clean, pushed,
   unblocked, supervised vault — and then each of those is checked again here
   directly, from `git status`, from the safety state directory, from
   `git ls-remote`, and from `launchctl`.
8. Retirement. The old File Provider root must be gone, its recorded Trash
   location must still exist, and every file in the trashed copy must be
   byte-present either in the live vault's Git object database or in the sealed
   archive — so that retiring it demonstrably lost nothing.

This module is the driver: argument parsing, the order the checks run in, and
the report. Each check itself lives in a sibling module — `vault_safety_gitio`
(the readers everything else re-derives from), `vault_safety_record` (record
shape, scenario re-derivation, redaction), `vault_safety_restores`,
`vault_safety_livestate`, and `vault_safety_retirement`.

Exit status is 0 only when every check passes. Any failure prints the reason and
exits 1; a harness that cannot run its own checks exits 2.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path

import vault_safety_livestate as livestate
import vault_safety_retirement as retirement
import vault_safety_scenarios as scenarios
from vault_safety_gitio import CheckFailure, HarnessError
from vault_safety_record import (
    EVIDENCE_DIR_NAME,
    GateEvidence,
    validate_gates,
    validate_recorded_claims,
    validate_scenarios,
    verify_no_orphan_evidence,
    verify_redaction,
)
from vault_safety_restores import verify_restores

REPO_ROOT = Path(__file__).resolve().parents[1]
SCHEMA_VERSION = 2

# The manifest scope the sealed archive is compared over. It lives in the
# scenario catalogue because the restore gates record it too, so the exclusion
# this harness applies and the one those drills applied are the same list.
ARCHIVE_MANIFEST_EXCLUSIONS = scenarios.ARCHIVE_MANIFEST_EXCLUSIONS


class Harness:
    def __init__(self, args: argparse.Namespace) -> None:
        self.args = args
        self.live_vault = Path(args.live_vault)
        self.archive = Path(args.sealed_archive)
        self.old_root = Path(args.old_root)
        self.repo_root = REPO_ROOT
        self.evidence_file = (REPO_ROOT / args.evidence).resolve()
        self.evidence_root = (REPO_ROOT / EVIDENCE_DIR_NAME).resolve()
        self.results: list[tuple[str, bool, str]] = []
        self.record: dict = {}
        self.gates: dict[str, GateEvidence] = {}
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
        # Tightening a scenario bumps the catalogue, which is what stops an old
        # record from being accepted against a definition it was never run under.
        if record.get("scenario_catalogue_version") != scenarios.CATALOGUE_VERSION:
            raise CheckFailure(
                "the gates were recorded against scenario catalogue version "
                f"{record.get('scenario_catalogue_version')!r}, but version "
                f"{scenarios.CATALOGUE_VERSION} is in force; they have to be run again"
            )
        gates = record.get("gates")
        if not isinstance(gates, dict) or not gates:
            raise CheckFailure("evidence record must carry a non-empty gates object")
        self.record = record
        return (
            f"schema version {SCHEMA_VERSION}, scenario catalogue version "
            f"{scenarios.CATALOGUE_VERSION}, {len(gates)} gate(s)"
        )

    def verify_gates(self) -> str:
        self.gates = validate_gates(self.record, self.repo_root, self.evidence_root)
        self.evidence_files = {name: found.path for name, found in self.gates.items()}
        return f"{len(self.gates)} gate record(s) validated and re-hashed"

    def verify_scenarios(self) -> str:
        return validate_scenarios(self.gates)

    def verify_recorded_claims(self) -> str:
        return validate_recorded_claims(self.gates)

    def verify_no_orphan_evidence(self) -> str:
        return verify_no_orphan_evidence(self.evidence_root, self.evidence_files)

    # -- redaction --------------------------------------------------------

    def verify_redaction(self) -> str:
        return verify_redaction(self.live_vault, self.evidence_files)

    # -- restores ---------------------------------------------------------

    def verify_restores(self) -> str:
        return verify_restores(
            live_vault=self.live_vault,
            archive=self.archive,
            github_repository=self.args.github_repository,
            manifest_exclusions=ARCHIVE_MANIFEST_EXCLUSIONS,
            clone_timeout=self.args.clone_timeout,
        )

    # -- live state -------------------------------------------------------

    def verify_health_command(self) -> str:
        return livestate.verify_health_command(
            command=Path(self.args.health_command),
            live_vault=self.live_vault,
            launchagent_label=self.args.launchagent_label,
        )

    def verify_live_vault_directly(self) -> str:
        return livestate.verify_live_vault_directly(self.live_vault)

    def verify_launchagent(self) -> str:
        return livestate.verify_launchagent(
            launchagent_label=self.args.launchagent_label, live_vault=self.live_vault
        )

    # -- retirement -------------------------------------------------------

    def verify_retirement(self) -> str:
        return retirement.verify_retirement(
            record=self.record, old_root=self.old_root, archive=self.archive
        )

    def verify_trashed_copy_is_fully_accounted(self) -> str:
        return retirement.verify_trashed_copy_is_fully_accounted(
            record=self.record, live_vault=self.live_vault, archive=self.archive
        )

    def verify_no_process_uses_the_old_root(self) -> str:
        return retirement.verify_no_process_uses_the_old_root(
            old_root=self.old_root, harness_script_name=Path(__file__).name
        )

    def verify_config_points_only_at_the_live_vault(self) -> str:
        return retirement.verify_config_points_only_at_the_live_vault(
            live_vault=self.live_vault, old_root=self.old_root
        )

    # -- driver -----------------------------------------------------------

    def run_all(self) -> int:
        self.check("evidence-record-loads", self.load_record)
        if not self.results[-1][1]:
            self.report()
            return 1

        self.check("gate-records-and-hashes", self.verify_gates)
        if not self.results[-1][1]:
            # Everything below reads the retained evidence this check is what
            # proves is present, intact, and parseable. Without it they would
            # each pass over an empty set and report a clean run.
            self.report()
            return 1

        self.check("scenario-observations-hold", self.verify_scenarios)
        self.check("recorded-claims-substantiated", self.verify_recorded_claims)
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
