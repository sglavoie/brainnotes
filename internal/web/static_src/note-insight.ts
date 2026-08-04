import {
  buildNoteInsightForm,
  normalizeManagementErrorMessage,
  normalizeNoteInsightResponse,
} from "./api";
import { queryElement } from "./dom";
import { setStatusText } from "./status";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type ConfirmLike = (message: string) => boolean;
type ClipboardWriter = (text: string) => Promise<void>;

export type NoteInsightDeps = {
  fetch?: FetchLike;
  confirm?: ConfirmLike;
  writeClipboard?: ClipboardWriter;
};

const disclosureMessage = [
  "Ask AI sends this note, related-note context, and your optional prompt to your configured [agent].command.",
  "BrainNotes does not store the prompt or AI output. Continue?",
].join("\n");

const postRequest = (csrfToken: string, body: string): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
    "X-BN-CSRF": csrfToken,
  },
  body,
});

const readResponsePayload = async (response: Response): Promise<unknown> => {
  try {
    if ((response.headers.get("Content-Type") || "").includes("application/json")) {
      return await response.json();
    }
    return await response.text();
  } catch {
    return "";
  }
};

export const initNoteInsight = (deps: NoteInsightDeps = {}): void => {
  const root = queryElement(document, "[data-note-insight]", HTMLElement);
  if (!root) {
    return;
  }

  const endpoint = root.getAttribute("data-note-insight-endpoint") || "/api/note/insight";
  const key = root.getAttribute("data-note-insight-key") || "";
  const csrfToken = root.getAttribute("data-note-insight-csrf") || "";
  const enabled = (root.getAttribute("data-note-insight-enabled") || "").toLowerCase() === "true";
  const prompt = queryElement(root, "[data-note-insight-prompt]", HTMLTextAreaElement);
  const submit = queryElement(root, "[data-note-insight-submit]", HTMLButtonElement);
  const status = queryElement(root, "[data-note-insight-status]", HTMLElement);
  const result = queryElement(root, "[data-note-insight-result]", HTMLElement);
  const body = queryElement(root, "[data-note-insight-body]", HTMLElement);
  const copy = queryElement(root, "[data-note-insight-copy]", HTMLButtonElement);
  if (!prompt || !submit || !status || !result || !body || !copy) {
    return;
  }

  const fetcher = deps.fetch || fetch;
  const confirmer = deps.confirm || ((message: string) => window.confirm(message));
  const clipboardWrite =
    deps.writeClipboard ||
    (navigator.clipboard && typeof navigator.clipboard.writeText === "function"
      ? (text: string) => navigator.clipboard.writeText(text)
      : undefined);
  let disclosureAccepted = false;
  let output = "";

  const setStatus = (message: string, kind: "" | "is-success" | "is-error" | "is-loading" = ""): void => {
    setStatusText(status, message || " ", kind, ["is-success", "is-error", "is-loading"]);
  };

  const setBusy = (busy: boolean): void => {
    root.setAttribute("aria-busy", String(busy));
    prompt.disabled = busy || !enabled;
    submit.disabled = busy || !enabled;
    copy.disabled = busy || output.trim() === "";
  };

  const run = async (): Promise<void> => {
    if (!enabled || key === "" || csrfToken === "") {
      setStatus("Ask AI is unavailable.", "is-error");
      return;
    }
    if (!disclosureAccepted) {
      disclosureAccepted = confirmer(disclosureMessage);
      if (!disclosureAccepted) {
        setStatus("Ask AI canceled.");
        return;
      }
    }

    setBusy(true);
    setStatus("Generating response...", "is-loading");
    try {
      const requestBody = buildNoteInsightForm({
        key,
        prompt: prompt.value,
        token: csrfToken,
      }).toString();
      const response = await fetcher(endpoint, postRequest(csrfToken, requestBody));
      const payload = await readResponsePayload(response);
      if (!response.ok) {
        setStatus(normalizeManagementErrorMessage(response.status, payload), "is-error");
        return;
      }
      const normalized = normalizeNoteInsightResponse(payload);
      if (!normalized.ok || !normalized.enabled) {
        setStatus(normalized.message || "Ask AI is unavailable.", "is-error");
        return;
      }
      if (normalized.key && normalized.key !== key) {
        setStatus("Note changed while the response was running. Try again.", "is-error");
        return;
      }

      output = normalized.output;
      body.innerHTML = normalized.rendered_html;
      result.hidden = false;
      setStatus("Response ready.", "is-success");
    } catch {
      setStatus("Ask AI failed.", "is-error");
    } finally {
      setBusy(false);
    }
  };

  submit.addEventListener("click", () => {
    void run();
  });
  prompt.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" || (!event.ctrlKey && !event.metaKey) || submit.disabled) {
      return;
    }
    event.preventDefault();
    void run();
  });
  copy.addEventListener("click", async () => {
    if (output.trim() === "") {
      setStatus("Nothing to copy.", "is-error");
      return;
    }
    if (!clipboardWrite) {
      setStatus("Clipboard unavailable.", "is-error");
      return;
    }
    copy.disabled = true;
    try {
      await clipboardWrite(output);
      setStatus("Response markdown copied.", "is-success");
    } catch {
      setStatus("Copy failed.", "is-error");
    } finally {
      copy.disabled = false;
    }
  });
};
