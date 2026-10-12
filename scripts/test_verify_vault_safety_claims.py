"""Self-asserted claims and record shape.

Split from the former single module; the shared fixture is in
`vault_evidence_testkit.py`. Run with `python3 -m unittest discover -s scripts -p "test_verify_vault_safety*.py"`.
"""

from __future__ import annotations

import unittest

import vault_safety_scenarios as scenarios
import verify_vault_safety_evidence as harness
from vault_evidence_testkit import RecordTestCase



class TestSelfAssertedClaims(RecordTestCase):
    def test_an_exit_status_no_narrative_supports_is_rejected(self) -> None:
        # `interrupted_delete_recovered_by_watcher` observes a watcher, not a
        # process, so a zero here is a number with nothing behind it.
        self.record["gates"]["interrupted_delete_recovered_by_watcher"]["exit_status"] = 0
        self.save()
        with self.assertRejected("records no single exit status"):
            self.verify()

    def test_an_exit_status_that_contradicts_its_narrative_is_rejected(self) -> None:
        # This drill's mutation is supposed to fail. A record claiming it exited
        # 0 is claiming a different run than the one it retained.
        self.record["gates"]["local_commit_failure_blocks_later_writes"]["exit_status"] = 0
        self.save()
        with self.assertRejected("exit_status is 0 but the narrative records"):
            self.verify()

    def test_a_missing_exit_status_its_narrative_shows_is_rejected(self) -> None:
        del self.record["gates"]["bn_doctor"]["exit_status"]
        self.save()
        with self.assertRejected("exit_status is missing"):
            self.verify()

    def test_an_outcome_of_false_is_rejected(self) -> None:
        self.record["gates"]["bn_doctor"]["outcome"] = False
        self.save()
        with self.assertRejected("outcome is False, not true"):
            self.verify()

    def test_a_checked_at_that_is_not_the_narratives_own_is_rejected(self) -> None:
        self.record["gates"]["bn_doctor"]["checked_at"] = "2026-08-29T09:00:00Z"
        self.save()
        with self.assertRejected("checked_at is .* but the narrative was recorded at"):
            self.verify()

    def test_gates_that_disagree_about_what_they_both_saw_are_rejected(self) -> None:
        # Four gates counted the tracked paths at HEAD separately. Rewriting one
        # narrative leaves the other three still saying what the number was.
        self.replace_in_evidence(
            "restore_from_local_git", "tracked_paths_at_HEAD: 1286", "tracked_paths_at_HEAD: 1285"
        )
        with self.assertRejected("tracked paths at HEAD is reported inconsistently"):
            self.verify()

    def test_restores_that_did_not_produce_the_same_manifest_are_rejected(self) -> None:
        digest = "0" * 64
        self.replace_in_evidence(
            "restore_from_local_git",
            "local_restore_manifest_sha256: 6142b3656f9419694848412077a18b01cef9458a43a9face39a9d63678c5a9f1",
            f"local_restore_manifest_sha256: {digest}",
        )
        with self.assertRejected("observations that must agree do not"):
            self.verify()


class TestRecordShape(RecordTestCase):
    def test_an_older_schema_version_is_rejected(self) -> None:
        self.record["schema_version"] = harness.SCHEMA_VERSION - 1
        self.save()
        with self.assertRejected("schema_version must be"):
            self.verify()

    def test_a_record_from_an_older_scenario_catalogue_is_rejected(self) -> None:
        self.record["scenario_catalogue_version"] = scenarios.CATALOGUE_VERSION - 1
        self.save()
        with self.assertRejected("have to be run again"):
            self.verify()

    def test_paths_redacted_must_be_claimed(self) -> None:
        del self.record["gates"]["bn_doctor"]["paths_redacted"]
        self.save()
        with self.assertRejected("paths_redacted is not true"):
            self.verify()

    def test_an_absolute_evidence_path_is_rejected(self) -> None:
        self.record["gates"]["bn_doctor"]["evidence_path"] = "/etc/hosts"
        self.save()
        with self.assertRejected("must be repository-relative"):
            self.verify()

    def test_an_evidence_path_that_traverses_upward_is_rejected(self) -> None:
        self.record["gates"]["bn_doctor"]["evidence_path"] = (
            f"{harness.EVIDENCE_DIR_NAME}/../../../etc/hosts"
        )
        self.save()
        with self.assertRejected("must not traverse upward"):
            self.verify()

    def test_a_symlink_out_of_the_evidence_directory_is_rejected(self) -> None:
        outside = self.root / "elsewhere.txt"
        outside.write_text(self.evidence_text("bn_doctor"), encoding="utf-8")
        link = self.root / harness.EVIDENCE_DIR_NAME / "bn_doctor_link.txt"
        link.symlink_to(outside)
        self.record["gates"]["bn_doctor"]["evidence_path"] = (
            f"{harness.EVIDENCE_DIR_NAME}/bn_doctor_link.txt"
        )
        self.save()
        with self.assertRejected("resolves outside the evidence directory"):
            self.verify()

    def test_retained_evidence_no_gate_claims_is_rejected(self) -> None:
        (self.root / harness.EVIDENCE_DIR_NAME / "leftover.txt").write_text("", encoding="utf-8")
        with self.assertRejected("retained evidence with no gate record"):
            self.verify()



if __name__ == "__main__":
    unittest.main(verbosity=2)
