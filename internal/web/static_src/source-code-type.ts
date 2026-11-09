import { syntaxTree } from "@codemirror/language";
import { Decoration, EditorView, ViewPlugin, type DecorationSet, type ViewUpdate } from "@codemirror/view";

const codeMarks = (view: EditorView): DecorationSet => {
  const ranges: ReturnType<Decoration["range"]>[] = [];
  for (const { from, to } of view.visibleRanges) {
    syntaxTree(view.state).iterate({ from, to, enter(node) {
      if (!["FencedCode", "CodeBlock", "InlineCode"].includes(node.name)) return;
      ranges.push(Decoration.mark({ class: "cm-code" }).range(Math.max(from, node.from), Math.min(to, node.to)));
      return false;
    } });
  }
  return Decoration.set(ranges, true);
};

export const codeTypography = ViewPlugin.fromClass(class {
  decorations: DecorationSet;
  constructor(view: EditorView) { this.decorations = codeMarks(view); }
  update(update: ViewUpdate): void {
    if (update.docChanged || update.viewportChanged || syntaxTree(update.startState) !== syntaxTree(update.state)) this.decorations = codeMarks(update.view);
  }
}, { decorations: (plugin) => plugin.decorations });
