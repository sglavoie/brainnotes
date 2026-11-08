import { noteKeyFromPath, readNoteReference, renderReference } from "./note-reference";

let panel: SideReference | undefined;

export const keepReference = (path: string, returnTarget?: HTMLElement): void => {
  panel ||= new SideReference();
  void panel.open(noteKeyFromPath(path), returnTarget);
};

export const initSideReference = (): void => {
  try {
    const key = sessionStorage.getItem(storageKey());
    if (key) { panel ||= new SideReference(); void panel.open(key); }
  } catch { /* Reading works without persistence. */ }
};

const storageKey = (): string => `bn.reference:${document.documentElement.dataset.vaultId}`;

class SideReference {
  private readonly root = document.createElement("aside");
  private readonly content = document.createElement("div");
  private readonly close = document.createElement("button");
  private request?: AbortController;
  private returnTarget?: HTMLElement;

  constructor() {
    this.root.className = "side-reference";
    this.root.setAttribute("aria-label", "Reference note");
    this.root.hidden = true;
    const header = document.createElement("header");
    const label = document.createElement("h2");
    label.textContent = "Beside your note";
    this.close.type = "button";
    this.close.className = "bn-button";
    this.close.textContent = "Close reference";
    this.close.addEventListener("click", () => this.hide());
    header.append(label, this.close);
    this.content.className = "reference-content";
    this.content.setAttribute("aria-live", "polite");
    this.root.append(header, this.content);
    this.root.addEventListener("keydown", (event) => {
      if (event.key === "Escape") { event.stopPropagation(); this.hide(); }
    });
    document.body.append(this.root);
  }

  async open(key: string, returnTarget?: HTMLElement): Promise<void> {
    this.request?.abort();
    const request = new AbortController();
    this.request = request;
    this.returnTarget = returnTarget;
    this.root.hidden = false;
    document.body.classList.add("has-reference");
    this.content.textContent = "Loading reference…";
    if (returnTarget) this.close.focus({ preventScroll: true });
    try { sessionStorage.setItem(storageKey(), key); } catch { /* Optional persistence. */ }
    try {
      const note = await readNoteReference(key, request.signal);
      if (!request.signal.aborted) { renderReference(this.content, note); this.content.scrollTop = 0; }
    } catch {
      if (!request.signal.aborted) this.showRetry(key);
    }
  }

  private showRetry(key: string): void {
    this.content.textContent = "Reference unavailable. Your current note is still here. ";
    const retry = document.createElement("button");
    retry.type = "button";
    retry.className = "bn-button";
    retry.textContent = "Retry reference";
    retry.addEventListener("click", () => { void this.open(key, this.returnTarget); });
    this.content.append(retry);
  }

  private hide(): void {
    this.request?.abort();
    this.root.hidden = true;
    document.body.classList.remove("has-reference");
    try { sessionStorage.removeItem(storageKey()); } catch { /* Optional persistence. */ }
    const target = this.returnTarget?.isConnected ? this.returnTarget : document.querySelector<HTMLElement>("[data-note-body], .cm-content");
    target?.focus({ preventScroll: true });
  }
}
