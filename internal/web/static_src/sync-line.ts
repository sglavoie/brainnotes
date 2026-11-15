import { fetchSyncStatus, type FetchLike, type SyncOutcome } from "./sync-requests";
import { syncLineSummary } from "./sync-status-view";
import type { SyncStatusResult } from "./api";
import { bindSyncRefresh } from "./sync-line-refresh";

// The one line every page carries about synchronization.
//
// It replaced a sentence the navigation rendered from the replica status file,
// which could only ever say how old a published copy was. This asks the status
// route instead, so the same element says "3 changes are waiting for a decision"
// on a vault that synchronizes and still says how stale a replica's copy is on one
// that publishes a copy — the route answers both, because those are the honest
// answers for the two modes.
//
// Pending work gets a bounded series of follow-up reads while the page is
// visible. Confirmed mutations and returning to a tab start a fresh series.

export type SyncLineDeps = {
  doc?: Document;
  fetch?: FetchLike;
};

// Refusal codes are the answers that mean "this server does not offer this",
// rather than "this server failed to answer". A mode whose guard refuses the route
// leaves the line empty: there is nothing wrong, and a nav that said so on every
// page would be reporting a decision as a fault.
const renderLine = (line: HTMLElement, status: SyncStatusResult, doc: Document): void => {
  if (!status.needs_person) return;
  line.classList.add("is-attention");
  const count = status.peer.conflicts;
  const incoming = status.headline === "conflict" && count > 0;
  const hasReview = status.review_path && count > 0;
  if (!incoming) line.textContent = `Sync: ${syncLineSummary(status)}`;
  if (!incoming && !hasReview) return;

  renderReview(line, status, doc, incoming ? count : 0);
};

const renderReview = (line: HTMLElement, status: SyncStatusResult, doc: Document, count: number): void => {
  const review = doc.createElement(status.review_path ? "a" : "span");
  if (status.review_path) {
    review.className = "sync-line-review";
    (review as HTMLAnchorElement).href = status.review_path;
  }
  review.textContent = count ? `${count} incoming` : "Review incoming changes";
  if (count) {
    const label = `Review ${count} incoming change${count === 1 ? "" : "s"}`;
    line.classList.add("is-count");
    line.ariaLabel = line.title = review.ariaLabel = label;
    review.classList.add("sync-line-count");
  } else {
    review.title = syncLineSummary(status);
    line.append(" ");
  }
  line.append(review);
};

/**
 * Reads the status once and writes the line. Exported for the panel's reload.
 *
 * The line is a live region and follow-up reads usually return the same answer,
 * so an unchanged rendering leaves the DOM alone: rewriting identical text would
 * have a screen reader announce it again after every read.
 */
export const renderSyncLine = (
  line: HTMLElement,
  outcome: SyncOutcome<SyncStatusResult>,
  doc: Document
): void => {
  const next = line.cloneNode(false) as HTMLElement;
  next.className = "sync-line";
  next.ariaLabel = null;
  next.removeAttribute("title");
  if (!outcome.ok) {
    if (!["forbidden", "not_found"].includes(outcome.code)) {
      next.textContent = `Synchronization status is unavailable. ${outcome.message}`;
      next.classList.add("is-attention");
    }
  } else {
    renderLine(next, outcome.result, doc);
  }
  if (next.isEqualNode(line)) {
    return;
  }
  line.className = next.className;
  line.ariaLabel = next.ariaLabel;
  if (next.title) line.title = next.title;
  else line.removeAttribute("title");
  line.replaceChildren(...next.childNodes);
};

const bindings = new WeakMap<Document, () => void>();

export const initSyncLine = (deps: SyncLineDeps = {}): void => {
  const doc = deps.doc ?? document;
  bindings.get(doc)?.();
  const fetcher = deps.fetch ?? fetch;
  const line = doc.querySelector<HTMLElement>("[data-sync-line]");
  const endpoint = line?.dataset.syncStatusEndpoint;
  if (!line || !endpoint) {
    return;
  }
  bindings.set(doc, bindSyncRefresh({
    doc, line,
    read: () => fetchSyncStatus(fetcher, endpoint),
    render: (outcome) => renderSyncLine(line, outcome, doc),
  }));
};
