import {
  autocompletion,
  closeBrackets,
  closeBracketsKeymap,
  completionKeymap,
  completionStatus,
  type CompletionContext,
  type CompletionResult,
} from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { markdown, markdownLanguage } from "@codemirror/lang-markdown";
import { bracketMatching, codeFolding, foldEffect, defaultHighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { Annotation, EditorState } from "@codemirror/state";
import { openSearchPanel, search, searchKeymap } from "@codemirror/search";
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
    // One per editor, because it remembers what this vault answered.
    const refresh: CompletionRefresh = { noMatchPrefix: null };

    const view = new EditorView({
      parent: host,
      state: initialSourceState(EditorState.create({
        doc: initialValue,
        extensions: [
          history(),
          codeFolding({ placeholderDOM: frontmatterPlaceholder }),
          search({ top: true }),
          highlightSpecialChars(),
          bracketMatching(),
          closeBrackets(),
          // Markdown prose has no useful line length, so it wraps rather than
          // scrolling sideways — a note written in one paragraph per line is
          // otherwise unreadable in a pane half a window wide.
          EditorView.lineWrapping,
          EditorView.contentAttributes.of({ "aria-labelledby": "source-editor-heading" }),
          markdown({ base: markdownLanguage }),
          syntaxHighlighting(defaultHighlightStyle, { fallback: true }),
          autocompletion({ override: [wikilinkCompletionSource(options, refresh)] }),
          // Tab is deliberately not bound. CodeMirror's own documentation
          // warns that capturing it traps keyboard users in the editor, and a
          // Markdown note has no indentation this page needs to offer.
          keymap.of([
            ...closeBracketsKeymap,
            ...refreshingCompletionKeymap(refresh),
            ...completionKeymap,
            ...historyKeymap,
            ...formattingKeymap(options),
            ...searchKeymap,
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
      })),
    });

    const disposePosition = bindSourcePosition(view, host);
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
      openSearch: () => { openSearchPanel(view); },
      onChange(listener: () => void): void {
        listeners.push(listener);
      },
      destroy: () => { disposePosition(); view.destroy(); },
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

// CompletionRefresh is what the completion source tells the keymap below.
//
// noMatchPrefix is the shortest query this vault has answered with nothing. The
// server matches a note when its folded path or body *contains* the query
// (internal/web/match.go), so a longer query can only match fewer notes: once
// `[[Some Note` has come back empty, every query extending it is empty too, and
// no dialog is coming.
type CompletionRefresh = { noMatchPrefix: string | null };

// refreshingCompletionKeymap holds the three keys the completion dialog owns for
// as long as the caret is in an unclosed `[[` and an answer that could fill the
// dialog has not arrived. It is the price of `filter: false` below, paid here
// rather than passed on to whoever is typing.
//
// Without `validFor` the result is invalidated on every keystroke, and
// CodeMirror keeps the previous list on screen while the new query runs but
// marks it disabled (CompletionDialog.build → setDisabled). A disabled dialog
// looks like a live one — same options, same DOM, only a
// `cm-tooltip-autocomplete-disabled` class the public API does not expose — yet
// acceptCompletion and moveCompletionSelection both decline, which in a keymap
// means the key falls through: Enter to insertNewlineAndIndent, the arrows to
// cursorLineUp/Down. So typing `[[Fieldwo` and pressing Enter without pausing
// would put a newline inside the link and leave the completion behind, at the one
// moment somebody is most likely to press it.
//
// Declining the key loses a keystroke; the alternative spends it on the opposite
// of what it meant. The window is activateOnTypingDelay (100 ms) plus one
// in-process query.
//
// Position in this array buys one thing only: running ahead of `defaultKeymap`,
// so Enter never reaches insertNewlineAndIndent. It does *not* order this
// against `completionKeymap` — `autocompletion()` installs its own copy of those
// bindings at Prec.highest, so acceptCompletion runs before all of this and
// declines exactly when the dialog is disabled, which is the gap these fill. The
// spread below is therefore a duplicate; it is kept so the bindings survive
// somebody passing `autocompletion({ defaultKeymap: false })`, and CodeMirror
// dedupes by function identity.
const refreshingCompletionKeymap = (refresh: CompletionRefresh) => {
  const run = declineWhileRefreshing(refresh);
  return [
    { key: "Enter", run },
    { key: "ArrowDown", run },
    { key: "ArrowUp", run },
  ];
};

// declineWhileRefreshing is the narrowest predicate the public API can express.
// It cannot ask whether a dialog is on screen — currentCompletions() returns []
// for precisely the disabled dialog this is about — so it asks whether an answer
// that could produce one is still outstanding.
//
// The two exclusions are the cases where consuming the key would take it from
// somebody who meant it for something else. A composition commit is reported as
// Enter by some IMEs, and cancelling it loses the text being composed rather
// than a newline. A query extending one already known empty has no dialog coming
// at all, so waiting for it would eat an Enter for nothing — which is the
// ordinary move of writing a link before the note exists.
const declineWhileRefreshing =
  (refresh: CompletionRefresh) =>
  (view: EditorView): boolean => {
    if (view.composing || completionStatus(view.state) !== "pending") {
      return false;
    }
    const { main } = view.state.selection;
    if (!main.empty) {
      return false;
    }
    const link = wikilinkContextAt(view.state.doc.toString(), main.head);
    return link !== null && !knownEmpty(refresh, link.query);
  };

const knownEmpty = (refresh: CompletionRefresh, query: string): boolean =>
  refresh.noMatchPrefix !== null && query.startsWith(refresh.noMatchPrefix);

// recordEmptyAnswer keeps the shortest query this vault has answered with
// nothing. Shortest wins because backspacing to a shorter query that is also
// empty is a stronger fact, and because a query that *matches* clears the field
// outright.
//
// A query with nothing in it is excluded, and that exclusion is the whole of
// this function. `/api/search?q=` returns no rows without looking at a single
// note — buildSearchResponse short-circuits on an empty *folded* query, which is
// why whitespace counts as nothing here too — so its zero is the API declining
// to scan rather than the vault answering. Recording it would be recording a
// prefix of everything, turning knownEmpty into a constant true and switching
// the guard off for the rest of this editor's life. Resting the caret after `[[`
// for longer than the 100 ms activation delay is the ordinary way to reach it,
// which would have left the guard silently absent for exactly the first link of
// every deliberate one.
//
// So the field is either null or a query the vault genuinely answered, and
// knownEmpty can never be vacuously true.
const recordEmptyAnswer = (refresh: CompletionRefresh, query: string): void => {
  if (query.trim() === "") {
    return;
  }
  if (refresh.noMatchPrefix === null || query.length < refresh.noMatchPrefix.length) {
    refresh.noMatchPrefix = query;
  }
};

// wikilinkCompletionSource offers vault notes after a `[[`.
//
// The context decision is source-wikilink.ts's, over a plain string and offset,
// so it is tested in Vitest; what is left here is the CodeMirror shape around
// it.
//
// `filter: false` and the absence of `validFor` are one decision: the vault's
// search is the only thing that can rank these, so every keystroke asks it
// again and the answer is shown as given. Both defaults are wrong here for the
// same reason — they re-match what was typed against the option *label*, and
// the label is the note's path (wikilinkTargetForNote in
// internal/web/note_model.go) while the search matches its body too. A note at
// Notes/Fieldwork.md whose body says "Cartographer" is returned by the server
// for `[[Cartograph` and then dropped by the local filter, so the completion
// would only ever offer notes somebody could already have typed the path of.
//
// The cost is a query per keystroke, though not a request per keystroke:
// CodeMirror keeps one in flight per source and restarts it when the answer is
// stale, behind a 100 ms typing delay. `/api/search` is an in-process query over
// an already-loaded vault, so that is the cheaper side of the trade. The other
// cost is the window the re-query opens, which refreshingCompletionKeymap above
// exists to bound.
const wikilinkCompletionSource =
  (options: SourceViewOptions, refresh: CompletionRefresh) =>
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
      recordEmptyAnswer(refresh, link.query);
      return null;
    }
    refresh.noMatchPrefix = null;
    return {
      from: link.from,
      filter: false,
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

// Long metadata stays editable, but does not push the prose out of the first
// viewport. Short headers with long values wrap into many lines on phones,
// so bound both lines and characters. This never changes the source or selection.
const initialSourceState = (state: EditorState): EditorState => {
  const header = /^---\r?\n[\s\S]*?\r?\n---(?:\r?\n|$)/.exec(state.doc.toString());
  if (!header || (header[0].split("\n").length < 9 && header[0].length < 240)) return state;
  return state.update({ effects: foldEffect.of({ from: 0, to: header[0].trimEnd().length }) }).state;
};

const frontmatterPlaceholder = (_view: EditorView, unfold: (event: Event) => void): HTMLElement => {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "bn-button source-editor-frontmatter";
  button.textContent = "Show frontmatter";
  button.setAttribute("aria-label", "Show frontmatter in source editor");
  button.addEventListener("click", unfold);
  return button;
};
import { bindSourcePosition } from "./source-position";
