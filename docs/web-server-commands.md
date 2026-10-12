# Local web server: Command reference

Part of the [local web server](web-server.md).

## Command reference

### `bn serve` — run the local vault browser

```sh
bn serve [--addr host:port] [--port N] [--no-open]
bn serve --hosted [--addr host:port] [--no-open]
bn serve --read-only --state-dir DIR [--replica-status-file FILE] [--no-open]
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
- `GET /api/query/validate` compile one query expression and `GET /api/query/completions` field and value candidates at a caret, both read-only
- `GET /api/views` the saved views, and `POST /api/views/preview` then `POST /api/views/apply` to save, rename, or delete one
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
submit. Type `[[` to search for note targets, use Up/Down and Enter to insert
a suggestion, or tap one. Escape dismisses suggestions; Ctrl/⌘ + Enter still
captures the text. Completion preserves the rest of the draft, closes the link,
and respects the character limit. A counter shows the current length using the browser's input-limit
units (some emoji count as two), including after restoring an unfinished draft.
It writes through the
same primitive as `bn capture`, so the CLI and the browser produce identical
lines. The server owns the clock and echoes the exact bullet it wrote back to
the browser, which renders that string verbatim — there is no client-side
timestamp to disagree with the file. Successful captures list newest-first below
the box (session-scoped, capped at five entries, empty again after a reload), and
each one writes an undo journal entry named `bn web capture` — undoable with
`bn undo <journal_id>` or from the change-history panel on `/library`, with the
same whole-note caveat as the CLI. When `[capture].inbox_note` or `[capture].heading` is not
configured, the panel still renders but is disabled. **Configure capture** opens Settings at
the inbox-note field, with the heading field alongside it. Set both values,
then return to Brief to capture a thought. The setup link is hidden on read-only replicas.

An unfinished capture is kept in this tab's session storage, separately for
each vault, inbox path, and heading. Reloading or navigating away and back
restores the text without submitting it. A confirmed capture or manually clearing
the box clears the draft; a failed request keeps it. If browser storage is
unavailable, capture still works and the box explains that recovery is unavailable.
The displayed destination links to the inbox when it exists, and successful
captures offer **View inbox**, including after the first capture creates it.
After a capture finishes, focus returns to the capture box unless you moved to
another control or clicked elsewhere while waiting. This applies to failures too.
If the response is lost or a successful response cannot be confirmed, capture
keeps the draft and asks you to check the inbox before retrying: the write may
already have succeeded. **View inbox** is available when its address is known.

`POST /api/capture` is unpreviewed, like `POST /api/properties/value` and the
single-note frontmatter routes, but qualifies from the other direction: one
note, strictly additive, so it cannot destroy existing bytes, and a confirm step
would defeat the point of a quick-capture box. Its
safety comes from the append transaction being pinned to the revision it planned
against rather than from a client-echoed note hash — the box never loaded the
note and has no hash to send. A conflict that survives the primitive's three
internal replans returns `409`; resubmitting is always safe.

The **Recently modified** lists on Brief and Library are ordered by file
modification time.

**Pinned notes.** Four surfaces share one list: a section on `/` showing the
first seven pins with a link to the rest, the full `/pinned` page, a Pin/Unpin
toggle on every note page, and the same toggle on `/library` search rows. State
is the `pinned` frontmatter rank `bn pin` writes, so the CLI, Obsidian, and the
web UI always see the same list.

If the server refuses a Library Pin/Unpin action, its row displays the error and
a **Retry** button. The toggle keeps its previous state until a retry succeeds.

Pin/unpin controls and pin reordering require a confirmed success response.
If the response is lost or unreadable, **Refresh to check** reloads the page to
check the actual state before another attempt. Library and note toggles keep
their previous state, and the pinned list keeps its rows and stops queued moves
until refreshed. A server refusal still displays its error and allows retry.

Library change-history entries and the History workspace show the first three
affected paths below each command, with an expandable list for the remainder,
so you can identify a change before choosing Undo or previewing a restore.
Each affected filename offers **History for this path**, opening all recorded
changes for that exact path without carrying over other filters or pagination.
This also works for attachments and paths that have since been deleted or renamed;
it searches the recorded filename rather than following renames.

After **Create note** succeeds for a broken link, **Open note** takes you to the
new note. A failed creation keeps the Create action available for retry.
On the Broken links page, **Clear filter** appears beside the filter controls
whenever a filter is active, whether or not it matches any links.

On a note or in the source editor, **History for this note** opens the History
workspace with its vault-relative filename and **Exact path** matching. This
matches the recorded path, including case, without including similarly named
files or following earlier renames. For a broader search, switch **Path matching**
to **Contains**, the default case-insensitive substring search. The link is
available in local and hosted modes. History keeps its path, matching mode, origin, and file
class filters, page size, and position in the URL, preserving the view across
reloads, bookmarks, and browser Back/Forward. Changing a filter starts at the
first page; **Clear filters** keeps the selected page size. Empty filtered
results offer that action beside the message; a history with no recorded changes
explains where future entries will appear. Unreadable entries retain their warnings.

Choosing **Restore...** brings focus to the preview heading. While loading, the
preview can be canceled but cannot be applied. Only the latest selection can
populate it; canceling or changing the History view dismisses pending previews.
**Cancel** returns focus to the selected entry's Restore button.

The History API and browser URL accept `path_match=exact|contains` alongside
`path`; omitting the mode keeps substring matching. **Clear filters** resets it
to **Contains**.

Backlinks show the first two occurrence snippets per source note, with
**Show N more mentions** expanding the rest. The source link and total mention
count remain visible, and the disclosure works without JavaScript.

The editor labels its recovery action **Undo latest vault change**, since it can
undo a change to any note, not only the note being edited.

Pinned lists show their order without internal rank values. Unpinning removes
the row without reloading; keyboard focus moves to the next Unpin button (or
the previous one at the end), and to Library when the list becomes empty. Focus
that moved elsewhere while waiting stays there. Successful reorders announce
the note title and its new position.

Header search shows a keyboard hint with results. Enter opens the selected note,
or opens Library when no note is selected; Escape dismisses the results.

Reordering is web-only. Drag a row's handle on `/pinned`, or focus a handle and
press `Up`/`Down` for the same move from the keyboard. The browser posts note
keys, never ranks. Press `Escape` to cancel a drag; an interrupted pointer gesture
also restores the original position without saving. The server takes the midpoint
of the two neighbouring ranks as they are on disk, so a stale page cannot write
an order it computed against a list that has since moved. A move normally rewrites
exactly one note; in the rare case where the float gap between two ranks is exhausted, the server
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

