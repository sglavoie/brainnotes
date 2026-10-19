#!/usr/bin/env python3
"""Serve a disposable Brain Notes vault for physical-device qualification."""

import argparse
import base64
import hashlib
import shutil
import socket
import subprocess
import sys
import tempfile
from pathlib import Path

from vault_sandbox import Refusal, check_isolation, prepare_sandbox, resolved_vault, scrubbed_env


ROOT = Path(__file__).resolve().parents[1]
SAMPLE_PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII="
)


def seed_notes(vault: Path) -> None:
    """Give the phone journey enough prose, frontmatter, and an attachment."""
    metadata = "\n".join(f"field_{number}: value_{number}" for number in range(1, 26))
    prose = "\n\n".join(
        f"Paragraph {number}. The morning trail follows the river. Record the changing "
        "light, the birds along the bank, and the questions to revisit tomorrow."
        for number in range(1, 41)
    )
    (vault / "Writing.md").write_text(
        f"---\nstatus: draft\ntags:\n  - qualification\n{metadata}\n---\n"
        f"# Field journal\n\n{prose}\n\nSee [[Reading]] and this attachment:\n\n"
        "![Sample attachment](assets/sample.png)\n",
        encoding="utf-8",
    )
    (vault / "Reading.md").write_text(
        "# Reading\n\nA note to find in Library, open, and edit with a screen reader.\n\n"
        "Follow [[Writing]] back to the long note.\n",
        encoding="utf-8",
    )
    assets = vault / "assets"
    assets.mkdir()
    (assets / "sample.png").write_bytes(SAMPLE_PNG)


def lan_address() -> str:
    """Prefer a physical macOS interface to a VPN's default route."""
    for interface in ("en0", "en1"):
        try:
            result = subprocess.run(
                ["ipconfig", "getifaddr", interface],
                capture_output=True,
                text=True,
                check=False,
            )
        except FileNotFoundError:
            break
        if result.returncode == 0 and result.stdout.strip():
            return result.stdout.strip()
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as probe:
            probe.connect(("192.0.2.1", 9))
            return probe.getsockname()[0]
    except OSError:
        return "<computer-LAN-IP>"


def serve(port: int) -> int:
    fixture = Path(tempfile.mkdtemp(prefix="bn-ui-qualification-")).resolve()
    try:
        binary = fixture / "bn"
        subprocess.run(["just", "web-build"], cwd=ROOT, env=scrubbed_env(), check=True)
        subprocess.run(
            ["go", "build", "-o", str(binary), "./cmd/bn"],
            cwd=ROOT,
            env=scrubbed_env(),
            check=True,
        )
        vault = fixture / "vault"
        vault.mkdir()
        prepare_sandbox(str(binary), str(vault))
        check_isolation(resolved_vault(str(binary), vault), vault)
        seed_notes(vault)
        digest = hashlib.sha256(binary.read_bytes()).hexdigest()
        print(f"Fixture: {vault}", flush=True)
        print(f"Binary SHA-256: {digest}", flush=True)
        print(f"On this computer: http://127.0.0.1:{port}/", flush=True)
        print(f"On a phone on the same network: http://{lan_address()}:{port}/", flush=True)
        print("Open Writing in Library for the phone journey. Press Ctrl-C to stop and remove the fixture.", flush=True)
        command = [str(binary), "--vault", str(vault), "serve", "--hosted", "--addr", f"0.0.0.0:{port}", "--no-open"]
        process = subprocess.Popen(command, cwd=fixture, env=scrubbed_env())
        try:
            return process.wait()
        except KeyboardInterrupt:
            if process.poll() is None:
                process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
            return 0
    finally:
        shutil.rmtree(fixture)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=8765, help="listen port (default: 8765)")
    args = parser.parse_args()
    if not 1 <= args.port <= 65535:
        parser.error("--port must be between 1 and 65535")
    try:
        return serve(args.port)
    except (Refusal, subprocess.CalledProcessError) as error:
        print(f"UI qualification setup failed: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
