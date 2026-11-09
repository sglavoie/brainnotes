import { syntaxTree } from '@codemirror/language';
import { Decoration, type EditorView } from '@codemirror/view';

// Blank prose lines separate paragraphs, but needn't look like empty paragraphs.
// Keep active lines and code untouched so editing never loses its caret space.
export const proseSpacing = (view: EditorView, from: number, to: number): ReturnType<Decoration['range']>[] => {
  const marks: ReturnType<Decoration['range']>[] = [];
  for (let number = view.state.doc.lineAt(from).number; number <= view.state.doc.lineAt(to).number; number++) {
    const line = view.state.doc.line(number);
    if (line.text.trim() || view.state.selection.ranges.some((range) => range.from <= line.to && range.to >= line.from)) continue;
    let node = syntaxTree(view.state).resolveInner(line.from, 1);
    let code = false;
    for (; node.parent; node = node.parent) if (node.name === 'FencedCode' || node.name === 'CodeBlock') code = true;
    if (!code) marks.push(Decoration.line({ class: 'cm-prose-gap' }).range(line.from));
  }
  return marks;
};
