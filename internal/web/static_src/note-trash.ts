import { confirmAction, type ConfirmAction } from "./action-confirm";
import { responseFailure, unansweredRequest } from "./request-outcome";
import { announceConfirmedWrite } from "./write-notice";
import {
  buildNoteKeyMutationForm,
  type ManagementResult,
} from "./api";
import { queryElement } from "./dom";
import { setStatusText } from "./status";
import { rememberTrashUndo, showTrashUndo } from "./note-trash-undo";
import { newWriteOperationID, offerWriteOutcomeCheck } from "./write-outcome-check";

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
  confirm?: ConfirmAction;
  redirect?: (path: string) => void;
};

export const readTrashNoteConfig = (container: HTMLElement): TrashNoteConfig => ({
  key: container.getAttribute("data-trash-key") || "",
  csrfToken: container.getAttribute("data-trash-csrf") || "",
  confirmMessage: container.getAttribute("data-trash-confirm") || "Move this note to Trash?",
  redirectPath: container.getAttribute("data-trash-redirect") || "/",
});

export const buildTrashNoteFetchRequest = (
  config: Pick<TrashNoteConfig, "key" | "csrfToken">,
  operationID = ""
): TrashNoteFetchRequest => {
  const body = buildNoteKeyMutationForm({ key: config.key, token: config.csrfToken });
  if (operationID) body.set("operation_id", operationID);
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
  const confirmTrash = deps.confirm || ((message: string) => confirmAction({
    title: "Move note to Trash?", description: `${message}\nYou can restore it from History or Undo after moving it.`,
    confirmLabel: "Move to Trash",
  }, document, button));
  const redirect = deps.redirect || ((path: string): void => window.location.assign(path));

  const setStatus = (message: string, kind: TrashNoteStatusKind = ""): void => {
    setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
  };

  // A confirmed trash is the same move a direct success made, so the note is no
  // longer here to act on. The confirmation stays readable, with the way on
  // that a direct success would have taken offered beside it.
  const settleTrashedNote = (): void => {
    button.disabled = true;
    const onward = statusEl.ownerDocument.createElement("a");
    onward.href = config.redirectPath;
    onward.textContent = "Leave this note";
    statusEl.append(" ", onward);
  };

  button.addEventListener("click", async () => {
    if (!config.key || !config.csrfToken) {
      setStatus("Trash action unavailable.", "is-error");
      return;
    }
    if (button.disabled) return;
    button.disabled = true;
    let operationID = "";
    try {
      if (!await confirmTrash(config.confirmMessage)) {
        setStatus("Trash canceled.");
        return;
      }
      setStatus("Moving note to Trash...");
      operationID = newWriteOperationID();
      const request = buildTrashNoteFetchRequest(config, operationID);
      const resp = await fetcher(request.url, request.init);
      const payload = await readTrashResponsePayload(resp);
      const failure = responseFailure(resp, payload, "write");
      if (failure) {
        setStatus(failure.message, "is-error");
        if (failure.uncertain) offerWriteOutcomeCheck(statusEl, operationID, fetcher, "Trash", settleTrashedNote);
        return;
      }

      const result = payload as ManagementResult;
      announceTrashWrite(result);
      const message = result.message || "Moved note to Trash.";
      if (keepUndoNoticeOnPage(container, config.csrfToken, result, { fetch: fetcher, redirect })) {
        setStatus(message, "is-success");
        return;
      }
      setStatus(`${message} Redirecting...`, "is-success");
      redirect(config.redirectPath);
    } catch {
      setStatus(unansweredRequest("write").message, "is-error");
      offerWriteOutcomeCheck(statusEl, operationID, fetcher, "Trash", settleTrashedNote);
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
