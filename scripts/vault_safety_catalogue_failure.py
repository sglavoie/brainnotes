"""Scenarios for the gates where something goes wrong and has to fail closed.

A snapshot that fails after the bytes land, a deletion no journal record
explains, a crash mid-transaction, a remote that disappears: each of these
gates asserts what the engine does *instead* of committing, and that what it
writes about the failure never carries a note body.
"""

from __future__ import annotations

from vault_safety_policy import (
    BLOCKED_MARKER_KEYS,
    BOOL,
    FLOAT,
    INT,
    MAXIMUM_LATENCY_SECONDS_POLICY,
    OBSERVATION_SLACK_SECONDS,
    PUSH_FAILURE_STREAK_THRESHOLD,
    PUSH_INTERVAL_SECONDS_POLICY,
    STRINGS,
    TEXT,
)
from vault_safety_scenario_model import Relation, Require, Scenario, drill_commit

FAILURE_SCENARIOS = (
    Scenario(
        name="artifacts_contain_no_note_bodies",
        summary="no artifact class carries a note body, and paths are still recorded",
        required_sections=(
            "act",
            "observe",
            "paths are present, which is the deliberate other half of the contract",
        ),
        requires=(
            Require("secret_body_written_into_two_drill_notes", BOOL, equals=True),
            Require("artifact_classes_produced", TEXT),
            Require("git commit messages -- contains the note body", BOOL, equals=False),
            Require("guard structured log -- contains the note body", BOOL, equals=False),
            Require("safety marker blocked.json -- contains the note body", BOOL, equals=False),
            Require("safety marker watch.json -- contains the note body", BOOL, equals=False),
            Require("undo journal manifests -- contains the note body", BOOL, equals=False),
            Require("reconstructed notification body -- contains the note body", BOOL, equals=False),
            Require("reconstructed notification body -- names any path", BOOL, equals=False),
            Require("undo journal manifests -- bytes", INT, minimum=1),
            Require("blocked.json top-level keys", STRINGS, equals=BLOCKED_MARKER_KEYS),
            Require("marker records the affected path count", INT, minimum=1),
            Require("marker records paths (drill-owned, listed by count only)", BOOL, equals=True),
        ),
    ),
    Scenario(
        name="commit_and_marker_failure_blocks_after_restart",
        summary="a failed commit whose block marker also failed still refuses the next write",
        required_sections=(
            "act",
            "observe: the marker never landed, the intent did",
            "the filesystem is healthy again; the manager restarts",
        ),
        requires=(
            Require("attempts_needed_to_hit_the_window", INT, minimum=1),
            Require("action", TEXT),
            Require("exit_status", INT, equals=1),
            Require("blocked_marker_is_still_the_planted_placeholder", BOOL, equals=True),
            Require("intent_record_present", BOOL, equals=True),
            Require("intent_phase", TEXT, equals="planned"),
            Require("intent_path_count", INT, minimum=1),
            Require("intent_names_a_commit", BOOL, equals=False),
            Require("intent_recovery_present", BOOL, equals=True),
            Require("intent_operation", TEXT, equals="commit-journal"),
            Require("marker_placeholder_removed", BOOL, equals=True),
            Require("later_write_exit_status", INT, equals=1),
            Require("refusal_names_an_interrupted_snapshot", BOOL, equals=True),
            Require("later_write_left_target_untouched", BOOL, equals=True),
            Require("bytes_of_the_first_write_are_on_disk", BOOL, equals=True),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="interrupted_delete_recovered_by_watcher",
        summary="a rename interrupted before its commit is finished by the watcher, not blocked",
        required_sections=("act", "observe: the watcher finishes it rather than blocking"),
        requires=(
            Require("action", TEXT),
            Require("commits_after_reset", INT, minimum=0),
            Require("working_tree_entries_pending", INT, minimum=1),
            Require("old_path_present", BOOL, equals=False),
            Require("new_path_present", BOOL, equals=True),
            Require("recovery_commit_landed", BOOL, equals=True),
            Require(
                "seconds_to_recovery_commit",
                FLOAT,
                minimum=0.0,
                maximum=MAXIMUM_LATENCY_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            Require("blocked_marker_written", BOOL, equals=False),
            Require("deletion_of_old_path_committed", BOOL, equals=True),
            *drill_commit(changed=4),
        ),
        relations=(Relation("len(drill_owned_paths)", "==", ("changed_paths",)),),
    ),
    Scenario(
        name="local_commit_failure_blocks_later_writes",
        summary="a snapshot that fails after the bytes land blocks and refuses the next write",
        required_sections=(
            "act",
            "observe",
            "the next write is refused even though Git is healthy again",
        ),
        requires=(
            Require("action", TEXT),
            Require("exit_status", INT, equals=1),
            Require("error_says_change_is_on_disk", BOOL, equals=True),
            Require("error_says_writes_are_blocked", BOOL, equals=True),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=0),
            Require("bytes_landed_on_disk", BOOL, equals=True),
            Require("blocked_marker_written", BOOL, equals=True),
            Require("marker_kind", TEXT, equals="commit_failed"),
            Require("marker_operation", TEXT, equals="commit-journal"),
            Require("marker_path_count", INT, minimum=0),
            Require("later_write_exit_status", INT, equals=1),
            Require("later_write_left_target_untouched", BOOL, equals=True),
        ),
        # The commit is the thing that failed, so the history must not have
        # moved. A narrative claiming both a failure and a new commit is
        # describing two different runs.
        relations=(Relation("commits_after", "==", ("commits_before",)),),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="remote_outage_recovers_automatically",
        summary="an outage never blocks a local mutation and the backlog pushes itself",
        required_sections=("act: take the remote away", "observe: restore the remote and wait"),
        requires=(
            Require("local_mutation_exit_status", INT, equals=0),
            Require("local_commit_landed_during_outage", BOOL, equals=True),
            Require("bytes_available_during_outage", BOOL, equals=True),
            Require("external_edit_rounds_during_outage", INT, minimum=1),
            Require("recorded_push_attempts_during_outage", INT, minimum=1),
            Require("guard_push_failed_warnings", INT, minimum=1),
            Require("guard_push_failing_streak_notifications", INT, minimum=1),
            Require("streak_notification_threshold", INT, equals=PUSH_FAILURE_STREAK_THRESHOLD),
            Require("local_commits_kept_being_taken_during_the_outage", INT, minimum=1),
            Require("guard_still_running", BOOL, equals=True),
            Require(
                "seconds_to_automatic_push_after_recovery",
                FLOAT,
                minimum=0.0,
                maximum=PUSH_INTERVAL_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            Require("remote_ref_matches_local_head", BOOL, equals=True),
            Require("push_retry_state_cleared", BOOL, equals=True),
        ),
        relations=(
            # The streak notification is a threshold, so the warnings it counted
            # have to have actually happened.
            Relation("guard_push_failed_warnings", ">=", ("streak_notification_threshold",)),
        ),
    ),
    Scenario(
        name="txn_crash_converges_and_is_idempotent",
        summary="every durable transaction phase converges on reopen and stays converged",
        # One section per documented crash point. A drill that stopped
        # exercising one would still hash cleanly, so the sections are the check.
        required_sections=(
            "crash point intent -- record durable, no destination reached yet",
            "crash point applied:0 -- first destination reached, second still holding prior bytes",
            "crash point synced -- every destination reached, record not yet cleared",
        ),
        requires=(
            Require("intent_record_present_before_reopen", BOOL, equals=True, count=3),
            Require("reopen_exit_status", INT, equals=0, count=3),
            Require("both_destinations_hold_planned_bytes", BOOL, equals=True, count=3),
            Require("intent_record_cleared", BOOL, equals=True, count=3),
            Require("second_reopen_exit_status", INT, equals=0, count=3),
            Require("still_holds_planned_bytes_after_second_reopen", BOOL, equals=True, count=3),
        ),
    ),
    Scenario(
        name="unexplained_deletion_blocks",
        summary="a deletion with no journal record blocks and is never committed or pushed",
        required_sections=("act", "observe", "a later bn write is refused while the block stands"),
        requires=(
            Require("action", TEXT),
            Require("blocked_marker_appeared", BOOL, equals=True),
            Require(
                "seconds_to_block",
                FLOAT,
                minimum=0.0,
                maximum=OBSERVATION_SLACK_SECONDS,
            ),
            Require("marker_kind", TEXT, equals="unexplained_deletion"),
            Require("marker_operation", TEXT, equals="scan-pending"),
            Require("marker_reason", TEXT),
            Require("marker_path_count", INT, minimum=1),
            Require("marker_recovery_present", BOOL, equals=True),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=0),
            Require("origin_ref_moved", BOOL, equals=False),
            Require("note_still_recoverable_from_HEAD", BOOL, equals=True),
            Require("later_write_exit_status", INT, equals=1),
            Require("guard_blocked_events", INT, minimum=1),
        ),
        # "never committed" is the property; a narrative that reported a block
        # and a new commit in the same breath has not shown it.
        relations=(Relation("commits_after", "==", ("commits_before",)),),
    ),
)
