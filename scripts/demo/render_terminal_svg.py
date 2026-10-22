# /// script
# requires-python = ">=3.10"
# dependencies = ["rich==14.2.0"]
# ///
"""Render the terminal demo's ANSI frames as one animated SVG.

Usage:
  uv run scripts/demo/render_terminal_svg.py FRAMES.json OUTPUT.svg

FRAMES.json is a list of {"ansi", "hold_ms", "caption"} written by
scripts/demo/record.mjs. Every frame is an ANSI screen of the same size; the SVG
stacks them and CSS shows one at a time, so the README plays it as crisp text
without a GIF or a script.
"""

import html
import io
import json
import re
import sys
from pathlib import Path

from rich._export_format import CONSOLE_SVG_FORMAT
from rich.console import Console
from rich.terminal_theme import TerminalTheme
from rich.text import Text

TITLE = "bn — Brain Notes"
COLUMNS = 96

# A dark palette matching the browser workspace; explicit ANSI colors keep their values.
PALETTE = TerminalTheme(
    (26, 27, 38), (192, 202, 245),
    [(26, 27, 38), (247, 118, 142), (158, 206, 106), (224, 175, 104),
     (122, 162, 247), (187, 154, 247), (125, 207, 255), (192, 202, 245)],
)

# The terminal body of rich's template: everything one animation frame replaces.
TERMINAL_BODY = """    <g transform="translate({terminal_x}, {terminal_y})" clip-path="url(#{unique_id}-clip-terminal)">
    {backgrounds}
    <g class="{unique_id}-matrix">
    {matrix}
    </g>
    </g>"""

# One animation frame: its own styles and line clips, then the terminal body.
# Each frame keeps its own unique_id, since rich numbers style classes per export.
FRAME_FORMAT = """<style>
.{unique_id}-matrix {{ font-family: Fira Code, monospace; font-size: {char_height}px; line-height: {line_height}px; font-variant-east-asian: full-width; }}
{styles}
</style>
<defs>{lines}</defs>
<g class="bn-frame bn-f@INDEX@">
<text class="bn-caption" text-anchor="end" x="@CAPTION_X@" y="27">@CAPTION@</text>
""" + TERMINAL_BODY.replace("{unique_id}-clip-terminal", "bn-clip-terminal") + "\n</g>"


def export(ansi, code_format=CONSOLE_SVG_FORMAT, unique_id=None):
    console = Console(
        file=io.StringIO(), record=True, width=COLUMNS, color_system="truecolor"
    )
    console.print(Text.from_ansi(ansi), end="", soft_wrap=True)
    svg = console.export_svg(
        title=TITLE, theme=PALETTE, code_format=code_format, unique_id=unique_id
    )
    # Keep the checked-in image self-contained; use the viewer's monospace font.
    return re.sub(r"@font-face\s*\{[^}]*\}", "", svg)


def animate(frames):
    total = sum(frame["hold_ms"] for frame in frames)
    width = int(float(re.search(r'viewBox="0 0 ([\d.]+)', export(frames[0]["ansi"], unique_id="bn")).group(1)))
    rules = [
        f".bn-frame {{ visibility: hidden; animation: {total}ms step-end infinite; }}",
        ".bn-caption { font: bold 16px ui-monospace, Menlo, monospace; fill: #7aa2f7; }",
    ]
    bodies = []
    start = 0
    for index, frame in enumerate(frames):
        end = start + frame["hold_ms"]
        rules.append(
            f".bn-f{index} {{ animation-name: bn-k{index}; }} "
            f"@keyframes bn-k{index} {{ 0% {{ visibility: hidden; }} "
            f"{100 * start / total:.3f}% {{ visibility: visible; }} "
            f"{100 * end / total:.3f}% {{ visibility: {'visible' if end == total else 'hidden'}; }} }}"
        )
        body = export(frame["ansi"], FRAME_FORMAT, unique_id=f"bn{index}")
        bodies.append(
            body.replace("@INDEX@", str(index))
            .replace("@CAPTION_X@", str(width - 24))
            .replace("@CAPTION@", html.escape(frame["caption"]).replace(" ", "&#160;"))
        )
        start = end
    # Readers who asked for less motion get the last frame, which shows the most.
    last = len(frames) - 1
    rules.append(
        f"@media (prefers-reduced-motion: reduce) {{ .bn-frame {{ animation: none; }} .bn-f{last} {{ visibility: visible; }} }}"
    )
    outer = CONSOLE_SVG_FORMAT.replace(TERMINAL_BODY, "@FRAMES@").replace(
        "    </style>", "    @RULES@\n    </style>"
    )
    svg = export(frames[0]["ansi"], outer, unique_id="bn")
    return svg.replace("@RULES@", "\n".join(rules)).replace("@FRAMES@", "\n".join(bodies))


def main():
    source, destination = map(Path, sys.argv[1:])
    svg = animate(json.loads(source.read_text()))
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(svg)


if __name__ == "__main__":
    main()
