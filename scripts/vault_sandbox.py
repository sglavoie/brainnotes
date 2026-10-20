#!/usr/bin/env python3
"""Run one `bn` command against a throwaway sandbox vault, or refuse to run it.

This is the manual half of the verification guard whose test half lives in
`internal/cli/vault_isolation_test.go`. Both enforce the same rule, and both
exist because of the same incident: a refactoring session verified `bn
rename-key` by running the built binary after a plain `cd` into a scratch
directory. `$BNOTES_VAULT` was set in the ambient shell to the developer's real
vault, `cd` does not override an environment variable, and the equivalence
matrix renamed a frontmatter key in 214 real notes.

Three refusals, in this order:

  1. a resolved vault that names a real vault (see REAL_VAULT_DIR_NAMES) at any
     depth, wherever it sits;
  2. a resolved vault or an asserted sandbox outside the system temporary
     directory, because anything that outlives the verification is somebody's
     data;
  3. a resolved vault that is isolated but is not the sandbox this run
     asserted.

The resolution is not assumed. `$BNOTES_VAULT` is removed from the child
environment, `--vault` is passed explicitly, and `bn config` is then asked which
root it actually resolved — the answer is what the three rules are applied to.
A `--vault` in the caller's own arguments is refused rather than honoured, so
the sandbox this script proved is the one the command runs against.

    scripts/vault_sandbox.py -- rename-key tags labels
    scripts/vault_sandbox.py --keep -- set note.md status=done
    scripts/vault_sandbox.py --sandbox "$TMPDIR/fixture" -- repair --dry-run

Exit code 2 is a refusal by this script. Anything else is the `bn` command's own
exit code.
"""

import argparse
import os
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

CONFIG_FILE_NAME = "brainnotes.toml"
VAULT_ENV = "BNOTES_VAULT"

# Directory names that identify a real vault wherever they appear in a path.
# Rule 2 already refuses everything outside the temporary directory; this is the
# belt-and-braces half, so a real vault reached through a symlink under the temp
# directory is still refused and the refusal names the vault that was damaged.
REAL_VAULT_DIR_NAMES = ("BrainNotesVault",)

REFUSED = 2


class Refusal(Exception):
    """A verification this script will not run."""


def normalize(path: str | Path) -> Path:
    """Absolute, symlink-free where the path exists, clean where it does not.

    macOS reaches its temp directory through /var -> /private/var, so a
    comparison that skipped this would refuse every legitimate sandbox.
    """
    candidate = Path(path).expanduser().absolute()
    try:
        return candidate.resolve(strict=True)
    except OSError:
        return Path(os.path.normpath(candidate))


def within(child: Path, parent: Path) -> bool:
    return child == parent or parent in child.parents


def check_isolation(resolved: Path, sandbox: Path) -> None:
    """Raise Refusal unless resolved is the asserted isolated sandbox."""
    temp_dir = normalize(tempfile.gettempdir())
    for name in REAL_VAULT_DIR_NAMES:
        if name in resolved.parts:
            raise Refusal(
                f"refusing to verify against {resolved}: it is the real vault "
                f"{name!r}, which no test or manual verification may write to"
            )
    for label, path in (("asserted sandbox", sandbox), ("resolved vault", resolved)):
        if not within(path, temp_dir):
            raise Refusal(
                f"refusing to verify against {path}: the {label} is outside the "
                f"system temporary directory {temp_dir}, so it is not an isolated vault"
            )
    if not within(resolved, sandbox):
        raise Refusal(
            f"refusing to verify against {resolved}: it is outside the asserted "
            f"isolated vault {sandbox}"
        )


def scrubbed_env() -> dict[str, str]:
    """The child environment with the ambient vault removed.

    This is the single line that would have prevented the incident.
    """
    env = dict(os.environ)
    env.pop(VAULT_ENV, None)
    return env


def resolved_vault(bn: str, sandbox: Path) -> Path:
    """Ask `bn` which vault root it resolves, rather than assuming it."""
    result = subprocess.run(
        [bn, "config", "--vault", str(sandbox)],
        env=scrubbed_env(),
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise Refusal(
            f"`{bn} config --vault {sandbox}` failed, so the resolved vault is "
            f"unknown and nothing may be run:\n{result.stderr.strip()}"
        )
    for line in result.stdout.splitlines():
        if line.startswith("Vault:"):
            return normalize(line.split(":", 1)[1].strip())
    raise Refusal(
        f"`{bn} config` did not report a vault root, so the resolved vault is "
        f"unknown:\n{result.stdout.strip()}"
    )


def seed_sandbox(bn: str, sandbox: Path) -> None:
    """Make sandbox a vault root if it is not one already."""
    if (sandbox / CONFIG_FILE_NAME).is_file():
        return
    result = subprocess.run(
        [bn, "init", str(sandbox)],
        env=scrubbed_env(),
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise Refusal(f"`{bn} init {sandbox}` failed:\n{result.stderr.strip()}")


def prepare_sandbox(bn: str, requested: str | None) -> tuple[Path, bool]:
    """Return the sandbox to use and whether this run created it.

    The isolation rules are applied to the directory before `bn init` writes
    anything into it, so a refusal leaves the filesystem untouched.
    """
    if requested:
        sandbox = normalize(requested)
        if not sandbox.is_dir():
            raise Refusal(f"--sandbox {sandbox} is not a directory")
        created = False
    else:
        sandbox = normalize(tempfile.mkdtemp(prefix="bn-verify-"))
        created = True
    check_isolation(sandbox, sandbox)
    seed_sandbox(bn, sandbox)
    return sandbox, created


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Run one `bn` command against a throwaway sandbox vault.",
        epilog="Put the bn arguments after `--`.",
    )
    parser.add_argument(
        "--sandbox",
        help="an existing sandbox vault to reuse; must be inside the system temporary directory",
    )
    parser.add_argument(
        "--bn",
        default=os.environ.get("BN_BIN", "bn"),
        help="the bn binary to run (default: $BN_BIN, else `bn` on PATH)",
    )
    parser.add_argument(
        "--keep",
        action="store_true",
        help="keep a sandbox this run created, and print its path",
    )
    parser.add_argument("command", nargs=argparse.REMAINDER, help="bn arguments")
    return parser.parse_args(argv)


def bn_command(remainder: list[str]) -> list[str]:
    """The bn arguments: the remainder minus the one `--` that separates them.

    Only a leading `--` belongs to this script. Any later one is the bn
    command's own — `unset a.md b.md -- title` and `checkpoint run -- true`
    both mean something different without it.
    """
    if remainder and remainder[0] == "--":
        return remainder[1:]
    return list(remainder)


def run(args: argparse.Namespace) -> int:
    command = bn_command(args.command)
    if not command:
        raise Refusal("no bn command was given; put the bn arguments after `--`")
    if any(arg == "--vault" or arg.startswith("--vault=") for arg in command):
        raise Refusal(
            "refusing a command that names its own --vault: this script owns the "
            "vault it proved, and a second one would decide where the write lands"
        )

    sandbox, created = prepare_sandbox(args.bn, args.sandbox)
    try:
        check_isolation(resolved_vault(args.bn, sandbox), sandbox)
        print(f"+ {args.bn} --vault {sandbox} {' '.join(command)}", flush=True)
        return subprocess.run(
            [args.bn, "--vault", str(sandbox), *command],
            env=scrubbed_env(),
            check=False,
        ).returncode
    finally:
        if created and args.keep:
            print(f"sandbox kept at {sandbox}", flush=True)
        elif created:
            shutil.rmtree(sandbox, ignore_errors=True)


def main(argv: list[str]) -> int:
    try:
        return run(parse_args(argv))
    except Refusal as refusal:
        print(f"vault_sandbox: {refusal}", file=sys.stderr)
        return REFUSED


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
