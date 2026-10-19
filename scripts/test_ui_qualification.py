"""Qualification evidence must identify a build without claiming a human pass."""

import contextlib
import io
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import ui_qualification as qualification


class QualificationEvidenceTests(unittest.TestCase):
    def test_release_refuses_dirty_source(self):
        with patch.object(qualification, "git_output", return_value=" M README.md"):
            with self.assertRaisesRegex(qualification.Refusal, "clean checkout"):
                qualification.require_clean_release(True)
            qualification.require_clean_release(False)

    def test_record_preserves_identity_and_leaves_observations_pending(self):
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "bn"
            binary.write_bytes(b"qualified build")
            record = Path(directory) / "record.json"
            source = {"commit": "commit-id", "working_tree": "", "source_sha256": "source-digest"}
            with contextlib.redirect_stdout(io.StringIO()):
                qualification.record_build(record, 8765, binary, source, True)
            result = json.loads(record.read_text())
            self.assertEqual(result["commit"], "commit-id")
            self.assertEqual(result["source_sha256"], "source-digest")
            self.assertEqual(result["binary_sha256"], qualification.hashlib.sha256(binary.read_bytes()).hexdigest())
            self.assertTrue(result["release_candidate"])
            self.assertTrue(all(value is None for value in result["observations"].values()))
            with contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaises(FileExistsError):
                    qualification.record_build(record, 8765, binary, source, False)
            self.assertEqual(json.loads(record.read_text()), result)

    def test_source_digest_changes_for_edits_and_deleted_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            note = root / "source.txt"
            note.write_text("first version")
            with patch.object(qualification, "ROOT", root):
                with patch.object(qualification.subprocess, "check_output", return_value=b"source.txt\0"):
                    original = qualification.source_digest()
                    note.write_text("changed version")
                    changed = qualification.source_digest()
                    note.unlink()
                    deleted = qualification.source_digest()
            self.assertEqual(len({original, changed, deleted}), 3)


if __name__ == "__main__":
    unittest.main()
