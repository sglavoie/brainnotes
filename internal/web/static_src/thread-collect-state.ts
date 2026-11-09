import type { CollectRequest } from "./thread-loader";

export type ThreadAttempt = { key: string; content: string; revision: string; saveID: string };
export type CollectionDraft = { target: string; group: string; commentary: string; pending?: ThreadAttempt };
export const collectionStorage = (vault: string, request: CollectRequest, status: HTMLElement) => {
  const key = `bn.thread-collect.v1:${JSON.stringify([vault, request.source, request.quote])}`;
  return {
    read(): CollectionDraft | null {
      try {
        const value: unknown = JSON.parse(sessionStorage.getItem(key) || "null");
        if (!value || typeof value !== "object") return null;
        const draft = value as CollectionDraft;
        if (typeof draft.target !== "string" || typeof draft.group !== "string" || typeof draft.commentary !== "string") return null;
        if (draft.pending && ![draft.pending.key, draft.pending.content, draft.pending.revision, draft.pending.saveID].every((v) => typeof v === "string")) return null;
        return draft;
      } catch { return null; }
    },
    write(draft: CollectionDraft): void {
      try { sessionStorage.setItem(key, JSON.stringify(draft)); }
      catch { status.textContent = "Recovery storage is unavailable. Keep this dialog open until the save is confirmed."; }
    },
    clear(): void {
      try { sessionStorage.removeItem(key); }
      catch { status.textContent += " The browser could not clear its recovery copy."; }
    },
  };
};
