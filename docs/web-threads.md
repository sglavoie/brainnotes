# Threads

A thread keeps a question, collected passages, commentary, and a draft in one
ordinary Markdown note. Open **Threads** in the workspace navigation, choose
**Start a thread**, and give it a title and question. The default folder is
`Threads`; you can change it before creation.

While reading a note, select a passage and choose **Add to thread**. Choose a
thread, a group (**Evidence**, **Counterexamples**, or **Still unclear**), and
optionally explain why the passage matters. The note's **Actions → Add to
thread** collects the whole note as a source without quoting a passage. Source
notes are not changed. To start another thread while collecting, use the link
that opens Threads in a new tab, then reopen the collection dialog.

The thread opens in the existing editor with collected material alongside your
question and draft. On narrow screens, writing comes first and collected
material follows below. Move passages earlier or later, change their groups,
edit their commentary, or remove them. These changes join the editor's undo
history and become durable only when you choose **Save**. **Refresh material**
reads the current editor text, including unsaved changes. **Source** view shows
the complete Markdown, including collection markers; Find also reveals matching
text inside a collection. The usual editor conflict and draft recovery controls
remain available.

Collected quotations are snapshots. Their source wikilinks remain ordinary
links, so Brain Notes' rename/move operations can update them. A source whose
revision changed, or whose original revision is unknown, gets a notice to check
the quotation. This is deliberately conservative: even a metadata edit can
trigger the notice. Missing or ambiguous sources get a separate warning. Neither
condition changes or deletes the collected text.

The Daily Brief offers up to three recently modified threads under **Resume a
thread**. The complete list is on **Threads**. Saved questions, passages, order,
commentary, and writing live in the Markdown file, so they are available on
another device once the vault's normal synchronization delivers that file.
Unsubmitted collection commentary is kept in this browser tab; unsaved editor
text uses the editor's existing browser draft recovery.

**Open in synthesis** uses the saved thread as the anchor and selects its
available source notes in collection order. Repeated sources appear once. Existing
synthesis work is preserved. If the thread has more than the workbench's ten
supporting-note limit, choose sources with its **Add** buttons; nothing is
silently truncated. Missing sources and changed quotations remain visible as
warnings. AI is optional, and all collection and manual writing work in local
and hosted modes. Read-only replicas can read the Markdown note but do not offer
thread editing or collection.

A thread holds at most 100 collected items. Each quotation and commentary is
limited to 8,000 characters. Collection writes use the same revision checks,
idempotent save receipts, journal, content history, and sync behavior as other
editor saves. After an uncertain collection response, **Check addition** retries
the exact same save; it is also available after reopening the same passage in
the same tab following a reload.

For external editors: keep the `bn:thread` HTML comments around the collection
and its items intact. Question and draft text outside the collection is preserved
verbatim. Unrecognized text inside the collection causes the collection tools to
refuse the edit rather than dropping your writing. The full source editor remains
available to repair the markers.
