import { EditorView } from "@codemirror/view";

export const bodyStartLine = (text: string): number => {
  const header = /^(?:\uFEFF)?---[ \t]*\r?\n[\s\S]*?\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|$)/.exec(text);
  return header ? header[0].split("\n").length : 1;
};

type Passage = { line: number; offset: number };
const blocks = (preview: HTMLElement): HTMLElement[] => Array.from(preview.querySelectorAll("[data-source-line]"));

const previewPassage = (preview: HTMLElement): Passage | null => {
  const top = preview.getBoundingClientRect().top;
  const items = blocks(preview);
  const block = [...items].reverse().find((item) => item.getBoundingClientRect().top <= top + 8) || items[0];
  return block ? { line: Number(block.dataset.sourceLine), offset: block.getBoundingClientRect().top - top } : null;
};

const revealPreview = (preview: HTMLElement, passage: Passage): void => {
  const items = blocks(preview);
  const block = [...items].reverse().find((item) => Number(item.dataset.sourceLine) <= passage.line) || items[0];
  if (block) preview.scrollTop += block.getBoundingClientRect().top - preview.getBoundingClientRect().top - passage.offset;
};

const initialLine = (): number | null => {
  try {
    const position = JSON.parse(new URLSearchParams(location.search).get("read_at") || "null");
    return Number.isSafeInteger(position?.sourceLine) && position.sourceLine > 0 ? position.sourceLine : null;
  } catch { return null; }
};

// This is the geometry seam: all source offsets come from CodeMirror, all
// rendered locations from the shared Markdown renderer. It never changes text.
export const bindSourcePosition = (view: EditorView, host: HTMLElement): (() => void) => {
  const panel = host.closest<HTMLElement>("[data-source-editor]");
  const preview = panel?.querySelector<HTMLElement>("[data-source-preview]");
  const panes = panel?.querySelector<HTMLElement>("[data-source-active]");
  if (!preview || !panes) return () => {};
  let active = panes.dataset.sourceActive;
  let passage: Passage | null = null;
  let editPassage: Passage = { line: initialLine() || 1, offset: 0 };
  let frame = 0;
  let source = view.state.doc;
  let bodyStart = bodyStartLine(source.toString());
  const bodyLine = (): number => {
    if (source !== view.state.doc) {
      source = view.state.doc;
      bodyStart = bodyStartLine(source.toString());
    }
    return bodyStart;
  };
  const editorPassage = (): Passage => {
    const block = view.lineBlockAtHeight(view.scrollDOM.scrollTop);
    return { line: Math.max(1, view.state.doc.lineAt(block.from).number - bodyLine() + 1),
      offset: block.top - view.scrollDOM.scrollTop };
  };
  const revealEditor = (position: Passage): void => {
    const line = Math.max(1, Math.min(view.state.doc.lines, position.line + bodyLine() - 1));
    view.dispatch({ effects: EditorView.scrollIntoView(view.state.doc.line(line).from, { y: "start", yMargin: Math.max(0, position.offset) }) });
  };
  const remember = (): void => { if (preview.clientHeight) passage = previewPassage(preview); };
  const rememberEditor = (): void => {
    if (view.scrollDOM.clientHeight) editPassage = editorPassage();
  };
  preview.addEventListener("scroll", remember);
  view.scrollDOM.addEventListener("scroll", rememberEditor);
  const observer = new MutationObserver(() => {
    const next = panes.dataset.sourceActive;
    if (next !== active) {
      if (next === "preview") passage = editPassage;
      else if (passage) revealEditor(passage);
      active = next;
    }
    if (active === "preview" && passage) {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => { if (passage) revealPreview(preview, passage); });
    }
  });
  observer.observe(panes, { attributes: true, attributeFilter: ["data-source-active"] });
  observer.observe(preview, { childList: true });
  const line = initialLine();
  if (line) {
    passage = { line, offset: 0 };
    const target = view.state.doc.line(Math.min(view.state.doc.lines, line + bodyLine() - 1));
    view.dispatch({ selection: { anchor: target.from } });
    revealEditor(passage);
  }
  return () => {
    observer.disconnect(); cancelAnimationFrame(frame);
    preview.removeEventListener("scroll", remember);
    view.scrollDOM.removeEventListener("scroll", rememberEditor);
  };
};
