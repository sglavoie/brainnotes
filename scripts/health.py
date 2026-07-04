#!/usr/bin/env python3

import argparse
import subprocess
import sys
from pathlib import Path


def run(command: list[str], cwd: Path) -> None:
    print("+", " ".join(command), flush=True)
    subprocess.run(command, cwd=cwd, check=True)


def main() -> int:
    parser = argparse.ArgumentParser(description="Run repository health checks.")
    parser.add_argument(
        "--test",
        action="store_true",
        help="run the validation suite used by automated checks",
    )
    args = parser.parse_args()

    repo_root = Path(__file__).resolve().parent.parent
    commands = [
        ["just", "ci"],
    ]

    try:
        for command in commands:
            run(command, repo_root)
    except subprocess.CalledProcessError as err:
        return err.returncode

    if args.test:
        print("health checks passed", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
