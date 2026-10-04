import {
  buildCaptureForm,
  managementMutationEventName,
  normalizeCaptureResponse,
  normalizeManagementErrorMessage,
  type CaptureResult,
} from "./api";
import { bindCaptureDraft } from "./capture-draft";
import { jsonPostRequest, readResponsePayload } from "./daily-brief-http";
import { queryElement } from "./dom";
import { setStatusText } from "./status";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type CaptureStatusKind = "" | "is-success" | "is-error" | "is-loading";

export type CaptureDeps = {
  fetch?: FetchLike;
};

// Disabling the input can blur it. Only subsequent interaction outside the
// capture controls cancels the normal return, including a click on plain text.
const captureFocusReturn = (input: HTMLTextAreaElement, submit: HTMLButtonElement): (() => void) => {
  const doc = input.ownerDocument;
  let movedAway = false;
  const observe = (event: Event): void => {
    if (event.target !== input && event.target !== submit) movedAway = true;
  };
  doc.addEventListener("focusin", observe, true);
  doc.addEventListener("pointerdown", observe, true);
  return () => {
    doc.removeEventListener("focusin", observe, true);
    doc.removeEventListener("pointerdown", observe, true);
    if (!movedAway && input.isConnected) input.focus();
  };
};

class Capture {
  private readonly saveDraft: () => void;
  private readonly csrfToken: string;

  constructor(
    private readonly root: HTMLElement,
    private readonly input: HTMLTextAreaElement,
    private readonly submit: HTMLButtonElement,
    private readonly status: HTMLElement,
    private readonly recent: HTMLElement,
    private readonly fetcher: FetchLike
  ) {
    this.csrfToken = root.dataset.captureCsrf || "";
    this.saveDraft = bindCaptureDraft(root, input);
    this.updateCount();
    input.addEventListener("input", () => this.updateCount());
    submit.addEventListener("click", () => { void this.run(); });
    input.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" || (!event.ctrlKey && !event.metaKey) || event.isComposing) {
        return;
      }
      event.preventDefault();
      void this.run();
    });
  }

  private updateCount(): void {
    const count = this.root.querySelector<HTMLElement>("[data-capture-count]");
    if (count && this.input.maxLength >= 0) {
      // Match the browser's maxlength units, including surrogate pairs.
      count.textContent = `${this.input.value.length} / ${this.input.maxLength} characters`;
    }
  }

  private setStatus(message: string, kind: CaptureStatusKind): void {
    setStatusText(this.status, message || " ", kind, ["is-success", "is-error", "is-loading"]);
  }

  private captured(result: CaptureResult): void {
    this.input.value = "";
    this.updateCount();
    this.saveDraft();
    if (result.detail) {
      const item = document.createElement("li");
      item.className = "capture-recent-item";
      item.textContent = result.detail;
      this.recent.prepend(item);
      while (this.recent.children.length > 5) {
        this.recent.lastElementChild?.remove();
      }
    }
    const link = queryElement(this.root, "[data-capture-view-inbox]", HTMLAnchorElement);
    if (link && result.path?.startsWith("/n/")) {
      link.href = result.path;
      link.hidden = false;
    }
    this.setStatus(result.message || `Captured to ${this.root.dataset.captureTarget}.`, "is-success");
    window.dispatchEvent(new CustomEvent(managementMutationEventName, { detail: result }));
  }

  private async run(): Promise<void> {
    if (this.submit.disabled) {
      return;
    }
    this.saveDraft();
    const text = this.input.value;
    if (text.trim() === "") {
      this.setStatus("Type something to capture.", "is-error");
      return;
    }
    if (this.csrfToken === "") {
      this.setStatus("Quick capture is unavailable.", "is-error");
      return;
    }
    this.input.disabled = true;
    this.submit.disabled = true;
    const returnFocus = captureFocusReturn(this.input, this.submit);
    this.setStatus("Capturing...", "is-loading");
    try {
      const body = buildCaptureForm({ text, token: this.csrfToken }).toString();
      const response = await this.fetcher("/api/capture", jsonPostRequest(this.csrfToken, body));
      const payload = await readResponsePayload(response);
      if (!response.ok) {
        this.setStatus(normalizeManagementErrorMessage(response.status, payload), "is-error");
        return;
      }
      const result = normalizeCaptureResponse(payload);
      if (!result.ok) {
        this.setStatus(result.message || "Capture failed.", "is-error");
        return;
      }
      this.captured(result);
    } catch {
      this.setStatus("Capture failed.", "is-error");
    } finally {
      this.input.disabled = false;
      this.submit.disabled = false;
      returnFocus();
    }
  }
}

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
  if (root.dataset.captureConfigured?.toLowerCase() !== "true") {
    input.disabled = true;
    submit.disabled = true;
    setStatusText(status,
      "Quick capture is not configured. Set capture.inbox_note and capture.heading in brainnotes.toml.",
      "is-error", ["is-success", "is-error", "is-loading"]);
    return;
  }
  new Capture(root, input, submit, status, recent, deps.fetch || fetch.bind(window));
};
