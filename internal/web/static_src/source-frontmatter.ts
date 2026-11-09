import { codeFolding, foldEffect, foldedRanges } from "@codemirror/language";
import { EditorState } from "@codemirror/state";
import { EditorView, ViewPlugin, type ViewUpdate } from "@codemirror/view";

const headerRange = (state: EditorState): { from: number; to: number; body: number } | null => {
  const header = /^(?:\uFEFF)?---[ \t]*\r?\n[\s\S]*?\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|$)/.exec(state.doc.toString());
  return header ? { from: 0, to: header[0].trimEnd().length, body: header[0].length } : null;
};

// Folding is presentation only. The full source remains in the editor state,
// including for draft recovery, Find, undo, preview and Save.
export const initialSourceState = (state: EditorState): EditorState => {
  const range = headerRange(state);
  return range ? state.update({ effects: foldEffect.of(range), selection: { anchor: range.body } }).state : state;
};

const frontmatterPlaceholder = (_view: EditorView, unfold: (event: Event) => void): HTMLElement => {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "bn-button source-editor-frontmatter";
  button.textContent = "Show frontmatter";
  button.setAttribute("aria-label", "Show frontmatter in source editor");
  button.setAttribute("aria-expanded", "false");
  button.addEventListener("click", unfold);
  return button;
};

class FrontmatterControl {
  private readonly button = document.createElement("button");
  private range: ReturnType<typeof headerRange>;

  constructor(private readonly view: EditorView) {
    this.range = headerRange(view.state);
    this.button.type = "button";
    this.button.className = "bn-button source-editor-frontmatter source-frontmatter-hide";
    this.button.textContent = "Hide frontmatter";
    this.button.setAttribute("aria-label", "Hide frontmatter in source editor");
    this.button.setAttribute("aria-expanded", "true");
    this.button.addEventListener("click", () => this.fold());
    view.dom.prepend(this.button);
    this.sync();
  }

  update(update: ViewUpdate): void {
    if (update.docChanged) this.range = headerRange(update.state);
    if (update.docChanged || foldedRanges(update.startState) !== foldedRanges(update.state)) this.sync();
  }

  destroy(): void { this.button.remove(); }

  private sync(): void {
    let folded = false;
    if (this.range) foldedRanges(this.view.state).between(0, this.range.to, (from) => { if (from === 0) folded = true; });
    this.button.hidden = !this.range || folded;
  }

  private fold(): void {
    if (!this.range) return;
    const selection = this.view.state.selection.main;
    this.view.dispatch({ effects: foldEffect.of(this.range),
      ...(selection.from < this.range.to ? { selection: { anchor: this.range.body } } : {}) });
    this.view.dom.querySelector<HTMLButtonElement>(".cm-content .source-editor-frontmatter")?.focus({ preventScroll: true });
  }
}

export const frontmatterFolding = [codeFolding({ placeholderDOM: frontmatterPlaceholder }), ViewPlugin.fromClass(FrontmatterControl)];
