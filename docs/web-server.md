# Local web server

Note pages offer **Edit in browser** and, on local servers, **Open in external
editor**. **More actions** contains Synthesize and Trash; expand it to use them.

The folder fields in **New note**, the editor's **Move**, and Library's bulk
**Move** suggest existing note folders as you type. You can still enter a new
path; suggestions are optional and manual entry works if they cannot load.

When Library has no matches for text search, **Create a note titled…** opens
New note with that text filled in. Review the title and folder, then choose
**Create and write**. Opening the form does not create anything. This action is
unavailable on read-only replicas.

Three of the larger web surfaces have their own pages: the
[Related Notes explorer](web-related-notes.md), the
[Synthesis workbench](web-synthesis.md), and
[Health, Daily Fixes, and properties](web-health.md).

On the **Edit source** page, change the **Note title** field above the formatting
toolbar and click **Rename**. This renames the file in its current folder and
updates incoming wikilinks. If the editor has unsaved text, save it first using
the prompt or cancel the rename. The **Save** button saves the source text;
**Rename** applies the title change.

Editor management actions show progress and results beside their controls.
While an action or its **Save first** step is running, other management actions
are disabled to prevent repeated submissions. Canceling during **Save first**
cancels the follow-up action; the save itself can still finish.
If a response is lost or cannot confirm success, the editor keeps the action's
inputs and asks you to check the inbox, note, or History before retrying. It does
not retry automatically or navigate to an unconfirmed rename/move destination.

**Find** opens search and replacement within the source editor, including text
outside the visible portion of long notes. Ctrl/⌘ + F opens it while editing;
Enter and Shift + Enter in its Find field move between matches. Escape closes
the panel and returns to editing. Replacements can be undone and stay in the
draft until you click **Save**. On narrow screens, Find switches from Preview to
Edit automatically.

If the editor preview fails, **Retry preview** renders the current draft without
saving or changing the source. Any previous preview is labeled outdated until
rendering succeeds. Retrying disables the button while rendering and returns
keyboard focus to the preview pane when the button disappears, unless you moved
to another control while waiting.

If creating a missing note loses its response or receives an unreadable success
response, the browser says it couldn't confirm creation. **Refresh to check**
reloads the current page so you can see whether the link is still broken before
retrying; refreshing does not send another create request.

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
**Edit in browser** carries it into the editor too: use the note title link to return
to the note, then **Back to results** to resume browsing. Reloading the editor,
renaming, or moving the note preserves this context.

If header search fails, **Retry search** repeats the current query and filters
without retyping. Dismissing search also dismisses the retry action.
Library's **Copy search link** copies the current text search, filters, query
expression, sort order, and limit, with a manual-copy fallback if needed. Unlike
saved views, it includes text search and facet filters. The link omits the
result-position anchor. Library's Sort menu also offers **Oldest modified first**
and **Vault path**.
When Library has no matches, **Remove search text** clears only the text query,
preserving filters, the expression, sort order, and result limit. **Reset search**
still clears the whole search.
If the filter picker cannot load, **Retry filters** reloads its choices without
changing the search, selected filters, ordering, or result limit. The error stays
visible until loading succeeds, and selected filters can still be removed.

On screens up to 960 pixels wide, **Metadata** and **Contents** start collapsed
and expand independently. Desktop sidebars keep their existing layout and
visibility preference; expanding mobile metadata does not change that preference.

## Reference sections

- [Command reference](web-server-commands.md)
- [The query bar and saved views](web-server-query-bar.md)
- [Note actions, Trash, rendering, filters and graph, options, assets, and security](web-server-security.md)

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
- [Serving modes and capabilities](internals/serving-modes.md) — the internal contract behind the capabilities table in [options, assets, and security](web-server-security.md).
