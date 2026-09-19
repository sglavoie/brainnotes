import { queryElement } from "./dom";
import { fetchSyncStatus, type FetchLike, type SyncOutcome } from "./sync-requests";
import { syncLineSummary, syncNeedsPerson } from "./sync-status-view";
import type { SyncStatusResult } from "./api";

// The one line every page carries about synchronization.
//
// It replaced a sentence the navigation rendered from the replica status file,
// which could only ever say how old a published copy was. This asks the status
// route instead, so the same element says "3 changes are waiting for a decision"
// on a vault that synchronizes and still says how stale a replica's copy is on one
// that publishes a copy — the route answers both, because those are the honest
// answers for the two modes.
//
// It is deliberately not a poller. A timer on every page would ask a question
// nobody is looking at, and the states this reports move when somebody saves,
// exports or acknowledges rather than on the second. It reads once and reads again
// when the tab is looked at again, which is when the answer is being read.

export type SyncLineDeps = {
  doc?: Document;
  fetch?: FetchLike;
};

// refusalCodes are the answers that mean "this server does not offer this",
// rather than "this server failed to answer". A mode whose guard refuses the route
// leaves the line empty: there is nothing wrong, and a nav that said so on every
// page would be reporting a decision as a fault.
const refusalCodes = ["forbidden", "not_found"];

const renderLine = (line: HTMLElement, status: SyncStatusResult, doc: Document): void => {
  line.textContent = syncLineSummary(status);
  line.classList.toggle("is-attention", syncNeedsPerson(status));
  if (status.review_path === "") {
    return;
  }
  const review = doc.createElement("a");
  review.className = "sync-line-review";
  review.href = status.review_path;
  review.textContent = "Review";
  line.append(" ", review);
};

const renderFailure = (line: HTMLElement, outcome: { code: string; message: string }): void => {
  line.classList.remove("is-attention");
  if (refusalCodes.includes(outcome.code)) {
    line.textContent = "";
    return;
  }
  line.textContent = `Synchronization status is unavailable. ${outcome.message}`;
  line.classList.add("is-attention");
};

/** Reads the status once and writes the line. Exported for the panel's reload. */
export const renderSyncLine = (
  line: HTMLElement,
  outcome: SyncOutcome<SyncStatusResult>,
  doc: Document
): void => {
  line.textContent = "";
  if (!outcome.ok) {
    renderFailure(line, outcome);
    return;
  }
  renderLine(line, outcome.result, doc);
};

export const initSyncLine = (deps: SyncLineDeps = {}): void => {
  const doc = deps.doc ?? document;
  const fetcher = deps.fetch ?? ((input, init) => window.fetch(input, init));
  const line = queryElement(doc, "[data-sync-line]", HTMLElement);
  const endpoint = line?.getAttribute("data-sync-status-endpoint") ?? "";
  if (!line || endpoint === "") {
    return;
  }
  const read = async (): Promise<void> => {
    renderSyncLine(line, await fetchSyncStatus(fetcher, endpoint), doc);
  };
  void read();
  doc.addEventListener("visibilitychange", () => {
    if (doc.visibilityState === "visible") {
      void read();
    }
  });
};
