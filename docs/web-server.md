# Local web server

Three of the larger web surfaces have their own pages: the
[Related Notes explorer](web-related-notes.md), the
[Synthesis workbench](web-synthesis.md), and
[Health, Daily Fixes, and properties](web-health.md).

On the **Edit source** page, change the **Note title** field above the formatting
toolbar and click **Rename**. This renames the file in its current folder and
updates incoming wikilinks. If the editor has unsaved text, save it first using
the prompt or cancel the rename. The **Save** button saves the source text;
**Rename** applies the title change.

Rendered notes show the body before Ask AI. The Contents list highlights the
current section as you scroll, without changing the URL or keyboard focus.
The first Tab stop is **Skip to note**: press Enter to move directly to the
rendered body, bypassing navigation and metadata controls. It also works with
JavaScript disabled and stays out of printed notes.
Code blocks offer **Copy code**, preserving whitespace; if the browser refuses
clipboard access, a selected text field lets you copy manually.
Ask AI responses and Daily Brief insights offer the same manual-copy fallback
for their original Markdown.
Headings also offer **Copy section link**, which copies the full note URL with
that heading's existing anchor and uses the same manual-copy fallback.

Opening a Library search result offers **Back to results** on the note, preserving
the search text, filters, and sort order even after a reload or opening in a new
tab. **Copy note URL** copies the full note address without that search context
or a section anchor, with the same manual-copy fallback.
**Back to results** also brings the opened result into view and restores keyboard
focus after results load. Browser Back remembers the result too. If that note no
longer matches, the search still loads normally; interacting while results load
cancels the automatic jump.
Following body links, backlinks, or outgoing links to other notes carries that
same Library context along, so **Back to results** remains available as you explore.
**Edit source** carries it into the editor too: use the note title link to return
to the note, then **Back to results** to resume browsing. Reloading the editor,
renaming, or moving the note preserves this context.

If header search fails, **Retry search** repeats the current query and filters
without retyping. Dismissing search also dismisses the retry action.
When Library has no matches, **Remove search text** clears only the text query,
preserving filters, the expression, sort order, and result limit. **Reset search**
still clears the whole search.
If the filter picker cannot load, **Retry filters** reloads its choices without
changing the search, selected filters, ordering, or result limit. The error stays
visible until loading succeeds, and selected filters can still be removed.

On screens up to 960 pixels wide, **Metadata** and **Contents** start collapsed
and expand independently. Desktop sidebars keep their existing layout and
visibility preference; expanding mobile metadata does not change that preference.

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
submit. A counter shows the current length using the browser's input-limit
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

If a Library Pin/Unpin action fails, its row displays the error and a **Retry**
button. The toggle returns to its previous state until a retry succeeds.

Library change-history entries and the History workspace show the first three
affected paths below each command, with an expandable list for the remainder,
so you can identify a change before choosing Undo or previewing a restore.

After **Create note** succeeds for a broken link, **Open note** takes you to the
new note. A failed creation keeps the Create action available for retry.
On the Broken links page, an empty filtered result offers **Clear filter** to
return to the full list.

On a note, **History for this note** opens the History workspace with its
vault-relative filename in **Path contains**. This uses the existing substring
filter, so it can also include paths containing that filename. The link is
available in local and hosted modes. History keeps its path, origin, and file
class filters, page size, and position in the URL, preserving the view across
reloads, bookmarks, and browser Back/Forward. Changing a filter starts at the
first page; **Clear filters** keeps the selected page size. Empty filtered
results offer that action beside the message; a history with no recorded changes
explains where future entries will appear. Unreadable entries retain their warnings.

Choosing **Restore...** brings focus to the preview heading. While loading, the
preview can be canceled but cannot be applied. Only the latest selection can
populate it; canceling or changing the History view dismisses pending previews.
**Cancel** returns focus to the selected entry's Restore button.

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

## The query bar and saved views

Library results show **Modified** using the note's `last` frontmatter timestamp,
the same value used by recently-modified sorting. Missing or invalid timestamps
are omitted. Date-only values keep their calendar date; full timestamps display
in the browser's local time, with the original value available on hover.

Select Library results and choose **Copy wikilinks** to copy one `[[target]]`
per line in displayed order. This uses the notes' exact-case vault targets and
copies only the selected, displayed results. It also works in read-only mode
and offers manual copying if clipboard access is unavailable.

Shift-click a result checkbox to select or clear the range from the last clicked
checkbox, using the clicked checkbox's new state. Only displayed results are
included. A new search, **Select displayed**, or **Clear selection** resets the
range anchor; changing selection clears any pending bulk-action preview.

Library's **Sort** control offers default order, recently modified, and title
in either direction. It preserves the search text, filters, and result limit;
the URL and saved-view form carry the chosen ordering. Other orderings loaded
from a saved view appear as **Current order**. Choosing a standard ordering
removes any relevance anchor; choosing the original ordering again restores it.

**Query help** below the expression field expands examples and explains how
text search, facets, and expressions combine. Press `Ctrl` + `Space` in the
expression field to request field and value suggestions.

The primary navigation highlights the current page. In Library results,
**Retry search** reruns a failed request with the current query and filters.
**Reset search** opens a clean Library page, clearing the text, facet filters,
query expression, ordering, relative-note context, and limit, along with any
selection or bulk preview.

When a search succeeds with no matches, Library names the active constraints:
text, filters, and/or expression. **Remove filters** and **Remove expression**
appear when applicable and remove only that kind of constraint, preserving the
remaining search, ordering, and result limit. **Reset search** still clears all
search settings.

**Select displayed** selects only the notes currently shown, even when the
match total is larger. **Clear selection** unchecks them all. Changing the
selection clears any bulk preview, so the next action previews the new selection.
The selected count sits beside these controls and remains visible in read-only
mode, where selections can still be copied as wikilinks.

Press `/` outside an editable field to focus the header search. The shortcut
leaves editors and open dialogs alone. Escape closes the suggestions and returns
focus to the control you came from without scrolling, provided it is still
available. Leaving search normally clears that return target.
Search suggestions are links: middle-click
or Ctrl/⌘-click to open a note in another tab, or use the arrow keys and Enter
to open it in the current tab. **Open results in Library** carries the typed
search to the Library; Enter does the same when no suggestion is selected,
including while suggestions are loading or unavailable. On the Library it
preserves the active filters, ordering, and limit. On other pages header search
searches the whole vault, so the link carries only the typed text.

`/library` and `/graph` carry the same query bar, which takes the same
expression language as `bn find -q` — see
[Query expressions](commands-query.md#query-expressions--q) for the syntax and
the field reference. There is one execution path behind all three surfaces, so
an expression means exactly the same thing at the terminal, in the library, and
as a graph filter.

The bar validates as you type against `GET /api/query/validate`, which returns
the canonical spelling of a valid expression, the fields it names, and whether
it needs the link graph. A rejected expression is shown in place with the
offending span underlined by a caret rather than silently returning nothing.
`GET /api/query/completions` supplies candidates at the caret: field names with
a one-line hint, and values sourced per field — tags, categories, statuses,
types and folders from the facet counts, property names and values from the
property index, note titles for the graph fields. Accepting a field completion
chains straight into its value list; `Ctrl` + `Space` asks for completions from
any position, including an empty bar. Both routes are read-only: `GET` only, no
CSRF token, no writes.

A confirmed expression is published into the URL as `expr=`, so a query is
shareable and restorable exactly like the facet chips, and it ANDs with them —
a chip and an expression that contradict each other return nothing rather than
one overriding the other. Every offset that crosses the wire on these two
routes (the error span and column, the caret position, the span a completion
replaces) is a UTF-16 code-unit index into the query, matching what the browser
uses, so a caret cannot drift on accented text.

**Saved views.** The views defined as `[[views]]` blocks in `brainnotes.toml`
(see [Configuration](configuration.md#saved-views)) render as a strip in the
primary navigation on every page, in file order, populated client-side from
`GET /api/views`. An enabled view is a plain anchor carrying only `expr`, `sort`,
`relative_to`, and `limit` — its whole stored query shape and never the chips
currently selected, so following one is
shareable and middle-clickable like any other nav link; it stays on the current
page when that page hosts a query bar and lands on `/library` otherwise, so
following a view from the graph keeps you on the graph. A view whose query no
longer compiles renders as an unavailable item instead of vanishing. Click or
tap it, or focus it and press Enter or Space, to expand its error; it never
opens the invalid query. This matches `bn views` and `bn views check`. If the
listing itself fails, a compact **Saved views unavailable** message offers
**Retry**. Retry stays disabled while loading; a successful empty listing leaves
the strip empty. A delayed listing cannot replace newer views returned by a save.

On Library and Graph, saved views are highlighted when their complete query
matches the page, with no additional text search or facets. Changing the query,
sort, or filters updates the highlight without navigating away.

The Library header can save the current query expression as a view. Cancelling
the form with **Cancel** or Escape returns focus to **Save as view**. The form
shows the expression, ordering, relevance anchor (if any), and limit it
will store. Text search and facet chips are separate filters and are **not
saved**; the form lists any active exclusions before saving. With no expression,
write one in the query bar first. Saving a view is a configuration write, so it
goes through `POST /api/views/preview` then
`POST /api/views/apply` with the same one-use receipt every other config change
uses, and the preview returns the exact `brainnotes.toml` diff. Creating a view
that does not exist yet auto-chains preview into apply; overwriting an existing
one stops on the rendered diff until you confirm, because an upsert replaces the
whole block and drops any comments inside it, while a rename rewrites only the
`name` line and preserves them. Each apply consumes its receipt, so a retry
re-previews against the current file. Saves, renames, and deletes write undo
journal entries named `bn serve view save`, `bn serve view rename`, and
`bn serve view delete`.

**A view's ordering and cap are executed.** `/api/search` accepts `sort`,
`relative_to`, and `limit` alongside the filter parameters, so a view opened
from the strip returns the result set it was saved as rather than the same
filter under the page's own best-match ranking.

| Parameter | Effect |
| --- | --- |
| `sort` | Any `bn find --sort` key, with the same `-key` / `key:desc` direction spellings. Absent, results keep the page's best-match ranking. |
| `relative_to` | The anchor for `sort=relevance`, resolved like any route note. Required by `relevance` and rejected with any other key, exactly as a saved view and `bn find` are. |
| `limit` | A positive whole number. Applied *after* the ordering, so it keeps the top N. |

The ordering runs on the server, before the response is trimmed, through the
same `internal/query` ordering `bn find` uses — the CLI and the web cannot order
a vault differently. That includes `relevance`, whose scores come from the
recall corpus but whose ordering is `query.OrderNotesByRelevance`: the anchor
sorts above everything, an unindexed note has no score and sorts last in both
directions without being dropped, ties stay in ascending path order in both
directions, and `-relevance` reverses the scores and nothing else. A view saving
`sort = "-relevance"` with a `limit` therefore keeps the same top N here as at
the terminal. A `sort` the server cannot execute, an anchor that does not
resolve, or a non-integer or negative `limit` is a structured `400`, never a
silently dropped ordering.

`limit` can only narrow: the fixed payload cap still applies, so a view saving
`limit = 500` does not become a way to serialize the whole vault. Neither `sort`
nor `limit` is a filter — a request carrying one and no actual filter still
selects nothing rather than returning the vault ordered.

The Library reports the full match count before either limit, for example
“Showing 50 of 137 matching notes.” It identifies a requested query limit
separately from the server's 50-note display cap. Bulk actions affect only
selected displayed notes. The search API keeps its JSON array response and adds
`X-BN-Search-Total` (matches before limits), `X-BN-Search-Limit` (requested limit,
zero when unlimited), and `X-BN-Search-Cap` (server display cap) response headers.

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

After trashing a note from its note page, the Daily Brief shows an Undo notice
for that specific change. Undo restores the note and returns to it; conflicts
remain visible with a link to History. The notice survives the redirect in
this tab only and is scoped to the current server session. If browser storage
is unavailable, the note page stays open with the same Undo control.

Note pages keep **Copy wikilink** and **Copy note URL** visible, with vault path,
route, and Markdown link under **More copy options**. Copy controls offer
selected, read-only text for manual copying
when clipboard access is unavailable or refused. Keyboard navigation in header
search keeps the selected result visible within the dropdown without scrolling
the page.

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

Printing a rendered note with the browser's Print / Save as PDF command includes
its title and body, with dark text on white in either theme. Navigation, sidebars,
copy controls, and AI/related panels are omitted; long code lines wrap to fit.

In the browser source editor, a `•` before the tab title indicates unsaved edits
or a save whose outcome is still unconfirmed. It clears when the current text is
confirmed saved or you return to the saved text with no outstanding save.

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
(`broken=1`, `orphan=1`, `dead_end=1`), date/age filters, link-count ranges,
and `expr=`, the query expression the query bar writes. The facet picker is
searchable, count-labeled, keyboard usable, and writes selected filters into
the URL, so filtered views can be shared.

Every filter ANDs, `expr=` included, and both `/api/search` and
`/api/graph.json` accept it with identical semantics. An expression that does
not compile is a structured `400` naming the parameter, the query, the span,
and any did-you-mean candidates — not an empty result set. An expression
naming a graph field (`near:`, `degree`, `cluster-size`, …) works on both
routes, since the graph is already in hand there.

The graph view loads the whole vault by default. The free-text box has two
Search modes. In **Filter** (the default) the query keeps only matching notes
and links between visible notes, and the status line reports the reduced node
and edge counts. In **Focus** the query stops removing anything: the graph keeps
its shape and instead rings each match and dims everything except the matches
and their one-hop neighbors, so a term is shown *in* its context rather than cut
out of it. The mode is carried in the URL as `q_mode=focus` (on the page and as
`/api/graph.json?q_mode=focus`), so a focused view can be shared and restored.
Focus mode never moves the viewport while you type — the layout is left exactly
where it is; press `Enter` in the search box or click **Zoom to matches** to fit
the view to the matched nodes. Facets keep filtering in both modes, so you can
scope the graph with a tag and then focus a term within that scope, and a click
focus unions with the search focus rather than replacing it. `expr=` also keeps
filtering in both modes: in Focus mode the free-text box only dims nodes, while
a query expression still removes them, so an expression scopes the graph and
the text then highlights within it.

Nodes with broken links, orphan status, or
dead-end status are styled distinctly and expose those issues in the node
details panel. Use the Spread slider to rerun the layout from a compact
overview up to a wider graph. Labels default to Auto: small graphs are fully
labeled, dense graphs reveal labels for hovered or selected nodes, nearby
context, and zoomed views, and the All mode is available when every label is
needed. Click a node to lock its details, highlight its one-hop neighborhood,
and enable Open note; click empty graph space to clear the selection.

Three display controls thin a dense graph without changing what was loaded.
**Isolate selection** redraws only the selected note and the notes linked
directly to it — exactly one undirected hop, so an inbound-only link still
counts as a neighbor — and **Show whole graph** restores the full view. The
radius is fixed rather than adjustable: everything outside that neighborhood is
removed from the canvas outright, so a node the isolation dropped cannot be
hovered, clicked, or opened. **Min degree** hides nodes with fewer than *N*
links in the current view (the selection and the isolate anchor are always
kept), applied in a single pass, so dropping a hub's leaves can leave the hub
itself sitting at zero. **Collapse folders** folds each group of notes sharing
its first 1–3 folder path segments into one aggregate node sized by how many
notes it stands for; click an aggregate to expand that folder, click it again to
fold it back. Reciprocal links (`A → B` and `B → A`) draw as a single
double-headed line throughout. All three knobs are carried in the URL as
`g_isolate`, `g_mindeg`, and `g_collapse` — omitted when off — so a drilled-down
view is shareable, and none of them refetches: they are display-only reductions
over the graph already in the browser, and the status line names each reduction
that is actually removing something.

The isolation radius used to be a **Hops** slider carried as `g_hops`. That
parameter is retired. Opening an old link still sends `g_hops` to the server on
that first `/graph` request — the `/graph` handler simply ignores it, as it
ignores every display parameter. Once the page has loaded, its own
initialization rewrites the address bar to drop `g_hops`, keeping every other
query parameter and the fragment intact, and the view renders the same fixed
one-hop neighborhood. The later `/api/graph.json` request never carries any
display parameter, `g_hops` included.

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

### Serving modes

`bn serve` publishes the vault under one of three profiles, chosen once at
startup. `--read-only` and `--hosted` are mutually exclusive — they are different
deployments, so the pair is refused rather than resolved.

| | local (default) | `--read-only` | `--hosted` |
| --- | --- | --- | --- |
| Note, property, pin, cockpit, config writes | yes | no | yes |
| Editor and `[agent].command` launches | yes | no | no |
| Open action | starts the editor | navigates to `/n/<key>` | navigates to `/n/<key>` |
| Request body limit | 32 MiB | 1 MiB | 1 MiB |

Read-only is an allowlist: a route it does not name is unavailable, so its only
writes are the Daily Brief visit and Open/Another activity records in the
external `--state-dir`. Hosted is **not** read-only with writes re-enabled — the
two dimensions are independent. A hosted server keeps every document action and
refuses the routes that would start a *configured* command on the serving machine
(`/api/config/open`, `/api/note/insight`, `/api/brief/insight`,
`/api/daily-review/agent`, `/api/synthesis/generate`), which are refused before
the handler reads the request and refused again at the one place each process
would be started. That is the boundary being defended — a command a person named
in `brainnotes.local.toml`, run on a machine nobody is sitting at — and not
subprocesses as such: a hosted write still forks `git` through
`internal/vaultgit`, because the Git safety engine is what makes the write
recoverable, and it runs a fixed executable with argv the engine builds. The Ask AI, daily review, and synthesis generation controls
report that this server does not launch processes, rather than advising a
`[agent].command` that would change nothing on this machine.

The default bind is localhost (`127.0.0.1:8729`). There is no auth layer in any
mode; a server reachable from another machine belongs behind an authenticated
reverse proxy or tunnel that protects every path, including attachments and
readiness. All side-effecting web routes use `POST`,
require a per-process CSRF token (`X-BN-CSRF` header or `token` form field),
validate note route keys, and reject paths outside the vault. A state-changing
request must additionally be same-origin — judged from `Sec-Fetch-Site` when the
browser sends it, otherwise from `Origin` against the request host; a request
with neither header is not a browser request and is left to the token. A request
body over the profile's limit is rejected with `413`. Every response is
`private, no-store` with `X-Content-Type-Options: nosniff`,
`Referrer-Policy: same-origin`, and `X-Frame-Options: DENY`, so an upstream
shared cache retains nothing, the note being read stays out of the `Referer` of
its external links, and no page can be framed into a clickjack of a write
control the CSRF token would have accepted; `/static/`
assets relax that to `private, no-cache` because the bundle holds no vault
content. Attachments served from `/media` are declared by extension, and one
whose type a browser would execute (HTML, XML, SVG, script) is sent under
`Content-Security-Policy: sandbox` as a `Content-Disposition: attachment`
download; a type merely sniffed from the bytes is downgraded to
`application/octet-stream`. `brainnotes.toml`, `brainnotes.local.toml`, and every
dot-path are never attachments, symlink aliases included. Overview bulk
automation must `POST /api/notes/bulk/preview` first, then send the returned
one-use `preview_id` to `POST /api/notes/bulk`; missing, stale, mismatched, or
reused previews are rejected with `409`. Preview receipts stay process-local,
expire after five minutes, and never write journal entries by themselves.
Cockpit fixes follow the same preview-then-apply contract via
`POST /api/fixes/preview` and `POST /api/fixes/apply`, and property changes
follow it via `POST /api/properties/preview` and `POST /api/properties/apply`
(definitions use `POST /api/properties/schema/preview` and
`POST /api/properties/schema/apply`). Saved views follow it via
`POST /api/views/preview` and `POST /api/views/apply`, which write only the
`[[views]]` section of `brainnotes.toml` and never a note; a receipt is issued
for one specific change and is refused by the other config endpoint, so a
schema receipt cannot apply a view or the reverse. Synthesis notes follow it via
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
- [Serving modes and capabilities](internals/serving-modes.md) — the internal contract behind the table above.
