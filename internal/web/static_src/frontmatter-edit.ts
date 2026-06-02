import {
  buildFrontmatterSetForm,
  buildFrontmatterUnsetForm,
  normalizeManagementErrorMessage,
  type FrontmatterSetRequest,
  type FrontmatterUnsetRequest,
  type ManagementResult,
} from "./api";
import { queryAllElements, queryElement } from "./dom";
import { setStatusText } from "./status";

type FrontmatterStatusKind = "" | "is-success" | "is-error";
type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type FrontmatterOp = FrontmatterSetRequest["op"];

export type FrontmatterPanelConfig = {
  noteKey: string;
  csrfToken: string;
};

export type FrontmatterSetConfig = FrontmatterPanelConfig & {
  property: string;
  op: FrontmatterOp;
  value: string;
};

export type FrontmatterUnsetConfig = FrontmatterPanelConfig & {
  property: string;
};

export type FrontmatterFetchRequest = {
  url: string;
  init: RequestInit;
};

export type FrontmatterActionDeps = {
  fetch?: FetchLike;
};

export const readFrontmatterPanelConfig = (
  panel: HTMLElement
): FrontmatterPanelConfig => ({
  noteKey: panel.getAttribute("data-frontmatter-key") || "",
  csrfToken: panel.getAttribute("data-frontmatter-csrf") || "",
});

export const readFrontmatterSetConfig = (form: HTMLFormElement): FrontmatterSetConfig => {
  const panel = form.closest<HTMLElement>("[data-frontmatter-panel]");
  const panelConfig = panel ? readFrontmatterPanelConfig(panel) : { noteKey: "", csrfToken: "" };
  const formData = new FormData(form);
  return {
    ...panelConfig,
    property:
      form.getAttribute("data-frontmatter-property") ||
      String(formData.get("property") || "").trim(),
    op: normalizeFrontmatterOp(form.getAttribute("data-frontmatter-op")),
    value: String(formData.get("value") || ""),
  };
};

export const readFrontmatterUnsetConfig = (
  form: HTMLFormElement
): FrontmatterUnsetConfig => {
  const panel = form.closest<HTMLElement>("[data-frontmatter-panel]");
  const panelConfig = panel ? readFrontmatterPanelConfig(panel) : { noteKey: "", csrfToken: "" };
  return {
    ...panelConfig,
    property: form.getAttribute("data-frontmatter-property") || "",
  };
};

export const buildFrontmatterSetFetchRequest = (
  config: FrontmatterSetConfig
): FrontmatterFetchRequest => frontmatterFetchRequest(
  "/api/notes/frontmatter/set",
  config.csrfToken,
  buildFrontmatterSetForm({
    key: config.noteKey,
    token: config.csrfToken,
    property: config.property,
    op: config.op,
    value: config.value,
  })
);

export const buildFrontmatterUnsetFetchRequest = (
  config: FrontmatterUnsetConfig
): FrontmatterFetchRequest => frontmatterFetchRequest(
  "/api/notes/frontmatter/unset",
  config.csrfToken,
  buildFrontmatterUnsetForm({
    key: config.noteKey,
    token: config.csrfToken,
    property: config.property,
  })
);

export const initFrontmatterEditing = (deps: FrontmatterActionDeps = {}): void => {
  const panel = queryElement(document, "[data-frontmatter-panel]", HTMLElement);
  if (!panel) {
    return;
  }
  const statusEl = queryElement(panel, "[data-frontmatter-status]", HTMLElement);
  const fetcher = deps.fetch || fetch;
  const setStatus = (message: string, kind: FrontmatterStatusKind = ""): void => {
    setStatusText(statusEl, message || " ", kind, ["is-success", "is-error"]);
  };

  for (const form of queryAllElements(panel, "[data-frontmatter-set]", HTMLFormElement)) {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const config = readFrontmatterSetConfig(form);
      if (!validFrontmatterBaseConfig(config)) {
        setStatus("Frontmatter action unavailable.", "is-error");
        return;
      }

      setFormDisabled(form, true);
      setStatus("Updating frontmatter...");
      try {
        const request = buildFrontmatterSetFetchRequest(config);
        const resp = await fetcher(request.url, request.init);
        const payload = await readFrontmatterResponsePayload(resp);
        if (!resp.ok) {
          setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }
        const result = payload as ManagementResult;
        setStatus(result && result.ok && result.message ? result.message : "Updated frontmatter.", "is-success");
      } catch {
        setStatus("Frontmatter update failed.", "is-error");
      } finally {
        setFormDisabled(form, false);
      }
    });
  }

  for (const form of queryAllElements(panel, "[data-frontmatter-unset]", HTMLFormElement)) {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const config = readFrontmatterUnsetConfig(form);
      if (!validFrontmatterBaseConfig(config)) {
        setStatus("Frontmatter action unavailable.", "is-error");
        return;
      }

      setFormDisabled(form, true);
      setStatus("Updating frontmatter...");
      try {
        const request = buildFrontmatterUnsetFetchRequest(config);
        const resp = await fetcher(request.url, request.init);
        const payload = await readFrontmatterResponsePayload(resp);
        if (!resp.ok) {
          setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
          return;
        }
        const result = payload as ManagementResult;
        setStatus(result && result.ok && result.message ? result.message : "Unset frontmatter.", "is-success");
      } catch {
        setStatus("Frontmatter update failed.", "is-error");
      } finally {
        setFormDisabled(form, false);
      }
    });
  }
};

const normalizeFrontmatterOp = (value: string | null): FrontmatterOp => {
  if (value === "+=" || value === "-=") {
    return value;
  }
  return "=";
};

const validFrontmatterBaseConfig = (
  config: FrontmatterPanelConfig & { property: string }
): boolean =>
  config.noteKey.trim() !== "" &&
  config.csrfToken.trim() !== "" &&
  config.property.trim() !== "";

const frontmatterFetchRequest = (
  url: string,
  csrfToken: string,
  body: URLSearchParams
): FrontmatterFetchRequest => ({
  url,
  init: {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": csrfToken,
    },
    body: body.toString(),
  },
});

const setFormDisabled = (form: HTMLFormElement, disabled: boolean): void => {
  for (const control of queryAllElements(form, "button, input", HTMLElement)) {
    if (
      control instanceof HTMLButtonElement ||
      control instanceof HTMLInputElement
    ) {
      control.disabled = disabled;
    }
  }
};

const readFrontmatterResponsePayload = async (resp: Response): Promise<unknown> => {
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
