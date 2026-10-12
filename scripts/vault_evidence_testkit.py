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

The tests are split across `test_verify_vault_safety_{record,results,claims}.py`;
this module holds the shared fixture. Run them with
`python3 -m unittest discover -s scripts -p "test_verify_vault_safety*.py"`.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import shutil
import tempfile
import unittest
from pathlib import Path

import verify_vault_safety_evidence as harness

REPO_ROOT = Path(__file__).resolve().parents[1]
RECORD_NAME = "docs/operations/vault-safety-evidence.json"

# The timing gate, named once because several tests drive it: it is the only
# one whose scenario asserts a *configured* policy, so it is where a narrative
# can claim a floor the run never enforced.
FLOOR_GATE = "external_edit_scheduled_by_readiness_and_floor"


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
