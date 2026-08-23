# Local web server

Three of the larger web surfaces have their own pages: the
[Related Notes explorer](web-related-notes.md), the
[Synthesis workbench](web-synthesis.md), and
[Health, Daily Fixes, and properties](web-health.md).

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
- `/synthesize?source=<note-key>` the curated synthesis workbench, launched from any note page
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
- `POST /api/synthesis/context` and `POST /api/synthesis/generate` read-only packet building and optional local AI drafting
- `POST /api/synthesis/preview` then `POST /api/synthesis/apply` to review the exact note bytes and then create them

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

## Note actions, Trash, and rendering

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

Editor opens from note pages and Daily Brief cards use the same editor resolution as CLI open
commands (`[open].editor`, then `$EDITOR`). Successful opens are asynchronous;
if editing the file outside the web UI changes the body, refresh the page
manually to see updates.

Media embeds (`![[image.png]]`) render through `/media/...` only when the file
exists. Missing media embeds remain plain text (they do not redirect to
`/broken`).

## Filters and the graph view

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

## Options, assets, and security

`--addr` overrides `[serve].addr`. `--port` rewrites only the port on the
resolved address host. `--no-open` forces browser launch off even when
`[serve].open_browser = true`.

Local binaries built with `just build` or `just install` remember this checkout
path. `bn serve` rebuilds web assets there before starting, preferring the local
native `node_modules/esbuild/bin/esbuild` binary so an already-installed checkout
does not need to launch `node` or `npm`. If that binary is missing, it falls back
to `npm run build:web`. Binaries without the local checkout stamp fall back to
detecting a checkout from the current directory, then to embedded assets.

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
`POST /api/properties/schema/apply`). Synthesis notes follow it via
`POST /api/synthesis/preview` and `POST /api/synthesis/apply`, where the receipt
holds the whole plan and the apply call carries nothing else, so the bytes
created are always the bytes reviewed. Property receipts are additionally
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

## Daily Brief dogfood readout

Over two weeks, from local state only:

1. Count distinct visit days in the last 14 days from `.bnotes/brief.json` `visits`.
2. Compute average visits per week as `visit_days / 2`.
3. Compute open-day rate as `days_with_open / visit_days` (where opens come from `opens` and are grouped by local date).
4. Success threshold: at least 4 visits per week and opens on at least 50% of visited days.

## Related documentation

- [Related Notes explorer](web-related-notes.md)
- [Synthesis workbench](web-synthesis.md)
- [Health, Daily Fixes, and properties](web-health.md)
