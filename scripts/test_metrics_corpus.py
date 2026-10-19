"""Regression checks for the text-only refactoring denominator."""

from __future__ import annotations

import contextlib
import io
import subprocess
import tempfile
import unittest
from pathlib import Path

from metrics_corpus import Corpus
from metrics_size import report_debt, report_denominator, report_file_size


class CorpusTextTest(unittest.TestCase):
    def test_binary_assets_do_not_become_source_lines_or_debt(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q"], cwd=root, check=True)
            (root / ".refactorignore").write_text("", encoding="utf-8")
            (root / "code.go").write_text("// TODO: real\npackage code\n", encoding="utf-8")
            (root / "graph.ts").write_text("const key = `a\0b`;\n", encoding="utf-8")
            (root / "baseline.png").write_bytes(b"\x89PNG\r\n\x00TODO\nXXX\n")
            subprocess.run(["git", "add", "."], cwd=root, check=True)

            corpus = Corpus(root)
            self.assertIn("code.go", corpus.files)
            self.assertIn("graph.ts", corpus.files)
            self.assertIn("baseline.png", corpus.assets)
            self.assertNotIn("baseline.png", corpus.loc)
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                report_denominator(corpus)
                report_file_size(corpus)
                report_debt(corpus)
            report = output.getvalue()
            self.assertIn("1 binary files", report)
            self.assertIn("1 matches for TODO", report)
            self.assertNotIn("baseline.png:", report)


if __name__ == "__main__":
    unittest.main()
