import {
  buildCreateNoteForm,
  normalizeManagementErrorMessage,
} from "./api";
import { normalizeManagementResult } from "./api-internal";
import { queryAllElements, queryElement } from "./dom";
import { setStatusText } from "./status";

type CreateNoteStatusKind = "" | "is-success" | "is-error";
type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type CreateNoteConfig = {
  sourceKey: string;
  target: string;
  csrfToken: string;
};

export type CreateNoteFetchRequest = {
  url: string;
  init: RequestInit;
};

export type CreateNoteActionDeps = {
  fetch?: FetchLike;
};

export const readCreateNoteConfig = (container: HTMLElement): CreateNoteConfig => ({
  sourceKey: container.getAttribute("data-create-source-key") || "",
  target: container.getAttribute("data-create-target") || "",
  csrfToken: container.getAttribute("data-create-csrf") || "",
});

export const buildCreateNoteFetchRequest = (
  config: CreateNoteConfig
): CreateNoteFetchRequest => {
  const body = buildCreateNoteForm({
    key: config.sourceKey,
    target: config.target,
    token: config.csrfToken,
  });
  return {
    url: "/api/notes/create",
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

const showCreatedNote = (button: HTMLButtonElement, path: string | undefined): void => {
  button.textContent = "Created";
  if (!path?.startsWith("/n/") || /[\\\r\n]/.test(path)) return;
  const link = document.createElement("a");
  link.href = path;
  link.textContent = "Open note";
  const hadFocus = document.activeElement === button;
  button.replaceWith(link);
  if (hadFocus) link.focus();
};

export const initBrokenCreateActions = (deps: CreateNoteActionDeps = {}): void => {
  const containers = queryAllElements(document, "[data-create-note]", HTMLElement);
  if (containers.length === 0) {
    return;
  }
  const fetcher = deps.fetch || fetch;

  for (const container of containers) {
    const button = queryElement(container, "[data-create-button]", HTMLButtonElement);
    const statusEl = queryElement(container, "[data-create-status]", HTMLElement);
    if (!button || !statusEl) {
      continue;
    }

    const config = readCreateNoteConfig(container);
    const setStatus = (message: string, kind: CreateNoteStatusKind = ""): void => {
      setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
    };

    button.addEventListener("click", async () => {
      if (!config.sourceKey || !config.target || !config.csrfToken) {
        setStatus("Create action unavailable.", "is-error");
        return;
      }

      button.disabled = true;
      let created = false;
      setStatus("Creating note...");

      try {
        const request = buildCreateNoteFetchRequest(config);
        const resp = await fetcher(request.url, request.init);
        const payload = await readCreateResponsePayload(resp);
        const result = normalizeManagementResult(payload);
        if (!resp.ok || !result.ok) {
          setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }

        created = true;
        setStatus(result.message || "Created note.", "is-success");
        showCreatedNote(button, result.path);
      } catch {
        setStatus("Create failed.", "is-error");
      } finally {
        button.disabled = created;
      }
    });
  }
};

const readCreateResponsePayload = async (resp: Response): Promise<unknown> => {
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
