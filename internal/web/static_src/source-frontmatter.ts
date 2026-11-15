import { codeFolding, foldEffect, foldedRanges, unfoldEffect } from "@codemirror/language";
import { EditorState } from "@codemirror/state";
import { EditorView, ViewPlugin, type ViewUpdate } from "@codemirror/view";
import { repeatedTitle } from "./source-presentation";

const headerRange = (state: EditorState): { from: number; to: number; body: number } | null => {
  const header = /^(?:\uFEFF)?---[ \t]*\r?\n[\s\S]*?\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|$)/.exec(state.doc.toString());
  return header ? { from: 0, to: header[0].trimEnd().length, body: header[0].length } : null;
};

// Folding is presentation only. The full source remains in the editor state,
// including for draft recovery, Find, undo, preview and Save.
export const initialSourceState = (state: EditorState, title = ""): EditorState => {
  const range = headerRange(state);
  const heading = repeatedTitle(state, title);
  return state.update({ effects: range ? foldEffect.of(range) : [], selection: { anchor: heading?.next ?? range?.body ?? 0 } }).state;
};

const frontmatterPlaceholder = (): HTMLElement => {
  const placeholder = document.createElement("span");
  placeholder.className = "source-frontmatter-placeholder";
  placeholder.setAttribute("aria-hidden", "true");
  return placeholder;
};

class FrontmatterControl {
  private readonly button: HTMLButtonElement | null;
  private range: ReturnType<typeof headerRange>;
  private folded = false;
  private readonly toggle = (): void => {
    if (!this.range) return;
    const selection = this.view.state.selection.main;
    this.view.dispatch({ effects: (this.folded ? unfoldEffect : foldEffect).of(this.range),
      ...(!this.folded && selection.from < this.range.to ? { selection: { anchor: this.range.body } } : {}) });
    this.button?.focus({ preventScroll: true });
  };

  constructor(private readonly view: EditorView) {
    this.range = headerRange(view.state);
    this.button = view.dom.closest("[data-source-editor]")?.querySelector("[data-source-frontmatter]") ?? null;
    this.button?.addEventListener("click", this.toggle);
    this.sync();
  }

  update(update: ViewUpdate): void {
    if (update.docChanged) {
      // Edits after a closed header cannot change its range. In particular,
      // ordinary prose typing must not flatten the entire note to rescan it.
      let headerChanged = this.range === null;
      update.changes.iterChangedRanges((from) => {
        if (this.range && from <= this.range.body) headerChanged = true;
      });
      if (headerChanged) this.range = headerRange(update.state);
    }
    if (update.docChanged || foldedRanges(update.startState) !== foldedRanges(update.state)) this.sync();
  }

  destroy(): void { this.button?.removeEventListener("click", this.toggle); }

  private sync(): void {
    this.folded = false;
    if (this.range) foldedRanges(this.view.state).between(0, this.range.to, (from) => { if (from === 0) this.folded = true; });
    if (!this.button) return;
    this.button.hidden = !this.range;
    this.button.textContent = `${this.folded ? "Show" : "Hide"} frontmatter`;
    this.button.setAttribute("aria-label", `${this.button.textContent} in source editor`);
    this.button.setAttribute("aria-expanded", String(!this.folded));
  }
}

export const frontmatterFolding = [codeFolding({ placeholderDOM: frontmatterPlaceholder }), ViewPlugin.fromClass(FrontmatterControl)];
