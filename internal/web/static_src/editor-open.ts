import { buildOpenNoteForm, normalizeOpenErrorMessage } from "./api";
import { queryElement } from "./dom";
import { setStatusText } from "./status";

type OpenEditorStatusKind = "" | "is-success" | "is-error";

export type OpenEditorConfig = {
  key: string;
  csrfToken: string;
};

export type OpenEditorFetchRequest = {
  url: string;
  init: RequestInit;
};

export const readOpenEditorConfig = (container: HTMLElement): OpenEditorConfig => ({
  key: container.getAttribute("data-open-key") || "",
  csrfToken: container.getAttribute("data-open-csrf") || "",
});

export const buildOpenEditorFetchRequest = (
  config: OpenEditorConfig
): OpenEditorFetchRequest => {
  const body = buildOpenNoteForm({ key: config.key, token: config.csrfToken });
  return {
    url: "/api/open",
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

export const initOpenInEditor = (): void => {
  const container = queryElement(document, "[data-open-editor]", HTMLElement);
  if (!container) {
    return;
  }
  const button = queryElement(container, "[data-open-button]", HTMLButtonElement);
  const statusEl = queryElement(container, "[data-open-status]", HTMLElement);
  if (!button || !statusEl) {
    return;
  }

  const config = readOpenEditorConfig(container);

  const setStatus = (message: string, kind: OpenEditorStatusKind = ""): void => {
    setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
  };

  button.addEventListener("click", async () => {
    if (!config.key || !config.csrfToken) {
      setStatus("Open action unavailable.", "is-error");
      return;
    }

    button.disabled = true;
    setStatus("Opening editor…");

    try {
      const request = buildOpenEditorFetchRequest(config);
      const resp = await fetch(request.url, request.init);
      if (!resp.ok) {
        const detail = await resp.text();
        setStatus(normalizeOpenErrorMessage(resp.status, detail), "is-error");
        return;
      }

      setStatus("Opened in editor. Refresh after saving changes.", "is-success");
    } catch {
      setStatus("Open failed.", "is-error");
    } finally {
      button.disabled = false;
    }
  });
};
