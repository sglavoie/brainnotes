import { addPinRefresh, readPinOutcome, unconfirmedPin, type PinOutcome } from "./pin-outcome";
import {
  buildNotePinRemoveFetchRequest,
  buildNotePinSetFetchRequest,
  type SearchResult,
} from "./api";
import { queryElement } from "./dom";

const overviewCSRFToken = (doc: Document): string | undefined =>
  queryElement(doc, "[data-overview-csrf]", HTMLElement)?.dataset.overviewCsrf;

export const renderOverviewPin = (doc: Document, result: SearchResult): {
  button: HTMLButtonElement;
  feedback: HTMLElement;
} => {
  const button = doc.createElement("button");
  button.type = "button";
  button.className = "bn-button pin-button overview-result-pin";
  button.setAttribute("data-pin-button", "");

  // The status stays rendered while empty: a live region that appears in the
  // same task as its text is often not announced. Only the spacing and the
  // Retry button follow whether there is anything to say.
  const feedback = doc.createElement("div");
  feedback.className = "overview-pin-feedback";
  feedback.dataset.empty = "";
  const status = doc.createElement("p");
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  const retry = doc.createElement("button");
  retry.type = "button";
  retry.className = "bn-button overview-bulk-button";
  retry.hidden = true;
  retry.textContent = "Retry pin";
  retry.setAttribute("aria-label", `Retry pin action for ${result.title || result.key}`);
  retry.addEventListener("click", () => button.click());
  feedback.append(status, retry);

  const applyState = (pinned: boolean): void => {
    const label = pinned ? "Unpin" : "Pin";
    button.setAttribute("aria-pressed", String(pinned));
    button.textContent = label;
    button.setAttribute("aria-label", `${label} ${result.title || result.key}`);
  };
  const hasMessage = (): boolean => status.textContent !== "";
  const report = (message: string): void => {
    status.textContent = message;
    feedback.toggleAttribute("data-empty", message === "");
    if (message === "") retry.hidden = true;
  };
  const reportFailure = (outcome: PinOutcome): void => {
    report(outcome.message);
    retry.hidden = outcome.kind === "unconfirmed";
    if (retry.hidden) addPinRefresh(status);
  };
  applyState(Boolean(result.pinned));

  button.addEventListener("click", async () => {
    const pinned = button.getAttribute("aria-pressed") === "true";
    // Read at click time: the page owns the token, not this result row.
    const token = overviewCSRFToken(doc);
    if (!token) {
      report("Pin action unavailable. Reload the page and try again.");
      return;
    }
    const retryFocused = doc.activeElement === retry;
    button.disabled = true;
    retry.disabled = true;
    if (hasMessage()) {
      retry.hidden = false;
      report(pinned ? "Unpinning note…" : "Pinning note…");
    }
    try {
      const request = pinned
        ? buildNotePinRemoveFetchRequest({ key: result.key, token })
        : buildNotePinSetFetchRequest({ key: result.key, token });
      const resp = await fetch(request.url, request.init);
      const outcome = await readPinOutcome(resp);
      if (outcome.kind === "success") {
        applyState(!pinned);
        report("");
      } else {
        reportFailure(outcome);
      }
    } catch {
      reportFailure(unconfirmedPin());
    } finally {
      button.disabled = false;
      retry.disabled = false;
      if (retryFocused && !hasMessage()) {
        button.focus();
      }
    }
  });
  return { button, feedback };
};
