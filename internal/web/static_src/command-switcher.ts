import { keepReference } from "./side-reference";
import { highlightedText } from "./text-highlight";
import { recentEntries, rememberCurrentNote, savedViewEntries, searchEntries, workspaceEntries, type SwitcherEntry } from "./switcher-data";

export const initCommandSwitcher = (): void => {
  rememberCurrentNote();
  for (const hint of document.querySelectorAll("[data-switcher-key]")) {
    hint.textContent = /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘K" : "Ctrl K";
    hint.closest("button")?.setAttribute("aria-label", `Search ${hint.textContent} — notes and actions`);
  }
  const switcher = new CommandSwitcher();
  for (const trigger of document.querySelectorAll("[data-switcher-launch]")) trigger.addEventListener("click", () => switcher.open());
  document.addEventListener("keydown", (event) => {
    if (event.key.toLowerCase() !== "k" || !(event.metaKey || event.ctrlKey) || event.altKey || event.shiftKey || event.isComposing || event.repeat) return;
    event.preventDefault();
    event.stopPropagation();
    switcher.open();
  }, { capture: true });
};

class CommandSwitcher {
  private readonly dialog = document.createElement("dialog");
  private readonly input = document.createElement("input");
  private readonly list = document.createElement("div");
  private readonly status = document.createElement("p");
  private origin: HTMLElement | null = null;
  private entries: SwitcherEntry[] = [];
  private views: SwitcherEntry[] = [];
  private active = 0;
  private selectionMoved = false;
  private timer = 0;
  private request?: AbortController;
  private afterClose?: () => void;
  private generation = 0;

  constructor() {
    this.dialog.className = "command-switcher";
    this.dialog.setAttribute("aria-labelledby", "switcher-title");
    const title = document.createElement("h2");
    title.id = "switcher-title"; title.textContent = "Find your next thought";
    const label = document.createElement("label");
    label.htmlFor = "switcher-input"; label.textContent = "Search notes, views and actions";
    this.input.id = "switcher-input";
    this.input.type = "search";
    this.input.placeholder = "A note, an idea, somewhere to go…";
    this.input.setAttribute("role", "combobox");
    this.input.setAttribute("aria-autocomplete", "list");
    this.input.setAttribute("aria-expanded", "true");
    this.input.setAttribute("aria-controls", "switcher-results");
    this.list.id = "switcher-results";
    this.list.setAttribute("role", "listbox");
    this.list.setAttribute("aria-label", "Notes and actions");
    this.status.setAttribute("role", "status");
    const close = document.createElement("button");
    close.type = "button"; close.className = "bn-button"; close.textContent = "Close search";
    close.addEventListener("click", () => this.dialog.close());
    this.input.addEventListener("input", () => { this.selectionMoved = false; this.search(); });
    this.input.addEventListener("keydown", (event) => this.keydown(event));
    this.dialog.addEventListener("close", () => this.closed());
    const beside = document.createElement("button");
    beside.type = "button"; beside.className = "bn-button switcher-beside";
    beside.textContent = "Keep beside · Alt + Enter";
    beside.addEventListener("click", () => this.keepBeside());
    this.dialog.append(title, label, this.input, this.list, this.status, beside, close);
    document.body.append(this.dialog);
  }

  open(): void {
    if (document.querySelector("dialog[open]")) return;
    this.origin = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    this.input.value = "";
    this.selectionMoved = false;
    this.views = [];
    this.dialog.showModal();
    this.input.focus({ preventScroll: true });
    this.search();
    const generation = ++this.generation;
    void savedViewEntries().then((views) => {
      if (!this.dialog.open || generation !== this.generation) return;
      this.views = views; this.search();
    }).catch(() => {
      if (this.dialog.open && generation === this.generation) this.status.textContent = "Saved views unavailable. Notes and actions are still available.";
    });
  }

  private closed(): void {
    this.generation++;
    this.request?.abort();
    window.clearTimeout(this.timer);
    this.origin?.focus({ preventScroll: true });
    const action = this.afterClose; this.afterClose = undefined;
    action?.();
  }

  private search(): void {
    this.request?.abort(); window.clearTimeout(this.timer);
    const query = this.input.value.trim().toLocaleLowerCase();
    const local = [...recentEntries(), ...this.views, ...workspaceEntries()].filter((entry) => entry.label.toLocaleLowerCase().includes(query));
    this.render(local);
    this.status.textContent = query ? "Searching notes…" : "↑ ↓ to choose · Enter to open · Esc to return";
    if (!query) return;
    const request = new AbortController(); this.request = request;
    this.timer = window.setTimeout(() => {
      void searchEntries(query, request.signal).then((notes) => {
        if (request.signal.aborted) return;
        const paths = new Set(notes.map((entry) => entry.path?.toLocaleLowerCase()));
        this.render([...notes, ...local.filter((entry) => !entry.path || !paths.has(entry.path.toLocaleLowerCase()))]);
        this.status.textContent = this.entries.length ? `${this.entries.length} ${this.entries.length === 1 ? "result" : "results"}` : "No matches. Try another word or open Library.";
      }).catch(() => {
        if (!request.signal.aborted) this.status.textContent = "Search unavailable. Try again or open Library.";
      });
    }, 120);
  }

  private render(entries: SwitcherEntry[]): void {
    const previous = this.entries[this.active];
    const selected = entries.findIndex((entry) => this.selectionMoved && previous && entry.path === previous.path && entry.label === previous.label);
    this.entries = entries; this.active = Math.max(0, selected); this.list.replaceChildren();
    entries.forEach((entry, index) => {
      const option = document.createElement("div");
      option.id = `switcher-option-${index}`; option.setAttribute("role", "option");
      const label = document.createElement("span");
      const terms = this.input.value.trim().split(/\s+/);
      const name = document.createElement("span"); name.className = "switcher-result-title"; name.append(highlightedText(entry.label, terms)); label.append(name);
      if (entry.excerpt) {
        const excerpt = document.createElement("span"); excerpt.className = "switcher-excerpt";
        excerpt.append(highlightedText(entry.excerpt, terms)); label.append(excerpt);
      }
      const detail = document.createElement("small"); detail.textContent = entry.detail;
      option.append(label, detail);
      option.addEventListener("click", () => this.choose(index));
      this.list.append(option);
    });
    this.highlight();
  }

  private highlight(): void {
    const beside = this.dialog.querySelector<HTMLButtonElement>(".switcher-beside");
    if (beside) beside.disabled = !this.entries[this.active]?.path?.startsWith("/n/");
    Array.from(this.list.children).forEach((option, index) => option.setAttribute("aria-selected", String(index === this.active)));
    if (!this.entries.length) { this.input.removeAttribute("aria-activedescendant"); return; }
    this.input.setAttribute("aria-activedescendant", `switcher-option-${this.active}`);
  }

  private keydown(event: KeyboardEvent): void {
    if (event.isComposing) return;
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); this.dialog.close(); return; }
    if (event.key === "Enter") { event.preventDefault(); if (event.altKey) this.keepBeside(); else this.choose(this.active); }
    const direction = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
    if (!direction || !this.entries.length) return;
    event.preventDefault();
    this.selectionMoved = true;
    this.active = (this.active + direction + this.entries.length) % this.entries.length;
    this.highlight(); this.list.children[this.active]?.scrollIntoView({ block: "nearest" });
  }

  private keepBeside(): void {
    const path = this.entries[this.active]?.path;
    if (!path?.startsWith("/n/")) return;
    const origin = this.origin;
    this.afterClose = () => keepReference(path, origin || undefined);
    this.dialog.close();
  }

  private choose(index: number): void {
    const entry = this.entries[index];
    if (!entry) return;
    this.afterClose = () => {
      if (entry.path) window.location.assign(entry.path);
      else document.querySelector<HTMLButtonElement>(entry.action === "capture" ? "[data-capture-launch]" : "[data-reading-mode]")?.click();
    };
    this.dialog.close();
  }
}
