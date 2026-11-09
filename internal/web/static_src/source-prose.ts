import { syntaxTree } from "@codemirror/language";
import { StateEffect, StateField } from "@codemirror/state";
import { Decoration, EditorView, ViewPlugin, type DecorationSet, type ViewUpdate } from "@codemirror/view";

// Decorations only: Markdown remains selectable, editable and byte-for-byte
// intact. Inspect just visible syntax, preserving long-note typing performance.
const syntaxMarks = (view: EditorView): DecorationSet => {
  const ranges: ReturnType<Decoration["range"]>[] = [];
  for (const { from, to } of view.visibleRanges) {
    syntaxTree(view.state).iterate({ from, to, enter(node) {
      if (["FencedCode", "CodeBlock", "InlineCode"].includes(node.name)) return false;
      if (!["HeaderMark", "EmphasisMark", "LinkMark", "QuoteMark", "ListMark"].includes(node.name)) return;
      const line = view.state.doc.lineAt(node.from);
      if (view.state.selection.ranges.some((range) => range.from <= line.to && range.to >= line.from)) return;
      if (node.to > node.from) ranges.push(Decoration.mark({ class: "cm-quiet-syntax" }).range(node.from, node.to));
    } });
  }
  return Decoration.set(ranges, true);
};

export const proseSyntax = ViewPlugin.fromClass(class {
  decorations: DecorationSet;
  constructor(view: EditorView) { this.decorations = syntaxMarks(view); }
  update(update: ViewUpdate): void {
    if (update.docChanged || update.selectionSet || update.viewportChanged || syntaxTree(update.startState) !== syntaxTree(update.state)) this.decorations = syntaxMarks(update.view);
  }
}, { decorations: (plugin) => plugin.decorations });

export const revealPassage = StateEffect.define<number | null>();
export const restoredPassage = StateField.define<DecorationSet>({
  create: () => Decoration.none,
  update(value, transaction) {
    if (transaction.docChanged || transaction.selection) value = Decoration.none;
    for (const effect of transaction.effects) if (effect.is(revealPassage)) {
      value = effect.value === null ? Decoration.none : Decoration.set([
        Decoration.line({ class: "resume-passage" }).range(transaction.state.doc.lineAt(effect.value).from),
      ]);
    }
    return value;
  },
  provide: (field) => EditorView.decorations.from(field),
});
