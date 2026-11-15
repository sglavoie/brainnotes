const markup = `<dialog class="command-switcher" data-shortcuts-sheet aria-labelledby="shortcuts-title">
  <header><h2 id="shortcuts-title">Keyboard shortcuts</h2><button type="button" class="bn-button" autofocus>Close</button></header>
  <p>Ctrl/⌘ means Control on Windows/Linux or Command on Mac. Turn off Single-key shortcuts in Workspace. Typing fields, dialogs, the editor and Graph keep their own keys.</p>
  <h3>Move around</h3>
  <ul>
    <li><kbd>g</kbd> then <kbd>b</kbd> Brief, <kbd>l</kbd> Library, <kbd>t</kbd> Threads, <kbd>p</kbd> Pinned, <kbd>g</kbd> Graph — within one second.</li>
    <li><kbd>j</kbd> / <kbd>k</kbd> next / previous Library, Pinned or Threads row; <kbd>Enter</kbd> or <kbd>o</kbd> open; <kbd>x</kbd> toggle its checkbox.</li>
    <li><kbd>e</kbd> edit the note; <kbd>/</kbd> focus search; <kbd>Escape</kbd> return from search; <kbd>?</kbd> show this sheet.</li>
    <li>Ctrl/⌘ <kbd>K</kbd> search notes and actions; Ctrl/⌘ Shift Space capture; Ctrl/⌘ Enter submit capture or Ask AI.</li>
  </ul>
  <h3>Search and references</h3>
  <p>In the switcher: ↑ / ↓ choose, Enter open, Alt Enter keep beside, Escape close. On previewable note links: Alt ↓ opens a preview; Escape closes it. Escape also closes a focused side reference. In search suggestions: ↑ / ↓ choose and Enter open. Capture wikilinks and filter suggestions use ↑ / ↓, Enter and Escape. Control Space requests query-expression suggestions.</p>
  <h3>Graph and Pinned</h3>
  <p>On the Graph canvas: arrow keys browse notes, Home / End first / last, Enter / Space toggle neighbour labels, Escape clear selection. In its list: arrow keys choose notes or expand folders, then use Open note. On a Pinned drag handle: ↑ / ↓ reorder; Escape cancels a drag.</p>
  <h3>Editor</h3>
  <p>Ctrl/⌘ S save, F find, B bold, I italic, E inline code, Alt K link, Shift K wikilink, Shift 8 bullet list, Alt 1–6 headings. Ctrl/⌘ Z undo; Ctrl/⌘ Shift Z redo (Control Y also works on Windows/Linux). In completion suggestions: ↑ / ↓ choose, Enter accept, Escape dismiss. Escape leaves Focus view. Editor tabs use ← / →, Home / End.</p>
  <h3>Properties and reading</h3>
  <p>With a selected reading passage focused: Alt Enter writes a thought. In Properties rows: ↑ / ↓ move, Space selects, Enter edits. Escape closes the property editor, then clears selection. On a reading sidebar resize handle: ← / → adjust width, Shift moves farther. On a side-reference resize handle: ← / → adjust width, Home / End choose minimum / maximum.</p>
  <p>Escape closes dialogs and popovers. Tab / Shift Tab move between controls; Enter activates links and buttons, Space activates buttons and checkboxes. Shortcuts only act when the corresponding control is available.</p>
</dialog>
`;

export class ShortcutSheet {
  private readonly dialog: HTMLDialogElement;
  private origin: HTMLElement | null = null;

  constructor() {
    const template = document.createElement("template");
    template.innerHTML = markup;
    this.dialog = template.content.firstElementChild as HTMLDialogElement;
    this.dialog.querySelector("button")!.addEventListener("click", () => this.dialog.close());
    this.dialog.addEventListener("close", () => {
      if (!this.origin?.isConnected) return;
      const closed = this.origin.closest("details:not([open])");
      const target = closed?.querySelector<HTMLElement>(":scope > summary") || this.origin;
      target.focus();
    });
    document.body.append(this.dialog);
  }

  open(origin: HTMLElement | null): void {
    this.origin = origin;
    this.dialog.showModal();
  }
}
