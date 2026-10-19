import { normalizeSearchResults } from "./api-search";
import { wikilinkContextAt, wikilinkOptions, wikilinkSearchURL, type WikilinkOption } from "./source-wikilink";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

const modifiedKey = (event: KeyboardEvent): boolean =>
  event.isComposing || event.ctrlKey || event.metaKey || event.altKey;

// Keep the native textarea and its capture/draft behavior. Suggestions only
// replace the unfinished link at the caret; the server supplies exact targets.
export class CaptureWikilinks {
  private readonly list: HTMLUListElement;
  private options: WikilinkOption[] = [];
  private active = 0;
  private generation = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private request: AbortController | undefined;
  private composing = false;

  constructor(private readonly input: HTMLTextAreaElement, private readonly fetcher: FetchLike) {
    this.list = input.ownerDocument.createElement("ul");
    this.list.id = "capture-wikilinks";
    this.list.className = "capture-wikilinks";
    this.list.setAttribute("role", "listbox");
    this.list.setAttribute("aria-label", "Note suggestions");
    this.list.hidden = true;
    (input.closest(".capture-controls") || input).after(this.list);
    // As in the query bar, focus stays in the field while the active option
    // is announced through aria-activedescendant, which needs combobox
    // semantics and an expanded state that follows the visible list.
    input.setAttribute("role", "combobox");
    input.setAttribute("aria-expanded", "false");
    input.setAttribute("aria-autocomplete", "list");
    input.setAttribute("aria-controls", this.list.id);
    input.setAttribute("aria-haspopup", "listbox");
    input.addEventListener("input", () => this.schedule());
    input.addEventListener("click", () => this.schedule());
    input.addEventListener("keyup", (event) => {
      if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) this.schedule();
    });
    input.addEventListener("blur", () => this.close());
    input.addEventListener("compositionstart", () => { this.composing = true; this.close(); });
    input.addEventListener("compositionend", () => { this.composing = false; this.schedule(); });
    input.addEventListener("keydown", (event) => this.keydown(event));
    // Retain textarea focus until click, including Safari's synthetic mouse
    // event after a tap. Choosing on pointerdown would also choose on scrolling.
    this.list.addEventListener("mousedown", (event) => event.preventDefault());
  }

  close(): void {
    this.generation += 1;
    clearTimeout(this.timer);
    this.request?.abort();
    this.list.hidden = true;
    this.options = [];
    this.input.setAttribute("aria-expanded", "false");
    this.input.removeAttribute("aria-activedescendant");
  }

  private context() {
    if (this.composing || this.input.disabled || this.input.selectionStart !== this.input.selectionEnd) return null;
    const context = wikilinkContextAt(this.input.value, this.input.selectionStart);
    // Heading and alias completion are not note-target searches.
    return context && !/[#|]/.test(context.query) ? context : null;
  }

  private schedule(): void {
    this.close();
    if (!this.context()) return;
    const generation = this.generation;
    this.timer = setTimeout(() => { void this.search(generation); }, 150);
  }

  private async search(generation: number): Promise<void> {
    const context = this.context();
    if (!context) return;
    const text = this.input.value;
    const caret = this.input.selectionStart;
    this.request = new AbortController();
    try {
      const response = await this.fetcher(wikilinkSearchURL("/api/search", context.query), {
        headers: { Accept: "application/json" }, signal: this.request.signal,
      });
      if (!response.ok || response.redirected) return;
      const matches = wikilinkOptions(normalizeSearchResults(await response.json()));
      if (!this.isCurrent(generation, text, caret)) return;
      this.options = matches;
      this.active = 0;
      this.render();
    } catch {
      // Optional suggestions must never interrupt capture or erase its draft.
      if (generation === this.generation) this.close();
    }
  }

  private isCurrent(generation: number, text: string, caret: number): boolean {
    return generation === this.generation && this.input.isConnected && !this.input.disabled
      && this.input.value === text && this.input.selectionStart === caret && this.context() !== null;
  }

  private render(): void {
    this.list.replaceChildren();
    this.list.hidden = this.options.length === 0;
    this.input.setAttribute("aria-expanded", String(!this.list.hidden));
    this.options.forEach((option, index) => {
      const row = this.input.ownerDocument.createElement("li");
      row.id = `${this.list.id}-${index}`;
      row.setAttribute("role", "option");
      row.textContent = option.detail ? `${option.label} — ${option.detail}` : option.label;
      row.addEventListener("click", () => this.choose(index));
      this.list.append(row);
    });
    this.highlight();
  }

  private highlight(): void {
    Array.from(this.list.children).forEach((row, index) => {
      row.setAttribute("aria-selected", String(index === this.active));
    });
    const row = this.list.children[this.active];
    if (row) {
      this.input.setAttribute("aria-activedescendant", row.id);
      row.scrollIntoView?.({ block: "nearest" });
    }
  }

  private keydown(event: KeyboardEvent): void {
    if (modifiedKey(event)) return;
    if (event.key === "Escape") { this.close(); return; }
    if (this.list.hidden) return;
    if (event.key === "Enter") {
      event.preventDefault();
      this.choose(this.active);
    } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const direction = event.key === "ArrowDown" ? 1 : -1;
      this.active = (this.active + direction + this.options.length) % this.options.length;
      this.highlight();
    } else if (event.key === "Tab") this.close();
  }

  private choose(index: number): void {
    const context = this.context();
    const option = this.options[index];
    if (!context || !option) return;
    const caret = this.input.selectionStart;
    const closed = this.input.value.slice(caret, caret + 2) === "]]";
    const insertion = option.label + (closed ? "" : "]]");
    const length = this.input.value.length - (caret - context.from) + insertion.length;
    if (this.input.maxLength >= 0 && length > this.input.maxLength) { this.close(); return; }
    this.input.setRangeText(insertion, context.from, caret, "end");
    if (closed) this.input.setSelectionRange(this.input.selectionStart + 2, this.input.selectionStart + 2);
    this.close();
    this.input.focus();
    this.input.dispatchEvent(new Event("input", { bubbles: true }));
  }
}
