import { historyEntryDetails } from "./history-entry-details";
import { setStatusText } from "./status";
import type { HistoryEntry, HistoryQuery, HistoryResult, HistoryWarning } from "./api";
import { appendHistoryPaths, formatHistoryTimestamp, historyFileCountLabel } from "./history-panel-view";
import type { HistoryWorkspaceElements } from "./history-workspace-elements";

/**
 * renderHistoryWarnings shows the entries the journal could not read.
 *
 * They are never filtered and never paged: an unreadable manifest has no
 * command and no paths, so no filter can be true of it, and a page that dropped
 * it would be a complete-looking account of an incomplete history. The count is
 * the server's own total rather than the length of this list, because the list
 * is capped and the total is not.
 */
export const renderHistoryWarnings = (
  container: HTMLElement,
  warnings: HistoryWarning[],
  warningCount: number
): void => {
  container.textContent = "";
  container.hidden = warningCount === 0;
  if (warningCount === 0) {
    return;
  }
  for (const warning of warnings) {
    const item = document.createElement("li");
    item.className = "history-warning";
    item.textContent = warning.id
      ? `${warning.id}: ${warning.message}`
      : warning.message;
    container.appendChild(item);
  }
  if (warningCount > warnings.length) {
    const more = document.createElement("li");
    more.className = "history-warning";
    more.textContent = `${warningCount - warnings.length} more unreadable entries are not listed here.`;
    container.appendChild(more);
  }
};

const historyClassSummary = (entry: HistoryEntry): string =>
  entry.classes
    .map((count) => `${count.label || count.class} ${count.count}`)
    .join(", ");

const historyEntryCard = (
  entry: HistoryEntry,
  onRestore: (entryID: string, trigger: HTMLButtonElement) => void
): HTMLLIElement => {
  const item = document.createElement("li");
  item.className = "history-entry";
  item.setAttribute("data-history-entry", entry.id);

  const head = document.createElement("div");
  head.className = "history-entry-head";

  const origin = document.createElement("span");
  origin.className = "history-origin";
  origin.setAttribute("data-history-entry-origin", entry.origin);
  origin.textContent = entry.origin_label || entry.origin;
  head.appendChild(origin);

  const title = document.createElement("strong");
  title.className = "history-entry-title";
  title.textContent = entry.summary || "Recorded change";
  head.prepend(title);

  const meta = document.createElement("p");
  meta.className = "history-meta";
  const classes = historyClassSummary(entry);
  const parts = [
    formatHistoryTimestamp(entry.timestamp),
    historyFileCountLabel(entry.file_count || entry.files.length),
  ];
  meta.textContent = classes ? `${parts.join(" - ")} - ${classes}` : parts.join(" - ");

  const restore = document.createElement("button");
  restore.className = "bn-button history-button";
  restore.type = "button";
  restore.setAttribute("data-history-preview-entry", entry.id);
  restore.setAttribute("aria-label", `Preview restoring ${entry.command || entry.id}`);
  restore.textContent = "Preview restore";
  restore.addEventListener("click", () => {
    onRestore(entry.id, restore);
  });

  item.appendChild(head);
  item.appendChild(meta);
  appendHistoryPaths(item, entry);
  item.appendChild(historyEntryDetails(entry));
  item.appendChild(restore);
  return item;
};

export const renderHistoryEntryList = (
  container: HTMLElement,
  entries: HistoryEntry[],
  onRestore: (entryID: string, trigger: HTMLButtonElement) => void
): void => {
  container.textContent = "";
  for (const entry of entries) {
    container.appendChild(historyEntryCard(entry, onRestore));
  }
};

/** historyRangeLabel names the window, not just the page, so a filtered view
 * cannot be mistaken for the whole journal. */
export const historyRangeLabel = (offset: number, shown: number, total: number): string => {
  if (total === 0) {
    return "0 entries";
  }
  if (shown === 0) {
    return `No entries at this position of ${total}.`;
  }
  return `${offset + 1}-${offset + shown} of ${total}`;
};

export const updateHistoryPager = (
  elements: HistoryWorkspaceElements,
  result: HistoryResult,
  shown: number
): void => {
  const offset = result.offset || 0;
  const total = result.total || 0;
  if (elements.range) {
    elements.range.textContent = historyRangeLabel(offset, shown, total);
  }
  if (elements.previous) {
    elements.previous.disabled = offset <= 0;
  }
  if (elements.next) {
    elements.next.disabled = !result.has_more;
  }
};

export const renderHistoryEmptyStatus = (
  elements: HistoryWorkspaceElements, query: HistoryQuery, warningCount: number
): void => {
  const filtered = Boolean(query.path || query.origins?.length || query.classes?.length);
  const unfiltered = warningCount > 0
    ? "No readable changes to show. See the history warnings below."
    : "No changes have been recorded yet. Changes made through Brain Notes will appear here.";
  setStatusText(elements.status,
    filtered ? "No recorded change matches these filters." : unfiltered, "is-empty");
  if (!filtered) return;
  const clear = elements.status.ownerDocument.createElement("button");
  clear.type = "button";
  clear.className = "bn-button history-button";
  clear.textContent = "Clear filters";
  clear.addEventListener("click", () => {
    elements.clearFilters?.focus();
    elements.clearFilters?.click();
  });
  elements.status.append(" ", clear);
};
