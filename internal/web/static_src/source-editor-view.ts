import {
  autocompletion,
  closeBrackets,
  closeBracketsKeymap,
  completionKeymap,
  type CompletionContext,
  type CompletionResult,
} from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { markdown, markdownLanguage } from "@codemirror/lang-markdown";
import { bracketMatching, defaultHighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { Annotation, EditorState } from "@codemirror/state";
import { EditorView, highlightSpecialChars, keymap } from "@codemirror/view";

import { wikilinkContextAt, type WikilinkOption } from "./source-wikilink";
import type { SourceFormatCommand, SourceFormatOptions } from "./source-format";
import type { SourceSurface, SourceSurfaceFactory } from "./source-surface";

// The CodeMirror adapter, and the only module in the editor that imports it.
//
// Everything here is either a CodeMirror API call or a translation between one
// and the surface interface. That containment is deliberate: CodeMirror
// measures a real layout, so none of this can be asserted under jsdom, and a
// module that mixed editor behaviour into it would drag that behaviour into the
// browser suite with it. What this file is worth testing for is covered by
// internal/web/e2e/editor.spec.js in a real browser.

export type SourceViewOptions = {
  onSave(): void;
  onCommand(command: SourceFormatCommand, options?: SourceFormatOptions): void;
  // completeWikilink answers the notes matching `query`, or null when the
  // completion should be abandoned. It is injected rather than imported so the
  // adapter stays free of the API layer.
  completeWikilink?(query: string): Promise<WikilinkOption[]>;
};

// programmaticChange marks a document replacement the controller made — loading
// the note, or discarding an edit in favour of the vault's version. The change
// listener ignores it, because treating a load as a user edit would mark a
// freshly-opened note dirty and arm the unsaved-changes prompt.
const programmaticChange = Annotation.define<boolean>();

export const createSourceSurfaceFactory = (
  options: SourceViewOptions
): SourceSurfaceFactory => {
  return (host: HTMLElement, initialValue: string): SourceSurface => {
    const listeners: (() => void)[] = [];

    const view = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: initialValue,
        extensions: [
          history(),
          highlightSpecialChars(),
          bracketMatching(),
          closeBrackets(),
          // Markdown prose has no useful line length, so it wraps rather than
          // scrolling sideways — a note written in one paragraph per line is
          // otherwise unreadable in a pane half a window wide.
          EditorView.lineWrapping,
          markdown({ base: markdownLanguage }),
          syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
          autocompletion({ override: [wikilinkCompletionSource(options)] }),
          // Tab is deliberately not bound. CodeMirror's own documentation
          // warns that capturing it traps keyboard users in the editor, and a
          // Markdown note has no indentation this page needs to offer.
          keymap.of([
            ...closeBracketsKeymap,
            ...completionKeymap,
            ...historyKeymap,
            ...formattingKeymap(options),
            ...defaultKeymap,
          ]),
          EditorView.updateListener.of((update) => {
            if (!update.docChanged) {
              return;
            }
            if (update.transactions.some((tr) => tr.annotation(programmaticChange))) {
              return;
            }
            for (const listener of listeners) {
              listener();
            }
          }),
        ],
      }),
    });

    return {
      getValue: () => view.state.doc.toString(),
      setValue(value: string): void {
        view.dispatch({
          changes: { from: 0, to: view.state.doc.length, insert: value },
          annotations: programmaticChange.of(true),
        });
      },
      getSelection() {
        const range = view.state.selection.main;
        return { from: range.from, to: range.to };
      },
      applyEdit(edit): void {
        view.dispatch({
          changes: { from: edit.from, to: edit.to, insert: edit.insert },
          selection: { anchor: edit.selectionFrom, head: edit.selectionTo },
          scrollIntoView: true,
        });
      },
      focus: () => view.focus(),
      onChange(listener: () => void): void {
        listeners.push(listener);
      },
      destroy: () => view.destroy(),
    };
  };
};

// formattingKeymap routes the shortcuts back through the controller, so the
// toolbar button and the key combination run the same transform. Each binding
// returns true to stop the key reaching the browser: Mod-s especially, which
// otherwise offers to save the HTML page.
const formattingKeymap = (options: SourceViewOptions) => {
  const command = (name: SourceFormatCommand, format?: SourceFormatOptions) => () => {
    options.onCommand(name, format);
    return true;
  };
  const headings = [1, 2, 3, 4, 5, 6].map((level) => ({
    key: `Mod-Alt-${level}`,
    run: command("heading", { headingLevel: level }),
  }));
  return [
    {
      key: "Mod-s",
      run: () => {
        options.onSave();
        return true;
      },
    },
    { key: "Mod-b", run: command("bold") },
    { key: "Mod-i", run: command("italic") },
    { key: "Mod-e", run: command("code") },
    { key: "Mod-k", run: command("link") },
    { key: "Mod-Shift-k", run: command("wikilink") },
    { key: "Mod-Shift-8", run: command("bullet") },
    ...headings,
  ];
};

// wikilinkCompletionSource offers vault notes after a `[[`.
//
// The context decision is source-wikilink.ts's, over a plain string and offset,
// so it is tested in Vitest; what is left here is the CodeMirror shape around
// it. `validFor` keeps the list alive while the query only grows with ordinary
// characters, which is what stops every keystroke becoming a search request.
const wikilinkCompletionSource =
  (options: SourceViewOptions) =>
  async (context: CompletionContext): Promise<CompletionResult | null> => {
    if (!options.completeWikilink) {
      return null;
    }
    const link = wikilinkContextAt(context.state.doc.toString(), context.pos);
    if (!link) {
      return null;
    }
    const matches = await options.completeWikilink(link.query);
    if (matches.length === 0) {
      return null;
    }
    return {
      from: link.from,
      validFor: /^[^[\]\n|#]*$/,
      options: matches.map((match) => ({
        label: match.label,
        detail: match.detail || undefined,
        type: "text",
        apply: applyWikilink(match.label),
      })),
    };
  };

// applyWikilink closes the brackets unless they are already there, so accepting
// a completion leaves a finished link rather than one the user has to close by
// hand — and does not double the closer when closeBrackets already inserted it.
const applyWikilink =
  (label: string) =>
  (view: EditorView, _completion: unknown, from: number, to: number): void => {
    const closed = view.state.doc.sliceString(to, to + 2) === "]]";
    const insert = closed ? label : `${label}]]`;
    view.dispatch({
      changes: { from, to, insert },
      selection: { anchor: from + insert.length + (closed ? 2 : 0) },
    });
  };
