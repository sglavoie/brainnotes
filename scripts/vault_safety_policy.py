"""The policy the vault-safety scenarios are asserted against.

These are the numbers and shapes a drill is measured with rather than the
scenarios themselves: the debounce and push constants `internal/vaultgit`
implements, the autosave floor the timing gate runs at, the path prefix a drill
is allowed to have touched, and the observation kinds a scenario's requirements
are spelled in.

`CATALOGUE_VERSION` lives here because it is the version of the whole
definition: the evidence record carries it and the harness checks it for
equality, so tightening any scenario is not a silent change — it invalidates
every record produced under the older catalogue.
"""

from __future__ import annotations

import re

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
