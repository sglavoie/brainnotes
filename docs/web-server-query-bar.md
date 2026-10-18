# Local web server: The query bar and saved views

Part of the [local web server](web-server.md).

## The query bar and saved views

Library results show **Modified** using the note's `last` frontmatter timestamp,
the same value used by recently-modified sorting. Missing or invalid timestamps
are omitted. Date-only values keep their calendar date; full timestamps display
in the browser's local time, with the original value available on hover.

For result paging, selection, copying wikilinks, sort choices, and recovery,
see [Find a note](web-server.md#find-a-note), the authoritative guide to Library
controls. A selection is the checked notes; a saved view stores the search that
finds notes, independently of those checkboxes.

**Query help** below the expression field expands examples and explains how
text search, facets, and expressions combine. Press `Ctrl` + `Space` in the
expression field to request field and value suggestions.

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
If validation is unavailable, **Retry check** retries the current expression
without changing the applied search. Validation and suggestions pause during
text composition and resume after the composed text is committed.
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
`GET /api/views`. An enabled view is a plain anchor carrying `q`, `expr`, `sort`,
`relative_to`, and `limit` — its whole stored selection, so following one is
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
matches the page, with no additional constraints. Changing the query,
sort, or filters updates the highlight without navigating away.

The Library header saves the complete current search as a view. Cancelling
the form with **Cancel** or Escape returns focus to **Save as view**. The form
shows text search, every active facet, the expression, ordering, relevance
anchor (if any), and result limit. The server combines the facets and expression
into the saved query and preserves literal text separately as `search`. This
keeps text matching and snippets intact when reopened. A text-only or facet-only
search can be saved directly. Default browser ordering is persisted as `match`
(title hits first, then title/folder/path), and the Library's effective 50-result
cap is saved when no smaller limit was selected. `bn find --view` applies the
same text, constraints, ordering and cap. A saved view follows changing vault
contents; it is not a frozen list of note IDs. Saving is a configuration write and
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

The Library reports the full match count and the current page range. A requested
query limit bounds the entire ordered result set; the 50-note payload cap bounds
each page. Bulk actions affect all selected notes, including other result pages.
The search API keeps its JSON array response and adds
`X-BN-Search-Total` (matches before limits), `X-BN-Search-Limit` (requested limit,
zero when unlimited), and `X-BN-Search-Cap` (server display cap) response headers.

Selection recovery uses the same read-only search API with up to 50 repeated
`selected` keys per request. It checks membership **after** ordering and the total
query limit, before page offset and payload slicing. This parameter only narrows
the result; it cannot turn an empty search into a whole-vault query. Recovered
keys receive current wikilink targets and one-based result positions for copying
in query order, and bulk actions still require a new
preview.
