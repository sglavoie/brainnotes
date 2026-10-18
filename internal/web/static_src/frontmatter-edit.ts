import { announceConfirmedWrite } from "./write-notice";
import {
  buildFrontmatterSetForm,
  buildFrontmatterUnsetForm,
  normalizeManagementErrorMessage,
  type FrontmatterSetRequest,
} from "./api";
import { asRecord } from "./api-internal";
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

  let pending = false;
  const forms = queryAllElements(panel, "[data-frontmatter-set], [data-frontmatter-unset]", HTMLFormElement);
  for (const form of forms) {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (pending) return;
      const action = readFrontmatterAction(form);
      if (!action) {
        setStatus("Frontmatter action unavailable.", "is-error");
        return;
      }
      pending = true;
      const restore = disablePanel(panel);
      setStatus("Updating frontmatter...");
      try {
        const resp = await fetcher(action.request.url, action.request.init);
        const outcome = await readFrontmatterOutcome(resp, action.message);
        if (outcome.kind === "is-success") announceConfirmedWrite();
        setStatus(outcome.message, outcome.kind);
        if (outcome.refresh && statusEl) addFrontmatterRefresh(statusEl, outcome.refresh);
      } catch {
        setStatus(unconfirmedMessage, "is-error");
        if (statusEl) addFrontmatterRefresh(statusEl, "Refresh to check");
      } finally {
        restore();
        pending = false;
      }
    });
  }
};

const readFrontmatterAction = (form: HTMLFormElement): { request: FrontmatterFetchRequest; message: string } | null => {
  if (form.hasAttribute("data-frontmatter-set")) {
    const config = readFrontmatterSetConfig(form);
    return validFrontmatterBaseConfig(config)
      ? { request: buildFrontmatterSetFetchRequest(config), message: "Updated frontmatter." } : null;
  }
  const config = readFrontmatterUnsetConfig(form);
  return validFrontmatterBaseConfig(config)
    ? { request: buildFrontmatterUnsetFetchRequest(config), message: "Unset frontmatter." } : null;
};

const unconfirmedMessage = "Change not confirmed. Refresh to check before retrying.";
type FrontmatterOutcome = { message: string; kind: FrontmatterStatusKind; refresh?: string };

const readFrontmatterOutcome = async (resp: Response, fallback: string): Promise<FrontmatterOutcome> => {
  const payload = await readFrontmatterResponsePayload(resp);
  if (!resp.ok) return { message: normalizeManagementErrorMessage(resp.status, payload), kind: "is-error" };
  const record = asRecord(payload);
  const message = typeof record?.message === "string" ? record.message : "";
  if (record?.ok === true) return { message: message || fallback, kind: "is-success", refresh: "Refresh properties" };
  if (record?.ok === false) return { message: message || "The property change was refused.", kind: "is-error" };
  return { message: unconfirmedMessage, kind: "is-error", refresh: "Refresh to check" };
};

const addFrontmatterRefresh = (status: HTMLElement, label: string): void => {
  const link = status.ownerDocument.createElement("a");
  link.href = window.location.href;
  link.textContent = label;
  link.addEventListener("click", (event) => {
    if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    window.location.reload();
  });
  status.append(" ", link);
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

const disablePanel = (panel: HTMLElement): (() => void) => {
  const controls = Array.from(panel.querySelectorAll<HTMLInputElement | HTMLButtonElement | HTMLSelectElement | HTMLTextAreaElement>(
    "button, input, select, textarea"
  ));
  const previous = controls.map((control) => control.disabled);
  controls.forEach((control) => { control.disabled = true; });
  return () => controls.forEach((control, index) => { control.disabled = previous[index]; });
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
