# Health, Daily Fixes, and properties

Part of the [local web server](web-server.md).

## Daily Fixes

The Health page starts with Daily Fixes: the same hygiene queue as `bn doctor`
(over the notes the server successfully loaded — a note quarantined by the
2-second timeout above is absent from the web queue but still scanned by the CLI),
covering frontmatter parse errors, schema violations, broken wikilink targets,
duplicate titles, orphan notes, dead-end notes, long-tail property values, and —
when their `[cockpit]` keys are configured — the advisory rules `sensitive_content`,
`missing_review_date`, `stale_status`, `wip_limit`, `hub_needs_synthesis`, and
`capture_backlog`.
Tasks are grouped by issue and affected note, sorted by priority, and identified
with content-aware hashes over the issue kind, detail, affected relative paths,
locations, and file content. Marking a task Done hides that exact issue until its
signature changes; Snooze hides it until the selected local deadline, either
tomorrow or next week; Clear returns a suppressed task to the active queue.

The advisory rules are triage-only: they never carry a fix action, so the web UI
suppresses the fix button for them (driven by each task's `triage_only` flag) and
they are resolved by editing notes yourself and then marking Done or Snooze.
`sensitive_content` rows show only a redacted `line N: <detector category>`
location — the matched secret value is never sent to the browser, the queue JSON,
or any review surface.

`GET /api/cockpit?view=daily` caps the workflow-filtered queue to the top
`cockpit.daily_limit` tasks (default `10`) while leaving the counts as the full
totals, so the daily panel shows the highest-priority findings without losing the
active/hidden tallies. `GET /api/daily-review/context` returns the same
deterministic, redacted snapshot as `bn doctor --review-context` — per-kind
groups and ordered items with server-redacted locations, no file hashes, and no
raw bodies — so the CLI and web review surfaces cannot drift for the notes the
server loaded. The one intended difference is coverage, not format: a note
quarantined by the server-only 2-second load timeout is absent from the web
snapshot while `bn doctor` still includes it from its full-vault scan.

`POST /api/daily-review/agent` is an opt-in, local-only AI review. A non-empty
legacy `cockpit.agent_command` remains the daily-review-only override (split on
whitespace, no shell); otherwise the route uses the shared top-level
`[agent].command` argv and timeout. It feeds exactly the redacted daily-review
context on stdin and returns stdout verbatim as `{ok, enabled: true, output}`.
When neither command is configured, the route returns a clean disabled response
(`{ok: true, enabled: false}`) with HTTP `200`, not an error—no AI provider
dependency is added. Failures and timeouts return `internal_error` (HTTP `500`).
Because only the redacted daily-review context crosses this boundary, raw note
bodies, file hashes, and unredacted sensitive values never reach this agent
route. The route is CSRF-protected like every other side-effecting web action.

Cockpit workflow state is stored in the served vault at
`<vault>/.bnotes/cockpit.json`, migrated automatically from readable records in
the legacy `<vault>/.bnotes/review.json` on first load. It is runtime UI state,
not note metadata: it does not edit note frontmatter, does not write undo
journal entries, and is pruned when the underlying issue is resolved or changes
enough to receive a new signature. `GET /api/cockpit` returns active tasks plus
`active`, `done_hidden`, `snoozed_hidden`, and `total_generated` counts.
`POST /api/cockpit/item` requires CSRF and form fields `id`,
`action=mark_done|snooze|clear` (`done` is accepted as an alias for
`mark_done`), plus optional `snooze=tomorrow|week`. The note-writing actions
`exempt_dead_end` and `clean_long_tail` are property writes: they are refused
with `409` while the configuration is invalid, pin the configuration they were
planned with, and refuse an affected note that changed since the queue was built.

Fixable tasks advertise a fix action (schema repair, deterministic relink, or
blank-stub creation for a broken target). Applying a fix is preview-gated:
`POST /api/fixes/preview` returns the planned per-note changes plus a one-use
`preview_id`, and `POST /api/fixes/apply` must echo that receipt back before any
file is touched. A receipt that is missing, expired, replayed, or whose task
changed on disk since the preview is rejected with `409` so a stale fix never
lands. Both endpoints require CSRF and the task `id` and `action`; a successful
apply writes an undo journal entry named `bn cockpit fix`.

## Properties workbench

The Properties pages are an interactive workbench over every property your notes
actually store plus every property declared in `[frontmatter.properties]`. The
catalog sorts properties so the ones needing attention come first and reports
configured status, coverage, distinct logical values, observed value kinds, and
violation counts (missing required, wrong type, disallowed value) alongside
values YAML cannot safely represent as frontmatter text. A logical value counts
at most once per note, so a list that repeats an item does not inflate the
statistics. `/properties/<key>` drills into one property with its distinct
values, a filterable note list (all, violations, missing required, unsupported),
a free-text title/path search, and a value filter. Note rows are paginated at 50
per page by default with a hard maximum of 200.

Editing a value uses a control that matches the declared kind: a text field for
`string`, a wikilink field that adds the `[[ ]]` wrapping for you, native date
and datetime pickers, numeric fields for `int` and `float`, a checkbox for
`bool`, and a row of removable chips plus an add field for `list`. An
undeclared property is edited as text. Every control validates before it will
submit, so a malformed date or a value outside `allowed` is refused in the
browser rather than at the server.

The note list is keyboard operable end to end: `/` focuses the search box, the
arrow keys move the focused row, space toggles that row's selection, enter opens
the inline editor or confirms the focused action, and escape cancels the editor
or closes the preview dialog. Focus returns to the row you started from after a
dialog closes.

Saving follows a hybrid model. A single-note replacement of one value, and
adding one item to one note's list, save immediately: the row updates in place
and an undo toast appears. Everything riskier is preview-gated — removing a list
item, unsetting a property, any change touching more than one note, a bulk
replacement of a value across notes, renaming a property, applying schema
repairs, and every property-definition edit. Those go through
`POST /api/properties/preview`, which returns the affected files, per-note
before/after values, the `brainnotes.toml` diff for a definition change,
projected violation totals, blockers, warnings, and a one-use `preview_id` with
its `expires_at`. The preview dialog shows all of it and requires an explicit
confirmation; `POST /api/properties/apply` refuses to write unless it receives
that receipt. A receipt is single-use, expires after five minutes, and is
invalidated when `brainnotes.toml` changes underneath it, so a stale plan can
never land. Fatal blockers (unparseable frontmatter, a missing note, a rename
that would collide with an existing key, a configuration conflict) suppress the
receipt entirely, which means there is nothing to confirm and no apply is
possible until the blocker is resolved. A skipped repair is reported as an
advisory warning instead and does not block the rest.

After a successful apply the edited rows update immediately, the property
summary and violation counts refetch without a page reload, and an undo toast
links to the history panel at the bottom of the page. That panel lists recent
property and configuration changes with their journal entry IDs, so you can undo
a specific one rather than only the latest. If a note changed on disk between
reading the page and saving, the write is refused with `409` and nothing is
touched — refresh and retry.

Property definitions are editable from the same page. The definition editor sets
`type`, `items` for a list, `required`, and the `allowed` list, previews the
exact `brainnotes.toml` diff plus how many notes would newly violate the
definition, and only then writes. Definition changes never rewrite notes: adding
`allowed = [...]` reveals violations, and fixing them is a separate, separately
confirmed repair. For an unconfigured property whose observed values unanimously
support one kind, the pages propose an inferred `[frontmatter.properties.<key>]`
block, which you can review and adopt into the editor with one click before
saving it. The catalog also offers **Add to brainnotes.toml** on every inferred
block; it previews the exact configuration diff and writes the definition after
you confirm it. Clicking the `brainnotes.toml` label in that section opens the
configuration file through `[open].editor`, with `$EDITOR` as the fallback.
Inference is deliberately conservative: it never guesses `required` or
`allowed`, and it stays silent when the data mixes kinds, mixes scalars with
lists, or contains a value it cannot classify.

While the page is visible it polls `GET /api/config/status` every five seconds
and stops polling when the tab is hidden. If `brainnotes.toml` was edited
externally and reloaded cleanly, the page reloads its data and tells you why. If
the file is now invalid, a banner reports the parse error and property writes are
refused until it is fixed. If a change needs a server restart to take effect, a
restart-required banner says so. Status changes are announced to screen readers,
as are save results and errors.

An unsupported value (a YAML null, mapping, or nested list) is shown by label
only — never as text a write could reproduce — cannot be used as a value filter,
and cannot be edited from the browser. Fix those in the file directly.

The workbench does not provide spreadsheet-style editing, advanced value
transforms (regular expressions, split/join or case rules), or reusable edit
recipes. Use **Settings** for portable configuration and templates, and
**Edit in browser** on a note to edit its Markdown body.

## Compact summaries and recovery

Properties leads with its filter and property list. **Property statistics** expands
the full counts when needed. On phones, the filter retains a normal touch-sized
height instead of stretching vertically.

Health reports a failed fixes request once. **Reload fixes** reads the queue again
without applying a fix. Counts and tasks remain visible with an out-of-date notice
until a successful refresh. The shared **Sync:** status describes synchronization,
separately from these vault-health results.
