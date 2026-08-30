"""The repository-owned, versioned catalogue of vault-safety acceptance scenarios.

`docs/operations/vault-safety-evidence.json` is a record of gates a person ran
once, and `scripts/verify_vault_safety_evidence.py` is the harness that refuses
to believe it. This module is the half of that refusal the harness cannot supply
on its own: it says which gates have to exist at all, and, for each one, what
its retained narrative has to *show* before the gate counts as passed.

Why it is a separate, versioned file
------------------------------------

The record names its evidence and hashes it. A hash proves the bytes have not
drifted since they were written; it proves nothing about whether those bytes
describe a scenario that held. Re-hashing a narrative that says
`blocked_marker_written: False` accepts it just as happily as one that says
`True`. So the decisive observations of every scenario are declared here, in the
repository, under version control, and the harness parses each narrative and
re-derives the verdict from those observations rather than reading the gate's
own `outcome`.

`CATALOGUE_VERSION` is recorded in the evidence file and checked for equality.
Tightening a scenario is therefore not a silent change: it invalidates every
record produced under the older catalogue, and the gates have to be run again
against the definition that is now in force.

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

Cross-gate agreements are declared at the bottom: values that several
independent gates observed separately and must therefore report identically.
"""

from __future__ import annotations

import ast
import re
from dataclasses import dataclass
from datetime import datetime

CATALOGUE_VERSION = 2

# --------------------------------------------------------------------------
# policy the scenarios are asserted against
# --------------------------------------------------------------------------

# The debounce and push policy `internal/vaultgit` implements. The gates that
# measure a latency record these numbers too, so a drill run against a build
# with different constants disagrees with the catalogue instead of quietly
# re-baselining what "on time" means.
QUIET_SECONDS_POLICY = 30
MAXIMUM_LATENCY_SECONDS_POLICY = 120
PUSH_INTERVAL_SECONDS_POLICY = 300
PUSH_FAILURE_STREAK_THRESHOLD = 3

# The autosave floor the timing gate is run at, and the canonical token
# `internal/config` normalises it to. Unlike the three constants above this one
# is a *configured* value rather than a compiled-in one, which is exactly why
# the gate records both forms: a narrative that measured a different floor from
# the one it claims to have set is describing a run nobody can reproduce.
AUTOSAVE_FLOOR_SECONDS_POLICY = 300
AUTOSAVE_FLOOR_TOKEN_POLICY = "5m"

# A measured latency is the policy window plus however long the observer took to
# notice the commit. The slack is what separates "later than the policy allows"
# from "the poller was a beat behind"; it is deliberately generous, because the
# property under test is that the ceiling exists at all, not its exact value.
OBSERVATION_SLACK_SECONDS = 60.0

# `.gitignore` is the one tracked path the sealed archive cannot match. The
# archive is a copy of the pre-migration source, and the vault `.gitignore` was
# written *by* the migration to neutralise a global excludesFile that would
# otherwise have silently dropped `brainnotes.toml` from the history. It is Git
# plumbing, not vault content, so it is excluded from the manifest scope rather
# than waived per-run. Anything else appearing here would be a real divergence.
ARCHIVE_MANIFEST_EXCLUSIONS = frozenset({".gitignore"})

# Every path a drill is allowed to have touched. The gates that list the paths
# their commit carried are checked against this, which is the same contract the
# redaction check enforces from the other direction: a drill that committed a
# real note would name it here.
DRILL_PATH = re.compile(r"\A(?:\.bnotes/|vault-safety-drill-)")

# The top-level shape of `blocked.json`, owned by `internal/vaultgit`. The
# redaction gate reports the marker's key set, so it is checked rather than read.
BLOCKED_MARKER_KEYS = [
    "kind",
    "observed_at",
    "operation",
    "paths",
    "reason",
    "recovery",
    "version",
]

# --------------------------------------------------------------------------
# value kinds
# --------------------------------------------------------------------------

BOOL = "bool"
INT = "int"
FLOAT = "float"
TEXT = "text"
HEX64 = "hex64"
OID = "oid"
PATHS = "paths"
STRINGS = "strings"

_HEX64 = re.compile(r"\A[0-9a-f]{64}\Z")
_OID = re.compile(r"\A[0-9a-f]{7,40}\Z")

_UNSET = object()


# --------------------------------------------------------------------------
# narrative parsing
# --------------------------------------------------------------------------

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


def _drill_commit(changed: int) -> tuple[Require, ...]:
    """The observations every gate that asserts "one commit, its paths only" shares."""
    return (
        Require("commit", OID),
        Require("subject", TEXT),
        Require("changed_paths", INT, equals=changed),
        Require("drill_owned_paths", PATHS, each_matches=DRILL_PATH),
        Require("unrelated_paths_swept_in", INT, equals=0),
    )


_SCENARIO_LIST = (
    Scenario(
        name="artifacts_contain_no_note_bodies",
        summary="no artifact class carries a note body, and paths are still recorded",
        required_sections=(
            "act",
            "observe",
            "paths are present, which is the deliberate other half of the contract",
        ),
        requires=(
            Require("secret_body_written_into_two_drill_notes", BOOL, equals=True),
            Require("artifact_classes_produced", TEXT),
            Require("git commit messages -- contains the note body", BOOL, equals=False),
            Require("guard structured log -- contains the note body", BOOL, equals=False),
            Require("safety marker blocked.json -- contains the note body", BOOL, equals=False),
            Require("safety marker watch.json -- contains the note body", BOOL, equals=False),
            Require("undo journal manifests -- contains the note body", BOOL, equals=False),
            Require("reconstructed notification body -- contains the note body", BOOL, equals=False),
            Require("reconstructed notification body -- names any path", BOOL, equals=False),
            Require("undo journal manifests -- bytes", INT, minimum=1),
            Require("blocked.json top-level keys", STRINGS, equals=BLOCKED_MARKER_KEYS),
            Require("marker records the affected path count", INT, minimum=1),
            Require("marker records paths (drill-owned, listed by count only)", BOOL, equals=True),
        ),
    ),
    Scenario(
        name="bn_doctor",
        summary="the vault hygiene check runs clean against the live vault",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("output_lines", INT, minimum=1),
            Require("stderr_empty", BOOL, equals=True),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="brainnotes_ci",
        summary="the full BrainNotes validation envelope passes",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="cli_mutation_commits_immediately",
        summary="a CLI note mutation commits its own paths before it returns",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=1),
            Require("working_tree_entries_left_dirty", INT, equals=0),
            Require("frontmatter_applied", BOOL, equals=True),
            *_drill_commit(changed=3),
        ),
        relations=(
            Relation("commits_after", "==", ("commits_before",), delta=1),
            Relation("len(drill_owned_paths)", "==", ("changed_paths",)),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="commit_and_marker_failure_blocks_after_restart",
        summary="a failed commit whose block marker also failed still refuses the next write",
        required_sections=(
            "act",
            "observe: the marker never landed, the intent did",
            "the filesystem is healthy again; the manager restarts",
        ),
        requires=(
            Require("attempts_needed_to_hit_the_window", INT, minimum=1),
            Require("action", TEXT),
            Require("exit_status", INT, equals=1),
            Require("blocked_marker_is_still_the_planted_placeholder", BOOL, equals=True),
            Require("intent_record_present", BOOL, equals=True),
            Require("intent_phase", TEXT, equals="planned"),
            Require("intent_path_count", INT, minimum=1),
            Require("intent_names_a_commit", BOOL, equals=False),
            Require("intent_recovery_present", BOOL, equals=True),
            Require("intent_operation", TEXT, equals="commit-journal"),
            Require("marker_placeholder_removed", BOOL, equals=True),
            Require("later_write_exit_status", INT, equals=1),
            Require("refusal_names_an_interrupted_snapshot", BOOL, equals=True),
            Require("later_write_left_target_untouched", BOOL, equals=True),
            Require("bytes_of_the_first_write_are_on_disk", BOOL, equals=True),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="debounce_survives_daemon_restart",
        summary="killing the daemon during a debounce cannot postpone the snapshot",
        requires=(
            Require("persisted_first_change_at_recorded", BOOL, equals=True),
            Require("daemon_kills_during_debounce", INT, minimum=1),
            Require("first_change_at_unchanged_across_restarts", BOOL, equals=True),
            Require(
                "seconds_from_first_change_to_commit",
                FLOAT,
                minimum=0.0,
                maximum=MAXIMUM_LATENCY_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            *_drill_commit(changed=1),
        ),
        relations=(Relation("len(drill_owned_paths)", "==", ("changed_paths",)),),
    ),
    Scenario(
        name="durable_state_commits_immediately",
        summary="a durable sidecar-state write commits that path and nothing else",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=1),
            Require("staged_exactly", TEXT, equals=".bnotes/recall.json"),
            *_drill_commit(changed=1),
        ),
        relations=(
            Relation("commits_after", "==", ("commits_before",), delta=1),
            Relation("len(drill_owned_paths)", "==", ("changed_paths",)),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="external_edit_scheduled_by_readiness_and_floor",
        summary="an external edit commits once it is both ready and past the autosave floor",
        # Two sections because the schedule is two independent conditions, and a
        # drill that measured only the first would still look like a passing
        # timing gate. `due_at = max(ready_at, floor_at)`: the first section
        # holds `floor_at` out of the way and measures readiness on its own; the
        # second makes the change ready early and measures the floor deferring
        # it. Neither section on its own distinguishes the two.
        required_sections=(
            "readiness alone: the first external edit after a quiet history",
            "the floor: an edit that is ready long before the floor allows a commit",
        ),
        requires=(
            # The policy each section ran under, recorded from the guard's own
            # published record rather than from the drill's intent, so a floor
            # that never took effect cannot be reported as one that did.
            Require("quiet_seconds_policy", INT, equals=QUIET_SECONDS_POLICY, count=2),
            Require(
                "maximum_latency_seconds_policy",
                INT,
                equals=MAXIMUM_LATENCY_SECONDS_POLICY,
                count=2,
            ),
            Require(
                "autosave_floor_seconds_policy",
                INT,
                equals=AUTOSAVE_FLOOR_SECONDS_POLICY,
                count=2,
            ),
            Require(
                "published_autosave_min_interval",
                TEXT,
                equals=AUTOSAVE_FLOOR_TOKEN_POLICY,
                count=2,
            ),
            Require("external_writes_during_burst", INT, minimum=1, count=2),
            Require("commit_landed", BOOL, equals=True, count=2),
            # Section one. With no previous external autosave the floor has
            # nothing to be measured from, so readiness alone decides and the
            # commit lands inside the window the fixed constants define.
            Require("previous_external_autosave_recorded", BOOL, equals=False),
            Require(
                "seconds_from_first_change_to_commit",
                FLOAT,
                minimum=QUIET_SECONDS_POLICY,
                maximum=MAXIMUM_LATENCY_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            # Section two, and the decisive half of the gate. The change is
            # ready within 30 seconds and would have been committed within two
            # minutes under readiness alone; the floor pushes the published due
            # time past that ceiling, and the commit lands where the due time
            # said it would rather than where readiness did.
            Require("due_time_was_deferred_past_readiness", BOOL, equals=True),
            Require(
                "seconds_from_first_change_to_published_due",
                FLOAT,
                minimum=MAXIMUM_LATENCY_SECONDS_POLICY,
                maximum=AUTOSAVE_FLOOR_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            Require(
                "seconds_from_previous_autosave_to_published_due",
                FLOAT,
                minimum=AUTOSAVE_FLOOR_SECONDS_POLICY,
                maximum=AUTOSAVE_FLOOR_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            Require(
                "seconds_from_first_change_to_deferred_commit",
                FLOAT,
                minimum=MAXIMUM_LATENCY_SECONDS_POLICY,
                maximum=AUTOSAVE_FLOOR_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            Require(
                "seconds_from_previous_autosave_to_deferred_commit",
                FLOAT,
                minimum=AUTOSAVE_FLOOR_SECONDS_POLICY,
                maximum=AUTOSAVE_FLOOR_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            # A floor that deferred nothing because the guard committed twice in
            # between would satisfy every duration above. This is what says the
            # wait was one wait.
            Require("commits_between_the_two_autosaves", INT, equals=0),
            Require("commit", OID, count=2),
            Require("subject", TEXT, count=2),
            Require("changed_paths", INT, equals=1, count=2),
            Require("drill_owned_paths", PATHS, each_matches=DRILL_PATH, count=2),
            Require("unrelated_paths_swept_in", INT, equals=0, count=2),
        ),
        relations=(Relation("len(drill_owned_paths)", "==", ("changed_paths",)),),
    ),
    Scenario(
        name="fresh_clone_of_content_remote",
        summary="a fresh clone of the content remote is complete and at the live head",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("clone_HEAD", OID),
            Require("live_HEAD", OID),
            Require("clone_is_at_the_live_head", BOOL, equals=True),
            Require("tracked_paths_in_the_clone", INT, minimum=1),
            Require("clone_is_clean", BOOL, equals=True),
            Require("git_fsck_exit_status", INT, equals=0),
        ),
        same_value=(("clone_HEAD", "live_HEAD"),),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="history_restore_commits_immediately",
        summary="an undo of a journal entry commits its own paths",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=1),
            Require("restored_frontmatter_removed", BOOL, equals=True),
            *_drill_commit(changed=3),
        ),
        relations=(
            Relation("commits_after", "==", ("commits_before",), delta=1),
            Relation("len(drill_owned_paths)", "==", ("changed_paths",)),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="interrupted_delete_recovered_by_watcher",
        summary="a rename interrupted before its commit is finished by the watcher, not blocked",
        required_sections=("act", "observe: the watcher finishes it rather than blocking"),
        requires=(
            Require("action", TEXT),
            Require("commits_after_reset", INT, minimum=0),
            Require("working_tree_entries_pending", INT, minimum=1),
            Require("old_path_present", BOOL, equals=False),
            Require("new_path_present", BOOL, equals=True),
            Require("recovery_commit_landed", BOOL, equals=True),
            Require(
                "seconds_to_recovery_commit",
                FLOAT,
                minimum=0.0,
                maximum=MAXIMUM_LATENCY_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            Require("blocked_marker_written", BOOL, equals=False),
            Require("deletion_of_old_path_committed", BOOL, equals=True),
            *_drill_commit(changed=4),
        ),
        relations=(Relation("len(drill_owned_paths)", "==", ("changed_paths",)),),
    ),
    Scenario(
        name="local_commit_failure_blocks_later_writes",
        summary="a snapshot that fails after the bytes land blocks and refuses the next write",
        required_sections=(
            "act",
            "observe",
            "the next write is refused even though Git is healthy again",
        ),
        requires=(
            Require("action", TEXT),
            Require("exit_status", INT, equals=1),
            Require("error_says_change_is_on_disk", BOOL, equals=True),
            Require("error_says_writes_are_blocked", BOOL, equals=True),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=0),
            Require("bytes_landed_on_disk", BOOL, equals=True),
            Require("blocked_marker_written", BOOL, equals=True),
            Require("marker_kind", TEXT, equals="commit_failed"),
            Require("marker_operation", TEXT, equals="commit-journal"),
            Require("marker_path_count", INT, minimum=0),
            Require("later_write_exit_status", INT, equals=1),
            Require("later_write_left_target_untouched", BOOL, equals=True),
        ),
        # The commit is the thing that failed, so the history must not have
        # moved. A narrative claiming both a failure and a new commit is
        # describing two different runs.
        relations=(Relation("commits_after", "==", ("commits_before",)),),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="old_vault_trashed_recoverably",
        summary="the old File Provider root is out of use, recoverable, and lost nothing",
        required_sections=(
            "before: nothing is still using the old root",
            "nothing in the old root is only in the old root",
            "the sealed archive is verified one last time, then the old root is moved",
            "after: the trashed copy is byte-for-byte what was moved",
        ),
        requires=(
            Require("processes_referencing_the_old_root", INT, equals=0),
            Require("bn_config_exit_status", INT, equals=0),
            Require("bn_config_names_the_old_root", BOOL, equals=False),
            Require("bn_config_names_the_live_vault", BOOL, equals=True),
            Require("launchagent_names_the_old_root", BOOL, equals=False),
            Require("launchagent_names_the_live_vault", BOOL, equals=True),
            Require("files_in_the_old_root", INT, minimum=1),
            Require("files_byte_present_in_the_live_history_or_the_sealed_archive", INT, minimum=1),
            Require("machinery_files_classified_by_path", INT, minimum=0),
            Require("unaccounted_files", INT, equals=0),
            Require(
                "approved_old_vault_manifest_sha256_matches_the_migration_record",
                BOOL,
                equals=True,
            ),
            Require("old_root_still_in_place", BOOL, equals=False),
            Require("trash_location", TEXT),
            Require("trash_location_is_a_directory", BOOL, equals=True),
            Require("files_before_the_move", INT, minimum=1),
            Require("files_after_the_move", INT, minimum=1),
            Require("object_names_identical_after_the_move", BOOL, equals=True),
            Require("sealed_archive_still_present", BOOL, equals=True),
        ),
        relations=(
            # Every file is accounted for by exactly one class, so the census
            # has to add up rather than merely report a zero at the end.
            Relation(
                "files_in_the_old_root",
                "==",
                (
                    "files_byte_present_in_the_live_history_or_the_sealed_archive",
                    "machinery_files_classified_by_path",
                    "unaccounted_files",
                ),
            ),
            Relation("files_after_the_move", "==", ("files_before_the_move",)),
            Relation("files_before_the_move", "==", ("files_in_the_old_root",)),
        ),
    ),
    Scenario(
        name="post_retirement_verification",
        summary="health, push, clone and both restore manifests still hold after retirement",
        required_sections=("health", "push", "fresh clone and restore manifests"),
        requires=(
            Require("health_command_exit_status", INT, equals=0),
            Require("health_verdict", TEXT, equals="OK"),
            Require("push_exit_status", INT, equals=0),
            Require("remote_tip_matches_local_head", BOOL, equals=True),
            Require("fresh_clone_exit_status", INT, equals=0),
            Require("tracked_paths_at_HEAD", INT, minimum=1),
            Require("live_vault_manifest_sha256", HEX64),
            Require("fresh_clone_manifest_sha256", HEX64),
            Require("identical", BOOL, equals=True),
            Require("baseline_clone_manifest_sha256", HEX64),
            Require("sealed_archive_manifest_sha256", HEX64),
            Require("identical_at_baseline", BOOL, equals=True),
        ),
        same_value=(
            ("live_vault_manifest_sha256", "fresh_clone_manifest_sha256"),
            ("baseline_clone_manifest_sha256", "sealed_archive_manifest_sha256"),
        ),
    ),
    Scenario(
        name="remote_outage_recovers_automatically",
        summary="an outage never blocks a local mutation and the backlog pushes itself",
        required_sections=("act: take the remote away", "observe: restore the remote and wait"),
        requires=(
            Require("local_mutation_exit_status", INT, equals=0),
            Require("local_commit_landed_during_outage", BOOL, equals=True),
            Require("bytes_available_during_outage", BOOL, equals=True),
            Require("external_edit_rounds_during_outage", INT, minimum=1),
            Require("recorded_push_attempts_during_outage", INT, minimum=1),
            Require("guard_push_failed_warnings", INT, minimum=1),
            Require("guard_push_failing_streak_notifications", INT, minimum=1),
            Require("streak_notification_threshold", INT, equals=PUSH_FAILURE_STREAK_THRESHOLD),
            Require("local_commits_kept_being_taken_during_the_outage", INT, minimum=1),
            Require("guard_still_running", BOOL, equals=True),
            Require(
                "seconds_to_automatic_push_after_recovery",
                FLOAT,
                minimum=0.0,
                maximum=PUSH_INTERVAL_SECONDS_POLICY + OBSERVATION_SLACK_SECONDS,
            ),
            Require("remote_ref_matches_local_head", BOOL, equals=True),
            Require("push_retry_state_cleared", BOOL, equals=True),
        ),
        relations=(
            # The streak notification is a threshold, so the warnings it counted
            # have to have actually happened.
            Relation("guard_push_failed_warnings", ">=", ("streak_notification_threshold",)),
        ),
    ),
    Scenario(
        name="restore_from_github_clone",
        summary="a fresh clone of the content remote reproduces the live vault exactly",
        requires=(
            Require("method", TEXT),
            Require("remote", TEXT),
            Require("tracked_paths_at_HEAD", INT, minimum=1),
            Require("tracked_paths_at_baseline", INT, minimum=1),
            Require("baseline_commit", OID),
            Require(
                "paths_excluded_from_the_archive_comparison",
                STRINGS,
                equals=sorted(ARCHIVE_MANIFEST_EXCLUSIONS),
            ),
            Require("live_vault_manifest_sha256", HEX64),
            Require("github_restore_manifest_sha256", HEX64),
            Require("identical_to_live_vault", BOOL, equals=True),
            Require("identical_to_the_local_git_restore", BOOL, equals=True),
        ),
        same_value=(("live_vault_manifest_sha256", "github_restore_manifest_sha256"),),
    ),
    Scenario(
        name="restore_from_local_git",
        summary="a fresh restore from the local history reproduces the live vault exactly",
        requires=(
            Require("method", TEXT),
            Require("tracked_paths_at_HEAD", INT, minimum=1),
            Require("tracked_paths_at_baseline", INT, minimum=1),
            Require("baseline_commit", OID),
            Require(
                "paths_excluded_from_the_archive_comparison",
                STRINGS,
                equals=sorted(ARCHIVE_MANIFEST_EXCLUSIONS),
            ),
            Require("live_vault_manifest_sha256", HEX64),
            Require("local_restore_manifest_sha256", HEX64),
            Require("identical_to_live_vault", BOOL, equals=True),
        ),
        same_value=(("live_vault_manifest_sha256", "local_restore_manifest_sha256"),),
    ),
    Scenario(
        name="restore_from_sealed_archive",
        summary="the sealed archive reproduces the vault's baseline commit exactly",
        required_sections=("act", "observe", "the archive's own SHA256SUMS still verifies"),
        requires=(
            Require("method", TEXT),
            Require("archive_is_destination_agnostic", BOOL, equals=True),
            Require("tracked_paths_at_HEAD", INT, minimum=1),
            Require("tracked_paths_at_baseline", INT, minimum=1),
            Require("baseline_commit", OID),
            Require(
                "paths_excluded_from_the_archive_comparison",
                STRINGS,
                equals=sorted(ARCHIVE_MANIFEST_EXCLUSIONS),
            ),
            Require("baseline_local_restore_manifest_sha256", HEX64),
            Require("baseline_github_restore_manifest_sha256", HEX64),
            Require("sealed_archive_manifest_sha256", HEX64),
            Require("all_three_identical", BOOL, equals=True),
            Require("shasum_c_exit_status", INT, equals=0),
        ),
        same_value=(
            (
                "baseline_local_restore_manifest_sha256",
                "baseline_github_restore_manifest_sha256",
                "sealed_archive_manifest_sha256",
            ),
        ),
    ),
    Scenario(
        name="scripts_complete_suite",
        summary="the automation repository's complete suite passes, guard module included",
        requires=(
            Require("command", TEXT),
            Require("exit_status", INT, equals=0),
            Require("brainnotes guard shard result", TEXT, each_matches=re.compile(r"\Aok\b")),
        ),
        exit_status_key="exit_status",
    ),
    Scenario(
        name="txn_crash_converges_and_is_idempotent",
        summary="every durable transaction phase converges on reopen and stays converged",
        # One section per documented crash point. A drill that stopped
        # exercising one would still hash cleanly, so the sections are the check.
        required_sections=(
            "crash point intent -- record durable, no destination reached yet",
            "crash point applied:0 -- first destination reached, second still holding prior bytes",
            "crash point synced -- every destination reached, record not yet cleared",
        ),
        requires=(
            Require("intent_record_present_before_reopen", BOOL, equals=True, count=3),
            Require("reopen_exit_status", INT, equals=0, count=3),
            Require("both_destinations_hold_planned_bytes", BOOL, equals=True, count=3),
            Require("intent_record_cleared", BOOL, equals=True, count=3),
            Require("second_reopen_exit_status", INT, equals=0, count=3),
            Require("still_holds_planned_bytes_after_second_reopen", BOOL, equals=True, count=3),
        ),
    ),
    Scenario(
        name="unexplained_deletion_blocks",
        summary="a deletion with no journal record blocks and is never committed or pushed",
        required_sections=("act", "observe", "a later bn write is refused while the block stands"),
        requires=(
            Require("action", TEXT),
            Require("blocked_marker_appeared", BOOL, equals=True),
            Require(
                "seconds_to_block",
                FLOAT,
                minimum=0.0,
                maximum=OBSERVATION_SLACK_SECONDS,
            ),
            Require("marker_kind", TEXT, equals="unexplained_deletion"),
            Require("marker_operation", TEXT, equals="scan-pending"),
            Require("marker_reason", TEXT),
            Require("marker_path_count", INT, minimum=1),
            Require("marker_recovery_present", BOOL, equals=True),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=0),
            Require("origin_ref_moved", BOOL, equals=False),
            Require("note_still_recoverable_from_HEAD", BOOL, equals=True),
            Require("later_write_exit_status", INT, equals=1),
            Require("guard_blocked_events", INT, minimum=1),
        ),
        # "never committed" is the property; a narrative that reported a block
        # and a new commit in the same breath has not shown it.
        relations=(Relation("commits_after", "==", ("commits_before",)),),
    ),
    Scenario(
        name="web_mutation_commits_immediately",
        summary="a mutation through the web API commits its own paths",
        requires=(
            Require("request", TEXT),
            Require("http_status", INT, equals=200),
            Require("api_message", TEXT),
            Require("commits_before", INT, minimum=0),
            Require("commits_after", INT, minimum=1),
            Require("stub_note_created", BOOL, equals=True),
            Require("working_tree_entries_left_dirty", INT, equals=0),
            *_drill_commit(changed=2),
        ),
        relations=(
            Relation("commits_after", "==", ("commits_before",), delta=1),
            Relation("len(drill_owned_paths)", "==", ("changed_paths",)),
        ),
    ),
)

SCENARIOS: dict[str, Scenario] = {scenario.name: scenario for scenario in _SCENARIO_LIST}
REQUIRED_GATES = frozenset(SCENARIOS)

# Values several gates observed independently and must therefore agree on. Each
# entry is a label and the `(gate, observation)` pairs that have to be identical.
# These are the checks that catch a single narrative being rewritten: the other
# gates that saw the same number are still saying what it was.
CROSS_GATE_AGREEMENTS: tuple[tuple[str, tuple[tuple[str, str], ...]], ...] = (
    (
        "the manifest over the live vault at HEAD",
        (
            ("restore_from_local_git", "live_vault_manifest_sha256"),
            ("restore_from_github_clone", "live_vault_manifest_sha256"),
            ("post_retirement_verification", "live_vault_manifest_sha256"),
        ),
    ),
    (
        "the manifest over the pre-migration baseline",
        (
            ("restore_from_sealed_archive", "sealed_archive_manifest_sha256"),
            ("post_retirement_verification", "sealed_archive_manifest_sha256"),
        ),
    ),
    (
        "the number of tracked paths at HEAD",
        (
            ("restore_from_local_git", "tracked_paths_at_HEAD"),
            ("restore_from_github_clone", "tracked_paths_at_HEAD"),
            ("restore_from_sealed_archive", "tracked_paths_at_HEAD"),
            ("post_retirement_verification", "tracked_paths_at_HEAD"),
            ("fresh_clone_of_content_remote", "tracked_paths_in_the_clone"),
        ),
    ),
    (
        "the number of tracked paths at the baseline",
        (
            ("restore_from_local_git", "tracked_paths_at_baseline"),
            ("restore_from_github_clone", "tracked_paths_at_baseline"),
            ("restore_from_sealed_archive", "tracked_paths_at_baseline"),
        ),
    ),
    (
        "the pre-migration baseline commit",
        (
            ("restore_from_local_git", "baseline_commit"),
            ("restore_from_github_clone", "baseline_commit"),
            ("restore_from_sealed_archive", "baseline_commit"),
        ),
    ),
    (
        "the census of the old root",
        (
            ("old_vault_trashed_recoverably", "files_in_the_old_root"),
            ("old_vault_trashed_recoverably", "files_before_the_move"),
        ),
    ),
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


def cross_gate_disagreements(parsed_by_gate: dict[str, ParsedEvidence]) -> list[str]:
    """Values separate gates observed independently and must report identically."""
    problems: list[str] = []
    for label, pairs in CROSS_GATE_AGREEMENTS:
        values: dict[str, str] = {}
        for gate, key in pairs:
            parsed = parsed_by_gate.get(gate)
            observation = parsed.first(key) if parsed else None
            if observation is None:
                problems.append(f"{label}: gate {gate} does not record {key}")
                break
            values[f"{gate}.{key}"] = observation.raw
        else:
            if len(set(values.values())) > 1:
                disagreeing = ", ".join(
                    f"{name}={value}" for name, value in sorted(values.items())
                )
                problems.append(f"{label} is reported inconsistently: {disagreeing}")
    return problems
