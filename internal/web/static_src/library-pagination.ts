import { updateBrowserQuery } from "./filters";
import type { SearchCounts } from "./search-summary";

// Position belongs to navigation, never to a saved query or a copied search.
export class LibraryPagination {
  offset: number;
  private signature: string | null = null;
  private counts: SearchCounts | null = null;
  private shown = 0;
  private unavailable = false;
  private readonly panel: HTMLElement | null;
  private readonly previous: HTMLButtonElement | null;
  private readonly next: HTMLButtonElement | null;

  constructor(private readonly root: HTMLElement, change: () => void, private readonly blocked: () => boolean) {
    const offset = Number(new URLSearchParams(window.location.search).get("offset"));
    this.offset = Number.isSafeInteger(offset) && offset >= 0 ? offset : 0;
    this.panel = root.querySelector("[data-library-pagination]");
    this.previous = root.querySelector("[data-library-previous]");
    this.next = root.querySelector("[data-library-next]");
    this.previous?.addEventListener("click", () => this.move(-1, change));
    this.next?.addEventListener("click", () => this.move(1, change));
  }

  prepare(apiURL: string): { url: string; changed: boolean } {
    const changed = this.signature !== null && this.signature !== apiURL;
    this.signature = apiURL;
    if (changed) this.offset = 0;
    this.syncURL();
    const url = new URL(apiURL, window.location.origin);
    if (this.offset) url.searchParams.set("offset", String(this.offset));
    return { url: url.pathname + url.search, changed };
  }

  render(counts: SearchCounts | null, shown: number): void {
    this.counts = counts;
    this.shown = shown;
    this.unavailable = false;
    this.display();
  }

  failed(): void {
    this.unavailable = true;
    this.shown = 0;
  }

  private display(): void {
    const { counts, shown } = this;
    const total = counts ? Math.min(counts.total, counts.limit || counts.total) : shown;
    if (this.panel) this.panel.hidden = !showPages(counts, total, this.offset);
    if (this.previous) this.previous.disabled = this.offset === 0;
    if (this.next) this.next.disabled = !counts || this.offset + shown >= total || shown === 0;
    const label = this.root.querySelector("[data-library-page]");
    if (label) label.textContent = this.pageLabel(total);
  }

  busy(busy: boolean): void {
    if (busy) {
      if (this.previous) this.previous.disabled = true;
      if (this.next) this.next.disabled = true;
    } else this.display();
  }

  private move(direction: number, change: () => void): void {
    if (!this.counts || this.blocked() || this.root.getAttribute("aria-busy") === "true") return;
    const total = Math.min(this.counts.total, this.counts.limit || this.counts.total);
    const last = Math.max(0, Math.floor((total - 1) / this.counts.cap) * this.counts.cap);
    this.offset = Math.max(0, Math.min(last, this.offset + direction * this.counts.cap));
    this.syncURL();
    change();
    // Keep keyboard users at the result heading when page buttons disable.
    const heading = this.root.querySelector<HTMLElement>("#overview-results-heading");
    if (heading) { heading.tabIndex = -1; heading.focus(); }
  }

  private pageLabel(total: number): string {
    if (this.unavailable) return "Page unavailable";
    return this.shown ? `${this.offset + 1}–${this.offset + this.shown} of ${total}` : "No results on this page";
  }

  private syncURL(): void {
    const params = new URLSearchParams(window.location.search);
    if (this.offset) params.set("offset", String(this.offset));
    else params.delete("offset");
    updateBrowserQuery(params);
  }
}

const showPages = (counts: SearchCounts | null, total: number, offset: number): boolean =>
  counts !== null && (total > counts.cap || offset > 0);
