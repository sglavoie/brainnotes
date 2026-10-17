import { announceConfirmedWrite } from "./write-notice";
import {
  buildNoteKeyMutationForm,
  normalizeManagementErrorMessage,
  type ManagementResult,
} from "./api";
import { queryElement } from "./dom";
import { setStatusText } from "./status";
import { rememberTrashUndo, showTrashUndo } from "./note-trash-undo";

type TrashNoteStatusKind = "" | "is-success" | "is-error";
type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type TrashNoteConfig = {
  key: string;
  csrfToken: string;
  confirmMessage: string;
  redirectPath: string;
};

export type TrashNoteFetchRequest = {
  url: string;
  init: RequestInit;
};

export type TrashNoteActionDeps = {
  fetch?: FetchLike;
  confirm?: (message: string) => boolean;
  redirect?: (path: string) => void;
};

export const readTrashNoteConfig = (container: HTMLElement): TrashNoteConfig => ({
  key: container.getAttribute("data-trash-key") || "",
  csrfToken: container.getAttribute("data-trash-csrf") || "",
  confirmMessage: container.getAttribute("data-trash-confirm") || "Move this note to Trash?",
  redirectPath: container.getAttribute("data-trash-redirect") || "/",
});

export const buildTrashNoteFetchRequest = (
  config: Pick<TrashNoteConfig, "key" | "csrfToken">
): TrashNoteFetchRequest => {
  const body = buildNoteKeyMutationForm({ key: config.key, token: config.csrfToken });
  return {
    url: "/api/notes/trash",
    init: {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": config.csrfToken,
      },
      body: body.toString(),
    },
  };
};

const keepUndoNoticeOnPage = (
  container: HTMLElement,
  token: string,
  result: ManagementResult,
  deps: TrashNoteActionDeps
): boolean => {
  if (!result?.ok || !result.journal_id?.trim()) {
    return false;
  }
  const notice = { token, journalID: result.journal_id, path: window.location.pathname };
  if (rememberTrashUndo(notice)) {
    return false;
  }
  // Storage may be disabled. Keep the receipt and its Undo control on this
  // page instead of losing them in the redirect.
  showTrashUndo(container, notice, deps);
  return true;
};

export const initTrashNoteAction = (deps: TrashNoteActionDeps = {}): void => {
  const container = queryElement(document, "[data-trash-note]", HTMLElement);
  if (!container) {
    return;
  }
  const button = queryElement(container, "[data-trash-button]", HTMLButtonElement);
  const statusEl = queryElement(container, "[data-trash-status]", HTMLElement);
  if (!button || !statusEl) {
    return;
  }

  const config = readTrashNoteConfig(container);
  const fetcher = deps.fetch || fetch;
  const confirmAction = deps.confirm || ((message: string): boolean => window.confirm(message));
  const redirect = deps.redirect || ((path: string): void => window.location.assign(path));

  const setStatus = (message: string, kind: TrashNoteStatusKind = ""): void => {
    setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
  };

  button.addEventListener("click", async () => {
    if (!config.key || !config.csrfToken) {
      setStatus("Trash action unavailable.", "is-error");
      return;
    }
    if (!confirmAction(config.confirmMessage)) {
      setStatus("Trash canceled.");
      return;
    }

    button.disabled = true;
    setStatus("Moving note to Trash...");

    try {
      const request = buildTrashNoteFetchRequest(config);
      const resp = await fetcher(request.url, request.init);
      const payload = await readTrashResponsePayload(resp);
      if (!resp.ok) {
        setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }

      const result = payload as ManagementResult;
      announceTrashWrite(result);
      const message = result && result.ok && result.message ? result.message : "Moved note to Trash.";
      if (keepUndoNoticeOnPage(container, config.csrfToken, result, { fetch: fetcher, redirect })) {
        setStatus(message, "is-success");
        return;
      }
      setStatus(`${message} Redirecting...`, "is-success");
      redirect(config.redirectPath);
    } catch {
      setStatus("Trash failed.", "is-error");
    } finally {
      button.disabled = false;
    }
  });
};

const readTrashResponsePayload = async (resp: Response): Promise<unknown> => {
  const contentType = resp.headers?.get("Content-Type") || "";
  try {
    if (contentType.includes("application/json")) {
      return await resp.json();
    }
    return await resp.text();
  } catch {
    return "";
  }
};

const announceTrashWrite = (result: ManagementResult): void => {
  if (result?.ok) announceConfirmedWrite();
};
