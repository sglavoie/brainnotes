import { StateField, type EditorState, type Extension } from "@codemirror/state";
import { Decoration, EditorView, ViewPlugin, type DecorationSet } from "@codemirror/view";
import { sourcePresentation } from "./source-presentation";

const collectionRange = (state: EditorState): { from: number; to: number } | null => {
  const text = state.doc.toString();
  const start = "<!-- bn:thread:v1 -->", end = "<!-- bn:thread:end -->";
  const from = text.indexOf(start), stop = text.indexOf(end);
  if (from < 0 || stop < from || text.indexOf(start, from + 1) !== -1 || text.indexOf(end, stop + 1) !== -1) return null;
  return { from, to: stop + end.length };
};

const decorations = (state: EditorState, source: boolean): DecorationSet => {
  if (source) return Decoration.none;
  const range = collectionRange(state);
  if (!range || state.selection.ranges.some((selection) => selection.from <= range.to && selection.to >= range.from)) return Decoration.none;
  return Decoration.set([Decoration.replace({ block: true }).range(range.from, range.to)]);
};

// Keep all bytes in CodeMirror for undo/save/recovery. Source view and Find
// reveal collection markup; ordinary writing shows it in the reference column.
export const threadPresentation = (host: HTMLElement): Extension => {
  const panel = host.closest<HTMLElement>("[data-thread-editor]");
  if (!panel) return [];
  const field = StateField.define<{ source: boolean; value: DecorationSet }>({
    create(state) { const source = panel.dataset.writingSource === "true"; return { source, value: decorations(state, source) }; },
    update(value, transaction) {
      let source = value.source;
      for (const effect of transaction.effects) if (effect.is(sourcePresentation)) source = effect.value;
      if (!transaction.docChanged && !transaction.selection && source === value.source) return value;
      return { source, value: decorations(transaction.state, source) };
    },
    provide: (f) => EditorView.decorations.from(f, (value) => value.value),
  });
  return [field, ViewPlugin.fromClass(class {
    readonly change = (): void => this.view.dispatch({ effects: sourcePresentation.of(panel?.dataset.writingSource === "true") });
    constructor(readonly view: EditorView) { panel?.addEventListener("bn:writing-presentation", this.change); }
    destroy(): void { panel?.removeEventListener("bn:writing-presentation", this.change); }
  })];
};
