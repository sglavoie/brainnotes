import { readableExcerpt } from "./note-excerpt";

const linkSelector = '[data-note-body] a[href^="/n/"], .backlink-summary a[href^="/n/"], .related-card-title a, .overview-result-link[href^="/n/"]';
const linkTitle = (link: HTMLAnchorElement): string =>
  (link.querySelector(".overview-result-title") || link).textContent?.trim() || "Note";

class NotePeek {
  private readonly panel = document.createElement("div");
  private readonly title = document.createElement("h2");
  private readonly excerpt = document.createElement("p");
  private readonly openLink = document.createElement("a");
  private readonly closeButton = document.createElement("button");
  private active: HTMLAnchorElement | null = null;
  private returnTarget: HTMLElement | null = null;
  private request: AbortController | null = null;
  private timer = 0;

  constructor() {
    this.panel.className = "note-peek";
    this.panel.id = "note-peek";
    this.panel.hidden = true;
    this.panel.popover = "manual";
    this.panel.setAttribute("role", "dialog");
    this.panel.setAttribute("aria-labelledby", "note-peek-title");
    this.title.id = "note-peek-title";
    this.excerpt.setAttribute("role", "status");
    this.openLink.textContent = "Open note";
    this.closeButton.type = "button";
    this.closeButton.className = "bn-button";
    this.closeButton.textContent = "Close preview";
    this.closeButton.addEventListener("click", () => this.close(true));
    this.panel.append(this.title, this.excerpt, this.openLink, this.closeButton);
    document.body.append(this.panel);
    this.panel.addEventListener("pointerenter", () => window.clearTimeout(this.timer));
    this.panel.addEventListener("pointerleave", () => this.scheduleClose());
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && this.active) { event.preventDefault(); this.close(true); }
    });
    document.addEventListener("pointerdown", (event) => {
      if (event.target instanceof Node && !this.panel.contains(event.target) && event.target !== this.active &&
          !(event.target instanceof Element && event.target.closest(".note-peek-trigger"))) this.close(false);
    });
    window.addEventListener("resize", () => { if (this.active) this.position(this.active); });
    window.addEventListener("scroll", () => { if (this.active) this.position(this.active); }, { passive: true });
  }

  attach(link: HTMLAnchorElement): void {
    if (link.dataset.peekAttached) return;
    link.dataset.peekAttached = "true";
    const button = document.createElement("button");
    button.type = "button";
    button.className = "note-peek-trigger";
    button.setAttribute("aria-label", `Preview ${linkTitle(link)}`);
    button.setAttribute("aria-controls", this.panel.id);
    button.innerHTML = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/></svg>';
    button.title = "Preview note";
    const wrapper = document.createElement(link.classList.contains("overview-result-link") ? "div" : "span");
    wrapper.className = "note-peek-anchor";
    const focused = document.activeElement === link;
    link.before(wrapper);
    wrapper.append(link, button);
    if (focused) link.focus({ preventScroll: true });
    button.addEventListener("click", () => { void this.show(link, button); });
    button.addEventListener("pointerenter", () => window.clearTimeout(this.timer));
    link.addEventListener("pointerenter", (event) => {
      if (event.pointerType !== "mouse" || document.querySelector("dialog[open]")) return;
      window.clearTimeout(this.timer);
      this.timer = window.setTimeout(() => { void this.show(link); }, 350);
    });
    link.addEventListener("pointerleave", () => this.scheduleClose());
    link.addEventListener("keydown", (event) => {
      if (event.altKey && event.key === "ArrowDown") { event.preventDefault(); void this.show(link, link); }
    });
  }

  private scheduleClose(): void {
    window.clearTimeout(this.timer);
    this.timer = window.setTimeout(() => {
      if (!this.returnTarget && !this.panel.contains(document.activeElement)) this.close(false);
    }, 200);
  }

  private close(restore: boolean): void {
    window.clearTimeout(this.timer);
    this.request?.abort();
    this.panel.hidePopover();
    this.panel.hidden = true;
    this.active = null;
    if (restore) this.returnTarget?.focus({ preventScroll: true });
    this.returnTarget = null;
  }

  private position(link: HTMLAnchorElement): void {
    const rect = link.getBoundingClientRect();
    const width = this.panel.offsetWidth;
    const height = this.panel.offsetHeight;
    this.panel.style.left = `${Math.max(12, Math.min(rect.left, window.innerWidth - width - 12))}px`;
    this.panel.style.top = `${Math.max(12, Math.min(rect.bottom + 8, window.innerHeight - height - 12))}px`;
  }

  private async show(link: HTMLAnchorElement, returnTarget?: HTMLElement): Promise<void> {
    this.close(false);
    this.active = link;
    this.returnTarget = returnTarget || null;
    this.title.textContent = linkTitle(link);
    this.excerpt.textContent = "Loading note…";
    this.openLink.href = link.href;
    this.panel.hidden = false;
    this.panel.showPopover();
    this.position(link);
    if (returnTarget) this.closeButton.focus({ preventScroll: true });
    await this.load(link);
  }

  private async load(link: HTMLAnchorElement): Promise<void> {
    const request = new AbortController();
    this.request = request;
    try {
      const key = decodeURIComponent(new URL(link.href).pathname.slice(3));
      const response = await fetch(`/api/note/peek?${new URLSearchParams({ key })}`, { signal: request.signal });
      if (!response.ok) throw new Error("Preview unavailable");
      const result: { title?: string; excerpt?: string } = await response.json();
      if (request.signal.aborted) return;
      this.title.textContent = result.title || this.title.textContent;
      this.excerpt.textContent = readableExcerpt(result.excerpt || "This note has no excerpt yet.");
      this.position(link);
    } catch {
      if (!request.signal.aborted) this.excerpt.textContent = "Preview unavailable. You can still try opening the note.";
    }
  }
}

export const initNotePeek = (): void => {
  const scopes = document.querySelectorAll("[data-note-body], .note-meta-sidebar, [data-related-notes], #library-results-list");
  if (!scopes.length) return;
  const peek = new NotePeek();
  const attach = (): void => { for (const link of document.querySelectorAll<HTMLAnchorElement>(linkSelector)) peek.attach(link); };
  attach();
  // Search and related results arrive after the document. Ignore mutations to
  // text/status; only new elements can introduce links needing a preview.
  const observer = new MutationObserver((records) => {
    if (records.some((record) => [...record.addedNodes].some((node) => node instanceof Element && !node.matches(".note-peek-trigger")))) attach();
  });
  for (const scope of scopes) observer.observe(scope, { childList: true, subtree: true });
};
