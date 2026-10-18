# Browser workspace

Start with `bn serve`, then choose a task below. Brief, Library, Pinned, and Graph
are in the main navigation. On Brief, Library, Pinned, New note, Synthesis, the
editor, note reading, and management pages, **Workspace** opens navigation and theme controls.
**New note** is a separate action. **Manage** contains
Properties, Health, Settings, Sync, and History, subject to the server's capabilities.
Saved views have their own row. In the source editor, **Workspace** opens this
navigation, search, and theme controls without leaving your draft.

Every workspace has **Skip to content**; note pages have **Skip to note**.
These links and the navigation disclosures work without JavaScript.

## Find a note

Use Library to combine text search, filters, and an optional **Query expression**.
Library shows one results list as you type; Enter moves focus to its heading.
**Filters** opens the filter picker; active constraints remain visible when it is
closed. **Search actions** contains Copy search link and Save as view.
Search on other pages still offers a quick-open popup. **Search help** explains
how constraints combine when you need it.
A note must match every active constraint. Expressions from a URL or saved view
open their editor automatically. Removing one constraint keeps the other
constraints, result limit, and sort order.

**Save as view** stores text search, facets, and the expression together.
**Copy search link** copies those constraints, sort order, and limit, without a
result-position anchor. Clipboard controls offer a selected text field if direct
copying is unavailable. Sort choices include **Oldest modified first** and
**Vault path**.

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
**Selection help** appears alongside the actions once notes are selected and explains range selection: Shift-click a checkbox to select
or clear the range from the previous checkbox. Selections and copied wikilinks
include other result pages; the count identifies
selections outside the current page. Changing the query, filters, or ordering
clears the selection. Bulk actions apply only to the selected notes and still
require a preview before applying changes.

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

A note shows its rendered body, properties, backlinks, outgoing links, and
Contents. Properties initially show their values. Choose **Edit** beside a
property to reveal its existing Set/Unset and list controls; **Add property**
opens the new-property form. Read-only replicas show values without edit controls.

Contents highlights the current section as you scroll without moving focus or
changing the URL. At widths up to 960 pixels, **Metadata** and **Contents** start
collapsed and open independently. Opening mobile metadata does not change your
desktop sidebar preference.

**Back to results** preserves Library text, filters, and sort through reloads,
new tabs, and following links between notes. Returning restores the opened
result's position and keyboard focus after results load. If it no longer matches,
the search loads normally; interacting while loading cancels the automatic jump.
Pinned, Brief, and Graph links also carry a named return destination.

The reading toolbar keeps **Edit in browser**, **Pin**, and **Actions** visible.
**Actions** groups copying, note history, external-editor access, synthesis, and Trash.

Use **Copy code** in a code block’s header, the **#** button beside a heading
(**Copy section link**), and
**Copy note URL** for a clean note address without search context or a section
anchor. Each offers manual copying when the browser refuses clipboard access.
Ask AI and Daily Brief insights offer the same fallback for their Markdown.
The body appears before Ask AI, and **Skip to note** bypasses navigation and
metadata controls. Skip links are omitted from print output.

Explore further with the [Related Notes explorer](web-related-notes.md),
[Graph](web-server-security.md), or [Synthesis workbench](web-synthesis.md).

## Create and edit

Choose **New note**, enter a title and optional vault-relative folder, and select
**Create and write**. A blank folder uses the vault root. The new title-only note
opens in the browser editor; **Save** writes your text. **Capture** sends a quick
thought to the configured inbox instead.

Unfinished titles and folders are kept in this tab for this vault when browser
storage is available. If a creation response is lost, **Check creation** safely
reuses the original request, including after a reload; it never checks or retries
automatically. Confirmed creation clears those saved details. If browser storage
is unavailable, keep the form open until creation is confirmed.

On a note, choose **Edit in browser**, or **Open in external editor** on local
servers. The source editor opens with the writing surface visible on a narrow
screen. Its toolbar keeps **Save** nearby while scrolling; **Format** reveals formatting
commands and Escape closes them. Long frontmatter starts collapsed; **Show
frontmatter** expands it without changing the source.
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

## Adjust settings

Settings groups portable configuration by purpose and shows readable names and
current values. Group links jump directly to each section without discarding drafts.
Upload sizes display and edit in MiB; conversion preserves exact byte counts and
refuses values that would require rounding to a whole byte. Choose **Edit** beside a setting to reveal its controls; size and
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
established. Check the current note, values, inbox, or History before retrying;
the browser does not automatically resend an unanswered write. Settings and
Templates offer **Check History in a new tab**, preserving typed inputs.
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
changes point to checking the current values, inbox, or History before retrying.

Synchronization shows one summary and when the server checked it. Healthy
transport details start collapsed; pending work or problems open them automatically.
Expand **Synchronization details** for the exact check time and the separate saved,
packaged, acknowledged, and applied facts.

The shared **Sync:** line refreshes after confirmed writes and polls pending work for
up to one minute. It stops while the page is hidden and ignores superseded replies.

## Reference

- [Server commands](web-server-commands.md)
- [Rendering, attachments, options, and security](web-server-security.md)
- [Serving modes and capabilities](internals/serving-modes.md)
- [Contributor setup and browser verification](development.md)

## Daily Brief

Brief leads with the recommended notes, followed by capture and maintenance.
Each card shows its title, excerpt, and Open/Another actions. **Why this note?**
reveals its location and recommendation reasons; **Ask AI** expands the prompt.
Both start closed again when Another loads a replacement. Activity counts and
optional tool setup follow the recommendations.

## Daily Brief dogfood readout

Over two weeks, from local state only:

1. Count distinct visit days in the last 14 days from `.bnotes/brief.json` `visits`.
2. Compute average visits per week as `visit_days / 2`.
3. Compute open-day rate as `days_with_open / visit_days`, grouping `opens` by local date.
4. Success threshold: at least 4 visits per week and opens on at least 50% of visited days.
