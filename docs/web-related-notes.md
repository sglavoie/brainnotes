# Related Notes explorer

Part of the [local web server](web-server.md).

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
