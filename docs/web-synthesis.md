# Synthesis workbench

Part of the [local web server](web-server.md).

`/synthesize?source=<note-key>` turns one note and the notes you choose beside it
into a new note. Every note page carries a **Synthesize** link that opens the
workbench with that note as the anchor. The route requires exactly one non-blank
`source`, so a missing, blank, or repeated key is a `400` rather than a silently
chosen anchor. The page is a server-rendered shell that is inert without
JavaScript and explains itself rather than rendering dead controls.

The anchor is always first and can be neither removed nor reordered. Supporting
notes come from the anchor's Related suggestions or from a search box, up to ten,
and their order is yours: drag a row's handle, or focus it and press `Up`/`Down`.
The order matters, because it is the order the context packet and the note's
Sources list use. A supporter you previously dismissed in the Related explorer can
still be chosen here — an explicit selection is authoritative, and recall
suppression never drops a note you picked.

**Prepare context** builds the packet. `POST /api/synthesis/context` reloads
configuration, resolves every key against the current vault, ranks the selection,
and returns the anchor's complete body plus at least one exact excerpt per
supporter, together with a manifest and a `context_id`. A supporter with no
extractable body block is refused by name rather than reduced to a bare title, so
an empty or unusable note produces an actionable message instead of a silently
thinner packet. The byte budget grows as far as the mandatory anchor body and one
excerpt per supporter require, up to a hard 1 MiB ceiling.

A `context_id` lasts one hour and is reusable, so generating twice — or previewing
after a failed generation — costs nothing and never rebuilds the packet. Changing
the sources, their order, the purpose, or the free-text prompt discards it and
asks you to prepare again. Editing the title, folder, or draft does not, because
those never reach the packet.

Packets and receipts are also bounded in memory. The server keeps at most 32
context packets and 8 MiB of packet bytes, and at most 32 unredeemed receipts and
8 MiB of planned note bytes; a new one past either bound drops whichever record is
closest to expiry, and every record is released on its own within a minute of
expiring rather than waiting for your next request. An evicted packet is the same
`404` an expired one is, and an evicted receipt the same `preview_required`, so
the remedy is unchanged: prepare or preview again, with everything you wrote
intact.

Those controls stay editable while a request is in flight, so a packet or a
receipt is accepted only when the inputs it was built from still match what you
see: a context response is dropped unless the source order, purpose, and prompt
are unchanged, and a preview response is dropped unless the context, title,
folder, and draft are. A dropped response leaves everything you wrote intact and
asks you to prepare or preview again, so a slow request can never re-enable
Generate or Create for values you have since replaced.

Generation is optional and unavailable unless `[agent].command` is configured;
without it the button names the key to set and manual drafting is unaffected. The
first generation of a session shows a disclosure naming the local command and what
is sent to it. It states that your prompt and that packet are retained in server
memory until the context expires, is evicted to keep memory bounded, or the server
restarts and are never written to disk, that the stored packet is sent to the
command only after you accept, and that the generated candidate is not stored on
the server.
`POST /api/synthesis/generate` feeds the stored packet bytes to that command's
stdin and returns its stdout verbatim beside separately rendered safe HTML. The
output never lands in your draft: it appears in its own candidate panel with
Replace, Append, Copy, and Dismiss, so repeated generations and outright failures
both leave whatever you have written untouched. Freshness is checked immediately
before and after the command runs — a source note that moved, vanished, or changed
bytes, and an edited `brainnotes.toml`, are all `409`, while an expired
`context_id` is `404`, because the remedy there is to rebuild rather than to
retry.

The destination folder defaults to the anchor's own folder and stays editable.
**Preview** plans the note without writing it: `POST /api/synthesis/preview`
returns the destination path, the exact Markdown the file will hold, a rendered
view of it, the receipt's expiry, and the ordered Sources list. The draft is sent
exactly as typed — the server normalizes line endings and trims edge blank lines
itself — and is refused if it is empty, larger than 1 MiB, not valid UTF-8, or
already contains a `## Sources` heading, since the workbench appends that section
itself. Each refusal is shown beside the title, folder, or draft it belongs to.

The Sources section is a `## Sources` heading followed by one
`- [[Folder/Note|Title]]` bullet per note in selection order, anchor first. It is
generated rather than typed, so it always matches the packet the note was written
from.

`POST /api/synthesis/apply` accepts nothing but the `preview_id`. Title, folder,
draft, and sources all come from the stored plan, so no request can substitute
content you never reviewed, and the bytes written are the ones the preview showed.
A receipt lasts five minutes and is single-use: it is spent before any file is
touched, so a replay — including a retry after a failed apply — is refused with
`preview_required` instead of creating a second note. Any change to the title,
folder, draft, or selection invalidates the receipt in the browser, and you
preview again.

Version one creates new notes only. If the destination is taken by the time you
apply, whether between the preview and the apply or during the commit itself, the
apply is refused and the existing file is left exactly as it was. The same holds
for a source note edited after the preview: the plan pins every source revision,
so it is refused with `409` and neither a note nor a journal entry is created.
Change the title or folder, or preview again, and retry.

A successful apply writes one undo journal entry named `bn web synthesize`, so a
synthesis note can be taken back with `bn undo <journal_id>` or from the change
history panel on `/library`, like any other web write. Building a context and
generating a candidate are read-only: neither writes Markdown, recall state, nor
the journal, and neither leaves anything on disk.

Your selection and the text you typed are kept in the browser's local storage,
namespaced by vault and anchor, so a refresh or an accidental close does not lose
them. Only note keys and your own text are stored — never note bodies, excerpts,
the packet, or rendered HTML — and note identities are rehydrated on load, which is
also how a note that has left the vault is detected and reported. A restored
candidate is shown as Markdown, because its rendering was never saved. A context
is never restored: after a refresh you prepare it again. **Discard** clears the
saved record and the whole selection, and asks for confirmation whenever there is
meaningful work to lose. A successful apply clears that record too. If the browser
refuses to delete it, neither says the saved work was cleared: a warning explains
that it will come back, and the apply stays on the workbench with a link to the
new note instead of navigating past the warning. A browser without local storage
or without a writable clipboard loses only those conveniences; creating a note by
hand still works end to end.

Maintainer smoke validation covers a 1440×1000 desktop and a 390×844 narrow
viewport with keyboard-only selection, reordering, generation, preview, and apply;
light and dark themes; a vault with no agent configured; and an agent command that
fails, times out, or returns unusable output.
