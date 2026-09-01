"""Reading a retained gate narrative back into observations.

Every gate under `docs/operations/vault-safety-evidence/` is a plain-text
narrative written by a Python drill: a `# key: value` header, `## title`
sections, and indented `key: value` observations underneath them. This module is
the reader. It decides nothing about whether a gate passed — that is
`vault_safety_scenario_model` — it only turns the retained bytes into the
values the scenario is re-derived from.
"""

from __future__ import annotations

import ast
import re
from dataclasses import dataclass

_HEADER = re.compile(r"\A#\s+(?P<key>[a-z_]+):\s*(?P<value>.*?)\s*\Z")
_SECTION = re.compile(r"\A##\s+(?P<title>.+?)\s*\Z")
_OBSERVATION = re.compile(r"\A\s+(?P<key>[^:]+?):\s*(?P<value>.*?)\s*\Z")


class EvidenceFormatError(Exception):
    """A retained narrative is not in the format every gate is written in."""


@dataclass(frozen=True)
class Observation:
    key: str
    raw: str
    value: object
    section: str
    line: int


@dataclass(frozen=True)
class ParsedEvidence:
    gate: str
    title: str
    recorded_at: str
    outcome: str
    outcome_at: str
    sections: tuple[str, ...]
    observations: dict[str, tuple[Observation, ...]]

    def first(self, key: str) -> Observation | None:
        found = self.observations.get(key)
        return found[0] if found else None


def typed_value(raw: str) -> object:
    """The narrative's own notation, read back as a value.

    The gates were written by a Python drill printing Python values, so `True`,
    an integer, a float and a list literal all mean here what they meant there.
    Anything else stays a string rather than being coerced into something that
    would compare equal to a number by accident.
    """
    if raw == "True":
        return True
    if raw == "False":
        return False
    if re.fullmatch(r"-?\d+", raw):
        return int(raw)
    if re.fullmatch(r"-?\d+\.\d+", raw):
        return float(raw)
    if raw.startswith("[") and raw.endswith("]"):
        try:
            return ast.literal_eval(raw)
        except (ValueError, SyntaxError):
            return raw
    return raw


def parse_evidence(text: str) -> ParsedEvidence:
    """Read a retained narrative into its header, sections and observations.

    Verbatim command output is quoted with a leading `|` and set off by a
    `--- tail of output ---` rule; those lines are not observations and are
    skipped, so a colon inside someone else's log cannot invent a key.
    """
    header: dict[str, str] = {}
    sections: list[str] = []
    observations: dict[str, list[Observation]] = {}
    outcome_raw: str | None = None
    outcome_line = 0
    current = ""
    last_content_line = 0

    lines = text.splitlines()
    for number, line in enumerate(lines, start=1):
        stripped = line.strip()
        if stripped:
            last_content_line = number
        if not stripped:
            continue
        if stripped.startswith(("|", "---")):
            continue

        section = _SECTION.match(line)
        if section:
            current = section.group("title")
            sections.append(current)
            continue

        if line.startswith("#"):
            matched = _HEADER.match(line)
            if not matched:
                continue
            key, value = matched.group("key"), matched.group("value")
            if key == "outcome":
                outcome_raw = value
                outcome_line = number
            else:
                header.setdefault(key, value)
            continue

        observation = _OBSERVATION.match(line)
        if not observation:
            continue
        key = observation.group("key").strip()
        raw = observation.group("value")
        observations.setdefault(key, []).append(
            Observation(key=key, raw=raw, value=typed_value(raw), section=current, line=number)
        )

    for required in ("gate", "title", "recorded"):
        if required not in header:
            raise EvidenceFormatError(f"the narrative has no `# {required}:` header")
    if outcome_raw is None:
        raise EvidenceFormatError("the narrative has no `# outcome:` trailer")
    if outcome_line != last_content_line:
        # A trailer that is not last is a trailer for something other than the
        # whole run — including, at worst, a section that was appended after the
        # verdict was recorded.
        raise EvidenceFormatError("the `# outcome:` trailer is not the last line of the narrative")

    verdict = outcome_raw.split(" ", 1)[0].strip()
    stamped = re.search(r"\((?P<at>[^)]+)\)", outcome_raw)
    return ParsedEvidence(
        gate=header["gate"],
        title=header["title"],
        recorded_at=header["recorded"],
        outcome=verdict,
        outcome_at=stamped.group("at") if stamped else "",
        sections=tuple(sections),
        observations={key: tuple(found) for key, found in observations.items()},
    )
