import { createUndoToast } from "./properties-toast";
import { undoJournalEntry } from "./properties-workbench-requests";

const storageKey = "bn.trash-undo.v1";
type TrashNotice = { token: string; journalID: string; path: string };
type UndoDeps = {
  fetch?: typeof fetch;
  redirect?: (path: string) => void;
};

// The CSRF token is minted per server instance. A notice from a different
// server/vault must never offer an undo against the new instance's journal.
export const rememberTrashUndo = (notice: TrashNotice): boolean => {
  try {
    window.sessionStorage.setItem(storageKey, JSON.stringify(notice));
    return true;
  } catch {
    return false;
  }
};

const readTrashUndo = (token: string): TrashNotice | null => {
  try {
    const raw = window.sessionStorage.getItem(storageKey);
    window.sessionStorage.removeItem(storageKey);
    const value = JSON.parse(raw || "null") as Partial<TrashNotice> | null;
    if (!value || value.token !== token || typeof value.journalID !== "string" || !value.journalID.trim()) {
      return null;
    }
    return { token, journalID: value.journalID.trim(), path: notePath(value.path) };
  } catch {
    return null;
  }
};

const notePath = (path: unknown): string =>
  typeof path === "string" && path.startsWith("/n/") && !/[\\\r\n]/.test(path) ? path : "/";

export const showTrashUndo = (
  host: HTMLElement,
  notice: TrashNotice,
  deps: UndoDeps = {}
): void => {
  const fetcher = deps.fetch || fetch;
  const redirect = deps.redirect || ((path: string) => window.location.assign(path));
  const historyHref = "/history";
  const toast = createUndoToast(document, host, {
    onUndo: (journalID) => {
      toast.show({ message: "Restoring note...", journalID: "", historyHref });
      void undoJournalEntry(fetcher, notice.token, journalID).then((outcome) => {
        if (outcome.ok) {
          redirect(notePath(notice.path));
          return;
        }
        toast.show({ message: outcome.message, journalID: "", historyHref });
      });
    },
  });
  toast.element.dataset.trashUndoToast = "";
  toast.show({ message: "Moved note to Trash.", journalID: notice.journalID, historyHref });
};

export const initTrashUndo = (deps: UndoDeps = {}): void => {
  const host = document.querySelector<HTMLElement>("[data-trash-undo-host]");
  const token = host?.dataset.trashUndoCsrf;
  if (!host || !token) {
    return;
  }
  const notice = readTrashUndo(token);
  if (notice) {
    showTrashUndo(host, notice, deps);
  }
};
