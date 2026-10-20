# Local web server: Note actions, Trash, rendering, filters, graph, options, assets, and security

Part of the [local web server](web-server.md).

## Note actions, Trash, and rendering

The overview search results can select notes for batch lifecycle work: move to
Trash, move to a folder, set/list-edit frontmatter, or unset frontmatter. Bulk
actions require an inline preview first: the server returns a one-use
`preview_id`, counts, destination paths, frontmatter before/after values, move
rewrite counts, and any failures, then the follow-up apply call must reuse that
preview receipt before it expires. Note pages can move the current note to
Trash, edit frontmatter in place, create a blank note for a broken wikilink
target, and edit the note body with **Edit in browser**, a raw-Markdown source
editor with preview that saves through `/api/source/save` against the revision it
loaded. **Edit in browser** is available in local and hosted modes and absent on
read-only replicas, which refuse every `/api/source/*` route. In local mode,
**Actions → Open in external editor** opens the file in the configured editor
instead. These web
actions use the same note-management code as the CLI where practical, refresh
the served vault state after success, and return structured JSON for automation.
The frontmatter sidebar reports success only after the server confirms it and
offers **Refresh properties** to update the displayed values. If the outcome
cannot be confirmed, **Refresh to check** lets you inspect the note before
trying again. Refresh is explicit and preserves the current URL, including
Library return context; overlapping property submissions are blocked while a
request is pending.

Trash is a visible vault-root `Trash/` folder. Trashing a note moves the file
there, chooses a non-conflicting destination name, and leaves `Trash/` ignored by
default so trashed notes do not continue to appear in search, graph, or
diagnostics.

After trashing a note from its note page, the Daily Brief shows an Undo notice
for that specific change. Undo restores the note and returns to it; conflicts
remain visible with a link to History. The notice survives the redirect in
this tab only and is scoped to the current server session. If browser storage
is unavailable, the note page stays open with the same Undo control.

Note pages group **Copy wikilink**, **Copy note URL**, vault path,
route, and Markdown link under **Actions**. Copy controls offer
selected, read-only text for manual copying
when clipboard access is unavailable or refused. Keyboard navigation in header
search keeps the selected result visible within the dropdown without scrolling
the page.

Rendered note pages syntax-highlight fenced code blocks that include a language
info string, such as `go`, `sh`, or `json`, on the server. Unlabeled fences
remain plain escaped code blocks. Note bodies also auto-link GFM-style bare URLs
such as `https://example.com`. Beyond that, rendering supports the GitHub
Flavored Markdown extensions — tables, `~~strikethrough~~`, and task lists
(`- [ ]` / `- [x]`, shown as read-only checkboxes on the rendered page; change
them in **Edit in browser** or your editor) — plus footnotes, definition lists, and typographic punctuation
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

The body limit is the profile's default, not an absolute cap. A few routes whose
payload is a whole note, draft, or file are widened wherever the profile is
narrower, and each re-applies the same bound itself: `/api/source/preview`,
`/api/source/save`, and `/api/source/create` accept up to 12 MiB + 32 KiB (three
times the 4 MiB note-source limit, for form encoding), `/api/synthesis/preview` up
to 3 MiB + 32 KiB, and `/api/attachments/upload` up to 25 MiB (the most any vault
may configure). So a hosted server can save a full-size note or upload a full-size
attachment; local serving's 32 MiB already covers all three. See
[Serving modes](internals/serving-modes.md) for the rationale.

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
body over its route's limit (the profile's, or a widened one listed above) is
rejected with `413`. Every response is
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
reused previews, and previews taken before the configuration last changed, are
rejected with `409`. Preview receipts stay process-local,
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

