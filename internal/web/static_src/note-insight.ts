import { confirmAction, type ConfirmAction } from "./action-confirm";
import {
  buildNoteInsightForm,
  normalizeManagementErrorMessage,
  normalizeNoteInsightResponse,
} from "./api";
import { queryElement } from "./dom";
import { captureFocusReturn, focusedWithin } from "./focus-return";
import { copyWithFallback } from "./clipboard";
import { setStatusText } from "./status";
import { isUnreachableError, serverUnreachableMessage } from "./request-outcome";
import { isSubmitChord } from "./submit-chord";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type ConfirmLike = ConfirmAction;
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

const describeClientError = (error: unknown): string => {
  if (isUnreachableError(error)) {
    return `Ask AI failed. ${serverUnreachableMessage}`;
  }
  if (error instanceof Error && error.message.trim() !== "") {
    return `Ask AI failed: ${error.message}`;
  }
  return "Ask AI failed.";
};

type InsightElements = {
  root: HTMLElement;
  prompt: HTMLTextAreaElement;
  submit: HTMLButtonElement;
  status: HTMLElement;
  result: HTMLElement;
  body: HTMLElement;
  copy: HTMLButtonElement;
};

const resolveInsightElements = (): InsightElements | null => {
  const root = queryElement(document, "[data-note-insight]", HTMLElement);
  if (!root) return null;
  const prompt = queryElement(root, "[data-note-insight-prompt]", HTMLTextAreaElement);
  const submit = queryElement(root, "[data-note-insight-submit]", HTMLButtonElement);
  const status = queryElement(root, "[data-note-insight-status]", HTMLElement);
  const result = queryElement(root, "[data-note-insight-result]", HTMLElement);
  const body = queryElement(root, "[data-note-insight-body]", HTMLElement);
  const copy = queryElement(root, "[data-note-insight-copy]", HTMLButtonElement);
  return prompt && submit && status && result && body && copy
    ? { root, prompt, submit, status, result, body, copy } : null;
};

class NoteInsight {
  private readonly endpoint: string;
  private readonly key: string;
  private readonly csrfToken: string;
  private readonly enabled: boolean;
  private readonly fetcher: FetchLike;
  private readonly confirmer: ConfirmLike;
  private readonly clipboardWrite: ClipboardWriter | undefined;
  private disclosureAccepted = false;
  private output = "";

  constructor(private readonly els: InsightElements, deps: NoteInsightDeps) {
    this.endpoint = els.root.dataset.noteInsightEndpoint || "/api/note/insight";
    this.key = els.root.dataset.noteInsightKey || "";
    this.csrfToken = els.root.dataset.noteInsightCsrf || "";
    this.enabled = els.root.dataset.noteInsightEnabled?.toLowerCase() === "true";
    this.fetcher = deps.fetch || fetch;
    this.confirmer = deps.confirm || ((message) => confirmAction({ title: "Send note to Ask AI?", description: message, confirmLabel: "Send note", danger: false }, els.root.ownerDocument, els.submit));
    this.clipboardWrite = deps.writeClipboard || (navigator.clipboard?.writeText
      ? (text) => navigator.clipboard.writeText(text) : undefined);
    els.submit.addEventListener("click", () => { void this.run(); });
    els.prompt.addEventListener("keydown", (event) => {
      if (!isSubmitChord(event) || els.submit.disabled) return;
      event.preventDefault();
      void this.run();
    });
    els.copy.addEventListener("click", () => { void this.copy(); });
  }

  private setStatus(message: string, kind: "" | "is-success" | "is-error" | "is-loading" = ""): void {
    setStatusText(this.els.status, message || " ", kind, ["is-success", "is-error", "is-loading"]);
  }

  private setBusy(busy: boolean): void {
    this.els.root.setAttribute("aria-busy", String(busy));
    this.els.prompt.disabled = busy || !this.enabled;
    this.els.submit.disabled = busy || !this.enabled;
    this.els.copy.disabled = busy || this.output.trim() === "";
  }

  private async canRun(): Promise<boolean> {
    if (!this.enabled || !this.key || !this.csrfToken) {
      this.setStatus("Ask AI is unavailable.", "is-error");
      return false;
    }
    if (!this.disclosureAccepted) this.disclosureAccepted = await this.confirmer(disclosureMessage);
    if (!this.disclosureAccepted) this.setStatus("Ask AI canceled.");
    return this.disclosureAccepted;
  }

  private showResponse(payload: unknown): void {
    const normalized = normalizeNoteInsightResponse(payload);
    if (!normalized.ok || !normalized.enabled) {
      this.setStatus(normalized.message || "Ask AI is unavailable.", "is-error");
      return;
    }
    if (normalized.key && normalized.key !== this.key) {
      this.setStatus("Note changed while the response was running. Try again.", "is-error");
      return;
    }
    this.output = normalized.output;
    this.els.result.querySelector("[data-copy-fallback]")?.remove();
    this.els.body.innerHTML = normalized.rendered_html;
    this.els.result.hidden = false;
    this.setStatus("Response ready.", "is-success");
  }

  private async run(): Promise<void> {
    if (this.els.submit.disabled) return;
    const focused = focusedWithin(this.els.root);
    this.setBusy(true);
    const accepted = await this.canRun();
    // Watched from here, after any disclosure dialog has handed focus back.
    const returnFocus = captureFocusReturn(focused);
    if (!accepted) { this.setBusy(false); returnFocus(); return; }
    this.setStatus("Generating response…", "is-loading");
    try {
      const requestBody = buildNoteInsightForm({
        key: this.key, prompt: this.els.prompt.value, token: this.csrfToken,
      }).toString();
      const response = await this.fetcher(this.endpoint, postRequest(this.csrfToken, requestBody));
      const payload = await readResponsePayload(response);
      if (!response.ok) {
        this.setStatus(normalizeManagementErrorMessage(response.status, payload), "is-error");
        return;
      }
      this.showResponse(payload);
    } catch (error) {
      this.setStatus(describeClientError(error), "is-error");
    } finally {
      this.setBusy(false);
      returnFocus();
    }
  }

  private async copy(): Promise<void> {
    if (!this.output.trim()) {
      this.setStatus("Nothing to copy.", "is-error");
      return;
    }
    this.els.copy.disabled = true;
    try {
      const feedback = await copyWithFallback(this.els.result,
        { text: this.output, label: "Response markdown" }, this.clipboardWrite);
      this.setStatus(feedback.message, feedback.kind);
    } finally {
      this.els.copy.disabled = false;
    }
  }
}

export const initNoteInsight = (deps: NoteInsightDeps = {}): void => {
  const els = resolveInsightElements();
  if (els) new NoteInsight(els, deps);
};
