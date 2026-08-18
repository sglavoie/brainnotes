import {
  buildCaptureForm,
  managementMutationEventName,
  normalizeCaptureResponse,
  normalizeManagementErrorMessage,
} from "./api";
import { jsonPostRequest, readResponsePayload } from "./daily-brief-http";
import { queryElement } from "./dom";
import { setStatusText } from "./status";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type CaptureStatusKind = "" | "is-success" | "is-error" | "is-loading";

export type CaptureDeps = {
  fetch?: FetchLike;
};

const captureEndpoint = "/api/capture";
const recentLimit = 5;
const unconfiguredMessage =
  "Quick capture is not configured. Set capture.inbox_note and capture.heading in brainnotes.toml.";

export const initCapture = (deps: CaptureDeps = {}): void => {
  const root = queryElement(document, "[data-capture-root]", HTMLElement);
  if (!root) {
    return;
  }
  const input = queryElement(root, "[data-capture-input]", HTMLTextAreaElement);
  const submit = queryElement(root, "[data-capture-submit]", HTMLButtonElement);
  const status = queryElement(root, "[data-capture-status]", HTMLElement);
  const recent = queryElement(root, "[data-capture-recent]", HTMLElement);
  if (!input || !submit || !status || !recent) {
    return;
  }

  const csrfToken = root.getAttribute("data-capture-csrf") || "";
  const target = root.getAttribute("data-capture-target") || "";
  const configured =
    (root.getAttribute("data-capture-configured") || "").toLowerCase() === "true";

  const setStatus = (message: string, kind: CaptureStatusKind = ""): void => {
    setStatusText(status, message || " ", kind, ["is-success", "is-error", "is-loading"]);
  };

  if (!configured) {
    input.disabled = true;
    submit.disabled = true;
    setStatus(unconfiguredMessage, "is-error");
    return;
  }

  const fetcher = deps.fetch || fetch;

  const prependRecent = (line: string): void => {
    const item = document.createElement("li");
    item.className = "capture-recent-item";
    item.textContent = line;
    recent.prepend(item);
    while (recent.children.length > recentLimit) {
      recent.lastElementChild?.remove();
    }
  };

  const run = async (): Promise<void> => {
    const text = input.value;
    if (text.trim() === "") {
      setStatus("Type something to capture.", "is-error");
      return;
    }
    if (csrfToken === "") {
      setStatus("Quick capture is unavailable.", "is-error");
      return;
    }

    input.disabled = true;
    submit.disabled = true;
    setStatus("Capturing...", "is-loading");
    try {
      const body = buildCaptureForm({ text, token: csrfToken }).toString();
      const response = await fetcher(captureEndpoint, jsonPostRequest(csrfToken, body));
      const payload = await readResponsePayload(response);
      if (!response.ok) {
        setStatus(normalizeManagementErrorMessage(response.status, payload), "is-error");
        return;
      }
      const result = normalizeCaptureResponse(payload);
      if (!result.ok) {
        setStatus(result.message || "Capture failed.", "is-error");
        return;
      }

      input.value = "";
      if (result.detail) {
        prependRecent(result.detail);
      }
      setStatus(result.message || `Captured to ${target}.`, "is-success");
      window.dispatchEvent(new CustomEvent(managementMutationEventName, { detail: result }));
    } catch {
      setStatus("Capture failed.", "is-error");
    } finally {
      input.disabled = false;
      submit.disabled = false;
      input.focus();
    }
  };

  submit.addEventListener("click", () => {
    void run();
  });
  input.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" || (!event.ctrlKey && !event.metaKey) || submit.disabled) {
      return;
    }
    event.preventDefault();
    void run();
  });
};
