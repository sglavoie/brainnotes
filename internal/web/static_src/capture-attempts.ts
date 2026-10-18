import { newSaveID } from "./source-save";

// An edited draft does not forget an earlier unanswered submission. Reusing
// its exact text reuses its ID; confirmed captures release their ID so a later
// intentional capture of the same thought remains possible.
export class CaptureAttempts {
  private readonly key: string;
  private readonly attempts = new Map<string, string>();

  constructor(private readonly root: HTMLElement) {
    this.key = `bn.capture-attempts.v1:${JSON.stringify([
      root.dataset.captureVault, root.dataset.captureTarget, root.dataset.captureHeading,
    ])}`;
    if (!root.dataset.captureVault) return;
    try {
      const saved: unknown = JSON.parse(sessionStorage.getItem(this.key) || "[]");
      if (Array.isArray(saved)) {
        for (const item of saved) {
          if (Array.isArray(item) && typeof item[0] === "string" && typeof item[1] === "string" && /^[\w-]{1,128}$/.test(item[1])) {
            this.attempts.set(item[0], item[1]);
          }
        }
      }
    } catch { /* The draft keeper reports unavailable browser storage. */ }
  }

  has(text: string): boolean { return this.attempts.has(text); }

  start(text: string): string {
    const id = this.attempts.get(text) || newSaveID();
    this.attempts.set(text, id);
    this.persist();
    return id;
  }

  settled(text: string): void {
    this.attempts.delete(text);
    this.persist();
  }

  private persist(): void {
    if (!this.root.dataset.captureVault) return;
    try {
      if (this.attempts.size) sessionStorage.setItem(this.key, JSON.stringify([...this.attempts]));
      else sessionStorage.removeItem(this.key);
    } catch {
      const status = this.root.querySelector<HTMLElement>("[data-capture-draft-status]");
      if (status) {
        status.textContent = "The browser could not remember capture attempts. Retry on this page; after reloading, check your inbox first.";
        status.hidden = false;
      }
    }
  }
}
