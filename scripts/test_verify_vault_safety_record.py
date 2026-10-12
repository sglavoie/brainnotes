"""Record-level checks: the record as it stands, missing gates, rewritten evidence.

Split from the former single module; the shared fixture is in
`vault_evidence_testkit.py`. Run with `python3 -m unittest discover -s scripts -p "test_verify_vault_safety*.py"`.
"""

from __future__ import annotations

import unittest

import vault_safety_scenarios as scenarios
from vault_evidence_testkit import RecordTestCase



class TestTheRecordAsItStands(RecordTestCase):
    def test_the_recorded_evidence_passes_every_record_level_check(self) -> None:
        instance = self.verify()
        self.assertEqual(set(instance.gates), scenarios.REQUIRED_GATES)

    def test_every_scenario_in_the_catalogue_is_recorded(self) -> None:
        self.assertEqual(set(self.record["gates"]), scenarios.REQUIRED_GATES)

    def test_quoted_command_output_does_not_become_an_observation(self) -> None:
        # `brainnotes_ci` retains a tail of `just ci` output, and timestamps in
        # it contain colons. A parser that read those as observations would
        # invent keys out of somebody else's log.
        parsed = scenarios.parse_evidence(self.evidence_text("brainnotes_ci"))
        self.assertEqual(sorted(parsed.observations), ["command", "exit_status"])


class TestMissingAndUnknownGates(RecordTestCase):
    def test_a_missing_required_gate_is_rejected(self) -> None:
        del self.record["gates"]["unexplained_deletion_blocks"]
        self.save()
        with self.assertRejected("missing required gate.*unexplained_deletion_blocks"):
            self.verify()

    def test_dropping_the_gate_and_its_evidence_is_still_rejected(self) -> None:
        gate = self.record["gates"].pop("local_commit_failure_blocks_later_writes")
        (self.root / gate["evidence_path"]).unlink()
        self.save()
        with self.assertRejected("missing required gate"):
            self.verify()

    def test_a_gate_with_no_scenario_is_rejected(self) -> None:
        borrowed = dict(self.record["gates"]["bn_doctor"])
        self.record["gates"]["some_gate_nobody_defined"] = borrowed
        self.save()
        with self.assertRejected("no scenario of that name"):
            self.verify()


class TestRewrittenEvidence(RecordTestCase):
    def test_false_evidence_with_an_updated_hash_is_rejected(self) -> None:
        # The failure mode the whole harness exists to refuse: the gate that
        # says a failed snapshot blocks the vault, rewritten to say no marker
        # was ever written, and re-hashed so the integrity check is happy.
        self.replace_in_evidence(
            "local_commit_failure_blocks_later_writes",
            "blocked_marker_written: True",
            "blocked_marker_written: False",
        )
        with self.assertRejected("blocked_marker_written.*requires True"):
            self.verify()

    def test_a_stale_hash_is_still_rejected(self) -> None:
        text = self.evidence_text("bn_doctor").replace("stderr_empty: True", "stderr_empty: False")
        self.rewrite_evidence("bn_doctor", text, rehash=False)
        with self.assertRejected("does not hash to its recorded digest"):
            self.verify()

    def test_a_removed_decisive_observation_is_rejected(self) -> None:
        self.replace_in_evidence(
            "unexplained_deletion_blocks", "  marker_kind: unexplained_deletion\n", ""
        )
        with self.assertRejected("marker_kind.*appears 0 time"):
            self.verify()

    def test_a_narrative_that_flipped_its_own_verdict_is_rejected(self) -> None:
        self.replace_in_evidence("bn_doctor", "# outcome: pass", "# outcome: fail")
        with self.assertRejected("records outcome 'fail'"):
            self.verify()

    def test_an_undeclared_false_observation_is_rejected(self) -> None:
        # Nothing requires this key, which is the point: in these narratives a
        # False is a negative finding, and one no scenario planned for is a
        # failure rather than a detail that slips through unread.
        self.replace_in_evidence(
            "bn_doctor", "  stderr_empty: True\n", "  stderr_empty: True\n  stderr_was_read: False\n"
        )
        with self.assertRejected("stderr_was_read.*is False"):
            self.verify()

    def test_a_narrative_pointed_at_by_the_wrong_gate_is_rejected(self) -> None:
        source = self.record["gates"]["brainnotes_ci"]
        self.record["gates"]["bn_doctor"]["evidence_path"] = source["evidence_path"]
        self.record["gates"]["bn_doctor"]["evidence_sha256"] = source["evidence_sha256"]
        self.save()
        with self.assertRejected("headed `# gate: brainnotes_ci`"):
            self.verify()

    def test_a_narrative_with_no_outcome_trailer_is_rejected(self) -> None:
        text = self.evidence_text("bn_doctor")
        self.rewrite_evidence("bn_doctor", text[: text.index("# outcome:")])
        with self.assertRejected("cannot be read as a gate record"):
            self.verify()

    def test_a_verdict_recorded_before_the_run_it_describes_is_rejected(self) -> None:
        # Read the trailer the retained narrative actually carries rather than
        # naming a timestamp here: refreshing the evidence must not rot the test.
        text = self.evidence_text("bn_doctor")
        trailer = text[text.index("# outcome:") :].splitlines()[0]
        self.replace_in_evidence("bn_doctor", trailer, "# outcome: pass (2026-08-28T00:00:00Z)")
        with self.assertRejected("outcome predates the run"):
            self.verify()


if __name__ == "__main__":
    unittest.main(verbosity=2)
