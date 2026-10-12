"""Malformed gate-specific results.

Split from the former single module; the shared fixture is in
`vault_evidence_testkit.py`. Run with `python3 -m unittest discover -s scripts -p "test_verify_vault_safety*.py"`.
"""

from __future__ import annotations

import unittest

from vault_evidence_testkit import FLOOR_GATE, RecordTestCase



class TestMalformedGateSpecificResults(RecordTestCase):
    def test_a_non_numeric_count_is_rejected(self) -> None:
        self.replace_in_evidence(
            "cli_mutation_commits_immediately", "commits_after: 5", "commits_after: several"
        )
        with self.assertRejected("commits_after.*not an integer"):
            self.verify()

    def test_a_commit_count_that_does_not_add_up_is_rejected(self) -> None:
        # One mutation, one commit. A narrative claiming two has not shown the
        # property the gate is named for, even though every value in it is a
        # perfectly well-formed integer.
        self.replace_in_evidence(
            "cli_mutation_commits_immediately", "commits_after: 5", "commits_after: 6"
        )
        with self.assertRejected("commits_after is 6, but commits_before"):
            self.verify()

    def test_a_census_that_does_not_account_for_every_file_is_rejected(self) -> None:
        self.replace_in_evidence(
            "old_vault_trashed_recoverably", "machinery_files_classified_by_path: 7", "machinery_files_classified_by_path: 6"
        )
        with self.assertRejected("files_in_the_old_root is 1302"):
            self.verify()

    def test_a_marker_shape_that_is_not_the_engines_is_rejected(self) -> None:
        self.replace_in_evidence(
            "artifacts_contain_no_note_bodies",
            "'recovery', 'version']",
            "'version']",
        )
        with self.assertRejected("blocked.json top-level keys"):
            self.verify()

    def test_a_snapshot_later_than_the_policy_allows_is_rejected(self) -> None:
        self.replace_in_evidence(
            "debounce_survives_daemon_restart",
            "seconds_from_first_change_to_commit: 122.3",
            "seconds_from_first_change_to_commit: 1122.3",
        )
        with self.assertRejected("above the allowed"):
            self.verify()

    def test_a_policy_the_build_does_not_implement_is_rejected(self) -> None:
        self.replace_in_evidence(
            FLOOR_GATE,
            "maximum_latency_seconds_policy: 120",
            "maximum_latency_seconds_policy: 900",
        )
        with self.assertRejected("maximum_latency_seconds_policy"):
            self.verify()

    def test_a_floor_the_guard_was_not_enforcing_is_rejected(self) -> None:
        # The floor is configured rather than compiled in, so the gate records
        # the guard's own published token. A narrative that measured a
        # five-minute wait and claims a thirty-minute floor is describing a run
        # that did not happen.
        self.replace_in_evidence(
            FLOOR_GATE,
            "published_autosave_min_interval: 5m",
            "published_autosave_min_interval: 30m",
        )
        with self.assertRejected("published_autosave_min_interval"):
            self.verify()

    def test_an_autosave_taken_sooner_than_the_floor_allows_is_rejected(self) -> None:
        # The property the second section exists for: the commit waited out the
        # floor rather than landing when readiness alone would have allowed it.
        # A narrative reporting a readiness-shaped delay here has shown the
        # opposite of the gate.
        self.replace_in_evidence(
            FLOOR_GATE,
            "seconds_from_previous_autosave_to_deferred_commit: 305.1",
            "seconds_from_previous_autosave_to_deferred_commit: 41.2",
        )
        with self.assertRejected(
            "seconds_from_previous_autosave_to_deferred_commit.*below the required"
        ):
            self.verify()

    def test_a_due_time_the_floor_never_deferred_is_rejected(self) -> None:
        self.replace_in_evidence(
            FLOOR_GATE,
            "due_time_was_deferred_past_readiness: True",
            "due_time_was_deferred_past_readiness: False",
        )
        with self.assertRejected("due_time_was_deferred_past_readiness.*requires True"):
            self.verify()

    def test_a_floor_that_swallowed_an_autosave_of_its_own_is_rejected(self) -> None:
        # Two autosaves five minutes apart with a third in between is not one
        # deferred commit; it is a floor that was not in force, wearing the
        # right durations.
        self.replace_in_evidence(
            FLOOR_GATE,
            "commits_between_the_two_autosaves: 0",
            "commits_between_the_two_autosaves: 1",
        )
        with self.assertRejected("commits_between_the_two_autosaves"):
            self.verify()

    def test_a_readiness_only_timing_narrative_is_rejected(self) -> None:
        # The shape the retired quiet-and-maximum drill produced: readiness
        # measured, the floor never exercised. It is a perfectly well-formed
        # narrative and it passes every integrity check, which is exactly why
        # the section is what refuses it.
        text = self.evidence_text(FLOOR_GATE)
        head, marker, _ = text.partition("## the floor:")
        self.assertTrue(marker, "the floor section is not in the fixture")
        self.rewrite_evidence(FLOOR_GATE, head + "# outcome: pass (2026-08-29T23:27:59Z)\n")
        with self.assertRejected("no `## the floor"):
            self.verify()

    def test_an_unexercised_scenario_section_is_rejected(self) -> None:
        # The three crash points are three sections. Dropping one is how a
        # drill quietly stops covering a durable phase.
        self.replace_in_evidence(
            "txn_crash_converges_and_is_idempotent",
            "## crash point synced -- every destination reached, record not yet cleared",
            "## a note about the third crash point",
        )
        with self.assertRejected("no `## crash point synced"):
            self.verify()

    def test_a_scenario_run_fewer_times_than_it_declares_is_rejected(self) -> None:
        text = self.evidence_text("txn_crash_converges_and_is_idempotent")
        head, marker, tail = text.rpartition("## crash point synced")
        self.assertTrue(marker and "second_reopen_exit_status" in tail)
        self.rewrite_evidence(
            "txn_crash_converges_and_is_idempotent", head + "# outcome: pass (2026-08-29T02:48:13Z)\n"
        )
        with self.assertRejected("appears 2 time\\(s\\)"):
            self.verify()

    def test_a_commit_naming_a_path_outside_the_drill_is_rejected(self) -> None:
        self.replace_in_evidence(
            "cli_mutation_commits_immediately",
            "'vault-safety-drill-cli.md'",
            "'Some Real Note.md'",
        )
        with self.assertRejected("Some Real Note.md.*does not allow"):
            self.verify()


if __name__ == "__main__":
    unittest.main(verbosity=2)
