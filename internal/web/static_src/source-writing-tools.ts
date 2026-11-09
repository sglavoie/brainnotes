import { Compartment, type Extension, type Text } from "@codemirror/state";
import { EditorView, ViewPlugin, type ViewUpdate } from "@codemirror/view";
import { countSourceWords, sourceBodyStart, wordCountLabel } from "./source-word-count";

const spellcheckPreference = (): boolean => {
  try { return localStorage.getItem("bn.writing-spellcheck") !== "false"; }
  catch { return true; }
};

class WritingCount {
  private timer: ReturnType<typeof setTimeout> | undefined;
  private counted: Text | null = null;
  private words = 0;
  private body = 0;

  constructor(private readonly view: EditorView, private readonly label: HTMLElement | null) {
    this.schedule();
  }

  update(update: ViewUpdate): void {
    if (update.docChanged || update.selectionSet) this.schedule();
  }

  destroy(): void { clearTimeout(this.timer); }

  private schedule(): void {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.render(), 300);
  }

  private render(): void {
    if (!this.label) return;
    const { doc, selection } = this.view.state;
    if (doc !== this.counted) {
      const text = doc.toString();
      this.body = sourceBodyStart(text);
      this.words = countSourceWords(text.slice(this.body));
      this.counted = doc;
    }
    const { from, to, empty } = selection.main;
    const selected = countSourceWords(doc.sliceString(Math.min(to, Math.max(from, this.body)), to));
    this.label.textContent = empty ? wordCountLabel(this.words) : `${wordCountLabel(selected)} selected · ${wordCountLabel(this.words)} total`;
  }
}

export const writingTools = (host: HTMLElement): Extension => {
  const panel = host.closest("[data-source-editor]");
  const checkbox = panel?.querySelector<HTMLInputElement>("[data-writing-spellcheck]");
  const label = panel?.querySelector<HTMLElement>("[data-writing-count]") ?? null;
  const spelling = new Compartment();
  const enabled = spellcheckPreference();
  if (checkbox) checkbox.checked = enabled;
  return [
    spelling.of(EditorView.contentAttributes.of({ spellcheck: String(enabled) })),
    ViewPlugin.fromClass(class {
      private readonly count: WritingCount;
      private readonly change = (): void => {
        const checked = checkbox?.checked ?? true;
        this.view.dispatch({ effects: spelling.reconfigure(EditorView.contentAttributes.of({ spellcheck: String(checked) })) });
        try { localStorage.setItem("bn.writing-spellcheck", String(checked)); }
        catch { /* The choice still applies to this editor. */ }
      };
      constructor(private readonly view: EditorView) {
        this.count = new WritingCount(view, label);
        checkbox?.addEventListener("change", this.change);
      }
      update(update: ViewUpdate): void { this.count.update(update); }
      destroy(): void {
        this.count.destroy();
        checkbox?.removeEventListener("change", this.change);
      }
    }),
  ];
};
