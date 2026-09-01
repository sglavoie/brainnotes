"""Scenarios for the gates where a change is supposed to be committed.

Every gate here asserts the same shape from a different entry point: the bytes
landed, exactly one commit followed, it carried the drill's own paths and
nothing else, and — where the schedule rather than the write is the property
under test — it happened inside the window the policy allows.
"""

from __future__ import annotations

from vault_safety_policy import (
    AUTOSAVE_FLOOR_SECONDS_POLICY,
    AUTOSAVE_FLOOR_TOKEN_POLICY,
    BOOL,
    DRILL_PATH,
    FLOAT,
    INT,
    MAXIMUM_LATENCY_SECONDS_POLICY,
    OBSERVATION_SLACK_SECONDS,
    OID,
    PATHS,
    QUIET_SECONDS_POLICY,
    TEXT,
)
from vault_safety_scenario_model import Relation, Require, Scenario, drill_commit

MUTATION_SCENARIOS = (
    Scenario(
        name="cli_mutation_commits_immediately",
        summary="a CLI note mutation commits its own paths before it returns",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=1),
            Require("working_tree_entries_left_dirty", INT, equals=0),
            Require("frontmatter_applied", BOOL, equals=True),
            *drill_commit(changed=3),
        ),
        relations=(
            Relation("commits_after", "==", ("commits_before",), delta=1),
            Relation("len(drill_owned_paths)", "==", ("changed_paths",)),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="debounce_survives_daemon_restart",
        summary="killing the daemon during a debounce cannot postpone the snapshot",
        requires=(
            Require("persisted_first_change_at_recorded", BOOL, equals=True),
            Require("daemon_kills_during_debounce", INT, minimum=1),
            Require("first_change_at_unchanged_across_restarts", BOOL, equals=True),
            Require(
                "seconds_from_first_change_to_commit",
                FLOAT,
                minimum=0.0,
                maximum=MAXIMUM_LATENCY_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            *drill_commit(changed=1),
        ),
        relations=(Relation("len(drill_owned_paths)", "==", ("changed_paths",)),),
    ),
    Scenario(
        name="durable_state_commits_immediately",
        summary="a durable sidecar-state write commits that path and nothing else",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=1),
            Require("staged_exactly", TEXT, equals=".bnotes/recall.json"),
            *drill_commit(changed=1),
        ),
        relations=(
            Relation("commits_after", "==", ("commits_before",), delta=1),
            Relation("len(drill_owned_paths)", "==", ("changed_paths",)),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="external_edit_scheduled_by_readiness_and_floor",
        summary="an external edit commits once it is both ready and past the autosave floor",
        # Two sections because the schedule is two independent conditions, and a
        # drill that measured only the first would still look like a passing
        # timing gate. `due_at = max(ready_at, floor_at)`: the first section
        # holds `floor_at` out of the way and measures readiness on its own; the
        # second makes the change ready early and measures the floor deferring
        # it. Neither section on its own distinguishes the two.
        required_sections=(
            "readiness alone: the first external edit after a quiet history",
            "the floor: an edit that is ready long before the floor allows a commit",
        ),
        requires=(
            # The policy each section ran under, recorded from the guard's own
            # published record rather than from the drill's intent, so a floor
            # that never took effect cannot be reported as one that did.
            Require("quiet_seconds_policy", INT, equals=QUIET_SECONDS_POLICY, count=2),
            Require(
                "maximum_latency_seconds_policy",
                INT,
                equals=MAXIMUM_LATENCY_SECONDS_POLICY,
                count=2,
            ),
            Require(
                "autosave_floor_seconds_policy",
                INT,
                equals=AUTOSAVE_FLOOR_SECONDS_POLICY,
                count=2,
            ),
            Require(
                "published_autosave_min_interval",
                TEXT,
                equals=AUTOSAVE_FLOOR_TOKEN_POLICY,
                count=2,
            ),
            Require("external_writes_during_burst", INT, minimum=1, count=2),
            Require("commit_landed", BOOL, equals=True, count=2),
            # Section one. With no previous external autosave the floor has
            # nothing to be measured from, so readiness alone decides and the
            # commit lands inside the window the fixed constants define.
            Require("previous_external_autosave_recorded", BOOL, equals=False),
            Require(
                "seconds_from_first_change_to_commit",
                FLOAT,
                minimum=QUIET_SECONDS_POLICY,
                maximum=MAXIMUM_LATENCY_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            # Section two, and the decisive half of the gate. The change is
            # ready within 30 seconds and would have been committed within two
            # minutes under readiness alone; the floor pushes the published due
            # time past that ceiling, and the commit lands where the due time
            # said it would rather than where readiness did.
            Require("due_time_was_deferred_past_readiness", BOOL, equals=True),
            Require(
                "seconds_from_first_change_to_published_due",
                FLOAT,
                minimum=MAXIMUM_LATENCY_SECONDS_POLICY,
                maximum=AUTOSAVE_FLOOR_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            Require(
                "seconds_from_previous_autosave_to_published_due",
                FLOAT,
                minimum=AUTOSAVE_FLOOR_SECONDS_POLICY,
                maximum=AUTOSAVE_FLOOR_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            Require(
                "seconds_from_first_change_to_deferred_commit",
                FLOAT,
                minimum=MAXIMUM_LATENCY_SECONDS_POLICY,
                maximum=AUTOSAVE_FLOOR_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            Require(
                "seconds_from_previous_autosave_to_deferred_commit",
                FLOAT,
                minimum=AUTOSAVE_FLOOR_SECONDS_POLICY,
                maximum=AUTOSAVE_FLOOR_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            # A floor that deferred nothing because the guard committed twice in
            # between would satisfy every duration above. This is what says the
            # wait was one wait.
            Require("commits_between_the_two_autosaves", INT, equals=0),
            Require("commit", OID, count=2),
            Require("subject", TEXT, count=2),
            Require("changed_paths", INT, equals=1, count=2),
            Require("drill_owned_paths", PATHS, each_matches=DRILL_PATH, count=2),
            Require("unrelated_paths_swept_in", INT, equals=0, count=2),
        ),
        relations=(Relation("len(drill_owned_paths)", "==", ("changed_paths",)),),
    ),
    Scenario(
        name="history_restore_commits_immediately",
        summary="an undo of a journal entry commits its own paths",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=1),
            Require("restored_frontmatter_removed", BOOL, equals=True),
            *drill_commit(changed=3),
        ),
        relations=(
            Relation("commits_after", "==", ("commits_before",), delta=1),
            Relation("len(drill_owned_paths)", "==", ("changed_paths",)),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="web_mutation_commits_immediately",
        summary="a mutation through the web API commits its own paths",
        requires=(
            Require("request", TEXT),
            Require("http_status", INT, equals=200),
            Require("api_message", TEXT),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=1),
            Require("stub_note_created", BOOL, equals=True),
            Require("working_tree_entries_left_dirty", INT, equals=0),
            *drill_commit(changed=2),
        ),
        relations=(
            Relation("commits_after", "==", ("commits_before",), delta=1),
            Relation("len(drill_owned_paths)", "==", ("changed_paths",)),
        ),
    ),
)
