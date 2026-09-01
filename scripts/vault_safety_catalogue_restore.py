"""Scenarios for the gates that prove the vault can be rebuilt, and retired.

Three independent sources — the local Git history, the content remote, and the
sealed pre-migration archive — must reproduce the same bytes, and the old File
Provider root must be gone without having taken anything with it. The suite
gates that run the project's own checks live here too: they are the ones that
say the build the drills ran against was itself healthy.
"""

from __future__ import annotations

import re

from vault_safety_policy import (
    ARCHIVE_MANIFEST_EXCLUSIONS,
    BOOL,
    HEX64,
    INT,
    OID,
    STRINGS,
    TEXT,
)
from vault_safety_scenario_model import Relation, Require, Scenario

RESTORE_SCENARIOS = (
    Scenario(
        name="bn_doctor",
        summary="the vault hygiene check runs clean against the live vault",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("output_lines", INT, minimum=1),
            Require("stderr_empty", BOOL, equals=True),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="brainnotes_ci",
        summary="the full BrainNotes validation envelope passes",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="fresh_clone_of_content_remote",
        summary="a fresh clone of the content remote is complete and at the live head",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("clone_HEAD", OID),
            Require("live_HEAD", OID),
            Require("clone_is_at_the_live_head", BOOL, equals=True),
            Require("tracked_paths_in_the_clone", INT, minimum=1),
            Require("clone_is_clean", BOOL, equals=True),
            Require("git_fsck_exit_status", INT, equals=0),
        ),
        same_value=(("clone_HEAD", "live_HEAD"),),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="old_vault_trashed_recoverably",
        summary="the old File Provider root is out of use, recoverable, and lost nothing",
        required_sections=(
            "before: nothing is still using the old root",
            "nothing in the old root is only in the old root",
            "the sealed archive is verified one last time, then the old root is moved",
            "after: the trashed copy is byte-for-byte what was moved",
        ),
        requires=(
            Require("processes_referencing_the_old_root", INT, equals=0),
            Require("bn_config_exit_status", INT, equals=0),
            Require("bn_config_names_the_old_root", BOOL, equals=False),
            Require("bn_config_names_the_live_vault", BOOL, equals=True),
            Require("launchagent_names_the_old_root", BOOL, equals=False),
            Require("launchagent_names_the_live_vault", BOOL, equals=True),
            Require("files_in_the_old_root", INT, minimum=1),
            Require("files_byte_present_in_the_live_history_or_the_sealed_archive", INT, minimum=1),
            Require("machinery_files_classified_by_path", INT, minimum=0),
            Require("unaccounted_files", INT, equals=0),
            Require(
                "approved_old_vault_manifest_sha256_matches_the_migration_record",
                BOOL,
                equals=True,
            ),
            Require("old_root_still_in_place", BOOL, equals=False),
            Require("trash_location", TEXT),
            Require("trash_location_is_a_directory", BOOL, equals=True),
            Require("files_before_the_move", INT, minimum=1),
            Require("files_after_the_move", INT, minimum=1),
            Require("object_names_identical_after_the_move", BOOL, equals=True),
            Require("sealed_archive_still_present", BOOL, equals=True),
        ),
        relations=(
            # Every file is accounted for by exactly one class, so the census
            # has to add up rather than merely report a zero at the end.
            Relation(
                "files_in_the_old_root",
                "==",
                (
                    "files_byte_present_in_the_live_history_or_the_sealed_archive",
                    "machinery_files_classified_by_path",
                    "unaccounted_files",
                ),
            ),
            Relation("files_after_the_move", "==", ("files_before_the_move",)),
            Relation("files_before_the_move", "==", ("files_in_the_old_root",)),
        ),
    ),
    Scenario(
        name="post_retirement_verification",
        summary="health, push, clone and both restore manifests still hold after retirement",
        required_sections=("health", "push", "fresh clone and restore manifests"),
        requires=(
            Require("health_command_exit_status", INT, equals=0),
            Require("health_verdict", TEXT, equals="OK"),
            Require("push_exit_status", INT, equals=0),
            Require("remote_tip_matches_local_head", BOOL, equals=True),
            Require("fresh_clone_exit_status", INT, equals=0),
            Require("tracked_paths_at_HEAD", INT, minimum=1),
            Require("live_vault_manifest_sha256", HEX64),
            Require("fresh_clone_manifest_sha256", HEX64),
            Require("identical", BOOL, equals=True),
            Require("baseline_clone_manifest_sha256", HEX64),
            Require("sealed_archive_manifest_sha256", HEX64),
            Require("identical_at_baseline", BOOL, equals=True),
        ),
        same_value=(
            ("live_vault_manifest_sha256", "fresh_clone_manifest_sha256"),
            ("baseline_clone_manifest_sha256", "sealed_archive_manifest_sha256"),
        ),
    ),
    Scenario(
        name="restore_from_github_clone",
        summary="a fresh clone of the content remote reproduces the live vault exactly",
        requires=(
            Require("method", TEXT),
            Require("remote", TEXT),
            Require("tracked_paths_at_HEAD", INT, minimum=1),
            Require("tracked_paths_at_baseline", INT, minimum=1),
            Require("baseline_commit", OID),
            Require(
                "paths_excluded_from_the_archive_comparison",
                STRINGS,
                equals=sorted(ARCHIVE_MANIFEST_EXCLUSIONS),
            ),
            Require("live_vault_manifest_sha256", HEX64),
            Require("github_restore_manifest_sha256", HEX64),
            Require("identical_to_live_vault", BOOL, equals=True),
            Require("identical_to_the_local_git_restore", BOOL, equals=True),
        ),
        same_value=(("live_vault_manifest_sha256", "github_restore_manifest_sha256"),),
    ),
    Scenario(
        name="restore_from_local_git",
        summary="a fresh restore from the local history reproduces the live vault exactly",
        requires=(
            Require("method", TEXT),
            Require("tracked_paths_at_HEAD", INT, minimum=1),
            Require("tracked_paths_at_baseline", INT, minimum=1),
            Require("baseline_commit", OID),
            Require(
                "paths_excluded_from_the_archive_comparison",
                STRINGS,
                equals=sorted(ARCHIVE_MANIFEST_EXCLUSIONS),
            ),
            Require("live_vault_manifest_sha256", HEX64),
            Require("local_restore_manifest_sha256", HEX64),
            Require("identical_to_live_vault", BOOL, equals=True),
        ),
        same_value=(("live_vault_manifest_sha256", "local_restore_manifest_sha256"),),
    ),
    Scenario(
        name="restore_from_sealed_archive",
        summary="the sealed archive reproduces the vault's baseline commit exactly",
        required_sections=("act", "observe", "the archive's own SHA256SUMS still verifies"),
        requires=(
            Require("method", TEXT),
            Require("archive_is_destination_agnostic", BOOL, equals=True),
            Require("tracked_paths_at_HEAD", INT, minimum=1),
            Require("tracked_paths_at_baseline", INT, minimum=1),
            Require("baseline_commit", OID),
            Require(
                "paths_excluded_from_the_archive_comparison",
                STRINGS,
                equals=sorted(ARCHIVE_MANIFEST_EXCLUSIONS),
            ),
            Require("baseline_local_restore_manifest_sha256", HEX64),
            Require("baseline_github_restore_manifest_sha256", HEX64),
            Require("sealed_archive_manifest_sha256", HEX64),
            Require("all_three_identical", BOOL, equals=True),
            Require("shasum_c_exit_status", INT, equals=0),
        ),
        same_value=(
            (
                "baseline_local_restore_manifest_sha256",
                "baseline_github_restore_manifest_sha256",
                "sealed_archive_manifest_sha256",
            ),
        ),
    ),
    Scenario(
        name="scripts_complete_suite",
        summary="the automation repository's complete suite passes, guard module included",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("brainnotes guard shard result", TEXT, each_matches=re.compile(r"\Aok\b")),
        ),
        exit_status_key="exit_status",
    ),
)
