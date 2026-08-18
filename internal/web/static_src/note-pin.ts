import {
  buildNotePinRemoveFetchRequest,
  buildNotePinSetFetchRequest,
  normalizeManagementErrorMessage,
  readNotePinResponsePayload,
  type ManagementResult,
  type NotePinFetchRequest,
} from "./api";
import { queryElement } from "./dom";
import { setStatusText } from "./status";

type NotePinStatusKind = "" | "is-success" | "is-error";
type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type NotePinConfig = {
  key: string;
  csrfToken: string;
  pinned: boolean;
};

export type NotePinActionDeps = {
  fetch?: FetchLike;
};

export const readNotePinConfig = (container: HTMLElement): NotePinConfig => ({
  key: container.getAttribute("data-pin-key") || "",
  csrfToken: container.getAttribute("data-pin-csrf") || "",
  pinned: container.getAttribute("data-pin-state") === "pinned",
});

export const buildNotePinToggleRequest = (config: NotePinConfig): NotePinFetchRequest => {
  const request = { key: config.key, token: config.csrfToken };
  return config.pinned
    ? buildNotePinRemoveFetchRequest(request)
    : buildNotePinSetFetchRequest(request);
};

export const initNotePinAction = (deps: NotePinActionDeps = {}): void => {
  const container = queryElement(document, "[data-pin-note]", HTMLElement);
  if (!container) {
    return;
  }
  const button = queryElement(container, "[data-pin-button]", HTMLButtonElement);
  const statusEl = queryElement(container, "[data-pin-status]", HTMLElement);
  if (!button || !statusEl) {
    return;
  }

  const config = readNotePinConfig(container);
  const fetcher = deps.fetch || fetch;

  const setStatus = (message: string, kind: NotePinStatusKind = ""): void => {
    setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
  };

  const applyState = (pinned: boolean): void => {
    config.pinned = pinned;
    container.setAttribute("data-pin-state", pinned ? "pinned" : "unpinned");
    button.setAttribute("aria-pressed", pinned ? "true" : "false");
    button.textContent = pinned ? "Unpin" : "Pin";
  };

  button.addEventListener("click", async () => {
    if (!config.key || !config.csrfToken) {
      setStatus("Pin action unavailable.", "is-error");
      return;
    }

    const pinning = !config.pinned;
    button.disabled = true;
    setStatus(pinning ? "Pinning note..." : "Unpinning note...");

    try {
      const request = buildNotePinToggleRequest(config);
      const resp = await fetcher(request.url, request.init);
      const payload = await readNotePinResponsePayload(resp);
      if (!resp.ok) {
        setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }

      const result = payload as ManagementResult;
      applyState(pinning);
      const fallback = pinning ? "Pinned note." : "Unpinned note.";
      setStatus(result && result.ok && result.message ? result.message : fallback, "is-success");
    } catch {
      setStatus(pinning ? "Pin failed." : "Unpin failed.", "is-error");
    } finally {
      button.disabled = false;
    }
  });
};
