"""The repository-owned, versioned catalogue of vault-safety acceptance scenarios.

`docs/operations/vault-safety-evidence.json` is a record of gates a person ran
once, and `scripts/verify_vault_safety_evidence.py` is the harness that refuses
to believe it. This module is the half of that refusal the harness cannot supply
on its own: it says which gates have to exist at all, and, for each one, what
its retained narrative has to *show* before the gate counts as passed.

Why it is a separate, versioned surface
---------------------------------------

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

Where the parts live
--------------------

This module is the assembled catalogue and the stable import surface. The
pieces it assembles are siblings in `scripts/`:

* `vault_safety_policy` — the constants the scenarios are asserted against
  (the debounce, push and autosave policy `internal/vaultgit` implements) and
  the observation value kinds.
* `vault_safety_narrative` — reading a retained gate narrative back into
  observations.
* `vault_safety_scenario_model` — what a scenario declares (`Require`,
  `Relation`, `Scenario`) and how a narrative is judged by it.
* `vault_safety_catalogue_mutation`, `vault_safety_catalogue_failure`,
  `vault_safety_catalogue_restore` — the scenarios themselves, grouped by what
  the gate is trying to show.

Cross-gate agreements are declared below: values that several independent gates
observed separately and must therefore report identically.
"""

from __future__ import annotations

from vault_safety_catalogue_failure import FAILURE_SCENARIOS
from vault_safety_catalogue_mutation import MUTATION_SCENARIOS
from vault_safety_catalogue_restore import RESTORE_SCENARIOS
from vault_safety_narrative import (
    EvidenceFormatError,
    Observation,
    ParsedEvidence,
    parse_evidence,
    typed_value,
)
from vault_safety_policy import ARCHIVE_MANIFEST_EXCLUSIONS, CATALOGUE_VERSION
from vault_safety_scenario_model import (
    Relation,
    Require,
    Scenario,
    unsupported_claims,
    validate_evidence,
)

__all__ = [
    "ARCHIVE_MANIFEST_EXCLUSIONS",
    "CATALOGUE_VERSION",
    "CROSS_GATE_AGREEMENTS",
    "EvidenceFormatError",
    "Observation",
    "ParsedEvidence",
    "REQUIRED_GATES",
    "Relation",
    "Require",
    "SCENARIOS",
    "Scenario",
    "cross_gate_disagreements",
    "parse_evidence",
    "typed_value",
    "unsupported_claims",
    "validate_evidence",
]

# Sorted by name so the catalogue reads in one order regardless of which group a
# scenario was written into.
_SCENARIO_LIST = tuple(
    sorted(
        (*MUTATION_SCENARIOS, *FAILURE_SCENARIOS, *RESTORE_SCENARIOS),
        key=lambda scenario: scenario.name,
    )
)

SCENARIOS: dict[str, Scenario] = {scenario.name: scenario for scenario in _SCENARIO_LIST}
REQUIRED_GATES = frozenset(SCENARIOS)

if len(SCENARIOS) != len(_SCENARIO_LIST):
    # Two groups claiming the same gate name would silently drop one scenario's
    # requirements while still reporting the gate as covered.
    raise RuntimeError("the scenario catalogue declares the same gate name twice")

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
