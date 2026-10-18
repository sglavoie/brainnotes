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

  const feedback = doc.createElement("div");
  feedback.className = "overview-pin-feedback";
  feedback.hidden = true;
  const status = doc.createElement("p");
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  const retry = doc.createElement("button");
  retry.type = "button";
  retry.className = "bn-button overview-bulk-button";
  retry.textContent = "Retry";
  retry.setAttribute("aria-label", `Retry pin action for ${result.title || result.key}`);
  retry.addEventListener("click", () => button.click());
  feedback.append(status, retry);

  const applyState = (pinned: boolean): void => {
    const label = pinned ? "Unpin" : "Pin";
    button.setAttribute("aria-pressed", String(pinned));
    button.textContent = label;
    button.setAttribute("aria-label", `${label} ${result.title || result.key}`);
  };
  const report = (message: string): void => {
    status.textContent = message;
    feedback.hidden = message === "";
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
    retry.hidden = false;
    if (!feedback.hidden) {
      report(pinned ? "Unpinning note..." : "Pinning note...");
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
      if (retryFocused && feedback.hidden) {
        button.focus();
      }
    }
  });
  return { button, feedback };
};
