"""Argument handling for `vault_sandbox.py`.

`just ci` runs it with every other scripts/test_*.py. The end-to-end cases run
against a fake `bn` that records its argv, so nothing here touches a real vault
or needs a built binary.
"""

from __future__ import annotations

import json
import shutil
import stat
import sys
import tempfile
import unittest
from pathlib import Path

import vault_sandbox

FAKE_BN = """#!{python}
import json, sys
args = sys.argv[1:]
if args[:1] == ["config"]:
    print("Vault: " + args[args.index("--vault") + 1])
elif args[:1] == ["init"]:
    pass
else:
    with open({record!r}, "w") as handle:
        json.dump(args, handle)
"""


class TestBnCommand(unittest.TestCase):
    def test_only_the_leading_separator_is_stripped(self) -> None:
        cases = {
            ("--", "unset", "a.md", "b.md", "--", "title"): ["unset", "a.md", "b.md", "--", "title"],
            ("--", "checkpoint", "run", "--", "true"): ["checkpoint", "run", "--", "true"],
            ("unset", "a.md", "--", "title"): ["unset", "a.md", "--", "title"],
            ("--",): [],
            (): [],
        }
        for remainder, expected in cases.items():
            with self.subTest(remainder=remainder):
                self.assertEqual(vault_sandbox.bn_command(list(remainder)), expected)


class TestRunPassesSeparatorsThrough(unittest.TestCase):
    def setUp(self) -> None:
        self.scratch = Path(tempfile.mkdtemp(prefix="bn-sandbox-test-"))
        self.addCleanup(shutil.rmtree, self.scratch, ignore_errors=True)
        self.record = self.scratch / "argv.json"
        self.fake_bn = self.scratch / "bn"
        self.fake_bn.write_text(FAKE_BN.format(python=sys.executable, record=str(self.record)))
        self.fake_bn.chmod(self.fake_bn.stat().st_mode | stat.S_IXUSR)

    def run_sandbox(self, *argv: str) -> list[str]:
        exit_code = vault_sandbox.main(["--bn", str(self.fake_bn), *argv])
        self.assertEqual(exit_code, 0)
        recorded = json.loads(self.record.read_text())
        self.assertEqual(recorded[0], "--vault")
        return recorded[2:]

    def test_unset_keeps_its_property_separator(self) -> None:
        self.assertEqual(
            self.run_sandbox("--", "unset", "a.md", "b.md", "--", "title"),
            ["unset", "a.md", "b.md", "--", "title"],
        )

    def test_checkpoint_run_keeps_its_command_separator(self) -> None:
        self.assertEqual(
            self.run_sandbox("--", "checkpoint", "run", "--", "true"),
            ["checkpoint", "run", "--", "true"],
        )

    def test_a_command_naming_its_own_vault_is_still_refused(self) -> None:
        exit_code = vault_sandbox.main(["--bn", str(self.fake_bn), "--", "--vault", "x", "list"])
        self.assertEqual(exit_code, vault_sandbox.REFUSED)
        self.assertFalse(self.record.exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
