# Maintenance and bulk-edit commands

This reference is split into three pages:

- [`bn lint`, `bn rename-key`, `bn repair`, `bn relink`](commands-maintenance-schema.md) — schema violations and link repair.
- [`bn rename`, `bn move`, `bn replace`, `bn set`, `bn tags`, `bn unset`, `bn values`](commands-maintenance-edits.md) — renames, moves, and property edits.
- `bn doctor`, below.

## Command reference

### `bn doctor` — show the vault hygiene queue (Daily Fix Cockpit)

```sh
bn doctor [--json] [--daily] [--review-context] [--folder path]
```

Reports the current hygiene queue for the vault. It is read-only and never
modifies files; the exit code is always `0` for a successful scan, and an empty
queue means the vault is clean. The queue is the CLI face of the same Daily Fix
Cockpit the web UI serves (`bn serve`), so both surfaces show identical tasks
for every note both surfaces loaded. `bn doctor` always reads the full vault
with no per-note timeout; the one intended exception is that `bn serve` bounds
each note read to a fixed 2-second timeout and quarantines a timed-out note (it
appears in the server's "Skipped notes" diagnostic and is absent from the web
queue until you **restart** the server), whereas `bn doctor` still includes that
note. See `bn serve` above for the full description of this server-only bound.

Detected rule kinds:

| Kind | What it flags |
| --- | --- |
| `frontmatter_error` | frontmatter that does not parse as YAML |
| `schema_violation` | frontmatter values that violate `[frontmatter.properties]` |
| `broken_target` | wikilinks whose target note does not exist |
| `duplicate_title` | two or more notes sharing the same title |
| `orphan_note` | notes with no inbound links from other notes |
| `dead_end_note` | notes with no resolved outgoing links, except notes with boolean `dead_end: false` |
| `long_tail_value` | rare frontmatter values that a dominant sibling value can absorb (e.g. a `status` typo to merge into the common value) |
| `sensitive_content` | notes whose body matches a built-in secret detector (AWS/GitHub/Slack/Google keys, JWTs, private-key blocks, secret assignments); off unless `sensitive_scan = true` |
| `missing_review_date` | notes in a `review_statuses` status that lack the `review_date_property` |
| `stale_status` | notes whose status has not changed in more than `stale_status_days` days |
| `wip_limit` | more concurrent `wip_statuses` notes than `max_wip` allows |
| `hub_needs_synthesis` | hub/MOC notes (by `hub_type_values`) with a `hub_empty_headings` heading present but empty |
| `capture_backlog` | more unprocessed captures under `[capture].heading` in the inbox note than `max_captures` allows; off unless `max_captures` is set |

The last six kinds are **advisory / triage-only**: they carry no automatic
fix and are resolved by editing notes yourself, then marking the task done or
snoozed. Each is disabled by default until you configure its `[cockpit]` keys
(an empty gating list, or `sensitive_scan = false`, means the rule is off).
`sensitive_content` never surfaces a matched value — a task shows only the
line number and detector category, so secrets are never echoed into the queue,
JSON, or web UI. `capture_backlog` counts the bullets under `[capture].heading`
with the same scanner `bn capture` writes through, and its signature includes the
inbox note's content: the task re-signs on every capture, so a snooze lasts only
until the next one. That churn is intentional — a backing-up inbox should come
back — but it does mean Snooze behaves differently here than on the other rules.

Issues are grouped into tasks by kind and detail, content-addressed over the
issue and every affected note's path, location, and file content, and sorted by
priority. Each task lists the fix actions available for it. Tasks you mark done
or snooze through the cockpit (web UI or the state file) are hidden from the
default view; the footer reports how many were suppressed.

`--json` emits the full queue — every task, its affected notes, available
actions, and the active/done/snoozed/total counts — in a stable schema
identical to the web `GET /api/cockpit` payload. `--daily` caps the surfaced
tasks to the top `cockpit.daily_limit` (default `10`) after workflow-state
filtering, mirroring the web daily view (`GET /api/cockpit?view=daily`); the
counts still report the full active/hidden totals, so `active` can exceed the
number of tasks shown. `--review-context` emits a deterministic, redacted JSON
snapshot of that daily queue for agent review — it implies `--daily`, omits
file hashes and raw bodies, keeps only the server-redacted per-note locations,
and never invokes any AI command. `--folder <path>` keeps only tasks that touch
at least one note under a vault-relative folder; other `find` dimensions do not
map onto tasks that group many notes and are intentionally not offered.

Workflow state (done/snoozed) lives in `<vault>/.bnotes/cockpit.json`, migrated
automatically from readable records in the legacy `<vault>/.bnotes/review.json`
on first load. Tune the queue with the `[cockpit]` config block:
`disabled_rules` hides rule kinds entirely, `priority` surfaces chosen kinds
first, and three keys tune `long_tail_value`: a value is flagged only when it is
rare — its count is at most `long_tail_max_count` (default `2`) — *and* the
property has a dominant sibling value at least `long_tail_head_factor` (default
`3`) times as frequent to consolidate into. This keeps genuine typos/variants
(e.g. `status = Maintenance` when `status = Completed` dominates) while dropping
both common head values and unique-per-note fields (`email`, `phone`, `url`) that
have no dominant sibling. Qualifying values are then capped by `long_tail_limit`
(default `5`, rarest first) so a property with many variants cannot flood the
queue. `long_tail_ignore_properties` (default empty) excludes named properties
from the rule entirely — set it to open-vocabulary fields (e.g. `tags`,
`topics`, `created`) where a long tail is expected and never a typo, keeping the
rule focused on enum-like properties such as `status`, `kind`, and `type`.

```sh
bn doctor                                   # human-readable queue
bn doctor --json                            # machine-readable queue
bn doctor --daily                           # top cockpit.daily_limit tasks only
bn doctor --review-context                  # redacted daily snapshot for review
bn doctor --folder Projects                 # only tasks touching Projects/
```

For a daily review workflow, pipe the redacted context into a local agent:

```sh
bn doctor --review-context | my-review-agent   # no secrets/bodies leave the vault
```

The same redacted snapshot backs the web review surface
(`GET /api/daily-review/context`) and the optional AI route
(`POST /api/daily-review/agent`); see `bn serve` below.

