"""What a vault-safety scenario declares, and how a narrative is judged by it.

The record names its evidence and hashes it. A hash proves the bytes have not
drifted since they were written; it proves nothing about whether those bytes
describe a scenario that held. Re-hashing a narrative that says
`blocked_marker_written: False` accepts it just as happily as one that says
`True`. So the decisive observations of every scenario are declared in the
catalogue modules, in the repository, under version control, and the functions
here parse each narrative and re-derive the verdict from those observations
rather than reading the gate's own `outcome`.

What a scenario declares
------------------------

* `required_sections` — narrative sections that must be present. A drill that
  quietly stopped exercising its third crash point loses a section, and losing
  a section is a failure rather than an omission nobody notices.
* `requires` — the decisive observations: key, value kind, the value the gate
  is claiming, how many times it must appear, and numeric bounds where a
  duration is the property under test.
* `relations` — arithmetic that has to hold *between* observations, so a
  narrative cannot claim a commit landed and also report the same commit count
  before and after.
* `same_value` — observations that must agree with each other verbatim, which
  is how "two independent restores produced the same manifest" is checked
  rather than believed.
* `expected_false` — the complete set of booleans this scenario is allowed to
  report as `False`. Every other boolean in the narrative must be `True`: in
  these files a `False` is a negative finding, and one that no scenario planned
  for is a failure even when it is not a key anybody thought to require.
* `exit_status_key` — the observation that substantiates the record's
  `exit_status`, or `None` when nothing in the narrative can. A record may not
  carry an exit status the harness cannot check against something.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from datetime import datetime

from vault_safety_narrative import Observation, ParsedEvidence
from vault_safety_policy import (
    BOOL,
    DRILL_PATH,
    FLOAT,
    HEX64,
    INT,
    OID,
    PATHS,
    STRINGS,
    TEXT,
)

_HEX64 = re.compile(r"\A[0-9a-f]{64}\Z")
_OID = re.compile(r"\A[0-9a-f]{7,40}\Z")

_UNSET = object()


# --------------------------------------------------------------------------
# scenario definitions
# --------------------------------------------------------------------------


@dataclass(frozen=True)
class Require:
    """One decisive observation a scenario's narrative has to carry."""

    key: str
    kind: str
    equals: object = _UNSET
    minimum: float | None = None
    maximum: float | None = None
    count: int = 1
    each_matches: re.Pattern[str] | None = None


@dataclass(frozen=True)
class Relation:
    """Arithmetic between observations: `left <op> sum(right) + delta`.

    An operand is an observation key, or `len(key)` for a list-valued one.
    """

    left: str
    op: str
    right: tuple[str, ...]
    delta: int = 0


@dataclass(frozen=True)
class Scenario:
    name: str
    summary: str
    requires: tuple[Require, ...]
    required_sections: tuple[str, ...] = ()
    relations: tuple[Relation, ...] = ()
    same_value: tuple[tuple[str, ...], ...] = ()
    expected_false: frozenset[str] = frozenset()
    exit_status_key: str | None = None

    @property
    def allowed_false(self) -> frozenset[str]:
        declared = {req.key for req in self.requires if req.equals is False}
        return frozenset(declared | set(self.expected_false))


def drill_commit(changed: int) -> tuple[Require, ...]:
    """The observations every gate that asserts "one commit, its paths only" shares."""
    return (
        Require("commit", OID),
        Require("subject", TEXT),
        Require("changed_paths", INT, equals=changed),
        Require("drill_owned_paths", PATHS, each_matches=DRILL_PATH),
        Require("unrelated_paths_swept_in", INT, equals=0),
    )


# --------------------------------------------------------------------------
# validation
# --------------------------------------------------------------------------


def _kind_problem(observation: Observation, kind: str) -> str | None:
    raw, value = observation.raw, observation.value
    if kind == BOOL:
        return None if isinstance(value, bool) else f"is {raw!r}, not True or False"
    if kind == INT:
        if isinstance(value, bool) or not isinstance(value, int):
            return f"is {raw!r}, not an integer"
        return None
    if kind == FLOAT:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            return f"is {raw!r}, not a number"
        return None
    if kind == HEX64:
        return None if _HEX64.match(raw) else f"is {raw!r}, not 64 lowercase hex characters"
    if kind == OID:
        return None if _OID.match(raw) else f"is {raw!r}, not a Git object name"
    if kind in (PATHS, STRINGS):
        if not isinstance(value, list) or not all(isinstance(item, str) for item in value):
            return f"is {raw!r}, not a list of strings"
        return None
    if kind == TEXT:
        return None if raw else "is empty"
    raise ValueError(f"unknown observation kind {kind!r}")


def _numeric(observation: Observation) -> float | None:
    if isinstance(observation.value, bool):
        return None
    if isinstance(observation.value, (int, float)):
        return float(observation.value)
    if isinstance(observation.value, list):
        return float(len(observation.value))
    return None


def _check_require(scenario: Scenario, parsed: ParsedEvidence, require: Require) -> list[str]:
    found = parsed.observations.get(require.key, ())
    if len(found) < require.count:
        return [
            (
                f"observation {require.key!r} appears {len(found)} time(s), "
                f"but the scenario requires {require.count}"
            )
        ]

    problems: list[str] = []
    for observation in found:
        where = f"observation {require.key!r} on line {observation.line}"
        kind_problem = _kind_problem(observation, require.kind)
        if kind_problem:
            problems.append(f"{where} {kind_problem}")
            continue
        if require.equals is not _UNSET:
            expected = require.equals
            actual = observation.value
            if require.kind in (PATHS, STRINGS):
                actual = sorted(actual)
                expected = sorted(expected)
            elif require.kind in (TEXT, HEX64, OID):
                actual = observation.raw
            if actual != expected:
                problems.append(f"{where} is {actual!r}, but the scenario requires {expected!r}")
                continue
        number = _numeric(observation)
        if require.minimum is not None and (number is None or number < require.minimum):
            problems.append(f"{where} is {observation.raw!r}, below the required {require.minimum}")
        if require.maximum is not None and (number is None or number > require.maximum):
            problems.append(f"{where} is {observation.raw!r}, above the allowed {require.maximum}")
        if require.each_matches is not None:
            values = observation.value if require.kind in (PATHS, STRINGS) else [observation.raw]
            for item in values:
                if not require.each_matches.search(item):
                    # A path here is by definition one the drill should have
                    # owned, so it is safe to name; a value that is not a path
                    # is the whole point of the failure.
                    problems.append(f"{where} carries {item!r}, which the scenario does not allow")
    return problems


def _check_relation(parsed: ParsedEvidence, relation: Relation) -> list[str]:
    operands = [relation.left, *relation.right]
    resolved: dict[str, tuple[Observation, ...]] = {}
    for operand in operands:
        key = operand[4:-1] if operand.startswith("len(") and operand.endswith(")") else operand
        found = parsed.observations.get(key, ())
        if not found:
            return [f"relation operand {operand!r} is missing from the narrative"]
        resolved[operand] = found

    width = max(len(found) for found in resolved.values())
    for operand, found in resolved.items():
        if len(found) not in (1, width):
            return [
                (
                    f"relation operand {operand!r} appears {len(found)} time(s), "
                    f"which cannot be paired with {width}"
                )
            ]

    problems: list[str] = []
    for index in range(width):
        values: dict[str, float] = {}
        for operand, found in resolved.items():
            observation = found[index] if len(found) == width else found[0]
            number = _numeric(observation)
            if number is None:
                problems.append(f"relation operand {operand!r} is {observation.raw!r}, not a number")
                break
            values[operand] = number
        else:
            left = values[relation.left]
            right = sum(values[operand] for operand in relation.right) + relation.delta
            expression = " + ".join(relation.right)
            if relation.delta:
                expression += f" + {relation.delta}"
            if relation.op == "==" and left != right:
                problems.append(f"{relation.left} is {left:g}, but {expression} is {right:g}")
            elif relation.op == ">=" and left < right:
                problems.append(f"{relation.left} is {left:g}, below {expression} at {right:g}")
            elif relation.op == "<=" and left > right:
                problems.append(f"{relation.left} is {left:g}, above {expression} at {right:g}")
    return problems


def validate_evidence(scenario: Scenario, parsed: ParsedEvidence) -> list[str]:
    """Re-derive a gate's verdict from its narrative. Empty means it holds."""
    problems: list[str] = []

    if parsed.gate != scenario.name:
        problems.append(f"the narrative is headed `# gate: {parsed.gate}`, not {scenario.name}")
    if parsed.outcome != "pass":
        problems.append(f"the narrative records outcome {parsed.outcome!r}")
    if parsed.outcome_at and parsed.recorded_at:
        try:
            started = datetime.fromisoformat(parsed.recorded_at.replace("Z", "+00:00"))
            finished = datetime.fromisoformat(parsed.outcome_at.replace("Z", "+00:00"))
        except ValueError:
            problems.append("the narrative's recorded and outcome timestamps are not ISO-8601")
        else:
            if finished < started:
                problems.append("the narrative's outcome predates the run it is the outcome of")

    for title in scenario.required_sections:
        if title not in parsed.sections:
            problems.append(f"the narrative has no `## {title}` section")

    for require in scenario.requires:
        problems.extend(_check_require(scenario, parsed, require))

    for relation in scenario.relations:
        problems.extend(_check_relation(parsed, relation))

    for group in scenario.same_value:
        seen = {}
        for key in group:
            observation = parsed.first(key)
            if observation is None:
                problems.append(f"observation {key!r} is missing from the narrative")
                break
            seen[key] = observation.raw
        else:
            if len(set(seen.values())) > 1:
                problems.append(
                    "observations that must agree do not: " + ", ".join(sorted(seen))
                )

    allowed_false = scenario.allowed_false
    for key, found in parsed.observations.items():
        if key in allowed_false:
            continue
        for observation in found:
            if observation.value is False:
                problems.append(
                    f"observation {key!r} on line {observation.line} is False, and the "
                    "scenario does not allow that as a passing result"
                )
    return problems


def unsupported_claims(scenario: Scenario, gate: dict, parsed: ParsedEvidence) -> list[str]:
    """Claims the record makes that its own evidence does not substantiate.

    `outcome` and `exit_status` used to be taken at face value; a record that
    verifies itself is worth nothing, so each one is now either corroborated by
    the retained narrative or refused. A gate whose narrative records no exit
    status may not claim one, because there is nothing to check it against.
    """
    problems: list[str] = []

    if gate.get("outcome") is not True:
        problems.append(f"outcome is {gate.get('outcome')!r}, not true")
    elif parsed.outcome != "pass":
        problems.append(f"outcome claims true but the narrative records {parsed.outcome!r}")

    checked_at = gate.get("checked_at")
    if checked_at != parsed.recorded_at:
        problems.append(
            f"checked_at is {checked_at!r} but the narrative was recorded at "
            f"{parsed.recorded_at!r}"
        )

    claimed = gate.get("exit_status", _UNSET)
    if scenario.exit_status_key is None:
        if claimed is not _UNSET:
            problems.append(
                f"exit_status is {claimed!r}, but this gate's narrative records no single "
                "exit status that could substantiate it"
            )
    else:
        observation = parsed.first(scenario.exit_status_key)
        if observation is None:
            problems.append(
                f"the narrative records no {scenario.exit_status_key!r} to substantiate an "
                "exit status"
            )
        elif claimed is _UNSET:
            problems.append(
                f"exit_status is missing, but the narrative records "
                f"{scenario.exit_status_key} {observation.raw}"
            )
        elif isinstance(claimed, bool) or not isinstance(claimed, int):
            problems.append(f"exit_status must be an integer, got {claimed!r}")
        elif claimed != observation.value:
            problems.append(
                f"exit_status is {claimed!r} but the narrative records "
                f"{scenario.exit_status_key} {observation.raw}"
            )
    return problems
