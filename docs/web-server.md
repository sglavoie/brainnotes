# Browser workspace

Start with `bn serve`, then choose a task below. Brief, Library, Pinned, and Graph
are in the main navigation. On wide screens, these destinations and **Search**
are available in the header. On narrower screens, the **Search notes** magnifying
glass opens Library directly; **Workspace** opens the full navigation,
search, and theme controls on Brief, note reading, and other workspaces.
On Library and Graph, the page's own search stays in the main content.
**New note** is a separate action, shown as **+** beside Search on compact headers. **Manage** contains
Properties, Health, Settings, Sync, and History, subject to the server's capabilities.
Saved views have their own row. In the source editor, **Workspace** opens this
navigation, search, and theme controls without leaving your draft.

Every workspace has **Skip to content**; note pages have **Skip to note**.
These links and the navigation disclosures work without JavaScript.

## Find a note

Use Library to combine text search, filters, and an optional **Query expression**.
Before searching, recently modified notes appear first in Recent activity, with
short prose excerpts and relative dates. Hover a date for its exact timestamp.
**Compact list** hides excerpts for faster scanning and remembers the choice in
this browser.
Sorting and result actions appear once a search or filter is active.
Library shows one results list as you type; Enter moves focus to its heading.
The matching count and result actions sit together above the list.
An active search uses a compact phone layout with two-line excerpts to leave more
room for results.
**Filters** opens the filter picker and the optional **Query expression** disclosure;
active constraints remain visible when it is closed. Expressions from a saved view
or URL open both disclosures so the active query stays visible. **Search actions**
contains Copy search link and Save as view.
While a search updates, previous results remain visible with an updating treatment;
a loading announcement appears only if the request takes longer than 350 ms.
Superseded requests are cancelled. Text composition finishes before a search runs.
Search on other pages still offers a quick-open popup. **Search help** explains
how constraints combine when you need it.
A note must match every active constraint. Expressions from a URL or saved view
open their editor automatically. Removing one constraint keeps the other
constraints, result limit, and sort order.

**Save as view** stores text search, facets, and the expression together.
**Copy search link** copies those constraints, sort order, and limit, without a
result-position anchor. Clipboard controls offer a selected text field if direct
copying is unavailable. Sort choices include default order, title in either
direction, newest or oldest modified first, and **Vault path**. A custom saved
ordering appears as **Current order**.

Recent activity is open on the initial Library screen and collapses when a
search starts. You can open it during a search; typing keeps that choice.

Results emphasize titles, excerpts, and locations. Excerpts show readable link
labels, highlight matching text, and finish boundary words where possible.
Narrow screens show up to three excerpt lines; open the note to read the rest.
Matching still uses the original note text. **Match details** expands the
matching fields and structural information such as orphan or dead-end status;
these are neutral facts. Broken links remain visible as warnings.

Use **Next results** and **Previous results** to browse up to 50 matches per page.
A saved view's explicit limit still bounds the whole result set. Note links keep
the page position for **Back to results**; Copy search link and Save as view
omit that navigation position.

Select notes with their checkboxes or **Select displayed**. The selected count,
**Clear selection**, and **Copy wikilinks** appear once there is a selection.
**Selection help** appears alongside the actions once notes are selected.
Shift-click a checkbox to select or clear the range from the previous checkbox.
Selections and copied wikilinks
include other result pages; copied links use exact-case vault targets in result
order, one `[[target]]` per line. Copying also works on read-only replicas. The
count identifies selections outside the current page. Changing the query, filters, or ordering
clears the selection. Bulk actions apply only to the selected notes and still
require a preview before applying changes.

With browser session storage available, selections also survive opening a note,
**Back to results**, and reloads in the same tab. They are scoped to the vault and
search. On return, the server rechecks the saved keys against the current search,
including its total limit; missing or nonmatching notes leave the selection.
A failed check offers **Retry search** and keeps the saved keys for another try.
Preview receipts are never restored. Changing the search or clearing selection
removes its stored selection. **Select displayed** affects the current page only;
**Clear selection** clears all pages. A selection change invalidates any pending
bulk preview. Shift-click ranges stay within the displayed page.

When there are no matches, remove just the search text, filters, or expression,
or use **Reset search** to clear the entire search. **Create a note titled…**
opens New note with the text query filled in; it does not create anything until
you confirm the title and folder. The originating search travels through
creation and editing. **Back to results** on the form and reading page returns to
that exact search, even if the new note does not match it.
Creation is unavailable on read-only replicas.

**Retry search** repeats a failed search with the current query and filters.
Dismissing header search dismisses its retry action. **Retry filters** reloads
filter choices without changing the search, selected filters, sort, or limit;
selected filters can still be removed while the choices are unavailable.

See [query expressions and saved views](web-server-query-bar.md) for syntax.

## Read and follow connections

A note shows its rendered body, properties, references, outgoing links, and
Contents. **Referenced in…** lists the notes that link here. Each **Read passages**
disclosure reveals readable excerpts; **Source details** reveals line or property
locations only when needed. Properties initially show a compact summary of the first two values.
**All properties** reveals the complete values and editing controls; the expanded
or compact preference is remembered in this browser. Choose **Edit** beside a
property to reveal **Update property**, **Remove property**, and list controls.
**Add property** opens the new-property form. Read-only replicas show values without edit controls.

Contents highlights the current section as you scroll without moving focus or
changing the URL. At widths up to 960 pixels, **Metadata** and **Contents** start
collapsed and open independently. Opening mobile metadata does not change your
desktop sidebar preference.

Prose stays within a 70-character measure on wide screens, including when
metadata is hidden. Tables, code, and media can use the wider content column.
**Edit** opens at the passage currently being read. On narrow screens,
switching Edit/Preview keeps the corresponding passage in view; returning to the
note retains the reading position.

**Back to results** preserves Library text, filters, and sort through reloads,
new tabs, and following links between notes. Returning restores the opened
result's position and keyboard focus after results load. If it no longer matches,
the search loads normally; interacting while loading cancels the automatic jump.
Pinned, Brief, and Graph links also carry a named return destination.

The reading toolbar keeps **Edit**, **Pin**, and **Actions** visible. On phones,
Search, Workspace and note actions share a compact row where space permits;
Capture and New note are available in Workspace.
**Actions** groups copying, note history, external-editor access, synthesis, and Trash.

Use **Copy code** in a code block’s header, the **#** button beside a heading
(**Copy section link**), and
**Copy note URL** for a clean note address without search context or a section
anchor. Each offers manual copying when the browser refuses clipboard access.
Ask AI and Daily Brief insights offer the same fallback for their Markdown.
The body appears before Ask AI, and **Skip to note** bypasses navigation and
metadata controls. Skip links are omitted from print output.

Explore further with the [Related Notes explorer](web-related-notes.md),
[Graph](#explore-graph), or [Synthesis workbench](web-synthesis.md).
**Explore nearby notes** beside Related Notes opens Graph around the current note
and selects it. **Show whole graph** returns to the full vault view.

## Create and edit

Choose **New note** and start in **Your thought**. The first sentence suggests an
editable title; once you edit that title, further writing leaves it alone. The
optional folder sits beneath it as an editable destination. A title supplied by
search or restored after an uncertain creation is preserved. Select **Create and write**
to create the note with this writing and continue in the editor. You can also start
with just a title. A blank folder uses the vault root; **Save** stores further edits.
**Capture** sends a quick
thought to the configured inbox instead.

Unfinished titles and folders are kept in this tab for this vault when browser
storage is available. If a creation response is lost, **Check creation** safely
reuses the original request, including after a reload; it never checks or retries
automatically. Confirmed creation clears those saved details. If browser storage
is unavailable, keep the form open until creation is confirmed.

On a note, choose **Edit**, or **Open in external editor** on local
servers. The source editor opens with the writing surface visible on a narrow
screen. Its toolbar keeps **Save** nearby while scrolling; **Format** reveals formatting
commands and Escape closes them. Frontmatter starts collapsed; **Show
frontmatter** expands it and **Hide frontmatter** folds it again without changing
the source or marking the note as edited. Find still searches the full source.
On desktop, **View** chooses **Write**, **Split**, **Preview**, or **Source**; **Type** offers
Book, Sans, or Monospace, with code always in monospace. Book and Sans follow the
reading type preference in both directions; Monospace remains an editor choice.
These choices are remembered in this browser. A browser without saved preferences
starts with Write and Book. Inactive prose syntax and blank-line spacing are softened without changing the
Markdown. An inactive leading heading that repeats the page title is folded in
writing mode. Moving the cursor into it or finding it reveals the exact source;
**Source** shows the original heading and syntax. On phones the View choice lives
inside Format alongside Type. Write centers the same reading measure and keeps Save in the
toolbar. Narrow screens retain Edit/Preview tabs, with the type choice inside Format.

Below the writing surface, **This note and this vault** contains History and
**Rename note**, which reveals the title field and Rename button. Rename changes the
filename and incoming wikilinks; Save changes the source text. The folder fields
in New note, the editor's Move action, and Library's bulk Move suggest existing
folders while allowing a new path or manual entry if suggestions cannot load.

When a management action requires saved text, the editor offers **Save first**
or Cancel. Other management actions are disabled while it runs. Cancel during
Save first cancels the follow-up action, although the save can still finish.
The editor keeps inputs and avoids navigating to an unconfirmed rename or move
destination if the response is lost or unreadable.

**Find**, or Ctrl/⌘ + F while editing, searches and replaces across the whole
source, including text outside the viewport. Enter and Shift + Enter move
between matches; Escape closes Find and returns to editing. On narrow screens,
Find switches from Preview to Edit. Replacements remain in the draft until Save
and can be undone while editing.

**Retry preview** renders the current draft without saving. If rendering failed,
the previous preview is labeled outdated. Retrying disables the button and
returns focus to the preview when it disappears, unless you moved focus elsewhere.

The note title link returns from editing to reading, preserving the originating
workspace and reading position. This survives reloads, renames, and moves.
If the section was removed, reading position uses a proportional fallback.

If a write reply is lost, **Check outcome** checks the journaled attempt. A
confirmed result links to History. A restored reminder disappears when its
attempt is confirmed; the confirmation and History link remain visible on the
current page.

## Adjust settings

Settings groups portable configuration by purpose and shows readable names and
current values. Group links jump directly to each section without discarding drafts.
**Find a setting** narrows the list by name, group, key, or description; rows
with unsaved edits and pending confirmations remain visible even when they do
not match. Clearing the search restores the full list.
Upload sizes display and edit in MiB; conversion preserves exact byte counts and
refuses values that would require rounding to a whole byte. Choose **Edit** beside a
setting to reveal its controls; size and
age fields name their units. **Configuration details** contains the exact key,
type, and source file. Machine-local overrides remain visible as explanations of
why a setting cannot be edited here.

Save previews the change beside that setting before confirmation. Saving one
setting or choosing Refresh preserves unsaved edits in other fields, along with
focus and text selection. **Unsaved changes** identifies values still being edited.
Changing a value after preview requires another preview before it can be saved.

## Recover and resolve uncertain changes

**Actions** on a note contains Synthesize and Trash. Destructive confirmations
use the same application dialog as Undo: affected content and consequences are
shown with an explicit action button, Cancel, Escape, and focus restoration.
Sync decisions retain their preview and one-use receipt requirements.

History opens on recent changes with path search visible. **More filters** reveals
path matching, origin, content type, and page size. Active constraints remain visible
when those controls are collapsed.

History leads with a readable action summary; **Details** keeps the exact command
and entry identifier available. Choose **Preview restore** to inspect the change.
Recorded dates show a named month, full year, and time-zone offset in the browser's
local time, including restore previews and Undo confirmations.
For notes and templates, the preview highlights removed and added text and collapses
long unchanged passages. It labels the current version and the version after
restoring. Binary files, private state, and files above the comparison limits retain
their path and restore effect with an explanation of why text is unavailable.

If the editor encounters a changed note, it compares **Current note** with **Your
draft**. The comparison follows edits to the draft; expanding unchanged context or
full current text does not save anything.

Undo previews the recorded action, time, and affected paths. Confirmation is
bound to that entry and preview, so a newer change cannot become the target.
A stale preview requires another review. Use **History** for recovery, and
[Health and Daily Fixes](web-health.md) for vault maintenance.

When a write loses its response, the outcome is **unconfirmed**: it may already
have happened. This includes Trash, which stays on the note until success can be
established. For Trash, Synthesize, Library bulk changes, Property workbench
changes, portable settings, and Templates, choose **Check outcome**. The browser
checks the exact attempt against
the journal and identifies a confirmed History entry. An unconfirmed result can
mean the write is still running or recovery needs attention; check again, then
refresh current values and make a new preview if needed. The browser never
resends an unanswered write. Pending checks remain available after navigation or
reload in the same tab. Settings and Templates also offer **Check History in a
new tab**, preserving typed inputs.
A failed preview is reported separately because it submitted no change.

If creating a missing note is unconfirmed, **Refresh to check** reloads the page
to see whether the link is still broken; it does not submit creation again.
Saves and captures retain their draft during delayed confirmation. A slow save
says **Still saving…**; an unanswered write says **Save not confirmed**.
Capture offers **Check / retry capture**: retrying checks its recorded receipt
before appending anything, so a lost response cannot duplicate the thought.
Unanswered capture IDs survive reloads in the same tab when browser storage is
available. The editor's capture control uses the same receipt protection.
**Draft kept in this browser** describes a local copy; **Saved to vault** confirms a save.
**Save not confirmed** keeps Save available for a safe retry. Other uncertain
changes point to the applicable outcome check or current values before retrying.

Synchronization shows one summary and when the server checked it. Healthy
transport details start collapsed; pending work or problems open them automatically.
Expand **Synchronization details** for the exact check time and the separate saved,
packaged, acknowledged, and applied facts.

The shared sync line links directly to review when incoming changes need decisions,
using the count as its link text. Other problems and an unreadable status show a
short explanation. Routine status stays on the Sync page. The status
refreshes after confirmed writes and polls pending work for up to one minute. It
stops while the page is hidden and ignores superseded replies.

## Explore Graph

Graph uses the same **Workspace** menu as the other pages. Search and its
Filter/Focus mode stay visible. **Refine graph** groups the facet picker, query
expression, and layout controls; it opens for incoming filters or advanced layout
options and shows an active marker when collapsed. A note-centered view keeps it
closed so the neighborhood remains visible on entry. Within it, **Filters** opens the picker
while active constraints stay visible. **Query expression** opens automatically for a
query loaded from a URL or saved view. **Graph layout** contains spread, minimum
degree, folder collapsing, isolation, label mode, and viewport controls.
On phones, the canvas appears before the selected-note details.
When opened from a note, Graph starts with that note and its immediate
neighbors; the note is selected and **Show whole graph** remains visible above
the canvas.

Choose a note in **Notes and folders in this view**, or use arrow keys on the
graph canvas to move through displayed notes in title order. Home/End select the
first/last note; Escape clears selection. **Open note** follows the selected
note. Choosing a collapsed folder in the list expands it. Both the list and
keyboard navigation respect filtering, isolation, and folder collapsing.

## Reference

- [Server commands](web-server-commands.md)
- [Rendering, attachments, options, and security](web-server-security.md)
- [Serving modes and capabilities](internals/serving-modes.md)
- [Contributor setup and browser verification](development.md)

## Daily Brief

On writable servers, **Quick capture** appears above the recommendations, with
the input and Capture button visible on entry on phones. Keyboard and wikilink
hints appear while composing. A confirmed capture returns the field to one line,
keeps focus ready for the next thought, and shows a compact receipt with an inbox
link. **Captured this session** reveals up to five earlier receipts. Failed or
unconfirmed captures keep the draft and recovery instructions visible.

Recommended notes follow capture; maintenance stays farther down the page.
Each card shows its title, a short reason, excerpt, and **Another** action.
Continue also shows the note's edit date when available. **Continue** offers
**Resume writing** in the browser on writable servers, or **Read note** on a
replica. **Rediscover** offers **Revisit note**; **Connect** names both notes and
their relationship, with **Explore connection** comparing their passages. These
actions work without an external editor. A note title opens the browser reader.
The configured capture inbox is excluded from Brief recommendations. Excerpts
prefer substantive passages over short introductions such as “Every Sunday:”.
Connect cards prefer a passage mentioning the shared connecting note, or the
source note for a direct connection. Long passages are clipped around that
mention; when no matching prose exists, the ordinary excerpt remains available.
This changes the excerpt, not the selected notes or ranking. Existing daily
cards retain their saved excerpts until replaced with Another or a new day's selection.
**Why this note?**
reveals its location and recommendation reasons; **Ask AI** expands the prompt.
Both start closed again when Another loads a replacement. Activity counts and
optional tool setup follow the recommendations.

When this browser has a remembered passage, Continue names the last section,
shows its excerpt, and offers **Continue here**. Resuming returns to that source
line with a brief highlight. These bounded hints are scoped to the vault and
browser; they are separate from draft recovery and do not change the note or its
recommendation ranking. If browser storage is unavailable, the ordinary excerpt
and navigation still work. Connect also previews a passage from each displayed
source, preferring their shared connecting note. If those previews cannot load,
the original card remains usable.

## Reading and quick interactions

**Quiet reading** hides the metadata, contents, and secondary reading panels;
**Book type** uses a serif face for prose and is the default for a new browser.
Turning it off selects Sans in reading and writing. Both preferences persist in this browser.
**Quiet reading** and **Book type** live under **Actions**.
The compact property summary sits beneath the title, with **All properties**
expanding the full editor there. Empty backlink and outgoing-link sections stay hidden.
A leading heading that repeats the page title is visually suppressed without
editing the Markdown. Printing retains a single title and the note body.

Hover deliberately over a note link to preview an excerpt. The eye button also
opens the preview by touch or keyboard; **Alt + Down** works on a focused link.
**Escape** closes it, and **Open note** preserves the originating Library search.
Previews are available in note bodies, backlinks, related notes, and Library
results. They do not record an open or send content to an agent.

On writable servers, **Capture** or **Ctrl/⌘ + Shift + Space** opens a small
capture dialog over the current workspace, including unsaved writing. Escape
keeps the capture draft; **Ctrl/⌘ + Enter** submits it. A confirmed capture closes
the dialog, restores focus, and offers an inbox link. Failed or unconfirmed
captures retain the text and the existing safe retry behavior. On the Brief, the
dialog uses the same live form as the inline capture area.

Related notes lead with the connection and excerpt. **Why this connection?**
contains the supporting details; common filler words are omitted from textual
match explanations. Unconfigured AI appears as an optional setup disclosure.
Short confirmation and card transitions respect reduced-motion preferences.

Select up to 8,000 characters in a note and choose **Write a thought** in the
small floating toolbar (or press **Alt + Enter** with the selection focused).
The composer keeps your quotation and source visible while you write. **Save
thought…** suggests an editable title; **Save thought** creates a Markdown note
with the quotation, a source wikilink, and your writing. **Back to your passage**
restores the selection and reading position. Escape also returns to reading,
keeping unfinished writing in this tab. Selecting the same passage again restores
its draft; different passages have separate drafts. After an uncertain response,
**Check creation** checks the original request without duplicating the note,
including after a reload. This action is available on local and hosted writable
servers. Ordinary reading loads the composer only when you select a passage.

Confirmed captures briefly show **Captured ✓** on the button, and pinning marks
the control in place. The editor reserves a status line for save feedback.
Brief cards show a remembered section or the note's actual modification date
when available; hover the date for the exact timestamp. Connection previews
highlight the connecting reference, and a saved thought displays links back to
its sources.

## Thinking workspace

Use **Search** in the header or **Ctrl/⌘ + K** from any workspace to find notes,
recently visited notes in this browser, saved views, and actions. Arrow keys choose a
result, Enter opens it, and Escape returns focus to your writing. Search covers
the vault independently of the current Library filters. With a query entered,
**Create “…”** opens a reviewable creation form and **Search in Library** carries
the text into Library. Creation is absent on read-only replicas; Keep beside
appears only when the selected result is a note. The editor uses
**Ctrl/⌘ + Alt + K** for Markdown links; **Ctrl/⌘ + Shift + K** still inserts a wikilink.

**Explore connection** in the Brief, Related Notes, or selected graph note opens
both notes together. Passages mentioning the other note or connecting note are
preferred; otherwise the opening prose is shown. The connecting note is a link,
and each source title opens the full note. Previewing a connection does not
record an open or change the notes. On writable servers, **Write a thought about
this connection** opens a writing area beneath the passages. Write first, then
choose **Save thought…** to review an editable title suggested from the first
sentence, then **Save thought** to save. Both source links are included automatically.
After confirmed creation, the saved thought appears beneath the source pair.
**Keep exploring** returns to the same place with focus on the connection you
opened; **Open note** opens the new note. An uncertain response keeps the form
and offers **Check creation** using the same request. Unfinished writing and an
unconfirmed creation receipt are kept in this tab; reopen the same connection after reloading to
recover them. Short link-list passages include nearby substantive prose, and the
matching reference is highlighted.

Search results include a matching passage with highlighted words. **Alt + Enter**,
or **Keep beside**, opens the selected note as a reference while preserving unsaved
writing.

A link preview's **Keep beside this note** action keeps a reference visible while
you read or write. It remains open across navigation and browser sessions for this
vault; close it with **Close reference** or Escape while focus is inside it. Desktop uses a
side panel with a draggable divider (also adjustable with arrow keys); its width is
remembered in this browser. Opening and resizing it preserves the visible reading
passage. Phones use a panel below the reading area that can collapse to a small
strip. References show bounded plain-text passages, with a full-note link for
longer notes. No attachments or
agents are loaded by a reference preview.

Reading and writing places are remembered for up to 32 recent notes in this
browser, scoped to the vault. Reopening a note resumes the last passage; an
explicit section link or editor return position takes precedence. The Brief's
Continue card uses the remembered passage too. **Workspace → Forget remembered
places** clears these positions, recent search notes, and the side reference for
this vault, without clearing drafts or pending creation receipts. If browser
storage is unavailable, normal reading and writing remain available.

The Brief's capture field starts as one line and expands on focus or when it
contains a draft. Capture and recommendations remain first. **Vault overview** expands
health, pinned notes and recently modified notes. Reading uses a distinct title
aligned with the prose, compact property presentation, and the existing Quiet
reading and Book type preferences. Interaction motion respects reduced motion.

Graph uses muted folder colors and wraps labels at word boundaries. Selecting a
note in **Focus** mode gently centers it without changing zoom or rearranging
nodes; ordinary Filter mode preserves the viewport. Reduced motion makes these
viewport changes immediate. Reading-to-writing title transitions are enabled
only in browsers supporting cross-document view transitions, and only when
reduced motion is off.

### Thoughts beside their source

A thought created from a selected passage leaves an expandable **Your thought**
marker beside that passage. Multiple thoughts at the same passage share a count.
**Read beside this passage** opens the saved note as a reference. Reload reconstructs
these associations from the ordinary `From [[source]]` line and literal quotation
in the saved note, including older thoughts created with that format. Source files
are never annotated or rewritten, and the normal wikilink rename/move behavior
keeps provenance connected.

Only a unique match in the current rendered prose receives a marker. Changed,
deleted or repeated quotations remain under **Thoughts from earlier passages**
with their quotation and saved-note link, without guessing a location. Discovery
is bounded to 50 matching notes per source; the references list still includes all
linked notes. An unrelated blockquote or an ambiguous source link is not attached.

A note preview kept beside the page retains its title and excerpt while the fuller
reference loads. Connection comparisons and new thought markers use short transitions
that respect reduced motion.
