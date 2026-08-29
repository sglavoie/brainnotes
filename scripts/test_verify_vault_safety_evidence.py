"""Tests for the record-level half of the vault safety acceptance harness.

The harness has two halves. One re-derives live properties from the machine —
restores, `git status`, `launchctl`, the Trash copy — and can only run where
that machine is. The other decides whether the retained evidence record is
worth anything at all, and that half is what these tests drive, against a
throwaway copy of the real record and the real narratives.

The question every test here asks is the one the harness exists for: what does
a person who wants a green result have to do to get one? Deleting a gate,
rewriting a narrative and re-hashing it, claiming an exit status nothing
recorded, or leaving one scenario section unexercised all have to fail, because
each of them is cheaper than fixing the thing the gate was watching.

Run with `python3 scripts/test_verify_vault_safety_evidence.py`.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import tempfile
import unittest
from pathlib import Path

import vault_safety_scenarios as scenarios
import verify_vault_safety_evidence as harness

REPO_ROOT = Path(__file__).resolve().parents[1]
RECORD_NAME = "docs/operations/vault-safety-evidence.json"


def fake_args(root: Path) -> argparse.Namespace:
    """The harness's argv, with the live-machine arguments pointed nowhere.

    Every check these tests drive reads the record and the retained files; none
    of them touches the vault, the archive, or the old root, so those paths are
    placeholders that would fail loudly if a check started using them.
    """
    return argparse.Namespace(
        evidence=RECORD_NAME,
        live_vault=str(root / "no-live-vault"),
        sealed_archive=str(root / "no-archive"),
        old_root=str(root / "no-old-root"),
        github_repository="sglavoie/brainnotes-vault",
        health_command=str(root / "no-health-command"),
        launchagent_label="com.example.absent",
        clone_timeout=1,
        json=False,
    )


class RecordTestCase(unittest.TestCase):
    """A disposable copy of the real record and its retained narratives."""

    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory(prefix="vault-evidence-test-")
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        (self.root / harness.EVIDENCE_DIR_NAME).parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(
            REPO_ROOT / harness.EVIDENCE_DIR_NAME, self.root / harness.EVIDENCE_DIR_NAME
        )
        shutil.copy2(REPO_ROOT / RECORD_NAME, self.root / RECORD_NAME)
        self.record = json.loads((self.root / RECORD_NAME).read_text(encoding="utf-8"))

    # -- fixture editing --------------------------------------------------

    def save(self) -> None:
        (self.root / RECORD_NAME).write_text(json.dumps(self.record, indent=2), encoding="utf-8")

    def evidence_text(self, gate: str) -> str:
        return (self.root / self.record["gates"][gate]["evidence_path"]).read_text(encoding="utf-8")

    def rewrite_evidence(self, gate: str, text: str, *, rehash: bool = True) -> None:
        """Rewrite a narrative the way someone covering something up would.

        The hash is updated by default, because a rewrite that left the digest
        stale is caught by the integrity check that already existed and would
        say nothing about whether the gate's meaning is enforced.
        """
        path = self.root / self.record["gates"][gate]["evidence_path"]
        path.write_text(text, encoding="utf-8")
        if rehash:
            self.record["gates"][gate]["evidence_sha256"] = hashlib.sha256(
                text.encode("utf-8")
            ).hexdigest()
            self.save()

    def replace_in_evidence(self, gate: str, old: str, new: str) -> None:
        text = self.evidence_text(gate)
        self.assertIn(old, text, f"fixture text {old!r} is not in the {gate} narrative")
        self.rewrite_evidence(gate, text.replace(old, new, 1))

    # -- running ----------------------------------------------------------

    def build(self) -> harness.Harness:
        instance = harness.Harness(fake_args(self.root))
        instance.repo_root = self.root
        instance.evidence_file = (self.root / RECORD_NAME).resolve()
        instance.evidence_root = (self.root / harness.EVIDENCE_DIR_NAME).resolve()
        return instance

    def verify(self) -> harness.Harness:
        """Every check that reads only the record and its retained evidence."""
        instance = self.build()
        instance.load_record()
        instance.verify_gates()
        instance.verify_scenarios()
        instance.verify_recorded_claims()
        instance.verify_no_orphan_evidence()
        return instance

    def assertRejected(self, pattern: str):
        return self.assertRaisesRegex(harness.CheckFailure, pattern)


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
        self.replace_in_evidence(
            "bn_doctor", "# outcome: pass (2026-08-29T03:32:15Z)", "# outcome: pass (2026-08-28T00:00:00Z)"
        )
        with self.assertRejected("outcome predates the run"):
            self.verify()


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
            "external_edit_debounced_quiet_and_max",
            "maximum_latency_seconds_policy: 120",
            "maximum_latency_seconds_policy: 900",
        )
        with self.assertRejected("maximum_latency_seconds_policy"):
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
