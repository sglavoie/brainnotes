# Local web server

## Command reference

### `bn serve` — run the local vault browser

```sh
bn serve [--addr host:port] [--port N] [--no-open]
```

Starts a local HTTP server for vault browsing and lightweight note management:

- `/` quick capture, Daily Brief cards (Continue, Rediscover, Connect), rolling seven-day activity, compact health snapshot, recent notes, and primary search
- `/library` faceted search, bulk note operations, change history, and recent notes
- `/health` Daily Fixes, vault metrics, and full diagnostics from the shared snapshot
- `/properties` property catalog and `/properties/<key>` the interactive property workbench
- `/pinned` the full pinned list in your order, with drag or arrow-key reordering
- `/n/<note-key>` rendered note view (body, editable frontmatter, backlinks/outgoing, broken affordances)
- `/broken` unresolved wikilink diagnostics with one-click blank stub creation
- `/graph` interactive Cytoscape graph
- `POST /api/capture` quick capture into the configured inbox note, echoing the exact line written
- `POST /api/pins/set`, `POST /api/pins/remove`, and `POST /api/pins/reorder` pin, unpin, and reorder, addressed by note key only
- `GET /api/history` recent journal entries and `POST /api/undo` for web-originated undo
- `POST /api/brief/view` idempotent per-day visit recording that refreshes the rolling activity summary
- `POST /api/brief/item` card actions (`open` and `another`) addressed by server-issued `card_id` only
- `POST /api/brief/insight` optional local AI insight for one card, returning raw output and separately rendered safe HTML
- `GET /api/cockpit` hygiene queue (add `?view=daily` for the capped daily view) and `POST /api/cockpit/item` for Done/Snooze/Clear triage
- `POST /api/fixes/preview` then `POST /api/fixes/apply` to preview and apply a single task's fix
- `GET /api/daily-review/context` read-only redacted daily-review snapshot and `POST /api/daily-review/agent` optional local AI review
- `GET /api/properties` property catalog statistics and `GET /api/properties/detail?property=...` per-property values and note rows
- `POST /api/properties/value` immediate single-note edit, `POST /api/properties/preview` then `POST /api/properties/apply` for every other note change
- `POST /api/properties/schema/preview` then `POST /api/properties/schema/apply` for property definitions in `brainnotes.toml`
- `GET /api/config/status` current configuration generation, validity, and whether property writes are allowed

Resilient note loading (server-only): `bn serve` bounds each note read to a
fixed 2-second timeout so a single stalled file cannot hang a page load. A note
that exceeds that timeout is quarantined for the rest of the `bn serve` process
lifetime — it is skipped without a re-read on every later request and excluded
from recent notes, search, the graph, the cockpit queue, and the health counts.
Skipped notes surface in Health diagnostics and are summarized on the home page,
so the omission is visible rather than silent. Quarantine is in-memory
only and never touches disk; **restart** the server to clear it and retry a
previously skipped note. This bound is server-only: `bn doctor` and every other
CLI command keep their original
full-vault loading semantics and read every note without a per-note timeout.

The home page opens with a **Quick capture** panel above Today's brief: the
configured destination (`Inbox.md · ## Captures`), a two-row text box capped at
2,000 characters, a Capture button, and `Ctrl`/`⌘` + `Enter` as the keyboard
submit. It writes through the
same primitive as `bn capture`, so the CLI and the browser produce identical
lines. The server owns the clock and echoes the exact bullet it wrote back to
the browser, which renders that string verbatim — there is no client-side
timestamp to disagree with the file. Successful captures list newest-first below
the box (session-scoped, capped at five entries, empty again after a reload), and
each one writes an undo journal entry named `bn web capture` — undoable with
`bn undo <journal_id>` or from the change-history panel on `/library`, with the
same whole-note caveat as the CLI. When `[capture].inbox_note` or `[capture].heading` is not
configured, the panel still renders but is disabled and names the two keys to
set, so an unconfigured vault does not look like the feature is missing.

`POST /api/capture` is unpreviewed, like `POST /api/properties/value` and the
single-note frontmatter routes, but qualifies from the other direction: one
note, strictly additive, so it cannot destroy existing bytes, and a confirm step
would defeat the point of a quick-capture box. Its
safety comes from the append transaction being pinned to the revision it planned
against rather than from a client-echoed note hash — the box never loaded the
note and has no hash to send. A conflict that survives the primitive's three
internal replans returns `409`; resubmitting is always safe.

**Pinned notes.** Four surfaces share one list: a section on `/` showing the
first seven pins with a link to the rest, the full `/pinned` page, a Pin/Unpin
toggle on every note page, and the same toggle on `/library` search rows. State
is the `pinned` frontmatter rank `bn pin` writes, so the CLI, Obsidian, and the
web UI always see the same list.

Reordering is web-only. Drag a row's handle on `/pinned`, or focus a handle and
press `Up`/`Down` for the same move from the keyboard. The browser posts note
keys, never ranks — the server takes the midpoint of the two neighbouring ranks
as they are on disk, so a stale page cannot write an order it computed against a
list that has since moved. A move normally rewrites exactly one note; in the
rare case where the float gap between two ranks is exhausted, the server
renumbers the whole pinned list in one transaction instead, so that one reorder
can rewrite more than one note. The pinned list is uncapped and so is that
renumber: however long the list is, it commits as one action that `bn undo`
reverts in one step, or it does not commit at all.

The two named neighbours must still sit either side of one gap, and the two ways
that can fail answer differently. A neighbour key naming a note that is no
longer in the vault at all returns `404`. A neighbour that still exists but is
no longer pinned returns `409`, and so does a pair that is still pinned but now
has another pin between them, or that names a head or tail which is no longer
the real end of the list. Reload the page in either case. Reordering never runs from
the CLI.

That gap check is also a commit precondition: a reorder pins the revision of
every note in the pinned list it validated against, so a gap another writer
filled in between is refused with `409` instead of interpolated into twice. Two
reorders that move different notes into the same gap therefore cannot both
succeed, and the one that is refused leaves no note change and no journal entry
behind.

`POST /api/pins/set`, `POST /api/pins/remove`, and `POST /api/pins/reorder` are
the same write class as `POST /api/notes/frontmatter/set`: CSRF-gated,
single-property, and pinned to the revision they planned against rather than to
a client-echoed hash. They are unpreviewed for the same reason that route is —
one property whose whole content is a number this UI owns. Each of them writes
only the `pinned` property, and each writes a single note — with one deliberate
exception: the whole-list renormalization above, where a `reorder` that runs out
of float gap renumbers the whole pinned list through a single transaction,
because half a renumbering is a scrambled order rather than a partial one.

Daily Brief cards stay stable for the local day (including restarts): Continue
shows the most recently edited substantive note, Rediscover surfaces the
longest-unseen substantive note, and Connect suggests an unlinked note related
to Continue using the same `ModeConnect` ranking engine. `Another` on Continue
atomically replaces Continue and Connect together; `Another` on Rediscover or
Connect replaces only that slot. A successful `open` records activity only after
the editor process starts. Card actions are path-safe and keyed by server-issued
IDs; callers never submit filesystem paths.

Daily Brief runtime state lives in `<vault>/.bnotes/brief.json` and stores only
local-date selections, content-addressed card IDs, exposure/open timestamps, and
visit/open activity windows. It never stores note bodies, AI request payloads,
or AI responses. Reads are fail-closed for corrupt, unsupported, or unsafe
(e.g. symlinked) state files; successful writes are atomic and sidecar-only, so
brief state updates never create undo journal entries and never mutate Markdown.

`POST /api/brief/insight` is optional and uses only the top-level `[agent]`
command/timeout. It sends the selected card's bounded context packet to that
local command's stdin and returns the raw stdout plus separately rendered safe
HTML. The output is never persisted to `.bnotes/brief.json`, undo history, or
Markdown files.

No telemetry, background schedulers, reminders, or analytics pipelines are
introduced: activity metrics are local-only and computed from the brief sidecar.

## Related Notes explorer

Every rendered note page includes a full-width Related Notes explorer after the
body. Balanced loads five candidates immediately; Rediscover and Connect remain
lazy until their tabs are activated. **Balanced** is the broad mix from
`bn related`, **Rediscover** limits the set to older or not-recently-opened graph
neighbors, and **Connect** favors text- or metadata-supported notes that are not
already directly linked. Each tab expands independently in groups of five to a
hard cap of twenty. Cards show the candidate path, a bounded prose-first excerpt,
relationship, optional bridge, and up to three human reasons. Numeric scores and
ranking weights are intentionally not sent to or rendered by the browser.

Feedback is directed from the note being viewed to the candidate. **Useful**
adds a positive ranking signal. **Dismiss** and **Later** suppress that pair in
all three modes; Later offers tomorrow, next week, or a strict future local date
derived from the server's date. The Feedback drawer lists every active explicit
decision for the source, including candidates outside the current result prefix.
**Clear** removes the rating or snooze but preserves open history, so the pair is
eligible again without pretending it was never opened. An expired Later decision
also becomes eligible again on read; expiry does not rewrite the state file.

Candidate title links remain ordinary links and work without JavaScript. With
JavaScript enabled, navigation attempts to record an open using `sendBeacon`,
falling back to a keepalive request; tracking failure never blocks navigation.
**Open in editor** starts the configured editor first and records the open only
after launch. Its JSON response distinguishes editor launch from recall-history
recording, including the partial-success case where the editor opened but state
could not be saved. The explorer itself is progressive enhancement: without
JavaScript the mount remains harmless and note links/body rendering are
unchanged, while `bn related` remains the full CLI interface.

Related feedback and open history live only in
`<vault>/.bnotes/recall.json`. This workflow never changes Markdown and never
creates an undo-journal entry. A missing sidecar is an empty, non-mutating read.
Corrupt, invalid, unsupported-version, or symlinked state fails closed and is
not repaired or overwritten automatically. Ignored and process-quarantined
notes are filtered through the same resilient snapshot as the rest of the web
server, so they cannot leak through results, bridges, or the feedback drawer.

The HTTP surface is `GET /api/related?source=<note-key>&mode=<mode>&limit=<1..20>`
and CSRF-protected `POST /api/related/item`. The POST form accepts note route
keys—not filesystem paths—and `action=view|open_editor|useful|dismissed|later|clear`;
Later additionally requires `until=YYYY-MM-DD`. Browser code must treat these
JSON shapes as a strict score-free contract, preserve each mode's ranked-prefix
replacement semantics, and rerun queries after a mutation instead of attempting
to reproduce ranking locally.

Maintainer smoke validation covers a 1440×1000 desktop and a 390×844 narrow
viewport (inside the 640-pixel breakpoint) with keyboard-only tab activation,
Show more, Later, drawer Clear, and focus recovery;
light, dark, and system themes; empty results; unavailable editor and unreadable
recall-state errors; long titles, Unicode excerpts, missing bridges, and a full
twenty-result panel. Run the smoke pass against a local `bn serve` instance after
`npm run build:web`; the automated lifecycle, controller, contract, responsive
CSS, race, and `just ci` checks remain the release gate of record.

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
`mark_done`), plus optional `snooze=tomorrow|week`.

Fixable tasks advertise a fix action (schema repair, deterministic relink, or
blank-stub creation for a broken target). Applying a fix is preview-gated:
`POST /api/fixes/preview` returns the planned per-note changes plus a one-use
`preview_id`, and `POST /api/fixes/apply` must echo that receipt back before any
file is touched. A receipt that is missing, expired, replayed, or whose task
changed on disk since the preview is rejected with `409` so a stale fix never
lands. Both endpoints require CSRF and the task `id` and `action`; a successful
apply writes an undo journal entry named `bn cockpit fix`.

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

Version one deliberately excludes: spreadsheet-style grid editing of many
properties at once, advanced value transforms (regular expressions, split/join,
case rules), saved or reusable edit recipes, browser editing of settings other
than property definitions or of raw TOML, and any redesign of the note-body
editor — bodies still open in your configured editor.

The overview search results can select notes for batch lifecycle work: move to
Trash, move to a folder, set/list-edit frontmatter, or unset frontmatter. Bulk
actions require an inline preview first: the server returns a one-use
`preview_id`, counts, destination paths, frontmatter before/after values, move
rewrite counts, and any failures, then the follow-up apply call must reuse that
preview receipt before it expires. Note pages can move the current note to
Trash, edit frontmatter in place, create a blank note for a broken wikilink
target, and open the file in the configured editor for body edits. These web
actions use the same note-management code as the CLI where practical, refresh
the served vault state after success, and return structured JSON for automation.

Trash is a visible vault-root `Trash/` folder. Trashing a note moves the file
there, chooses a non-conflicting destination name, and leaves `Trash/` ignored by
default so trashed notes do not continue to appear in search, graph, or
diagnostics.

Rendered note pages syntax-highlight fenced code blocks that include a language
info string, such as `go`, `sh`, or `json`, on the server. Unlabeled fences
remain plain escaped code blocks. Note bodies also auto-link GFM-style bare URLs
such as `https://example.com`. Beyond that, rendering supports the GitHub
Flavored Markdown extensions — tables, `~~strikethrough~~`, and task lists
(`- [ ]` / `- [x]`, shown as read-only checkboxes since `bn serve` does not edit
note bodies) — plus footnotes, definition lists, and typographic punctuation
(straight quotes become curly, `---` becomes an em dash, `--` an en dash, and
`...` an ellipsis). Wide tables scroll horizontally so they stay readable on
narrow viewports, and wikilinks resolve inside table cells just as they do in
body text. Inline and fenced code are left untouched by these transforms.

Local binaries built with `just build` or `just install` remember this checkout
path. `bn serve` rebuilds web assets there before starting, preferring the local
native `node_modules/esbuild/bin/esbuild` binary so an already-installed checkout
does not need to launch `node` or `npm`. If that binary is missing, it falls back
to `npm run build:web`. Binaries without the local checkout stamp fall back to
detecting a checkout from the current directory, then to embedded assets.

The overview and graph views share the same URL filter parameters for text,
tags, category, status, type, folder, property presence, issue filters
(`broken=1`, `orphan=1`, `dead_end=1`), date/age filters, and link-count
ranges. The facet picker is searchable, count-labeled, keyboard usable, and
writes selected filters into the URL, so filtered views can be shared.

The graph view loads the whole vault by default. Filtering keeps only matching
notes and links between visible notes, while the status line reports the
reduced node and edge counts. Nodes with broken links, orphan status, or
dead-end status are styled distinctly and expose those issues in the node
details panel. Use the Spread slider to rerun the layout from a compact
overview up to a wider graph. Labels default to Auto: small graphs are fully
labeled, dense graphs reveal labels for hovered or selected nodes, nearby
context, and zoomed views, and the All mode is available when every label is
needed. Click a node to lock its details, highlight its one-hop neighborhood,
and enable Open note; click empty graph space to clear the selection.

`--addr` overrides `[serve].addr`. `--port` rewrites only the port on the
resolved address host. `--no-open` forces browser launch off even when
`[serve].open_browser = true`.

The default bind is localhost (`127.0.0.1:8729`). This is single-user local
software; there is no auth layer. All side-effecting web routes use `POST`,
require a per-process CSRF token (`X-BN-CSRF` header or `token` form field),
validate note route keys, and reject paths outside the vault. Overview bulk
automation must `POST /api/notes/bulk/preview` first, then send the returned
one-use `preview_id` to `POST /api/notes/bulk`; missing, stale, mismatched, or
reused previews are rejected with `409`. Preview receipts stay process-local,
expire after five minutes, and never write journal entries by themselves.
Cockpit fixes follow the same preview-then-apply contract via
`POST /api/fixes/preview` and `POST /api/fixes/apply`, and property changes
follow it via `POST /api/properties/preview` and `POST /api/properties/apply`
(definitions use `POST /api/properties/schema/preview` and
`POST /api/properties/schema/apply`). Property receipts are additionally
invalidated when `brainnotes.toml` changes, so an apply planned against an older
configuration is refused rather than reinterpreted. Preview-gating covers every
change that spans notes or properties; the routes exempt from it are all
single-note and deliberately narrow. `POST /api/properties/value`
accepts only a single-note value replacement or list addition, and it requires the
note's current hash, so a note edited since the page loaded is rejected with
`409`. `POST /api/notes/frontmatter/set`, `POST /api/notes/frontmatter/unset`,
and the three `POST /api/pins/*` routes each write one property and rely on the
write's own revision precondition rather than a client-echoed hash. All of them
write a single note except one: a `POST /api/pins/reorder` whose float gap is
exhausted renumbers the whole pinned list in one transaction.
`POST /api/capture` appends one line to one note and cannot destroy
existing bytes; it sends no hash (the quick-capture box never loaded the note)
and is instead pinned to the revision its append was planned against, so a
competing write is rejected with `409` rather than overwritten. Cockpit triage
(Done/Snooze/Clear) uses `POST /api/cockpit/item` with the same CSRF token and
only writes `<vault>/.bnotes/cockpit.json`.
File-changing web actions write undo journal entries with `bn web ...` command
names (cockpit fixes use `bn cockpit fix`, property changes use
`bn serve property ...`); `POST /api/undo` restores the latest entry by default
or a selected entry ID.

Editor opens from note pages and Daily Brief cards use the same editor resolution as CLI open
commands (`[open].editor`, then `$EDITOR`). Successful opens are asynchronous;
if editing the file outside the web UI changes the body, refresh the page
manually to see updates.

Media embeds (`![[image.png]]`) render through `/media/...` only when the file
exists. Missing media embeds remain plain text (they do not redirect to
`/broken`).

Two-week Daily Brief dogfood readout (local state only):

1. Count distinct visit days in the last 14 days from `.bnotes/brief.json` `visits`.
2. Compute average visits per week as `visit_days / 2`.
3. Compute open-day rate as `days_with_open / visit_days` (where opens come from `opens` and are grouped by local date).
4. Success threshold: at least 4 visits per week and opens on at least 50% of visited days.
