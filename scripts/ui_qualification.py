#!/usr/bin/env python3
"""Serve a disposable Brain Notes vault for physical-device qualification."""

import argparse
import base64
import hashlib
import json
import shutil
import socket
import subprocess
import sys
import tempfile
from pathlib import Path
from datetime import datetime, timezone

# Importing the fixture helper must not dirty a clean release checkout.
sys.dont_write_bytecode = True

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
        "---\nstatus: draft\n---\n# Reading\n\nField observations to find in Library, open, and edit with a screen reader.\n\n"
        "Follow [[Writing]] back to the long note.\n",
        encoding="utf-8",
    )
    notes = vault / "Notes"
    notes.mkdir()
    (notes / "Earlier observations.md").write_text(
        "---\nstatus: draft\ntitle: A long note title about field observations and questions worth revisiting\n---\n"
        "# Earlier observations\n\nField observations from the riverside walk. "
        "Compare [[Writing]] with [[Reading]] and record what changed.\n",
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


def git_output(*args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=ROOT, text=True).rstrip("\n")


def source_digest() -> str:
    """Identify the source actually built, including any uncommitted files."""
    paths = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=ROOT
    ).decode().split("\0")
    digest = hashlib.sha256()
    for name in sorted(set(paths) - {""}):
        path = ROOT / name
        digest.update(name.encode() + b"\0")
        digest.update(hashlib.sha256(path.read_bytes()).digest() if path.is_file() else b"missing")
    return digest.hexdigest()


def require_clean_release(release: bool) -> None:
    if release and git_output("status", "--porcelain"):
        raise Refusal("Release qualification needs a clean checkout. Commit the final source and generated assets first.")


def record_build(record: Path | None, port: int, binary: Path, source: dict[str, str], release: bool) -> None:
    evidence = {
        "started_at": datetime.now(timezone.utc).isoformat(),
        **source,
        "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "release_candidate": release,
        "mode": "hosted",
        "port": port,
        "vault_fixture": str(binary.parent / "vault"),
        "phone": {"model": None, "os": None, "browser": None, "orientation": None, "text_size": None},
        "desktop": {"os": None, "browser": None, "screen_reader": None},
        "observations": {"phone_writing": None, "screen_reader": None, "navigation_and_layout": None},
    }
    print(f"Commit: {evidence['commit']}", flush=True)
    print(f"Working tree: {'modified' if evidence['working_tree'] else 'clean'}", flush=True)
    print(f"Source SHA-256: {evidence['source_sha256']}", flush=True)
    print(f"Binary SHA-256: {evidence['binary_sha256']}", flush=True)
    if record:
        with record.open("x", encoding="utf-8") as output:
            json.dump(evidence, output, indent=2)
            output.write("\n")
        print(f"Qualification record (observations pending): {record}", flush=True)


def serve(port: int, record: Path | None = None, release: bool = False) -> int:
    require_clean_release(release)
    fixture = Path(tempfile.mkdtemp(prefix="bn-ui-qualification-")).resolve()
    try:
        binary = fixture / "bn"
        subprocess.run(["just", "web-build"], cwd=ROOT, env=scrubbed_env(), check=True)
        require_clean_release(release)
        source = {
            "commit": git_output("rev-parse", "HEAD"),
            "working_tree": git_output("status", "--porcelain"),
            "source_sha256": source_digest(),
        }
        subprocess.run(
            ["go", "build", "-o", str(binary), "./cmd/bn"],
            cwd=ROOT,
            env=scrubbed_env(),
            check=True,
        )
        if source_digest() != source["source_sha256"] or git_output("rev-parse", "HEAD") != source["commit"]:
            raise Refusal("Source changed during the build. Restart qualification against the final source.")
        require_clean_release(release)
        vault = fixture / "vault"
        vault.mkdir()
        prepare_sandbox(str(binary), str(vault))
        check_isolation(resolved_vault(str(binary), vault), vault)
        seed_notes(vault)
        print(f"Fixture: {vault}", flush=True)
        record_build(record, port, binary, source, release)
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
    parser.add_argument("--record", type=Path, help="write a new JSON build record with pending observation fields")
    parser.add_argument("--release", action="store_true", help="require a clean checkout and a persistent build record")
    args = parser.parse_args()
    if not 1 <= args.port <= 65535:
        parser.error("--port must be between 1 and 65535")
    record = args.record.expanduser().resolve() if args.record else None
    if args.release and record is None:
        parser.error("--release requires --record")
    if record and record.exists():
        parser.error("--record must name a new file; existing qualification evidence is never overwritten")
    if record and record.is_relative_to(ROOT):
        parser.error("--record must be outside the checkout so recording evidence does not change the tested source")
    try:
        return serve(args.port, record, args.release)
    except (Refusal, subprocess.CalledProcessError, OSError) as error:
        print(f"UI qualification setup failed: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
