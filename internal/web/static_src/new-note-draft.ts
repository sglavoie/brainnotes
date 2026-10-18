import { asRecord } from "./api-internal";

export type NewNoteValues = { title: string; folder: string };
export type PendingCreation = { signature: string; id: string };
type Draft = NewNoteValues & { pending?: PendingCreation };

const parseDraft = (raw: string | null): Draft | null => {
  const value = asRecord(JSON.parse(raw || "null"));
  if (!value || typeof value.title !== "string" || typeof value.folder !== "string") return null;
  const pending = asRecord(value.pending);
  const draft: Draft = { title: value.title, folder: value.folder };
  if (pending && typeof pending.signature === "string" && typeof pending.id === "string") {
    draft.pending = { signature: pending.signature, id: pending.id };
  }
  return draft;
};

// Session storage is tab-local and origin-scoped. The opaque vault identity
// also protects against a different vault later being served at that origin.
export const newNoteDraft = (form: HTMLElement) => {
  const vault = form.dataset.newNoteVault;
  const suggestion = new URLSearchParams(window.location.search).get("title") || "";
  const key = `bn.new-note.v1:${JSON.stringify([vault, suggestion])}`;
  const notice = form.querySelector<HTMLElement>("[data-new-note-draft-status]");
  const report = (text: string): void => { if (notice && notice.textContent !== text) notice.textContent = text; };
  return {
    restore: (): Draft | null => {
      if (!vault) return null;
      try {
        const draft = parseDraft(window.sessionStorage.getItem(key));
        if (draft) report("Unfinished note creation restored in this tab.");
        return draft;
      } catch { report("Draft recovery is unavailable in this browser."); return null; }
    },
    save: (draft: Draft): void => {
      if (!vault) return;
      try {
        window.sessionStorage.setItem(key, JSON.stringify(draft));
        report("Creation details kept in this tab.");
      } catch { report("The browser could not keep these details. Keep this page open until creation is confirmed."); }
    },
    clear: (): void => {
      if (!vault) return;
      try { window.sessionStorage.removeItem(key); report(""); }
      catch { report("The browser could not clear these details. They may reappear after a reload."); }
    },
  };
};
