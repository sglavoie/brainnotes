import { prepareTitleNavigation } from "./title-navigation";
import { closeButton } from "./close-button";
import { highlightedText } from "./text-highlight";
import { closestEntries, queryEntries, recentEntries, savedViewEntries, searchEntries, workspaceEntries, type SwitcherEntry } from "./switcher-data";

const activate = (selector: string, reveal: boolean): void => {
  const control = document.querySelector<HTMLElement>(selector);
  if (reveal) control?.closest("details")?.setAttribute("open", "");
  control?.click();
};

export class CommandSwitcher {
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
    const header = document.createElement("header");
    header.append(title, closeButton("Close search", () => this.dialog.close()));
    this.input.addEventListener("input", () => { this.selectionMoved = false; this.search(); });
    this.input.addEventListener("keydown", (event) => this.keydown(event));
    this.dialog.addEventListener("close", () => this.closed());
    window.addEventListener("pageshow", (event) => { if (event.persisted) this.dialog.close(); });
    const beside = document.createElement("button");
    beside.type = "button"; beside.className = "bn-button switcher-beside";
    beside.textContent = "Keep beside";
    beside.setAttribute("aria-keyshortcuts", "Alt+Enter");
    beside.addEventListener("click", () => this.keepBeside());
    const footer = document.createElement("footer");
    const keys = document.createElement("p");
    keys.className = "switcher-keys";
    keys.innerHTML = "<kbd>&gt;</kbd> for actions only · <kbd>Enter</kbd> to open · <kbd>Alt Enter</kbd> to keep beside · <kbd>Escape</kbd> to close";
    footer.append(keys, beside);
    this.dialog.append(header, label, this.input, this.list, this.status, footer);
    document.body.append(this.dialog);
  }

  open(origin: HTMLElement | null): void {
    if (document.querySelector("dialog[open]")) return;
    this.origin = origin;
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

  // A leading ">" asks for actions only: nothing is searched and nothing is
  // offered for creation, so "> pin" can never open or make a note.
  private search(): void {
    this.request?.abort(); window.clearTimeout(this.timer);
    const typed = this.input.value.trim().toLocaleLowerCase();
    const commands = typed.startsWith(">");
    const query = commands ? typed.slice(1).trim() : typed;
    const matches = (entry: SwitcherEntry): boolean => entry.label.toLocaleLowerCase().includes(query);
    const actionsFirst = (a: SwitcherEntry, b: SwitcherEntry): number => Number(b.detail === "Action") - Number(a.detail === "Action");
    const local = commands ? workspaceEntries().filter(matches).sort(actionsFirst) : [...recentEntries(), ...this.views, ...workspaceEntries()].filter(matches);
    this.render(local, commands ? "" : this.input.value.trim());
    if (!query || commands) return;
    this.status.textContent = "Searching notes…";
    const request = new AbortController(); this.request = request;
    this.timer = window.setTimeout(() => { void this.searchNotes(query, local, request.signal); }, 120);
  }

  private async searchNotes(query: string, local: SwitcherEntry[], signal: AbortSignal): Promise<void> {
    try {
      const notes = await searchEntries(query, signal);
      const closest = notes.length ? [] : await closestEntries(query, signal).catch(() => []);
      if (signal.aborted) return;
      const found = notes.length ? notes : closest;
      const paths = new Set(found.map((entry) => entry.path?.toLocaleLowerCase()));
      this.render([...found, ...local.filter((entry) => !entry.path || !paths.has(entry.path.toLocaleLowerCase()))], this.input.value.trim());
      if (closest.length) this.status.textContent = `No exact matches. ${closest.length === 1 ? "Closest title" : "Closest titles"} shown.`;
    } catch {
      if (!signal.aborted) this.status.textContent = "Search unavailable. Try again or open Library.";
    }
  }

  private render(entries: SwitcherEntry[], creatable: string): void {
    const results = [...entries, ...queryEntries(creatable)];
    const previous = this.entries[this.active];
    const selected = results.findIndex((entry) => this.selectionMoved && previous && entry.path === previous.path && entry.label === previous.label);
    this.entries = results; this.active = Math.max(0, selected); this.list.replaceChildren();
    results.forEach((entry, index) => {
      const option = document.createElement("div");
      option.id = `switcher-option-${index}`; option.setAttribute("role", "option");
      if (index >= entries.length) option.className = "switcher-query-action";
      const label = document.createElement("span");
      const terms = this.input.value.trim().replace(/^>/, "").trim().split(/\s+/);
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
    this.status.textContent = entries.length ? `${entries.length} ${entries.length === 1 ? "result" : "results"}` : results.length ? "No matches. Try another search or use the actions below." : "No matching actions.";
    this.highlight();
  }

  private highlight(): void {
    const beside = this.dialog.querySelector<HTMLButtonElement>(".switcher-beside");
    if (beside) beside.hidden = !this.entries[this.active]?.path?.startsWith("/n/");
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
    this.afterClose = () => document.dispatchEvent(new CustomEvent("bn:passage-reference", { detail: { path, target: origin } }));
    this.dialog.close();
  }

  private choose(index: number): void {
    const entry = this.entries[index];
    if (!entry) return;
    if (entry.path?.startsWith("/n/")) {
      this.request?.abort(); window.clearTimeout(this.timer); this.generation++;
      prepareTitleNavigation(this.list.children[index]?.querySelector(".switcher-result-title"));
      window.location.assign(entry.path);
      return;
    }
    this.afterClose = () => {
      if (entry.path) window.location.assign(entry.path);
      else if (entry.click) activate(entry.click, entry.reveal === true);
    };
    this.dialog.close();
  }
}
