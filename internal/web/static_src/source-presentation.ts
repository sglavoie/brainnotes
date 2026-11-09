import { StateEffect, StateField, type EditorState, type Extension } from '@codemirror/state';
import { Decoration, EditorView, ViewPlugin, type DecorationSet } from '@codemirror/view';

export const sourcePresentation = StateEffect.define<boolean>();

export const repeatedTitle = (state: EditorState, title: string): { from: number; to: number; next: number } | null => {
  // Presentation work stays bounded even for a very large source document.
  const opening = state.doc.sliceString(0, Math.min(state.doc.length, 32768));
  const header = /^(?:\uFEFF)?---[ \t]*\r?\n[\s\S]*?\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|$)/.exec(opening);
  const start = header?.[0].length || 0;
  const heading = /^\s*# ([^\n]+)(?:\n|$)/.exec(opening.slice(start));
  if (!heading || heading[1].trim() !== title.trim()) return null;
  const from = start + heading[0].indexOf('#');
  const to = state.doc.lineAt(from).to;
  const following = /^\s*/.exec(opening.slice(to))?.[0].length || 0;
  return { from, to, next: Math.min(state.doc.length, to + following) };
};

const titleDecoration = (state: EditorState, title: string, source: boolean): DecorationSet => {
  if (source) return Decoration.none;
  const range = repeatedTitle(state, title);
  if (!range || state.selection.ranges.some((selection) => selection.from <= range.to && selection.to >= range.from)) return Decoration.none;
  return Decoration.set([Decoration.replace({ block: true }).range(range.from, range.to)]);
};

// A direct StateField decoration may replace a block; a viewport plugin must
// never do that. Selection/Find reveals it, while source bytes remain intact.
export const titlePresentation = (host: HTMLElement): Extension => {
  const panel = host.closest<HTMLElement>('[data-source-editor]');
  const title = panel?.querySelector('[data-source-note-return]')?.textContent || '';
  const state = StateField.define<{ source: boolean; decorations: DecorationSet }>({
    create(editor) {
      const source = panel?.dataset.writingSource === 'true';
      return { source, decorations: titleDecoration(editor, title, source) };
    },
    update(value, transaction) {
      let source = value.source;
      for (const effect of transaction.effects) if (effect.is(sourcePresentation)) source = effect.value;
      if (!transaction.docChanged && !transaction.selection && source === value.source) return value;
      return { source, decorations: titleDecoration(transaction.state, title, source) };
    },
    provide: (field) => EditorView.decorations.from(field, (value) => value.decorations),
  });
  return [state, ViewPlugin.fromClass(class {
    readonly change = (): void => this.view.dispatch({ effects: sourcePresentation.of(panel?.dataset.writingSource === 'true') });
    constructor(readonly view: EditorView) { panel?.addEventListener('bn:writing-presentation', this.change); }
    destroy(): void { panel?.removeEventListener('bn:writing-presentation', this.change); }
  })];
};
